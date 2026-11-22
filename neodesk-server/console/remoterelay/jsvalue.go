package remoterelay

import (
	"errors"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/unitronix/betterdesk-server/console/jsbind"
)

// The relay reads the JSON messages of agents and viewers with JSON.parse
// and writes some of them out again with JSON.stringify. This file does
// both the way JavaScript does: an object keeps its keys in JavaScript's
// order (array indexes by number first, then the others as they first
// came, the last of two equal keys giving the value), numbers come out as
// JavaScript prints them, and half of a surrogate pair survives as the
// escape it came from. It is the reading and writing part of
// routes/internal/jsjson, which this package cannot import.

// stringifyDepth is how deeply arrays and objects may be nested in a
// message the old relay wrote out again: one level more and JSON.stringify
// ran out of stack inside the socket's listener and the Node process
// ended (recorded in testdata/node_remote.json, crash.deepestStatus plus
// the message's own object). The limit is that of the recording's Node
// version and stack size.
const stringifyDepth = 4098

// errSyntax is the SyntaxError of JSON.parse.
var errSyntax = errors.New("not JSON")

type jsKind uint8

const (
	jsUndefined jsKind = iota
	jsNull
	jsBool
	jsNumber
	jsString
	jsArray
	jsObject
)

// jsValue is a value JSON.parse made, or undefined (the zero value).
type jsValue struct {
	kind  jsKind
	flag  bool
	num   float64
	text  string
	items []jsValue
	// keys are an object's keys in JavaScript's order.
	keys    []string
	members map[string]jsValue
}

// get is v[name] for an own member of an object; everything else has no
// members the relay reads.
func (v jsValue) get(name string) jsValue {
	if v.kind == jsObject {
		return v.members[name]
	}
	return jsValue{}
}

// truthy is !!v.
func (v jsValue) truthy() bool {
	switch v.kind {
	case jsUndefined, jsNull:
		return false
	case jsBool:
		return v.flag
	case jsNumber:
		return v.num != 0 && !math.IsNaN(v.num)
	case jsString:
		return v.text != ""
	}
	return true
}

// isText reports v === text.
func (v jsValue) isText(text string) bool { return v.kind == jsString && v.text == text }

// withType is { ...v, type } without event_type: a copy of the object
// with its key type set (in its place, or at the end when it had none) and
// event_type removed.
func (v jsValue) withType(typ jsValue) jsValue {
	out := jsValue{kind: jsObject, members: make(map[string]jsValue, len(v.members)+1)}
	for _, key := range v.keys {
		if key == "event_type" {
			continue
		}
		out.keys = append(out.keys, key)
		out.members[key] = v.members[key]
	}
	if _, ok := out.members["type"]; !ok {
		out.keys = propertyOrder(append(out.keys, "type"))
	}
	out.members["type"] = typ
	return out
}

// stringify is JSON.stringify(v).
func (v jsValue) stringify() string { return string(v.appendJSON(nil)) }

func (v jsValue) appendJSON(buf []byte) []byte {
	switch v.kind {
	case jsBool:
		if v.flag {
			return append(buf, "true"...)
		}
		return append(buf, "false"...)
	case jsNumber:
		if math.IsNaN(v.num) || math.IsInf(v.num, 0) {
			return append(buf, "null"...)
		}
		return append(buf, jsbind.NumberString(v.num)...)
	case jsString:
		return appendQuoted(buf, v.text)
	case jsArray:
		buf = append(buf, '[')
		for i, item := range v.items {
			if i > 0 {
				buf = append(buf, ',')
			}
			buf = item.appendJSON(buf)
		}
		return append(buf, ']')
	case jsObject:
		buf = append(buf, '{')
		for i, key := range v.keys {
			if i > 0 {
				buf = append(buf, ',')
			}
			buf = appendQuoted(buf, key)
			buf = append(buf, ':')
			buf = v.members[key].appendJSON(buf)
		}
		return append(buf, '}')
	}
	return append(buf, "null"...)
}

