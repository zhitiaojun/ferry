package transfer

import (
	"errors"
	"sync"
	"time"

	"github.com/cwlindean/filetransfer/core/proto"
	"github.com/cwlindean/filetransfer/core/session"
)

// ErrCancelled is returned when a transfer is cancelled locally or by the peer.
var ErrCancelled = errors.New("transfer: cancelled")

// IsCancelled reports whether err represents a local or peer transfer cancellation.
func IsCancelled(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrCancelled) {
		return true
	}
	var rem *session.RemoteError
	return errors.As(err, &rem) && rem.Code == proto.CodeCancelled
}

const (
	cancelWriteTimeout = 2 * time.Second
	cancelDrainGrace   = 3 * time.Second
)

// graceWaitPeerClose drains incoming frames after sending CodeCancelled until
// the peer closes its side (having received CodeCancelled) or grace expires.
// Using an AfterFunc read-deadline fuse guarantees buffered transports such as
// relay.Conn unblock even if the peer never closes.
func graceWaitPeerClose(s *session.Session, grace time.Duration) {
	if s == nil {
		return
	}
	if grace <= 0 {
		grace = cancelDrainGrace
	}
	deadline := time.Now().Add(grace)
	fuse := time.AfterFunc(grace, func() {
		_ = s.Conn().SetReadDeadline(time.Now())
	})
	defer fuse.Stop()
	for {
		rem := time.Until(deadline)
		if rem <= 0 {
			return
		}
		if _, err := s.Recv(rem); err != nil {
			return
		}
	}
}

func sendCancelAndWait(s *session.Session, msg string) {
	if s == nil {
		return
	}
	_ = s.Conn().SetWriteDeadline(time.Now().Add(cancelWriteTimeout))
	_ = s.SendError(proto.CodeCancelled, "%s", msg)
	_ = s.Conn().SetWriteDeadline(time.Time{})
	graceWaitPeerClose(s, cancelDrainGrace)
}

var (
	pingTokenKeepalive   = [8]byte{}
	pingTokenPeerPaused  = [8]byte{'F', 'T', 'P', 'A', 'U', 'S', 'E', 1}
	pingTokenPeerResumed = [8]byte{'F', 'T', 'P', 'A', 'U', 'S', 'E', 0}
)

type cancelEntry struct {
	id uint64
	fn func()
}

// Controller allows callers to pause, resume, or cancel an in-flight transfer.
type Controller struct {
	mu           sync.Mutex
	paused       bool
	peerPaused   bool
	cancelled    bool
	sig          chan struct{}
	nextID       uint64
	cancels      []cancelEntry
	onState      func(paused bool)
	onPauseState func(localPaused, peerPaused bool)
}

// NewController creates an active transfer controller.
func NewController() *Controller {
	return &Controller{
		sig: make(chan struct{}),
	}
}

func (c *Controller) notifyLocked() {
	if c.sig != nil {
		close(c.sig)
	}
	c.sig = make(chan struct{})
}

// Pause pauses the transfer locally until Resume or Cancel is called.
func (c *Controller) Pause() {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.cancelled || c.paused {
		c.mu.Unlock()
		return
	}
	c.paused = true
	c.notifyLocked()
	cb := c.onState
	cbBoth := c.onPauseState
	peerPaused := c.peerPaused
	c.mu.Unlock()
	if cb != nil {
		cb(true)
	}
	if cbBoth != nil {
		cbBoth(true, peerPaused)
	}
}

// Resume unpauses a locally paused transfer.
func (c *Controller) Resume() {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.cancelled || !c.paused {
		c.mu.Unlock()
		return
	}
	c.paused = false
	c.notifyLocked()
	cb := c.onState
	cbBoth := c.onPauseState
	peerPaused := c.peerPaused
	c.mu.Unlock()
	if cb != nil {
		cb(false)
	}
	if cbBoth != nil {
		cbBoth(false, peerPaused)
	}
}

// TogglePause toggles local pause state and returns whether the transfer is now
// locally paused. If the transfer is currently paused only by the peer ("谁暂停谁恢复"),
// TogglePause refuses to resume or change state and returns false.
func (c *Controller) TogglePause() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	if c.cancelled || (!c.paused && c.peerPaused) {
		c.mu.Unlock()
		return false
	}
	c.paused = !c.paused
	paused := c.paused
	peerPaused := c.peerPaused
	c.notifyLocked()
	cb := c.onState
	cbBoth := c.onPauseState
	c.mu.Unlock()
	if cb != nil {
		cb(paused)
	}
	if cbBoth != nil {
		cbBoth(paused, peerPaused)
	}
	return paused
}

