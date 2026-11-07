//go:build !windows

package discovery

import "net"

// GatewayIPv4 is implemented for Windows diagnostics only (see
// topology_windows.go); elsewhere hotspot detection falls back to "unknown".
func GatewayIPv4() net.IP { return nil }
