package zwibserve

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/golang-jwt/jwt/v4"
)

// Default maximum message size
const maxMessageSize = 100 * 1024

// nextClientNumber is just a number identifing the client for logging.
var nextClientNumber int64

type client struct {
	id              string
	ws              *websocket.Conn
	protocolVersion uint16
	docID           string

	// last document range sent in an append message
	lastEnd uint64

	// for tokens
	userID          string
	writePermission bool
	adminPermission bool

	db  DocumentDB
	hub *hub

	// A single thread writes to the socket. When another thread wants to
	// send, it appends to the queue and signals the condition variable.
	// To close the socket, we set closed = true and signal the condition.
	wakeup *sync.Cond
	mutex  sync.Mutex
	closed bool

	// queued messages to send, other than key-information
	queued [][]byte

	// queued keys to update to client
	keys []Key

	// maximum message size requested by client.
	maxSize int
}

func idstr(id int64) string {
	return fmt.Sprintf("%x", id)
}

// Takes over the connection and runs the client, Responsible for closing the socket.
func runClient(hub *hub, db DocumentDB, ws *websocket.Conn) {
	c := &client{
		ws:      ws,
		db:      db,
		hub:     hub,
		id:      idstr(atomic.AddInt64(&nextClientNumber, 1)),
		maxSize: maxMessageSize,
	}

	c.wakeup = sync.NewCond(&c.mutex)

	go c.writeThread()

	// wait up to 30 seconds for init message
	message, err := readMessageWithTimeout(c.ws, 30*time.Second)
	if err != nil {
		ws.Close()
		log.Printf("%s: error waiting for init message: %v", c.id, err)
		return
	}

	if len(message) < 2 {
		ws.Close()
		log.Printf("Received message is too short. Close connection")
		return
	}

	if message[0] == serverIdentificationMessageType {
		hub.swarm.HandleIncomingConnection(ws, message)
		return
	}

	defer func() {
		c.mutex.Lock()
		c.closed = true
		c.wakeup.Signal()
		c.mutex.Unlock()
	}()

	if !c.processInitMessage(message) {
		return
	}

	log.Printf("Client %s connected", c.id)

	hub.addClient(c.docID, c)
	defer hub.RemoveClient(c.docID, c.id)

	sessionKeys, _ := c.db.GetDocumentKeys(c.docID)
	c.notifyKeysUpdated(c.hub.getClientKeys(c.docID))
	c.notifyKeysUpdated(sessionKeys)
	sessionKeys = nil

	for {
		message, err = readMessage(c.ws)
		if err != nil {
			log.Printf("client for %s disconnected", c.docID)
			break
		}

		if message[0] == 0x02 || message[0] == 0x05 {
			if !c.processAppend(message) {
				break
			}
		} else if message[0] == 0x03 {
			if !c.processSetKey(message) {
				break
			}
		} else if message[0] == 0x04 {
			if !c.processBroadcast(message) {
				break
			}
		} else {
			log.Printf("client %v sent unexpected message type %v", c.id, message[0])
		}
	}
}

func (c *client) writeThread() {
	defer func() {
		err := recover()
		if err != nil {
			log.Printf("Handling panic: %v", err)
			c.ws.Close()
		}
	}()

	closed := false
	for !closed {
		c.mutex.Lock()

		for len(c.queued) == 0 && len(c.keys) == 0 && !c.closed {
			c.wakeup.Wait()
		}

		messages := c.queued
		keys := c.keys
		closed = c.closed
		c.queued = nil
		c.keys = nil
		c.mutex.Unlock()

		// send all the queued messages.
		for _, message := range messages {
			c.sendMessage(message)
		}

		if len(keys) > 0 {
			info := keyInformationMessage{
				MessageType: keyInformationMessageType,
			}
			for _, k := range keys {
				info.Keys = append(info.Keys, keyInformation{
					Version:     uint32(k.Version),
					NameLength:  uint32(len(k.Name)),
					Name:        k.Name,
					ValueLength: uint32(len(k.Value)),
					Value:       k.Value,
				})
			}
			c.sendMessage(encode(nil, info))
		}
	}
	c.ws.Close()
}

// Reads a complete message, taking into account the MORE byte to
// join continuation messages together.
func readMessage(conn *websocket.Conn) ([]uint8, error) {
	// only to be called from runClient
	var buffer []byte
	for {
		_, p, err := conn.ReadMessage()
		if err != nil {
			return nil, err
		}
		if len(p) < 2 {
			log.Panicf("message too short")
		}

		if buffer == nil {
			// first message
			buffer = append(buffer, p...)
		} else if p[0] == continuationMessageType {
			// continuation message
			buffer = append(buffer, p[2:]...)
		} else {
			log.Panicf("expected continuation message")
		}

		if p[1] == 0 {
			break
		}
	}
	return buffer, nil
}

