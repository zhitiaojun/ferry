//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

var (
	wizUser32   = syscall.NewLazyDLL("user32.dll")
	wizGdi32    = syscall.NewLazyDLL("gdi32.dll")
	wizKernel32 = syscall.NewLazyDLL("kernel32.dll")
	wizShell32  = syscall.NewLazyDLL("shell32.dll")
	wizOle32    = syscall.NewLazyDLL("ole32.dll")

	procWizRegisterClassExW      = wizUser32.NewProc("RegisterClassExW")
	procWizCreateWindowExW       = wizUser32.NewProc("CreateWindowExW")
	procWizDefWindowProcW        = wizUser32.NewProc("DefWindowProcW")
	procWizDestroyWindow         = wizUser32.NewProc("DestroyWindow")
	procWizGetMessageW           = wizUser32.NewProc("GetMessageW")
	procWizIsDialogMessageW      = wizUser32.NewProc("IsDialogMessageW")
	procWizTranslateMessage      = wizUser32.NewProc("TranslateMessage")
	procWizDispatchMessageW      = wizUser32.NewProc("DispatchMessageW")
	procWizPostMessageW          = wizUser32.NewProc("PostMessageW")
	procWizPostQuitMessage       = wizUser32.NewProc("PostQuitMessage")
	procWizSendMessageW          = wizUser32.NewProc("SendMessageW")
	procWizGetWindowTextLengthW  = wizUser32.NewProc("GetWindowTextLengthW")
	procWizGetWindowTextW        = wizUser32.NewProc("GetWindowTextW")
	procWizSetWindowTextW        = wizUser32.NewProc("SetWindowTextW")
	procWizEnableWindow          = wizUser32.NewProc("EnableWindow")
	procWizShowWindow            = wizUser32.NewProc("ShowWindow")
	procWizUpdateWindow          = wizUser32.NewProc("UpdateWindow")
	procWizInvalidateRect        = wizUser32.NewProc("InvalidateRect")
	procWizBeginPaint            = wizUser32.NewProc("BeginPaint")
	procWizEndPaint              = wizUser32.NewProc("EndPaint")
	procWizFillRect              = wizUser32.NewProc("FillRect")
	procWizDrawTextW             = wizUser32.NewProc("DrawTextW")
	procWizGetClientRect         = wizUser32.NewProc("GetClientRect")
	procWizLoadCursorW           = wizUser32.NewProc("LoadCursorW")
	procWizLoadIconW             = wizUser32.NewProc("LoadIconW")
	procWizLoadImageW            = wizUser32.NewProc("LoadImageW")
	procWizSystemParametersInfoW = wizUser32.NewProc("SystemParametersInfoW")
	procWizSetDpiAwareCtx        = wizUser32.NewProc("SetProcessDpiAwarenessContext")
	procWizSetDPIAware           = wizUser32.NewProc("SetProcessDPIAware")
	procWizGetDpiForSystem       = wizUser32.NewProc("GetDpiForSystem")
	procWizGetDpiForWindow       = wizUser32.NewProc("GetDpiForWindow")
	procWizSetFocus              = wizUser32.NewProc("SetFocus")
	procWizGetModuleHandleW      = wizKernel32.NewProc("GetModuleHandleW")
	procWizCreateFontW           = wizGdi32.NewProc("CreateFontW")
	procWizSelectObject          = wizGdi32.NewProc("SelectObject")
	procWizDeleteObject          = wizGdi32.NewProc("DeleteObject")
	procWizCreateSolidBrush      = wizGdi32.NewProc("CreateSolidBrush")
	procWizCreatePen             = wizGdi32.NewProc("CreatePen")
	procWizRoundRect             = wizGdi32.NewProc("RoundRect")
	procWizSetBkMode             = wizGdi32.NewProc("SetBkMode")
	procWizSetTextColor          = wizGdi32.NewProc("SetTextColor")
	procWizSHBrowseForFolderW    = wizShell32.NewProc("SHBrowseForFolderW")
	procWizSHGetPathFromIDListW  = wizShell32.NewProc("SHGetPathFromIDListW")
	procWizCoInitializeEx        = wizOle32.NewProc("CoInitializeEx")
	procWizCoUninitialize        = wizOle32.NewProc("CoUninitialize")
	procWizCoTaskMemFree         = wizOle32.NewProc("CoTaskMemFree")
)

