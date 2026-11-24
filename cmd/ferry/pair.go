package main

import (
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/cwlindean/filetransfer/core/client"
	"github.com/cwlindean/filetransfer/core/identity"
)

func defaultPairedPath() string { return client.DefaultPairedPath() }

// runPair prints this device's pairing URI, imports a peer's URI, or lists the
// trusted peers.
func runPair(args []string) int {
	for _, a := range args {
		if a == "--code" || a == "-code" || strings.HasPrefix(a, "--code=") || strings.HasPrefix(a, "-code=") {
			return runPairPake(args)
		}
	}
	fs := flag.NewFlagSet("pair", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	keyPath := fs.String("key", defaultKeyPath(), "device key file")
	show := fs.Bool("show", false, "print this device's pairing URI")
	add := fs.String("add", "", "import a peer from a pairing URI")
	name := fs.String("name", "", "device name carried in the URI")
	pairedPath := fs.String("paired", defaultPairedPath(), "trusted peer table")
	listFlag := fs.Bool("list", false, "list trusted peers")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	emit := func(v any) {
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(v)
	}

	switch {
	case *show:
		key, err := loadDeviceKey(*keyPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ferry: %v\n", err)
			return 1
		}
		secret := make([]byte, identity.PairSecretLen)
		if _, err := rand.Read(secret); err != nil {
			fmt.Fprintf(os.Stderr, "ferry: read random: %v\n", err)
			return 1
		}
		uri := identity.PairURI(key.ID(), key.Public(), *name, secret)
		emit(map[string]any{
			"ok":        true,
			"action":    "show",
			"device_id": key.IDHex(),
			"name":      *name,
			"uri":       uri,
		})
		return 0

	case *add != "":
		peer, secret, err := identity.ParsePairURI(*add)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ferry: %v\n", err)
			return 1
		}
		store, err := identity.LoadPaired(*pairedPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ferry: %v\n", err)
			return 1
		}
		if err := store.Add(peer); err != nil {
			fmt.Fprintf(os.Stderr, "ferry: %v\n", err)
			return 1
		}
		if err := store.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "ferry: save paired table: %v\n", err)
			return 1
		}
		emit(map[string]any{
			"ok":         true,
			"action":     "add",
			"device_id":  peer.DeviceID,
			"name":       peer.Name,
			"paired":     len(store.List()),
			"has_secret": len(secret) == identity.PairSecretLen,
		})
		return 0

	case *listFlag:
		store, err := identity.LoadPaired(*pairedPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ferry: %v\n", err)
			return 1
		}
		devices := store.List()
		emit(map[string]any{"ok": true, "action": "list", "devices": devices})
		return 0

	default:
		fmt.Fprintln(os.Stderr, "ferry: pair needs --show, --add <uri>, --list, or --code <6digits>")
		return 2
	}
}
