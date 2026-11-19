// Package servermgmt is the server management of the console (beta): what
// the machine the console runs on looks like (processor, memory, disks,
// network), a file browser confined to a list of folders, the control of
// BetterDesk's system services, and a terminal: a shell on the machine in
// the browser (/ws/server-management/terminal).
//
// Everything here acts on the machine with the rights of the console's
// account. The routes and the socket let only accounts with the permission
// server.config in, and every path the file browser gets goes through the
// folder lists (see Roots) before anything is opened, written, moved or
// removed; service names and actions go through fixed allow lists.
package servermgmt

import (
	"context"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Options configure the service. A zero field is the real thing.
type Options struct {
	// Getenv reads the environment as the service reads it on every call:
	// BETTERDESK_PATH, RUSTDESK_DIR and RUSTDESK_PATH (the server's folder,
	// default /opt/betterdesk or C:\BetterDesk),
	// BETTERDESK_CONSOLE_PATH (the console's folder, default WorkDir),
	// BETTERDESK_FILE_ALLOWED_ROOTS and BETTERDESK_MUTABLE_ROOTS (more
	// folders to read and to change, separated by commas), HOME and
	// USERPROFILE, TMPDIR, TMP and TEMP (the temp folder), and for the
	// terminal BETTERDESK_TERMINAL_USER, SERVER_MANAGEMENT_USER, SHELL,
	// COMSPEC and LANG. Default os.Getenv.
	Getenv func(string) string
	// WorkDir is the folder relative paths are taken from, and the
	// console's folder when BETTERDESK_CONSOLE_PATH is not set. The old
	// console used its working directory, which its service definitions
	// set to the console's folder; the console passes CONSOLE_DIR.
	// Default: the working directory.
	WorkDir string
	// LogDir and TempDirs are the folders every install may read besides
	// the configured ones: /var/log, and /tmp and /var/tmp. Tests point
	// them elsewhere.
	LogDir   string
	TempDirs []string
	// GOOS is the operating system the service acts for. Default
	// runtime.GOOS.
	GOOS string
	// Run runs a command without a shell and returns what it printed and
	// its exit code: -1 when a signal ended it. err is a command that
	// could not start. The commands are systemctl, df, wmic and
	// powershell. Default: os/exec with ctx's deadline, the process killed
	// when it passes.
	Run func(ctx context.Context, name string, args ...string) (stdout, stderr []byte, exitCode int, err error)
	// RestartSelf is asked when a service action would restart the
	// console's own service (betterdesk-server, BetterDeskServer): true
	// means the process ends by itself so that its supervisor starts it
	// again, and nothing is run. Nil runs the action like any other.
	RestartSelf func() bool
	// Now is the clock. Default time.Now.
	Now func() time.Time
	// Started is when the process started, for its uptime. Default: when
	// New was called.
	Started time.Time
	// Terminal configures the terminal socket (terminal.go).
	Terminal TerminalOptions

	// host reads the machine; tests give a made-up one.
	host func() hostReading
	// beforeChange sees every path the file browser is about to write,
	// create, move or remove, after all checks. Tests use it to stop a
	// change outside their folder; nil lets everything through.
	beforeChange func(paths ...string) error
}

// Service is the server management of one console process.
type Service struct {
	opts Options
	host *Host

	ptyOnce sync.Once
	ptyOK   bool

	// termMu guards the terminals with a shell (by session id) and the
	// number of the last terminal opened
	termMu    sync.Mutex
	terminals map[string]*terminal
	nextTerm  int64
}

// New returns the service.
func New(opts Options) *Service {
	if opts.Getenv == nil {
		opts.Getenv = os.Getenv
	}
	if opts.WorkDir == "" {
		opts.WorkDir, _ = os.Getwd()
	}
	if opts.LogDir == "" {
		opts.LogDir = "/var/log"
	}
	if opts.TempDirs == nil {
		opts.TempDirs = []string{"/tmp", "/var/tmp"}
	}
	if opts.GOOS == "" {
		opts.GOOS = runtime.GOOS
	}
	if opts.Run == nil {
		opts.Run = runCommand
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Started.IsZero() {
		opts.Started = opts.Now()
	}
	if opts.host == nil {
		opts.host = readHost
	}
	s := &Service{opts: opts, terminals: map[string]*terminal{}}
	s.host = &Host{read: opts.host, now: opts.Now}
	s.opts.Terminal = s.terminalDefaults(opts.Terminal)
	return s
}

// windows reports whether the service acts for Windows.
func (s *Service) windows() bool { return s.opts.GOOS == "windows" }

// linux reports whether the service acts for Linux.
func (s *Service) linux() bool { return s.opts.GOOS == "linux" }

// Platform is process.platform: the name Node gave the operating system.
func (s *Service) Platform() string { return nodePlatform(s.opts.GOOS) }

// nodePlatform names an operating system as process.platform did.
func nodePlatform(goos string) string {
	if goos == "windows" {
		return "win32"
	}
	return goos
}

// Arch is process.arch: the name Node gave the processor architecture.
func Arch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	case "mips64le":
		return "mips64el"
	case "mipsle":
		return "mipsel"
	}
	return runtime.GOARCH
}

