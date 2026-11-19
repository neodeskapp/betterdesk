package store_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// emptyLike returns a new empty database of the same kind as like.
func emptyLike(t *testing.T, like *store.DB) *store.DB {
	t.Helper()
	if like.IsPostgres() {
		return storetest.Postgres(t)
	}
	return storetest.SQLite(t)
}

var (
	freshCatalogsMu sync.Mutex
	freshCatalogs   = map[store.Dialect]catalog{}
)

// freshCatalog returns the catalog of a database of the same kind that
// Migrate set up from nothing. It is read once per kind and test run;
// callers get their own copy.
func freshCatalog(t *testing.T, like *store.DB) catalog {
	t.Helper()
	freshCatalogsMu.Lock()
	defer freshCatalogsMu.Unlock()
	c, ok := freshCatalogs[like.Dialect()]
	if !ok {
		fresh := emptyLike(t, like)
		mustMigrate(t, fresh)
		c = readCatalog(t, fresh)
		freshCatalogs[like.Dialect()] = c
	}
	return c.without()
}

// dropColumn takes a column away again, with the indexes on it, to get the
// table an older version had.
func dropColumn(t *testing.T, db *store.DB, table, column string) {
	t.Helper()
	if !db.IsPostgres() {
		for key, entry := range readCatalog(t, db).only([]string{table}) {
			if !strings.HasPrefix(key, "index:") {
				continue
			}
			columns := strings.Split(strings.TrimSuffix(strings.SplitN(strings.SplitN(entry, "columns=(", 2)[1], ")", 2)[0], ")"), ",")
			for _, c := range columns {
				if c == column {
					mustExec(t, db, `DROP INDEX "`+strings.TrimPrefix(key, "index:"+table+".")+`"`)
				}
			}
		}
	}
	mustExec(t, db, `ALTER TABLE "`+table+`" DROP COLUMN "`+column+`"`)
}

// Every column a later version added comes back when it is missing: with
// the definition a fresh install has, with its default in the rows that
// were there, and without touching the other columns. The three columns
// that come with a data fix (token_hash, slug, product_type) have their
// own tests.
func TestMigrateAddsEachMissingColumn(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		want := freshCatalog(t, db).loose()
		mustMigrate(t, db)
		fillTables(t, db, readCatalog(t, db).tables(), 2)
		added := 0
		for _, name := range store.MigrationStepNames(db.Dialect()) {
			target, ok := strings.CutPrefix(name, "add ")
			if !ok {
				continue
			}
			added++
			table, column, _ := strings.Cut(target, ".")
			if !db.IsPostgres() && sqliteCannotAdd[target] {
				continue
			}
			t.Run(target, func(t *testing.T) {
				dropColumn(t, db, table, column)
				shape, err := db.Inspect(testContext(t))
				if err != nil {
					t.Fatal(err)
				}
				if shape.Table(table).Has(column) {
					t.Fatalf("%s is still there after the drop", target)
				}
				var kept []string
				for _, c := range shape.Table(table).Columns() {
					kept = append(kept, c.Name)
				}
				rowsBefore := readRows(t, db, table, kept)

				mustMigrate(t, db)

				requireSameCatalog(t, "catalog with "+target+" added back", want, readCatalog(t, db).loose())
				if rowsAfter := readRows(t, db, table, kept); strings.Join(rowsAfter, "\n") != strings.Join(rowsBefore, "\n") {
					t.Errorf("rows of %s changed in their other columns:\n before %v\n after  %v", table, rowsBefore, rowsAfter)
				}
				col := db.Shape().Table(table).Column(column)
				if col.Type == store.TypeMissing {
					t.Fatalf("the shape after Migrate has no %s", target)
				}
				requireColumnDefault(t, db, table, col)
			})
		}
		if wantSteps := map[store.Dialect]int{store.SQLite: 37, store.Postgres: 29}[db.Dialect()]; added != wantSteps {
			t.Errorf("%d column steps on %s, want %d", added, db.Dialect(), wantSteps)
		}
	})
}

