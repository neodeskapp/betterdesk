package webui

import (
	"compress/gzip"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const startJS = "/_ui/immutable/entry/start.t3st.js"

func gunzip(t *testing.T, body string) string {
	t.Helper()
	unpacked, err := gzip.NewReader(strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(unpacked)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestHashedFilesGoOutPrecompressedAndCachedForAYear(t *testing.T) {
	bases(t, func(t *testing.T, h *Host, base, dir string) {
		plain := readSite(t, dir, "_ui/immutable/entry/start.t3st.js")
		cases := []struct {
			accept   string
			encoding string
			file     string
		}{
			{"br, gzip", "br", ".br"},
			{"gzip, deflate, br, zstd", "br", ".br"},
			{"gzip", "gzip", ".gz"},
			{"br", "br", ".br"},
			{"", "", ""},
			{"identity", "", ""},
			{"deflate", "", ""},
			// The header is searched for the words, not parsed
			{"br;q=0", "br", ".br"},
			{"a-br", "br", ".br"},
			{"BR", "", ""},
			{"abr, gzip2", "", ""},
			{"x-gzip", "gzip", ".gz"},
		}
		for _, c := range cases {
			a := serve(h, "GET", base+startJS, map[string]string{"Accept-Encoding": c.accept})
			wantStatus(t, a, "Accept-Encoding "+c.accept, 200)
			if a.get("Content-Encoding") != c.encoding {
				t.Errorf("Accept-Encoding %q: Content-Encoding %q, want %q", c.accept, a.get("Content-Encoding"), c.encoding)
			}
			if want := readSite(t, dir, "_ui/immutable/entry/start.t3st.js"+c.file); a.body != want {
				t.Errorf("Accept-Encoding %q: the body is not the file start.t3st.js%s", c.accept, c.file)
			}
			if a.get("Cache-Control") != "public, max-age=31536000, immutable" || a.get("Vary") != "Accept-Encoding" ||
				a.get("Content-Type") != "application/javascript; charset=utf-8" || a.get("Accept-Ranges") != "bytes" {
				t.Errorf("Accept-Encoding %q: headers %v", c.accept, a.header)
			}
			if c.encoding == "gzip" && gunzip(t, a.body) != plain {
				t.Error("the gzip copy does not unpack to the file")
			}
		}
		// Only the copies the build has: no brotli copy of the stylesheet, no gzip copy of the picture
		a := serve(h, "GET", base+"/_ui/immutable/assets/app.t3st.css", map[string]string{"Accept-Encoding": "br, gzip"})
		if a.get("Content-Encoding") != "gzip" || a.get("Content-Type") != "text/css; charset=utf-8" {
			t.Errorf("stylesheet: encoding %q, type %q", a.get("Content-Encoding"), a.get("Content-Type"))
		}
		a = serve(h, "GET", base+"/_ui/immutable/assets/pic.t3st.svg", map[string]string{"Accept-Encoding": "gzip"})
		if a.get("Content-Encoding") != "" || a.get("Content-Type") != "image/svg+xml" || a.body != readSite(t, dir, "_ui/immutable/assets/pic.t3st.svg") {
			t.Errorf("picture: encoding %q, type %q", a.get("Content-Encoding"), a.get("Content-Type"))
		}
		// A type the table does not know, and an extension in capitals
		a = serve(h, "GET", base+"/_ui/immutable/chunks/data.t3st.bin", nil)
		if a.status != 200 || a.get("Content-Type") != "application/octet-stream" || a.body != "\x00\x01\x02\x03\xfe\xff" {
			t.Errorf("binary file: %d %q %q", a.status, a.get("Content-Type"), a.body)
		}
		if a := serve(h, "GET", base+"/_ui/immutable/chunks/UPPER.JS", nil); a.get("Content-Type") != "application/octet-stream" {
			t.Errorf("UPPER.JS: type %q", a.get("Content-Type"))
		}

		head := serve(h, "HEAD", base+startJS, map[string]string{"Accept-Encoding": "br"})
		if head.status != 200 || head.body != "" || head.get("Content-Encoding") != "br" || head.get("Content-Length") == "" {
			t.Errorf("HEAD: %d, headers %v", head.status, head.header)
		}
	})
}

func TestHashedFileValidators(t *testing.T) {
	bases(t, func(t *testing.T, h *Host, base, _ string) {
		a := serve(h, "GET", base+startJS, nil)
		if a.get("ETag") != siteETagStart || a.get("Last-Modified") != siteModified {
			t.Fatalf("ETag %q, Last-Modified %q; want %q, %q", a.get("ETag"), a.get("Last-Modified"), siteETagStart, siteModified)
		}
		for name, headers := range map[string]map[string]string{
			"matching tag":       {"If-None-Match": siteETagStart},
			"strong form":        {"If-None-Match": strings.TrimPrefix(siteETagStart, "W/")},
			"any tag":            {"If-None-Match": "*"},
			"one of several":     {"If-None-Match": `"a", ` + siteETagStart},
			"not modified since": {"If-Modified-Since": siteModified},
			"a later date":       {"If-Modified-Since": "Wed, 01 Jan 2031 00:00:00 GMT"},
		} {
			a := serve(h, "GET", base+startJS, headers)
			if a.status != 304 || a.body != "" || a.get("Content-Type") != "" || a.get("Content-Length") != "" ||
				a.get("ETag") != siteETagStart || a.get("Cache-Control") != "public, max-age=31536000, immutable" {
				t.Errorf("%s: %d, headers %v", name, a.status, a.header)
			}
		}
		for name, headers := range map[string]map[string]string{
			"another tag":       {"If-None-Match": `"nope"`},
			"an earlier date":   {"If-Modified-Since": "Fri, 02 Jan 2026 03:04:04 GMT"},
			"not a date":        {"If-Modified-Since": "garbage"},
			"a forced reload":   {"If-None-Match": siteETagStart, "Cache-Control": "no-cache"},
			"tag but old date":  {"If-None-Match": siteETagStart, "If-Modified-Since": "Sat, 01 Jan 2000 00:00:00 GMT"},
			"If-Match holds":    {"If-Match": siteETagStart},
			"If-Match any":      {"If-Match": "*"},
			"unmodified since":  {"If-Unmodified-Since": siteModified},
			"not a date either": {"If-Unmodified-Since": "garbage"},
		} {
			wantStatus(t, serve(h, "GET", base+startJS, headers), name, 200)
		}
		// The compressed copy has its own tag
		br := serve(h, "GET", base+startJS, map[string]string{"Accept-Encoding": "br"})
		if br.get("ETag") == siteETagStart || br.get("ETag") == "" {
			t.Errorf("the brotli copy has the tag %q", br.get("ETag"))
		}
		a = serve(h, "GET", base+startJS, map[string]string{"Accept-Encoding": "br", "If-None-Match": br.get("ETag")})
		if a.status != 304 || a.get("Content-Encoding") != "" {
			t.Errorf("the brotli copy with its tag: %d, Content-Encoding %q", a.status, a.get("Content-Encoding"))
		}
	})
}

// A failed precondition and a range outside the file are failures of the
// request: the answer of ServerError with that status, and the file's
// validators still on it.
func TestFailedPreconditionAndRange(t *testing.T) {
	bases(t, func(t *testing.T, h *Host, base, _ string) {
		for _, target := range []string{base + startJS, base + "/licenses.txt"} {
			for name, headers := range map[string]map[string]string{
				"If-Match with another tag":          {"If-Match": `"nope"`},
				"If-Unmodified-Since an earlier day": {"If-Unmodified-Since": "Sat, 01 Jan 2000 00:00:00 GMT"},
			} {
				a := serve(h, "GET", target, with(headers, "Accept", "application/json"))
				if a.status != 412 || a.body != `{"success":false,"error":"Internal Server Error"}` || a.get("ETag") != "" && !strings.HasPrefix(a.get("ETag"), `W/"`) {
					t.Errorf("%s %s: %d %q", target, name, a.status, a.body)
				}
			}
			a := serve(h, "GET", target, map[string]string{"Range": "bytes=100000-", "Accept": browser})
			if a.status != 416 || !strings.Contains(a.body, `<meta name="bd-error" content="500">`) || !strings.HasPrefix(a.get("Content-Range"), "bytes */") ||
				a.get("Cache-Control") != "no-store" || a.get("Accept-Ranges") != "bytes" || a.get("Last-Modified") != siteModified {
				t.Errorf("%s with a range outside the file: %d, headers %v", target, a.status, a.header)
			}
		}
	})
	dev := New(Options{Dir: copySite(t, "")})
	a := serve(dev, "GET", "/licenses.txt", map[string]string{"Range": "bytes=100000-", "Accept": "application/json"})
	if a.status != 416 || a.body != `{"success":false,"error":"Range Not Satisfiable"}` || a.get("Content-Type") != "text/plain; charset=utf-8" {
		t.Errorf("outside production: %d %q (%s)", a.status, a.body, a.get("Content-Type"))
	}
	a = serve(dev, "GET", "/licenses.txt", map[string]string{"If-Match": `"nope"`, "Accept": "application/json"})
	if a.status != 412 || a.body != `{"success":false,"error":"Precondition Failed"}` {
		t.Errorf("outside production: %d %q", a.status, a.body)
	}
}

func TestByteRanges(t *testing.T) {
	bases(t, func(t *testing.T, h *Host, base, dir string) {
		file := readSite(t, dir, "licenses.txt")
		size := len(file)
		cases := []struct {
			name, header string
			status       int
			body, span   string
		}{
			{"the first bytes", "bytes=0-9", 206, file[:10], "bytes 0-9/"},
			{"from a byte on", "bytes=10-", 206, file[10:], "bytes 10-" + itoa(size-1) + "/"},
			{"the last bytes", "bytes=-5", 206, file[size-5:], "bytes " + itoa(size-5) + "-" + itoa(size-1) + "/"},
			{"past the end is cut", "bytes=0-100000", 206, file, "bytes 0-" + itoa(size-1) + "/"},
			{"overlapping ranges are one", "bytes=5-9,0-6", 206, file[:10], "bytes 0-9/"},
			{"adjacent ranges are one", "bytes=0-4,5-9", 206, file[:10], "bytes 0-9/"},
			{"two ranges: the whole file", "bytes=0-0,5-6", 200, file, ""},
			{"another unit: the whole file", "items=0-5", 200, file, ""},
			{"capitals: the whole file", "BYTES=0-1", 200, file, ""},
			{"spaces around numbers", "bytes= 2 - 4 ", 206, file[2:5], "bytes 2-4/"},
		}
		for _, c := range cases {
			a := serve(h, "GET", base+"/licenses.txt", map[string]string{"Range": c.header})
			if a.status != c.status || a.body != c.body || a.get("Content-Length") != itoa(len(c.body)) {
				t.Errorf("%s (%s): %d with %d bytes, want %d with %d", c.name, c.header, a.status, len(a.body), c.status, len(c.body))
			}
			want := ""
			if c.span != "" {
				want = c.span + itoa(size)
			}
			if a.get("Content-Range") != want {
				t.Errorf("%s: Content-Range %q, want %q", c.name, a.get("Content-Range"), want)
			}
		}
		for name, header := range map[string]string{"start after end": "bytes=5-2", "after the file": "bytes=100000-", "more than the file from the end": "bytes=-100000", "nothing": "bytes=", "no bytes": "bytes=-0"} {
			a := serve(h, "GET", base+"/licenses.txt", map[string]string{"Range": header})
			if a.status != 416 || a.get("Content-Range") != "bytes */"+itoa(size) {
				t.Errorf("%s (%s): %d, Content-Range %q", name, header, a.status, a.get("Content-Range"))
			}
		}
		// If-Range: the range counts only while the copy is current
		tag := serve(h, "GET", base+"/licenses.txt", nil).get("ETag")
		for name, c := range map[string]struct {
			ifRange string
			status  int
		}{
			"the current tag":  {tag, 206},
			"another tag":      {`"nope"`, 200},
			"the current date": {siteModified, 206},
			"an earlier date":  {"Sat, 01 Jan 2000 00:00:00 GMT", 200},
			"not a date":       {"garbage", 200},
		} {
			a := serve(h, "GET", base+"/licenses.txt", map[string]string{"Range": "bytes=0-9", "If-Range": c.ifRange})
			wantStatus(t, a, "If-Range "+name, c.status)
		}
		wantStatus(t, serve(h, "GET", base+"/licenses.txt", map[string]string{"Range": "bytes=100000-", "If-Range": `"nope"`}), "a stale range outside the file", 200)

		head := serve(h, "HEAD", base+"/licenses.txt", map[string]string{"Range": "bytes=0-9"})
		if head.status != 206 || head.body != "" || head.get("Content-Length") != "10" {
			t.Errorf("HEAD with a range: %d, length %q", head.status, head.get("Content-Length"))
		}
		a := serve(h, "GET", base+startJS, map[string]string{"Range": "bytes=0-9", "Accept-Encoding": "br"})
		if a.status != 206 || a.body != readSite(t, dir, "_ui/immutable/entry/start.t3st.js.br")[:10] || a.get("Content-Encoding") != "br" {
			t.Errorf("a range of the brotli copy: %d, %d bytes", a.status, len(a.body))
		}
		empty := serve(h, "GET", base+"/empty.txt", map[string]string{"Range": "bytes=0-"})
		if empty.status != 416 || empty.get("Content-Range") != "bytes */0" {
			t.Errorf("a range of an empty file: %d %q", empty.status, empty.get("Content-Range"))
		}
	})
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestOtherBuildFiles(t *testing.T) {
	bases(t, func(t *testing.T, h *Host, base, dir string) {
		cases := []struct{ target, file, contentType, cache string }{
			{"/licenses.txt", "licenses.txt", "text/plain; charset=UTF-8", "public, max-age=3600"},
			{"/_ui/version.json", "_ui/version.json", "application/json; charset=UTF-8", "no-cache"},
			{"/fonts/test.woff2", "fonts/test.woff2", "font/woff2", "public, max-age=3600"},
			{"/img/logo.svg", "img/logo.svg", "image/svg+xml", "public, max-age=3600"},
			{"/img/a%20b.txt", "img/a b.txt", "text/plain; charset=UTF-8", "public, max-age=3600"},
			{"/LICENSE", "LICENSE", "application/octet-stream", "public, max-age=3600"},
			{"/routes.json", "routes.json", "application/json; charset=UTF-8", "public, max-age=3600"},
			{"/empty.txt", "empty.txt", "text/plain; charset=UTF-8", "public, max-age=3600"},
			// The path is decoded and cleaned before the file is looked up
			{"/img%2Flogo.svg", "img/logo.svg", "image/svg+xml", "public, max-age=3600"},
			{"/licenses%2Etxt", "licenses.txt", "text/plain; charset=UTF-8", "public, max-age=3600"},
			{"/img/../licenses.txt", "licenses.txt", "text/plain; charset=UTF-8", "public, max-age=3600"},
			{"/img/%2e%2e/licenses.txt", "licenses.txt", "text/plain; charset=UTF-8", "public, max-age=3600"},
			{"//licenses.txt", "licenses.txt", "text/plain; charset=UTF-8", "public, max-age=3600"},
			{"/./img//logo.svg", "img/logo.svg", "image/svg+xml", "public, max-age=3600"},
			{"/licenses.txt?v=1", "licenses.txt", "text/plain; charset=UTF-8", "public, max-age=3600"},
			// A dot directory is fine, a dot file is not (below)
			{"/.well-known/note.txt", ".well-known/note.txt", "text/plain; charset=UTF-8", "public, max-age=3600"},
		}
		for _, c := range cases {
			a := serve(h, "GET", base+c.target, nil)
			if a.status != 200 || a.body != readSite(t, dir, c.file) {
				t.Errorf("%s: %d with %d bytes, want the file %s", c.target, a.status, len(a.body), c.file)
				continue
			}
			if a.get("Content-Type") != c.contentType || a.get("Cache-Control") != c.cache || a.get("Accept-Ranges") != "bytes" ||
				a.get("Last-Modified") != siteModified || !strings.HasPrefix(a.get("ETag"), `W/"`) || a.get("Content-Length") != itoa(len(a.body)) {
				t.Errorf("%s: headers %v", c.target, a.header)
			}
			if a.get("Vary") != "" || a.get("Content-Encoding") != "" || a.get("Content-Security-Policy") != "" {
				t.Errorf("%s: headers of a page or a hashed file on a plain file: %v", c.target, a.header)
			}
			head := serve(h, "HEAD", base+c.target, nil)
			if head.status != 200 || head.body != "" || head.get("Content-Length") != a.get("Content-Length") || head.get("ETag") != a.get("ETag") {
				t.Errorf("HEAD %s: %d, headers %v", c.target, head.status, head.header)
			}
		}
		tag := serve(h, "GET", base+"/licenses.txt", nil).get("ETag")
		wantStatus(t, serve(h, "GET", base+"/licenses.txt", map[string]string{"If-None-Match": tag}), "a plain file with its tag", 304)
	})
}

// The modification time counts in whole milliseconds, rounded: the tag
// holds it, and Last-Modified is cut to the second after the rounding.
func TestFileTimeIsRoundedToMilliseconds(t *testing.T) {
	h, dir := testHost(t, "")
	at := siteTime.Add(321_600_000) // 03:04:05.9996 is 03:04:06.000 in whole milliseconds
	if err := os.Chtimes(filepath.Join(dir, "licenses.txt"), at, at); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "licenses.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if info.ModTime().Nanosecond() != at.Nanosecond() {
		t.Skip("this file system does not keep nanoseconds")
	}
	a := serve(h, "GET", "/licenses.txt", nil)
	if a.get("Last-Modified") != "Fri, 02 Jan 2026 03:04:06 GMT" || a.get("ETag") != `W/"118-19b7ca99070"` {
		t.Errorf("Last-Modified %q, ETag %q", a.get("Last-Modified"), a.get("ETag"))
	}
}

// A header that is set before Handle runs stays, as it did behind the old
// console's security middleware.
func TestFileKeepsHeadersSetBefore(t *testing.T) {
	h, _ := testHost(t, "")
	send := func(target string) answer {
		r := rawRequest("GET", target, nil)
		return record(func(w http.ResponseWriter) {
			w.Header().Set("Cache-Control", "no-store")
			h.Handle(w, r)
		})
	}
	if a := send("/licenses.txt"); a.status != 200 || a.get("Cache-Control") != "no-store" {
		t.Errorf("a plain file: %d, Cache-Control %q", a.status, a.get("Cache-Control"))
	}
	if a := send("/_ui/version.json"); a.get("Cache-Control") != "no-cache" {
		t.Errorf("version.json: Cache-Control %q", a.get("Cache-Control"))
	}
	if a := send(startJS); a.get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Errorf("a hashed file: Cache-Control %q", a.get("Cache-Control"))
	}
}

func TestHTMLFilesAreNeverSentAsFiles(t *testing.T) {
	bases(t, func(t *testing.T, h *Host, base, _ string) {
		for _, p := range []string{"/index.html", "/other.html", "/OTHER.HTML", "/other%2Ehtml", "/page.htm", "/PAGE.HTM", "/img/../other.html", "/other.htm%6C"} {
			for _, user := range []map[string]string{nil, admin} {
				a := serve(h, "GET", base+p, user)
				if strings.Contains(a.body, "another html file") || strings.Contains(a.body, "<p>htm</p>") || a.status == 200 {
					t.Errorf("%s: %d %q", p, a.status, clip(a.body))
				}
			}
		}
	})
}

func TestNoFilesOutsideTheBuild(t *testing.T) {
	bases(t, func(t *testing.T, h *Host, base, dir string) {
		secret := filepath.Join(filepath.Dir(dir), "webui-secret.txt")
		if err := os.WriteFile(secret, []byte("secret"), 0o644); err != nil {
			t.Fatal(err)
		}
		defer os.Remove(secret)
		up := filepath.Base(dir)
		for _, p := range []string{
			"/_ui/immutable/..%2F..%2F..%2Fwebui-secret.txt", "/..%2Fwebui-secret.txt", "/_ui/../../webui-secret.txt", "/../webui-secret.txt",
			"/_ui/immutable/../../../webui-secret.txt", "/%2e%2e/webui-secret.txt", "/%2e%2e%2fwebui-secret.txt", `/..\webui-secret.txt`,
			`/_ui/immutable/..\..\..\webui-secret.txt`, "/..%5Cwebui-secret.txt", "/img/..%2F..%2Fwebui-secret.txt", "/_ui/immutable/../../../" + up + "/../webui-secret.txt",
			"/_ui/immutable/%2e%2e/%2e%2e/%2e%2e/webui-secret.txt", "/....//webui-secret.txt", "/webui-secret.txt", "/img/..%00/../webui-secret.txt",
		} {
			for _, method := range []string{"GET", "HEAD"} {
				a := serve(h, method, base+p, with(admin, "Accept-Encoding", "br, gzip"))
				if strings.Contains(a.body, "secret") || a.status == 200 || a.status == 206 {
					t.Errorf("%s %s: %d %q", method, p, a.status, clip(a.body))
				}
			}
		}
		// The guard of the hashed files answers an empty 404 itself
		a := serve(h, "GET", base+"/_ui/immutable/../../../webui-secret.txt", admin)
		if a.status != 404 || a.body != "" || !a.handled || a.get("Content-Length") != "0" {
			t.Errorf("a hashed-file path that leaves the build: %d %q (handled %v)", a.status, a.body, a.handled)
		}
		// The page itself is not a static file: an encoded way to it is an
		// address like any other (no page has it), and without a list of
		// pages it is a console page, which signed out redirects
		a = serve(h, "GET", base+"/_ui/immutable/..%2F..%2Findex.html", nil)
		if a.status != 404 || a.get("Cache-Control") != "no-store" || a.get("Content-Security-Policy") != sitePolicy {
			t.Errorf("an encoded way to index.html: %d, headers %v", a.status, a.header)
		}
		if err := os.Remove(filepath.Join(dir, "routes.json")); err != nil {
			t.Fatal(err)
		}
		unlisted := New(Options{Dir: dir, Production: true, Session: sessionFromHeader})
		wantStatus(t, serve(unlisted, "GET", base+"/_ui/immutable/..%2F..%2Findex.html", nil), "the same without a list of pages", 302)
		a = serve(h, "GET", base+"/_ui/immutable/nope.js", admin)
		wantDeclined(t, a, "a missing hashed file")
	})
}

func TestDotFilesDirectoriesAndOddNames(t *testing.T) {
	bases(t, func(t *testing.T, h *Host, base, _ string) {
		for _, p := range []string{"/.hidden.txt", "/fonts", "/fonts/", "/.well-known", "/_ui", "/licenses.txt/", "/licenses.txt%00", "/LICENSES.TXT",
			"/img/a+b.txt", `/img\logo.svg`, "/licenses.txt%2F", "/_ui/version.json/", "/licenses.txt;x"} {
			a := serve(h, "GET", base+p, map[string]string{"Accept": "application/json"})
			if a.status == 200 || strings.Contains(a.body, "licenses file") || strings.Contains(a.body, "dotfile") {
				t.Errorf("%s: %d %q", p, a.status, clip(a.body))
			}
		}
		// A hashed file whose name starts with a dot is refused, not passed on
		a := serve(h, "GET", base+"/_ui/immutable/chunks/.hidden.js", map[string]string{"Accept": "application/json"})
		if a.status != 404 || !a.handled || a.body != `{"success":false,"error":"Internal Server Error"}` {
			t.Errorf("a hashed dot file: %d %q (handled %v)", a.status, a.body, a.handled)
		}
		// A directory of hashed files: passed on without the slash, a failed request with it
		a = serve(h, "GET", base+"/_ui/immutable/entry", admin)
		if a.status != 404 || !isAppPage(a) || a.get("Vary") != "Accept-Encoding" {
			t.Errorf("a directory of hashed files: %d, Vary %q", a.status, a.get("Vary"))
		}
		a = serve(h, "GET", base+"/_ui/immutable/entry/", map[string]string{"Accept": "application/json"})
		if a.status != 404 || !a.handled || a.body != `{"success":false,"error":"Internal Server Error"}` {
			t.Errorf("a directory of hashed files with a slash: %d %q (handled %v)", a.status, a.body, a.handled)
		}
		// The routes match in any letter case, the files on disk do not (on Linux)
		a = serve(h, "GET", base+"/_UI/IMMUTABLE/nope.js", admin)
		wantDeclined(t, a, "a missing hashed file in capitals")
	})
}

// Paths under the hashed files are not decoded, and dot segments in them
// are resolved. What then lies outside the hashed files is not sent with
// their year-long caching (W3): index.html never comes past the page
// handler's session check, and version.json is not cached for a year.
func TestHashedFilePathsAreTakenAsWritten(t *testing.T) {
	bases(t, func(t *testing.T, h *Host, base, dir string) {
		for _, target := range []string{"/_ui/immutable/../version.json", "/_ui/immutable/../../index.html", "/_ui/immutable/../../licenses.txt",
			"/_ui/immutable/entry/../../version.json", "/_ui/immutable/./../version.json", "/_UI/IMMUTABLE/../version.json"} {
			for _, user := range []map[string]string{nil, admin} {
				a := serve(h, "GET", base+target, with(user, "Accept-Encoding", "br, gzip"))
				if a.status != 404 || a.body != "" || !a.handled || strings.Contains(a.get("Cache-Control"), "immutable") || a.get("Vary") != "" {
					t.Errorf("%s: %d %q (handled %v), headers %v", target, a.status, clip(a.body), a.handled, a.header)
				}
			}
		}
		wantStatus(t, serve(h, "GET", base+"/_ui/immutable/entry/./start.t3st.js", nil), "a dot segment", 200)
		wantStatus(t, serve(h, "GET", base+"/_ui/immutable/entry/../entry/start.t3st.js", nil), "a way out and back in", 200)
		wantStatus(t, serve(h, "GET", base+"/_ui/immutable/entry//start.t3st.js", nil), "a double slash", 200)
		// With an escape in it the path names no hashed file. The plain
		// file route decodes it and sends the file as any build file: an
		// hour of caching, no precompressed copy
		for _, target := range []string{"/_ui/immutable/entry%2Fstart.t3st.js", "/_ui/immutable/entry/start.t3st%2Ejs"} {
			a := serve(h, "GET", base+target, map[string]string{"Accept-Encoding": "br"})
			if a.status != 200 || a.body != readSite(t, dir, "_ui/immutable/entry/start.t3st.js") || a.get("Cache-Control") != "public, max-age=3600" ||
				a.get("Content-Encoding") != "" || a.get("Content-Type") != "application/javascript; charset=UTF-8" {
				t.Errorf("%s: %d, headers %v", target, a.status, a.header)
			}
		}
		wantDeclined(t, serve(h, "GET", base+startJS+"/", nil), "a file with a slash at the end")
	})
}

// Names that could mean something else than a file of the build on some
// system are refused before the disk is asked.
func TestInBuild(t *testing.T) {
	h, dir := testHost(t, "")
	for rel, want := range map[string]string{
		"licenses.txt":         filepath.Join(dir, "licenses.txt"),
		"_ui/version.json":     filepath.Join(dir, "_ui", "version.json"),
		".well-known/note.txt": filepath.Join(dir, ".well-known", "note.txt"),
		".":                    dir,
	} {
		if got, ok := h.inBuild(rel); !ok || got != want {
			t.Errorf("inBuild(%q) = %q, %v; want %q", rel, got, ok, want)
		}
	}
	for _, rel := range []string{"", "..", "../x", "a/../../x", "/etc/passwd", `a\b`, `..\x`, "a\x00b", `img\..\..\secret.txt`} {
		if got, ok := h.inBuild(rel); ok {
			t.Errorf("inBuild(%q) = %q, want it refused", rel, got)
		}
	}
}
