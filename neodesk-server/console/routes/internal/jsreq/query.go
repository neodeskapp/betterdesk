package jsreq

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
)

// Query is req.query[name]: the value Express's query parser (the qs
// package with arrayLimit 1000) gave a handler for one parameter of the
// query string.
//
//	?a=1           "1"
//	?a=1&a=2       []any{"1", "2"}
//	?a[]=1         []any{"1"}
//	?a[x]=1        an object
//	(no a)         nil
//
// A handler that wants plain text has to say what it does with the other
// forms, as the Node handler did without knowing: String, ParseInt and
// goapi.Truthy read them the way JavaScript does.
func Query(r *http.Request, name string) any {
	return QueryValue(r.URL.RawQuery, name)
}

const (
	qsArrayLimit     = 1000
	qsParameterLimit = 1000
	qsDepth          = 5
)

type missingType struct{}

// missing is JavaScript's undefined while a query is put together.
var missing = missingType{}

// qsMap is an object under construction: keys in the order they were set.
type qsMap struct {
	keys []string
	vals map[string]any
}

func (m *qsMap) get(key string) (any, bool) {
	v, ok := m.vals[key]
	return v, ok
}

func (m *qsMap) set(key string, v any) {
	if _, ok := m.vals[key]; !ok {
		m.keys = append(m.keys, key)
	}
	m.vals[key] = v
}

// qsNode is a list or an object while the query is put together.
type qsNode struct {
	list     bool
	at       map[int]any // a list's elements by index; a gap has no entry
	length   int         // a list's length: one past its highest index
	obj      *qsMap
	overflow bool // a list that outgrew the limit and became an object
	max      float64
	conflict bool // a list made from a value and an object under one key
	cmax     float64
}

func qsList(items ...any) *qsNode {
	n := &qsNode{list: true, at: map[int]any{}}
	for _, v := range items {
		n.push(v)
	}
	return n
}

// put sets a list's element at index i; missing leaves a gap there.
func (n *qsNode) put(i int, v any) {
	if v == missing {
		delete(n.at, i)
	} else {
		n.at[i] = v
	}
	n.length = max(n.length, i+1)
}

// push adds an element at the end of a list.
func (n *qsNode) push(v any) { n.put(n.length, v) }

// item is a list's element at index i, missing for a gap.
func (n *qsNode) item(i int) any {
	if v, ok := n.at[i]; ok {
		return v
	}
	return missing
}

// indexes are the indexes of a list's elements, ascending.
func (n *qsNode) indexes() []int {
	out := make([]int, 0, len(n.at))
	for i := range n.at {
		out = append(out, i)
	}
	slices.Sort(out)
	return out
}

func qsObject() *qsNode { return &qsNode{obj: &qsMap{vals: map[string]any{}}} }

func (n *qsNode) has(key string) bool {
	if n.list {
		i, ok := listIndex(key)
		_, has := n.at[i]
		return ok && has
	}
	_, ok := n.obj.get(key)
	return ok
}

func (n *qsNode) get(key string) any {
	if n.list {
		if i, ok := listIndex(key); ok {
			return n.item(i)
		}
		return missing
	}
	if v, ok := n.obj.get(key); ok {
		return v
	}
	return missing
}

func (n *qsNode) set(key string, v any) {
	if !n.list {
		n.obj.set(key, v)
		return
	}
	i, ok := listIndex(key)
	if !ok {
		return
	}
	n.put(i, v)
}

// keys lists the keys the way JavaScript does: array indexes in rising
// order, then the others as they were set.
func (n *qsNode) keys() []string {
	if n.list {
		var out []string
		for _, i := range n.indexes() {
			out = append(out, strconv.Itoa(i))
		}
		return out
	}
	var indexes, names []string
	for _, k := range n.obj.keys {
		if isArrayIndex(k) {
			indexes = append(indexes, k)
		} else {
			names = append(names, k)
		}
	}
	for i := 1; i < len(indexes); i++ {
		for j := i; j > 0; j-- {
			a, _ := strconv.ParseUint(indexes[j-1], 10, 64)
			b, _ := strconv.ParseUint(indexes[j], 10, 64)
			if a <= b {
				break
			}
			indexes[j-1], indexes[j] = indexes[j], indexes[j-1]
		}
	}
	return append(indexes, names...)
}

