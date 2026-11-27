//go:build windows

package main

import (
	"math"
	"strings"
	"syscall"
	"unsafe"
)

var (
	wizUser32   = syscall.NewLazyDLL("user32.dll")
	wizGdi32    = syscall.NewLazyDLL("gdi32.dll")
	wizKernel32 = syscall.NewLazyDLL("kernel32.dll")
	wizShell32  = syscall.NewLazyDLL("shell32.dll")
	wizOle32    = syscall.NewLazyDLL("ole32.dll")
	wizDwmapi   = syscall.NewLazyDLL("dwmapi.dll")

	procWizRegisterClassExW           = wizUser32.NewProc("RegisterClassExW")
	procWizCreateWindowExW            = wizUser32.NewProc("CreateWindowExW")
	procWizDefWindowProcW             = wizUser32.NewProc("DefWindowProcW")
	procWizDestroyWindow              = wizUser32.NewProc("DestroyWindow")
	procWizGetMessageW                = wizUser32.NewProc("GetMessageW")
	procWizTranslateMessage           = wizUser32.NewProc("TranslateMessage")
	procWizDispatchMessageW           = wizUser32.NewProc("DispatchMessageW")
	procWizPostMessageW               = wizUser32.NewProc("PostMessageW")
	procWizPostQuitMessage            = wizUser32.NewProc("PostQuitMessage")
	procWizSendMessageW               = wizUser32.NewProc("SendMessageW")
	procWizGetWindowTextLengthW       = wizUser32.NewProc("GetWindowTextLengthW")
	procWizGetWindowTextW             = wizUser32.NewProc("GetWindowTextW")
	procWizSetWindowTextW             = wizUser32.NewProc("SetWindowTextW")
	procWizEnableWindow               = wizUser32.NewProc("EnableWindow")
	procWizShowWindow                 = wizUser32.NewProc("ShowWindow")
	procWizUpdateWindow               = wizUser32.NewProc("UpdateWindow")
	procWizInvalidateRect             = wizUser32.NewProc("InvalidateRect")
	procWizBeginPaint                 = wizUser32.NewProc("BeginPaint")
	procWizEndPaint                   = wizUser32.NewProc("EndPaint")
	procWizGetClientRect              = wizUser32.NewProc("GetClientRect")
	procWizLoadCursorW                = wizUser32.NewProc("LoadCursorW")
	procWizLoadIconW                  = wizUser32.NewProc("LoadIconW")
	procWizLoadImageW                 = wizUser32.NewProc("LoadImageW")
	procWizSystemParametersInfoW      = wizUser32.NewProc("SystemParametersInfoW")
	procWizSetDpiAwareCtx             = wizUser32.NewProc("SetProcessDpiAwarenessContext")
	procWizSetDPIAware                = wizUser32.NewProc("SetProcessDPIAware")
	procWizGetDpiForSystem            = wizUser32.NewProc("GetDpiForSystem")
	procWizGetDpiForWindow            = wizUser32.NewProc("GetDpiForWindow")
	procWizTrackMouseEvent            = wizUser32.NewProc("TrackMouseEvent")
	procWizSetCursor                  = wizUser32.NewProc("SetCursor")
	procWizScreenToClient             = wizUser32.NewProc("ScreenToClient")
	procWizSetWindowPos               = wizUser32.NewProc("SetWindowPos")
	procWizSetTimer                   = wizUser32.NewProc("SetTimer")
	procWizKillTimer                  = wizUser32.NewProc("KillTimer")
	procWizDrawTextW                  = wizUser32.NewProc("DrawTextW")
	procWizSetLayeredWindowAttributes = wizUser32.NewProc("SetLayeredWindowAttributes")

	procWizGetModuleHandleW = wizKernel32.NewProc("GetModuleHandleW")

	procWizCreateFontW              = wizGdi32.NewProc("CreateFontW")
	procWizSelectObject             = wizGdi32.NewProc("SelectObject")
	procWizDeleteObject             = wizGdi32.NewProc("DeleteObject")
	procWizCreateSolidBrush         = wizGdi32.NewProc("CreateSolidBrush")
	procWizCreatePen                = wizGdi32.NewProc("CreatePen")
	procWizSetBkMode                = wizGdi32.NewProc("SetBkMode")
	procWizSetBkColor               = wizGdi32.NewProc("SetBkColor")
	procWizSetTextColor             = wizGdi32.NewProc("SetTextColor")
	procWizCreateDIBSection         = wizGdi32.NewProc("CreateDIBSection")
	procWizGdiFlush                 = wizGdi32.NewProc("GdiFlush")
	procWizCreateCompatDC           = wizGdi32.NewProc("CreateCompatibleDC")
	procWizDeleteDC                 = wizGdi32.NewProc("DeleteDC")
	procWizBitBlt                   = wizGdi32.NewProc("BitBlt")
	procWizMoveToEx                 = wizGdi32.NewProc("MoveToEx")
	procWizLineTo                   = wizGdi32.NewProc("LineTo")
	procWizTextFaceW                = wizGdi32.NewProc("GetTextFaceW")
	procWizDwmSetWindowAttribute    = wizDwmapi.NewProc("DwmSetWindowAttribute")
	procWizDwmExtendFrameIntoClient = wizDwmapi.NewProc("DwmExtendFrameIntoClientArea")
	procWizSHBrowseForFolderW       = wizShell32.NewProc("SHBrowseForFolderW")
	procWizSHGetPathFromIDListW     = wizShell32.NewProc("SHGetPathFromIDListW")
	procWizCoInitializeEx           = wizOle32.NewProc("CoInitializeEx")
	procWizCoUninitialize           = wizOle32.NewProc("CoUninitialize")
	procWizCoTaskMemFree            = wizOle32.NewProc("CoTaskMemFree")
	procWizRegGetValueW             = syscall.NewLazyDLL("advapi32.dll").NewProc("RegGetValueW")
)

