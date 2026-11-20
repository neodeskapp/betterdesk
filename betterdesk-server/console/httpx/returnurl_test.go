package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
)

type vector struct {
	In  string  `json:"in"`
	Out *string `json:"out"`
}

func loadVectors(t testing.TB, name string) []vector {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var vectors []vector
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	return vectors
}

func short(s string) string {
	if len(s) > 120 {
		return s[:120] + "..."
	}
	return s
}

// Every input with the answer Node's safeReturnPath gave for it
// (testdata/returnurl_vectors.json, written by
// .ui-rework/go/tools/webui/gen-returnurl-vectors.js).
func TestSafeReturnPathMatchesNode(t *testing.T) {
	vectors := loadVectors(t, "returnurl_vectors.json")
	if len(vectors) < 3000 {
		t.Fatalf("only %d vectors", len(vectors))
	}
	accepted, failed, resolved := 0, 0, 0
	for _, v := range vectors {
		got, ok := SafeReturnPath(v.In)
		if v.Out != nil && hasDotSegment(*v.Out) {
			// W1: Node's parser left the dot segments in; the browser
			// resolves them, and so does SafeReturnPath before its checks
			resolved++
			want, wantOK := resolvedByBrowser(*v.Out)
			if ok != wantOK || got != want {
				t.Errorf("SafeReturnPath(%q) = %q, %v; Node gives %q, which the browser reads as %q (%v)", short(v.In), short(got), ok, short(*v.Out), short(want), wantOK)
				failed++
			}
			accepted++
			continue
		}
		switch {
		case v.Out == nil && ok:
			t.Errorf("SafeReturnPath(%q) = %q, Node refuses it", short(v.In), short(got))
			failed++
		case v.Out != nil && !ok:
			t.Errorf("SafeReturnPath(%q) refused, Node gives %q", short(v.In), short(*v.Out))
			failed++
		case v.Out != nil && got != *v.Out:
			t.Errorf("SafeReturnPath(%q) = %q, Node gives %q", short(v.In), short(got), short(*v.Out))
			failed++
		}
		if v.Out != nil {
			accepted++
		}
		if failed > 40 {
			t.Fatal("too many differences")
		}
	}
	if accepted < 1000 || len(vectors)-accepted < 1000 {
		t.Errorf("%d accepted and %d refused vectors: both kinds must be well covered", accepted, len(vectors)-accepted)
	}
	if resolved == 0 || resolved*10 > len(vectors) {
		t.Errorf("%d of %d vectors where Node kept dot segments, expected some but under a tenth", resolved, len(vectors))
	}
	t.Logf("%d vectors compared with the path the browser makes of Node's answer (W1)", resolved)
}

// hasDotSegment reports whether the path of a return path still has a "."
// or ".." segment.
func hasDotSegment(returnPath string) bool {
	path := returnPath
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	for _, segment := range strings.Split(path, "/") {
		if isSingleDot(segment) || isDoubleDot(segment) {
			return true
		}
	}
	return false
}

// resolvedByBrowser is what a browser makes of a return path whose
// characters are already encoded, by the URL standard's path state: a "."
// segment goes, a ".." segment takes the one before it with it, and either
// one at the end leaves a slash. It also says whether the console may send
// someone there.
func resolvedByBrowser(returnPath string) (string, bool) {
	path, rest := returnPath, ""
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path, rest = path[:i], path[i:]
	}
	parts := strings.Split(path[1:], "/")
	var kept []string
	for i, part := range parts {
		last := i == len(parts)-1
		switch {
		case part == ".." || strings.EqualFold(part, ".%2e") || strings.EqualFold(part, "%2e.") || strings.EqualFold(part, "%2e%2e"):
			if len(kept) > 0 {
				kept = kept[:len(kept)-1]
			}
			if last {
				kept = append(kept, "")
			}
		case part == "." || strings.EqualFold(part, "%2e"):
			if last {
				kept = append(kept, "")
			}
		default:
			kept = append(kept, part)
		}
	}
	path = "/" + strings.Join(kept, "/")
	if strings.HasPrefix(path, "//") || blockedPrefix.MatchString(path) {
		return "", false
	}
	return path + rest, true
}

