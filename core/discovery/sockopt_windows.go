//go:build windows

package discovery

import "syscall"

func setUDPSockOpts(fd uintptr) error {
	h := syscall.Handle(fd)
	if err := syscall.SetsockoptInt(h, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); err != nil {
		return err
	}
	return syscall.SetsockoptInt(h, syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
}
