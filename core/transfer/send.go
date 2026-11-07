package transfer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/cwlindean/filetransfer/core/proto"
	"github.com/cwlindean/filetransfer/core/session"
)

// Send streams paths to the peer over an established session.
//
// Files are sent one at a time. For every chunk the sender waits for the
// cumulative ACK before sending the next one (M1 keeps the window at one
// chunk; the ACK rule in §4.4 permits this).
func Send(s *session.Session, paths []string, cfg Config) (*Result, error) {
	start := time.Now()
	if len(paths) == 0 {
		return nil, errors.New("transfer: no input files")
	}

	entries := make([]proto.FileEntry, 0, len(paths))
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("stat %s: %w", p, err)
		}
		if st.IsDir() {
			return nil, fmt.Errorf("transfer: %s is a directory", p)
		}
		entries = append(entries, proto.FileEntry{
			Name:    filepath.Base(p),
			Size:    uint64(st.Size()),
			MtimeMS: st.ModTime().UnixMilli(),
		})
	}

	tid := cfg.TransferID
	if tid == ([16]byte{}) {
		var err error
		tid, err = newTransferID()
		if err != nil {
			return nil, err
		}
	}
	manifest := &proto.Manifest{
		TransferID: tid,
		ChunkSize:  uint32(cfg.chunkSize()),
		Files:      entries,
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}

	if cfg.Log != nil {
		cfg.Log.Event("transfer.start", map[string]any{
			"transfer_id": hex.EncodeToString(tid[:]),
			"files":       len(paths),
			"chunk_size":  manifest.ChunkSize,
			"bytes":       totalSize(entries),
			"peer":        s.Peer.Name,
		})
	}

	if err := s.SendMsg(proto.TypeManifest, manifest); err != nil {
		return nil, err
	}
	startIdx, startOff, err := waitStart(s, tid, cfg.timeout())
	if err != nil {
		return nil, fmt.Errorf("manifest ack: %w", err)
	}

	buf := make([]byte, cfg.chunkSize())
	hashes := make([][32]byte, len(paths))
	var total int64
	chunks := 0

	for i, p := range paths {
		if entries[i].Size == 0 {
			hashes[i] = sha256.Sum256(nil)
			continue
		}
		if i < startIdx {
			hashes[i], err = hashFile(p)
			if err != nil {
				return nil, err
			}
			continue
		}
		off := uint64(0)
		if i == startIdx {
			off = startOff
		}
		h, n, c, err := sendFile(s, tid, uint32(i), p, buf, cfg, off)
		if err != nil {
			_ = s.SendError(proto.CodeIOError, "send %s: %v", entries[i].Name, err)
			return nil, err
		}
		hashes[i] = h
		total += n
		chunks += c
	}

	fin := &proto.Fin{Status: proto.FinOK, TransferID: tid, Hashes: hashes}
	if err := s.SendMsg(proto.TypeFin, fin); err != nil {
		return nil, err
	}
	v, err := s.Expect(proto.TypeFin, cfg.timeout()*3)
	if err != nil {
		return nil, fmt.Errorf("final FIN: %w", err)
	}
	peerFin, ok := v.(*proto.Fin)
	if !ok {
		return nil, proto.NewError(proto.CodePayloadInvalid, "peer FIN did not decode")
	}
	if peerFin.Status != proto.FinOK {
		return nil, fmt.Errorf("transfer: peer reported status %d", peerFin.Status)
	}

	res := &Result{
		TransferID: tid,
		Files:      names(entries),
		Bytes:      total,
		Chunks:     chunks,
		Duration:   time.Since(start),
	}
	if cfg.Log != nil {
		cfg.Log.Event("transfer.done", map[string]any{
			"transfer_id": hex.EncodeToString(tid[:]),
			"files":       len(res.Files),
			"bytes":       res.Bytes,
			"chunks":      res.Chunks,
			"duration_ms": res.Duration.Milliseconds(),
		})
	}
	return res, nil
}

