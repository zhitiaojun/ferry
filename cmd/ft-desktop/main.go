//go:build windows

// Command ft-desktop is the Windows desktop shell of filetransfer.
//
// Default mode is a tray-resident receiver: a notification-area icon plus a
// background listener that accepts filetransfer sessions on the LAN and writes
// them to a receive directory, showing a balloon when files arrive.
//
// With -drop it shows the "drop window" prototype: a small always-on-top box
// that accepts files dragged out of Explorer and prints their paths as JSON.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"sync"
	"time"

	"github.com/cwlindean/filetransfer/core/client"
	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/ipc"
	"github.com/cwlindean/filetransfer/core/signaling"
	"github.com/cwlindean/filetransfer/core/transfer"
)

var version = "1.0.0"

var (
	flagDrop    = flag.Bool("drop", false, "show the drop window instead of the tray receiver")
	flagDir     = flag.String("dir", "", "receive directory (default: %USERPROFILE%\\Downloads)")
	flagPort    = flag.Int("port", 18800, "receive port on all interfaces")
	flagPos     = flag.String("pos", "", "drop window position X,Y (drop mode only)")
	flagSize    = flag.String("size", "260x90", "drop window size WxH (drop mode only)")
	flagLog     = flag.String("log", "", "append JSONL events to this file")
	flagKey     = flag.String("key", "", "device key file (default: %LOCALAPPDATA%\\Ferry\\device.key)")
	flagPair    = flag.String("paired", "", "trusted device table (default: %LOCALAPPDATA%\\Ferry\\paired.json)")
	flagSig     = flag.String("signal", client.DefaultSignalURL, "signaling URL used to send via the relay")
	flagSTUN    = flag.String("stun", client.DefaultSTUN, "comma-separated UDP STUN servers for hole punching")
	flagWebBase = flag.String("web", client.DefaultWebBaseURL, "public website and temp share base URL")
	flagSend    = flag.String("send", "", "diagnostic: send these comma-separated files to the paired device, then exit")
	flagAuto    = flag.String("startup", "", "diagnostic: on/off/status for the HKCU Run entry, then exit")
	flagMain    = flag.Bool("main", false, "open the main window at startup (default behavior unless -silent)")
	flagSilent  = flag.Bool("silent", false, "start minimized to tray without opening the main window")
	flagSendTo  = flag.Bool("install-sendto", false, "install the Explorer \"Send to\" shortcut, then exit")
	flagPanel   = flag.Bool("panel", true, "show the drop panel while files are dragged")
)

