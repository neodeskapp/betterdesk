package goapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Object is a JSON object that keeps its keys in the order a JavaScript
// object keeps them: keys that are array indexes first, in numeric order,
// then the others in the order they were first set. A nil *Object reads as
// an object without keys and encodes as null.
type Object struct {
	keys []string
	vals map[string]any
}

// NewObject builds an object from key and value pairs, in order:
//
//	NewObject("success", false, "error", "not found")
func NewObject(pairs ...any) *Object {
	o := &Object{vals: make(map[string]any, len(pairs)/2)}
	for i := 0; i+1 < len(pairs); i += 2 {
		key, _ := pairs[i].(string)
		o.Set(key, pairs[i+1])
	}
	return o
}

// Get returns the value of key. A missing key gives nil, like a JSON null.
// Has tells the two apart.
func (o *Object) Get(key string) any {
	if o == nil {
		return nil
	}
	return o.vals[key]
}

// Has reports whether the object has key, also when its value is null.
func (o *Object) Has(key string) bool {
	if o == nil {
		return false
	}
	_, ok := o.vals[key]
	return ok
}

// Set stores value under key and returns the object. A new key goes to the
// end; an existing key keeps its place.
func (o *Object) Set(key string, value any) *Object {
	if o.vals == nil {
		o.vals = make(map[string]any)
	}
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = value
	return o
}

// Delete removes key.
func (o *Object) Delete(key string) {
	if o == nil {
		return
	}
	if _, ok := o.vals[key]; !ok {
		return
	}
	delete(o.vals, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

// Len returns the number of keys.
func (o *Object) Len() int {
	if o == nil {
		return 0
	}
	return len(o.keys)
}

// Keys returns the keys in JavaScript's property order.
func (o *Object) Keys() []string {
	if o == nil {
		return nil
	}
	var indexes, names []string
	for _, k := range o.keys {
		if _, ok := arrayIndex(k); ok {
			indexes = append(indexes, k)
		} else {
			names = append(names, k)
		}
	}
	if len(indexes) == 0 {
		return names
	}
	sort.Slice(indexes, func(i, j int) bool {
		a, _ := arrayIndex(indexes[i])
		b, _ := arrayIndex(indexes[j])
		return a < b
	})
	return append(indexes, names...)
}

// arrayIndex reports whether key is a canonical array index (0 to 2^32-2).
func arrayIndex(key string) (uint64, bool) {
	if key == "" || len(key) > 10 || (len(key) > 1 && key[0] == '0') {
		return 0, false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < '0' || key[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(key, 10, 64)
	if err != nil || n > math.MaxUint32-1 {
		return 0, false
	}
	return n, true
}

// Clone returns a copy that shares the values, like {...o} in JavaScript.
func (o *Object) Clone() *Object {
	c := &Object{vals: make(map[string]any, o.Len())}
	if o == nil {
		return c
	}
	c.keys = append(c.keys, o.keys...)
	for k, v := range o.vals {
		c.vals[k] = v
	}
	return c
}

// Bool reports whether the value of key is truthy in JavaScript's sense.
func (o *Object) Bool(key string) bool { return Truthy(o.Get(key)) }

// Str returns the value of key when it is a string, and "" otherwise.
func (o *Object) Str(key string) string {
	s, _ := o.Get(key).(string)
	return s
}

// Number returns the value of key when it is a number.
func (o *Object) Number(key string) (float64, bool) {
	n, ok := o.Get(key).(float64)
	return n, ok
}

// Object returns the value of key when it is an object, and nil otherwise.
func (o *Object) Object(key string) *Object {
	v, _ := o.Get(key).(*Object)
	return v
}

// MarshalJSON writes the object with its keys in order.
func (o *Object) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	if err := encodeValue(&buf, o); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// UnmarshalJSON reads a JSON object and keeps its key order.
func (o *Object) UnmarshalJSON(data []byte) error {
	v, err := Parse(data)
	if err != nil {
		return err
	}
	parsed, ok := v.(*Object)
	if !ok || parsed == nil {
		return errors.New("goapi: JSON value is not an object")
	}
	*o = *parsed
	return nil
}

// Parse decodes JSON the way JSON.parse does and returns nil, bool, float64,
// string, []any or *Object. The whole input has to be one JSON value.
func Parse(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := parseValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("goapi: unexpected data after the JSON value")
	}
	return v, nil
}

func parseValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			o := NewObject()
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, _ := keyTok.(string)
				val, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				o.Set(key, val)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return o, nil
		case '[':
			arr := []any{}
			for dec.More() {
				val, err := parseValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, val)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return arr, nil
		}
		return nil, fmt.Errorf("goapi: unexpected %q in JSON", rune(t))
	case json.Number:
		// A number too large for a float64 is Infinity, as in JavaScript.
		f, _ := strconv.ParseFloat(string(t), 64)
		return f, nil
	default:
		return tok, nil
	}
}

