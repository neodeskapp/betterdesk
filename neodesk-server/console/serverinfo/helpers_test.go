package serverinfo

import (
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
)

// The vectors in testdata were recorded from the Node console's own
// modules on Node 22 (testdata/README.md). Each holds an input and what
// the Node function answered for it.

// loadVectors reads testdata/<name>.json.gz into v.
func loadVectors(t testing.TB, name string, v any) {
	t.Helper()
	file, err := os.Open(filepath.Join("testdata", name+".json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	unzipped, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(unzipped)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

// atLeast fails the test when a vector list is shorter than expected: a
// file that was cut or recorded empty must not pass.
func atLeast(t testing.TB, name string, got, want int) {
	t.Helper()
	if got < want {
		t.Fatalf("%s: %d vectors, want at least %d", name, got, want)
	}
}

// mismatches stops a test after a number of reported differences.
type mismatches struct {
	t     testing.TB
	count int
}

func (m *mismatches) errorf(format string, args ...any) {
	m.t.Helper()
	m.count++
	if m.count <= 15 {
		m.t.Errorf(format, args...)
	}
	if m.count == 16 {
		m.t.Fatalf("more differences follow")
	}
}

// jsonText is v as JSON without HTML escaping, the way the console writes
// its answers.
func jsonText(t testing.TB, v any) string {
	t.Helper()
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// sameJSON reports whether got encodes to the JSON value want holds,
// whatever the order of object keys.
func sameJSON(t testing.TB, got any, want json.RawMessage) bool {
	t.Helper()
	var a, b any
	if err := json.Unmarshal([]byte(jsonText(t, got)), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &b); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(a, b)
}

// fileState is a file of a recorded case: nil for a missing one, a
// directory, or content.
type fileState struct {
	Dir   bool    `json:"dir"`
	Bytes *string `json:"bytes"`
	Mode  *int    `json:"mode"`
}

func decodeBase64(t testing.TB, s string) []byte {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// textOrDir is a file a script laid out: no field or JSON null for none, a
// string for its text, {"dir":true} for a directory.
type textOrDir struct {
	Exists bool
	Dir    bool
	Text   string
}

func (f *textOrDir) UnmarshalJSON(data []byte) error {
	*f = textOrDir{}
	switch {
	case string(data) == "null":
		return nil
	case len(data) > 0 && data[0] == '"':
		f.Exists = true
		return json.Unmarshal(data, &f.Text)
	}
	f.Exists, f.Dir = true, true
	return nil
}

// put lays the file out at path. A file gets the mode the script gave it.
func (f textOrDir) put(t testing.TB, path string, mode fs.FileMode) {
	t.Helper()
	switch {
	case !f.Exists:
	case f.Dir:
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	default:
		if err := os.WriteFile(path, []byte(f.Text), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
}

// checkFile compares a file on disk with the state a script recorded.
func checkFile(t testing.TB, m *mismatches, label, path string, want *fileState) {
	t.Helper()
	info, err := os.Stat(path)
	switch {
	case want == nil:
		if err == nil {
			m.errorf("%s: %s exists, Node left none", label, filepath.Base(path))
		}
		return
	case err != nil:
		m.errorf("%s: %s is missing: %v", label, filepath.Base(path), err)
		return
	case want.Dir:
		if !info.IsDir() {
			m.errorf("%s: %s is a file, Node has a directory", label, filepath.Base(path))
		}
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		m.errorf("%s: %v", label, err)
		return
	}
	if wantBytes := decodeBase64(t, *want.Bytes); string(raw) != string(wantBytes) {
		m.errorf("%s: %s holds\n%q\nNode wrote\n%q", label, filepath.Base(path), raw, wantBytes)
	}
	if want.Mode != nil && int(info.Mode().Perm()) != *want.Mode {
		m.errorf("%s: %s has mode %o, Node left %o", label, filepath.Base(path), info.Mode().Perm(), *want.Mode)
	}
}

// listDir returns the sorted names in dir, or nil when it does not exist.
func listDir(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	names := []string{}
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// recordingLogger keeps what the service logs.
type recordingLogger struct {
	mu    sync.Mutex
	lines []string
}

func (l *recordingLogger) Warn(args ...any) { l.add("warn", args) }

func (l *recordingLogger) Error(args ...any) { l.add("error", args) }

func (l *recordingLogger) add(level string, args []any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lines = append(l.lines, level+": "+strings.TrimSuffix(fmt.Sprintln(args...), "\n"))
}

func (l *recordingLogger) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.lines...)
}

// apiAnswer is what a script let the server's API answer: data, or a
// failure with a status.
type apiAnswer struct {
	Data   json.RawMessage `json:"data"`
	Fail   bool            `json:"fail"`
	Status int             `json:"status"`
}

// apiCall is one request the stub API saw.
type apiCall struct {
	Method string
	Path   string
	Body   string
}

// stubAPI stands in for the server's API. It answers every request with
// the answer set for its method.
type stubAPI struct {
	mu      sync.Mutex
	answers map[string]*apiAnswer
	calls   []apiCall
}

func (s *stubAPI) set(method string, a *apiAnswer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.answers == nil {
		s.answers = map[string]*apiAnswer{}
	}
	s.answers[method] = a
	s.calls = nil
}

func (s *stubAPI) seen() []apiCall {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]apiCall(nil), s.calls...)
}

func (s *stubAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	s.mu.Lock()
	s.calls = append(s.calls, apiCall{Method: r.Method, Path: r.URL.Path, Body: string(body)})
	a := s.answers[r.Method]
	s.mu.Unlock()
	switch {
	case a == nil:
		http.Error(w, `{"error":"no answer"}`, http.StatusInternalServerError)
	case a.Fail:
		http.Error(w, `{"error":"failed"}`, a.Status)
	default:
		w.Header().Set("Content-Type", "application/json")
		// The script's undefined data is an empty body; its "" is one too.
		if len(a.Data) > 0 && string(a.Data) != `""` {
			_, _ = w.Write(a.Data)
		}
	}
}

func (s *stubAPI) client() *goapi.Client {
	return goapi.New(s)
}

// envMap is a process environment for a Service.
func envMap(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}
