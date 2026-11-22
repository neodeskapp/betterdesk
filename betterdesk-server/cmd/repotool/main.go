// Command repotool holds the repository's own checks and maintenance
// commands, which CI and the local gate run. It is a development tool and
// not part of the server binary.
//
//	go run ./cmd/repotool <command> [flags]
//	go run ./cmd/repotool --root DIR <command> [flags]
//
// It works on the repository that holds the current folder (the first
// folder upwards with betterdesk-server/go.mod in it), or on --root.
// Each command exits with 0 when it passed, 1 when it found something and
// 2 when it was called wrongly. "go run ./cmd/repotool help" lists the
// commands.
package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

// command is one command of repotool. run takes the repository's root
// and the arguments after the command's name and returns the exit status.
type command struct {
	about string
	run   func(root string, args []string, stdout, stderr io.Writer) int
}

// commands are the commands of repotool by name. Add a command here. They
// are set in init because the local gate runs other commands through run.
var commands map[string]command

func init() {
	commands = map[string]command{
		"check-provenance": {
			about: "check the clean-room provenance documents",
			run:   checkProvenance,
		},
		"check-pairing-i18n": {
			about: "check that every console language has the pairing keys",
			run:   checkPairingI18n,
		},
		"check-port-docs": {
			about: "fail on scripts and Go files that present port 21120, which never shipped, as live",
			run:   checkPortDocs,
		},
		"check-js": {
			about: "fail on tracked JavaScript outside .github/javascript-allowlist.txt",
			run:   checkJS,
		},
		"bump-version": {
			about: "check or bump the product version [--verify | --sync | --patch | --minor | --set X.Y.Z] [--dry-run] [--list-paths]",
			run:   bumpVersion,
		},
		"i18n-check": {
			about: "audit the translation files [--system ID] [--json] [--verbose]",
			run:   i18nCheck,
		},
		"local-gate": {
			about: "the complete local check before a commit [--dry-run] [--preflight-only] [--report-dir DIR]",
			run:   localGate,
		},
		"docker-smoke": {
			about: "test the local image [--database-type sqlite|postgres] [--database-url URL] [--keep]",
			run:   dockerSmoke,
		},
	}
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	root := ""
	if len(args) >= 2 && args[0] == "--root" {
		root, args = args[1], args[2:]
	}
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	name, rest := args[0], args[1:]
	if name == "help" || name == "-h" || name == "--help" {
		usage(stdout)
		return 0
	}
	cmd, ok := commands[name]
	if !ok {
		fmt.Fprintf(stderr, "repotool: unknown command %q\n", name)
		usage(stderr)
		return 2
	}
	if root == "" {
		var err error
		if root, err = findRoot(); err != nil {
			fmt.Fprintln(stderr, "repotool:", err)
			return 2
		}
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintln(stderr, "repotool:", err)
		return 2
	}
	return cmd.run(abs, rest, stdout, stderr)
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: repotool [--root DIR] <command> [flags]")
	fmt.Fprintln(w)
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		fmt.Fprintf(w, "  %-20s %s\n", name, commands[name].about)
	}
}

// findRoot is the first folder from the current one upwards that holds
// betterdesk-server/go.mod.
func findRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "betterdesk-server", "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no repository here: no folder upwards holds betterdesk-server/go.mod (use --root)")
		}
		dir = parent
	}
}
