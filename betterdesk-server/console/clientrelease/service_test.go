package clientrelease

import (
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
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/unitronix/betterdesk-server/console/updater"
)

// fakeGitHub serves one release of the client fork.
type fakeGitHub struct {
	srv   *httptest.Server
	mu    sync.Mutex
	files map[string][]byte
	hits  map[string]int
	// redirect sends asset downloads to another address.
	redirect string
}

func newFakeGitHub(t *testing.T, files map[string][]byte) *fakeGitHub {
	t.Helper()
	f := &fakeGitHub{files: files, hits: map[string]int{}}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.hits[r.URL.Path]++
		f.mu.Unlock()
		if r.URL.Path == "/repos/trybetterdesk/betterdesk-client/releases/latest" {
			assets := []map[string]any{}
			for name, data := range f.files {
				assets = append(assets, map[string]any{"name": name, "size": len(data), "browser_download_url": f.srv.URL + "/download/" + name})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "1.4.6-dev.3", "assets": assets})
			return
		}
		if name, ok := strings.CutPrefix(r.URL.Path, "/download/"); ok {
			if f.redirect != "" {
				http.Redirect(w, r, f.redirect, http.StatusFound)
				return
			}
			if data, ok := f.files[name]; ok {
				_, _ = w.Write(data)
				return
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitHub) hitsOf(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[path]
}

func sumsOf(files map[string][]byte) []byte {
	var b strings.Builder
	for name, data := range files {
		sum := sha256.Sum256(data)
		b.WriteString(hex.EncodeToString(sum[:]) + "  " + name + "\n")
	}
	return []byte(b.String())
}

func sign(key ed25519.PrivateKey, data []byte) []byte {
	return []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(key, data)) + "\n")
}

func newKey(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return pub, priv
}

// packages are a release's package files.
func packages() map[string][]byte {
	return map[string][]byte{
		"betterdesk-1.4.6-dev.3-windows-x86_64.exe":             []byte("windows exe"),
		"betterdesk-1.4.6-dev.3-linux-x86_64.deb":               []byte("debian package"),
		"betterdesk-1.4.6-dev.3-linux-x86_64-portable.tar.gz":   []byte("portable tar"),
		"betterdesk-1.4.6-dev.3-macos-aarch64.dmg":              []byte("disk image"),
		"betterdesk-1.4.6-dev.3-windows-x86_64.msi":             []byte("not handed out"),
		"betterdesk-1.4.6-dev.3-linux-x86_64-suse.rpm":          []byte("not handed out either"),
		"generator-templates-1.4.6-dev.3.tar.gz":                []byte("templates"),
		"betterdesk-1.4.6-dev.3-macos-aarch64-portable.zip":     []byte("app zip"),
		"betterdesk-1.4.6-dev.3-linux-aarch64.rpm":              []byte("arm rpm"),
		"betterdesk-1.4.6-dev.3-windows-aarch64.tar.gz":         []byte("build folder"),
		"betterdesk-1.4.6-dev.3-linux-x86_64-portable.tar.gz.x": []byte("junk"),
	}
}

// signedRelease builds a signed release of the files.
func signedRelease(key ed25519.PrivateKey, files map[string][]byte) map[string][]byte {
	out := map[string][]byte{}
	for k, v := range files {
		out[k] = v
	}
	sums := sumsOf(files)
	out[SumsFile] = sums
	out[SignatureFile] = sign(key, sums)
	return out
}

func newService(t *testing.T, gh *fakeGitHub, pub ed25519.PublicKey, env map[string]string) *Service {
	t.Helper()
	u, _ := url.Parse(gh.srv.URL)
	if env == nil {
		env = map[string]string{}
	}
	env[EnvPublicKey] = base64.StdEncoding.EncodeToString(pub)
	return New(Options{
		DataDir:      t.TempDir(),
		Getenv:       func(k string) string { return env[k] },
		HTTP:         gh.srv.Client(),
		APIBase:      gh.srv.URL,
		AllowedHosts: map[string]bool{u.Hostname(): true},
	})
}

