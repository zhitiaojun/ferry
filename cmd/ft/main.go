// Command ft is the command line client for the filetransfer skeleton.
//
//	ft send --addr 127.0.0.1:18800 --file a.bin --file b.txt
//	ft recv --listen 127.0.0.1:18800 --out ./recv
//
// M1 supports one transfer per invocation over plain TCP (path "lan").
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/cwlindean/filetransfer/core/discovery"
	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/logging"
	"github.com/cwlindean/filetransfer/core/pathmgr"
	"github.com/cwlindean/filetransfer/core/proto"
	"github.com/cwlindean/filetransfer/core/session"
	"github.com/cwlindean/filetransfer/core/transfer"
)

var version = "0.0.0-dev"

var (
	goVersion  = runtime.Version()
	goPlatform = runtime.GOOS + "/" + runtime.GOARCH
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "-version", "--version", "version":
		fmt.Printf("ft %s\nprotocol %d\ngo %s\nplatform %s\n", version, proto.Version, goVersion, goPlatform)
		return
	case "send":
		os.Exit(runSend(os.Args[2:]))
	case "recv":
		os.Exit(runRecv(os.Args[2:]))
	case "whoami":
		os.Exit(runWhoami(os.Args[2:]))
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "ft: unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

// runWhoami prints the signaling identity used by --signal (protocol §8.2).
func runWhoami(args []string) int {
	fs := flag.NewFlagSet("whoami", flag.ContinueOnError)
	keyPath := fs.String("key", defaultKeyPath(), "device key file")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	k, err := loadDeviceKey(*keyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ft whoami: %v\n", err)
		return 1
	}
	fmt.Printf("device_id %s\npubkey %s\nkey %s\n", k.IDHex(), k.PubHex(), *keyPath)
	return 0
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  ft send --addr <host:port> --file <path> [--file <path>...] [options]
  ft recv --listen <host:port> --out <dir> [options]
  ft version

send options:
  --discover                find the peer via LAN announcements instead of --addr
  --to NAME                 with --discover: pick the device whose name contains NAME
  --discover-timeout D      how long to wait for announcements (default 5s)
  --allow lan|punch|relay   allowed paths (M2: only lan)
  --name NAME               device name shown to the peer (default: hostname)
  --log FILE                write JSONL events to FILE
  --timeout DURATION        per-frame wait (default 30s)
  --chunk BYTES             chunk size (default 65536)

recv options:
  --lan                     listen on all interfaces and announce on the LAN
  --out DIR                 output directory (created if missing)
  --once                    accept a single connection (default true)
  same --name/--log/--timeout/--chunk as send
`)
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
	allow := fs.String("allow", "lan", "allowed paths, comma separated")
	name := fs.String("name", defaultName(), "device name")
	logPath := fs.String("log", "", "JSONL log file")
	timeout := fs.Duration("timeout", 30*time.Second, "per-frame wait")
	chunk := fs.Int("chunk", int(proto.DefaultChunkSize), "chunk size in bytes")
	signalURL := fs.String("signal", "", "signaling URL (wss://…/signal) to send via the relay")
	toID := fs.String("to-id", "", "target device id (64 hex) for --signal")
	keyPath := fs.String("key", defaultKeyPath(), "device key file for --signal")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	paths, code := checkAllow(*allow)
	if code != 0 {
		return code
	}
	if len(files) == 0 {
		fmt.Fprintln(os.Stderr, "ft send: at least one --file is required")
		return 2
	}
	logger, closeLog, err := openLog(*logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ft send: %v\n", err)
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
	if *signalURL != "" {
		if *toID == "" {
			fmt.Fprintln(os.Stderr, "ft send: --to-id is required with --signal")
			return 2
		}
		cctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		sum, err := sendViaRelay(cctx, relayOptions{
			SignalURL: *signalURL, KeyPath: *keyPath, Name: *name, ToID: *toID,
			Files: files, Chunk: *chunk, Timeout: *timeout, Log: logger,
		})
		if err != nil {
			return fail(logger, "relay", err)
		}
		printSummary(sum)
		return 0
	}
	if target == "" {
		fmt.Fprintln(os.Stderr, "ft send: --addr/--peer (or --discover) is required")
		return 2
	}

	p := pathmgr.NewTCPDial(target)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	nc, err := p.Dial(ctx)
	if err != nil {
		return fail(logger, "dial", err)
	}
	s, err := session.Dial(nc, session.Params{
		DeviceID:     identity.DeviceID(*name),
		Name:         *name,
		Capabilities: proto.CapResume,
	})
	if err != nil {
		_ = nc.Close()
		return fail(logger, "handshake", err)
	}
	defer s.Close()

	topology := topologyOf(target)
	if logger != nil {
		logger.Event("session.ready", map[string]any{"peer": s.Peer.Name, "path": paths[0], "topology": topology, "addr": target})
	}

	res, err := transfer.Send(s, files, transfer.Config{
		ChunkSize: uint32(*chunk),
		Timeout:   *timeout,
		Log:       logger,
	})
	if err != nil {
		return fail(logger, "transfer", err)
	}
	printSummary(summary{
		OK:         true,
		Path:       paths[0],
		Topology:   topology,
		Peer:       s.Peer.Name,
		TransferID: fmt.Sprintf("%x", res.TransferID),
		Files:      res.Files,
		Bytes:      res.Bytes,
		Chunks:     res.Chunks,
		DurationMS: res.Duration.Milliseconds(),
	})
	return 0
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
	signalURL := fs.String("signal", "", "signaling URL (wss://…/signal) to receive via the relay")
	keyPath := fs.String("key", defaultKeyPath(), "device key file for --signal")
	_ = fs.Bool("once", true, "accept one connection (kept for CLI stability)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *out == "" {
		fmt.Fprintln(os.Stderr, "ft recv: --out is required")
		return 2
	}

	logger, closeLog, err := openLog(*logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ft recv: %v\n", err)
		return 1
	}
	defer closeLog()

	if *signalURL != "" {
		cctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		sum, err := recvViaRelay(cctx, relayOptions{
			SignalURL: *signalURL, KeyPath: *keyPath, Name: *name, OutDir: *out,
			Chunk: *chunk, Timeout: *timeout, Log: logger,
		})
		if err != nil {
			return fail(logger, "relay", err)
		}
		printSummary(sum)
		return 0
	}

	addr := *listen
	if addr == "" {
		if *lan {
			addr = "0.0.0.0:0"
		} else {
			addr = "127.0.0.1:0"
		}
	}
	p, err := pathmgr.NewTCPListener(addr)
	if err != nil {
		return fail(logger, "listen", err)
	}
	defer p.Close()

	// The address line is how the evidence script discovers an ephemeral port.
	if logger != nil {
		logger.Event("path.listen", map[string]any{"addr": p.Addr(), "path": p.Name(), "lan": *lan})
	}
	fmt.Fprintf(os.Stderr, "listening %s\n", p.Addr())
	// Optional LAN announcement: peers find this receiver without --addr.
	announceCtx, stopAnnounce := context.WithCancel(context.Background())
	defer stopAnnounce()
	if *lan {
		tcpPort := listenerPort(p.Addr())
		go func() {
			_ = discovery.Announce(announceCtx, discovery.Config{
				DeviceID: identity.DeviceID(*name),
				Name:     *name,
				Port:     tcpPort,
				Log:      logger,
			})
		}()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	nc, err := p.Accept(ctx)
	if err != nil {
		return fail(logger, "accept", err)
	}
	stopAnnounce()

	s, err := session.Accept(nc, session.Params{
		DeviceID:     identity.DeviceID(*name),
		Name:         *name,
		Capabilities: proto.CapResume,
	})
	if err != nil {
		_ = nc.Close()
		return fail(logger, "handshake", err)
	}
	defer s.Close()

	topology := topologyOfRemote(nc)
	if logger != nil {
		logger.Event("session.ready", map[string]any{"peer": s.Peer.Name, "path": p.Name(), "topology": topology, "addr": nc.RemoteAddr().String()})
	}

	res, err := transfer.Receive(s, *out, transfer.Config{
		ChunkSize: uint32(*chunk),
		Timeout:   *timeout,
		Log:       logger,
	})
	if err != nil {
		return fail(logger, "transfer", err)
	}
	printSummary(summary{
		OK:         true,
		Path:       p.Name(),
		Topology:   topology,
		Peer:       s.Peer.Name,
		TransferID: fmt.Sprintf("%x", res.TransferID),
		Files:      res.Files,
		Bytes:      res.Bytes,
		Chunks:     res.Chunks,
		DurationMS: res.Duration.Milliseconds(),
	})
	return 0
}

// checkAllow validates --allow and returns the first usable path name.
func checkAllow(allow string) ([]string, int) {
	var paths []string
	for _, p := range strings.Split(allow, ",") {
		p = strings.TrimSpace(p)
		switch p {
		case "":
			continue
		case "lan", "relay":
			paths = append(paths, p)
		case "punch":
			fmt.Fprintf(os.Stderr, "ft: path %q is not implemented yet\n", p)
			return nil, 3
		default:
			fmt.Fprintf(os.Stderr, "ft: unknown path %q\n", p)
			return nil, 2
		}
	}
	if len(paths) == 0 {
		fmt.Fprintln(os.Stderr, "ft: --allow selects no usable path")
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
	fmt.Fprintf(os.Stderr, "ft: %s: %v\n", stage, err)
	return 1
}

func printSummary(s summary) {
	enc := json.NewEncoder(os.Stdout)
	_ = enc.Encode(s)
}

func defaultName() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "ft-device"
	}
	return h
}

var _ io.Writer = os.Stderr

// listenerPort extracts the TCP port from a listen address.
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

// discoverPeer waits for a LAN announcement and returns its TCP address.
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

// topologyOf classifies an outbound peer address for diagnostics (D24/R6):
// "local" for loopback, "hotspot" when the peer is our default gateway (the
// phone that hosts the hotspot), otherwise "lan".
func topologyOf(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	return classifyIP(net.ParseIP(host))
}

// topologyOfRemote classifies the inbound peer seen on nc.
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
