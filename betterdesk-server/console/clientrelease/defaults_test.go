package clientrelease

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// TestShippedSettingsNameTheFork checks that the compose files and the
// managed config's example give BETTERDESK_CLIENT_REPO the client fork,
// the repository whose releases are signed with the release key.
func TestShippedSettingsNameTheFork(t *testing.T) {
	setting := regexp.MustCompile(`BETTERDESK_CLIENT_(?:ALLOWED_)?REPOS?=(?:\$\{BETTERDESK_CLIENT_REPO:-)?([^}\s]*)`)
	for _, file := range []string{
		"../../../docker-compose.single.yml",
		"../../../docker-compose.quick.single.yml",
		"../../../docker-compose.quick.single.macvlan.yml",
		"../managedconfig/env.example",
	} {
		raw, err := os.ReadFile(filepath.FromSlash(file))
		if err != nil {
			t.Fatal(err)
		}
		found := setting.FindAllSubmatch(raw, -1)
		if len(found) == 0 {
			t.Errorf("%s sets no BETTERDESK_CLIENT_REPO", file)
		}
		for _, m := range found {
			if string(m[1]) != DefaultRepo {
				t.Errorf("%s: %s, want %s", file, m[0], DefaultRepo)
			}
		}
	}
}
