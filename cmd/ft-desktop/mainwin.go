//go:build windows

// The main window is deliberately a plain Win32 window drawn with GDI: the
// desktop app is a small always-running utility, so a browser engine (WebView2
// or an Edge --app window) would cost 100-250 MB of resident memory for a
// screen with one list and three buttons. Everything here is owner-drawn from
// layout(), which both WM_PAINT and hit testing share so the two can never
// disagree about where a row or button is.
//
// The look follows design/tokens.md (plan A, "instrument ledger"): paper and
// ink neutrals, a serif title, tabular mono digits, hairline rules, and path
// stamps that encode LAN / punch / relay / failure as colour + border + text.
// The only animation in the whole window is the dot that breathes while
// receiving, driven by the window's own WM_TIMER.
package main

import (
	"fmt"
	"math"
	"os"
	"strings"
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
	wmEraseBkgnd  = 0x0014
	wmShowWindow  = 0x0018
	wmSetCursor   = 0x0020
	wmGetMinMax   = 0x0024
	wmMouseLeave  = 0x02A3
	wmDpiChanged  = 0x02E0

	wsOverlappedWindow = 0x00CF0000

	dtEndEllipsis = 0x8000
	dtRight       = 0x0002
	dtWordBreak   = 0x0010

	// Layout baseline from design/tokens.md §5, in design pixels at 96 dpi.
	// Everything is multiplied by the monitor DPI at draw time.
	designW    = 880
	designH    = 520
	designMinW = 720
	designMinH = 420

	headerH    = 32
	sideW      = 206
	contentPad = 14
	peerRowH   = 34
	ledgerRowH = 28
	barH       = 46
	stampH     = 18
	btnH       = 28

	// The receiving dot breathes over 1.6 s; 40 ms per frame is smooth enough
	// for a 3 px disc and costs one small invalidation per frame.
	mainTimerID = 8
	breathMs    = 1600
	breathStep  = 40

	tmeLeave = 0x00000002
	srcCopy  = 0x00CC0020
	htClient = 1
	idcHand  = 32649

	stockNullBrush = 5 // GetStockObject(NULL_BRUSH)
	psSolid        = 0
	psDash         = 1

	// Hover targets. Device rows carry their index in hoverIdx; the rest are
	// the constants below.
	hoverNone = iota
	hoverPeer
	hoverZone
	hoverFile
	hoverOpen
	hoverPause
	hoverEmpty
)

var (
	comdlg32 = syscall.NewLazyDLL("comdlg32.dll")
	advapi32 = syscall.NewLazyDLL("advapi32.dll")

	procGetOpenFile  = comdlg32.NewProc("GetOpenFileNameW")
	procSetTextColor = gdi32.NewProc("SetTextColor")
	procSetBkMode    = gdi32.NewProc("SetBkMode")
	procCreateBrush  = gdi32.NewProc("CreateSolidBrush")
	procCreateFontW  = gdi32.NewProc("CreateFontW")
	procSelectObject = gdi32.NewProc("SelectObject")
	procDeleteObject = gdi32.NewProc("DeleteObject")
	procCompatDC     = gdi32.NewProc("CreateCompatibleDC")
	procCompatBmp    = gdi32.NewProc("CreateCompatibleBitmap")
	procDeleteDC     = gdi32.NewProc("DeleteDC")
	procBitBlt       = gdi32.NewProc("BitBlt")
	procStock        = gdi32.NewProc("GetStockObject")
	procEllipse      = gdi32.NewProc("Ellipse")
	procCharExtra    = gdi32.NewProc("SetTextCharacterExtra")
	procTextExtent   = gdi32.NewProc("GetTextExtentPoint32W")
	procTextFace     = gdi32.NewProc("GetTextFaceW")
	procTrackMouse   = user32.NewProc("TrackMouseEvent")
	procSetCursor    = user32.NewProc("SetCursor")
	procRegGetValue  = advapi32.NewProc("RegGetValueW")
	procGetTickCount = kernel32.NewProc("GetTickCount")
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

// ---------------------------------------------------------------------------
// Palette: exactly the token values from design/tokens.md §1.
// COLORREF is 0x00BBGGRR, hence col() taking r, g, b in that order.
// ---------------------------------------------------------------------------

type palette struct {
	paper, paper2, paper3         uint32
	ink, ink2, ink3               uint32
	line, lineSoft                uint32
	accent, accentInk, accentSoft uint32
	danger, amber, violet         uint32
}

func col(r, g, b uint32) uint32 { return b<<16 | g<<8 | r }

var palLight = palette{
	paper:      col(0xF7, 0xF6, 0xF0),
	paper2:     col(0xF0, 0xEE, 0xE7),
	paper3:     col(0xE6, 0xE4, 0xDB),
	ink:        col(0x16, 0x21, 0x1C),
	ink2:       col(0x50, 0x59, 0x55),
	ink3:       col(0x7E, 0x87, 0x83),
	line:       col(0xD6, 0xD4, 0xCC),
	lineSoft:   col(0xE1, 0xE0, 0xD8),
	accent:     col(0x2C, 0x5C, 0x49),
	accentInk:  col(0xFB, 0xFA, 0xF7),
	accentSoft: col(0xD6, 0xED, 0xE2),
	danger:     col(0xB6, 0x40, 0x36),
	amber:      col(0x91, 0x5C, 0x08),
	violet:     col(0x66, 0x51, 0x89),
}

var palDark = palette{
	paper:      col(0x0E, 0x14, 0x11),
	paper2:     col(0x16, 0x1E, 0x1A),
	paper3:     col(0x1E, 0x28, 0x23),
	ink:        col(0xE8, 0xE6, 0xDF),
	ink2:       col(0xAB, 0xA9, 0xA1),
	ink3:       col(0x7E, 0x7C, 0x74),
	line:       col(0x2B, 0x34, 0x30),
	lineSoft:   col(0x22, 0x2A, 0x26),
	accent:     col(0x89, 0xC7, 0xAB),
	accentInk:  col(0x11, 0x19, 0x16),
	accentSoft: col(0x1B, 0x2F, 0x26),
	danger:     col(0xED, 0x81, 0x74),
	amber:      col(0xE3, 0xB6, 0x67),
	violet:     col(0xBE, 0xA3, 0xDF),
}

var (
	palOnce  sync.Once
	palVal   palette
	darkMode bool
)

// theme reads HKCU\...\Themes\Personalize\AppsUseLightTheme once per process:
// the window does not track live theme switches, a restart picks them up.
func theme() (palette, bool) {
	palOnce.Do(func() {
		darkMode = appsUseDarkTheme()
		palVal = palLight
		if darkMode {
			palVal = palDark
		}
	})
	return palVal, darkMode
}

func appsUseDarkTheme() bool {
	const (
		rrfRtRegDword = 0x00000010
		errorSuccess  = 0
	)
	// HKEY_CURRENT_USER is the sign-extended pseudo handle on 64-bit Windows.
	hkey := ^uintptr(0x7ffffffe) // 0xFFFFFFFF80000001
	key := utf16Ptr(`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`)
	name := utf16Ptr("AppsUseLightTheme")
	var v, cb uint32 = 1, 4
	r, _, _ := procRegGetValue.Call(hkey, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(name)),
		rrfRtRegDword, 0, uintptr(unsafe.Pointer(&v)), uintptr(unsafe.Pointer(&cb)))
	if r != errorSuccess {
		return false // value missing: Windows default is the light theme
	}
	return v == 0
}

