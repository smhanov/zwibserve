package zwibserve

import (
	"fmt"
	"log"
	"math/rand"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// StressTestArgs gives the parameters for performing a stress test against another server.
type StressTestArgs struct {
	// The address of the other server, eg wss://otherserver.com/socket
	Address string

	// The document to connect to
	DocumentID string

	// The number of clients which are modifying the document
	NumTeachers int

	// The number of clients which are merely listening for changes
	NumStudents int

	// The average number of milliseconds a teacher waits before making each change.
	// Default: 1000
	DelayMS int

	// The number of bytes in each change (default: 200)
	ChangeLength int

	// Show all steps
	Verbose bool
}

const maxPingTimes = 10000
const randomConnectTime = 3000
const defaultChangeLength = 10

type stressTestArgs struct {
	StressTestArgs

	// Wait group
	wg sync.WaitGroup

	// Set to true if there has been an error and we should abort everything.
	abort bool

	mutex sync.Mutex
	// wrap-around buffer of ping times for avg / min / max calculations
	pingTimes     []int64
	nextPingIndex int
	lastShowTime  time.Time
	numConnected  int
	docLength     int64
}

func (args *stressTestArgs) recordPingTime(value int64, docLength int64) {
	args.mutex.Lock()
	defer args.mutex.Unlock()
	if docLength > args.docLength {
		args.docLength = docLength
	}
	if len(args.pingTimes) < maxPingTimes {
		args.pingTimes = append(args.pingTimes, value)
	} else {

		args.pingTimes[args.nextPingIndex] = value

		args.nextPingIndex++
		if args.nextPingIndex == maxPingTimes {
			args.nextPingIndex = 0
		}
	}

	args.showStats()
}

func (args *stressTestArgs) recordConnection() {
	args.mutex.Lock()
	defer args.mutex.Unlock()
	args.numConnected++
	args.showStats()
}

func (args *stressTestArgs) showStats() {
	// requires locked mutex
	if time.Since(args.lastShowTime) < 100*time.Millisecond {
		return
	}
	args.lastShowTime = time.Now()
	var sum, min, max, avg int64

	if len(args.pingTimes) > 0 {
		sum = args.pingTimes[0]
		min = args.pingTimes[0]
		max = args.pingTimes[0]

		for i := 1; i < len(args.pingTimes); i++ {
			v := args.pingTimes[i]
			sum += v
			if v < min {
				min = v
			}
			if v > max {
				max = v
			}
		}
		avg = int64(float64(sum) / float64(len(args.pingTimes)))
	}

	str := fmt.Sprintf("Connections=%d docLength=%d Screen-to-screen time avg=%dms min=%dms max=%dms      ",
		args.numConnected,
		args.docLength,
		avg,
		min, max)

	if args.Verbose {
		log.Print(str)
	} else {
		os.Stderr.Write([]byte(str + "\r"))
	}
}

// RunStressTest runs a stress test against another server. The test continues
// forever, or until you quit the process.
func RunStressTest(argsIn StressTestArgs) {
	args := &stressTestArgs{StressTestArgs: argsIn}
	if args.ChangeLength <= 0 {
		args.ChangeLength = defaultChangeLength
	} else if args.ChangeLength < 8 {
		args.ChangeLength = 8 // need to encode sending ms
	}

	if args.DelayMS == 0 {
		args.DelayMS = 1000
	}

	id := 1
	for i := 0; i < args.NumStudents; i++ {
		args.wg.Add(1)
		go abortOnError(args, id, studentClient)

		id++
	}

	for i := 0; i < args.NumTeachers; i++ {
		args.wg.Add(1)
		go abortOnError(args, id, teacherClient)

		id++
	}

	// in case we have all students and they all connect before 1s:
	time.Sleep((randomConnectTime + 100) * time.Millisecond)
	args.mutex.Lock()
	args.showStats()
	args.mutex.Unlock()

	args.wg.Wait()
}

func abortOnError(args *stressTestArgs, clientID int, fn func(args *stressTestArgs, clientID int)) {
	defer func() {
		err := recover()
		if err != nil {
			args.abort = true
		}
	}()
	fn(args, clientID)
}

func connect(args *stressTestArgs, clientID int) *websocket.Conn {
	u, err := url.Parse(args.Address)
	if err != nil {
		log.Panic(err)
	}

	// wait a random amount of time
	time.Sleep(time.Duration(rand.Intn(randomConnectTime)) * time.Millisecond)

	if args.Verbose {
		log.Printf("Client %d connecting to %s...", clientID, u.String())
	}
	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Panic(err)
	}

	return c
}

func sendMessage(conn *websocket.Conn, message interface{}) {
	err := conn.WriteMessage(websocket.BinaryMessage, encode(nil, message))
	if err != nil {
		log.Panic(err)
	}
}

func readStressMessage(conn *websocket.Conn) []uint8 {
	message, err := readMessage(conn)
	if err != nil {
		log.Panic(err)
	}
	if len(message) == 0 {
		log.Panicf("Got zero-length message")
	}
	return message
}

