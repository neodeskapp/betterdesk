package generator

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/serverinfo"
	"github.com/unitronix/betterdesk-server/console/ssrfguard"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// A seed made up for these tests.
var testSeed = base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{5}, 32))

type fakeServerInfo struct{}

func (fakeServerInfo) ResolvePublicKey(context.Context) serverinfo.NullString { return "PUBKEY" }
func (fakeServerInfo) ResolveClientFacingHost(*serverinfo.RequestHost, string) string {
	return "desk.example.test"
}
func (fakeServerInfo) DefaultServerHost() string { return "desk.example.test" }
func (fakeServerInfo) DefaultUseHTTPS() bool     { return false }
func (fakeServerInfo) DefaultAPIPort() string    { return "21114" }
func (fakeServerInfo) DefaultRelayPort() string  { return "21117" }

// fakeRunner records the tools a test's service starts and answers with
// what its handler says.
type fakeRunner struct {
	mu       sync.Mutex
	commands []Command
	handle   func(c Command) (Result, error)
}

func (f *fakeRunner) Run(_ context.Context, c Command) (Result, error) {
	f.mu.Lock()
	f.commands = append(f.commands, c)
	f.mu.Unlock()
	if f.handle == nil {
		return Result{}, errors.New("no tool expected")
	}
	return f.handle(c)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// templateTree writes a complete template tree below root: the six
// portable templates with binaries over 100 KB.
func templateTree(t *testing.T, root string) {
	t.Helper()
	var templates []any
	for _, name := range requiredTemplates {
		dir := filepath.Join(root, name)
		must(t, os.MkdirAll(dir, 0o755))
		must(t, os.WriteFile(filepath.Join(dir, ".custom-txt-here"), []byte("marker\n"), 0o644))
		binary := "betterdesk"
		if strings.HasPrefix(name, "windows") {
			binary = "betterdesk.exe"
		}
		must(t, os.WriteFile(filepath.Join(dir, binary), bytes.Repeat([]byte{1}, 110*1024), 0o755))
		must(t, os.WriteFile(filepath.Join(root, name+".tar.gz"), []byte("archive"), 0o644))
		platform, arch, _ := strings.Cut(name, "-")
		templates = append(templates, goapi.NewObject("platform", platform, "arch", arch, "format", "portable", "template_path", name,
			"binary_path", binary, "archive", name+".tar.gz", "sha256", strings.Repeat("a", 64)))
	}
	required := make([]any, len(requiredTemplates))
	for i, r := range requiredTemplates {
		required[i] = r
	}
	manifest := goapi.NewObject("schema_version", float64(2), "sku", "generator-templates", "version", "1.0.0",
		"required_platforms", required, "templates", templates)
	must(t, os.WriteFile(filepath.Join(root, "manifest.json"), []byte(stringify(manifest)), 0o644))
}

// readyModule makes the template module ready: templates, state, seed.
func readyModule(t *testing.T, s *Service) {
	t.Helper()
	templateTree(t, s.TemplatesDir())
	must(t, os.WriteFile(s.moduleStatePath(), []byte(`{"termsAccepted":true,"status":"ready"}`), 0o600))
	must(t, os.WriteFile(s.SigningSeedPath(), []byte(testSeed+"\n"), 0o600))
}

// newBundle stores a bundle with a finalized profile, as the create route
// does, and returns it.
func newBundle(t *testing.T, s *Service, profile *goapi.Object, commit string) *Bundle {
	t.Helper()
	ctx := context.Background()
	v, err := ValidateBranding(profile, nil)
	if err != nil || !v.Valid {
		t.Fatalf("profile: %v %v", err, v.Errors)
	}
	normalized := s.FinalizeSupportBranding(ctx, v.Normalized)
	id, err := s.NewBundleID()
	must(t, err)
	normalized.Set("bundle_id", id)
	fingerprint := HashBranding(normalized)
	hash := fingerprint
	if commit != "" {
		hash = GenerationKey(fingerprint, commit)
	}
	b, err := s.CreateBundle(ctx, NewBundle{BundleID: id, Slug: "b" + id, Name: "Test", Branding: stringify(normalized),
		BrandingHash: hash, ConfigFingerprint: fingerprint, ClientCommit: commit, ProductType: ProductSupport})
	must(t, err)
	return b
}

func startWorker(s *Service) {
	s.worker.mu.Lock()
	s.worker.startupReady = true
	s.worker.mu.Unlock()
}

func buildsByKey(t *testing.T, s *Service, hash string) map[string]*Build {
	t.Helper()
	list, err := s.ListBuildsForHash(context.Background(), hash)
	must(t, err)
	out := map[string]*Build{}
	for _, b := range list {
		out[b.Key()] = b
	}
	return out
}

// TestTemplateBuilds builds every queued template build. The stage gets
// the signed custom.txt of the bundle's profile and the installer
// scripts, the marker goes; Windows is zipped, the rest packed by tar in
// the work folder; each row ends ready with the artifact's name, size and
// checksum. The Linux x64 build comes first: it is the host's.
func TestTemplateBuilds(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		runner := &fakeRunner{}
		s := New(Options{DB: db, ServerInfo: fakeServerInfo{}, DataDir: t.TempDir(), Run: runner, Env: func() []string { return nil }, GOOS: "linux", GOARCH: "amd64"})
		readyModule(t, s)
		profile := goapi.NewObject("company_name", "Acme", "server_host", "desk.example.com", "use_https", false,
			"install_service", true, "relay_host", "relay.example.com")
		bundle := newBundle(t, s, profile, "")
		must(t, s.EnqueueBuildsForHash(ctx, bundle.BrandingHash, false, nil, false))

		wantTxt, _, err := BuildAndSignSupportCustomTxt(CustomTxtOptions{
			AppName: "Acme", Host: "desk.example.com", Relay: "relay.example.com", API: "http://desk.example.com:21114",
			Key: "PUBKEY", DisableSettings: true, DisableSettingsSet: true,
		}, testSeed)
		must(t, err)
		var stages []string
		runner.handle = func(c Command) (Result, error) {
			if c.Name != "tar" || len(c.Args) != 3 || c.Args[0] != "-czf" {
				return Result{}, errors.New("unexpected tool " + c.Name + " " + strings.Join(c.Args, " "))
			}
			stage := filepath.Join(c.Dir, c.Args[2])
			stages = append(stages, stage)
			got, err := os.ReadFile(filepath.Join(stage, "custom.txt"))
			if err != nil || string(got) != wantTxt.Content {
				t.Errorf("custom.txt of %s: %v %q", stage, err, got)
			}
			if exists(filepath.Join(stage, ".custom-txt-here")) {
				t.Errorf("%s keeps the marker", stage)
			}
			platform := "linux"
			if strings.Contains(stage, "macos") {
				platform = "macos"
			}
			install, uninstall := UnixInstallerScripts(platform, InstallerOptions{InstallService: true})
			gotInstall, _ := os.ReadFile(filepath.Join(stage, "install-betterdesk-support.sh"))
			gotUninstall, _ := os.ReadFile(filepath.Join(stage, "uninstall-betterdesk-support.sh"))
			if string(gotInstall) != install || string(gotUninstall) != uninstall {
				t.Errorf("%s: installer scripts differ", stage)
			}
			return Result{}, os.WriteFile(c.Args[1], []byte("tgz:"+c.Args[2]), 0o644)
		}
		startWorker(s)
		for i := 0; i < 6; i++ {
			s.Tick(ctx)
		}
		if len(stages) != 4 || !strings.HasSuffix(stages[0], "betterdesk-support-linux-x64") {
			t.Fatalf("tar ran for %v, the host's build first", stages)
		}
		builds := buildsByKey(t, s, bundle.BrandingHash)
		for _, p := range Platforms {
			if p.Format != "portable" {
				continue
			}
			b := builds[p.Key()]
			ext := "tar.gz"
			if p.Platform == "windows" {
				ext = "zip"
			}
			name := "betterdesk-support-" + bundle.BrandingHash[:12] + "-" + p.Platform + "-" + p.Arch + "." + ext
			if b == nil || b.Status != "ready" || filepath.Base(b.ArtifactPath) != name || filepath.Dir(b.ArtifactPath) != s.ArtifactDir() {
				t.Fatalf("%s: %+v", p.Key(), b)
			}
			data, err := os.ReadFile(b.ArtifactPath)
			must(t, err)
			sum := sha256.Sum256(data)
			if b.ArtifactSHA256 != hex.EncodeToString(sum[:]) || b.Size() != float64(len(data)) {
				t.Errorf("%s: checksum or size do not match the file", p.Key())
			}
			if p.Platform == "windows" {
				zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
				must(t, err)
				names := map[string]bool{}
				for _, f := range zr.File {
					names[f.Name] = true
				}
				root := "betterdesk-support-windows-" + p.Arch + "/"
				for _, want := range []string{"custom.txt", "betterdesk.exe", "Install-BetterDesk.ps1", "Uninstall-BetterDesk.ps1"} {
					if !names[root+want] {
						t.Errorf("%s: no %s in the zip: %v", p.Key(), want, names)
					}
				}
				if names[root+".custom-txt-here"] {
					t.Errorf("%s: the zip has the marker", p.Key())
				}
			}
		}
		if entries, _ := os.ReadDir(s.workRoot()); len(entries) != 0 {
			t.Errorf("work folders left: %v", entries)
		}
	})
}

