//go:build windows

package main

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/cwlindean/filetransfer/core/client"
	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/pairing"
)

const (
	mainClass     = "ftMainWin"
	wmAppPaint    = wmApp + 10
	wmAppReload   = wmApp + 11
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

	designW    = 880
	designH    = 540
	designMinW = 720
	designMinH = 440

	railW      = 64
	topBarH    = 36
	sendColW   = 312
	contentPad = 24

	mainTimerID = 8
	breathStep  = 100

	tmeLeave = 0x00000002
	srcCopy  = 0x00CC0020
	htClient = 1
	idcHand  = 32649

	psSolid = 0

	pageHome     = 0
	pageDevices  = 1
	pageSettings = 2

	fltAll  = 0
	fltSend = 1
	fltRecv = 2

	hoverNone = iota
	hoverCapMin
	hoverCapMax
	hoverCapClose
	hoverNavHome
	hoverNavDevices
	hoverNavSettings
	hoverTileFiles
	hoverTileFolder
	hoverQueueClear
	hoverAddDevice
	hoverPeerPicker
	hoverSendBtn
	hoverSendText
	hoverSendClip
	hoverTempShare
	hoverFltAll
	hoverFltSend
	hoverFltRecv
	hoverOpenDir
	hoverLedgerRow
	hoverDevAddBtn
	hoverDevSendBtn
	hoverDevRenameBtn
	hoverDevDeleteBtn
	hoverSetRecv
	hoverSetResetDir
	hoverSetChangeDir
	hoverSetOpenDir
	hoverSetAuto
	hoverSetRename
	hoverSetCopyID
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
	ofnNoChangeDir   = 0x00000008
	ofnAllowMulti    = 0x00000200
	ofnFileMustExist = 0x00001000
	ofnExplorer      = 0x00080000
)

type palette struct {
	paper, paper2, paper3         uint32
	ink, ink2, ink3               uint32
	line, lineSoft                uint32
	accent, accentInk, accentSoft uint32
	danger, amber, violet         uint32
}

func col(r, g, b uint32) uint32 { return b<<16 | g<<8 | r }

var palLight = palette{
	paper:      col(0xFF, 0xFF, 0xFF),
	paper2:     col(0xFA, 0xFA, 0xFA),
	paper3:     col(0xF3, 0xF4, 0xF6),
	ink:        col(0x11, 0x18, 0x27),
	ink2:       col(0x4B, 0x55, 0x63),
	ink3:       col(0x9C, 0xA3, 0xAF),
	line:       col(0xE5, 0xE7, 0xEB),
	lineSoft:   col(0xF3, 0xF4, 0xF6),
	accent:     col(0x0D, 0x94, 0x88),
	accentInk:  col(0xFF, 0xFF, 0xFF),
	accentSoft: col(0xF0, 0xFD, 0xFA),
	danger:     col(0xDC, 0x26, 0x26),
	amber:      col(0xD9, 0x77, 0x06),
	violet:     col(0x7C, 0x3A, 0xED),
}

var palDark = palette{
	paper:      col(0x11, 0x18, 0x27),
	paper2:     col(0x18, 0x22, 0x30),
	paper3:     col(0x1F, 0x29, 0x37),
	ink:        col(0xF3, 0xF4, 0xF6),
	ink2:       col(0x9C, 0xA3, 0xAF),
	ink3:       col(0x6B, 0x72, 0x80),
	line:       col(0x27, 0x32, 0x44),
	lineSoft:   col(0x1F, 0x29, 0x37),
	accent:     col(0x14, 0xB8, 0xA6),
	accentInk:  col(0xFF, 0xFF, 0xFF),
	accentSoft: col(0x13, 0x35, 0x33),
	danger:     col(0xF8, 0x71, 0x71),
	amber:      col(0xFB, 0xBF, 0x24),
	violet:     col(0xA7, 0x8B, 0xFA),
}

var (
	palOnce  sync.Once
	palVal   palette
	darkMode bool
)

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
	hkey := ^uintptr(0x7ffffffe)
	key := utf16Ptr(`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`)
	name := utf16Ptr("AppsUseLightTheme")
	var v, cb uint32 = 1, 4
	r, _, _ := procRegGetValue.Call(hkey, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(name)),
		rrfRtRegDword, 0, uintptr(unsafe.Pointer(&v)), uintptr(unsafe.Pointer(&cb)))
	if r != errorSuccess {
		return false
	}
	return v == 0
}

type mainWin struct {
	app      *app
	hwnd     syscall.Handle
	peers    []client.Peer
	sel      int
	page     int
	filter   int
	queued   []string
	status   string
	hostName string
	fullID   string
	pal      palette
	dark     bool
	acrylic  bool
	dpi      int32
	hover    int
	hoverIdx int
	tracking bool
	timer    uintptr

	// Hit-test rectangles updated on every layout() pass.
	titleBarR   rectT
	capMinR     rectT
	capMaxR     rectT
	capCloseR   rectT
	railR       rectT
	navHomeR    rectT
	navDevicesR rectT
	navSetR     rectT

	sendColR      rectT
	sendCardR     rectT
	tileFilesR    rectT
	tileFolderR   rectT
	queueBoxR     rectT
	queueClearR   rectT
	addDevLinkR   rectT
	peerPickerR   rectT
	sendBtnR      rectT
	sendTextR     rectT
	sendClipR     rectT
	tempShareBtnR rectT

	fltAllR     rectT
	fltSendR    rectT
	fltRecvR    rectT
	openDirR    rectT
	activeTxR   rectT
	ledgerRowsR []rectT

	devAddBtnR     rectT
	devSendBtnsR   []rectT
	devRenameBtnsR []rectT
	devDeleteBtnsR []rectT

	setRecvBtnR   rectT
	setResetDirR  rectT
	setChangeDirR rectT
	setOpenDirR   rectT
	setAutoBtnR   rectT
	setRenameBtnR rectT
	setCopyIDRowR rectT

	fontH1   syscall.Handle
	fontH2   syscall.Handle
	fontBody syscall.Handle
	fontBold syscall.Handle
	fontSm   syscall.Handle
	fontMono syscall.Handle
}

var (
	mainMu   sync.Mutex
	mainWinH syscall.Handle
)

var recvEnabled atomic.Bool

func init() { recvEnabled.Store(true) }

var trayApp *app

func openMainWindow(a *app) {
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
	}
	h, _, _ := procCreateWindowExW.Call(
		wsExAppWindow, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(utf16Ptr("轻渡"))),
		wsOverlappedWindow&^0x00080000, 0x80000000, 0x80000000, uintptr(ww), uintptr(wh),
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
		mw := &mainWin{app: trayApp, pal: pal, dark: dark, sel: 0, page: pageHome, filter: fltAll}
		mw.hwnd = hwnd
		mw.dpi = windowDPI(hwnd)
		procSetWindowPos.Call(uintptr(hwnd), 0, 0, 0, 0, 0, 0x0027)
		mw.acrylic = enableAcrylicBackdrop(hwnd, dark)
		mw.makeFonts()
		mw.loadPeers()
		mw.refreshLocalMeta()
		mainMu.Lock()
		mainWins[hwnd] = mw
		mainMu.Unlock()
		procDragAcceptFiles.Call(uintptr(hwnd), 1)
		mw.syncTimer(hwnd)
		return 0
	case wmNcCalcSize:
		if wParam != 0 {
			if zoomed, _, _ := procIsZoomed.Call(uintptr(hwnd)); zoomed != 0 && w != nil {
				r := (*rectT)(ptrAt(lParam))
				pad := w.maxFramePad()
				r.left += pad
				r.top += pad
				r.right -= pad
				r.bottom -= pad
			}
			return 0
		}
	case wmNcHitTest:
		if w != nil {
			return w.ncHitTest(hwnd, lParam)
		}
	case wmPaint:
		if w != nil {
			w.paint(hwnd)
		}
		return 0
	case wmEraseBkgnd:
		return 1
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
		if w != nil {
			w.paintDC(uintptr(wParam), hwnd)
		}
		return 0
	case wmAppPaint:
		if w != nil {
			procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		}
		return 0
	case wmAppReload:
		if w != nil {
			w.loadPeers()
			w.refreshLocalMeta()
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

func (w *mainWin) minMaxInfo(hwnd syscall.Handle, lParam uintptr) {
	type mmi struct {
		reserved     pointT
		maxSize      pointT
		maxPosition  pointT
		minTrack     pointT
		maxTrackSize pointT
	}
	m := (*mmi)(ptrAt(lParam))
	procDefWindowProcW.Call(uintptr(hwnd), wmGetMinMax, 0, lParam)
	m.minTrack = pointT{w.px(designMinW), w.px(designMinH)}
}

func (w *mainWin) maxFramePad() int32 {
	const (
		smCxSizeFrame    = 32
		smCxPaddedBorder = 92
	)
	dpi := uintptr(w.dpi)
	if dpi == 0 {
		dpi = 96
	}
	if err := procGetSystemMetricsForDpi.Find(); err == nil {
		f, _, _ := procGetSystemMetricsForDpi.Call(smCxSizeFrame, dpi)
		p, _, _ := procGetSystemMetricsForDpi.Call(smCxPaddedBorder, dpi)
		if f+p > 0 {
			return int32(f + p)
		}
	}
	f, _, _ := procGetSystemMetrics.Call(smCxSizeFrame)
	p, _, _ := procGetSystemMetrics.Call(smCxPaddedBorder)
	if f+p > 0 {
		return int32(f + p)
	}
	return w.px(8)
}

func (w *mainWin) ncHitTest(hwnd syscall.Handle, lParam uintptr) uintptr {
	pt := pointT{int32(int16(lParam & 0xffff)), int32(int16((lParam >> 16) & 0xffff))}
	procScreenToClient.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&pt)))
	var cr rectT
	procGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&cr)))
	w.layout(cr.right, cr.bottom)

	if zoomed, _, _ := procIsZoomed.Call(uintptr(hwnd)); zoomed == 0 {
		bw := w.px(6)
		onL := pt.x < bw
		onR := pt.x >= cr.right-bw
		onT := pt.y < bw
		onB := pt.y >= cr.bottom-bw
		switch {
		case onT && onL:
			return htTopLeft
		case onT && onR:
			return htTopRight
		case onB && onL:
			return htBottomLeft
		case onB && onR:
			return htBottomRight
		case onL:
			return htLeft
		case onR:
			return htRight
		case onT:
			return htTop
		case onB:
			return htBottom
		}
	}
	if ptIn(w.titleBarR, pt.x, pt.y) &&
		!ptIn(w.capMinR, pt.x, pt.y) &&
		!ptIn(w.capMaxR, pt.x, pt.y) &&
		!ptIn(w.capCloseR, pt.x, pt.y) {
		return htCaption
	}
	return htClient
}

