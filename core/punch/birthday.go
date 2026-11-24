package punch

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	mrand "math/rand/v2"
	"net"
	"sync"
	"time"

	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/logging"
	"github.com/cwlindean/filetransfer/core/pathmgr"
	"github.com/cwlindean/filetransfer/core/session"
)

const (
	bpMagic            = "FTBP"
	bpPacketLen        = 29 // 4 magic + 1 phase + 16 tag + 8 nonce
	bpPhasePing  byte  = 1
	bpPhasePong  byte  = 2
	bpPhaseConf  byte  = 3
	bpDefaultSockets  = 128
	bpMaxSockets      = 192
	bpDefaultWaves    = 3
	bpDefaultProbes   = 1024
	bpDefaultInterval = 400 * time.Millisecond
	bpDefaultPortMin  = 1024
	bpDefaultPortMax   = 65535
)

// BirthdayConfig tunes symmetric NAT birthday-paradox UDP hole punching.
type BirthdayConfig struct {
	SelfID         [32]byte
	PeerID         [32]byte
	SessionID      string
	PeerCandidates []Candidate
	LocalMapping   Behavior
	NumSockets     int
	Waves          int
	ProbesPerWave  int
	WaveInterval   time.Duration
	PortMin         int
	PortMax         int
	BindInPortRange bool
	Log             *logging.Logger
}

func (c BirthdayConfig) numSockets() int {
	n := c.NumSockets
	if n <= 0 {
		n = bpDefaultSockets
	}
	if n < 4 {
		n = 4
	}
	if n > bpMaxSockets {
		n = bpMaxSockets
	}
	return n
}

func (c BirthdayConfig) waves() int {
	if c.Waves <= 0 {
		return bpDefaultWaves
	}
	return c.Waves
}

func (c BirthdayConfig) probesPerWave() int {
	if c.ProbesPerWave <= 0 {
		return bpDefaultProbes
	}
	return c.ProbesPerWave
}

func (c BirthdayConfig) waveInterval() time.Duration {
	if c.WaveInterval <= 0 {
		return bpDefaultInterval
	}
	return c.WaveInterval
}

func (c BirthdayConfig) portRange() (int, int) {
	lo, hi := c.PortMin, c.PortMax
	if lo <= 0 {
		lo = bpDefaultPortMin
	}
	if hi <= 0 || hi > 65535 {
		hi = bpDefaultPortMax
	}
	if hi < lo {
		hi = lo
	}
	return lo, hi
}

// BirthdayPunch opens multiple local UDP sockets and probes the peer's public
// IP(s) across observed-nearby and random ports to collide through symmetric
// NATs (or TUN user-space NATs). On success, all losing sockets are closed and
// the winning socket + verified remote UDPAddr are returned ready for QUIC.
func BirthdayPunch(ctx context.Context, cfg BirthdayConfig) (*net.UDPConn, *net.UDPAddr, error) {
	ips, hintPorts := extractBirthdayTargets(cfg.PeerCandidates)
	if len(ips) == 0 {
		return nil, nil, fmt.Errorf("birthday punch: no peer IPv4 target")
	}

	tag := birthdayTag(cfg.SelfID, cfg.PeerID, cfg.SessionID)
	isLeader := bytes.Compare(cfg.SelfID[:], cfg.PeerID[:]) < 0

	socks, err := openBirthdaySockets(cfg.numSockets(), cfg)
	if err != nil {
		return nil, nil, err
	}
	closeAll := func(skip int) {
		for i, s := range socks {
			if i == skip || s == nil {
				continue
			}
			_ = s.Close()
		}
	}

	pctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type winInfo struct {
		idx   int
		raddr *net.UDPAddr
	}
	winCh := make(chan winInfo, 1)
	var winOnce sync.Once
	var wg sync.WaitGroup

	var nonce [8]byte
	_, _ = rand.Read(nonce[:])

	// Start readers on every ephemeral socket.
	for i, sock := range socks {
		wg.Add(1)
		go func(idx int, c *net.UDPConn) {
			defer wg.Done()
			var seenPong sync.Map
			buf := make([]byte, 128)
			for {
				nr, ra, rerr := c.ReadFromUDP(buf)
				if rerr != nil {
					return
				}
				phase, ok := parseBirthdayPacket(buf[:nr], tag, nonce)
				if !ok {
					continue
				}
				switch phase {
				case bpPhasePing:
					pong := buildBirthdayPacket(bpPhasePong, tag, nonce)
					_, _ = c.WriteToUDP(pong, ra)
					_, _ = c.WriteToUDP(pong, ra)
				case bpPhasePong:
					if isLeader {
						winOnce.Do(func() {
							conf := buildBirthdayPacket(bpPhaseConf, tag, nonce)
							for k := 0; k < 3; k++ {
								_, _ = c.WriteToUDP(conf, ra)
								if k < 2 {
									time.Sleep(10 * time.Millisecond)
								}
							}
							winCh <- winInfo{idx: idx, raddr: cloneUDPAddr(ra)}
						})
					} else {
						if _, dup := seenPong.LoadOrStore(ra.String(), true); !dup {
							pong := buildBirthdayPacket(bpPhasePong, tag, nonce)
							_, _ = c.WriteToUDP(pong, ra)
						}
					}
				case bpPhaseConf:
					if !isLeader {
						winOnce.Do(func() {
							winCh <- winInfo{idx: idx, raddr: cloneUDPAddr(ra)}
						})
					}
				}
			}
		}(i, sock)
	}

	// Probe sender goroutine.
	wg.Add(1)
	go func() {
		defer wg.Done()
		sendBirthdayWaves(pctx, socks, ips, hintPorts, tag, nonce, cfg)
	}()

	select {
	case w := <-winCh:
		cancel()
		closeAll(w.idx)
		winner := socks[w.idx]
		_ = winner.SetReadDeadline(time.Now())
		wg.Wait()
		_ = winner.SetReadDeadline(time.Time{})
		if cfg.Log != nil {
			cfg.Log.Event("punch.birthday_hit", map[string]any{
				"local":   winner.LocalAddr().String(),
				"remote":  w.raddr.String(),
				"sockets": len(socks),
				"leader":  isLeader,
			})
		}
		return winner, w.raddr, nil
	case <-ctx.Done():
		cancel()
		closeAll(-1)
		wg.Wait()
		return nil, nil, fmt.Errorf("birthday punch: %w", ctx.Err())
	}
}

