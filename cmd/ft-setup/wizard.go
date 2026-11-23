//go:build windows

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

const (
	wizClassName = "FerrySetupWizardV2"

	wizIDPathEdit    = 101
	wmAppWizardDone  = 0x8000 + 10
	wizProgressTimer = 31

	wizModeInstall   = 0
	wizModeUninstall = 1

	wizHoverNone = iota
	wizHoverMin
	wizHoverClose
	wizHoverBrowse
	wizHoverChkDesk
	wizHoverChkMenu
	wizHoverChkAuto
	wizHoverChkLaunch
	wizHoverCancel
	wizHoverPrimary
)

type wizPoint struct{ x, y int32 }

type wizRect struct{ left, top, right, bottom int32 }

type wizMsg struct {
	hwnd    syscall.Handle
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      wizPoint
}

type wizWndClassEx struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     syscall.Handle
	hIcon         syscall.Handle
	hCursor       syscall.Handle
	hbrBackground syscall.Handle
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       syscall.Handle
}

type wizPaintStruct struct {
	hdc         syscall.Handle
	fErase      int32
	rcPaint     wizRect
	fRestore    int32
	fIncUpdate  int32
	rgbReserved [32]byte
}

type wizBrowseInfo struct {
	hwndOwner      syscall.Handle
	pidlRoot       uintptr
	pszDisplayName *uint16
	lpszTitle      *uint16
	ulFlags        uint32
	lpfn           uintptr
	lParam         uintptr
	iImage         int32
}

type wizardState struct {
	mode           int
	hwnd           syscall.Handle
	dpi            int32
	defaultDir     string
	pal            setupPalette
	dark           bool
	acrylic        bool
	needSelfDelete bool

	hEditPath syscall.Handle
	editBg    syscall.Handle

	fontH1   syscall.Handle
	fontH2   syscall.Handle
	fontBold syscall.Handle
	fontBody syscall.Handle
	fontSm   syscall.Handle
	hIcon    syscall.Handle
	tempIcon string

	chkDesk   bool
	chkMenu   bool
	chkAuto   bool
	chkLaunch bool

	working    bool
	done       bool
	statusErr  bool
	statusText string
	launchDir  string
	progTick   int

	hover    int
	tracking bool

	// Hit rects computed by layout()
	titleBarR   wizRect
	capMinR     wizRect
	capCloseR   wizRect
	dirCardR    wizRect
	editBoxR    wizRect
	browseBtnR  wizRect
	optCardR    wizRect
	chkDeskR    wizRect
	chkMenuR    wizRect
	chkAutoR    wizRect
	chkLaunchR  wizRect
	doneLaunchR wizRect
	statusR     wizRect
	progBarR    wizRect
	cancelBtnR  wizRect
	primBtnR    wizRect

	mu      sync.Mutex
	lastErr error
}

var (
	activeWizard    *wizardState
	wizardWndCB     = syscall.NewCallback(wizardWndProc)
	wizClassRegOnce sync.Once
)

func runWizard(defaultDir string) int {
	return runSetupUI(wizModeInstall, defaultDir)
}

func runUninstallWizard(defaultDir string) int {
	return runSetupUI(wizModeUninstall, defaultDir)
}

func runSetupUI(mode int, defaultDir string) int {
	runtime.LockOSThread()

	dpiRaw, _, _ := procWizGetDpiForSystem.Call()
	dpi := int32(dpiRaw)
	if dpi == 0 {
		dpi = 96
	}
	pal, dark := setupTheme()

	w := &wizardState{
		mode:       mode,
		dpi:        dpi,
		defaultDir: defaultDir,
		pal:        pal,
		dark:       dark,
		chkDesk:    true,
		chkMenu:    true,
		chkAuto:    true,
		chkLaunch:  true,
		statusText: "准备就绪，将保留您已有的设备密钥与配对记录。",
	}
	activeWizard = w
	defer w.cleanup()

	w.hIcon, w.tempIcon = loadPayloadIcon()
	hInstRaw, _, _ := procWizGetModuleHandleW.Call(0)
	hInst := syscall.Handle(hInstRaw)
	cursor, _, _ := procWizLoadCursorW.Call(0, 32512)
	icon := uintptr(w.hIcon)
	if icon == 0 {
		icon, _, _ = procWizLoadIconW.Call(0, 32512)
	}

	cls := wizUTF16Ptr(wizClassName)
	wizClassRegOnce.Do(func() {
		bg := createBrush(pal.paper)
		wc := wizWndClassEx{
			cbSize:        uint32(unsafe.Sizeof(wizWndClassEx{})),
			lpfnWndProc:   wizardWndCB,
			hInstance:     hInst,
			hIcon:         syscall.Handle(icon),
			hIconSm:       syscall.Handle(icon),
			hCursor:       syscall.Handle(cursor),
			hbrBackground: bg,
			lpszClassName: cls,
		}
		procWizRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	})

	winW, winH := wizScale(dpi, 504), wizScale(dpi, 476)
	title := "轻渡安装向导"
	if mode == wizModeUninstall {
		winW, winH = wizScale(dpi, 436), wizScale(dpi, 296)
		title = "卸载轻渡"
	}
	x, y := centerOnWorkArea(winW, winH)

	// Omit WS_SYSMENU so Win11 DWM draws no native caption buttons over our client titlebar.
	const style = 0x00C00000 | 0x02000000 | 0x00020000
	const exStyle = 0x00040000 | 0x00010000 // WS_EX_APPWINDOW | WS_EX_CONTROLPARENT

	hwndRaw, _, _ := procWizCreateWindowExW.Call(
		exStyle,
		uintptr(unsafe.Pointer(cls)),
		uintptr(unsafe.Pointer(wizUTF16Ptr(title))),
		style,
		uintptr(x), uintptr(y), uintptr(winW), uintptr(winH),
		0, 0, uintptr(hInst), 0,
	)
	if hwndRaw == 0 {
		return 1
	}
	w.hwnd = syscall.Handle(hwndRaw)
	procWizShowWindow.Call(hwndRaw, 5)
	procWizUpdateWindow.Call(hwndRaw)

	var msg wizMsg
	for {
		r, _, _ := procWizGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		procWizTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procWizDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
	return 0
}

func (w *wizardState) cleanup() {
	for _, h := range []syscall.Handle{
		w.fontH1, w.fontH2, w.fontBold, w.fontBody, w.fontSm, w.editBg,
	} {
		if h != 0 {
			procWizDeleteObject.Call(uintptr(h))
		}
	}
	if w.tempIcon != "" {
		_ = os.Remove(w.tempIcon)
	}
}

func createBrush(colorRef uint32) syscall.Handle {
	h, _, _ := procWizCreateSolidBrush.Call(uintptr(colorRef))
	return syscall.Handle(h)
}

func loadPayloadIcon() (syscall.Handle, string) {
	data, err := payload.ReadFile("payload/menu/ft.ico")
	if err != nil || len(data) == 0 {
		return 0, ""
	}
	tmp := filepath.Join(os.TempDir(), "ferry-setup-icon.ico")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return 0, ""
	}
	h, _, _ := procWizLoadImageW.Call(0, uintptr(unsafe.Pointer(wizUTF16Ptr(tmp))), 1, 0, 0, 0x0010|0x0040)
	return syscall.Handle(h), tmp
}

