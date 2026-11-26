//go:build windows

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/zhitiaojun/ferry/core/proto"
)

func TestDefaultReceiveDirHasNoFerrySubdir(t *testing.T) {
	got := defaultReceiveDir()
	base := filepath.Base(got)
	if !strings.EqualFold(base, "Downloads") {
		t.Fatalf("defaultReceiveDir() = %q, want base Downloads (no Ferry subfolder)", got)
	}
}

func TestAppSettingsPersistenceRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	sp := filepath.Join(tmp, "settings.json")
	customDir := filepath.Join(tmp, "CustomRecv")

	a := newApp(defaultReceiveDir(), 18800)
	a.settingsPath = sp
	a.name = defaultDeviceName()

	if a.isCustomReceiveDir() {
		t.Fatal("default receive dir should not report isCustomReceiveDir=true")
	}

	effName, err := a.setDeviceName("  我的台式机\n ")
	if err != nil {
		t.Fatalf("setDeviceName: %v", err)
	}
	if effName != "我的台式机" || a.deviceName() != "我的台式机" {
		t.Fatalf("deviceName = %q / %q, want 我的台式机", effName, a.deviceName())
	}

	if err := a.setReceiveDir(customDir); err != nil {
		t.Fatalf("setReceiveDir: %v", err)
	}
	if !a.isCustomReceiveDir() {
		t.Fatal("expected isCustomReceiveDir=true after custom path")
	}
	if _, err := os.Stat(customDir); err != nil {
		t.Fatalf("custom dir not created: %v", err)
	}

	loaded := loadDesktopSettings(sp)
	if loaded.DeviceName != "我的台式机" {
		t.Fatalf("loaded DeviceName = %q, want 我的台式机", loaded.DeviceName)
	}
	if !sameCleanedPath(loaded.ReceiveDir, customDir) {
		t.Fatalf("loaded ReceiveDir = %q, want %q", loaded.ReceiveDir, customDir)
	}

	// Resetting device name to blank restores default hostname and clears override.
	resetName, err := a.setDeviceName("   ")
	if err != nil {
		t.Fatalf("setDeviceName empty: %v", err)
	}
	if resetName != defaultDeviceName() {
		t.Fatalf("reset name = %q, want %q", resetName, defaultDeviceName())
	}

	// Oversized UTF-8 name must clamp cleanly <= MaxDeviceName bytes.
	longName := strings.Repeat("电脑", 20)
	clamped, err := a.setDeviceName(longName)
	if err != nil {
		t.Fatalf("setDeviceName long: %v", err)
	}
	if !utf8.ValidString(clamped) || len(clamped) > proto.MaxDeviceName {
		t.Fatalf("clamped name invalid/too long: len=%d valid=%v", len(clamped), utf8.ValidString(clamped))
	}

	// Reset download dir back to default without touching actual user Downloads writes.
	if err := a.setReceiveDir(defaultReceiveDir()); err != nil {
		t.Fatalf("setReceiveDir(default): %v", err)
	}
	if a.isCustomReceiveDir() {
		t.Fatal("expected isCustomReceiveDir=false after setting default Downloads")
	}
	reloaded := loadDesktopSettings(sp)
	if reloaded.ReceiveDir != "" {
		t.Fatalf("expected persisted ReceiveDir to clear when default, got %q", reloaded.ReceiveDir)
	}
}

func TestWriteTempTextFile(t *testing.T) {
	const want = "你好，轻渡！\n第二行测试"
	fp, err := writeTempTextFile("text", want)
	if err != nil {
		t.Fatalf("writeTempTextFile: %v", err)
	}
	defer os.RemoveAll(filepath.Dir(fp))

	base := filepath.Base(fp)
	if !strings.HasPrefix(base, "text-") || !strings.HasSuffix(base, ".txt") {
		t.Fatalf("unexpected filename %q", base)
	}
	got, err := os.ReadFile(fp)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != want {
		t.Fatalf("content = %q, want %q", string(got), want)
	}
}

func TestServerConfigPersistenceAndValidation(t *testing.T) {
	tmp := t.TempDir()
	sp := filepath.Join(tmp, "settings.json")
	a := newApp(defaultReceiveDir(), 18800)
	a.settingsPath = sp
	a.signalURL = "wss://ferry.cwlindean.cn/signal"
	a.webBase = "https://ferry.cwlindean.cn"
	a.stunServers = splitSTUNList("123.57.253.208:40000,123.57.253.208:40001")

	if a.isCustomServerConfig() {
		t.Fatal("default server config should not be marked custom")
	}
	if err := a.setServerConfig("http://bad", "", ""); err == nil {
		t.Fatal("expected invalid signal scheme rejected")
	}
	if err := a.setServerConfig("", "ftp://bad", ""); err == nil {
		t.Fatal("expected invalid web scheme rejected")
	}
	if err := a.setServerConfig("", "", "bad-stun"); err == nil {
		t.Fatal("expected invalid stun rejected")
	}

	if err := a.setServerConfig("wss://self.example.com/signal", "https://self.example.com/", "1.2.3.4:3478, 5.6.7.8:3479"); err != nil {
		t.Fatalf("setServerConfig: %v", err)
	}
	if !a.isCustomServerConfig() {
		t.Fatal("expected isCustomServerConfig=true")
	}
	sig, web, stun := a.serverConfig()
	if sig != "wss://self.example.com/signal" || web != "https://self.example.com" || stun != "1.2.3.4:3478,5.6.7.8:3479" {
		t.Fatalf("serverConfig = %q, %q, %q", sig, web, stun)
	}
	loaded := loadDesktopSettings(sp)
	if loaded.SignalURL != sig || loaded.WebBaseURL != web || loaded.STUNServers != stun {
		t.Fatalf("persisted server config = %+v", loaded)
	}

	if err := a.resetServerConfig(); err != nil {
		t.Fatalf("resetServerConfig: %v", err)
	}
	if a.isCustomServerConfig() {
		t.Fatal("expected isCustomServerConfig=false after reset")
	}
	reloaded := loadDesktopSettings(sp)
	if reloaded.SignalURL != "" || reloaded.WebBaseURL != "" || reloaded.STUNServers != "" {
		t.Fatalf("expected persisted server overrides cleared, got %+v", reloaded)
	}
}