func main() {
	setDPIAware()
	if os.Getenv("FT_DPI_DEBUG") != "" {
		d, _, _ := procGetDpiForSystem.Call()
		w, _, _ := user32.NewProc("GetSystemMetrics").Call(0)
		h, _, _ := user32.NewProc("GetSystemMetrics").Call(1)
		os.WriteFile("dpi-debug.txt", []byte(fmt.Sprintf("dpi=%d screen=%dx%d\n", d, w, h)), 0644)
	}
	flag.Parse()

	settingsPath := defaultSettingsPath()
	cfg := loadDesktopSettings(settingsPath)

	dir := *flagDir
	if dir == "" {
		dir = cfg.ReceiveDir
	}
	if dir == "" {
		dir = defaultReceiveDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		if *flagDir == "" && cfg.ReceiveDir != "" {
			dir = defaultReceiveDir()
			if derr := os.MkdirAll(dir, 0o755); derr != nil {
				fatal("create receive directory: %v", derr)
			}
		} else {
			fatal("create receive directory: %v", err)
		}
	}

	app := newApp(dir, *flagPort)
	app.settingsPath = settingsPath
	app.name = cfg.DeviceName
	if app.name == "" {
		app.name = defaultDeviceName()
	}
	app.pairedPath = *flagPair
	if app.pairedPath == "" {
		app.pairedPath = defaultPairedPath()
	}
	app.signalURL = *flagSig
	app.stunServers = splitSTUNList(*flagSTUN)
	app.webBase = *flagWebBase
	if *flagLog != "" {
		f, err := os.OpenFile(*flagLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			fatal("open log: %v", err)
		}
		app.logOut = f
	}

	if *flagSendTo {
		if err := installSendTo(); err != nil {
			fmt.Fprintln(os.Stderr, "install-sendto:", err)
			os.Exit(1)
		}
		fmt.Println("sendto shortcut installed")
		return
	}

	if *flagAuto != "" {
		switch *flagAuto {
		case "status":
			fmt.Printf("autostart=%v\n", autostartEnabled())
		case "on", "off":
			if err := setAutostart(*flagAuto == "on"); err != nil {
				fmt.Fprintln(os.Stderr, "startup:", err)
				os.Exit(1)
			}
			fmt.Printf("autostart=%v\n", autostartEnabled())
		default:
			fatal(`-startup must be on, off or status`)
		}
		return
	}

	if *flagDrop {
		w, h := parseSize(*flagSize)
		pos, err := parsePos(*flagPos)
		if err != nil {
			fatal("%v", err)
		}
		os.Exit(runDropWindow(app, w, h, 16, pos))
	}
	keyPath := *flagKey
	if keyPath == "" {
		keyPath = defaultKeyPath()
	}
	app.keyPath = keyPath

	files := flag.Args()
	if *flagSend != "" {
		files = strings.Split(*flagSend, ",")
	}
	if len(files) > 0 && app.logOut == nil {
		// Explorer's "Send to" starts us without a console: keep a log so the
		// outcome is still inspectable.
		sd := defaultStateDir()
		_ = os.MkdirAll(sd, 0o755)
		if f, err := os.OpenFile(filepath.Join(sd, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			app.logOut = f
		}
	}
	if len(files) > 0 && *flagSend == "" {
		if *flagKey == "" && *flagPair == "" {
			if cl, derr := ipc.TryConnect(defaultStateDir()); derr == nil {
				sres, serr := cl.Send(context.Background(), ipc.SendRequest{
					Files:       files,
					Interactive: true,
				})
				if serr != nil {
					if strings.Contains(serr.Error(), "已取消") {
						return
					}
					app.event("desktop.send_error", map[string]any{"error": serr.Error(), "ipc": true})
					fmt.Fprintln(os.Stderr, "send:", serr)
					os.Exit(1)
				}
				fmt.Printf("{\"ok\":true,\"to\":%q,\"path\":%q,\"bytes\":%d,\"chunks\":%d}\n", sres.Peer, sres.Path, sres.Bytes, sres.Chunks)
				return
			}
		}
		res, peer, cancelled, err := sendInteractive(app, files)
		if cancelled {
			return
		}
		if err != nil {
			app.event("desktop.send_error", map[string]any{"error": err.Error()})
			fmt.Fprintln(os.Stderr, "send:", err)
			os.Exit(1)
		}
		fmt.Printf("{\"ok\":true,\"to\":%q,\"bytes\":%d,\"chunks\":%d}\n", peer.Name, res.Bytes, res.Chunks)
		return
	}
	if *flagSend != "" {
		if *flagKey == "" && *flagPair == "" {
			if cl, derr := ipc.TryConnect(defaultStateDir()); derr == nil {
				sres, serr := cl.Send(context.Background(), ipc.SendRequest{Files: files})
				if serr != nil {
					app.event("desktop.send_error", map[string]any{"error": serr.Error(), "ipc": true})
					fmt.Fprintln(os.Stderr, "send:", serr)
					os.Exit(1)
				}
				fmt.Printf("{\"ok\":true,\"to\":%q,\"path\":%q,\"bytes\":%d,\"chunks\":%d}\n", sres.Peer, sres.Path, sres.Bytes, sres.Chunks)
				return
			}
		}
		res, peer, err := sendViaRelay(app, files)
		if err != nil {
			app.event("desktop.send_error", map[string]any{"error": err.Error()})
			fmt.Fprintln(os.Stderr, "send:", err)
			os.Exit(1)
		}
		fmt.Printf("{\"ok\":true,\"to\":%q,\"bytes\":%d,\"chunks\":%d}\n", peer.Name, res.Bytes, res.Chunks)
		return
	}

	app.showMain = !*flagSilent
	if *flagPanel {
		newDropPanel(app) // window + hook are created on the tray thread
	}
	dumpStacks()
	os.Exit(runTray(app, keyPath))
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ft-desktop: "+format+"\n", args...)
	os.Exit(2)
}

func parseSize(s string) (int32, int32) {
	var w, h int32 = 260, 90
	parts := strings.SplitN(strings.ToLower(s), "x", 2)
	if len(parts) == 2 {
		var a, b int32
		if _, err := fmt.Sscanf(parts[0], "%d", &a); err == nil && a >= 80 {
			w = a
		}
		if _, err := fmt.Sscanf(parts[1], "%d", &b); err == nil && b >= 40 {
			h = b
		}
	}
	return w, h
}

func parsePos(s string) (*[2]int32, error) {
	if s == "" {
		return nil, nil
	}
	var x, y int32
	if _, err := fmt.Sscanf(s, "%d,%d", &x, &y); err != nil {
		return nil, fmt.Errorf("invalid -pos %q: want X,Y", s)
	}
	return &[2]int32{x, y}, nil
}

// activeTransfer tracks the currently running send or receive session for live UI progress.
type activeTransfer struct {
	id         uint64
	dir        string // "send" | "recv"
	peer       string
	stage      string // "connecting" | "waiting" | "transferring" | "paused" | "peer_paused" | "verifying"
	channel    string // "" | "LAN" | "打洞" | "中继"
	fileName   string
	fileIndex  int
	fileCount  int
	bytesDone  int64
	bytesTotal int64
	speedBPS   int64
	startedAt  time.Time
	samples    []txSpeedSample
	paused     bool
	peerPaused bool
	canPause   bool
	canCancel  bool
	ctrl       *transfer.Controller
}

type txSpeedSample struct {
	t     time.Time
	bytes int64
}

// app holds state shared by tray and drop modes.
type app struct {
	dir          string
	name         string
	settingsPath string
	port         int
	keyPath      string
	webBase      string
	pairedPath   string
	signalURL    string
	stunServers  []string
	logOut       *os.File
	showMain     bool

	mu           sync.Mutex
	recent       []string
	txSeq        uint64
	tx           *activeTransfer
	lastPaint    time.Time
	signalState  string
	sigClient    *signaling.Client
	sigHoldCount int
	sigResumeCh  chan struct{}
	sigStopCur   context.CancelFunc
	cloudPeers   map[string]bool
	lanPeers     map[string]lanPeerEntry
	lanScanning  bool
	candRoutes   map[string]chan *signaling.Envelope
	readyRoute   chan signaling.RelayReady
}

func splitSTUNList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func (a *app) beginTransfer(dir, peer, fileName string, fileCount int, total int64) uint64 {
	if a == nil {
		return 0
	}
	now := time.Now()
	a.mu.Lock()
	a.txSeq++
	id := a.txSeq
	a.tx = &activeTransfer{
		id:         id,
		dir:        dir,
		peer:       peer,
		stage:      "connecting",
		fileName:   fileName,
		fileIndex:  1,
		fileCount:  fileCount,
		bytesTotal: total,
		startedAt:  now,
		samples:    []txSpeedSample{{t: now, bytes: 0}},
	}
	a.lastPaint = now
	a.mu.Unlock()
	if h := existingMainWindow(); h != 0 {
		procPostMessageW.Call(uintptr(h), wmAppPaint, 0, 0)
	}
	return id
}

func (a *app) updateTransfer(id uint64, stage, channel, peer, fileName string, fileIdx, fileCnt int, done, total int64) {
	if a == nil || id == 0 {
		return
	}
	now := time.Now()
	a.mu.Lock()
	if a.tx == nil || a.tx.id != id {
		a.mu.Unlock()
		return
	}
	changedMajor := false
	if a.tx.ctrl != nil {
		localP := a.tx.ctrl.IsPaused()
		peerP := a.tx.ctrl.IsPeerPaused()
		if a.tx.paused != localP || a.tx.peerPaused != peerP {
			changedMajor = true
		}
		a.tx.paused = localP
		a.tx.peerPaused = peerP
		if stage != "verifying" && a.tx.stage != "verifying" {
			switch {
			case localP:
				stage = "paused"
			case peerP:
				stage = "peer_paused"
			}
		}
	} else if stage != "" {
		a.tx.paused = stage == "paused"
		a.tx.peerPaused = stage == "peer_paused"
	}
	if stage != "" && stage != a.tx.stage {
		if (a.tx.stage == "paused" || a.tx.stage == "peer_paused") && stage == "transferring" {
			a.tx.samples = []txSpeedSample{{t: now, bytes: a.tx.bytesDone}}
		}
		a.tx.stage = stage
		changedMajor = true
	}
	if channel != "" && channel != a.tx.channel {
		a.tx.channel = channel
		changedMajor = true
	}
	if peer != "" && peer != a.tx.peer {
		a.tx.peer = peer
		changedMajor = true
	}
	if fileName != "" {
		a.tx.fileName = fileName
	}
	if fileIdx > 0 {
		a.tx.fileIndex = fileIdx
	}
	if fileCnt > 0 {
		a.tx.fileCount = fileCnt
	}
	if done >= 0 {
		prevDone := a.tx.bytesDone
		a.tx.bytesDone = done
		if done < prevDone || len(a.tx.samples) == 0 {
			a.tx.samples = []txSpeedSample{{t: now, bytes: done}}
		} else {
			a.tx.samples = append(a.tx.samples, txSpeedSample{t: now, bytes: done})
			for len(a.tx.samples) > 2 && now.Sub(a.tx.samples[0].t) > 2*time.Second {
				a.tx.samples = a.tx.samples[1:]
			}
		}
	}
	if total > 0 {
		a.tx.bytesTotal = total
	}
	if a.tx.stage == "verifying" || a.tx.stage == "paused" || a.tx.stage == "peer_paused" || a.tx.paused || a.tx.peerPaused {
		a.tx.speedBPS = 0
	} else {
		var spd int64
		if len(a.tx.samples) >= 2 {
			first := a.tx.samples[0]
			dt := now.Sub(first.t).Seconds()
			db := a.tx.bytesDone - first.bytes
			if dt >= 0.15 && db > 0 {
				spd = int64(float64(db) / dt)
			}
		}
		if spd == 0 && a.tx.bytesDone > 0 {
			if elapsed := now.Sub(a.tx.startedAt).Seconds(); elapsed >= 0.5 {
				spd = int64(float64(a.tx.bytesDone) / elapsed)
			}
		}
		a.tx.speedBPS = spd
	}
	shouldPaint := changedMajor || now.Sub(a.lastPaint) >= 80*time.Millisecond
	if shouldPaint {
		a.lastPaint = now
	}
	a.mu.Unlock()
	if shouldPaint {
		if h := existingMainWindow(); h != 0 {
			procPostMessageW.Call(uintptr(h), wmAppPaint, 0, 0)
		}
	}
}

func (a *app) endTransfer(id uint64) {
	if a == nil || id == 0 {
		return
	}
	a.mu.Lock()
	cleared := false
	if a.tx != nil && a.tx.id == id {
		a.tx = nil
		cleared = true
	}
	a.mu.Unlock()
	if cleared {
		if h := existingMainWindow(); h != 0 {
			procPostMessageW.Call(uintptr(h), wmAppPaint, 0, 0)
		}
	}
}

func (a *app) currentTransfer() *activeTransfer {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tx == nil {
		return nil
	}
	cp := *a.tx
	return &cp
}

func (a *app) attachTransferControl(id uint64, ctrl *transfer.Controller, canPause, canCancel bool) {
	if a == nil || id == 0 || ctrl == nil {
		return
	}
	localNow, peerNow := ctrl.IsPaused(), ctrl.IsPeerPaused()
	a.mu.Lock()
	matched := a.tx != nil && a.tx.id == id
	if matched {
		a.tx.ctrl = ctrl
		a.tx.canPause = canPause
		a.tx.canCancel = canCancel
		a.tx.paused = localNow
		a.tx.peerPaused = peerNow
		if localNow || peerNow {
			a.tx.speedBPS = 0
			if a.tx.stage != "verifying" {
				if localNow {
					a.tx.stage = "paused"
				} else {
					a.tx.stage = "peer_paused"
				}
			}
		}
	}
	a.mu.Unlock()
	if !matched {
		return
	}
	ctrl.SetOnPauseState(func(localPaused, peerPaused bool) {
		a.mu.Lock()
		changed := a.tx != nil && a.tx.id == id
		if changed {
			wasAnyPaused := a.tx.paused || a.tx.peerPaused
			a.tx.paused = localPaused
			a.tx.peerPaused = peerPaused
			if a.tx.stage != "verifying" {
				switch {
				case localPaused:
					a.tx.stage = "paused"
				case peerPaused:
					a.tx.stage = "peer_paused"
				default:
					if a.tx.stage == "paused" || a.tx.stage == "peer_paused" {
						a.tx.stage = "transferring"
					}
				}
			}
			if localPaused || peerPaused {
				a.tx.speedBPS = 0
			} else if wasAnyPaused {
				a.tx.samples = []txSpeedSample{{t: time.Now(), bytes: a.tx.bytesDone}}
			}
			a.lastPaint = time.Now()
		}
		a.mu.Unlock()
		if changed {
			if h := existingMainWindow(); h != 0 {
				procPostMessageW.Call(uintptr(h), wmAppPaint, 0, 0)
			}
		}
	})
	if h := existingMainWindow(); h != 0 {
		procPostMessageW.Call(uintptr(h), wmAppPaint, 0, 0)
	}
}

func (a *app) togglePauseTransfer() bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	var ctrl *transfer.Controller
	allowed := a.tx != nil && a.tx.ctrl != nil && a.tx.canPause && a.tx.stage != "verifying"
	if allowed {
		ctrl = a.tx.ctrl
	}
	a.mu.Unlock()
	if !allowed {
		return false
	}
	if !ctrl.IsPaused() && ctrl.IsPeerPaused() {
		return false
	}
	ctrl.TogglePause()
	return true
}

