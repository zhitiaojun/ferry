//go:build windows

package main

import (
	"fmt"
	"math"
	"sync"
	"syscall"
	"unsafe"

	"github.com/cwlindean/filetransfer/core/client"
)

// The drop panel is the "only while dragging" surface: it appears in the
// bottom-right corner when Explorer begins dragging files (SysDragImage), expands
// on pointer hover to pick a target device, and sends on release without ever
// taking focus (WS_EX_NOACTIVATE).

var (
	procSetTimer         = user32.NewProc("SetTimer")
	procKillTimer        = user32.NewProc("KillTimer")
	procScreenToClient   = user32.NewProc("ScreenToClient")
	procRoundRect        = gdi32.NewProc("RoundRect")
	procCreatePen        = gdi32.NewProc("CreatePen")
	procRectangle        = gdi32.NewProc("Rectangle")
	procCreateRoundRgn   = gdi32.NewProc("CreateRoundRectRgn")
	procSetWindowRgn     = user32.NewProc("SetWindowRgn")
	procSetWinEventHook  = user32.NewProc("SetWinEventHook")
	procUnhookWinEvent   = user32.NewProc("UnhookWinEvent")
	procGetClassNameW    = user32.NewProc("GetClassNameW")
	procIsWindow         = user32.NewProc("IsWindow")
	procGetAsyncKeyState = user32.NewProc("GetAsyncKeyState")
	procDragQueryPoint   = shell32.NewProc("DragQueryPoint")
)

const (
	wmMouseMove = 0x0200
	wmTimer     = 0x0113
	wmPanelShow = wmApp + 20
	wmPanelHide = wmApp + 21

	wsExNoActivate = 0x08000000
	swpNoActivate  = 0x0010
	swpShowWindow  = 0x0040
	hwndTopmost    = ^uintptr(0)

	// Design pixels at 96 dpi, scaled dynamically by monitor DPI.
	compactW  = 192
	compactH  = 54
	panelW    = 304
	panelH    = 192
	anchorGap = 20

	panelTimerID = 7
)

type dropPanel struct {
	hwnd     syscall.Handle
	app      *app
	peers    []client.Peer
	active   int
	shown    bool
	expanded bool
	acrylic  bool
	outside  int
	dragGone int
	buttonUp int
	pw, ph   int32
	dpi      int32
	timer    uintptr
	fontLg   syscall.Handle
	fontSm   syscall.Handle
	fontNm   syscall.Handle
}

var thePanel *dropPanel
var panelWndProcPtr uintptr
var winEventPtr uintptr

func newDropPanel(a *app) *dropPanel {
	p := &dropPanel{app: a, active: 0}
	thePanel = p
	return p
}

func (p *dropPanel) px(v int32) int32 {
	dpi := p.dpi
	if dpi == 0 {
		dpi = 96
	}
	return int32(math.Round(float64(v) * float64(dpi) / 96.0))
}

// ensure creates the panel window on first use and enables Win11 Acrylic glass.
func (p *dropPanel) ensure() error {
	if p.hwnd != 0 {
		return nil
	}
	hinst := getModuleHandle()
	if panelWndProcPtr == 0 {
		panelWndProcPtr = syscall.NewCallback(panelWndProc)
	}
	cls := "ftDropPanel"
	var wc wndClassExW
	wc.cbSize = uint32(unsafe.Sizeof(wc))
	wc.style = 0x00020000 // CS_DROPSHADOW
	wc.lpfnWndProc = panelWndProcPtr
	wc.hInstance = hinst
	cur, _, _ := procLoadCursorW.Call(0, uintptr(idcArrow))
	wc.hCursor = syscall.Handle(cur)
	name := utf16Ptr(cls)
	wc.lpszClassName = name
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		if err != nil && err.Error() != "The operation completed successfully." {
			return fmt.Errorf("注册面板窗口类失败: %v", err)
		}
	}
	cw, ch := p.size()
	h, _, err := procCreateWindowExW.Call(
		uintptr(wsExTopmost|wsExToolWindow|wsExNoActivate),
		uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(utf16Ptr("轻渡 投放"))),
		uintptr(wsPopup),
		0, 0, uintptr(cw), uintptr(ch), 0, 0, uintptr(hinst), 0)
	if h == 0 {
		return fmt.Errorf("创建面板窗口失败: %v", err)
	}
	p.hwnd = syscall.Handle(h)
	_, dark := theme()
	p.acrylic = enableUnfocusedAcrylic(p.hwnd, dark)
	p.applyDPI()
	procDragAcceptFiles.Call(uintptr(p.hwnd), 1)
	return nil
}

