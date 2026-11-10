package reports

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/store"
)

// The engine computes with the values the old console's JavaScript had:
// rows as its database drivers handed them over and JSON as JSON.parse
// made it. A value is one of
//
//	nil             null
//	bool            a flag
//	float64         a number
//	string          a text
//	[]any           an array
//	*goapi.Object   an object, its keys in JavaScript's order
//	Date            a PostgreSQL time, which node-postgres made a Date
//	Buffer          a BLOB or bytea, which the drivers made a Buffer
//
// Stringify writes a value as JSON.stringify did, and String (of a
// clock) as String(value) did, which for a Date depends on the time zone
// of the process.

// Date is a time read from PostgreSQL. In JSON it is the instant in UTC
// with milliseconds; as text it is the long local form of Date.toString.
type Date struct {
	T time.Time
}

// Buffer is a BLOB on SQLite or a bytea on PostgreSQL.
type Buffer []byte

// Stringify is JSON.stringify(v).
func Stringify(v any) []byte { return appendJSON(nil, v) }

func appendJSON(buf []byte, v any) []byte {
	switch t := v.(type) {
	case nil:
		return append(buf, "null"...)
	case bool:
		return strconv.AppendBool(buf, t)
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return append(buf, "null"...)
		}
		return append(buf, jsbind.NumberString(t)...)
	case string:
		return appendQuoted(buf, t)
	case []any:
		buf = append(buf, '[')
		for i, item := range t {
			if i > 0 {
				buf = append(buf, ',')
			}
			buf = appendJSON(buf, item)
		}
		return append(buf, ']')
	case *goapi.Object:
		if t == nil {
			return append(buf, "null"...)
		}
		buf = append(buf, '{')
		for i, key := range t.Keys() {
			if i > 0 {
				buf = append(buf, ',')
			}
			buf = appendQuoted(buf, key)
			buf = append(buf, ':')
			buf = appendJSON(buf, t.Get(key))
		}
		return append(buf, '}')
	case Date:
		return appendQuoted(buf, store.ISOTime(t.T))
	case Buffer:
		buf = append(buf, `{"type":"Buffer","data":[`...)
		for i, b := range t {
			if i > 0 {
				buf = append(buf, ',')
			}
			buf = strconv.AppendInt(buf, int64(b), 10)
		}
		return append(buf, "]}"...)
	}
	panic(fmt.Sprintf("reports: %T is no JavaScript value", v))
}

// appendQuoted writes s as JSON.stringify writes a text: only the quote,
// the backslash and control characters are escaped.
func appendQuoted(buf []byte, s string) []byte {
	quoted, _ := store.NullString{String: s, Valid: true}.MarshalJSON()
	return append(buf, quoted...)
}

// Truthy is !!v.
func Truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case float64:
		return t != 0 && !math.IsNaN(t)
	case string:
		return t != ""
	}
	return true
}

// isObject is typeof v === 'object' && v !== null: arrays, objects,
// dates and buffers.
func isObject(v any) bool {
	switch v.(type) {
	case []any, *goapi.Object, Date, Buffer:
		return true
	}
	return false
}

// ParseJSON is JSON.parse(text).
func ParseJSON(text string) (any, error) {
	return goapi.Parse([]byte(text))
}

// prop is v[key] for a member that is not inherited, with false for
// undefined. Only objects have such members.
func prop(v any, key string) (any, bool) {
	if o, ok := v.(*goapi.Object); ok && o.Has(key) {
		return o.Get(key), true
	}
	return nil, false
}

// or is a || b, where an undefined a (found false) counts as false.
func or(a any, found bool, b any) any {
	if found && Truthy(a) {
		return a
	}
	return b
}

// bufferText is buf.toString(): the bytes read as UTF-8.
func bufferText(b Buffer) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var out strings.Builder
	for len(b) > 0 {
		r, size := utf8.DecodeRune(b)
		out.WriteRune(r)
		b = b[size:]
	}
	return out.String()
}

// clock knows the time zone of the process, which String(date) uses.
type clock struct {
	loc      *time.Location
	zoneName func(time.Time) string
}

// String is String(v).
func (c clock) String(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return jsbind.NumberString(t)
	case string:
		return t
	case []any:
		parts := make([]string, len(t))
		for i, item := range t {
			if item != nil {
				parts[i] = c.String(item)
			}
		}
		return strings.Join(parts, ",")
	case *goapi.Object:
		return "[object Object]"
	case Date:
		return jsDateString(t.T.In(c.loc), c.zoneName)
	case Buffer:
		return bufferText(t)
	}
	return fmt.Sprint(v)
}

// Number is Number(v).
func (c clock) Number(v any) float64 {
	switch t := v.(type) {
	case nil:
		return 0
	case bool:
		if t {
			return 1
		}
		return 0
	case float64:
		return t
	case string:
		return jsbind.ParseNumber(t)
	case *goapi.Object:
		return math.NaN()
	case Date:
		return float64(t.T.UnixMilli())
	}
	return jsbind.ParseNumber(c.String(v))
}

