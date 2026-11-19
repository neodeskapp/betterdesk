package serverinfo

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
)

// oracleVars are the variables a script passed to a patch function: any of
// the managed ones, some missing.
type oracleVars map[string]string

func (v oracleVars) vars() EnvVars {
	return EnvVars{
		P2PFirst:                v["P2P_FIRST"],
		AlwaysUseRelay:          v["ALWAYS_USE_RELAY"],
		P2PFallbackMs:           v["P2P_FALLBACK_MS"],
		SameNATRelay:            v["SAME_NAT_RELAY"],
		AllowSharedNATInitiator: v["ALLOW_SHARED_NAT_INITIATOR"],
		LoggedInOnlyInitiator:   v["LOGGED_IN_ONLY_INITIATOR"],
		OperatorOnlyOutbound:    v["OPERATOR_ONLY_OUTBOUND"],
	}
}

type serviceState struct {
	Unit             textOrDir         `json:"unit"`
	DropIn           textOrDir         `json:"dropin"`
	Compose          textOrDir         `json:"compose"`
	DockerEnv        bool              `json:"dockerenv"`
	IsDocker         bool              `json:"isDocker"`
	Env              map[string]string `json:"env"`
	Privileged       bool              `json:"privileged"`
	PrivilegedResult *struct {
		Path string `json:"path"`
	} `json:"privilegedResult"`
	PrivilegedFail string          `json:"privilegedFail"`
	APIGet         *apiAnswer      `json:"apiGet"`
	APIPut         *apiAnswer      `json:"apiPut"`
	Reload         json.RawMessage `json:"reload"`
	Restart        json.RawMessage `json:"restart"`
	ShellError     *string         `json:"shellError"`
}

type outcome struct {
	Out json.RawMessage `json:"out"`
	Err *string         `json:"err"`
}

type connectionVectors struct {
	ParseSystemd []struct {
		Text string            `json:"text"`
		Out  map[string]string `json:"out"`
	} `json:"parse_systemd"`
	PatchSystemd []struct {
		Text string     `json:"text"`
		Vars oracleVars `json:"vars"`
		Out  string     `json:"out"`
	} `json:"patch_systemd"`
	ParseCompose []struct {
		Text string            `json:"text"`
		Out  map[string]string `json:"out"`
	} `json:"parse_compose"`
	PatchCompose []struct {
		Text string     `json:"text"`
		Vars oracleVars `json:"vars"`
		Out  *string    `json:"out"`
		Err  *string    `json:"err"`
	} `json:"patch_compose"`
	SettingsFromEnv []struct {
		Env    map[string]string `json:"env"`
		Source string            `json:"source"`
		Out    json.RawMessage   `json:"out"`
		Mode   string            `json:"mode"`
	} `json:"settings_from_env"`
	EnvVarsFromSettings []struct {
		Settings map[string]any  `json:"settings"`
		Out      json.RawMessage `json:"out"`
	} `json:"env_vars_from_settings"`
	SettingsFromAPI []struct {
		Data    any             `json:"data"`
		HasData bool            `json:"hasData"`
		Out     json.RawMessage `json:"out"`
	} `json:"settings_from_api"`
	Service []struct {
		State           serviceState   `json:"state"`
		Settings        map[string]any `json:"settings"`
		SplitDeployment bool           `json:"splitDeployment"`
		Source          string         `json:"source"`
		Get             outcome        `json:"get"`
		GetCalls        []struct {
			Method string `json:"method"`
			URL    string `json:"url"`
		} `json:"getCalls"`
		Set struct {
			JSON *string `json:"json"`
			Err  *string `json:"err"`
		} `json:"set"`
		SetCalls []struct {
			Method string `json:"method"`
			URL    string `json:"url"`
			Body   string `json:"body"`
		} `json:"setCalls"`
		PrivilegedCalls []string `json:"privilegedCalls"`
		After           struct {
			Unit    *fileState `json:"unit"`
			DropIn  *fileState `json:"dropin"`
			Compose *fileState `json:"compose"`
		} `json:"after"`
		GetAfter outcome `json:"getAfter"`
		Restart  struct {
			Out *string `json:"out"`
			Err *string `json:"err"`
		} `json:"restart"`
		ShellCalls []struct {
			Command string `json:"command"`
			Cwd     string `json:"cwd"`
			Timeout int    `json:"timeout"`
		} `json:"shellCalls"`
		ControlCalls []string `json:"controlCalls"`
	} `json:"service"`
	Shell []struct {
		Command   string  `json:"command"`
		TimeoutMs int     `json:"timeoutMs"`
		Cwd       string  `json:"cwd"`
		Err       *string `json:"err"`
	} `json:"shell"`
}