// TestTemplateBuildFails records the error of a failed build on its row.
func TestTemplateBuildFails(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		runner := &fakeRunner{handle: func(c Command) (Result, error) { return Result{Code: 2, Stderr: "  disk full \n"}, nil }}
		s := New(Options{DB: db, ServerInfo: fakeServerInfo{}, DataDir: t.TempDir(), Run: runner, Env: func() []string { return nil }, GOOS: "linux", GOARCH: "amd64"})
		readyModule(t, s)
		bundle := newBundle(t, s, goapi.NewObject("server_host", "h.example"), "")
		must(t, s.EnqueueBuildsForHash(ctx, bundle.BrandingHash, false, []any{goapi.NewObject("platform", "linux")}, true))
		startWorker(s)
		s.Tick(ctx)
		b := buildsByKey(t, s, bundle.BrandingHash)["linux/x64/portable"]
		if b == nil || b.Status != "failed" || !strings.Contains(string(mustRaw(t, b)), `"error_message":"tar failed (2): disk full"`) {
			t.Fatalf("row: %s", mustRaw(t, b))
		}
	})
}

func mustRaw(t *testing.T, b *Build) []byte {
	t.Helper()
	if b == nil {
		return []byte("null")
	}
	raw, err := b.MarshalJSON()
	must(t, err)
	return raw
}

