package webui

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

const (
	csrfHint     = " If you recently disabled SSL, clear your browser cookies for this site and reload."
	csrfPageText = "Invalid or missing CSRF token. Please refresh the page and try again."
	csrfAPIText  = "Invalid CSRF token. Please refresh the page and try again."
)

// marked finds the error an app page is marked with.
func marked(a answer) string {
	_, rest, ok := strings.Cut(a.body, `<meta name="bd-error" content="`)
	if !ok {
		return ""
	}
	code, _, _ := strings.Cut(rest, `"`)
	return code
}

func errorPage(h *Host, method, target string, kind int, o ErrorOptions) (answer, bool) {
	r := rawRequest(method, target, nil)
	ok := false
	a := record(func(w http.ResponseWriter) { ok = h.ErrorPage(w, r, kind, o) })
	return a, ok
}

func noBuild(t *testing.T, production bool) *Host {
	t.Helper()
	return New(Options{Dir: filepath.Join(t.TempDir(), "missing"), Production: production, Session: sessionFromHeader})
}

func TestErrorPageMarksTheErrorInThePage(t *testing.T) {
	bases(t, func(t *testing.T, h *Host, base, dir string) {
		a, ok := errorPage(h, "POST", base+"/x/missing", 404, ErrorOptions{})
		if !ok || a.status != 404 {
			t.Fatalf("answered %v with %d", ok, a.status)
		}
		want := strings.Replace(readSite(t, dir, "index.html"), "<head>", `<head><meta name="bd-error" content="404">`, 1)
		if a.body != want {
			t.Errorf("body %q", clip(a.body))
		}
		if a.get("Content-Type") != "text/html; charset=utf-8" || a.get("Cache-Control") != "no-store" || a.get("Content-Security-Policy") != sitePolicy || a.get("ETag") == "" {
			t.Errorf("headers %v", a.header)
		}
	})
}

func TestErrorPageKeepsItsWordsEscapedAndItsStatus(t *testing.T) {
	bases(t, func(t *testing.T, h *Host, base, _ string) {
		a, _ := errorPage(h, "POST", base+"/x/guest", 403, ErrorOptions{Title: "Link expired", Message: `The guest link "x" <b>ended</b> & isn't back`})
		if a.status != 403 || !strings.Contains(a.body,
			`<head><meta name="bd-error" content="403" data-title="Link expired" data-message="The guest link &quot;x&quot; &lt;b&gt;ended&lt;/b&gt; &amp; isn&#39;t back">`) {
			t.Errorf("403 with words: %d %q", a.status, clip(a.body))
		}
		// The status may differ from the kind
		for _, c := range []struct {
			kind, status, wantStatus int
			wantMark                 string
		}{
			{404, 400, 400, "404"}, {500, 503, 503, "500"}, {403, 0, 403, "403"}, {500, 0, 500, "500"}, {418, 0, 418, "500"}, {401, 0, 401, "500"}, {404, 200, 200, "404"},
		} {
			a, ok := errorPage(h, "GET", base+"/x", c.kind, ErrorOptions{Status: c.status})
			if !ok || a.status != c.wantStatus || marked(a) != c.wantMark {
				t.Errorf("kind %d with status %d: %d marked %q, want %d marked %q", c.kind, c.status, a.status, marked(a), c.wantStatus, c.wantMark)
			}
			if strings.Contains(a.body, "data-title") || strings.Contains(a.body, "data-message") {
				t.Errorf("kind %d: words nobody gave: %q", c.kind, clip(a.body))
			}
		}
		// Not a status: a failure of the caller, shown as one
		a, ok := errorPage(h, "GET", base+"/x", 0, ErrorOptions{Message: "x"})
		if !ok || a.status != 500 || marked(a) != "500" || strings.Contains(a.body, "data-message") {
			t.Errorf("kind 0: %d marked %q", a.status, marked(a))
		}
	})
}

func TestErrorPageDeclinesOutsideTheBase(t *testing.T) {
	h, _ := testHost(t, "/app2")
	for _, target := range []string{"/x/old-page", "/", "/app2x", "/APP2/x", "/api/x"} {
		a, ok := errorPage(h, "POST", target, 404, ErrorOptions{})
		if ok || a.body != "" || len(a.header) != 0 {
			t.Errorf("%s: answered %v, body %q, headers %v", target, ok, a.body, a.header)
		}
	}
	for _, target := range []string{"/app2", "/app2/", "/app2/x/y?z"} {
		if a, ok := errorPage(h, "POST", target, 404, ErrorOptions{}); !ok || marked(a) != "404" {
			t.Errorf("%s: answered %v", target, ok)
		}
	}
}

