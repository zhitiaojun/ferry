//go:build windows

package discovery

import (
	"net"
	"os/exec"
	"strings"
)

// GatewayIPv4 returns the first IPv4 default gateway from `route print -4`, or
// nil. Used only for diagnostics, never to route traffic.
func GatewayIPv4() net.IP {
	for _, row := range defaultRoutes() {
		return row[0]
	}
	return nil
}

// GatewayFor returns the default gateway of the local subnet that contains
// remote — the hotspot host when the peer is the phone running the hotspot.
// It returns nil when remote is not on a directly connected physical subnet.
func GatewayFor(remote net.IP) net.IP {
	if remote == nil {
		return nil
	}
	var local net.IP
	ifaces, err := Interfaces()
	if err == nil {
		for _, ifc := range ifaces {
			if ifc.Virtual || ifc.Mask == nil {
				continue
			}
			if (&net.IPNet{IP: ifc.IP, Mask: ifc.Mask}).Contains(remote) {
				local = ifc.IP
				break
			}
		}
	}
	if local == nil {
		return nil
	}
	for _, row := range defaultRoutes() {
		if row[1] != nil && row[1].Equal(local) {
			return row[0]
		}
	}
	return nil
}

// defaultRoutes returns {gateway, interface address} pairs for default routes.
func defaultRoutes() [][2]net.IP {
	out, err := exec.Command("route", "print", "-4").Output()
	if err != nil {
		return nil
	}
	var rows [][2]net.IP
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) < 4 || f[0] != "0.0.0.0" || f[1] != "0.0.0.0" {
			continue
		}
		gw := net.ParseIP(f[2]).To4()
		ifc := net.ParseIP(f[3]).To4()
		if gw == nil {
			continue
		}
		rows = append(rows, [2]net.IP{gw, ifc})
	}
	return rows
}
