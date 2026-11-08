//go:build windows

package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"

	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/proto"
	"github.com/cwlindean/filetransfer/core/session"
	"github.com/cwlindean/filetransfer/core/transfer"
)

const (
	menuOpenDir = 1
	menuInfo    = 2
	menuDrop    = 3
	menuStartup = 4
	menuQuit    = 5
	menuSendTo  = 6
	menuMain    = 7
)

type tray struct {
	app   *app
	hwnd  syscall.Handle
	nid   notifyIconData
	key   *identity.Key
	devID [32]byte
	name  string
	ln    net.Listener
}

func runTray(a *app, keyPath string) int {
	trayApp = a
	t := &tray{app: a}
	key, err := identity.LoadOrCreateKey(keyPath)
	if err != nil {
		messageBox("ft", "无法加载设备密钥："+err.Error())
		return 1
	}
	t.key = key
	t.devID = key.ID()
	t.name, _ = os.Hostname()
	if t.name == "" {
		t.name = "windows"
	}

	// Background receiver.
	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", a.port))
	if err != nil {
		messageBox("ft", fmt.Sprintf("端口 %d 被占用：%v", a.port, err))
		return 1
	}
	t.ln = ln
	a.event("desktop.receiver.listen", map[string]any{
		"addr": ln.Addr().String(),
		"dir":  a.dir,
		"id":   fmt.Sprintf("%x", t.devID[:4]),
	})
	go t.acceptLoop()

	// Tray window + icon.
	if !t.createWindow() {
		messageBox("ft", "创建托盘窗口失败")
		return 1
	}
	t.addIcon()
	if a.showMain {
		openMainWindow(a)
	}

	var msg msgT
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
	t.removeIcon()
	ln.Close()
	return 0
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

// defaultKeyPath keeps the device key where Windows apps normally write user
// state: %LOCALAPPDATA%\ft. Unlike ~/.ft this directory is always writable for a
// normal user process.
func defaultKeyPath() string {
	if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
		return filepath.Join(dir, "ft", "device.key")
	}
	return filepath.Join(homeDir(), ".ft", "device.key")
}

func (t *tray) createWindow() bool {
	hInst := getModuleHandle()
	cls := utf16Ptr("ftTrayWnd")
	icon, _, _ := procLoadIconW.Call(0, idiApplication)
	cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
	wc := wndClassExW{
		cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		lpfnWndProc:   syscall.NewCallback(trayWndProc),
		hInstance:     hInst,
		hIcon:         syscall.Handle(icon),
		hCursor:       syscall.Handle(cursor),
		lpszClassName: cls,
	}
	trayHandle = t
	if r, _, _ := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return false
	}
	hwnd, _, _ := procCreateWindowExW.Call(
		wsExToolWindow, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(utf16Ptr("ft"))),
		0, 0, 0, 0, 0, 0, 0, uintptr(hInst), 0)
	if hwnd == 0 {
		return false
	}
	t.hwnd = syscall.Handle(hwnd)
	return true
}

// trayHandle lets the window procedure reach the running instance; the message
// loop is single-threaded, so one global is enough.
var trayHandle *tray

func (t *tray) addIcon() {
	icon, _, _ := procLoadIconW.Call(0, idiApplication)
	nid := notifyIconData{
		hWnd:             t.hwnd,
		uID:              1,
		uFlags:           nifMessage | nifIcon | nifTip,
		uCallbackMessage: wmTray,
		hIcon:            syscall.Handle(icon),
	}
	utf16Into(nid.szTip[:], "ft：局域网接收中，右键菜单")
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&nid)))
	t.nid = nid
}

func (t *tray) removeIcon() {
	nid := t.nid
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&nid)))
}

func (t *tray) balloon(title, text string) {
	nid := t.nid
	nid.cbSize = uint32(unsafe.Sizeof(nid))
	nid.uFlags = nifInfo
	utf16Into(nid.szInfoTitle[:], title)
	utf16Into(nid.szInfo[:], text)
	nid.dwInfoFlags = niifInfo
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&nid)))
}

