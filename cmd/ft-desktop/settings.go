//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zhitiaojun/ferry/core/client"
	"github.com/zhitiaojun/ferry/core/pairing"
)

type desktopSettings struct {
	DeviceName  string `json:"device_name,omitempty"`
	ReceiveDir  string `json:"receive_dir,omitempty"`
	SignalURL   string `json:"signal_url,omitempty"`
	WebBaseURL  string `json:"web_base_url,omitempty"`
	STUNServers string `json:"stun_servers,omitempty"`
}

func defaultSettingsPath() string {
	return filepath.Join(defaultStateDir(), "settings.json")
}

// defaultReceiveDir places received files directly in the user's Downloads
// folder without creating a Ferry subfolder.
func defaultReceiveDir() string {
	home := homeDir()
	if home == "" {
		return "."
	}
	return filepath.Join(home, "Downloads")
}

func defaultDeviceName() string {
	host, _ := os.Hostname()
	clean := pairing.SanitizeName(host)
	if clean == "" {
		return "Windows"
	}
	return clean
}

func loadDesktopSettings(path string) desktopSettings {
	if path == "" {
		return desktopSettings{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return desktopSettings{}
	}
	var s desktopSettings
	if err := json.Unmarshal(data, &s); err != nil {
		return desktopSettings{}
	}
	s.DeviceName = pairing.SanitizeName(s.DeviceName)
	s.ReceiveDir = strings.TrimSpace(s.ReceiveDir)
	if s.ReceiveDir != "" {
		s.ReceiveDir = filepath.Clean(s.ReceiveDir)
	}
	s.SignalURL = strings.TrimSpace(s.SignalURL)
	s.WebBaseURL = strings.TrimRight(strings.TrimSpace(s.WebBaseURL), "/")
	s.STUNServers = strings.TrimSpace(s.STUNServers)
	return s
}

func saveDesktopSettings(path string, s desktopSettings) error {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".settings-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}

func sameCleanedPath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func (a *app) receiveDir() string {
	if a == nil {
		return defaultReceiveDir()
	}
	a.mu.Lock()
	d := a.dir
	a.mu.Unlock()
	if d == "" {
		return defaultReceiveDir()
	}
	return d
}

func (a *app) isCustomReceiveDir() bool {
	return !sameCleanedPath(a.receiveDir(), defaultReceiveDir())
}

func (a *app) setReceiveDir(dir string) error {
	clean := strings.TrimSpace(dir)
	if clean == "" {
		return errors.New("下载目录不能为空")
	}
	clean = filepath.Clean(clean)
	if err := os.MkdirAll(clean, 0o755); err != nil {
		return fmt.Errorf("无法创建或访问目录：%w", err)
	}
	a.mu.Lock()
	a.dir = clean
	sp := a.settingsPath
	curName := a.name
	a.mu.Unlock()

	persistedDir := clean
	if sameCleanedPath(clean, defaultReceiveDir()) {
		persistedDir = ""
	}
	if sp == "" {
		return nil
	}
	s := loadDesktopSettings(sp)
	if curName != "" && !strings.EqualFold(curName, defaultDeviceName()) {
		s.DeviceName = pairing.SanitizeName(curName)
	}
	s.ReceiveDir = persistedDir
	return saveDesktopSettings(sp, s)
}

func (a *app) resetReceiveDir() (string, error) {
	def := defaultReceiveDir()
	if err := os.MkdirAll(def, 0o755); err != nil {
		return "", fmt.Errorf("无法创建默认下载目录：%w", err)
	}
	a.mu.Lock()
	a.dir = def
	sp := a.settingsPath
	a.mu.Unlock()
	if sp != "" {
		s := loadDesktopSettings(sp)
		s.ReceiveDir = ""
		if err := saveDesktopSettings(sp, s); err != nil {
			return def, err
		}
	}
	return def, nil
}

func (a *app) deviceName() string {
	if a == nil {
		return defaultDeviceName()
	}
	a.mu.Lock()
	n := a.name
	a.mu.Unlock()
	if n == "" {
		return defaultDeviceName()
	}
	return n
}

func (a *app) setDeviceName(raw string) (string, error) {
	clean := pairing.SanitizeName(raw)
	def := defaultDeviceName()
	effective := clean
	persisted := clean
	if effective == "" || effective == def {
		effective = def
		persisted = ""
	}
	a.mu.Lock()
	a.name = effective
	sp := a.settingsPath
	curDir := a.dir
	a.mu.Unlock()

	if sp != "" {
		s := loadDesktopSettings(sp)
		s.DeviceName = persisted
		if curDir != "" && !sameCleanedPath(curDir, defaultReceiveDir()) {
			s.ReceiveDir = filepath.Clean(curDir)
		}
		if err := saveDesktopSettings(sp, s); err != nil {
			return effective, err
		}
	}
	return effective, nil
}

func validateServerConfig(sigRaw, webRaw, stunRaw string) (sig, web, stun string, err error) {
	sig = strings.TrimSpace(sigRaw)
	if sig == "" {
		sig = client.DefaultSignalURL
	} else {
		u, uerr := url.Parse(sig)
		if uerr != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" {
			return "", "", "", errors.New("信令地址格式无效（需以 wss:// 或 ws:// 开头）")
		}
	}

	web = strings.TrimRight(strings.TrimSpace(webRaw), "/")
	if web == "" {
		web = client.DefaultWebBaseURL
	} else {
		u, uerr := url.Parse(web)
		if uerr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "", "", "", errors.New("临时快传网址格式无效（需以 https:// 或 http:// 开头）")
		}
	}

	stunRaw = strings.TrimSpace(stunRaw)
	if stunRaw == "" {
		stun = client.DefaultSTUN
	} else {
		parts := splitSTUNList(stunRaw)
		if len(parts) == 0 {
			return "", "", "", errors.New("STUN 地址不能为空")
		}
		for _, p := range parts {
			host, portStr, serr := net.SplitHostPort(p)
			if serr != nil || strings.TrimSpace(host) == "" {
				return "", "", "", fmt.Errorf("STUN 地址 %q 无效（需为 host:port）", p)
			}
			port, perr := strconv.Atoi(portStr)
			if perr != nil || port < 1 || port > 65535 {
				return "", "", "", fmt.Errorf("STUN 端口无效：%s", p)
			}
		}
		stun = strings.Join(parts, ",")
	}
	return sig, web, stun, nil
}

