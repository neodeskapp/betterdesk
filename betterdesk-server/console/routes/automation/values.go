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

// resultText is an agent's result as it is stored: text as it came, null
// as no result, anything else as its JSON as JavaScript writes it (keys in
// JavaScript's order, of two equal keys the last).
func resultText(raw middleware.Value) jsbind.Value {
	v := jsbind.Of(raw)
	if v.IsString() || v.IsNull() {
		return v
	}
	if parsed, err := jsjson.Parse(string(raw.Raw())); err == nil {
		if out, err := parsed.MarshalJSON(); err == nil {
			return jsbind.String(string(out))
		}
	}
	return jsbind.String(v.JSON())
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