func centerOnWorkArea(w, h int32) (int32, int32) {
	var rc wizRect
	procWizSystemParametersInfoW.Call(0x0030, 0, uintptr(unsafe.Pointer(&rc)), 0)
	if rc.right <= rc.left || rc.bottom <= rc.top {
		return 100, 100
	}
	return rc.left + (rc.right-rc.left-w)/2, rc.top + (rc.bottom-rc.top-h)/2
}

func setSetupDPIAware() {
	const perMonitorV2 = ^uintptr(0) - 3
	if r, _, _ := procWizSetDpiAwareCtx.Call(perMonitorV2); r != 0 {
		return
	}
	procWizSetDPIAware.Call()
}

func wizPtrAt(u uintptr) unsafe.Pointer {
	var p unsafe.Pointer
	*(*uintptr)(unsafe.Pointer(&p)) = u
	return p
}

func wizUTF16Ptr(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return nil
	}
	return p
}

func wizScale(dpi, v int32) int32 {
	if dpi <= 0 {
		dpi = 96
	}
	return int32(float64(v)*float64(dpi)/96.0 + 0.5)
}

func (w *wizardState) s(v int32) int32 { return wizScale(w.dpi, v) }

func wizardWndProc(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	w := activeWizard
	const (
		wmCreate         = 0x0001
		wmDestroy        = 0x0002
		wmPaint          = 0x000F
		wmClose          = 0x0010
		wmEraseBkgnd     = 0x0014
		wmSetCursor      = 0x0020
		wmNcCalcSize     = 0x0083
		wmNcHitTest      = 0x0084
		wmTimer          = 0x0113
		wmCtlColorEdit   = 0x0133
		wmCtlColorStatic = 0x0138
		wmMouseMove      = 0x0200
		wmLButtonUp      = 0x0202
		wmMouseLeave     = 0x02A3
	)

	switch msg {
	case wmCreate:
		if w != nil {
			w.hwnd = hwnd
			if d, _, _ := procWizGetDpiForWindow.Call(uintptr(hwnd)); d != 0 {
				w.dpi = int32(d)
			}
			procWizSetWindowPos.Call(uintptr(hwnd), 0, 0, 0, 0, 0, 0x0027)
			w.acrylic = enableSetupAcrylic(hwnd, w.dark)
			w.editBg = createBrush(w.pal.paper2)
			w.makeFonts()
			if w.mode == wizModeInstall {
				w.initEditControl()
			}
		}
		return 0

	case wmNcCalcSize:
		if wParam != 0 {
			return 0
		}

	case wmNcHitTest:
		if w != nil {
			return w.ncHitTest(hwnd, lParam)
		}

	case wmEraseBkgnd:
		return 1

	case wmPaint:
		if w != nil {
			w.paint(hwnd)
			return 0
		}

	case wmMouseMove:
		if w != nil {
			w.onMouseMove(hwnd, int32(lParam&0xffff), int32((lParam>>16)&0xffff))
			return 0
		}

	case wmMouseLeave:
		if w != nil {
			w.tracking = false
			if w.hover != wizHoverNone {
				w.hover = wizHoverNone
				procWizInvalidateRect.Call(uintptr(hwnd), 0, 0)
			}
			return 0
		}

	case wmSetCursor:
		if w != nil && uint16(lParam&0xffff) == 1 && w.cursorIsHand() {
			cur, _, _ := procWizLoadCursorW.Call(0, 32649) // IDC_HAND
			procWizSetCursor.Call(cur)
			return 1
		}

	case wmLButtonUp:
		if w != nil {
			w.onClick(hwnd, int32(lParam&0xffff), int32((lParam>>16)&0xffff))
			return 0
		}

	case wmCtlColorEdit:
		if w != nil {
			hdc := wParam
			procWizSetBkColor.Call(hdc, uintptr(w.pal.paper2))
			procWizSetBkMode.Call(hdc, 2) // OPAQUE
			procWizSetTextColor.Call(hdc, uintptr(w.pal.ink))
			return uintptr(w.editBg)
		}

	case wmCtlColorStatic:
		if w != nil && lParam == uintptr(w.hEditPath) {
			hdc := wParam
			procWizSetBkColor.Call(hdc, uintptr(w.pal.paper2))
			procWizSetBkMode.Call(hdc, 2) // OPAQUE
			procWizSetTextColor.Call(hdc, uintptr(w.pal.ink2))
			return uintptr(w.editBg)
		}

	case wmTimer:
		if w != nil && wParam == wizProgressTimer && w.working {
			w.progTick = (w.progTick + 1) % 40
			procWizInvalidateRect.Call(uintptr(hwnd), 0, 0)
			return 0
		}

	case wmAppWizardDone:
		if w != nil {
			w.onTaskDone()
		}
		return 0

	case wmClose:
		if w != nil {
			if w.working {
				return 0
			}
			w.finishClose()
			return 0
		}

	case wmDestroy:
		procWizKillTimer.Call(uintptr(hwnd), wizProgressTimer)
		procWizPostQuitMessage.Call(0)
		return 0
	}

	r, _, _ := procWizDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return r
}

