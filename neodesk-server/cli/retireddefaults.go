package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/managedconfig"
	"github.com/neodeskapp/neodesk/neodesk-server/console/serverinfo"
)

// retiredDefault is a template entry whose default changed: an env file
// that still holds the old lines exactly as the template wrote them gets
// the template's lines of today. A value or comment someone changed stays.
type retiredDefault struct {
	Key string
	// Old is the comment lines and the KEY=value line as older templates
	// wrote them.
	Old string
	// OldValue is the value of the old line.
	OldValue string
}

// retiredDefaults: until DECISIONS A239 the template set
// SESSION_MAX_PER_USER=1, which every install's env file got. Since A239
// the value is empty and Settings, Sign-in decides; an install that set a
// value of its own keeps it, and it decides over the setting.
var retiredDefaults = []retiredDefault{{
	Key: "SESSION_MAX_PER_USER",
	Old: "# Concurrent panel sessions per account. 1 (default) = a new login ends the\n" +
		"# previous session; N = keep the N most recently active; 0 = unlimited.\n" +
		"SESSION_MAX_PER_USER=1\n",
	OldValue: "1",
}}

// RetireEnvDefaults gives the env file at path the template's lines of
// today in place of the old lines of a default that changed, where the
// file still holds them as the template wrote them. A file with CRLF line
// ends keeps them. It returns the keys it changed; a file that needs no
// change is not written.
func RetireEnvDefaults(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	text := string(raw)
	crlf := strings.Contains(text, "\r\n")
	if crlf {
		text = strings.ReplaceAll(text, "\r\n", "\n")
	}
	var changed []string
	for _, d := range retiredDefaults {
		lines := templateLines(d.Key)
		if lines == "" {
			continue
		}
		at := strings.Index("\n"+text, "\n"+d.Old)
		if at < 0 {
			continue
		}
		text = text[:at] + lines + text[at+len(d.Old):]
		changed = append(changed, d.Key)
	}
	if len(changed) == 0 {
		return nil, nil
	}
	if crlf {
		text = strings.ReplaceAll(text, "\n", "\r\n")
	}
	if err := rewrite(path, []byte(text)); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return changed, nil
}

// RetiredValue is the old default of a key RetireEnvDefaults changed, so
// the start that changed the file can drop that value from its own
// environment.
func RetiredValue(key string) string {
	for _, d := range retiredDefaults {
		if d.Key == key {
			return d.OldValue
		}
	}
	return ""
}

// templateLines is the key's line in the built-in template with the
// comment lines right above it, "" when the template has no such key.
func templateLines(key string) string {
	var pending strings.Builder
	for _, entry := range serverinfo.ParseEnvLines(managedconfig.Template()).Entries {
		if entry.Type != serverinfo.EnvPair {
			if strings.TrimSpace(entry.Raw) == "" {
				pending.Reset()
				continue
			}
			pending.WriteString(entry.Raw + "\n")
			continue
		}
		if entry.Key == key {
			return pending.String() + entry.Key + "=" + entry.RawValue + "\n"
		}
		pending.Reset()
	}
	return ""
}