// ---------------------------------------------------------------------------
// Window state
// ---------------------------------------------------------------------------

// row is one hit-testable line of the window.
type row struct {
	rect rectT
	kind int // 0 device
	idx  int
}

type mainWin struct {
	app      *app
	hwnd     syscall.Handle
	peers    []client.Peer
	sel      int
	status   string
	devID    string
	rows     []row
	rowsTop  int32
	pal      palette
	dark     bool
	dpi      int32
	hover    int
	hoverIdx int
	tracking bool
	timer    uintptr

	// Rects from layout(); shared by painting and hit testing.
	hdrR        rectT
	sideR       rectT
	barR        rectT
	zone        rectT
	pill        rectT
	statusR     rectT
	ledgerLabel rectT
	ledgerHdr   rectT
	ledgerT     rectT
	btnFile     rectT
	btnOpen     rectT
	btnPause    rectT
	btnEmpty    rectT

	fontWin   syscall.Handle
	fontSerif syscall.Handle
	fontBody  syscall.Handle
	fontSm    syscall.Handle
	fontMono  syscall.Handle
}

var (
	mainMu   sync.Mutex
	mainWinH syscall.Handle
)

// recvEnabled is sampled by the tray accept loop; false closes new sessions at
// once instead of dropping the listening socket (so the port stays reserved).
var recvEnabled atomic.Bool

func init() { recvEnabled.Store(true) }

// trayApp is set by runTray so the main window can reach the shared state.
var trayApp *app

// openMainWindow shows the main window, creating it on first use. It must be
// called on the tray's UI thread.
func openMainWindow(a *app) {
	// Do not hold mainMu across CreateWindowExW: WM_CREATE arrives on this same
	// thread and currentWin() takes the same mutex, which would deadlock.
	if h := existingMainWindow(); h != 0 {
		if iconic, _, _ := procIsIconic.Call(uintptr(h)); iconic != 0 {
			procShowWindow.Call(uintptr(h), swRestore)
		} else {
			procShowWindow.Call(uintptr(h), swShow)
		}
		procSetForegroundWindow.Call(uintptr(h))
		if mw := currentWin(h); mw != nil {
			mw.loadPeers()
			mw.syncTimer(h)
		}
		procInvalidateRect.Call(uintptr(h), 0, 1)
		return
	}
	pal, dark := theme()
	dpi := screenDPI()
	ww := scalePx(dpi, designW)
	wh := scalePx(dpi, designH)
	hInst := getModuleHandle()
	cls := utf16Ptr(mainClass)
	cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
	icon := loadAppIcon(0)
	smallIcon := loadAppIcon(16)
	// The class background is the paper colour so the first frame does not
	// flash the system window colour; every pixel is drawn again in WM_PAINT.
	bg, _, _ := procCreateBrush.Call(uintptr(pal.paper))
	wc := wndClassExW{
		cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		lpfnWndProc:   syscall.NewCallback(mainWndProc),
		hInstance:     hInst,
		hIcon:         icon,
		hIconSm:       smallIcon,
		hCursor:       syscall.Handle(cursor),
		lpszClassName: cls,
		hbrBackground: syscall.Handle(bg),
	}
	if r, _, _ := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 && a != nil {
		a.event("desktop.main_class_exists", nil)
		// Already registered by a previous window: reuse it.
	}
	h, _, _ := procCreateWindowExW.Call(
		wsExAppWindow, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(utf16Ptr("轻渡 · Ferry"))),
		wsOverlappedWindow, 0x80000000, 0x80000000, uintptr(ww), uintptr(wh),
		0, 0, uintptr(hInst), 0)
	hwnd := syscall.Handle(h)
	if hwnd == 0 {
		err := syscall.GetLastError()
		messageBox("轻渡", fmt.Sprintf("创建主窗口失败：%v", err))
		return
	}
	mainMu.Lock()
	mainWinH = hwnd
	if mw := mainWins[hwnd]; mw != nil {
		mw.pal, mw.dark = pal, dark
	}
	mainMu.Unlock()
	if a != nil {
		a.event("desktop.main_window", map[string]any{
			"hwnd": fmt.Sprintf("%x", uintptr(hwnd)),
			"dark": dark,
		})
	}
	procShowWindow.Call(uintptr(hwnd), swShow)
	procUpdateWindow.Call(uintptr(hwnd))
}

