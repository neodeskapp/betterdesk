package serverinfo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// endpointsWorld is a console's data folder and env file, and the
// environment a start gives it.
type endpointsWorld struct {
	dataDir, envPath string
}

func newEndpointsWorld(t *testing.T) endpointsWorld {
	t.Helper()
	root := t.TempDir()
	return endpointsWorld{dataDir: filepath.Join(root, "data"), envPath: filepath.Join(root, ".env")}
}

// start is a start of the console with env as its environment.
func (w endpointsWorld) start(env map[string]string) *Service {
	return New(Options{APIURL: "http://127.0.0.1:21114/api", GoAPIPort: 21114, DataDir: w.dataDir, EnvPath: w.envPath,
		Getenv: envMap(env), Logger: &recordingLogger{}})
}

func (w endpointsWorld) durable(t *testing.T) (string, bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(w.dataDir, DurableBasename))
	if os.IsNotExist(err) {
		return "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(raw), true
}

var composeEnv = map[string]string{
	EnvPublicServerID:    "id-env.example",
	EnvPublicRelayServer: "relay-env.example",
	EnvPublicAPIURL:      "https://api-env.example",
}

// L87: the addresses saved in the console win over the environment after
// a restart; the environment is only the first default.
func TestSavedEndpointsWinOverTheEnvironmentAfterARestart(t *testing.T) {
	w := newEndpointsWorld(t)
	first := w.start(composeEnv)
	if got := first.ReadPublicEndpointEnv(); got.PublicServerID != "id-env.example" || got.PublicAPIURL != "https://api-env.example" {
		t.Fatalf("first start: %+v, want the environment's addresses", got)
	}
	if _, err := first.SavePublicEndpointSettings(PublicEndpointsInput{
		PublicServerID: "id-saved.example", PublicRelayServer: "relay-saved.example", PublicAPIURL: "https://api-saved.example",
	}); err != nil {
		t.Fatal(err)
	}

	restarted := w.start(composeEnv)
	want := PublicEndpoints{PublicServerID: "id-saved.example", PublicRelayServer: "relay-saved.example", PublicAPIURL: "https://api-saved.example"}
	if got := restarted.ReadPublicEndpointEnv(); got != want {
		t.Fatalf("after a restart: %+v, want the saved %+v", got, want)
	}
	got := restarted.ResolveRustDeskEndpoints(nil, "")
	if got.Host != "id-saved.example" || got.Relay != "relay-saved.example:21117" || got.API != "https://api-saved.example" {
		t.Errorf("client addresses after a restart: %+v", got)
	}
	if got.Sources != (EndpointSources{Host: SourceSaved, Relay: SourceSaved, API: SourceSaved}) {
		t.Errorf("sources %+v, want saved for all three", got.Sources)
	}
}

// L87: a value the environment sets says it comes from the environment,
// and the start copies nothing into the public endpoints file, so the
// page does not call it saved.
func TestEnvironmentEndpointsAreNotCalledSaved(t *testing.T) {
	w := newEndpointsWorld(t)
	s := w.start(map[string]string{EnvPublicServerID: "id-env.example"})
	got := s.ResolveRustDeskEndpoints(nil, "")
	if got.Host != "id-env.example" || got.Sources.Host != SourceEnv || got.Sources.API != SourceDerived {
		t.Errorf("from the environment: %+v", got)
	}
	if text, ok := w.durable(t); ok {
		t.Errorf("the start wrote the public endpoints file: %q", text)
	}
	// Once the page saves another address, that one is saved and wins
	if _, err := s.SavePublicEndpointSettings(PublicEndpointsInput{PublicServerID: "id-saved.example"}); err != nil {
		t.Fatal(err)
	}
	if got := w.start(map[string]string{EnvPublicServerID: "id-env.example"}).ResolveRustDeskEndpoints(nil, ""); got.Host != "id-saved.example" || got.Sources.Host != SourceSaved || got.Sources.API != SourceDerived {
		t.Errorf("after a save and a restart: %+v", got)
	}
}

// L87: an address left empty on the page leaves the environment's in
// force: only a value counts as saved.
func TestAnEmptySavedEndpointLeavesTheEnvironmentsValue(t *testing.T) {
	w := newEndpointsWorld(t)
	if _, err := w.start(nil).SavePublicEndpointSettings(PublicEndpointsInput{PublicServerID: "id-saved.example"}); err != nil {
		t.Fatal(err)
	}
	got := w.start(composeEnv).ReadPublicEndpointEnv()
	want := PublicEndpoints{PublicServerID: "id-saved.example", PublicRelayServer: "relay-env.example", PublicAPIURL: "https://api-env.example"}
	if got != want {
		t.Errorf("%+v, want %+v", got, want)
	}
}

