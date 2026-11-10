package serverinfo

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"regexp"
	"strings"
)

// The functions in this file read and rewrite ".env" files: lines of
// KEY=value, comments that start with "#", and blank lines. A rewrite keeps
// every line it does not change exactly as it was, in the same order.
//
// Lines end at "\n" or "\r\n". UpsertEnvKey writes every line back with
// "\n" alone; MergeEnv keeps the existing text and appends lines that end
// in "\n".

// ProtectedEnvKeys are the keys a merge must never overwrite.
var ProtectedEnvKeys = []string{
	"SESSION_SECRET",
	"DEFAULT_ADMIN_PASSWORD",
	"DEFAULT_ADMIN_USERNAME",
	"DATABASE_URL",
	"DB_TYPE",
	"DB_PATH",
	"ORG_PEER_VAULT_KEY",
	"REMOTE_TARGET_VAULT_KEY",
	"JWT_SECRET",
}

// The kinds of line ParseEnvLines tells apart.
const (
	EnvComment = "comment"
	EnvBlank   = "blank"
	EnvPair    = "pair"
)

// EnvLine is one line of an env file. Key and Value are set for a pair.
// Raw is the line without its line ending.
type EnvLine struct {
	Type  string
	Key   string
	Value string
	Raw   string
}

// EnvFile is a parsed env file: its lines in order, and the value of each
// key. A key that is set twice has the value of its last line.
type EnvFile struct {
	Entries []EnvLine
	keys    []string
	values  map[string]string
}

// Get returns the value of key and whether the file sets it.
func (f *EnvFile) Get(key string) (string, bool) {
	if f == nil {
		return "", false
	}
	v, ok := f.values[key]
	return v, ok
}

// Keys returns the keys in the order they first appear.
func (f *EnvFile) Keys() []string {
	if f == nil {
		return nil
	}
	return append([]string(nil), f.keys...)
}

