package middleware

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/httpx"
)

func okHandler(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

func serve(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// The header sets seen on the wire from middleware/security.js
// (spec/auth-sessions.md 8.1).
var httpHeaders = map[string]string{
	"Content-Security-Policy":           "default-src 'self';script-src 'self';script-src-attr 'none';style-src 'self' 'unsafe-inline';font-src 'self';img-src 'self' data: blob:;media-src 'self' blob:;connect-src 'self' ws:;frame-src 'none';object-src 'none';worker-src 'self' blob:;base-uri 'self';form-action 'self';frame-ancestors 'none'",
	"Cross-Origin-Resource-Policy":      "same-origin",
	"Referrer-Policy":                   "strict-origin-when-cross-origin",
	"X-Content-Type-Options":            "nosniff",
	"X-Dns-Prefetch-Control":            "off",
	"X-Download-Options":                "noopen",
	"X-Frame-Options":                   "DENY",
	"X-Permitted-Cross-Domain-Policies": "none",
	"X-Xss-Protection":                  "0",
	"Permissions-Policy":                "geolocation=(), microphone=(self), camera=(), payment=(), usb=(), accelerometer=(), gyroscope=(), magnetometer=()",
	"Cache-Control":                     "no-store",
}

func TestSecurityHeadersHTTP(t *testing.T) {
	h := SecurityHeaders(SecurityOptions{})(http.HandlerFunc(okHandler))
	req := httptest.NewRequest("GET", "/api/x", nil)
	rec := httptest.NewRecorder()
	rec.Header().Set("X-Powered-By", "Express")
	h.ServeHTTP(rec, req)
	got := rec.Header()
	if len(got) != len(httpHeaders) {
		t.Errorf("%d headers, want %d: %v", len(got), len(httpHeaders), got)
	}
	for name, want := range httpHeaders {
		if got.Get(name) != want {
			t.Errorf("%s: %q, want %q", name, got.Get(name), want)
		}
	}
	for _, absent := range []string{"X-Powered-By", "Strict-Transport-Security", "Cross-Origin-Opener-Policy", "Origin-Agent-Cluster", "Cross-Origin-Embedder-Policy"} {
		if got.Get(absent) != "" {
			t.Errorf("%s is set: %q", absent, got.Get(absent))
		}
	}
	// HSTS never goes out without HTTPS
	rec = serve(SecurityHeaders(SecurityOptions{HSTS: true})(http.HandlerFunc(okHandler)), req)
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("Strict-Transport-Security in HTTP mode")
	}
}

func TestSecurityHeadersHTTPS(t *testing.T) {
	req := httptest.NewRequest("GET", "/api/x", nil)
	got := serve(SecurityHeaders(SecurityOptions{HTTPS: true, HSTS: true})(http.HandlerFunc(okHandler)), req).Header()
	want := map[string]string{}
	for name, value := range httpHeaders {
		want[name] = value
	}
	want["Content-Security-Policy"] = strings.Replace(httpHeaders["Content-Security-Policy"], "connect-src 'self' ws:", "connect-src 'self' wss:", 1) + ";upgrade-insecure-requests"
	want["Cross-Origin-Opener-Policy"] = "same-origin"
	want["Origin-Agent-Cluster"] = "?1"
	want["Strict-Transport-Security"] = "max-age=31536000; includeSubDomains"
	if len(got) != len(want) {
		t.Errorf("%d headers, want %d: %v", len(got), len(want), got)
	}
	for name, value := range want {
		if got.Get(name) != value {
			t.Errorf("%s: %q, want %q", name, got.Get(name), value)
		}
	}
	got = serve(SecurityHeaders(SecurityOptions{HTTPS: true})(http.HandlerFunc(okHandler)), req).Header()
	if got.Get("Strict-Transport-Security") != "" {
		t.Error("Strict-Transport-Security although HSTS is off")
	}
	if got.Get("Origin-Agent-Cluster") != "?1" {
		t.Error("the other HTTPS headers must not depend on HSTS")
	}
}

// tests/security.middleware.test.js: a strict policy on what the server
// answers, for the API and for pages alike.
func TestSecurityPolicyIsStrict(t *testing.T) {
	h := SecurityHeaders(SecurityOptions{})(http.HandlerFunc(okHandler))
	for _, p := range []string{"/api/x", "/remote/device-123"} {
		rec := serve(h, httptest.NewRequest("GET", p, nil))
		csp := rec.Header().Get("Content-Security-Policy")
		directives := map[string]string{}
		for _, part := range strings.Split(csp, ";") {
			name, value, _ := strings.Cut(part, " ")
			directives[name] = value
		}
		if directives["script-src"] != "'self'" || directives["script-src-attr"] != "'none'" {
			t.Errorf("%s: script-src %q, script-src-attr %q", p, directives["script-src"], directives["script-src-attr"])
		}
		for _, bad := range []string{"nonce-", "unsafe-eval", "jsdelivr", "googleapis", "gstatic"} {
			if strings.Contains(csp, bad) {
				t.Errorf("%s: policy has %s", p, bad)
			}
		}
		if directives["frame-ancestors"] != "'none'" || directives["object-src"] != "'none'" {
			t.Errorf("%s: frame-ancestors %q, object-src %q", p, directives["frame-ancestors"], directives["object-src"])
		}
		if rec.Header().Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s: X-Frame-Options %q", p, rec.Header().Get("X-Frame-Options"))
		}
	}
}

func TestHSTSEnabled(t *testing.T) {
	cases := []struct {
		https      bool
		env        string
		selfSigned bool
		want       bool
	}{
		{false, "", false, false},
		{false, "true", false, false},
		{true, "", false, true},
		// tests/security.middleware.test.js: no HSTS with self-signed certificates
		{true, "", true, false},
		{true, "true", true, true},
		{true, "1", true, true},
		{true, "ON", true, true},
		{true, "false", false, false},
		{true, "0", false, false},
		{true, "Off", false, false},
		{true, "yes", false, true},
		{true, "yes", true, false},
		{true, " true", true, false},
	}
	for _, c := range cases {
		if got := HSTSEnabled(c.https, c.env, c.selfSigned); got != c.want {
			t.Errorf("HSTSEnabled(%v, %q, %v) = %v, want %v", c.https, c.env, c.selfSigned, got, c.want)
		}
	}
}

func TestSecurityCacheControlByPath(t *testing.T) {
	h := SecurityHeaders(SecurityOptions{})(http.HandlerFunc(okHandler))
	for p, want := range map[string]string{
		"/":               "no-store",
		"/api/x":          "no-store",
		"/css/a.css":      "public, max-age=3600",
		"/img/x.png":      "public, max-age=3600",
		"/fonts/x.woff2":  "public, max-age=3600",
		"/CSS/a.css":      "no-store",
		"/cssx/a.css":     "no-store",
		"/img":            "no-store",
		"/x/img/y.png":    "no-store",
		"/fonts/?x=1":     "public, max-age=3600",
		"/a?x=/img/y.png": "no-store",
	} {
		if got := serve(h, httptest.NewRequest("GET", p, nil)).Header().Get("Cache-Control"); got != want {
			t.Errorf("%s: Cache-Control %q, want %q", p, got, want)
		}
	}
}

// spec/auth-sessions.md 8.3 and tests/security/05-csrf-cors-ssrf.test.js.
func TestCORS(t *testing.T) {
	reached := false
	h := CORS()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { reached = true }))
	allow := map[string]string{
		"Access-Control-Allow-Methods":     "GET, POST, PUT, PATCH, DELETE, OPTIONS",
		"Access-Control-Allow-Headers":     "Content-Type, Authorization, X-API-Key, X-CSRF-Token",
		"Access-Control-Allow-Credentials": "true",
		"Access-Control-Max-Age":           "86400",
	}
	for _, origin := range []string{"http://localhost:1420", "tauri://localhost", "https://tauri.localhost"} {
		for _, method := range []string{"GET", "OPTIONS"} {
			req := httptest.NewRequest(method, "/api/devices", nil)
			req.Header.Set("Origin", origin)
			reached = false
			rec := serve(h, req)
			if rec.Header().Get("Access-Control-Allow-Origin") != origin {
				t.Errorf("%s %s: Allow-Origin %q", method, origin, rec.Header().Get("Access-Control-Allow-Origin"))
			}
			for name, want := range allow {
				if rec.Header().Get(name) != want {
					t.Errorf("%s %s: %s = %q", method, origin, name, rec.Header().Get(name))
				}
			}
			if rec.Header().Get("Vary") != "" {
				t.Errorf("Vary is set: %q", rec.Header().Get("Vary"))
			}
			if method == "OPTIONS" && (rec.Code != 204 || reached || rec.Body.Len() != 0) {
				t.Errorf("OPTIONS %s: status %d, reached %v", origin, rec.Code, reached)
			}
			if method == "GET" && !reached {
				t.Errorf("GET %s did not reach the route", origin)
			}
		}
	}
	for _, origin := range []string{"https://evil.example", "http://localhost:1421", "HTTP://LOCALHOST:1420", "http://localhost:1420/", "null", ""} {
		req := httptest.NewRequest("OPTIONS", "/api/devices", nil)
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		rec := serve(h, req)
		if rec.Code != 204 {
			t.Errorf("OPTIONS from %q: status %d, want 204 for everyone", origin, rec.Code)
		}
		for name := range rec.Header() {
			if strings.HasPrefix(name, "Access-Control-") {
				t.Errorf("origin %q got %s", origin, name)
			}
		}
		if rec.Header().Get("ETag") != `W/"a-bAsFyilMr4Ra1hIU5PyoyFRunpI"` {
			t.Errorf("the 204 carries ETag %q", rec.Header().Get("ETag"))
		}
	}
	// Two Origin headers are no origin the console knows
	req := httptest.NewRequest("GET", "/api/x", nil)
	req.Header.Add("Origin", "tauri://localhost")
	req.Header.Add("Origin", "tauri://localhost")
	if got := serve(h, req).Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("two Origin headers: Allow-Origin %q", got)
	}
	// Outside /api/ nothing happens, OPTIONS included
	for _, p := range []string{"/x", "/apix", "/", "/api.json"} {
		req := httptest.NewRequest("OPTIONS", p, nil)
		req.Header.Set("Origin", "tauri://localhost")
		reached = false
		rec := serve(h, req)
		if !reached || rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("OPTIONS %s: reached %v, Allow-Origin %q", p, reached, rec.Header().Get("Access-Control-Allow-Origin"))
		}
	}
	for _, p := range []string{"/api", "/api/", "/API/x", "/api/x/y?z=1"} {
		req := httptest.NewRequest("OPTIONS", p, nil)
		if rec := serve(h, req); rec.Code != 204 {
			t.Errorf("OPTIONS %s: status %d, want 204", p, rec.Code)
		}
	}
}