func (p *dropPanel) applyDPI() {
	dpi, _, _ := procGetDpiForWindow.Call(uintptr(p.hwnd))
	if dpi == 0 {
		dpi, _, _ = procGetDpiForSystem.Call()
	}
	if dpi == 0 {
		dpi = 96
	}
	if int32(dpi) == p.dpi && p.pw != 0 {
		return
	}
	p.dpi = int32(dpi)
	p.pw, p.ph = p.size()
	p.makeFonts()
	p.applyRegion()
}

// applyRegion clips corners on systems without native DWM rounded corners.
func (p *dropPanel) applyRegion() {
	if p.acrylic {
		return
	}
	rad := uintptr(p.px(12))
	r, _, _ := procCreateRoundRgn.Call(0, 0, uintptr(p.pw+1), uintptr(p.ph+1), rad, rad)
	procSetWindowRgn.Call(uintptr(p.hwnd), r, 1)
}

func (p *dropPanel) freeFonts() {
	for _, f := range []syscall.Handle{p.fontLg, p.fontNm, p.fontSm} {
		if f != 0 {
			procDeleteObject.Call(uintptr(f))
		}
	}
	p.fontLg, p.fontNm, p.fontSm = 0, 0, 0
}

func (p *dropPanel) makeFonts() {
	p.freeFonts()
	p.fontLg = createUIFont(p.px(14), 600)
	p.fontNm = createUIFont(p.px(13), 600)
	p.fontSm = createUIFont(p.px(12), 400)
}

func (p *dropPanel) reloadPeers() {
	peers, err := client.Peers(p.app.pairedPath)
	if err != nil {
		peers = nil
	}
	if len(peers) > 3 {
		peers = peers[:3]
	}
	p.peers = peers
	if p.active >= len(peers) || p.active < 0 {
		p.active = 0
	}
}

// show anchors the panel at the bottom-right work-area corner.
func (p *dropPanel) show() {
	if p.shown {
		return
	}
	if err := p.ensure(); err != nil {
		return
	}
	_, dark := theme()
	p.acrylic = enableUnfocusedAcrylic(p.hwnd, dark)
	p.applyDPI()
	p.reloadPeers()
	p.expanded = false
	p.outside = 0
	p.dragGone = 0
	p.buttonUp = 0
	w, h := p.size()
	p.pw, p.ph = w, h
	p.applyRegion()
	x, y := p.place(w, h)
	procSetWindowPos.Call(uintptr(p.hwnd), hwndTopmost, uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		swpNoActivate|swpShowWindow)
	p.shown = true
	p.app.event("desktop.panel_show", map[string]any{"peers": len(p.peers), "x": x, "y": y})
	p.timer, _, _ = procSetTimer.Call(uintptr(p.hwnd), panelTimerID, 60, 0)
	procInvalidateRect.Call(uintptr(p.hwnd), 0, 1)
}

func (p *dropPanel) hide() {
	if p.hwnd == 0 || !p.shown {
		return
	}
	if p.timer != 0 {
		procKillTimer.Call(uintptr(p.hwnd), panelTimerID)
		p.timer = 0
	}
	procShowWindow.Call(uintptr(p.hwnd), swHide)
	p.shown = false
	p.expanded = false
}

func (p *dropPanel) size() (int32, int32) {
	w, h := int32(panelW), int32(panelH)
	if !p.expanded {
		w, h = compactW, compactH
	}
	return p.px(w), p.px(h)
}

func (p *dropPanel) place(w, h int32) (int32, int32) {
	var wa rectT
	procSystemParametersInfoW.Call(uintptr(spiGetWorkArea), 0, uintptr(unsafe.Pointer(&wa)), 0)
	gap := p.px(anchorGap)
	return wa.right - w - gap, wa.bottom - h - gap
}

