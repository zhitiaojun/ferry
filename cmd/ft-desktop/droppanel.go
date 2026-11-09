//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/cwlindean/filetransfer/core/client"
)

// The drop panel is the "only while dragging" surface: it appears when the
// shell starts dragging files (a SysDragImage window is created), shows one box
// per paired device with the first one enlarged, lets the pointer highlight a
// box, and sends to that box when the files are released on it. It never takes
// focus (WS_EX_NOACTIVATE) so the drag itself is not disturbed.

var (
	procSetTimer        = user32.NewProc("SetTimer")
	procKillTimer       = user32.NewProc("KillTimer")
	procScreenToClient  = user32.NewProc("ScreenToClient")
	procRoundRect       = gdi32.NewProc("RoundRect")
	procCreatePen       = gdi32.NewProc("CreatePen")
	procRectangle       = gdi32.NewProc("Rectangle")
	procCreateRoundRgn  = gdi32.NewProc("CreateRoundRectRgn")
	procSetWindowRgn    = user32.NewProc("SetWindowRgn")
	procSetWinEventHook = user32.NewProc("SetWinEventHook")
	procUnhookWinEvent  = user32.NewProc("UnhookWinEvent")
	procGetClassNameW   = user32.NewProc("GetClassNameW")
	procDragQueryPoint  = shell32.NewProc("DragQueryPoint")
)

const (
	wmMouseMove = 0x0200
	wmTimer     = 0x0113
	wmPanelShow = wmApp + 20
	wmPanelHide = wmApp + 21

	wsExNoActivate  = 0x08000000
	swShowNoActivate = 4
	swpNoActivate   = 0x0010
	swpShowWindow   = 0x0040
	hwndTopmost     = ^uintptr(0)

	// Design pixels at 96 dpi; scaled by the monitor DPI at creation time.
	// The panel has two shapes: a small "drop here" window that appears when a
	// drag starts, and the three-device picker it grows into once the pointer
	// reaches it. Both are pinned to the bottom-right corner of the work area.
	panelW   = 300
	panelH   = 180
	compactW = 196
	compactH = 60
	anchorGap = 24

	panelTimerID = 7

	// COLORREF is 0x00BBGGRR.
	colSurface    = 0x00FFFFFF
	colLine       = 0x00D6DDD5
	colAccent     = 0x005B6B09
	colAccentSoft = 0x00ECF2E4
	colInk        = 0x001D2117
	colMuted      = 0x0061675B
)

type dropPanel struct {
	hwnd   syscall.Handle
	app    *app
	peers  []client.Peer
	active int
	hover  int
	shown    bool
	expanded bool
	outside  int
	pw, ph   int32
	dpi    int32
	timer  uintptr
	fontLg syscall.Handle
	fontSm syscall.Handle
	fontNm syscall.Handle
}

var thePanel *dropPanel
var panelWndProcPtr uintptr
var winEventPtr uintptr

func newDropPanel(a *app) *dropPanel {
	p := &dropPanel{app: a, active: 0, hover: -1}
	thePanel = p
	return p
}

// ensure creates the panel window on first use.
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
		// Class may already be registered by a previous window; only fail on
		// other errors.
		if err != nil && err.Error() != "The operation completed successfully." {
			return fmt.Errorf("注册面板窗口类失败: %v", err)
		}
	}
	cw, ch := p.size()
	h, _, err := procCreateWindowExW.Call(
		uintptr(wsExTopmost|wsExToolWindow|wsExNoActivate),
		uintptr(unsafe.Pointer(name)), uintptr(unsafe.Pointer(utf16Ptr("ft 投放"))),
		uintptr(wsPopup),
		0, 0, uintptr(cw), uintptr(ch), 0, 0, uintptr(hinst), 0)
	if h == 0 {
		return fmt.Errorf("创建面板窗口失败: %v", err)
	}
	p.hwnd = syscall.Handle(h)
	p.applyDPI()
	procDragAcceptFiles.Call(uintptr(p.hwnd), 1)
	return nil
}

// applyDPI sizes the panel for the monitor it is on. The window's DPI is only
// reliable once it has been shown at least once, so this runs again on show.
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

