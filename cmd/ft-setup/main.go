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
	appName        = "Ferry"
	runKeyPath     = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValue       = "Ferry"
	legacyRunValue = "filetransfer"
	ctxKeyPath     = `Software\Classes\*\shell\FileTransfer`
	// classicMenuCLSID is the well-known per-user switch that makes Explorer use
	// the Windows 10 context menu, where plain registry verbs are first-level.
	classicMenuCLSID = `Software\Classes\CLSID\{86ca1aa0-34aa-4e8b-a509-50c905bae2a2}\InprocServer32`
	classicMenuRoot  = `Software\Classes\CLSID\{86ca1aa0-34aa-4e8b-a509-50c905bae2a2}`
	appKeyPath       = `Software\filetransfer`
	uninstallKeyPath = `Software\Microsoft\Windows\CurrentVersion\Uninstall\Ferry`
)

type installOptions struct {
	Dir               string
	Autostart         bool
	DesktopShortcut   bool
	StartMenuShortcut bool
	LaunchAfter       bool
}

func main() {
	setSetupDPIAware()
	uninstall := flag.Bool("uninstall", false, "remove the installation, autostart entry and SendTo shortcut")
	dir := flag.String("dir", "", "install directory (default: %LOCALAPPDATA%\\Ferry)")
	noStartup := flag.Bool("no-autostart", false, "do not register login autostart")
	classic := flag.String("classic-menu", "", "on|off — switch Explorer to the classic (Windows 10) context menu so first-level verbs show up")
	quiet := flag.Bool("quiet", false, "do not show the GUI wizard or result dialog (for scripted runs)")
	flag.Parse()
	quietMode = *quiet

	marker(os.Args)
	logf("start argv=%q", os.Args)
	if *dir == "" {
		if *uninstall {
			if saved := readInstallLocation(); saved != "" {
				*dir = saved
			}
		}
	}
	if *dir == "" {
		base, err := os.UserConfigDir() // %APPDATA% on Windows
		if err != nil {
			finish("无法定位用户目录", err)
			return
		}
		*dir = filepath.Join(filepath.Dir(base), "Local", "Ferry")
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
		finish("轻渡 (Ferry) 已卸载", nil)
		return
	}
	if !*quiet {
		os.Exit(runWizard(*dir))
	}
	opts := installOptions{
		Dir:       *dir,
		Autostart: !*noStartup,
	}
	if err := installWithOptions(opts); err != nil {
		if os.IsPermission(err) && elevateSelf() {
			return // 已交回提权后的那个实例
		}
		finish("安装失败", err)
		return
	}
	finish("安装完成\n\n目录："+*dir+"\n可执行："+filepath.Join(*dir, "bin", "ferry-desktop.exe"), nil)
}

func stopRunningProcess(name string) {
	cmd := exec.Command("taskkill", "/F", "/IM", name)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000, HideWindow: true}
	_ = cmd.Run()
}