func (p *dropPanel) setExpanded(v bool) {
	if p.expanded == v {
		return
	}
	p.expanded = v
	p.outside = 0
	w, h := p.size()
	p.pw, p.ph = w, h
	p.applyRegion()
	x, y := p.place(w, h)
	procSetWindowPos.Call(uintptr(p.hwnd), hwndTopmost, uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		swpNoActivate|swpShowWindow)
	procInvalidateRect.Call(uintptr(p.hwnd), 0, 1)
	p.app.event("desktop.panel_shape", map[string]any{"expanded": v})
}

func (p *dropPanel) layout(w, h int32) (title rectT, rows []rectT) {
	pad := p.px(14)
	title = rectT{pad, p.px(12), w - pad, p.px(36)}
	n := len(p.peers)
	if n == 0 {
		return
	}
	top := title.bottom + p.px(6)
	gap := p.px(6)
	rowH := p.px(40)
	avail := h - top - pad
	if n > 0 && int32(n)*rowH+int32(n-1)*gap > avail {
		rowH = (avail - int32(n-1)*gap) / int32(n)
	}
	y := top
	for i := 0; i < n; i++ {
		rows = append(rows, rectT{pad, y, w - pad, y + rowH})
		y += rowH + gap
	}
	return
}

func (p *dropPanel) hit(x, y int32) int {
	_, rows := p.layout(p.pw, p.ph)
	for i, r := range rows {
		if ptIn(r, x, y) {
			return i
		}
	}
	return -1
}

func (p *dropPanel) paint(hwnd syscall.Handle) {
	var ps paintStruct
	hdc, _, _ := procBeginPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))
	defer procEndPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))

	pal, dark := theme()
	cv := newGlassCanvas(hdc, p.pw, p.ph)
	if cv == nil {
		fillRect(hdc, rectT{0, 0, p.pw, p.ph}, pal.paper)
		return
	}

	full := rectT{0, 0, p.pw, p.ph}
	if p.acrylic {
		// Leave glass background transparent (A=0) so ACCENT_ENABLE_ACRYLICBLURBEHIND's
		// live frosted Gaussian blur + GradientColor renders directly, plus a crisp hairline edge.
		cv.drawAlphaRoundRect(full, float64(p.px(12)), 0, 0, pal.line, 190, 1.0)
	} else {
		cv.fillColorAlpha(full, pal.paper, 255)
		cv.drawAlphaRoundRect(full, float64(p.px(12)), 0, 0, pal.line, 255, 1.0)
	}
	_ = dark

	if !p.expanded {
		// Compact floating pill: small paper-boat dot mark + single clear label.
		markSz := p.px(10)
		cy := p.ph / 2
		label := "拖到这里发送"
		tw := textWidth(cv.memDC, p.fontNm, label)
		totalW := markSz + p.px(8) + tw
		startX := (p.pw - totalW) / 2
		if startX < p.px(10) {
			startX = p.px(10)
		}
		markR := rectT{startX, cy - markSz/2, startX + markSz, cy + markSz/2}
		cv.drawAlphaRoundRect(markR, float64(markSz)/2, pal.accent, 255, 0, 0, 0)
		textR := rectT{markR.right + p.px(8), 0, p.pw - p.px(10), p.ph}
		cv.drawAlphaText(p.fontNm, label, textR, dtSingleLine|dtVCenter, pal.ink)
		cv.finishBlit(hdc)
		return
	}

	title, rows := p.layout(p.pw, p.ph)
	cv.drawAlphaText(p.fontLg, "发给", title, dtSingleLine|dtVCenter, pal.ink2)

	if len(p.peers) == 0 {
		box := rectT{p.px(14), title.bottom + p.px(12), p.pw - p.px(14), p.ph - p.px(18)}
		boxFill := pal.paper
		boxA := byte(175)
		if !p.acrylic {
			boxA = 255
		}
		cv.drawAlphaRoundRect(box, float64(p.px(10)), boxFill, boxA, pal.line, 220, 1.0)
		cv.drawAlphaText(p.fontSm, "还没有已配对设备\n松手后打开配对窗口",
			rectT{box.left + p.px(10), box.top + p.px(14), box.right - p.px(10), box.bottom - p.px(10)},
			dtCenter|dtWordBreak, pal.ink2)
		cv.finishBlit(hdc)
		return
	}

	for i, r := range rows {
		active := i == p.active
		fill := pal.paper
		fillA := byte(170)
		edge := pal.line
		borderW := 1.0
		if active {
			fill = pal.accentSoft
			fillA = 225
			edge = pal.accent
			borderW = float64(p.px(2))
			if borderW < 1.5 {
				borderW = 1.5
			}
		}
		if !p.acrylic {
			fillA = 255
		}
		cv.drawAlphaRoundRect(r, float64(p.px(10)), fill, fillA, edge, 255, borderW)

		name := peerLabel(p.peers[i])
		rightPad := p.px(12)
		if active {
			hintW := textWidth(cv.memDC, p.fontSm, "松手发送") + p.px(16)
			hintR := rectT{r.right - hintW, r.top, r.right - p.px(12), r.bottom}
			cv.drawAlphaText(p.fontSm, "松手发送", hintR, dtSingleLine|dtVCenter|dtRight, pal.accent)
			rightPad = hintW + p.px(4)
		}
		nmR := rectT{r.left + p.px(12), r.top, r.right - rightPad, r.bottom}
		cv.drawAlphaText(p.fontNm, name, nmR, dtSingleLine|dtVCenter|dtEndEllipsis, pal.ink)
	}

	cv.finishBlit(hdc)
}

