package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/logging"
	"github.com/cwlindean/filetransfer/core/pairing"
	"github.com/cwlindean/filetransfer/core/signaling"
)

// pakeOptions configures `ft pair --code`.
type pakeOptions struct {
	SignalURL string
	KeyPath   string
	Paired    string
	Name      string
	Code      string
	With      string
	Wait      bool
	Timeout   time.Duration
	Log       *logging.Logger
}

// runPairPake pairs two devices that both know the same one-time 6-digit code.
// The code never crosses the wire: both sides run SPAKE2 over pair_relay and
// then exchange authenticated public keys.
func runPairPake(args []string) int {
	fs := flag.NewFlagSet("pair", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	keyPath := fs.String("key", defaultKeyPath(), "device key file")
	pairedPath := fs.String("paired", defaultPairedPath(), "trusted peer table")
	code := fs.String("code", "", "one-time 6-digit code shared out of band")
	with := fs.String("with", "", "peer device id (initiator)")
	wait := fs.Bool("wait", false, "wait for an initiator instead of dialing one")
	signalURL := fs.String("signal", "", "signaling URL (wss://…/signal)")
	name := fs.String("name", "", "device name shown to the peer")
	timeout := fs.Duration("timeout", 60*time.Second, "how long to wait for the peer")
	logPath := fs.String("log", "", "JSONL log file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	opts := pakeOptions{
		SignalURL: *signalURL,
		KeyPath:   *keyPath,
		Paired:    *pairedPath,
		Name:      *name,
		Code:      *code,
		With:      *with,
		Wait:      *wait,
		Timeout:   *timeout,
	}
	emit := func(v any) {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(v)
	}
	if err := pakeOptionsError(opts); err != nil {
		return pairFail(emit, err)
	}
	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return pairFail(emit, fmt.Errorf("open log: %w", err))
		}
		defer f.Close()
		opts.Log = logging.New(f)
	}

	key, err := loadDeviceKey(opts.KeyPath)
	if err != nil {
		return pairFail(emit, err)
	}
	paired, err := identity.LoadPaired(opts.Paired)
	if err != nil {
		return pairFail(emit, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()
	peer, err := exchangePAKE(ctx, key, opts)
	if err != nil {
		return pairFail(emit, err)
	}
	entry := identity.PairedDevice{
		DeviceID: peer.id,
		Name:     peer.name,
		PubKey:   hex.EncodeToString(peer.pub),
		PairedAt: time.Now().UTC(),
	}
	if err := paired.Add(entry); err != nil {
		return pairFail(emit, err)
	}
	if err := paired.Save(); err != nil {
		return pairFail(emit, err)
	}
	emit(map[string]any{
		"ok":        true,
		"action":    "pake",
		"device_id": key.IDHex(),
		"peer":      map[string]any{"device_id": peer.id, "name": peer.name},
		"paired":    len(paired.List()),
	})
	return 0
}

func pakeOptionsError(o pakeOptions) error {
	if o.Code == "" {
		return fmt.Errorf("--code is required (6-digit one-time code)")
	}
	if len(o.Code) != 6 {
		return fmt.Errorf("--code must be 6 characters")
	}
	if o.SignalURL == "" {
		return fmt.Errorf("--signal is required")
	}
	if !o.Wait && o.With == "" {
		return fmt.Errorf("--with is required unless --wait is set")
	}
	return nil
}

func pairFail(emit func(any), err error) int {
	emit(map[string]any{"ok": false, "error": err.Error()})
	fmt.Fprintf(os.Stderr, "ft: %v\n", err)
	return 1
}

// pakePeer is what each side learns about the other.
type pakePeer struct {
	id   string
	pub  []byte
	name string
}

const (
	pakeMethod   = "pake"
	pakeTagLabel = "pair"
	// payload = sid(16) || share(65); confirms carry tag(32) || pubkey(32).
	pakeSIDLen = 16
)

func exchangePAKE(ctx context.Context, key *identity.Key, o pakeOptions) (*pakePeer, error) {
	client, err := signaling.Dial(ctx, o.SignalURL, key, o.Log)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	begins := make(chan signaling.PairBegin, 4)
	relays := make(chan signaling.PairRelay, 8)
	client.On(signaling.TypePairBegin, func(e *signaling.Envelope) {
		if m, err := signaling.Decode[signaling.PairBegin](e); err == nil {
			begins <- m
		}
	})
	client.On(signaling.TypePairRelay, func(e *signaling.Envelope) {
		if m, err := signaling.Decode[signaling.PairRelay](e); err == nil {
			relays <- m
		}
	})

	if o.Wait {
		return respondPAKE(ctx, client, key, o, begins, relays)
	}
	return initiatePAKE(ctx, client, key, o, relays)
}

func initiatePAKE(ctx context.Context, client *signaling.Client, key *identity.Key, o pakeOptions, relays chan signaling.PairRelay) (*pakePeer, error) {
	sid := newSID16()
	ex, err := pairing.Start(pairing.RoleInitiator, o.Code, sid)
	if err != nil {
		return nil, err
	}
	first := make([]byte, 0, pakeSIDLen+len(ex.Message()))
	first = append(first, sid[:]...)
	first = append(first, ex.Message()...)
	if err := client.Send(ctx, signaling.TypePairBegin, signaling.PairBegin{
		ToDevice:   o.With,
		FromDevice: key.IDHex(),
		Method:     pakeMethod,
		Proof:      base64.StdEncoding.EncodeToString(first),
	}); err != nil {
		return nil, err
	}

	reply, err := waitRelay(ctx, relays, o.With)
	if err != nil {
		return nil, err
	}
	share, err := base64.StdEncoding.DecodeString(reply)
	if err != nil {
		return nil, fmt.Errorf("decode share: %w", err)
	}
	shared, err := ex.Finish(share)
	if err != nil {
		return nil, fmt.Errorf("code mismatch: %w", err)
	}
	if _, err := sendConfirm(ctx, client, o.With, shared, key); err != nil {
		return nil, err
	}
	// Verify the responder's tag before trusting its key.
	raw, err := waitRelay(ctx, relays, o.With)
	if err != nil {
		return nil, err
	}
	blob, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(blob) != 32+32 {
		return nil, fmt.Errorf("malformed confirmation")
	}
	if !equalConst(blob[:32], pairing.ConfirmTag(shared, pakeTagLabel)) {
		return nil, fmt.Errorf("peer failed the pairing confirmation")
	}
	return checkPeer(o.With, &confirmPayload{tag: blob[:32], pub: blob[32:]})
}

func respondPAKE(ctx context.Context, client *signaling.Client, key *identity.Key, o pakeOptions, begins chan signaling.PairBegin, relays chan signaling.PairRelay) (*pakePeer, error) {
	var begin signaling.PairBegin
	select {
	case begin = <-begins:
	case <-ctx.Done():
		return nil, fmt.Errorf("no pairing request within %s", o.Timeout)
	case <-client.Done():
		return nil, fmt.Errorf("signaling connection dropped")
	}
	if begin.Method != pakeMethod {
		return nil, fmt.Errorf("unsupported pairing method %q", begin.Method)
	}
	blob, err := base64.StdEncoding.DecodeString(begin.Proof)
	if err != nil || len(blob) != pakeSIDLen+pairing.ShareSize {
		return nil, fmt.Errorf("malformed pairing payload")
	}
	var sid [16]byte
	copy(sid[:], blob[:pakeSIDLen])
	ex, err := pairing.Start(pairing.RoleResponder, o.Code, sid)
	if err != nil {
		return nil, err
	}
	shared, err := ex.Finish(blob[pakeSIDLen:])
	if err != nil {
		return nil, fmt.Errorf("code mismatch: %w", err)
	}
	if err := client.Send(ctx, signaling.TypePairRelay, signaling.PairRelay{
		ToDevice: begin.FromDevice,
		Payload:  base64.StdEncoding.EncodeToString(ex.Message()),
	}); err != nil {
		return nil, err
	}
	reply, err := waitRelay(ctx, relays, begin.FromDevice)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(reply)
	if err != nil || len(raw) != 32+32 {
		return nil, fmt.Errorf("malformed confirmation")
	}
	if !equalConst(raw[:32], pairing.ConfirmTag(shared, pakeTagLabel)) {
		return nil, fmt.Errorf("peer failed the pairing confirmation")
	}
	peers, err := checkPeer(begin.FromDevice, &confirmPayload{tag: raw[:32], pub: raw[32:]})
	if err != nil {
		return nil, err
	}
	if _, err := sendConfirm(ctx, client, begin.FromDevice, shared, key); err != nil {
		return nil, err
	}
	return peers, nil
}

type confirmPayload struct {
	tag []byte
	pub []byte
}

func sendConfirm(ctx context.Context, client *signaling.Client, to string, shared [32]byte, key *identity.Key) (*confirmPayload, error) {
	tag := pairing.ConfirmTag(shared, pakeTagLabel)
	body := make([]byte, 0, 64)
	body = append(body, tag...)
	body = append(body, key.Public()...)
	if err := client.Send(ctx, signaling.TypePairRelay, signaling.PairRelay{
		ToDevice: to,
		Payload:  base64.StdEncoding.EncodeToString(body),
	}); err != nil {
		return nil, err
	}
	return &confirmPayload{tag: tag, pub: key.Public()}, nil
}

func checkPeer(wantID string, p *confirmPayload) (*pakePeer, error) {
	sum := sha256.Sum256(p.pub)
	got := hex.EncodeToString(sum[:])
	if got != wantID {
		return nil, fmt.Errorf("peer key does not match device id %s", wantID)
	}
	return &pakePeer{id: got, pub: p.pub}, nil
}

func waitRelay(ctx context.Context, relays chan signaling.PairRelay, from string) (string, error) {
	for {
		select {
		case m := <-relays:
			if from != "" && m.FromDevice != "" && m.FromDevice != from {
				continue
			}
			return m.Payload, nil
		case <-ctx.Done():
			return "", fmt.Errorf("timed out waiting for the peer")
		}
	}
}

func newSID16() [16]byte {
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
