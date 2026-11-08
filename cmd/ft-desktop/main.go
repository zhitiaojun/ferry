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
	"strings"
	"time"
)

var (
	flagDrop = flag.Bool("drop", false, "show the drop window instead of the tray receiver")
	flagDir  = flag.String("dir", "", "receive directory (default: %USERPROFILE%\\Downloads\\ft)")
	flagPort = flag.Int("port", 18800, "receive port on all interfaces")
	flagPos  = flag.String("pos", "", "drop window position X,Y (drop mode only)")
	flagSize = flag.String("size", "260x90", "drop window size WxH (drop mode only)")
	flagLog  = flag.String("log", "", "append JSONL events to this file")
	flagKey  = flag.String("key", "", "device key file (default: %LOCALAPPDATA%\\ft\\device.key)")
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
	if *flagLog != "" {
		f, err := os.OpenFile(*flagLog, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			fatal("open log: %v", err)
		}
		app.logOut = f
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
	dir    string
	port   int
	logOut *os.File
}

func newApp(dir string, port int) *app { return &app{dir: dir, port: port} }

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
