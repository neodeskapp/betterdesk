package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The value types in this file make a row look in JSON as it always did.
// The console sent rows to the browser the way its database drivers
// returned them, so the JSON of a column depends on the column's type:
// a time is the stored text on SQLite and an ISO string on PostgreSQL, a
// flag is 0 or 1 in an integer column and true or false in a boolean
// column, a PostgreSQL bigint is a string. schema.md has the full table.
//
// A value has to know the type of the column it was read from. Select and
// SelectRow pass that along; rows read with DB.Query do not carry it, and
// the types that need it refuse to scan there.

// Rows is a result set whose Scan tells the value types of this package
// which column type each value comes from.
type Rows struct {
	*sql.Rows
	dialect Dialect
	sources []source
}

// Select reads rows like Querier.Query and returns them as Rows.
func Select(ctx context.Context, q Querier, query string, args ...any) (*Rows, error) {
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	return &Rows{Rows: rows, dialect: q.Dialect()}, nil
}

// Scan copies the current row into dest, like sql.Rows.Scan.
func (r *Rows) Scan(dest ...any) error {
	if r.sources == nil {
		types, err := r.Rows.ColumnTypes()
		if err != nil {
			return fmt.Errorf("read the column types: %w", err)
		}
		r.sources = make([]source, len(types))
		for i, t := range types {
			r.sources[i] = source{known: true, dialect: r.dialect, typ: driverColumnType(r.dialect, t.DatabaseTypeName())}
		}
	}
	for i, d := range dest {
		if aware, ok := d.(columnAware); ok && i < len(r.sources) {
			aware.setSource(r.sources[i])
		}
	}
	return r.Rows.Scan(dest...)
}

// Row is the result of SelectRow.
type Row struct {
	rows *Rows
	err  error
}

// SelectRow reads at most one row, like Querier.QueryRow. Scan returns
// sql.ErrNoRows when there is none.
func SelectRow(ctx context.Context, q Querier, query string, args ...any) *Row {
	rows, err := Select(ctx, q, query, args...)
	return &Row{rows: rows, err: err}
}

// Scan copies the row into dest and releases the result set.
func (r *Row) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	defer r.rows.Close()
	if !r.rows.Next() {
		if err := r.rows.Err(); err != nil {
			return err
		}
		return sql.ErrNoRows
	}
	if err := r.rows.Scan(dest...); err != nil {
		return err
	}
	return r.rows.Close()
}

// source is the column a value is being read from.
type source struct {
	known   bool
	dialect Dialect
	typ     ColumnType
}

type columnAware interface {
	setSource(source)
}

// errNoSource is returned when a value cannot tell what to become without
// its column type.
func errNoSource(kind string, src any) error {
	return fmt.Errorf("store: cannot scan %T into %s without the column type: read the row with store.Select or store.SelectRow", src, kind)
}

// driverColumnType maps the type name a driver reports for a result column
// to a ColumnType.
func driverColumnType(dialect Dialect, name string) ColumnType {
	name = strings.ToUpper(name)
	if dialect == SQLite {
		if name == "" {
			return TypeOther
		}
		return sqliteColumnType(name)
	}
	switch name {
	case "INT2", "INT4", "OID":
		return TypeInteger
	case "INT8":
		return TypeBigInt
	case "BOOL":
		return TypeBoolean
	case "TEXT", "VARCHAR", "BPCHAR", "NAME":
		return TypeText
	case "FLOAT4":
		return TypeReal
	case "FLOAT8":
		return TypeDouble
	case "NUMERIC":
		return TypeNumeric
	case "BYTEA":
		return TypeBlob
	case "TIMESTAMPTZ":
		return TypeTimestampTZ
	case "TIMESTAMP":
		return TypeTimestamp
	case "DATE":
		return TypeDate
	case "JSON":
		return TypeJSON
	case "JSONB":
		return TypeJSONB
	}
	return TypeOther
}

// ---- Time ----

// Time is a time column.
//
// On SQLite times are text and the JSON is that text, whatever format it
// has. On PostgreSQL the JSON is the instant in UTC with milliseconds,
// "2026-11-10T01:05:51.694Z". A PostgreSQL column without time zone is read
// as local time of this process, as the console always did. NULL is null.
type Time struct {
	text    string
	instant time.Time
	state   timeState
	src     source
}

type timeState uint8

const (
	timeNull timeState = iota
	timeText
	timeInstant
)

func (t *Time) setSource(s source) { t.src = s }

