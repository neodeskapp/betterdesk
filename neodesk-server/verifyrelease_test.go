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
