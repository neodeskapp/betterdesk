package goapipath

import (
	"errors"
	"net/http"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
)

// The query string as Express read it: qs.parse(text, { allowPrototypes:
// true, arrayLimit: 1000 }) of qs 6.17. A name that comes once is text. A
// name that comes several times, or with brackets (a[]=1, a[0]=1), is a
// list; a[b]=1 is an object. Lists nest to five levels, hold up to 1000
// elements and turn into objects beyond that, and the first 1000 pairs
// count. The functions below follow parse.js and utils.js of that package
// step by step, because the results for mixed forms (a=1&a[b]=2) follow
// from the steps and from nothing simpler.

const (
	queryArrayLimit     = 1000
	queryParameterLimit = 1000
	queryDepth          = 5
)

type queryKind int

const (
	queryUndefined queryKind = iota
	queryString
	queryArray
	queryObject
)

// queryNode is one value of the parsed query. A nil *queryNode is
// undefined: a name that is not there, or a hole in a list.
type queryNode struct {
	kind queryKind
	text string
	// at holds a list's elements by index while the query is put
	// together, and length is the list's length as JavaScript counts it:
	// a hole has no entry, so a[999]=x holds one element. compact turns
	// them into items, the elements without the holes.
	at     map[int]*queryNode
	length int
	items  []*queryNode
	// keys are an object's keys in the order they were set.
	keys    []string
	members map[string]*queryNode
	// overflow marks an object that stands for a list beyond the limit,
	// maxIndex is its highest index.
	overflow bool
	maxIndex float64
	// conflict marks a list or object made from a text and an object under
	// one name, conflictIndex is its highest index.
	conflict      bool
	conflictIndex float64
}

func textNode(s string) *queryNode { return &queryNode{kind: queryString, text: s} }

func listNode(items ...*queryNode) *queryNode {
	n := &queryNode{kind: queryArray, at: map[int]*queryNode{}}
	for _, item := range items {
		n.push(item)
	}
	return n
}

// put sets a list's element at index i; nil leaves a hole there.
func (n *queryNode) put(i int, item *queryNode) {
	if item == nil {
		delete(n.at, i)
	} else {
		n.at[i] = item
	}
	n.length = max(n.length, i+1)
}

// push adds an element at the end of a list.
func (n *queryNode) push(item *queryNode) { n.put(n.length, item) }

// indexes are the indexes of a list's elements, ascending.
func (n *queryNode) indexes() []int {
	out := make([]int, 0, len(n.at))
	for i := range n.at {
		out = append(out, i)
	}
	slices.Sort(out)
	return out
}

func objectNode() *queryNode { return &queryNode{kind: queryObject, members: map[string]*queryNode{}} }

func (n *queryNode) isList() bool { return n != nil && n.kind == queryArray }

// isContainer is typeof n === 'object': a list or an object.
func (n *queryNode) isContainer() bool {
	return n != nil && (n.kind == queryArray || n.kind == queryObject)
}

// falsy is !n: undefined or the empty text.
func (n *queryNode) falsy() bool {
	return n == nil || n.kind == queryUndefined || (n.kind == queryString && n.text == "")
}

func (n *queryNode) has(key string) bool {
	_, ok := n.members[key]
	return ok
}

func (n *queryNode) set(key string, value *queryNode) {
	if _, ok := n.members[key]; !ok {
		n.keys = append(n.keys, key)
	}
	n.members[key] = value
}