func readMessageWithTimeout(conn *websocket.Conn, timeout time.Duration) ([]uint8, error) {
	// wait up to 'timeout' seconds for init message
	conn.SetReadDeadline(time.Now().Add(timeout))
	msg, err := readMessage(conn)
	conn.SetReadDeadline(time.Time{})
	return msg, err
}

func sendMessage(conn *websocket.Conn, data []byte, maxSize int) {
	send := len(data)
	if send > maxSize {
		send = maxSize
		data[1] = 1
	} else {
		data[1] = 0
	}

	// send first part
	err := conn.WriteMessage(websocket.BinaryMessage, data[:send])
	if err != nil {
		log.Printf("Got ERROR writing to socket: %v", err)
	}
	data = data[send:]
	for len(data) > 0 {
		log.Printf("Sent %d/%d bytes", send, len(data))
		writer, err := conn.NextWriter(websocket.BinaryMessage)
		if err != nil {
			log.Panic(err)
		}

		send := len(data)
		more := byte(0)
		if send > maxSize-2 {
			send = maxSize - 2
			more = 1
		}

		writer.Write([]byte{0xff, more})
		writer.Write(data[:send])
		writer.Close()
		data = data[send:]
	}
}

// Writes a complete message, respecting maximum message size and breaking it into chunks
// if necessary. MUST ONLY BE CALLED BY WRITETHREAD
func (c *client) sendMessage(data []byte) {
	sendMessage(c.ws, data, c.maxSize)
}

func (c *client) enqueue(message interface{}) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	c.queued = append(c.queued, encode(nil, message))
	c.wakeup.Signal()
}

type errorCode uint16

const (
	errorUnspecified   errorCode = 0
	errorDoesNotExist  errorCode = 1
	errorInvalidOffset errorCode = 3
	errorAccessDenied  errorCode = 4
)

var errorStrings = []string{
	"unspecified",
	"does not exist",
	"already exists",
	"invalid offset",
	"access denied",
}

func (c *client) enqueueError(code errorCode, text string) {
	log.Printf("Client %v error: %s", c.id, text)
	if text == "" && int(code) < len(errorStrings) {
		text = errorStrings[code]
	}
	c.enqueue(errorMessage{
		MessageType: 0x80,
		ErrorCode:   uint16(code),
		Description: text,
	})
}

func (c *client) enqueueAppend(data []byte, offset uint64) {
	if offset < c.lastEnd {
		return
	}

	c.lastEnd = offset + uint64(len(data))
	//log.Printf("Append: %d bytes at offset %d", len(data), offset)

	if c.protocolVersion < 3 {
		c.enqueue(appendMessageV2{
			MessageType: appendV2MessageType,
			Offset:      offset,
			Data:        data,
		})
		return
	}

	c.enqueue(appendMessage{
		MessageType: appendMessageType,
		Generation:  0, // Future feature
		Offset:      offset,
		Data:        data,
	})
}

func (c *client) enqueueBroadcast(data []byte) {
	c.enqueue(broadcastMessage{
		MessageType: 0x04,
		DataLength:  uint32(len(data)),
		Data:        data,
	})
}

func (c *client) enqueueAckNack(ack uint16, length uint64) {

	c.enqueue(ackNackMessage{
		MessageType: 0x81,
		Ack:         ack,
		Offset:      length,
	})
}

func (c *client) enqueueSetKeyAckNack(ack bool, requestID uint16) {
	var Ack uint16
	if ack {
		log.Printf("Client %v << KEY ACK", c.id)
		Ack = 0x01
	} else {
		log.Printf("Client %v << KEY NACK", c.id)
		Ack = 0x00
	}

	c.enqueue(setKeyAckNackMessage{
		MessageType: 0x83,
		Ack:         Ack,
		RequestID:   requestID,
	})

}

// Decode both the V2 and V3 version into the V3 version.
func decodeInitMessage(m *initMessage, data []uint8) error {
	if len(data) > 4 && data[0] == initMessageType &&
		data[2] == 0 && data[3] == 2 {
		// protocol version 2
		var m2 initMessageV2
		err := decode(&m2, data)
		if err != nil {
			return err
		}

		m.MessageType = m2.MessageType
		m.More = m2.More
		m.ProtocolVersion = m2.ProtocolVersion
		m.MaxMessageSize = m2.MaxMessageSize
		m.CreationMode = m2.CreationMode
		m.Offset = m2.Offset
		m.DocIDLength = uint32(m2.DocIDLength)
		m.DocID = m2.DocID
		m.Data = m2.Data
		return nil
	}

	return decode(m, data)
}

