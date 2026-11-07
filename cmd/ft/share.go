package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"
	qrcode "github.com/skip2/go-qrcode"

	"github.com/cwlindean/filetransfer/core/lanweb"
	"github.com/cwlindean/filetransfer/core/logging"
)

// shareOptions drives `ft share --web`: serve a directory on the LAN, register
// a six-digit code with the public site, and hold the relay socket so a
// browser can fall back to it when the LAN path is unreachable.
type shareOptions struct {
	BaseURL  string // https://ft.cwlindean.cn
	Password string
	Name     string
	Dir      string
	Port     int
	LogPath  string
}

type shareReg struct {
	Room  string `json:"room"`
	Token string `json:"device_token"`
}

func shareMain(o shareOptions) error {
	logw := os.Stdout
	if o.LogPath != "" {
		f, err := os.OpenFile(o.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		defer f.Close()
		logw = f
	}
	log := logging.New(logw)

	dir, err := filepath.Abs(o.Dir)
	if err != nil {
		return err
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return fmt.Errorf("%s is not a directory", o.Dir)
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 15 * time.Second}
	if err := unlock(client, o.BaseURL, o.Password); err != nil {
		return err
	}

	port := o.Port
	srv, err := lanweb.New(dir, "", log)
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return err
	}
	port = ln.Addr().(*net.TCPAddr).Port
	go srv.ListenAndServe("", ln)

	code := randomCode()
	var lans []string
	for _, ip := range lanURLs(srv.Token(), port) {
		lans = append(lans, ip)
	}
	reg, err := registerDevice(client, o.BaseURL, code, o.Name, lans)
	if err != nil {
		return err
	}

	shareURL := strings.TrimRight(o.BaseURL, "/") + "/app#code=" + code
	fmt.Fprintf(os.Stderr, "\n6 位码：%s   房间：%s\n", code, reg.Room)
	for _, u := range lans {
		fmt.Fprintf(os.Stderr, "局域网地址：%s\n", u)
	}
	fmt.Fprintf(os.Stderr, "浏览器打开：%s\n", shareURL)
	if qr, err := qrcode.New(shareURL, qrcode.Medium); err == nil {
		fmt.Fprintln(os.Stderr, qr.ToSmallString(false))
	}
	log.Event("share.ready", map[string]any{"code": code, "room": reg.Room, "lan": lans, "url": shareURL})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go heartbeat(ctx, client, o.BaseURL, code, o.Name, lans, reg.Token)
	if err := serveRelay(ctx, o.BaseURL, reg, srv, log); err != nil {
		return err
	}
	return nil
}

func unlock(client *http.Client, base, password string) error {
	if password == "" {
		return fmt.Errorf("web password is required (pass --password or set FT_WEB_PASSWORD)")
	}
	body, _ := json.Marshal(map[string]string{"password": password})
	resp, err := client.Post(strings.TrimRight(base, "/")+"/api/unlock", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("unlock: %s", resp.Status)
	}
	return nil
}

func registerDevice(client *http.Client, base, code, name string, lans []string) (*shareReg, error) {
	body, _ := json.Marshal(map[string]any{"code": code, "name": name, "lan": lans})
	resp, err := client.Post(strings.TrimRight(base, "/")+"/api/devices", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("register: %s %s", resp.Status, strings.TrimSpace(string(out)))
	}
	var reg shareReg
	if err := json.Unmarshal(out, &reg); err != nil {
		return nil, err
	}
	return &reg, nil
}

func heartbeat(ctx context.Context, client *http.Client, base, code, name string, lans []string, token string) {
	t := time.NewTicker(2 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		body, _ := json.Marshal(map[string]any{"code": code, "name": name, "lan": lans, "device_token": token})
		req, _ := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(base, "/")+"/api/devices", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err == nil {
			resp.Body.Close()
		}
	}
}

func serveRelay(ctx context.Context, base string, reg *shareReg, srv *lanweb.Server, log *logging.Logger) error {
	backoff := time.Second
	for {
		err := relayOnce(ctx, base, reg, srv, log)
		if ctx.Err() != nil {
			return nil
		}
		msg := "connection closed"
		if err != nil {
			msg = err.Error()
		}
		log.Event("share.relay_lost", map[string]any{"room": reg.Room, "error": msg, "retry_ms": backoff.Milliseconds()})
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		if backoff < 15*time.Second {
			backoff *= 2
		}
	}
}

func relayOnce(ctx context.Context, base string, reg *shareReg, srv *lanweb.Server, log *logging.Logger) error {
	wsURL := strings.Replace(strings.Replace(strings.TrimRight(base, "/"), "https://", "wss://", 1), "http://", "ws://", 1)
	u := wsURL + "/web/" + reg.Room + "?role=host"
	c, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{
		HTTPHeader: http.Header{"X-Ft-Device-Token": []string{reg.Token}},
	})
	if err != nil {
		return err
	}
	defer c.Close(websocket.StatusNormalClosure, "bye")
	log.Event("share.relay_ready", map[string]any{"room": reg.Room})

	// ESA drops idle WebSocket connections after ~30 s, so the host side keeps
	// sending protocol-level pings; the browser answers them automatically.
	pingCtx, stopPing := context.WithCancel(ctx)
	defer stopPing()
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-pingCtx.Done():
				return
			case <-t.C:
				pc, cancel := context.WithTimeout(pingCtx, 10*time.Second)
				err := c.Ping(pc)
				cancel()
				if err != nil {
					return
				}
			}
		}
	}()

	return serveWebHost(ctx, c, srv.Dir(), srv, log)
}

func randomCode() string {
	n := rand.Intn(900000) + 100000
	return fmt.Sprintf("%06d", n)
}
