package httpx

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The vectors in testdata/static/vectors.json were recorded from
// express.static (serve-static 1.16.3, send 0.19.2) by
// .ui-rework/go/tools/vectors/static.js.
type staticVector struct {
	ID      string      `json:"id"`
	Method  string      `json:"method"`
	Path    string      `json:"path"`
	Headers [][2]string `json:"headers"`
	Answer  struct {
		Status  int         `json:"status"`
		Headers [][2]string `json:"headers"`
		Body    string      `json:"body"`
	} `json:"answer"`
	Error *struct {
		Status int `json:"status"`
	} `json:"error"`
}

type staticFixture struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	MtimeNs string `json:"mtimeNs"`
}

type staticVectorFile struct {
	Fixtures []staticFixture `json:"fixtures"`
	Configs  []struct {
		Mount   string         `json:"mount"`
		Root    string         `json:"root"`
		Options map[string]any `json:"options"`
	} `json:"configs"`
	Mime    map[string]string `json:"mime"`
	Vectors []staticVector    `json:"vectors"`
}

func loadStaticVectors(t testing.TB) staticVectorFile {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "static", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file staticVectorFile
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	return file
}

// writeFixtures puts the recorded files into a fresh folder with the
// recorded modification times.
func writeFixtures(t testing.TB, fixtures []staticFixture) string {
	t.Helper()
	root := t.TempDir()
	for _, f := range fixtures {
		full := filepath.Join(root, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		content, err := base64.StdEncoding.DecodeString(f.Content)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			t.Fatal(err)
		}
		ns, err := strconv.ParseInt(f.MtimeNs, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		mtime := time.Unix(0, ns)
		if err := os.Chtimes(full, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func maxAgeOf(t testing.TB, v any) time.Duration {
	switch value := v.(type) {
	case nil:
		return 0
	case float64:
		return time.Duration(value) * time.Millisecond
	case string:
		units := map[byte]time.Duration{'d': 24 * time.Hour, 'h': time.Hour}
		if unit, ok := units[value[len(value)-1]]; ok {
			n, _ := strconv.Atoi(value[:len(value)-1])
			return time.Duration(n) * unit
		}
		n, _ := strconv.Atoi(value)
		return time.Duration(n) * time.Millisecond
	}
	t.Fatalf("max age %v", v)
	return 0
}

// expressSend answers the way Express's res.type('text').send(body) does,
// which is what the recording app's last handlers use.
func expressSend(w http.ResponseWriter, r *http.Request, status int, body string) {
	h := w.Header()
	h.Set("Content-Type", "text/plain; charset=utf-8")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	if h.Get("ETag") == "" {
		h.Set("ETag", ETag([]byte(body)))
	}
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, body)
	}
}

// rawRequest builds a request whose target is exactly what a client sent,
// also when net/http would refuse to parse it.
func rawRequest(method, target string, headers [][2]string) *http.Request {
	r := &http.Request{Method: method, RequestURI: target, Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: http.Header{}, Host: "vectors.test", RemoteAddr: "127.0.0.1:40000", Body: http.NoBody}
	if u, err := url.ParseRequestURI(target); err == nil {
		r.URL = u
	} else {
		r.URL = &url.URL{Path: "/"}
	}
	for _, h := range headers {
		r.Header.Add(h[0], h[1])
	}
	return r
}

func staticHandler(t testing.TB, file staticVectorFile, root string, errorsSeen *sync.Map) http.Handler {
	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expressSend(w, r, http.StatusNotFound, "NEXT "+r.Method+" "+r.RequestURI)
	})
	for i := len(file.Configs) - 1; i >= 0; i-- {
		c := file.Configs[i]
		opts := StaticOptions{
			MaxAge:         maxAgeOf(t, c.Options["maxAge"]),
			Immutable:      c.Options["immutable"] == true,
			NoRedirect:     c.Options["redirect"] == false,
			NoFallthrough:  c.Options["fallthrough"] == false,
			NoETag:         c.Options["etag"] == false,
			NoLastModified: c.Options["lastModified"] == false,
			NoAcceptRanges: c.Options["acceptRanges"] == false,
			NoCacheControl: c.Options["cacheControl"] == false,
			OnError: func(w http.ResponseWriter, r *http.Request, status int, err error) {
				if err == nil {
					t.Errorf("%s: OnError without an error", r.RequestURI)
				}
				errorsSeen.Store(r.Header.Get("X-Vec-Id"), status)
				expressSend(w, r, status, "ERROR "+strconv.Itoa(status))
			},
		}
		if dot, ok := c.Options["dotfiles"].(string); ok {
			opts.Dotfiles = dot
		}
		switch index := c.Options["index"].(type) {
		case bool:
			opts.NoIndex = !index
		case []any:
			for _, name := range index {
				opts.Index = append(opts.Index, name.(string))
			}
		}
		switch c.Options["setHeaders"] {
		case "version":
			opts.SetHeaders = func(w http.ResponseWriter, filePath string, _ fs.FileInfo) {
				if strings.HasSuffix(filePath, "version.json") {
					w.Header().Set("Cache-Control", "no-cache")
				}
			}
		case "preset":
			opts.SetHeaders = func(w http.ResponseWriter, _ string, _ fs.FileInfo) {
				w.Header().Set("Content-Type", "text/x-preset")
				w.Header().Set("ETag", `"preset"`)
				w.Header().Set("Last-Modified", "Mon, 01 Jan 2001 00:00:00 GMT")
				w.Header().Set("Accept-Ranges", "none")
			}
		}
		dir := root
		if c.Root != "" {
			dir = filepath.Join(root, c.Root)
		}
		handler = NewStatic(dir, opts).Middleware(c.Mount)(handler)
	}
	return handler
}