func connectionInput(settings map[string]any) ConnectionInput {
	return ConnectionInput{
		Mode:                    Field(settings, "mode"),
		P2PFallbackMs:           Field(settings, "p2p_fallback_ms"),
		SameNATRelay:            Field(settings, "same_nat_relay"),
		AllowSharedNATInitiator: Field(settings, "allow_shared_nat_initiator"),
		LoggedInOnlyInitiator:   Field(settings, "logged_in_only_initiator"),
		OperatorOnlyOutbound:    Field(settings, "operator_only_outbound"),
	}
}

func TestSystemdEnvironmentAgainstNode(t *testing.T) {
	var v connectionVectors
	loadVectors(t, "connection", &v)
	atLeast(t, "parse_systemd", len(v.ParseSystemd), 700)
	atLeast(t, "patch_systemd", len(v.PatchSystemd), 1400)
	m := &mismatches{t: t}
	found := 0
	all := m
	for _, c := range v.ParseSystemd {
		m := unitCases(all, c.Text)
		got := ParseSystemdEnvironment(c.Text)
		found += len(got)
		if !reflect.DeepEqual(got, c.Out) {
			m.errorf("ParseSystemdEnvironment(%q) = %q, Node answers %q", c.Text, got, c.Out)
		}
	}
	if found < 800 {
		t.Fatalf("only %d variables were read over all units", found)
	}
	changed := 0
	for _, c := range v.PatchSystemd {
		m := unitCases(all, c.Text)
		got := PatchSystemdEnvironment(c.Text, c.Vars.vars())
		if got != c.Out {
			m.errorf("PatchSystemdEnvironment(%q, %v) =\n%q\nNode answers\n%q", c.Text, c.Vars, got, c.Out)
		}
		if got != c.Text {
			changed++
		}
	}
	if changed < 1000 {
		t.Fatalf("only %d units changed", changed)
	}
}

func TestComposeEnvironmentAgainstNode(t *testing.T) {
	var v connectionVectors
	loadVectors(t, "connection", &v)
	atLeast(t, "parse_compose", len(v.ParseCompose), 700)
	atLeast(t, "patch_compose", len(v.PatchCompose), 1400)
	m := &mismatches{t: t}
	found := 0
	for _, c := range v.ParseCompose {
		got := ParseDockerComposeEnvironment(c.Text)
		found += len(got)
		if !reflect.DeepEqual(got, c.Out) {
			m.errorf("ParseDockerComposeEnvironment(%q) = %q, Node answers %q", c.Text, got, c.Out)
		}
	}
	if found < 80 {
		t.Fatalf("only %d variables were read over all compose files", found)
	}
	patched, refused := 0, 0
	for _, c := range v.PatchCompose {
		got, err := PatchDockerComposeEnvironment(c.Text, c.Vars.vars())
		switch {
		case c.Err != nil:
			refused++
			if err == nil || err.Error() != *c.Err || !errors.Is(err, ErrComposeServerNotFound) {
				m.errorf("PatchDockerComposeEnvironment(%q) = %q, %v; Node throws %q", c.Text, got, err, *c.Err)
			}
		case err != nil:
			m.errorf("PatchDockerComposeEnvironment(%q) fails with %v; Node answers %q", c.Text, err, *c.Out)
		case got != *c.Out:
			m.errorf("PatchDockerComposeEnvironment(%q, %v) =\n%q\nNode answers\n%q", c.Text, c.Vars, got, *c.Out)
		default:
			patched++
		}
	}
	if patched < 300 || refused < 200 {
		t.Fatalf("%d patched and %d refused: too few", patched, refused)
	}
}

func TestSettingsFromEnvAgainstNode(t *testing.T) {
	var v connectionVectors
	loadVectors(t, "connection", &v)
	atLeast(t, "settings_from_env", len(v.SettingsFromEnv), 2000)
	m := &mismatches{t: t}
	relay := 0
	for _, c := range v.SettingsFromEnv {
		if got := jsonText(t, SettingsFromEnv(c.Env, c.Source)); got != string(c.Out) {
			m.errorf("SettingsFromEnv(%q, %q) =\n%s\nNode answers\n%s", c.Env, c.Source, got, c.Out)
		}
		if got := ModeFromEnvVars(c.Env); got != c.Mode {
			m.errorf("ModeFromEnvVars(%q) = %q, Node answers %q", c.Env, got, c.Mode)
		}
		if c.Mode == ModeRelayOnly {
			relay++
		}
	}
	if relay < 300 || relay > len(v.SettingsFromEnv)-300 {
		t.Fatalf("%d of %d cases are relay_only: the vectors do not test both modes", relay, len(v.SettingsFromEnv))
	}
}

func TestEnvVarsFromSettingsAgainstNode(t *testing.T) {
	var v connectionVectors
	loadVectors(t, "connection", &v)
	atLeast(t, "env_vars_from_settings", len(v.EnvVarsFromSettings), 1600)
	m := &mismatches{t: t}
	for _, c := range v.EnvVarsFromSettings {
		got := jsonText(t, EnvVarsFromSettings(NormalizeConnectionMode(connectionInput(c.Settings))))
		if got != string(c.Out) {
			m.errorf("EnvVarsFromSettings(%v) =\n%s\nNode answers\n%s", c.Settings, got, c.Out)
		}
	}
}

