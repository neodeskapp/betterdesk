package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/updater"
)

func releaseDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "neodesk-server-linux-amd64"), []byte("a binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func args(dir string) []string {
	return []string{"-dir", dir, "-version", "3.6.0", "-commit", strings.Repeat("d", 40), "-branch", "main",
		"-owner", "neodeskapp", "-repo", "neodesk", "-created", "2026-11-22T10:00:00Z"}
}

// TestReleaseNeedsTheReleaseKey: with the release key compiled into the
// updater, a release without a signing key, or signed with another key,
// is refused before manifest.sig is written; a PKCS#8 PEM key is read
// (L92).
func TestReleaseNeedsTheReleaseKey(t *testing.T) {
	dir := releaseDir(t)
	var out bytes.Buffer
	if err := run(args(dir), func(string) string { return "" }, &out); err == nil || !strings.Contains(err.Error(), "refuses a release without a signature") {
		t.Errorf("no signing key: %v", err)
	}

	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}))
	parsed, err := privateKey(keyPEM)
	if err != nil || !parsed.Equal(private) {
		t.Fatalf("the PEM key: %v", err)
	}
	getenv := func(k string) string {
		if k == "NEODESK_RELEASE_SIGNING_KEY" {
			return keyPEM
		}
		return ""
	}
	if err := run(args(dir), getenv, &out); err == nil || !strings.Contains(err.Error(), "not the release key") {
		t.Errorf("another key: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "manifest.sig")); err == nil {
		t.Error("manifest.sig was written with another key")
	}
}

// The installers and compose files the release workflow puts next to the
// binaries are release files too: SHA256SUMS lists them with the
// binaries and the manifest, SHA256SUMS.sig signs that list, and the
// check refuses a release whose installer was changed afterwards or whose
// list was signed by another key. The manifest stays the binaries' only,
// as installs of 3.6.0-rc.4 read it.
func TestTheInstallersAreReleaseFiles(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, other, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := releaseDir(t)
	for _, name := range append([]string{"manifest.json", "notes.txt"}, installerFiles...) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("the file "+name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeFileList(dir, []string{"manifest.json", "neodesk-server-linux-amd64"}, private); err != nil {
		t.Fatal(err)
	}
	sums, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range append([]string{"manifest.json", "neodesk-server-linux-amd64"}, installerFiles...) {
		if updater.SumOf(sums, name) == "" {
			t.Errorf("SHA256SUMS does not list %s:\n%s", name, sums)
		}
	}
	if updater.SumOf(sums, "notes.txt") != "" {
		t.Error("SHA256SUMS lists a file that is no release file")
	}
	sig, err := os.ReadFile(filepath.Join(dir, "SHA256SUMS.sig"))
	if err != nil || !updater.VerifySignature(updater.SumsPayload(sums), sig, updater.Keys{public}) {
		t.Fatalf("SHA256SUMS.sig does not verify: %v", err)
	}
	if err := checkFileList(dir, updater.Keys{public}); err != nil {
		t.Errorf("the check refuses the release: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "neodesk.sh"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := checkFileList(dir, updater.Keys{public}); err == nil || !strings.Contains(err.Error(), "neodesk.sh") {
		t.Errorf("a changed installer: %v", err)
	}
	if err := writeFileList(dir, []string{"manifest.json", "neodesk-server-linux-amd64"}, other); err != nil {
		t.Fatal(err)
	}
	if err := checkFileList(dir, updater.Keys{public}); err == nil || !strings.Contains(err.Error(), "SHA256SUMS.sig") {
		t.Errorf("a list signed by another key: %v", err)
	}
}
