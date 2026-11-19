package meshcentral

import (
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/httpx"
)

// The mesh routes hand the whole query of a request on to the API server
// and read link tokens from it. Both depend on how the console has always
// read a query: repeated and bracketed parameters (a=1&a=2, a[]=1, a[x]=1)
// become lists and objects, and such a value turned into text is "1,2" or
// "[object Object]". This file reads a query that way and writes it out
// again the way the old routes did.

// query is the parsed query of a request: a text, a list ([]any) or an
// object (*object) per parameter.
type query struct {
	values *object
}

// requestQuery reads the query of a request. A byte above 0x7F counts as
// the Latin-1 character with that number, and a "#" ends the query.
func requestQuery(r *http.Request) *query {
	raw, _, _ := strings.Cut(r.URL.RawQuery, "#")
	return parseQuery(httpx.Latin1(raw))
}

// get is the value of a parameter, nil when the query has none.
func (q *query) get(name string) any {
	v, ok := q.values.get(name)
	if !ok {
		return nil
	}
	return v
}

// set gives a parameter a text value. A new parameter goes to the end.
func (q *query) set(name, value string) {
	q.values.set(name, value)
}

// delete removes a parameter.
func (q *query) delete(name string) {
	q.values.delete(name)
}

// search writes the query as the text that goes to the API server: one
// name=value per parameter, a list as its elements joined by commas, an
// object as "[object Object]". ok is false when a value cannot be turned
// into text.
func (q *query) search() (text string, ok bool) {
	var b strings.Builder
	for i, key := range q.values.keys() {
		v, _ := q.values.get(key)
		value, ok := toText(v)
		if !ok {
			return "", false
		}
		if i > 0 {
			b.WriteByte('&')
		}
		formEncode(&b, key)
		b.WriteByte('=')
		formEncode(&b, value)
	}
	return b.String(), true
}

// formEncode writes s the way a form is encoded: letters, digits and
// * - . _ as they are, a space as +, every other byte as %XX.
func formEncode(b *strings.Builder, s string) {
	const upperhex = "0123456789ABCDEF"
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '*', c == '-', c == '.', c == '_':
			b.WriteByte(c)
		case c == ' ':
			b.WriteByte('+')
		default:
			b.WriteByte('%')
			b.WriteByte(upperhex[c>>4])
			b.WriteByte(upperhex[c&15])
		}
	}
}

// toText is String(v) for a value of a query. An object with a member
// named toString has no text: JavaScript finds the member where it looks
// for the function and gives up.
func toText(v any) (string, bool) {
	switch t := v.(type) {
	case nil:
		return "", true
	case string:
		return t, true
	case []any:
		parts := make([]string, len(t))
		for i, item := range t {
			part, ok := toText(item)
			if !ok {
				return "", false
			}
			parts[i] = part
		}
		return strings.Join(parts, ","), true
	case *object:
		if _, own := t.get("toString"); own {
			return "", false
		}
		return "[object Object]", true
	}
	return "", false
}

// truthy is !!v for a value of a query: false for a missing parameter and
// for the empty text.
func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case string:
		return t != ""
	}
	return true
}

// object is an object whose keys come in the order JavaScript lists
// them: array indexes ascending, then the other keys as they were added.
type object struct {
	m     map[string]any
	order []string
}

func newObject() *object { return &object{m: map[string]any{}} }

func (o *object) get(key string) (any, bool) {
	v, ok := o.m[key]
	return v, ok
}

func (o *object) set(key string, value any) {
	if _, ok := o.m[key]; !ok {
		o.order = append(o.order, key)
	}
	o.m[key] = value
}

func (o *object) delete(key string) {
	if _, ok := o.m[key]; !ok {
		return
	}
	delete(o.m, key)
	o.order = slices.DeleteFunc(o.order, func(k string) bool { return k == key })
}

func (o *object) keys() []string {
	var indexes, names []string
	for _, key := range o.order {
		if isArrayIndex(key) {
			indexes = append(indexes, key)
		} else {
			names = append(names, key)
		}
	}
	if len(indexes) == 0 {
		return names
	}
	sort.Slice(indexes, func(i, j int) bool {
		a, _ := strconv.ParseUint(indexes[i], 10, 64)
		b, _ := strconv.ParseUint(indexes[j], 10, 64)
		return a < b
	})
	return append(indexes, names...)
}