func TestSafeReturnPathKeepsConsolePaths(t *testing.T) {
	for in, want := range map[string]string{
		"/devices":              "/devices",
		"/devices?open=12345":   "/devices?open=12345",
		"/app/devices?open=1#x": "/app/devices?open=1#x",
		"/settings?tab=auth":    "/settings?tab=auth",
		"/remote/123456789":     "/remote/123456789",
		"/":                     "/",
	} {
		if got, ok := SafeReturnPath(in); !ok || got != want {
			t.Errorf("SafeReturnPath(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

func TestSafeReturnPathRefuses(t *testing.T) {
	for name, in := range map[string]string{
		"an absolute URL":                 "https://evil.example/",
		"a protocol-relative URL":         "//evil.example/x",
		"a backslash host":                `/\evil.example`,
		"a backslash anywhere":            `/devices\..\x`,
		"a tab that browsers drop":        "/\t/evil.example",
		"a newline":                       "/devices\r\nSet-Cookie: x=1",
		"an encoded backslash":            "/%5Cevil.example",
		"an encoded newline":              "/devices%0d%0aSet-Cookie:%20x=1",
		"dot segments that end as a host": "/..//evil.example",
		"encoded dot segments":            "/.%2e//evil.example",
		"a scheme":                        "javascript:alert(1)",
		"a relative path":                 "devices",
		"the sign-in page":                "/login?return=/devices",
		"the new sign-in page":            "/app/login",
		"the remote sign-in page":         "/remote/login",
		"sign-out":                        "/logout",
		"the API":                         "/api/auth/logout",
		"nothing":                         "",
		"a very long path":                "/" + strings.Repeat("a", 3000),
		"a sign-in page in capitals":      "/LOGIN/",
		"a socket":                        "/ws",
		"a sign-in page before a space":   "/login ",
	} {
		if got, ok := SafeReturnPath(in); ok {
			t.Errorf("%s: SafeReturnPath(%q) = %q, want it refused", name, short(in), short(got))
		}
	}
}

// What a browser's URL parser changes on the way.
func TestSafeReturnPathNormalises(t *testing.T) {
	for in, want := range map[string]string{
		"/a/./b/../c":             "/a/c",
		"/a/%2E%2e/b":             "/b",
		"/a/..":                   "/",
		"/a b?c d#e f":            "/a%20b?c%20d#e%20f",
		"/a  ":                    "/a",
		"/a?":                     "/a",
		"/a#":                     "/a",
		"/a?#b":                   "/a#b",
		"/café?q='x'#`":           "/caf%C3%A9?q=%27x%27#%60",
		"/{x}|^[y]`":              "/%7Bx%7D|^[y]%60",
		"/100%":                   "/100%",
		"/apix":                   "/apix",
		"/%6Cogin":                "/%6Cogin",
		"/x?return=//evil":        "/x?return=//evil",
		"/\U0001F600":             "/%F0%9F%98%80",
		"/a\xffb":                 "/a%EF%BF%BDb",
		"/x#a#b?c":                "/x#a#b?c",
		"/remote/900100001?a=1&b": "/remote/900100001?a=1&b",
	} {
		if got, ok := SafeReturnPath(in); !ok || got != want {
			t.Errorf("SafeReturnPath(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

// Dot segments are resolved in every path before the checks, also after a
// name that starts with a dot, where Node 22's URL parser left them alone
// (W1): the browser resolves them, so /a/.b/../../logout is /logout.
func TestSafeReturnPathResolvesDotSegmentsAfterADotName(t *testing.T) {
	for in, want := range map[string]string{
		"/a/.b/../x":               "/a/x",
		"/a/.b/./c":                "/a/.b/c",
		"/a/.b/..?x y":             "/a/?x%20y",
		"/a/.well-known/../../x#h": "/x#h",
		"/a/.b/../x y":             "/a/x%20y",
		"/a/.b/%2e%2e/x":           "/a/x",
		"/a./../x":                 "/x",
		"/a/b/../x":                "/a/x",
		"/a/.b":                    "/a/.b",
	} {
		if got, ok := SafeReturnPath(in); !ok || got != want {
			t.Errorf("SafeReturnPath(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"/.b/../login", "/a/.b/../../logout", "/a/.well-known/../../api/auth/logout", "/x/.y/../../login?return=/z", "/a/.b/../..//evil.example"} {
		if got, ok := SafeReturnPath(in); ok {
			t.Errorf("SafeReturnPath(%q) = %q, want it refused", in, got)
		}
	}
}

// The limit counts UTF-16 code units, as the page's JavaScript does.
func TestSafeReturnPathLengthLimit(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"/" + strings.Repeat("a", 2047), true},
		{"/" + strings.Repeat("a", 2048), false},
		{"/" + strings.Repeat("€", 2047), true},
		{"/" + strings.Repeat("€", 2048), false},
		{"/" + strings.Repeat("\U0001F600", 1023) + "a", true},
		{"/" + strings.Repeat("\U0001F600", 1024), false},
	}
	for _, c := range cases {
		if _, ok := SafeReturnPath(c.in); ok != c.want {
			t.Errorf("SafeReturnPath(%d bytes starting %q) accepted = %v, want %v", len(c.in), c.in[:8], ok, c.want)
		}
	}
}

func TestEncodeURIComponentMatchesNode(t *testing.T) {
	vectors := loadVectors(t, "encodeuricomponent_vectors.json")
	if len(vectors) < 128 {
		t.Fatalf("only %d vectors", len(vectors))
	}
	for _, v := range vectors {
		if got := EncodeURIComponent(v.In); got != *v.Out {
			t.Errorf("EncodeURIComponent(%q) = %q, Node gives %q", v.In, got, *v.Out)
		}
	}
	if got := EncodeURIComponent("a b!'()*~/?é"); got != "a%20b!'()*~%2F%3F%C3%A9" {
		t.Errorf("EncodeURIComponent = %q", got)
	}
}

func requestFor(method, target string, headers map[string]string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestLoginRedirectURL(t *testing.T) {
	app := "/app"
	evil := "//evil.example"
	cases := []struct {
		name     string
		r        *http.Request
		returnTo *string
		want     string
	}{
		{"a first visit gets the way back but no expired notice",
			requestFor("GET", "/devices?open=1", nil), nil, "/login?return=%2Fdevices%3Fopen%3D1"},
		{"a browser that still sends a session cookie was signed out",
			requestFor("GET", "/devices?open=1", map[string]string{"Cookie": "theme=x; bd.sid=s%3Aabc.def"}), nil,
			"/login?expired=1&return=%2Fdevices%3Fopen%3D1"},
		{"HEAD keeps the way back", requestFor("HEAD", "/devices", nil), nil, "/login?return=%2Fdevices"},
		{"a form post gets no return", requestFor("POST", "/devices?open=1", nil), nil, "/login"},
		{"a form post with an ended session", requestFor("POST", "/form", map[string]string{"Cookie": "betterdesk.sid=x"}), nil, "/login?expired=1"},
		{"an unsafe path gets no return", requestFor("GET", "/x", nil), &evil, "/login"},
		{"an explicit return path wins over the request URL", requestFor("GET", "/devices?open=1", nil), &app, "/login?return=%2Fapp"},
		{"an explicit return path also counts for a post", requestFor("POST", "/devices", nil), &app, "/login?return=%2Fapp"},
		{"the return path is encoded as a query value",
			requestFor("GET", "/a%20b?c='d'&e=(f)*!~", nil), nil, "/login?return=%2Fa%2520b%3Fc%3D%2527d%2527%26e%3D(f)*!~"},
	}
	for _, c := range cases {
		if got := LoginRedirectURL(c.r, c.returnTo); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}

	raw := &http.Request{Method: "GET", RequestURI: "//evil.example", Header: http.Header{}}
	if got := LoginRedirectURL(raw, nil); got != "/login" {
		t.Errorf("a request for //evil.example: %q, want /login", got)
	}
}

func TestHadSessionCookie(t *testing.T) {
	cases := map[string]bool{
		"theme=x; bd.sid=s%3Aabc.def": true,
		"betterdesk.sid=s%3Aabc":      true,
		"bd.sid":                      true,
		" \tbd.sid =x":                true,
		"a=1;bd.sid=2":                true,
		"xbd.sid=1":                   false,
		"bd.sidx=1":                   false,
		"a=bd.sid":                    false,
		"BD.SID=1":                    false,
		"":                            false,
	}
	for cookie, want := range cases {
		r := requestFor("GET", "/", map[string]string{"Cookie": cookie})
		if got := HadSessionCookie(r); got != want {
			t.Errorf("HadSessionCookie(%q) = %v, want %v", cookie, got, want)
		}
	}
	two := requestFor("GET", "/", nil)
	two.Header.Add("Cookie", "a=1")
	two.Header.Add("Cookie", "bd.sid=2")
	if !HadSessionCookie(two) {
		t.Error("a session cookie in a second Cookie header is not seen")
	}
	if HadSessionCookie(requestFor("GET", "/", nil)) {
		t.Error("a request without cookies had a session cookie")
	}
}

func TestOriginalURL(t *testing.T) {
	if got := OriginalURL(requestFor("GET", "/a%20b?c=d", nil)); got != "/a%20b?c=d" {
		t.Errorf("OriginalURL = %q", got)
	}
	raw := &http.Request{Method: "GET", RequestURI: "/caf\xc3\xa9?x=\xff"}
	if got := OriginalURL(raw); got != "/cafÃ©?x=ÿ" {
		t.Errorf("OriginalURL = %q, want the bytes read as Latin-1", got)
	}
	client := &http.Request{Method: "GET", URL: &url.URL{Path: "/a b", RawQuery: "x=1"}}
	if got := OriginalURL(client); got != "/a%20b?x=1" {
		t.Errorf("OriginalURL without a request line = %q", got)
	}
}

var (
	encodedControl = regexp.MustCompile(`(?i)%(?:0[0-9a-f]|1[0-9a-f]|7f|5c)`)
	blockedPrefix  = regexp.MustCompile(`(?i)^/(?:api|ws|socket)(?:/|$)|^/(?:app/)?(?:remote/)?(?:login|logout)(?:/|$)`)
)

// FuzzSafeReturnPath checks what must hold for every path that is let
// through, whatever the input.
func FuzzSafeReturnPath(f *testing.F) {
	for _, seed := range []string{
		"/devices?open=1#x", "/", "//evil.example", "/..//evil.example", "/.%2e//evil.example", `/\evil`, "/%5cevil",
		"/a/../login", "/LOGIN ", "/app/remote/logout/", "/a b?c d#e f", "/café", "/%", "/a?#", "/x/%2E/%2e%2E/api",
		"/\t/evil.example", "/a%0d%0ab", "https://evil.example/", "/a\xff", "/" + strings.Repeat("a", 2047), "/. /..  ",
	} {
		f.Add(seed)
	}
	base, _ := url.Parse("http://console.invalid/start")
	f.Fuzz(func(t *testing.T, in string) {
		out, ok := SafeReturnPath(in)
		if !ok {
			if out != "" {
				t.Fatalf("refused %q but answered %q", in, out)
			}
			return
		}
		if !strings.HasPrefix(out, "/") || strings.HasPrefix(out, "//") {
			t.Fatalf("%q gives %q, which does not start with exactly one slash", in, out)
		}
		for i := 0; i < len(out); i++ {
			if c := out[i]; c <= 0x20 || c >= 0x7F || c == '\\' {
				t.Fatalf("%q gives %q with the byte %#x", in, out, c)
			}
		}
		if encodedControl.MatchString(out) {
			t.Fatalf("%q gives %q with an encoded control character or backslash", in, out)
		}
		path := out
		if i := strings.IndexAny(path, "?#"); i >= 0 {
			path = path[:i]
		}
		if blockedPrefix.MatchString(path) {
			t.Fatalf("%q gives %q, a blocked path", in, out)
		}
		for _, segment := range strings.Split(path, "/") {
			if isSingleDot(segment) || isDoubleDot(segment) {
				t.Fatalf("%q gives %q with a dot segment left", in, out)
			}
		}
		p, s, h := normalizeReturnPath(out)
		if p+s+h != out {
			t.Fatalf("%q gives %q, which parses back to %q", in, out, p+s+h)
		}
		if len(out) <= maxReturnLength {
			if again, ok := SafeReturnPath(out); !ok || again != out {
				t.Fatalf("%q gives %q, which is then %q (%v)", in, out, again, ok)
			}
		}
		if u, err := url.Parse(out); err == nil {
			if u.Scheme != "" || u.Host != "" || u.Opaque != "" || u.User != nil {
				t.Fatalf("%q gives %q, which has a scheme or a host", in, out)
			}
			if to := base.ResolveReference(u); to.Host != base.Host || to.Scheme != base.Scheme {
				t.Fatalf("%q gives %q, which leads to %s", in, out, to)
			}
		}
	})
}
