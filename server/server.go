// Package server implements ft-server: the signaling and relay endpoint that
// runs on BJ behind openresty (protocol §8). It keeps all state in memory and
// holds no pairing data.
package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/logging"
	"github.com/cwlindean/filetransfer/core/signaling"
)

// buildVersion is stamped by the release build (-ldflags -X).
var buildVersion = "0.8.0-dev"

// Limits are the resource bounds for one server process.
type Limits struct {
	MaxPeers      int           // concurrent logged-in devices
	MaxRelayRooms int           // concurrent relay sessions
	RelayIdle     time.Duration // drop a spliced relay after this much silence
	LoginTTL      time.Duration // time allowed between challenge and login
	PingIdle      time.Duration // close a signaling socket after this much silence

	RelayRate int64 // bytes/second per relay room (0 = unlimited)

	LoginBurst  int           // login attempts per window, per source IP
	LoginWindow time.Duration // window length for LoginBurst

	PairBurst  int           // pairing messages allowed per window, per peer
	PairWindow time.Duration // window length for PairBurst
}

// DefaultLimits are sized for the 256 MB BJ quota (D9): the signaling state is
// a few hundred bytes per peer, and a relay room holds two sockets only.
func DefaultLimits() Limits {
	return Limits{
		MaxPeers:      512,
		MaxRelayRooms: 50,
		RelayIdle:     60 * time.Second,
		LoginTTL:      30 * time.Second,
		PingIdle:      90 * time.Second,
		RelayRate:     22 << 20,
		LoginBurst:    10,
		LoginWindow:   time.Minute,
		PairBurst:     30,
		PairWindow:    time.Minute,
	}
}

// Server is the in-memory signaling and relay hub.
type Server struct {
	limits Limits
	log    *logging.Logger
	web    *Web
	start  time.Time

	relayBytes atomic.Int64 // bytes spliced through relay rooms since start

	mu     sync.Mutex
	peers  map[string]*peer // device id hex → connection
	rooms  map[string]*room // relay token → room
	nextID uint64

	loginMu     sync.Mutex
	loginLimits map[string]*loginBucket
}

type peer struct {
	id   string
	pub  []byte
	conn *websocket.Conn
	wmu  sync.Mutex // serialises writes to conn

	// Pairing budget: pairing messages are cheap to forward but easy to
	// abuse as a relay for arbitrary traffic, so each peer gets a small
	// fixed window.
	pairMu     sync.Mutex
	pairCount  int
	pairWindow time.Time
}

func (p *peer) send(ctx context.Context, e *signaling.Envelope) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	p.wmu.Lock()
	defer p.wmu.Unlock()
	return p.conn.Write(ctx, websocket.MessageText, raw)
}

// loginBucket counts unauthenticated login attempts from one source address.
type loginBucket struct {
	window time.Time
	count  int
}

type room struct {
	token    string
	transfer string
	created  time.Time

	mu      sync.Mutex
	conns   [2]*websocket.Conn
	readyCh chan struct{}
	done    chan struct{}
	once    sync.Once

	log     *logging.Logger
	bytesMu sync.Mutex
	bytes   [2]int64
	onBytes func(int) // optional hook: total relay bytes for /metrics

	// Per-room bandwidth budget (bytes/second). Exceeding it parks the pump
	// until the next second instead of buffering.
	rate       int64
	rateMu     sync.Mutex
	rateWindow time.Time
	rateBytes  int64
}

// addBytes records how much data one side pushed through the splice. It backs
// the R7 check: once a direct path takes over, these numbers stop growing.
func (r *room) addBytes(side int, n int) {
	r.bytesMu.Lock()
	r.bytes[side] += int64(n)
	r.bytesMu.Unlock()
	if r.onBytes != nil {
		r.onBytes(n)
	}
}

func (r *room) byteCounts() (int64, int64) {
	r.bytesMu.Lock()
	defer r.bytesMu.Unlock()
	return r.bytes[0], r.bytes[1]
}

