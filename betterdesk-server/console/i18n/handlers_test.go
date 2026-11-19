package i18n

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/httpx"
)

// routes registers the handlers the way the console's router does.
func routes(h *Handlers) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/i18n/languages", h.Languages)
	mux.HandleFunc("GET /api/i18n/translations/{code}", h.Translations)
	mux.HandleFunc("POST /api/i18n/set/{code}", h.Set)
	mux.HandleFunc("GET /api/i18n/validate/{code}", h.Validate)
	mux.HandleFunc("POST /api/i18n/upload", h.Upload)
	mux.HandleFunc("DELETE /api/i18n/{code}", h.Delete)
	mux.HandleFunc("GET /api/panel/languages", h.PanelLanguages)
	mux.HandleFunc("GET /api/panel/languages/{code}/missing", h.PanelMissing)
	mux.HandleFunc("POST /api/panel/languages/{code}/fix", h.PanelFix)
	mux.HandleFunc("GET /api/console/strings/{code}", h.Strings)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r.WithContext(httpx.WithJSONCompression(r.Context())))
	})
}

// recorder stands in for the session, the user table and the audit log.
type recorder struct {
	audit     [][2]string
	persisted []string
	preferred string
}

// handlersFor builds handlers on dir whose hooks follow the request
// headers the vector script used to steer its Node stand-ins.
func handlersFor(t testing.TB, dir string, now time.Time) (*Handlers, *recorder, *logs) {
	t.Helper()
	b, l := loaded(t, dir, "en")
	rec := &recorder{}
	h := &Handlers{Bundle: b, Logf: l.logf, Now: func() time.Time { return now }}
	h.SetPreferredLanguage = func(r *http.Request, code string) error {
		user := r.Header.Get("X-User")
		if user == "" {
			return nil
		}
		rec.preferred = code
		if r.Header.Get("X-Persist-Fails") == "1" {
			return errors.New("db down")
		}
		rec.persisted = append(rec.persisted, user+" "+code)
		return nil
	}
	h.Audit = func(r *http.Request, action, details string) error {
		if r.Header.Get("X-Audit-Fails") == "1" {
			return errors.New("audit down")
		}
		rec.audit = append(rec.audit, [2]string{action, details})
		return nil
	}
	return h, rec, l
}

// perRequest sets what the Node console read from its environment.
func perRequest(h *Handlers, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.Production = r.Header.Get("X-Production") == "1"
		h.SecureCookie = h.Production || r.Header.Get("X-Https") == "1"
		next.ServeHTTP(w, r)
	})
}

var plainClient = &http.Client{
	Transport:     &http.Transport{DisableCompression: true},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

type answer struct {
	status int
	header http.Header
	body   string
}

func do(t testing.TB, base, method, path string, headers map[string]string, body io.Reader, length int64) answer {
	t.Helper()
	req, err := http.NewRequest(method, base+path, body)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	if body != nil {
		req.ContentLength = length
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := plainClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("%s %s: read: %v", method, path, err)
	}
	if res.Header.Get("Content-Encoding") == "gzip" && len(raw) > 0 {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("%s %s: gzip: %v", method, path, err)
		}
		if raw, err = io.ReadAll(zr); err != nil {
			t.Fatalf("%s %s: gunzip: %v", method, path, err)
		}
	}
	return answer{res.StatusCode, res.Header, string(raw)}
}

func headersOf(c any) map[string]string {
	out := map[string]string{}
	if o, ok := at(c, "headers").(*Object); ok {
		for _, k := range o.Keys() {
			out[k] = str(o, k)
		}
	}
	return out
}

func fileNames(t testing.TB, dir string) []string {
	t.Helper()
	items, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, item := range items {
		if !item.IsDir() {
			names = append(names, item.Name())
		}
	}
	sort.Strings(names)
	return names
}

// goMuxRedirects reports paths with a literal . or .. element. Go's
// ServeMux answers those with a redirect before any handler runs; Express
// handed them to the route.
func goMuxRedirects(path string) bool {
	for _, part := range strings.Split(path, "/") {
		if part == "." || part == ".." {
			return true
		}
	}
	return false
}

var nodeTempDir = regexp.MustCompile(`/tmp/i18n-[a-z]+-[A-Za-z0-9]+`)

