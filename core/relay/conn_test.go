package relay

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func wsEchoServer(t *testing.T, onConn func(*websocket.Conn)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		c.SetReadLimit(2 << 20)
		onConn(c)
	}))
}

func TestConnCoalescesSmallWritesAndFlushesOnClose(t *testing.T) {
	gotCh := make(chan []byte, 1)
	framesCh := make(chan int, 1)
	srv := wsEchoServer(t, func(c *websocket.Conn) {
		defer c.CloseNow()
		var buf bytes.Buffer
		frames := 0
		for {
			typ, data, err := c.Read(t.Context())
			if err != nil {
				break
			}
			if typ == websocket.MessageBinary {
				frames++
				buf.Write(data)
			}
		}
		framesCh <- frames
		gotCh <- buf.Bytes()
	})
	defer srv.Close()

	u := "ws" + strings.TrimPrefix(srv.URL, "http")
	c, err := Dial(t.Context(), u)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}

	// Small single write should flush promptly without waiting on a timer.
	start := time.Now()
	if _, err := c.Write([]byte("hello")); err != nil {
		t.Fatalf("Write small: %v", err)
	}
	if time.Since(start) > 200*time.Millisecond {
		t.Fatalf("small write took %v", time.Since(start))
	}

	// Burst of small writes followed immediately by Close must all flush intact.
	var want bytes.Buffer
	want.WriteString("hello")
	chunk := bytes.Repeat([]byte("A"), 16<<10)
	for i := 0; i < 32; i++ {
		if _, err := c.Write(chunk); err != nil {
			t.Fatalf("Write %d: %v", i, err)
		}
		want.Write(chunk)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got := <-gotCh
	frames := <-framesCh
	if !bytes.Equal(got, want.Bytes()) {
		t.Fatalf("received %d bytes, want %d bytes", len(got), want.Len())
	}
	if frames == 0 {
		t.Fatal("expected binary frames")
	}
}

func TestConnReadDeadlineCancelsBlockedRead(t *testing.T) {
	srv := wsEchoServer(t, func(c *websocket.Conn) {
		defer c.CloseNow()
		<-t.Context().Done()
	})
	defer srv.Close()

	u := "ws" + strings.TrimPrefix(srv.URL, "http")
	c, err := Dial(t.Context(), u)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	errCh := make(chan error, 1)
	go func() {
		buf := make([]byte, 16)
		_, rerr := c.Read(buf)
		errCh <- rerr
	}()

	time.Sleep(40 * time.Millisecond)
	_ = c.SetReadDeadline(time.Now())

	select {
	case rerr := <-errCh:
		if rerr == nil || rerr == io.EOF {
			t.Fatalf("expected deadline/cancel error, got %v", rerr)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked Read did not unblock on SetReadDeadline(now)")
	}
}