// sqliteCannotAdd are the two column steps that cannot work on SQLite: it
// refuses to add a column whose default is an expression. The Node console
// ran the same statement, caught the error and logged it at every start
// (BUGS.md D17).
var sqliteCannotAdd = map[string]bool{
	"settings.updated_at":        true,
	"branding_config.updated_at": true,
}

// On a SQLite table from before updated_at existed the column cannot be
// added. The step fails as it did in the Node console: a warning, the
// table as it was, and the migration goes on.
func TestMigrateCannotAddUpdatedAtOnSQLite(t *testing.T) {
	for target := range sqliteCannotAdd {
		table, column, _ := strings.Cut(target, ".")
		t.Run(target, func(t *testing.T) {
			db := storetest.SQLite(t)
			mustMigrate(t, db)
			mustExec(t, db, `INSERT INTO `+table+` (key, value) VALUES ('k', 'v')`)
			dropColumn(t, db, table, column)
			dropColumn(t, db, "device_groups", "tag_filter")

			var err error
			out := captureLog(t, func() { err = db.Migrate(testContext(t)) })
			if err != nil {
				t.Fatalf("Migrate: %v", err)
			}
			if !strings.Contains(out, "[DB] WARN: Migration add "+target+" error: ") || !strings.Contains(out, "non-constant default") {
				t.Errorf("no warning about the column SQLite cannot add:\n%s", out)
			}
			shape := db.Shape()
			if shape.Table(table).Has(column) {
				t.Errorf("%s was added; BUGS.md D17 and schema.md say it cannot be", target)
			}
			if !shape.Table("device_groups").Has("tag_filter") {
				t.Error("the steps after the failed one did not run")
			}
			if rows := queryStrings(t, db, `SELECT key, value FROM `+table); len(rows) != 1 || rows[0][1] != "v" {
				t.Errorf("rows of %s after the failed step = %v", table, rows)
			}
		})
	}
}

// requireColumnDefault checks that every row has the column's default in
// it, which is what ADD COLUMN gives the rows that were there.
func requireColumnDefault(t *testing.T, db *store.DB, table string, col store.Column) {
	t.Helper()
	def := col.Default
	if def == "" {
		def = "NULL"
	}
	var off string
	switch {
	case strings.Contains(strings.ToLower(def), "now"):
		off = fmt.Sprintf(`SELECT COUNT(*) FROM "%s" WHERE "%s" IS NULL`, table, col.Name)
	case db.IsPostgres():
		off = fmt.Sprintf(`SELECT COUNT(*) FROM "%s" WHERE "%s" IS DISTINCT FROM (%s)`, table, col.Name, def)
	default:
		off = fmt.Sprintf(`SELECT COUNT(*) FROM "%s" WHERE "%s" IS NOT (%s)`, table, col.Name, def)
	}
	if n := queryStrings(t, db, off)[0][0]; n != "0" {
		t.Errorf("%s rows of %s do not have the default %s in %s", n, table, def, col.Name)
	}
	if n := queryStrings(t, db, `SELECT COUNT(*) FROM "`+table+`"`)[0][0]; n == "0" {
		t.Fatalf("fixture: %s is empty", table)
	}
}

// sha256 of "abc", the standard test vector.
const sha256OfABC = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"

func insertUser(t *testing.T, db *store.DB, name string) int64 {
	t.Helper()
	id, err := db.Insert(testContext(t), `INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)`, name, "hash", "admin")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	return id
}