// isArrayIndex reports whether key is a whole number from 0 to 2^32-2
// written without leading zeros.
func isArrayIndex(key string) bool {
	if key == "" || len(key) > 10 || (len(key) > 1 && key[0] == '0') {
		return false
	}
	var n uint64
	for i := 0; i < len(key); i++ {
		c := key[i]
		if c < '0' || c > '9' {
			return false
		}
		n = n*10 + uint64(c-'0')
	}
	return n <= 4294967294
}

// The limits of the console's query parser.
const (
	arrayLimit     = 1000
	parameterLimit = 1000
	depthLimit     = 5
)

type gap struct{}

// missing marks a place of a list that has no element.
var missing = gap{}

// node is a list or an object while a query is put together.
type node struct {
	list     bool
	items    []any // a list's elements; missing marks a gap
	obj      *object
	overflow bool // a list that outgrew the limit and became an object
	max      float64
	conflict bool // a list made from a value and an object under one key
	cmax     float64
}

func listOf(items ...any) *node { return &node{list: true, items: items} }

func objectNode() *node { return &node{obj: newObject()} }

func (n *node) has(key string) bool {
	if n.list {
		i, ok := listIndex(key)
		return ok && i < len(n.items) && n.items[i] != missing
	}
	_, ok := n.obj.get(key)
	return ok
}

func (n *node) get(key string) any {
	if n.list {
		if i, ok := listIndex(key); ok && i < len(n.items) {
			return n.items[i]
		}
		return missing
	}
	if v, ok := n.obj.get(key); ok {
		return v
	}
	return missing
}

func (n *node) set(key string, v any) {
	if !n.list {
		n.obj.set(key, v)
		return
	}
	i, ok := listIndex(key)
	if !ok {
		return
	}
	for len(n.items) <= i {
		n.items = append(n.items, missing)
	}
	n.items[i] = v
}

func (n *node) keys() []string {
	if !n.list {
		return n.obj.keys()
	}
	var out []string
	for i, v := range n.items {
		if v != missing {
			out = append(out, strconv.Itoa(i))
		}
	}
	return out
}

func (n *node) count() int {
	c := 0
	for _, v := range n.items {
		if v != missing {
			c++
		}
	}
	return c
}

func listIndex(key string) (int, bool) {
	if !isArrayIndex(key) || len(key) > 7 {
		return 0, false
	}
	i, _ := strconv.Atoi(key)
	return i, true
}

// numberString is how JavaScript writes a whole number that is not
// negative.
func numberString(f float64) string {
	if f >= 1e21 {
		return strconv.FormatFloat(f, 'e', -1, 64)
	}
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// canonicalInt reads key as a whole number when it is written the way
// JavaScript prints that number.
func canonicalInt(key string) (float64, bool) {
	digits := strings.TrimPrefix(key, "-")
	if digits == "" || (len(digits) > 1 && digits[0] == '0') || key == "-0" {
		return 0, false
	}
	for i := 0; i < len(digits); i++ {
		if digits[i] < '0' || digits[i] > '9' {
			return 0, false
		}
	}
	f, err := strconv.ParseFloat(key, 64)
	if err != nil {
		return 0, false
	}
	printed := numberString(f)
	if f < 0 {
		printed = "-" + numberString(-f)
	}
	if printed != key {
		return 0, false
	}
	return f, true
}

func toObjectNode(list *node) *node {
	out := objectNode()
	for i, v := range list.items {
		if v != missing {
			out.obj.set(strconv.Itoa(i), v)
		}
	}
	return out
}

func markOverflow(n *node, max float64) *node {
	n.overflow, n.max = true, max
	return n
}

func markConflict(n *node, max float64) *node {
	n.conflict, n.cmax = true, max
	return n
}

// concat is [].concat(a, b): lists are spread one level.
func concat(parts ...any) *node {
	out := listOf()
	for _, p := range parts {
		if n, ok := p.(*node); ok && n.list {
			out.items = append(out.items, n.items...)
		} else {
			out.items = append(out.items, p)
		}
	}
	return out
}

// combine puts a second value under a key that already has one.
func combine(a, b any) any {
	if n, ok := a.(*node); ok && n.overflow {
		values := []any{b}
		if bn, ok := b.(*node); ok && bn.list {
			values = bn.items
		}
		index := n.max
		for _, v := range values {
			index++
			n.set(numberString(index), v)
		}
		n.max = index
		return n
	}
	result := concat(a, b)
	if len(result.items) > arrayLimit {
		return markOverflow(toObjectNode(result), float64(len(result.items)-1))
	}
	return result
}

// decodePart decodes a key or a value: + is a space, and escapes that do
// not decode leave the text as it is.
func decodePart(s string) string {
	plain := strings.ReplaceAll(s, "+", " ")
	if !strings.Contains(s, "%") {
		return plain
	}
	if decoded, ok := decodeURIComponent(plain); ok {
		return decoded
	}
	return plain
}

// decodeURIComponent decodes %XX sequences that form valid UTF-8. ok is
// false where JavaScript throws.
func decodeURIComponent(s string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '%' {
			b.WriteByte(s[i])
			i++
			continue
		}
		first, ok := percentByte(s, i)
		if !ok {
			return "", false
		}
		i += 3
		if first < 0x80 {
			b.WriteByte(first)
			continue
		}
		n := 0
		switch {
		case first&0xE0 == 0xC0:
			n = 2
		case first&0xF0 == 0xE0:
			n = 3
		case first&0xF8 == 0xF0:
			n = 4
		default:
			return "", false
		}
		seq := []byte{first}
		for len(seq) < n {
			next, ok := percentByte(s, i)
			if !ok || next&0xC0 != 0x80 {
				return "", false
			}
			seq = append(seq, next)
			i += 3
		}
		if r, size := utf8.DecodeRune(seq); r == utf8.RuneError || size != n {
			return "", false
		}
		b.Write(seq)
	}
	return b.String(), true
}

