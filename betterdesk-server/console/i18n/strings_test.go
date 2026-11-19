package i18n

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func stringsServer(t *testing.T) (*Handlers, *httptest.Server) {
	t.Helper()
	h, _, _ := handlersFor(t, copyDir(t, "testdata/lang-edge"), time.Now())
	srv := httptest.NewServer(routes(h))
	t.Cleanup(srv.Close)
	return h, srv
}

func TestStringsBodyAndCaching(t *testing.T) {
	h, srv := stringsServer(t)
	want := stringify(h.Bundle.Merged("de"), "")

	got := do(t, srv.URL, "GET", "/api/console/strings/de", nil, nil, 0)
	if got.status != 200 || got.body != want {
		t.Fatalf("status %d, body %s", got.status, short(got.body))
	}
	etag := got.header.Get("ETag")
	if !regexp.MustCompile(`^"[0-9a-f]{16}"$`).MatchString(etag) {
		t.Errorf("ETag %q, want 16 hex digits in quotes", etag)
	}
	revision := strings.Trim(etag, `"`)
	if revision != sha(want)[:16] || h.StringsRevision("de") != revision {
		t.Errorf("revision %q is not the start of the body's SHA-256 %q", revision, sha(want)[:16])
	}
	if got := h.StringsURL("de"); got != "/api/console/strings/de?v="+revision {
		t.Errorf("StringsURL = %q", got)
	}
	checks := map[string]string{
		"Content-Type":     "application/json; charset=utf-8",
		"Cache-Control":    "no-cache",
		"Vary":             "Accept-Encoding",
		"Content-Encoding": "",
	}
	for name, w := range checks {
		if g := got.header.Get(name); g != w {
			t.Errorf("%s: %q, want %q", name, g, w)
		}
	}
	if g := got.header.Get("Content-Length"); g != numberString(float64(len(want))) {
		t.Errorf("Content-Length %q for %d bytes", g, len(want))
	}

	cache := map[string]string{
		"?v=" + revision:                    "public, max-age=31536000, immutable",
		"?v=old":                            "no-cache",
		"?v=":                               "no-cache",
		"?x=1&v=" + revision:                "public, max-age=31536000, immutable",
		"?v=" + revision + "&v=" + revision: "no-cache",
		"?v[]=" + revision:                  "no-cache",
		"?V=" + revision:                    "no-cache",
	}
	for query, w := range cache {
		res := do(t, srv.URL, "GET", "/api/console/strings/de"+query, nil, nil, 0)
		if g := res.header.Get("Cache-Control"); res.status != 200 || g != w {
			t.Errorf("%s: status %d, Cache-Control %q, want %q", query, res.status, g, w)
		}
	}
}

func TestStringsUnknownCodes(t *testing.T) {
	_, srv := stringsServer(t)
	// fr.json holds null, so fr is no language either
	for _, code := range []string{"xx", "EN", "e", "abcd", "zh-tw", "zh-TWxyz", "constructor", "__proto__", "..%2Fconfig", "pl", "fr", "fr%00", "deu", "de.json"} {
		got := do(t, srv.URL, "GET", "/api/console/strings/"+code, nil, nil, 0)
		if got.status != 404 || got.body != `{"success":false,"error":"Language not found"}` {
			t.Errorf("%s: %d %s", code, got.status, short(got.body))
		}
		if got.header.Get("Cache-Control") != "" || got.header.Get("Vary") != "" {
			t.Errorf("%s: the 404 carries the caching headers of the strings", code)
		}
	}
	if got := do(t, srv.URL, "GET", "/api/console/strings/zh-TW", nil, nil, 0); got.status != 200 {
		t.Errorf("zh-TW: %d", got.status)
	}
}