// mainWins is the handle -> state map: Win32 only carries the handle around.
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
	case wmCreate:
		pal, dark := theme()
		mw := &mainWin{app: trayApp, pal: pal, dark: dark, sel: 0, hover: hoverNone}
		mw.hwnd = hwnd
		mw.dpi = windowDPI(hwnd)
		mw.makeFonts()
		mw.loadPeers()
		mw.refreshDeviceID()
		// The bar starts quiet and states where files land; transient action
		// feedback replaces it later.
		if mw.app != nil {
			mw.status = "接收目录 " + mw.app.dir
		}
		mainMu.Lock()
		mainWins[hwnd] = mw
		mainMu.Unlock()
		procDragAcceptFiles.Call(uintptr(hwnd), 1)
		mw.syncTimer(hwnd)
		return 0
	case wmPaint:
		if w != nil {
			w.paint(hwnd)
		}
		return 0
	case wmEraseBkgnd:
		return 1 // every pixel is painted from layout(); no flicker
	case wmSizeMsg:
		procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		return 0
	case wmShowWindow:
		if w != nil {
			if wParam != 0 {
				w.syncTimer(hwnd)
			} else if w.timer != 0 {
				procKillTimer.Call(uintptr(hwnd), mainTimerID)
				w.timer = 0
			}
		}
		return 0
	case wmTimer:
		if w != nil && wParam == mainTimerID {
			// Only the pill breathes, so only that strip is repainted.
			if r := w.pill; r.right > r.left {
				procInvalidateRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&r)), 0)
			}
		}
		return 0
	case wmMouseMove:
		if w != nil {
			w.onMouseMove(hwnd, int32(lParam&0xffff), int32((lParam>>16)&0xffff))
		}
		return 0
	case wmMouseLeave:
		if w != nil {
			w.tracking = false
			if w.hover != hoverNone {
				w.hover, w.hoverIdx = hoverNone, 0
				procInvalidateRect.Call(uintptr(hwnd), 0, 0)
			}
		}
		return 0
	case wmSetCursor:
		if w != nil && loword(lParam) == htClient && w.cursorIsHand() {
			h, _, _ := procLoadCursorW.Call(0, idcHand)
			procSetCursor.Call(h)
			return 1
		}
		r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
		return r
	case wmGetMinMax:
		if w != nil {
			w.minMaxInfo(hwnd, lParam)
		}
		return 0
	case wmDpiChanged:
		if w != nil {
			w.dpi = int32(loword(wParam))
			w.makeFonts()
			if lParam != 0 {
				pr := (*rectT)(ptrAt(lParam))
				procSetWindowPos.Call(uintptr(hwnd), 0, uintptr(pr.left), uintptr(pr.top),
					uintptr(pr.right-pr.left), uintptr(pr.bottom-pr.top), 0x0004|0x0010)
			}
			procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		}
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
			if w.timer != 0 {
				procKillTimer.Call(uintptr(hwnd), mainTimerID)
				w.timer = 0
			}
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

// minMaxInfo keeps the window from being resized below 720×420 design pixels,
// window frame included.
func (w *mainWin) minMaxInfo(hwnd syscall.Handle, lParam uintptr) {
	type mmi struct {
		reserved     pointT
		maxSize      pointT
		maxPosition  pointT
		minTrack     pointT
		maxTrackSize pointT
	}
	m := (*mmi)(ptrAt(lParam))
	var wr, cr rectT
	procGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&wr)))
	procGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&cr)))
	frameW := (wr.right - wr.left) - cr.right
	frameH := (wr.bottom - wr.top) - cr.bottom
	procDefWindowProcW.Call(uintptr(hwnd), wmGetMinMax, 0, lParam)
	m.minTrack = pointT{w.px(designMinW) + frameW, w.px(designMinH) + frameH}
}

// ptrAt turns a pointer Windows handed us as an integer back into a pointer
// (WM_GETMINMAXINFO and WM_DPICHANGED carry struct pointers in lParam). The
// provenance vet's unsafeptr check wants cannot be expressed here because the
// integer arrives through the message loop, so the value goes through memory,
// which is the round trip package unsafe documents.
func ptrAt(u uintptr) unsafe.Pointer {
	var p unsafe.Pointer
	*(*uintptr)(unsafe.Pointer(&p)) = u
	return p
}

// ---------------------------------------------------------------------------
// Fonts
// ---------------------------------------------------------------------------

var (
	faceOnce  sync.Once
	serifFace string
	sansFace  string
	monoFace  string
)

func pickFaces() {
	pick := func(cands []string) string {
		for _, c := range cands {
			if faceExists(c) {
				return c
			}
		}
		return cands[len(cands)-1]
	}
	serifFace = pick([]string{"Songti SC", "SimSun", "宋体", "Microsoft YaHei UI"})
	sansFace = pick([]string{"Microsoft YaHei UI", "Microsoft YaHei", "Segoe UI"})
	monoFace = pick([]string{"Consolas", "Cascadia Mono", "Courier New"})
}

// faceExists asks GDI which face it actually realized: CreateFontW happily
// returns a font for a missing name and silently substitutes, so the realized
// name is the only honest answer.
func faceExists(name string) bool {
	h, _, _ := procCreateFontW.Call(uintptr(0xFFFFFFF4), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 5, 0,
		uintptr(unsafe.Pointer(utf16Ptr(name))))
	if h == 0 {
		return false
	}
	defer procDeleteObject.Call(h)
	dc, _, _ := procCompatDC.Call(0)
	if dc == 0 {
		return false
	}
	defer procDeleteDC.Call(dc)
	old, _, _ := procSelectObject.Call(dc, h)
	var buf [64]uint16
	n, _, _ := procTextFace.Call(dc, uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])))
	procSelectObject.Call(dc, old)
	if n == 0 {
		return false
	}
	got := strings.ToLower(strings.TrimSpace(syscall.UTF16ToString(buf[:n])))
	want := strings.ToLower(name)
	return got == want || strings.Contains(got, want) || strings.Contains(want, got)
}

func (w *mainWin) makeFonts() {
	faceOnce.Do(pickFaces)
	w.freeFonts()
	mk := func(size int32, weight int32, face string) syscall.Handle {
		h, _, _ := procCreateFontW.Call(uintptr(-w.px(size)), 0, 0, 0, uintptr(weight), 0, 0, 0,
			1, 0, 0, 5, 0, uintptr(unsafe.Pointer(utf16Ptr(face))))
		return syscall.Handle(h)
	}
	w.fontWin = mk(13, 400, serifFace)   // window title, serif 13
	w.fontSerif = mk(17, 400, serifFace) // drop-zone headline, serif 17
	w.fontBody = mk(13, 400, sansFace)   // body 13
	w.fontSm = mk(12, 400, sansFace)     // secondary 12
	w.fontMono = mk(11, 400, monoFace)   // labels, ids, numbers, stamps 11
}

func (w *mainWin) freeFonts() {
	for _, f := range []syscall.Handle{w.fontWin, w.fontSerif, w.fontBody, w.fontSm, w.fontMono} {
		if f != 0 {
			procDeleteObject.Call(uintptr(f))
		}
	}
	w.fontWin, w.fontSerif, w.fontBody, w.fontSm, w.fontMono = 0, 0, 0, 0, 0
}

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

func (w *mainWin) refreshDeviceID() {
	w.devID = ""
	if w.app != nil {
		w.devID = w.app.deviceID()
	}
}

