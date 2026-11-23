//go:build webuidiff

package webui

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// TestAgainstNode sends the same requests to a running Node console and to
// a Host over the same build, and compares the answers:
//
//	ND_URL=http://bdgo-w1:5000 WEB_UI_DIR=/repo/web-ui/build \
//	    go test -tags webuidiff ./console/webui/ -run TestAgainstNode -v
//
// Both must see the same directory (same files, same modification times).
// The Node console is the whole server, so the Host gets what the server's
// pipeline does before the routes: the Cache-Control it sets for every
// request (middleware/security.js and server.js). Requests are signed out;
// what a session changes is covered by the recorded answers
// (TestAnswersMatchNode).
func TestAgainstNode(t *testing.T) {
	nodeURL, dir := os.Getenv("ND_URL"), os.Getenv("WEB_UI_DIR")
	if nodeURL == "" || dir == "" {
		t.Skip("ND_URL and WEB_UI_DIR are not set")
	}
	node, err := url.Parse(nodeURL)
	if err != nil {
		t.Fatal(err)
	}
	h := New(Options{Dir: dir, Production: true})
	if h.page == nil {
		t.Fatalf("no build in %s", dir)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		presetCacheControl(w, r)
		if !h.Handle(w, r) {
			h.NotFound(w, r)
		}
	}))
	defer server.Close()
	local, _ := url.Parse(server.URL)

	requests := diffRequests(t, dir)
	if len(requests) < 150 {
		t.Fatalf("only %d requests", len(requests))
	}
	differences, compared := 0, 0
	byStatus := map[int]int{}
	for _, request := range requests {
		if reason, ok := answeredElsewhere[request.target]; ok {
			t.Logf("%s %s is not compared: %s", request.method, request.target, reason)
			continue
		}
		want, err := exchange(node.Host, request)
		if err != nil {
			t.Errorf("%s: Node: %v", request.name(), err)
			continue
		}
		got, err := exchange(local.Host, request)
		if err != nil {
			t.Errorf("%s: Go: %v", request.name(), err)
			continue
		}
		compared++
		byStatus[want.status]++
		if problems := compareAnswers(got, want); len(problems) > 0 {
			differences++
			t.Errorf("%s\n    %s", request.name(), strings.Join(problems, "\n    "))
		}
	}
	t.Logf("%d requests compared, %d differ; Node's statuses: %v", compared, differences, byStatus)
}

// answeredElsewhere are requests the Node server answers before they reach
// the web app, so the answers say nothing about this package.
var answeredElsewhere = map[string]string{
	"/fonts":               "web-nodejs/public has a fonts folder, and the server's static mount for it redirects to /fonts/",
	"/img":                 "web-nodejs/public has an img folder, and the server's static mount for it redirects to /img/",
	"/_ui/immutable/../..": "the server's static mount for web-nodejs/public resolves the path to its own folder and redirects",
}

// presetCacheControl does what the Node server does to every response
// before its routes run: no-store, an hour for /css/, /img/ and /fonts/,
// and the stricter no-store for paths that do not end like a static file.
func presetCacheControl(w http.ResponseWriter, r *http.Request) {
	path := parseRequest(r).path
	value := "no-store"
	if strings.HasPrefix(path, "/css/") || strings.HasPrefix(path, "/img/") || strings.HasPrefix(path, "/fonts/") {
		value = "public, max-age=3600"
	}
	static := false
	for _, ext := range []string{"js", "css", "png", "jpg", "jpeg", "gif", "svg", "ico", "woff", "woff2", "ttf", "eot", "map", "proto"} {
		if strings.HasSuffix(path, "."+ext) {
			static = true
		}
	}
	if !static {
		value = "no-cache, no-store, must-revalidate"
	}
	w.Header().Set("Cache-Control", value)
}

type diffRequest struct {
	method, target string
	headers        [][2]string
}

func (d diffRequest) name() string {
	return fmt.Sprintf("%s %q %v", d.method, d.target, d.headers)
}

