package automation

import (
	"math"
	"net/http"
	"strings"

	"github.com/unitronix/betterdesk-server/console/goapipath"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsjson"
	"github.com/unitronix/betterdesk-server/console/store"
)

// given is a value of a request: as the statements bind it, and as the
// JSON an answer repeats it with.
type given struct {
	value jsbind.Value
	json  []byte
}

// member is body[key].
func member(body middleware.JSONObject, key string) given {
	raw := body.Get(key)
	g := given{value: jsbind.Of(raw)}
	if raw.Exists() {
		g.json = stringify(raw)
	}
	return g
}

// constant is a value the route fills in itself.
func constant(v jsbind.Value) given { return given{value: v, json: []byte(v.JSON())} }

// text is the text s.
func text(s string) given { return constant(jsbind.String(s)) }

// or is g || fallback.
func (g given) or(fallback given) given {
	if g.value.Truthy() {
		return g
	}
	return fallback
}

// trimmed is g.trim() for a text. Half of a surrogate pair stays half in
// the answer and is the replacement character in the database.
func (g given) trimmed() given {
	out := given{value: jsbind.String(jsbind.Trim(g.value.Str()))}
	out.json = []byte(out.value.JSON())
	if v, err := jsjson.Parse(string(g.json)); err == nil && v.Kind() == jsjson.String {
		out.json = jsjson.AppendQuoted(nil, jsbind.Trim(v.Str()))
	}
	return out
}

// MarshalJSON implements json.Marshaler: JSON.stringify of the value.
func (g given) MarshalJSON() ([]byte, error) {
	if len(g.json) == 0 {
		return []byte("null"), nil
	}
	return g.json, nil
}

// stringify is JSON.stringify of a body value after JSON.parse: numbers as
// JavaScript prints them, an object's keys in JavaScript's order, of two
// equal keys the last.
func stringify(raw middleware.Value) []byte {
	v, err := jsjson.Parse(string(raw.Raw()))
	if err != nil {
		return []byte(jsbind.Of(raw).JSON())
	}
	out, _ := v.MarshalJSON()
	return out
}

// asText is String(v) for a body value. It fails where JavaScript throws:
// for an object with a member named toString, also inside a list.
func asText(raw middleware.Value) (string, error) {
	if v, err := jsjson.Parse(string(raw.Raw())); err == nil {
		if _, err := v.Text(); err != nil {
			return "", err
		}
	}
	return jsbind.Of(raw).ToString(), nil
}

// errNotAFunction is the TypeError of a method call on a value that has
// no such method: name.trim() for a name that is no text.
type errNotAFunction string

func (e errNotAFunction) Error() string { return string(e) + " is not a function" }

// turnedOff is enabled === false || enabled === 0 || enabled === '0' ||
// enabled === 'false'.
func turnedOff(v jsbind.Value) bool {
	switch {
	case v.IsString():
		return v.Str() == "0" || v.Str() == "false"
	case v.IsNumber():
		return v.ToNumber() == 0
	case v.IsUndefined(), v.IsNull(), v.IsArray(), v.IsObject():
		return false
	}
	return !v.Truthy()
}

// flag is a flag as a statement takes it: 1 or 0 on SQLite, true or false
// on PostgreSQL.
func flag(db *store.DB, on bool) jsbind.Value {
	if db.IsPostgres() {
		return jsbind.Bool(on)
	}
	if on {
		return jsbind.Number(1)
	}
	return jsbind.Number(0)
}

// queryValue is req.query[name] as text, a list or an object. The query
// ends at a "#".
func queryValue(r *http.Request, name string) jsbind.Value {
	query, _, _ := strings.Cut(r.URL.RawQuery, "#")
	return jsbind.Query(query, name)
}

// limit is "if (req.query.limit) Math.min(Math.max(parseInt(req.query.limit,
// 10) || 100, 1), 500)": the limit of a request, from 1 to 500 and 100 when
// the text gives 0 or no number, and false when the query gives none. It
// fails where JavaScript could not make text of the value.
func limit(r *http.Request) (n float64, ok bool, err error) {
	value := goapipath.Query(r, "limit")
	if !value.Truthy() {
		return 0, false, nil
	}
	text, err := value.Text()
	if err != nil {
		return 0, false, err
	}
	return clampLimit(parseInt10(text)), true, nil
}

// clampLimit is Math.min(Math.max(n || 100, 1), 500).
func clampLimit(n float64) float64 {
	if n == 0 || math.IsNaN(n) {
		n = 100
	}
	return math.Min(math.Max(n, 1), 500)
}

// parseInt10 is parseInt(text, 10): the whole number the text starts
// with, after white space and a sign, and NaN when it starts with none.
// "0x10" is 0, where parseInt without a base reads a hexadecimal number.
func parseInt10(text string) float64 {
	digits := strings.TrimLeftFunc(text, jsbind.IsSpace)
	if digits != "" && (digits[0] == '+' || digits[0] == '-') {
		digits = digits[1:]
	}
	if len(digits) > 1 && digits[0] == '0' && (digits[1] == 'x' || digits[1] == 'X') {
		return 0
	}
	return goapipath.ParseInt(text)
}

// counts reports whether a limit limits: "if (filters.limit)" for a
// number.
func counts(limit float64) bool { return limit != 0 && !math.IsNaN(limit) }
