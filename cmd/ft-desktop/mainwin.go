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

	"github.com/zhitiaojun/ferry/core/client"
	"github.com/zhitiaojun/ferry/core/identity"
	"github.com/zhitiaojun/ferry/core/pairing"
	"github.com/zhitiaojun/ferry/core/proto"
)

const (
	mainClass         = "ftMainWin"
	wmAppPaint        = wmApp + 10
	wmAppReload       = wmApp + 11
	wmConfirmIncoming = wmApp + 12
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
	designH    = 580
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
	pageHistory  = 1
	pageDevices  = 2
	pageSettings = 3

	fltAll  = 0
	fltSend = 1
	fltRecv = 2

	hoverNone = iota
	hoverCapMin
	hoverCapMax
	hoverCapClose
	hoverNavHome
	hoverNavHistory
	hoverNavDevices
	hoverNavSettings
	hoverNetRetry
	hoverTargetPill
	hoverTileFiles
	hoverTileFolder
	hoverQueueClear
	hoverHomeScanLan
	hoverAddDevice
	hoverPeerPicker
	hoverSendBtn
	hoverSendText
	hoverSendClip
	hoverTempShare
	hoverAllHistory
	hoverFltAll
	hoverFltSend
	hoverFltRecv
	hoverOpenDir
	hoverTxPause
	hoverTxCancel
	hoverLedgerRow
	hoverDevScanLanBtn
	hoverDevAddBtn
	hoverDevSendBtn
	hoverDevRenameBtn
	hoverDevDeleteBtn
	hoverDevLanSendBtn
	hoverDevLanSelectBtn
	hoverSetRecv
	hoverSetResetDir
	hoverSetChangeDir
	hoverSetOpenDir
	hoverSetAuto
	hoverSetResetSrv
	hoverSetChangeSrv
	hoverSetRename
	hoverSetCopyID
	hoverAboutRepo
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
	app              *app
	hwnd             syscall.Handle
	peers            []client.Peer
	lanTargets       []lanPeerEntry
	selectedLAN      lanPeerEntry
	sel              int
	userPickedTarget bool
	clipPreview      clipboardPreview
	unreadHistory    bool
	lastLedgerHead   string
	page             int
	filter           int
	queued           []string
	status           string
	hostName         string
	fullID           string
	pal              palette
	dark             bool
	acrylic          bool
	dpi              int32
	hover            int
	hoverIdx         int
	tracking         bool
	timer            uintptr

	// Hit-test rectangles updated on every layout() pass.
	titleBarR   rectT
	capMinR     rectT
	capMaxR     rectT
	capCloseR   rectT
	railR       rectT
	navHomeR    rectT
	navHistoryR rectT
	navDevicesR rectT
	navSetR     rectT

	netBannerR      rectT
	netRetryR       rectT
	targetPillRects []rectT
	targetPillItems []sendTarget
	dropZoneR       rectT
	clipBarR        rectT
	latestBarR      rectT
	allHistoryLinkR rectT
	coldReadyR      rectT
	coldLeftCardR   rectT
	coldRightCardR  rectT

	sendColR      rectT
	sendCardR     rectT
	tileFilesR    rectT
	tileFolderR   rectT
	queueBoxR     rectT
	queueClearR   rectT
	homeScanLanR  rectT
	addDevLinkR   rectT
	peerPickerR   rectT
	sendBtnR      rectT
	sendTextR     rectT
	sendClipR     rectT
	tempShareBtnR rectT

	fltAllR     rectT
	fltSendR    rectT
	fltRecvR    rectT
	openDirR     rectT
	activeTxR    rectT
	txPauseBtnR  rectT
	txCancelBtnR rectT
	ledgerRowsR  []rectT

	devScanLanBtnR    rectT
	devAddBtnR        rectT
	devSendBtnsR      []rectT
	devRenameBtnsR    []rectT
	devDeleteBtnsR    []rectT
	devLanSendBtnsR   []rectT
	devLanSelectBtnsR []rectT

	setRecvBtnR   rectT
	setResetDirR  rectT
	setChangeDirR rectT
	setOpenDirR   rectT
	setAutoBtnR   rectT
	setResetSrvR  rectT
	setChangeSrvR rectT
	setRenameBtnR rectT
	setCopyIDRowR rectT
	aboutRepoR    rectT

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

	incomingMu      sync.Mutex
	pendingIncoming *incomingConfirmReq
)

type incomingConfirmReq struct {
	title  string
	body   string
	dlg    syscall.Handle
	done   bool
	respCh chan error
}

func isMainWindowOpen() (syscall.Handle, bool) {
	h := existingMainWindow()
	if h == 0 {
		return 0, false
	}
	vis, _, _ := procIsWindowVisible.Call(uintptr(h))
	if vis == 0 {
		return 0, false
	}
	iconic, _, _ := procIsIconic.Call(uintptr(h))
	if iconic != 0 {
		return 0, false
	}
	return h, true
}

func formatIncomingConfirmBody(peerName, remoteAddr string, m *proto.Manifest) string {
	from := peerName
	if from == "" {
		from = "局域网未知设备"
	}
	if remoteAddr != "" {
		from += " (" + remoteAddr + ")"
	}
	var total uint64
	for _, f := range m.Files {
		total += f.Size
	}
	var lines []string
	limit := len(m.Files)
	if limit > 3 {
		limit = 3
	}
	for i := 0; i < limit; i++ {
		lines = append(lines, fmt.Sprintf("• %s (%s)", m.Files[i].Name, humanBytes(int64(m.Files[i].Size))))
	}
	if len(m.Files) > limit {
		lines = append(lines, fmt.Sprintf("• 等共 %d 个文件", len(m.Files)))
	}
	return fmt.Sprintf("来自：%s\n共 %d 个文件（%s）\n%s\n\n该设备未配对，仅本次临时接收",
		from, len(m.Files), humanBytes(int64(total)), strings.Join(lines, "\n"))
}

func (r *incomingConfirmReq) resolve(err error) {
	incomingMu.Lock()
	if r.done {
		incomingMu.Unlock()
		return
	}
	r.done = true
	if pendingIncoming == r {
		pendingIncoming = nil
	}
	ch := r.respCh
	incomingMu.Unlock()
	select {
	case ch <- err:
	default:
	}
}

func cancelPendingIncomingConfirm(reason string) {
	incomingMu.Lock()
	req := pendingIncoming
	if req == nil || req.done {
		incomingMu.Unlock()
		return
	}
	req.done = true
	pendingIncoming = nil
	dlg := req.dlg
	ch := req.respCh
	incomingMu.Unlock()

	if dlg != 0 {
		procPostMessageW.Call(uintptr(dlg), wmClose, 0, 0)
	}
	if reason == "" {
		reason = "对方已关闭主界面"
	}
	select {
	case ch <- proto.NewError(proto.CodeCancelled, reason):
	default:
	}
}

func requestIncomingConfirm(peerName, remoteAddr string, m *proto.Manifest) error {
	hwnd, ok := isMainWindowOpen()
	if !ok {
		return proto.NewError(proto.CodeUnauthorized, "对方未打开轻渡主界面，无法接收局域网临时文件")
	}
	req := &incomingConfirmReq{
		title:  "收到局域网临时文件",
		body:   formatIncomingConfirmBody(peerName, remoteAddr, m),
		respCh: make(chan error, 1),
	}
	incomingMu.Lock()
	if pendingIncoming != nil && !pendingIncoming.done {
		incomingMu.Unlock()
		return proto.NewError(proto.CodeCancelled, "对方正在确认另一条传输请求，请稍后再试")
	}
	pendingIncoming = req
	incomingMu.Unlock()

	procPostMessageW.Call(uintptr(hwnd), wmConfirmIncoming, 0, 0)

	timer := time.NewTimer(60 * time.Second)
	defer timer.Stop()
	select {
	case err := <-req.respCh:
		return err
	case <-timer.C:
		incomingMu.Lock()
		dlg := req.dlg
		if !req.done {
			req.done = true
			if pendingIncoming == req {
				pendingIncoming = nil
			}
		}
		incomingMu.Unlock()
		if dlg != 0 {
			procPostMessageW.Call(uintptr(dlg), wmClose, 0, 0)
		}
		return proto.NewError(proto.CodeTimeout, "等待对方确认超时")
	}
}

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
			mw.ensureSmartDefaultTarget()
			mw.refreshClipboard()
			mw.syncTimer(h)
		}
		if a != nil {
			a.triggerLANScan(nil)
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
	procSetForegroundWindow.Call(uintptr(hwnd))
}

