package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func newWebTest(t *testing.T) (*Web, *httptest.Server) {
	t.Helper()
	w := NewWeb("s3cret", DefaultWebLimits(), nil)
	mux := http.NewServeMux()
	w.registerRoutes(mux)
	hs := httptest.NewServer(mux)
	t.Cleanup(hs.Close)
	return w, hs
}

func cookieFor(w *Web) *http.Cookie {
	return &http.Cookie{Name: "ftw", Value: w.sign(time.Now().Add(time.Hour))}
}

func doJSON(t *testing.T, method, url string, body any, ck *http.Cookie) (*http.Response, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if ck != nil {
		req.AddCookie(ck)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	out, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, out
}

func TestWebDecoyHidesTransferUI(t *testing.T) {
	w, hs := newWebTest(t)

	resp, page := doJSON(t, "GET", hs.URL+"/", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("decoy status = %d", resp.StatusCode)
	}
	if !strings.Contains(string(page), "山径笔记") {
		t.Fatalf("decoy page is not the disguise")
	}
	if strings.Contains(string(page), "传文件") {
		t.Fatalf("decoy must not mention the transfer UI")
	}

	if resp, _ := doJSON(t, "GET", hs.URL+"/app", nil, nil); resp.StatusCode != 404 {
		t.Fatalf("app without cookie = %d, want 404", resp.StatusCode)
	}
	if resp, _ := doJSON(t, "GET", hs.URL+"/app", nil, cookieFor(w)); resp.StatusCode != 200 {
		t.Fatalf("app with cookie = %d, want 200", resp.StatusCode)
	}

	// The wrong password is rejected slowly enough to make guessing boring.
	start := time.Now()
	resp, _ = doJSON(t, "POST", hs.URL+"/api/unlock", map[string]string{"password": "nope"}, nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("bad unlock = %d", resp.StatusCode)
	}
	if el := time.Since(start); el < 300*time.Millisecond {
		t.Fatalf("bad unlock returned in %s, want a deliberate delay", el)
	}
}

func TestWebDeviceRegisterAndResolve(t *testing.T) {
	w, hs := newWebTest(t)
	ck := cookieFor(w)

	if resp, _ := doJSON(t, "POST", hs.URL+"/api/devices", map[string]any{"code": "123456"}, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("register without cookie = %d, want 403", resp.StatusCode)
	}

	resp, body := doJSON(t, "POST", hs.URL+"/api/devices", map[string]any{
		"code": "123456", "name": "pc", "lan": []string{"http://10.0.0.5:8080/tok/"},
	}, ck)
	if resp.StatusCode != 200 {
		t.Fatalf("register = %d %s", resp.StatusCode, body)
	}
	var reg struct {
		Room  string `json:"room"`
		Token string `json:"device_token"`
	}
	if err := json.Unmarshal(body, &reg); err != nil || reg.Room == "" || reg.Token == "" {
		t.Fatalf("register response = %s", body)
	}

	if resp, _ := doJSON(t, "GET", hs.URL+"/api/resolve?code=123456", nil, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("resolve without cookie = %d", resp.StatusCode)
	}

	resp, body = doJSON(t, "GET", hs.URL+"/api/resolve?code=123456", nil, ck)
	if resp.StatusCode != 200 {
		t.Fatalf("resolve = %d %s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), reg.Room) || !strings.Contains(string(body), "10.0.0.5") {
		t.Fatalf("resolve body = %s", body)
	}

	// Re-registering with the same code and a wrong token is refused, so a
	// guessed code cannot hijack an active session.
	resp, _ = doJSON(t, "POST", hs.URL+"/api/devices", map[string]any{
		"code": "123456", "name": "evil", "device_token": "deadbeef",
	}, ck)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("hijack attempt = %d, want 403", resp.StatusCode)
	}

	if resp, _ := doJSON(t, "GET", hs.URL+"/api/resolve?code=000000", nil, ck); resp.StatusCode != 404 {
		t.Fatalf("unknown code = %d, want 404", resp.StatusCode)
	}
}

func TestWebRelayPipesBetweenGuestAndHost(t *testing.T) {
	w, hs := newWebTest(t)
	ck := cookieFor(w)
	_, body := doJSON(t, "POST", hs.URL+"/api/devices", map[string]any{"code": "654321", "name": "pc"}, ck)
	var reg struct {
		Room  string `json:"room"`
		Token string `json:"device_token"`
	}
	if err := json.Unmarshal(body, &reg); err != nil {
		t.Fatal(err)
	}

	wsURL := strings.Replace(hs.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	host, _, err := websocket.Dial(ctx, wsURL+"/web/"+reg.Room+"?role=host", &websocket.DialOptions{
		HTTPHeader: http.Header{"X-Ft-Device-Token": []string{reg.Token}},
	})
	if err != nil {
		t.Fatalf("host dial: %v", err)
	}
	defer host.Close(websocket.StatusNormalClosure, "")

	guest, _, err := websocket.Dial(ctx, wsURL+"/web/"+reg.Room+"?role=guest", &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": []string{"ftw=" + ck.Value}},
	})
	if err != nil {
		t.Fatalf("guest dial: %v", err)
	}
	defer guest.Close(websocket.StatusNormalClosure, "")

	if err := host.Write(ctx, websocket.MessageText, []byte("from-host")); err != nil {
		t.Fatalf("host write: %v", err)
	}
	typ, data, err := guest.Read(ctx)
	if err != nil || string(data) != "from-host" || typ != websocket.MessageText {
		t.Fatalf("guest read = %q %v", data, err)
	}

	if err := guest.Write(ctx, websocket.MessageBinary, []byte{1, 2, 3}); err != nil {
		t.Fatalf("guest write: %v", err)
	}
	typ, data, err = host.Read(ctx)
	if err != nil || !bytes.Equal(data, []byte{1, 2, 3}) || typ != websocket.MessageBinary {
		t.Fatalf("host read = %v %v", data, err)
	}
}
