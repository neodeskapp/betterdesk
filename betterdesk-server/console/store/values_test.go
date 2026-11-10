package store_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	_ "time/tzdata" // the zone of the Berlin vectors, whatever the machine has

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// valueVectors is what the Node console's drivers returned for a table of
// values: for each case the statement that stores the value, the query
// that reads it, and JSON.stringify of what the driver gave.
type valueVectors struct {
	Dialect string      `json:"dialect"`
	TZ      string      `json:"tz"`
	Driver  string      `json:"driver"`
	Create  string      `json:"create"`
	Cases   []valueCase `json:"cases"`
}

type valueCase struct {
	Name     string `json:"name"`
	Go       string `json:"go"`
	JS       string `json:"js"`
	JSON     string `json:"json"`
	Insert   string `json:"insert"`
	Query    string `json:"query"`
	Fallback string `json:"fallback"`
	Parsed   string `json:"parsed"`
}

func readValueVectors(t *testing.T, name string) valueVectors {
	t.Helper()
	var v valueVectors
	if err := json.Unmarshal([]byte(readTestdata(t, "vectors/"+name)), &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Cases) < 40 {
		t.Fatalf("%s has %d cases", name, len(v.Cases))
	}
	return v
}

// processZone names the vector file for the time zone this process runs
// in. PostgreSQL columns without a time zone are read in it.
func processZone(t *testing.T) string {
	t.Helper()
	switch zone := os.Getenv("TZ"); zone {
	case "", "UTC":
		if _, offset := time.Now().Zone(); offset != 0 {
			t.Skip("the local time zone is not UTC and TZ does not say which it is")
		}
		return "utc"
	case "Europe/Berlin":
		return "berlin"
	default:
		t.Skipf("no vectors for TZ=%s", zone)
		return ""
	}
}

// marshalJS encodes like the console's JSON answers must be encoded: no
// HTML escaping, so the bytes compare with JSON.stringify.
func marshalJS(t *testing.T, v any) string {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

func newValue(t *testing.T, c valueCase) any {
	t.Helper()
	switch c.Go {
	case "Time":
		return new(store.Time)
	case "Bool":
		return new(store.Bool)
	case "Int64String":
		return new(store.Int64String)
	case "Float":
		return new(store.Float)
	case "JSONText":
		return new(store.JSONText)
	case "JSONParsed":
		return &store.JSONParsed{Fallback: c.Fallback}
	case "NullString":
		return new(store.NullString)
	}
	t.Fatalf("case %q: unknown value type %q", c.Name, c.Go)
	return nil
}

func loadValueTable(t *testing.T, db *store.DB, v valueVectors) {
	t.Helper()
	mustExec(t, db, v.Create)
	for _, c := range v.Cases {
		if c.Insert != "" {
			mustExec(t, db, c.Insert)
		}
	}
}

func vectorsFor(t *testing.T, db *store.DB) valueVectors {
	t.Helper()
	if db.IsPostgres() {
		return readValueVectors(t, "values-postgres-"+processZone(t)+".json")
	}
	return readValueVectors(t, "values-sqlite.json")
}

// Every value type gives the JSON the Node console sent for the same
// stored value, on both databases. The expected JSON is what its drivers
// returned, recorded by testdata/nodeschema/vectors.js.
func TestValuesMatchNode(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		v := vectorsFor(t, db)
		loadValueTable(t, db, v)
		ctx := testContext(t)
		replayed := map[string]int{}
		for _, c := range v.Cases {
			if c.Go == "none" {
				continue
			}
			value := newValue(t, c)
			if err := store.SelectRow(ctx, db, c.Query).Scan(value); err != nil {
				t.Errorf("%s (%s): scan: %v", c.Name, c.Go, err)
				continue
			}
			want := c.JSON
			if c.Go == "JSONParsed" {
				want = c.Parsed
			}
			if got := marshalJS(t, value); got != want {
				t.Errorf("%s (%s): JSON = %s, Node sent %s", c.Name, c.Go, got, want)
			}
			replayed[c.Go]++
		}
		for _, kind := range []string{"Time", "Bool", "Int64String", "Float", "JSONText", "JSONParsed", "NullString"} {
			if replayed[kind] < 3 {
				t.Errorf("only %d cases for %s", replayed[kind], kind)
			}
		}
	})
}

