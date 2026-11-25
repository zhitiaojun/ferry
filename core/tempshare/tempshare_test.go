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
	"github.com/cwlindean/filetransfer/core/transfer"
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

	key := testKey(t)
	cases := []struct {
		name string
		pub  string
		sig  string
	}{
		{
			name: "zero_torsion_key_and_sig",
			pub:  strings.Repeat("00", 32),
			sig:  strings.Repeat("00", 64),
		},
		{
			name: "valid_key_bad_sig",
			pub:  key.PubHex(),
			sig:  strings.Repeat("ab", 64),
		},
	}
	for _, tc := range cases {
		body, _ := json.Marshal(map[string]any{
			"name": "fake",
			"mode": "send",
			"ts":   time.Now().Unix(),
			"pub":  tc.pub,
			"sig":  tc.sig,
		})
		resp, err := http.Post(hs.URL+"/api/temp/create", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s: unsigned create status = %d, want 403", tc.name, resp.StatusCode)
		}
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

func TestTempShareProgressCallbacks(t *testing.T) {
	s := server.New(nil)
	hs := httptest.NewServer(s.Handler())
	defer hs.Close()

	key := testKey(t)
	srcDir := t.TempDir()
	srcFile := filepath.Join(srcDir, "payload.bin")
	payload := bytes.Repeat([]byte("tempshare-progress"), 4096)
	if err := os.WriteFile(srcFile, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	var hostSendEvents []tempshare.TransferProgress
	hostSend, err := tempshare.StartHost(tempshare.HostOptions{
		BaseURL:     hs.URL,
		Key:         key,
		DeviceName:  "HostPhone",
		Mode:        "send",
		SharedFiles: []string{srcFile},
		OnProgress:  func(p tempshare.TransferProgress) { hostSendEvents = append(hostSendEvents, p) },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer hostSend.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// LAN download + client progress.
	sessLAN, err := tempshare.ResolveByURL(ctx, hostSend.Status().ShareURL)
	if err != nil {
		t.Fatal(err)
	}
	var clientRecvLAN []tempshare.TransferProgress
	if _, _, err := tempshare.DownloadAllWithProgress(ctx, sessLAN, t.TempDir(), func(p tempshare.TransferProgress) {
		clientRecvLAN = append(clientRecvLAN, p)
	}); err != nil {
		t.Fatal(err)
	}
	assertProgressComplete(t, "clientRecvLAN", clientRecvLAN)
	assertProgressComplete(t, "hostSendLAN", hostSendEvents)

	// Relay download + client progress.
	hostSendEvents = nil
	sessRelay, err := tempshare.ResolveByCode(ctx, hs.URL, hostSend.Status().Code)
	if err != nil {
		t.Fatal(err)
	}
	sessRelay.LAN = nil
	var clientRecvRelay []tempshare.TransferProgress
	if _, _, err := tempshare.DownloadAllWithProgress(ctx, sessRelay, t.TempDir(), func(p tempshare.TransferProgress) {
		clientRecvRelay = append(clientRecvRelay, p)
	}); err != nil {
		t.Fatal(err)
	}
	assertProgressComplete(t, "clientRecvRelay", clientRecvRelay)
	assertProgressComplete(t, "hostSendRelay", hostSendEvents)

	_ = hostSend.Close()

	// Recv host + LAN/Relay uploads.
	var hostRecvEvents []tempshare.TransferProgress
	hostRecv, err := tempshare.StartHost(tempshare.HostOptions{
		BaseURL:    hs.URL,
		Key:        key,
		DeviceName: "HostPC",
		Mode:       "recv",
		RecvDir:    t.TempDir(),
		OnProgress: func(p tempshare.TransferProgress) { hostRecvEvents = append(hostRecvEvents, p) },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer hostRecv.Close()

	sessUpLAN, err := tempshare.ResolveByURL(ctx, hostRecv.Status().ShareURL)
	if err != nil {
		t.Fatal(err)
	}
	var clientUpLAN []tempshare.TransferProgress
	if _, _, err := tempshare.UploadFilesWithProgress(ctx, sessUpLAN, []string{srcFile}, func(p tempshare.TransferProgress) {
		clientUpLAN = append(clientUpLAN, p)
	}); err != nil {
		t.Fatal(err)
	}
	assertProgressComplete(t, "clientUpLAN", clientUpLAN)
	assertProgressComplete(t, "hostRecvLAN", hostRecvEvents)

	hostRecvEvents = nil
	sessUpRelay, err := tempshare.ResolveByURL(ctx, hostRecv.Status().ShareURL)
	if err != nil {
		t.Fatal(err)
	}
	sessUpRelay.LAN = nil
	var clientUpRelay []tempshare.TransferProgress
	if _, _, err := tempshare.UploadFilesWithProgress(ctx, sessUpRelay, []string{srcFile}, func(p tempshare.TransferProgress) {
		clientUpRelay = append(clientUpRelay, p)
	}); err != nil {
		t.Fatal(err)
	}
	assertProgressComplete(t, "clientUpRelay", clientUpRelay)
	assertProgressComplete(t, "hostRecvRelay", hostRecvEvents)
}

func assertProgressComplete(t *testing.T, label string, events []tempshare.TransferProgress) {
	t.Helper()
	if len(events) < 2 {
		t.Fatalf("%s: expected >=2 progress events, got %d (%+v)", label, len(events), events)
	}
	if !events[len(events)-1].Done {
		t.Fatalf("%s: expected final event Done=true, got %+v", label, events[len(events)-1])
	}
}

func TestTempShareUploadDownloadControl(t *testing.T) {
	s := server.New(nil)
	hs := httptest.NewServer(s.Handler())
	defer hs.Close()

	key := testKey(t)
	srcDir := t.TempDir()
	srcFile := filepath.Join(srcDir, "ctrl.bin")
	payload := bytes.Repeat([]byte("tempshare-control"), 8192)
	if err := os.WriteFile(srcFile, payload, 0o644); err != nil {
		t.Fatal(err)
	}

	hostRecv, err := tempshare.StartHost(tempshare.HostOptions{
		BaseURL:    hs.URL,
		Key:        key,
		DeviceName: "HostPC",
		Mode:       "recv",
		RecvDir:    t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer hostRecv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Cancel immediately returns ErrCancelled.
	sess, err := tempshare.ResolveByURL(ctx, hostRecv.Status().ShareURL)
	if err != nil {
		t.Fatal(err)
	}
	cCancel := transfer.NewController()
	cCancel.Cancel()
	if _, _, uerr := tempshare.UploadFilesWithControl(ctx, sess, []string{srcFile}, cCancel, nil); !strings.Contains(uerr.Error(), "cancelled") {
		t.Fatalf("expected ErrCancelled, got %v", uerr)
	}

	// Pause then resume over relay upload.
	sessRelay, err := tempshare.ResolveByURL(ctx, hostRecv.Status().ShareURL)
	if err != nil {
		t.Fatal(err)
	}
	sessRelay.LAN = nil
	cPause := transfer.NewController()
	cPause.Pause()
	time.AfterFunc(120*time.Millisecond, cPause.Resume)
	var sawPaused bool
	via, up, err := tempshare.UploadFilesWithControl(ctx, sessRelay, []string{srcFile}, cPause, func(p tempshare.TransferProgress) {
		if p.Stage == "paused" {
			sawPaused = true
		}
	})
	if err != nil || via != "relay" || len(up) != 1 || !sawPaused {
		t.Fatalf("pause/resume upload via=%q up=%v err=%v sawPaused=%v", via, up, err, sawPaused)
	}
}
