package cli

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"
)

func runCert(t *testing.T, args ...string) (int, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	env := Env{Stdout: &out, Stderr: &errOut, Getenv: func(string) string { return "" },
		Now: func() time.Time { return time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC) }, GOOS: runtime.GOOS}
	code := Cert(args, env)
	return code, out.String() + errOut.String()
}

func readCert(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("%s holds no certificate", path)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

// L66: the binary makes the self-signed certificate for both installers:
// valid for the hosts given and for this machine, ten years from now, the
// key readable by its owner only, an existing pair kept without --force.
func TestCertMakesASelfSignedPair(t *testing.T) {
	dir := t.TempDir()
	code, out := runCert(t, "--out", dir, "--host", "203.0.113.7", "--host", "desk.example.org")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	crtPath, keyPath := filepath.Join(dir, "betterdesk.crt"), filepath.Join(dir, "betterdesk.key")
	if _, err := tls.LoadX509KeyPair(crtPath, keyPath); err != nil {
		t.Fatalf("the key does not belong to the certificate: %v", err)
	}
	cert := readCert(t, crtPath)
	if cert.Subject.CommonName != "203.0.113.7" || cert.Issuer.String() != cert.Subject.String() {
		t.Errorf("subject %q, issuer %q", cert.Subject, cert.Issuer)
	}
	for _, want := range []string{"desk.example.org", "localhost"} {
		if !slices.Contains(cert.DNSNames, want) {
			t.Errorf("no DNS name %s in %v", want, cert.DNSNames)
		}
	}
	for _, want := range []string{"203.0.113.7", "127.0.0.1"} {
		if !slices.ContainsFunc(cert.IPAddresses, func(ip net.IP) bool { return ip.Equal(net.ParseIP(want)) }) {
			t.Errorf("no IP address %s in %v", want, cert.IPAddresses)
		}
	}
	if days := cert.NotAfter.Sub(time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)).Hours() / 24; days < 3649 || days > 3651 {
		t.Errorf("valid for %.0f days", days)
	}
	if !slices.Contains(cert.ExtKeyUsage, x509.ExtKeyUsageServerAuth) || cert.IsCA {
		t.Errorf("key usage %v, CA %v", cert.ExtKeyUsage, cert.IsCA)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(keyPath); fi.Mode().Perm() != 0o600 {
			t.Errorf("the key has mode %v", fi.Mode().Perm())
		}
	}

	before, _ := os.ReadFile(crtPath)
	if code, out := runCert(t, "--out", dir, "--host", "198.51.100.1"); code != 1 {
		t.Errorf("an existing pair was not refused: exit %d: %s", code, out)
	}
	if after, _ := os.ReadFile(crtPath); !bytes.Equal(before, after) {
		t.Error("a refused run changed the certificate")
	}
	if code, out := runCert(t, "--out", dir, "--host", "198.51.100.1", "--force"); code != 0 {
		t.Fatalf("--force: exit %d: %s", code, out)
	}
	if cert := readCert(t, crtPath); cert.Subject.CommonName != "198.51.100.1" {
		t.Errorf("--force kept the old certificate (%s)", cert.Subject.CommonName)
	}
	if code, _ := runCert(t, "--host", "x"); code != 2 {
		t.Errorf("no --out: exit %d, want 2", code)
	}
}
