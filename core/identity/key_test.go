package identity

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoginMessageFormat(t *testing.T) {
	var id [32]byte
	for i := range id {
		id[i] = byte(i)
	}
	got := string(LoginMessage(id, "a1b2"))
	want := "FT-SIGNAL-V1\n000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f\na1b2"
	if got != want {
		t.Fatalf("login message = %q, want %q", got, want)
	}
}

func TestSignAndVerifyLogin(t *testing.T) {
	k, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	nonce := "00112233445566778899aabbccddeeff"
	sig := k.Sign(LoginMessage(k.ID(), nonce))

	if err := VerifyLogin(k.Public(), k.ID(), nonce, sig); err != nil {
		t.Fatalf("verify fresh signature: %v", err)
	}
	if err := VerifyLogin(k.Public(), k.ID(), "ffeeddccbbaa99887766554433221100", sig); err == nil {
		t.Fatal("verify accepted a different nonce")
	}
	other, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyLogin(k.Public(), other.ID(), nonce, sig); err == nil {
		t.Fatal("verify accepted a device id that is not sha256(pubkey)")
	}
	bad := bytes.Clone(sig)
	bad[0] ^= 0xff
	if err := VerifyLogin(k.Public(), k.ID(), nonce, bad); err == nil {
		t.Fatal("verify accepted a corrupted signature")
	}
}

func TestKeyFromSeedIsDeterministic(t *testing.T) {
	seed := bytes.Repeat([]byte{7}, ed25519.SeedSize)
	a, err := KeyFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	b, err := KeyFromSeed(seed)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID() != b.ID() || a.PubHex() != b.PubHex() {
		t.Fatal("same seed produced different identities")
	}
	if len(a.IDHex()) != 64 {
		t.Fatalf("device id hex length = %d, want 64", len(a.IDHex()))
	}
	if _, err := hex.DecodeString(a.PubHex()); err != nil {
		t.Fatalf("pubkey hex: %v", err)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "device.key")

	k, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("key file mode = %o, want 600", perm)
		}
	}

	again, err := LoadKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID() != k.ID() {
		t.Fatal("reloaded key has a different device id")
	}

	// LoadOrCreate on an existing file must not regenerate.
	third, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if third.ID() != k.ID() {
		t.Fatal("LoadOrCreateKey replaced an existing key")
	}
}

func TestLoadOrCreateRejectsCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "device.key")
	if err := os.WriteFile(path, []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateKey(path); err == nil {
		t.Fatal("LoadOrCreateKey accepted a corrupt key file")
	}
}
