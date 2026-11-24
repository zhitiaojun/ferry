// Command ferry is the cross-platform CLI and MCP server for Ferry (轻渡).
//
//	ferry send --to-id <peer_id> --file a.bin [--file b.txt]
//	ferry send --discover --file a.bin
//	ferry recv --signal wss://ferry.cwlindean.cn/signal --out ./recv
//	ferry recv --lan --out ./recv
//	ferry daemon
//	ferry mcp
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cwlindean/filetransfer/core/client"
	"github.com/cwlindean/filetransfer/core/discovery"
	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/ipc"
	"github.com/cwlindean/filetransfer/core/lanweb"
	"github.com/cwlindean/filetransfer/core/logging"
	"github.com/cwlindean/filetransfer/core/pairing"
	"github.com/cwlindean/filetransfer/core/pathmgr"
	"github.com/cwlindean/filetransfer/core/proto"
	"github.com/cwlindean/filetransfer/core/session"
	"github.com/cwlindean/filetransfer/core/transfer"
	qrcode "github.com/skip2/go-qrcode"
)

var version = "1.0.0"

var (
	goVersion  = runtime.Version()
	goPlatform = runtime.GOOS + "/" + runtime.GOARCH
)

const defaultSTUN = client.DefaultSTUN

func defaultKeyPath() string { return client.DefaultKeyPath() }

func loadDeviceKey(path string) (*identity.Key, error) {
	k, err := identity.LoadOrCreateKey(path)
	if err != nil {
		return nil, fmt.Errorf("device key %s: %w", path, err)
	}
	return k, nil
}

func truncHex(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func channelPathName(ch string) string {
	switch ch {
	case "LAN":
		return "lan"
	case "打洞":
		return "punch"
	case "中继":
		return "relay"
	default:
		return strings.ToLower(ch)
	}
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "-version", "--version", "version":
		fmt.Printf("Ferry (ferry) %s\nprotocol %d\ngo %s\nplatform %s\n", version, proto.Version, goVersion, goPlatform)
		return
	case "send":
		os.Exit(runSend(os.Args[2:]))
	case "recv":
		os.Exit(runRecv(os.Args[2:]))
	case "whoami":
		os.Exit(runWhoami(os.Args[2:]))
	case "pair":
		os.Exit(runPair(os.Args[2:]))
	case "share":
		os.Exit(runShare(os.Args[2:]))
	case "daemon":
		os.Exit(runDaemon(os.Args[2:]))
	case "mcp":
		os.Exit(runMCP(os.Args[2:]))
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "ferry: unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func runWhoami(args []string) int {
	fs := flag.NewFlagSet("whoami", flag.ContinueOnError)
	keyPath := fs.String("key", defaultKeyPath(), "device key file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	k, err := loadDeviceKey(*keyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ferry whoami: %v\n", err)
		return 1
	}
	fmt.Printf("device_id %s\npubkey %s\nkey %s\n", k.IDHex(), k.PubHex(), *keyPath)
	return 0
}

