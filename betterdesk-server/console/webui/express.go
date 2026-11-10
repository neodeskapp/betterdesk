package webui

import (
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/httpx"
)

// request is the address of a request as the console's routing reads it.
type request struct {
	original string // path and query as sent, not decoded
	path     string // the path, not decoded
	query    string // what follows the "?"
}

func parseRequest(r *http.Request) request {
	original := httpx.OriginalURL(r)
	target := original
	if !strings.HasPrefix(target, "/") && r.URL != nil {
		target = httpx.Latin1(r.URL.RequestURI())
	}
	if i := strings.IndexByte(target, '#'); i >= 0 {
		target = target[:i]
	}
	path, query, _ := strings.Cut(target, "?")
	if path == "" {
		path = "/"
	}
	return request{original: original, path: path, query: query}
}

// uriReserved are the characters decodeURI leaves percent-encoded.
const uriReserved = ";/?:@&=+$,#"

// decodeURIComponent decodes percent escapes as JavaScript does. It fails
// on a "%" without two hex digits and on escapes that are not UTF-8.
func decodeURIComponent(s string) (string, bool) {
	return decodeEscapes(s, "")
}

// decodeURI is decodeURIComponent, but an escape of a reserved character
// stays as written.
func decodeURI(s string) (string, bool) {
	return decodeEscapes(s, uriReserved)
}

func decodeEscapes(s, keep string) (string, bool) {
	if !strings.Contains(s, "%") {
		return s, true
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] != '%' {
			b.WriteByte(s[i])
			i++
			continue
		}
		first, ok := escapedByte(s, i)
		if !ok {
			return "", false
		}
		if first < utf8.RuneSelf {
			if strings.IndexByte(keep, first) >= 0 {
				b.WriteString(s[i : i+3])
			} else {
				b.WriteByte(first)
			}
			i += 3
			continue
		}
		size := 0
		switch {
		case first&0xE0 == 0xC0:
			size = 2
		case first&0xF0 == 0xE0:
			size = 3
		case first&0xF8 == 0xF0:
			size = 4
		default:
			return "", false
		}
		var buf [4]byte
		buf[0] = first
		for n := 1; n < size; n++ {
			next, ok := escapedByte(s, i+3*n)
			if !ok || next&0xC0 != 0x80 {
				return "", false
			}
			buf[n] = next
		}
		if !utf8.Valid(buf[:size]) {
			return "", false
		}
		b.Write(buf[:size])
		i += 3 * size
	}
	return b.String(), true
}

func escapedByte(s string, i int) (byte, bool) {
	if i+2 >= len(s) || s[i] != '%' || !isHex(s[i+1]) || !isHex(s[i+2]) {
		return 0, false
	}
	return unhex(s[i+1])<<4 | unhex(s[i+2]), true
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}

func unhex(c byte) byte {
	switch {
	case c >= 'a':
		return c - 'a' + 10
	case c >= 'A':
		return c - 'A' + 10
	}
	return c - '0'
}

// extname is the extension of the last name in a slash-separated path, as
// Node's path.extname gives it: from the last dot to the end, and nothing
// when the name starts with that dot.
func extname(path string) string {
	startDot, startPart, end := -1, 0, -1
	matchedSlash := true
	preDotState := 0
	for i := len(path) - 1; i >= 0; i-- {
		c := path[i]
		if c == '/' {
			if !matchedSlash {
				startPart = i + 1
				break
			}
			continue
		}
		if end == -1 {
			matchedSlash = false
			end = i + 1
		}
		if c == '.' {
			if startDot == -1 {
				startDot = i
			} else if preDotState != 1 {
				preDotState = 1
			}
		} else if startDot != -1 {
			preDotState = -1
		}
	}
	if startDot == -1 || end == -1 || preDotState == 0 ||
		(preDotState == 1 && startDot == end-1 && startDot == startPart+1) {
		return ""
	}
	return path[startDot:end]
}

// encodeURIKeep are the characters JavaScript's encodeURI leaves alone,
// besides letters and digits.
const encodeURIKeep = ";/?:@&=+$,-_.!~*'()#"

