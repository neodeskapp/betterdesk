package backup

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// asOrdinaryRole opens the install's PostgreSQL database as a new role
// that is no superuser, with the rights on its tables an install's role
// has. Such a role cannot switch foreign keys off for a restore.
func (in *install) asOrdinaryRole(t *testing.T) *store.DB {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, in.dsn)
	if err != nil {
		t.Fatal(err)
	}
	role, password := randomName("bk_role_"), randomName("pw_")
	for _, q := range []string{
		`CREATE ROLE ` + role + ` LOGIN NOSUPERUSER PASSWORD '` + password + `'`,
		`GRANT USAGE ON SCHEMA public TO ` + role,
		`GRANT ALL ON ALL TABLES IN SCHEMA public TO ` + role,
		`GRANT ALL ON ALL SEQUENCES IN SCHEMA public TO ` + role,
	} {
		if _, err := admin.Exec(ctx, q); err != nil {
			admin.Close()
			t.Fatalf("%s: %v", strings.Fields(q)[0], err)
		}
	}
	u, err := url.Parse(in.dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(role, password)
	pool, err := pgxpool.New(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	db := store.OpenPostgres(pool)
	t.Cleanup(func() {
		_ = db.Close()
		pool.Close()
		c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = admin.Exec(c, `DROP OWNED BY `+role)
		_, _ = admin.Exec(c, `DROP ROLE `+role)
		admin.Close()
	})
	return db
}

func needPostgres(t *testing.T) {
	t.Helper()
	if os.Getenv(storetest.PostgresEnv) == "" {
		t.Skipf("%s is not set", storetest.PostgresEnv)
	}
}

// BK9: a restore by a role that cannot switch foreign keys off fills
// parents before their children and empties children before their
// parents, so no cascade takes restored rows and no child row waits for
// its parent; a table whose rows fail keeps the rows it had.
func TestFullRestoreOnPostgresKeepsChildRows(t *testing.T) {
	needPostgres(t)
	ctx := context.Background()
	in := newInstall(t, "postgres")
	mustExec(t, in.db, `INSERT INTO users (id, username, password_hash, role) VALUES (7, 'old', 'h', 'admin')`)
	mustExec(t, in.db, `INSERT INTO access_tokens (token, user_id, expires_at) VALUES ('old-token', 7, '2027-01-01 00:00:00+00')`)
	mustExec(t, in.db, `INSERT INTO organizations (id, name, slug) VALUES ('o-old', 'Old', 'old')`)
	mustExec(t, in.db, `INSERT INTO org_users (id, org_id, username, password_hash) VALUES ('u-old', 'o-old', 'a', 'h')`)
	mustExec(t, in.db, `INSERT INTO folders (id, name) VALUES (3, 'kept')`)
	db := in.asOrdinaryRole(t)

	dump := parsedJSON(t, `{"tables":{`+
		`"access_tokens":[{"token":"new-token","user_id":8,"expires_at":"2027-01-01T00:00:00.000Z"}],`+
		`"folders":[{"id":4,"name":null}],`+
		`"org_users":[{"id":"u-new","org_id":"o-new","username":"b","password_hash":"h"}],`+
		`"organizations":[{"id":"o-new","name":"New","slug":"new"}],`+
		`"users":[{"id":8,"username":"new","password_hash":"h","role":"admin"}]}}`)
	res, err := ImportAllTables(ctx, db, dump)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(res.Restored, ","); got != "access_tokens,org_users,organizations,users" {
		t.Errorf("restored %s", got)
	}
	if len(res.Warnings) != 1 || !strings.HasPrefix(res.Warnings[0], "Table folders: ") {
		t.Errorf("warnings %q", res.Warnings)
	}
	tables := snapshot(t, in.db)
	for table, want := range map[string]string{
		"access_tokens": "new-token",
		"org_users":     "u-new",
		"organizations": "o-new",
		"users":         "new",
		"folders":       "kept",
	} {
		rows := tables[table]
		if len(rows) != 1 || !strings.Contains(rows[0], want) {
			t.Errorf("%s: %q, want one row with %s", table, rows, want)
		}
	}
}

// BK9: a restore moves every sequence past the restored ids, so the next
// row a table gets has a free id.
func TestRestoreMovesSequences(t *testing.T) {
	needPostgres(t)
	ctx := context.Background()
	in := newInstall(t, "postgres")
	dump := parsedJSON(t, `{"tables":{"folders":[{"id":1,"name":"a"},{"id":41,"name":"b"}],"user_groups":[]}}`)
	if res, err := ImportAllTables(ctx, in.db, dump); err != nil || len(res.Warnings) != 0 {
		t.Fatalf("import: %+v %v", res, err)
	}
	var id int64
	if err := in.db.QueryRow(ctx, `INSERT INTO folders (name) VALUES ('next') RETURNING id`).Scan(&id); err != nil || id != 42 {
		t.Errorf("next folder: %d %v", id, err)
	}
	// an empty table leaves its sequence where it was
	if err := in.db.QueryRow(ctx, `INSERT INTO user_groups (guid, name) VALUES ('g', 'n') RETURNING id`).Scan(&id); err != nil || id < 2 {
		t.Errorf("user group after an empty restore: %d %v", id, err)
	}
}
