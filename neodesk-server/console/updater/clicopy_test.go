package updater

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// On a Linux host install the command line is a root-owned copy of the
// server binary (/usr/local/bin/neodesk). An update from the console
// replaces only the server binary, so the update screen says to run
// `sudo neodesk-setup --update` once when the copy is no longer the
// running binary. A current copy, a link (older layouts), no copy at all,
// another system and Docker say nothing.
func TestTheUpdateScreenSaysWhenTheCommandLineCopyIsOlder(t *testing.T) {
	status := func(in *install) string {
		t.Helper()
		data, err := json.Marshal(in.svc.BinaryStatus())
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	copyPath := func(in *install) string { return filepath.Join(filepath.Dir(in.dir), "usr-local-bin-neodesk") }
	withCopy := func(o *Options) { o.CLICopy = filepath.Join(filepath.Dir(o.DataDir), "usr-local-bin-neodesk") }

	in := newInstall(t, withCopy)
	if got := status(in); strings.Contains(got, "cliOutdated") {
		t.Errorf("no copy: %s", got)
	}
	current, _ := os.ReadFile(in.binary)
	if err := os.WriteFile(copyPath(in), current, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := status(in); strings.Contains(got, "cliOutdated") {
		t.Errorf("a current copy: %s", got)
	}
	if err := os.WriteFile(copyPath(in), binaryFor("3.4.0", "0000000"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := status(in); !strings.Contains(got, `"cliOutdated":true`) {
		t.Errorf("an older copy: %s", got)
	}
	// A link into the server folder always runs the current binary
	if err := os.Remove(copyPath(in)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(in.binary, copyPath(in)); err == nil {
		if got := status(in); strings.Contains(got, "cliOutdated") {
			t.Errorf("a link: %s", got)
		}
	}

	for name, mutate := range map[string]func(*Options){
		"Windows": func(o *Options) { withCopy(o); o.GOOS = "windows" },
		"Docker":  func(o *Options) { withCopy(o); o.Docker = true },
	} {
		other := newInstall(t, mutate)
		if err := os.WriteFile(copyPath(other), binaryFor("3.4.0", "0000000"), 0o755); err != nil {
			t.Fatal(err)
		}
		if got := status(other); strings.Contains(got, "cliOutdated") {
			t.Errorf("%s: %s", name, got)
		}
	}
}