const (
	wizClassName = "FerrySetupWizard"

	wizIDInstall     = 1 // IDOK
	wizIDCancel      = 2 // IDCANCEL
	wizIDPathEdit    = 101
	wizIDBrowseBtn   = 102
	wizIDChkDesktop  = 103
	wizIDChkStart    = 104
	wizIDChkAuto     = 105
	wizIDChkLaunch   = 106
	wizIDStatusLabel = 107

	wmAppWizardDone = 0x8000 + 10
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

type wizDrawItemStruct struct {
	ctlType    uint32
	ctlID      uint32
	itemID     uint32
	itemAction uint32
	itemState  uint32
	hwndItem   syscall.Handle
	hDC        syscall.Handle
	rcItem     wizRect
	itemData   uintptr
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
	hwnd       syscall.Handle
	dpi        int32
	defaultDir string

	hEditPath   syscall.Handle
	hBtnBrowse  syscall.Handle
	hChkDesk    syscall.Handle
	hChkMenu    syscall.Handle
	hChkAuto    syscall.Handle
	hChkLaunch  syscall.Handle
	hLblStatus  syscall.Handle
	hBtnInstall syscall.Handle
	hBtnCancel  syscall.Handle

	fontTitle    syscall.Handle
	fontSub      syscall.Handle
	fontBold     syscall.Handle
	fontBody     syscall.Handle
	brushBg      syscall.Handle
	brushHdr     syscall.Handle
	brushCard    syscall.Handle
	brushTeal    syscall.Handle
	brushTealHot syscall.Handle
	brushMute    syscall.Handle
	hIcon        syscall.Handle
	tempIcon     string

	installing bool
	done       bool
	statusErr  bool
	wantLaunch bool
	launchDir  string

	mu      sync.Mutex
	lastErr error
}

var activeWizard *wizardState

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

func wizCreateFont(dpi, pt int32, weight int32) syscall.Handle {
	height := -wizScale(dpi, pt)
	h, _, _ := procWizCreateFontW.Call(
		uintptr(height), 0, 0, 0, uintptr(weight),
		0, 0, 0,
		1, // DEFAULT_CHARSET
		0, 0, 5, 0,
		uintptr(unsafe.Pointer(wizUTF16Ptr("Microsoft YaHei UI"))),
	)
	return syscall.Handle(h)
}

func runWizard(defaultDir string) int {
	runtime.LockOSThread()

	dpiRaw, _, _ := procWizGetDpiForSystem.Call()
	dpi := int32(dpiRaw)
	if dpi == 0 {
		dpi = 96
	}

	w := &wizardState{
		dpi:        dpi,
		defaultDir: defaultDir,
	}
	activeWizard = w

	w.brushBg = createBrush(0x00FCFAF8)      // #F8FAFC (RGB -> BGR)
	w.brushHdr = createBrush(0x0088940D)     // #0D9488
	w.brushCard = createBrush(0x00FFFFFF)    // #FFFFFF
	w.brushTeal = createBrush(0x0088940D)    // #0D9488
	w.brushTealHot = createBrush(0x006E760F) // #0F766E
	w.brushMute = createBrush(0x00B8A394)    // #94A3B8

	w.hIcon, w.tempIcon = loadPayloadIcon()
	defer w.cleanup()

	hInstRaw, _, _ := procWizGetModuleHandleW.Call(0)
	hInst := syscall.Handle(hInstRaw)
	cursor, _, _ := procWizLoadCursorW.Call(0, 32512) // IDC_ARROW
	icon := uintptr(w.hIcon)
	if icon == 0 {
		icon, _, _ = procWizLoadIconW.Call(0, 32512)
	}

	cls := wizUTF16Ptr(wizClassName)
	wc := wizWndClassEx{
		cbSize:        uint32(unsafe.Sizeof(wizWndClassEx{})),
		lpfnWndProc:   syscall.NewCallback(wizardWndProc),
		hInstance:     hInst,
		hIcon:         syscall.Handle(icon),
		hIconSm:       syscall.Handle(icon),
		hCursor:       syscall.Handle(cursor),
		hbrBackground: w.brushBg,
		lpszClassName: cls,
	}
	if r, _, _ := procWizRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return 1
	}

	winW := wizScale(dpi, 520)
	winH := wizScale(dpi, 440)
	x, y := centerOnWorkArea(winW, winH)

	// WS_OVERLAPPED | WS_CAPTION | WS_SYSMENU | WS_MINIMIZEBOX | WS_CLIPCHILDREN
	const style = 0x00000000 | 0x00C00000 | 0x00080000 | 0x00020000 | 0x02000000
	const exStyle = 0x00010000 // WS_EX_CONTROLPARENT

	hwndRaw, _, _ := procWizCreateWindowExW.Call(
		exStyle,
		uintptr(unsafe.Pointer(cls)),
		uintptr(unsafe.Pointer(wizUTF16Ptr("轻渡 · Ferry 安装向导"))),
		style,
		uintptr(x), uintptr(y), uintptr(winW), uintptr(winH),
		0, 0, uintptr(hInst), 0,
	)
	if hwndRaw == 0 {
		return 1
	}
	w.hwnd = syscall.Handle(hwndRaw)
	procWizShowWindow.Call(hwndRaw, 5) // SW_SHOW
	procWizUpdateWindow.Call(hwndRaw)

	var msg wizMsg
	for {
		r, _, _ := procWizGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		handled, _, _ := procWizIsDialogMessageW.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&msg)))
		if handled == 0 {
			procWizTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
			procWizDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
		}
	}
	return 0
}