// The i18n routes against the answers of the Node routes for the same
// requests on the same files: status, body, content type, ETag, encoding,
// Vary, the cookie, audit entries, the stored preference and removed files.
func TestRoutesLikeNode(t *testing.T) {
	for _, name := range []string{"routes-edge.json", "routes-upload.json"} {
		t.Run(name, func(t *testing.T) {
			want := fixedVectors(t, name)
			now := time.UnixMilli(int64(num(want, "now")))
			fixture := filepath.Join("testdata", str(want, "label"))
			cases := list(want, "vectors")
			if len(cases) < 100 {
				t.Fatalf("only %d vectors", len(cases))
			}
			skipped := 0
			for _, c := range cases {
				method, path := str(c, "method"), str(c, "path")
				if goMuxRedirects(path) {
					skipped++
					continue
				}
				dir := copyDir(t, fixture)
				for _, f := range strs(c, "removeFiles") {
					if err := os.RemoveAll(filepath.Join(dir, f)); err != nil {
						t.Fatal(err)
					}
				}
				h, rec, _ := handlersFor(t, dir, now)
				srv := httptest.NewServer(perRequest(h, routes(h)))
				for _, b := range list(c, "before") {
					do(t, srv.URL, str(b, "0"), str(b, "1"), map[string]string{"X-User": "1"}, nil, 0)
				}
				rec.audit, rec.persisted, rec.preferred = nil, nil, ""
				before := fileNames(t, dir)
				got := do(t, srv.URL, method, path, headersOf(c), nil, 0)
				srv.Close()

				label := method + " " + path + " " + stringify(at(c, "headers"), "")
				wantBody := nodeTempDir.ReplaceAllString(str(c, "body"), "<dir>")
				gotBody := strings.ReplaceAll(got.body, dir, "<dir>")
				pathInBody := wantBody != str(c, "body")
				if got.status != int(num(c, "status")) {
					t.Errorf("%s: status %d, Node %v (body %s)", label, got.status, num(c, "status"), short(got.body))
					continue
				}
				if gotBody != wantBody {
					t.Errorf("%s: body\n got  %s\n want %s", label, short(gotBody), short(wantBody))
				}
				if g, w := got.header.Get("Content-Type"), str(c, "contentType"); g != w {
					t.Errorf("%s: Content-Type %q, Node %q", label, g, w)
				}
				if g, w := got.header.Get("ETag"), str(c, "etag"); g != w && !pathInBody {
					t.Errorf("%s: ETag %q, Node %q", label, g, w)
				}
				if g, w := got.header.Get("Content-Encoding"), str(c, "contentEncoding"); g != w {
					t.Errorf("%s: Content-Encoding %q, Node %q", label, g, w)
				}
				if g, w := got.header.Get("Vary"), str(c, "vary"); g != w {
					t.Errorf("%s: Vary %q, Node %q", label, g, w)
				}
				if g, w := got.header.Values("Set-Cookie"), strs(c, "setCookie"); !reflect.DeepEqual(g, w) {
					t.Errorf("%s: Set-Cookie %q, Node %q", label, g, w)
				}
				var wantAudit [][2]string
				for _, a := range list(c, "audit") {
					wantAudit = append(wantAudit, [2]string{str(a, "action"), str(a, "details")})
				}
				if !reflect.DeepEqual(rec.audit, wantAudit) {
					t.Errorf("%s: audit %v, Node %v", label, rec.audit, wantAudit)
				}
				var wantPersisted []string
				for _, p := range list(c, "persisted") {
					wantPersisted = append(wantPersisted, numberString(num(p, "id"))+" "+str(p, "lang"))
				}
				if !reflect.DeepEqual(rec.persisted, wantPersisted) {
					t.Errorf("%s: stored preference %v, Node %v", label, rec.persisted, wantPersisted)
				}
				if w := str(c, "sessionPreferred"); rec.preferred != w {
					t.Errorf("%s: session preference %q, Node %q", label, rec.preferred, w)
				}
				var removed []string
				after := map[string]bool{}
				for _, f := range fileNames(t, dir) {
					after[f] = true
				}
				for _, f := range before {
					if !after[f] {
						removed = append(removed, f)
					}
				}
				if !reflect.DeepEqual(removed, strs(c, "removed")) {
					t.Errorf("%s: removed files %v, Node %v", label, removed, strs(c, "removed"))
				}
			}
			if skipped > 4 {
				t.Errorf("%d vectors skipped for paths Go's router redirects", skipped)
			}
		})
	}
}

