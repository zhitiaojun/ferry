package transfer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cwlindean/filetransfer/core/proto"
	"github.com/cwlindean/filetransfer/core/session"
)

// Send streams paths to the peer over an established session using a sliding
// window of unacknowledged chunks so RTT does not stall throughput.
func Send(s *session.Session, paths []string, cfg Config) (*Result, error) {
	start := time.Now()
	if len(paths) == 0 {
		return nil, errors.New("transfer: no input files")
	}
	if cfg.Control != nil && cfg.Control.IsCancelled() {
		return nil, ErrCancelled
	}
	var inSendFile atomic.Bool
	if cfg.Control != nil {
		unreg := cfg.Control.OnCancel(func() {
			if !inSendFile.Load() {
				_ = s.Conn().SetReadDeadline(time.Now())
			}
		})
		defer unreg()
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
		if cfg.Control != nil && cfg.Control.IsCancelled() {
			return nil, ErrCancelled
		}
		return nil, err
	}
	startIdx, startOff, err := waitStart(s, tid, cfg.startTimeout(), cfg.Control)
	if err != nil {
		if errors.Is(err, ErrCancelled) || (cfg.Control != nil && cfg.Control.IsCancelled()) {
			if errors.Is(err, ErrCancelled) {
				sendCancelAndWait(s, "发送方已取消传输")
			}
			return nil, ErrCancelled
		}
		return nil, fmt.Errorf("manifest ack: %w", err)
	}

	buf := make([]byte, cfg.chunkSize())
	hashes := make([][32]byte, len(paths))
	batchTotal := int64(totalSize(entries))
	var completedBefore int64
	var sessionBase int64
	var total int64
	chunks := 0

	var slow *slowWatchdog
	if cfg.MinBytesPerSec > 0 {
		slow = startSlowWatchdog(s, batchTotal, cfg)
		defer slow.stop()
	}
	var upg *upgradeMonitor
	if cfg.UpgradeCh != nil {
		upg = startUpgradeMonitor(s, batchTotal, cfg)
		defer upg.stop()
	}

	for i, p := range paths {
		if entries[i].Size == 0 {
			hashes[i] = sha256.Sum256(nil)
			if cfg.OnDetailProgress != nil {
				cfg.OnDetailProgress(ProgressSnapshot{
					Stage:      "transferring",
					FileIndex:  i + 1,
					FileCount:  len(entries),
					FileName:   entries[i].Name,
					BytesDone:  completedBefore,
					BytesTotal: batchTotal,
				})
			}
			continue
		}
		if i < startIdx {
			hashes[i], err = hashFile(p)
			if err != nil {
				return nil, err
			}
			completedBefore += int64(entries[i].Size)
			continue
		}
		off := uint64(0)
		if i == startIdx {
			off = startOff
		}
		if slow != nil {
			slow.setConfirmed(completedBefore+int64(off), sessionBase)
		}
		if upg != nil {
			upg.setBatchDone(completedBefore + int64(off))
		}
		if cfg.OnDetailProgress != nil {
			cfg.OnDetailProgress(ProgressSnapshot{
				Stage:      "transferring",
				FileIndex:  i + 1,
				FileCount:  len(entries),
				FileName:   entries[i].Name,
				BytesDone:  completedBefore + int64(off),
				BytesTotal: batchTotal,
			})
		}
		inSendFile.Store(true)
		h, n, c, err := sendFile(s, tid, uint32(i), len(entries), entries[i].Name, entries[i].Size, p, buf, cfg, off, completedBefore, sessionBase, batchTotal, slow, upg)
		inSendFile.Store(false)
		if err != nil {
			if errors.Is(err, ErrPathUpgrade) {
				if upg != nil {
					upg.stop()
				}
				return nil, ErrPathUpgrade
			}
			if errors.Is(err, ErrCancelled) {
				return nil, ErrCancelled
			}
			if !errors.Is(err, ErrSlowPath) {
				_ = s.SendError(proto.CodeIOError, "send %s: %v", entries[i].Name, err)
			}
			return nil, err
		}
		hashes[i] = h
		completedBefore += int64(entries[i].Size)
		sessionBase += int64(entries[i].Size - off)
		total += n
		chunks += c
	}

	if slow != nil {
		slow.stop()
	}
	if upg != nil {
		upg.stop()
	}

	if cfg.Control != nil {
		sentPreFinPause := false
		for {
			stateCh := cfg.Control.StateCh()
			if cfg.Control.IsCancelled() {
				sendCancelAndWait(s, "发送方已取消传输")
				return nil, ErrCancelled
			}
			if !cfg.Control.IsAnyPaused() {
				break
			}
			localP := cfg.Control.IsPaused()
			if localP && !sentPreFinPause {
				if perr := s.SendMsg(proto.TypePing, &proto.Ping{Token: pingTokenPeerPaused}); perr != nil {
					return nil, perr
				}
				sentPreFinPause = true
			} else if !localP && sentPreFinPause {
				if perr := s.SendMsg(proto.TypePing, &proto.Ping{Token: pingTokenPeerResumed}); perr != nil {
					return nil, perr
				}
				sentPreFinPause = false
			}
			if cfg.OnDetailProgress != nil && len(entries) > 0 {
				cfg.OnDetailProgress(ProgressSnapshot{
					Stage:      cfg.Control.PauseStage(),
					FileIndex:  len(entries),
					FileCount:  len(entries),
					FileName:   entries[len(entries)-1].Name,
					BytesDone:  batchTotal,
					BytesTotal: batchTotal,
				})
			}
			timer := time.NewTimer(keepaliveInterval(cfg.timeout()))
			var kaErr error
			select {
			case <-stateCh:
				timer.Stop()
			case <-timer.C:
				tok := pingTokenKeepalive
				if cfg.Control.IsPaused() {
					tok = pingTokenPeerPaused
				}
				if perr := s.SendMsg(proto.TypePing, &proto.Ping{Token: tok}); perr != nil {
					kaErr = perr
				}
			}
			if kaErr != nil {
				return nil, kaErr
			}
		}
		if sentPreFinPause {
			if perr := s.SendMsg(proto.TypePing, &proto.Ping{Token: pingTokenPeerResumed}); perr != nil {
				return nil, perr
			}
		}
	}

	if cfg.OnDetailProgress != nil && len(entries) > 0 {
		cfg.OnDetailProgress(ProgressSnapshot{
			Stage:      "verifying",
			FileIndex:  len(entries),
			FileCount:  len(entries),
			FileName:   entries[len(entries)-1].Name,
			BytesDone:  batchTotal,
			BytesTotal: batchTotal,
		})
	}
	fin := &proto.Fin{Status: proto.FinOK, TransferID: tid, Hashes: hashes}
	if err := s.SendMsg(proto.TypeFin, fin); err != nil {
		if cfg.Control != nil && cfg.Control.IsCancelled() {
			sendCancelAndWait(s, "发送方已取消传输")
			return nil, ErrCancelled
		}
		return nil, err
	}
	peerFin, err := waitFin(s, cfg.timeout()*3, cfg.Control)
	if err != nil {
		if errors.Is(err, ErrCancelled) || (cfg.Control != nil && cfg.Control.IsCancelled()) {
			if errors.Is(err, ErrCancelled) {
				sendCancelAndWait(s, "发送方已取消传输")
			}
			return nil, ErrCancelled
		}
		return nil, fmt.Errorf("final FIN: %w", err)
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

// sendFile streams chunks of one file up to cfg.windowBytes() in flight while
// a concurrent ACK reader advances the window and emits progress.
func sendFile(
	s *session.Session,
	tid [16]byte,
	index uint32,
	fileCount int,
	fileName string,
	fileSize uint64,
	path string,
	buf []byte,
	cfg Config,
	startOffset uint64,
	completedBefore int64,
	sessionBase int64,
	batchTotal int64,
	slow *slowWatchdog,
	upg *upgradeMonitor,
) ([32]byte, int64, int, error) {
	var zero [32]byte

	f, err := os.Open(path)
	if err != nil {
		return zero, 0, 0, err
	}
	defer f.Close()

	h := sha256.New()
	if startOffset > 0 {
		if _, err := io.CopyN(h, f, int64(startOffset)); err != nil {
			return zero, 0, 0, fmt.Errorf("hash %d resumed bytes of %s: %w", startOffset, path, err)
		}
	}
	if startOffset >= fileSize {
		var sum [32]byte
		copy(sum[:], h.Sum(nil))
		return sum, int64(startOffset), 0, nil
	}

	windowBytes := uint64(cfg.windowBytes())
	var mu sync.Mutex
	cond := sync.NewCond(&mu)
	if slow != nil {
		slow.attachCond(cond)
		defer slow.attachCond(nil)
	}
	if upg != nil {
		upg.attachCond(cond)
		defer upg.attachCond(nil)
	}
	fileDone := make(chan struct{})
	defer close(fileDone)
	sendLoopDone := make(chan struct{})
	var sendLoopOnce sync.Once
	markSendLoopDone := func() {
		sendLoopOnce.Do(func() { close(sendLoopDone) })
	}
	defer markSendLoopDone()
	if cfg.Control != nil {
		unregFile := cfg.Control.OnCancel(func() {
			mu.Lock()
			cond.Broadcast()
			mu.Unlock()
			go func() {
				select {
				case <-sendLoopDone:
				case <-time.After(cancelWriteTimeout):
					_ = s.Conn().SetDeadline(time.Now())
				}
			}()
		})
		defer unregFile()

		go func() {
			for {
				ch := cfg.Control.StateCh()
				if ch == nil {
					return
				}
				select {
				case <-fileDone:
					return
				case <-ch:
					mu.Lock()
					cond.Broadcast()
					mu.Unlock()
				}
			}
		}()
	}

	sentOff := startOffset
	ackedOff := startOffset
	var recvErr error
	failSig := make(chan struct{})
	var failOnce sync.Once
	signalAckFail := func() {
		failOnce.Do(func() { close(failSig) })
	}

	pongCh := make(chan *proto.Ping, 4)
	flushPong := func() error {
		for {
			select {
			case p := <-pongCh:
				if err := s.SendMsg(proto.TypePong, p); err != nil {
					return err
				}
			default:
				return nil
			}
		}
	}

	ackDone := make(chan error, 1)
	go func() {
		ackDone <- readFileAcks(s, tid, index, fileCount, fileName, fileSize, startOffset, completedBefore, sessionBase, batchTotal, cfg, slow, upg, &mu, cond, &sentOff, &ackedOff, &recvErr, signalAckFail, pongCh)
	}()

	waitSendPause := func() error {
		if cfg.Control == nil || !cfg.Control.IsAnyPaused() {
			return nil
		}
		sentPeerPause := false
		for {
			stateCh := cfg.Control.StateCh()
			if cfg.Control.IsCancelled() {
				return ErrCancelled
			}
			if !cfg.Control.IsAnyPaused() {
				break
			}
			mu.Lock()
			curAck := ackedOff
			rerr := recvErr
			mu.Unlock()
			if rerr != nil {
				return rerr
			}
			if upg != nil && upg.isTriggered() {
				return ErrPathUpgrade
			}
			localP := cfg.Control.IsPaused()
			if localP && !sentPeerPause {
				if perr := s.SendMsg(proto.TypePing, &proto.Ping{Token: pingTokenPeerPaused}); perr != nil {
					return perr
				}
				sentPeerPause = true
			} else if !localP && sentPeerPause {
				if perr := s.SendMsg(proto.TypePing, &proto.Ping{Token: pingTokenPeerResumed}); perr != nil {
					return perr
				}
				sentPeerPause = false
			}
			if cfg.OnDetailProgress != nil {
				cfg.OnDetailProgress(ProgressSnapshot{
					Stage:      cfg.Control.PauseStage(),
					FileIndex:  int(index) + 1,
					FileCount:  fileCount,
					FileName:   fileName,
					BytesDone:  completedBefore + int64(curAck),
					BytesTotal: batchTotal,
				})
			}
			ka := keepaliveInterval(cfg.timeout())
			timer := time.NewTimer(ka)
			var loopErr error
			select {
			case <-failSig:
				timer.Stop()
				mu.Lock()
				rerr = recvErr
				mu.Unlock()
				loopErr = rerr
			case p := <-pongCh:
				timer.Stop()
				if perr := s.SendMsg(proto.TypePong, p); perr != nil {
					loopErr = perr
				}
			case <-stateCh:
				timer.Stop()
			case <-timer.C:
				tok := pingTokenKeepalive
				if cfg.Control.IsPaused() {
					tok = pingTokenPeerPaused
				}
				if perr := s.SendMsg(proto.TypePing, &proto.Ping{Token: tok}); perr != nil {
					loopErr = perr
				}
			}
			if loopErr != nil {
				return loopErr
			}
		}
		if sentPeerPause {
			if perr := s.SendMsg(proto.TypePing, &proto.Ping{Token: pingTokenPeerResumed}); perr != nil {
				return perr
			}
		}
		if cfg.OnDetailProgress != nil {
			mu.Lock()
			curAck := ackedOff
			mu.Unlock()
			cfg.OnDetailProgress(ProgressSnapshot{
				Stage:      "transferring",
				FileIndex:  int(index) + 1,
				FileCount:  fileCount,
				FileName:   fileName,
				BytesDone:  completedBefore + int64(curAck),
				BytesTotal: batchTotal,
			})
		}
		return nil
	}

	chunks := 0
	var writeErr error
	for {
		if perr := flushPong(); perr != nil {
			writeErr = perr
			break
		}
		if cfg.Control != nil && cfg.Control.IsCancelled() {
			writeErr = ErrCancelled
			break
		}
		if cfg.Control != nil && cfg.Control.IsAnyPaused() {
			if perr := waitSendPause(); perr != nil {
				writeErr = perr
				break
			}
		}
		if upg != nil && upg.isTriggered() {
			writeErr = ErrPathUpgrade
			break
		}
		n, rerr := f.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			sum := sha256.Sum256(chunk)

			var pausedErr error
			mu.Lock()
			for {
				for (sentOff-ackedOff) >= windowBytes && recvErr == nil && len(pongCh) == 0 && (slow == nil || !slow.isTriggered()) && (upg == nil || !upg.isTriggered()) && (cfg.Control == nil || (!cfg.Control.IsCancelled() && !cfg.Control.IsAnyPaused())) {
					cond.Wait()
				}
				if len(pongCh) > 0 && recvErr == nil {
					mu.Unlock()
					if perr := flushPong(); perr != nil {
						pausedErr = perr
						break
					}
					mu.Lock()
					continue
				}
				if cfg.Control != nil && !cfg.Control.IsCancelled() && cfg.Control.IsAnyPaused() && recvErr == nil && (slow == nil || !slow.isTriggered()) && (upg == nil || !upg.isTriggered()) {
					mu.Unlock()
					if perr := waitSendPause(); perr != nil {
						pausedErr = perr
						break
					}
					mu.Lock()
					continue
				}
				break
			}
			if pausedErr != nil {
				writeErr = pausedErr
				break
			}
			if cfg.Control != nil && cfg.Control.IsCancelled() {
				mu.Unlock()
				writeErr = ErrCancelled
				break
			}
			if slow != nil && slow.isTriggered() {
				mu.Unlock()
				writeErr = ErrSlowPath
				break
			}
			if upg != nil && upg.isTriggered() {
				mu.Unlock()
				writeErr = ErrPathUpgrade
				break
			}
			if recvErr != nil {
				writeErr = recvErr
				mu.Unlock()
				break
			}
			off := sentOff
			sentOff += uint64(n)
			mu.Unlock()

			msg := &proto.Data{
				TransferID:  tid,
				FileIndex:   index,
				Offset:      off,
				ChunkSHA256: sum,
				Data:        chunk,
			}
			if err := s.SendMsg(proto.TypeData, msg); err != nil {
				if cfg.Control != nil && cfg.Control.IsCancelled() {
					writeErr = ErrCancelled
				} else {
					writeErr = err
					_ = s.Conn().SetReadDeadline(time.Now())
				}
				break
			}
			_, _ = h.Write(chunk)
			chunks++
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			writeErr = rerr
			_ = s.Conn().SetReadDeadline(time.Now())
			break
		}
	}

	if writeErr == nil {
		var pausedErr error
		mu.Lock()
		for {
			for ackedOff < fileSize && recvErr == nil && len(pongCh) == 0 && (slow == nil || !slow.isTriggered()) && (upg == nil || !upg.isTriggered()) && (cfg.Control == nil || (!cfg.Control.IsCancelled() && !cfg.Control.IsAnyPaused())) {
				cond.Wait()
			}
			if len(pongCh) > 0 && recvErr == nil {
				mu.Unlock()
				if perr := flushPong(); perr != nil {
					pausedErr = perr
					break
				}
				mu.Lock()
				continue
			}
			if cfg.Control != nil && !cfg.Control.IsCancelled() && cfg.Control.IsAnyPaused() && recvErr == nil && (slow == nil || !slow.isTriggered()) && (upg == nil || !upg.isTriggered()) {
				mu.Unlock()
				if perr := waitSendPause(); perr != nil {
					pausedErr = perr
					break
				}
				mu.Lock()
				continue
			}
			break
		}
		if pausedErr != nil {
			writeErr = pausedErr
		} else {
			if cfg.Control != nil && cfg.Control.IsCancelled() {
				writeErr = ErrCancelled
			}
			mu.Unlock()
		}
	}

	if errors.Is(writeErr, ErrCancelled) || (cfg.Control != nil && cfg.Control.IsCancelled()) {
		markSendLoopDone()
		_ = s.Conn().SetWriteDeadline(time.Now().Add(cancelWriteTimeout))
		_ = s.SendError(proto.CodeCancelled, "发送方已取消传输")
		_ = s.Conn().SetWriteDeadline(time.Time{})
		_ = s.Conn().SetReadDeadline(time.Now().Add(cancelDrainGrace))
		fuse := time.AfterFunc(cancelDrainGrace, func() {
			_ = s.Conn().SetReadDeadline(time.Now())
		})
		ackErr := <-ackDone
		fuse.Stop()
		if ackErr == nil {
			graceWaitPeerClose(s, cancelDrainGrace)
		}
		return zero, 0, 0, ErrCancelled
	}

	if upg != nil && upg.isTriggered() {
		// Notify peer while readFileAcks is still draining in-flight ACKs so
		// unbuffered transports (such as net.Pipe) cannot deadlock, then stop ACK reader.
		_ = s.Conn().SetWriteDeadline(time.Now().Add(300 * time.Millisecond))
		_ = s.SendError(proto.CodePathSwitch, "upgrading to direct path")
		_ = s.Conn().SetWriteDeadline(time.Time{})
		_ = s.Conn().SetReadDeadline(time.Now())
		<-ackDone
		return zero, 0, 0, ErrPathUpgrade
	}

	ackErr := <-ackDone
	if slow != nil && slow.isTriggered() {
		return zero, 0, 0, ErrSlowPath
	}
	if writeErr != nil {
		var remote *session.RemoteError
		if errors.As(ackErr, &remote) {
			return zero, 0, 0, ackErr
		}
		return zero, 0, 0, writeErr
	}
	if ackErr != nil {
		return zero, 0, 0, ackErr
	}

	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum, int64(fileSize), chunks, nil
}

func readFileAcks(
	s *session.Session,
	tid [16]byte,
	index uint32,
	fileCount int,
	fileName string,
	fileSize uint64,
	startOffset uint64,
	completedBefore int64,
	sessionBase int64,
	batchTotal int64,
	cfg Config,
	slow *slowWatchdog,
	upg *upgradeMonitor,
	mu *sync.Mutex,
	cond *sync.Cond,
	sentOff *uint64,
	ackedOff *uint64,
	recvErr *error,
	signalFail func(),
	pongCh chan<- *proto.Ping,
) error {
	fail := func(err error) error {
		mu.Lock()
		*recvErr = err
		cond.Broadcast()
		mu.Unlock()
		if signalFail != nil {
			signalFail()
		}
		return err
	}

	var cancelDeadline time.Time
	for {
		timeout := cfg.timeout()
		if cfg.Control != nil && cfg.Control.IsCancelled() {
			if cancelDeadline.IsZero() {
				cancelDeadline = time.Now().Add(cancelDrainGrace)
			}
			rem := time.Until(cancelDeadline)
			if rem <= 0 {
				return fail(ErrCancelled)
			}
			if rem < timeout {
				timeout = rem
			}
		}
		fr, err := s.Recv(timeout)
		if err != nil {
			if cfg.Control != nil && cfg.Control.IsCancelled() {
				return fail(ErrCancelled)
			}
			return fail(err)
		}
		if cfg.Control != nil && cfg.Control.IsCancelled() {
			if fr.Type == proto.TypeError {
				return fail(ErrCancelled)
			}
			continue
		}
		switch fr.Type {
		case proto.TypePing:
			if p, perr := proto.ParsePing(fr.Payload); perr == nil {
				if pongCh != nil {
					select {
					case pongCh <- p:
					default:
					}
					mu.Lock()
					cond.Broadcast()
					mu.Unlock()
				}
				switch p.Token {
				case pingTokenPeerPaused:
					if cfg.Control != nil {
						cfg.Control.SetPeerPaused(true)
					}
					if slow != nil {
						slow.notePeerPaused()
					}
					if cfg.OnDetailProgress != nil {
						mu.Lock()
						curAck := *ackedOff
						mu.Unlock()
						cfg.OnDetailProgress(ProgressSnapshot{
							Stage:      cfg.Control.PauseStage(),
							FileIndex:  int(index) + 1,
							FileCount:  fileCount,
							FileName:   fileName,
							BytesDone:  completedBefore + int64(curAck),
							BytesTotal: batchTotal,
						})
					}
				case pingTokenPeerResumed:
					if cfg.Control != nil {
						cfg.Control.SetPeerPaused(false)
					}
					if cfg.OnDetailProgress != nil {
						mu.Lock()
						curAck := *ackedOff
						mu.Unlock()
						cfg.OnDetailProgress(ProgressSnapshot{
							Stage:      cfg.Control.PauseStage(),
							FileIndex:  int(index) + 1,
							FileCount:  fileCount,
							FileName:   fileName,
							BytesDone:  completedBefore + int64(curAck),
							BytesTotal: batchTotal,
						})
					}
				default:
					if slow != nil {
						slow.notePeerPaused()
					}
				}
			} else if slow != nil {
				slow.notePeerPaused()
			}
			continue
		case proto.TypePong:
			continue
		case proto.TypeError:
			return fail(remoteErrorFrom(fr.Payload))
		case proto.TypeAck:
		default:
			return fail(proto.NewError(proto.CodeUnexpectedMessage, fmt.Sprintf("want ACK, got %s", fr.Type)))
		}
		ack, err := proto.ParseAck(fr.Payload)
		if err != nil {
			return fail(proto.NewError(proto.CodePayloadInvalid, "ACK did not decode"))
		}
		if ack.TransferID != tid {
			return fail(proto.NewError(proto.CodeProtocolViolation, "ACK transfer_id mismatch"))
		}
		if ack.FileIndex != index {
			continue
		}
		if ack.Offset > fileSize {
			return fail(proto.NewError(proto.CodeOffsetInvalid, fmt.Sprintf("ACK offset %d exceeds file size %d", ack.Offset, fileSize)))
		}

		mu.Lock()
		if ack.Offset > *sentOff {
			sent := *sentOff
			mu.Unlock()
			return fail(proto.NewError(proto.CodeOffsetInvalid, fmt.Sprintf("ACK offset %d ahead of sent %d", ack.Offset, sent)))
		}
		if ack.Offset <= *ackedOff {
			mu.Unlock()
			continue
		}
		*ackedOff = ack.Offset
		curAck := *ackedOff
		cond.Broadcast()
		mu.Unlock()

		if slow != nil {
			slow.setConfirmed(completedBefore+int64(curAck), sessionBase+int64(curAck-startOffset))
		}
		if upg != nil {
			upg.setBatchDone(completedBefore + int64(curAck))
		}
		if cfg.OnProgress != nil {
			cfg.OnProgress(int64(curAck))
		}
		if cfg.OnDetailProgress != nil {
			stage := "transferring"
			if cfg.Control != nil {
				stage = cfg.Control.PauseStage()
			}
			cfg.OnDetailProgress(ProgressSnapshot{
				Stage:      stage,
				FileIndex:  int(index) + 1,
				FileCount:  fileCount,
				FileName:   fileName,
				BytesDone:  completedBefore + int64(curAck),
				BytesTotal: batchTotal,
			})
		}
		if curAck == fileSize {
			return nil
		}
	}
}

type speedSample struct {
	t     time.Time
	bytes int64
}

type slowWatchdog struct {
	s          *session.Session
	batchTotal int64
	minBps     float64
	grace      time.Duration
	win        time.Duration
	minRem     int64
	ctrl       *Controller
	start      time.Time

	mu              sync.Mutex
	batchDone       int64
	sessDone        int64
	peerPausedUntil time.Time
	triggered       bool
	stopped         bool
	cond            *sync.Cond
	stopCh          chan struct{}
	doneCh          chan struct{}
}

func startSlowWatchdog(s *session.Session, batchTotal int64, cfg Config) *slowWatchdog {
	now := time.Now()
	w := &slowWatchdog{
		s:          s,
		batchTotal: batchTotal,
		minBps:     float64(cfg.MinBytesPerSec),
		grace:      cfg.slowGrace(),
		win:        cfg.slowWindow(),
		minRem:     cfg.slowMinRemaining(),
		ctrl:       cfg.Control,
		start:      now,
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
	}
	go w.run()
	return w
}

func (w *slowWatchdog) notePeerPaused() {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.peerPausedUntil = time.Now().Add(15 * time.Second)
	w.mu.Unlock()
}

func (w *slowWatchdog) setConfirmed(batchDone, sessDone int64) {
	w.mu.Lock()
	if batchDone > w.batchDone {
		w.batchDone = batchDone
	}
	if sessDone > w.sessDone {
		w.sessDone = sessDone
	}
	w.mu.Unlock()
}

func (w *slowWatchdog) attachCond(c *sync.Cond) {
	w.mu.Lock()
	w.cond = c
	w.mu.Unlock()
}

func (w *slowWatchdog) isTriggered() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.triggered
}

