package main

import (
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cwlindean/filetransfer/core/identity"
)

// defaultPairedPath is ~/.ft/paired.json.
func defaultPairedPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "paired.json"
	}
	return filepath.Join(home, ".ft", "paired.json")
}

// runPair prints this device's pairing URI, imports a peer's URI, or lists the
// trusted peers. The URI is the payload a QR code would carry (protocol §8.6);
// the scanner proves the one-time secret in-band once pairing lands (M6).
func runPair(args []string) int {
	// The one-time-code flow has its own flag set; keep `ft pair --show/--add`
	// unchanged for QR based pairing.
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
			fmt.Fprintf(os.Stderr, "ft: %v\n", err)
			return 1
		}
		secret := make([]byte, identity.PairSecretLen)
		if _, err := rand.Read(secret); err != nil {
			fmt.Fprintf(os.Stderr, "ft: read random: %v\n", err)
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
			fmt.Fprintf(os.Stderr, "ft: %v\n", err)
			return 1
		}
		store, err := identity.LoadPaired(*pairedPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "ft: %v\n", err)
			return 1
		}
		if err := store.Add(peer); err != nil {
			fmt.Fprintf(os.Stderr, "ft: %v\n", err)
			return 1
		}
		if err := store.Save(); err != nil {
			fmt.Fprintf(os.Stderr, "ft: save paired table: %v\n", err)
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
			fmt.Fprintf(os.Stderr, "ft: %v\n", err)
			return 1
		}
		devices := store.List()
		emit(map[string]any{"ok": true, "action": "list", "devices": devices})
		return 0

	default:
		fmt.Fprintln(os.Stderr, "ft: pair needs --show, --add <uri> or --list")
		return 2
	}
}
