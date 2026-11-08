//go:build windows

// The main window is deliberately a plain Win32 window drawn with GDI: the
// desktop app is a small always-running utility, so a browser engine (WebView2
// or an Edge --app window) would cost 100-250 MB of resident memory for a
// screen with one list and three buttons. Everything here is owner-drawn from
// layout(), which both WM_PAINT and hit testing share so the two can never
// disagree about where a row or button is.
package main

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"github.com/cwlindean/filetransfer/core/client"
)

const (
	mainClass     = "ftMainWin"
	wmAppPaint    = wmApp + 10
	wmPrint       = 0x0317
	wmPrintClient = 0x0318
	wmSizeMsg     = 0x0005

	// Colors are system colors so the window follows the user's theme.
	colorWindowBg   = 5
	colorWindowText = 8
	colorHighlight  = 13
	colorHighText   = 14
	colorBtnFace    = 15
	colorBtnShadow  = 16
	colorGrayText   = 17
	colorBtnText    = 18
	colorBtnHiLight = 20

	dtEndEllipsis = 0x8000
	dtRight       = 0x0002
	dtWordBreak   = 0x0010

	swRestore = 9
)

var (
	comdlg32          = syscall.NewLazyDLL("comdlg32.dll")
	procGetOpenFile   = comdlg32.NewProc("GetOpenFileNameW")
	procSetTextColor  = gdi32.NewProc("SetTextColor")
	procSetBkMode     = gdi32.NewProc("SetBkMode")
	procCreateBrush   = gdi32.NewProc("CreateSolidBrush")
	procGetSysColor   = user32.NewProc("GetSysColor")
	procSysColorBrush = user32.NewProc("GetSysColorBrush")
	procCreateFontW   = gdi32.NewProc("CreateFontW")
	procSelectObject  = gdi32.NewProc("SelectObject")
	procDeleteObject  = gdi32.NewProc("DeleteObject")
	procMoveWindow    = user32.NewProc("MoveWindow")
	procSetWindowText = user32.NewProc("SetWindowTextW")
	procSetFocus      = user32.NewProc("SetFocus")
)

type openFileNameW struct {
	structSize    uint32
	hwndOwner     syscall.Handle
	hInstance     syscall.Handle
	filter        *uint16
	customFilter  *uint16
	maxCustFilter uint32
	filterIndex   uint32
	file          *uint16
	maxFile       uint32
	fileTitle     *uint16
	maxFileTitle  uint32
	initialDir    *uint16
	title         *uint16
	flags         uint32
	fileOffset    uint16
	fileExtension uint16
	defExt        *uint16
	custData      uintptr
	hook          uintptr
	templateName  *uint16
	reservedPtr   unsafe.Pointer
	reserved      uint32
	flagsEx       uint32
}

const (
	ofnReadOnly      = 0x00000001
	ofnOverwrite     = 0x00000002
	ofnNoChangeDir   = 0x00000008
	ofnAllowMulti    = 0x00000200
	ofnFileMustExist = 0x00001000
	ofnExplorer      = 0x00080000
)

// row is one hit-testable line of the window.
type row struct {
	rect rectT
	kind int // 0 device, 1 button
	idx  int
}

type mainWin struct {
	app      *app
	hwnd     syscall.Handle
	peers    []client.Peer
	sel      int
	status   string
	rows     []row
	btnFile  rectT
	btnOpen  rectT
	btnPause rectT
	fonts    [4]syscall.Handle // title, body, small, mono
	brushBox syscall.Handle
}

var (
	mainMu   sync.Mutex
	mainWinH syscall.Handle
)

// recvEnabled is sampled by the tray accept loop; false closes new sessions at
// once instead of dropping the listening socket (so the port stays reserved).
var recvEnabled atomic.Bool

func init() { recvEnabled.Store(true) }

