package server

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
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

func TestWebPublicSiteAndTempRoutes(t *testing.T) {
	_, hs := newWebTest(t)

	resp, page := doJSON(t, "GET", hs.URL+"/", nil, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("home status = %d", resp.StatusCode)
	}
	if !strings.Contains(string(page), "轻渡 Ferry") || !strings.Contains(string(page), "/transfer") {
		t.Fatalf("home page missing brand or /transfer link")
	}

	if resp, _ := doJSON(t, "GET", hs.URL+"/transfer", nil, nil); resp.StatusCode != 200 {
		t.Fatalf("/transfer without cookie = %d, want 200", resp.StatusCode)
	}
	if resp, _ := doJSON(t, "GET", hs.URL+"/s/abc123", nil, nil); resp.StatusCode != 200 {
		t.Fatalf("/s/abc123 without cookie = %d, want 200", resp.StatusCode)
	}
	if resp, _ := doJSON(t, "GET", hs.URL+"/s/", nil, nil); resp.StatusCode != 404 {
		t.Fatalf("/s/ empty token = %d, want 404", resp.StatusCode)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Now().Unix()
	canonical := "ferry-temp-v1\n" + strconv.FormatInt(ts, 10) + "\nsend\nMyPhone"
	sig := hex.EncodeToString(ed25519.Sign(priv, []byte(canonical)))

	// Invalid signature must be rejected.
	if resp, _ := doJSON(t, "POST", hs.URL+"/api/temp/create", map[string]any{
		"name": "MyPhone", "mode": "send", "ts": ts,
		"pub": hex.EncodeToString(pub), "sig": strings.Repeat("00", 64),
	}, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("bad sig = %d, want 403", resp.StatusCode)
	}

	resp, body := doJSON(t, "POST", hs.URL+"/api/temp/create", map[string]any{
		"name": "MyPhone", "mode": "send", "lan": []string{"http://192.168.1.20:8080/tok/"},
		"ts": ts, "pub": hex.EncodeToString(pub), "sig": sig,
	}, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("temp create = %d %s", resp.StatusCode, body)
	}
	var created struct {
		Code       string `json:"code"`
		ShareToken string `json:"share_token"`
		Room       string `json:"room"`
		HostToken  string `json:"host_token"`
	}
	if err := json.Unmarshal(body, &created); err != nil || len(created.Code) != 6 || created.ShareToken == "" {
		t.Fatalf("create body = %s", body)
	}

	// Resolve by share token and by 6-digit code without cookie.
	resp, rbody := doJSON(t, "GET", hs.URL+"/api/temp/resolve?token="+created.ShareToken, nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(rbody), created.Room) {
		t.Fatalf("resolve token = %d %s", resp.StatusCode, rbody)
	}
	resp, rbody = doJSON(t, "GET", hs.URL+"/api/temp/resolve?code="+created.Code, nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(string(rbody), created.ShareToken) {
		t.Fatalf("resolve code = %d %s", resp.StatusCode, rbody)
	}

	// Guest WebSocket authorized by ?token=ShareToken (no cookie needed).
	wsURL := strings.Replace(hs.URL, "http://", "ws://", 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hostWS, _, err := websocket.Dial(ctx, wsURL+"/web/"+created.Room+"?role=host&token="+created.HostToken, nil)
	if err != nil {
		t.Fatalf("host ws dial: %v", err)
	}
	defer hostWS.CloseNow()
	guestWS, _, err := websocket.Dial(ctx, wsURL+"/web/"+created.Room+"?role=guest&token="+created.ShareToken, nil)
	if err != nil {
		t.Fatalf("guest ws dial: %v", err)
	}
	defer guestWS.CloseNow()
	if err := hostWS.Write(ctx, websocket.MessageText, []byte("ping")); err != nil {
		t.Fatal(err)
	}
	if _, got, err := guestWS.Read(ctx); err != nil || string(got) != "ping" {
		t.Fatalf("guest read = %q %v", got, err)
	}

	// Close room invalidates resolve.
	if resp, _ := doJSON(t, "POST", hs.URL+"/api/temp/close", map[string]string{
		"room": created.Room, "host_token": created.HostToken,
	}, nil); resp.StatusCode != 200 {
		t.Fatalf("temp close = %d", resp.StatusCode)
	}
	if resp, _ := doJSON(t, "GET", hs.URL+"/api/temp/resolve?token="+created.ShareToken, nil, nil); resp.StatusCode != 404 {
		t.Fatalf("resolve after close = %d, want 404", resp.StatusCode)
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

// TestWebRoomRecreatedAfterClose guards the reconnection path: once a pump
// tears a room down, the next attach must get a fresh room instead of the
// spent one, otherwise every reconnect closes immediately after ready.
func TestWebRoomRecreatedAfterClose(t *testing.T) {
	w, hs := newWebTest(t)
	ck := cookieFor(w)
	_, body := doJSON(t, "POST", hs.URL+"/api/devices", map[string]any{"code": "112233", "name": "pc"}, ck)
	var reg struct {
		Room  string `json:"room"`
		Token string `json:"device_token"`
	}
	if err := json.Unmarshal(body, &reg); err != nil {
		t.Fatal(err)
	}
	wsURL := strings.Replace(hs.URL, "http://", "ws://", 1)

	dialHost := func() *websocket.Conn {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c, _, err := websocket.Dial(ctx, wsURL+"/web/"+reg.Room+"?role=host", &websocket.DialOptions{
			HTTPHeader: http.Header{"X-Ft-Device-Token": []string{reg.Token}},
		})
		if err != nil {
			t.Fatalf("host dial: %v", err)
		}
		return c
	}
	dialGuest := func() *websocket.Conn {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c, _, err := websocket.Dial(ctx, wsURL+"/web/"+reg.Room+"?role=guest", &websocket.DialOptions{
			HTTPHeader: http.Header{"Cookie": []string{"ftw=" + ck.Value}},
		})
		if err != nil {
			t.Fatalf("guest dial: %v", err)
		}
		return c
	}
	echo := func(host, guest *websocket.Conn, msg string) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := host.Write(ctx, websocket.MessageText, []byte(msg)); err != nil {
			t.Fatalf("host write: %v", err)
		}
		_, data, err := guest.Read(ctx)
		if err != nil || string(data) != msg {
			t.Fatalf("guest read = %q %v", data, err)
		}
	}

	host, guest := dialHost(), dialGuest()
	echo(host, guest, "first")
	_ = guest.Close(websocket.StatusNormalClosure, "done")
	time.Sleep(100 * time.Millisecond)

	host2, guest2 := dialHost(), dialGuest()
	defer host2.Close(websocket.StatusNormalClosure, "")
	defer guest2.Close(websocket.StatusNormalClosure, "")
	echo(host2, guest2, "second")
}
