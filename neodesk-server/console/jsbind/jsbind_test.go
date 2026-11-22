package jsbind

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// vectors is testdata/vectors.json: what JavaScript, better-sqlite3 and
// node-postgres made of strings and values (testdata/README.md).
type vectors struct {
	Strings []struct {
		S       string `json:"s"`
		Number  string `json:"number"`
		NegZero bool   `json:"negZero"`
		Trim    string `json:"trim"`
		Lower   string `json:"lower"`
	} `json:"strings"`
	Values []struct {
		JSON      string  `json:"json"`
		Number    string  `json:"number"`
		String    string  `json:"string"`
		Truthy    bool    `json:"truthy"`
		Stringify string  `json:"stringify"`
		Postgres  *string `json:"postgres"`
		SQLite    struct {
			Type  string  `json:"type"`
			Text  *string `json:"text"`
			Error string  `json:"error"`
		} `json:"sqlite"`
	} `json:"values"`
	Lists []struct {
		JSON   string `json:"json"`
		SQLite struct {
			A     string `json:"a"`
			B     string `json:"b"`
			Error string `json:"error"`
		} `json:"sqlite"`
		Postgres []*string `json:"postgres"`
	} `json:"lists"`
}

func loadVectors(t *testing.T) vectors {
	t.Helper()
	raw, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Strings) < 4000 || len(v.Values) < 50 || len(v.Lists) < 20 {
		t.Fatalf("vectors.json is short: %d strings, %d values, %d lists", len(v.Strings), len(v.Values), len(v.Lists))
	}
	return v
}

// valueOf is the Value of JSON text, read the way a request body is. The
// marks "@undefined", "@NaN", "@Infinity", "@-Infinity" and "@-0" stand
// for the values JSON cannot write.
func valueOf(t testing.TB, text string) Value {
	t.Helper()
	var got Value
	handler := middleware.Bodies(middleware.BodyOptions{})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = marked(Of(middleware.Body(r).Get("v")))
	}))
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"v":`+text+`}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("the body parser refused %s: %d", text, rec.Code)
	}
	return got
}

func marked(v Value) Value {
	switch {
	case v.IsArray():
		for i, item := range v.items {
			v.items[i] = marked(item)
		}
	case v.IsString():
		switch v.str {
		case "@undefined":
			return Undefined
		case "@NaN":
			return Number(math.NaN())
		case "@Infinity":
			return Number(math.Inf(1))
		case "@-Infinity":
			return Number(math.Inf(-1))
		case "@-0":
			return Number(math.Copysign(0, -1))
		}
	}
	return v
}

func TestStringsAsJavaScriptReadsThem(t *testing.T) {
	for _, vec := range loadVectors(t).Strings {
		n := ParseNumber(vec.S)
		if got := NumberString(n); got != vec.Number {
			t.Errorf("Number(%q) = %s, want %s", vec.S, got, vec.Number)
		}
		if negZero := n == 0 && math.Signbit(n); negZero != vec.NegZero {
			t.Errorf("Number(%q) is -0: %v, want %v", vec.S, negZero, vec.NegZero)
		}
		if got := Trim(vec.S); got != vec.Trim {
			t.Errorf("%q.trim() = %q, want %q", vec.S, got, vec.Trim)
		}
		// The final sigma is the one letter ToLower leaves to its callers
		if got := ToLower(vec.S); got != strings.ReplaceAll(vec.Lower, "ς", "σ") {
			t.Errorf("%q.toLowerCase() = %q, want %q", vec.S, got, vec.Lower)
		}
	}
}

