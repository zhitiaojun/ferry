//go:build windows

package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
)

func TestIsImageFilename(t *testing.T) {
	yes := []string{
		"a.png", "b.PNG", "c.jpg", "d.JpEg", "e.gif", "f.bmp",
		"g.webp", "h.tif", "i.TIFF", "j.heic", "k.HEIF", "l.avif",
		"m.ico", "n.svg", `C:\Users\demo\Downloads\photo.webp`,
	}
	for _, p := range yes {
		if !isImageFilename(p) {
			t.Errorf("isImageFilename(%q) = false, want true", p)
		}
	}

	no := []string{
		"", "noext", "doc.pdf", "note.txt", "clip.mp4", "archive.zip", "app.exe", "img.png.bak",
	}
	for _, p := range no {
		if isImageFilename(p) {
			t.Errorf("isImageFilename(%q) = true, want false", p)
		}
	}
}

func buildExifAPP1(endian string, orient uint16) []byte {
	var bo binary.ByteOrder = binary.LittleEndian
	if endian == "MM" {
		bo = binary.BigEndian
	}
	// APP1 payload: "Exif\x00\x00" (6) + TIFF header (8) + IFD count (2) + 1 entry (12) + next IFD (4)
	payload := make([]byte, 6+8+2+12+4)
	copy(payload[:6], "Exif\x00\x00")
	tiffData := payload[6:]
	copy(tiffData[0:2], endian)
	bo.PutUint16(tiffData[2:4], 42)
	bo.PutUint32(tiffData[4:8], 8) // IFD0 offset

	ifd := tiffData[8:]
	bo.PutUint16(ifd[0:2], 1)          // 1 tag entry
	bo.PutUint16(ifd[2:4], 0x0112)     // Orientation
	bo.PutUint16(ifd[4:6], 3)          // SHORT
	bo.PutUint32(ifd[6:10], 1)         // count = 1
	bo.PutUint16(ifd[10:12], orient)   // value left-aligned in 4-byte field
	bo.PutUint32(ifd[14:18], 0)        // next IFD = 0

	seg := make([]byte, 2+2+len(payload))
	seg[0] = 0xFF
	seg[1] = 0xE1
	binary.BigEndian.PutUint16(seg[2:4], uint16(2+len(payload)))
	copy(seg[4:], payload)
	return seg
}

func injectJPEGExif(t *testing.T, jpegBytes []byte, endian string, orient uint16) []byte {
	t.Helper()
	if len(jpegBytes) < 2 || jpegBytes[0] != 0xFF || jpegBytes[1] != 0xD8 {
		t.Fatalf("invalid JPEG header")
	}
	seg := buildExifAPP1(endian, orient)
	out := make([]byte, 0, len(jpegBytes)+len(seg))
	out = append(out, jpegBytes[:2]...)
	out = append(out, seg...)
	out = append(out, jpegBytes[2:]...)
	return out
}

func TestReadJPEGExifOrientation(t *testing.T) {
	base := image.NewRGBA(image.Rect(0, 0, 2, 2))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, base, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	plain := buf.Bytes()

	if got := readJPEGExifOrientation(plain); got != 1 {
		t.Fatalf("plain JPEG orientation = %d, want 1", got)
	}
	for _, endian := range []string{"II", "MM"} {
		for _, want := range []uint16{1, 3, 6, 8} {
			withExif := injectJPEGExif(t, plain, endian, want)
			if got := readJPEGExifOrientation(withExif); got != int(want) {
				t.Errorf("endian=%s orient=%d: got %d", endian, want, got)
			}
		}
	}

	if got := readJPEGExifOrientation([]byte("not-a-jpeg")); got != 1 {
		t.Errorf("non-JPEG got %d, want 1", got)
	}
	truncated := []byte{0xFF, 0xD8, 0xFF, 0xE1, 0x00, 0x10, 'E', 'x', 'i', 'f', 0, 0}
	if got := readJPEGExifOrientation(truncated); got != 1 {
		t.Errorf("truncated APP1 got %d, want 1", got)
	}
}

