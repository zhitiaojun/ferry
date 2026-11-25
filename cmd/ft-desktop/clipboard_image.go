//go:build windows

package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"
)

const (
	cfDIB       = 8
	cfHDROP     = 15
	gmemMove    = 0x0002
	maxImgBytes = 50 << 20
	maxImgPx    = int64(64_000_000)
)

var clipboardMu sync.Mutex

var imageExtensions = map[string]bool{
	".png":  true,
	".jpg":  true,
	".jpeg": true,
	".gif":  true,
	".bmp":  true,
	".webp": true,
	".tif":  true,
	".tiff": true,
	".heic": true,
	".heif": true,
	".avif": true,
	".ico":  true,
	".svg":  true,
}

type clipboardEntry struct {
	format uint32
	data   []byte
}

func isImageFilename(path string) bool {
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(path)))
	return imageExtensions[ext]
}

// readJPEGExifOrientation scans JPEG APP1 Exif segments for TIFF IFD0 tag 0x0112.
// It returns an orientation in 1..8, defaulting to 1 if absent or malformed.
func readJPEGExifOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 1
	}
	pos := 2
	for pos < len(data) {
		if data[pos] != 0xFF {
			pos++
			continue
		}
		for pos < len(data) && data[pos] == 0xFF {
			pos++
		}
		if pos >= len(data) {
			break
		}
		marker := data[pos]
		pos++
		if marker == 0xD9 || marker == 0xDA {
			break
		}
		if marker == 0x00 || marker == 0x01 || (marker >= 0xD0 && marker <= 0xD8) {
			continue
		}
		if pos+2 > len(data) {
			break
		}
		segLen := int(binary.BigEndian.Uint16(data[pos : pos+2]))
		if segLen < 2 || pos+segLen > len(data) {
			break
		}
		payload := data[pos+2 : pos+segLen]
		pos += segLen

		if marker == 0xE1 && len(payload) >= 14 && bytes.Equal(payload[:6], []byte("Exif\x00\x00")) {
			if orient := parseTIFFOrientation(payload[6:]); orient != 0 {
				return orient
			}
		}
	}
	return 1
}

func parseTIFFOrientation(tiff []byte) int {
	if len(tiff) < 8 {
		return 0
	}
	var bo binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 0
	}
	if bo.Uint16(tiff[2:4]) != 42 {
		return 0
	}
	ifdOff := int(bo.Uint32(tiff[4:8]))
	if ifdOff < 8 || ifdOff+2 > len(tiff) {
		return 0
	}
	count := int(bo.Uint16(tiff[ifdOff : ifdOff+2]))
	for i := 0; i < count; i++ {
		entOff := ifdOff + 2 + i*12
		if entOff+12 > len(tiff) {
			break
		}
		if bo.Uint16(tiff[entOff:entOff+2]) != 0x0112 {
			continue
		}
		typ := bo.Uint16(tiff[entOff+2 : entOff+4])
		valCount := bo.Uint32(tiff[entOff+4 : entOff+8])
		if typ == 3 && valCount >= 1 {
			v := int(bo.Uint16(tiff[entOff+8 : entOff+10]))
			if v >= 1 && v <= 8 {
				return v
			}
		}
		return 1
	}
	return 0
}

func toNormalizedRGBA(src image.Image) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Src)
	return dst
}