func TestSettingsFromAPIAgainstNode(t *testing.T) {
	var v connectionVectors
	loadVectors(t, "connection", &v)
	atLeast(t, "settings_from_api", len(v.SettingsFromAPI), 1200)
	m := &mismatches{t: t}
	for _, c := range v.SettingsFromAPI {
		data := c.Data
		if !c.HasData {
			data = Undefined
		}
		if got := jsonText(t, SettingsFromAPI(data)); got != string(c.Out) {
			m.errorf("SettingsFromAPI(%v) =\n%s\nNode answers\n%s", c.Data, got, c.Out)
		}
	}
}

// connectionStage is one recorded case of the service laid out on disk.
type connectionStage struct {
	root    string
	api     *stubAPI
	service *Service

	privilegedCalls []string
	controlCalls    []string
	shellCalls      []string
}

func (st *connectionStage) path(name string) string { return filepath.Join(st.root, name) }

// scrub names the files of the stage the way the script named them.
func (st *connectionStage) scrub(text string) string {
	return strings.ReplaceAll(text, st.root+string(filepath.Separator), "{ROOT}/")
}

func jsObject(t testing.TB, raw json.RawMessage, extra ...any) *goapi.Object {
	t.Helper()
	parsed, err := goapi.Parse(raw)
	if err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	object, ok := parsed.(*goapi.Object)
	if !ok {
		t.Fatalf("%s is not an object", raw)
	}
	for i := 0; i+1 < len(extra); i += 2 {
		object.Set(extra[i].(string), extra[i+1])
	}
	return object
}