// The same through a result set with several rows and columns, inside a
// transaction: Rows.Scan gives every value its own column type.
func TestSelectScansEachColumnByItsType(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		v := vectorsFor(t, db)
		loadValueTable(t, db, v)
		ctx := testContext(t)
		flag, number := "c_integer", "c_integer"
		wantFlag, wantNumber := "1", "1"
		if db.IsPostgres() {
			flag, number = "c_boolean", "c_bigint"
			wantFlag, wantNumber = "true", `"1"`
		}
		mustExec(t, db, `INSERT INTO vec (c_text, `+flag+`) VALUES ('first', `+db.Pick("1", "TRUE")+`)`)
		mustExec(t, db, `UPDATE vec SET `+number+` = 1 WHERE c_text = 'first'`)
		err := db.InTx(ctx, func(tx *store.Tx) error {
			rows, err := store.Select(ctx, tx, `SELECT c_text, `+flag+`, `+number+`, id FROM vec WHERE c_text IN ('first', 'plain') ORDER BY c_text`)
			if err != nil {
				return err
			}
			defer rows.Close()
			var got []string
			for rows.Next() {
				var text store.NullString
				var f store.Bool
				var n, id store.Int64String
				if err := rows.Scan(&text, &f, &n, &id); err != nil {
					return err
				}
				got = append(got, marshalJS(t, []any{text, f, n}))
				if idJSON := marshalJS(t, id); !regexp.MustCompile(`^\d+$`).MatchString(idJSON) {
					t.Errorf("id from an integer key = %s, want a number", idJSON)
				}
			}
			want := []string{`["first",` + wantFlag + `,` + wantNumber + `]`, `["plain",null,null]`}
			if strings.Join(got, " ") != strings.Join(want, " ") {
				t.Errorf("rows = %v, want %v", got, want)
			}
			return rows.Err()
		})
		if err != nil {
			t.Fatal(err)
		}
	})
}

// A value that needs its column type refuses to guess when it is read
// without one. Values that do not need it work either way.
func TestValuesNeedTheColumnType(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		v := vectorsFor(t, db)
		loadValueTable(t, db, v)
		ctx := testContext(t)
		timeColumn := db.Pick("c_datetime", "c_timestamptz")
		jsonColumn := db.Pick("c_text", "c_jsonb")
		refused := map[string]any{
			`SELECT ` + timeColumn + ` FROM vec WHERE ` + timeColumn + ` IS NOT NULL ORDER BY id LIMIT 1`: new(store.Time),
			`SELECT c_integer FROM vec WHERE c_integer = 42`:                                              new(store.Int64String),
			`SELECT c_real FROM vec WHERE c_real IS NOT NULL ORDER BY id LIMIT 1`:                         new(store.Float),
			`SELECT ` + jsonColumn + ` FROM vec WHERE ` + jsonColumn + ` IS NOT NULL ORDER BY id LIMIT 1`: new(store.JSONText),
		}
		for query, value := range refused {
			err := db.QueryRow(ctx, query).Scan(value)
			if err == nil || !strings.Contains(err.Error(), "store.Select") {
				t.Errorf("plain scan into %T = %v, want an error that points to store.Select", value, err)
			}
			if err := store.SelectRow(ctx, db, query).Scan(value); err != nil {
				t.Errorf("SelectRow scan into %T: %v", value, err)
			}
		}

		var flag store.Bool
		var text store.NullString
		parsed := store.JSONParsed{Fallback: "[]"}
		if err := db.QueryRow(ctx, `SELECT c_integer, c_text, c_text FROM vec WHERE c_integer = 1 OR c_text = 'not json' ORDER BY c_integer`).Scan(&flag, &text, &parsed); err != nil {
			t.Errorf("plain scan into Bool, NullString and JSONParsed: %v", err)
		}
		// A NULL needs no column type.
		var nothing store.Time
		if err := db.QueryRow(ctx, `SELECT NULL`).Scan(&nothing); err != nil || !nothing.IsNull() || marshalJS(t, nothing) != "null" {
			t.Errorf("NULL into Time = %s, %v", marshalJS(t, nothing), err)
		}
	})
}