// onDrop handles WM_DROPFILES while the panel is visible.
func (p *dropPanel) onDrop(hdrop syscall.Handle) {
	files := dragQueryFiles(hdrop)
	var pt pointT
	ok, _, _ := procDragQueryPoint.Call(uintptr(hdrop), uintptr(unsafe.Pointer(&pt)))
	procDragFinish.Call(uintptr(hdrop))
	idx := p.active
	if !p.expanded {
		idx = 0
		ok = 0
	}
	if ok != 0 {
		if h := p.hit(pt.x, pt.y); h >= 0 {
			idx = h
		}
	}
	p.hide()
	if len(files) == 0 {
		return
	}
	if len(p.peers) == 0 {
		openMainWindow(p.app)
		openPairWindow(existingMainWindow(), p.app)
		if trayHandle != nil {
			trayHandle.balloon("尚未配对设备", "请先扫码或输入 6 位码配对后再拖放发送")
		}
		return
	}
	if idx < 0 || idx >= len(p.peers) {
		idx = 0
	}
	peer := p.peers[idx]
	go func() {
		res, err := sendToPeer(p.app, files, peer)
		if err != nil {
			p.app.event("desktop.panel_send_error", map[string]any{"error": err.Error(), "peer": peer.Name})
			if trayHandle != nil {
				trayHandle.balloon("发送失败", err.Error())
			}
			return
		}
		p.app.event("desktop.panel_sent", map[string]any{
			"peer": peer.Name, "files": len(res.Files), "bytes": res.Bytes,
		})
		if trayHandle != nil {
			trayHandle.balloon("发送完成", fmt.Sprintf("已发送给 %s（%s）", peerLabel(peer), summarizeFiles(res.Files)))
		}
		if h := existingMainWindow(); h != 0 {
			procPostMessageW.Call(uintptr(h), wmAppPaint, 0, 0)
		}
	}()
}

