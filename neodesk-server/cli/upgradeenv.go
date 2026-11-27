package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/serverinfo"
	"github.com/neodeskapp/neodesk/neodesk-server/internal/envvar"
)

const upgradeEnvUsage = "Usage: neodesk-server upgrade-env --from FILE [--write] [--supervisor systemd|windows-service|docker]"

// The groups of spec updater-installers.md 4.3 ("Env keys carried into the
// Go server") that upgrade-env changes. Every other key is carried as it
// is: the keys of group A, the listener keys of group B (the Go console
// has those listeners), the installer's own keys and the server's.
var (
	// commentedKeys are commented out with a note: the web app's build on
	// disk (WEB_UI_DIR, group C) and what only the Node console's updater
	// and build workers read (group D).
	commentedKeys = map[string]string{
		"WEB_UI_DIR":                    "the web app is built into the server; this would serve an old build from disk",
		"NEODESK_GOTOOLCHAIN":           "only the old console's updater built the server from source",
		"GOTOOLCHAIN":                   "only the old console's updater built the server from source",
		"GOCACHE":                       "only the old console's updater built the server from source",
		"GOMODCACHE":                    "only the old console's updater built the server from source",
		"HOME":                          "only the old console's updater built the server from source",
		"USERPROFILE":                   "only the old console's updater built the server from source",
		"AGENT_CLIENT_ARTIFACT_DIR":     "no build worker reads it",
		"AGENT_CLIENT_BUILD_POLL_MS":    "no build worker reads it",
		"AGENT_CLIENT_BUILD_TIMEOUT_MS": "no build worker reads it",
		"AGENT_CLIENT_SOURCE_DIR":       "no build worker reads it",
		"AGENT_LIB_DIR":                 "no build worker reads it",
		"BUILD_CACHE_DIR":               "no build worker reads it",
		"BUILD_USER":                    "no build worker reads it",
		"CARGO_BIN":                     "no build worker reads it",
		"CARGO_HOME":                    "no build worker reads it",
		"CARGO_TARGET_DIR":              "no build worker reads it",
		"GO_BIN":                        "no build worker reads it",
		"NPM_BIN":                       "no build worker reads it",
		"RDCLIENT_ARTIFACT_DIR":         "no build worker reads it",
		"RDCLIENT_BUILD_POLL_MS":        "no build worker reads it",
		"RDCLIENT_BUILD_TIMEOUT_MS":     "no build worker reads it",
		"RUSTUP_HOME":                   "no build worker reads it",
	}
	// removedKeys are taken out: they belonged to Node itself or to the
	// time Node ran behind the Go console.
	removedKeys = map[string]string{
		"NODE_EXTRA_CA_CERTS":   "the console calls the server's API inside the process",
		"CONSOLE_NODE_UPSTREAM": "there is no Node console to pass requests to",
	}
)

// upgradeSetting is a key upgrade-env adds, with its value.
type upgradeSetting struct{ Key, Value string }

// upgradePlan is what upgrade-env does to one env file.
type upgradePlan struct {
	// Text is the new content of the file.
	Text string
	// Kept are the keys carried as they are, in the order of the file.
	Kept []string
	// FromPort is the console port taken over from PORT; empty when the
	// file had CONSOLE_PORT already.
	FromPort string
	// Added are the keys appended, in order.
	Added []upgradeSetting
	// Commented and Removed are the keys commented out and taken out.
	Commented, Removed []string
}

// Changed reports whether the plan changes the file.
func (p upgradePlan) Changed() bool {
	return len(p.Added) > 0 || len(p.Commented) > 0 || len(p.Removed) > 0
}

// byteOrderMark is U+FEFF in UTF-8, which a file written on Windows may
// start with.
const byteOrderMark = "\xef\xbb\xbf"

// errNotAPort is a PORT the old console could not have listened on.
var errNotAPort = errors.New("not a port")

