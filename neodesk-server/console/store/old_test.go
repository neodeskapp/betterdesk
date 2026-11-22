package store_test

import (
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// oldVersions are console releases whose schema, made by that release's own
// start code, is kept in testdata/old.
var oldVersions = []string{"v3.0.0", "v3.4.0", "v3.5.200"}

// keepsOldProductTypeDefault names the installs on which one column keeps a
// definition a fresh install does not have: SQLite cannot change the
// default of a column, and the consoles from 3.3.21 to 3.5.15 created
// agent_bundles.product_type with the default 'agent'. The rows are fixed;
// the Node console left the column like this too.
var keepsOldProductTypeDefault = map[string]bool{"v3.4.0/sqlite": true}

// An install that last ran an older console is brought to the current
// schema in place: every row is still there with what it held, the tables
// and columns are those of a fresh install, and a second run changes
// nothing.
func TestMigrateOldInstalls(t *testing.T) {
	for _, version := range oldVersions {
		t.Run(version, func(t *testing.T) {
			storetest.Each(t, func(t *testing.T, db *store.DB) {
				ctx := testContext(t)
				storetest.ApplyDump(t, db, readTestdata(t, "old/"+version+"-"+db.Dialect().String()+".sql"))
				oldTables := readCatalog(t, db).tables()
				fillTables(t, db, oldTables, 3)

				oldShape, err := db.Inspect(ctx)
				if err != nil {
					t.Fatal(err)
				}
				oldColumns := map[string][]string{}
				rowsBefore := snapshot{}
				for _, table := range oldTables {
					for _, col := range oldShape.Table(table).Columns() {
						oldColumns[table] = append(oldColumns[table], col.Name)
					}
					rowsBefore[table] = readRows(t, db, table, oldColumns[table])
					if len(rowsBefore[table]) != 3 {
						t.Fatalf("fixture: %s has %d rows, want 3", table, len(rowsBefore[table]))
					}
				}

				mustMigrate(t, db)

				// Each access token is kept as its hash only (BUGS RV128)
				if rows, ok := rowsBefore["access_tokens"]; ok {
					rowsBefore["access_tokens"] = keptAsHashes(rows, oldColumns["access_tokens"])
				}
				rowsAfter := snapshot{}
				for _, table := range oldTables {
					rowsAfter[table] = readRows(t, db, table, oldColumns[table])
				}
				requireSameSnapshot(t, "rows of the "+version+" install in their old columns", rowsBefore, rowsAfter)

				want := freshCatalog(t, db).loose()
				got := readCatalog(t, db).without(sessionTable).loose()
				if keepsOldProductTypeDefault[version+"/"+db.Dialect().String()] {
					const key = "column:agent_bundles.product_type"
					if !strings.Contains(got[key], "default='agent'") {
						t.Errorf("%s = %q, want the default 'agent' this version gave the column", key, got[key])
					}
					got[key] = want[key]
				}
				requireSameCatalog(t, version+" install after Migrate against a fresh install", want, got)

				tables := readCatalog(t, db).tables()
				catalogAfter := readCatalog(t, db)
				allRows := readSnapshot(t, db, tables)
				mustMigrate(t, db)
				requireSameCatalog(t, "catalog after the second Migrate", catalogAfter, readCatalog(t, db))
				requireSameSnapshot(t, "rows after the second Migrate", allRows, readSnapshot(t, db, tables))

				// The data fixes that go with added columns ran on the old rows.
				slugs := map[string]bool{}
				for _, r := range queryStrings(t, db, `SELECT COALESCE(slug, ''), product_type FROM agent_bundles`) {
					if r[0] == "" || slugs[r[0]] {
						t.Errorf("agent bundle slug %q is empty or taken", r[0])
					}
					slugs[r[0]] = true
					if r[1] != "betterdesk-support" {
						t.Errorf("agent bundle product type = %q", r[1])
					}
				}
				if len(slugs) != 3 && strings.Contains(strings.Join(oldTables, ","), "agent_bundles") {
					t.Errorf("agent bundles after Migrate: %d slugs, want 3", len(slugs))
				}
			})
		})
	}
}

// keptAsHashes is how rows of access_tokens read after Migrate, given as
// readRows gave them with these columns: the token column holds "sha256 "
// and the hash of the token, and token_hash, where the table has it, the
// hash (BUGS RV128).
func keptAsHashes(rows, columns []string) []string {
	token, hash := slices.Index(columns, "token"), slices.Index(columns, "token_hash")
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		values := strings.Split(row, " | ")
		sum := store.HashAccessToken(values[token])
		values[token] = store.StoredAccessToken(sum)
		if hash >= 0 {
			values[hash] = sum
		}
		out = append(out, strings.Join(values, " | "))
	}
	sort.Strings(out)
	return out
}