// readyBuilder makes the client builder ready with an external provider
// that builds the given targets.
func readyBuilder(t *testing.T, s *Service, commit string) {
	t.Helper()
	must(t, os.MkdirAll(filepath.Join(s.SourceDir(), "res", "betterdesk"), 0o755))
	must(t, os.WriteFile(filepath.Join(s.SourceDir(), "Cargo.toml"), []byte("[package]\n"), 0o644))
	must(t, os.WriteFile(filepath.Join(s.SourceDir(), "res", "betterdesk", "custom-client-signing.pub"), []byte("old\n"), 0o644))
	must(t, os.WriteFile(s.builderStatePath(), []byte(`{"termsAccepted":true,"installedCommit":"`+commit+`","status":"ready"}`), 0o600))
}

// TestEmbeddedBuild runs a build with an external provider: the request
// file carries the signed configuration and the install's public key, the
// command line is the provider and the request in the source folder, the
// provider's progress reaches the row, every target's artifact is copied
// and checked, and the generation becomes the one the bundle hands out.
// A failed remote build marks every catalogue entry failed.
func TestEmbeddedBuild(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		commit := strings.Repeat("ab", 20)
		runner := &fakeRunner{}
		env := []string{
			"BETTERDESK_CLIENT_BUILD_COMMAND=/opt/provider",
			`BETTERDESK_CLIENT_PROVIDER_TARGETS_JSON=[{"platform":"linux","arch":"x64"},{"platform":"windows","arch":"x64"}]`,
			"BETTERDESK_CUSTOM_CLIENT_SIGNING_SEED=" + testSeed,
		}
		s := New(Options{DB: db, ServerInfo: fakeServerInfo{}, DataDir: t.TempDir(), Embedded: true, Run: runner, Env: func() []string { return env }, GOOS: "linux", GOARCH: "amd64"})
		readyBuilder(t, s, commit)
		if ready, err := s.GeneratorReady(); err != nil || !ready {
			t.Fatalf("builder not ready: %v", err)
		}
		bundle := newBundle(t, s, goapi.NewObject("company_name", "Acme", "server_host", "desk.example.com"), commit)
		must(t, s.EnqueueBuildsForHash(ctx, bundle.BrandingHash, false, nil, false))
		seed, _ := base64.StdEncoding.DecodeString(testSeed)
		wantKey := base64.StdEncoding.EncodeToString(ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey))
		wantTxt, _, err := BuildAndSignSupportCustomTxt(CustomTxtOptions{AppName: "Acme", Host: "desk.example.com", Relay: "desk.example.com",
			API: "https://desk.example.com:21114", Key: "PUBKEY", DisableSettings: true, DisableSettingsSet: true}, testSeed)
		must(t, err)
		fail := false
		runner.handle = func(c Command) (Result, error) {
			if c.Name == "tar" && c.Args[0] == "-tzf" {
				return Result{Stdout: "betterdesk-support/\nbetterdesk-support/betterdesk\n"}, nil
			}
			if c.Name != "/opt/provider" || len(c.Args) != 1 || c.Dir != s.SourceDir() {
				return Result{}, errors.New("unexpected command " + c.Name)
			}
			envOf := map[string]string{}
			for _, kv := range c.Env {
				k, v, _ := strings.Cut(kv, "=")
				envOf[k] = v
			}
			if envOf["BETTERDESK_CLIENT_BUILD_REQUEST"] != c.Args[0] || envOf["BETTERDESK_CLIENT_SOURCE_DIR"] != s.SourceDir() {
				t.Errorf("provider environment: %v", envOf)
			}
			raw, err := os.ReadFile(c.Args[0])
			must(t, err)
			request, err := parseJSON(string(raw))
			must(t, err)
			if prop(request, "signed_config_b64") != wantTxt.Content || prop(request, "signing_public_key") != wantKey ||
				prop(request, "clientCommit") != commit || prop(request, "generationId") != bundle.BrandingHash[:48] {
				t.Errorf("request: %s", raw)
			}
			if fail {
				return Result{Code: 1, Stderr: "compile error"}, nil
			}
			_, _ = c.Stdout.Write([]byte("BETTERDESK_BUILD_PROGRESS|40|compiling\n"))
			out := envOf["BETTERDESK_CLIENT_OUTPUT_DIR"]
			var assets []any
			for _, a := range []struct{ name, platform, content string }{{"s-lin.tar.gz", "linux", "linux bytes"}, {"s-win.zip", "windows", ""}} {
				content := []byte(a.content)
				if a.platform == "windows" {
					var buf bytes.Buffer
					zw := zip.NewWriter(&buf)
					f, _ := zw.Create("betterdesk-support/betterdesk.exe")
					_, _ = f.Write([]byte("exe"))
					must(t, zw.Close())
					content = buf.Bytes()
				}
				must(t, os.WriteFile(filepath.Join(out, a.name), content, 0o644))
				sum := sha256.Sum256(content)
				assets = append(assets, goapi.NewObject("name", a.name, "sha256", hex.EncodeToString(sum[:]), "size", float64(len(content)),
					"platform", a.platform, "arch", "x86_64", "format", "portable"))
			}
			manifest := goapi.NewObject("schema_version", float64(1), "sku", ProductSupport, "external_config", false, "client_commit", commit, "assets", assets)
			return Result{}, os.WriteFile(filepath.Join(out, "manifest.json"), []byte(stringify(manifest)), 0o644)
		}
		startWorker(s)
		s.Tick(ctx)
		builds := buildsByKey(t, s, bundle.BrandingHash)
		for _, key := range []string{"linux/x64/portable", "windows/x64/portable"} {
			b := builds[key]
			if b == nil || b.Status != "ready" || !s.IsArtifactPathSafe(b.ArtifactPath) {
				t.Fatalf("%s: %s", key, mustRaw(t, b))
			}
		}
		row, err := s.GetBundle(ctx, bundle.BundleID)
		must(t, err)
		if row.ActiveGenerationHash.String != bundle.BrandingHash {
			t.Errorf("generation not promoted: %q", row.ActiveGenerationHash.String)
		}
		pub, _ := os.ReadFile(filepath.Join(s.SourceDir(), "res", "betterdesk", "custom-client-signing.pub"))
		if strings.TrimSpace(string(pub)) != wantKey {
			t.Errorf("the source has public key %q", pub)
		}

		fail = true
		must(t, s.EnqueueBuildsForHash(ctx, bundle.BrandingHash, true, nil, false))
		s.worker.lastFinished = s.opts.Now().Add(-s.opts.Cooldown * 2)
		s.Tick(ctx)
		failed := 0
		for _, b := range buildsByKey(t, s, bundle.BrandingHash) {
			if b.Status == "failed" && strings.Contains(string(mustRaw(t, b)), `"error_message":"betterdesk_client_build_failed:1:compile error"`) {
				failed++
			}
		}
		if failed != len(Platforms) {
			t.Errorf("%d rows failed, the remote provider's error belongs to all %d", failed, len(Platforms))
		}
	})
}

