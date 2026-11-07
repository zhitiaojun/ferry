//go:build windows

// Command ft-desktop is the Windows desktop shell of filetransfer.
//
// This file is a technical pre-research prototype of the "drop window": a
// small always-on-top box at the bottom-right of the screen that accepts files
// dragged out of Windows Explorer and reports their absolute paths as JSONL.
//
// # Known limitation (found during pre-research)
//
// Windows offers no public API to detect that Explorer has *started* a drag
// anywhere on the screen. The only supported hook, an OLE IDropTarget
// registered with RegisterDragDrop, is woken up when the cursor enters the
// registered window -- Explorer never notifies a third party that a drag
// gesture began. There is no documented "drag started" event, and the usual
// workarounds (a WH_MOUSE_LL hook polling the left button plus inspection of
// shell window classes, or watching the undocumented OLE "DoDragDrop" worker
// window) are hacks that break across Windows versions and are blocked
// entirely when Explorer runs at a higher integrity level than the target.
//
// Therefore this prototype implements the other variant of the idea: an
// always-present, always-on-top drop target -- "drop the files onto this box"
// instead of "the box pops up when you start dragging". The "pop up on drag
// start" variant is not implementable without those hacks, so it is
// deliberately not attempted here.
//
// No cgo: user32/shell32/gdi32 are reached through syscall.NewLazyDLL.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Window styles and messages.
const (
	wsPopup        = 0x80000000 // WS_POPUP
	wsVisible      = 0x10000000 // WS_VISIBLE
	wsExTopmost    = 0x00000008 // WS_EX_TOPMOST
	wsExToolWindow = 0x00000080 // WS_EX_TOOLWINDOW
	wsExNoActivate = 0x08000000 // WS_EX_NOACTIVATE

	wmDestroy   = 0x0002 // WM_DESTROY
	wmClose     = 0x0010 // WM_CLOSE
	wmPaint     = 0x000F // WM_PAINT
	wmDropFiles = 0x0233 // WM_DROPFILES registers a window as a drop target.

	spiGetWorkArea = 0x0030 // SPI_GETWORKAREA
	smCXScreen     = 0      // SM_CXSCREEN
	smCYScreen     = 1      // SM_CYSCREEN

	dtCenter     = 0x0001 // DT_CENTER
	dtVCenter    = 0x0004 // DT_VCENTER
	dtSingleLine = 0x0020 // DT_SINGLELINE
	dtNoPrefix   = 0x0800 // DT_NOPREFIX: never treat '&' as a mnemonic

	idcArrow = 32512 // IDC_ARROW

	defaultGUIFont = 17 // DEFAULT_GUI_FONT stock object
	bkTransparent  = 1  // TRANSPARENT
	colorWindow    = 6  // COLOR_WINDOW + 1: system-colored background brush

	maxPathChars = 32768 // a drop can carry \\?\ extended-length paths
)

var (
	user32  = syscall.NewLazyDLL("user32.dll")
	shell32 = syscall.NewLazyDLL("shell32.dll")
	gdi32   = syscall.NewLazyDLL("gdi32.dll")

	procRegisterClassExW      = user32.NewProc("RegisterClassExW")
	procCreateWindowExW       = user32.NewProc("CreateWindowExW")
	procDefWindowProcW        = user32.NewProc("DefWindowProcW")
	procDestroyWindow         = user32.NewProc("DestroyWindow")
	procPostQuitMessage       = user32.NewProc("PostQuitMessage")
	procGetMessageW           = user32.NewProc("GetMessageW")
	procTranslateMessage      = user32.NewProc("TranslateMessage")
	procDispatchMessageW      = user32.NewProc("DispatchMessageW")
	procBeginPaint            = user32.NewProc("BeginPaint")
	procEndPaint              = user32.NewProc("EndPaint")
	procGetClientRect         = user32.NewProc("GetClientRect")
	procDrawTextW             = user32.NewProc("DrawTextW")
	procSetWindowTextW        = user32.NewProc("SetWindowTextW")
	procInvalidateRect        = user32.NewProc("InvalidateRect")
	procLoadCursorW           = user32.NewProc("LoadCursorW")
	procSystemParametersInfoW = user32.NewProc("SystemParametersInfoW")
	procGetSystemMetrics      = user32.NewProc("GetSystemMetrics")
	procSetProcessDPIAware    = user32.NewProc("SetProcessDPIAware")
	procSetBkMode             = gdi32.NewProc("SetBkMode")
	procGetStockObject        = gdi32.NewProc("GetStockObject")
	procSelectObject          = gdi32.NewProc("SelectObject")
	procDragAcceptFiles       = shell32.NewProc("DragAcceptFiles")
	procDragQueryFileW        = shell32.NewProc("DragQueryFileW")
	procDragFinish            = shell32.NewProc("DragFinish")
)