func usage() {
	fmt.Fprintf(os.Stderr, `usage:
  ferry send --to-id <hex> --file <path> [--file <path>...] [options]
  ferry send --addr <host:port>|--discover --file <path> [options]
  ferry recv [--signal URL] --out <dir> [options]
  ferry share [--send <file>...] [--recv] [options]
  ferry share join <code|url> [--out <dir>] [--file <path>...]
  ferry pair [--show | --add <uri> | --list | --code <6digits> --with <id>|--wait]
  ferry daemon [--port 18800] [--dir DIR] [--signal URL]
  ferry mcp [--key FILE] [--paired FILE] [--out DIR] [--signal URL]
  ferry whoami [--key FILE]
  ferry version

send options:
  --to-id HEX               target 64-hex device ID (auto-uses --signal %s)
  --signal URL              signaling WebSocket URL (default %s when --to-id is set)
  --allow PATHS             allowed paths: auto | lan,punch,relay | punch | relay (default auto)
  --punch                   prefer UDP hole punching (default true in auto mode)
  --stun SERVERS            comma-separated STUN servers (default BJ STUN)
  --key FILE                device key file (default %s)
  --paired FILE             trusted peer table (default %s)
  --no-tls                  disable pinned TLS 1.3 over relay (debug only)
  --no-ipc                  bypass running desktop/daemon IPC
  --addr / --peer ADDR      direct LAN TCP target host:port
  --discover                find peer via LAN broadcast/multicast
  --to NAME                 with --discover: match device name substring
  --discover-timeout D      LAN scan wait (default 5s)
  --retry-for DURATION      retry dropped LAN transfer with .ftpart resume (default 2m)
  --name NAME               local device name shown to peer (default: hostname)
  --log FILE                write JSONL events to FILE
  --timeout DURATION        per-frame wait (default 30s)
  --chunk BYTES             chunk size (default %d)

recv options:
  --signal URL              receive over signaling (punch+relay race + hot upgrade)
  --allow PATHS             auto (default) | punch,relay | relay
  --lan                     listen on 0.0.0.0 and announce on the LAN
  --listen ADDR             explicit TCP listen address host:port
  --out DIR                 output directory (created if missing)
  --web                     serve a browser upload/download page on the LAN
  --web-port PORT           LAN web listen port (0 = random)
  --web-token TOKEN         LAN web path token (random by default)
  --qr                      print an ASCII QR code for the LAN web URL
  same --key/--paired/--stun/--no-tls/--name/--log/--timeout/--chunk as send
`, client.DefaultSignalURL, client.DefaultSignalURL, defaultKeyPath(), defaultPairedPath(), proto.DefaultChunkSize)
}

type fileList []string

func (f *fileList) String() string { return strings.Join(*f, ",") }
func (f *fileList) Set(v string) error {
	if v == "" {
		return errors.New("empty file path")
	}
	*f = append(*f, v)
	return nil
}

type summary struct {
	OK         bool     `json:"ok"`
	Path       string   `json:"path"`
	Topology   string   `json:"topology,omitempty"`
	Peer       string   `json:"peer,omitempty"`
	TransferID string   `json:"transfer_id,omitempty"`
	Files      []string `json:"files,omitempty"`
	Bytes      int64    `json:"bytes"`
	Chunks     int      `json:"chunks"`
	DurationMS int64    `json:"duration_ms"`
	Attempts   int      `json:"attempts,omitempty"`
	Error      string   `json:"error,omitempty"`
}