func (w *wizardState) cleanup() {
	for _, h := range []syscall.Handle{
		w.fontTitle, w.fontSub, w.fontBold, w.fontBody,
		w.brushBg, w.brushHdr, w.brushCard, w.brushTeal, w.brushTealHot, w.brushMute,
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
	// IMAGE_ICON=1, LR_LOADFROMFILE=0x0010, LR_DEFAULTSIZE=0x0040
	h, _, _ := procWizLoadImageW.Call(0, uintptr(unsafe.Pointer(wizUTF16Ptr(tmp))), 1, 0, 0, 0x0010|0x0040)
	return syscall.Handle(h), tmp
}

func centerOnWorkArea(w, h int32) (int32, int32) {
	var rc wizRect
	const spiGetWorkArea = 0x0030
	procWizSystemParametersInfoW.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&rc)), 0)
	if rc.right <= rc.left || rc.bottom <= rc.top {
		return 100, 100
	}
	x := rc.left + (rc.right-rc.left-w)/2
	y := rc.top + (rc.bottom-rc.top-h)/2
	return x, y
}

func wizardWndProc(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	w := activeWizard
	const (
		wmCreate         = 0x0001
		wmDestroy        = 0x0002
		wmPaint          = 0x000F
		wmClose          = 0x0010
		wmDrawItem       = 0x002B
		wmCommand        = 0x0111
		wmCtlColorEdit   = 0x0133
		wmCtlColorStatic = 0x0138
	)

	switch msg {
	case wmCreate:
		if w != nil {
			w.hwnd = hwnd
			if d, _, _ := procWizGetDpiForWindow.Call(uintptr(hwnd)); d != 0 {
				w.dpi = int32(d)
			}
			w.initControls()
		}
		return 0

	case wmPaint:
		if w != nil {
			w.paint(hwnd)
			return 0
		}

	case wmCtlColorStatic:
		if w != nil {
			hdc := syscall.Handle(wParam)
			procWizSetBkMode.Call(uintptr(hdc), 1) // TRANSPARENT
			ctrl := syscall.Handle(lParam)
			if ctrl == w.hLblStatus && w.statusErr {
				procWizSetTextColor.Call(uintptr(hdc), 0x002626DC) // #DC2626
			} else if ctrl == w.hLblStatus && w.done {
				procWizSetTextColor.Call(uintptr(hdc), 0x006E760F) // #0F766E
			} else {
				procWizSetTextColor.Call(uintptr(hdc), 0x003B291E) // #1E293B
			}
			return uintptr(w.brushBg)
		}

	case wmCtlColorEdit:
		if w != nil {
			hdc := syscall.Handle(wParam)
			procWizSetBkMode.Call(uintptr(hdc), 2) // OPAQUE
			procWizSetTextColor.Call(uintptr(hdc), 0x003B291E)
			return uintptr(w.brushCard)
		}

	case wmDrawItem:
		if w != nil && wParam == wizIDInstall {
			di := (*wizDrawItemStruct)(wizPtrAt(lParam))
			w.drawPrimaryButton(di)
			return 1
		}

	case wmCommand:
		if w != nil {
			id := uint16(wParam & 0xffff)
			code := uint16((wParam >> 16) & 0xffff)
			if code == 0 { // BN_CLICKED
				switch id {
				case wizIDBrowseBtn:
					w.onBrowse()
					return 0
				case wizIDInstall:
					w.onPrimary()
					return 0
				case wizIDCancel:
					if !w.installing {
						w.finishClose()
					}
					return 0
				}
			}
		}

	case wmAppWizardDone:
		if w != nil {
			w.onInstallDone()
		}
		return 0

	case wmClose:
		if w != nil {
			if w.installing {
				return 0
			}
			w.finishClose()
			return 0
		}

	case wmDestroy:
		procWizPostQuitMessage.Call(0)
		return 0
	}

	r, _, _ := procWizDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return r
}

