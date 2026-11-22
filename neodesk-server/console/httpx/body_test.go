package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func writeBody(method string, headers map[string]string, preset map[string]string, status int, contentType, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/page", nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	for k, v := range preset {
		rec.Header().Set(k, v)
	}
	WriteBody(rec, r, status, contentType, []byte(body))
	return rec
}

// The tags Express gives the same bodies sent with res.send.
func TestWriteBody(t *testing.T) {
	rec := writeBody("GET", nil, nil, 200, "text/html; charset=utf-8", "<p>Zażółć</p>")
	if rec.Code != 200 || rec.Body.String() != "<p>Zażółć</p>" {
		t.Errorf("status %d, body %q", rec.Code, rec.Body.String())
	}
	h := rec.Header()
	if h.Get("Content-Type") != "text/html; charset=utf-8" || h.Get("Content-Length") != "17" || h.Get("ETag") != ETag([]byte("<p>Zażółć</p>")) {
		t.Errorf("headers %v", h)
	}
	if got := writeBody("GET", nil, nil, 404, "text/plain; charset=utf-8", "Not Found").Header().Get("ETag"); got != `W/"9-0gXL1ngzMqISxa6S1zx3F4wtLyg"` {
		t.Errorf("ETag of \"Not Found\": %s", got)
	}

	head := writeBody("HEAD", nil, nil, 200, "text/plain; charset=utf-8", "hello")
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") != "5" || head.Header().Get("ETag") == "" {
		t.Errorf("HEAD: %d, body %q, headers %v", head.Code, head.Body.String(), head.Header())
	}

	// Headers that are set already: the tag stays, the type is replaced
	kept := writeBody("GET", nil, map[string]string{"ETag": `"mine"`, "Content-Type": "text/x-old"}, 200, "text/plain; charset=utf-8", "hello")
	if kept.Header().Get("ETag") != `"mine"` || kept.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Errorf("preset headers: %v", kept.Header())
	}
	untyped := writeBody("GET", nil, map[string]string{"Content-Type": "text/x-old"}, 200, "", "hello")
	if untyped.Header().Get("Content-Type") != "text/x-old" {
		t.Errorf("an empty content type replaced %q", untyped.Header().Get("Content-Type"))
	}
}

// A body sent with res.send answers 304 to a matching If-None-Match, also
// when it says Cache-Control: no-store (seen on the Node console: a page
// answers 304 with its no-store header and no body).
func TestWriteBodyNotModified(t *testing.T) {
	const body = "<html>page</html>"
	tag := ETag([]byte(body))
	noStore := map[string]string{"Cache-Control": "no-store"}
	cases := []struct {
		name    string
		method  string
		headers map[string]string
		status  int
		want    int
	}{
		{"matching tag", "GET", map[string]string{"If-None-Match": tag}, 200, 304},
		{"matching tag on HEAD", "HEAD", map[string]string{"If-None-Match": tag}, 200, 304},
		{"any tag", "GET", map[string]string{"If-None-Match": "*"}, 200, 304},
		{"a 2xx other than 200", "GET", map[string]string{"If-None-Match": tag}, 201, 304},
		{"another tag", "GET", map[string]string{"If-None-Match": `"other"`}, 200, 200},
		{"a forced reload", "GET", map[string]string{"If-None-Match": tag, "Cache-Control": "no-cache"}, 200, 200},
		{"POST", "POST", map[string]string{"If-None-Match": tag}, 200, 200},
		{"an error keeps its status", "GET", map[string]string{"If-None-Match": tag}, 404, 404},
		{"a redirect keeps its status", "GET", map[string]string{"If-None-Match": "*"}, 302, 302},
		{"a date without Last-Modified", "GET", map[string]string{"If-Modified-Since": "Fri, 02 Jan 2026 03:04:05 GMT"}, 200, 200},
	}
	for _, c := range cases {
		rec := writeBody(c.method, c.headers, noStore, c.status, "text/html; charset=utf-8", body)
		if rec.Code != c.want {
			t.Errorf("%s: status %d, want %d", c.name, rec.Code, c.want)
			continue
		}
		h := rec.Header()
		if h.Get("Cache-Control") != "no-store" || h.Get("ETag") != tag {
			t.Errorf("%s: headers %v", c.name, h)
		}
		if c.want == http.StatusNotModified {
			if rec.Body.Len() != 0 || h.Get("Content-Type") != "" || h.Get("Content-Length") != "" {
				t.Errorf("%s: a 304 with body %q and headers %v", c.name, rec.Body.String(), h)
			}
		} else if c.method != "HEAD" && rec.Body.String() != body {
			t.Errorf("%s: body %q", c.name, rec.Body.String())
		}
	}

	// Last-Modified of the answer counts for If-Modified-Since
	dated := map[string]string{"Last-Modified": "Fri, 02 Jan 2026 03:04:05 GMT"}
	if rec := writeBody("GET", map[string]string{"If-Modified-Since": "Fri, 02 Jan 2026 03:04:05 GMT"}, dated, 200, "text/plain", "x"); rec.Code != 304 {
		t.Errorf("not modified since: %d", rec.Code)
	}
	if rec := writeBody("GET", map[string]string{"If-Modified-Since": "Fri, 02 Jan 2026 03:04:04 GMT"}, dated, 200, "text/plain", "x"); rec.Code != 200 {
		t.Errorf("modified since: %d", rec.Code)
	}
}

func TestWriteBodyStatusesWithoutBody(t *testing.T) {
	for _, status := range []int{204, 304} {
		rec := writeBody("GET", nil, nil, status, "text/plain; charset=utf-8", "gone")
		if rec.Code != status || rec.Body.Len() != 0 || rec.Header().Get("Content-Type") != "" || rec.Header().Get("Content-Length") != "" {
			t.Errorf("%d: body %q, headers %v", status, rec.Body.String(), rec.Header())
		}
	}
	rec := writeBody("GET", nil, nil, 205, "text/plain; charset=utf-8", "gone")
	if rec.Code != 205 || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != "0" {
		t.Errorf("205: body %q, headers %v", rec.Body.String(), rec.Header())
	}
}