func TestApplyExifOrientation(t *testing.T) {
	// 2x3 asymmetric grid:
	// (0,0)=R  (1,0)=G
	// (0,1)=B  (1,1)=Y
	// (0,2)=C  (1,2)=M
	R := color.RGBA{255, 0, 0, 255}
	G := color.RGBA{0, 255, 0, 255}
	B := color.RGBA{0, 0, 255, 255}
	Y := color.RGBA{255, 255, 0, 255}
	C := color.RGBA{0, 255, 255, 255}
	M := color.RGBA{255, 0, 255, 255}

	src := image.NewRGBA(image.Rect(0, 0, 2, 3))
	src.SetRGBA(0, 0, R)
	src.SetRGBA(1, 0, G)
	src.SetRGBA(0, 1, B)
	src.SetRGBA(1, 1, Y)
	src.SetRGBA(0, 2, C)
	src.SetRGBA(1, 2, M)

	cases := []struct {
		orient int
		w, h   int
		grid   [][]color.RGBA // [y][x]
	}{
		{1, 2, 3, [][]color.RGBA{{R, G}, {B, Y}, {C, M}}},
		{2, 2, 3, [][]color.RGBA{{G, R}, {Y, B}, {M, C}}},
		{3, 2, 3, [][]color.RGBA{{M, C}, {Y, B}, {G, R}}},
		{4, 2, 3, [][]color.RGBA{{C, M}, {B, Y}, {R, G}}},
		{5, 3, 2, [][]color.RGBA{{R, B, C}, {G, Y, M}}},
		{6, 3, 2, [][]color.RGBA{{C, B, R}, {M, Y, G}}},
		{7, 3, 2, [][]color.RGBA{{M, Y, G}, {C, B, R}}},
		{8, 3, 2, [][]color.RGBA{{G, Y, M}, {R, B, C}}},
	}

	for _, tc := range cases {
		got := applyExifOrientation(src, tc.orient)
		if got.Bounds().Dx() != tc.w || got.Bounds().Dy() != tc.h {
			t.Fatalf("orient %d dims = %dx%d, want %dx%d", tc.orient, got.Bounds().Dx(), got.Bounds().Dy(), tc.w, tc.h)
		}
		for y := 0; y < tc.h; y++ {
			for x := 0; x < tc.w; x++ {
				if c := got.RGBAAt(x, y); c != tc.grid[y][x] {
					t.Errorf("orient %d (%d,%d) = %+v, want %+v", tc.orient, x, y, c, tc.grid[y][x])
				}
			}
		}
	}
}

