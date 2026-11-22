package generator

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/store"
)

// The vectors of the generator worker (testdata/README.md).

// TestFolderZipMatchesAdmZip packs the fixture the oracle packed with
// adm-zip and compares every entry as a program that unpacks it sees it:
// name, order, attributes, method, DOS time, CRC, size, "made by",
// version, flags and content.
func TestFolderZipMatchesAdmZip(t *testing.T) {
	var z struct {
		Fixture []struct {
			Path    string `json:"path"`
			Dir     string `json:"dir"`
			Content string `json:"content"`
			Mode    uint32 `json:"mode"`
		} `json:"fixture"`
		MTime   time.Time `json:"mtime"`
		Entries []struct {
			Name    string  `json:"name"`
			Attr    uint32  `json:"attr"`
			Method  uint16  `json:"method"`
			Time    uint32  `json:"time"`
			CRC     uint32  `json:"crc"`
			Size    uint64  `json:"size"`
			Made    uint16  `json:"made"`
			Version uint16  `json:"version"`
			Flags   uint16  `json:"flags"`
			Data    *string `json:"data"`
		} `json:"entries"`
	}
	load(t, "zip", &z)
	stage := filepath.Join(t.TempDir(), "betterdesk-support-windows-x64")
	for _, f := range z.Fixture {
		if f.Dir != "" {
			if err := os.MkdirAll(filepath.Join(stage, f.Dir), 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		p := filepath.Join(stage, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f.Content), os.FileMode(f.Mode)); err != nil {
			t.Fatal(err)
		}
		_ = os.Chmod(p, os.FileMode(f.Mode))
		_ = os.Chtimes(p, z.MTime, z.MTime)
	}
	for _, f := range z.Fixture {
		dir := f.Dir
		if f.Path != "" {
			dir = filepath.Dir(f.Path)
		}
		if dir != "." {
			p := filepath.Join(stage, filepath.FromSlash(dir))
			_ = os.Chmod(p, 0o755)
			_ = os.Chtimes(p, z.MTime, z.MTime)
		}
	}
	out := filepath.Join(t.TempDir(), "out.zip")
	if err := writeFolderZip(stage, filepath.Base(stage), out); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	if len(zr.File) != len(z.Entries) {
		t.Fatalf("%d entries, adm-zip wrote %d", len(zr.File), len(z.Entries))
	}
	for i, f := range zr.File {
		want := z.Entries[i]
		dos := uint32(f.ModifiedDate)<<16 | uint32(f.ModifiedTime)
		if f.Name != want.Name || f.ExternalAttrs != want.Attr || f.Method != want.Method || dos != want.Time ||
			f.CRC32 != want.CRC || f.UncompressedSize64 != want.Size || f.CreatorVersion != want.Made ||
			f.ReaderVersion != want.Version || f.Flags != want.Flags {
			t.Errorf("entry %d:\n adm-zip %+v\n      go %s %x %d %x %x %d %x %d %x", i, want, f.Name, f.ExternalAttrs, f.Method, dos, f.CRC32, f.UncompressedSize64, f.CreatorVersion, f.ReaderVersion, f.Flags)
			continue
		}
		if want.Data != nil {
			rc, err := f.Open()
			if err != nil {
				t.Fatal(err)
			}
			data, _ := io.ReadAll(rc)
			rc.Close()
			if base64.StdEncoding.EncodeToString(data) != *want.Data {
				t.Errorf("entry %s: content differs", f.Name)
			}
		}
	}
}

// TestEmbeddedPlatformsMatchNode checks the build capabilities and the
// platform rules of embedded mode for provider settings, against what
// the Node services said with the same settings on the same machine.
func TestEmbeddedPlatformsMatchNode(t *testing.T) {
	var cases []struct {
		Settings struct {
			Command     string  `json:"command"`
			Actions     string  `json:"actions"`
			Token       string  `json:"token"`
			Targets     *string `json:"targets"`
			File        string  `json:"file"`
			FileContent *string `json:"fileContent"`
		} `json:"settings"`
		Capabilities string `json:"capabilities"`
		Platforms    string `json:"platforms"`
		Available    string `json:"available"`
		Selections   []struct {
			Given     bool     `json:"given"`
			Selection string   `json:"selection"`
			Valid     bool     `json:"valid"`
			Error     string   `json:"error"`
			Details   []string `json:"details"`
			Selected  []string `json:"selected"`
		} `json:"selections"`
		FailedFor int `json:"failedFor"`
	}
	load(t, "embedded_platforms", &cases)
	for i, c := range cases {
		scratch := t.TempDir()
		env := []string{"PATH=" + os.Getenv("PATH")}
		add := func(k, v string) {
			if v != "" {
				env = append(env, k+"="+v)
			}
		}
		add("BETTERDESK_CLIENT_BUILD_COMMAND", c.Settings.Command)
		add("BETTERDESK_CLIENT_GITHUB_ACTIONS", c.Settings.Actions)
		add("BETTERDESK_GITHUB_TOKEN", c.Settings.Token)
		if c.Settings.Targets != nil {
			add("BETTERDESK_CLIENT_PROVIDER_TARGETS_JSON", *c.Settings.Targets)
		}
		if c.Settings.File != "" {
			file := strings.Replace(c.Settings.File, "<SCRATCH>", scratch, 1)
			if c.Settings.FileContent != nil {
				if err := os.WriteFile(file, []byte(*c.Settings.FileContent), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			add("BETTERDESK_CLIENT_PROVIDER_TARGETS_FILE", file)
		}
		s := New(Options{DataDir: filepath.Join(scratch, "data"), Embedded: true, Env: func() []string { return env }})
		if got := strings.ReplaceAll(stringify(s.BuildCapabilities()), scratch, "<SCRATCH>"); got != c.Capabilities {
			t.Errorf("case %d capabilities:\n node %s\n   go %s", i, c.Capabilities, got)
		}
		if got := mustJSON(t, s.PlatformCatalog()); got != c.Platforms {
			t.Errorf("case %d catalogue:\n node %s\n   go %s", i, c.Platforms, got)
		}
		if got := mustJSON(t, nonNilPlatforms(s.AvailablePlatforms())); got != c.Available {
			t.Errorf("case %d available:\n node %s\n   go %s", i, c.Available, got)
		}
		for _, sel := range c.Selections {
			v, _ := parseJSON(sel.Selection)
			r := s.ValidatePlatformSelection(v, sel.Given)
			var keys []string
			for _, p := range r.Selected {
				keys = append(keys, p.Key())
			}
			details := r.Details
			if details == nil {
				details = []string{}
			}
			if r.Valid != sel.Valid || r.Error != sel.Error || !sameStrings(details, sel.Details) || !sameStrings(keys, sel.Selected) {
				t.Errorf("case %d selection %s: node %v %q %v %v, go %v %q %v %v", i, sel.Selection, sel.Valid, sel.Error, sel.Details, sel.Selected, r.Valid, r.Error, details, keys)
			}
		}
		row := &Build{Platform: "linux", Arch: "x64", Format: "portable"}
		if got := len(s.failedPlatformsForError("local_builder_x", row)) + 100*len(s.failedPlatformsForError("betterdesk_client_build_failed:1", row)); got != c.FailedFor {
			t.Errorf("case %d failed platforms: node %d, go %d", i, c.FailedFor, got)
		}
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// TestEmbeddedManifestsMatchNode checks a provider's manifest.json the
// way the Node worker did, and the choice of a target's asset.
func TestEmbeddedManifestsMatchNode(t *testing.T) {
	var v struct {
		Cases []struct {
			Name         string  `json:"name"`
			Manifest     string  `json:"manifest"`
			Text         *string `json:"text"`
			BundleCommit *string `json:"bundleCommit"`
			Required     []struct {
				Platform, Arch, Format string
			} `json:"required"`
			Result string `json:"result"`
		} `json:"cases"`
		Finds []struct {
			Platform, Arch, Format, Result string
		} `json:"finds"`
		Assets []struct {
			Name, Content string
		} `json:"assets"`
	}
	load(t, "embedded_manifests", &v)
	root := t.TempDir()
	for _, a := range v.Assets {
		if err := os.WriteFile(filepath.Join(root, a.Name), []byte(a.Content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := New(Options{DataDir: t.TempDir(), Embedded: true, Env: func() []string { return nil }})
	for _, c := range v.Cases {
		text := c.Manifest
		if c.Text != nil {
			text = *c.Text
		}
		if err := os.WriteFile(filepath.Join(root, "manifest.json"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		bundle := &Bundle{}
		if c.BundleCommit != nil {
			bundle.ClientCommit = store.NullString{String: *c.BundleCommit, Valid: true}
		}
		var required []Platform
		for _, r := range c.Required {
			required = append(required, Platform{Platform: r.Platform, Arch: r.Arch, Format: r.Format})
		}
		got := "ok"
		if _, err := s.validateEmbeddedManifest(root, bundle, required); err != nil {
			got = err.Error()
			if c.Text != nil {
				got, _, _ = strings.Cut(got, ":")
			}
		}
		if got != c.Result {
			t.Errorf("%s (commit %v, required %v): node %q, go %q", c.Name, c.BundleCommit, c.Required, c.Result, got)
		}
	}
	var base any
	for _, c := range v.Cases {
		if c.Name == "good" {
			base, _ = parseJSON(c.Manifest)
			break
		}
	}
	for _, f := range v.Finds {
		got, err := s.findManifestArtifact(root, base, f.Platform, f.Arch, f.Format)
		result := ""
		if err != nil {
			result = "error " + err.Error()
		} else {
			result, _ = filepath.Rel(root, got)
		}
		if result != f.Result {
			t.Errorf("find %s/%s/%s: node %q, go %q", f.Platform, f.Arch, f.Format, f.Result, result)
		}
	}
}
