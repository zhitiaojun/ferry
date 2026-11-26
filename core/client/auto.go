package client

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/zhitiaojun/ferry/core/discovery"
	"github.com/zhitiaojun/ferry/core/identity"
	"github.com/zhitiaojun/ferry/core/logging"
	"github.com/zhitiaojun/ferry/core/pairing"
	"github.com/zhitiaojun/ferry/core/pathmgr"
	"github.com/zhitiaojun/ferry/core/proto"
	"github.com/zhitiaojun/ferry/core/punch"
	"github.com/zhitiaojun/ferry/core/relay"
	"github.com/zhitiaojun/ferry/core/session"
	"github.com/zhitiaojun/ferry/core/signaling"
	"github.com/zhitiaojun/ferry/core/transfer"
)

const (
	// DefaultSTUN is the public BJ STUN pair used for UDP candidate discovery.
	DefaultSTUN = "123.57.253.208:40000,123.57.253.208:40001"
	// PunchHeadStart gives Cone/UPnP punching a brief head start before dialing
	// the relay; symmetric/TUN environments fall into relay within 600 ms.
	PunchHeadStart = 600 * time.Millisecond
	// PunchMinBytesPerSec is the 5 Mbps floor below which a punched transfer
	// falls back to the relay and resumes from .ftpart.
	PunchMinBytesPerSec int64 = 625_000
	// HotUpgradeMinBytes skips background birthday punching on small batches
	// that finish over the 200M relay before P2P setup would pay off.
	HotUpgradeMinBytes int64 = 4 << 20
	// HotUpgradeTimeout bounds background birthday-paradox NAT punching.
	HotUpgradeTimeout = 10 * time.Second
	// RelayFallbackWait is how long a receiver waits for a fallback relay room
	// when an active punched transfer fails mid-stream.
	RelayFallbackWait = 8 * time.Second
)

// Test hooks overridden only inside package client unit tests.
var (
	testBirthdayPortRange [2]int
	testDisableFastPunch  bool
)

