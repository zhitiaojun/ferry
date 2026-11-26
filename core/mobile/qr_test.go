package mobile

import (
	"bytes"
	"image"
	"image/png"
	"strings"
	"testing"
	"time"

	"github.com/zhitiaojun/ferry/core/pairing"
)

func TestQRGenerateAndDecodeRoundTrip(t *testing.T) {
	id := strings.Repeat("c3", 32)
	uri := pairing.BuildPairURI(id, "519082", "HyperX", time.Now().UTC().Add(pairing.DefaultTTL))

	pngBytes, err := GenerateQRPNG(uri, 320)
	if err != nil {
		t.Fatalf("GenerateQRPNG: %v", err)
	}
	im, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	b := im.Bounds()
	w, h := b.Dx(), b.Dy()
	gray := make([]byte, w*h)
	gimg := image.NewGray(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			gimg.Set(x, y, im.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	copy(gray, gimg.Pix)

	got, err := DecodeQRGray(gray, w, h)
	if err != nil {
		t.Fatalf("DecodeQRGray: %v", err)
	}
	if got != uri {
		t.Fatalf("DecodeQRGray = %q, want %q", got, uri)
	}

	// Blank frame returns "", nil
	blank := bytes.Repeat([]byte{255}, 128*128)
	gotBlank, err := DecodeQRGray(blank, 128, 128)
	if err != nil || gotBlank != "" {
		t.Fatalf("blank frame got (%q, %v), want (\"\", nil)", gotBlank, err)
	}
}