// decode both append and append_v2 into the append message
func decodeAppendMessage(m *appendMessage, data []uint8) error {
	if data[0] == appendV2MessageType {
		var m2 appendMessageV2
		err := decode(&m2, data)
		if err != nil {
			return err
		}
		m.MessageType = m2.MessageType
		m.More = m2.More
		m.Offset = m2.Offset
		m.Data = m2.Data
		return nil
	}
	return decode(m, data)
}

func (c *client) processInitMessage(data []uint8) bool {
	var m initMessage
	err := decodeInitMessage(&m, data)
	if err != nil {
		log.Printf("Client %v: %v", c.id, err)
		return false
	}

	if m.MessageType != 0x01 {
		log.Printf("Client %v: ERROR Expected INIT message", c.id)
		return false
	}

	if m.ProtocolVersion != 2 && m.ProtocolVersion != 3 {
		log.Printf("Client %v: protocol versiom must be 2", c.id)
		return false
	}
	c.protocolVersion = m.ProtocolVersion

	if m.MaxMessageSize != 0 {
		c.maxSize = int(m.MaxMessageSize)
	}

	errorCodeOnMissing := errorDoesNotExist

	// check if its a token
	realDocID, userID, permissions, err := c.db.GetToken(m.DocID)

	if err == ErrMissing && c.hub.jwtKey != "" {
		// interpret as JWT token
		realDocID, userID, permissions, err = decodeJWT(c.hub.jwtKey, c.hub.keyIsBase64, m.DocID)
	}

	if err == nil {
		log.Printf("Token %s maps to doc %s", m.DocID, realDocID)
		c.docID = realDocID
		c.writePermission = strings.Contains(permissions, "w")
		c.adminPermission = strings.Contains(permissions, "a")
		c.userID = userID

		if !strings.Contains(permissions, "r") ||
			m.CreationMode == AlwaysCreate && !c.writePermission {
			c.enqueueError(errorAccessDenied, "")
			return false
		}

		if !c.writePermission && m.CreationMode == PossiblyCreate {
			m.CreationMode = NeverCreate
			errorCodeOnMissing = errorAccessDenied
		}

	} else if err == ErrMissing && c.hub.jwtKey == "" {
		c.docID = m.DocID
		c.writePermission = true
	} else if (err == ErrMissing || err == errTokenExpired || err == errSignatureInvalid) && c.hub.jwtKey != "" {
		c.enqueueError(0x0004, "access denied")
		return false
	} else {
		c.enqueueError(0, err.Error())
		return false
	}

	initialData := m.Data

	// look up document id
	// if the document exists and create mode is ALWAYS_CREATE, then send error code ALREADY_EXISTS
	// if the document does not exist and create mode is NEVER_CREATE then send error code DOES NOT EXIST
	log.Printf("client %v looks for document %s", c.id, c.docID)
	doc, created, err := c.db.GetDocument(c.docID, CreateMode(m.CreationMode), initialData)
	if err != nil {
		switch err {
		case ErrExists:
			c.enqueueError(0x0002, "already exists")
		case ErrMissing:
			c.enqueueError(errorCodeOnMissing, "")
		default:
			c.enqueueError(0, err.Error())
		}
		return false
	}

	offset := int(m.Offset)
	if created {
		offset = len(doc)
	}

	// if the document exists and its size is < bytesSynced, then send error code INVALID_OFFSET
	if len(doc) < offset {
		c.enqueueError(0x0003, "invalid offset")
		return false
	}

	// note that at least broadcast m is always sent, even if empty.
	c.enqueueAppend(doc[offset:], uint64(offset))
	return true
}

type claims struct {
	jwt.StandardClaims
	UserID      string `json:"u"`
	Permissions string `json:"p"`
}

var errTokenExpired error
var errSignatureInvalid error

func init() {
	errTokenExpired = errors.New("token expired")
	errSignatureInvalid = errors.New("signature invalid")
}