// comparableHeaders are the answer's headers without the ones the HTTP
// server itself adds.
func comparableHeaders(pairs [][2]string) map[string]string {
	out := map[string]string{}
	for _, h := range pairs {
		name := strings.ToLower(h[0])
		switch name {
		case "date", "connection", "keep-alive", "x-powered-by", "transfer-encoding":
			continue
		}
		if prev, ok := out[name]; ok {
			out[name] = prev + ", " + h[1]
		} else {
			out[name] = h[1]
		}
	}
	return out
}

func recordedHeaders(h http.Header) map[string]string {
	var pairs [][2]string
	for name, values := range h {
		for _, v := range values {
			pairs = append(pairs, [2]string{name, v})
		}
	}
	return comparableHeaders(pairs)
}

func diffHeaders(want, got map[string]string) string {
	var lines []string
	for name, w := range want {
		if g, ok := got[name]; !ok {
			lines = append(lines, fmt.Sprintf("missing %s: %s", name, w))
		} else if g != w {
			lines = append(lines, fmt.Sprintf("%s: got %q want %q", name, g, w))
		}
	}
	for name, g := range got {
		if _, ok := want[name]; !ok {
			lines = append(lines, fmt.Sprintf("extra %s: %s", name, g))
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "; ")
}

func TestStaticAgainstExpress(t *testing.T) {
	file := loadStaticVectors(t)
	if len(file.Vectors) < 1000 {
		t.Fatalf("only %d vectors", len(file.Vectors))
	}
	root := writeFixtures(t, file.Fixtures)
	var errorsSeen sync.Map
	handler := staticHandler(t, file, root, &errorsSeen)
	for _, v := range file.Vectors {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, rawRequest(v.Method, v.Path, v.Headers))
		name := v.ID + " " + v.Method + " " + v.Path + " " + fmt.Sprint(v.Headers[:len(v.Headers)-1])
		if rec.Code != v.Answer.Status {
			t.Errorf("%s: status %d, want %d", name, rec.Code, v.Answer.Status)
			continue
		}
		wantBody, _ := base64.StdEncoding.DecodeString(v.Answer.Body)
		if got := rec.Body.Bytes(); string(got) != string(wantBody) {
			t.Errorf("%s: body %q, want %q", name, got, wantBody)
		}
		if d := diffHeaders(comparableHeaders(v.Answer.Headers), recordedHeaders(rec.Header())); d != "" {
			t.Errorf("%s: headers: %s", name, d)
		}
		got, called := errorsSeen.Load(v.ID)
		switch {
		case v.Error == nil && called:
			t.Errorf("%s: OnError called with %v, Express had no error", name, got)
		case v.Error != nil && !called:
			t.Errorf("%s: OnError not called, Express passed on a %d", name, v.Error.Status)
		case v.Error != nil && got != v.Error.Status:
			t.Errorf("%s: OnError status %v, want %d", name, got, v.Error.Status)
		}
	}
}

