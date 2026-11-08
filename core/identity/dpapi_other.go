//go:build !windows

package identity

// sealAvailable is false off Windows: the key file relies on its 0600 mode.
func sealAvailable() bool { return false }