func stageConnection(t testing.TB, root string, state serviceState) *connectionStage {
	t.Helper()
	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	st := &connectionStage{root: root, api: &stubAPI{}}
	state.Unit.put(t, st.path("unit"), 0o640)
	state.DropIn.put(t, st.path("dropin"), 0o640)
	state.Compose.put(t, st.path("compose"), 0o640)
	if state.DockerEnv {
		if err := os.WriteFile(st.path("dockerenv"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	st.api.set("GET", state.APIGet)
	st.api.set("PUT", state.APIPut)
	st.api.set("GET", state.APIGet)

	st.service = New(Options{
		SystemdUnit:   st.path("unit"),
		SystemdDropIn: st.path("dropin"),
		ComposePath:   st.path("compose"),
		DockerEnvFile: st.path("dockerenv"),
		IsDocker:      state.IsDocker,
		Getenv:        envMap(state.Env),
		GoAPI:         st.api.client(),
		Logger:        &recordingLogger{},
		CanUsePrivileged: func() bool {
			return state.Privileged
		},
		WritePrivilegedEnv: func(_ context.Context, vars EnvVars) (string, error) {
			st.privilegedCalls = append(st.privilegedCalls, `{"action":"write_connection_env","vars":`+jsonText(t, vars)+`}`)
			if state.PrivilegedFail != "" {
				return "", errors.New(state.PrivilegedFail)
			}
			if state.PrivilegedResult != nil {
				return state.PrivilegedResult.Path, nil
			}
			return "", nil
		},
		DaemonReload: func(context.Context) *goapi.Object {
			st.controlCalls = append(st.controlCalls, "daemonReload")
			return jsObject(t, state.Reload)
		},
		RestartService: func(_ context.Context, name string) *goapi.Object {
			st.controlCalls = append(st.controlCalls, "restartService "+name)
			return jsObject(t, state.Restart, "service", name)
		},
		RunShell: func(_ context.Context, dir, command string) error {
			st.shellCalls = append(st.shellCalls, command+" in "+dir)
			if state.ShellError != nil {
				return errors.New(*state.ShellError)
			}
			return nil
		},
	})
	return st
}

func (st *connectionStage) checkOutcome(t testing.TB, m *mismatches, label string, got any, err error, want outcome) {
	t.Helper()
	switch {
	case want.Err != nil:
		if err == nil || st.scrub(err.Error()) != *want.Err {
			m.errorf("%s: error %v, Node throws %q", label, err, *want.Err)
		}
	case err != nil:
		m.errorf("%s: error %v, Node answers %s", label, err, want.Out)
	default:
		if text := jsonText(t, got); text != string(want.Out) {
			m.errorf("%s: answer\n%s\nNode answers\n%s", label, text, want.Out)
		}
	}
}

func TestConnectionServiceAgainstNode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the recorded cases use directories in place of files, which read differently on Windows")
	}
	var v connectionVectors
	loadVectors(t, "connection", &v)
	atLeast(t, "service", len(v.Service), 300)
	root := filepath.Join(t.TempDir(), "conn")
	ctx := context.Background()
	m := &mismatches{t: t}
	sources, setKinds, restartKinds := map[string]int{}, map[string]int{}, map[string]int{}
	all := m
	for i, c := range v.Service {
		label := "case " + strconv.Itoa(i)
		m := unitCases(all, c.State.Unit.Text, c.State.DropIn.Text)

		st := stageConnection(t, root, c.State)
		if got := st.service.IsDockerSplitDeployment(); got != c.SplitDeployment {
			m.errorf("%s: IsDockerSplitDeployment = %v, Node answers %v", label, got, c.SplitDeployment)
		}
		if got := st.service.DetectDeploymentSource(); got != c.Source {
			m.errorf("%s: DetectDeploymentSource = %q, Node answers %q", label, got, c.Source)
		}
		sources[c.Source]++
		got, err := st.service.GetConnectionMode(ctx)
		st.checkOutcome(t, m, label+" get", got, err, c.Get)
		if calls := st.api.seen(); len(calls) != len(c.GetCalls) {
			m.errorf("%s get: %d calls to the server, Node made %d", label, len(calls), len(c.GetCalls))
		} else {
			for j, call := range calls {
				if call.Method != c.GetCalls[j].Method || call.Path != "/api"+c.GetCalls[j].URL {
					m.errorf("%s get: called %s %s, Node called %s %s", label, call.Method, call.Path, c.GetCalls[j].Method, c.GetCalls[j].URL)
				}
			}
		}

		st = stageConnection(t, root, c.State)
		write, err := st.service.SetConnectionMode(ctx, connectionInput(c.Settings))
		switch {
		case c.Set.Err != nil:
			setKinds["error"]++
			if err == nil || st.scrub(err.Error()) != *c.Set.Err {
				m.errorf("%s set: error %v, Node throws %q", label, err, *c.Set.Err)
			}
		case err != nil:
			m.errorf("%s set: error %v, Node answers %s", label, err, *c.Set.JSON)
		default:
			setKinds[write.Source+" "+write.Method]++
			// A route adds its restart note the way the Node route did.
			answer := write.Object().Set("restartRequired", "PENDING")
			if text := st.scrub(jsonText(t, answer)); text != *c.Set.JSON {
				m.errorf("%s set: answer\n%s\nNode answers\n%s", label, text, *c.Set.JSON)
			}
		}
		if !reflect.DeepEqual(st.privilegedCalls, c.PrivilegedCalls) && (len(st.privilegedCalls) != 0 || len(c.PrivilegedCalls) != 0) {
			m.errorf("%s set: privileged calls %q, Node made %q", label, st.privilegedCalls, c.PrivilegedCalls)
		}
		checkFile(t, m, label+" set", st.path("unit"), c.After.Unit)
		checkFile(t, m, label+" set", st.path("dropin"), c.After.DropIn)
		checkFile(t, m, label+" set", st.path("compose"), c.After.Compose)
		got, err = st.service.GetConnectionMode(ctx)
		st.checkOutcome(t, m, label+" get after set", got, err, c.GetAfter)
		// The recorded calls are those of the write and of the read after it.
		calls := st.api.seen()
		if len(calls) != len(c.SetCalls) {
			m.errorf("%s set: %d calls to the server, Node made %d", label, len(calls), len(c.SetCalls))
		} else {
			for j, call := range calls {
				want := c.SetCalls[j]
				if call.Method != want.Method || call.Path != "/api"+want.URL || call.Body != want.Body {
					m.errorf("%s set: sent %s %s %s, Node sent %s %s %s", label, call.Method, call.Path, call.Body, want.Method, want.URL, want.Body)
				}
			}
		}

		st = stageConnection(t, root, c.State)
		restart, err := st.service.RestartServer(ctx)
		switch {
		case c.Restart.Err != nil:
			restartKinds["error"]++
			if err == nil || err.Error() != *c.Restart.Err || !errors.Is(err, ErrNoRestart) {
				m.errorf("%s restart: error %v, Node throws %q", label, err, *c.Restart.Err)
			}
		case err != nil:
			m.errorf("%s restart: error %v, Node answers %s", label, err, *c.Restart.Out)
		default:
			restartKinds[restart.Source]++
			if text := jsonText(t, restart); text != *c.Restart.Out {
				m.errorf("%s restart: answer\n%s\nNode answers\n%s", label, text, *c.Restart.Out)
			}
		}
		if !reflect.DeepEqual(st.controlCalls, c.ControlCalls) && (len(st.controlCalls) != 0 || len(c.ControlCalls) != 0) {
			m.errorf("%s restart: service calls %q, Node made %q", label, st.controlCalls, c.ControlCalls)
		}
		var wantShell []string
		for _, call := range c.ShellCalls {
			// Node ran the command in the folder of the compose file, with
			// two minutes for it.
			if call.Cwd != "." || call.Timeout != int(shellTimeout/time.Millisecond) {
				t.Fatalf("%s: Node ran the command in %q with %d ms", label, call.Cwd, call.Timeout)
			}
			wantShell = append(wantShell, call.Command+" in "+root)
		}
		if !reflect.DeepEqual(st.shellCalls, wantShell) {
			m.errorf("%s restart: shell calls %q, Node made %q", label, st.shellCalls, wantShell)
		}
	}
	for _, source := range []string{ConnectionSourceSystemd, ConnectionSourceDocker, ConnectionSourceDefaults} {
		if sources[source] < 20 {
			t.Errorf("only %d cases with source %q", sources[source], source)
		}
	}
	for _, kind := range []string{"error", "go-api ", "systemd unit_direct", "systemd privileged_dropin", "docker "} {
		if setKinds[kind] < 10 {
			t.Errorf("only %d writes of kind %q (%v)", setKinds[kind], kind, setKinds)
		}
	}
	for _, kind := range []string{"error", ConnectionSourceSystemd, ConnectionSourceDocker} {
		if restartKinds[kind] < 20 {
			t.Errorf("only %d restarts of kind %q", restartKinds[kind], kind)
		}
	}
}

func TestRunShellAgainstNode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the recorded commands are for /bin/sh")
	}
	var v connectionVectors
	loadVectors(t, "connection", &v)
	atLeast(t, "shell", len(v.Shell), 10)
	failed := 0
	for _, c := range v.Shell {
		err := runShellWithin(context.Background(), c.Cwd, c.Command, time.Duration(c.TimeoutMs)*time.Millisecond)
		switch {
		case c.Err == nil && err != nil:
			t.Errorf("%q: error %v, Node ran it without one", c.Command, err)
		case c.Err != nil && (err == nil || err.Error() != *c.Err):
			t.Errorf("%q: error %q, Node throws %q", c.Command, err, *c.Err)
		}
		if c.Err != nil {
			failed++
		}
	}
	if failed < 6 {
		t.Fatalf("only %d failing commands", failed)
	}
}