// applyRegion clips the window to a rounded rectangle matching its current
// size. Both shapes are resized at runtime, so this must follow every resize
// or the expanded panel gets cut off at the compact window's outline.
func (p *dropPanel) applyRegion() {
	rad := uintptr(float64(14) * float64(p.dpi) / 96.0)
	r, _, _ := procCreateRoundRgn.Call(0, 0, uintptr(p.pw+1), uintptr(p.ph+1), rad, rad)
	procSetWindowRgn.Call(uintptr(p.hwnd), r, 1)
}

func (p *dropPanel) makeFonts() {
	mk := func(size int32, weight int32, face string) syscall.Handle {
		h, _, _ := procCreateFontW.Call(uintptr(size), 0, 0, 0, uintptr(weight), 0, 0, 0,
			1, 0, 0, 5, 0, uintptr(unsafe.Pointer(utf16Ptr(face))))
		return syscall.Handle(h)
	}
	sc := func(v int32) int32 { return int32(float64(v) * float64(p.dpi) / 96.0) }
	p.fontLg = mk(-sc(16), 700, "Microsoft YaHei UI")
	p.fontNm = mk(-sc(13), 600, "Microsoft YaHei UI")
	p.fontSm = mk(-sc(11), 400, "Consolas")
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
	if p.active >= len(peers) {
		p.active = 0
	}
}

// show places the panel next to the cursor, clamped to the work area.
func (p *dropPanel) show() {
	if err := p.ensure(); err != nil {
		return
	}
	p.applyDPI()
	p.reloadPeers()
	if len(p.peers) == 0 {
		p.app.event("desktop.panel_skip", map[string]any{"reason": "no peers"})
		return
	}
	p.expanded = false
	p.outside = 0
	w, h := p.size()
	p.pw, p.ph = w, h
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
}

// size returns the panel size in physical pixels for the current shape.
func (p *dropPanel) size() (int32, int32) {
	w, h := int32(panelW), int32(panelH)
	if !p.expanded {
		w, h = compactW, compactH
	}
	dpi := p.dpi
	if dpi == 0 {
		dpi = 96
	}
	return int32(int64(w) * int64(dpi) / 96), int32(int64(h) * int64(dpi) / 96)
}

// place returns the top-left corner that pins the window to the bottom-right
// of the work area, keeping that corner fixed when the shape changes.
func (p *dropPanel) place(w, h int32) (int32, int32) {
	var wa rectT
	procSystemParametersInfoW.Call(uintptr(spiGetWorkArea), 0, uintptr(unsafe.Pointer(&wa)), 0)
	gap := int32(int64(anchorGap) * int64(p.dpi) / 96)
	return wa.right - w - gap, wa.bottom - h - gap
}

// setExpanded morphs between the drop hint and the device picker. The
// bottom-right corner stays put, so the window grows up and to the left.
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

// layout splits the client area into the title row, three device boxes and the
// footer. The active box gets 2.35x the width of the others, matching the HTML
// prototype the user approved.
func (p *dropPanel) layout(w, h int32) (title rectT, boxes []rectT, foot rectT) {
	pad := int32(12)
	title = rectT{pad, pad, w - pad, pad + 18}
	foot = rectT{pad, h - pad - 18, w - pad, h - pad}
	top := title.bottom + 12
	bottom := foot.top - 12
	n := len(p.peers)
	if n == 0 {
		return
	}
	gap := int32(8)
	avail := w - 2*pad - gap*int32(n-1)
	weights := make([]float64, n)
	total := 0.0
	for i := range p.peers {
		weights[i] = 1
		if i == p.active {
			weights[i] = 2.0
		}
		total += weights[i]
	}
	x := pad
	for i := 0; i < n; i++ {
		wI := int32(float64(avail) * weights[i] / total)
		if i == n-1 {
			wI = w - pad - x
		}
		boxes = append(boxes, rectT{x, top, x + wI, bottom})
		x += wI + gap
	}
	return
}

func (p *dropPanel) hit(x, y int32) int {
	_, boxes, _ := p.layout(p.pw, p.ph)
	for i, b := range boxes {
		if ptIn(b, x, y) {
			return i
		}
	}
	return -1
}