type setupPalette struct {
	paper, paper2, paper3         uint32
	ink, ink2, ink3               uint32
	line, lineSoft                uint32
	accent, accentInk, accentSoft uint32
	danger                        uint32
}

func wizCol(r, g, b uint32) uint32 { return b<<16 | g<<8 | r }

var wizPalLight = setupPalette{
	paper:      wizCol(0xFF, 0xFF, 0xFF),
	paper2:     wizCol(0xF5, 0xF5, 0xF7),
	paper3:     wizCol(0xEA, 0xEA, 0xEE),
	ink:        wizCol(0x1D, 0x1D, 0x1F),
	ink2:       wizCol(0x51, 0x51, 0x54),
	ink3:       wizCol(0x86, 0x86, 0x8B),
	line:       wizCol(0xD8, 0xD8, 0xDE),
	lineSoft:   wizCol(0xE8, 0xE8, 0xED),
	accent:     wizCol(0x00, 0x66, 0xCC),
	accentInk:  wizCol(0xFF, 0xFF, 0xFF),
	accentSoft: wizCol(0xE8, 0xF2, 0xFC),
	danger:     wizCol(0xD9, 0x2D, 0x20),
}

var wizPalDark = setupPalette{
	paper:      wizCol(0x0C, 0x0D, 0x10),
	paper2:     wizCol(0x15, 0x17, 0x1C),
	paper3:     wizCol(0x1F, 0x22, 0x29),
	ink:        wizCol(0xEF, 0xF1, 0xF5),
	ink2:       wizCol(0xA1, 0xA8, 0xB5),
	ink3:       wizCol(0x6E, 0x76, 0x85),
	line:       wizCol(0x2A, 0x2E, 0x38),
	lineSoft:   wizCol(0x20, 0x24, 0x2D),
	accent:     wizCol(0x16, 0x4E, 0xA6),
	accentInk:  wizCol(0xF2, 0xF5, 0xFA),
	accentSoft: wizCol(0x0E, 0x1C, 0x31),
	danger:     wizCol(0xFF, 0x5A, 0x52),
}

func setupTheme() (setupPalette, bool) {
	dark := setupAppsUseDarkTheme()
	if dark {
		return wizPalDark, true
	}
	return wizPalLight, false
}

func setupAppsUseDarkTheme() bool {
	hkey := ^uintptr(0x7ffffffe) // HKEY_CURRENT_USER
	key := wizUTF16Ptr(`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`)
	name := wizUTF16Ptr("AppsUseLightTheme")
	var v, cb uint32 = 1, 4
	r, _, _ := procWizRegGetValueW.Call(hkey, uintptr(unsafe.Pointer(key)), uintptr(unsafe.Pointer(name)),
		0x00000010, 0, uintptr(unsafe.Pointer(&v)), uintptr(unsafe.Pointer(&cb)))
	if r != 0 {
		return false
	}
	return v == 0
}

