package contract

import (
	"net/http"
	"reflect"
	"testing"
)

func TestNormalizeHeadersKeepsTheAllowlist(t *testing.T) {
	header := http.Header{}
	header.Set("Content-Type", "application/json; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	header.Set("Date", "Tue, 10 Nov 2026 01:12:19 GMT")
	header.Set("Content-Length", "2645")
	header.Set("Connection", "keep-alive")
	header.Set("Keep-Alive", "timeout=5")
	header.Set("X-Request-Id", "abc")
	header.Set("ETag", `W/"a55-p+8wZs0R24JwSjepEd3VXPsipRE"`)
	header.Set("Last-Modified", "Tue, 10 Nov 2026 01:00:00 GMT")
	header.Set("RateLimit-Policy", "1000000;w=60")
	header.Set("RateLimit-Limit", "1000000")
	header.Set("RateLimit-Remaining", "999997")
	header.Set("RateLimit-Reset", "58")
	header.Set("Retry-After", "17")
	header.Set("Access-Control-Allow-Origin", "tauri://localhost")
	header.Set("Access-Control-Max-Age", "86400")
	header.Set("X-Frame-Options", "DENY")
	header.Add("Vary", "Accept-Encoding")
	header.Add("Vary", "Origin")

	got := NormalizeHeaders(header, NewNormalizer("http://bdgo-c1:5000", ""))
	want := map[string][]string{
		"content-type":                {"application/json; charset=utf-8"},
		"cache-control":               {"no-store"},
		"etag":                        {"<present>"},
		"last-modified":               {"<present>"},
		"ratelimit-policy":            {"1000000;w=60"},
		"ratelimit-limit":             {"1000000"},
		"ratelimit-remaining":         {"<n>"},
		"ratelimit-reset":             {"<n>"},
		"retry-after":                 {"<n>"},
		"access-control-allow-origin": {"tauri://localhost"},
		"access-control-max-age":      {"86400"},
		"x-frame-options":             {"DENY"},
		"vary":                        {"Accept-Encoding", "Origin"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("headers:\n got %v\nwant %v", got, want)
	}
}

func TestNormalizeSetCookie(t *testing.T) {
	cases := map[string]string{
		"bd.sid=s%3Aabc.def; Path=/; Expires=Tue, 10 Nov 2026 01:42:20 GMT; HttpOnly; SameSite=Lax": "bd.sid=<value>; Path=/; Expires=<date>; HttpOnly; SameSite=Lax",
		"bd.guest=; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT; HttpOnly; SameSite=Lax":          "bd.guest=; Path=/; Expires=<past>; HttpOnly; SameSite=Lax",
		"__csrf_h=a21957%7C8dc98f; Path=/; HttpOnly; SameSite=Lax":                                  "__csrf_h=<value>; Path=/; HttpOnly; SameSite=Lax",
		"lang=de; Max-Age=31536000; Path=/; Secure":                                                 "lang=<value>; Max-Age=<n>; Path=/; Secure",
		"gone=x; Max-Age=0; Path=/":                                                                 "gone=<value>; Max-Age=0; Path=/",
		"flag":                                                                                      "flag",
	}
	for in, want := range cases {
		if got := normalizeSetCookie(in); got != want {
			t.Errorf("normalizeSetCookie(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}

func TestSetCookieHeadersAreSortedByName(t *testing.T) {
	header := http.Header{}
	header.Add("Set-Cookie", "bd.sid=two; Path=/")
	header.Add("Set-Cookie", "bd.guest=; Path=/")
	header.Add("Set-Cookie", "bd.sid=; Path=/login")
	got := NormalizeHeaders(header, nil)["set-cookie"]
	want := []string{"bd.guest=; Path=/", "bd.sid=<value>; Path=/", "bd.sid=; Path=/login"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("set-cookie = %q, want %q (by name, the same name in the order sent)", got, want)
	}
}

func TestNormalizeHeadersTakesOutTheInstance(t *testing.T) {
	header := http.Header{}
	header.Set("Location", "http://bdgo-c1:5000/login?return=%2Fdevices")
	header.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'nonce-r4nd0m+/='; connect-src ws://bdgo-c1:5000")
	header.Set("Expires", "Tue, 10 Nov 2026 01:42:20 GMT")
	header.Set("Pragma", "no-cache")
	got := NormalizeHeaders(header, NewNormalizer("http://bdgo-c1:5000", "172.18.0.5"))
	want := map[string][]string{
		"location":                {"<base>/login?return=%2Fdevices"},
		"content-security-policy": {"default-src 'self'; script-src 'self' 'nonce-<nonce>'; connect-src ws://<host>"},
		"expires":                 {"<date>"},
		"pragma":                  {"no-cache"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("headers:\n got %v\nwant %v", got, want)
	}

	header = http.Header{}
	header.Set("Expires", "0")
	if got := NormalizeHeaders(header, nil)["expires"]; !reflect.DeepEqual(got, []string{"0"}) {
		t.Errorf("Expires: 0 became %q, want it kept", got)
	}
}

// The console's own address, which the network tools report when a case
// points them at the console, is masked like the client's.
func TestNormalizerConsoleIP(t *testing.T) {
	n := NewNormalizer("http://bdgo-c1:5000", "172.18.0.5").withConsoleIP("172.18.0.9")
	cases := map[string]string{
		`{"host":"bdgo-c1","ip":"172.18.0.9"}`:   `{"host":"<hostname>","ip":"<console-ip>"}`,
		"connect ECONNREFUSED 172.18.0.9:1":      "connect ECONNREFUSED <console-ip>:1",
		"172.18.0.9 asked by 172.18.0.5":         "<console-ip> asked by <client-ip>",
		"172.18.0.90 and 2172.18.0.9 stay as is": "172.18.0.90 and 2172.18.0.9 stay as is",
	}
	for in, want := range cases {
		if got := n.Text(in); got != want {
			t.Errorf("Text(%q) = %q, want %q", in, got, want)
		}
	}
	if got := NewNormalizer("http://localhost:5000", "").withConsoleIP("127.0.0.1").Text("127.0.0.1:1"); got != "127.0.0.1:1" {
		t.Errorf("a console on loopback: %q", got)
	}
}

func TestNormalizerText(t *testing.T) {
	n := NewNormalizer("http://bdgo-c1:5000", "172.18.0.5")
	cases := map[string]string{
		"see http://bdgo-c1:5000/x":                    "see <base>/x",
		"wss://bdgo-c1:5000/ws":                        "wss://<host>/ws",
		"host bdgo-c1 only":                            "host <hostname> only",
		"ip=172.18.0.5; ua=x":                          "ip=<client-ip>; ua=x",
		"::ffff:172.18.0.5":                            "::ffff:<client-ip>",
		"172.18.0.5":                                   "<client-ip>",
		"172.18.0.5,172.18.0.5":                        "<client-ip>,<client-ip>",
		"from 172.18.0.5.":                             "from <client-ip>.",
		"172.18.0.50 and 1172.18.0.5 and 172.18.0.5.1": "172.18.0.50 and 1172.18.0.5 and 172.18.0.5.1",
		"nothing here":                                 "nothing here",
	}
	for in, want := range cases {
		if got := n.Text(in); got != want {
			t.Errorf("Text(%q) = %q, want %q", in, got, want)
		}
	}
	var none *Normalizer
	if got := none.Text("http://bdgo-c1:5000"); got != "http://bdgo-c1:5000" {
		t.Errorf("a nil Normalizer changed the text to %q", got)
	}
}

func jsonHeader(contentType string) http.Header {
	h := http.Header{}
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	return h
}

func TestNewAnswerBodies(t *testing.T) {
	n := NewNormalizer("http://bdgo-c1:5000", "")

	a := NewAnswer(200, jsonHeader("application/json; charset=utf-8"), []byte(`{"b":12345678901234567890,"a":"http://bdgo-c1:5000/x","c":1.50}`), false, n)
	if a.Status != 200 || a.Body.Kind != BodyJSON {
		t.Fatalf("JSON answer = %+v", a)
	}
	if got, want := string(a.Body.JSON), `{"a":"<base>/x","b":12345678901234567890,"c":1.50}`; got != want {
		t.Errorf("JSON body = %s, want %s (keys sorted, numbers as written, address replaced)", got, want)
	}

	a = NewAnswer(200, jsonHeader("application/problem+json"), []byte(`[1]`), false, n)
	if a.Body.Kind != BodyJSON {
		t.Errorf("+json body kind = %q, want json", a.Body.Kind)
	}

	a = NewAnswer(500, jsonHeader("application/json"), []byte(`{"cut off`), false, n)
	if a.Body.Kind != BodyBytes || a.Body.Type != "application/json" || a.Body.Length != 9 || a.Body.Head != `{"cut off` {
		t.Errorf("broken JSON body = %+v, want bytes with the head kept", a.Body)
	}

	html := []byte("<!doctype html><title>x</title>" + string(make([]byte, 400)))
	a = NewAnswer(200, jsonHeader("text/html; charset=utf-8"), html, false, n)
	if a.Body.Kind != BodyBytes || a.Body.Type != "text/html" || a.Body.Length != len(html) || len(a.Body.Head) != headBytes || len(a.Body.SHA256) != 64 {
		t.Errorf("HTML body = %+v", a.Body)
	}

	png := []byte{0x89, 'P', 'N', 'G', 0xff}
	a = NewAnswer(200, jsonHeader("image/png"), png, false, n)
	if a.Body.Kind != BodyBytes || a.Body.Head != "" || a.Body.Length != 5 {
		t.Errorf("PNG body = %+v, want bytes without a head", a.Body)
	}

	if a = NewAnswer(204, jsonHeader(""), nil, false, n); a.Body.Kind != BodyEmpty {
		t.Errorf("no body: kind %q, want empty", a.Body.Kind)
	}
	if a = NewAnswer(200, jsonHeader("application/json"), []byte("xx"), true, n); a.Body.Kind != BodyEncoded {
		t.Errorf("undecoded body: kind %q, want encoded", a.Body.Kind)
	}
	if a = NewAnswer(200, jsonHeader("text/event-stream"), nil, false, n); a.Body.Kind != BodyStream {
		t.Errorf("event stream: kind %q, want stream", a.Body.Kind)
	}
}

func TestBodyHashIsTakenAfterTheAddressIsReplaced(t *testing.T) {
	one := NewAnswer(200, jsonHeader("text/html"), []byte("<a href=http://bdgo-c1:5000/x>"), false, NewNormalizer("http://bdgo-c1:5000", ""))
	two := NewAnswer(200, jsonHeader("text/html"), []byte("<a href=http://bdgo-other:5000/x>"), false, NewNormalizer("http://bdgo-other:5000", ""))
	if one.Body.SHA256 != two.Body.SHA256 || one.Body.Length != two.Body.Length || one.Body.Head != "<a href=<base>/x>" {
		t.Errorf("the same page from two instances differs: %+v and %+v", one.Body, two.Body)
	}
}
