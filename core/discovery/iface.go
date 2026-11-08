package discovery

import (
	"fmt"
	"net"
	"sort"
	"strings"
)

// Iface is one IPv4-capable network interface considered for LAN discovery.
//
// Virtual marks TUN/TAP/VPN/hypervisor adapters. They are listed so that logs
// can explain what was skipped (D28) but never used for LAN discovery or
// direct connections.
type Iface struct {
	Name      string
	Index     int
	IP        net.IP
	Mask      net.IPMask
	Broadcast net.IP
	Virtual   bool
}

func (i Iface) String() string { return fmt.Sprintf("%s(%s)", i.Name, i.IP) }

// virtualHints are matched case-insensitively against the interface name.
// Windows names TUN adapters by product ("Meta", "Wintun", "Tailscale"), so a
// name match is the portable signal; the address check below catches the rest.
var virtualHints = []string{
	"tun", "tap", "vpn", "wireguard", "wintun", "tailscale", "zerotier",
	"nordlynx", "proton", "clash", "mihomo", "singbox", "sing-box", "meta",
	"hyper-v", "vethernet", "vmware", "virtualbox", "vbox", "wsl", "docker",
	"utun", "ppp", "loopback",
}

func isVirtualName(name string) bool {
	n := strings.ToLower(name)
	for _, h := range virtualHints {
		if strings.Contains(n, h) {
			return true
		}
	}
	return false
}

// isFakeIP reports whether ip is inside the TUN fake-ip range 198.18.0.0/15,
// which is what a transparent proxy hands out instead of real addresses.
func isFakeIP(ip net.IP) bool {
	ip4 := ip.To4()
	return ip4 != nil && ip4[0] == 198 && (ip4[1] == 18 || ip4[1] == 19)
}

// IsTunnelFakeIP reports whether ip falls in the 198.18.0.0/15 range that
// transparent-proxy VPN clients use for fake addresses. A connection whose
// local address lands in that range did not leave through the physical NIC.
func IsTunnelFakeIP(ip net.IP) bool { return isFakeIP(ip) }

func isLinkLocal(ip net.IP) bool {
	ip4 := ip.To4()
	return ip4 != nil && ip4[0] == 169 && ip4[1] == 254
}

func broadcastFor(ip net.IP, mask net.IPMask) net.IP {
	ip4 := ip.To4()
	if ip4 == nil || len(mask) != 4 {
		return nil
	}
	b := make(net.IP, 4)
	for i := 0; i < 4; i++ {
		b[i] = ip4[i] | ^mask[i]
	}
	return b
}

// Hint describes one interface supplied by the caller instead of enumerating
// the system. Android uses it because Go's netlink interface dump is denied to
// apps there ("route ip+net: netlinkrib: permission denied"); Java enumerates
// the Wi-Fi address and passes it across the gomobile bridge.
type Hint struct {
	Name   string `json:"name"`
	Index  int    `json:"index"`
	IP     string `json:"ip"`
	Prefix int    `json:"prefix"`
}

// iface converts the hint into an Iface, reporting whether it is usable.
func (h Hint) iface() (Iface, bool) {
	ip := net.ParseIP(h.IP).To4()
	if ip == nil || h.Prefix < 0 || h.Prefix > 32 {
		return Iface{}, false
	}
	mask := net.CIDRMask(h.Prefix, 32)
	if mask == nil {
		return Iface{}, false
	}
	return Iface{
		Name:      h.Name,
		Index:     h.Index,
		IP:        ip,
		Mask:      mask,
		Broadcast: broadcastFor(ip, mask),
	}, true
}

// Resolve exposes the hint as an Iface (used by candidate gathering).
func (h Hint) Resolve() (Iface, bool) { return h.iface() }

// Interfaces lists every up, non-loopback interface that has an IPv4 address.
func Interfaces() ([]Iface, error) {
	nifs, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("discovery: list interfaces: %w", err)
	}
	out := make([]Iface, 0, len(nifs))
	for _, ni := range nifs {
		if ni.Flags&net.FlagUp == 0 || ni.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ni.Addrs()
		if err != nil {
			continue
		}
		var ip4 net.IP
		var mask net.IPMask
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if v := ipn.IP.To4(); v != nil && len(ipn.Mask) == 4 {
				ip4, mask = v, ipn.Mask
				break
			}
		}
		if ip4 == nil {
			continue
		}
		out = append(out, Iface{
			Name:      ni.Name,
			Index:     ni.Index,
			IP:        ip4,
			Mask:      mask,
			Broadcast: broadcastFor(ip4, mask),
			Virtual:   isVirtualName(ni.Name) || isFakeIP(ip4),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out, nil
}

// Eligible returns the physical interfaces usable for LAN traffic.
func Eligible() ([]Iface, error) {
	all, err := Interfaces()
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, ifc := range all {
		if ifc.Virtual || isLinkLocal(ifc.IP) || ifc.Broadcast == nil {
			continue
		}
		out = append(out, ifc)
	}
	return out, nil
}