func (w *slowWatchdog) stop() {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.stopped = true
	close(w.stopCh)
	w.mu.Unlock()
	<-w.doneCh
}

func (w *slowWatchdog) run() {
	defer close(w.doneCh)
	interval := w.win / 10
	if interval < 40*time.Millisecond {
		interval = 40 * time.Millisecond
	}
	if interval > 200*time.Millisecond {
		interval = 200 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	samples := []speedSample{{t: w.start, bytes: 0}}
	for {
		select {
		case <-w.stopCh:
			return
		case now := <-ticker.C:
			w.mu.Lock()
			batchDone := w.batchDone
			sessDone := w.sessDone
			peerPaused := now.Before(w.peerPausedUntil)
			cond := w.cond
			w.mu.Unlock()

			if (w.ctrl != nil && w.ctrl.IsAnyPaused()) || peerPaused {
				samples = []speedSample{{t: now, bytes: sessDone}}
				continue
			}

			samples = append(samples, speedSample{t: now, bytes: sessDone})
			cutoff := now.Add(-(w.win + 2*time.Second))
			prune := 0
			for prune < len(samples)-1 && samples[prune].t.Before(cutoff) {
				prune++
			}
			if prune > 0 {
				samples = samples[prune:]
			}

			if now.Sub(w.start) < w.grace+w.win {
				continue
			}
			if w.batchTotal-batchDone < w.minRem {
				continue
			}

			target := now.Add(-w.win)
			refIdx := -1
			for i := len(samples) - 1; i >= 0; i-- {
				if !samples[i].t.After(target) {
					refIdx = i
					break
				}
			}
			if refIdx < 0 {
				continue
			}
			dt := now.Sub(samples[refIdx].t).Seconds()
			if dt <= 0 {
				continue
			}
			rate := float64(sessDone-samples[refIdx].bytes) / dt
			if rate < w.minBps {
				w.mu.Lock()
				w.triggered = true
				cond = w.cond
				w.mu.Unlock()
				_ = w.s.Conn().SetDeadline(time.Now())
				if cond != nil {
					cond.L.Lock()
					cond.Broadcast()
					cond.L.Unlock()
				}
				return
			}
		}
	}
}

type upgradeMonitor struct {
	s          *session.Session
	batchTotal int64
	minRem     int64
	upgradeCh  <-chan struct{}

	batchDone atomic.Int64
	triggered atomic.Bool

	mu      sync.Mutex
	cond    *sync.Cond
	stopped bool
	stopCh  chan struct{}
	doneCh  chan struct{}
}

func startUpgradeMonitor(s *session.Session, batchTotal int64, cfg Config) *upgradeMonitor {
	u := &upgradeMonitor{
		s:          s,
		batchTotal: batchTotal,
		minRem:     cfg.upgradeMinRemaining(),
		upgradeCh:  cfg.UpgradeCh,
		stopCh:     make(chan struct{}),
		doneCh:     make(chan struct{}),
	}
	go u.run()
	return u
}

func (u *upgradeMonitor) setBatchDone(v int64) {
	for {
		cur := u.batchDone.Load()
		if v <= cur {
			return
		}
		if u.batchDone.CompareAndSwap(cur, v) {
			return
		}
	}
}

func (u *upgradeMonitor) attachCond(c *sync.Cond) {
	u.mu.Lock()
	u.cond = c
	u.mu.Unlock()
}

func (u *upgradeMonitor) isTriggered() bool {
	return u.triggered.Load()
}

func (u *upgradeMonitor) stop() {
	u.mu.Lock()
	if u.stopped {
		u.mu.Unlock()
		return
	}
	u.stopped = true
	close(u.stopCh)
	u.mu.Unlock()
	<-u.doneCh
}

func (u *upgradeMonitor) run() {
	defer close(u.doneCh)
	select {
	case <-u.stopCh:
		return
	case <-u.upgradeCh:
	}

	if u.batchTotal-u.batchDone.Load() < u.minRem {
		return
	}
	u.triggered.Store(true)

	u.mu.Lock()
	cond := u.cond
	u.mu.Unlock()
	if cond != nil {
		cond.L.Lock()
		cond.Broadcast()
		cond.L.Unlock()
	}

	// Safety fuse: if a blocked SendMsg stalls teardown, expire deadlines.
	fuse := time.NewTimer(500 * time.Millisecond)
	defer fuse.Stop()
	select {
	case <-u.stopCh:
	case <-fuse.C:
		_ = u.s.Conn().SetDeadline(time.Now())
	}
}

// waitStart waits for the receiver's answer to MANIFEST: ACK(0,0) for a fresh
// transfer, or RESUME(file, offset) when a .ftpart file is being continued.
func waitStart(s *session.Session, tid [16]byte, timeout time.Duration, ctrl *Controller) (int, uint64, error) {
	for {
		if ctrl != nil && ctrl.IsCancelled() {
			return 0, 0, ErrCancelled
		}
		f, err := s.Recv(timeout)
		if err != nil {
			if ctrl != nil && ctrl.IsCancelled() {
				return 0, 0, ErrCancelled
			}
			return 0, 0, err
		}
		switch f.Type {
		case proto.TypePing:
			if p, perr := proto.ParsePing(f.Payload); perr == nil {
				_ = s.SendMsg(proto.TypePong, p)
			}
		case proto.TypePong:
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
}

func waitFin(s *session.Session, timeout time.Duration, ctrl *Controller) (*proto.Fin, error) {
	for {
		if ctrl != nil && ctrl.IsCancelled() {
			return nil, ErrCancelled
		}
		f, err := s.Recv(timeout)
		if err != nil {
			if ctrl != nil && ctrl.IsCancelled() {
				return nil, ErrCancelled
			}
			return nil, err
		}
		switch f.Type {
		case proto.TypePing:
			if p, perr := proto.ParsePing(f.Payload); perr == nil {
				_ = s.SendMsg(proto.TypePong, p)
			}
		case proto.TypePong:
		case proto.TypeError:
			return nil, remoteErrorFrom(f.Payload)
		case proto.TypeFin:
			fin, err := proto.ParseFin(f.Payload)
			if err != nil {
				return nil, proto.NewError(proto.CodePayloadInvalid, "peer FIN did not decode")
			}
			return fin, nil
		default:
			return nil, proto.NewError(proto.CodeUnexpectedMessage, fmt.Sprintf("want FIN, got %s", f.Type))
		}
	}
}

func keepaliveInterval(timeout time.Duration) time.Duration {
	ka := timeout / 3
	if ka < 100*time.Millisecond {
		ka = 100 * time.Millisecond
	}
	if ka > 10*time.Second {
		ka = 10 * time.Second
	}
	return ka
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
