package tempshare

import (
	"bytes"
	"context"
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

	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/lanweb"
	"github.com/cwlindean/filetransfer/core/logging"
)

// DefaultBaseURL is the public Ferry website and relay base URL.
const DefaultBaseURL = "https://ferry.cwlindean.cn"

// TransferProgress reports live file transfer progress for a TempShare host or client.
type TransferProgress struct {
	ID         uint64
	Direction  string // "send" | "recv"
	Channel    string // "lan" | "relay"
	Stage      string // "connecting" | "transferring" | "verifying"
	FileName   string
	FileIndex  int
	FileCount  int
	BytesDone  int64
	BytesTotal int64
	Done       bool
}

// HostActiveTransfer describes the transfer currently running inside a TempShare Host.
type HostActiveTransfer struct {
	Direction  string `json:"direction"`
	Channel    string `json:"channel"`
	Stage      string `json:"stage"`
	FileName   string `json:"file_name"`
	BytesDone  int64  `json:"bytes_done"`
	BytesTotal int64  `json:"bytes_total"`
	SpeedBPS   int64  `json:"speed_bps,omitempty"`
}

type txSpeedSample struct {
	t     time.Time
	bytes int64
}

// HostOptions configures a temporary transfer host session.
type HostOptions struct {
	BaseURL     string
	Key         *identity.Key
	DeviceName  string
	Mode        string // "send", "recv", or "both"
	SharedFiles []string
	RecvDir     string
	LANHintIP   string
	OnUploaded  func(lanweb.File)
	OnProgress  func(TransferProgress)
	Log         *logging.Logger
}

// HostStatus summarizes an active temporary transfer host session.
type HostStatus struct {
	Active        bool                `json:"active"`
	Code          string              `json:"code"`
	ShareURL      string              `json:"url"`
	LANURL        string              `json:"lan_url"`
	Mode          string              `json:"mode"`
	FileCount     int                 `json:"file_count"`
	TTLSeconds    int                 `json:"ttl_s"`
	UploadedCount int                 `json:"uploaded_count"`
	LastFile      string              `json:"last_file"`
	ActiveTx      *HostActiveTransfer `json:"active_tx,omitempty"`
}

// Host owns a local lanweb server and a public temporary relay room.
type Host struct {
	baseURL    string
	key        *identity.Key
	deviceName string
	mode       string
	room       string
	hostToken  string
	code       string
	shareToken string
	shareURL   string
	lanURL     string
	lanURLs    []string
	expiresAt  time.Time

	srv        *lanweb.Server
	ln         net.Listener
	httpClient *http.Client
	log        *logging.Logger
	onProgress func(TransferProgress)

	cancel    context.CancelFunc
	closeOnce sync.Once

	mu            sync.Mutex
	uploadedCount int
	lastFile      string
	activeTxID    uint64
	activeTx      *HostActiveTransfer
	txSamples     []txSpeedSample

	whMu sync.Mutex
	wh   *webHost
}

// SignCreate produces the Ed25519 signature required by POST /api/temp/create.
func SignCreate(key *identity.Key, ts int64, mode, name string) string {
	msg := "ferry-temp-v1\n" + strconv.FormatInt(ts, 10) + "\n" + mode + "\n" + name
	return hex.EncodeToString(key.Sign([]byte(msg)))
}

// StartHost starts a temporary transfer session on both LAN HTTP and public WSS relay.
func StartHost(opts HostOptions) (*Host, error) {
	base := strings.TrimRight(strings.TrimSpace(opts.BaseURL), "/")
	if base == "" {
		base = DefaultBaseURL
	}
	mode := strings.TrimSpace(opts.Mode)
	if mode == "" {
		mode = "recv"
	}
	if opts.Key == nil || len(opts.Key.Public()) == 0 {
		return nil, fmt.Errorf("invalid device identity key")
	}

	var h *Host
	srv, err := lanweb.NewScoped(lanweb.ScopedOptions{
		Mode:        mode,
		RecvDir:     opts.RecvDir,
		SharedFiles: opts.SharedFiles,
		DeviceName:  opts.DeviceName,
		Log:         opts.Log,
		OnUploaded: func(f lanweb.File) {
			if h != nil {
				h.mu.Lock()
				h.uploadedCount++
				h.lastFile = f.Name
				h.mu.Unlock()
			}
			if opts.OnUploaded != nil {
				opts.OnUploaded(f)
			}
		},
		OnProgress: func(p lanweb.Progress) {
			if h != nil {
				h.reportProgress(TransferProgress{
					ID:         p.ID,
					Direction:  p.Direction,
					Channel:    p.Channel,
					Stage:      p.Stage,
					FileName:   p.FileName,
					FileIndex:  1,
					FileCount:  1,
					BytesDone:  p.BytesDone,
					BytesTotal: p.BytesTotal,
					Done:       p.Done,
				})
			}
		},
	})
	if err != nil {
		return nil, err
	}
	if mode == "send" && len(srv.Snapshot()) == 0 {
		return nil, fmt.Errorf("no valid files selected to share")
	}

	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		return nil, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		_ = srv.ListenAndServe("", ln)
	}()

	lanURLs := collectLANURLs(srv, port, opts.LANHintIP)
	lanPrimary := ""
	if len(lanURLs) > 0 {
		lanPrimary = lanURLs[0]
	}

	httpClient := &http.Client{Timeout: 15 * time.Second}
	reg, err := createTempRoom(httpClient, base, opts.Key, opts.DeviceName, mode, lanURLs, "", "")
	if err != nil {
		_ = ln.Close()
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	h = &Host{
		baseURL:    base,
		key:        opts.Key,
		deviceName: opts.DeviceName,
		mode:       mode,
		room:       reg.Room,
		hostToken:  reg.HostToken,
		code:       reg.Code,
		shareToken: reg.ShareToken,
		shareURL:   base + "/s/" + reg.ShareToken,
		lanURL:     lanPrimary,
		lanURLs:    lanURLs,
		expiresAt:  time.Now().Add(time.Duration(reg.TTLS) * time.Second),
		srv:        srv,
		ln:         ln,
		httpClient: httpClient,
		log:        opts.Log,
		onProgress: opts.OnProgress,
		cancel:     cancel,
	}

	go h.heartbeatLoop(ctx)
	go h.relayLoop(ctx)
	return h, nil
}