func post(target, contentType, body string) *http.Request {
	req := httptest.NewRequest("POST", target, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("Content-Length", strconv.Itoa(len(body)))
	return req
}

// spec/auth-sessions.md 8.4 and tests/requireJsonContentType.test.js.
func TestRequireJSON(t *testing.T) {
	h := RequireJSON()(http.HandlerFunc(okHandler))
	multipart := "multipart/form-data; boundary=x"
	bare := func(method, target, contentType string) *http.Request {
		req := httptest.NewRequest(method, target, nil)
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		return req
	}
	withLength := func(req *http.Request) *http.Request {
		req.Header.Set("Content-Length", "0")
		return req
	}
	cases := []struct {
		name string
		req  *http.Request
		want int
	}{
		{"POST without body and type", bare("POST", "/api/x", ""), 415},
		{"POST with Content-Length 0 and no type", withLength(bare("POST", "/api/x", "")), 415},
		{"POST with the JSON type but no length", bare("POST", "/api/x", "application/json"), 415},
		{"POST with the JSON type and Content-Length 0", withLength(bare("POST", "/api/x", "application/json")), 200},
		{"charset parameter", post("/api/x", "application/json; charset=utf-8", "{}"), 200},
		{"vnd.api+json", post("/api/x", "application/vnd.api+json", "{}"), 415},
		{"json with a suffix", post("/api/x", "application/json+xml", "{}"), 415},
		{"upper case type", post("/api/x", "APPLICATION/JSON", "{}"), 200},
		{"broken parameter", post("/api/x", "application/json; charset", "{}"), 415},
		{"text/plain", post("/api/x", "text/plain", "{}"), 415},
		{"form", post("/api/x", "application/x-www-form-urlencoded", "a=1"), 415},
		{"PUT without type", bare("PUT", "/api/x", ""), 415},
		{"PATCH without type", bare("PATCH", "/api/x", ""), 415},
		{"DELETE", bare("DELETE", "/api/x", ""), 200},
		{"GET", bare("GET", "/api/x", ""), 200},
		{"HEAD", bare("HEAD", "/api/x", ""), 200},
		{"OPTIONS", bare("OPTIONS", "/api/x", ""), 200},
		{"not the API", post("/login", "application/x-www-form-urlencoded", "a=1"), 200},
		{"/api without slash", post("/api", "text/plain", "x"), 200},
		{"upper case is /api/ too", post("/API/x", "text/plain", "x"), 415},
		// tests/requireJsonContentType.test.js
		{"multipart restore", post("/api/settings/restore", multipart, "x"), 200},
		{"multipart module archive", post("/api/generator/module/install-local", multipart, "x"), 200},
		{"multipart to updates/restore", post("/api/settings/updates/restore", multipart, "x"), 415},
		{"unrelated POST without type", bare("POST", "/api/settings/email/smtp/test", ""), 415},
		{"JSON to updates/restore", post("/api/settings/updates/restore", "application/json", `{"backupName":"x"}`), 200},
		{"restore with a slash", post("/api/settings/restore/", multipart, "x"), 415},
		{"upload path", post("/api/chat/upload", multipart, "x"), 200},
		{"upload anywhere in the path", post("/api/x/uploads/y", "text/plain", "x"), 200},
		{"import path", post("/api/languages/import", multipart, "x"), 200},
		{"upload in the query only", post("/api/x?y=/upload", "text/plain", "x"), 415},
		{"Upload in upper case", post("/api/Upload", "text/plain", "x"), 200},
	}
	for _, c := range cases {
		rec := serve(h, c.req)
		if rec.Code != c.want {
			t.Errorf("%s: status %d, want %d", c.name, rec.Code, c.want)
		}
		if c.want == 415 {
			if rec.Body.String() != `{"success":false,"error":"Content-Type must be application/json"}` {
				t.Errorf("%s: body %s", c.name, rec.Body.String())
			}
			if rec.Header().Get("Content-Type") != "application/json; charset=utf-8" {
				t.Errorf("%s: Content-Type %q", c.name, rec.Header().Get("Content-Type"))
			}
		}
	}
}

// The router matches /API/x as /api/x, so the rule has to as well (M2).
func TestRequireJSONInAnyLetterCase(t *testing.T) {
	h := RequireJSON()(http.HandlerFunc(okHandler))
	for _, target := range []string{"/API/x", "/Api/devices", "/aPi/x/y", "/API/"} {
		for _, contentType := range []string{"text/plain", "application/x-www-form-urlencoded", ""} {
			if rec := serve(h, post(target, contentType, "a=1")); rec.Code != http.StatusUnsupportedMediaType {
				t.Errorf("POST %s as %q: status %d, want 415", target, contentType, rec.Code)
			}
		}
		if rec := serve(h, post(target, "application/json", "{}")); rec.Code != http.StatusOK {
			t.Errorf("POST %s as JSON: status %d, want 200", target, rec.Code)
		}
	}
	// The exemptions name the same routes in any letter case
	for _, target := range []string{"/API/chat/upload", "/api/CHAT/UPLOAD", "/Api/Settings/Restore", "/api/languages/IMPORT"} {
		if rec := serve(h, post(target, "multipart/form-data; boundary=x", "x")); rec.Code != http.StatusOK {
			t.Errorf("POST %s as multipart: status %d, want 200", target, rec.Code)
		}
	}
}

func TestNoStoreForNonFiles(t *testing.T) {
	h := NoStoreForNonFiles()(http.HandlerFunc(okHandler))
	const noStore = "no-cache, no-store, must-revalidate"
	fileLike := []string{".js", ".css", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".woff", ".woff2", ".ttf", ".eot", ".map", ".proto"}
	for _, ext := range fileLike {
		rec := httptest.NewRecorder()
		rec.Header().Set("Cache-Control", "before")
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/x/file"+ext+"?v=1.txt", nil))
		if got := rec.Header().Get("Cache-Control"); got != "before" {
			t.Errorf("%s: Cache-Control %q, want it left alone", ext, got)
		}
	}
	for _, p := range []string{"/", "/api/devices", "/a.json", "/a.html", "/a.JS", "/a.woff3", "/a.js/", "/a.txt?x=.js", "/js", "/a.jsx", "/a.webp", "/a.txt"} {
		rec := httptest.NewRecorder()
		rec.Header().Set("Cache-Control", "before")
		h.ServeHTTP(rec, httptest.NewRequest("GET", p, nil))
		if got := rec.Header().Get("Cache-Control"); got != noStore {
			t.Errorf("%s: Cache-Control %q, want %q", p, got, noStore)
		}
	}
}

// tests/jsonCompression.test.js, with gzip where Node preferred Brotli
// (decision A8).
func TestMarkAPICompression(t *testing.T) {
	type device struct {
		ID       string `json:"id"`
		Hostname string `json:"hostname"`
		Online   bool   `json:"online"`
	}
	var big struct {
		Devices []device `json:"devices"`
	}
	for i := 0; i < 400; i++ {
		big.Devices = append(big.Devices, device{ID: "10000000" + string(rune('0'+i%10)), Hostname: "host", Online: i%2 == 0})
	}
	plain, _ := httpx.Marshal(big)
	routes := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch httpx.RawPath(r) {
		case "/api/small":
			httpx.WriteJSON(w, r, 200, map[string]bool{"success": true})
		case "/api/missing":
			httpx.WriteJSON(w, r, 404, big)
		default:
			httpx.WriteJSON(w, r, 200, big)
		}
	})
	h := MarkAPICompression(func(r *http.Request) bool { return strings.HasPrefix(httpx.RawPath(r), "/api/bd/") })(routes)
	get := func(target, acceptEncoding string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", target, nil)
		if acceptEncoding != "" {
			req.Header.Set("Accept-Encoding", acceptEncoding)
		}
		return serve(h, req)
	}
	unzip := func(rec *httptest.ResponseRecorder) []byte {
		zr, err := gzip.NewReader(rec.Body)
		if err != nil {
			t.Fatalf("not gzip: %v", err)
		}
		out, _ := io.ReadAll(zr)
		return out
	}

	for _, accept := range []string{"gzip, deflate, br", "gzip", "br;q=0, gzip", "GZIP"} {
		rec := get("/api/big", accept)
		if rec.Header().Get("Content-Encoding") != "gzip" || !bytes.Equal(unzip(rec), plain) {
			t.Errorf("Accept-Encoding %q: Content-Encoding %q", accept, rec.Header().Get("Content-Encoding"))
		}
		if rec.Header().Get("Vary") != "Accept-Encoding" || rec.Header().Get("Content-Length") != "" {
			t.Errorf("Accept-Encoding %q: Vary %q, Content-Length %q", accept, rec.Header().Get("Vary"), rec.Header().Get("Content-Length"))
		}
	}
	for _, accept := range []string{"", "identity", "gzip;q=0", "deflate", "*"} {
		rec := get("/api/big", accept)
		if rec.Header().Get("Content-Encoding") != "" || !bytes.Equal(rec.Body.Bytes(), plain) {
			t.Errorf("Accept-Encoding %q: compressed although the client did not ask", accept)
		}
	}
	if rec := get("/api/small", "gzip"); rec.Header().Get("Content-Encoding") != "" || rec.Body.String() != `{"success":true}` {
		t.Errorf("small answer: Content-Encoding %q, body %s", rec.Header().Get("Content-Encoding"), rec.Body.String())
	}
	if rec := get("/api/missing", "gzip"); rec.Code != 404 || rec.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("status of a compressed answer: %d, Content-Encoding %q", rec.Code, rec.Header().Get("Content-Encoding"))
	}
	// An unchanged answer is a 304 for the ETag the browser has
	first := get("/api/big", "gzip")
	req := httptest.NewRequest("GET", "/api/big", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("If-None-Match", first.Header().Get("ETag"))
	if again := serve(h, req); again.Code != 304 || again.Body.Len() != 0 || first.Header().Get("ETag") != httpx.ETag(plain) {
		t.Errorf("revalidation: status %d, %d bytes, ETag %q", again.Code, again.Body.Len(), first.Header().Get("ETag"))
	}
	// Outside /api/, and for the routers that answer before the compression step
	for _, target := range []string{"/big", "/apix/big", "/api/bd/devices"} {
		if rec := get(target, "gzip"); rec.Header().Get("Content-Encoding") != "" {
			t.Errorf("%s was compressed", target)
		}
	}
	for _, target := range []string{"/API/big", "/api/bd", "/api/bdx/y"} {
		if rec := get(target, "gzip"); rec.Header().Get("Content-Encoding") != "gzip" {
			t.Errorf("%s was not compressed", target)
		}
	}
	// nil exempts nothing
	if rec := serve(MarkAPICompression(nil)(routes), func() *http.Request {
		r := httptest.NewRequest("GET", "/api/bd/devices", nil)
		r.Header.Set("Accept-Encoding", "gzip")
		return r
	}()); rec.Header().Get("Content-Encoding") != "gzip" {
		t.Error("without an exemption /api/bd is compressed like the rest")
	}
}

// bodyResult runs the body parsers on a request and reports what they
// did.
type bodyResult struct {
	next   bool
	status int
	err    *BodyError
	req    *http.Request
}

func runBodies(req *http.Request, mw func(http.Handler) http.Handler) bodyResult {
	var res bodyResult
	h := mw(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { res.next, res.req = true, r }))
	h.ServeHTTP(httptest.NewRecorder(), req)
	return res
}

func bodies(limit int64, res *bodyResult) func(http.Handler) http.Handler {
	return Bodies(BodyOptions{Limit: limit, OnError: func(_ http.ResponseWriter, _ *http.Request, status int, err error) {
		res.status = status
		errors.As(err, &res.err)
	}})
}

