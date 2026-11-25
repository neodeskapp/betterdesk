package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/neodeskapp/neodesk/neodesk-server/console/updater"
)

const verifyReleaseUsage = "verify-release --manifest FILE --signature FILE [--name ASSET] BINARY   check a downloaded binary against its release's signed manifest\n" +
	"       neodesk-server verify-release --sums FILE --signature FILE [--name NAME] FILE   check an installer against its release's signed SHA256SUMS"

// runVerifyRelease is "verify-release": it checks a downloaded binary
// against the manifest of its release and the manifest's signature with
// the release key compiled into this binary (updater.VerifyFile). ASSET
// is the binary's name in the manifest (neodesk-server-linux-amd64),
// the file's own name by default. With --sums instead of --manifest it
// checks an installer or compose file of a release against the release's
// SHA256SUMS and that list's signature, SHA256SUMS.sig
// (updater.SumsPayload). Exit status: 0 verified, 1 refused, 2 a usage
// error.
func runVerifyRelease(args []string, _ io.Reader, stdout, stderr io.Writer) int {
	return verifyRelease(args, stdout, stderr, updater.CompiledKeys())
}

func verifyRelease(args []string, stdout, stderr io.Writer, keys updater.Keys) int {
	usage := func() int {
		fmt.Fprintln(stderr, "Usage: neodesk-server "+verifyReleaseUsage)
		return 2
	}
	var manifestPath, sumsPath, signaturePath, name, binary string
	for i := 0; i < len(args); i++ {
		switch a := args[i]; a {
		case "--manifest", "--sums", "--signature", "--name":
			if i+1 >= len(args) || args[i+1] == "" {
				return usage()
			}
			i++
			switch a {
			case "--manifest":
				manifestPath = args[i]
			case "--sums":
				sumsPath = args[i]
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
	if (manifestPath == "") == (sumsPath == "") || signaturePath == "" || binary == "" {
		return usage()
	}
	if name == "" {
		name = filepath.Base(binary)
	}
	refuse := func(err error) int {
		fmt.Fprintf(stderr, "neodesk-server verify-release: %v\n", err)
		return 1
	}
	if sumsPath != "" {
		if err := verifyListed(sumsPath, signaturePath, binary, name, keys); err != nil {
			return refuse(err)
		}
		fmt.Fprintf(stdout, "verified: %s, by the release's signed SHA256SUMS\n", name)
		return 0
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

// verifyListed checks that file is the release file name that the
// release's SHA256SUMS (sumsPath) lists, and that the list is signed by
// one of the keys (signaturePath, SHA256SUMS.sig).
func verifyListed(sumsPath, signaturePath, file, name string, keys updater.Keys) error {
	sums, err := os.ReadFile(sumsPath)
	if err != nil {
		return err
	}
	signature, err := os.ReadFile(signaturePath)
	if errors.Is(err, fs.ErrNotExist) {
		return errors.New("the release has no signature of its SHA256SUMS (SHA256SUMS.sig)")
	} else if err != nil {
		return err
	}
	if !updater.VerifySignature(updater.SumsPayload(sums), signature, keys) {
		return errors.New("the signature of SHA256SUMS does not verify against the release key")
	}
	want := updater.SumOf(sums, name)
	if want == "" {
		return fmt.Errorf("SHA256SUMS of the release does not list %s", name)
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	if got := sha256.Sum256(data); hex.EncodeToString(got[:]) != want {
		return fmt.Errorf("%s is not the file the release's SHA256SUMS names", name)
	}
	return nil
}
