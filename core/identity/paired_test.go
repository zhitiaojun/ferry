package identity

import (
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
	"testing"
)

func TestPairURIRoundTrip(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	secret := make([]byte, PairSecretLen)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	uri := PairURI(key.ID(), key.Public(), "笔记本电脑", secret)
	peer, got, err := ParsePairURI(uri)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if peer.DeviceID != key.IDHex() {
		t.Fatalf("device id = %s, want %s", peer.DeviceID, key.IDHex())
	}
	if peer.Name != "笔记本电脑" {
		t.Fatalf("name = %q", peer.Name)
	}
	if string(got) != string(secret) {
		t.Fatal("secret mismatch")
	}
	if _, err := peer.PublicKeyBytes(); err != nil {
		t.Fatal(err)
	}
}

func TestParsePairURIRejectsBadID(t *testing.T) {
	key, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	secret := make([]byte, PairSecretLen)
	uri := PairURI(key.ID(), key.Public(), "", secret)
	// Flip one hex digit of the device id so it no longer hashes the pubkey.
	rest := uri[len(PairURIScheme):]
	orig := rest[0]
	repl := byte('0')
	if orig == '0' {
		repl = '1'
	}
	bad := PairURIScheme + string(repl) + rest[1:]
	if _, _, err := ParsePairURI(bad); err == nil {
		t.Fatal("expected mismatch error")
	}
}

func TestPairURIRejectsOtherScheme(t *testing.T) {
	if _, _, err := ParsePairURI("https://example.com/x"); err == nil {
		t.Fatal("expected scheme error")
	}
	if _, _, err := ParsePairURI(PairURIScheme + "abc"); err == nil {
		t.Fatal("expected format error")
	}
}

func TestPairedStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "paired.json")
	store, err := LoadPaired(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(store.List()) != 0 {
		t.Fatal("new store should be empty")
	}

	a, _ := GenerateKey()
	b, _ := GenerateKey()
	sa := hex.EncodeToString(a.Public())
	if err := store.Add(PairedDevice{DeviceID: a.IDHex(), Name: "pc", PubKey: sa}); err != nil {
		t.Fatal(err)
	}
	// A device id that is not the pubkey hash must be refused.
	if err := store.Add(PairedDevice{DeviceID: b.IDHex(), PubKey: sa}); err == nil {
		t.Fatal("expected id/pubkey mismatch")
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}

	again, err := LoadPaired(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := again.Get(a.IDHex())
	if !ok || got.Name != "pc" {
		t.Fatalf("reload lost the peer: %+v", got)
	}
	if len(again.List()) != 1 {
		t.Fatalf("want 1 peer, got %d", len(again.List()))
	}
	if got.PairedAt.IsZero() {
		t.Fatal("PairedAt not filled")
	}
}

func TestUpdatePeerName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "paired.json")
	store, err := LoadPaired(path)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := GenerateKey()
	unknown, _ := GenerateKey()
	if err := store.Add(PairedDevice{
		DeviceID: a.IDHex(),
		PubKey:   hex.EncodeToString(a.Public()),
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(); err != nil {
		t.Fatal(err)
	}

	if UpdatePeerName(path, unknown.IDHex(), "stranger") {
		t.Fatal("UpdatePeerName should ignore unpaired peer")
	}
	if UpdatePeerName(path, a.IDHex(), "   ") {
		t.Fatal("UpdatePeerName should ignore blank name")
	}
	if !UpdatePeerName(path, a.IDHex(), "小米手机") {
		t.Fatal("UpdatePeerName should succeed for paired peer")
	}
	if UpdatePeerName(path, a.IDHex(), "小米手机") {
		t.Fatal("UpdatePeerName should return false when name is unchanged")
	}

	reloaded, err := LoadPaired(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reloaded.Get(a.IDHex())
	if !ok || got.Name != "小米手机" {
		t.Fatalf("got = %+v (ok=%v), want Name=小米手机", got, ok)
	}
	if _, ok := reloaded.Get(unknown.IDHex()); ok {
		t.Fatal("unpaired peer was unexpectedly trusted")
	}
}
