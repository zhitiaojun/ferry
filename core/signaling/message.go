// Package signaling implements the client side of the ft-server signaling
// protocol (protocol §8): a JSON envelope over a WebSocket, an Ed25519 login
// challenge, presence, candidate exchange and relay booking.
package signaling

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Version is the envelope version understood by this implementation.
const Version = 1

// MaxMessage is the largest envelope accepted from a peer (protocol §8.1).
const MaxMessage = 64 << 10

// Message types (protocol §8.3).
const (
	TypeLoginBegin  = "login_begin"
	TypeLoginChal   = "login_challenge"
	TypeLogin       = "login"
	TypeLoginOK     = "login_ok"
	TypePresence    = "presence"
	TypePairBegin   = "pair_begin"
	TypePairRelay   = "pair_relay"
	TypePairResult  = "pair_result"
	TypeCandidates  = "candidates"
	TypeSendRequest = "send_request"
	TypeSendResp    = "send_response"
	TypeRelayOpen   = "relay_open"
	TypeRelayReady  = "relay_ready"
	TypePing        = "ping"
	TypePong        = "pong"
	TypeError       = "error"
)

// Error codes used by the signaling server.
const (
	ErrUnsupportedType = "unsupported_type"
	ErrBadEnvelope     = "bad_envelope"
	ErrAuthFailed      = "auth_failed"
	ErrPeerOffline     = "peer_offline"
	ErrBadRequest      = "bad_request"
	ErrRateLimited     = "rate_limited"
	ErrTooLarge        = "message_too_large"
)

// Envelope is one WebSocket text message (protocol §8.1).
type Envelope struct {
	V    int             `json:"v"`
	Type string          `json:"type"`
	ID   string          `json:"id,omitempty"`
	Ts   int64           `json:"ts,omitempty"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Build creates an envelope with a JSON-encoded data payload and a millisecond
// timestamp. A nil data value produces an empty object.
func Build(typ, id string, data any) (*Envelope, error) {
	e := &Envelope{V: Version, Type: typ, ID: id, Ts: time.Now().UnixMilli()}
	if data == nil {
		return e, nil
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("signaling: marshal %s data: %w", typ, err)
	}
	e.Data = raw
	return e, nil
}

// Parse validates an incoming envelope and returns it with the payload still
// undecoded.
func Parse(raw []byte) (*Envelope, error) {
	if len(raw) > MaxMessage {
		return nil, fmt.Errorf("signaling: message is %d bytes, limit %d", len(raw), MaxMessage)
	}
	var e Envelope
	if err := json.Unmarshal(raw, &e); err != nil {
		return nil, fmt.Errorf("signaling: bad envelope: %w", err)
	}
	if e.V != Version {
		return nil, fmt.Errorf("signaling: unsupported envelope version %d", e.V)
	}
	if e.Type == "" {
		return nil, errors.New("signaling: envelope has no type")
	}
	return &e, nil
}

// Decode unmarshals the envelope payload into T.
func Decode[T any](e *Envelope) (T, error) {
	var v T
	if len(e.Data) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(e.Data, &v); err != nil {
		return v, fmt.Errorf("signaling: decode %s: %w", e.Type, err)
	}
	return v, nil
}

// MustEncode marshals a typed data struct, panicking only on programmer error
// (types in this package are plain structs).
func MustEncode(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return raw
}

// LoginBegin is the first login message (C→S).
type LoginBegin struct {
	DeviceID string `json:"device_id"`
	PubKey   string `json:"pubkey"`
}

// LoginChallenge is the server's nonce challenge (S→C).
type LoginChallenge struct {
	Nonce       string `json:"nonce"`
	ExpiresInMS int64  `json:"expires_in_ms"`
}

// Login carries the signature over identity.LoginMessage (C→S).
type Login struct {
	Signature string `json:"signature"`
}

// Peer is one entry of the online table.
type Peer struct {
	DeviceID string `json:"device_id"`
	Online   bool   `json:"online"`
}

// LoginOK confirms the login and returns the current online table (S→C).
type LoginOK struct {
	ServerTime int64  `json:"server_time"`
	Peers      []Peer `json:"peers"`
}

// Presence announces a peer coming online or going offline (S→C).
type Presence struct {
	DeviceID string `json:"device_id"`
	Online   bool   `json:"online"`
}

// Candidate is one network path offered to the peer.
type Candidate struct {
	Kind     string `json:"kind"`
	Addr     string `json:"addr"`
	Priority int    `json:"priority"`
}

// Candidates exchanges path candidates for a session (both directions).
type Candidates struct {
	ToDevice   string      `json:"to_device"`
	SessionID  string      `json:"session_id"`
	Candidates []Candidate `json:"candidates"`
}

// FileInfo names one file in a send request.
type FileInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// SendRequest asks the peer to accept a transfer (C→S→C).
type SendRequest struct {
	ToDevice   string     `json:"to_device"`
	FromDevice string     `json:"from_device,omitempty"` // filled in by the server when relaying
	SessionID  string     `json:"session_id"`
	TransferID string     `json:"transfer_id"`
	Files      []FileInfo `json:"files"`
}

// SendResponse accepts or rejects a transfer (C→S→C).
type SendResponse struct {
	ToDevice   string `json:"to_device"`
	TransferID string `json:"transfer_id"`
	Accept     bool   `json:"accept"`
	Reason     string `json:"reason,omitempty"`
}

// RelayOpen books a relay room for both devices (C→S).
type RelayOpen struct {
	ToDevice   string `json:"to_device"`
	TransferID string `json:"transfer_id"`
}

// RelayReady returns the one-time relay token (S→C).
type RelayReady struct {
	RelayURL    string `json:"relay_url"`
	Token       string `json:"token"`
	ExpiresInMS int64  `json:"expires_in_ms"`
}

// PairBegin starts pairing (C→S). FromDevice is the initiator's device id; the
// responder only trusts it after the PAKE confirmation proves the key hash.
type PairBegin struct {
	ToDevice   string `json:"to_device"`
	FromDevice string `json:"from_device"`
	Name       string `json:"name,omitempty"`
	Method     string `json:"method"`
	Proof      string `json:"proof"`
}

// PairRelay relays one pairing payload between the two devices. ToDevice must
// be set by the sender; the server fills FromDevice.
type PairRelay struct {
	ToDevice   string `json:"to_device"`
	FromDevice string `json:"from_device"`
	Name       string `json:"name,omitempty"`
	Payload    string `json:"payload"`
}

// PairResult reports the outcome of pairing (S→C).
type PairResult struct {
	OK   bool `json:"ok"`
	Peer struct {
		DeviceID string `json:"device_id"`
		PubKey   string `json:"pubkey"`
	} `json:"peer"`
}

// ErrorBody is the payload of a type="error" message (S→C).
type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message,omitempty"`
	Ref     string `json:"ref,omitempty"`
}

// Ping carries a client timestamp echoed by pong.
type Ping struct {
	T int64 `json:"t"`
}