func (w *wizardState) s(v int32) int32 { return wizScale(w.dpi, v) }

func (w *wizardState) initControls() {
	w.fontTitle = wizCreateFont(w.dpi, 22, 700)
	w.fontSub = wizCreateFont(w.dpi, 13, 400)
	w.fontBold = wizCreateFont(w.dpi, 14, 700)
	w.fontBody = wizCreateFont(w.dpi, 14, 400)

	var rc wizRect
	procWizGetClientRect.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&rc)))
	cw := rc.right - rc.left
	pad := w.s(28)
	y := w.s(98)

	// Section label: install directory
	w.createStatic("安装目录", pad, y, cw-pad*2, w.s(20), w.fontBold, 0)
	y += w.s(24)

	btnBrowseW := w.s(84)
	gap := w.s(10)
	editW := cw - pad*2 - btnBrowseW - gap
	rowH := w.s(28)

	// WS_CHILD|WS_VISIBLE|WS_TABSTOP|ES_AUTOHSCROLL = 0x50010080
	// WS_EX_CLIENTEDGE = 0x00000200
	w.hEditPath = createChildEx(0x00000200, "EDIT", w.defaultDir, 0x50010080, pad, y, editW, rowH, w.hwnd, wizIDPathEdit, w.fontBody)
	// EM_SETMARGINS = 0x00D3, EC_LEFTMARGIN|EC_RIGHTMARGIN = 3
	procWizSendMessageW.Call(uintptr(w.hEditPath), 0x00D3, 3, uintptr(w.s(6))|uintptr(w.s(6)<<16))

	// BS_PUSHBUTTON = 0x00000000
	w.hBtnBrowse = createChildEx(0, "BUTTON", "浏览...", 0x50010000, pad+editW+gap, y, btnBrowseW, rowH, w.hwnd, wizIDBrowseBtn, w.fontBody)
	y += w.s(44)

	// Section label: options
	w.createStatic("安装选项", pad, y, cw-pad*2, w.s(20), w.fontBold, 0)
	y += w.s(26)

	chkH := w.s(24)
	chkW := cw - pad*2
	// BS_AUTOCHECKBOX = 0x00000003
	w.hChkDesk = createChildEx(0, "BUTTON", "创建桌面快捷方式", 0x50010003, pad+w.s(4), y, chkW, chkH, w.hwnd, wizIDChkDesktop, w.fontBody)
	y += w.s(28)
	w.hChkMenu = createChildEx(0, "BUTTON", "创建开始菜单快捷方式", 0x50010003, pad+w.s(4), y, chkW, chkH, w.hwnd, wizIDChkStart, w.fontBody)
	y += w.s(28)
	w.hChkAuto = createChildEx(0, "BUTTON", "开机自动启动（静默驻留托盘）", 0x50010003, pad+w.s(4), y, chkW, chkH, w.hwnd, wizIDChkAuto, w.fontBody)
	y += w.s(28)
	w.hChkLaunch = createChildEx(0, "BUTTON", "安装完成后立即启动 轻渡", 0x50010003, pad+w.s(4), y, chkW, chkH, w.hwnd, wizIDChkLaunch, w.fontBody)
	y += w.s(36)

	// Check all options by default (BM_SETCHECK=0x00F1, BST_CHECKED=1)
	for _, h := range []syscall.Handle{w.hChkDesk, w.hChkMenu, w.hChkAuto, w.hChkLaunch} {
		procWizSendMessageW.Call(uintptr(h), 0x00F1, 1, 0)
	}

	// Status label (multi-line)
	w.hLblStatus = w.createStatic("准备就绪，将保留您已有的设备密钥与配对记录。", pad, y, cw-pad*2, w.s(38), w.fontBody, wizIDStatusLabel)

	// Bottom buttons
	btnH := w.s(34)
	btnInstallW := w.s(112)
	btnCancelW := w.s(86)
	btnY := rc.bottom - pad - btnH
	cancelX := cw - pad - btnCancelW
	installX := cancelX - gap - btnInstallW

	w.hBtnInstall = createChildEx(0, "BUTTON", "立即安装", 0x5001000B, installX, btnY, btnInstallW, btnH, w.hwnd, wizIDInstall, w.fontBold) // BS_OWNERDRAW=0xB
	w.hBtnCancel = createChildEx(0, "BUTTON", "取消", 0x50010000, cancelX, btnY, btnCancelW, btnH, w.hwnd, wizIDCancel, w.fontBody)
	procWizSetFocus.Call(uintptr(w.hBtnInstall))
}

