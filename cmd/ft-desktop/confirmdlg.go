//go:build windows

package main

import (
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

const (
	confirmClassName = "FerryConfirmWin"
	confirmHoverNone = 0
	confirmHoverCanc = 1
	confirmHoverOk   = 2
	confirmHoverClos = 3
)

var (
	confirmClassOnce sync.Once
	confirmWndProcCB = syscall.NewCallback(confirmWndProc)

	confirmMu       sync.Mutex
	confirmWinH     syscall.Handle
	confirmObjs     = map[syscall.Handle]*confirmWin{}
	pendingConfOpts *confirmOpts
)

type confirmOpts struct {
	Owner       syscall.Handle
	Title       string
	Body        string
	ConfirmText string
	CancelText  string
	Danger      bool
	HideCancel  bool
	OnConfirm   func()
}

type confirmWin struct {
	hwnd     syscall.Handle
	opts     confirmOpts
	pal      palette
	dark     bool
	acrylic  bool
	dpi      int32
	hover    int
	tracking bool

	fontTitle syscall.Handle
	fontBody  syscall.Handle
	fontSm    syscall.Handle

	closeRect  rectT
	cancelRect rectT
	okRect     rectT
}

// openConfirmDialog opens a borderless Win11 Acrylic confirmation or info dialog
// matching the main desktop UI.
func openConfirmDialog(opts confirmOpts) {
	confirmMu.Lock()
	prev := confirmWinH
	confirmMu.Unlock()
	if prev != 0 {
		procDestroyWindow.Call(uintptr(prev))
	}

	pal, _ := theme()
	dpi := screenDPI()
	if opts.Owner != 0 {
		dpi = windowDPI(opts.Owner)
	}
	dw, dh := int32(396), int32(188)
	if strings.Count(opts.Body, "\n") >= 3 || len(opts.Body) > 120 {
		dw, dh = 432, 256
	}
	ww := scalePx(dpi, dw)
	wh := scalePx(dpi, dh)

	var x, y int32
	var wa rectT
	procSystemParametersInfoW.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&wa)), 0)
	if opts.Owner != 0 {
		var wr rectT
		procGetWindowRect.Call(uintptr(opts.Owner), uintptr(unsafe.Pointer(&wr)))
		if wr.right > wr.left && wr.bottom > wr.top {
			x = wr.left + (wr.right-wr.left-ww)/2
			y = wr.top + (wr.bottom-wr.top-wh)/2
		}
	}
	if x == 0 && y == 0 {
		x = wa.left + (wa.right-wa.left-ww)/2
		y = wa.top + (wa.bottom-wa.top-wh)/2
	}

	hInst := getModuleHandle()
	cls := utf16Ptr(confirmClassName)
	confirmClassOnce.Do(func() {
		cur, _, _ := procLoadCursorW.Call(0, idcArrow)
		bg, _, _ := procCreateBrush.Call(uintptr(pal.paper))
		wc := wndClassExW{
			cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
			lpfnWndProc:   confirmWndProcCB,
			hInstance:     hInst,
			hIcon:         loadAppIcon(0),
			hIconSm:       loadAppIcon(16),
			hCursor:       syscall.Handle(cur),
			hbrBackground: syscall.Handle(bg),
			lpszClassName: cls,
		}
		procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	})

	cp := opts
	confirmMu.Lock()
	pendingConfOpts = &cp
	confirmMu.Unlock()

	// WS_CAPTION | WS_CLIPCHILDREN (omit WS_SYSMENU so DWM draws no ghost buttons)
	const style = 0x00C00000 | 0x02000000
	h, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(cls)),
		uintptr(unsafe.Pointer(utf16Ptr(opts.Title))),
		style,
		uintptr(x), uintptr(y), uintptr(ww), uintptr(wh),
		uintptr(opts.Owner), 0, uintptr(hInst), 0,
	)
	hwnd := syscall.Handle(h)
	if hwnd == 0 {
		confirmMu.Lock()
		pendingConfOpts = nil
		confirmMu.Unlock()
		return
	}
	procShowWindow.Call(uintptr(hwnd), swShow)
	procUpdateWindow.Call(uintptr(hwnd))
	procSetFocus.Call(uintptr(hwnd))
}