// planUpgrade works out the env file of the Go server from the old
// console's. Lines it does not change stay byte for byte and in place,
// with their line ends; added lines go at the end with the file's line
// end. The file is read as the server's env loader reads it
// (serverinfo.ParseEnvLines, line by line).
//
// envFile is the file's absolute path: it is CONSOLE_DIR's folder and
// NEODESK_ENV_FILE's value. supervisor is NEODESK_SUPERVISOR's.
func planUpgrade(text, envFile, supervisor string) (upgradePlan, error) {
	var plan upgradePlan
	bom := ""
	if rest, ok := strings.CutPrefix(text, byteOrderMark); ok {
		bom, text = byteOrderMark, rest
	}
	lineEnd := "\n"
	if strings.Contains(text, "\r\n") {
		lineEnd = "\r\n"
	}

	var out strings.Builder
	out.WriteString(bom)
	present := map[string]bool{}
	values := map[string]string{}
	kept := map[string]bool{}
	commented := map[string]bool{}
	removed := map[string]bool{}
	for rest := text; rest != ""; {
		line, ending := rest, ""
		if i := strings.IndexByte(rest, '\n'); i >= 0 {
			line, ending, rest = rest[:i], "\n", rest[i+1:]
			if strings.HasSuffix(line, "\r") {
				line, ending = line[:len(line)-1], "\r\n"
			}
		} else {
			rest = ""
		}
		entry := serverinfo.ParseEnvLines(line).Entries[0]
		if entry.Type != serverinfo.EnvPair {
			out.WriteString(line + ending)
			continue
		}
		key := entry.Key
		// The loader keeps the first value that is not empty
		if value := jsbind.Trim(entry.Value); values[key] == "" {
			values[key] = value
		}
		present[key] = true
		if _, ok := removedKeys[key]; ok {
			if !removed[key] {
				removed[key] = true
				plan.Removed = append(plan.Removed, key)
			}
			continue
		}
		if why, ok := commentedKeys[key]; ok {
			if !commented[key] {
				commented[key] = true
				plan.Commented = append(plan.Commented, key)
			}
			// The note ends its line also before a last line without one
			noteEnd := ending
			if noteEnd == "" {
				noteEnd = lineEnd
			}
			out.WriteString("# Commented out by neodesk-server upgrade-env: " + why + noteEnd)
			out.WriteString("# " + line + ending)
			continue
		}
		if !kept[key] {
			kept[key] = true
			plan.Kept = append(plan.Kept, key)
		}
		out.WriteString(line + ending)
	}

	add := func(key, value string) {
		if !present[key] {
			plan.Added = append(plan.Added, upgradeSetting{key, value})
		}
	}
	if !present["CONSOLE_PORT"] {
		// The port the old console listened on, parseInt(PORT, 10), stays
		// the console's. Without one the server's default applies (3375
		// since 3.6.0-rc.5, DECISIONS A250): the old console's default,
		// 5000, is not written down
		if n, ok := parseIntPrefix(values["PORT"]); ok && n != 0 {
			if n < 1 || n > 65535 {
				return upgradePlan{}, fmt.Errorf("PORT=%s: %w", values["PORT"], errNotAPort)
			}
			plan.FromPort = strconv.FormatInt(n, 10)
			add("CONSOLE_PORT", plan.FromPort)
		}
	}
	add("HOST", "127.0.0.1")
	add("SIGNAL_PORT", "21116")
	add("RELAY_PORT", "21117")
	add("GO_API_PORT", "21114")
	add("CONSOLE_DIR", filepath.Dir(envFile))
	add("NEODESK_ENV_FILE", envFile)
	add("NEODESK_SUPERVISOR", supervisor)

	if len(plan.Added) > 0 {
		// After the old lines, ended, and a blank line
		if body := out.String()[len(bom):]; body != "" {
			if !strings.HasSuffix(body, "\n") {
				out.WriteString(lineEnd)
			}
			out.WriteString(lineEnd)
		}
		out.WriteString("# Added by neodesk-server upgrade-env: the console runs inside the server" + lineEnd)
		for _, s := range plan.Added {
			out.WriteString(s.Key + "=" + s.Value + lineEnd)
		}
	}
	plan.Text = out.String()
	return plan, nil
}

