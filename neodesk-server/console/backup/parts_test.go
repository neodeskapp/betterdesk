package backup

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// A restore of one part puts back that part and nothing else, on SQLite
// and PostgreSQL. "Server device database" brings back the devices and
// leaves the console's accounts; on SQLite it put the whole shared file
// back at the next start, which also removed every account made after
// the backup (BUGS L401). "Console data" brings back the accounts and
// leaves the devices, which its dump of every table also replaced.
func TestRestoreOfOnePartLeavesTheOther(t *testing.T) {
	dialects(t, func(t *testing.T, dialect string) {
		in := newInstall(t, dialect)
		ctx := context.Background()
		mustExec(t, in.db, `INSERT INTO users (id, username, password_hash, role) VALUES (60, 'before', 'x', 'viewer')`)
		mustExec(t, in.db, `INSERT INTO peers (id, hostname) VALUES ('100000001', 'pc-before')`)
		s := in.service(t, "3.6.0")
		archive, _, err := s.CreateFullBackup(ctx)
		if err != nil {
			t.Fatal(err)
		}
		mustExec(t, in.db, `INSERT INTO users (id, username, password_hash, role) VALUES (61, 'after', 'x', 'viewer')`)
		mustExec(t, in.db, `INSERT INTO peers (id, hostname) VALUES ('100000002', 'pc-after')`)

		res, err := s.RestoreFullBackup(ctx, archive, RestoreOptions{GoDB: true})
		if err != nil || !slices.Contains(res.Restored, "goserver-db") {
			t.Fatalf("server part: %+v %v", res, err)
		}
		if slices.ContainsFunc(res.Restored, func(p string) bool { return strings.HasPrefix(p, "database") }) {
			t.Errorf("server part restored the console's data: %+v", res.Restored)
		}
		// the server's tables are back now; a restart makes the server read them
		if got := names(t, in.db, `SELECT id FROM peers ORDER BY id`); !slices.Equal(got, []string{"100000001"}) {
			t.Errorf("devices after the server part: %v, want the backup's", got)
		}
		if _, err := os.Stat(in.dbPath + RestoredSuffix); dialect == "sqlite" && err == nil {
			t.Error("a whole database file waits for the next start")
		}
		if got := names(t, in.db, `SELECT username FROM users WHERE id IN (60, 61) ORDER BY id`); !slices.Equal(got, []string{"before", "after"}) {
			t.Errorf("accounts after the server part: %v, want both", got)
		}

		mustExec(t, in.db, `INSERT INTO peers (id, hostname) VALUES ('100000003', 'pc-later')`)
		res, err = s.RestoreFullBackup(ctx, archive, RestoreOptions{Database: true})
		if err != nil || slices.Contains(res.Restored, "goserver-db") {
			t.Fatalf("console part: %+v %v", res, err)
		}
		if got := names(t, in.db, `SELECT username FROM users WHERE id IN (60, 61) ORDER BY id`); !slices.Equal(got, []string{"before"}) {
			t.Errorf("accounts after the console part: %v, want the backup's", got)
		}
		if got := names(t, in.db, `SELECT id FROM peers ORDER BY id`); !slices.Equal(got, []string{"100000001", "100000003"}) {
			t.Errorf("devices after the console part: %v, want them as they were", got)
		}
	})
}

// The server's own tables are what the signal server creates and the
// console's schema does not; a table added to either side has to be
// sorted into one part here.
func TestServerTablesAreTheSignalServersOwn(t *testing.T) {
	ctx := context.Background()
	both := storetest.SQLite(t)
	storetest.Prepare(t, both, storetest.GoServerFirst)
	console := storetest.Migrated(t)
	inBoth := names(t, both, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	ofConsole := names(t, console, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'`)
	var want []string
	for _, name := range inBoth {
		if !slices.Contains(ofConsole, name) && name != "panel_sessions" {
			want = append(want, name)
		}
	}
	slices.Sort(want)
	var got []string
	for name := range serverTables {
		got = append(got, name)
	}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Errorf("server tables\n%v\nthe signal server's own\n%v", got, want)
	}
	_ = ctx
}

func names(t *testing.T, db *store.DB, query string) []string {
	t.Helper()
	out, err := readStrings(context.Background(), db, query)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