func ptrAt(u uintptr) unsafe.Pointer {
	var p unsafe.Pointer
	*(*uintptr)(unsafe.Pointer(&p)) = u
	return p
}

// ---------------------------------------------------------------------------
// Fonts (ClearType Microsoft YaHei UI for all Chinese/UI copy; Consolas only for hex IDs)
// ---------------------------------------------------------------------------

var (
	faceOnce sync.Once
	sansFace string
	monoFace string
)

func pickFaces() {
	sansFace = uiFontFace()
	monoFace = sansFace
}

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
	w.fontH1 = createUIFont(w.px(19), 700)
	w.fontH2 = createUIFont(w.px(15), 600)
	w.fontBody = createUIFont(w.px(13), 400)
	w.fontBold = createUIFont(w.px(13), 600)
	w.fontSm = createUIFont(w.px(12), 400)
	w.fontMono = createUIFont(w.px(12), 400)
}

func (w *mainWin) freeFonts() {
	for _, f := range []syscall.Handle{w.fontH1, w.fontH2, w.fontBody, w.fontBold, w.fontSm, w.fontMono} {
		if f != 0 {
			procDeleteObject.Call(uintptr(f))
		}
	}
	w.fontH1, w.fontH2, w.fontBody, w.fontBold, w.fontSm, w.fontMono = 0, 0, 0, 0, 0, 0
}

// ---------------------------------------------------------------------------
// State
// ---------------------------------------------------------------------------

func (w *mainWin) refreshLocalMeta() {
	if w.app != nil {
		w.hostName = w.app.deviceName()
	} else {
		w.hostName = defaultDeviceName()
	}
	w.fullID = ""
	if w.app != nil && w.app.keyPath != "" {
		if k, err := identity.LoadOrCreateKey(w.app.keyPath); err == nil {
			w.fullID = k.IDHex()
		}
	}
}

