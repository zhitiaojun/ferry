// Package identity derives device identifiers and nonces.
//
// M1 only needs a stable, non-cryptographic device id for local testing: the
// ED25519 keypair and pairing flow arrive in M3 (D26). The wire format already
// carries a 32-byte device id and a 16-byte nonce, so nothing here is throwaway.
package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
)

// DeviceID derives a stable 32-byte id from a local label (for M1: hostname).
// It is not a public key and must not be treated as an authenticated identity.
func DeviceID(label string) [32]byte {
	return sha256.Sum256([]byte("ft-device-v1:" + label))
}

// Nonce returns a cryptographically random 16-byte nonce.
func Nonce() ([16]byte, error) {
	var n [16]byte
	if _, err := rand.Read(n[:]); err != nil {
		return n, fmt.Errorf("read random nonce: %w", err)
	}
	return n, nil
}
