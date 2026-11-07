// Package logging writes JSONL event logs for transfer sessions.
//
// One line per event: {"ts":"...","event":"...",...}. The format is stable so
// evidence logs can be diffed and grepped without a parser.
package logging

import (
	"encoding/json"
	"io"
	"sync"
	"time"
)

// Logger writes one JSON object per line.
type Logger struct {
	mu  sync.Mutex
	enc *json.Encoder
}

// New returns a logger writing to w. A nil writer means the logger is disabled.
func New(w io.Writer) *Logger {
	if w == nil {
		return &Logger{}
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return &Logger{enc: enc}
}

// Event writes one event line. Keys are emitted as given; "ts" and "event" are
// reserved.
func (l *Logger) Event(event string, kv map[string]any) {
	if l == nil || l.enc == nil {
		return
	}
	m := make(map[string]any, len(kv)+2)
	for k, v := range kv {
		m[k] = v
	}
	m["ts"] = time.Now().UTC().Format(time.RFC3339Nano)
	m["event"] = event

	l.mu.Lock()
	defer l.mu.Unlock()
	_ = l.enc.Encode(m)
}