func (w *wizardState) makeFonts() {
	w.fontH1 = createSetupFont(w.dpi, 18, 700)
	w.fontH2 = createSetupFont(w.dpi, 15, 600)
	w.fontBold = createSetupFont(w.dpi, 13, 600)
	w.fontBody = createSetupFont(w.dpi, 13, 400)
	w.fontSm = createSetupFont(w.dpi, 12, 400)
}

func (w *wizardState) initEditControl() {
	var cr wizRect
	procWizGetClientRect.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&cr)))
	w.layout(cr.right, cr.bottom)

	hInstRaw, _, _ := procWizGetModuleHandleW.Call(0)
	// WS_CHILD | WS_VISIBLE | WS_TABSTOP | ES_AUTOHSCROLL = 0x50010080
	inX := w.editBoxR.left + w.s(8)
	inY := w.editBoxR.top + w.s(6)
	inW := (w.editBoxR.right - w.editBoxR.left) - w.s(16)
	inH := (w.editBoxR.bottom - w.editBoxR.top) - w.s(12)
	var exStyle uintptr
	if w.acrylic {
		exStyle = 0x00080000 // WS_EX_LAYERED
	}
	h, _, _ := procWizCreateWindowExW.Call(
		exStyle,
		uintptr(unsafe.Pointer(wizUTF16Ptr("EDIT"))),
		uintptr(unsafe.Pointer(wizUTF16Ptr(w.defaultDir))),
		0x50010080,
		uintptr(inX), uintptr(inY), uintptr(inW), uintptr(inH),
		uintptr(w.hwnd), wizIDPathEdit, hInstRaw, 0,
	)
	if h == 0 && exStyle != 0 {
		h, _, _ = procWizCreateWindowExW.Call(
			0,
			uintptr(unsafe.Pointer(wizUTF16Ptr("EDIT"))),
			uintptr(unsafe.Pointer(wizUTF16Ptr(w.defaultDir))),
			0x50010080,
			uintptr(inX), uintptr(inY), uintptr(inW), uintptr(inH),
			uintptr(w.hwnd), wizIDPathEdit, hInstRaw, 0,
		)
		exStyle = 0
	}
	w.hEditPath = syscall.Handle(h)
	if w.hEditPath != 0 {
		if w.acrylic && exStyle != 0 {
			procWizSetLayeredWindowAttributes.Call(h, 0, 255, 0x00000002) // LWA_ALPHA
		}
		procWizSendMessageW.Call(h, 0x0030, uintptr(w.fontBody), 1)
		procWizSendMessageW.Call(h, 0x00D3, 3, uintptr(w.s(2))|uintptr(w.s(2)<<16))
	}
}

