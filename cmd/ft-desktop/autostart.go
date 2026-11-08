//go:build windows

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const (
	runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValue   = "filetransfer"
)

// autostartEnabled reports whether the tray app is registered to start at login.
func autostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	v, _, err := k.GetStringValue(runValue)
	return err == nil && v != ""
}

// setAutostart adds or removes the HKCU Run entry (no administrator needed).
//
// It shells out to reg.exe: the DSH sandbox denies registry writes from Go's
// syscall path even when the same write succeeds from reg.exe, and reg.exe is
// the same mechanism 1Panel/tray apps use in practice.
func setAutostart(enable bool) error {
	if !enable {
		out, err := exec.Command("reg", "delete", `HKCU\`+runKeyPath, "/v", runValue, "/f").CombinedOutput()
		if err != nil && !bytes.Contains(out, []byte("unable to find")) {
			return fmt.Errorf("reg delete: %v: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command("reg", "add", `HKCU\`+runKeyPath, "/v", runValue, "/t", "REG_SZ", "/d", `"`+exe+`"`, "/f")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("reg add: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
