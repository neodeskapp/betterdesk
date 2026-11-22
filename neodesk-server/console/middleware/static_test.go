package middleware

import (
	"errors"
	"io/fs"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// The public and protos folders of the console's tree answer as the same
// folders on disk: the same status, headers and body for files, folders,
// conditional and range requests, HEAD and paths that are no file. The
// installed fonts are served at /fonts from their own folder.
func TestStaticFoldersFromTheTree(t *testing.T) {
	site := t.TempDir()
	at := time.Date(2026, 11, 19, 2, 36, 24, 958814300, time.UTC)
	for name, content := range map[string]string{
		"tree/public/favicon.svg":             "<svg/>\r\n",
		"tree/public/img/betterdesk_icon.png": "png bytes of the icon",
		"tree/public/img/login-slides/a.jpg":  "jpeg",
		"tree/public/.hidden":                 "hidden",
		"tree/protos/message.proto":           "syntax = \"proto3\";",
		"data/fonts/Inter/inter.woff2":        "woff2 of the font",
		"data/fonts/Inter/meta.json":          `{"family":"Inter"}`,
	} {
		full := filepath.Join(site, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(full, at, at); err != nil {
			t.Fatal(err)
		}
	}
	fonts := filepath.Join(site, "data", "fonts")
	onDisk := StaticFoldersOptions{PublicDir: filepath.Join(site, "tree", "public"), ProtosDir: filepath.Join(site, "tree", "protos"), FontsDir: fonts}
	fromTree := StaticFoldersOptions{Static: os.DirFS(filepath.Join(site, "tree")), FontsDir: fonts}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })

	etag := serve(StaticFolders(onDisk)(next), httptest.NewRequest("GET", "/favicon.svg", nil)).Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag on disk")
	}
	type request struct {
		method, target string
		header         map[string]string
	}
	requests := []request{}
	for _, target := range []string{
		"/favicon.svg", "/img/betterdesk_icon.png", "/img/login-slides/a.jpg", "/img", "/img/", "/img/login-slides",
		"/IMG/betterdesk_icon.png", "/protos/message.proto", "/protos", "/protos/", "/PROTOS/message.proto",
		"/fonts", "/fonts/", "/fonts/Inter/inter.woff2", "/fonts/Inter", "/fonts/missing.woff2",
		"/.hidden", "/img/../favicon.svg", "/img/%2e%2e/favicon.svg", "/%2e%2e/%2e%2e/etc/passwd", "/favicon.svg?v=1",
		"/missing.png", "/favicon.svg/", "/img%2fbetterdesk_icon.png", "/fav%69con.svg",
	} {
		requests = append(requests,
			request{"GET", target, nil},
			request{"HEAD", target, nil},
			request{"GET", target, map[string]string{"If-None-Match": etag}},
			request{"GET", target, map[string]string{"If-Modified-Since": at.Format(http.TimeFormat)}},
			request{"GET", target, map[string]string{"Range": "bytes=1-3"}},
			request{"GET", target, map[string]string{"Range": "bytes=99-"}},
			request{"POST", target, nil},
		)
	}
	for _, production := range []bool{false, true} {
		onDisk.Production, fromTree.Production = production, production
		disk, tree := StaticFolders(onDisk)(next), StaticFolders(fromTree)(next)
		for _, rq := range requests {
			r1, r2 := httptest.NewRequest(rq.method, rq.target, nil), httptest.NewRequest(rq.method, rq.target, nil)
			for k, v := range rq.header {
				r1.Header.Set(k, v)
				r2.Header.Set(k, v)
			}
			want, got := serve(disk, r1), serve(tree, r2)
			label := rq.method + " " + rq.target + " " + strings.TrimSpace(strings.Join(slices.Sorted(maps.Keys(rq.header)), ","))
			if got.Code != want.Code || got.Body.String() != want.Body.String() {
				t.Errorf("%s: %d %q from the tree, %d %q from disk", label, got.Code, got.Body.String(), want.Code, want.Body.String())
			}
			for _, name := range slices.Sorted(maps.Keys(want.Header())) {
				if got.Header().Get(name) != want.Header().Get(name) {
					t.Errorf("%s: %s %q from the tree, %q from disk", label, name, got.Header().Get(name), want.Header().Get(name))
				}
			}
			if len(got.Header()) != len(want.Header()) {
				t.Errorf("%s: headers %v from the tree, %v from disk", label, got.Header(), want.Header())
			}
		}
	}

	// The installed fonts are files, and only below /fonts
	if rec := serve(StaticFolders(fromTree)(next), httptest.NewRequest("GET", "/fonts/Inter/meta.json", nil)); rec.Code != 200 || rec.Body.String() != `{"family":"Inter"}` {
		t.Errorf("an installed font's file: %d %q", rec.Code, rec.Body.String())
	}
	if rec := serve(StaticFolders(fromTree)(next), httptest.NewRequest("GET", "/fonts", nil)); rec.Code != http.StatusMovedPermanently || rec.Header().Get("Location") != "/fonts/" {
		t.Errorf("the fonts folder: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := serve(StaticFolders(fromTree)(next), httptest.NewRequest("GET", "/Inter/meta.json", nil)); rec.Code != http.StatusTeapot {
		t.Errorf("a font's file at the root: %d", rec.Code)
	}
	// Without the tree and without folders nothing of it is served
	bare := StaticFolders(StaticFoldersOptions{FontsDir: fonts})(next)
	for _, target := range []string{"/favicon.svg", "/protos/message.proto"} {
		if rec := serve(bare, httptest.NewRequest("GET", target, nil)); rec.Code != http.StatusTeapot {
			t.Errorf("%s without the tree: %d", target, rec.Code)
		}
	}
}

// The tree answers only for names below its root that are clean paths in
// it; anything else is a file that is not there.
func TestStaticTreeNames(t *testing.T) {
	root := staticRoot("public")
	tree := staticTree{root: root, dir: "public", fsys: fstest.MapFS{
		"public/a/b.txt": {Data: []byte("b")},
		"secret.txt":     {Data: []byte("s")},
	}}
	sep := string(filepath.Separator)
	if info, err := tree.Stat(root + sep + "a" + sep + "b.txt"); err != nil || info.Size() != 1 {
		t.Fatalf("a file of the tree: %v", err)
	}
	if info, err := tree.Stat(root); err != nil || !info.IsDir() {
		t.Errorf("the root: %v", err)
	}
	for _, name := range []string{
		root + sep + "a" + sep + sep + "b.txt",
		root + sep + "a" + sep + "." + sep + "b.txt",
		root + sep + ".." + sep + "secret.txt",
		root + "x" + sep + "a" + sep + "b.txt",
		filepath.Dir(root) + sep + "secret.txt",
	} {
		if _, err := tree.Stat(name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(%q) = %v, want a file that is not there", name, err)
		}
		if f, err := tree.Open(name); !errors.Is(err, fs.ErrNotExist) {
			if f != nil {
				_ = f.Close()
			}
			t.Errorf("Open(%q) = %v, want a file that is not there", name, err)
		}
	}
}