func TestEncodeClipboardImage(t *testing.T) {
	dir := t.TempDir()

	// 1. Transparent 3x2 PNG (odd width exercises 4-byte DIB row padding: 3*3=9 -> stride 12).
	pngPath := filepath.Join(dir, "sample.png")
	srcPNG := image.NewRGBA(image.Rect(0, 0, 3, 2))
	srcPNG.SetRGBA(0, 0, color.RGBA{255, 0, 0, 255})   // top-left: red
	srcPNG.SetRGBA(1, 0, color.RGBA{0, 0, 0, 0})       // top-mid: transparent -> white in DIB
	srcPNG.SetRGBA(2, 0, color.RGBA{0, 200, 0, 255})   // top-right: green
	srcPNG.SetRGBA(0, 1, color.RGBA{0, 0, 255, 255})   // bottom-left: blue
	srcPNG.SetRGBA(1, 1, color.RGBA{255, 255, 0, 255}) // bottom-mid: yellow
	srcPNG.SetRGBA(2, 1, color.RGBA{0, 0, 0, 0})       // bottom-right: transparent -> white in DIB

	var rawBuf bytes.Buffer
	if err := png.Encode(&rawBuf, srcPNG); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pngPath, rawBuf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	pngBytes, dibBytes, ok := encodeClipboardImage(pngPath)
	if !ok {
		t.Fatal("encodeClipboardImage(sample.png) = false")
	}
	if !bytes.Equal(pngBytes, rawBuf.Bytes()) {
		t.Fatal("expected orientation-1 PNG bytes to be preserved verbatim")
	}
	// BITMAPINFOHEADER checks: biSize=40, 3x2, 24bpp, BI_RGB, stride=12, size=24
	if len(dibBytes) != 40+12*2 {
		t.Fatalf("dibBytes len = %d, want %d", len(dibBytes), 40+12*2)
	}
	if got := binary.LittleEndian.Uint32(dibBytes[0:4]); got != 40 {
		t.Errorf("biSize = %d, want 40", got)
	}
	if got := int32(binary.LittleEndian.Uint32(dibBytes[4:8])); got != 3 {
		t.Errorf("biWidth = %d, want 3", got)
	}
	if got := int32(binary.LittleEndian.Uint32(dibBytes[8:12])); got != 2 {
		t.Errorf("biHeight = %d, want 2", got)
	}
	if got := binary.LittleEndian.Uint16(dibBytes[14:16]); got != 24 {
		t.Errorf("biBitCount = %d, want 24", got)
	}
	if got := binary.LittleEndian.Uint32(dibBytes[16:20]); got != 0 {
		t.Errorf("biCompression = %d, want 0", got)
	}
	if got := binary.LittleEndian.Uint32(dibBytes[20:24]); got != 24 {
		t.Errorf("biSizeImage = %d, want 24", got)
	}
	// Bottom-up row 0 in DIB = image row y=1: (blue, yellow, white) + 3 padding bytes
	wantRow0 := []byte{
		255, 0, 0, // B,G,R for blue
		0, 255, 255, // B,G,R for yellow
		255, 255, 255, // B,G,R for transparent -> white
		0, 0, 0, // row stride padding
	}
	if !bytes.Equal(dibBytes[40:52], wantRow0) {
		t.Errorf("DIB bottom row = %v, want %v", dibBytes[40:52], wantRow0)
	}
	// Bottom-up row 1 in DIB = image row y=0: (red, white, green) + 3 padding bytes
	wantRow1 := []byte{
		0, 0, 255, // red
		255, 255, 255, // transparent -> white
		0, 200, 0, // green
		0, 0, 0,
	}
	if !bytes.Equal(dibBytes[52:64], wantRow1) {
		t.Errorf("DIB top row = %v, want %v", dibBytes[52:64], wantRow1)
	}

	// 2. JPEG with EXIF orientation 6: left half red, right half blue on 16x8 -> rotates to 8x16 (top red, bottom blue).
	jpgSrc := image.NewRGBA(image.Rect(0, 0, 16, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 16; x++ {
			if x < 8 {
				jpgSrc.SetRGBA(x, y, color.RGBA{240, 10, 10, 255})
			} else {
				jpgSrc.SetRGBA(x, y, color.RGBA{10, 10, 240, 255})
			}
		}
	}
	var jpgBuf bytes.Buffer
	if err := jpeg.Encode(&jpgBuf, jpgSrc, &jpeg.Options{Quality: 95}); err != nil {
		t.Fatal(err)
	}
	jpgPath := filepath.Join(dir, "rotated.jpg")
	if err := os.WriteFile(jpgPath, injectJPEGExif(t, jpgBuf.Bytes(), "MM", 6), 0o644); err != nil {
		t.Fatal(err)
	}
	rotPNG, rotDIB, ok := encodeClipboardImage(jpgPath)
	if !ok {
		t.Fatal("encodeClipboardImage(rotated.jpg) = false")
	}
	decRot, err := png.Decode(bytes.NewReader(rotPNG))
	if err != nil {
		t.Fatalf("decode rotated PNG: %v", err)
	}
	if decRot.Bounds().Dx() != 8 || decRot.Bounds().Dy() != 16 {
		t.Fatalf("rotated PNG dims = %dx%d, want 8x16", decRot.Bounds().Dx(), decRot.Bounds().Dy())
	}
	if r, _, b, _ := decRot.At(2, 2).RGBA(); r>>8 < 180 || b>>8 > 80 {
		t.Errorf("rotated top pixel at (2,2) = (%d, *, %d), expected reddish", r>>8, b>>8)
	}
	if r, _, b, _ := decRot.At(2, 13).RGBA(); b>>8 < 180 || r>>8 > 80 {
		t.Errorf("rotated bottom pixel at (2,13) = (%d, *, %d), expected bluish", r>>8, b>>8)
	}
	if w := int32(binary.LittleEndian.Uint32(rotDIB[4:8])); w != 8 {
		t.Errorf("rotated DIB biWidth = %d, want 8", w)
	}
	if h := int32(binary.LittleEndian.Uint32(rotDIB[8:12])); h != 16 {
		t.Errorf("rotated DIB biHeight = %d, want 16", h)
	}

	// 3. GIF, BMP, and TIFF round-trips.
	rect4x3 := image.NewRGBA(image.Rect(0, 0, 4, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 4; x++ {
			rect4x3.SetRGBA(x, y, color.RGBA{30, 140, 200, 255})
		}
	}
	for _, tc := range []struct {
		name string
		enc  func(*bytes.Buffer) error
	}{
		{"a.gif", func(b *bytes.Buffer) error { return gif.Encode(b, rect4x3, nil) }},
		{"b.bmp", func(b *bytes.Buffer) error { return bmp.Encode(b, rect4x3) }},
		{"c.tiff", func(b *bytes.Buffer) error { return tiff.Encode(b, rect4x3, nil) }},
	} {
		var ob bytes.Buffer
		if err := tc.enc(&ob); err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(dir, tc.name)
		if err := os.WriteFile(p, ob.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		pb, db, ok := encodeClipboardImage(p)
		if !ok {
			t.Fatalf("encodeClipboardImage(%s) = false", tc.name)
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(pb))
		if err != nil || cfg.Width != 4 || cfg.Height != 3 {
			t.Fatalf("%s decoded config = %+v, err=%v", tc.name, cfg, err)
		}
		if len(db) != 40+12*3 {
			t.Fatalf("%s DIB len = %d, want %d", tc.name, len(db), 40+12*3)
		}
	}

	// 4. Corrupt image and undecodable .heic return ok == false cleanly.
	corrupt := filepath.Join(dir, "bad.png")
	_ = os.WriteFile(corrupt, []byte("not-png-data"), 0o644)
	if _, _, ok := encodeClipboardImage(corrupt); ok {
		t.Error("corrupt.png returned ok=true, want false")
	}
	heic := filepath.Join(dir, "photo.heic")
	_ = os.WriteFile(heic, []byte("fake-heic-payload"), 0o644)
	if _, _, ok := encodeClipboardImage(heic); ok {
		t.Error("photo.heic returned ok=true, want false")
	}
}

func TestBuildDropFilesBytes(t *testing.T) {
	rel := filepath.Join(".", "sample-image.heic")
	wantAbs, err := filepath.Abs(rel)
	if err != nil {
		t.Fatal(err)
	}
	buf, err := buildDropFilesBytes(rel)
	if err != nil {
		t.Fatalf("buildDropFilesBytes: %v", err)
	}
	if len(buf) < 24 {
		t.Fatalf("buf too short: %d", len(buf))
	}
	if pFiles := binary.LittleEndian.Uint32(buf[0:4]); pFiles != 20 {
		t.Errorf("pFiles = %d, want 20", pFiles)
	}
	if ptX := binary.LittleEndian.Uint32(buf[4:8]); ptX != 0 {
		t.Errorf("pt.x = %d, want 0", ptX)
	}
	if ptY := binary.LittleEndian.Uint32(buf[8:12]); ptY != 0 {
		t.Errorf("pt.y = %d, want 0", ptY)
	}
	if fNC := binary.LittleEndian.Uint32(buf[12:16]); fNC != 0 {
		t.Errorf("fNC = %d, want 0", fNC)
	}
	if fWide := binary.LittleEndian.Uint32(buf[16:20]); fWide != 1 {
		t.Errorf("fWide = %d, want 1", fWide)
	}
	payload := buf[20:]
	if len(payload)%2 != 0 || len(payload) < 4 {
		t.Fatalf("unexpected UTF-16 payload len %d", len(payload))
	}
	u16 := make([]uint16, len(payload)/2)
	for i := range u16 {
		u16[i] = binary.LittleEndian.Uint16(payload[i*2 : i*2+2])
	}
	if u16[len(u16)-1] != 0 || u16[len(u16)-2] != 0 {
		t.Errorf("expected double-NUL termination, tail = %v", u16[len(u16)-2:])
	}
	if got := syscall.UTF16ToString(u16); got != wantAbs {
		t.Errorf("decoded path = %q, want %q", got, wantAbs)
	}
}

func TestDecodeDIBToPNGRoundTrip(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 3, 2))
	src.SetRGBA(0, 0, color.RGBA{210, 20, 30, 255})
	src.SetRGBA(1, 0, color.RGBA{10, 180, 90, 255})
	src.SetRGBA(2, 0, color.RGBA{40, 80, 240, 255})
	src.SetRGBA(0, 1, color.RGBA{255, 255, 0, 255})
	src.SetRGBA(1, 1, color.RGBA{0, 255, 255, 255})
	src.SetRGBA(2, 1, color.RGBA{128, 64, 32, 255})

	dib24 := buildDIB24(src)
	pngOut, ok := decodeDIBToPNG(dib24)
	if !ok {
		t.Fatal("decodeDIBToPNG(24bpp) returned false")
	}
	decoded, err := png.Decode(bytes.NewReader(pngOut))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	if decoded.Bounds().Dx() != 3 || decoded.Bounds().Dy() != 2 {
		t.Fatalf("decoded bounds = %v, want 3x2", decoded.Bounds())
	}
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			got := color.RGBAModel.Convert(decoded.At(x, y)).(color.RGBA)
			want := src.RGBAAt(x, y)
			if got != want {
				t.Fatalf("pixel (%d,%d) = %+v, want %+v", x, y, got, want)
			}
		}
	}

	// Verify 32-bit BI_RGB DIB with zero alpha channel (typical Windows screenshot bitblt).
	dib32 := make([]byte, 40+3*2*4)
	binary.LittleEndian.PutUint32(dib32[0:4], 40)
	binary.LittleEndian.PutUint32(dib32[4:8], 3)
	negTwo := int32(-2)
	binary.LittleEndian.PutUint32(dib32[8:12], uint32(negTwo)) // top-down
	binary.LittleEndian.PutUint16(dib32[12:14], 1)
	binary.LittleEndian.PutUint16(dib32[14:16], 32)
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			c := src.RGBAAt(x, y)
			off := 40 + (y*3+x)*4
			dib32[off] = c.B
			dib32[off+1] = c.G
			dib32[off+2] = c.R
			dib32[off+3] = 0
		}
	}
	pngOut32, ok := decodeDIBToPNG(dib32)
	if !ok {
		t.Fatal("decodeDIBToPNG(32bpp top-down) returned false")
	}
	decoded32, err := png.Decode(bytes.NewReader(pngOut32))
	if err != nil {
		t.Fatalf("png.Decode 32bpp: %v", err)
	}
	gotTL := color.RGBAModel.Convert(decoded32.At(0, 0)).(color.RGBA)
	if wantTL := src.RGBAAt(0, 0); gotTL != wantTL {
		t.Fatalf("32bpp pixel (0,0) = %+v, want %+v", gotTL, wantTL)
	}
}
