package i18n

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// CookieName is the cookie that remembers the console language.
const CookieName = "betterdesk_lang"

// cookieMaxAge is one year in seconds.
const cookieMaxAge = 365 * 24 * 60 * 60

// Choice is a language value as Express hands it to a route: the lang
// query parameter after the query parser, or the cookie after the cookie
// parser. Both can be lists or objects (?lang[]=de, a cookie starting with
// j:), and those follow JavaScript's rules for truth and text.
type Choice struct {
	// Truthy says whether the value counts as given. An empty string
	// does not; a list or object always does.
	Truthy bool
	// Name is the value as text: what is looked up as a language.
	Name string
	// Cookie is the text Express stores when the value is written to a
	// cookie: the string itself, or j: and JSON for a list or object.
	Cookie string
	// Throws is set for a value JavaScript cannot turn into text (an
	// object with its own toString key). A request with such a value is
	// answered with status 500.
	Throws bool
}

// Text is the Choice for a plain string.
func Text(s string) Choice {
	return Choice{Truthy: s != "", Name: s, Cookie: s}
}

func choiceOf(v any) Choice {
	if v == undefined {
		return Choice{}
	}
	if s, ok := v.(string); ok {
		return Text(s)
	}
	name, ok := jsString(v)
	c := Choice{Truthy: truthy(v), Name: name, Throws: !ok}
	if isObjectLike(v) {
		c.Cookie = "j:" + stringify(v, "")
	} else {
		c.Cookie = name
	}
	return c
}

// ErrNotText is the failure for a language value that JavaScript cannot
// turn into text. Its text is the TypeError's message, which the console's
// error answer with status 500 carries outside production.
var ErrNotText = errors.New("Cannot convert object to primitive value")

// PickLanguage returns the language of a request.
//
// The first given value of query, the signed-in user's preferred language,
// cookie and the Accept-Language header is taken; when none is given the
// default language. If the taken value is not a language (has says no),
// the default language is used: a wrong ?lang= does not fall through to
// the cookie.
//
// Accept-Language is read loosely: entries sorted by q (missing q is 1,
// an unreadable q compares equal to everything, as in JavaScript), and for
// each entry first the full tag, case-sensitive ("zh-TW"), then the part
// before the first dash ("zh").
func PickLanguage(has func(string) bool, query Choice, preferred string, cookie Choice, acceptLanguage, defaultLang string) (string, error) {
	var chosen Choice
	switch {
	case query.Truthy:
		chosen = query
	case preferred != "":
		chosen = Text(preferred)
	case cookie.Truthy:
		chosen = cookie
	default:
		chosen = Text(defaultLang)
		if lang := acceptedLanguage(has, acceptLanguage); lang != "" {
			chosen = Text(lang)
		}
	}
	if chosen.Throws {
		return "", ErrNotText
	}
	if !has(chosen.Name) {
		return defaultLang, nil
	}
	return chosen.Name, nil
}

// Selection is the language of a request and the cookie to set for it.
type Selection struct {
	// Lang is the request language.
	Lang string
	// SetCookie is the Set-Cookie header value to add, or empty. The
	// cookie is set when ?lang= names a language.
	SetCookie string
	// Err is ErrNotText for a query or cookie value that cannot be
	// text; the request is to be answered with status 500. Lang is then
	// the default language.
	Err error
}

// RequestLanguage picks the language for r. preferred is the signed-in
// user's stored language, empty when there is none or nobody is signed in.
// now is the time the cookie's expiry is counted from.
func (b *Bundle) RequestLanguage(r *http.Request, preferred string, now time.Time) Selection {
	query := QueryChoice(r.URL.RawQuery)
	cookie := CookieChoice(strings.Join(r.Header.Values("Cookie"), "; "))
	accept := strings.Join(r.Header.Values("Accept-Language"), ", ")
	lang, err := PickLanguage(b.Has, query, preferred, cookie, accept, b.DefaultLang)
	if err != nil {
		return Selection{Lang: b.DefaultLang, Err: err}
	}
	sel := Selection{Lang: lang}
	if query.Truthy && b.Has(query.Name) {
		sel.SetCookie = LangCookie(query.Cookie, false, now)
	}
	return sel
}

// LangCookie formats the Set-Cookie value for the language cookie in
// Express's attribute order: one year, path /, readable by scripts,
// SameSite=Lax, and Secure when secure is set.
func LangCookie(value string, secure bool, now time.Time) string {
	out := CookieName + "=" + encodeURIComponent(value) +
		"; Max-Age=" + strconv.Itoa(cookieMaxAge) +
		"; Path=/; Expires=" + now.Add(cookieMaxAge*time.Second).UTC().Format(http.TimeFormat)
	if secure {
		out += "; Secure"
	}
	return out + "; SameSite=Lax"
}

// encodeURIComponent escapes every byte except A-Z a-z 0-9 - _ . ! ~ * ' ( ).
func encodeURIComponent(s string) string {
	const upper = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.IndexByte("-_.!~*'()", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(upper[c>>4])
		b.WriteByte(upper[c&0xF])
	}
	return b.String()
}

