package contract

import (
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strings"
)

// A volatile value is not compared by value, but its format still is: a
// timestamp that comes as "2026-11-10 01:02:03" must not turn into
// "2026-11-10T01:02:03.000Z" unnoticed. The format of a string is its
// class: every digit written as 9, every ASCII lower-case letter as a,
// every ASCII upper-case letter as A, everything else as it is.

// VolatileMark marks a path of a JSON body as volatile, with what is still
// checked there. In a golden a mark without a format is written as the path
// alone.
type VolatileMark struct {
	// Path is the place in the body (see path).
	Path string `json:"path"`
	// Format is the class every string at the path had in the recordings:
	// "9999-99-99 99:99:99". A string there must have this class.
	Format string `json:"format,omitempty"`
	// Pattern is set instead of Format when the classes differed only in
	// how long a run of digits or letters is, or when the strings are
	// random tokens (see deriveFormat). It is a regular expression the
	// class of a string there must match: "^9+\.9+\.9+\.9+$",
	// "^[9aA+/_=-]{64}$".
	Pattern string `json:"pattern,omitempty"`
	// Integer says every number at the path was a whole number in the
	// recordings. A number there must be whole.
	Integer bool `json:"integer,omitempty"`
	// AnyOrder marks an array whose elements come in no fixed order. Its
	// value is not volatile: it must hold the same elements, compared
	// after sorting both sides. Set by hand with the prefix AnyOrder.
	AnyOrder bool `json:"anyOrder,omitempty"`
	// Elements is set on a length mark ([#]) when the golden keeps its
	// first element as the sample of every element: each element must
	// equal it, except at the marks below the array ([*]). Without it
	// (goldens recorded before samples were kept) each element only has
	// to be of the sample's JSON type.
	Elements bool `json:"elements,omitempty"`
	// Optional marks a key that may be missing. Where it is there, its
	// value is volatile. Set by hand with the prefix Optional.
	Optional bool `json:"optional,omitempty"`
}

// MarshalJSON writes a mark without a format as its path.
func (m VolatileMark) MarshalJSON() ([]byte, error) {
	type plain VolatileMark
	var value any = plain(m)
	if m.Format == "" && m.Pattern == "" && !m.Integer && !m.AnyOrder && !m.Elements && !m.Optional {
		value = m.Path
	}
	return marshalPlain(value)
}

// UnmarshalJSON reads a path or a mark with its format.
func (m *VolatileMark) UnmarshalJSON(data []byte) error {
	*m = VolatileMark{}
	if len(data) > 0 && data[0] == '"' {
		return json.Unmarshal(data, &m.Path)
	}
	type plain VolatileMark
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode((*plain)(m)); err != nil {
		return fmt.Errorf("volatile mark: %w", err)
	}
	if m.Path == "" {
		return fmt.Errorf("volatile mark without a path")
	}
	return nil
}

// plainMarks makes marks without formats from paths. A path that starts
// with AnyOrder makes a mark of an array in any order, one that starts
// with Optional a mark of a key that may be missing.
func plainMarks(paths []string) []VolatileMark {
	if len(paths) == 0 {
		return nil
	}
	marks := make([]VolatileMark, len(paths))
	for i, p := range paths {
		if rest, ok := strings.CutPrefix(p, AnyOrder); ok {
			marks[i] = VolatileMark{Path: rest, AnyOrder: true}
			continue
		}
		if rest, ok := strings.CutPrefix(p, Optional); ok {
			marks[i] = VolatileMark{Path: rest, Optional: true}
			continue
		}
		marks[i] = VolatileMark{Path: p}
	}
	return marks
}

// markPaths lists the paths of marks, a mark of an array in any order
// with the prefix AnyOrder, a mark of a key that may be missing with the
// prefix Optional.
func markPaths(marks []VolatileMark) []string {
	if len(marks) == 0 {
		return nil
	}
	paths := make([]string, len(marks))
	for i, m := range marks {
		paths[i] = m.text()
	}
	return paths
}

