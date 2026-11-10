package contract

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Volatile values are found by recording the same request against two fresh
// instances. Where the two answers hold different values of the same JSON
// type, that path is volatile: a comparison checks the type there and the
// format the two values share (format.go), not the value. A difference in
// status, headers, JSON type or key set is
// not volatile but a problem: the console answers differently for a reason
// somebody has to look at.

// detection is what comparing two recordings of one answer found.
type detection struct {
	// Volatile are the paths that differ in value, as goldens write them.
	Volatile []string
	// Problems are the differences that must not be there.
	Problems []string
	// TimeBound counts the paths that were equal in both recordings but
	// hold a time close to now (see timeBound), so they are volatile too.
	TimeBound int
}

// detectVolatile compares two recordings of the same request. known and
// knownHeaders are the paths and headers already marked volatile; nothing
// is reported there. now is the time of the recording.
func detectVolatile(first, second Answer, known, knownHeaders []string, now time.Time) detection {
	var d detection
	if first.Failed != second.Failed {
		d.Problems = append(d.Problems, fmt.Sprintf("answer: %s in the first recording, %s in the second", describeFailed(first.Failed), describeFailed(second.Failed)))
	}
	if first.Status != second.Status {
		d.Problems = append(d.Problems, fmt.Sprintf("status: %d in the first recording, %d in the second", first.Status, second.Status))
	}
	if !equalStrings(first.Redirects, second.Redirects) {
		d.Problems = append(d.Problems, fmt.Sprintf("redirects: %s in the first recording, %s in the second", quoteList(first.Redirects), quoteList(second.Redirects)))
	}
	for _, diff := range compareHeaders(first.Headers, second.Headers, knownHeaders) {
		d.Problems = append(d.Problems, strings.NewReplacer("expected", "first recording", "got", "second recording").Replace(diff.String()))
	}

	set, err := newVolatileSet(known)
	if err != nil {
		d.Problems = append(d.Problems, "bad volatile path: "+err.Error())
		return d
	}
	if first.Body.Kind != second.Body.Kind {
		d.Problems = append(d.Problems, fmt.Sprintf("body: %s in the first recording, %s in the second", describeBody(first.Body), describeBody(second.Body)))
		return d
	}
	switch first.Body.Kind {
	case BodyJSON:
		a, errA := decodeJSON(first.Body.JSON)
		b, errB := decodeJSON(second.Body.JSON)
		if errA != nil || errB != nil {
			d.Problems = append(d.Problems, "body: a recording does not hold JSON")
			return d
		}
		walk := walker{now: now, found: &d}
		for _, p := range walk.compare(path{}, a, b, set.at()) {
			d.Volatile = append(d.Volatile, p.String())
		}
		sort.Strings(d.Volatile)
	case BodyBytes:
		if first.Body.Type != second.Body.Type {
			d.Problems = append(d.Problems, fmt.Sprintf("body: media type %q in the first recording, %q in the second", first.Body.Type, second.Body.Type))
			return d
		}
		if !set.at().isValue() && (first.Body.Length != second.Body.Length || first.Body.SHA256 != second.Body.SHA256) {
			d.Volatile = []string{rootPath}
		}
	}
	return d
}

type walker struct {
	now   time.Time
	found *detection
}

func (w walker) problem(here path, format string, args ...any) {
	w.found.Problems = append(w.found.Problems, "body "+here.String()+": "+fmt.Sprintf(format, args...))
}

// compare walks two recordings of a JSON value. It returns the volatile
// paths below here, written from here: an empty path is the value itself.
func (w walker) compare(here path, a, b any, at volatileAt) []path {
	if at.isValue() {
		return nil
	}
	typeA, typeB := jsonType(a), jsonType(b)
	if typeA != typeB {
		w.problem(here, "%s %s in the first recording, %s %s in the second", typeA, show(a), typeB, show(b))
		return nil
	}
	switch va := a.(type) {
	case map[string]any:
		vb := b.(map[string]any)
		var onlyA, onlyB []string
		var found []path
		for _, key := range sortedKeys(va) {
			other, ok := vb[key]
			if !ok {
				onlyA = append(onlyA, key)
				continue
			}
			s := step{kind: stepKey, key: key}
			for _, sub := range w.compare(here.with(s), va[key], other, at.key(key)) {
				found = append(found, append(path{s}, sub...))
			}
		}
		for _, key := range sortedKeys(vb) {
			if _, ok := va[key]; !ok {
				onlyB = append(onlyB, key)
			}
		}
		if len(onlyA) > 0 || len(onlyB) > 0 {
			w.problem(here, "keys differ: only in the first recording %s, only in the second %s", quoteList(onlyA), quoteList(onlyB))
		}
		return found
	case []any:
		vb := b.([]any)
		if at.isLength() {
			return nil
		}
		if len(va) != len(vb) {
			return []path{{step{kind: stepLength}}}
		}
		// What every element has volatile becomes [*], the rest [index].
		perElement := make([][]path, len(va))
		count := map[string]int{}
		for i := range va {
			perElement[i] = w.compare(here.with(step{kind: stepIndex, index: i}), va[i], vb[i], at.element(i))
			for _, sub := range perElement[i] {
				count[sub.String()]++
			}
		}
		var found []path
		emitted := map[string]bool{}
		for i, subs := range perElement {
			for _, sub := range subs {
				key := sub.String()
				if count[key] == len(va) {
					if !emitted[key] {
						emitted[key] = true
						found = append(found, append(path{step{kind: stepEvery}}, sub...))
					}
					continue
				}
				found = append(found, append(path{step{kind: stepIndex, index: i}}, sub...))
			}
		}
		return found
	}
	if !equalScalar(a, b) {
		return []path{{}}
	}
	if timeBound(a, w.now) {
		w.found.TimeBound++
		return []path{{}}
	}
	return nil
}

