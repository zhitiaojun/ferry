package client

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
)

const (
	// DefaultSignalURL is the deployed signaling endpoint.
	DefaultSignalURL = "wss://ferry.cwlindean.cn/signal"
	// DefaultWebBaseURL is the public Ferry website and temp share base URL.
	DefaultWebBaseURL = "https://ferry.cwlindean.cn"
)

var migrateOnce sync.Once

// DefaultStateDir returns the canonical per-user Ferry state directory
// (%LOCALAPPDATA%\Ferry on Windows, ~/.ferry elsewhere), migrating any legacy
// device.key and paired.json from the former ft / .ft directory once.
func DefaultStateDir() string {
	dir, old := resolveStateDirs(runtime.GOOS, os.Getenv("LOCALAPPDATA"), homeDir())
	migrateOnce.Do(func() {
		migrateLegacyState(old, dir)
	})
	return dir
}

// DefaultKeyPath returns the default device identity key file path.
func DefaultKeyPath() string {
	return filepath.Join(DefaultStateDir(), "device.key")
}

// DefaultPairedPath returns the default trusted peer table file path.
func DefaultPairedPath() string {
	return filepath.Join(DefaultStateDir(), "paired.json")
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

func resolveStateDirs(goos, localAppData, home string) (dir, old string) {
	if goos == "windows" && localAppData != "" {
		return filepath.Join(localAppData, "Ferry"), filepath.Join(localAppData, "ft")
	}
	if home == "" {
		return ".", ""
	}
	return filepath.Join(home, ".ferry"), filepath.Join(home, ".ft")
}

func migrateLegacyState(oldDir, newDir string) {
	if oldDir == "" || oldDir == newDir {
		return
	}
	type item struct {
		name string
		mode os.FileMode
	}
	for _, it := range []item{{"device.key", 0o600}, {"paired.json", 0o644}} {
		dst := filepath.Join(newDir, it.name)
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join(oldDir, it.name))
		if err != nil {
			continue
		}
		if err := os.MkdirAll(newDir, 0o700); err != nil {
			return
		}
		_ = os.WriteFile(dst, data, it.mode)
	}
}
