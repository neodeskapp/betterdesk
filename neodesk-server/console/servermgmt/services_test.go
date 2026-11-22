//go:build unix

package servermgmt

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A restart of the console's own service (betterdesk-server on Linux,
// BetterDeskServer on Windows) is the process's exit when RestartSelf says
// so; nothing runs then. Any other action, any other service, and a
// console without a supervisor run the command.
func TestRestartSelf(t *testing.T) {
	exits := 0
	f := &fakeRun{}
	linux := New(Options{GOOS: "linux", Run: f.run, RestartSelf: func() bool { exits++; return true }})
	windows := New(Options{GOOS: "windows", Run: f.run, RestartSelf: func() bool { exits++; return true }})
	for _, tc := range []struct {
		s            *Service
		name, action string
		exit         bool
	}{
		{linux, "betterdesk-server", "restart", true},
		{linux, "betterdesk-server.service", "restart", true},
		{windows, "BetterDeskServer", "restart", true},
		{linux, "betterdesk-server", "stop", false},
		{linux, "betterdesk-server", "status", false},
		{linux, "betterdesk-console", "restart", false},
		{linux, "BetterDeskServer", "restart", false},
		{windows, "BetterDeskConsole", "restart", false},
		{windows, "BetterDeskServer", "stop", false},
	} {
		before, ran := exits, len(f.calls)
		res, err := tc.s.ControlService(context.Background(), tc.name, tc.action)
		if err != nil {
			t.Fatalf("%s %s: %v", tc.name, tc.action, err)
		}
		if exited := exits > before; exited != tc.exit || exited == (len(f.calls) > ran) {
			t.Errorf("%s %s: exit %v, command %v", tc.name, tc.action, exited, len(f.calls) > ran)
		}
		if tc.exit && (res.ExitCode != 0 || res.Stdout != "" || res.Name != tc.name || res.Action != tc.action) {
			t.Errorf("%s: %+v", tc.name, res)
		}
	}
	none := New(Options{GOOS: "linux", Run: f.run, RestartSelf: func() bool { return false }})
	if _, err := none.ControlService(context.Background(), "betterdesk-server", "restart"); err != nil || !strings.HasPrefix(f.calls[len(f.calls)-1], "systemctl restart betterdesk-server") {
		t.Errorf("without a supervisor: %v %q", err, f.calls)
	}
}

// The real command runner: the exit code, and -1 for a command the time
// limit killed.
func TestRunCommand(t *testing.T) {
	_, _, code, err := runCommand(context.Background(), "sh", "-c", "echo out; echo err >&2; exit 3")
	if err != nil || code != 3 {
		t.Errorf("exit 3: %d %v", code, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if _, _, code, err := runCommand(ctx, "sleep", "5"); err != nil || code != -1 {
		t.Errorf("killed: %d %v", code, err)
	}
	if _, _, _, err := runCommand(context.Background(), "/nonexistent/systemctl"); err == nil {
		t.Error("a missing command started")
	}
}