// Win32 structures (amd64 layout).

type rect struct {
	left, top, right, bottom int32
}

type point struct {
	x, y int32
}

type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     windows.Handle
	hIcon         windows.Handle
	hCursor       windows.Handle
	hbrBackground windows.Handle
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       windows.Handle
}

type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

type paintStruct struct {
	hdc         uintptr
	fErase      int32
	rcPaint     rect
	fRestore    int32
	fIncUpdate  int32
	rgbReserved [32]byte
}

// dropEvent is the single JSON line emitted per WM_DROPFILES.
type dropEvent struct {
	Event string   `json:"event"`
	TS    string   `json:"ts"`
	Files []string `json:"files"`
}

// State touched only from the message loop goroutine.
var (
	// statusText is what WM_PAINT renders; the drop handler replaces it.
	statusText = "拖文件到这里"
	// logPath is the optional -log destination ("" means stdout only).
	logPath string
)

func main() {
	logFlag := flag.String("log", "", "append each drop event as a JSON line to this file (created if missing)")
	sizeFlag := flag.String("size", "260x90", "window size as WxH in pixels")
	marginFlag := flag.Int("margin", 16, "gap between the window and the screen edge, in pixels")
	posFlag := flag.String("pos", "", "window position as X,Y in pixels (default: bottom-right corner)")
	flag.Parse()

	width, height, err := parseSize(*sizeFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ft-desktop: invalid -size %q: %v\n", *sizeFlag, err)
		os.Exit(2)
	}
	if *marginFlag < 0 {
		fmt.Fprintf(os.Stderr, "ft-desktop: invalid -margin %d: must be >= 0\n", *marginFlag)
		os.Exit(2)
	}
	margin := int32(*marginFlag)

	var pos *[2]int32
	if *posFlag != "" {
		x, y, err := parsePos(*posFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ft-desktop: invalid -pos %q: %v\n", *posFlag, err)
			os.Exit(2)
		}
		pos = &[2]int32{x, y}
	}

	// Validate the log destination up front so a typo fails at launch instead
	// of silently losing the first drop.
	logPath = *logFlag
	if logPath != "" {
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ft-desktop: cannot open -log file: %v\n", err)
			os.Exit(2)
		}
		f.Close()
	}

	// Per-monitor-unaware processes get virtualized coordinates and a blurry
	// window under non-100% scaling. Becoming DPI-aware keeps -size and
	// -margin literal pixels; failure (pre-Vista) is harmless.
	procSetProcessDPIAware.Call()

	// hInstance of this executable; the class must be registered against it.
	// GetModuleHandleEx(0, nil, &h) is the "handle of the calling process"
	// form (x/sys/windows does not expose plain GetModuleHandle).
	var hinst windows.Handle
	if err := windows.GetModuleHandleEx(0, nil, &hinst); err != nil {
		fmt.Fprintf(os.Stderr, "ft-desktop: GetModuleHandleEx: %v\n", err)
		os.Exit(1)
	}

	if err := run(hinst, width, height, margin, pos); err != nil {
		fmt.Fprintf(os.Stderr, "ft-desktop: %v\n", err)
		os.Exit(1)
	}
}