// openMainWindow shows the main window, creating it on first use. It must be
// called on the tray's UI thread.
func openMainWindow(a *app) {
	// Do not hold mainMu across CreateWindowExW: WM_CREATE arrives on this same
	// thread and currentWin() takes the same mutex, which would deadlock.
	if h := existingMainWindow(); h != 0 {
		procShowWindow.Call(uintptr(h), swShow)
		procSetForegroundWindow.Call(uintptr(h))
		return
	}
	hInst := getModuleHandle()
	cls := utf16Ptr(mainClass)
	cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
	icon, _, _ := procLoadIconW.Call(0, idiApplication)
	wc := wndClassExW{
		cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		lpfnWndProc:   syscall.NewCallback(mainWndProc),
		hInstance:     hInst,
		hIcon:         syscall.Handle(icon),
		hCursor:       syscall.Handle(cursor),
		lpszClassName: cls,
		hbrBackground: syscall.Handle(colorWindow + 1),
	}
	if r, _, _ := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		a.event("desktop.main_class_exists", nil)
		// Class already registered by a previous window: reuse it.
	}
	hwnd, _, _ := procCreateWindowExW.Call(
		wsExAppWindow, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(utf16Ptr("ft · 文件传递"))),
		wsOverlappedWindow, 0x80000000, 0x80000000, 880, 468,
		0, 0, uintptr(hInst), 0)
	if hwnd == 0 {
		err := syscall.GetLastError()
		messageBox("ft", fmt.Sprintf("创建主窗口失败：%v", err))
		return
	}
	mainMu.Lock()
	mainWinH = syscall.Handle(hwnd)
	mainMu.Unlock()
	if a != nil {
		a.event("desktop.main_window", map[string]any{"hwnd": fmt.Sprintf("%x", uintptr(hwnd))})
	}
	procShowWindow.Call(hwnd, swShow)
	procUpdateWindow.Call(hwnd)
}

const wsOverlappedWindow = 0x00CF0000

// w is the per-window state; Win32 only carries the handle, so map it.
var mainWins = map[syscall.Handle]*mainWin{}

func existingMainWindow() syscall.Handle {
	mainMu.Lock()
	defer mainMu.Unlock()
	return mainWinH
}

func currentWin(hwnd syscall.Handle) *mainWin {
	mainMu.Lock()
	defer mainMu.Unlock()
	return mainWins[hwnd]
}

func mainWndProc(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	w := currentWin(hwnd)
	switch msg {
	case 0x0001: // WM_CREATE
		mw := &mainWin{app: trayApp, status: "就绪"}
		mw.makeFonts()
		mw.loadPeers()
		mainMu.Lock()
		mainWins[hwnd] = mw
		mainMu.Unlock()
		procDragAcceptFiles.Call(uintptr(hwnd), 1)
		return 0
	case wmPaint:
		if w != nil {
			w.paint(hwnd)
		}
		return 0
	case wmSizeMsg:
		procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		return 0
	case wmLButtonUp:
		if w != nil {
			w.click(hwnd, int32(lParam&0xffff), int32((lParam>>16)&0xffff))
		}
		return 0
	case wmDropFiles:
		if w != nil {
			w.onDrop(hwnd, syscall.Handle(wParam))
		}
		return 0
	case wmPrint, wmPrintClient:
		// PrintWindow and screen readers ask for the content without generating
		// WM_PAINT; drawing into the supplied DC keeps both paths identical.
		if w != nil {
			w.paintDC(uintptr(wParam), hwnd)
		}
		return 0
	case wmAppPaint:
		if w != nil {
			procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		}
		return 0
	case wmClose:
		procShowWindow.Call(uintptr(hwnd), swHide)
		return 0
	case wmDestroy:
		if w != nil {
			w.freeFonts()
		}
		mainMu.Lock()
		delete(mainWins, hwnd)
		if mainWinH == hwnd {
			mainWinH = 0
		}
		mainMu.Unlock()
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return r
}

// trayApp is set by runTray so the main window can reach the shared state.
var trayApp *app

func (w *mainWin) makeFonts() {
	faces := []struct {
		size int32
		face string
		wt   int32
	}{
		{-22, "Microsoft YaHei UI", 600},
		{-15, "Microsoft YaHei UI", 400},
		{-13, "Microsoft YaHei UI", 400},
		{-13, "Consolas", 400},
	}
	for i, f := range faces {
		h, _, _ := procCreateFontW.Call(uintptr(f.size), 0, 0, 0, uintptr(f.wt), 0, 0, 0,
			1, 0, 0, 5, 0, uintptr(unsafe.Pointer(utf16Ptr(f.face))))
		w.fonts[i] = syscall.Handle(h)
	}
	b, _, _ := procCreateBrush.Call(uintptr(sysColor(colorBtnFace)))
	w.brushBox = syscall.Handle(b)
}

func (w *mainWin) freeFonts() {
	for _, f := range w.fonts {
		if f != 0 {
			procDeleteObject.Call(uintptr(f))
		}
	}
	if w.brushBox != 0 {
		procDeleteObject.Call(uintptr(w.brushBox))
	}
}

func sysColor(idx int) uint32 {
	c, _, _ := procGetSysColor.Call(uintptr(idx))
	return uint32(c)
}

// loadPeers refreshes the paired device list from disk.
func (w *mainWin) loadPeers() {
	peers, err := client.Peers(w.app.pairedPath)
	if err != nil {
		w.status = "读取配对表失败"
		return
	}
	w.peers = peers
	if w.sel >= len(peers) {
		w.sel = len(peers) - 1
	}
	if w.sel < 0 {
		w.sel = 0
	}
}

// layout computes every rectangle the window draws or hit-tests.
func (w *mainWin) layout(width, height int32) {
	const pad = 20
	w.rows = w.rows[:0]
	top := int32(96)
	leftW := int32(300)
	for i := range w.peers {
		r := rectT{pad, top + int32(i)*46, pad + leftW, top + int32(i)*46 + 40}
		w.rows = append(w.rows, row{rect: r, kind: 0, idx: i})
	}
	bw := int32(132)
	by := height - 46
	w.btnFile = rectT{pad, by, pad + bw, by + 32}
	w.btnOpen = rectT{pad + bw + 10, by, pad + 2*bw + 10, by + 32}
	w.btnPause = rectT{width - pad - bw, by, width - pad, by + 32}
}

func (w *mainWin) paint(hwnd syscall.Handle) {
	var ps paintStruct
	hdc, _, _ := procBeginPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))
	defer procEndPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))
	w.paintDC(hdc, hwnd)
}