func currentConfirmWin(hwnd syscall.Handle) *confirmWin {
	confirmMu.Lock()
	defer confirmMu.Unlock()
	return confirmObjs[hwnd]
}

func confirmWndProc(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	cw := currentConfirmWin(hwnd)
	switch msg {
	case wmCreate:
		pal, dark := theme()
		confirmMu.Lock()
		var opts confirmOpts
		if pendingConfOpts != nil {
			opts = *pendingConfOpts
			pendingConfOpts = nil
		}
		confirmMu.Unlock()
		if opts.ConfirmText == "" {
			opts.ConfirmText = "确定"
		}
		if opts.CancelText == "" {
			opts.CancelText = "取消"
		}
		w := &confirmWin{
			hwnd: hwnd,
			opts: opts,
			pal:  pal,
			dark: dark,
			dpi:  windowDPI(hwnd),
		}
		procSetWindowPos.Call(uintptr(hwnd), 0, 0, 0, 0, 0, 0x0027)
		w.acrylic = enableAcrylicBackdrop(hwnd, dark)
		w.makeFonts()
		confirmMu.Lock()
		confirmWinH = hwnd
		confirmObjs[hwnd] = w
		confirmMu.Unlock()
		return 0

	case wmNcCalcSize:
		if wParam != 0 {
			return 0
		}

	case wmNcHitTest:
		if cw != nil {
			return cw.ncHitTest(hwnd, lParam)
		}

	case wmEraseBkgnd:
		return 1

	case wmPaint:
		if cw != nil {
			cw.paint(hwnd)
			return 0
		}

	case wmMouseMove:
		if cw != nil {
			cw.onMouseMove(hwnd, int32(lParam&0xffff), int32((lParam>>16)&0xffff))
			return 0
		}

	case wmMouseLeave:
		if cw != nil {
			cw.tracking = false
			if cw.hover != confirmHoverNone {
				cw.hover = confirmHoverNone
				procInvalidateRect.Call(uintptr(hwnd), 0, 0)
			}
			return 0
		}

	case wmSetCursor:
		if cw != nil && loword(lParam) == htClient && (cw.hover == confirmHoverCanc || cw.hover == confirmHoverOk) {
			h, _, _ := procLoadCursorW.Call(0, idcHand)
			procSetCursor.Call(h)
			return 1
		}

	case wmLButtonUp:
		if cw != nil {
			cw.onClick(hwnd, int32(lParam&0xffff), int32((lParam>>16)&0xffff))
			return 0
		}

	case 0x0100: // WM_KEYDOWN
		if cw != nil {
			switch wParam {
			case 0x1B: // VK_ESCAPE
				procDestroyWindow.Call(uintptr(hwnd))
				return 0
			case 0x0D: // VK_RETURN
				cw.confirm()
				return 0
			}
		}

	case wmClose:
		procDestroyWindow.Call(uintptr(hwnd))
		return 0

	case wmDestroy:
		if cw != nil {
			cw.freeResources()
		}
		confirmMu.Lock()
		delete(confirmObjs, hwnd)
		if confirmWinH == hwnd {
			confirmWinH = 0
		}
		confirmMu.Unlock()
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return r
}

func (w *confirmWin) px(v int32) int32 {
	dpi := w.dpi
	if dpi == 0 {
		dpi = 96
	}
	return int32(float64(v)*float64(dpi)/96.0 + 0.5)
}

func (w *confirmWin) makeFonts() {
	w.fontTitle = createUIFont(w.px(16), 700)
	w.fontBody = createUIFont(w.px(13), 400)
	w.fontSm = createUIFont(w.px(12), 400)
}

func (w *confirmWin) freeResources() {
	for _, f := range []syscall.Handle{w.fontTitle, w.fontBody, w.fontSm} {
		if f != 0 {
			procDeleteObject.Call(uintptr(f))
		}
	}
}

func (w *confirmWin) layout(cw, ch int32) {
	pad := w.px(24)
	w.closeRect = rectT{cw - w.px(38), 0, cw, w.px(32)}

	btnH := w.px(32)
	btnBottom := ch - w.px(18)
	btnTop := btnBottom - btnH
	okW := w.px(82)
	cancW := w.px(72)
	w.okRect = rectT{cw - pad - okW, btnTop, cw - pad, btnBottom}
	if w.opts.HideCancel {
		w.cancelRect = rectT{}
	} else {
		w.cancelRect = rectT{w.okRect.left - w.px(10) - cancW, btnTop, w.okRect.left - w.px(10), btnBottom}
	}
}

func (w *confirmWin) ncHitTest(hwnd syscall.Handle, lParam uintptr) uintptr {
	pt := pointT{int32(int16(lParam & 0xffff)), int32(int16((lParam >> 16) & 0xffff))}
	procScreenToClient.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&pt)))
	var cr rectT
	procGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&cr)))
	w.layout(cr.right, cr.bottom)
	if pt.y < w.px(34) && !ptIn(w.closeRect, pt.x, pt.y) {
		return htCaption
	}
	return htClient
}