func TestStaticContentTypes(t *testing.T) {
	file := loadStaticVectors(t)
	if len(file.Mime) < 100 {
		t.Fatalf("only %d extensions recorded", len(file.Mime))
	}
	for ext, want := range file.Mime {
		if got := StaticContentType("/srv/file." + ext); got != want {
			t.Errorf(".%s: %q, want %q", ext, got, want)
		}
		if got := StaticContentType("FILE." + strings.ToUpper(ext)); got != want {
			t.Errorf("upper case .%s: %q, want %q", ext, got, want)
		}
	}
	// The name after the last slash counts when there is no dot
	if got := StaticContentType("/srv/json"); got != "application/json; charset=UTF-8" {
		t.Errorf("a file named json: %q", got)
	}
	if got := StaticContentType("/srv/README"); got != "application/octet-stream" {
		t.Errorf("no extension: %q", got)
	}
}

func TestStaticDefaultErrorAnswer(t *testing.T) {
	file := loadStaticVectors(t)
	root := writeFixtures(t, file.Fixtures)
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { t.Error("next called") })
	s := NewStatic(root, StaticOptions{NoFallthrough: true})

	rec := httptest.NewRecorder()
	s.Serve(rec, rawRequest("GET", "/missing.txt", nil), "/missing.txt", next)
	if rec.Code != 404 || rec.Body.String() != "Not Found" || rec.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Errorf("missing file: %d %q %q", rec.Code, rec.Body.String(), rec.Header().Get("Content-Type"))
	}

	rec = httptest.NewRecorder()
	s.Serve(rec, rawRequest("GET", "/big.bin", [][2]string{{"Range", "bytes=5000-"}}), "/big.bin", next)
	if rec.Code != 416 || rec.Header().Get("Content-Range") != "bytes */1000" || rec.Body.String() != "Range Not Satisfiable" {
		t.Errorf("range: %d %q %q", rec.Code, rec.Header().Get("Content-Range"), rec.Body.String())
	}

	rec = httptest.NewRecorder()
	s.Serve(rec, rawRequest("HEAD", "/missing.txt", nil), "/missing.txt", next)
	if rec.Code != 404 || rec.Body.Len() != 0 {
		t.Errorf("HEAD of a missing file: %d %q", rec.Code, rec.Body.String())
	}
}

// brokenFS fails where a test says so.
type brokenFS struct {
	statErr error
	openErr error
	seen    []string
}

func (b *brokenFS) Stat(name string) (fs.FileInfo, error) {
	b.seen = append(b.seen, name)
	if b.statErr != nil {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: b.statErr}
	}
	return os.Stat(name)
}

func (b *brokenFS) Open(name string) (io.ReadSeekCloser, error) {
	b.seen = append(b.seen, name)
	if b.openErr != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: b.openErr}
	}
	return os.Open(name)
}

func TestStaticDiskErrors(t *testing.T) {
	file := loadStaticVectors(t)
	root := writeFixtures(t, file.Fixtures)
	cases := []struct {
		name    string
		fs      *brokenFS
		status  int
		wantErr bool
	}{
		{"stat permission", &brokenFS{statErr: syscall.EACCES}, 500, true},
		{"stat i/o", &brokenFS{statErr: syscall.EIO}, 500, true},
		{"stat not a directory", &brokenFS{statErr: syscall.ENOTDIR}, 0, false},
		{"stat name too long", &brokenFS{statErr: syscall.ENAMETOOLONG}, 0, false},
		{"stat missing", &brokenFS{statErr: syscall.ENOENT}, 0, false},
		{"open permission", &brokenFS{openErr: syscall.EACCES}, 500, true},
		{"open missing after stat", &brokenFS{openErr: syscall.ENOENT}, 404, true},
	}
	for _, c := range cases {
		nextCalled := false
		next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { nextCalled = true })
		var gotStatus int
		var gotErr error
		s := NewStatic(root, StaticOptions{FS: c.fs, OnError: func(w http.ResponseWriter, _ *http.Request, status int, err error) {
			gotStatus, gotErr = status, err
			w.WriteHeader(status)
		}})
		rec := httptest.NewRecorder()
		s.Serve(rec, rawRequest("GET", "/favicon.svg", nil), "/favicon.svg", next)
		if c.wantErr {
			if nextCalled || gotStatus != c.status || gotErr == nil {
				t.Errorf("%s: next=%v status=%d err=%v, want OnError with %d", c.name, nextCalled, gotStatus, gotErr, c.status)
			}
			var pathErr *fs.PathError
			if !errors.As(gotErr, &pathErr) {
				t.Errorf("%s: error %v does not carry the disk error", c.name, gotErr)
			}
		} else if !nextCalled || gotErr != nil {
			t.Errorf("%s: next=%v err=%v, want a plain miss", c.name, nextCalled, gotErr)
		}
	}
}