func (w *wizardState) createStatic(text string, x, y, width, height int32, font syscall.Handle, id uint16) syscall.Handle {
	return createChildEx(0, "STATIC", text, 0x50000000, x, y, width, height, w.hwnd, id, font)
}

func createChildEx(exStyle uint32, className, text string, style uint32, x, y, width, height int32, parent syscall.Handle, id uint16, font syscall.Handle) syscall.Handle {
	hInst, _, _ := procWizGetModuleHandleW.Call(0)
	h, _, _ := procWizCreateWindowExW.Call(
		uintptr(exStyle),
		uintptr(unsafe.Pointer(wizUTF16Ptr(className))),
		uintptr(unsafe.Pointer(wizUTF16Ptr(text))),
		uintptr(style),
		uintptr(x), uintptr(y), uintptr(width), uintptr(height),
		uintptr(parent), uintptr(id), hInst, 0,
	)
	if h != 0 && font != 0 {
		procWizSendMessageW.Call(h, 0x0030, uintptr(font), 1) // WM_SETFONT
	}
	return syscall.Handle(h)
}

func (w *wizardState) paint(hwnd syscall.Handle) {
	var ps wizPaintStruct
	hdcRaw, _, _ := procWizBeginPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))
	hdc := syscall.Handle(hdcRaw)
	defer procWizEndPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))

	var rc wizRect
	procWizGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&rc)))

	// Fill main background
	procWizFillRect.Call(uintptr(hdc), uintptr(unsafe.Pointer(&rc)), uintptr(w.brushBg))

	// Draw teal header banner
	hdrRc := wizRect{left: 0, top: 0, right: rc.right, bottom: w.s(78)}
	procWizFillRect.Call(uintptr(hdc), uintptr(unsafe.Pointer(&hdrRc)), uintptr(w.brushHdr))

	procWizSetBkMode.Call(uintptr(hdc), 1) // TRANSPARENT
	procWizSetTextColor.Call(uintptr(hdc), 0x00FFFFFF)

	pad := w.s(28)
	titleRc := wizRect{left: pad, top: w.s(15), right: rc.right - pad, bottom: w.s(44)}
	oldFont, _, _ := procWizSelectObject.Call(uintptr(hdc), uintptr(w.fontTitle))
	drawTextSingle(hdc, "轻渡 · Ferry", &titleRc)

	subRc := wizRect{left: pad, top: w.s(46), right: rc.right - pad, bottom: w.s(68)}
	procWizSelectObject.Call(uintptr(hdc), uintptr(w.fontSub))
	procWizSetTextColor.Call(uintptr(hdc), 0x00EEFDF8)
	drawTextSingle(hdc, "v1.0.0 Windows 安装向导  ·  局域网直连 · UDP 打洞 · 服务器中继", &subRc)

	procWizSelectObject.Call(uintptr(hdc), oldFont)
}