func (w *wizardState) layout(cw, ch int32) {
	s := w.s
	topH := s(36)
	w.titleBarR = wizRect{0, 0, cw, topH}
	bw := s(42)
	w.capCloseR = wizRect{cw - bw, 0, cw, topH}
	w.capMinR = wizRect{cw - bw*2, 0, cw - bw, topH}

	w.dirCardR, w.editBoxR, w.browseBtnR = wizRect{}, wizRect{}, wizRect{}
	w.optCardR, w.chkDeskR, w.chkMenuR, w.chkAutoR, w.chkLaunchR = wizRect{}, wizRect{}, wizRect{}, wizRect{}, wizRect{}
	w.doneLaunchR, w.statusR, w.progBarR = wizRect{}, wizRect{}, wizRect{}
	w.cancelBtnR, w.primBtnR = wizRect{}, wizRect{}

	pad := s(26)
	btnH := s(36)
	btnY := ch - pad - btnH
	gap := s(10)

	if w.mode == wizModeUninstall {
		if w.done && !w.statusErr {
			pw := s(120)
			px := (cw - pw) / 2
			w.primBtnR = wizRect{px, btnY, px + pw, btnY + btnH}
			return
		}
		primW := s(108)
		cancW := s(84)
		w.primBtnR = wizRect{cw - pad - primW, btnY, cw - pad, btnY + btnH}
		w.cancelBtnR = wizRect{w.primBtnR.left - gap - cancW, btnY, w.primBtnR.left - gap, btnY + btnH}
		w.progBarR = wizRect{pad, btnY - s(24), cw - pad, btnY - s(18)}
		w.statusR = wizRect{pad, btnY - s(48), cw - pad, btnY - s(26)}
		return
	}

	// Install mode
	if w.done && !w.statusErr {
		pw := s(132)
		px := (cw - pw) / 2
		w.primBtnR = wizRect{px, btnY, px + pw, btnY + btnH}
		w.doneLaunchR = wizRect{cw/2 - s(100), btnY - s(42), cw/2 + s(100), btnY - s(14)}
		return
	}

	primW := s(108)
	cancW := s(84)
	w.primBtnR = wizRect{cw - pad - primW, btnY, cw - pad, btnY + btnH}
	w.cancelBtnR = wizRect{w.primBtnR.left - gap - cancW, btnY, w.primBtnR.left - gap, btnY + btnH}

	y := topH + s(80)
	// Location card
	w.dirCardR = wizRect{pad, y, cw - pad, y + s(74)}
	browseW := s(74)
	rowTop := y + s(32)
	rowBot := y + s(62)
	w.browseBtnR = wizRect{w.dirCardR.right - s(12) - browseW, rowTop, w.dirCardR.right - s(12), rowBot}
	w.editBoxR = wizRect{w.dirCardR.left + s(12), rowTop, w.browseBtnR.left - s(8), rowBot}
	y = w.dirCardR.bottom + s(14)

	// Options card (4 checkboxes)
	rowH := s(34)
	w.optCardR = wizRect{pad, y, cw - pad, y + s(30) + rowH*4 + s(6)}
	cy := y + s(30)
	w.chkDeskR = wizRect{pad + s(10), cy, cw - pad - s(10), cy + rowH}
	cy += rowH
	w.chkMenuR = wizRect{pad + s(10), cy, cw - pad - s(10), cy + rowH}
	cy += rowH
	w.chkAutoR = wizRect{pad + s(10), cy, cw - pad - s(10), cy + rowH}
	cy += rowH
	w.chkLaunchR = wizRect{pad + s(10), cy, cw - pad - s(10), cy + rowH}

	w.statusR = wizRect{pad, w.optCardR.bottom + s(10), cw - pad, w.optCardR.bottom + s(32)}
	w.progBarR = wizRect{pad, w.statusR.bottom + s(4), cw - pad, w.statusR.bottom + s(10)}
}

func wizPtIn(r wizRect, x, y int32) bool {
	return x >= r.left && x < r.right && y >= r.top && y < r.bottom
}

func (w *wizardState) ncHitTest(hwnd syscall.Handle, lParam uintptr) uintptr {
	pt := wizPoint{int32(int16(lParam & 0xffff)), int32(int16((lParam >> 16) & 0xffff))}
	procWizScreenToClient.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&pt)))
	var cr wizRect
	procWizGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&cr)))
	w.layout(cr.right, cr.bottom)
	if wizPtIn(w.titleBarR, pt.x, pt.y) && !wizPtIn(w.capMinR, pt.x, pt.y) && !wizPtIn(w.capCloseR, pt.x, pt.y) {
		return 2 // HTCAPTION
	}
	return 1 // HTCLIENT
}

func (w *wizardState) surfaceAlpha(a byte) byte {
	if !w.acrylic {
		return 255
	}
	return a
}

func (w *wizardState) paint(hwnd syscall.Handle) {
	var ps wizPaintStruct
	hdcRaw, _, _ := procWizBeginPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))
	hdc := uintptr(hdcRaw)
	defer procWizEndPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))

	var cr wizRect
	procWizGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&cr)))
	if cr.right <= 0 || cr.bottom <= 0 {
		return
	}
	w.layout(cr.right, cr.bottom)

	cv := newSetupGlassCanvas(hdc, cr.right, cr.bottom)
	if cv == nil {
		return
	}

	barA := byte(150)
	workA := byte(216)
	if w.dark {
		barA = 164
		workA = 222
	}
	cv.fillColorAlpha(w.titleBarR, w.pal.paper2, w.surfaceAlpha(barA))
	cv.fillColorAlpha(wizRect{0, w.titleBarR.bottom - 1, cr.right, w.titleBarR.bottom}, w.pal.line, w.surfaceAlpha(175))
	cv.fillColorAlpha(wizRect{0, w.titleBarR.bottom, cr.right, cr.bottom}, w.pal.paper, w.surfaceAlpha(workA))

	w.drawTitleBar(cv)
	if w.mode == wizModeUninstall {
		w.drawUninstallBody(cv, cr)
	} else {
		w.drawInstallBody(cv, cr)
	}
	cv.finishBlit(hdc)
}

