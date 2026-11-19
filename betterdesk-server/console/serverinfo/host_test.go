package serverinfo

import (
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

type hostVectors struct {
	NormalizeServerHost []struct {
		In  string     `json:"in"`
		Out HostResult `json:"out"`
	} `json:"normalize_server_host"`
	NormalizeHostInput []struct {
		In  string `json:"in"`
		Out string `json:"out"`
	} `json:"normalize_host_input"`
	StripRequestHost []struct {
		In  string `json:"in"`
		Out string `json:"out"`
	} `json:"strip_request_host"`
	PhoneUnreachable []struct {
		In  string `json:"in"`
		Out bool   `json:"out"`
	} `json:"phone_unreachable"`
	DefaultServer []struct {
		APIURL string `json:"apiUrl"`
		Host   string `json:"host"`
		HTTPS  bool   `json:"https"`
		Port   string `json:"port"`
	} `json:"default_server"`
}

func TestHostHelpersAgainstNode(t *testing.T) {
	var v hostVectors
	loadVectors(t, "hosts", &v)
	atLeast(t, "normalize_server_host", len(v.NormalizeServerHost), 2000)
	atLeast(t, "normalize_host_input", len(v.NormalizeHostInput), 2000)
	atLeast(t, "strip_request_host", len(v.StripRequestHost), 2000)
	atLeast(t, "phone_unreachable", len(v.PhoneUnreachable), 2000)
	m := &mismatches{t: t}

	valid := 0
	for _, c := range v.NormalizeServerHost {
		if c.Out.Valid {
			valid++
		}
		if got := NormalizeServerHost(c.In); got != c.Out {
			m.errorf("NormalizeServerHost(%q) = %+v, Node answers %+v", c.In, got, c.Out)
		}
	}
	if valid < 300 || valid > len(v.NormalizeServerHost)-300 {
		t.Fatalf("%d of %d hosts are valid: the vectors do not test both answers", valid, len(v.NormalizeServerHost))
	}
	for _, c := range v.NormalizeHostInput {
		if got := NormalizeHostInput(c.In); got != c.Out {
			m.errorf("NormalizeHostInput(%q) = %q, Node answers %q", c.In, got, c.Out)
		}
	}
	for _, c := range v.StripRequestHost {
		if got := StripRequestHost(c.In); got != c.Out {
			m.errorf("StripRequestHost(%q) = %q, Node answers %q", c.In, got, c.Out)
		}
	}
	unreachable := 0
	for _, c := range v.PhoneUnreachable {
		if c.Out {
			unreachable++
		}
		if got := IsPhoneUnreachableHost(c.In); got != c.Out {
			m.errorf("IsPhoneUnreachableHost(%q) = %v, Node answers %v", c.In, got, c.Out)
		}
	}
	if unreachable < 10 {
		t.Fatalf("only %d hosts count as unreachable", unreachable)
	}
}

// The JSON of a host result has "error" only when the host is refused.
func TestHostResultJSON(t *testing.T) {
	if got := jsonText(t, NormalizeServerHost("desk.example.com")); got != `{"valid":true,"host":"desk.example.com"}` {
		t.Errorf("valid: %s", got)
	}
	if got := jsonText(t, NormalizeServerHost("")); got != `{"valid":false,"error":"server_host_required","host":""}` {
		t.Errorf("empty: %s", got)
	}
	if got := jsonText(t, NormalizeServerHost("not valid")); got != `{"valid":false,"error":"server_host_invalid","host":""}` {
		t.Errorf("invalid: %s", got)
	}
}

func TestDefaultServerHostAgainstNode(t *testing.T) {
	var v hostVectors
	loadVectors(t, "hosts", &v)
	atLeast(t, "default_server", len(v.DefaultServer), 30)
	https := 0
	for _, c := range v.DefaultServer {
		s := New(Options{APIURL: c.APIURL})
		if got := s.DefaultServerHost(); got != c.Host {
			t.Errorf("API address %q: DefaultServerHost = %q, Node answers %q", c.APIURL, got, c.Host)
		}
		if got := s.DefaultUseHTTPS(); got != c.HTTPS {
			t.Errorf("API address %q: DefaultUseHTTPS = %v, Node answers %v", c.APIURL, got, c.HTTPS)
		}
		if got := s.DefaultAPIPort(); got != c.Port {
			t.Errorf("API address %q: DefaultAPIPort = %q, Node answers %q", c.APIURL, got, c.Port)
		}
		if c.HTTPS {
			https++
		}
	}
	if https < 5 {
		t.Fatalf("only %d https addresses", https)
	}
}

// snapshot is the state of the env files after a recorded case.
type snapshot struct {
	Durable      *fileState `json:"durable"`
	Legacy       *fileState `json:"legacy"`
	DataDirFiles []string   `json:"dataDirFiles"`
	RootFiles    []string   `json:"rootFiles"`
}

// endpointStage is the layout of one recorded case on disk.
type endpointStage struct {
	root, dataDir, envPath, durablePath string
}

// stageEndpoints lays out the two env files as the script did.
func stageEndpoints(t testing.TB, root string, durable, legacy textOrDir, noDataDir bool) endpointStage {
	t.Helper()
	st := endpointStage{root: root, dataDir: filepath.Join(root, "data"), envPath: filepath.Join(root, ".env")}
	if noDataDir {
		st.dataDir = filepath.Join(root, "missing", "data")
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatal(err)
		}
	} else if err := os.MkdirAll(st.dataDir, 0o755); err != nil {
		t.Fatal(err)
	}
	st.durablePath = filepath.Join(st.dataDir, DurableBasename)
	if !noDataDir {
		durable.put(t, st.durablePath, 0o644)
	}
	legacy.put(t, st.envPath, 0o644)
	return st
}

