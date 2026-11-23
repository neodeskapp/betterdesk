package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
)

// cookieKey is the field of a stored session that holds the cookie.
const cookieKey = "cookie"

// fields is a JSON object that keeps the order of its keys and the exact
// text of its values, so a field this code does not know goes back to the
// database as it came.
type fields struct {
	keys   []string
	values map[string]json.RawMessage
}

func newFields() *fields {
	return &fields{values: map[string]json.RawMessage{}}
}

// parseFields reads a JSON object. When a key appears twice the last value
// counts, at the place of the first.
func parseFields(text []byte) (*fields, error) {
	dec := json.NewDecoder(bytes.NewReader(text))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, errors.New("not a JSON object")
	}
	f := newFields()
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, errors.New("not a JSON object")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		f.set(key, raw)
	}
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("text after the JSON object")
	}
	return f, nil
}

func (f *fields) get(key string) (json.RawMessage, bool) {
	raw, ok := f.values[key]
	return raw, ok
}

func (f *fields) set(key string, raw json.RawMessage) {
	if _, ok := f.values[key]; !ok {
		f.keys = append(f.keys, key)
	}
	f.values[key] = raw
}

func (f *fields) delete(key string) {
	if _, ok := f.values[key]; !ok {
		return
	}
	delete(f.values, key)
	for i, k := range f.keys {
		if k == key {
			f.keys = append(f.keys[:i], f.keys[i+1:]...)
			break
		}
	}
}

// encode writes the object, with or without the cookie field.
func (f *fields) encode(withCookie bool) string {
	var b strings.Builder
	b.WriteByte('{')
	first := true
	for _, key := range f.keys {
		if !withCookie && key == cookieKey {
			continue
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		b.Write(mustJSON(key))
		b.WriteByte(':')
		b.Write(f.values[key])
	}
	b.WriteByte('}')
	return b.String()
}

// mustJSON encodes a value this package built itself.
func mustJSON(v any) json.RawMessage {
	raw, err := httpx.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("session: value cannot be stored as JSON: %v", err))
	}
	return raw
}

// str reads a field that holds a JSON string, and "" for anything else.
func (f *fields) str(key string) string {
	raw, ok := f.values[key]
	if !ok || len(raw) == 0 || raw[0] != '"' {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

// num reads a field as JavaScript's Number() would: a number, a string
// that holds one, true as 1. Anything else is 0.
func (f *fields) num(key string) float64 {
	raw, ok := f.values[key]
	if !ok {
		return 0
	}
	return jsNumber(raw)
}

func jsNumber(raw json.RawMessage) float64 {
	text := strings.TrimSpace(string(raw))
	if text == "true" {
		return 1
	}
	if strings.HasPrefix(text, `"`) {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return 0
		}
		text = strings.Trim(s, jsWhitespace)
	}
	n, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0
	}
	return n
}

// millis reads a time field in milliseconds, 0 when it is not set.
func (f *fields) millis(key string) int64 {
	return toInt64(f.num(key))
}

func toInt64(n float64) int64 {
	if n >= math.MaxInt64 || n <= math.MinInt64 {
		return 0
	}
	return int64(n)
}

// truthy reports whether a field holds what JavaScript takes for true:
// anything but a missing field, null, false, 0 and "".
func (f *fields) truthy(key string) bool {
	raw, ok := f.values[key]
	if !ok {
		return false
	}
	switch text := strings.TrimSpace(string(raw)); {
	case text == "null", text == "false", text == `""`, text == "":
		return false
	case text[0] == '{', text[0] == '[', text[0] == '"', text == "true":
		return true
	default:
		return jsNumber(raw) != 0
	}
}

// cookieState is the "cookie" object of a stored session.
//
// originalMaxAge is the lifetime in milliseconds that every answer renews:
// the cookie's Expires and the row's expires_at become "now plus
// originalMaxAge". expires is the time written last. The other attributes
// are kept as they were stored.
type cookieState struct {
	hasMaxAge      bool
	originalMaxAge int64
	expires        time.Time
	rest           *fields
}

