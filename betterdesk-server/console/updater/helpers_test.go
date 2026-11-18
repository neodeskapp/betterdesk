package updater

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	oldCommit = "1111111111111111111111111111111111111111"
	newCommit = "2222222222222222222222222222222222222222"
	asset     = "betterdesk-server-linux-amd64"
)

// fakeGitHub answers the requests of a Service: the API, the release
// files on github.com and their redirects to the file host. Routes are
// "GET https://host/path"; a missing route is 404.
type fakeGitHub struct {
	mu       sync.Mutex
	routes   map[string]func(*http.Request) *http.Response
	requests []string
	headers  []http.Header
}

func newFakeGitHub() *fakeGitHub {
	return &fakeGitHub{routes: map[string]func(*http.Request) *http.Response{}}
}

func (f *fakeGitHub) RoundTrip(req *http.Request) (*http.Response, error) {
	key := req.Method + " " + req.URL.Scheme + "://" + req.URL.Host + req.URL.EscapedPath()
	if req.URL.RawQuery != "" {
		key += "?" + req.URL.RawQuery
	}
	f.mu.Lock()
	f.requests = append(f.requests, key)
	f.headers = append(f.headers, req.Header.Clone())
	route := f.routes[key]
	f.mu.Unlock()
	if route == nil {
		return respond(404, `{"message":"Not Found"}`, nil), nil
	}
	return route(req), nil
}

func (f *fakeGitHub) on(key string, fn func(*http.Request) *http.Response) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[key] = fn
}

func (f *fakeGitHub) count(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if strings.HasPrefix(r, prefix) {
			n++
		}
	}
	return n
}

func respond(status int, body string, header http.Header) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body))}
}

func redirect(location string) *http.Response {
	return respond(302, "", http.Header{"Location": {location}})
}

// testRelease is a release the fake GitHub serves.
type testRelease struct {
	tag, version, commit, branch string
	binaries                     map[string][]byte
	manifest                     *Manifest
	manifestText                 []byte
	signature                    []byte
}

// binaryFor is the content of a fake binary: a shell script that prints
// the -version line, so the real runVersion can run it on Unix.
func binaryFor(version, commit string) []byte {
	line := fmt.Sprintf("betterdesk-server %s (built 2026-11-18", version)
	if commit != "" {
		line += ", commit " + commit
	}
	return []byte("#!/bin/sh\necho '" + line + ")'\n")
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func newTestRelease(version, commit, branch string) *testRelease {
	tag := "v" + version
	if branch == BranchDevelopment {
		tag = "dev"
	}
	r := &testRelease{tag: tag, version: version, commit: commit, branch: branch, binaries: map[string][]byte{
		asset:                                 binaryFor(version, commit),
		"betterdesk-server-windows-amd64.exe": []byte("MZ windows " + version),
	}}
	r.manifest = &Manifest{SchemaVersion: 1, Product: Product, Version: version, Channel: ChannelOf(branch), Created: "2026-11-18T10:00:00Z",
		Owner: "UNITRONIX", Repo: "BetterDesk", Branch: branch, SHA: commit}
	for _, name := range []string{asset, "betterdesk-server-windows-amd64.exe"} {
		r.manifest.Files = append(r.manifest.Files, ManifestFile{Path: name, SHA256: sum(r.binaries[name]), Size: int64(len(r.binaries[name]))})
	}
	r.render()
	return r
}

func (r *testRelease) render() {
	r.manifestText, _ = json.MarshalIndent(r.manifest, "", "  ")
}

func (r *testRelease) sign(private ed25519.PrivateKey) {
	r.signature = []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(private, CanonicalPayload(r.manifest.Signed()))) + "\n")
}

// serve puts the release on the fake GitHub: the API's answer and every
// file, each on github.com with a redirect to the file host.
func (f *fakeGitHub) serve(r *testRelease) {
	path := "/repos/UNITRONIX/BetterDesk/releases/latest"
	if r.branch == BranchDevelopment {
		path = "/repos/UNITRONIX/BetterDesk/releases/tags/dev"
	}
	type ghAsset struct {
		Name string `json:"name"`
		Size int    `json:"size"`
		URL  string `json:"browser_download_url"`
	}
	files := map[string][]byte{"manifest.json": r.manifestText}
	if r.signature != nil {
		files["manifest.sig"] = r.signature
	}
	for name, body := range r.binaries {
		files[name] = body
	}
	var assets []ghAsset
	for name, body := range files {
		url := "https://github.com/UNITRONIX/BetterDesk/releases/download/" + r.tag + "/" + name
		assets = append(assets, ghAsset{Name: name, Size: len(body), URL: url})
		content := body
		target := "https://objects.githubusercontent.com/github-production-release-asset/" + name
		f.on("GET "+url, func(*http.Request) *http.Response { return redirect(target) })
		f.on("GET "+target, func(*http.Request) *http.Response { return respond(200, string(content), nil) })
	}
	release, _ := json.Marshal(map[string]any{
		"tag_name": r.tag, "name": "BetterDesk " + r.version + "\nnotes", "draft": false, "prerelease": r.branch == BranchDevelopment,
		"published_at": "2026-11-18T10:00:00Z", "author": map[string]string{"login": "unitronix"}, "assets": assets,
	})
	f.on("GET https://api.github.com"+path, func(*http.Request) *http.Response { return respond(200, string(release), nil) })
	if r.tag != "dev" {
		f.on("GET https://api.github.com/repos/UNITRONIX/BetterDesk/releases/tags/"+r.tag, func(*http.Request) *http.Response { return respond(200, string(release), nil) })
	}
	compare, _ := json.Marshal(map[string]any{
		"total_commits": 3,
		"commits": []map[string]any{
			{"sha": oldCommit, "commit": map[string]any{"message": "first\nbody", "committer": map[string]string{"date": "d1"}, "author": map[string]string{"name": "a"}}},
			{"sha": r.commit, "commit": map[string]any{"message": "last", "committer": map[string]string{"date": "d2"}, "author": map[string]string{"name": "b"}}},
		},
		"files": []map[string]string{
			{"filename": "betterdesk-server/main.go", "status": "modified", "sha": "x"},
			{"filename": "web-ui/src/app.ts", "status": "renamed", "previous_filename": "web-ui/src/old.ts"},
			{"filename": "docs/readme.md", "status": "modified"},
			{"filename": "web-nodejs/server.js", "status": "removed"},
			{"filename": "betterdesk.sh"},
		},
	})
	f.on("GET https://api.github.com/repos/UNITRONIX/BetterDesk/compare/"+oldCommit+"..."+r.commit, func(*http.Request) *http.Response {
		return respond(200, string(compare), nil)
	})
}