// encodeURLAllowed reports whether a Location header may carry c as it is.
func encodeURLAllowed(c byte) bool {
	return c == 0x21 || c >= 0x23 && c <= 0x3B || c == 0x3D || c >= 0x3F && c <= 0x5F ||
		c >= 0x61 && c <= 0x7A || c == 0x7C || c == 0x7E
}

// encodeURL makes an address fit for a Location header: what a URL cannot
// carry is percent-encoded, escapes that are already there stay, and a "%"
// that starts no escape becomes %25.
func encodeURL(s string) string {
	if !utf8.ValidString(s) {
		s = string([]rune(s))
	}
	var b strings.Builder
	b.Grow(len(s) + 16)
	encode := func(i int) int {
		c := s[i]
		if c < utf8.RuneSelf {
			if isAlphanumeric(c) || strings.IndexByte(encodeURIKeep, c) >= 0 {
				b.WriteByte(c)
			} else {
				writePercent(&b, c)
			}
			return 1
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		for j := 0; j < size; j++ {
			writePercent(&b, s[i+j])
		}
		return size
	}
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c != '%' && c < utf8.RuneSelf && encodeURLAllowed(c):
			b.WriteByte(c)
			i++
		case c != '%':
			i += encode(i)
		case i+1 == len(s):
			b.WriteString("%25")
			i++
		case !isHex(s[i+1]):
			b.WriteString("%25")
			i++
			i += encode(i)
		case i+2 == len(s) || isHex(s[i+2]):
			b.WriteByte('%')
			i++
		default:
			b.WriteString("%25")
			b.WriteByte(s[i+1])
			i += 2
			i += encode(i)
		}
	}
	return b.String()
}

const upperHex = "0123456789ABCDEF"

func writePercent(b *strings.Builder, c byte) {
	b.WriteByte('%')
	b.WriteByte(upperHex[c>>4])
	b.WriteByte(upperHex[c&0x0F])
}