func enableSetupAcrylic(hwnd syscall.Handle, dark bool) bool {
	if hwnd == 0 {
		return false
	}
	var darkVal int32
	if dark {
		darkVal = 1
	}
	procWizDwmSetWindowAttribute.Call(uintptr(hwnd), 20, uintptr(unsafe.Pointer(&darkVal)), 4)
	corner := int32(2) // DWMWCP_ROUND
	procWizDwmSetWindowAttribute.Call(uintptr(hwnd), 33, uintptr(unsafe.Pointer(&corner)), 4)

	backdrop := int32(3) // DWMSBT_TRANSIENTWINDOW
	hr, _, _ := procWizDwmSetWindowAttribute.Call(uintptr(hwnd), 38, uintptr(unsafe.Pointer(&backdrop)), 4)
	if hr != 0 {
		return false
	}
	type margins struct{ l, r, t, b int32 }
	m := margins{-1, -1, -1, -1}
	hr2, _, _ := procWizDwmExtendFrameIntoClient.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&m)))
	if hr2 != 0 {
		zero := margins{}
		procWizDwmExtendFrameIntoClient.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&zero)))
		return false
	}
	return true
}

func createSetupFont(dpi, px, weight int32) syscall.Handle {
	hPx := wizScale(dpi, px)
	face := "Microsoft YaHei UI"
	useWeight := weight
	for _, c := range []string{"MiSans", "Microsoft YaHei UI", "Segoe UI"} {
		if setupFaceExists(c) {
			face = c
			break
		}
	}
	if weight >= 600 && face == "MiSans" && setupFaceExists("MiSans Semibold") {
		face = "MiSans Semibold"
		useWeight = 400
	}
	h, _, _ := procWizCreateFontW.Call(
		uintptr(-hPx), 0, 0, 0, uintptr(useWeight),
		0, 0, 0, 1, 0, 0, 5, 0,
		uintptr(unsafe.Pointer(wizUTF16Ptr(face))),
	)
	return syscall.Handle(h)
}

func setupFaceExists(name string) bool {
	h, _, _ := procWizCreateFontW.Call(uintptr(0xFFFFFFF4), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 5, 0,
		uintptr(unsafe.Pointer(wizUTF16Ptr(name))))
	if h == 0 {
		return false
	}
	defer procWizDeleteObject.Call(h)
	dc, _, _ := procWizCreateCompatDC.Call(0)
	if dc == 0 {
		return false
	}
	defer procWizDeleteDC.Call(dc)
	old, _, _ := procWizSelectObject.Call(dc, h)
	var buf [64]uint16
	n, _, _ := procWizTextFaceW.Call(dc, uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])))
	procWizSelectObject.Call(dc, old)
	if n == 0 {
		return false
	}
	got := strings.ToLower(strings.TrimSpace(syscall.UTF16ToString(buf[:n])))
	want := strings.ToLower(name)
	return got == want || strings.Contains(got, want) || strings.Contains(want, got)
}

type setupGlassCanvas struct {
	memDC  uintptr
	bmp    uintptr
	oldBmp uintptr
	w, h   int32
	pix    []byte
}