func TestValuesAsJavaScriptAndTheDriversReadThem(t *testing.T) {
	db := storetest.SQLite(t)
	ctx := context.Background()
	for _, vec := range loadVectors(t).Values {
		v := valueOf(t, vec.JSON)
		if got := NumberString(v.ToNumber()); got != vec.Number {
			t.Errorf("Number(%s) = %s, want %s", vec.JSON, got, vec.Number)
		}
		if got := v.ToString(); got != vec.String {
			t.Errorf("String(%s) = %q, want %q", vec.JSON, got, vec.String)
		}
		if got := v.Truthy(); got != vec.Truthy {
			t.Errorf("!!%s = %v, want %v", vec.JSON, got, vec.Truthy)
		}
		if got := v.JSON(); got != vec.Stringify {
			t.Errorf("JSON.stringify(%s) = %s, want %s", vec.JSON, got, vec.Stringify)
		}

		args, err := Args(store.Postgres, 1, v)
		if err != nil {
			t.Errorf("Args(postgres, %s): %v", vec.JSON, err)
		} else if got, isText := args[0].(string); (vec.Postgres == nil) != (args[0] == nil) || (vec.Postgres != nil && (!isText || got != *vec.Postgres)) {
			t.Errorf("node-postgres sends %s as %v, got %#v", vec.JSON, show(vec.Postgres), args[0])
		}

		args, err = Args(store.SQLite, 2, v, v)
		if vec.SQLite.Error != "" {
			_, message, _ := strings.Cut(vec.SQLite.Error, ": ")
			if err == nil || Message(err) != message {
				t.Errorf("better-sqlite3 refuses %s twice with %q, got %v", vec.JSON, message, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("Args(sqlite, %s, %s): %v", vec.JSON, vec.JSON, err)
			continue
		}
		var typ string
		var text *string
		if err := db.QueryRow(ctx, "SELECT typeof(?) AS type, CAST(? AS TEXT) AS text", args...).Scan(&typ, &text); err != nil {
			t.Errorf("bind %s: %v", vec.JSON, err)
			continue
		}
		if typ != vec.SQLite.Type || show(text) != show(vec.SQLite.Text) {
			t.Errorf("SQLite got %s as %s %s, want %s %s", vec.JSON, typ, show(text), vec.SQLite.Type, show(vec.SQLite.Text))
		}
	}
}

func show(s *string) string {
	if s == nil {
		return "NULL"
	}
	return "'" + *s + "'"
}

func TestListsOfParametersAsTheDriversBoundThem(t *testing.T) {
	db := storetest.SQLite(t)
	ctx := context.Background()
	for _, vec := range loadVectors(t).Lists {
		values := valueOf(t, vec.JSON).Items()

		pg, err := Args(store.Postgres, 2, values...)
		if err != nil || len(pg) != len(vec.Postgres) {
			t.Errorf("Args(postgres, %s) = %v, %v", vec.JSON, pg, err)
		} else {
			for i, want := range vec.Postgres {
				if got, isText := pg[i].(string); (want == nil) != (pg[i] == nil) || (want != nil && (!isText || got != *want)) {
					t.Errorf("node-postgres sends value %d of %s as %s, got %#v", i, vec.JSON, show(want), pg[i])
				}
			}
		}

		args, err := Args(store.SQLite, 2, values...)
		if vec.SQLite.Error != "" {
			_, message, _ := strings.Cut(vec.SQLite.Error, ": ")
			if err == nil || Message(err) != message {
				t.Errorf("better-sqlite3 refuses %s with %q, got %v", vec.JSON, message, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("Args(sqlite, %s): %v", vec.JSON, err)
			continue
		}
		var a, b Cell
		if err := db.QueryRow(ctx, "SELECT ? AS a, ? AS b", args...).Scan(&a, &b); err != nil {
			t.Errorf("bind %s: %v", vec.JSON, err)
			continue
		}
		gotA, _ := a.MarshalJSON()
		gotB, _ := b.MarshalJSON()
		if string(gotA) != vec.SQLite.A || string(gotB) != vec.SQLite.B {
			t.Errorf("SQLite got %s as %s and %s, want %s and %s", vec.JSON, gotA, gotB, vec.SQLite.A, vec.SQLite.B)
		}
	}
}

func TestMembersOfAnObject(t *testing.T) {
	v := valueOf(t, `{"length":0,"name":"x","list":[1,2]}`)
	if !v.IsObject() || v.IsArray() || v.Member("length").ToNumber() != 0 || !v.Member("length").IsNumber() {
		t.Errorf("length of %v", v)
	}
	if v.Member("name").Str() != "x" || len(v.Member("list").Items()) != 2 {
		t.Errorf("members of %v", v)
	}
	if !v.Member("missing").IsUndefined() || !String("x").Member("length").IsUndefined() {
		t.Error("a missing member, and a member of a text, is undefined")
	}
}

func TestOrTakesTheFirstValueThatIsNotEmpty(t *testing.T) {
	fallback := String("fallback")
	for _, empty := range []Value{Undefined, Null, Bool(false), Number(0), Number(math.NaN()), String("")} {
		if got := empty.Or(fallback); got.Str() != "fallback" {
			t.Errorf("%#v || fallback = %#v", empty, got)
		}
	}
	for _, full := range []Value{Bool(true), Number(-1), String("0"), valueOf(t, "[]"), valueOf(t, "{}")} {
		if got := full.Or(fallback); got.IsString() && got.Str() == "fallback" {
			t.Errorf("%#v || fallback took the fallback", full)
		}
	}
}

func TestCell(t *testing.T) {
	tests := []struct {
		in     any
		json   string
		truthy bool
	}{
		{nil, "null", false},
		{int64(0), "0", false},
		{int64(12), "12", true},
		{int64(-9007199254740993), "-9007199254740992", true},
		{int64(1) << 62, "4611686018427388000", true},
		{1.5, "1.5", true},
		{1e21, "1e+21", true},
		{math.Inf(1), "null", true},
		{"abc", `"abc"`, true},
		{"", `""`, false},
		{[]byte("bytes"), `"bytes"`, true},
		{true, "true", true},
		{false, "false", false},
	}
	for _, tt := range tests {
		var c Cell
		if err := c.Scan(tt.in); err != nil {
			t.Errorf("Scan(%#v): %v", tt.in, err)
			continue
		}
		got, _ := c.MarshalJSON()
		if string(got) != tt.json || c.Value().Truthy() != tt.truthy {
			t.Errorf("Cell(%#v) = %s, truthy %v; want %s, %v", tt.in, got, c.Value().Truthy(), tt.json, tt.truthy)
		}
	}
	var c Cell
	if err := c.Scan(struct{}{}); err == nil {
		t.Error("Scan took a struct")
	}
	if got, _ := CellOf(float64(9)).MarshalJSON(); string(got) != "9" {
		t.Errorf("CellOf(9.0) = %s", got)
	}
}

func FuzzParseNumber(f *testing.F) {
	for _, seed := range []string{"", "12", " 1.5e3 ", "0x1F", "0b101", "0o17", "-Infinity", "1e400", "abc", "\u00a01\ufeff", "1_0", "0x1p3", ".", "+"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, s string) {
		n := ParseNumber(s)
		text := NumberString(n)
		if math.IsNaN(n) {
			return
		}
		// A number reads back as itself, and white space around the text
		// does not change it
		if back := ParseNumber(text); back != n {
			t.Errorf("Number(%q) = %s, which reads back as %v", s, text, back)
		}
		if padded := ParseNumber(" \t" + s + "\n"); padded != n {
			t.Errorf("Number(%q) = %v, with white space %v", s, n, padded)
		}
		// Only digits, signs, a dot, an exponent, Infinity or a base
		// prefix make a number
		for _, r := range Trim(s) {
			if !strings.ContainsRune("0123456789+-.eEInfityxXoObBaAcCdDfF", r) {
				t.Errorf("Number(%q) = %v, but %q is in no number", s, n, r)
			}
		}
	})
}