// fakeGitHub answers the requests of a test and records them.
type fakeGitHub struct {
	mu       sync.Mutex
	requests []*http.Request
	answer   func(r *http.Request) *http.Response
}

func (f *fakeGitHub) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	f.requests = append(f.requests, r)
	f.mu.Unlock()
	resp := f.answer(r)
	if resp == nil {
		return nil, errors.New("no answer for " + r.URL.String())
	}
	resp.Request = r
	return resp, nil
}

func reply(status int, body []byte, headers ...string) *http.Response {
	h := http.Header{}
	for i := 0; i+1 < len(headers); i += 2 {
		h.Set(headers[i], headers[i+1])
	}
	return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(bytes.NewReader(body))}
}

// templateArchive is a tar.gz of a complete template tree.
func templateArchive(t *testing.T) []byte {
	t.Helper()
	src := filepath.Join(t.TempDir(), "generator-templates")
	templateTree(t, src)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	base := filepath.Dir(src)
	must(t, filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(base, p)
		hdr, _ := tar.FileInfoHeader(info, "")
		hdr.Name = filepath.ToSlash(rel)
		if info.IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			data, _ := os.ReadFile(p)
			_, err = tw.Write(data)
		}
		return err
	}))
	must(t, tw.Close())
	must(t, gz.Close())
	return buf.Bytes()
}

