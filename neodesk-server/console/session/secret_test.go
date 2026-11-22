package session

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

// The vectors of the spec: session ids and their signed values under
// testSecret, as the Node console wrote them.
var signVectors = []struct{ sid, signed, wire string }{
	{
		"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"s:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA.Qb/rWF8+jkBOBM6RtELK7KYpNGwASfnZPgGVN+Fmpz4",
		"s%3AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA.Qb%2FrWF8%2BjkBOBM6RtELK7KYpNGwASfnZPgGVN%2BFmpz4",
	},
	{
		"abcdefghijklmnopqrstuvwxyz012345",
		"s:abcdefghijklmnopqrstuvwxyz012345.lfQkjckWZ9U3n2POwhxoJb2qwpnCD15/bLHZtOW8FkM",
		"s%3Aabcdefghijklmnopqrstuvwxyz012345.lfQkjckWZ9U3n2POwhxoJb2qwpnCD15%2FbLHZtOW8FkM",
	},
	{
		"k3-_Zx9QmT1uV7wY2aB4cD6eF8gH0iJL",
		"s:k3-_Zx9QmT1uV7wY2aB4cD6eF8gH0iJL./zhdktwspTfAI2mL/u/Sfj6iyWhu8NEaeXF0q9fiYgY",
		"s%3Ak3-_Zx9QmT1uV7wY2aB4cD6eF8gH0iJL.%2FzhdktwspTfAI2mL%2Fu%2FSfj6iyWhu8NEaeXF0q9fiYgY",
	},
	{
		"SID00000000000000000000000000002",
		"s:SID00000000000000000000000000002.7v0UvmlKPf1vOXoGbOBv959WIYQqLBqIeCCTWsssgwQ",
		"s%3ASID00000000000000000000000000002.7v0UvmlKPf1vOXoGbOBv959WIYQqLBqIeCCTWsssgwQ",
	},
}

func TestSignVectors(t *testing.T) {
	for _, v := range signVectors {
		if got := cookiePrefix + sign(v.sid, testSecret); got != v.signed {
			t.Errorf("sign(%s) = %s, want %s", v.sid, got, v.signed)
		}
		sid, ok := unsign(v.signed[len(cookiePrefix):], testSecret)
		if !ok || sid != v.sid {
			t.Errorf("unsign(%s) = %q, %v", v.signed, sid, ok)
		}
	}
}

func TestUnsignRefuses(t *testing.T) {
	good := sign("abc", testSecret)
	cases := map[string]string{
		"another secret":           sign("abc", "another-secret"),
		"the mac of another id":    "abd" + good[3:],
		"a mac one character off":  good[:len(good)-1] + "A",
		"a mac cut short":          good[:len(good)-1],
		"a mac with padding":       good + "=",
		"no dot":                   "abc",
		"no mac":                   "abc.",
		"empty":                    "",
		"a longer value":           good + "x",
		"the id repeated":          "abc." + good,
		"hex instead of base64":    "abc.41bfeb585f3e8e404e04ce91b442caeca629346c0049f9d93e019537e166a73e",
		"URL-safe base64 alphabet": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA.Qb_rWF8-jkBOBM6RtELK7KYpNGwASfnZPgGVN-Fmpz4",
	}
	for name, signed := range cases {
		if sid, ok := unsign(signed, testSecret); ok {
			t.Errorf("%s: unsign(%q) accepted id %q", name, signed, sid)
		}
	}
	if _, ok := unsign(good, testSecret); !ok {
		t.Fatal("the good value was refused")
	}
}

func TestNewID(t *testing.T) {
	pattern := regexp.MustCompile(`^[A-Za-z0-9_-]{32}$`)
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		id, err := newID()
		if err != nil {
			t.Fatal(err)
		}
		if !pattern.MatchString(id) {
			t.Fatalf("id %q is not 32 URL-safe characters", id)
		}
		if seen[id] {
			t.Fatalf("id %q came twice", id)
		}
		seen[id] = true
	}
}

func TestLoadSecretFromEnvironment(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, secretFile), []byte("from-file"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSecret(env("SESSION_SECRET", " from env "), dir)
	if err != nil || got != " from env " {
		t.Fatalf("got %q, %v; the environment's value is taken as it is", got, err)
	}
}

func TestLoadSecretFromFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, secretFile), []byte("\n  from-file \r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSecret(env(), dir)
	if err != nil || got != "from-file" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestLoadSecretGenerates(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data", "deeper")
	got, err := LoadSecret(env(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(got) {
		t.Fatalf("generated secret %q is not 64 hex characters", got)
	}
	path := filepath.Join(dir, secretFile)
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != got {
		t.Fatalf("the file holds %q, want the secret without a line break", content)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Fatalf("file mode %o, want 600", mode)
		}
	}
	again, err := LoadSecret(env(), dir)
	if err != nil || again != got {
		t.Fatalf("second load gave %q, %v; want the saved secret", again, err)
	}
	other, err := LoadSecret(env(), t.TempDir())
	if err != nil || other == got {
		t.Fatalf("another directory gave the same secret (%v)", err)
	}
}

func TestLoadSecretWhenTheFileCannotBeWritten(t *testing.T) {
	// The data directory's parent is a file, so nothing can be created
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(blocker, "data")
	got, err := LoadSecret(env(), dir)
	if err != nil || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(got) {
		t.Fatalf("got %q, %v; want a secret that lives in memory", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, secretFile)); err == nil {
		t.Fatal("the file exists after all")
	}
}

func TestLoadSecretUnreadableFile(t *testing.T) {
	// A directory in the file's place is there but cannot be read
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, secretFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if got, err := LoadSecret(env(), dir); err == nil {
		t.Fatalf("got secret %q, want an error", got)
	}
}

func TestLoadSecretEmptyFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, secretFile), []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSecret(env(), dir)
	if err != nil || got != "" {
		t.Fatalf("got %q, %v; an empty file is an empty secret, which New refuses", got, err)
	}
}