// text is the mark's path as plainMarks reads it.
func (m VolatileMark) text() string {
	if m.AnyOrder {
		return AnyOrder + m.Path
	}
	if m.Optional {
		return Optional + m.Path
	}
	return m.Path
}

// formatClass is the class of a string: digits become 9, ASCII lower-case
// letters a, ASCII upper-case letters A, every other character stays.
func formatClass(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			b.WriteByte('9')
		case r >= 'a' && r <= 'z':
			b.WriteByte('a')
		case r >= 'A' && r <= 'Z':
			b.WriteByte('A')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// withoutVersionSuffix writes the product version as the version without
// its suffix where it has one ("3.6.0-rc.2" as "3.6.0"), so a value made
// from it has the same class on a release candidate as on a release: the
// cache version "3.6.0-rc.2.1795409105554" of restart-status (L302). A
// version inside a longer one is left alone.
func withoutVersionSuffix(s string) string {
	version := productVersion()
	cut := strings.IndexAny(version, "-+")
	if cut <= 0 || !strings.Contains(s, version) {
		return s
	}
	pattern := regexp.MustCompile(`(^|[^0-9.])` + regexp.QuoteMeta(version) + `($|[^0-9A-Za-z])`)
	return replaceAddress(s, pattern, version[:cut])
}

// classRun is a piece of a class: a run of 9, a or A, or one other character.
type classRun struct {
	char   rune
	length int
}

func classRuns(class string) []classRun {
	var runs []classRun
	for _, r := range class {
		isClass := r == '9' || r == 'a' || r == 'A'
		if n := len(runs); isClass && n > 0 && runs[n-1].char == r {
			runs[n-1].length++
			continue
		}
		runs = append(runs, classRun{r, 1})
	}
	return runs
}

// generalizeClasses finds what the classes of the values at one volatile
// path have in common. When all are the same, that class is the format.
// When they differ only in the length of runs of digits or letters, the
// result is a pattern: a regular expression with 9+, a+ or A+ for those
// runs. Otherwise there is nothing to check and both results are empty.
func generalizeClasses(classes []string) (format, pattern string) {
	if len(classes) == 0 || classes[0] == "" {
		return "", ""
	}
	same := true
	for _, class := range classes[1:] {
		if class != classes[0] {
			same = false
			break
		}
	}
	if same {
		return classes[0], ""
	}
	base := classRuns(classes[0])
	varies := make([]bool, len(base))
	for _, class := range classes[1:] {
		runs := classRuns(class)
		if len(runs) != len(base) {
			return "", ""
		}
		for i, run := range runs {
			if run.char != base[i].char {
				return "", ""
			}
			if run.length != base[i].length {
				varies[i] = true
			}
		}
	}
	var b strings.Builder
	b.WriteString("^")
	for i, run := range base {
		if varies[i] {
			b.WriteString(string(run.char) + "+")
			continue
		}
		b.WriteString(regexp.QuoteMeta(strings.Repeat(string(run.char), run.length)))
	}
	b.WriteString("$")
	return "", b.String()
}

// isInteger reports whether a JSON number is a whole number.
func isInteger(n json.Number) bool {
	r, ok := new(big.Rat).SetString(string(n))
	return ok && r.IsInt()
}

// deriveFormat finds the format of the strings at one volatile path. It is
// generalizeClasses with one guard: a string where letters and digits touch
// ("mwa1slae", a hash, a UUID, a key in base64) is a random token unless
// its letters are the same in every sample (the T and Z of a timestamp, the
// m and s of "3m12.5s"). In a random token the runs of letters and digits
// fall where chance puts them, and so do the other characters of its
// alphabet (+ and / in base64, - and _ in its URL form), so two samples
// that happen to agree say nothing about the third. Such strings, and
// strings whose classes differ in more than run lengths, get a pattern of
// words instead: every stretch of letters, digits and + / - _ = is one
// word, written [9aA+/_=-]{n} when all samples have n characters there and
// [9aA+/_=-]+ otherwise. A UUID gives ^[9aA+/_=-]{36}$, a 32-byte key in
// base64 ^[9aA+/_=-]{44}$.
func deriveFormat(values []string) (format, pattern string) {
	values = append([]string(nil), values...)
	classes := make([]string, len(values))
	for i, value := range values {
		values[i] = withoutVersionSuffix(value)
		classes[i] = formatClass(values[i])
	}
	format, pattern = generalizeClasses(classes)
	if format == "" && pattern == "" {
		return "", wordPattern(classes)
	}
	if lettersTouchDigits(classes) && !sameLetters(values) {
		return "", wordPattern(classes)
	}
	return loosenNanoseconds(format, pattern)
}

// nanoseconds is how a fraction of nine digits looks in a class, and
// anyFraction the pattern that takes its place.
const (
	nanoseconds = ".999999999"
	anyFraction = `(\.9{1,9})?`
)

// loosenNanoseconds lets a fraction of exactly nine digits be shorter or
// missing. Nine digits after a dot are nanoseconds as Go prints them, in a
// time (RFC3339Nano) and in a duration, and Go leaves out the zeros at the
// end: one time in ten "04:05:28.305952740Z" comes as "04:05:28.30595274Z".
func loosenNanoseconds(format, pattern string) (string, string) {
	rest, loosened := pattern, false
	if format != "" {
		rest = "^" + regexp.QuoteMeta(format) + "$"
	}
	quoted := regexp.QuoteMeta(nanoseconds)
	var b strings.Builder
	for {
		i := strings.Index(rest, quoted)
		if i < 0 {
			b.WriteString(rest)
			break
		}
		after := rest[i+len(quoted):]
		b.WriteString(rest[:i])
		if strings.HasPrefix(after, "9") || strings.HasPrefix(after, "+") {
			// More than nine digits, or a run of any length already.
			b.WriteString(quoted)
		} else {
			b.WriteString(anyFraction)
			loosened = true
		}
		rest = after
	}
	if !loosened {
		return format, pattern
	}
	return "", b.String()
}

// lettersTouchDigits reports whether a class has two runs side by side
// with nothing between them: letters next to digits, or a change of case.
func lettersTouchDigits(classes []string) bool {
	isClass := func(r rune) bool { return r == '9' || r == 'a' || r == 'A' }
	for _, class := range classes {
		runs := classRuns(class)
		for i := 1; i < len(runs); i++ {
			if isClass(runs[i-1].char) && isClass(runs[i].char) {
				return true
			}
		}
	}
	return false
}

var letterRuns = regexp.MustCompile(`[A-Za-z]+`)

// sameLetters reports whether every value has the same letters in the same
// order, stretch by stretch.
func sameLetters(values []string) bool {
	if len(values) == 0 {
		return true
	}
	first := strings.Join(letterRuns.FindAllString(values[0], -1), " ")
	for _, value := range values[1:] {
		if strings.Join(letterRuns.FindAllString(value, -1), " ") != first {
			return false
		}
	}
	return true
}

// wordChars are the characters of a word in a class, wordClass the same as
// a character class of a regular expression.
const (
	wordChars = "9aA+/_=-"
	wordClass = "[9aA+/_=-]"
)

// wordPattern is the pattern of words for classes, or "" when they do not
// have the same words and characters between them in the same order.
func wordPattern(classes []string) string {
	if len(classes) == 0 || classes[0] == "" {
		return ""
	}
	// A word is written as a run with the character 0.
	words := func(class string) []classRun {
		var runs []classRun
		for _, r := range class {
			if !strings.ContainsRune(wordChars, r) {
				runs = append(runs, classRun{r, 1})
			} else if n := len(runs); n > 0 && runs[n-1].char == 0 {
				runs[n-1].length++
			} else {
				runs = append(runs, classRun{0, 1})
			}
		}
		return runs
	}
	base := words(classes[0])
	varies := make([]bool, len(base))
	for _, class := range classes[1:] {
		runs := words(class)
		if len(runs) != len(base) {
			return ""
		}
		for i, run := range runs {
			if run.char != base[i].char {
				return ""
			}
			if run.length != base[i].length {
				varies[i] = true
			}
		}
	}
	var b strings.Builder
	b.WriteString("^")
	hasWord := false
	for i, run := range base {
		switch {
		case run.char != 0:
			b.WriteString(regexp.QuoteMeta(string(run.char)))
		case varies[i]:
			hasWord = true
			b.WriteString(wordClass + "+")
		default:
			hasWord = true
			fmt.Fprintf(&b, "%s{%d}", wordClass, run.length)
		}
	}
	if !hasWord {
		return ""
	}
	b.WriteString("$")
	return b.String()
}

// deriveMark works out what stays checked at a volatile path from the
// values the recordings have there: the format when all are strings, whole
// numbers when all are numbers. bodies are the decoded bodies of every
// recording of the answer, for every role that got the same status: the
// more samples, the less a format depends on chance.
func deriveMark(text string, bodies ...any) VolatileMark {
	mark := VolatileMark{Path: text}
	p, err := parsePath(text)
	if err != nil || (len(p) > 0 && p[len(p)-1].kind == stepLength) {
		return mark
	}
	var values []any
	for _, body := range bodies {
		values = append(values, collect(body, p)...)
	}
	if len(values) == 0 {
		return mark
	}
	var texts []string
	integers := 0
	for _, value := range values {
		switch v := value.(type) {
		case string:
			texts = append(texts, v)
		case json.Number:
			if isInteger(v) {
				integers++
			}
		}
	}
	switch {
	case len(texts) == len(values):
		mark.Format, mark.Pattern = deriveFormat(texts)
	case integers == len(values):
		mark.Integer = true
	}
	return mark
}

// collect lists the values at a path; [*] takes every element of an array.
func collect(value any, p path) []any {
	if len(p) == 0 {
		return []any{value}
	}
	switch s := p[0]; s.kind {
	case stepKey:
		if object, ok := value.(map[string]any); ok {
			if child, ok := object[s.key]; ok {
				return collect(child, p[1:])
			}
		}
	case stepIndex:
		if array, ok := value.([]any); ok && s.index < len(array) {
			return collect(array[s.index], p[1:])
		}
	case stepEvery:
		if array, ok := value.([]any); ok {
			var out []any
			for _, child := range array {
				out = append(out, collect(child, p[1:])...)
			}
			return out
		}
	}
	return nil
}

// formatProblem checks a volatile value against a mark. It returns what the
// mark expects and ok false when the value does not fit.
func formatProblem(mark *VolatileMark, value any) (expected string, ok bool) {
	switch v := value.(type) {
	case string:
		class := formatClass(withoutVersionSuffix(v))
		switch {
		case mark.Format != "":
			return "format " + mark.Format, class == mark.Format
		case mark.Pattern != "":
			re, err := regexp.Compile(mark.Pattern)
			if err != nil {
				return "a readable pattern in the golden (" + err.Error() + ")", false
			}
			return "format " + mark.Pattern, re.MatchString(class)
		}
	case json.Number:
		if mark.Integer {
			return "a whole number", isInteger(v)
		}
	}
	return "", true
}

// secretWords mark a key whose value a message must not show.
var secretWords = []string{"token", "secret", "password", "key", "cookie", "sid"}

// looksSecret reports whether a path has a key that names a secret.
func looksSecret(p path) bool {
	for _, s := range p {
		if s.kind != stepKey {
			continue
		}
		key := strings.ToLower(s.key)
		for _, word := range secretWords {
			if strings.Contains(key, word) {
				return true
			}
		}
	}
	return false
}

// showAt prints a value for a message about a path: the value itself, or
// only its type and class when the path names a secret.
func showAt(p path, value any) string {
	if !looksSecret(p) {
		if s, ok := value.(string); ok {
			return show(value) + " (" + clip(formatClass(s)) + ")"
		}
		return show(value)
	}
	if s, ok := value.(string); ok {
		return "a string of the class " + clip(formatClass(s))
	}
	return "a " + jsonType(value)
}
