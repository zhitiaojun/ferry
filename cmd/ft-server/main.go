// Command ft-server is the BJ signaling and relay endpoint (protocol §8).
// It listens on loopback and is published by openresty; see RUNBOOK §D.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/zhitiaojun/ferry/core/logging"
	"github.com/zhitiaojun/ferry/server"
)

var version = "1.0.0"

func main() {
	listen := flag.String("listen", "127.0.0.1:18790", "listen address (loopback only on BJ)")
	logPath := flag.String("log", "", "write JSONL logs to this file instead of stderr")
	memLimit := flag.Int64("memlimit", 180<<20, "Go soft memory limit in bytes (0 disables)")
	stunFlag := flag.String("stun", "", "comma-separated UDP addresses for STUN (e.g. 0.0.0.0:40000,0.0.0.0:40001); empty disables")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("ft-server %s\nprotocol 1\n", version)
		return
	}

	var log *logging.Logger
	if *logPath != "" {
		f, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ft-server: open log: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		log = logging.New(f)
	} else {
		log = logging.New(os.Stderr)
	}

	if *memLimit > 0 {
		debug.SetMemoryLimit(*memLimit)
	}

	srv := server.New(log)
	httpSrv := &http.Server{
		Addr:              *listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	log.Event("server.start", map[string]any{
		"listen": *listen, "version": version, "memlimit": *memLimit,
	})

	var stun *server.STUN
	if *stunFlag != "" {
		stunSrv, err := server.ListenSTUN(splitList(*stunFlag), log)
		if err != nil {
			fmt.Fprintln(os.Stderr, "ft-server: stun:", err)
			os.Exit(1)
		}
		stun = stunSrv
		defer stun.Close()
		go func() {
			if err := stun.Serve(context.Background()); err != nil {
				log.Event("server.error", map[string]any{"stun": err.Error()})
			}
		}()
		log.Event("server.stun", map[string]any{"addrs": stun.Addrs()})
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpSrv.ListenAndServe()
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			log.Event("server.error", map[string]any{"error": err.Error()})
			os.Exit(1)
		}
	case s := <-sig:
		log.Event("server.shutdown", map[string]any{"signal": s.String()})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(ctx)
		if stun != nil {
			_ = stun.Close()
		}
	}
}

func splitList(s string) []string {
	out := []string{}
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
