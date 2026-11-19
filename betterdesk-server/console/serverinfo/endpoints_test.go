package serverinfo

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type endpointVectors struct {
	Normalize []struct {
		Raw       map[string]any `json:"raw"`
		Normalize struct {
			Out *PublicEndpoints `json:"out"`
			Err *string          `json:"err"`
		} `json:"normalize"`
		Validate struct {
			Out *PublicEndpoints `json:"out"`
			Err *string          `json:"err"`
		} `json:"validate"`
	} `json:"normalize"`
	OverrideActive []struct {
		Env PublicEndpoints `json:"env"`
		Out bool            `json:"out"`
	} `json:"override_active"`
	Read []struct {
		Durable textOrDir         `json:"durable"`
		Legacy  textOrDir         `json:"legacy"`
		Env     map[string]string `json:"env"`
		Options struct {
			NoDataDir bool `json:"noDataDir"`
		} `json:"options"`
		First       PublicEndpoints `json:"first"`
		Panel       string          `json:"panel"`
		Settings    PublicEndpoints `json:"settings"`
		After       snapshot        `json:"after"`
		Second      PublicEndpoints `json:"second"`
		AfterSecond snapshot        `json:"afterSecond"`
	} `json:"read"`
	Write []struct {
		Durable textOrDir         `json:"durable"`
		Legacy  textOrDir         `json:"legacy"`
		Env     map[string]string `json:"env"`
		Raw     map[string]any    `json:"raw"`
		Options struct {
			NoDataDir bool `json:"noDataDir"`
		} `json:"options"`
		Out      *PublicEndpoints  `json:"out"`
		Err      *string           `json:"err"`
		EnvAfter map[string]string `json:"envAfter"`
		After    snapshot          `json:"after"`
		Read     PublicEndpoints   `json:"read"`
	} `json:"write"`
}

func endpointsInput(raw map[string]any) PublicEndpointsInput {
	return PublicEndpointsInput{
		PublicServerID:    Field(raw, "public_server_id"),
		PublicRelayServer: Field(raw, "public_relay_server"),
		PublicAPIURL:      Field(raw, "public_api_url"),
	}
}

func TestNormalizeSettingsAgainstNode(t *testing.T) {
	var v endpointVectors
	loadVectors(t, "endpoints", &v)
	atLeast(t, "normalize", len(v.Normalize), 1300)
	m := &mismatches{t: t}
	errorsSeen := map[string]int{}
	accepted := 0
	check := func(name string, raw map[string]any, got PublicEndpoints, err error, wantOut *PublicEndpoints, wantErr *string) {
		switch {
		case wantErr != nil:
			errorsSeen[*wantErr]++
			if err == nil || err.Error() != *wantErr {
				m.errorf("%s(%v) = %+v, %v; Node throws %q", name, raw, got, err, *wantErr)
			}
		case err != nil:
			m.errorf("%s(%v) fails with %v; Node answers %+v", name, raw, err, *wantOut)
		case got != *wantOut:
			m.errorf("%s(%v) = %+v; Node answers %+v", name, raw, got, *wantOut)
		default:
			accepted++
		}
	}
	ipv6 := &ipv6Cases{t: t}
	all := m
	for _, c := range v.Normalize {
		m = ipv6.mismatches(all, c.Raw)
		in := endpointsInput(c.Raw)
		got, err := NormalizeSettings(in)
		check("NormalizeSettings", c.Raw, got, err, c.Normalize.Out, c.Normalize.Err)
		got, err = ValidateSettings(in)
		check("ValidateSettings", c.Raw, got, err, c.Validate.Out, validateErrorSI7(c.Raw, c.Validate.Err))
	}
	for _, text := range []string{"invalid_host", "invalid_api_url", "invalid_env_value", "invalid_public_host", "invalid_public_api_url"} {
		if errorsSeen[text] < 20 {
			t.Errorf("only %d cases fail with %q", errorsSeen[text], text)
		}
	}
	if accepted < 300 {
		t.Fatalf("only %d accepted settings", accepted)
	}
	for _, c := range v.OverrideActive {
		if got := IsEnvOverrideActive(c.Env); got != c.Out {
			t.Errorf("IsEnvOverrideActive(%+v) = %v, Node answers %v", c.Env, got, c.Out)
		}
	}
	if len(v.OverrideActive) != 8 {
		t.Fatalf("%d override cases, want 8", len(v.OverrideActive))
	}
}

