package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveStateDirsAndMigrateLegacyState(t *testing.T) {
	wDir, wOld := resolveStateDirs("windows", `C:\Users\u\AppData\Local`, `C:\Users\u`)
	if wDir != filepath.Join(`C:\Users\u\AppData\Local`, "Ferry") || wOld != filepath.Join(`C:\Users\u\AppData\Local`, "ft") {
		t.Fatalf("unexpected windows dirs: dir=%q old=%q", wDir, wOld)
	}
	lDir, lOld := resolveStateDirs("linux", "", "/home/u")
	if lDir != filepath.Join("/home/u", ".ferry") || lOld != filepath.Join("/home/u", ".ft") {
		t.Fatalf("unexpected linux dirs: dir=%q old=%q", lDir, lOld)
	}

	tmp := t.TempDir()
	oldDir := filepath.Join(tmp, "ft")
	newDir := filepath.Join(tmp, "Ferry")
	if err := os.MkdirAll(oldDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "device.key"), []byte("KEY-DATA"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(oldDir, "paired.json"), []byte(`{"devices":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	migrateLegacyState(oldDir, newDir)

	gotKey, err := os.ReadFile(filepath.Join(newDir, "device.key"))
	if err != nil || string(gotKey) != "KEY-DATA" {
		t.Fatalf("migrated key mismatch: %q, err=%v", string(gotKey), err)
	}
	gotPaired, err := os.ReadFile(filepath.Join(newDir, "paired.json"))
	if err != nil || string(gotPaired) != `{"devices":[]}` {
		t.Fatalf("migrated paired mismatch: %q, err=%v", string(gotPaired), err)
	}

	// Existing files in newDir must never be overwritten.
	if err := os.WriteFile(filepath.Join(newDir, "device.key"), []byte("NEW-KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	migrateLegacyState(oldDir, newDir)
	gotKey, _ = os.ReadFile(filepath.Join(newDir, "device.key"))
	if string(gotKey) != "NEW-KEY" {
		t.Fatalf("existing destination overwritten: %q", string(gotKey))
	}
}
