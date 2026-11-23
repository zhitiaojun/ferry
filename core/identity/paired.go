package identity

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// PairURIScheme is the prefix of a pairing URI (the payload of the QR code,
// protocol §8.6). The secret in it is what the scanner proves possession of
// once in-band pairing lands (M6); M3 only stores the peer public key.
const PairURIScheme = "ftpair://v1/"

// PairSecretLen is the length of the one-time pairing secret.
const PairSecretLen = 16

// PairedDevice is a peer whose identity key we trust.
type PairedDevice struct {
	DeviceID string    `json:"device_id"`
	Name     string    `json:"name,omitempty"`
	PubKey   string    `json:"pubkey"`
	PairedAt time.Time `json:"paired_at"`
}

// PairURI encodes the payload shown as a QR code:
//
//	ftpair://v1/<device_id_hex>/<pubkey_hex>/<secret_b64url>?name=<name>
func PairURI(id [32]byte, pub ed25519.PublicKey, name string, secret []byte) string {
	u := PairURIScheme + hex.EncodeToString(id[:]) + "/" + hex.EncodeToString(pub) +
		"/" + base64.RawURLEncoding.EncodeToString(secret)
	if name != "" {
		u += "?name=" + url.QueryEscape(name)
	}
	return u
}

// ParsePairURI decodes a pairing URI and checks that the device id really is
// the hash of the public key it carries.
func ParsePairURI(s string) (PairedDevice, []byte, error) {
	if !strings.HasPrefix(s, PairURIScheme) {
		return PairedDevice{}, nil, errors.New("identity: not a pairing URI")
	}
	rest := strings.TrimPrefix(s, PairURIScheme)
	name := ""
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		q, err := url.ParseQuery(rest[i+1:])
		if err != nil {
			return PairedDevice{}, nil, fmt.Errorf("identity: bad pairing query: %w", err)
		}
		name = q.Get("name")
		rest = rest[:i]
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 3 {
		return PairedDevice{}, nil, errors.New("identity: pairing URI needs id, pubkey and secret")
	}
	id, err := hex.DecodeString(parts[0])
	if err != nil || len(id) != 32 {
		return PairedDevice{}, nil, errors.New("identity: bad device id in pairing URI")
	}
	pub, err := hex.DecodeString(parts[1])
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return PairedDevice{}, nil, errors.New("identity: bad public key in pairing URI")
	}
	secret, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(secret) != PairSecretLen {
		return PairedDevice{}, nil, errors.New("identity: bad secret in pairing URI")
	}
	sum := sha256.Sum256(pub)
	if !hmac.Equal(sum[:], id) {
		return PairedDevice{}, nil, errors.New("identity: device id does not match public key")
	}
	return PairedDevice{
		DeviceID: hex.EncodeToString(id),
		Name:     name,
		PubKey:   hex.EncodeToString(pub),
		PairedAt: time.Now().UTC(),
	}, secret, nil
}

// PairProof is the HMAC a scanner sends for the one-time secret (M6 uses it;
// M3 stores the secret so the check can be added without changing the URI).
func PairProof(secret []byte, nonce string) []byte {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(nonce))
	return m.Sum(nil)
}

// PairedStore is the small on-disk table of trusted peers. It is JSON so the
// file stays inspectable; writes are atomic (temp file + rename).
type PairedStore struct {
	path    string
	mu      sync.Mutex
	devices map[string]PairedDevice
}

// LoadPaired reads the trusted-peer table, treating a missing file as empty.
func LoadPaired(path string) (*PairedStore, error) {
	s := &PairedStore{path: path, devices: map[string]PairedDevice{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("identity: read paired table: %w", err)
	}
	var list []PairedDevice
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("identity: parse paired table: %w", err)
	}
	for _, d := range list {
		s.devices[d.DeviceID] = d
	}
	return s, nil
}

// Add records a trusted peer, rejecting entries whose id does not match their
// public key.
func (s *PairedStore) Add(d PairedDevice) error {
	pub, err := hex.DecodeString(d.PubKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("identity: bad public key")
	}
	sum := sha256.Sum256(pub)
	if hex.EncodeToString(sum[:]) != d.DeviceID {
		return errors.New("identity: device id does not match public key")
	}
	if d.PairedAt.IsZero() {
		d.PairedAt = time.Now().UTC()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.devices[d.DeviceID] = d
	return nil
}

// Get looks a peer up by device id.
func (s *PairedStore) Get(id string) (PairedDevice, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.devices[id]
	return d, ok
}

// Remove deletes a peer from the store, returning true if it was present.
func (s *PairedStore) Remove(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.devices[id]; !ok {
		return false
	}
	delete(s.devices, id)
	return true
}

// List returns the trusted peers sorted by name then id.
func (s *PairedStore) List() []PairedDevice {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]PairedDevice, 0, len(s.devices))
	for _, d := range s.devices {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return out[i].DeviceID < out[j].DeviceID
	})
	return out
}

// Save writes the table atomically with owner-only permissions.
func (s *PairedStore) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := make([]PairedDevice, 0, len(s.devices))
	for _, d := range s.devices {
		list = append(list, d)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].DeviceID < list[j].DeviceID })
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".paired-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, s.path)
}

// UpdatePeerName refreshes the stored friendly name of an already-paired
// device. Unpaired device IDs and blank names are ignored.
func UpdatePeerName(pairedPath, peerID, name string) bool {
	name = strings.TrimSpace(name)
	if pairedPath == "" || peerID == "" || name == "" {
		return false
	}
	store, err := LoadPaired(pairedPath)
	if err != nil {
		return false
	}
	dev, ok := store.Get(peerID)
	if !ok || dev.Name == name {
		return false
	}
	dev.Name = name
	if err := store.Add(dev); err != nil {
		return false
	}
	return store.Save() == nil
}

// RenamePeer sets or clears the stored display name for a paired peer on disk.
// Unknown peer IDs return (false, nil).
func RenamePeer(pairedPath, peerID, name string) (bool, error) {
	if pairedPath == "" || peerID == "" {
		return false, nil
	}
	store, err := LoadPaired(pairedPath)
	if err != nil {
		return false, err
	}
	dev, ok := store.Get(peerID)
	if !ok {
		return false, nil
	}
	dev.Name = strings.TrimSpace(name)
	if err := store.Add(dev); err != nil {
		return false, err
	}
	if err := store.Save(); err != nil {
		return false, err
	}
	return true, nil
}

// RemovePeer deletes a paired peer from pairedPath and persists the change.
// Unknown peer IDs return (false, nil).
func RemovePeer(pairedPath, peerID string) (bool, error) {
	if pairedPath == "" || peerID == "" {
		return false, nil
	}
	store, err := LoadPaired(pairedPath)
	if err != nil {
		return false, err
	}
	if !store.Remove(peerID) {
		return false, nil
	}
	if err := store.Save(); err != nil {
		return false, err
	}
	return true, nil
}

// PublicKeyBytes decodes the stored hex public key.
func (d PairedDevice) PublicKeyBytes() (ed25519.PublicKey, error) {
	pub, err := hex.DecodeString(d.PubKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return nil, errors.New("identity: bad stored public key")
	}
	return ed25519.PublicKey(pub), nil
}