func newSetupGlassCanvas(refDC uintptr, w, h int32) *setupGlassCanvas {
	if w <= 0 || h <= 0 {
		return nil
	}
	memDC, _, _ := procWizCreateCompatDC.Call(refDC)
	if memDC == 0 {
		return nil
	}
	type bitmapInfoHeader struct {
		biSize          uint32
		biWidth         int32
		biHeight        int32
		biPlanes        uint16
		biBitCount      uint16
		biCompression   uint32
		biSizeImage     uint32
		biXPelsPerMeter int32
		biYPelsPerMeter int32
		biClrUsed       uint32
		biClrImportant  uint32
	}
	var bmi bitmapInfoHeader
	bmi.biSize = uint32(unsafe.Sizeof(bmi))
	bmi.biWidth = w
	bmi.biHeight = -h
	bmi.biPlanes = 1
	bmi.biBitCount = 32
	var bits uintptr
	bmp, _, _ := procWizCreateDIBSection.Call(memDC, uintptr(unsafe.Pointer(&bmi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 || bits == 0 {
		procWizDeleteDC.Call(memDC)
		return nil
	}
	oldBmp, _, _ := procWizSelectObject.Call(memDC, bmp)
	pix := unsafe.Slice((*byte)(wizPtrAt(bits)), int(w)*int(h)*4)
	clear(pix)
	return &setupGlassCanvas{memDC: memDC, bmp: bmp, oldBmp: oldBmp, w: w, h: h, pix: pix}
}

func (g *setupGlassCanvas) close() {
	if g == nil {
		return
	}
	if g.oldBmp != 0 {
		procWizSelectObject.Call(g.memDC, g.oldBmp)
	}
	if g.bmp != 0 {
		procWizDeleteObject.Call(g.bmp)
	}
	if g.memDC != 0 {
		procWizDeleteDC.Call(g.memDC)
	}
}

func (g *setupGlassCanvas) finishBlit(dstDC uintptr) {
	if g == nil {
		return
	}
	procWizGdiFlush.Call()
	procWizBitBlt.Call(dstDC, 0, 0, uintptr(g.w), uintptr(g.h), g.memDC, 0, 0, 0x00CC0020)
	g.close()
}

func (g *setupGlassCanvas) clampRect(r wizRect) (int32, int32, int32, int32, bool) {
	x0, y0, x1, y1 := r.left, r.top, r.right, r.bottom
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > g.w {
		x1 = g.w
	}
	if y1 > g.h {
		y1 = g.h
	}
	return x0, y0, x1, y1, x1 > x0 && y1 > y0
}

func (g *setupGlassCanvas) fillColorAlpha(r wizRect, color uint32, alpha byte) {
	x0, y0, x1, y1, ok := g.clampRect(r)
	if !ok {
		return
	}
	procWizGdiFlush.Call()
	cr := byte(color & 0xff)
	cg := byte((color >> 8) & 0xff)
	cb := byte((color >> 16) & 0xff)
	pb := byte((uint32(cb)*uint32(alpha) + 127) / 255)
	pg := byte((uint32(cg)*uint32(alpha) + 127) / 255)
	pr := byte((uint32(cr)*uint32(alpha) + 127) / 255)
	stride := int(g.w) * 4
	for y := y0; y < y1; y++ {
		off := int(y)*stride + int(x0)*4
		for x := x0; x < x1; x++ {
			g.pix[off] = pb
			g.pix[off+1] = pg
			g.pix[off+2] = pr
			g.pix[off+3] = alpha
			off += 4
		}
	}
}

func (g *setupGlassCanvas) blendPremulPixel(px, py int32, fb, fg, fr, fa byte) {
	if fa == 0 || px < 0 || py < 0 || px >= g.w || py >= g.h {
		return
	}
	off := (int(py)*int(g.w) + int(px)) * 4
	if fa == 255 {
		g.pix[off] = fb
		g.pix[off+1] = fg
		g.pix[off+2] = fr
		g.pix[off+3] = 255
		return
	}
	a := uint32(fa)
	inv := 255 - a
	pB := (uint32(fb)*a + 127) / 255
	pG := (uint32(fg)*a + 127) / 255
	pR := (uint32(fr)*a + 127) / 255
	g.pix[off] = byte(pB + (uint32(g.pix[off])*inv+127)/255)
	g.pix[off+1] = byte(pG + (uint32(g.pix[off+1])*inv+127)/255)
	g.pix[off+2] = byte(pR + (uint32(g.pix[off+2])*inv+127)/255)
	g.pix[off+3] = byte(a + (uint32(g.pix[off+3])*inv+127)/255)
}

func (g *setupGlassCanvas) drawAlphaRoundRect(r wizRect, rad float64, fill uint32, fillA byte, edge uint32, edgeA byte, border float64) {
	x0, y0, x1, y1, ok := g.clampRect(r)
	if !ok {
		return
	}
	procWizGdiFlush.Call()
	rw := float64(r.right - r.left)
	rh := float64(r.bottom - r.top)
	if rad > rw/2 {
		rad = rw / 2
	}
	if rad > rh/2 {
		rad = rh / 2
	}
	cx := float64(r.left) + rw/2
	cy := float64(r.top) + rh/2
	hx := rw/2 - rad
	hy := rh/2 - rad

	fb, fg, fr := byte((fill>>16)&0xff), byte((fill>>8)&0xff), byte(fill&0xff)
	eb, eg, er := byte((edge>>16)&0xff), byte((edge>>8)&0xff), byte(edge&0xff)

	for y := y0; y < y1; y++ {
		dy := math.Abs(float64(y)+0.5-cy) - hy
		if dy < 0 {
			dy = 0
		}
		for x := x0; x < x1; x++ {
			dx := math.Abs(float64(x)+0.5-cx) - hx
			if dx < 0 {
				dx = 0
			}
			sd := math.Hypot(dx, dy) - rad
			if sd >= 0.7 {
				continue
			}
			outerCov := 1.0
			if sd > -0.7 {
				outerCov = (0.7 - sd) / 1.4
			}
			if border > 0 && edgeA > 0 && sd > -border-0.7 {
				innerCov := 0.0
				isd := sd + border
				if isd < 0.7 {
					if isd <= -0.7 {
						innerCov = 1.0
					} else {
						innerCov = (0.7 - isd) / 1.4
					}
				}
				borderCov := outerCov - innerCov
				if innerCov > 0 && fillA > 0 {
					fa := byte(float64(fillA)*innerCov + 0.5)
					g.blendPremulPixel(x, y, fb, fg, fr, fa)
				}
				if borderCov > 0 {
					ea := byte(float64(edgeA)*borderCov + 0.5)
					g.blendPremulPixel(x, y, eb, eg, er, ea)
				}
			} else if fillA > 0 {
				fa := byte(float64(fillA)*outerCov + 0.5)
				g.blendPremulPixel(x, y, fb, fg, fr, fa)
			}
		}
	}
}

func (g *setupGlassCanvas) drawAlphaText(font syscall.Handle, s string, r wizRect, flags uint32, color uint32) {
	if s == "" || r.right <= r.left || r.bottom <= r.top {
		return
	}
	x0, y0, x1, y1, ok := g.clampRect(r)
	if !ok {
		return
	}
	rw := r.right - r.left
	rh := r.bottom - r.top
	scratch := newSetupGlassCanvas(g.memDC, rw, rh)
	if scratch == nil {
		return
	}
	defer scratch.close()

	procWizSetBkMode.Call(scratch.memDC, 1) // TRANSPARENT
	if font != 0 {
		procWizSelectObject.Call(scratch.memDC, uintptr(font))
	}
	procWizSetTextColor.Call(scratch.memDC, uintptr(wizCol(255, 255, 255)))
	u := syscall.StringToUTF16(s)
	rr := wizRect{0, 0, rw, rh}
	procWizDrawTextW.Call(scratch.memDC, uintptr(unsafe.Pointer(&u[0])), uintptr(len(u)-1), uintptr(unsafe.Pointer(&rr)), uintptr(flags|0x0800))
	procWizGdiFlush.Call()

	fb, fg, fr := byte((color>>16)&0xff), byte((color>>8)&0xff), byte(color&0xff)
	for py := y0; py < y1; py++ {
		sy := int(py - r.top)
		sOff := sy*int(rw)*4 + int(x0-r.left)*4
		for px := x0; px < x1; px++ {
			b := uint32(scratch.pix[sOff])
			gr := uint32(scratch.pix[sOff+1])
			re := uint32(scratch.pix[sOff+2])
			sOff += 4
			cov := (re*77 + gr*150 + b*29) >> 8
			if cov > 0 {
				g.blendPremulPixel(px, py, fb, fg, fr, byte(cov))
			}
		}
	}
}

func (g *setupGlassCanvas) drawAlphaGraphic(r wizRect, color uint32, fn func(dc uintptr, w, h int32)) {
	x0, y0, x1, y1, ok := g.clampRect(r)
	if !ok {
		return
	}
	rw := r.right - r.left
	rh := r.bottom - r.top
	scratch := newSetupGlassCanvas(g.memDC, rw, rh)
	if scratch == nil {
		return
	}
	defer scratch.close()

	fn(scratch.memDC, rw, rh)
	procWizGdiFlush.Call()

	fb, fg, fr := byte((color>>16)&0xff), byte((color>>8)&0xff), byte(color&0xff)
	for py := y0; py < y1; py++ {
		sy := int(py - r.top)
		sOff := sy*int(rw)*4 + int(x0-r.left)*4
		for px := x0; px < x1; px++ {
			b := uint32(scratch.pix[sOff])
			gr := uint32(scratch.pix[sOff+1])
			re := uint32(scratch.pix[sOff+2])
			sOff += 4
			cov := (re*77 + gr*150 + b*29) >> 8
			if cov > 0 {
				g.blendPremulPixel(px, py, fb, fg, fr, byte(cov))
			}
		}
	}
}

func drawSetupBrandMark(cv *setupGlassCanvas, r wizRect, rad float64, accent uint32) {
	cv.drawAlphaRoundRect(r, rad, accent, 255, 0, 0, 0)
	cv.drawAlphaGraphic(r, wizCol(255, 255, 255), func(dc uintptr, rw, rh int32) {
		pw := rw / 12
		if pw < 1 {
			pw = 1
		}
		pen, _, _ := procWizCreatePen.Call(0, uintptr(pw), uintptr(wizCol(255, 255, 255)))
		oldPen, _, _ := procWizSelectObject.Call(dc, pen)
		defer func() {
			procWizSelectObject.Call(dc, oldPen)
			procWizDeleteObject.Call(pen)
		}()
		procWizMoveToEx.Call(dc, uintptr(rw*22/100), uintptr(rh*50/100), 0)
		procWizLineTo.Call(dc, uintptr(rw*78/100), uintptr(rh*38/100))
		procWizLineTo.Call(dc, uintptr(rw*66/100), uintptr(rh*64/100))
		procWizLineTo.Call(dc, uintptr(rw*30/100), uintptr(rh*64/100))
		procWizLineTo.Call(dc, uintptr(rw*22/100), uintptr(rh*50/100))
		procWizMoveToEx.Call(dc, uintptr(rw*24/100), uintptr(rh*76/100), 0)
		procWizLineTo.Call(dc, uintptr(rw*76/100), uintptr(rh*76/100))
	})
}

func drawSetupCloseGlyph(cv *setupGlassCanvas, r wizRect, color uint32) {
	cv.drawAlphaGraphic(r, color, func(dc uintptr, rw, rh int32) {
		pen, _, _ := procWizCreatePen.Call(0, 1, uintptr(wizCol(255, 255, 255)))
		oldPen, _, _ := procWizSelectObject.Call(dc, pen)
		defer func() {
			procWizSelectObject.Call(dc, oldPen)
			procWizDeleteObject.Call(pen)
		}()
		sz := rw
		if rh < sz {
			sz = rh
		}
		sz = sz * 28 / 100
		if sz < 8 {
			sz = 8
		}
		x0 := (rw - sz) / 2
		y0 := (rh - sz) / 2
		procWizMoveToEx.Call(dc, uintptr(x0), uintptr(y0), 0)
		procWizLineTo.Call(dc, uintptr(x0+sz+1), uintptr(y0+sz+1))
		procWizMoveToEx.Call(dc, uintptr(x0+sz), uintptr(y0), 0)
		procWizLineTo.Call(dc, uintptr(x0-1), uintptr(y0+sz+1))
	})
}

func drawSetupMinGlyph(cv *setupGlassCanvas, r wizRect, color uint32, sz int32) {
	cv.drawAlphaGraphic(r, color, func(dc uintptr, rw, rh int32) {
		pen, _, _ := procWizCreatePen.Call(0, 1, uintptr(wizCol(255, 255, 255)))
		oldPen, _, _ := procWizSelectObject.Call(dc, pen)
		defer func() {
			procWizSelectObject.Call(dc, oldPen)
			procWizDeleteObject.Call(pen)
		}()
		x0 := (rw - sz) / 2
		y0 := rh / 2
		procWizMoveToEx.Call(dc, uintptr(x0), uintptr(y0), 0)
		procWizLineTo.Call(dc, uintptr(x0+sz+1), uintptr(y0))
	})
}

func drawSetupCheckmark(cv *setupGlassCanvas, r wizRect, color uint32, penW int32) {
	if penW < 1 {
		penW = 2
	}
	cv.drawAlphaGraphic(r, color, func(dc uintptr, rw, rh int32) {
		pen, _, _ := procWizCreatePen.Call(0, uintptr(penW), uintptr(wizCol(255, 255, 255)))
		oldPen, _, _ := procWizSelectObject.Call(dc, pen)
		defer func() {
			procWizSelectObject.Call(dc, oldPen)
			procWizDeleteObject.Call(pen)
		}()
		procWizMoveToEx.Call(dc, uintptr(rw*24/100), uintptr(rh*52/100), 0)
		procWizLineTo.Call(dc, uintptr(rw*43/100), uintptr(rh*71/100))
		procWizLineTo.Call(dc, uintptr(rw*77/100), uintptr(rh*31/100))
	})
}

func wizShade(c uint32, dark bool, amount float64) uint32 {
	target := uint32(0)
	if dark {
		target = 0x00FFFFFF
	}
	mix := func(x, y uint32) uint32 {
		v := float64(x) + (float64(y)-float64(x))*amount
		if v < 0 {
			v = 0
		} else if v > 255 {
			v = 255
		}
		return uint32(v + 0.5)
	}
	return mix(c&0xff, target&0xff) |
		mix((c>>8)&0xff, (target>>8)&0xff)<<8 |
		mix((c>>16)&0xff, (target>>16)&0xff)<<16
}
