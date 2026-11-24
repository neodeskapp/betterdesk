package main

import (
	"io"
	"os"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/ndcli"
)

// The command line (package ndcli) lives in the server binary. It runs
// when the binary is called neodesk (neodesk.sh adds that name as a copy
// of the binary, neodesk.ps1 and the image as a link), or as
// `neodesk-server cli ...` with any name.
// Called neodesk, it hands the installer's options to neodesk-setup
// (cli_setup.go).

// cliName reports whether the program was started as neodesk.
func cliName(arg0 string) bool {
	// Either separator: a Windows path reads the same on every system
	name := strings.ToLower(arg0[strings.LastIndexAny(arg0, `/\`)+1:])
	name = strings.TrimSuffix(name, ".exe")
	return name == "neodesk"
}

// runCLI runs the command line, or neodesk-setup for the installer's
// options. `neodesk local ...` reads the server's env file first, as the
// server's own commands do; the other commands talk to a console and
// leave it alone.
func runCLI(args []string) int {
	if code, handled := handToSetup(args, osSetupCall()); handled {
		return code
	}
	if len(args) > 0 && args[0] == "local" {
		loadEnvFileFromOS()
	}
	return ndcli.Main(args, ndcli.OSEnv(Version, localCommands()))
}

// localCommands are the server binary's own commands for `neodesk local`.
func localCommands() map[string]ndcli.LocalCommand {
	out := map[string]ndcli.LocalCommand{}
	for name, cmd := range subcommands {
		if name == "cli" {
			continue
		}
		run := cmd.run
		out[name] = ndcli.LocalCommand{Usage: cmd.usage, Run: func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
			return run(args, stdin, stdout, stderr)
		}}
	}
	return out
}

func init() {
	subcommands["cli"] = subcommand{
		usage: "cli <group> <action> ...                  the command line (also installed as neodesk); cli --help lists everything",
		run: func(args []string, _ io.Reader, _, _ io.Writer) int {
			return ndcli.Main(args, ndcli.OSEnv(Version, localCommands()))
		},
	}
}

// startedAsCLI runs the command line when the binary was started as
// neodesk, and reports whether it did.
func startedAsCLI() (int, bool) {
	if !cliName(os.Args[0]) {
		return 0, false
	}
	return runCLI(os.Args[1:]), true
}
