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
