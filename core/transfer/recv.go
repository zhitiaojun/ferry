package transfer

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/cwlindean/filetransfer/core/proto"
	"github.com/cwlindean/filetransfer/core/session"
)

// recvFile is one file being written to <out>/<name>.ftpart.
type recvFile struct {
	entry   proto.FileEntry
	part    string
	f       *os.File
	h       hash.Hash
	written uint64
}

// Receive accepts one transfer on an established session and writes the files
// into outDir. A leftover <name>.ftpart makes Receive resume from its length
// (M1 rehearsal of §6.3 path switching).
func Receive(s *session.Session, outDir string, cfg Config) (*Result, error) {
	start := time.Now()
	if cfg.Control != nil && cfg.Control.IsCancelled() {
		return nil, ErrCancelled
	}
	if cfg.Control != nil {
		unreg := cfg.Control.OnCancel(func() {
			_ = s.Conn().SetReadDeadline(time.Now())
		})
		defer unreg()
	}

	v, err := s.Expect(proto.TypeManifest, cfg.timeout())
	if err != nil {
		if cfg.Control != nil && cfg.Control.IsCancelled() {
			return nil, ErrCancelled
		}
		return nil, fmt.Errorf("manifest: %w", err)
	}
	m, ok := v.(*proto.Manifest)
	if !ok {
		return nil, proto.NewError(proto.CodePayloadInvalid, "MANIFEST did not decode")
	}
	if err := m.Validate(); err != nil {
		code := proto.CodeManifestInvalid
		var pe *proto.Error
		if errors.As(err, &pe) {
			code = pe.Code
		}
		_ = s.SendError(code, "%s", err)
		return nil, err
	}
	if cfg.Confirm != nil {
		if err := cfg.Confirm(m); err != nil {
			code := proto.CodeCancelled
			msg := err.Error()
			var pe *proto.Error
			if errors.As(err, &pe) {
				code = pe.Code
				msg = pe.Msg
			}
			_ = s.SendError(code, "%s", msg)
			return nil, err
		}
	}

	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("create output dir: %w", err)
	}

	files := make([]recvFile, len(m.Files))
	for i, e := range m.Files {
		if !safeName(e.Name) {
			_ = s.SendError(proto.CodeManifestInvalid, "unsafe file name %q", e.Name)
			return nil, proto.NewError(proto.CodeManifestInvalid, fmt.Sprintf("unsafe file name %q", e.Name))
		}
		rf := recvFile{entry: e, part: filepath.Join(outDir, e.Name+".ftpart"), h: sha256.New()}
		st, statErr := os.Stat(rf.part)
		switch {
		case statErr == nil:
			if uint64(st.Size()) > e.Size {
				_ = s.SendError(proto.CodeOffsetInvalid, "%s: part file larger than declared size", e.Name)
				return nil, proto.NewError(proto.CodeOffsetInvalid, fmt.Sprintf("%s: part file larger than declared size", e.Name))
			}
			rf.written = uint64(st.Size())
		case os.IsNotExist(statErr):
			// Create the placeholder now so empty files and FIN renames work.
			if err := os.WriteFile(rf.part, nil, 0o644); err != nil {
				_ = s.SendError(proto.CodeIOError, "create %s: %v", e.Name, err)
				return nil, fmt.Errorf("create part file: %w", err)
			}
		default:
			_ = s.SendError(proto.CodeIOError, "stat %s: %v", e.Name, statErr)
			return nil, fmt.Errorf("stat part file: %w", statErr)
		}
		files[i] = rf
	}
	closeAll := func() {
		for i := range files {
			if files[i].f != nil {
				_ = files[i].f.Close()
				files[i].f = nil
			}
		}
	}
	removeParts := func() {
		closeAll()
		for i := range files {
			if files[i].part != "" {
				_ = os.Remove(files[i].part)
			}
		}
	}
	defer closeAll()

	// First file that still needs bytes.
	startIdx := len(files)
	var startOff uint64
	for i := range files {
		if files[i].written < files[i].entry.Size {
			startIdx = i
			startOff = files[i].written
			break
		}
	}

	// Seed hashes of everything already on disk.
	for i := range files {
		if files[i].written == 0 {
			continue
		}
		if err := files[i].openPart(); err != nil {
			_ = s.SendError(proto.CodeIOError, "resume %s: %v", files[i].entry.Name, err)
			return nil, err
		}
		if i >= startIdx {
			break
		}
	}

	if cfg.Log != nil {
		cfg.Log.Event("transfer.accept", map[string]any{
			"files":       len(m.Files),
			"chunk_size":  m.ChunkSize,
			"bytes":       totalSize(m.Files),
			"resume_from": startOff,
			"resume_file": startIdx,
			"peer":        s.Peer.Name,
		})
	}

	// Fresh transfer: plain ACK(0,0). Otherwise tell the sender where to start.
	if startIdx == 0 && startOff == 0 {
		if err := s.SendMsg(proto.TypeAck, &proto.Ack{TransferID: m.TransferID}); err != nil {
			return nil, err
		}
	} else {
		if err := s.SendMsg(proto.TypeResume, &proto.Resume{
			TransferID: m.TransferID,
			FileIndex:  uint32(startIdx),
			Offset:     startOff,
		}); err != nil {
			return nil, err
		}
	}

	cur := startIdx
	var received int64
	chunks := 0
	batchTotal := int64(totalSize(m.Files))
	var totalWritten int64
	for i := range files {
		totalWritten += int64(files[i].written)
	}
	emitProgress := func(stage string, idx int) {
		if cfg.OnDetailProgress == nil {
			return
		}
		fname := ""
		fileIdx := 0
		if len(files) > 0 {
			if idx < 0 {
				idx = 0
			} else if idx >= len(files) {
				idx = len(files) - 1
			}
			fileIdx = idx + 1
			fname = files[idx].entry.Name
		}
		cfg.OnDetailProgress(ProgressSnapshot{
			Stage:      stage,
			FileIndex:  fileIdx,
			FileCount:  len(files),
			FileName:   fname,
			BytesDone:  totalWritten,
			BytesTotal: batchTotal,
		})
	}
	emitProgress("transferring", cur)

	if cfg.Control != nil && cfg.Control.IsCancelled() {
		_ = s.Conn().SetWriteDeadline(time.Now().Add(300 * time.Millisecond))
		_ = s.SendError(proto.CodeCancelled, "接收方已取消传输")
		_ = s.Conn().SetWriteDeadline(time.Time{})
		removeParts()
		return nil, ErrCancelled
	}

	type recvFrame struct {
		frame proto.Frame
		err   error
	}
	frameCh := make(chan recvFrame, 1)
	go func() {
		defer close(frameCh)
		for {
			fr, rerr := s.Recv(cfg.timeout())
			frameCh <- recvFrame{frame: fr, err: rerr}
			if rerr != nil || fr.Type == proto.TypeFin || fr.Type == proto.TypeError {
				return
			}
		}
	}()
	readerDone := false
	defer func() {
		if !readerDone {
			_ = s.Conn().SetReadDeadline(time.Now())
			for range frameCh {
			}
		}
	}()
	drainForRemoteError := func() error {
		if readerDone {
			return nil
		}
		_ = s.Conn().SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		for next := range frameCh {
			if next.err != nil {
				break
			}
			if next.frame.Type == proto.TypeError {
				readerDone = true
				rerr := remoteErrorFrom(next.frame.Payload)
				var rem *session.RemoteError
				if errors.As(rerr, &rem) && rem.Code == proto.CodeCancelled {
					removeParts()
				}
				return rerr
			}
		}
		readerDone = true
		return nil
	}

	paused := cfg.Control != nil && cfg.Control.IsPaused()
	var kaTicker *time.Ticker
	var kaC <-chan time.Time
	defer func() {
		if kaTicker != nil {
			kaTicker.Stop()
		}
	}()
	if paused {
		kaTicker = time.NewTicker(keepaliveInterval(cfg.timeout()))
		kaC = kaTicker.C
		emitProgress("paused", cur)
	}
	pendingAck := false
	var pendingIdx uint32
	var pendingOff uint64

	for {
		var stateCh <-chan struct{}
		if cfg.Control != nil {
			stateCh = cfg.Control.StateCh()
		}
		if cfg.Control != nil && cfg.Control.IsCancelled() {
			_ = s.Conn().SetWriteDeadline(time.Now().Add(300 * time.Millisecond))
			_ = s.SendError(proto.CodeCancelled, "接收方已取消传输")
			_ = s.Conn().SetWriteDeadline(time.Time{})
			removeParts()
			return nil, ErrCancelled
		}

		var rfMsg recvFrame
		select {
		case <-stateCh:
			if cfg.Control.IsCancelled() {
				_ = s.Conn().SetWriteDeadline(time.Now().Add(300 * time.Millisecond))
				_ = s.SendError(proto.CodeCancelled, "接收方已取消传输")
				_ = s.Conn().SetWriteDeadline(time.Time{})
				removeParts()
				return nil, ErrCancelled
			}
			nowPaused := cfg.Control.IsPaused()
			if nowPaused != paused {
				paused = nowPaused
				if paused {
					if kaTicker == nil {
						kaTicker = time.NewTicker(keepaliveInterval(cfg.timeout()))
					}
					kaC = kaTicker.C
					emitProgress("paused", cur)
				} else {
					if kaTicker != nil {
						kaTicker.Stop()
						kaTicker = nil
					}
					kaC = nil
					if pendingAck {
						if err := s.SendMsg(proto.TypeAck, &proto.Ack{
							TransferID: m.TransferID,
							FileIndex:  pendingIdx,
							Offset:     pendingOff,
						}); err != nil {
							if cfg.Control != nil && cfg.Control.IsCancelled() {
								removeParts()
								return nil, ErrCancelled
							}
							if rerr := drainForRemoteError(); rerr != nil {
								return nil, rerr
							}
							return nil, err
						}
						pendingAck = false
					}
					emitProgress("transferring", cur)
				}
			}
			continue

		case <-kaC:
			var tok [8]byte
			if perr := s.SendMsg(proto.TypePing, &proto.Ping{Token: tok}); perr != nil {
				if cfg.Control != nil && cfg.Control.IsCancelled() {
					removeParts()
					return nil, ErrCancelled
				}
				if rerr := drainForRemoteError(); rerr != nil {
					return nil, rerr
				}
				return nil, perr
			}
			continue

		case rfMsg = <-frameCh:
		}

		if rfMsg.err != nil {
			readerDone = true
			if cfg.Control != nil && cfg.Control.IsCancelled() {
				removeParts()
				return nil, ErrCancelled
			}
			return nil, fmt.Errorf("wait for data: %w", rfMsg.err)
		}
		frame := rfMsg.frame

		switch frame.Type {
		case proto.TypePing:
			if p, perr := proto.ParsePing(frame.Payload); perr == nil {
				_ = s.SendMsg(proto.TypePong, p)
			}

		case proto.TypePong:
			// keepalive response

		case proto.TypeError:
			readerDone = true
			rerr := remoteErrorFrom(frame.Payload)
			var rem *session.RemoteError
			if errors.As(rerr, &rem) && rem.Code == proto.CodeCancelled {
				removeParts()
			}
			return nil, rerr

		case proto.TypeData:
			d, err := proto.ParseData(frame.Payload)
			if err != nil {
				_ = s.SendError(proto.CodePayloadInvalid, "malformed DATA: %v", err)
				return nil, err
			}
			if d.TransferID != m.TransferID {
				_ = s.SendError(proto.CodeProtocolViolation, "DATA transfer_id mismatch")
				return nil, proto.NewError(proto.CodeProtocolViolation, "DATA transfer_id mismatch")
			}

			for int(d.FileIndex) > cur {
				if cur >= len(files) || files[cur].written != files[cur].entry.Size {
					_ = s.SendError(proto.CodeProtocolViolation, "unexpected file index %d", d.FileIndex)
					return nil, proto.NewError(proto.CodeProtocolViolation, "data for non-current file")
				}
				if err := files[cur].finish(); err != nil {
					return nil, err
				}
				cur++
			}
			if int(d.FileIndex) != cur {
				_ = s.SendError(proto.CodeProtocolViolation, "unexpected file index %d (current %d)", d.FileIndex, cur)
				return nil, proto.NewError(proto.CodeProtocolViolation, "unexpected file index")
			}

			rf := &files[cur]
			if rf.f == nil {
				if err := rf.openPart(); err != nil {
					_ = s.SendError(proto.CodeIOError, "open %s: %v", rf.entry.Name, err)
					return nil, err
				}
			}
			end := d.Offset + uint64(len(d.Data))
			switch {
			case d.Offset == rf.written:
				if end > rf.entry.Size {
					_ = s.SendError(proto.CodeOffsetInvalid, "chunk exceeds declared size")
					return nil, proto.NewError(proto.CodeOffsetInvalid, "chunk exceeds declared size")
				}
				sum := sha256.Sum256(d.Data)
				if sum != d.ChunkSHA256 {
					_ = s.SendError(proto.CodeChunkHashMismatch, "chunk at offset %d", d.Offset)
					return nil, proto.NewError(proto.CodeChunkHashMismatch, fmt.Sprintf("chunk hash mismatch at offset %d", d.Offset))
				}
				if _, err := rf.f.Write(d.Data); err != nil {
					_ = s.SendError(proto.CodeIOError, "write %s: %v", rf.entry.Name, err)
					return nil, fmt.Errorf("write part file: %w", err)
				}
				_, _ = rf.h.Write(d.Data)
				rf.written = end
				received += int64(len(d.Data))
				totalWritten += int64(len(d.Data))
				chunks++
			case end == rf.written:
				// Duplicate chunk (our ACK was lost); ACK again.
			default:
				_ = s.SendError(proto.CodeOffsetInvalid, "offset %d, expected %d", d.Offset, rf.written)
				return nil, proto.NewError(proto.CodeOffsetInvalid, fmt.Sprintf("offset %d, expected %d", d.Offset, rf.written))
			}
			if paused {
				pendingAck = true
				pendingIdx = uint32(cur)
				pendingOff = rf.written
				emitProgress("paused", cur)
			} else {
				if err := s.SendMsg(proto.TypeAck, &proto.Ack{
					TransferID: m.TransferID,
					FileIndex:  uint32(cur),
					Offset:     rf.written,
				}); err != nil {
					if cfg.Control != nil && cfg.Control.IsCancelled() {
						removeParts()
						return nil, ErrCancelled
					}
					if rerr := drainForRemoteError(); rerr != nil {
						return nil, rerr
					}
					return nil, err
				}
				emitProgress("transferring", cur)
			}

		case proto.TypeFin:
			readerDone = true
			fin, err := proto.ParseFin(frame.Payload)
			if err != nil {
				_ = s.SendError(proto.CodePayloadInvalid, "malformed FIN")
				return nil, err
			}
			totalWritten = batchTotal
			emitProgress("verifying", len(files)-1)
			for cur < len(files) && files[cur].written == files[cur].entry.Size {
				if err := files[cur].finish(); err != nil {
					return nil, err
				}
				cur++
			}
			if cur != len(files) {
				_ = s.SendError(proto.CodeProtocolViolation, "FIN before all files were written")
				return nil, proto.NewError(proto.CodeProtocolViolation, "FIN before all files were written")
			}
			if len(fin.Hashes) != len(files) {
				_ = s.SendError(proto.CodeFileHashMismatch, "expected %d file hashes, got %d", len(files), len(fin.Hashes))
				return nil, proto.NewError(proto.CodeFileHashMismatch, "file hash count mismatch")
			}
			for i := range files {
				var got [32]byte
				copy(got[:], files[i].h.Sum(nil))
				if got != fin.Hashes[i] {
					_ = s.SendError(proto.CodeFileHashMismatch, "file %s hash mismatch", files[i].entry.Name)
					return nil, proto.NewError(proto.CodeFileHashMismatch, fmt.Sprintf("file %s hash mismatch", files[i].entry.Name))
				}
			}
			for i := range files {
				if err := os.Rename(files[i].part, filepath.Join(outDir, files[i].entry.Name)); err != nil {
					_ = s.SendError(proto.CodeIOError, "rename %s: %v", files[i].entry.Name, err)
					return nil, fmt.Errorf("rename %s: %w", files[i].entry.Name, err)
				}
			}
			if err := s.SendMsg(proto.TypeFin, &proto.Fin{
				Status:     proto.FinOK,
				TransferID: m.TransferID,
				Hashes:     fin.Hashes,
			}); err != nil {
				return nil, err
			}

			res := &Result{
				TransferID: m.TransferID,
				Files:      names(m.Files),
				Bytes:      received,
				Chunks:     chunks,
				Duration:   time.Since(start),
			}
			if cfg.Log != nil {
				cfg.Log.Event("transfer.done", map[string]any{
					"transfer_id": fmt.Sprintf("%x", m.TransferID),
					"files":       len(res.Files),
					"bytes":       res.Bytes,
					"chunks":      res.Chunks,
					"duration_ms": res.Duration.Milliseconds(),
				})
			}
			return res, nil
		}
	}
}