func (w *mainWin) paintDC(hdc uintptr, hwnd syscall.Handle) {

	var cr rectT
	procGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&cr)))
	w.layout(cr.right, cr.bottom)

	procSetBkMode.Call(hdc, bkTransparent)
	fillRect(hdc, cr, sysColor(colorWindowBg))

	name, _ := os.Hostname()
	dev := "—"
	if w.app != nil {
		dev = "…"
	}
	drawText(hdc, w.fonts[0], "ft · 文件传递", rectT{20, 14, cr.right - 20, 42}, dtSingleLine|dtVCenter, sysColor(colorWindowText))
	sub := fmt.Sprintf("%s · 端口 %d · %s", name, w.app.port, recvWord())
	if w.app != nil {
		if id := w.app.deviceID(); id != "" {
			dev = id
			sub = fmt.Sprintf("%s · %s · 端口 %d · %s", name, dev, w.app.port, recvWord())
		}
	}
	drawText(hdc, w.fonts[2], sub, rectT{20, 44, cr.right - 340, 66}, dtSingleLine|dtVCenter, sysColor(colorGrayText))
	drawText(hdc, w.fonts[2], w.status, rectT{cr.right - 330, 44, cr.right - 20, 66}, dtSingleLine|dtVCenter|dtEndEllipsis|dtRight, sysColor(colorGrayText))
	hline(hdc, 20, cr.right-20, 76)

	drawText(hdc, w.fonts[2], "已配对设备", rectT{20, 74, 200, 94}, dtSingleLine|dtVCenter, sysColor(colorGrayText))
	if len(w.peers) == 0 {
		drawText(hdc, w.fonts[2], "还没有配对设备。\n用 ft pair 或 App 的 6 位码完成配对后，这里会列出设备。",
			rectT{20, 100, 20 + 300, 152}, dtWordBreak, sysColor(colorGrayText))
	}
	for _, r := range w.rows {
		if r.kind != 0 {
			continue
		}
		p := w.peers[r.idx]
		label := p.Name
		if label == "" {
			label = "未命名设备"
		}
		if r.idx == w.sel {
			fillRect(hdc, r.rect, sysColor(colorHighlight))
			drawText(hdc, w.fonts[1], label, rectT{r.rect.left + 10, r.rect.top + 2, r.rect.right - 10, r.rect.top + 22}, dtSingleLine|dtVCenter, sysColor(colorHighText))
			drawText(hdc, w.fonts[3], p.ID, rectT{r.rect.left + 10, r.rect.top + 20, r.rect.right - 10, r.rect.bottom - 2}, dtSingleLine|dtVCenter|dtEndEllipsis, sysColor(colorHighText))
		} else {
			drawText(hdc, w.fonts[1], label, rectT{r.rect.left + 10, r.rect.top + 2, r.rect.right - 10, r.rect.top + 22}, dtSingleLine|dtVCenter, sysColor(colorWindowText))
			drawText(hdc, w.fonts[3], p.ID, rectT{r.rect.left + 10, r.rect.top + 20, r.rect.right - 10, r.rect.bottom - 2}, dtSingleLine|dtVCenter|dtEndEllipsis, sysColor(colorGrayText))
		}
	}

	// Drop zone.
	dropBox := rectT{20 + 320, 96, cr.right - 20, 262}
	fillRect(hdc, dropBox, sysColor(colorBtnFace))
	drawText(hdc, w.fonts[1], "把文件拖到这里发给选中的设备", rectT{dropBox.left, dropBox.top + 40, dropBox.right, dropBox.top + 66}, dtSingleLine|dtCenter|dtVCenter, sysColor(colorWindowText))
	drawText(hdc, w.fonts[2], "或点下方「选择文件…」", rectT{dropBox.left, dropBox.top + 66, dropBox.right, dropBox.top + 90}, dtSingleLine|dtCenter|dtVCenter, sysColor(colorGrayText))

	// Recent transfers.
	drawText(hdc, w.fonts[2], "最近传输", rectT{dropBox.left, 274, dropBox.right, 294}, dtSingleLine|dtVCenter, sysColor(colorGrayText))
	hline(hdc, dropBox.left, dropBox.right, 296)
	recent := w.app.recentLines(5)
	y := int32(304)
	for _, line := range recent {
		drawText(hdc, w.fonts[3], line, rectT{dropBox.left, y, dropBox.right, y + 24}, dtSingleLine|dtVCenter, sysColor(colorWindowText))
		y += 26
	}
	if len(recent) == 0 {
		drawText(hdc, w.fonts[2], "还没有传输记录。接收或发送完成后，这里会按时间倒序列出最近几条。",
			rectT{dropBox.left, y, dropBox.right, y + 60}, dtWordBreak, sysColor(colorGrayText))
	}

	hz := cr.bottom - 26
	hline(hdc, 20, cr.right-20, hz-10)
	button(hdc, w.fonts[1], w.btnFile, "选择文件…", false)
	button(hdc, w.fonts[1], w.btnOpen, "打开接收目录", false)
	pauseLabel := "暂停接收"
	if !recvEnabled.Load() {
		pauseLabel = "恢复接收"
	}
	button(hdc, w.fonts[1], w.btnPause, pauseLabel, false)

}