// home is os.homedir(): HOME (USERPROFILE on Windows) when it is set,
// else the account's folder.
func (s *Service) home() string {
	key := "HOME"
	if s.windows() {
		key = "USERPROFILE"
	}
	if h := s.opts.Getenv(key); h != "" {
		return h
	}
	if u, err := user.Current(); err == nil {
		return u.HomeDir
	}
	return ""
}

// tempDir is os.tmpdir(): TMPDIR, TMP or TEMP, else the first of TempDirs
// (/tmp), without a slash at the end. On Windows TEMP or TMP, else the
// Windows folder's
// temp, without a backslash at the end unless it is a drive's root.
func (s *Service) tempDir() string {
	env := s.opts.Getenv
	if s.windows() {
		dir := firstSet(env("TEMP"), env("TMP"))
		if dir == "" {
			dir = firstSet(env("SystemRoot"), env("windir")) + `\temp`
		}
		if len(dir) > 1 && strings.HasSuffix(dir, `\`) && !strings.HasSuffix(dir, `:\`) {
			dir = dir[:len(dir)-1]
		}
		return dir
	}
	fallback := "/tmp"
	if len(s.opts.TempDirs) > 0 {
		fallback = s.opts.TempDirs[0]
	}
	dir := firstSet(env("TMPDIR"), env("TMP"), env("TEMP"), fallback)
	if len(dir) > 1 && strings.HasSuffix(dir, "/") {
		dir = dir[:len(dir)-1]
	}
	return dir
}

func firstSet(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// resolve is path.resolve(p): p made absolute against WorkDir and
// cleaned, without a separator at the end.
func (s *Service) resolve(p string) string {
	if s.windows() {
		return resolveWindows(s.opts.WorkDir, p)
	}
	if strings.HasPrefix(p, "/") {
		return filepath.Clean(p)
	}
	return filepath.Clean(s.opts.WorkDir + "/" + p)
}

// resolveWindows is path.win32.resolve for the paths the file browser
// gets: a path with a drive and a backslash or slash after it is absolute,
// one that starts with a separator is on WorkDir's drive, and anything
// else lies below WorkDir.
func resolveWindows(workDir, p string) string {
	switch {
	case len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/'):
		return filepath.Clean(p)
	case strings.HasPrefix(p, `\\`) || strings.HasPrefix(p, "//"):
		return filepath.Clean(p)
	case strings.HasPrefix(p, `\`) || strings.HasPrefix(p, "/"):
		return filepath.Clean(filepath.VolumeName(workDir) + p)
	case len(p) >= 2 && p[1] == ':':
		abs, err := filepath.Abs(p)
		if err != nil {
			return filepath.Clean(p)
		}
		return abs
	}
	return filepath.Clean(workDir + `\` + p)
}