func panelWndProc(hwnd syscall.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	p := thePanel
	if p == nil {
		r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
		return r
	}
	switch msg {
	case wmCreate:
		p.hwnd = hwnd
		return 0
	case wmEraseBkgnd:
		return 1
	case wmPaint:
		p.paint(hwnd)
		return 0
	case wmTimer:
		if !p.shown {
			return 0
		}
		ksEsc, _, _ := procGetAsyncKeyState.Call(0x1B) // VK_ESCAPE
		if int32(ksEsc)&0x8000 != 0 {
			p.hide()
			return 0
		}
		ksL, _, _ := procGetAsyncKeyState.Call(1) // VK_LBUTTON
		ksR, _, _ := procGetAsyncKeyState.Call(2) // VK_RBUTTON
		btnDown := int32(ksL)&0x8000 != 0 || int32(ksR)&0x8000 != 0
		if !btnDown {
			p.buttonUp++
			if p.buttonUp*60 >= 180 {
				p.buttonUp = 0
				p.hide()
				return 0
			}
		} else {
			p.buttonUp = 0
		}

		var cursor pointT
		procGetCursorPos.Call(uintptr(unsafe.Pointer(&cursor)))
		var wr rectT
		procGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&wr)))
		inside := cursor.x >= wr.left && cursor.x < wr.right && cursor.y >= wr.top && cursor.y < wr.bottom
		pad := p.px(16)
		insideLoose := cursor.x >= wr.left-pad && cursor.x < wr.right+pad && cursor.y >= wr.top-pad && cursor.y < wr.bottom+pad

		// Shell hides SysDragImage while crossing non-drop windows (such as desktop widgets)
		// or WM_DROPFILES targets without destroying it; keep tracking until IsWindow == 0.
		dragMu.Lock()
		for h := range dragWins {
			if r, _, _ := procIsWindow.Call(h); r == 0 {
				delete(dragWins, h)
				continue
			}
			var buf [64]uint16
			n, _, _ := procGetClassNameW.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
			if n == 0 || syscall.UTF16ToString(buf[:n]) != "SysDragImage" {
				delete(dragWins, h)
			}
		}
		live := len(dragWins)
		dragMu.Unlock()
		if live == 0 && !(btnDown && insideLoose) {
			p.dragGone++
			if p.dragGone*60 >= 300 {
				p.dragGone = 0
				p.hide()
				return 0
			}
		} else {
			p.dragGone = 0
		}
		if !p.expanded {
			if inside {
				p.setExpanded(true)
			}
			return 0
		}
		if !insideLoose {
			p.outside++
			if p.outside*60 >= 360 {
				p.setExpanded(false)
			}
			return 0
		}
		p.outside = 0
		pt := cursor
		procScreenToClient.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&pt)))
		if h := p.hit(pt.x, pt.y); h >= 0 && h != p.active {
			p.active = h
			p.app.event("desktop.panel_hover", map[string]any{"index": h})
			procInvalidateRect.Call(uintptr(hwnd), 0, 0)
		}
		return 0
	case wmDropFiles:
		if p.shown {
			p.onDrop(syscall.Handle(wParam))
			return 0
		}
	case wmPanelShow:
		p.show()
		return 0
	case wmPanelHide:
		p.hide()
		return 0
	case wmDestroy:
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return r
}

func winEventProc(hook uintptr, event uint32, hwnd uintptr, idObject, idChild int32, idThread, ms uint32) uintptr {
	const (
		eventObjectCreate  = 0x8000
		eventObjectDestroy = 0x8001
		eventObjectShow    = 0x8002
	)
	if hwnd == 0 || idObject != 0 {
		return 0
	}
	var buf [64]uint16
	n, _, _ := procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return 0
	}
	cls := syscall.UTF16ToString(buf[:n])
	if cls != "SysDragImage" {
		return 0
	}
	if thePanel == nil || thePanel.hwnd == 0 {
		return 0
	}
	dragMu.Lock()
	switch event {
	case eventObjectCreate, eventObjectShow:
		dragWins[hwnd] = true
	case eventObjectDestroy:
		delete(dragWins, hwnd)
	}
	alive := len(dragWins)
	dragMu.Unlock()
	if (event == eventObjectCreate || event == eventObjectShow) && alive > 0 {
		procPostMessageW.Call(uintptr(thePanel.hwnd), wmPanelShow, 0, 0)
	}
	return 0
}

var (
	dragMu   sync.Mutex
	dragWins = map[uintptr]bool{}
)

func startDragWatch() uintptr {
	const (
		eventObjectCreate = 0x8000
		eventObjectShow   = 0x8002
		winEventOutOfCtx  = 0x0000
		winEventSkipOwn   = 0x0002
	)
	if winEventPtr == 0 {
		winEventPtr = syscall.NewCallback(winEventProc)
	}
	h, _, _ := procSetWinEventHook.Call(eventObjectCreate, eventObjectShow, 0, winEventPtr, 0, 0,
		winEventOutOfCtx|winEventSkipOwn)
	return h
}

const wmCreate = 0x0001

func dragQueryFiles(hdrop syscall.Handle) []string {
	count := int(dragQueryFile(hdrop, 0xFFFFFFFF, nil, 0))
	if count <= 0 {
		return nil
	}
	out := make([]string, 0, count)
	for i := 0; i < count; i++ {
		need := int(dragQueryFile(hdrop, uint32(i), nil, 0))
		if need <= 0 {
			continue
		}
		buf := make([]uint16, need+2)
		dragQueryFile(hdrop, uint32(i), &buf[0], uint32(len(buf)))
		out = append(out, syscall.UTF16ToString(buf))
	}
	return out
}