// The errors a route tells apart are the exported values.
func TestValidateSettingsErrors(t *testing.T) {
	_, err := ValidateSettings(PublicEndpointsInput{PublicServerID: "not valid host!!"})
	if !errors.Is(err, ErrInvalidPublicHost) {
		t.Errorf("bad host: %v", err)
	}
	_, err = ValidateSettings(PublicEndpointsInput{PublicAPIURL: "ftp://bad"})
	if !errors.Is(err, ErrInvalidPublicAPIURL) {
		t.Errorf("bad API address: %v", err)
	}
	_, err = ValidateSettings(PublicEndpointsInput{PublicServerID: "evil.example.com\nSESSION_SECRET=hacked"})
	if !errors.Is(err, ErrInvalidPublicHost) {
		t.Errorf("line break in the host: %v", err)
	}
	// A line break or NUL in the API address is a bad API address, not a
	// bad host (BUGS SI7)
	for _, api := range []string{"https://api.example.com\nOTHER=1", "https://api.\rexample.com", "https://api.example.com/\x00"} {
		_, err = ValidateSettings(PublicEndpointsInput{PublicServerID: "remote.example.com", PublicAPIURL: api})
		if !errors.Is(err, ErrInvalidPublicAPIURL) {
			t.Errorf("line break in the API address %q: %v", api, err)
		}
		if _, err = NormalizeSettings(PublicEndpointsInput{PublicAPIURL: api}); !errors.Is(err, ErrInvalidEnvValue) || err.Error() != "invalid_env_value" {
			t.Errorf("normalized %q: %v", api, err)
		}
	}
	got, err := ValidateSettings(PublicEndpointsInput{
		PublicServerID: "remote.example.com", PublicRelayServer: "relay.example.com", PublicAPIURL: "https://api.example.com/",
	})
	want := PublicEndpoints{PublicServerID: "remote.example.com", PublicRelayServer: "relay.example.com", PublicAPIURL: "https://api.example.com"}
	if err != nil || got != want {
		t.Errorf("split domains: %+v, %v", got, err)
	}
	got, err = ValidateSettings(PublicEndpointsInput{PublicServerID: Undefined, PublicRelayServer: nil, PublicAPIURL: ""})
	if err != nil || got != (PublicEndpoints{}) {
		t.Errorf("cleared: %+v, %v", got, err)
	}
}

func TestReadPublicEndpointsAgainstNode(t *testing.T) {
	var v endpointVectors
	loadVectors(t, "endpoints", &v)
	atLeast(t, "read", len(v.Read), 700)
	base := t.TempDir()
	m := &mismatches{t: t}
	migrated := 0
	for i, c := range v.Read {
		label := "case " + strconv.Itoa(i)
		st := stageEndpoints(t, filepath.Join(base, strconv.Itoa(i)), c.Durable, c.Legacy, c.Options.NoDataDir)
		s := New(Options{DataDir: st.dataDir, EnvPath: st.envPath, Getenv: envMap(c.Env), Logger: &recordingLogger{}})

		if got := s.ReadPublicEndpointEnv(); got != c.First {
			m.errorf("%s: settings %+v, Node answers %+v", label, got, c.First)
		}
		if got := s.ReadPanelPublicHostValue(); got != c.Panel {
			m.errorf("%s: panel host %q, Node answers %q", label, got, c.Panel)
		}
		if got := s.GetPublicEndpointSettings(); got != c.Settings {
			m.errorf("%s: settings again %+v, Node answers %+v", label, got, c.Settings)
		}
		st.check(t, m, label, c.After)
		if !c.Durable.Exists && c.After.Durable != nil {
			migrated++
		}

		// The copy into the durable file happens once per process.
		if err := os.RemoveAll(st.durablePath); err != nil {
			t.Fatal(err)
		}
		if got := s.ReadPublicEndpointEnv(); got != c.Second {
			m.errorf("%s: settings after the file went %+v, Node answers %+v", label, got, c.Second)
		}
		st.check(t, m, label+" (second read)", c.AfterSecond)
	}
	if migrated < 50 {
		t.Fatalf("only %d cases copied into the durable file", migrated)
	}
}

