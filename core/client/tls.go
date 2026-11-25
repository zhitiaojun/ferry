package client

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"time"

	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/relay"
	"github.com/cwlindean/filetransfer/core/session"
	"github.com/cwlindean/filetransfer/core/signaling"
)

// PairedPeer returns the pinned public key of a peer from the trusted table.
func PairedPeer(path, id string) (ed25519.PublicKey, error) {
	return pairedPeer(path, id)
}

// IsVerifiedPaired reports whether a session peer proved possession of an
// Ed25519 key present in the trusted table at pairedPath.
func IsVerifiedPaired(pairedPath string, peer session.Peer) bool {
	if pairedPath == "" || !peer.AuthVerified || len(peer.PubKey) != ed25519.PublicKeySize {
		return false
	}
	stored, err := pairedPeer(pairedPath, hex.EncodeToString(peer.DeviceID[:]))
	if err != nil {
		return false
	}
	return bytes.Equal(stored, peer.PubKey)
}

// pairedPeer returns the pinned public key of a peer from the trusted table.
func pairedPeer(path, id string) (ed25519.PublicKey, error) {
	store, err := identity.LoadPaired(path)
	if err != nil {
		return nil, err
	}
	d, ok := store.Get(id)
	if !ok {
		return nil, fmt.Errorf("peer %s is not paired; import its URI with: ft pair --add <uri>", truncHex(id))
	}
	return d.PublicKeyBytes()
}

// clientTLS wraps the relay stream with the pinned TLS 1.3 channel (D26): our
// identity certificate on one side, the paired peer's key as the only accepted
// pin on the other, so neither the server nor the relay room can impersonate.
func clientTLS(ctx context.Context, rc net.Conn, key *identity.Key, peer ed25519.PublicKey) (net.Conn, error) {
	cfg, err := identity.ClientTLS(key, peer)
	if err != nil {
		return nil, err
	}
	tc := tls.Client(rc, cfg)
	hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := tc.HandshakeContext(hctx); err != nil {
		return nil, fmt.Errorf("tls handshake: %w", err)
	}
	return tc, nil
}

// ServerTLS wraps an inbound relay stream with pinned TLS 1.3 (D26).
func ServerTLS(ctx context.Context, rc net.Conn, key *identity.Key, peer ed25519.PublicKey) (net.Conn, error) {
	cfg, err := identity.ServerTLS(key, peer)
	if err != nil {
		return nil, err
	}
	tc := tls.Server(rc, cfg)
	hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := tc.HandshakeContext(hctx); err != nil {
		_ = rc.Close()
		return nil, fmt.Errorf("tls handshake: %w", err)
	}
	return tc, nil
}

// readyURL turns the server's relay_ready payload into the absolute WebSocket
// URL the relay room expects (path plus one-time token).
func readyURL(signalURL string, ready signaling.RelayReady) string {
	u, err := relay.RelayURL(signalURL, ready.RelayURL, ready.Token)
	if err != nil {
		return ""
	}
	return u
}