// parseSize parses "WxH" (an 'x' or 'X' separator).
func parseSize(s string) (int32, int32, error) {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == 'x' || r == 'X' })
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("want WxH, e.g. 260x90 (two numbers separated by 'x')")
	}
	w, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("width: %v", err)
	}
	h, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("height: %v", err)
	}
	if w <= 0 || h <= 0 {
		return 0, 0, fmt.Errorf("width and height must be positive")
	}
	// A sanity bound: a typo like 2600000x900 should fail loudly rather than
	// ask the window manager for a multi-million-pixel popup.
	if w > 10000 || h > 10000 {
		return 0, 0, fmt.Errorf("width and height must be <= 10000")
	}
	return int32(w), int32(h), nil
}

// parsePos parses "X,Y" (two integers separated by a comma).
func parsePos(s string) (int32, int32, error) {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == ',' })
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("want X,Y, e.g. 100,200 (two numbers separated by ',')")
	}
	x, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("x: %v", err)
	}
	y, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("y: %v", err)
	}
	return int32(x), int32(y), nil
}

// run creates the window, registers it as a drop target and pumps messages.
func run(hinst windows.Handle, width, height, margin int32, pos *[2]int32) error {
	className, err := windows.UTF16PtrFromString("ftDropWindow")
	if err != nil {
		return err
	}
	title, err := windows.UTF16PtrFromString("ft drop")
	if err != nil {
		return err
	}

	cursor, _, _ := procLoadCursorW.Call(0, idcArrow)

	wc := wndClassExW{
		lpfnWndProc:   syscall.NewCallback(wndProc),
		hInstance:     hinst,
		hCursor:       windows.Handle(cursor),
		hbrBackground: colorWindow,
		lpszClassName: className,
	}
	wc.cbSize = uint32(unsafe.Sizeof(wc))

	atom, _, callErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	if atom == 0 {
		return fmt.Errorf("RegisterClassExW: %v", callErr)
	}

	x, y := windowOrigin(width, height, margin, pos)
	hwnd, _, callErr := procCreateWindowExW.Call(
		wsExTopmost|wsExToolWindow|wsExNoActivate,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(title)),
		wsPopup|wsVisible,
		uintptr(x), uintptr(y), uintptr(width), uintptr(height),
		0, 0, uintptr(hinst), 0,
	)
	if hwnd == 0 {
		return fmt.Errorf("CreateWindowExW: %v", callErr)
	}

	// TRUE: the window accepts files dragged from Explorer. This is what makes
	// the shell post WM_DROPFILES (with an HDROP) when a drop lands on us.
	procDragAcceptFiles.Call(hwnd, 1)

	return messageLoop()
}

// windowOrigin computes the window position inside the primary screen's work
// area. Without -pos it sits in the bottom-right corner; with -pos the given
// coordinates are used as-is, clamped so the whole window stays on screen.
func windowOrigin(width, height, margin int32, pos *[2]int32) (int32, int32) {
	var wa rect
	ok, _, _ := procSystemParametersInfoW.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&wa)), 0)
	if ok == 0 || wa.right <= wa.left || wa.bottom <= wa.top {
		cx, _, _ := procGetSystemMetrics.Call(smCXScreen)
		cy, _, _ := procGetSystemMetrics.Call(smCYScreen)
		wa = rect{right: int32(cx), bottom: int32(cy)}
	}
	x := wa.right - width - margin
	y := wa.bottom - height - margin
	if pos != nil {
		x, y = pos[0], pos[1]
	}
	if x < wa.left {
		x = wa.left
	}
	if y < wa.top {
		y = wa.top
	}
	if maxX := wa.right - width; x > maxX {
		x = maxX
	}
	if maxY := wa.bottom - height; y > maxY {
		y = maxY
	}
	return x, y
}

