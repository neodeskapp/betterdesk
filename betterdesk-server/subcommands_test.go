package main

import (
	"io"
	"strings"
	"testing"
)

// A first argument that does not start with "-" names a command; flags and
// no arguments run the server.
func TestRunSubcommand(t *testing.T) {
	for _, args := range [][]string{nil, {}, {"-mode", "all"}, {"--version"}, {"-version"}} {
		if _, handled := runSubcommand(args); handled {
			t.Errorf("%v was taken for a command", args)
		}
	}
	if code, handled := runSubcommand([]string{"no-such-command"}); !handled || code != 2 {
		t.Errorf("unknown command: %d %v", code, handled)
	}
	var got []string
	saved := subcommands["rollback"]
	subcommands["rollback"] = subcommand{usage: saved.usage, run: func(args []string, _ io.Reader, _, _ io.Writer) int {
		got = args
		return 7
	}}
	defer func() { subcommands["rollback"] = saved }()
	if code, handled := runSubcommand([]string{"rollback", "--x"}); !handled || code != 7 || strings.Join(got, ",") != "--x" {
		t.Errorf("rollback: %d %v %v", code, handled, got)
	}
	for name, cmd := range subcommands {
		if !strings.HasPrefix(cmd.usage, name) {
			t.Errorf("the usage of %s does not start with its name", name)
		}
	}
}

// The commands of package cli are reached by their names, with the
// streams they are given.
func TestCLICommandsAreWired(t *testing.T) {
	for _, c := range []struct {
		name  string
		args  []string
		code  int
		usage string
	}{
		{"env", nil, 2, "usage: betterdesk-server env merge"},
		{"env", []string{"split"}, 2, "usage: betterdesk-server env merge"},
		{"env", []string{"merge"}, 2, "Usage: betterdesk-server env merge"},
		{"upgrade-env", nil, 2, "Usage: betterdesk-server upgrade-env"},
		{"reset-password", []string{"a", "b"}, 1, "Usage: betterdesk-server reset-password"},
		{"check", []string{"--bogus"}, 1, "FAIL Unknown option: --bogus"},
		{"config", []string{"bogus"}, 2, "Usage:\n  betterdesk-server config check"},
	} {
		var out, errOut strings.Builder
		code := subcommands[c.name].run(c.args, strings.NewReader(""), &out, &errOut)
		if code != c.code || !strings.Contains(out.String()+errOut.String(), c.usage) {
			t.Errorf("%s %v: %d %q %q", c.name, c.args, code, out.String(), errOut.String())
		}
	}
}