func TestErrorPageAtTheRootForEveryAddress(t *testing.T) {
	h, _ := testHost(t, "")
	for _, target := range []string{"/x/anything", "/", "/api/x", "/app2/x"} {
		if a, ok := errorPage(h, "POST", target, 404, ErrorOptions{}); !ok || a.status != 404 || marked(a) != "404" {
			t.Errorf("%s: answered %v with %d", target, ok, a.status)
		}
	}
}

func TestErrorPageDeclinesWithoutABuild(t *testing.T) {
	h := noBuild(t, true)
	a, ok := errorPage(h, "GET", "/app/x", 404, ErrorOptions{})
	if ok || a.body != "" || len(a.header) != 0 {
		t.Errorf("answered %v, body %q, headers %v", ok, a.body, a.header)
	}
}

func TestErrorPageInAPageWithoutAHead(t *testing.T) {
	for html, want := range map[string]string{
		"<html><body>console app</body></html>":               "<html><body>console app</body></html>",
		`<html><HEAD lang="x"><title>t</title></HEAD></html>`: `<html><HEAD lang="x"><meta name="bd-error" content="404"><title>t</title></HEAD></html>`,
		"<html><header>h</header><head>\n</head></html>":      "<html><header>h</header><head><meta name=\"bd-error\" content=\"404\">\n</head></html>",
		"<html><head><head></head></html>":                    `<html><head><meta name="bd-error" content="404"><head></head></html>`,
		"<html><head data-x></head></html>":                   "<html><head data-x><meta name=\"bd-error\" content=\"404\"></head></html>",
	} {
		dir := copySite(t, "")
		writeFile(t, filepath.Join(dir, "index.html"), html)
		h := New(Options{Dir: dir, Production: true})
		if a, ok := errorPage(h, "GET", "/x", 404, ErrorOptions{}); !ok || a.body != want {
			t.Errorf("page %q: %q, want %q", html, a.body, want)
		}
	}
}

func TestNotFound(t *testing.T) {
	bases(t, func(t *testing.T, h *Host, base, _ string) {
		notFound := func(method, target string, headers map[string]string) answer {
			r := rawRequest(method, target, headers)
			return record(func(w http.ResponseWriter) { h.NotFound(w, r) })
		}
		const jsonBody = `{"success":false,"error":"Not Found"}`
		a := notFound("DELETE", base+"/devices", map[string]string{"Accept": browser})
		if a.status != 404 || marked(a) != "404" || a.get("Content-Security-Policy") != sitePolicy {
			t.Errorf("a browser: %d marked %q", a.status, marked(a))
		}
		a = notFound("POST", base+"/devices", nil)
		if a.status != 404 || marked(a) != "404" {
			t.Errorf("no Accept header counts as a browser: %d %q", a.status, clip(a.body))
		}
		for _, accept := range []string{"application/json", "text/plain", "image/png", "text/html;q=0, */*", "garbage"} {
			a = notFound("POST", base+"/devices", map[string]string{"Accept": accept})
			if a.status != 404 || a.body != jsonBody || a.get("Content-Type") != "application/json; charset=utf-8" || a.get("ETag") == "" {
				t.Errorf("Accept %q: %d %q (%s)", accept, a.status, a.body, a.get("Content-Type"))
			}
		}
		for _, accept := range []string{"*/*", "text/*", "text/html;q=0.1", "application/json, text/html;q=0.5"} {
			if a = notFound("POST", base+"/devices", map[string]string{"Accept": accept}); marked(a) != "404" {
				t.Errorf("Accept %q: no error page: %q", accept, clip(a.body))
			}
		}
		// The API and sockets get JSON whatever they accept
		for _, target := range []string{"/api/nothing", "/api", "/ws/nothing", "/WS", "/Api/x"} {
			a = notFound("GET", target, map[string]string{"Accept": browser})
			if a.status != 404 || a.body != jsonBody || marked(a) != "" {
				t.Errorf("%s: %d %q", target, a.status, clip(a.body))
			}
		}
		head := notFound("HEAD", base+"/missing.txt", map[string]string{"Accept": "application/json"})
		if head.status != 404 || head.body != "" || head.get("Content-Length") != "37" {
			t.Errorf("HEAD: %d %q, length %q", head.status, head.body, head.get("Content-Length"))
		}
	})
}

