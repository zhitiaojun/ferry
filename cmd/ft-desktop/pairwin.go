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

	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/pairing"
	qrcode "github.com/skip2/go-qrcode"
)

const (
	pairClassName  = "FerryPairWin"
	wmPairEvent    = wmApp + 31
	wmCtlColorEdit = 0x0133
	pairTimerID    = 21

	pairIDEditCode = 301
	pairIDEditPeer = 302
)

var (
	pairMu   sync.Mutex
	pairWinH syscall.Handle
	pairObjs = map[syscall.Handle]*pairWin{}
)

type pairWin struct {
	hwnd    syscall.Handle
	app     *app
	pal     palette
	dark    bool
	acrylic bool
	dpi     int32

	manualMode bool
	busy       bool
	expired    bool
	code       string
	uri        string
	expiresAt  time.Time
	qrBits     [][]bool
	status     string
	hoverZone  int
	tracking   bool

	editCode syscall.Handle
	editPeer syscall.Handle
	editBg   syscall.Handle

	fontTitle syscall.Handle
	fontSub   syscall.Handle
	fontCode  syscall.Handle
	fontBody  syscall.Handle
	fontSm    syscall.Handle

	mu       sync.Mutex
	gen      uint64
	cancelFn context.CancelFunc
	lastErr  string
	lastDone bool

	// Hit-test rectangles calculated in layout()
	closeRect   rectT
	qrRect      rectT
	regenRect   rectT
	copyRect    rectT
	refreshRect rectT
	submitRect  rectT
	switchRect  rectT
}

// openPairWindow shows the QR code + 6-digit pairing dialog (or brings it
// forward if already open). Must run on the tray UI thread.
func openPairWindow(owner syscall.Handle, a *app) {
	if a == nil {
		return
	}
	pairMu.Lock()
	existing := pairWinH
	pairMu.Unlock()
	if existing != 0 {
		procShowWindow.Call(uintptr(existing), swShow)
		procSetForegroundWindow.Call(uintptr(existing))
		return
	}

	pal, _ := theme()
	dpi := screenDPI()
	if owner != 0 {
		dpi = windowDPI(owner)
	}
	ww := scalePx(dpi, 372)
	wh := scalePx(dpi, 488)

	// Center over owner window when visible, else over work area.
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
	cls := utf16Ptr(pairClassName)
	cur, _, _ := procLoadCursorW.Call(0, idcArrow)
	icon := loadAppIcon(0)
	bg, _, _ := procCreateBrush.Call(uintptr(pal.paper))
	wc := wndClassExW{
		cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		lpfnWndProc:   syscall.NewCallback(pairWndProc),
		hInstance:     hInst,
		hIcon:         icon,
		hIconSm:       loadAppIcon(16),
		hCursor:       syscall.Handle(cur),
		hbrBackground: syscall.Handle(bg),
		lpszClassName: cls,
	}
	procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))

	// WS_CAPTION | WS_CLIPCHILDREN (omit WS_SYSMENU so DWM draws no ghost close button)
	const style = 0x00C00000 | 0x02000000
	trayApp = a
	h, _, _ := procCreateWindowExW.Call(
		0x00010000, // WS_EX_CONTROLPARENT
		uintptr(unsafe.Pointer(cls)),
		uintptr(unsafe.Pointer(utf16Ptr("添加设备"))),
		style,
		uintptr(x), uintptr(y), uintptr(ww), uintptr(wh),
		uintptr(owner), 0, uintptr(hInst), 0,
	)
	hwnd := syscall.Handle(h)
	if hwnd == 0 {
		return
	}
	procShowWindow.Call(uintptr(hwnd), swShow)
	procUpdateWindow.Call(uintptr(hwnd))
}

func currentPairWin(hwnd syscall.Handle) *pairWin {
	pairMu.Lock()
	defer pairMu.Unlock()
	return pairObjs[hwnd]
}

