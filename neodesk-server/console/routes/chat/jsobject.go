package chat

import (
	"sort"
	"strconv"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsjson"
)

// object builds a JavaScript object to write out as JSON.stringify wrote
// it: keys that are array indexes first, in numeric order, then the
// others in the order they were first set; a key set again keeps its
// place; a value that is undefined leaves its key out.
type object struct {
	keys []string
	vals map[string][]byte
}

func newObject() *object { return &object{vals: map[string][]byte{}} }

// raw sets key to a value already written as JSON.
func (o *object) raw(key string, json []byte) *object {
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = json
	return o
}

func (o *object) str(key, s string) *object { return o.raw(key, jsjson.AppendQuoted(nil, s)) }

func (o *object) num(key string, f float64) *object { return o.val(key, jsjson.OfNumber(f)) }

func (o *object) boolean(key string, b bool) *object {
	if b {
		return o.raw(key, []byte("true"))
	}
	return o.raw(key, []byte("false"))
}

// val sets key to a value read from JSON; undefined leaves the key out.
func (o *object) val(key string, v jsjson.Value) *object {
	if v.Kind() == jsjson.Undefined {
		return o
	}
	b, _ := v.MarshalJSON()
	return o.raw(key, b)
}

// spread copies the own properties of v into the object, as {...v} does:
// an object's members, an array's elements and a string's UTF-16 units
// under their index; nothing for the other types.
func (o *object) spread(v jsjson.Value) *object {
	switch v.Kind() {
	case jsjson.Object:
		for _, k := range v.Keys() {
			o.val(k, v.Get(k))
		}
	case jsjson.Array:
		for i, item := range v.Items() {
			o.val(strconv.Itoa(i), item)
		}
	case jsjson.String:
		for i, unit := range utf16Units(v.Str()) {
			o.str(strconv.Itoa(i), unit)
		}
	}
	return o
}

// json writes the object.
func (o *object) json() []byte {
	var index, named []string
	for _, k := range o.keys {
		if isArrayIndex(k) {
			index = append(index, k)
		} else {
			named = append(named, k)
		}
	}
	sort.SliceStable(index, func(i, j int) bool {
		a, _ := strconv.ParseUint(index[i], 10, 64)
		b, _ := strconv.ParseUint(index[j], 10, 64)
		return a < b
	})
	buf := []byte{'{'}
	for i, k := range append(index, named...) {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = jsjson.AppendQuoted(buf, k)
		buf = append(buf, ':')
		buf = append(buf, o.vals[k]...)
	}
	return append(buf, '}')
}

// text is the object as JSON text, for a socket.
func (o *object) text() string { return string(o.json()) }

// isArrayIndex reports whether key is a canonical array index, 0 to 2^32-2.
func isArrayIndex(key string) bool {
	if key == "" || (len(key) > 1 && key[0] == '0') {
		return false
	}
	n, err := strconv.ParseUint(key, 10, 64)
	return err == nil && n <= 1<<32-2
}

// array writes JSON texts as one JSON array.
func array(items [][]byte) []byte {
	buf := []byte{'['}
	for i, item := range items {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = append(buf, item...)
	}
	return append(buf, ']')
}

// utf16Units splits s into its UTF-16 units, each as a string: a
// character outside the basic plane gives its two halves, each kept as
// the three bytes WTF-8 gives a lone half.
func utf16Units(s string) []string {
	var out []string
	for i := 0; i < len(s); {
		unit := jsjson.Slice(s[i:], 1)
		if unit == "" {
			break
		}
		if len(unit) == 3 && unit[0] == 0xED && s[i] != 0xED {
			// The first half of a pair: the second half follows
			r := []rune(s[i:])[0]
			low := 0xDC00 + (r-0x10000)&0x3FF
			out = append(out, unit, string([]byte{0xED, 0x80 | byte(low>>6&0x3F), 0x80 | byte(low&0x3F)}))
			i += len(string(r))
			continue
		}
		out = append(out, unit)
		i += len(unit)
	}
	return out
}

// hasLoneSurrogate reports half of a surrogate pair in s (kept as WTF-8),
// which encodeURIComponent refuses.
func hasLoneSurrogate(s string) bool {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == 0xED && s[i+1] >= 0xA0 && s[i+1] <= 0xBF {
			return true
		}
	}
	return false
}
