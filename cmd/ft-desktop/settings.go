//go:build windows

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cwlindean/filetransfer/core/pairing"
)

type desktopSettings struct {
	DeviceName string `json:"device_name,omitempty"`
	ReceiveDir string `json:"receive_dir,omitempty"`
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