func (w *wizardState) drawTitleBar(cv *setupGlassCanvas) {
	s := w.s
	mSz := s(18)
	my := (w.titleBarR.bottom - mSz) / 2
	markR := wizRect{s(14), my, s(14) + mSz, my + mSz}
	drawSetupBrandMark(cv, markR, float64(s(5)), w.pal.accent)
	title := "轻渡安装"
	if w.mode == wizModeUninstall {
		title = "卸载轻渡"
	}
	cv.drawAlphaText(w.fontSm, title, wizRect{markR.right + s(8), 0, w.capMinR.left, w.titleBarR.bottom},
		0x0020|0x0004, w.pal.ink)

	if w.hover == wizHoverMin {
		cv.fillColorAlpha(w.capMinR, w.pal.paper3, w.surfaceAlpha(155))
	}
	drawSetupMinGlyph(cv, w.capMinR, w.pal.ink, s(10))

	closeInk := w.pal.ink
	if w.hover == wizHoverClose {
		cv.fillColorAlpha(w.capCloseR, w.pal.danger, 235)
		closeInk = wizCol(255, 255, 255)
	}
	drawSetupCloseGlyph(cv, w.capCloseR, closeInk)
}

func (w *wizardState) drawInstallBody(cv *setupGlassCanvas, cr wizRect) {
	s := w.s
	pad := s(26)
	topY := w.titleBarR.bottom

	if w.done && !w.statusErr {
		cy := topY + s(86)
		badge := wizRect{cr.right/2 - s(28), cy, cr.right/2 + s(28), cy + s(56)}
		cv.drawAlphaRoundRect(badge, float64(s(28)), w.pal.accent, 255, 0, 0, 0)
		drawSetupCheckmark(cv, badge, w.pal.accentInk, s(3))

		cv.drawAlphaText(w.fontH1, "轻渡 安装完成", wizRect{pad, badge.bottom + s(18), cr.right - pad, badge.bottom + s(48)},
			0x0001|0x0020|0x0004, w.pal.ink)
		cv.drawAlphaText(w.fontSm, "已安装到 "+w.launchDir, wizRect{pad, badge.bottom + s(52), cr.right - pad, badge.bottom + s(76)},
			0x0001|0x0020|0x0004|0x8000, w.pal.ink2)

		w.drawCheckboxRow(cv, w.doneLaunchR, w.chkLaunch, w.hover == wizHoverChkLaunch, "安装完成后立即启动 轻渡")
		w.drawButton(cv, w.primBtnR, "完成", true, false, w.hover == wizHoverPrimary)
		return
	}

	// Brand hero row
	logo := wizRect{pad, topY + s(18), pad + s(38), topY + s(56)}
	drawSetupBrandMark(cv, logo, float64(s(10)), w.pal.accent)
	cv.drawAlphaText(w.fontH1, "安装 轻渡", wizRect{logo.right + s(14), topY + s(16), cr.right - pad, topY + s(40)},
		0x0020|0x0004, w.pal.ink)
	cv.drawAlphaText(w.fontSm, "跨设备文件互传 · 局域网直连 · UDP 打洞 · 服务器中继",
		wizRect{logo.right + s(14), topY + s(40), cr.right - pad, topY + s(60)},
		0x0020|0x0004|0x8000, w.pal.ink2)

	// Card 1: Install directory
	cv.drawAlphaRoundRect(w.dirCardR, float64(s(12)), w.pal.paper, w.surfaceAlpha(205), w.pal.line, w.surfaceAlpha(220), 1.0)
	cv.drawAlphaText(w.fontBold, "安装位置", wizRect{w.dirCardR.left + s(14), w.dirCardR.top + s(8), w.dirCardR.right - s(14), w.dirCardR.top + s(28)},
		0x0020|0x0004, w.pal.ink)
	cv.drawAlphaRoundRect(w.editBoxR, float64(s(7)), w.pal.paper2, 255, w.pal.line, 255, 1.0)
	w.drawButton(cv, w.browseBtnR, "浏览…", false, false, w.hover == wizHoverBrowse)

	// Card 2: Options
	cv.drawAlphaRoundRect(w.optCardR, float64(s(12)), w.pal.paper, w.surfaceAlpha(205), w.pal.line, w.surfaceAlpha(220), 1.0)
	cv.drawAlphaText(w.fontBold, "安装选项", wizRect{w.optCardR.left + s(14), w.optCardR.top + s(8), w.optCardR.right - s(14), w.optCardR.top + s(28)},
		0x0020|0x0004, w.pal.ink)
	w.drawCheckboxRow(cv, w.chkDeskR, w.chkDesk, w.hover == wizHoverChkDesk, "创建桌面快捷方式")
	w.drawCheckboxRow(cv, w.chkMenuR, w.chkMenu, w.hover == wizHoverChkMenu, "创建开始菜单快捷方式")
	w.drawCheckboxRow(cv, w.chkAutoR, w.chkAuto, w.hover == wizHoverChkAuto, "开机自动启动（静默驻留托盘）")
	w.drawCheckboxRow(cv, w.chkLaunchR, w.chkLaunch, w.hover == wizHoverChkLaunch, "安装完成后立即启动 轻渡")

	statusClr := w.pal.ink2
	if w.statusErr {
		statusClr = w.pal.danger
	}
	cv.drawAlphaText(w.fontSm, w.statusText, w.statusR, 0x0020|0x0004|0x8000, statusClr)
	if w.working {
		w.drawIndeterminateBar(cv, w.progBarR)
	}

	w.drawButton(cv, w.cancelBtnR, "取消", false, false, w.hover == wizHoverCancel)
	primLabel := "立即安装"
	if w.working {
		primLabel = "安装中…"
	} else if w.statusErr {
		primLabel = "重试安装"
	}
	w.drawButton(cv, w.primBtnR, primLabel, true, false, w.hover == wizHoverPrimary)
}