// sendFile writes every chunk of one file and returns its whole-file hash.
func sendFile(s *session.Session, tid [16]byte, index uint32, path string, buf []byte, cfg Config, startOffset uint64) ([32]byte, int64, int, error) {
	var zero [32]byte

	f, err := os.Open(path)
	if err != nil {
		return zero, 0, 0, err
	}
	defer f.Close()

	h := sha256.New()
	var off int64
	if startOffset > 0 {
		n, err := io.CopyN(h, f, int64(startOffset))
		if err != nil {
			return zero, 0, 0, fmt.Errorf("hash %d resumed bytes of %s: %w", startOffset, path, err)
		}
		off = n
	}
	chunks := 0
	for {
		n, rerr := f.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			sum := sha256.Sum256(chunk)
			msg := &proto.Data{
				TransferID:  tid,
				FileIndex:   index,
				Offset:      uint64(off),
				ChunkSHA256: sum,
				Data:        chunk,
			}
			if err := s.SendMsg(proto.TypeData, msg); err != nil {
				return zero, 0, 0, err
			}
			_, _ = h.Write(chunk)
			off += int64(n)
			chunks++
			if err := waitAck(s, tid, index, uint64(off), cfg.timeout()); err != nil {
				return zero, 0, 0, err
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return zero, 0, 0, rerr
		}
	}

	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum, off, chunks, nil
}

// waitStart waits for the receiver's answer to MANIFEST: ACK(0,0) for a fresh
// transfer, or RESUME(file, offset) when a .ftpart file is being continued.
func waitStart(s *session.Session, tid [16]byte, timeout time.Duration) (int, uint64, error) {
	f, err := s.Recv(timeout)
	if err != nil {
		return 0, 0, err
	}
	switch f.Type {
	case proto.TypeAck:
		ack, err := proto.ParseAck(f.Payload)
		if err != nil {
			return 0, 0, err
		}
		if ack.TransferID != tid {
			return 0, 0, proto.NewError(proto.CodeProtocolViolation, "ACK transfer_id mismatch")
		}
		return 0, 0, nil
	case proto.TypeResume:
		r, err := proto.ParseResume(f.Payload)
		if err != nil {
			return 0, 0, err
		}
		if r.TransferID != tid {
			return 0, 0, proto.NewError(proto.CodeProtocolViolation, "RESUME transfer_id mismatch")
		}
		return int(r.FileIndex), r.Offset, nil
	case proto.TypeError:
		return 0, 0, remoteErrorFrom(f.Payload)
	default:
		return 0, 0, proto.NewError(proto.CodeUnexpectedMessage, fmt.Sprintf("want ACK or RESUME, got %s", f.Type))
	}
}

// hashFile returns the SHA-256 of a whole file (used for files the receiver
// already has).
func hashFile(path string) ([32]byte, error) {
	var zero [32]byte
	f, err := os.Open(path)
	if err != nil {
		return zero, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return zero, fmt.Errorf("hash %s: %w", path, err)
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

// waitAck waits for an ACK that covers want bytes of file index. A stale ACK
// (offset behind want) is tolerated once; anything ahead is a protocol error.
func waitAck(s *session.Session, tid [16]byte, index uint32, want uint64, timeout time.Duration) error {
	for attempt := 0; attempt < 3; attempt++ {
		v, err := s.Expect(proto.TypeAck, timeout)
		if err != nil {
			return err
		}
		ack, ok := v.(*proto.Ack)
		if !ok {
			return proto.NewError(proto.CodePayloadInvalid, "ACK did not decode")
		}
		if ack.TransferID != tid {
			return proto.NewError(proto.CodeProtocolViolation, "ACK transfer_id mismatch")
		}
		if ack.FileIndex != index {
			// Ack for an earlier file is stale; keep waiting.
			continue
		}
		if ack.Offset == want {
			return nil
		}
		if ack.Offset < want {
			continue
		}
		return proto.NewError(proto.CodeOffsetInvalid, fmt.Sprintf("ACK offset %d ahead of sent %d", ack.Offset, want))
	}
	return proto.NewError(proto.CodeTimeout, "no matching ACK")
}

func totalSize(entries []proto.FileEntry) uint64 {
	var n uint64
	for _, e := range entries {
		n += e.Size
	}
	return n
}

func names(entries []proto.FileEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Name
	}
	return out
}
