package httpx

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// The tags Express's etag package gives for the same bodies.
func TestETagMatchesExpress(t *testing.T) {
	cases := map[string]string{
		"":                 `W/"0-2jmj7l5rSw0yVb/vlWAYkK/YBwk"`,
		"{}":               `W/"2-vyGp6PvFo4RvsFtPoIWeCReyIC8"`,
		`{"success":true}`: `W/"10-oV4hJxRVSENxc/wX8+mA4/Pe4tA"`,
		"{\"status\":\"ok\",\"text\":\"Zażółć <b>&</b>   x\"}": `W/"32-6NCB2WAR6NQpyHb49ioqyuLanh0"`,
	}
	for body, want := range cases {
		if got := ETag([]byte(body)); got != want {
			t.Errorf("ETag(%q) = %s, want %s", body, got, want)
		}
	}
}

func TestMarshalLooksLikeJSONStringify(t *testing.T) {
	got, err := Marshal(map[string]any{"e": "<>&", "f": nil, "a": 1.0, "b": 1e21, "c": 1e-7, "g": []any{1, "x"}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":1,"b":1e+21,"c":1e-7,"e":"<>&","f":null,"g":[1,"x"]}`
	if string(got) != want {
		t.Errorf("Marshal = %s\nwant      %s", got, want)
	}
}

func TestISOTime(t *testing.T) {
	at := time.Date(2026, 1, 2, 4, 4, 5, 60_000_000, time.FixedZone("CET", 3600))
	if got := ISOTime(at); got != "2026-01-02T03:04:05.060Z" {
		t.Errorf("ISOTime = %s", got)
	}
}

func TestFresh(t *testing.T) {
	const tag = `W/"2-vyGp6PvFo4RvsFtPoIWeCReyIC8"`
	modified := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	cases := []struct {
		name    string
		method  string
		headers map[string]string
		etag    string
		last    time.Time
		want    bool
	}{
		{"no validators", "GET", nil, tag, time.Time{}, false},
		{"matching tag", "GET", map[string]string{"If-None-Match": tag}, tag, time.Time{}, true},
		{"strong form of the weak tag", "GET", map[string]string{"If-None-Match": `"2-vyGp6PvFo4RvsFtPoIWeCReyIC8"`}, tag, time.Time{}, true},
		{"one of several", "GET", map[string]string{"If-None-Match": `"x", ` + tag}, tag, time.Time{}, true},
		{"other tag", "GET", map[string]string{"If-None-Match": `"x"`}, tag, time.Time{}, false},
		{"star", "HEAD", map[string]string{"If-None-Match": "*"}, tag, time.Time{}, true},
		{"no-cache wins", "GET", map[string]string{"If-None-Match": tag, "Cache-Control": "max-age=0, no-cache"}, tag, time.Time{}, false},
		{"POST is never fresh", "POST", map[string]string{"If-None-Match": tag}, tag, time.Time{}, false},
		{"tag matches but no last-modified for the date", "GET", map[string]string{"If-None-Match": tag, "If-Modified-Since": "Fri, 02 Jan 2026 03:04:05 GMT"}, tag, time.Time{}, false},
		{"not modified since", "GET", map[string]string{"If-Modified-Since": "Fri, 02 Jan 2026 03:04:05 GMT"}, "", modified, true},
		{"modified since", "GET", map[string]string{"If-Modified-Since": "Fri, 02 Jan 2026 03:04:04 GMT"}, "", modified, false},
		{"response without a tag", "GET", map[string]string{"If-None-Match": tag}, "", time.Time{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := httptest.NewRequest(c.method, "/", nil)
			for k, v := range c.headers {
				r.Header.Set(k, v)
			}
			if got := Fresh(r, c.etag, c.last); got != c.want {
				t.Errorf("Fresh = %v, want %v", got, c.want)
			}
		})
	}
}

func serveJSON(t *testing.T, method string, headers map[string]string, compressible bool, status int, v any) *http.Response {
	t.Helper()
	r := httptest.NewRequest(method, "/api/x", nil)
	for k, val := range headers {
		r.Header.Set(k, val)
	}
	if compressible {
		r = r.WithContext(WithJSONCompression(r.Context()))
	}
	rec := httptest.NewRecorder()
	WriteJSON(rec, r, status, v)
	return rec.Result()
}

func TestWriteJSONPlain(t *testing.T) {
	res := serveJSON(t, "POST", nil, false, 201, map[string]any{"success": true})
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 201 || string(body) != `{"success":true}` {
		t.Errorf("status %d body %s", res.StatusCode, body)
	}
	if got := res.Header.Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := res.Header.Get("Content-Length"); got != "16" {
		t.Errorf("Content-Length = %q", got)
	}
	if got := res.Header.Get("ETag"); got != `W/"10-oV4hJxRVSENxc/wX8+mA4/Pe4tA"` {
		t.Errorf("ETag = %q", got)
	}
	if res.Header.Get("Content-Encoding") != "" || res.Header.Get("Vary") != "" {
		t.Errorf("unexpected encoding headers: %v", res.Header)
	}
}

func TestWriteJSONNotModified(t *testing.T) {
	tag := `W/"10-oV4hJxRVSENxc/wX8+mA4/Pe4tA"`
	res := serveJSON(t, "GET", map[string]string{"If-None-Match": tag}, false, 200, map[string]any{"success": true})
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusNotModified || len(body) != 0 {
		t.Errorf("status %d body %q, want 304 without a body", res.StatusCode, body)
	}
	if res.Header.Get("Content-Type") != "" {
		t.Errorf("a 304 carries Content-Type %q", res.Header.Get("Content-Type"))
	}

	// An error answer is never turned into 304
	res = serveJSON(t, "GET", map[string]string{"If-None-Match": "*"}, false, 404, map[string]any{"success": false})
	if res.StatusCode != 404 {
		t.Errorf("status %d, want 404", res.StatusCode)
	}
}

func TestWriteJSONCompression(t *testing.T) {
	big := map[string]any{"data": strings.Repeat("device ", 400)}
	gz := map[string]string{"Accept-Encoding": "br, gzip"}

	res := serveJSON(t, "GET", gz, true, 200, big)
	if got := res.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := res.Header.Get("Vary"); got != "Accept-Encoding" {
		t.Errorf("Vary = %q", got)
	}
	if res.Header.Get("Content-Length") != "" {
		t.Errorf("a compressed answer carries Content-Length %q of the plain body", res.Header.Get("Content-Length"))
	}
	if res.Header.Get("ETag") == "" {
		t.Error("a compressed GET has no ETag")
	}
	zr, err := gzip.NewReader(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	plain, _ := io.ReadAll(zr)
	want, _ := Marshal(big)
	if string(plain) != string(want) {
		t.Errorf("decompressed body differs from the JSON")
	}

	// A compressed answer to a write has no ETag
	if res := serveJSON(t, "POST", gz, true, 200, big); res.Header.Get("Content-Encoding") != "gzip" || res.Header.Get("ETag") != "" {
		t.Errorf("POST: encoding %q etag %q", res.Header.Get("Content-Encoding"), res.Header.Get("ETag"))
	}

	// Not compressed: small body, request not marked, client does not accept gzip, gzip refused with q=0
	for name, res := range map[string]*http.Response{
		"small":      serveJSON(t, "GET", gz, true, 200, map[string]any{"ok": true}),
		"not marked": serveJSON(t, "GET", gz, false, 200, big),
		"no accept":  serveJSON(t, "GET", nil, true, 200, big),
		"q=0":        serveJSON(t, "GET", map[string]string{"Accept-Encoding": "gzip;q=0, identity"}, true, 200, big),
	} {
		if got := res.Header.Get("Content-Encoding"); got != "" {
			t.Errorf("%s: Content-Encoding = %q, want none", name, got)
		}
	}
}

func TestWriteJSONHead(t *testing.T) {
	res := serveJSON(t, "HEAD", nil, false, 200, map[string]any{"success": true})
	body, _ := io.ReadAll(res.Body)
	if len(body) != 0 || res.Header.Get("Content-Length") != "16" {
		t.Errorf("HEAD: body %q, Content-Length %q", body, res.Header.Get("Content-Length"))
	}
}