func TestWritePublicEndpointsAgainstNode(t *testing.T) {
	var v endpointVectors
	loadVectors(t, "endpoints", &v)
	atLeast(t, "write", len(v.Write), 900)
	base := t.TempDir()
	m := &mismatches{t: t}
	written, refused := 0, 0
	ipv6 := &ipv6Cases{t: t}
	all := m
	for i, c := range v.Write {
		label := "case " + strconv.Itoa(i)
		m := ipv6.mismatches(all, c.Raw, c.Env, c.Durable, c.Legacy)
		st := stageEndpoints(t, filepath.Join(base, strconv.Itoa(i)), c.Durable, c.Legacy, c.Options.NoDataDir)
		s := New(Options{DataDir: st.dataDir, EnvPath: st.envPath, Getenv: envMap(c.Env), Logger: &recordingLogger{}})

		got, err := s.SavePublicEndpointSettings(endpointsInput(c.Raw))
		switch {
		case c.Err != nil:
			refused++
			want := strings.ReplaceAll(*validateErrorSI7(c.Raw, c.Err), "{ROOT}", st.root)
			if err == nil || err.Error() != want {
				m.errorf("%s: error %v, Node throws %q", label, err, want)
			}
		case err != nil:
			m.errorf("%s: error %v, Node answers %+v", label, err, *c.Out)
		default:
			written++
			if got != *c.Out {
				m.errorf("%s: stored %+v, Node stored %+v", label, got, *c.Out)
			}
		}
		if read := s.ReadPublicEndpointEnv(); read != c.Read {
			m.errorf("%s: reads back %+v, Node reads %+v", label, read, c.Read)
		}
		for _, key := range durableKeys {
			if got := s.env(key); got != c.EnvAfter[key] {
				m.errorf("%s: environment %s = %q, Node has %q", label, key, got, c.EnvAfter[key])
			}
		}
		st.check(t, m, label, c.After)
	}
	if written < 300 || refused < 100 {
		t.Fatalf("%d writes and %d refusals: too few", written, refused)
	}
}

// failingFS makes one operation fail.
type failingFS struct {
	OSFS
	failRename  bool
	failWriteOf string
	chmodErr    error
	chmods      []string
}

func (f *failingFS) Rename(oldPath, newPath string) error {
	if f.failRename {
		return &fs.PathError{Op: "rename", Path: oldPath, Err: fs.ErrPermission}
	}
	return f.OSFS.Rename(oldPath, newPath)
}

func (f *failingFS) WriteFile(path string, data []byte, perm fs.FileMode) error {
	if f.failWriteOf != "" && strings.Contains(path, f.failWriteOf) {
		return &fs.PathError{Op: "open", Path: path, Err: fs.ErrPermission}
	}
	return f.OSFS.WriteFile(path, data, perm)
}

func (f *failingFS) Chmod(path string, mode fs.FileMode) error {
	f.chmods = append(f.chmods, path+" "+strconv.FormatUint(uint64(mode), 8))
	if f.chmodErr != nil {
		return f.chmodErr
	}
	return f.OSFS.Chmod(path, mode)
}

