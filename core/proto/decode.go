package proto

import (
	"encoding/binary"
	"fmt"
)

func be32(b []byte) uint32 { return binary.BigEndian.Uint32(b) }
func be64(b []byte) uint64 { return binary.BigEndian.Uint64(b) }

// DecodePayload parses p according to message type t (§3).
func DecodePayload(t Type, p []byte) (any, error) {
	switch t {
	case TypeHello:
		return ParseHello(p)
	case TypeHelloAck:
		return ParseHelloAck(p)
	case TypeManifest:
		return ParseManifest(p)
	case TypeData:
		return ParseData(p)
	case TypeAck:
		return ParseAck(p)
	case TypeResume:
		return ParseResume(p)
	case TypeFin:
		return ParseFin(p)
	case TypeError:
		return ParseError(p)
	case TypePing, TypePong:
		return ParsePing(p)
	default:
		return nil, NewError(CodeUnknownType, fmt.Sprintf("unknown message type 0x%02X", uint8(t)))
	}
}