func (a *app) cancelTransfer() bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	var ctrl *transfer.Controller
	allowed := a.tx != nil && a.tx.ctrl != nil && a.tx.canCancel
	if allowed {
		ctrl = a.tx.ctrl
		a.tx.stage = "cancelling"
		a.tx.speedBPS = 0
		a.tx.paused = false
		a.tx.peerPaused = false
		a.lastPaint = time.Now()
	}
	a.mu.Unlock()
	if !allowed {
		return false
	}
	ctrl.Cancel()
	if h := existingMainWindow(); h != 0 {
		procPostMessageW.Call(uintptr(h), wmAppPaint, 0, 0)
	}
	return true
}

// deviceID returns the short id of this device, or "" when the key is not
// loaded yet (the main window shows the port instead).
func (a *app) deviceID() string {
	if a.keyPath == "" {
		return ""
	}
	k, err := identity.LoadKey(a.keyPath)
	if err != nil {
		return ""
	}
	id := k.ID()
	return fmt.Sprintf("%x", id[:6])
}

// note records a one-line summary for the main window's "最近传输" list.
func (a *app) note(line string) {
	a.mu.Lock()
	a.recent = append([]string{line}, a.recent...)
	if len(a.recent) > 8 {
		a.recent = a.recent[:8]
	}
	a.mu.Unlock()
}

