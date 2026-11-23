package main

import (
	"database/sql"
	"errors"
	"io"
	"path/filepath"
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
		{"env", nil, 2, "usage: neodesk-server env merge"},
		{"env", []string{"split"}, 2, "usage: neodesk-server env merge"},
		{"env", []string{"merge"}, 2, "Usage: neodesk-server env merge"},
		{"upgrade-env", nil, 2, "Usage: neodesk-server upgrade-env"},
		{"reset-password", []string{"a", "b"}, 1, "Usage: neodesk-server reset-password"},
		{"check", []string{"--bogus"}, 1, "FAIL Unknown option: --bogus"},
		{"config", []string{"bogus"}, 2, "Usage:\n  neodesk-server config check"},
	} {
		var out, errOut strings.Builder
		code := subcommands[c.name].run(c.args, strings.NewReader(""), &out, &errOut)
		if code != c.code || !strings.Contains(out.String()+errOut.String(), c.usage) {
			t.Errorf("%s %v: %d %q %q", c.name, c.args, code, out.String(), errOut.String())
		}
	}
}

// neodesk-server migrate is the migration tool with its flags: -mode
// backup copies a SQLite file next to it, -h prints the modes and
// succeeds, a mode it does not know prints them and fails (L71).
func TestMigrateCommand(t *testing.T) {
	cmd, ok := subcommands["migrate"]
	if !ok {
		t.Fatal("neodesk-server has no migrate command")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "db_v2.sqlite3")
	file, err := sql.Open("sqlite", src)
	if err == nil {
		_, err = file.Exec(`CREATE TABLE peers (id TEXT)`)
		err = errors.Join(err, file.Close())
	}
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut strings.Builder
	if code := cmd.run([]string{"-mode", "backup", "-src", src}, strings.NewReader(""), &out, &errOut); code != 0 {
		t.Fatalf("migrate -mode backup: %d %q %q", code, out.String(), errOut.String())
	}
	backups, _ := filepath.Glob(filepath.Join(dir, "db_v2_backup_*.sqlite3"))
	if len(backups) != 1 {
		t.Errorf("backups next to the file: %v", backups)
	}
	for _, c := range []struct {
		args []string
		code int
	}{{[]string{"-h"}, 0}, {[]string{"-mode", "nope"}, 1}} {
		out.Reset()
		errOut.Reset()
		if code := cmd.run(c.args, strings.NewReader(""), &out, &errOut); code != c.code || !strings.Contains(out.String()+errOut.String(), "sqlite2pg") {
			t.Errorf("migrate %v: %d %q %q", c.args, code, out.String(), errOut.String())
		}
	}
}
