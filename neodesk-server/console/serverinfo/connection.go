package serverinfo

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
)

// The connection mode says whether clients try a direct connection first
// or always go through the relay, with a few switches around that. It is
// stored where the server reads its environment from: the server's API
// when the console and the server run in two containers, the systemd unit
// on a Linux install, or the compose file.

// ManagedEnvKeys are the server's environment variables the connection
// mode is stored in, in the order they are written.
var ManagedEnvKeys = []string{
	"P2P_FIRST",
	"ALWAYS_USE_RELAY",
	"P2P_FALLBACK_MS",
	"SAME_NAT_RELAY",
	"ALLOW_SHARED_NAT_INITIATOR",
	"LOGGED_IN_ONLY_INITIATOR",
	"OPERATOR_ONLY_OUTBOUND",
}

// The two modes, and the places the settings can come from.
const (
	ModeP2PFirst  = "p2p_first"
	ModeRelayOnly = "relay_only"

	ConnectionSourceGoAPI    = "go-api"
	ConnectionSourceSystemd  = "systemd"
	ConnectionSourceDocker   = "docker"
	ConnectionSourceDefaults = "defaults"

	defaultP2PFallbackMs = 2000
)

// Errors of the connection mode. Their texts are fixed; a route tells the
// errors apart by errors.Is.
var (
	// ErrNotConfigurable: there is neither a systemd unit nor a compose
	// file to store the mode in.
	ErrNotConfigurable = errors.New("not_configurable")
	// ErrComposeServerNotFound: the compose file has no "server" service
	// with an "environment:" list.
	ErrComposeServerNotFound = errors.New("docker_compose_server_not_found")
	// ErrNoRestart: there is no way to restart the server from here.
	ErrNoRestart = errors.New("no_restart")
)

// ConnectionMode holds the connection settings.
type ConnectionMode struct {
	Mode                    string `json:"mode"`
	P2PFallbackMs           Number `json:"p2p_fallback_ms"`
	SameNATRelay            bool   `json:"same_nat_relay"`
	AllowSharedNATInitiator bool   `json:"allow_shared_nat_initiator"`
	LoggedInOnlyInitiator   bool   `json:"logged_in_only_initiator"`
	OperatorOnlyOutbound    bool   `json:"operator_only_outbound"`
}

// ConnectionSettings are the connection settings as read, with where they
// came from and whether they can be changed there.
type ConnectionSettings struct {
	ConnectionMode
	Source   string `json:"source"`
	Writable bool   `json:"writable"`
}

// DefaultConnectionMode is what the server does without any setting.
func DefaultConnectionMode() ConnectionMode {
	return ConnectionMode{Mode: ModeP2PFirst, P2PFallbackMs: defaultP2PFallbackMs, SameNATRelay: true}
}

// ConnectionInput holds the connection settings as a request body gave
// them: any JSON value, or Undefined for a missing field.
type ConnectionInput struct {
	Mode                    any
	P2PFallbackMs           any
	SameNATRelay            any
	AllowSharedNATInitiator any
	LoggedInOnlyInitiator   any
	OperatorOnlyOutbound    any
}

// Input returns the settings as input for SetConnectionMode, to put back
// what GetConnectionMode read earlier.
func (m ConnectionMode) Input() ConnectionInput {
	return ConnectionInput{
		Mode:                    m.Mode,
		P2PFallbackMs:           float64(m.P2PFallbackMs),
		SameNATRelay:            m.SameNATRelay,
		AllowSharedNATInitiator: m.AllowSharedNATInitiator,
		LoggedInOnlyInitiator:   m.LoggedInOnlyInitiator,
		OperatorOnlyOutbound:    m.OperatorOnlyOutbound,
	}
}

