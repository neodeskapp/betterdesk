// Command release-manifest writes the files that make a folder of
// neodesk-server binaries a release the in-app updater installs
// (console/updater): manifest.json, manifest.sig when a signing key is
// given, and SHA256SUMS in the form "sha256sum -c" reads.
//
//	go run ./cmd/release-manifest -dir dist -version 3.6.0 -commit <sha> -branch main
//
// The signing key is in NEODESK_RELEASE_SIGNING_KEY: a PKCS#8 PEM
// private key, as "openssl genpkey -algorithm ed25519" writes it, or the
// ed25519 seed (32 bytes) or private key (64 bytes) in base64; it is never
// taken from the command line. The written signature must verify against
// the release keys compiled into the updater (updater.CompiledKeys) and
// -verify-key (public keys in base64, comma separated), as the updater
// checks it. While a key is compiled in, a release without a signing key
// is refused: no install would take it.
//
// With -check the command writes nothing: it checks an existing release
// folder (the manifest's form, every binary's SHA-256 and size, and the
// signature against the compiled keys and -verify-key).
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/updater"
)

var reBinary = regexp.MustCompile(`^neodesk-server-([a-z0-9]+)-([a-z0-9]+)(\.exe)?$`)

func main() {
	if err := run(os.Args[1:], os.Getenv, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "release-manifest:", err)
		os.Exit(1)
	}
}

func run(args []string, getenv func(string) string, out io.Writer) error {
	fs := flag.NewFlagSet("release-manifest", flag.ContinueOnError)
	dir := fs.String("dir", "dist", "folder with the binaries; the files are written there")
	version := fs.String("version", "", "version of the release, X.Y.Z")
	commit := fs.String("commit", "", "commit the binaries were built from, 40 hex digits")
	branch := fs.String("branch", "main", "branch of the channel: main (stable) or dev (development)")
	owner := fs.String("owner", "neodeskapp", "GitHub owner the release is published under")
	repo := fs.String("repo", "neodesk", "GitHub repository the release is published in")
	minRollback := fs.String("min-rollback", "", "oldest version this release can be rolled back to (optional)")
	created := fs.String("created", "", "time of the release, RFC 3339 (default: now)")
	verifyKey := fs.String("verify-key", "", "public keys to check the signature against, base64, comma separated")
	check := fs.Bool("check", false, "check the release folder instead of writing it")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *check {
		return checkRelease(*dir, *owner, *repo, *branch, *verifyKey, out)
	}
	if *created == "" {
		*created = time.Now().UTC().Format(time.RFC3339)
	}
	manifest := updater.Manifest{
		SchemaVersion: 1, Product: updater.Product, Version: *version, Channel: updater.ChannelOf(*branch),
		Created: *created, MinRollbackVersion: *minRollback, Owner: *owner, Repo: *repo, Branch: *branch,
		SHA: strings.ToLower(*commit),
	}
	entries, err := os.ReadDir(*dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		m := reBinary.FindStringSubmatch(e.Name())
		if m == nil || !e.Type().IsRegular() {
			continue
		}
		sum, size, err := hashFile(filepath.Join(*dir, e.Name()))
		if err != nil {
			return err
		}
		manifest.Files = append(manifest.Files, updater.ManifestFile{Path: e.Name(), SHA256: sum, Size: size, OS: m[1], Arch: m[2]})
	}
	sort.Slice(manifest.Files, func(i, j int) bool { return manifest.Files[i].Path < manifest.Files[j].Path })
	text, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	text = append(text, '\n')
	if _, err := updater.ParseManifest(text, updater.ManifestRules{Owner: *owner, Repo: *repo, Branch: *branch}); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(*dir, "manifest.json"), text, 0o644); err != nil {
		return err
	}
	written := []string{"manifest.json"}

	payload := updater.CanonicalPayload(manifest.Signed())
	compiled, extra := updater.CompiledKeys(), updater.ParseKeys(*verifyKey)
	if seed := strings.TrimSpace(getenv("NEODESK_RELEASE_SIGNING_KEY")); seed != "" {
		key, err := privateKey(seed)
		if err != nil {
			return err
		}
		sig := base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload)) + "\n"
		if len(compiled) > 0 && !updater.VerifySignature(payload, []byte(sig), compiled) {
			return errors.New("the signature does not verify against the release key compiled into the updater: the signing key is not the release key")
		}
		if *verifyKey != "" && !updater.VerifySignature(payload, []byte(sig), extra) {
			return errors.New("the signature does not verify against -verify-key")
		}
		if err := os.WriteFile(filepath.Join(*dir, "manifest.sig"), []byte(sig), 0o644); err != nil {
			return err
		}
		written = append(written, "manifest.sig")
		fmt.Fprintf(out, "signed with the key %s\n", base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey)))
	} else if len(compiled) > 0 || *verifyKey != "" {
		return errors.New("NEODESK_RELEASE_SIGNING_KEY is not set, and the updater refuses a release without a signature")
	} else {
		fmt.Fprintln(out, "NEODESK_RELEASE_SIGNING_KEY is not set: the release is not signed")
	}

	var sums strings.Builder
	names := append([]string{}, written...)
	for _, f := range manifest.Files {
		names = append(names, f.Path)
	}
	sort.Strings(names)
	for _, name := range names {
		sum, _, err := hashFile(filepath.Join(*dir, name))
		if err != nil {
			return err
		}
		sums.WriteString(sum + "  " + name + "\n")
	}
	if err := os.WriteFile(filepath.Join(*dir, "SHA256SUMS"), []byte(sums.String()), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(out, "wrote manifest.json (%d binaries), SHA256SUMS%s\n", len(manifest.Files), map[bool]string{true: ", manifest.sig"}[len(written) > 1])
	return nil
}

