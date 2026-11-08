//go:build !windows

package identity

import "errors"

var errSealUnsupported = errors.New("identity: platform key sealing unavailable")

// sealAvailable is false off Windows: the key file relies on its 0600 mode.
func sealAvailable() bool { return false }

func dpapiProtect(data []byte) ([]byte, error)   { return nil, errSealUnsupported }
func dpapiUnprotect(data []byte) ([]byte, error) { return nil, errSealUnsupported }