// messageLoop runs until WM_QUIT.
func messageLoop() error {
	var m msg
	for {
		ret, _, callErr := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		switch int32(ret) { // GetMessageW returns -1 on error, 0 on WM_QUIT.
		case -1:
			return fmt.Errorf("GetMessageW: %v", callErr)
		case 0:
			return nil
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

// wndProc keeps every parameter uintptr-sized, which syscall.NewCallback
// requires.
func wndProc(hwnd, message, wParam, lParam uintptr) uintptr {
	switch message {
	case wmPaint:
		paint(hwnd)
		return 0
	case wmDropFiles:
		handleDrop(hwnd, wParam)
		return 0
	case wmClose:
		procDestroyWindow.Call(hwnd) // triggers WM_DESTROY
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	ret, _, _ := procDefWindowProcW.Call(hwnd, message, wParam, lParam)
	return ret
}

// paint draws statusText centered in the client area.
func paint(hwnd uintptr) {
	var ps paintStruct
	hdc, _, _ := procBeginPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))
	if hdc == 0 {
		return
	}
	defer procEndPaint.Call(hwnd, uintptr(unsafe.Pointer(&ps)))

	var rc rect
	procGetClientRect.Call(hwnd, uintptr(unsafe.Pointer(&rc)))

	font, _, _ := procGetStockObject.Call(defaultGUIFont)
	if font != 0 {
		procSelectObject.Call(hdc, font)
	}
	procSetBkMode.Call(hdc, bkTransparent)

	text16, err := windows.UTF16FromString(statusText)
	if err != nil || len(text16) == 0 {
		return
	}
	procDrawTextW.Call(
		hdc,
		uintptr(unsafe.Pointer(&text16[0])),
		uintptr(len(text16)-1), // length in UTF-16 code units, excluding NUL
		uintptr(unsafe.Pointer(&rc)),
		dtCenter|dtVCenter|dtSingleLine|dtNoPrefix,
	)
}

// handleDrop turns an HDROP into one JSON line (stdout, and the -log file when
// configured) and repaints the window with the received-file count.
func handleDrop(hwnd, hDrop uintptr) {
	defer procDragFinish.Call(hDrop) // always release the HDROP

	// 0xFFFFFFFF asks "how many files?" without filling a buffer.
	count, _, _ := procDragQueryFileW.Call(hDrop, 0xFFFFFFFF, 0, 0)
	files := make([]string, 0, count)

	buf := make([]uint16, maxPathChars)
	for i := uintptr(0); i < count; i++ {
		n, _, _ := procDragQueryFileW.Call(hDrop, i, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
		// n == 0: unreadable entry; n >= len(buf): would be truncated, skip it.
		if n == 0 || n >= uintptr(len(buf)) {
			continue
		}
		files = append(files, windows.UTF16ToString(buf[:n]))
	}

	line, err := json.Marshal(dropEvent{
		Event: "drop",
		TS:    time.Now().Format(time.RFC3339),
		Files: files,
	})
	if err != nil {
		// Unreachable for this struct, but never drop the report silently.
		fmt.Fprintf(os.Stderr, "ft-desktop: marshal drop event: %v\n", err)
		return
	}
	line = append(line, '\n')

	if _, err := os.Stdout.Write(line); err != nil {
		fmt.Fprintf(os.Stderr, "ft-desktop: write stdout: %v\n", err)
	}
	if logPath != "" {
		appendLog(line)
	}

	statusText = fmt.Sprintf("收到 %d 个文件", len(files))
	if text16, err := windows.UTF16PtrFromString(statusText); err == nil {
		procSetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(text16)))
	}
	procInvalidateRect.Call(hwnd, 0, 1) // repaint the whole client area
}

// appendLog appends one JSONL record. The file is reopened per drop so external
// rotation is picked up; a failure is reported but never kills the drop path.
func appendLog(line []byte) {
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ft-desktop: open -log file: %v\n", err)
		return
	}
	if _, err := f.Write(line); err != nil {
		fmt.Fprintf(os.Stderr, "ft-desktop: write -log file: %v\n", err)
	}
	if err := f.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "ft-desktop: close -log file: %v\n", err)
	}
}