// decodeURIComponent decodes %XX sequences that form valid UTF-8. ok is
// false where JavaScript throws a URIError.
func decodeURIComponent(s string) (string, bool) {
	if !strings.Contains(s, "%") {
		return s, true
	}
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
	hi, lo := hexValue(uint16(s[i+1])), hexValue(uint16(s[i+2]))
	if hi < 0 || lo < 0 {
		return 0, false
	}
	return byte(hi<<4 | lo), true
}

// --- the cookie ---

// CookieChoice reads the language cookie from a Cookie header the way
// cookie-parser reads it: the first cookie of that name counts, quotes around
// the value are dropped, percent escapes are decoded when they are valid,
// and a value starting with j: is read as JSON when that gives a true
// value.
func CookieChoice(header string) Choice {
	raw, ok := cookieValue(latin1(header), len(header), CookieName)
	if !ok {
		return Choice{}
	}
	if strings.HasPrefix(raw, "j:") {
		if v, err := parseJSON(raw[2:]); err == nil && truthy(v) {
			return choiceOf(v)
		}
	}
	return Text(raw)
}

func cookieValue(str string, length int, name string) (string, bool) {
	if length < 2 {
		return "", false
	}
	n := len(str)
	index := 0
	for {
		eq := indexFrom(str, '=', index)
		if eq == -1 {
			break
		}
		end := indexFrom(str, ';', index)
		if end == -1 {
			end = n
		} else if eq > end {
			index = strings.LastIndexByte(str[:eq], ';') + 1
			continue
		}
		keyStart := cookieStart(str, index, eq)
		keyEnd := cookieEnd(str, eq, keyStart)
		if str[keyStart:keyEnd] == name {
			valStart := cookieStart(str, eq+1, end)
			valEnd := cookieEnd(str, end, valStart)
			if valStart < n && valEnd >= 1 && str[valStart] == '"' && str[valEnd-1] == '"' {
				valStart++
				valEnd--
			}
			val := ""
			if valStart < valEnd {
				val = str[valStart:valEnd]
			}
			if decoded, ok := decodeURIComponent(val); ok {
				val = decoded
			}
			return val, true
		}
		index = end + 1
		if index >= n {
			break
		}
	}
	return "", false
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

func cookieStart(s string, index, max int) int {
	for {
		if index >= len(s) || (s[index] != ' ' && s[index] != '\t') {
			return index
		}
		index++
		if index >= max {
			return max
		}
	}
}

func cookieEnd(s string, index, min int) int {
	for index > min {
		index--
		if s[index] != ' ' && s[index] != '\t' {
			return index + 1
		}
	}
	return min
}

// --- Accept-Language ---

type acceptEntry struct {
	raw, code string
	q         float64
}

func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

// acceptedLanguage returns the first language of an Accept-Language header
// that has knows, or "".
func acceptedLanguage(has func(string) bool, header string) string {
	if header == "" {
		return ""
	}
	var list []acceptEntry
	for _, item := range strings.Split(latin1(header), ",") {
		parts := strings.Split(jsTrim(item), ";")
		raw := jsTrim(parts[0])
		code, _, _ := strings.Cut(raw, "-")
		q := 1.0
		if len(parts) > 1 && parts[1] != "" {
			q = math.NaN()
			if _, value, found := strings.Cut(parts[1], "="); found {
				value, _, _ = strings.Cut(value, "=")
				q = parseFloat(value)
			}
		}
		list = append(list, acceptEntry{raw, code, q})
	}
	v8Sort(list, func(a, b acceptEntry) float64 {
		d := b.q - a.q
		if math.IsNaN(d) {
			return 0
		}
		return d
	})
	for _, e := range list {
		if has(e.raw) {
			return e.raw
		}
		if has(e.code) {
			return e.code
		}
	}
	return ""
}

// parseFloat is JavaScript's parseFloat: the longest prefix that reads as
// a decimal number after leading white space, NaN when there is none.
func parseFloat(s string) float64 {
	s = strings.TrimLeftFunc(s, isJSSpace)
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	if strings.HasPrefix(s[i:], "Infinity") {
		if s[0] == '-' {
			return math.Inf(-1)
		}
		return math.Inf(1)
	}
	digits := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		digits++
	}
	if i < len(s) && s[i] == '.' {
		j := i + 1
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
			digits++
		}
		if digits > 0 {
			i = j
		}
	}
	if digits == 0 {
		return math.NaN()
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		if j < len(s) && s[j] >= '0' && s[j] <= '9' {
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			i = j
		}
	}
	f, _ := strconv.ParseFloat(s[:i], 64)
	return f
}

// --- the query parameter ---

// QueryChoice reads the lang parameter from a raw query string the way
// Express's query parser (qs) reads it: repeated and bracketed parameters
// (lang=a&lang=b, lang[]=a, lang[0]=a, lang[x]=a) become lists and
// objects.
func QueryChoice(rawQuery string) Choice {
	return choiceOf(qsParam(rawQuery, "lang"))
}

const (
	qsArrayLimit     = 1000
	qsParameterLimit = 1000
	qsDepth          = 5
)

