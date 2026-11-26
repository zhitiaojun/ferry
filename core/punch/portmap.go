package punch

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zhitiaojun/ferry/core/discovery"
)

const (
	defaultPortmapTimeout = 250 * time.Millisecond
	portmapLeaseSeconds   = 120
)

type portmapResult struct {
	addr    string
	cleanup func()
}

// mapPort attempts NAT-PMP/PCP and UPnP IGD concurrently within timeout,
// returning the first mapped public IPv4 endpoint and a best-effort unmap func.
func mapPort(ctx context.Context, localPort int, ifaces []discovery.Iface, timeout time.Duration) (string, func(), bool) {
	if localPort <= 0 || localPort > 65535 || len(ifaces) == 0 {
		return "", nil, false
	}
	if timeout <= 0 {
		timeout = defaultPortmapTimeout
	}
	mctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ch := make(chan portmapResult, 2)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		if addr, cl, ok := mapNATPMP(mctx, localPort, ifaces); ok {
			select {
			case ch <- portmapResult{addr: addr, cleanup: cl}:
			default:
				if cl != nil {
					cl()
				}
			}
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		if addr, cl, ok := mapUPnP(mctx, localPort, ifaces); ok {
			select {
			case ch <- portmapResult{addr: addr, cleanup: cl}:
			default:
				if cl != nil {
					cl()
				}
			}
		}
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case res := <-ch:
		cancel()
		go func() {
			<-done
			for {
				select {
				case extra := <-ch:
					if extra.cleanup != nil {
						extra.cleanup()
					}
				default:
					return
				}
			}
		}()
		return res.addr, res.cleanup, true
	case <-done:
		return "", nil, false
	case <-mctx.Done():
		return "", nil, false
	}
}

func isPublicIPv4(ip net.IP) bool {
	ip4 := ip.To4()
	if ip4 == nil {
		return false
	}
	if !ip4.IsGlobalUnicast() || ip4.IsPrivate() || ip4.IsLoopback() || ip4.IsMulticast() || ip4.IsLinkLocalUnicast() || ip4.IsLinkLocalMulticast() {
		return false
	}
	// Reject 0.0.0.0/8 and RFC 6598 CGNAT 100.64.0.0/10.
	if ip4[0] == 0 || (ip4[0] == 100 && ip4[1]&0xc0 == 64) {
		return false
	}
	return true
}

func candidateGateways(ifaces []discovery.Iface) []net.IP {
	seen := map[string]bool{}
	var gws []net.IP
	for _, ifc := range ifaces {
		ip4 := ifc.IP.To4()
		if ip4 == nil {
			continue
		}
		gw := net.IPv4(ip4[0], ip4[1], ip4[2], 1)
		s := gw.String()
		if !seen[s] {
			seen[s] = true
			gws = append(gws, gw)
		}
	}
	return gws
}

func mapNATPMP(ctx context.Context, localPort int, ifaces []discovery.Iface) (string, func(), bool) {
	for _, gw := range candidateGateways(ifaces) {
		if ctx.Err() != nil {
			return "", nil, false
		}
		if addr, cl, ok := tryNATPMPGateway(ctx, gw.String()+":5351", localPort, localClientForGateway(gw, ifaces)); ok {
			return addr, cl, true
		}
	}
	return "", nil, false
}

func localClientForGateway(gw net.IP, ifaces []discovery.Iface) net.IP {
	gw4 := gw.To4()
	for _, ifc := range ifaces {
		if ip4 := ifc.IP.To4(); ip4 != nil && gw4 != nil && ip4[0] == gw4[0] && ip4[1] == gw4[1] && ip4[2] == gw4[2] {
			return ip4
		}
	}
	for _, ifc := range ifaces {
		if ip4 := ifc.IP.To4(); ip4 != nil {
			return ip4
		}
	}
	return nil
}

