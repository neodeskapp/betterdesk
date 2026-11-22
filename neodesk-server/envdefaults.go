package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/unitronix/betterdesk-server/cli"
)

// applyEnvDefaultsFromOS gives the env file BETTERDESK_ENV_FILE names the
// keys of the built-in template it lacks (cli.ApplyEnvDefaults) and reads
// the file again, so this start has them. An update from the console's
// Updates page replaces only the binary; this brings the env file to what
// the installers' update leaves (DECISIONS A125). A file that cannot be
// read or written is reported on stderr and the start goes on.
func applyEnvDefaultsFromOS() {
	path := os.Getenv("BETTERDESK_ENV_FILE")
	if path == "" {
		return
	}
	added, err := cli.ApplyEnvDefaults(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARN: the new settings of this release were not added to the env file: %v\n", err)
		return
	}
	if len(added) > 0 {
		fmt.Fprintf(os.Stderr, "Added the settings of this release to %s: %s\n", path, strings.Join(added, ", "))
		loadEnvFileFromOS()
	}
}

// printsOnly reports whether the arguments ask only for the version or the
// usage, as the updater's and the installers' check of a new binary does:
// such a run changes no file.
func printsOnly(args []string) bool {
	for _, a := range args {
		switch a {
		case "-version", "--version", "-h", "-help", "--help":
			return true
		}
	}
	return false
}