// TestInstallFromGitHub installs the templates of the newest release:
// the release from the API, the archive through a redirect to GitHub's
// file host, its SHA-256 from the asset's digest. Every request goes to
// an allowed host over https with the headers and the token of the old
// service and without Accept-Encoding. A redirect to another host, or a
// wrong checksum, is an error recorded in the module's state.
func TestInstallFromGitHub(t *testing.T) {
	archive := templateArchive(t)
	sum := sha256.Sum256(archive)
	release := func(digest string) []byte {
		return []byte(`{"tag_name":"v1.2.3","assets":[{"name":"generator-templates-1.2.3.tar.gz","digest":"` + digest +
			`","browser_download_url":"https://github.com/trybetterdesk/betterdesk-client/releases/download/v1.2.3/generator-templates-1.2.3.tar.gz"}]}`)
	}
	for _, c := range []struct {
		name     string
		digest   string
		location string
		wantErr  string
	}{
		{"installs", "sha256:" + hex.EncodeToString(sum[:]), "https://objects.githubusercontent.com/x/archive", ""},
		{"wrong checksum", "sha256:" + strings.Repeat("0", 64), "https://objects.githubusercontent.com/x/archive", "generator_archive_checksum_mismatch"},
		{"redirect elsewhere", "sha256:" + hex.EncodeToString(sum[:]), "https://evil.example/archive", "github_fetch_host_not_allowed:evil.example"},
		{"plain http", "sha256:" + hex.EncodeToString(sum[:]), "http://objects.githubusercontent.com/x", "github_fetch_requires_https"},
	} {
		t.Run(c.name, func(t *testing.T) {
			gh := &fakeGitHub{answer: func(r *http.Request) *http.Response {
				switch {
				case r.URL.Host == "api.github.com" && r.URL.Opaque == "//api.github.com/repos/trybetterdesk/betterdesk-client/releases/latest":
					return reply(200, release(c.digest))
				case r.URL.Host == "github.com":
					return reply(302, nil, "Location", c.location)
				case r.URL.Host == "objects.githubusercontent.com":
					return reply(200, archive)
				}
				return nil
			}}
			env := []string{"BETTERDESK_GITHUB_TOKEN=made-up-token", "PATH=" + os.Getenv("PATH")}
			s := New(Options{DataDir: t.TempDir(), HTTP: gh, Env: func() []string { return env }})
			must(t, os.WriteFile(filepath.Join(s.opts.DataDir, signingSeedName), []byte(testSeed), 0o600))
			if _, err := s.AcceptModuleTerms(); err != nil {
				t.Fatal(err)
			}
			state, err := s.InstallFromGitHub(context.Background(), "", "")
			current, _ := s.ModuleState()
			if c.wantErr != "" {
				if err == nil || err.Error() != c.wantErr || current.Get("status") != "error" || current.Get("error") != c.wantErr {
					t.Fatalf("got %v, state %s", err, stringify(current))
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if state.Get("status") != "ready" || state.Get("installedVersion") != "1.2.3" || state.Get("templateCount") != float64(6) || !s.TemplatesExist() {
					t.Fatalf("state %s", stringify(state))
				}
			}
			for _, r := range gh.requests {
				// the token goes to the API only, not to the file hosts (BUGS GN10)
				token := ""
				if r.URL.Host == "api.github.com" {
					token = "Bearer made-up-token"
				}
				if r.URL.Scheme != "https" || r.Header.Get("User-Agent") != "BetterDesk-Console-Generator" || r.Header.Get("X-GitHub-Api-Version") != "2022-11-28" ||
					r.Header.Get("Authorization") != token || r.Header.Get("Accept-Encoding") != "" {
					t.Errorf("request %s: %v", r.URL, r.Header)
				}
				if !githubFetchHosts[r.URL.Host] {
					t.Errorf("request to %s", r.URL.Host)
				}
			}
			if entries, _ := os.ReadDir(s.ModuleDir()); len(entries) > 0 {
				for _, e := range entries {
					if strings.HasPrefix(e.Name(), ".tmp-install") {
						t.Errorf("left %s", e.Name())
					}
				}
			}
		})
	}
}

// TestInstallNeedsTerms refuses to download before the terms are
// accepted, without a request.
func TestInstallNeedsTerms(t *testing.T) {
	gh := &fakeGitHub{answer: func(*http.Request) *http.Response { return nil }}
	s := New(Options{DataDir: t.TempDir(), HTTP: gh, Env: func() []string { return nil }})
	_, err := s.InstallFromGitHub(context.Background(), "", "")
	if ErrorCode(err) != "terms_not_accepted" || len(gh.requests) != 0 {
		t.Fatalf("got %v after %d requests", err, len(gh.requests))
	}
}

// TestSyncSource clones the ref's commit with git and keeps the old
// source when a step fails.
func TestSyncSource(t *testing.T) {
	commit := strings.Repeat("c", 40)
	gh := &fakeGitHub{answer: func(r *http.Request) *http.Response {
		if r.URL.Opaque == "//api.github.com/repos/trybetterdesk/betterdesk-client/commits/master" {
			return reply(200, []byte(`{"sha":"`+commit+`"}`))
		}
		return nil
	}}
	failSubmodules := false
	runner := &fakeRunner{handle: func(c Command) (Result, error) {
		if c.Name != "git" {
			return Result{}, errors.New("unexpected " + c.Name)
		}
		switch c.Args[0] {
		case "clone":
			dest := c.Args[len(c.Args)-1]
			must(t, os.MkdirAll(filepath.Join(dest, "libs", "hbb_common"), 0o755))
			must(t, os.WriteFile(filepath.Join(dest, "Cargo.toml"), nil, 0o644))
			must(t, os.WriteFile(filepath.Join(dest, "libs", "hbb_common", "Cargo.toml"), nil, 0o644))
		case "-C":
			if c.Args[2] == "submodule" && failSubmodules {
				return Result{Code: 1, Stderr: "network down"}, nil
			}
		}
		return Result{}, nil
	}}
	s := New(Options{DataDir: t.TempDir(), HTTP: gh, Run: runner, Env: func() []string { return nil }})
	result, err := s.SyncSource(context.Background(), nil, nil, false)
	if err != nil || !result.Changed || result.Remote.SHA != commit || !exists(filepath.Join(s.SourceDir(), "Cargo.toml")) {
		t.Fatalf("sync: %+v %v", result, err)
	}
	want := [][]string{
		{"clone", "--filter=blob:none", "--no-tags", "--no-checkout", "https://github.com/trybetterdesk/betterdesk-client.git"},
		{"-C", "", "checkout", "--detach", commit},
		{"-C", "", "submodule", "update", "--init", "--recursive"},
	}
	if len(runner.commands) != len(want) {
		t.Fatalf("%d git commands", len(runner.commands))
	}
	for i, c := range runner.commands {
		for j, arg := range want[i] {
			if arg != "" && c.Args[j] != arg {
				t.Errorf("command %d: %v", i, c.Args)
			}
		}
	}
	state, _ := s.BuilderState()
	if prop(state, "installedCommit") != commit || prop(state, "status") != "ready" {
		t.Fatalf("state %s", stringify(state))
	}

	if result, err := s.SyncSource(context.Background(), nil, nil, false); err != nil || result.Changed {
		t.Fatalf("second sync: %+v %v", result, err)
	}
	failSubmodules = true
	if _, err := s.SyncSource(context.Background(), nil, nil, true); err == nil || !strings.HasPrefix(err.Error(), "betterdesk_client_submodule_failed:network down") {
		t.Fatalf("failed sync: %v", err)
	}
	state, _ = s.BuilderState()
	if prop(state, "status") != "error" || !exists(filepath.Join(s.SourceDir(), "Cargo.toml")) {
		t.Fatalf("after a failed sync: %s", stringify(state))
	}
}

// TestMissingTool words a tool that is not installed as Node did.
func TestMissingTool(t *testing.T) {
	_, err := ExecRunner{}.Run(context.Background(), Command{Name: "betterdesk-no-such-tool"})
	if err == nil || err.Error() != "spawn betterdesk-no-such-tool ENOENT" || ErrorCode(err) != "ENOENT" {
		t.Fatalf("got %v", err)
	}
}

// TestArtifactPathSafe accepts only paths strictly below the folder.
func TestArtifactPathSafe(t *testing.T) {
	s := New(Options{DataDir: t.TempDir(), Env: func() []string { return nil }})
	root := s.ArtifactDir()
	for path, want := range map[string]bool{
		filepath.Join(root, "a.zip"): true, filepath.Join(root, "x", "a.zip"): true, root: false, "": false,
		filepath.Join(root, "..", "a.zip"): false, "/etc/passwd": false, root + "x/a.zip": false,
	} {
		if got := s.IsArtifactPathSafe(path); got != want {
			t.Errorf("%q: %v", path, got)
		}
	}
}

// TestTwoWorkersClaimARowOnce lets only one of two workers on one
// database take a queued build that both read as queued; the other gets
// the next one (BUGS GN5: both took it and built it).
func TestTwoWorkersClaimARowOnce(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		dataDir := t.TempDir()
		a := New(Options{DB: db, ServerInfo: fakeServerInfo{}, DataDir: dataDir, Env: func() []string { return nil }})
		b := New(Options{DB: db, ServerInfo: fakeServerInfo{}, DataDir: dataDir, Env: func() []string { return nil }})
		readyModule(t, a)
		bundle := newBundle(t, a, goapi.NewObject("server_host", "h.example"), "")
		must(t, a.EnqueueBuildsForHash(ctx, bundle.BrandingHash, false, nil, false))
		seenByA, err := a.listPendingBuilds(ctx, 50)
		must(t, err)
		seenByB, err := b.listPendingBuilds(ctx, 50)
		must(t, err)
		if len(seenByA) < 2 || seenByA[0].Key() != seenByB[0].Key() {
			t.Fatalf("pending builds: %d", len(seenByA))
		}
		took, err := b.claimBuild(ctx, seenByB[0])
		must(t, err)
		if !took {
			t.Fatal("the first worker did not take the build")
		}
		took, err = a.claimBuild(ctx, seenByA[0])
		must(t, err)
		if took {
			t.Fatal("the second worker took the build the first one builds")
		}
		if row := buildsByKey(t, a, bundle.BrandingHash)[seenByA[0].Key()]; row.Status != "building" {
			t.Fatalf("claimed row: %s", mustRaw(t, row))
		}
		next, err := a.claimNextBuild(ctx)
		must(t, err)
		if next == nil || next.build.Key() == seenByA[0].Key() {
			t.Fatalf("the second worker's next build: %+v", next)
		}
	})
}

