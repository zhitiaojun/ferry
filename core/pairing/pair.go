package pairing

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/proto"
	"github.com/cwlindean/filetransfer/core/signaling"
)

// Method is the pairing method named in pair_begin.
const Method = "pake"

// ConfirmLabel is bound into the confirmation tag.
const ConfirmLabel = "pair"

// Mode selects which side of the PAKE handshake this device plays.
type Mode int

const (
	// Initiate dials a known peer: it must know the peer's device id.
	Initiate Mode = iota
	// Respond waits for an initiator that chose this device.
	Respond
)

// Peer is what the pairing proves about the other device.
type Peer struct {
	ID   string // device id hex (sha256 of PubKey)
	Pub  []byte // Ed25519 public key
	Name string // friendly device name reported by the peer
}

// SanitizeName strips control characters, trims whitespace, and truncates on
// UTF-8 rune boundaries so device names never exceed proto.MaxDeviceName bytes.
func SanitizeName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		if r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	s := strings.TrimSpace(b.String())
	if len(s) <= proto.MaxDeviceName {
		return s
	}
	cut := 0
	for i := range s {
		if i > proto.MaxDeviceName {
			break
		}
		cut = i
	}
	if cut == 0 {
		for _, r := range s {
			rl := utf8.RuneLen(r)
			if rl > 0 && cut+rl <= proto.MaxDeviceName {
				cut += rl
			} else {
				break
			}
		}
	}
	return strings.TrimSpace(s[:cut])
}

// Run performs the one-time-code pairing over an already logged-in signaling
// connection. Both sides must use the same code; the code itself never leaves
// the device.
func Run(ctx context.Context, client *signaling.Client, key *identity.Key, paired *identity.PairedStore, mode Mode, code, peerID string, timeout time.Duration) (Peer, error) {
	return RunWithName(ctx, client, key, paired, mode, code, peerID, "", timeout)
}

// RunWithName performs SPAKE2 pairing and exchanges friendly device names so
// both initiator and responder record each other's name in PairedStore.
func RunWithName(ctx context.Context, client *signaling.Client, key *identity.Key, paired *identity.PairedStore, mode Mode, code, peerID, localName string, timeout time.Duration) (Peer, error) {
	if len(code) != 6 {
		return Peer{}, fmt.Errorf("pairing code must be 6 digits")
	}
	if mode == Initiate && peerID == "" {
		return Peer{}, fmt.Errorf("peer device id is required")
	}
	localName = SanitizeName(localName)
	begins := make(chan signaling.PairBegin, 4)
	relays := make(chan signaling.PairRelay, 8)
	errCh := make(chan error, 4)
	client.On(signaling.TypePairBegin, func(e *signaling.Envelope) {
		if m, err := signaling.Decode[signaling.PairBegin](e); err == nil {
			select {
			case begins <- m:
			default:
			}
		}
	})
	client.On(signaling.TypePairRelay, func(e *signaling.Envelope) {
		if m, err := signaling.Decode[signaling.PairRelay](e); err == nil {
			select {
			case relays <- m:
			default:
			}
		}
	})
	client.On(signaling.TypeError, func(e *signaling.Envelope) {
		b, _ := signaling.Decode[signaling.ErrorBody](e)
		select {
		case errCh <- fmt.Errorf("signaling: %s: %s", b.Code, b.Message):
		default:
		}
	})
	defer func() {
		client.Off(signaling.TypePairBegin)
		client.Off(signaling.TypePairRelay)
		client.Off(signaling.TypeError)
	}()

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var peer Peer
	var err error
	if mode == Respond {
		peer, err = respond(ctx, client, key, code, localName, begins, relays, errCh)
	} else {
		peer, err = initiate(ctx, client, key, code, peerID, localName, relays, errCh)
	}
	if err != nil {
		return Peer{}, err
	}
	if paired != nil {
		saveName := SanitizeName(peer.Name)
		if saveName == "" {
			if existing, ok := paired.Get(peer.ID); ok && existing.Name != "" {
				saveName = existing.Name
			}
		}
		if err := paired.Add(identity.PairedDevice{
			DeviceID: peer.ID, Name: saveName, PubKey: hex.EncodeToString(peer.Pub), PairedAt: time.Now().UTC(),
		}); err != nil {
			return Peer{}, err
		}
	}
	return peer, nil
}