func openMainWindowOnHome(a *app) {
	openMainWindow(a)
	if h := existingMainWindow(); h != 0 {
		if mw := currentWin(h); mw != nil {
			mw.page = pageHome
			mw.syncTimer(h)
		}
		procSetForegroundWindow.Call(uintptr(h))
		procInvalidateRect.Call(uintptr(h), 0, 1)
	}
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
		mw.ensureSmartDefaultTarget()
		mw.refreshLocalMeta()
		mw.refreshClipboard()
		if trayApp != nil {
			trayApp.triggerLANScan(nil)
		}
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
		if wParam == 1 { // SIZE_MINIMIZED
			cancelPendingIncomingConfirm("对方最小化了主界面")
		}
		procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		return 0
	case wmActivate:
		if w != nil && loword(wParam) != 0 {
			w.refreshClipboard()
			w.ensureSmartDefaultTarget()
			procInvalidateRect.Call(uintptr(hwnd), 0, 0)
		}
		return 0
	case wmShowWindow:
		if w != nil {
			if wParam != 0 {
				w.refreshClipboard()
				w.ensureSmartDefaultTarget()
				if w.app != nil {
					w.app.triggerLANScan(nil)
				}
				w.syncTimer(hwnd)
			} else {
				cancelPendingIncomingConfirm("对方关闭了主界面")
				if w.timer != 0 {
					procKillTimer.Call(uintptr(hwnd), mainTimerID)
					w.timer = 0
				}
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
			w.ensureSmartDefaultTarget()
			w.refreshLocalMeta()
			w.refreshClipboard()
			procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		}
		return 0
	case wmConfirmIncoming:
		incomingMu.Lock()
		req := pendingIncoming
		incomingMu.Unlock()
		if req == nil {
			return 0
		}
		if _, ok := isMainWindowOpen(); !ok {
			req.resolve(proto.NewError(proto.CodeUnauthorized, "对方未打开轻渡主界面，无法接收局域网临时文件"))
			return 0
		}
		procSetForegroundWindow.Call(uintptr(hwnd))
		dlg := openConfirmDialog(confirmOpts{
			Owner:       hwnd,
			Title:       req.title,
			Body:        req.body,
			ConfirmText: "接收",
			CancelText:  "拒绝",
			OnConfirm: func() {
				req.resolve(nil)
			},
			OnCancel: func() {
				req.resolve(proto.NewError(proto.CodeCancelled, "对方拒绝接收"))
			},
		})
		if dlg == 0 {
			req.resolve(proto.NewError(proto.CodeCancelled, "无法弹出确认窗口"))
			return 0
		}
		incomingMu.Lock()
		stale := req.done
		if !stale {
			req.dlg = dlg
		}
		incomingMu.Unlock()
		if stale {
			procPostMessageW.Call(uintptr(dlg), wmClose, 0, 0)
		}
		return 0
	case wmClose:
		cancelPendingIncomingConfirm("对方关闭了主界面")
		procShowWindow.Call(uintptr(hwnd), swHide)
		return 0
	case wmDestroy:
		cancelPendingIncomingConfirm("对方关闭了主界面")
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
	w.syncLANTargets()
}

func (w *mainWin) syncLANTargets() {
	if w.app == nil {
		w.lanTargets = nil
		w.selectedLAN = lanPeerEntry{}
		return
	}
	w.lanTargets = w.app.unpairedLANPeers(w.peers)
	if w.selectedLAN.id != "" {
		found := false
		for _, e := range w.lanTargets {
			if e.id == w.selectedLAN.id {
				w.selectedLAN = e
				found = true
				break
			}
		}
		if !found {
			w.selectedLAN = lanPeerEntry{}
		}
	}
}

func (w *mainWin) refreshClipboard() {
	w.clipPreview = inspectClipboardPreview()
}

func (w *mainWin) syncUnreadHistory() {
	if w.app == nil {
		return
	}
	lines := w.app.recentLines(1)
	if len(lines) == 0 {
		return
	}
	head := lines[0]
	if w.lastLedgerHead == "" {
		w.lastLedgerHead = head
		return
	}
	if head != w.lastLedgerHead {
		w.lastLedgerHead = head
		if w.page != pageHistory {
			w.unreadHistory = true
		}
	}
}

func (w *mainWin) ensureSmartDefaultTarget() {
	if w.app == nil {
		return
	}
	w.syncLANTargets()
	if w.userPickedTarget {
		if w.selectedLAN.id != "" && w.selectedLAN.addr != "" {
			return
		}
		if w.sel >= 0 && w.sel < len(w.peers) {
			return
		}
		w.userPickedTarget = false
	}
	// Prefer a paired peer currently reachable on LAN, then cloud-online paired peer,
	// then the first paired peer, and finally an auto-discovered LAN peer.
	for i, p := range w.peers {
		if w.app.peerReachability(p.ID) == "lan" {
			w.sel = i
			w.selectedLAN = lanPeerEntry{}
			return
		}
	}
	for i, p := range w.peers {
		if w.app.peerReachability(p.ID) == "cloud" {
			w.sel = i
			w.selectedLAN = lanPeerEntry{}
			return
		}
	}
	if len(w.peers) > 0 {
		if w.sel < 0 || w.sel >= len(w.peers) {
			w.sel = 0
		}
		w.selectedLAN = lanPeerEntry{}
		return
	}
	if len(w.lanTargets) > 0 {
		w.selectedLAN = w.lanTargets[0]
	}
}

func (w *mainWin) targetReachability(t sendTarget) string {
	if t.isLAN {
		return "lan"
	}
	if w.app == nil {
		return "offline"
	}
	return w.app.peerReachability(t.peer.ID)
}

func targetsEqual(a, b sendTarget) bool {
	if a.isLAN != b.isLAN {
		return false
	}
	if a.isLAN {
		return a.lan.id == b.lan.id && a.lan.addr == b.lan.addr
	}
	return a.peer.ID == b.peer.ID
}

func (w *mainWin) measureText(font syscall.Handle, s string) int32 {
	if s == "" {
		return 0
	}
	dc, _, _ := procCompatDC.Call(0)
	if dc == 0 {
		return int32(len([]rune(s))) * w.px(13)
	}
	defer procDeleteDC.Call(dc)
	return textWidth(dc, font, s)
}

func (w *mainWin) syncTimer(hwnd syscall.Handle) {
	// Keep timer off when idle in v2; redraws happen on user/network events.
	if w.timer != 0 {
		procKillTimer.Call(uintptr(hwnd), mainTimerID)
		w.timer = 0
	}
}

type sendTarget struct {
	isLAN bool
	peer  client.Peer
	lan   lanPeerEntry
}

func (t sendTarget) label() string {
	if t.isLAN {
		if t.lan.name != "" {
			return t.lan.name
		}
		return t.lan.addr
	}
	return peerLabel(t.peer)
}

func (w *mainWin) targetCount() int {
	return len(w.peers) + len(w.lanTargets)
}

func (w *mainWin) hasPeers() bool { return w.targetCount() > 0 }

func (w *mainWin) activeTarget() (sendTarget, bool) {
	if w.selectedLAN.id != "" && w.selectedLAN.addr != "" {
		return sendTarget{isLAN: true, lan: w.selectedLAN}, true
	}
	if len(w.peers) > 0 {
		if w.sel < 0 || w.sel >= len(w.peers) {
			w.sel = 0
		}
		return sendTarget{peer: w.peers[w.sel]}, true
	}
	if len(w.lanTargets) > 0 {
		return sendTarget{isLAN: true, lan: w.lanTargets[0]}, true
	}
	return sendTarget{}, false
}

func (w *mainWin) cycleTarget() {
	total := w.targetCount()
	if total <= 1 {
		return
	}
	cur := 0
	if w.selectedLAN.id != "" {
		cur = len(w.peers)
		for i, e := range w.lanTargets {
			if e.id == w.selectedLAN.id {
				cur = len(w.peers) + i
				break
			}
		}
	} else if len(w.peers) > 0 {
		cur = w.sel
	}
	next := (cur + 1) % total
	if next < len(w.peers) {
		w.sel = next
		w.selectedLAN = lanPeerEntry{}
	} else {
		w.selectedLAN = w.lanTargets[next-len(w.peers)]
	}
}

func (w *mainWin) scanLAN(hwnd syscall.Handle) {
	if w.app == nil {
		return
	}
	w.status = "正在搜索同局域网设备…"
	started := w.app.triggerLANScan(func(_ int) {
		mainMu.Lock()
		if mw := mainWins[hwnd]; mw != nil {
			mw.syncLANTargets()
			if len(mw.lanTargets) > 0 {
				mw.status = fmt.Sprintf("已发现 %d 台局域网未配对设备", len(mw.lanTargets))
			} else if len(mw.peers) > 0 {
				mw.status = "未发现新的未配对局域网设备"
			} else {
				mw.status = "暂未发现其他局域网设备，请确认对方已打开轻渡"
			}
		}
		mainMu.Unlock()
		procPostMessageW.Call(uintptr(hwnd), wmAppPaint, 0, 0)
	})
	if !started {
		w.status = "正在搜索局域网，请稍候…"
	}
	procInvalidateRect.Call(uintptr(hwnd), 0, 1)
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
	w.navHistoryR = rectT{navX, w.navHomeR.bottom + p(6), navX + navW, w.navHomeR.bottom + p(6) + navH}
	w.navDevicesR = rectT{navX, w.navHistoryR.bottom + p(6), navX + navW, w.navHistoryR.bottom + p(6) + navH}
	w.navSetR = rectT{navX, w.navDevicesR.bottom + p(6), navX + navW, w.navDevicesR.bottom + p(6) + navH}

	w.syncLANTargets()

	// Reset page-specific rects
	w.netBannerR, w.netRetryR = rectT{}, rectT{}
	w.targetPillRects = w.targetPillRects[:0]
	w.targetPillItems = w.targetPillItems[:0]
	w.dropZoneR, w.clipBarR, w.latestBarR, w.allHistoryLinkR = rectT{}, rectT{}, rectT{}, rectT{}
	w.coldReadyR, w.coldLeftCardR, w.coldRightCardR = rectT{}, rectT{}, rectT{}
	w.tileFilesR, w.tileFolderR, w.queueBoxR, w.queueClearR = rectT{}, rectT{}, rectT{}, rectT{}
	w.homeScanLanR, w.addDevLinkR, w.peerPickerR, w.sendBtnR, w.sendTextR, w.sendClipR, w.tempShareBtnR = rectT{}, rectT{}, rectT{}, rectT{}, rectT{}, rectT{}, rectT{}
	w.fltAllR, w.fltSendR, w.fltRecvR, w.openDirR, w.activeTxR, w.txPauseBtnR, w.txCancelBtnR = rectT{}, rectT{}, rectT{}, rectT{}, rectT{}, rectT{}, rectT{}
	w.ledgerRowsR = w.ledgerRowsR[:0]
	w.devScanLanBtnR, w.devAddBtnR = rectT{}, rectT{}
	w.devSendBtnsR = w.devSendBtnsR[:0]
	w.devRenameBtnsR = w.devRenameBtnsR[:0]
	w.devDeleteBtnsR = w.devDeleteBtnsR[:0]
	w.devLanSendBtnsR = w.devLanSendBtnsR[:0]
	w.devLanSelectBtnsR = w.devLanSelectBtnsR[:0]
	w.setRecvBtnR, w.setResetDirR, w.setChangeDirR, w.setOpenDirR, w.setAutoBtnR, w.setResetSrvR, w.setChangeSrvR, w.setRenameBtnR, w.setCopyIDRowR, w.aboutRepoR = rectT{}, rectT{}, rectT{}, rectT{}, rectT{}, rectT{}, rectT{}, rectT{}, rectT{}, rectT{}

	switch w.page {
	case pageHome:
		pad := p(28)
		paneL := rw + pad
		paneR := cw - pad
		maxW := p(780)
		if paneR-paneL > maxW {
			extra := (paneR - paneL - maxW) / 2
			paneL += extra
			paneR -= extra
		}

		topY := topH + p(20)
		sigKind := "offline"
		if w.app != nil {
			_, sigKind = w.app.signalHomeStatus()
		}
		if sigKind != "online" {
			w.netBannerR = rectT{paneL, topY, paneR, topY + p(36)}
			w.netRetryR = rectT{paneR - p(82), topY + p(5), paneR - p(8), topY + p(31)}
			topY = w.netBannerR.bottom + p(12)
		}

		var tx *activeTransfer
		if w.app != nil {
			tx = w.app.currentTransfer()
		}

		if !w.hasPeers() && tx == nil {
			// Cold Start: Readiness pill + Value-first Temp Share / LAN Left + Pairing Right
			w.coldReadyR = rectT{paneL, topY, paneR, topY + p(32)}
			cardsTop := w.coldReadyR.bottom + p(14)
			cardsBot := ch - p(36)
			if cardsBot < cardsTop+p(200) {
				cardsBot = cardsTop + p(200)
			}
			gap := p(16)
			leftW := (paneR - paneL - gap) * 56 / 100
			w.coldLeftCardR = rectT{paneL, cardsTop, paneL + leftW, cardsBot}
			w.coldRightCardR = rectT{w.coldLeftCardR.right + gap, cardsTop, paneR, cardsBot}

			btnY := cardsBot - p(54)
			btnB := cardsBot - p(18)
			inPad := p(18)
			scanW := p(98)
			w.homeScanLanR = rectT{w.coldLeftCardR.right - inPad - scanW, btnY, w.coldLeftCardR.right - inPad, btnB}
			w.tempShareBtnR = rectT{w.coldLeftCardR.left + inPad, btnY, w.homeScanLanR.left - p(10), btnB}
			w.addDevLinkR = rectT{w.coldRightCardR.left + inPad, btnY, w.coldRightCardR.right - inPad, btnB}
			break
		}

		// Target device pill bar
		pillRowY := topY
		pillH := p(30)
		pillX := paneL + p(36)
		pillGap := p(8)
		tempLabel := "+ 临时扫码传"
		tempW := w.measureText(w.fontSm, tempLabel) + p(22)

		var allTargets []sendTarget
		for _, peer := range w.peers {
			allTargets = append(allTargets, sendTarget{peer: peer})
		}
		for _, lp := range w.lanTargets {
			allTargets = append(allTargets, sendTarget{isLAN: true, lan: lp})
		}
		for _, t := range allTargets {
			lbl := t.label()
			if w.targetReachability(t) == "lan" {
				lbl += " · 局域网"
			}
			pw := w.measureText(w.fontSm, lbl) + p(34)
			if pw > p(210) {
				pw = p(210)
			}
			if pillX+pw > paneR && pillX > paneL+p(36) {
				pillX = paneL + p(36)
				pillRowY += pillH + p(8)
			}
			w.targetPillItems = append(w.targetPillItems, t)
			w.targetPillRects = append(w.targetPillRects, rectT{pillX, pillRowY, pillX + pw, pillRowY + pillH})
			pillX += pw + pillGap
		}
		if pillX+tempW > paneR && pillX > paneL+p(36) {
			pillX = paneL + p(36)
			pillRowY += pillH + p(8)
		}
		w.tempShareBtnR = rectT{pillX, pillRowY, pillX + tempW, pillRowY + pillH}
		stageTop := pillRowY + pillH + p(14)

		if tx != nil {
			// Active Transfer takes center stage
			showPause := tx.canPause && tx.stage != "verifying"
			txH := p(116)
			if showPause || tx.canCancel {
				txH = p(144)
			}
			availH := ch - p(36) - stageTop
			ty := stageTop + (availH-txH)/3
			if ty < stageTop {
				ty = stageTop
			}
			w.activeTxR = rectT{paneL, ty, paneR, ty + txH}
			btnY := w.activeTxR.bottom - p(40)
			btnB := w.activeTxR.bottom - p(14)
			curR := paneR - p(16)
			if tx.canCancel {
				cwBtn := p(76)
				w.txCancelBtnR = rectT{curR - cwBtn, btnY, curR, btnB}
				curR = w.txCancelBtnR.left - p(8)
			}
			if showPause {
				pwBtn := p(76)
				if !tx.paused && (tx.peerPaused || tx.stage == "peer_paused") {
					pwBtn = p(94)
				}
				w.txPauseBtnR = rectT{curR - pwBtn, btnY, curR, btnB}
			}
			break
		}

		// Idle Hero Stage: Drop Zone + Single-Purpose Clipboard Bar + Latest-1 Footer
		bottomEdge := ch - p(32)
		hasRecent := w.app != nil && len(w.app.recentLines(1)) > 0
		aboveLatest := bottomEdge
		if hasRecent {
			w.latestBarR = rectT{paneL, bottomEdge - p(34), paneR, bottomEdge}
			w.allHistoryLinkR = rectT{paneR - p(74), w.latestBarR.top, paneR, w.latestBarR.bottom}
			w.openDirR = rectT{w.allHistoryLinkR.left - p(92), w.latestBarR.top, w.allHistoryLinkR.left - p(10), w.latestBarR.bottom}
			aboveLatest = w.latestBarR.top - p(10)
		}

		clipH := p(54)
		w.clipBarR = rectT{paneL, aboveLatest - clipH, paneR, aboveLatest}
		clipBtnLbl := "发送剪贴板"
		if w.clipPreview.kind == "image" {
			clipBtnLbl = "发送截图"
		}
		clipBtnW := w.measureText(w.fontBold, clipBtnLbl) + p(42)
		if clipBtnW < p(108) {
			clipBtnW = p(108)
		}
		w.sendClipR = rectT{paneR - p(14) - clipBtnW, w.clipBarR.top + p(11), paneR - p(14), w.clipBarR.bottom - p(11)}

		dzBottom := w.clipBarR.top - p(12)
		if dzBottom < stageTop+p(180) {
			dzBottom = stageTop + p(180)
		}
		w.dropZoneR = rectT{paneL, stageTop, paneR, dzBottom}

		dzMidX := (w.dropZoneR.left + w.dropZoneR.right) / 2
		dzH := w.dropZoneR.bottom - w.dropZoneR.top
		contentH := p(148)
		if len(w.queued) > 0 {
			contentH = p(190)
		}
		innerTop := w.dropZoneR.top + (dzH-contentH)/2
		if innerTop < w.dropZoneR.top+p(14) {
			innerTop = w.dropZoneR.top + p(14)
		}
		btnRowTop := innerTop + p(112)
		if len(w.queued) > 0 {
			qw := p(360)
			if qw > (w.dropZoneR.right-w.dropZoneR.left)-p(40) {
				qw = (w.dropZoneR.right - w.dropZoneR.left) - p(40)
			}
			qy := innerTop + p(106)
			w.queueBoxR = rectT{dzMidX - qw/2, qy, dzMidX + qw/2, qy + p(30)}
			w.queueClearR = rectT{w.queueBoxR.right - p(46), qy, w.queueBoxR.right, qy + p(30)}
			btnRowTop = w.queueBoxR.bottom + p(14)
		}

		b1W := p(138)
		b2W := p(98)
		b3W := p(86)
		bGap := p(10)
		totalBW := b1W + b2W + b3W + bGap*2
		bx := dzMidX - totalBW/2
		btnH := p(36)
		w.sendBtnR = rectT{bx, btnRowTop, bx + b1W, btnRowTop + btnH}
		w.tileFolderR = rectT{w.sendBtnR.right + bGap, btnRowTop, w.sendBtnR.right + bGap + b2W, btnRowTop + btnH}
		w.sendTextR = rectT{w.tileFolderR.right + bGap, btnRowTop, w.tileFolderR.right + bGap + b3W, btnRowTop + btnH}

	case pageHistory:
		pad := p(28)
		paneL := rw + pad
		paneR := cw - pad
		w.openDirR = rectT{paneR - p(108), topH + p(24), paneR, topH + p(52)}
		tabY := topH + p(62)
		w.fltAllR = rectT{paneL, tabY, paneL + p(42), tabY + p(28)}
		w.fltSendR = rectT{w.fltAllR.right + p(16), tabY, w.fltAllR.right + p(68), tabY + p(28)}
		w.fltRecvR = rectT{w.fltSendR.right + p(16), tabY, w.fltSendR.right + p(68), tabY + p(28)}

		ry := w.fltAllR.bottom + p(8)
		if w.app != nil {
			if tx := w.app.currentTransfer(); tx != nil {
				showPause := tx.canPause && tx.stage != "verifying"
				h := p(92)
				if showPause || tx.canCancel {
					h = p(122)
				}
				w.activeTxR = rectT{paneL, ry, paneR, ry + h}
				btnY := w.activeTxR.bottom - p(32)
				btnB := w.activeTxR.bottom - p(10)
				curR := paneR - p(14)
				if tx.canCancel {
					cwBtn := p(66)
					w.txCancelBtnR = rectT{curR - cwBtn, btnY, curR, btnB}
					curR = w.txCancelBtnR.left - p(8)
				}
				if showPause {
					pwBtn := p(66)
					if !tx.paused && (tx.peerPaused || tx.stage == "peer_paused") {
						pwBtn = p(86)
					}
					w.txPauseBtnR = rectT{curR - pwBtn, btnY, curR, btnB}
				}
				ry = w.activeTxR.bottom + p(10)
			}
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
		w.devScanLanBtnR = rectT{w.devAddBtnR.left - p(10) - p(98), topH + p(24), w.devAddBtnR.left - p(10), topH + p(58)}
		y := topH + p(94)
		rowH := p(62)
		btnGap := p(8)
		maxY := ch - p(36)
		if len(w.peers) == 0 {
			y += p(68)
		} else {
			for range w.peers {
				if y+rowH > maxY {
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
		}
		lanY := y + p(18) + p(46)
		for range w.lanTargets {
			if lanY+rowH > maxY {
				break
			}
			topB := lanY + p(15)
			botB := lanY + rowH - p(15)
			selR := rectT{paneR - p(82), topB, paneR - p(16), botB}
			sendR := rectT{selR.left - btnGap - p(74), topB, selR.left - btnGap, botB}
			w.devLanSelectBtnsR = append(w.devLanSelectBtnsR, selR)
			w.devLanSendBtnsR = append(w.devLanSendBtnsR, sendR)
			lanY += rowH
		}

	case pageSettings:
		pad := p(28)
		paneL := rw + pad
		paneR := cw - pad
		rowH := p(50)
		y := topH + p(72)
		w.setRecvBtnR = rectT{paneR - p(88), y + p(10), paneR - p(16), y + rowH - p(10)}
		y += rowH
		btnTop := y + p(10)
		btnBot := y + rowH - p(10)
		btnGap := p(8)
		w.setOpenDirR = rectT{paneR - p(72), btnTop, paneR - p(16), btnBot}
		w.setChangeDirR = rectT{w.setOpenDirR.left - btnGap - p(56), btnTop, w.setOpenDirR.left - btnGap, btnBot}
		if w.app != nil && w.app.isCustomReceiveDir() {
			w.setResetDirR = rectT{w.setChangeDirR.left - btnGap - p(68), btnTop, w.setChangeDirR.left - btnGap, btnBot}
		}
		y += rowH
		w.setAutoBtnR = rectT{paneR - p(88), y + p(10), paneR - p(16), y + rowH - p(10)}
		y += rowH
		srvTop := y + p(10)
		srvBot := y + rowH - p(10)
		w.setChangeSrvR = rectT{paneR - p(56), srvTop, paneR - p(16), srvBot}
		if w.app != nil && w.app.isCustomServerConfig() {
			w.setResetSrvR = rectT{w.setChangeSrvR.left - btnGap - p(68), srvTop, w.setChangeSrvR.left - btnGap, srvBot}
		} else {
			w.setResetSrvR = rectT{}
		}
		infoTop := y + rowH + p(38)
		infoRowH := p(36)
		w.setRenameBtnR = rectT{paneR - p(70), infoTop + p(5), paneR - p(16), infoTop + infoRowH - p(5)}
		infoY := infoTop + infoRowH
		w.setCopyIDRowR = rectT{paneL, infoY, paneR, infoY + infoRowH}
		aboutTop := infoTop + infoRowH*3 + p(34)
		aboutH := p(46)
		w.aboutRepoR = rectT{paneR - p(240), aboutTop + p(6), paneR - p(16), aboutTop + aboutH - p(6)}
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
	w.syncUnreadHistory()
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
	case pageHistory:
		w.drawHistory(cv, cr)
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
	drawBrandBoatSDF(cv, markR, col(255, 255, 255))
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
		{w.navHistoryR, pageHistory, hoverNavHistory, "记录", 1},
		{w.navDevicesR, pageDevices, hoverNavDevices, "设备", 2},
		{w.navSetR, pageSettings, hoverNavSettings, "设置", 3},
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
		iconSz := w.px(20)
		iconX := (it.r.left + it.r.right - iconSz) / 2
		iconR := rectT{iconX, it.r.top + w.px(6), iconX + iconSz, it.r.top + w.px(6) + iconSz}
		w.drawNavIcon(cv, iconR, it.kind, fg)
		if it.page == pageHistory && w.unreadHistory && !active {
			ds := w.px(6)
			dx := iconR.right - w.px(2)
			dy := iconR.top
			cv.drawAlphaRoundRect(rectT{dx, dy, dx + ds, dy + ds}, float64(ds)/2.0, col(0x10, 0xB9, 0x81), 255, 0, 0, 0)
		}
		lblR := rectT{it.r.left, it.r.top + w.px(28), it.r.right, it.r.bottom - w.px(4)}
		cv.drawAlphaText(w.fontSm, it.label, lblR, dtCenter|dtSingleLine|dtVCenter, fg)
	}
}

func (w *mainWin) drawNavIcon(cv *glassCanvas, r rectT, kind int, color uint32) {
	drawNavIconSDF(cv, r, kind, color)
}

func (w *mainWin) drawHome(cv *glassCanvas, cr rectT) {
	p := w.px
	tileA := w.surfaceAlpha(205)

	// Determine pane horizontal bounds matching layout()
	paneL := w.railR.right + p(28)
	paneR := cr.right - p(28)
	maxW := p(780)
	if paneR-paneL > maxW {
		extra := (paneR - paneL - maxW) / 2
		paneL += extra
		paneR -= extra
	}

	// 1. Progressive Disclosure Network Banner (shown ONLY when signaling != online)
	if w.netBannerR.bottom > w.netBannerR.top {
		warnBg := col(0xFE, 0xF3, 0xC7)
		if w.dark {
			warnBg = col(0x45, 0x1A, 0x03)
		}
		cv.drawAlphaRoundRect(w.netBannerR, float64(p(10)), warnBg, w.surfaceAlpha(235), w.pal.amber, w.surfaceAlpha(120), 1.0)
		msg := "云端信令未连接，同局域网设备仍可高速直传"
		if w.app != nil {
			if _, kind := w.app.signalHomeStatus(); kind == "connecting" {
				msg = "正在连接云端信令服务器，同局域网设备仍可直传"
			}
		}
		cv.drawAlphaText(w.fontSm, msg,
			rectT{w.netBannerR.left + p(14), w.netBannerR.top, w.netRetryR.left - p(10), w.netBannerR.bottom},
			dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.amber)

		rBg := w.pal.paper
		if w.hover == hoverNetRetry {
			rBg = w.pal.paper2
		}
		cv.drawAlphaRoundRect(w.netRetryR, float64(p(7)), rBg, 255, w.pal.amber, 160, 1.0)
		cv.drawAlphaText(w.fontBold, "重试连接", w.netRetryR, dtCenter|dtSingleLine|dtVCenter, w.pal.ink)
	}

	var tx *activeTransfer
	if w.app != nil {
		tx = w.app.currentTransfer()
	}

	// 2. Cold Start State (no paired/LAN devices discovered yet and no active transfer)
	if !w.hasPeers() && tx == nil {
		// Readiness pill (Goal Gradient: never 0%)
		readyTxt := fmt.Sprintf("本机已就绪（%s · 加密通道与局域网监听已开启）", w.hostName)
		pillW := textWidth(cv.memDC, w.fontSm, readyTxt) + p(38)
		if pillW > paneR-paneL {
			pillW = paneR - paneL
		}
		readyR := rectT{paneL, w.coldReadyR.top, paneL + pillW, w.coldReadyR.bottom}
		cv.drawAlphaRoundRect(readyR, float64(p(16)), w.pal.accentSoft, w.surfaceAlpha(240), 0, 0, 0)
		dotSz := p(7)
		dotX := readyR.left + p(12)
		dotY := readyR.top + (readyR.bottom-readyR.top-dotSz)/2
		cv.drawAlphaRoundRect(rectT{dotX, dotY, dotX + dotSz, dotY + dotSz}, float64(dotSz)/2.0, col(0x10, 0xB9, 0x81), 255, 0, 0, 0)
		cv.drawAlphaText(w.fontSm, readyTxt,
			rectT{dotX + dotSz + p(8), readyR.top, readyR.right - p(10), readyR.bottom},
			dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.accent)

		// Left Card: Value Before Wall (Temp Share & LAN Discovery)
		cv.drawAlphaRoundRect(w.coldLeftCardR, float64(p(14)), w.pal.paper2, tileA, w.pal.line, w.surfaceAlpha(210), 1.0)
		lMidX := (w.coldLeftCardR.left + w.coldLeftCardR.right) / 2
		iconBox := rectT{lMidX - p(24), w.coldLeftCardR.top + p(28), lMidX + p(24), w.coldLeftCardR.top + p(76)}
		cv.drawAlphaRoundRect(iconBox, float64(p(14)), w.pal.accentSoft, w.surfaceAlpha(240), 0, 0, 0)
		drawUploadSDF(cv, rectT{iconBox.left + p(10), iconBox.top + p(10), iconBox.right - p(10), iconBox.bottom - p(10)}, w.pal.accent)

		cv.drawAlphaText(w.fontH2, "免配对直接体验",
			rectT{w.coldLeftCardR.left + p(18), iconBox.bottom + p(14), w.coldLeftCardR.right - p(18), iconBox.bottom + p(38)},
			dtCenter|dtSingleLine|dtVCenter, w.pal.ink)
		cv.drawAlphaText(w.fontSm, "直接拖入文件，或生成临时网页二维码让对方浏览器扫码下载，也可以搜索同 Wi-Fi 设备免配对互传。",
			rectT{w.coldLeftCardR.left + p(24), iconBox.bottom + p(44), w.coldLeftCardR.right - p(24), w.tempShareBtnR.top - p(14)},
			dtCenter|dtWordBreak, w.pal.ink2)

		tsFill := w.pal.accent
		if w.hover == hoverTempShare {
			tsFill = shade(tsFill, w.dark, 0.12)
		}
		cv.drawAlphaRoundRect(w.tempShareBtnR, float64(p(10)), tsFill, 255, 0, 0, 0)
		cv.drawAlphaText(w.fontBold, "临时快传（二维码 / 网页）", w.tempShareBtnR, dtCenter|dtSingleLine|dtVCenter, w.pal.accentInk)

		scanFill := w.pal.paper
		scanEdge := w.pal.line
		if w.hover == hoverHomeScanLan {
			scanFill = w.pal.paper3
			scanEdge = w.pal.accent
		}
		scanLbl := "搜索局域网"
		if w.app != nil && w.app.isLANScanning() {
			scanLbl = "搜索中…"
		}
		cv.drawAlphaRoundRect(w.homeScanLanR, float64(p(10)), scanFill, 255, scanEdge, 220, 1.0)
		cv.drawAlphaText(w.fontSm, scanLbl, w.homeScanLanR, dtCenter|dtSingleLine|dtVCenter, w.pal.ink)

		// Right Card: Pair Device
		cv.drawAlphaRoundRect(w.coldRightCardR, float64(p(14)), w.pal.paper2, tileA, w.pal.line, w.surfaceAlpha(210), 1.0)
		rMidX := (w.coldRightCardR.left + w.coldRightCardR.right) / 2
		rIconBox := rectT{rMidX - p(24), w.coldRightCardR.top + p(28), rMidX + p(24), w.coldRightCardR.top + p(76)}
		cv.drawAlphaRoundRect(rIconBox, float64(p(14)), w.pal.paper, 255, w.pal.line, 220, 1.0)
		drawNavIconSDF(cv, rectT{rIconBox.left + p(10), rIconBox.top + p(10), rIconBox.right - p(10), rIconBox.bottom - p(10)}, 2, w.pal.ink2)

		cv.drawAlphaText(w.fontH2, "添加常用设备",
			rectT{w.coldRightCardR.left + p(18), rIconBox.bottom + p(14), w.coldRightCardR.right - p(18), rIconBox.bottom + p(38)},
			dtCenter|dtSingleLine|dtVCenter, w.pal.ink)
		cv.drawAlphaText(w.fontSm, "与自己的手机或其他电脑配对一次，后续支持跨网络加密穿透与后台免确认自动接收。",
			rectT{w.coldRightCardR.left + p(20), rIconBox.bottom + p(44), w.coldRightCardR.right - p(20), w.addDevLinkR.top - p(14)},
			dtCenter|dtWordBreak, w.pal.ink2)

		addFill := w.pal.paper
		addEdge := w.pal.accent
		addFg := w.pal.accent
		if w.hover == hoverAddDevice {
			addFill = w.pal.accentSoft
		}
		cv.drawAlphaRoundRect(w.addDevLinkR, float64(p(10)), addFill, 255, addEdge, 220, 1.0)
		cv.drawAlphaText(w.fontBold, "扫码配对设备", w.addDevLinkR, dtCenter|dtSingleLine|dtVCenter, addFg)

		if w.status != "" {
			cv.drawAlphaText(w.fontSm, w.status, rectT{paneL, cr.bottom - p(30), paneR, cr.bottom - p(8)},
				dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)
		}
		return
	}

	// 3. Target Device Pill Row
	if len(w.targetPillRects) > 0 || w.tempShareBtnR.right > w.tempShareBtnR.left {
		rowTop := w.tempShareBtnR.top
		if len(w.targetPillRects) > 0 {
			rowTop = w.targetPillRects[0].top
		}
		cv.drawAlphaText(w.fontSm, "发给", rectT{paneL, rowTop, paneL + p(34), rowTop + p(30)},
			dtSingleLine|dtVCenter, w.pal.ink2)
	}
	curTarget, hasCur := w.activeTarget()
	for i, pr := range w.targetPillRects {
		t := w.targetPillItems[i]
		selected := hasCur && targetsEqual(curTarget, t)
		hovered := w.hover == hoverTargetPill && w.hoverIdx == i
		bg, edge, fg := w.pal.paper2, w.pal.line, w.pal.ink
		if selected {
			bg, edge, fg = w.pal.accentSoft, w.pal.accent, w.pal.accent
		} else if hovered {
			bg = w.pal.paper3
		}
		cv.drawAlphaRoundRect(pr, float64(p(15)), bg, tileA, edge, w.surfaceAlpha(220), 1.0)

		reach := w.targetReachability(t)
		dotCol := w.pal.ink3
		switch reach {
		case "lan":
			dotCol = col(0x10, 0xB9, 0x81)
		case "cloud":
			dotCol = w.pal.accent
		}
		dotSz := p(7)
		dotX := pr.left + p(11)
		dotY := pr.top + (pr.bottom-pr.top-dotSz)/2
		cv.drawAlphaRoundRect(rectT{dotX, dotY, dotX + dotSz, dotY + dotSz}, float64(dotSz)/2.0, dotCol, 255, 0, 0, 0)

		lbl := t.label()
		if reach == "lan" {
			lbl += " · 局域网"
		}
		font := w.fontSm
		if selected {
			font = w.fontBold
		}
		cv.drawAlphaText(font, lbl, rectT{dotX + dotSz + p(7), pr.top, pr.right - p(10), pr.bottom},
			dtSingleLine|dtVCenter|dtEndEllipsis, fg)
	}

	tsFill, tsEdge, tsFg := w.pal.paper, w.pal.line, w.pal.ink2
	if w.hover == hoverTempShare {
		tsFill, tsEdge, tsFg = w.pal.accentSoft, w.pal.accent, w.pal.accent
	}
	cv.drawAlphaRoundRect(w.tempShareBtnR, float64(p(15)), tsFill, tileA, tsEdge, w.surfaceAlpha(200), 1.0)
	cv.drawAlphaText(w.fontSm, "+ 临时扫码传", w.tempShareBtnR, dtCenter|dtSingleLine|dtVCenter, tsFg)

	// 4. Active Transfer Focus
	if tx != nil && w.activeTxR.bottom > w.activeTxR.top {
		w.drawActiveTransferCard(cv, w.activeTxR, tx)
		if w.status != "" {
			cv.drawAlphaText(w.fontSm, w.status, rectT{paneL, cr.bottom - p(30), paneR, cr.bottom - p(8)},
				dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)
		}
		return
	}

	// 5. Idle Hero Dashed Drop Zone
	cv.drawAlphaRoundRect(w.dropZoneR, float64(p(16)), w.pal.paper2, w.surfaceAlpha(185), 0, 0, 0)
	drawDashedRoundRect(cv, w.dropZoneR, float64(p(16)), w.pal.line, w.surfaceAlpha(240), 1.5, float64(p(7)), float64(p(5)))

	dzMidX := (w.dropZoneR.left + w.dropZoneR.right) / 2
	dzH := w.dropZoneR.bottom - w.dropZoneR.top
	contentH := p(148)
	if len(w.queued) > 0 {
		contentH = p(190)
	}
	innerTop := w.dropZoneR.top + (dzH-contentH)/2
	if innerTop < w.dropZoneR.top+p(14) {
		innerTop = w.dropZoneR.top + p(14)
	}

	iconBox := rectT{dzMidX - p(23), innerTop, dzMidX + p(23), innerTop + p(46)}
	cv.drawAlphaRoundRect(iconBox, float64(p(13)), w.pal.accentSoft, w.surfaceAlpha(240), 0, 0, 0)
	drawUploadSDF(cv, rectT{iconBox.left + p(10), iconBox.top + p(10), iconBox.right - p(10), iconBox.bottom - p(10)}, w.pal.accent)

	headline := "拖入文件或文件夹，直接发送"
	subline := "端到端加密传输"
	if hasCur {
		headline = "拖入文件或文件夹，发往 " + curTarget.label()
		switch w.targetReachability(curTarget) {
		case "lan":
			subline = "端到端加密 · 已自动就绪同局域网高速直连"
		case "cloud":
			subline = "端到端加密 · 云端在线，将自动协商最优路径"
		default:
			subline = "对方当前离线 · 上线后可送达，或使用「临时扫码传」"
		}
	}
	cv.drawAlphaText(w.fontH2, headline,
		rectT{w.dropZoneR.left + p(24), iconBox.bottom + p(10), w.dropZoneR.right - p(24), iconBox.bottom + p(36)},
		dtCenter|dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink)
	cv.drawAlphaText(w.fontSm, subline,
		rectT{w.dropZoneR.left + p(24), iconBox.bottom + p(36), w.dropZoneR.right - p(24), iconBox.bottom + p(58)},
		dtCenter|dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)

	if len(w.queued) > 0 && w.queueBoxR.bottom > w.queueBoxR.top {
		cv.drawAlphaRoundRect(w.queueBoxR, float64(p(8)), w.pal.paper, 255, w.pal.line, 220, 1.0)
		qLbl := fmt.Sprintf("已选 %s", summarizeFiles(w.queued))
		cv.drawAlphaText(w.fontSm, qLbl,
			rectT{w.queueBoxR.left + p(10), w.queueBoxR.top, w.queueClearR.left - p(6), w.queueBoxR.bottom},
			dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink)
		clrFg := w.pal.ink2
		if w.hover == hoverQueueClear {
			clrFg = w.pal.danger
		}
		cv.drawAlphaText(w.fontSm, "清除", w.queueClearR, dtCenter|dtSingleLine|dtVCenter, clrFg)
	}

	// Action Row inside Hero Drop Zone: [选择文件并发送] [发文件夹] [发文字]
	sendFill := w.pal.accent
	if w.hover == hoverSendBtn {
		sendFill = shade(sendFill, w.dark, 0.12)
	}
	cv.drawAlphaRoundRect(w.sendBtnR, float64(p(10)), sendFill, 255, 0, 0, 0)
	sendLbl := "选择文件并发送"
	if len(w.queued) > 0 {
		sendLbl = "立即发送"
	}
	cv.drawAlphaText(w.fontBold, sendLbl, w.sendBtnR, dtCenter|dtSingleLine|dtVCenter, w.pal.accentInk)

	fldFill, fldEdge := w.pal.paper, w.pal.line
	if w.hover == hoverTileFolder {
		fldFill, fldEdge = w.pal.paper3, w.pal.accent
	}
	cv.drawAlphaRoundRect(w.tileFolderR, float64(p(10)), fldFill, 255, fldEdge, 220, 1.0)
	cv.drawAlphaText(w.fontSm, "发文件夹", w.tileFolderR, dtCenter|dtSingleLine|dtVCenter, w.pal.ink)

	txtFill, txtEdge := w.pal.paper, w.pal.line
	if w.hover == hoverSendText {
		txtFill, txtEdge = w.pal.paper3, w.pal.accent
	}
	cv.drawAlphaRoundRect(w.sendTextR, float64(p(10)), txtFill, 255, txtEdge, 220, 1.0)
	cv.drawAlphaText(w.fontSm, "发文字", w.sendTextR, dtCenter|dtSingleLine|dtVCenter, w.pal.ink)

	// 6. Single-Purpose Smart Clipboard Bar (NO arrows, NO unrelated buttons)
	cv.drawAlphaRoundRect(w.clipBarR, float64(p(12)), w.pal.paper2, tileA, w.pal.line, w.surfaceAlpha(210), 1.0)
	cIconR := rectT{w.clipBarR.left + p(12), w.clipBarR.top + p(11), w.clipBarR.left + p(44), w.clipBarR.bottom - p(11)}
	cv.drawAlphaRoundRect(cIconR, float64(p(8)), w.pal.accentSoft, w.surfaceAlpha(235), 0, 0, 0)
	drawClipSDF(cv, rectT{cIconR.left + p(6), cIconR.top + p(6), cIconR.right - p(6), cIconR.bottom - p(6)}, w.pal.accent)

	clipTitle := "剪贴板快捷发送"
	clipSub := "复制截图或文本后，点右侧一键发往对方"
	clipActionLbl := "发送剪贴板"
	if w.clipPreview.kind != "" {
		clipTitle = w.clipPreview.title
		clipSub = w.clipPreview.sub
		if w.clipPreview.kind == "image" {
			clipActionLbl = "发送截图"
		}
	}
	cv.drawAlphaText(w.fontBold, clipTitle,
		rectT{cIconR.right + p(10), w.clipBarR.top + p(7), w.sendClipR.left - p(10), w.clipBarR.top + p(27)},
		dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink)
	cv.drawAlphaText(w.fontSm, clipSub,
		rectT{cIconR.right + p(10), w.clipBarR.top + p(27), w.sendClipR.left - p(10), w.clipBarR.bottom - p(7)},
		dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink3)

	cbFill := w.pal.accentSoft
	cbFg := w.pal.accent
	if w.hover == hoverSendClip {
		cbFill = w.pal.accent
		cbFg = w.pal.accentInk
	}
	cv.drawAlphaRoundRect(w.sendClipR, float64(p(9)), cbFill, 255, 0, 0, 0)
	sendIconR := rectT{w.sendClipR.left + p(11), w.sendClipR.top + p(8), w.sendClipR.left + p(27), w.sendClipR.bottom - p(8)}
	drawSendSDF(cv, sendIconR, cbFg)
	cv.drawAlphaText(w.fontBold, clipActionLbl,
		rectT{sendIconR.right + p(4), w.sendClipR.top, w.sendClipR.right - p(10), w.sendClipR.bottom},
		dtCenter|dtSingleLine|dtVCenter, cbFg)

	// 7. Compact Latest-1 Activity Footer (no table!)
	if w.latestBarR.bottom > w.latestBarR.top && w.app != nil {
		if lines := w.app.recentLines(1); len(lines) > 0 {
			r := parseLedgerLine(lines[0])
			cv.fillColorAlpha(rectT{paneL, w.latestBarR.top - p(4), paneR, w.latestBarR.top - p(3)}, w.pal.lineSoft, w.surfaceAlpha(200))
			tagR := rectT{paneL, w.latestBarR.top + p(6), paneL + p(38), w.latestBarR.bottom - p(6)}
			cv.drawAlphaRoundRect(tagR, float64(p(5)), w.pal.paper3, 255, 0, 0, 0)
			cv.drawAlphaText(w.fontSm, "最近", tagR, dtCenter|dtSingleLine|dtVCenter, w.pal.ink2)

			verb := "刚收到"
			if r.dir == "发" {
				verb = "刚发出"
			}
			fname := r.files
			if fname == "" {
				fname = r.peer
			}
			summary := fmt.Sprintf("%s · %s", verb, fname)
			if r.size != "" || r.peer != "" {
				var meta []string
				if r.size != "" {
					meta = append(meta, r.size)
				}
				if r.peer != "" {
					meta = append(meta, r.peer)
				}
				summary += "（" + strings.Join(meta, " · ") + "）"
			}
			cv.drawAlphaText(w.fontSm, summary,
				rectT{tagR.right + p(8), w.latestBarR.top, w.openDirR.left - p(8), w.latestBarR.bottom},
				dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink)

			openClr := w.pal.ink2
			if w.hover == hoverOpenDir {
				openClr = w.pal.accent
			}
			cv.drawAlphaText(w.fontSm, "打开接收目录", w.openDirR, dtRight|dtSingleLine|dtVCenter, openClr)

			allClr := w.pal.accent
			if w.hover == hoverAllHistory {
				allClr = shade(allClr, w.dark, 0.14)
			}
			cv.drawAlphaText(w.fontBold, "全部记录 ›", w.allHistoryLinkR, dtRight|dtSingleLine|dtVCenter, allClr)
		}
	}

	if w.status != "" {
		cv.drawAlphaText(w.fontSm, w.status, rectT{paneL, cr.bottom - p(28), paneR, cr.bottom - p(6)},
			dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)
	}
}

func (w *mainWin) drawHistory(cv *glassCanvas, cr rectT) {
	p := w.px
	topH := w.titleBarR.bottom
	paneL := w.railR.right + p(28)
	paneR := cr.right - p(28)

	cv.drawAlphaText(w.fontH1, "传输记录", rectT{paneL, topH + p(20), w.openDirR.left - p(12), topH + p(54)},
		dtSingleLine|dtVCenter, w.pal.ink)

	openClr := w.pal.ink2
	if w.hover == hoverOpenDir {
		openClr = w.pal.accent
	}
	cv.drawAlphaText(w.fontSm, "打开接收文件夹", w.openDirR, dtRight|dtSingleLine|dtVCenter, openClr)

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
			if hovered && r.path != "失败" && r.path != "已取消" && r.path != "对方已取消" {
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
	localPaused := tx.paused || tx.stage == "paused"
	peerPaused := (tx.peerPaused || tx.stage == "peer_paused") && !localPaused
	anyPaused := localPaused || peerPaused
	accentCol := w.pal.accent
	if anyPaused {
		accentCol = w.pal.amber
	}
	cv.drawAlphaRoundRect(r, float64(p(12)), w.pal.paper, w.surfaceAlpha(215), accentCol, w.surfaceAlpha(160), 1.0)

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
			dtRight|dtSingleLine|dtVCenter, accentCol)
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
	if tx.bytesTotal == 0 && (tx.stage == "connecting" || tx.stage == "waiting" || tx.stage == "transferring" || anyPaused) {
		fillW = (barR.right - barR.left) * 28 / 100
	}
	if fillW > 0 {
		if fillW < p(6) {
			fillW = p(6)
		}
		if barR.left+fillW > barR.right {
			fillW = barR.right - barR.left
		}
		cv.drawAlphaRoundRect(rectT{barR.left, barR.top, barR.left + fillW, barR.bottom}, float64(p(3)), accentCol, 255, 0, 0, 0)
	}

	// Bottom file + bytes + elapsed/ETA metadata
	fileInfo := tx.fileName
	if tx.fileCount > 1 && tx.fileIndex > 0 {
		fileInfo = fmt.Sprintf("第 %d/%d 个 · %s", tx.fileIndex, tx.fileCount, tx.fileName)
	}
	if fileInfo == "" {
		fileInfo = "准备文件信息…"
	}
	pauseLabel := ""
	if localPaused {
		pauseLabel = "已暂停"
	} else if peerPaused {
		pauseLabel = "对方已暂停"
	}
	rightInfo := formatTransferMetricsWithLabel(tx.startedAt, tx.bytesDone, tx.bytesTotal, tx.speedBPS, pauseLabel)
	rw := textWidth(cv.memDC, w.fontSm, rightInfo) + p(8)
	maxRW := (inR - inL) * 62 / 100
	if rw > maxRW {
		rw = maxRW
	}
	metaTop := r.top + p(56)
	metaBot := r.top + p(80)
	cv.drawAlphaText(w.fontSm, fileInfo, rectT{inL, metaTop, inR - rw - p(8), metaBot},
		dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)
	cv.drawAlphaText(w.fontSm, rightInfo, rectT{inR - rw, metaTop, inR, metaBot},
		dtRight|dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)

	if w.txPauseBtnR.right > w.txPauseBtnR.left {
		pLabel := "暂停"
		pBg := w.pal.paper2
		pFg := w.pal.ink
		if localPaused {
			pLabel = "继续"
			if w.hover == hoverTxPause {
				pBg = w.pal.accentSoft
			}
		} else if peerPaused {
			pLabel = "对方已暂停"
			pBg = w.pal.paper3
			pFg = w.pal.ink3
		} else if w.hover == hoverTxPause {
			pBg = w.pal.accentSoft
		}
		cv.drawAlphaRoundRect(w.txPauseBtnR, float64(p(6)), pBg, w.surfaceAlpha(235), w.pal.line, w.surfaceAlpha(200), 1.0)
		cv.drawAlphaText(w.fontSm, pLabel, w.txPauseBtnR, dtCenter|dtSingleLine|dtVCenter, pFg)
	}
	if w.txCancelBtnR.right > w.txCancelBtnR.left {
		cBg := w.pal.paper2
		if w.hover == hoverTxCancel {
			cBg = w.pal.paper3
		}
		cv.drawAlphaRoundRect(w.txCancelBtnR, float64(p(6)), cBg, w.surfaceAlpha(235), w.pal.danger, w.surfaceAlpha(180), 1.0)
		cv.drawAlphaText(w.fontSm, "取消", w.txCancelBtnR, dtCenter|dtSingleLine|dtVCenter, w.pal.danger)
	}
}

func stageDisplayLabel(stage string) string {
	switch stage {
	case "connecting":
		return "连接中"
	case "waiting":
		return "等待对方确认"
	case "racing":
		return "协商直连路径"
	case "transferring":
		return "传输中"
	case "paused":
		return "已暂停"
	case "peer_paused":
		return "对方已暂停"
	case "cancelling":
		return "正在取消"
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
	case "网页快传", "web":
		return "网页快传", pal.accentSoft, pal.accent
	case "打洞", "punch", "udp 打洞":
		if dark {
			return "UDP 打洞", col(0x45, 0x1A, 0x03), pal.amber
		}
		return "UDP 打洞", col(0xFE, 0xF3, 0xC7), pal.amber
	case "中继", "relay", "服务器中继":
		return "服务器中继", pal.paper3, pal.ink2
	case "已取消", "cancelled", "canceled":
		if dark {
			return "已取消", col(0x45, 0x1A, 0x03), pal.amber
		}
		return "已取消", col(0xFE, 0xF3, 0xC7), pal.amber
	case "对方已取消":
		if dark {
			return "对方已取消", col(0x45, 0x1A, 0x03), pal.amber
		}
		return "对方已取消", col(0xFE, 0xF3, 0xC7), pal.amber
	case "失败", "failed", "error":
		if dark {
			return "失败", col(0x7F, 0x1D, 0x1D), pal.danger
		}
		return "失败", col(0xFE, 0xE2, 0xE2), pal.danger
	default:
		return "", 0, 0
	}
}

func formatSpeed(bps int64) string {
	switch {
	case bps <= 0:
		return ""
	case bps >= 1<<30:
		return fmt.Sprintf("%.2f GB/s", float64(bps)/(1<<30))
	case bps >= 1<<20:
		return fmt.Sprintf("%.1f MB/s", float64(bps)/(1<<20))
	case bps >= 1<<10:
		return fmt.Sprintf("%.1f KB/s", float64(bps)/(1<<10))
	default:
		return fmt.Sprintf("%d B/s", bps)
	}
}

func formatTransferMetrics(startedAt time.Time, done, total, speedBPS int64) string {
	return formatTransferMetricsState(startedAt, done, total, speedBPS, false)
}

func formatTransferMetricsState(startedAt time.Time, done, total, speedBPS int64, paused bool) string {
	pauseLabel := ""
	if paused {
		pauseLabel = "已暂停"
	}
	return formatTransferMetricsWithLabel(startedAt, done, total, speedBPS, pauseLabel)
}

func formatTransferMetricsWithLabel(startedAt time.Time, done, total, speedBPS int64, pauseLabel string) string {
	paused := pauseLabel != ""
	var parts []string
	if total > 0 {
		parts = append(parts, humanBytes(done)+" / "+humanBytes(total))
	} else if done > 0 {
		parts = append(parts, humanBytes(done))
	}
	var elapsed time.Duration
	if !startedAt.IsZero() {
		elapsed = time.Since(startedAt)
		if !paused && speedBPS <= 0 && done > 0 && elapsed >= 500*time.Millisecond {
			speedBPS = int64(float64(done) / elapsed.Seconds())
		}
	}
	if paused {
		parts = append(parts, pauseLabel)
	} else if speedBPS > 0 {
		parts = append(parts, formatSpeed(speedBPS))
	}
	if !startedAt.IsZero() {
		if elapsed < time.Second {
			elapsed = time.Second
		}
		parts = append(parts, fmt.Sprintf("已用 %ds", int(elapsed.Seconds())))
		if !paused {
			if speedBPS > 0 && total > 0 && done > 0 && done < total {
				remSec := int(float64(total-done)/float64(speedBPS) + 0.5)
				if remSec < 1 {
					remSec = 1
				}
				if remSec >= 60 {
					parts = append(parts, fmt.Sprintf("剩余约 %dm%ds", remSec/60, remSec%60))
				} else {
					parts = append(parts, fmt.Sprintf("剩余约 %ds", remSec))
				}
			} else if eta := formatETA(elapsed, done, total); eta != "" {
				parts = append(parts, eta)
			}
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
	drawFolderSDF(cv, r, color)
}

func (w *mainWin) drawDevices(cv *glassCanvas, cr rectT) {
	p := w.px
	topH := w.titleBarR.bottom
	pad := p(28)
	paneL := w.railR.right + pad
	paneR := cr.right - pad

	cv.drawAlphaText(w.fontH1, "设备", rectT{paneL, topH + p(20), w.devScanLanBtnR.left - p(12), topH + p(52)}, dtSingleLine|dtVCenter, w.pal.ink)
	cv.drawAlphaText(w.fontSm, "已配对设备免确认后台接收；附近局域网设备支持免配对临时发送。",
		rectT{paneL, topH + p(54), w.devScanLanBtnR.left - p(12), topH + p(76)},
		dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)

	scanFill := w.pal.paper2
	if w.hover == hoverDevScanLanBtn {
		scanFill = w.pal.paper3
	}
	cv.drawAlphaRoundRect(w.devScanLanBtnR, float64(p(8)), scanFill, w.surfaceAlpha(220), w.pal.line, w.surfaceAlpha(220), 1.0)
	scanBtnLbl := "搜索局域网"
	if w.app != nil && w.app.isLANScanning() {
		scanBtnLbl = "搜索中…"
	}
	cv.drawAlphaText(w.fontSm, scanBtnLbl, w.devScanLanBtnR, dtCenter|dtSingleLine|dtVCenter, w.pal.ink)

	addFill := w.pal.accent
	if w.hover == hoverDevAddBtn {
		addFill = shade(addFill, w.dark, 0.12)
	}
	cv.drawAlphaRoundRect(w.devAddBtnR, float64(p(8)), addFill, 255, addFill, 255, 0)
	cv.drawAlphaText(w.fontBold, "添加设备", w.devAddBtnR, dtCenter|dtSingleLine|dtVCenter, w.pal.accentInk)

	rowH := p(62)
	top := topH + p(94)
	pairedBot := top
	if len(w.peers) == 0 {
		pairedBot = top + p(68)
		box := rectT{paneL, top, paneR, pairedBot}
		cv.drawAlphaRoundRect(box, float64(p(12)), w.pal.paper2, w.surfaceAlpha(190), w.pal.line, w.surfaceAlpha(200), 1.0)
		cv.drawAlphaText(w.fontSm, "还没有已配对设备 · 点击右上角「添加设备」绑定，或下方直接向同局域网设备临时发送。",
			rectT{paneL + p(16), top, paneR - p(16), pairedBot}, dtCenter|dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)
	} else {
		pairedBot = top + int32(len(w.devSendBtnsR))*rowH
		box := rectT{paneL, top, paneR, pairedBot}
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
	}

	// Section 2: Unpaired LAN peers nearby (temporary LAN transfer)
	lanHdrY := pairedBot + p(18)
	lanTopY := lanHdrY + p(46)
	if lanTopY+p(52) <= cr.bottom-p(24) {
		cv.drawAlphaText(w.fontBold, "同局域网附近设备", rectT{paneL + p(2), lanHdrY, paneR, lanHdrY + p(22)},
			dtSingleLine|dtVCenter, w.pal.ink)
		cv.drawAlphaText(w.fontSm, "免配对临时传输 · 需对方打开轻渡主界面并点击确认接收",
			rectT{paneL + p(2), lanHdrY + p(22), paneR, lanHdrY + p(42)},
			dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)

		if len(w.lanTargets) == 0 {
			emptyBot := lanTopY + p(62)
			if emptyBot <= cr.bottom-p(24) {
				cv.drawAlphaRoundRect(rectT{paneL, lanTopY, paneR, emptyBot}, float64(p(12)),
					w.pal.paper2, w.surfaceAlpha(190), w.pal.line, w.surfaceAlpha(200), 1.0)
				cv.drawAlphaText(w.fontSm, "暂未发现其他未配对局域网设备，点击右上角「搜索局域网」刷新",
					rectT{paneL + p(16), lanTopY, paneR - p(16), emptyBot},
					dtCenter|dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)
			}
		} else if len(w.devLanSendBtnsR) > 0 {
			lanBot := lanTopY + int32(len(w.devLanSendBtnsR))*rowH
			cv.drawAlphaRoundRect(rectT{paneL, lanTopY, paneR, lanBot}, float64(p(12)),
				w.pal.paper, w.surfaceAlpha(195), w.pal.line, w.surfaceAlpha(200), 1.0)
			for i, br := range w.devLanSendBtnsR {
				ry := lanTopY + int32(i)*rowH
				entry := w.lanTargets[i]
				nameX := paneL + p(16)
				nameMaxR := br.left - p(12)
				badge := "局域网临时"
				badgeW := textWidth(cv.memDC, w.fontSm, badge) + p(14)
				nameW := textWidth(cv.memDC, w.fontBold, entry.name)
				nameLimitR := nameMaxR - badgeW - p(8)
				if nameLimitR < nameX+p(40) {
					nameLimitR = nameX + p(40)
				}
				cv.drawAlphaText(w.fontBold, entry.name,
					rectT{nameX, ry + p(9), nameLimitR, ry + p(31)},
					dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink)

				badgeL := nameX + nameW + p(8)
				if badgeL+badgeW > nameMaxR {
					badgeL = nameMaxR - badgeW
				}
				if badgeL > nameX+p(24) {
					badgeR := rectT{badgeL, ry + p(10), badgeL + badgeW, ry + p(30)}
					cv.drawAlphaRoundRect(badgeR, float64(p(10)), w.pal.accentSoft, w.surfaceAlpha(240), 0, 0, 0)
					cv.drawAlphaText(w.fontSm, badge, badgeR, dtCenter|dtSingleLine|dtVCenter, w.pal.accent)
				}

				sub := entry.addr
				if entry.iface != "" {
					sub += " · " + entry.iface
				}
				cv.drawAlphaText(w.fontSm, sub,
					rectT{paneL + p(16), ry + p(31), br.left - p(12), ry + p(52)},
					dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)

				bFill := w.pal.paper2
				if w.hover == hoverDevLanSendBtn && w.hoverIdx == i {
					bFill = w.pal.paper3
				}
				cv.drawAlphaRoundRect(br, float64(p(6)), bFill, w.surfaceAlpha(210), w.pal.line, w.surfaceAlpha(200), 1.0)
				cv.drawAlphaText(w.fontSm, "发送文件", br, dtCenter|dtSingleLine|dtVCenter, w.pal.ink)

				if i < len(w.devLanSelectBtnsR) {
					sr := w.devLanSelectBtnsR[i]
					sFill := w.pal.paper2
					sEdge := w.pal.line
					sFg := w.pal.ink
					if w.selectedLAN.id == entry.id {
						sFill = w.pal.accentSoft
						sEdge = w.pal.accent
						sFg = w.pal.accent
					} else if w.hover == hoverDevLanSelectBtn && w.hoverIdx == i {
						sFill = w.pal.paper3
					}
					sLbl := "选为目标"
					if w.selectedLAN.id == entry.id {
						sLbl = "已选中"
					}
					cv.drawAlphaRoundRect(sr, float64(p(6)), sFill, w.surfaceAlpha(210), sEdge, w.surfaceAlpha(200), 1.0)
					cv.drawAlphaText(w.fontSm, sLbl, sr, dtCenter|dtSingleLine|dtVCenter, sFg)
				}
				if i+1 < len(w.devLanSendBtnsR) {
					cv.fillColorAlpha(rectT{paneL + p(16), ry + rowH - 1, paneR - p(16), ry + rowH}, w.pal.lineSoft, w.surfaceAlpha(175))
				}
			}
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

	cv.drawAlphaText(w.fontH1, "设置", rectT{paneL, topH + p(16), paneR, topH + p(44)}, dtSingleLine|dtVCenter, w.pal.ink)
	cv.drawAlphaText(w.fontSm, "常规接收选项与本机技术信息。", rectT{paneL, topH + p(44), paneR, topH + p(64)},
		dtSingleLine|dtVCenter, w.pal.ink2)

	rowH := p(50)
	top := topH + p(72)
	g1 := rectT{paneL, top, paneR, top + rowH*4}
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
	srvText := "默认 · ferry.cwlindean.cn"
	portText := "18800"
	if w.app != nil {
		dirText = w.app.receiveDir()
		srvText = w.app.serverHostSummary()
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
	srvActions := make([]g1Action, 0, 2)
	if w.setResetSrvR.right > w.setResetSrvR.left {
		srvActions = append(srvActions, g1Action{"恢复默认", w.setResetSrvR, hoverSetResetSrv})
	}
	srvActions = append(srvActions, g1Action{"修改", w.setChangeSrvR, hoverSetChangeSrv})

	g1Rows := []struct {
		title, sub string
		actions    []g1Action
	}{
		{"后台接收文件", "关闭后其他设备暂时无法向此电脑发送", []g1Action{{recvTxt, w.setRecvBtnR, hoverSetRecv}}},
		{"接收文件保存位置", dirText, dirActions},
		{"开机自动启动", "登录后自动缩至右下角托盘接收", []g1Action{{autoTxt, w.setAutoBtnR, hoverSetAuto}}},
		{"服务器地址", srvText, srvActions},
	}
	for i, r := range g1Rows {
		ry := top + int32(i)*rowH
		textRight := paneR - p(16)
		if len(r.actions) > 0 {
			textRight = r.actions[0].r.left - p(12)
		}
		cv.drawAlphaText(w.fontBold, r.title,
			rectT{paneL + p(16), ry + p(6), textRight, ry + p(26)},
			dtSingleLine|dtVCenter, w.pal.ink)
		cv.drawAlphaText(w.fontSm, r.sub,
			rectT{paneL + p(16), ry + p(26), textRight, ry + p(44)},
			dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)

		for _, act := range r.actions {
			drawSmallBtn(act.r, act.label, act.hoverID)
		}
		if i+1 < len(g1Rows) {
			cv.fillColorAlpha(rectT{paneL + p(16), ry + rowH - 1, paneR - p(16), ry + rowH}, w.pal.lineSoft, w.surfaceAlpha(175))
		}
	}

	// Quiet technical info section
	lblY := g1.bottom + p(16)
	cv.drawAlphaText(w.fontBold, "本机信息", rectT{paneL + p(4), lblY, paneR, lblY + p(20)},
		dtSingleLine|dtVCenter, w.pal.ink2)

	infoRowH := p(36)
	infoTop := lblY + p(22)
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

	// About card tucked neatly inside Settings
	aboutLblY := g2.bottom + p(14)
	cv.drawAlphaText(w.fontBold, "关于", rectT{paneL + p(4), aboutLblY, paneR, aboutLblY + p(20)},
		dtSingleLine|dtVCenter, w.pal.ink2)

	aboutTop := aboutLblY + p(20)
	aboutH := p(46)
	g3 := rectT{paneL, aboutTop, paneR, aboutTop + aboutH}
	cv.drawAlphaRoundRect(g3, float64(p(12)), w.pal.paper, w.surfaceAlpha(195), w.pal.line, w.surfaceAlpha(200), 1.0)

	emSz := p(26)
	emX := paneL + p(16)
	emY := aboutTop + (aboutH-emSz)/2
	emR := rectT{emX, emY, emX + emSz, emY + emSz}
	cv.drawAlphaRoundRect(emR, float64(p(7)), w.pal.accent, 255, 0, 0, 0)
	drawBrandBoatSDF(cv, emR, col(255, 255, 255))

	cv.drawAlphaText(w.fontBold, "轻渡 Ferry", rectT{emR.right + p(10), aboutTop, emR.right + p(98), aboutTop + aboutH},
		dtSingleLine|dtVCenter, w.pal.ink)
	verTxt := "v" + strings.TrimPrefix(version, "v")
	cv.drawAlphaText(w.fontMono, verTxt, rectT{emR.right + p(98), aboutTop, w.aboutRepoR.left - p(10), aboutTop + aboutH},
		dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)

	repoColor := w.pal.accent
	if w.hover == hoverAboutRepo {
		repoColor = shade(w.pal.accent, w.dark, 0.14)
	}
	cv.drawAlphaText(w.fontSm, strings.TrimPrefix(ferryRepoURL, "https://"), w.aboutRepoR,
		dtRight|dtSingleLine|dtVCenter|dtEndEllipsis, repoColor)

	if w.status != "" {
		cv.drawAlphaText(w.fontSm, w.status,
			rectT{paneL, g3.bottom + p(10), paneR, cr.bottom - p(8)},
			dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)
	}
}

const ferryRepoURL = "https://github.com/zhitiaojun/ferry"

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
	case "lan", "lan 直连", "打洞", "punch", "udp 打洞", "中继", "relay", "服务器中继", "已取消", "对方已取消", "cancelled", "canceled", "失败", "failed", "error", "未配对":
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
	case ptIn(w.navHistoryR, x, y):
		kind = hoverNavHistory
	case ptIn(w.navDevicesR, x, y):
		kind = hoverNavDevices
	case ptIn(w.navSetR, x, y):
		kind = hoverNavSettings
	case w.page == pageHome && ptIn(w.netRetryR, x, y):
		kind = hoverNetRetry
	case w.page == pageHome && ptIn(w.tileFolderR, x, y):
		kind = hoverTileFolder
	case w.page == pageHome && len(w.queued) > 0 && ptIn(w.queueClearR, x, y):
		kind = hoverQueueClear
	case w.page == pageHome && ptIn(w.homeScanLanR, x, y):
		kind = hoverHomeScanLan
	case w.page == pageHome && ptIn(w.addDevLinkR, x, y):
		kind = hoverAddDevice
	case w.page == pageHome && ptIn(w.sendBtnR, x, y):
		kind = hoverSendBtn
	case w.page == pageHome && ptIn(w.sendTextR, x, y):
		kind = hoverSendText
	case w.page == pageHome && ptIn(w.sendClipR, x, y):
		kind = hoverSendClip
	case w.page == pageHome && ptIn(w.tempShareBtnR, x, y):
		kind = hoverTempShare
	case w.page == pageHome && ptIn(w.allHistoryLinkR, x, y):
		kind = hoverAllHistory
	case w.page == pageHome && ptIn(w.openDirR, x, y):
		kind = hoverOpenDir
	case w.page == pageHistory && ptIn(w.fltAllR, x, y):
		kind = hoverFltAll
	case w.page == pageHistory && ptIn(w.fltSendR, x, y):
		kind = hoverFltSend
	case w.page == pageHistory && ptIn(w.fltRecvR, x, y):
		kind = hoverFltRecv
	case w.page == pageHistory && ptIn(w.openDirR, x, y):
		kind = hoverOpenDir
	case (w.page == pageHome || w.page == pageHistory) && ptIn(w.txPauseBtnR, x, y):
		if w.app != nil {
			if tx := w.app.currentTransfer(); tx != nil && (tx.paused || !tx.peerPaused) {
				kind = hoverTxPause
			}
		}
	case (w.page == pageHome || w.page == pageHistory) && ptIn(w.txCancelBtnR, x, y):
		kind = hoverTxCancel
	case w.page == pageDevices && ptIn(w.devScanLanBtnR, x, y):
		kind = hoverDevScanLanBtn
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
	case w.page == pageSettings && w.setResetSrvR.right > w.setResetSrvR.left && ptIn(w.setResetSrvR, x, y):
		kind = hoverSetResetSrv
	case w.page == pageSettings && ptIn(w.setChangeSrvR, x, y):
		kind = hoverSetChangeSrv
	case w.page == pageSettings && ptIn(w.setRenameBtnR, x, y):
		kind = hoverSetRename
	case w.page == pageSettings && ptIn(w.setCopyIDRowR, x, y):
		kind = hoverSetCopyID
	case w.page == pageSettings && ptIn(w.aboutRepoR, x, y):
		kind = hoverAboutRepo
	default:
		if w.page == pageHome {
			for i, pr := range w.targetPillRects {
				if ptIn(pr, x, y) {
					kind, idx = hoverTargetPill, i
					break
				}
			}
		} else if w.page == pageHistory {
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
			if kind == hoverNone {
				for i, br := range w.devLanSendBtnsR {
					if ptIn(br, x, y) {
						kind, idx = hoverDevLanSendBtn, i
						break
					}
				}
			}
			if kind == hoverNone {
				for i, sr := range w.devLanSelectBtnsR {
					if ptIn(sr, x, y) {
						kind, idx = hoverDevLanSelectBtn, i
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
		w.refreshClipboard()
		procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		return
	case ptIn(w.navHistoryR, x, y):
		w.page = pageHistory
		w.unreadHistory = false
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
		case ptIn(w.netRetryR, x, y):
			if w.app != nil {
				w.app.restartSignaling()
				w.status = "正在重连云端信令服务器…"
				procInvalidateRect.Call(uintptr(hwnd), 0, 1)
			}
		case ptIn(w.tileFolderR, x, y):
			if dir := browseFolderDialog(hwnd, "选择要发送的文件夹"); dir != "" {
				items, _, _, err := describeDesktopPaths([]string{dir})
				if err != nil || len(items) == 0 {
					w.status = "所选文件夹内没有文件"
					procInvalidateRect.Call(uintptr(hwnd), 0, 1)
				} else if target, ok := w.activeTarget(); ok {
					w.queued = nil
					w.sendAsyncTarget(hwnd, target, []string{dir})
				} else {
					w.queued = []string{dir}
					w.status = ""
					procInvalidateRect.Call(uintptr(hwnd), 0, 1)
				}
			}
		case len(w.queued) > 0 && ptIn(w.queueClearR, x, y):
			w.queued = nil
			procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		case ptIn(w.homeScanLanR, x, y):
			w.page = pageDevices
			w.loadPeers()
			w.scanLAN(hwnd)
		case ptIn(w.addDevLinkR, x, y):
			openPairWindow(hwnd, w.app)
		case ptIn(w.sendBtnR, x, y):
			target, ok := w.activeTarget()
			if !ok {
				openPairWindow(hwnd, w.app)
				return
			}
			if len(w.queued) > 0 {
				files := append([]string(nil), w.queued...)
				w.queued = nil
				w.sendAsyncTarget(hwnd, target, files)
			} else {
				w.pickAndSend(hwnd)
			}
		case ptIn(w.sendTextR, x, y):
			w.openSendText(hwnd)
		case ptIn(w.sendClipR, x, y):
			w.sendClipboard(hwnd)
		case ptIn(w.tempShareBtnR, x, y):
			openTempShareWindow(hwnd, w.app)
		case ptIn(w.allHistoryLinkR, x, y):
			w.page = pageHistory
			w.unreadHistory = false
			w.status = ""
			procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		case ptIn(w.openDirR, x, y):
			if w.app != nil {
				shellOpen(w.app.receiveDir())
			}
		case ptIn(w.txPauseBtnR, x, y):
			if w.app != nil && w.app.togglePauseTransfer() {
				procInvalidateRect.Call(uintptr(hwnd), 0, 1)
			}
		case ptIn(w.txCancelBtnR, x, y):
			if w.app != nil && w.app.cancelTransfer() {
				procInvalidateRect.Call(uintptr(hwnd), 0, 1)
			}
		default:
			for i, pr := range w.targetPillRects {
				if ptIn(pr, x, y) && i < len(w.targetPillItems) {
					picked := w.targetPillItems[i]
					w.userPickedTarget = true
					if picked.isLAN {
						w.selectedLAN = picked.lan
					} else {
						w.selectedLAN = lanPeerEntry{}
						for pi, peer := range w.peers {
							if peer.ID == picked.peer.ID {
								w.sel = pi
								break
							}
						}
					}
					w.status = ""
					procInvalidateRect.Call(uintptr(hwnd), 0, 1)
					return
				}
			}
		}

	case pageHistory:
		switch {
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
		case ptIn(w.txPauseBtnR, x, y):
			if w.app != nil && w.app.togglePauseTransfer() {
				procInvalidateRect.Call(uintptr(hwnd), 0, 1)
			}
		case ptIn(w.txCancelBtnR, x, y):
			if w.app != nil && w.app.cancelTransfer() {
				procInvalidateRect.Call(uintptr(hwnd), 0, 1)
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
		if ptIn(w.devScanLanBtnR, x, y) {
			w.scanLAN(hwnd)
			return
		}
		if ptIn(w.devAddBtnR, x, y) {
			openPairWindow(hwnd, w.app)
			return
		}
		for i, br := range w.devSendBtnsR {
			if ptIn(br, x, y) && i < len(w.peers) {
				w.sel = i
				w.selectedLAN = lanPeerEntry{}
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
		for i, br := range w.devLanSendBtnsR {
			if ptIn(br, x, y) && i < len(w.lanTargets) {
				entry := w.lanTargets[i]
				w.selectedLAN = entry
				w.pickAndSend(hwnd)
				return
			}
		}
		for i, sr := range w.devLanSelectBtnsR {
			if ptIn(sr, x, y) && i < len(w.lanTargets) {
				w.selectedLAN = w.lanTargets[i]
				w.page = pageHome
				w.status = "已选择局域网临时目标：" + w.selectedLAN.name
				procInvalidateRect.Call(uintptr(hwnd), 0, 1)
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
		case w.setResetSrvR.right > w.setResetSrvR.left && ptIn(w.setResetSrvR, x, y):
			if w.app != nil {
				if err := w.app.resetServerConfig(); err != nil {
					w.status = "恢复默认服务器失败：" + err.Error()
				} else {
					w.status = "已恢复默认服务器配置并重连"
				}
				procInvalidateRect.Call(uintptr(hwnd), 0, 1)
			}
		case ptIn(w.setChangeSrvR, x, y):
			openServerConfigDialog(hwnd, w.app, func() {
				mainMu.Lock()
				if mw := mainWins[hwnd]; mw != nil {
					mw.status = "服务器配置已保存并重连"
				}
				mainMu.Unlock()
				procInvalidateRect.Call(uintptr(hwnd), 0, 1)
			})
		case ptIn(w.setRenameBtnR, x, y):
			w.openRenameDialog(hwnd)
		case ptIn(w.setCopyIDRowR, x, y):
			if w.fullID != "" && copyToClipboard(hwnd, w.fullID) {
				w.status = "已复制本机设备 ID"
				procInvalidateRect.Call(uintptr(hwnd), 0, 1)
			}
		case ptIn(w.aboutRepoR, x, y):
			shellOpen(ferryRepoURL)
		}
	}
}

func (w *mainWin) sendClipboard(hwnd syscall.Handle) {
	target, ok := w.activeTarget()
	if !ok {
		openPairWindow(hwnd, w.app)
		return
	}
	if pngBytes, hasImg := readClipboardImageAsPNG(); hasImg && len(pngBytes) > 0 {
		tmpDir, err := os.MkdirTemp("", "ferry-clip-*")
		if err != nil {
			w.status = "创建临时截图失败：" + err.Error()
			procInvalidateRect.Call(uintptr(hwnd), 0, 1)
			return
		}
		imgPath := filepath.Join(tmpDir, fmt.Sprintf("clipboard-%s.png", time.Now().Format("150405")))
		if err := os.WriteFile(imgPath, pngBytes, 0o600); err != nil {
			os.RemoveAll(tmpDir)
			w.status = "写入临时截图失败：" + err.Error()
			procInvalidateRect.Call(uintptr(hwnd), 0, 1)
			return
		}
		label := target.label()
		w.status = fmt.Sprintf("正在将剪贴板截图发往 %s…", label)
		procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		go func() {
			defer os.RemoveAll(tmpDir)
			var res client.Result
			var serr error
			if target.isLAN {
				res, serr = sendToLANTarget(w.app, []string{imgPath}, label, target.lan.addr)
			} else {
				res, serr = sendToPeer(w.app, []string{imgPath}, target.peer)
			}
			msg := fmt.Sprintf("已将剪贴板截图发给 %s（%s）", label, humanBytes(res.Bytes))
			if serr != nil {
				switch {
				case strings.Contains(serr.Error(), "对方已取消"):
					msg = "对方已取消传输"
				case strings.Contains(serr.Error(), "已取消"):
					msg = "已取消发送"
				default:
					msg = "发送失败：" + serr.Error()
				}
			}
			mainMu.Lock()
			if mw := mainWins[hwnd]; mw != nil {
				mw.status = msg
			}
			mainMu.Unlock()
			procPostMessageW.Call(uintptr(hwnd), wmAppPaint, 0, 0)
		}()
		return
	}
	txt, ok := readClipboardText()
	if !ok || strings.TrimSpace(txt) == "" {
		w.status = "剪贴板中没有文本或截图"
		procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		return
	}
	w.sendTextPayload(hwnd, target, txt, "clipboard", "剪贴板")
}

func (w *mainWin) openSendText(hwnd syscall.Handle) {
	target, ok := w.activeTarget()
	if !ok {
		openPairWindow(hwnd, w.app)
		return
	}
	openTextInputDialog(textInputOpts{
		Owner:       hwnd,
		Title:       "发送文字",
		Subtitle:    "发给 " + target.label() + "（Ctrl+Enter 快速发送）",
		ConfirmText: "发送",
		Multiline:   true,
		AllowEmpty:  false,
		OnSubmit: func(text string) {
			w.sendTextPayload(hwnd, target, text, "text", "文字")
		},
	})
}

func (w *mainWin) sendTextPayload(hwnd syscall.Handle, target sendTarget, txt, prefix, kindLabel string) {
	tmp, err := writeTempTextFile(prefix, txt)
	if err != nil {
		w.status = "创建临时文本失败：" + err.Error()
		procInvalidateRect.Call(uintptr(hwnd), 0, 1)
		return
	}
	label := target.label()
	w.status = fmt.Sprintf("正在将%s发往 %s…", kindLabel, label)
	procInvalidateRect.Call(uintptr(hwnd), 0, 1)
	go func() {
		defer os.RemoveAll(filepath.Dir(tmp))
		var res client.Result
		var err error
		if target.isLAN {
			res, err = sendToLANTarget(w.app, []string{tmp}, label, target.lan.addr)
		} else {
			res, err = sendToPeer(w.app, []string{tmp}, target.peer)
		}
		msg := fmt.Sprintf("已将%s发给 %s（%s）", kindLabel, label, humanBytes(res.Bytes))
		if err != nil {
			switch {
			case strings.Contains(err.Error(), "对方已取消"):
				msg = "对方已取消传输"
			case strings.Contains(err.Error(), "已取消"):
				msg = "已取消发送"
			default:
				msg = "发送失败：" + err.Error()
			}
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
	target, ok := w.activeTarget()
	if !ok {
		openPairWindow(hwnd, w.app)
		return
	}
	files := openFileDialog(uintptr(hwnd))
	if len(files) == 0 {
		return
	}
	w.sendAsyncTarget(hwnd, target, files)
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
	target, ok := w.activeTarget()
	if !ok {
		items, _, _, err := describeDesktopPaths(files)
		if err != nil || len(items) == 0 {
			w.status = "所选路径内没有可发送的文件"
		} else {
			w.queued = files
		}
		openPairWindow(hwnd, w.app)
		return
	}
	w.sendAsyncTarget(hwnd, target, files)
}

func (w *mainWin) sendAsyncTarget(hwnd syscall.Handle, target sendTarget, files []string) {
	if len(files) == 0 || w.app == nil {
		return
	}
	label := target.label()
	w.status = fmt.Sprintf("正在发送给 %s…", label)
	procInvalidateRect.Call(uintptr(hwnd), 0, 1)
	go func() {
		var res client.Result
		var err error
		if target.isLAN {
			res, err = sendToLANTarget(w.app, files, label, target.lan.addr)
		} else {
			res, err = sendToPeer(w.app, files, target.peer)
		}
		doneLabel := label
		if res.PeerName != "" {
			doneLabel = res.PeerName
		}
		msg := fmt.Sprintf("已发送给 %s（%s）", doneLabel, summarizeFiles(res.Files))
		if err != nil {
			switch {
			case strings.Contains(err.Error(), "对方已取消"):
				msg = "对方已取消传输"
			case strings.Contains(err.Error(), "已取消"):
				msg = "已取消发送"
			default:
				msg = "发送失败：" + err.Error()
			}
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
