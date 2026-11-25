package main

import (
	"bufio"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/ndcli"
)

// clientOptions reads testdata/client-options.txt: the desktop client's
// options as scripts/client-options.sh found them in the client's code,
// and the client commit they come from.
func clientOptions(t *testing.T) (options []string, from string) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", "client-options.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "# ") && from == "":
			from = line
		case line == "" || strings.HasPrefix(line, "#"):
		default:
			options = append(options, line)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(options) < 20 {
		t.Fatalf("read only %d options from testdata/client-options.txt", len(options))
	}
	return options, from
}

// L431: the client's options are kept once in Go, in clientFlags (handed
// over) and clientFlagsKept (neodesk's own). Together they are the
// client's list; a new option of the client must go into one of them.
// Regenerate the list with scripts/client-options.sh from a client
// checkout.
func TestClientFlagsAreTheClients(t *testing.T) {
	want, from := clientOptions(t)
	got := slices.Clone(clientFlags)
	for flag := range clientFlagsKept {
		got = append(got, flag)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("clientFlags and clientFlagsKept are %v,\nthe client takes %v\n(%s)", got, want, from)
	}
}

// An option neodesk hands to the client is neither the installer's nor
// one of the command line's own; each kept one is.
func TestClientFlagsClashWithNoOtherOption(t *testing.T) {
	own := ndcli.GlobalFlags()
	for _, flag := range clientFlags {
		if slices.Contains(setupFlags, flag) || slices.Contains(own, flag) {
			t.Errorf("%s goes to the client, but it is neodesk's too", flag)
		}
		if _, kept := clientFlagsKept[flag]; kept {
			t.Errorf("%s is both handed over and kept", flag)
		}
	}
	for flag := range clientFlagsKept {
		if !slices.Contains(setupFlags, flag) && !slices.Contains(own, flag) {
			t.Errorf("%s is kept, but neither the installer nor the command line takes it", flag)
		}
	}
}

// Which calls go to the client: a first argument that is one of the
// client's options, with the arguments as they were. Without the client
// the call ends with a message that names it and a non-zero exit.
func TestWhichCallsGoToTheClient(t *testing.T) {
	type ran struct {
		path string
		args []string
	}
	for _, tc := range []struct {
		name     string
		args     []string
		present  bool
		want     *ran
		wantCode int
	}{
		{"--join", []string{"--join", "https://desk.example.com/join/ABCDEF23"}, true, &ran{clientCommand, []string{"--join", "https://desk.example.com/join/ABCDEF23"}}, 7},
		{"--service", []string{"--service"}, true, &ran{clientCommand, []string{"--service"}}, 7},
		{"--password", []string{"--password", "two words"}, true, &ran{clientCommand, []string{"--password", "two words"}}, 7},
		{"--connect", []string{"--connect", "123456789", "--relay"}, true, &ran{clientCommand, []string{"--connect", "123456789", "--relay"}}, 7},
		{"--join without the client", []string{"--join", "ABCDEF23"}, false, nil, ndcli.ExitUsage},
		{"bare", nil, true, nil, 0},
		{"--version", []string{"--version"}, true, nil, 0},
		{"--config", []string{"--config", "x.json", "devices", "list"}, true, nil, 0},
		{"devices list --password", []string{"devices", "list", "--password"}, true, nil, 0},
		{"--json --join", []string{"--json", "--join"}, true, nil, 0},
	} {
		var got *ran
		var stderr strings.Builder
		code, handled := handToClient(tc.args, clientCall{
			find: func() (string, bool) { return clientCommand, tc.present },
			run: func(path string, args []string) int {
				got = &ran{path, args}
				return 7
			},
			stderr: &stderr,
		})
		if handled != (tc.want != nil || tc.wantCode != 0) || code != tc.wantCode {
			t.Errorf("%s: handled %v, exit %d, want exit %d", tc.name, handled, code, tc.wantCode)
		}
		switch {
		case tc.want == nil && got != nil:
			t.Errorf("%s: ran %s %v", tc.name, got.path, got.args)
		case tc.want != nil && (got == nil || got.path != tc.want.path || !slices.Equal(got.args, tc.want.args)):
			t.Errorf("%s: ran %+v, want %+v", tc.name, got, tc.want)
		}
		if tc.wantCode == ndcli.ExitUsage && !strings.Contains(stderr.String(), clientCommand) {
			t.Errorf("%s: the message does not name %s: %q", tc.name, clientCommand, stderr.String())
		}
	}
}

// Called as neodesk on a machine without the client, a client option
// ends with the message and a non-zero exit, and the installer's options
// still go to neodesk-setup.
func TestAClientOptionWithoutTheClientSaysSo(t *testing.T) {
	if _, err := os.Stat(clientCommand); err == nil {
		t.Skip("this machine has the desktop client at " + clientCommand)
	}
	dir, record := fakeSetup(t)
	for _, flag := range []string{"--join", "--service", "--password"} {
		code, stdout, stderr := runAsNeodesk(t, dir, flag, "x")
		if code == 0 || !strings.Contains(stderr, "desktop client") || !strings.Contains(stderr, clientCommand) {
			t.Errorf("neodesk %s without the client: exit %d\nstdout %q\nstderr %q", flag, code, stdout, stderr)
		}
		if _, err := os.Stat(record); err == nil {
			t.Errorf("neodesk %s ran neodesk-setup", flag)
		}
	}
	if code, _, _ := runAsNeodesk(t, dir, "--update"); code != 7 {
		t.Errorf("neodesk --update did not go to neodesk-setup: exit %d", code)
	}
}

