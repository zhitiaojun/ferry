//go:build windows

package main

import (
	"sync"
	"syscall"
	"unsafe"
)

const (
	srvClassName = "FerryServerConfigWin"
	srvHoverNone = 0
	srvHoverClos = 1
	srvHoverRst  = 2
	srvHoverCanc = 3
	srvHoverOk   = 4
)

var (
	srvClassOnce sync.Once
	srvWndProcCB = syscall.NewCallback(srvWndProc)

	srvMu        sync.Mutex
	srvWinH      syscall.Handle
	srvObjs      = map[syscall.Handle]*srvWin{}
	pendingSApp  *app
	pendingSCB   func()
)

type srvWin struct {
	hwnd     syscall.Handle
	app      *app
	onSaved  func()
	pal      palette
	dark     bool
	acrylic  bool
	dpi      int32
	hover    int
	tracking bool
	errMsg   string

	editSig  syscall.Handle
	editWeb  syscall.Handle
	editStun syscall.Handle
	editBg   syscall.Handle

	fontTitle syscall.Handle
	fontBody  syscall.Handle
	fontSm    syscall.Handle

	closeRect  rectT
	sigBoxR    rectT
	webBoxR    rectT
	stunBoxR   rectT
	resetRect  rectT
	cancelRect rectT
	okRect     rectT
}