// loadPeers refreshes the paired device list from disk.
func (w *mainWin) loadPeers() {
	if w.app == nil {
		return
	}
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

func (w *mainWin) syncTimer(hwnd syscall.Handle) {
	if recvEnabled.Load() {
		if w.timer == 0 {
			t, _, _ := procSetTimer.Call(uintptr(hwnd), mainTimerID, breathStep, 0)
			w.timer = t
		}
		return
	}
	if w.timer != 0 {
		procKillTimer.Call(uintptr(hwnd), mainTimerID)
		w.timer = 0
	}
}

func (w *mainWin) hasPeers() bool { return len(w.peers) > 0 }

func (w *mainWin) activePeer() (client.Peer, bool) {
	if len(w.peers) == 0 {
		return client.Peer{}, false
	}
	if w.sel < 0 || w.sel >= len(w.peers) {
		w.sel = 0
	}
	return w.peers[w.sel], true
}

// ---------------------------------------------------------------------------
// Layout — one pass, shared by painting and hit testing
// ---------------------------------------------------------------------------

func (w *mainWin) layout(cw, ch int32) {
	p := w.px
	header := p(headerH)
	bar := p(barH)
	pad := p(contentPad)
	side := p(sideW)

	w.hdrR = rectT{0, 0, cw, header}
	w.barR = rectT{0, ch - bar, cw, ch}
	if w.barR.top < header+p(150) { // very small window: keep a usable body
		w.barR.top = header + p(150)
	}
	w.sideR = rectT{0, header, side, w.barR.top}

	w.rowsTop = header + pad + p(22)
	w.rows = w.rows[:0]
	for i := range w.peers {
		y := w.rowsTop + int32(i)*p(peerRowH)
		if y+p(peerRowH) > w.barR.top-p(34) {
			break
		}
		w.rows = append(w.rows, row{rect: rectT{0, y, side, y + p(peerRowH)}, idx: i})
	}
	w.btnEmpty = rectT{pad, w.rowsTop + p(78), pad + p(150), w.rowsTop + p(78) + p(26)}

	paneL := side + 1
	paneR := cw - pad
	labelTop := header + pad
	w.zone = rectT{paneL, labelTop + p(22), paneR, labelTop + p(22) + p(72)}

	labTop := w.zone.bottom + p(14)
	w.ledgerLabel = rectT{paneL, labTop, paneR, labTop + p(18)}
	w.ledgerHdr = rectT{paneL, labTop + p(24), paneR, labTop + p(24) + p(20)}
	w.ledgerT = rectT{paneL, w.ledgerHdr.bottom, paneR, w.barR.top - p(6)}

	bh := p(btnH)
	by := w.barR.top + (bar-bh)/2
	w.btnPause = rectT{paneR - p(88), by, paneR, by + bh}
	w.btnOpen = rectT{w.btnPause.left - p(8) - p(100), by, w.btnPause.left - p(8), by + bh}
	w.btnFile = rectT{w.btnOpen.left - p(8) - p(94), by, w.btnOpen.left - p(8), by + bh}

	ph := p(22)
	w.pill = rectT{pad, w.barR.top + (bar-ph)/2, pad + p(132), w.barR.top + (bar-ph)/2 + ph}
	w.statusR = rectT{w.pill.right + p(14), w.barR.top, w.btnFile.left - p(12), w.barR.bottom}
}

// ---------------------------------------------------------------------------
// Painting
// ---------------------------------------------------------------------------

func (w *mainWin) paint(hwnd syscall.Handle) {
	var ps paintStruct
	hdc, _, _ := procBeginPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))
	defer procEndPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))
	w.paintDC(hdc, hwnd)
}

func (w *mainWin) paintDC(hdc uintptr, hwnd syscall.Handle) {
	var cr rectT
	procGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&cr)))
	if cr.right <= 0 || cr.bottom <= 0 {
		return
	}
	w.layout(cr.right, cr.bottom)

	memDC, _, _ := procCompatDC.Call(hdc)
	if memDC != 0 {
		bmp, _, _ := procCompatBmp.Call(hdc, uintptr(cr.right), uintptr(cr.bottom))
		if bmp != 0 {
			oldBmp, _, _ := procSelectObject.Call(memDC, bmp)
			w.drawScene(memDC, cr)
			procBitBlt.Call(hdc, 0, 0, uintptr(cr.right), uintptr(cr.bottom), memDC, 0, 0, srcCopy)
			procSelectObject.Call(memDC, oldBmp)
			procDeleteObject.Call(bmp)
			procDeleteDC.Call(memDC)
			return
		}
		procDeleteDC.Call(memDC)
	}
	w.drawScene(hdc, cr)
}

func (w *mainWin) drawScene(dc uintptr, cr rectT) {
	procSetBkMode.Call(dc, bkTransparent)
	fillRect(dc, cr, w.pal.paper)
	w.drawHeader(dc, cr)
	w.drawSide(dc, cr)
	w.drawPane(dc, cr)
	w.drawBar(dc, cr)
}

func (w *mainWin) drawHeader(dc uintptr, cr rectT) {
	p := w.px
	fillRect(dc, w.hdrR, w.pal.paper2)
	w.hline(dc, 0, cr.right, w.hdrR.bottom-1, w.pal.lineSoft)

	x := p(contentPad)
	bot := w.hdrR.bottom
	ftW := textWidth(dc, w.fontWin, "轻渡")
	drawText(dc, w.fontWin, "轻渡", rectT{x, 0, x + ftW + p(2), bot}, dtSingleLine|dtVCenter, w.pal.ink)
	drawText(dc, w.fontWin, "· Ferry", rectT{x + ftW + p(4), 0, x + ftW + p(110), bot},
		dtSingleLine|dtVCenter, w.pal.ink2)

	if sub := w.localSummary(); sub != "" {
		drawText(dc, w.fontMono, sub, rectT{x + p(150), 0, cr.right - p(contentPad), bot},
			dtSingleLine|dtVCenter|dtRight|dtEndEllipsis, w.pal.ink3)
	}
}

func (w *mainWin) localSummary() string {
	var parts []string
	if name, err := os.Hostname(); err == nil && name != "" {
		parts = append(parts, name)
	}
	if w.devID != "" {
		parts = append(parts, shortID(w.devID))
	}
	if w.app != nil {
		parts = append(parts, fmt.Sprintf(":%d", w.app.port))
	}
	return strings.Join(parts, " · ")
}