func readPayloadAny(names ...string) ([]byte, error) {
	var lastErr error
	for _, n := range names {
		data, err := payload.ReadFile(n)
		if err == nil {
			return data, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func defaultFerryStateDir() string {
	if base, err := os.UserConfigDir(); err == nil {
		return filepath.Join(filepath.Dir(base), "Local", "Ferry")
	}
	return ""
}

func legacyFtStateDir() string {
	if base, err := os.UserConfigDir(); err == nil {
		return filepath.Join(filepath.Dir(base), "Local", "ft")
	}
	return ""
}

func migrateLegacyState(installDir string) {
	oldDir := legacyFtStateDir()
	if oldDir == "" {
		return
	}
	targets := []string{installDir}
	if def := defaultFerryStateDir(); def != "" && !strings.EqualFold(filepath.Clean(def), filepath.Clean(installDir)) {
		targets = append(targets, def)
	}
	for _, name := range []string{"device.key", "paired.json"} {
		data, err := os.ReadFile(filepath.Join(oldDir, name))
		if err != nil {
			continue
		}
		mode := os.FileMode(0o644)
		if name == "device.key" {
			mode = 0o600
		}
		for _, dir := range targets {
			dst := filepath.Join(dir, name)
			if _, err := os.Stat(dst); err == nil {
				continue
			}
			if err := os.MkdirAll(dir, 0o755); err == nil {
				_ = os.WriteFile(dst, data, mode)
			}
		}
	}
}

func installWithOptions(opts installOptions) error {
	dir := strings.TrimSpace(opts.Dir)
	if dir == "" {
		return fmt.Errorf("安装目录不能为空")
	}
	stopRunningProcess("ferry-desktop.exe")
	stopRunningProcess("ft-desktop.exe")

	bin := filepath.Join(dir, "bin")
	logf("install dir=%s autostart=%v desktop=%v startmenu=%v", dir, opts.Autostart, opts.DesktopShortcut, opts.StartMenuShortcut)
	if err := os.MkdirAll(bin, 0o755); err != nil {
		return err
	}
	binaries := []struct {
		out  string
		srcs []string
	}{
		{"ferry.exe", []string{"payload/ferry.exe", "payload/ft.exe"}},
		{"ferry-desktop.exe", []string{"payload/ferry-desktop.exe", "payload/ft-desktop.exe"}},
	}
	for _, b := range binaries {
		data, err := readPayloadAny(b.srcs...)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(bin, b.out), data, 0o755); err != nil {
			return err
		}
	}
	setupDest := filepath.Join(dir, "ferry-setup.exe")
	if self, err := os.Executable(); err == nil {
		if data, rerr := os.ReadFile(self); rerr == nil {
			_ = os.WriteFile(setupDest, data, 0o755)
		}
	}
	migrateLegacyState(dir)

	// Extract menu assets (including ft.ico) before creating shortcuts so the
	// icon path exists for Desktop, Start Menu, SendTo, and Uninstall entries.
	menuDir := filepath.Join(dir, "menu")
	if err := extractDir("payload/menu", menuDir); err != nil {
		logf("extract menu err=%v", err)
	}
	desktopExe := filepath.Join(bin, "ferry-desktop.exe")
	iconPath := filepath.Join(menuDir, "ft.ico")
	if _, err := os.Stat(iconPath); err != nil {
		iconPath = desktopExe
	}

	if err := writeDesktopExeKey(desktopExe); err != nil {
		logf("write DesktopExe err=%v", err)
	}
	if err := setRunAutostart(opts.Autostart, desktopExe); err != nil {
		return err
	}
	if err := writeUninstallKey(dir, setupDest, iconPath); err != nil {
		logf("write Uninstall err=%v", err)
	}
	if err := writeContextMenu(desktopExe); err != nil {
		fmt.Fprintln(os.Stderr, "提示：写右键菜单失败（可稍后重装重试）：", err)
	}
	if err := writeSendTo(desktopExe, iconPath); err != nil {
		fmt.Fprintln(os.Stderr, "提示：写“发送到”快捷方式失败（可稍后从托盘菜单重试）：", err)
	}
	if opts.DesktopShortcut {
		if err := writeDesktopShortcut(desktopExe, iconPath); err != nil {
			fmt.Fprintln(os.Stderr, "提示：创建桌面快捷方式失败：", err)
		}
	}
	if opts.StartMenuShortcut {
		if err := writeStartMenuShortcuts(desktopExe, setupDest, dir, iconPath); err != nil {
			fmt.Fprintln(os.Stderr, "提示：创建开始菜单快捷方式失败：", err)
		}
	}
	if err := registerShellMenu(menuDir); err != nil {
		fmt.Fprintln(os.Stderr, "提示：一级右键菜单注册失败（需要开发者模式）：", err)
	}
	if opts.LaunchAfter {
		_ = exec.Command(desktopExe).Start()
	}
	return nil
}

func writeDesktopExeKey(desktopExe string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, appKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue("DesktopExe", desktopExe)
}

func setRunAutostart(enable bool, desktopExe string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open Run key: %w", err)
	}
	defer k.Close()
	_ = k.DeleteValue(legacyRunValue)
	if !enable {
		if err := k.DeleteValue(runValue); err != nil && err != registry.ErrNotExist {
			return fmt.Errorf("delete Run value: %w", err)
		}
		return nil
	}
	cmd := fmt.Sprintf(`"%s" -silent`, desktopExe)
	if err := k.SetStringValue(runValue, cmd); err != nil {
		return fmt.Errorf("set Run value: %w", err)
	}
	return nil
}

func writeUninstallKey(dir, setupExe, iconPath string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, uninstallKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	for name, val := range map[string]string{
		"DisplayName":     "轻渡 (Ferry)",
		"DisplayVersion":  "1.0.0",
		"Publisher":       "zhitiaojun",
		"InstallLocation": dir,
		"UninstallString": fmt.Sprintf(`"%s" -uninstall -dir "%s"`, setupExe, dir),
		"DisplayIcon":     iconPath,
	} {
		if err := k.SetStringValue(name, val); err != nil {
			return err
		}
	}
	_ = k.SetDWordValue("NoModify", 1)
	_ = k.SetDWordValue("NoRepair", 1)
	return nil
}

func readInstallLocation() string {
	k, err := registry.OpenKey(registry.CURRENT_USER, uninstallKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, err := k.GetStringValue("InstallLocation")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(v)
}

func remove(dir string) error {
	for _, proc := range []string{"ferry-desktop.exe", "ft-desktop.exe", "ferry.exe", "ft.exe"} {
		stopRunningProcess(proc)
	}
	if k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE); err == nil {
		_ = k.DeleteValue(runValue)
		_ = k.DeleteValue(legacyRunValue)
		k.Close()
	}
	// revert the classic-menu switch only if we were the ones who enabled it
	if mk, err := registry.OpenKey(registry.CURRENT_USER, appKeyPath, registry.QUERY_VALUE); err == nil {
		if v, _, verr := mk.GetIntegerValue("ClassicMenu"); verr == nil && v == 1 {
			_ = setClassicMenu(false)
		}
		mk.Close()
	}
	_ = registry.DeleteKey(registry.CURRENT_USER, uninstallKeyPath)
	_ = registry.DeleteKey(registry.CURRENT_USER, appKeyPath)
	removeContextMenu()
	_ = removeShellMenu()
	removeShortcuts()

	if err := os.RemoveAll(dir); err != nil {
		// When launched via the Start Menu / Settings uninstall entry from
		// inside dir, the running binary locks its own file until exit.
		if self, serr := os.Executable(); serr == nil && strings.HasPrefix(strings.ToLower(self), strings.ToLower(filepath.Clean(dir))+string(filepath.Separator)) {
			cmd := exec.Command("cmd.exe", "/C", fmt.Sprintf(`ping 127.0.0.1 -n 2 >NUL & rmdir /S /Q "%s"`, dir))
			cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000, HideWindow: true}
			_ = cmd.Start()
			return nil
		}
		return err
	}
	return nil
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

func escPS(s string) string {
	return strings.ReplaceAll(s, "'", "''")
}

func runHiddenPS(script string) error {
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000, HideWindow: true}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func shortcutPS(lnkExpr, target, args, workDir, desc, iconPath string) string {
	b := fmt.Sprintf(`$s = $ws.CreateShortcut(%s)
$s.TargetPath = '%s'
$s.WorkingDirectory = '%s'
`, lnkExpr, escPS(target), escPS(workDir))
	if args != "" {
		b += fmt.Sprintf("$s.Arguments = '%s'\n", escPS(args))
	}
	if desc != "" {
		b += fmt.Sprintf("$s.Description = '%s'\n", escPS(desc))
	}
	if iconPath != "" {
		b += fmt.Sprintf("$s.IconLocation = '%s,0'\n", escPS(iconPath))
	}
	b += "$s.Save()\n"
	return b
}

// writeSendTo creates the Explorer "Send to" shortcut through WScript.Shell.
func writeSendTo(target, iconPath string) error {
	sendTo, err := sendToDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(sendTo, 0o755); err != nil {
		return err
	}
	_ = os.Remove(filepath.Join(sendTo, "ft 发送到.lnk"))
	lnk := filepath.Join(sendTo, "轻渡 发送到.lnk")
	script := "$ws = New-Object -ComObject WScript.Shell\n" +
		shortcutPS(fmt.Sprintf("'%s'", escPS(lnk)), target, "", filepath.Dir(target), "用轻渡 Ferry 发送到已配对设备", iconPath)
	return runHiddenPS(script)
}

func writeDesktopShortcut(desktopExe, iconPath string) error {
	script := "$ws = New-Object -ComObject WScript.Shell\n" +
		"$desk = [Environment]::GetFolderPath('Desktop')\n" +
		shortcutPS(`(Join-Path $desk '轻渡.lnk')`, desktopExe, "", filepath.Dir(desktopExe), "轻渡 · Ferry 文件快传", iconPath)
	return runHiddenPS(script)
}

func writeStartMenuShortcuts(desktopExe, setupExe, installDir, iconPath string) error {
	uninstArgs := fmt.Sprintf(`-uninstall -dir "%s"`, installDir)
	script := "$ws = New-Object -ComObject WScript.Shell\n" +
		"$dir = Join-Path ([Environment]::GetFolderPath('Programs')) '轻渡'\n" +
		"New-Item -ItemType Directory -Force -Path $dir | Out-Null\n" +
		shortcutPS(`(Join-Path $dir '轻渡.lnk')`, desktopExe, "", filepath.Dir(desktopExe), "轻渡 · Ferry 文件快传", iconPath) +
		shortcutPS(`(Join-Path $dir '卸载轻渡.lnk')`, setupExe, uninstArgs, installDir, "卸载 轻渡 (Ferry)", iconPath)
	return runHiddenPS(script)
}

func removeShortcuts() {
	if sendTo, err := sendToDir(); err == nil && sendTo != "" {
		_ = os.Remove(filepath.Join(sendTo, "轻渡 发送到.lnk"))
		_ = os.Remove(filepath.Join(sendTo, "ft 发送到.lnk"))
	}
	script := `
$deskLnk = Join-Path ([Environment]::GetFolderPath('Desktop')) '轻渡.lnk'
Remove-Item -Force -ErrorAction SilentlyContinue $deskLnk
$menuDir = Join-Path ([Environment]::GetFolderPath('Programs')) '轻渡'
Remove-Item -Recurse -Force -ErrorAction SilentlyContinue $menuDir
`
	_ = runHiddenPS(script)
}

// marker records the installer startup line in %TEMP%\ft-setup-ran.txt.
func marker(args []string) {
	line := fmt.Sprintf("%s user=%s home=%s temp=%s argv=%q\n", timeNow(), os.Getenv("USERNAME"), os.Getenv("USERPROFILE"), os.TempDir(), args)
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

// registerShellMenu registers the sparse MSIX package that makes the shell show
// a real Windows 11 first-level context menu item. Registry verbs alone can only
// reach the "Show more options" menu.
func registerShellMenu(menu string) error {
	manifest := filepath.Join(menu, "AppxManifest.xml")
	if _, err := os.Stat(manifest); err != nil {
		return err
	}
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
