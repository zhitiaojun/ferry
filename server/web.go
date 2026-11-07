// Web endpoints served by ft-server on the public site: a decoy page, a
// password gate, device discovery by short code, and the relay WebSocket the
// browser uses when a LAN path is not available.
//
// The browser never learns the device list: it must present a code that a
// device registered moments ago. The page keeps the "not a file tool" look;
// the transfer UI only appears after the hidden gesture and the password.
package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/cwlindean/filetransfer/core/logging"
)

//go:embed assets/*
var webAssets embed.FS

// WebLimits bounds the discovery surface.
type WebLimits struct {
	DeviceTTL      time.Duration // how long a registered code stays resolvable
	CodeAttempts   int           // resolve attempts per IP per window
	UnlockAttempts int           // password attempts per IP per window
	AttemptWindow  time.Duration
	MaxLANURLs     int
}

// DefaultWebLimits are the production values: a code lives ten minutes and a
// stranger gets a handful of guesses.
func DefaultWebLimits() WebLimits {
	return WebLimits{
		DeviceTTL:      10 * time.Minute,
		CodeAttempts:   20,
		UnlockAttempts: 8,
		AttemptWindow:  5 * time.Minute,
		MaxLANURLs:     4,
	}
}

type webDevice struct {
	Code     string   `json:"code"`
	Name     string   `json:"name"`
	Room     string   `json:"room"`
	Token    string   `json:"-"`
	LAN      []string `json:"lan"`
	Expires  time.Time
	LastSeen time.Time
}

type rateBucket struct {
	n     int
	until time.Time
}

// Web serves the public site and the discovery API.
type Web struct {
	limits   WebLimits
	password [32]byte // sha256 of FT_WEB_PASSWORD; zero means disabled
	secret   []byte   // cookie signing key, random per process

	mu      sync.Mutex
	devices map[string]*webDevice // by code
	byRoom  map[string]*webDevice
	limits4 map[string]*rateBucket // rate limits keyed by ip|kind
	rooms   map[string]*webRoom

	log *logging.Logger
}

// NewWeb builds the web surface. password may be empty, which disables unlock
// (the transfer UI stays unreachable) — deployments set FT_WEB_PASSWORD.
func NewWeb(password string, limits WebLimits, log *logging.Logger) *Web {
	w := &Web{
		limits:  limits,
		secret:  make([]byte, 32),
		devices: map[string]*webDevice{},
		byRoom:  map[string]*webDevice{},
		limits4: map[string]*rateBucket{},
		rooms:   map[string]*webRoom{},
		log:     log,
	}
	if _, err := rand.Read(w.secret); err != nil {
		panic(err)
	}
	if password != "" {
		w.password = sha256.Sum256([]byte(password))
	}
	return w
}

func (w *Web) enabled() bool { return w.password != [32]byte{} }

// registerRoutes attaches the web surface to mux.
func (w *Web) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", w.handleDecoy)
	mux.HandleFunc("/app", w.handleApp)
	mux.HandleFunc("/api/unlock", w.handleUnlock)
	mux.HandleFunc("/api/devices", w.handleDevices)
	mux.HandleFunc("/api/resolve", w.handleResolve)
	mux.HandleFunc("/web/", w.handleWebSocket)
}

func (w *Web) handleDecoy(rw http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(rw, r)
		return
	}
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.Header().Set("Cache-Control", "no-store")
	_, _ = rw.Write(mustAsset("assets/index.html"))
}

func (w *Web) handleApp(rw http.ResponseWriter, r *http.Request) {
	if !w.authorized(r) {
		http.Error(rw, "not found", http.StatusNotFound)
		return
	}
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.Header().Set("Cache-Control", "no-store")
	_, _ = rw.Write(mustAsset("assets/app.html"))
}

