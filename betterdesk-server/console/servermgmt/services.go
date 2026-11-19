package servermgmt

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/unitronix/betterdesk-server/console/jsbind"
)

// ServiceNamePattern is what a service name must look like before it is
// looked up in the allow list.
var ServiceNamePattern = regexp.MustCompile(`^[A-Za-z0-9_.@:-]{1,128}$`)

// AllowedServices are the system services the page may list and control:
// BetterDesk's own under every name an install used.
var AllowedServices = map[string]bool{
	"betterdesk-server": true, "betterdesk-server.service": true,
	"betterdesk-console": true, "betterdesk-console.service": true,
	"betterdesk":     true,
	"betterdesk-api": true, "betterdesk-api.service": true,
	"betterdesk-go": true, "betterdesk-go.service": true,
	"rustdesksignal": true, "rustdesksignal.service": true,
	"rustdeskrelay": true, "rustdeskrelay.service": true,
	"hbbs": true, "hbbs.service": true,
	"hbbr": true, "hbbr.service": true,
	"BetterDeskServer": true, "BetterDeskConsole": true, "BetterDeskSignal": true, "BetterDeskRelay": true,
}

// The actions systemctl may run on Linux and PowerShell on Windows.
var (
	linuxActions   = map[string]bool{"start": true, "stop": true, "restart": true, "reload": true, "enable": true, "disable": true, "status": true}
	windowsActions = map[string]bool{"start": true, "stop": true, "restart": true, "status": true}
)

// The refusals of the service control.
var (
	ErrInvalidServiceName = errors.New("Invalid service name")
	ErrServiceNotAllowed  = errors.New("Service is not allowlisted")
	ErrActionNotAllowed   = errors.New("Action not allowed")
	ErrNoServiceControl   = errors.New("Service control not supported on this platform")
)

// StartError is a service command that could not be started at all
// (systemctl or powershell missing). The old console had no handler for
// it and ended; a route drops the request.
type StartError struct{ Err error }

func (e *StartError) Error() string { return "start service command: " + e.Err.Error() }
func (e *StartError) Unwrap() error { return e.Err }

// Unit is one service of the list.
type Unit struct {
	Name string
	// Load is systemd's load state; nil on Windows, which has none.
	Load        *string
	Active      string
	Sub         string
	Description string
}

// ListServices lists the allowed services the service manager knows:
// systemctl list-units on Linux, Get-Service on Windows, none elsewhere.
// A command that fails lists nothing.
func (s *Service) ListServices(ctx context.Context) []Unit {
	var units []Unit
	switch {
	case s.linux():
		units = parseSystemctl(s.output(ctx, 8*time.Second, "systemctl", "list-units", "--type=service", "--all", "--no-legend", "--no-pager", "--plain"))
	case s.windows():
		units = parseGetService(s.output(ctx, 10*time.Second, "powershell", "-NoProfile", "-Command", "Get-Service | Select-Object Name,Status,DisplayName | ConvertTo-Csv -NoTypeInformation"))
	}
	out := []Unit{}
	for _, u := range units {
		if AllowedServices[u.Name] {
			out = append(out, u)
		}
	}
	return out
}

// parseSystemctl reads the lines of systemctl list-units --plain: unit,
// load, active and sub state, then the description. Only units named
// *.service count.
func parseSystemctl(raw string) []Unit {
	var units []Unit
	for _, line := range strings.Split(raw, "\n") {
		parts := splitSpace(jsbind.Trim(line))
		if len(parts) < 4 || !strings.HasSuffix(parts[0], ".service") {
			continue
		}
		load := parts[1]
		units = append(units, Unit{Name: parts[0], Load: &load, Active: parts[2], Sub: parts[3], Description: strings.Join(parts[4:], " ")})
	}
	return units
}

// getServiceLine is one line of Get-Service as CSV: name, status, display
// name.
var getServiceLine = regexp.MustCompile(`^"([^"]*)","([^"]*)","([^"]*)"$`)

// parseGetService reads the CSV of Get-Service after its header line. The
// lines end in CR LF or LF.
func parseGetService(raw string) []Unit {
	var units []Unit
	lines := strings.Split(raw, "\n")
	for _, line := range lines[1:] {
		m := getServiceLine.FindStringSubmatch(strings.TrimSuffix(line, "\r"))
		if m == nil {
			continue
		}
		status := strings.ToLower(m[2])
		active := "inactive"
		if status == "running" {
			active = "active"
		}
		units = append(units, Unit{Name: m[1], Active: active, Sub: status, Description: m[3]})
	}
	return units
}

