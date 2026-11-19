package backup

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/avatars"
	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// Tests of what a restore writes, refuses and leaves behind. They need
// neither Node nor PostgreSQL unless a server is configured.

func dialects(t *testing.T, fn func(t *testing.T, dialect string)) {
	t.Run("sqlite", func(t *testing.T) { fn(t, "sqlite") })
	t.Run("postgres", func(t *testing.T) {
		if os.Getenv(storetest.PostgresEnv) == "" {
			t.Skipf("%s is not set", storetest.PostgresEnv)
		}
		fn(t, "postgres")
	})
}

func fixedClock() func() time.Time {
	return func() time.Time { return time.Date(2026, 11, 10, 8, 9, 10, 123456789, time.UTC) }
}

func TestFullBackupHoldsEveryPart(t *testing.T) {
	dialects(t, func(t *testing.T, dialect string) {
		in := newInstall(t, dialect)
		in.seed(t)
		s := in.service(t, "3.5.224")
		s.o.Now = fixedClock()
		archive, name, err := s.CreateFullBackup(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if name != "betterdesk-backup-2026-11-10-08-09-10.tar.gz" {
			t.Errorf("name %q", name)
		}
		entries := tarEntries(t, archive)
		var names []string
		modes := map[string]string{}
		for _, e := range entries {
			names = append(names, e.name)
			modes[e.name] = string(e.header[100:107])
		}
		want := []string{"manifest.json", "console/data.json", "goserver/data.json", "console/.env", "console/.session_secret"}
		if dialect == "sqlite" {
			want = append(want, "console/auth.db")
		}
		want = append(want, "console/uploads/bg/nested.jpg", "console/uploads/logo-0011.png", "console/uploads/zz-ä.txt",
			"goserver/id_ed25519", "goserver/id_ed25519.pub", "goserver/.api_key")
		if dialect == "sqlite" {
			want = append(want, "goserver/db_v2.sqlite3")
		}
		want = append(want, "goserver/mesh_agent_server.pem", "README.txt")
		if strings.Join(names, ",") != strings.Join(want, ",") {
			t.Fatalf("entries:\n %q\nwant\n %q", names, want)
		}
		for name, mode := range map[string]string{"manifest.json": "0000644", "console/data.json": "0000644", "console/.env": "0000600",
			"goserver/id_ed25519": "0000600", "console/uploads/bg/nested.jpg": "0000644", "README.txt": "0000644"} {
			if modes[name] != mode {
				t.Errorf("%s: mode %q, want %q", name, modes[name], mode)
			}
		}
		manifest := string(entries[0].data)
		for _, part := range []string{`"_format": "betterdesk-full-backup"`, `"_version": 1`, `"_created": "2026-11-10T08:09:10.123Z"`,
			`"_console_version": "3.5.224"`, `"dbType": "` + dialect + `"`, `"components": [` + "\n" + `    "console/data.json",`} {
			if !strings.Contains(manifest, part) {
				t.Errorf("manifest has no %q:\n%s", part, manifest)
			}
		}
		readme := string(entries[len(entries)-1].data)
		if !strings.Contains(readme, "Created:          2026-11-10T08:09:10.123Z\n") || !strings.Contains(readme, "Database engine:  "+dialect+"\n") {
			t.Errorf("README:\n%s", readme)
		}
	})
}

func TestFullBackupStats(t *testing.T) {
	dialects(t, func(t *testing.T, dialect string) {
		in := newInstall(t, dialect)
		in.seed(t)
		st := in.service(t, "x").FullBackupStats(context.Background())
		if st.Users != 2 || st.Folders != 1 || st.Strategies != 1 || st.Settings < 4 || st.Backend != "betterdesk" || st.DBType != dialect {
			t.Errorf("stats %+v", st)
		}
		c := st.Components
		if !c.Env || !c.SessionSecret || c.Uploads != 3 || !c.GoPrivKey || !c.GoPubKey || !c.GoAPIKey || !c.MeshAgentCert {
			t.Errorf("components %+v", c)
		}
		if c.ConsoleDB != (dialect == "sqlite") || c.GoDB != (dialect == "sqlite") {
			t.Errorf("database files %+v", c)
		}
		if err := os.Remove(filepath.Join(in.keysPath, "mesh_agent_server.pem")); err != nil {
			t.Fatal(err)
		}
		if in.service(t, "x").FullBackupStats(context.Background()).Components.MeshAgentCert {
			t.Error("a removed file still counts")
		}
	})
}

// An archive that breaks half way is refused before anything is written:
// the database and the files stay as they were.
func TestBrokenArchiveChangesNothing(t *testing.T) {
	dialects(t, func(t *testing.T, dialect string) {
		in := newInstall(t, dialect)
		in.seed(t)
		s := in.service(t, "x")
		archive, _, err := s.CreateFullBackup(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, in.db, `UPDATE folders SET name = 'changed after the backup'`)
		if err := os.WriteFile(filepath.Join(in.keysPath, "id_ed25519"), []byte("new key"), 0o600); err != nil {
			t.Fatal(err)
		}
		before, beforeFiles := snapshot(t, in.db), in.files(t)

		flipped := bytes.Clone(archive)
		flipped[len(flipped)/2] ^= 0x55
		for label, broken := range map[string][]byte{
			"cut in half":     archive[:len(archive)/2],
			"byte changed":    flipped,
			"trailer missing": archive[:len(archive)-4],
		} {
			_, err := s.RestoreFullBackup(context.Background(), broken, RestoreOptions{Database: true, Uploads: true, Secrets: true, Env: true, GoDB: true})
			var invalid *ErrInvalidArchive
			if !errors.As(err, &invalid) || !strings.HasPrefix(err.Error(), "Invalid backup archive: Not a valid gzip/tar archive: ") {
				t.Errorf("%s: %v", label, err)
			}
		}
		diffSnapshots(t, "after the refused restores", before, snapshot(t, in.db))
		after := in.files(t)
		for name, content := range beforeFiles {
			if after[name] != content {
				t.Errorf("file %s changed", name)
			}
		}
	})
}

// A table that fails keeps its rows; the others are replaced and the run
// goes on, as before.
func TestFailingTableKeepsItsRows(t *testing.T) {
	in := newInstall(t, "sqlite")
	in.seed(t)
	dump, err := branding.ParseJSON([]byte(`{"tables":{"folders":[{"id":7,"name":"from backup"}],"strategies":[{"guid":"x","name":true}],"settings":[{"key":"only","value":"one"}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	res, err := ImportAllTables(context.Background(), in.db, dump)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Restored, ",") != "folders,settings" || len(res.Warnings) != 1 ||
		res.Warnings[0] != "Table strategies: SQLite3 can only bind numbers, strings, bigints, buffers, and null" {
		t.Errorf("result %+v", res)
	}
	tables := snapshot(t, in.db)
	if strings.Join(tables["strategies"], ",") == "" || !strings.Contains(strings.Join(tables["strategies"], ","), "'g-1'") {
		t.Errorf("strategies lost their rows: %q", tables["strategies"])
	}
	if f := tables["folders"]; len(f) != 1 || !strings.HasPrefix(f[0], "7|'from backup'|'#6366f1'|'folder'|0|'") {
		t.Errorf("folders %q", tables["folders"])
	}
	// The writer has foreign keys back on.
	var fk int
	if err := in.db.InTx(context.Background(), func(tx *store.Tx) error {
		return tx.QueryRow(context.Background(), `PRAGMA foreign_keys`).Scan(&fk)
	}); err != nil || fk != 1 {
		t.Errorf("foreign keys after the import: %d %v", fk, err)
	}
}

func TestUploadNamesFromTheArchive(t *testing.T) {
	in := newInstall(t, "sqlite")
	uploads := filepath.Join(in.dataDir, "uploads")
	outside := filepath.Join(t.TempDir(), "outside")
	for _, dir := range []string{uploads, outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(uploads, "link")); err != nil {
		t.Fatal(err)
	}
	entries := []Entry{{Name: "manifest.json", Data: []byte(`{"_format":"betterdesk-full-backup","_version":1}`)}}
	for _, name := range []string{"console/uploads/ok.png", "console/uploads/../evil.png", "console/uploads/a..b.png", "console/uploads/sub/deep.png",
		"console/uploads/", "console/uploads/./dot.png", "console/uploads/link/escape.png", "console/uploadsX/no.png",
		// a NUL survives the name cut when a line break follows it
		"console/uploads/nul\x00name\n.png"} {
		entries = append(entries, Entry{Name: name, Data: []byte("data of " + name)})
	}
	tar, err := buildTar(entries, 1)
	if err != nil {
		t.Fatal(err)
	}
	// A name that starts with a slash after the prefix, as no archive the
	// console made has, written by hand into the tar.
	abs, err := buildTar([]Entry{{Name: "console/uploads/x", Data: []byte("abs")}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	copy(abs[0:100], append([]byte("console/uploads//abs.png"), make([]byte, 76)...))
	archive := gzipBytes(t, append(tar[:len(tar)-1024], abs...))

	res, err := in.service(t, "x").RestoreFullBackup(context.Background(), archive, RestoreOptions{Uploads: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Restored, ",") != "uploads (2 files)" {
		t.Errorf("result %+v", res)
	}
	for _, name := range []string{"ok.png", "sub/deep.png"} {
		if data, err := os.ReadFile(filepath.Join(uploads, name)); err != nil || string(data) != "data of console/uploads/"+name {
			t.Errorf("%s: %q %v", name, data, err)
		}
	}
	for _, path := range []string{filepath.Join(in.dataDir, "evil.png"), filepath.Join(uploads, "a..b.png"), filepath.Join(uploads, "dot.png"),
		filepath.Join(outside, "escape.png"), filepath.Join(uploads, "abs.png"), "/abs.png"} {
		if _, err := os.Stat(path); err == nil {
			t.Errorf("%s was written", path)
		}
	}
}

func gzipBytes(t *testing.T, tar []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	if _, err := zw.Write(tar); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// A restored file that is new gets the mode of its part; one that exists
// keeps its own. A target that cannot be written is a warning with Node's
// text, and the parts after it still run.
func TestRestoredFiles(t *testing.T) {
	src := newInstall(t, "sqlite")
	src.seed(t)
	archive, _, err := src.service(t, "x").CreateFullBackup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	dst := newInstall(t, "sqlite")
	if err := os.WriteFile(filepath.Join(dst.keysPath, ".api_key"), []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dst.keysPath, "id_ed25519"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := dst.service(t, "x").RestoreFullBackup(context.Background(), archive, RestoreOptions{Uploads: true, Secrets: true, Env: true})
	if err != nil {
		t.Fatal(err)
	}
	wantWarning := "goserver/id_ed25519 restore failed: EISDIR: illegal operation on a directory, open '" + filepath.Join(dst.keysPath, "id_ed25519") + "'"
	if len(res.Warnings) != 1 || res.Warnings[0] != wantWarning {
		t.Errorf("warnings %q, want %q", res.Warnings, wantWarning)
	}
	if strings.Join(res.Restored, ",") != "uploads (3 files),secrets/keys,.env" || strings.Join(res.Skipped, ",") != "database,goserver-db" || !res.RequiresRestart {
		t.Errorf("result %+v", res)
	}
	for path, mode := range map[string]os.FileMode{
		filepath.Join(dst.dataDir, ".session_secret"):          0o600,
		filepath.Join(dst.keysPath, "id_ed25519.pub"):          0o644,
		filepath.Join(dst.keysPath, ".api_key"):                0o640,
		filepath.Join(dst.dataDir, "uploads", "logo-0011.png"): 0o644,
		dst.envFile: 0o600,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm() &^ 0o022; got != mode&^0o022 {
			t.Errorf("%s: mode %o, want %o", path, info.Mode().Perm(), mode)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(dst.keysPath, ".api_key")); string(data) != "api-key-bytes" {
		t.Errorf("api key not restored (%d bytes)", len(data))
	}
}

// BR14: a full restore that replaced branding_config reloads the
// branding, so the pages show the restored look at once.
func TestRestoreReloadsTheBranding(t *testing.T) {
	dialects(t, func(t *testing.T, dialect string) {
		ctx := context.Background()
		withBranding := func(in *install) (*Service, *branding.Service) {
			b, err := branding.New(branding.Options{DB: in.db})
			if err != nil {
				t.Fatal(err)
			}
			b.LoadBranding(ctx)
			s := in.service(t, "x")
			s.o.Branding = b
			return s, b
		}
		src := newInstall(t, dialect)
		srcService, srcBranding := withBranding(src)
		if err := srcBranding.SaveBranding(ctx, parsedJSON(t, `{"appName":"From the backup"}`)); err != nil {
			t.Fatal(err)
		}
		archive, _, err := srcService.CreateFullBackup(ctx)
		if err != nil {
			t.Fatal(err)
		}

		dst := newInstall(t, dialect)
		dstService, dstBranding := withBranding(dst)
		if name := dstBranding.GetBranding().Text("appName"); name == "From the backup" {
			t.Fatalf("the branding is restored before the restore: %q", name)
		}
		res, err := dstService.RestoreFullBackup(ctx, archive, RestoreOptions{Database: true})
		if err != nil || len(res.Warnings) != 0 {
			t.Fatalf("restore: %+v %v", res, err)
		}
		if name := dstBranding.GetBranding().Text("appName"); name != "From the backup" {
			t.Errorf("app name after the restore: %q", name)
		}
	})
}

// writeFiles writes files below dir: name to content.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// filesBelow lists the files below dir with their content.
func filesBelow(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		data, _ := os.ReadFile(path)
		rel, _ := filepath.Rel(dir, path)
		out[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	return out
}

// CH2: the chat files, with the links that say who may download them, go
// into the full backup and come back with the console data.
func TestFullBackupCarriesTheChatFiles(t *testing.T) {
	ctx := context.Background()
	src := newInstall(t, "sqlite")
	chat := map[string]string{
		"0123456789abcdef0123456789abcdef.pdf":        "a chat file",
		"fedcba9876543210fedcba9876543210":            "no extension",
		".links/0123456789abcdef0123456789abcdef.pdf": `{"owner":50}` + "\n",
	}
	writeFiles(t, filepath.Join(src.consoleDir, "data", "chat-files"), chat)
	archive, _, err := src.service(t, "x").CreateFullBackup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range tarEntries(t, archive) {
		if strings.HasPrefix(e.name, "console/chat-files/") {
			names = append(names, e.name)
		}
	}
	if strings.Join(names, ",") != "console/chat-files/.links/0123456789abcdef0123456789abcdef.pdf,console/chat-files/0123456789abcdef0123456789abcdef.pdf,console/chat-files/fedcba9876543210fedcba9876543210" {
		t.Errorf("chat entries %q", names)
	}

	dst := newInstall(t, "sqlite")
	writeFiles(t, filepath.Join(dst.consoleDir, "data", "chat-files"), map[string]string{"11111111111111111111111111111111.txt": "kept"})
	res, err := dst.service(t, "x").RestoreFullBackup(ctx, archive, RestoreOptions{Database: true})
	if err != nil || len(res.Warnings) != 0 || !slices.Contains(res.Restored, "chat files (3 files)") {
		t.Fatalf("restore: %+v %v", res, err)
	}
	want := map[string]string{"11111111111111111111111111111111.txt": "kept"}
	for name, content := range chat {
		want[name] = content
	}
	if got := filesBelow(t, filepath.Join(dst.consoleDir, "data", "chat-files")); !maps.Equal(got, want) {
		t.Errorf("chat files after the restore: %q", got)
	}

	// without the console data they stay where they are
	other := newInstall(t, "sqlite")
	res, err = other.service(t, "x").RestoreFullBackup(ctx, archive, RestoreOptions{Uploads: true})
	if err != nil || slices.Contains(res.Restored, "chat files (3 files)") {
		t.Fatalf("restore without the data: %+v %v", res, err)
	}
	if got := filesBelow(t, filepath.Join(other.consoleDir, "data", "chat-files")); len(got) != 0 {
		t.Errorf("chat files restored without the console data: %q", got)
	}
}

// TK10: the ticket attachments go into the full backup and come back
// with the console data.
func TestFullBackupCarriesTheAttachments(t *testing.T) {
	ctx := context.Background()
	src := newInstall(t, "sqlite")
	attachments := map[string]string{"1731230000000-a1b2c3d4.pdf": "an attachment", "1731230000001-e5f6a7b8.png": "a picture"}
	writeFiles(t, filepath.Join(src.consoleDir, "data", "attachments"), attachments)
	archive, _, err := src.service(t, "x").CreateFullBackup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	files, err := ExtractTarGz(archive, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range attachments {
		if data, ok := files.Get("console/attachments/" + name); !ok || string(data) != content {
			t.Errorf("%s in the archive: %q %v", name, data, ok)
		}
	}

	dst := newInstall(t, "sqlite")
	res, err := dst.service(t, "x").RestoreFullBackup(ctx, archive, RestoreOptions{Database: true})
	if err != nil || len(res.Warnings) != 0 || !slices.Contains(res.Restored, "attachments (2 files)") {
		t.Fatalf("restore: %+v %v", res, err)
	}
	if got := filesBelow(t, filepath.Join(dst.consoleDir, "data", "attachments")); !maps.Equal(got, attachments) {
		t.Errorf("attachments after the restore: %q", got)
	}
	other := newInstall(t, "sqlite")
	if _, err := other.service(t, "x").RestoreFullBackup(ctx, archive, RestoreOptions{Uploads: true}); err != nil {
		t.Fatal(err)
	}
	if got := filesBelow(t, filepath.Join(other.consoleDir, "data", "attachments")); len(got) != 0 {
		t.Errorf("attachments restored without the console data: %q", got)
	}
}

// AV2: a restore that replaced the accounts or their pictures drops the
// list of who has a picture, so the list shows the restored accounts at
// once and not up to 30 seconds later.
func TestRestoreDropsTheListOfPictures(t *testing.T) {
	dialects(t, func(t *testing.T, dialect string) {
		ctx := context.Background()
		listed := func(av *avatars.Service) string {
			entries, err := av.ListDirectory(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, e := range entries {
				names = append(names, e.Username)
			}
			return strings.Join(names, ",")
		}
		withAvatars := func(in *install) (*Service, *avatars.Service) {
			av := avatars.New(avatars.Options{DB: in.db})
			s := in.snapshotService(t)
			s.o.Avatars = av
			return s, av
		}

		// a full backup of an install where nobody has a picture
		empty := newInstall(t, dialect)
		mustExec(t, empty.db, `INSERT INTO users (id, username, password_hash, role) VALUES (50, 'seed_admin', 'h', 'admin')`)
		archive, _, err := empty.service(t, "x").CreateFullBackup(ctx)
		if err != nil {
			t.Fatal(err)
		}
		in := newInstall(t, dialect)
		in.seed(t)
		s, av := withAvatars(in)
		if got := listed(av); got != "seed_admin" {
			t.Fatalf("pictures before the restore: %q", got)
		}
		if res, err := s.RestoreFullBackup(ctx, archive, RestoreOptions{Database: true}); err != nil || len(res.Warnings) != 0 {
			t.Fatalf("full restore: %+v %v", res, err)
		}
		if got := listed(av); got != "" {
			t.Errorf("pictures after the full restore: %q", got)
		}

		// a dump with one of the two tables only
		for _, c := range []struct{ tables, unwanted string }{
			{`{"user_avatars":[]}`, "seed_admin"},
			{`{"users":[{"id":50,"username":"renamed","password_hash":"h","role":"admin"}]}`, "seed_admin"},
		} {
			dump, err := CreateTarGz([]Entry{
				{Name: "manifest.json", Data: []byte(`{"_format":"betterdesk-full-backup","_version":1}`)},
				{Name: "console/data.json", Data: []byte(`{"tables":` + c.tables + `}`)},
			}, 1)
			if err != nil {
				t.Fatal(err)
			}
			in := newInstall(t, dialect)
			in.seed(t)
			s, av := withAvatars(in)
			if got := listed(av); got != "seed_admin" {
				t.Fatalf("pictures before the restore: %q", got)
			}
			if res, err := s.RestoreFullBackup(ctx, dump, RestoreOptions{Database: true}); err != nil || len(res.Warnings) != 0 {
				t.Fatalf("restore of %s: %+v %v", c.tables, res, err)
			}
			if got := listed(av); got == c.unwanted {
				t.Errorf("pictures after the restore of %s: %q", c.tables, got)
			}
		}

		// a snapshot that replaces the accounts
		in = newInstall(t, dialect)
		in.seed(t)
		s, av = withAvatars(in)
		if got := listed(av); got != "seed_admin" {
			t.Fatalf("pictures before the snapshot: %q", got)
		}
		snapshot := parsedJSON(t, `{"_format":"betterdesk-backup","_version":1,"console":{"users":[{"id":60,"username":"restored","password_hash":"h","role":"admin"}]}}`)
		if res := s.RestoreBackup(ctx, snapshot, SnapshotOptions{Users: true}); !slices.Contains(res.Restored, "users") {
			t.Fatalf("snapshot restore: %+v", res)
		}
		if got := listed(av); got != "" {
			t.Errorf("pictures after the snapshot: %q", got)
		}
	})
}

// BK2: a full backup on SQLite brings integers back exactly, those past
// 2^53 too, and a whole number bound into a text column stays "7".
func TestSQLiteNumbersComeBackAsTheyWere(t *testing.T) {
	ctx := context.Background()
	src := newInstall(t, "sqlite")
	mustExec(t, src.db, `INSERT INTO device_telemetry (device_id, memory_total_bytes, memory_used_bytes, cpu_usage_percent) VALUES ('d1', 9007199254740993, -9007199254740995, 2.0)`)
	archive, _, err := src.service(t, "x").CreateFullBackup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	files, err := ExtractTarGz(archive, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	dump, _ := files.Get("console/data.json")
	if !strings.Contains(string(dump), `"memory_used_bytes":"-9007199254740995","memory_total_bytes":"9007199254740993"`) {
		t.Errorf("the dump does not hold the integers exactly: %s", telemetryOf(string(dump)))
	}
	dst := newInstall(t, "sqlite")
	if res, err := dst.service(t, "x").RestoreFullBackup(ctx, archive, RestoreOptions{Database: true}); err != nil || len(res.Warnings) != 0 {
		t.Fatalf("restore: %+v %v", res, err)
	}
	want := snapshot(t, src.db)["device_telemetry"]
	if got := snapshot(t, dst.db)["device_telemetry"]; strings.Join(got, "\n") != strings.Join(want, "\n") || !strings.Contains(want[0], "|-9007199254740995|9007199254740993|") {
		t.Errorf("device_telemetry:\n got  %q\n want %q", got, want)
	}

	// numbers a dump holds for text columns
	parsed := parsedJSON(t, `{"tables":{"settings":[{"key":"whole","value":7},{"key":"fraction","value":7.5},{"key":"big","value":1e21},{"key":"negative","value":-3}]}}`)
	if _, err := ImportAllTables(ctx, dst.db, parsed); err != nil {
		t.Fatal(err)
	}
	rows, err := readStrings(ctx, dst.db, `SELECT key || '=' || value || ':' || typeof(value) FROM settings ORDER BY key`)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rows, ","); got != "big=1.0e+21:text,fraction=7.5:text,negative=-3:text,whole=7:text" {
		t.Errorf("settings: %s", got)
	}
}

// telemetryOf is the device_telemetry part of a dump, for a failure.
func telemetryOf(dump string) string {
	i := strings.Index(dump, `"device_telemetry":`)
	if i < 0 {
		return "(no device_telemetry)"
	}
	return dump[i:min(len(dump), i+300)]
}

func parsedJSON(t *testing.T, text string) any {
	t.Helper()
	v, err := branding.ParseJSON([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestGunzipLimit(t *testing.T) {
	archive, err := CreateTarGz([]Entry{{Name: "big", Data: bytes.Repeat([]byte("a"), 100000)}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ExtractTarGz(archive, Limits{MaxBytes: 50000}); err == nil || err.Error() != "Cannot create a Buffer larger than 50000 bytes" {
		t.Errorf("limit: %v", err)
	}
	if _, err := ExtractTarGz(archive, Limits{MaxBytes: 200000}); err != nil {
		t.Errorf("under the limit: %v", err)
	}
}

// A size field that leads back to the same header made the old console
// loop for ever; here it ends the read.
func TestExtractStopsOnALoop(t *testing.T) {
	tar, err := buildTar([]Entry{{Name: "a", Data: []byte("x")}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	copy(tar[124:136], "-1000\x00\x00\x00\x00\x00\x00\x00")
	sum := 0
	copy(tar[148:156], "        ")
	for _, b := range tar[:512] {
		sum += int(b)
	}
	writeOctal(tar, int64(sum), 148, 7)
	done := make(chan error, 1)
	go func() {
		_, err := ExtractTarGz(gzipBytes(t, tar), Limits{})
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrNoProgress) {
			t.Errorf("err %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the reader loops")
	}
}