func recvWord() string {
	if recvEnabled.Load() {
		return "接收中"
	}
	return "已暂停"
}

func (w *mainWin) click(hwnd syscall.Handle, x, y int32) {
	var cr rectT
	procGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&cr)))
	w.layout(cr.right, cr.bottom)
	for _, r := range w.rows {
		if ptIn(r.rect, x, y) {
			w.sel = r.idx
			procInvalidateRect.Call(uintptr(hwnd), 0, 1)
			return
		}
	}
	switch {
	case ptIn(w.btnFile, x, y):
		w.pickAndSend(hwnd)
	case ptIn(w.btnOpen, x, y):
		shellOpen(w.app.dir)
	case ptIn(w.btnPause, x, y):
		on := !recvEnabled.Load()
		recvEnabled.Store(on)
		w.status = "接收已恢复"
		if !on {
			w.status = "接收已暂停"
		}
		procInvalidateRect.Call(uintptr(hwnd), 0, 1)
	}
}

func (w *mainWin) activePeer() (client.Peer, bool) {
	if len(w.peers) == 0 {
		return client.Peer{}, false
	}
	if w.sel < 0 || w.sel >= len(w.peers) {
		w.sel = 0
	}
	return w.peers[w.sel], true
}

func (w *mainWin) pickAndSend(hwnd syscall.Handle) {
	peer, ok := w.activePeer()
	if !ok {
		messageBox("ft", "还没有配对设备：先用 ft pair 或 App 的 6 位码配对")
		return
	}
	files := openFileDialog(uintptr(hwnd))
	if len(files) == 0 {
		return
	}
	w.sendAsync(hwnd, peer, files)
}