func drawTextSingle(hdc syscall.Handle, s string, rc *wizRect) {
	const flags = 0x00000020 | 0x00000800 // DT_SINGLELINE | DT_NOPREFIX
	p := syscall.StringToUTF16(s)
	procWizDrawTextW.Call(uintptr(hdc), uintptr(unsafe.Pointer(&p[0])), uintptr(len(p)-1), uintptr(unsafe.Pointer(rc)), flags)
}

func (w *wizardState) drawPrimaryButton(di *wizDrawItemStruct) {
	hdc := di.hDC
	rc := di.rcItem
	procWizFillRect.Call(uintptr(hdc), uintptr(unsafe.Pointer(&rc)), uintptr(w.brushBg))

	const (
		odsSelected = 0x0001
		odsDisabled = 0x0004
	)
	brush := w.brushTeal
	penColor := uint32(0x0088940D)
	if di.itemState&odsDisabled != 0 {
		brush = w.brushMute
		penColor = 0x00B8A394
	} else if di.itemState&odsSelected != 0 {
		brush = w.brushTealHot
		penColor = 0x006E760F
	}
	pen, _, _ := procWizCreatePen.Call(0, 1, uintptr(penColor))
	oldPen, _, _ := procWizSelectObject.Call(uintptr(hdc), pen)
	oldBrush, _, _ := procWizSelectObject.Call(uintptr(hdc), uintptr(brush))
	r := w.s(8)
	procWizRoundRect.Call(uintptr(hdc), uintptr(rc.left), uintptr(rc.top), uintptr(rc.right), uintptr(rc.bottom), uintptr(r), uintptr(r))
	procWizSelectObject.Call(uintptr(hdc), oldBrush)
	procWizSelectObject.Call(uintptr(hdc), oldPen)
	procWizDeleteObject.Call(pen)

	label := getWindowText(di.hwndItem)
	if label == "" {
		label = "立即安装"
	}
	procWizSetBkMode.Call(uintptr(hdc), 1)
	procWizSetTextColor.Call(uintptr(hdc), 0x00FFFFFF)
	oldFont, _, _ := procWizSelectObject.Call(uintptr(hdc), uintptr(w.fontBold))
	p := syscall.StringToUTF16(label)
	const dtFlags = 0x0001 | 0x0004 | 0x0020 | 0x0800 // DT_CENTER|DT_VCENTER|DT_SINGLELINE|DT_NOPREFIX
	procWizDrawTextW.Call(uintptr(hdc), uintptr(unsafe.Pointer(&p[0])), uintptr(len(p)-1), uintptr(unsafe.Pointer(&rc)), dtFlags)
	procWizSelectObject.Call(uintptr(hdc), oldFont)
}

func getWindowText(hwnd syscall.Handle) string {
	n, _, _ := procWizGetWindowTextLengthW.Call(uintptr(hwnd))
	buf := make([]uint16, n+2)
	procWizGetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}

func isChecked(hwnd syscall.Handle) bool {
	// BM_GETCHECK = 0x00F0, BST_CHECKED = 1
	r, _, _ := procWizSendMessageW.Call(uintptr(hwnd), 0x00F0, 0, 0)
	return r == 1
}

func setWindowEnabled(hwnd syscall.Handle, on bool) {
	v := uintptr(0)
	if on {
		v = 1
	}
	procWizEnableWindow.Call(uintptr(hwnd), v)
}

func setWindowText(hwnd syscall.Handle, text string) {
	procWizSetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(wizUTF16Ptr(text))))
}