func (st endpointStage) check(t testing.TB, m *mismatches, label string, want snapshot) {
	t.Helper()
	checkFile(t, m, label, st.durablePath, want.Durable)
	checkFile(t, m, label, st.envPath, want.Legacy)
	if got := listDir(st.dataDir); !sameNames(got, want.DataDirFiles) {
		m.errorf("%s: data folder holds %q, Node left %q", label, got, want.DataDirFiles)
	}
	if got := listDir(st.root); !sameNames(got, want.RootFiles) {
		m.errorf("%s: folder holds %q, Node left %q", label, got, want.RootFiles)
	}
}

func sameNames(a, b []string) bool {
	if (a == nil) != (b == nil) || len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type resolveVectors struct {
	Resolve []struct {
		Cfg     oracleConfig      `json:"cfg"`
		Env     map[string]string `json:"env"`
		Durable textOrDir         `json:"durable"`
		Legacy  textOrDir         `json:"legacy"`
		Headers *struct {
			ForwardedHost *string `json:"x-forwarded-host"`
			Host          *string `json:"host"`
		} `json:"headers"`
		Trust     bool              `json:"trust"`
		QueryHost string            `json:"queryHost"`
		Endpoints ResolvedEndpoints `json:"endpoints"`
		Host      string            `json:"host"`
		NoReq     ResolvedEndpoints `json:"noReq"`
		Panel     string            `json:"panel"`
		After     snapshot          `json:"after"`
	} `json:"resolve"`
}

func TestResolveEndpointsAgainstNode(t *testing.T) {
	var v resolveVectors
	loadVectors(t, "resolve", &v)
	atLeast(t, "resolve", len(v.Resolve), 3000)
	base := t.TempDir()
	m := &mismatches{t: t}
	sources := map[string]int{}
	for i, c := range v.Resolve {
		label := "case " + strconv.Itoa(i)
		st := stageEndpoints(t, filepath.Join(base, strconv.Itoa(i)), c.Durable, c.Legacy, false)
		opts := c.Cfg.options()
		opts.DataDir, opts.EnvPath = st.dataDir, st.envPath
		opts.Getenv = envMap(c.Env)
		opts.Logger = &recordingLogger{}
		s := New(opts)

		var req *RequestHost
		if c.Headers != nil {
			// The request goes through RequestHostOf, as a route's does.
			r := &http.Request{Header: http.Header{}}
			if c.Headers.ForwardedHost != nil {
				r.Header.Set("X-Forwarded-Host", latin1Bytes(*c.Headers.ForwardedHost))
			}
			if c.Headers.Host != nil {
				r.Host = latin1Bytes(*c.Headers.Host)
			}
			req = RequestHostOf(r)
		}

		c.Endpoints.Relay, c.NoReq.Relay = relayUM4(c.Endpoints.Relay), relayUM4(c.NoReq.Relay)
		if got := s.ResolveRustDeskEndpoints(req, c.QueryHost); got != c.Endpoints {
			m.errorf("%s: endpoints %+v, want %+v", label, got, c.Endpoints)
		}
		if got := s.ResolveClientFacingHost(req, c.QueryHost); got != c.Host {
			m.errorf("%s: host %q, Node answers %q", label, got, c.Host)
		}
		if got := s.ResolveRustDeskEndpoints(nil, c.QueryHost); got != c.NoReq {
			m.errorf("%s: endpoints without a request %+v, want %+v", label, got, c.NoReq)
		}
		if got := s.ReadPanelPublicHost(); got != c.Panel {
			m.errorf("%s: panel host %q, Node answers %q", label, got, c.Panel)
		}
		st.check(t, m, label, c.After)
		sources[c.Endpoints.Sources.Host]++
		sources["api:"+c.Endpoints.Sources.API]++
	}
	for _, source := range []string{SourceEnv, SourceQuery, SourcePanelHost, SourceConfig, SourceRequest, "api:" + SourceDerived, "api:" + SourceEnv} {
		if sources[source] < 20 {
			t.Errorf("only %d cases with source %q", sources[source], source)
		}
	}
}

// latin1Bytes turns the text a script put into a header into the bytes
// that arrive for it: Node reads header bytes as Latin-1 characters.
func latin1Bytes(s string) string {
	b := make([]byte, 0, len(s))
	for _, r := range s {
		b = append(b, byte(r))
	}
	return string(b)
}

func TestResolvedEndpointsJSON(t *testing.T) {
	s := New(Options{APIURL: "http://127.0.0.1:21114/api", GoAPIPort: 21114, DataDir: t.TempDir(), EnvPath: filepath.Join(t.TempDir(), ".env"),
		Getenv: envMap(nil)})
	got := jsonText(t, s.ResolveRustDeskEndpoints(nil, ""))
	want := `{"host":"127.0.0.1","relay":"127.0.0.1:21117","api":"http://127.0.0.1:21114",` +
		`"sources":{"host":"config","relay":"config","api":"config"},"env_override_active":false}`
	if got != want {
		t.Errorf("answer\n%s\nwant\n%s", got, want)
	}
}

// Several X-Forwarded-Host headers reach the service joined with ", ", as
// Node joined them, and the first one counts.
func TestRequestHostOfJoinsHeaders(t *testing.T) {
	r := &http.Request{Header: http.Header{}, Host: "console.example.com:5000"}
	r.Header.Add("X-Forwarded-Host", "first.example:8443")
	r.Header.Add("X-Forwarded-Host", "second.example")
	req := RequestHostOf(r)
	if req.ForwardedHost != "first.example:8443, second.example" || req.Host != "console.example.com:5000" {
		t.Fatalf("got %+v", req)
	}
	if got := StripRequestHost(req.ForwardedHost); got != "first.example" {
		t.Fatalf("StripRequestHost = %q", got)
	}
	if RequestHostOf(nil) != nil {
		t.Fatal("a nil request gives a host")
	}
}