func segments(c any) []byte {
	var out []byte
	for _, s := range list(c, "segments") {
		if b64 := str(s, "b64"); b64 != "" || at(s, "repeat") == undefined {
			raw, _ := base64.StdEncoding.DecodeString(b64)
			out = append(out, raw...)
			continue
		}
		out = append(out, bytes.Repeat([]byte(str(s, "repeat")), int(num(s, "n")))...)
	}
	return out
}

// The upload route against multer and busboy: about 140 forms, good and
// broken, with the answer, the files written and the audit entry.
func TestUploadLikeNode(t *testing.T) {
	cases := list(fixedVectors(t, "upload.json"))
	if len(cases) < 130 {
		t.Fatalf("only %d vectors", len(cases))
	}
	original := map[string]string{}
	for _, f := range fileNames(t, "testdata/lang-upload") {
		raw, _ := os.ReadFile(filepath.Join("testdata/lang-upload", f))
		original[f] = string(raw)
	}
	for _, c := range cases {
		name := str(c, "name")
		dir := copyDir(t, "testdata/lang-upload")
		h, rec, _ := handlersFor(t, dir, time.Now())
		srv := httptest.NewServer(perRequest(h, routes(h)))
		headers := map[string]string{"X-User": "7"}
		if ct, ok := at(c, "contentType").(string); ok {
			headers["Content-Type"] = ct
		}
		if flag(c, "production") {
			headers["X-Production"] = "1"
		}
		if flag(c, "auditFails") {
			headers["X-Audit-Fails"] = "1"
		}
		body := segments(c)
		var reader io.Reader
		length := int64(len(body))
		switch {
		case flag(c, "chunked") || (len(body) == 0 && !flag(c, "contentLengthZero")):
			// Node's client sends a POST without a length chunked.
			reader, length = io.NopCloser(bytes.NewReader(body)), -1
		case len(body) > 0:
			reader = bytes.NewReader(body)
		}
		got := do(t, srv.URL, "POST", "/api/i18n/upload", headers, reader, length)
		srv.Close()

		if got.status != int(num(c, "status")) || got.body != str(c, "body") {
			t.Errorf("%s:\n got  %d %s\n want %v %s", name, got.status, short(got.body), num(c, "status"), short(str(c, "body")))
		}
		if g, w := got.header.Get("Content-Type"), str(c, "contentTypeOut"); g != w {
			t.Errorf("%s: Content-Type %q, Node %q", name, g, w)
		}
		changed := map[string]string{}
		for _, f := range fileNames(t, dir) {
			raw, _ := os.ReadFile(filepath.Join(dir, f))
			if old, ok := original[f]; !ok || old != string(raw) {
				changed[f] = base64.StdEncoding.EncodeToString(raw)
			}
		}
		wantChanged := map[string]string{}
		if o, ok := at(c, "changed").(*Object); ok {
			for _, k := range o.Keys() {
				wantChanged[k] = str(o, k)
			}
		}
		if !reflect.DeepEqual(changed, wantChanged) {
			t.Errorf("%s: files written %v, Node wrote %v", name, decodeAll(changed), decodeAll(wantChanged))
		}
		var wantAudit [][2]string
		for _, a := range list(c, "audit") {
			wantAudit = append(wantAudit, [2]string{str(a, "action"), str(a, "details")})
		}
		if !reflect.DeepEqual(rec.audit, wantAudit) {
			t.Errorf("%s: audit %v, Node %v", name, rec.audit, wantAudit)
		}
		if g, w := stringify(h.Bundle.Available(), ""), str(c, "available"); g != w {
			t.Errorf("%s: available languages after the upload\n got  %s\n want %s", name, g, w)
		}
	}
}

func decodeAll(m map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		raw, _ := base64.StdEncoding.DecodeString(v)
		out[k] = short(string(raw))
	}
	return out
}

// filePart is a form part with a JSON file in the field "file".
func filePart(name, content string) string {
	return "Content-Disposition: form-data; name=\"file\"; filename=\"" + name + "\"\r\nContent-Type: application/json\r\n\r\n" + content
}

// codePart is a form part with the text field "code".
func codePart(code string) string {
	return "Content-Disposition: form-data; name=\"code\"\r\n\r\n" + code
}

