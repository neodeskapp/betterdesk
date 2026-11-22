package billingclock

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
)

// The vectors were written from the Node console by the scripts in
// testdata/nodeoracle (see the README there).

func loadJSON(t *testing.T, name string, into any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read the vectors: %v", err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
}

// TestZoneVectors replays what Intl.DateTimeFormat made of time zone
// names, case variants, offsets and other text (zones.json). A billing
// zone is one of those that the server's loader (time.LoadLocation) also
// loads as written, so the server uses the zone the settings show: other
// letter case, offsets and names only ICU knows are refused (BUGS CM2).
func TestZoneVectors(t *testing.T) {
	var file struct {
		Vectors [][2]*string `json:"vectors"`
	}
	loadJSON(t, "zones.json", &file)
	if len(file.Vectors) < 5000 {
		t.Fatalf("only %d vectors", len(file.Vectors))
	}
	failures, accepted, refusedByLoader := 0, 0, 0
	for _, v := range file.Vectors {
		input, want := *v[0], v[1]
		got, ok := resolveZone(input)
		if ok != (want != nil) || (ok && got != *want) {
			failures++
			if failures <= 20 {
				t.Errorf("resolveZone(%q) = %q, %v; Node: %v", input, got, ok, describe(want))
			}
		}
		// The check trims the name before it asks Intl and the loader
		name := jsTrim(input)
		_, intl := resolveZone(name)
		_, err := time.LoadLocation(name)
		loads := err == nil && name != "" && name != "Local"
		if valid := IsValidIANATimezone(input); valid != (intl && loads) {
			failures++
			if failures <= 20 {
				t.Errorf("IsValidIANATimezone(%q) = %v; Intl takes it: %v, the server's loader: %v", input, valid, intl, loads)
			}
		}
		switch {
		case intl && loads:
			accepted++
		case intl:
			refusedByLoader++
		}
	}
	if failures > 0 {
		t.Errorf("%d of %d differ", failures, len(file.Vectors))
	}
	if accepted < 400 || refusedByLoader < 1000 {
		t.Errorf("%d zones accepted, %d that Intl took refused", accepted, refusedByLoader)
	}
	for zone, want := range map[string]bool{
		"Europe/Berlin": true, " Europe/Berlin ": true, "UTC": true, "America/Argentina/Buenos_Aires": true,
		"europe/berlin": false, "EUROPE/BERLIN": false, "+01:00": false, "+0100": false, "-05": false,
		"IST": false, "Local": false, "": false,
	} {
		if got := IsValidIANATimezone(zone); got != want {
			t.Errorf("IsValidIANATimezone(%q) = %v, want %v", zone, got, want)
		}
	}
}

func describe(s *string) string {
	if s == nil {
		return "throws"
	}
	return *s
}

// TestHostZoneVectors replays the machine's time zone as Intl reported it
// and what detectOsTimezone made of it, for values of TZ and targets of
// /etc/localtime (hostzone.json).
func TestHostZoneVectors(t *testing.T) {
	var vectors []struct {
		TZ       *string `json:"tz"`
		Link     *string `json:"link"`
		Resolved *string `json:"resolved"`
		Detected string  `json:"detected"`
	}
	loadJSON(t, "hostzone.json", &vectors)
	const zoneinfo = "/usr/share/zoneinfo"
	if _, err := os.Stat(filepath.Join(zoneinfo, "Asia", "Tokyo")); err != nil {
		t.Skip("the vectors need the zoneinfo folder of the machine they were written on")
	}
	tokyo, err := os.ReadFile(filepath.Join(zoneinfo, "Asia", "Tokyo"))
	if err != nil {
		t.Fatal(err)
	}
	fixed, replayed := 0, 0
	for _, v := range vectors {
		// A target this machine's zoneinfo folder lacks cannot be replayed:
		// Ubuntu, for one, keeps right/, posix/ and the old names such as
		// US/Pacific in the tzdata-legacy package
		if v.Link != nil {
			if _, err := os.Stat(*v.Link); err != nil {
				continue
			}
		}
		replayed++
		host := HostFS{
			LookupEnv: func(name string) (string, bool) {
				if name != "TZ" || v.TZ == nil {
					return "", false
				}
				return *v.TZ, true
			},
			Realpath: func(p string) (string, error) {
				if v.Link == nil {
					return p, nil
				}
				return filepath.EvalSymlinks(*v.Link)
			},
			ReadFile: func(string) ([]byte, error) { return tokyo, nil },
			ZoneInfo: os.DirFS(zoneinfo),
		}
		want := ""
		if v.Resolved != nil && *v.Resolved != "Etc/Unknown" {
			want = *v.Resolved
		}
		name := describe(v.TZ) + " with " + describe(v.Link)
		if got := HostZone(host); got != want {
			t.Errorf("TZ %s: HostZone = %q, Node resolved %q", name, got, want)
		}
		s := New(Options{
			HostZone: func() string { return HostZone(host) },
			TZ: func() string {
				if v.TZ == nil {
					return ""
				}
				return *v.TZ
			},
		})
		detected := v.Detected
		if !IsValidIANATimezone(detected) {
			// The server's loader cannot load Node's answer (BUGS CM2): TZ
			// when it can load that, else UTC
			detected = DefaultBillingTimezone
			if v.TZ != nil && IsValidIANATimezone(*v.TZ) {
				detected = jsTrim(*v.TZ)
			}
			fixed++
		}
		if got := s.DetectOSTimezone(); got != detected {
			t.Errorf("TZ %s: DetectOSTimezone = %q, Node %q, want %q", name, got, v.Detected, detected)
		}
	}
	if fixed == 0 || fixed == replayed {
		t.Errorf("%d of %d detected zones the server's loader cannot load", fixed, replayed)
	}
	t.Logf("%d of %d vectors replayed", replayed, len(vectors))
}