func (w *mainWin) loadPeers() {
	if w.app == nil {
		return
	}
	peers, err := client.Peers(w.app.pairedPath)
	if err != nil {
		w.status = "读取配对列表失败"
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
	// Keep timer off when idle in v2; redraws happen on user/network events.
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
// Layout — shared by painting and hit testing
// ---------------------------------------------------------------------------

func (w *mainWin) layout(cw, ch int32) {
	p := w.px
	topH := p(topBarH)
	w.titleBarR = rectT{0, 0, cw, topH}
	bw := p(44)
	w.capCloseR = rectT{cw - bw, 0, cw, topH}
	w.capMaxR = rectT{cw - bw*2, 0, cw - bw, topH}
	w.capMinR = rectT{cw - bw*3, 0, cw - bw*2, topH}

	rw := p(railW)
	w.railR = rectT{0, topH, rw, ch}

	navX := p(8)
	navW := rw - p(16)
	navH := p(50)
	w.navHomeR = rectT{navX, topH + p(12), navX + navW, topH + p(12) + navH}
	w.navDevicesR = rectT{navX, w.navHomeR.bottom + p(6), navX + navW, w.navHomeR.bottom + p(6) + navH}
	w.navSetR = rectT{navX, w.navDevicesR.bottom + p(6), navX + navW, w.navDevicesR.bottom + p(6) + navH}

	// Reset page-specific rects
	w.tileFilesR, w.tileFolderR, w.queueBoxR, w.queueClearR = rectT{}, rectT{}, rectT{}, rectT{}
	w.addDevLinkR, w.peerPickerR, w.sendBtnR, w.sendTextR, w.sendClipR, w.tempShareBtnR = rectT{}, rectT{}, rectT{}, rectT{}, rectT{}, rectT{}
	w.fltAllR, w.fltSendR, w.fltRecvR, w.openDirR, w.activeTxR = rectT{}, rectT{}, rectT{}, rectT{}, rectT{}
	w.ledgerRowsR = w.ledgerRowsR[:0]
	w.devAddBtnR = rectT{}
	w.devSendBtnsR = w.devSendBtnsR[:0]
	w.devRenameBtnsR = w.devRenameBtnsR[:0]
	w.devDeleteBtnsR = w.devDeleteBtnsR[:0]
	w.setRecvBtnR, w.setResetDirR, w.setChangeDirR, w.setOpenDirR, w.setAutoBtnR, w.setRenameBtnR, w.setCopyIDRowR = rectT{}, rectT{}, rectT{}, rectT{}, rectT{}, rectT{}, rectT{}

	switch w.page {
	case pageHome:
		colW := p(sendColW)
		if colW > (cw-rw)/2 {
			colW = (cw - rw) / 2
		}
		w.sendColR = rectT{rw, topH, rw + colW, ch}
		pad := p(22)
		cardW := colW - pad*2
		cardH := p(252)
		if len(w.queued) > 0 {
			cardH = p(292)
		}
		cy := topH + (ch-topH-cardH)/2
		if cy < topH+p(20) {
			cy = topH + p(20)
		}
		w.sendCardR = rectT{rw + pad, cy, rw + pad + cardW, cy + cardH}

		inPad := p(16)
		inL := w.sendCardR.left + inPad
		inR := w.sendCardR.right - inPad
		gap := p(10)
		tileW := (inR - inL - gap) / 2
		tileTop := w.sendCardR.top + inPad
		tileH := p(76)
		w.tileFilesR = rectT{inL, tileTop, inL + tileW, tileTop + tileH}
		w.tileFolderR = rectT{w.tileFilesR.right + gap, tileTop, inR, tileTop + tileH}

		y := w.tileFilesR.bottom + p(12)
		if len(w.queued) > 0 {
			w.queueBoxR = rectT{inL, y, inR, y + p(32)}
			w.queueClearR = rectT{inR - p(42), y, inR, y + p(32)}
			y = w.queueBoxR.bottom + p(12)
		}
		w.addDevLinkR = rectT{inR - p(64), y, inR, y + p(22)}
		y += p(24)
		w.peerPickerR = rectT{inL, y, inR, y + p(34)}
		y = w.peerPickerR.bottom + p(14)
		w.sendBtnR = rectT{inL, y, inR, y + p(38)}
		footY := w.sendCardR.bottom + p(10)
		w.sendTextR = rectT{w.sendCardR.left + p(2), footY, w.sendCardR.left + p(56), footY + p(22)}
		w.sendClipR = rectT{w.sendTextR.right + p(12), footY, w.sendTextR.right + p(78), footY + p(22)}
		w.tempShareBtnR = rectT{w.sendCardR.left, footY + p(32), w.sendCardR.right, footY + p(68)}

		// Right transfers pane
		rPad := p(28)
		paneL := w.sendColR.right + rPad
		paneR := cw - rPad
		tabY := topH + p(62)
		w.fltAllR = rectT{paneL, tabY, paneL + p(42), tabY + p(28)}
		w.fltSendR = rectT{w.fltAllR.right + p(16), tabY, w.fltAllR.right + p(68), tabY + p(28)}
		w.fltRecvR = rectT{w.fltSendR.right + p(16), tabY, w.fltSendR.right + p(68), tabY + p(28)}
		w.openDirR = rectT{paneR - p(104), tabY, paneR, tabY + p(28)}

		ry := w.fltAllR.bottom + p(8)
		if w.app != nil && w.app.currentTransfer() != nil {
			w.activeTxR = rectT{paneL, ry, paneR, ry + p(92)}
			ry = w.activeTxR.bottom + p(10)
		}
		rowH := p(54)
		for range w.filteredLedger() {
			if ry+rowH > ch-p(36) {
				break
			}
			w.ledgerRowsR = append(w.ledgerRowsR, rectT{paneL, ry, paneR, ry + rowH})
			ry += rowH
		}

	case pageDevices:
		pad := p(28)
		paneR := cw - pad
		w.devAddBtnR = rectT{paneR - p(96), topH + p(24), paneR, topH + p(58)}
		y := topH + p(94)
		rowH := p(62)
		btnGap := p(8)
		for range w.peers {
			if y+rowH > ch-p(36) {
				break
			}
			topB := y + p(15)
			botB := y + rowH - p(15)
			delR := rectT{paneR - p(70), topB, paneR - p(16), botB}
			renR := rectT{delR.left - btnGap - p(62), topB, delR.left - btnGap, botB}
			sendR := rectT{renR.left - btnGap - p(74), topB, renR.left - btnGap, botB}
			w.devDeleteBtnsR = append(w.devDeleteBtnsR, delR)
			w.devRenameBtnsR = append(w.devRenameBtnsR, renR)
			w.devSendBtnsR = append(w.devSendBtnsR, sendR)
			y += rowH
		}

	case pageSettings:
		pad := p(28)
		paneL := rw + pad
		paneR := cw - pad
		rowH := p(58)
		y := topH + p(86)
		w.setRecvBtnR = rectT{paneR - p(88), y + p(13), paneR - p(16), y + rowH - p(13)}
		y += rowH
		btnTop := y + p(13)
		btnBot := y + rowH - p(13)
		btnGap := p(8)
		w.setOpenDirR = rectT{paneR - p(72), btnTop, paneR - p(16), btnBot}
		w.setChangeDirR = rectT{w.setOpenDirR.left - btnGap - p(56), btnTop, w.setOpenDirR.left - btnGap, btnBot}
		if w.app != nil && w.app.isCustomReceiveDir() {
			w.setResetDirR = rectT{w.setChangeDirR.left - btnGap - p(68), btnTop, w.setChangeDirR.left - btnGap, btnBot}
		}
		y += rowH
		w.setAutoBtnR = rectT{paneR - p(88), y + p(13), paneR - p(16), y + rowH - p(13)}
		infoTop := y + rowH + p(50)
		infoRowH := p(42)
		w.setRenameBtnR = rectT{paneR - p(70), infoTop + p(8), paneR - p(16), infoTop + infoRowH - p(8)}
		infoY := infoTop + infoRowH
		w.setCopyIDRowR = rectT{paneL, infoY, paneR, infoY + infoRowH}
	}
}

// ---------------------------------------------------------------------------
// Painting (Acrylic Glass Rail + Opaque Crisp Surfaces)
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

	cv := newGlassCanvas(hdc, cr.right, cr.bottom)
	if cv == nil {
		fillRect(hdc, cr, w.pal.paper)
		return
	}
	w.drawScene(cv, cr)
	cv.finishBlit(hdc)
}

func (w *mainWin) surfaceAlpha(a byte) byte {
	if !w.acrylic {
		return 255
	}
	return a
}

func (w *mainWin) drawScene(cv *glassCanvas, cr rectT) {
	// 1. Unified seamless L-shaped Acrylic header + left navigation rail (identical transparency).
	railAlpha := byte(148)
	if w.dark {
		railAlpha = 162
	}
	barA := w.surfaceAlpha(railAlpha)
	cv.fillColorAlpha(w.titleBarR, w.pal.paper2, barA)
	cv.fillColorAlpha(w.railR, w.pal.paper2, barA)
	w.drawTitleBar(cv)
	w.drawRail(cv)

	// Hairline dividers separating workspace from top bar and left rail.
	divA := w.surfaceAlpha(175)
	cv.fillColorAlpha(rectT{w.railR.right, w.titleBarR.bottom - 1, cr.right, w.titleBarR.bottom}, w.pal.line, divA)
	cv.fillColorAlpha(rectT{w.railR.right - 1, w.titleBarR.bottom, w.railR.right, cr.bottom}, w.pal.line, divA)

	// 2. Right workspace (subtly translucent Acrylic, clearly brighter than top bar + rail).
	rightR := rectT{w.railR.right, w.titleBarR.bottom, cr.right, cr.bottom}
	workAlpha := byte(216)
	if w.dark {
		workAlpha = 222
	}
	cv.fillColorAlpha(rightR, w.pal.paper, w.surfaceAlpha(workAlpha))

	switch w.page {
	case pageHome:
		w.drawHome(cv, cr)
	case pageDevices:
		w.drawDevices(cv, cr)
	case pageSettings:
		w.drawSettings(cv, cr)
	}
}

func (w *mainWin) drawTitleBar(cv *glassCanvas) {
	p := w.px
	// Brand mark + title on left
	mSz := p(18)
	my := (w.titleBarR.bottom - mSz) / 2
	markR := rectT{p(14), my, p(14) + mSz, my + mSz}
	cv.drawAlphaRoundRect(markR, float64(p(5)), w.pal.accent, 255, 0, 0, 0)
	cv.drawAlphaGraphic(markR, col(255, 255, 255), func(dc uintptr, rw, rh int32) {
		pen, _, _ := procCreatePen.Call(psSolid, 1, uintptr(col(255, 255, 255)))
		oldPen, _, _ := procSelectObject.Call(dc, pen)
		defer func() {
			procSelectObject.Call(dc, oldPen)
			procDeleteObject.Call(pen)
		}()
		// Tiny paper-boat hull + water wave
		procMoveToEx.Call(dc, uintptr(rw*22/100), uintptr(rh*50/100), 0)
		procLineTo.Call(dc, uintptr(rw*78/100), uintptr(rh*38/100))
		procLineTo.Call(dc, uintptr(rw*66/100), uintptr(rh*64/100))
		procLineTo.Call(dc, uintptr(rw*30/100), uintptr(rh*64/100))
		procLineTo.Call(dc, uintptr(rw*22/100), uintptr(rh*50/100))
		procMoveToEx.Call(dc, uintptr(rw*24/100), uintptr(rh*76/100), 0)
		procLineTo.Call(dc, uintptr(rw*76/100), uintptr(rh*76/100))
	})
	cv.drawAlphaText(w.fontSm, "轻渡", rectT{markR.right + p(8), 0, w.capMinR.left, w.titleBarR.bottom},
		dtSingleLine|dtVCenter, w.pal.ink)

	// Minimize button
	if w.hover == hoverCapMin {
		cv.fillColorAlpha(w.capMinR, w.pal.paper3, w.surfaceAlpha(155))
	}
	cv.drawAlphaGraphic(w.capMinR, w.pal.ink, func(dc uintptr, rw, rh int32) {
		pen, _, _ := procCreatePen.Call(psSolid, 1, uintptr(col(255, 255, 255)))
		oldPen, _, _ := procSelectObject.Call(dc, pen)
		defer func() {
			procSelectObject.Call(dc, oldPen)
			procDeleteObject.Call(pen)
		}()
		sz := p(10)
		x0 := (rw - sz) / 2
		y0 := rh / 2
		procMoveToEx.Call(dc, uintptr(x0), uintptr(y0), 0)
		procLineTo.Call(dc, uintptr(x0+sz+1), uintptr(y0))
	})

	// Maximize / Restore button
	if w.hover == hoverCapMax {
		cv.fillColorAlpha(w.capMaxR, w.pal.paper3, w.surfaceAlpha(155))
	}
	zoomed, _, _ := procIsZoomed.Call(uintptr(w.hwnd))
	cv.drawAlphaGraphic(w.capMaxR, w.pal.ink, func(dc uintptr, rw, rh int32) {
		pen, _, _ := procCreatePen.Call(psSolid, 1, uintptr(col(255, 255, 255)))
		oldPen, _, _ := procSelectObject.Call(dc, pen)
		defer func() {
			procSelectObject.Call(dc, oldPen)
			procDeleteObject.Call(pen)
		}()
		box := func(x0, y0, x1, y1 int32) {
			procMoveToEx.Call(dc, uintptr(x0), uintptr(y0), 0)
			procLineTo.Call(dc, uintptr(x1), uintptr(y0))
			procLineTo.Call(dc, uintptr(x1), uintptr(y1))
			procLineTo.Call(dc, uintptr(x0), uintptr(y1))
			procLineTo.Call(dc, uintptr(x0), uintptr(y0))
		}
		if zoomed != 0 {
			sz := p(8)
			x0 := (rw-sz)/2 - p(1)
			y0 := (rh-sz)/2 + p(1)
			box(x0, y0, x0+sz, y0+sz)
			procMoveToEx.Call(dc, uintptr(x0+p(2)), uintptr(y0), 0)
			procLineTo.Call(dc, uintptr(x0+p(2)), uintptr(y0-p(2)))
			procLineTo.Call(dc, uintptr(x0+p(2)+sz), uintptr(y0-p(2)))
			procLineTo.Call(dc, uintptr(x0+p(2)+sz), uintptr(y0-p(2)+sz))
			procLineTo.Call(dc, uintptr(x0+sz), uintptr(y0-p(2)+sz))
		} else {
			sz := p(10)
			x0 := (rw - sz) / 2
			y0 := (rh - sz) / 2
			box(x0, y0, x0+sz, y0+sz)
		}
	})

	// Close button
	closeInk := w.pal.ink
	if w.hover == hoverCapClose {
		cv.fillColorAlpha(w.capCloseR, w.pal.danger, 235)
		closeInk = col(255, 255, 255)
	}
	drawCloseGlyph(cv, w.capCloseR, closeInk)
}

func (w *mainWin) drawRail(cv *glassCanvas) {
	items := []struct {
		r     rectT
		page  int
		hover int
		label string
		kind  int
	}{
		{w.navHomeR, pageHome, hoverNavHome, "传输", 0},
		{w.navDevicesR, pageDevices, hoverNavDevices, "设备", 1},
		{w.navSetR, pageSettings, hoverNavSettings, "设置", 2},
	}
	for _, it := range items {
		active := w.page == it.page
		fg := w.pal.ink2
		if active {
			fg = w.pal.accent
			fillA := byte(230)
			if !w.acrylic {
				fillA = 255
			}
			cv.drawAlphaRoundRect(it.r, float64(w.px(8)), w.pal.accentSoft, fillA, 0, 0, 0)
		} else if w.hover == it.hover {
			fg = w.pal.ink
			cv.drawAlphaRoundRect(it.r, float64(w.px(8)), w.pal.paper3, 180, 0, 0, 0)
		}
		iconR := rectT{it.r.left + w.px(16), it.r.top + w.px(7), it.r.right - w.px(16), it.r.top + w.px(25)}
		w.drawNavIcon(cv, iconR, it.kind, fg)
		lblR := rectT{it.r.left, it.r.top + w.px(26), it.r.right, it.r.bottom - w.px(4)}
		cv.drawAlphaText(w.fontSm, it.label, lblR, dtCenter|dtSingleLine|dtVCenter, fg)
	}
}

func (w *mainWin) drawNavIcon(cv *glassCanvas, r rectT, kind int, color uint32) {
	cv.drawAlphaGraphic(r, color, func(dc uintptr, rw, rh int32) {
		penW := int32(math.Max(1.4, float64(w.px(2))*0.8))
		pen, _, _ := procCreatePen.Call(psSolid, uintptr(penW), uintptr(col(255, 255, 255)))
		oldPen, _, _ := procSelectObject.Call(dc, pen)
		nullBr, _, _ := procStock.Call(5) // NULL_BRUSH
		oldBr, _, _ := procSelectObject.Call(dc, nullBr)
		defer func() {
			procSelectObject.Call(dc, oldBr)
			procSelectObject.Call(dc, oldPen)
			procDeleteObject.Call(pen)
		}()
		move := func(x, y int32) { procMoveToEx.Call(dc, uintptr(x), uintptr(y), 0) }
		line := func(x, y int32) { procLineTo.Call(dc, uintptr(x), uintptr(y)) }

		switch kind {
		case 0: // Send arrow
			move(rw*2/10, rh*8/10)
			line(rw*8/10, rh*2/10)
			move(rw*38/100, rh*2/10)
			line(rw*8/10, rh*2/10)
			line(rw*8/10, rh*62/100)
		case 1: // Devices
			procRoundRect.Call(dc, uintptr(rw*12/100), uintptr(rh*16/100), uintptr(rw*66/100), uintptr(rh*66/100), 3, 3)
			move(rw*28/100, rh*82/100)
			line(rw*50/100, rh*82/100)
			procRoundRect.Call(dc, uintptr(rw*54/100), uintptr(rh*38/100), uintptr(rw*88/100), uintptr(rh*88/100), 3, 3)
		case 2: // Settings circle + center dot
			procEllipse.Call(dc, uintptr(rw*18/100), uintptr(rh*18/100), uintptr(rw*82/100), uintptr(rh*82/100))
			procEllipse.Call(dc, uintptr(rw*40/100), uintptr(rh*40/100), uintptr(rw*60/100), uintptr(rh*60/100))
		}
	})
}

func (w *mainWin) drawHome(cv *glassCanvas, cr rectT) {
	p := w.px
	topH := w.titleBarR.bottom
	// Left send card gutter
	colAlpha := byte(198)
	if w.dark {
		colAlpha = 208
	}
	cv.fillColorAlpha(w.sendColR, w.pal.paper2, w.surfaceAlpha(colAlpha))
	cv.fillColorAlpha(rectT{w.sendColR.right - 1, topH, w.sendColR.right, cr.bottom}, w.pal.line, w.surfaceAlpha(175))

	// Compact Send Card
	cardA := w.surfaceAlpha(185)
	tileA := w.surfaceAlpha(205)
	cv.drawAlphaRoundRect(w.sendCardR, float64(p(12)), w.pal.paper, cardA, w.pal.line, w.surfaceAlpha(210), 1.0)

	// Dual tiles: 添加文件 / 添加文件夹
	fEdge := w.pal.line
	if w.hover == hoverTileFiles {
		fEdge = w.pal.accent
	}
	cv.drawAlphaRoundRect(w.tileFilesR, float64(p(8)), w.pal.paper, tileA, fEdge, w.surfaceAlpha(220), 1.0)
	plusR := rectT{(w.tileFilesR.left+w.tileFilesR.right)/2 - p(13), w.tileFilesR.top + p(12),
		(w.tileFilesR.left+w.tileFilesR.right)/2 + p(13), w.tileFilesR.top + p(38)}
	cv.drawAlphaRoundRect(plusR, float64(p(13)), w.pal.accent, 255, 0, 0, 0)
	cv.drawAlphaText(w.fontH2, "+", plusR, dtCenter|dtSingleLine|dtVCenter, w.pal.accentInk)
	cv.drawAlphaText(w.fontSm, "添加文件",
		rectT{w.tileFilesR.left, plusR.bottom + p(4), w.tileFilesR.right, w.tileFilesR.bottom - p(6)},
		dtCenter|dtSingleLine|dtVCenter, w.pal.ink)

	dEdge := w.pal.line
	if w.hover == hoverTileFolder {
		dEdge = w.pal.accent
	}
	cv.drawAlphaRoundRect(w.tileFolderR, float64(p(8)), w.pal.paper, tileA, dEdge, w.surfaceAlpha(220), 1.0)
	fldIconR := rectT{(w.tileFolderR.left+w.tileFolderR.right)/2 - p(13), w.tileFolderR.top + p(12),
		(w.tileFolderR.left+w.tileFolderR.right)/2 + p(13), w.tileFolderR.top + p(38)}
	cv.drawAlphaRoundRect(fldIconR, float64(p(13)), w.pal.paper, tileA, w.pal.line, w.surfaceAlpha(220), 1.0)
	w.drawFolderGlyph(cv, fldIconR, w.pal.ink2)
	cv.drawAlphaText(w.fontSm, "添加文件夹",
		rectT{w.tileFolderR.left, fldIconR.bottom + p(4), w.tileFolderR.right, w.tileFolderR.bottom - p(6)},
		dtCenter|dtSingleLine|dtVCenter, w.pal.ink)

	// Queued summary row
	if len(w.queued) > 0 {
		cv.drawAlphaRoundRect(w.queueBoxR, float64(p(6)), w.pal.paper2, tileA, w.pal.line, w.surfaceAlpha(210), 1.0)
		label := fmt.Sprintf("已选 %s", summarizeFiles(w.queued))
		cv.drawAlphaText(w.fontSm, label,
			rectT{w.queueBoxR.left + p(8), w.queueBoxR.top, w.queueClearR.left - p(4), w.queueBoxR.bottom},
			dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink)
		cv.drawAlphaText(w.fontSm, "清除", w.queueClearR, dtCenter|dtSingleLine|dtVCenter, w.pal.ink2)
	}

	// Recipient row
	lblTop := w.addDevLinkR.top
	cv.drawAlphaText(w.fontSm, "发给", rectT{w.tileFilesR.left, lblTop, w.addDevLinkR.left, w.addDevLinkR.bottom},
		dtSingleLine|dtVCenter, w.pal.ink2)
	cv.drawAlphaText(w.fontSm, "添加设备", w.addDevLinkR, dtRight|dtSingleLine|dtVCenter, w.pal.accent)

	pEdge := w.pal.line
	if w.hover == hoverPeerPicker {
		pEdge = w.pal.accent
	}
	cv.drawAlphaRoundRect(w.peerPickerR, float64(p(8)), w.pal.paper, tileA, pEdge, w.surfaceAlpha(220), 1.0)
	if peer, ok := w.activePeer(); ok {
		txt := peerLabel(peer)
		if len(w.peers) > 1 {
			txt += "  ▾"
		}
		cv.drawAlphaText(w.fontBold, txt,
			rectT{w.peerPickerR.left + p(10), w.peerPickerR.top, w.peerPickerR.right - p(10), w.peerPickerR.bottom},
			dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink)
	} else {
		cv.drawAlphaText(w.fontSm, "尚未配对设备，点此扫码添加",
			rectT{w.peerPickerR.left + p(10), w.peerPickerR.top, w.peerPickerR.right - p(10), w.peerPickerR.bottom},
			dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)
	}

	// Primary Send button
	btnFill := w.pal.accent
	if w.hover == hoverSendBtn {
		btnFill = shade(btnFill, w.dark, 0.12)
	}
	cv.drawAlphaRoundRect(w.sendBtnR, float64(p(8)), btnFill, 255, btnFill, 255, 0)
	btnLbl := "发送"
	if len(w.queued) == 0 && w.hasPeers() {
		btnLbl = "选择文件并发送"
	} else if !w.hasPeers() {
		btnLbl = "扫码配对设备"
	}
	cv.drawAlphaText(w.fontBold, btnLbl, w.sendBtnR, dtCenter|dtSingleLine|dtVCenter, w.pal.accentInk)

	// Footer links under Send Card
	textColor := w.pal.ink2
	if w.hover == hoverSendText {
		textColor = w.pal.accent
	}
	cv.drawAlphaText(w.fontSm, "发送文字", w.sendTextR, dtSingleLine|dtVCenter, textColor)
	cv.drawAlphaText(w.fontSm, "·",
		rectT{w.sendTextR.right, w.sendTextR.top, w.sendClipR.left, w.sendTextR.bottom},
		dtCenter|dtSingleLine|dtVCenter, w.pal.ink3)

	clipColor := w.pal.ink2
	if w.hover == hoverSendClip {
		clipColor = w.pal.accent
	}
	cv.drawAlphaText(w.fontSm, "发送剪贴板", w.sendClipR, dtSingleLine|dtVCenter, clipColor)
	cv.drawAlphaText(w.fontSm, "支持直接拖入文件",
		rectT{w.sendClipR.right + p(4), w.sendClipR.top, w.sendCardR.right - p(2), w.sendClipR.bottom},
		dtRight|dtSingleLine|dtVCenter, w.pal.ink3)

	tsFill := w.pal.paper
	tsEdge := w.pal.line
	tsFg := w.pal.ink
	if w.hover == hoverTempShare {
		tsFill = w.pal.accentSoft
		tsEdge = w.pal.accent
		tsFg = w.pal.accent
	}
	cv.drawAlphaRoundRect(w.tempShareBtnR, float64(p(9)), tsFill, tileA, tsEdge, w.surfaceAlpha(220), 1.0)
	cv.drawAlphaText(w.fontSm, "临时快传（生成二维码 / 网页直传）", w.tempShareBtnR, dtCenter|dtSingleLine|dtVCenter, tsFg)

	// Right Transfers Pane
	paneL := w.sendColR.right + p(28)
	paneR := cr.right - p(28)
	sigLbl, sigKind := "信令服务器：未连接", "offline"
	if w.app != nil {
		sigLbl, sigKind = w.app.signalHomeStatus()
	}
	pillBg, pillFg, dotCol := w.pal.paper3, w.pal.ink2, w.pal.ink3
	switch sigKind {
	case "online":
		pillBg, pillFg, dotCol = w.pal.accentSoft, w.pal.accent, w.pal.accent
	case "connecting":
		if w.dark {
			pillBg = col(0x45, 0x1A, 0x03)
		} else {
			pillBg = col(0xFE, 0xF3, 0xC7)
		}
		pillFg, dotCol = w.pal.amber, w.pal.amber
	}
	pillW := textWidth(cv.memDC, w.fontSm, sigLbl) + p(36)
	pillH := p(24)
	pillY := topH + p(25)
	pillR := rectT{paneR - pillW, pillY, paneR, pillY + pillH}
	cv.drawAlphaText(w.fontH1, "传输记录", rectT{paneL, topH + p(20), pillR.left - p(12), topH + p(54)}, dtSingleLine|dtVCenter, w.pal.ink)
	cv.drawAlphaRoundRect(pillR, float64(p(12)), pillBg, w.surfaceAlpha(235), 0, 0, 0)
	dotSz := p(6)
	dotX := pillR.left + p(9)
	dotY := pillY + (pillH-dotSz)/2
	cv.drawAlphaRoundRect(rectT{dotX, dotY, dotX + dotSz, dotY + dotSz}, float64(dotSz)/2.0, dotCol, 255, 0, 0, 0)
	cv.drawAlphaText(w.fontSm, sigLbl, rectT{dotX + dotSz + p(6), pillY, pillR.right - p(8), pillR.bottom}, dtSingleLine|dtVCenter, pillFg)

	// Underlined filter tabs
	tabItems := []struct {
		r     rectT
		flt   int
		label string
	}{
		{w.fltAllR, fltAll, "全部"},
		{w.fltSendR, fltSend, "已发出"},
		{w.fltRecvR, fltRecv, "已收到"},
	}
	cv.fillColorAlpha(rectT{paneL, w.fltAllR.bottom, paneR, w.fltAllR.bottom + 1}, w.pal.line, 255)
	for _, tb := range tabItems {
		c := w.pal.ink2
		if w.filter == tb.flt {
			c = w.pal.ink
			cv.fillColorAlpha(rectT{tb.r.left, tb.r.bottom - p(2), tb.r.right, tb.r.bottom}, w.pal.accent, 255)
		}
		cv.drawAlphaText(w.fontBold, tb.label, tb.r, dtSingleLine|dtVCenter, c)
	}
	openClr := w.pal.ink2
	if w.hover == hoverOpenDir {
		openClr = w.pal.accent
	}
	cv.drawAlphaText(w.fontSm, "打开接收文件夹", w.openDirR, dtRight|dtSingleLine|dtVCenter, openClr)

	if tx := w.app.currentTransfer(); tx != nil && w.activeTxR.bottom > w.activeTxR.top {
		w.drawActiveTransferCard(cv, w.activeTxR, tx)
	}

	rows := w.filteredLedger()
	if len(rows) == 0 && w.app.currentTransfer() == nil {
		midY := (w.fltAllR.bottom + cr.bottom) / 2
		cv.drawAlphaText(w.fontBold, "还没有传输记录", rectT{paneL, midY - p(24), paneR, midY},
			dtCenter|dtSingleLine|dtVCenter, w.pal.ink)
		cv.drawAlphaText(w.fontSm, "收发完成后，文件会出现在这里。", rectT{paneL, midY + p(2), paneR, midY + p(26)},
			dtCenter|dtSingleLine|dtVCenter, w.pal.ink2)
	} else {
		for i, rr := range w.ledgerRowsR {
			if i >= len(rows) {
				break
			}
			r := rows[i]
			hovered := w.hover == hoverLedgerRow && w.hoverIdx == i
			if hovered {
				cv.fillColorAlpha(rr, w.pal.paper2, w.surfaceAlpha(190))
			}
			title := r.files
			if title == "" {
				title = r.peer
			}
			dirWord := "来自 "
			if r.dir == "发" {
				dirWord = "发给 "
			}
			subParts := []string{dirWord + dashIfEmpty(r.peer)}
			if r.size != "" {
				subParts = append(subParts, r.size)
			}
			if r.dur != "" {
				subParts = append(subParts, r.dur)
			}
			if r.time != "" {
				subParts = append(subParts, r.time)
			}
			sub := strings.Join(subParts, " · ")

			rightLimit := rr.right - p(4)
			if hovered {
				openW := p(66)
				cv.drawAlphaText(w.fontSm, "打开位置",
					rectT{rr.right - openW, rr.top, rr.right - p(4), rr.bottom},
					dtRight|dtSingleLine|dtVCenter, w.pal.accent)
				rightLimit = rr.right - openW - p(6)
			}
			if chLabel, chBg, chFg := channelDisplayLabel(r.path, w.pal, w.dark); chLabel != "" {
				badgeW := textWidth(cv.memDC, w.fontSm, chLabel) + p(16)
				if badgeW < p(54) {
					badgeW = p(54)
				}
				badgeR := rectT{rightLimit - badgeW, rr.top + p(15), rightLimit, rr.bottom - p(15)}
				cv.drawAlphaRoundRect(badgeR, float64(p(11)), chBg, w.surfaceAlpha(235), 0, 0, 0)
				cv.drawAlphaText(w.fontSm, chLabel, badgeR, dtCenter|dtSingleLine|dtVCenter, chFg)
				rightLimit = badgeR.left - p(10)
			}

			cv.drawAlphaText(w.fontBold, title,
				rectT{rr.left + p(4), rr.top + p(6), rightLimit, rr.top + p(28)},
				dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink)
			cv.drawAlphaText(w.fontSm, sub,
				rectT{rr.left + p(4), rr.top + p(28), rightLimit, rr.bottom - p(6)},
				dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)
			cv.fillColorAlpha(rectT{rr.left, rr.bottom - 1, rr.right, rr.bottom}, w.pal.lineSoft, w.surfaceAlpha(175))
		}
	}

	if w.status != "" {
		cv.drawAlphaText(w.fontSm, w.status,
			rectT{paneL, cr.bottom - p(32), paneR, cr.bottom - p(8)},
			dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)
	}
}

func (w *mainWin) drawActiveTransferCard(cv *glassCanvas, r rectT, tx *activeTransfer) {
	p := w.px
	cv.drawAlphaRoundRect(r, float64(p(12)), w.pal.paper, w.surfaceAlpha(215), w.pal.accent, w.surfaceAlpha(160), 1.0)

	inL := r.left + p(14)
	inR := r.right - p(14)

	dirVerb := "正在发往 "
	if tx.dir == "recv" {
		dirVerb = "正在接收来自 "
	}
	stageTxt := stageDisplayLabel(tx.stage)
	headTxt := dirVerb + dashIfEmpty(tx.peer) + " · " + stageTxt

	chLabel, chBg, chFg := channelDisplayLabel(tx.channel, w.pal, w.dark)
	if chLabel == "" {
		chLabel = "协商通道"
		chBg = w.pal.paper3
		chFg = w.pal.ink2
	}
	badgeW := textWidth(cv.memDC, w.fontSm, chLabel) + p(16)
	if badgeW < p(56) {
		badgeW = p(56)
	}
	badgeR := rectT{inR - badgeW, r.top + p(11), inR, r.top + p(33)}
	cv.drawAlphaRoundRect(badgeR, float64(p(11)), chBg, w.surfaceAlpha(240), 0, 0, 0)
	cv.drawAlphaText(w.fontSm, chLabel, badgeR, dtCenter|dtSingleLine|dtVCenter, chFg)

	pctTxt := ""
	pctRatio := 0.0
	if tx.bytesTotal > 0 {
		if tx.bytesDone >= tx.bytesTotal {
			pctRatio = 1.0
		} else if tx.bytesDone > 0 {
			pctRatio = float64(tx.bytesDone) / float64(tx.bytesTotal)
		}
		pctTxt = fmt.Sprintf("%d%%", int(pctRatio*100+0.5))
	}
	pctW := p(42)
	if pctTxt != "" {
		cv.drawAlphaText(w.fontBold, pctTxt,
			rectT{badgeR.left - p(6) - pctW, r.top + p(10), badgeR.left - p(6), r.top + p(34)},
			dtRight|dtSingleLine|dtVCenter, w.pal.accent)
	} else {
		pctW = 0
	}
	cv.drawAlphaText(w.fontBold, headTxt,
		rectT{inL, r.top + p(10), badgeR.left - p(12) - pctW, r.top + p(34)},
		dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink)

	// Progress track + fill
	barR := rectT{inL, r.top + p(42), inR, r.top + p(48)}
	cv.drawAlphaRoundRect(barR, float64(p(3)), w.pal.paper3, w.surfaceAlpha(235), 0, 0, 0)
	fillW := int32(float64(barR.right-barR.left) * pctRatio)
	if tx.bytesTotal == 0 && (tx.stage == "connecting" || tx.stage == "waiting" || tx.stage == "transferring") {
		fillW = (barR.right - barR.left) * 28 / 100
	}
	if fillW > 0 {
		if fillW < p(6) {
			fillW = p(6)
		}
		if barR.left+fillW > barR.right {
			fillW = barR.right - barR.left
		}
		cv.drawAlphaRoundRect(rectT{barR.left, barR.top, barR.left + fillW, barR.bottom}, float64(p(3)), w.pal.accent, 255, 0, 0, 0)
	}

	// Bottom file + bytes + elapsed/ETA metadata
	fileInfo := tx.fileName
	if tx.fileCount > 1 && tx.fileIndex > 0 {
		fileInfo = fmt.Sprintf("第 %d/%d 个 · %s", tx.fileIndex, tx.fileCount, tx.fileName)
	}
	if fileInfo == "" {
		fileInfo = "准备文件信息…"
	}
	rightInfo := formatTransferMetrics(tx.startedAt, tx.bytesDone, tx.bytesTotal)
	rw := textWidth(cv.memDC, w.fontSm, rightInfo) + p(8)
	if rw > (inR-inL)/2 {
		rw = (inR - inL) / 2
	}
	cv.drawAlphaText(w.fontSm, fileInfo, rectT{inL, r.top + p(58), inR - rw - p(8), r.bottom - p(10)},
		dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)
	cv.drawAlphaText(w.fontSm, rightInfo, rectT{inR - rw, r.top + p(58), inR, r.bottom - p(10)},
		dtRight|dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)
}

func stageDisplayLabel(stage string) string {
	switch stage {
	case "connecting":
		return "连接中"
	case "waiting":
		return "等待接收方"
	case "racing":
		return "协商直连路径"
	case "transferring":
		return "传输中"
	case "verifying":
		return "校验写入中"
	default:
		return "传输中"
	}
}

func channelDisplayLabel(ch string, pal palette, dark bool) (string, uint32, uint32) {
	switch strings.ToLower(strings.TrimSpace(ch)) {
	case "lan", "lan 直连":
		return "LAN 直连", pal.accentSoft, pal.accent
	case "打洞", "punch", "udp 打洞":
		if dark {
			return "UDP 打洞", col(0x45, 0x1A, 0x03), pal.amber
		}
		return "UDP 打洞", col(0xFE, 0xF3, 0xC7), pal.amber
	case "中继", "relay", "服务器中继":
		return "服务器中继", pal.paper3, pal.ink2
	case "失败", "failed", "error":
		if dark {
			return "失败", col(0x7F, 0x1D, 0x1D), pal.danger
		}
		return "失败", col(0xFE, 0xE2, 0xE2), pal.danger
	default:
		return "", 0, 0
	}
}

func formatTransferMetrics(startedAt time.Time, done, total int64) string {
	var parts []string
	if total > 0 {
		parts = append(parts, humanBytes(done)+" / "+humanBytes(total))
	} else if done > 0 {
		parts = append(parts, humanBytes(done))
	}
	if !startedAt.IsZero() {
		elapsed := time.Since(startedAt)
		if elapsed < time.Second {
			elapsed = time.Second
		}
		parts = append(parts, fmt.Sprintf("已用 %ds", int(elapsed.Seconds())))
		if eta := formatETA(elapsed, done, total); eta != "" {
			parts = append(parts, eta)
		}
	}
	return strings.Join(parts, " · ")
}

func formatETA(elapsed time.Duration, done, total int64) string {
	if total <= 0 || done <= 0 || done >= total || elapsed < time.Second {
		return ""
	}
	sec := elapsed.Seconds()
	rate := float64(done) / sec
	if rate <= 0 {
		return ""
	}
	remSec := int(float64(total-done)/rate + 0.5)
	if remSec < 1 {
		remSec = 1
	}
	if remSec >= 60 {
		return fmt.Sprintf("剩余约 %dm%ds", remSec/60, remSec%60)
	}
	return fmt.Sprintf("剩余约 %ds", remSec)
}

func (w *mainWin) drawFolderGlyph(cv *glassCanvas, r rectT, color uint32) {
	cv.drawAlphaGraphic(r, color, func(dc uintptr, rw, rh int32) {
		pen, _, _ := procCreatePen.Call(psSolid, 1, uintptr(col(255, 255, 255)))
		oldPen, _, _ := procSelectObject.Call(dc, pen)
		nullBr, _, _ := procStock.Call(5)
		oldBr, _, _ := procSelectObject.Call(dc, nullBr)
		defer func() {
			procSelectObject.Call(dc, oldBr)
			procSelectObject.Call(dc, oldPen)
			procDeleteObject.Call(pen)
		}()
		procMoveToEx.Call(dc, uintptr(rw*22/100), uintptr(rh*32/100), 0)
		procLineTo.Call(dc, uintptr(rw*44/100), uintptr(rh*32/100))
		procLineTo.Call(dc, uintptr(rw*52/100), uintptr(rh*40/100))
		procLineTo.Call(dc, uintptr(rw*78/100), uintptr(rh*40/100))
		procLineTo.Call(dc, uintptr(rw*78/100), uintptr(rh*72/100))
		procLineTo.Call(dc, uintptr(rw*22/100), uintptr(rh*72/100))
		procLineTo.Call(dc, uintptr(rw*22/100), uintptr(rh*32/100))
	})
}

func (w *mainWin) drawDevices(cv *glassCanvas, cr rectT) {
	p := w.px
	topH := w.titleBarR.bottom
	pad := p(28)
	paneL := w.railR.right + pad
	paneR := cr.right - pad

	cv.drawAlphaText(w.fontH1, "设备", rectT{paneL, topH + p(20), paneR - p(110), topH + p(52)}, dtSingleLine|dtVCenter, w.pal.ink)
	cv.drawAlphaText(w.fontSm, "已配对的设备可直接互相收发文件。", rectT{paneL, topH + p(54), paneR - p(110), topH + p(76)},
		dtSingleLine|dtVCenter, w.pal.ink2)

	addFill := w.pal.accent
	if w.hover == hoverDevAddBtn {
		addFill = shade(addFill, w.dark, 0.12)
	}
	cv.drawAlphaRoundRect(w.devAddBtnR, float64(p(8)), addFill, 255, addFill, 255, 0)
	cv.drawAlphaText(w.fontBold, "添加设备", w.devAddBtnR, dtCenter|dtSingleLine|dtVCenter, w.pal.accentInk)

	if len(w.peers) == 0 {
		box := rectT{paneL, topH + p(94), paneR, topH + p(224)}
		cv.drawAlphaRoundRect(box, float64(p(12)), w.pal.paper2, w.surfaceAlpha(190), w.pal.line, w.surfaceAlpha(200), 1.0)
		cv.drawAlphaText(w.fontBold, "还没有已配对设备", rectT{paneL, topH + p(130), paneR, topH + p(158)},
			dtCenter|dtSingleLine|dtVCenter, w.pal.ink)
		cv.drawAlphaText(w.fontSm, "点击右上角「添加设备」，用手机轻渡扫码或输入 6 位配对码即可绑定。",
			rectT{paneL + p(20), topH + p(160), paneR - p(20), topH + p(194)}, dtCenter|dtSingleLine|dtVCenter, w.pal.ink2)
		return
	}

	rowH := p(62)
	top := topH + p(94)
	box := rectT{paneL, top, paneR, top + int32(len(w.devSendBtnsR))*rowH}
	cv.drawAlphaRoundRect(box, float64(p(12)), w.pal.paper, w.surfaceAlpha(195), w.pal.line, w.surfaceAlpha(200), 1.0)
	for i, br := range w.devSendBtnsR {
		ry := top + int32(i)*rowH
		peer := w.peers[i]
		nameX := paneL + p(16)
		nameMaxR := br.left - p(12)
		peerName := peerLabel(peer)

		reach := "offline"
		if w.app != nil {
			reach = w.app.peerReachability(peer.ID)
		}
		stLabel, stBg, stFg := "离线", w.pal.paper3, w.pal.ink2
		switch reach {
		case "lan":
			stLabel, stBg, stFg = "局域网", w.pal.accentSoft, w.pal.accent
		case "cloud":
			stLabel, stBg, stFg = "云端在线", w.pal.accentSoft, w.pal.accent
		}
		stBadgeW := textWidth(cv.memDC, w.fontSm, stLabel) + p(14)
		nameW := textWidth(cv.memDC, w.fontBold, peerName)
		nameLimitR := nameMaxR - stBadgeW - p(8)
		if nameLimitR < nameX+p(40) {
			nameLimitR = nameX + p(40)
		}
		cv.drawAlphaText(w.fontBold, peerName,
			rectT{nameX, ry + p(9), nameLimitR, ry + p(31)},
			dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink)

		badgeL := nameX + nameW + p(8)
		if badgeL+stBadgeW > nameMaxR {
			badgeL = nameMaxR - stBadgeW
		}
		if badgeL > nameX+p(24) {
			badgeR := rectT{badgeL, ry + p(10), badgeL + stBadgeW, ry + p(30)}
			cv.drawAlphaRoundRect(badgeR, float64(p(10)), stBg, w.surfaceAlpha(240), 0, 0, 0)
			cv.drawAlphaText(w.fontSm, stLabel, badgeR, dtCenter|dtSingleLine|dtVCenter, stFg)
		}

		cv.drawAlphaText(w.fontSm, "设备 ID "+truncID(peer.ID),
			rectT{paneL + p(16), ry + p(31), br.left - p(12), ry + p(52)},
			dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)

		bFill := w.pal.paper2
		if w.hover == hoverDevSendBtn && w.hoverIdx == i {
			bFill = w.pal.paper3
		}
		cv.drawAlphaRoundRect(br, float64(p(6)), bFill, w.surfaceAlpha(210), w.pal.line, w.surfaceAlpha(200), 1.0)
		cv.drawAlphaText(w.fontSm, "发送文件", br, dtCenter|dtSingleLine|dtVCenter, w.pal.ink)

		if i < len(w.devRenameBtnsR) {
			rr := w.devRenameBtnsR[i]
			rFill := w.pal.paper2
			if w.hover == hoverDevRenameBtn && w.hoverIdx == i {
				rFill = w.pal.paper3
			}
			cv.drawAlphaRoundRect(rr, float64(p(6)), rFill, w.surfaceAlpha(210), w.pal.line, w.surfaceAlpha(200), 1.0)
			cv.drawAlphaText(w.fontSm, "重命名", rr, dtCenter|dtSingleLine|dtVCenter, w.pal.ink)
		}
		if i < len(w.devDeleteBtnsR) {
			dr := w.devDeleteBtnsR[i]
			dFill := w.pal.paper2
			dEdge := w.pal.line
			dFg := w.pal.danger
			if w.hover == hoverDevDeleteBtn && w.hoverIdx == i {
				dFill = w.pal.danger
				dEdge = w.pal.danger
				dFg = w.pal.accentInk
			}
			cv.drawAlphaRoundRect(dr, float64(p(6)), dFill, w.surfaceAlpha(220), dEdge, w.surfaceAlpha(220), 1.0)
			cv.drawAlphaText(w.fontSm, "删除", dr, dtCenter|dtSingleLine|dtVCenter, dFg)
		}
		if i+1 < len(w.devSendBtnsR) {
			cv.fillColorAlpha(rectT{paneL + p(16), ry + rowH - 1, paneR - p(16), ry + rowH}, w.pal.lineSoft, w.surfaceAlpha(175))
		}
	}
	if w.status != "" {
		cv.drawAlphaText(w.fontSm, w.status,
			rectT{paneL, cr.bottom - p(32), paneR, cr.bottom - p(8)},
			dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)
	}
}

func (w *mainWin) drawSettings(cv *glassCanvas, cr rectT) {
	p := w.px
	topH := w.titleBarR.bottom
	pad := p(28)
	paneL := w.railR.right + pad
	paneR := cr.right - pad

	cv.drawAlphaText(w.fontH1, "设置", rectT{paneL, topH + p(20), paneR, topH + p(52)}, dtSingleLine|dtVCenter, w.pal.ink)
	cv.drawAlphaText(w.fontSm, "常规接收选项与本机技术信息。", rectT{paneL, topH + p(54), paneR, topH + p(76)},
		dtSingleLine|dtVCenter, w.pal.ink2)

	rowH := p(58)
	top := topH + p(86)
	g1 := rectT{paneL, top, paneR, top + rowH*3}
	cv.drawAlphaRoundRect(g1, float64(p(12)), w.pal.paper, w.surfaceAlpha(195), w.pal.line, w.surfaceAlpha(200), 1.0)

	recvTxt := "已暂停"
	if recvEnabled.Load() {
		recvTxt = "已开启"
	}
	autoTxt := "未开启"
	if autostartEnabled() {
		autoTxt = "已开启"
	}
	dirText := defaultReceiveDir()
	portText := "18800"
	if w.app != nil {
		dirText = w.app.receiveDir()
		portText = fmt.Sprintf("%d", w.app.port)
	}

	drawSmallBtn := func(r rectT, label string, hoverID int) {
		bFill := w.pal.paper2
		if w.hover == hoverID {
			bFill = w.pal.paper3
		}
		cv.drawAlphaRoundRect(r, float64(p(6)), bFill, w.surfaceAlpha(210), w.pal.line, w.surfaceAlpha(200), 1.0)
		cv.drawAlphaText(w.fontSm, label, r, dtCenter|dtSingleLine|dtVCenter, w.pal.ink)
	}

	type g1Action struct {
		label   string
		r       rectT
		hoverID int
	}
	dirActions := make([]g1Action, 0, 3)
	if w.setResetDirR.right > w.setResetDirR.left {
		dirActions = append(dirActions, g1Action{"恢复默认", w.setResetDirR, hoverSetResetDir})
	}
	dirActions = append(dirActions,
		g1Action{"更改", w.setChangeDirR, hoverSetChangeDir},
		g1Action{"打开", w.setOpenDirR, hoverSetOpenDir},
	)

	g1Rows := []struct {
		title, sub string
		actions    []g1Action
	}{
		{"后台接收文件", "关闭后其他设备暂时无法向此电脑发送", []g1Action{{recvTxt, w.setRecvBtnR, hoverSetRecv}}},
		{"接收文件保存位置", dirText, dirActions},
		{"开机自动启动", "登录后自动缩至右下角托盘接收", []g1Action{{autoTxt, w.setAutoBtnR, hoverSetAuto}}},
	}
	for i, r := range g1Rows {
		ry := top + int32(i)*rowH
		textRight := paneR - p(16)
		if len(r.actions) > 0 {
			textRight = r.actions[0].r.left - p(12)
		}
		cv.drawAlphaText(w.fontBold, r.title,
			rectT{paneL + p(16), ry + p(8), textRight, ry + p(30)},
			dtSingleLine|dtVCenter, w.pal.ink)
		cv.drawAlphaText(w.fontSm, r.sub,
			rectT{paneL + p(16), ry + p(30), textRight, ry + p(50)},
			dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)

		for _, act := range r.actions {
			drawSmallBtn(act.r, act.label, act.hoverID)
		}
		if i+1 < len(g1Rows) {
			cv.fillColorAlpha(rectT{paneL + p(16), ry + rowH - 1, paneR - p(16), ry + rowH}, w.pal.lineSoft, w.surfaceAlpha(175))
		}
	}

	// Quiet technical info section
	lblY := g1.bottom + p(24)
	cv.drawAlphaText(w.fontBold, "本机信息", rectT{paneL + p(4), lblY, paneR, lblY + p(22)},
		dtSingleLine|dtVCenter, w.pal.ink2)

	infoRowH := p(42)
	infoTop := lblY + p(26)
	g2 := rectT{paneL, infoTop, paneR, infoTop + infoRowH*3}
	cv.drawAlphaRoundRect(g2, float64(p(12)), w.pal.paper, w.surfaceAlpha(195), w.pal.line, w.surfaceAlpha(200), 1.0)

	idShow := truncID(w.fullID) + "（点击复制）"
	if w.fullID == "" {
		idShow = "未生成"
	}
	connShow := "局域网端口 " + portText
	if w.app != nil {
		connShow = "信令：" + w.app.signalStatusText() + " · 端口 " + portText
	}
	g2Rows := []struct {
		k, v string
		font syscall.Handle
	}{
		{"设备名称", w.hostName, w.fontSm},
		{"设备 ID", idShow, w.fontMono},
		{"连接状态", connShow, w.fontSm},
	}
	for i, r := range g2Rows {
		ry := infoTop + int32(i)*infoRowH
		if i == 1 && w.hover == hoverSetCopyID {
			cv.fillColorAlpha(rectT{paneL + 1, ry + 1, paneR - 1, ry + infoRowH - 1}, w.pal.paper2, w.surfaceAlpha(190))
		}
		cv.drawAlphaText(w.fontSm, r.k, rectT{paneL + p(16), ry, paneL + p(140), ry + infoRowH},
			dtSingleLine|dtVCenter, w.pal.ink2)
		valRight := paneR - p(16)
		if i == 0 {
			valRight = w.setRenameBtnR.left - p(10)
			drawSmallBtn(w.setRenameBtnR, "修改", hoverSetRename)
		}
		cv.drawAlphaText(r.font, r.v, rectT{paneL + p(140), ry, valRight, ry + infoRowH},
			dtRight|dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink)
		if i+1 < len(g2Rows) {
			cv.fillColorAlpha(rectT{paneL + p(16), ry + infoRowH - 1, paneR - p(16), ry + infoRowH}, w.pal.lineSoft, 255)
		}
	}

	if w.status != "" {
		cv.drawAlphaText(w.fontSm, w.status,
			rectT{paneL, g2.bottom + p(12), paneR, cr.bottom - p(12)},
			dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)
	}
}

// ---------------------------------------------------------------------------
// Ledger parsing & filtering (compatible with cmd/ft-desktop/mainwin_test.go)
// ---------------------------------------------------------------------------

type ledgerRow struct {
	time, dir, peer, files, size, dur, path string
}

func (w *mainWin) filteredLedger() []ledgerRow {
	if w.app == nil {
		return nil
	}
	lines := w.app.recentLines(12)
	out := make([]ledgerRow, 0, len(lines))
	for _, l := range lines {
		r := parseLedgerLine(l)
		if w.filter == fltSend && r.dir != "发" {
			continue
		}
		if w.filter == fltRecv && r.dir != "收" {
			continue
		}
		out = append(out, r)
	}
	return out
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
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
	case "lan", "lan 直连", "打洞", "punch", "udp 打洞", "中继", "relay", "服务器中继", "失败", "failed", "error", "未配对":
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Interaction
// ---------------------------------------------------------------------------

func (w *mainWin) cursorIsHand() bool {
	switch w.hover {
	case hoverNone, hoverCapMin, hoverCapMax, hoverCapClose:
		return false
	}
	return true
}

func (w *mainWin) onMouseMove(hwnd syscall.Handle, x, y int32) {
	var cr rectT
	procGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&cr)))
	w.layout(cr.right, cr.bottom)

	kind, idx := hoverNone, 0
	switch {
	case ptIn(w.capMinR, x, y):
		kind = hoverCapMin
	case ptIn(w.capMaxR, x, y):
		kind = hoverCapMax
	case ptIn(w.capCloseR, x, y):
		kind = hoverCapClose
	case ptIn(w.navHomeR, x, y):
		kind = hoverNavHome
	case ptIn(w.navDevicesR, x, y):
		kind = hoverNavDevices
	case ptIn(w.navSetR, x, y):
		kind = hoverNavSettings
	case w.page == pageHome && ptIn(w.tileFilesR, x, y):
		kind = hoverTileFiles
	case w.page == pageHome && ptIn(w.tileFolderR, x, y):
		kind = hoverTileFolder
	case w.page == pageHome && len(w.queued) > 0 && ptIn(w.queueClearR, x, y):
		kind = hoverQueueClear
	case w.page == pageHome && ptIn(w.addDevLinkR, x, y):
		kind = hoverAddDevice
	case w.page == pageHome && ptIn(w.peerPickerR, x, y):
		kind = hoverPeerPicker
	case w.page == pageHome && ptIn(w.sendBtnR, x, y):
		kind = hoverSendBtn
	case w.page == pageHome && ptIn(w.sendTextR, x, y):
		kind = hoverSendText
	case w.page == pageHome && ptIn(w.sendClipR, x, y):
		kind = hoverSendClip
	case w.page == pageHome && ptIn(w.tempShareBtnR, x, y):
		kind = hoverTempShare
	case w.page == pageHome && ptIn(w.fltAllR, x, y):
		kind = hoverFltAll
	case w.page == pageHome && ptIn(w.fltSendR, x, y):
		kind = hoverFltSend
	case w.page == pageHome && ptIn(w.fltRecvR, x, y):
		kind = hoverFltRecv
	case w.page == pageHome && ptIn(w.openDirR, x, y):
		kind = hoverOpenDir
	case w.page == pageDevices && ptIn(w.devAddBtnR, x, y):
		kind = hoverDevAddBtn
	case w.page == pageSettings && ptIn(w.setRecvBtnR, x, y):
		kind = hoverSetRecv
	case w.page == pageSettings && w.setResetDirR.right > w.setResetDirR.left && ptIn(w.setResetDirR, x, y):
		kind = hoverSetResetDir
	case w.page == pageSettings && ptIn(w.setChangeDirR, x, y):
		kind = hoverSetChangeDir
	case w.page == pageSettings && ptIn(w.setOpenDirR, x, y):
		kind = hoverSetOpenDir
	case w.page == pageSettings && ptIn(w.setAutoBtnR, x, y):
		kind = hoverSetAuto
	case w.page == pageSettings && ptIn(w.setRenameBtnR, x, y):
		kind = hoverSetRename
	case w.page == pageSettings && ptIn(w.setCopyIDRowR, x, y):
		kind = hoverSetCopyID
	default:
		if w.page == pageHome {
			for i, rr := range w.ledgerRowsR {
				if ptIn(rr, x, y) {
					kind, idx = hoverLedgerRow, i
					break
				}
			}
		} else if w.page == pageDevices {
			for i, br := range w.devSendBtnsR {
				if ptIn(br, x, y) {
					kind, idx = hoverDevSendBtn, i
					break
				}
			}
			if kind == hoverNone {
				for i, rr := range w.devRenameBtnsR {
					if ptIn(rr, x, y) {
						kind, idx = hoverDevRenameBtn, i
						break
					}
				}
			}
			if kind == hoverNone {
				for i, dr := range w.devDeleteBtnsR {
					if ptIn(dr, x, y) {
						kind, idx = hoverDevDeleteBtn, i
						break
					}
				}
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
	case ptIn(w.capMinR, x, y):
		procShowWindow.Call(uintptr(hwnd), swMinimize)
		return
	case ptIn(w.capMaxR, x, y):
		if zoomed, _, _ := procIsZoomed.Call(uintptr(hwnd)); zoomed != 0 {
			procShowWindow.Call(uintptr(hwnd), swRestore)
		} else {
			procShowWindow.Call(uintptr(hwnd), swMaximize)
		}
		return
	case ptIn(w.capCloseR, x, y):
		procPostMessageW.Call(uintptr(hwnd), wmClose, 0, 0)
		return
	case ptIn(w.navHomeR, x, y):
		w.page = pageHome
		w.status = ""
		procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		return
	case ptIn(w.navDevicesR, x, y):
		w.page = pageDevices
		w.status = ""
		w.loadPeers()
		procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		return
	case ptIn(w.navSetR, x, y):
		w.page = pageSettings
		w.status = ""
		w.refreshLocalMeta()
		procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		return
	}

	switch w.page {
	case pageHome:
		switch {
		case ptIn(w.tileFilesR, x, y):
			if files := openFileDialog(uintptr(hwnd)); len(files) > 0 {
				w.queued = files
				w.status = ""
				procInvalidateRect.Call(uintptr(hwnd), 0, 1)
			}
		case ptIn(w.tileFolderR, x, y):
			if dir := browseFolderDialog(hwnd, "选择要发送的文件夹"); dir != "" {
				files := expandSendPaths([]string{dir})
				if len(files) == 0 {
					w.status = "所选文件夹内没有文件"
				} else {
					w.queued = files
					w.status = ""
				}
				procInvalidateRect.Call(uintptr(hwnd), 0, 1)
			}
		case len(w.queued) > 0 && ptIn(w.queueClearR, x, y):
			w.queued = nil
			procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		case ptIn(w.addDevLinkR, x, y):
			openPairWindow(hwnd, w.app)
		case ptIn(w.peerPickerR, x, y):
			if len(w.peers) == 0 {
				openPairWindow(hwnd, w.app)
			} else if len(w.peers) > 1 {
				w.sel = (w.sel + 1) % len(w.peers)
				procInvalidateRect.Call(uintptr(hwnd), 0, 1)
			}
		case ptIn(w.sendBtnR, x, y):
			peer, ok := w.activePeer()
			if !ok {
				openPairWindow(hwnd, w.app)
				return
			}
			if len(w.queued) > 0 {
				files := append([]string(nil), w.queued...)
				w.queued = nil
				w.sendAsync(hwnd, peer, files)
			} else {
				w.pickAndSend(hwnd)
			}
		case ptIn(w.sendTextR, x, y):
			w.openSendText(hwnd)
		case ptIn(w.sendClipR, x, y):
			w.sendClipboard(hwnd)
		case ptIn(w.tempShareBtnR, x, y):
			openTempShareWindow(hwnd, w.app)
		case ptIn(w.fltAllR, x, y):
			w.filter = fltAll
			procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		case ptIn(w.fltSendR, x, y):
			w.filter = fltSend
			procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		case ptIn(w.fltRecvR, x, y):
			w.filter = fltRecv
			procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		case ptIn(w.openDirR, x, y):
			if w.app != nil {
				shellOpen(w.app.receiveDir())
			}
		default:
			for _, rr := range w.ledgerRowsR {
				if ptIn(rr, x, y) && w.app != nil {
					shellOpen(w.app.receiveDir())
					return
				}
			}
		}

	case pageDevices:
		if ptIn(w.devAddBtnR, x, y) {
			openPairWindow(hwnd, w.app)
			return
		}
		for i, br := range w.devSendBtnsR {
			if ptIn(br, x, y) && i < len(w.peers) {
				w.sel = i
				w.pickAndSend(hwnd)
				return
			}
		}
		for i, rr := range w.devRenameBtnsR {
			if ptIn(rr, x, y) && i < len(w.peers) {
				w.openPeerRenameDialog(hwnd, w.peers[i])
				return
			}
		}
		for i, dr := range w.devDeleteBtnsR {
			if ptIn(dr, x, y) && i < len(w.peers) {
				w.removeSavedPeer(hwnd, w.peers[i])
				return
			}
		}

	case pageSettings:
		switch {
		case ptIn(w.setRecvBtnR, x, y):
			on := !recvEnabled.Load()
			recvEnabled.Store(on)
			w.status = map[bool]string{true: "已开启后台接收", false: "已暂停接收"}[on]
			procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		case w.setResetDirR.right > w.setResetDirR.left && ptIn(w.setResetDirR, x, y):
			if w.app != nil {
				if dir, err := w.app.resetReceiveDir(); err != nil {
					w.status = "恢复默认目录失败：" + err.Error()
				} else {
					w.status = "已恢复默认下载目录：" + dir
				}
				procInvalidateRect.Call(uintptr(hwnd), 0, 1)
			}
		case ptIn(w.setChangeDirR, x, y):
			if chosen := browseFolderDialog(hwnd, "选择接收文件保存目录"); chosen != "" && w.app != nil {
				if err := w.app.setReceiveDir(chosen); err != nil {
					w.status = "更改下载目录失败：" + err.Error()
				} else {
					w.status = "接收目录已更改为 " + w.app.receiveDir()
				}
				procInvalidateRect.Call(uintptr(hwnd), 0, 1)
			}
		case ptIn(w.setOpenDirR, x, y):
			if w.app != nil {
				shellOpen(w.app.receiveDir())
			}
		case ptIn(w.setAutoBtnR, x, y):
			on := !autostartEnabled()
			if err := setAutostart(on); err != nil {
				w.status = "开机自启设置失败：" + err.Error()
			} else {
				w.status = map[bool]string{true: "已设置开机静默自启", false: "已取消开机自启"}[on]
			}
			procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		case ptIn(w.setRenameBtnR, x, y):
			w.openRenameDialog(hwnd)
		case ptIn(w.setCopyIDRowR, x, y):
			if w.fullID != "" && copyToClipboard(hwnd, w.fullID) {
				w.status = "已复制本机设备 ID"
				procInvalidateRect.Call(uintptr(hwnd), 0, 1)
			}
		}
	}
}

func (w *mainWin) sendClipboard(hwnd syscall.Handle) {
	peer, ok := w.activePeer()
	if !ok {
		openPairWindow(hwnd, w.app)
		return
	}
	txt, ok := readClipboardText()
	if !ok || strings.TrimSpace(txt) == "" {
		w.status = "剪贴板中没有文本内容"
		procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		return
	}
	w.sendTextPayload(hwnd, peer, txt, "clipboard", "剪贴板")
}

func (w *mainWin) openSendText(hwnd syscall.Handle) {
	peer, ok := w.activePeer()
	if !ok {
		openPairWindow(hwnd, w.app)
		return
	}
	openTextInputDialog(textInputOpts{
		Owner:       hwnd,
		Title:       "发送文字",
		Subtitle:    "发给 " + peerLabel(peer) + "（Ctrl+Enter 快速发送）",
		ConfirmText: "发送",
		Multiline:   true,
		AllowEmpty:  false,
		OnSubmit: func(text string) {
			w.sendTextPayload(hwnd, peer, text, "text", "文字")
		},
	})
}

func (w *mainWin) sendTextPayload(hwnd syscall.Handle, peer client.Peer, txt, prefix, kindLabel string) {
	tmp, err := writeTempTextFile(prefix, txt)
	if err != nil {
		w.status = "创建临时文本失败：" + err.Error()
		procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		return
	}
	w.status = fmt.Sprintf("正在将%s发往 %s…", kindLabel, peerLabel(peer))
	procInvalidateRect.Call(uintptr(hwnd), 0, 1)
	go func() {
		defer os.RemoveAll(filepath.Dir(tmp))
		res, err := sendToPeer(w.app, []string{tmp}, peer)
		msg := fmt.Sprintf("已将%s发给 %s（%d 字节）", kindLabel, peerLabel(peer), res.Bytes)
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

func (w *mainWin) openRenameDialog(hwnd syscall.Handle) {
	if w.app == nil {
		return
	}
	openTextInputDialog(textInputOpts{
		Owner:       hwnd,
		Title:       "修改设备名称",
		Subtitle:    "其他设备配对或传输时将看到此名称（留空恢复计算机名）",
		Initial:     w.app.deviceName(),
		ConfirmText: "保存",
		Multiline:   false,
		AllowEmpty:  true,
		OnSubmit: func(text string) {
			eff, err := w.app.setDeviceName(text)
			mainMu.Lock()
			if mw := mainWins[hwnd]; mw != nil {
				mw.hostName = eff
				if err != nil {
					mw.status = "保存设备名称失败：" + err.Error()
				} else {
					mw.status = "设备名称已更新为 " + eff
				}
			}
			mainMu.Unlock()
			procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		},
	})
}

func (w *mainWin) openPeerRenameDialog(hwnd syscall.Handle, peer client.Peer) {
	if w.app == nil {
		return
	}
	openTextInputDialog(textInputOpts{
		Owner:       hwnd,
		Title:       "重命名已保存设备",
		Subtitle:    "仅修改本机显示的设备备注名（设备 ID " + truncID(peer.ID) + "）",
		Initial:     peer.Name,
		ConfirmText: "保存",
		Multiline:   false,
		AllowEmpty:  true,
		OnSubmit: func(text string) {
			clean := pairing.SanitizeName(text)
			ok, err := identity.RenamePeer(w.app.pairedPath, peer.ID, clean)
			mainMu.Lock()
			if mw := mainWins[hwnd]; mw != nil {
				if err != nil {
					mw.status = "重命名失败：" + err.Error()
				} else if ok {
					mw.loadPeers()
					shown := clean
					if shown == "" {
						shown = truncID(peer.ID)
					}
					mw.status = "已将设备重命名为 " + shown
				}
			}
			mainMu.Unlock()
			if thePanel != nil {
				thePanel.reloadPeers()
			}
			procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		},
	})
}

func (w *mainWin) removeSavedPeer(hwnd syscall.Handle, peer client.Peer) {
	if w.app == nil {
		return
	}
	pairedPath := w.app.pairedPath
	label := peerLabel(peer)
	openConfirmDialog(confirmOpts{
		Owner:       hwnd,
		Title:       "移除设备",
		Body:        fmt.Sprintf("确定移除已保存的设备「%s」吗？\n移除后需重新配对才能向该设备收发文件。", label),
		ConfirmText: "移除",
		CancelText:  "取消",
		Danger:      true,
		OnConfirm: func() {
			ok, err := identity.RemovePeer(pairedPath, peer.ID)
			mainMu.Lock()
			if mw := mainWins[hwnd]; mw != nil {
				if err != nil {
					mw.status = "移除失败：" + err.Error()
				} else if ok {
					mw.loadPeers()
					mw.status = "已移除设备 " + label
				}
			}
			mainMu.Unlock()
			if thePanel != nil {
				thePanel.reloadPeers()
			}
			procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		},
	})
}

func (w *mainWin) pickAndSend(hwnd syscall.Handle) {
	peer, ok := w.activePeer()
	if !ok {
		openPairWindow(hwnd, w.app)
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
	var files []string
	count, _, _ := procDragQueryFileW.Call(uintptr(hdrop), 0xffffffff, 0, 0)
	for i := uintptr(0); i < count; i++ {
		var buf [1024]uint16
		n, _, _ := procDragQueryFileW.Call(uintptr(hdrop), i, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		if n > 0 {
			files = append(files, syscall.UTF16ToString(buf[:n]))
		}
	}
	peer, ok := w.activePeer()
	if !ok {
		w.queued = expandSendPaths(files)
		openPairWindow(hwnd, w.app)
		return
	}
	w.sendAsync(hwnd, peer, files)
}

func (w *mainWin) sendAsync(hwnd syscall.Handle, peer client.Peer, files []string) {
	if len(files) == 0 || w.app == nil {
		return
	}
	w.status = fmt.Sprintf("正在发送给 %s…", peerLabel(peer))
	procInvalidateRect.Call(uintptr(hwnd), 0, 1)
	go func() {
		res, err := sendToPeer(w.app, files, peer)
		msg := fmt.Sprintf("已发送给 %s（%s）", peerLabel(peer), summarizeFiles(res.Files))
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

func splitMultiSelect(buf []uint16) []string {
	var parts []string
	start := 0
	for i, c := range buf {
		if c != 0 {
			continue
		}
		if i == start {
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