// Tokens stored before the hash column existed get their hash: when the
// column is missing and when it is there but empty. A hash that is there
// stays, and so does a token without text.
func TestMigrateHashesAccessTokens(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		mustMigrate(t, db)
		for _, missingColumn := range []bool{true, false} {
			t.Run(fmt.Sprintf("column missing %v", missingColumn), func(t *testing.T) {
				mustExec(t, db, `DELETE FROM users`)
				user := insertUser(t, db, "token-owner")
				expires := "2030-01-01 00:00:00"
				insert := `INSERT INTO access_tokens (token, token_hash, user_id, expires_at) VALUES (?, ?, ?, ?)`
				mustExec(t, db, insert, "abc", nil, user, expires)
				mustExec(t, db, insert, "token-2", "", user, expires)
				mustExec(t, db, insert, "token-3", "already-hashed", user, expires)
				mustExec(t, db, insert, "", nil, user, expires)
				want := map[string]string{"abc": sha256OfABC, "token-2": store.HashAccessToken("token-2"), "token-3": "already-hashed", "": "NULL"}
				if missingColumn {
					dropColumn(t, db, "access_tokens", "token_hash")
					want["token-3"] = store.HashAccessToken("token-3")
				}

				for run := 1; run <= 2; run++ {
					mustMigrate(t, db)
					rows := queryStrings(t, db, `SELECT token, token_hash FROM access_tokens`)
					if len(rows) != 4 {
						t.Fatalf("run %d: %d tokens, want 4", run, len(rows))
					}
					for _, r := range rows {
						if r[1] != want[r[0]] {
							t.Errorf("run %d: hash of token %q = %s, want %s", run, r[0], r[1], want[r[0]])
						}
					}
				}
				index := readCatalog(t, db)["index:access_tokens.idx_access_tokens_hash"]
				if !strings.Contains(strings.ToLower(index), "unique") || !strings.Contains(index, "token_hash IS NOT NULL") {
					t.Errorf("idx_access_tokens_hash = %q, want the partial unique index", index)
				}
			})
		}
	})
}

type backfillVectors struct {
	Slugify []struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	} `json:"slugify"`
	// Each scenario is a table of bundles before the migration and the slug
	// every bundle has after it.
	Scenarios []struct {
		Name string `json:"name"`
		// ColumnMissing: the table has no slug column yet.
		ColumnMissing bool `json:"column_missing"`
		Rows          []struct {
			BundleID string  `json:"bundle_id"`
			Name     string  `json:"name"`
			Slug     *string `json:"slug"`
		} `json:"rows"`
		Want map[string]string `json:"want"`
	} `json:"scenarios"`
	TokenHashes map[string]string `json:"token_hashes"`
}

// readBackfillVectors reads what the Node console's own code gave for a
// list of inputs (testdata/nodeschema/vectors.js writes the file).
func readBackfillVectors(t *testing.T) backfillVectors {
	t.Helper()
	var v backfillVectors
	if err := json.Unmarshal([]byte(readTestdata(t, "vectors/backfills.json")), &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Slugify) < 10 || len(v.Scenarios) < 2 || len(v.TokenHashes) < 3 {
		t.Fatalf("vectors/backfills.json is incomplete: %d names, %d scenarios, %d hashes", len(v.Slugify), len(v.Scenarios), len(v.TokenHashes))
	}
	return v
}

// The slug of a name and the hash of a token are what the Node console
// computed for the same input.
func TestSlugAndTokenHashMatchNode(t *testing.T) {
	v := readBackfillVectors(t)
	for _, c := range v.Slugify {
		if got := store.SlugifyBundleName(c.Name); got != c.Slug {
			t.Errorf("SlugifyBundleName(%q) = %q, Node gave %q", c.Name, got, c.Slug)
		}
	}
	for token, want := range v.TokenHashes {
		if got := store.HashAccessToken(token); got != want {
			t.Errorf("HashAccessToken(%q) = %q, Node gave %q", token, got, want)
		}
	}
	if got := store.HashAccessToken("abc"); got != sha256OfABC {
		t.Errorf("HashAccessToken(abc) = %s", got)
	}
}