// The systemd unit and the compose file from the Jest tests of the
// connection service.
const (
	sampleSystemd = `[Unit]
Description=BetterDesk Server

[Service]
Type=simple
ExecStart=/opt/betterdesk/betterdesk-server -mode all
Environment=P2P_FIRST=Y
Environment=ALWAYS_USE_RELAY=N
Restart=always

[Install]
WantedBy=multi-user.target
`
	sampleCompose = `services:
  server:
    image: betterdesk-server:local
    environment:
      - ENCRYPTED_ONLY=1
      - DB_URL=/opt/rustdesk/db_v2.sqlite3
      - P2P_FIRST=Y
  console:
    environment:
      - NODE_ENV=production
`
)

func relayOnlyVars() EnvVars {
	return EnvVarsFromSettings(NormalizeConnectionMode(ConnectionInput{
		Mode: "relay_only", P2PFallbackMs: 3000.0, SameNATRelay: false, AllowSharedNATInitiator: Undefined,
		LoggedInOnlyInitiator: true, OperatorOnlyOutbound: true,
	}))
}

func TestEnvVarsFromSettingsRelayOnly(t *testing.T) {
	got := EnvVarsFromSettings(NormalizeConnectionMode(ConnectionInput{
		Mode: "relay_only", P2PFallbackMs: 3000.0, SameNATRelay: false, AllowSharedNATInitiator: true,
		LoggedInOnlyInitiator: true, OperatorOnlyOutbound: true,
	}))
	want := EnvVars{P2PFirst: "N", AlwaysUseRelay: "Y", P2PFallbackMs: "3000", SameNATRelay: "N",
		AllowSharedNATInitiator: "Y", LoggedInOnlyInitiator: "Y", OperatorOnlyOutbound: "Y"}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if got := jsonText(t, got); got != `{"P2P_FIRST":"N","ALWAYS_USE_RELAY":"Y","P2P_FALLBACK_MS":"3000","SAME_NAT_RELAY":"N",`+
		`"ALLOW_SHARED_NAT_INITIATOR":"Y","LOGGED_IN_ONLY_INITIATOR":"Y","OPERATOR_ONLY_OUTBOUND":"Y"}` {
		t.Errorf("JSON %s", got)
	}
	for _, key := range ManagedEnvKeys {
		if want.Get(key) == "" {
			t.Errorf("Get(%q) is empty", key)
		}
	}
	if want.Get("OTHER") != "" {
		t.Error("Get of an unmanaged key has a value")
	}
}

func TestPatchSystemdSample(t *testing.T) {
	env := ParseSystemdEnvironment(sampleSystemd)
	if !reflect.DeepEqual(env, map[string]string{"P2P_FIRST": "Y", "ALWAYS_USE_RELAY": "N"}) {
		t.Fatalf("parsed %q", env)
	}
	want := `[Unit]
Description=BetterDesk Server

[Service]
Type=simple
Environment=P2P_FIRST=N
Environment=ALWAYS_USE_RELAY=Y
Environment=P2P_FALLBACK_MS=3000
Environment=SAME_NAT_RELAY=N
Environment=ALLOW_SHARED_NAT_INITIATOR=N
Environment=LOGGED_IN_ONLY_INITIATOR=Y
Environment=OPERATOR_ONLY_OUTBOUND=Y
ExecStart=/opt/betterdesk/betterdesk-server -mode all
Restart=always

[Install]
WantedBy=multi-user.target
`
	if got := PatchSystemdEnvironment(sampleSystemd, relayOnlyVars()); got != want {
		t.Errorf("patched unit\n%s\nwant\n%s", got, want)
	}
}