// appendQuoted appends s as JSON.stringify writes a string.
func appendQuoted(buf []byte, s string) []byte {
	const hex = "0123456789abcdef"
	buf = append(buf, '"')
	for i := 0; i < len(s); {
		if half, ok := halfPair(s[i:]); ok {
			buf = append(buf, '\\', 'u', hex[half>>12&0xF], hex[half>>8&0xF], hex[half>>4&0xF], hex[half&0xF])
			i += 3
			continue
		}
		size := charSize(s[i:])
		switch c := s[i]; {
		case size > 1:
			buf = append(buf, s[i:i+size]...)
		case c >= 0x80:
			buf = append(buf, 0xEF, 0xBF, 0xBD)
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
			buf = append(buf, '\\', 'u', '0', '0', hex[c>>4], hex[c&0xF])
		default:
			buf = append(buf, c)
		}
		i += size
	}
	return append(buf, '"')
}

// charSize is the length of the UTF-8 character s starts with, 1 for a
// byte that starts none.
func charSize(s string) int {
	c := s[0]
	switch {
	case c >= 0xC2 && c < 0xE0 && len(s) >= 2 && cont(s[1]):
		return 2
	case c >= 0xE0 && c < 0xF0 && len(s) >= 3 && cont(s[1]) && cont(s[2]):
		if r := rune(c&0x0F)<<12 | rune(s[1]&0x3F)<<6 | rune(s[2]&0x3F); r >= 0x800 {
			return 3
		}
	case c >= 0xF0 && c < 0xF5 && len(s) >= 4 && cont(s[1]) && cont(s[2]) && cont(s[3]):
		if r := rune(c&0x07)<<18 | rune(s[1]&0x3F)<<12 | rune(s[2]&0x3F)<<6 | rune(s[3]&0x3F); r >= 0x10000 && r <= 0x10FFFF {
			return 4
		}
	}
	return 1
}

func cont(b byte) bool { return b&0xC0 == 0x80 }

// halfPair reports whether s starts with half of a surrogate pair in the
// three bytes it is kept in here, and which half.
func halfPair(s string) (rune, bool) {
	if len(s) >= 3 && s[0] == 0xED && s[1] >= 0xA0 && s[1] <= 0xBF && cont(s[2]) {
		return 0xD000 | rune(s[1]&0x3F)<<6 | rune(s[2]&0x3F), true
	}
	return 0, false
}

// message is a JSON text as the relay reads it: whether JSON.parse took
// it, how deeply it nests, and its value when the relay acts on its type
// and it nests no deeper than stringifyDepth. Any other message is read
// only at its top: typ holds its type when it is an object, so the caller
// can tell what Node did with it.
type message struct {
	value jsValue
	depth int
	// shallow is set for a message read at its top only: value is then
	// undefined and typ is the top-level type member.
	shallow bool
	typ     jsValue
}

// readMessage reads a text message. ok is false where JSON.parse threw.
// Only a message whose top-level type wanted takes is built as a tree:
// the tree costs many times the text, and the relay drops every other
// message.
func readMessage(text string, wanted func(typ jsValue) bool) (message, bool) {
	depth, ok := scanJSON(text)
	if !ok {
		return message{}, false
	}
	if isNullText(text) {
		return message{value: jsValue{kind: jsNull}}, true
	}
	typ := topLevelType(text)
	if depth > stringifyDepth || !wanted(typ) {
		return message{depth: depth, shallow: true, typ: typ}, true
	}
	v, err := parseJS(text)
	if err != nil {
		return message{}, false
	}
	return message{value: v, depth: depth}, true
}

// isNullText reports JSON text that is the value null, with the
// whitespace JSON.parse allows around it.
func isNullText(s string) bool {
	return strings.Trim(s, " \t\n\r") == "null"
}

// isNull reports a message that is the value null, where reading a
// member threw a TypeError in Node.
func (m message) isNull() bool { return !m.shallow && m.value.kind == jsNull }

// typeMember is frame.type.
func (m message) typeMember() jsValue {
	if m.shallow {
		return m.typ
	}
	return m.value.get("type")
}

