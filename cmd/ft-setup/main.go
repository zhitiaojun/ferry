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
	"time"
	"unsafe"

	"golang.org/x/sys/windows/registry"
)

//go:embed all:payload
var payload embed.FS

const (
	appName    = "filetransfer"
	runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`
	ctxKeyPath = `Software\Classes\*\shell\FileTransfer`
	// classicMenuCLSID is the well-known per-user switch that makes Explorer use
	// the Windows 10 context menu, where plain registry verbs are first-level.
	classicMenuCLSID = `Software\Classes\CLSID\{86ca1aa0-34aa-4e8b-a509-50c905bae2a2}\InprocServer32`
	classicMenuRoot  = `Software\Classes\CLSID\{86ca1aa0-34aa-4e8b-a509-50c905bae2a2}`
	appKeyPath       = `Software\filetransfer`
)

func main() {
	uninstall := flag.Bool("uninstall", false, "remove the installation, autostart entry and SendTo shortcut")
	dir := flag.String("dir", "", "install directory (default: %LOCALAPPDATA%\\ft)")
	noStartup := flag.Bool("no-autostart", false, "do not register login autostart")
	classic := flag.String("classic-menu", "", "on|off — switch Explorer to the classic (Windows 10) context menu so first-level verbs show up")
	quiet := flag.Bool("quiet", false, "do not show the result dialog (for scripted runs)")
	flag.Parse()
	quietMode = *quiet

	marker(os.Args)
	logf("start argv=%q", os.Args)
	if *dir == "" {
		base, err := os.UserConfigDir() // %APPDATA% on Windows
		if err != nil {
			finish("无法定位用户目录", err)
			return
		}
		*dir = filepath.Join(filepath.Dir(base), "Local", "ft")
	}
	if *classic != "" {
		if *classic != "on" && *classic != "off" {
			finish("参数错误", fmt.Errorf("-classic-menu 只接受 on 或 off"))
			return
		}
		if err := setClassicMenu(*classic == "on"); err != nil {
			finish("切换右键菜单失败", err)
			return
		}
		finish("已切换右键菜单为"+map[bool]string{true: "经典（Win10 风格）", false: "Windows 11 默认"}[*classic == "on"]+"\n\n需要重启资源管理器或重新登录才生效", nil)
		return
	}
	if *uninstall {
		if err := remove(*dir); err != nil {
			if os.IsPermission(err) && elevateSelf() {
				return
			}
			finish("卸载失败", err)
			return
		}
		finish("已卸载", nil)
		return
	}
	if err := install(*dir, !*noStartup); err != nil {
		if os.IsPermission(err) && elevateSelf() {
			return // 已交回提权后的那个实例
		}
		finish("安装失败", err)
		return
	}
	finish("安装完成\n\n目录："+*dir+"\n可执行："+filepath.Join(*dir, "bin", "ft-desktop.exe"), nil)
}

func install(dir string, autostart bool) error {
	bin := filepath.Join(dir, "bin")
	logf("install dir=%s", dir)
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
	if err := writeContextMenu(filepath.Join(bin, "ft-desktop.exe")); err != nil {
		fmt.Fprintln(os.Stderr, "提示：写右键菜单失败（可稍后重装重试）：", err)
	}
	if err := writeSendTo(filepath.Join(bin, "ft-desktop.exe")); err != nil {
		fmt.Fprintln(os.Stderr, "提示：写“发送到”快捷方式失败（可稍后从托盘菜单重试）：", err)
	}
	if err := installShellMenu(dir); err != nil {
		if os.IsPermission(err) || strings.Contains(err.Error(), "Access is denied") || strings.Contains(err.Error(), "UnauthorizedAccess") {
			if elevateSelf() {
				return nil
			}
		}
		fmt.Fprintln(os.Stderr, "提示：一级右键菜单注册失败（需要开发者模式）：", err)
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
	// revert the classic-menu switch only if we were the ones who enabled it
	if mk, err := registry.OpenKey(registry.CURRENT_USER, appKeyPath, registry.QUERY_VALUE); err == nil {
		if v, _, verr := mk.GetIntegerValue("ClassicMenu"); verr == nil && v == 1 {
			_ = setClassicMenu(false)
		}
		mk.Close()
	}
	removeContextMenu()
	_ = removeShellMenu()
	sendTo, _ := sendToDir()
	if sendTo != "" {
		_ = os.Remove(filepath.Join(sendTo, "轻渡 发送到.lnk"))
		_ = os.Remove(filepath.Join(sendTo, "ft 发送到.lnk"))
	}
	return os.RemoveAll(dir)
}

// writeContextMenu adds a first-level Explorer verb so a single file can be
// sent without going through the classic "Send to" submenu.
func writeContextMenu(target string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, ctxKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	_ = k.SetStringValue("", "用轻渡发送")
	for name, val := range map[string]string{
		"MUIVerb":          "用轻渡发送",
		"Icon":             target,
		"MultiSelectModel": "Single",
	} {
		if err := k.SetStringValue(name, val); err != nil {
			return err
		}
	}
	ck, _, err := registry.CreateKey(registry.CURRENT_USER, ctxKeyPath+`\command`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer ck.Close()
	return ck.SetStringValue("", fmt.Sprintf(`"%s" "%%1"`, target))
}

func removeContextMenu() {
	_ = registry.DeleteKey(registry.CURRENT_USER, ctxKeyPath+`\command`)
	_ = registry.DeleteKey(registry.CURRENT_USER, ctxKeyPath)
}

// setClassicMenu toggles the Windows 10 style context menu for the current user.
// We leave a marker so -uninstall can revert only what we changed.
func setClassicMenu(on bool) error {
	if on {
		k, _, err := registry.CreateKey(registry.CURRENT_USER, classicMenuCLSID, registry.SET_VALUE)
		if err != nil {
			return err
		}
		_ = k.SetStringValue("", "")
		k.Close()
		mk, _, err := registry.CreateKey(registry.CURRENT_USER, appKeyPath, registry.SET_VALUE)
		if err != nil {
			return err
		}
		defer mk.Close()
		return mk.SetDWordValue("ClassicMenu", 1)
	}
	if err := registry.DeleteKey(registry.CURRENT_USER, classicMenuCLSID); err != nil && err != registry.ErrNotExist {
		return err
	}
	if err := registry.DeleteKey(registry.CURRENT_USER, classicMenuRoot); err != nil && err != registry.ErrNotExist {
		return err
	}
	if mk, err := registry.OpenKey(registry.CURRENT_USER, appKeyPath, registry.SET_VALUE); err == nil {
		_ = mk.DeleteValue("ClassicMenu")
		mk.Close()
	}
	return nil
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
	lnk := filepath.Join(sendTo, "轻渡 发送到.lnk")
	script := fmt.Sprintf(`$s=(New-Object -ComObject WScript.Shell).CreateShortcut('%s');$s.TargetPath='%s';$s.WorkingDirectory='%s';$s.Save()`,
		strings.ReplaceAll(lnk, "'", "''"), target, filepath.Dir(target))
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Run()
}

// marker records the very first thing the process does next to the exe, so a
// silent failure still tells us whether the exe ran and what it could see.
func marker(args []string) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	line := fmt.Sprintf("%s user=%s home=%s temp=%s argv=%q\n", timeNow(), os.Getenv("USERNAME"), os.Getenv("USERPROFILE"), os.TempDir(), args)
	_ = os.WriteFile(filepath.Join(filepath.Dir(exe), "setup-ran.txt"), []byte(line), 0o644)
	_ = os.WriteFile(filepath.Join(os.TempDir(), "ft-setup-ran.txt"), []byte(line), 0o644)
}

// elevateSelf re-launches the installer with the "runas" verb when writing the
// per-user directory is denied. Some machines launch unknown exes with a
// restricted token; elevation is the only reliable escape there.
func elevateSelf() bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	args := strings.Join(os.Args[1:], " ")
	verbPtr, _ := syscall.UTF16PtrFromString("runas")
	filePtr, _ := syscall.UTF16PtrFromString(exe)
	var argPtr *uint16
	if args != "" {
		argPtr, _ = syscall.UTF16PtrFromString(args)
	}
	shell32 := syscall.NewLazyDLL("shell32.dll")
	ret, _, _ := shell32.NewProc("ShellExecuteW").Call(0, uintptr(unsafe.Pointer(verbPtr)), uintptr(unsafe.Pointer(filePtr)), uintptr(unsafe.Pointer(argPtr)), 0, 1 /*SW_SHOWNORMAL*/)
	return ret > 32
}

// logf appends to %TEMP%\ft-setup.log so a silent double-click failure leaves a trace.
func logf(format string, args ...any) {
	f, err := os.OpenFile(filepath.Join(os.TempDir(), "ft-setup.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s "+format+"\n", append([]any{timeNow()}, args...)...)
}

func timeNow() string { return time.Now().Format("2006-01-02 15:04:05") }

// finish reports the outcome in a message box (the exe is built as a GUI app, so
// stderr would be invisible when the user double-clicks it).
func finish(title string, err error) {
	body := title
	if err != nil {
		body += "\n\n" + err.Error()
		logf("error: %v", err)
	} else {
		logf("ok: %s", title)
	}
	if quietMode {
		fmt.Println(body)
		return
	}
	msgBox(title, body)
}

// quietMode suppresses the result dialog so the installer can run unattended.
var quietMode bool

func msgBox(title, body string) {
	dll := syscall.NewLazyDLL("user32.dll")
	proc := dll.NewProc("MessageBoxW")
	text, _ := syscall.UTF16PtrFromString(body)
	cap, _ := syscall.UTF16PtrFromString(title)
	const mbOK, mbIconInfo, mbIconError = 0x0, 0x40, 0x10
	flags := uintptr(mbOK | mbIconInfo)
	if strings.Contains(body, "失败") || strings.Contains(body, "无法") {
		flags = mbOK | mbIconError
	}
	proc.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(cap)), flags)
}

// installShellMenu registers the sparse MSIX package that makes the shell show
// a real Windows 11 first-level context menu item. Registry verbs alone can only
// reach the "Show more options" menu.
func installShellMenu(dir string) error {
	menu := filepath.Join(dir, "menu")
	if err := extractDir("payload/menu", menu); err != nil {
		return err
	}
	manifest := filepath.Join(menu, "AppxManifest.xml")
	// Removing a previous registration first keeps reinstall idempotent.
	ps := fmt.Sprintf(`Get-AppxPackage FileTransfer.ShellMenu | Remove-AppxPackage -ErrorAction SilentlyContinue; Add-AppxPackage -Register '%s' -ExternalLocation '%s'`, manifest, menu)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", ps)
	// Add-AppxPackage needs a console screen buffer; a GUI-subsystem installer has
	// none, so give PowerShell its own (hidden) console.
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000010, HideWindow: true}
	out, err := cmd.CombinedOutput()
	logf("shellmenu register err=%v out=%s", err, out)
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func removeShellMenu() error {
	ps := `Get-AppxPackage FileTransfer.ShellMenu | Remove-AppxPackage`
	cmd := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", ps)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000010, HideWindow: true}
	out, err := cmd.CombinedOutput()
	logf("shellmenu remove err=%v out=%s", err, out)
	return err
}

// extractDir copies an embedded directory tree onto disk.
func extractDir(root, dest string) error {
	entries, err := payload.ReadDir(root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		src := root + "/" + e.Name()
		out := filepath.Join(dest, e.Name())
		if e.IsDir() {
			if err := extractDir(src, out); err != nil {
				return err
			}
			continue
		}
		data, err := payload.ReadFile(src)
		if err != nil {
			return err
		}
		if err := os.WriteFile(out, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