func runSend(args []string) int {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	var files fileList
	fs.Var(&files, "file", "file to send (repeatable)")
	addr := fs.String("addr", "", "peer address host:port")
	peer := fs.String("peer", "", "peer address (alias of --addr)")
	discover := fs.Bool("discover", false, "find the peer via LAN announcements")
	to := fs.String("to", "", "with --discover: device name substring to pick")
	discoverTimeout := fs.Duration("discover-timeout", 5*time.Second, "how long to wait for announcements")
	allow := fs.String("allow", "auto", "allowed paths: auto, lan, punch, relay")
	name := fs.String("name", defaultName(), "device name")
	logPath := fs.String("log", "", "JSONL log file")
	timeout := fs.Duration("timeout", 30*time.Second, "per-frame wait")
	chunk := fs.Int("chunk", int(proto.DefaultChunkSize), "chunk size in bytes")
	signalURL := fs.String("signal", "", "signaling URL (wss://…/signal)")
	toID := fs.String("to-id", "", "target device id (64 hex)")
	keyPath := fs.String("key", defaultKeyPath(), "device key file")
	pairedPath := fs.String("paired", defaultPairedPath(), "trusted peer table")
	noTLS := fs.Bool("no-tls", false, "relay without the pinned TLS 1.3 layer (debug only)")
	noIPC := fs.Bool("no-ipc", false, "bypass running Ferry daemon IPC")
	punchPath := fs.Bool("punch", true, "with --signal: enable UDP hole punching and background hot upgrade")
	stunServers := fs.String("stun", defaultSTUN, "comma separated STUN servers for --punch")
	retryFor := fs.Duration("retry-for", 2*time.Minute, "keep retrying a dropped LAN transfer this long (resumes from the receiver's .ftpart)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	paths, code := checkAllow(*allow)
	if code != 0 {
		return code
	}
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "ferry send: at least one --file is required")
		return 2
	}
	logger, closeLog, err := openLog(*logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ferry send: %v\n", err)
		return 1
	}
	defer closeLog()

	target := *addr
	if target == "" {
		target = *peer
	}
	if *discover {
		target, err = discoverPeer(logger, *name, *to, *discoverTimeout)
		if err != nil {
			return fail(logger, "discover", err)
		}
	}

	sigURL := strings.TrimSpace(*signalURL)
	if sigURL == "" && *toID != "" {
		sigURL = client.DefaultSignalURL
	}

	if sigURL != "" {
		if *toID == "" {
			fmt.Fprintln(os.Stderr, "ferry send: --to-id is required with --signal")
			return 2
		}

		relayOnly := hasPath(paths, "relay") && !hasPath(paths, "auto") && !hasPath(paths, "punch")
		mode := "auto"
		if relayOnly || !*punchPath {
			mode = "relay"
		}

		// Delegate through local daemon IPC when using default identity & endpoint
		// so we reuse its online signaling session and live LAN peer table.
		if !*noIPC && !*noTLS && *keyPath == defaultKeyPath() && *pairedPath == defaultPairedPath() && sigURL == client.DefaultSignalURL {
			if cl, derr := ipc.TryConnect(client.DefaultStateDir()); derr == nil {
				sres, serr := cl.Send(context.Background(), ipc.SendRequest{
					Peer:  *toID,
					Files: files,
					Mode:  mode,
				})
				if serr != nil {
					return fail(logger, mode, serr)
				}
				printSummary(summary{
					OK: true, Path: sres.Path, Peer: sres.Peer, TransferID: sres.TransferID,
					Files: sres.Files, Bytes: sres.Bytes, Chunks: sres.Chunks, DurationMS: sres.DurationMS,
				})
				return 0
			}
		}

		cctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		opts := client.Options{
			SignalURL:   sigURL,
			KeyPath:     *keyPath,
			PairedPath:  *pairedPath,
			Name:        pairing.SanitizeName(*name),
			STUNServers: splitCSV(*stunServers),
			Chunk:       *chunk,
			Timeout:     *timeout,
			NoTLS:       *noTLS,
			Log:         logger,
		}
		var res client.Result
		if mode == "relay" {
			res, err = client.SendFiles(cctx, opts, *toID, files)
		} else {
			res, err = client.SendAuto(cctx, opts, *toID, files)
		}
		if err != nil {
			return fail(logger, mode, err)
		}
		peerOut := res.PeerName
		if peerOut == "" {
			peerOut = truncHex(*toID)
		}
		printSummary(summary{
			OK: true, Path: channelPathName(res.Channel), Peer: peerOut, TransferID: res.TransferID,
			Files: res.Files, Bytes: res.Bytes, Chunks: res.Chunks, DurationMS: res.Duration.Milliseconds(),
		})
		return 0
	}

	if target == "" {
		fmt.Fprintln(os.Stderr, "ferry send: --to-id, --addr/--peer, or --discover is required")
		return 2
	}

	pathName := "lan"
	if len(paths) > 0 && paths[0] != "auto" {
		pathName = paths[0]
	}
	sum, err := sendLANTarget(target, pathName, files, *chunk, *timeout, *retryFor, logger)
	if err != nil {
		return fail(logger, "transfer", err)
	}
	printSummary(sum)
	return 0
}

