//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// installSendTo drops a shortcut into the user's "Send to" folder so Explorer's
// right-click menu gets a "发送到 ft" entry. Windows appends the selected paths
// to the shortcut's target, which our main() treats as files to send.
func installSendTo() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return fmt.Errorf("APPDATA is not set")
	}
	lnk := filepath.Join(appData, "Microsoft", "Windows", "SendTo", "轻渡 发送到.lnk")
	script := fmt.Sprintf(`$ws = New-Object -ComObject WScript.Shell
$s = $ws.CreateShortcut('%s')
$s.TargetPath = '%s'
$s.Description = '用轻渡 Ferry 发送到已配对设备'
$s.Save()`, escapePS(lnk), escapePS(exe))
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("powershell: %v: %s", err, out)
	}
	return nil
}

// escapePS quotes a path for a single-quoted PowerShell string.
func escapePS(s string) string {
	out := make([]rune, 0, len(s)+2)
	for _, r := range s {
		if r == '\'' {
			out = append(out, '\'', '\'')
			continue
		}
		out = append(out, r)
	}
	return string(out)
}
