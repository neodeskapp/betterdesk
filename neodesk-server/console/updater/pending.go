package updater

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// ExitRestart is the exit status that asks the supervisor (systemd, the
// Windows service manager, Docker) to start the process again (DECISIONS
// A30).
const ExitRestart = 3

// MaxStarts is how many starts a new binary gets to become ready. The
// next start puts the previous binary back.
const MaxStarts = 3

// maxMarkerBytes caps the pending marker that is read.
const maxMarkerBytes = 64 << 10

// pendingMarker is <binary>.update-pending.json: an update, restore or
// repair that swapped the binary and waits for the new one to become
// ready.
type pendingMarker struct {
	Kind       string `json:"kind"`
	From       string `json:"from"`
	FromCommit string `json:"fromCommit"`
	To         string `json:"to"`
	ToCommit   string `json:"toCommit"`
	// PrevSHA256 is the SHA-256 of the binary kept as .prev, checked
	// before it is put back.
	PrevSHA256 string `json:"prevSha256"`
	Starts     int    `json:"starts"`
	// DataDir is the console's data folder, where the outcome is
	// recorded.
	DataDir string `json:"dataDir"`
	At      string `json:"at"`
}

// PendingInfo is the pending update as the install status shows it.
type PendingInfo struct {
	Kind   string `json:"kind"`
	From   string `json:"from"`
	To     string `json:"to"`
	Starts int    `json:"starts"`
	At     string `json:"at"`
}

func writeMarker(path string, m pendingMarker) error {
	text, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, text, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func readMarker(path string) (pendingMarker, error) {
	var m pendingMarker
	f, err := os.Open(path)
	if err != nil {
		return m, err
	}
	defer f.Close()
	buf := make([]byte, maxMarkerBytes+1)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return m, err
	}
	if n > maxMarkerBytes {
		return m, errors.New("the pending marker is too large")
	}
	if err := json.Unmarshal(buf[:n], &m); err != nil {
		return m, fmt.Errorf("the pending marker is no JSON: %w", err)
	}
	return m, nil
}

// StartupOptions are what StartupCheck needs.
type StartupOptions struct {
	// Args are the process's arguments after the program name.
	Args []string
	// Version and Commit are the running binary's.
	Version, Commit string
	// Executable finds the running binary. Nil means os.Executable.
	Executable func() (string, error)
	// Logf takes the lines the check logs. Nil drops them.
	Logf func(format string, args ...any)
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
	// RenameAside asks for the Windows way of a swap; on Windows it is
	// used anyway.
	RenameAside bool

	ops fileOps
}

// Pending is what StartupCheck found.
type Pending struct {
	rolledBack bool
	marker     *pendingMarker
	paths      BinaryPaths
	commit     string
	now        func() time.Time
	logf       func(format string, args ...any)
}

// RolledBack reports that the check put the previous binary back. The
// process must then end with ExitRestart, before it opens anything, so
// that the supervisor starts the previous binary.
func (p *Pending) RolledBack() bool { return p != nil && p.rolledBack }

// notStarting are arguments that print something and end; such a run is
// no start of the server and does not count.
var notStarting = map[string]bool{
	"-version": true, "--version": true, "-h": true, "-help": true, "--help": true,
	"-migrate-sqlite-auth": true, "--migrate-sqlite-auth": true,
	"-migrate-sqlite-auth-dry-run": true, "--migrate-sqlite-auth-dry-run": true,
}