// Scan implements sql.Scanner.
func (t *Time) Scan(src any) error {
	col := t.src
	*t = Time{}
	switch v := src.(type) {
	case nil:
		return nil
	case string:
		t.setText(v, col)
		return nil
	case []byte:
		t.setText(string(v), col)
		return nil
	case time.Time:
		if !col.known {
			return errNoSource("store.Time", src)
		}
		switch {
		case col.dialect == SQLite:
			// The driver parsed the text of a column declared DATETIME.
			t.state, t.text, t.instant = timeInstant, SQLiteTime(v), v
		case col.typ == TypeTimestamp || col.typ == TypeDate:
			local := LocalWallClock(v, time.Local)
			t.state, t.text, t.instant = timeInstant, ISOTime(local), local
		default:
			t.state, t.text, t.instant = timeInstant, ISOTime(v), v
		}
		return nil
	}
	return fmt.Errorf("store: cannot scan %T into store.Time", src)
}

func (t *Time) setText(text string, col source) {
	if col.known && col.dialect == Postgres && isTimeType(col.typ) && (text == "infinity" || text == "-infinity") {
		// An infinite timestamp was the number Infinity, which JSON has no
		// word for.
		return
	}
	t.state, t.text = timeText, text
}

func isTimeType(t ColumnType) bool {
	return t == TypeTimestampTZ || t == TypeTimestamp || t == TypeDate
}

// IsNull reports whether the column was NULL.
func (t Time) IsNull() bool { return t.state == timeNull }

// String returns the text that goes into JSON, empty for NULL.
func (t Time) String() string { return t.text }

// Instant returns the point in time, and false for NULL and for text that
// is none of the formats the console wrote.
func (t Time) Instant() (time.Time, bool) {
	switch t.state {
	case timeInstant:
		return t.instant, true
	case timeText:
		return ParseTime(t.text)
	}
	return time.Time{}, false
}

// MarshalJSON implements json.Marshaler.
func (t Time) MarshalJSON() ([]byte, error) {
	if t.state == timeNull {
		return []byte("null"), nil
	}
	return jsQuote(nil, t.text), nil
}

// LocalWallClock takes the date and clock reading of t and returns the
// instant at which a clock in loc shows them. A PostgreSQL timestamp
// without time zone was read this way, with the zone of the process.
func LocalWallClock(t time.Time, loc *time.Location) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), loc)
}

// SQLiteTime formats t as SQLite's datetime('now') does: UTC, seconds, no
// zone, "2026-11-10 01:05:50". A fraction of a second is kept when t has
// one.
func SQLiteTime(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05.999999999")
}

// ISOTime formats t as JavaScript's Date.toISOString does: UTC with
// milliseconds, "2026-11-10T01:05:51.694Z". What is left after the last
// full millisecond is dropped, never rounded up, which is how the console's
// driver turned PostgreSQL's microseconds into a Date.
func ISOTime(t time.Time) string {
	t = t.UTC()
	if y := t.Year(); y < 0 || y > 9999 {
		sign := '+'
		if y < 0 {
			sign, y = '-', -y
		}
		return fmt.Sprintf("%c%06d%s", sign, y, t.Format("-01-02T15:04:05.000Z"))
	}
	return t.Format("2006-01-02T15:04:05.000Z")
}

// DateTimeMillis formats t as the console did for expiry times it computed
// itself: UTC with milliseconds and no zone, "2026-11-17 01:05:51.694".
func DateTimeMillis(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05.000")
}

// NowSQL is the SQL expression for the current time that gives a row the
// value the console's own statements gave it: datetime('now') on SQLite,
// NOW() on PostgreSQL.
func NowSQL(dialect Dialect) string {
	if dialect == Postgres {
		return "NOW()"
	}
	return "datetime('now')"
}

// timeFormats are the forms of time text in the console's SQLite tables.
var timeFormats = []string{
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05.999999999Z07:00",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05.999999999Z07:00",
}

