package punch

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cwlindean/filetransfer/core/discovery"
)

func TestIsPublicIPv4(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"8.8.8.8", true},
		{"175.167.145.222", true},
		{"192.168.1.1", false},
		{"10.0.0.1", false},
		{"172.16.5.4", false},
		{"100.64.0.1", false},
		{"100.127.255.254", false},
		{"100.128.0.1", true},
		{"127.0.0.1", false},
		{"0.0.0.0", false},
	}
	for _, tc := range cases {
		if got := isPublicIPv4(net.ParseIP(tc.ip)); got != tc.want {
			t.Errorf("isPublicIPv4(%s) = %v, want %v", tc.ip, got, tc.want)
		}
	}
}

func TestTryNATPMPGatewayAndCleanup(t *testing.T) {
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	var unmapped atomic.Bool
	go func() {
		buf := make([]byte, 128)
		for {
			n, ra, err := c.ReadFromUDP(buf)
			if err != nil {
				return
			}
			if n >= 2 && buf[0] == 0 && buf[1] == 0 {
				var resp [12]byte
				resp[0] = 0
				resp[1] = 128
				copy(resp[8:12], []byte{198, 51, 100, 42})
				_, _ = c.WriteToUDP(resp[:], ra)
			} else if n >= 12 && buf[0] == 0 && buf[1] == 1 {
				life := binary.BigEndian.Uint32(buf[8:12])
				if life == 0 {
					unmapped.Store(true)
				}
				var resp [16]byte
				resp[0] = 0
				resp[1] = 129
				copy(resp[8:10], buf[4:6])
				binary.BigEndian.PutUint16(resp[10:12], 45678)
				copy(resp[12:16], buf[8:12])
				_, _ = c.WriteToUDP(resp[:], ra)
			}
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	addr, cl, ok := tryNATPMPGateway(ctx, c.LocalAddr().String(), 50001, net.IPv4(192, 168, 1, 20))
	if !ok {
		t.Fatal("tryNATPMPGateway failed")
	}
	if addr != "198.51.100.42:45678" {
		t.Fatalf("mapped addr = %q, want 198.51.100.42:45678", addr)
	}
	cl()
	deadline := time.Now().Add(time.Second)
	for !unmapped.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !unmapped.Load() {
		t.Fatal("cleanup did not send lifetime=0 unmap")
	}
}

func TestMapUPnPLocationSuccessAndRejectCGNAT(t *testing.T) {
	var externalIP string
	var addCalled, delCalled atomic.Bool

	mux := http.NewServeMux()
	mux.HandleFunc("/root.xml", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `<?xml version="1.0"?>
<root xmlns="urn:schemas-upnp-org:device-1-0">
  <device>
    <deviceType>urn:schemas-upnp-org:device:InternetGatewayDevice:1</deviceType>
    <deviceList>
      <device>
        <serviceList>
          <service>
            <serviceType>urn:schemas-upnp-org:service:WANIPConnection:1</serviceType>
            <controlURL>/ctl/IPConn</controlURL>
          </service>
        </serviceList>
      </device>
    </deviceList>
  </device>
</root>`)
	})
	mux.HandleFunc("/ctl/IPConn", func(w http.ResponseWriter, r *http.Request) {
		action := r.Header.Get("SOAPAction")
		switch {
		case strings.Contains(action, "AddPortMapping"):
			addCalled.Store(true)
			_, _ = io.WriteString(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:AddPortMappingResponse xmlns:u="urn:schemas-upnp-org:service:WANIPConnection:1"/></s:Body></s:Envelope>`)
		case strings.Contains(action, "GetExternalIPAddress"):
			_, _ = io.WriteString(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:GetExternalIPAddressResponse xmlns:u="urn:schemas-upnp-org:service:WANIPConnection:1"><NewExternalIPAddress>`+externalIP+`</NewExternalIPAddress></u:GetExternalIPAddressResponse></s:Body></s:Envelope>`)
		case strings.Contains(action, "DeletePortMapping"):
			delCalled.Store(true)
			_, _ = io.WriteString(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:DeletePortMappingResponse xmlns:u="urn:schemas-upnp-org:service:WANIPConnection:1"/></s:Body></s:Envelope>`)
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	ifaces := []discovery.Iface{{IP: net.IPv4(192, 168, 1, 10)}}

	// First test CGNAT IP rejection (must auto-delete mapping and return false).
	externalIP = "100.64.12.34"
	if _, _, ok := mapUPnPLocation(t.Context(), srv.URL+"/root.xml", 55123, ifaces); ok {
		t.Fatal("CGNAT external IP must be rejected")
	}
	if !delCalled.Load() {
		t.Fatal("expected DeletePortMapping cleanup on rejected CGNAT IP")
	}

	// Next test valid public IP.
	delCalled.Store(false)
	addCalled.Store(false)
	externalIP = "203.0.113.99"
	addr, cleanup, ok := mapUPnPLocation(t.Context(), srv.URL+"/root.xml", 55123, ifaces)
	if !ok {
		t.Fatal("mapUPnPLocation failed for public IP")
	}
	if !addCalled.Load() || addr != "203.0.113.99:55123" {
		t.Fatalf("got addr=%q addCalled=%v", addr, addCalled.Load())
	}
	cleanup()
	if !delCalled.Load() {
		t.Fatal("cleanup did not call DeletePortMapping")
	}
}
