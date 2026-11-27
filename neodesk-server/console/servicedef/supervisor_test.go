package servicedef

import (
	"context"
	"strings"
	"testing"
)

// The native Windows service has no NSSM settings: the check runs no
// nssm command and reports nothing to fix, where it reported nssm's
// failure as the service definition's error. An NSSM install (and one
// that names no supervisor) is still checked.
func TestWindowsCheckLeavesTheNativeServiceAlone(t *testing.T) {
	for _, c := range []struct {
		supervisor string
		wantNSSM   bool
	}{{"windows-service", false}, {"task", false}, {"nssm", true}, {"", true}} {
		var commands []string
		s := New(Options{
			Windows:        true,
			ServerService:  "NeoDeskServer",
			ConsoleService: "NeoDeskConsole",
			ConsoleEnvPath: t.TempDir() + `\.env`,
			Supervisor:     c.supervisor,
			Shell: func(_ context.Context, command string) (string, error) {
				commands = append(commands, command)
				return "", nil
			},
			Exec: func(_ context.Context, name string, args ...string) error {
				commands = append(commands, name+" "+strings.Join(args, " "))
				return nil
			},
		})
		result := s.Check(context.Background())
		ranNSSM := false
		for _, command := range commands {
			if strings.Contains(command, "nssm") {
				ranNSSM = true
			}
		}
		if ranNSSM != c.wantNSSM {
			t.Errorf("supervisor %q: nssm commands %v, want them: %v", c.supervisor, commands, c.wantNSSM)
		}
		if !c.wantNSSM && (result.Error != nil || result.Changed || len(result.Changes) != 0) {
			t.Errorf("supervisor %q: result %+v, want nothing to fix", c.supervisor, result)
		}
	}
}