// arrayIndexKey reports whether an object key is a whole number as
// JavaScript writes one; such keys come first when an object lists its
// keys.
func arrayIndexKey(key string) (uint64, bool) {
	if key == "" || len(key) > 10 || (len(key) > 1 && key[0] == '0') {
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

// orderedKeys is Object.keys(n) for an object: whole numbers ascending,
// then the other keys as they were set.
func (n *queryNode) orderedKeys() []string {
	keys := append([]string(nil), n.keys...)
	sort.SliceStable(keys, func(a, b int) bool {
		ia, oka := arrayIndexKey(keys[a])
		ib, okb := arrayIndexKey(keys[b])
		if oka && okb {
			return ia < ib
		}
		return oka && !okb
	})
	return keys
}

// integerKey is the test "String(parseInt(key, 10)) === key".
func integerKey(key string) (float64, bool) {
	n := parseInt(key, false)
	if n != n || NumberText(n) != key {
		return 0, false
	}
	return n, true
}

// arrayToObject turns a list into an object with its indexes as keys.
// Holes are left out.
func arrayToObject(list *queryNode) *queryNode {
	obj := objectNode()
	for _, i := range list.indexes() {
		obj.set(NumberText(float64(i)), list.at[i])
	}
	return obj
}

func markOverflow(obj *queryNode, maxIndex float64) *queryNode {
	obj.overflow, obj.maxIndex = true, maxIndex
	return obj
}

func markConflict(n *queryNode, index float64) *queryNode {
	n.conflict, n.conflictIndex = true, index
	return n
}

// concat is [].concat(parts...): a list is spread one level, anything
// else is one element.
func concat(parts ...*queryNode) *queryNode {
	out := listNode()
	for _, part := range parts {
		if part.isList() {
			for i, item := range part.at {
				out.at[out.length+i] = item
			}
			out.length += part.length
			continue
		}
		out.push(part)
	}
	return out
}

// combine is utils.combine: b joins a, and a list that grows beyond the
// limit becomes an overflow object.
func combine(a, b *queryNode) *queryNode {
	if a != nil && a.overflow {
		values := []*queryNode{b}
		if b.isList() {
			values = make([]*queryNode, b.length)
			for i := range values {
				values[i] = b.at[i]
			}
		}
		index := a.maxIndex
		for _, value := range values {
			index++
			a.set(NumberText(index), value)
		}
		a.maxIndex = index
		return a
	}
	result := concat(a, b)
	if result.length > queryArrayLimit {
		return markOverflow(arrayToObject(result), float64(result.length-1))
	}
	return result
}

// maxNumericIndex is the highest whole-number key of an object, and
// minimum when none is higher.
func maxNumericIndex(obj *queryNode, minimum float64) float64 {
	highest := minimum
	for _, key := range obj.keys {
		if n, ok := integerKey(key); ok && n > highest {
			highest = n
		}
	}
	return highest
}

// merge is utils.merge with strictMerge on: source is merged into target
// and the result returned. Lists and objects are changed in place.
func merge(target, source *queryNode) *queryNode {
	if source.falsy() {
		return target
	}
	if !source.isContainer() {
		switch {
		case target.isList():
			next := target.length
			if len(target.at) >= queryArrayLimit {
				return markOverflow(arrayToObject(concat(target, source)), float64(next))
			}
			target.push(source)
		case target.isContainer():
			switch {
			case target.overflow:
				target.maxIndex++
				target.set(NumberText(target.maxIndex), source)
			case target.conflict:
				target.conflictIndex++
				target.set(NumberText(target.conflictIndex), source)
			default:
				return markConflict(listNode(target, source), 1)
			}
		default:
			return listNode(target, source)
		}
		return target
	}

	if !target.isContainer() {
		if source.overflow {
			result := objectNode()
			result.set("0", target)
			for _, key := range source.orderedKeys() {
				result.set(NumberText(parseInt(key, false)+1), source.members[key])
			}
			return markOverflow(result, source.maxIndex+1)
		}
		combined := concat(listNode(target), source)
		if combined.length > queryArrayLimit {
			return markOverflow(arrayToObject(combined), float64(combined.length-1))
		}
		return combined
	}

	if target.isList() && source.isList() {
		for _, i := range source.indexes() {
			item := source.at[i]
			switch existing := target.at[i]; {
			case existing != nil:
				if existing.isContainer() && item.isContainer() {
					target.at[i] = merge(existing, item)
				} else {
					target.push(item)
				}
			default:
				target.put(i, item)
			}
		}
		if len(target.at) > queryArrayLimit {
			return markOverflow(arrayToObject(target), float64(target.length-1))
		}
		return target
	}

	acc := target
	if target.isList() {
		acc = arrayToObject(target)
		if target.conflict {
			markConflict(acc, float64(target.length-1))
		}
	}
	for _, key := range sourceKeys(source) {
		value := sourceValue(source, key)
		if acc.has(key) {
			acc.set(key, merge(acc.members[key], value))
		} else {
			acc.set(key, value)
		}
		if source.overflow && !acc.overflow {
			markOverflow(acc, maxNumericIndex(acc, source.maxIndex))
		}
		if acc.overflow {
			if n, ok := integerKey(key); ok && n >= 0 && n > acc.maxIndex {
				acc.maxIndex = n
			}
		}
		if acc.conflict {
			if n, ok := integerKey(key); ok && n > acc.conflictIndex {
				acc.conflictIndex = n
			}
		}
	}
	return acc
}

// sourceKeys is Object.keys of a list or an object.
func sourceKeys(source *queryNode) []string {
	if !source.isList() {
		return source.orderedKeys()
	}
	var keys []string
	for _, i := range source.indexes() {
		keys = append(keys, NumberText(float64(i)))
	}
	return keys
}

func sourceValue(source *queryNode, key string) *queryNode {
	if !source.isList() {
		return source.members[key]
	}
	index, _ := arrayIndexKey(key)
	return source.at[int(index)]
}

// splitKey is splitKeyIntoSegments: "a[b][c]" is a, [b], [c]. Groups
// beyond the depth, and a group that is not closed, stay one segment.
func splitKey(key string) []string {
	var segments []string
	first := strings.IndexByte(key, '[')
	parent := key
	if first >= 0 {
		parent = key[:first]
	}
	if parent != "" {
		segments = append(segments, parent)
	}
	open := first
	for collected := 0; open >= 0 && collected < queryDepth; collected++ {
		level, closed := 1, -1
		for i := open + 1; i < len(key) && closed < 0; i++ {
			switch key[i] {
			case '[':
				level++
			case ']':
				level--
				if level == 0 {
					closed = i
				}
			}
		}
		if closed < 0 {
			return append(segments, "["+key[open:]+"]")
		}
		segments = append(segments, key[open:closed+1])
		next := strings.IndexByte(key[closed+1:], '[')
		if next < 0 {
			return segments
		}
		open = closed + 1 + next
	}
	if open >= 0 {
		segments = append(segments, "["+key[open:]+"]")
	}
	return segments
}

// parseObject builds the value a key stands for from its segments, from
// the innermost outwards.
func parseObject(chain []string, value *queryNode) *queryNode {
	leaf := value
	for i := len(chain) - 1; i >= 0; i-- {
		root := chain[i]
		var obj *queryNode
		if root == "[]" {
			if leaf != nil && leaf.overflow {
				obj = leaf
			} else {
				obj = combine(listNode(), leaf)
			}
		} else {
			obj = objectNode()
			clean := root
			if strings.HasPrefix(root, "[") && strings.HasSuffix(root, "]") && len(root) >= 2 {
				clean = root[1 : len(root)-1]
			}
			index, whole := integerKey(clean)
			validIndex := whole && root != clean && index >= 0
			switch {
			case validIndex && index < queryArrayLimit:
				obj = listNode()
				obj.put(int(index), leaf)
			case validIndex:
				obj.set(clean, leaf)
				markOverflow(obj, index)
			case clean != "__proto__":
				obj.set(clean, leaf)
			}
		}
		leaf = obj
	}
	return leaf
}

// decodeQueryPart is utils.decode: "+" is a space, and %XX is decoded
// when the whole text decodes; otherwise it stays as it is.
func decodeQueryPart(s string) string {
	s = strings.ReplaceAll(s, "+", " ")
	if !strings.Contains(s, "%") {
		return s
	}
	if decoded, ok := decodeURIComponent(s); ok {
		return decoded
	}
	return s
}

// decodeURIComponent decodes %XX the way JavaScript does. It fails where
// JavaScript throws: at a % without two hex digits and at escaped bytes
// that are not well-formed UTF-8.
func decodeURIComponent(s string) (string, bool) {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '%' {
			b.WriteByte(s[i])
			i++
			continue
		}
		var run []byte
		for i < len(s) && s[i] == '%' {
			if i+2 >= len(s) || !isDigit(s[i+1], 16) || !isDigit(s[i+2], 16) {
				return "", false
			}
			run = append(run, hexValue(s[i+1])<<4|hexValue(s[i+2]))
			i += 3
		}
		// Every character's bytes are escaped one after the other, so a
		// run of escapes holds whole characters or the text is refused.
		if !utf8.Valid(run) {
			return "", false
		}
		b.Write(run)
	}
	return b.String(), true
}

func hexValue(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	}
	return c - 'A' + 10
}