// openServerConfigDialog opens an Acrylic dialog allowing the user to customize
// the signaling WSS URL, temporary share Web base URL, and STUN server list.
func openServerConfigDialog(owner syscall.Handle, a *app, onSaved func()) {
	if a == nil {
		return
	}
	srvMu.Lock()
	prev := srvWinH
	srvMu.Unlock()
	if prev != 0 {
		procDestroyWindow.Call(uintptr(prev))
	}

	pal, _ := theme()
	dpi := screenDPI()
	if owner != 0 {
		dpi = windowDPI(owner)
	}
	ww := scalePx(dpi, 476)
	wh := scalePx(dpi, 356)

	var x, y int32
	var wa rectT
	procSystemParametersInfoW.Call(spiGetWorkArea, 0, uintptr(unsafe.Pointer(&wa)), 0)
	if owner != 0 {
		var wr rectT
		procGetWindowRect.Call(uintptr(owner), uintptr(unsafe.Pointer(&wr)))
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
	cls := utf16Ptr(srvClassName)
	srvClassOnce.Do(func() {
		cur, _, _ := procLoadCursorW.Call(0, idcArrow)
		bg, _, _ := procCreateBrush.Call(uintptr(pal.paper))
		wc := wndClassExW{
			cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
			lpfnWndProc:   srvWndProcCB,
			hInstance:     hInst,
			hIcon:         loadAppIcon(0),
			hIconSm:       loadAppIcon(16),
			hCursor:       syscall.Handle(cur),
			hbrBackground: syscall.Handle(bg),
			lpszClassName: cls,
		}
		procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	})

	srvMu.Lock()
	pendingSApp = a
	pendingSCB = onSaved
	srvMu.Unlock()

	const style = 0x00C00000 | 0x02000000 // WS_CAPTION | WS_CLIPCHILDREN
	h, _, _ := procCreateWindowExW.Call(
		0x00010000, // WS_EX_CONTROLPARENT
		uintptr(unsafe.Pointer(cls)),
		uintptr(unsafe.Pointer(utf16Ptr("服务器地址设置"))),
		style,
		uintptr(x), uintptr(y), uintptr(ww), uintptr(wh),
		uintptr(owner), 0, uintptr(hInst), 0,
	)
	hwnd := syscall.Handle(h)
	if hwnd == 0 {
		srvMu.Lock()
		pendingSApp = nil
		pendingSCB = nil
		srvMu.Unlock()
		return
	}
	procShowWindow.Call(uintptr(hwnd), swShow)
	procUpdateWindow.Call(uintptr(hwnd))
	procSetForegroundWindow.Call(uintptr(hwnd))
	srvMu.Lock()
	sw := srvObjs[hwnd]
	srvMu.Unlock()
	if sw != nil && sw.editSig != 0 {
		procSetFocus.Call(uintptr(sw.editSig))
	}
}

func srvWndProc(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	srvMu.Lock()
	sw := srvObjs[hwnd]
	srvMu.Unlock()

	switch msg {
	case wmCreate:
		srvMu.Lock()
		a := pendingSApp
		cb := pendingSCB
		pendingSApp = nil
		pendingSCB = nil
		srvMu.Unlock()

		pal, dark := theme()
		dpi := windowDPI(hwnd)
		bg, _, _ := procCreateBrush.Call(uintptr(pal.paper2))
		w := &srvWin{
			hwnd:    hwnd,
			app:     a,
			onSaved: cb,
			pal:     pal,
			dark:    dark,
			acrylic: enableAcrylicBackdrop(hwnd, dark),
			dpi:     dpi,
			editBg:  syscall.Handle(bg),
		}
		w.makeFonts()
		srvMu.Lock()
		srvWinH = hwnd
		srvObjs[hwnd] = w
		srvMu.Unlock()
		w.createEdits()
		return 0

	case wmDpiChanged:
		if sw != nil {
			sw.dpi = int32(wParam & 0xffff)
			sw.freeFonts()
			sw.makeFonts()
			for _, ed := range []syscall.Handle{sw.editSig, sw.editWeb, sw.editStun} {
				if ed != 0 {
					procSendMessageW.Call(uintptr(ed), 0x0030, uintptr(sw.fontBody), 1)
				}
			}
			procInvalidateRect.Call(uintptr(hwnd), 0, 1)
			return 0
		}

	case wmNcCalcSize:
		if wParam != 0 {
			return 0
		}

	case wmNcHitTest:
		if sw != nil {
			return sw.ncHitTest(hwnd, lParam)
		}

	case wmEraseBkgnd:
		return 1

	case wmPaint:
		if sw != nil {
			sw.paint(hwnd)
			return 0
		}

	case wmMouseMove:
		if sw != nil {
			sw.onMouseMove(hwnd, int32(lParam&0xffff), int32((lParam>>16)&0xffff))
			return 0
		}

	case wmMouseLeave:
		if sw != nil {
			sw.tracking = false
			if sw.hover != srvHoverNone {
				sw.hover = srvHoverNone
				procInvalidateRect.Call(uintptr(hwnd), 0, 0)
			}
			return 0
		}

	case wmSetCursor:
		if sw != nil && loword(lParam) == htClient && sw.hover != srvHoverNone {
			h, _, _ := procLoadCursorW.Call(0, idcHand)
			procSetCursor.Call(h)
			return 1
		}

	case wmLButtonUp:
		if sw != nil {
			sw.onClick(hwnd, int32(lParam&0xffff), int32((lParam>>16)&0xffff))
			return 0
		}

	case wmCtlColorEdit:
		if sw != nil {
			hdc := wParam
			procSetBkColor.Call(hdc, uintptr(sw.pal.paper2))
			procSetBkMode.Call(hdc, 2)
			procSetTextColor.Call(hdc, uintptr(sw.pal.ink))
			return uintptr(sw.editBg)
		}

	case wmClose:
		procDestroyWindow.Call(uintptr(hwnd))
		return 0

	case wmDestroy:
		if sw != nil {
			sw.freeFonts()
			if sw.editBg != 0 {
				procDeleteObject.Call(uintptr(sw.editBg))
			}
		}
		srvMu.Lock()
		delete(srvObjs, hwnd)
		if srvWinH == hwnd {
			srvWinH = 0
		}
		srvMu.Unlock()
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return r
}

func (w *srvWin) px(v int32) int32 {
	dpi := w.dpi
	if dpi == 0 {
		dpi = 96
	}
	return int32(float64(v)*float64(dpi)/96.0 + 0.5)
}

func (w *srvWin) makeFonts() {
	w.fontTitle = createUIFont(w.px(16), 700)
	w.fontBody = createUIFont(w.px(13), 400)
	w.fontSm = createUIFont(w.px(12), 400)
}

func (w *srvWin) freeFonts() {
	for _, f := range []syscall.Handle{w.fontTitle, w.fontBody, w.fontSm} {
		if f != 0 {
			procDeleteObject.Call(uintptr(f))
		}
	}
}

func (w *srvWin) layout(cw, ch int32) {
	pad := w.px(24)
	w.closeRect = rectT{cw - w.px(38), 0, cw, w.px(32)}

	btnH := w.px(32)
	btnBottom := ch - w.px(18)
	btnTop := btnBottom - btnH
	okW := w.px(74)
	cancW := w.px(70)
	rstW := w.px(82)
	w.okRect = rectT{cw - pad - okW, btnTop, cw - pad, btnBottom}
	w.cancelRect = rectT{w.okRect.left - w.px(10) - cancW, btnTop, w.okRect.left - w.px(10), btnBottom}
	w.resetRect = rectT{pad, btnTop, pad + rstW, btnBottom}

	boxH := w.px(34)
	y := w.px(88)
	w.sigBoxR = rectT{pad, y, cw - pad, y + boxH}
	y += boxH + w.px(30)
	w.webBoxR = rectT{pad, y, cw - pad, y + boxH}
	y += boxH + w.px(30)
	w.stunBoxR = rectT{pad, y, cw - pad, y + boxH}
}

func (w *srvWin) createEdits() {
	var cr rectT
	procGetClientRect.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&cr)))
	w.layout(cr.right, cr.bottom)

	sig, web, stun := "", "", ""
	if w.app != nil {
		sig, web, stun = w.app.serverConfig()
	}

	const (
		wsChild       = 0x40000000
		wsVisible     = 0x10000000
		wsTabStop     = 0x00010000
		esAutoHScroll = 0x0080
		emSetMargins  = 0x00D3
		emSetLimitTxt = 0x00C5
	)
	style := uintptr(wsChild | wsVisible | wsTabStop | esAutoHScroll)
	makeEdit := func(id uintptr, box rectT, initial string) syscall.Handle {
		h, _, _ := procCreateWindowExW.Call(
			0,
			uintptr(unsafe.Pointer(utf16Ptr("EDIT"))),
			0,
			style,
			uintptr(box.left+w.px(10)), uintptr(box.top+w.px(7)),
			uintptr((box.right-box.left)-w.px(20)), uintptr((box.bottom-box.top)-w.px(14)),
			uintptr(w.hwnd), id, uintptr(getModuleHandle()), 0,
		)
		ed := syscall.Handle(h)
		if ed != 0 {
			procSendMessageW.Call(h, 0x0030, uintptr(w.fontBody), 1)
			procSendMessageW.Call(h, emSetLimitTxt, 512, 0)
			procSendMessageW.Call(h, emSetMargins, 3, uintptr(w.px(2))|uintptr(w.px(2)<<16))
			if initial != "" {
				procSetWindowTextW.Call(h, uintptr(unsafe.Pointer(utf16Ptr(initial))))
			}
		}
		return ed
	}
	w.editSig = makeEdit(601, w.sigBoxR, sig)
	w.editWeb = makeEdit(602, w.webBoxR, web)
	w.editStun = makeEdit(603, w.stunBoxR, stun)
}

