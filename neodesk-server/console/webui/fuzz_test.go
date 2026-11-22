package webui

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fuzzSecret = "OUTSIDE-THE-BUILD-SECRET"

// FuzzHandlePath sends arbitrary request targets to Handle and checks that
// it never touches or sends a file outside the build directory and never
// panics, whatever the method, the session and the headers that pick files.
// Files that must stay private lie next to the build, also in a directory
// whose name starts like the build's.
func FuzzHandlePath(f *testing.F) {
	for _, seed := range []string{
		"/", "/devices?open=1", "/login?return=/devices", "/_ui/immutable/entry/start.t3st.js", "/_ui/immutable/../../../secret.txt",
		"/_ui/immutable/..%2F..%2Fsecret.txt", "/..%2Fsecret.txt", "/%2e%2e/secret.txt", `/..\secret.txt`, "/_ui/immutable/../../001-other/secret.txt",
		"/_ui/immutable/../..", "/_ui/immutable/../../", "/licenses.txt", "/img/a%20b.txt", "/app/devices", "/app2/_ui/immutable/../../../secret.js",
		"/app2/licenses.txt", "/app2//..//secret.txt", "/%E0%A4%A", "/%00", "/licenses.txt%00/../../secret.txt", "//secret.txt", "/.hidden.txt",
		"/_ui/immutable/\x00", "http://x/../secret.txt", "*", "", "/_ui/immutable/C:/x", "/CON", "/_ui/immutable/nul", "/a/../../001/licenses.txt",
		"/_ui/immutable/../../../001/licenses.txt", "/_ui/immutable/../../../001-other/secret.txt", "/#/../secret.txt", "/?/../secret.txt",
		`/_ui/immutable/..\..\..\secret.txt`, "/_ui/immutable/entry/", "/_ui/immutable//", "/\u00e9", "/%2e%2e%2f%2e%2e%2fsecret.txt", "/..;/secret.txt",
		"/app//evil.example", `/app/\evil.example`, "/login?return=//evil.example", "/remote?x=\r\n",
	} {
		f.Add(seed, uint8(0))
		f.Add(seed, uint8(0x35))
	}

	dir := copySite(f, "")
	outside := filepath.Dir(dir)
	for _, name := range []string{"secret.txt", "secret.js", "index.html", "version.json"} {
		writeFile(f, filepath.Join(outside, name), fuzzSecret)
	}
	if err := os.MkdirAll(dir+"-other", 0o755); err != nil {
		f.Fatal(err)
	}
	writeFile(f, filepath.Join(dir+"-other", "secret.txt"), fuzzSecret)

	watched := &watchedFiles{}
	var hosts []*Host
	for _, base := range []string{"", "/app2"} {
		for _, production := range []bool{true, false} {
			hosts = append(hosts, newHost(Options{Dir: dir, Base: &base, Production: production, Session: sessionFromHeader}, dir, watched))
		}
	}
	watched.take()
	methods := []string{"GET", "HEAD", "POST", "DELETE"}
	inside := dir + string(filepath.Separator)

	f.Fuzz(func(t *testing.T, target string, choice uint8) {
		h := hosts[int(choice)%len(hosts)]
		method := methods[int(choice>>2)%len(methods)]
		r := rawRequest(method, target, nil)
		// The target as the bytes the fuzzer gave
		r.RequestURI = target
		if choice&0x10 != 0 {
			r.Header.Set("Accept-Encoding", "br, gzip")
		}
		if choice&0x20 != 0 {
			r.Header.Set("X-Test-User", "admin")
		}
		if choice&0x40 != 0 {
			r.Header.Set("Range", "bytes=0-3")
		}
		rec := httptest.NewRecorder()
		handled := h.Handle(rec, r)
		if !handled {
			if rec.Body.Len() != 0 {
				t.Fatalf("%s %q: declined, but wrote %q", method, target, rec.Body.String())
			}
			h.NotFound(rec, r)
		}
		for _, access := range watched.take() {
			if access.name != dir && !strings.HasPrefix(access.name, inside) {
				t.Fatalf("%s %q: %s of %s, outside the build in %s", method, target, access.op, access.name, dir)
			}
			if filepath.Clean(access.name) != access.name || strings.Contains(access.name, "\x00") {
				t.Fatalf("%s %q: %s of the odd name %q", method, target, access.op, access.name)
			}
		}
		if strings.Contains(rec.Body.String(), fuzzSecret) {
			t.Fatalf("%s %q: the answer holds a file from outside the build", method, target)
		}
		if rec.Code < 200 || rec.Code > 599 {
			t.Fatalf("%s %q: status %d", method, target, rec.Code)
		}
		if location := rec.Header().Get("Location"); location != "" {
			if !strings.HasPrefix(location, "/") || strings.HasPrefix(location, "//") || strings.HasPrefix(location, `/\`) || strings.ContainsAny(location, "\r\n\x00 ") {
				t.Fatalf("%s %q: redirect to %q", method, target, location)
			}
		}
		if rec.Code == http.StatusOK && handled && (method == "POST" || method == "DELETE") {
			t.Fatalf("%s %q was answered with a file or a page", method, target)
		}
	})
}