// A save goes through a temporary file: when the rename fails, the file in
// place is untouched and the environment keeps its values.
func TestSavePublicEndpointsRenameFails(t *testing.T) {
	root := t.TempDir()
	dataDir, envPath := filepath.Join(root, "data"), filepath.Join(root, ".env")
	durable := filepath.Join(dataDir, DurableBasename)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(durable, []byte("PUBLIC_SERVER_ID=old.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fsys := &failingFS{failRename: true}
	s := New(Options{DataDir: dataDir, EnvPath: envPath, FS: fsys, Getenv: envMap(nil)})
	_, err := s.SavePublicEndpointSettings(PublicEndpointsInput{PublicServerID: "new.example"})
	if err == nil || !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("error %v, want a permission error", err)
	}
	if got, _ := os.ReadFile(durable); string(got) != "PUBLIC_SERVER_ID=old.example\n" {
		t.Errorf("the durable file changed: %q", got)
	}
	if _, err := os.Stat(envPath); err == nil {
		t.Error("the console's env file was written after the durable file failed")
	}
	if got := s.ReadPublicEndpointEnv().PublicServerID; got != "old.example" {
		t.Errorf("reads %q after a failed save", got)
	}
}

// When the console's env file cannot be written, the durable file has the
// new values already and the environment values are not replaced: a
// variable set in the environment keeps winning.
func TestSavePublicEndpointsSecondFileFails(t *testing.T) {
	root := t.TempDir()
	dataDir, envPath := filepath.Join(root, "data"), filepath.Join(root, ".env")
	fsys := &failingFS{failWriteOf: filepath.Join(root, "..env.")}
	s := New(Options{DataDir: dataDir, EnvPath: envPath, FS: fsys, Getenv: envMap(map[string]string{"PUBLIC_SERVER_ID": "from-compose.example"})})
	_, err := s.SavePublicEndpointSettings(PublicEndpointsInput{PublicServerID: "new.example"})
	if err == nil {
		t.Fatal("no error")
	}
	got, readErr := os.ReadFile(filepath.Join(dataDir, DurableBasename))
	if readErr != nil || !strings.Contains(string(got), "PUBLIC_SERVER_ID=new.example\n") {
		t.Errorf("durable file %q, %v", got, readErr)
	}
	if got := s.ReadPublicEndpointEnv().PublicServerID; got != "from-compose.example" {
		t.Errorf("reads %q: the environment value was replaced although the save failed", got)
	}
}

// The written files are for their owner only, also on a file system that
// refuses the change of mode after the rename.
func TestSavePublicEndpointsModes(t *testing.T) {
	root := t.TempDir()
	dataDir, envPath := filepath.Join(root, "data"), filepath.Join(root, ".env")
	if err := os.WriteFile(envPath, []byte("PORT=5000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fsys := &failingFS{chmodErr: errors.New("not supported")}
	s := New(Options{DataDir: dataDir, EnvPath: envPath, FS: fsys, Getenv: envMap(nil)})
	if _, err := s.SavePublicEndpointSettings(PublicEndpointsInput{PublicServerID: "new.example"}); err != nil {
		t.Fatalf("a refused chmod failed the save: %v", err)
	}
	durable := filepath.Join(dataDir, DurableBasename)
	if want := []string{durable + " 600", envPath + " 600"}; strings.Join(fsys.chmods, "|") != strings.Join(want, "|") {
		t.Errorf("chmod calls %q, want %q", fsys.chmods, want)
	}
	for _, path := range []string{durable, envPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
			t.Errorf("%s has mode %o", path, info.Mode().Perm())
		}
	}
	if names := listDir(dataDir); len(names) != 1 {
		t.Errorf("data folder holds %q, want only the settings file", names)
	}
}

// After a save the service reads the saved values even where the process
// environment had others, and a cleared setting no longer comes from the
// environment.
func TestSavePublicEndpointsReplacesEnvironment(t *testing.T) {
	root := t.TempDir()
	s := New(Options{DataDir: filepath.Join(root, "data"), EnvPath: filepath.Join(root, ".env"), Getenv: envMap(map[string]string{
		"PUBLIC_SERVER_ID":    "from-compose.example",
		"PUBLIC_RELAY_SERVER": "relay-compose.example",
		"PANEL_PUBLIC_HOST":   "panel-compose.example",
	})})
	if got := s.ReadPublicEndpointEnv(); got.PublicServerID != "from-compose.example" || got.PublicRelayServer != "relay-compose.example" {
		t.Fatalf("before the save: %+v", got)
	}
	if _, err := s.SavePublicEndpointSettings(PublicEndpointsInput{PublicServerID: "saved.example", PublicRelayServer: ""}); err != nil {
		t.Fatal(err)
	}
	want := PublicEndpoints{PublicServerID: "saved.example"}
	if got := s.ReadPublicEndpointEnv(); got != want {
		t.Errorf("after the save: %+v, want %+v", got, want)
	}
	if got := s.ReadPanelPublicHostValue(); got != "panel-compose.example" {
		t.Errorf("panel host %q: a save must leave it alone", got)
	}
}