func tryNATPMPGateway(ctx context.Context, gwAddr string, localPort int, localIP net.IP) (string, func(), bool) {
	raddr, err := net.ResolveUDPAddr("udp4", gwAddr)
	if err != nil {
		return "", nil, false
	}
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return "", nil, false
	}
	defer c.Close()

	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	} else {
		_ = c.SetDeadline(time.Now().Add(400 * time.Millisecond))
	}

	// Step 1: external address query [ver=0, op=0].
	if _, err := c.WriteToUDP([]byte{0, 0}, raddr); err != nil {
		return "", nil, false
	}
	buf := make([]byte, 128)
	n, _, err := c.ReadFromUDP(buf)
	if err != nil || n < 4 {
		return "", nil, false
	}
	// Version 2 means PCP rather than NAT-PMP.
	if buf[0] == 2 && localIP != nil {
		return tryPCPGateway(ctx, c, raddr, localPort, localIP)
	}
	if buf[0] != 0 || buf[1] != 128 || n < 12 || binary.BigEndian.Uint16(buf[2:4]) != 0 {
		return "", nil, false
	}
	extIP := net.IPv4(buf[8], buf[9], buf[10], buf[11])
	if !isPublicIPv4(extIP) {
		return "", nil, false
	}

	// Step 2: request UDP port mapping [ver=0, op=1, res=0, intPort, extPort, lifetime].
	var req [12]byte
	req[0] = 0
	req[1] = 1
	binary.BigEndian.PutUint16(req[4:6], uint16(localPort))
	binary.BigEndian.PutUint16(req[6:8], uint16(localPort))
	binary.BigEndian.PutUint32(req[8:12], portmapLeaseSeconds)
	if _, err := c.WriteToUDP(req[:], raddr); err != nil {
		return "", nil, false
	}
	n, _, err = c.ReadFromUDP(buf)
	if err != nil || n < 16 || buf[0] != 0 || buf[1] != 129 || binary.BigEndian.Uint16(buf[2:4]) != 0 {
		return "", nil, false
	}
	extPort := int(binary.BigEndian.Uint16(buf[10:12]))
	if extPort == 0 {
		return "", nil, false
	}

	cleanup := func() {
		uc, uerr := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
		if uerr != nil {
			return
		}
		defer uc.Close()
		var delReq [12]byte
		delReq[0] = 0
		delReq[1] = 1
		binary.BigEndian.PutUint16(delReq[4:6], uint16(localPort))
		_, _ = uc.WriteToUDP(delReq[:], raddr)
	}
	return net.JoinHostPort(extIP.String(), strconv.Itoa(extPort)), cleanup, true
}

func tryPCPGateway(ctx context.Context, c *net.UDPConn, raddr *net.UDPAddr, localPort int, localIP net.IP) (string, func(), bool) {
	ip4 := localIP.To4()
	if ip4 == nil {
		return "", nil, false
	}
	var nonce [12]byte
	_, _ = rand.Read(nonce[:])

	var req [60]byte
	req[0] = 2 // version 2
	req[1] = 1 // MAP opcode
	binary.BigEndian.PutUint32(req[4:8], portmapLeaseSeconds)
	req[18], req[19] = 0xff, 0xff
	copy(req[20:24], ip4)
	copy(req[24:36], nonce[:])
	req[36] = 17 // UDP
	binary.BigEndian.PutUint16(req[40:42], uint16(localPort))
	binary.BigEndian.PutUint16(req[42:44], uint16(localPort))

	if _, err := c.WriteToUDP(req[:], raddr); err != nil {
		return "", nil, false
	}
	buf := make([]byte, 128)
	n, _, err := c.ReadFromUDP(buf)
	if err != nil || n < 60 || buf[0] != 2 || buf[1] != 0x81 || buf[3] != 0 {
		return "", nil, false
	}
	extPort := int(binary.BigEndian.Uint16(buf[42:44]))
	extIP := net.IP(buf[44:60]).To4()
	if !isPublicIPv4(extIP) || extPort == 0 {
		return "", nil, false
	}
	cleanup := func() {
		uc, uerr := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
		if uerr != nil {
			return
		}
		defer uc.Close()
		var delReq [60]byte
		copy(delReq[:], req[:])
		binary.BigEndian.PutUint32(delReq[4:8], 0)
		_, _ = uc.WriteToUDP(delReq[:], raddr)
	}
	return net.JoinHostPort(extIP.String(), strconv.Itoa(extPort)), cleanup, true
}