func parse(t *testing.T, req *http.Request, limit int64) bodyResult {
	t.Helper()
	var onErr bodyResult
	res := runBodies(req, bodies(limit, &onErr))
	res.status, res.err = onErr.status, onErr.err
	if res.next == (res.err != nil) {
		t.Fatalf("next called: %v, error: %v; exactly one must happen", res.next, res.err)
	}
	return res
}

func jsonRequest(body string) *http.Request {
	req := httptest.NewRequest("POST", "/api/x", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func TestJSONBodyErrors(t *testing.T) {
	gz := func(s string) string {
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		_, _ = zw.Write([]byte(s))
		_ = zw.Close()
		return buf.String()
	}
	with := func(req *http.Request, name, value string) *http.Request {
		req.Header.Set(name, value)
		return req
	}
	cases := []struct {
		name    string
		req     *http.Request
		status  int
		errType string
		message string
	}{
		{"malformed", jsonRequest(`{"a":`), 400, ErrTypeParseFailed, ""},
		{"trailing data", jsonRequest(`{"a":1} x`), 400, ErrTypeParseFailed, ""},
		{"two values", jsonRequest(`{}{}`), 400, ErrTypeParseFailed, ""},
		{"scalar number", jsonRequest(`1`), 400, ErrTypeParseFailed, "invalid JSON: the body has to be an object or an array"},
		{"scalar string", jsonRequest(`"s"`), 400, ErrTypeParseFailed, "invalid JSON: the body has to be an object or an array"},
		{"null", jsonRequest(`null`), 400, ErrTypeParseFailed, "invalid JSON: the body has to be an object or an array"},
		{"white space only", jsonRequest("  \n"), 400, ErrTypeParseFailed, "invalid JSON: the body has to be an object or an array"},
		{"two byte order marks", jsonRequest("\xEF\xBB\xBF\xEF\xBB\xBF{}"), 400, ErrTypeParseFailed, ""},
		{"too large", jsonRequest(`{"a":"` + strings.Repeat("x", 100) + `"}`), 413, ErrTypeTooLarge, "request entity too large"},
		{"too large after gzip", with(jsonRequest(gz(`{"a":"`+strings.Repeat("x", 100)+`"}`)), "Content-Encoding", "gzip"), 413, ErrTypeTooLarge, "request entity too large"},
		{"charset latin1", with(jsonRequest(`{}`), "Content-Type", "application/json; charset=latin1"), 415, ErrTypeCharsetUnsupported, `unsupported charset "LATIN1"`},
		{"charset utf8 without dash", with(jsonRequest(`{}`), "Content-Type", "application/json; charset=utf8"), 415, ErrTypeCharsetUnsupported, `unsupported charset "UTF8"`},
		{"charset utf-32", with(jsonRequest(`{}`), "Content-Type", "application/json; charset=utf-32"), 415, ErrTypeCharsetUnsupported, `unsupported charset "UTF-32"`},
		{"charset utf-7", with(jsonRequest(`{}`), "Content-Type", "application/json; charset=utf-7"), 415, ErrTypeCharsetUnsupported, `unsupported charset "UTF-7"`},
		{"encoding br", with(jsonRequest(`{}`), "Content-Encoding", "br"), 415, ErrTypeEncodingUnsupported, `unsupported content encoding "br"`},
		{"encoding list", with(jsonRequest(gz(`{}`)), "Content-Encoding", "gzip, deflate"), 415, ErrTypeEncodingUnsupported, `unsupported content encoding "gzip, deflate"`},
		{"not gzip", with(jsonRequest(`{}`), "Content-Encoding", "gzip"), 400, "", "incorrect header check"},
		{"not gzip, one byte", with(jsonRequest(`{`), "Content-Encoding", "gzip"), 400, "", "incorrect header check"},
		{"gzip magic only", with(jsonRequest("\x1f"), "Content-Encoding", "gzip"), 400, "", "unexpected end of file"},
		{"not deflate", with(jsonRequest(`{"a":1}`), "Content-Encoding", "deflate"), 400, "", "incorrect header check"},
		{"gzip cut short", with(jsonRequest(gz(`{"a":1}`)[:12]), "Content-Encoding", "gzip"), 400, "", "unexpected end of file"},
		{"gzip followed by other bytes", with(jsonRequest(gz(`{"a":1}`)+"junk"), "Content-Encoding", "gzip"), 400, "", "incorrect header check"},
		{"empty gzip", with(with(jsonRequest(""), "Content-Encoding", "gzip"), "Content-Length", "0"), 400, "", "unexpected end of file"},
	}
	for _, c := range cases {
		res := parse(t, c.req, 64)
		if res.err == nil {
			t.Errorf("%s: accepted", c.name)
			continue
		}
		if res.status != c.status || res.err.Status != c.status || res.err.Type != c.errType {
			t.Errorf("%s: OnError(%d, {Status: %d, Type: %q}), want %d %q", c.name, res.status, res.err.Status, res.err.Type, c.status, c.errType)
		}
		if c.message != "" && res.err.Message != c.message {
			t.Errorf("%s: message %q, want %q", c.name, res.err.Message, c.message)
		}
		if res.err.Error() == "" {
			t.Errorf("%s: no error text for development mode", c.name)
		}
	}
	// A form with the same faults
	form := func(body, contentType string) *http.Request {
		req := httptest.NewRequest("POST", "/login", strings.NewReader(body))
		req.Header.Set("Content-Type", contentType)
		return req
	}
	if res := parse(t, form(strings.Repeat("a=1&", 1000)+"a=1", "application/x-www-form-urlencoded"), 0); res.err == nil || res.status != 413 || res.err.Type != ErrTypeTooManyParameters || res.err.Message != "too many parameters" {
		t.Errorf("1001 parameters: %+v", res.err)
	}
	if res := parse(t, form("a=1", "application/x-www-form-urlencoded; charset=iso-8859-1"), 0); res.err == nil || res.status != 415 || res.err.Message != `unsupported charset "ISO-8859-1"` {
		t.Errorf("form charset: %+v", res.err)
	}
	if res := parse(t, form("a="+strings.Repeat("x", 100), "application/x-www-form-urlencoded"), 64); res.err == nil || res.status != 413 || res.err.Type != ErrTypeTooLarge {
		t.Errorf("large form: %+v", res.err)
	}
}

// failingReader breaks off like a connection that closes mid body.
type failingReader struct {
	data []byte
	err  error
}

func (f *failingReader) Read(p []byte) (int, error) {
	if len(f.data) == 0 {
		return 0, f.err
	}
	n := copy(p, f.data)
	f.data = f.data[n:]
	return n, nil
}

func TestBodyAborted(t *testing.T) {
	var gz, zl bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte(`{"a":1}`))
	_ = zw.Close()
	dw := zlib.NewWriter(&zl)
	_, _ = dw.Write([]byte(`{"a":1}`))
	_ = dw.Close()
	// The start of a body in each encoding, then the connection breaks
	starts := map[string][]byte{"": []byte(`{"a":`), "gzip": gz.Bytes()[:14], "deflate": zl.Bytes()[:5]}
	for encoding, start := range starts {
		req := httptest.NewRequest("POST", "/api/x", &failingReader{data: start, err: io.ErrUnexpectedEOF})
		req.Header.Set("Content-Type", "application/json")
		req.ContentLength = 100
		if encoding != "" {
			req.Header.Set("Content-Encoding", encoding)
		}
		res := parse(t, req, 0)
		if res.err == nil || res.status != 400 || res.err.Type != ErrTypeAborted || res.err.Message != "request aborted" {
			t.Errorf("encoding %q: %+v, want 400 request.aborted", encoding, res.err)
		}
		if !errors.Is(res.err, io.ErrUnexpectedEOF) {
			t.Errorf("encoding %q: the read error is not wrapped", encoding)
		}
	}
}

// countingReader notes how much of a body was read.
type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func TestBodyLimitBoundaries(t *testing.T) {
	const limit = 1000
	pad := func(total int) string { return `{"d":"` + strings.Repeat("x", total-8) + `"}` }
	deflate := func(s string) string {
		var buf bytes.Buffer
		zw := zlib.NewWriter(&buf)
		_, _ = zw.Write([]byte(s))
		_ = zw.Close()
		return buf.String()
	}
	if res := parse(t, jsonRequest(pad(limit)), limit); res.err != nil {
		t.Errorf("exactly at the limit: %v", res.err)
	} else if d, _ := Body(res.req).String("d"); len(d) != limit-8 {
		t.Errorf("at the limit: %d bytes of data", len(d))
	}
	if res := parse(t, jsonRequest(pad(limit+1)), limit); res.err == nil || res.status != 413 {
		t.Errorf("one byte over: %+v", res.err)
	}
	// Without a Content-Length the bytes are counted as they come
	chunked := jsonRequest(pad(limit + 1))
	chunked.ContentLength = -1
	chunked.TransferEncoding = []string{"chunked"}
	if res := parse(t, chunked, limit); res.err == nil || res.status != 413 {
		t.Errorf("chunked, one byte over: %+v", res.err)
	}
	chunked = jsonRequest(pad(limit))
	chunked.ContentLength = -1
	chunked.TransferEncoding = []string{"chunked"}
	if res := parse(t, chunked, limit); res.err != nil {
		t.Errorf("chunked, at the limit: %v", res.err)
	}
	// The limit counts the bytes after decompression
	for body, want := range map[string]bool{deflate(pad(limit)): true, deflate(pad(limit + 1)): false} {
		req := jsonRequest(body)
		req.Header.Set("Content-Encoding", "deflate")
		if res := parse(t, req, limit); (res.err == nil) != want {
			t.Errorf("deflate body of %d bytes: accepted %v, want %v", len(body), res.err == nil, want)
		}
	}
	// A declared length over the limit is refused, and the body is read
	// off before the answer so the client gets it
	src := &countingReader{r: strings.NewReader(pad(5000))}
	req := httptest.NewRequest("POST", "/api/x", src)
	req.Header.Set("Content-Type", "application/json")
	req.ContentLength = 5000
	if res := parse(t, req, limit); res.err == nil || res.status != 413 || src.n != 5000 {
		t.Errorf("declared too large: %+v, %d bytes read off", res.err, src.n)
	}
	// Types the console does not read are left alone, whatever their size
	src = &countingReader{r: strings.NewReader(pad(5000))}
	req = httptest.NewRequest("POST", "/x", src)
	req.Header.Set("Content-Type", "text/plain")
	if res := parse(t, req, limit); res.err != nil || src.n != 0 || BodyParsed(res.req) || Body(res.req).Len() != 0 {
		t.Errorf("text body: err %v, %d bytes read", res.err, src.n)
	}
}

func TestBodyCharsetsAndEmpty(t *testing.T) {
	utf16le := func(s string) string {
		var out []byte
		for _, r := range s {
			out = append(out, byte(r), byte(r>>8))
		}
		return string(out)
	}
	swap := func(s string) string {
		b := []byte(s)
		for i := 0; i+1 < len(b); i += 2 {
			b[i], b[i+1] = b[i+1], b[i]
		}
		return string(b)
	}
	cases := []struct {
		name, contentType, body, wantA string
	}{
		{"utf-8", "application/json; charset=utf-8", "{\"a\":\"\xc3\xa9\"}", "\xc3\xa9"},
		{"UTF-8 quoted", `application/json; charset="UTF-8"`, `{"a":"x"}`, "x"},
		{"byte order mark", "application/json", "\xEF\xBB\xBF{\"a\":\"x\"}", "x"},
		{"utf-16le", "application/json; charset=utf-16le", utf16le("{\"a\":\"\u00e9\"}"), "\xc3\xa9"},
		{"utf-16be", "application/json; charset=utf-16be", swap(utf16le("{\"a\":\"\u00e9\"}")), "\xc3\xa9"},
		{"utf-16 with LE mark", "application/json; charset=utf-16", "\xff\xfe" + utf16le(`{"a":"x"}`), "x"},
		{"utf-16 with BE mark", "application/json; charset=utf-16", "\xfe\xff" + swap(utf16le(`{"a":"x"}`)), "x"},
		{"utf-16 guessed LE", "application/json; charset=utf-16", utf16le(`{"a":"x"}`), "x"},
		{"utf-16 guessed BE", "application/json; charset=UTF-16", swap(utf16le(`{"a":"x"}`)), "x"},
		{"utf-16le odd byte", "application/json; charset=utf-16le", utf16le(`{"a":"x"}`) + " ", "x"},
		{"last charset counts", "application/json; charset=latin1; charset=utf-8", `{"a":"x"}`, "x"},
		{"invalid UTF-8 becomes U+FFFD", "application/json", "{\"a\":\"\xe2\x82\"}", "\xEF\xBF\xBD"},
	}
	for _, c := range cases {
		req := httptest.NewRequest("POST", "/api/x", strings.NewReader(c.body))
		req.Header.Set("Content-Type", c.contentType)
		res := parse(t, req, 0)
		if res.err != nil {
			t.Errorf("%s: %v", c.name, res.err)
			continue
		}
		if got, _ := Body(res.req).String("a"); got != c.wantA {
			t.Errorf("%s: a = %q, want %q", c.name, got, c.wantA)
		}
	}
	for name, req := range map[string]*http.Request{
		"empty body":           jsonRequest(""),
		"byte order mark only": jsonRequest("\xEF\xBB\xBF"),
		"no body at all":       httptest.NewRequest("GET", "/api/x", nil),
		"other type":           post("/x", "text/plain", `{"a":1}`),
		"type with bad param":  post("/x", "application/json; charset", `{"a":1}`),
		"+json type":           post("/x", "application/vnd.api+json", `{"a":1}`),
	} {
		res := parse(t, req, 0)
		if res.err != nil || Body(res.req).Len() != 0 || string(BodyValue(res.req).Raw()) != "{}" {
			t.Errorf("%s: err %v, body %s", name, res.err, BodyValue(res.req).Raw())
		}
	}
	// Without the middleware a handler still gets an empty body
	bare := httptest.NewRequest("GET", "/", nil)
	if Body(bare).Len() != 0 || string(BodyValue(bare).Raw()) != "{}" || BodyParsed(bare) {
		t.Error("a request the parsers never saw must have an empty body")
	}
}

// tests/jsonBody.test.js: the 2 MB cap and the attachment route's own
// parser.
func TestAttachmentBody(t *testing.T) {
	big := `{"filename":"log.zip","data":"` + strings.Repeat("B", 4<<20) + `"}`
	var onErr bodyResult
	global := bodies(0, &onErr)
	attachment := AttachmentBody(func(_ http.ResponseWriter, _ *http.Request, status int, err error) {
		onErr.status = status
		errors.As(err, &onErr.err)
	})
	request := func(method, target, body string) *http.Request {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		return req
	}
	size := func(r *http.Request) int {
		d, _ := Body(r).String("data")
		return len(d)
	}

	// A ticket attachment above 2 MB reaches the route
	onErr = bodyResult{}
	res := runBodies(request("POST", "/api/tickets/7/attachments", big), func(next http.Handler) http.Handler { return global(attachment(next)) })
	if !res.next || onErr.err != nil || size(res.req) != 4<<20 {
		t.Errorf("attachment: next %v, err %v", res.next, onErr.err)
	}
	// A step in between that looks at the body early (it is empty then) does not hide it later
	peek := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if Body(r).Len() != 0 || string(BodyValue(r).Raw()) != "{}" {
				t.Error("the attachment body is there before its parser ran")
			}
			next.ServeHTTP(w, r)
		})
	}
	res = runBodies(request("POST", "/api/tickets/7/attachments", `{"data":"abcd"}`), func(next http.Handler) http.Handler { return global(peek(attachment(next))) })
	if !res.next || size(res.req) != 4 {
		t.Errorf("after an early look at the body: next %v, size %d", res.next, size(res.req))
	}
	// The console-wide parser leaves the attachment body to its route
	res = runBodies(request("POST", "/api/tickets/7/attachments", `{"filename":"a","data":"aGk="}`), global)
	if !res.next || BodyParsed(res.req) || Body(res.req).Has("data") {
		t.Errorf("the console-wide parser read the attachment body")
	}
	// Other paths keep the 2 MB cap
	onErr = bodyResult{}
	res = runBodies(request("POST", "/api/other", big), global)
	if res.next || onErr.status != 413 {
		t.Errorf("other path: next %v, status %d", res.next, onErr.status)
	}
	// Only POST to the attachment path is left to the route
	for _, c := range []struct{ method, target string }{
		{"PUT", "/api/tickets/7/attachments"},
		{"POST", "/api/tickets/x/attachments"},
		{"POST", "/api/tickets/7/attachments/x"},
		{"POST", "/API/tickets/7/attachments"},
		{"POST", "/api/tickets/7/attachment"},
	} {
		onErr = bodyResult{}
		res = runBodies(request(c.method, c.target, big), global)
		if res.next || onErr.status != 413 {
			t.Errorf("%s %s: next %v, status %d, want 413", c.method, c.target, res.next, onErr.status)
		}
	}
	for _, target := range []string{"/api/tickets/7/attachments/", "/api/tickets/123456/attachments?x=1"} {
		res = runBodies(request("POST", target, big), global)
		if !res.next || BodyParsed(res.req) {
			t.Errorf("%s was read by the console-wide parser", target)
		}
	}
	// Small bodies parse on every path
	res = runBodies(request("POST", "/api/other", `{"data":"abc"}`), global)
	if !res.next || size(res.req) != 3 {
		t.Errorf("small body: next %v, size %d", res.next, size(res.req))
	}
	// The route's parser has its own limit, and reads nothing twice
	onErr = bodyResult{}
	res = runBodies(request("POST", "/api/tickets/7/attachments", `{"data":"`+strings.Repeat("B", AttachmentBodyLimit)+`"}`), attachment)
	if res.next || onErr.status != 413 {
		t.Errorf("attachment over 35 MB: next %v, status %d", res.next, onErr.status)
	}
	res = runBodies(request("POST", "/api/other", `{"data":"abc"}`), func(next http.Handler) http.Handler { return global(attachment(next)) })
	if !res.next || size(res.req) != 3 {
		t.Errorf("a body the first parser read: size %d", size(res.req))
	}
	form := httptest.NewRequest("POST", "/api/tickets/7/attachments", strings.NewReader("data=abc"))
	form.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res = runBodies(form, func(next http.Handler) http.Handler { return global(attachment(next)) })
	if !res.next || size(res.req) != 3 {
		t.Errorf("a form to the attachment path is read by the form parser: size %d", size(res.req))
	}
}

