// Package servicedef keeps the service definition of the NeoDesk
// server in shape for the console's settings (the part of
// services/updateService.js that sanitizeGoServerServiceConfig was): the
// server's API stays on plain HTTP, the mesh is on, the server reads the
// console's .env file and keeps its own ports.
//
// On Linux that is the systemd unit of neodesk-server, rewritten in
// place and followed by a reload of the units through the privileged
// helper. On Windows it is the NSSM service's parameters and environment,
// and the billing clock keys of the console's .env are copied into the
// server's environment there, because the server does not read that
// file on Windows.
package servicedef

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/advancedconfig"
)

// Result is what a check did, as the console's answers show it:
//
//	{"changed": false, "changes": [], "error": null, "needsRestart": false}
type Result struct {
	Changed bool     `json:"changed"`
	Changes []string `json:"changes"`
	// Error is the message of what stopped the check, null without one.
	Error        *string `json:"error"`
	NeedsRestart bool    `json:"needsRestart"`
}

// Options are the settings of a Service.
type Options struct {
	// Windows says the console runs on Windows (process.platform ===
	// 'win32').
	Windows bool
	// ServerService and ConsoleService are the names of the two services:
	// neodesk-server and neodesk-console, on Windows NeoDeskServer
	// and NeoDeskConsole.
	ServerService, ConsoleService string
	// ConsoleEnvPath is the console's .env file (web-nodejs/.env).
	ConsoleEnvPath string
	// Shell runs a command line through the system's shell and returns
	// what it wrote to standard output (execSync, five seconds). Nil means
	// RunShell.
	Shell func(ctx context.Context, command string) (string, error)
	// Exec runs a program without a shell (execFileSync, five seconds).
	// Nil means RunFile.
	Exec func(ctx context.Context, name string, args ...string) error
	// DaemonReload reloads systemd's units through the privileged helper
	// (lib/privilegedUpdateHelper.js). Nil fails with
	// ErrNoPrivilegedHelper.
	DaemonReload func(ctx context.Context) error
	// Stat, ReadFile and WriteFile reach the files. Nil means the os
	// package.
	Stat      func(string) (os.FileInfo, error)
	ReadFile  func(string) ([]byte, error)
	WriteFile func(string, []byte) error
}

// ErrNoPrivilegedHelper is the failure of a units reload without a
// privileged helper.
var ErrNoPrivilegedHelper = errors.New("Privileged update failed")

// unitHint is what a refused read or write of the unit adds to its
// message: the installer's update writes the unit as it should be.
const unitHint = "Run once as root: sudo neodesk-setup --update"

// allowedUnits are the unit files the privileged helper may touch.
var allowedUnits = map[string]bool{
	"/etc/systemd/system/neodesk-server.service":  true,
	"/etc/systemd/system/neodesk-console.service": true,
}

// Service checks the server's service definition.
type Service struct {
	opts Options
}

// New returns a Service.
func New(opts Options) *Service {
	if opts.Shell == nil {
		opts.Shell = RunShell
	}
	if opts.Exec == nil {
		opts.Exec = RunFile
	}
	if opts.DaemonReload == nil {
		opts.DaemonReload = func(context.Context) error { return ErrNoPrivilegedHelper }
	}
	if opts.Stat == nil {
		opts.Stat = os.Stat
	}
	if opts.ReadFile == nil {
		opts.ReadFile = os.ReadFile
	}
	if opts.WriteFile == nil {
		opts.WriteFile = func(name string, data []byte) error { return os.WriteFile(name, data, 0o666) }
	}
	return &Service{opts: opts}
}

// Check brings the service definition up to date and reports what it
// changed. It does not fail: what stops it is the result's error.
func (s *Service) Check(ctx context.Context) Result {
	result := Result{Changes: []string{}}
	var err error
	if s.opts.Windows {
		err = s.checkWindows(ctx, &result)
	} else {
		err = s.checkSystemd(ctx, &result)
	}
	if err != nil {
		message := err.Error()
		result.Error = &message
	}
	return result
}

