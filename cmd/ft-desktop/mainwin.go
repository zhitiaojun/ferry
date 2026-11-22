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

	designW    = 880
	designH    = 540
	designMinW = 720
	designMinH = 440

	railW      = 64
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
	hoverNavHome
	hoverNavDevices
	hoverNavSettings
	hoverTileFiles
	hoverTileFolder
	hoverQueueClear
	hoverAddDevice
	hoverPeerPicker
	hoverSendBtn
	hoverSendClip
	hoverFltAll
	hoverFltSend
	hoverFltRecv
	hoverOpenDir
	hoverLedgerRow
	hoverDevAddBtn
	hoverDevSendBtn
	hoverSetRecv
	hoverSetOpenDir
	hoverSetAuto
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
	railR       rectT
	navHomeR    rectT
	navDevicesR rectT
	navSetR     rectT

	sendColR    rectT
	sendCardR   rectT
	tileFilesR  rectT
	tileFolderR rectT
	queueBoxR   rectT
	queueClearR rectT
	addDevLinkR rectT
	peerPickerR rectT
	sendBtnR    rectT
	sendClipR   rectT

	fltAllR     rectT
	fltSendR    rectT
	fltRecvR    rectT
	openDirR    rectT
	ledgerRowsR []rectT

	devAddBtnR   rectT
	devSendBtnsR []rectT

	setRecvBtnR   rectT
	setOpenDirR   rectT
	setAutoBtnR   rectT
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
	var wr, cr rectT
	procGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&wr)))
	procGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&cr)))
	frameW := (wr.right - wr.left) - cr.right
	frameH := (wr.bottom - wr.top) - cr.bottom
	procDefWindowProcW.Call(uintptr(hwnd), wmGetMinMax, 0, lParam)
	m.minTrack = pointT{w.px(designMinW) + frameW, w.px(designMinH) + frameH}
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
	pick := func(cands []string) string {
		for _, c := range cands {
			if faceExists(c) {
				return c
			}
		}
		return cands[len(cands)-1]
	}
	sansFace = pick([]string{"Microsoft YaHei UI", "Microsoft YaHei", "Segoe UI"})
	monoFace = pick([]string{"Consolas", "Cascadia Mono", "Courier New"})
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
	mk := func(size int32, weight int32, face string) syscall.Handle {
		h, _, _ := procCreateFontW.Call(uintptr(-w.px(size)), 0, 0, 0, uintptr(weight), 0, 0, 0,
			1, 0, 0, 5, 0, uintptr(unsafe.Pointer(utf16Ptr(face))))
		return syscall.Handle(h)
	}
	w.fontH1 = mk(19, 700, sansFace)
	w.fontH2 = mk(15, 600, sansFace)
	w.fontBody = mk(13, 400, sansFace)
	w.fontBold = mk(13, 600, sansFace)
	w.fontSm = mk(12, 400, sansFace)
	w.fontMono = mk(12, 400, monoFace)
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
	w.hostName, _ = os.Hostname()
	if w.hostName == "" {
		w.hostName = "Windows"
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
	rw := p(railW)
	w.railR = rectT{0, 0, rw, ch}

	navX := p(8)
	navW := rw - p(16)
	navH := p(50)
	w.navHomeR = rectT{navX, p(16), navX + navW, p(16) + navH}
	w.navDevicesR = rectT{navX, w.navHomeR.bottom + p(6), navX + navW, w.navHomeR.bottom + p(6) + navH}
	w.navSetR = rectT{navX, w.navDevicesR.bottom + p(6), navX + navW, w.navDevicesR.bottom + p(6) + navH}

	// Reset page-specific rects
	w.tileFilesR, w.tileFolderR, w.queueBoxR, w.queueClearR = rectT{}, rectT{}, rectT{}, rectT{}
	w.addDevLinkR, w.peerPickerR, w.sendBtnR, w.sendClipR = rectT{}, rectT{}, rectT{}, rectT{}
	w.fltAllR, w.fltSendR, w.fltRecvR, w.openDirR = rectT{}, rectT{}, rectT{}, rectT{}
	w.ledgerRowsR = w.ledgerRowsR[:0]
	w.devAddBtnR = rectT{}
	w.devSendBtnsR = w.devSendBtnsR[:0]
	w.setRecvBtnR, w.setOpenDirR, w.setAutoBtnR, w.setCopyIDRowR = rectT{}, rectT{}, rectT{}, rectT{}

	switch w.page {
	case pageHome:
		colW := p(sendColW)
		if colW > (cw-rw)/2 {
			colW = (cw - rw) / 2
		}
		w.sendColR = rectT{rw, 0, rw + colW, ch}
		pad := p(22)
		cardW := colW - pad*2
		cardH := p(252)
		if len(w.queued) > 0 {
			cardH = p(292)
		}
		cy := (ch - cardH) / 2
		if cy < p(24) {
			cy = p(24)
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
		w.sendClipR = rectT{inL, w.sendCardR.bottom + p(10), inL + p(86), w.sendCardR.bottom + p(32)}

		// Right transfers pane
		rPad := p(28)
		paneL := w.sendColR.right + rPad
		paneR := cw - rPad
		tabY := p(66)
		w.fltAllR = rectT{paneL, tabY, paneL + p(42), tabY + p(28)}
		w.fltSendR = rectT{w.fltAllR.right + p(16), tabY, w.fltAllR.right + p(68), tabY + p(28)}
		w.fltRecvR = rectT{w.fltSendR.right + p(16), tabY, w.fltSendR.right + p(68), tabY + p(28)}
		w.openDirR = rectT{paneR - p(104), tabY, paneR, tabY + p(28)}

		ry := w.fltAllR.bottom + p(8)
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
		w.devAddBtnR = rectT{paneR - p(96), p(28), paneR, p(62)}
		y := p(98)
		rowH := p(58)
		for range w.peers {
			if y+rowH > ch-pad {
				break
			}
			w.devSendBtnsR = append(w.devSendBtnsR, rectT{paneR - p(88), y + p(13), paneR - p(16), y + rowH - p(13)})
			y += rowH
		}

	case pageSettings:
		pad := p(28)
		paneL := rw + pad
		paneR := cw - pad
		rowH := p(58)
		y := p(90)
		w.setRecvBtnR = rectT{paneR - p(88), y + p(13), paneR - p(16), y + rowH - p(13)}
		y += rowH
		w.setOpenDirR = rectT{paneR - p(88), y + p(13), paneR - p(16), y + rowH - p(13)}
		y += rowH
		w.setAutoBtnR = rectT{paneR - p(88), y + p(13), paneR - p(16), y + rowH - p(13)}
		_ = paneL
		infoY := y + rowH + p(50) + p(42)
		w.setCopyIDRowR = rectT{paneL, infoY, paneR, infoY + p(42)}
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

func (w *mainWin) drawScene(cv *glassCanvas, cr rectT) {
	// 1. Left navigation rail (Win11 Acrylic frosted glass when available).
	if w.acrylic {
		railAlpha := byte(175)
		if w.dark {
			railAlpha = 185
		}
		cv.fillColorAlpha(w.railR, w.pal.paper2, railAlpha)
	} else {
		cv.fillColorAlpha(w.railR, w.pal.paper, 255)
	}
	// Hairline divider between rail and workspace.
	cv.fillColorAlpha(rectT{w.railR.right - 1, 0, w.railR.right, cr.bottom}, w.pal.line, 255)
	w.drawRail(cv)

	// 2. Right workspace (crisp opaque surfaces for comfortable daily reading).
	rightR := rectT{w.railR.right, 0, cr.right, cr.bottom}
	cv.fillColorAlpha(rightR, w.pal.paper, 255)

	switch w.page {
	case pageHome:
		w.drawHome(cv, cr)
	case pageDevices:
		w.drawDevices(cv, cr)
	case pageSettings:
		w.drawSettings(cv, cr)
	}
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
	// Left send card gutter
	cv.fillColorAlpha(w.sendColR, w.pal.paper2, 255)
	cv.fillColorAlpha(rectT{w.sendColR.right - 1, 0, w.sendColR.right, cr.bottom}, w.pal.line, 255)

	// Compact Send Card
	cv.drawAlphaRoundRect(w.sendCardR, float64(p(12)), w.pal.paper, 255, w.pal.line, 255, 1.0)

	// Dual tiles: 添加文件 / 添加文件夹
	fEdge := w.pal.line
	if w.hover == hoverTileFiles {
		fEdge = w.pal.accent
	}
	cv.drawAlphaRoundRect(w.tileFilesR, float64(p(8)), w.pal.paper, 255, fEdge, 255, 1.0)
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
	cv.drawAlphaRoundRect(w.tileFolderR, float64(p(8)), w.pal.paper, 255, dEdge, 255, 1.0)
	fldIconR := rectT{(w.tileFolderR.left+w.tileFolderR.right)/2 - p(13), w.tileFolderR.top + p(12),
		(w.tileFolderR.left+w.tileFolderR.right)/2 + p(13), w.tileFolderR.top + p(38)}
	cv.drawAlphaRoundRect(fldIconR, float64(p(13)), w.pal.paper, 255, w.pal.line, 255, 1.0)
	w.drawFolderGlyph(cv, fldIconR, w.pal.ink2)
	cv.drawAlphaText(w.fontSm, "添加文件夹",
		rectT{w.tileFolderR.left, fldIconR.bottom + p(4), w.tileFolderR.right, w.tileFolderR.bottom - p(6)},
		dtCenter|dtSingleLine|dtVCenter, w.pal.ink)

	// Queued summary row
	if len(w.queued) > 0 {
		cv.drawAlphaRoundRect(w.queueBoxR, float64(p(6)), w.pal.paper2, 255, w.pal.line, 255, 1.0)
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
	cv.drawAlphaRoundRect(w.peerPickerR, float64(p(8)), w.pal.paper, 255, pEdge, 255, 1.0)
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
	clipColor := w.pal.ink2
	if w.hover == hoverSendClip {
		clipColor = w.pal.accent
	}
	cv.drawAlphaText(w.fontSm, "发送剪贴板", w.sendClipR, dtSingleLine|dtVCenter, clipColor)
	cv.drawAlphaText(w.fontSm, "支持直接拖入文件",
		rectT{w.sendClipR.right, w.sendClipR.top, w.sendCardR.right, w.sendClipR.bottom},
		dtRight|dtSingleLine|dtVCenter, w.pal.ink3)

	// Right Transfers Pane
	paneL := w.sendColR.right + p(28)
	paneR := cr.right - p(28)
	cv.drawAlphaText(w.fontH1, "传输记录", rectT{paneL, p(24), paneR, p(58)}, dtSingleLine|dtVCenter, w.pal.ink)

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

	rows := w.filteredLedger()
	if len(rows) == 0 {
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
			if w.hover == hoverLedgerRow && w.hoverIdx == i {
				cv.fillColorAlpha(rr, w.pal.paper2, 255)
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
			if r.time != "" {
				subParts = append(subParts, r.time)
			}
			if r.path == "失败" {
				subParts = append(subParts, "失败")
			}
			sub := strings.Join(subParts, " · ")

			rightActionW := p(72)
			cv.drawAlphaText(w.fontBold, title,
				rectT{rr.left + p(4), rr.top + p(6), rr.right - rightActionW, rr.top + p(28)},
				dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink)
			cv.drawAlphaText(w.fontSm, sub,
				rectT{rr.left + p(4), rr.top + p(28), rr.right - rightActionW, rr.bottom - p(6)},
				dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)
			if w.hover == hoverLedgerRow && w.hoverIdx == i {
				cv.drawAlphaText(w.fontSm, "打开位置",
					rectT{rr.right - rightActionW, rr.top, rr.right - p(4), rr.bottom},
					dtRight|dtSingleLine|dtVCenter, w.pal.accent)
			}
			cv.fillColorAlpha(rectT{rr.left, rr.bottom - 1, rr.right, rr.bottom}, w.pal.lineSoft, 255)
		}
	}

	if w.status != "" {
		cv.drawAlphaText(w.fontSm, w.status,
			rectT{paneL, cr.bottom - p(32), paneR, cr.bottom - p(8)},
			dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)
	}
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
	pad := p(28)
	paneL := w.railR.right + pad
	paneR := cr.right - pad

	cv.drawAlphaText(w.fontH1, "设备", rectT{paneL, p(24), paneR - p(110), p(56)}, dtSingleLine|dtVCenter, w.pal.ink)
	cv.drawAlphaText(w.fontSm, "已配对的设备可直接互相收发文件。", rectT{paneL, p(58), paneR - p(110), p(80)},
		dtSingleLine|dtVCenter, w.pal.ink2)

	addFill := w.pal.accent
	if w.hover == hoverDevAddBtn {
		addFill = shade(addFill, w.dark, 0.12)
	}
	cv.drawAlphaRoundRect(w.devAddBtnR, float64(p(8)), addFill, 255, addFill, 255, 0)
	cv.drawAlphaText(w.fontBold, "添加设备", w.devAddBtnR, dtCenter|dtSingleLine|dtVCenter, w.pal.accentInk)

	if len(w.peers) == 0 {
		box := rectT{paneL, p(98), paneR, p(228)}
		cv.drawAlphaRoundRect(box, float64(p(12)), w.pal.paper2, 255, w.pal.line, 255, 1.0)
		cv.drawAlphaText(w.fontBold, "还没有已配对设备", rectT{paneL, p(134), paneR, p(162)},
			dtCenter|dtSingleLine|dtVCenter, w.pal.ink)
		cv.drawAlphaText(w.fontSm, "点击右上角「添加设备」，用手机轻渡扫码或输入 6 位配对码即可绑定。",
			rectT{paneL + p(20), p(164), paneR - p(20), p(198)}, dtCenter|dtSingleLine|dtVCenter, w.pal.ink2)
		return
	}

	rowH := p(58)
	top := p(98)
	box := rectT{paneL, top, paneR, top + int32(len(w.devSendBtnsR))*rowH}
	cv.drawAlphaRoundRect(box, float64(p(12)), w.pal.paper, 255, w.pal.line, 255, 1.0)
	for i, br := range w.devSendBtnsR {
		ry := top + int32(i)*rowH
		peer := w.peers[i]
		cv.drawAlphaText(w.fontBold, peerLabel(peer),
			rectT{paneL + p(16), ry + p(8), br.left - p(12), ry + p(30)},
			dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink)
		cv.drawAlphaText(w.fontSm, "设备 ID "+truncID(peer.ID),
			rectT{paneL + p(16), ry + p(30), br.left - p(12), ry + p(50)},
			dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)

		bFill := w.pal.paper2
		if w.hover == hoverDevSendBtn && w.hoverIdx == i {
			bFill = w.pal.paper3
		}
		cv.drawAlphaRoundRect(br, float64(p(6)), bFill, 255, w.pal.line, 255, 1.0)
		cv.drawAlphaText(w.fontSm, "发送文件", br, dtCenter|dtSingleLine|dtVCenter, w.pal.ink)
		if i+1 < len(w.devSendBtnsR) {
			cv.fillColorAlpha(rectT{paneL + p(16), ry + rowH - 1, paneR - p(16), ry + rowH}, w.pal.lineSoft, 255)
		}
	}
}

func (w *mainWin) drawSettings(cv *glassCanvas, cr rectT) {
	p := w.px
	pad := p(28)
	paneL := w.railR.right + pad
	paneR := cr.right - pad

	cv.drawAlphaText(w.fontH1, "设置", rectT{paneL, p(24), paneR, p(56)}, dtSingleLine|dtVCenter, w.pal.ink)
	cv.drawAlphaText(w.fontSm, "常规接收选项与本机技术信息。", rectT{paneL, p(58), paneR, p(80)},
		dtSingleLine|dtVCenter, w.pal.ink2)

	rowH := p(58)
	top := p(90)
	g1 := rectT{paneL, top, paneR, top + rowH*3}
	cv.drawAlphaRoundRect(g1, float64(p(12)), w.pal.paper, 255, w.pal.line, 255, 1.0)

	recvTxt := "已暂停"
	if recvEnabled.Load() {
		recvTxt = "已开启"
	}
	autoTxt := "未开启"
	if autostartEnabled() {
		autoTxt = "已开启"
	}
	dirText := ""
	portText := "18800"
	if w.app != nil {
		dirText = w.app.dir
		portText = fmt.Sprintf("%d", w.app.port)
	}

	g1Rows := []struct {
		title, sub, btn string
		btnR            rectT
		hoverID         int
	}{
		{"后台接收文件", "关闭后其他设备暂时无法向此电脑发送", recvTxt, w.setRecvBtnR, hoverSetRecv},
		{"接收文件保存位置", dirText, "打开", w.setOpenDirR, hoverSetOpenDir},
		{"开机自动启动", "登录后自动缩至右下角托盘接收", autoTxt, w.setAutoBtnR, hoverSetAuto},
	}
	for i, r := range g1Rows {
		ry := top + int32(i)*rowH
		cv.drawAlphaText(w.fontBold, r.title,
			rectT{paneL + p(16), ry + p(8), r.btnR.left - p(12), ry + p(30)},
			dtSingleLine|dtVCenter, w.pal.ink)
		cv.drawAlphaText(w.fontSm, r.sub,
			rectT{paneL + p(16), ry + p(30), r.btnR.left - p(12), ry + p(50)},
			dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)

		bFill := w.pal.paper2
		if w.hover == r.hoverID {
			bFill = w.pal.paper3
		}
		cv.drawAlphaRoundRect(r.btnR, float64(p(6)), bFill, 255, w.pal.line, 255, 1.0)
		cv.drawAlphaText(w.fontSm, r.btn, r.btnR, dtCenter|dtSingleLine|dtVCenter, w.pal.ink)
		if i+1 < len(g1Rows) {
			cv.fillColorAlpha(rectT{paneL + p(16), ry + rowH - 1, paneR - p(16), ry + rowH}, w.pal.lineSoft, 255)
		}
	}

	// Quiet technical info section
	lblY := g1.bottom + p(24)
	cv.drawAlphaText(w.fontBold, "本机信息", rectT{paneL + p(4), lblY, paneR, lblY + p(22)},
		dtSingleLine|dtVCenter, w.pal.ink2)

	infoRowH := p(42)
	infoTop := lblY + p(26)
	g2 := rectT{paneL, infoTop, paneR, infoTop + infoRowH*3}
	cv.drawAlphaRoundRect(g2, float64(p(12)), w.pal.paper, 255, w.pal.line, 255, 1.0)

	idShow := truncID(w.fullID) + "（点击复制）"
	if w.fullID == "" {
		idShow = "未生成"
	}
	g2Rows := []struct {
		k, v string
		font syscall.Handle
	}{
		{"设备名称", w.hostName, w.fontSm},
		{"设备 ID", idShow, w.fontMono},
		{"本地监听端口", portText, w.fontMono},
	}
	for i, r := range g2Rows {
		ry := infoTop + int32(i)*infoRowH
		if i == 1 && w.hover == hoverSetCopyID {
			cv.fillColorAlpha(rectT{paneL + 1, ry + 1, paneR - 1, ry + infoRowH - 1}, w.pal.paper2, 255)
		}
		cv.drawAlphaText(w.fontSm, r.k, rectT{paneL + p(16), ry, paneL + p(140), ry + infoRowH},
			dtSingleLine|dtVCenter, w.pal.ink2)
		cv.drawAlphaText(r.font, r.v, rectT{paneL + p(140), ry, paneR - p(16), ry + infoRowH},
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
	case "lan", "打洞", "punch", "中继", "relay", "失败", "failed", "error", "未配对":
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// Interaction
// ---------------------------------------------------------------------------

func (w *mainWin) cursorIsHand() bool {
	return w.hover != hoverNone
}

func (w *mainWin) onMouseMove(hwnd syscall.Handle, x, y int32) {
	var cr rectT
	procGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&cr)))
	w.layout(cr.right, cr.bottom)

	kind, idx := hoverNone, 0
	switch {
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
	case w.page == pageHome && ptIn(w.sendClipR, x, y):
		kind = hoverSendClip
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
	case w.page == pageSettings && ptIn(w.setOpenDirR, x, y):
		kind = hoverSetOpenDir
	case w.page == pageSettings && ptIn(w.setAutoBtnR, x, y):
		kind = hoverSetAuto
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
		case ptIn(w.sendClipR, x, y):
			w.sendClipboard(hwnd)
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
				shellOpen(w.app.dir)
			}
		default:
			for _, rr := range w.ledgerRowsR {
				if ptIn(rr, x, y) && w.app != nil {
					shellOpen(w.app.dir)
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

	case pageSettings:
		switch {
		case ptIn(w.setRecvBtnR, x, y):
			on := !recvEnabled.Load()
			recvEnabled.Store(on)
			w.status = map[bool]string{true: "已开启后台接收", false: "已暂停接收"}[on]
			procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		case ptIn(w.setOpenDirR, x, y):
			if w.app != nil {
				shellOpen(w.app.dir)
			}
		case ptIn(w.setAutoBtnR, x, y):
			on := !autostartEnabled()
			if err := setAutostart(on); err != nil {
				w.status = "开机自启设置失败：" + err.Error()
			} else {
				w.status = map[bool]string{true: "已设置开机静默自启", false: "已取消开机自启"}[on]
			}
			procInvalidateRect.Call(uintptr(hwnd), 0, 1)
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
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("clipboard-%s.txt", time.Now().Format("150405")))
	if err := os.WriteFile(tmp, []byte(txt), 0o600); err != nil {
		w.status = "读取剪贴板失败：" + err.Error()
		procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		return
	}
	w.status = fmt.Sprintf("正在将剪贴板发往 %s…", peerLabel(peer))
	procInvalidateRect.Call(uintptr(hwnd), 0, 1)
	go func() {
		defer os.Remove(tmp)
		res, err := sendToPeer(w.app, []string{tmp}, peer)
		msg := fmt.Sprintf("已将剪贴板发给 %s（%d 字节）", peerLabel(peer), res.Bytes)
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
