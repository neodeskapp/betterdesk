package agentreleases

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestResolveAllowedGithubHost(t *testing.T) {
	for host, want := range map[string]string{
		"release-assets.githubusercontent.com": "release-assets.githubusercontent.com",
		"objects.githubusercontent.com":        "objects.githubusercontent.com",
		"api.github.com":                       "api.github.com",
		"github.com":                           "github.com",
		"GitHub.com":                           "github.com",
		"evil.example":                         "",
		"127.0.0.1":                            "",
		"":                                     "",
		"api.github.com.evil.example":          "",
		// what every JavaScript object has, which a lookup in a plain
		// object let through (BUGS AG1)
		"constructor": "",
		"__proto__":   "",
		"Constructor": "",
		"toString":    "",
	} {
		if got := ResolveAllowedGithubHost(host); got != want {
			t.Errorf("ResolveAllowedGithubHost(%q) = %q, want %q", host, got, want)
		}
	}
	want := []string{"api.github.com", "github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com"}
	if got := GithubHosts(); !reflect.DeepEqual(got, want) {
		t.Errorf("GithubHosts = %v", got)
	}
}

func TestSigningKey(t *testing.T) {
	s := New(Options{})
	// The fingerprint the Node console reported for its built-in key
	if got := s.SigningKeyFingerprint(); got != "ea2b916f457871f0" || s.KeyFromEnvironment() {
		t.Errorf("built-in key: %q, from the environment %v", got, s.KeyFromEnvironment())
	}
	s = New(Options{PublicKey: "  abc  "})
	sum := sha256.Sum256([]byte("abc"))
	if got := s.SigningKeyFingerprint(); got != hex.EncodeToString(sum[:])[:16] || !s.KeyFromEnvironment() {
		t.Errorf("key from the environment: %q", got)
	}
}

