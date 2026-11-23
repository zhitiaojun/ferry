package tempshare

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/cwlindean/filetransfer/core/lanweb"
)

// ResolvedSession describes a temporary transfer session resolved from a URL or 6-digit code.
type ResolvedSession struct {
	BaseURL    string   `json:"base_url"`
	Name       string   `json:"name"`
	Mode       string   `json:"mode"`
	LAN        []string `json:"lan"`
	Room       string   `json:"room"`
	GuestToken string   `json:"guest_token"`
	TTLSeconds int      `json:"ttl_s"`
}

// ProbeResult reports the chosen transport ("lan" or "relay") and current files.
type ProbeResult struct {
	Via     string        `json:"via"`
	LANBase string        `json:"lan_base,omitempty"`
	Files   []lanweb.File `json:"files"`
}

// ParseShareURL parses https://host/s/<token> into baseURL and token.
func ParseShareURL(raw string) (baseURL, token string, ok bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", "", false
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", "", false
	}
	p := strings.Trim(u.Path, "/")
	if !strings.HasPrefix(p, "s/") {
		return "", "", false
	}
	tok := strings.TrimPrefix(p, "s/")
	if tok == "" || strings.Contains(tok, "/") {
		return "", "", false
	}
	return u.Scheme + "://" + u.Host, tok, true
}

// ResolveByURL resolves a https://host/s/<token> link.
func ResolveByURL(ctx context.Context, shareURL string) (*ResolvedSession, error) {
	base, tok, ok := ParseShareURL(shareURL)
	if !ok {
		return nil, fmt.Errorf("invalid share url")
	}
	return resolve(ctx, base, "/api/temp/resolve?token="+url.QueryEscape(tok))
}

// ResolveByCode resolves a 6-digit temporary code against baseURL.
func ResolveByCode(ctx context.Context, baseURL, code string) (*ResolvedSession, error) {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = DefaultBaseURL
	}
	code = strings.TrimSpace(code)
	if len(code) != 6 {
		return nil, fmt.Errorf("请输入 6 位数字码")
	}
	return resolve(ctx, base, "/api/temp/resolve?code="+url.QueryEscape(code))
}

func resolve(ctx context.Context, base, path string) (*ResolvedSession, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("链接已失效或不存在 (%d)", resp.StatusCode)
	}
	var s ResolvedSession
	if err := json.Unmarshal(out, &s); err != nil {
		return nil, err
	}
	s.BaseURL = base
	return &s, nil
}

// ProbeTransport tries LAN URLs first (fast direct transfer for apps), falling back to relay.
func ProbeTransport(ctx context.Context, sess *ResolvedSession) (*ProbeResult, error) {
	if sess == nil {
		return nil, fmt.Errorf("nil session")
	}
	lanClient := &http.Client{Timeout: 1200 * time.Millisecond}
	for _, raw := range sess.LAN {
		base := strings.TrimRight(raw, "/")
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/list", nil)
		if err != nil {
			continue
		}
		resp, err := lanClient.Do(req)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			continue
		}
		var list struct {
			Files []lanweb.File `json:"files"`
		}
		if err := json.Unmarshal(body, &list); err != nil {
			continue
		}
		return &ProbeResult{Via: "lan", LANBase: base, Files: list.Files}, nil
	}

	ws, err := dialGuestRelay(ctx, sess)
	if err != nil {
		return nil, err
	}
	defer ws.Close(websocket.StatusNormalClosure, "bye")
	files, err := relayList(ctx, ws)
	if err != nil {
		return nil, err
	}
	return &ProbeResult{Via: "relay", Files: files}, nil
}