// SetPeerPaused updates whether the remote peer has paused the transfer.
func (c *Controller) SetPeerPaused(paused bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.cancelled || c.peerPaused == paused {
		c.mu.Unlock()
		return
	}
	c.peerPaused = paused
	localPaused := c.paused
	c.notifyLocked()
	cbBoth := c.onPauseState
	c.mu.Unlock()
	if cbBoth != nil {
		cbBoth(localPaused, paused)
	}
}

// IsPaused reports whether the transfer is locally paused (and not cancelled).
func (c *Controller) IsPaused() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	p := c.paused && !c.cancelled
	c.mu.Unlock()
	return p
}

// IsPeerPaused reports whether the transfer is currently paused by the peer.
func (c *Controller) IsPeerPaused() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	p := c.peerPaused && !c.cancelled
	c.mu.Unlock()
	return p
}

// IsAnyPaused reports whether either side has paused the transfer.
func (c *Controller) IsAnyPaused() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	p := (c.paused || c.peerPaused) && !c.cancelled
	c.mu.Unlock()
	return p
}

// PauseStage returns "paused" (local), "peer_paused" (remote), or "transferring".
func (c *Controller) PauseStage() string {
	if c == nil {
		return "transferring"
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancelled {
		return "transferring"
	}
	if c.paused {
		return "paused"
	}
	if c.peerPaused {
		return "peer_paused"
	}
	return "transferring"
}

// Cancel marks the transfer as cancelled, wakes any paused waiters, and invokes
// registered cancellation callbacks outside the mutex.
func (c *Controller) Cancel() {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.cancelled {
		c.mu.Unlock()
		return
	}
	c.cancelled = true
	c.paused = false
	c.peerPaused = false
	c.notifyLocked()
	cbs := make([]func(), len(c.cancels))
	for i, e := range c.cancels {
		cbs[i] = e.fn
	}
	c.cancels = nil
	c.mu.Unlock()
	for _, fn := range cbs {
		if fn != nil {
			fn()
		}
	}
}

// IsCancelled reports whether Cancel has been called.
func (c *Controller) IsCancelled() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	v := c.cancelled
	c.mu.Unlock()
	return v
}

// SetOnState registers a callback invoked when local Pause/Resume changes state.
func (c *Controller) SetOnState(fn func(paused bool)) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.onState = fn
	c.mu.Unlock()
}

// SetOnPauseState registers a callback invoked whenever local or peer pause state changes.
func (c *Controller) SetOnPauseState(fn func(localPaused, peerPaused bool)) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.onPauseState = fn
	c.mu.Unlock()
}

// OnCancel registers fn to be called when Cancel is invoked. If already cancelled,
// fn is invoked immediately outside the mutex. The returned function unregisters fn.
func (c *Controller) OnCancel(fn func()) func() {
	if c == nil || fn == nil {
		return func() {}
	}
	c.mu.Lock()
	if c.cancelled {
		c.mu.Unlock()
		fn()
		return func() {}
	}
	c.nextID++
	id := c.nextID
	c.cancels = append(c.cancels, cancelEntry{id: id, fn: fn})
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		for i, e := range c.cancels {
			if e.id == id {
				c.cancels = append(c.cancels[:i], c.cancels[i+1:]...)
				break
			}
		}
		c.mu.Unlock()
	}
}

// StateCh returns a channel that is closed on the next Pause, Resume, or Cancel transition.
func (c *Controller) StateCh() <-chan struct{} {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	if c.sig == nil {
		c.sig = make(chan struct{})
	}
	ch := c.sig
	c.mu.Unlock()
	return ch
}

// WaitResumeOrCancel waits up to d for the controller to leave paused state or
// be cancelled. If d <= 0, it waits until state changes.
func (c *Controller) WaitResumeOrCancel(d time.Duration) (paused bool, cancelled bool) {
	if c == nil {
		return false, false
	}
	c.mu.Lock()
	if !(c.paused || c.peerPaused) || c.cancelled {
		p, x := (c.paused || c.peerPaused), c.cancelled
		c.mu.Unlock()
		return p, x
	}
	if c.sig == nil {
		c.sig = make(chan struct{})
	}
	ch := c.sig
	c.mu.Unlock()

	if d > 0 {
		t := time.NewTimer(d)
		select {
		case <-ch:
			t.Stop()
		case <-t.C:
		}
	} else {
		<-ch
	}

	c.mu.Lock()
	p, x := (c.paused || c.peerPaused) && !c.cancelled, c.cancelled
	c.mu.Unlock()
	return p, x
}
