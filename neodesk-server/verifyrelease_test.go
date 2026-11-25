package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/updater"
)

// TestVerifyReleaseCommand: verify-release passes a binary of a release
// signed with the key and refuses a changed binary and a release without
// its signature file, with the exit statuses the installers read (L92).
func TestVerifyReleaseCommand(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	binary := []byte("a new neodesk-server")
	sum := sha256.Sum256(binary)
	m := updater.Manifest{SchemaVersion: 1, Product: updater.Product, Version: "3.6.0", Channel: "stable", Created: "2026-11-22T10:00:00Z",
		Owner: "neodeskapp", Repo: "neodesk", Branch: "main", SHA: strings.Repeat("c", 40),
		Files: []updater.ManifestFile{{Path: "neodesk-server-linux-amd64", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(binary))}}}
	text, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(private, updater.CanonicalPayload(m.Signed()))) + "\n"
	write := func(name string, data []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	manifest, signature := write("manifest.json", text), write("manifest.sig", []byte(sig))
	downloaded := write("neodesk-server.new", binary)
	changed := write("changed", append([]byte("A"), binary[1:]...))
	keys := updater.Keys{public}

	run := func(args ...string) (int, string, string) {
		var out, errOut strings.Builder
		code := verifyRelease(args, &out, &errOut, keys)
		return code, out.String(), errOut.String()
	}
	if code, out, errOut := run("--manifest", manifest, "--signature", signature, "--name", "neodesk-server-linux-amd64", downloaded); code != 0 ||
		out != "verified: neodesk-server 3.6.0 (ccccccc), neodesk-server-linux-amd64\n" {
		t.Errorf("a signed binary: %d %q %q", code, out, errOut)
	}
	if code, _, errOut := run("--manifest", manifest, "--signature", signature, "--name", "neodesk-server-linux-amd64", changed); code != 1 || errOut == "" {
		t.Errorf("a changed binary: %d %q", code, errOut)
	}
	if code, _, errOut := run("--manifest", manifest, "--signature", filepath.Join(dir, "none.sig"), "--name", "neodesk-server-linux-amd64", downloaded); code != 1 ||
		!strings.Contains(errOut, "no signature") {
		t.Errorf("no signature file: %d %q", code, errOut)
	}
	if code, _, _ := run("--manifest", manifest, downloaded); code != 2 {
		t.Errorf("without --signature: %d, want the usage", code)
	}
	if _, ok := subcommands["verify-release"]; !ok {
		t.Error("verify-release is not a command")
	}
}

// verify-release --sums checks an installer the scripts take from a
// release (neodesk.sh, neodesk.ps1) against the release's SHA256SUMS and
// its signature, SHA256SUMS.sig: a listed file with a valid signature
// passes; a changed file, a name the list lacks, a list signed with
// another key or without its signature are refused.
func TestVerifyReleaseChecksAnInstallerAgainstTheSignedFileList(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, other, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	write := func(name string, data []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	script := []byte("#!/usr/bin/env bash\nNEODESK_SH_REVISION=x\n")
	sum := sha256.Sum256(script)
	list := []byte(hex.EncodeToString(sum[:]) + "  neodesk.sh\n")
	sums := write("SHA256SUMS", list)
	signed := write("SHA256SUMS.sig", []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(private, updater.SumsPayload(list)))+"\n"))
	byOther := write("other.sig", []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(other, updater.SumsPayload(list)))+"\n"))
	downloaded := write("neodesk.sh.new", script)
	changed := write("changed", append([]byte("#"), script...))
	run := func(args ...string) (int, string, string) {
		var out, errOut strings.Builder
		code := verifyRelease(args, &out, &errOut, updater.Keys{public})
		return code, out.String(), errOut.String()
	}
	if code, out, errOut := run("--sums", sums, "--signature", signed, "--name", "neodesk.sh", downloaded); code != 0 || out != "verified: neodesk.sh, by the release's signed SHA256SUMS\n" {
		t.Errorf("a listed installer: %d %q %q", code, out, errOut)
	}
	for what, args := range map[string][]string{
		"a changed installer":     {"--sums", sums, "--signature", signed, "--name", "neodesk.sh", changed},
		"a name the list lacks":   {"--sums", sums, "--signature", signed, "--name", "neodesk.ps1", downloaded},
		"another key's signature": {"--sums", sums, "--signature", byOther, "--name", "neodesk.sh", downloaded},
		"no signature file":       {"--sums", sums, "--signature", filepath.Join(dir, "none.sig"), "--name", "neodesk.sh", downloaded},
	} {
		if code, _, errOut := run(args...); code != 1 || errOut == "" {
			t.Errorf("%s: %d %q", what, code, errOut)
		}
	}
	if code, _, _ := run("--sums", sums, "--manifest", sums, "--signature", signed, downloaded); code != 2 {
		t.Errorf("--sums with --manifest: %d, want the usage", code)
	}
	if !strings.Contains(verifyReleaseUsage, "verify-release --sums FILE") {
		t.Error("the usage, which the installers look for, does not name --sums")
	}
}
