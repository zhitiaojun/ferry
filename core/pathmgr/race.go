package pathmgr

import (
	"context"
	"errors"
	"fmt"
)

// Attempt is one way of reaching the peer. Fn must return as soon as ctx is
// cancelled, closing whatever it opened.
type Attempt[T any] struct {
	Name string
	Fn   func(context.Context) (T, error)
}

type raceResult[T any] struct {
	value T
	name  string
	err   error
}

// Race starts every attempt at once and returns the first success. The
// remaining attempts are cancelled through their context; Race waits for them
// to return so no goroutine or socket is left behind.
//
// This is the mechanism behind D2: LAN, punch and relay all start together and
// the first usable path wins.
func Race[T any](ctx context.Context, attempts ...Attempt[T]) (T, string, error) {
	var zero T
	if len(attempts) == 0 {
		return zero, "", errors.New("race: no attempts")
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ch := make(chan raceResult[T], len(attempts))
	for _, a := range attempts {
		a := a
		go func() {
			v, err := a.Fn(ctx)
			ch <- raceResult[T]{value: v, name: a.Name, err: err}
		}()
	}

	var errs []error
	for i := 0; i < len(attempts); i++ {
		r := <-ch
		if r.err == nil {
			cancel()
			// Drain the rest so every attempt has stopped before returning.
			for j := i + 1; j < len(attempts); j++ {
				<-ch
			}
			return r.value, r.name, nil
		}
		errs = append(errs, fmt.Errorf("%s: %w", r.name, r.err))
	}
	return zero, "", errors.Join(errs...)
}