// The routes with a JSON limit of their own (I2): with RouteBodies the
// console-wide parser leaves their bodies unread, and RouteJSON with the
// route's limit reads them after the route's checks.
func TestRoutesWithTheirOwnBodyLimit(t *testing.T) {
	var onErr bodyResult
	opts := BodyOptions{RouteBodies: true, OnError: func(_ http.ResponseWriter, _ *http.Request, status int, err error) {
		onErr.status = status
		errors.As(err, &onErr.err)
	}}
	global := Bodies(opts)
	routeJSON := func(limit int64) func(http.Handler) http.Handler {
		return RouteJSON(BodyOptions{Limit: limit, OnError: opts.OnError})
	}
	request := func(method, target string, size int) *http.Request {
		req := httptest.NewRequest(method, target, strings.NewReader(`{"data":"`+strings.Repeat("B", size)+`"}`))
		req.Header.Set("Content-Type", "application/json")
		return req
	}
	routes := []struct {
		method, target string
		limit          int64
	}{
		{"POST", "/api/ui-shell", UIShellBodyLimit},
		{"POST", "/API/UI-Shell/", UIShellBodyLimit},
		{"POST", "/api/panel/pairing/codes", PairingBodyLimit},
		{"POST", "/api/generator/bundles", GeneratorBodyLimit},
		{"PUT", "/api/generator/bundles/b-1", GeneratorBodyLimit},
		{"POST", "/api/generator/preview?x=1", GeneratorBodyLimit},
	}
	for _, c := range routes {
		// The console-wide parser leaves even a body over its 2 MB to the route
		onErr = bodyResult{}
		res := runBodies(request(c.method, c.target, 3<<20), global)
		if !res.next || BodyParsed(res.req) || onErr.status != 0 {
			t.Errorf("%s %s: next %v, parsed %v, status %d; want it left to the route", c.method, c.target, res.next, BodyParsed(res.req), onErr.status)
		}
		// The route's own parser takes what fits its limit and refuses more
		both := func(next http.Handler) http.Handler { return global(routeJSON(c.limit)(next)) }
		res = runBodies(request(c.method, c.target, int(c.limit)-100), both)
		if d, _ := Body(res.req).String("data"); !res.next || len(d) != int(c.limit)-100 {
			t.Errorf("%s %s under its limit: next %v, %d bytes of data", c.method, c.target, res.next, len(d))
		}
		onErr = bodyResult{}
		res = runBodies(request(c.method, c.target, int(c.limit)+1), both)
		if res.next || onErr.status != 413 {
			t.Errorf("%s %s over its limit: next %v, status %d, want 413", c.method, c.target, res.next, onErr.status)
		}
	}
	// Other routes keep the console-wide parser and its 2 MB
	for _, c := range []struct{ method, target string }{
		{"GET", "/api/ui-shell"},
		{"POST", "/api/ui-shell/x"},
		{"POST", "/api/generator/bundles/b-1/rebuild"},
		{"DELETE", "/api/panel/pairing/codes"},
		{"POST", "/api/other"},
	} {
		onErr = bodyResult{}
		if res := runBodies(request(c.method, c.target, 3<<20), global); res.next || onErr.status != 413 {
			t.Errorf("%s %s: next %v, status %d, want the console-wide 413", c.method, c.target, res.next, onErr.status)
		}
	}
	// Without RouteBodies the console-wide parser reads them all
	onErr = bodyResult{}
	if res := runBodies(request("POST", "/api/ui-shell", 3<<20), bodies(0, &onErr)); res.next || onErr.status != 413 {
		t.Errorf("without RouteBodies: next %v, status %d", res.next, onErr.status)
	}
}

func bodyOf(t *testing.T, body string) JSONObject {
	t.Helper()
	res := parse(t, jsonRequest(body), 0)
	if res.err != nil {
		t.Fatalf("%s: %v", body, res.err)
	}
	return Body(res.req)
}

