package transfer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cwlindean/filetransfer/core/logging"
	"github.com/cwlindean/filetransfer/core/proto"
	"github.com/cwlindean/filetransfer/core/session"
)

func testParams(name string) session.Params {
	var id [32]byte
	copy(id[:], sha256Sum(name))
	return session.Params{DeviceID: id, Name: name, Capabilities: proto.CapResume}
}

func sha256Sum(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

func sessionPair(t *testing.T) (*session.Session, *session.Session) {
	t.Helper()
	a, b := net.Pipe()
	type accepted struct {
		s   *session.Session
		err error
	}
	ch := make(chan accepted, 1)
	go func() {
		s, err := session.Accept(b, testParams("recv"))
		ch <- accepted{s, err}
	}()
	sa, err := session.Dial(a, testParams("send"))
	if err != nil {
		t.Fatalf("dial side: %v", err)
	}
	got := <-ch
	if got.err != nil {
		t.Fatalf("accept side: %v", got.err)
	}
	t.Cleanup(func() {
		_ = sa.Close()
		_ = got.s.Close()
	})
	return sa, got.s
}

func writePattern(t *testing.T, path string, size int64) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	buf := make([]byte, 32*1024)
	var written int64
	for written < size {
		n := int64(len(buf))
		if size-written < n {
			n = size - written
		}
		for i := range buf[:n] {
			buf[i] = byte((written + int64(i)) % 251)
		}
		if _, err := f.Write(buf[:n]); err != nil {
			t.Fatal(err)
		}
		written += n
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func fileHash(t *testing.T, path string) string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func roundTrip(t *testing.T, files []string, outDir string, chunk uint32, cfg Config) (*Result, *Result) {
	t.Helper()
	sa, sb := sessionPair(t)
	cfg.ChunkSize = chunk
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}

	type recvOut struct {
		res *Result
		err error
	}
	ch := make(chan recvOut, 1)
	go func() {
		res, err := Receive(sb, outDir, cfg)
		ch <- recvOut{res, err}
	}()

	sendRes, err := Send(sa, files, cfg)
	if err != nil {
		t.Fatalf("send: %v", err)
	}
	got := <-ch
	if got.err != nil {
		t.Fatalf("receive: %v", got.err)
	}
	return sendRes, got.res
}

// TestRoundTripHash covers G1: 2.1 MB and 10 MB, three runs each, hashes must
// match and nothing may be left behind.
func TestRoundTripHash(t *testing.T) {
	cases := []struct {
		name string
		size int64
	}{
		{"2.1MB", 2_100_000},
		{"10MB", 10 << 20},
	}
	for _, tc := range cases {
		for run := 1; run <= 3; run++ {
			t.Run(fmt.Sprintf("%s-run%d", tc.name, run), func(t *testing.T) {
				dir := t.TempDir()
				src := filepath.Join(dir, "payload.bin")
				writePattern(t, src, tc.size)
				outDir := filepath.Join(dir, "out")

				sendRes, recvRes := roundTrip(t, []string{src}, outDir, 0, Config{})
				if recvRes.Bytes != tc.size {
					t.Fatalf("received %d bytes, want %d", recvRes.Bytes, tc.size)
				}
				want := fileHash(t, src)
				got := fileHash(t, filepath.Join(outDir, "payload.bin"))
				if want != got {
					t.Fatalf("hash mismatch: want %s got %s", want, got)
				}
				if sendRes.Chunks != recvRes.Chunks {
					t.Fatalf("chunk count differs: send %d recv %d", sendRes.Chunks, recvRes.Chunks)
				}
				if _, err := os.Stat(filepath.Join(outDir, "payload.bin.ftpart")); err == nil {
					t.Fatal("part file left behind after success")
				}
			})
		}
	}
}

func TestEmptyFile(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.bin")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	small := filepath.Join(dir, "small.txt")
	if err := os.WriteFile(small, []byte("hello filetransfer"), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "out")

	_, recvRes := roundTrip(t, []string{empty, small}, outDir, 0, Config{})
	if recvRes.Bytes != int64(len("hello filetransfer")) {
		t.Fatalf("bytes: %d", recvRes.Bytes)
	}
	if st, err := os.Stat(filepath.Join(outDir, "empty.bin")); err != nil || st.Size() != 0 {
		t.Fatalf("empty.bin: %v", err)
	}
	if got, want := fileHash(t, filepath.Join(outDir, "small.txt")), fileHash(t, small); got != want {
		t.Fatalf("small.txt hash mismatch")
	}
}

// TestResumeFromPartFile rehearses §6.3: a .ftpart file from an earlier
// interrupted run makes the receiver ask for a RESUME offset.
func TestResumeFromPartFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "payload.bin")
	writePattern(t, src, 2_100_000)
	outDir := filepath.Join(dir, "out")
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Pretend an earlier transfer wrote the first 700000 bytes.
	full, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outDir, "payload.bin.ftpart"), full[:700_000], 0o644); err != nil {
		t.Fatal(err)
	}

	logPath := filepath.Join(dir, "log.jsonl")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()

	_, recvRes := roundTrip(t, []string{src}, outDir, 65536, Config{Log: logging.New(logFile)})
	if recvRes.Bytes != 2_100_000-700_000 {
		t.Fatalf("transferred %d bytes, want %d", recvRes.Bytes, 2_100_000-700_000)
	}
	if got, want := fileHash(t, filepath.Join(outDir, "payload.bin")), fileHash(t, src); got != want {
		t.Fatalf("hash mismatch after resume")
	}
}