func pairWndProc(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	pw := currentPairWin(hwnd)
	switch msg {
	case wmCreate:
		pal, dark := theme()
		p := &pairWin{
			hwnd: hwnd,
			app:  trayApp,
			pal:  pal,
			dark: dark,
			dpi:  windowDPI(hwnd),
		}
		procSetWindowPos.Call(uintptr(hwnd), 0, 0, 0, 0, 0, 0x0027)
		p.acrylic = enableAcrylicBackdrop(hwnd, dark)
		p.editBg = syscall.Handle(createPairBrush(pal.paper2))
		p.makeFonts()
		p.initEdits()
		pairMu.Lock()
		pairWinH = hwnd
		pairObjs[hwnd] = p
		pairMu.Unlock()
		procSetTimer.Call(uintptr(hwnd), pairTimerID, 1000, 0)
		p.refreshSession()
		return 0

	case wmNcCalcSize:
		if wParam != 0 {
			return 0
		}

	case wmNcHitTest:
		if pw != nil {
			return pw.ncHitTest(hwnd, lParam)
		}

	case wmEraseBkgnd:
		return 1

	case wmPaint:
		if pw != nil {
			pw.paint(hwnd)
			return 0
		}

	case wmMouseMove:
		if pw != nil {
			pw.onMouseMove(hwnd, int32(lParam&0xffff), int32((lParam>>16)&0xffff))
			return 0
		}

	case wmMouseLeave:
		if pw != nil {
			pw.tracking = false
			if pw.hoverZone != 0 {
				pw.hoverZone = 0
				procInvalidateRect.Call(uintptr(hwnd), 0, 0)
			}
			return 0
		}

	case wmSetCursor:
		if pw != nil && loword(lParam) == htClient && pw.hoverZone != 0 && pw.hoverZone != 7 {
			h, _, _ := procLoadCursorW.Call(0, idcHand)
			procSetCursor.Call(h)
			return 1
		}

	case wmLButtonUp:
		if pw != nil {
			pw.onClick(hwnd, int32(lParam&0xffff), int32((lParam>>16)&0xffff))
			return 0
		}

	case wmTimer:
		if pw != nil && wParam == pairTimerID && !pw.manualMode && !pw.expired {
			if time.Now().UTC().After(pw.expiresAt) {
				pw.expired = true
				pw.cancelWait()
				pw.status = "配对码和二维码已超过 10 分钟有效期"
			}
			procInvalidateRect.Call(uintptr(hwnd), 0, 0)
			return 0
		}

	case wmPairEvent:
		if pw != nil {
			pw.onAsyncEvent()
			return 0
		}

	case wmCtlColorEdit:
		if pw != nil {
			hdc := wParam
			procSetBkMode.Call(hdc, 2) // OPAQUE
			procSetTextColor.Call(hdc, uintptr(pw.pal.ink))
			return uintptr(pw.editBg)
		}

	case wmClose:
		procDestroyWindow.Call(uintptr(hwnd))
		return 0

	case wmDestroy:
		procKillTimer.Call(uintptr(hwnd), pairTimerID)
		if pw != nil {
			pw.cancelWait()
			pw.freeResources()
		}
		pairMu.Lock()
		delete(pairObjs, hwnd)
		if pairWinH == hwnd {
			pairWinH = 0
		}
		pairMu.Unlock()
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return r
}

func createPairBrush(color uint32) uintptr {
	b, _, _ := procCreateBrush.Call(uintptr(color))
	return b
}

func (p *pairWin) px(v int32) int32 {
	dpi := p.dpi
	if dpi == 0 {
		dpi = 96
	}
	return int32(float64(v)*float64(dpi)/96.0 + 0.5)
}

func (p *pairWin) makeFonts() {
	p.fontTitle = createUIFont(p.px(17), 700)
	p.fontSub = createUIFont(p.px(12), 400)
	p.fontCode = createUIFont(p.px(20), 700)
	p.fontBody = createUIFont(p.px(13), 600)
	p.fontSm = createUIFont(p.px(12), 400)
}

func (p *pairWin) freeResources() {
	for _, f := range []syscall.Handle{p.fontTitle, p.fontSub, p.fontCode, p.fontBody, p.fontSm} {
		if f != 0 {
			procDeleteObject.Call(uintptr(f))
		}
	}
	if p.editBg != 0 {
		procDeleteObject.Call(uintptr(p.editBg))
	}
}

func (p *pairWin) initEdits() {
	hInst := getModuleHandle()
	// Initially hidden; positioned dynamically on switch to manualMode.
	// WS_CHILD | WS_TABSTOP | WS_BORDER | ES_AUTOHSCROLL = 0x40810080
	const style = 0x40810080
	mkEdit := func(id uint16) syscall.Handle {
		h, _, _ := procCreateWindowExW.Call(
			0,
			uintptr(unsafe.Pointer(utf16Ptr("EDIT"))),
			0,
			style,
			0, 0, 100, 28,
			uintptr(p.hwnd), uintptr(id), uintptr(hInst), 0,
		)
		if h != 0 {
			procSendMessageW.Call(h, 0x0030, uintptr(p.fontBody), 1) // WM_SETFONT
			procSendMessageW.Call(h, 0x00D3, 3, uintptr(p.px(8))|uintptr(p.px(8)<<16))
		}
		return syscall.Handle(h)
	}
	p.editCode = mkEdit(pairIDEditCode)
	p.editPeer = mkEdit(pairIDEditPeer)
}

func (p *pairWin) setManualMode(on bool) {
	p.manualMode = on
	p.status = ""
	if on {
		p.cancelWait()
		p.layoutManual()
		procShowWindow.Call(uintptr(p.editCode), swShow)
		procShowWindow.Call(uintptr(p.editPeer), swShow)
		procSetFocus.Call(uintptr(p.editCode))
	} else {
		procShowWindow.Call(uintptr(p.editCode), swHide)
		procShowWindow.Call(uintptr(p.editPeer), swHide)
		if p.expired || p.code == "" {
			p.refreshSession()
		} else {
			p.startResponder()
		}
	}
	procInvalidateRect.Call(uintptr(p.hwnd), 0, 1)
}

func (p *pairWin) layoutManual() {
	var cr rectT
	procGetClientRect.Call(uintptr(p.hwnd), uintptr(unsafe.Pointer(&cr)))
	pad := p.px(28)
	ew := cr.right - pad*2
	eh := p.px(32)
	procSetWindowPos.Call(uintptr(p.editCode), 0, uintptr(pad), uintptr(p.px(136)), uintptr(ew), uintptr(eh), 0x0004)
	procSetWindowPos.Call(uintptr(p.editPeer), 0, uintptr(pad), uintptr(p.px(214)), uintptr(ew), uintptr(eh), 0x0004)
}

func (p *pairWin) cancelWait() {
	p.mu.Lock()
	cancel := p.cancelFn
	p.cancelFn = nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (p *pairWin) refreshSession() {
	if p.app == nil {
		return
	}
	p.cancelWait()
	p.expired = false
	p.code = pairing.GenerateCode()
	p.expiresAt = time.Now().UTC().Add(pairing.DefaultTTL)
	p.status = "等待手机扫码或输入配对码…"

	key, err := identity.LoadOrCreateKey(p.app.keyPath)
	if err != nil {
		p.status = "读取密钥失败：" + err.Error()
		p.qrBits = nil
		procInvalidateRect.Call(uintptr(p.hwnd), 0, 1)
		return
	}
	host := p.app.deviceName()
	p.uri = pairing.BuildPairURI(key.IDHex(), p.code, host, p.expiresAt)
	if qr, qerr := qrcode.New(p.uri, qrcode.Medium); qerr == nil {
		qr.DisableBorder = true
		p.qrBits = qr.Bitmap()
	} else {
		p.qrBits = nil
	}
	p.startResponder()
	procInvalidateRect.Call(uintptr(p.hwnd), 0, 1)
}

func (p *pairWin) startResponder() {
	p.cancelWait()
	code := p.code
	rem := time.Until(p.expiresAt)
	if rem <= 0 {
		p.expired = true
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), rem)
	p.mu.Lock()
	p.gen++
	gen := p.gen
	p.cancelFn = cancel
	p.mu.Unlock()

	hwnd := p.hwnd
	app := p.app
	go func() {
		defer cancel()
		key, err := identity.LoadOrCreateKey(app.keyPath)
		if err != nil {
			p.reportAsync(gen, false, err.Error())
			return
		}
		paired, err := identity.LoadPaired(app.pairedPath)
		if err != nil {
			p.reportAsync(gen, false, err.Error())
			return
		}
		for ctx.Err() == nil {
			left := time.Until(p.expiresAt)
			if left <= 0 {
				return
			}
			cl, release, derr := app.acquireSignalForPair(ctx, key)
			if derr != nil {
				if ctx.Err() == nil {
					p.reportAsync(gen, false, "信令连接失败，请检查网络或刷新重试")
				}
				return
			}
			peer, perr := pairing.RunWithName(ctx, cl, key, paired, pairing.Respond, code, "", app.deviceName(), left)
			release()
			if perr == nil {
				if serr := paired.Save(); serr != nil {
					p.reportAsync(gen, false, serr.Error())
					return
				}
				display := peer.Name
				if display == "" {
					display = truncID(peer.ID)
				}
				p.reportAsync(gen, true, display)
				return
			}
			if ctx.Err() == nil && strings.Contains(perr.Error(), "signaling connection dropped") {
				select {
				case <-ctx.Done():
					return
				case <-time.After(400 * time.Millisecond):
					continue
				}
			}
			if ctx.Err() == nil {
				p.reportAsync(gen, false, perr.Error())
			}
			return
		}
		_ = hwnd
	}()
}

func (p *pairWin) submitManual() {
	if p.busy || p.app == nil {
		return
	}
	code := strings.TrimSpace(readEditText(p.editCode))
	peerText := strings.TrimSpace(readEditText(p.editPeer))
	var peerName string

	if strings.HasPrefix(peerText, "ferry://") {
		u, err := pairing.ParsePairURI(peerText, time.Now().UTC())
		if err != nil {
			p.status = err.Error()
			procInvalidateRect.Call(uintptr(p.hwnd), 0, 1)
			return
		}
		code = u.Code
		peerText = u.DeviceID
		peerName = u.Name
	}
	if len(code) != 6 {
		p.status = "请输入完整的 6 位数字配对码"
		procInvalidateRect.Call(uintptr(p.hwnd), 0, 1)
		return
	}
	if len(peerText) != 64 {
		p.status = "请填入 64 位设备 ID 或粘贴完整的 ferry:// 配对链接"
		procInvalidateRect.Call(uintptr(p.hwnd), 0, 1)
		return
	}

	p.busy = true
	p.status = "正在与对方验证配对…"
	procInvalidateRect.Call(uintptr(p.hwnd), 0, 1)

	p.cancelWait()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	p.mu.Lock()
	p.gen++
	gen := p.gen
	p.cancelFn = cancel
	p.mu.Unlock()

	app := p.app
	go func() {
		defer cancel()
		key, err := identity.LoadOrCreateKey(app.keyPath)
		if err != nil {
			p.reportAsync(gen, false, err.Error())
			return
		}
		paired, err := identity.LoadPaired(app.pairedPath)
		if err != nil {
			p.reportAsync(gen, false, err.Error())
			return
		}
		cl, release, err := app.acquireSignalForPair(ctx, key)
		if err != nil {
			p.reportAsync(gen, false, "信令连接失败："+err.Error())
			return
		}
		defer release()
		peer, err := pairing.RunWithName(ctx, cl, key, paired, pairing.Initiate, code, peerText, app.deviceName(), 60*time.Second)
		if err != nil {
			p.reportAsync(gen, false, err.Error())
			return
		}
		if peerName != "" {
			if d, ok := paired.Get(peer.ID); ok && d.Name == "" {
				d.Name = pairing.SanitizeName(peerName)
				if d.Name != "" {
					_ = paired.Add(d)
				}
			}
		}
		if serr := paired.Save(); serr != nil {
			p.reportAsync(gen, false, serr.Error())
			return
		}
		display := peer.Name
		if display == "" {
			display = truncID(peer.ID)
		}
		p.reportAsync(gen, true, display)
	}()
}

func (p *pairWin) reportAsync(gen uint64, ok bool, msg string) {
	p.mu.Lock()
	if p.gen != gen {
		p.mu.Unlock()
		return
	}
	p.lastDone = ok
	p.lastErr = msg
	p.mu.Unlock()
	procPostMessageW.Call(uintptr(p.hwnd), wmPairEvent, 0, 0)
}

func (p *pairWin) onAsyncEvent() {
	p.mu.Lock()
	ok := p.lastDone
	msg := p.lastErr
	p.mu.Unlock()

	p.busy = false
	if !ok {
		p.status = msg
		procInvalidateRect.Call(uintptr(p.hwnd), 0, 1)
		return
	}

	if thePanel != nil {
		thePanel.reloadPeers()
	}
	if mh := existingMainWindow(); mh != 0 {
		if mw := currentWin(mh); mw != nil {
			mw.loadPeers()
			mw.status = "配对成功，已添加设备 " + msg
		}
		procPostMessageW.Call(uintptr(mh), wmAppPaint, 0, 0)
	}
	if trayHandle != nil {
		trayHandle.balloon("配对成功", "已添加信任设备 "+msg)
	}
	procDestroyWindow.Call(uintptr(p.hwnd))
}

func (p *pairWin) layout(cw, ch int32) {
	p.closeRect, p.qrRect, p.regenRect, p.copyRect, p.refreshRect, p.submitRect, p.switchRect = rectT{}, rectT{}, rectT{}, rectT{}, rectT{}, rectT{}, rectT{}
	p.closeRect = rectT{cw - p.px(40), 0, cw, p.px(34)}
	pad := p.px(28)
	p.switchRect = rectT{pad, ch - p.px(44), cw - pad, ch - p.px(12)}

	if p.manualMode {
		p.layoutManual()
		p.submitRect = rectT{pad, p.px(270), cw - pad, p.px(308)}
		return
	}

	qrSz := p.px(184)
	qx := (cw - qrSz) / 2
	qy := p.px(98)
	p.qrRect = rectT{qx, qy, qx + qrSz, qy + qrSz}
	p.regenRect = rectT{qx + p.px(36), qy + qrSz/2 + p.px(8), qx + qrSz - p.px(36), qy + qrSz/2 + p.px(40)}

	codeY := qy + qrSz + p.px(18)
	p.copyRect = rectT{cw/2 + p.px(52), codeY + p.px(2), cw/2 + p.px(100), codeY + p.px(28)}
	cdY := codeY + p.px(36)
	p.refreshRect = rectT{cw/2 + p.px(56), cdY, cw/2 + p.px(104), cdY + p.px(22)}
}

func (p *pairWin) ncHitTest(hwnd syscall.Handle, lParam uintptr) uintptr {
	pt := pointT{int32(int16(lParam & 0xffff)), int32(int16((lParam >> 16) & 0xffff))}
	procScreenToClient.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&pt)))
	var cr rectT
	procGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&cr)))
	p.layout(cr.right, cr.bottom)
	if pt.y < p.px(36) && !ptIn(p.closeRect, pt.x, pt.y) {
		return htCaption
	}
	return htClient
}

