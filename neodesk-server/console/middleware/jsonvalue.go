package middleware

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Kind is the JavaScript type of a body value.
type Kind int

// The kinds of a Value. KindUndefined is a member that is not there.
const (
	KindUndefined Kind = iota
	KindNull
	KindBool
	KindNumber
	KindString
	KindArray
	KindObject
)

// Value is one value of a request body. It keeps the bytes the client
// sent, so a number is neither rounded nor reformatted on its way through
// a handler: 12345678901234567890 and 1.0 stay what they are. The zero
// Value is JavaScript's undefined.
//
// Each method stands for an expression a JavaScript handler would write
// with the value v; the comments name it.
type Value struct {
	raw json.RawMessage
}

// rawValue wraps JSON text that is known to be one valid value.
func rawValue(raw []byte) Value { return Value{raw: raw} }

func stringValue(s string) Value {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return Value{raw: bytes.TrimSuffix(buf.Bytes(), []byte("\n"))}
}

// Kind is typeof v, with null, arrays and undefined told apart.
func (v Value) Kind() Kind {
	if len(v.raw) == 0 {
		return KindUndefined
	}
	switch v.raw[0] {
	case 'n':
		return KindNull
	case 't', 'f':
		return KindBool
	case '"':
		return KindString
	case '[':
		return KindArray
	case '{':
		return KindObject
	}
	return KindNumber
}

// Exists is v !== undefined.
func (v Value) Exists() bool { return len(v.raw) > 0 }

// Raw is the value as the client sent it, nil for undefined.
func (v Value) Raw() json.RawMessage { return v.raw }

// Truthy is !!v: false for undefined, null, false, 0, -0 and "", true for
// everything else, also "0", "false", [] and {}.
func (v Value) Truthy() bool {
	switch v.Kind() {
	case KindUndefined, KindNull:
		return false
	case KindBool:
		return v.raw[0] == 't'
	case KindString:
		return !bytes.Equal(v.raw, []byte(`""`))
	case KindNumber:
		f := jsNumber(string(v.raw))
		return f != 0 && !math.IsNaN(f)
	}
	return true
}

// Str is the text when typeof v === 'string'. A number or a boolean is not
// converted; Text does that.
func (v Value) Str() (string, bool) {
	if v.Kind() != KindString {
		return "", false
	}
	var s string
	if err := json.Unmarshal(v.raw, &s); err != nil {
		return "", false
	}
	return s, true
}

// Number is the number when typeof v === 'number', with every digit the
// client sent. JavaScript itself would have kept about 17 of them.
func (v Value) Number() (json.Number, bool) {
	if v.Kind() != KindNumber {
		return "", false
	}
	return json.Number(v.raw), true
}

// Float is the number as JavaScript sees it when typeof v === 'number'. A
// literal beyond the range of a float64 is infinite.
func (v Value) Float() (float64, bool) {
	if v.Kind() != KindNumber {
		return 0, false
	}
	return jsNumber(string(v.raw)), true
}

// Bool is the value when typeof v === 'boolean'.
func (v Value) Bool() (value, ok bool) {
	if v.Kind() != KindBool {
		return false, false
	}
	return v.raw[0] == 't', true
}

// Object is the value when it is an object: v && typeof v === 'object' &&
// !Array.isArray(v).
func (v Value) Object() (JSONObject, bool) {
	if v.Kind() != KindObject {
		return JSONObject{}, false
	}
	return parseObject(v.raw), true
}

// Array is the elements when Array.isArray(v).
func (v Value) Array() ([]Value, bool) {
	if v.Kind() != KindArray {
		return nil, false
	}
	var items []json.RawMessage
	if err := json.Unmarshal(v.raw, &items); err != nil {
		return nil, false
	}
	out := make([]Value, len(items))
	for i, item := range items {
		out[i] = Value{raw: item}
	}
	return out, true
}

// Decode is json.Unmarshal of the value into dst. It fails for undefined.
func (v Value) Decode(dst any) error {
	if !v.Exists() {
		return errors.New("no value")
	}
	return json.Unmarshal(v.raw, dst)
}

// Text is bodyString(v, fallback) of lib/bodyScalars.js: a string as it
// is, a finite number as String(v) writes it, a boolean as "true" or
// "false", and the fallback for everything else (null, arrays, objects,
// undefined).
func (v Value) Text(fallback string) string {
	switch v.Kind() {
	case KindString:
		if s, ok := v.Str(); ok {
			return s
		}
	case KindNumber:
		if f := jsNumber(string(v.raw)); !math.IsInf(f, 0) && !math.IsNaN(f) {
			return jsNumberString(f)
		}
	case KindBool:
		if v.raw[0] == 't' {
			return "true"
		}
		return "false"
	}
	return fallback
}

// Int is bodyInt(v, fallback) of lib/bodyScalars.js: a finite number cut
// to a whole number, a string that is not blank through parseInt(v, 10),
// and the fallback for everything else. The result stays within
// JavaScript's safe integers.
func (v Value) Int(fallback int64) int64 {
	return v.IntBetween(fallback, -maxSafeInteger, maxSafeInteger)
}