func TestJSONObjectHelpers(t *testing.T) {
	obj := bodyOf(t, `{"s":"text","empty":"","zero":0,"negzero":-0,"fzero":0.0,"one":1,"null":null,"f":false,"t":true,
		"zs":"0","fs":"false","arr":[],"obj":{},"id":12345678901234567890,"fl":1.0,"exp":1E2,"tiny":1e-400,"huge":1e400,
		"nested":{"k":[1,"two",{"three":3}]},"dup":1,"dup":2,"esc":"a\u00e9\n"}`)

	for key, want := range map[string]bool{"s": true, "empty": false, "zero": false, "negzero": false, "fzero": false, "one": true, "null": false,
		"f": false, "t": true, "zs": true, "fs": true, "arr": true, "obj": true, "id": true, "tiny": false, "huge": true, "missing": false} {
		if got := obj.Truthy(key); got != want {
			t.Errorf("Truthy(%q) = %v, want %v", key, got, want)
		}
	}
	for key, want := range map[string]bool{"s": true, "null": true, "empty": true, "zero": true, "missing": false, "S": false} {
		if got := obj.Has(key); got != want {
			t.Errorf("Has(%q) = %v, want %v", key, got, want)
		}
	}
	if s, ok := obj.String("s"); !ok || s != "text" {
		t.Errorf("String(s) = %q, %v", s, ok)
	}
	if s, ok := obj.String("esc"); !ok || s != "a\xc3\xa9\n" {
		t.Errorf("String(esc) = %q, %v", s, ok)
	}
	for _, key := range []string{"one", "null", "t", "arr", "obj", "missing"} {
		if s, ok := obj.String(key); ok || s != "" {
			t.Errorf("String(%q) = %q, %v; only a JSON string is a string", key, s, ok)
		}
	}
	// Numbers keep every digit and their form
	for key, want := range map[string]string{"id": "12345678901234567890", "fl": "1.0", "exp": "1E2", "negzero": "-0", "huge": "1e400"} {
		if n, ok := obj.Number(key); !ok || n.String() != want {
			t.Errorf("Number(%q) = %q, %v; want %q", key, n, ok, want)
		}
		if string(obj.Raw(key)) != want {
			t.Errorf("Raw(%q) = %s, want %s", key, obj.Raw(key), want)
		}
	}
	if _, ok := obj.Number("zs"); ok {
		t.Error(`Number of the string "0" must not be a number`)
	}
	if obj.Raw("missing") != nil || string(obj.Raw("null")) != "null" || string(obj.Raw("obj")) != "{}" {
		t.Errorf("Raw: missing %q, null %q, obj %q", obj.Raw("missing"), obj.Raw("null"), obj.Raw("obj"))
	}
	if n, _ := obj.Number("dup"); n != "2" {
		t.Errorf("a name sent twice keeps the last value, got %s", n)
	}
	// Kinds
	for key, want := range map[string]Kind{"s": KindString, "zero": KindNumber, "null": KindNull, "f": KindBool, "arr": KindArray, "obj": KindObject, "missing": KindUndefined} {
		if got := obj.Get(key).Kind(); got != want {
			t.Errorf("Kind(%q) = %v, want %v", key, got, want)
		}
	}
	if f, ok := obj.Get("id").Float(); !ok || f != 12345678901234567890 {
		t.Errorf("Float(id) = %v, %v", f, ok)
	}
	if b, ok := obj.Get("t").Bool(); !ok || !b {
		t.Errorf("Bool(t) = %v, %v", b, ok)
	}
	if _, ok := obj.Get("one").Bool(); ok {
		t.Error("the number 1 is not a boolean")
	}
	// Nested values
	nested, ok := obj.Get("nested").Object()
	if !ok {
		t.Fatal("nested is not an object")
	}
	items, ok := nested.Get("k").Array()
	if !ok || len(items) != 3 {
		t.Fatalf("nested.k: %v, %d items", ok, len(items))
	}
	if two, _ := items[1].Str(); two != "two" {
		t.Errorf("nested.k[1] = %q", two)
	}
	if inner, ok := items[2].Object(); !ok || inner.Int("three", 0) != 3 {
		t.Error("nested.k[2].three")
	}
	if _, ok := obj.Get("arr").Object(); ok {
		t.Error("an array is not an object")
	}
	if _, ok := obj.Get("obj").Array(); ok {
		t.Error("an object is not an array")
	}
	var decoded struct{ K []any }
	if err := obj.Get("nested").Decode(&decoded); err != nil || len(decoded.K) != 3 {
		t.Errorf("Decode: %v, %v", err, decoded)
	}
	if err := obj.Get("missing").Decode(&decoded); err == nil {
		t.Error("Decode of a missing member must fail")
	}
}

func TestObjectKeysInJavaScriptOrder(t *testing.T) {
	obj := bodyOf(t, `{"b":1,"2":2,"a":3,"1":4,"01":5,"-1":6,"4294967295":7,"4294967294":8,"b":9,"0":10}`)
	want := []string{"0", "1", "2", "4294967294", "b", "a", "01", "-1", "4294967295"}
	if got := obj.Keys(); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("Keys() = %v, want %v", got, want)
	}
	if obj.Len() != len(want) {
		t.Errorf("Len() = %d", obj.Len())
	}
	raw, err := json.Marshal(obj)
	if err != nil || string(raw) != `{"0":10,"1":4,"2":2,"4294967294":8,"b":9,"a":3,"01":5,"-1":6,"4294967295":7}` {
		t.Errorf("MarshalJSON = %s, %v", raw, err)
	}
	keys := obj.Keys()
	keys[0] = "changed"
	if obj.Keys()[0] != "0" {
		t.Error("Keys() must return a copy")
	}
}

func TestBodyOfAnArrayAndBind(t *testing.T) {
	res := parse(t, jsonRequest(` [1,{"a":2}] `), 0)
	if Body(res.req).Len() != 0 {
		t.Error("an array body is no object: Body must be empty, as plainBodyObject gave")
	}
	items, ok := BodyValue(res.req).Array()
	if !ok || len(items) != 2 || string(BodyValue(res.req).Raw()) != `[1,{"a":2}]` {
		t.Errorf("BodyValue: %v %s", ok, BodyValue(res.req).Raw())
	}
	var list []any
	if err := Bind(res.req, &list); err != nil || len(list) != 2 {
		t.Errorf("Bind into a slice: %v", err)
	}

	res = parse(t, jsonRequest(`{"name":"n","count":3,"id":12345678901234567890,"extra":true}`), 0)
	var shape struct {
		Name  string      `json:"name"`
		Count int         `json:"count"`
		ID    json.Number `json:"id"`
	}
	if err := Bind(res.req, &shape); err != nil || shape.Name != "n" || shape.Count != 3 || shape.ID != "12345678901234567890" {
		t.Errorf("Bind: %v %+v", err, shape)
	}
	var wrong struct {
		Name int `json:"name"`
	}
	if err := Bind(res.req, &wrong); err == nil {
		t.Error("Bind must report a type that does not fit")
	}
	// Without a body, Bind sees {}
	shape.Name = "kept"
	if err := Bind(httptest.NewRequest("GET", "/", nil), &shape); err != nil || shape.Name != "kept" {
		t.Errorf("Bind without a body: %v %+v", err, shape)
	}
}

// tests/bodyScalars.test.js, and the rest of lib/bodyScalars.js.
func TestBodyScalars(t *testing.T) {
	obj := bodyOf(t, `{"arr":["x"],"num":["1"],"ok":"ok","len":{"length":42},"n42":"42","big":999,"obj":{"x":1},"t":true,"fs":"false","ta":["true"],
		"length":42,"float":1.9,"neg":-1.9,"padded":" 42 ","text":"42abc","blank":"  ","nan":"abc","huge":"99999999999999999999","exp":"1e3","inf":1e400,
		"one":1,"zero":0,"s1":"1","s0":"0","two":2,"yes":"yes","f":false,"null":null,"n":1.0,"e21":1e21,"small":0.0000001,"frac":0.5,"negzero":-0}`)

	// bodyString rejects arrays and objects
	if obj.Text("arr", "") != "" || obj.Text("ok", "") != "ok" || obj.Text("obj", "") != "" {
		t.Error("Text of an array, a string, an object")
	}
	for key, want := range map[string]string{"big": "999", "t": "true", "f": "false", "float": "1.9", "n": "1", "e21": "1e+21", "small": "1e-7",
		"frac": "0.5", "negzero": "0", "null": "fb", "inf": "fb", "missing": "fb", "blank": "  "} {
		if got := obj.Text(key, "fb"); got != want {
			t.Errorf("Text(%q) = %q, want %q", key, got, want)
		}
	}
	// bodyInt rejects arrays and objects and clamps
	if obj.Int("num", 0) != 0 || obj.Int("len", 0) != 0 || obj.Int("n42", 0) != 42 {
		t.Error("Int of an array, an object, a string")
	}
	if got := obj.IntBetween("big", 0, -100, 100); got != 100 {
		t.Errorf("IntBetween clamps to the maximum: %d", got)
	}
	if got := obj.IntBetween("neg", 0, 5, 100); got != 5 {
		t.Errorf("IntBetween clamps to the minimum: %d", got)
	}
	if got := obj.IntBetween("missing", 500, 0, 100); got != 100 {
		t.Errorf("the fallback is clamped too: %d", got)
	}
	for key, want := range map[string]int64{"float": 1, "neg": -1, "padded": 42, "text": 42, "blank": -7, "nan": -7, "huge": 9007199254740991,
		"exp": 1, "inf": -7, "t": -7, "null": -7, "missing": -7, "length": 42, "frac": 0} {
		if got := obj.Int(key, -7); got != want {
			t.Errorf("Int(%q) = %d, want %d", key, got, want)
		}
	}
	// plainBodyObject: an array body has no length member
	array := parse(t, jsonRequest(`[1,2,3]`), 0)
	if got := Body(array.req).IntBetween("length", 99, 0, 100); got != 99 {
		t.Errorf("length of an array body: %d", got)
	}
	// bodyBool
	if !obj.Flag("t", false) || obj.Flag("fs", true) || obj.Flag("ta", false) {
		t.Error("Flag of true, of the string false, of an array")
	}
	for key, want := range map[string][2]bool{"one": {true, true}, "zero": {false, false}, "s1": {true, true}, "s0": {false, false}, "n": {true, true},
		"negzero": {false, false}, "two": {false, true}, "yes": {false, true}, "null": {false, true}, "missing": {false, true}, "obj": {false, true}} {
		if got := [2]bool{obj.Flag(key, false), obj.Flag(key, true)}; got != want {
			t.Errorf("Flag(%q) = %v, want %v", key, got, want)
		}
	}
}

func TestJSNumberString(t *testing.T) {
	for literal, want := range map[string]string{
		"0": "0", "-0": "0", "1": "1", "1.0": "1", "-1.5": "-1.5", "100": "100", "1e2": "100", "1E2": "100", "0.1": "0.1", "0.000001": "0.000001",
		"0.0000001": "1e-7", "1.5e-7": "1.5e-7", "123456789012345680000": "123456789012345680000", "1e21": "1e+21", "1.5e300": "1.5e+300",
		"12345678901234567890": "12345678901234567000", "9007199254740993": "9007199254740992", "5e-324": "5e-324", "123.456": "123.456", "1e-400": "0",
	} {
		if got := jsNumberString(jsNumber(literal)); got != want {
			t.Errorf("String(%s) = %q, want %q", literal, got, want)
		}
	}
}