func (w *srvWin) ncHitTest(hwnd syscall.Handle, lParam uintptr) uintptr {
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

func (w *srvWin) paint(hwnd syscall.Handle) {
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
	if w.hover == srvHoverClos {
		cv.fillColorAlpha(w.closeRect, w.pal.danger, 230)
		closeInk = col(0xFF, 0xFF, 0xFF)
	}
	drawCloseGlyph(cv, w.closeRect, closeInk)

	pad := w.px(24)
	cv.drawAlphaText(w.fontTitle, "服务器地址设置", rectT{pad, w.px(18), w.closeRect.left - pClamp(w.px(8)), w.px(44)},
		dtSingleLine|dtVCenter, w.pal.ink)
	cv.drawAlphaText(w.fontSm, "留空并保存即恢复对应项默认值；修改后立即重连信令", rectT{pad, w.px(44), cr.right - pad, w.px(64)},
		dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)

	drawField := func(lbl string, box rectT) {
		cv.drawAlphaText(w.fontSm, lbl, rectT{box.left, box.top - w.px(22), box.right, box.top - w.px(2)},
			dtSingleLine|dtVCenter, w.pal.ink2)
		cv.drawAlphaRoundRect(box, float64(w.px(8)), w.pal.paper2, 245, w.pal.line, 255, 1.0)
	}
	drawField("信令地址 (WSS)", w.sigBoxR)
	drawField("临时快传网址 (HTTPS)", w.webBoxR)
	drawField("STUN 服务器 (host:port，逗号分隔)", w.stunBoxR)

	if w.errMsg != "" {
		cv.drawAlphaText(w.fontSm, w.errMsg, rectT{pad, w.stunBoxR.bottom + w.px(6), cr.right - pad, w.cancelRect.top - w.px(4)},
			dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.danger)
	}

	rstFill := w.pal.paper2
	if w.hover == srvHoverRst {
		rstFill = w.pal.paper3
	}
	cv.drawAlphaRoundRect(w.resetRect, float64(w.px(7)), rstFill, 235, w.pal.line, 255, 1.0)
	cv.drawAlphaText(w.fontSm, "恢复默认", w.resetRect, dtCenter|dtSingleLine|dtVCenter, w.pal.ink)

	cancFill := w.pal.paper2
	if w.hover == srvHoverCanc {
		cancFill = w.pal.paper3
	}
	cv.drawAlphaRoundRect(w.cancelRect, float64(w.px(7)), cancFill, 235, w.pal.line, 255, 1.0)
	cv.drawAlphaText(w.fontSm, "取消", w.cancelRect, dtCenter|dtSingleLine|dtVCenter, w.pal.ink)

	okFill := w.pal.accent
	if w.hover == srvHoverOk {
		okFill = shade(okFill, w.dark, 0.12)
	}
	cv.drawAlphaRoundRect(w.okRect, float64(w.px(7)), okFill, 255, okFill, 255, 0)
	cv.drawAlphaText(w.fontSm, "保存", w.okRect, dtCenter|dtSingleLine|dtVCenter, w.pal.accentInk)

	cv.finishBlit(hdc)
}

func (w *srvWin) onMouseMove(hwnd syscall.Handle, x, y int32) {
	z := srvHoverNone
	switch {
	case ptIn(w.closeRect, x, y):
		z = srvHoverClos
	case ptIn(w.resetRect, x, y):
		z = srvHoverRst
	case ptIn(w.cancelRect, x, y):
		z = srvHoverCanc
	case ptIn(w.okRect, x, y):
		z = srvHoverOk
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

func (w *srvWin) onClick(hwnd syscall.Handle, x, y int32) {
	switch {
	case ptIn(w.closeRect, x, y), ptIn(w.cancelRect, x, y):
		procDestroyWindow.Call(uintptr(hwnd))
	case ptIn(w.resetRect, x, y):
		if w.app != nil {
			if err := w.app.resetServerConfig(); err != nil {
				w.errMsg = err.Error()
				procInvalidateRect.Call(uintptr(hwnd), 0, 0)
				return
			}
		}
		cb := w.onSaved
		procDestroyWindow.Call(uintptr(hwnd))
		if cb != nil {
			cb()
		}
	case ptIn(w.okRect, x, y):
		if w.app != nil {
			sig := readEditText(w.editSig)
			web := readEditText(w.editWeb)
			stun := readEditText(w.editStun)
			if err := w.app.setServerConfig(sig, web, stun); err != nil {
				w.errMsg = err.Error()
				procInvalidateRect.Call(uintptr(hwnd), 0, 0)
				return
			}
		}
		cb := w.onSaved
		procDestroyWindow.Call(uintptr(hwnd))
		if cb != nil {
			cb()
		}
	}
}