func decodeJWT(jwtKey string, keyIsBase64 bool, tokenString string) (realDocID string, userID string, permissions string, err error) {
	// decode JWT token and verify signature using JSON Web Keyset
	// pass your custom claims to the parser function
	var token *jwt.Token
	token, err = jwt.ParseWithClaims(tokenString, &claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}

		var key []byte
		var err error
		if keyIsBase64 {
			key, err = base64.StdEncoding.DecodeString(jwtKey)
		} else {
			key = []byte(jwtKey)
		}

		return key, err
	})

	// type-assert `Claims` into a variable of the appropriate type
	if err == nil {
		myClaims := token.Claims.(*claims)

		now := time.Now().Unix()
		if myClaims.ExpiresAt <= now {
			log.Printf("User's JWT Token has expired. Token: %d Now is: %d", myClaims.ExpiresAt, now)
			err = ErrMissing
		}

		realDocID = myClaims.Subject
		userID = myClaims.UserID
		permissions = myClaims.Permissions
	} else {
		bits := err.(*jwt.ValidationError).Errors
		if bits&jwt.ValidationErrorExpired != 0 {
			err = errTokenExpired
		} else if bits&jwt.ValidationErrorSignatureInvalid != 0 {
			err = errSignatureInvalid
		}
	}
	return
}

func (c *client) processAppend(data []uint8) bool {
	var m appendMessage
	err := decodeAppendMessage(&m, data)
	if err != nil {
		log.Printf("Client %v: %v", c.id, err)
		return false
	}

	if !c.writePermission {
		log.Printf("Nack. no permission to write")
		m.Data = nil
	}

	// attempt to append to document
	newLength, err := c.db.AppendDocument(c.docID, m.Offset, m.Data)

	if err == nil && c.writePermission {
		c.enqueueAckNack(0x01, newLength)
		c.lastEnd = newLength
		c.hub.Append(c.docID, c.id, m.Offset, m.Data)
	} else if err == nil && !c.writePermission {
		c.enqueueAckNack(0x02, newLength)
	} else if err == ErrConflict {
		//log.Printf("Nack. offset should be %d not %d", newLength, m.Offset)
		c.enqueueAckNack(0x00, newLength)
	} else if err == ErrMissing {
		log.Printf("ErrMissing during append: %s does not exist", c.docID)
		c.enqueueError(0x0001, "does not exist")
	} else {
		log.Panic(err)
	}

	return true
}

func (c *client) processSetKey(data []uint8) bool {
	var m setKeyMessage
	err := decode(&m, data)
	if err != nil {
		log.Printf("Client %v: %v", c.id, err)
		return false
	}

	var ack bool
	if strings.HasPrefix(m.Name, "admin:") && !c.adminPermission {
		log.Printf("Client %v: Tried to set admin: key but lacks permissions.", c.id)

	} else if m.Lifetime == 0x00 {
		ack = c.hub.SetClientKey(c.docID, c.id, int(m.OldVersion), int(m.NewVersion), m.Name, m.Value)

	} else {
		key := Key{int(m.NewVersion), m.Name, m.Value}
		ack = nil == c.db.SetDocumentKey(c.docID, int(m.OldVersion), key)
		if ack {
			c.hub.SetSessionKey(c.docID, c.id, key)
		}
	}

	if !ack || m.OldVersion != m.NewVersion {
		c.enqueueSetKeyAckNack(ack, m.RequestID)
	}

	return true
}

func (c *client) processBroadcast(data []uint8) bool {
	var m broadcastMessage
	err := decode(&m, data)
	if err != nil {
		log.Printf("Client %v: %v", c.id, err)
		return false
	}

	// attempt to append to document
	c.hub.Broadcast(c.docID, c.id, m.Data)
	return true
}

func (c *client) notifyKeysUpdated(keys []Key) {
	// a key has been updated. By the time we get around to sending it, it may have been updated
	// again, so just store the fact that it needs to be sent for the writeThread

	c.mutex.Lock()
	defer c.mutex.Unlock()
	added := false

outer:
	for _, key := range keys {
		for i, k := range c.keys {
			if k.Name == key.Name {
				if key.Version >= k.Version {
					c.keys[i] = key
					added = true
				}
				continue outer
			}
		}
		// key not found, add and wakeup the writeThread
		c.keys = append(c.keys, key)
		added = true
	}

	if added {
		c.wakeup.Signal()
	}
}

// The client has lost access to the document.
func (c *client) notifyLostAccess(code errorCode) {
	log.Printf("    Client %v lost access to the document. Closing connection.", c.id)
	c.enqueueError(code, "")
	c.mutex.Lock()
	c.closed = true
	c.mutex.Unlock()
}

func (c *client) notifyPermissionChange(permissions string) {
	c.mutex.Lock()
	c.writePermission = strings.Contains(permissions, "w")
	c.adminPermission = strings.Contains(permissions, "a")
	c.mutex.Unlock()
	if !strings.Contains(permissions, "r") {
		c.notifyLostAccess(errorAccessDenied)
	}
}