// DownloadAll downloads all shared files into outDir, preferring LAN and falling back to relay.
func DownloadAll(ctx context.Context, sess *ResolvedSession, outDir string) (via string, saved []string, err error) {
	probe, err := ProbeTransport(ctx, sess)
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", nil, err
	}
	if probe.Via == "lan" {
		dlClient := &http.Client{Timeout: 10 * time.Minute}
		for _, f := range probe.Files {
			dest, derr := downloadLANFile(ctx, dlClient, probe.LANBase, f, outDir)
			if derr != nil {
				return "lan", saved, derr
			}
			saved = append(saved, dest)
		}
		return "lan", saved, nil
	}

	ws, err := dialGuestRelay(ctx, sess)
	if err != nil {
		return "", nil, err
	}
	defer ws.Close(websocket.StatusNormalClosure, "bye")
	for _, f := range probe.Files {
		dest, derr := relayDownloadFile(ctx, ws, f.Name, outDir)
		if derr != nil {
			return "relay", saved, derr
		}
		saved = append(saved, dest)
	}
	return "relay", saved, nil
}

// UploadFiles uploads local files to the host, preferring LAN and falling back to relay.
func UploadFiles(ctx context.Context, sess *ResolvedSession, paths []string) (via string, uploaded []string, err error) {
	if len(paths) == 0 {
		return "", nil, fmt.Errorf("no files selected")
	}
	probe, err := ProbeTransport(ctx, sess)
	if err != nil {
		return "", nil, err
	}
	if probe.Via == "lan" {
		upClient := &http.Client{Timeout: 30 * time.Minute}
		for _, p := range paths {
			if uerr := uploadLANFile(ctx, upClient, probe.LANBase, p); uerr != nil {
				return "lan", uploaded, uerr
			}
			uploaded = append(uploaded, filepath.Base(p))
		}
		return "lan", uploaded, nil
	}

	ws, err := dialGuestRelay(ctx, sess)
	if err != nil {
		return "", nil, err
	}
	defer ws.Close(websocket.StatusNormalClosure, "bye")
	for _, p := range paths {
		name, uerr := relayUploadFile(ctx, ws, p)
		if uerr != nil {
			return "relay", uploaded, uerr
		}
		uploaded = append(uploaded, name)
	}
	return "relay", uploaded, nil
}

func dialGuestRelay(ctx context.Context, sess *ResolvedSession) (*websocket.Conn, error) {
	wsBase := strings.Replace(strings.Replace(sess.BaseURL, "https://", "wss://", 1), "http://", "ws://", 1)
	u := wsBase + "/web/" + sess.Room + "?role=guest&token=" + url.QueryEscape(sess.GuestToken)
	c, _, err := websocket.Dial(ctx, u, nil)
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(4 << 20)
	return c, nil
}

