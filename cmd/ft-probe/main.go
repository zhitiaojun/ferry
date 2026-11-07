// Command ft-probe exercises a deployed signaling endpoint: login, then keep
// the WebSocket alive with protocol pings for a fixed duration. It exists for
// the M3 ESA preflight (idle timeout, long-lived connection) and is not shipped
// to end users.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/signaling"
)

func main() {
	signalURL := flag.String("signal", "", "signaling URL (wss://…)")
	keyPath := flag.String("key", "", "device key file")
	dur := flag.Duration("duration", 10*time.Minute, "how long to stay connected")
	pingEvery := flag.Duration("ping", 30*time.Second, "probe ping interval (0 disables)")
	keepalive := flag.Duration("keepalive", 30*time.Second, "client keepalive interval (0 disables)")
	flag.Parse()
	if *signalURL == "" || *keyPath == "" {
		fmt.Fprintln(os.Stderr, "usage: ft-probe -signal wss://host/signal -key path.key [-duration 10m] [-ping 30s]")
		os.Exit(2)
	}

	key, err := identity.LoadOrCreateKey(*keyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "probe: key: %v\n", err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *dur+30*time.Second)
	defer cancel()

	signaling.KeepaliveInterval = *keepalive
	start := time.Now()
	client, err := signaling.Dial(ctx, *signalURL, key, nil)
	if err != nil {
		fmt.Printf(`{"event":"probe.error","error":%q}`+"\n", err.Error())
		os.Exit(1)
	}
	defer client.Close()
	fmt.Printf(`{"event":"probe.login","device":%q,"elapsed_ms":%d}`+"\n", client.DeviceID(), time.Since(start).Milliseconds())

	pings, fails := 0, 0
	var tick <-chan time.Time
	if *pingEvery > 0 {
		t := time.NewTicker(*pingEvery)
		defer t.Stop()
		tick = t.C
	}
	deadline := time.After(*dur)
	for {
		select {
		case <-tick:
			pctx, pcancel := context.WithTimeout(ctx, 10*time.Second)
			rtt, err := client.Ping(pctx)
			pcancel()
			if err != nil {
				fails++
				fmt.Printf(`{"event":"probe.ping_error","elapsed_ms":%d,"error":%q}`+"\n", time.Since(start).Milliseconds(), err.Error())
				if fails >= 2 {
					fmt.Printf(`{"event":"probe.dead","elapsed_ms":%d}`+"\n", time.Since(start).Milliseconds())
					os.Exit(1)
				}
				continue
			}
			pings++
			fmt.Printf(`{"event":"probe.ping","n":%d,"rtt_ms":%d,"elapsed_ms":%d}`+"\n", pings, rtt.Milliseconds(), time.Since(start).Milliseconds())
		case <-deadline:
			fmt.Printf(`{"event":"probe.done","elapsed_ms":%d,"pings":%d,"fails":%d}`+"\n", time.Since(start).Milliseconds(), pings, fails)
			return
		case <-client.Done():
			fmt.Printf(`{"event":"probe.closed","elapsed_ms":%d,"error":%q}`+"\n", time.Since(start).Milliseconds(), client.Err())
			os.Exit(1)
		}
	}
}
