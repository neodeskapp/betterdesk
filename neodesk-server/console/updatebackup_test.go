package console

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/updater"
)

const (
	runningCommit = "1111111111111111111111111111111111111111"
	releaseCommit = "2222222222222222222222222222222222222222"
)

// fakeBinary is a binary that "prints" its -version line (fakeVersion
// reads it back).
func fakeBinary(version, commit string) []byte {
	return []byte("#!/bin/sh\necho 'betterdesk-server " + version + " (built 2026-11-18, commit " + commit + ")'\n")
}

var reFakeVersion = regexp.MustCompile(`echo '([^']*)'`)

func fakeVersion(_ context.Context, binary string) (string, error) {
	data, err := os.ReadFile(binary)
	if err != nil {
		return "", err
	}
	m := reFakeVersion.FindSubmatch(data)
	if m == nil {
		return "", errors.New("exec format error")
	}
	return string(m[1]) + "\n", nil
}

// releaseFiles is a GitHub with one stable release, 3.6.0 for
// linux/amd64, unsigned.
type releaseFiles map[string][]byte

func (f releaseFiles) RoundTrip(r *http.Request) (*http.Response, error) {
	body, ok := f[r.URL.Host+r.URL.Path]
	status := http.StatusOK
	if !ok {
		status, body = http.StatusNotFound, []byte(`{"message":"Not Found"}`)
	}
	return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: r}, nil
}

func newReleaseFiles(t *testing.T) releaseFiles {
	t.Helper()
	asset := updater.AssetName("linux", "amd64")
	binary := fakeBinary("3.6.0", releaseCommit)
	sum := sha256.Sum256(binary)
	manifest, err := json.Marshal(updater.Manifest{
		SchemaVersion: 1, Product: updater.Product, Version: "3.6.0", Channel: updater.ChannelOf(updater.BranchStable),
		Created: "2026-11-18T10:00:00Z", Owner: "trybetterdesk", Repo: "betterdesk", Branch: updater.BranchStable, SHA: releaseCommit,
		Files: []updater.ManifestFile{{Path: asset, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(binary))}},
	})
	if err != nil {
		t.Fatal(err)
	}
	const download = "github.com/trybetterdesk/betterdesk/releases/download/v3.6.0/"
	files := releaseFiles{download + "manifest.json": manifest, download + asset: binary}
	type ghAsset struct {
		Name string `json:"name"`
		Size int    `json:"size"`
		URL  string `json:"browser_download_url"`
	}
	var assets []ghAsset
	for _, name := range []string{"manifest.json", asset} {
		assets = append(assets, ghAsset{Name: name, Size: len(files[download+name]), URL: "https://" + download + name})
	}
	release, err := json.Marshal(map[string]any{
		"tag_name": "v3.6.0", "name": "BetterDesk 3.6.0", "draft": false, "prerelease": false,
		"published_at": "2026-11-18T10:00:00Z", "author": map[string]string{"login": "unitronix"}, "assets": assets,
	})
	if err != nil {
		t.Fatal(err)
	}
	files["api.github.com/repos/trybetterdesk/betterdesk/releases/latest"] = release
	return files
}

// RV17: an update with createBackup keeps the console's data backup
// beside the binary it replaces. The backup service the console wires
// into the updater makes it, and it holds the server's database named
// by Options.ServerDB.
func TestUpdateKeepsTheDataBackup(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "bin", "betterdesk-server")
	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, fakeBinary("3.5.0", runningCommit), 0o755); err != nil {
		t.Fatal(err)
	}
	w := newWorld(t, setup{options: func(o *Options) {
		o.Version, o.Commit = "3.5.0", runningCommit
		o.ServerDB = filepath.Join(root, "db_v2.sqlite3")
		o.updaterOptions = func(u *updater.Options) {
			rename := false
			u.Keys = updater.Keys{}
			u.Transport = newReleaseFiles(t)
			u.Executable = func() (string, error) { return binary, nil }
			u.GOOS, u.GOARCH, u.RenameAside = "linux", "amd64", &rename
			u.RunVersion = fakeVersion
			u.FreeSpace = func(string) (int64, error) { return 10 << 30, nil }
		}
	}})
	out, err := w.s.Deps().Updater.Install(context.Background(), updater.InstallOptions{SHA: releaseCommit, Backup: true})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	result, ok := out.(*updater.InstallResult)
	if !ok || result.BackupPath == nil {
		t.Fatalf("install answered %T %+v", out, out)
	}
	if result.BackedUp != 2 {
		t.Errorf("backedUp %d, want the binary and the data backup", result.BackedUp)
	}
	var manifest updater.BackupManifest
	raw, err := os.ReadFile(filepath.Join(*result.BackupPath, "manifest.json"))
	if err != nil || json.Unmarshal(raw, &manifest) != nil {
		t.Fatalf("backup manifest: %v %s", err, raw)
	}
	if manifest.DataBackup == "" {
		t.Fatalf("the backup manifest names no data backup: %s", raw)
	}
	archive, err := os.ReadFile(filepath.Join(*result.BackupPath, manifest.DataBackup))
	if err != nil {
		t.Fatalf("data backup: %v", err)
	}
	names := tarNames(t, archive)
	want := []string{"manifest.json", "console/data.json"}
	if !w.db.IsPostgres() {
		// On SQLite the server's database is the console's, named by ServerDB
		want = append(want, "goserver/db_v2.sqlite3")
	}
	for _, name := range want {
		if !names[name] {
			t.Errorf("the data backup has no %s: %v", name, names)
		}
	}
}

// tarNames lists the files of a .tar.gz.
func tarNames(t *testing.T, archive []byte) map[string]bool {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	names := map[string]bool{}
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return names
		}
		if err != nil {
			t.Fatal(err)
		}
		names[strings.TrimPrefix(h.Name, "./")] = true
	}
}