func (w *Web) handleUnlock(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method", http.StatusMethodNotAllowed)
		return
	}
	if !w.enabled() {
		http.Error(rw, "disabled", http.StatusServiceUnavailable)
		return
	}
	ip := clientIP(r)
	if !w.allow(ip+"|unlock", w.limits.UnlockAttempts) {
		http.Error(rw, "slow down", http.StatusTooManyRequests)
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		http.Error(rw, "bad json", http.StatusBadRequest)
		return
	}
	sum := sha256.Sum256([]byte(body.Password))
	if subtle.ConstantTimeCompare(sum[:], w.password[:]) != 1 {
		time.Sleep(350 * time.Millisecond)
		http.Error(rw, "denied", http.StatusForbidden)
		return
	}
	http.SetCookie(rw, &http.Cookie{
		Name:     "ftw",
		Value:    w.sign(time.Now().Add(2 * time.Hour)),
		Path:     "/",
		HttpOnly: true,
		// Behind nginx the TLS ends at the proxy, so trust the forwarded
		// scheme; keep Secure for real public TLS (and allow local http tests).
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		SameSite: http.SameSiteLaxMode,
		MaxAge:   7200,
	})
	rw.Header().Set("Content-Type", "application/json")
	_, _ = rw.Write([]byte(`{"ok":true}`))
}