// NormalizeConnectionMode reads the settings out of a request body the way
// JavaScript reads them: the mode is relay_only only for exactly that text,
// the fallback time is the value as a number (NaN when it is none), the
// relay for clients behind the same NAT is on unless the value is false,
// and the other switches are on only for true.
func NormalizeConnectionMode(in ConnectionInput) ConnectionMode {
	mode := ModeP2PFirst
	if s, ok := in.Mode.(string); ok && s == ModeRelayOnly {
		mode = ModeRelayOnly
	}
	fallback := jsNumber(in.P2PFallbackMs)
	return ConnectionMode{
		Mode:                    mode,
		P2PFallbackMs:           Number(fallback),
		SameNATRelay:            !isFalse(in.SameNATRelay),
		AllowSharedNATInitiator: isTrue(in.AllowSharedNATInitiator),
		LoggedInOnlyInitiator:   isTrue(in.LoggedInOnlyInitiator),
		OperatorOnlyOutbound:    isTrue(in.OperatorOnlyOutbound),
	}
}

// EnvVars are the server's environment variables for a connection mode.
// An empty value is not written.
type EnvVars struct {
	P2PFirst                string `json:"P2P_FIRST"`
	AlwaysUseRelay          string `json:"ALWAYS_USE_RELAY"`
	P2PFallbackMs           string `json:"P2P_FALLBACK_MS"`
	SameNATRelay            string `json:"SAME_NAT_RELAY"`
	AllowSharedNATInitiator string `json:"ALLOW_SHARED_NAT_INITIATOR"`
	LoggedInOnlyInitiator   string `json:"LOGGED_IN_ONLY_INITIATOR"`
	OperatorOnlyOutbound    string `json:"OPERATOR_ONLY_OUTBOUND"`
}

// Get returns the value of one of the managed variables.
func (v EnvVars) Get(key string) string {
	switch key {
	case "P2P_FIRST":
		return v.P2PFirst
	case "ALWAYS_USE_RELAY":
		return v.AlwaysUseRelay
	case "P2P_FALLBACK_MS":
		return v.P2PFallbackMs
	case "SAME_NAT_RELAY":
		return v.SameNATRelay
	case "ALLOW_SHARED_NAT_INITIATOR":
		return v.AllowSharedNATInitiator
	case "LOGGED_IN_ONLY_INITIATOR":
		return v.LoggedInOnlyInitiator
	case "OPERATOR_ONLY_OUTBOUND":
		return v.OperatorOnlyOutbound
	}
	return ""
}

func isManagedKey(key string) bool {
	for _, k := range ManagedEnvKeys {
		if k == key {
			return true
		}
	}
	return false
}

func yn(value bool) string {
	if value {
		return "Y"
	}
	return "N"
}

// parseYN reads a switch: Y, YES, 1, TRUE and ON are on, N, NO, 0, FALSE
// and OFF are off, in any letter case and with white space around.
// Anything else, also a missing value, is the default.
func parseYN(value string, present, defaultValue bool) bool {
	if !present || value == "" {
		return defaultValue
	}
	switch jsUpper(jsTrim(value)) {
	case "Y", "YES", "1", "TRUE", "ON":
		return true
	case "N", "NO", "0", "FALSE", "OFF":
		return false
	}
	return defaultValue
}

// ModeFromEnvVars returns the mode the server's variables stand for:
// relay_only when ALWAYS_USE_RELAY is on or P2P_FIRST is off.
func ModeFromEnvVars(env map[string]string) string {
	alwaysRelay, hasRelay := env["ALWAYS_USE_RELAY"]
	p2pFirst, hasP2P := env["P2P_FIRST"]
	if parseYN(alwaysRelay, hasRelay, false) || !parseYN(p2pFirst, hasP2P, true) {
		return ModeRelayOnly
	}
	return ModeP2PFirst
}

// EnvVarsFromSettings returns the variables to store for the settings. A
// fallback time that is not a number of zero or more becomes 2000.
func EnvVarsFromSettings(m ConnectionMode) EnvVars {
	fallback := Number(defaultP2PFallbackMs)
	if m.P2PFallbackMs.IsFinite() && m.P2PFallbackMs >= 0 {
		fallback = m.P2PFallbackMs
	}
	vars := EnvVars{
		P2PFirst:                "Y",
		AlwaysUseRelay:          "N",
		P2PFallbackMs:           numberString(float64(fallback)),
		SameNATRelay:            yn(m.SameNATRelay),
		AllowSharedNATInitiator: yn(m.AllowSharedNATInitiator),
		LoggedInOnlyInitiator:   yn(m.LoggedInOnlyInitiator),
		OperatorOnlyOutbound:    yn(m.OperatorOnlyOutbound),
	}
	if m.Mode == ModeRelayOnly {
		vars.P2PFirst, vars.AlwaysUseRelay = "N", "Y"
	}
	return vars
}

