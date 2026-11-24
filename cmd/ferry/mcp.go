package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cwlindean/filetransfer/core/client"
	"github.com/cwlindean/filetransfer/core/discovery"
	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/ipc"
	"github.com/cwlindean/filetransfer/core/pairing"
	"github.com/cwlindean/filetransfer/core/proto"
	"github.com/cwlindean/filetransfer/core/signaling"
)

const mcpDefaultSignal = client.DefaultSignalURL

type mcpConfig struct {
	KeyPath    string
	PairedPath string
	OutDir     string
	SignalURL  string
	Name       string
	STUN       []string
}

func (c mcpConfig) canUseIPC() bool {
	return c.KeyPath == client.DefaultKeyPath() &&
		c.PairedPath == client.DefaultPairedPath() &&
		c.SignalURL == client.DefaultSignalURL
}

// runMCP starts a newline-delimited JSON-RPC 2.0 MCP server on stdin/stdout so
// AI agents can list peers, pair, send files/text, and receive files cleanly.
func runMCP(args []string) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	keyPath := fs.String("key", client.DefaultKeyPath(), "device key file")
	pairedPath := fs.String("paired", client.DefaultPairedPath(), "trusted peer table")
	outDir := fs.String("out", mcpDefaultOutDir(), "default directory for received files")
	signalURL := fs.String("signal", mcpDefaultSignal, "signaling server WebSocket URL")
	name := fs.String("name", defaultName(), "device name shown to peers")
	stun := fs.String("stun", defaultSTUN, "comma-separated STUN servers")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg := mcpConfig{
		KeyPath:    *keyPath,
		PairedPath: *pairedPath,
		OutDir:     *outDir,
		SignalURL:  *signalURL,
		Name:       *name,
		STUN:       splitCSV(*stun),
	}
	if err := serveMCP(os.Stdin, os.Stdout, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "ferry mcp: %v\n", err)
		return 1
	}
	return 0
}

func mcpDefaultOutDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "received"
	}
	return filepath.Join(home, "Downloads")
}

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type mcpWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (m *mcpWriter) send(resp jsonRPCResponse) error {
	b, err := marshalClean(resp)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	m.mu.Lock()
	defer m.mu.Unlock()
	_, err = m.w.Write(b)
	return err
}

func marshalClean(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\r\n"), nil
}

func serveMCP(r io.Reader, w io.Writer, cfg mcpConfig) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	mw := &mcpWriter{w: w}
	ctx := context.Background()

	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			_ = mw.send(jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      json.RawMessage("null"),
				Error:   &jsonRPCError{Code: -32700, Message: "parse error: " + err.Error()},
			})
			continue
		}
		if err := handleMCPMessage(ctx, mw, cfg, req); err != nil {
			return err
		}
	}
	return sc.Err()
}

func hasRequestID(id json.RawMessage) bool {
	s := bytes.TrimSpace(id)
	return len(s) > 0 && !bytes.Equal(s, []byte("null"))
}

func handleMCPMessage(ctx context.Context, w *mcpWriter, cfg mcpConfig, req jsonRPCRequest) error {
	reply := hasRequestID(req.ID)
	switch req.Method {
	case "notifications/initialized":
		return nil
	case "initialize":
		if !reply {
			return nil
		}
		var initParams struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &initParams)
		pv := "2025-03-26"
		if initParams.ProtocolVersion == "2024-11-05" || initParams.ProtocolVersion == "2025-03-26" {
			pv = initParams.ProtocolVersion
		}
		return w.send(jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": pv,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "ferry", "version": version},
			},
		})
	case "ping":
		if !reply {
			return nil
		}
		return w.send(jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}})
	case "tools/list":
		if !reply {
			return nil
		}
		return w.send(jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{"tools": mcpToolDefs()},
		})
	case "tools/call":
		if !reply {
			return nil
		}
		var call struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &call); err != nil || call.Name == "" {
			return w.send(jsonRPCResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Error:   &jsonRPCError{Code: -32602, Message: "invalid tools/call params"},
			})
		}
		return w.send(jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  invokeMCPTool(ctx, cfg, call.Name, call.Arguments),
		})
	default:
		if !reply {
			return nil
		}
		return w.send(jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonRPCError{Code: -32601, Message: "method not found: " + req.Method},
		})
	}
}

type mcpToolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func mcpToolDefs() []mcpToolSpec {
	return []mcpToolSpec{
		{
			Name:        "ferry_whoami",
			Description: "Return this device's ID, name, fresh pairing URI, signaling URL, and local key/paired/output paths.",
			InputSchema: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{},
				"additionalProperties": false,
			},
		},
		{
			Name:        "ferry_list_peers",
			Description: "List trusted paired devices and optionally scan for active LAN receivers.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"discover_lan": map[string]any{"type": "boolean", "description": "Scan UDP LAN announcements when true"},
					"timeout_ms":   map[string]any{"type": "integer", "description": "LAN scan duration in ms (default 3000)"},
				},
			},
		},
		{
			Name:        "ferry_send_files",
			Description: "Send local files to a peer identified by paired name, 64-hex device ID, or LAN IP:port.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"peer", "files"},
				"properties": map[string]any{
					"peer":  map[string]any{"type": "string", "description": "Paired device name, 64-hex device ID, or LAN host:port"},
					"files": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Absolute or relative file paths to send"},
					"mode":  map[string]any{"type": "string", "enum": []string{"auto", "lan", "punch", "relay"}, "description": "Transport mode (default auto)"},
				},
			},
		},
		{
			Name:        "ferry_send_text",
			Description: "Send a UTF-8 text note as a temporary .txt file to a peer.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"peer", "text"},
				"properties": map[string]any{
					"peer":     map[string]any{"type": "string", "description": "Paired device name, 64-hex device ID, or LAN host:port"},
					"text":     map[string]any{"type": "string", "description": "UTF-8 text content to send"},
					"filename": map[string]any{"type": "string", "description": "Optional target filename (default note-HHmmss.txt)"},
					"mode":     map[string]any{"type": "string", "enum": []string{"auto", "lan", "punch", "relay"}, "description": "Transport mode (default auto)"},
				},
			},
		},
		{
			Name:        "ferry_receive_once",
			Description: "Wait for a single incoming transfer (auto punch+relay by default, or punch/relay/lan) and save files to out_dir.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"out_dir":     map[string]any{"type": "string", "description": "Destination directory (defaults to configured out_dir)"},
					"mode":        map[string]any{"type": "string", "enum": []string{"auto", "race", "punch", "relay", "lan"}, "description": "Receive mode (default auto)"},
					"timeout_sec": map[string]any{"type": "integer", "description": "Max wait seconds (1-300, default 60)"},
					"listen":      map[string]any{"type": "string", "description": "Bind address host:port for mode=lan (default 0.0.0.0:0)"},
				},
			},
		},
		{
			Name:        "ferry_pair_add",
			Description: "Trust and pair a peer device from a ferry:// PAKE QR URI or legacy ftpair:// URI.",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"uri"},
				"properties": map[string]any{
					"uri": map[string]any{"type": "string", "description": "Peer pairing URI (ferry:// or ftpair://)"},
				},
			},
		},
	}
}

func invokeMCPTool(ctx context.Context, cfg mcpConfig, name string, raw json.RawMessage) map[string]any {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage("{}")
	}
	var (
		res any
		err error
	)
	switch name {
	case "ferry_whoami":
		res, err = mcpWhoami(cfg)
	case "ferry_list_peers":
		res, err = mcpListPeers(ctx, cfg, raw)
	case "ferry_send_files":
		res, err = mcpSendFilesTool(ctx, cfg, raw)
	case "ferry_send_text":
		res, err = mcpSendTextTool(ctx, cfg, raw)
	case "ferry_receive_once":
		res, err = mcpReceiveOnce(ctx, cfg, raw)
	case "ferry_pair_add":
		res, err = mcpPairAdd(ctx, cfg, raw)
	default:
		err = fmt.Errorf("unknown tool: %s", name)
	}
	if err != nil {
		return mcpTextResult(err.Error(), true)
	}
	b, merr := marshalClean(res)
	if merr != nil {
		return mcpTextResult("encode result: "+merr.Error(), true)
	}
	return mcpTextResult(string(b), false)
}

func mcpTextResult(text string, isErr bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isErr,
	}
}

