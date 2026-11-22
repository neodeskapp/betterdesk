package session

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func env(pairs ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return func(key string) string { return m[key] }
}

func TestPolicyDefaults(t *testing.T) {
	got := PolicyFromEnv(env())
	want := Policy{IdleTimeoutMs: 30 * 60 * 1000, MaxAgeMs: 24 * 60 * 60 * 1000, WarnBeforeMs: 60 * 1000, MaxSessionsPerUser: 1}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestPolicyFromEnv(t *testing.T) {
	const day = 24 * 60 * 60 * 1000
	cases := []struct {
		name string
		env  []string
		want Policy
	}{
		{"values in range are taken", []string{"SESSION_IDLE_TIMEOUT", "120000", "SESSION_MAX_AGE", "600000", "SESSION_IDLE_WARN_MS", "30000", "SESSION_MAX_PER_USER", "5"},
			Policy{120000, 600000, 30000, 5}},
		{"idle below the minimum", []string{"SESSION_IDLE_TIMEOUT", "1"}, Policy{60000, day, 60000, 1}},
		{"idle above the maximum", []string{"SESSION_IDLE_TIMEOUT", "999999999999"}, Policy{day, day, 60000, 1}},
		{"absolute lifetime below the minimum", []string{"SESSION_IDLE_TIMEOUT", "60000", "SESSION_MAX_AGE", "1000"}, Policy{60000, 300000, 60000, 1}},
		{"absolute lifetime above the maximum", []string{"SESSION_MAX_AGE", "99999999999"}, Policy{1800000, 7 * day, 60000, 1}},
		{"absolute lifetime is raised to the idle timeout", []string{"SESSION_IDLE_TIMEOUT", "3600000", "SESSION_MAX_AGE", "600000"}, Policy{3600000, 3600000, 60000, 1}},
		{"zero gives the default", []string{"SESSION_IDLE_TIMEOUT", "0", "SESSION_MAX_AGE", "0", "SESSION_IDLE_WARN_MS", "0"}, Policy{1800000, day, 60000, 1}},
		{"a negative number gives the default", []string{"SESSION_IDLE_TIMEOUT", "-5000000"}, Policy{1800000, day, 60000, 1}},
		{"text gives the default", []string{"SESSION_IDLE_TIMEOUT", "soon", "SESSION_MAX_AGE", "never"}, Policy{1800000, day, 60000, 1}},
		{"the leading number counts", []string{"SESSION_IDLE_TIMEOUT", " 120000ms", "SESSION_MAX_AGE", "+600000.9"}, Policy{120000, 600000, 60000, 1}},
		{"1e6 is 1", []string{"SESSION_IDLE_TIMEOUT", "1e6"}, Policy{60000, day, 60000, 1}},
		{"a number too long to be finite gives the default", []string{"SESSION_IDLE_TIMEOUT", strings.Repeat("9", 400)}, Policy{1800000, day, 60000, 1}},
		{"warning below the minimum", []string{"SESSION_IDLE_WARN_MS", "1"}, Policy{1800000, day, 15000, 1}},
		{"warning above ten minutes", []string{"SESSION_IDLE_WARN_MS", "9999999"}, Policy{1800000, day, 600000, 1}},
		{"warning is at most the idle timeout", []string{"SESSION_IDLE_TIMEOUT", "60000", "SESSION_IDLE_WARN_MS", "500000"}, Policy{60000, day, 60000, 1}},
		{"no limit", []string{"SESSION_MAX_PER_USER", "0"}, Policy{1800000, day, 60000, 0}},
		{"limit with spaces", []string{"SESSION_MAX_PER_USER", " 12 "}, Policy{1800000, day, 60000, 12}},
		{"empty limit", []string{"SESSION_MAX_PER_USER", "  "}, Policy{1800000, day, 60000, 1}},
		{"negative limit", []string{"SESSION_MAX_PER_USER", "-3"}, Policy{1800000, day, 60000, 1}},
		{"limit with a sign", []string{"SESSION_MAX_PER_USER", "+3"}, Policy{1800000, day, 60000, 1}},
		{"limit with a unit", []string{"SESSION_MAX_PER_USER", "3x"}, Policy{1800000, day, 60000, 1}},
		{"limit in words", []string{"SESSION_MAX_PER_USER", "many"}, Policy{1800000, day, 60000, 1}},
		{"limit above the maximum", []string{"SESSION_MAX_PER_USER", "99999999"}, Policy{1800000, day, 60000, 1000}},
		{"limit far above the maximum", []string{"SESSION_MAX_PER_USER", strings.Repeat("9", 400)}, Policy{1800000, day, 60000, 1000}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PolicyFromEnv(env(tc.env...)); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestPolicyJSON(t *testing.T) {
	raw, err := json.Marshal(Policy{1, 2, 3, 4})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"idleTimeoutMs":1,"maxAgeMs":2,"warnBeforeMs":3,"maxSessionsPerUser":4}`; string(raw) != want {
		t.Fatalf("got %s, want %s", raw, want)
	}
}

func TestCleanupInterval(t *testing.T) {
	cases := []struct {
		idle int64
		want time.Duration
	}{
		{60 * 1000, 5 * time.Minute},
		{10 * 60 * 1000, 10 * time.Minute},
		{30 * 60 * 1000, 30 * time.Minute},
		{24 * 60 * 60 * 1000, time.Hour},
	}
	for _, tc := range cases {
		if got := (Policy{IdleTimeoutMs: tc.idle}).CleanupInterval(); got != tc.want {
			t.Errorf("idle %d ms: interval %v, want %v", tc.idle, got, tc.want)
		}
	}
}

func TestPolicyNormalized(t *testing.T) {
	def := PolicyFromEnv(env())
	if got := (Policy{}).normalized(); got != def {
		t.Errorf("zero policy: got %+v, want the defaults", got)
	}
	got := Policy{IdleTimeoutMs: 5, MaxAgeMs: 10, WarnBeforeMs: 20000, MaxSessionsPerUser: 0}.normalized()
	if want := (Policy{60000, 60000, 20000, 0}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestCleanIP(t *testing.T) {
	cases := map[string]string{
		"::ffff:203.0.113.9":     "203.0.113.9",
		"  203.0.113.9\n":        "203.0.113.9",
		"2001:db8::1":            "2001:db8::1",
		"":                       "",
		strings.Repeat("a", 200): strings.Repeat("a", 128),
		"::FFFF:203.0.113.9":     "::FFFF:203.0.113.9",
		" ::ffff:::ffff:1.2.3.4": "::ffff:1.2.3.4",
	}
	for in, want := range cases {
		if got := cleanIP(in); got != want {
			t.Errorf("cleanIP(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanUserAgent(t *testing.T) {
	if got := cleanUserAgent("  Mozilla/5.0 \t"); got != "Mozilla/5.0" {
		t.Errorf("got %q", got)
	}
	if got := cleanUserAgent(strings.Repeat("u", 600)); len(got) != 512 {
		t.Errorf("length %d, want 512", len(got))
	}
	// 512 characters, not bytes
	if got := cleanUserAgent(strings.Repeat("ä", 600)); got != strings.Repeat("ä", 512) {
		t.Errorf("a user agent of two-byte characters was cut at %d characters", len([]rune(got)))
	}
}

func TestCleanFingerprint(t *testing.T) {
	cases := map[string]string{
		"abc123def456":           "abc123def456",
		"  fp_AZaz09_.=+/-  ":    "fp_AZaz09_.=+/-",
		"bad fingerprint!!":      "",
		"short":                  "",
		"12345678":               "12345678",
		strings.Repeat("a", 128): strings.Repeat("a", 128),
		strings.Repeat("a", 129): "",
		"abcdefgh\nabcdefgh":     "",
		"abcdefghä":              "",
		"":                       "",
	}
	for in, want := range cases {
		if got := cleanFingerprint(in); got != want {
			t.Errorf("cleanFingerprint(%q) = %q, want %q", in, got, want)
		}
	}
}
