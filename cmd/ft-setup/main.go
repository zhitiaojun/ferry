//go:build windows

// Command ft-setup installs the Windows side of filetransfer: it copies the
// binaries next to itself, registers the tray app to start at login, adds the
// Explorer "SendTo" entry, and can undo all of it with -uninstall.
package main

import (
	"embed"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

//go:embed payload/ft.exe payload/ft-desktop.exe
var payload embed.FS

const (
	appName    = "filetransfer"
	runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
)

func main() {
	uninstall := flag.Bool("uninstall", false, "remove the installation, autostart entry and SendTo shortcut")
	dir := flag.String("dir", "", "install directory (default: %LOCALAPPDATA%\\ft)")
	noStartup := flag.Bool("no-autostart", false, "do not register login autostart")
	flag.Parse()

	if *dir == "" {
		base, err := os.UserConfigDir() // %APPDATA% on Windows
		if err != nil {
			fatal("locate user dir: %v", err)
		}
		*dir = filepath.Join(filepath.Dir(base), "Local", "ft")
	}
	if *uninstall {
		if err := remove(*dir); err != nil {
			fatal("uninstall: %v", err)
		}
		fmt.Println("已卸载。配置目录保留在", *dir)
		return
	}
	if err := install(*dir, !*noStartup); err != nil {
		fatal("install: %v", err)
	}
	fmt.Println("已安装到", *dir)
	fmt.Println("开始菜单/任务栏都可以直接运行：", filepath.Join(*dir, "bin", "ft-desktop.exe"))
}

func install(dir string, autostart bool) error {
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return err
	}
	for _, name := range []string{"ft.exe", "ft-desktop.exe"} {
		data, err := payload.ReadFile("payload/" + name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(bin, name), data, 0o755); err != nil {
			return err
		}
	}
	self, err := os.Executable()
	if err == nil {
		if data, rerr := os.ReadFile(self); rerr == nil {
			_ = os.WriteFile(filepath.Join(dir, "ft-setup.exe"), data, 0o755)
		}
	}
	if autostart {
		k, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
		if err != nil {
			return fmt.Errorf("open Run key: %w", err)
		}
		defer k.Close()
		cmd := fmt.Sprintf(`"%s" -startup`, filepath.Join(bin, "ft-desktop.exe"))
		if err := k.SetStringValue(appName, cmd); err != nil {
			return fmt.Errorf("set Run value: %w", err)
		}
	}
	if err := writeSendTo(filepath.Join(bin, "ft-desktop.exe")); err != nil {
		fmt.Fprintln(os.Stderr, "提示：写“发送到”快捷方式失败（可稍后从托盘菜单重试）：", err)
	}
	return nil
}

func remove(dir string) error {
	// Stop anything still running from the install dir.
	_ = exec.Command("taskkill", "/F", "/IM", "ft-desktop.exe").Run()
	_ = exec.Command("taskkill", "/F", "/IM", "ft.exe").Run()

	if k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE); err == nil {
		_ = k.DeleteValue(appName)
		k.Close()
	}
	sendTo, _ := sendToDir()
	if sendTo != "" {
		_ = os.Remove(filepath.Join(sendTo, "ft 发送到.lnk"))
	}
	return os.RemoveAll(dir)
}

func sendToDir() (string, error) {
	appData, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(appData, "Microsoft", "Windows", "SendTo"), nil
}

// writeSendTo creates the Explorer "Send to" shortcut through WScript.Shell.
func writeSendTo(target string) error {
	sendTo, err := sendToDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(sendTo, 0o755); err != nil {
		return err
	}
	lnk := filepath.Join(sendTo, "ft 发送到.lnk")
	script := fmt.Sprintf(`$s=(New-Object -ComObject WScript.Shell).CreateShortcut('%s');$s.TargetPath='%s';$s.WorkingDirectory='%s';$s.Save()`,
		strings.ReplaceAll(lnk, "'", "''"), target, filepath.Dir(target))
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run()
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ft-setup: "+format+"\n", args...)
	os.Exit(1)
}
