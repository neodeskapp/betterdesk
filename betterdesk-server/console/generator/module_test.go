package generator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/goapi"
)

// TestValidateTemplates refuses each way a template tree can be broken,
// with the code the old module threw.
func TestValidateTemplates(t *testing.T) {
	for _, c := range []struct {
		name   string
		break_ func(root string)
		want   string
	}{
		{"complete", func(string) {}, ""},
		{"no manifest", func(root string) { os.Remove(filepath.Join(root, "manifest.json")) }, "generator_manifest_invalid"},
		{"wrong schema", func(root string) { rewrite(root, `"schema_version":2`, `"schema_version":3`) }, "generator_manifest_invalid"},
		{"a platform less", func(root string) { rewrite(root, `"macos-aarch64"]`, `"macos-aarch64x"]`) }, "generator_manifest_platforms_invalid"},
		{"a symbolic link", func(root string) { os.Symlink("/etc", filepath.Join(root, "linux-x86_64", "etc")) }, "generator_archive_symlink"},
		{"path with ..", func(root string) {
			rewrite(root, `"template_path":"linux-x86_64"`, `"template_path":"../linux-x86_64"`)
		}, "generator_template_path_invalid:linux-x86_64"},
		{"absolute path", func(root string) {
			rewrite(root, `"template_path":"linux-x86_64"`, `"template_path":"/linux-x86_64"`)
		}, "generator_template_path_invalid:linux-x86_64"},
		{"drive letter", func(root string) {
			rewrite(root, `"template_path":"linux-x86_64"`, `"template_path":"c:linux-x86_64"`)
		}, "generator_template_path_invalid:linux-x86_64"},
		{"no folder", func(root string) { os.RemoveAll(filepath.Join(root, "macos-x86_64")) }, "generator_template_missing:macos-x86_64"},
		{"no marker", func(root string) { os.Remove(filepath.Join(root, "linux-aarch64", ".custom-txt-here")) }, "generator_template_marker_missing:linux-aarch64"},
		{"a custom.txt", func(root string) {
			os.WriteFile(filepath.Join(root, "windows-x86_64", "custom.txt"), []byte("x"), 0o644)
		}, "generator_template_contains_custom_txt:windows-x86_64"},
		{"small binary", func(root string) {
			os.WriteFile(filepath.Join(root, "linux-x86_64", "betterdesk"), make([]byte, minBinarySize-1), 0o755)
		}, "generator_template_binary_missing:linux-x86_64"},
		{"binary outside", func(root string) {
			rewrite(root, `"binary_path":"betterdesk.exe"`, `"binary_path":"a/../../windows-aarch64/betterdesk.exe"`)
		}, "generator_template_binary_path_invalid:windows-x86_64"},
		{"no checksum", func(root string) { rewriteOnce(root, `"sha256":"`+strings.Repeat("a", 64)+`"`, `"sha256":"x"`) }, "generator_template_hash_invalid:windows-x86_64"},
		{"no archive", func(root string) { os.Remove(filepath.Join(root, "macos-aarch64.tar.gz")) }, "generator_template_archive_missing:macos-aarch64"},
		{"twice", func(root string) {
			rewrite(root, `"platform":"windows","arch":"aarch64"`, `"platform":"windows","arch":"x86_64"`)
		}, "generator_template_duplicate:windows-x86_64"},
		{"not portable", func(root string) { rewriteOnce(root, `"format":"portable"`, `"format":"deb"`) }, "generator_manifest_templates_incomplete"},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := New(Options{DataDir: t.TempDir(), Env: func() []string { return nil }})
			root := filepath.Join(t.TempDir(), "templates")
			templateTree(t, root)
			c.break_(root)
			_, count, err := s.ValidateTemplates(root)
			got := ""
			if err != nil {
				got = err.Error()
			}
			if got != c.want || (err == nil && count != 6) {
				t.Fatalf("got %q (%d templates), want %q", got, count, c.want)
			}
		})
	}
}

func rewrite(root, from, to string) {
	p := filepath.Join(root, "manifest.json")
	b, _ := os.ReadFile(p)
	os.WriteFile(p, []byte(strings.ReplaceAll(string(b), from, to)), 0o644)
}

