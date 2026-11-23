package main

import (
	"bytes"
	"strings"
	"testing"
)

// The binary is the command line when it is called neodesk, and the
// server under any other name.
func TestTheBinaryIsTheCommandLineAsNeodesk(t *testing.T) {
	for name, want := range map[string]bool{
		"neodesk": true, "/usr/local/bin/neodesk": true, `C:\Tools\neodesk.exe`: true, "NEODESK.EXE": true,
		"neodesk-server": false, "/usr/local/bin/neodesk-server": false, "neodesk.sh": false, "": false,
	} {
		if got := cliName(name); got != want {
			t.Errorf("cliName(%q) = %v", name, got)
		}
	}
}

// neodesk-server cli runs the command line under the server's name too,
// and neodesk local reaches the server's own commands.
func TestTheServerRunsTheCommandLineAsCli(t *testing.T) {
	cmd, ok := subcommands["cli"]
	if !ok {
		t.Fatal("no cli subcommand")
	}
	if code := cmd.run([]string{"--version"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Errorf("cli --version: %d", code)
	}
	local := localCommands()
	if _, ok := local["backup"]; !ok {
		t.Error("neodesk local has no backup")
	}
	if _, ok := local["cli"]; ok {
		t.Error("neodesk local cli would loop")
	}
}
