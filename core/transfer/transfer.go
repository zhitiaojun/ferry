// Package transfer implements the M1 chunked transfer: MANIFEST, DATA with
// per-chunk hashes, cumulative ACK, FIN with whole-file hashes.
//
// Memory use is bounded by the chunk size: files are streamed, never loaded
// whole. Resume/path switching is layered on top by core/pathmgr (M1 only
// defines the frames).
package transfer

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/cwlindean/filetransfer/core/logging"
	"github.com/cwlindean/filetransfer/core/proto"
)

// ErrSlowPath is returned by Send when MinBytesPerSec is configured and
// confirmed throughput stays below the threshold with enough bytes remaining.
var ErrSlowPath = errors.New("transfer: path below minimum speed")

// ErrPathUpgrade is returned by Send when UpgradeCh signals that a faster
// direct path is ready and enough bytes remain to justify switching.
var ErrPathUpgrade = errors.New("transfer: upgrading to faster path")

const (
	// DefaultWindowBytes bounds unacknowledged bytes kept in flight per file.
	DefaultWindowBytes = 4 << 20
	// DefaultSlowGrace skips initial ramp-up before checking transfer rate.
	DefaultSlowGrace = 1 * time.Second
	// DefaultSlowWindow is the rolling window over which transfer speed is measured.
	DefaultSlowWindow = 2500 * time.Millisecond
	// DefaultSlowMinRemaining avoids aborting near-complete transfers.
	DefaultSlowMinRemaining = 2 << 20
)

// ProgressSnapshot reports detailed batch transfer state and byte counts.
type ProgressSnapshot struct {
	Stage      string // "transferring" | "paused" | "verifying"
	FileIndex  int    // 1-based current file index
	FileCount  int    // total files in batch
	FileName   string // current file base name
	BytesDone  int64  // total confirmed bytes across batch
	BytesTotal int64  // total bytes across batch
}

// Config tunes a transfer. Zero values select the protocol defaults.
type Config struct {
	ChunkSize           uint32
	WindowBytes         int
	Timeout             time.Duration
	StartTimeout        time.Duration
	MinBytesPerSec      int64
	SlowGrace           time.Duration
	SlowWindow          time.Duration
	SlowMinRemaining    int64
	UpgradeCh           <-chan struct{}
	UpgradeMinRemaining int64
	Control             *Controller
	Log                 *logging.Logger
	// TransferID overrides the random id when the caller already committed to
	// one (e.g. the signaling transfer id); zero means "generate".
	TransferID [16]byte
	// Confirm, when set on the receiver, is called after MANIFEST is validated
	// and before any directory or .ftpart file is created. Returning an error
	// rejects the transfer cleanly without writing to outDir.
	Confirm func(m *proto.Manifest) error
	// OnProgress, when set, is called after each acknowledged chunk with the
	// number of bytes the peer confirmed for the current file. It is meant for
	// path switching decisions (relay -> punch upgrade) and diagnostics.
	OnProgress func(bytes int64)
	// OnDetailProgress, when set, reports batch stage, active file, and total progress.
	OnDetailProgress func(ProgressSnapshot)
}

func (c Config) chunkSize() int {
	if c.ChunkSize == 0 {
		return int(proto.DefaultChunkSize)
	}
	return int(c.ChunkSize)
}

func (c Config) windowBytes() int {
	w := c.WindowBytes
	if w <= 0 {
		w = DefaultWindowBytes
	}
	if cs := c.chunkSize(); w < cs {
		w = cs
	}
	return w
}

func (c Config) slowGrace() time.Duration {
	if c.SlowGrace <= 0 {
		return DefaultSlowGrace
	}
	return c.SlowGrace
}

func (c Config) slowWindow() time.Duration {
	if c.SlowWindow <= 0 {
		return DefaultSlowWindow
	}
	return c.SlowWindow
}

func (c Config) slowMinRemaining() int64 {
	if c.SlowMinRemaining <= 0 {
		return DefaultSlowMinRemaining
	}
	return c.SlowMinRemaining
}

func (c Config) upgradeMinRemaining() int64 {
	if c.UpgradeMinRemaining <= 0 {
		return DefaultSlowMinRemaining
	}
	return c.UpgradeMinRemaining
}

func (c Config) timeout() time.Duration {
	if c.Timeout == 0 {
		return sessionRecvTimeout
	}
	return c.Timeout
}

func (c Config) startTimeout() time.Duration {
	if c.StartTimeout > 0 {
		return c.StartTimeout
	}
	return c.timeout()
}

const sessionRecvTimeout = 30 * time.Second

// Result summarises a completed transfer.
type Result struct {
	TransferID [16]byte
	Files      []string
	Bytes      int64
	Chunks     int
	Duration   time.Duration
}

func newTransferID() ([16]byte, error) {
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return id, fmt.Errorf("generate transfer id: %w", err)
	}
	return id, nil
}

// copyAll is io.Copy without the 32 KiB buffer surprise: the caller's buffer
// stays the only large allocation.
func copyAll(dst io.Writer, src io.Reader, buf []byte) (int64, error) {
	var total int64
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return total, werr
			}
			total += int64(n)
		}
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}
