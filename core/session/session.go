// Package session implements the M1 session layer: HELLO/HELLO_ACK handshake,
// typed frame exchange, keepalive and closing. Path selection (LAN / punch /
// relay) is layered on top by core/pathmgr in later milestones.
package session

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/cwlindean/filetransfer/core/proto"
)

const (
	// HandshakeTimeout is the §4.1 ceiling for HELLO/HELLO_ACK.
	HandshakeTimeout = 5 * time.Second
	// DefaultRecvTimeout bounds a single wait for the next frame.
	DefaultRecvTimeout = 30 * time.Second
)

// Params are the local identity fields sent in HELLO.
type Params struct {
	DeviceID     [32]byte
	Name         string
	Capabilities uint32
}

// Peer describes the remote end after a successful handshake.
type Peer struct {
	Version      uint16
	DeviceID     [32]byte
	Name         string
	Capabilities uint32
}

// Session is a framed connection after (or during) the handshake.
type Session struct {
	nc  net.Conn
	sid [16]byte
	seq uint64

	mu   sync.Mutex // serialises frame writes
	Peer Peer
}

// Encoder is implemented by every payload type in core/proto.
type Encoder interface {
	Encode() ([]byte, error)
}

// RemoteError is a peer-reported ERROR frame.
type RemoteError struct {
	Code proto.ErrorCode
	Msg  string
}

func (e *RemoteError) Error() string {
	return fmt.Sprintf("remote error 0x%04X: %s", uint16(e.Code), e.Msg)
}

// Is lets errors.Is match on the code.
func (e *RemoteError) Is(target error) bool {
	t, ok := target.(*RemoteError)
	return ok && t.Code == e.Code
}

func newSession(nc net.Conn, sid [16]byte) *Session {
	return &Session{nc: nc, sid: sid}
}

// SessionID returns the 16-byte session id used by every frame.
func (s *Session) SessionID() [16]byte { return s.sid }

// Conn exposes the underlying connection (used by path switching).
func (s *Session) Conn() net.Conn { return s.nc }

// Close closes the underlying connection.
func (s *Session) Close() error { return s.nc.Close() }

// Drain reads until the peer closes its side or the timeout expires, then
// closes the connection. After the final FIN-OK the receiver closes first; if
// the transport is torn down immediately the sender may lose that last frame,
// so receivers call Drain before Close.
func (s *Session) Drain(timeout time.Duration) {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	_ = s.nc.SetReadDeadline(time.Now().Add(timeout))
	buf := make([]byte, 512)
	for {
		if _, err := s.nc.Read(buf); err != nil {
			break
		}
	}
	_ = s.nc.Close()
}

// SendMsg encodes msg and writes it as one frame.
func (s *Session) SendMsg(t proto.Type, msg Encoder) error {
	payload, err := msg.Encode()
	if err != nil {
		return fmt.Errorf("encode %s: %w", t, err)
	}
	if len(payload) > proto.MaxPayload {
		return proto.NewError(proto.CodeFrameTooLarge, fmt.Sprintf("%s payload %d exceeds limit", t, len(payload)))
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	f := proto.Frame{
		Header: proto.Header{
			Version:   proto.Version,
			Type:      t,
			SessionID: s.sid,
			Seq:       s.seq,
			Length:    uint32(len(payload)),
		},
		Payload: payload,
	}
	s.seq++
	if err := proto.WriteFrame(s.nc, f); err != nil {
		return fmt.Errorf("write %s: %w", t, err)
	}
	return nil
}

// Recv reads the next frame. A positive timeout sets a read deadline; zero
// means no deadline.
func (s *Session) Recv(timeout time.Duration) (proto.Frame, error) {
	if timeout > 0 {
		if err := s.nc.SetReadDeadline(time.Now().Add(timeout)); err != nil {
			return proto.Frame{}, err
		}
	} else if err := s.nc.SetReadDeadline(time.Time{}); err != nil {
		return proto.Frame{}, err
	}

	f, err := s.recvRaw()
	if err != nil {
		return proto.Frame{}, err
	}
	if f.SessionID != s.sid {
		return proto.Frame{}, proto.NewError(proto.CodeProtocolViolation, "session_id mismatch")
	}
	return f, nil
}

// recvRaw reads and validates a frame without checking the session id; the
// handshake uses it before the session id is known.
func (s *Session) recvRaw() (proto.Frame, error) {
	f, err := proto.ReadFrame(s.nc)
	if err != nil {
		return proto.Frame{}, err
	}
	return f, nil
}

// Expect waits for a specific message type and decodes it. ERROR frames are
// returned as *RemoteError.
func (s *Session) Expect(t proto.Type, timeout time.Duration) (any, error) {
	f, err := s.Recv(timeout)
	if err != nil {
		return nil, err
	}
	if f.Type == proto.TypeError {
		if msg, perr := proto.ParseError(f.Payload); perr == nil {
			return nil, &RemoteError{Code: msg.Code, Msg: msg.Message}
		}
		return nil, proto.NewError(proto.CodePayloadInvalid, "malformed ERROR frame")
	}
	if f.Type != t {
		return nil, proto.NewError(proto.CodeUnexpectedMessage, fmt.Sprintf("want %s, got %s", t, f.Type))
	}
	return proto.DecodePayload(t, f.Payload)
}

// SendError reports an error to the peer.
func (s *Session) SendError(code proto.ErrorCode, format string, args ...any) error {
	return s.SendMsg(proto.TypeError, &proto.ErrorMsg{Code: code, Message: fmt.Sprintf(format, args...)})
}

// Ping sends PING and waits for the matching PONG.
func (s *Session) Ping(timeout time.Duration) error {
	var token [8]byte
	if _, err := rand.Read(token[:]); err != nil {
		return fmt.Errorf("read random token: %w", err)
	}
	if err := s.SendMsg(proto.TypePing, &proto.Ping{Token: token}); err != nil {
		return err
	}
	v, err := s.Expect(proto.TypePong, timeout)
	if err != nil {
		return err
	}
	pong, ok := v.(*proto.Ping)
	if !ok {
		return proto.NewError(proto.CodePayloadInvalid, "PONG did not decode")
	}
	if pong.Token != token {
		return proto.NewError(proto.CodeProtocolViolation, "PONG token mismatch")
	}
	return nil
}

// ErrTimeout is returned when a peer does not answer inside the deadline.
var ErrTimeout = errors.New("session: timeout")
