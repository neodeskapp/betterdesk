package config

import (
	"bytes"
	"log"
	"net"
	"strings"
	"testing"
)

func TestParseTrustedProxies(t *testing.T) {
	t.Parallel()
	nets, err := ParseTrustedProxies("127.0.0.1/32, 10.0.0.0/8, 2001:db8::1")
	if err != nil {
		t.Fatal(err)
	}
	if len(nets) != 3 {
		t.Fatalf("len=%d, want 3", len(nets))
	}
	if !nets[0].Contains(net.ParseIP("127.0.0.1")) {
		t.Fatal("expected 127.0.0.1 in first net")
	}
	if !nets[2].Contains(net.ParseIP("2001:db8::1")) {
		t.Fatal("expected bare IPv6 to become /128")
	}
}

func TestParseTrustedProxiesInvalid(t *testing.T) {
	t.Parallel()
	if _, err := ParseTrustedProxies("not-an-ip"); err == nil {
		t.Fatal("expected error")
	}
}

func TestShouldHonorForwardedHeaders(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		TrustProxy: true,
		TrustedProxies: []*net.IPNet{
			mustParseCIDR(t, "10.0.0.0/8"),
		},
	}
	if !cfg.ShouldHonorForwardedHeaders("10.0.0.2:50123") {
		t.Fatal("trusted proxy should honor headers")
	}
	if cfg.ShouldHonorForwardedHeaders("203.0.113.1:443") {
		t.Fatal("untrusted remote must not honor headers")
	}
	cfg.TrustedProxies = nil
	if cfg.ShouldHonorForwardedHeaders("10.0.0.2:50123") {
		t.Fatal("empty allowlist must not honor headers")
	}
	cfg.TrustProxy = false
	cfg.TrustedProxies = []*net.IPNet{mustParseCIDR(t, "10.0.0.0/8")}
	if cfg.ShouldHonorForwardedHeaders("10.0.0.2:50123") {
		t.Fatal("TrustProxy=false must not honor headers")
	}
}

func TestIPIsPanelSignalProxy(t *testing.T) {
	t.Parallel()
	cfg := DefaultConfig()
	if !cfg.IPIsPanelSignalProxy(net.ParseIP("127.0.0.1")) {
		t.Fatal("127.0.0.1 should match default loopback allowlist")
	}
	if !cfg.IPIsPanelSignalProxy(net.ParseIP("::1")) {
		t.Fatal("::1 should match default loopback allowlist")
	}
	if cfg.IPIsPanelSignalProxy(net.ParseIP("198.51.100.1")) {
		t.Fatal("public IP must not match default panel proxy allowlist")
	}

	cfg.PanelSignalProxyCIDRs = nil
	if cfg.IPIsPanelSignalProxy(net.ParseIP("127.0.0.1")) {
		t.Fatal("empty allowlist must reject")
	}

	nets, err := ParseTrustedProxies("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	cfg.PanelSignalProxyCIDRs = nets
	if !cfg.IPIsPanelSignalProxy(net.ParseIP("10.1.2.3")) {
		t.Fatal("10.1.2.3 should match 10.0.0.0/8")
	}
	if cfg.IPIsPanelSignalProxy(net.ParseIP("127.0.0.1")) {
		t.Fatal("loopback should not match custom 10.0.0.0/8-only allowlist")
	}
}

func mustParseCIDR(t *testing.T, cidr string) *net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR(cidr)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// HTTPS-14: TRUST_PROXY without TRUSTED_PROXIES, as the installer's
// Traefik setup has it, logs what it means with the value as written,
// not that headers will be IGNORED.
func TestTrustProxyWithoutListSaysWhatItMeans(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	t.Setenv("TRUST_PROXY", "1")

	cfg := DefaultConfig()
	cfg.TrustProxy = true
	cfg.WarnProxyTrustMisconfig()
	out := buf.String()
	for _, want := range []string{"TRUST_PROXY=1 without TRUSTED_PROXIES", "the console trusts its proxy", "21114, 21118, 21119"} {
		if !strings.Contains(out, want) {
			t.Errorf("log %q, want %q in it", out, want)
		}
	}
	if strings.Contains(out, "IGNORED") || strings.Contains(out, "=Y") {
		t.Errorf("log reads like a misconfiguration: %q", out)
	}

	buf.Reset()
	cfg.TrustedProxies, _ = ParseTrustedProxies("10.0.0.0/8")
	cfg.WarnProxyTrustMisconfig()
	if buf.Len() != 0 {
		t.Errorf("logged with TRUSTED_PROXIES set: %q", buf.String())
	}
}
