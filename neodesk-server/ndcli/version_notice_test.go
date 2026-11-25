package ndcli

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
)

// The command line says on stderr when it is older than the server it
// talks to (the server names its version on a token's answers), once per
// pair of versions: a second command against the same server says
// nothing, a newer server says it again. Its own version newer or equal,
// a development build, a server that names no version and --json's
// standard output say nothing.
func TestTheCommandLineSaysWhenItIsOlderThanTheServer(t *testing.T) {
	f := newFake(t)
	server := "3.6.0-rc.5"
	f.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if server != "" {
			w.Header().Set("X-NeoDesk-Version", server)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true,"data":[]}`))
	})
	dir := t.TempDir()
	run := func(version string, args ...string) (string, string) {
		t.Helper()
		var out, errOut bytes.Buffer
		e := &Env{
			Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errOut,
			Getenv: func(k string) string {
				return map[string]string{"NEODESK_URL": f.srv.URL, "NEODESK_TOKEN": testToken}[k]
			},
			UserConfigDir: func() (string, error) { return dir, nil },
			Terminal:      func(any) bool { return false },
			Version:       version,
		}
		if code := Main(args, e); code != 0 {
			t.Fatalf("%v: exit %d: %s", args, code, errOut.String())
		}
		return out.String(), errOut.String()
	}
	const told = "is older than the server"

	if _, stderr := run("3.6.0-rc.4", "devices", "list"); !strings.Contains(stderr, told) || !strings.Contains(stderr, "3.6.0-rc.4") || !strings.Contains(stderr, "3.6.0-rc.5") {
		t.Errorf("an older command line said: %q", stderr)
	}
	if _, stderr := run("3.6.0-rc.4", "devices", "list"); strings.Contains(stderr, told) {
		t.Errorf("the same pair told twice: %q", stderr)
	}
	server = "3.6.0"
	stdout, stderr := run("3.6.0-rc.4", "--json", "devices", "list")
	if !strings.Contains(stderr, told) || strings.Contains(stdout, told) {
		t.Errorf("a newer server: stdout %q, stderr %q", stdout, stderr)
	}
	for _, c := range []struct{ cli, server string }{{"3.6.0", "3.6.0"}, {"3.6.1", "3.6.0"}, {"dev", "3.6.0"}, {"3.6.0-rc.4", ""}, {"3.6.0-rc.4", "dev"}} {
		server = c.server
		if _, stderr := run(c.cli, "devices", "list"); strings.Contains(stderr, told) {
			t.Errorf("command line %q, server %q: %q", c.cli, c.server, stderr)
		}
	}
}

// Release candidates count as numbers (rc.9 before rc.10) and before the
// release itself.
func TestOlderVersionOrdersReleaseCandidates(t *testing.T) {
	for _, c := range []struct {
		a, b  string
		older bool
	}{
		{"3.6.0-rc.9", "3.6.0-rc.10", true}, {"3.6.0-rc.10", "3.6.0-rc.9", false},
		{"3.6.0-rc.5", "3.6.0", true}, {"3.6.0", "3.6.0-rc.5", false},
		{"3.5.280", "3.6.0-rc.1", true}, {"3.10.0", "3.9.9", false}, {"v3.6.0", "3.7.0", false},
	} {
		if got := olderVersion(c.a, c.b); got != c.older {
			t.Errorf("%s older than %s: %v", c.a, c.b, got)
		}
	}
}