func TestStringsNotModified(t *testing.T) {
	h, srv := stringsServer(t)
	first := do(t, srv.URL, "GET", "/api/console/strings/de", nil, nil, 0)
	etag := first.header.Get("ETag")
	cases := []struct {
		name            string
		headers         map[string]string
		status          int
		contentType     string
		contentEncoding string
	}{
		{"the exact tag", map[string]string{"If-None-Match": etag}, 304, "application/json; charset=utf-8", ""},
		{"the exact tag with gzip offered", map[string]string{"If-None-Match": etag, "Accept-Encoding": "gzip"}, 304, "application/json; charset=utf-8", ""},
		{"the exact tag although the client says no-cache", map[string]string{"If-None-Match": etag, "Cache-Control": "no-cache"}, 304, "application/json; charset=utf-8", ""},
		{"the weak form", map[string]string{"If-None-Match": "W/" + etag}, 304, "", ""},
		{"the weak form with gzip offered", map[string]string{"If-None-Match": "W/" + etag, "Accept-Encoding": "gzip"}, 304, "", "gzip"},
		{"a list", map[string]string{"If-None-Match": `"other", ` + etag}, 304, "", ""},
		{"a star", map[string]string{"If-None-Match": "*"}, 304, "", ""},
		{"the weak form with no-cache", map[string]string{"If-None-Match": "W/" + etag, "Cache-Control": "no-cache"}, 200, "application/json; charset=utf-8", ""},
		{"another tag", map[string]string{"If-None-Match": `"0000000000000000"`}, 200, "application/json; charset=utf-8", ""},
	}
	for _, c := range cases {
		got := do(t, srv.URL, "GET", "/api/console/strings/de", c.headers, nil, 0)
		if got.status != c.status {
			t.Errorf("%s: status %d, want %d", c.name, got.status, c.status)
			continue
		}
		// Go's HTTP server drops Content-Type from every 304, so what the
		// handler itself sets is read from a recorder.
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/console/strings/de", nil)
		req.SetPathValue("code", "de")
		for k, v := range c.headers {
			req.Header.Set(k, v)
		}
		h.Strings(rec, req)
		if g := rec.Header().Get("Content-Type"); rec.Code != c.status || g != c.contentType {
			t.Errorf("%s: the handler set status %d and Content-Type %q, want %d and %q", c.name, rec.Code, g, c.status, c.contentType)
		}
		if c.status == 304 && got.header.Get("Content-Type") != "" {
			t.Errorf("%s: Content-Type %q on a 304 from the server", c.name, got.header.Get("Content-Type"))
		}
		if g := got.header.Get("Content-Encoding"); g != c.contentEncoding {
			t.Errorf("%s: Content-Encoding %q, want %q", c.name, g, c.contentEncoding)
		}
		if c.status == 304 && (got.body != "" || got.header.Get("ETag") != etag || got.header.Get("Vary") != "Accept-Encoding" || got.header.Get("Cache-Control") != "no-cache") {
			t.Errorf("%s: 304 with body %q and headers %v", c.name, got.body, got.header)
		}
	}
}

func TestStringsCompression(t *testing.T) {
	_, srv := stringsServer(t)
	plain := do(t, srv.URL, "GET", "/api/console/strings/de", nil, nil, 0)
	cases := map[string]string{
		"gzip":              "gzip",
		"gzip, deflate, br": "gzip",
		"deflate, gzip;q=0": "gzip", // the old code looked for the word only
		"x-gzip":            "gzip",
		"GZIP":              "",
		"br":                "", // Node sent brotli here
		"identity":          "",
		"gzipped":           "",
		"":                  "",
	}
	for accept, want := range cases {
		headers := map[string]string{}
		if accept != "" {
			headers["Accept-Encoding"] = accept
		}
		got := do(t, srv.URL, "GET", "/api/console/strings/de", headers, nil, 0)
		if g := got.header.Get("Content-Encoding"); g != want {
			t.Errorf("Accept-Encoding %q: Content-Encoding %q, want %q", accept, g, want)
		}
		if got.status != 200 || got.body != plain.body {
			t.Errorf("Accept-Encoding %q: status %d, the decoded body differs: %v", accept, got.status, got.body != plain.body)
		}
		if got.header.Get("ETag") != plain.header.Get("ETag") {
			t.Errorf("Accept-Encoding %q: ETag %q", accept, got.header.Get("ETag"))
		}
	}
	head := do(t, srv.URL, "HEAD", "/api/console/strings/de", map[string]string{"Accept-Encoding": "gzip"}, nil, 0)
	if head.status != 200 || head.body != "" || head.header.Get("Content-Encoding") != "gzip" || head.header.Get("Content-Length") == "" {
		t.Errorf("HEAD: %d, body %q, headers %v", head.status, head.body, head.header)
	}
}

