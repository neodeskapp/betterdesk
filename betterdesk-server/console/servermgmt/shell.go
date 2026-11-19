package servermgmt

import (
	"math"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/unitronix/betterdesk-server/console/jsbind"
)

// The shells of the terminal on Linux and other Unix systems.
var (
	// fallbackShells are tried in this order after the account's own.
	fallbackShells = []string{"/bin/bash", "/usr/bin/bash", "/bin/zsh", "/bin/ash", "/bin/sh"}
	// allowedShellNames are the base names an account's shell may have.
	allowedShellNames = map[string]bool{"bash": true, "sh": true, "dash": true, "zsh": true, "ash": true}
)

// lastResortShell is the shell when no other one is there: /bin/sh, on
// Windows COMSPEC or else powershell.exe.
func (s *Service) lastResortShell() string {
	if s.windows() {
		return firstSet(s.opts.Getenv("COMSPEC"), "powershell.exe")
	}
	return "/bin/sh"
}

// Account is the account a terminal's shell runs as.
type Account struct {
	Username string
	// UID and GID are numbers as parseInt read them from /etc/passwd.
	UID, GID float64
	Home     string
	Shell    string
}

// allowedShellPath is a shell named by an absolute path without "..",
// whose base name is one of the allowed shells.
func allowedShellPath(shell string) bool {
	return strings.HasPrefix(shell, "/") && !strings.Contains(shell, "..") && allowedShellNames[path.Base(shell)]
}

// windowsShellRefused finds characters of command chaining in a Windows
// shell name.
var windowsShellRefused = regexp.MustCompile("[;&|`$()]")

// ResolveLoginShell picks the shell to start: shell when it is a known
// shell that exists and is executable, else the first fallback that is,
// else /bin/sh. On Windows it is shell (COMSPEC or powershell.exe when
// empty) unless it holds characters of command chaining.
func (s *Service) ResolveLoginShell(shell string) string {
	if s.windows() {
		winShell := firstSet(shell, s.lastResortShell())
		if !windowsShellRefused.MatchString(winShell) {
			return winShell
		}
		return s.lastResortShell()
	}
	candidates := fallbackShells
	if allowedShellPath(shell) {
		candidates = append([]string{shell}, fallbackShells...)
	}
	for _, candidate := range candidates {
		if s.opts.Terminal.Executable(candidate) {
			return candidate
		}
	}
	return s.lastResortShell()
}

// passwdEntry is one line of /etc/passwd as parsePasswd read it.
type passwdEntry struct {
	username string
	uid, gid float64
	home     string
	shell    string
}

// parsePasswd reads /etc/passwd: lines without white space at their ends,
// empty ones skipped, user and group id read with parseInt and lines
// where either is no integer left out. An empty home is "/", an empty
// shell the last resort.
func parsePasswd(text, lastResort string) []passwdEntry {
	var out []passwdEntry
	for _, line := range strings.Split(text, "\n") {
		line = jsbind.Trim(line)
		if line == "" {
			continue
		}
		p := strings.Split(line, ":")
		field := func(i int) string {
			if i < len(p) {
				return p[i]
			}
			return ""
		}
		uid, uidOK := parseIntField(field(2))
		gid, gidOK := parseIntField(field(3))
		if !uidOK || !gidOK {
			continue
		}
		out = append(out, passwdEntry{
			username: field(0), uid: uid, gid: gid,
			home: firstSet(field(5), "/"), shell: firstSet(field(6), lastResort),
		})
	}
	return out
}

// parseIntField is parseInt(text, 10) for a passwd field, ok when
// Number.isInteger holds for the result: not NaN, not infinite.
func parseIntField(text string) (float64, bool) {
	n := parseIntJS(text)
	if math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, false
	}
	return n, true
}

// validLoginShell is a shell that lets one log in: not empty, without
// "nologin" in it and not ending in "false", in any letter case.
var noLogin = regexp.MustCompile(`(?i)nologin|false$`)

func validLoginShell(shell string) bool { return shell != "" && !noLogin.MatchString(shell) }