// checkSystemd is the Linux side: the unit of the server service.
func (s *Service) checkSystemd(ctx context.Context, result *Result) error {
	name := s.opts.ServerService
	out, err := s.opts.Shell(ctx, "systemctl show "+shellQuote(name)+" --property=FragmentPath --value 2>/dev/null || true")
	if err != nil {
		return err
	}
	fragment := jsTrim(out)
	if fragment == "" {
		fragment = "/etc/systemd/system/" + name + ".service"
	}
	if _, err := s.opts.Stat(fragment); err != nil {
		return nil
	}
	data, err := s.opts.ReadFile(fragment)
	if err != nil {
		return s.privilegedError(err, fragment, "Run the update preparation step as root.")
	}
	original := advancedconfig.DecodeUTF8(data)
	clean := StripIncompatibleGoAPITLSArgs(original, false)
	clean = strings.ReplaceAll(clean, "Environment=HBBS_API_URL=https://localhost", "Environment=HBBS_API_URL=http://localhost")
	clean = strings.ReplaceAll(clean, "Environment=NEODESK_API_URL=https://localhost", "Environment=NEODESK_API_URL=http://localhost")

	if lineStarting(clean, "Environment=MESH_ENABLED=") < 0 {
		clean = insertServiceLine(clean, "Environment=MESH_ENABLED=Y")
		result.NeedsRestart = true
		result.Changes = append(result.Changes, "set MESH_ENABLED=Y in neodesk-server systemd unit")
	}
	if text, changed := EnsureGoServerEnvironmentFile(clean, s.opts.ConsoleEnvPath); changed {
		clean = text
		result.NeedsRestart = true
		result.Changes = append(result.Changes, "set EnvironmentFile for console .env on neodesk-server systemd unit")
	}
	if text, changed := EnsureGoServerSignalRelayPorts(clean); changed {
		clean = text
		result.NeedsRestart = true
		result.Changes = append(result.Changes, "set SIGNAL_PORT=21116 / RELAY_PORT=21117 / GO_API_PORT=21114 on neodesk-server systemd unit")
	}

	if clean == original {
		return nil
	}
	if err := s.opts.WriteFile(fragment, []byte(clean)); err != nil {
		return s.privilegedError(err, fragment, "")
	}
	if err := s.opts.DaemonReload(ctx); err != nil {
		return err
	}
	result.Changed = true
	mesh := false
	for _, change := range result.Changes {
		if strings.Contains(change, "MESH_ENABLED") {
			mesh = true
		}
	}
	if !mesh {
		result.Changes = append(result.Changes, "patched neodesk-server systemd unit")
	}
	return nil
}

// privilegedError adds the hint to a refused read or write of the unit.
// otherHint is what a refused read of a file the helper may not touch
// gets; a write always gets the unit hint.
func (s *Service) privilegedError(err error, path, otherHint string) error {
	err = fileError(err, path)
	if !errors.Is(err, os.ErrPermission) && !isEPERM(err) {
		return err
	}
	hint := unitHint
	if otherHint != "" && !s.isAllowedUnit(path) {
		hint = otherHint
	}
	return errors.New(err.Error() + ". " + hint)
}

// isAllowedUnit is isAllowedSystemdUnitPath: an absolute path that is,
// after its links, one of the two unit files.
func (s *Service) isAllowedUnit(path string) bool {
	trimmed := jsTrim(path)
	if !strings.HasPrefix(trimmed, "/") {
		return false
	}
	return allowedUnits[cleanPath(trimmed)]
}

// checkWindows is the Windows side: the NSSM services.
func (s *Service) checkWindows(ctx context.Context, result *Result) error {
	server := s.opts.ServerService
	args, err := s.opts.Shell(ctx, `nssm get "`+server+`" AppParameters 2>nul`)
	if err != nil {
		return err
	}
	if clean := StripIncompatibleGoAPITLSArgs(args, true); clean != jsTrim(args) {
		if err := s.opts.Exec(ctx, "nssm", "set", server, "AppParameters", clean); err != nil {
			return err
		}
		result.Changed = true
		result.Changes = append(result.Changes, "removed Go API TLS flags from NSSM service parameters")
	}

	// The three steps below fail quietly: a service may not exist
	if env, err := s.opts.Shell(ctx, `nssm get "`+server+`" AppEnvironmentExtra 2>nul`); err == nil {
		if text, changed := EnsureMeshEnabledInServiceEnv(env); changed {
			if s.opts.Exec(ctx, "nssm", "set", server, "AppEnvironmentExtra", text) == nil {
				result.Changed, result.NeedsRestart = true, true
				result.Changes = append(result.Changes, "set MESH_ENABLED=Y on NeoDesk Go Server NSSM environment")
			}
		}
	}
	if changed, err := s.syncBillingEnv(ctx); err == nil && changed {
		result.Changed, result.NeedsRestart = true, true
		result.Changes = append(result.Changes, "synced billing/NTP env to NeoDesk Go Server NSSM environment")
	}
	console := s.opts.ConsoleService
	if env, err := s.opts.Shell(ctx, `nssm get "`+console+`" AppEnvironmentExtra 2>nul`); err == nil {
		clean := strings.ReplaceAll(env, "HBBS_API_URL=https://localhost", "HBBS_API_URL=http://localhost")
		clean = strings.ReplaceAll(clean, "NEODESK_API_URL=https://localhost", "NEODESK_API_URL=http://localhost")
		if clean != env && s.opts.Exec(ctx, "nssm", "set", console, "AppEnvironmentExtra", clean) == nil {
			result.Changed = true
			result.Changes = append(result.Changes, "kept console Go API URLs on HTTP in NSSM environment")
		}
	}
	return nil
}