func mcpWhoami(cfg mcpConfig) (any, error) {
	key, err := loadDeviceKey(cfg.KeyPath)
	if err != nil {
		return nil, err
	}
	secret := make([]byte, identity.PairSecretLen)
	if _, err := rand.Read(secret); err != nil {
		return nil, fmt.Errorf("read random: %w", err)
	}
	return map[string]any{
		"device_id":   key.IDHex(),
		"name":        cfg.Name,
		"pair_uri":    identity.PairURI(key.ID(), key.Public(), cfg.Name, secret),
		"key_path":    cfg.KeyPath,
		"paired_path": cfg.PairedPath,
		"out_dir":     cfg.OutDir,
		"signal_url":  cfg.SignalURL,
	}, nil
}

func mcpListPeers(ctx context.Context, cfg mcpConfig, raw json.RawMessage) (any, error) {
	var args struct {
		DiscoverLAN bool `json:"discover_lan"`
		TimeoutMS   int  `json:"timeout_ms"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if !args.DiscoverLAN && cfg.canUseIPC() {
		if cl, derr := ipc.TryConnect(client.DefaultStateDir()); derr == nil {
			if pr, perr := cl.Peers(ctx); perr == nil {
				return map[string]any{
					"paired":     pr.Paired,
					"lan":        pr.LAN,
					"daemon_ipc": true,
				}, nil
			}
		}
	}

	store, err := identity.LoadPaired(cfg.PairedPath)
	if err != nil {
		return nil, err
	}
	lan := make([]map[string]string, 0)
	if args.DiscoverLAN {
		ms := args.TimeoutMS
		if ms <= 0 {
			ms = 3000
		}
		if ms < 200 {
			ms = 200
		} else if ms > 15000 {
			ms = 15000
		}
		lctx, cancel := context.WithTimeout(ctx, time.Duration(ms)*time.Millisecond)
		defer cancel()
		l, err := discovery.Listen(discovery.Config{
			DeviceID: identity.DeviceID(cfg.Name),
			Name:     cfg.Name,
		})
		if err != nil {
			return nil, fmt.Errorf("lan discover: %w", err)
		}
		_ = l.Run(lctx)
		_ = l.Close()
		for _, d := range l.Devices() {
			lan = append(lan, map[string]string{
				"name":  d.Name,
				"addr":  d.TCPAddr,
				"iface": d.Iface,
			})
		}
	}
	return map[string]any{
		"paired": store.List(),
		"lan":    lan,
	}, nil
}

func mcpSendFilesTool(ctx context.Context, cfg mcpConfig, raw json.RawMessage) (any, error) {
	var args struct {
		Peer  string   `json:"peer"`
		Files []string `json:"files"`
		Mode  string   `json:"mode"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	return mcpSendFiles(ctx, cfg, args.Peer, args.Files, args.Mode)
}

func mcpSendTextTool(ctx context.Context, cfg mcpConfig, raw json.RawMessage) (any, error) {
	var args struct {
		Peer     string `json:"peer"`
		Text     string `json:"text"`
		Filename string `json:"filename"`
		Mode     string `json:"mode"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if args.Text == "" {
		return nil, fmt.Errorf("text cannot be empty")
	}
	fname := strings.TrimSpace(args.Filename)
	if fname == "" {
		fname = "note-" + time.Now().Format("150405") + ".txt"
	} else {
		fname = filepath.Base(strings.ReplaceAll(fname, "\\", "/"))
		if fname == "" || fname == "." || fname == ".." {
			return nil, fmt.Errorf("invalid filename")
		}
	}
	tmpDir, err := os.MkdirTemp("", "ferry-mcp-text-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)

	fp := filepath.Join(tmpDir, fname)
	if err := os.WriteFile(fp, []byte(args.Text), 0o600); err != nil {
		return nil, err
	}
	return mcpSendFiles(ctx, cfg, args.Peer, []string{fp}, args.Mode)
}

func mcpSendFiles(ctx context.Context, cfg mcpConfig, peer string, files []string, mode string) (summary, error) {
	if len(files) == 0 {
		return summary{}, fmt.Errorf("files must not be empty")
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = "auto"
	}

	if cfg.canUseIPC() {
		if cl, derr := ipc.TryConnect(client.DefaultStateDir()); derr == nil {
			sres, serr := cl.Send(ctx, ipc.SendRequest{Peer: peer, Files: files, Mode: mode})
			if serr != nil {
				return summary{}, serr
			}
			return summary{
				OK: true, Path: sres.Path, Peer: sres.Peer, TransferID: sres.TransferID,
				Files: sres.Files, Bytes: sres.Bytes, Chunks: sres.Chunks, DurationMS: sres.DurationMS,
			}, nil
		}
	}

	target, kind, err := resolveMCPTarget(cfg.PairedPath, peer)
	if err != nil {
		return summary{}, err
	}
	timeout := 30 * time.Second
	chunk := int(proto.DefaultChunkSize)

	if kind == "lan" {
		if mode != "auto" && mode != "lan" {
			return summary{}, fmt.Errorf("peer %q resolved to LAN address %s; mode %q requires a device ID", peer, target, mode)
		}
		return sendLANTarget(target, "lan", files, chunk, timeout, 30*time.Second, nil)
	}

	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	opts := client.Options{
		SignalURL:   cfg.SignalURL,
		KeyPath:     cfg.KeyPath,
		PairedPath:  cfg.PairedPath,
		Name:        cfg.Name,
		STUNServers: cfg.STUN,
		Chunk:       chunk,
		Timeout:     timeout,
	}

	var res client.Result
	switch mode {
	case "auto", "punch":
		res, err = client.SendAuto(cctx, opts, target, files)
	case "relay":
		res, err = client.SendFiles(cctx, opts, target, files)
	case "lan":
		return summary{}, fmt.Errorf("mode=lan requires a host:port LAN address")
	default:
		return summary{}, fmt.Errorf("unknown mode %q (want auto, lan, relay, or punch)", mode)
	}
	if err != nil {
		return summary{}, err
	}
	peerOut := res.PeerName
	if peerOut == "" {
		peerOut = truncHex(target)
	}
	return summary{
		OK: true, Path: channelPathName(res.Channel), Peer: peerOut, TransferID: res.TransferID,
		Files: res.Files, Bytes: res.Bytes, Chunks: res.Chunks, DurationMS: res.Duration.Milliseconds(),
	}, nil
}

func resolveMCPTarget(pairedPath, rawPeer string) (target, kind string, err error) {
	p := strings.TrimSpace(rawPeer)
	if p == "" {
		return "", "", fmt.Errorf("peer cannot be empty")
	}
	if _, port, serr := net.SplitHostPort(p); serr == nil && port != "" {
		if n, aerr := strconv.Atoi(port); aerr == nil && n > 0 && n <= 65535 {
			return p, "lan", nil
		}
	}
	if len(p) == 64 {
		if _, herr := hex.DecodeString(p); herr == nil {
			return strings.ToLower(p), "id", nil
		}
	}
	store, lerr := identity.LoadPaired(pairedPath)
	if lerr != nil {
		return "", "", lerr
	}
	low := strings.ToLower(p)
	var matches []identity.PairedDevice
	for _, d := range store.List() {
		if strings.EqualFold(d.Name, p) {
			return d.DeviceID, "id", nil
		}
		if strings.HasPrefix(strings.ToLower(d.DeviceID), low) ||
			(d.Name != "" && strings.Contains(strings.ToLower(d.Name), low)) {
			matches = append(matches, d)
		}
	}
	if len(matches) == 1 {
		return matches[0].DeviceID, "id", nil
	}
	if len(matches) > 1 {
		names := make([]string, 0, len(matches))
		for _, m := range matches {
			names = append(names, fmt.Sprintf("%s(%s)", m.Name, truncHex(m.DeviceID)))
		}
		return "", "", fmt.Errorf("ambiguous peer %q matches: %s", p, strings.Join(names, ", "))
	}
	return "", "", fmt.Errorf("peer %q not found in %s and is not a 64-hex ID or host:port", p, pairedPath)
}

func mcpReceiveOnce(ctx context.Context, cfg mcpConfig, raw json.RawMessage) (any, error) {
	var args struct {
		OutDir     string `json:"out_dir"`
		Mode       string `json:"mode"`
		TimeoutSec int    `json:"timeout_sec"`
		Listen     string `json:"listen"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	outDir := strings.TrimSpace(args.OutDir)
	if outDir == "" {
		outDir = cfg.OutDir
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	sec := args.TimeoutSec
	if sec <= 0 {
		sec = 60
	} else if sec > 300 {
		sec = 300
	}
	wait := time.Duration(sec) * time.Second
	mode := strings.ToLower(strings.TrimSpace(args.Mode))
	if mode == "" {
		mode = "auto"
	}

	cctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	timeout := 30 * time.Second
	chunk := int(proto.DefaultChunkSize)

	var (
		sum summary
		err error
	)
	switch mode {
	case "auto", "race", "punch", "relay":
		res, rerr := client.ReceiveOneAuto(cctx, client.ReceiveOptions{
			KeyPath:      cfg.KeyPath,
			SignalURL:    cfg.SignalURL,
			OutDir:       outDir,
			PairedPath:   cfg.PairedPath,
			Name:         cfg.Name,
			STUNServers:  cfg.STUN,
			Chunk:        chunk,
			Timeout:      timeout,
			DisablePunch: mode == "relay",
		})
		if rerr != nil {
			err = rerr
		} else {
			sum = summary{
				OK: true, Path: channelPathName(res.Channel), Peer: res.PeerName,
				TransferID: res.TransferID, Files: res.Files, Bytes: res.Bytes,
				Chunks: res.Chunks, DurationMS: res.Duration.Milliseconds(),
			}
		}
	case "lan":
		listen := strings.TrimSpace(args.Listen)
		if listen == "" {
			listen = "0.0.0.0:0"
		}
		sum, err = recvLAN(cctx, listen, listen != "127.0.0.1:0", outDir, cfg.Name, chunk, timeout, wait, nil, nil)
	default:
		return nil, fmt.Errorf("unknown mode %q (want auto, race, punch, relay, or lan)", mode)
	}
	if err != nil {
		return nil, err
	}

	absOut, _ := filepath.Abs(outDir)
	savedPaths := make([]string, 0, len(sum.Files))
	for _, f := range sum.Files {
		savedPaths = append(savedPaths, filepath.Join(absOut, f))
	}
	return map[string]any{
		"ok":          sum.OK,
		"path":        sum.Path,
		"peer":        sum.Peer,
		"transfer_id": sum.TransferID,
		"files":       sum.Files,
		"saved_paths": savedPaths,
		"bytes":       sum.Bytes,
		"chunks":      sum.Chunks,
		"duration_ms": sum.DurationMS,
	}, nil
}

func mcpPairAdd(ctx context.Context, cfg mcpConfig, raw json.RawMessage) (any, error) {
	var args struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	uri := strings.TrimSpace(args.URI)
	if uri == "" {
		return nil, fmt.Errorf("uri cannot be empty")
	}

	store, err := identity.LoadPaired(cfg.PairedPath)
	if err != nil {
		return nil, err
	}

	if strings.HasPrefix(uri, "ferry://") {
		payload, perr := pairing.ParsePairURI(uri, time.Now().UTC())
		if perr != nil {
			return nil, perr
		}
		key, kerr := loadDeviceKey(cfg.KeyPath)
		if kerr != nil {
			return nil, kerr
		}
		pctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		cl, derr := signaling.Dial(pctx, cfg.SignalURL, key, nil)
		if derr != nil {
			return nil, derr
		}
		defer cl.Close()
		peer, rerr := pairing.RunWithName(pctx, cl, key, store, pairing.Initiate, payload.Code, payload.DeviceID, cfg.Name, 45*time.Second)
		if rerr != nil {
			return nil, rerr
		}
		if serr := store.Save(); serr != nil {
			return nil, serr
		}
		return map[string]any{
			"ok":        true,
			"device_id": peer.ID,
			"name":      peer.Name,
			"paired":    len(store.List()),
		}, nil
	}

	peer, secret, err := identity.ParsePairURI(uri)
	if err != nil {
		return nil, err
	}
	if err := store.Add(peer); err != nil {
		return nil, err
	}
	if err := store.Save(); err != nil {
		return nil, err
	}
	return map[string]any{
		"ok":         true,
		"device_id":  peer.DeviceID,
		"name":       peer.Name,
		"paired":     len(store.List()),
		"has_secret": len(secret) == identity.PairSecretLen,
	}, nil
}