func (w *Web) handleDevices(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "method", http.StatusMethodNotAllowed)
		return
	}
	if !w.authorized(r) {
		http.Error(rw, "forbidden", http.StatusForbidden)
		return
	}
	var body struct {
		Code  string   `json:"code"`
		Name  string   `json:"name"`
		Token string   `json:"device_token"`
		LAN   []string `json:"lan"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&body); err != nil {
		http.Error(rw, "bad json", http.StatusBadRequest)
		return
	}
	code := strings.TrimSpace(body.Code)
	if len(code) != 6 || !allDigits(code) {
		http.Error(rw, "code must be 6 digits", http.StatusBadRequest)
		return
	}
	if len(body.LAN) > w.limits.MaxLANURLs {
		body.LAN = body.LAN[:w.limits.MaxLANURLs]
	}
	for _, u := range body.LAN {
		if !validLANURL(u) {
			http.Error(rw, "lan url must be http(s)://host[:port]/...", http.StatusBadRequest)
			return
		}
	}

	w.mu.Lock()
	defer w.mu.Unlock()
	w.gcLocked()
	d := w.devices[code]
	if d != nil && body.Token != "" && subtle.ConstantTimeCompare([]byte(d.Token), []byte(body.Token)) != 1 {
		http.Error(rw, "wrong device token", http.StatusForbidden)
		return
	}
	if d == nil {
		d = &webDevice{Code: code, Room: randomHex(8), Token: randomHex(16)}
		w.devices[code] = d
		w.byRoom[d.Room] = d
	}
	if strings.TrimSpace(body.Name) != "" {
		d.Name = truncate(strings.TrimSpace(body.Name), 48)
	}
	d.LAN = body.LAN
	d.LastSeen = time.Now()
	d.Expires = time.Now().Add(w.limits.DeviceTTL)

	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(map[string]any{
		"ok":           true,
		"room":         d.Room,
		"device_token": d.Token,
		"ttl_s":        int(w.limits.DeviceTTL / time.Second),
	})
}

func (w *Web) handleResolve(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(rw, "method", http.StatusMethodNotAllowed)
		return
	}
	if !w.authorized(r) {
		http.Error(rw, "forbidden", http.StatusForbidden)
		return
	}
	ip := clientIP(r)
	if !w.allow(ip+"|code", w.limits.CodeAttempts) {
		http.Error(rw, "slow down", http.StatusTooManyRequests)
		return
	}
	code := r.URL.Query().Get("code")
	w.mu.Lock()
	w.gcLocked()
	d := w.devices[code]
	w.mu.Unlock()
	if d == nil || time.Now().After(d.Expires) {
		time.Sleep(200 * time.Millisecond)
		http.Error(rw, "not found", http.StatusNotFound)
		return
	}
	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(map[string]any{
		"ok":      true,
		"name":    d.Name,
		"lan":     d.LAN,
		"room":    d.Room,
		"ttl_s":   int(time.Until(d.Expires) / time.Second),
		"relay":   true,
		"version": "1",
	})
}

// handleWebSocket joins the browser and the device for a room and pipes
// messages between them. The server only forwards bytes.
func (w *Web) handleWebSocket(rw http.ResponseWriter, r *http.Request) {
	room := strings.TrimPrefix(r.URL.Path, "/web/")
	if room == "" || strings.Contains(room, "/") {
		http.NotFound(rw, r)
		return
	}
	role := r.URL.Query().Get("role")
	w.mu.Lock()
	d := w.byRoom[room]
	w.mu.Unlock()
	if d == nil || time.Now().After(d.Expires) {
		http.NotFound(rw, r)
		return
	}
	switch role {
	case "host":
		hdr := r.Header.Get("X-FT-Device-Token")
		if hdr == "" {
			hdr = r.URL.Query().Get("token")
		}
		if subtle.ConstantTimeCompare([]byte(hdr), []byte(d.Token)) != 1 {
			http.Error(rw, "forbidden", http.StatusForbidden)
			return
		}
	case "guest":
		if !w.authorized(r) {
			http.Error(rw, "forbidden", http.StatusForbidden)
			return
		}
	default:
		http.Error(rw, "role", http.StatusBadRequest)
		return
	}

	c, err := websocket.Accept(rw, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	c.SetReadLimit(1 << 20)
	w.mu.Lock()
	wr := w.rooms[room]
	if wr == nil || wr.isClosed() {
		wr = &webRoom{room: room, ready: make(chan struct{})}
		w.rooms[room] = wr
	}
	wr.log = w.log
	w.mu.Unlock()

	if err := wr.attach(r.Context(), role, c); err != nil {
		c.Close(websocket.StatusPolicyViolation, err.Error())
		return
	}
	wr.serve(r.Context())

	// Drop closed rooms so the next reconnection builds a fresh ready channel.
	if wr.isClosed() {
		w.mu.Lock()
		if w.rooms[room] == wr {
			delete(w.rooms, room)
		}
		w.mu.Unlock()
	}
}

func (w *Web) authorized(r *http.Request) bool {
	c, err := r.Cookie("ftw")
	if err != nil || c.Value == "" {
		return false
	}
	return w.verify(c.Value)
}

func (w *Web) sign(exp time.Time) string {
	payload := strconv.FormatInt(exp.Unix(), 10)
	mac := hmac.New(sha256.New, w.secret)
	mac.Write([]byte(payload))
	return payload + "." + hex.EncodeToString(mac.Sum(nil))
}

func (w *Web) verify(v string) bool {
	i := strings.LastIndexByte(v, '.')
	if i <= 0 {
		return false
	}
	payload, sig := v[:i], v[i+1:]
	exp, err := strconv.ParseInt(payload, 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	mac := hmac.New(sha256.New, w.secret)
	mac.Write([]byte(payload))
	want := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(sig), []byte(want))
}

func (w *Web) allow(key string, n int) bool {
	now := time.Now()
	w.mu.Lock()
	defer w.mu.Unlock()
	b := w.limits4[key]
	if b == nil || now.After(b.until) {
		b = &rateBucket{until: now.Add(w.limits.AttemptWindow)}
		w.limits4[key] = b
	}
	b.n++
	return b.n <= n
}

func (w *Web) gcLocked() {
	now := time.Now()
	for code, d := range w.devices {
		if now.After(d.Expires.Add(5 * time.Minute)) {
			delete(w.devices, code)
			delete(w.byRoom, d.Room)
			delete(w.rooms, d.Room)
		}
	}
	for k, b := range w.limits4 {
		if now.After(b.until) {
			delete(w.limits4, k)
		}
	}
}

func mustAsset(name string) []byte {
	b, err := webAssets.ReadFile(name)
	if err != nil {
		panic(err)
	}
	return b
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func validLANURL(s string) bool {
	if !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
		return false
	}
	return len(s) <= 300
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func clientIP(r *http.Request) string {
	if v := r.Header.Get("X-Forwarded-For"); v != "" {
		return strings.TrimSpace(strings.Split(v, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func fmtErr(format string, a ...any) string { return fmt.Sprintf(format, a...) }