// Text in a SQLite column declared DATETIME reaches Go as a parsed time,
// not as the text. Time gives it back in the form datetime('now') writes,
// which is the only form the console's statements put there. Other text in
// such a column comes out in that form too.
func TestTimeFromSQLiteDatetimeColumn(t *testing.T) {
	db := storetest.SQLite(t)
	ctx := testContext(t)
	mustExec(t, db, `CREATE TABLE d (id INTEGER PRIMARY KEY, at DATETIME DEFAULT (datetime('now')), plain TEXT DEFAULT (datetime('now')))`)
	mustExec(t, db, `INSERT INTO d (id) VALUES (1)`)
	mustExec(t, db, `INSERT INTO d (id, at, plain) VALUES (2, '2026-11-10T01:05:50.930Z', '2026-11-10T01:05:50.930Z')`)

	var at, plain store.Time
	if err := store.SelectRow(ctx, db, `SELECT at, plain FROM d WHERE id = 1`).Scan(&at, &plain); err != nil {
		t.Fatal(err)
	}
	if marshalJS(t, at) != marshalJS(t, plain) || !regexp.MustCompile(`^"\d{4}-\d\d-\d\d \d\d:\d\d:\d\d"$`).MatchString(marshalJS(t, at)) {
		t.Errorf("now in a DATETIME column = %s, in a TEXT column = %s, want the same text", marshalJS(t, at), marshalJS(t, plain))
	}
	if err := store.SelectRow(ctx, db, `SELECT at, plain FROM d WHERE id = 2`).Scan(&at, &plain); err != nil {
		t.Fatal(err)
	}
	if got := marshalJS(t, plain); got != `"2026-11-10T01:05:50.930Z"` {
		t.Errorf("ISO text in a TEXT column = %s", got)
	}
	if got := marshalJS(t, at); got != `"2026-11-10 01:05:50.93"` {
		t.Errorf("ISO text in a DATETIME column = %s, want it in SQLite's form", got)
	}
	// The way around it: ask SQLite for the text.
	if err := store.SelectRow(ctx, db, `SELECT CAST(at AS TEXT) FROM d WHERE id = 2`).Scan(&at); err != nil {
		t.Fatal(err)
	}
	if got := marshalJS(t, at); got != `"2026-11-10T01:05:50.930Z"` {
		t.Errorf("CAST(at AS TEXT) = %s, want the stored text", got)
	}
}

var literalPattern = regexp.MustCompile(`VALUES \('([^']+)'\)`)

// A PostgreSQL time without zone is the clock reading of the console's
// own time zone. The vectors come from a console that ran in Berlin.
func TestLocalWallClockMatchesNode(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	v := readValueVectors(t, "values-postgres-berlin.json")
	checked := 0
	for _, c := range v.Cases {
		m := literalPattern.FindStringSubmatch(c.Insert)
		if c.Go != "Time" || m == nil || !(strings.Contains(c.Insert, "c_timestamp)") || strings.Contains(c.Insert, "c_date)")) {
			continue
		}
		wall, ok := store.ParseTime(m[1])
		if !ok {
			wall, err = time.Parse("2006-01-02", m[1])
			if err != nil {
				t.Fatalf("case %q: %v", c.Name, err)
			}
		}
		got := `"` + store.ISOTime(store.LocalWallClock(wall, berlin)) + `"`
		if got != c.JSON {
			t.Errorf("%s: %s read in Berlin = %s, Node sent %s", c.Name, m[1], got, c.JSON)
		}
		checked++
	}
	if checked != 3 {
		t.Errorf("checked %d cases, want 3", checked)
	}
}