// TestSystemdEnvironmentSeveralAssignmentsOnALine reads and writes each
// assignment of an Environment= line, quoted or not, also on an indented
// line (BUGS SI9: a line was one variable, so a second assignment became
// part of the first one's value, and a save removed the line with the
// variables on it that the console does not manage).
func TestSystemdEnvironmentSeveralAssignmentsOnALine(t *testing.T) {
	unit := "[Service]\n" +
		"Environment=\"P2P_FIRST=N\" \"ALWAYS_USE_RELAY=Y\"\n" +
		"  Environment=SAME_NAT_RELAY=N LOG_LEVEL=debug\n" +
		"Environment='OPERATOR_ONLY_OUTBOUND=Y' \"TITLE=a b\"\n" +
		"Environment=P2P_FALLBACK_MS=500\n" +
		"ExecStart=/opt/betterdesk/betterdesk-server\n"
	want := map[string]string{"P2P_FIRST": "N", "ALWAYS_USE_RELAY": "Y", "SAME_NAT_RELAY": "N", "OPERATOR_ONLY_OUTBOUND": "Y", "P2P_FALLBACK_MS": "500"}
	if got := ParseSystemdEnvironment(unit); !reflect.DeepEqual(got, want) {
		t.Errorf("parsed %q, want %q", got, want)
	}
	patched := "[Service]\n" +
		"  Environment=LOG_LEVEL=debug\n" +
		"Environment=\"TITLE=a b\"\n" +
		"Environment=P2P_FIRST=N\n" +
		"Environment=ALWAYS_USE_RELAY=Y\n" +
		"Environment=P2P_FALLBACK_MS=3000\n" +
		"Environment=SAME_NAT_RELAY=N\n" +
		"Environment=ALLOW_SHARED_NAT_INITIATOR=N\n" +
		"Environment=LOGGED_IN_ONLY_INITIATOR=Y\n" +
		"Environment=OPERATOR_ONLY_OUTBOUND=Y\n" +
		"ExecStart=/opt/betterdesk/betterdesk-server\n"
	if got := PatchSystemdEnvironment(unit, relayOnlyVars()); got != patched {
		t.Errorf("patched unit\n%s\nwant\n%s", got, patched)
	}
}

func TestPatchComposeSample(t *testing.T) {
	env := ParseDockerComposeEnvironment(sampleCompose)
	if !reflect.DeepEqual(env, map[string]string{"P2P_FIRST": "Y"}) {
		t.Fatalf("parsed %q", env)
	}
	want := `services:
  server:
    image: betterdesk-server:local
    environment:
      - ENCRYPTED_ONLY=1
      - DB_URL=/opt/rustdesk/db_v2.sqlite3
      - P2P_FIRST=N
      - ALWAYS_USE_RELAY=Y
      - P2P_FALLBACK_MS=3000
      - SAME_NAT_RELAY=N
      - ALLOW_SHARED_NAT_INITIATOR=N
      - LOGGED_IN_ONLY_INITIATOR=Y
      - OPERATOR_ONLY_OUTBOUND=Y
  console:
    environment:
      - NODE_ENV=production
`
	got, err := PatchDockerComposeEnvironment(sampleCompose, relayOnlyVars())
	if err != nil || got != want {
		t.Errorf("patched compose file\n%s\n%v\nwant\n%s", got, err, want)
	}
	if _, err := PatchDockerComposeEnvironment("services:\n  console:\n    image: x\n", relayOnlyVars()); !errors.Is(err, ErrComposeServerNotFound) {
		t.Errorf("a file without the server: %v", err)
	}
}

