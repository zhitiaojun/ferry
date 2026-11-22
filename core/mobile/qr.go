package mobile

import (
	"errors"
	"fmt"
	"image"

	"github.com/makiuchi-d/gozxing"
	gozxingqr "github.com/makiuchi-d/gozxing/qrcode"
	qrcode "github.com/skip2/go-qrcode"
)

// GenerateQRPNG renders content into a square PNG QR code of size pixels.
func GenerateQRPNG(content string, size int) ([]byte, error) {
	if content == "" {
		return nil, errors.New("qr content is empty")
	}
	if size <= 0 {
		size = 512
	}
	return qrcode.Encode(content, qrcode.Medium, size)
}

// DecodeQRGray decodes the first QR payload found in an 8-bit grayscale buffer
// (such as Android Camera2 YUV_420_888 plane 0 luminance bytes). Returns ("", nil)
// when no QR code is detected in the frame.
func DecodeQRGray(gray []byte, width, height int) (string, error) {
	if width <= 0 || height <= 0 {
		return "", fmt.Errorf("invalid image dimensions %dx%d", width, height)
	}
	need := width * height
	if len(gray) < need {
		return "", fmt.Errorf("grayscale buffer too short: got %d, want %d", len(gray), need)
	}
	img := &image.Gray{
		Pix:    gray[:need],
		Stride: width,
		Rect:   image.Rect(0, 0, width, height),
	}
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		return "", nil
	}
	res, err := gozxingqr.NewQRCodeReader().Decode(bmp, nil)
	if err != nil || res == nil {
		return "", nil
	}
	return res.GetText(), nil
}
