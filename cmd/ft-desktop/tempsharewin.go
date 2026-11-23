//go:build windows

package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/lanweb"
	"github.com/cwlindean/filetransfer/core/tempshare"
)

const (
	tempClassName = "FerryTempShareWin"
	tempTimerID   = 2
	wmTempUpdate  = wmApp + 45

	tsModeSelect = 0
	tsModeHost   = 1
	tsModeJoin   = 2

	tsHoverNone   = 0
	tsHoverClose  = 1
	tsHoverSend   = 2
	tsHoverRecv   = 3
	tsHoverToJoin = 4
	tsHoverCopy   = 5
	tsHoverStop   = 6
	tsHoverJoinGo = 7
	tsHoverBack   = 8
)

var (
	tempClassOnce sync.Once
	tempWndProcCB = syscall.NewCallback(tempWndProc)

	tempWinMu   sync.Mutex
	tempWinH    syscall.Handle
	tempWins    = map[syscall.Handle]*tempWin{}
	tempEdits   = map[syscall.Handle]uintptr{}
	tempOwners  = map[syscall.Handle]*tempWin{}
	pendingTApp *app
	pendingTOwn syscall.Handle
)

type tempWin struct {
	hwnd     syscall.Handle
	owner    syscall.Handle
	app      *app
	pal      palette
	dark     bool
	acrylic  bool
	dpi      int32
	mode     int
	hover    int
	tracking bool
	busy     bool
	status   string
	errMsg   string

	host   *tempshare.Host
	qrBits [][]bool

	codeEdit syscall.Handle
	editBg   syscall.Handle

	fontTitle syscall.Handle
	fontSub   syscall.Handle
	fontBody  syscall.Handle
	fontSm    syscall.Handle
	fontCode  syscall.Handle

	closeRect  rectT
	sendCardR  rectT
	recvCardR  rectT
	toJoinR    rectT
	qrRect     rectT
	copyRect   rectT
	stopRect   rectT
	codeBoxR   rectT
	joinGoRect rectT
	backRect   rectT
}

// openTempShareWindow opens the Acrylic temporary share window (send/recv HTTPS QR or join by 6-digit code).
func openTempShareWindow(owner syscall.Handle, a *app) {
	tempWinMu.Lock()
	prev := tempWinH
	tempWinMu.Unlock()
	if prev != 0 {
		procSetForegroundWindow.Call(uintptr(prev))
		return
	}

	pal, _ := theme()
	dpi := screenDPI()
	if owner != 0 {
		dpi = windowDPI(owner)
	}
	ww := scalePx(dpi, 420)
	wh := scalePx(dpi, 496)

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
	cls := utf16Ptr(tempClassName)
	tempClassOnce.Do(func() {
		cur, _, _ := procLoadCursorW.Call(0, idcArrow)
		bg, _, _ := procCreateBrush.Call(uintptr(pal.paper))
		wc := wndClassExW{
			cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
			lpfnWndProc:   tempWndProcCB,
			hInstance:     hInst,
			hIcon:         loadAppIcon(0),
			hIconSm:       loadAppIcon(16),
			hCursor:       syscall.Handle(cur),
			hbrBackground: syscall.Handle(bg),
			lpszClassName: cls,
		}
		procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
	})

	tempWinMu.Lock()
	pendingTApp = a
	pendingTOwn = owner
	tempWinMu.Unlock()

	const style = 0x00C00000 | 0x02000000
	h, _, _ := procCreateWindowExW.Call(
		0x00010000,
		uintptr(unsafe.Pointer(cls)),
		uintptr(unsafe.Pointer(utf16Ptr("临时快传"))),
		style,
		uintptr(x), uintptr(y), uintptr(ww), uintptr(wh),
		uintptr(owner), 0, uintptr(hInst), 0,
	)
	hwnd := syscall.Handle(h)
	if hwnd == 0 {
		tempWinMu.Lock()
		pendingTApp = nil
		pendingTOwn = 0
		tempWinMu.Unlock()
		return
	}
	procShowWindow.Call(uintptr(hwnd), swShow)
	procUpdateWindow.Call(uintptr(hwnd))
}

func currentTempWin(hwnd syscall.Handle) *tempWin {
	tempWinMu.Lock()
	defer tempWinMu.Unlock()
	return tempWins[hwnd]
}

