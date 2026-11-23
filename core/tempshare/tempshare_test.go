package tempshare_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/lanweb"
	"github.com/cwlindean/filetransfer/core/tempshare"
	"github.com/cwlindean/filetransfer/server"
)

func testKey(t *testing.T) *identity.Key {
	t.Helper()
	k, err := identity.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestUnsignedTempCreateRejected(t *testing.T) {
	s := server.New(nil)
	hs := httptest.NewServer(s.Handler())
	defer hs.Close()

	body, _ := json.Marshal(map[string]any{
		"name": "fake",
		"mode": "send",
		"ts":   time.Now().Unix(),
		"pub":  strings.Repeat("00", 32),
		"sig":  strings.Repeat("00", 64),
	})
	resp, err := http.Post(hs.URL+"/api/temp/create", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("unsigned create status = %d, want 403", resp.StatusCode)
	}
}

func TestTempShareSendAndRecvRoundTrip(t *testing.T) {
	s := server.New(nil)
	hs := httptest.NewServer(s.Handler())
	defer hs.Close()

	key := testKey(t)
	srcDir := t.TempDir()
	srcFile := filepath.Join(srcDir, "hello.txt")
	payload := []byte("ferry temporary share payload 12345")
	if err := os.WriteFile(srcFile, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	// 1. Host in "send" mode.
	hostSend, err := tempshare.StartHost(tempshare.HostOptions{
		BaseURL:     hs.URL,
		Key:         key,
		DeviceName:  "HostPhone",
		Mode:        "send",
		SharedFiles: []string{srcFile},
	})
	if err != nil {
		t.Fatal(err)
	}
	st := hostSend.Status()
	if !st.Active || len(st.Code) != 6 || !strings.HasPrefix(st.ShareURL, hs.URL+"/s/") {
		t.Fatalf("unexpected status: %+v", st)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Resolve by URL and download via LAN.
	sessURL, err := tempshare.ResolveByURL(ctx, st.ShareURL)
	if err != nil {
		t.Fatal(err)
	}
	outLAN := t.TempDir()
	via, saved, err := tempshare.DownloadAll(ctx, sessURL, outLAN)
	if err != nil {
		t.Fatalf("DownloadAll LAN: %v", err)
	}
	if via != "lan" || len(saved) != 1 {
		t.Fatalf("via=%q saved=%v", via, saved)
	}
	got, _ := os.ReadFile(saved[0])
	if !bytes.Equal(got, payload) {
		t.Fatalf("LAN downloaded bytes mismatch")
	}

	// Resolve by 6-digit code and force relay by clearing LAN URLs.
	sessCode, err := tempshare.ResolveByCode(ctx, hs.URL, st.Code)
	if err != nil {
		t.Fatal(err)
	}
	sessCode.LAN = nil
	outRelay := t.TempDir()
	via, saved, err = tempshare.DownloadAll(ctx, sessCode, outRelay)
	if err != nil {
		t.Fatalf("DownloadAll relay: %v", err)
	}
	if via != "relay" || len(saved) != 1 {
		t.Fatalf("via=%q saved=%v", via, saved)
	}
	got, _ = os.ReadFile(saved[0])
	if !bytes.Equal(got, payload) {
		t.Fatalf("relay downloaded bytes mismatch")
	}

	// Close send host and verify resolve returns error.
	_ = hostSend.Close()
	if _, err := tempshare.ResolveByURL(ctx, st.ShareURL); err == nil {
		t.Fatalf("expected closed session resolve to fail")
	}

	// 2. Host in "recv" mode, test relay upload.
	recvDir := t.TempDir()
	uploaded := make(chan lanweb.File, 1)
	hostRecv, err := tempshare.StartHost(tempshare.HostOptions{
		BaseURL:    hs.URL,
		Key:        key,
		DeviceName: "HostPC",
		Mode:       "recv",
		RecvDir:    recvDir,
		OnUploaded: func(f lanweb.File) { uploaded <- f },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer hostRecv.Close()

	sessRecv, err := tempshare.ResolveByURL(ctx, hostRecv.Status().ShareURL)
	if err != nil {
		t.Fatal(err)
	}
	sessRecv.LAN = nil // force relay
	upVia, upNames, err := tempshare.UploadFiles(ctx, sessRecv, []string{srcFile})
	if err != nil {
		t.Fatalf("UploadFiles relay: %v", err)
	}
	if upVia != "relay" || len(upNames) != 1 || upNames[0] != "hello.txt" {
		t.Fatalf("upVia=%q upNames=%v", upVia, upNames)
	}
	select {
	case f := <-uploaded:
		if f.Name != "hello.txt" {
			t.Fatalf("uploaded name = %q", f.Name)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for OnUploaded callback")
	}
}