func TestDuplicateChunkAccepted(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "dup.bin")
	writePattern(t, src, 300_000)
	outDir := filepath.Join(dir, "out")

	sa, sb := sessionPair(t)

	recvCh := make(chan error, 1)
	go func() {
		_, err := Receive(sb, outDir, Config{Timeout: 10 * time.Second})
		recvCh <- err
	}()

	entry := proto.FileEntry{Name: "dup.bin", Size: 300_000}
	manifest := &proto.Manifest{TransferID: [16]byte{1}, ChunkSize: 65536, Files: []proto.FileEntry{entry}}
	if err := sa.SendMsg(proto.TypeManifest, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := sa.Expect(proto.TypeAck, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	chunk := data[:65536]
	sum := sha256.Sum256(chunk)
	msg := &proto.Data{TransferID: manifest.TransferID, FileIndex: 0, Offset: 0, ChunkSHA256: sum, Data: chunk}
	for i := 0; i < 2; i++ { // the second copy is the duplicate
		if err := sa.SendMsg(proto.TypeData, msg); err != nil {
			t.Fatal(err)
		}
		if _, err := sa.Expect(proto.TypeAck, 5*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	_ = sa.Close() // receiver ends with an IO error after the handshake
	if err := <-recvCh; err == nil {
		t.Fatal("expected an error when the sender disappears")
	}
}

func TestChunkHashMismatch(t *testing.T) {
	dir := t.TempDir()
	outDir := filepath.Join(dir, "out")
	sa, sb := sessionPair(t)

	recvCh := make(chan error, 1)
	go func() {
		_, err := Receive(sb, outDir, Config{Timeout: 10 * time.Second})
		recvCh <- err
	}()

	manifest := &proto.Manifest{
		TransferID: [16]byte{2},
		ChunkSize:  65536,
		Files:      []proto.FileEntry{{Name: "bad.bin", Size: 10}},
	}
	if err := sa.SendMsg(proto.TypeManifest, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := sa.Expect(proto.TypeAck, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	bad := &proto.Data{
		TransferID:  manifest.TransferID,
		FileIndex:   0,
		Offset:      0,
		ChunkSHA256: sha256.Sum256([]byte("other")),
		Data:        []byte("0123456789"),
	}
	if err := sa.SendMsg(proto.TypeData, bad); err != nil {
		t.Fatal(err)
	}
	// Read the ERROR frame first: the receiver blocks in the write otherwise.
	// Expect turns an ERROR frame into *session.RemoteError.
	_, err := sa.Expect(proto.TypeError, 5*time.Second)
	var remote *session.RemoteError
	if !errors.As(err, &remote) || remote.Code != proto.CodeChunkHashMismatch {
		t.Fatalf("want chunk_hash_mismatch in ERROR, got %v", err)
	}
	err = <-recvCh
	var pe *proto.Error
	if !errors.As(err, &pe) || pe.Code != proto.CodeChunkHashMismatch {
		t.Fatalf("want chunk_hash_mismatch, got %v", err)
	}
}

func TestOffsetInvalid(t *testing.T) {
	dir := t.TempDir()
	sa, sb := sessionPair(t)

	recvCh := make(chan error, 1)
	go func() {
		_, err := Receive(sb, filepath.Join(dir, "out"), Config{Timeout: 10 * time.Second})
		recvCh <- err
	}()

	manifest := &proto.Manifest{
		TransferID: [16]byte{3},
		ChunkSize:  65536,
		Files:      []proto.FileEntry{{Name: "gap.bin", Size: 100}},
	}
	if err := sa.SendMsg(proto.TypeManifest, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := sa.Expect(proto.TypeAck, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	data := []byte("0123456789")
	msg := &proto.Data{
		TransferID:  manifest.TransferID,
		FileIndex:   0,
		Offset:      10, // gap: expected 0
		ChunkSHA256: sha256.Sum256(data),
		Data:        data,
	}
	if err := sa.SendMsg(proto.TypeData, msg); err != nil {
		t.Fatal(err)
	}
	_, err := sa.Expect(proto.TypeError, 5*time.Second)
	var remote *session.RemoteError
	if !errors.As(err, &remote) || remote.Code != proto.CodeOffsetInvalid {
		t.Fatalf("want offset_invalid in ERROR, got %v", err)
	}
	err = <-recvCh
	var pe *proto.Error
	if !errors.As(err, &pe) || pe.Code != proto.CodeOffsetInvalid {
		t.Fatalf("want offset_invalid, got %v", err)
	}
}

func TestDisconnectMidTransfer(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "cut.bin")
	writePattern(t, src, 1<<20)
	outDir := filepath.Join(dir, "out")

	a, b := net.Pipe()
	type accepted struct {
		s   *session.Session
		err error
	}
	ch := make(chan accepted, 1)
	go func() {
		s, err := session.Accept(b, testParams("recv"))
		ch <- accepted{s, err}
	}()
	sa, err := session.Dial(a, testParams("send"))
	if err != nil {
		t.Fatal(err)
	}
	got := <-ch
	if got.err != nil {
		t.Fatal(got.err)
	}

	recvCh := make(chan error, 1)
	go func() {
		_, err := Receive(got.s, outDir, Config{Timeout: 5 * time.Second})
		recvCh <- err
	}()

	// Send MANIFEST and one DATA frame by hand, then cut the connection.
	entry := proto.FileEntry{Name: "cut.bin", Size: 1 << 20}
	manifest := &proto.Manifest{TransferID: [16]byte{4}, ChunkSize: 65536, Files: []proto.FileEntry{entry}}
	if err := sa.SendMsg(proto.TypeManifest, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := sa.Expect(proto.TypeAck, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	chunk := make([]byte, 65536)
	msg := &proto.Data{TransferID: manifest.TransferID, ChunkSHA256: sha256.Sum256(chunk), Data: chunk}
	if err := sa.SendMsg(proto.TypeData, msg); err != nil {
		t.Fatal(err)
	}
	if _, err := sa.Expect(proto.TypeAck, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	_ = sa.Close()
	_ = got.s.Close()

	select {
	case err := <-recvCh:
		if err == nil {
			t.Fatal("receive succeeded after disconnect")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("receive did not fail after disconnect")
	}
	if _, err := os.Stat(filepath.Join(outDir, "cut.bin.ftpart")); err != nil {
		t.Fatalf("partial file should remain for resume: %v", err)
	}
}

func TestDetailProgressSendAndReceive(t *testing.T) {
	srcDir := t.TempDir()
	outDir := t.TempDir()
	f1 := filepath.Join(srcDir, "one.bin")
	f2 := filepath.Join(srcDir, "two.bin")
	writePattern(t, f1, 70*1024)
	writePattern(t, f2, 40*1024)
	wantTotal := int64(110 * 1024)

	sa, sb := sessionPair(t)
	var sendSnaps, recvSnaps []ProgressSnapshot

	recvCh := make(chan error, 1)
	go func() {
		_, err := Receive(sb, outDir, Config{
			ChunkSize: 32 * 1024,
			Timeout:   10 * time.Second,
			OnDetailProgress: func(s ProgressSnapshot) {
				recvSnaps = append(recvSnaps, s)
			},
		})
		recvCh <- err
	}()

	_, err := Send(sa, []string{f1, f2}, Config{
		ChunkSize: 32 * 1024,
		Timeout:   10 * time.Second,
		OnDetailProgress: func(s ProgressSnapshot) {
			sendSnaps = append(sendSnaps, s)
		},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if rerr := <-recvCh; rerr != nil {
		t.Fatalf("Receive: %v", rerr)
	}

	checkSnaps := func(side string, snaps []ProgressSnapshot) {
		t.Helper()
		if len(snaps) < 3 {
			t.Fatalf("%s too few snapshots: %d", side, len(snaps))
		}
		var prev int64 = -1
		sawTransferring := false
		sawVerifying := false
		for _, s := range snaps {
			if s.BytesTotal != wantTotal {
				t.Fatalf("%s BytesTotal = %d, want %d", side, s.BytesTotal, wantTotal)
			}
			if s.BytesDone < prev || s.BytesDone > wantTotal {
				t.Fatalf("%s non-monotonic BytesDone: prev=%d cur=%d", side, prev, s.BytesDone)
			}
			prev = s.BytesDone
			if s.FileCount != 2 || s.FileIndex < 1 || s.FileIndex > 2 {
				t.Fatalf("%s invalid file counts: %+v", side, s)
			}
			if s.Stage == "transferring" {
				sawTransferring = true
			}
			if s.Stage == "verifying" {
				sawVerifying = true
			}
		}
		last := snaps[len(snaps)-1]
		if !sawTransferring || !sawVerifying || last.BytesDone != wantTotal {
			t.Fatalf("%s missing stage or incomplete final: trans=%v verify=%v last=%+v", side, sawTransferring, sawVerifying, last)
		}
	}
	checkSnaps("send", sendSnaps)
	checkSnaps("recv", recvSnaps)
}

type throttledConn struct {
	net.Conn
	bytesPerSec int64
}

func (c *throttledConn) Read(p []byte) (int, error) {
	if len(p) > 32*1024 {
		p = p[:32*1024]
	}
	n, err := c.Conn.Read(p)
	if n > 0 && c.bytesPerSec > 0 {
		wait := time.Duration(float64(n) / float64(c.bytesPerSec) * float64(time.Second))
		time.Sleep(wait)
	}
	return n, err
}

func TestSlowPathWatchdogTriggersAndLeavesResumablePart(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "big.bin")
	const totalSize = 3 << 20
	writePattern(t, src, totalSize)
	outDir := filepath.Join(dir, "out")

	a, b := net.Pipe()
	slowB := &throttledConn{Conn: b, bytesPerSec: 120_000} // ~120 KB/s (< 625 KB/s)

	recvCh := make(chan error, 1)
	sbCh := make(chan *session.Session, 1)
	go func() {
		sb, err := session.Accept(slowB, testParams("recv"))
		if err != nil {
			recvCh <- err
			return
		}
		sbCh <- sb
		_, rerr := Receive(sb, outDir, Config{Timeout: 5 * time.Second})
		recvCh <- rerr
	}()

	sa, err := session.Dial(a, testParams("send"))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer sa.Close()

	_, sendErr := Send(sa, []string{src}, Config{
		ChunkSize:        64 << 10,
		WindowBytes:      256 << 10,
		Timeout:          5 * time.Second,
		MinBytesPerSec:   625_000,
		SlowGrace:        150 * time.Millisecond,
		SlowWindow:       400 * time.Millisecond,
		SlowMinRemaining: 1 << 20,
	})
	_ = sa.Close()
	if sb, ok := <-sbCh; ok {
		_ = sb.Close()
	}
	<-recvCh

	if !errors.Is(sendErr, ErrSlowPath) {
		t.Fatalf("expected ErrSlowPath, got %v", sendErr)
	}
	st, err := os.Stat(filepath.Join(outDir, "big.bin.ftpart"))
	if err != nil || st.Size() == 0 {
		t.Fatalf("expected non-empty .ftpart for resume, got st=%v err=%v", st, err)
	}
}

func TestSlowPathWatchdogSkipsSmallRemaining(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "small.bin")
	writePattern(t, src, 512<<10) // 512 KiB < SlowMinRemaining (2 MiB)
	outDir := filepath.Join(dir, "out")

	a, b := net.Pipe()
	slowB := &throttledConn{Conn: b, bytesPerSec: 250_000}

	type recvRes struct {
		res *Result
		err error
	}
	recvCh := make(chan recvRes, 1)
	go func() {
		sb, err := session.Accept(slowB, testParams("recv"))
		if err != nil {
			recvCh <- recvRes{nil, err}
			return
		}
		defer sb.Close()
		res, rerr := Receive(sb, outDir, Config{Timeout: 10 * time.Second})
		recvCh <- recvRes{res, rerr}
	}()

	sa, err := session.Dial(a, testParams("send"))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer sa.Close()

	_, err = Send(sa, []string{src}, Config{
		ChunkSize:        64 << 10,
		WindowBytes:      128 << 10,
		Timeout:          10 * time.Second,
		MinBytesPerSec:   625_000,
		SlowGrace:        100 * time.Millisecond,
		SlowWindow:       250 * time.Millisecond,
		SlowMinRemaining: 2 << 20,
	})
	if err != nil {
		t.Fatalf("small transfer should not abort on slow path: %v", err)
	}
	rr := <-recvCh
	if rr.err != nil {
		t.Fatalf("receive: %v", rr.err)
	}
}

func TestPathUpgradeAbortsWithCodePathSwitchAndResumes(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "upgrade.bin")
	const totalSize = 3 << 20
	writePattern(t, src, totalSize)
	outDir := filepath.Join(dir, "out")

	a, b := net.Pipe()
	pacedB := &throttledConn{Conn: b, bytesPerSec: 1_200_000} // ~1.2 MB/s so upgrade hits mid-transfer

	recvCh := make(chan error, 1)
	go func() {
		sb, err := session.Accept(pacedB, testParams("recv"))
		if err != nil {
			recvCh <- err
			return
		}
		defer sb.Close()
		_, rerr := Receive(sb, outDir, Config{Timeout: 5 * time.Second})
		recvCh <- rerr
	}()

	sa, err := session.Dial(a, testParams("send"))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer sa.Close()

	upgradeCh := make(chan struct{})
	tid := [16]byte{9, 9, 9}
	go func() {
		time.Sleep(250 * time.Millisecond)
		close(upgradeCh)
	}()

	_, sendErr := Send(sa, []string{src}, Config{
		ChunkSize:           64 << 10,
		WindowBytes:         256 << 10,
		Timeout:             5 * time.Second,
		TransferID:          tid,
		UpgradeCh:           upgradeCh,
		UpgradeMinRemaining: 1 << 20,
	})
	_ = sa.Close()

	recvErr := <-recvCh
	if !errors.Is(sendErr, ErrPathUpgrade) {
		t.Fatalf("expected ErrPathUpgrade, got %v", sendErr)
	}
	var remote *session.RemoteError
	if !errors.As(recvErr, &remote) || remote.Code != proto.CodePathSwitch {
		t.Fatalf("expected RemoteError CodePathSwitch on receiver, got %v", recvErr)
	}

	st, err := os.Stat(filepath.Join(outDir, "upgrade.bin.ftpart"))
	if err != nil || st.Size() == 0 || st.Size() >= totalSize {
		t.Fatalf("expected partial .ftpart on disk, got st=%v err=%v", st, err)
	}

	// Resume on a second session and verify final SHA-256 matches.
	_, recvRes := roundTrip(t, []string{src}, outDir, 0, Config{TransferID: tid})
	if recvRes.Bytes != totalSize-st.Size() {
		t.Fatalf("resumed bytes = %d, want %d", recvRes.Bytes, totalSize-st.Size())
	}
	if got, want := fileHash(t, filepath.Join(outDir, "upgrade.bin")), fileHash(t, src); got != want {
		t.Fatalf("hash mismatch after upgrade resume")
	}
}

func TestPathUpgradeIgnoredWhenRemainingSmall(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "small_upg.bin")
	writePattern(t, src, 512<<10) // < UpgradeMinRemaining (2 MiB)
	outDir := filepath.Join(dir, "out")

	upgradeCh := make(chan struct{})
	close(upgradeCh) // already signaled before transfer

	_, recvRes := roundTrip(t, []string{src}, outDir, 0, Config{
		UpgradeCh:           upgradeCh,
		UpgradeMinRemaining: 2 << 20,
	})
	if recvRes.Bytes != 512<<10 {
		t.Fatalf("bytes = %d, want %d", recvRes.Bytes, 512<<10)
	}
}

func TestConfirmRejectLeavesOutDirClean(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "secret.txt")
	writePattern(t, src, 4096)
	outDir := filepath.Join(dir, "nonexistent-out")

	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	recvErrCh := make(chan error, 1)
	go func() {
		sb, err := session.Accept(b, testParams("recv"))
		if err != nil {
			recvErrCh <- err
			return
		}
		defer sb.Close()
		_, rerr := Receive(sb, outDir, Config{
			Timeout: 5 * time.Second,
			Confirm: func(m *proto.Manifest) error {
				if len(m.Files) != 1 || m.Files[0].Name != "secret.txt" {
					return fmt.Errorf("unexpected manifest in Confirm: %+v", m)
				}
				return proto.NewError(proto.CodeUnauthorized, "rejected by user")
			},
		})
		recvErrCh <- rerr
	}()

	sa, err := session.Dial(a, testParams("send"))
	if err != nil {
		t.Fatal(err)
	}
	defer sa.Close()

	_, sendErr := Send(sa, []string{src}, Config{Timeout: 5 * time.Second})
	if sendErr == nil {
		t.Fatal("expected Send error when receiver rejects")
	}
	var remote *session.RemoteError
	if !errors.As(sendErr, &remote) || remote.Code != proto.CodeUnauthorized {
		t.Fatalf("expected RemoteError CodeUnauthorized, got %v", sendErr)
	}
	if recvErr := <-recvErrCh; recvErr == nil {
		t.Fatal("expected Receive error")
	}
	if _, serr := os.Stat(outDir); !os.IsNotExist(serr) {
		t.Fatalf("outDir should not be created when Confirm rejects, stat=%v", serr)
	}
}

func TestConfirmAcceptCompletesTransfer(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "ok.bin")
	writePattern(t, src, 8192)
	outDir := filepath.Join(dir, "out")

	called := false
	_, recvRes := roundTrip(t, []string{src}, outDir, 0, Config{
		Confirm: func(m *proto.Manifest) error {
			called = true
			return nil
		},
	})
	if !called {
		t.Fatal("Confirm was not called")
	}
	if recvRes.Bytes != 8192 {
		t.Fatalf("bytes = %d, want 8192", recvRes.Bytes)
	}
}

func TestSendPauseResumeAndCancel(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "pause-send.bin")
	writePattern(t, src, 2<<20)

	// 1. Pause longer than Timeout (600ms), then resume and complete cleanly.
	outOK := filepath.Join(dir, "out-ok")
	ctrl := NewController()
	a, b := net.Pipe()
	type resErr struct {
		res *Result
		err error
	}
	recvCh := make(chan resErr, 1)
	go func() {
		defer b.Close()
		sb, err := session.Accept(b, testParams("recv"))
		if err != nil {
			recvCh <- resErr{nil, err}
			return
		}
		defer sb.Close()
		res, rerr := Receive(sb, outOK, Config{Timeout: 600 * time.Millisecond})
		recvCh <- resErr{res, rerr}
	}()

	sa, err := session.Dial(a, testParams("send"))
	if err != nil {
		t.Fatal(err)
	}
	pausedOnce := false
	_, err = Send(sa, []string{src}, Config{
		ChunkSize:      64 << 10,
		WindowBytes:    128 << 10,
		Timeout:        600 * time.Millisecond,
		MinBytesPerSec: 1 << 20, // must not false-trigger during pause
		SlowGrace:      50 * time.Millisecond,
		SlowWindow:     150 * time.Millisecond,
		Control:        ctrl,
		OnDetailProgress: func(s ProgressSnapshot) {
			if !pausedOnce && s.BytesDone >= 256<<10 {
				pausedOnce = true
				ctrl.Pause()
				time.AfterFunc(950*time.Millisecond, ctrl.Resume)
			}
		},
	})
	_ = sa.Close()
	if err != nil {
		t.Fatalf("Send after pause/resume failed: %v", err)
	}
	rr := <-recvCh
	if rr.err != nil {
		t.Fatalf("Receive after pause/resume failed: %v", rr.err)
	}
	if fileHash(t, src) != fileHash(t, filepath.Join(outOK, "pause-send.bin")) {
		t.Fatal("hash mismatch after send pause/resume")
	}

	// 2. Sender cancel returns ErrCancelled and cleans receiver .ftpart.
	outCancel := filepath.Join(dir, "out-cancel")
	ctrl2 := NewController()
	c1, c2 := net.Pipe()
	recvErr2 := make(chan error, 1)
	go func() {
		defer c2.Close()
		sb, err := session.Accept(c2, testParams("recv"))
		if err != nil {
			recvErr2 <- err
			return
		}
		defer sb.Close()
		_, rerr := Receive(sb, outCancel, Config{Timeout: 2 * time.Second})
		recvErr2 <- rerr
	}()
	sa2, err := session.Dial(c1, testParams("send"))
	if err != nil {
		t.Fatal(err)
	}
	cancelledOnce := false
	_, sendErr2 := Send(sa2, []string{src}, Config{
		ChunkSize:   64 << 10,
		WindowBytes: 128 << 10,
		Timeout:     2 * time.Second,
		Control:     ctrl2,
		OnDetailProgress: func(s ProgressSnapshot) {
			if !cancelledOnce && s.BytesDone >= 128<<10 {
				cancelledOnce = true
				ctrl2.Cancel()
			}
		},
	})
	_ = sa2.Close()
	if !errors.Is(sendErr2, ErrCancelled) {
		t.Fatalf("expected ErrCancelled, got %v", sendErr2)
	}
	if rerr2 := <-recvErr2; rerr2 == nil {
		t.Fatal("expected Receive error on sender cancel")
	}
	if _, statErr := os.Stat(filepath.Join(outCancel, "pause-send.bin.ftpart")); !os.IsNotExist(statErr) {
		t.Fatalf("expected .ftpart removed on cancel, stat=%v", statErr)
	}
}

func TestReceivePauseResumeAndCancel(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "pause-recv.bin")
	writePattern(t, src, 2<<20)

	// 1. Receiver pauses across > Timeout, then resumes and completes cleanly.
	outOK := filepath.Join(dir, "out-ok")
	recvCtrl := NewController()
	a, b := net.Pipe()
	type resErr struct {
		res *Result
		err error
	}
	recvCh := make(chan resErr, 1)
	go func() {
		defer b.Close()
		sb, err := session.Accept(b, testParams("recv"))
		if err != nil {
			recvCh <- resErr{nil, err}
			return
		}
		defer sb.Close()
		pausedOnce := false
		res, rerr := Receive(sb, outOK, Config{
			Timeout: 600 * time.Millisecond,
			Control: recvCtrl,
			OnDetailProgress: func(s ProgressSnapshot) {
				if !pausedOnce && s.BytesDone >= 256<<10 {
					pausedOnce = true
					recvCtrl.Pause()
					time.AfterFunc(950*time.Millisecond, recvCtrl.Resume)
				}
			},
		})
		recvCh <- resErr{res, rerr}
	}()

	sa, err := session.Dial(a, testParams("send"))
	if err != nil {
		t.Fatal(err)
	}
	_, sendErr := Send(sa, []string{src}, Config{
		ChunkSize:      64 << 10,
		WindowBytes:    128 << 10,
		Timeout:        600 * time.Millisecond,
		MinBytesPerSec: 1 << 20, // must tolerate receiver pause via keepalive ping
		SlowGrace:      50 * time.Millisecond,
		SlowWindow:     150 * time.Millisecond,
	})
	_ = sa.Close()
	if sendErr != nil {
		t.Fatalf("Send failed during receiver pause/resume: %v", sendErr)
	}
	rr := <-recvCh
	if rr.err != nil {
		t.Fatalf("Receive failed after pause/resume: %v", rr.err)
	}
	if fileHash(t, src) != fileHash(t, filepath.Join(outOK, "pause-recv.bin")) {
		t.Fatal("hash mismatch after receiver pause/resume")
	}

	// 2. Receiver cancel returns ErrCancelled, notifies sender, and cleans .ftpart.
	outCancel := filepath.Join(dir, "out-cancel")
	recvCtrl2 := NewController()
	c1, c2 := net.Pipe()
	recvCh2 := make(chan error, 1)
	go func() {
		defer c2.Close()
		sb, err := session.Accept(c2, testParams("recv"))
		if err != nil {
			recvCh2 <- err
			return
		}
		defer sb.Close()
		cancelledOnce := false
		_, rerr := Receive(sb, outCancel, Config{
			Timeout: 2 * time.Second,
			Control: recvCtrl2,
			OnDetailProgress: func(s ProgressSnapshot) {
				if !cancelledOnce && s.BytesDone >= 128<<10 {
					cancelledOnce = true
					recvCtrl2.Cancel()
				}
			},
		})
		recvCh2 <- rerr
	}()

	sa2, err := session.Dial(c1, testParams("send"))
	if err != nil {
		t.Fatal(err)
	}
	_, sendErr2 := Send(sa2, []string{src}, Config{
		ChunkSize:   64 << 10,
		WindowBytes: 128 << 10,
		Timeout:     2 * time.Second,
	})
	_ = sa2.Close()
	if rerr2 := <-recvCh2; !errors.Is(rerr2, ErrCancelled) {
		t.Fatalf("expected receiver ErrCancelled, got %v", rerr2)
	}
	var rem *session.RemoteError
	if !errors.As(sendErr2, &rem) || rem.Code != proto.CodeCancelled {
		t.Fatalf("expected sender RemoteError(CodeCancelled), got %v", sendErr2)
	}
	if _, statErr := os.Stat(filepath.Join(outCancel, "pause-recv.bin.ftpart")); !os.IsNotExist(statErr) {
		t.Fatalf("expected .ftpart removed on receiver cancel, stat=%v", statErr)
	}
}

func TestPauseSyncAndOwnerOnlyResume(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "sync.bin")
	writePattern(t, src, 2<<20)

	// 1. Sender pauses -> receiver sees peer_paused and cannot resume -> sender resumes & completes.
	out1 := filepath.Join(dir, "out1")
	sendCtrl1 := NewController()
	recvCtrl1 := NewController()
	a, b := net.Pipe()
	type resErr struct {
		err error
	}
	recvCh1 := make(chan resErr, 1)
	recvPeerPausedSeen := make(chan struct{}, 1)
	go func() {
		defer b.Close()
		sb, err := session.Accept(b, testParams("recv"))
		if err != nil {
			recvCh1 <- resErr{err}
			return
		}
		defer sb.Close()
		_, rerr := Receive(sb, out1, Config{
			Timeout: 2 * time.Second,
			Control: recvCtrl1,
			OnDetailProgress: func(s ProgressSnapshot) {
				if s.Stage == "peer_paused" {
					select {
					case recvPeerPausedSeen <- struct{}{}:
					default:
					}
				}
			},
		})
		recvCh1 <- resErr{rerr}
	}()

	sa, err := session.Dial(a, testParams("send"))
	if err != nil {
		t.Fatal(err)
	}
	pausedOnce := false
	sendDone1 := make(chan error, 1)
	go func() {
		defer sa.Close()
		_, serr := Send(sa, []string{src}, Config{
			ChunkSize:   64 << 10,
			WindowBytes: 128 << 10,
			Timeout:     2 * time.Second,
			Control:     sendCtrl1,
			OnDetailProgress: func(s ProgressSnapshot) {
				if pausedOnce && s.Stage == "transferring" && sendCtrl1.IsPaused() {
					t.Errorf("emitted transferring while locally paused: %+v", s)
				}
				if !pausedOnce && s.BytesDone >= 256<<10 {
					pausedOnce = true
					sendCtrl1.Pause()
				}
			},
		})
		sendDone1 <- serr
	}()

	select {
	case <-recvPeerPausedSeen:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for receiver to observe peer_paused")
	}
	if !recvCtrl1.IsPeerPaused() {
		t.Fatal("expected receiver controller IsPeerPaused() = true")
	}
	// Owner-only resume: receiver must not be able to resume a sender-initiated pause.
	if recvCtrl1.TogglePause() {
		t.Fatal("receiver TogglePause should return false while paused by peer")
	}
	if !recvCtrl1.IsPeerPaused() {
		t.Fatal("receiver pause state changed despite owner-only rule")
	}
	sendCtrl1.Resume()

	if serr := <-sendDone1; serr != nil {
		t.Fatalf("Send failed after synced pause/resume: %v", serr)
	}
	if rr := <-recvCh1; rr.err != nil {
		t.Fatalf("Receive failed after synced pause/resume: %v", rr.err)
	}
	if fileHash(t, src) != fileHash(t, filepath.Join(out1, "sync.bin")) {
		t.Fatal("hash mismatch after sender pause sync")
	}

	// 2. Receiver pauses -> sender sees peer_paused and cannot resume -> receiver resumes & completes.
	out2 := filepath.Join(dir, "out2")
	sendCtrl2 := NewController()
	recvCtrl2 := NewController()
	c1, c2 := net.Pipe()
	recvCh2 := make(chan resErr, 1)
	go func() {
		defer c2.Close()
		sb, err := session.Accept(c2, testParams("recv"))
		if err != nil {
			recvCh2 <- resErr{err}
			return
		}
		defer sb.Close()
		pausedRecv := false
		_, rerr := Receive(sb, out2, Config{
			Timeout: 2 * time.Second,
			Control: recvCtrl2,
			OnDetailProgress: func(s ProgressSnapshot) {
				if !pausedRecv && s.BytesDone >= 256<<10 {
					pausedRecv = true
					recvCtrl2.Pause()
				}
			},
		})
		recvCh2 <- resErr{rerr}
	}()

	sa2, err := session.Dial(c1, testParams("send"))
	if err != nil {
		t.Fatal(err)
	}
	sendPeerPausedSeen := make(chan struct{}, 1)
	sendDone2 := make(chan error, 1)
	go func() {
		defer sa2.Close()
		_, serr := Send(sa2, []string{src}, Config{
			ChunkSize:   64 << 10,
			WindowBytes: 128 << 10,
			Timeout:     2 * time.Second,
			Control:     sendCtrl2,
			OnDetailProgress: func(s ProgressSnapshot) {
				if s.Stage == "peer_paused" {
					select {
					case sendPeerPausedSeen <- struct{}{}:
					default:
					}
				}
			},
		})
		sendDone2 <- serr
	}()

	select {
	case <-sendPeerPausedSeen:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for sender to observe peer_paused")
	}
	if !sendCtrl2.IsPeerPaused() {
		t.Fatal("expected sender controller IsPeerPaused() = true")
	}
	// Owner-only resume: sender must not be able to resume a receiver-initiated pause.
	if sendCtrl2.TogglePause() {
		t.Fatal("sender TogglePause should return false while paused by peer")
	}
	recvCtrl2.Resume()

	if serr := <-sendDone2; serr != nil {
		t.Fatalf("Send failed after receiver pause sync: %v", serr)
	}
	if rr := <-recvCh2; rr.err != nil {
		t.Fatalf("Receive failed after receiver pause sync: %v", rr.err)
	}
	if fileHash(t, src) != fileHash(t, filepath.Join(out2, "sync.bin")) {
		t.Fatal("hash mismatch after receiver pause sync")
	}
}

func TestIsCancelledAndGracefulCancelDelivery(t *testing.T) {
	if !IsCancelled(ErrCancelled) {
		t.Fatal("IsCancelled(ErrCancelled) = false")
	}
	if !IsCancelled(&session.RemoteError{Code: proto.CodeCancelled, Msg: "peer cancelled"}) {
		t.Fatal("IsCancelled(RemoteError{CodeCancelled}) = false")
	}
	if IsCancelled(errors.New("other error")) {
		t.Fatal("IsCancelled(other error) = true")
	}

	// Simulate an asynchronous buffered transport (like WebSocket relay) where
	// closing immediately on cancel could otherwise race ahead of CodeCancelled.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	dir := t.TempDir()
	src := filepath.Join(dir, "cancel-grace.bin")
	writePattern(t, src, 2<<20)
	outDir := filepath.Join(dir, "out")

	recvCtrl := NewController()
	recvErrCh := make(chan error, 1)
	go func() {
		conn, aerr := ln.Accept()
		if aerr != nil {
			recvErrCh <- aerr
			return
		}
		defer conn.Close()
		sb, serr := session.Accept(conn, testParams("recv"))
		if serr != nil {
			recvErrCh <- serr
			return
		}
		defer sb.Close()
		once := false
		_, rerr := Receive(sb, outDir, Config{
			Timeout: 2 * time.Second,
			Control: recvCtrl,
			OnDetailProgress: func(s ProgressSnapshot) {
				if !once && s.BytesDone >= 128<<10 {
					once = true
					recvCtrl.Cancel()
				}
			},
		})
		recvErrCh <- rerr
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	sa, err := session.Dial(conn, testParams("send"))
	if err != nil {
		t.Fatal(err)
	}
	defer sa.Close()

	_, sendErr := Send(sa, []string{src}, Config{
		ChunkSize:   64 << 10,
		WindowBytes: 128 << 10,
		Timeout:     2 * time.Second,
	})
	if rerr := <-recvErrCh; !errors.Is(rerr, ErrCancelled) {
		t.Fatalf("expected receiver ErrCancelled, got %v", rerr)
	}
	if !IsCancelled(sendErr) {
		t.Fatalf("expected sender to receive CodeCancelled (IsCancelled=true), got %v", sendErr)
	}
}

func TestFolderRelativePathsAndCancelCleanup(t *testing.T) {
	srcRoot := t.TempDir()
	sendDir := filepath.Join(srcRoot, "album")
	if err := os.MkdirAll(filepath.Join(sendDir, "nested", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sendDir, "empty_sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	f1 := filepath.Join(sendDir, "cover.bin")
	f2 := filepath.Join(sendDir, "nested", "deep", "shot.bin")
	writePattern(t, f1, 64<<10)
	writePattern(t, f2, 192<<10)

	outDir := filepath.Join(t.TempDir(), "recv")
	a, b := net.Pipe()
	type recvOut struct {
		res *Result
		err error
	}
	recvCh := make(chan recvOut, 1)
	go func() {
		defer b.Close()
		sb, err := session.Accept(b, testParams("recv"))
		if err != nil {
			recvCh <- recvOut{err: err}
			return
		}
		defer sb.Close()
		r, err := Receive(sb, outDir, Config{Timeout: 5 * time.Second})
		recvCh <- recvOut{res: r, err: err}
	}()

	sa, err := session.Dial(a, testParams("send"))
	if err != nil {
		t.Fatal(err)
	}
	sres, err := Send(sa, []string{sendDir}, Config{ChunkSize: 32 << 10, Timeout: 5 * time.Second})
	_ = sa.Close()
	if err != nil {
		t.Fatalf("Send folder failed: %v", err)
	}
	rr := <-recvCh
	if rr.err != nil {
		t.Fatalf("Receive folder failed: %v", rr.err)
	}
	if len(sres.Files) != 2 || len(rr.res.Files) != 2 {
		t.Fatalf("unexpected file counts send=%v recv=%v", sres.Files, rr.res.Files)
	}
	got1 := filepath.Join(outDir, "album", "cover.bin")
	got2 := filepath.Join(outDir, "album", "nested", "deep", "shot.bin")
	if fileHash(t, f1) != fileHash(t, got1) || fileHash(t, f2) != fileHash(t, got2) {
		t.Fatal("received folder file hash mismatch")
	}
	if _, err := os.Stat(filepath.Join(outDir, "album", "empty_sub")); !os.IsNotExist(err) {
		t.Fatalf("expected empty subdirectory to be skipped, stat=%v", err)
	}

	// Cancel mid-folder transfer must remove .ftpart and empty created directories.
	bigFolder := filepath.Join(srcRoot, "bigset")
	if err := os.MkdirAll(filepath.Join(bigFolder, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	bigFile := filepath.Join(bigFolder, "sub", "large.bin")
	writePattern(t, bigFile, 2<<20)

	outCancel := filepath.Join(t.TempDir(), "cancel-recv")
	c1, c2 := net.Pipe()
	recvErr2 := make(chan error, 1)
	go func() {
		defer c2.Close()
		sb, err := session.Accept(c2, testParams("recv"))
		if err != nil {
			recvErr2 <- err
			return
		}
		defer sb.Close()
		_, rerr := Receive(sb, outCancel, Config{Timeout: 2 * time.Second})
		recvErr2 <- rerr
	}()
	sa2, err := session.Dial(c1, testParams("send"))
	if err != nil {
		t.Fatal(err)
	}
	ctrl := NewController()
	cancelledOnce := false
	_, sendErr2 := Send(sa2, []string{bigFolder}, Config{
		ChunkSize:   64 << 10,
		WindowBytes: 128 << 10,
		Timeout:     2 * time.Second,
		Control:     ctrl,
		OnDetailProgress: func(s ProgressSnapshot) {
			if !cancelledOnce && s.BytesDone >= 128<<10 {
				cancelledOnce = true
				ctrl.Cancel()
			}
		},
	})
	_ = sa2.Close()
	if !errors.Is(sendErr2, ErrCancelled) {
		t.Fatalf("expected ErrCancelled, got %v", sendErr2)
	}
	if rerr2 := <-recvErr2; rerr2 == nil {
		t.Fatal("expected Receive error on cancel")
	}
	if _, err := os.Stat(filepath.Join(outCancel, "bigset")); !os.IsNotExist(err) {
		t.Fatalf("expected cancelled folder leftovers pruned, stat=%v", err)
	}
}
