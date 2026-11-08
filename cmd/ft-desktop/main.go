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
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"strings"
	"sync"
	"time"

	"github.com/cwlindean/filetransfer/core/identity"
)

var (
	flagDrop   = flag.Bool("drop", false, "show the drop window instead of the tray receiver")
	flagDir    = flag.String("dir", "", "receive directory (default: %USERPROFILE%\\Downloads\\ft)")
	flagPort   = flag.Int("port", 18800, "receive port on all interfaces")
	flagPos    = flag.String("pos", "", "drop window position X,Y (drop mode only)")
	flagSize   = flag.String("size", "260x90", "drop window size WxH (drop mode only)")
	flagLog    = flag.String("log", "", "append JSONL events to this file")
	flagKey    = flag.String("key", "", "device key file (default: %LOCALAPPDATA%\\ft\\device.key)")
	flagPair   = flag.String("paired", "", "trusted device table (default: %LOCALAPPDATA%\\ft\\paired.json)")
	flagSig    = flag.String("signal", "wss://ft.cwlindean.cn/signal", "signaling URL used to send via the relay")
	flagSend   = flag.String("send", "", "diagnostic: send these comma-separated files to the paired device, then exit")
	flagAuto   = flag.String("startup", "", "diagnostic: on/off/status for the HKCU Run entry, then exit")
	flagMain   = flag.Bool("main", false, "open the main window at startup")
	flagSendTo = flag.Bool("install-sendto", false, "install the Explorer \"Send to\" shortcut, then exit")
)

func main() {
	flag.Parse()

	dir := *flagDir
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fatal("cannot find home directory: %v", err)
		}
		dir = filepath.Join(home, "Downloads", "ft")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fatal("create receive directory: %v", err)
	}

	app := newApp(dir, *flagPort)
	app.pairedPath = *flagPair
	if app.pairedPath == "" {
		app.pairedPath = defaultPairedPath()
	}
	app.signalURL = *flagSig
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
		if d, err := localAppData(); err == nil {
			_ = os.MkdirAll(filepath.Join(d, "ft"), 0o755)
			if f, err := os.OpenFile(filepath.Join(d, "ft", "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
				app.logOut = f
			}
		}
	}
	if len(files) > 0 && *flagSend == "" {
		res, peer, err := sendViaRelay(app, files)
		if err != nil {
			app.event("desktop.send_error", map[string]any{"error": err.Error()})
			fmt.Fprintln(os.Stderr, "send:", err)
			os.Exit(1)
		}
		fmt.Printf("{\"ok\":true,\"to\":%q,\"bytes\":%d,\"chunks\":%d}\n", peer.Name, res.Bytes, res.Chunks)
		return
	}
	if *flagSend != "" {
		res, peer, err := sendViaRelay(app, files)
		if err != nil {
			app.event("desktop.send_error", map[string]any{"error": err.Error()})
			fmt.Fprintln(os.Stderr, "send:", err)
			os.Exit(1)
		}
		fmt.Printf("{\"ok\":true,\"to\":%q,\"bytes\":%d,\"chunks\":%d}\n", peer.Name, res.Bytes, res.Chunks)
		return
	}

	app.showMain = *flagMain
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

// app holds state shared by tray and drop modes.
type app struct {
	dir        string
	port       int
	keyPath    string
	pairedPath string
	signalURL  string
	logOut     *os.File
	showMain   bool

	mu     sync.Mutex
	recent []string
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

// defaultPairedPath keeps the trusted-device table next to the device key.
func defaultPairedPath() string {
	if d, err := localAppData(); err == nil {
		return filepath.Join(d, "ft", "paired.json")
	}
	return "paired.json"
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
