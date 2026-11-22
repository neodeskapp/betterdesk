package cli

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestApplyEnvDefaultsAddsTheTemplatesNewKeys: the start after an update
// from the console gives the env file the template's keys it lacks, as
// the installers' update does, but none the installers fill in or
// upgrade-env decides; the file's own lines stay as they were, and a
// second start writes nothing (L93).
func TestApplyEnvDefaultsAddsTheTemplatesNewKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	before := "# my settings\nSESSION_SECRET=kept-as-it-is\nLOG_LEVEL=info\nCONSOLE_PORT=5000\n"
	if err := os.WriteFile(path, []byte(before), 0o600); err != nil {
		t.Fatal(err)
	}
	added, err := ApplyEnvDefaults(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"SMTP_PORT", "TRUST_PROXY", "SESSION_MAX_PER_USER", "GUACD_ADDRESS"} {
		if !slices.Contains(added, want) {
			t.Errorf("%s not added: %v", want, added)
		}
	}
	for _, never := range []string{"PORT", "HOST", "WEB_UI_DIR", "DEFAULT_ADMIN_PASSWORD", "DATA_DIR", "LOG_LEVEL", "SESSION_SECRET", "CONSOLE_PORT"} {
		if slices.Contains(added, never) {
			t.Errorf("%s added: %v", never, added)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.HasPrefix(text, before) || !strings.Contains(text, "\nSMTP_PORT=587\n") || strings.Contains(text, "__") {
		t.Fatalf("the file after the update:\n%s", text)
	}

	again, err := ApplyEnvDefaults(path)
	if err != nil || again != nil {
		t.Fatalf("a second start added %v, %v", again, err)
	}
	if raw2, _ := os.ReadFile(path); string(raw2) != text {
		t.Error("a second start changed the file")
	}
}
