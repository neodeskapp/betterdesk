package main

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"syscall"

	"github.com/neodeskapp/neodesk/neodesk-server/ndcli"
)

// On a Linux host that also has the NeoDesk desktop client, the client is
// /usr/bin/neodesk, and the command line /usr/local/bin/neodesk comes first
// in PATH. neodesk stays the command line and hands the client's own
// options to /usr/bin/neodesk, with the arguments as they were, the way it
// hands the installer's options to neodesk-setup (cli_setup.go).

// clientCommand is where the desktop client's Linux packages install it.
const clientCommand = "/usr/bin/neodesk"

// clientFlags are the client's command-line options that neodesk hands to
// it as its first argument. Together with clientFlagsKept they are the
// options of testdata/client-options.txt, which scripts/client-options.sh
// writes from the client's code; TestClientFlagsAreTheClients compares
// the two.
var clientFlags = []string{
	"--after-install", "--assign", "--before-uninstall", "--build-date",
	"--check-hwcodec-config", "--claim-rustdesk-scheme", "--cm", "--connect",
	"--deploy", "--elevate", "--file-transfer", "--get-id", "--import-config",
	"--install", "--install-idd", "--install-remote-printer", "--install-service",
	"--join", "--no-server", "--noinstall", "--option", "--password", "--play",
	"--port-forward", "--portable-service", "--quick_support", "--rdp",
	"--release-rustdesk-scheme", "--remove", "--run-as-system", "--server",
	"--service", "--set-id", "--set-unlock-pin", "--silent-install", "--switch_uuid",
	"--terminal", "--terminal-helper", "--tray", "--uninstall-amyuni-idd",
	"--uninstall-cert", "--uninstall-remote-printer", "--uninstall-service",
	"--view-camera", "--whiteboard",
}

// clientFlagsKept are the client's options that name something of neodesk
// already, and who keeps them. The client takes them when it is called as
// /usr/bin/neodesk.
var clientFlagsKept = map[string]string{
	"--config":    "the command line (the config file)",
	"--version":   "the command line",
	"--update":    "the installer, neodesk-setup",
	"--uninstall": "the installer, neodesk-setup",
	// The client's --relay only qualifies --connect and the like
	"--relay": "the installer, neodesk-setup",
}

// clientCall is what the hand-over needs from the process. Tests replace
// it.
type clientCall struct {
	// find returns the client's path, or false when there is none
	find func() (string, bool)
	// run runs the client with the arguments and returns its exit status
	// (on Linux it replaces this process and does not return)
	run    func(path string, args []string) int
	stderr io.Writer
}

// handToClient runs the desktop client when `neodesk args...` starts with
// one of its options, and reports whether it did. Without the client the
// call ends with a message and a non-zero exit.
func handToClient(args []string, c clientCall) (code int, handled bool) {
	if len(args) == 0 || !slices.Contains(clientFlags, args[0]) {
		return 0, false
	}
	path, ok := c.find()
	if !ok {
		fmt.Fprintf(c.stderr, "neodesk: %s is an option of the NeoDesk desktop client, which is not on this machine (%s).\n", args[0], clientCommand)
		fmt.Fprintln(c.stderr, "neodesk is the server's command line here; neodesk --help lists its commands.")
		return ndcli.ExitUsage, true
	}
	return c.run(path, args), true
}

// osClientCall is the process's own clientCall.
func osClientCall() clientCall {
	return clientCall{find: findClient, run: runClient, stderr: os.Stderr}
}

// findClient finds the desktop client at /usr/bin/neodesk on Linux: an
// executable file that is not this binary.
func findClient() (string, bool) {
	if runtime.GOOS != "linux" {
		return "", false
	}
	st, err := os.Stat(clientCommand)
	if err != nil || !st.Mode().IsRegular() || st.Mode()&0o111 == 0 {
		return "", false
	}
	if exe, err := os.Executable(); err == nil {
		if self, err := os.Stat(exe); err == nil && os.SameFile(st, self) {
			return "", false
		}
	}
	return clientCommand, true
}

// runClient runs the client in place of this process, as runSetup does
// with neodesk-setup.
func runClient(path string, args []string) int {
	err := syscall.Exec(path, append([]string{path}, args...), os.Environ())
	fmt.Fprintf(os.Stderr, "neodesk: could not start %s: %v\n", path, err)
	return ndcli.ExitFailed
}