// splitEnvLines cuts text at "\n" and "\r\n".
func splitEnvLines(content string) []string {
	lines := strings.Split(content, "\n")
	for i := 0; i < len(lines)-1; i++ {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}

// ParseEnvLines reads env file text. A line without "=" or without a key
// before it counts as a comment. The key is trimmed; the value is what
// follows the first "=", untouched.
func ParseEnvLines(content string) *EnvFile {
	f := &EnvFile{values: map[string]string{}}
	for _, raw := range splitEnvLines(content) {
		trimmed := jsTrim(raw)
		if trimmed == "" {
			f.Entries = append(f.Entries, EnvLine{Type: EnvBlank, Raw: raw})
			continue
		}
		eq := strings.IndexByte(raw, '=')
		if strings.HasPrefix(trimmed, "#") || eq == -1 {
			f.Entries = append(f.Entries, EnvLine{Type: EnvComment, Raw: raw})
			continue
		}
		key := jsTrim(raw[:eq])
		if key == "" {
			f.Entries = append(f.Entries, EnvLine{Type: EnvComment, Raw: raw})
			continue
		}
		value := raw[eq+1:]
		f.Entries = append(f.Entries, EnvLine{Type: EnvPair, Key: key, Value: value, Raw: raw})
		if _, seen := f.values[key]; !seen {
			f.keys = append(f.keys, key)
		}
		f.values[key] = value
	}
	return f
}

// Substitution is one placeholder of an env template and its value: the
// text "__KEY__" is replaced by Value.
type Substitution struct {
	Key   string
	Value string
}

// ApplySubstitutions replaces every "__KEY__" in text, one substitution
// after the other in the order given. A value that holds the placeholder
// of a later substitution is therefore replaced too.
func ApplySubstitutions(text string, subs []Substitution) string {
	for _, s := range subs {
		text = strings.ReplaceAll(text, "__"+s.Key+"__", s.Value)
	}
	return text
}

// EnvConfig holds the console settings BuildEnvSubstitutions falls back on
// for keys the existing env file does not set. An empty field counts as
// not set. Numbers are given as text ("21114").
type EnvConfig struct {
	RustdeskDir          string
	KeysPath             string
	GoAPIPort            string
	APIPort              string
	DBType               string
	SSLCertPath          string
	SSLKeyPath           string
	RemoteTargetVaultKey string
	PubKeyPath           string
	APIKeyPath           string
	DBPath               string
	DatabaseURL          string
	DataDir              string
	SessionSecret        string
}

// EnvSubstitutionInput is what BuildEnvSubstitutions works from.
type EnvSubstitutionInput struct {
	// ExistingContent is the text of the env file in use. It is not read
	// when EnvMap is set.
	ExistingContent string
	// EnvMap is the parsed env file in use, in place of ExistingContent.
	EnvMap *EnvFile
	// Config gives the values to use where the env file has none.
	Config EnvConfig
	// Random is the source of the 32 bytes for a new remote target vault
	// key. It is read on every call, also when the key exists. Nil means
	// crypto/rand.
	Random io.Reader
}

// BuildEnvSubstitutions returns the values for the placeholders of the env
// template: for each one the value of the existing env file, else the
// console setting, else a default. The order is fixed, and
// ApplySubstitutions follows it.
func BuildEnvSubstitutions(in EnvSubstitutionInput) ([]Substitution, error) {
	env := in.EnvMap
	if env == nil {
		env = ParseEnvLines(in.ExistingContent)
	}
	cfg := in.Config
	pick := func(key string, fallbacks ...string) string {
		if v, ok := env.Get(key); ok && v != "" {
			return v
		}
		for _, fb := range fallbacks {
			if fb != "" {
				return fb
			}
		}
		return ""
	}
	under := func(dir, name string) string {
		if dir == "" {
			return ""
		}
		return dir + "/" + name
	}

	rustdeskDir := pick("RUSTDESK_DIR", cfg.RustdeskDir, cfg.KeysPath)
	goPort := pick("GO_API_PORT", cfg.GoAPIPort, "21114")
	apiPort := pick("API_PORT", cfg.APIPort, "21121")
	dbType := pick("DB_TYPE", cfg.DBType, "sqlite")
	sslCert := pick("SSL_CERT_PATH", cfg.SSLCertPath, under(rustdeskDir, "ssl/betterdesk.crt"))
	sslKey := pick("SSL_KEY_PATH", cfg.SSLKeyPath, under(rustdeskDir, "ssl/betterdesk.key"))

	random := in.Random
	if random == nil {
		random = rand.Reader
	}
	fresh := make([]byte, 32)
	if _, err := io.ReadFull(random, fresh); err != nil {
		return nil, fmt.Errorf("make remote target vault key: %w", err)
	}
	vaultKey := pick("REMOTE_TARGET_VAULT_KEY", cfg.RemoteTargetVaultKey, base64.RawURLEncoding.EncodeToString(fresh))

	goAPIBase := "http://localhost:" + goPort + "/api"

	return []Substitution{
		{"RUSTDESK_DIR", rustdeskDir},
		{"PUB_KEY_PATH", pick("PUB_KEY_PATH", cfg.PubKeyPath, under(rustdeskDir, "id_ed25519.pub"))},
		{"API_KEY_PATH", pick("API_KEY_PATH", cfg.APIKeyPath, under(rustdeskDir, ".api_key"))},
		{"DB_TYPE", dbType},
		{"DB_PATH", pick("DB_PATH", cfg.DBPath, under(rustdeskDir, "db_v2.sqlite3"))},
		{"DATABASE_URL", pick("DATABASE_URL", cfg.DatabaseURL)},
		{"DATA_DIR", pick("DATA_DIR", cfg.DataDir)},
		{"GO_API_PORT", goPort},
		{"HBBS_API_URL", pick("HBBS_API_URL", goAPIBase)},
		{"BETTERDESK_API_URL", pick("BETTERDESK_API_URL", goAPIBase)},
		{"API_PORT", apiPort},
		{"DEFAULT_ADMIN_PASSWORD", pick("DEFAULT_ADMIN_PASSWORD")},
		{"SESSION_SECRET", pick("SESSION_SECRET", cfg.SessionSecret)},
		{"ORG_PEER_VAULT_KEY", pick("ORG_PEER_VAULT_KEY")},
		{"REMOTE_TARGET_VAULT_KEY", vaultKey},
		{"GUACD_ADDRESS", pick("GUACD_ADDRESS", "127.0.0.1:4822")},
		{"SSL_CERT_PATH", sslCert},
		{"SSL_KEY_PATH", sslKey},
	}, nil
}

var unresolvedPlaceholder = regexp.MustCompile(`__[^_]+__`)

// AssertResolvedSubstitutions returns an error for the first value that
// still holds a "__PLACEHOLDER__".
func AssertResolvedSubstitutions(subs []Substitution) error {
	for _, s := range subs {
		if unresolvedPlaceholder.MatchString(s.Value) {
			//lint:ignore ST1005 the installer prints this text as its error
			return fmt.Errorf("Unresolved .env substitution for %s", s.Key)
		}
	}
	return nil
}

// MergeEnv adds to an env file the keys of a template it does not have.
// The existing text stays as it is, with a line break added when it lacks
// the last one. Each missing key is appended as KEY=value, after the
// comment and blank lines that stand before it in the template since the
// last key that was appended. Comments above template keys the file
// already has are carried along to the next missing key.
func MergeEnv(existingContent, templateContent string) (content string, added []string) {
	existing := ParseEnvLines(existingContent)
	template := ParseEnvLines(templateContent)

	var b strings.Builder
	b.WriteString(existingContent)
	if existingContent != "" && !strings.HasSuffix(existingContent, "\n") {
		b.WriteByte('\n')
	}

	added = []string{}
	var pending strings.Builder
	for _, entry := range template.Entries {
		if entry.Type != EnvPair {
			pending.WriteString(entry.Raw)
			pending.WriteByte('\n')
			continue
		}
		if _, ok := existing.Get(entry.Key); ok {
			continue
		}
		b.WriteString(pending.String())
		pending.Reset()
		b.WriteString(entry.Key)
		b.WriteByte('=')
		b.WriteString(entry.Value)
		b.WriteByte('\n')
		added = append(added, entry.Key)
	}
	return b.String(), added
}

// BuildFreshEnv returns the template as a new env file: with a line break
// at the end.
func BuildFreshEnv(templateContent string) string {
	if strings.HasSuffix(templateContent, "\n") {
		return templateContent
	}
	return templateContent + "\n"
}

// UpsertEnvKey sets one key in env file text. Every line that sets the key
// becomes "KEY=value", without the indent it may have had; when no line
// sets it, "KEY=value" is appended. All other lines stay as they are and
// in place. The result ends with a line break, and "\r\n" line endings
// come out as "\n".
//
// A line sets the key when the text before its first "=" is the key once
// trimmed. Comment lines and "export KEY=value" lines do not.
func UpsertEnvKey(existingContent, key, value string) string {
	lines := splitEnvLines(existingContent)
	replaced := false
	for i, line := range lines {
		trimmed := jsTrim(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 || jsTrim(line[:eq]) != key {
			continue
		}
		replaced = true
		lines[i] = key + "=" + value
	}
	content := strings.Join(lines, "\n")
	if !replaced {
		if content != "" && !strings.HasSuffix(content, "\n") {
			content += "\n"
		}
		return content + key + "=" + value + "\n"
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content
}

// MergeEnvFileOptions says which files MergeEnvFile works on.
type MergeEnvFileOptions struct {
	// TargetPath is the env file to write.
	TargetPath string
	// TemplatePath is the template (.env.example).
	TemplatePath string
	// FreshInstall writes the whole template even when the target has
	// content.
	FreshInstall bool
	// Substitutions fill the template's placeholders.
	Substitutions []Substitution
}

// MergeEnvResult is what MergeEnvFile did: Mode is "fresh" when the target
// was written from the template alone and "merge" when keys were added to
// it. Added lists the keys written or added.
type MergeEnvResult struct {
	Mode  string   `json:"mode"`
	Added []string `json:"added"`
}

// MergeEnvFile fills the template and writes the target: the whole
// template when the target is missing or blank or on a fresh install, and
// otherwise the target with the template's missing keys appended. A new
// target is created with mode 0600.
func MergeEnvFile(fsys FS, opts MergeEnvFileOptions) (MergeEnvResult, error) {
	if !fsys.Exists(opts.TemplatePath) {
		//lint:ignore ST1005 the installer prints this text as its error
		return MergeEnvResult{}, fmt.Errorf("Template not found: %s", opts.TemplatePath)
	}
	if err := AssertResolvedSubstitutions(opts.Substitutions); err != nil {
		return MergeEnvResult{}, err
	}
	rawTemplate, err := readText(fsys, opts.TemplatePath)
	if err != nil {
		return MergeEnvResult{}, nodeReadError(err, opts.TemplatePath)
	}
	template := ApplySubstitutions(rawTemplate, opts.Substitutions)
	existing := ""
	if fsys.Exists(opts.TargetPath) {
		if existing, err = readText(fsys, opts.TargetPath); err != nil {
			return MergeEnvResult{}, nodeReadError(err, opts.TargetPath)
		}
	}

	if opts.FreshInstall || jsTrim(existing) == "" {
		if err := fsys.WriteFile(opts.TargetPath, []byte(BuildFreshEnv(template)), 0o600); err != nil {
			return MergeEnvResult{}, nodeError(err, "open", opts.TargetPath)
		}
		keys := ParseEnvLines(template).Keys()
		if keys == nil {
			keys = []string{}
		}
		return MergeEnvResult{Mode: "fresh", Added: keys}, nil
	}

	content, added := MergeEnv(existing, template)
	if err := fsys.WriteFile(opts.TargetPath, []byte(content), 0o600); err != nil {
		return MergeEnvResult{}, nodeError(err, "open", opts.TargetPath)
	}
	return MergeEnvResult{Mode: "merge", Added: added}, nil
}