func (w *wizardState) setControlsEnabled(on bool) {
	for _, h := range []syscall.Handle{w.hEditPath, w.hBtnBrowse, w.hChkDesk, w.hChkMenu, w.hChkAuto, w.hChkLaunch, w.hBtnCancel, w.hBtnInstall} {
		setWindowEnabled(h, on)
	}
	procWizInvalidateRect.Call(uintptr(w.hBtnInstall), 0, 1)
}

func (w *wizardState) onBrowse() {
	if w.installing {
		return
	}
	// COINIT_APARTMENTTHREADED = 2
	hr, _, _ := procWizCoInitializeEx.Call(0, 2)
	if int32(hr) >= 0 {
		defer procWizCoUninitialize.Call()
	}

	disp := make([]uint16, 260)
	bi := wizBrowseInfo{
		hwndOwner:      w.hwnd,
		pszDisplayName: &disp[0],
		lpszTitle:      wizUTF16Ptr("选择 轻渡 · Ferry 的安装文件夹"),
		ulFlags:        0x0001 | 0x0040 | 0x0010, // BIF_RETURNONLYFSDIRS | BIF_NEWDIALOGSTYLE | BIF_EDITBOX
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
			setWindowText(w.hEditPath, chosen)
		}
	}
}

func (w *wizardState) onPrimary() {
	if w.installing {
		return
	}
	if w.done && !w.statusErr {
		w.finishClose()
		return
	}

	dir := strings.TrimSpace(getWindowText(w.hEditPath))
	if dir == "" {
		w.statusErr = true
		setWindowText(w.hLblStatus, "请选择或输入有效的安装目录。")
		procWizInvalidateRect.Call(uintptr(w.hLblStatus), 0, 1)
		return
	}

	w.wantLaunch = isChecked(w.hChkLaunch)
	w.launchDir = dir
	opts := installOptions{
		Dir:               dir,
		Autostart:         isChecked(w.hChkAuto),
		DesktopShortcut:   isChecked(w.hChkDesk),
		StartMenuShortcut: isChecked(w.hChkMenu),
		LaunchAfter:       false, // Launched smoothly when user clicks "完成".
	}

	w.installing = true
	w.done = false
	w.statusErr = false
	w.setControlsEnabled(false)
	setWindowText(w.hBtnInstall, "安装中...")
	setWindowText(w.hLblStatus, "正在写入程序文件与快捷方式，请稍候...")
	procWizInvalidateRect.Call(uintptr(w.hLblStatus), 0, 1)

	hwnd := w.hwnd
	go func() {
		err := installWithOptions(opts)
		w.mu.Lock()
		w.lastErr = err
		w.mu.Unlock()
		procWizPostMessageW.Call(uintptr(hwnd), wmAppWizardDone, 0, 0)
	}()
}

func (w *wizardState) onInstallDone() {
	w.mu.Lock()
	err := w.lastErr
	w.mu.Unlock()

	w.installing = false
	if err != nil {
		w.done = false
		w.statusErr = true
		w.setControlsEnabled(true)
		setWindowText(w.hBtnInstall, "重新安装")
		setWindowText(w.hLblStatus, fmt.Sprintf("安装失败：%v", err))
		procWizInvalidateRect.Call(uintptr(w.hLblStatus), 0, 1)
		return
	}

	w.done = true
	w.statusErr = false
	setWindowEnabled(w.hBtnInstall, true)
	setWindowText(w.hBtnInstall, "完成")
	procWizShowWindow.Call(uintptr(w.hBtnCancel), 0) // SW_HIDE
	setWindowText(w.hLblStatus, "安装完成！已安装到："+w.launchDir)
	procWizInvalidateRect.Call(uintptr(w.hLblStatus), 0, 1)
	procWizInvalidateRect.Call(uintptr(w.hBtnInstall), 0, 1)
	procWizSetFocus.Call(uintptr(w.hBtnInstall))
}

func (w *wizardState) finishClose() {
	if w.done && !w.statusErr && w.wantLaunch && w.launchDir != "" {
		exe := filepath.Join(w.launchDir, "bin", "ferry-desktop.exe")
		cmd := exec.Command(exe)
		_ = cmd.Start()
	}
	procWizDestroyWindow.Call(uintptr(w.hwnd))
}
