package main

import (
	"reflect"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// 2026-11-19T10:20:30.456Z
const sampleMS = 1795083630456.0

func TestStampForms(t *testing.T) {
	cases := []struct {
		s        stamp
		sqlite   any
		instant  string
		fraction float64
	}{
		{stamp{sampleMS, sqlSeconds}, "2026-11-19 10:20:30", "2026-11-19T10:20:30Z", 0},
		{stamp{sampleMS, isoMillis}, "2026-11-19T10:20:30.456Z", "2026-11-19T10:20:30.456Z", 0},
		{stamp{sampleMS, epochMillis}, sampleMS, "2026-11-19T10:20:30.456Z", 0},
		// A fraction of a millisecond is cut off, as new Date(ms) does
		{stamp{sampleMS + 0.9, isoMillis}, "2026-11-19T10:20:30.456Z", "2026-11-19T10:20:30.456Z", 0},
	}
	for _, c := range cases {
		if got := c.s.sqlite(); got != c.sqlite {
			t.Errorf("%v on SQLite: %v, want %v", c.s, got, c.sqlite)
		}
		if got := c.s.instant().Format(time.RFC3339Nano); got != c.instant {
			t.Errorf("%v instant: %s, want %s", c.s, got, c.instant)
		}
	}
}

func TestSQLiteValuesAreWhatTheNodeSeedBound(t *testing.T) {
	cases := []struct {
		in   any
		want any
	}{
		{"text", "text"},
		{42.0, 42.0},
		{nil, nil},
		{undefined, nil},
		// better-sqlite3 bound every number as a double
		{flag(true), 1.0},
		{flag(false), 0.0},
		{jsonText(`{"a":1}`), `{"a":1}`},
		{stamp{sampleMS, sqlSeconds}, "2026-11-19 10:20:30"},
	}
	for _, c := range cases {
		got, err := sqliteValue(c.in)
		if err != nil {
			t.Fatalf("%v: %v", c.in, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%#v: %#v, want %#v", c.in, got, c.want)
		}
	}
	if _, err := sqliteValue(true); err == nil {
		t.Error("a bool went through; better-sqlite3 refused them")
	}
}

func TestPostgresValuesFollowTheColumnType(t *testing.T) {
	at := time.UnixMilli(int64(sampleMS)).UTC()
	cases := []struct {
		kind string
		in   any
		want any
	}{
		{"boolean", flag(true), true},
		{"boolean", flag(false), false},
		{"integer", flag(true), int64(1)},
		{"integer", 7.0, int64(7)},
		{"bigint", 1795083630456.0, int64(1795083630456)},
		{"real", 36.5, 36.5},
		{"double precision", 0.1, 0.1},
		{"text", 5.0, "5"},
		{"text", "x", "x"},
		{"timestamp with time zone", stamp{sampleMS, sqlSeconds}, at.Truncate(time.Second)},
		{"timestamp with time zone", stamp{sampleMS, isoMillis}, at},
		{"timestamp with time zone", stamp{sampleMS, epochMillis}, at},
		{"timestamp without time zone", stamp{sampleMS, isoMillis}, at},
		{"bigint", stamp{sampleMS, epochMillis}, int64(sampleMS)},
		{"text", stamp{sampleMS, sqlSeconds}, "2026-11-19 10:20:30"},
		{"jsonb", jsonText(`{"a":1}`), `{"a":1}`},
		{"timestamp with time zone", nil, nil},
	}
	for _, c := range cases {
		got, err := postgresValue(c.kind, c.in)
		if err != nil {
			t.Fatalf("%s %v: %v", c.kind, c.in, err)
		}
		if g, ok := got.(time.Time); ok {
			if w, ok := c.want.(time.Time); !ok || !g.Equal(w) {
				t.Errorf("%s %#v: %v, want %v", c.kind, c.in, got, c.want)
			}
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s %#v: %#v, want %#v", c.kind, c.in, got, c.want)
		}
	}
	if _, err := postgresValue("integer", 2.5); err == nil {
		t.Error("2.5 went into an integer column")
	}
}

func TestInsertStatementQuotesUserAndUsesTheDatabaseClock(t *testing.T) {
	w := &writer{db: storetest.SQLite(t)}
	q, args, err := w.insertStatement("peers", []col{{"id", "1"}, {"user", "u"}, {"updated_at", now{}}}, "ON CONFLICT(id) DO NOTHING")
	if err != nil {
		t.Fatal(err)
	}
	if want := `INSERT INTO peers (id, "user", updated_at) VALUES (?, ?, datetime('now')) ON CONFLICT(id) DO NOTHING`; q != want {
		t.Errorf("statement:\n%s\nwant:\n%s", q, want)
	}
	if !reflect.DeepEqual(args, []any{"1", "u"}) {
		t.Errorf("args %v", args)
	}
}