func (w *mainWin) drawSide(dc uintptr, cr rectT) {
	p := w.px
	w.vline(dc, w.sideR.right-1, w.sideR.top, w.sideR.bottom, w.pal.lineSoft)

	x := p(contentPad)
	lab := rectT{x, w.sideR.top + p(contentPad) - p(3), w.sideR.right - x, w.sideR.top + p(contentPad) + p(15)}
	drawTextSpaced(dc, w.fontMono, "设备", lab, dtSingleLine|dtVCenter, w.pal.ink3, p(1))
	drawText(dc, w.fontMono, fmt.Sprintf("%02d", len(w.peers)), lab,
		dtSingleLine|dtVCenter|dtRight, w.pal.accent)

	if len(w.peers) == 0 {
		w.drawEmptyPeers(dc)
	} else {
		for _, r := range w.rows {
			w.drawPeerRow(dc, r)
		}
	}

	// Footer: paired count on the left, route policy on the right.
	fy := w.sideR.bottom - p(30)
	w.hline(dc, x, w.sideR.right-p(1), fy, w.pal.lineSoft)
	fr := rectT{x, fy + p(1), w.sideR.right - x, w.sideR.bottom}
	drawText(dc, w.fontMono, fmt.Sprintf("已配对 %d", len(w.peers)), fr,
		dtSingleLine|dtVCenter, w.pal.ink3)
	drawText(dc, w.fontMono, "LAN·打洞·中继", fr, dtSingleLine|dtVCenter|dtRight, w.pal.ink3)
}

func (w *mainWin) drawPeerRow(dc uintptr, r row) {
	p := w.px
	peer := w.peers[r.idx]
	sel := r.idx == w.sel
	switch {
	case sel:
		fillRect(dc, r.rect, w.pal.accentSoft)
		fillRect(dc, rectT{r.rect.left, r.rect.top, r.rect.left + p(2), r.rect.bottom}, w.pal.accent)
	case w.hover == hoverPeer && w.hoverIdx == r.idx:
		fillRect(dc, r.rect, w.pal.paper2)
	}
	name := peer.Name
	if name == "" {
		name = "未命名设备"
	}
	nameR := rectT{r.rect.left + p(14), r.rect.top + p(1), r.rect.right - p(12), r.rect.top + p(19)}
	drawText(dc, w.fontBody, name, nameR, dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink)
	idR := rectT{nameR.left, r.rect.top + p(17), r.rect.right - p(12), r.rect.top + p(32)}
	drawText(dc, w.fontMono, shortID(peer.ID), idR, dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink3)
}

func (w *mainWin) drawEmptyPeers(dc uintptr) {
	p := w.px
	x := p(contentPad)
	right := w.sideR.right - x
	top := w.rowsTop
	drawText(dc, w.fontBody, "还没有配对设备",
		rectT{x, top, right, top + p(20)}, dtSingleLine|dtVCenter, w.pal.ink)
	drawText(dc, w.fontSm, "在另一台设备上用 6 位码配对，配对后会出现在这里。",
		rectT{x, top + p(24), right, top + p(74)}, dtWordBreak, w.pal.ink3)
	w.btn(dc, w.btnEmpty, "刷新设备列表", true, w.hover == hoverEmpty)
}

func (w *mainWin) drawPane(dc uintptr, cr rectT) {
	p := w.px
	top := w.hdrR.bottom + p(contentPad)
	drawTextSpaced(dc, w.fontMono, "发送",
		rectT{w.zone.left, top, w.zone.right, top + p(16)}, dtSingleLine|dtVCenter, w.pal.ink3, p(1))
	if peer, ok := w.activePeer(); ok {
		drawText(dc, w.fontMono, "→ "+peerLabel(peer),
			rectT{w.zone.left, top, w.zone.right, top + p(16)},
			dtSingleLine|dtVCenter|dtRight|dtEndEllipsis, w.pal.ink3)
	}

	// Drop zone: paper-2 fill, 1px dashed line border, centred hint.
	zoneFill, zoneEdge := w.pal.paper2, w.pal.line
	if w.hover == hoverZone {
		zoneFill, zoneEdge = w.pal.accentSoft, w.pal.accent
	}
	roundBox(dc, w.zone, p(7), zoneFill, zoneFill)
	dashedBox(dc, w.zone, p(7), zoneEdge)
	cy := (w.zone.top + w.zone.bottom) / 2
	big := "拖文件到这里"
	if !w.hasPeers() {
		big = "先配对一台设备"
	}
	drawText(dc, w.fontSerif, big,
		rectT{w.zone.left, cy - p(22), w.zone.right, cy + p(1)},
		dtCenter|dtSingleLine|dtVCenter, w.pal.ink)
	drawText(dc, w.fontSm, "或点下方「选择文件…」发送给左侧选中的设备",
		rectT{w.zone.left + p(10), cy + p(1), w.zone.right - p(10), cy + p(21)},
		dtCenter|dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)

	w.drawLedger(dc)
}