// stripQuotes removes one pair of double or single quotes around s.
func stripQuotes(s string) string {
	if (strings.HasPrefix(s, `"`) && strings.HasSuffix(s, `"`)) || (strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'")) {
		if len(s) < 2 {
			return ""
		}
		return s[1 : len(s)-1]
	}
	return s
}

// isJSLineEnd reports whether r ends a line for "^", "$" and "." in a
// JavaScript pattern.
func isJSLineEnd(r rune) bool {
	return r == '\n' || r == '\r' || r == 0x2028 || r == 0x2029
}

// systemdAssignments splits the value of an Environment= line into its
// assignments as systemd does: separated by spaces or tabs, each one
// optionally in double or single quotes, which may hold spaces; a quote
// that is not closed runs to the end of the line. It returns each
// assignment without its quotes, and as it was written.
func systemdAssignments(value string) (assignments, written []string) {
	i := 0
	for i < len(value) {
		if value[i] == ' ' || value[i] == '\t' {
			i++
			continue
		}
		start := i
		if q := value[i]; q == '"' || q == '\'' {
			end := strings.IndexByte(value[i+1:], q)
			if end < 0 {
				return append(assignments, value[i+1:]), append(written, value[i:])
			}
			i += end + 2
			assignments, written = append(assignments, value[start+1:i-1]), append(written, value[start:i])
			continue
		}
		for i < len(value) && value[i] != ' ' && value[i] != '\t' {
			i++
		}
		assignments, written = append(assignments, value[start:i]), append(written, value[start:i])
	}
	return assignments, written
}

// ParseSystemdEnvironment reads the managed variables out of the text of a
// systemd unit: each assignment of its "Environment=" lines, indented or
// not (systemdAssignments).
func ParseSystemdEnvironment(content string) map[string]string {
	env := map[string]string{}
	const prefix = "Environment="
	for _, line := range strings.FieldsFunc(content, isJSLineEnd) {
		trimmed := jsTrim(line)
		if !strings.HasPrefix(trimmed, prefix) {
			continue
		}
		assignments, _ := systemdAssignments(trimmed[len(prefix):])
		for _, pair := range assignments {
			eq := strings.IndexByte(pair, '=')
			if eq <= 0 {
				continue
			}
			if key := jsTrim(pair[:eq]); isManagedKey(key) {
				env[key] = jsTrim(pair[eq+1:])
			}
		}
	}
	return env
}

// PatchSystemdEnvironment returns the text of a systemd unit with the
// managed variables set to vars. Every assignment of a managed variable is
// taken out of its "Environment=" line, which goes when nothing else is
// left on it. The new lines go into the [Service] section, before its
// ExecStart line or else at the end of its first block of lines; a unit
// without that section gets them at the end.
func PatchSystemdEnvironment(content string, vars EnvVars) string {
	var out []string
	for _, line := range strings.Split(content, "\n") {
		trimmed := jsTrim(line)
		if strings.HasPrefix(trimmed, "Environment=") {
			assignments, written := systemdAssignments(trimmed[len("Environment="):])
			var kept []string
			for i, pair := range assignments {
				if key, _, _ := strings.Cut(pair, "="); !isManagedKey(key) {
					kept = append(kept, written[i])
				}
			}
			switch {
			case len(kept) == len(assignments):
			case len(kept) == 0:
				continue
			default:
				indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
				end := ""
				if strings.HasSuffix(line, "\r") {
					end = "\r"
				}
				line = indent + "Environment=" + strings.Join(kept, " ") + end
			}
		}
		out = append(out, line)
	}

	idx := len(out)
	for i, line := range out {
		if strings.HasPrefix(jsTrim(line), "[Service]") {
			idx = i + 1
			break
		}
	}
	for idx < len(out) {
		trimmed := jsTrim(out[idx])
		if trimmed == "" || strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "ExecStart=") {
			break
		}
		idx++
	}

	patched := append([]string{}, out[:idx]...)
	for _, key := range ManagedEnvKeys {
		if value := vars.Get(key); value != "" {
			patched = append(patched, "Environment="+key+"="+value)
		}
	}
	patched = append(patched, out[idx:]...)
	return strings.Join(patched, "\n")
}

