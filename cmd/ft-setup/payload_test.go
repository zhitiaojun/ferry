//go:build windows

package main

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestGunzipBytesRoundTrip(t *testing.T) {
	want := bytes.Repeat([]byte("ferry-setup-payload-check-"), 1024)
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	if _, err := gw.Write(want); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gw.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	got, err := gunzipBytes(buf.Bytes())
	if err != nil {
		t.Fatalf("gunzipBytes: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("decompressed mismatch: got %d bytes, want %d bytes", len(got), len(want))
	}

	if _, err := gunzipBytes([]byte("not gzip data")); err == nil {
		t.Fatal("expected error for invalid gzip input")
	}
}

func TestExtractMenuPayload(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "menu")
	if err := extractDir("payload/menu", dest); err != nil {
		t.Fatalf("extractDir(payload/menu): %v", err)
	}
	for _, rel := range []string{"AppxManifest.xml", "ft.ico"} {
		info, err := os.Stat(filepath.Join(dest, rel))
		if err != nil {
			t.Fatalf("missing extracted file %s: %v", rel, err)
		}
		if info.Size() == 0 {
			t.Fatalf("extracted file %s is empty", rel)
		}
	}
}