// outcome is a value or the message of what was thrown.
type outcome struct {
	Value json.RawMessage `json:"value"`
	Error *string         `json:"error"`
}

// check compares what Go got with what Node gave.
func (o outcome) check(t *testing.T, what string, got any, err error) {
	t.Helper()
	if o.Error != nil {
		if err == nil || err.Error() != *o.Error {
			t.Errorf("%s: error %v, Node threw %q", what, err, *o.Error)
		}
		return
	}
	if err != nil {
		t.Errorf("%s: error %v, Node gave %s", what, err, o.Value)
		return
	}
	if text := mustJSON(t, got); !sameJSON(text, o.Value) {
		t.Errorf("%s:\n got %s\nNode %s", what, text, o.Value)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	text, err := httpx.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return text
}

// sameJSON compares two JSON texts by their bytes after a round trip
// that keeps key order and numbers as written.
func sameJSON(a, b []byte) bool {
	va, errA := goapi.Parse(a)
	vb, errB := goapi.Parse(b)
	if errA != nil || errB != nil {
		return false
	}
	ta, _ := httpx.Marshal(va)
	tb, _ := httpx.Marshal(vb)
	return string(ta) == string(tb)
}

// raw parses a body of the vectors; null is no value.
func raw(t *testing.T, text *string) any {
	t.Helper()
	if text == nil {
		return Undefined
	}
	v, err := goapi.Parse([]byte(*text))
	if err != nil {
		t.Fatalf("parse %q: %v", *text, err)
	}
	return v
}

type serviceVectors struct {
	Linux, Windows platformVectors
}

type platformVectors struct {
	Defaults  json.RawMessage `json:"defaults"`
	Common    []string        `json:"common"`
	Detected  string          `json:"detected"`
	Normalize []struct {
		Raw        *string `json:"raw"`
		Normalized outcome `json:"normalized"`
		Validated  outcome `json:"validated"`
	} `json:"normalize"`
	NTP      [][2]any `json:"ntp"`
	Zones    [][2]any `json:"zones"`
	Flags    [][3]any `json:"flags"`
	EnvFiles []struct {
		Content  string            `json:"content"`
		Parsed   map[string]string `json:"parsed"`
		Settings outcome           `json:"settings"`
	} `json:"envFiles"`
	Writes []struct {
		Before *string `json:"before"`
		Raw    string  `json:"raw"`
		Result outcome `json:"result"`
		After  *string `json:"after"`
		Mode   *int    `json:"mode"`
	} `json:"writes"`
	Saves []struct {
		Docker bool         `json:"docker"`
		Raw    string       `json:"raw"`
		Put    *apiAnswer   `json:"put"`
		Result outcome      `json:"result"`
		Calls  []vectorCall `json:"calls"`
		After  string       `json:"after"`
	} `json:"saves"`
	Reads []struct {
		Content *string      `json:"content"`
		Remote  *apiAnswer   `json:"remote"`
		Result  outcome      `json:"result"`
		Calls   []vectorCall `json:"calls"`
		Layout  *string      `json:"layout"`
		Docker  bool         `json:"docker"`
		Split   *bool        `json:"split"`
	} `json:"reads"`
}

// apiAnswer is what the API stub said: data with status 200, or a
// failure, with a status (an answer of the server) or without (no
// answer).
type apiAnswer struct {
	Data   json.RawMessage `json:"data"`
	Throws *struct {
		Thrown string          `json:"thrown"`
		Status int             `json:"status"`
		Data   json.RawMessage `json:"data"`
	} `json:"throws"`
}

type vectorCall struct {
	Method string  `json:"method"`
	Path   string  `json:"path"`
	Body   *string `json:"body,omitempty"`
}

// answered reports whether the answer can be given in process: a call
// without an answer (a refused connection, a timeout) cannot.
func (a *apiAnswer) answered() bool { return a == nil || a.Throws == nil || a.Throws.Status != 0 }

// apiStub is an API server that answers every call with one answer and
// remembers the calls.
type apiStub struct {
	answer *apiAnswer
	calls  []vectorCall
}

func (s *apiStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	call := vectorCall{Method: r.Method, Path: strings.TrimPrefix(r.URL.RequestURI(), "/api")}
	if r.Method == http.MethodPut {
		body, _ := io.ReadAll(r.Body)
		text := string(body)
		call.Body = &text
	}
	s.calls = append(s.calls, call)
	status, data := http.StatusOK, s.answer.Data
	if s.answer.Throws != nil {
		status, data = s.answer.Throws.Status, s.answer.Throws.Data
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if len(data) > 0 {
		_, _ = w.Write(data)
	}
}

func newVectorService(t *testing.T, linux bool, envPath string, api http.Handler, docker bool) *Service {
	t.Helper()
	return New(Options{
		EnvPath: envPath,
		Docker:  docker,
		Getenv: func(name string) string {
			if name == "BETTERDESK_DOCKER_LAYOUT" {
				return "split"
			}
			return ""
		},
		Linux: linux,
		GoAPI: goapi.New(api),
		ServiceConfig: func(context.Context) any {
			return json.RawMessage(`{"changed":false,"changes":["stub"],"error":null,"needsRestart":false}`)
		},
		HostZone: func() string { return "UTC" },
		TZ:       func() string { return "" },
	})
}

// skewFix turns Node's outcomes for a body whose max_skew_ms is a JSON
// number into what the service gives since CM6: the number is taken as
// it is, where Node read it with parseInt (2500.9 was 2500, 0.5 the
// default 2000, 1e21 was 1), and a save refuses one that is no whole
// number from 1 to 600000. A refusal of the servers comes first, as
// before. It reports whether the body is one CM6 changes.
func skewFix(t *testing.T, body any, normalized, validated *outcome) bool {
	t.Helper()
	obj, ok := body.(*goapi.Object)
	if !ok || !obj.Has("max_skew_ms") {
		return false
	}
	n, ok := obj.Get("max_skew_ms").(float64)
	if !ok || (n == math.Trunc(n) && n >= 1 && n <= 600000) {
		return false
	}
	if normalized.Error == nil {
		parsed, err := goapi.Parse(normalized.Value)
		if err != nil {
			t.Fatal(err)
		}
		settings := parsed.(*goapi.Object)
		settings.Set("max_skew_ms", n)
		normalized.Value = mustJSON(t, settings)
	}
	if validated.Error == nil {
		message := ErrInvalidMaxSkew.Error()
		validated.Value, validated.Error = nil, &message
	}
	return true
}

// zoneFix turns Node's outcome of a check for a body whose billing zone
// the server's loader cannot load as written (other letter case, an
// offset) into what the service gives since CM2: the check refuses the
// zone. It reports whether the body is one CM2 changes.
func zoneFix(body any, validated *outcome) bool {
	if validated.Error != nil || !refusedZone(body) {
		return false
	}
	message := ErrInvalidBillingTimezone.Error()
	validated.Value, validated.Error = nil, &message
	return true
}

// refusedZone reports whether a body names a billing zone the server's
// loader cannot load as written.
func refusedZone(body any) bool {
	obj, ok := body.(*goapi.Object)
	if !ok {
		return false
	}
	zone, ok := obj.Get("billing_timezone").(string)
	return ok && jsTrim(zone) != "" && !loadsAsWritten(zone)
}

// loadsAsWritten reports whether the server's loader loads a zone, trimmed,
// as it is written.
func loadsAsWritten(zone string) bool {
	name := jsTrim(zone)
	_, err := time.LoadLocation(name)
	return err == nil && name != "Local"
}

// TestServiceVectors replays services/billingClockConfigService.js:
// normalizeSettings and validateSettings on Linux and on Windows, the
// checks of a server and a time zone, the flags and the reading of the
// .env file, writes into the file, saves and reads outside and inside
// the Docker layout with two containers (service.json).
func TestServiceVectors(t *testing.T) {
	var vectors serviceVectors
	loadJSON(t, "service.json", &vectors)
	for _, p := range []struct {
		name  string
		linux bool
		v     platformVectors
	}{{"linux", true, vectors.Linux}, {"windows", false, vectors.Windows}} {
		s := newVectorService(t, p.linux, filepath.Join(t.TempDir(), ".env"), http.NotFoundHandler(), false)
		if got := strings.Join(CommonTimezones(), ","); got != strings.Join(p.v.Common, ",") {
			t.Errorf("%s: common zones %s, Node %s", p.name, got, strings.Join(p.v.Common, ","))
		}
		defaults, err := s.NormalizeSettings(Undefined)
		if err != nil || !sameJSON(mustJSON(t, defaults), p.v.Defaults) {
			t.Errorf("%s: defaults %+v (%v), Node %s", p.name, defaults, err, p.v.Defaults)
		}
		if len(p.v.Normalize) < 90 {
			t.Fatalf("%s: only %d bodies", p.name, len(p.v.Normalize))
		}
		skews, zones := 0, 0
		for _, v := range p.v.Normalize {
			body := raw(t, v.Raw)
			if skewFix(t, body, &v.Normalized, &v.Validated) {
				skews++
			}
			if zoneFix(body, &v.Validated) {
				zones++
			}
			got, err := s.NormalizeSettings(body)
			v.Normalized.check(t, p.name+" normalize "+describe(v.Raw), got, err)
			got, err = s.ValidateSettings(body)
			v.Validated.check(t, p.name+" validate "+describe(v.Raw), got, err)
		}
		if skews < 6 {
			t.Errorf("%s: only %d bodies whose skew is a number that is no whole number from 1 to 600000 (CM6)", p.name, skews)
		}
		if zones < 3 {
			t.Errorf("%s: only %d bodies whose zone the server's loader cannot load (CM2)", p.name, zones)
		}
	}

	v := vectors.Linux
	for _, c := range v.NTP {
		if got := IsValidNTPServerEntry(c[0].(string)); got != c[1].(bool) {
			t.Errorf("IsValidNTPServerEntry(%q) = %v, Node %v", c[0], got, c[1])
		}
	}
	for _, c := range v.Zones {
		// A zone the server's loader cannot load is refused (CM2)
		want := c[1].(bool) && loadsAsWritten(c[0].(string))
		if got := IsValidIANATimezone(c[0].(string)); got != want {
			t.Errorf("IsValidIANATimezone(%q) = %v, Node %v, want %v", c[0], got, c[1], want)
		}
	}
	for _, c := range v.Flags {
		value, ok := c[0].(string)
		if got := EnvToBool(value, ok, c[1].(bool)); got != c[2].(bool) {
			t.Errorf("EnvToBool(%q, %v, fallback %v) = %v, Node %v", c[0], ok, c[1], got, c[2])
		}
	}
	s := newVectorService(t, true, filepath.Join(t.TempDir(), ".env"), http.NotFoundHandler(), false)
	for _, f := range v.EnvFiles {
		parsed := ParseEnvFile(f.Content)
		if string(mustJSON(t, parsed)) != string(mustJSON(t, f.Parsed)) {
			t.Errorf("ParseEnvFile(%q) = %v, Node %v", f.Content, parsed, f.Parsed)
		}
		got, err := s.SettingsFromEnvContent(f.Content)
		f.Settings.check(t, "settings of "+f.Content, got, err)
	}
}

// TestRollbackPutsBackWhatTheSaveChecksRefuse: a .env file holds values
// an older console took or someone wrote by hand, which a save refuses
// today. A save over them and its rollback put the old values back
// (BUGS.md RV95).
func TestRollbackPutsBackWhatTheSaveChecksRefuse(t *testing.T) {
	for _, old := range []string{
		"BILLING_TIMEZONE=europe/berlin\n",
		"BILLING_TIMEZONE=+01:00\n",
		"BILLING_MAX_CLOCK_SKEW_MS=900000\n",
		"NTP_SERVERS=ntp_1.example,pool.ntp.org\n",
	} {
		path := filepath.Join(t.TempDir(), ".env")
		if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
			t.Fatal(err)
		}
		s := newVectorService(t, true, path, http.NotFoundHandler(), false)
		before, err := s.GetClockSettings()
		if err != nil {
			t.Fatalf("%q: read: %v", old, err)
		}
		if _, err := s.SaveClockSettings(context.Background(), goapi.NewObject("billing_timezone", "Europe/Paris")); err != nil {
			t.Fatalf("%q: save: %v", old, err)
		}
		if err := s.Rollback(context.Background(), goapi.NewObject("type", "billing-clock", "settings", before.Object())); err != nil {
			t.Errorf("%q: rollback: %v", old, err)
			continue
		}
		if after, err := s.GetClockSettings(); err != nil || after != before {
			t.Errorf("%q: after the rollback %+v (%v), before %+v", old, after, err, before)
		}
	}
	// What cannot be read as settings at all still fails the rollback
	s := newVectorService(t, true, filepath.Join(t.TempDir(), ".env"), http.NotFoundHandler(), false)
	if err := s.Rollback(context.Background(), goapi.NewObject("type", "billing-clock", "settings", nil)); err == nil {
		t.Error("a rollback without settings passed")
	}
}

// TestWriteVectors replays the checked writes into the .env file: the
// file before, the settings, and the file after with its mode.
func TestWriteVectors(t *testing.T) {
	var vectors serviceVectors
	loadJSON(t, "service.json", &vectors)
	if len(vectors.Linux.Writes) < 50 {
		t.Fatalf("only %d writes", len(vectors.Linux.Writes))
	}
	for i, w := range vectors.Linux.Writes {
		path := filepath.Join(t.TempDir(), ".env")
		if w.Before != nil {
			if err := os.WriteFile(path, []byte(*w.Before), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		s := newVectorService(t, true, path, http.NotFoundHandler(), false)
		body := w.Raw
		parsed := raw(t, &body)
		if zoneFix(parsed, &w.Result) {
			// The refused zone leaves the file as it was (CM2)
			w.After, w.Mode = w.Before, nil
		}
		got, err := s.ValidateSettings(parsed)
		if err == nil {
			err = s.writeEnv(got)
		}
		if err != nil {
			got = Settings{}
		}
		w.Result.check(t, "write "+w.Raw, got, err)
		after, readErr := os.ReadFile(path)
		switch {
		case w.After == nil && readErr == nil:
			t.Errorf("write %d (%s): the file was created, Node created none", i, w.Raw)
		case w.After != nil && string(after) != *w.After:
			t.Errorf("write %d (%s):\n got %q\nNode %q", i, w.Raw, after, *w.After)
		}
		if w.Before == nil && w.Mode != nil && readErr == nil {
			info, _ := os.Stat(path)
			if mode := int(info.Mode().Perm()); mode != *w.Mode && os.Getenv("GOOS") != "windows" {
				t.Errorf("write %d: a new file has mode %o, Node %o", i, mode, *w.Mode)
			}
		}
	}
}

// TestSaveAndReadVectors replays saves and reads outside and inside the
// Docker layout with two containers, with the calls to the API server.
// Answers without a server answer (a refused connection, a timeout)
// cannot happen in process and are left out.
func TestSaveAndReadVectors(t *testing.T) {
	var vectors serviceVectors
	loadJSON(t, "service.json", &vectors)
	replayed, zones := 0, 0
	for _, sv := range vectors.Linux.Saves {
		if !sv.Put.answered() {
			continue
		}
		replayed++
		path := filepath.Join(t.TempDir(), ".env")
		if err := os.WriteFile(path, []byte("PORT=5000\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		stub := &apiStub{answer: sv.Put}
		s := newVectorService(t, true, path, stub, sv.Docker)
		body := sv.Raw
		parsed := raw(t, &body)
		if (sv.Result.Error == nil || len(sv.Calls) > 0) && refusedZone(parsed) {
			// Node's checks took the settings; the zone the server's
			// loader cannot load is refused before any call or write (CM2)
			message := ErrInvalidBillingTimezone.Error()
			sv.Result, sv.Calls, sv.After = outcome{Error: &message}, nil, "PORT=5000\n"
			zones++
		}
		result, err := s.SaveClockSettings(context.Background(), parsed)
		var got any
		if err == nil {
			if sv.Docker {
				got = map[string]any{"settings": result.Settings, "serviceConfig": nil, "status": result.Status, "restart": nil, "dockerMode": true}
			} else {
				got = map[string]any{"settings": result.Settings, "serviceConfig": result.ServiceConfig, "restart": nil}
			}
		}
		what := "save " + sv.Raw
		if sv.Docker {
			what += " in Docker with " + string(mustJSON(t, sv.Put))
		}
		sv.Result.check(t, what, sortedKeys(t, got), err)
		checkCalls(t, what, stub.calls, sv.Calls)
		if after, _ := os.ReadFile(path); string(after) != sv.After {
			t.Errorf("%s: file\n got %q\nNode %q", what, after, sv.After)
		}
	}
	for _, rv := range vectors.Linux.Reads {
		if rv.Split != nil {
			layout := *rv.Layout
			s := New(Options{Docker: rv.Docker, Getenv: func(string) string { return layout }})
			if got := s.IsDockerSplitDeployment(); got != *rv.Split {
				t.Errorf("layout %q, Docker %v: split %v, Node %v", layout, rv.Docker, got, *rv.Split)
			}
			continue
		}
		if !rv.Remote.answered() {
			continue
		}
		replayed++
		path := filepath.Join(t.TempDir(), ".env")
		if rv.Content != nil {
			if err := os.WriteFile(path, []byte(*rv.Content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		stub := &apiStub{answer: rv.Remote}
		s := newVectorService(t, true, path, stub, true)
		got, err := s.GetRuntimeClockSettings(context.Background())
		what := "read with " + string(mustJSON(t, rv.Remote))
		rv.Result.check(t, what, got, err)
		checkCalls(t, what, stub.calls, rv.Calls)
	}
	if replayed < 100 {
		t.Errorf("only %d saves and reads replayed", replayed)
	}
	if zones < 3 {
		t.Errorf("only %d saves of a zone the server's loader cannot load (CM2)", zones)
	}
}

// sortedKeys is a map in the key order of the answer Node gave, so the
// comparison does not depend on how Go orders a map.
func sortedKeys(t *testing.T, v any) any {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	o := goapi.NewObject()
	for _, key := range []string{"settings", "serviceConfig", "status", "restart", "dockerMode"} {
		if value, has := m[key]; has {
			parsed, err := goapi.Parse(mustJSON(t, value))
			if err != nil {
				t.Fatal(err)
			}
			o.Set(key, parsed)
		}
	}
	return o
}

func checkCalls(t *testing.T, what string, got, want []vectorCall) {
	t.Helper()
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if string(mustJSON(t, got)) != string(mustJSON(t, want)) {
		t.Errorf("%s: calls\n got %s\nNode %s", what, mustJSON(t, got), mustJSON(t, want))
	}
}

// TestHostZoneShape pins ICU's rule for a TZ value that names a zone: at
// most two digits, and only at the end. Three digits make it a rule of
// the C library, and /etc/localtime decides.
func TestHostZoneShape(t *testing.T) {
	link := func(string) (string, error) { return "/usr/share/zoneinfo/Asia/Tokyo", nil }
	for tz, want := range map[string]string{
		"Etc/GMT+12":  "Etc/GMT+12",
		"Etc/GMT+120": "Asia/Tokyo",
		"UTC1":        "",
		"UTC123":      "Asia/Tokyo",
		"EST5EDT":     "EST5EDT",
		"EST5EDT1":    "Asia/Tokyo",
	} {
		got := HostZone(HostFS{LookupEnv: func(string) (string, bool) { return tz, true }, Realpath: link})
		if got != want {
			t.Errorf("TZ %q: %q, want %q", tz, got, want)
		}
	}
}