func (w *mainWin) drawLedger(dc uintptr) {
	p := w.px
	rows := w.ledgerRows()
	c := w.columns()

	drawTextSpaced(dc, w.fontMono, "最近传输", w.ledgerLabel,
		dtSingleLine|dtVCenter, w.pal.ink3, p(1))
	drawText(dc, w.fontMono, fmt.Sprintf("%02d", len(rows)), w.ledgerLabel,
		dtSingleLine|dtVCenter|dtRight, w.pal.accent)

	w.hline(dc, w.ledgerLabel.left, w.ledgerLabel.right, w.ledgerLabel.bottom+p(3), w.pal.line)

	hc := w.ledgerHdr
	stamp := w.stampCol()
	head := func(r rectT, s string, right bool) {
		flags := uint32(dtSingleLine | dtVCenter)
		if right {
			flags |= dtRight
		}
		drawText(dc, w.fontMono, s, rectT{r.left, hc.top, r.right, hc.bottom}, flags, w.pal.ink3)
	}
	head(c.time, "时间", true)
	head(c.dir, "方向", false)
	head(c.peer, "对端", false)
	head(c.files, "文件", false)
	head(c.size, "大小", true)
	head(c.dur, "用时", true)
	head(stamp, "路径", true)

	if len(rows) == 0 || w.ledgerT.bottom <= w.ledgerHdr.bottom+p(ledgerRowH)/2 {
		drawText(dc, w.fontSm, "还没有传输记录。收发完成后，这里按时间倒序列出最近几条。",
			rectT{c.peer.left, hc.bottom + p(4), w.ledgerT.right, hc.bottom + p(44)},
			dtWordBreak, w.pal.ink3)
		return
	}

	y := w.ledgerHdr.bottom
	for _, r := range rows {
		h := p(ledgerRowH)
		if y+h > w.ledgerT.bottom {
			break
		}
		line := rectT{w.ledgerT.left, y, w.ledgerT.right, y + h}
		val := func(c rectT, s string, right bool, font syscall.Handle, color uint32) {
			flags := uint32(dtSingleLine | dtVCenter)
			if right {
				flags |= dtRight
			}
			drawText(dc, font, s, rectT{c.left, line.top, c.right, line.bottom}, flags, color)
		}
		val(c.time, dashIfEmpty(r.time), true, w.fontMono, w.pal.ink3)
		val(c.dir, dashIfEmpty(r.dir), false, w.fontMono, w.pal.ink2)
		val(c.peer, dashIfEmpty(r.peer), false, w.fontBody, w.pal.ink)
		val(c.files, dashIfEmpty(r.files), false, w.fontSm, w.pal.ink2)
		val(c.size, dashIfEmpty(r.size), true, w.fontMono, w.pal.ink)
		val(c.dur, dashIfEmpty(r.dur), true, w.fontMono, w.pal.ink2)
		if r.path != "" {
			label, color := pathStamp(r.path, w.pal)
			w.stamp(dc, w.ledgerT.right, (line.top+line.bottom)/2, label, color)
		} else {
			drawText(dc, w.fontMono, "—", rectT{stamp.left, line.top, stamp.right, line.bottom},
				dtSingleLine|dtVCenter|dtRight, w.pal.ink3)
		}
		w.hline(dc, line.left, line.right, line.bottom-1, w.pal.lineSoft)
		y += h
	}
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

type ledgerCols struct {
	time, dir, peer, files, size, dur rectT
}

// columns splits the ledger horizontally: time and the three number columns are
// right-aligned mono, the peer name takes whatever is left.
func (w *mainWin) columns() ledgerCols {
	p := w.px
	l, r := w.ledgerT.left, w.ledgerT.right
	timeW, dirW, filesW, sizeW, durW, stampW := p(44), p(24), p(64), p(62), p(52), p(54)
	var c ledgerCols
	c.time = rectT{l, 0, l + timeW, 0}
	c.dir = rectT{c.time.right + p(8), 0, c.time.right + p(8) + dirW, 0}
	c.size = rectT{r - stampW - p(8) - durW - p(8) - sizeW, 0, r - stampW - p(8) - durW - p(8), 0}
	c.dur = rectT{c.size.right + p(8), 0, c.size.right + p(8) + durW, 0}
	c.files = rectT{c.size.left - p(10) - filesW, 0, c.size.left - p(10), 0}
	c.peer = rectT{c.dir.right + p(10), 0, c.files.left - p(8), 0}
	return c
}

func (w *mainWin) stampCol() rectT {
	p := w.px
	r := w.ledgerT.right
	return rectT{r - p(54), 0, r, 0}
}

// ledgerRow is one parsed line of app.recent ("15:04  → Name  2 个文件 · 2.1 MB").
// That text is the only record the desktop keeps (the fields are shared with
// the tray and the drop panel), so the ledger parses it back into columns
// instead of inventing a parallel data structure.
type ledgerRow struct {
	time, dir, peer, files, size, dur, path string
}

func (w *mainWin) ledgerRows() []ledgerRow {
	if w.app == nil {
		return nil
	}
	lines := w.app.recentLines(8)
	out := make([]ledgerRow, 0, len(lines))
	for _, l := range lines {
		out = append(out, parseLedgerLine(l))
	}
	return out
}

func parseLedgerLine(line string) ledgerRow {
	var r ledgerRow
	s := strings.TrimSpace(line)
	if i := strings.IndexAny(s, "→←"); i >= 0 {
		r.dir = "发"
		if strings.HasPrefix(s[i:], "←") {
			r.dir = "收"
		}
		if t := strings.TrimSpace(s[:i]); isClock(t) {
			r.time = t
		}
		s = strings.TrimSpace(s[i+len("→"):])
	}
	segs := strings.Split(s, "·")
	head := strings.TrimSpace(segs[0])
	// "Name  2 个文件" keeps the peer and the file count in one segment.
	if j := strings.Index(head, "  "); j >= 0 {
		r.peer = strings.TrimSpace(head[:j])
		r.files = strings.TrimSpace(head[j:])
	} else {
		r.peer = head
	}
	for _, seg := range segs[1:] {
		t := strings.TrimSpace(seg)
		switch {
		case t == "":
		case strings.HasSuffix(t, "个文件"):
			r.files = t
		case isPathToken(t):
			r.path = t
		case isDurationToken(t):
			r.dur = t
		case isSizeToken(t):
			r.size = t
		default:
			if r.files == "" {
				r.files = t
			}
		}
	}
	return r
}

func isClock(s string) bool {
	if len(s) != 5 || s[2] != ':' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if i == 2 {
			continue
		}
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func isSizeToken(s string) bool {
	f := strings.Fields(s)
	if len(f) != 2 || f[0] == "" || f[0][0] < '0' || f[0][0] > '9' {
		return false
	}
	switch f[1] {
	case "B", "KB", "MB", "GB", "TB":
		return true
	}
	return false
}

func isDurationToken(s string) bool {
	f := strings.Fields(s)
	if len(f) != 2 || f[0] == "" || f[0][0] < '0' || f[0][0] > '9' {
		return false
	}
	switch f[1] {
	case "ms", "s", "秒", "毫秒", "分", "min":
		return true
	}
	return false
}

func isPathToken(s string) bool {
	switch strings.ToLower(s) {
	case "lan", "打洞", "punch", "中继", "relay", "失败", "failed", "error", "未配对":
		return true
	}
	return false
}

// pathStamp maps a path word to its stamp label and token colour: LAN is the
// accent (it is the good case), punch is amber, relay is violet, failures are
// danger. Colour never carries the meaning alone — the label spells it out.
func pathStamp(word string, pal palette) (string, uint32) {
	switch strings.ToLower(word) {
	case "lan":
		return "LAN", pal.accent
	case "打洞", "punch":
		return "打洞", pal.amber
	case "中继", "relay":
		return "中继", pal.violet
	}
	return "失败", pal.danger
}

func (w *mainWin) drawBar(dc uintptr, cr rectT) {
	fillRect(dc, w.barR, w.pal.paper2)
	w.hline(dc, 0, cr.right, w.barR.top, w.pal.lineSoft)

	on := recvEnabled.Load()
	w.drawPill(dc, w.pill, on)
	if w.status != "" {
		drawText(dc, w.fontSm, w.status, w.statusR, dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink3)
	}
	w.btn(dc, w.btnFile, "选择文件…", true, w.hover == hoverFile)
	w.btn(dc, w.btnOpen, "打开接收目录", false, w.hover == hoverOpen)
	if on {
		w.btn(dc, w.btnPause, "暂停接收", false, w.hover == hoverPause)
	} else {
		w.btn(dc, w.btnPause, "恢复接收", true, w.hover == hoverPause)
	}
}

// drawPill is the receiving indicator: a breathing accent dot plus the port.
func (w *mainWin) drawPill(dc uintptr, r rectT, on bool) {
	p := w.px
	if r.right <= r.left {
		return
	}
	roundBox(dc, r, (r.bottom-r.top)/2, w.pal.paper, w.pal.line)

	cx := r.left + p(13)
	cy := (r.top + r.bottom) / 2
	color := w.pal.ink3
	if on {
		color = blend(w.pal.paper, w.pal.accent, breathAlpha())
	}
	disc(dc, cx, cy, p(3), color)

	port := 0
	if w.app != nil {
		port = w.app.port
	}
	label := fmt.Sprintf("接收中 · :%d", port)
	if !on {
		label = fmt.Sprintf("已暂停 · :%d", port)
	}
	drawText(dc, w.fontMono, label, rectT{cx + p(9), r.top, r.right - p(10), r.bottom},
		dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)
}

// breathAlpha runs 0.55 -> 1 -> 0.55 over breathMs: the only animation here.
func breathAlpha() float64 {
	t, _, _ := procGetTickCount.Call()
	phase := float64(uint32(t)%breathMs) / float64(breathMs)
	return 0.55 + 0.45*(0.5+0.5*math.Sin(2*math.Pi*phase-math.Pi/2))
}

// btn draws one button: the primary style is accent-filled, the secondary is
// outlined. Both use the 6 px radius from the tokens.
func (w *mainWin) btn(dc uintptr, r rectT, label string, primary, hover bool) {
	p := w.px
	if primary {
		fill := w.pal.accent
		if hover {
			fill = shade(w.pal.accent, w.dark, 0.12)
		}
		roundBox(dc, r, p(6), fill, fill)
		drawText(dc, w.fontSm, label, r, dtCenter|dtSingleLine|dtVCenter, w.pal.accentInk)
		return
	}
	fill := w.pal.paper
	if hover {
		fill = w.pal.paper3
	}
	roundBox(dc, r, p(6), fill, w.pal.line)
	drawText(dc, w.fontSm, label, r, dtCenter|dtSingleLine|dtVCenter, w.pal.ink)
}

// stamp draws the path badge: 1px border, mono 11 text, 18 px tall.
func (w *mainWin) stamp(dc uintptr, right, cy int32, label string, color uint32) {
	p := w.px
	half := p(stampH) / 2
	padX := p(6)
	tw := textWidth(dc, w.fontMono, label) + p(1)
	r := rectT{right - tw - padX*2, cy - half, right, cy + half}
	roundBox(dc, r, p(3), w.pal.paper, color)
	drawTextSpaced(dc, w.fontMono, label, r, dtCenter|dtSingleLine|dtVCenter, color, p(1))
}

// ---------------------------------------------------------------------------
// Interaction
// ---------------------------------------------------------------------------

func (w *mainWin) cursorIsHand() bool {
	switch w.hover {
	case hoverFile, hoverOpen, hoverPause, hoverEmpty, hoverZone, hoverPeer:
		return true
	}
	return false
}

func (w *mainWin) onMouseMove(hwnd syscall.Handle, x, y int32) {
	var cr rectT
	procGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&cr)))
	w.layout(cr.right, cr.bottom)

	kind, idx := hoverNone, 0
	switch {
	case ptIn(w.btnFile, x, y):
		kind = hoverFile
	case ptIn(w.btnOpen, x, y):
		kind = hoverOpen
	case ptIn(w.btnPause, x, y):
		kind = hoverPause
	case !w.hasPeers() && ptIn(w.btnEmpty, x, y):
		kind = hoverEmpty
	case ptIn(w.zone, x, y):
		kind = hoverZone
	default:
		for _, r := range w.rows {
			if ptIn(r.rect, x, y) {
				kind, idx = hoverPeer, r.idx
				break
			}
		}
	}
	if kind != w.hover || idx != w.hoverIdx {
		w.hover, w.hoverIdx = kind, idx
		procInvalidateRect.Call(uintptr(hwnd), 0, 0)
	}
	if !w.tracking {
		var tme struct {
			cbSize    uint32
			flags     uint32
			hwnd      syscall.Handle
			hoverTime uint32
		}
		tme.cbSize = uint32(unsafe.Sizeof(tme))
		tme.flags = tmeLeave
		tme.hwnd = hwnd
		procTrackMouse.Call(uintptr(unsafe.Pointer(&tme)))
		w.tracking = true
	}
}