func TestSettingsFromEnvSwitches(t *testing.T) {
	got := SettingsFromEnv(map[string]string{"P2P_FIRST": "Y", "LOGGED_IN_ONLY_INITIATOR": "Y"}, ConnectionSourceSystemd)
	if !got.LoggedInOnlyInitiator || got.OperatorOnlyOutbound || got.Mode != ModeP2PFirst || !got.Writable {
		t.Errorf("login-only: %+v", got)
	}
	got = SettingsFromEnv(map[string]string{"P2P_FIRST": "Y", "OPERATOR_ONLY_OUTBOUND": "Y"}, ConnectionSourceSystemd)
	if !got.OperatorOnlyOutbound || got.LoggedInOnlyInitiator {
		t.Errorf("operator-only: %+v", got)
	}
	got = SettingsFromEnv(nil, ConnectionSourceDefaults)
	want := ConnectionSettings{ConnectionMode: DefaultConnectionMode(), Source: ConnectionSourceDefaults}
	if got != want {
		t.Errorf("nothing set: %+v, want %+v", got, want)
	}
	if text := jsonText(t, got); text != `{"mode":"p2p_first","p2p_fallback_ms":2000,"same_nat_relay":true,"allow_shared_nat_initiator":false,`+
		`"logged_in_only_initiator":false,"operator_only_outbound":false,"source":"defaults","writable":false}` {
		t.Errorf("JSON %s", text)
	}
	for env, want := range map[string]string{"Y|N": ModeP2PFirst, "N|Y": ModeRelayOnly, "|Y": ModeRelayOnly, "N|": ModeRelayOnly, "|": ModeP2PFirst} {
		p2p, relay, _ := strings.Cut(env, "|")
		vars := map[string]string{}
		if p2p != "" {
			vars["P2P_FIRST"] = p2p
		}
		if relay != "" {
			vars["ALWAYS_USE_RELAY"] = relay
		}
		if got := ModeFromEnvVars(vars); got != want {
			t.Errorf("ModeFromEnvVars(%v) = %q, want %q", vars, got, want)
		}
	}
}

func TestSystemdPaths(t *testing.T) {
	if !strings.Contains(DefaultSystemdDropIn, "betterdesk-server.service.d") || !strings.Contains(DefaultSystemdDropIn, "50-betterdesk-connection.conf") {
		t.Errorf("drop-in path %q", DefaultSystemdDropIn)
	}
	s := New(Options{})
	if s.opts.SystemdUnit != "/etc/systemd/system/betterdesk-server.service" || s.opts.SystemdDropIn != DefaultSystemdDropIn ||
		s.opts.DockerEnvFile != "/.dockerenv" || s.opts.ServerServiceName != "betterdesk-server" {
		t.Errorf("defaults %+v", s.opts)
	}
}

// The two-container layout from the Jest test: the settings are read from
// and written to the server's API, and nothing needs a restart.
func TestConnectionModeSplitDocker(t *testing.T) {
	api := &stubAPI{}
	api.set("GET", &apiAnswer{Data: json.RawMessage(`{"mode":"p2p_first","p2p_fallback_ms":2500,"same_nat_relay":true,` +
		`"allow_shared_nat_initiator":false,"logged_in_only_initiator":true,"operator_only_outbound":false}`)})
	api.set("PUT", &apiAnswer{Data: json.RawMessage(`{"config":{"mode":"relay_only","p2p_fallback_ms":3500,"same_nat_relay":false,` +
		`"allow_shared_nat_initiator":true,"logged_in_only_initiator":true,"operator_only_outbound":true}}`)})
	api.set("GET", api.answers["GET"])
	s := New(Options{IsDocker: true, Getenv: envMap(map[string]string{"BETTERDESK_DOCKER_LAYOUT": "split"}), GoAPI: api.client(),
		DockerEnvFile: filepath.Join(t.TempDir(), "none"), SystemdUnit: filepath.Join(t.TempDir(), "none")})
	if !s.IsDockerSplitDeployment() {
		t.Fatal("not a split deployment")
	}
	ctx := context.Background()
	current, err := s.GetConnectionMode(ctx)
	if err != nil || current.Source != ConnectionSourceGoAPI || !current.Writable || current.P2PFallbackMs != 2500 || !current.LoggedInOnlyInitiator {
		t.Fatalf("read %+v, %v", current, err)
	}

	write, err := s.SetConnectionMode(ctx, ConnectionInput{Mode: "relay_only", P2PFallbackMs: 3500.0, SameNATRelay: false,
		AllowSharedNATInitiator: true, LoggedInOnlyInitiator: true, OperatorOnlyOutbound: true})
	if err != nil {
		t.Fatal(err)
	}
	calls := api.seen()
	wantBody := `{"mode":"relay_only","p2p_fallback_ms":3500,"same_nat_relay":false,"allow_shared_nat_initiator":true,` +
		`"logged_in_only_initiator":true,"operator_only_outbound":true}`
	if len(calls) != 2 || calls[1].Method != "PUT" || calls[1].Path != "/api/connection/config" || calls[1].Body != wantBody {
		t.Fatalf("calls %+v", calls)
	}
	if write.Source != ConnectionSourceGoAPI || !write.Settings.OperatorOnlyOutbound || write.Settings.Mode != ModeRelayOnly {
		t.Errorf("write %+v", write)
	}
	want := `{"source":"go-api","settings":{"mode":"relay_only","p2p_fallback_ms":3500,"same_nat_relay":false,"allow_shared_nat_initiator":true,` +
		`"logged_in_only_initiator":true,"operator_only_outbound":true,"source":"go-api","writable":true},"restart":null,"restartRequired":null,"dockerMode":true}`
	if got := jsonText(t, write.Object()); got != want {
		t.Errorf("answer\n%s\nwant\n%s", got, want)
	}

	// Without an answer from the server the defaults come back, not
	// writable.
	api.set("GET", &apiAnswer{Fail: true, Status: 502})
	log := &recordingLogger{}
	s.log = log
	current, err = s.GetConnectionMode(ctx)
	wantSettings := ConnectionSettings{ConnectionMode: DefaultConnectionMode(), Source: ConnectionSourceGoAPI}
	if err != nil || current != wantSettings {
		t.Errorf("read without the server %+v, %v", current, err)
	}
	if lines := log.all(); len(lines) != 1 || lines[0] != "error: Failed to read split-Docker connection settings: Request failed with status code 502" {
		t.Errorf("logged %q", lines)
	}
}

