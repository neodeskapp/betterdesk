package main

import (
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// testdata/rows.json.gz holds the rows the Node seed wrote straight into
// the database at six times (testdata/nodeoracle/rows.js). The tests seed
// a fresh database at each time and compare what is in the tables, on
// SQLite as the Node seed left it and on PostgreSQL in that database's
// forms.

type oracleRow struct {
	Table string         `json:"table"`
	Row   map[string]any `json:"row"`
}

type oracleCase struct {
	Now     float64     `json:"now"`
	Users   [][2]any    `json:"users"`
	Rows    []oracleRow `json:"rows"`
	Updates []struct {
		SQL    string `json:"sql"`
		Params []any  `json:"params"`
	} `json:"updates"`
}

func loadOracle(t *testing.T) []oracleCase {
	t.Helper()
	f, err := os.Open("testdata/rows.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		Cases []oracleCase `json:"cases"`
	}
	if err := json.NewDecoder(gz).Decode(&data); err != nil {
		t.Fatal(err)
	}
	if len(data.Cases) == 0 {
		t.Fatal("no cases in testdata/rows.json.gz")
	}
	return data.Cases
}

// oracleContracts are the contracts rows.js gave the billing part.
var oracleContracts = []struct {
	org, id, pkg             string
	included, rate, rounding float64
}{
	{"northwind", "contract-northwind", "pkg-business", 1200, 48, 10},
	{"harbor", "contract-harbor", "pkg-basic", 300, 60, 15},
	{"lab", "contract-lab", "pkg-payg", 0, 75, 5},
}

// oracleBilling is what the API part hands the billing part in rows.js:
// the organizations' ids and the contracts with their packages, as the
// console's answers have them.
func oracleBilling() (map[string]any, []contractTerms) {
	orgIDs := map[string]any{}
	var made []contractTerms
	for _, k := range oracleContracts {
		orgIDs[k.org] = "org-" + k.org
		made = append(made, contractTerms{Org: k.org, ID: k.id, Pkg: termsOf(map[string]any{
			"id": k.pkg, "currency": "EUR", "included_minutes": k.included, "overage_rate": k.rate, "rounding_minutes": k.rounding,
		})})
	}
	return orgIDs, made
}

// seedDirect runs the direct parts of the demo seed at the case's time on
// a fresh database shaped as the stack has it: the signal server's tables
// first, then the console's.
func seedDirect(t *testing.T, db *store.DB, c oracleCase) map[string]int {
	t.Helper()
	ctx := context.Background()
	storetest.Prepare(t, db, storetest.GoServerFirst)
	if _, err := session.New(ctx, session.Options{DB: db, Secret: "test-secret"}); err != nil {
		t.Fatal(err)
	}
	for _, u := range c.Users {
		if _, err := db.Exec(ctx, `INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, 'x', 'admin')`, int64(u[0].(float64)), u[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range oracleContracts {
		if _, err := db.Exec(ctx, `INSERT INTO billing_packages (id, name) VALUES (?, ?)`, k.pkg, k.pkg); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO billing_contracts (id, target_type, target_key, package_id) VALUES (?, 'org', ?, ?)`, k.id, k.org, k.pkg); err != nil {
			t.Fatal(err)
		}
	}
	orgIDs, made := oracleBilling()

	before := map[string]int{}
	for _, r := range c.Rows {
		if _, seen := before[r.Table]; !seen {
			before[r.Table] = countRows(t, db, r.Table)
		}
	}
	w, err := newWriter(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	d := newDemo(time.UnixMilli(int64(c.Now)))
	if err := d.seedGoDB(ctx, w, io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := d.seedAuthDB(ctx, w, "test", io.Discard); err != nil {
		t.Fatal(err)
	}
	if err := d.seedBillingSessions(ctx, w, orgIDs, made, io.Discard); err != nil {
		t.Fatal(err)
	}
	return before
}

func countRows(t *testing.T, db *store.DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(context.Background(), `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// tableRows reads a table's rows in the order they were written, each
// column as the database gives it back: on SQLite through quote(), so
// text, integers and reals keep their kind; on PostgreSQL as row_to_json
// with the column types.
func tableRows(t *testing.T, db *store.DB, table string, skip int) ([]map[string]any, map[string]string) {
	t.Helper()
	ctx := context.Background()
	types := map[string]string{}
	var out []map[string]any
	if db.IsPostgres() {
		rows, err := db.Query(ctx, `SELECT column_name, data_type FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = ?`, table)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var name, kind string
			if err := rows.Scan(&name, &kind); err != nil {
				t.Fatal(err)
			}
			types[name] = kind
		}
		rows.Close()
		// A serial id gives the order of the writes; ctid does only while
		// every row fits the page the last one went to.
		order := "ctid"
		if _, ok := types["id"]; ok && (types["id"] == "integer" || types["id"] == "bigint") {
			order = "id"
		}
		data, err := db.Query(ctx, `SELECT row_to_json(x)::text FROM `+table+` x ORDER BY `+order)
		if err != nil {
			t.Fatal(err)
		}
		defer data.Close()
		for data.Next() {
			var text string
			if err := data.Scan(&text); err != nil {
				t.Fatal(err)
			}
			var row map[string]any
			if err := json.Unmarshal([]byte(text), &row); err != nil {
				t.Fatal(err)
			}
			out = append(out, row)
		}
	} else {
		cols, err := db.Query(ctx, `SELECT name FROM pragma_table_info(?) ORDER BY cid`, table)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for cols.Next() {
			var name string
			if err := cols.Scan(&name); err != nil {
				t.Fatal(err)
			}
			names = append(names, name)
		}
		cols.Close()
		exprs := make([]string, len(names))
		for i, n := range names {
			exprs[i] = `quote("` + n + `")`
		}
		data, err := db.Query(ctx, `SELECT `+strings.Join(exprs, ", ")+` FROM `+table+` ORDER BY rowid`)
		if err != nil {
			t.Fatal(err)
		}
		defer data.Close()
		for data.Next() {
			vals := make([]string, len(names))
			ptrs := make([]any, len(names))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := data.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			row := map[string]any{}
			for i, n := range names {
				row[n] = sqlLiteral(vals[i])
			}
			out = append(out, row)
		}
	}
	if skip > len(out) {
		t.Fatalf("%s lost rows", table)
	}
	return out[skip:], types
}

// sqlLiteral reads what quote() wrote: 'text', NULL, an integer, a real.
func sqlLiteral(s string) any {
	switch {
	case s == "NULL":
		return nil
	case strings.HasPrefix(s, "'"):
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
	case strings.ContainsAny(s, ".eE"):
		f, _ := strconv.ParseFloat(s, 64)
		return realValue(f)
	}
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// realValue is a SQLite REAL, kept apart from an INTEGER.
type realValue float64

// sameAsNode compares a value read back with the one the Node seed passed.
// SQLite: the text, the number (an integer column holds a whole number as
// an integer). PostgreSQL: by the column's type, as DECISIONS D3 has the
// forms.
func sameAsNode(node, got any, pgType string) bool {
	if lit, ok := node.(map[string]any); ok && lit["sql"] != nil {
		return got != nil // datetime('now') or NOW(): the database's clock
	}
	if pgType == "" {
		switch n := node.(type) {
		case nil:
			return got == nil
		case string:
			return got == n
		case float64:
			switch g := got.(type) {
			case int64:
				return float64(g) == n
			case realValue:
				return float64(g) == n
			}
		}
		return false
	}
	switch {
	case strings.HasPrefix(pgType, "timestamp"):
		if node == nil || node == "" {
			return got == nil
		}
		var want time.Time
		switch n := node.(type) {
		case string:
			var ok bool
			if want, ok = store.ParseTime(n); !ok {
				return false
			}
		case float64:
			want = time.UnixMilli(int64(n))
		}
		g, ok := got.(string)
		if !ok {
			return false
		}
		have, err := time.Parse(time.RFC3339Nano, g)
		return err == nil && have.Equal(want)
	case pgType == "boolean":
		g, ok := got.(bool)
		return ok && g == (node == 1.0)
	case pgType == "jsonb" || pgType == "json":
		var want any
		if err := json.Unmarshal([]byte(node.(string)), &want); err != nil {
			return false
		}
		return reflect.DeepEqual(want, got)
	case pgType == "real":
		n, ok1 := node.(float64)
		g, ok2 := got.(float64)
		return ok1 && ok2 && float32(n) == float32(g)
	}
	switch n := node.(type) {
	case nil:
		return got == nil
	case string:
		return got == n
	case float64:
		g, ok := got.(float64)
		return ok && g == n
	}
	return false
}

func checkAgainstNode(t *testing.T, db *store.DB, c oracleCase, before map[string]int) {
	t.Helper()
	want := map[string][]map[string]any{}
	var order []string
	for _, r := range c.Rows {
		if _, seen := want[r.Table]; !seen {
			order = append(order, r.Table)
		}
		want[r.Table] = append(want[r.Table], r.Row)
	}
	for _, table := range order {
		got, types := tableRows(t, db, table, before[table])
		if len(got) != len(want[table]) {
			t.Errorf("%s: %d rows, the Node seed wrote %d", table, len(got), len(want[table]))
			continue
		}
		bad := 0
		for i, w := range want[table] {
			for col, nv := range w {
				if !sameAsNode(nv, got[i][col], types[col]) {
					if bad < 5 {
						t.Errorf("%s row %d %s: %v (%T), the Node seed wrote %v", table, i+1, col, got[i][col], got[i][col], nv)
					}
					bad++
				}
			}
		}
		if bad > 5 {
			t.Errorf("%s: %d more values differ", table, bad-5)
		}
	}
	// The contract counters the seed updated.
	for _, u := range c.Updates {
		if !strings.HasPrefix(u.SQL, "UPDATE billing_contracts SET remaining_minutes = ?, current_period_ym = ?") {
			t.Fatalf("an update the test does not know: %s", u.SQL)
		}
		var remaining float64
		var period string
		if err := db.QueryRow(context.Background(), `SELECT remaining_minutes, current_period_ym FROM billing_contracts WHERE id = ?`, u.Params[2]).
			Scan(&remaining, &period); err != nil {
			t.Fatal(err)
		}
		if remaining != u.Params[0].(float64) || period != u.Params[1] {
			t.Errorf("contract %v: remaining %v, period %q; the Node seed set %v, %v", u.Params[2], remaining, period, u.Params[0], u.Params[1])
		}
	}
}

func TestDirectRowsAreTheNodeSeedsOnSQLite(t *testing.T) {
	for _, c := range loadOracle(t) {
		t.Run(isoText(c.Now), func(t *testing.T) {
			db := storetest.SQLite(t)
			before := seedDirect(t, db, c)
			checkAgainstNode(t, db, c, before)
		})
	}
}

func TestDirectRowsAreTheNodeSeedsInPostgresForms(t *testing.T) {
	if os.Getenv(storetest.PostgresEnv) == "" {
		t.Skipf("%s is not set", storetest.PostgresEnv)
	}
	for _, c := range loadOracle(t) {
		t.Run(isoText(c.Now), func(t *testing.T) {
			db := storetest.Postgres(t)
			before := seedDirect(t, db, c)
			checkAgainstNode(t, db, c, before)
		})
	}
}

// A second run replaces the demo rows instead of adding more.
func TestSeedingTwiceKeepsOneSetOfRows(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		c := loadOracle(t)[0]
		seedDirect(t, db, c)
		var tables []string
		for _, r := range c.Rows {
			if !slices.Contains(tables, r.Table) {
				tables = append(tables, r.Table)
			}
		}
		counts := func() string {
			var b strings.Builder
			for _, table := range tables {
				fmt.Fprintf(&b, "%s=%d ", table, countRows(t, db, table))
			}
			return b.String()
		}
		first := counts()
		ctx := context.Background()
		w, err := newWriter(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		orgIDs, made := oracleBilling()
		d := newDemo(time.UnixMilli(int64(c.Now)))
		if err := d.seedGoDB(ctx, w, io.Discard); err != nil {
			t.Fatal(err)
		}
		if err := d.seedAuthDB(ctx, w, "test", io.Discard); err != nil {
			t.Fatal(err)
		}
		if err := d.seedBillingSessions(ctx, w, orgIDs, made, io.Discard); err != nil {
			t.Fatal(err)
		}
		if second := counts(); second != first {
			t.Errorf("after a second run:\n%s\nafter the first:\n%s", second, first)
		}
	})
}

// The row builders at 200 more times, against the fingerprints rows.js
// took of the Node seed's rows: each table's rows as lists of values in
// the order of the INSERT's columns, as JSON, hashed.
func TestBuildersMatchTheNodeSeedAtManyTimes(t *testing.T) {
	f, err := os.Open("testdata/rows.json.gz")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		Fingerprints []struct {
			Now  float64           `json:"now"`
			Sums map[string]string `json:"sums"`
		} `json:"fingerprints"`
	}
	if err := json.NewDecoder(gz).Decode(&data); err != nil {
		t.Fatal(err)
	}
	if len(data.Fingerprints) < 100 {
		t.Fatalf("%d fingerprints", len(data.Fingerprints))
	}
	failed := 0
	for _, fp := range data.Fingerprints {
		got := builderRows(t, newDemo(time.UnixMilli(int64(fp.Now))))
		for table, want := range fp.Sums {
			sum := sha256.Sum256([]byte(mustStringify(got[table])))
			if hex.EncodeToString(sum[:]) != want {
				if failed < 10 {
					t.Errorf("%s: %s differs from the Node seed's", isoText(fp.Now), table)
				}
				failed++
			}
		}
	}
	if failed > 10 {
		t.Errorf("%d more tables differ", failed-10)
	}
}

// builderRows are the rows the builders make, per table, each a list of
// values in the column order of the Node seed's INSERT, as SQLite gets them.
func builderRows(t *testing.T, d *demo) map[string]any {
	t.Helper()
	out := map[string][]any{}
	add := func(table string, values ...any) {
		row := make([]any, len(values))
		for i, v := range values {
			s, err := sqliteValue(v)
			if err != nil {
				t.Fatal(err)
			}
			row[i] = s
		}
		out[table] = append(out[table], row)
	}
	for _, r := range d.inventoryRows() {
		add("device_inventory", r.DeviceID, r.Hardware, r.Software, r.CollectedAt, r.ReceivedAt)
		add("device_telemetry", r.DeviceID, r.CPU, r.MemoryUsed, r.MemoryTotal, r.Uptime, r.CollectedAt, r.ReceivedAt)
	}
	for _, id := range activityDevices {
		sessions, summaries := d.activityRows(id)
		for _, s := range sessions {
			add("activity_sessions", s.DeviceID, s.App, s.Title, s.Category, s.StartedAt, s.EndedAt, s.Duration, s.CreatedAt)
		}
		for _, s := range summaries {
			add("activity_summaries", s.DeviceID, s.Idle, s.Sessions, s.Active, s.ReportedAt, s.ReceivedAt)
		}
	}
	for _, h := range historyDevices {
		events, metrics, sessions := d.historyRows(h)
		for _, e := range events {
			add("peer_availability_events", e.PeerID, e.From, e.To, e.Source, e.CreatedAt)
		}
		for _, m := range metrics {
			add("peer_metrics", m.PeerID, m.CPU, m.Mem, m.Disk, m.CreatedAt)
		}
		for _, s := range sessions {
			add("peer_connection_sessions", s.HostID, s.PeerID, s.PeerName, s.PeerIP, s.Username, s.Transport, s.RelayUUID, s.StartedAt, s.EndedAt, "signal")
		}
	}
	for _, k := range d.ticketRows() {
		add("tickets", k.Title, k.Description, k.Status, k.Priority, k.Category, k.Device, k.CreatedBy, k.AssignedTo,
			k.SLADueAt, k.ResolvedAt, k.ClosedAt, k.CreatedAt, k.UpdatedAt)
	}
	orgIDs, made := oracleBilling()
	terms, contractIDs := map[string]pkgTerms{}, map[string]any{}
	for _, c := range made {
		terms[c.Org], contractIDs[c.Org] = c.Pkg, c.ID
	}
	rows, _ := d.billingRows(orgIDs, terms, contractIDs)
	for _, b := range rows {
		add("billing_sessions", b.ID, b.OrgID, b.ContractID, b.Operator, b.Operator, b.DeviceID, b.DeviceName, b.RelayUUID,
			"rustdesk", b.Status, b.Phase, b.StartedAt, b.EndedAt, b.RawSeconds, b.Billed, b.Included, b.Overage, 0.0,
			b.Amount, b.Currency, b.CreatedAt, b.UpdatedAt)
	}
	result := map[string]any{}
	for table, rows := range out {
		result[table] = rows
	}
	return result
}

func TestTheNewestMessageStaysUnreadUnlessTheAdminWroteIt(t *testing.T) {
	d := newDemo(time.UnixMilli(int64(sampleMS)))
	rows := d.chatRows(conversation{ID: "c", Messages: []chatMessage{
		{"900100008", 10, "from the device"}, {"admin", 5, "from the admin"},
	}})
	if !rows[0].Read || !rows[1].Read {
		t.Errorf("read: %v %v; the newest is the admin's and counts as read", rows[0].Read, rows[1].Read)
	}
	rows = d.chatRows(conversation{ID: "c", Messages: []chatMessage{
		{"admin", 10, "from the admin"}, {"demo_operator", 5, "from the operator"},
	}})
	if !rows[0].Read || rows[1].Read {
		t.Errorf("read: %v %v; the operator's newest message stays unread", rows[0].Read, rows[1].Read)
	}
	if rows[1].FromID != "operator:demo_operator" || rows[0].FromID != "operator" || rows[0].FromName != "admin" {
		t.Errorf("senders: %+v", rows)
	}
}