func tempWndProc(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	tw := currentTempWin(hwnd)
	switch msg {
	case wmCreate:
		pal, dark := theme()
		tempWinMu.Lock()
		a := pendingTApp
		own := pendingTOwn
		pendingTApp = nil
		pendingTOwn = 0
		tempWinMu.Unlock()
		w := &tempWin{
			hwnd:  hwnd,
			owner: own,
			app:   a,
			pal:   pal,
			dark:  dark,
			dpi:   windowDPI(hwnd),
			mode:  tsModeSelect,
		}
		procSetWindowPos.Call(uintptr(hwnd), 0, 0, 0, 0, 0, 0x0027)
		w.acrylic = enableAcrylicBackdrop(hwnd, dark)
		w.editBg = syscall.Handle(createPairBrush(pal.paper2))
		w.makeFonts()
		tempWinMu.Lock()
		tempWinH = hwnd
		tempWins[hwnd] = w
		tempWinMu.Unlock()
		procSetTimer.Call(uintptr(hwnd), tempTimerID, 1000, 0)
		return 0

	case wmNcCalcSize:
		if wParam != 0 {
			return 0
		}

	case wmNcHitTest:
		if tw != nil {
			return tw.ncHitTest(hwnd, lParam)
		}

	case wmEraseBkgnd:
		return 1

	case wmPaint:
		if tw != nil {
			tw.paint(hwnd)
			return 0
		}

	case wmMouseMove:
		if tw != nil {
			tw.onMouseMove(hwnd, int32(lParam&0xffff), int32((lParam>>16)&0xffff))
			return 0
		}

	case wmMouseLeave:
		if tw != nil {
			tw.tracking = false
			if tw.hover != tsHoverNone {
				tw.hover = tsHoverNone
				procInvalidateRect.Call(uintptr(hwnd), 0, 0)
			}
			return 0
		}

	case wmSetCursor:
		if tw != nil && loword(lParam) == htClient && tw.hover > tsHoverClose {
			h, _, _ := procLoadCursorW.Call(0, idcHand)
			procSetCursor.Call(h)
			return 1
		}

	case wmLButtonUp:
		if tw != nil {
			tw.onClick(hwnd, int32(lParam&0xffff), int32((lParam>>16)&0xffff))
			return 0
		}

	case wmTimer:
		if tw != nil && wParam == tempTimerID && tw.mode == tsModeHost {
			procInvalidateRect.Call(uintptr(hwnd), 0, 0)
			return 0
		}

	case wmTempUpdate:
		procInvalidateRect.Call(uintptr(hwnd), 0, 0)
		return 0

	case wmCtlColorEdit:
		if tw != nil {
			hdc := wParam
			procSetBkColor.Call(hdc, uintptr(tw.pal.paper2))
			procSetBkMode.Call(hdc, 2)
			procSetTextColor.Call(hdc, uintptr(tw.pal.ink))
			return uintptr(tw.editBg)
		}

	case wmClose:
		procDestroyWindow.Call(uintptr(hwnd))
		return 0

	case wmDestroy:
		procKillTimer.Call(uintptr(hwnd), tempTimerID)
		if tw != nil {
			if tw.host != nil {
				h := tw.host
				tw.host = nil
				go func() { _ = h.Close() }()
			}
			if tw.codeEdit != 0 {
				tempWinMu.Lock()
				delete(tempEdits, tw.codeEdit)
				delete(tempOwners, tw.codeEdit)
				tempWinMu.Unlock()
			}
			tw.freeResources()
		}
		tempWinMu.Lock()
		delete(tempWins, hwnd)
		if tempWinH == hwnd {
			tempWinH = 0
		}
		tempWinMu.Unlock()
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return r
}

func (w *tempWin) px(v int32) int32 {
	dpi := w.dpi
	if dpi == 0 {
		dpi = 96
	}
	return int32(float64(v)*float64(dpi)/96.0 + 0.5)
}

func (w *tempWin) makeFonts() {
	w.fontTitle = createUIFont(w.px(18), 700)
	w.fontSub = createUIFont(w.px(13), 400)
	w.fontBody = createUIFont(w.px(13), 600)
	w.fontSm = createUIFont(w.px(12), 400)
	w.fontCode = createUIFont(w.px(24), 700)
}

func (w *tempWin) freeResources() {
	for _, f := range []syscall.Handle{w.fontTitle, w.fontSub, w.fontBody, w.fontSm, w.fontCode} {
		if f != 0 {
			procDeleteObject.Call(uintptr(f))
		}
	}
	if w.editBg != 0 {
		procDeleteObject.Call(uintptr(w.editBg))
	}
}

func (w *tempWin) layout(cw, ch int32) {
	pad := w.px(26)
	w.closeRect = rectT{cw - w.px(38), 0, cw, w.px(32)}
	w.sendCardR, w.recvCardR, w.toJoinR = rectT{}, rectT{}, rectT{}
	w.qrRect, w.copyRect, w.stopRect = rectT{}, rectT{}, rectT{}
	w.codeBoxR, w.joinGoRect, w.backRect = rectT{}, rectT{}, rectT{}

	switch w.mode {
	case tsModeSelect:
		w.sendCardR = rectT{pad, w.px(110), cw - pad, w.px(210)}
		w.recvCardR = rectT{pad, w.px(226), cw - pad, w.px(326)}
		w.toJoinR = rectT{pad, ch - w.px(52), cw - pad, ch - w.px(22)}
	case tsModeHost:
		qrSz := w.px(208)
		qx := (cw - qrSz) / 2
		qy := w.px(96)
		w.qrRect = rectT{qx, qy, qx + qrSz, qy + qrSz}
		btnY := ch - w.px(60)
		half := (cw - pad*2 - w.px(12)) / 2
		w.copyRect = rectT{pad, btnY, pad + half, btnY + w.px(38)}
		w.stopRect = rectT{w.copyRect.right + w.px(12), btnY, cw - pad, btnY + w.px(38)}
	case tsModeJoin:
		w.codeBoxR = rectT{pad, w.px(150), cw - pad, w.px(196)}
		w.joinGoRect = rectT{pad, w.px(214), cw - pad, w.px(254)}
		w.backRect = rectT{pad, ch - w.px(52), cw - pad, ch - w.px(22)}
	}
}

func (w *tempWin) ensureJoinEdit() {
	if w.codeEdit != 0 {
		procShowWindow.Call(uintptr(w.codeEdit), swShow)
		procSetFocus.Call(uintptr(w.codeEdit))
		return
	}
	var cr rectT
	procGetClientRect.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&cr)))
	w.layout(cr.right, cr.bottom)
	const (
		wsChild   = 0x40000000
		wsVisible = 0x10000000
		esCenter  = 0x0001
		esNumber  = 0x2000
	)
	inX := w.codeBoxR.left + w.px(12)
	inY := w.codeBoxR.top + w.px(10)
	inW := (w.codeBoxR.right - w.codeBoxR.left) - w.px(24)
	inH := (w.codeBoxR.bottom - w.codeBoxR.top) - w.px(20)
	h, _, _ := procCreateWindowExW.Call(
		0,
		uintptr(unsafe.Pointer(utf16Ptr("EDIT"))),
		0,
		wsChild|wsVisible|esCenter|esNumber,
		uintptr(inX), uintptr(inY), uintptr(inW), uintptr(inH),
		uintptr(w.hwnd), 501, uintptr(getModuleHandle()), 0,
	)
	w.codeEdit = syscall.Handle(h)
	if w.codeEdit != 0 {
		procSendMessageW.Call(h, 0x0030, uintptr(w.fontCode), 1)
		procSendMessageW.Call(h, 0x00C5, 6, 0) // EM_SETLIMITTEXT
		procSetFocus.Call(h)
	}
}

