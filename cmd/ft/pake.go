package main

import (
	"context"
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
	timeout := fs.Duration("timeout", 60*time.Second, "how long to wait for the peer")
	logPath := fs.String("log", "", "JSONL log file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	opts := pakeOptions{
		SignalURL: *signalURL, KeyPath: *keyPath, Paired: *pairedPath,
		Code: *code, With: *with, Wait: *wait, Timeout: *timeout,
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
	client, err := signaling.Dial(ctx, opts.SignalURL, key, opts.Log)
	if err != nil {
		return pairFail(emit, err)
	}
	defer client.Close()

	mode := pairing.Initiate
	if opts.Wait {
		mode = pairing.Respond
	}
	host, _ := os.Hostname()
	peer, err := pairing.RunWithName(ctx, client, key, paired, mode, opts.Code, opts.With, host, opts.Timeout)
	if err != nil {
		return pairFail(emit, err)
	}
	if err := paired.Save(); err != nil {
		return pairFail(emit, err)
	}
	peerObj := map[string]any{"device_id": peer.ID, "pubkey": hex.EncodeToString(peer.Pub)}
	if peer.Name != "" {
		peerObj["name"] = peer.Name
	}
	emit(map[string]any{
		"ok":        true,
		"action":    "pake",
		"device_id": key.IDHex(),
		"peer":      peerObj,
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
