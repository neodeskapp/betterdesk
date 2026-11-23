//go:build linux

package servermgmt

import (
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/managedconfig"
)

// The server's folder follows the rule of the update and management
// routes (managedconfig): NEODESK_PATH, RUSTDESK_DIR, RUSTDESK_PATH,
// else /opt/neodesk, on Windows C:\NeoDesk.
func TestServerFolderFollowsManagedConfig(t *testing.T) {
	for _, env := range []map[string]string{
		{}, {"RUSTDESK_PATH": "/srv/rp"}, {"RUSTDESK_DIR": "/srv/rd", "RUSTDESK_PATH": "/srv/rp"},
		{"NEODESK_PATH": "/srv/bd", "RUSTDESK_DIR": "/srv/rd", "RUSTDESK_PATH": "/srv/rp"}, {"NEODESK_PATH": "", "RUSTDESK_PATH": "/srv/rp"},
	} {
		getenv := func(k string) string { return env[k] }
		want := managedconfig.New(managedconfig.Options{Getenv: getenv, GOOS: "linux"}).Paths().ServerPath
		s := New(Options{Getenv: getenv, GOOS: "linux", WorkDir: "/srv/console"})
		if read := s.ReadRoots(); read[0] != want {
			t.Errorf("%v: read %v, want %s first", env, read, want)
		}
	}
	win := New(Options{Getenv: func(string) string { return "" }, GOOS: "windows", WorkDir: `C:\console`})
	if got := win.ReadRoots()[0]; got != `C:\NeoDesk` {
		t.Errorf("windows: %s", got)
	}
}