func TestStaticWindowsNames(t *testing.T) {
	file := loadStaticVectors(t)
	root := writeFixtures(t, file.Fixtures)
	if err := os.WriteFile(filepath.Join(root, "NUL"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "favicon.svg."), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	serve := func(windows bool, target string) (int, []string) {
		rec := &brokenFS{}
		s := NewStatic(root, StaticOptions{FS: rec})
		s.windows = windows
		w := httptest.NewRecorder()
		s.Middleware("")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) })).ServeHTTP(w, rawRequest("GET", target, nil))
		return w.Code, rec.seen
	}
	// On Windows a backslash is a separator
	if code, _ := serve(true, `/img\logo.png`); code != 200 {
		t.Errorf(`windows /img\logo.png: %d, want the file`, code)
	}
	if code, _ := serve(false, `/img\logo.png`); code != 404 {
		t.Errorf(`/img\logo.png elsewhere: %d, want a miss`, code)
	}
	for _, target := range []string{"/NUL", "/nul.txt", "/CON", "/com1", "/LPT9.x", "/favicon.svg.", "/favicon.svg%20", "/favicon.svg::$DATA",
		"/C:/Windows/win.ini", "/img/C:", "/a%3Ab", "/img/aux/x", "/x%7Cy", "/x%3Fy", "/x*y", `/x"y`} {
		code, seen := serve(true, target)
		if code != 404 || len(seen) != 0 {
			t.Errorf("windows %s: status %d, disk calls %v; want a miss without touching the disk", target, code, seen)
		}
	}
	for _, target := range []string{"/NUL", "/favicon.svg."} {
		if code, _ := serve(false, target); code != 200 {
			t.Errorf("%s elsewhere: %d, want the file of that name", target, code)
		}
	}
	for _, target := range []string{`/..\favicon.svg`, `/img\..\..\x`, "/img/../../x"} {
		for _, windows := range []bool{true, false} {
			if code, seen := serve(windows, target); code != 404 || len(seen) != 0 {
				t.Errorf("windows=%v %s: status %d, disk calls %v", windows, target, code, seen)
			}
		}
	}
}

