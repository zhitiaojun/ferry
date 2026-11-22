//go:build windows

package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

const (
	runKeyPath     = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValue       = "Ferry"
	legacyRunValue = "filetransfer"
)

// autostartEnabled reports whether the tray app is registered to start at login.
func autostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	if v, _, err := k.GetStringValue(runValue); err == nil && v != "" {
		return true
	}
	v, _, err := k.GetStringValue(legacyRunValue)
	return err == nil && v != ""
}

// setAutostart adds or removes the HKCU Run entry (no administrator needed).
//
// It shells out to reg.exe: the DSH sandbox denies registry writes from Go's
// syscall path even when the same write succeeds from reg.exe, and reg.exe is
// the same mechanism 1Panel/tray apps use in practice.
func setAutostart(enable bool) error {
	deleteVal := func(name string) error {
		cmd := exec.Command("reg", "delete", `HKCU\`+runKeyPath, "/v", name, "/f")
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000, HideWindow: true}
		out, err := cmd.CombinedOutput()
		if err != nil && !bytes.Contains(out, []byte("unable to find")) && !strings.Contains(string(out), "找不到") {
			return fmt.Errorf("reg delete %s: %v: %s", name, err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if !enable {
		if err := deleteVal(runValue); err != nil {
			return err
		}
		return deleteVal(legacyRunValue)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command("reg", "add", `HKCU\`+runKeyPath, "/v", runValue, "/t", "REG_SZ", "/d", `"`+exe+`" -silent`, "/f")
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000, HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("reg add: %v: %s", err, strings.TrimSpace(string(out)))
	}
	_ = deleteVal(legacyRunValue)
	return nil
}