// Bundles from before the slug column get the slug the Node console's
// migration gave the same rows: from the name, numbered when taken, from
// the bundle id when the name gives nothing. Slugs that exist stay.
func TestMigrateGivesAgentBundlesSlugs(t *testing.T) {
	v := readBackfillVectors(t)
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		mustMigrate(t, db)
		for _, scenario := range v.Scenarios {
			t.Run(scenario.Name, func(t *testing.T) {
				mustExec(t, db, `DELETE FROM agent_bundles`)
				for _, b := range scenario.Rows {
					var slug any
					if b.Slug != nil {
						slug = *b.Slug
					}
					mustExec(t, db, `INSERT INTO agent_bundles (bundle_id, name, slug) VALUES (?, ?, ?)`, b.BundleID, b.Name, slug)
				}
				if scenario.ColumnMissing {
					dropColumn(t, db, "agent_bundles", "slug")
				}

				for run := 1; run <= 2; run++ {
					mustMigrate(t, db)
					rows := queryStrings(t, db, `SELECT bundle_id, slug FROM agent_bundles`)
					if len(rows) != len(scenario.Want) || len(rows) < 4 {
						t.Fatalf("run %d: %d bundles, want %d", run, len(rows), len(scenario.Want))
					}
					for _, r := range rows {
						if r[1] != scenario.Want[r[0]] {
							t.Errorf("run %d: slug of bundle %s = %q, Node gave %q", run, r[0], r[1], scenario.Want[r[0]])
						}
					}
				}
				index := readCatalog(t, db)["index:agent_bundles.idx_agent_bundles_slug"]
				if !strings.Contains(strings.ToLower(index), "unique") {
					t.Errorf("idx_agent_bundles_slug = %q, want a unique index", index)
				}
			})
		}
	})
}

// Product types of older versions become the one current product. On
// PostgreSQL a column from an older version also gets NOT NULL and the
// current default; SQLite cannot change a column, so only the rows change
// there.
func TestMigrateFixesAgentBundleProductTypes(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		mustMigrate(t, db)
		if db.IsPostgres() {
			mustExec(t, db, `ALTER TABLE agent_bundles ALTER COLUMN product_type DROP NOT NULL`)
			mustExec(t, db, `ALTER TABLE agent_bundles ALTER COLUMN product_type SET DEFAULT 'support-agent'`)
		} else {
			dropColumn(t, db, "agent_bundles", "product_type")
			mustExec(t, db, `ALTER TABLE agent_bundles ADD COLUMN product_type TEXT DEFAULT 'agent'`)
		}
		for i, value := range []any{"agent", "rdclient", " Support-Agent ", "betterdesk-support", "something else", nil, ""} {
			mustExec(t, db, `INSERT INTO agent_bundles (bundle_id, name, slug, product_type) VALUES (?, ?, ?, ?)`,
				fmt.Sprintf("bundle-%d", i), fmt.Sprintf("Bundle %d", i), fmt.Sprintf("bundle-%d", i), value)
		}

		for run := 1; run <= 2; run++ {
			mustMigrate(t, db)
			rows := queryStrings(t, db, `SELECT product_type FROM agent_bundles`)
			if len(rows) != 7 {
				t.Fatalf("run %d: %d bundles, want 7", run, len(rows))
			}
			for _, r := range rows {
				if r[0] != "betterdesk-support" {
					t.Errorf("run %d: product_type = %q, want betterdesk-support", run, r[0])
				}
			}
			col := db.Shape().Table("agent_bundles").Column("product_type")
			if db.IsPostgres() {
				if !col.NotNull || !strings.Contains(col.Default, "betterdesk-support") {
					t.Errorf("run %d: product_type column = %+v, want NOT NULL with the current default", run, col)
				}
			} else if col.NotNull || col.Default != "'agent'" {
				t.Errorf("run %d: product_type column = %+v, want it as the old version made it", run, col)
			}
		}
	})
}