func isAlphanumeric(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

var htmlEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&#39;")

// redirect answers with a redirect the way the console always has: the
// Location header, and a short text or HTML body picked by the Accept
// header.
func redirect(w http.ResponseWriter, r *http.Request, status int, target string) {
	location := encodeURL(target)
	h := w.Header()
	h.Set("Location", location)
	kind := accepts(r, "text/plain", "text/html")
	appendVary(h, "Accept")
	body := ""
	switch kind {
	case "text/plain":
		h.Set("Content-Type", "text/plain; charset=utf-8")
		body = http.StatusText(status) + ". Redirecting to " + location
	case "text/html":
		h.Set("Content-Type", "text/html; charset=utf-8")
		body = "<p>" + http.StatusText(status) + ". Redirecting to " + htmlEscaper.Replace(location) + "</p>"
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if r.Method != http.MethodHead {
		_, _ = w.Write([]byte(body))
	}
}

// appendVary adds a field to the Vary header unless it is there already.
func appendVary(h http.Header, field string) {
	current := strings.Join(h.Values("Vary"), ", ")
	if current == "" {
		h.Set("Vary", field)
		return
	}
	for _, name := range strings.Split(current, ",") {
		name = strings.Trim(name, " ")
		if name == "*" {
			h.Set("Vary", "*")
			return
		}
		if strings.EqualFold(name, field) {
			h.Set("Vary", current)
			return
		}
	}
	h.Set("Vary", current+", "+field)
}

// jsSpaceChars is what JavaScript's \s matches, for use inside a character
// class; jsSpace is that class.
const (
	jsSpaceChars = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`
	jsSpace      = `[` + jsSpaceChars + `]`
)

func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

func jsTrim(s string) string {
	return strings.TrimFunc(s, isJSSpace)
}

// mediaRange is one entry of an Accept header.
type mediaRange struct {
	kind, subtype string
	params        map[string]string
	q             float64
	order         int
}

var mediaRangePattern = regexp.MustCompile(`^` + jsSpace + `*([^` + jsSpaceChars + `/;]+)/([^;` + jsSpaceChars + `]+)` + jsSpace + `*(?:;(.*))?$`)

// accepts picks the media type the client likes best among the offered
// ones, as the "accepts" package of the old console did: the first offer
// without an Accept header, "" when the client takes none.
func accepts(r *http.Request, offers ...string) string {
	header := httpx.Latin1(strings.Join(r.Header.Values("Accept"), ", "))
	if header == "" {
		return offers[0]
	}
	ranges := parseAccept(header)
	type priority struct {
		q       float64
		s, o, i int
	}
	chosen := make([]priority, 0, len(offers))
	for i, offer := range offers {
		kind, subtype, _ := strings.Cut(offer, "/")
		best := priority{o: -1, i: i}
		for _, candidate := range ranges {
			s, ok := specificity(kind, subtype, candidate)
			if !ok {
				continue
			}
			if firstTruthy(float64(best.s-s), best.q-candidate.q, float64(best.o-candidate.order)) < 0 {
				best = priority{q: candidate.q, s: s, o: candidate.order, i: i}
			}
		}
		if best.q > 0 {
			chosen = append(chosen, best)
		}
	}
	if len(chosen) == 0 {
		return ""
	}
	sort.SliceStable(chosen, func(a, b int) bool {
		x, y := chosen[a], chosen[b]
		return firstTruthy(y.q-x.q, float64(y.s-x.s), float64(x.o-y.o), float64(x.i-y.i)) < 0
	})
	return offers[chosen[0].i]
}

// firstTruthy is JavaScript's a || b || c for numbers: the first that is
// neither zero nor NaN, else the last.
func firstTruthy(values ...float64) float64 {
	for _, v := range values {
		if v != 0 && !math.IsNaN(v) {
			return v
		}
	}
	return values[len(values)-1]
}

// specificity says how exactly a media range names a type: 4 for the type,
// 2 for the subtype, 1 for parameters. An offer has no parameters, so a
// range with parameters fits only when each is "*" or empty.
func specificity(kind, subtype string, candidate mediaRange) (int, bool) {
	s := 0
	switch {
	case strings.EqualFold(candidate.kind, kind):
		s |= 4
	case candidate.kind != "*":
		return 0, false
	}
	switch {
	case strings.EqualFold(candidate.subtype, subtype):
		s |= 2
	case candidate.subtype != "*":
		return 0, false
	}
	if len(candidate.params) > 0 {
		for _, value := range candidate.params {
			if value != "*" && value != "" {
				return 0, false
			}
		}
		s |= 1
	}
	return s, true
}

func parseAccept(header string) []mediaRange {
	var ranges []mediaRange
	for i, part := range splitQuoted(header, ",") {
		m := mediaRangePattern.FindStringSubmatch(jsTrim(part))
		if m == nil {
			continue
		}
		entry := mediaRange{kind: m[1], subtype: m[2], q: 1, order: i}
		if m[3] != "" {
			for _, param := range splitQuoted(m[3], ";") {
				key, value, _ := strings.Cut(jsTrim(param), "=")
				if n := len(value); n > 0 && value[0] == '"' && value[n-1] == '"' {
					if n == 1 {
						value = ""
					} else {
						value = value[1 : n-1]
					}
				}
				if strings.ToLower(key) == "q" {
					entry.q = parseFloat(value)
					break
				}
				if entry.params == nil {
					entry.params = map[string]string{}
				}
				entry.params[strings.ToLower(key)] = value
			}
		}
		ranges = append(ranges, entry)
	}
	return ranges
}

// splitQuoted splits at a separator, but not inside double quotes.
func splitQuoted(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := parts[:1]
	for _, part := range parts[1:] {
		last := len(out) - 1
		if strings.Count(out[last], `"`)%2 == 0 {
			out = append(out, part)
		} else {
			out[last] += sep + part
		}
	}
	return out
}

var floatPrefix = regexp.MustCompile(`^[+-]?(?:Infinity|(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]+)?)`)

// parseFloat reads the number a string starts with, as JavaScript's
// parseFloat does; NaN when it starts with none.
func parseFloat(s string) float64 {
	m := floatPrefix.FindString(strings.TrimLeftFunc(s, isJSSpace))
	if m == "" {
		return math.NaN()
	}
	if strings.HasSuffix(m, "Infinity") {
		if m[0] == '-' {
			return math.Inf(-1)
		}
		return math.Inf(1)
	}
	v, err := strconv.ParseFloat(m, 64)
	if err != nil && !math.IsInf(v, 0) {
		return math.NaN()
	}
	return v
}