// applyExifOrientation transforms src into a top-left oriented RGBA image
// according to EXIF orientation values 1..8.
func applyExifOrientation(src *image.RGBA, orient int) *image.RGBA {
	if src == nil {
		return nil
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if b.Min.X != 0 || b.Min.Y != 0 {
		src = toNormalizedRGBA(src)
	}
	if orient <= 1 || orient > 8 {
		return src
	}

	dw, dh := w, h
	if orient >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for sy := 0; sy < h; sy++ {
		srcRow := sy * src.Stride
		for sx := 0; sx < w; sx++ {
			var dx, dy int
			switch orient {
			case 2:
				dx, dy = w-1-sx, sy
			case 3:
				dx, dy = w-1-sx, h-1-sy
			case 4:
				dx, dy = sx, h-1-sy
			case 5:
				dx, dy = sy, sx
			case 6:
				dx, dy = h-1-sy, sx
			case 7:
				dx, dy = h-1-sy, w-1-sx
			case 8:
				dx, dy = sy, w-1-sx
			}
			sOff := srcRow + sx*4
			dOff := dy*dst.Stride + dx*4
			copy(dst.Pix[dOff:dOff+4], src.Pix[sOff:sOff+4])
		}
	}
	return dst
}

func compositeOverWhite(src *image.RGBA) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Over)
	return dst
}

// buildDIB24 formats img as a packed CF_DIB (40-byte BITMAPINFOHEADER + 24-bit
// bottom-up BI_RGB BGR pixel data with 4-byte aligned rows).
func buildDIB24(img *image.RGBA) []byte {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil
	}
	if b.Min.X != 0 || b.Min.Y != 0 {
		img = toNormalizedRGBA(img)
	}
	const hdrSize = 40
	rowStride := ((w*3 + 3) / 4) * 4
	imgSize := rowStride * h
	buf := make([]byte, hdrSize+imgSize)

	binary.LittleEndian.PutUint32(buf[0:4], hdrSize)
	binary.LittleEndian.PutUint32(buf[4:8], uint32(int32(w)))
	binary.LittleEndian.PutUint32(buf[8:12], uint32(int32(h)))
	binary.LittleEndian.PutUint16(buf[12:14], 1)
	binary.LittleEndian.PutUint16(buf[14:16], 24)
	binary.LittleEndian.PutUint32(buf[16:20], 0) // BI_RGB
	binary.LittleEndian.PutUint32(buf[20:24], uint32(imgSize))

	for y := 0; y < h; y++ {
		srcY := h - 1 - y
		srcRow := srcY * img.Stride
		dstRow := hdrSize + y*rowStride
		for x := 0; x < w; x++ {
			s := srcRow + x*4
			d := dstRow + x*3
			buf[d] = img.Pix[s+2]   // B
			buf[d+1] = img.Pix[s+1] // G
			buf[d+2] = img.Pix[s]   // R
		}
	}
	return buf
}

// encodeClipboardImage decodes an image file under safety limits and produces
// PNG bytes (preserving alpha) plus a 24-bit CF_DIB payload composited over white.
func encodeClipboardImage(path string) (pngBytes []byte, dibBytes []byte, ok bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > maxImgBytes {
		return nil, nil, false
	}
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 || int64(len(raw)) > maxImgBytes {
		return nil, nil, false
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(raw))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > maxImgPx {
		return nil, nil, false
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, nil, false
	}
	orient := 1
	if format == "jpeg" {
		orient = readJPEGExifOrientation(raw)
	}
	rgba := applyExifOrientation(toNormalizedRGBA(img), orient)
	if rgba == nil {
		return nil, nil, false
	}

	if format == "png" && orient == 1 {
		pngBytes = raw
	} else {
		var out bytes.Buffer
		enc := png.Encoder{CompressionLevel: png.BestSpeed}
		if err := enc.Encode(&out, rgba); err != nil {
			return nil, nil, false
		}
		pngBytes = out.Bytes()
	}

	dibBytes = buildDIB24(compositeOverWhite(rgba))
	if len(pngBytes) == 0 || len(dibBytes) == 0 {
		return nil, nil, false
	}
	return pngBytes, dibBytes, true
}

// buildDropFilesBytes constructs a CF_HDROP payload (20-byte DROPFILES header
// followed by a double-NUL-terminated UTF-16LE absolute path).
func buildDropFilesBytes(path string) ([]byte, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := syscall.StringToUTF16(abs) // includes single trailing NUL
	const headerSize = 20
	buf := make([]byte, headerSize+(len(u)+1)*2)
	binary.LittleEndian.PutUint32(buf[0:4], headerSize) // pFiles
	binary.LittleEndian.PutUint32(buf[16:20], 1)         // fWide = TRUE
	for i, c := range u {
		binary.LittleEndian.PutUint16(buf[headerSize+i*2:headerSize+i*2+2], c)
	}
	return buf, nil
}

