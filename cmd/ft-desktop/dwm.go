//go:build windows

package main

import (
	"math"
	"syscall"
	"unsafe"
)

var (
	dwmapi = syscall.NewLazyDLL("dwmapi.dll")

	procDwmSetWindowAttribute      = dwmapi.NewProc("DwmSetWindowAttribute")
	procDwmExtendFrameIntoClient   = dwmapi.NewProc("DwmExtendFrameIntoClientArea")
	procCreateDIBSection           = gdi32.NewProc("CreateDIBSection")
	procGdiFlush                   = gdi32.NewProc("GdiFlush")
	procSetWindowCompositionAttrib = user32.NewProc("SetWindowCompositionAttribute")
)

type dwmMargins struct {
	cxLeft, cxRight, cyTop, cyBottom int32
}

type accentPolicy struct {
	AccentState   uint32
	AccentFlags   uint32
	GradientColor uint32
	AnimationId   uint32
}

type windowCompositionAttribData struct {
	Attrib uint32
	Data   unsafe.Pointer
	SizeOf uintptr
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

// enableAcrylicBackdrop requests Windows 11 Acrylic Gaussian blur and native
// rounded corners via DWM. Returns true only when DWM accepts DWMSBT_TRANSIENTWINDOW
// (Win11 22H2+); older Windows returns false so callers fall back to opaque fills.
func enableAcrylicBackdrop(hwnd syscall.Handle, dark bool) bool {
	if hwnd == 0 {
		return false
	}
	const (
		dwmwaUseImmersiveDarkMode   = 20
		dwmwaWindowCornerPreference = 33
		dwmwaSystemBackdropType     = 38

		dwmwcpRound          = 2
		dwmsbtTransientGlass = 3 // DWMSBT_TRANSIENTWINDOW (Acrylic)
	)

	var darkVal int32
	if dark {
		darkVal = 1
	}
	procDwmSetWindowAttribute.Call(uintptr(hwnd), dwmwaUseImmersiveDarkMode, uintptr(unsafe.Pointer(&darkVal)), 4)

	corner := int32(dwmwcpRound)
	procDwmSetWindowAttribute.Call(uintptr(hwnd), dwmwaWindowCornerPreference, uintptr(unsafe.Pointer(&corner)), 4)

	backdrop := int32(dwmsbtTransientGlass)
	hr, _, _ := procDwmSetWindowAttribute.Call(uintptr(hwnd), dwmwaSystemBackdropType, uintptr(unsafe.Pointer(&backdrop)), 4)
	if hr != 0 {
		return false
	}

	m := dwmMargins{-1, -1, -1, -1}
	hr2, _, _ := procDwmExtendFrameIntoClient.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&m)))
	if hr2 != 0 {
		zero := dwmMargins{}
		procDwmExtendFrameIntoClient.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&zero)))
		return false
	}
	return true
}

// enableUnfocusedAcrylic enables Acrylic blur that remains frosted even on
// WS_EX_NOACTIVATE popup windows that never take focus (unlike Win11
// SystemBackdropType=3, which turns opaque gray when unfocused).
func enableUnfocusedAcrylic(hwnd syscall.Handle, dark bool) bool {
	if hwnd == 0 {
		return false
	}
	const (
		dwmwaUseImmersiveDarkMode   = 20
		dwmwaWindowCornerPreference = 33
		dwmwcpRound                 = 2
		wcaAccentPolicy             = 19
		accentEnableAcrylic         = 4
		accentFlagBlend             = 2
	)
	var darkVal int32
	if dark {
		darkVal = 1
	}
	procDwmSetWindowAttribute.Call(uintptr(hwnd), dwmwaUseImmersiveDarkMode, uintptr(unsafe.Pointer(&darkVal)), 4)
	corner := int32(dwmwcpRound)
	procDwmSetWindowAttribute.Call(uintptr(hwnd), dwmwaWindowCornerPreference, uintptr(unsafe.Pointer(&corner)), 4)

	m := dwmMargins{-1, -1, -1, -1}
	procDwmExtendFrameIntoClient.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&m)))

	grad := uint32(0x9CF7F5F5) // Light frost (#F5F5F7, 0xAABBGGRR)
	if dark {
		grad = 0xB41C1715 // Dark frost (#15171C, 0xAABBGGRR)
	}
	pol := accentPolicy{
		AccentState:   accentEnableAcrylic,
		AccentFlags:   accentFlagBlend,
		GradientColor: grad,
	}
	data := windowCompositionAttribData{
		Attrib: wcaAccentPolicy,
		Data:   unsafe.Pointer(&pol),
		SizeOf: unsafe.Sizeof(pol),
	}
	if err := procSetWindowCompositionAttrib.Find(); err == nil {
		r, _, _ := procSetWindowCompositionAttrib.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&data)))
		if r != 0 {
			return true
		}
	}
	return enableAcrylicBackdrop(hwnd, dark)
}

// glassCanvas backs GDI + premultiplied-alpha compositing in a 32-bit BGRA DIB
// so Win11 Acrylic glass regions stay translucent (A < 255) while opaque surfaces
// and ClearType glyphs render sharply at A = 255 without disappearing into glass.
type glassCanvas struct {
	memDC  uintptr
	bmp    uintptr
	oldBmp uintptr
	w, h   int32
	pix    []byte
}

