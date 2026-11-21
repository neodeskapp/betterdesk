package agentreleases

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

const releaseList = "https://api.github.com/repos/UNITRONIX/BetterDesk-Client/releases?per_page=30"

// offer makes the fixture's GitHub list one release: the signed files of
// the fixture under the tag.
func (f *releaseFixture) offer(t *testing.T, tag string) {
	t.Helper()
	var assets []any
	for name := range f.files {
		assets = append(assets, map[string]any{"name": name,
			"browser_download_url": "https://github.com/UNITRONIX/BetterDesk-Client/releases/download/support-v2.0.0/" + name})
	}
	list, err := json.Marshal([]any{map[string]any{"tag_name": tag, "assets": assets}})
	if err != nil {
		t.Fatal(err)
	}
	f.gh.mu.Lock()
	f.gh.answers[releaseList] = reply(http.StatusOK, list)
	f.gh.requests = nil
	f.gh.mu.Unlock()
}

// TestImportChecksTheTag refuses a tag that is no plain file name with
// the support-v prefix, and a tag not higher than every cached release,
// before any file is downloaded; a higher one is imported (BUGS RV55: the
// signed files of an old release came in under any higher tag, and a
// tag with ".." parts wrote outside the cache).
func TestImportChecksTheTag(t *testing.T) {
	for _, c := range []struct {
		name   string
		cached []string
		tag    string
		want   string
	}{
		{"path in the tag", nil, "support-v/../../../../outside", "generic_agent_release_tag_invalid"},
		{"dot parts", nil, "support-v2.0.0/..", "generic_agent_release_tag_invalid"},
		{"prefix alone", nil, "support-v", "generic_agent_release_tag_invalid"},
		{"older signed files under a lower tag", []string{"support-v2.10.0"}, "support-v2.9.0", "generic_agent_release_not_newer:support-v2.9.0"},
		{"the same version", []string{"support-v2.0.0"}, "support-v2.0", "generic_agent_release_not_newer:support-v2.0"},
		{"a suffix below the release", []string{"support-v2.0.0"}, "support-v2.0.0-rc.1", "generic_agent_release_not_newer:support-v2.0.0-rc.1"},
		{"higher by number", []string{"support-v2.9.0", "support-v2.0.0"}, "support-v2.10.0", ""},
		{"release above its suffix", []string{"support-v2.0.0-rc.2"}, "support-v2.0.0", ""},
		{"first release", nil, "support-v1.0.0", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newRelease(t, zipOf(t, "agent/x.exe"))
			data := filepath.Join(t.TempDir(), "data")
			s := f.service(t, data, "agent/x\n")
			for _, tag := range c.cached {
				writeFile(t, filepath.Join(s.Root(), tag, "manifest.json"), `{"assets":[]}`)
			}
			f.offer(t, c.tag)
			imported, err := s.ImportLatest(context.Background())
			if c.want != "" {
				if err == nil || err.Error() != c.want || len(f.gh.requests) != 1 {
					t.Fatalf("got %v after %d requests, want %s after the list alone", err, len(f.gh.requests), c.want)
				}
				if entries, _ := os.ReadDir(s.Root()); len(entries) != len(c.cached) {
					t.Errorf("the cache holds %d entries, want the %d cached", len(entries), len(c.cached))
				}
				if _, err := os.Stat(filepath.Join(filepath.Dir(data), "outside")); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("a folder outside the cache: %v", err)
				}
				return
			}
			if err != nil || imported.Path != filepath.Join(s.Root(), c.tag) {
				t.Fatalf("got %+v, %v", imported, err)
			}
		})
	}
}

// TestFailedImportLeavesNoStaging removes the staging folder when the
// rename fails, here because another import stored the tag while this
// one downloaded; staging folders left by an older console are no
// releases and serve no files (BUGS RV51: each failed import left a
// full copy that was listed and served as the newest release).
func TestFailedImportLeavesNoStaging(t *testing.T) {
	f := newRelease(t, zipOf(t, "agent/x.exe"))
	s := f.service(t, t.TempDir(), "agent/x\n")
	dest := filepath.Join(s.Root(), "support-v2.0.0")
	leftover := filepath.Join(s.Root(), "support-v2.0.0.tmp-4242-1794312120000")
	writeFile(t, filepath.Join(leftover, "manifest.json"), `{"assets":[]}`)
	writeFile(t, filepath.Join(leftover, "agent-windows.zip"), "zip")
	f.offer(t, "support-v2.0.0")
	file := "https://objects.githubusercontent.com/files/SHA256SUMS"
	served := f.gh.answers[file]
	f.gh.answers[file] = func() *http.Response {
		writeFile(t, filepath.Join(dest, "manifest.json"), `{"assets":[]}`)
		return served()
	}
	if _, err := s.ImportLatest(context.Background()); err == nil {
		t.Fatal("the rename onto a full folder went through")
	}
	entries, _ := os.ReadDir(s.Root())
	if len(entries) != 2 {
		t.Errorf("the cache holds %d entries, want the stored tag and the old leftover", len(entries))
	}
	if got := s.ListCached(); len(got) != 1 || got[0].Tag != "support-v2.0.0" {
		t.Errorf("ListCached: %+v", got)
	}
	if got := s.ResolveAsset(filepath.Base(leftover), "agent-windows.zip"); got != "" {
		t.Errorf("a staging folder's file is served: %s", got)
	}
}

// TestCompareTags orders versions by number, a suffix below its release.
func TestCompareTags(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"support-v2.10.0", "support-v2.9.0", 1},
		{"support-v2.0", "support-v2.0.0", 0},
		{"support-v2.0.01", "support-v2.0.1", 0},
		{"support-v2.0.0-rc.1", "support-v2.0.0", -1},
		{"support-v2.0.0-rc.10", "support-v2.0.0-rc.9", 1},
		{"support-v2.0.0-rc", "support-v2.0.0-rc.1", -1},
		{"support-v2.0.0-1", "support-v2.0.0-beta", -1},
		{"support-v10", "support-v9.9.9", 1},
		{"support-v2.x", "support-v2.1", 1},
	} {
		if got := compareTags(c.a, c.b); got != c.want {
			t.Errorf("compareTags(%s, %s) = %d, want %d", c.a, c.b, got, c.want)
		}
		if got := compareTags(c.b, c.a); got != -c.want {
			t.Errorf("compareTags(%s, %s) = %d, want %d", c.b, c.a, got, -c.want)
		}
	}
}
