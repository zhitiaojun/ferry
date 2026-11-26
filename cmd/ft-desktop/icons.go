package main

import "math"

// renderVectorSDF rasterizes an analytical signed-distance-field icon defined on
// a 24x24 SVG viewBox into rect r at native physical pixel resolution with subpixel
// anti-aliasing and strict 1:1 aspect ratio centered inside r.
func renderVectorSDF(cv *glassCanvas, r rectT, color uint32, sdf func(vx, vy float64) float64) {
	if cv == nil || len(cv.pix) == 0 || sdf == nil {
		return
	}
	x0, y0, x1, y1, ok := cv.clampRect(r)
	if !ok {
		return
	}
	rw := float64(r.right - r.left)
	rh := float64(r.bottom - r.top)
	side := rw
	if rh < side {
		side = rh
	}
	if side <= 0 {
		return
	}
	ox := float64(r.left) + (rw-side)/2.0
	oy := float64(r.top) + (rh-side)/2.0
	scale := side / 24.0
	invScale := 1.0 / scale

	fb, fg, fr := byte((color>>16)&0xff), byte((color>>8)&0xff), byte(color&0xff)
	for py := y0; py < y1; py++ {
		vy := (float64(py) + 0.5 - oy) * invScale
		for px := x0; px < x1; px++ {
			vx := (float64(px) + 0.5 - ox) * invScale
			dPx := sdf(vx, vy) * scale
			cov := 0.5 - dPx
			if cov <= 0 {
				continue
			}
			if cov >= 1 {
				cov = 1
			}
			cv.blendPremulPixel(px, py, fb, fg, fr, byte(cov*255+0.5))
		}
	}
}

func sdSegment(px, py, x1, y1, x2, y2 float64) float64 {
	ax, ay := px-x1, py-y1
	bx, by := x2-x1, y2-y1
	den := bx*bx + by*by
	if den == 0 {
		return math.Hypot(ax, ay)
	}
	h := (ax*bx + ay*by) / den
	if h < 0 {
		h = 0
	} else if h > 1 {
		h = 1
	}
	return math.Hypot(ax-bx*h, ay-by*h)
}

func sdLineStroke(px, py, x1, y1, x2, y2, halfW float64) float64 {
	return sdSegment(px, py, x1, y1, x2, y2) - halfW
}

func sdCircleRing(px, py, cx, cy, rad, halfW float64) float64 {
	return math.Abs(math.Hypot(px-cx, py-cy)-rad) - halfW
}

func sdRoundRectStroke(px, py, x, y, w, h, rx, halfW float64) float64 {
	hw := w / 2.0
	hh := h / 2.0
	if rx > hw {
		rx = hw
	}
	if rx > hh {
		rx = hh
	}
	cx := x + hw
	cy := y + hh
	qx := math.Abs(px-cx) - (hw - rx)
	qy := math.Abs(py-cy) - (hh - rx)
	dx := qx
	if dx < 0 {
		dx = 0
	}
	dy := qy
	if dy < 0 {
		dy = 0
	}
	inMax := qx
	if qy > inMax {
		inMax = qy
	}
	if inMax > 0 {
		inMax = 0
	}
	d := math.Hypot(dx, dy) + inMax - rx
	return math.Abs(d) - halfW
}

// drawNavIconSDF draws crisp SVG-viewBox vector icons for the left navigation rail:
// kind 0 = transfer arrow, 1 = paired devices, 2 = settings gear.
func drawNavIconSDF(cv *glassCanvas, r rectT, kind int, color uint32) {
	const halfW = 0.92
	switch kind {
	case 0: // Send arrow
		renderVectorSDF(cv, r, color, func(vx, vy float64) float64 {
			d := sdLineStroke(vx, vy, 6, 18, 18, 6, halfW)
			d = math.Min(d, sdLineStroke(vx, vy, 9.5, 6, 18, 6, halfW))
			d = math.Min(d, sdLineStroke(vx, vy, 18, 6, 18, 14.5, halfW))
			return d
		})
	case 1: // Devices (laptop + phone)
		renderVectorSDF(cv, r, color, func(vx, vy float64) float64 {
			d := sdRoundRectStroke(vx, vy, 2.2, 4.2, 12.6, 9.4, 1.8, halfW)
			d = math.Min(d, sdLineStroke(vx, vy, 4.2, 16.4, 10.6, 16.4, halfW))
			d = math.Min(d, sdRoundRectStroke(vx, vy, 14.4, 8.8, 7.4, 11.4, 1.8, halfW))
			d = math.Min(d, sdLineStroke(vx, vy, 17.0, 17.8, 19.2, 17.8, halfW))
			return d
		})
	case 2: // Settings gear (center ring + outer ring + 8 teeth)
		renderVectorSDF(cv, r, color, func(vx, vy float64) float64 {
			d := sdCircleRing(vx, vy, 12, 12, 2.8, halfW)
			d = math.Min(d, sdCircleRing(vx, vy, 12, 12, 6.4, halfW))
			for k := 0; k < 8; k++ {
				ang := float64(k) * math.Pi / 4.0
				cs, sn := math.Cos(ang), math.Sin(ang)
				d = math.Min(d, sdLineStroke(vx, vy, 12+6.6*cs, 12+6.6*sn, 12+9.3*cs, 12+9.3*sn, halfW))
			}
			return d
		})
	}
}