// BillingEnvKeys are the keys of the console's .env file that the server
// needs on Windows.
var billingEnvKeys = []string{
	"NTP_SERVERS",
	"BILLING_MAX_CLOCK_SKEW_MS",
	"BILLING_REQUIRE_SYNCED_CLOCK",
	"BILLING_TRUST_OS_NTP",
	"BILLING_TIMEZONE",
}

// syncBillingEnv copies the billing clock keys of the console's .env
// file into the server service's environment block.
func (s *Service) syncBillingEnv(ctx context.Context) (bool, error) {
	if _, err := s.opts.Stat(s.opts.ConsoleEnvPath); err != nil {
		return false, nil
	}
	data, err := s.opts.ReadFile(s.opts.ConsoleEnvPath)
	if err != nil {
		return false, err
	}
	vars := ParseEnvFileKeys(advancedconfig.DecodeUTF8(data), billingEnvKeys)
	if len(vars) == 0 {
		return false, nil
	}
	server := s.opts.ServerService
	existing, err := s.opts.Shell(ctx, `nssm get "`+server+`" AppEnvironmentExtra 2>nul`)
	if err != nil {
		return false, err
	}
	merged, changed := MergeBillingEnvIntoWindowsServiceExtra(existing, vars)
	if !changed {
		return false, nil
	}
	if err := s.opts.Exec(ctx, "nssm", "set", server, "AppEnvironmentExtra", merged); err != nil {
		return false, err
	}
	return true, nil
}

// ParseEnvFileKeys reads the wanted keys out of the text of a .env file,
// as KEY=value lines (see the console's .env rules); the last line of a
// key wins.
func ParseEnvFileKeys(content string, keys []string) map[string]string {
	out := map[string]string{}
	wanted := map[string]bool{}
	for _, key := range keys {
		wanted[key] = true
	}
	for _, line := range splitCRLF(content) {
		trimmed := jsTrim(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		eq := strings.IndexByte(trimmed, '=')
		if eq <= 0 {
			continue
		}
		if key := jsTrim(trimmed[:eq]); wanted[key] {
			out[key] = jsTrim(trimmed[eq+1:])
		}
	}
	return out
}

// MergeBillingEnvIntoWindowsServiceExtra sets the billing keys in an
// environment block of KEY=value lines. The block is written again, one
// line per key in the order the keys came first, only when a value
// changed.
func MergeBillingEnvIntoWindowsServiceExtra(existing string, vars map[string]string) (string, bool) {
	var order []string
	values := map[string]string{}
	for _, line := range strings.Split(strings.ReplaceAll(existing, "\r\n", "\n"), "\n") {
		line = jsTrim(line)
		if line == "" {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			continue
		}
		key := line[:eq]
		if _, seen := values[key]; !seen {
			order = append(order, key)
		}
		values[key] = line[eq+1:]
	}
	changed := false
	for _, key := range billingEnvKeys {
		next, ok := vars[key]
		if !ok {
			continue
		}
		if current, seen := values[key]; !seen || current != next {
			if !seen {
				order = append(order, key)
			}
			values[key] = next
			changed = true
		}
	}
	if !changed {
		return existing, false
	}
	lines := make([]string, len(order))
	for i, key := range order {
		lines[i] = key + "=" + values[key]
	}
	return strings.Join(lines, "\n"), true
}

// splitCRLF is text.split(/\r?\n/).
func splitCRLF(text string) []string {
	lines := strings.Split(text, "\n")
	for i := range lines[:len(lines)-1] {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}

// shellQuote quotes a value for a POSIX shell.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