func TestParseMediaType(t *testing.T) {
	accepted := map[string]string{
		"application/json":                    "application/json",
		"APPLICATION/JSON":                    "application/json",
		" application/json ":                  "application/json",
		"application/json; charset=utf-8":     "application/json",
		"application/json;charset=utf-8":      "application/json",
		"application/json ; a = b ; c=\"d;\"": "application/json",
		"application/json; x=\"a\\\"b\"":      "application/json",
		"application/vnd.api+json":            "application/vnd.api+json",
		"application/json+":                   "application/json",
		"multipart/form-data; boundary=x":     "multipart/form-data",
	}
	for header, want := range accepted {
		if mt, ok := parseMediaType(header); !ok || mt.full != want {
			t.Errorf("parseMediaType(%q) = %q, %v; want %q", header, mt.full, ok, want)
		}
	}
	for _, header := range []string{"", "json", "application", "application/", "/json", "application/json/x", "application/json;", "application/json; charset",
		"application/json; charset=", "application/json; =x", "application/json;;", "application/json; a=b;", "application/json, text/plain",
		"application/json text", "application/json; a=\"b", "application/json; a=b\"", "application/a+b+json", "application/x+a.b", "*/*", "application/*",
		"\tapplication/json", "application/json; a=\"\x01\"", ".a/json", "application/" + strings.Repeat("x", 128)} {
		if mt, ok := parseMediaType(header); ok {
			t.Errorf("parseMediaType(%q) accepted as %q", header, mt.full)
		}
	}
	mt, _ := parseMediaType(`application/json; Charset="UTF-8"; charset=latin1; x="a\"b"`)
	if mt.params["charset"] != "latin1" || mt.params["x"] != `a"b` {
		t.Errorf("params: %v", mt.params)
	}
}