// The column is added with its final definition when it is missing, and
// the rows that were there get the default.
func TestMigrateAddsAgentBundleProductType(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		mustMigrate(t, db)
		mustExec(t, db, `INSERT INTO agent_bundles (bundle_id, name, slug) VALUES ('b1', 'One', 'one')`)
		dropColumn(t, db, "agent_bundles", "product_type")
		mustMigrate(t, db)
		if rows := queryStrings(t, db, `SELECT product_type FROM agent_bundles`); len(rows) != 1 || rows[0][0] != "betterdesk-support" {
			t.Errorf("product_type after the column was added = %v", rows)
		}
		col := db.Shape().Table("agent_bundles").Column("product_type")
		if !col.NotNull || !strings.Contains(col.Default, "betterdesk-support") {
			t.Errorf("product_type column = %+v", col)
		}
	})
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// An empty group table gets its Default group. A table with any row is
// left alone, also when none of the rows is called Default.
func TestMigrateSeedsDefaultGroups(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		mustMigrate(t, db)
		notes := map[string]string{"user_groups": "Default user group", "device_groups": "Default device group"}
		for table, note := range notes {
			rows := queryStrings(t, db, `SELECT guid, name, note FROM `+table)
			if len(rows) != 1 || rows[0][1] != "Default" || rows[0][2] != note || !uuidPattern.MatchString(rows[0][0]) {
				t.Errorf("%s after the first Migrate = %v, want one Default row with a random guid and note %q", table, rows, note)
			}
			mustExec(t, db, `DELETE FROM `+table)
			mustExec(t, db, `INSERT INTO `+table+` (guid, name) VALUES ('own-guid', 'Own group')`)
		}
		mustMigrate(t, db)
		for table := range notes {
			if rows := queryStrings(t, db, `SELECT name FROM `+table); len(rows) != 1 || rows[0][0] != "Own group" {
				t.Errorf("%s with a row of its own after Migrate = %v, want it untouched", table, rows)
			}
			mustExec(t, db, `DELETE FROM `+table)
		}
		mustMigrate(t, db)
		for table := range notes {
			if rows := queryStrings(t, db, `SELECT name FROM `+table); len(rows) != 1 || rows[0][0] != "Default" {
				t.Errorf("%s emptied and migrated = %v, want the Default row again", table, rows)
			}
		}
	})
}

// Two processes that start at once end with one schema and one of each
// default row, and neither fails.
func TestMigrateConcurrently(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		handles := []*store.DB{db, db}
		if !db.IsPostgres() {
			// A second process is a second handle on the same file.
			path := queryStrings(t, db, `SELECT file FROM pragma_database_list WHERE name = 'main'`)[0][0]
			for i := 0; i < 2; i++ {
				other, err := store.OpenSQLite(path)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = other.Close() })
				handles = append(handles, other)
			}
		} else {
			handles = append(handles, db, db)
		}

		var wg sync.WaitGroup
		errs := make([]error, len(handles))
		for i, h := range handles {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs[i] = h.Migrate(testContext(t))
			}()
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Errorf("migration %d: %v", i, err)
			}
		}
		requireSameCatalog(t, "catalog after concurrent migrations", freshCatalog(t, db), readCatalog(t, db))
		for _, table := range []string{"user_groups", "device_groups"} {
			if rows := queryStrings(t, db, `SELECT name FROM `+table); len(rows) != 1 {
				t.Errorf("%s has %d rows after concurrent migrations, want 1", table, len(rows))
			}
		}
	})
}

