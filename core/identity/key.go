package identity

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Key is a device identity: an Ed25519 keypair plus the 32-byte device id
// derived from the public key (SHA-256). This is the authenticated identity
// used by the signaling server (protocol §8.2); the label-derived DeviceID
// below is only for M1/M2 LAN testing and never proves possession.
type Key struct {
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
	id   [32]byte
}

// GenerateKey returns a fresh random identity.
func GenerateKey() (*Key, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519 key: %w", err)
	}
	return keyFromPair(pub, priv), nil
}

// KeyFromSeed derives an identity from a 32-byte seed (tests, deterministic fixtures).
func KeyFromSeed(seed []byte) (*Key, error) {
	if len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("identity: seed must be %d bytes, got %d", ed25519.SeedSize, len(seed))
	}
	priv := ed25519.NewKeyFromSeed(seed)
	pub := priv.Public().(ed25519.PublicKey)
	return keyFromPair(pub, priv), nil
}

func keyFromPair(pub ed25519.PublicKey, priv ed25519.PrivateKey) *Key {
	return &Key{priv: priv, pub: pub, id: sha256.Sum256(pub)}
}

// Public returns the public key.
func (k *Key) Public() ed25519.PublicKey { return k.pub }

// ID returns the device id: SHA-256 of the public key.
func (k *Key) ID() [32]byte { return k.id }

// IDHex returns the device id as 64 lowercase hex characters.
func (k *Key) IDHex() string { return hex32(k.id) }

// PubHex returns the public key as 64 lowercase hex characters.
func (k *Key) PubHex() string {
	return hex32([32]byte(k.pub))
}

// Sign signs msg with the private key.
func (k *Key) Sign(msg []byte) []byte { return ed25519.Sign(k.priv, msg) }

// LoginMessage builds the exact bytes the client signs for signaling login
// (protocol §8.2): "FT-SIGNAL-V1\n<device_id_hex>\n<nonce_hex>".
func LoginMessage(deviceID [32]byte, nonceHex string) []byte {
	return []byte("FT-SIGNAL-V1\n" + hex32(deviceID) + "\n" + nonceHex)
}

// VerifyLogin checks that sig is a valid signature over LoginMessage(id, nonce)
// by pub, and that SHA-256(pub) == id.
func VerifyLogin(pub ed25519.PublicKey, id [32]byte, nonceHex string, sig []byte) error {
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("identity: bad public key length %d", len(pub))
	}
	if sum := sha256.Sum256(pub); sum != id {
		return fmt.Errorf("identity: device id is not sha256(pubkey)")
	}
	if !ed25519.Verify(pub, LoginMessage(id, nonceHex), sig) {
		return errors.New("identity: bad signature")
	}
	return nil
}

// MarshalPEM encodes the private key as PKCS#8 PEM.
func (k *Key) MarshalPEM() ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(k.priv)
	if err != nil {
		return nil, fmt.Errorf("marshal private key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// ParseKeyPEM parses a PKCS#8 PEM Ed25519 private key.
func ParseKeyPEM(data []byte) (*Key, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("identity: no PEM block found")
	}
	v, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("identity: parse private key: %w", err)
	}
	priv, ok := v.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("identity: unexpected key type %T", v)
	}
	return keyFromPair(priv.Public().(ed25519.PublicKey), priv), nil
}

// Save writes the private key to path with 0600 permissions (parent dir 0700).
func (k *Key) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create key dir: %w", err)
	}
	data, err := k.MarshalPEM()
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write key: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("install key: %w", err)
	}
	return nil
}

// LoadKey reads a key saved by Save.
func LoadKey(path string) (*Key, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read key: %w", err)
	}
	return ParseKeyPEM(data)
}

// LoadOrCreateKey loads path, generating and saving a new key when the file
// does not exist yet. An existing but unreadable file is an error, never
// silently replaced (that would change the device id).
func LoadOrCreateKey(path string) (*Key, error) {
	k, err := LoadKey(path)
	if err == nil {
		return k, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	k, err = GenerateKey()
	if err != nil {
		return nil, err
	}
	if err := k.Save(path); err != nil {
		return nil, err
	}
	return k, nil
}

func hex32(b [32]byte) string { return hex.EncodeToString(b[:]) }
