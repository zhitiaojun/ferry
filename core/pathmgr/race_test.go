package pathmgr

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRaceFirstSuccessWins(t *testing.T) {
	var losers atomic.Int32

	val, name, err := Race(context.Background(),
		Attempt[string]{Name: "slow", Fn: func(ctx context.Context) (string, error) {
			<-ctx.Done()
			losers.Add(1)
			return "", ctx.Err()
		}},
		Attempt[string]{Name: "fast", Fn: func(ctx context.Context) (string, error) {
			return "ok", nil
		}},
	)
	if err != nil {
		t.Fatalf("race: %v", err)
	}
	if val != "ok" || name != "fast" {
		t.Fatalf("got %q from %q, want ok/fast", val, name)
	}
	if losers.Load() != 1 {
		t.Fatalf("loser was not cancelled (count=%d)", losers.Load())
	}
}

func TestRaceAllFail(t *testing.T) {
	_, _, err := Race(context.Background(),
		Attempt[string]{Name: "a", Fn: func(context.Context) (string, error) {
			return "", errors.New("nope")
		}},
		Attempt[string]{Name: "b", Fn: func(context.Context) (string, error) {
			return "", errors.New("nope too")
		}},
	)
	if err == nil || !strings.Contains(err.Error(), "a: nope") || !strings.Contains(err.Error(), "b: nope too") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRaceStopsAfterContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, _, err := Race(ctx,
		Attempt[string]{Name: "hang", Fn: func(ctx context.Context) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		}},
	)
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
	if time.Since(start) > time.Second {
		t.Fatalf("race did not return promptly")
	}
}

func TestRaceNoAttempts(t *testing.T) {
	if _, _, err := Race[string](context.Background()); err == nil {
		t.Fatal("expected error for empty attempt list")
	}
}
