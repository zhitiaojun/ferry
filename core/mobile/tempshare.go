package mobile

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/lanweb"
	"github.com/cwlindean/filetransfer/core/tempshare"
)

var (
	tempMu   sync.Mutex
	tempHost *tempshare.Host
)

// StartTempShare starts an HTTPS-accessible temporary transfer session
// (mode: "send" or "recv") backed by both local LAN HTTP and public WSS relay.
func StartTempShare(baseURL, keyPath, deviceName, mode, filePathsJSON, outDir, hintJSON string) (string, error) {
	tempMu.Lock()
	if tempHost != nil {
		_ = tempHost.Close()
		tempHost = nil
	}
	tempMu.Unlock()

	if baseURL == "" {
		baseURL = DefaultWebBaseURL
	}
	key, err := identity.LoadOrCreateKey(keyPath)
	if err != nil {
		return "", err
	}
	var shared []string
	if filePathsJSON != "" && filePathsJSON != "[]" {
		if err := json.Unmarshal([]byte(filePathsJSON), &shared); err != nil {
			return "", fmt.Errorf("bad file list: %w", err)
		}
	}
	hintIP := ""
	if h := parseHint(hintJSON); h != nil {
		hintIP = h.IP
	}
	host, err := tempshare.StartHost(tempshare.HostOptions{
		BaseURL:     baseURL,
		Key:         key,
		DeviceName:  deviceName,
		Mode:        mode,
		SharedFiles: shared,
		RecvDir:     outDir,
		LANHintIP:   hintIP,
		OnUploaded: func(f lanweb.File) {
			if outDir != "" {
				recordReceivedFrom(outDir, "web", "临时快传", []string{filepath.Join(outDir, f.Name)}, f.Size)
			}
		},
	})
	if err != nil {
		return "", err
	}

	tempMu.Lock()
	tempHost = host
	tempMu.Unlock()

	b, _ := json.Marshal(host.Status())
	return string(b), nil
}

// StopTempShare closes the active temporary transfer session if any.
func StopTempShare() error {
	tempMu.Lock()
	h := tempHost
	tempHost = nil
	tempMu.Unlock()
	if h != nil {
		return h.Close()
	}
	return nil
}

// TempShareStatus returns the JSON status of the active temporary share session.
func TempShareStatus() string {
	tempMu.Lock()
	h := tempHost
	tempMu.Unlock()
	if h == nil {
		return `{"active":false}`
	}
	b, _ := json.Marshal(h.Status())
	return string(b)
}

// ResolveTempShare resolves a https://.../s/<token> link and probes LAN vs relay.
func ResolveTempShare(shareURL string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sess, err := tempshare.ResolveByURL(ctx, shareURL)
	if err != nil {
		return "", err
	}
	probe, err := tempshare.ProbeTransport(ctx, sess)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(map[string]any{
		"ok":    true,
		"name":  sess.Name,
		"mode":  sess.Mode,
		"via":   probe.Via,
		"files": probe.Files,
	})
	return string(b), nil
}

// TempShareDownloadAll downloads all files from a temporary share URL into outDir
// without adding the remote host to paired.json.
func TempShareDownloadAll(shareURL, outDir string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	sess, err := tempshare.ResolveByURL(ctx, shareURL)
	if err != nil {
		return "", err
	}
	via, saved, err := tempshare.DownloadAll(ctx, sess, outDir)
	if err != nil {
		return "", err
	}
	var total int64
	recordReceivedFrom(outDir, via, sess.Name, saved, total)
	b, _ := json.Marshal(map[string]any{
		"ok":    true,
		"via":   via,
		"files": saved,
	})
	return string(b), nil
}

// TempShareUploadFiles uploads selected local file paths to a temporary share
// host without adding it to paired.json.
func TempShareUploadFiles(shareURL, filePathsJSON string) (string, error) {
	var paths []string
	if err := json.Unmarshal([]byte(filePathsJSON), &paths); err != nil {
		return "", fmt.Errorf("bad file list: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	sess, err := tempshare.ResolveByURL(ctx, shareURL)
	if err != nil {
		return "", err
	}
	via, uploaded, err := tempshare.UploadFiles(ctx, sess, paths)
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(map[string]any{
		"ok":    true,
		"via":   via,
		"files": uploaded,
	})
	return string(b), nil
}