// queryParameterLimit is how many parameters of a query string count.
const queryParameterLimit = 1000

// queryString reads one parameter of a query string that must be a single
// plain string, as the console's query parser (qs) saw it. It answers false
// when the parameter is missing, given more than once, or given with
// brackets (name[]=..., name[key]=...), which make a list or an object.
//
// "[name]=x" counts as "name=x". A second spelling of the name does not
// spoil the first when its value is empty: "name=x&[name]=" is "x".
func queryString(query, name string) (string, bool) {
	query = replaceFold(replaceFold(query, "%5B", "["), "%5D", "]")
	parts := strings.Split(query, "&")
	if len(parts) > queryParameterLimit {
		parts = parts[:queryParameterLimit]
	}
	// One entry per key as written; a key given twice is a list.
	type entry struct {
		value string
		once  bool
	}
	entries := map[string]*entry{}
	var order []string
	for _, part := range parts {
		pos := strings.Index(part, "]=")
		if pos >= 0 {
			pos++
		} else {
			pos = strings.IndexByte(part, '=')
		}
		rawKey, rawValue := part, ""
		if pos >= 0 {
			rawKey, rawValue = part[:pos], part[pos+1:]
		}
		key := decodeFormValue(rawKey)
		if seen, ok := entries[key]; ok {
			seen.once = false
			continue
		}
		entries[key] = &entry{value: decodeFormValue(rawValue), once: true}
		order = append(order, key)
	}

	const (
		missing = iota
		plain
		compound
	)
	value, state := "", missing
	for _, key := range order {
		top, nested := topLevelKey(key)
		if key == "" || top != name {
			continue
		}
		e := entries[key]
		isString := e.once && !nested
		switch {
		case state == missing && isString:
			value, state = e.value, plain
		case state == missing:
			state = compound
		case isString && e.value == "":
			// an empty value merges into what is there without a trace
		default:
			state = compound
		}
	}
	return value, state == plain
}

// topLevelKey is the name a query key ends up under: "a" for "a", "a[b]"
// and "[a]", and whether the key goes deeper than that name.
func topLevelKey(key string) (top string, nested bool) {
	first := strings.IndexByte(key, '[')
	switch {
	case first < 0:
		return key, false
	case first > 0:
		return key[:first], true
	}
	level := 0
	for i := 0; i < len(key); i++ {
		switch key[i] {
		case '[':
			level++
		case ']':
			if level--; level == 0 {
				return key[1:i], strings.IndexByte(key[i+1:], '[') >= 0
			}
		}
	}
	// A bracket that never closes: the key is a name as it stands
	return key, false
}

// decodeFormValue decodes a key or a value of a query string: "+" is a
// space, and what does not decode stays as written.
func decodeFormValue(s string) string {
	s = strings.ReplaceAll(s, "+", " ")
	if decoded, ok := decodeURIComponent(s); ok {
		return decoded
	}
	return s
}

// replaceFold replaces every occurrence of an ASCII pattern in any letter
// case.
func replaceFold(s, pattern, with string) string {
	if len(s) < len(pattern) {
		return s
	}
	var b strings.Builder
	start := 0
	for i := 0; i+len(pattern) <= len(s); {
		if asciiEqualFold(s[i:i+len(pattern)], pattern) {
			b.WriteString(s[start:i])
			b.WriteString(with)
			i += len(pattern)
			start = i
			continue
		}
		i++
	}
	if start == 0 {
		return s
	}
	b.WriteString(s[start:])
	return b.String()
}

func asciiEqualFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if x >= 'A' && x <= 'Z' {
			x += 'a' - 'A'
		}
		if y >= 'A' && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}

// hasPrefixFold reports whether s starts with an ASCII prefix in any
// letter case.
func hasPrefixFold(s, prefix string) bool {
	return len(s) >= len(prefix) && asciiEqualFold(s[:len(prefix)], prefix)
}