// L87: PUBLIC_ENDPOINTS_ENV_OVERRIDE on lets the environment win over
// what the console saved, as ENROLLMENT_MODE_ENV_OVERRIDE does for the
// enrollment mode.
func TestEndpointOverrideSwitchLetsTheEnvironmentWin(t *testing.T) {
	w := newEndpointsWorld(t)
	if _, err := w.start(nil).SavePublicEndpointSettings(PublicEndpointsInput{PublicServerID: "id-saved.example", PublicRelayServer: "relay-saved.example"}); err != nil {
		t.Fatal(err)
	}
	for _, on := range []string{"Y", "yes", "1", "true", "on"} {
		env := map[string]string{EnvPublicServerID: "id-env.example", EnvPublicEndpointsOverride: on}
		got := w.start(env).ResolveRustDeskEndpoints(nil, "")
		if got.Host != "id-env.example" || got.Sources.Host != SourceEnv {
			t.Errorf("override %q: host %q from %q, want the environment's", on, got.Host, got.Sources.Host)
		}
		// A setting the environment does not set stays the saved one
		if got.Relay != "relay-saved.example:21117" || got.Sources.Relay != SourceSaved {
			t.Errorf("override %q: relay %q from %q, want the saved one", on, got.Relay, got.Sources.Relay)
		}
	}
	env := map[string]string{EnvPublicServerID: "id-env.example", EnvPublicEndpointsOverride: "N"}
	if got := w.start(env).ReadPublicEndpointEnv(); got.PublicServerID != "id-saved.example" {
		t.Errorf("override off: %q, want the saved one", got.PublicServerID)
	}
}

// PANEL_PUBLIC_HOST, which the page does not save, keeps the
// environment first.
func TestPanelPublicHostKeepsTheEnvironmentFirst(t *testing.T) {
	w := newEndpointsWorld(t)
	if err := os.MkdirAll(w.dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(w.dataDir, DurableBasename), []byte("PANEL_PUBLIC_HOST=old.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := w.start(map[string]string{PanelPublicHostKey: "new.example"}).ReadPanelPublicHostValue(); got != "new.example" {
		t.Errorf("panel host %q, want the environment's", got)
	}
	if got := w.start(nil).ReadPanelPublicHostValue(); got != "old.example" {
		t.Errorf("panel host without the variable %q, want the file's", got)
	}
}

// endpointKeys are the three public endpoint settings.
var endpointKeys = []string{EnvPublicServerID, EnvPublicRelayServer, EnvPublicAPIURL}

// envSets reports whether env sets one of keys to more than white space.
func envSets(env map[string]string, keys ...string) bool {
	for _, key := range keys {
		if strings.TrimFunc(env[key], fuzzIsSpace) != "" {
			return true
		}
	}
	return false
}

// fileSets reports whether a recorded env file sets one of keys, read by
// Node's rule (nodeReadEnv).
func fileSets(f textOrDir, keys ...string) bool {
	if !f.Exists || f.Dir {
		return false
	}
	values := nodeReadEnv(f.Text)
	for _, key := range keys {
		if values[key] != "" {
			return true
		}
	}
	return false
}

// nodeCopiedIntoDurable reports whether Node's first read copied the
// environment or the console's env file into the public endpoints file:
// the file had no value and one of the others had one. The console no
// longer copies (BUGS L87), so the file is left as it was.
func nodeCopiedIntoDurable(env map[string]string, durable, legacy textOrDir) bool {
	return !fileSets(durable, durableKeys...) && (envSets(env, durableKeys...) || fileSets(legacy, durableKeys...))
}

// savedOverEnv reports whether a value saved in the public endpoints file
// now wins over the environment, where Node let the environment win
// (BUGS L87).
func savedOverEnv(env map[string]string, durable textOrDir) bool {
	return fileSets(durable, endpointKeys...) && envSets(env, endpointKeys...)
}

// L90: a save tells the signal server the relay in force: the saved one,
// else the environment's.
func TestSaveTellsTheRelayInForce(t *testing.T) {
	w := newEndpointsWorld(t)
	var told []string
	s := New(Options{APIURL: "http://127.0.0.1:21114/api", DataDir: w.dataDir, EnvPath: w.envPath,
		Getenv: envMap(map[string]string{EnvPublicRelayServer: "relay-env.example"}), Logger: &recordingLogger{},
		OnPublicRelay: func(relay string) { told = append(told, relay) }})
	if _, err := s.SavePublicEndpointSettings(PublicEndpointsInput{PublicRelayServer: "relay-saved.example"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePublicEndpointSettings(PublicEndpointsInput{PublicRelayServer: "bad host!"}); err == nil {
		t.Fatal("a bad relay was saved")
	}
	if _, err := s.SavePublicEndpointSettings(PublicEndpointsInput{}); err != nil {
		t.Fatal(err)
	}
	// The second save was refused and tells nothing; the cleared relay
	// leaves none until a start reads the environment again
	if len(told) != 2 || told[0] != "relay-saved.example" || told[1] != "" {
		t.Fatalf("told %q, want the saved relay, then none", told)
	}
}
