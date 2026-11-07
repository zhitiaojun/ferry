package proto

import (
	"encoding/binary"
	"fmt"
	"io"
)

// Protocol constants from docs/spec/protocol.md §1.
const (
	// Magic is the four-byte frame magic: ASCII "FTRX".
	Magic = "FTRX"
	// Version is the current major protocol version.
	Version = 1
	// HeaderSize is the fixed frame header size in bytes.
	HeaderSize = 36
	// MaxPayload is the largest accepted payload (1 MiB).
	MaxPayload = 1 << 20
)

// Type identifies a session-layer message (§3.1).
type Type uint8

// Message types (§3.1).
const (
	TypeHello    Type = 0x01
	TypeHelloAck Type = 0x02
	TypeManifest Type = 0x03
	TypeData     Type = 0x04
	TypeAck      Type = 0x05
	TypeResume   Type = 0x06
	TypeFin      Type = 0x07
	TypeError    Type = 0x08
	TypePing     Type = 0x09
	TypePong     Type = 0x0A
)

var typeNames = map[Type]string{
	TypeHello:    "HELLO",
	TypeHelloAck: "HELLO_ACK",
	TypeManifest: "MANIFEST",
	TypeData:     "DATA",
	TypeAck:      "ACK",
	TypeResume:   "RESUME",
	TypeFin:      "FIN",
	TypeError:    "ERROR",
	TypePing:     "PING",
	TypePong:     "PONG",
}

// String returns the protocol name of the message type.
func (t Type) String() string {
	if s, ok := typeNames[t]; ok {
		return s
	}
	return fmt.Sprintf("TYPE_0x%02X", uint8(t))
}

// Known reports whether t is a defined session message type.
func (t Type) Known() bool {
	_, ok := typeNames[t]
	return ok
}

// ParseType resolves a protocol type name such as "HELLO" to its Type.
func ParseType(name string) (Type, bool) {
	for t, n := range typeNames {
		if n == name {
			return t, true
		}
	}
	return 0, false
}

// Header is the fixed 36-byte frame header (§2).
type Header struct {
	Version   uint8
	Type      Type
	Flags     uint16
	SessionID [16]byte
	Seq       uint64
	Length    uint32
}

// Frame is one protocol frame: a fixed header plus its payload.
type Frame struct {
	Header
	Payload []byte
}

// MarshalFrame encodes f into wire bytes.
func MarshalFrame(f Frame) ([]byte, error) {
	version := f.Version
	if version == 0 {
		version = Version
	}
	if version != Version {
		return nil, NewError(CodeVersionUnsupported, fmt.Sprintf("cannot marshal version %d", version))
	}
	if !f.Type.Known() {
		return nil, NewError(CodeUnknownType, fmt.Sprintf("unknown message type 0x%02X", uint8(f.Type)))
	}
	if f.SessionID == ([16]byte{}) {
		return nil, NewError(CodeProtocolViolation, "session_id must not be zero")
	}
	if len(f.Payload) > MaxPayload {
		return nil, NewError(CodeFrameTooLarge, fmt.Sprintf("payload %d exceeds %d", len(f.Payload), MaxPayload))
	}

	buf := make([]byte, HeaderSize+len(f.Payload))
	copy(buf[0:4], Magic)
	buf[4] = version
	buf[5] = byte(f.Type)
	binary.BigEndian.PutUint16(buf[6:8], f.Flags)
	copy(buf[8:24], f.SessionID[:])
	binary.BigEndian.PutUint64(buf[24:32], f.Seq)
	binary.BigEndian.PutUint32(buf[32:36], uint32(len(f.Payload)))
	copy(buf[HeaderSize:], f.Payload)
	return buf, nil
}

// ParseHeader decodes and validates a frame header from the first HeaderSize
// bytes of b. It does not look at the payload.
func ParseHeader(b []byte) (Header, error) {
	var h Header
	if len(b) < HeaderSize {
		return h, io.ErrUnexpectedEOF
	}
	if string(b[0:4]) != Magic {
		return h, NewError(CodeBadMagic, "magic mismatch (not a filetransfer frame)")
	}
	h.Version = b[4]
	if h.Version != Version {
		return h, NewError(CodeVersionUnsupported, fmt.Sprintf("unsupported version %d", h.Version))
	}
	h.Type = Type(b[5])
	h.Flags = binary.BigEndian.Uint16(b[6:8])
	copy(h.SessionID[:], b[8:24])
	h.Seq = binary.BigEndian.Uint64(b[24:32])
	h.Length = binary.BigEndian.Uint32(b[32:36])
	if h.Length > MaxPayload {
		return h, NewError(CodeFrameTooLarge, fmt.Sprintf("declared payload %d exceeds %d", h.Length, MaxPayload))
	}
	if h.SessionID == ([16]byte{}) {
		return h, NewError(CodeProtocolViolation, "session_id must not be zero")
	}
	if !h.Type.Known() {
		return h, NewError(CodeUnknownType, fmt.Sprintf("unknown message type 0x%02X", uint8(h.Type)))
	}
	return h, nil
}

// ReadFrame reads one complete frame from r. It returns io.EOF only when the
// stream ends exactly at a frame boundary; a truncated frame yields
// io.ErrUnexpectedEOF.
func ReadFrame(r io.Reader) (Frame, error) {
	head := make([]byte, HeaderSize)
	if _, err := io.ReadFull(r, head); err != nil {
		return Frame{}, err
	}
	h, err := ParseHeader(head)
	if err != nil {
		return Frame{}, err
	}
	f := Frame{Header: h, Payload: make([]byte, h.Length)}
	if h.Length > 0 {
		if _, err := io.ReadFull(r, f.Payload); err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return Frame{}, err
		}
	}
	return f, nil
}

// WriteFrame encodes f and writes it to w.
func WriteFrame(w io.Writer, f Frame) error {
	b, err := MarshalFrame(f)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}
