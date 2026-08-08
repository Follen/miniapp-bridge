package wmpf

import (
	_ "embed"
	"fmt"
	"io"
	"strings"
)

//go:embed schema.proto
var recoveredSchema string

type WireField struct {
	Number   int
	WireType int
	Raw      []byte
	Value    []byte
}
type GenericMessage struct {
	Type   string
	Fields []WireField
}

func MessageTypes() []string {
	var out []string
	for _, line := range strings.Split(recoveredSchema, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "message" {
			out = append(out, f[1])
		}
	}
	return out
}
func HasMessageType(name string) bool {
	for _, v := range MessageTypes() {
		if v == name {
			return true
		}
	}
	return false
}
func DecodeGeneric(name string, data []byte) (GenericMessage, error) {
	if !HasMessageType(name) {
		return GenericMessage{}, fmt.Errorf("unknown protobuf message type %q", name)
	}
	m := GenericMessage{Type: name}
	for i := 0; i < len(data); {
		start := i
		tag, e := readVar(data, &i)
		if e != nil {
			return m, e
		}
		field := int(tag >> 3)
		wire := int(tag & 7)
		if field < 1 {
			return m, fmt.Errorf("invalid protobuf field %d", field)
		}
		valueStart := i
		switch wire {
		case 0:
			_, e = readVar(data, &i)
			if e != nil {
				return m, e
			}
		case 1:
			if len(data)-i < 8 {
				return m, io.ErrUnexpectedEOF
			}
			i += 8
		case 2:
			l, e := readVar(data, &i)
			if e != nil {
				return m, e
			}
			if l > uint64(len(data)-i) {
				return m, io.ErrUnexpectedEOF
			}
			i += int(l)
		case 5:
			if len(data)-i < 4 {
				return m, io.ErrUnexpectedEOF
			}
			i += 4
		default:
			return m, fmt.Errorf("unsupported protobuf wire type %d", wire)
		}
		m.Fields = append(m.Fields, WireField{Number: field, WireType: wire, Raw: append([]byte(nil), data[start:i]...), Value: append([]byte(nil), data[valueStart:i]...)})
	}
	return m, nil
}
func EncodeGeneric(m GenericMessage) []byte {
	out := make([]byte, 0)
	for _, f := range m.Fields {
		out = append(out, f.Raw...)
	}
	return out
}
func SchemaFieldCount(name string) int {
	inside := false
	n := 0
	for _, line := range strings.Split(recoveredSchema, "\n") {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "message ") {
			inside = strings.HasPrefix(t, "message "+name+" ")
			continue
		}
		if inside && strings.HasPrefix(t, "}") {
			break
		}
		if inside && strings.Contains(t, "=") {
			n++
		}
	}
	return n
}
