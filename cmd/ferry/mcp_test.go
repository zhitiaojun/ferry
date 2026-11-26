package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhitiaojun/ferry/core/identity"
	"github.com/zhitiaojun/ferry/core/proto"
)

func TestServeMCPInitializeListWhoamiPairAndPeers(t *testing.T) {
	dir := t.TempDir()
	cfg := mcpConfig{
		KeyPath:    filepath.Join(dir, "device.key"),
		PairedPath: filepath.Join(dir, "paired.json"),
		OutDir:     filepath.Join(dir, "out"),
		SignalURL:  mcpDefaultSignal,
		Name:       "test-agent-pc",
	}

	peerKey, err := identity.LoadOrCreateKey(filepath.Join(dir, "peer.key"))
	if err != nil {
		t.Fatal(err)
	}
	sec := make([]byte, identity.PairSecretLen)
	_, _ = rand.Read(sec)
	peerURI := identity.PairURI(peerKey.ID(), peerKey.Public(), "Pixel-9", sec)

	reqs := []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"ferry_whoami","arguments":{}}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"ferry_pair_add","arguments":{"uri":"` + peerURI + `"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"ferry_list_peers","arguments":{}}}`,
	}
	var out bytes.Buffer
	if err := serveMCP(strings.NewReader(strings.Join(reqs, "\n")+"\n"), &out, cfg); err != nil {
		t.Fatalf("serveMCP: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("expected 5 responses (notification skipped), got %d:\n%s", len(lines), out.String())
	}

	var initResp struct {
		Result struct {
			ProtocolVersion string `json:"protocolVersion"`
			ServerInfo      struct {
				Name string `json:"name"`
			} `json:"serverInfo"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &initResp); err != nil {
		t.Fatal(err)
	}
	if initResp.Result.ProtocolVersion != "2025-03-26" || initResp.Result.ServerInfo.Name != "ferry" {
		t.Fatalf("unexpected init result: %+v", initResp)
	}

	var listResp struct {
		Result struct {
			Tools []mcpToolSpec `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[1]), &listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Result.Tools) != 6 {
		t.Fatalf("want 6 tools, got %d", len(listResp.Result.Tools))
	}
	for _, tl := range listResp.Result.Tools {
		if !strings.HasPrefix(tl.Name, "ferry_") {
			t.Fatalf("unexpected non-ferry tool name: %q", tl.Name)
		}
	}

	var peersResp struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[4]), &peersResp); err != nil {
		t.Fatal(err)
	}
	if peersResp.Result.IsError || len(peersResp.Result.Content) == 0 || !strings.Contains(peersResp.Result.Content[0].Text, "Pixel-9") {
		t.Fatalf("unexpected ferry_list_peers response: %s", lines[4])
	}
}

func TestServeMCPSendTextEndToEnd(t *testing.T) {
	dir := t.TempDir()
	recvDir := filepath.Join(dir, "recv")
	if err := os.MkdirAll(recvDir, 0o755); err != nil {
		t.Fatal(err)
	}

	addrCh := make(chan string, 1)
	recvRes := make(chan summary, 1)
	recvErr := make(chan error, 1)
	go func() {
		sum, err := recvLAN(context.Background(), "127.0.0.1:0", false, recvDir, "recv-pc",
			int(proto.DefaultChunkSize), 10*time.Second, 10*time.Second, func(a string) {
				addrCh <- a
			}, nil)
		if err != nil {
			recvErr <- err
			return
		}
		recvRes <- sum
	}()

	addr := <-addrCh
	cfg := mcpConfig{
		KeyPath:    filepath.Join(dir, "device.key"),
		PairedPath: filepath.Join(dir, "paired.json"),
		OutDir:     filepath.Join(dir, "out"),
		SignalURL:  mcpDefaultSignal,
		Name:       "send-pc",
	}
	req := `{"jsonrpc":"2.0","id":10,"method":"tools/call","params":{"name":"ferry_send_text","arguments":{"peer":"` + addr + `","text":"hello agent mcp","filename":"agent-note.txt"}}}` + "\n"
	var out bytes.Buffer
	if err := serveMCP(strings.NewReader(req), &out, cfg); err != nil {
		t.Fatalf("serveMCP: %v", err)
	}

	select {
	case err := <-recvErr:
		t.Fatalf("receiver error: %v", err)
	case sum := <-recvRes:
		if !sum.OK || len(sum.Files) != 1 || sum.Files[0] != "agent-note.txt" {
			t.Fatalf("unexpected receiver summary: %+v", sum)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for receiver")
	}

	got, err := os.ReadFile(filepath.Join(recvDir, "agent-note.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello agent mcp" {
		t.Fatalf("received text = %q, want %q", string(got), "hello agent mcp")
	}

	var callResp struct {
		Result struct {
			IsError bool `json:"isError"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &callResp); err != nil {
		t.Fatalf("unmarshal response: %v (%s)", err, out.String())
	}
	if callResp.Result.IsError {
		t.Fatalf("tool call returned error: %s", callResp.Result.Content[0].Text)
	}
}