// StartupCheck runs first in main, before the configuration and the
// database. It removes what earlier swaps left in the binary's folder,
// and when an update, restore or repair is waiting for this binary to
// become ready (the marker <binary>.update-pending.json), it counts this
// start. On the fourth start it puts the previous binary back, records
// the failure in the data folder and returns a Pending whose RolledBack
// is true. A marker written for another binary than this one is removed.
//
// It never stops the start because of its own trouble: a marker it
// cannot read or write is logged and left alone.
func StartupCheck(o StartupOptions) *Pending {
	if o.Executable == nil {
		o.Executable = os.Executable
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	if o.ops.rename == nil {
		o.ops = osFileOps()
	}
	p := &Pending{commit: o.Commit, now: o.Now, logf: o.Logf}
	for _, arg := range o.Args {
		name, _, _ := cutFlag(arg)
		if notStarting[name] {
			return p
		}
	}
	paths, err := ResolveBinaryPaths(o.Executable)
	if err != nil {
		o.Logf("update check: %v", err)
		return p
	}
	p.paths = paths
	for _, leftover := range paths.leftovers() {
		if err := os.Remove(leftover); err == nil {
			o.Logf("update check: removed %s", filepath.Base(leftover))
		}
	}

	m, err := readMarker(paths.Pending)
	if errors.Is(err, os.ErrNotExist) {
		return p
	}
	if err != nil {
		o.Logf("update check: %v; removing it", err)
		_ = os.Remove(paths.Pending)
		return p
	}
	if m.To != o.Version || (m.ToCommit != "" && o.Commit != "" && !shaMatches(m.ToCommit, o.Commit)) {
		o.Logf("update check: the pending %s to %s is not this binary (%s); removing the marker", m.Kind, m.To, o.Version)
		_ = os.Remove(paths.Pending)
		return p
	}
	m.Starts++
	if m.Starts <= MaxStarts {
		if err := writeMarker(paths.Pending, m); err != nil {
			o.Logf("update check: cannot count this start: %v", err)
			return p
		}
		o.Logf("update check: start %d of %d for %s %s", m.Starts, MaxStarts, m.Kind, m.To)
		p.marker = &m
		return p
	}

	detail := fmt.Sprintf("Version %s did not become ready in %d starts; version %s was put back", m.To, MaxStarts, m.From)
	s := swapper{paths: paths, ops: o.ops, renameAside: o.RenameAside || renameAsideByDefault, pid: os.Getpid(), now: o.Now}
	sum, sumErr := fileSHA256(paths.Prev)
	switch {
	case sumErr != nil:
		detail = fmt.Sprintf("Version %s did not become ready in %d starts, and the previous binary is missing: %v", m.To, MaxStarts, sumErr)
	case m.PrevSHA256 != "" && sum != m.PrevSHA256:
		detail = fmt.Sprintf("Version %s did not become ready in %d starts, and the previous binary was changed since the update; it was not put back", m.To, MaxStarts)
	default:
		if err := s.restorePrev(false); err != nil {
			detail = fmt.Sprintf("Version %s did not become ready in %d starts, and the previous binary could not be put back: %v", m.To, MaxStarts, err)
		} else {
			p.rolledBack = true
		}
	}
	o.Logf("update check: %s", detail)
	_ = os.Remove(paths.Pending)
	if dir, ok := checkedDataDir(m.DataDir); ok {
		files := dataFiles{dir: dir, now: o.Now}
		_ = files.markStale("rollback", detail, m.ToCommit)
		_ = files.persistResult(LastResult{
			SHA: m.ToCommit, Failed: []Failure{{File: "neodesk-server-update", Error: detail}},
			ServicesFailed: []ServiceFailure{}, ServicesRestarted: []string{}, RolledBack: p.rolledBack,
		})
	}
	return p
}

// Ready is called once the server listens and its database is migrated.
// After an update, restore or repair it removes the marker, writes the
// commit into the data folder's .update_sha, records the success and
// clears a stale marker.
func (p *Pending) Ready() {
	if p == nil || p.marker == nil {
		return
	}
	m := *p.marker
	p.marker = nil
	if err := os.Remove(p.paths.Pending); err != nil && !errors.Is(err, os.ErrNotExist) {
		p.logf("update check: cannot remove the pending marker: %v", err)
	}
	p.logf("update check: %s to %s is ready after %d start(s)", m.Kind, m.To, m.Starts)
	dir, ok := checkedDataDir(m.DataDir)
	if !ok {
		return
	}
	files := dataFiles{dir: dir, now: p.now}
	if ValidSHA(p.commit) {
		_ = files.saveSHA(p.commit)
	}
	files.clearStale()
	_ = files.persistResult(LastResult{
		SHA: p.commit, Applied: 1, Failed: []Failure{}, ServicesFailed: []ServiceFailure{},
		ServicesRestarted: []string{"server"}, ShaSaved: ValidSHA(p.commit),
	})
}

// checkedDataDir accepts the data folder a marker names when it is an
// absolute, clean path of a folder that exists.
func checkedDataDir(dir string) (string, bool) {
	if dir == "" || !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return "", false
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return "", false
	}
	return dir, true
}

// cutFlag splits "-name=value" into "-name" and "value".
func cutFlag(arg string) (name, value string, hasValue bool) {
	for i := 1; i < len(arg); i++ {
		if arg[i] == '=' {
			return arg[:i], arg[i+1:], true
		}
	}
	return arg, "", false
}

// renameAsideByDefault is the swap of this platform.
var renameAsideByDefault = runtime.GOOS == "windows"