func sendLANTarget(target, pathName string, files []string, chunk int, timeout, retryFor time.Duration, logger *logging.Logger) (summary, error) {
	topology := topologyOf(target)
	res, err := sendLAN(target, files, chunk, timeout, retryFor, logger)
	if err != nil {
		return summary{}, err
	}
	return summary{
		OK:         true,
		Path:       pathName,
		Topology:   topology,
		Peer:       res.Peer,
		TransferID: fmt.Sprintf("%x", res.Res.TransferID),
		Files:      res.Res.Files,
		Bytes:      res.Res.Bytes,
		Chunks:     res.Res.Chunks,
		DurationMS: res.Res.Duration.Milliseconds(),
		Attempts:   res.Attempts,
	}, nil
}

func runRecv(args []string) int {
	fs := flag.NewFlagSet("recv", flag.ContinueOnError)
	listen := fs.String("listen", "", "listen address host:port")
	lan := fs.Bool("lan", false, "listen on all interfaces and announce on the LAN")
	out := fs.String("out", ".", "output directory")
	name := fs.String("name", defaultName(), "device name")
	logPath := fs.String("log", "", "JSONL log file")
	timeout := fs.Duration("timeout", 30*time.Second, "per-frame wait")
	chunk := fs.Int("chunk", int(proto.DefaultChunkSize), "chunk size in bytes")
	allow := fs.String("allow", "auto", "allowed paths for --signal: auto, punch, relay")
	signalURL := fs.String("signal", "", "signaling URL (wss://…/signal)")
	keyPath := fs.String("key", defaultKeyPath(), "device key file for --signal")
	pairedPath := fs.String("paired", defaultPairedPath(), "trusted peer table for --signal")
	noTLS := fs.Bool("no-tls", false, "relay without the pinned TLS 1.3 layer (debug only)")
	punchPath := fs.Bool("punch", true, "with --signal: enable UDP hole punching and background hot upgrade")
	stunServers := fs.String("stun", defaultSTUN, "comma separated STUN servers for --punch")
	web := fs.Bool("web", false, "serve a browser page on the LAN instead of the file protocol")
	webPort := fs.Int("web-port", 0, "LAN web listen port (0 = random)")
	webToken := fs.String("web-token", "", "LAN web path token (random by default)")
	webQR := fs.Bool("qr", false, "print an ASCII QR code for the LAN web URL")
	_ = fs.Bool("once", true, "accept one connection (kept for CLI stability)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *out == "" {
		fmt.Fprintln(os.Stderr, "ferry recv: --out is required")
		return 2
	}
	paths, code := checkAllow(*allow)
	if code != 0 {
		return code
	}

	logger, closeLog, err := openLog(*logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ferry recv: %v\n", err)
		return 1
	}
	defer closeLog()

	if *web {
		if err := os.MkdirAll(*out, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "ferry recv: %v\n", err)
			return 1
		}
		srv, err := lanweb.New(*out, *webToken, logger)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ferry recv: %v\n", err)
			return 1
		}
		ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", *webPort))
		if err != nil {
			fmt.Fprintf(os.Stderr, "ferry recv: %v\n", err)
			return 1
		}
		port := ln.Addr().(*net.TCPAddr).Port
		urls := lanURLs(srv.Token(), port)
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"ok": true, "mode": "lanweb", "dir": *out, "port": port, "token": srv.Token(), "urls": urls})
		if *webQR && len(urls) > 0 {
			if qr, err := qrcode.New(urls[0], qrcode.Medium); err == nil {
				fmt.Fprintln(os.Stderr, qr.ToSmallString(false))
				fmt.Fprintf(os.Stderr, "扫这个二维码：%s\n", urls[0])
			}
		}
		if err := srv.ListenAndServe("", ln); err != nil {
			fmt.Fprintf(os.Stderr, "ferry recv: %v\n", err)
			return 1
		}
		return 0
	}

	if *signalURL != "" {
		if err := os.MkdirAll(*out, 0o755); err != nil {
			return fail(logger, "mkdir", err)
		}
		relayOnly := hasPath(paths, "relay") && !hasPath(paths, "auto") && !hasPath(paths, "punch")
		disablePunch := relayOnly || !*punchPath

		cctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		cctx, tcancel := context.WithTimeout(cctx, 5*time.Minute)
		defer tcancel()

		res, err := client.ReceiveOneAuto(cctx, client.ReceiveOptions{
			KeyPath:      *keyPath,
			SignalURL:    *signalURL,
			OutDir:       *out,
			PairedPath:   *pairedPath,
			Name:         pairing.SanitizeName(*name),
			STUNServers:  splitCSV(*stunServers),
			Chunk:        *chunk,
			Timeout:      *timeout,
			DisablePunch: disablePunch,
			NoTLS:        *noTLS,
			Log:          logger,
		})
		if err != nil {
			return fail(logger, "recv", err)
		}
		printSummary(summary{
			OK: true, Path: channelPathName(res.Channel), Peer: res.PeerName, TransferID: res.TransferID,
			Files: res.Files, Bytes: res.Bytes, Chunks: res.Chunks, DurationMS: res.Duration.Milliseconds(),
		})
		return 0
	}

	sum, err := recvLAN(context.Background(), *listen, *lan, *out, *name, *chunk, *timeout, 120*time.Second, func(a string) {
		fmt.Fprintf(os.Stderr, "listening %s\n", a)
	}, logger)
	if err != nil {
		return fail(logger, "recv", err)
	}
	printSummary(sum)
	return 0
}