var (
	composeServices    = regexp.MustCompile(`^services:` + jsSpaceClass + `*$`)
	composeServer      = regexp.MustCompile(`^(` + jsSpaceClass + `*)server:` + jsSpaceClass + `*$`)
	composeEnvironment = regexp.MustCompile(`^` + jsSpaceClass + `+environment:` + jsSpaceClass + `*$`)
	composeItem        = regexp.MustCompile(`^` + jsSpaceClass + `+-` + jsSpaceClass + `+`)
	composeMapEntry    = regexp.MustCompile(`^` + jsSpaceClass + `+([A-Za-z_][A-Za-z0-9_.-]*)` + jsSpaceClass + `*:(?:` + jsSpaceClass + `+(.*))?$`)
)

// composeBlock is an "environment:" of the service named "server": the
// index of its line, the index after its last entry, whether its entries
// are a mapping ("KEY: value") rather than a list ("- KEY=value"), and
// what the first entry starts with ("" when it has none).
type composeBlock struct {
	start, end int
	mapping    bool
	lead       string
}

// composeScan walks the lines of a compose file through the
// "environment:" of the service named "server" under "services:". It calls
// entry for every entry of it: a "- KEY=value" item, or a "KEY: value"
// line of a mapping, which must be indented deeper than "environment:".
// Blank and comment lines between entries are skipped. With
// stopAfterBlock set it ends at the first other line after the entries,
// or at the end of the service; without it, it reads on, and a later
// "environment:" line in the same service starts a block again. It
// returns the last block seen (start -1 for none).
func composeScan(lines []string, stopAfterBlock bool, entry func(line string, mapping bool)) composeBlock {
	block := composeBlock{start: -1, end: -1}
	inServices, inServer, inEnvironment := false, false, false
	serverIndent, envIndent := 0, 0
	for i, line := range lines {
		if composeServices.MatchString(line) {
			inServices = true
			continue
		}
		if !inServices {
			continue
		}
		if m := composeServer.FindStringSubmatch(line); m != nil {
			inServer, inEnvironment = true, false
			serverIndent = leadingJSSpace(m[1])
			continue
		}
		if !inServer {
			continue
		}

		trimmed := jsTrim(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if leadingJSSpace(line) <= serverIndent {
			if stopAfterBlock {
				break
			}
			inServer, inEnvironment = false, false
			continue
		}
		if composeEnvironment.MatchString(line) {
			inEnvironment = true
			envIndent = leadingJSSpace(line)
			block = composeBlock{start: i, end: i + 1}
			continue
		}
		if !inEnvironment {
			continue
		}
		first := block.end == block.start+1 && block.lead == ""
		switch {
		case !block.mapping && composeItem.MatchString(line):
			if first {
				block.lead = composeItem.FindString(line)
			}
		case (first || block.mapping) && leadingJSSpace(line) > envIndent && composeMapEntry.MatchString(line):
			if first {
				block.mapping = true
				block.lead = line[:len(line)-len(strings.TrimLeftFunc(line, isJSSpace))]
			}
		default:
			if stopAfterBlock {
				return block
			}
			inEnvironment = false
			continue
		}
		block.end = i + 1
		entry(line, block.mapping)
	}
	return block
}

// composeEntry is the key and the value of an entry of an
// "environment:" block, as written; ok is false for a list item without
// "=", whose key is then the whole item.
func composeEntry(line string, mapping bool) (key, value string, ok bool) {
	if mapping {
		m := composeMapEntry.FindStringSubmatch(line)
		return m[1], jsTrim(m[2]), true
	}
	raw := jsTrim(composeItem.ReplaceAllString(line, ""))
	return strings.Cut(raw, "=")
}

// ParseDockerComposeEnvironment reads the managed variables out of the
// text of a compose file: the entries of the "environment:" of the
// service named "server", a list of "- KEY=value" or a mapping of
// "KEY: value". Values that refer to another variable ("${...}") are
// skipped.
func ParseDockerComposeEnvironment(content string) map[string]string {
	env := map[string]string{}
	composeScan(strings.Split(content, "\n"), false, func(line string, mapping bool) {
		key, value, ok := composeEntry(line, mapping)
		key, value = jsTrim(key), stripQuotes(jsTrim(value))
		if !ok || key == "" || strings.Contains(value, "${") {
			return
		}
		if isManagedKey(key) {
			env[key] = value
		}
	})
	return env
}

// PatchDockerComposeEnvironment returns the text of a compose file with
// the managed variables of the "server" service set to vars: their
// entries are taken out of the service's "environment:" and the new ones
// are added after its last entry, written and indented as its first entry
// is ("KEY: "value"" in a mapping), or as "      - KEY=value" when it has
// none. It fails with ErrComposeServerNotFound when there is no such
// block.
func PatchDockerComposeEnvironment(content string, vars EnvVars) (string, error) {
	lines := strings.Split(content, "\n")
	block := composeScan(lines, true, func(string, bool) {})
	if block.start < 0 {
		return "", ErrComposeServerNotFound
	}

	out := append([]string{}, lines[:block.start+1]...)
	for _, line := range lines[block.start+1 : block.end] {
		if trimmed := jsTrim(line); trimmed != "" && !strings.HasPrefix(trimmed, "#") {
			if key, _, _ := composeEntry(line, block.mapping); isManagedKey(key) {
				continue
			}
		}
		out = append(out, line)
	}
	for _, key := range ManagedEnvKeys {
		value := vars.Get(key)
		switch {
		case value == "":
		case block.mapping:
			out = append(out, block.lead+key+`: "`+value+`"`)
		case block.lead != "":
			out = append(out, strings.TrimRightFunc(block.lead, isJSSpace)+" "+key+"="+value)
		default:
			out = append(out, "      - "+key+"="+value)
		}
	}
	out = append(out, lines[block.end:]...)
	return strings.Join(out, "\n"), nil
}

// SettingsFromEnv turns the server's variables into settings. A variable
// that is missing or cannot be read has its default.
func SettingsFromEnv(env map[string]string, source string) ConnectionSettings {
	fallback := Number(defaultP2PFallbackMs)
	if raw := env["P2P_FALLBACK_MS"]; raw != "" {
		if n := Number(jsParseInt(raw)); n.IsFinite() && n >= 0 {
			fallback = n
		}
	}
	flag := func(key string, defaultValue bool) bool {
		value, present := env[key]
		return parseYN(value, present, defaultValue)
	}
	return ConnectionSettings{
		ConnectionMode: ConnectionMode{
			Mode:                    ModeFromEnvVars(env),
			P2PFallbackMs:           fallback,
			SameNATRelay:            flag("SAME_NAT_RELAY", true),
			AllowSharedNATInitiator: flag("ALLOW_SHARED_NAT_INITIATOR", false),
			LoggedInOnlyInitiator:   flag("LOGGED_IN_ONLY_INITIATOR", false),
			OperatorOnlyOutbound:    flag("OPERATOR_ONLY_OUTBOUND", false),
		},
		Source:   source,
		Writable: source != ConnectionSourceDefaults,
	}
}

// SettingsFromAPI turns the server's answer into settings. A fallback time
// outside 0 to 30000 becomes 2000.
func SettingsFromAPI(data any) ConnectionSettings {
	fallback := Number(defaultP2PFallbackMs)
	if n := Number(jsNumber(Field(data, "p2p_fallback_ms"))); n.IsFinite() && n >= 0 && n <= 30000 {
		fallback = n
	}
	mode := ModeP2PFirst
	if s, ok := Field(data, "mode").(string); ok && s == ModeRelayOnly {
		mode = ModeRelayOnly
	}
	return ConnectionSettings{
		ConnectionMode: ConnectionMode{
			Mode:                    mode,
			P2PFallbackMs:           fallback,
			SameNATRelay:            !isFalse(Field(data, "same_nat_relay")),
			AllowSharedNATInitiator: isTrue(Field(data, "allow_shared_nat_initiator")),
			LoggedInOnlyInitiator:   isTrue(Field(data, "logged_in_only_initiator")),
			OperatorOnlyOutbound:    isTrue(Field(data, "operator_only_outbound")),
		},
		Source:   ConnectionSourceGoAPI,
		Writable: true,
	}
}

// isDockerRuntime reports whether the console runs in a container now.
func (s *Service) isDockerRuntime() bool {
	return s.fs.Exists(s.opts.DockerEnvFile) || s.env("DOCKER") == "true"
}

// IsDockerSplitDeployment reports whether the console and the server run
// in two containers: the console is in a container and
// NEODESK_DOCKER_LAYOUT is "split".
func (s *Service) IsDockerSplitDeployment() bool {
	return s.opts.IsDocker && lowerASCII(jsTrim(s.env("NEODESK_DOCKER_LAYOUT"))) == "split"
}

// DetectDeploymentSource says where the connection mode is stored:
// "systemd" for a unit outside a container, "docker" for a compose file
// (in a container, or where there is no unit), else "systemd" when there
// is a unit after all, else "defaults".
func (s *Service) DetectDeploymentSource() string {
	unit := s.fs.Exists(s.opts.SystemdUnit)
	docker := s.isDockerRuntime()
	switch {
	case unit && !docker:
		return ConnectionSourceSystemd
	case s.opts.ComposePath != "" && s.fs.Exists(s.opts.ComposePath) && (docker || !unit):
		return ConnectionSourceDocker
	case unit:
		return ConnectionSourceSystemd
	}
	return ConnectionSourceDefaults
}

func (s *Service) readSystemdSettings() (ConnectionSettings, error) {
	content, err := readText(s.fs, s.opts.SystemdUnit)
	if err != nil {
		return ConnectionSettings{}, nodeReadError(err, s.opts.SystemdUnit)
	}
	env := ParseSystemdEnvironment(content)
	dropIn, err := readText(s.fs, s.opts.SystemdDropIn)
	switch {
	case err == nil:
		for key, value := range ParseSystemdEnvironment(dropIn) {
			env[key] = value
		}
	case !errors.Is(err, fs.ErrNotExist):
		return ConnectionSettings{}, nodeReadError(err, s.opts.SystemdDropIn)
	}
	return SettingsFromEnv(env, ConnectionSourceSystemd), nil
}

func (s *Service) readDockerSettings() (ConnectionSettings, error) {
	content, err := readText(s.fs, s.opts.ComposePath)
	if err != nil {
		return ConnectionSettings{}, nodeReadError(err, s.opts.ComposePath)
	}
	return SettingsFromEnv(ParseDockerComposeEnvironment(content), ConnectionSourceDocker), nil
}

// apiConfig returns the settings object of an answer of the server's
// connection API: its "config" member when it has one, else the answer.
func apiConfig(data any) any {
	if config := Field(data, "config"); truthy(config) {
		return config
	}
	return data
}

// GetConnectionMode reads the connection settings from where they are
// stored. With two containers that is the server's API; when it does not
// answer, the defaults come back marked as not writable. Without a unit
// and a compose file the defaults come back with the source "defaults".
// An error means the unit or the compose file is there and cannot be
// read.
func (s *Service) GetConnectionMode(ctx context.Context) (ConnectionSettings, error) {
	if s.IsDockerSplitDeployment() {
		resp, err := s.goAPIGet(ctx, "/connection/config")
		if err != nil {
			s.log.Error("Failed to read split-Docker connection settings:", err.Error())
			return ConnectionSettings{ConnectionMode: DefaultConnectionMode(), Source: ConnectionSourceGoAPI}, nil
		}
		return SettingsFromAPI(apiConfig(resp.Data())), nil
	}

	defaults := ConnectionSettings{ConnectionMode: DefaultConnectionMode(), Source: ConnectionSourceDefaults}
	var settings ConnectionSettings
	var err error
	s.fileMu.RLock()
	defer s.fileMu.RUnlock()
	switch s.DetectDeploymentSource() {
	case ConnectionSourceSystemd:
		settings, err = s.readSystemdSettings()
	case ConnectionSourceDocker:
		settings, err = s.readDockerSettings()
	default:
		return defaults, nil
	}
	switch {
	case err == nil:
		return settings, nil
	case errors.Is(err, fs.ErrNotExist):
		// The file went away between the look and the read.
		return defaults, nil
	}
	return ConnectionSettings{}, err
}

var errNoGoAPI = errors.New("the server's API is not available")

func (s *Service) goAPIGet(ctx context.Context, path string) (*goapi.Response, error) {
	if s.opts.GoAPI == nil {
		return nil, errNoGoAPI
	}
	return s.opts.GoAPI.Get(ctx, path)
}

// ConnectionWrite is what SetConnectionMode did.
type ConnectionWrite struct {
	// Source is where the settings were stored: "go-api", "systemd" or
	// "docker".
	Source string
	// Settings are the settings as stored. For "go-api" they are what the
	// server reports back.
	Settings ConnectionMode
	// Path is the file written, Vars the variables written to it. Both are
	// empty for "go-api".
	Path string
	Vars EnvVars
	// Method is "privileged_dropin" or "unit_direct" for "systemd".
	Method string
}

// Object returns the result as the settings route answers it, with the
// keys in the order clients have always received. A route adds its own "restartRequired" with
// Set, which keeps the place of a key that is there already.
func (w *ConnectionWrite) Object() *goapi.Object {
	switch w.Source {
	case ConnectionSourceGoAPI:
		return goapi.NewObject(
			"source", w.Source,
			"settings", ConnectionSettings{ConnectionMode: w.Settings, Source: ConnectionSourceGoAPI, Writable: true},
			"restart", nil,
			"restartRequired", nil,
			"dockerMode", true,
		)
	case ConnectionSourceSystemd:
		return goapi.NewObject("path", w.Path, "vars", w.Vars, "method", w.Method, "source", w.Source, "settings", w.Settings)
	}
	return goapi.NewObject("path", w.Path, "vars", w.Vars, "source", w.Source, "settings", w.Settings)
}

// SetConnectionMode stores the connection settings. With two containers it
// sends them to the server's API, which applies them at once. Otherwise it
// writes the server's variables to the systemd drop-in through the
// privileged helper, to the systemd unit itself when there is no helper,
// or to the compose file; the server reads them at its next start.
//
// Errors: ErrNotConfigurable without a unit or compose file,
// ErrComposeServerNotFound for a compose file without the server's
// environment list, the error of the server's API (a *goapi.Error), and
// file errors (a *FileError).
func (s *Service) SetConnectionMode(ctx context.Context, in ConnectionInput) (*ConnectionWrite, error) {
	normalized := NormalizeConnectionMode(in)

	if s.IsDockerSplitDeployment() {
		if s.opts.GoAPI == nil {
			return nil, errNoGoAPI
		}
		resp, err := s.opts.GoAPI.Put(ctx, "/connection/config", goapi.Body(normalized))
		if err != nil {
			return nil, err
		}
		var data any
		if answer := resp.Data(); truthy(answer) {
			data = answer
		}
		persisted := SettingsFromAPI(apiConfig(data))
		return &ConnectionWrite{Source: ConnectionSourceGoAPI, Settings: persisted.ConnectionMode}, nil
	}

	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	vars := EnvVarsFromSettings(normalized)
	switch s.DetectDeploymentSource() {
	case ConnectionSourceSystemd:
		path, method, err := s.writeSystemdSettings(ctx, vars)
		if err != nil {
			return nil, err
		}
		return &ConnectionWrite{Source: ConnectionSourceSystemd, Settings: normalized, Path: path, Vars: vars, Method: method}, nil
	case ConnectionSourceDocker:
		if err := s.writeDockerSettings(vars); err != nil {
			return nil, err
		}
		return &ConnectionWrite{Source: ConnectionSourceDocker, Settings: normalized, Path: s.opts.ComposePath, Vars: vars}, nil
	}
	return nil, ErrNotConfigurable
}

func (s *Service) writeSystemdSettings(ctx context.Context, vars EnvVars) (path, method string, err error) {
	if s.opts.CanUsePrivileged != nil && s.opts.WritePrivilegedEnv != nil && s.opts.CanUsePrivileged() {
		path, err := s.opts.WritePrivilegedEnv(ctx, vars)
		if err != nil {
			return "", "", err
		}
		if path == "" {
			path = s.opts.SystemdDropIn
		}
		return path, "privileged_dropin", nil
	}

	unit := s.opts.SystemdUnit
	content, err := readText(s.fs, unit)
	if err != nil {
		return "", "", s.systemdWriteError(nodeReadError(err, unit))
	}
	if err := s.fs.WriteFile(unit, []byte(PatchSystemdEnvironment(content, vars)), 0o644); err != nil {
		return "", "", s.systemdWriteError(nodeError(err, "open", unit))
	}
	return unit, "unit_direct", nil
}

// systemdWriteError explains a refused write of the unit: the console
// usually runs as a user that may not change it.
func (s *Service) systemdWriteError(err error) error {
	if !errors.Is(err, fs.ErrPermission) {
		return err
	}
	return &FileError{
		Code: "EACCES",
		msg: "Cannot write systemd connection settings (permission denied on " + s.opts.SystemdUnit + "). " +
			"Run once as root: sudo ./neodesk.sh --update " +
			"then retry \u2014 or set ALLOW_SHARED_NAT_INITIATOR via a systemd drop-in.",
		err: err,
	}
}

func (s *Service) writeDockerSettings(vars EnvVars) error {
	path := s.opts.ComposePath
	content, err := readText(s.fs, path)
	if err != nil {
		return nodeReadError(err, path)
	}
	next, err := PatchDockerComposeEnvironment(content, vars)
	if err != nil {
		return err
	}
	return nodeError(s.fs.WriteFile(path, []byte(next), 0o644), "open", path)
}

// RestartResult is what RestartServer did.
type RestartResult struct {
	Source string `json:"source"`
	// DaemonReload is the answer of the systemd reload, null where none
	// was needed.
	DaemonReload *goapi.Object `json:"daemonReload"`
	// Restarts has one entry for the server: component, service, success
	// and, on failure, what the restart reported.
	Restarts []*goapi.Object `json:"restarts"`
}

// RestartServer restarts the server so that it reads a changed connection
// mode: through systemd after a reload of the units, or with "docker
// compose up -d server" in the folder of the compose file. A failed reload
// or restart shows in the result, not as an error. The error is
// ErrNoRestart when there is neither a unit nor a compose file.
func (s *Service) RestartServer(ctx context.Context) (RestartResult, error) {
	result := RestartResult{Source: s.DetectDeploymentSource(), Restarts: []*goapi.Object{}}

	switch result.Source {
	case ConnectionSourceSystemd:
		if s.opts.DaemonReload == nil || s.opts.RestartService == nil {
			return result, errors.New("restart server: no service control configured")
		}
		result.DaemonReload = s.opts.DaemonReload(ctx)
		if !result.DaemonReload.Bool("success") {
			return result, nil
		}
		name := s.opts.ServerServiceName
		entry := goapi.NewObject("component", "server", "service", name)
		restarted := s.opts.RestartService(ctx, name)
		for _, key := range restarted.Keys() {
			entry.Set(key, restarted.Get(key))
		}
		result.Restarts = append(result.Restarts, entry)
		return result, nil

	case ConnectionSourceDocker:
		entry := goapi.NewObject("component", "server", "service", "neodesk-server")
		if err := s.opts.RunShell(ctx, filepath.Dir(s.opts.ComposePath), "docker compose up -d server"); err != nil {
			entry.Set("success", false).Set("error", err.Error())
		} else {
			entry.Set("success", true)
		}
		result.Restarts = append(result.Restarts, entry)
		return result, nil
	}
	return result, ErrNoRestart
}
