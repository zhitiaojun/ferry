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
	"strconv"
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
	res, ws, err := probeAndKeepRelay(ctx, sess)
	if ws != nil {
		_ = ws.CloseNow()
	}
	return res, err
}

func probeAndKeepRelay(ctx context.Context, sess *ResolvedSession) (*ProbeResult, *websocket.Conn, error) {
	if sess == nil {
		return nil, nil, fmt.Errorf("nil session")
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
		return &ProbeResult{Via: "lan", LANBase: base, Files: list.Files}, nil, nil
	}

	ws, err := dialGuestRelay(ctx, sess)
	if err != nil {
		return nil, nil, err
	}
	files, err := relayList(ctx, ws)
	if err != nil {
		_ = ws.CloseNow()
		return nil, nil, err
	}
	return &ProbeResult{Via: "relay", Files: files}, ws, nil
}

// DownloadAll downloads all shared files into outDir, preferring LAN and falling back to relay.
func DownloadAll(ctx context.Context, sess *ResolvedSession, outDir string) (via string, saved []string, err error) {
	return DownloadAllWithProgress(ctx, sess, outDir, nil)
}

// DownloadAllWithProgress downloads all shared files into outDir, preferring LAN
// and falling back to relay, reporting live transfer progress via onProgress.
func DownloadAllWithProgress(ctx context.Context, sess *ResolvedSession, outDir string, onProgress func(TransferProgress)) (via string, saved []string, err error) {
	probe, ws, err := probeAndKeepRelay(ctx, sess)
	if err != nil {
		return "", nil, err
	}
	if ws != nil {
		defer ws.CloseNow()
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", nil, err
	}

	var totalBytes int64
	for _, f := range probe.Files {
		totalBytes += f.Size
	}
	fileCount := len(probe.Files)
	txID := lanweb.NextProgressID()
	if onProgress != nil && fileCount > 0 {
		onProgress(TransferProgress{
			ID:         txID,
			Direction:  "recv",
			Channel:    probe.Via,
			Stage:      "connecting",
			FileName:   probe.Files[0].Name,
			FileIndex:  1,
			FileCount:  fileCount,
			BytesDone:  0,
			BytesTotal: totalBytes,
		})
		defer onProgress(TransferProgress{
			ID:        txID,
			Direction: "recv",
			Channel:   probe.Via,
			Done:      true,
		})
	}

	var completed int64
	if probe.Via == "lan" {
		dlClient := &http.Client{Timeout: 10 * time.Minute}
		for idx, f := range probe.Files {
			dest, derr := downloadLANFile(ctx, dlClient, probe.LANBase, f, outDir, txID, idx+1, fileCount, completed, totalBytes, onProgress)
			if derr != nil {
				return "lan", saved, derr
			}
			completed += f.Size
			saved = append(saved, dest)
		}
		return "lan", saved, nil
	}

	for idx, f := range probe.Files {
		dest, derr := relayDownloadFile(ctx, ws, f.Name, outDir, txID, idx+1, fileCount, completed, totalBytes, f.Size, onProgress)
		if derr != nil {
			return "relay", saved, derr
		}
		completed += f.Size
		saved = append(saved, dest)
	}
	return "relay", saved, nil
}

// UploadFiles uploads local files to the host, preferring LAN and falling back to relay.
func UploadFiles(ctx context.Context, sess *ResolvedSession, paths []string) (via string, uploaded []string, err error) {
	return UploadFilesWithProgress(ctx, sess, paths, nil)
}

