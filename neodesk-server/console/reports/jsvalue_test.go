package reports

import (
	"math"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

func TestMathRound(t *testing.T) {
	for _, c := range []struct{ in, want float64 }{
		{0.5, 1}, {1.5, 2}, {2.5, 3}, {-0.5, 0}, {-1.5, -1}, {-2.5, -2}, {0.49999999999999994, 0},
		{1.4999999999999998, 1}, {4503599627370495.5, 4503599627370496}, {-0.4, 0}, {math.Inf(1), math.Inf(1)},
	} {
		got := jsRound(c.in)
		if got != c.want {
			t.Errorf("Math.round(%v) = %v, want %v", c.in, got, c.want)
		}
	}
	if !math.Signbit(jsRound(-0.4)) || !math.Signbit(jsRound(math.Copysign(0, -1))) || math.Signbit(jsRound(0.4)) {
		t.Error("Math.round keeps the sign of a zero")
	}
	if !math.IsNaN(jsRound(math.NaN())) {
		t.Error("Math.round(NaN) is NaN")
	}
}

// TestTallyCountsEveryName: a name every JavaScript object has counts
// from 1 like any other, __proto__ too (BUGS.md RP3).
func TestTallyCountsEveryName(t *testing.T) {
	counts := newTally()
	for _, name := range []string{"b", "10", "constructor", "__proto__", "2", "b", "constructor", "toString", "4294967295", "01", "__proto__"} {
		counts.add(name)
	}
	const want = `{"2":1,"10":1,"b":2,"constructor":2,"__proto__":2,"toString":1,"4294967295":1,"01":1}`
	if got := string(Stringify(counts.counts)); got != want {
		t.Errorf("tally:\n got %s\nwant %s", got, want)
	}
}

func TestStringify(t *testing.T) {
	v := goapi.NewObject(
		"n", []any{1e21, 1e-7, math.Copysign(0, -1), math.NaN(), math.Inf(-1), 0.1, 123456789.125},
		"s", "quote \" backslash \\ line  tab\t nul\x00 ü",
		"d", Date{T: time.Date(2026, 11, 10, 1, 5, 51, 694999999, time.FixedZone("x", 3600))},
		"b", Buffer{0, 255},
		"o", goapi.NewObject("b", nil, "1", true),
	)
	const want = `{"n":[1e+21,1e-7,0,null,null,0.1,123456789.125],"s":"quote \" backslash \\ line` + " " + ` tab\t nul\u0000 ü","d":"2026-11-10T00:05:51.694Z","b":{"type":"Buffer","data":[0,255]},"o":{"1":true,"b":null}}`
	if got := string(Stringify(v)); got != want {
		t.Errorf("Stringify:\n got %s\nwant %s", got, want)
	}
}

func TestStringOfValues(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	c := clock{loc: berlin, zoneName: DefaultZoneName}
	for _, x := range []struct {
		v    any
		want string
	}{
		{nil, "null"}, {true, "true"}, {1e21, "1e+21"}, {-0.0, "0"}, {"x", "x"},
		{[]any{1.0, nil, "a", []any{2.0, 3.0}}, "1,,a,2,3"}, {goapi.NewObject("a", 1.0), "[object Object]"},
		{Date{T: time.Date(2026, 7, 4, 10, 0, 0, 0, time.UTC)}, "Sat Jul 04 2026 12:00:00 GMT+0200 (CEST)"},
		{Date{T: time.Date(2026, 1, 4, 10, 0, 0, 0, time.UTC)}, "Sun Jan 04 2026 11:00:00 GMT+0100 (CET)"},
		{Buffer("hé"), "hé"}, {Buffer{0xff, 'a'}, "�a"},
	} {
		if got := c.String(x.v); got != x.want {
			t.Errorf("String(%#v) = %q, want %q", x.v, got, x.want)
		}
	}
	utc := clock{loc: time.UTC, zoneName: DefaultZoneName}
	if got := utc.String(Date{T: time.Date(999, 12, 31, 23, 59, 59, 0, time.UTC)}); got != "Tue Dec 31 0999 23:59:59 GMT+0000 (Coordinated Universal Time)" {
		t.Errorf("a date in UTC: %q", got)
	}
	for _, x := range []struct {
		v    any
		want float64
	}{{nil, 0}, {false, 0}, {" 0x10 ", 16}, {"", 0}, {[]any{"7"}, 7}, {[]any{}, 0}, {Date{T: time.UnixMilli(1500)}, 1500}} {
		if got := c.Number(x.v); got != x.want {
			t.Errorf("Number(%#v) = %v, want %v", x.v, got, x.want)
		}
	}
	for _, v := range []any{"abc", goapi.NewObject(), []any{1.0, 2.0}} {
		if got := c.Number(v); !math.IsNaN(got) {
			t.Errorf("Number(%#v) = %v, want NaN", v, got)
		}
	}
}

// TestDriverValues: what the old drivers made of each kind of column.
func TestDriverValues(t *testing.T) {
	local := time.FixedZone("local", 2*3600)
	at := time.Date(2026, 11, 10, 1, 5, 51, 694000000, time.UTC)
	for _, c := range []struct {
		dialect  store.Dialect
		typeName string
		in       any
		want     string
	}{
		{store.SQLite, "INTEGER", int64(5000000000), `5000000000`},
		{store.SQLite, "INTEGER", int64(9007199254740993), `9007199254740992`},
		{store.SQLite, "REAL", 0.1, `0.1`},
		{store.SQLite, "TEXT", "2026-11-10 01:05:50", `"2026-11-10 01:05:50"`},
		{store.SQLite, "BLOB", []byte{1}, `{"type":"Buffer","data":[1]}`},
		{store.SQLite, "", nil, `null`},
		{store.Postgres, "INT4", int64(42), `42`},
		{store.Postgres, "INT8", int64(42), `"42"`},
		{store.Postgres, "FLOAT4", float64(float32(0.1)), `0.1`},
		{store.Postgres, "FLOAT8", 0.1, `0.1`},
		{store.Postgres, "NUMERIC", "12.50", `"12.50"`},
		{store.Postgres, "BOOL", true, `true`},
		{store.Postgres, "JSONB", []byte(`{"b":1,"10":2,"2":3}`), `{"2":3,"10":2,"b":1}`},
		{store.Postgres, "JSON", `"text"`, `"text"`},
		{store.Postgres, "TIMESTAMPTZ", at, `"2026-11-10T01:05:51.694Z"`},
		{store.Postgres, "TIMESTAMP", at, `"2026-11-09T23:05:51.694Z"`},
		{store.Postgres, "TIMESTAMPTZ", "infinity", `null`},
		{store.Postgres, "BYTEA", []byte{7}, `{"type":"Buffer","data":[7]}`},
	} {
		if got := string(Stringify(fromDriver(c.dialect, c.typeName, c.in, local))); got != c.want {
			t.Errorf("%s %s %#v: %s, want %s", c.dialect, c.typeName, c.in, got, c.want)
		}
	}
}

func TestDefaultZoneName(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		t    time.Time
		want string
	}{
		{time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), "Coordinated Universal Time"},
		{time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("", 0)), "Coordinated Universal Time"},
		{time.Date(2026, 1, 1, 0, 0, 0, 0, berlin), "CET"},
		{time.Date(2026, 7, 1, 0, 0, 0, 0, berlin), "CEST"},
	} {
		if got := DefaultZoneName(c.t); got != c.want {
			t.Errorf("%v: %q, want %q", c.t, got, c.want)
		}
	}
}
