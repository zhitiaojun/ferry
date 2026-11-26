//go:build windows

package main

import (
	"syscall"
	"testing"
	"unsafe"
)

func resolvedTextFace(h syscall.Handle) string {
	dc, _, _ := procCompatDC.Call(0)
	if dc == 0 {
		return ""
	}
	defer procDeleteDC.Call(dc)
	old, _, _ := procSelectObject.Call(dc, uintptr(h))
	var buf [64]uint16
	n, _, _ := procTextFace.Call(dc, uintptr(len(buf)), uintptr(unsafe.Pointer(&buf[0])))
	procSelectObject.Call(dc, old)
	if n == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:n])
}

func TestEmbeddedMiSansFontLoads(t *testing.T) {
	n := ensureEmbeddedFonts()
	if n < 1 {
		t.Fatalf("expected >= 1 embedded font registered, got %d", n)
	}
	face := uiFontFace()
	boldFace := uiFontBoldFace(face)
	t.Logf("loaded=%d uiFontFace=%q uiFontBoldFace=%q", n, face, boldFace)
	if face != "MiSans" && face != "MiSans Regular" && face != "MiSans Normal" {
		t.Fatalf("expected MiSans family, got %q", face)
	}
	fReg := createUIFont(14, 400)
	if fReg == 0 {
		t.Fatal("createUIFont regular returned 0")
	}
	defer procDeleteObject.Call(uintptr(fReg))

	fBold := createUIFont(16, 600)
	if fBold == 0 {
		t.Fatal("createUIFont bold returned 0")
	}
	defer procDeleteObject.Call(uintptr(fBold))

	t.Logf("resolved regular=%q bold=%q", resolvedTextFace(fReg), resolvedTextFace(fBold))
}