func (w *mainWin) onDrop(hwnd syscall.Handle, hdrop syscall.Handle) {
	defer procDragFinish.Call(uintptr(hdrop))
	peer, ok := w.activePeer()
	if !ok {
		messageBox("ft", "还没有配对设备：先用 ft pair 或 App 的 6 位码配对")
		return
	}
	var files []string
	count, _, _ := procDragQueryFileW.Call(uintptr(hdrop), 0xffffffff, 0, 0)
	for i := uintptr(0); i < count; i++ {
		var buf [1024]uint16
		n, _, _ := procDragQueryFileW.Call(uintptr(hdrop), i, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if n > 0 {
			files = append(files, syscall.UTF16ToString(buf[:n]))
		}
	}
	w.sendAsync(hwnd, peer, files)
}

func (w *mainWin) sendAsync(hwnd syscall.Handle, peer client.Peer, files []string) {
	if len(files) == 0 {
		return
	}
	w.status = fmt.Sprintf("正在发送 %d 个文件给 %s…", len(files), peerLabel(peer))
	procInvalidateRect.Call(uintptr(hwnd), 0, 1)
	go func() {
		res, err := sendToPeer(w.app, files, peer)
		msg := fmt.Sprintf("已发送给 %s：%d 个文件，%d 字节", peerLabel(peer), len(res.Files), res.Bytes)
		if err != nil {
			msg = "发送失败：" + err.Error()
		}
		mainMu.Lock()
		if mw := mainWins[hwnd]; mw != nil {
			mw.status = msg
		}
		mainMu.Unlock()
		procPostMessageW.Call(uintptr(hwnd), wmAppPaint, 0, 0)
	}()
}

func peerLabel(p client.Peer) string {
	if p.Name != "" {
		return p.Name
	}
	return truncID(p.ID)
}

// openFileDialog shows the native multi-select dialog (no shell extension
// dependency, works without a browser engine).
func openFileDialog(owner uintptr) []string {
	buf := make([]uint16, 64*1024)
	ofn := openFileNameW{
		hwndOwner: syscall.Handle(owner),
		filter:    utf16Ptr("所有文件 (*.*)\x00*.*\x00\x00"),
		file:      &buf[0],
		maxFile:   uint32(len(buf)),
		title:     utf16Ptr("选择要发送的文件"),
		flags:     ofnExplorer | ofnAllowMulti | ofnFileMustExist | ofnNoChangeDir,
	}
	ofn.structSize = uint32(unsafe.Sizeof(ofn))
	ok, _, _ := procGetOpenFile.Call(uintptr(unsafe.Pointer(&ofn)))
	if ok == 0 {
		return nil
	}
	parts := splitMultiSelect(buf)
	if len(parts) == 1 {
		return parts
	}
	dir := parts[0]
	out := make([]string, 0, len(parts)-1)
	for _, name := range parts[1:] {
		out = append(out, dir+`\`+name)
	}
	return out
}

// splitMultiSelect decodes the double-NUL separated buffer the dialog returns:
// one path when a single file was chosen, else directory followed by names.
func splitMultiSelect(buf []uint16) []string {
	var parts []string
	start := 0
	for i, c := range buf {
		if c != 0 {
			continue
		}
		if i == start { // double NUL: end of list
			break
		}
		parts = append(parts, syscall.UTF16ToString(buf[start:i]))
		start = i + 1
	}
	return parts
}

func ptIn(r rectT, x, y int32) bool {
	return x >= r.left && x < r.right && y >= r.top && y < r.bottom
}

func fillRect(hdc uintptr, r rectT, color uint32) {
	b, _, _ := procCreateBrush.Call(uintptr(color))
	defer procDeleteObject.Call(b)
	rr := r
	procFillRect.Call(hdc, uintptr(unsafe.Pointer(&rr)), b)
}

func hline(hdc uintptr, x1, x2, y int32) {
	fillRect(hdc, rectT{x1, y, x2, y + 1}, sysColor(colorBtnShadow))
}

func drawText(hdc uintptr, font syscall.Handle, s string, r rectT, flags uint32, color uint32) {
	if font != 0 {
		procSelectObject.Call(hdc, uintptr(font))
	}
	procSetTextColor.Call(hdc, uintptr(color))
	p := utf16Ptr(s)
	rr := r
	procDrawTextW.Call(hdc, uintptr(unsafe.Pointer(p)), ^uintptr(0), uintptr(unsafe.Pointer(&rr)), uintptr(flags))
}

func button(hdc uintptr, font syscall.Handle, r rectT, label string, _ bool) {
	fillRect(hdc, r, sysColor(colorBtnFace))
	fillRect(hdc, rectT{r.left, r.top, r.right, r.top + 1}, sysColor(colorBtnHiLight))
	fillRect(hdc, rectT{r.left, r.top, r.left + 1, r.bottom}, sysColor(colorBtnHiLight))
	fillRect(hdc, rectT{r.left, r.bottom - 1, r.right, r.bottom}, sysColor(colorBtnShadow))
	fillRect(hdc, rectT{r.right - 1, r.top, r.right, r.bottom}, sysColor(colorBtnShadow))
	drawText(hdc, font, label, r, dtCenter|dtVCenter|dtSingleLine, sysColor(colorBtnText))
}