// PickTerminalUser chooses the account the shell runs as
// (pickTerminalUser):
//
//   - BETTERDESK_TERMINAL_USER, else SERVER_MANAGEMENT_USER: the account of
//     that name or user id in /etc/passwd, when its shell lets one log in;
//   - when the console runs as root: the first account with a user id from
//     1000 to 59999 that is not "nobody", has a login shell and a home
//     other than "/";
//   - else the console's own account, with SHELL or its own shell.
//
// On Windows it is the console's own account with COMSPEC or
// powershell.exe. The shell is passed through ResolveLoginShell. Only a
// console running as root switches to the account; otherwise the shell
// runs as the console's account whatever the result says.
func (s *Service) PickTerminalUser() Account {
	t := s.opts.Terminal
	current := t.CurrentUser()
	if s.windows() {
		current.Shell = s.lastResortShell()
		return current
	}
	text, _ := t.Passwd()
	passwd := parsePasswd(text, s.lastResortShell())
	configured := firstSet(s.opts.Getenv("BETTERDESK_TERMINAL_USER"), s.opts.Getenv("SERVER_MANAGEMENT_USER"))
	if configured != "" {
		for _, u := range passwd {
			if u.username == configured || jsbind.NumberString(u.uid) == configured {
				if validLoginShell(u.shell) {
					return Account{Username: u.username, UID: u.uid, GID: u.gid, Home: u.home, Shell: s.ResolveLoginShell(u.shell)}
				}
				break
			}
		}
	}
	if t.IsRoot() {
		for _, u := range passwd {
			if u.uid >= 1000 && u.uid < 60000 && u.username != "nobody" && validLoginShell(u.shell) && u.home != "" && u.home != "/" {
				return Account{Username: u.username, UID: u.uid, GID: u.gid, Home: u.home, Shell: s.ResolveLoginShell(u.shell)}
			}
		}
	}
	loginShell := ""
	if sh := s.opts.Getenv("SHELL"); validLoginShell(sh) {
		loginShell = sh
	} else if validLoginShell(current.Shell) {
		loginShell = current.Shell
	}
	current.Home = firstSet(current.Home, s.home())
	current.Shell = s.ResolveLoginShell(loginShell)
	return current
}

// inheritedEnv are the variables a shell takes from the console's
// environment, on Windows also those its shells need to start. Nothing
// else of that environment reaches the shell: it holds the database
// address and the keys.
var (
	inheritedEnv        = []string{"PATH"}
	inheritedEnvWindows = []string{"PATH", "COMSPEC", "SystemRoot", "PATHEXT", "TEMP", "TMP", "USERPROFILE"}
)

// shellEnv is the environment of a shell: the inherited variables as the
// console has them, then the account, the terminal type and size
// (buildSpawnOptions).
func (s *Service) shellEnv(a Account, cols, rows int, pty bool) []string {
	term := "dumb"
	if pty {
		term = "xterm-256color"
	}
	keys := inheritedEnv
	if s.windows() {
		keys = inheritedEnvWindows
	}
	console := s.opts.Terminal.Environ()
	var env []string
	for _, key := range keys {
		for _, entry := range console {
			name, _, _ := strings.Cut(entry, "=")
			if name == key || s.windows() && strings.EqualFold(name, key) {
				env = append(env, entry)
				break
			}
		}
	}
	set := [][2]string{
		{"HOME", firstSet(a.Home, s.home())},
		{"USER", a.Username},
		{"LOGNAME", a.Username},
		{"SHELL", firstSet(a.Shell, s.lastResortShell())},
		{"TERM", term},
		{"LANG", firstSet(s.opts.Getenv("LANG"), "en_US.UTF-8")},
		{"COLUMNS", strconv.Itoa(cols)},
		{"LINES", strconv.Itoa(rows)},
	}
	if pty {
		// node-pty named the working directory in PWD; pipes kept the
		// console's own
		set = append(set, [2]string{"PWD", firstSet(a.Home, s.home())})
	}
	for _, kv := range set {
		env = setEnv(env, kv[0], kv[1], s.windows())
	}
	return env
}

// setEnv sets key in an environment list, replacing the entry that has
// it (in any letter case on Windows).
func setEnv(env []string, key, value string, foldCase bool) []string {
	for i, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if name == key || foldCase && strings.EqualFold(name, key) {
			env[i] = key + "=" + value
			return env
		}
	}
	return append(env, key+"="+value)
}

// executableFile reports a regular file the process may execute
// (isExecutableFile: stat, then access with X_OK).
func executableFile(file string) bool {
	info, err := os.Stat(file)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	return canExecute(file)
}