func mapUPnP(ctx context.Context, localPort int, ifaces []discovery.Iface) (string, func(), bool) {
	loc, err := discoverIGDLocation(ctx)
	if err != nil {
		return "", nil, false
	}
	return mapUPnPLocation(ctx, loc, localPort, ifaces)
}

func mapUPnPLocation(ctx context.Context, loc string, localPort int, ifaces []discovery.Iface) (string, func(), bool) {
	ctrlURL, serviceType, err := findIGDControlURL(ctx, loc)
	if err != nil {
		return "", nil, false
	}
	u, err := url.Parse(ctrlURL)
	if err != nil {
		return "", nil, false
	}
	localClient := resolveLocalClientForHost(u.Hostname(), ifaces)
	if localClient == "" {
		return "", nil, false
	}
	if err := upnpAddPortMapping(ctx, ctrlURL, serviceType, localPort, localClient); err != nil {
		return "", nil, false
	}
	extIPStr, err := upnpGetExternalIP(ctx, ctrlURL, serviceType)
	if err != nil || !isPublicIPv4(net.ParseIP(extIPStr)) {
		upnpDeletePortMapping(ctrlURL, serviceType, localPort)
		return "", nil, false
	}
	cleanup := func() {
		upnpDeletePortMapping(ctrlURL, serviceType, localPort)
	}
	return net.JoinHostPort(extIPStr, strconv.Itoa(localPort)), cleanup, true
}

func discoverIGDLocation(ctx context.Context) (string, error) {
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return "", err
	}
	defer c.Close()

	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	} else {
		_ = c.SetDeadline(time.Now().Add(400 * time.Millisecond))
	}

	mcast, err := net.ResolveUDPAddr("udp4", "239.255.255.250:1900")
	if err != nil {
		return "", err
	}
	targets := []string{
		"urn:schemas-upnp-org:device:InternetGatewayDevice:1",
		"urn:schemas-upnp-org:device:InternetGatewayDevice:2",
		"urn:schemas-upnp-org:service:WANIPConnection:1",
	}
	for _, st := range targets {
		msg := "M-SEARCH * HTTP/1.1\r\n" +
			"HOST: 239.255.255.250:1900\r\n" +
			"MAN: \"ssdp:discover\"\r\n" +
			"MX: 1\r\n" +
			"ST: " + st + "\r\n\r\n"
		_, _ = c.WriteToUDP([]byte(msg), mcast)
	}

	buf := make([]byte, 2048)
	for {
		n, _, err := c.ReadFromUDP(buf)
		if err != nil {
			return "", err
		}
		if loc := parseSSDPLocation(string(buf[:n])); loc != "" {
			return loc, nil
		}
	}
}

func parseSSDPLocation(resp string) string {
	for _, line := range strings.Split(resp, "\n") {
		line = strings.TrimSpace(line)
		if len(line) > 9 && strings.EqualFold(line[:9], "LOCATION:") {
			return strings.TrimSpace(line[9:])
		}
	}
	return ""
}