// newCookieState is the cookie of a new session.
func newCookieState(now time.Time, maxAgeMs int64, secure bool) cookieState {
	c := cookieState{rest: newFields()}
	c.rest.set("secure", mustJSON(secure))
	c.rest.set("httpOnly", mustJSON(true))
	c.rest.set("path", mustJSON("/"))
	c.rest.set("sameSite", mustJSON("lax"))
	c.setMaxAge(now, maxAgeMs)
	return c
}

// parseCookieState reads the cookie object of a stored session.
func parseCookieState(raw json.RawMessage) (cookieState, error) {
	text := bytes.TrimSpace(raw)
	if len(text) == 0 || (text[0] != '{' && text[0] != '[') {
		return cookieState{}, errors.New("the stored session has no cookie object")
	}
	c := cookieState{rest: newFields()}
	if text[0] == '[' {
		// An array carries no attributes
		text = []byte("{}")
	}
	obj, err := parseFields(text)
	if err != nil {
		return cookieState{}, fmt.Errorf("the stored session's cookie: %w", err)
	}
	if maxAge, ok := obj.get("originalMaxAge"); ok && isJSONNumber(maxAge) {
		c.hasMaxAge = true
		c.originalMaxAge = toInt64(jsNumber(maxAge))
	}
	if t, err := time.Parse(time.RFC3339Nano, obj.str("expires")); err == nil {
		c.expires = t
	}
	// A cookie is HttpOnly and for every path unless it says otherwise
	defaults := map[string]json.RawMessage{"httpOnly": mustJSON(true), "path": mustJSON("/")}
	for _, key := range []string{"secure", "httpOnly", "path", "sameSite"} {
		if value, ok := obj.get(key); ok {
			c.rest.set(key, value)
		} else if value, ok := defaults[key]; ok {
			c.rest.set(key, value)
		}
	}
	return c, nil
}

func isJSONNumber(raw json.RawMessage) bool {
	text := bytes.TrimSpace(raw)
	return len(text) > 0 && (text[0] == '-' || (text[0] >= '0' && text[0] <= '9'))
}

// setMaxAge gives the cookie a lifetime from now on.
func (c *cookieState) setMaxAge(now time.Time, ms int64) {
	c.hasMaxAge = true
	c.originalMaxAge = ms
	c.expires = now.Add(lifetime(ms))
}

// touch renews the cookie: it expires originalMaxAge from now. A cookie
// without a lifetime lasts until the browser closes.
func (c *cookieState) touch(now time.Time) {
	if !c.hasMaxAge {
		c.expires = time.Time{}
		return
	}
	c.expires = now.Add(lifetime(c.originalMaxAge))
}

func (c *cookieState) secure() bool   { return c.rest.truthy("secure") }
func (c *cookieState) httpOnly() bool { return c.rest.truthy("httpOnly") }
func (c *cookieState) path() string   { return c.rest.str("path") }

// sameSite is the SameSite attribute: the stored word, and "strict" for a
// stored true.
func (c *cookieState) sameSite() string {
	if raw, ok := c.rest.get("sameSite"); ok && strings.TrimSpace(string(raw)) == "true" {
		return "strict"
	}
	return c.rest.str("sameSite")
}

// encode writes the cookie object as it is stored: originalMaxAge, expires
// as an ISO time with milliseconds, then the attributes.
func (c *cookieState) encode() json.RawMessage {
	var b strings.Builder
	b.WriteString(`{"originalMaxAge":`)
	if c.hasMaxAge {
		b.WriteString(strconv.FormatInt(c.originalMaxAge, 10))
	} else {
		b.WriteString("null")
	}
	b.WriteString(`,"expires":`)
	if c.expires.IsZero() {
		b.WriteString("null")
	} else {
		b.Write(mustJSON(httpx.ISOTime(c.expires)))
	}
	if rest := c.rest.encode(true); rest != "{}" {
		b.WriteByte(',')
		b.WriteString(rest[1 : len(rest)-1])
	}
	b.WriteByte('}')
	return json.RawMessage(b.String())
}

// lifetime is a number of milliseconds as a duration. A number too large
// for a duration becomes the longest one.
func lifetime(ms int64) time.Duration {
	const limit = int64(math.MaxInt64 / int64(time.Millisecond))
	return time.Duration(max(-limit, min(limit, ms))) * time.Millisecond
}