// timeWindow is how far from now a value may be to count as bound to the
// time of the recording.
const timeWindow = 45 * 24 * time.Hour

var (
	isoDate   = regexp.MustCompile(`(?:^|[^0-9])(\d{4})-(\d{2})-(\d{2})(?:[^0-9]|$)`)
	wordyDate = regexp.MustCompile(`\b(?:(\d{1,2}) (Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)[a-z]*\.?,? (\d{4})|(Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)[a-z]*\.? (\d{1,2}),? (\d{4}))\b`)
)

// timeBound reports whether a value that was the same in two recordings
// still depends on when it was recorded: a string with a date within 45
// days of now, or a whole number that is a Unix time (seconds or
// milliseconds) in that range. Two instances recorded minutes apart agree
// on "2026-11-10" and on the start of today; a console checked tomorrow
// does not.
func timeBound(value any, now time.Time) bool {
	near := func(t time.Time) bool {
		d := t.Sub(now)
		return d > -timeWindow && d < timeWindow
	}
	switch v := value.(type) {
	case string:
		for _, m := range isoDate.FindAllStringSubmatch(v, -1) {
			if t, err := time.Parse("2006-01-02", m[1]+"-"+m[2]+"-"+m[3]); err == nil && near(t) {
				return true
			}
		}
		for _, m := range wordyDate.FindAllStringSubmatch(v, -1) {
			day, month, year := m[1], m[2], m[3]
			if month == "" {
				month, day, year = m[4], m[5], m[6]
			}
			if t, err := time.Parse("2 Jan 2006", day+" "+month+" "+year); err == nil && near(t) {
				return true
			}
		}
	case json.Number:
		n, err := strconv.ParseInt(string(v), 10, 64)
		if err != nil {
			return false
		}
		return near(time.Unix(n, 0)) || near(time.UnixMilli(n))
	}
	return false
}

// maskVolatile returns the answer with every volatile value replaced by a
// placeholder of its type, so a golden holds nothing that belongs to one
// instance and recording again changes as little as possible: strings
// become "<volatile>", numbers 0, booleans false, objects {} and arrays [].
// An array with a volatile length keeps one emptied element as the sample
// of its element type. A volatile body that is not JSON loses length, hash
// and head.
func maskVolatile(a Answer, volatile []string) (Answer, error) {
	set, err := newVolatileSet(volatile)
	if err != nil {
		return a, err
	}
	switch a.Body.Kind {
	case BodyJSON:
		value, err := decodeJSON(a.Body.JSON)
		if err != nil {
			return a, fmt.Errorf("body is not JSON: %w", err)
		}
		a.Body.JSON = encodeJSON(maskJSON(value, set.at()))
	case BodyBytes:
		if set.at().isValue() {
			a.Body.Length, a.Body.SHA256, a.Body.Head = 0, "", ""
		}
	}
	return a, nil
}

func maskJSON(value any, at volatileAt) any {
	if len(at) == 0 {
		return value
	}
	if at.isValue() {
		return placeholder(value)
	}
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			v[key] = maskJSON(child, at.key(key))
		}
	case []any:
		if at.isLength() {
			if len(v) == 0 {
				return v
			}
			return []any{placeholder(v[0])}
		}
		for i, child := range v {
			v[i] = maskJSON(child, at.element(i))
		}
	}
	return value
}

// volatilePlaceholder stands in a golden where a volatile string was.
const volatilePlaceholder = "<volatile>"

func placeholder(value any) any {
	switch value.(type) {
	case string:
		return volatilePlaceholder
	case json.Number, float64:
		return json.Number("0")
	case bool:
		return false
	case map[string]any:
		return map[string]any{}
	case []any:
		return []any{}
	}
	return value
}