// postForm sends parts (header lines, an empty line, the content) as a
// multipart form to POST /api/i18n/upload.
func postForm(t testing.TB, h *Handlers, parts ...string) answer {
	t.Helper()
	var form strings.Builder
	for _, p := range parts {
		form.WriteString("--B\r\n" + p + "\r\n")
	}
	form.WriteString("--B--\r\n")
	srv := httptest.NewServer(routes(h))
	defer srv.Close()
	body := form.String()
	return do(t, srv.URL, "POST", "/api/i18n/upload", map[string]string{"Content-Type": "multipart/form-data; boundary=B"}, strings.NewReader(body), int64(len(body)))
}

// An upload whose code is not one of the languages the console loads is
// refused before anything is written (BUGS.md L7).
func TestUploadRefusesCodeOutsideTheLanguageTable(t *testing.T) {
	for _, parts := range [][]string{
		{filePart("xx.json", `{"a":1}`)},
		{filePart("en-US.json", `{"a":1}`)},
		{filePart("abcde.json", `{"a":1}`)},
		{filePart("EN.json", `{"a":1}`)},
		{filePart("fr.json", `{"_meta":{"code":"en-GB"},"a":1}`)},
		{codePart("xx"), filePart("foo.json", `{"a":1}`)},
	} {
		dir := copyDir(t, "testdata/lang-upload")
		h, rec, _ := handlersFor(t, dir, time.Now())
		before := fileNames(t, dir)
		got := postForm(t, h, parts...)
		if got.status != 400 || got.body != `{"success":false,"error":"Invalid language code"}` {
			t.Errorf("%q: %d %s", parts, got.status, got.body)
		}
		if after := fileNames(t, dir); !reflect.DeepEqual(after, before) || len(rec.audit) != 0 {
			t.Errorf("%q: files %v (before %v), audit %v", parts, after, before, rec.audit)
		}
	}
	h, _, _ := handlersFor(t, copyDir(t, "testdata/lang-upload"), time.Now())
	if got := postForm(t, h, filePart("fr.json", `{"a":1}`)); got.status != 200 || !h.Bundle.Has("fr") {
		t.Errorf("a code of the table: %d %s", got.status, got.body)
	}
}

// A code that is not a string is refused with 400 like any other code
// outside the language table, whatever its length says (BUGS.md L10).
func TestUploadCodeChecks(t *testing.T) {
	for _, parts := range [][]string{
		{filePart("fr.json", `{"_meta":{"code":12345}}`)},
		{filePart("fr.json", `{"_meta":{"code":true}}`)},
		{filePart("fr.json", `{"_meta":{"code":["de"]}}`)},
		{filePart("fr.json", `{"_meta":{"code":["de","fr"]}}`)},
		{filePart("fr.json", `{"_meta":{"code":{"length":3}}}`)},
		{filePart("fr.json", `{"_meta":{"code":{"toString":"de"}}}`)},
		{"Content-Disposition: form-data; name=\"code[length]\"\r\n\r\n3", filePart("foo.json", `{"a":1}`)},
		{codePart("de"), codePart("fr"), filePart("foo.json", `{"a":1}`)},
		{"Content-Disposition: form-data; name=\"code[3]\"\r\n\r\nde", filePart("foo.json", `{"a":1}`)},
	} {
		dir := copyDir(t, "testdata/lang-upload")
		h, rec, _ := handlersFor(t, dir, time.Now())
		before := fileNames(t, dir)
		got := postForm(t, h, parts...)
		if got.status != 400 || got.body != `{"success":false,"error":"Invalid language code"}` {
			t.Errorf("%q: %d %s", parts, got.status, got.body)
		}
		if after := fileNames(t, dir); !reflect.DeepEqual(after, before) || len(rec.audit) != 0 {
			t.Errorf("%q: files %v (before %v), audit %v", parts, after, before, rec.audit)
		}
	}
	for _, code := range []string{"zh-TW", "nb", "de"} {
		h, _, _ := handlersFor(t, copyDir(t, "testdata/lang-upload"), time.Now())
		if got := postForm(t, h, filePart("x.json", `{"_meta":{"code":"`+code+`"}}`)); got.status != 200 || !h.Bundle.Has(code) {
			t.Errorf("%s: %d %s", code, got.status, got.body)
		}
	}
}