// qsNode is a list or an object while the query is put together.
type qsNode struct {
	list     bool
	items    []any // a list's elements; undefined marks a gap
	obj      *Object
	overflow bool // a list that outgrew the limit and became an object
	max      float64
	conflict bool // a list made from a value and an object under one key
	cmax     float64
}

func qsList(items ...any) *qsNode { return &qsNode{list: true, items: items} }

func qsObject() *qsNode { return &qsNode{obj: NewObject()} }

func (n *qsNode) has(key string) bool {
	if n.list {
		i, ok := listIndex(key)
		return ok && i < len(n.items) && n.items[i] != undefined
	}
	_, ok := n.obj.Get(key)
	return ok
}

func (n *qsNode) get(key string) any {
	if n.list {
		if i, ok := listIndex(key); ok && i < len(n.items) {
			return n.items[i]
		}
		return undefined
	}
	if v, ok := n.obj.Get(key); ok {
		return v
	}
	return undefined
}

func (n *qsNode) set(key string, v any) {
	if !n.list {
		n.obj.Set(key, v)
		return
	}
	i, ok := listIndex(key)
	if !ok {
		return
	}
	for len(n.items) <= i {
		n.items = append(n.items, undefined)
	}
	n.items[i] = v
}

func (n *qsNode) keys() []string {
	if !n.list {
		return append([]string(nil), n.obj.Keys()...)
	}
	var out []string
	for i, v := range n.items {
		if v != undefined {
			out = append(out, strconv.Itoa(i))
		}
	}
	return out
}

func (n *qsNode) count() int {
	c := 0
	for _, v := range n.items {
		if v != undefined {
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
	for i, v := range list.items {
		if v != undefined {
			out.obj.Set(strconv.Itoa(i), v)
		}
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
			out.items = append(out.items, n.items...)
		} else {
			out.items = append(out.items, p)
		}
	}
	return out
}

func qsCombine(a, b any) any {
	if n, ok := a.(*qsNode); ok && n.overflow {
		values := []any{b}
		if bn, ok := b.(*qsNode); ok && bn.list {
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
	result := qsConcat(a, b)
	if len(result.items) > qsArrayLimit {
		return qsOverflow(qsToObject(result), float64(len(result.items)-1))
	}
	return result
}

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
	return v != nil && v != undefined
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
			next := len(tgt.items)
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
				old, ok := parseIntPrefix(k)
				name := "NaN"
				if ok {
					name = numberString(old + 1)
				}
				result.set(name, src.get(k))
			}
			return qsOverflow(result, src.max+1)
		}
		combined := qsConcat(qsList(target), source)
		if len(combined.items) > qsArrayLimit {
			return qsOverflow(qsToObject(combined), float64(len(combined.items)-1))
		}
		return combined
	}
	mergeTarget := tgt
	if tgt.list && !src.list {
		mergeTarget = qsToObject(tgt)
		if tgt.conflict {
			qsConflict(mergeTarget, float64(len(tgt.items)-1))
		}
	}
	if tgt.list && src.list {
		for i, item := range src.items {
			if item == undefined {
				continue
			}
			key := strconv.Itoa(i)
			if tgt.has(key) {
				_, a := tgt.items[i].(*qsNode)
				_, b := item.(*qsNode)
				if a && b {
					tgt.items[i] = qsMerge(tgt.items[i], item)
				} else {
					tgt.items = append(tgt.items, item)
				}
			} else {
				tgt.set(key, item)
			}
		}
		if tgt.count() > qsArrayLimit {
			return qsOverflow(qsToObject(tgt), float64(len(tgt.items)-1))
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

// parseIntPrefix is parseInt(s, 10).
func parseIntPrefix(s string) (float64, bool) {
	s = strings.TrimLeftFunc(s, isJSSpace)
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

// qsValue turns the finished tree into plain values, dropping list gaps.
func qsValue(v any) any {
	n, ok := v.(*qsNode)
	if !ok {
		return v
	}
	if n.list {
		out := []any{}
		for _, item := range n.items {
			if item != undefined {
				out = append(out, qsValue(item))
			}
		}
		return out
	}
	out := NewObject()
	for _, k := range n.obj.Keys() {
		item, _ := n.obj.Get(k)
		out.Set(k, qsValue(item))
	}
	return out
}

// qsParam parses a raw query string and returns the value of one top-level
// parameter, or undefined.
func qsParam(rawQuery, name string) any {
	if rawQuery == "" {
		return undefined
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
		existing, seen := temp[key]
		switch {
		case seen:
			temp[key] = qsCombine(existing, val)
		default:
			temp[key] = val
			order = append(order, key)
		}
	}
	var result any = undefined
	for _, key := range order {
		if key == "" {
			continue
		}
		chain := qsSegments(key)
		if len(chain) == 0 || qsStrip(chain[0]) != name {
			continue
		}
		value := qsBuild(chain[1:], temp[key])
		if result == undefined {
			result = value
		} else {
			result = qsMerge(result, value)
		}
	}
	return qsValue(result)
}
