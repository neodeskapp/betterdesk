//go:build webuidiff

package webui

import (
	"bytes"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestEmbeddedServerAgainstDisk sends the same requests to two running
// consoles, one serving the build in its binary and one with WEB_UI_DIR
// naming the same build on disk, and compares the whole answers: status,
// every header but Date, body. Every file of the build is asked for with
// and without compression, validators and a range:
//
//	BD_URL=http://bdgo-bb1:5000 BD_DISK_URL=http://bdgo-bb2:5000 WEB_UI_DIR=/repo/web-ui/build \
//	    go test -tags webuidiff ./console/webui/ -run TestEmbeddedServerAgainstDisk -v
func TestEmbeddedServerAgainstDisk(t *testing.T) {
	embeddedURL, diskURL, dir := os.Getenv("BD_URL"), os.Getenv("BD_DISK_URL"), os.Getenv("WEB_UI_DIR")
	if embeddedURL == "" || diskURL == "" || dir == "" {
		t.Skip("BD_URL, BD_DISK_URL and WEB_UI_DIR are not set")
	}
	inBinary, err := url.Parse(embeddedURL)
	if err != nil {
		t.Fatal(err)
	}
	onDisk, err := url.Parse(diskURL)
	if err != nil {
		t.Fatal(err)
	}
	files := buildFiles(t, dir)
	var requests []diffRequest
	add := func(method, target string, headers ...string) {
		r := diffRequest{method: method, target: target}
		for i := 0; i+1 < len(headers); i += 2 {
			r.headers = append(r.headers, [2]string{headers[i], headers[i+1]})
		}
		requests = append(requests, r)
	}
	for _, file := range files {
		target := (&url.URL{Path: "/" + file}).EscapedPath()
		first, err := exchange(onDisk.Host, diffRequest{method: "GET", target: target})
		if err != nil {
			t.Fatal(err)
		}
		add("GET", target)
		add("GET", target, "Accept-Encoding", "gzip, deflate, br, zstd")
		add("GET", target, "Accept-Encoding", "gzip")
		add("HEAD", target, "Accept-Encoding", "br")
		add("GET", target, "Accept", browser)
		add("GET", target, "If-None-Match", first.header.Get("ETag"))
		add("GET", target, "If-Modified-Since", first.header.Get("Last-Modified"))
		add("GET", target, "Range", "bytes=0-9")
		add("GET", target+"/", "Accept", browser)
	}
	for _, target := range []string{"/", "/login", "/devices", "/no-such-page", "/app/devices", "/index.html", "/_ui/immutable/",
		"/_ui/immutable/nope.js", "/fonts", "/fonts/", "/missing.txt", "/.embedfs-times.json", "/.gitkeep"} {
		add("GET", target, "Accept", browser)
		add("GET", target, "Accept", "application/json")
		add("HEAD", target)
	}

	differences := 0
	headerNames := map[string]bool{}
	for _, r := range requests {
		got, err := exchange(inBinary.Host, r)
		if err != nil {
			t.Fatalf("%s: %v", r.name(), err)
		}
		want, err := exchange(onDisk.Host, r)
		if err != nil {
			t.Fatalf("%s: %v", r.name(), err)
		}
		var problems []string
		if got.status != want.status {
			problems = append(problems, "status "+itoa(got.status)+", from disk "+itoa(want.status))
		}
		names := map[string]bool{}
		for name := range got.header {
			names[name] = true
		}
		for name := range want.header {
			names[name] = true
		}
		sorted := make([]string, 0, len(names))
		for name := range names {
			headerNames[name] = true
			sorted = append(sorted, name)
		}
		sort.Strings(sorted)
		for _, name := range sorted {
			if name == "Date" {
				continue
			}
			g, w := strings.Join(got.header.Values(name), ", "), strings.Join(want.header.Values(name), ", ")
			if name == "Set-Cookie" {
				// A signed-out visitor gets a new CSRF token with every answer
				g, w = cookieValue.ReplaceAllString(g, "=<value>;"), cookieValue.ReplaceAllString(w, "=<value>;")
			}
			if g != w {
				problems = append(problems, name+": "+clip(g)+", from disk "+clip(w))
			}
		}
		if !bytes.Equal(got.body, want.body) {
			problems = append(problems, "the bodies differ")
		}
		if len(problems) > 0 {
			differences++
			if differences < 30 {
				t.Errorf("%s\n    %s", r.name(), strings.Join(problems, "\n    "))
			}
		}
	}
	list := make([]string, 0, len(headerNames))
	for name := range headerNames {
		list = append(list, name)
	}
	sort.Strings(list)
	t.Logf("%d requests for %d files compared, %d differ; headers compared: %s", len(requests), len(files), differences, strings.Join(list, " "))
}

// cookieValue is the value of a Set-Cookie header, up to its attributes.
var cookieValue = regexp.MustCompile(`=[^;]*;`)
