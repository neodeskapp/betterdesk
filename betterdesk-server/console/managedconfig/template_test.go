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
	} {
		if !lines[want] {
			t.Errorf("the template has no line %q", want)
		}
	}
}
