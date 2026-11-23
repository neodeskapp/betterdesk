package webui

import (
	"bytes"
	"errors"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/embedfs"
)

// embedBuild turns the build in dir into an embedded one, as
// cmd/embed-webui and go:embed leave it: the files without times (embed.FS
// has none), their times in the stamp, and the placeholder.
func embedBuild(t testing.TB, dir string) *embedfs.FS {
	t.Helper()
	staged := filepath.Join(t.TempDir(), "dist")
	if _, _, err := embedfs.Copy(dir, staged); err != nil {
		t.Fatal(err)
	}
	tree := fstest.MapFS{"dist/" + embedfs.Placeholder: {}}
	err := filepath.WalkDir(staged, func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(staged, name)
		tree["dist/"+filepath.ToSlash(rel)] = &fstest.MapFile{Data: raw, Mode: 0o444}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	build, err := embedfs.New(tree, "dist", time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	return build
}

// embeddedHost is a Host for the build in opts.Dir as the binary would
// carry it; nobuild gives a binary built without the build step.
func embeddedHost(t testing.TB, opts Options, nobuild bool) *Host {
	t.Helper()
	if nobuild {
		empty, err := embedfs.New(fstest.MapFS{"dist/" + embedfs.Placeholder: {}}, "dist", time.Unix(0, 0))
		if err != nil {
			t.Fatal(err)
		}
		return newEmbedded(opts, empty, nil)
	}
	return newEmbedded(opts, embedBuild(t, opts.Dir), nil)
}

// buildFiles lists the files of a build as slash-separated paths.
func buildFiles(t testing.TB, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(dir, func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(dir, name)
		files = append(files, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	return files
}

// The build in the binary answers every request for every file of the
// build exactly as the same build on disk does: status, every header and
// the body. WEB_UI_DIR names the build to compare (the real one in the
// dev image: WEB_UI_DIR=/repo/web-ui/build); without it the test build.
func TestEmbeddedAnswersMatchTheBuildOnDisk(t *testing.T) {
	dir := os.Getenv("WEB_UI_DIR")
	if dir == "" {
		dir = copySite(t, "")
	}
	files := buildFiles(t, dir)
	if len(files) < 10 {
		t.Fatalf("only %d files in %s", len(files), dir)
	}
	build := embedBuild(t, dir)
	abs, _ := filepath.Abs(dir)

	compared := 0
	for _, production := range []bool{true, false} {
		opts := Options{Production: production, Session: sessionFromHeader}
		disk := New(Options{Dir: dir, Production: production, Session: sessionFromHeader})
		inBinary := newEmbedded(opts, build, nil)
		if (disk.page == nil) != (inBinary.page == nil) || disk.Base() != inBinary.Base() {
			t.Fatalf("production %v: the hosts see different builds", production)
		}

		type request struct {
			method, target string
			headers        map[string]string
		}
		var requests []request
		add := func(method, target string, headers ...string) {
			h := map[string]string{}
			for i := 0; i+1 < len(headers); i += 2 {
				h[headers[i]] = headers[i+1]
			}
			requests = append(requests, request{method, target, h})
		}
		for _, file := range files {
			target := (&url.URL{Path: "/" + file}).EscapedPath()
			first := serve(disk, "GET", target, nil)
			add("GET", target)
			add("GET", target, "Accept-Encoding", "gzip, deflate, br, zstd")
			add("GET", target, "Accept-Encoding", "gzip")
			add("HEAD", target, "Accept-Encoding", "br")
			add("GET", target, "Accept", browser)
			add("GET", target, "If-None-Match", first.get("ETag"))
			add("GET", target, "If-Modified-Since", first.get("Last-Modified"))
			add("GET", target, "If-Modified-Since", "Sat, 01 Jan 2000 00:00:00 GMT")
			add("GET", target, "Range", "bytes=0-9")
			add("GET", target, "Range", "bytes=-3", "If-Range", first.get("ETag"))
			add("GET", target+"/", "Accept", browser)
			add("GET", strings.ToUpper(target))
		}
		for _, target := range []string{"/", "/login", "/devices", "/devices?open=1", "/no-such-page", "/remote/900100001",
			"/app/devices", "/index.html", "/_ui/immutable/", "/_ui/immutable", "/_ui/immutable/chunks/", "/_ui/immutable/nope.js",
			"/_ui/immutable/../../licenses.txt", "/_ui/immutable/../..", "/fonts", "/fonts/", "/img", "/.hidden", "/" + embedfs.StampFile,
			"/" + embedfs.Placeholder, "/_ui/immutable/../../" + embedfs.StampFile, "/missing.txt", "/api/x", "/x%ZZ"} {
			add("GET", target, "Accept", browser)
			add("GET", target, "Accept", "application/json")
			add("HEAD", target)
			add("GET", target, "Accept", browser, "X-Test-User", "admin")
		}

		for _, r := range requests {
			want := serve(disk, r.method, r.target, r.headers)
			got := serve(inBinary, r.method, r.target, r.headers)
			compared++
			// Outside production an error names the build directory; a HEAD
			// answer has the length of the GET answer that names it
			wantBody := strings.ReplaceAll(want.body, abs, embeddedDir)
			namesDir := wantBody != want.body
			if r.method == "HEAD" {
				namesDir = strings.Contains(serve(disk, "GET", r.target, r.headers).body, abs)
			}
			var problems []string
			if got.status != want.status || got.handled != want.handled {
				problems = append(problems, "status "+itoa(got.status)+", on disk "+itoa(want.status))
			}
			names := map[string]bool{}
			for name := range want.header {
				names[name] = true
			}
			for name := range got.header {
				names[name] = true
			}
			for name := range names {
				if namesDir && (name == "Content-Length" || name == "Etag") {
					continue
				}
				if g, w := got.get(name), want.get(name); g != w {
					problems = append(problems, name+": "+g+", on disk "+w)
				}
			}
			if got.body != wantBody {
				problems = append(problems, "body "+clip(got.body)+", on disk "+clip(wantBody))
			}
			if len(problems) > 0 {
				t.Errorf("production %v: %s %s %v\n    %s", production, r.method, r.target, r.headers, strings.Join(problems, "\n    "))
			}
		}
	}
	t.Logf("%d answers compared for %d files of %s", compared, len(files), dir)
}

// The build in this binary is what cmd/embed-webui copied into dist: the
// same files with the same bytes, each with its recorded time. In a tree
// where the step has not run, dist holds only the placeholder and the
// binary has no build.
func TestTheBinaryCarriesWhatTheStepCopied(t *testing.T) {
	build, err := embedded()
	if err != nil {
		t.Fatal(err)
	}
	var onDisk []string
	for _, name := range buildFiles(t, "dist") {
		if name != embedfs.Placeholder && name != embedfs.StampFile {
			onDisk = append(onDisk, name)
		}
	}
	var inBinary []string
	err = fs.WalkDir(build, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		inBinary = append(inBinary, name)
		raw, err := build.ReadFile(name)
		if err != nil {
			return err
		}
		disk, err := os.ReadFile(filepath.Join("dist", filepath.FromSlash(name)))
		if err != nil {
			return err
		}
		if !bytes.Equal(raw, disk) {
			t.Errorf("%s differs from dist/%s", name, name)
		}
		info, err := build.Stat(name)
		if err != nil {
			return err
		}
		if info.ModTime().Equal(time.Unix(0, 0)) {
			t.Errorf("%s has no recorded time", name)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(onDisk, inBinary) {
		t.Errorf("dist has %d files, the binary %d", len(onDisk), len(inBinary))
	}
	_, statErr := os.Stat(filepath.Join("dist", "index.html"))
	if HasEmbeddedBuild() != (statErr == nil) {
		t.Errorf("HasEmbeddedBuild %v, dist/index.html: %v", HasEmbeddedBuild(), statErr)
	}
	t.Logf("the binary carries %d files", len(inBinary))
}

// With Embedded the directory is not read; without Embedded and without a
// directory there is no build, whatever the binary carries.
func TestEmbeddedOptionDecidesWhereTheBuildComesFrom(t *testing.T) {
	site := copySite(t, "")
	h := New(Options{Embedded: true, Dir: site, Production: true, Session: sessionFromHeader})
	a := serve(h, "GET", "/login", map[string]string{"Accept": browser})
	if isAppPage(a) {
		t.Error("the embedded host served the page of the directory")
	}
	if (h.page != nil) != HasEmbeddedBuild() {
		t.Errorf("page %v, HasEmbeddedBuild %v", h.page != nil, HasEmbeddedBuild())
	}
	if h.dir != embeddedDir {
		t.Errorf("files are named below %q", h.dir)
	}
	none := New(Options{Production: true, Session: sessionFromHeader})
	if none.page != nil {
		t.Error("no directory and no Embedded gave a build")
	}
	if a := serve(New(Options{Dir: site, Production: true, Session: sessionFromHeader}), "GET", "/login", map[string]string{"Accept": browser}); !isAppPage(a) {
		t.Errorf("the directory's page was not served: %d", a.status)
	}
}

func TestEmbeddedBuildThatCannotBeReadIsNoBuild(t *testing.T) {
	log := captureLog(t)
	h := newEmbedded(Options{Production: true}, nil, errors.New("bad stamp"))
	if h.page != nil {
		t.Fatal("a build that cannot be read gave a page")
	}
	a := serve(h, "GET", "/login", map[string]string{"Accept": browser})
	if a.status != http.StatusNotFound || a.body != "Not Found" {
		t.Errorf("%d %q", a.status, a.body)
	}
	if !strings.Contains(log.String(), "bad stamp") {
		t.Errorf("not logged: %q", log.String())
	}
}

// A Host's names for the embedded build: the root, and paths below it.
// Anything else is no file of the build.
func TestEmbeddedFileNames(t *testing.T) {
	files := embeddedFiles{root: embeddedDir, build: embedBuild(t, copySite(t, ""))}
	sep := string(filepath.Separator)
	for name, want := range map[string]string{
		embeddedDir:                                   ".",
		embeddedDir + sep + "licenses.txt":            "licenses.txt",
		embeddedDir + sep + "_ui" + sep + "immutable": "_ui/immutable",
	} {
		got, err := files.rel("stat", name)
		if err != nil || got != want {
			t.Errorf("%q: %q %v, want %q", name, got, err, want)
		}
	}
	for _, name := range []string{"", "licenses.txt", embeddedDir + "x", embeddedDir + sep, embeddedDir + sep + "..",
		embeddedDir + sep + "a" + sep + sep + "b", filepath.Dir(embeddedDir)} {
		if _, err := files.Stat(name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("stat %q: %v", name, err)
		}
		if _, err := files.Open(name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("open %q: %v", name, err)
		}
		if _, err := files.ReadFile(name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("read %q: %v", name, err)
		}
	}
	info, err := files.Stat(embeddedDir + sep + "licenses.txt")
	if err != nil || !info.ModTime().Equal(siteTime) {
		t.Errorf("time of licenses.txt: %v %v", info, err)
	}
}