func initiate(ctx context.Context, client *signaling.Client, key *identity.Key, code, peerID, localName string, relays chan signaling.PairRelay, errCh <-chan error) (Peer, error) {
	sid := newPairSID()
	ex, err := Start(RoleInitiator, code, sid)
	if err != nil {
		return Peer{}, err
	}
	first := append(sid[:], ex.Message()...)
	if err := client.Send(ctx, signaling.TypePairBegin, signaling.PairBegin{
		ToDevice: peerID, FromDevice: key.IDHex(), Name: localName, Method: Method,
		Proof: base64.StdEncoding.EncodeToString(first),
	}); err != nil {
		return Peer{}, err
	}
	shareMsg, err := waitRelay(ctx, client, relays, errCh, peerID)
	if err != nil {
		return Peer{}, err
	}
	share, err := base64.StdEncoding.DecodeString(shareMsg.Payload)
	if err != nil {
		return Peer{}, fmt.Errorf("decode share: %w", err)
	}
	shared, err := ex.Finish(share)
	if err != nil {
		return Peer{}, fmt.Errorf("code mismatch: %w", err)
	}
	if err := sendConfirm(ctx, client, peerID, localName, shared, key); err != nil {
		return Peer{}, err
	}
	confirmMsg, err := waitRelay(ctx, client, relays, errCh, peerID)
	if err != nil {
		return Peer{}, err
	}
	blob, err := base64.StdEncoding.DecodeString(confirmMsg.Payload)
	if err != nil || len(blob) != 64 {
		return Peer{}, fmt.Errorf("malformed confirmation")
	}
	if !equalConst(blob[:32], ConfirmTag(shared, ConfirmLabel)) {
		return Peer{}, fmt.Errorf("peer failed the pairing confirmation")
	}
	peer, err := checkPeer(peerID, blob[32:])
	if err != nil {
		return Peer{}, err
	}
	peer.Name = SanitizeName(confirmMsg.Name)
	if peer.Name == "" {
		peer.Name = SanitizeName(shareMsg.Name)
	}
	return peer, nil
}

func respond(ctx context.Context, client *signaling.Client, key *identity.Key, code, localName string, begins chan signaling.PairBegin, relays chan signaling.PairRelay, errCh <-chan error) (Peer, error) {
	var begin signaling.PairBegin
	select {
	case begin = <-begins:
	case err := <-errCh:
		return Peer{}, err
	case <-ctx.Done():
		return Peer{}, fmt.Errorf("no pairing request before the deadline")
	case <-client.Done():
		return Peer{}, fmt.Errorf("signaling connection dropped")
	}
	if begin.Method != Method {
		return Peer{}, fmt.Errorf("unsupported pairing method %q", begin.Method)
	}
	blob, err := base64.StdEncoding.DecodeString(begin.Proof)
	if err != nil || len(blob) != 16+ShareSize {
		return Peer{}, fmt.Errorf("malformed pairing payload")
	}
	var sid [16]byte
	copy(sid[:], blob[:16])
	ex, err := Start(RoleResponder, code, sid)
	if err != nil {
		return Peer{}, err
	}
	shared, err := ex.Finish(blob[16:])
	if err != nil {
		return Peer{}, fmt.Errorf("code mismatch: %w", err)
	}
	if err := client.Send(ctx, signaling.TypePairRelay, signaling.PairRelay{
		ToDevice: begin.FromDevice, Name: localName, Payload: base64.StdEncoding.EncodeToString(ex.Message()),
	}); err != nil {
		return Peer{}, err
	}
	confirmMsg, err := waitRelay(ctx, client, relays, errCh, begin.FromDevice)
	if err != nil {
		return Peer{}, err
	}
	confirm, err := base64.StdEncoding.DecodeString(confirmMsg.Payload)
	if err != nil || len(confirm) != 64 {
		return Peer{}, fmt.Errorf("malformed confirmation")
	}
	if !equalConst(confirm[:32], ConfirmTag(shared, ConfirmLabel)) {
		return Peer{}, fmt.Errorf("peer failed the pairing confirmation")
	}
	peer, err := checkPeer(begin.FromDevice, confirm[32:])
	if err != nil {
		return Peer{}, err
	}
	peer.Name = SanitizeName(confirmMsg.Name)
	if peer.Name == "" {
		peer.Name = SanitizeName(begin.Name)
	}
	if err := sendConfirm(ctx, client, begin.FromDevice, localName, shared, key); err != nil {
		return Peer{}, err
	}
	return peer, nil
}

func sendConfirm(ctx context.Context, client *signaling.Client, to, localName string, shared [32]byte, key *identity.Key) error {
	body := append(ConfirmTag(shared, ConfirmLabel), key.Public()...)
	return client.Send(ctx, signaling.TypePairRelay, signaling.PairRelay{
		ToDevice: to, Name: localName, Payload: base64.StdEncoding.EncodeToString(body),
	})
}

func checkPeer(wantID string, pub []byte) (Peer, error) {
	sum := sha256.Sum256(pub)
	got := hex.EncodeToString(sum[:])
	if got != wantID {
		return Peer{}, fmt.Errorf("peer key does not match device id %s", wantID)
	}
	return Peer{ID: got, Pub: pub}, nil
}

func waitRelay(ctx context.Context, client *signaling.Client, relays chan signaling.PairRelay, errCh <-chan error, from string) (signaling.PairRelay, error) {
	for {
		select {
		case m := <-relays:
			if from != "" && m.FromDevice != "" && m.FromDevice != from {
				continue
			}
			return m, nil
		case err := <-errCh:
			return signaling.PairRelay{}, err
		case <-client.Done():
			return signaling.PairRelay{}, fmt.Errorf("signaling connection dropped")
		case <-ctx.Done():
			return signaling.PairRelay{}, fmt.Errorf("timed out waiting for the peer")
		}
	}
}

func newPairSID() [16]byte {
	var sid [16]byte
	if _, err := rand.Read(sid[:]); err != nil {
		panic(err)
	}
	return sid
}

func equalConst(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}