// New returns a server with default limits.
func New(log *logging.Logger) *Server {
	return &Server{
		limits:      DefaultLimits(),
		log:         log,
		web:         NewWeb(os.Getenv("FT_WEB_PASSWORD"), DefaultWebLimits(), log),
		start:       time.Now(),
		peers:       make(map[string]*peer),
		rooms:       make(map[string]*room),
		loginLimits: make(map[string]*loginBucket),
	}
}

// Handler returns the HTTP handler serving /signal, /relay and /healthz.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/signal", s.handleSignal)
	mux.HandleFunc("/relay", s.handleRelay)
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/metrics", s.handleMetrics)
	if s.web != nil {
		s.web.registerRoutes(mux)
	}
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	peers, rooms := len(s.peers), len(s.rooms)
	s.mu.Unlock()
	fmt.Fprintf(w, "ok uptime=%s peers=%d relay_rooms=%d\n",
		time.Since(s.start).Round(time.Second), peers, rooms)
}

// handleMetrics exposes a small JSON snapshot for monitoring: liveness,
// online peers, relay rooms and relay bytes. It is intentionally unauthenticated
// but bound behind the site's private path (openresty only proxies /healthz and
// /metrics to loopback).
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	peers, rooms := len(s.peers), len(s.rooms)
	s.mu.Unlock()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"ok":          true,
		"version":     buildVersion,
		"uptime_s":    int64(time.Since(s.start).Seconds()),
		"peers":       peers,
		"relay_rooms": rooms,
		"relay_bytes": s.relayBytes.Load(),
		"goroutines":  runtime.NumGoroutine(),
		"heap_mb":     float64(ms.HeapAlloc) / (1 << 20),
		"sys_mb":      float64(ms.Sys) / (1 << 20),
		"num_gc":      ms.NumGC,
	})
}

// accept upgrades an HTTP request to a WebSocket with the project's limits.
func (s *Server) accept(w http.ResponseWriter, r *http.Request) (*websocket.Conn, error) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: []string{"ferry.cwlindean.cn", "ft.cwlindean.cn", "localhost", "127.0.0.1"},
	})
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(signaling.MaxMessage)
	return conn, nil
}

func (s *Server) handleSignal(w http.ResponseWriter, r *http.Request) {
	conn, err := s.accept(w, r)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	ctx := r.Context()

	// Unauthenticated login attempts are the cheapest thing to flood, so cap
	// them per source address before touching the handshake.
	if !s.allowLogin(clientIP(r)) {
		http.Error(w, "too many login attempts", http.StatusTooManyRequests)
		conn.Close(websocket.StatusPolicyViolation, "rate limited")
		return
	}

	p, err := s.login(ctx, conn)
	if err != nil {
		s.writeError(ctx, conn, "", signaling.ErrAuthFailed, err.Error())
		conn.Close(websocket.StatusPolicyViolation, "login failed")
		return
	}

	if err := s.register(p); err != nil {
		s.writeError(ctx, conn, "", signaling.ErrBadRequest, err.Error())
		conn.Close(websocket.StatusPolicyViolation, err.Error())
		return
	}
	defer s.unregister(p)

	s.sendLoginOK(ctx, p)
	s.broadcastPresence(p.id, true)

	p.conn = conn
	for {
		conn.SetReadLimit(signaling.MaxMessage)
		typ, raw, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			continue
		}
		e, err := signaling.Parse(raw)
		if err != nil {
			s.writeError(ctx, conn, "", signaling.ErrBadEnvelope, err.Error())
			continue
		}
		s.route(ctx, p, e)
	}
}

