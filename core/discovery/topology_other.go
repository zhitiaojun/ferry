//go:build !windows

package discovery

import "net"

// GatewayIPv4 is implemented for Windows diagnostics only (see
// topology_windows.go); elsewhere hotspot detection falls back to "unknown".
func GatewayIPv4() net.IP { return nil }

// GatewayFor reports nil off Windows: hotspot topology is only diagnosed on
// the development host.
func GatewayFor(remote net.IP) net.IP { return nil }