// openPart opens (or creates) the part file, seeds the hash with the bytes
// already on disk and positions the writer at the resume offset.
func (rf *recvFile) openPart() error {
	f, err := os.OpenFile(rf.part, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open %s: %w", rf.entry.Name, err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return err
	}
	if rf.written > 0 {
		n, err := io.CopyN(rf.h, f, int64(rf.written))
		if err != nil {
			_ = f.Close()
			return fmt.Errorf("hash existing bytes of %s: %w", rf.entry.Name, err)
		}
		if uint64(n) != rf.written {
			_ = f.Close()
			return fmt.Errorf("hash existing bytes of %s: short read", rf.entry.Name)
		}
	}
	if _, err := f.Seek(int64(rf.written), io.SeekStart); err != nil {
		_ = f.Close()
		return err
	}
	rf.f = f
	return nil
}

// finish flushes and closes a completed part file.
func (rf *recvFile) finish() error {
	if rf.f == nil {
		return nil
	}
	if err := rf.f.Sync(); err != nil {
		_ = rf.f.Close()
		rf.f = nil
		return fmt.Errorf("sync %s: %w", rf.entry.Name, err)
	}
	if err := rf.f.Close(); err != nil {
		rf.f = nil
		return fmt.Errorf("close %s: %w", rf.entry.Name, err)
	}
	rf.f = nil
	return nil
}

func remoteErrorFrom(payload []byte) error {
	msg, err := proto.ParseError(payload)
	if err != nil {
		return proto.NewError(proto.CodePayloadInvalid, "malformed ERROR frame")
	}
	return &session.RemoteError{Code: msg.Code, Msg: msg.Message}
}

// safeName rejects names that could escape outDir.
func safeName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	return filepath.Base(name) == name
}