// scanJSON checks text as JSON.parse would, without recursion, and
// returns how deeply its arrays and objects nest.
func scanJSON(s string) (depth int, ok bool) {
	const (
		wantValue = iota
		wantValueOrEnd
		wantKeyOrEnd
		wantKey
		afterValue
	)
	var stack []byte
	i := 0
	skip := func() {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
			i++
		}
	}
	state := wantValue
	for {
		skip()
		switch state {
		case wantValue, wantValueOrEnd:
			if i >= len(s) {
				return 0, false
			}
			c := s[i]
			if state == wantValueOrEnd && c == ']' {
				stack = stack[:len(stack)-1]
				i++
				state = afterValue
				continue
			}
			switch {
			case c == '{' || c == '[':
				stack = append(stack, c)
				if len(stack) > depth {
					depth = len(stack)
				}
				i++
				state = wantKeyOrEnd
				if c == '[' {
					state = wantValueOrEnd
				}
				continue
			case c == '"':
				end, ok := scanString(s, i)
				if !ok {
					return 0, false
				}
				i = end
			case c == 't' || c == 'f' || c == 'n':
				word := map[byte]string{'t': "true", 'f': "false", 'n': "null"}[c]
				if !strings.HasPrefix(s[i:], word) {
					return 0, false
				}
				i += len(word)
			default:
				end, ok := scanNumber(s, i)
				if !ok {
					return 0, false
				}
				i = end
			}
			state = afterValue
		case wantKeyOrEnd, wantKey:
			if i >= len(s) {
				return 0, false
			}
			if state == wantKeyOrEnd && s[i] == '}' {
				stack = stack[:len(stack)-1]
				i++
				state = afterValue
				continue
			}
			if s[i] != '"' {
				return 0, false
			}
			end, ok := scanString(s, i)
			if !ok {
				return 0, false
			}
			i = end
			skip()
			if i >= len(s) || s[i] != ':' {
				return 0, false
			}
			i++
			state = wantValue
		case afterValue:
			if len(stack) == 0 {
				return depth, i == len(s)
			}
			if i >= len(s) {
				return 0, false
			}
			top := stack[len(stack)-1]
			switch {
			case s[i] == ',':
				i++
				state = wantValue
				if top == '{' {
					state = wantKey
				}
			case top == '{' && s[i] == '}', top == '[' && s[i] == ']':
				stack = stack[:len(stack)-1]
				i++
			default:
				return 0, false
			}
		}
	}
}

// scanString checks the string at s[i] (its opening quote) and returns
// the index after its closing quote.
func scanString(s string, i int) (int, bool) {
	i++
	for i < len(s) {
		c := s[i]
		switch {
		case c == '"':
			return i + 1, true
		case c < 0x20:
			return 0, false
		case c == '\\':
			if i+1 >= len(s) {
				return 0, false
			}
			switch s[i+1] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				i += 2
			case 'u':
				if i+6 > len(s) {
					return 0, false
				}
				for _, h := range []byte(s[i+2 : i+6]) {
					if !isHex(h) {
						return 0, false
					}
				}
				i += 6
			default:
				return 0, false
			}
		default:
			i++
		}
	}
	return 0, false
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

// scanNumber checks the number at s[i] and returns the index after it.
func scanNumber(s string, i int) (int, bool) {
	digits := func() bool {
		from := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		return i > from
	}
	if i < len(s) && s[i] == '-' {
		i++
	}
	if i < len(s) && s[i] == '0' {
		i++
	} else if !digits() {
		return 0, false
	}
	if i < len(s) && s[i] == '.' {
		i++
		if !digits() {
			return 0, false
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		if !digits() {
			return 0, false
		}
	}
	return i, true
}

// topLevelType reads the type member at the top of a text scanJSON took,
// without going into nested values: undefined when the text is no object
// or has no such member, else the last one's value when it is not an
// array or object.
func topLevelType(s string) jsValue {
	i := 0
	skip := func() {
		for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
			i++
		}
	}
	skip()
	if i >= len(s) || s[i] != '{' {
		return jsValue{}
	}
	i++
	var typ jsValue
	for {
		skip()
		if s[i] == '}' {
			return typ
		}
		end, _ := scanString(s, i)
		p := &parser{s: s[i:end]}
		key, _ := p.str()
		i = end
		skip()
		i++ // the colon
		skip()
		start := i
		i = skipValue(s, i)
		if key == "type" {
			if c := s[start]; c == '{' || c == '[' {
				typ = jsValue{kind: jsObject}
			} else if v, err := parseJS(s[start:i]); err == nil {
				typ = v
			}
		}
		skip()
		if s[i] == ',' {
			i++
		}
	}
}