// On PostgreSQL Migrate waits for the advisory lock of its schema, so a
// second process does nothing until the first is done.
func TestMigrateWaitsForTheSchemaLock(t *testing.T) {
	db := storetest.Postgres(t)
	ctx := testContext(t)
	release := make(chan struct{})
	held := make(chan error, 1)
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- db.InTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Exec(ctx, `SELECT pg_advisory_lock(?, hashtext(current_schema()))`, store.AdvisoryLockClass)
			held <- err
			if err != nil {
				return err
			}
			<-release
			_, err = tx.Exec(ctx, `SELECT pg_advisory_unlock(?, hashtext(current_schema()))`, store.AdvisoryLockClass)
			return err
		})
	}()
	if err := <-held; err != nil {
		t.Fatalf("take the lock: %v", err)
	}

	migrated := make(chan error, 1)
	go func() { migrated <- db.Migrate(ctx) }()
	select {
	case err := <-migrated:
		t.Fatalf("Migrate returned (%v) while another session held the schema lock", err)
	case <-time.After(500 * time.Millisecond):
	}
	if tables := readCatalog(t, db).tables(); len(tables) != 0 {
		t.Errorf("Migrate created %d tables while it should have been waiting", len(tables))
	}

	close(release)
	if err := <-holderDone; err != nil {
		t.Fatalf("lock holder: %v", err)
	}
	if err := <-migrated; err != nil {
		t.Fatalf("Migrate after the lock was released: %v", err)
	}
	if tables := readCatalog(t, db).tables(); len(tables) != 55 {
		t.Errorf("%d tables after Migrate, want 55", len(tables))
	}
}

// captureLog collects what the migration logs while fn runs.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(previous)
	fn()
	return buf.String()
}

// Migrate says what it did, one line per change in the console's form, and
// says nothing when there was nothing to do.
func TestMigrateReportsWhatItDid(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		first := captureLog(t, func() { mustMigrate(t, db) })
		for _, want := range []string{
			"[DB] Migration: created 55 tables: peer, ",
			"[DB] Migration: added users.first_name\n",
			"[DB] Migration: added the default row to user_groups\n",
		} {
			if !strings.Contains(first, want) {
				t.Errorf("log of the first Migrate lacks %q:\n%s", want, first)
			}
		}
		if again := captureLog(t, func() { mustMigrate(t, db) }); again != "" {
			t.Errorf("a Migrate with nothing to do logged:\n%s", again)
		}

		user := insertUser(t, db, "u")
		mustExec(t, db, `INSERT INTO access_tokens (token, user_id, expires_at) VALUES ('t1', ?, '2030-01-01 00:00:00')`, user)
		mustExec(t, db, `INSERT INTO agent_bundles (bundle_id, name) VALUES ('b1', 'One')`)
		dropColumn(t, db, "settings", "updated_at")
		fixes := captureLog(t, func() { mustMigrate(t, db) })
		for _, want := range []string{
			"[DB] Migration: added settings.updated_at\n",
			"[DB] Migration: hashed 1 access tokens\n",
			"[DB] Migration: gave 1 agent bundles a slug\n",
		} {
			if !strings.Contains(fixes, want) {
				t.Errorf("log lacks %q:\n%s", want, fixes)
			}
		}
		if strings.Contains(fixes, "created") {
			t.Errorf("log reports created tables on a database that had them all:\n%s", fixes)
		}
	})
}

// A step the console could live without logs a warning and the migration
// goes on. A step it cannot live without stops the migration with an error
// that names the step.
func TestMigrateStepFailures(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		mustMigrate(t, db)
		// Two bundles with one slug: the unique index cannot be made.
		mustExec(t, db, `DROP INDEX idx_agent_bundles_slug`)
		mustExec(t, db, `INSERT INTO agent_bundles (bundle_id, name, slug) VALUES ('b1', 'One', 'same'), ('b2', 'Two', 'same')`)
		dropColumn(t, db, "settings", "updated_at")
		var err error
		out := captureLog(t, func() { err = db.Migrate(testContext(t)) })
		if err != nil {
			t.Fatalf("Migrate with a failing tolerated step: %v", err)
		}
		if !strings.Contains(out, "[DB] WARN: Migration agent_bundles.slug error: ") {
			t.Errorf("no warning about the failed step:\n%s", out)
		}
		if !db.Shape().Table("settings").Has("updated_at") {
			t.Error("the steps after the failed one did not run")
		}

		// Two tokens with one hash: the migration cannot go on.
		user := insertUser(t, db, "u")
		mustExec(t, db, `DROP INDEX idx_access_tokens_hash`)
		mustExec(t, db, `INSERT INTO access_tokens (token, token_hash, user_id, expires_at) VALUES ('t1', 'same', ?, '2030-01-01 00:00:00'), ('t2', 'same', ?, '2030-01-01 00:00:00')`, user, user)
		err = db.Migrate(testContext(t))
		if err == nil || !strings.Contains(err.Error(), "migrate console schema: hash access tokens: ") {
			t.Errorf("Migrate with a failing required step = %v, want an error naming the step", err)
		}
	})
}