// After an upload the strings endpoint sends the new texts under a new
// revision, also for the languages the uploaded default language fills;
// a language nothing changed keeps its strings, and a deleted one is gone
// (BUGS.md L6).
func TestStringsAfterUploadHaveTheNewRevision(t *testing.T) {
	h, _, _ := handlersFor(t, copyDir(t, "testdata/lang-upload"), time.Now())
	srv := httptest.NewServer(routes(h))
	defer srv.Close()
	get := func(code string) answer { return do(t, srv.URL, "GET", "/api/console/strings/"+code, nil, nil, 0) }
	upload := func(name, content string) {
		t.Helper()
		form := "--B\r\nContent-Disposition: form-data; name=\"file\"; filename=\"" + name + "\"\r\nContent-Type: application/json\r\n\r\n" + content + "\r\n--B--\r\n"
		res := do(t, srv.URL, "POST", "/api/i18n/upload", map[string]string{"Content-Type": "multipart/form-data; boundary=B"}, strings.NewReader(form), int64(len(form)))
		if res.status != 200 {
			t.Fatalf("upload %s: %d %s", name, res.status, res.body)
		}
	}

	de, pl := get("de"), get("pl")
	revision := h.StringsRevision("de")
	upload("de.json", `{"a":"Neu"}`)
	again := get("de")
	if !strings.Contains(again.body, `"a":"Neu"`) || again.header.Get("ETag") == de.header.Get("ETag") {
		t.Errorf("de after the upload: %s, ETag %s", short(again.body), again.header.Get("ETag"))
	}
	if h.StringsRevision("de") == revision || h.StringsURL("de") != "/api/console/strings/de?v="+h.StringsRevision("de") {
		t.Errorf("the revision stayed %s after the upload", revision)
	}
	if got := get("pl"); got.body != pl.body || got.header.Get("ETag") != pl.header.Get("ETag") {
		t.Error("pl changed although nothing it takes changed")
	}

	upload("en.json", `{"a":"A","b":{"c":"C2"}}`)
	if got := get("pl"); !strings.Contains(got.body, `"c":"C2"`) || got.header.Get("ETag") == pl.header.Get("ETag") {
		t.Errorf("pl after an upload of the default language: %s", short(got.body))
	}

	if res := do(t, srv.URL, "DELETE", "/api/i18n/de", nil, nil, 0); res.status != 200 {
		t.Fatalf("delete: %d %s", res.status, res.body)
	}
	if got := get("de"); got.status != 404 {
		t.Errorf("de after the delete: %d", got.status)
	}
}

func TestLanguageSetsTheCookieForTheQuery(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	h, _, _ := handlersFor(t, copyDir(t, "testdata/lang-upload"), now)
	h.SecureCookie = true
	h.PreferredLanguage = func(r *http.Request) string { return r.Header.Get("X-Pref") }
	cases := []struct {
		query, pref, cookie, accept string
		lang, setCookie             string
	}{
		{"", "", "", "", "en", ""},
		{"lang=de", "pl", "betterdesk_lang=en", "pl", "de", "betterdesk_lang=de; Max-Age=31536000; Path=/; Expires=Wed, 10 Nov 2027 12:00:00 GMT; SameSite=Lax"},
		{"lang=xx", "pl", "", "", "pl", ""},
		{"", "pl", "betterdesk_lang=de", "de", "pl", ""},
		{"", "", "betterdesk_lang=de", "pl", "de", ""},
		{"", "", "", "pl;q=0.5, de;q=0.8", "de", ""},
		{"lang[]=de", "", "", "", "en", ""},
		{"lang[toString]=de", "", "", "", "en", ""},
	}
	for _, c := range cases {
		r := &http.Request{URL: &url.URL{Path: "/", RawQuery: c.query}, Header: http.Header{}}
		for name, value := range map[string]string{"X-Pref": c.pref, "Cookie": c.cookie, "Accept-Language": c.accept} {
			if value != "" {
				r.Header.Set(name, value)
			}
		}
		w := httptest.NewRecorder()
		lang, err := h.Language(w, r)
		if lang != c.lang || err != nil {
			t.Errorf("%q: language %q, error %v; want %q", c.query, lang, err, c.lang)
		}
		if got := w.Header().Get("Set-Cookie"); got != c.setCookie {
			t.Errorf("%q: Set-Cookie %q, want %q", c.query, got, c.setCookie)
		}
	}
}

