package signaling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"

	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/logging"
)

// ErrClosed is returned by calls after the connection has been closed.
var ErrClosed = errors.New("signaling: connection closed")

// Client is a logged-in signaling connection.
//
// One goroutine (started by Dial) reads messages; Call waits for the reply
// carrying the same envelope id, everything else goes to registered handlers.
type Client struct {
	url  string
	conn *websocket.Conn
	key  *identity.Key
	log  *logging.Logger

	mu       sync.Mutex
	waiters  map[string]chan *Envelope
	handlers map[string]func(*Envelope)
	closed   bool
	err      error
	done     chan struct{}

	seq atomic.Uint64
}

// Dial connects to url (wss://…/signal), performs the Ed25519 login handshake
// and starts the read loop.
func Dial(ctx context.Context, url string, key *identity.Key, log *logging.Logger) (*Client, error) {
	if key == nil {
		return nil, errors.New("signaling: nil identity key")
	}
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{})
	if err != nil {
		return nil, fmt.Errorf("signaling: dial %s: %w", url, err)
	}
	conn.SetReadLimit(MaxMessage)
	c := &Client{
		url:      url,
		conn:     conn,
		key:      key,
		log:      log,
		waiters:  make(map[string]chan *Envelope),
		handlers: make(map[string]func(*Envelope)),
		done:     make(chan struct{}),
	}
	if err := c.login(ctx); err != nil {
		conn.Close(websocket.StatusPolicyViolation, "login failed")
		return nil, err
	}
	go c.readLoop()
	if KeepaliveInterval > 0 {
		go c.keepalive(KeepaliveInterval)
	}
	return c, nil
}

// KeepaliveInterval is how often a logged-in client pings the server.
//
// It exists because middleboxes drop quiet connections: ESA tears down an idle
// WebSocket after ~30 s (measured 2026-11-07: a connection whose first message
// arrived at 30 s was already gone; one pinging every 28 s survived), and the
// server drops sessions silent for PingIdle (90 s). 15 s is the protocol
// heartbeat (§6) and leaves a wide margin under the ~30 s path timeout.
var KeepaliveInterval = 15 * time.Second

func (c *Client) keepalive(every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_, err := c.Ping(ctx)
			cancel()
			if err != nil {
				c.fail(err)
				return
			}
		case <-c.done:
			return
		}
	}
}

// login runs the four-step challenge handshake (protocol §8.2).
func (c *Client) login(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	begin, err := Build(TypeLoginBegin, "", LoginBegin{DeviceID: c.key.IDHex(), PubKey: c.key.PubHex()})
	if err != nil {
		return err
	}
	if err := c.write(ctx, begin); err != nil {
		return err
	}
	challenge, err := c.readUntil(ctx, TypeLoginChal)
	if err != nil {
		return err
	}
	ch, err := Decode[LoginChallenge](challenge)
	if err != nil {
		return err
	}
	if ch.Nonce == "" {
		return errors.New("signaling: empty login nonce")
	}
	sig := c.key.Sign(identity.LoginMessage(c.key.ID(), ch.Nonce))

	login, err := Build(TypeLogin, challenge.ID, Login{Signature: hexEncode(sig)})
	if err != nil {
		return err
	}
	if err := c.write(ctx, login); err != nil {
		return err
	}
	ok, err := c.readUntil(ctx, TypeLoginOK)
	if err != nil {
		return err
	}
	body, err := Decode[LoginOK](ok)
	if err != nil {
		return err
	}
	if c.log != nil {
		c.log.Event("signal.login", map[string]any{
			"device_id":   c.key.IDHex(),
			"server_time": body.ServerTime,
			"peers":       len(body.Peers),
		})
	}
	return nil
}

// readUntil reads the next envelope of type want, failing on error envelopes.
func (c *Client) readUntil(ctx context.Context, want string) (*Envelope, error) {
	for {
		typ, data, err := c.conn.Read(ctx)
		if err != nil {
			return nil, fmt.Errorf("signaling: read: %w", err)
		}
		if typ != websocket.MessageText {
			return nil, fmt.Errorf("signaling: unexpected %v frame during handshake", typ)
		}
		e, err := Parse(data)
		if err != nil {
			return nil, err
		}
		if e.Type == TypeError {
			body, _ := Decode[ErrorBody](e)
			return nil, fmt.Errorf("signaling: server error %s: %s", body.Code, body.Message)
		}
		if e.Type != want {
			return nil, fmt.Errorf("signaling: want %s, got %s", want, e.Type)
		}
		return e, nil
	}
}