// jsDateString is Date.prototype.toString: "Tue Nov 10 2026 01:05:51
// GMT+0100 (Central European Standard Time)". The name in brackets comes
// from zoneName.
func jsDateString(t time.Time, zoneName func(time.Time) string) string {
	year := t.Year()
	yearText := fmt.Sprintf("%04d", year)
	if year < 0 {
		yearText = fmt.Sprintf("-%06d", -year)
	}
	return t.Format("Mon Jan 02 ") + yearText + t.Format(" 15:04:05 GMT-0700") + " (" + zoneName(t) + ")"
}

// DefaultZoneName names the zone of t the way JavaScript does in a date,
// as far as this program knows the names: UTC is "Coordinated Universal
// Time", every other zone gets its abbreviation ("CET"), where
// JavaScript prints a long name from its own data ("Central European
// Standard Time").
func DefaultZoneName(t time.Time) string {
	name, offset := t.Zone()
	if offset == 0 && (name == "UTC" || name == "") {
		return "Coordinated Universal Time"
	}
	return name
}

// inherited holds, as text, the members every JavaScript object has from
// Object.prototype. An object used to count names reads them where a
// name is one of them.
var inherited = map[string]string{
	"constructor":          "function Object() { [native code] }",
	"__defineGetter__":     "function __defineGetter__() { [native code] }",
	"__defineSetter__":     "function __defineSetter__() { [native code] }",
	"hasOwnProperty":       "function hasOwnProperty() { [native code] }",
	"__lookupGetter__":     "function __lookupGetter__() { [native code] }",
	"__lookupSetter__":     "function __lookupSetter__() { [native code] }",
	"isPrototypeOf":        "function isPrototypeOf() { [native code] }",
	"propertyIsEnumerable": "function propertyIsEnumerable() { [native code] }",
	"toString":             "function toString() { [native code] }",
	"valueOf":              "function valueOf() { [native code] }",
	"toLocaleString":       "function toLocaleString() { [native code] }",
}

// isInherited reports whether every plain object already has a member of
// that name, __proto__ included.
func isInherited(name string) bool {
	_, ok := inherited[name]
	return ok || name == "__proto__"
}

// tally is a plain object that counts names:
//
//	counts[name] = (counts[name] || 0) + 1
//
// A name an object inherits does not count from 1: it starts as the text
// of the inherited function with "1" added, and each further one adds
// another "1". __proto__ never becomes a key. The keys come out in
// JavaScript's order: whole numbers first.
type tally struct {
	counts *goapi.Object
}

func newTally() *tally { return &tally{counts: goapi.NewObject()} }

func (t *tally) add(name string) {
	if name == "__proto__" {
		return
	}
	switch current := t.counts.Get(name).(type) {
	case float64:
		t.counts.Set(name, current+1)
	case string:
		t.counts.Set(name, current+"1")
	default:
		if text, ok := inherited[name]; ok {
			t.counts.Set(name, text+"1")
			return
		}
		t.counts.Set(name, float64(1))
	}
}

// fromDriver turns a value the Go driver read into the value the old
// console's driver gave for the same column: better-sqlite3 on SQLite,
// node-postgres on PostgreSQL. typeName is the column's type as the
// driver names it; local is the zone of the process, which node-postgres
// read times without a zone in.
func fromDriver(dialect store.Dialect, typeName string, v any, local *time.Location) any {
	typeName = strings.ToUpper(typeName)
	if dialect == store.SQLite {
		switch t := v.(type) {
		case int64:
			return float64(t)
		case []byte:
			return Buffer(append([]byte(nil), t...))
		case time.Time:
			return store.SQLiteTime(t)
		}
		return v
	}
	switch t := v.(type) {
	case int64:
		if typeName == "INT8" {
			return strconv.FormatInt(t, 10)
		}
		return float64(t)
	case float64:
		if typeName == "FLOAT4" {
			// node-postgres parsed the short text PostgreSQL writes for
			// a four-byte number
			f, _ := strconv.ParseFloat(strconv.FormatFloat(t, 'g', -1, 32), 64)
			return f
		}
		return t
	case string:
		return postgresText(typeName, t)
	case []byte:
		if typeName == "BYTEA" {
			return Buffer(append([]byte(nil), t...))
		}
		return postgresText(typeName, string(t))
	case time.Time:
		if typeName == "TIMESTAMP" || typeName == "DATE" {
			return Date{T: store.LocalWallClock(t, local)}
		}
		return Date{T: t}
	}
	return v
}

// postgresText is a column that arrived as text: JSON is parsed, an
// infinite time is a number.
func postgresText(typeName, text string) any {
	switch typeName {
	case "JSON", "JSONB":
		if parsed, err := goapi.Parse([]byte(text)); err == nil {
			return parsed
		}
	case "TIMESTAMPTZ", "TIMESTAMP", "DATE":
		switch text {
		case "infinity":
			return math.Inf(1)
		case "-infinity":
			return math.Inf(-1)
		}
	}
	return text
}
