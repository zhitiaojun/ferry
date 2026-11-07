//go:build windows

package discovery

import (
	"net"
	"os/exec"
	"strings"
)

// GatewayIPv4 returns the IPv4 default gateway from `route print -4`, or nil.
// It is used only for diagnostics (hotspot topology detection), never to route
// traffic.
func GatewayIPv4() net.IP {
	out, err := exec.Command("route", "print", "-4").Output()
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 3 && f[0] == "0.0.0.0" && f[1] == "0.0.0.0" {
			if ip := net.ParseIP(f[2]).To4(); ip != nil {
				return ip
			}
		}
	}
	return nil
}