// BirthdayConnect establishes a verified TLS 1.3 + session over a QUIC tunnel
// built on the UDP pair discovered by BirthdayPunch.
func BirthdayConnect(
	ctx context.Context,
	cfg BirthdayConfig,
	key *identity.Key,
	peerPub ed25519.PublicKey,
	params session.Params,
) (*session.Session, func(), error) {
	udpConn, raddr, err := BirthdayPunch(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}

	isLeader := bytes.Compare(cfg.SelfID[:], cfg.PeerID[:]) < 0
	if isLeader {
		tcfg, err := identity.ClientTLS(key, peerPub)
		if err != nil {
			_ = udpConn.Close()
			return nil, nil, err
		}
		q := pathmgr.NewQUIC(udpConn, tcfg)
		conn, err := q.DialAddr(ctx, raddr.String())
		if err != nil {
			_ = q.Close()
			_ = udpConn.Close()
			return nil, nil, fmt.Errorf("birthday quic dial: %w", err)
		}
		sess, err := session.Dial(conn, params)
		if err != nil {
			_ = conn.Close()
			_ = q.Close()
			_ = udpConn.Close()
			return nil, nil, fmt.Errorf("birthday session dial: %w", err)
		}
		cleanup := func() {
			_ = q.Close()
			_ = udpConn.Close()
		}
		return sess, cleanup, nil
	}

	tcfg, err := identity.ServerTLS(key, peerPub)
	if err != nil {
		_ = udpConn.Close()
		return nil, nil, err
	}
	q := pathmgr.NewQUIC(udpConn, tcfg)
	if err := q.PrepareListen(ctx); err != nil {
		_ = q.Close()
		_ = udpConn.Close()
		return nil, nil, err
	}
	conn, err := q.Accept(ctx)
	if err != nil {
		_ = q.Close()
		_ = udpConn.Close()
		return nil, nil, fmt.Errorf("birthday quic accept: %w", err)
	}
	sess, err := session.Accept(conn, params)
	if err != nil {
		_ = conn.Close()
		_ = q.Close()
		_ = udpConn.Close()
		return nil, nil, fmt.Errorf("birthday session accept: %w", err)
	}
	cleanup := func() {
		_ = q.Close()
		_ = udpConn.Close()
	}
	return sess, cleanup, nil
}

func sendBirthdayWaves(
	ctx context.Context,
	socks []*net.UDPConn,
	ips []net.IP,
	hintPorts []int,
	tag [16]byte,
	nonce [8]byte,
	cfg BirthdayConfig,
) {
	ping := buildBirthdayPacket(bpPhasePing, tag, nonce)
	lo, hi := cfg.portRange()
	span := hi - lo + 1
	waves := cfg.waves()
	probesPerWave := cfg.probesPerWave()
	interval := cfg.waveInterval()

	// Precompute observed + sequential symmetric nearby ports.
	var seqPorts []int
	seenPort := map[int]bool{}
	addPort := func(p int) {
		if p < 1 || p > 65535 || seenPort[p] {
			return
		}
		seenPort[p] = true
		seqPorts = append(seqPorts, p)
	}
	for _, p := range hintPorts {
		addPort(p)
		for delta := 1; delta <= 32; delta++ {
			addPort(p + delta)
			addPort(p - delta)
		}
	}

	// Pace at ~1.5 ms per probe (~660 pps, ~19 KB/s) so routers/TUN do not burst-drop.
	pace := 1500 * time.Microsecond

	for wave := 0; wave < waves; wave++ {
		if ctx.Err() != nil {
			return
		}
		sent := 0
		if wave == 0 && len(seqPorts) > 0 {
			for i, port := range seqPorts {
				if ctx.Err() != nil {
					return
				}
				ip := ips[i%len(ips)]
				sock := socks[i%len(socks)]
				_, _ = sock.WriteToUDP(ping, &net.UDPAddr{IP: ip, Port: port})
				sent++
				select {
				case <-ctx.Done():
					return
				case <-time.After(pace):
				}
			}
		}

		for sent < probesPerWave {
			if ctx.Err() != nil {
				return
			}
			ip := ips[mrand.IntN(len(ips))]
			port := lo + mrand.IntN(span)
			sock := socks[mrand.IntN(len(socks))]
			_, _ = sock.WriteToUDP(ping, &net.UDPAddr{IP: ip, Port: port})
			sent++
			select {
			case <-ctx.Done():
				return
			case <-time.After(pace):
			}
		}

		if wave < waves-1 {
			t := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-t.C:
			}
		}
	}
}

