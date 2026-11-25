package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// oldSessionLimit is the session limit as every env file made from the
// template until A239 has it.
const oldSessionLimit = "# Concurrent panel sessions per account. 1 (default) = a new login ends the\n" +
	"# previous session; N = keep the N most recently active; 0 = unlimited.\n" +
	"SESSION_MAX_PER_USER=1\n"

// TestRetireEnvDefaultsLetsTheSettingDecideTheSessionLimit: an env file
// that still has the template's old session limit, untouched, gets the
// new lines with an empty value, so Settings, Sign-in decides (several
// sessions unless "One session per account" is on). A value or comment
// someone changed stays, and a second run changes nothing.
func TestRetireEnvDefaultsLetsTheSettingDecideTheSessionLimit(t *testing.T) {
	for _, tc := range []struct {
		name, before string
		retired      bool
	}{
		{"the template's lines", "LOG_LEVEL=info\n" + oldSessionLimit + "SMTP_PORT=587\n", true},
		{"the template's lines with CRLF", strings.ReplaceAll("LOG_LEVEL=info\n"+oldSessionLimit, "\n", "\r\n"), true},
		{"a value of its own", "LOG_LEVEL=info\n" + strings.Replace(oldSessionLimit, "=1\n", "=3\n", 1), false},
		{"one session chosen with a comment of its own", "# keep one session\nSESSION_MAX_PER_USER=1\n", false},
		{"no limit at all", "LOG_LEVEL=info\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".env")
			if err := os.WriteFile(path, []byte(tc.before), 0o600); err != nil {
				t.Fatal(err)
			}
			retired, err := RetireEnvDefaults(path)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := os.ReadFile(path)
			text := string(raw)
			if !tc.retired {
				if len(retired) != 0 || text != tc.before {
					t.Fatalf("changed %v:\n%s", retired, text)
				}
				return
			}
			if !slices.Equal(retired, []string{"SESSION_MAX_PER_USER"}) {
				t.Errorf("retired %v", retired)
			}
			if strings.Contains(text, "SESSION_MAX_PER_USER=1") || !strings.Contains(text, "\nSESSION_MAX_PER_USER=") ||
				!strings.Contains(text, "One session per account") || !strings.HasPrefix(text, "LOG_LEVEL=info") {
				t.Errorf("the file after:\n%s", text)
			}
			if strings.Contains(tc.before, "\r\n") && strings.Count(text, "\r\n") != strings.Count(text, "\n") {
				t.Errorf("mixed line ends:\n%q", text)
			}
			again, err := RetireEnvDefaults(path)
			if err != nil || len(again) != 0 {
				t.Errorf("a second run: %v %v", again, err)
			}
			// The new template line is what ApplyEnvDefaults would add: nothing is added twice
			if added, err := ApplyEnvDefaults(path); err != nil || slices.Contains(added, "SESSION_MAX_PER_USER") {
				t.Errorf("added again: %v %v", added, err)
			}
		})
	}
}