// readLoop dispatches replies to waiters and events to handlers.
func (c *Client) readLoop() {
	defer c.fail(ErrClosed)
	for {
		typ, data, err := c.conn.Read(context.Background())
		if err != nil {
			c.fail(err)
			return
		}
		if typ != websocket.MessageText {
			c.fail(fmt.Errorf("signaling: unexpected %v frame", typ))
			return
		}
		e, err := Parse(data)
		if err != nil {
			c.fail(err)
			return
		}
		if e.ID != "" {
			c.mu.Lock()
			ch, ok := c.waiters[e.ID]
			if ok {
				delete(c.waiters, e.ID)
			}
			c.mu.Unlock()
			if ok {
				ch <- e
				continue
			}
		}
		c.mu.Lock()
		h := c.handlers[e.Type]
		c.mu.Unlock()
		if h != nil {
			h(e)
		}
	}
}

// On registers a handler for one message type. It must be called before the
// messages arrive; handlers run on the read goroutine and must not block.
func (c *Client) On(typ string, h func(*Envelope)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers[typ] = h
}

// Send transmits a message without waiting for a reply.
func (c *Client) Send(ctx context.Context, typ string, data any) error {
	e, err := Build(typ, "", data)
	if err != nil {
		return err
	}
	return c.write(ctx, e)
}

// Call sends a message and waits for the reply with the same envelope id. If
// want is non-empty the reply type must match it.
func (c *Client) Call(ctx context.Context, typ string, data any, want string) (*Envelope, error) {
	id := "c-" + strconv.FormatUint(c.seq.Add(1), 10)
	e, err := Build(typ, id, data)
	if err != nil {
		return nil, err
	}
	ch := make(chan *Envelope, 1)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, c.err
	}
	c.waiters[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.waiters, id)
		c.mu.Unlock()
	}()

	if err := c.write(ctx, e); err != nil {
		return nil, err
	}
	select {
	case reply := <-ch:
		if reply.Type == TypeError {
			body, _ := Decode[ErrorBody](reply)
			return nil, fmt.Errorf("signaling: server error %s: %s", body.Code, body.Message)
		}
		if want != "" && reply.Type != want {
			return nil, fmt.Errorf("signaling: want %s, got %s", want, reply.Type)
		}
		return reply, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.done:
		return nil, c.err
	}
}

// Ping sends a ping and returns the round-trip time.
func (c *Client) Ping(ctx context.Context) (time.Duration, error) {
	start := time.Now()
	reply, err := c.Call(ctx, TypePing, Ping{T: start.UnixMilli()}, TypePong)
	if err != nil {
		return 0, err
	}
	if _, err := Decode[Ping](reply); err != nil {
		return 0, err
	}
	return time.Since(start), nil
}

// SignalURL returns the signaling URL this client connected to.
func (c *Client) SignalURL() string { return c.url }

// DeviceID returns the logged-in device id in hex.
func (c *Client) DeviceID() string { return c.key.IDHex() }

// Err returns the reason the connection closed, if it has.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close closes the connection and waits for the read loop to finish.
func (c *Client) Close() error {
	c.fail(ErrClosed)
	<-c.done
	return nil
}

// Done is closed when the connection stops.
func (c *Client) Done() <-chan struct{} { return c.done }

func (c *Client) write(ctx context.Context, e *Envelope) error {
	raw, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("signaling: marshal envelope: %w", err)
	}
	if len(raw) > MaxMessage {
		return fmt.Errorf("signaling: envelope is %d bytes, limit %d", len(raw), MaxMessage)
	}
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return c.err
	}
	if err := c.conn.Write(ctx, websocket.MessageText, raw); err != nil {
		c.fail(err)
		return fmt.Errorf("signaling: write: %w", err)
	}
	return nil
}

func (c *Client) fail(err error) {
	c.mu.Lock()
	if !c.closed {
		c.closed = true
		c.err = err
		close(c.done)
	}
	c.mu.Unlock()
	c.conn.CloseNow()
}

func hexEncode(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = digits[v>>4]
		out[i*2+1] = digits[v&0x0f]
	}
	return string(out)
}