// TestQueuedBuildsInTimeOrder takes the oldest queued build first by its
// time, also where the name of its weekday sorts after the newer one's
// (BUGS GN6: on PostgreSQL a Friday build went before an older Monday
// build).
func TestQueuedBuildsInTimeOrder(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s := New(Options{DB: db, ServerInfo: fakeServerInfo{}, DataDir: t.TempDir(), Env: func() []string { return nil }, GOOS: "linux", GOARCH: "amd64"})
		readyModule(t, s)
		bundle := newBundle(t, s, goapi.NewObject("server_host", "h.example"), "")
		must(t, s.EnqueueBuildsForHash(ctx, bundle.BrandingHash, false, nil, false))
		for key, b := range buildsByKey(t, s, bundle.BrandingHash) {
			if key != "windows/x64/portable" && key != "windows/arm64/portable" {
				must(t, s.UpsertBuild(ctx, BuildUpdate{BrandingHash: b.BrandingHash, Platform: b.Platform, Arch: b.Arch, Format: b.Format, Status: "ready", ArtifactSize: int64(1)}))
			}
		}
		for arch, created := range map[string]string{"arm64": "2026-11-09 08:00:00", "x64": "2026-11-13 08:00:00"} {
			if _, err := db.Exec(ctx, "UPDATE agent_bundle_builds SET created_at = ? WHERE platform = 'windows' AND arch = ?", created, arch); err != nil {
				t.Fatal(err)
			}
		}
		next, err := s.claimNextBuild(ctx)
		must(t, err)
		if next == nil || next.build.Key() != "windows/arm64/portable" {
			t.Fatalf("first build: %+v, want the Monday one (windows/arm64)", next)
		}
	})
}