// ParseTime reads time text as the console's SQLite tables hold it. Text
// without a zone is UTC.
func ParseTime(text string) (time.Time, bool) {
	for _, layout := range timeFormats {
		if t, err := time.Parse(layout, text); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// ---- Bool ----

// Bool is a flag column. The JSON is what the column holds: 0 or 1 from an
// integer column (every flag on SQLite, and a few on PostgreSQL), true or
// false from a PostgreSQL boolean column, null for NULL.
type Bool struct {
	state boolState
	n     int64
}

type boolState uint8

const (
	boolNull boolState = iota
	boolInteger
	boolBoolean
)

// Scan implements sql.Scanner.
func (b *Bool) Scan(src any) error {
	*b = Bool{}
	switch v := src.(type) {
	case nil:
	case bool:
		b.state = boolBoolean
		if v {
			b.n = 1
		}
	case int64:
		b.state, b.n = boolInteger, v
	default:
		return fmt.Errorf("store: cannot scan %T into store.Bool", src)
	}
	return nil
}

// IsNull reports whether the column was NULL.
func (b Bool) IsNull() bool { return b.state == boolNull }

// True reports whether the flag is set: true, or a number other than 0.
func (b Bool) True() bool { return b.n != 0 }

// MarshalJSON implements json.Marshaler.
func (b Bool) MarshalJSON() ([]byte, error) {
	switch b.state {
	case boolNull:
		return []byte("null"), nil
	case boolBoolean:
		return strconv.AppendBool(nil, b.n != 0), nil
	}
	return strconv.AppendInt(nil, b.n, 10), nil
}

// BoolArg is the parameter to write a flag into a column of the given
// type: true or false for a boolean column, 1 or 0 for an integer one.
func BoolArg(column ColumnType, value bool) any {
	if column == TypeBoolean {
		return value
	}
	if value {
		return 1
	}
	return 0
}

// ---- Int64String ----

// Int64String is an integer column or a count. The JSON is a number,
// except for PostgreSQL bigint and numeric values, which were strings:
// ids of tables the signal server created, COUNT(*) and SUM without a
// cast, byte counts. NULL is null.
type Int64String struct {
	V        int64
	Valid    bool
	asString bool
	text     string
	src      source
}

// NewInt64String returns a value that marshals as one read from a column
// of the given type would.
func NewInt64String(v int64, column ColumnType) Int64String {
	return Int64String{V: v, Valid: true, asString: column == TypeBigInt || column == TypeNumeric}
}

func (n *Int64String) setSource(s source) { n.src = s }

// Scan implements sql.Scanner.
func (n *Int64String) Scan(src any) error {
	col := n.src
	*n = Int64String{}
	switch v := src.(type) {
	case nil:
		return nil
	case int64:
		if !col.known {
			return errNoSource("store.Int64String", src)
		}
		n.V, n.Valid = v, true
		n.asString = col.dialect == Postgres && (col.typ == TypeBigInt || col.typ == TypeNumeric)
		return nil
	case string:
		// A PostgreSQL numeric arrives as text and was sent as that text.
		if !col.known {
			return errNoSource("store.Int64String", src)
		}
		n.Valid, n.asString, n.text = true, true, v
		if i, err := strconv.ParseInt(v, 10, 64); err == nil {
			n.V = i
		} else if f, err := strconv.ParseFloat(v, 64); err == nil {
			n.V = int64(f)
		}
		return nil
	}
	return fmt.Errorf("store: cannot scan %T into store.Int64String", src)
}

// MarshalJSON implements json.Marshaler.
func (n Int64String) MarshalJSON() ([]byte, error) {
	switch {
	case !n.Valid:
		return []byte("null"), nil
	case n.text != "":
		return jsQuote(nil, n.text), nil
	case n.asString:
		return strconv.AppendQuote(nil, strconv.FormatInt(n.V, 10)), nil
	}
	return strconv.AppendInt(nil, n.V, 10), nil
}

// ---- Float ----

// Float is a REAL or double precision column. A PostgreSQL real has four
// bytes, and the JSON has the short form of that number (0.1, not
// 0.10000000149011612). NULL and values that are not numbers are null.
type Float struct {
	V      float64
	Valid  bool
	single bool
	src    source
}

func (f *Float) setSource(s source) { f.src = s }

// Scan implements sql.Scanner.
func (f *Float) Scan(src any) error {
	col := f.src
	*f = Float{}
	switch v := src.(type) {
	case nil:
		return nil
	case float64:
		if !col.known {
			return errNoSource("store.Float", src)
		}
		f.V, f.Valid = v, true
		f.single = col.dialect == Postgres && col.typ == TypeReal
		return nil
	case int64:
		f.V, f.Valid = float64(v), true
		return nil
	}
	return fmt.Errorf("store: cannot scan %T into store.Float", src)
}

// MarshalJSON implements json.Marshaler.
func (f Float) MarshalJSON() ([]byte, error) {
	if !f.Valid || math.IsNaN(f.V) || math.IsInf(f.V, 0) {
		return []byte("null"), nil
	}
	if f.V == 0 {
		return []byte("0"), nil
	}
	if f.single {
		return json.Marshal(float32(f.V))
	}
	return json.Marshal(f.V)
}

// ---- JSON columns ----

// JSONText is a column that holds JSON, sent the way the driver returned
// it: the stored text as a JSON string when the column is text (always on
// SQLite), the parsed value when the column is PostgreSQL json or jsonb.
// Use it where the console passed the column through untouched; use
// JSONParsed where it parsed the text first.
type JSONText struct {
	state jsonState
	text  string
	src   source
}

type jsonState uint8

const (
	jsonNull jsonState = iota
	jsonString
	jsonValue
)

func (j *JSONText) setSource(s source) { j.src = s }

// Scan implements sql.Scanner.
func (j *JSONText) Scan(src any) error {
	col := j.src
	*j = JSONText{}
	var text string
	switch v := src.(type) {
	case nil:
		return nil
	case string:
		text = v
	case []byte:
		text = string(v)
	default:
		return fmt.Errorf("store: cannot scan %T into store.JSONText", src)
	}
	if !col.known {
		return errNoSource("store.JSONText", src)
	}
	j.text, j.state = text, jsonString
	if col.dialect == Postgres && (col.typ == TypeJSON || col.typ == TypeJSONB) {
		j.state = jsonValue
	}
	return nil
}

// IsNull reports whether the column was NULL.
func (j JSONText) IsNull() bool { return j.state == jsonNull }

// Text returns the JSON text of the column, empty for NULL.
func (j JSONText) Text() string { return j.text }

// Parsed returns the column as a parsed value, see JSONParsed.
func (j JSONText) Parsed(fallback string) JSONParsed {
	return JSONParsed{Fallback: fallback, state: j.state, text: j.text}
}

// MarshalJSON implements json.Marshaler.
func (j JSONText) MarshalJSON() ([]byte, error) {
	switch j.state {
	case jsonNull:
		return []byte("null"), nil
	case jsonString:
		return jsQuote(nil, j.text), nil
	}
	return jsRestringify(j.text)
}

// JSONParsed is a column that holds JSON, sent as the parsed value on both
// databases. Fallback is the JSON to send when the text does not parse
// ("[]", "{}"); empty means null. NULL is null.
type JSONParsed struct {
	Fallback string
	state    jsonState
	text     string
	src      source
}

func (j *JSONParsed) setSource(s source) { j.src = s }

// Scan implements sql.Scanner.
func (j *JSONParsed) Scan(src any) error {
	col := j.src
	*j = JSONParsed{Fallback: j.Fallback}
	switch v := src.(type) {
	case nil:
		return nil
	case string:
		j.text = v
	case []byte:
		j.text = string(v)
	default:
		return fmt.Errorf("store: cannot scan %T into store.JSONParsed", src)
	}
	j.state = jsonString
	if col.known && col.dialect == Postgres && (col.typ == TypeJSON || col.typ == TypeJSONB) {
		j.state = jsonValue
	}
	return nil
}

// IsNull reports whether the column was NULL.
func (j JSONParsed) IsNull() bool { return j.state == jsonNull }

// MarshalJSON implements json.Marshaler.
func (j JSONParsed) MarshalJSON() ([]byte, error) {
	fallback := j.Fallback
	if fallback == "" {
		fallback = "null"
	}
	switch j.state {
	case jsonNull:
		return []byte("null"), nil
	case jsonValue:
		// The driver had parsed the column already. An object or array went
		// out as it was; a string was parsed once more, as text.
		text := strings.TrimSpace(j.text)
		if !strings.HasPrefix(text, `"`) {
			return jsRestringify(text)
		}
		var inner string
		if err := json.Unmarshal([]byte(text), &inner); err != nil {
			return []byte(fallback), nil
		}
		if out, err := jsRestringify(inner); err == nil {
			return out, nil
		}
		return []byte(fallback), nil
	}
	if out, err := jsRestringify(j.text); err == nil {
		return out, nil
	}
	return []byte(fallback), nil
}

// jsRestringify returns what JSON.stringify(JSON.parse(text)) returns in
// JavaScript: no white space, numbers as doubles, keys that are array
// indexes first, of two equal keys the last value.
func jsRestringify(text string) ([]byte, error) {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	out, err := jsValue(dec, nil)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("store: more than one JSON value")
	}
	return out, nil
}

func jsValue(dec *json.Decoder, buf []byte) ([]byte, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch v := tok.(type) {
	case nil:
		return append(buf, "null"...), nil
	case bool:
		return strconv.AppendBool(buf, v), nil
	case string:
		return jsQuote(buf, v), nil
	case json.Number:
		f, err := v.Float64()
		if err != nil && !math.IsInf(f, 0) {
			return nil, err
		}
		return jsNumber(buf, f), nil
	case json.Delim:
		switch v {
		case '[':
			buf = append(buf, '[')
			for first := true; dec.More(); first = false {
				if !first {
					buf = append(buf, ',')
				}
				if buf, err = jsValue(dec, buf); err != nil {
					return nil, err
				}
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return append(buf, ']'), nil
		case '{':
			return jsObject(dec, buf)
		}
	}
	return nil, fmt.Errorf("store: unexpected JSON token %v", tok)
}

type jsMember struct {
	key   string
	index int64 // the key as an array index, -1 when it is none
	value []byte
}

func jsObject(dec *json.Decoder, buf []byte) ([]byte, error) {
	var members []jsMember
	at := map[string]int{}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		value, err := jsValue(dec, nil)
		if err != nil {
			return nil, err
		}
		if i, ok := at[key]; ok {
			members[i].value = value
			continue
		}
		at[key] = len(members)
		members = append(members, jsMember{key: key, index: arrayIndex(key), value: value})
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	sort.SliceStable(members, func(a, b int) bool {
		ia, ib := members[a].index, members[b].index
		if ia >= 0 && ib >= 0 {
			return ia < ib
		}
		return ia >= 0 && ib < 0
	})
	buf = append(buf, '{')
	for i, m := range members {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = jsQuote(buf, m.key)
		buf = append(buf, ':')
		buf = append(buf, m.value...)
	}
	return append(buf, '}'), nil
}

// arrayIndex returns the number a JavaScript object key stands for when
// the key is an array index (0 to 4294967294, written without a leading
// zero), and -1 otherwise.
func arrayIndex(key string) int64 {
	if key == "" || len(key) > 10 || (len(key) > 1 && key[0] == '0') {
		return -1
	}
	n, err := strconv.ParseInt(key, 10, 64)
	if err != nil || n < 0 || n > 4294967294 || key[0] == '+' || key[0] == '-' {
		return -1
	}
	return n
}

// jsNumber appends f the way JavaScript prints a number in JSON.
func jsNumber(buf []byte, f float64) []byte {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return append(buf, "null"...)
	}
	if f == 0 {
		return append(buf, '0')
	}
	out, err := json.Marshal(f)
	if err != nil {
		return append(buf, "null"...)
	}
	return append(buf, out...)
}

// jsQuote appends s as a JSON string the way JSON.stringify writes one:
// only the quote, the backslash and control characters are escaped.
func jsQuote(buf []byte, s string) []byte {
	const hex = "0123456789abcdef"
	buf = append(buf, '"')
	for i := 0; i < len(s); {
		c := s[i]
		if c < utf8.RuneSelf {
			switch {
			case c == '"' || c == '\\':
				buf = append(buf, '\\', c)
			case c == '\b':
				buf = append(buf, '\\', 'b')
			case c == '\f':
				buf = append(buf, '\\', 'f')
			case c == '\n':
				buf = append(buf, '\\', 'n')
			case c == '\r':
				buf = append(buf, '\\', 'r')
			case c == '\t':
				buf = append(buf, '\\', 't')
			case c < 0x20:
				buf = append(buf, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xf])
			default:
				buf = append(buf, c)
			}
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			buf = append(buf, "\ufffd"...)
		} else {
			buf = append(buf, s[i:i+size]...)
		}
		i += size
	}
	return append(buf, '"')
}

// ---- NullString ----

// NullString is a text column that can be NULL. The JSON is the text, or
// null for NULL. The console often sent something else for the empty
// cases; OrEmpty and OrNull are those two habits.
type NullString struct {
	String string
	Valid  bool
}

// Scan implements sql.Scanner.
func (n *NullString) Scan(src any) error {
	*n = NullString{}
	switch v := src.(type) {
	case nil:
	case string:
		n.String, n.Valid = v, true
	case []byte:
		n.String, n.Valid = string(v), true
	default:
		return fmt.Errorf("store: cannot scan %T into store.NullString", src)
	}
	return nil
}

// OrEmpty returns the text, and "" for NULL, where the console wrote the
// value or else an empty string.
func (n NullString) OrEmpty() string { return n.String }

// OrNull returns the value with the empty text turned into NULL, where the
// console wrote the value or else null.
func (n NullString) OrNull() NullString {
	if n.String == "" {
		return NullString{}
	}
	return n
}

// MarshalJSON implements json.Marshaler.
func (n NullString) MarshalJSON() ([]byte, error) {
	if !n.Valid {
		return []byte("null"), nil
	}
	return jsQuote(nil, n.String), nil
}