func (p *pairWin) paint(hwnd syscall.Handle) {
	var ps paintStruct
	hdc, _, _ := procBeginPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))
	defer procEndPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))

	var cr rectT
	procGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&cr)))
	if cr.right <= 0 || cr.bottom <= 0 {
		return
	}
	p.layout(cr.right, cr.bottom)

	cv := newGlassCanvas(hdc, cr.right, cr.bottom)
	if cv == nil {
		fillRect(hdc, cr, p.pal.paper)
		return
	}
	if p.acrylic {
		a := byte(212)
		if p.dark {
			a = 222
		}
		cv.fillColorAlpha(cr, p.pal.paper, a)
	} else {
		cv.fillColorAlpha(cr, p.pal.paper, 255)
	}

	// Seamless top-right close button matching borderless Acrylic frame.
	closeInk := p.pal.ink2
	if p.hoverZone == 7 {
		cv.fillColorAlpha(p.closeRect, p.pal.danger, 230)
		closeInk = col(0xFF, 0xFF, 0xFF)
	}
	drawCloseGlyph(cv, p.closeRect, closeInk)

	pad := p.px(28)
	if p.manualMode {
		cv.drawAlphaText(p.fontTitle, "输入配对码", rectT{pad, p.px(32), cr.right - pad, p.px(60)},
			dtCenter|dtSingleLine|dtVCenter, p.pal.ink)
		cv.drawAlphaText(p.fontSub, "输入另一台设备上的 6 位配对码（10 分钟内有效）",
			rectT{pad, p.px(62), cr.right - pad, p.px(86)}, dtCenter|dtSingleLine|dtVCenter, p.pal.ink2)

		cv.drawAlphaText(p.fontSm, "6 位配对码", rectT{pad, p.px(112), cr.right - pad, p.px(132)},
			dtSingleLine|dtVCenter, p.pal.ink2)
		cv.drawAlphaText(p.fontSm, "对方设备 ID（或粘贴 ferry:// 配对链接）",
			rectT{pad, p.px(190), cr.right - pad, p.px(210)}, dtSingleLine|dtVCenter, p.pal.ink2)

		fill := p.pal.accent
		if p.hoverZone == 5 && !p.busy {
			fill = shade(fill, p.dark, 0.12)
		}
		cv.drawAlphaRoundRect(p.submitRect, float64(p.px(8)), fill, 255, fill, 255, 0)
		btnLabel := "配对"
		if p.busy {
			btnLabel = "配对中…"
		}
		cv.drawAlphaText(p.fontBody, btnLabel, p.submitRect, dtCenter|dtSingleLine|dtVCenter, p.pal.accentInk)

		if p.status != "" {
			cv.drawAlphaText(p.fontSm, p.status,
				rectT{pad, p.submitRect.bottom + p.px(14), cr.right - pad, p.switchRect.top - p.px(8)},
				dtCenter|dtWordBreak, p.pal.ink2)
		}

		cv.fillColorAlpha(rectT{pad, p.switchRect.top - p.px(4), cr.right - pad, p.switchRect.top - p.px(3)}, p.pal.line, 200)
		cv.drawAlphaText(p.fontSm, "返回二维码", p.switchRect, dtCenter|dtSingleLine|dtVCenter, p.pal.accent)
		cv.finishBlit(hdc)
		return
	}

	// QR mode
	cv.drawAlphaText(p.fontTitle, "添加设备", rectT{pad, p.px(32), cr.right - pad, p.px(60)},
		dtCenter|dtSingleLine|dtVCenter, p.pal.ink)
	cv.drawAlphaText(p.fontSub, "用手机轻渡扫描下方二维码",
		rectT{pad, p.px(62), cr.right - pad, p.px(84)}, dtCenter|dtSingleLine|dtVCenter, p.pal.ink2)

	// White QR card (always high-contrast white behind QR modules for camera recognition)
	cv.drawAlphaRoundRect(p.qrRect, float64(p.px(12)), col(0xFF, 0xFF, 0xFF), 255, p.pal.line, 255, 1.0)
	if len(p.qrBits) > 0 {
		n := len(p.qrBits)
		inset := p.px(14)
		avail := (p.qrRect.right - p.qrRect.left) - inset*2
		mod := avail / int32(n)
		if mod < 1 {
			mod = 1
		}
		drawSz := mod * int32(n)
		ox := p.qrRect.left + ((p.qrRect.right-p.qrRect.left)-drawSz)/2
		oy := p.qrRect.top + ((p.qrRect.bottom-p.qrRect.top)-drawSz)/2
		darkCol := col(0x11, 0x18, 0x27)
		for r := 0; r < n; r++ {
			for c := 0; c < n; c++ {
				if p.qrBits[r][c] {
					mr := rectT{ox + int32(c)*mod, oy + int32(r)*mod, ox + int32(c+1)*mod, oy + int32(r+1)*mod}
					fillRect(cv.memDC, mr, darkCol)
				}
			}
		}
		cv.makeOpaqueRect(rectT{ox, oy, ox + drawSz, oy + drawSz})
	}

	if p.expired {
		cv.drawAlphaRoundRect(p.qrRect, float64(p.px(12)), col(0xFF, 0xFF, 0xFF), 240, p.pal.line, 255, 1.0)
		cv.drawAlphaText(p.fontBody, "已过期（超过 10 分钟）",
			rectT{p.qrRect.left + p.px(8), p.qrRect.top + p.px(56), p.qrRect.right - p.px(8), p.qrRect.top + p.px(84)},
			dtCenter|dtSingleLine|dtVCenter, p.pal.danger)
		cv.drawAlphaRoundRect(p.regenRect, float64(p.px(8)), p.pal.accent, 255, p.pal.accent, 255, 0)
		cv.drawAlphaText(p.fontSm, "重新生成", p.regenRect, dtCenter|dtSingleLine|dtVCenter, p.pal.accentInk)
	}

	// 6-digit code + copy button
	codeStr := p.code
	if len(codeStr) == 6 {
		codeStr = codeStr[:3] + " " + codeStr[3:]
	}
	codeY := p.qrRect.bottom + p.px(18)
	cv.drawAlphaText(p.fontCode, codeStr, rectT{pad, codeY, p.copyRect.left - p.px(10), codeY + p.px(30)},
		dtRight|dtSingleLine|dtVCenter, p.pal.ink)

	copyFill := p.pal.paper
	if p.hoverZone == 3 {
		copyFill = p.pal.paper3
	}
	cv.drawAlphaRoundRect(p.copyRect, float64(p.px(6)), copyFill, 240, p.pal.line, 255, 1.0)
	cv.drawAlphaText(p.fontSm, "复制", p.copyRect, dtCenter|dtSingleLine|dtVCenter, p.pal.ink)

	// Countdown + refresh
	cdY := codeY + p.px(36)
	cdText := "已过期"
	if !p.expired {
		rem := time.Until(p.expiresAt)
		if rem < 0 {
			rem = 0
		}
		sec := int(rem.Seconds())
		cdText = fmt.Sprintf("10 分钟内有效 · 剩余 %02d:%02d", sec/60, sec%60)
	}
	cv.drawAlphaText(p.fontSm, cdText, rectT{pad, cdY, p.refreshRect.left - p.px(6), cdY + p.px(22)},
		dtRight|dtSingleLine|dtVCenter, p.pal.ink2)
	cv.drawAlphaText(p.fontSm, "刷新", p.refreshRect, dtCenter|dtSingleLine|dtVCenter, p.pal.accent)

	if p.status != "" {
		cv.drawAlphaText(p.fontSm, p.status,
			rectT{pad, cdY + p.px(28), cr.right - pad, p.switchRect.top - p.px(10)},
			dtCenter|dtWordBreak|dtEndEllipsis, p.pal.ink3)
	}

	cv.fillColorAlpha(rectT{pad, p.switchRect.top - p.px(4), cr.right - pad, p.switchRect.top - p.px(3)}, p.pal.line, 200)
	cv.drawAlphaText(p.fontSm, "改为输入对方配对码", p.switchRect, dtCenter|dtSingleLine|dtVCenter, p.pal.accent)
	cv.finishBlit(hdc)
}

