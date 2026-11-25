package transfer

import (
	"errors"
	"sync"
	"time"
)

// ErrCancelled is returned when a transfer is cancelled locally or by the peer.
var ErrCancelled = errors.New("transfer: cancelled")

type cancelEntry struct {
	id uint64
	fn func()
}

// Controller allows callers to pause, resume, or cancel an in-flight transfer.
type Controller struct {
	mu        sync.Mutex
	paused    bool
	cancelled bool
	sig       chan struct{}
	nextID    uint64
	cancels   []cancelEntry
	onState   func(paused bool)
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

// Pause pauses the transfer until Resume or Cancel is called.
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
	c.mu.Unlock()
	if cb != nil {
		cb(true)
	}
}

// Resume unpauses a paused transfer.
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
	c.mu.Unlock()
	if cb != nil {
		cb(false)
	}
}

// TogglePause toggles pause state and returns whether the transfer is now paused.
func (c *Controller) TogglePause() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	if c.cancelled {
		c.mu.Unlock()
		return false
	}
	c.paused = !c.paused
	paused := c.paused
	c.notifyLocked()
	cb := c.onState
	c.mu.Unlock()
	if cb != nil {
		cb(paused)
	}
	return paused
}

// IsPaused reports whether the transfer is currently paused (and not cancelled).
func (c *Controller) IsPaused() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	p := c.paused && !c.cancelled
	c.mu.Unlock()
	return p
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

// SetOnState registers a callback invoked when Pause/Resume changes state.
func (c *Controller) SetOnState(fn func(paused bool)) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.onState = fn
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
	if !c.paused || c.cancelled {
		p, x := c.paused, c.cancelled
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
	p, x := c.paused && !c.cancelled, c.cancelled
	c.mu.Unlock()
	return p, x
}
