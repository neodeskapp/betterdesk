package contract

import (
	"encoding/json"
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// Diff is one difference between the answer a golden expects and the answer
// a console gave.
type Diff struct {
	// Where names the place: "status", "header content-type", "body data.id".
	Where string
	// What says what is wrong when Want and Got alone do not: "missing key".
	What string
	Want string
	Got  string
}

func (d Diff) String() string {
	switch {
	case d.What != "" && d.Want == "" && d.Got == "":
		return fmt.Sprintf("%s: %s", d.Where, d.What)
	case d.What != "":
		return fmt.Sprintf("%s: %s: expected %s, got %s", d.Where, d.What, d.Want, d.Got)
	}
	return fmt.Sprintf("%s: expected %s, got %s", d.Where, d.Want, d.Got)
}

// CompareAnswers lists how got differs from want. They are the same when:
// the status is equal, the headers on the allowlist are equal (a header in
// want.VolatileHeaders only has to be there), and the bodies are equal.
// JSON bodies are compared value by value: the same keys (a missing key is
// not a null), arrays in the same order, numbers by value. At a path in
// want.Volatile the value is free, but its JSON type has to match and, when
// the mark has one, its format (see VolatileMark). An array with a volatile
// length may have any number of elements: each is compared with the
// golden's sample element and the marks below the array when the length
// mark says so (VolatileMark.Elements), else it only has to be of the
// sample's type. An array marked AnyOrder must hold the same elements in
// any order.
func CompareAnswers(want GoldenAnswer, got Answer) []Diff {
	var diffs []Diff
	if want.Failed != got.Failed {
		diffs = append(diffs, Diff{Where: "answer", Want: describeFailed(want.Failed), Got: describeFailed(got.Failed)})
	}
	if want.Status != got.Status {
		diffs = append(diffs, Diff{Where: "status", Want: fmt.Sprint(want.Status), Got: fmt.Sprint(got.Status)})
	}
	if !equalStrings(want.Redirects, got.Redirects) {
		diffs = append(diffs, Diff{Where: "redirects", Want: quoteList(want.Redirects), Got: quoteList(got.Redirects)})
	}
	diffs = append(diffs, compareHeaders(want.Headers, got.Headers, want.VolatileHeaders)...)

	volatile, err := newVolatileSetOf(want.Volatile)
	if err != nil {
		return append(diffs, Diff{Where: "golden", What: "bad volatile path: " + err.Error()})
	}
	return append(diffs, compareBodies(want.Body, got.Body, volatile)...)
}

func describeFailed(failed string) string {
	if failed == "" {
		return "an answer"
	}
	return "no answer (" + failed + ")"
}

func compareHeaders(want, got map[string][]string, volatile []string) []Diff {
	skipValue := map[string]bool{}
	for _, name := range volatile {
		skipValue[strings.ToLower(name)] = true
	}
	names := map[string]bool{}
	for name := range want {
		names[name] = true
	}
	for name := range got {
		names[name] = true
	}
	sorted := make([]string, 0, len(names))
	for name := range names {
		sorted = append(sorted, name)
	}
	sort.Strings(sorted)

	var diffs []Diff
	for _, name := range sorted {
		w, hasWant := want[name]
		g, hasGot := got[name]
		where := "header " + name
		switch {
		case !hasGot:
			diffs = append(diffs, Diff{Where: where, What: "missing", Want: quoteList(w), Got: "no header"})
		case !hasWant:
			diffs = append(diffs, Diff{Where: where, What: "unexpected", Want: "no header", Got: quoteList(g)})
		case skipValue[name]:
		case !equalStrings(w, g):
			diffs = append(diffs, Diff{Where: where, Want: quoteList(w), Got: quoteList(g)})
		}
	}
	return diffs
}

func compareBodies(want, got Body, volatile *volatileSet) []Diff {
	if want.Kind != got.Kind {
		return []Diff{{Where: "body", What: "kind", Want: describeBody(want), Got: describeBody(got)}}
	}
	at := volatile.at()
	switch want.Kind {
	case BodyJSON:
		wantValue, err := decodeJSON(want.JSON)
		if err != nil {
			return []Diff{{Where: "golden", What: "body is not JSON: " + err.Error()}}
		}
		gotValue, err := decodeJSON(got.JSON)
		if err != nil {
			return []Diff{{Where: "body", What: "not JSON: " + err.Error()}}
		}
		var diffs []Diff
		compareJSON(path{}, wantValue, gotValue, at, &diffs)
		return diffs
	case BodyBytes:
		var diffs []Diff
		if want.Type != got.Type {
			diffs = append(diffs, Diff{Where: "body", What: "media type", Want: quote(want.Type), Got: quote(got.Type)})
		}
		if at.isValue() {
			return diffs
		}
		if want.Length != got.Length || want.SHA256 != got.SHA256 {
			d := Diff{
				Where: "body",
				What:  "content",
				Want:  fmt.Sprintf("%d bytes, sha256 %s", want.Length, short(want.SHA256)),
				Got:   fmt.Sprintf("%d bytes, sha256 %s", got.Length, short(got.SHA256)),
			}
			if want.Head != got.Head {
				d.Want += ", starting " + quote(want.Head)
				d.Got += ", starting " + quote(got.Head)
			}
			diffs = append(diffs, d)
		}
		return diffs
	}
	return nil
}

func describeBody(b Body) string {
	switch b.Kind {
	case BodyJSON:
		return "JSON " + clip(string(b.JSON))
	case BodyBytes:
		return fmt.Sprintf("%s, %d bytes", b.Type, b.Length)
	case BodyEmpty:
		return "no body"
	case BodyEncoded:
		return "a body in an encoding the suite cannot read"
	case BodyStream:
		return "an event stream"
	}
	return quote(b.Kind)
}

// compareJSON walks want and got together and adds what differs to diffs.
func compareJSON(p path, want, got any, at volatileAt, diffs *[]Diff) {
	where := "body " + p.String()
	wantType, gotType := jsonType(want), jsonType(got)
	if wantType != gotType {
		gotText := gotType
		if !looksSecret(p) {
			gotText += " " + show(got)
		}
		*diffs = append(*diffs, Diff{Where: where, What: "type", Want: wantType + " " + show(want), Got: gotText})
		return
	}
	if at.isValue() {
		for _, mark := range at.marks() {
			if expected, ok := formatProblem(mark, got); !ok {
				*diffs = append(*diffs, Diff{Where: where, What: "format of a volatile value", Want: expected, Got: showAt(p, got)})
				break
			}
		}
		return
	}
	if w, isArray := want.([]any); isArray && at.isAnyOrder() {
		compareAnyOrder(where, w, got.([]any), diffs)
		return
	}
	switch w := want.(type) {
	case map[string]any:
		g := got.(map[string]any)
		for _, key := range sortedKeys(w) {
			gotValue, ok := g[key]
			if !ok {
				if !at.key(key).isOptional() {
					*diffs = append(*diffs, Diff{Where: where, What: "missing key " + quote(key)})
				}
				continue
			}
			compareJSON(p.with(step{kind: stepKey, key: key}), w[key], gotValue, at.key(key), diffs)
		}
		for _, key := range sortedKeys(g) {
			if _, ok := w[key]; !ok && !at.key(key).isOptional() {
				*diffs = append(*diffs, Diff{Where: where, What: "unexpected key " + quote(key) + " with value " + show(g[key])})
			}
		}
	case []any:
		g := got.([]any)
		if at.isLength() {
			// Any number of elements; each has to be like the golden's
			// sample element, or of its type in an older golden.
			if len(w) == 0 {
				return
			}
			for i, element := range g {
				here := p.with(step{kind: stepIndex, index: i})
				if at.checksElements() {
					compareJSON(here, w[0], element, at.element(i), diffs)
				} else if jsonType(element) != jsonType(w[0]) {
					*diffs = append(*diffs, Diff{Where: "body " + here.String(), What: "type", Want: jsonType(w[0]), Got: jsonType(element) + " " + show(element)})
				}
			}
			return
		}
		if len(w) != len(g) {
			*diffs = append(*diffs, Diff{Where: where, What: "array length", Want: fmt.Sprint(len(w)), Got: fmt.Sprint(len(g))})
		}
		for i := 0; i < len(w) && i < len(g); i++ {
			compareJSON(p.with(step{kind: stepIndex, index: i}), w[i], g[i], at.element(i), diffs)
		}
	default:
		if !equalScalar(want, got) {
			*diffs = append(*diffs, Diff{Where: where, Want: show(want), Got: show(got)})
		}
	}
}

// compareAnyOrder compares two arrays as lists of elements in any order:
// every element of want must be in got as often, and nothing else.
// Elements are compared by their JSON text, keys sorted.
func compareAnyOrder(where string, want, got []any, diffs *[]Diff) {
	count := map[string]int{}
	for _, element := range want {
		count[string(encodeJSON(element))]++
	}
	var unexpected []string
	for _, element := range got {
		text := string(encodeJSON(element))
		if count[text] > 0 {
			count[text]--
			continue
		}
		unexpected = append(unexpected, text)
	}
	var missing []string
	for text, n := range count {
		for ; n > 0; n-- {
			missing = append(missing, text)
		}
	}
	sort.Strings(missing)
	sort.Strings(unexpected)
	for _, text := range missing {
		*diffs = append(*diffs, Diff{Where: where, What: "missing element (in any order) " + clip(text)})
	}
	for _, text := range unexpected {
		*diffs = append(*diffs, Diff{Where: where, What: "unexpected element (in any order) " + clip(text)})
	}
}

// sortedElements returns the elements of an array sorted by their JSON
// text, the order a golden keeps an array in any order in.
func sortedElements(array []any) []any {
	out := append([]any(nil), array...)
	sort.SliceStable(out, func(i, j int) bool {
		return string(encodeJSON(out[i])) < string(encodeJSON(out[j]))
	})
	return out
}

// equalScalar compares two values of the same JSON type that are not
// objects or arrays. Numbers are equal by value: 1, 1.0 and 1e0 are the same.
func equalScalar(a, b any) bool {
	an, aIsNumber := a.(json.Number)
	bn, bIsNumber := b.(json.Number)
	if aIsNumber && bIsNumber {
		if an == bn {
			return true
		}
		ar, okA := new(big.Rat).SetString(string(an))
		br, okB := new(big.Rat).SetString(string(bn))
		return okA && okB && ar.Cmp(br) == 0
	}
	return a == b
}

func sortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// clipAt is how much of a value a diff shows.
const clipAt = 240

func clip(s string) string {
	if len(s) <= clipAt {
		return s
	}
	return strings.ToValidUTF8(s[:clipAt], "") + fmt.Sprintf("... (%d bytes)", len(s))
}

func quote(s string) string {
	return clip(fmt.Sprintf("%q", s))
}

func quoteList(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = quote(v)
	}
	return strings.Join(quoted, ", ")
}

func short(hash string) string {
	if len(hash) > 12 {
		return hash[:12]
	}
	return hash
}

// show prints a JSON value for a diff, as JSON.
func show(value any) string {
	return clip(string(encodeJSON(value)))
}