func recvLAN(parent context.Context, listen string, lan bool, out, name string, chunk int, timeout, acceptTimeout time.Duration, onListen func(string), logger *logging.Logger) (summary, error) {
	if parent == nil {
		parent = context.Background()
	}
	addr := listen
	if addr == "" {
		if lan {
			addr = "0.0.0.0:0"
		} else {
			addr = "127.0.0.1:0"
		}
	}
	p, err := pathmgr.NewTCPListener(addr)
	if err != nil {
		return summary{}, err
	}
	defer p.Close()

	if logger != nil {
		logger.Event("path.listen", map[string]any{"addr": p.Addr(), "path": p.Name(), "lan": lan})
	}
	if onListen != nil {
		onListen(p.Addr())
	}
	announceCtx, stopAnnounce := context.WithCancel(parent)
	defer stopAnnounce()
	if lan {
		tcpPort := listenerPort(p.Addr())
		go func() {
			_ = discovery.Announce(announceCtx, discovery.Config{
				DeviceID: identity.DeviceID(name),
				Name:     name,
				Port:     tcpPort,
				Log:      logger,
			})
		}()
	}

	ctx, cancel := context.WithTimeout(parent, acceptTimeout)
	defer cancel()
	nc, err := p.Accept(ctx)
	if err != nil {
		return summary{}, err
	}
	stopAnnounce()

	s, err := session.Accept(nc, session.Params{
		DeviceID:     identity.DeviceID(name),
		Name:         name,
		Capabilities: proto.CapResume,
	})
	if err != nil {
		_ = nc.Close()
		return summary{}, err
	}
	defer s.Close()

	topology := topologyOfRemote(nc)
	if logger != nil {
		logger.Event("session.ready", map[string]any{"peer": s.Peer.Name, "path": p.Name(), "topology": topology, "addr": nc.RemoteAddr().String()})
	}

	res, err := transfer.Receive(s, out, transfer.Config{
		ChunkSize: uint32(chunk),
		Timeout:   timeout,
		Log:       logger,
	})
	if err != nil {
		return summary{}, err
	}
	return summary{
		OK:         true,
		Path:       p.Name(),
		Topology:   topology,
		Peer:       s.Peer.Name,
		TransferID: fmt.Sprintf("%x", res.TransferID),
		Files:      res.Files,
		Bytes:      res.Bytes,
		Chunks:     res.Chunks,
		DurationMS: res.Duration.Milliseconds(),
	}, nil
}

