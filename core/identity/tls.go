package identity

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"time"
)

// Certificate builds the self-signed certificate this device presents on the
// pinned TLS channel (D26): the key is the identity key and the common name is
// the device id, so the peer can eyeball it while debugging. Trust comes from
// pinning, never from a CA.
func (k *Key) Certificate() (tls.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return tls.Certificate{}, err
	}
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: k.IDHex()[:16]},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, k.pub, k.priv)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("identity: create certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: k.priv, Leaf: leaf}, nil
}

func sha256Sum(b []byte) [32]byte { return sha256.Sum256(b) }

// checkPinned accepts a peer certificate only if its key is the expected one.
func checkPinned(peer ed25519.PublicKey) func([][]byte, [][]*x509.Certificate) error {
	return func(raw [][]byte, _ [][]*x509.Certificate) error {
		if len(raw) == 0 {
			return errors.New("identity: peer sent no certificate")
		}
		cert, err := x509.ParseCertificate(raw[0])
		if err != nil {
			return fmt.Errorf("identity: parse peer certificate: %w", err)
		}
		pub, ok := cert.PublicKey.(ed25519.PublicKey)
		if !ok {
			return errors.New("identity: peer certificate is not Ed25519")
		}
		sum := sha256Sum(pub)
		want := sha256Sum(peer)
		if !bytes.Equal(sum[:], want[:]) {
			return fmt.Errorf("identity: peer key %s is not the paired key %s",
				hex.EncodeToString(sum[:8]), hex.EncodeToString(want[:8]))
		}
		return nil
	}
}

// ClientTLS returns a config that presents our certificate and pins the peer.
func ClientTLS(k *Key, peer ed25519.PublicKey) (*tls.Config, error) {
	cert, err := k.Certificate()
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates:          []tls.Certificate{cert},
		MinVersion:            tls.VersionTLS13,
		InsecureSkipVerify:    true, // trust is the pin below, not a CA chain
		VerifyPeerCertificate: checkPinned(peer),
	}, nil
}

// ServerTLS returns a config that presents our certificate, demands the peer's
// certificate and pins it.
func ServerTLS(k *Key, peer ed25519.PublicKey) (*tls.Config, error) {
	cert, err := k.Certificate()
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates:          []tls.Certificate{cert},
		MinVersion:            tls.VersionTLS13,
		ClientAuth:            tls.RequireAnyClientCert,
		InsecureSkipVerify:    true,
		VerifyPeerCertificate: checkPinned(peer),
	}, nil
}