type backfillTimes struct {
	Instants []struct {
		MS     int64  `json:"ms"`
		ISO    string `json:"iso"`
		Expiry string `json:"expiry"`
	} `json:"instants"`
}

// The time formats are the ones JavaScript and SQLite produce.
func TestTimeFormats(t *testing.T) {
	var v backfillTimes
	if err := json.Unmarshal([]byte(readTestdata(t, "vectors/backfills.json")), &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Instants) < 8 {
		t.Fatalf("%d instants in the vectors", len(v.Instants))
	}
	for _, c := range v.Instants {
		at := time.UnixMilli(c.MS)
		if got := store.ISOTime(at); got != c.ISO {
			t.Errorf("ISOTime(%d ms) = %s, toISOString gave %s", c.MS, got, c.ISO)
		}
		if year := at.UTC().Year(); year >= 0 && year <= 9999 {
			if got := store.DateTimeMillis(at); got != c.Expiry {
				t.Errorf("DateTimeMillis(%d ms) = %s, the console built %s", c.MS, got, c.Expiry)
			}
		}
	}
	// ISOTime is in UTC whatever zone the time carries.
	east := time.Date(2026, 11, 10, 3, 5, 51, 694_000_000, time.FixedZone("east", 2*3600))
	if got := store.ISOTime(east); got != "2026-11-10T01:05:51.694Z" {
		t.Errorf("ISOTime of a time in another zone = %s", got)
	}

	// SQLiteTime against SQLite itself.
	db := storetest.SQLite(t)
	for _, unix := range []int64{0, 1762736751, 253402300799} {
		want := queryStrings(t, db, `SELECT datetime(?, 'unixepoch')`, unix)[0][0]
		if got := store.SQLiteTime(time.Unix(unix, 0).In(time.FixedZone("east", 2*3600))); got != want {
			t.Errorf("SQLiteTime(%d) = %q, SQLite's datetime gives %q", unix, got, want)
		}
	}
}

