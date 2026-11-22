package webui

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// siteTime is the modification time every file of a test build gets, so
// tags and dates are the same on every machine. The fraction checks the
// rounding to milliseconds.
var siteTime = time.Date(2026, 1, 2, 3, 4, 5, 678_000_000, time.UTC)

const (
	siteETagStart = `W/"81-19b7ca98f2e"` // _ui/immutable/entry/start.t3st.js
	siteModified  = "Fri, 02 Jan 2026 03:04:05 GMT"
)

// copySite copies testdata/site into a new directory as a build for the
// given base.
func copySite(t testing.TB, base string) string {
	t.Helper()
	dir := t.TempDir()
	err := filepath.WalkDir("testdata/site", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel("testdata/site", name)
		target := filepath.Join(dir, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		if rel == "index.html" && base != "" {
			html := strings.Replace(string(raw), `base: ""`, `base: "`+base+`"`, 1)
			raw = []byte(strings.Replace(html, `assets: ""`, `assets: "`+base+`"`, 1))
		}
		return os.WriteFile(target, raw, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	touchAll(t, dir, siteTime)
	return dir
}

func touchAll(t testing.TB, dir string, at time.Time) {
	t.Helper()
	err := filepath.WalkDir(dir, func(name string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		return os.Chtimes(name, at, at)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func readSite(t testing.TB, dir, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// sessionFromHeader reads who is signed in from the X-Test-User header of a
// test request: a role, or "norole" for a session without one.
func sessionFromHeader(r *http.Request) (bool, string) {
	switch user := r.Header.Get("X-Test-User"); user {
	case "":
		return false, ""
	case "norole":
		return true, ""
	default:
		return true, user
	}
}

// latin1Bytes writes a string of characters up to U+00FF as one byte each,
// the way they travel in a request line.
func latin1Bytes(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r > 0xFF {
			b.WriteString(string(r))
			continue
		}
		b.WriteByte(byte(r))
	}
	return b.String()
}

// rawRequest builds a server request for any request target, also ones the
// standard library would not parse.
func rawRequest(method, target string, headers map[string]string) *http.Request {
	target = latin1Bytes(target)
	r := &http.Request{
		Method:     method,
		RequestURI: target,
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     http.Header{},
		Host:       "console.test",
		RemoteAddr: "192.0.2.7:40000",
		Body:       http.NoBody,
	}
	if u, err := url.ParseRequestURI(target); err == nil {
		r.URL = u
	} else {
		r.URL = &url.URL{Path: "/"}
	}
	for name, value := range headers {
		r.Header.Set(name, latin1Bytes(value))
	}
	return r
}

type answer struct {
	status int
	header http.Header
	body   string
	// handled is what Handle answered; false means NotFound wrote the answer
	handled bool
}

func (a answer) get(name string) string { return strings.Join(a.header.Values(name), ", ") }

// serve sends a request through Handle and, when it declines, NotFound.
func serve(h *Host, method, target string, headers map[string]string) answer {
	r := rawRequest(method, target, headers)
	rec := httptest.NewRecorder()
	handled := h.Handle(rec, r)
	if !handled {
		h.NotFound(rec, r)
	}
	res := rec.Result()
	body, _ := io.ReadAll(res.Body)
	return answer{status: res.StatusCode, header: res.Header, body: string(body), handled: handled}
}

func record(fn func(w http.ResponseWriter)) answer {
	rec := httptest.NewRecorder()
	fn(rec)
	res := rec.Result()
	body, _ := io.ReadAll(res.Body)
	return answer{status: res.StatusCode, header: res.Header, body: string(body), handled: true}
}

var admin = map[string]string{"X-Test-User": "admin"}

func with(headers map[string]string, more ...string) map[string]string {
	out := map[string]string{}
	for k, v := range headers {
		out[k] = v
	}
	for i := 0; i+1 < len(more); i += 2 {
		out[more[i]] = more[i+1]
	}
	return out
}

// testHost is a Host for a copy of the test build at the given base, in
// production mode.
func testHost(t testing.TB, base string) (*Host, string) {
	t.Helper()
	dir := copySite(t, base)
	return New(Options{Dir: dir, Production: true, Session: sessionFromHeader}), dir
}

// bases runs a test for a build at the root and for one under /app2.
func bases(t *testing.T, fn func(t *testing.T, h *Host, base, dir string)) {
	t.Helper()
	for _, base := range []string{"", "/app2"} {
		name := "root"
		if base != "" {
			name = base[1:]
		}
		t.Run(name, func(t *testing.T) {
			h, dir := testHost(t, base)
			if h.Base() != base {
				t.Fatalf("the host serves at %q, the build is for %q", h.Base(), base)
			}
			fn(t, h, base, dir)
		})
	}
}

// fileAccess is one use of the file system by a Host: "stat", "open" or
// "read" and the path.
type fileAccess struct{ op, name string }

// watchedFiles records every path a Host touches.
type watchedFiles struct {
	mu   sync.Mutex
	seen []fileAccess
}

func (f *watchedFiles) note(op, name string) {
	f.mu.Lock()
	f.seen = append(f.seen, fileAccess{op, name})
	f.mu.Unlock()
}

func (f *watchedFiles) Stat(name string) (fs.FileInfo, error) {
	f.note("stat", name)
	return os.Stat(name)
}

func (f *watchedFiles) Open(name string) (io.ReadSeekCloser, error) {
	f.note("open", name)
	return os.Open(name)
}

func (f *watchedFiles) ReadFile(name string) ([]byte, error) {
	f.note("read", name)
	return os.ReadFile(name)
}

// take answers what was touched since the last call.
func (f *watchedFiles) take() []fileAccess {
	f.mu.Lock()
	defer f.mu.Unlock()
	seen := f.seen
	f.seen = nil
	return seen
}

func writeFile(t testing.TB, name, content string) {
	t.Helper()
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