type diffAnswer struct {
	status int
	header http.Header
	body   []byte
}

// exchange sends one request as written (the request target is not parsed
// or cleaned on the way) and reads the answer.
func exchange(host string, request diffRequest) (diffAnswer, error) {
	conn, err := net.DialTimeout("tcp", host, 5*time.Second)
	if err != nil {
		return diffAnswer{}, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	var out bytes.Buffer
	fmt.Fprintf(&out, "%s %s HTTP/1.1\r\nHost: %s\r\nConnection: close\r\n", request.method, request.target, host)
	for _, header := range request.headers {
		fmt.Fprintf(&out, "%s: %s\r\n", header[0], header[1])
	}
	if request.method == "POST" {
		out.WriteString("Content-Length: 0\r\n")
	}
	out.WriteString("\r\n")
	if _, err := conn.Write(out.Bytes()); err != nil {
		return diffAnswer{}, err
	}
	res, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: request.method})
	if err != nil {
		return diffAnswer{}, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return diffAnswer{}, err
	}
	return diffAnswer{status: res.StatusCode, header: res.Header, body: body}, nil
}

// comparedHeaders are the headers this package decides. The others on a
// Node answer come from the rest of the server (helmet, CORS, rate limits,
// cookies) or from the HTTP layer (Date, Connection).
var comparedHeaders = []string{
	"Content-Type", "Content-Length", "Content-Encoding", "Content-Range", "Cache-Control", "ETag", "Last-Modified",
	"Accept-Ranges", "Location", "Vary", "Allow",
}

func compareAnswers(got, want diffAnswer) []string {
	var problems []string
	if got.status != want.status {
		problems = append(problems, fmt.Sprintf("status %d, Node %d", got.status, want.status))
	}
	names := append([]string{}, comparedHeaders...)
	// A page carries the policy of the build; every other answer of the
	// Node server carries helmet's, which is not this package's
	if strings.HasPrefix(want.header.Get("Content-Type"), "text/html") && bytes.Contains(want.body, []byte("<html")) {
		names = append(names, "Content-Security-Policy")
	}
	sort.Strings(names)
	for _, name := range names {
		have, expect := strings.Join(got.header.Values(name), ", "), strings.Join(want.header.Values(name), ", ")
		if have != expect {
			problems = append(problems, fmt.Sprintf("%s: %q, Node %q", name, have, expect))
		}
	}
	if !bytes.Equal(got.body, want.body) {
		problems = append(problems, fmt.Sprintf("body (%d bytes) %q, Node (%d bytes) %q", len(got.body), clip(string(got.body)), len(want.body), clip(string(want.body))))
	}
	return problems
}