func (w *wizardState) drawUninstallBody(cv *setupGlassCanvas, cr wizRect) {
	s := w.s
	pad := s(26)
	topY := w.titleBarR.bottom

	if w.done && !w.statusErr {
		cy := topY + s(32)
		badge := wizRect{cr.right/2 - s(24), cy, cr.right/2 + s(24), cy + s(48)}
		cv.drawAlphaRoundRect(badge, float64(s(24)), w.pal.accent, 255, 0, 0, 0)
		drawSetupCheckmark(cv, badge, w.pal.accentInk, s(3))

		cv.drawAlphaText(w.fontH1, "轻渡 已卸载", wizRect{pad, badge.bottom + s(14), cr.right - pad, badge.bottom + s(42)},
			0x0001|0x0020|0x0004, w.pal.ink)
		cv.drawAlphaText(w.fontSm, "已清理程序文件、开机自启项、右键菜单与快捷方式。",
			wizRect{pad, badge.bottom + s(44), cr.right - pad, badge.bottom + s(68)},
			0x0001|0x0020|0x0004, w.pal.ink2)

		w.drawButton(cv, w.primBtnR, "完成", true, false, w.hover == wizHoverPrimary)
		return
	}

	logo := wizRect{pad, topY + s(24), pad + s(38), topY + s(62)}
	drawSetupBrandMark(cv, logo, float64(s(10)), w.pal.accent)
	cv.drawAlphaText(w.fontH1, "卸载 轻渡", wizRect{logo.right + s(14), topY + s(22), cr.right - pad, topY + s(48)},
		0x0020|0x0004, w.pal.ink)
	cv.drawAlphaText(w.fontSm, "目录："+w.defaultDir,
		wizRect{logo.right + s(14), topY + s(48), cr.right - pad, topY + s(68)},
		0x0020|0x0004|0x8000, w.pal.ink2)

	card := wizRect{pad, topY + s(80), cr.right - pad, topY + s(144)}
	cv.drawAlphaRoundRect(card, float64(s(12)), w.pal.paper, w.surfaceAlpha(205), w.pal.line, w.surfaceAlpha(220), 1.0)
	cv.drawAlphaText(w.fontSm, "将移除程序文件、开机自启、资源管理器右键菜单及快捷方式。\n您已接收下载的个人文件不会被删除。",
		wizRect{card.left + s(14), card.top + s(12), card.right - s(14), card.bottom - s(10)},
		0x0010, w.pal.ink2)

	if w.working {
		cv.drawAlphaText(w.fontSm, w.statusText, w.statusR, 0x0020|0x0004, w.pal.ink2)
		w.drawIndeterminateBar(cv, w.progBarR)
	} else if w.statusErr {
		cv.drawAlphaText(w.fontSm, w.statusText, w.statusR, 0x0020|0x0004|0x8000, w.pal.danger)
	}

	w.drawButton(cv, w.cancelBtnR, "取消", false, false, w.hover == wizHoverCancel)
	primLabel := "确认卸载"
	if w.working {
		primLabel = "卸载中…"
	}
	w.drawButton(cv, w.primBtnR, primLabel, true, true, w.hover == wizHoverPrimary)
}

func (w *wizardState) drawCheckboxRow(cv *setupGlassCanvas, r wizRect, checked, hovered bool, text string) {
	s := w.s
	if hovered {
		cv.drawAlphaRoundRect(r, float64(s(6)), w.pal.paper2, w.surfaceAlpha(185), 0, 0, 0)
	}
	boxSz := s(16)
	bx := r.left + s(6)
	by := r.top + ((r.bottom-r.top)-boxSz)/2
	boxR := wizRect{bx, by, bx + boxSz, by + boxSz}
	if checked {
		cv.drawAlphaRoundRect(boxR, float64(s(4)), w.pal.accent, 255, w.pal.accent, 255, 0)
		drawSetupCheckmark(cv, boxR, w.pal.accentInk, s(2))
	} else {
		edge := w.pal.line
		if hovered {
			edge = w.pal.accent
		}
		cv.drawAlphaRoundRect(boxR, float64(s(4)), w.pal.paper, 240, edge, 255, 1.2)
	}
	cv.drawAlphaText(w.fontBody, text, wizRect{boxR.right + s(10), r.top, r.right - s(6), r.bottom},
		0x0020|0x0004|0x8000, w.pal.ink)
}

func (w *wizardState) drawButton(cv *setupGlassCanvas, r wizRect, label string, primary, danger, hovered bool) {
	s := w.s
	if primary {
		fill := w.pal.accent
		if danger {
			fill = w.pal.danger
		}
		if w.working {
			fill = w.pal.ink3
		} else if hovered {
			fill = wizShade(fill, w.dark, 0.12)
		}
		cv.drawAlphaRoundRect(r, float64(s(8)), fill, 255, fill, 255, 0)
		cv.drawAlphaText(w.fontBold, label, r, 0x0001|0x0020|0x0004, w.pal.accentInk)
		return
	}
	fill := w.pal.paper2
	edge := w.pal.line
	if hovered && !w.working {
		fill = w.pal.paper3
		edge = w.pal.accent
	}
	cv.drawAlphaRoundRect(r, float64(s(8)), fill, w.surfaceAlpha(235), edge, 255, 1.0)
	cv.drawAlphaText(w.fontBody, label, r, 0x0001|0x0020|0x0004, w.pal.ink)
}