// install is a fake installation: a binary of the old version in a
// folder of its own, the data folder, and a Service for them.
type install struct {
	t       *testing.T
	dir     string
	binary  string
	dataDir string
	gh      *fakeGitHub
	env     map[string]string
	svc     *Service
	opts    Options
}

func newInstall(t *testing.T, mutate ...func(*Options)) *install {
	t.Helper()
	root := t.TempDir()
	in := &install{t: t, dir: filepath.Join(root, "bin"), dataDir: filepath.Join(root, "data"), gh: newFakeGitHub(), env: map[string]string{}}
	in.binary = filepath.Join(in.dir, "betterdesk-server")
	for _, dir := range []string{in.dir, in.dataDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(in.binary, binaryFor("3.5.0", oldCommit), 0o755); err != nil {
		t.Fatal(err)
	}
	aside := false
	in.opts = Options{
		DataDir: in.dataDir, ConsoleDir: filepath.Join(root, "console"), Version: "3.5.0", Commit: oldCommit,
		Getenv:     func(k string) string { return in.env[k] },
		Keys:       Keys{},
		Transport:  in.gh,
		Executable: func() (string, error) { return in.binary, nil },
		GOOS:       "linux", GOARCH: "amd64",
		RenameAside: &aside,
		RunVersion:  fakeRunVersion,
		FreeSpace:   func(string) (int64, error) { return 10 << 30, nil },
		Now:         func() time.Time { return time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC) },
		PID:         4242,
		Logger:      quietLogger{},
	}
	for _, m := range mutate {
		m(&in.opts)
	}
	in.svc = New(in.opts)
	return in
}

type quietLogger struct{}

func (quietLogger) Warn(...any) {}
func (quietLogger) Info(...any) {}

// fakeRunVersion "runs" a fake binary by reading the line its script
// prints, as the real one would.
var reEcho = regexp.MustCompile(`echo '([^']*)'`)

func fakeRunVersion(_ context.Context, binary string) (string, error) {
	data, err := os.ReadFile(binary)
	if err != nil {
		return "", err
	}
	m := reEcho.FindSubmatch(data)
	if m == nil {
		return "", fmt.Errorf("exec format error")
	}
	return string(m[1]) + "\n", nil
}

func (in *install) binaryContent() string {
	in.t.Helper()
	data, err := os.ReadFile(in.binary)
	if err != nil {
		in.t.Fatal(err)
	}
	return string(data)
}

// assertUntouched checks that the install is as it was: the old binary
// in place, no pending marker, no copy left in the binary's folder and
// no download left in the data folder.
func (in *install) assertUntouched() {
	in.t.Helper()
	if got := in.binaryContent(); got != string(binaryFor("3.5.0", oldCommit)) {
		in.t.Errorf("the binary changed: %q", got)
	}
	if _, err := os.Stat(in.binary + ".update-pending.json"); err == nil {
		in.t.Error("a pending marker was written")
	}
	entries, _ := os.ReadDir(in.dir)
	for _, e := range entries {
		if e.Name() != "betterdesk-server" && e.Name() != "betterdesk-server.prev" {
			in.t.Errorf("left in the binary's folder: %s", e.Name())
		}
	}
	if in.svc.busy.Load() {
		in.t.Error("the service is still busy")
	}
	if _, err := os.Stat(filepath.Join(in.dataDir, inProgressFile)); err == nil {
		in.t.Error("the in-progress marker is still there")
	}
	if entries, err := os.ReadDir(filepath.Join(in.dataDir, updatesDir)); err == nil {
		for _, e := range entries {
			if files, _ := os.ReadDir(filepath.Join(in.dataDir, updatesDir, e.Name())); len(files) > 0 {
				in.t.Errorf("a download is left: %s", e.Name())
			}
		}
	}
}

func (in *install) readJSON(path string, v any) {
	in.t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		in.t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		in.t.Fatalf("%s: %v", path, err)
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(b.String())
}