func newGlassCanvas(refDC uintptr, w, h int32) *glassCanvas {
	if w <= 0 || h <= 0 {
		return nil
	}
	memDC, _, _ := procCompatDC.Call(refDC)
	if memDC == 0 {
		return nil
	}
	var bmi bitmapInfoHeader
	bmi.biSize = uint32(unsafe.Sizeof(bmi))
	bmi.biWidth = w
	bmi.biHeight = -h // top-down
	bmi.biPlanes = 1
	bmi.biBitCount = 32
	var bits uintptr
	bmp, _, _ := procCreateDIBSection.Call(memDC, uintptr(unsafe.Pointer(&bmi)), 0, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if bmp == 0 || bits == 0 {
		procDeleteDC.Call(memDC)
		return nil
	}
	oldBmp, _, _ := procSelectObject.Call(memDC, bmp)
	pix := unsafe.Slice((*byte)(ptrAt(bits)), int(w)*int(h)*4)
	clear(pix)
	return &glassCanvas{
		memDC:  memDC,
		bmp:    bmp,
		oldBmp: oldBmp,
		w:      w,
		h:      h,
		pix:    pix,
	}
}

func (g *glassCanvas) close() {
	if g == nil {
		return
	}
	if g.oldBmp != 0 {
		procSelectObject.Call(g.memDC, g.oldBmp)
	}
	if g.bmp != 0 {
		procDeleteObject.Call(g.bmp)
	}
	if g.memDC != 0 {
		procDeleteDC.Call(g.memDC)
	}
}

func (g *glassCanvas) finishBlit(dstDC uintptr) {
	if g == nil {
		return
	}
	procGdiFlush.Call()
	procBitBlt.Call(dstDC, 0, 0, uintptr(g.w), uintptr(g.h), g.memDC, 0, 0, srcCopy)
	g.close()
}

func (g *glassCanvas) clampRect(r rectT) (int32, int32, int32, int32, bool) {
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

// fillColorAlpha fills r uniformly with premultiplied (color, alpha).
func (g *glassCanvas) fillColorAlpha(r rectT, color uint32, alpha byte) {
	x0, y0, x1, y1, ok := g.clampRect(r)
	if !ok {
		return
	}
	procGdiFlush.Call()
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

// makeOpaqueRect stamps A = 255 across r after opaque GDI drawing.
func (g *glassCanvas) makeOpaqueRect(r rectT) {
	x0, y0, x1, y1, ok := g.clampRect(r)
	if !ok {
		return
	}
	procGdiFlush.Call()
	stride := int(g.w) * 4
	for y := y0; y < y1; y++ {
		off := int(y)*stride + int(x0)*4 + 3
		for x := x0; x < x1; x++ {
			g.pix[off] = 255
			off += 4
		}
	}
}

func (g *glassCanvas) blendPremulPixel(px, py int32, fb, fg, fr, fa byte) {
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

// drawAlphaRoundRect draws a smooth SDF anti-aliased rounded box with optional
// border width and translucency over any glass or solid surface.
func (g *glassCanvas) drawAlphaRoundRect(r rectT, rad float64, fill uint32, fillA byte, edge uint32, edgeA byte, border float64) {
	x0, y0, x1, y1, ok := g.clampRect(r)
	if !ok {
		return
	}
	procGdiFlush.Call()
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
			// Signed distance to outer boundary (negative inside)
			sd := math.Hypot(dx, dy) - rad
			if sd >= 0.7 {
				continue
			}
			outerCov := 1.0
			if sd > -0.7 {
				outerCov = (0.7 - sd) / 1.4
			}
			if border > 0 && edgeA > 0 && sd > -border-0.7 {
				// Blend border and fill cleanly across inner stroke edge
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

// drawAlphaText renders crisp GDI text into a black scratch DIB and composites
// glyph coverage onto the glass canvas via Porter-Duff SRC_OVER.
func (g *glassCanvas) drawAlphaText(font syscall.Handle, s string, r rectT, flags uint32, color uint32) {
	if s == "" || r.right <= r.left || r.bottom <= r.top {
		return
	}
	x0, y0, x1, y1, ok := g.clampRect(r)
	if !ok {
		return
	}
	rw := r.right - r.left
	rh := r.bottom - r.top
	scratch := newGlassCanvas(g.memDC, rw, rh)
	if scratch == nil {
		drawText(g.memDC, font, s, r, flags, color)
		return
	}
	defer scratch.close()

	procSetBkMode.Call(scratch.memDC, bkTransparent)
	drawText(scratch.memDC, font, s, rectT{0, 0, rw, rh}, flags, col(255, 255, 255))
	procGdiFlush.Call()

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

// drawAlphaGraphic runs a white-on-black GDI drawing callback onto a scratch DIB
// and composites its anti-aliased luminance in the supplied color onto glass.
func (g *glassCanvas) drawAlphaGraphic(r rectT, color uint32, fn func(dc uintptr, w, h int32)) {
	x0, y0, x1, y1, ok := g.clampRect(r)
	if !ok {
		return
	}
	rw := r.right - r.left
	rh := r.bottom - r.top
	scratch := newGlassCanvas(g.memDC, rw, rh)
	if scratch == nil {
		return
	}
	defer scratch.close()

	fn(scratch.memDC, rw, rh)
	procGdiFlush.Call()

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

func drawCloseGlyph(cv *glassCanvas, r rectT, color uint32) {
	drawCloseGlyphSDF(cv, r, color)
}