func TestStaticServeName(t *testing.T) {
	file := loadStaticVectors(t)
	root := writeFixtures(t, file.Fixtures)
	for name, content := range map[string]string{"50%.txt": "percent", "app.js.br": "compressed", "a%20b.txt": "escaped"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(root), "outside.txt"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewStatic(root, StaticOptions{MaxAge: time.Hour})
	serveName := func(method, name string, preset map[string]string) (*httptest.ResponseRecorder, bool) {
		rec := httptest.NewRecorder()
		for k, v := range preset {
			rec.Header().Set(k, v)
		}
		missed := false
		s.ServeName(rec, rawRequest(method, "/whatever/the/address/is", nil), name, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { missed = true }))
		return rec, missed
	}
	// The name is taken as it is: a % is a letter of the name
	for name, want := range map[string]string{"50%.txt": "percent", "a%20b.txt": "escaped", "img/half.txt": "half\n", "img/a b.txt": "space name\n",
		"/favicon.svg": `<svg xmlns="http://www.w3.org/2000/svg"/>`, "sub/deep/../index.html": "<p>index</p>"} {
		rec, missed := serveName("GET", name, nil)
		if missed || rec.Code != 200 || rec.Body.String() != want {
			t.Errorf("%q: missed %v, %d %q", name, missed, rec.Code, rec.Body.String())
		}
	}
	// Headers set before are kept, the others are the file's
	rec, _ := serveName("GET", "app.js.br", map[string]string{"Content-Type": "text/javascript", "Content-Encoding": "br", "Cache-Control": "public, max-age=31536000, immutable"})
	h := rec.Header()
	if rec.Code != 200 || h.Get("Content-Type") != "text/javascript" || h.Get("Content-Encoding") != "br" || h.Get("Cache-Control") != "public, max-age=31536000, immutable" ||
		h.Get("Content-Length") != "10" || h.Get("ETag") == "" || h.Get("Last-Modified") == "" {
		t.Errorf("precompressed file: %d %v", rec.Code, h)
	}
	if rec, _ := serveName("GET", "50%.txt", nil); rec.Header().Get("Cache-Control") != "public, max-age=3600" {
		t.Errorf("Cache-Control of the folder: %q", rec.Header().Get("Cache-Control"))
	}
	if rec, _ := serveName("HEAD", "50%.txt", nil); rec.Code != 200 || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != "7" {
		t.Errorf("HEAD: %d %q", rec.Code, rec.Body.String())
	}
	// What leaves the folder or is not a file there is a miss
	for _, name := range []string{"../outside.txt", "img/../../outside.txt", "..", "missing.txt", ".hidden", "sub", "sub/", "a%2520b.txt", "img/a%20b.txt", "x0" + string(rune(0)) + "y"} {
		rec, missed := serveName("GET", name, nil)
		if name == "sub" {
			// a folder redirects, as with Serve
			if rec.Code != 301 {
				t.Errorf("%q: %d, want the folder's redirect", name, rec.Code)
			}
			continue
		}
		if name == "sub/" {
			if missed || rec.Body.String() != "<p>index</p>" {
				t.Errorf("%q: missed %v %q, want the index file", name, missed, rec.Body.String())
			}
			continue
		}
		if !missed || rec.Body.Len() != 0 {
			t.Errorf("%q: missed %v, %d %q", name, missed, rec.Code, rec.Body.String())
		}
	}
	if _, missed := serveName("POST", "50%.txt", nil); !missed {
		t.Error("POST goes to the next handler")
	}
}

func TestStripMount(t *testing.T) {
	cases := []struct {
		path, mount string
		rest, base  string
		ok          bool
	}{
		{"/api/x", "/api/", "/x", "/api", true},
		{"/api", "/api/", "/", "/api", true},
		{"/api/", "/api/", "/", "/api", true},
		{"/API/x", "/api/", "/x", "/API", true},
		{"/apix", "/api/", "", "", false},
		{"/api.json", "/api", "", "", false},
		{"/api//x", "/api/", "/x", "/api", true},
		{"/api///x", "/api/", "//x", "/api", true},
		{"/x", "", "/x", "", true},
		{"/x", "/", "/x", "", true},
		{"/api/devices/7", "/api/devices", "/7", "/api/devices", true},
		{"/api/Devices", "/api/devices", "/", "/api/Devices", true},
		{"/ap", "/api", "", "", false},
		{"//api/x", "/api", "", "", false},
		{"/api%2Fx", "/api", "", "", false},
	}
	for _, c := range cases {
		rest, base, ok := StripMount(c.path, c.mount)
		if rest != c.rest || base != c.base || ok != c.ok {
			t.Errorf("StripMount(%q, %q) = %q, %q, %v; want %q, %q, %v", c.path, c.mount, rest, base, ok, c.rest, c.base, c.ok)
		}
	}
}

func TestRawPath(t *testing.T) {
	for target, want := range map[string]string{
		"/a/b?x=1":       "/a/b",
		"/a%2Fb":         "/a%2Fb",
		"/a#frag":        "/a",
		"/a?x#y":         "/a",
		"//a//b":         "//a//b",
		"/a/../b":        "/a/../b",
		"/%zz":           "/%zz",
		"/":              "/",
		"/a;b=c/d?e;f=g": "/a;b=c/d",
	} {
		if got := RawPath(rawRequest("GET", target, nil)); got != want {
			t.Errorf("RawPath(%q) = %q, want %q", target, got, want)
		}
	}
	// A request built by hand has no RequestURI
	r := httptest.NewRequest("GET", "/a%2Fb/c?x=1", nil)
	r.RequestURI = ""
	if got := RawPath(r); got != "/a%2Fb/c" {
		t.Errorf("without RequestURI: %q", got)
	}
}