func (a *app) recentLines(n int) []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if n > len(a.recent) {
		n = len(a.recent)
	}
	out := make([]string, n)
	copy(out, a.recent[:n])
	return out
}

func newApp(dir string, port int) *app { return &app{dir: dir, port: port} }

// localAppData returns %LOCALAPPDATA%, where the device key and peer table live.
func localAppData() (string, error) {
	if d := os.Getenv("LOCALAPPDATA"); d != "" {
		return d, nil
	}
	return "", fmt.Errorf("LOCALAPPDATA is not set")
}

// defaultStateDir delegates to client.DefaultStateDir so GUI, CLI, and MCP
// share %LOCALAPPDATA%\Ferry (or ~/.ferry) cleanly.
func defaultStateDir() string {
	return client.DefaultStateDir()
}

// defaultPairedPath keeps the trusted-device table next to the device key.
func defaultPairedPath() string {
	return filepath.Join(defaultStateDir(), "paired.json")
}

// event appends one JSON line to the log file (if any) and stderr.
func (a *app) event(kind string, kv map[string]any) {
	line := fmt.Sprintf(`{"event":%q,"ts":%q`, kind, time.Now().Format(time.RFC3339))
	for k, v := range kv {
		switch x := v.(type) {
		case string:
			line += fmt.Sprintf(`,"%s":%q`, k, x)
		case int:
			line += fmt.Sprintf(`,"%s":%d`, k, x)
		case int64:
			line += fmt.Sprintf(`,"%s":%d`, k, x)
		case bool:
			line += fmt.Sprintf(`,"%s":%t`, k, x)
		case []string:
			line += fmt.Sprintf(`,"%s":["%s"]`, k, strings.Join(x, `","`))
		}
	}
	line += "}\n"
	if a.logOut != nil {
		a.logOut.WriteString(line)
	}
	fmt.Fprint(os.Stderr, line)
}

// humanBytes keeps the ledger readable without pulling in a formatter.
func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

// dumpStacks is a debugging aid for hangs: with FT_DEBUG_STACKS=1 the process
// writes all goroutine stacks to the file named there after a delay.
func dumpStacks() {
	path := os.Getenv("FT_DEBUG_STACKS")
	if path == "" {
		return
	}
	go func() {
		time.Sleep(8 * time.Second)
		f, err := os.Create(path)
		if err != nil {
			return
		}
		defer f.Close()
		pprof.Lookup("goroutine").WriteTo(f, 1)
	}()
}
