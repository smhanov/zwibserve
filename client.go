package zwibserve

import (
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// Default maximum message size
const maxMessageSize = 100 * 1024

// nextClientNumber is just a number identifing the client for logging.
var nextClientNumber int64

type client struct {
	id    int64
	ws    *websocket.Conn
	docID string

	// for tokens
	userID          string
	writePermission bool

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

// Takes over the connection and runs the client, Responsible for closing the socket.
func runClient(hub *hub, db DocumentDB, ws *websocket.Conn) {
	c := &client{
		ws:      ws,
		db:      db,
		hub:     hub,
		id:      atomic.AddInt64(&nextClientNumber, 1),
		maxSize: maxMessageSize,
	}
	c.wakeup = sync.NewCond(&c.mutex)

	defer func() {
		c.mutex.Lock()
		c.closed = true
		c.wakeup.Signal()
		c.mutex.Unlock()
	}()

	go c.writeThread()

	log.Printf("Client %d connected", c.id)

	// wait up to 30 seconds for init message
	ws.SetReadDeadline(time.Now().Add(30 * time.Second))

	message, err := readMessage(c.ws)
	if err != nil {
		log.Printf("%d: error waiting for init message: %v", c.id, err)
		return
	}

	if !c.processInitMessage(message) {
		return
	}

	hub.addClient(c.docID, c)
	defer hub.removeClient(c.docID, c)

	sessionKeys, _ := c.db.GetDocumentKeys(c.docID)
	c.notifyKeysUpdated(c.hub.getClientKeys(c.docID))
	c.notifyKeysUpdated(sessionKeys)
	sessionKeys = nil

	// set read deadline back to normal
	ws.SetReadDeadline(time.Time{})

	for {
		message, err = readMessage(c.ws)
		if err != nil {
			log.Printf("client for %s disconnected", c.docID)
			break
		}

		if message[0] == 0x02 {
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
			log.Printf("client %v sent unepected message type %v", c.id, message[0])
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
				MessageType: 0x82,
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
		} else if p[0] == 0xff {
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

// Writes a complete message, respecting maximum message size and breaking it into chunks
// if necessary. MUST ONLY BE CALLED BY WRITETHREAD
func (c *client) sendMessage(data []byte) {
	send := len(data)
	if send > c.maxSize {
		send = c.maxSize
		data[1] = 1
	} else {
		data[1] = 0
	}

	// send first part
	err := c.ws.WriteMessage(websocket.BinaryMessage, data[:send])
	if err != nil {
		log.Printf("Got ERROR writing to socket: %v", err)
	}
	data = data[send:]
	for len(data) > 0 {
		log.Printf("Sent %d/%d bytes", send, len(data))
		writer, err := c.ws.NextWriter(websocket.BinaryMessage)
		if err != nil {
			log.Panic(err)
		}

		send := len(data)
		more := byte(0)
		if send > c.maxSize-2 {
			send = c.maxSize - 2
			more = 1
		}

		writer.Write([]byte{0xff, more})
		writer.Write(data[:send])
		writer.Close()
		data = data[send:]
	}
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
	c.enqueue(appendMessage{
		MessageType: 0x02,
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

func (c *client) processInitMessage(data []uint8) bool {
	var m initMessage
	err := decode(&m, data)
	if err != nil {
		log.Printf("Client %v: %v", c.id, err)
		return false
	}

	if m.MessageType != 0x01 {
		log.Printf("Client %v: ERROR Expected INIT message", c.id)
		return false
	}

	if m.ProtocolVersion != 2 {
		log.Printf("Client %v: protocol versiom must be 2", c.id)
		return false
	}

	if m.MaxMessageSize != 0 {
		c.maxSize = int(m.MaxMessageSize)
	}

	errorCodeOnMissing := errorDoesNotExist

	// check if its a token
	realDocID, userID, permissions, err := c.db.GetToken(m.DocID)
	if err == nil {
		log.Printf("Token %s maps to doc %s", m.DocID, realDocID)
		c.docID = realDocID
		c.writePermission = strings.Contains(permissions, "w")
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

	} else if err == ErrMissing {
		c.docID = m.DocID
		c.writePermission = true
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

func (c *client) processAppend(data []uint8) bool {
	var m appendMessage
	err := decode(&m, data)
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
		c.hub.append(c.docID, c, m.Offset, m.Data)
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
	if m.Lifetime == 0x00 {
		ack = c.hub.setClientKey(c.docID, c, int(m.OldVersion), int(m.NewVersion), m.Name, m.Value)
	} else {
		key := Key{int(m.NewVersion), m.Name, m.Value}
		ack = nil == c.db.SetDocumentKey(c.docID, int(m.OldVersion), key)
		if ack {
			c.hub.setSessionKey(c.docID, c, key)
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
	c.hub.broadcast(c.docID, c, m.Data)
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
	c.mutex.Unlock()
	if !strings.Contains(permissions, "r") {
		c.notifyLostAccess(errorAccessDenied)
	}
}