// DefaultSTUNList returns the default BJ STUN server list.
func DefaultSTUNList() []string {
	var out []string
	for _, s := range strings.Split(DefaultSTUN, ",") {
		if t := strings.TrimSpace(s); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// ReceiveOptions configures an auto-path inbound transfer (UDP punch or relay).
type ReceiveOptions struct {
	Key                *identity.Key
	KeyPath            string
	SignalURL          string
	OutDir             string
	PairedPath         string
	Name               string
	STUNServers        []string        // nil uses DefaultSTUNList; empty non-nil skips STUN
	LANHint            *discovery.Hint // optional interface hint (Android avoids netlink)
	SkipLANEnum        bool            // true skips discovery.Eligible() when LANHint is nil
	Chunk              int
	Timeout            time.Duration
	HotUpgradeMinBytes int64 // 0 defaults to HotUpgradeMinBytes (4 MiB)
	DisableHotUpgrade  bool
	DisablePunch       bool
	NoTLS              bool
	Control            *transfer.Controller
	Log                *logging.Logger
	OnStatus           func(Status)
}

func gatherCandidates(ctx context.Context, stun []string, hint *discovery.Hint, skipLANEnum bool, log *logging.Logger) (*punch.Gathered, error) {
	var g *punch.Gathered
	var err error
	switch {
	case hint != nil:
		g, err = punch.GatherHint(ctx, stun, 2*time.Second, hint, log)
	case skipLANEnum:
		g, err = punch.GatherSTUNOnly(ctx, stun, 2*time.Second, log)
	default:
		g, err = punch.Gather(ctx, stun, 2*time.Second, log)
	}
	if err != nil {
		return nil, err
	}
	if os.Getenv("FT_PUNCH_LOOPBACK") == "1" && g.Conn != nil {
		if ap, ok := g.Conn.LocalAddr().(*net.UDPAddr); ok {
			g.Candidates = append(g.Candidates, punch.Candidate{
				Kind: "lan", Addr: net.JoinHostPort("127.0.0.1", fmt.Sprint(ap.Port)), Priority: 100,
			})
		}
	}
	return g, nil
}

// SendAuto prefers UDP hole punching (with a PunchHeadStart over relay when
// UDP candidates exist) and automatically falls back via .ftpart resume to
// relay if punch fails or drops below 5 Mbps.
func SendAuto(ctx context.Context, o Options, toID string, files []string) (Result, error) {
	if len(files) == 0 {
		return Result{}, fmt.Errorf("no files to send")
	}
	if o.Control != nil {
		if o.Control.IsCancelled() {
			return Result{}, transfer.ErrCancelled
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
		unreg := o.Control.OnCancel(cancel)
		defer unreg()
	}
	infos, total, err := describeFiles(files)
	if err != nil {
		return Result{}, err
	}
	firstName := infos[0].Name
	emitStatus := func(st Status) {
		if o.OnStatus != nil {
			o.OnStatus(st)
		}
	}
	emitStatus(Status{
		Stage:      "connecting",
		FileIndex:  1,
		FileCount:  len(infos),
		FileName:   firstName,
		BytesTotal: total,
	})

	key := o.Key
	if key == nil {
		key, err = identity.LoadOrCreateKey(o.KeyPath)
		if err != nil {
			return Result{}, fmt.Errorf("device key %s: %w", o.KeyPath, err)
		}
	}
	peerPub, err := pairedPeer(o.PairedPath, toID)
	if err != nil {
		return Result{}, err
	}

	var g *punch.Gathered
	var gerr error
	if !o.DisablePunch {
		stun := o.STUNServers
		if stun == nil {
			stun = DefaultSTUNList()
		}
		g, gerr = gatherCandidates(ctx, stun, o.LANHint, o.SkipLANEnum, o.Log)
		if gerr == nil {
			defer g.Close()
		}
	} else {
		gerr = errors.New("punch disabled")
	}

	cl := o.SignalClient
	reused := cl != nil && cl.Alive()
	if !reused {
		cl, err = signaling.Dial(ctx, o.SignalURL, key, o.Log)
		if err != nil {
			return Result{}, err
		}
		defer cl.Close()
	}

	sessionID, err := randomHex16()
	if err != nil {
		return Result{}, err
	}
	transferID, err := randomHex16()
	if err != nil {
		return Result{}, err
	}

	events := make(chan *signaling.Envelope, 16)
	cl.On(signaling.TypeSendResp, func(e *signaling.Envelope) {
		select {
		case events <- e:
		default:
		}
	})
	cl.On(signaling.TypeError, func(e *signaling.Envelope) {
		select {
		case events <- e:
		default:
		}
	})
	defer cl.Off(signaling.TypeSendResp)
	defer cl.Off(signaling.TypeError)

	if reused && o.CandRoute != nil {
		unreg := o.CandRoute(sessionID, events)
		defer unreg()
	} else {
		cl.On(signaling.TypeCandidates, func(e *signaling.Envelope) {
			select {
			case events <- e:
			default:
			}
		})
		defer cl.Off(signaling.TypeCandidates)
	}

	req := signaling.SendRequest{
		ToDevice:   toID,
		SessionID:  sessionID,
		TransferID: transferID,
		Files:      infos,
	}
	if err := cl.Send(ctx, signaling.TypeSendRequest, req); err != nil {
		return Result{}, fmt.Errorf("send_request: %w", err)
	}
	emitStatus(Status{
		Stage:      "waiting",
		FileIndex:  1,
		FileCount:  len(infos),
		FileName:   firstName,
		BytesTotal: total,
	})

	var peerCands []signaling.Candidate
	accepted := false
	gotCands := false
	reqDeadline := time.NewTimer(15 * time.Second)
	defer reqDeadline.Stop()
	var candDeadline *time.Timer
	var candDeadlineC <-chan time.Time

	for !accepted || !gotCands {
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case <-cl.Done():
			return Result{}, fmt.Errorf("signaling connection dropped: %v", cl.Err())
		case <-reqDeadline.C:
			return Result{}, fmt.Errorf("peer did not answer the request")
		case <-candDeadlineC:
			gotCands = true
		case e := <-events:
			switch e.Type {
			case signaling.TypeError:
				body, _ := signaling.Decode[signaling.ErrorBody](e)
				return Result{}, fmt.Errorf("signaling: %s: %s", body.Code, body.Message)
			case signaling.TypeSendResp:
				r, derr := signaling.Decode[signaling.SendResponse](e)
				if derr != nil {
					return Result{}, derr
				}
				if !r.Accept {
					return Result{}, fmt.Errorf("peer rejected the transfer: %s", r.Reason)
				}
				accepted = true
				emitStatus(Status{
					Stage:      "racing",
					FileIndex:  1,
					FileCount:  len(infos),
					FileName:   firstName,
					BytesTotal: total,
				})
				var local []signaling.Candidate
				if g != nil {
					local = signalingCandidates(g.Candidates)
				}
				if err := cl.Send(ctx, signaling.TypeCandidates, signaling.Candidates{
					ToDevice: toID, SessionID: sessionID, Candidates: local,
				}); err != nil {
					return Result{}, fmt.Errorf("send candidates: %w", err)
				}
				if !gotCands {
					candDeadline = time.NewTimer(3 * time.Second)
					defer candDeadline.Stop()
					candDeadlineC = candDeadline.C
				}
			case signaling.TypeCandidates:
				c, derr := signaling.Decode[signaling.Candidates](e)
				if derr != nil {
					return Result{}, derr
				}
				peerCands = c.Candidates
				gotCands = true
			}
		}
	}

	if o.Log != nil {
		o.Log.Event("signal.accepted", map[string]any{"to": truncHex(toID), "files": len(infos), "bytes": total})
	}

	localName := pairing.SanitizeName(o.Name)
	peerLocal := localCandidates(peerCands)
	peerAddrs := punch.Resolve(peerLocal)

	var sess *session.Session
	var winner string

	if g != nil && len(peerAddrs) > 0 && !o.DisablePunch && !testDisableFastPunch && os.Getenv("FT_PUNCH_BLOCK") != "1" {
		tlsCfg, derr := identity.ClientTLS(key, peerPub)
		if derr != nil {
			return Result{}, derr
		}
		q := pathmgr.NewQUIC(g.Conn, tlsCfg)
		defer q.Close()
		if err := q.PrepareListen(ctx); err != nil {
			return Result{}, err
		}

		punchFailed := make(chan struct{})
		var punchOnce sync.Once
		markPunchFailed := func() {
			punchOnce.Do(func() { close(punchFailed) })
		}

		punchAttempt := pathmgr.Attempt[*session.Session]{
			Name: "punch",
			Fn: func(actx context.Context) (*session.Session, error) {
				pctx, pcancel := context.WithTimeout(actx, 12*time.Second)
				defer pcancel()
				go g.Punch(pctx, peerLocal)
				s, cerr := punch.Connect(pctx, q, peerAddrs, key.ID(), parseDeviceID(toID), sessionParams(key, localName))
				if cerr != nil {
					markPunchFailed()
					return nil, cerr
				}
				return s, nil
			},
		}
		relayAttempt := pathmgr.Attempt[*session.Session]{
			Name: "relay",
			Fn: func(actx context.Context) (*session.Session, error) {
				t := time.NewTimer(PunchHeadStart)
				select {
				case <-actx.Done():
					t.Stop()
					return nil, actx.Err()
				case <-punchFailed:
					t.Stop()
				case <-t.C:
				}
				return openRelaySession(actx, cl, key, peerPub, localName, toID, transferID, o.NoTLS)
			},
		}
		sess, winner, err = pathmgr.Race(ctx, punchAttempt, relayAttempt)
		if err != nil {
			return Result{}, fmt.Errorf("race: %w", err)
		}
	} else {
		sess, err = openRelaySession(ctx, cl, key, peerPub, localName, toID, transferID, o.NoTLS)
		if err != nil {
			return Result{}, err
		}
		winner = "relay"
	}

	peerName := pairing.SanitizeName(sess.Peer.Name)
	if peerName != "" && o.PairedPath != "" {
		identity.UpdatePeerName(o.PairedPath, toID, peerName)
	}
	if o.Log != nil {
		o.Log.Event("path.selected", map[string]any{"path": winner, "peer": truncHex(toID)})
	}
	emitStatus(Status{
		Stage:      "transferring",
		Channel:    winner,
		FileIndex:  1,
		FileCount:  len(infos),
		FileName:   firstName,
		BytesTotal: total,
	})

	hotMin := o.HotUpgradeMinBytes
	if hotMin <= 0 {
		hotMin = HotUpgradeMinBytes
	}

	type upgradePair struct {
		sess    *session.Session
		cleanup func()
	}
	var upgradeSig chan struct{}
	var upPairCh chan upgradePair
	var upDone chan struct{}
	var upgradedCleanup func()
	defer func() {
		if upDone != nil {
			<-upDone
		}
		if upPairCh != nil {
			for {
				select {
				case up := <-upPairCh:
					if up.sess != nil {
						_ = up.sess.Close()
					}
					if up.cleanup != nil {
						up.cleanup()
					}
				default:
					goto drained
				}
			}
		}
	drained:
		if upgradedCleanup != nil {
			upgradedCleanup()
		}
	}()

	if winner == "relay" && !o.DisableHotUpgrade && total >= hotMin &&
		(sess.Peer.Capabilities&proto.CapHotUpgrade) != 0 && len(peerAddrs) > 0 {
		bgCtx, bgCancel := context.WithTimeout(ctx, HotUpgradeTimeout)
		defer bgCancel()
		upgradeSig = make(chan struct{})
		upPairCh = make(chan upgradePair, 1)
		upDone = make(chan struct{})
		var localMap punch.Behavior
		if g != nil {
			localMap = g.Mapping
		}
		bpCfg := punch.BirthdayConfig{
			SelfID:          key.ID(),
			PeerID:          parseDeviceID(toID),
			SessionID:       sessionID,
			PeerCandidates:  peerLocal,
			LocalMapping:    localMap,
			PortMin:         testBirthdayPortRange[0],
			PortMax:         testBirthdayPortRange[1],
			BindInPortRange: testBirthdayPortRange[0] > 0 && testBirthdayPortRange[1] > testBirthdayPortRange[0],
			Log:             o.Log,
		}
		go func() {
			defer close(upDone)
			upS, upCl, berr := punch.BirthdayConnect(bgCtx, bpCfg, key, peerPub, sessionParams(key, localName))
			if berr != nil {
				return
			}
			select {
			case upPairCh <- upgradePair{sess: upS, cleanup: upCl}:
				close(upgradeSig)
			default:
				_ = upS.Close()
				if upCl != nil {
					upCl()
				}
			}
		}()
	}

	start := time.Now()
	tcfg := transfer.Config{
		ChunkSize:  uint32(o.Chunk),
		Timeout:    o.Timeout,
		Control:    o.Control,
		Log:        o.Log,
		TransferID: hex16(transferID),
		UpgradeCh:  upgradeSig,
		OnDetailProgress: func(snap transfer.ProgressSnapshot) {
			emitStatus(Status{
				Stage:      snap.Stage,
				Channel:    winner,
				FileIndex:  snap.FileIndex,
				FileCount:  snap.FileCount,
				FileName:   snap.FileName,
				BytesDone:  snap.BytesDone,
				BytesTotal: snap.BytesTotal,
			})
		},
	}
	if o.HotUpgradeMinBytes > 0 {
		tcfg.UpgradeMinRemaining = o.HotUpgradeMinBytes / 2
		if tcfg.UpgradeMinRemaining < 256<<10 {
			tcfg.UpgradeMinRemaining = 256 << 10
		}
	}
	if winner == "punch" && (sess.Peer.Capabilities&proto.CapRelayFallback) != 0 {
		tcfg.MinBytesPerSec = PunchMinBytesPerSec
	}

	res, err := transfer.Send(sess, files, tcfg)
	if errors.Is(err, transfer.ErrPathUpgrade) && !(o.Control != nil && o.Control.IsCancelled()) {
		up := <-upPairCh
		_ = sess.Close()
		sess = up.sess
		upgradedCleanup = up.cleanup
		winner = "punch"
		if o.Log != nil {
			o.Log.Event("path.hot_upgrade_punch", map[string]any{"peer": truncHex(toID), "transfer_id": transferID})
		}
		emitStatus(Status{
			Stage:      "transferring",
			Channel:    "punch",
			FileIndex:  1,
			FileCount:  len(infos),
			FileName:   firstName,
			BytesTotal: total,
		})
		tcfg.UpgradeCh = nil
		if (sess.Peer.Capabilities & proto.CapRelayFallback) != 0 {
			tcfg.MinBytesPerSec = PunchMinBytesPerSec
		}
		res, err = transfer.Send(sess, files, tcfg)
	}
	if errors.Is(err, transfer.ErrSlowPath) && !(o.Control != nil && o.Control.IsCancelled()) {
		if o.Log != nil {
			o.Log.Event("path.fallback_slow_punch", map[string]any{"peer": truncHex(toID), "transfer_id": transferID})
		}
		_ = sess.Close()
		emitStatus(Status{
			Stage:      "connecting",
			Channel:    "relay",
			FileIndex:  1,
			FileCount:  len(infos),
			FileName:   firstName,
			BytesTotal: total,
		})
		sess, err = openRelaySession(ctx, cl, key, peerPub, localName, toID, transferID, o.NoTLS)
		if err != nil {
			return Result{}, fmt.Errorf("fallback relay: %w", err)
		}
		winner = "relay"
		emitStatus(Status{
			Stage:      "transferring",
			Channel:    "relay",
			FileIndex:  1,
			FileCount:  len(infos),
			FileName:   firstName,
			BytesTotal: total,
		})
		tcfg.MinBytesPerSec = 0
		tcfg.UpgradeCh = nil
		res, err = transfer.Send(sess, files, tcfg)
	}
	_ = sess.Close()
	if err != nil {
		return Result{}, err
	}

	return Result{
		TransferID: transferID,
		Channel:    channelLabel(winner),
		PeerName:   peerName,
		Files:      res.Files,
		Bytes:      res.Bytes,
		Chunks:     res.Chunks,
		Duration:   time.Since(start),
	}, nil
}

// ReceiveAuto handles an inbound SendRequest by racing UDP hole punching and
// relay, and automatically accepting a fallback relay room if a slow punched
// path aborts mid-transfer.
func ReceiveAuto(
	ctx context.Context,
	o ReceiveOptions,
	cl *signaling.Client,
	req signaling.SendRequest,
	peerPub ed25519.PublicKey,
	candCh <-chan *signaling.Envelope,
	readyCh <-chan signaling.RelayReady,
) (Result, error) {
	if o.Key == nil {
		return Result{}, fmt.Errorf("nil identity key")
	}
	if cl == nil {
		return Result{}, fmt.Errorf("nil signaling client")
	}
	if o.Control != nil {
		if o.Control.IsCancelled() {
			return Result{}, transfer.ErrCancelled
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
		unreg := o.Control.OnCancel(cancel)
		defer unreg()
	}
	var total int64
	firstName := ""
	if len(req.Files) > 0 {
		firstName = req.Files[0].Name
		for _, f := range req.Files {
			total += f.Size
		}
	}
	emitStatus := func(st Status) {
		if o.OnStatus != nil {
			o.OnStatus(st)
		}
	}
	emitStatus(Status{
		Stage:      "connecting",
		FileIndex:  1,
		FileCount:  len(req.Files),
		FileName:   firstName,
		BytesTotal: total,
	})

	var g *punch.Gathered
	var gerr error
	if !o.DisablePunch {
		stun := o.STUNServers
		if stun == nil {
			stun = DefaultSTUNList()
		}
		g, gerr = gatherCandidates(ctx, stun, o.LANHint, o.SkipLANEnum, o.Log)
		if gerr == nil {
			defer g.Close()
		}
	} else {
		gerr = errors.New("punch disabled")
	}

	if err := cl.Send(ctx, signaling.TypeSendResp, signaling.SendResponse{
		ToDevice: req.FromDevice, TransferID: req.TransferID, Accept: true,
	}); err != nil {
		return Result{}, err
	}

	var local []signaling.Candidate
	if g != nil {
		local = signalingCandidates(g.Candidates)
	}
	if req.SessionID != "" {
		if err := cl.Send(ctx, signaling.TypeCandidates, signaling.Candidates{
			ToDevice: req.FromDevice, SessionID: req.SessionID, Candidates: local,
		}); err != nil {
			return Result{}, err
		}
	}

	emitStatus(Status{
		Stage:      "racing",
		FileIndex:  1,
		FileCount:  len(req.Files),
		FileName:   firstName,
		BytesTotal: total,
	})

	localName := pairing.SanitizeName(o.Name)
	var attempts []pathmgr.Attempt[*session.Session]

	var peerCandsMu sync.Mutex
	var savedPeerCands []signaling.Candidate
	savePeerCands := func(cs []signaling.Candidate) {
		peerCandsMu.Lock()
		savedPeerCands = append([]signaling.Candidate(nil), cs...)
		peerCandsMu.Unlock()
	}
	getSavedPeerCands := func() []signaling.Candidate {
		peerCandsMu.Lock()
		defer peerCandsMu.Unlock()
		return append([]signaling.Candidate(nil), savedPeerCands...)
	}

	if gerr == nil && g != nil && !o.DisablePunch && !testDisableFastPunch && os.Getenv("FT_PUNCH_BLOCK") != "1" {
		serverCfg, err := identity.ServerTLS(o.Key, peerPub)
		if err != nil {
			return Result{}, err
		}
		q := pathmgr.NewQUIC(g.Conn, serverCfg)
		defer q.Close()
		if err := q.PrepareListen(ctx); err != nil {
			return Result{}, err
		}
		attempts = append(attempts, pathmgr.Attempt[*session.Session]{
			Name: "punch",
			Fn: func(actx context.Context) (*session.Session, error) {
				var peerCands []signaling.Candidate
				t := time.NewTimer(10 * time.Second)
				defer t.Stop()
				for peerCands == nil {
					select {
					case <-actx.Done():
						return nil, actx.Err()
					case <-cl.Done():
						return nil, fmt.Errorf("signaling dropped: %v", cl.Err())
					case <-t.C:
						return nil, fmt.Errorf("peer did not publish candidates")
					case ce := <-candCh:
						c, derr := signaling.Decode[signaling.Candidates](ce)
						if derr != nil {
							return nil, derr
						}
						peerCands = c.Candidates
						if peerCands == nil {
							peerCands = []signaling.Candidate{}
						}
						savePeerCands(peerCands)
					}
				}
				peerLocal := localCandidates(peerCands)
				addrs := punch.Resolve(peerLocal)
				if len(addrs) == 0 {
					return nil, fmt.Errorf("peer has no udp candidates")
				}
				pctx, cancel := context.WithTimeout(actx, 25*time.Second)
				defer cancel()
				go g.Punch(pctx, peerLocal)
				return punch.Connect(pctx, q, addrs, o.Key.ID(), parseDeviceID(req.FromDevice), sessionParams(o.Key, localName))
			},
		})
	}

	attempts = append(attempts, pathmgr.Attempt[*session.Session]{
		Name: "relay",
		Fn: func(actx context.Context) (*session.Session, error) {
			var ready signaling.RelayReady
			select {
			case <-actx.Done():
				return nil, actx.Err()
			case <-cl.Done():
				return nil, fmt.Errorf("signaling dropped: %v", cl.Err())
			case ready = <-readyCh:
			}
			return acceptRelaySession(actx, o.SignalURL, ready, o.Key, peerPub, localName, o.NoTLS)
		},
	})

	sess, winner, err := pathmgr.Race(ctx, attempts...)
	if err != nil {
		return Result{}, fmt.Errorf("race: %w", err)
	}

	peerName := pairing.SanitizeName(sess.Peer.Name)
	if peerName != "" && o.PairedPath != "" {
		identity.UpdatePeerName(o.PairedPath, req.FromDevice, peerName)
	}
	if o.Log != nil {
		o.Log.Event("path.selected", map[string]any{"path": winner, "peer": truncHex(req.FromDevice)})
	}
	emitStatus(Status{
		Stage:      "transferring",
		Channel:    winner,
		FileIndex:  1,
		FileCount:  len(req.Files),
		FileName:   firstName,
		BytesTotal: total,
	})

	hotMin := o.HotUpgradeMinBytes
	if hotMin <= 0 {
		hotMin = HotUpgradeMinBytes
	}

	type recvUpgradePair struct {
		sess    *session.Session
		cleanup func()
	}
	var upPairCh chan recvUpgradePair
	var upDone chan struct{}
	var upgradedCleanup func()
	defer func() {
		if upDone != nil {
			<-upDone
		}
		if upPairCh != nil {
			for {
				select {
				case up := <-upPairCh:
					if up.sess != nil {
						_ = up.sess.Close()
					}
					if up.cleanup != nil {
						up.cleanup()
					}
				default:
					goto drainedRecv
				}
			}
		}
	drainedRecv:
		if upgradedCleanup != nil {
			upgradedCleanup()
		}
	}()

	if winner == "relay" && !o.DisableHotUpgrade && total >= hotMin &&
		(sess.Peer.Capabilities&proto.CapHotUpgrade) != 0 && req.SessionID != "" {
		bgCtx, bgCancel := context.WithTimeout(ctx, HotUpgradeTimeout)
		defer bgCancel()
		upPairCh = make(chan recvUpgradePair, 1)
		upDone = make(chan struct{})
		var localMap punch.Behavior
		if g != nil {
			localMap = g.Mapping
		}
		go func() {
			defer close(upDone)
			peerCands := getSavedPeerCands()
			if peerCands == nil && candCh != nil {
				waitC := time.NewTimer(2 * time.Second)
				select {
				case <-bgCtx.Done():
					waitC.Stop()
					return
				case <-waitC.C:
					return
				case ce := <-candCh:
					waitC.Stop()
					if c, derr := signaling.Decode[signaling.Candidates](ce); derr == nil {
						peerCands = c.Candidates
					}
				}
			}
			peerLocal := localCandidates(peerCands)
			if len(punch.Resolve(peerLocal)) == 0 {
				return
			}
			bpCfg := punch.BirthdayConfig{
				SelfID:          o.Key.ID(),
				PeerID:          parseDeviceID(req.FromDevice),
				SessionID:       req.SessionID,
				PeerCandidates:  peerLocal,
				LocalMapping:    localMap,
				PortMin:         testBirthdayPortRange[0],
				PortMax:         testBirthdayPortRange[1],
				BindInPortRange: testBirthdayPortRange[0] > 0 && testBirthdayPortRange[1] > testBirthdayPortRange[0],
				Log:             o.Log,
			}
			upS, upCl, berr := punch.BirthdayConnect(bgCtx, bpCfg, o.Key, peerPub, sessionParams(o.Key, localName))
			if berr != nil {
				return
			}
			select {
			case upPairCh <- recvUpgradePair{sess: upS, cleanup: upCl}:
			default:
				_ = upS.Close()
				if upCl != nil {
					upCl()
				}
			}
		}()
	}

	start := time.Now()
	tcfg := transfer.Config{
		ChunkSize:  uint32(o.Chunk),
		Timeout:    o.Timeout,
		Control:    o.Control,
		Log:        o.Log,
		TransferID: hex16(req.TransferID),
		OnDetailProgress: func(snap transfer.ProgressSnapshot) {
			emitStatus(Status{
				Stage:      snap.Stage,
				Channel:    winner,
				FileIndex:  snap.FileIndex,
				FileCount:  snap.FileCount,
				FileName:   snap.FileName,
				BytesDone:  snap.BytesDone,
				BytesTotal: snap.BytesTotal,
			})
		},
	}

	res, err := transfer.Receive(sess, o.OutDir, tcfg)
	if err != nil && !isTransferCancelled(err) && !(o.Control != nil && o.Control.IsCancelled()) && winner == "relay" && upPairCh != nil {
		var remote *session.RemoteError
		wantsSwitch := errors.As(err, &remote) && remote.Code == proto.CodePathSwitch
		var up recvUpgradePair
		gotUp := false
		if wantsSwitch {
			wt := time.NewTimer(3 * time.Second)
			select {
			case <-ctx.Done():
				wt.Stop()
			case <-upDone:
				wt.Stop()
				select {
				case up = <-upPairCh:
					gotUp = up.sess != nil
				default:
				}
			case up = <-upPairCh:
				wt.Stop()
				gotUp = up.sess != nil
			case <-wt.C:
			}
		} else {
			select {
			case up = <-upPairCh:
				gotUp = up.sess != nil
			default:
			}
		}
		if gotUp {
			_ = sess.Close()
			sess = up.sess
			upgradedCleanup = up.cleanup
			winner = "punch"
			if o.Log != nil {
				o.Log.Event("path.hot_upgrade_punch", map[string]any{"peer": truncHex(req.FromDevice), "transfer_id": req.TransferID})
			}
			emitStatus(Status{
				Stage:      "transferring",
				Channel:    "punch",
				FileIndex:  1,
				FileCount:  len(req.Files),
				FileName:   firstName,
				BytesTotal: total,
			})
			res, err = transfer.Receive(sess, o.OutDir, tcfg)
		}
	}
	if err != nil && !isTransferCancelled(err) && !(o.Control != nil && o.Control.IsCancelled()) && winner == "punch" {
		_ = sess.Close()
		emitStatus(Status{
			Stage:      "connecting",
			Channel:    "relay",
			FileIndex:  1,
			FileCount:  len(req.Files),
			FileName:   firstName,
			BytesTotal: total,
		})
		waitTimer := time.NewTimer(RelayFallbackWait)
		var ready signaling.RelayReady
		gotFallback := false
		select {
		case <-ctx.Done():
			waitTimer.Stop()
		case <-cl.Done():
			waitTimer.Stop()
		case <-waitTimer.C:
		case ready = <-readyCh:
			waitTimer.Stop()
			gotFallback = true
		}
		if gotFallback {
			relaySess, rerr := acceptRelaySession(ctx, o.SignalURL, ready, o.Key, peerPub, localName, o.NoTLS)
			if rerr != nil {
				return Result{}, fmt.Errorf("fallback relay: %w", rerr)
			}
			sess = relaySess
			winner = "relay"
			emitStatus(Status{
				Stage:      "transferring",
				Channel:    "relay",
				FileIndex:  1,
				FileCount:  len(req.Files),
				FileName:   firstName,
				BytesTotal: total,
			})
			res, err = transfer.Receive(sess, o.OutDir, tcfg)
		}
	}
	if err != nil {
		_ = sess.Close()
		return Result{}, err
	}
	sess.Drain(2 * time.Second)

	return Result{
		TransferID: req.TransferID,
		Channel:    channelLabel(winner),
		PeerName:   peerName,
		Files:      res.Files,
		Bytes:      res.Bytes,
		Chunks:     res.Chunks,
		Duration:   time.Since(start),
	}, nil
}

func openRelaySession(ctx context.Context, cl *signaling.Client, key *identity.Key, peerPub ed25519.PublicKey, localName, toID, transferID string, noTLS bool) (*session.Session, error) {
	ready, err := openRelayRoom(ctx, cl, toID, transferID)
	if err != nil {
		return nil, err
	}
	rc, err := relay.Dial(ctx, ready.url)
	if err != nil {
		return nil, err
	}
	conn := net.Conn(rc)
	if !noTLS {
		conn, err = clientTLS(ctx, conn, key, peerPub)
		if err != nil {
			_ = rc.Close()
			return nil, err
		}
	}
	s, err := session.Dial(conn, sessionParams(key, localName))
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("handshake over relay: %w", err)
	}
	return s, nil
}

func acceptRelaySession(ctx context.Context, signalURL string, ready signaling.RelayReady, key *identity.Key, peerPub ed25519.PublicKey, localName string, noTLS bool) (*session.Session, error) {
	u, err := relay.RelayURL(signalURL, ready.RelayURL, ready.Token)
	if err != nil {
		return nil, err
	}
	rc, err := relay.Dial(ctx, u)
	if err != nil {
		return nil, err
	}
	conn := net.Conn(rc)
	if !noTLS {
		conn, err = ServerTLS(ctx, conn, key, peerPub)
		if err != nil {
			_ = rc.Close()
			return nil, err
		}
	}
	s, err := session.Accept(conn, sessionParams(key, localName))
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	return s, nil
}

func channelLabel(winner string) string {
	switch winner {
	case "punch":
		return "打洞"
	case "lan":
		return "LAN"
	default:
		return "中继"
	}
}

func signalingCandidates(cs []punch.Candidate) []signaling.Candidate {
	out := make([]signaling.Candidate, 0, len(cs))
	for _, c := range cs {
		out = append(out, signaling.Candidate{Kind: c.Kind, Addr: c.Addr, Priority: c.Priority})
	}
	return out
}

func localCandidates(cs []signaling.Candidate) []punch.Candidate {
	out := make([]punch.Candidate, 0, len(cs))
	for _, c := range cs {
		out = append(out, punch.Candidate{Kind: c.Kind, Addr: c.Addr, Priority: c.Priority})
	}
	return out
}

func parseDeviceID(hexID string) [32]byte {
	var id [32]byte
	if b, err := hex.DecodeString(hexID); err == nil && len(b) == 32 {
		copy(id[:], b)
	}
	return id
}

// ReceiveOneAuto dials signaling, waits for one authorized SendRequest, and
// receives the transfer using ReceiveAuto (UDP punch + relay race + hot upgrade).
func ReceiveOneAuto(ctx context.Context, o ReceiveOptions) (Result, error) {
	key := o.Key
	if key == nil {
		var err error
		key, err = identity.LoadOrCreateKey(o.KeyPath)
		if err != nil {
			return Result{}, fmt.Errorf("device key %s: %w", o.KeyPath, err)
		}
		o.Key = key
	}
	sigURL := o.SignalURL
	if sigURL == "" {
		sigURL = DefaultSignalURL
		o.SignalURL = sigURL
	}
	cl, err := signaling.Dial(ctx, sigURL, key, o.Log)
	if err != nil {
		return Result{}, err
	}
	defer cl.Close()

	reqCh := make(chan *signaling.Envelope, 4)
	candCh := make(chan *signaling.Envelope, 8)
	readyCh := make(chan signaling.RelayReady, 2)
	cl.On(signaling.TypeSendRequest, func(e *signaling.Envelope) {
		select {
		case reqCh <- e:
		default:
		}
	})
	cl.On(signaling.TypeCandidates, func(e *signaling.Envelope) {
		select {
		case candCh <- e:
		default:
		}
	})
	cl.On(signaling.TypeRelayReady, func(e *signaling.Envelope) {
		if rr, rerr := signaling.Decode[signaling.RelayReady](e); rerr == nil {
			select {
			case readyCh <- rr:
			default:
			}
		}
	})
	if o.Log != nil {
		o.Log.Event("auto.ready", map[string]any{"device_id": truncHex(key.IDHex())})
	}

	var req signaling.SendRequest
	for {
		select {
		case <-ctx.Done():
			return Result{}, ctx.Err()
		case <-cl.Done():
			return Result{}, fmt.Errorf("signaling dropped: %v", cl.Err())
		case e := <-reqCh:
			r, derr := signaling.Decode[signaling.SendRequest](e)
			if derr != nil {
				return Result{}, derr
			}
			req = r
		}
		peerPub, perr := pairedPeer(o.PairedPath, req.FromDevice)
		if perr != nil {
			_ = cl.Send(ctx, signaling.TypeError, signaling.ErrorBody{Code: "unauthorized", Message: perr.Error()})
			continue
		}
		return ReceiveAuto(ctx, o, cl, req, peerPub, candCh, readyCh)
	}
}