func TestParseHTTPDate(t *testing.T) {
	const want = int64(1685750401000)
	for _, s := range []string{
		"Sat, 03 Jun 2023 00:00:01 GMT", "sat, 03 jun 2023 00:00:01 gmt", "Sat, 3 Jun 2023 00:00:01 GMT", "Saturday, 03-Jun-23 00:00:01 GMT",
		"Sat Jun  3 00:00:01 2023", "2023-06-03T00:00:01Z", "2023-06-03T00:00:01.000Z", "Sat, 03 Jun 2023 02:00:01 +0200",
		"Sat, 03 Jun 2023 00:00:01 UTC", "03 Jun 2023 00:00:01 GMT", "Fri, 02 Jun 2023 19:00:01 EST", " Sat, 03 Jun 2023 00:00:01 GMT ",
	} {
		if got, ok := parseHTTPDate(s); !ok || got != want {
			t.Errorf("%q: %d %v, want %d", s, got, ok, want)
		}
	}
	if got, ok := parseHTTPDate("2023-06-03"); !ok || got != 1685750400000 {
		t.Errorf("date only: %d %v", got, ok)
	}
	if got, ok := parseHTTPDate("2023-06-03T00:00:01.5Z"); !ok || got != want+500 {
		t.Errorf("fraction: %d %v", got, ok)
	}
	for _, s := range []string{"", "garbage", "1685750401000", "Sat, 03 Jun 2023 00:00:01 XYZ", "Sat, 32 Jun 2023 00:00:01 GMT"} {
		if got, ok := parseHTTPDate(s); ok {
			t.Errorf("%q: accepted as %d", s, got)
		}
	}
}

