//go:build nodeinterop

package session

import (
	"bytes"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

// TestPolicyMatchesNode gives the Node console's lib/sessionPolicy.js and
// this package the same settings and values and compares what they make of
// them.
func TestPolicyMatchesNode(t *testing.T) {
	values := []string{"", " ", "0", "1", "5", "-3", "+7", "60000", "59999", "60001", "120000", "300000", "299999",
		"1800000", "86400000", "86400001", "604800000", "604800001", "999999999999", "1e6", "12.9", " 42 ", "42abc", "abc",
		"0x10", "007", "\t90000\n", "Infinity", "NaN", strings.Repeat("9", 400), "15000", "14999", "600000", "600001", "1000", "many"}
	var envs []map[string]string
	for _, name := range []string{"SESSION_IDLE_TIMEOUT", "SESSION_MAX_AGE", "SESSION_IDLE_WARN_MS", "SESSION_MAX_PER_USER"} {
		for _, v := range values {
			envs = append(envs, map[string]string{name: v})
		}
	}
	for _, idle := range []string{"60000", "3600000", "90000000"} {
		for _, v := range values {
			envs = append(envs, map[string]string{"SESSION_IDLE_TIMEOUT": idle, "SESSION_MAX_AGE": v})
			envs = append(envs, map[string]string{"SESSION_IDLE_TIMEOUT": idle, "SESSION_IDLE_WARN_MS": v})
		}
	}
	input := map[string]any{
		"env": envs,
		"ip": []string{"", "::ffff:203.0.113.9", " 203.0.113.9 ", "2001:db8::1", "::FFFF:1.2.3.4", "::ffff:::ffff:1.2.3.4",
			strings.Repeat("a", 200), "\u00a0::ffff:10.0.0.1\ufeff", "::ffff:"},
		"userAgent": []string{"", "  Mozilla/5.0 \t", strings.Repeat("u", 600), strings.Repeat("ä", 600), " " + strings.Repeat("x", 512) + "y",
			strings.Repeat("😀", 300)[:4*255] + "ab" + strings.Repeat("z", 10)},
		"fingerprint": []string{"", "abc123def456", "  fp_AZaz09_.=+/-  ", "bad fingerprint!!", "short", "12345678", strings.Repeat("a", 128),
			strings.Repeat("a", 129), "abcdefgh\nabcdefgh", "abcdefgh\n", "abcdefghä", "abcdefg,", "abcdefg*"},
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", "testdata/interop/policy.js")
	cmd.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, stderr.String())
	}
	var node struct {
		Env         []Policy `json:"env"`
		IP          []string `json:"ip"`
		UserAgent   []string `json:"userAgent"`
		Fingerprint []string `json:"fingerprint"`
	}
	if err := json.Unmarshal(out, &node); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if len(node.Env) != len(envs) {
		t.Fatalf("node answered %d policies for %d settings", len(node.Env), len(envs))
	}
	for i, e := range envs {
		if got := PolicyFromEnv(func(key string) string { return e[key] }); got != node.Env[i] {
			t.Errorf("settings %q: Go %+v, Node %+v", e, got, node.Env[i])
		}
	}
	for i, v := range input["ip"].([]string) {
		if got := cleanIP(v); got != node.IP[i] {
			t.Errorf("address %q: Go %q, Node %q", v, got, node.IP[i])
		}
	}
	for i, v := range input["userAgent"].([]string) {
		if got := cleanUserAgent(v); got != node.UserAgent[i] {
			t.Errorf("user agent %q: Go %q, Node %q", v, got, node.UserAgent[i])
		}
	}
	for i, v := range input["fingerprint"].([]string) {
		if got := cleanFingerprint(v); got != node.Fingerprint[i] {
			t.Errorf("fingerprint %q: Go %q, Node %q", v, got, node.Fingerprint[i])
		}
	}
}
