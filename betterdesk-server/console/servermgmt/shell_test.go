package servermgmt

import (
	"testing"
)

// only is an Executable that knows just these files.
func only(present ...string) func(string) bool {
	return func(file string) bool {
		for _, p := range present {
			if p == file {
				return true
			}
		}
		return false
	}
}

func shellService(executable func(string) bool) *Service {
	return New(Options{GOOS: "linux", Getenv: func(string) string { return "" }, Terminal: TerminalOptions{Executable: executable}})
}

// The shell is the login shell first, then the fallbacks in order, and
// only shells that exist (the Jest tests of serverTerminalShell): the
// Docker image is Alpine, which has no bash.
func TestResolveLoginShell(t *testing.T) {
	cases := []struct {
		shell   string
		present []string
		want    string
	}{
		{"", []string{"/bin/sh"}, "/bin/sh"},
		{"/bin/bash", []string{"/bin/sh"}, "/bin/sh"},
		{"", []string{"/bin/ash", "/bin/sh"}, "/bin/ash"},
		{"/usr/bin/zsh", []string{"/usr/bin/zsh", "/bin/bash", "/bin/sh"}, "/usr/bin/zsh"},
		{"/bin/sh", []string{"/bin/bash", "/bin/sh"}, "/bin/sh"},
		{"", []string{"/usr/bin/bash", "/bin/zsh", "/bin/sh"}, "/usr/bin/bash"},
		{"", []string{"/bin/bash", "/bin/sh"}, "/bin/bash"},
		{"/usr/bin/python3", []string{"/usr/bin/python3", "/bin/sh"}, "/bin/sh"},
		{"/opt/../bin/bash", []string{"/opt/../bin/bash", "/bin/sh"}, "/bin/sh"},
		{"bash", []string{"bash", "/bin/sh"}, "/bin/sh"},
		{"/usr/local/bin/dash", []string{"/usr/local/bin/dash"}, "/usr/local/bin/dash"},
		{"/bin/bash/", []string{"/bin/bash/", "/bin/zsh"}, "/bin/bash/"},
		{"", nil, "/bin/sh"},
	}
	for _, tc := range cases {
		if got := shellService(only(tc.present...)).ResolveLoginShell(tc.shell); got != tc.want {
			t.Errorf("ResolveLoginShell(%q) with %v = %q, want %q", tc.shell, tc.present, got, tc.want)
		}
	}
	// the real file system by default
	if got := shellService(nil).ResolveLoginShell("/nonexistent/bash"); got == "/nonexistent/bash" {
		t.Errorf("a shell that is not there: %q", got)
	}
	win := New(Options{GOOS: "windows", Getenv: func(k string) string { return map[string]string{"COMSPEC": `C:\cmd.exe`}[k] }})
	for shell, want := range map[string]string{"": `C:\cmd.exe`, "pwsh.exe": "pwsh.exe", "cmd & calc": `C:\cmd.exe`, "a$(b)": `C:\cmd.exe`} {
		if got := win.ResolveLoginShell(shell); got != want {
			t.Errorf("windows %q = %q, want %q", shell, got, want)
		}
	}
}

const testPasswd = `root:x:0:0:root:/root:/bin/bash
daemon:x:1:1::/usr/sbin:/usr/sbin/nologin

# a comment
low:x:999:999::/home/low:/bin/bash
nl:x:1000:1000::/home/nl:/usr/sbin/nologin
fl:x:1001:1001::/home/fl:/bin/FALSE
rootdir:x:1002:1002::/:/bin/bash
nobody:x:1003:1003::/home/nobody:/bin/bash
nohome:x:1004:1004:::/bin/bash
  first:x:1005abc:1005::/home/first:/bin/zsh
second:x:1006:1006::/home/second:/bin/bash
big:x:60000:60000::/home/big:/bin/bash
shell-less:x:1007:1007::/home/sl:
falsy-name:x:1008:1008::/home/fn:/bin/falsehood`