func percentByte(s string, i int) (byte, bool) {
	if i+2 >= len(s) || s[i] != '%' {
		return 0, false
	}
	hi, lo := hexDigit(s[i+1]), hexDigit(s[i+2])
	if hi < 0 || lo < 0 {
		return 0, false
	}
	return byte(hi<<4 | lo), true
}

func hexDigit(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

func indexFrom(s string, c byte, from int) int {
	if from >= len(s) {
		return -1
	}
	i := strings.IndexByte(s[from:], c)
	if i < 0 {
		return -1
	}
	return from + i
}

// segments cuts a key into its name and its bracketed parts: a[b][] is a,
// [b], []. What lies deeper than the limit stays one part.
func segments(key string) []string {
	var out []string
	first := strings.IndexByte(key, '[')
	parent := key
	if first >= 0 {
		parent = key[:first]
	}
	if parent != "" {
		out = append(out, parent)
	}
	open := first
	collected := 0
	for open >= 0 && collected < depthLimit {
		level := 1
		closeAt := -1
		for i := open + 1; i < len(key) && closeAt < 0; i++ {
			switch key[i] {
			case '[':
				level++
			case ']':
				level--
				if level == 0 {
					closeAt = i
				}
			}
		}
		if closeAt < 0 {
			return append(out, "["+key[open:]+"]")
		}
		out = append(out, key[open:closeAt+1])
		collected++
		open = indexFrom(key, '[', closeAt+1)
	}
	if open >= 0 {
		out = append(out, "["+key[open:]+"]")
	}
	return out
}

func stripBrackets(segment string) string {
	if len(segment) >= 2 && segment[0] == '[' && segment[len(segment)-1] == ']' {
		return segment[1 : len(segment)-1]
	}
	return segment
}

// build wraps a value in the lists and objects its key asks for.
func build(chain []string, leaf any) any {
	for i := len(chain) - 1; i >= 0; i-- {
		root := chain[i]
		if root == "[]" {
			if n, ok := leaf.(*node); !ok || !n.overflow {
				leaf = combine(listOf(), leaf)
			}
			continue
		}
		obj := objectNode()
		clean := stripBrackets(root)
		index, isIndex := canonicalInt(clean)
		isIndex = isIndex && root != clean && index >= 0
		switch {
		case isIndex && index < arrayLimit:
			obj = listOf()
			obj.set(clean, leaf)
		case isIndex:
			obj.set(clean, leaf)
			markOverflow(obj, index)
		case clean != "__proto__":
			obj.set(clean, leaf)
		}
		leaf = obj
	}
	return leaf
}

func present(v any) bool {
	if s, ok := v.(string); ok {
		return s != ""
	}
	return v != nil && v != missing
}

// merge puts source into target, as the parser does for two keys that
// lead to the same place.
func merge(target, source any) any {
	if !present(source) {
		return target
	}
	src, sourceIsNode := source.(*node)
	tgt, targetIsNode := target.(*node)
	if !sourceIsNode {
		switch {
		case targetIsNode && tgt.list:
			next := len(tgt.items)
			if tgt.count() >= arrayLimit {
				return markOverflow(toObjectNode(concat(tgt, source)), float64(next))
			}
			tgt.set(strconv.Itoa(next), source)
		case targetIsNode:
			switch {
			case tgt.overflow:
				tgt.max++
				tgt.set(numberString(tgt.max), source)
			case tgt.conflict:
				tgt.cmax++
				tgt.set(numberString(tgt.cmax), source)
			default:
				return markConflict(listOf(target, source), 1)
			}
		default:
			return listOf(target, source)
		}
		return target
	}
	if !targetIsNode {
		if src.overflow {
			result := objectNode()
			result.set("0", target)
			for _, k := range src.keys() {
				old, ok := parseIntPrefix(k)
				name := "NaN"
				if ok {
					name = signedNumberString(old + 1)
				}
				result.set(name, src.get(k))
			}
			return markOverflow(result, src.max+1)
		}
		combined := concat(listOf(target), source)
		if len(combined.items) > arrayLimit {
			return markOverflow(toObjectNode(combined), float64(len(combined.items)-1))
		}
		return combined
	}
	mergeTarget := tgt
	if tgt.list && !src.list {
		mergeTarget = toObjectNode(tgt)
		if tgt.conflict {
			markConflict(mergeTarget, float64(len(tgt.items)-1))
		}
	}
	if tgt.list && src.list {
		for i, item := range src.items {
			if item == missing {
				continue
			}
			key := strconv.Itoa(i)
			if tgt.has(key) {
				_, a := tgt.items[i].(*node)
				_, b := item.(*node)
				if a && b {
					tgt.items[i] = merge(tgt.items[i], item)
				} else {
					tgt.items = append(tgt.items, item)
				}
			} else {
				tgt.set(key, item)
			}
		}
		if tgt.count() > arrayLimit {
			return markOverflow(toObjectNode(tgt), float64(len(tgt.items)-1))
		}
		return tgt
	}
	acc := mergeTarget
	for _, key := range src.keys() {
		value := src.get(key)
		if acc.has(key) {
			acc.set(key, merge(acc.get(key), value))
		} else {
			acc.set(key, value)
		}
		if src.overflow && !acc.overflow {
			max := src.max
			for _, k := range acc.keys() {
				if n, ok := canonicalInt(k); ok && n > max {
					max = n
				}
			}
			markOverflow(acc, max)
		}
		n, isInt := canonicalInt(key)
		if acc.overflow && isInt && n >= 0 && n > acc.max {
			acc.max = n
		}
		if acc.conflict && isInt && n > acc.cmax {
			acc.cmax = n
		}
	}
	return acc
}

func signedNumberString(f float64) string {
	if f < 0 {
		return "-" + numberString(-f)
	}
	return numberString(f)
}

// parseIntPrefix is parseInt(s, 10).
func parseIntPrefix(s string) (float64, bool) {
	s = strings.TrimLeftFunc(s, isSpace)
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	start := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == start {
		return 0, false
	}
	f, _ := strconv.ParseFloat(s[:i], 64)
	return f, true
}

// plain turns the finished tree into texts, []any and *object, dropping
// the gaps of lists.
func plain(v any) any {
	n, ok := v.(*node)
	if !ok {
		return v
	}
	if n.list {
		out := []any{}
		for _, item := range n.items {
			if item != missing {
				out = append(out, plain(item))
			}
		}
		return out
	}
	out := newObject()
	for _, k := range n.obj.keys() {
		item, _ := n.obj.get(k)
		out.set(k, plain(item))
	}
	return out
}

// parseQuery reads a raw query string the way the console's query parser
// does.
func parseQuery(raw string) *query {
	if raw == "" {
		return &query{values: newObject()}
	}
	clean := strings.NewReplacer("%5B", "[", "%5b", "[", "%5D", "]", "%5d", "]").Replace(raw)
	parts := strings.Split(clean, "&")
	if len(parts) > parameterLimit {
		parts = parts[:parameterLimit]
	}
	pairs := newObject()
	for _, part := range parts {
		pos := strings.Index(part, "]=")
		if pos == -1 {
			pos = strings.IndexByte(part, '=')
		} else {
			pos++
		}
		var key string
		var val any = ""
		if pos == -1 {
			key = decodePart(part)
		} else {
			key = decodePart(part[:pos])
			val = decodePart(part[pos+1:])
		}
		if existing, seen := pairs.get(key); seen {
			pairs.set(key, combine(existing, val))
		} else {
			pairs.set(key, val)
		}
	}
	var result any = objectNode()
	for _, key := range pairs.keys() {
		if key == "" {
			continue
		}
		value, _ := pairs.get(key)
		result = merge(result, build(segments(key), value))
	}
	values, ok := plain(result).(*object)
	if !ok {
		values = newObject()
	}
	return &query{values: values}
}