var encodedBrackets = strings.NewReplacer("%5B", "[", "%5b", "[", "%5D", "]", "%5d", "]")

// parseQuery is qs.parse of a raw query string with Express's options.
func parseQuery(raw string) *queryNode {
	root := objectNode()
	if raw == "" {
		return root
	}
	parts := strings.Split(encodedBrackets.Replace(raw), "&")
	if len(parts) > queryParameterLimit {
		parts = parts[:queryParameterLimit]
	}
	values := objectNode()
	for _, part := range parts {
		pos := strings.Index(part, "]=")
		if pos >= 0 {
			pos++
		} else {
			pos = strings.IndexByte(part, '=')
		}
		key, value := decodeQueryPart(part), ""
		if pos >= 0 {
			key, value = decodeQueryPart(part[:pos]), decodeQueryPart(part[pos+1:])
		}
		if values.has(key) {
			values.set(key, combine(values.members[key], textNode(value)))
		} else {
			values.set(key, textNode(value))
		}
	}
	for _, key := range values.orderedKeys() {
		if key == "" {
			continue
		}
		root = merge(root, parseObject(splitKey(key), values.members[key]))
	}
	compact(root)
	return root
}

// compact turns every list below n into its elements without the holes.
func compact(n *queryNode) {
	if n == nil {
		return
	}
	switch n.kind {
	case queryArray:
		n.items = make([]*queryNode, 0, len(n.at))
		for _, i := range n.indexes() {
			n.items = append(n.items, n.at[i])
		}
		n.at = nil
		for _, item := range n.items {
			compact(item)
		}
	case queryObject:
		for _, key := range n.keys {
			compact(n.members[key])
		}
	}
}