// A cancelled context stops the migration, also in a tolerated step.
func TestMigrateStopsWhenCancelled(t *testing.T) {
	db := storetest.SQLite(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := db.Migrate(ctx); err == nil {
		t.Fatal("Migrate with a cancelled context returned nil")
	}
}

func TestSplitStatements(t *testing.T) {
	got := store.SplitStatements("-- a comment; with a semicolon\nCREATE TABLE a (\n    x TEXT DEFAULT ';--'\n);\n\nCREATE INDEX i ON a (x) WHERE x != '';\n  \n")
	want := []string{"CREATE TABLE a (\n    x TEXT DEFAULT ';--'\n)", "CREATE INDEX i ON a (x) WHERE x != ''"}
	if len(got) != len(want) {
		t.Fatalf("SplitStatements gave %d statements %q, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("statement %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// schema.md lists every step, and no step that is gone.
func TestSchemaDocListsEveryStep(t *testing.T) {
	text := readFile(t, "schema.md")
	inDoc := map[string]bool{}
	for _, m := range regexp.MustCompile("(?m)^\\| *`([^`]+)` *\\|").FindAllStringSubmatch(text, -1) {
		inDoc[m[1]] = true
	}
	inCode := map[string]bool{}
	for _, dialect := range []store.Dialect{store.SQLite, store.Postgres} {
		for _, name := range store.MigrationStepNames(dialect) {
			inCode[name] = true
			if !inDoc[name] && !strings.HasPrefix(name, "create ") {
				t.Errorf("schema.md has no row for step %q (%s)", name, dialect)
			}
		}
	}
	var gone []string
	for name := range inDoc {
		if !inCode[name] && (strings.HasPrefix(name, "add ") || strings.HasPrefix(name, "seed ") || strings.HasPrefix(name, "run ")) {
			gone = append(gone, name)
		}
	}
	sort.Strings(gone)
	if len(gone) > 0 {
		t.Errorf("schema.md lists steps that Migrate does not run: %v", gone)
	}
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	return readTestdata(t, filepath.Join("..", name))
}

// A tolerated step that fails takes nothing else down with it: the tables
// that come right after it are still created. Here peer already has the
// column under another spelling, which SQLite refuses as a duplicate.
func TestMigrateToleratedFailureKeepsItsNeighbours(t *testing.T) {
	db := storetest.SQLite(t)
	mustExec(t, db, `CREATE TABLE peer (id TEXT PRIMARY KEY, uuid TEXT, pk BLOB, note TEXT, created_at TEXT, status_online INTEGER,
		last_online TEXT, is_deleted INTEGER, info TEXT, ip TEXT, "user" TEXT, is_banned INTEGER, banned_at TEXT,
		banned_reason TEXT, folder_id INTEGER, TAGS TEXT)`)
	var err error
	out := captureLog(t, func() { err = db.Migrate(testContext(t)) })
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !strings.Contains(out, "[DB] WARN: Migration add peer.tags error: ") {
		t.Errorf("no warning about peer.tags:\n%s", out)
	}
	shape := db.Shape()
	if n := len(shape.Tables()); n != 55 {
		t.Errorf("%d tables after a tolerated failure, want all 55", n)
	}
	for _, table := range []string{"device_inventory", "dlp_events", "users"} {
		if !shape.Table(table).Exists() {
			t.Errorf("%s was not created", table)
		}
	}
}
