package mobile

import (
	"crypto/rand"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/cwlindean/filetransfer/core/identity"
)

func TestPairedPeersEmptyAndPopulated(t *testing.T) {
	dir := t.TempDir()
	pairedPath := filepath.Join(dir, "paired.json")

	got, err := PairedPeers(pairedPath)
	if err != nil {
		t.Fatalf("empty PairedPeers: %v", err)
	}
	if got != "[]" {
		t.Fatalf("empty PairedPeers = %q, want []", got)
	}

	k, err := identity.LoadOrCreateKey(filepath.Join(dir, "peer.key"))
	if err != nil {
		t.Fatal(err)
	}
	secret := make([]byte, identity.PairSecretLen)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	uri := identity.PairURI(k.ID(), k.Public(), "laptop", secret)
	if err := PairImport(uri, pairedPath); err != nil {
		t.Fatalf("PairImport: %v", err)
	}

	got, err = PairedPeers(pairedPath)
	if err != nil {
		t.Fatalf("populated PairedPeers: %v", err)
	}
	var peers []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal([]byte(got), &peers); err != nil {
		t.Fatalf("unmarshal PairedPeers %q: %v", got, err)
	}
	if len(peers) != 1 || peers[0].ID != k.IDHex() || peers[0].Name != "laptop" {
		t.Fatalf("peers = %+v, want id=%s name=laptop", peers, k.IDHex())
	}

	if err := RenamePairedPeer(pairedPath, k.IDHex(), "  家里电脑\n "); err != nil {
		t.Fatalf("RenamePairedPeer: %v", err)
	}
	got, err = PairedPeers(pairedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(got), &peers); err != nil {
		t.Fatal(err)
	}
	if len(peers) != 1 || peers[0].Name != "家里电脑" {
		t.Fatalf("after rename peers = %+v, want 家里电脑", peers)
	}

	if err := RemovePairedPeer(pairedPath, k.IDHex()); err != nil {
		t.Fatalf("RemovePairedPeer: %v", err)
	}
	got, err = PairedPeers(pairedPath)
	if err != nil {
		t.Fatal(err)
	}
	if got != "[]" {
		t.Fatalf("after remove PairedPeers = %q, want []", got)
	}
}