// A unit the console may not write names the installer's update, which
// writes it (BUGS NL4: the hint named a script of the Node console).
func TestSetConnectionModePermissionDenied(t *testing.T) {
	dir := t.TempDir()
	unit := filepath.Join(dir, "unit")
	if err := os.WriteFile(unit, []byte(sampleSystemd), 0o644); err != nil {
		t.Fatal(err)
	}
	for name, fsys := range map[string]FS{
		"write refused": &failingFS{failWriteOf: unit},
		"read refused":  &refuseRead{path: unit},
	} {
		s := New(Options{SystemdUnit: unit, DockerEnvFile: filepath.Join(dir, "none"), FS: fsys, Getenv: envMap(nil)})
		_, err := s.SetConnectionMode(context.Background(), ConnectionInput{Mode: "relay_only"})
		want := "Cannot write systemd connection settings (permission denied on " + unit + "). " +
			"Run once as root: sudo ./betterdesk.sh --update " +
			"then retry \u2014 or set ALLOW_SHARED_NAT_INITIATOR via a systemd drop-in."
		if err == nil || err.Error() != want || !errors.Is(err, fs.ErrPermission) {
			t.Errorf("%s: error %v", name, err)
		}
		if got, _ := os.ReadFile(unit); string(got) != sampleSystemd {
			t.Errorf("%s: the unit changed", name)
		}
	}
}

type refuseRead struct {
	OSFS
	path string
}

func (f *refuseRead) ReadFile(path string) ([]byte, error) {
	if path == f.path {
		return nil, &fs.PathError{Op: "open", Path: path, Err: fs.ErrPermission}
	}
	return f.OSFS.ReadFile(path)
}

// Errors of the file system carry the message Node gave, because the
// connection routes answer with it.
func TestFileErrorMessages(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
		code string
	}{
		{&fs.PathError{Op: "open", Path: "/x", Err: fs.ErrNotExist}, "ENOENT: no such file or directory, open '/etc/x'", "ENOENT"},
		{&fs.PathError{Op: "open", Path: "/x", Err: fs.ErrPermission}, "EACCES: permission denied, open '/etc/x'", "EACCES"},
		{errors.New("something else"), "something else", ""},
	} {
		err := nodeError(c.err, "open", "/etc/x")
		var fileErr *FileError
		if err.Error() != c.want || !errors.As(err, &fileErr) || fileErr.Code != c.code || !errors.Is(err, c.err) {
			t.Errorf("nodeError(%v) = %v", c.err, err)
		}
		if again := nodeError(err, "rename", "/other"); again != err {
			t.Errorf("a wrapped error was wrapped again: %v", again)
		}
	}
	if nodeError(nil, "open", "/x") != nil || nodeReadError(nil, "/x") != nil {
		t.Error("no error became one")
	}
}

// Without a way to reload and restart, a systemd restart is an error, not
// a silent success.
func TestRestartServerWithoutServiceControl(t *testing.T) {
	dir := t.TempDir()
	unit := filepath.Join(dir, "unit")
	if err := os.WriteFile(unit, []byte(sampleSystemd), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(Options{SystemdUnit: unit, DockerEnvFile: filepath.Join(dir, "none"), Getenv: envMap(nil)})
	if _, err := s.RestartServer(context.Background()); err == nil || errors.Is(err, ErrNoRestart) {
		t.Fatalf("error %v", err)
	}
}

// Input puts read settings back through SetConnectionMode unchanged.
func TestConnectionModeInputRoundTrip(t *testing.T) {
	for _, mode := range []ConnectionMode{
		DefaultConnectionMode(),
		{Mode: ModeRelayOnly, P2PFallbackMs: 1500.5, SameNATRelay: false, AllowSharedNATInitiator: true, LoggedInOnlyInitiator: true, OperatorOnlyOutbound: true},
		{Mode: ModeP2PFirst, P2PFallbackMs: 0, SameNATRelay: true, OperatorOnlyOutbound: true},
	} {
		if got := NormalizeConnectionMode(mode.Input()); got != mode {
			t.Errorf("round trip of %+v gives %+v", mode, got)
		}
	}
}
