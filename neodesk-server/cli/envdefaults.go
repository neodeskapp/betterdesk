package cli

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/managedconfig"
	"github.com/neodeskapp/neodesk/neodesk-server/console/serverinfo"
)

// placeholder is a __KEY__ the installers fill when they write the
// template.
var placeholder = regexp.MustCompile(`__[A-Z0-9_]+__`)

// leftToInstallers are the template's keys ApplyEnvDefaults does not add:
// upgrade-env decides them (the console's port, address and folder, the
// server's ports, the env file and the supervisor), and PORT was the old
// console's port.
var leftToInstallers = map[string]bool{
	"PORT": true, "CONSOLE_PORT": true, "HOST": true, "SIGNAL_PORT": true, "RELAY_PORT": true,
	"GO_API_PORT": true, "CONSOLE_DIR": true, "NEODESK_ENV_FILE": true, "NEODESK_SUPERVISOR": true,
}

// ApplyEnvDefaults appends to the env file at path the keys of the
// built-in template it lacks, with the template's values and the comment
// lines above them, as the installers' update does with "env merge". An
// update from the console's Updates page replaces only the binary; the
// start of the new binary then gives the env file the keys a release
// added (DECISIONS A125). Left out are the keys whose value the installers
// fill in (a __KEY__ placeholder), the keys upgrade-env decides, comments
// out or removes, and PORT: their defaults in the code apply. A file that
// lacks none of the keys is not written. It returns the keys it added.
func ApplyEnvDefaults(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	content, added := serverinfo.MergeEnv(string(raw), defaultsTemplate(managedconfig.Template()))
	if len(added) == 0 {
		return nil, nil
	}
	if err := rewrite(path, []byte(content)); err != nil {
		return nil, fmt.Errorf("write %s: %w", path, err)
	}
	return added, nil
}

// defaultsTemplate is the template without the keys ApplyEnvDefaults
// leaves out, each with the comment lines right above it.
func defaultsTemplate(template string) string {
	var out, pending strings.Builder
	for _, entry := range serverinfo.ParseEnvLines(template).Entries {
		if entry.Type != serverinfo.EnvPair {
			pending.WriteString(entry.Raw + "\n")
			continue
		}
		_, commented := commentedKeys[entry.Key]
		_, removed := removedKeys[entry.Key]
		if leftToInstallers[entry.Key] || commented || removed || placeholder.MatchString(entry.Value) {
			pending.Reset()
			continue
		}
		out.WriteString(pending.String())
		pending.Reset()
		out.WriteString(entry.Key + "=" + entry.Value + "\n")
	}
	return out.String()
}