// ISOTime cuts a PostgreSQL time to milliseconds and never rounds up, as
// the console's driver did for every fraction of a second in the vectors,
// and as its arithmetic does for every microsecond there is.
func TestISOTimeCutsToMilliseconds(t *testing.T) {
	var fractions [][2]int
	if err := json.Unmarshal([]byte(readTestdata(t, "vectors/fractions.json")), &fractions); err != nil {
		t.Fatal(err)
	}
	if len(fractions) < 3000 {
		t.Fatalf("%d fractions in the vectors", len(fractions))
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, f := range fractions {
		got := store.ISOTime(base.Add(time.Duration(f[0]) * time.Microsecond))
		if want := fmt.Sprintf("2026-01-01T00:00:00.%03dZ", f[1]); got != want {
			t.Fatalf("ISOTime of .%06d s = %s, Node gave %s", f[0], got, want)
		}
	}
	// The driver computed 1000 * parseFloat("0.<digits>") and dropped the
	// fraction. For no microsecond does that differ from plain truncation.
	for micros := 0; micros < 1_000_000; micros++ {
		digits := strings.TrimRight(fmt.Sprintf("%06d", micros), "0")
		fraction, _ := strconv.ParseFloat("0."+digits, 64)
		if driver := int(math.Trunc(1000 * fraction)); micros > 0 && driver != micros/1000 {
			t.Fatalf(".%06d s: the driver's arithmetic gives %d ms, truncation %d", micros, driver, micros/1000)
		}
	}
	// Before 1970 and with a zone: still towards the start of the second.
	early := time.Date(1969, 12, 31, 23, 59, 59, 999_999_000, time.FixedZone("west", -5*3600))
	if got := store.ISOTime(early); got != "1970-01-01T04:59:59.999Z" {
		t.Errorf("ISOTime just before 1970 = %s", got)
	}
}

// Instant gives the point in time behind every form of time the console
// stores, so code can compare times without caring about the database.
func TestTimeInstant(t *testing.T) {
	want := time.Date(2026, 11, 10, 1, 5, 50, 0, time.UTC)
	for text, plus := range map[string]time.Duration{
		"2026-11-10 01:05:50":       0,
		"2026-11-10T01:05:50.930Z":  930 * time.Millisecond,
		"2026-11-10T01:05:50Z":      0,
		"2026-11-10 01:05:50.694":   694 * time.Millisecond,
		"2026-11-10T03:05:50+02:00": 0,
	} {
		got, ok := store.ParseTime(text)
		if !ok || !got.Equal(want.Add(plus)) {
			t.Errorf("ParseTime(%q) = %v, %v, want %v", text, got, ok, want.Add(plus))
		}
	}
	for _, text := range []string{"", "not a time", "2026-11-10"} {
		if got, ok := store.ParseTime(text); ok {
			t.Errorf("ParseTime(%q) = %v, want no time", text, got)
		}
	}

	storetest.Each(t, func(t *testing.T, db *store.DB) {
		ctx := testContext(t)
		mustExec(t, db, db.Pick(`CREATE TABLE stamps (at TEXT)`, `CREATE TABLE stamps (at TIMESTAMPTZ)`))
		mustExec(t, db, `INSERT INTO stamps (at) VALUES (`+store.NowSQL(db.Dialect())+`)`)
		var at store.Time
		if err := store.SelectRow(ctx, db, `SELECT at FROM stamps`).Scan(&at); err != nil {
			t.Fatal(err)
		}
		form := db.Pick(`^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d$`, `^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$`)
		if !regexp.MustCompile(form).MatchString(at.String()) {
			t.Errorf("a row written with NowSQL reads %q, want the form %s", at.String(), form)
		}
		instant, ok := at.Instant()
		if !ok || time.Since(instant) < -time.Minute || time.Since(instant) > time.Minute {
			t.Errorf("Instant of now = %v, %v", instant, ok)
		}
		if at.IsNull() {
			t.Error("a stored time reports NULL")
		}
	})
}

// BoolArg writes a flag in the form the column's type takes, and Bool
// reads it back as that type's JSON.
func TestBoolArgWritesWhatTheColumnTakes(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		ctx := testContext(t)
		mustExec(t, db, db.Pick(`CREATE TABLE flags (id INTEGER PRIMARY KEY, as_int INTEGER, as_bool INTEGER)`,
			`CREATE TABLE flags (id INTEGER PRIMARY KEY, as_int INTEGER, as_bool BOOLEAN)`))
		shape, err := db.Inspect(ctx)
		if err != nil {
			t.Fatal(err)
		}
		flags := shape.Table("flags")
		for id, value := range []bool{false, true} {
			mustExec(t, db, `INSERT INTO flags (id, as_int, as_bool) VALUES (?, ?, ?)`, id,
				store.BoolArg(flags.Type("as_int"), value), store.BoolArg(flags.Type("as_bool"), value))
		}
		var asInt, asBool store.Bool
		if err := store.SelectRow(ctx, db, `SELECT as_int, as_bool FROM flags WHERE id = 1`).Scan(&asInt, &asBool); err != nil {
			t.Fatal(err)
		}
		if got, want := marshalJS(t, []any{asInt, asBool}), db.Pick(`[1,1]`, `[1,true]`); got != want {
			t.Errorf("true written with BoolArg reads %s, want %s", got, want)
		}
		if !asInt.True() || !asBool.True() || asInt.IsNull() {
			t.Error("True() is false for a set flag")
		}
		if err := store.SelectRow(ctx, db, `SELECT as_int, as_bool FROM flags WHERE id = 0`).Scan(&asInt, &asBool); err != nil {
			t.Fatal(err)
		}
		if got, want := marshalJS(t, []any{asInt, asBool}), db.Pick(`[0,0]`, `[0,false]`); got != want {
			t.Errorf("false written with BoolArg reads %s, want %s", got, want)
		}
		if asInt.True() || asBool.True() {
			t.Error("True() is true for a cleared flag")
		}
	})
}

