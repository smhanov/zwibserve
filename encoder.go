package zwibserve

import (
	"errors"
	"reflect"
)

type initMessage struct {
	MessageType     uint8
	More            uint8
	ProtocolVersion uint16
	MaxMessageSize  uint32
	CreationMode    uint8
	Offset          uint64
	DocIDLength     uint8
	DocID           string
	Data            []byte
}

type appendMessage struct {
	MessageType uint8
	More        uint8
	Offset      uint64
	Data        []byte
}

type setKeyMessage struct {
	MessageType uint8
	More        uint8
	RequestID   uint16
	Lifetime    uint8
	OldVersion  uint32
	NewVersion  uint32
	NameLength  uint32
	Name        string
	ValueLength uint32
	Value       string
}

type errorMessage struct {
	MessageType uint8
	More        uint8
	ErrorCode   uint16
	Description string
}

type ackNackMessage struct {
	MessageType uint8
	More        uint8
	Ack         uint16
	Offset      uint64
}

type setKeyAckNackMessage struct {
	MessageType uint8
	More        uint8
	Ack         uint16
	RequestID   uint16
}

type keyInformationMessage struct {
	MessageType uint8
	More        uint8
	Keys        []keyInformation
}

type keyInformation struct {
	Version     uint32
	NameLength  uint32
	Name        string
	ValueLength uint32
	Value       string
}

type broadcastMessage struct {
	MessageType uint8
	More        uint8
	DataLength  uint32
	Data        []byte
}

func sizeof(kind reflect.Kind) int {
	var size int
	switch kind {
	case reflect.Uint8:
		size = 1
	case reflect.Uint16:
		size = 2
	case reflect.Uint32:
		size = 4
	case reflect.Uint64:
		size = 8
	}

	return size
}

func decode(s interface{}, m []byte) error {
	v := reflect.Indirect(reflect.ValueOf(s))

	var value uint64
	pos := 0

	for i := 0; i < v.NumField(); i++ {
		// Get the field tag value
		field := v.Field(i)
		kind := field.Kind()
		name := v.Type().Field(i).Name

		var size int
		if kind == reflect.String {
			size = int(value)
		} else {
			size = sizeof(kind)
		}

		if pos+size > len(m) {
			return errors.New("message too short")
		}

		if kind == reflect.String {
			field.SetString(string(m[pos : pos+size]))
		} else if name == "Data" {
			field.SetBytes(m[pos:])
			pos = len(m)
		} else {
			value = readUint(m, pos, size)
			field.SetUint(value)
		}

		pos += size
	}

	return nil
}

func encode(m []byte, s interface{}) []byte {
	v := reflect.ValueOf(s)
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		kind := field.Kind()
		name := v.Type().Field(i).Name

		if kind == reflect.String {
			m = append(m, []byte(field.String())...)
		} else if name == "Data" {
			m = append(m, field.Bytes()...)
		} else if kind == reflect.Slice {
			for j := 0; j < field.Len(); j++ {
				m = encode(m, field.Index(j).Interface())
			}
		} else {
			m = writeUint(m, field.Uint(), sizeof(kind))
		}
	}

	return m
}

func readUint(m []byte, at, size int) uint64 {
	var ret uint64
	for size > 0 {
		ret = ret*256 + uint64(m[at])
		size--
		at++
	}
	return ret
}

func writeUint(m []byte, v uint64, size int) []byte {
	for size > 0 {
		size--
		m = append(m, byte(v>>(size*8)))
	}
	return m
}
