package branding

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// fromFiles is fixtureOptions with the four built-in files taken from
// Files instead of from their paths: the same files, as the console's
// tree in the binary holds them.
func fromFiles(dir string) Options {
	opts := fixtureOptions(dir)
	opts.Files = os.DirFS(filepath.Join("testdata", "console"))
	opts.ThemesDir, opts.PalettesFile, opts.FaviconFile, opts.SupportersFile = "", "", "", ""
	return opts
}

// The built-in files read from Files give what the same files on disk
// give: the palettes, the theme list, each theme applied, the favicon
// and the supporters.
func TestBuiltinFilesFromFilesAsFromDisk(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		services := map[string]*Service{}
		for name, opts := range map[string]Options{"disk": fixtureOptions(t.TempDir()), "files": fromFiles(t.TempDir())} {
			opts.DB = db
			s, err := New(opts)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			services[name] = s
		}
		disk, files := services["disk"], services["files"]
		if got, want := Stringify(files.GetThemePalettes()), Stringify(disk.GetThemePalettes()); got != want || got == `{"dark":{},"light":{}}` {
			t.Errorf("palettes from Files: %s, from disk: %s", got, want)
		}
		listFiles, err := files.ListThemes()
		if err != nil {
			t.Fatal(err)
		}
		listDisk, err := disk.ListThemes()
		if err != nil {
			t.Fatal(err)
		}
		if got, want := Stringify(listFiles), Stringify(listDisk); got != want || len(listFiles) != 3 {
			t.Errorf("themes from Files: %s, from disk: %s", got, want)
		}
		for _, id := range []string{"light", "insolve", "default", "missing", "../light", "light.json", ""} {
			resultFiles, errFiles := files.ApplyTheme(ctx, id)
			brandingFiles := Stringify(files.GetBranding())
			resultDisk, errDisk := disk.ApplyTheme(ctx, id)
			brandingDisk := Stringify(disk.GetBranding())
			if resultFiles != resultDisk || (errFiles == nil) != (errDisk == nil) || brandingFiles != brandingDisk {
				t.Errorf("ApplyTheme(%q): from Files %v %v, from disk %v %v", id, resultFiles, errFiles, resultDisk, errDisk)
			}
		}
		if got, want := files.GenerateFavicon(), disk.GenerateFavicon(); got != want || got == defaultFaviconFallback {
			t.Errorf("favicon from Files: %.80q, from disk: %.80q", got, want)
		}
		if got, want := Stringify(files.LoadSupporters()), Stringify(disk.LoadSupporters()); got != want {
			t.Errorf("supporters from Files: %s, from disk: %s", got, want)
		}
	})
}

// A path wins over Files; Files without the file is what a missing file
// on disk is.
func TestBuiltinPathWinsOverFiles(t *testing.T) {
	tree := fstest.MapFS{
		"config/theme-palettes.json": {Data: []byte(`{"dark":{"bgPrimary":"#010101"},"light":{}}`)},
		"public/favicon.svg":         {Data: []byte(`<svg id="files"/>`)},
		"config/supporters.json":     {Data: []byte(`{"honorary":[{"name":"From files"}]}`)},
		"themes/only.json":           {Data: []byte(`{"type":"neodesk-theme","displayName":"Only in files","branding":{"appName":"x"}}`)},
	}
	opts := fixtureOptions(t.TempDir())
	opts.Files = tree
	s, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if dark, _ := s.GetThemePalettes().Get("dark"); dark.(*Object).Text("bgPrimary") != "#141616" {
		t.Errorf("the palettes came from Files beside PalettesFile: %s", Stringify(dark))
	}
	if s.GenerateFavicon() == `<svg id="files"/>` {
		t.Error("the favicon came from Files beside FaviconFile")
	}
	if list, _ := s.ListThemes(); len(list) != 3 {
		t.Errorf("themes beside ThemesDir: %s", Stringify(list))
	}

	opts = fixtureOptions(t.TempDir())
	opts.ThemesDir, opts.PalettesFile, opts.FaviconFile, opts.SupportersFile = "", "", "", ""
	opts.Files = tree
	if s, err = New(opts); err != nil {
		t.Fatal(err)
	}
	if dark, _ := s.GetThemePalettes().Get("dark"); dark.(*Object).Text("bgPrimary") != "#010101" {
		t.Errorf("palettes from Files: %s", Stringify(dark))
	}
	if got := s.GenerateFavicon(); got != `<svg id="files"/>` {
		t.Errorf("favicon from Files: %q", got)
	}
	if list, _ := s.ListThemes(); len(list) != 1 || list[0].(*Object).Text("name") != "Only in files" {
		t.Errorf("themes from Files: %s", Stringify(list))
	}
	honorary, _ := s.LoadSupporters().Get("honorary")
	if Stringify(honorary) != `[{"name":"From files"}]` {
		t.Errorf("supporters from Files: %s", Stringify(honorary))
	}

	opts.Files = fstest.MapFS{}
	if _, err := New(opts); err == nil {
		t.Error("New without the palettes in Files did not fail")
	}
}