func relayList(ctx context.Context, ws *websocket.Conn) ([]lanweb.File, error) {
	b, _ := json.Marshal(map[string]string{"t": "list"})
	if err := ws.Write(ctx, websocket.MessageText, b); err != nil {
		return nil, err
	}
	_, data, err := ws.Read(ctx)
	if err != nil {
		return nil, err
	}
	var msg struct {
		T     string        `json:"t"`
		Files []lanweb.File `json:"files"`
		Msg   string        `json:"msg"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, err
	}
	if msg.T == "err" {
		return nil, fmt.Errorf("%s", msg.Msg)
	}
	return msg.Files, nil
}

func downloadLANFile(ctx context.Context, client *http.Client, lanBase string, f lanweb.File, outDir string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, lanBase+"/f/"+url.PathEscape(f.Name), nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download %s: %s", f.Name, resp.Status)
	}
	dest := uniqueDestPath(outDir, f.Name)
	tmp := dest + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(out, h), resp.Body)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	gotHex := hex.EncodeToString(h.Sum(nil))
	if f.SHA256 != "" && gotHex != f.SHA256 {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("sha256 mismatch for %s", f.Name)
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	return dest, nil
}

func uploadLANFile(ctx context.Context, client *http.Client, lanBase, path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		fw, werr := mw.CreateFormFile("file", filepath.Base(path))
		if werr == nil {
			_, werr = io.Copy(fw, f)
		}
		if cerr := mw.Close(); werr == nil {
			werr = cerr
		}
		_ = pw.CloseWithError(werr)
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, lanBase+"/upload", pr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload %s: %s %s", filepath.Base(path), resp.Status, strings.TrimSpace(string(b)))
	}
	return nil
}

func relayDownloadFile(ctx context.Context, ws *websocket.Conn, name, outDir string) (string, error) {
	reqB, _ := json.Marshal(map[string]string{"t": "get", "name": name})
	if err := ws.Write(ctx, websocket.MessageText, reqB); err != nil {
		return "", err
	}
	dest := uniqueDestPath(outDir, name)
	tmp := dest + ".part"
	var out *os.File
	h := sha256.New()
	cleanup := func() {
		if out != nil {
			_ = out.Close()
			_ = os.Remove(tmp)
			out = nil
		}
	}
	defer cleanup()

	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			return "", err
		}
		if typ == websocket.MessageBinary {
			if out == nil {
				return "", fmt.Errorf("unexpected binary chunk")
			}
			if _, err := h.Write(data); err != nil {
				return "", err
			}
			if _, err := out.Write(data); err != nil {
				return "", err
			}
			continue
		}
		var msg struct {
			T      string `json:"t"`
			SHA256 string `json:"sha256"`
			Msg    string `json:"msg"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch msg.T {
		case "get.begin":
			out, err = os.Create(tmp)
			if err != nil {
				return "", err
			}
		case "get.end":
			if out == nil {
				return "", fmt.Errorf("missing get.begin")
			}
			if err := out.Close(); err != nil {
				return "", err
			}
			out = nil
			gotHex := hex.EncodeToString(h.Sum(nil))
			if msg.SHA256 != "" && gotHex != msg.SHA256 {
				_ = os.Remove(tmp)
				return "", fmt.Errorf("sha256 mismatch for %s", name)
			}
			if err := os.Rename(tmp, dest); err != nil {
				_ = os.Remove(tmp)
				return "", err
			}
			return dest, nil
		case "err":
			return "", fmt.Errorf("%s", msg.Msg)
		}
	}
}

func relayUploadFile(ctx context.Context, ws *websocket.Conn, path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	beginB, _ := json.Marshal(map[string]any{"t": "put.begin", "name": filepath.Base(path), "size": st.Size()})
	if err := ws.Write(ctx, websocket.MessageText, beginB); err != nil {
		return "", err
	}
	_, data, err := ws.Read(ctx)
	if err != nil {
		return "", err
	}
	var ready struct {
		T    string `json:"t"`
		Name string `json:"name"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(data, &ready); err != nil {
		return "", err
	}
	if ready.T == "err" {
		return "", fmt.Errorf("%s", ready.Msg)
	}
	if ready.T != "put.ready" {
		return "", fmt.Errorf("unexpected response %q", ready.T)
	}
	buf := make([]byte, webChunk)
	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			if werr := ws.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
				return "", werr
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", rerr
		}
	}
	if err := ws.Write(ctx, websocket.MessageText, []byte(`{"t":"put.end"}`)); err != nil {
		return "", err
	}
	_, doneData, err := ws.Read(ctx)
	if err != nil {
		return "", err
	}
	var done struct {
		T    string `json:"t"`
		Name string `json:"name"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(doneData, &done); err != nil {
		return "", err
	}
	if done.T == "err" {
		return "", fmt.Errorf("%s", done.Msg)
	}
	if done.T != "put.ok" {
		return "", fmt.Errorf("unexpected response %q", done.T)
	}
	return done.Name, nil
}

func uniqueDestPath(dir, name string) string {
	base := filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	if base == "" || base == "." || base == ".." {
		base = "file"
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	cand := filepath.Join(dir, base)
	for i := 2; ; i++ {
		if _, err := os.Stat(cand); os.IsNotExist(err) {
			return cand
		}
		cand = filepath.Join(dir, fmt.Sprintf("%s-%d%s", stem, i, ext))
	}
}

var _ = bytes.NewReader