// The account of the terminal: the configured one (by name or user id)
// when it has a login shell; when the console runs as root, the first
// account from 1000 to 59999 that is not nobody, has a login shell and a
// home other than /; else the console's own with SHELL or its shell.
func TestPickTerminalUser(t *testing.T) {
	type want struct {
		user, home, shell string
		uid               float64
	}
	cases := []struct {
		name string
		env  map[string]string
		root bool
		want want
	}{
		{"root picks the first local account", nil, true, want{"first", "/home/first", "/bin/zsh", 1005}},
		{"configured by name", map[string]string{"BETTERDESK_TERMINAL_USER": "second"}, true, want{"second", "/home/second", "/bin/bash", 1006}},
		{"configured by user id", map[string]string{"BETTERDESK_TERMINAL_USER": "1006"}, true, want{"second", "/home/second", "/bin/bash", 1006}},
		{"configured root", map[string]string{"BETTERDESK_TERMINAL_USER": "root"}, true, want{"root", "/root", "/bin/bash", 0}},
		{"configured without a login shell", map[string]string{"BETTERDESK_TERMINAL_USER": "daemon"}, true, want{"first", "/home/first", "/bin/zsh", 1005}},
		{"configured, false shell in capitals", map[string]string{"BETTERDESK_TERMINAL_USER": "fl"}, true, want{"first", "/home/first", "/bin/zsh", 1005}},
		{"configured missing", map[string]string{"BETTERDESK_TERMINAL_USER": "ghost"}, true, want{"first", "/home/first", "/bin/zsh", 1005}},
		{"the fallback variable", map[string]string{"SERVER_MANAGEMENT_USER": "second"}, true, want{"second", "/home/second", "/bin/bash", 1006}},
		{"the first variable wins", map[string]string{"BETTERDESK_TERMINAL_USER": "root", "SERVER_MANAGEMENT_USER": "second"}, true, want{"root", "/root", "/bin/bash", 0}},
		{"not root: the console's account", nil, false, want{"console", "/srv/console", "/bin/bash", 500}},
		{"not root: SHELL", map[string]string{"SHELL": "/bin/bash"}, false, want{"console", "/srv/console", "/bin/bash", 500}},
		{"not root: SHELL nologin", map[string]string{"SHELL": "/usr/sbin/nologin"}, false, want{"console", "/srv/console", "/bin/bash", 500}},
		{"not root: configured", map[string]string{"BETTERDESK_TERMINAL_USER": "second"}, false, want{"second", "/home/second", "/bin/bash", 1006}},
		{"a name ending in falsehood", map[string]string{"BETTERDESK_TERMINAL_USER": "falsy-name"}, true, want{"falsy-name", "/home/fn", "/bin/bash", 1008}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New(Options{GOOS: "linux", Getenv: func(k string) string { return tc.env[k] }, Terminal: TerminalOptions{
				Passwd: func() (string, error) { return testPasswd, nil },
				IsRoot: func() bool { return tc.root },
				CurrentUser: func() Account {
					return Account{Username: "console", UID: 500, GID: 500, Home: "/srv/console", Shell: "/usr/sbin/nologin"}
				},
				Executable: only("/bin/zsh", "/bin/bash", "/bin/sh"),
			}})
			got := s.PickTerminalUser()
			if got.Username != tc.want.user || got.Home != tc.want.home || got.Shell != tc.want.shell || got.UID != tc.want.uid {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
	// the edges of the user ids a console running as root picks
	edges := New(Options{GOOS: "linux", Getenv: func(string) string { return "" }, Terminal: TerminalOptions{
		Passwd: func() (string, error) {
			return "low:x:999:1::/home/low:/bin/sh\nbig:x:60000:1::/home/big:/bin/sh\nedge:x:59999:1::/home/edge:/bin/sh\n", nil
		},
		IsRoot:      func() bool { return true },
		CurrentUser: func() Account { return Account{Username: "console"} },
		Executable:  only("/bin/sh"),
	}})
	if got := edges.PickTerminalUser(); got.Username != "edge" {
		t.Errorf("user ids 999, 60000, 59999: picked %+v", got)
	}
	// not root, no valid SHELL: the console's own login shell
	own := New(Options{GOOS: "linux", Getenv: func(k string) string { return map[string]string{"SHELL": "/bin/false"}[k] }, Terminal: TerminalOptions{
		Passwd:      func() (string, error) { return testPasswd, nil },
		IsRoot:      func() bool { return false },
		CurrentUser: func() Account { return Account{Username: "console", Home: "/srv/console", Shell: "/bin/zsh"} },
		Executable:  only("/bin/zsh", "/bin/bash", "/bin/sh"),
	}})
	if got := own.PickTerminalUser(); got.Shell != "/bin/zsh" {
		t.Errorf("the console's own shell: %+v", got)
	}
	win := New(Options{GOOS: "windows", Getenv: func(string) string { return "" }, Terminal: TerminalOptions{
		CurrentUser: func() Account { return Account{Username: "svc", UID: -1, GID: -1, Home: `C:\Users\svc`} },
	}})
	if got := win.PickTerminalUser(); got.Username != "svc" || got.Shell != "powershell.exe" || got.Home != `C:\Users\svc` {
		t.Errorf("windows %+v", got)
	}
}

// The terminal's size from a message: String(value), parseInt, 0 and NaN
// take the default, then the limits.
func TestDimension(t *testing.T) {
	cases := []struct {
		raw  string
		want int
	}{
		{`1000`, 500}, {`"x"`, 80}, {`20.7`, 20}, {`"7xyz"`, 20}, {`"1e3"`, 20}, {`-1`, 20}, {`[300]`, 300}, {`{}`, 80},
		{`true`, 80}, {`"  42"`, 42}, {`"0x10"`, 80}, {`499.9`, 499}, {`1e21`, 20}, {`null`, 80}, {`"-0"`, 80}, {`""`, 80},
		{`501`, 500}, {`19`, 20}, {`[[1,2],[3]]`, 20}, {`[null]`, 80}, {`" \u00a0 77"`, 77}, {`1e400`, 80}, {`"+33"`, 33}, {`[]`, 80},
	}
	for _, tc := range cases {
		msg, ok := parseMessage([]byte(`{"cols":` + tc.raw + `}`))
		if !ok {
			t.Fatalf("%s did not parse", tc.raw)
		}
		if got := dimension(msg["cols"], 20, 500, 80); got != tc.want {
			t.Errorf("cols %s = %d, want %d", tc.raw, got, tc.want)
		}
	}
	msg, _ := parseMessage([]byte(`{}`))
	if got := dimension(msg["rows"], 5, 200, 24); got != 24 {
		t.Errorf("missing rows %d", got)
	}
}

// A message counts when it is a JSON object, in a text or a binary frame
// read as UTF-8; anything else is ignored.
func TestParseMessage(t *testing.T) {
	for raw, want := range map[string]bool{
		`{"type":"init"}`: true, `  {} `: true, "{}\n": true, `[{"type":"init"}]`: false, `null`: false, `5`: false, `"x"`: false,
		`{`: false, `{} x`: false, `{}{}`: false, "\xff": false, "{\"type\":\"\xff\"}": true, "\ufeff{}": false, ``: false,
	} {
		if _, ok := parseMessage([]byte(raw)); ok != want {
			t.Errorf("%q: %v, want %v", raw, ok, want)
		}
	}
	msg, _ := parseMessage([]byte(`{"type":"input","type":"init","data":"\ud800"}`))
	if msg["type"] != "init" {
		t.Errorf("the last of two members counts: %v", msg["type"])
	}
	if msg["data"] != "\ufffd" {
		t.Errorf("a lone surrogate: %q", msg["data"])
	}
}
