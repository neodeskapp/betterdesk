package main

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"runtime"
	"slices"
	"strings"
	"syscall"

	"github.com/neodeskapp/neodesk/neodesk-server/console/onboard"
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
// one of its options or is a join or support link alone, and reports
// whether it did. Without the client the call ends with a message and a
// non-zero exit.
func handToClient(args []string, c clientCall) (code int, handled bool) {
	if len(args) == 1 {
		if forward, kind, ok := clientLink(args[0]); ok {
			path, found := c.find()
			if !found {
				fmt.Fprintf(c.stderr, "neodesk: this is a NeoDesk %s link for the desktop client, which is not on this machine (%s).\n", kind, clientCommand)
				fmt.Fprintf(c.stderr, "Install the NeoDesk desktop client, then run: neodesk --join '%s'\n", args[0])
				return ndcli.ExitUsage, true
			}
			return c.run(path, forward), true
		}
	}
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

// clientLink returns the arguments the client takes a join or support
// link with, and the link's kind, when the link has exactly the shape the
// server gives its links (console/onboard) for a valid code: a link
// <base>/join/<code> or <base>/s/<code> goes as --join <link>, which the
// client takes for both kinds, and neodesk://join|support/<host[:port]>/
// <code>[?http=1] as it is, which the client takes alone. Anything else,
// a trailing slash, a query, a code in lower case or with a dash, is not
// one.
func clientLink(link string) (forward []string, kind string, ok bool) {
	if rest, deep := strings.CutPrefix(link, "neodesk://"); deep {
		verb, rest, _ := strings.Cut(rest, "/")
		hostPort, code, _ := strings.Cut(rest, "/")
		scheme := "https"
		if c, plain := strings.CutSuffix(code, "?http=1"); plain {
			code, scheme = c, "http"
		}
		kind = map[string]string{"join": onboard.KindJoin, "support": onboard.KindSupport}[verb]
		console, valid := onboard.ParseConsole(scheme + "://" + hostPort)
		if kind == "" || !valid || !onboard.ValidFormat(code) || console.DeepLink(kind, code) != link {
			return nil, "", false
		}
		return []string{link}, kind, true
	}
	u, err := url.Parse(link)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, "", false
	}
	console, valid := onboard.ParseConsole(u.Scheme + "://" + u.Host)
	verb, code, _ := strings.Cut(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
	kind = map[string]string{"join": onboard.KindJoin, "s": onboard.KindSupport}[verb]
	if kind == "" || !valid || !onboard.ValidFormat(code) || console.Link(kind, code) != link {
		return nil, "", false
	}
	return []string{"--join", link}, kind, true
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