// TestEnqueueKeepsFinishedBuilds leaves ready and building rows alone
// unless the queue is forced.
func TestEnqueueKeepsFinishedBuilds(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s := New(Options{DB: db, ServerInfo: fakeServerInfo{}, DataDir: t.TempDir(), Env: func() []string { return nil }})
		readyModule(t, s)
		bundle := newBundle(t, s, goapi.NewObject("server_host", "h.example"), "")
		must(t, s.EnqueueBuildsForHash(ctx, bundle.BrandingHash, false, nil, false))
		must(t, s.UpsertBuild(ctx, BuildUpdate{BrandingHash: bundle.BrandingHash, Platform: "linux", Arch: "x64", Format: "portable", Status: "ready", ArtifactPath: "/x", ArtifactSize: int64(5)}))
		must(t, s.EnqueueBuildsForHash(ctx, bundle.BrandingHash, false, nil, false))
		if b := buildsByKey(t, s, bundle.BrandingHash)["linux/x64/portable"]; b.Status != "ready" {
			t.Fatalf("a ready build was queued again: %s", b.Status)
		}
		must(t, s.EnqueueBuildsForHash(ctx, bundle.BrandingHash, true, nil, false))
		if b := buildsByKey(t, s, bundle.BrandingHash)["linux/x64/portable"]; b.Status != "queued" || b.ArtifactPath != "/x" {
			t.Fatalf("forced: %s", mustRaw(t, b))
		}
	})
}

// TestStoreRules: the list leaves revoked bundles out unless asked, and a
// requeue without an artifact keeps the stored path but not the size.
func TestStoreRules(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s := New(Options{DB: db, ServerInfo: fakeServerInfo{}, DataDir: t.TempDir(), Env: func() []string { return nil }})
		readyModule(t, s)
		live := newBundle(t, s, goapi.NewObject("server_host", "a.example"), "")
		gone := newBundle(t, s, goapi.NewObject("server_host", "b.example"), "")
		if _, err := s.SetRevoked(ctx, gone.BundleID, true); err != nil {
			t.Fatal(err)
		}
		for include, want := range map[bool]int{false: 1, true: 2} {
			list, err := s.ListBundles(ctx, include)
			must(t, err)
			if len(list) != want || list[len(list)-1].BundleID != live.BundleID && !include {
				t.Errorf("includeRevoked %v: %d bundles", include, len(list))
			}
		}
		must(t, s.UpsertBuild(ctx, BuildUpdate{BrandingHash: live.BrandingHash, Platform: "linux", Arch: "x64", Format: "portable", Status: "ready", ArtifactPath: "/x", ArtifactSize: int64(5), ArtifactSHA256: "s"}))
		refused, err := s.RequeuePlatformBuild(ctx, live.BrandingHash, "linux", "x64", "portable")
		if err != nil || refused != "" {
			t.Fatal(refused, err)
		}
		b := buildsByKey(t, s, live.BrandingHash)["linux/x64/portable"]
		if b.Status != "queued" || b.ArtifactPath != "/x" || b.Size() != 0 || b.ArtifactSHA256 != "s" {
			t.Fatalf("requeued row: %s", mustRaw(t, b))
		}
	})
}

// TestBuilderToken sends the token to api.github.com only: an artifact
// download from another host goes without it.
func TestBuilderToken(t *testing.T) {
	var auth []string
	gh := &fakeGitHub{answer: func(r *http.Request) *http.Response {
		auth = append(auth, r.URL.Host+" "+r.Header.Get("Authorization"))
		if r.URL.Host == "api.github.com" {
			return reply(302, nil, "Location", "https://pipelines.example/zip")
		}
		return reply(200, []byte("zip"))
	}}
	s := New(Options{DataDir: t.TempDir(), HTTP: gh, Lookup: testLookup, Env: func() []string { return []string{"GITHUB_TOKEN=made-up-token"} }})
	must(t, s.downloadToFile(context.Background(), "https://api.github.com/repos/x/y/actions/artifacts/1/zip", filepath.Join(t.TempDir(), "a.zip")))
	if len(auth) != 2 || auth[0] != "api.github.com Bearer made-up-token" || auth[1] != "pipelines.example " {
		t.Fatalf("authorization per host: %q", auth)
	}
}