func (p *pairWin) onMouseMove(hwnd syscall.Handle, x, y int32) {
	z := 0
	switch {
	case ptIn(p.closeRect, x, y):
		z = 7
	case !p.manualMode && p.expired && ptIn(p.regenRect, x, y):
		z = 1
	case !p.manualMode && ptIn(p.copyRect, x, y):
		z = 3
	case !p.manualMode && ptIn(p.refreshRect, x, y):
		z = 4
	case p.manualMode && ptIn(p.submitRect, x, y):
		z = 5
	case ptIn(p.switchRect, x, y):
		z = 6
	}
	if z != p.hoverZone {
		p.hoverZone = z
		procInvalidateRect.Call(uintptr(hwnd), 0, 0)
	}
	if !p.tracking {
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
		p.tracking = true
	}
}

func (p *pairWin) onClick(hwnd syscall.Handle, x, y int32) {
	switch {
	case ptIn(p.closeRect, x, y):
		procDestroyWindow.Call(uintptr(hwnd))
	case !p.manualMode && p.expired && ptIn(p.regenRect, x, y):
		p.refreshSession()
	case !p.manualMode && ptIn(p.copyRect, x, y):
		if copyToClipboard(hwnd, p.code) {
			p.status = "已复制配对码 " + p.code
			procInvalidateRect.Call(uintptr(hwnd), 0, 0)
		}
	case !p.manualMode && ptIn(p.refreshRect, x, y):
		p.refreshSession()
	case p.manualMode && ptIn(p.submitRect, x, y):
		p.submitManual()
	case ptIn(p.switchRect, x, y):
		p.setManualMode(!p.manualMode)
	}
}

func readEditText(hwnd syscall.Handle) string {
	n, _, _ := procGetWindowTextLengthW.Call(uintptr(hwnd))
	buf := make([]uint16, n+2)
	procGetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf)
}