// A text field or a filename* in a charset other than UTF-8, Latin-1,
// UTF-16LE or base64 is decoded by its charset, or read as UTF-8 when the
// charset is unknown, instead of being dropped (BUGS.md L11).
func TestUploadFieldInShiftJIS(t *testing.T) {
	if got := decodeField([]byte{0x93, 0xfa, 0x96, 0x7b}, "shift_jis"); got != "日本" {
		t.Errorf("Shift JIS field = %#v", got)
	}
	if got := decodeParam([]rune{0x93, 0xfa, 0x96, 0x7b}, "Shift_JIS"); got != "日本" {
		t.Errorf("Shift JIS parameter = %q", got)
	}
	if got := decodeField([]byte("日本"), "x-no-such-charset"); got != "日本" {
		t.Errorf("field in an unknown charset = %#v", got)
	}
	file := func(disposition string) string {
		return "Content-Disposition: form-data; name=\"file\"; " + disposition + "\r\nContent-Type: application/json\r\n\r\n{\"a\":1}"
	}
	for _, c := range []struct {
		parts []string
		code  string
	}{
		{[]string{"Content-Disposition: form-data; name=\"code\"\r\nContent-Type: text/plain; charset=shift_jis\r\n\r\nes", filePart("it.json", `{"a":1}`)}, "es"},
		{[]string{"Content-Disposition: form-data; name=\"code\"\r\nContent-Type: text/plain; charset=x-no-such-charset\r\n\r\nes", filePart("it.json", `{"a":1}`)}, "es"},
		{[]string{file("filename*=shift_jis''it.json")}, "it"},
		{[]string{file("filename*=x-no-such-charset''nl.json")}, "nl"},
	} {
		dir := copyDir(t, "testdata/lang-upload")
		h, _, _ := handlersFor(t, dir, time.Now())
		got := postForm(t, h, c.parts...)
		if got.status != 200 || str(mustParse(t, got.body), "data", "code") != c.code {
			t.Errorf("%q: %d %s; want the code %s", c.parts, got.status, got.body, c.code)
		}
	}
}

func mustParse(t testing.TB, text string) any {
	t.Helper()
	v, err := parseJSON(text)
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	return v
}

// The translations page's routes against Node, on the odd fixture files
// and with files added and removed.
func TestPanelRoutesLikeNode(t *testing.T) {
	cases := list(fixedVectors(t, "panel-edge.json"))
	if len(cases) < 40 {
		t.Fatalf("only %d vectors", len(cases))
	}
	for _, c := range cases {
		method, path := str(c, "method"), str(c, "path")
		if goMuxRedirects(path) {
			continue
		}
		dir := copyDir(t, "testdata/lang-edge")
		for _, f := range strs(c, "removeFiles") {
			if err := os.RemoveAll(filepath.Join(dir, f)); err != nil {
				t.Fatal(err)
			}
		}
		written := ""
		if o, ok := at(c, "write").(*Object); ok {
			for _, k := range o.Keys() {
				written += " " + k
				if err := os.WriteFile(filepath.Join(dir, k), []byte(str(o, k)), 0o644); err != nil {
					t.Fatal(err)
				}
			}
		}
		h, _, _ := handlersFor(t, dir, time.Now())
		srv := httptest.NewServer(routes(h))
		got := do(t, srv.URL, method, path, nil, nil, 0)
		srv.Close()
		label := method + " " + path + " without " + strings.Join(strs(c, "removeFiles"), ",") + " with" + written
		if got.status != int(num(c, "status")) {
			t.Errorf("%s: status %d, Node %v (%s)", label, got.status, num(c, "status"), short(got.body))
			continue
		}
		if got.body != str(c, "body") {
			t.Errorf("%s: body\n got  %s\n want %s", label, firstDifference(got.body, str(c, "body")), "")
		}
		if g, w := got.header.Get("ETag"), str(c, "etag"); g != w {
			t.Errorf("%s: ETag %q, Node %q", label, g, w)
		}
		if g, w := got.header.Get("Content-Type"), str(c, "contentType"); g != w {
			t.Errorf("%s: Content-Type %q, Node %q", label, g, w)
		}
	}
}