func extractBirthdayTargets(candidates []Candidate) (ips []net.IP, ports []int) {
	var pubIPs, allIPs []net.IP
	seenPub := map[string]bool{}
	seenAll := map[string]bool{}
	seenPort := map[int]bool{}

	for _, c := range candidates {
		if c.Kind == "ipv6" {
			continue
		}
		addr, err := net.ResolveUDPAddr("udp4", c.Addr)
		if err != nil || addr.IP == nil {
			continue
		}
		ip4 := addr.IP.To4()
		if ip4 == nil || ip4.IsUnspecified() || ip4.IsMulticast() {
			continue
		}
		key := ip4.String()
		if isPublicIPv4(ip4) && !seenPub[key] {
			seenPub[key] = true
			pubIPs = append(pubIPs, ip4)
		}
		if !seenAll[key] {
			seenAll[key] = true
			allIPs = append(allIPs, ip4)
		}
		if addr.Port > 0 && !seenPort[addr.Port] {
			seenPort[addr.Port] = true
			ports = append(ports, addr.Port)
		}
	}
	if len(pubIPs) > 0 {
		return pubIPs, ports
	}
	return allIPs, ports
}

func birthdayTag(a, b [32]byte, sessionID string) [16]byte {
	lo, hi := a, b
	if bytes.Compare(lo[:], hi[:]) > 0 {
		lo, hi = hi, lo
	}
	h := sha256.New()
	h.Write([]byte("ferry-bp-v1"))
	h.Write(lo[:])
	h.Write(hi[:])
	h.Write([]byte(sessionID))
	var out [16]byte
	copy(out[:], h.Sum(nil))
	return out
}

func buildBirthdayPacket(phase byte, tag [16]byte, nonce [8]byte) []byte {
	pkt := make([]byte, bpPacketLen)
	copy(pkt[0:4], bpMagic)
	pkt[4] = phase
	copy(pkt[5:21], tag[:])
	copy(pkt[21:29], nonce[:])
	return pkt
}

func parseBirthdayPacket(b []byte, wantTag [16]byte, ownNonce [8]byte) (byte, bool) {
	if len(b) != bpPacketLen || string(b[0:4]) != bpMagic {
		return 0, false
	}
	phase := b[4]
	if phase != bpPhasePing && phase != bpPhasePong && phase != bpPhaseConf {
		return 0, false
	}
	if !bytes.Equal(b[5:21], wantTag[:]) {
		return 0, false
	}
	if bytes.Equal(b[21:29], ownNonce[:]) {
		return 0, false
	}
	return phase, true
}

func cloneUDPAddr(a *net.UDPAddr) *net.UDPAddr {
	if a == nil {
		return nil
	}
	ip := make(net.IP, len(a.IP))
	copy(ip, a.IP)
	return &net.UDPAddr{IP: ip, Port: a.Port, Zone: a.Zone}
}

func openBirthdaySockets(n int, cfg BirthdayConfig) ([]*net.UDPConn, error) {
	socks := make([]*net.UDPConn, 0, n)
	closeSocks := func() {
		for _, s := range socks {
			if s != nil {
				_ = s.Close()
			}
		}
	}
	if cfg.BindInPortRange && cfg.PortMin > 0 && cfg.PortMax > cfg.PortMin {
		lo, hi := cfg.portRange()
		ports := make([]int, 0, hi-lo+1)
		for p := lo; p <= hi; p++ {
			ports = append(ports, p)
		}
		mrand.Shuffle(len(ports), func(i, j int) { ports[i], ports[j] = ports[j], ports[i] })
		for _, p := range ports {
			if len(socks) >= n {
				break
			}
			c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: p})
			if err != nil {
				continue
			}
			socks = append(socks, c)
		}
		if len(socks) < 4 {
			closeSocks()
			return nil, fmt.Errorf("birthday punch: failed to bind enough sockets in [%d,%d]", lo, hi)
		}
		return socks, nil
	}
	for i := 0; i < n; i++ {
		c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
		if err != nil {
			if len(socks) >= 4 {
				break
			}
			closeSocks()
			return nil, fmt.Errorf("birthday punch: open udp sockets: %w", err)
		}
		socks = append(socks, c)
	}
	return socks, nil
}
