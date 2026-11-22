package managedconfig

import (
	"strings"
	"testing"
)

// The values a fresh install's env file starts with: the installers write
// the template as it is.
func TestTemplateDefaults(t *testing.T) {
	lines := map[string]bool{}
	for _, line := range strings.Split(Template(), "\n") {
		lines[strings.TrimSpace(line)] = true
	}
	for _, want := range []string{
		// L75: mail goes only to a server whose certificate checks out
		"SMTP_TLS_VERIFY=true",
		// L74: the start summary and "[console] listening on" are logged
		"LOG_LEVEL=info",
	} {
		if !lines[want] {
			t.Errorf("the template has no line %q", want)
		}
	}
}

// L70: a new env file names its database once, in DATABASE_URL; the
// server takes the type from the address.
func TestTemplateNamesTheDatabaseOnce(t *testing.T) {
	var found []string
	for _, line := range strings.Split(Template(), "\n") {
		line = strings.TrimSpace(line)
		key, _, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(key, "#") {
			continue
		}
		switch key {
		case "DATABASE_URL", "DB_URL", "DB_TYPE", "DB_PATH":
			found = append(found, line)
		}
	}
	if len(found) != 1 || found[0] != "DATABASE_URL=__DATABASE_URL__" {
		t.Errorf("the template's database lines are %q, want only DATABASE_URL=__DATABASE_URL__", found)
	}
}