func firstDifference(got, want string) string {
	i := 0
	for i < len(got) && i < len(want) && got[i] == want[i] {
		i++
	}
	from := max(0, i-60)
	return "differs at byte " + numberString(float64(i)) + ": got ..." + short(got[from:min(len(got), i+120)]) + "\n want ..." + short(want[from:min(len(want), i+120)])
}

// The same routes on the real language files: the whole answers as hashes.
func TestPanelRoutesOnRealFilesLikeNode(t *testing.T) {
	cases := list(fixedVectors(t, "panel-real.json"))
	if len(cases) < 20 {
		t.Fatalf("only %d vectors", len(cases))
	}
	// A copy, so that the lookup is case-sensitive also where the repository lies on a file system that is not.
	h, _, _ := handlersFor(t, copyDir(t, realLangDir), time.Now())
	srv := httptest.NewServer(routes(h))
	defer srv.Close()
	for _, c := range cases {
		method, path := str(c, "method"), str(c, "path")
		if goMuxRedirects(path) {
			continue
		}
		got := do(t, srv.URL, method, path, nil, nil, 0)
		if got.status != int(num(c, "status")) || sha(got.body) != str(c, "bodySha") {
			t.Errorf("%s %s: status %d, Node %v, or the body differs: %s", method, path, got.status, num(c, "status"), short(got.body))
		}
		if g, w := got.header.Get("ETag"), str(c, "etag"); g != w {
			t.Errorf("%s %s: ETag %q, Node %q", method, path, g, w)
		}
	}
}

// panelAnswers sends the overview and the missing keys of one language to
// handlers on dir.
func panelAnswers(t testing.TB, dir, code string) (overview, missing answer) {
	t.Helper()
	h, _, _ := handlersFor(t, dir, time.Now())
	srv := httptest.NewServer(routes(h))
	defer srv.Close()
	return do(t, srv.URL, "GET", "/api/panel/languages", nil, nil, 0), do(t, srv.URL, "GET", "/api/panel/languages/"+code+"/missing", nil, nil, 0)
}

