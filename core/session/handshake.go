package session

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"net"
	"time"

	"github.com/zhitiaojun/ferry/core/proto"
)

func authTranscript(sid, helloNonce, challenge [16]byte, senderID, receiverID [32]byte) []byte {
	const domain = "ferry-session-auth-v1"
	out := make([]byte, 0, len(domain)+16+16+16+32+32)
	out = append(out, domain...)
	out = append(out, sid[:]...)
	out = append(out, helloNonce[:]...)
	out = append(out, challenge[:]...)
	out = append(out, senderID[:]...)
	out = append(out, receiverID[:]...)
	return out
}

// Dial performs the sender side of the §4.1 handshake on an established
// connection: HELLO -> HELLO_ACK (and optional CapAuth challenge-response).
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

	canAuth := p.Sign != nil && len(p.PubKey) == ed25519.PublicKeySize
	caps := p.Capabilities
	if canAuth {
		caps |= proto.CapAuth
	}

	hello := &proto.Hello{
		MinVersion:   proto.Version,
		MaxVersion:   proto.Version,
		DeviceID:     p.DeviceID,
		Nonce:        nonce,
		Capabilities: caps,
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

	if canAuth && (ack.Capabilities&proto.CapAuth) != 0 {
		af, err := s.recvRaw()
		if err != nil {
			return nil, fmt.Errorf("read AUTH challenge: %w", err)
		}
		if af.SessionID != sid {
			return nil, proto.NewError(proto.CodeProtocolViolation, "AUTH challenge session_id mismatch")
		}
		if af.Type == proto.TypeError {
			return nil, remoteError(af.Payload)
		}
		if af.Type != proto.TypeAuth {
			return nil, proto.NewError(proto.CodeUnexpectedMessage, fmt.Sprintf("want AUTH, got %s", af.Type))
		}
		ch, err := proto.ParseAuth(af.Payload)
		if err != nil {
			return nil, fmt.Errorf("parse AUTH challenge: %w", err)
		}
		if ch.Kind != proto.AuthKindChallenge {
			return nil, proto.NewError(proto.CodeProtocolViolation, "expected AUTH challenge")
		}
		sig := p.Sign(authTranscript(sid, nonce, ch.Nonce, p.DeviceID, ack.DeviceID))
		if len(sig) != ed25519.SignatureSize {
			return nil, proto.NewError(proto.CodeInternal, "invalid local signature length")
		}
		var proof proto.AuthMsg
		proof.Kind = proto.AuthKindProof
		copy(proof.PubKey[:], p.PubKey)
		copy(proof.Sig[:], sig)
		if err := s.SendMsg(proto.TypeAuth, &proof); err != nil {
			return nil, fmt.Errorf("send AUTH proof: %w", err)
		}
	}

	s.Peer = Peer{
		Version:      ack.Version,
		DeviceID:     ack.DeviceID,
		Name:         ack.Name,
		Capabilities: ack.Capabilities,
	}
	return s, nil
}

// Accept performs the receiver side of the handshake: HELLO -> HELLO_ACK
// (and optional CapAuth challenge-response).
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

	caps := p.Capabilities
	if p.VerifyAuth {
		caps |= proto.CapAuth
	}

	s := newSession(nc, f.SessionID)
	ack := &proto.HelloAck{
		Version:      proto.Version,
		DeviceID:     p.DeviceID,
		NonceEcho:    hello.Nonce,
		Capabilities: caps,
		Name:         p.Name,
	}
	if err := s.SendMsg(proto.TypeHelloAck, ack); err != nil {
		return nil, fmt.Errorf("send HELLO_ACK: %w", err)
	}

	var peerPub ed25519.PublicKey
	var verified bool
	if p.VerifyAuth && (hello.Capabilities&proto.CapAuth) != 0 {
		var challenge [16]byte
		if _, err := rand.Read(challenge[:]); err != nil {
			return nil, fmt.Errorf("generate auth challenge: %w", err)
		}
		if err := s.SendMsg(proto.TypeAuth, &proto.AuthMsg{
			Kind:  proto.AuthKindChallenge,
			Nonce: challenge,
		}); err != nil {
			return nil, fmt.Errorf("send AUTH challenge: %w", err)
		}
		pf, err := s.recvRaw()
		if err != nil {
			return nil, fmt.Errorf("read AUTH proof: %w", err)
		}
		if pf.SessionID != s.sid {
			return nil, proto.NewError(proto.CodeProtocolViolation, "AUTH proof session_id mismatch")
		}
		if pf.Type == proto.TypeError {
			return nil, remoteError(pf.Payload)
		}
		if pf.Type != proto.TypeAuth {
			_ = s.SendError(proto.CodeUnexpectedMessage, "want AUTH, got %s", pf.Type)
			return nil, proto.NewError(proto.CodeUnexpectedMessage, fmt.Sprintf("want AUTH, got %s", pf.Type))
		}
		proof, err := proto.ParseAuth(pf.Payload)
		if err != nil || proof.Kind != proto.AuthKindProof {
			_ = s.SendError(proto.CodeUnauthorized, "invalid auth proof")
			return nil, proto.NewError(proto.CodeUnauthorized, "invalid auth proof")
		}
		if sha256.Sum256(proof.PubKey[:]) != hello.DeviceID ||
			!ed25519.Verify(proof.PubKey[:], authTranscript(s.sid, hello.Nonce, challenge, hello.DeviceID, p.DeviceID), proof.Sig[:]) {
			_ = s.SendError(proto.CodeUnauthorized, "session auth failed")
			return nil, proto.NewError(proto.CodeUnauthorized, "session auth failed")
		}
		peerPub = append([]byte(nil), proof.PubKey[:]...)
		verified = true
	}

	s.Peer = Peer{
		Version:      proto.Version,
		DeviceID:     hello.DeviceID,
		Name:         hello.Name,
		Capabilities: hello.Capabilities,
		PubKey:       peerPub,
		AuthVerified: verified,
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
