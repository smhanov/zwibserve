package zwibserve

import (
	"log"
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

	db        DocumentDB
	hub       *hub
	writeChan chan func()

	// maximum message size requested by client.
	maxSize int
}

// Takes over the connection and runs the client, Responsible for closing the socket.
func runClient(hub *hub, db DocumentDB, ws *websocket.Conn) {
	c := &client{
		writeChan: make(chan func(), 8),
		ws:        ws,
		db:        db,
		hub:       hub,
		id:        atomic.AddInt64(&nextClientNumber, 1),
		maxSize:   maxMessageSize,
	}

	go func() {
		for fn := range c.writeChan {
			fn()
		}
		ws.Close()
		log.Printf("Client %d destroyed.", c.id)
	}()
	defer close(c.writeChan)

	log.Printf("Client %d connected", c.id)

	// wait up to 30 seconds for init message
	ws.SetReadDeadline(time.Now().Add(30 * time.Second))

	message, err := c.readMessage()
	if err != nil {
		log.Printf("%d: error waiting for init message: %v", c.id, err)
		return
	}

	if !c.processInitMessage(message) {
		return
	}

	hub.addClient(c.docID, c)
	defer hub.removeClient(c.docID, c)

	// set read deadline back to normal
	ws.SetReadDeadline(time.Time{})

	for {
		message, err = c.readMessage()
		if err != nil {
			log.Printf("client for %s disconnected", c.docID)
			break
		}

		if !c.processAppend(message) {
			break
		}
	}
}

// Reads a complete message, taking into account the MORE byte to
// join continuation messages together.
func (c *client) readMessage() ([]uint8, error) {
	// only to be called from runClient
	var buffer []byte
	for {
		_, p, err := c.ws.ReadMessage()
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

func (c *client) sendError(code uint16, text string) {
	c.writeChan <- func() {
		log.Printf("Client %v error: %s", c.id, text)
		var m []byte
		m = append(m, 0x80, 0x00)                     // error message type
		m = append(m, byte(code>>8), byte(code&0xff)) // code
		m = append(m, []byte(text)...)
		err := c.ws.WriteMessage(websocket.BinaryMessage, m)
		if err != nil {
			return
		}
	}
}

func (c *client) sendAppend(data []byte, offset uint64) {
	c.writeChan <- func() {
		var m []byte
		m = append(m, 0x02, 0x00) // message type, more
		m = writeUint64(m, offset)
		m = append(m, data...)
		c.sendMessage(m)
	}
}

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

func (c *client) sendAckNack(ack bool, length uint64) {
	c.writeChan <- func() {
		var m []byte
		m = append(m, 0x81, 0x00)
		if ack {
			log.Printf("Client %v << ACK", c.id)
			m = append(m, 0x00, 0x01)
		} else {
			log.Printf("Client %v << NACK", c.id)
			m = append(m, 0x00, 0x00)
		}
		m = writeUint64(m, length)
		c.ws.WriteMessage(websocket.BinaryMessage, m)
	}
}

func readUint16(m []byte) uint16 {
	return uint16(m[0])<<8 | uint16(m[1])
}

func readUint32(m []byte) uint32 {
	return uint32(m[0])<<24 | uint32(m[1])<<16 | uint32(m[2])<<8 | uint32(m[3])
}

func readUint64(m []byte) uint64 {
	return uint64(m[0])<<56 |
		uint64(m[1])<<48 |
		uint64(m[2])<<40 |
		uint64(m[3])<<32 |
		uint64(m[4])<<24 |
		uint64(m[5])<<16 |
		uint64(m[6])<<8 |
		uint64(m[7])
}

func writeUint64(m []byte, n uint64) []byte {
	return append(m,
		byte(n>>56),
		byte(n>>48),
		byte(n>>40),
		byte(n>>32),
		byte(n>>24),
		byte(n>>16),
		byte(n>>8),
		byte(n),
	)
}

func (c *client) processInitMessage(m []uint8) bool {
	if len(m) < 15 {
		log.Printf("Client %v: init message too short", c.id)
		return false
	}

	messageType := m[0]
	protocolVersion := int(readUint16(m[2:4]))
	maxSize := int(readUint32(m[4:8]))
	createMode := CreateMode(m[8])
	offset := int(readUint64(m[9:17]))
	idLength := int(m[17])

	if messageType != 1 {
		// error: expected init m. Send error and disconnect.
		log.Printf("Client %v: expected init message", c.id)
		return false
	}

	if protocolVersion != 2 {
		log.Printf("Client %v: protocol versiom must be 2", c.id)
		return false
	}

	if idLength > len(m)-18 {
		log.Printf("Client %v: id length overflows message", c.id)
		return false
	}

	if maxSize != 0 {
		c.maxSize = maxSize
	}

	c.docID = string(m[18 : 18+idLength])
	initialData := m[18+idLength:]

	// look up document id
	// if the document exists and create mode is ALWAYS_CREATE, then send error code ALREADY_EXISTS
	// if the document does not exist and create mode is NEVER_CREATE then send error code DOES NOT EXIST
	log.Printf("client %v looks for document %s", c.id, c.docID)
	doc, created, err := c.db.GetDocument(c.docID, createMode, initialData)
	if err != nil {
		switch err {
		case ErrExists:
			c.sendError(0x0002, "already exists")
		case ErrMissing:
			c.sendError(0x0001, "does not exist")
		default:
			c.sendError(0, err.Error())
		}
		return false
	}

	if created {
		offset = len(doc)
	}

	// if the document exists and its size is < bytesSynced, then send error code INVALID_OFFSET
	if len(doc) < offset {
		c.sendError(0x0003, "invalid offset")
		return false
	}

	// note that at least broadcast m is always sent, even if empty.
	c.sendAppend(doc[offset:], uint64(offset))
	return true
}

func (c *client) processAppend(m []uint8) bool {
	if len(m) < 13 {
		c.sendError(0, "invalid message length")
		return false
	}

	messageType := m[0]
	offset := readUint64(m[2:10])
	data := m[10:]

	if messageType != 0x02 {
		c.sendError(0, "expected append message")
		return false
	}

	// attempt to append to document
	newLength, err := c.db.AppendDocument(c.docID, offset, data)

	if err == nil {
		c.sendAckNack(true, newLength)
		c.hub.broadcast(c.docID, c, offset, data)
	} else if err == ErrConflict {
		log.Printf("Nack. offset should be %d not %d", newLength, offset)
		c.sendAckNack(false, newLength)
	} else if err == ErrMissing {
		c.sendError(0x0001, "does not exist")
	}

	return true
}