// FromGo turns any Go value that encodes as JSON into the values Parse
// returns, so that code can read it the way it reads an answer from Go.
func FromGo(v any) (any, error) {
	var buf bytes.Buffer
	if err := encodeValue(&buf, v); err != nil {
		return nil, err
	}
	return Parse(buf.Bytes())
}

// encodeValue writes v as JSON without escaping <, > and &. Numbers are
// written the way JSON.stringify writes them; NaN and the infinities become
// null.
func encodeValue(buf *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		buf.WriteString(strconv.FormatBool(t))
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			buf.WriteString("null")
			return nil
		}
		return encodePlain(buf, t)
	case *Object:
		if t == nil {
			buf.WriteString("null")
			return nil
		}
		buf.WriteByte('{')
		for i, k := range t.Keys() {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := encodePlain(buf, k); err != nil {
				return err
			}
			buf.WriteByte(':')
			if err := encodeValue(buf, t.vals[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	case []any:
		if t == nil {
			buf.WriteString("null")
			return nil
		}
		buf.WriteByte('[')
		for i, el := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := encodeValue(buf, el); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case []*Object:
		if t == nil {
			buf.WriteString("null")
			return nil
		}
		buf.WriteByte('[')
		for i, el := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := encodeValue(buf, el); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	default:
		return encodePlain(buf, v)
	}
	return nil
}

func encodePlain(buf *bytes.Buffer, v any) error {
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	buf.Truncate(buf.Len() - 1) // the newline Encode adds
	return nil
}

// Truthy reports whether a parsed JSON value is truthy in JavaScript: every
// value except null, false, 0, NaN and "".
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
	case *Object:
		return t != nil
	case int:
		return t != 0
	case int64:
		return t != 0
	}
	return true
}

// Dig follows a path into a parsed JSON value: a string steps into an
// object, an int into an array. It returns nil as soon as a step does not
// exist, like a chain of ?. in JavaScript.
func Dig(v any, path ...any) any {
	for _, step := range path {
		switch s := step.(type) {
		case string:
			v = prop(v, s)
		case int:
			switch arr := v.(type) {
			case []any:
				if s < 0 || s >= len(arr) {
					return nil
				}
				v = arr[s]
			case []*Object:
				if s < 0 || s >= len(arr) {
					return nil
				}
				v = arr[s]
			default:
				return nil
			}
		default:
			return nil
		}
	}
	return v
}

// prop reads a property of a parsed JSON value. Only objects have any.
func prop(v any, key string) any {
	switch t := v.(type) {
	case *Object:
		return t.Get(key)
	case map[string]any:
		return t[key]
	}
	return nil
}

// or is JavaScript's a || b.
func or(a, b any) any {
	if Truthy(a) {
		return a
	}
	return b
}

// jsString is JavaScript's String(v) for a parsed JSON value.
func jsString(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return jsNumberString(t)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case []any:
		parts := make([]string, len(t))
		for i, el := range t {
			if el != nil {
				parts[i] = jsString(el)
			}
		}
		return strings.Join(parts, ",")
	case *Object:
		return "[object Object]"
	}
	return fmt.Sprint(v)
}

func jsNumberString(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == 0:
		return "0"
	}
	var buf bytes.Buffer
	if err := encodePlain(&buf, f); err != nil {
		return strconv.FormatFloat(f, 'g', -1, 64)
	}
	return buf.String()
}