// UploadFilesWithProgress uploads local files to the host, preferring LAN and
// falling back to relay, reporting live transfer progress via onProgress.
func UploadFilesWithProgress(ctx context.Context, sess *ResolvedSession, paths []string, onProgress func(TransferProgress)) (via string, uploaded []string, err error) {
	if len(paths) == 0 {
		return "", nil, fmt.Errorf("no files selected")
	}
	sizes := make([]int64, len(paths))
	var totalBytes int64
	for i, p := range paths {
		if st, serr := os.Stat(p); serr == nil {
			sizes[i] = st.Size()
			totalBytes += st.Size()
		}
	}
	probe, ws, err := probeAndKeepRelay(ctx, sess)
	if err != nil {
		return "", nil, err
	}
	if ws != nil {
		defer ws.CloseNow()
	}

	fileCount := len(paths)
	txID := lanweb.NextProgressID()
	if onProgress != nil {
		onProgress(TransferProgress{
			ID:         txID,
			Direction:  "send",
			Channel:    probe.Via,
			Stage:      "connecting",
			FileName:   filepath.Base(paths[0]),
			FileIndex:  1,
			FileCount:  fileCount,
			BytesDone:  0,
			BytesTotal: totalBytes,
		})
		defer onProgress(TransferProgress{
			ID:        txID,
			Direction: "send",
			Channel:   probe.Via,
			Done:      true,
		})
	}

	var completed int64
	if probe.Via == "lan" {
		upClient := &http.Client{Timeout: 30 * time.Minute}
		for idx, p := range paths {
			if uerr := uploadLANFile(ctx, upClient, probe.LANBase, p, txID, idx+1, fileCount, completed, totalBytes, onProgress); uerr != nil {
				return "lan", uploaded, uerr
			}
			completed += sizes[idx]
			uploaded = append(uploaded, filepath.Base(p))
		}
		return "lan", uploaded, nil
	}

	for idx, p := range paths {
		name, uerr := relayUploadFile(ctx, ws, p, txID, idx+1, fileCount, completed, totalBytes, onProgress)
		if uerr != nil {
			return "relay", uploaded, uerr
		}
		completed += sizes[idx]
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

type progressIO struct {
	r          io.Reader
	txID       uint64
	direction  string
	channel    string
	fileName   string
	fileIndex  int
	fileCount  int
	baseBytes  int64
	totalBytes int64
	curBytes   int64
	lastEmit   time.Time
	onProgress func(TransferProgress)
}

func (p *progressIO) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 && p.onProgress != nil {
		p.curBytes += int64(n)
		done := p.baseBytes + p.curBytes
		now := time.Now()
		if now.Sub(p.lastEmit) >= 120*time.Millisecond || (p.totalBytes > 0 && done >= p.totalBytes) {
			p.lastEmit = now
			p.onProgress(TransferProgress{
				ID:         p.txID,
				Direction:  p.direction,
				Channel:    p.channel,
				Stage:      "transferring",
				FileName:   p.fileName,
				FileIndex:  p.fileIndex,
				FileCount:  p.fileCount,
				BytesDone:  done,
				BytesTotal: p.totalBytes,
			})
		}
	}
	return n, err
}

func downloadLANFile(ctx context.Context, client *http.Client, lanBase string, f lanweb.File, outDir string, txID uint64, fileIdx, fileCount int, completedBefore, totalBytes int64, onProgress func(TransferProgress)) (string, error) {
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
	if onProgress != nil {
		onProgress(TransferProgress{
			ID:         txID,
			Direction:  "recv",
			Channel:    "lan",
			Stage:      "transferring",
			FileName:   f.Name,
			FileIndex:  fileIdx,
			FileCount:  fileCount,
			BytesDone:  completedBefore,
			BytesTotal: totalBytes,
		})
	}
	h := sha256.New()
	src := io.Reader(resp.Body)
	if onProgress != nil {
		src = &progressIO{
			r:          resp.Body,
			txID:       txID,
			direction:  "recv",
			channel:    "lan",
			fileName:   f.Name,
			fileIndex:  fileIdx,
			fileCount:  fileCount,
			baseBytes:  completedBefore,
			totalBytes: totalBytes,
			lastEmit:   time.Now(),
			onProgress: onProgress,
		}
	}
	n, err := io.Copy(io.MultiWriter(out, h), src)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if onProgress != nil {
		onProgress(TransferProgress{
			ID:         txID,
			Direction:  "recv",
			Channel:    "lan",
			Stage:      "verifying",
			FileName:   f.Name,
			FileIndex:  fileIdx,
			FileCount:  fileCount,
			BytesDone:  completedBefore + n,
			BytesTotal: totalBytes,
		})
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

func uploadLANFile(ctx context.Context, client *http.Client, lanBase, path string, txID uint64, fileIdx, fileCount int, completedBefore, totalBytes int64, onProgress func(TransferProgress)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	fname := filepath.Base(path)
	if onProgress != nil {
		onProgress(TransferProgress{
			ID:         txID,
			Direction:  "send",
			Channel:    "lan",
			Stage:      "transferring",
			FileName:   fname,
			FileIndex:  fileIdx,
			FileCount:  fileCount,
			BytesDone:  completedBefore,
			BytesTotal: totalBytes,
		})
	}
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	go func() {
		fw, werr := mw.CreateFormFile("file", fname)
		var copied int64
		if werr == nil {
			src := io.Reader(f)
			if onProgress != nil {
				src = &progressIO{
					r:          f,
					txID:       txID,
					direction:  "send",
					channel:    "lan",
					fileName:   fname,
					fileIndex:  fileIdx,
					fileCount:  fileCount,
					baseBytes:  completedBefore,
					totalBytes: totalBytes,
					lastEmit:   time.Now(),
					onProgress: onProgress,
				}
			}
			copied, werr = io.Copy(fw, src)
		}
		if cerr := mw.Close(); werr == nil {
			werr = cerr
		}
		if werr == nil && onProgress != nil {
			onProgress(TransferProgress{
				ID:         txID,
				Direction:  "send",
				Channel:    "lan",
				Stage:      "verifying",
				FileName:   fname,
				FileIndex:  fileIdx,
				FileCount:  fileCount,
				BytesDone:  completedBefore + copied,
				BytesTotal: totalBytes,
			})
		}
		_ = pw.CloseWithError(werr)
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, lanBase+"/upload", pr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-File-Size", strconv.FormatInt(st.Size(), 10))
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("upload %s: %s %s", fname, resp.Status, strings.TrimSpace(string(b)))
	}
	return nil
}

func relayDownloadFile(ctx context.Context, ws *websocket.Conn, name, outDir string, txID uint64, fileIdx, fileCount int, completedBefore, totalBytes, fileSize int64, onProgress func(TransferProgress)) (string, error) {
	reqB, _ := json.Marshal(map[string]string{"t": "get", "name": name})
	if err := ws.Write(ctx, websocket.MessageText, reqB); err != nil {
		return "", err
	}
	dest := uniqueDestPath(outDir, name)
	tmp := dest + ".part"
	var out *os.File
	h := sha256.New()
	var written int64
	lastEmit := time.Now()
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
			written += int64(len(data))
			if onProgress != nil {
				now := time.Now()
				done := completedBefore + written
				if now.Sub(lastEmit) >= 120*time.Millisecond || (totalBytes > 0 && done >= totalBytes) {
					lastEmit = now
					onProgress(TransferProgress{
						ID:         txID,
						Direction:  "recv",
						Channel:    "relay",
						Stage:      "transferring",
						FileName:   name,
						FileIndex:  fileIdx,
						FileCount:  fileCount,
						BytesDone:  done,
						BytesTotal: totalBytes,
					})
				}
			}
			continue
		}
		var msg struct {
			T      string `json:"t"`
			Size   int64  `json:"size"`
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
			if totalBytes <= 0 && msg.Size > 0 {
				totalBytes = completedBefore + msg.Size
			}
			if onProgress != nil {
				onProgress(TransferProgress{
					ID:         txID,
					Direction:  "recv",
					Channel:    "relay",
					Stage:      "transferring",
					FileName:   name,
					FileIndex:  fileIdx,
					FileCount:  fileCount,
					BytesDone:  completedBefore,
					BytesTotal: totalBytes,
				})
			}
		case "get.end":
			if out == nil {
				return "", fmt.Errorf("missing get.begin")
			}
			if onProgress != nil {
				onProgress(TransferProgress{
					ID:         txID,
					Direction:  "recv",
					Channel:    "relay",
					Stage:      "verifying",
					FileName:   name,
					FileIndex:  fileIdx,
					FileCount:  fileCount,
					BytesDone:  completedBefore + written,
					BytesTotal: totalBytes,
				})
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

func relayUploadFile(ctx context.Context, ws *websocket.Conn, path string, txID uint64, fileIdx, fileCount int, completedBefore, totalBytes int64, onProgress func(TransferProgress)) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	fname := filepath.Base(path)
	beginB, _ := json.Marshal(map[string]any{"t": "put.begin", "name": fname, "size": st.Size()})
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
	if onProgress != nil {
		onProgress(TransferProgress{
			ID:         txID,
			Direction:  "send",
			Channel:    "relay",
			Stage:      "transferring",
			FileName:   fname,
			FileIndex:  fileIdx,
			FileCount:  fileCount,
			BytesDone:  completedBefore,
			BytesTotal: totalBytes,
		})
	}
	buf := make([]byte, webChunk)
	var sent int64
	lastEmit := time.Now()
	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			if werr := ws.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
				return "", werr
			}
			sent += int64(n)
			if onProgress != nil {
				now := time.Now()
				done := completedBefore + sent
				if now.Sub(lastEmit) >= 120*time.Millisecond || (totalBytes > 0 && done >= totalBytes) {
					lastEmit = now
					onProgress(TransferProgress{
						ID:         txID,
						Direction:  "send",
						Channel:    "relay",
						Stage:      "transferring",
						FileName:   fname,
						FileIndex:  fileIdx,
						FileCount:  fileCount,
						BytesDone:  done,
						BytesTotal: totalBytes,
					})
				}
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return "", rerr
		}
	}
	if onProgress != nil {
		onProgress(TransferProgress{
			ID:         txID,
			Direction:  "send",
			Channel:    "relay",
			Stage:      "verifying",
			FileName:   fname,
			FileIndex:  fileIdx,
			FileCount:  fileCount,
			BytesDone:  completedBefore + sent,
			BytesTotal: totalBytes,
		})
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