// IntBetween is bodyInt(v, fallback, { min, max }).
func (v Value) IntBetween(fallback, minimum, maximum int64) int64 {
	n := float64(fallback)
	switch v.Kind() {
	case KindNumber:
		if f := jsNumber(string(v.raw)); !math.IsInf(f, 0) && !math.IsNaN(f) {
			n = math.Trunc(f)
		}
	case KindString:
		if s, ok := v.Str(); ok && strings.TrimFunc(s, isJSSpace) != "" {
			if parsed, ok := jsParseInt(s); ok && !math.IsInf(parsed, 0) {
				n = parsed
			}
		}
	}
	if n < float64(minimum) {
		return minimum
	}
	if n > float64(maximum) {
		return maximum
	}
	return int64(n)
}

// Flag is bodyBool(v, fallback) of lib/bodyScalars.js: a boolean as it is,
// "true", "1" and the number 1 are true, "false", "0" and the number 0 are
// false, and everything else is the fallback.
func (v Value) Flag(fallback bool) bool {
	switch v.Kind() {
	case KindBool:
		return v.raw[0] == 't'
	case KindString:
		switch s, _ := v.Str(); s {
		case "true", "1":
			return true
		case "false", "0":
			return false
		}
	case KindNumber:
		switch jsNumber(string(v.raw)) {
		case 1:
			return true
		case 0:
			return false
		}
	}
	return fallback
}

// JSONObject is a request body that is an object: req.body of a JSON
// request, of a form, or the text fields of an upload. Members keep the
// bytes the client sent (see Value).
//
// It has own members only. A JavaScript handler could also see what every
// object inherits (constructor, toString, hasOwnProperty); no handler of
// the console reads those from a body.
type JSONObject struct {
	keys   []string
	values map[string]Value
}

// parseObject reads the members of a JSON object. A name sent twice keeps
// its first place and its last value.
func parseObject(raw []byte) JSONObject {
	obj := JSONObject{values: map[string]Value{}}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return obj
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		key, _ := tok.(string)
		var member json.RawMessage
		if err := dec.Decode(&member); err != nil {
			break
		}
		obj.set(key, Value{raw: member})
	}
	obj.sortKeys()
	return obj
}

func (o *JSONObject) set(key string, v Value) {
	if _, seen := o.values[key]; !seen {
		o.keys = append(o.keys, key)
	}
	o.values[key] = v
}

// arrayIndex reports whether a member name is a whole number as
// JavaScript writes one. Such names come first when an object lists its
// keys.
func arrayIndex(key string) (uint64, bool) {
	if key == "" || len(key) > 10 || (len(key) > 1 && key[0] == '0') {
		return 0, false
	}
	n, err := strconv.ParseUint(key, 10, 64)
	if err != nil || n > 4294967294 {
		return 0, false
	}
	return n, true
}

// sortKeys puts the keys in the order JavaScript lists them: whole
// numbers ascending, then the other names as they first appeared.
func (o *JSONObject) sortKeys() {
	sort.SliceStable(o.keys, func(a, b int) bool {
		ia, oka := arrayIndex(o.keys[a])
		ib, okb := arrayIndex(o.keys[b])
		switch {
		case oka && okb:
			return ia < ib
		default:
			return oka && !okb
		}
	})
}

// Has is Object.prototype.hasOwnProperty.call(body, key), which for a
// body is the same as body[key] !== undefined. A member that is null is
// there.
func (o JSONObject) Has(key string) bool {
	_, ok := o.values[key]
	return ok
}

// Get is body[key]. A missing member is the zero Value (undefined).
func (o JSONObject) Get(key string) Value { return o.values[key] }

// Keys is Object.keys(body).
func (o JSONObject) Keys() []string { return append([]string(nil), o.keys...) }

// Len is Object.keys(body).length.
func (o JSONObject) Len() int { return len(o.keys) }

// Raw is the member as the client sent it, nil when it is missing.
func (o JSONObject) Raw(key string) json.RawMessage { return o.values[key].raw }

// String is body[key] when typeof body[key] === 'string'.
func (o JSONObject) String(key string) (string, bool) { return o.values[key].Str() }

// Truthy is !!body[key], the test behind `if (!body.name)`.
func (o JSONObject) Truthy(key string) bool { return o.values[key].Truthy() }

// Number is body[key] when typeof body[key] === 'number', digit for digit.
func (o JSONObject) Number(key string) (json.Number, bool) { return o.values[key].Number() }

// Int is bodyInt(body[key], fallback).
func (o JSONObject) Int(key string, fallback int64) int64 { return o.values[key].Int(fallback) }

// IntBetween is bodyInt(body[key], fallback, { min, max }).
func (o JSONObject) IntBetween(key string, fallback, minimum, maximum int64) int64 {
	return o.values[key].IntBetween(fallback, minimum, maximum)
}

// Text is bodyString(body[key], fallback).
func (o JSONObject) Text(key, fallback string) string { return o.values[key].Text(fallback) }

// Flag is bodyBool(body[key], fallback).
func (o JSONObject) Flag(key string, fallback bool) bool { return o.values[key].Flag(fallback) }

// MarshalJSON writes the object with its members in the order of Keys.
func (o JSONObject) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, key := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		buf.Write(stringValue(key).raw)
		buf.WriteByte(':')
		buf.Write(o.values[key].raw)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}
