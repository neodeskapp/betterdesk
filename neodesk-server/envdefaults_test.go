package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStartAppliesTheEnvDefaults: a start gives the env file the
// template's keys it lacks and reads them, as after the installers'
// update; a run that prints the version changes nothing (L93).
func TestStartAppliesTheEnvDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("SESSION_SECRET=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NEODESK_ENV_FILE", path)
	t.Setenv("SMTP_PORT", "")
	t.Setenv("SESSION_SECRET", "from the service")
	// The start fills the process environment; the other tests get it back
	// as it was
	before := map[string]bool{}
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		before[key] = true
	}
	t.Cleanup(func() {
		for _, kv := range os.Environ() {
			if key, _, _ := strings.Cut(kv, "="); !before[key] {
				os.Unsetenv(key)
			}
		}
	})
	applyEnvDefaultsFromOS()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "\nSMTP_PORT=587\n") {
		t.Fatalf("the env file after the start:\n%s", raw)
	}
	if got := os.Getenv("SMTP_PORT"); got != "587" {
		t.Errorf("SMTP_PORT in this start: %q", got)
	}
	if got := os.Getenv("SESSION_SECRET"); got != "from the service" {
		t.Errorf("the service's own setting changed: %q", got)
	}

	for _, args := range [][]string{{"-version"}, {"--version"}, {"-h"}} {
		if !printsOnly(args) {
			t.Errorf("%v changes files", args)
		}
	}
	if printsOnly([]string{"-mode", "all"}) {
		t.Error("a server start is taken for a version check")
	}
}