func (n *qsNode) count() int { return len(n.at) }

// isArrayIndex reports whether key is a canonical array index: a whole
// number from 0 to 2^32-2 written without leading zeros.
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

func listIndex(key string) (int, bool) {
	if !isArrayIndex(key) || len(key) > 7 {
		return 0, false
	}
	i, _ := strconv.Atoi(key)
	return i, true
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
	if err != nil || numberString(f) != key {
		return 0, false
	}
	return f, true
}

func qsToObject(list *qsNode) *qsNode {
	out := qsObject()
	for _, i := range list.indexes() {
		out.obj.set(strconv.Itoa(i), list.at[i])
	}
	return out
}

func qsOverflow(n *qsNode, max float64) *qsNode {
	n.overflow, n.max = true, max
	return n
}

func qsConflict(n *qsNode, max float64) *qsNode {
	n.conflict, n.cmax = true, max
	return n
}

// qsConcat is [].concat(a, b): lists are spread one level.
func qsConcat(parts ...any) *qsNode {
	out := qsList()
	for _, p := range parts {
		if n, ok := p.(*qsNode); ok && n.list {
			for i, v := range n.at {
				out.at[out.length+i] = v
			}
			out.length += n.length
		} else {
			out.push(p)
		}
	}
	return out
}

func qsCombine(a, b any) any {
	if n, ok := a.(*qsNode); ok && n.overflow {
		values := []any{b}
		if bn, ok := b.(*qsNode); ok && bn.list {
			values = make([]any, bn.length)
			for i := range values {
				values[i] = bn.item(i)
			}
		}
		index := n.max
		for _, v := range values {
			index++
			n.set(numberString(index), v)
		}
		n.max = index
		return n
	}
	result := qsConcat(a, b)
	if result.length > qsArrayLimit {
		return qsOverflow(qsToObject(result), float64(result.length-1))
	}
	return result
}

// qsDecode decodes one key or value: "+" is a space, and a text whose
// escapes do not decode stays as it is.
func qsDecode(s string) string {
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
// false where JavaScript throws a URIError.
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
	hi, okHi := hexDigit(s[i+1])
	lo, okLo := hexDigit(s[i+2])
	if !okHi || !okLo {
		return 0, false
	}
	return hi<<4 | lo, true
}

func hexDigit(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10, true
	}
	return 0, false
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

// qsSegments splits a key into its parent and its bracketed parts, five
// levels deep; what lies deeper stays one part.
func qsSegments(key string) []string {
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
	collected := 0
	for open >= 0 && collected < qsDepth {
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
			return append(segments, "["+key[open:]+"]")
		}
		segments = append(segments, key[open:closeAt+1])
		collected++
		open = indexFrom(key, '[', closeAt+1)
	}
	if open >= 0 {
		segments = append(segments, "["+key[open:]+"]")
	}
	return segments
}

func qsStrip(segment string) string {
	if len(segment) >= 2 && segment[0] == '[' && segment[len(segment)-1] == ']' {
		return segment[1 : len(segment)-1]
	}
	return segment
}

// qsBuild wraps a value in the lists and objects its key asks for.
func qsBuild(chain []string, leaf any) any {
	for i := len(chain) - 1; i >= 0; i-- {
		root := chain[i]
		if root == "[]" {
			if n, ok := leaf.(*qsNode); !ok || !n.overflow {
				leaf = qsCombine(qsList(), leaf)
			}
			continue
		}
		obj := qsObject()
		clean := qsStrip(root)
		index, isIndex := canonicalInt(clean)
		isIndex = isIndex && root != clean && index >= 0
		switch {
		case isIndex && index < qsArrayLimit:
			obj = qsList()
			obj.set(clean, leaf)
		case isIndex:
			obj.set(clean, leaf)
			qsOverflow(obj, index)
		case clean != "__proto__":
			obj.set(clean, leaf)
		}
		leaf = obj
	}
	return leaf
}