func (a *app) currentSignalURL() string {
	if a == nil {
		return client.DefaultSignalURL
	}
	a.mu.Lock()
	v := a.signalURL
	a.mu.Unlock()
	if strings.TrimSpace(v) == "" {
		return client.DefaultSignalURL
	}
	return v
}

func (a *app) currentWebBase() string {
	if a == nil {
		return client.DefaultWebBaseURL
	}
	a.mu.Lock()
	v := a.webBase
	a.mu.Unlock()
	if strings.TrimSpace(v) == "" {
		return client.DefaultWebBaseURL
	}
	return strings.TrimRight(v, "/")
}

func (a *app) currentSTUNServers() []string {
	if a == nil {
		return client.DefaultSTUNList()
	}
	a.mu.Lock()
	out := append([]string(nil), a.stunServers...)
	a.mu.Unlock()
	if len(out) == 0 {
		return client.DefaultSTUNList()
	}
	return out
}

func (a *app) serverConfig() (sig, web, stun string) {
	sig = a.currentSignalURL()
	web = a.currentWebBase()
	stun = strings.Join(a.currentSTUNServers(), ",")
	return sig, web, stun
}

func (a *app) isCustomServerConfig() bool {
	sig, web, stun := a.serverConfig()
	return sig != client.DefaultSignalURL || web != client.DefaultWebBaseURL || stun != client.DefaultSTUN
}

func (a *app) serverHostSummary() string {
	sig, _, _ := a.serverConfig()
	host := sig
	if u, err := url.Parse(sig); err == nil && u.Host != "" {
		host = u.Host
	}
	if a.isCustomServerConfig() {
		return "自定义 · " + host
	}
	return "默认 · " + host
}

func (a *app) setServerConfig(sigRaw, webRaw, stunRaw string) error {
	sig, web, stun, err := validateServerConfig(sigRaw, webRaw, stunRaw)
	if err != nil {
		return err
	}
	if a == nil {
		return nil
	}
	a.mu.Lock()
	changed := a.signalURL != sig || a.webBase != web || strings.Join(a.stunServers, ",") != stun
	a.signalURL = sig
	a.webBase = web
	a.stunServers = splitSTUNList(stun)
	sp := a.settingsPath
	a.mu.Unlock()

	if sp != "" {
		s := loadDesktopSettings(sp)
		if sig == client.DefaultSignalURL {
			s.SignalURL = ""
		} else {
			s.SignalURL = sig
		}
		if web == client.DefaultWebBaseURL {
			s.WebBaseURL = ""
		} else {
			s.WebBaseURL = web
		}
		if stun == client.DefaultSTUN {
			s.STUNServers = ""
		} else {
			s.STUNServers = stun
		}
		if err := saveDesktopSettings(sp, s); err != nil {
			return err
		}
	}
	if changed {
		a.restartSignaling()
	}
	return nil
}

func (a *app) resetServerConfig() error {
	return a.setServerConfig("", "", "")
}

// writeTempTextFile stores UTF-8 text inside a temporary directory under a
// clean timestamped filename (<prefix>-HHmmss.txt). Callers should clean up
// filepath.Dir(path) after sending finishes.
func writeTempTextFile(prefix, text string) (string, error) {
	if prefix == "" {
		prefix = "text"
	}
	dir, err := os.MkdirTemp("", "ferry-msg-*")
	if err != nil {
		return "", err
	}
	fp := filepath.Join(dir, fmt.Sprintf("%s-%s.txt", prefix, time.Now().Format("150405")))
	if err := os.WriteFile(fp, []byte(text), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return fp, nil
}