func (w *mainWin) click(hwnd syscall.Handle, x, y int32) {
	var cr rectT
	procGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&cr)))
	w.layout(cr.right, cr.bottom)

	switch {
	case ptIn(w.btnFile, x, y):
		w.pickAndSend(hwnd)
		return
	case ptIn(w.btnOpen, x, y):
		if w.app != nil {
			shellOpen(w.app.dir)
		}
		return
	case ptIn(w.btnPause, x, y):
		on := !recvEnabled.Load()
		recvEnabled.Store(on)
		w.status = "接收已暂停"
		if on {
			w.status = "接收已恢复"
		}
		w.syncTimer(hwnd)
		procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		return
	case !w.hasPeers() && ptIn(w.btnEmpty, x, y):
		w.loadPeers()
		w.status = fmt.Sprintf("已读取 %d 台配对设备", len(w.peers))
		if len(w.peers) == 0 {
			w.status = "还没有配对设备"
		}
		procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		return
	}
	for _, r := range w.rows {
		if ptIn(r.rect, x, y) {
			w.sel = r.idx
			procInvalidateRect.Call(uintptr(hwnd), 0, 1)
			return
		}
	}
	if ptIn(w.zone, x, y) {
		w.pickAndSend(hwnd)
	}
}

func (w *mainWin) pickAndSend(hwnd syscall.Handle) {
	peer, ok := w.activePeer()
	if !ok {
		messageBox("轻渡", "还没有配对设备：先用 ferry/ft pair 或 App 的 6 位码配对")
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
		messageBox("轻渡", "还没有配对设备：先用 ferry/ft pair 或 App 的 6 位码配对")
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
	if len(files) == 0 || w.app == nil {
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

// ---------------------------------------------------------------------------
// GDI helpers
// ---------------------------------------------------------------------------

func (w *mainWin) px(v int32) int32 {
	if w.dpi == 0 {
		return v
	}
	return int32(math.Round(float64(v) * float64(w.dpi) / 96.0))
}

func scalePx(dpi int32, v int32) int32 {
	if dpi == 0 {
		dpi = 96
	}
	return int32(math.Round(float64(v) * float64(dpi) / 96.0))
}

func screenDPI() int32 {
	if dpi, _, _ := procGetDpiForSystem.Call(); dpi != 0 {
		return int32(dpi)
	}
	return 96
}

func windowDPI(hwnd syscall.Handle) int32 {
	if dpi, _, _ := procGetDpiForWindow.Call(uintptr(hwnd)); dpi != 0 {
		return int32(dpi)
	}
	return screenDPI()
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

func (w *mainWin) hline(hdc uintptr, x1, x2, y int32, color uint32) {
	fillRect(hdc, rectT{x1, y, x2, y + 1}, color)
}

func (w *mainWin) vline(hdc uintptr, x, y1, y2 int32, color uint32) {
	fillRect(hdc, rectT{x, y1, x + 1, y2}, color)
}

// roundBox fills a rounded rectangle and strokes it with a 1px pen. GDI pens
// and brushes are created per call: at 25 frames per second for one small dot
// that is cheaper than caching a palette of objects per window.
func roundBox(hdc uintptr, r rectT, radius int32, fill, edge uint32) {
	br, _, _ := procCreateBrush.Call(uintptr(fill))
	pen, _, _ := procCreatePen.Call(psSolid, 1, uintptr(edge))
	oldBr, _, _ := procSelectObject.Call(hdc, br)
	oldPen, _, _ := procSelectObject.Call(hdc, pen)
	procRoundRect.Call(hdc, uintptr(r.left), uintptr(r.top), uintptr(r.right), uintptr(r.bottom),
		uintptr(radius), uintptr(radius))
	procSelectObject.Call(hdc, oldBr)
	procSelectObject.Call(hdc, oldPen)
	procDeleteObject.Call(br)
	procDeleteObject.Call(pen)
}

// dashedBox strokes a rounded rectangle with a 1px dashed pen: the GDI
// equivalent of the mock's dashed drop zone. PS_DASH only works at width 1.
func dashedBox(hdc uintptr, r rectT, radius int32, edge uint32) {
	pen, _, _ := procCreatePen.Call(psDash, 1, uintptr(edge))
	oldPen, _, _ := procSelectObject.Call(hdc, pen)
	nullBrush, _, _ := procStock.Call(stockNullBrush)
	oldBr, _, _ := procSelectObject.Call(hdc, nullBrush)
	procRoundRect.Call(hdc, uintptr(r.left), uintptr(r.top), uintptr(r.right-1), uintptr(r.bottom-1),
		uintptr(radius), uintptr(radius))
	procSelectObject.Call(hdc, oldBr)
	procSelectObject.Call(hdc, oldPen)
	procDeleteObject.Call(pen)
}

// disc draws a filled circle. GDI shapes have no alpha, so the breathing is a
// blend towards the paper colour instead of an opacity change.
func disc(hdc uintptr, cx, cy, rad int32, color uint32) {
	br, _, _ := procCreateBrush.Call(uintptr(color))
	pen, _, _ := procCreatePen.Call(psSolid, 1, uintptr(color))
	oldBr, _, _ := procSelectObject.Call(hdc, br)
	oldPen, _, _ := procSelectObject.Call(hdc, pen)
	procEllipse.Call(hdc, uintptr(cx-rad), uintptr(cy-rad), uintptr(cx+rad+1), uintptr(cy+rad+1))
	procSelectObject.Call(hdc, oldBr)
	procSelectObject.Call(hdc, oldPen)
	procDeleteObject.Call(br)
	procDeleteObject.Call(pen)
}

// blend mixes two COLORREFs: t=0 is a, t=1 is b.
func blend(a, b uint32, t float64) uint32 {
	mix := func(x, y uint32) uint32 {
		v := float64(x) + (float64(y)-float64(x))*t
		if v < 0 {
			v = 0
		}
		if v > 255 {
			v = 255
		}
		return uint32(v + 0.5)
	}
	return mix(a&0xff, b&0xff) |
		mix((a>>8)&0xff, (b>>8)&0xff)<<8 |
		mix((a>>16)&0xff, (b>>16)&0xff)<<16
}

// shade nudges a colour towards black (light theme) or white (dark theme); it
// is how the primary button gets a hover state without a second token.
func shade(c uint32, dark bool, amount float64) uint32 {
	if dark {
		return blend(c, 0x00FFFFFF, amount)
	}
	return blend(c, 0x00000000, amount)
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

// drawTextSpaced is drawText with letter spacing (SetTextCharacterExtra), used
// for the small labels and the stamps.
func drawTextSpaced(hdc uintptr, font syscall.Handle, s string, r rectT, flags uint32, color uint32, extra int32) {
	if extra != 0 {
		procCharExtra.Call(hdc, uintptr(extra))
		defer procCharExtra.Call(hdc, 0)
	}
	drawText(hdc, font, s, r, flags, color)
}

type sizeT struct{ cx, cy int32 }

func textWidth(hdc uintptr, font syscall.Handle, s string) int32 {
	if font != 0 {
		procSelectObject.Call(hdc, uintptr(font))
	}
	var sz sizeT
	u := syscall.StringToUTF16(s)
	if len(u) <= 1 {
		return 0
	}
	procTextExtent.Call(hdc, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&sz)))
	return sz.cx
}