// testLookup resolves the made-up hosts of the download tests: two public
// ones, and one with a private address among its public ones.
func testLookup(_ context.Context, host string) ([]string, error) {
	switch host {
	case "api.github.com":
		return []string{"140.82.121.6"}, nil
	case "pipelines.example":
		return []string{"20.60.40.4", "2603:1030:403::4"}, nil
	case "inside.example":
		return []string{"20.60.40.5", "10.1.2.3"}, nil
	}
	return nil, &ssrfguard.LookupError{Code: "ENOTFOUND", Host: host}
}

// TestArtifactRedirectStaysPublic follows an artifact download only to
// https addresses outside loopback, link-local and the private ranges
// (BUGS GN10). A refused address is never requested, and every request
// carries the addresses the guard checked, which are the only ones the
// connection may go to.
func TestArtifactRedirectStaysPublic(t *testing.T) {
	for _, c := range []struct {
		location string
		want     string
	}{
		{"https://pipelines.example/zip", ""},
		{"http://pipelines.example/zip", "github_download_requires_https"},
		{"ftp://pipelines.example/zip", "github_download_requires_https"},
		{"https://127.0.0.1/zip", "Blocked IP address: 127.0.0.1"},
		{"https://[::1]:8443/zip", "Blocked IP address: ::1"},
		{"https://169.254.169.254/latest/meta-data", "Blocked IP address: 169.254.169.254"},
		{"https://192.168.1.10/zip", "Blocked IP address: 192.168.1.10"},
		{"https://localhost/zip", "Blocked hostname: localhost"},
		{"https://inside.example/zip", "Hostname inside.example resolves to blocked address 10.1.2.3"},
		{"https://nowhere.example/zip", "DNS lookup failed for nowhere.example: getaddrinfo ENOTFOUND nowhere.example"},
	} {
		t.Run(c.location, func(t *testing.T) {
			var hosts []string
			gh := &fakeGitHub{answer: func(r *http.Request) *http.Response {
				checked, _ := r.Context().Value(checkedHost{}).(ssrfguard.Host)
				hosts = append(hosts, r.URL.Host+" "+strings.Join(checked.Addresses, ","))
				if r.URL.Host == "api.github.com" {
					return reply(302, nil, "Location", c.location)
				}
				return reply(200, []byte("zip"))
			}}
			s := New(Options{DataDir: t.TempDir(), HTTP: gh, Lookup: testLookup, Env: func() []string { return nil }})
			err := s.downloadToFile(context.Background(), "https://api.github.com/repos/x/y/actions/artifacts/1/zip", filepath.Join(t.TempDir(), "a.zip"))
			want := []string{"api.github.com 140.82.121.6"}
			if c.want == "" {
				want = append(want, "pipelines.example 20.60.40.4,2603:1030:403::4")
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.Error() != c.want {
				t.Fatalf("error %v, want %s", err, c.want)
			}
			if strings.Join(hosts, "|") != strings.Join(want, "|") {
				t.Fatalf("requests %q, want %q", hosts, want)
			}
		})
	}
}

// TestDownloadDialsCheckedAddresses connects a request that carries the
// addresses the guard checked to those addresses only, in their order;
// any other request dials its host as given.
func TestDownloadDialsCheckedAddresses(t *testing.T) {
	var dialed []string
	dial := dialChecked(func(_ context.Context, _, address string) (net.Conn, error) {
		dialed = append(dialed, address)
		return nil, errors.New("refused")
	})
	checked := ssrfguard.Host{Name: "pipelines.example", Addresses: []string{"20.60.40.4", "2603:1030:403::4"}}
	ctx := context.WithValue(context.Background(), checkedHost{}, checked)
	if _, err := dial(ctx, "tcp", "pipelines.example:443"); err == nil {
		t.Fatal("dial did not fail")
	}
	if _, err := dial(ctx, "tcp", "proxy.example:3128"); err == nil {
		t.Fatal("dial did not fail")
	}
	if _, err := dial(context.Background(), "tcp", "api.github.com:443"); err == nil {
		t.Fatal("dial did not fail")
	}
	want := "20.60.40.4:443 [2603:1030:403::4]:443 proxy.example:3128 api.github.com:443"
	if got := strings.Join(dialed, " "); got != want {
		t.Fatalf("dialed %q, want %q", got, want)
	}

	// The service's own transport dials that way: a name that resolves
	// nowhere reaches the address it was checked at.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	defer ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	ctx = context.WithValue(context.Background(), checkedHost{}, ssrfguard.Host{Name: "artifact.invalid", Addresses: []string{"127.0.0.1"}})
	conn, err := defaultTransport().(*http.Transport).DialContext(ctx, "tcp", "artifact.invalid:"+port)
	must(t, err)
	conn.Close()
}