func rewriteOnce(root, from, to string) {
	p := filepath.Join(root, "manifest.json")
	b, _ := os.ReadFile(p)
	os.WriteFile(p, []byte(strings.Replace(string(b), from, to, 1)), 0o644)
}

// TestArchiveTraversal refuses an archive whose listing leaves the folder,
// before anything is unpacked.
func TestArchiveTraversal(t *testing.T) {
	for _, entry := range []string{"../x", "/etc/passwd", "a/../../x", `c:\x`, `a\..\..\x`} {
		runner := &fakeRunner{handle: func(c Command) (Result, error) {
			if c.Args[0] == "-tzf" {
				return Result{Stdout: "generator-templates/\n" + entry + "\n"}, nil
			}
			t.Errorf("unpacked an archive listing %q", entry)
			return Result{}, nil
		}}
		s := New(Options{DataDir: t.TempDir(), Run: runner, Env: func() []string { return nil }})
		if err := s.extractTarGz(context.Background(), filepath.Join(t.TempDir(), "a.tar.gz"), t.TempDir()); err == nil || err.Error() != "generator_archive_path_traversal" {
			t.Errorf("%q: %v", entry, err)
		}
	}
}

// TestSigningSeedCopy takes the seed from the environment or the data
// folder and refuses one that is no base64 of 32 bytes.
func TestSigningSeedCopy(t *testing.T) {
	for _, c := range []struct {
		env, file string
		found     bool
		err       string
	}{
		{"", "", false, ""},
		{" " + testSeed + " ", "", true, ""},
		{"", testSeed + "\n", true, ""},
		{strings.ReplaceAll(testSeed, "B", "-"), "", false, "custom_client_signing_seed_invalid"},
		{testSeed[:40], "", false, "custom_client_signing_seed_invalid"},
		{"", "not base64!", false, "custom_client_signing_seed_invalid"},
	} {
		data := t.TempDir()
		env := []string{}
		if c.env != "" {
			env = append(env, "BETTERDESK_CUSTOM_CLIENT_SIGNING_SEED="+c.env)
		}
		if c.file != "" {
			must(t, os.WriteFile(filepath.Join(data, signingSeedName), []byte(c.file), 0o600))
		}
		s := New(Options{DataDir: data, ConsoleDir: t.TempDir(), WorkDir: t.TempDir(), Env: func() []string { return env }})
		found, err := s.CopySigningSeedIfPresent()
		got := ""
		if err != nil {
			got = err.Error()
		}
		if found != c.found || got != c.err || (found && !s.SigningSeedValid()) {
			t.Errorf("env %q file %q: %v %q", c.env, c.file, found, got)
		}
	}
}

// TestLogoLimit takes a logo of up to 10 MB of text and refuses a larger
// one.
func TestLogoLimit(t *testing.T) {
	head := "data:image/png;base64,"
	for size, want := range map[int]bool{maxLogoBytes: true, maxLogoBytes + 1: false} {
		logo := head + strings.Repeat("A", size-len(head))
		v, err := ValidateBranding(goapiObject("server_host", "h.example", "logo_data_url", logo), nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := v.Valid && prop(v.Normalized, "logo_data_url") == logo; got != want {
			t.Errorf("logo of %d bytes: valid %v, errors %v", size, v.Valid, v.Errors)
		}
	}
}

// TestDefaultTransport sends no Accept-Encoding, as Node's requests did.
func TestDefaultTransport(t *testing.T) {
	got := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got <- r.Header.Get("Accept-Encoding") + "|" + r.Header.Get("Accept")
		w.Write([]byte(`{"sha":"x"}`))
	}))
	defer srv.Close()
	s := New(Options{DataDir: t.TempDir(), Env: func() []string { return nil }})
	if _, err := s.githubJSON(context.Background(), srv.URL+"/x"); err != nil {
		t.Fatal(err)
	}
	if h := <-got; h != "|application/vnd.github+json" {
		t.Fatalf("headers %q", h)
	}
}

func goapiObject(pairs ...any) any { return goapi.NewObject(pairs...) }