// ControlResult is what a service action did: its exit code (-1 when a
// signal ended it) and the last 4,096 characters of what it printed.
type ControlResult struct {
	Name     string `json:"name"`
	Action   string `json:"action"`
	ExitCode int    `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

// ControlService runs an action on an allowed service: systemctl <action>
// <name> on Linux (killed after 15 seconds), a PowerShell *-Service
// command on Windows (stopped after 20 seconds). A restart of the
// console's own service ends the process instead when RestartSelf says
// so. The name and the action are checked first; the refusals are the
// Err values of this package. A command that cannot start is a
// *StartError.
func (s *Service) ControlService(ctx context.Context, name, action string) (*ControlResult, error) {
	var actions map[string]bool
	switch {
	case s.linux():
		actions = linuxActions
	case s.windows():
		actions = windowsActions
	default:
		return nil, ErrNoServiceControl
	}
	if !ServiceNamePattern.MatchString(name) {
		return nil, ErrInvalidServiceName
	}
	if !AllowedServices[name] {
		return nil, ErrServiceNotAllowed
	}
	if !actions[action] {
		return nil, ErrActionNotAllowed
	}
	if action == "restart" && s.isOwnService(name) && s.opts.RestartSelf != nil && s.opts.RestartSelf() {
		return &ControlResult{Name: name, Action: action}, nil
	}
	var cmd string
	var args []string
	timeout := 15 * time.Second
	if s.linux() {
		cmd, args = "systemctl", []string{action, name}
	} else {
		verb := map[string]string{"restart": "Restart-Service", "start": "Start-Service", "stop": "Stop-Service"}[action]
		if verb == "" {
			verb = "Get-Service"
		}
		cmd = "powershell"
		args = []string{"-NoProfile", "-Command", verb + " -Name '" + strings.ReplaceAll(name, "'", "''") + "' | Out-String"}
		timeout = 20 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stdout, stderr, code, err := s.opts.Run(runCtx, cmd, args...)
	if err != nil {
		return nil, &StartError{Err: err}
	}
	return &ControlResult{
		Name: name, Action: action, ExitCode: code,
		Stdout: lastUnits(decodeUTF8(stdout), 4096), Stderr: lastUnits(decodeUTF8(stderr), 4096),
	}, nil
}

// isOwnService reports a name of the console's own service: the one
// process that runs the server and the console.
func (s *Service) isOwnService(name string) bool {
	if s.windows() {
		return name == "BetterDeskServer"
	}
	return strings.TrimSuffix(name, ".service") == "betterdesk-server"
}

// output is safeExecSync: what a command printed, trimmed, or "" when it
// fails, exits with another code than 0 or runs longer than timeout.
func (s *Service) output(ctx context.Context, timeout time.Duration, name string, args ...string) string {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stdout, _, code, err := s.opts.Run(ctx, name, args...)
	if err != nil || code != 0 {
		return ""
	}
	return jsbind.Trim(decodeUTF8(stdout))
}

// lastUnits is text.slice(-n): the last n UTF-16 code units. A character
// cut in half at the start becomes U+FFFD.
func lastUnits(text string, n int) string {
	units := utf16.Encode([]rune(text))
	if len(units) <= n {
		return text
	}
	return string(utf16.Decode(units[len(units)-n:]))
}

// runCommand runs a command without a shell, standard input empty, and
// waits at most a second for its output after it ended or was killed.
func runCommand(ctx context.Context, name string, args ...string) (stdout, stderr []byte, exitCode int, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	cmd.WaitDelay = time.Second
	err = cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return out.Bytes(), errOut.Bytes(), 0, nil
	case errors.As(err, &exitErr):
		if ctx.Err() != nil {
			// killed at the time limit, which Node reported as a signal
			return out.Bytes(), errOut.Bytes(), -1, nil
		}
		return out.Bytes(), errOut.Bytes(), exitErr.ExitCode(), nil
	case errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil:
		// it ended, but something it started kept the output open
		return out.Bytes(), errOut.Bytes(), cmd.ProcessState.ExitCode(), nil
	}
	return out.Bytes(), errOut.Bytes(), -1, err
}