// privateKey reads the signing key: an ed25519 key in a PKCS#8 PEM block,
// or a seed of 32 bytes or a private key of 64 bytes in base64.
func privateKey(text string) (ed25519.PrivateKey, error) {
	if block, _ := pem.Decode([]byte(text)); block != nil {
		parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, errors.New("NEODESK_RELEASE_SIGNING_KEY is a PEM block but no PKCS#8 private key")
		}
		key, ok := parsed.(ed25519.PrivateKey)
		if !ok {
			return nil, errors.New("NEODESK_RELEASE_SIGNING_KEY is no ed25519 key")
		}
		return key, nil
	}
	raw, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		return nil, errors.New("NEODESK_RELEASE_SIGNING_KEY is not base64")
	}
	switch len(raw) {
	case ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(raw), nil
	case ed25519.PrivateKeySize:
		return ed25519.PrivateKey(raw), nil
	}
	return nil, fmt.Errorf("NEODESK_RELEASE_SIGNING_KEY has %d bytes, expected 32 or 64", len(raw))
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// checkRelease checks a release folder as the updater would.
func checkRelease(dir, owner, repo, branch, keys string, out io.Writer) error {
	text, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return err
	}
	m, err := updater.ParseManifest(text, updater.ManifestRules{Owner: owner, Repo: repo, Branch: branch})
	if err != nil {
		return err
	}
	for _, f := range m.Files {
		sum, size, err := hashFile(filepath.Join(dir, f.Path))
		if err != nil {
			return err
		}
		if sum != f.SHA256 || (f.Size != 0 && size != f.Size) {
			return fmt.Errorf("%s does not match the manifest", f.Path)
		}
	}
	var sig []byte
	if data, err := os.ReadFile(filepath.Join(dir, "manifest.sig")); err == nil {
		sig = data
	}
	payload := updater.CanonicalPayload(m.Signed())
	trust := updater.EvaluateTrust(payload, sig, updater.CompiledKeys(), false)
	if !trust.OK {
		return errors.New(trust.Reason)
	}
	if keys != "" && (sig == nil || !updater.VerifySignature(payload, sig, updater.ParseKeys(keys))) {
		return errors.New("the signature does not verify against -verify-key")
	}
	fmt.Fprintf(out, "release %s (%s) of %s/%s: %d binaries match, signature: %s\n", m.Version, m.SHA[:7], owner, repo, len(m.Files),
		map[bool]string{true: "verified", false: "not verified (" + trust.Code + ")"}[trust.Verified])
	return nil
}