// jsNumber is JavaScript's Number(v) for a parsed JSON value.
func jsNumber(v any) float64 {
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
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case string:
		s := jsTrim(t)
		if s == "" {
			return 0
		}
		switch s {
		case "Infinity", "+Infinity":
			return math.Inf(1)
		case "-Infinity":
			return math.Inf(-1)
		}
		if m := radixLiteral.FindStringSubmatch(s); m != nil {
			base := map[string]int{"x": 16, "o": 8, "b": 2}[strings.ToLower(m[1])]
			n, err := strconv.ParseUint(m[2], base, 64)
			if err != nil {
				return math.NaN()
			}
			return float64(n)
		}
		if !decimalLiteral.MatchString(s) {
			return math.NaN()
		}
		f, _ := strconv.ParseFloat(s, 64)
		return f
	}
	return math.NaN()
}

var (
	decimalLiteral = regexp.MustCompile(`^[+-]?(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?$`)
	radixLiteral   = regexp.MustCompile(`^0([xXoObB])([0-9a-fA-F]+)$`)
)

// jsTrim is String.prototype.trim: it removes JavaScript's white space and
// line terminators from both ends.
func jsTrim(s string) string {
	return strings.TrimFunc(s, isJSSpace)
}

func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// EncodeURIComponent escapes s the way JavaScript's encodeURIComponent
// does: everything except letters, digits and - _ . ! ~ * ' ( ).
func EncodeURIComponent(s string) string {
	const upperhex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			strings.IndexByte("-_.!~*'()", c) >= 0:
			b.WriteByte(c)
		default:
			b.WriteByte('%')
			b.WriteByte(upperhex[c>>4])
			b.WriteByte(upperhex[c&15])
		}
	}
	return b.String()
}

// queryPair is one key and value of a query string.
type queryPair struct{ key, value string }

// appendParam adds one entry of an axios params object to a query string,
// the way axios serialises it: null is left out, an array becomes key[]=
// once per element, an object becomes key[name]= per property, everything
// else is its string form.
func appendParam(dst []queryPair, key string, v any) []queryPair {
	switch t := v.(type) {
	case nil:
		return dst
	case []any:
		for _, el := range t {
			dst = appendNested(dst, key+"[]", el)
		}
		return dst
	case []string:
		for _, el := range t {
			dst = append(dst, queryPair{key + "[]", el})
		}
		return dst
	case *Object:
		if t == nil {
			return dst
		}
		for _, k := range t.Keys() {
			dst = appendNested(dst, key+"["+k+"]", t.Get(k))
		}
		return dst
	case map[string]any:
		for _, k := range sortedKeys(t) {
			dst = appendNested(dst, key+"["+k+"]", t[k])
		}
		return dst
	}
	return append(dst, queryPair{key, jsString(v)})
}

// appendNested adds a value below the top level, where axios writes null as
// an empty string instead of leaving it out.
func appendNested(dst []queryPair, key string, v any) []queryPair {
	if v == nil {
		return append(dst, queryPair{key, ""})
	}
	return appendParam(dst, key, v)
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// paramsOf turns the params argument of a call into query pairs. It takes
// an *Object, a map[string]any or url.Values.
func paramsOf(params any) []queryPair {
	var dst []queryPair
	switch t := params.(type) {
	case *Object:
		for _, k := range t.Keys() {
			dst = appendParam(dst, k, t.Get(k))
		}
	case map[string]any:
		for _, k := range sortedKeys(t) {
			dst = appendParam(dst, k, t[k])
		}
	case url.Values:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if len(t[k]) == 1 {
				dst = append(dst, queryPair{k, t[k][0]})
				continue
			}
			for _, v := range t[k] {
				dst = append(dst, queryPair{k + "[]", v})
			}
		}
	}
	return dst
}

func encodeQuery(pairs []queryPair) string {
	var b strings.Builder
	for i, p := range pairs {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(url.QueryEscape(p.key))
		b.WriteByte('=')
		b.WriteString(url.QueryEscape(p.value))
	}
	return b.String()
}

// queryOf builds a query string the way URLSearchParams does.
func queryOf(pairs ...string) string {
	v := url.Values{}
	var order []string
	for i := 0; i+1 < len(pairs); i += 2 {
		v.Set(pairs[i], pairs[i+1])
		order = append(order, pairs[i])
	}
	var parts []string
	for _, k := range order {
		parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(v.Get(k)))
	}
	return strings.Join(parts, "&")
}
