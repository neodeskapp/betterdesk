package cli

import (
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/managedconfig"
	"github.com/neodeskapp/neodesk/neodesk-server/console/serverinfo"
)

// installerSubstKeys are the placeholders the installers fill, in the
// order write-installer-env-subst.js wrote them.
var installerSubstKeys = []string{
	"RUSTDESK_DIR", "PUB_KEY_PATH", "API_KEY_PATH", "DB_TYPE", "DB_PATH", "DATABASE_URL",
	"DATA_DIR", "GO_API_PORT", "HBBS_API_URL", "NEODESK_API_URL", "API_PORT",
	"DEFAULT_ADMIN_PASSWORD", "SESSION_SECRET", "REMOTE_TARGET_VAULT_KEY",
	"GUACD_ADDRESS", "SSL_CERT_PATH", "SSL_KEY_PATH",
}

const envMergeUsage = "Usage: neodesk-server env merge --target FILE [--fresh] [--subst-file JSON] [--subst-env] [--subst KEY=VALUE ...]"

// templatePath is the name the embedded template goes by.
const templatePath = "(built in)/.env.example"

// EnvMerge is "env merge", what merge-env.js did with the old console's
// .env.example: it writes the template as the env file on a fresh install
// (--fresh, or a target that is missing or blank) and otherwise appends the
// template's keys the target lacks, after filling the template's
// __KEY__ placeholders. The template is the one built into the binary.
//
// Placeholders come from --subst-file (a JSON file, read as JSON.parse and
// Object.assign read it), --subst KEY=VALUE and --subst-env, which takes
// the ND_SUBST_<KEY> variables the installers set, as
// write-installer-env-subst.js wrote them into the file they then passed.
// Later ones win, in the order given.
//
// On success it prints {"success":true,"mode":...,"added":[...]}; a failed
// write prints {"success":false,"error":...} on standard error and exits
// with 1; a usage error exits with 2.
func EnvMerge(args []string, env Env) int {
	return envMerge(args, env, managedconfig.Template(), serverinfo.OSFS{})
}

func envMerge(args []string, env Env, template string, files serverinfo.FS) int {
	usage := func() int {
		fmt.Fprintln(env.Stderr, envMergeUsage)
		return 2
	}
	target := ""
	fresh := false
	subs := newObject[substValue]()
	for i := 0; i < len(args); i++ {
		next := ""
		if i+1 < len(args) {
			next = args[i+1]
		}
		switch a := args[i]; {
		case a == "--target" && next != "":
			target = next
			i++
		case a == "--fresh":
			fresh = true
		case a == "--subst-file" && next != "":
			i++
			raw, err := os.ReadFile(next)
			if err == nil {
				err = readSubstFile(subs, raw)
			}
			if err != nil {
				fmt.Fprintf(env.Stderr, "neodesk-server env merge: %v\n", substFileError(next, err))
				return 1
			}
		case a == "--subst" && next != "":
			i++
			key, value, ok := strings.Cut(next, "=")
			if !ok {
				return usage()
			}
			subs.set(key, substValue{text: value})
		case a == "--subst-env":
			for _, key := range installerSubstKeys {
				subs.set(key, substValue{text: env.Getenv("ND_SUBST_" + key)})
			}
		case a == "--help" || a == "-h":
			return usage()
		}
	}
	if target == "" {
		return usage()
	}

	// MergeEnvFile refuses a value that still holds a placeholder, in this
	// order; a null value (String(null) is "null") never holds one
	ordered := subs.entries()
	list := make([]serverinfo.Substitution, 0, len(ordered))
	for _, key := range ordered {
		list = append(list, serverinfo.Substitution{Key: key, Value: subs.values[key].text})
	}
	result, err := serverinfo.MergeEnvFile(templateFiles{FS: files, text: template}, serverinfo.MergeEnvFileOptions{
		TargetPath:    target,
		TemplatePath:  templatePath,
		FreshInstall:  fresh,
		Substitutions: list,
	})
	if err != nil {
		return mergeFailed(env, err.Error())
	}
	out := orderedJSON{keys: []string{"success", "mode", "added"}, values: map[string]any{"success": true, "mode": result.Mode, "added": result.Added}}
	text, err := jsonText(out, false)
	if err != nil {
		return mergeFailed(env, err.Error())
	}
	fmt.Fprintln(env.Stdout, text)
	return 0
}

func mergeFailed(env Env, message string) int {
	out := orderedJSON{keys: []string{"success", "error"}, values: map[string]any{"success": false, "error": message}}
	text, err := jsonText(out, false)
	if err != nil {
		text = `{"success":false}`
	}
	fmt.Fprintln(env.Stderr, text)
	return 1
}

// templateFiles are the files with the built-in template at templatePath.
type templateFiles struct {
	serverinfo.FS
	text string
}

func (t templateFiles) Exists(path string) bool {
	return path == templatePath || t.FS.Exists(path)
}

func (t templateFiles) ReadFile(path string) ([]byte, error) {
	if path == templatePath {
		return []byte(t.text), nil
	}
	return t.FS.ReadFile(path)
}

func (t templateFiles) WriteFile(path string, data []byte, perm fs.FileMode) error {
	if path == templatePath {
		return &fs.PathError{Op: "open", Path: path, Err: fs.ErrPermission}
	}
	return t.FS.WriteFile(path, data, perm)
}
