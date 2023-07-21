package zwibserve

import (
	"errors"
	"reflect"
)

const (
	initMessageType           = 0x01
	appendV2MessageType       = 0x02
	setKeyMessageType         = 0x03
	broadcastMessageType      = 0x04
	appendMessageType         = 0x05
	errorMessageType          = 0x80
	ackNackMessageType        = 0x81
	keyInformationMessageType = 0x82

	serverIdentificationMessageType = 0x84
	swarmRegisterMessageType        = 0x85
	swarmDataMessageType            = 0x86

	continuationMessageType = 0xff
)

type initMessageV2 struct {
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

type initMessage struct {
	MessageType     uint8
	More            uint8
	ProtocolVersion uint16
	MaxMessageSize  uint32
	CreationMode    uint8
	Generation      uint32
	Offset          uint64
	DocIDLength     uint32
	DocID           string
	Data            []byte
}

type appendMessageV2 struct {
	MessageType uint8
	More        uint8
	Offset      uint64
	Data        []byte
}

type appendMessage struct {
	MessageType uint8
	More        uint8
	Generation  uint32
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
	Keys        []keyInformation `repeat:"true"`
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
	_, err := _decode(s, m, 0)
	return err
}

func _decode(s interface{}, m []byte, pos int) (int, error) {
	v := reflect.Indirect(reflect.ValueOf(s))

	var value uint64

	for i := 0; i < v.NumField(); i++ {
		// Get the field tag value
		field := v.Field(i)
		kind := field.Kind()
		name := v.Type().Field(i).Name

		isBytes := kind == reflect.Slice && field.Type().Elem().Kind() == reflect.Uint8

		var size int
		if kind == reflect.String || isBytes && name != "Data" {
			size = int(value)
		} else {
			size = sizeof(kind)
		}

		//log.Printf("Decode %s isbytes=%v size=%v", name, isBytes, size)

		//log.Printf("At %v decode %v size is %v", pos, name, size)
		if pos+size > len(m) {
			return pos, errors.New("message too short")
		}

		if kind == reflect.String {
			field.SetString(string(m[pos : pos+size]))
		} else if name == "Data" {
			field.SetBytes(m[pos:])
			pos = len(m)
		} else if kind == reflect.Slice {
			if isBytes {
				field.SetBytes(m[pos : pos+size])
			} else {
				// read the tag of the field
				repeatToEnd := v.Type().Field(i).Tag.Get("repeat") == "true"

				var err error
				elemType := field.Type().Elem()
				size = 0 // int(value) * int(elemType.Size())
				for j := uint64(0); repeatToEnd && pos < len(m) || !repeatToEnd && j < value; j++ {
					elemValue := reflect.New(elemType)
					pos, err = _decode(elemValue.Interface(), m, pos)
					if err != nil {
						return pos, err
					}
					tmp := reflect.Append(field, reflect.Indirect(elemValue))
					field.Set(tmp)
				}
			}
		} else {
			value = readUint(m, pos, size)
			field.SetUint(value)
		}

		pos += size
	}

	return pos, nil
}

func encode(m []byte, s interface{}) []byte {
	v := reflect.ValueOf(s)
	for i := 0; i < v.NumField(); i++ {
		field := v.Field(i)
		kind := field.Kind()
		//name := v.Type().Field(i).Name
		//log.Printf("Encode %s=%+v", name, s)
		if kind == reflect.String {
			m = append(m, []byte(field.String())...)
		} else if kind == reflect.Slice {
			if field.Type().Elem().Kind() == reflect.Uint8 {
				m = append(m, field.Bytes()...)
			} else {
				for j := 0; j < field.Len(); j++ {
					m = encode(m, field.Index(j).Interface())
				}
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
