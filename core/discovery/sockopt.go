package discovery

import (
	"context"
	"errors"
	"net"
	"syscall"
)

// listenUDP opens a UDP4 socket with SO_REUSEADDR (so several local test
// receivers can bind Port) and SO_BROADCAST (needed to send to a directed
// broadcast address on Windows).
func listenUDP(laddr *net.UDPAddr) (*net.UDPConn, error) {
	lc := net.ListenConfig{Control: func(network, address string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) {
			serr = setUDPSockOpts(fd)
		}); err != nil {
			return err
		}
		return serr
	}}
	pc, err := lc.ListenPacket(context.Background(), "udp4", laddr.String())
	if err != nil {
		return nil, err
	}
	conn, ok := pc.(*net.UDPConn)
	if !ok {
		_ = pc.Close()
		return nil, errors.New("discovery: unexpected packet conn type")
	}
	return conn, nil
}
