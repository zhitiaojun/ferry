package main

import (
	"math"
	"testing"
)

func newTestCanvas(w, h int32) *glassCanvas {
	return &glassCanvas{
		w:   w,
		h:   h,
		pix: make([]byte, int(w)*int(h)*4),
	}
}

func TestSDFPrimitives(t *testing.T) {
	if got := sdSegment(5, 5, 0, 5, 10, 5); math.Abs(got) > 1e-6 {
		t.Fatalf("sdSegment on line = %v, want 0", got)
	}
	if got := sdCircleRing(12+5, 12, 12, 12, 5, 1); math.Abs(got-(-1)) > 1e-6 {
		t.Fatalf("sdCircleRing centerline = %v, want -1", got)
	}
	if got := sdRoundRectStroke(1, 9, 2, 5, 10, 8, 2, 1); math.Abs(got) > 1e-6 {
		t.Fatalf("sdRoundRectStroke outer border = %v, want 0", got)
	}
	if got := sdRoundRectStroke(2, 9, 2, 5, 10, 8, 2, 1); math.Abs(got-(-1)) > 1e-6 {
		t.Fatalf("sdRoundRectStroke centerline = %v, want -1", got)
	}
}

func TestRenderVectorSDFSquareAspectInNonSquareRect(t *testing.T) {
	cv := newTestCanvas(40, 40)
	// Intentionally pass a non-square rect (16x24); the settings outer ring must
	// render as a true circle centered at (20, 20), not an ellipse.
	r := rectT{left: 12, top: 8, right: 28, bottom: 32}
	renderVectorSDF(cv, r, col(255, 255, 255), func(vx, vy float64) float64 {
		return sdCircleRing(vx, vy, 12, 12, 8, 1.0)
	})

	alphaAt := func(x, y int32) byte {
		return cv.pix[(int(y)*int(cv.w)+int(x))*4+3]
	}

	// Side = 16 -> scale = 16/24 = 2/3, centered at y0 = 12, x0 = 12 -> center (20, 20).
	// Radius 8 * 2/3 = 5.33 px from center along both axes.
	var horizMaxX, vertMaxY int32
	var horizPeak, vertPeak byte
	for d := int32(0); d < 10; d++ {
		if a := alphaAt(20+d, 20); a > horizPeak {
			horizPeak = a
			horizMaxX = d
		}
		if a := alphaAt(20, 20+d); a > vertPeak {
			vertPeak = a
			vertMaxY = d
		}
	}
	if horizPeak == 0 || vertPeak == 0 {
		t.Fatalf("expected non-zero ring pixels, got horiz=%d vert=%d", horizPeak, vertPeak)
	}
	if horizMaxX != vertMaxY {
		t.Fatalf("ring stretched into ellipse: horiz r=%d, vert r=%d", horizMaxX, vertMaxY)
	}
}

func TestDrawAllVectorIconsNonEmpty(t *testing.T) {
	cv := newTestCanvas(64, 64)
	r := rectT{left: 10, top: 10, right: 42, bottom: 42}
	for kind := 0; kind <= 2; kind++ {
		clear(cv.pix)
		drawNavIconSDF(cv, r, kind, col(15, 118, 110))
		if !hasPartialAlpha(cv.pix) {
			t.Fatalf("nav icon %d did not produce anti-aliased coverage", kind)
		}
	}
	clear(cv.pix)
	drawBrandBoatSDF(cv, r, col(255, 255, 255))
	if !hasPartialAlpha(cv.pix) {
		t.Fatal("brand boat icon did not produce anti-aliased coverage")
	}
	clear(cv.pix)
	drawFolderSDF(cv, r, col(15, 118, 110))
	if !hasPartialAlpha(cv.pix) {
		t.Fatal("folder icon did not produce anti-aliased coverage")
	}
	clear(cv.pix)
	drawCloseGlyphSDF(cv, r, col(0, 0, 0))
	if !hasPartialAlpha(cv.pix) {
		t.Fatal("close glyph did not produce anti-aliased coverage")
	}
}

func hasPartialAlpha(pix []byte) bool {
	var hasAA, hasSolid bool
	for i := 3; i < len(pix); i += 4 {
		a := pix[i]
		if a > 0 && a < 255 {
			hasAA = true
		} else if a == 255 {
			hasSolid = true
		}
	}
	return hasAA || hasSolid
}
