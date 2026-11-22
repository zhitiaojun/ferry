package mobile

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/pairing"
	"github.com/cwlindean/filetransfer/core/signaling"
)

// GeneratePairCode returns a random 6-digit one-time pairing code.
func GeneratePairCode() string {
	return pairing.GenerateCode()
}

// BuildMyPairURI builds a ferry://pair?... QR payload valid for ttlSec seconds
// (default 600 = 10 minutes).
func BuildMyPairURI(keyPath, code, name string, ttlSec int) (string, error) {
	if ttlSec <= 0 {
		ttlSec = int(pairing.DefaultTTL / time.Second)
	}
	key, err := identity.LoadOrCreateKey(keyPath)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(name) == "" {
		if h, herr := os.Hostname(); herr == nil {
			name = h
		}
	}
	exp := time.Now().UTC().Add(time.Duration(ttlSec) * time.Second)
	return pairing.BuildPairURI(key.IDHex(), code, name, exp), nil
}

// PairWithURI validates an incoming ferry://pair?... QR URI against its 10-minute
// expiration and runs SPAKE2 pairing against the peer.
func PairWithURI(signalURL, keyPath, pairedPath, rawURI string) (string, error) {
	u, err := pairing.ParsePairURI(rawURI, time.Now().UTC())
	if err != nil {
		return "", err
	}
	res, err := PairWithCode(signalURL, keyPath, pairedPath, u.Code, u.DeviceID)
	if err != nil {
		return "", err
	}
	if u.Name != "" {
		if store, lerr := identity.LoadPaired(pairedPath); lerr == nil {
			if dev, ok := store.Get(u.DeviceID); ok && dev.Name == "" {
				dev.Name = u.Name
				if aerr := store.Add(dev); aerr == nil {
					_ = store.Save()
				}
			}
		}
	}
	return res, nil
}

// PairWithCode runs the initiator side of one-time-code pairing against peerID.
// The 6-digit code never leaves the device; both sides prove knowledge of it
// with SPAKE2 (protocol §8.6).
func PairWithCode(signalURL, keyPath, pairedPath, code, peerID string) (string, error) {
	if signalURL == "" {
		signalURL = DefaultSignalURL
	}
	if peerID == "" {
		return "", fmt.Errorf("输入对方的设备 ID")
	}
	key, err := identity.LoadOrCreateKey(keyPath)
	if err != nil {
		return "", err
	}
	paired, err := identity.LoadPaired(pairedPath)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client, err := signaling.Dial(ctx, signalURL, key, nil)
	if err != nil {
		return "", err
	}
	defer client.Close()
	peer, err := pairing.Run(ctx, client, key, paired, pairing.Initiate, code, peerID, 60*time.Second)
	if err != nil {
		return "", err
	}
	if err := paired.Save(); err != nil {
		return "", err
	}
	return pairResult(peer, len(paired.List())), nil
}

// WaitPairCode runs the responder side: it waits for someone who typed the same
// code and then trusts them (default timeout 10 minutes).
func WaitPairCode(signalURL, keyPath, pairedPath, code string, timeoutMS int) (string, error) {
	if signalURL == "" {
		signalURL = DefaultSignalURL
	}
	if timeoutMS <= 0 {
		timeoutMS = int(pairing.DefaultTTL / time.Millisecond)
	}
	key, err := identity.LoadOrCreateKey(keyPath)
	if err != nil {
		return "", err
	}
	paired, err := identity.LoadPaired(pairedPath)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()
	client, err := signaling.Dial(ctx, signalURL, key, nil)
	if err != nil {
		return "", err
	}
	defer client.Close()
	peer, err := pairing.Run(ctx, client, key, paired, pairing.Respond, code, "", time.Duration(timeoutMS)*time.Millisecond)
	if err != nil {
		return "", err
	}
	if err := paired.Save(); err != nil {
		return "", err
	}
	return pairResult(peer, len(paired.List())), nil
}

func pairResult(peer pairing.Peer, total int) string {
	raw, _ := json.Marshal(map[string]any{
		"ok": true, "peer": peer.ID, "pubkey": fmt.Sprintf("%x", peer.Pub), "paired": total,
	})
	return string(raw)
}

// PairedPeers lists the trusted peers stored at pairedPath as a JSON array of
// {"id":"...","name":"..."} objects (returning "[]" if no file exists yet).
func PairedPeers(pairedPath string) (string, error) {
	store, err := identity.LoadPaired(pairedPath)
	if err != nil {
		return "", err
	}
	type item struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	list := store.List()
	out := make([]item, 0, len(list))
	for _, d := range list {
		out = append(out, item{ID: d.DeviceID, Name: d.Name})
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