func (h *Host) reportProgress(p TransferProgress) {
	if h == nil {
		return
	}
	now := time.Now()
	h.mu.Lock()
	if p.Done {
		if h.activeTxID == p.ID || p.ID == 0 {
			h.activeTxID = 0
			h.activeTx = nil
			h.txSamples = nil
		}
	} else {
		if h.activeTxID != p.ID || (len(h.txSamples) > 0 && p.BytesDone < h.txSamples[len(h.txSamples)-1].bytes) {
			h.activeTxID = p.ID
			h.txSamples = []txSpeedSample{{t: now, bytes: p.BytesDone}}
		} else {
			h.txSamples = append(h.txSamples, txSpeedSample{t: now, bytes: p.BytesDone})
			for len(h.txSamples) > 2 && now.Sub(h.txSamples[0].t) > 2*time.Second {
				h.txSamples = h.txSamples[1:]
			}
		}
		var speedBPS int64
		if p.Stage != "verifying" && len(h.txSamples) >= 2 {
			first := h.txSamples[0]
			dt := now.Sub(first.t).Seconds()
			db := p.BytesDone - first.bytes
			if dt >= 0.15 && db > 0 {
				speedBPS = int64(float64(db) / dt)
			}
		}
		h.activeTx = &HostActiveTransfer{
			Direction:  p.Direction,
			Channel:    p.Channel,
			Stage:      p.Stage,
			FileName:   p.FileName,
			BytesDone:  p.BytesDone,
			BytesTotal: p.BytesTotal,
			SpeedBPS:   speedBPS,
		}
	}
	cb := h.onProgress
	h.mu.Unlock()
	if cb != nil {
		cb(p)
	}
}

// Status returns a snapshot of the active session.
func (h *Host) Status() HostStatus {
	if h == nil {
		return HostStatus{}
	}
	h.mu.Lock()
	upCount := h.uploadedCount
	last := h.lastFile
	exp := h.expiresAt
	var activeCopy *HostActiveTransfer
	if h.activeTx != nil {
		cp := *h.activeTx
		activeCopy = &cp
	}
	h.mu.Unlock()
	rem := int(time.Until(exp).Seconds())
	if rem < 0 {
		rem = 0
	}
	return HostStatus{
		Active:        true,
		Code:          h.code,
		ShareURL:      h.shareURL,
		LANURL:        h.lanURL,
		Mode:          h.mode,
		FileCount:     len(h.srv.Snapshot()),
		TTLSeconds:    rem,
		UploadedCount: upCount,
		LastFile:      last,
		ActiveTx:      activeCopy,
	}
}

// Close tears down the local LAN listener, stops the relay loop, and closes the
// temporary room on the server.
func (h *Host) Close() error {
	if h == nil {
		return nil
	}
	h.closeOnce.Do(func() {
		h.cancel()
		if h.ln != nil {
			_ = h.ln.Close()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		body, _ := json.Marshal(map[string]string{
			"room":       h.room,
			"host_token": h.hostToken,
		})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.baseURL+"/api/temp/close", bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			resp, err := h.httpClient.Do(req)
			if err == nil {
				resp.Body.Close()
			}
		}
	})
	return nil
}

type tempCreateResp struct {
	Code       string `json:"code"`
	ShareToken string `json:"share_token"`
	Room       string `json:"room"`
	HostToken  string `json:"host_token"`
	TTLS       int    `json:"ttl_s"`
}