// decodeBase64 decodes as Node's Buffer.from(text, 'base64') did on the
// same texts.
func TestDecodeBase64AsNode(t *testing.T) {
	var vectors [][2]string
	raw := `[["",""],["YQ==","61"],["YQ","61"],["YWI=","6162"],["YWJj","616263"],["YW Jj","616263"],["YW\nJj","616263"],["YQ==YQ==","61"],["-_-_","fbffbf"],["+/+/","fbffbf"],["Y!Q@=","61"],["Y",""],["YWJjZA","61626364"],["====",""],["Y=Q",""],["eXGPOvUvFfz1FGGbYogKfYEEWVWzeJEKpmI1ngAU490=","79718f3af52f15fcf514619b62880a7d81045955b378910aa662359e0014e3dd"],["eXGPOvUvFfz1FGGbYogKfYEEWVWzeJEKpmI1ngAU490","79718f3af52f15fcf514619b62880a7d81045955b378910aa662359e0014e3dd"],["eXGP OvUv\tFfz1FGGbYogKfYEEWVWzeJEKpmI1ngAU490=","79718f3af52f15fcf514619b62880a7d81045955b378910aa662359e0014e3dd"],["ä ö",""],["YWJj=ZGVm","616263"]]`
	if err := json.Unmarshal([]byte(raw), &vectors); err != nil {
		t.Fatal(err)
	}
	for _, v := range vectors {
		if got := hex.EncodeToString(decodeBase64(v[0])); got != v[1] {
			t.Errorf("decodeBase64(%q) = %s, Node gave %s", v[0], got, v[1])
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestListCachedAndResolveAsset(t *testing.T) {
	data := t.TempDir()
	s := New(Options{DataDir: data})
	if got := s.ListCached(); len(got) != 0 || s.CacheExists() {
		t.Fatalf("without a cache: %v, exists %v", got, s.CacheExists())
	}
	root := s.Root()
	if root != filepath.Join(data, "modules", "neodesk-support-releases") {
		t.Fatalf("root = %s", root)
	}
	writeFile(t, filepath.Join(root, "support-v1.2.0", "manifest.json"), `{"imported_at":"x","assets":[]}`)
	writeFile(t, filepath.Join(root, "support-v1.10.0", "manifest.json"), `{"assets":[1]}`)
	writeFile(t, filepath.Join(root, "support-v1.2.0-rc", "manifest.json"), `{"imported_at":null}`)
	writeFile(t, filepath.Join(root, "array", "manifest.json"), `[1,2]`)
	writeFile(t, filepath.Join(root, "broken", "manifest.json"), `not json`)
	writeFile(t, filepath.Join(root, "null", "manifest.json"), `null`)
	writeFile(t, filepath.Join(root, "support-v1.2.0", "agent.zip"), "zip")
	writeFile(t, filepath.Join(root, "top.txt"), "top")
	writeFile(t, filepath.Join(root, "..", "beside.txt"), "outside the cache")
	if err := os.MkdirAll(filepath.Join(root, "support-v1.2.0", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(s.ListCached())
	want := `[{"tag":"support-v1.2.0-rc","imported_at":null,"manifest":{"imported_at":null}},` +
		`{"tag":"support-v1.2.0","imported_at":"x","manifest":{"imported_at":"x","assets":[]}},` +
		`{"tag":"support-v1.10.0","manifest":{"assets":[1]}},` +
		`{"tag":"array","manifest":[1,2]}]`
	if string(got) != want {
		t.Errorf("ListCached =\n %s\nwant\n %s", got, want)
	}
	for _, c := range []struct{ tag, name, want string }{
		{"support-v1.2.0", "agent.zip", filepath.Join(root, "support-v1.2.0", "agent.zip")},
		{".", "top.txt", filepath.Join(root, "top.txt")},
		{"support-v1.2.0", "missing.zip", ""},
		{"support-v1.2.0", "sub", ""},
		{"support-v1.2.0", "..", ""},
		{"..", "neodesk-support-releases", ""},
		{"..", "beside.txt", ""},
		{"support-v1.2.0", "a b.zip", ""},
		{"", "agent.zip", ""},
		{"support-v1.2.0/..", "top.txt", ""},
	} {
		if got := s.ResolveAsset(c.tag, c.name); got != c.want {
			t.Errorf("ResolveAsset(%q, %q) = %q, want %q", c.tag, c.name, got, c.want)
		}
	}
}

// github is a made-up GitHub: answers by URL, and the requests it got.
type github struct {
	mu       sync.Mutex
	answers  map[string]func() *http.Response
	requests []string
}

func (g *github) RoundTrip(req *http.Request) (*http.Response, error) {
	g.mu.Lock()
	g.requests = append(g.requests, req.Method+" "+req.URL.String()+" UA="+req.Header.Get("User-Agent")+" Accept="+req.Header.Get("Accept"))
	answer := g.answers[req.URL.String()]
	g.mu.Unlock()
	if answer == nil {
		return nil, errors.New("no answer for " + req.URL.String())
	}
	return answer(), nil
}

func reply(status int, body []byte, header ...string) func() *http.Response {
	return func() *http.Response {
		h := http.Header{}
		for i := 0; i+1 < len(header); i += 2 {
			h.Set(header[i], header[i+1])
		}
		return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(bytes.NewReader(body))}
	}
}

func zipOf(t *testing.T, names ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range names {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("x"))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type releaseFixture struct {
	key      ed25519.PrivateKey
	public   string
	files    map[string][]byte
	manifest map[string]any
	gh       *github
}

// newRelease is a signed release support-v2.0.0 with a zip and a tar.gz,
// whose files are served from github.com through a redirect to GitHub's
// file host.
func newRelease(t *testing.T, zipBody []byte) *releaseFixture {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	f := &releaseFixture{key: key, public: base64.StdEncoding.EncodeToString(pub), files: map[string][]byte{
		"agent-windows.zip":  zipBody,
		"agent-linux.tar.gz": []byte("tar.gz bytes"),
	}}
	var sums strings.Builder
	var assets []any
	for _, name := range []string{"agent-windows.zip", "agent-linux.tar.gz"} {
		sum := sha256.Sum256(f.files[name])
		digest := hex.EncodeToString(sum[:])
		sums.WriteString(digest + "  " + name + "\n")
		assets = append(assets, map[string]any{"name": name, "platform": "p", "sha256": strings.ToUpper(digest), "size": len(f.files[name])})
	}
	f.manifest = map[string]any{"schema_version": 1, "sku": "neodesk-support", "generic": true, "external_config": false, "assets": assets}
	f.sign(t)
	f.files["SHA256SUMS"] = []byte(sums.String())
	return f
}

func (f *releaseFixture) sign(t *testing.T) {
	t.Helper()
	body, err := json.Marshal(f.manifest)
	if err != nil {
		t.Fatal(err)
	}
	f.files["manifest.json"] = body
	f.files["manifest.sig"] = []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(f.key, body)) + "\n")
}

func (f *releaseFixture) serve() *github {
	g := &github{answers: map[string]func() *http.Response{}}
	var assets []any
	for name, body := range f.files {
		page := "https://github.com/neodeskapp/neodesk-client/releases/download/support-v2.0.0/" + name
		file := "https://objects.githubusercontent.com/files/" + name
		g.answers[page] = reply(http.StatusFound, nil, "Location", file)
		g.answers[file] = reply(http.StatusOK, body)
		assets = append(assets, map[string]any{"name": name, "browser_download_url": page})
	}
	list, _ := json.Marshal([]any{
		map[string]any{"tag_name": "support-v3.0.0", "draft": true},
		map[string]any{"tag_name": "support-v2.5.0", "prerelease": true},
		map[string]any{"tag_name": "other-v9"},
		map[string]any{"tag_name": "support-v2.0.0", "assets": assets},
	})
	g.answers["https://api.github.com/repos/neodeskapp/neodesk-client/releases?per_page=30"] = reply(http.StatusOK, list)
	f.gh = g
	return g
}

func (f *releaseFixture) service(t *testing.T, data string, tar string) *Service {
	return New(Options{
		DataDir: data, PublicKey: f.public, HTTP: f.serve(), PID: 4242,
		Now:     func() time.Time { return time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) },
		ListTar: func(context.Context, []byte) (string, error) { return tar, nil },
	})
}

func TestImportLatest(t *testing.T) {
	f := newRelease(t, zipOf(t, "agent/NeoDesk.exe", "agent/README.txt"))
	data := t.TempDir()
	s := f.service(t, data, "agent/\nagent/neodesk\n")
	imported, err := s.ImportLatest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(s.Root(), "support-v2.0.0")
	if imported.Tag != "support-v2.0.0" || imported.Path != dest {
		t.Errorf("imported %+v", imported)
	}
	for _, name := range []string{"agent-windows.zip", "agent-linux.tar.gz"} {
		got, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil || !bytes.Equal(got, f.files[name]) {
			t.Errorf("%s: %v", name, err)
		}
	}
	stored, err := os.ReadFile(filepath.Join(dest, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(stored), "{\n  \"assets\": [\n    {\n      \"name\": \"agent-windows.zip\",\n") ||
		!strings.HasSuffix(string(stored), "  \"release_tag\": \"support-v2.0.0\",\n  \"imported_at\": \"2026-11-10T12:00:00.000Z\"\n}\n") {
		t.Errorf("stored manifest:\n%s", stored)
	}
	if _, err := os.Stat(dest + ".tmp-4242-1794312000000"); !os.IsNotExist(err) {
		t.Errorf("the staging folder is left: %v", err)
	}
	if got := s.ListCached(); len(got) != 1 || got[0].Tag != "support-v2.0.0" {
		t.Errorf("ListCached after the import: %+v", got)
	}
	first := f.gh.requests[0]
	if first != "GET https://api.github.com/repos/neodeskapp/neodesk-client/releases?per_page=30 UA=NeoDesk-Console-Support-Releases Accept=application/vnd.github+json" {
		t.Errorf("first request: %s", first)
	}
	for _, r := range f.gh.requests[1:] {
		if !strings.HasSuffix(r, "Accept=application/octet-stream") {
			t.Errorf("file request: %s", r)
		}
	}
	// A second import of the same tag is told so before downloading
	f.gh.requests = nil
	_, err = s.ImportLatest(context.Background())
	if !errors.Is(err, ErrAlreadyImported) || err.Error() != "generic_agent_release_already_imported:support-v2.0.0" || len(f.gh.requests) != 1 {
		t.Errorf("second import: %v after %d requests", err, len(f.gh.requests))
	}
	if entries, _ := os.ReadDir(s.Root()); len(entries) != 1 {
		t.Errorf("the cache holds %d entries after the second import", len(entries))
	}
}

func TestImportRefusals(t *testing.T) {
	cases := []struct {
		name   string
		change func(t *testing.T, f *releaseFixture)
		tar    string
		zip    []byte
		want   string
	}{
		{name: "bad signature", change: func(t *testing.T, f *releaseFixture) {
			f.files["manifest.sig"] = []byte(base64.StdEncoding.EncodeToString(make([]byte, 64)))
		}, want: "generic_agent_release_signature_invalid"},
		{name: "checksum", change: func(t *testing.T, f *releaseFixture) {
			f.files["SHA256SUMS"] = []byte(strings.Repeat("0", 64) + "  agent-windows.zip\n")
		}, want: "generic_agent_release_checksum_mismatch:agent-windows.zip"},
		{name: "custom.txt in the zip", zip: nil, want: "generic_agent_release_external_config"},
		{name: "custom.txt in the tar", tar: "agent/Custom.TXT\n", want: "generic_agent_release_external_config"},
		{name: "manifest not generic", change: func(t *testing.T, f *releaseFixture) {
			f.manifest["generic"] = false
			f.sign(t)
		}, want: "generic_agent_release_manifest_invalid"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := zipOf(t, "agent/x.exe")
			if c.name == "custom.txt in the zip" {
				body = zipOf(t, "agent/x.exe", `agent\custom.txt`)
			}
			f := newRelease(t, body)
			if c.change != nil {
				c.change(t, f)
			}
			tar := c.tar
			if tar == "" {
				tar = "agent/x\n"
			}
			s := f.service(t, t.TempDir(), tar)
			if _, err := s.ImportLatest(context.Background()); err == nil || err.Error() != c.want {
				t.Errorf("error %v, want %s", err, c.want)
			}
			if entries, _ := os.ReadDir(s.Root()); len(entries) != 0 {
				t.Errorf("a refused release left %d entries", len(entries))
			}
		})
	}
}

func TestRequestsToGitHub(t *testing.T) {
	list := "https://api.github.com/repos/neodeskapp/neodesk-client/releases?per_page=30"
	for _, c := range []struct {
		name   string
		answer func() *http.Response
		more   map[string]func() *http.Response
		want   string
	}{
		{"list fails", reply(http.StatusForbidden, []byte("rate limited")), nil, "generic_agent_release_list_failed:403"},
		{"no support release", reply(http.StatusOK, []byte(`[{"tag_name":"v1"}]`)), nil, "generic_agent_release_not_found"},
		{"no list", reply(http.StatusOK, []byte(`{"message":"x"}`)), nil, "releases.find is not a function"},
		{"null", reply(http.StatusOK, []byte(`null`)), nil, "Cannot read properties of null (reading 'find')"},
		{"redirect elsewhere", reply(http.StatusMovedPermanently, nil, "Location", "https://evil.example/x"), nil, "generic_agent_release_redirect_host_invalid"},
		{"redirect to constructor", reply(http.StatusFound, nil, "Location", "https://constructor/x"), nil, "generic_agent_release_redirect_host_invalid"},
		{"redirect to http", reply(http.StatusTemporaryRedirect, nil, "Location", "http://github.com/x"), nil, `Protocol "http:" not supported. Expected "https:"`},
		{"redirect loop", reply(http.StatusFound, nil, "Location", list), nil, "generic_agent_release_redirect_limit"},
		{"303 is no redirect", reply(http.StatusSeeOther, nil, "Location", "https://github.com/x"), nil, "generic_agent_release_list_failed:303"},
		{"no manifest", reply(http.StatusOK, []byte(`[{"tag_name":"support-v1","assets":[{"name":"x"}]}]`)), nil, "generic_agent_release_manifest_missing"},
		{"asset elsewhere", reply(http.StatusOK, []byte(`[{"tag_name":"support-v1","assets":[`+
			`{"name":"manifest.json","browser_download_url":"https://evil.example/m"},{"name":"SHA256SUMS"},{"name":"manifest.sig"}]}]`)),
			nil, "generic_agent_release_asset_invalid"},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := &github{answers: map[string]func() *http.Response{list: c.answer}}
			for k, v := range c.more {
				g.answers[k] = v
			}
			s := New(Options{DataDir: t.TempDir(), HTTP: g})
			if _, err := s.ImportLatest(context.Background()); err == nil || err.Error() != c.want {
				t.Errorf("error %v, want %s", err, c.want)
			}
			if c.name == "redirect loop" && len(g.requests) != 4 {
				t.Errorf("redirect loop: %d requests, want the first and three redirects", len(g.requests))
			}
		})
	}
}