func readAll(t *testing.T, f *os.File) string {
	t.Helper()
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSignedReleaseIsOfferedAndEachPackageDownloadedOnce(t *testing.T) {
	pub, priv := newKey(t)
	gh := newFakeGitHub(t, signedRelease(priv, packages()))
	s := newService(t, gh, pub, nil)
	ctx := context.Background()

	join, err := s.Offers(ctx, KindJoin)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, o := range join {
		got = append(got, o.OS+"/"+o.Arch+"/"+o.Format+"@"+o.Version)
	}
	want := "windows/x64/exe@1.4.6-dev.3 macos/arm64/dmg@1.4.6-dev.3 linux/x64/deb@1.4.6-dev.3 linux/arm64/rpm@1.4.6-dev.3"
	if strings.Join(got, " ") != want {
		t.Fatalf("join offers %v, want %s", got, want)
	}
	support, _ := s.Offers(ctx, KindSupport)
	if len(support) != 3 || support[2].Format != "tar.gz" {
		t.Fatalf("support offers %+v", support)
	}

	for i := 0; i < 3; i++ {
		f, offer, err := s.Open(ctx, KindJoin, "windows", "x64", "exe")
		if err != nil {
			t.Fatal(err)
		}
		if text := readAll(t, f); text != "windows exe" || offer.Version != "1.4.6-dev.3" {
			t.Fatalf("package %q %+v", text, offer)
		}
	}
	if n := gh.hitsOf("/download/betterdesk-1.4.6-dev.3-windows-x86_64.exe"); n != 1 {
		t.Fatalf("the package was downloaded %d times", n)
	}
	if _, _, err := s.Open(ctx, KindJoin, "linux", "x64", "tar.gz"); !errors.Is(err, ErrNoPackage) {
		t.Fatalf("a support package for a join code: %v", err)
	}
	if st := s.Status(ctx); !st.Verified || st.Tag != "1.4.6-dev.3" || st.Source != "github" || st.Error != "" {
		t.Fatalf("status %+v", st)
	}
}

func TestReleaseSignedByAnotherKeyIsNotUsed(t *testing.T) {
	pub, _ := newKey(t)
	_, other := newKey(t)
	gh := newFakeGitHub(t, signedRelease(other, packages()))
	s := newService(t, gh, pub, nil)
	if _, err := s.Offers(context.Background(), KindJoin); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("offers of a release signed by another key: %v", err)
	}
	if st := s.Status(context.Background()); !strings.Contains(st.Error, "signature") {
		t.Fatalf("status %+v", st)
	}
	if n := gh.hitsOf("/download/betterdesk-1.4.6-dev.3-windows-x86_64.exe"); n != 0 {
		t.Fatal("a package of an unsigned release was downloaded")
	}
}

func TestReleaseWithoutSignatureIsNotUsed(t *testing.T) {
	pub, priv := newKey(t)
	files := signedRelease(priv, packages())
	delete(files, SignatureFile)
	gh := newFakeGitHub(t, files)
	s := newService(t, gh, pub, nil)
	if _, err := s.Offers(context.Background(), KindJoin); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("offers without a signature: %v", err)
	}
}

func TestPackageThatDoesNotMatchItsChecksumIsRefused(t *testing.T) {
	pub, priv := newKey(t)
	files := signedRelease(priv, packages())
	files["betterdesk-1.4.6-dev.3-windows-x86_64.exe"] = []byte("tampered exe")
	gh := newFakeGitHub(t, files)
	s := newService(t, gh, pub, nil)
	if _, _, err := s.Open(context.Background(), KindJoin, "windows", "x64", "exe"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a tampered package: %v", err)
	}
	matches, _ := filepath.Glob(filepath.Join(s.cacheDir(), "*", "*.exe"))
	if len(matches) != 0 {
		t.Fatalf("the tampered package was kept: %v", matches)
	}
}

