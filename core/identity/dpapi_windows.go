//go:build windows

package identity

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	crypt32         = windows.NewLazySystemDLL("crypt32.dll")
	procProtectData = crypt32.NewProc("CryptProtectData")
	procUnprotect   = crypt32.NewProc("CryptUnprotectData")
	kernel32        = windows.NewLazySystemDLL("kernel32.dll")
	procLocalFree   = kernel32.NewProc("LocalFree")
)

// dataBlob mirrors the Win32 DATA_BLOB structure.
type dataBlob struct {
	size uint32
	data *byte
}

// dpapiProtect seals data for the current Windows user (DPAPI, user scope).
// The ciphertext can only be read by the same user on the same machine.
func dpapiProtect(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("dpapi: empty input")
	}
	in := dataBlob{size: uint32(len(data)), data: &data[0]}
	var out dataBlob
	r, _, err := procProtectData.Call(
		uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0, 0,
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return nil, fmt.Errorf("dpapi: CryptProtectData: %w", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.data)))
	sealed := make([]byte, out.size)
	copy(sealed, unsafe.Slice(out.data, out.size))
	return sealed, nil
}

// dpapiUnprotect reverses dpapiProtect.
func dpapiUnprotect(sealed []byte) ([]byte, error) {
	if len(sealed) == 0 {
		return nil, fmt.Errorf("dpapi: empty input")
	}
	in := dataBlob{size: uint32(len(sealed)), data: &sealed[0]}
	var out dataBlob
	r, _, err := procUnprotect.Call(
		uintptr(unsafe.Pointer(&in)), 0, 0, 0, 0, 0,
		uintptr(unsafe.Pointer(&out)),
	)
	if r == 0 {
		return nil, fmt.Errorf("dpapi: CryptUnprotectData: %w", err)
	}
	defer procLocalFree.Call(uintptr(unsafe.Pointer(out.data)))
	plain := make([]byte, out.size)
	copy(plain, unsafe.Slice(out.data, out.size))
	return plain, nil
}

// sealAvailable reports whether the platform can protect the key at rest.
func sealAvailable() bool { return true }