// skipValue returns the index after the value at s[i] of a text scanJSON
// took.
func skipValue(s string, i int) int {
	switch c := s[i]; {
	case c == '"':
		end, _ := scanString(s, i)
		return end
	case c == '{' || c == '[':
		depth := 0
		for i < len(s) {
			switch s[i] {
			case '"':
				i, _ = scanString(s, i)
				continue
			case '{', '[':
				depth++
			case '}', ']':
				depth--
				if depth == 0 {
					return i + 1
				}
			}
			i++
		}
		return i
	case c == 't':
		return i + 4
	case c == 'f':
		return i + 5
	case c == 'n':
		return i + 4
	}
	end, _ := scanNumber(s, i)
	return end
}

// parseJS is JSON.parse(text) for a text that nests no deeper than
// stringifyDepth (readMessage checks that first).
func parseJS(text string) (jsValue, error) {
	p := &parser{s: text}
	p.space()
	v, err := p.value()
	if err != nil {
		return jsValue{}, err
	}
	p.space()
	if p.i != len(p.s) {
		return jsValue{}, errSyntax
	}
	return v, nil
}

type parser struct {
	s string
	i int
}

func (p *parser) space() {
	for p.i < len(p.s) {
		switch p.s[p.i] {
		case ' ', '\t', '\n', '\r':
			p.i++
		default:
			return
		}
	}
}

func (p *parser) peek() byte {
	if p.i < len(p.s) {
		return p.s[p.i]
	}
	return 0
}

func (p *parser) word(word string, v jsValue) (jsValue, error) {
	if !strings.HasPrefix(p.s[p.i:], word) {
		return jsValue{}, errSyntax
	}
	p.i += len(word)
	return v, nil
}

func (p *parser) value() (jsValue, error) {
	switch c := p.peek(); {
	case c == '{':
		return p.object()
	case c == '[':
		return p.array()
	case c == '"':
		s, err := p.str()
		return jsValue{kind: jsString, text: s}, err
	case c == 't':
		return p.word("true", jsValue{kind: jsBool, flag: true})
	case c == 'f':
		return p.word("false", jsValue{kind: jsBool})
	case c == 'n':
		return p.word("null", jsValue{kind: jsNull})
	case c == '-' || c >= '0' && c <= '9':
		end, ok := scanNumber(p.s, p.i)
		if !ok {
			return jsValue{}, errSyntax
		}
		// Out of range is an infinity or zero with the sign, as in JavaScript
		f, _ := strconv.ParseFloat(p.s[p.i:end], 64)
		p.i = end
		return jsValue{kind: jsNumber, num: f}, nil
	}
	return jsValue{}, errSyntax
}

func (p *parser) object() (jsValue, error) {
	p.i++
	out := jsValue{kind: jsObject, members: map[string]jsValue{}}
	var order []string
	p.space()
	if p.peek() == '}' {
		p.i++
		return out, nil
	}
	for {
		p.space()
		if p.peek() != '"' {
			return jsValue{}, errSyntax
		}
		key, err := p.str()
		if err != nil {
			return jsValue{}, err
		}
		p.space()
		if p.peek() != ':' {
			return jsValue{}, errSyntax
		}
		p.i++
		p.space()
		v, err := p.value()
		if err != nil {
			return jsValue{}, err
		}
		if _, seen := out.members[key]; !seen {
			order = append(order, key)
		}
		out.members[key] = v
		p.space()
		switch p.peek() {
		case ',':
			p.i++
		case '}':
			p.i++
			out.keys = propertyOrder(order)
			return out, nil
		default:
			return jsValue{}, errSyntax
		}
	}
}