// QueryValue is one value of req.query.
type QueryValue struct {
	node *queryNode
}

// Query is req.query[name] for the request.
func Query(r *http.Request, name string) QueryValue {
	return QueryOf(r.URL.RawQuery, name)
}

// QueryOf is Query for a raw query string, the text after the "?" as the
// client sent it.
func QueryOf(rawQuery, name string) QueryValue {
	raw, _, _ := strings.Cut(rawQuery, "#")
	root := parseQuery(httpx.Latin1(raw))
	if root.kind != queryObject {
		return QueryValue{}
	}
	return QueryValue{node: root.members[name]}
}

// Exists is value !== undefined: the query names it.
func (v QueryValue) Exists() bool { return v.node != nil }

// Truthy is !!value: false for a name that is missing or empty (a=),
// true for every other text, every list and every object.
func (v QueryValue) Truthy() bool { return !v.node.falsy() }

// ErrNotPrimitive is the TypeError JavaScript throws when it has to make
// text of an object that has a member named toString (a[toString]=1).
var ErrNotPrimitive = errors.New("Cannot convert object to primitive value")

// Text is String(value): the text itself, the elements of a list joined
// by commas, "[object Object]" for an object and "undefined" for a name
// that is missing. It fails with ErrNotPrimitive where JavaScript throws.
func (v QueryValue) Text() (string, error) {
	if v.node == nil {
		return "undefined", nil
	}
	return nodeText(v.node)
}

func nodeText(n *queryNode) (string, error) {
	switch n.kind {
	case queryString:
		return n.text, nil
	case queryArray:
		parts := make([]string, len(n.items))
		for i, item := range n.items {
			if item == nil {
				continue
			}
			text, err := nodeText(item)
			if err != nil {
				return "", err
			}
			parts[i] = text
		}
		return strings.Join(parts, ","), nil
	}
	if n.has("toString") {
		return "", ErrNotPrimitive
	}
	return "[object Object]", nil
}

// OneText is the value when the query gives it as text: name=value once,
// without brackets. ok is false for a missing name, a list and an object.
func (v QueryValue) OneText() (text string, ok bool) {
	if v.node == nil || v.node.kind != queryString {
		return "", false
	}
	return v.node.text, true
}