// login performs the challenge/response handshake and returns the peer.
func (s *Server) login(ctx context.Context, conn *websocket.Conn) (*peer, error) {
	ctx, cancel := context.WithTimeout(ctx, s.limits.LoginTTL)
	defer cancel()

	begin, err := s.readType(ctx, conn, signaling.TypeLoginBegin)
	if err != nil {
		return nil, err
	}
	b, err := signaling.Decode[signaling.LoginBegin](begin)
	if err != nil {
		return nil, err
	}
	pub, err := hex.DecodeString(b.PubKey)
	if err != nil || len(pub) != 32 || len(b.DeviceID) != 64 {
		return nil, errors.New("bad pubkey or device id")
	}
	var id [32]byte
	if raw, err := hex.DecodeString(b.DeviceID); err != nil || len(raw) != 32 {
		return nil, errors.New("bad device id")
	} else {
		copy(id[:], raw)
	}
	sum := sha256.Sum256(pub)
	if sum != id {
		return nil, errors.New("device id is not sha256(pubkey)")
	}

	nonce := randHex(16)
	ch, err := signaling.Build(signaling.TypeLoginChal, begin.ID, signaling.LoginChallenge{
		Nonce:       nonce,
		ExpiresInMS: s.limits.LoginTTL.Milliseconds(),
	})
	if err != nil {
		return nil, err
	}
	if err := writeEnvelope(ctx, conn, ch); err != nil {
		return nil, err
	}

	msg, err := s.readType(ctx, conn, signaling.TypeLogin)
	if err != nil {
		return nil, err
	}
	body, err := signaling.Decode[signaling.Login](msg)
	if err != nil {
		return nil, err
	}
	sig, err := hex.DecodeString(body.Signature)
	if err != nil || len(sig) != 64 {
		return nil, errors.New("bad signature encoding")
	}
	if err := identity.VerifyLogin(pub, id, nonce, sig); err != nil {
		return nil, err
	}
	return &peer{id: b.DeviceID, pub: pub, conn: conn}, nil
}

func (s *Server) readType(ctx context.Context, conn *websocket.Conn, want string) (*signaling.Envelope, error) {
	typ, raw, err := conn.Read(ctx)
	if err != nil {
		return nil, err
	}
	if typ != websocket.MessageText {
		return nil, errors.New("expected a text frame")
	}
	e, err := signaling.Parse(raw)
	if err != nil {
		return nil, err
	}
	if e.Type != want {
		return nil, fmt.Errorf("expected %s, got %s", want, e.Type)
	}
	return e, nil
}

// register adds the peer, replacing and closing any previous connection for the
// same device (a reconnect should not leave a zombie).
func (s *Server) register(p *peer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.peers) >= s.limits.MaxPeers {
		return errors.New("server is full")
	}
	if old, ok := s.peers[p.id]; ok && old != p {
		// A reconnect replaces the old socket; closing it also removes any
		// half-open session it still held.
		old.conn.CloseNow()
	}
	s.peers[p.id] = p
	return nil
}

func (s *Server) unregister(p *peer) {
	s.mu.Lock()
	if cur, ok := s.peers[p.id]; ok && cur == p {
		delete(s.peers, p.id)
	}
	s.mu.Unlock()
	s.broadcastPresence(p.id, false)
	if s.log != nil {
		s.log.Event("server.logout", map[string]any{"device_id": truncID(p.id), "online": s.online()})
	}
}

func (s *Server) online() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.peers)
}

func (s *Server) sendLoginOK(ctx context.Context, p *peer) {
	s.mu.Lock()
	peers := make([]signaling.Peer, 0, len(s.peers))
	for id := range s.peers {
		if id == p.id {
			continue
		}
		peers = append(peers, signaling.Peer{DeviceID: id, Online: true})
	}
	s.mu.Unlock()

	e, err := signaling.Build(signaling.TypeLoginOK, "", signaling.LoginOK{
		ServerTime: time.Now().UnixMilli(),
		Peers:      peers,
	})
	if err == nil {
		_ = p.send(ctx, e)
	}
	if s.log != nil {
		s.log.Event("server.login", map[string]any{"device_id": truncID(p.id), "online": len(peers) + 1})
	}
}