func hasPath(paths []string, name string) bool {
	for _, p := range paths {
		if p == name {
			return true
		}
	}
	return false
}

func checkAllow(allow string) ([]string, int) {
	var paths []string
	for _, p := range strings.Split(allow, ",") {
		p = strings.TrimSpace(p)
		switch p {
		case "":
			continue
		case "auto", "lan", "relay", "punch":
			paths = append(paths, p)
		default:
			fmt.Fprintf(os.Stderr, "ferry: unknown path %q\n", p)
			return nil, 2
		}
	}
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "ferry: --allow selects no usable path")
		return nil, 2
	}
	return paths, 0
}

func openLog(path string) (*logging.Logger, func(), error) {
	if path == "" {
		return nil, func() {}, nil
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, func() {}, err
	}
	return logging.New(f), func() { _ = f.Close() }, nil
}

func fail(logger *logging.Logger, stage string, err error) int {
	if logger != nil {
		logger.Event("error", map[string]any{"stage": stage, "error": err.Error()})
	}
	enc := json.NewEncoder(os.Stdout)
	_ = enc.Encode(summary{OK: false, Error: err.Error()})
	fmt.Fprintf(os.Stderr, "ferry: %s: %v\n", stage, err)
	return 1
}

func printSummary(s summary) {
	enc := json.NewEncoder(os.Stdout)
	_ = enc.Encode(s)
}

func defaultName() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "ferry-device"
	}
	return h
}

var _ io.Writer = os.Stderr

func listenerPort(addr string) int {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return 0
	}
	return n
}

func discoverPeer(logger *logging.Logger, name, want string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout+time.Second)
	defer cancel()
	l, err := discovery.Listen(discovery.Config{
		DeviceID: identity.DeviceID(name),
		Name:     name,
		Log:      logger,
	})
	if err != nil {
		return "", err
	}
	defer l.Close()
	go func() { _ = l.Run(ctx) }()

	want = strings.ToLower(strings.TrimSpace(want))
	dev, err := l.WaitFor(func(d discovery.Device) bool {
		return want == "" || strings.Contains(strings.ToLower(d.Name), want)
	}, timeout)
	if err != nil {
		seen := make([]string, 0, 4)
		for _, d := range l.Devices() {
			seen = append(seen, fmt.Sprintf("%s@%s", d.Name, d.TCPAddr))
		}
		if len(seen) == 0 {
			return "", fmt.Errorf("no device announced itself within %s", timeout)
		}
		return "", fmt.Errorf("no device matched %q within %s (seen: %s)", want, timeout, strings.Join(seen, ", "))
	}
	if logger != nil {
		logger.Event("discovery.found", map[string]any{"peer": dev.Name, "addr": dev.TCPAddr, "iface": dev.Iface})
	}
	return dev.TCPAddr, nil
}

func topologyOf(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	return classifyIP(net.ParseIP(host))
}

func topologyOfRemote(nc net.Conn) string {
	if nc == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(nc.RemoteAddr().String())
	if err != nil {
		return ""
	}
	return classifyIP(net.ParseIP(host))
}

func classifyIP(ip net.IP) string {
	if ip == nil {
		return ""
	}
	if ip.IsLoopback() {
		return "local"
	}
	if gw := discovery.GatewayFor(ip); gw != nil && gw.Equal(ip) {
		return "hotspot"
	}
	return "lan"
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func randomHex16() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func lanURLs(token string, port int) []string {
	var out []string
	if ifaces, err := discovery.Eligible(); err == nil {
		for _, ifc := range ifaces {
			out = append(out, fmt.Sprintf("http://%s:%d/%s/", ifc.IP.String(), port, token))
		}
	}
	if len(out) == 0 {
		out = append(out, fmt.Sprintf("http://127.0.0.1:%d/%s/", port, token))
	}
	return out
}