// A language file with a byte order mark reads on the translations page as
// it does without one, as the loader reads it (BUGS.md L12).
func TestPanelLanguagesReadsFileWithByteOrderMark(t *testing.T) {
	content := `{"meta":{"name":"French","native_name":"Français"},"a":"A-fr","extra":"x"}`
	var answers [2][2]answer
	for i, text := range []string{content, byteOrderMark + content} {
		dir := copyDir(t, "testdata/lang-upload")
		if err := os.WriteFile(filepath.Join(dir, "fr.json"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		answers[i][0], answers[i][1] = panelAnswers(t, dir, "fr")
	}
	plain, marked := answers[0], answers[1]
	for i, what := range []string{"overview", "missing keys"} {
		if marked[i].status != 200 || marked[i].status != plain[i].status || marked[i].body != plain[i].body {
			t.Errorf("%s with the mark: %d %s\nwithout: %d %s", what, marked[i].status, short(marked[i].body), plain[i].status, short(plain[i].body))
		}
	}
}

// panelRows are the rows of an overview by code, as JSON.
func panelRows(t testing.TB, body string) (rows map[string]string, total float64) {
	t.Helper()
	rows = map[string]string{}
	answer := mustParse(t, body)
	for _, row := range list(answer, "languages") {
		rows[str(row, "code")] = stringify(row, "")
	}
	return rows, num(answer, "refKeyCount")
}

// One broken file no longer takes the whole overview down: a file that
// holds null gets a row with its error as other broken files do, and a
// broken reference file (en.json, pl.json) gets one too while the others
// are counted against the reference that loads. The missing keys are
// counted against it as well (BUGS.md L13).
func TestPanelLanguagesKeepsTheListWithABrokenFile(t *testing.T) {
	for _, c := range []struct{ file, content, error string }{
		{"fr.json", "null", "Cannot convert undefined or null to object"},
		{"pl.json", "null", "Cannot convert undefined or null to object"},
		{"pl.json", `{"a": 1,}`, ""},
		{"en.json", "null", "Cannot convert undefined or null to object"},
		{"en.json", `{"a"`, ""},
	} {
		name := c.file + " " + c.content
		broken := copyDir(t, "testdata/lang-upload")
		if err := os.WriteFile(filepath.Join(broken, c.file), []byte(c.content), 0o644); err != nil {
			t.Fatal(err)
		}
		without := copyDir(t, "testdata/lang-upload")
		if err := os.Remove(filepath.Join(without, c.file)); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		gotOverview, gotMissing := panelAnswers(t, broken, "de")
		wantOverview, wantMissing := panelAnswers(t, without, "de")
		if gotOverview.status != 200 || gotMissing.status != 200 {
			t.Errorf("%s: overview %d %s, missing keys %d %s", name, gotOverview.status, gotOverview.body, gotMissing.status, gotMissing.body)
			continue
		}
		if gotMissing.body != wantMissing.body {
			t.Errorf("%s: missing keys\n got  %s\n want %s", name, short(gotMissing.body), short(wantMissing.body))
		}
		got, gotTotal := panelRows(t, gotOverview.body)
		want, wantTotal := panelRows(t, wantOverview.body)
		if gotTotal != wantTotal || gotTotal == 0 {
			t.Errorf("%s: %v reference keys, %v without the file", name, gotTotal, wantTotal)
		}
		code := strings.TrimSuffix(c.file, ".json")
		for other, row := range want {
			if got[other] != row {
				t.Errorf("%s: the row of %s\n got  %s\n want %s", name, other, got[other], row)
			}
		}
		row := mustParse(t, got[code])
		if message := str(row, "error"); message == "" || (c.error != "" && message != c.error) {
			t.Errorf("%s: the file's row %s", name, got[code])
		}
		if flag(row, "is_reference") != (code == "en" || code == "pl") {
			t.Errorf("%s: is_reference in %s", name, got[code])
		}
	}
}

// The missing keys of a code that is not a language code are refused with
// 400; the code is no longer cut down to its letters (BUGS.md L14).
func TestPanelMissingRefusesCodeWithOtherCharacters(t *testing.T) {
	h, _, _ := handlersFor(t, copyDir(t, "testdata/lang-upload"), time.Now())
	srv := httptest.NewServer(routes(h))
	defer srv.Close()
	for _, code := range []string{"e1n", "d.e", "123", "-", "x_x", "zh_TW", "de.json", "EN", "%2e%2e%2fen", "..%2F..%2Fpackage", "abcdefghi", "en-us"} {
		got := do(t, srv.URL, "GET", "/api/panel/languages/"+code+"/missing", nil, nil, 0)
		if got.status != 400 || got.body != `{"error":"Invalid language code"}` {
			t.Errorf("%s: %d %s", code, got.status, got.body)
		}
	}
	if got := do(t, srv.URL, "GET", "/api/panel/languages/fr/missing", nil, nil, 0); got.status != 404 {
		t.Errorf("fr: %d %s", got.status, got.body)
	}
	if got := do(t, srv.URL, "GET", "/api/panel/languages/de/missing", nil, nil, 0); got.status != 200 || str(mustParse(t, got.body), "code") != "de" {
		t.Errorf("de: %d %s", got.status, got.body)
	}
}

// Deleting a language that has no file and is not loaded answers 404 and
// writes no audit entry; a loaded language whose file is gone is still
// forgotten (BUGS.md L18).
func TestDeleteUnknownLanguageIsNotFound(t *testing.T) {
	dir := copyDir(t, "testdata/lang-upload")
	h, rec, _ := handlersFor(t, dir, time.Now())
	srv := httptest.NewServer(routes(h))
	defer srv.Close()
	for _, code := range []string{"fr", "yy", "abcdefgh", "en-US"} {
		got := do(t, srv.URL, "DELETE", "/api/i18n/"+code, nil, nil, 0)
		if got.status != 404 || got.body != `{"success":false,"error":"Language not found"}` {
			t.Errorf("%s: %d %s", code, got.status, got.body)
		}
	}
	if len(rec.audit) != 0 {
		t.Errorf("audit entries for languages that do not exist: %v", rec.audit)
	}
	if err := os.Remove(filepath.Join(dir, "de.json")); err != nil {
		t.Fatal(err)
	}
	if got := do(t, srv.URL, "DELETE", "/api/i18n/de", nil, nil, 0); got.status != 200 || h.Bundle.Has("de") {
		t.Errorf("de, loaded without a file: %d %s", got.status, got.body)
	}
	if !reflect.DeepEqual(rec.audit, [][2]string{{"language_deleted", "Language de deleted"}}) {
		t.Errorf("audit %v", rec.audit)
	}
}