func (w *wizardState) drawIndeterminateBar(cv *setupGlassCanvas, r wizRect) {
	s := w.s
	cv.drawAlphaRoundRect(r, float64(s(3)), w.pal.paper3, 240, 0, 0, 0)
	bw := r.right - r.left
	segW := bw * 32 / 100
	if segW < s(24) {
		segW = s(24)
	}
	pos := ((bw + segW) * int32(w.progTick)) / 40
	x0 := r.left + pos - segW
	x1 := r.left + pos
	if x0 < r.left {
		x0 = r.left
	}
	if x1 > r.right {
		x1 = r.right
	}
	if x1 > x0 {
		cv.drawAlphaRoundRect(wizRect{x0, r.top, x1, r.bottom}, float64(s(3)), w.pal.accent, 255, 0, 0, 0)
	}
}

func (w *wizardState) cursorIsHand() bool {
	switch w.hover {
	case wizHoverBrowse, wizHoverChkDesk, wizHoverChkMenu, wizHoverChkAuto, wizHoverChkLaunch, wizHoverCancel, wizHoverPrimary:
		return !w.working
	}
	return false
}

func (w *wizardState) onMouseMove(hwnd syscall.Handle, x, y int32) {
	var cr wizRect
	procWizGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&cr)))
	w.layout(cr.right, cr.bottom)

	h := wizHoverNone
	switch {
	case wizPtIn(w.capCloseR, x, y):
		h = wizHoverClose
	case wizPtIn(w.capMinR, x, y):
		h = wizHoverMin
	case !w.working && wizPtIn(w.primBtnR, x, y):
		h = wizHoverPrimary
	case !w.working && wizPtIn(w.cancelBtnR, x, y):
		h = wizHoverCancel
	case !w.working && !w.done && w.mode == wizModeInstall && wizPtIn(w.browseBtnR, x, y):
		h = wizHoverBrowse
	case !w.working && !w.done && w.mode == wizModeInstall && wizPtIn(w.chkDeskR, x, y):
		h = wizHoverChkDesk
	case !w.working && !w.done && w.mode == wizModeInstall && wizPtIn(w.chkMenuR, x, y):
		h = wizHoverChkMenu
	case !w.working && !w.done && w.mode == wizModeInstall && wizPtIn(w.chkAutoR, x, y):
		h = wizHoverChkAuto
	case !w.working && !w.done && w.mode == wizModeInstall && wizPtIn(w.chkLaunchR, x, y):
		h = wizHoverChkLaunch
	case w.done && !w.statusErr && w.mode == wizModeInstall && wizPtIn(w.doneLaunchR, x, y):
		h = wizHoverChkLaunch
	}
	if h != w.hover {
		w.hover = h
		procWizInvalidateRect.Call(uintptr(hwnd), 0, 0)
	}
	if !w.tracking {
		var tme struct {
			cbSize    uint32
			flags     uint32
			hwnd      syscall.Handle
			hoverTime uint32
		}
		tme.cbSize = uint32(unsafe.Sizeof(tme))
		tme.flags = 0x00000002 // TME_LEAVE
		tme.hwnd = hwnd
		procWizTrackMouseEvent.Call(uintptr(unsafe.Pointer(&tme)))
		w.tracking = true
	}
}

func (w *wizardState) onClick(hwnd syscall.Handle, x, y int32) {
	var cr wizRect
	procWizGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&cr)))
	w.layout(cr.right, cr.bottom)

	switch {
	case wizPtIn(w.capCloseR, x, y):
		if !w.working {
			w.finishClose()
		}
	case wizPtIn(w.capMinR, x, y):
		procWizShowWindow.Call(uintptr(hwnd), 6) // SW_MINIMIZE
	case !w.working && wizPtIn(w.cancelBtnR, x, y):
		w.finishClose()
	case !w.working && wizPtIn(w.primBtnR, x, y):
		w.onPrimary()
	case !w.working && !w.done && w.mode == wizModeInstall && wizPtIn(w.browseBtnR, x, y):
		w.onBrowse()
	case !w.working && !w.done && w.mode == wizModeInstall && wizPtIn(w.chkDeskR, x, y):
		w.chkDesk = !w.chkDesk
		procWizInvalidateRect.Call(uintptr(hwnd), 0, 0)
	case !w.working && !w.done && w.mode == wizModeInstall && wizPtIn(w.chkMenuR, x, y):
		w.chkMenu = !w.chkMenu
		procWizInvalidateRect.Call(uintptr(hwnd), 0, 0)
	case !w.working && !w.done && w.mode == wizModeInstall && wizPtIn(w.chkAutoR, x, y):
		w.chkAuto = !w.chkAuto
		procWizInvalidateRect.Call(uintptr(hwnd), 0, 0)
	case !w.working && (!w.done || !w.statusErr) && w.mode == wizModeInstall && (wizPtIn(w.chkLaunchR, x, y) || wizPtIn(w.doneLaunchR, x, y)):
		w.chkLaunch = !w.chkLaunch
		procWizInvalidateRect.Call(uintptr(hwnd), 0, 0)
	}
}

