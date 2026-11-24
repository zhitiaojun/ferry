package logging

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenRotatingRotatesAtThreshold(t *testing.T) {
	p := filepath.Join(t.TempDir(), "events.jsonl")
	log, closer, err := OpenRotating(p, 180)
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()

	for i := 0; i < 6; i++ {
		log.Event("tick", map[string]any{"i": i, "msg": strings.Repeat("x", 32)})
	}
	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}

	cur, err := os.Stat(p)
	if err != nil {
		t.Fatalf("current log missing: %v", err)
	}
	bak, err := os.Stat(p + ".1")
	if err != nil {
		t.Fatalf("rotated backup missing: %v", err)
	}
	if cur.Size() == 0 || bak.Size() == 0 {
		t.Fatalf("expected non-empty current (%d) and backup (%d) logs", cur.Size(), bak.Size())
	}
}