func TestNotFoundLogsUnmatchedAPIAndSocketPaths(t *testing.T) {
	h, _ := testHost(t, "")
	logged := captureLog(t)
	notFound := func(target string, headers map[string]string) {
		r := rawRequest("PUT", target, headers)
		record(func(w http.ResponseWriter) { h.NotFound(w, r) })
	}
	notFound("/api/nothing?x=1", map[string]string{"User-Agent": strings.Repeat("u", 79) + "END-and-more"})
	notFound("/ws/nothing", map[string]string{"X-Forwarded-For": "203.0.113.9, 10.0.0.1"})
	notFound("/missing.txt", nil)
	notFound("/api", nil)
	notFound("/apix/y", nil)
	notFound("/API/upper", nil)
	lines := strings.Split(strings.TrimSpace(logged.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("logged %d lines, want 2:\n%s", len(lines), logged.String())
	}
	if want := `[panel] 404 PUT /api/nothing?x=1 from 192.0.2.7 ua="` + strings.Repeat("u", 79) + `E"`; !strings.HasSuffix(lines[0], want) {
		t.Errorf("first line %q, want it to end with %q", lines[0], want)
	}
	if want := `[panel] 404 PUT /ws/nothing from 203.0.113.9, 10.0.0.1 ua=""`; !strings.HasSuffix(lines[1], want) {
		t.Errorf("second line %q, want it to end with %q", lines[1], want)
	}
}

func TestServerError(t *testing.T) {
	kaput := errors.New(`kaput <b>"x"</b>`)
	fail := func(h *Host, method, target string, headers map[string]string, err error, status int) answer {
		r := rawRequest(method, target, headers)
		return record(func(w http.ResponseWriter) { h.ServerError(w, r, err, status) })
	}
	bases(t, func(t *testing.T, h *Host, base, dir string) {
		// Production hides what went wrong
		a := fail(h, "GET", base+"/boom", map[string]string{"Accept": browser}, kaput, 0)
		if a.status != 500 || marked(a) != "500" || strings.Contains(a.body, "kaput") || strings.Contains(a.body, "data-message") {
			t.Errorf("a browser in production: %d %q", a.status, clip(a.body))
		}
		a = fail(h, "GET", "/api/boom", map[string]string{"Accept": browser}, kaput, 0)
		if a.status != 500 || a.body != `{"success":false,"error":"Internal Server Error"}` {
			t.Errorf("the API in production: %d %q", a.status, a.body)
		}
		a = fail(h, "POST", base+"/boom", map[string]string{"Accept": browser, "Content-Type": "application/json; charset=utf-8"}, kaput, 503)
		if a.status != 503 || a.body != `{"success":false,"error":"Internal Server Error"}` {
			t.Errorf("a request that sent JSON: %d %q", a.status, a.body)
		}
		a = fail(h, "GET", base+"/boom", map[string]string{"Accept": "text/plain"}, kaput, 502)
		if a.status != 502 || a.body != `{"success":false,"error":"Internal Server Error"}` {
			t.Errorf("a client that takes no HTML: %d %q", a.status, a.body)
		}
		a = fail(h, "GET", base+"/boom", map[string]string{"Accept": browser}, kaput, 503)
		if a.status != 503 || marked(a) != "500" {
			t.Errorf("a status of its own: %d marked %q", a.status, marked(a))
		}

		// Outside production the answer says what went wrong
		dev := New(Options{Dir: dir, Session: sessionFromHeader})
		a = fail(dev, "GET", base+"/boom", map[string]string{"Accept": browser}, kaput, 0)
		if a.status != 500 || !strings.Contains(a.body, `<meta name="bd-error" content="500" data-message="kaput &lt;b&gt;&quot;x&quot;&lt;/b&gt;">`) {
			t.Errorf("a browser outside production: %d %q", a.status, clip(a.body))
		}
		a = fail(dev, "GET", "/api/boom", nil, kaput, 0)
		if a.body != `{"success":false,"error":"kaput <b>\"x\"</b>"}` {
			t.Errorf("the API outside production: %q", a.body)
		}
		a = fail(dev, "GET", "/api/boom", nil, errors.New(""), 0)
		if a.body != `{"success":false,"error":"Internal Server Error"}` {
			t.Errorf("an error without a message: %q", a.body)
		}
		a = fail(dev, "GET", "/api/boom", nil, nil, 0)
		if a.status != 500 || a.body != `{"success":false,"error":"Internal Server Error"}` {
			t.Errorf("no error at all: %d %q", a.status, a.body)
		}
	})

	// Without a build: plain text with the same status
	a := fail(noBuild(t, true), "GET", "/boom", map[string]string{"Accept": browser}, kaput, 0)
	if a.status != 500 || a.body != "Internal Server Error" || a.get("Content-Type") != "text/plain; charset=utf-8" {
		t.Errorf("production without a build: %d %q (%s)", a.status, a.body, a.get("Content-Type"))
	}
	a = fail(noBuild(t, false), "GET", "/perm", map[string]string{"Accept": browser}, errors.New("Permission service unavailable"), 503)
	if a.status != 503 || a.body != "Permission service unavailable" {
		t.Errorf("outside production without a build: %d %q", a.status, a.body)
	}
}

func TestServerErrorIsLogged(t *testing.T) {
	h, _ := testHost(t, "")
	logged := captureLog(t)
	r := rawRequest("GET", "/boom", nil)
	record(func(w http.ResponseWriter) { h.ServerError(w, r, errors.New("kaput"), 0) })
	if !strings.Contains(logged.String(), "[panel] Server error: kaput") {
		t.Errorf("log: %q", logged.String())
	}
}

func TestCSRFError(t *testing.T) {
	csrf := func(h *Host, method, target string, headers map[string]string) answer {
		r := rawRequest(method, target, headers)
		return record(func(w http.ResponseWriter) { h.CSRFError(w, r) })
	}
	bases(t, func(t *testing.T, h *Host, base, _ string) {
		a := csrf(h, "POST", base+"/form", map[string]string{"Accept": browser})
		if a.status != 403 || marked(a) != "403" || !strings.Contains(a.body, `data-message="`+csrfPageText+csrfHint+`"`) ||
			a.get("Content-Security-Policy") != sitePolicy {
			t.Errorf("a refused form post: %d %q", a.status, clip(a.body))
		}
		a = csrf(h, "POST", "/api/form", map[string]string{"Accept": browser})
		if a.status != 403 || a.body != `{"success":false,"error":"`+csrfAPIText+csrfHint+`"}` {
			t.Errorf("the API: %d %q", a.status, a.body)
		}
		a = csrf(h, "POST", base+"/form", map[string]string{"Accept": browser, "Content-Type": "application/json"})
		if a.body != `{"success":false,"error":"`+csrfAPIText+csrfHint+`"}` {
			t.Errorf("a request that sent JSON: %q", a.body)
		}
		a = csrf(h, "POST", base+"/form", map[string]string{"Accept": "text/plain"})
		if a.status != 403 || a.body != `{"success":false,"error":"Invalid CSRF token`+csrfHint+`"}` {
			t.Errorf("a client that takes no HTML: %d %q", a.status, a.body)
		}
	})

	a := csrf(noBuild(t, true), "POST", "/form", map[string]string{"Accept": browser})
	if a.status != 403 || a.body != csrfPageText+csrfHint || a.get("Content-Type") != "text/plain; charset=utf-8" {
		t.Errorf("without a build: %d %q (%s)", a.status, a.body, a.get("Content-Type"))
	}

	// The hint is for a console without HTTPS that was reached without HTTPS
	dir := copySite(t, "")
	overHTTPS := New(Options{Dir: dir, Production: true, HTTPS: true})
	if a := csrf(overHTTPS, "POST", "/api/form", nil); a.body != `{"success":false,"error":"`+csrfAPIText+`"}` {
		t.Errorf("with HTTPS on: %q", a.body)
	}
	behindProxy := New(Options{Dir: dir, Production: true, Secure: func(r *http.Request) bool { return r.Header.Get("X-Forwarded-Proto") == "https" }})
	if a := csrf(behindProxy, "POST", "/api/form", map[string]string{"X-Forwarded-Proto": "https"}); a.body != `{"success":false,"error":"`+csrfAPIText+`"}` {
		t.Errorf("behind an HTTPS proxy: %q", a.body)
	}
	if a := csrf(behindProxy, "POST", "/api/form", nil); a.body != `{"success":false,"error":"`+csrfAPIText+csrfHint+`"}` {
		t.Errorf("behind the proxy over HTTP: %q", a.body)
	}
}

// Every error whose message names CSRF got the CSRF answer in the old
// console, and so it does here (BUGS.md W2).
func TestErrorThatNamesCSRFIsAnsweredAsOne(t *testing.T) {
	h, _ := testHost(t, "")
	for _, message := range []string{"invalid csrf token", "a CSRF thing"} {
		r := rawRequest("GET", "/api/x", nil)
		a := record(func(w http.ResponseWriter) { h.ServerError(w, r, errors.New(message), 500) })
		if a.status != 403 || !strings.Contains(a.body, csrfAPIText) {
			t.Errorf("%q: %d %q", message, a.status, a.body)
		}
	}
	r := rawRequest("GET", "/api/x", nil)
	if a := record(func(w http.ResponseWriter) { h.ServerError(w, r, errors.New("a Csrf thing"), 500) }); a.status != 500 {
		t.Errorf("another spelling: %d", a.status)
	}
	// A path under /app that does not decode and holds the word
	if a := serve(h, "GET", "/app/csrf%zz", map[string]string{"Accept": "application/json"}); a.status != 403 || !strings.Contains(a.body, "Invalid CSRF token") {
		t.Errorf("/app/csrf%%zz: %d %q", a.status, a.body)
	}
}
