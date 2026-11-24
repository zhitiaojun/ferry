package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	qrcode "github.com/skip2/go-qrcode"

	"github.com/cwlindean/filetransfer/core/client"
	"github.com/cwlindean/filetransfer/core/lanweb"
	"github.com/cwlindean/filetransfer/core/logging"
	"github.com/cwlindean/filetransfer/core/tempshare"
)

func runShare(args []string) int {
	if len(args) > 0 && args[0] == "join" {
		return runShareJoin(args[1:])
	}

	fs := flag.NewFlagSet("share", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var sendFiles fileList
	fs.Var(&sendFiles, "send", "file to share (repeatable)")
	recvMode := fs.Bool("recv", false, "wait for peer or browser to upload files")
	base := fs.String("base", client.DefaultWebBaseURL, "public site base URL")
	out := fs.String("out", ".", "receive directory in recv mode")
	dir := fs.String("dir", "", "share all top-level files in this directory")
	name := fs.String("name", defaultName(), "device name shown to peers")
	keyPath := fs.String("key", defaultKeyPath(), "device key file")
	logPath := fs.String("log", "", "JSONL log file")
	qrFlag := fs.Bool("qr", true, "print an ASCII QR code to stderr")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	for _, extra := range fs.Args() {
		sendFiles = append(sendFiles, extra)
	}
	if *dir != "" {
		entries, err := os.ReadDir(*dir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ferry share: %v\n", err)
			return 1
		}
		for _, e := range entries {
			if !e.IsDir() {
				sendFiles = append(sendFiles, filepath.Join(*dir, e.Name()))
			}
		}
	}

	mode := "recv"
	if len(sendFiles) > 0 && !*recvMode {
		mode = "send"
	} else if len(sendFiles) > 0 && *recvMode {
		mode = "both"
	}
	if mode != "send" {
		if err := os.MkdirAll(*out, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "ferry share: %v\n", err)
			return 1
		}
	}

	var logger *logging.Logger
	if *logPath != "" {
		var closer interface{ Close() error }
		var err error
		logger, closer, err = logging.OpenRotating(*logPath, logging.DefaultMaxBytes)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ferry share: %v\n", err)
			return 1
		}
		defer closer.Close()
	}

	key, err := loadDeviceKey(*keyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ferry share: %v\n", err)
		return 1
	}

	host, err := tempshare.StartHost(tempshare.HostOptions{
		BaseURL:     *base,
		Key:         key,
		DeviceName:  *name,
		Mode:        mode,
		SharedFiles: sendFiles,
		RecvDir:     *out,
		Log:         logger,
		OnUploaded: func(f lanweb.File) {
			fmt.Fprintf(os.Stderr, "已接收：%s（%d 字节）\n", f.Name, f.Size)
		},
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ferry share: %v\n", err)
		return 1
	}
	defer host.Close()

	st := host.Status()
	_ = json.NewEncoder(os.Stdout).Encode(st)
	fmt.Fprintf(os.Stderr, "\n临时快传（%s）\n6 位码：%s\n链接：%s\n", st.Mode, st.Code, st.ShareURL)
	if st.LANURL != "" {
		fmt.Fprintf(os.Stderr, "局域网：%s\n", st.LANURL)
	}
	if *qrFlag && st.ShareURL != "" {
		if qr, qerr := qrcode.New(st.ShareURL, qrcode.Medium); qerr == nil {
			fmt.Fprintln(os.Stderr, qr.ToSmallString(false))
		}
	}
	fmt.Fprintln(os.Stderr, "按 Ctrl+C 结束临时快传…")

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	return 0
}

func runShareJoin(args []string) int {
	fs := flag.NewFlagSet("share join", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	base := fs.String("base", client.DefaultWebBaseURL, "public site base URL")
	out := fs.String("out", ".", "destination directory when downloading")
	var files fileList
	fs.Var(&files, "file", "file to upload when joining a recv session (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "ferry share join: missing 6-digit code or https://.../s/<token> URL")
		return 2
	}
	target := strings.TrimSpace(fs.Arg(0))

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var sess *tempshare.ResolvedSession
	var err error
	if strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://") {
		sess, err = tempshare.ResolveByURL(ctx, target)
	} else {
		sess, err = tempshare.ResolveByCode(ctx, *base, target)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "ferry share join: %v\n", err)
		return 1
	}

	if sess.Mode == "send" {
		if err := os.MkdirAll(*out, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "ferry share join: %v\n", err)
			return 1
		}
		via, saved, err := tempshare.DownloadAll(context.Background(), sess, *out)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ferry share join: %v\n", err)
			return 1
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
			"ok": true, "mode": "download", "via": via, "peer": sess.Name, "files": saved,
		})
		return 0
	}

	for _, extra := range fs.Args()[1:] {
		files = append(files, extra)
	}
	if len(files) == 0 {
		fmt.Fprintf(os.Stderr, "ferry share join: peer %q is waiting for uploads; pass --file <path>\n", sess.Name)
		return 2
	}
	via, uploaded, err := tempshare.UploadFiles(context.Background(), sess, files)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ferry share join: %v\n", err)
		return 1
	}
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{
		"ok": true, "mode": "upload", "via": via, "peer": sess.Name, "files": uploaded,
	})
	return 0
}