// drawBrandBoatSDF draws the crisp Ferry paper-boat mark inside r.
func drawBrandBoatSDF(cv *glassCanvas, r rectT, color uint32) {
	const halfW = 0.92
	renderVectorSDF(cv, r, color, func(vx, vy float64) float64 {
		d := sdLineStroke(vx, vy, 4.5, 12.2, 19.5, 9.0, halfW)
		d = math.Min(d, sdLineStroke(vx, vy, 19.5, 9.0, 16.4, 15.4, halfW))
		d = math.Min(d, sdLineStroke(vx, vy, 16.4, 15.4, 7.0, 15.4, halfW))
		d = math.Min(d, sdLineStroke(vx, vy, 7.0, 15.4, 4.5, 12.2, halfW))
		d = math.Min(d, sdLineStroke(vx, vy, 5.4, 18.4, 18.6, 18.4, halfW))
		return d
	})
}

// drawFolderSDF draws an anti-aliased folder outline inside r.
func drawFolderSDF(cv *glassCanvas, r rectT, color uint32) {
	const halfW = 0.85
	renderVectorSDF(cv, r, color, func(vx, vy float64) float64 {
		d := sdLineStroke(vx, vy, 4.0, 7.2, 9.6, 7.2, halfW)
		d = math.Min(d, sdLineStroke(vx, vy, 9.6, 7.2, 11.6, 9.5, halfW))
		d = math.Min(d, sdLineStroke(vx, vy, 11.6, 9.5, 20.0, 9.5, halfW))
		d = math.Min(d, sdLineStroke(vx, vy, 20.0, 9.5, 20.0, 17.8, halfW))
		d = math.Min(d, sdLineStroke(vx, vy, 20.0, 17.8, 4.0, 17.8, halfW))
		d = math.Min(d, sdLineStroke(vx, vy, 4.0, 17.8, 4.0, 7.2, halfW))
		return d
	})
}

// drawCloseGlyphSDF draws an anti-aliased caption close cross centered in r.
func drawCloseGlyphSDF(cv *glassCanvas, r rectT, color uint32) {
	if cv == nil || len(cv.pix) == 0 {
		return
	}
	rw := float64(r.right - r.left)
	rh := float64(r.bottom - r.top)
	side := rw
	if rh < side {
		side = rh
	}
	sz := side * 0.28
	if sz < 8 {
		sz = 8
	}
	if sz > side {
		sz = side
	}
	cx := float64(r.left) + rw/2.0
	cy := float64(r.top) + rh/2.0
	box := rectT{
		left:   int32(math.Floor(cx - sz/2.0 - 2)),
		top:    int32(math.Floor(cy - sz/2.0 - 2)),
		right:  int32(math.Ceil(cx + sz/2.0 + 2)),
		bottom: int32(math.Ceil(cy + sz/2.0 + 2)),
	}
	half := sz / 2.0
	x0, y0, x1, y1 := cx-half, cy-half, cx+half, cy+half
	const halfW = 0.65

	cx0, cy0, cx1, cy1, ok := cv.clampRect(box)
	if !ok {
		return
	}
	fb, fg, fr := byte((color>>16)&0xff), byte((color>>8)&0xff), byte(color&0xff)
	for py := cy0; py < cy1; py++ {
		fpy := float64(py) + 0.5
		for px := cx0; px < cx1; px++ {
			fpx := float64(px) + 0.5
			d := math.Min(
				sdLineStroke(fpx, fpy, x0, y0, x1, y1, halfW),
				sdLineStroke(fpx, fpy, x1, y0, x0, y1, halfW),
			)
			cov := 0.5 - d
			if cov <= 0 {
				continue
			}
			if cov >= 1 {
				cov = 1
			}
			cv.blendPremulPixel(px, py, fb, fg, fr, byte(cov*255+0.5))
		}
	}
}
