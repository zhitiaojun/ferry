//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

type dropWindow struct {
	app     *app
	hwnd    syscall.Handle
	label   string
	width   int32
	height  int32
	margin  int32
	pos     *[2]int32
	dropped int
}

var dropHandle *dropWindow

func runDropWindow(a *app, w, h, margin int32, pos *[2]int32) int {
	d := &dropWindow{app: a, width: w, height: h, margin: margin, pos: pos, label: "拖文件到这里"}
	hInst := getModuleHandle()
	cls := utf16Ptr("ftDropWindow")
	cursor, _, _ := procLoadCursorW.Call(0, idcArrow)
	wc := wndClassExW{
		cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		lpfnWndProc:   syscall.NewCallback(dropWndProc),
		hInstance:     hInst,
		hCursor:       syscall.Handle(cursor),
		hbrBackground: colorWindow,
		lpszClassName: cls,
	}
	dropHandle = d
	if r, _, _ := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		messageBox("ft", "注册投放窗口失败")
		return 1
	}
	x, y := windowOrigin(w, h, margin, pos)
	hwnd, _, _ := procCreateWindowExW.Call(
		wsExTopmost|wsExToolWindow, uintptr(unsafe.Pointer(cls)),
		uintptr(unsafe.Pointer(utf16Ptr("ft drop"))), wsPopup|wsVisible,
		uintptr(x), uintptr(y), uintptr(w), uintptr(h), 0, 0, uintptr(hInst), 0)
	if hwnd == 0 {
		messageBox("ft", "创建投放窗口失败")
		return 1
	}
	d.hwnd = syscall.Handle(hwnd)
	procDragAcceptFiles.Call(hwnd, 1)
	procShowWindow.Call(hwnd, swShow)
	procUpdateWindow.Call(hwnd)
	a.event("desktop.drop_ready", map[string]any{"x": int(x), "y": int(y), "w": int(w), "h": int(h)})
	var msg msgT
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
	return 0
}

func windowOrigin(w, h, margin int32, pos *[2]int32) (int32, int32) {
	var wa rectT
	if r, _, _ := procSystemParametersInfoW.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&wa)), 0); r == 0 {
		wa = rectT{0, 0, 1280, 720}
	}
	if pos != nil {
		x, y := pos[0], pos[1]
		if x < wa.left {
			x = wa.left
		}
		if x+w > wa.right {
			x = wa.right - w
		}
		if y < wa.top {
			y = wa.top
		}
		if y+h > wa.bottom {
			y = wa.bottom - h
		}
		return x, y
	}
	return wa.right - w - margin, wa.bottom - h - margin
}

func dropWndProc(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	d := dropHandle
	switch msg {
	case wmDropFiles:
		if d != nil {
			d.onDrop(syscall.Handle(wParam))
		}
		return 0
	case wmPaint:
		if d != nil {
			d.paint(hwnd)
		}
		return 0
	case wmClose:
		procDestroyWindow.Call(uintptr(hwnd))
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return r
}

func (d *dropWindow) onDrop(hdrop syscall.Handle) {
	count := int(dragQueryFile(hdrop, 0xFFFFFFFF, nil, 0))
	files := make([]string, 0, count)
	for i := 0; i < count; i++ {
		buf := make([]uint16, 32768)
		n := int(dragQueryFile(hdrop, uint32(i), &buf[0], uint32(len(buf))))
		if n > 0 {
			files = append(files, syscall.UTF16ToString(buf[:n]))
		}
	}
	procDragFinish.Call(uintptr(hdrop))
	d.dropped = count
	d.label = fmt.Sprintf("收到 %d 个文件", count)
	procSendMessageW.Call(uintptr(d.hwnd), wmPaint, 0, 0)
	invalidateRect(uintptr(d.hwnd))
	d.app.event("desktop.drop", map[string]any{"files": files, "dir": d.app.dir})
	if count > 0 {
		go func() {
			if err := d.sendFiles(files); err != nil {
				d.app.event("desktop.drop_send_error", map[string]any{"error": err.Error()})
			}
		}()
	}
}

func (d *dropWindow) paint(hwnd syscall.Handle) {
	var ps paintStruct
	hdc, _, _ := procBeginPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))
	if hdc == 0 {
		return
	}
	defer procEndPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))

	var rc rectT
	procGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&rc)))

	font, _, _ := gdi32.NewProc("GetStockObject").Call(defaultGUIFont)
	old, _, _ := gdi32.NewProc("SelectObject").Call(hdc, font)
	bk, _, _ := gdi32.NewProc("SetBkMode").Call(hdc, bkTransparent)

	text := syscall.StringToUTF16(d.label)
	procDrawTextW.Call(hdc, uintptr(unsafe.Pointer(&text[0])), uintptr(len(text)-1),
		uintptr(unsafe.Pointer(&rc)), dtCenter|dtVCenter|dtSingleLine|dtNoPrefix)

	gdi32.NewProc("SelectObject").Call(hdc, old)
	gdi32.NewProc("SetBkMode").Call(hdc, bk)
}

func dragQueryFile(hdrop syscall.Handle, index uint32, buf *uint16, size uint32) uint32 {
	r, _, _ := procDragQueryFileW.Call(uintptr(hdrop), uintptr(index),
		uintptr(unsafe.Pointer(buf)), uintptr(size))
	return uint32(r)
}

func invalidateRect(hwnd uintptr) {
	procInvalidateRect.Call(hwnd, 0, 1)
}
