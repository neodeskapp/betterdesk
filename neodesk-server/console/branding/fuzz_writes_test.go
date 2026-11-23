package branding

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzImportPreset imports arbitrary JSON as a theme file, as POST
// /api/settings/branding/import does with its body: no panic, only the
// failures JavaScript had, and after an import that worked the settings
// export and import back.
func FuzzImportPreset(f *testing.F) {
	for _, seed := range []string{
		`{}`, `[]`, `null`, `"x"`, `{"type":"neodesk-theme"}`, `{"type":"neodesk-theme","branding":"x"}`,
		`{"type":"neodesk-theme","branding":{"colors":"red"}}`, `{"type":"neodesk-theme","branding":{"colors":{"bgPrimary":{"toString":1}}}}`,
		`{"type":"neodesk-theme","appearance":{"identity":{"appName":"A"},"palette":{"mode":"light","semantic":{"constructor":1}}}}`,
		`{"type":"neodesk-theme","branding":{"appName":{"toString":1}}}`, `{"type":"neodesk-theme","branding":[1,2,3]}`,
		`{"type":"neodesk-theme","branding":{"loginSlides":[{"imageUrl":"/a"},{"title":{"x":1}}],"customCss":"@import x;"}}`,
		`{"version":"2.0","type":"neodesk-theme","appearance":{"backgrounds":{"console":{"type":"image","imageUrl":"/u.png"}}},"branding":{"appName":"B"}}`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		preset, err := ParseJSON(data)
		if err != nil {
			return
		}
		s, err := fuzzService()
		if err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		ok, err := s.ImportPreset(ctx, preset)
		if err != nil {
			if !ownError(err, "TypeError") {
				t.Fatalf("ImportPreset: %v", err)
			}
			return
		}
		if !ok {
			return
		}
		exported, err := s.ExportPreset()
		if err != nil {
			t.Fatalf("export after an import: %v", err)
		}
		again, err := ParseJSON([]byte(Stringify(exported)))
		if err != nil {
			t.Fatalf("the export is no JSON: %v", err)
		}
		if ok, err := s.ImportPreset(ctx, again); !ok || err != nil {
			t.Fatalf("the export does not import: %v %v", ok, err)
		}
	})
}

// FuzzFontNames deletes fonts by any name, as DELETE
// /api/settings/fonts/{family} does with the path part decoded once more:
// no panic, nothing outside the fonts folder goes, and what
// DecodeURIComponent decodes comes back from its encoding.
func FuzzFontNames(f *testing.F) {
	for _, seed := range []string{"Inter", "Open%20Sans", "%E0%A4", "%", "..", "../..", "%2E%2E%2F%2E%2E", "你好", "a/b", "%ED%A0%80", "%C0%AF", "%F4%90%80%80", "-", " x ", "fonts"} {
		f.Add(seed)
	}
	dir := f.TempDir()
	opts := fixtureOptions(dir)
	s, err := New(opts)
	if err != nil {
		f.Fatal(err)
	}
	outside := filepath.Join(dir, "public", "outside")
	f.Fuzz(func(t *testing.T, raw string) {
		for _, d := range []string{outside, filepath.Join(opts.FontsDir, "kept")} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		family, err := DecodeURIComponent(raw)
		if err != nil {
			if !ownError(err, "URIError") {
				t.Fatalf("%q: %v", raw, err)
			}
			return
		}
		if !strings.Contains(raw, "%") && family != raw {
			t.Fatalf("%q without escapes decoded to %q", raw, family)
		}
		if utf8.ValidString(family) {
			if back, err := DecodeURIComponent(url.PathEscape(family)); err != nil || back != family {
				t.Fatalf("%q: %q does not come back from its encoding: %q %v", raw, family, back, err)
			}
		}
		if _, err := s.DeleteLocalFont(family); err != nil && Message(err) != "Invalid font family name" {
			t.Fatalf("%q: %v", family, err)
		}
		if _, err := os.Stat(outside); err != nil {
			t.Fatalf("%q: the folder next to the fonts went: %v", family, err)
		}
		if _, err := os.Stat(opts.FontsDir); err != nil {
			t.Fatalf("%q: the fonts folder went: %v", family, err)
		}
		if _, err := FontUploadFamily(family, family+".ttf"); err != nil {
			t.Fatalf("%q: FontUploadFamily: %v", family, err)
		}
	})
}
