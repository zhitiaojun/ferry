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
