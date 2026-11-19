package automation

import (
	"net/http"
	"strconv"
	"strings"

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

// filterOf reads a filter of a list from the query: one text, "" when the
// query does not name it. ok is false when the filter is no single text
// (given twice, in brackets or with a key).
func filterOf(r *http.Request, name string) (text string, ok bool) {
	text, _, err := jsbind.QueryText(r.URL.RawQuery, name)
	return text, err == nil
}

// filterInto names a filter of a list and where its text goes.
type filterInto struct {
	name string
	text *string
}

// readFilters reads the filters in the order given and answers 400
// "Invalid <name>" for the first that is no single text.
func readFilters(w http.ResponseWriter, r *http.Request, filters ...filterInto) bool {
	for _, f := range filters {
		text, ok := filterOf(r, f.name)
		if !ok {
			fail(w, r, http.StatusBadRequest, "Invalid "+f.name)
			return false
		}
		*f.text = text
	}
	return true
}

// maxRows is the most rows a list answers.
const maxRows = 500

// limitOf reads the limit of a list from the query. Without one, or with
// an empty one, the list is not limited (n is 0). Otherwise it has to be
// a whole number from 1, written in digits; a larger one than maxRows is
// maxRows. ok is false for anything else.
func limitOf(r *http.Request) (n int, ok bool) {
	text, ok := filterOf(r, "limit")
	if !ok || text == "" {
		return 0, ok
	}
	if strings.Trim(text, "0123456789") != "" || strings.Trim(text, "0") == "" {
		return 0, false
	}
	value, err := strconv.Atoi(text)
	if err != nil || value > maxRows {
		return maxRows, true
	}
	return value, true
}