func studentClient(args *stressTestArgs, clientID int) {
	defer args.wg.Done()
	conn := connect(args, clientID)
	defer conn.Close()
	args.recordConnection()

	sendConnectMessage(conn, args.DocumentID)
	first := true

	for !args.abort {
		m := readAppendMessage(conn)
		if !first && len(m.Data) >= 4 {
			diff := (time.Now().UnixMilli() & 0xffffffff) - decodeSendingMS(m.Data)
			args.recordPingTime(diff, int64(m.Offset)+int64(len(m.Data)))
		}
		first = false
		if args.Verbose {
			log.Printf("Student %d received append to offset %d", clientID, m.Offset)
		}
	}
}

func readAppendMessage(conn *websocket.Conn) appendMessage {
	bytes := readStressMessage(conn)

	var m appendMessage
	if bytes[0] == appendMessageType {
		err := decode(&m, bytes)
		if err != nil {
			log.Panic(err)
		}
	} else {
		log.Panicf("Received unexpected message type %d", bytes[0])
	}
	return m
}

func sendConnectMessage(conn *websocket.Conn, docID string) {
	sendMessage(conn, initMessage{
		MessageType:     initMessageType,
		ProtocolVersion: 0x0002,
		CreationMode:    0x00, // possibly create
		DocIDLength:     uint8(len(docID)),
		DocID:           docID,
	})
}

func teacherClient(args *stressTestArgs, clientID int) {
	defer args.wg.Done()
	conn := connect(args, clientID)
	defer conn.Close()
	args.recordConnection()

	nextChar := 'A'
	sendConnectMessage(conn, args.DocumentID)

	// wait for initial append
	m := readAppendMessage(conn)
	offset := m.Offset + uint64(len(m.Data))

	if len(m.Data) > 0 {
		nextChar = rune(m.Data[len(m.Data)-1]) + 1
		if nextChar > 'Z' {
			nextChar = 'A'
		}
	}

	if args.Verbose {
		log.Printf("Teacher %d received append to offset %d", clientID, m.Offset)
	}

	var mutex sync.Mutex
	gotAck := true // can we send yet?

	// writer thread
	go func() {
		for {
			// delay random amount of time related to the input delay
			value := time.Duration(rand.NormFloat64()*float64(args.DelayMS/2) + float64(args.DelayMS))
			time.Sleep(value * time.Millisecond)
			if args.abort {
				return
			}
			if !gotAck {
				continue
			}

			mutex.Lock()
			offsetToUse := offset
			charToUse := nextChar
			gotAck = false
			mutex.Unlock()

			data := make([]byte, args.ChangeLength)
			for i := range data {
				data[i] = byte(charToUse)
			}

			encodeSendingMS(data)

			if args.Verbose {
				log.Printf("Teacher %d attempts to add to document at offset %d", clientID, offsetToUse)
			}
			sendMessage(conn, appendMessage{
				MessageType: appendMessageType,
				Offset:      offsetToUse,
				Data:        data,
			})
		}
	}()

	// reading thread
	for !args.abort {
		bytes := readStressMessage(conn)
		if bytes[0] == appendMessageType {
			var m appendMessage
			err := decode(&m, bytes)
			if err != nil {
				log.Panic(err)
			}

			mutex.Lock()
			offset = m.Offset + uint64(len(m.Data))
			gotAck = true

			if len(m.Data) > 0 {
				nextChar = rune(m.Data[len(m.Data)-1]) + 1
				if nextChar > 'Z' {
					nextChar = 'A'
				}
			}

			mutex.Unlock()
			if len(m.Data) > 0 {
				diff := (time.Now().UnixMilli() & 0xffffffff) - decodeSendingMS(m.Data)
				args.recordPingTime(diff, int64(m.Offset)+int64(len(m.Data)))
			}

		} else if bytes[0] == ackNackMessageType {
			var m ackNackMessage
			err := decode(&m, bytes)
			if err != nil {
				log.Panic(err)
			}
			if m.Ack == 0x0001 {
				if args.Verbose {
					log.Printf("Teacher %d received ACK for offset %d", clientID, m.Offset)
				}
				mutex.Lock()
				offset = m.Offset
				gotAck = true
				nextChar += 1
				if nextChar > 'Z' {
					nextChar = 'A'
				}
				mutex.Unlock()
			} else {
				if args.Verbose {
					log.Printf("Teacher %d received NACK", clientID)
				}
				// continue to wait for Append before sending.
			}
		} else {
			log.Panicf("Teacher received unepexected message type 0x%x", bytes[0])
		}
	}
}

func encodeSendingMS(bytes []byte) {
	ts := time.Now().UnixMilli()
	bytes[0] = byte((ts >> 24) & 0xff)
	bytes[1] = byte((ts >> 16) & 0xff)
	bytes[2] = byte((ts >> 8) & 0xff)
	bytes[3] = byte((ts) & 0xff)
}

func decodeSendingMS(bytes []byte) int64 {
	return (int64(bytes[0]) << 24) |
		(int64(bytes[1]) << 16) |
		(int64(bytes[2]) << 8) |
		(int64(bytes[3]))
}
