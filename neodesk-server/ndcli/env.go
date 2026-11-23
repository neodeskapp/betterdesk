package ndcli

import (
	"io"
	"net/http"
	"os"
	"time"

	"golang.org/x/term"
)

// Exit codes. They are stable: scripts may test for them.
const (
	ExitOK          = 0
	ExitFailed      = 1  // the server said no for another reason, or something unexpected
	ExitUsage       = 2  // a wrong command, argument or flag
	ExitAuth        = 3  // no token, or the token was refused (401)
	ExitForbidden   = 4  // the account or the token may not do this (403)
	ExitNotFound    = 5  // nothing with that ID or address (404)
	ExitRejected    = 6  // the server refused the input or it clashes (400, 409, 413, 422)
	ExitRateLimited = 7  // too many requests (429)
	ExitServer      = 8  // the server failed (5xx)
	ExitNetwork     = 9  // the console could not be reached, or TLS failed
	ExitConfig      = 10 // the config file cannot be used
)

// exitCodes describes the exit codes for --help and describe.
var exitCodes = []struct {
	Code    int    `json:"code"`
	Meaning string `json:"meaning"`
}{
	{ExitOK, "done"},
	{ExitFailed, "the server said no for another reason, or something unexpected went wrong"},
	{ExitUsage, "wrong command, argument or flag"},
	{ExitAuth, "no token, or the token was refused (HTTP 401)"},
	{ExitForbidden, "the account or the token may not do this (HTTP 403)"},
	{ExitNotFound, "nothing has that ID or address (HTTP 404)"},
	{ExitRejected, "the server refused the input or it clashes with what is there (HTTP 400, 409, 413, 422)"},
	{ExitRateLimited, "too many requests, try again later (HTTP 429)"},
	{ExitServer, "the server failed (HTTP 5xx)"},
	{ExitNetwork, "the console could not be reached, or its certificate was refused"},
	{ExitConfig, "the config file cannot be used"},
}

// LocalCommand runs one of the server binary's own commands.
type LocalCommand struct {
	Usage string
	Run   func(args []string, stdin io.Reader, stdout, stderr io.Writer) int
}

// Env is what the command line uses from its process. Tests replace it.
type Env struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
	// UserConfigDir is os.UserConfigDir.
	UserConfigDir func() (string, error)
	// Terminal reports whether a stream is a terminal.
	Terminal func(any) bool
	// ReadSecret reads a line from the terminal without showing it.
	ReadSecret func() (string, error)
	// Transport makes the HTTP requests; nil is the default one.
	Transport http.RoundTripper
	Now       func() time.Time
	// Version is the binary's version, for --version and the User-Agent.
	Version string
	// Local are the server binary's own commands for `neodesk local`.
	Local map[string]LocalCommand
}

// OSEnv is the process's own Env.
func OSEnv(version string, local map[string]LocalCommand) *Env {
	return &Env{
		Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr,
		Getenv:        os.Getenv,
		UserConfigDir: os.UserConfigDir,
		Terminal: func(s any) bool {
			f, ok := s.(*os.File)
			return ok && term.IsTerminal(int(f.Fd()))
		},
		ReadSecret: func() (string, error) {
			b, err := term.ReadPassword(int(os.Stdin.Fd()))
			return string(b), err
		},
		Now:     time.Now,
		Version: version,
		Local:   local,
	}
}
