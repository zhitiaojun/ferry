package mobile

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/pairing"
	"github.com/cwlindean/filetransfer/core/signaling"
)

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
// code and then trusts them.
func WaitPairCode(signalURL, keyPath, pairedPath, code string, timeoutMS int) (string, error) {
	if signalURL == "" {
		signalURL = DefaultSignalURL
	}
	if timeoutMS <= 0 {
		timeoutMS = 90000
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