// UpgradeEnv is "upgrade-env --from FILE [--write]": it turns the old
// console's env file into the Go server's (spec updater-installers.md
// 4.3). Without --write it prints the plan and changes nothing; with it,
// it keeps the old file as FILE.pre-go (an existing one is never replaced)
// and writes the new content into FILE itself, so the file keeps its
// owner, mode and access rules. A file that needs no change is left alone.
//
// It prints key names and the values it adds, never a value it carries.
// Exit status: 0 done, 1 the file cannot be read or written or PORT is not
// a port, 2 a usage error.
func UpgradeEnv(args []string, env Env) int {
	from, write := "", false
	supervisor := "systemd"
	switch env.GOOS {
	case "windows":
		supervisor = "windows-service"
	}
	if envvar.SwitchOr(env.Getenv("DOCKER"), false) {
		supervisor = "docker"
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--from", "--supervisor":
			if i+1 >= len(args) || args[i+1] == "" {
				fmt.Fprintln(env.Stderr, upgradeEnvUsage)
				return 2
			}
			if args[i] == "--from" {
				from = args[i+1]
			} else {
				supervisor = args[i+1]
			}
			i++
		case "--write":
			write = true
		default:
			fmt.Fprintln(env.Stderr, upgradeEnvUsage)
			return 2
		}
	}
	if from == "" {
		fmt.Fprintln(env.Stderr, upgradeEnvUsage)
		return 2
	}
	path, err := filepath.Abs(from)
	if err != nil {
		path = from
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(env.Stderr, "neodesk-server upgrade-env: "+format+"\n", a...)
		return 1
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fail("read %s: %v", path, err)
	}
	plan, err := planUpgrade(string(raw), path, supervisor)
	switch {
	case errors.Is(err, errNotAPort):
		return fail("%v: set CONSOLE_PORT in %s and run this again", err, path)
	case err != nil:
		return fail("%v", err)
	}

	o := env.Stdout
	fmt.Fprintf(o, "upgrade-env: %s\n", path)
	for _, key := range plan.Kept {
		fmt.Fprintf(o, "  keep     %s\n", key)
	}
	if plan.FromPort != "" {
		fmt.Fprintf(o, "  map      PORT -> CONSOLE_PORT=%s\n", plan.FromPort)
	}
	for _, s := range plan.Added {
		if s.Key != "CONSOLE_PORT" {
			fmt.Fprintf(o, "  add      %s=%s\n", s.Key, s.Value)
		}
	}
	for _, key := range plan.Commented {
		fmt.Fprintf(o, "  comment  %s: %s\n", key, commentedKeys[key])
	}
	for _, key := range plan.Removed {
		fmt.Fprintf(o, "  remove   %s: %s\n", key, removedKeys[key])
	}
	switch {
	case !plan.Changed():
		fmt.Fprintln(o, "Nothing to change.")
		return 0
	case !write:
		fmt.Fprintln(o, "Nothing written: run again with --write to apply.")
		return 0
	}

	backup := path + ".pre-go"
	kept, err := keepBackup(backup, raw)
	if err != nil {
		return fail("keep the old file as %s: %v", backup, err)
	}
	if err := rewrite(path, []byte(plan.Text)); err != nil {
		return fail("write %s: %v (the old file is %s)", path, err, backup)
	}
	if kept {
		fmt.Fprintf(o, "Written: %s (the old file is %s)\n", path, backup)
	} else {
		fmt.Fprintf(o, "Written: %s (%s from an earlier run is kept)\n", path, backup)
	}
	return 0
}

// keepBackup writes the old content to backup with mode 0600, unless the
// file is there already. It reports whether it wrote one.
func keepBackup(backup string, content []byte) (bool, error) {
	f, err := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		return false, err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return false, err
	}
	return true, f.Close()
}

// rewrite replaces a file's content in place, which keeps its owner, mode
// and access rules.
func rewrite(path string, content []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0)
	if err != nil {
		return err
	}
	if _, err := f.Write(content); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