// propertyOrder puts keys in the order JavaScript lists the properties
// of an object: array indexes by their number, then the others as they
// were added.
func propertyOrder(added []string) []string {
	var indexes, names []string
	for _, key := range added {
		if _, ok := arrayIndex(key); ok {
			indexes = append(indexes, key)
		} else {
			names = append(names, key)
		}
	}
	if len(indexes) == 0 {
		return names
	}
	sort.SliceStable(indexes, func(a, b int) bool {
		x, _ := arrayIndex(indexes[a])
		y, _ := arrayIndex(indexes[b])
		return x < y
	})
	return append(indexes, names...)
}

// arrayIndex reads a key that is an array index: a whole number from 0
// to 4294967294 written without zeros in front.
func arrayIndex(key string) (uint64, bool) {
	if key == "" || len(key) > 10 || len(key) > 1 && key[0] == '0' {
		return 0, false
	}
	var n uint64
	for i := 0; i < len(key); i++ {
		if key[i] < '0' || key[i] > '9' {
			return 0, false
		}
		n = n*10 + uint64(key[i]-'0')
	}
	return n, n <= 4294967294
}

func (p *parser) array() (jsValue, error) {
	p.i++
	out := jsValue{kind: jsArray, items: []jsValue{}}
	p.space()
	if p.peek() == ']' {
		p.i++
		return out, nil
	}
	for {
		p.space()
		v, err := p.value()
		if err != nil {
			return jsValue{}, err
		}
		out.items = append(out.items, v)
		p.space()
		switch p.peek() {
		case ',':
			p.i++
		case ']':
			p.i++
			return out, nil
		default:
			return jsValue{}, errSyntax
		}
	}
}

// unit reads the four hex digits of a \u escape.
func (p *parser) unit() (rune, bool) {
	if p.i+4 > len(p.s) {
		return 0, false
	}
	var u rune
	for _, c := range []byte(p.s[p.i : p.i+4]) {
		switch {
		case c >= '0' && c <= '9':
			u = u<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			u = u<<4 | rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			u = u<<4 | rune(c-'A'+10)
		default:
			return 0, false
		}
	}
	p.i += 4
	return u, true
}

func (p *parser) str() (string, error) {
	p.i++
	var b strings.Builder
	for {
		if p.i >= len(p.s) {
			return "", errSyntax
		}
		c := p.s[p.i]
		p.i++
		switch {
		case c == '"':
			return b.String(), nil
		case c < 0x20:
			return "", errSyntax
		case c != '\\':
			b.WriteByte(c)
			continue
		}
		if p.i >= len(p.s) {
			return "", errSyntax
		}
		esc := p.s[p.i]
		p.i++
		switch esc {
		case '"', '\\', '/':
			b.WriteByte(esc)
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'u':
			u, ok := p.unit()
			if !ok {
				return "", errSyntax
			}
			if u >= 0xD800 && u < 0xDC00 && strings.HasPrefix(p.s[p.i:], `\u`) {
				// The first half of a pair, when the second half follows
				at := p.i
				p.i += 2
				if low, ok := p.unit(); ok && low >= 0xDC00 && low < 0xE000 {
					b.WriteRune(0x10000 + (u-0xD800)<<10 + (low - 0xDC00))
					continue
				}
				p.i = at
			}
			writeUnit(&b, u)
		default:
			return "", errSyntax
		}
	}
}

// writeUnit appends one UTF-16 unit: a character, or half of a pair in
// the three bytes its code point would have.
func writeUnit(b *strings.Builder, u rune) {
	if u >= 0xD800 && u < 0xE000 {
		b.Write([]byte{0xED, 0x80 | byte(u>>6&0x3F), 0x80 | byte(u&0x3F)})
		return
	}
	b.WriteRune(u)
}
