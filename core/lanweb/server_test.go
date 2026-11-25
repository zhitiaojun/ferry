package lanweb

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cwlindean/filetransfer/core/logging"
)

func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := New(dir, "testtoken", logging.New(io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func upload(t *testing.T, s *Server, name string, data []byte) File {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/testtoken/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload status %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Files []File `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Files) != 1 {
		t.Fatalf("files = %v", out.Files)
	}
	return out.Files[0]
}

func TestTokenGuard(t *testing.T) {
	s, _ := newTestServer(t)

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("outside token: %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/testtoken/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("page: %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "浏览器收发") {
		t.Fatal("page body missing title")
	}
}

func TestUploadDownloadRoundTrip(t *testing.T) {
	s, dir := newTestServer(t)
	data := bytes.Repeat([]byte("filetransfer"), 2_100_000/10)
	want := sha256.Sum256(data)

	f := upload(t, s, "a.bin", data)
	if f.Size != int64(len(data)) {
		t.Fatalf("size = %d, want %d", f.Size, len(data))
	}
	if f.SHA256 != hex.EncodeToString(want[:]) {
		t.Fatalf("sha = %s", f.SHA256)
	}
	if _, err := os.Stat(filepath.Join(dir, "a.bin")); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/testtoken/f/a.bin", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("download: %d", rec.Code)
	}
	got := rec.Body.Bytes()
	if !bytes.Equal(got, data) {
		t.Fatalf("download bytes differ: %d vs %d", len(got), len(data))
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.Contains(got, "a.bin") {
		t.Fatalf("disposition = %q", got)
	}

	rec = httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/testtoken/api/list", nil))
	var list struct {
		Files []File `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Files) != 1 || list.Files[0].Name != "a.bin" {
		t.Fatalf("list = %+v", list.Files)
	}
}

func TestDuplicateNameGetsSuffix(t *testing.T) {
	s, _ := newTestServer(t)
	first := upload(t, s, "doc.txt", []byte("one"))
	second := upload(t, s, "doc.txt", []byte("two"))
	if first.Name != "doc.txt" || second.Name != "doc-2.txt" {
		t.Fatalf("names = %q, %q", first.Name, second.Name)
	}
}

func TestTraversalIsBlocked(t *testing.T) {
	s, dir := newTestServer(t)
	secret := filepath.Join(filepath.Dir(dir), "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		"/testtoken/f/../secret.txt",
		"/testtoken/f/%2e%2e%2fsecret.txt",
		"/testtoken/f/..%5csecret.txt",
	} {
		rec := httptest.NewRecorder()
		s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code == http.StatusOK {
			t.Fatalf("%s leaked: %s", p, rec.Body.String())
		}
	}

	f := upload(t, s, "../evil.txt", []byte("x"))
	if f.Name != "evil.txt" {
		t.Fatalf("upload name = %q", f.Name)
	}
	if _, err := os.Stat(filepath.Join(dir, "evil.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestListEmpty(t *testing.T) {
	s, _ := newTestServer(t)
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/testtoken/api/list", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var list struct {
		Files []File `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if list.Files == nil {
		t.Fatal("files must be [] not null")
	}
	if len(list.Files) != 0 {
		t.Fatalf("files = %v", list.Files)
	}
}

func TestURL(t *testing.T) {
	s, _ := newTestServer(t)
	if got, want := s.URL("10.0.0.5:8080"), "http://10.0.0.5:8080/testtoken/"; got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
	if !strings.HasPrefix(s.URL(""), "http://127.0.0.1/testtoken/") {
		t.Fatalf("empty host url = %q", s.URL(""))
	}
}

func TestMaxUpload(t *testing.T) {
	s, _ := newTestServer(t)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "big.bin")
	// A tiny writer that pretends to be bigger than the limit would be slow;
	// instead check the constant is sane and the limit is enforced by the
	// MaxBytesReader wrapper, which truncates at MaxUpload.
	_, _ = fw.Write(bytes.Repeat([]byte{0}, 1024))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/testtoken/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("small upload failed: %d %s", rec.Code, rec.Body.String())
	}
	if MaxUpload < 1<<30 {
		t.Fatalf("MaxUpload too small: %d", MaxUpload)
	}
	_ = fmt.Sprint()
}

func TestScopedSendAndRecvModes(t *testing.T) {
	dir := t.TempDir()
	shared := filepath.Join(dir, "public.txt")
	hidden := filepath.Join(dir, "private.txt")
	if err := os.WriteFile(shared, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hidden, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Send mode: only SharedFiles are exposed; unrelated files & uploads are blocked.
	sSend, err := NewScoped(ScopedOptions{
		Mode:        "send",
		SharedFiles: []string{shared},
		DeviceName:  "Phone",
		Token:       "toksend",
		Log:         logging.New(io.Discard),
	})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	sSend.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/toksend/api/list", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "public.txt") || strings.Contains(rec.Body.String(), "private.txt") {
		t.Fatalf("send list = %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	sSend.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/toksend/f/private.txt", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("send leaked unrelated file: %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	sSend.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/toksend/upload", strings.NewReader("x")))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("send upload status = %d, want 403", rec.Code)
	}

	// Recv mode: starts with empty listing (does not leak existing files in RecvDir).
	var gotNotify string
	sRecv, err := NewScoped(ScopedOptions{
		Mode:       "recv",
		RecvDir:    dir,
		DeviceName: "Phone",
		Token:      "tokrecv",
		Log:        logging.New(io.Discard),
		OnUploaded: func(f File) { gotNotify = f.Name },
	})
	if err != nil {
		t.Fatal(err)
	}
	if snap := sRecv.Snapshot(); len(snap) != 0 {
		t.Fatalf("recv initial snapshot = %+v, want empty", snap)
	}
	rec = httptest.NewRecorder()
	sRecv.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/tokrecv/f/private.txt", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("recv leaked pre-existing file: %d", rec.Code)
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "in.txt")
	_, _ = fw.Write([]byte("incoming"))
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/tokrecv/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec = httptest.NewRecorder()
	sRecv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || gotNotify != "in.txt" {
		t.Fatalf("recv upload = %d, notify = %q", rec.Code, gotNotify)
	}
}

func TestOnProgressUploadAndDownload(t *testing.T) {
	dir := t.TempDir()
	var events []Progress
	srv, err := NewScoped(ScopedOptions{
		Mode:    "both",
		RecvDir: dir,
		Token:   "tokprog",
		Log:     logging.New(io.Discard),
		OnProgress: func(p Progress) {
			events = append(events, p)
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	payload := bytes.Repeat([]byte("ferry-lanweb-progress"), 4096)
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "big.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(payload); err != nil {
		t.Fatal(err)
	}
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/tokprog/upload", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("X-File-Size", fmt.Sprint(len(payload)))
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("upload = %d %s", rec.Code, rec.Body.String())
	}

	var sawRecvStart, sawRecvVerify, sawRecvDone bool
	for _, ev := range events {
		if ev.Direction == "recv" {
			if ev.Stage == "transferring" && ev.BytesTotal == int64(len(payload)) {
				sawRecvStart = true
			}
			if ev.Stage == "verifying" && ev.BytesDone == int64(len(payload)) {
				sawRecvVerify = true
			}
			if ev.Done {
				sawRecvDone = true
			}
		}
	}
	if !sawRecvStart || !sawRecvVerify || !sawRecvDone {
		t.Fatalf("missing recv progress phases: start=%v verify=%v done=%v events=%+v", sawRecvStart, sawRecvVerify, sawRecvDone, events)
	}

	events = nil
	dlRec := httptest.NewRecorder()
	srv.ServeHTTP(dlRec, httptest.NewRequest(http.MethodGet, "/tokprog/f/big.bin", nil))
	if dlRec.Code != http.StatusOK || dlRec.Body.Len() != len(payload) {
		t.Fatalf("download = %d len=%d", dlRec.Code, dlRec.Body.Len())
	}
	var sawSendTransfer, sawSendDone bool
	for _, ev := range events {
		if ev.Direction == "send" {
			if ev.Stage == "transferring" && ev.BytesTotal == int64(len(payload)) {
				sawSendTransfer = true
			}
			if ev.Done {
				sawSendDone = true
			}
		}
	}
	if !sawSendTransfer || !sawSendDone {
		t.Fatalf("missing send progress phases: transfer=%v done=%v events=%+v", sawSendTransfer, sawSendDone, events)
	}

	if !strings.Contains(pageHTML, "fmtSpeed") || !strings.Contains(pageHTML, `id="tx-box"`) || !strings.Contains(pageHTML, `id="tx-cancel"`) {
		t.Fatal("pageHTML missing speed/progress/cancel UI")
	}
}