func (p *dropPanel) paint(hwnd syscall.Handle) {
	var ps paintStruct
	hdc, _, _ := procBeginPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))
	defer procEndPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))
	defer procDeleteObject.Call(hdc)
	d := uintptr(hdc)

	pen, _, _ := procCreatePen.Call(0, 1, uintptr(colLine))
	brush, _, _ := procCreateBrush.Call(uintptr(colSurface))
	oldPen, _, _ := procSelectObject.Call(d, pen)
	oldBrush, _, _ := procSelectObject.Call(d, brush)
	rad := uintptr(float64(14) * float64(p.dpi) / 96.0)
	procRoundRect.Call(d, 0, 0, uintptr(p.pw+1), uintptr(p.ph+1), rad, rad)
	procSelectObject.Call(d, oldPen)
	procSelectObject.Call(d, oldBrush)
	procDeleteObject.Call(pen)
	procDeleteObject.Call(brush)

	procSetBkMode.Call(d, bkTransparent)
	if !p.expanded {
		// Small landing pad: dashed accent frame with one line of text.
		inset := int32(9)
		if sc := int32(float64(9) * float64(p.dpi) / 96.0); sc > inset {
			inset = sc
		}
		dotPen, _, _ := procCreatePen.Call(2, 1, uintptr(colAccent)) // PS_DOT
		old, _, _ := procSelectObject.Call(d, dotPen)
		procRectangle.Call(d, uintptr(inset), uintptr(inset), uintptr(p.pw-inset), uintptr(p.ph-inset))
		procSelectObject.Call(d, old)
		procDeleteObject.Call(dotPen)
		drawText(d, p.fontNm, "拖到此处开始传文件",
			rectT{inset + 6, inset + 4, p.pw - inset - 6, p.ph - inset - 4},
			dtSingleLine|dtVCenter|dtCenter, colMuted)
		return
	}
	title, boxes, foot := p.layout(p.pw, p.ph)
	drawText(d, p.fontLg, "发送到设备", title, dtSingleLine|dtVCenter, colInk)

	for i, b := range boxes {
		active := i == p.active
		fill := uintptr(colSurface)
		edge := uintptr(colLine)
		if active {
			fill = uintptr(colAccentSoft)
			edge = uintptr(colAccent)
		}
		bp, _, _ := procCreateBrush.Call(fill)
		ep, _, _ := procCreatePen.Call(0, 1, edge)
		op, _, _ := procSelectObject.Call(d, ep)
		ob, _, _ := procSelectObject.Call(d, bp)
		procRoundRect.Call(d, uintptr(b.left), uintptr(b.top), uintptr(b.right), uintptr(b.bottom), 12, 12)
		procSelectObject.Call(d, op)
		procSelectObject.Call(d, ob)
		procDeleteObject.Call(bp)
		procDeleteObject.Call(ep)

		name := p.peers[i].Name
		if name == "" {
			name = "未命名设备"
		}
		narrow := b.right-b.left < 150
		if narrow {
			// A narrow box only has room for the name, wrapped over two lines.
			nm := rectT{b.left + 3, b.top + 8, b.right - 3, b.bottom - 8}
			drawText(d, p.fontSm, name, nm, dtSingleLine|dtVCenter|dtCenter|dtEndEllipsis, colInk)
		} else {
			nm := rectT{b.left + 6, b.top + 10, b.right - 6, b.top + 32}
			drawText(d, p.fontNm, name, nm, dtSingleLine|dtVCenter|dtCenter|dtEndEllipsis, colInk)
			id := rectT{b.left + 6, b.top + 34, b.right - 6, b.top + 50}
			drawText(d, p.fontSm, shortID(p.peers[i].ID), id, dtSingleLine|dtCenter, colMuted)
		}
		if active {
			tag := rectT{b.left + 6, b.bottom - 24, b.right - 6, b.bottom - 8}
			drawText(d, p.fontSm, "松手发送", tag, dtSingleLine|dtCenter, colAccent)
		}
	}
	if len(p.peers) == 0 {
		drawText(d, p.fontSm, "还没有配对设备", rectT{12, p.ph / 2, p.pw - 12, p.ph/2 + 20},
			dtSingleLine|dtCenter, colMuted)
	}
	drawText(d, p.fontSm, "拖动时出现，松手后自动隐藏", foot, dtSingleLine|dtVCenter|dtCenter, colMuted)
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// onDrop is called with the WM_DROPFILES package while the panel is visible.
func (p *dropPanel) onDrop(hdrop syscall.Handle) {
	files := dragQueryFiles(hdrop)
	var pt pointT
	ok, _, _ := procDragQueryPoint.Call(uintptr(hdrop), uintptr(unsafe.Pointer(&pt)))
	procDragFinish.Call(uintptr(hdrop))
	idx := p.active
	if !p.expanded {
		// Dropped straight onto the small window: send to the first device.
		idx = 0
		ok = 0
	}
	if ok != 0 {
		if h := p.hit(pt.x, pt.y); h >= 0 {
			idx = h
		}
	}
	p.hide()
	if len(files) == 0 || idx >= len(p.peers) {
		return
	}
	peer := p.peers[idx]
	go func() {
		res, err := sendToPeer(p.app, files, peer)
		if err != nil {
			p.app.event("desktop.panel_send_error", map[string]any{"error": err.Error(), "peer": peer.Name})
			return
		}
		p.app.event("desktop.panel_sent", map[string]any{
			"peer": peer.Name, "files": len(res.Files), "bytes": res.Bytes,
		})
		p.app.note(fmt.Sprintf("发送 → %s · %d 个文件 · %d B", peer.Name, len(res.Files), res.Bytes))
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
	case wmPaint:
		p.paint(hwnd)
		return 0
	case wmTimer:
		if !p.shown {
			return 0
		}
		var cursor pointT
		procGetCursorPos.Call(uintptr(unsafe.Pointer(&cursor)))
		var wr rectT
		procGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&wr)))
		inside := cursor.x >= wr.left && cursor.x < wr.right && cursor.y >= wr.top && cursor.y < wr.bottom
		if !p.expanded {
			// The small "drop here" window turns into the picker as soon as
			// the dragged file reaches it.
			if inside {
				p.setExpanded(true)
			}
			return 0
		}
		if !inside {
			p.outside++
			if p.outside*60 >= 260 {
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

// winEventProc turns the shell's drag image into panel visibility. The shell
// creates a window of class SysDragImage when a drag starts and destroys it on
// drop or cancel, which is the only public signal available for both cases.
func winEventProc(hook uintptr, event uint32, hwnd uintptr, idObject, idChild int32, idThread, ms uint32) uintptr {
	const (
		eventObjectCreate  = 0x8000
		eventObjectDestroy = 0x8001
	)
	if hwnd == 0 || idObject != 0 { // OBJID_WINDOW
		return 0
	}
	var buf [64]uint16
	n, _, _ := procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if n == 0 {
		return 0
	}
	cls := syscall.UTF16ToString(buf[:n])
	if thePanel != nil && thePanel.app != nil {
		thePanel.app.event("desktop.panel_event", map[string]any{"class": cls, "event": int(event)})
	}
	if cls != "SysDragImage" {
		return 0
	}
	if thePanel == nil || thePanel.hwnd == 0 {
		return 0
	}
	switch event {
	case eventObjectCreate:
		procPostMessageW.Call(uintptr(thePanel.hwnd), wmPanelShow, 0, 0)
	case eventObjectDestroy:
		procPostMessageW.Call(uintptr(thePanel.hwnd), wmPanelHide, 0, 0)
	}
	return 0
}

// startDragWatch installs the WinEvent hook on the calling thread, which must
// be the thread that pumps messages (the tray thread). Out-of-context WinEvents
// are delivered through that message loop.
func startDragWatch() uintptr {
	const (
		eventObjectCreate  = 0x8000
		eventObjectDestroy = 0x8001
		winEventOutOfCtx   = 0x0000
		winEventSkipOwn    = 0x0002
	)
	if winEventPtr == 0 {
		winEventPtr = syscall.NewCallback(winEventProc)
	}
	h, _, _ := procSetWinEventHook.Call(eventObjectCreate, eventObjectDestroy, 0, winEventPtr, 0, 0,
		winEventOutOfCtx|winEventSkipOwn)
	return h
}

const wmCreate = 0x0001

func dragQueryFiles(hdrop syscall.Handle) []string {
	count := int(dragQueryFile(hdrop, 0xFFFFFFFF, nil, 0))
	if count <= 0 {
		return nil
	}
	files := make([]string, 0, count)
	for i := 0; i < count; i++ {
		buf := make([]uint16, 32768)
		n := int(dragQueryFile(hdrop, uint32(i), &buf[0], uint32(len(buf))))
		if n > 0 {
			files = append(files, syscall.UTF16ToString(buf[:n]))
		}
	}
	return files
}