func TestDecodeUTF8ReplacesLikeBrowsers(t *testing.T) {
	const fffd = "\xEF\xBF\xBD"
	for in, want := range map[string]string{
		"plain":                      "plain",
		"\xe2\x82":                   fffd,
		"\xe2\x82x":                  fffd + "x",
		"\xf0\x9f\x98x":              fffd + "x",
		"\xc0\xaf":                   fffd + fffd,
		"\xe0\x80\xaf":               fffd + fffd + fffd,
		"\xed\xa0\x80":               fffd + fffd + fffd,
		"\xf4\x90\x80\x80":           fffd + fffd + fffd + fffd,
		"\xff\xfeA":                  fffd + fffd + "A",
		"a\xc3\xa9b\xf0\x9f\x98\x80": "a\xc3\xa9b\xf0\x9f\x98\x80",
		"\xc3":                       fffd,
		"\xc3\xc3\xa9":               fffd + "\xc3\xa9",
		"\x80":                       fffd,
	} {
		if got := decodeUTF8([]byte(in)); got != want {
			t.Errorf("decodeUTF8(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseForm(t *testing.T) {
	flat := func(fields []formField) string {
		var parts []string
		for _, f := range fields {
			parts = append(parts, f.key+"="+strings.Join(f.values, "|"))
		}
		return strings.Join(parts, "&")
	}
	const fffd = "\xEF\xBF\xBD"
	for body, want := range map[string]string{
		"a=1&b=2":               "a=1&b=2",
		"a=1&a=2&a=3":           "a=1|2|3",
		"a[]=1&a[]=2":           "a[]=1|2",
		"a[b]=1":                "a[b]=1",
		"a=b=c":                 "a=b=c",
		"a":                     "a=",
		"a=":                    "a=",
		"=b":                    "=b",
		"=":                     "=",
		"&&":                    "",
		"a=1&&b=2":              "a=1&b=2",
		"a+b=c+d":               "a b=c d",
		"a%20b=c%2Bd":           "a b=c+d",
		"n=%C3%A9":              "n=\xc3\xa9",
		"bad=%zz":               "bad=%zz",
		"bad=%E9":               "bad=" + fffd,
		"bad=%":                 "bad=%",
		"bad=%C3%28":            "bad=" + fffd + "(",
		"x=%41%zz":              "x=A%zz",
		"x=%ED%A0%80":           "x=" + fffd + fffd + fffd,
		"x=%E2%82":              "x=" + fffd,
		"x=\xc3\xa9%E9":         "x=" + fffd + fffd,
		"x=\xc3\xa9%41":         "x=\xc3\xa9A",
		"a=1&A=2":               "a=1&A=2",
		"x=1;y=2":               "x=1;y=2",
		"a=%u00e9":              "a=%u00e9",
		" a = 1 ":               " a = 1 ",
		"a=1&a":                 "a=1|",
		"x=\xf0\x9f\x98\x80%E9": "x==\x00" + fffd,
	} {
		if got := flat(parseForm(body)); got != want {
			t.Errorf("parseForm(%q) = %q, want %q", body, got, want)
		}
	}
	if tooManyParameters(strings.Repeat("a&", 999) + "a") {
		t.Error("1000 pairs are allowed")
	}
	if !tooManyParameters(strings.Repeat("a&", 1000) + "a") {
		t.Error("1001 pairs are too many")
	}
}

func TestFormBodyAsObject(t *testing.T) {
	req := httptest.NewRequest("POST", "/login", strings.NewReader("username=admin&tags=a&tags=b&n=42&on=true&2=x&1=y"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	res := parse(t, req, 0)
	if res.err != nil {
		t.Fatal(res.err)
	}
	obj := Body(res.req)
	if s, _ := obj.String("username"); s != "admin" {
		t.Errorf("username = %q", s)
	}
	if tags, ok := obj.Get("tags").Array(); !ok || len(tags) != 2 {
		t.Errorf("a field sent twice is an array: %s", obj.Raw("tags"))
	}
	if obj.Int("n", 0) != 42 || !obj.Flag("on", false) || obj.Text("n", "") != "42" {
		t.Error("form values are strings the scalar helpers read")
	}
	if got := strings.Join(obj.Keys(), ","); got != "1,2,username,tags,n,on" {
		t.Errorf("Keys() = %s", got)
	}
	var shape struct {
		Username string   `json:"username"`
		Tags     []string `json:"tags"`
	}
	if err := Bind(res.req, &shape); err != nil || shape.Username != "admin" || len(shape.Tags) != 2 {
		t.Errorf("Bind of a form: %v %+v", err, shape)
	}
}

func multipartRequest(target, boundary string, parts ...string) *http.Request {
	var body strings.Builder
	for _, p := range parts {
		body.WriteString("--" + boundary + "\r\n" + p + "\r\n")
	}
	body.WriteString("--" + boundary + "--\r\n")
	req := httptest.NewRequest("POST", target, strings.NewReader(body.String()))
	req.Header.Set("Content-Type", "multipart/form-data; boundary="+boundary)
	return req
}

func filePart(field, filename, contentType, content string) string {
	return `Content-Disposition: form-data; name="` + field + `"; filename="` + filename + `"` + "\r\nContent-Type: " + contentType + "\r\n\r\n" + content
}

func textPart(field, value string) string {
	return `Content-Disposition: form-data; name="` + field + `"` + "\r\n\r\n" + value
}

func TestParseUpload(t *testing.T) {
	dir := t.TempDir()
	left := func() int {
		entries, _ := os.ReadDir(dir)
		return len(entries)
	}
	// Memory
	up, err := ParseUpload(multipartRequest("/api/x/upload", "b", textPart("code", "de"), filePart("file", "de.json", "application/json", `{"a":1}`)), UploadOptions{Field: "file", MaxFileSize: 100})
	if err != nil || up.File == nil || string(up.File.Buffer) != `{"a":1}` || up.File.Size != 7 || up.File.OriginalName != "de.json" || up.File.MIMEType != "application/json" || up.File.FieldName != "file" || up.File.Path != "" {
		t.Fatalf("memory upload: %v %+v", err, up)
	}
	if code, _ := up.Fields.String("code"); code != "de" {
		t.Errorf("field code = %q", code)
	}
	// Disk
	up, err = ParseUpload(multipartRequest("/x", "b", filePart("logo", "My Logo.PNG", "image/png", "PNGDATA")), UploadOptions{Field: "logo", MaxFileSize: 100, Dir: dir,
		Filename: func(f *UploadedFile) (string, error) {
			return "logo-" + strings.ToLower(filepath.Ext(f.OriginalName)), nil
		}})
	if err != nil || up.File == nil || up.File.Path != filepath.Join(dir, "logo-.png") || up.File.Filename != "logo-.png" || up.File.Destination != dir || up.File.Size != 7 || up.File.Buffer != nil {
		t.Fatalf("disk upload: %v %+v", err, up.File)
	}
	if content, _ := os.ReadFile(up.File.Path); string(content) != "PNGDATA" {
		t.Errorf("file on disk: %q", content)
	}
	_ = os.Remove(up.File.Path)
	// Disk without a name function: 32 hex digits
	up, err = ParseUpload(multipartRequest("/x", "b", filePart("logo", "a.png", "image/png", "x")), UploadOptions{Field: "logo", Dir: dir})
	if err != nil || len(up.File.Filename) != 32 || strings.Trim(up.File.Filename, "0123456789abcdef") != "" {
		t.Fatalf("default file name: %v %q", err, up.File.Filename)
	}
	_ = os.Remove(up.File.Path)

	exactly := strings.Repeat("x", 100)
	if up, err = ParseUpload(multipartRequest("/x", "b", filePart("logo", "a.png", "image/png", exactly)), UploadOptions{Field: "logo", MaxFileSize: 100, Dir: dir}); err != nil || up.File.Size != 100 {
		t.Errorf("a file of exactly the limit: %v", err)
	}
	_ = os.Remove(filepath.Join(dir, up.File.Filename))

	failures := []struct {
		name  string
		req   *http.Request
		opts  UploadOptions
		code  string
		field string
		text  string
	}{
		{"one byte over, disk", multipartRequest("/x", "b", filePart("logo", "a.png", "image/png", exactly+"y")), UploadOptions{Field: "logo", MaxFileSize: 100, Dir: dir}, UploadFileTooLarge, "logo", "File too large"},
		{"one byte over, memory", multipartRequest("/x", "b", filePart("logo", "a.png", "image/png", exactly+"y")), UploadOptions{Field: "logo", MaxFileSize: 100}, UploadFileTooLarge, "logo", "File too large"},
		{"wrong field", multipartRequest("/x", "b", filePart("other", "a.png", "image/png", "x")), UploadOptions{Field: "logo", Dir: dir}, UploadUnexpectedFile, "other", "Unexpected file field"},
		{"second file", multipartRequest("/x", "b", filePart("logo", "a.png", "image/png", "x"), filePart("logo", "b.png", "image/png", "y")), UploadOptions{Field: "logo", Dir: dir}, UploadUnexpectedFile, "logo", "Unexpected file field"},
		{"file then wrong field", multipartRequest("/x", "b", filePart("logo", "a.png", "image/png", "x"), filePart("z", "b.png", "image/png", "y")), UploadOptions{Field: "logo", Dir: dir}, UploadUnexpectedFile, "z", "Unexpected file field"},
		{"field without name", multipartRequest("/x", "b", textPart("", "v")), UploadOptions{Field: "logo", Dir: dir}, UploadMissingName, "", "Field name missing"},
		{"field too long", multipartRequest("/x", "b", textPart("big", strings.Repeat("v", maxFieldSize+1))), UploadOptions{Field: "logo", Dir: dir}, UploadFieldTooLong, "big", "Field value too long"},
		{"array index too large", multipartRequest("/x", "b", textPart("a[10001]", "v")), UploadOptions{Field: "logo", Dir: dir}, UploadArrayIndexLarge, "a[10001]", "Field name array index too large"},
		{"filter refuses", multipartRequest("/x", "b", filePart("logo", "a.txt", "text/plain", "x")), UploadOptions{Field: "logo", Dir: dir, Filter: func(*UploadedFile) (bool, error) { return false, errors.New("Invalid file type") }}, "", "", "Invalid file type"},
		{"name function fails", multipartRequest("/x", "b", filePart("logo", "a.png", "image/png", "x")), UploadOptions{Field: "logo", Dir: dir, Filename: func(*UploadedFile) (string, error) { return "", errors.New("no name") }}, "", "", "no name"},
		{"folder missing", multipartRequest("/x", "b", filePart("logo", "a.png", "image/png", "x")), UploadOptions{Field: "logo", Dir: filepath.Join(dir, "missing")}, "", "", ""},
	}
	for _, c := range failures {
		src := &countingReader{r: c.req.Body}
		c.req.Body = io.NopCloser(src)
		total := c.req.ContentLength
		up, err := ParseUpload(c.req, c.opts)
		if err == nil || up != nil {
			t.Errorf("%s: no error", c.name)
			continue
		}
		if UploadErrorCode(err) != c.code {
			t.Errorf("%s: code %q, want %q", c.name, UploadErrorCode(err), c.code)
		}
		var ue *UploadError
		if errors.As(err, &ue) && ue.Field != c.field {
			t.Errorf("%s: field %q, want %q", c.name, ue.Field, c.field)
		}
		if c.text != "" && err.Error() != c.text {
			t.Errorf("%s: message %q, want %q", c.name, err.Error(), c.text)
		}
		if left() != 0 {
			t.Errorf("%s: %d files left behind", c.name, left())
			for _, e := range func() []os.DirEntry { es, _ := os.ReadDir(dir); return es }() {
				_ = os.Remove(filepath.Join(dir, e.Name()))
			}
		}
		if src.n != total {
			t.Errorf("%s: %d of %d request bytes read; the rest has to be read off before the answer", c.name, src.n, total)
		}
	}

	// A filter that skips leaves the request without a file, and the slot free
	skip := func(f *UploadedFile) (bool, error) { return f.MIMEType != "text/plain", nil }
	up, err = ParseUpload(multipartRequest("/x", "b", filePart("file", "a.txt", "text/plain", "x"), filePart("file", "a.bin", "application/octet-stream", "yy"), textPart("k", "v")), UploadOptions{Field: "file", Filter: skip})
	if err != nil || up.File == nil || up.File.OriginalName != "a.bin" || !up.Fields.Has("k") {
		t.Errorf("skip then take: %v %+v", err, up)
	}
	// Not multipart: nothing is read, the fields are the parsed body
	res := parse(t, jsonRequest(`{"a":1}`), 0)
	up, err = ParseUpload(res.req, UploadOptions{Field: "file"})
	if err != nil || up.File != nil || up.Fields.Int("a", 0) != 1 {
		t.Errorf("JSON request to an upload route: %v %+v", err, up)
	}
	for name, req := range map[string]*http.Request{
		"no boundary":       post("/x", "multipart/form-data", "x"),
		"multipart/mixed":   post("/x", "multipart/mixed; boundary=b", "--b--\r\n"),
		"cut short":         post("/x", "multipart/form-data; boundary=b", "--b\r\n"+filePart("file", "a.png", "image/png", "x")),
		"other boundary":    post("/x", "multipart/form-data; boundary=c", "--b\r\n"+textPart("a", "1")+"\r\n--b--\r\n"),
		"spaces around =":   post("/x", "multipart/form-data; boundary = b", "--b--\r\n"),
		"nothing at all":    post("/x", "multipart/form-data; boundary=b", ""),
		"boundary is empty": post("/x", `multipart/form-data; boundary=""`, "--b--\r\n"),
	} {
		if up, err := ParseUpload(req, UploadOptions{Field: "file", Dir: dir}); err == nil || up != nil || UploadErrorCode(err) != "" || left() != 0 {
			t.Errorf("%s: err %v", name, err)
		}
	}
	want := map[string]string{"no boundary": "Multipart: Boundary not found", "multipart/mixed": "Unsupported content type: multipart/mixed; boundary=b", "cut short": "Unexpected end of form", "spaces around =": "Malformed content type"}
	for name, text := range want {
		req := map[string]*http.Request{
			"no boundary":     post("/x", "multipart/form-data", "x"),
			"multipart/mixed": post("/x", "multipart/mixed; boundary=b", "--b--\r\n"),
			"cut short":       post("/x", "multipart/form-data; boundary=b", "--b\r\n"+filePart("file", "a.png", "image/png", "x")),
			"spaces around =": post("/x", "multipart/form-data; boundary = b", "--b--\r\n"),
		}[name]
		if _, err := ParseUpload(req, UploadOptions{Field: "file"}); err == nil || err.Error() != text {
			t.Errorf("%s: %v, want %q", name, err, text)
		}
	}
}

func TestUploadFieldNames(t *testing.T) {
	up, err := ParseUpload(multipartRequest("/x", "b",
		textPart("a[b]", "1"), textPart("a[c]", "2"), textPart("l[]", "x"), textPart("l[]", "y"), textPart("d", "1"), textPart("d", "2"),
		textPart("n[0]", "p"), textPart("n[2]", "q"), textPart("[x]", "lit"), textPart("s", "1"), textPart("s[k]", "2"), textPart("x[]y", "lit2"),
		textPart("arr[0]", "a"), textPart("arr[k]", "b"), textPart("10", "ten"), textPart("2", "two"), textPart("e%22%0A", "v"),
		textPart("one[]", "only"), textPart("deep[a][]", "v")), UploadOptions{Field: "file"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(up.Fields)
	want := `{"2":"two","10":"ten","a":{"b":"1","c":"2"},"l":["x","y"],"d":["1","2"],"n":["p",null,"q"],"[x]":"lit","s":{"":"1","k":"2"},"x[]y":"lit2","arr":{"0":"a","k":"b"},"e\"\n":"v","one":["only"],"deep":{"a":["v"]}}`
	if string(raw) != want {
		t.Errorf("fields\n got  %s\n want %s", raw, want)
	}
}

func TestUploadFileNames(t *testing.T) {
	for header, want := range map[string]string{
		`form-data; name="file"; filename="a.png"`:                                   "a.png",
		`form-data; name="file"; filename="../../etc/pass wd.png"`:                   "pass wd.png",
		`form-data; name="file"; filename="C:\dir\a.png"`:                            "a.png",
		`form-data; name="file"; filename="a%22b%0A.png"`:                            "a\"b\n.png",
		"form-data; name=\"file\"; filename=\"\xc3\xa9.png\"":                        "\xc3\x83\xc2\xa9.png",
		`form-data; name="file"; filename*=UTF-8''%C3%A9.png`:                        "\xc3\xa9.png",
		`form-data; name="file"; filename="plain.png"; filename*=utf-8'en'st%C3%A4r`: "st\xc3\xa4r",
		`form-data; name="file"; filename*=iso-8859-1''%E9.png`:                      "\xc3\xa9.png",
		`form-data; name=file; filename=a.png`:                                       "a.png",
		`FORM-DATA; NAME="file"; FILENAME="A.PNG"`:                                   "A.PNG",
		`form-data; name="file"; filename="a\"b\\c\d.png"`:                           "d.png",
		`form-data; name="file"; filename="a\"b\x.png"`:                              "x.png",
		`form-data; name="file"; filename="q\"uo\"te.png"`:                           `q"uo"te.png`,
		`form-data; name="file"; filename="50%.pdf"`:                                 "50%.pdf",
	} {
		part := "Content-Disposition: " + header + "\r\nContent-Type: image/png\r\n\r\nx"
		up, err := ParseUpload(multipartRequest("/x", "b", part), UploadOptions{Field: "file"})
		if err != nil || up.File == nil {
			t.Errorf("%s: %v, file %v", header, err, up)
			continue
		}
		if up.File.OriginalName != want {
			t.Errorf("%s: OriginalName %q, want %q", header, up.File.OriginalName, want)
		}
	}
	// Parts that are no file of this form are skipped without an error
	for _, part := range []string{
		"Content-Type: image/png\r\n\r\nx",
		"Content-Disposition: attachment; name=\"file\"; filename=\"a.png\"\r\n\r\nx",
		"Content-Disposition: form-data; name=\"file\"; filename=\"a.png\r\n\r\nx",
		"Content-Disposition: form-data; name=\"file\"; filename=\"..\"\r\n\r\nx",
		"Content-Disposition: form-data; name=\"file\"; filename=\"dir/\"\r\n\r\nx",
		"Content-Disposition: form-data; name=\"file\"\r\nContent-Type: application/octet-stream\r\n\r\nx",
	} {
		up, err := ParseUpload(multipartRequest("/x", "b", part, textPart("k", "v")), UploadOptions{Field: "file"})
		if err != nil || up.File != nil || !up.Fields.Has("k") || up.Fields.Has("file") {
			t.Errorf("%q: err %v, file %v, fields %v", part, err, up.File, up.Fields.Keys())
		}
	}
	// An empty filename makes the part a text field
	up, err := ParseUpload(multipartRequest("/x", "b", filePart("file", "", "image/png", "x")), UploadOptions{Field: "file"})
	if err != nil || up.File != nil || up.Fields.Text("file", "") != "x" {
		t.Errorf("empty filename: %v %+v", err, up)
	}
}

// tests/multerCleanup.test.js
func TestUploadCleanupIsConfined(t *testing.T) {
	// A temporary folder and a data folder that are not inside each other,
	// wherever this test runs
	base := t.TempDir()
	tmpRoot, data := filepath.Join(base, "tmp"), filepath.Join(base, "data")
	for _, dir := range []string{tmpRoot, data} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(name, tmpRoot)
	}
	if os.TempDir() != tmpRoot {
		t.Fatalf("the temporary folder is %q, want %q", os.TempDir(), tmpRoot)
	}
	tmp := filepath.Join(os.TempDir(), "bd-upload-test-file")
	if err := os.WriteFile(tmp, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmp)
	// Allows files under the temporary folder
	if got, ok := ConfinedUploadPath(tmp, ""); !ok || got != tmp {
		t.Errorf("ConfinedUploadPath(%q) = %q, %v", tmp, got, ok)
	}
	if !RemoveUploadedFile(&UploadedFile{Path: tmp}, "") {
		t.Error("the file in the temporary folder was not removed")
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Error("the file is still there")
	}
	// Rejects path traversal outside the roots
	evil := filepath.Join(os.TempDir(), "..", "etc", "passwd")
	if _, ok := ConfinedUploadPath(evil, ""); ok {
		t.Errorf("%s is outside the temporary folder", evil)
	}
	// Rejects empty paths
	if _, ok := ConfinedUploadPath("", ""); ok {
		t.Error("the empty path")
	}
	if RemoveUploadedFile(nil, "") || RemoveUploadedFile(&UploadedFile{}, "") {
		t.Error("nothing to remove")
	}

	// The data folder is the second root
	inside := filepath.Join(data, "uploads", "logo-0123.png")
	_ = os.MkdirAll(filepath.Dir(inside), 0o755)
	_ = os.WriteFile(inside, []byte("x"), 0o600)
	odd := filepath.Join(data, "uploads", "a b.png")
	_ = os.WriteFile(odd, []byte("x"), 0o600)
	other := filepath.Join(data, "..", "keep.txt")
	_ = os.WriteFile(other, []byte("x"), 0o600)
	defer os.Remove(other)
	if got, ok := ConfinedUploadPath(inside, data); !ok || got != inside {
		t.Errorf("a file below the data folder: %q %v", got, ok)
	}
	if _, ok := ConfinedUploadPath(inside, ""); ok {
		t.Error("without the data folder as a root the file is outside")
	}
	if _, ok := ConfinedUploadPath(odd, data); ok {
		t.Error("a name with a space is not a name the console gives")
	}
	if _, ok := ConfinedUploadPath(data, data); ok {
		t.Error("the data folder itself")
	}
	if _, ok := ConfinedUploadPath(filepath.Join(data, "uploads", "..", "..", "keep.txt"), data); ok {
		t.Error("a path that climbs out of the data folder")
	}
	if RemoveUploadedFile(&UploadedFile{Path: other}, data) {
		t.Error("removed a file next to the data folder")
	}
	if _, ok := ConfinedUploadPath("/etc/passwd", data); ok {
		t.Error("/etc/passwd")
	}
	if _, ok := ConfinedUploadPath(inside+"\x00", data); ok {
		t.Error("a path with a NUL byte")
	}
	if RemoveUploadedFile(&UploadedFile{Path: "/etc/hostname"}, data) {
		t.Error("removed a file outside the roots")
	}
	if !RemoveUploadedFile(&UploadedFile{Path: inside}, data) {
		t.Error("a file below the data folder was not removed")
	}
	if RemoveUploadedFile(&UploadedFile{Path: inside}, data) {
		t.Error("a file that is gone cannot be removed again")
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("an unrelated file disappeared")
	}
}

func TestStaticFoldersMaxAge(t *testing.T) {
	site := t.TempDir()
	for _, f := range []string{"public/favicon.svg", "public/img/logo.png", "protos/message.proto", "uploads/logo.png", "wallpapers/w.jpg"} {
		full := filepath.Join(site, filepath.FromSlash(f))
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		_ = os.WriteFile(full, []byte("x"), 0o644)
	}
	opts := StaticFoldersOptions{PublicDir: filepath.Join(site, "public"), ProtosDir: filepath.Join(site, "protos"),
		UploadsDir: filepath.Join(site, "uploads"), WallpapersDir: filepath.Join(site, "wallpapers")}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	get := func(h http.Handler, target string) *httptest.ResponseRecorder {
		return serve(h, httptest.NewRequest("GET", target, nil))
	}
	// Without a Cache-Control from an earlier step the folders' own rule shows
	production := opts
	production.Production = true
	for target, want := range map[string]string{
		"/favicon.svg":          "public, max-age=604800",
		"/protos/message.proto": "public, max-age=604800",
		"/uploads/logo.png":     "public, max-age=2592000",
		"/wallpapers/w.jpg":     "public, max-age=2592000, immutable",
	} {
		if got := get(StaticFolders(production)(next), target).Header().Get("Cache-Control"); got != want {
			t.Errorf("production %s: %q, want %q", target, got, want)
		}
	}
	for target, want := range map[string]string{"/favicon.svg": "public, max-age=0", "/wallpapers/w.jpg": "public, max-age=0, immutable"} {
		if got := get(StaticFolders(opts)(next), target).Header().Get("Cache-Control"); got != want {
			t.Errorf("development %s: %q, want %q", target, got, want)
		}
	}
	// What is no file goes on
	for _, target := range []string{"/missing.png", "/api/devices", "/uploads/../public/favicon.svg", "/wallpapers/"} {
		if rec := get(StaticFolders(opts)(next), target); rec.Code != http.StatusTeapot {
			t.Errorf("%s: status %d, want the next handler", target, rec.Code)
		}
	}
	// A folder that is not configured or does not exist is skipped
	partial := StaticFoldersOptions{PublicDir: filepath.Join(site, "public"), WallpapersDir: filepath.Join(site, "none")}
	if rec := get(StaticFolders(partial)(next), "/wallpapers/w.jpg"); rec.Code != http.StatusTeapot {
		t.Errorf("missing wallpapers folder: %d", rec.Code)
	}
	if rec := get(StaticFolders(partial)(next), "/protos/message.proto"); rec.Code != http.StatusTeapot {
		t.Errorf("unset protos folder: %d", rec.Code)
	}
}

// failingOpen finds every file of the real disk but cannot open one.
type failingOpen struct{}

func (failingOpen) Stat(name string) (fs.FileInfo, error) { return os.Stat(name) }

func (failingOpen) Open(string) (io.ReadSeekCloser, error) { return nil, errors.New("disk on fire") }

// A precondition that does not hold and a range outside the file are the
// client's answer, not a failure of the server: the step answers 412 and
// 416 itself, with the file's validators and no body, and OnError is not
// called (M6). A file that cannot be read still goes to OnError.
func TestStaticFoldersAnswerPreconditionAndRange(t *testing.T) {
	site := t.TempDir()
	full := filepath.Join(site, "uploads", "big.bin")
	_ = os.MkdirAll(filepath.Dir(full), 0o755)
	_ = os.WriteFile(full, bytes.Repeat([]byte("x"), 1000), 0o644)
	var called []int
	opts := StaticFoldersOptions{UploadsDir: filepath.Join(site, "uploads"), Production: true,
		OnError: func(w http.ResponseWriter, r *http.Request, s int, _ error) {
			called = append(called, s)
			httpx.WriteJSON(w, r, s, map[string]any{"success": false, "error": "Internal Server Error"})
		}}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	h := SecurityHeaders(SecurityOptions{})(StaticFolders(opts)(next))
	cases := []struct {
		method, name, value string
		status              int
		contentRange        string
	}{
		{"GET", "If-Match", `"nope"`, 412, ""},
		{"HEAD", "If-Match", `"nope"`, 412, ""},
		{"GET", "If-Unmodified-Since", "Sat, 03 Jun 2000 00:00:00 GMT", 412, ""},
		{"GET", "Range", "bytes=5000-", 416, "bytes */1000"},
		{"HEAD", "Range", "bytes=5-2", 416, "bytes */1000"},
	}
	for _, c := range cases {
		called = nil
		req := httptest.NewRequest(c.method, "/uploads/big.bin", nil)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(c.name, c.value)
		rec := serve(h, req)
		hd := rec.Header()
		if rec.Code != c.status || rec.Body.Len() != 0 || len(called) != 0 {
			t.Errorf("%s with %s %s: %d %q, OnError %v; want %d without a body", c.method, c.name, c.value, rec.Code, rec.Body.String(), called, c.status)
		}
		if hd.Get("Content-Type") != "" || hd.Get("Content-Length") != "0" || hd.Get("ETag") == "" || hd.Get("Last-Modified") == "" || hd.Get("Content-Range") != c.contentRange {
			t.Errorf("%s with %s %s: headers %v", c.method, c.name, c.value, hd)
		}
		if hd.Get("X-Frame-Options") != "DENY" {
			t.Errorf("%s with %s %s: the security headers are gone: %v", c.method, c.name, c.value, hd)
		}
	}
	// What holds is still answered with the file
	req := httptest.NewRequest("GET", "/uploads/big.bin", nil)
	req.Header.Set("Range", "bytes=0-9")
	if rec := serve(h, req); rec.Code != 206 || rec.Body.String() != "xxxxxxxxxx" {
		t.Errorf("a range in the file: %d %q", rec.Code, rec.Body.String())
	}
	// A file that cannot be read is a failure of the server
	called = nil
	broken := opts
	broken.FS = failingOpen{}
	rec := serve(StaticFolders(broken)(next), httptest.NewRequest("GET", "/uploads/big.bin", nil))
	if rec.Code != 500 || len(called) != 1 || called[0] != 500 {
		t.Errorf("a file that cannot be opened: %d, OnError %v", rec.Code, called)
	}
}

// The security headers run first and set Cache-Control for everything;
// the folders' max-age replaces it on their files (S15). What is no file
// keeps the security headers' rule.
func TestStaticFoldersMaxAgeAppliesBehindSecurityHeaders(t *testing.T) {
	site := t.TempDir()
	for _, f := range []string{"public/favicon.svg", "public/img/logo.png", "fonts/a.woff2", "protos/message.proto", "uploads/logo.png", "wallpapers/w.jpg"} {
		full := filepath.Join(site, filepath.FromSlash(f))
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		_ = os.WriteFile(full, []byte("x"), 0o644)
	}
	opts := StaticFoldersOptions{PublicDir: filepath.Join(site, "public"), FontsDir: filepath.Join(site, "fonts"), ProtosDir: filepath.Join(site, "protos"),
		UploadsDir: filepath.Join(site, "uploads"), WallpapersDir: filepath.Join(site, "wallpapers")}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	production := opts
	production.Production = true
	cases := []struct {
		opts   StaticFoldersOptions
		target string
		want   string
	}{
		{production, "/favicon.svg", "public, max-age=604800"},
		{production, "/img/logo.png", "public, max-age=604800"},
		{production, "/fonts/a.woff2", "public, max-age=604800"},
		{production, "/protos/message.proto", "public, max-age=604800"},
		{production, "/uploads/logo.png", "public, max-age=2592000"},
		{production, "/wallpapers/w.jpg", "public, max-age=2592000, immutable"},
		{opts, "/favicon.svg", "public, max-age=0"},
		{opts, "/img/logo.png", "public, max-age=0"},
		{opts, "/uploads/logo.png", "public, max-age=0"},
		{opts, "/wallpapers/w.jpg", "public, max-age=0, immutable"},
	}
	for _, c := range cases {
		for _, https := range []bool{false, true} {
			h := SecurityHeaders(SecurityOptions{HTTPS: https})(StaticFolders(c.opts)(next))
			rec := serve(h, httptest.NewRequest("GET", c.target, nil))
			if rec.Code != 200 || rec.Header().Get("Cache-Control") != c.want {
				t.Errorf("production %v, HTTPS %v, %s: %d %q, want %q", c.opts.Production, https, c.target, rec.Code, rec.Header().Get("Cache-Control"), c.want)
			}
		}
	}
	h := SecurityHeaders(SecurityOptions{})(StaticFolders(production)(next))
	for target, want := range map[string]string{"/img/missing.png": "public, max-age=3600", "/missing.png": "no-store"} {
		rec := serve(h, httptest.NewRequest("GET", target, nil))
		if rec.Code != http.StatusTeapot || rec.Header().Get("Cache-Control") != want {
			t.Errorf("%s: %d %q, want the next handler with %q", target, rec.Code, rec.Header().Get("Cache-Control"), want)
		}
	}
}
