package session

import (
	"crypto/rand"
	"fmt"
	"net"
	"time"

	"github.com/cwlindean/filetransfer/core/proto"
)

// Dial performs the sender side of the §4.1 handshake on an established
// connection: HELLO -> HELLO_ACK, verifying the nonce echo and version.
func Dial(nc net.Conn, p Params) (*Session, error) {
	var sid, nonce [16]byte
	if _, err := rand.Read(sid[:]); err != nil {
		return nil, fmt.Errorf("generate session id: %w", err)
	}
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	s := newSession(nc, sid)

	if err := nc.SetDeadline(time.Now().Add(HandshakeTimeout)); err != nil {
		return nil, err
	}
	defer nc.SetDeadline(time.Time{})

	hello := &proto.Hello{
		MinVersion:   proto.Version,
		MaxVersion:   proto.Version,
		DeviceID:     p.DeviceID,
		Nonce:        nonce,
		Capabilities: p.Capabilities,
		Name:         p.Name,
	}
	if err := s.SendMsg(proto.TypeHello, hello); err != nil {
		return nil, fmt.Errorf("send HELLO: %w", err)
	}

	f, err := s.recvRaw()
	if err != nil {
		return nil, fmt.Errorf("read HELLO_ACK: %w", err)
	}
	if f.SessionID != sid {
		return nil, proto.NewError(proto.CodeProtocolViolation, "HELLO_ACK session_id mismatch")
	}
	if f.Type == proto.TypeError {
		return nil, remoteError(f.Payload)
	}
	if f.Type != proto.TypeHelloAck {
		return nil, proto.NewError(proto.CodeUnexpectedMessage, fmt.Sprintf("want HELLO_ACK, got %s", f.Type))
	}

	ack, err := proto.ParseHelloAck(f.Payload)
	if err != nil {
		return nil, fmt.Errorf("parse HELLO_ACK: %w", err)
	}
	if ack.NonceEcho != nonce {
		return nil, proto.NewError(proto.CodeProtocolViolation, "HELLO_ACK nonce echo mismatch")
	}
	if ack.Version != proto.Version {
		return nil, proto.NewError(proto.CodeVersionUnsupported, fmt.Sprintf("peer version %d", ack.Version))
	}

	s.Peer = Peer{
		Version:      ack.Version,
		DeviceID:     ack.DeviceID,
		Name:         ack.Name,
		Capabilities: ack.Capabilities,
	}
	return s, nil
}

// Accept performs the receiver side of the handshake: HELLO -> HELLO_ACK.
func Accept(nc net.Conn, p Params) (*Session, error) {
	if err := nc.SetDeadline(time.Now().Add(HandshakeTimeout)); err != nil {
		return nil, err
	}
	defer nc.SetDeadline(time.Time{})

	tmp := &Session{nc: nc}
	f, err := tmp.recvRaw()
	if err != nil {
		return nil, fmt.Errorf("read HELLO: %w", err)
	}
	if f.Type == proto.TypeError {
		return nil, remoteError(f.Payload)
	}
	if f.Type != proto.TypeHello {
		_ = tmp.SendMsg(proto.TypeError, &proto.ErrorMsg{
			Code:    proto.CodeUnexpectedMessage,
			Message: fmt.Sprintf("want HELLO, got %s", f.Type),
		})
		return nil, proto.NewError(proto.CodeUnexpectedMessage, fmt.Sprintf("want HELLO, got %s", f.Type))
	}
	if f.SessionID == ([16]byte{}) {
		return nil, proto.NewError(proto.CodeProtocolViolation, "HELLO with zero session_id")
	}

	hello, err := proto.ParseHello(f.Payload)
	if err != nil {
		_ = tmp.SendMsg(proto.TypeError, &proto.ErrorMsg{Code: proto.CodePayloadInvalid, Message: "malformed HELLO"})
		return nil, fmt.Errorf("parse HELLO: %w", err)
	}
	if hello.MinVersion > proto.Version || hello.MaxVersion < proto.Version {
		_ = tmp.SendMsg(proto.TypeError, &proto.ErrorMsg{
			Code:    proto.CodeVersionUnsupported,
			Message: fmt.Sprintf("no common version (peer %d..%d, local %d)", hello.MinVersion, hello.MaxVersion, proto.Version),
		})
		return nil, proto.NewError(proto.CodeVersionUnsupported, "no common protocol version")
	}

	s := newSession(nc, f.SessionID)
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	ack := &proto.HelloAck{
		Version:      proto.Version,
		DeviceID:     p.DeviceID,
		NonceEcho:    hello.Nonce,
		Capabilities: p.Capabilities,
		Name:         p.Name,
	}
	if err := s.SendMsg(proto.TypeHelloAck, ack); err != nil {
		return nil, fmt.Errorf("send HELLO_ACK: %w", err)
	}

	s.Peer = Peer{
		Version:      proto.Version,
		DeviceID:     hello.DeviceID,
		Name:         hello.Name,
		Capabilities: hello.Capabilities,
	}
	return s, nil
}

func remoteError(payload []byte) error {
	msg, err := proto.ParseError(payload)
	if err != nil {
		return proto.NewError(proto.CodePayloadInvalid, "malformed ERROR frame")
	}
	return &RemoteError{Code: msg.Code, Msg: msg.Message}
}