func createTempRoom(client *http.Client, base string, key *identity.Key, name, mode string, lanURLs []string, room, hostToken string) (*tempCreateResp, error) {
	ts := time.Now().Unix()
	sig := SignCreate(key, ts, mode, name)
	payload := map[string]any{
		"name": name,
		"mode": mode,
		"lan":  lanURLs,
		"ts":   ts,
		"pub":  key.PubHex(),
		"sig":  sig,
	}
	if room != "" && hostToken != "" {
		payload["room"] = room
		payload["host_token"] = hostToken
	}
	body, _ := json.Marshal(payload)
	resp, err := client.Post(base+"/api/temp/create", "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("temp create: %s %s", resp.Status, strings.TrimSpace(string(out)))
	}
	var reg tempCreateResp
	if err := json.Unmarshal(out, &reg); err != nil {
		return nil, err
	}
	return &reg, nil
}

func (h *Host) heartbeatLoop(ctx context.Context) {
	t := time.NewTicker(2 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		reg, err := createTempRoom(h.httpClient, h.baseURL, h.key, h.deviceName, h.mode, h.lanURLs, h.room, h.hostToken)
		if err == nil && reg.TTLS > 0 {
			h.mu.Lock()
			h.expiresAt = time.Now().Add(time.Duration(reg.TTLS) * time.Second)
			h.mu.Unlock()
		}
	}
}

func (h *Host) relayLoop(ctx context.Context) {
	backoff := 400 * time.Millisecond
	for {
		err := h.relayOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		wait := 300 * time.Millisecond
		if err != nil {
			wait = backoff
			if backoff < 10*time.Second {
				backoff *= 2
			}
		} else {
			backoff = 400 * time.Millisecond
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
	}
}

func (h *Host) relayOnce(ctx context.Context) error {
	wsBase := strings.Replace(strings.Replace(h.baseURL, "https://", "wss://", 1), "http://", "ws://", 1)
	u := wsBase + "/web/" + h.room + "?role=host&token=" + h.hostToken
	c, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{
		HTTPHeader: http.Header{"X-Ft-Device-Token": []string{h.hostToken}},
	})
	if err != nil {
		return err
	}
	defer c.Close(websocket.StatusNormalClosure, "bye")

	pingCtx, stopPing := context.WithCancel(ctx)
	defer stopPing()
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-pingCtx.Done():
				return
			case <-t.C:
				pc, cancel := context.WithTimeout(pingCtx, 10*time.Second)
				err := c.Ping(pc)
				cancel()
				if err != nil {
					return
				}
			}
		}
	}()

	wh := newWebHost(c, h.srv, h.log, h.reportProgress)
	h.whMu.Lock()
	h.wh = wh
	h.whMu.Unlock()
	defer func() {
		h.whMu.Lock()
		if h.wh == wh {
			h.wh = nil
		}
		h.whMu.Unlock()
	}()
	return wh.run(ctx)
}

func (h *Host) currentWebHost() *webHost {
	if h == nil {
		return nil
	}
	h.whMu.Lock()
	wh := h.wh
	h.whMu.Unlock()
	return wh
}

// ActivePaused reports whether the active TempShare transfer is paused.
func (h *Host) ActivePaused() bool {
	if h == nil {
		return false
	}
	if wh := h.currentWebHost(); wh != nil && wh.hasActive() {
		return wh.isActivePaused()
	}
	return h.srv.ActivePaused()
}

// TogglePauseActive pauses or resumes the currently active TempShare transfer.
func (h *Host) TogglePauseActive() bool {
	if h == nil {
		return false
	}
	if wh := h.currentWebHost(); wh != nil && wh.togglePauseActive() {
		return true
	}
	return h.srv.TogglePauseActive()
}

// CancelActive cancels the currently active TempShare transfer without closing
// the TempShare hosting session.
func (h *Host) CancelActive() bool {
	if h == nil {
		return false
	}
	if wh := h.currentWebHost(); wh != nil && wh.cancelActive() {
		return true
	}
	return h.srv.CancelActive()
}

func collectLANURLs(srv *lanweb.Server, port int, hintIP string) []string {
	seen := map[string]bool{}
	var out []string
	addIP := func(ip string) {
		ip = strings.TrimSpace(ip)
		if ip == "" || ip == "<nil>" || seen[ip] {
			return
		}
		parsed := net.ParseIP(ip)
		if parsed == nil || parsed.IsLoopback() || parsed.To4() == nil || parsed.IsLinkLocalUnicast() {
			return
		}
		seen[ip] = true
		out = append(out, srv.URL(net.JoinHostPort(ip, strconv.Itoa(port))))
	}
	addIP(hintIP)
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.IsLoopback() || ipn.IP.To4() == nil || ipn.IP.IsLinkLocalUnicast() {
				continue
			}
			addIP(ipn.IP.String())
		}
	}
	if len(out) > 4 {
		out = out[:4]
	}
	return out
}
