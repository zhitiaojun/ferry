//go:build windows

package main

import (
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

const (
	inputClassName = "FerryInputWin"
	wmInputSubmit  = wmApp + 41
	inputHoverNone = 0
	inputHoverCanc = 1
	inputHoverOk   = 2
	inputHoverClos = 3
)

var (
	procSetWindowLongPtr = user32.NewProc("SetWindowLongPtrW")
	procCallWindowProc   = user32.NewProc("CallWindowProcW")
	procGetKeyState      = user32.NewProc("GetKeyState")
	procSetBkColor       = gdi32.NewProc("SetBkColor")

	inputClassOnce  sync.Once
	inputWndProcCB  = syscall.NewCallback(inputWndProc)
	inputEditProcCB = syscall.NewCallback(inputEditWndProc)

	inputMu      sync.Mutex
	inputWinH    syscall.Handle
	inputObjs    = map[syscall.Handle]*inputWin{}
	inputEdits   = map[syscall.Handle]uintptr{}
	inputOwners  = map[syscall.Handle]*inputWin{}
	pendingInOpt *textInputOpts
)

type textInputOpts struct {
	Owner       syscall.Handle
	Title       string
	Subtitle    string
	Initial     string
	ConfirmText string
	Multiline   bool
	AllowEmpty  bool
	OnSubmit    func(text string)
}

type inputWin struct {
	hwnd     syscall.Handle
	edit     syscall.Handle
	opts     textInputOpts
	pal      palette
	dark     bool
	acrylic  bool
	dpi      int32
	hover    int
	tracking bool
	errMsg   string

	editBg    syscall.Handle
	fontTitle syscall.Handle
	fontBody  syscall.Handle
	fontSm    syscall.Handle

	closeRect  rectT
	boxRect    rectT
	cancelRect rectT
	okRect     rectT
}

// openTextInputDialog opens a borderless Acrylic Win32 dialog with MiSans typography
// for single-line (device rename) or multi-line (send text) input.
func openTextInputDialog(opts textInputOpts) {
	inputMu.Lock()
	prev := inputWinH
	inputMu.Unlock()
	if prev != 0 {
		procDestroyWindow.Call(uintptr(prev))
	}

	pal, _ := theme()
	dpi := screenDPI()
	if opts.Owner != 0 {
		dpi = windowDPI(opts.Owner)
	}
	dw, dh := int32(392), int32(204)
	if opts.Multiline {
		dw, dh = 448, 326
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
	cls := utf16Ptr(inputClassName)
	inputClassOnce.Do(func() {
		cur, _, _ := procLoadCursorW.Call(0, idcArrow)
		bg, _, _ := procCreateBrush.Call(uintptr(pal.paper))
		wc := wndClassExW{
			cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
			lpfnWndProc:   inputWndProcCB,
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
	inputMu.Lock()
	pendingInOpt = &cp
	inputMu.Unlock()

	// WS_CAPTION | WS_CLIPCHILDREN (omit WS_SYSMENU so DWM draws no ghost buttons)
	const style = 0x00C00000 | 0x02000000
	h, _, _ := procCreateWindowExW.Call(
		0x00010000, // WS_EX_CONTROLPARENT
		uintptr(unsafe.Pointer(cls)),
		uintptr(unsafe.Pointer(utf16Ptr(opts.Title))),
		style,
		uintptr(x), uintptr(y), uintptr(ww), uintptr(wh),
		uintptr(opts.Owner), 0, uintptr(hInst), 0,
	)
	hwnd := syscall.Handle(h)
	if hwnd == 0 {
		inputMu.Lock()
		pendingInOpt = nil
		inputMu.Unlock()
		return
	}
	procShowWindow.Call(uintptr(hwnd), swShow)
	procUpdateWindow.Call(uintptr(hwnd))
	if iw := currentInputWin(hwnd); iw != nil && iw.edit != 0 {
		procSetFocus.Call(uintptr(iw.edit))
	}
}

func currentInputWin(hwnd syscall.Handle) *inputWin {
	inputMu.Lock()
	defer inputMu.Unlock()
	return inputObjs[hwnd]
}

func inputWndProc(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	iw := currentInputWin(hwnd)
	switch msg {
	case wmCreate:
		pal, dark := theme()
		inputMu.Lock()
		var opts textInputOpts
		if pendingInOpt != nil {
			opts = *pendingInOpt
			pendingInOpt = nil
		}
		inputMu.Unlock()
		if opts.ConfirmText == "" {
			opts.ConfirmText = "确定"
		}
		w := &inputWin{
			hwnd: hwnd,
			opts: opts,
			pal:  pal,
			dark: dark,
			dpi:  windowDPI(hwnd),
		}
		procSetWindowPos.Call(uintptr(hwnd), 0, 0, 0, 0, 0, 0x0027)
		w.acrylic = enableAcrylicBackdrop(hwnd, dark)
		w.editBg = syscall.Handle(createPairBrush(pal.paper2))
		w.makeFonts()
		inputMu.Lock()
		inputWinH = hwnd
		inputObjs[hwnd] = w
		inputMu.Unlock()
		w.initEdit()
		return 0

	case wmNcCalcSize:
		if wParam != 0 {
			return 0
		}

	case wmNcHitTest:
		if iw != nil {
			return iw.ncHitTest(hwnd, lParam)
		}

	case wmEraseBkgnd:
		return 1

	case wmPaint:
		if iw != nil {
			iw.paint(hwnd)
			return 0
		}

	case wmMouseMove:
		if iw != nil {
			iw.onMouseMove(hwnd, int32(lParam&0xffff), int32((lParam>>16)&0xffff))
			return 0
		}

	case wmMouseLeave:
		if iw != nil {
			iw.tracking = false
			if iw.hover != inputHoverNone {
				iw.hover = inputHoverNone
				procInvalidateRect.Call(uintptr(hwnd), 0, 0)
			}
			return 0
		}

	case wmSetCursor:
		if iw != nil && loword(lParam) == htClient && (iw.hover == inputHoverCanc || iw.hover == inputHoverOk) {
			h, _, _ := procLoadCursorW.Call(0, idcHand)
			procSetCursor.Call(h)
			return 1
		}

	case wmLButtonUp:
		if iw != nil {
			iw.onClick(hwnd, int32(lParam&0xffff), int32((lParam>>16)&0xffff))
			return 0
		}

	case wmInputSubmit:
		if iw != nil {
			iw.submit()
			return 0
		}

	case wmCtlColorEdit:
		if iw != nil {
			hdc := wParam
			procSetBkColor.Call(hdc, uintptr(iw.pal.paper2))
			procSetBkMode.Call(hdc, 2) // OPAQUE
			procSetTextColor.Call(hdc, uintptr(iw.pal.ink))
			return uintptr(iw.editBg)
		}

	case wmClose:
		procDestroyWindow.Call(uintptr(hwnd))
		return 0

	case wmDestroy:
		if iw != nil {
			if iw.edit != 0 {
				inputMu.Lock()
				delete(inputEdits, iw.edit)
				delete(inputOwners, iw.edit)
				inputMu.Unlock()
			}
			iw.freeResources()
		}
		inputMu.Lock()
		delete(inputObjs, hwnd)
		if inputWinH == hwnd {
			inputWinH = 0
		}
		inputMu.Unlock()
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return r
}

func inputEditWndProc(edit syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	const (
		wmKeyDown = 0x0100
		vkReturn  = 0x0D
		vkEscape  = 0x1B
		vkControl = 0x11
	)
	inputMu.Lock()
	orig := inputEdits[edit]
	owner := inputOwners[edit]
	inputMu.Unlock()

	if msg == wmKeyDown && owner != nil {
		switch wParam {
		case vkEscape:
			procPostMessageW.Call(uintptr(owner.hwnd), wmClose, 0, 0)
			return 0
		case vkReturn:
			if !owner.opts.Multiline {
				procPostMessageW.Call(uintptr(owner.hwnd), wmInputSubmit, 0, 0)
				return 0
			}
			ks, _, _ := procGetKeyState.Call(vkControl)
			if ks&0x8000 != 0 {
				procPostMessageW.Call(uintptr(owner.hwnd), wmInputSubmit, 0, 0)
				return 0
			}
		}
	}
	if orig != 0 {
		r, _, _ := procCallWindowProc.Call(orig, uintptr(edit), uintptr(msg), wParam, lParam)
		return r
	}
	r, _, _ := procDefWindowProcW.Call(uintptr(edit), uintptr(msg), wParam, lParam)
	return r
}

func (w *inputWin) px(v int32) int32 {
	dpi := w.dpi
	if dpi == 0 {
		dpi = 96
	}
	return int32(float64(v)*float64(dpi)/96.0 + 0.5)
}

func (w *inputWin) makeFonts() {
	w.fontTitle = createUIFont(w.px(16), 700)
	w.fontBody = createUIFont(w.px(13), 400)
	w.fontSm = createUIFont(w.px(12), 400)
}

func (w *inputWin) freeResources() {
	for _, f := range []syscall.Handle{w.fontTitle, w.fontBody, w.fontSm} {
		if f != 0 {
			procDeleteObject.Call(uintptr(f))
		}
	}
	if w.editBg != 0 {
		procDeleteObject.Call(uintptr(w.editBg))
	}
}

func (w *inputWin) initEdit() {
	var cr rectT
	procGetClientRect.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&cr)))
	w.layout(cr.right, cr.bottom)

	const (
		wsChild       = 0x40000000
		wsVisible     = 0x10000000
		wsTabStop     = 0x00010000
		wsVScroll     = 0x00200000
		esMultiline   = 0x0004
		esAutoVScroll = 0x0040
		esAutoHScroll = 0x0080
		esWantReturn  = 0x1000
		emSetMargins  = 0x00D3
		emSetSel      = 0x00B1
		emSetLimitTxt = 0x00C5
	)
	style := uintptr(wsChild | wsVisible | wsTabStop | esAutoHScroll)
	if w.opts.Multiline {
		style = uintptr(wsChild | wsVisible | wsTabStop | wsVScroll | esMultiline | esAutoVScroll | esWantReturn)
	}
	inX := w.boxRect.left + w.px(10)
	inY := w.boxRect.top + w.px(8)
	inW := (w.boxRect.right - w.boxRect.left) - w.px(20)
	inH := (w.boxRect.bottom - w.boxRect.top) - w.px(16)
	if !w.opts.Multiline {
		inY = w.boxRect.top + w.px(7)
		inH = (w.boxRect.bottom - w.boxRect.top) - w.px(14)
	}

	h, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(utf16Ptr("EDIT"))),
		0,
		style,
		uintptr(inX), uintptr(inY), uintptr(inW), uintptr(inH),
		uintptr(w.hwnd), 401, uintptr(getModuleHandle()), 0,
	)
	w.edit = syscall.Handle(h)
	if w.edit == 0 {
		return
	}
	procSendMessageW.Call(h, 0x0030, uintptr(w.fontBody), 1)
	if w.opts.Multiline {
		procSendMessageW.Call(h, emSetLimitTxt, 1048576, 0)
	} else {
		procSendMessageW.Call(h, emSetLimitTxt, 128, 0)
		procSendMessageW.Call(h, emSetMargins, 3, uintptr(w.px(2))|uintptr(w.px(2)<<16))
	}
	if w.opts.Initial != "" {
		procSetWindowTextW.Call(h, uintptr(unsafe.Pointer(utf16Ptr(w.opts.Initial))))
		procSendMessageW.Call(h, emSetSel, 0, ^uintptr(0))
	}
	if err := procSetWindowLongPtr.Find(); err == nil {
		orig, _, _ := procSetWindowLongPtr.Call(h, ^uintptr(3), inputEditProcCB)
		inputMu.Lock()
		inputEdits[w.edit] = orig
		inputOwners[w.edit] = w
		inputMu.Unlock()
	}
}

func (w *inputWin) layout(cw, ch int32) {
	pad := w.px(24)
	w.closeRect = rectT{cw - w.px(38), 0, cw, w.px(32)}

	btnH := w.px(32)
	btnBottom := ch - w.px(18)
	btnTop := btnBottom - btnH
	okW := w.px(78)
	cancW := w.px(72)
	w.okRect = rectT{cw - pad - okW, btnTop, cw - pad, btnBottom}
	w.cancelRect = rectT{w.okRect.left - w.px(10) - cancW, btnTop, w.okRect.left - w.px(10), btnBottom}

	boxTop := w.px(76)
	boxBottom := btnTop - w.px(18)
	if !w.opts.Multiline {
		boxBottom = boxTop + w.px(36)
	}
	w.boxRect = rectT{pad, boxTop, cw - pad, boxBottom}
}

func (w *inputWin) ncHitTest(hwnd syscall.Handle, lParam uintptr) uintptr {
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

func (w *inputWin) paint(hwnd syscall.Handle) {
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
	if w.hover == inputHoverClos {
		cv.fillColorAlpha(w.closeRect, w.pal.danger, 230)
		closeInk = col(0xFF, 0xFF, 0xFF)
	}
	drawCloseGlyph(cv, w.closeRect, closeInk)

	pad := w.px(24)
	cv.drawAlphaText(w.fontTitle, w.opts.Title, rectT{pad, w.px(18), w.closeRect.left - pClamp(w.px(8)), w.px(44)},
		dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink)
	if w.opts.Subtitle != "" {
		cv.drawAlphaText(w.fontSm, w.opts.Subtitle, rectT{pad, w.px(44), cr.right - pad, w.px(66)},
			dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)
	}

	// Input surface & border
	cv.drawAlphaRoundRect(w.boxRect, float64(w.px(8)), w.pal.paper2, 245, w.pal.line, 255, 1.0)

	if w.errMsg != "" {
		cv.drawAlphaText(w.fontSm, w.errMsg,
			rectT{pad, w.cancelRect.top, w.cancelRect.left - w.px(8), w.cancelRect.bottom},
			dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.danger)
	}

	cancFill := w.pal.paper2
	if w.hover == inputHoverCanc {
		cancFill = w.pal.paper3
	}
	cv.drawAlphaRoundRect(w.cancelRect, float64(w.px(7)), cancFill, 235, w.pal.line, 255, 1.0)
	cv.drawAlphaText(w.fontSm, "取消", w.cancelRect, dtCenter|dtSingleLine|dtVCenter, w.pal.ink)

	okFill := w.pal.accent
	if w.hover == inputHoverOk {
		okFill = shade(okFill, w.dark, 0.12)
	}
	cv.drawAlphaRoundRect(w.okRect, float64(w.px(7)), okFill, 255, okFill, 255, 0)
	cv.drawAlphaText(w.fontSm, w.opts.ConfirmText, w.okRect, dtCenter|dtSingleLine|dtVCenter, w.pal.accentInk)

	cv.finishBlit(hdc)
}

func pClamp(v int32) int32 {
	if v < 0 {
		return 0
	}
	return v
}

func (w *inputWin) onMouseMove(hwnd syscall.Handle, x, y int32) {
	z := inputHoverNone
	switch {
	case ptIn(w.closeRect, x, y):
		z = inputHoverClos
	case ptIn(w.cancelRect, x, y):
		z = inputHoverCanc
	case ptIn(w.okRect, x, y):
		z = inputHoverOk
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

func (w *inputWin) onClick(hwnd syscall.Handle, x, y int32) {
	switch {
	case ptIn(w.closeRect, x, y), ptIn(w.cancelRect, x, y):
		procDestroyWindow.Call(uintptr(hwnd))
	case ptIn(w.okRect, x, y):
		w.submit()
	}
}

func (w *inputWin) submit() {
	raw := readEditText(w.edit)
	if !w.opts.AllowEmpty && strings.TrimSpace(raw) == "" {
		w.errMsg = "内容不能为空"
		procInvalidateRect.Call(uintptr(w.hwnd), 0, 0)
		return
	}
	cb := w.opts.OnSubmit
	hwnd := w.hwnd
	procDestroyWindow.Call(uintptr(hwnd))
	if cb != nil {
		cb(raw)
	}
}