func (w *confirmWin) paint(hwnd syscall.Handle) {
	var ps paintStruct
	hdc, _, _ := procBeginPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))
	defer procEndPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))

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
	a := byte(255)
	if w.acrylic {
		a = 212
		if w.dark {
			a = 222
		}
	}
	cv.fillColorAlpha(cr, w.pal.paper, a)

	closeInk := w.pal.ink2
	if w.hover == confirmHoverClos {
		cv.fillColorAlpha(w.closeRect, w.pal.danger, 230)
		closeInk = col(0xFF, 0xFF, 0xFF)
	}
	drawCloseGlyph(cv, w.closeRect, closeInk)

	pad := w.px(24)
	cv.drawAlphaText(w.fontTitle, w.opts.Title, rectT{pad, w.px(20), pClamp(w.closeRect.left - w.px(8)), w.px(46)},
		dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink)

	bodyRect := rectT{pad, w.px(54), cr.right - pad, w.okRect.top - w.px(16)}
	if w.opts.Body != "" && bodyRect.bottom > bodyRect.top {
		cv.drawAlphaText(w.fontBody, w.opts.Body, bodyRect, dtWordBreak, w.pal.ink2)
	}

	if !w.opts.HideCancel {
		cancFill := w.pal.paper2
		if w.hover == confirmHoverCanc {
			cancFill = w.pal.paper3
		}
		cv.drawAlphaRoundRect(w.cancelRect, float64(w.px(7)), cancFill, 235, w.pal.line, 255, 1.0)
		cv.drawAlphaText(w.fontSm, w.opts.CancelText, w.cancelRect, dtCenter|dtSingleLine|dtVCenter, w.pal.ink)
	}

	okFill := w.pal.accent
	okText := w.pal.accentInk
	if w.opts.Danger {
		okFill = w.pal.danger
		okText = col(0xFF, 0xFF, 0xFF)
	}
	if w.hover == confirmHoverOk {
		okFill = shade(okFill, w.dark, 0.12)
	}
	cv.drawAlphaRoundRect(w.okRect, float64(w.px(7)), okFill, 255, okFill, 255, 0)
	cv.drawAlphaText(w.fontSm, w.opts.ConfirmText, w.okRect, dtCenter|dtSingleLine|dtVCenter, okText)

	cv.finishBlit(hdc)
}

func (w *confirmWin) onMouseMove(hwnd syscall.Handle, x, y int32) {
	z := confirmHoverNone
	switch {
	case ptIn(w.closeRect, x, y):
		z = confirmHoverClos
	case !w.opts.HideCancel && ptIn(w.cancelRect, x, y):
		z = confirmHoverCanc
	case ptIn(w.okRect, x, y):
		z = confirmHoverOk
	}
	if z != w.hover {
		w.hover = z
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

func (w *confirmWin) onClick(hwnd syscall.Handle, x, y int32) {
	switch {
	case ptIn(w.closeRect, x, y):
		procDestroyWindow.Call(uintptr(hwnd))
	case !w.opts.HideCancel && ptIn(w.cancelRect, x, y):
		procDestroyWindow.Call(uintptr(hwnd))
	case ptIn(w.okRect, x, y):
		w.confirm()
	}
}

func (w *confirmWin) confirm() {
	cb := w.opts.OnConfirm
	hwnd := w.hwnd
	procDestroyWindow.Call(uintptr(hwnd))
	if cb != nil {
		cb()
	}
}
