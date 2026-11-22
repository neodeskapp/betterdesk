package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/unitronix/betterdesk-server/console/updater"
)

const verifyReleaseUsage = "verify-release --manifest FILE --signature FILE [--name ASSET] BINARY   check a downloaded binary against its release's signed manifest"

// runVerifyRelease is "verify-release": it checks a downloaded binary
// against the manifest of its release and the manifest's signature with
// the release key compiled into this binary (updater.VerifyFile). ASSET
// is the binary's name in the manifest (betterdesk-server-linux-amd64),
// the file's own name by default. Exit status: 0 verified, 1 refused, 2 a
// usage error.
func runVerifyRelease(args []string, _ io.Reader, stdout, stderr io.Writer) int {
	return verifyRelease(args, stdout, stderr, updater.CompiledKeys())
}

func verifyRelease(args []string, stdout, stderr io.Writer, keys updater.Keys) int {
	usage := func() int {
		fmt.Fprintln(stderr, "Usage: betterdesk-server "+verifyReleaseUsage)
		return 2
	}
	var manifestPath, signaturePath, name, binary string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--manifest", "--signature", "--name":
			if i+1 >= len(args) || args[i+1] == "" {
				return usage()
			}
			i++
			switch a {
			case "--manifest":
				manifestPath = args[i]
			case "--signature":
				signaturePath = args[i]
			default:
				name = args[i]
			}
		default:
			if binary != "" || a == "" || a[0] == '-' {
				return usage()
			}
			binary = a
		}
	}
	if manifestPath == "" || signaturePath == "" || binary == "" {
		return usage()
	}
	if name == "" {
		name = filepath.Base(binary)
	}
	refuse := func(err error) int {
		fmt.Fprintf(stderr, "betterdesk-server verify-release: %v\n", err)
		return 1
	}
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return refuse(err)
	}
	signature, err := os.ReadFile(signaturePath)
	if errors.Is(err, fs.ErrNotExist) {
		signature = nil
	} else if err != nil {
		return refuse(err)
	}
	m, err := updater.VerifyFile(manifest, signature, binary, name, keys)
	if err != nil {
		return refuse(err)
	}
	fmt.Fprintf(stdout, "verified: %s %s (%s), %s\n", updater.Product, m.Version, m.SHA[:7], name)
	return 0
}