func registerClipboardFormat(name string) uint32 {
	p := utf16Ptr(name)
	r, _, _ := procRegisterClipboardFormatW.Call(uintptr(unsafe.Pointer(p)))
	return uint32(r)
}

func allocHGlobal(data []byte) uintptr {
	if len(data) == 0 {
		return 0
	}
	hMem, _, _ := procGlobalAlloc.Call(gmemMove, uintptr(len(data)))
	if hMem == 0 {
		return 0
	}
	p, _, _ := procGlobalLock.Call(hMem)
	if p == 0 {
		procGlobalFree.Call(hMem)
		return 0
	}
	dst := unsafe.Slice((*byte)(ptrAt(p)), len(data))
	copy(dst, data)
	procGlobalUnlock.Call(hMem)
	return hMem
}

func setClipboardEntries(hwnd syscall.Handle, entries []clipboardEntry) bool {
	if len(entries) == 0 {
		return false
	}
	clipboardMu.Lock()
	defer clipboardMu.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	var opened bool
	for i := 0; i < 10; i++ {
		if ok, _, _ := procOpenClipboard.Call(uintptr(hwnd)); ok != 0 {
			opened = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !opened {
		return false
	}
	defer procCloseClipboard.Call()

	if r, _, _ := procEmptyClipboard.Call(); r == 0 {
		return false
	}
	for _, e := range entries {
		if e.format == 0 || len(e.data) == 0 {
			return false
		}
		hMem := allocHGlobal(e.data)
		if hMem == 0 {
			return false
		}
		r, _, _ := procSetClipboardData.Call(uintptr(e.format), hMem)
		if r == 0 {
			procGlobalFree.Call(hMem)
			return false
		}
	}
	return true
}

func clipboardOwnerHWND() syscall.Handle {
	if trayHandle != nil && trayHandle.hwnd != 0 {
		return trayHandle.hwnd
	}
	if h := existingMainWindow(); h != 0 {
		return h
	}
	return 0
}

// copyImageFileToClipboard writes a decoded image as PNG + CF_DIB, falling back
// to CF_HDROP when the file format cannot be decoded cleanly.
func copyImageFileToClipboard(path string) (mode string, ok bool) {
	if !isImageFilename(path) {
		return "", false
	}
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	hwnd := clipboardOwnerHWND()

	if pngBytes, dibBytes, decoded := encodeClipboardImage(path); decoded {
		var entries []clipboardEntry
		if pngFmt := registerClipboardFormat("PNG"); pngFmt != 0 {
			entries = append(entries, clipboardEntry{format: pngFmt, data: pngBytes})
		}
		entries = append(entries, clipboardEntry{format: cfDIB, data: dibBytes})
		if setClipboardEntries(hwnd, entries) {
			return "bitmap", true
		}
	}

	dropBytes, err := buildDropFilesBytes(path)
	if err != nil {
		return "", false
	}
	if setClipboardEntries(hwnd, []clipboardEntry{{format: cfHDROP, data: dropBytes}}) {
		return "file", true
	}
	return "", false
}

func (a *app) maybeCopySingleImageToClipboard(outDir string, names []string) bool {
	if len(names) != 1 {
		return false
	}
	return a.maybeCopySingleImagePathsToClipboard([]string{filepath.Join(outDir, names[0])})
}

func (a *app) maybeCopySingleImagePathsToClipboard(paths []string) bool {
	if len(paths) != 1 {
		return false
	}
	mode, ok := copyImageFileToClipboard(paths[0])
	if !ok {
		return false
	}
	if a != nil {
		a.event("desktop.clipboard_image_copied", map[string]any{
			"file": filepath.Base(paths[0]),
			"mode": mode,
		})
	}
	return true
}
