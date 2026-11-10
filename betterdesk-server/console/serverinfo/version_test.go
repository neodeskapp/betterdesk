package serverinfo

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

type versionVectors struct {
	Read []struct {
		Root            textOrDir `json:"root"`
		ConsoleFile     textOrDir `json:"consoleFile"`
		Pkg             textOrDir `json:"pkg"`
		Embedded        textOrDir `json:"embedded"`
		Layout          string    `json:"layout"`
		Fallback        *string   `json:"fallback"`
		Version         string    `json:"version"`
		PackageVersion  *string   `json:"packageVersion"`
		EmbeddedVersion *string   `json:"embeddedVersion"`
	} `json:"read"`
	EmbeddedWithoutDir []*string `json:"embedded_without_dir"`
}

func TestProductVersionAgainstNode(t *testing.T) {
	var v versionVectors
	loadVectors(t, "version", &v)
	atLeast(t, "read", len(v.Read), 700)
	tmp := t.TempDir()
	m := &mismatches{t: t}
	sources := map[string]int{}
	for i, c := range v.Read {
		label := "case " + strconv.Itoa(i)
		base := filepath.Join(tmp, strconv.Itoa(i))
		consoleDir := base
		switch c.Layout {
		case "default":
			consoleDir = filepath.Join(base, "web-nodejs")
		case "named":
			consoleDir = filepath.Join(base, "console")
		}
		serverDir := filepath.Join(base, "betterdesk-server")
		embeddedDir := filepath.Join(serverDir, "internal", "productversion")
		for _, dir := range []string{consoleDir, embeddedDir} {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		c.Root.put(t, filepath.Join(base, "VERSION"), 0o644)
		if c.Layout != "flat" {
			c.ConsoleFile.put(t, filepath.Join(consoleDir, "VERSION"), 0o644)
		}
		c.Pkg.put(t, filepath.Join(consoleDir, "package.json"), 0o644)
		c.Embedded.put(t, filepath.Join(embeddedDir, "VERSION"), 0o644)

		consoleArg := consoleDir
		if c.Layout == "default" {
			consoleArg = ""
		}
		fallback := DefaultVersionFallback
		if c.Fallback != nil {
			fallback = *c.Fallback
		}
		got := ReadProductVersion(OSFS{}, base, consoleArg, fallback)
		if got != c.Version {
			m.errorf("%s: ReadProductVersion = %q, Node answers %q (%+v)", label, got, c.Version, c)
		}
		switch {
		case got == fallback:
			sources["fallback"]++
		default:
			sources["found"]++
		}

		pkg, ok := ReadPackageJSONVersion(OSFS{}, consoleDir)
		if ok != (c.PackageVersion != nil) || (ok && pkg != *c.PackageVersion) {
			m.errorf("%s: ReadPackageJSONVersion = %q, %v; Node answers %v (package.json %q)", label, pkg, ok, c.PackageVersion, c.Pkg.Text)
		}
		if ok {
			sources["package"]++
		}
		embedded, ok := ReadEmbeddedServerVersion(OSFS{}, serverDir)
		if ok != (c.EmbeddedVersion != nil) || (ok && embedded != *c.EmbeddedVersion) {
			m.errorf("%s: ReadEmbeddedServerVersion = %q, %v; Node answers %v", label, embedded, ok, c.EmbeddedVersion)
		}
		if ok {
			sources["embedded"]++
		}
	}
	for _, source := range []string{"fallback", "found", "package", "embedded"} {
		if sources[source] < 50 {
			t.Errorf("only %d cases of kind %q", sources[source], source)
		}
	}
	for _, out := range v.EmbeddedWithoutDir {
		if out != nil {
			t.Errorf("Node read a version without a folder: %q", *out)
		}
	}
	if _, ok := ReadEmbeddedServerVersion(OSFS{}, ""); ok {
		t.Error("ReadEmbeddedServerVersion found a version without a folder")
	}
}

// The order of the sources, as the Jest tests of lib/productVersion.js pin
// it down.
func TestProductVersionSources(t *testing.T) {
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	fsys := OSFS{}

	root := t.TempDir()
	write(filepath.Join(root, "VERSION"), "4.1.2\n")
	if got := ReadProductVersion(fsys, root, "", DefaultVersionFallback); got != "4.1.2" {
		t.Errorf("root VERSION: %q", got)
	}

	root = t.TempDir()
	consoleDir := filepath.Join(root, "web-nodejs")
	write(filepath.Join(consoleDir, "package.json"), `{"version":"3.9.8"}`)
	if got := ReadProductVersion(fsys, root, consoleDir, DefaultVersionFallback); got != "3.9.8" {
		t.Errorf("package.json without VERSION: %q", got)
	}
	if got := ReadProductVersion(fsys, root, "", DefaultVersionFallback); got != "3.9.8" {
		t.Errorf("package.json in the default console folder: %q", got)
	}
	write(filepath.Join(root, "VERSION"), "2.0.1\n")
	if got := ReadProductVersion(fsys, root, consoleDir, DefaultVersionFallback); got != "2.0.1" {
		t.Errorf("VERSION before package.json: %q", got)
	}

	root = t.TempDir()
	consoleDir = filepath.Join(root, "console")
	write(filepath.Join(consoleDir, "VERSION"), "3.3.0\n")
	write(filepath.Join(consoleDir, "package.json"), `{"version":"3.2.0"}`)
	if got := ReadProductVersion(fsys, root, consoleDir, DefaultVersionFallback); got != "3.3.0" {
		t.Errorf("console VERSION before package.json: %q", got)
	}

	root = t.TempDir()
	consoleDir = filepath.Join(root, "BetterDeskConsole")
	write(filepath.Join(root, "VERSION"), "3.3.1\n")
	write(filepath.Join(consoleDir, "package.json"), `{"version":"3.5.108"}`)
	if got := ReadProductVersion(fsys, consoleDir, consoleDir, DefaultVersionFallback); got != "3.5.108" {
		t.Errorf("flat install: %q", got)
	}

	root = t.TempDir()
	serverDir := filepath.Join(root, "betterdesk-server")
	embedded := filepath.Join(serverDir, "internal", "productversion", "VERSION")
	if _, ok := ReadEmbeddedServerVersion(fsys, serverDir); ok {
		t.Error("an absent embedded version was found")
	}
	write(embedded, "3.5.218\n")
	write(filepath.Join(root, "VERSION"), "3.5.217\n")
	if got, ok := ReadEmbeddedServerVersion(fsys, serverDir); !ok || got != "3.5.218" {
		t.Errorf("embedded version: %q, %v", got, ok)
	}
	if got := ReadProductVersion(fsys, root, root, DefaultVersionFallback); got != "3.5.217" {
		t.Errorf("root VERSION beside the embedded one: %q", got)
	}
	write(embedded, "not-a-version\n")
	if _, ok := ReadEmbeddedServerVersion(fsys, serverDir); ok {
		t.Error("a malformed embedded version was accepted")
	}

	// Without a server folder nothing is read, also when the working
	// directory happens to be a server source tree.
	t.Chdir(serverDir)
	write(embedded, "3.5.218\n")
	if got, ok := ReadEmbeddedServerVersion(fsys, ""); ok {
		t.Errorf("a version was read without a server folder: %q", got)
	}

	if got := ReadProductVersion(fsys, t.TempDir(), "", "7.7.7"); got != "7.7.7" {
		t.Errorf("fallback: %q", got)
	}
	if DefaultVersionFallback != "0.0.0" {
		t.Errorf("default fallback %q", DefaultVersionFallback)
	}
}

func TestIsSemver(t *testing.T) {
	for v, want := range map[string]bool{"1.2.3": true, "10.20.30": true, "1.2": false, "1.2.3.4": false, "v1.2.3": false, "1.2.3\n": false, "": false, "1.2.x": false} {
		if got := IsSemver(v); got != want {
			t.Errorf("IsSemver(%q) = %v", v, got)
		}
	}
}
