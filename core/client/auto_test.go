package client

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math/rand/v2"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/pathmgr"
	"github.com/cwlindean/filetransfer/core/proto"
	"github.com/cwlindean/filetransfer/core/punch"
	"github.com/cwlindean/filetransfer/core/signaling"
	"github.com/cwlindean/filetransfer/core/transfer"
	"github.com/cwlindean/filetransfer/server"
)

func setupPairedKeys(t *testing.T, dir string) (sendKeyPath, recvKeyPath, sendPairedPath, recvPairedPath string, sendKey, recvKey *identity.Key) {
	t.Helper()
	sendKeyPath = filepath.Join(dir, "send.key")
	recvKeyPath = filepath.Join(dir, "recv.key")
	sendPairedPath = filepath.Join(dir, "send-paired.json")
	recvPairedPath = filepath.Join(dir, "recv-paired.json")

	var err error
	sendKey, err = identity.LoadOrCreateKey(sendKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	recvKey, err = identity.LoadOrCreateKey(recvKeyPath)
	if err != nil {
		t.Fatal(err)
	}

	sp, err := identity.LoadPaired(sendPairedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := sp.Add(identity.PairedDevice{
		DeviceID: recvKey.IDHex(), Name: "recv-pc", PubKey: recvKey.PubHex(), PairedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := sp.Save(); err != nil {
		t.Fatal(err)
	}

	rp, err := identity.LoadPaired(recvPairedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := rp.Add(identity.PairedDevice{
		DeviceID: sendKey.IDHex(), Name: "send-pc", PubKey: sendKey.PubHex(), PairedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := rp.Save(); err != nil {
		t.Fatal(err)
	}
	return sendKeyPath, recvKeyPath, sendPairedPath, recvPairedPath, sendKey, recvKey
}

func writeTestPayload(t *testing.T, path string, size int) string {
	t.Helper()
	b := make([]byte, size)
	for i := range b {
		b[i] = byte((i * 31) % 251)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func fileSHA256(t *testing.T, path string) string {
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

func TestSendAndReceiveAutoPunchWinsOverRelay(t *testing.T) {
	srv := server.New(nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	sigURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/signal"

	dir := t.TempDir()
	sendKeyPath, _, sendPairedPath, recvPairedPath, _, recvKey := setupPairedKeys(t, dir)
	outDir := filepath.Join(dir, "out")
	src := filepath.Join(dir, "sample.bin")
	wantSum := writeTestPayload(t, src, 512<<10)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	recvCl, err := signaling.Dial(ctx, sigURL, recvKey, nil)
	if err != nil {
		t.Fatalf("recv signaling: %v", err)
	}
	defer recvCl.Close()

	reqCh := make(chan signaling.SendRequest, 1)
	candCh := make(chan *signaling.Envelope, 8)
	readyCh := make(chan signaling.RelayReady, 2)
	recvCl.On(signaling.TypeSendRequest, func(e *signaling.Envelope) {
		if r, rerr := signaling.Decode[signaling.SendRequest](e); rerr == nil {
			reqCh <- r
		}
	})
	recvCl.On(signaling.TypeCandidates, func(e *signaling.Envelope) {
		candCh <- e
	})
	recvCl.On(signaling.TypeRelayReady, func(e *signaling.Envelope) {
		if rr, rerr := signaling.Decode[signaling.RelayReady](e); rerr == nil {
			readyCh <- rr
		}
	})

	type recvOut struct {
		res Result
		err error
	}
	recvDone := make(chan recvOut, 1)
	go func() {
		req := <-reqCh
		peerPub, perr := PairedPeer(recvPairedPath, req.FromDevice)
		if perr != nil {
			recvDone <- recvOut{err: perr}
			return
		}
		res, rerr := ReceiveAuto(ctx, ReceiveOptions{
			Key:         recvKey,
			SignalURL:   sigURL,
			OutDir:      outDir,
			PairedPath:  recvPairedPath,
			Name:        "recv-pc",
			STUNServers: []string{},
		}, recvCl, req, peerPub, candCh, readyCh)
		recvDone <- recvOut{res: res, err: rerr}
	}()

	sendRes, err := SendAuto(ctx, Options{
		SignalURL:   sigURL,
		KeyPath:     sendKeyPath,
		PairedPath:  sendPairedPath,
		Name:        "send-pc",
		STUNServers: []string{},
	}, recvKey.IDHex(), []string{src})
	if err != nil {
		t.Fatalf("SendAuto: %v", err)
	}
	rr := <-recvDone
	if rr.err != nil {
		t.Fatalf("ReceiveAuto: %v", rr.err)
	}
	if sendRes.Channel != "打洞" || rr.res.Channel != "打洞" {
		t.Fatalf("expected punch channel on both ends, got send=%q recv=%q", sendRes.Channel, rr.res.Channel)
	}
	if got := fileSHA256(t, filepath.Join(outDir, "sample.bin")); got != wantSum {
		t.Fatalf("sha256 mismatch: got %s want %s", got, wantSum)
	}
}

func TestSendAutoImmediateRelayWhenPeerHasNoCandidates(t *testing.T) {
	srv := server.New(nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	sigURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/signal"

	dir := t.TempDir()
	sendKeyPath, _, sendPairedPath, recvPairedPath, _, recvKey := setupPairedKeys(t, dir)
	outDir := filepath.Join(dir, "out")
	src := filepath.Join(dir, "relay.bin")
	wantSum := writeTestPayload(t, src, 256<<10)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	recvCl, err := signaling.Dial(ctx, sigURL, recvKey, nil)
	if err != nil {
		t.Fatalf("recv signaling: %v", err)
	}
	defer recvCl.Close()

	reqCh := make(chan signaling.SendRequest, 1)
	readyCh := make(chan signaling.RelayReady, 1)
	recvCl.On(signaling.TypeSendRequest, func(e *signaling.Envelope) {
		if r, rerr := signaling.Decode[signaling.SendRequest](e); rerr == nil {
			reqCh <- r
		}
	})
	recvCl.On(signaling.TypeRelayReady, func(e *signaling.Envelope) {
		if rr, rerr := signaling.Decode[signaling.RelayReady](e); rerr == nil {
			readyCh <- rr
		}
	})

	recvErr := make(chan error, 1)
	go func() {
		req := <-reqCh
		peerPub, perr := PairedPeer(recvPairedPath, req.FromDevice)
		if perr != nil {
			recvErr <- perr
			return
		}
		if err := recvCl.Send(ctx, signaling.TypeSendResp, signaling.SendResponse{
			ToDevice: req.FromDevice, TransferID: req.TransferID, Accept: true,
		}); err != nil {
			recvErr <- err
			return
		}
		if err := recvCl.Send(ctx, signaling.TypeCandidates, signaling.Candidates{
			ToDevice: req.FromDevice, SessionID: req.SessionID, Candidates: []signaling.Candidate{},
		}); err != nil {
			recvErr <- err
			return
		}
		rr := <-readyCh
		s, err := acceptRelaySession(ctx, sigURL, rr, recvKey, peerPub, "recv-pc", false)
		if err != nil {
			recvErr <- err
			return
		}
		defer s.Drain(time.Second)
		_, err = transfer.Receive(s, outDir, transfer.Config{TransferID: hex16(req.TransferID)})
		recvErr <- err
	}()

	start := time.Now()
	res, err := SendAuto(ctx, Options{
		SignalURL:   sigURL,
		KeyPath:     sendKeyPath,
		PairedPath:  sendPairedPath,
		Name:        "send-pc",
		STUNServers: []string{},
	}, recvKey.IDHex(), []string{src})
	if err != nil {
		t.Fatalf("SendAuto: %v", err)
	}
	if err := <-recvErr; err != nil {
		t.Fatalf("receiver: %v", err)
	}
	if res.Channel != "中继" {
		t.Fatalf("expected 中继, got %q", res.Channel)
	}
	if time.Since(start) >= PunchHeadStart {
		t.Fatalf("empty candidates should skip PunchHeadStart (%v), took %v", PunchHeadStart, time.Since(start))
	}
	if got := fileSHA256(t, filepath.Join(outDir, "relay.bin")); got != wantSum {
		t.Fatalf("sha256 mismatch: got %s want %s", got, wantSum)
	}
}

func TestSlowPunchFallbackToRelayResumesPartFile(t *testing.T) {
	srv := server.New(nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	sigURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/signal"

	dir := t.TempDir()
	_, _, _, recvPairedPath, sendKey, recvKey := setupPairedKeys(t, dir)
	outDir := filepath.Join(dir, "out")
	src := filepath.Join(dir, "resume.bin")
	const totalBytes = 3 << 20
	wantSum := writeTestPayload(t, src, totalBytes)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	sendCl, err := signaling.Dial(ctx, sigURL, sendKey, nil)
	if err != nil {
		t.Fatalf("send signaling: %v", err)
	}
	defer sendCl.Close()
	recvCl, err := signaling.Dial(ctx, sigURL, recvKey, nil)
	if err != nil {
		t.Fatalf("recv signaling: %v", err)
	}
	defer recvCl.Close()

	reqCh := make(chan signaling.SendRequest, 1)
	candCh := make(chan *signaling.Envelope, 8)
	readyCh := make(chan signaling.RelayReady, 2)
	recvCl.On(signaling.TypeSendRequest, func(e *signaling.Envelope) {
		if r, rerr := signaling.Decode[signaling.SendRequest](e); rerr == nil {
			reqCh <- r
		}
	})
	recvCl.On(signaling.TypeCandidates, func(e *signaling.Envelope) {
		candCh <- e
	})
	recvCl.On(signaling.TypeRelayReady, func(e *signaling.Envelope) {
		if rr, rerr := signaling.Decode[signaling.RelayReady](e); rerr == nil {
			readyCh <- rr
		}
	})

	type recvOut struct {
		res Result
		err error
	}
	recvDone := make(chan recvOut, 1)
	go func() {
		req := <-reqCh
		peerPub, perr := PairedPeer(recvPairedPath, req.FromDevice)
		if perr != nil {
			recvDone <- recvOut{err: perr}
			return
		}
		res, rerr := ReceiveAuto(ctx, ReceiveOptions{
			Key:         recvKey,
			SignalURL:   sigURL,
			OutDir:      outDir,
			PairedPath:  recvPairedPath,
			Name:        "recv-pc",
			STUNServers: []string{},
		}, recvCl, req, peerPub, candCh, readyCh)
		recvDone <- recvOut{res: res, err: rerr}
	}()

	// Establish a real ReceiveAuto punch session first, then intentionally send
	// a partial prefix and simulate ErrSlowPath fallback over relay.
	respCh := make(chan signaling.SendResponse, 1)
	sendCands := make(chan signaling.Candidates, 1)
	sendCl.On(signaling.TypeSendResp, func(e *signaling.Envelope) {
		if r, derr := signaling.Decode[signaling.SendResponse](e); derr == nil {
			respCh <- r
		}
	})
	sendCl.On(signaling.TypeCandidates, func(e *signaling.Envelope) {
		if c, derr := signaling.Decode[signaling.Candidates](e); derr == nil {
			sendCands <- c
		}
	})

	g, err := punch.Gather(ctx, []string{}, 2*time.Second, nil)
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	defer g.Close()

	sid, _ := randomHex16()
	tid, _ := randomHex16()
	infos, _, _ := describeFiles([]string{src})
	if err := sendCl.Send(ctx, signaling.TypeSendRequest, signaling.SendRequest{
		ToDevice: recvKey.IDHex(), SessionID: sid, TransferID: tid, Files: infos,
	}); err != nil {
		t.Fatal(err)
	}
	resp := <-respCh
	if !resp.Accept {
		t.Fatal("receiver declined")
	}
	rcands := <-sendCands
	if err := sendCl.Send(ctx, signaling.TypeCandidates, signaling.Candidates{
		ToDevice: recvKey.IDHex(), SessionID: sid, Candidates: signalingCandidates(g.Candidates),
	}); err != nil {
		t.Fatal(err)
	}

	tlsCfg, err := identity.ClientTLS(sendKey, recvKey.Public())
	if err != nil {
		t.Fatal(err)
	}
	q := pathmgr.NewQUIC(g.Conn, tlsCfg)
	defer q.Close()
	if err := q.PrepareListen(ctx); err != nil {
		t.Fatal(err)
	}
	peerLocal := localCandidates(rcands.Candidates)
	pctx, pcancel := context.WithTimeout(ctx, 10*time.Second)
	go g.Punch(pctx, peerLocal)
	punchSess, err := punch.Connect(pctx, q, punch.Resolve(peerLocal), sendKey.ID(), recvKey.ID(), sessionParams(sendKey, "send-pc"))
	pcancel()
	if err != nil {
		t.Fatalf("punch.Connect: %v", err)
	}

	// Send first 512 KiB over punch, then simulate slow-punch abort.
	rawData, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	const partialBytes = 512 << 10
	tidArr := hex16(tid)
	if err := punchSess.SendMsg(proto.TypeManifest, &proto.Manifest{
		TransferID: tidArr, ChunkSize: 256 << 10,
		Files: []proto.FileEntry{{Name: "resume.bin", Size: totalBytes}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := punchSess.Expect(proto.TypeAck, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	for off := 0; off < partialBytes; off += 256 << 10 {
		chunk := rawData[off : off+(256<<10)]
		if err := punchSess.SendMsg(proto.TypeData, &proto.Data{
			TransferID:  tidArr,
			FileIndex:   0,
			Offset:      uint64(off),
			ChunkSHA256: sha256.Sum256(chunk),
			Data:        chunk,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := punchSess.Expect(proto.TypeAck, 5*time.Second); err != nil {
			t.Fatal(err)
		}
	}
	_ = punchSess.Close()

	relaySess, err := openRelaySession(ctx, sendCl, sendKey, recvKey.Public(), "send-pc", recvKey.IDHex(), tid, false)
	if err != nil {
		t.Fatalf("openRelaySession: %v", err)
	}
	defer relaySess.Close()
	res, err := transfer.Send(relaySess, []string{src}, transfer.Config{TransferID: tidArr})
	if err != nil {
		t.Fatalf("transfer.Send resume: %v", err)
	}
	rr := <-recvDone
	if rr.err != nil {
		t.Fatalf("ReceiveAuto: %v", rr.err)
	}
	if rr.res.Channel != "中继" {
		t.Fatalf("expected fallback channel 中继, got %q", rr.res.Channel)
	}
	if rr.res.Bytes != totalBytes-partialBytes {
		t.Fatalf("expected resumed bytes %d, got %d (send total %d)", totalBytes-partialBytes, rr.res.Bytes, res.Bytes)
	}
	if got := fileSHA256(t, filepath.Join(outDir, "resume.bin")); got != wantSum {
		t.Fatalf("sha256 mismatch: got %s want %s", got, wantSum)
	}
}

func TestRelayFirstHotUpgradeToPunchResumesPartFile(t *testing.T) {
	lo := 38000 + rand.IntN(18000)
	hi := lo + 800

	prevRange := testBirthdayPortRange
	prevDisable := testDisableFastPunch
	testBirthdayPortRange = [2]int{lo, hi}
	testDisableFastPunch = true
	t.Cleanup(func() {
		testBirthdayPortRange = prevRange
		testDisableFastPunch = prevDisable
	})

	lim := server.DefaultLimits()
	lim.RelayRate = 1 << 20 // 1 MiB/s paced relay so hot upgrade lands mid-transfer
	srv := server.NewWithLimits(lim, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	sigURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/signal"

	dir := t.TempDir()
	sendKeyPath, _, sendPairedPath, recvPairedPath, _, recvKey := setupPairedKeys(t, dir)
	outDir := filepath.Join(dir, "out")
	src := filepath.Join(dir, "hot_upgrade.bin")
	wantSum := writeTestPayload(t, src, 6<<20)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	recvCl, err := signaling.Dial(ctx, sigURL, recvKey, nil)
	if err != nil {
		t.Fatalf("recv signaling: %v", err)
	}
	defer recvCl.Close()

	reqCh := make(chan signaling.SendRequest, 1)
	candCh := make(chan *signaling.Envelope, 8)
	readyCh := make(chan signaling.RelayReady, 2)
	recvCl.On(signaling.TypeSendRequest, func(e *signaling.Envelope) {
		if r, rerr := signaling.Decode[signaling.SendRequest](e); rerr == nil {
			reqCh <- r
		}
	})
	recvCl.On(signaling.TypeCandidates, func(e *signaling.Envelope) {
		candCh <- e
	})
	recvCl.On(signaling.TypeRelayReady, func(e *signaling.Envelope) {
		if rr, rerr := signaling.Decode[signaling.RelayReady](e); rerr == nil {
			readyCh <- rr
		}
	})

	type recvOut struct {
		res Result
		err error
	}
	recvDone := make(chan recvOut, 1)
	go func() {
		req := <-reqCh
		peerPub, perr := PairedPeer(recvPairedPath, req.FromDevice)
		if perr != nil {
			recvDone <- recvOut{err: perr}
			return
		}
		res, rerr := ReceiveAuto(ctx, ReceiveOptions{
			Key:                recvKey,
			SignalURL:          sigURL,
			OutDir:             outDir,
			PairedPath:         recvPairedPath,
			Name:               "recv-pc",
			STUNServers:        []string{},
			HotUpgradeMinBytes: 2 << 20,
		}, recvCl, req, peerPub, candCh, readyCh)
		recvDone <- recvOut{res: res, err: rerr}
	}()

	sendRes, err := SendAuto(ctx, Options{
		SignalURL:          sigURL,
		KeyPath:            sendKeyPath,
		PairedPath:         sendPairedPath,
		Name:               "send-pc",
		STUNServers:        []string{},
		HotUpgradeMinBytes: 2 << 20,
	}, recvKey.IDHex(), []string{src})
	if err != nil {
		t.Fatalf("SendAuto: %v", err)
	}
	rr := <-recvDone
	if rr.err != nil {
		t.Fatalf("ReceiveAuto: %v", rr.err)
	}
	if sendRes.Channel != "打洞" || rr.res.Channel != "打洞" {
		t.Fatalf("expected hot upgrade to punch (打洞), got send=%q recv=%q", sendRes.Channel, rr.res.Channel)
	}
	if got := fileSHA256(t, filepath.Join(outDir, "hot_upgrade.bin")); got != wantSum {
		t.Fatalf("sha256 mismatch: got %s want %s", got, wantSum)
	}
}

func TestRelayCancelDeliversCodeCancelled(t *testing.T) {
	lim := server.DefaultLimits()
	lim.RelayRate = 2 << 20 // 2 MiB/s paced relay so in-flight buffers queue up realistically
	srv := server.NewWithLimits(lim, nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	sigURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/signal"

	dir := t.TempDir()
	sendKeyPath, _, sendPairedPath, recvPairedPath, _, recvKey := setupPairedKeys(t, dir)
	src := filepath.Join(dir, "cancel_payload.bin")
	writeTestPayload(t, src, 8<<20)

	runOne := func(name string, cancelSender bool) {
		t.Helper()
		outDir := filepath.Join(dir, name)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		recvCl, err := signaling.Dial(ctx, sigURL, recvKey, nil)
		if err != nil {
			t.Fatalf("%s: recv signaling: %v", name, err)
		}
		defer recvCl.Close()

		reqCh := make(chan signaling.SendRequest, 1)
		candCh := make(chan *signaling.Envelope, 8)
		readyCh := make(chan signaling.RelayReady, 2)
		recvCl.On(signaling.TypeSendRequest, func(e *signaling.Envelope) {
			if r, rerr := signaling.Decode[signaling.SendRequest](e); rerr == nil {
				reqCh <- r
			}
		})
		recvCl.On(signaling.TypeCandidates, func(e *signaling.Envelope) {
			candCh <- e
		})
		recvCl.On(signaling.TypeRelayReady, func(e *signaling.Envelope) {
			if rr, rerr := signaling.Decode[signaling.RelayReady](e); rerr == nil {
				readyCh <- rr
			}
		})

		sendCtrl := transfer.NewController()
		recvCtrl := transfer.NewController()

		recvDone := make(chan error, 1)
		go func() {
			req := <-reqCh
			peerPub, perr := PairedPeer(recvPairedPath, req.FromDevice)
			if perr != nil {
				recvDone <- perr
				return
			}
			recvCancelTriggered := false
			_, rerr := ReceiveAuto(ctx, ReceiveOptions{
				Key:               recvKey,
				SignalURL:         sigURL,
				OutDir:            outDir,
				PairedPath:        recvPairedPath,
				Name:              "recv-pc",
				DisablePunch:      true,
				DisableHotUpgrade: true,
				Control:           recvCtrl,
				OnStatus: func(st Status) {
					if !cancelSender && !recvCancelTriggered && st.BytesDone >= 1<<20 {
						recvCancelTriggered = true
						recvCtrl.Cancel()
					}
				},
			}, recvCl, req, peerPub, candCh, readyCh)
			recvDone <- rerr
		}()

		sendCancelTriggered := false
		_, sendErr := SendAuto(ctx, Options{
			SignalURL:         sigURL,
			KeyPath:           sendKeyPath,
			PairedPath:        sendPairedPath,
			Name:              "send-pc",
			DisablePunch:      true,
			DisableHotUpgrade: true,
			Control:           sendCtrl,
			OnStatus: func(st Status) {
				if cancelSender && !sendCancelTriggered && st.BytesDone >= 1<<20 {
					sendCancelTriggered = true
					sendCtrl.Cancel()
				}
			},
		}, recvKey.IDHex(), []string{src})
		recvErr := <-recvDone

		if cancelSender {
			if !errors.Is(sendErr, transfer.ErrCancelled) {
				t.Fatalf("%s: expected sender ErrCancelled, got %v", name, sendErr)
			}
			if !transfer.IsCancelled(recvErr) {
				t.Fatalf("%s: expected receiver transfer.IsCancelled(err)=true (CodeCancelled), got %v", name, recvErr)
			}
		} else {
			if !errors.Is(recvErr, transfer.ErrCancelled) {
				t.Fatalf("%s: expected receiver ErrCancelled, got %v", name, recvErr)
			}
			if !transfer.IsCancelled(sendErr) {
				t.Fatalf("%s: expected sender transfer.IsCancelled(err)=true (CodeCancelled), got %v", name, sendErr)
			}
		}
		if _, statErr := os.Stat(filepath.Join(outDir, "cancel_payload.bin.ftpart")); !os.IsNotExist(statErr) {
			t.Fatalf("%s: expected .ftpart removed after cancel, stat=%v", name, statErr)
		}
	}

	runOne("sender_cancel", true)
	runOne("receiver_cancel", false)
}
