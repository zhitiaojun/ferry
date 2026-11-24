package logging

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// DefaultMaxBytes is the default 2 MiB rotation threshold for OpenRotating.
const DefaultMaxBytes int64 = 2 << 20

type rotatingFile struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	size     int64
	f        *os.File
}

// OpenRotating opens path for appending JSONL logs, rotating to path+".1" once
// the file size reaches maxBytes (default DefaultMaxBytes when maxBytes <= 0).
func OpenRotating(path string, maxBytes int64) (*Logger, io.Closer, error) {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, nil, err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, err
	}
	var sz int64
	if st, serr := f.Stat(); serr == nil {
		sz = st.Size()
	}
	rf := &rotatingFile{
		path:     path,
		maxBytes: maxBytes,
		size:     sz,
		f:        f,
	}
	return New(rf), rf, nil
}

func (r *rotatingFile) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return 0, fmt.Errorf("logging: writer closed")
	}
	if r.size+int64(len(p)) > r.maxBytes && r.size > 0 {
		_ = r.f.Close()
		r.f = nil
		bak := r.path + ".1"
		_ = os.Remove(bak)
		_ = os.Rename(r.path, bak)
		nf, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			return 0, err
		}
		r.f = nf
		r.size = 0
	}
	n, err := r.f.Write(p)
	r.size += int64(n)
	return n, err
}

func (r *rotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}
