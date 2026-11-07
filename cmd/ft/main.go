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
	"os"
	"runtime"
	"strings"
	"time"

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
	case "help", "-h", "--help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "ft: unknown command %q\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  ft send --addr <host:port> --file <path> [--file <path>...] [options]
  ft recv --listen <host:port> --out <dir> [options]
  ft version

send options:
  --allow lan|punch|relay   allowed paths (M1: only lan)
  --name NAME               device name shown to the peer (default: hostname)
  --log FILE                write JSONL events to FILE
  --timeout DURATION        per-frame wait (default 30s)
  --chunk BYTES             chunk size (default 65536)

recv options:
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
	allow := fs.String("allow", "lan", "allowed paths, comma separated")
	name := fs.String("name", defaultName(), "device name")
	logPath := fs.String("log", "", "JSONL log file")
	timeout := fs.Duration("timeout", 30*time.Second, "per-frame wait")
	chunk := fs.Int("chunk", int(proto.DefaultChunkSize), "chunk size in bytes")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	paths, code := checkAllow(*allow)
	if code != 0 {
		return code
	}
	if *addr == "" || len(files) == 0 {
		fmt.Fprintln(os.Stderr, "ft send: --addr and at least one --file are required")
		return 2
	}

	logger, closeLog, err := openLog(*logPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ft send: %v\n", err)
		return 1
	}
	defer closeLog()

	p := pathmgr.NewTCPDial(*addr)
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

	if logger != nil {
		logger.Event("session.ready", map[string]any{"peer": s.Peer.Name, "path": paths[0]})
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
	listen := fs.String("listen", "127.0.0.1:0", "listen address host:port")
	out := fs.String("out", ".", "output directory")
	name := fs.String("name", defaultName(), "device name")
	logPath := fs.String("log", "", "JSONL log file")
	timeout := fs.Duration("timeout", 30*time.Second, "per-frame wait")
	chunk := fs.Int("chunk", int(proto.DefaultChunkSize), "chunk size in bytes")
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

	p, err := pathmgr.NewTCPListener(*listen)
	if err != nil {
		return fail(logger, "listen", err)
	}
	defer p.Close()

	// The address line is how the evidence script discovers an ephemeral port.
	if logger != nil {
		logger.Event("path.listen", map[string]any{"addr": p.Addr(), "path": p.Name()})
	}
	fmt.Fprintf(os.Stderr, "listening %s\n", p.Addr())

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	nc, err := p.Accept(ctx)
	if err != nil {
		return fail(logger, "accept", err)
	}

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

	if logger != nil {
		logger.Event("session.ready", map[string]any{"peer": s.Peer.Name, "path": p.Name()})
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
		case "lan":
			paths = append(paths, p)
		case "punch", "relay":
			fmt.Fprintf(os.Stderr, "ft: path %q is not implemented in M1\n", p)
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
