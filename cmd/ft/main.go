// Command ft is the filetransfer command-line client.
//
// At M0 it only reports build information; send/receive subcommands arrive
// with the M1 session and transfer implementation.
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"

	"github.com/cwlindean/filetransfer/core/proto"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "0.0.0-dev"

func main() {
	verbose := flag.Bool("version", false, "print version information and exit")
	flag.Parse()

	if *verbose {
		fmt.Printf("ft %s\n", version)
		fmt.Printf("protocol %d\n", proto.Version)
		fmt.Printf("go %s\n", runtime.Version())
		fmt.Printf("platform %s/%s\n", runtime.GOOS, runtime.GOARCH)
		return
	}

	fmt.Fprintln(os.Stderr, "ft: no subcommand implemented yet (M0 skeleton)")
	flag.Usage()
	os.Exit(2)
}