func (w *wizardState) onBrowse() {
	if w.working || w.hEditPath == 0 {
		return
	}
	hr, _, _ := procWizCoInitializeEx.Call(0, 2)
	if int32(hr) >= 0 {
		defer procWizCoUninitialize.Call()
	}
	disp := make([]uint16, 260)
	bi := wizBrowseInfo{
		hwndOwner:      w.hwnd,
		pszDisplayName: &disp[0],
		lpszTitle:      wizUTF16Ptr("选择 轻渡 的安装文件夹"),
		ulFlags:        0x0001 | 0x0040 | 0x0010,
	}
	pidl, _, _ := procWizSHBrowseForFolderW.Call(uintptr(unsafe.Pointer(&bi)))
	if pidl == 0 {
		return
	}
	defer procWizCoTaskMemFree.Call(pidl)

	pathBuf := make([]uint16, 32768)
	ok, _, _ := procWizSHGetPathFromIDListW.Call(pidl, uintptr(unsafe.Pointer(&pathBuf[0])))
	if ok != 0 {
		chosen := strings.TrimSpace(syscall.UTF16ToString(pathBuf))
		if chosen != "" {
			if !strings.EqualFold(filepath.Base(chosen), "Ferry") {
				chosen = filepath.Join(chosen, "Ferry")
			}
			procWizSetWindowTextW.Call(uintptr(w.hEditPath), uintptr(unsafe.Pointer(wizUTF16Ptr(chosen))))
		}
	}
}

func (w *wizardState) onPrimary() {
	if w.working {
		return
	}
	if w.done && !w.statusErr {
		w.finishClose()
		return
	}

	if w.mode == wizModeUninstall {
		w.working = true
		w.statusErr = false
		w.statusText = "正在停止运行中进程并移除程序文件…"
		procWizSetTimer.Call(uintptr(w.hwnd), wizProgressTimer, 30, 0)
		procWizInvalidateRect.Call(uintptr(w.hwnd), 0, 1)

		dir := w.defaultDir
		hwnd := w.hwnd
		go func() {
			needSelf, err := remove(dir)
			w.mu.Lock()
			w.lastErr = err
			w.needSelfDelete = needSelf
			w.mu.Unlock()
			procWizPostMessageW.Call(uintptr(hwnd), wmAppWizardDone, 0, 0)
		}()
		return
	}

	dir := strings.TrimSpace(getWindowText(w.hEditPath))
	if dir == "" {
		w.statusErr = true
		w.statusText = "请选择或输入有效的安装目录。"
		procWizInvalidateRect.Call(uintptr(w.hwnd), 0, 1)
		return
	}
	w.launchDir = dir
	opts := installOptions{
		Dir:               dir,
		Autostart:         w.chkAuto,
		DesktopShortcut:   w.chkDesk,
		StartMenuShortcut: w.chkMenu,
		LaunchAfter:       false,
	}

	w.working = true
	w.done = false
	w.statusErr = false
	w.statusText = "正在写入程序文件与快捷方式，请稍候…"
	if w.hEditPath != 0 {
		procWizEnableWindow.Call(uintptr(w.hEditPath), 0)
	}
	procWizSetTimer.Call(uintptr(w.hwnd), wizProgressTimer, 30, 0)
	procWizInvalidateRect.Call(uintptr(w.hwnd), 0, 1)

	hwnd := w.hwnd
	go func() {
		err := installWithOptions(opts)
		w.mu.Lock()
		w.lastErr = err
		w.mu.Unlock()
		procWizPostMessageW.Call(uintptr(hwnd), wmAppWizardDone, 0, 0)
	}()
}

func (w *wizardState) onTaskDone() {
	procWizKillTimer.Call(uintptr(w.hwnd), wizProgressTimer)
	w.mu.Lock()
	err := w.lastErr
	w.mu.Unlock()

	w.working = false
	if err != nil {
		if os.IsPermission(err) && elevateSelf() {
			procWizDestroyWindow.Call(uintptr(w.hwnd))
			return
		}
		w.done = false
		w.statusErr = true
		if w.hEditPath != 0 {
			procWizEnableWindow.Call(uintptr(w.hEditPath), 1)
		}
		prefix := "安装失败："
		if w.mode == wizModeUninstall {
			prefix = "卸载失败："
		}
		w.statusText = prefix + err.Error()
		procWizInvalidateRect.Call(uintptr(w.hwnd), 0, 1)
		return
	}

	w.done = true
	w.statusErr = false
	if w.hEditPath != 0 {
		procWizShowWindow.Call(uintptr(w.hEditPath), 0) // SW_HIDE
	}
	procWizInvalidateRect.Call(uintptr(w.hwnd), 0, 1)
}

func (w *wizardState) finishClose() {
	if w.done && !w.statusErr {
		if w.mode == wizModeInstall && w.chkLaunch && w.launchDir != "" {
			exe := filepath.Join(w.launchDir, "bin", "ferry-desktop.exe")
			_ = exec.Command(exe).Start()
		} else if w.mode == wizModeUninstall && w.needSelfDelete {
			scheduleSelfDelete(w.defaultDir)
		}
	}
	procWizDestroyWindow.Call(uintptr(w.hwnd))
}

func getWindowText(hwnd syscall.Handle) string {
	n, _, _ := procWizGetWindowTextLengthW.Call(uintptr(hwnd))
	buf := make([]uint16, n+2)
	procWizGetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}