func TestEncodeURL(t *testing.T) {
	for in, want := range map[string]string{
		"/a b/":         "/a%20b/",
		"/a%20b/":       "/a%20b/",
		"/a%2fb/":       "/a%2fb/",
		`/a"<>` + "`{}": "/a%22%3C%3E%60%7B%7D",
		`/a\^[]|~!`:     `/a\^[]|~!`,
		"/100%/":        "/100%25/",
		"/a%zz":         "/a%25zz",
		"/a%4":          "/a%4",
		"/a%4z":         "/a%254z",
		"/a%":           "/a%25",
		"/a%[":          "/a%25%5B",
		"/\xe9/":        "/%C3%A9/",
		"/a?q=<1>&r=#x": "/a?q=%3C1%3E&r=#x",
		"/a\x7f\x01":    "/a%7F%01",
		"/a%%41":        "/a%25%2541",
	} {
		if got := encodeURL(in); got != want {
			t.Errorf("encodeURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestJSMillisRoundsLikeNode(t *testing.T) {
	for _, c := range []struct {
		sec, nsec int64
		want      int64
	}{
		{1685577600, 999600000, 1685577601000},
		{1685577600, 499999999, 1685577600500},
		{1685577602, 500000, 1685577602001},
		{1685577602, 499999, 1685577602001},
		{1685577602, 400000, 1685577602000},
		{0, 400000000, 400},
		{-1, 500000, -999},
		{-1, 999500000, 0},
	} {
		if got := jsMillis(time.Unix(c.sec, c.nsec)); got != c.want {
			t.Errorf("jsMillis(%d.%09d) = %d, want %d", c.sec, c.nsec, got, c.want)
		}
	}
}

// recordingFS notes every path the file server asks the disk for.
type recordingFS struct {
	mu    sync.Mutex
	paths []string
}

func (f *recordingFS) Stat(name string) (fs.FileInfo, error) {
	f.note(name)
	return os.Stat(name)
}

func (f *recordingFS) Open(name string) (io.ReadSeekCloser, error) {
	f.note(name)
	return os.Open(name)
}

func (f *recordingFS) note(name string) {
	f.mu.Lock()
	f.paths = append(f.paths, name)
	f.mu.Unlock()
}

// FuzzStaticPath: whatever the request path, no file outside the root is
// asked for, and nothing outside it is sent.
func FuzzStaticPath(f *testing.F) {
	base := f.TempDir()
	root := filepath.Join(base, "root")
	const secret = "TOP-SECRET-OUTSIDE-THE-ROOT"
	files := map[string]string{
		"root/index.html":      "<p>root</p>",
		"root/a.txt":           "inside a",
		"root/sub/b.txt":       "inside b",
		"root/sub/index.html":  "<p>sub</p>",
		"root/.hidden":         "inside hidden",
		"root/.well/c.txt":     "inside c",
		"secret.txt":           secret,
		"root-sibling/d.txt":   secret,
		"rootx":                secret,
		"root/sub/deep/e.json": "{}",
	}
	for name, content := range files {
		full := filepath.Join(base, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			f.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			f.Fatal(err)
		}
	}
	for _, seed := range []string{"/a.txt", "/sub/", "/sub", "/../secret.txt", "/%2e%2e/secret.txt", "/..%2fsecret.txt", `/..\secret.txt`, "/sub/../../secret.txt",
		"/%2e%2e%2f%2e%2e%2fsecret.txt", "//..//secret.txt", "/sub/..%5c..%5csecret.txt", "/a.txt%00", "/%zz", "/.hidden", "/.well/c.txt", "/....//secret.txt",
		"/..;/secret.txt", "/sub/./../a.txt", "/../root-sibling/d.txt", "/../rootx", "/%c0%ae%c0%ae/secret.txt", "/\x00", "/a.txt?x=../../secret.txt",
		"/a.txt#../secret.txt", "/sub/deep/../../../secret.txt", "/C:/secret.txt", "/sub/deep/e.json", "/%E0%A4%A", "/..%00/secret.txt", "/.%2e/secret.txt"} {
		f.Add(seed, uint8(0), "")
		f.Add(seed, uint8(128), "bytes=0-3")
	}
	f.Fuzz(func(t *testing.T, target string, mode uint8, rangeHeader string) {
		if !strings.HasPrefix(target, "/") || strings.ContainsAny(target, "\r\n ") {
			return
		}
		rec := &recordingFS{}
		opts := StaticOptions{FS: rec, NoFallthrough: mode&1 != 0, NoRedirect: mode&2 != 0, NoIndex: mode&4 != 0}
		opts.Dotfiles = []string{"", "allow", "deny", "ignore"}[int(mode>>3)&3]
		s := NewStatic(root, opts)
		s.windows = mode&32 != 0
		mount := ""
		if mode&64 != 0 {
			mount = "/m"
		}
		var headers [][2]string
		if rangeHeader != "" {
			headers = append(headers, [2]string{"Range", rangeHeader}, [2]string{"If-Range", rangeHeader}, [2]string{"If-None-Match", rangeHeader},
				[2]string{"If-Modified-Since", rangeHeader}, [2]string{"If-Match", rangeHeader}, [2]string{"If-Unmodified-Since", rangeHeader})
		}
		w := httptest.NewRecorder()
		next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
		if mode&128 != 0 {
			// the same text as a file name a caller picked
			s.ServeName(w, rawRequest("GET", "/x", headers), target, next)
		} else {
			s.Middleware(mount)(next).ServeHTTP(w, rawRequest("GET", target, headers))
		}

		prefix := root + string(filepath.Separator)
		for _, p := range rec.paths {
			cleaned := filepath.Clean(p)
			if cleaned != root && !strings.HasPrefix(cleaned, prefix) {
				t.Fatalf("target %q: the disk was asked for %q, outside %q", target, p, root)
			}
		}
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("target %q: a file outside the root was sent", target)
		}
		switch w.Code {
		case 200, 206, 301, 304, 400, 403, 404, 412, 416, http.StatusTeapot:
		default:
			t.Fatalf("target %q: status %d", target, w.Code)
		}
		if w.Code == 206 || w.Code == 200 {
			if cl := w.Header().Get("Content-Length"); cl != strconv.Itoa(w.Body.Len()) {
				t.Fatalf("target %q range %q: Content-Length %s for %d bytes", target, rangeHeader, cl, w.Body.Len())
			}
		}
	})
}