func (s *Server) broadcastPresence(deviceID string, online bool) {
	e, err := signaling.Build(signaling.TypePresence, "", signaling.Presence{DeviceID: deviceID, Online: online})
	if err != nil {
		return
	}
	s.mu.Lock()
	targets := make([]*peer, 0, len(s.peers))
	for id, p := range s.peers {
		if id != deviceID {
			targets = append(targets, p)
		}
	}
	s.mu.Unlock()
	for _, p := range targets {
		_ = p.send(context.Background(), e)
	}
}

// route dispatches one authenticated message.
func (s *Server) route(ctx context.Context, p *peer, e *signaling.Envelope) {
	switch e.Type {
	case signaling.TypePing:
		s.mu.Lock()
		alive := s.peers[p.id] == p
		s.mu.Unlock()
		if !alive {
			_ = p.conn.CloseNow()
			return
		}
		body, _ := signaling.Decode[signaling.Ping](e)
		pong, err := signaling.Build(signaling.TypePong, e.ID, body)
		if err == nil {
			_ = p.send(ctx, pong)
		}
	case signaling.TypePairBegin, signaling.TypePairRelay:
		if !s.allowPair(p) {
			s.writeError(ctx, p.conn, e.ID, signaling.ErrRateLimited, "too many pairing messages")
			return
		}
		fallthrough
	case signaling.TypeCandidates, signaling.TypeSendRequest, signaling.TypeSendResp:
		to, ok := toDevice(e)
		if !ok {
			s.writeError(ctx, p.conn, e.ID, signaling.ErrBadRequest, "missing to_device")
			return
		}
		target := s.peerByID(to)
		if target == nil {
			s.writeError(ctx, p.conn, e.ID, signaling.ErrPeerOffline, "peer is not online")
			return
		}
		// The receiver has to know whom to answer, so the server stamps the
		// sender into send_request before forwarding.
		if e.Type == signaling.TypeSendRequest {
			e = withSender(e, p.id)
		}
		if err := target.send(ctx, e); err != nil {
			s.writeError(ctx, p.conn, e.ID, signaling.ErrPeerOffline, "peer write failed")
		}
	case signaling.TypeRelayOpen:
		s.openRelay(ctx, p, e)
	default:
		s.writeError(ctx, p.conn, e.ID, signaling.ErrUnsupportedType, "unsupported message type "+e.Type)
	}
}

// allowLogin applies the per-IP login budget. The table is pruned lazily so a
// long scan cannot grow it without bound.
func (s *Server) allowLogin(ip string) bool {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	now := time.Now()
	if len(s.loginLimits) > 4096 {
		for k, b := range s.loginLimits {
			if now.Sub(b.window) > s.limits.LoginWindow {
				delete(s.loginLimits, k)
			}
		}
	}
	b, ok := s.loginLimits[ip]
	if !ok || now.Sub(b.window) > s.limits.LoginWindow {
		b = &loginBucket{window: now}
		s.loginLimits[ip] = b
	}
	b.count++
	return b.count <= s.limits.LoginBurst
}

// allowPair applies the per-peer pairing budget (PairWindow, PairBurst).
func (s *Server) allowPair(p *peer) bool {
	p.pairMu.Lock()
	defer p.pairMu.Unlock()
	now := time.Now()
	if p.pairWindow.IsZero() || now.Sub(p.pairWindow) > s.limits.PairWindow {
		p.pairWindow = now
		p.pairCount = 0
	}
	p.pairCount++
	return p.pairCount <= s.limits.PairBurst
}

// withSender returns a copy of e whose send_request payload carries the
// sender's device id.
func withSender(e *signaling.Envelope, from string) *signaling.Envelope {
	var req signaling.SendRequest
	if err := json.Unmarshal(e.Data, &req); err != nil {
		return e
	}
	req.FromDevice = from
	raw, err := json.Marshal(req)
	if err != nil {
		return e
	}
	cp := *e
	cp.Data = raw
	return &cp
}