func qsTruthy(v any) bool {
	if s, ok := v.(string); ok {
		return s != ""
	}
	return v != nil && v != missing
}

func qsMerge(target, source any) any {
	if !qsTruthy(source) {
		return target
	}
	src, sourceIsNode := source.(*qsNode)
	tgt, targetIsNode := target.(*qsNode)
	if !sourceIsNode {
		switch {
		case targetIsNode && tgt.list:
			next := tgt.length
			if tgt.count() >= qsArrayLimit {
				return qsOverflow(qsToObject(qsConcat(tgt, source)), float64(next))
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
				return qsConflict(qsList(target, source), 1)
			}
		default:
			return qsList(target, source)
		}
		return target
	}
	if !targetIsNode {
		if src.overflow {
			result := qsObject()
			result.set("0", target)
			for _, k := range src.keys() {
				old, ok := ParseInt(k, 10)
				name := "NaN"
				if ok {
					name = numberString(old + 1)
				}
				result.set(name, src.get(k))
			}
			return qsOverflow(result, src.max+1)
		}
		combined := qsConcat(qsList(target), source)
		if combined.length > qsArrayLimit {
			return qsOverflow(qsToObject(combined), float64(combined.length-1))
		}
		return combined
	}
	mergeTarget := tgt
	if tgt.list && !src.list {
		mergeTarget = qsToObject(tgt)
		if tgt.conflict {
			qsConflict(mergeTarget, float64(tgt.length-1))
		}
	}
	if tgt.list && src.list {
		for _, i := range src.indexes() {
			item := src.at[i]
			key := strconv.Itoa(i)
			if tgt.has(key) {
				_, a := tgt.at[i].(*qsNode)
				_, b := item.(*qsNode)
				if a && b {
					tgt.at[i] = qsMerge(tgt.at[i], item)
				} else {
					tgt.push(item)
				}
			} else {
				tgt.set(key, item)
			}
		}
		if tgt.count() > qsArrayLimit {
			return qsOverflow(qsToObject(tgt), float64(tgt.length-1))
		}
		return tgt
	}
	acc := mergeTarget
	for _, key := range src.keys() {
		value := src.get(key)
		if acc.has(key) {
			acc.set(key, qsMerge(acc.get(key), value))
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
			qsOverflow(acc, max)
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

// qsValue turns the finished tree into plain values, dropping list gaps.
func qsValue(v any) any {
	n, ok := v.(*qsNode)
	if !ok {
		if v == missing {
			return nil
		}
		return v
	}
	if n.list {
		out := make([]any, 0, len(n.at))
		for _, i := range n.indexes() {
			out = append(out, qsValue(n.at[i]))
		}
		return out
	}
	out := goapi.NewObject()
	for _, k := range n.obj.keys {
		item, _ := n.obj.get(k)
		out.Set(k, qsValue(item))
	}
	return out
}

// QueryValue is Query for a raw query string (without the "?").
func QueryValue(rawQuery, name string) any {
	if rawQuery == "" {
		return nil
	}
	clean := strings.NewReplacer("%5B", "[", "%5b", "[", "%5D", "]", "%5d", "]").Replace(rawQuery)
	parts := strings.Split(clean, "&")
	if len(parts) > qsParameterLimit {
		parts = parts[:qsParameterLimit]
	}
	temp := map[string]any{}
	var order []string
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
			key = qsDecode(part)
		} else {
			key = qsDecode(part[:pos])
			val = qsDecode(part[pos+1:])
		}
		if existing, seen := temp[key]; seen {
			temp[key] = qsCombine(existing, val)
		} else {
			temp[key] = val
			order = append(order, key)
		}
	}
	var result any = missing
	for _, key := range order {
		if key == "" {
			continue
		}
		chain := qsSegments(key)
		if len(chain) == 0 || qsStrip(chain[0]) != name {
			continue
		}
		value := qsBuild(chain[1:], temp[key])
		if result == missing {
			result = value
		} else {
			result = qsMerge(result, value)
		}
	}
	return qsValue(result)
}
