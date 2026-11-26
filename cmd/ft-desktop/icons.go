//go:build windows

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
// kind 0 = transfer arrows, 1 = history clock, 2 = paired devices, 3 = settings gear.
func drawNavIconSDF(cv *glassCanvas, r rectT, kind int, color uint32) {
	const halfW = 0.92
	switch kind {
	case 0: // Bidirectional transfer arrows
		renderVectorSDF(cv, r, color, func(vx, vy float64) float64 {
			d := sdLineStroke(vx, vy, 5.0, 8.2, 19.0, 8.2, halfW)
			d = math.Min(d, sdLineStroke(vx, vy, 15.0, 4.6, 19.0, 8.2, halfW))
			d = math.Min(d, sdLineStroke(vx, vy, 15.0, 11.8, 19.0, 8.2, halfW))
			d = math.Min(d, sdLineStroke(vx, vy, 19.0, 15.8, 5.0, 15.8, halfW))
			d = math.Min(d, sdLineStroke(vx, vy, 9.0, 12.2, 5.0, 15.8, halfW))
			d = math.Min(d, sdLineStroke(vx, vy, 9.0, 19.4, 5.0, 15.8, halfW))
			return d
		})
	case 1: // History clock
		renderVectorSDF(cv, r, color, func(vx, vy float64) float64 {
			d := sdCircleRing(vx, vy, 12, 12, 8.4, halfW)
			d = math.Min(d, sdLineStroke(vx, vy, 12, 7.2, 12, 12, halfW))
			d = math.Min(d, sdLineStroke(vx, vy, 12, 12, 15.6, 14.1, halfW))
			return d
		})
	case 2: // Devices (laptop + phone)
		renderVectorSDF(cv, r, color, func(vx, vy float64) float64 {
			d := sdRoundRectStroke(vx, vy, 2.2, 4.2, 12.6, 9.4, 1.8, halfW)
			d = math.Min(d, sdLineStroke(vx, vy, 4.2, 16.4, 10.6, 16.4, halfW))
			d = math.Min(d, sdRoundRectStroke(vx, vy, 14.4, 8.8, 7.4, 11.4, 1.8, halfW))
			d = math.Min(d, sdLineStroke(vx, vy, 17.0, 17.8, 19.2, 17.8, halfW))
			return d
		})
	case 3: // Settings gear (center ring + outer ring + 8 teeth)
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

// drawUploadSDF draws an upload arrow over a bottom tray inside r.
func drawUploadSDF(cv *glassCanvas, r rectT, color uint32) {
	const halfW = 0.95
	renderVectorSDF(cv, r, color, func(vx, vy float64) float64 {
		d := sdLineStroke(vx, vy, 12.0, 15.0, 12.0, 4.6, halfW)
		d = math.Min(d, sdLineStroke(vx, vy, 7.4, 9.2, 12.0, 4.6, halfW))
		d = math.Min(d, sdLineStroke(vx, vy, 16.6, 9.2, 12.0, 4.6, halfW))
		d = math.Min(d, sdLineStroke(vx, vy, 4.4, 15.4, 4.4, 19.0, halfW))
		d = math.Min(d, sdLineStroke(vx, vy, 4.4, 19.0, 19.6, 19.0, halfW))
		d = math.Min(d, sdLineStroke(vx, vy, 19.6, 19.0, 19.6, 15.4, halfW))
		return d
	})
}

// drawSendSDF draws a paper-plane send icon inside r.
func drawSendSDF(cv *glassCanvas, r rectT, color uint32) {
	const halfW = 0.92
	renderVectorSDF(cv, r, color, func(vx, vy float64) float64 {
		d := sdLineStroke(vx, vy, 20.5, 3.5, 14.0, 20.5, halfW)
		d = math.Min(d, sdLineStroke(vx, vy, 14.0, 20.5, 10.6, 13.4, halfW))
		d = math.Min(d, sdLineStroke(vx, vy, 10.6, 13.4, 3.5, 10.0, halfW))
		d = math.Min(d, sdLineStroke(vx, vy, 3.5, 10.0, 20.5, 3.5, halfW))
		d = math.Min(d, sdLineStroke(vx, vy, 20.5, 3.5, 10.6, 13.4, halfW))
		return d
	})
}

// drawClipSDF draws a clipboard icon inside r for the smart clipboard bar.
func drawClipSDF(cv *glassCanvas, r rectT, color uint32) {
	const halfW = 0.90
	renderVectorSDF(cv, r, color, func(vx, vy float64) float64 {
		d := sdRoundRectStroke(vx, vy, 5.2, 4.8, 13.6, 15.8, 2.0, halfW)
		d = math.Min(d, sdRoundRectStroke(vx, vy, 8.8, 2.8, 6.4, 3.2, 1.0, halfW))
		d = math.Min(d, sdLineStroke(vx, vy, 8.8, 10.6, 15.2, 10.6, halfW))
		d = math.Min(d, sdLineStroke(vx, vy, 8.8, 14.8, 13.6, 14.8, halfW))
		return d
	})
}

// drawDashedRoundRect draws an anti-aliased dashed rounded rectangle border on cv.
func drawDashedRoundRect(cv *glassCanvas, r rectT, rad float64, edge uint32, edgeA byte, border, dashLen, gapLen float64) {
	if cv == nil || len(cv.pix) == 0 || edgeA == 0 || border <= 0 {
		return
	}
	x0, y0, x1, y1, ok := cv.clampRect(r)
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
	period := dashLen + gapLen
	if period <= 0 {
		period = 10
	}
	eb, eg, er := byte((edge>>16)&0xff), byte((edge>>8)&0xff), byte(edge&0xff)

	for y := y0; y < y1; y++ {
		py := float64(y) + 0.5
		dy := math.Abs(py-cy) - hy
		if dy < 0 {
			dy = 0
		}
		for x := x0; x < x1; x++ {
			px := float64(x) + 0.5
			dx := math.Abs(px-cx) - hx
			if dx < 0 {
				dx = 0
			}
			sd := math.Hypot(dx, dy) - rad
			distToStroke := math.Abs(sd + border/2)
			if distToStroke >= border/2+0.7 {
				continue
			}
			strokeCov := 1.0
			if distToStroke > border/2-0.7 {
				strokeCov = (border/2 + 0.7 - distToStroke) / 1.4
			}
			if strokeCov <= 0 {
				continue
			}
			// Compute continuous coordinate along perimeter starting top-left clockwise.
			var perim float64
			rx, ry := px-cx, py-cy
			switch {
			case math.Abs(rx) <= hx && ry < 0:
				perim = px - (cx - hx)
			case math.Abs(ry) <= hy && rx > 0:
				perim = 2*hx + math.Pi*rad/2 + (py - (cy - hy))
			case math.Abs(rx) <= hx && ry > 0:
				perim = 2*hx + 2*hy + math.Pi*rad + ((cx + hx) - px)
			case math.Abs(ry) <= hy && rx < 0:
				perim = 4*hx + 2*hy + 1.5*math.Pi*rad + ((cy + hy) - py)
			default:
				// Corner quadrants: always visible or continuous via arc angle.
				var ax, ay float64
				base := 0.0
				switch {
				case rx > 0 && ry < 0: // top-right
					ax, ay = px-(cx+hx), -(py-(cy-hy))
					base = 2*hx + math.Atan2(ax, ay)*rad
				case rx > 0 && ry > 0: // bottom-right
					ax, ay = px-(cx+hx), py-(cy+hy)
					base = 2*hx + math.Pi*rad/2 + 2*hy + math.Atan2(ay, ax)*rad
				case rx < 0 && ry > 0: // bottom-left
					ax, ay = -(px-(cx-hx)), py-(cy+hy)
					base = 4*hx + math.Pi*rad + 2*hy + math.Atan2(ax, ay)*rad
				default: // top-left
					ax, ay = -(px-(cx-hx)), -(py-(cy-hy))
					base = 4*hx + 1.5*math.Pi*rad + 4*hy + math.Atan2(ay, ax)*rad
				}
				perim = base
			}
			ph := math.Mod(perim, period)
			if ph < 0 {
				ph += period
			}
			if ph > dashLen {
				continue
			}
			a := byte(float64(edgeA)*strokeCov + 0.5)
			cv.blendPremulPixel(x, y, eb, eg, er, a)
		}
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