// diffRequests is the list of requests: pages signed out, files of the
// build with every encoding, validators and ranges, odd paths, the old
// /app addresses, unknown paths with different Accept headers, HEAD.
func diffRequests(t *testing.T, dir string) []diffRequest {
	t.Helper()
	var list []diffRequest
	add := func(method, target string, headers ...string) {
		request := diffRequest{method: method, target: target}
		for i := 0; i+1 < len(headers); i += 2 {
			request.headers = append(request.headers, [2]string{headers[i], headers[i+1]})
		}
		list = append(list, request)
	}

	// One hashed file with and without precompressed copies, a font and a
	// text file, picked from the build
	hashedJS, hashedCSS := findFile(t, dir, "_ui/immutable", ".js"), findFile(t, dir, "_ui/immutable", ".css")
	font, text := findFile(t, dir, "fonts", ".woff2"), findFile(t, dir, "fonts", ".txt")

	pages := []string{"/", "/login", "/login?desktop=1", "/login/", "/portal", "/remote/login", "/remote/guest?t=x", "/remote/900100012",
		"/remote/900100012?guest=tok", "/remote", "/remote/", "/remote/a", "/remote/x/y", "/devices", "/devices?open=123", "/devices/",
		"/settings?tab=auth", "/organizations/7", "/organizations/a%20b", "/organizations/a%2Fb", "/policies", "/policies/7", "/policies/7/x",
		"/no-such-page", "/devices/7/x", "/DEVICES", "/%64evices", "/x.html", "/index.html", "/a.b/c", "/d/abc", "/error/404",
		"/devices?x=%27%22%3C", "/organizations/%25", "/login?return=%2Fdevices", "/x..", "/cdap/devices/abc"}
	for _, p := range pages {
		add("GET", p, "Accept", browser)
		add("HEAD", p, "Accept", browser)
	}
	for _, accept := range []string{"", "text/html", "text/plain", "application/json", "*/*", "image/png", "text/html;q=0, */*", "text/plain;q=0.5, text/html;q=0.4"} {
		headers := []string{}
		if accept != "" {
			headers = []string{"Accept", accept}
		}
		add("GET", "/devices?open=1", headers...)
		add("GET", "/remote", headers...)
		add("GET", "/app/devices?open=1", headers...)
		add("GET", "/no-such-file.zzz", headers...)
		add("GET", "/webui-diff/missing.txt", headers...)
	}
	add("GET", "/devices", "Accept", browser, "Cookie", "nd.sid=s%3Agone.sig")
	add("GET", "/devices", "Accept", browser, "Cookie", "theme=dark")
	add("GET", "/remote", "Accept", browser, "Cookie", "nd.sid=s%3Agone.sig")

	for _, p := range []string{"/app", "/app/", "/app?activateAgent=1", "/app/devices?open=900100001", "/app/_ui/immutable/start.js", "/app//evil.example/x",
		`/app/\evil.example`, "/APP/devices", "/app/x?return=//evil.example", "/app/..//evil.example", "/app/%2F%2Fevil.example", "/app/a%20b"} {
		add("GET", p, "Accept", browser)
		add("HEAD", p)
	}

	for _, encoding := range []string{"", "br", "gzip", "br, gzip", "gzip, deflate, br, zstd", "identity", "br;q=0", "BR", "a-br", "deflate", "x-gzip"} {
		headers := []string{}
		if encoding != "" {
			headers = []string{"Accept-Encoding", encoding}
		}
		add("GET", hashedJS, headers...)
		add("GET", hashedCSS, headers...)
	}
	add("HEAD", hashedJS, "Accept-Encoding", "br")
	add("GET", hashedJS+"?v=1")
	add("GET", strings.Replace(hashedJS, "/_ui/immutable/", "/_ui/immutable/./", 1))
	add("GET", strings.Replace(hashedJS, "/_ui/immutable/", "/_UI/IMMUTABLE/", 1))
	add("GET", hashedJS+".br")
	add("GET", hashedJS+"/")
	add("GET", strings.Replace(hashedJS, ".js", "%2Ejs", 1))
	for _, p := range []string{"/_ui/immutable/nope.js", "/_ui/immutable/", "/_ui/immutable", "/_ui/immutable/chunks", "/_ui/immutable/chunks/", "/_ui/immutable/../version.json",
		"/_ui/immutable/../../licenses.txt", "/_ui/immutable/../../../etc/passwd", "/_ui/immutable/../..", "/_ui/immutable/..%2F..%2Findex.html", "/_ui/version.json",
		"/_ui/version.json?x=1", "/licenses.txt", "/routes.json", font, text, "/fonts", "/fonts/", "/img", "/_ui", "/licenses.txt/", "/LICENSES.TXT",
		"/img/../licenses.txt", "/img/%2e%2e/licenses.txt", "/..%2Flicenses.txt", "/%2e%2e/%2e%2e/etc/passwd", "//licenses.txt", "/./licenses.txt",
		"/licenses%2Etxt", "/licenses.txt%00", "/index.html.br", "/licenses.txt.gz", "/missing.txt", "/.hidden", `/img\x.svg`, "/licenses.txt;x"} {
		add("GET", p, "Accept", browser)
		add("HEAD", p)
	}

	for _, file := range []string{hashedJS, "/licenses.txt", font} {
		first, err := exchange(mustHost(t), diffRequest{method: "GET", target: file})
		if err != nil {
			t.Fatal(err)
		}
		tag, modified := first.header.Get("ETag"), first.header.Get("Last-Modified")
		if tag == "" || modified == "" {
			t.Fatalf("%s: Node sent no validators (%d)", file, first.status)
		}
		for _, headers := range [][]string{
			{"If-None-Match", tag}, {"If-None-Match", "*"}, {"If-None-Match", `"nope"`}, {"If-None-Match", `"a", ` + tag},
			{"If-None-Match", strings.TrimPrefix(tag, "W/")}, {"If-None-Match", tag, "Cache-Control", "no-cache"},
			{"If-Modified-Since", modified}, {"If-Modified-Since", "Sat, 01 Jan 2000 00:00:00 GMT"}, {"If-Modified-Since", "Wed, 01 Jan 2031 00:00:00 GMT"},
			{"If-Modified-Since", "garbage"}, {"If-None-Match", tag, "If-Modified-Since", "Sat, 01 Jan 2000 00:00:00 GMT"},
			{"If-Match", tag}, {"If-Match", "*"}, {"If-Match", `"nope"`, "Accept", "application/json"}, {"If-Match", `"nope"`, "Accept", browser},
			{"If-Unmodified-Since", modified}, {"If-Unmodified-Since", "Sat, 01 Jan 2000 00:00:00 GMT", "Accept", "application/json"},
			{"Range", "bytes=0-9"}, {"Range", "bytes=10-"}, {"Range", "bytes=-5"}, {"Range", "bytes=0-0,5-6"}, {"Range", "bytes=5-9,0-6"},
			{"Range", "bytes=5-2", "Accept", "application/json"}, {"Range", "bytes=999999999-", "Accept", browser}, {"Range", "items=0-5"},
			{"Range", "bytes=0-9", "If-Range", tag}, {"Range", "bytes=0-9", "If-Range", `"nope"`}, {"Range", "bytes=0-9", "If-Range", modified},
			{"Range", "bytes=0-9", "If-Range", "Sat, 01 Jan 2000 00:00:00 GMT"},
		} {
			add("GET", file, headers...)
		}
		add("HEAD", file, "Range", "bytes=0-9")
		add("HEAD", file, "If-None-Match", tag)
	}
	page, err := exchange(mustHost(t), diffRequest{method: "GET", target: "/login"})
	if err != nil {
		t.Fatal(err)
	}
	add("GET", "/login", "If-None-Match", page.header.Get("ETag"))
	add("HEAD", "/login", "If-None-Match", page.header.Get("ETag"))
	add("GET", "/login", "If-None-Match", `"other"`)
	add("GET", "/no-such-page", "If-None-Match", "*")

	// What nothing answers
	for _, p := range []string{"/webui-diff/x", "/webui-diff/x.txt", "/webui-diff/x.js"} {
		for _, accept := range []string{"", browser, "application/json", "text/plain"} {
			headers := []string{}
			if accept != "" {
				headers = []string{"Accept", accept}
			}
			add("GET", p, headers...)
			add("HEAD", p, headers...)
		}
	}
	return list
}

func mustHost(t *testing.T) string {
	t.Helper()
	node, err := url.Parse(os.Getenv("ND_URL"))
	if err != nil {
		t.Fatal(err)
	}
	return node.Host
}

// findFile picks the first file with the given extension under a
// directory of the build that has no precompressed sibling named like
// another extension, as a URL path.
func findFile(t *testing.T, dir, under, ext string) string {
	t.Helper()
	var found string
	var walk func(rel string)
	walk = func(rel string) {
		entries, err := os.ReadDir(dir + "/" + rel)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if found != "" {
				return
			}
			if entry.IsDir() {
				walk(rel + "/" + entry.Name())
			} else if strings.HasSuffix(entry.Name(), ext) {
				found = "/" + rel + "/" + entry.Name()
			}
		}
	}
	walk(under)
	if found == "" {
		t.Fatalf("no %s file under %s/%s", ext, dir, under)
	}
	return found
}
