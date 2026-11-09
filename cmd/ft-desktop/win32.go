//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

// Win32 bindings shared by the tray shell and the drop window. No cgo: the
// three DLLs below are the only ones used, always through syscall.NewLazyDLL.
var (
	user32   = syscall.NewLazyDLL("user32.dll")
	shell32  = syscall.NewLazyDLL("shell32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procRegisterClassExW      = user32.NewProc("RegisterClassExW")
	procCreateWindowExW       = user32.NewProc("CreateWindowExW")
	procDefWindowProcW        = user32.NewProc("DefWindowProcW")
	procDefWindowProc         = user32.NewProc("DefWindowProcW")
	procDestroyWindow         = user32.NewProc("DestroyWindow")
	procGetMessageW           = user32.NewProc("GetMessageW")
	procTranslateMessage      = user32.NewProc("TranslateMessage")
	procDispatchMessageW      = user32.NewProc("DispatchMessageW")
	procPostQuitMessage       = user32.NewProc("PostQuitMessage")
	procPostMessageW          = user32.NewProc("PostMessageW")
	procGetModuleHandleW      = kernel32.NewProc("GetModuleHandleW")
	procLoadCursorW           = user32.NewProc("LoadCursorW")
	procLoadIconW             = user32.NewProc("LoadIconW")
	procSetForegroundWindow   = user32.NewProc("SetForegroundWindow")
	procGetCursorPos          = user32.NewProc("GetCursorPos")
	procCreatePopupMenu       = user32.NewProc("CreatePopupMenu")
	procAppendMenuW           = user32.NewProc("AppendMenuW")
	procDestroyMenu           = user32.NewProc("DestroyMenu")
	procTrackPopupMenu        = user32.NewProc("TrackPopupMenu")
	procShellNotifyIconW      = shell32.NewProc("Shell_NotifyIconW")
	procShellExecuteW         = user32.NewProc("ShellExecuteW")
	procMessageBoxW           = user32.NewProc("MessageBoxW")
	procGetWindowTextW        = user32.NewProc("GetWindowTextW")
	procGetWindowRect         = user32.NewProc("GetWindowRect")
	procSetWindowPos          = user32.NewProc("SetWindowPos")
	procShowWindow            = user32.NewProc("ShowWindow")
	procUpdateWindow          = user32.NewProc("UpdateWindow")
	procBeginPaint            = user32.NewProc("BeginPaint")
	procEndPaint              = user32.NewProc("EndPaint")
	procFillRect              = user32.NewProc("FillRect")
	procDrawTextW             = user32.NewProc("DrawTextW")
	procGetClientRect         = user32.NewProc("GetClientRect")
	procSendMessageW          = user32.NewProc("SendMessageW")
	procDragAcceptFiles       = shell32.NewProc("DragAcceptFiles")
	procDragQueryFileW        = shell32.NewProc("DragQueryFileW")
	procDragFinish            = shell32.NewProc("DragFinish")
	procSystemParametersInfoW = user32.NewProc("SystemParametersInfoW")
	procInvalidateRect        = user32.NewProc("InvalidateRect")
	procSetDpiAwareCtx        = user32.NewProc("SetProcessDpiAwarenessContext")
	procSetDPIAware           = user32.NewProc("SetProcessDPIAware")
	procGetDpiForWindow       = user32.NewProc("GetDpiForWindow")
	procGetDpiForSystem       = user32.NewProc("GetDpiForSystem")
)

const (
	wsPopup        = 0x80000000
	wsVisible      = 0x10000000
	wsExTopmost    = 0x00000008
	wsExToolWindow = 0x00000080
	wsExAppWindow  = 0x00040000

	wmDestroy   = 0x0002
	wmClose     = 0x0010
	wmPaint     = 0x000F
	wmCommand   = 0x0111
	wmDropFiles = 0x0233
	wmRButtonUp = 0x0205
	wmLButtonUp = 0x0202
	wmApp       = 0x8000
	wmTray      = wmApp + 1

	spiGetWorkArea = 0x0030
	smCXScreen     = 0
	smCYScreen     = 1

	dtCenter     = 0x0001
	dtVCenter    = 0x0004
	dtSingleLine = 0x0020
	dtNoPrefix   = 0x0800

	idcArrow       = 32512
	idiApplication = 32512
	defaultGUIFont = 17
	bkTransparent  = 1
	colorWindow    = 6

	swShow = 5
	swHide = 0

	mfString    = 0x0000
	mfSeparator = 0x0800
	mfChecked   = 0x0008
	mfGrayed    = 0x0001

	tpmLeftAlign   = 0x0000
	tpmRightButton = 0x0002
	tpmReturnCmd   = 0x0100

	nimAdd    = 0x00000000
	nimModify = 0x00000001
	nimDelete = 0x00000002

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004
	nifInfo    = 0x00000010

	niifInfo = 0x00000001
	niifUser = 0x00000004

	mbOK       = 0x0000
	mbIconInfo = 0x0040
)

type wndClassExW struct {
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

type msgT struct {
	hwnd    syscall.Handle
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      pointT
}

type pointT struct{ x, y int32 }

type rectT struct{ left, top, right, bottom int32 }

type paintStruct struct {
	hdc         syscall.Handle
	fErase      int32
	rcPaint     rectT
	fRestore    int32
	fIncUpdate  int32
	rgbReserved [32]byte
}

// notifyIconData mirrors NOTIFYICONDATAW (Vista+ layout); cbSize is filled at
// call time so the same struct works on 32- and 64-bit Windows.
type notifyIconData struct {
	cbSize           uint32
	hWnd             syscall.Handle
	uID              uint32
	uFlags           uint32
	uCallbackMessage uint32
	hIcon            syscall.Handle
	szTip            [128]uint16
	dwState          uint32
	dwStateMask      uint32
	szInfo           [256]uint16
	uVersion         uint32
	szInfoTitle      [64]uint16
	dwInfoFlags      uint32
	guidItem         [16]byte
	hBalloonIcon     syscall.Handle
}

func utf16Ptr(s string) *uint16 {
	p, err := syscall.UTF16PtrFromString(s)
	if err != nil {
		return nil
	}
	return p
}

func utf16From(s string) [256]uint16 {
	var out [256]uint16
	copy(out[:], syscall.StringToUTF16(s))
	return out
}

// utf16Into copies s (truncated) into a fixed-size Win32 character array.
func utf16Into(dst []uint16, s string) {
	for i := range dst {
		dst[i] = 0
	}
	src := syscall.StringToUTF16(s)
	n := len(src) - 1 // last element is the terminator
	if n > len(dst)-1 {
		n = len(dst) - 1
	}
	copy(dst[:n], src[:n])
}

func loword(v uintptr) uint16 { return uint16(v & 0xffff) }
func hiword(v uintptr) uint16 { return uint16((v >> 16) & 0xffff) }

func getModuleHandle() syscall.Handle {
	h, _, _ := procGetModuleHandleW.Call(0)
	return syscall.Handle(h)
}

func messageBox(title, text string) {
	procMessageBoxW.Call(0, uintptr(unsafe.Pointer(utf16Ptr(text))),
		uintptr(unsafe.Pointer(utf16Ptr(title))), mbOK|mbIconInfo)
}

func shellOpen(path string) {
	op := utf16Ptr("open")
	procShellExecuteW.Call(0, uintptr(unsafe.Pointer(op)), uintptr(unsafe.Pointer(utf16Ptr(path))), 0, 0, swShow)
}

// setDPIAware makes the process per-monitor DPI aware. Without it Windows
// bitmap-stretches our windows (blurry text) and reports virtualized
// coordinates, which is why the drop panel looked fuzzy and misplaced.
func setDPIAware() {
	const perMonitorV2 = ^uintptr(0) - 3 // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 == -4
	if r, _, _ := procSetDpiAwareCtx.Call(perMonitorV2); r != 0 {
		return
	}
	procSetDPIAware.Call()
}

// scaleForDPI converts design pixels (96 dpi) to device pixels.
func scaleForDPI(hwnd syscall.Handle, v int32) int32 {
	dpi, _, _ := procGetDpiForWindow.Call(uintptr(hwnd))
	if dpi == 0 {
		dpi = 96
	}
	return int32(float64(v) * float64(dpi) / 96.0)
}