func TestLanguagesAnswers500WhenANameIsNotText(t *testing.T) {
	dir := copyDir(t, "testdata/lang-upload")
	if err := os.WriteFile(filepath.Join(dir, "de.json"), []byte(`{"_meta":{"native":{"a":1}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	h, _, l := handlersFor(t, dir, time.Now())
	l.take()
	w := httptest.NewRecorder()
	h.Languages(w, httptest.NewRequest("GET", "/api/i18n/languages", nil))
	if w.Code != 500 || w.Body.String() != `{"success":false,"error":"Failed to get languages"}` {
		t.Errorf("%d %s", w.Code, w.Body.String())
	}
	if lines := l.take(); len(lines) != 1 || !strings.HasPrefix(lines[0], "Get languages error:") {
		t.Errorf("log lines %q", lines)
	}
}

// A multipart request without a body (no Content-Length, no
// Transfer-Encoding) never reaches the form parser.
func TestUploadWithoutABody(t *testing.T) {
	h, _, _ := handlersFor(t, copyDir(t, "testdata/lang-upload"), time.Now())
	srv := httptest.NewServer(routes(h))
	defer srv.Close()
	send := func(extra string) string {
		conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("POST /api/i18n/upload HTTP/1.1\r\nHost: x\r\nConnection: close\r\nContent-Type: multipart/form-data; boundary=B\r\n" + extra + "\r\n"))
		res, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		buf := new(strings.Builder)
		_, _ = bufio.NewReader(res.Body).WriteTo(buf)
		return res.Status[:3] + " " + buf.String()
	}
	if got := send(""); got != `400 {"success":false,"error":"No file uploaded"}` {
		t.Errorf("no length: %s", got)
	}
	if got := send("Content-Length: 0\r\n"); got != `500 {"success":false,"error":"Unexpected end of form"}` {
		t.Errorf("length 0: %s", got)
	}
}

func TestUploadHidesParserMessagesInProduction(t *testing.T) {
	h, _, l := handlersFor(t, copyDir(t, "testdata/lang-upload"), time.Now())
	body := "--B\r\nContent-Disposition: form-data; name=\"other\"; filename=\"de.json\"\r\nContent-Type: application/json\r\n\r\n{}\r\n--B--\r\n"
	for production, want := range map[bool]string{false: "Unexpected file field", true: "Internal Server Error"} {
		h.Production = production
		l.take()
		r := httptest.NewRequest("POST", "/api/i18n/upload", strings.NewReader(body))
		r.Header.Set("Content-Type", "multipart/form-data; boundary=B")
		r.Header.Set("Content-Length", numberString(float64(len(body))))
		w := httptest.NewRecorder()
		h.Upload(w, r)
		if w.Code != 500 || w.Body.String() != `{"success":false,"error":"`+want+`"}` {
			t.Errorf("production %v: %d %s", production, w.Code, w.Body.String())
		}
		if lines := l.take(); len(lines) != 1 || lines[0] != "Server error: Unexpected file field" {
			t.Errorf("production %v: log lines %q", production, lines)
		}
	}
}

// Name order as Node's localeCompare with sensitivity "base" gives it.
func TestSortByBaseLettersLikeNode(t *testing.T) {
	want := vectors(t, "collate.json")
	sorts := list(want, "sorts")
	if len(sorts) < 50 {
		t.Fatalf("only %d vectors", len(sorts))
	}
	for i, s := range sorts {
		input := strs(s, "input")
		entries := make([]*Object, len(input))
		for j := range input {
			entries[j] = obj("pos", float64(j))
		}
		sortByBaseLetters(entries, input)
		for j, e := range entries {
			if num(e, "pos") != num(s, "order", numberString(float64(j))) {
				t.Errorf("sort %d (%q): position %d holds input %v, Node has %v", i, input, j, num(e, "pos"), num(s, "order", numberString(float64(j))))
				break
			}
		}
	}
}
