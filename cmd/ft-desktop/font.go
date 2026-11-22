//go:build windows

package main

import (
	"embed"
	"io/fs"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

//go:embed fonts/*.ttf
var embeddedFonts embed.FS

var (
	procAddFontMemResourceEx = gdi32.NewProc("AddFontMemResourceEx")

	fontLoadOnce    sync.Once
	fontMemHandles  []uintptr
	loadedFontCount int
)

// ensureEmbeddedFonts registers the bundled Xiaomi MiSans fonts into the current
// process via AddFontMemResourceEx so the UI always renders with MiSans even when
// the font is not installed system-wide.
func ensureEmbeddedFonts() int {
	fontLoadOnce.Do(func() {
		entries, err := fs.ReadDir(embeddedFonts, "fonts")
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".ttf") {
				continue
			}
			data, err := embeddedFonts.ReadFile("fonts/" + e.Name())
			if err != nil || len(data) == 0 {
				continue
			}
			var count uint32
			h, _, _ := procAddFontMemResourceEx.Call(
				uintptr(unsafe.Pointer(&data[0])),
				uintptr(len(data)),
				0,
				uintptr(unsafe.Pointer(&count)),
			)
			if h != 0 && count > 0 {
				fontMemHandles = append(fontMemHandles, h)
				loadedFontCount += int(count)
			}
		}
	})
	return loadedFontCount
}

func uiFontFace() string {
	ensureEmbeddedFonts()
	for _, c := range []string{"MiSans", "MiSans Regular", "MiSans Normal", "Microsoft YaHei UI", "Microsoft YaHei", "Segoe UI"} {
		if faceExists(c) {
			return c
		}
	}
	return "Microsoft YaHei UI"
}

func uiFontBoldFace(fallback string) string {
	ensureEmbeddedFonts()
	for _, c := range []string{"MiSans Semibold", "MiSans SemiBold", "MiSans Medium", "MiSans"} {
		if faceExists(c) {
			return c
		}
	}
	return fallback
}

func createUIFont(heightPx, weight int32) syscall.Handle {
	face := uiFontFace()
	useWeight := weight
	if weight >= 600 {
		if boldFace := uiFontBoldFace(face); boldFace != face {
			face = boldFace
			useWeight = 400
		}
	}
	h, _, _ := procCreateFontW.Call(uintptr(-heightPx), 0, 0, 0, uintptr(useWeight), 0, 0, 0,
		1, 0, 0, 5, 0, uintptr(unsafe.Pointer(utf16Ptr(face))))
	return syscall.Handle(h)
}
