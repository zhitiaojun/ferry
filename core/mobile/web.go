// Package mobile: gomobile bindings for the Android shell.
package mobile

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cwlindean/filetransfer/core/lanweb"
	"github.com/cwlindean/filetransfer/core/logging"
)

var (
	webMu sync.Mutex
	webSV *lanweb.Server
	webLn net.Listener
)

// StartWebShare serves outDir over HTTP on the LAN and returns a JSON summary
// with the page URL. The page is the same one `ft recv --web` serves, so a
// browser on the same Wi-Fi can upload and download without any app on the
// other side.
//
// hintJSON is the wlan0 interface description produced by MainActivity.lanHint
// (same shape as the punch hint); it only supplies the address shown in the
// URL. Binding is always 0.0.0.0 so a hotspot client can reach it.
func StartWebShare(outDir string, port int, hintJSON string) (string, error) {
	webMu.Lock()
	defer webMu.Unlock()
	if webSV != nil {
		return "", fmt.Errorf("web share is already running")
	}
	if outDir == "" {
		return "", fmt.Errorf("outDir is empty")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
	if err != nil {
		return "", err
	}
	srv, err := lanweb.New(outDir, "", logging.New(nil))
	if err != nil {
		ln.Close()
		return "", err
	}
	go func() {
		_ = srv.ListenAndServe("", ln)
	}()

	host := ""
	if h := parseHint(hintJSON); h != nil && h.IP != "" {
		host = h.IP
	}
	if host == "" || host == "<nil>" {
		host = firstLANIP()
	}
	if host == "" {
		host = "127.0.0.1"
	}
	tcp, _ := ln.Addr().(*net.TCPAddr)
	portNow := 0
	if tcp != nil {
		portNow = tcp.Port
	}
	url := srv.URL(net.JoinHostPort(host, fmt.Sprint(portNow)))

	webSV, webLn = srv, ln
	b, _ := json.Marshal(map[string]any{
		"ok":    true,
		"mode":  "lanweb",
		"dir":   filepath.Base(outDir),
		"port":  portNow,
		"token": srv.Token(),
		"url":   url,
		"ts":    time.Now().Format(time.RFC3339),
	})
	return string(b), nil
}

// StopWebShare closes the page listener; safe to call when nothing runs.
func StopWebShare() error {
	webMu.Lock()
	defer webMu.Unlock()
	if webLn != nil {
		_ = webLn.Close()
		webLn = nil
	}
	webSV = nil
	return nil
}

func firstLANIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLoopback() || ipn.IP.To4() == nil {
			continue
		}
		if ipn.IP.IsLinkLocalUnicast() {
			continue
		}
		return ipn.IP.String()
	}
	return ""
}