func TestValueHelpers(t *testing.T) {
	if got := marshalJS(t, []any{store.NewInt64String(5, store.TypeInteger), store.NewInt64String(5, store.TypeBigInt), store.Int64String{}}); got != `[5,"5",null]` {
		t.Errorf("NewInt64String as integer, bigint and the zero value = %s", got)
	}

	null, empty, text := store.NullString{}, store.NullString{Valid: true}, store.NullString{String: "x", Valid: true}
	if got := marshalJS(t, []any{null, empty, text}); got != `[null,"","x"]` {
		t.Errorf("NullString = %s", got)
	}
	if got := marshalJS(t, []any{null.OrEmpty(), empty.OrEmpty(), text.OrEmpty()}); got != `["","","x"]` {
		t.Errorf("OrEmpty = %s", got)
	}
	if got := marshalJS(t, []any{null.OrNull(), empty.OrNull(), text.OrNull()}); got != `[null,null,"x"]` {
		t.Errorf("OrNull = %s", got)
	}

	db := storetest.SQLite(t)
	ctx := testContext(t)
	var raw store.JSONText
	if err := store.SelectRow(ctx, db, `SELECT '{"b": [1.0, 2]}'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if got := marshalJS(t, []any{raw, raw.Parsed("{}")}); got != `["{\"b\": [1.0, 2]}",{"b":[1,2]}]` {
		t.Errorf("JSONText and its Parsed = %s", got)
	}
	if raw.Text() != `{"b": [1.0, 2]}` || raw.IsNull() {
		t.Errorf("JSONText.Text = %q", raw.Text())
	}
	if err := store.SelectRow(ctx, db, `SELECT 'broken'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if got := marshalJS(t, []any{raw.Parsed("[]"), raw.Parsed("")}); got != `[[],null]` {
		t.Errorf("Parsed of broken JSON with and without a fallback = %s", got)
	}
}

// SelectRow behaves like QueryRow: no row is sql.ErrNoRows, a broken query
// is its error, and the result set is released.
func TestSelectRow(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		ctx := testContext(t)
		mustExec(t, db, `CREATE TABLE one (n INTEGER)`)
		var n store.Int64String
		if err := store.SelectRow(ctx, db, `SELECT n FROM one`).Scan(&n); !errors.Is(err, sql.ErrNoRows) {
			t.Errorf("no row: error = %v, want sql.ErrNoRows", err)
		}
		if err := store.SelectRow(ctx, db, `SELECT n FROM missing_table`).Scan(&n); err == nil || errors.Is(err, sql.ErrNoRows) {
			t.Errorf("broken query: error = %v", err)
		}
		// A read whose Scan fails gives its connection back as well: more of
		// them than there are connections must not end up waiting for one.
		failing, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		for i := 0; i < 40; i++ {
			var number int
			err := store.SelectRow(failing, db, `SELECT 'abc'`).Scan(&number)
			if err == nil || errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("read %d with a failing Scan: error = %v, want the conversion error at once", i, err)
			}
		}
		mustExec(t, db, `INSERT INTO one (n) VALUES (?)`, 7)
		// More reads than there are connections: each one must give its back.
		for i := 0; i < 40; i++ {
			if err := store.SelectRow(ctx, db, `SELECT n FROM one WHERE n = ?`, 7).Scan(&n); err != nil || n.V != 7 || !n.Valid {
				t.Fatalf("read %d: %+v, %v", i, n, err)
			}
		}
		if _, err := store.Select(ctx, db, `SELECT n FROM missing_table`); err == nil {
			t.Error("Select on a missing table returned no error")
		}
	})
}