func (t *tray) menu() {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	defer procDestroyMenu.Call(menu)
	appendMenu(menu, mfString, menuOpenDir, "打开接收目录")
	appendMenu(menu, mfString, menuInfo, "显示接收信息")
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString, menuMain, "打开主界面")
	appendMenu(menu, mfString, menuDrop, "显示投放小窗")
	startupFlags := uint32(mfString)
	if autostartEnabled() {
		startupFlags |= mfChecked
	}
	appendMenu(menu, startupFlags, menuStartup, "开机自启")
	appendMenu(menu, mfString, menuSendTo, "安装右键「发送到」")
	appendMenu(menu, mfSeparator, 0, "")
	appendMenu(menu, mfString, menuQuit, "退出")

	var pt pointT
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	procSetForegroundWindow.Call(uintptr(t.hwnd))
	cmd, _, _ := procTrackPopupMenu.Call(menu, tpmRightButton|tpmLeftAlign|tpmReturnCmd,
		uintptr(pt.x), uintptr(pt.y), 0, uintptr(t.hwnd), 0)
	procPostMessageW.Call(uintptr(t.hwnd), 0, 0, 0)
	if cmd != 0 {
		t.command(uint16(cmd))
	}
}

func appendMenu(menu uintptr, flags uint32, id uint16, text string) {
	var p *uint16
	if text != "" {
		p = utf16Ptr(text)
	}
	procAppendMenuW.Call(menu, uintptr(flags), uintptr(id), uintptr(unsafe.Pointer(p)))
}

func (t *tray) command(id uint16) {
	switch id {
	case menuOpenDir:
		shellOpen(t.app.dir)
	case menuInfo:
		messageBox("ft 接收信息", fmt.Sprintf(
			"设备 ID：%x\n端口：%d\n目录：%s\n\n在手机/电脑上用这个地址即可发送：\n%s",
			t.devID[:4], t.app.port, t.app.dir, localAddr(t.app.port)))
	case menuMain:
		openMainWindow(t.app)
	case menuDrop:
		exe, err := os.Executable()
		if err == nil {
			cmd := exec.Command(exe, "-drop")
			cmd.Start()
		}
	case menuStartup:
		on := !autostartEnabled()
		if err := setAutostart(on); err != nil {
			t.balloon("开机自启", fmt.Sprintf("设置失败：%v", err))
		} else if on {
			t.balloon("开机自启", "已设置：登录后自动在通知区运行")
		} else {
			t.balloon("开机自启", "已取消开机自启")
		}
	case menuSendTo:
		if err := installSendTo(); err != nil {
			t.balloon("发送到", fmt.Sprintf("安装失败：%v", err))
		} else {
			t.balloon("发送到", "已在右键菜单加入「ft 发送到」")
		}
	case menuQuit:
		procPostQuitMessage.Call(0)
	}
}

func localAddr(port int) string {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok || ipn.IP.IsLoopback() || ipn.IP.To4() == nil {
			continue
		}
		return fmt.Sprintf("%s:%d", ipn.IP.String(), port)
	}
	return fmt.Sprintf("127.0.0.1:%d", port)
}

func (t *tray) acceptLoop() {
	for {
		nc, err := t.ln.Accept()
		if err != nil {
			return
		}
		if !recvEnabled.Load() {
			// Paused: keep the socket bound but refuse new sessions so the
			// sender fails fast instead of hanging in the handshake.
			nc.Close()
			continue
		}
		go t.handle(nc)
	}
}

func (t *tray) handle(nc net.Conn) {
	params := session.Params{DeviceID: t.devID, Name: t.name, Capabilities: proto.CapResume}
	sess, err := session.Accept(nc, params)
	if err != nil {
		nc.Close()
		t.app.event("desktop.session_error", map[string]any{"error": err.Error()})
		return
	}
	peer := fmt.Sprintf("%x", sess.Peer.DeviceID[:4])
	t.app.event("desktop.session_ready", map[string]any{"peer": peer, "name": sess.Peer.Name})

	if !recvEnabled.Load() {
		nc.Close()
		return
	}
	res, err := transfer.Receive(sess, t.app.dir, transfer.Config{})
	if err != nil {
		t.app.event("desktop.transfer_error", map[string]any{"peer": peer, "error": err.Error()})
		t.balloon("接收失败", err.Error())
		return
	}
	t.app.event("desktop.transfer_done", map[string]any{
		"peer": peer, "files": res.Files, "bytes": res.Bytes,
	})
	t.app.note(fmt.Sprintf("%s  ← %s  %d 个文件 · %s", time.Now().Format("15:04"), peer, len(res.Files), humanBytes(res.Bytes)))
	title := fmt.Sprintf("已接收 %d 个文件", len(res.Files))
	t.balloon(title, fmt.Sprintf("来自 %s，共 %d 字节，已存入 %s", peer, res.Bytes, t.app.dir))
}

func trayWndProc(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case wmTray:
		if trayHandle != nil {
			switch loword(lParam) {
			case wmRButtonUp, wmLButtonUp:
				trayHandle.menu()
			}
		}
		return 0
	case wmCommand:
		if trayHandle != nil {
			trayHandle.command(loword(wParam))
		}
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return r
}