// A join or support link as the only argument goes to the client when it
// has exactly the shape the server gives its links (console/onboard):
// <base>/join/<code> and <base>/s/<code> go as --join <link>, which the
// client takes for both kinds, and neodesk://join|support/... as it is,
// which the client takes alone. A code is eight symbols of the codes'
// alphabet in upper case, as the server writes it. Anything else stays
// with the command line, and without the client the call ends with exit 2
// and a message that names neodesk --join and the client.
func TestABareLinkGoesToTheClient(t *testing.T) {
	handed := map[string][]string{
		"https://desk.example.com/join/ABCDEF23":          {"--join", "https://desk.example.com/join/ABCDEF23"},
		"https://desk.example.com:8443/s/ABCDEF23":        {"--join", "https://desk.example.com:8443/s/ABCDEF23"},
		"http://192.168.1.10:5000/join/ABCDEF23":          {"--join", "http://192.168.1.10:5000/join/ABCDEF23"},
		"https://[2001:db8::1]:8443/s/ABCDEF23":           {"--join", "https://[2001:db8::1]:8443/s/ABCDEF23"},
		"neodesk://join/desk.example.com/ABCDEF23":        {"neodesk://join/desk.example.com/ABCDEF23"},
		"neodesk://support/desk.lan:5000/ABCDEF23?http=1": {"neodesk://support/desk.lan:5000/ABCDEF23?http=1"},
	}
	lookAlikes := []string{
		"https://desk.example.com/join/ABCDEF23/",
		"https://desk.example.com/join/ABCDEF23?ref=mail",
		"https://desk.example.com/join/ABCDEF23#top",
		"https://desk.example.com/joins/ABCDEF23",
		"https://desk.example.com/support/ABCDEF23",
		"https://desk.example.com/console/join/ABCDEF23",
		"https://desk.example.com/join/ABCDEF21",
		"https://desk.example.com/join/ABCDEF2",
		"https://desk.example.com/join/abcdef23",
		"https://desk.example.com/join/ABCD-EF23",
		"https://user@desk.example.com/join/ABCDEF23",
		"https://desk_example.com/join/ABCDEF23",
		"https://desk.example.com:99999/join/ABCDEF23",
		"ftp://desk.example.com/join/ABCDEF23",
		"https:/desk.example.com/join/ABCDEF23",
		"neodesk://connect/123456789",
		"neodesk://join/desk.example.com/ABCDEF23?http=0",
		"neodesk://join/desk.example.com/ABCDEF23/x",
		"rustdesk://join/desk.example.com/ABCDEF23",
		"ABCDEF23",
		"devices",
	}
	call := func(args []string, present bool) (code int, handled bool, ran []string, stderr string) {
		var errOut strings.Builder
		code, handled = handToClient(args, clientCall{
			find: func() (string, bool) { return clientCommand, present },
			run: func(path string, a []string) int {
				if path != clientCommand {
					t.Errorf("ran %s", path)
				}
				ran = a
				return 7
			},
			stderr: &errOut,
		})
		return code, handled, ran, errOut.String()
	}
	for link, want := range handed {
		if code, handled, ran, _ := call([]string{link}, true); !handled || code != 7 || !slices.Equal(ran, want) {
			t.Errorf("%s: handled %v, exit %d, ran %q, want %q", link, handled, code, ran, want)
		}
		code, handled, ran, stderr := call([]string{link}, false)
		if !handled || code != ndcli.ExitUsage || ran != nil || !strings.Contains(stderr, "neodesk --join") || !strings.Contains(stderr, "desktop client") {
			t.Errorf("%s without the client: handled %v, exit %d, stderr %q", link, handled, code, stderr)
		}
		if code, handled, ran, _ := call([]string{link, "devices"}, true); handled || code != 0 || ran != nil {
			t.Errorf("%s with another argument went to the client: %q", link, ran)
		}
	}
	for _, link := range lookAlikes {
		if code, handled, ran, stderr := call([]string{link}, true); handled || code != 0 || ran != nil {
			t.Errorf("look-alike %s went to the client: exit %d, ran %q, %q", link, code, ran, stderr)
		}
	}
}

// Called as neodesk without the client, a bare support link ends with
// exit 2 and the message, and a look-alike gets the command line's own
// answer.
func TestABareLinkWithoutTheClientSaysSo(t *testing.T) {
	if _, err := os.Stat(clientCommand); err == nil {
		t.Skip("this machine has the desktop client at " + clientCommand)
	}
	dir, _ := fakeSetup(t)
	code, _, stderr := runAsNeodesk(t, dir, "https://desk.example.com/s/ABCDEF23")
	if code != ndcli.ExitUsage || !strings.Contains(stderr, "neodesk --join") || !strings.Contains(stderr, clientCommand) {
		t.Errorf("a support link without the client: exit %d, %q", code, stderr)
	}
	if _, _, stderr := runAsNeodesk(t, dir, "https://desk.example.com/s/ABCDEF23/x"); strings.Contains(stderr, "desktop client") {
		t.Errorf("a look-alike named the client: %q", stderr)
	}
}