func (w *tempWin) hideJoinEdit() {
	if w.codeEdit != 0 {
		procShowWindow.Call(uintptr(w.codeEdit), 0)
	}
}

func (w *tempWin) ncHitTest(hwnd syscall.Handle, lParam uintptr) uintptr {
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

func (w *tempWin) paint(hwnd syscall.Handle) {
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
	if w.hover == tsHoverClose {
		cv.fillColorAlpha(w.closeRect, w.pal.danger, 230)
		closeInk = col(0xFF, 0xFF, 0xFF)
	}
	drawCloseGlyph(cv, w.closeRect, closeInk)

	pad := w.px(26)
	switch w.mode {
	case tsModeSelect:
		cv.drawAlphaText(w.fontTitle, "临时快传", rectT{pad, w.px(28), cr.right - pad, w.px(56)},
			dtCenter|dtSingleLine|dtVCenter, w.pal.ink)
		cv.drawAlphaText(w.fontSm, "生成 HTTPS 二维码与 6 位码，对方用浏览器或轻渡即可收发",
			rectT{pad, w.px(60), cr.right - pad, w.px(88)}, dtCenter|dtWordBreak, w.pal.ink2)

		sFill := w.pal.paper2
		sEdge := w.pal.line
		if w.hover == tsHoverSend {
			sFill = w.pal.accentSoft
			sEdge = w.pal.accent
		}
		cv.drawAlphaRoundRect(w.sendCardR, float64(w.px(12)), sFill, 240, sEdge, 255, 1.2)
		cv.drawAlphaText(w.fontBody, "我要发文件",
			rectT{w.sendCardR.left + w.px(18), w.sendCardR.top + w.px(20), w.sendCardR.right - w.px(18), w.sendCardR.top + w.px(46)},
			dtSingleLine|dtVCenter, w.pal.ink)
		cv.drawAlphaText(w.fontSm, "选择要分享的文件，对方扫码或输码直接下载",
			rectT{w.sendCardR.left + w.px(18), w.sendCardR.top + w.px(50), w.sendCardR.right - w.px(18), w.sendCardR.bottom - w.px(16)},
			dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)

		rFill := w.pal.paper2
		rEdge := w.pal.line
		if w.hover == tsHoverRecv {
			rFill = w.pal.accentSoft
			rEdge = w.pal.accent
		}
		cv.drawAlphaRoundRect(w.recvCardR, float64(w.px(12)), rFill, 240, rEdge, 255, 1.2)
		cv.drawAlphaText(w.fontBody, "我要收文件",
			rectT{w.recvCardR.left + w.px(18), w.recvCardR.top + w.px(20), w.recvCardR.right - w.px(18), w.recvCardR.top + w.px(46)},
			dtSingleLine|dtVCenter, w.pal.ink)
		cv.drawAlphaText(w.fontSm, "等待对方上传文件到本机接收目录（不暴露旧文件）",
			rectT{w.recvCardR.left + w.px(18), w.recvCardR.top + w.px(50), w.recvCardR.right - w.px(18), w.recvCardR.bottom - w.px(16)},
			dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink2)

		if w.status != "" {
			cv.drawAlphaText(w.fontSm, w.status, rectT{pad, w.px(344), cr.right - pad, w.px(384)},
				dtCenter|dtWordBreak, w.pal.ink2)
		}
		if w.errMsg != "" {
			cv.drawAlphaText(w.fontSm, w.errMsg, rectT{pad, w.px(344), cr.right - pad, w.px(384)},
				dtCenter|dtWordBreak, w.pal.danger)
		}
		cv.drawAlphaText(w.fontSm, "输入对方 6 位临时码 →", w.toJoinR, dtCenter|dtSingleLine|dtVCenter, w.pal.accent)

	case tsModeHost:
		st := w.host.Status()
		modeSub := "等待对方上传文件到本机"
		if st.Mode == "send" {
			modeSub = fmt.Sprintf("仅分享已选中的 %d 个文件", st.FileCount)
		}
		cv.drawAlphaText(w.fontTitle, "临时快传进行中", rectT{pad, w.px(26), cr.right - pad, w.px(54)},
			dtCenter|dtSingleLine|dtVCenter, w.pal.ink)
		cv.drawAlphaText(w.fontSm, modeSub, rectT{pad, w.px(56), cr.right - pad, w.px(80)},
			dtCenter|dtSingleLine|dtVCenter, w.pal.accent)

		cv.drawAlphaRoundRect(w.qrRect, float64(w.px(12)), col(0xFF, 0xFF, 0xFF), 255, w.pal.line, 255, 1.0)
		if len(w.qrBits) > 0 {
			n := len(w.qrBits)
			inset := pClamp(w.px(12))
			avail := (w.qrRect.right - w.qrRect.left) - inset*2
			mod := avail / int32(n)
			if mod < 1 {
				mod = 1
			}
			drawSz := mod * int32(n)
			ox := w.qrRect.left + ((w.qrRect.right-w.qrRect.left)-drawSz)/2
			oy := w.qrRect.top + ((w.qrRect.bottom-w.qrRect.top)-drawSz)/2
			darkCol := col(0x11, 0x18, 0x27)
			for r := 0; r < n; r++ {
				for c := 0; c < n; c++ {
					if w.qrBits[r][c] {
						mr := rectT{ox + int32(c)*mod, oy + int32(r)*mod, ox + int32(c+1)*mod, oy + int32(r+1)*mod}
						fillRect(cv.memDC, mr, darkCol)
					}
				}
			}
			cv.makeOpaqueRect(rectT{ox, oy, ox + drawSz, oy + drawSz})
		}

		codeStr := st.Code
		if len(codeStr) == 6 {
			codeStr = codeStr[:3] + " " + codeStr[3:]
		}
		cy := w.qrRect.bottom + pClamp(w.px(14))
		cv.drawAlphaText(w.fontCode, codeStr, rectT{pad, cy, cr.right - pad, cy + w.px(32)},
			dtCenter|dtSingleLine|dtVCenter, w.pal.ink)

		ttlText := fmt.Sprintf("10 分钟有效 · 剩余 %02d:%02d", st.TTLSeconds/60, st.TTLSeconds%60)
		cv.drawAlphaText(w.fontSm, ttlText, rectT{pad, cy + w.px(36), cr.right - pad, cy + w.px(58)},
			dtCenter|dtSingleLine|dtVCenter, w.pal.ink2)

		infoText := st.ShareURL
		if st.UploadedCount > 0 {
			infoText = fmt.Sprintf("已收到 %d 个文件 · %s", st.UploadedCount, st.LastFile)
		} else if w.status != "" {
			infoText = w.status
		}
		cv.drawAlphaText(w.fontSm, infoText, rectT{pad, cy + w.px(62), cr.right - pad, cy + w.px(88)},
			dtCenter|dtSingleLine|dtVCenter|dtEndEllipsis, w.pal.ink3)

		cFill := w.pal.paper2
		if w.hover == tsHoverCopy {
			cFill = w.pal.paper3
		}
		cv.drawAlphaRoundRect(w.copyRect, float64(w.px(8)), cFill, 240, w.pal.line, 255, 1.0)
		cv.drawAlphaText(w.fontSm, "复制链接", w.copyRect, dtCenter|dtSingleLine|dtVCenter, w.pal.ink)

		sFill := w.pal.danger
		if w.hover == tsHoverStop {
			sFill = shade(sFill, w.dark, 0.12)
		}
		cv.drawAlphaRoundRect(w.stopRect, float64(w.px(8)), sFill, 255, sFill, 255, 0)
		cv.drawAlphaText(w.fontSm, "结束快传", w.stopRect, dtCenter|dtSingleLine|dtVCenter, col(0xFF, 0xFF, 0xFF))

	case tsModeJoin:
		cv.drawAlphaText(w.fontTitle, "输入 6 位临时码", rectT{pad, w.px(34), cr.right - pad, w.px(62)},
			dtCenter|dtSingleLine|dtVCenter, w.pal.ink)
		cv.drawAlphaText(w.fontSm, "输入发起方设备上的 6 位数字码，自动优先局域网直连",
			rectT{pad, w.px(68), cr.right - pad, w.px(96)}, dtCenter|dtWordBreak, w.pal.ink2)

		cv.drawAlphaRoundRect(w.codeBoxR, float64(w.px(10)), w.pal.paper2, 245, w.pal.line, 255, 1.0)

		jFill := w.pal.accent
		if w.hover == tsHoverJoinGo {
			jFill = shade(jFill, w.dark, 0.12)
		}
		cv.drawAlphaRoundRect(w.joinGoRect, float64(w.px(8)), jFill, 255, jFill, 255, 0)
		btnTxt := "连接"
		if w.busy {
			btnTxt = "处理中…"
		}
		cv.drawAlphaText(w.fontBody, btnTxt, w.joinGoRect, dtCenter|dtSingleLine|dtVCenter, w.pal.accentInk)

		if w.errMsg != "" {
			cv.drawAlphaText(w.fontSm, w.errMsg, rectT{pad, w.px(274), cr.right - pad, w.px(340)},
				dtCenter|dtWordBreak, w.pal.danger)
		} else if w.status != "" {
			cv.drawAlphaText(w.fontSm, w.status, rectT{pad, w.px(274), cr.right - pad, w.px(340)},
				dtCenter|dtWordBreak, w.pal.ink2)
		}
		cv.drawAlphaText(w.fontSm, "← 返回发起快传", w.backRect, dtCenter|dtSingleLine|dtVCenter, w.pal.accent)
	}

	cv.finishBlit(hdc)
}

func (w *tempWin) onMouseMove(hwnd syscall.Handle, x, y int32) {
	z := tsHoverNone
	switch {
	case ptIn(w.closeRect, x, y):
		z = tsHoverClose
	case w.mode == tsModeSelect && ptIn(w.sendCardR, x, y):
		z = tsHoverSend
	case w.mode == tsModeSelect && ptIn(w.recvCardR, x, y):
		z = tsHoverRecv
	case w.mode == tsModeSelect && ptIn(w.toJoinR, x, y):
		z = tsHoverToJoin
	case w.mode == tsModeHost && ptIn(w.copyRect, x, y):
		z = tsHoverCopy
	case w.mode == tsModeHost && ptIn(w.stopRect, x, y):
		z = tsHoverStop
	case w.mode == tsModeJoin && ptIn(w.joinGoRect, x, y):
		z = tsHoverJoinGo
	case w.mode == tsModeJoin && ptIn(w.backRect, x, y):
		z = tsHoverBack
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

func (w *tempWin) onClick(hwnd syscall.Handle, x, y int32) {
	if ptIn(w.closeRect, x, y) {
		procDestroyWindow.Call(uintptr(hwnd))
		return
	}
	switch w.mode {
	case tsModeSelect:
		switch {
		case ptIn(w.sendCardR, x, y) && !w.busy:
			files := openFileDialog(uintptr(hwnd))
			if len(files) > 0 {
				w.startHost("send", files)
			}
		case ptIn(w.recvCardR, x, y) && !w.busy:
			w.startHost("recv", nil)
		case ptIn(w.toJoinR, x, y):
			w.mode = tsModeJoin
			w.status, w.errMsg = "", ""
			w.ensureJoinEdit()
			procInvalidateRect.Call(uintptr(hwnd), 0, 0)
		}
	case tsModeHost:
		switch {
		case ptIn(w.copyRect, x, y) && w.host != nil:
			copyToClipboard(hwnd, w.host.Status().ShareURL)
			w.status = "已复制临时快传链接"
			procInvalidateRect.Call(uintptr(hwnd), 0, 0)
		case ptIn(w.stopRect, x, y):
			procDestroyWindow.Call(uintptr(hwnd))
		}
	case tsModeJoin:
		switch {
		case ptIn(w.backRect, x, y) && !w.busy:
			w.hideJoinEdit()
			w.mode = tsModeSelect
			w.status, w.errMsg = "", ""
			procInvalidateRect.Call(uintptr(hwnd), 0, 0)
		case ptIn(w.joinGoRect, x, y) && !w.busy:
			w.joinByCode()
		}
	}
}

func (w *tempWin) startHost(mode string, files []string) {
	if w.app == nil {
		return
	}
	w.busy = true
	w.errMsg = ""
	w.status = "正在创建临时快传会话…"
	procInvalidateRect.Call(uintptr(w.hwnd), 0, 0)

	a := w.app
	hwnd := w.hwnd
	own := w.owner
	go func() {
		key, err := identity.LoadOrCreateKey(a.keyPath)
		var host *tempshare.Host
		if err == nil {
			host, err = tempshare.StartHost(tempshare.HostOptions{
				BaseURL:     a.webBase,
				Key:         key,
				DeviceName:  a.deviceName(),
				Mode:        mode,
				SharedFiles: files,
				RecvDir:     a.receiveDir(),
				OnUploaded: func(f lanweb.File) {
					a.note(fmt.Sprintf("收到 %s · 来自 临时快传（网页）", f.Name))
					if own != 0 {
						procInvalidateRect.Call(uintptr(own), 0, 1)
					}
					procPostMessageW.Call(uintptr(hwnd), wmTempUpdate, 0, 0)
				},
			})
		}
		if err != nil {
			tempWinMu.Lock()
			cur := tempWins[hwnd]
			tempWinMu.Unlock()
			if cur != nil {
				cur.busy = false
				cur.errMsg = "创建失败：" + err.Error()
				cur.status = ""
				procPostMessageW.Call(uintptr(hwnd), wmTempUpdate, 0, 0)
			}
			return
		}
		var bits [][]bool
		if q, qerr := qrcode.New(host.Status().ShareURL, qrcode.Medium); qerr == nil {
			bits = q.Bitmap()
		}
		tempWinMu.Lock()
		cur := tempWins[hwnd]
		tempWinMu.Unlock()
		if cur == nil {
			_ = host.Close()
			return
		}
		cur.busy = false
		cur.host = host
		cur.qrBits = bits
		cur.mode = tsModeHost
		cur.status = ""
		cur.errMsg = ""
		procPostMessageW.Call(uintptr(hwnd), wmTempUpdate, 0, 0)
	}()
}

func (w *tempWin) joinByCode() {
	if w.app == nil {
		return
	}
	code := strings.TrimSpace(readEditText(w.codeEdit))
	if len(code) != 6 {
		w.errMsg = "请输入 6 位数字码"
		procInvalidateRect.Call(uintptr(w.hwnd), 0, 0)
		return
	}
	w.busy = true
	w.errMsg = ""
	w.status = "正在查找并连接对方…"
	procInvalidateRect.Call(uintptr(w.hwnd), 0, 0)

	a := w.app
	hwnd := w.hwnd
	own := w.owner
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		sess, err := tempshare.ResolveByCode(ctx, a.webBase, code)
		if err != nil {
			w.finishJoinError(hwnd, err.Error())
			return
		}
		if sess.Mode == "send" {
			via, saved, err := tempshare.DownloadAll(context.Background(), sess, a.receiveDir())
			if err != nil {
				w.finishJoinError(hwnd, "下载失败："+err.Error())
				return
			}
			viaLabel := "云端中继"
			if via == "lan" {
				viaLabel = "局域网直连"
			}
			a.note(fmt.Sprintf("收到 %d 个文件 · 来自 %s（%s）", len(saved), sess.Name, viaLabel))
			if own != 0 {
				procInvalidateRect.Call(uintptr(own), 0, 1)
			}
			tempWinMu.Lock()
			cur := tempWins[hwnd]
			tempWinMu.Unlock()
			if cur != nil {
				cur.busy = false
				cur.status = fmt.Sprintf("已通过%s下载 %d 个文件到接收目录", viaLabel, len(saved))
				procPostMessageW.Call(uintptr(hwnd), wmTempUpdate, 0, 0)
			}
			return
		}

		// Host is in recv/both mode: let user pick files to upload.
		files := openFileDialog(uintptr(hwnd))
		if len(files) == 0 {
			tempWinMu.Lock()
			cur := tempWins[hwnd]
			tempWinMu.Unlock()
			if cur != nil {
				cur.busy = false
				cur.status = ""
				procPostMessageW.Call(uintptr(hwnd), wmTempUpdate, 0, 0)
			}
			return
		}
		via, uploaded, err := tempshare.UploadFiles(context.Background(), sess, files)
		if err != nil {
			w.finishJoinError(hwnd, "上传失败："+err.Error())
			return
		}
		viaLabel := "云端中继"
		if via == "lan" {
			viaLabel = "局域网直连"
		}
		a.note(fmt.Sprintf("发往 %s · %d 个文件（%s）", sess.Name, len(uploaded), viaLabel))
		if own != 0 {
			procInvalidateRect.Call(uintptr(own), 0, 1)
		}
		tempWinMu.Lock()
		cur := tempWins[hwnd]
		tempWinMu.Unlock()
		if cur != nil {
			cur.busy = false
			cur.status = fmt.Sprintf("已通过%s发送 %d 个文件给 %s", viaLabel, len(uploaded), sess.Name)
			procPostMessageW.Call(uintptr(hwnd), wmTempUpdate, 0, 0)
		}
	}()
}

func (w *tempWin) finishJoinError(hwnd syscall.Handle, msg string) {
	tempWinMu.Lock()
	cur := tempWins[hwnd]
	tempWinMu.Unlock()
	if cur != nil {
		cur.busy = false
		cur.errMsg = msg
		cur.status = ""
		procPostMessageW.Call(uintptr(hwnd), wmTempUpdate, 0, 0)
	}
}