func toDevice(e *signaling.Envelope) (string, bool) {
	var f struct {
		ToDevice string `json:"to_device"`
	}
	if err := json.Unmarshal(e.Data, &f); err != nil || f.ToDevice == "" {
		return "", false
	}
	return f.ToDevice, true
}

func (s *Server) peerByID(id string) *peer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.peers[id]
}

// openRelay creates a room shared by the two devices and sends relay_ready to
// both (protocol §8.5).
func (s *Server) openRelay(ctx context.Context, from *peer, e *signaling.Envelope) {
	req, err := signaling.Decode[signaling.RelayOpen](e)
	if err != nil || req.ToDevice == "" {
		s.writeError(ctx, from.conn, e.ID, signaling.ErrBadRequest, "bad relay_open")
		return
	}
	to := s.peerByID(req.ToDevice)
	if to == nil {
		s.writeError(ctx, from.conn, e.ID, signaling.ErrPeerOffline, "peer is not online")
		return
	}

	s.mu.Lock()
	if len(s.rooms) >= s.limits.MaxRelayRooms {
		s.mu.Unlock()
		s.writeError(ctx, from.conn, e.ID, signaling.ErrBadRequest, "relay is full")
		return
	}
	token := randHex(24)
	rm := &room{
		token: token, transfer: req.TransferID, created: time.Now(),
		readyCh: make(chan struct{}), done: make(chan struct{}),
		log: s.log, rate: s.limits.RelayRate,
		onBytes: func(n int) { s.relayBytes.Add(int64(n)) },
	}
	s.rooms[token] = rm
	s.mu.Unlock()

	ready, err := signaling.Build(signaling.TypeRelayReady, e.ID, signaling.RelayReady{
		RelayURL:    "/relay",
		Token:       token,
		ExpiresInMS: 60000,
	})
	if err != nil {
		return
	}
	_ = from.send(ctx, ready)

	// The target learns about the room too, so both sides can connect.
	targetMsg, err := signaling.Build(signaling.TypeRelayReady, "", signaling.RelayReady{
		RelayURL:    "/relay",
		Token:       token,
		ExpiresInMS: 60000,
	})
	if err == nil {
		_ = to.send(ctx, targetMsg)
	}

	if s.log != nil {
		s.log.Event("server.relay_open", map[string]any{
			"from": truncID(from.id), "to": truncID(to.id), "transfer": req.TransferID,
		})
	}
	time.AfterFunc(61*time.Second, func() { s.expireRoom(token) })
}

func (s *Server) expireRoom(token string) {
	s.mu.Lock()
	rm, ok := s.rooms[token]
	if ok && rm.bothConnected() {
		s.mu.Unlock()
		return
	}
	delete(s.rooms, token)
	s.mu.Unlock()
	if ok {
		rm.closeAll()
	}
}

// handleRelay upgrades /relay?token=… and pairs the two sides.
func (s *Server) handleRelay(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Error(w, "missing token", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	rm, ok := s.rooms[token]
	s.mu.Unlock()
	if !ok {
		http.Error(w, "unknown or expired token", http.StatusNotFound)
		return
	}
	conn, err := s.accept(w, r)
	if err != nil {
		return
	}
	defer conn.CloseNow()

	_, both := rm.attach(conn)
	if both {
		s.mu.Lock()
		delete(s.rooms, token)
		s.mu.Unlock()
	} else {
		// Wait for the other side (the room expires after 60 s).
		select {
		case <-r.Context().Done():
			rm.closeAll()
			return
		case <-rm.ready():
		}
	}
	rm.startSplice(r.Context(), s.limits.RelayIdle)
	<-rm.done
}

func (s *Server) writeError(ctx context.Context, conn *websocket.Conn, ref, code, msg string) {
	e, err := signaling.Build(signaling.TypeError, "", signaling.ErrorBody{Code: code, Message: msg, Ref: ref})
	if err != nil {
		return
	}
	_ = writeEnvelope(ctx, conn, e)
}

func writeEnvelope(ctx context.Context, conn *websocket.Conn, e *signaling.Envelope) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, raw)
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func truncID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