func findIGDControlURL(ctx context.Context, loc string) (controlURL, serviceType string, err error) {
	baseURL, err := url.Parse(loc)
	if err != nil {
		return "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, loc, nil)
	if err != nil {
		return "", "", err
	}
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("upnp description status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 128<<10))
	if err != nil {
		return "", "", err
	}

	type service struct {
		ServiceType string `xml:"serviceType"`
		ControlURL  string `xml:"controlURL"`
	}
	dec := xml.NewDecoder(bytes.NewReader(raw))
	var bestCtrl, bestType string
	for {
		tok, derr := dec.Token()
		if derr == io.EOF {
			break
		}
		if derr != nil {
			return "", "", derr
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "service" {
			continue
		}
		var svc service
		if decErr := dec.DecodeElement(&svc, &se); decErr != nil {
			continue
		}
		switch svc.ServiceType {
		case "urn:schemas-upnp-org:service:WANIPConnection:1",
			"urn:schemas-upnp-org:service:WANIPConnection:2",
			"urn:schemas-upnp-org:service:WANPPPConnection:1":
			bestCtrl = strings.TrimSpace(svc.ControlURL)
			bestType = strings.TrimSpace(svc.ServiceType)
		}
		if bestCtrl != "" {
			break
		}
	}
	if bestCtrl == "" {
		return "", "", fmt.Errorf("no WANIPConnection/WANPPPConnection service")
	}
	ref, err := url.Parse(bestCtrl)
	if err != nil {
		return "", "", err
	}
	return baseURL.ResolveReference(ref).String(), bestType, nil
}

func resolveLocalClientForHost(host string, ifaces []discovery.Iface) string {
	ip := net.ParseIP(host).To4()
	if ip != nil {
		for _, ifc := range ifaces {
			if ip4 := ifc.IP.To4(); ip4 != nil && ip4[0] == ip[0] && ip4[1] == ip[1] && ip4[2] == ip[2] {
				return ip4.String()
			}
		}
	}
	if conn, err := net.DialTimeout("udp4", net.JoinHostPort(host, "1900"), 150*time.Millisecond); err == nil {
		defer conn.Close()
		if la, ok := conn.LocalAddr().(*net.UDPAddr); ok && la.IP.To4() != nil && !la.IP.IsLoopback() {
			return la.IP.To4().String()
		}
	}
	for _, ifc := range ifaces {
		if ip4 := ifc.IP.To4(); ip4 != nil {
			return ip4.String()
		}
	}
	return ""
}

func soapCall(ctx context.Context, ctrlURL, serviceType, action, innerXML string) ([]byte, error) {
	body := `<?xml version="1.0"?>` +
		`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">` +
		`<s:Body>` +
		`<u:` + action + ` xmlns:u="` + serviceType + `">` + innerXML + `</u:` + action + `>` +
		`</s:Body></s:Envelope>`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ctrlURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPAction", `"`+serviceType+`#`+action+`"`)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<10))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("soap %s status %d", action, resp.StatusCode)
	}
	return raw, nil
}

func upnpAddPortMapping(ctx context.Context, ctrlURL, serviceType string, port int, localIP string) error {
	p := strconv.Itoa(port)
	inner := `<NewRemoteHost></NewRemoteHost>` +
		`<NewExternalPort>` + p + `</NewExternalPort>` +
		`<NewProtocol>UDP</NewProtocol>` +
		`<NewInternalPort>` + p + `</NewInternalPort>` +
		`<NewInternalClient>` + localIP + `</NewInternalClient>` +
		`<NewEnabled>1</NewEnabled>` +
		`<NewPortMappingDescription>Ferry</NewPortMappingDescription>` +
		`<NewLeaseDuration>` + strconv.Itoa(portmapLeaseSeconds) + `</NewLeaseDuration>`
	_, err := soapCall(ctx, ctrlURL, serviceType, "AddPortMapping", inner)
	return err
}

func upnpGetExternalIP(ctx context.Context, ctrlURL, serviceType string) (string, error) {
	raw, err := soapCall(ctx, ctrlURL, serviceType, "GetExternalIPAddress", "")
	if err != nil {
		return "", err
	}
	dec := xml.NewDecoder(bytes.NewReader(raw))
	for {
		tok, derr := dec.Token()
		if derr != nil {
			break
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "NewExternalIPAddress" {
			continue
		}
		var ipStr string
		if dec.DecodeElement(&ipStr, &se) == nil {
			return strings.TrimSpace(ipStr), nil
		}
	}
	return "", fmt.Errorf("missing NewExternalIPAddress")
}

func upnpDeletePortMapping(ctrlURL, serviceType string, port int) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	p := strconv.Itoa(port)
	inner := `<NewRemoteHost></NewRemoteHost><NewExternalPort>` + p + `</NewExternalPort><NewProtocol>UDP</NewProtocol>`
	_, _ = soapCall(ctx, ctrlURL, serviceType, "DeletePortMapping", inner)
}
