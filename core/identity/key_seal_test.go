package identity

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestKeyFileIsSealedOnWindows(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "device.key")
	k, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Save(path); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if sealAvailable() {
		if !bytes.HasPrefix(raw, []byte(keySealMagic)) {
			t.Fatal("key file is not marked as sealed")
		}
		if bytes.Contains(raw, []byte("PRIVATE KEY")) {
			t.Fatal("sealed key file still contains PEM plaintext")
		}
	}
	back, err := LoadKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if back.ID() != k.ID() {
		t.Fatal("round trip changed the device id")
	}
}

func TestLoadLegacyPlainPEM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.key")
	k, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	pemBytes, err := k.MarshalPEM()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	back, err := LoadKey(path)
	if err != nil {
		t.Fatalf("legacy PEM must still load: %v", err)
	}
	if back.ID() != k.ID() {
		t.Fatal("legacy load changed the device id")
	}
}
