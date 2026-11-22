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
)

func releaseDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "betterdesk-server-linux-amd64"), []byte("a binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func args(dir string) []string {
	return []string{"-dir", dir, "-version", "3.6.0", "-commit", strings.Repeat("d", 40), "-branch", "main",
		"-owner", "trybetterdesk", "-repo", "betterdesk", "-created", "2026-11-22T10:00:00Z"}
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
		if k == "BETTERDESK_RELEASE_SIGNING_KEY" {
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