func TestDownloadsStayOnAllowedHosts(t *testing.T) {
	pub, priv := newKey(t)
	gh := newFakeGitHub(t, signedRelease(priv, packages()))
	gh.redirect = "https://evil.example/x.exe"
	s := newService(t, gh, pub, nil)
	if _, _, err := s.Open(context.Background(), KindJoin, "windows", "x64", "exe"); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("a redirect to another host: %v", err)
	}
}

func TestStoredReleaseServesWithoutGitHub(t *testing.T) {
	pub, priv := newKey(t)
	gh := newFakeGitHub(t, signedRelease(priv, packages()))
	s := newService(t, gh, pub, nil)
	ctx := context.Background()
	f, _, err := s.Open(ctx, KindJoin, "windows", "x64", "exe")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	// A restart while GitHub is gone
	again := New(Options{DataDir: s.opts.DataDir, Getenv: s.opts.Getenv, HTTP: gh.srv.Client(), APIBase: "https://127.0.0.1:1", AllowedHosts: s.opts.AllowedHosts})
	f, offer, err := again.Open(ctx, KindJoin, "windows", "x64", "exe")
	if err != nil {
		t.Fatalf("after a restart: %v", err)
	}
	if text := readAll(t, f); text != "windows exe" || offer.Version != "1.4.6-dev.3" {
		t.Fatalf("package %q", text)
	}
}

func TestFolderSource(t *testing.T) {
	ctx := context.Background()
	write := func(dir string, files map[string][]byte) {
		for name, data := range files {
			if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	open := func(dir string, pub ed25519.PublicKey) *Service {
		env := map[string]string{EnvLocalDir: dir, EnvPublicKey: base64.StdEncoding.EncodeToString(pub)}
		return New(Options{DataDir: t.TempDir(), Getenv: func(k string) string { return env[k] }})
	}
	pub, priv := newKey(t)

	t.Run("plain files", func(t *testing.T) {
		dir := t.TempDir()
		write(dir, packages())
		f, offer, err := open(dir, pub).Open(ctx, KindSupport, "linux", "x64", "tar.gz")
		if err != nil {
			t.Fatal(err)
		}
		if text := readAll(t, f); text != "portable tar" || offer.Version != "1.4.6-dev.3" {
			t.Fatalf("package %q", text)
		}
	})
	t.Run("checksums are checked", func(t *testing.T) {
		dir := t.TempDir()
		write(dir, signedRelease(priv, packages()))
		s := open(dir, pub)
		if st := s.Status(ctx); st.Source != "folder" || !st.Verified {
			t.Fatalf("status %+v", st)
		}
		write(dir, map[string][]byte{"betterdesk-1.4.6-dev.3-linux-x86_64.deb": []byte("changed")})
		if _, _, err := s.Open(ctx, KindJoin, "linux", "x64", "deb"); err == nil {
			t.Fatal("a changed file was served")
		}
	})
	t.Run("a bad signature", func(t *testing.T) {
		dir := t.TempDir()
		_, other := newKey(t)
		write(dir, signedRelease(other, packages()))
		if _, err := open(dir, pub).Offers(ctx, KindJoin); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("offers: %v", err)
		}
	})
}

// The client packages trust the keys the server's own updates trust, so
// the release key is named in one place.
func TestCompiledKeysAreTheServersReleaseKeys(t *testing.T) {
	keys, want := Keys(""), updater.CompiledKeys()
	if len(keys) == 0 || len(keys) != len(want) {
		t.Fatalf("%d compiled keys, the updater has %d", len(keys), len(want))
	}
	for i := range keys {
		if !keys[i].Equal(want[i]) {
			t.Fatalf("key %d is not the updater's", i)
		}
	}
	if got := base64.StdEncoding.EncodeToString(keys[0]); got != "iwissiXvSjeFFsG8SdKEZq11uAtRLWCrD7Ky1vfY2n4=" {
		t.Fatalf("compiled key %s", got)
	}
}
