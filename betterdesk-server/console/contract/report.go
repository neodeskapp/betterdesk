package contract

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// Report collects what a run did, for the summary at its end.
type Report struct {
	settings *Settings
	started  time.Time

	tallies map[string]*tally
	// handlers is who answered each matrix route or case: layer, then ID.
	handlers   map[string]map[string]string
	hasRouting bool
	findings   []string
	seen       map[string]bool
	problems   []string
	slow       []slowRequest

	// files, volatile and timeBound count what a recording wrote: golden
	// files, volatile paths in them, and paths marked volatile because
	// they hold a time close to now.
	files, volatile, timeBound int
	// frozen are the golden files a recording left as they are, because the
	// Go console answers their requests. removed counts the golden files a
	// first recording deleted: no route or case stands for them any more.
	frozen  map[string]bool
	removed int
	// recordedNew are the golden files of requests the Go console answers
	// that a recording with CONTRACT_RECORD_NEW=1 wrote, because they were
	// new.
	recordedNew map[string]bool

	// redact puts placeholders in place of secret values in a line of the
	// summary; nil leaves lines as they are.
	redact func(string) string
}

type tally struct{ passed, failed int }

type slowRequest struct {
	what string
	took time.Duration
}

// slowAfter is how long a request may take before the summary lists it.
const slowAfter = 3 * time.Second

func newReport(settings *Settings) *Report {
	return &Report{
		settings: settings,
		started:  time.Now(),
		tallies:  map[string]*tally{},
		handlers: map[string]map[string]string{},
		seen:     map[string]bool{},
		frozen:   map[string]bool{},

		recordedNew: map[string]bool{},
	}
}

// froze notes a golden file a recording left as it is.
func (r *Report) froze(file string) {
	r.frozen[file] = true
}

// count adds one checked (or recorded) answer to a group's tally.
func (r *Report) count(group string, passed bool) {
	t := r.tallies[group]
	if t == nil {
		t = &tally{}
		r.tallies[group] = t
	}
	if passed {
		t.passed++
	} else {
		t.failed++
	}
}

func (r *Report) answeredBy(layer, id, handler string) {
	if r.handlers[layer] == nil {
		r.handlers[layer] = map[string]string{}
	}
	r.handlers[layer][id] = handler
}

// finding notes something a person should look at: a request without an
// answer, a lost session. The same finding is listed once.
func (r *Report) finding(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	if !r.seen[text] {
		r.seen[text] = true
		r.findings = append(r.findings, text)
	}
}

// problem notes a request that two fresh instances answer differently in
// more than a value.
func (r *Report) problem(format string, args ...any) {
	r.problems = append(r.problems, fmt.Sprintf(format, args...))
}

func (r *Report) took(what string, d time.Duration) {
	if d >= slowAfter {
		r.slow = append(r.slow, slowRequest{what, d})
	}
}

// Print writes the summary: per group how many answers passed and failed,
// who answered, and the findings.
func (r *Report) Print(w io.Writer) {
	verb := "compared with"
	passed := "passed"
	switch r.settings.Record {
	case RecordFirst:
		verb, passed = "first recording into", "recorded"
	case RecordMerge:
		verb, passed = "second recording merged into", "recorded"
	}
	fmt.Fprintf(w, "\n== contract summary: %s, %s %s ==\n", r.settings.BaseURL, verb, r.settings.GoldenDir)

	groups := make([]string, 0, len(r.tallies))
	for group := range r.tallies {
		groups = append(groups, group)
	}
	sort.Strings(groups)
	var total tally
	for _, group := range groups {
		t := r.tallies[group]
		total.passed += t.passed
		total.failed += t.failed
		fmt.Fprintf(w, "  %-36s %6d %s %6d failed\n", group, t.passed, passed, t.failed)
	}
	fmt.Fprintf(w, "  %-36s %6d %s %6d failed   (%d groups, %s)\n", "total", total.passed, passed, total.failed, len(groups), time.Since(r.started).Round(time.Second))

	layers := make([]string, 0, len(r.handlers))
	for layer := range r.handlers {
		layers = append(layers, layer)
	}
	sort.Strings(layers)
	for _, layer := range layers {
		byHandler := map[string]int{}
		for _, handler := range r.handlers[layer] {
			byHandler[handler]++
		}
		what := "matrix routes"
		if layer == "cases" {
			what = "cases"
		}
		if !r.hasRouting {
			fmt.Fprintf(w, "  answered by Go: not known for the %d %s (the console has no routing report)\n", len(r.handlers[layer]), what)
			continue
		}
		fmt.Fprintf(w, "  answered by Go: %d of %d %s (node %d, nobody %d)\n",
			byHandler[HandlerGo], len(r.handlers[layer]), what, byHandler[HandlerNode], byHandler[HandlerNone])
	}

	if r.settings.Record != Compare {
		fmt.Fprintf(w, "  wrote %d golden files with %d volatile paths", r.files, r.volatile)
		if r.timeBound > 0 {
			fmt.Fprintf(w, " (%d of them equal in both recordings but bound to today's date)", r.timeBound)
		}
		fmt.Fprintln(w)
		fmt.Fprintf(w, "  frozen: %d (goldens of requests the Go console answers, left as they are)\n", len(r.frozen))
		if len(r.recordedNew) > 0 {
			fmt.Fprintf(w, "  recorded new: %d (new goldens of requests the Go console answers, CONTRACT_RECORD_NEW=1)\n", len(r.recordedNew))
		}
		if r.removed > 0 {
			fmt.Fprintf(w, "  removed %d golden files that no route or case stands for any more\n", r.removed)
		}
	}
	printList(w, "answers that differ between two fresh instances in more than a value", r.redacted(r.problems))
	printList(w, "findings", r.redacted(r.findings))
	if len(r.slow) > 0 {
		sort.SliceStable(r.slow, func(i, j int) bool { return r.slow[i].took > r.slow[j].took })
		lines := make([]string, 0, len(r.slow))
		for i, s := range r.slow {
			if i == 15 {
				lines = append(lines, fmt.Sprintf("... and %d more", len(r.slow)-i))
				break
			}
			lines = append(lines, fmt.Sprintf("%s took %s", s.what, s.took.Round(100*time.Millisecond)))
		}
		printList(w, fmt.Sprintf("requests slower than %s", slowAfter), lines)
	}
}

// redacted is lines with the secrets' values replaced.
func (r *Report) redacted(lines []string) []string {
	if r.redact == nil {
		return lines
	}
	out := make([]string, len(lines))
	for i, line := range lines {
		out[i] = r.redact(line)
	}
	return out
}

func printList(w io.Writer, title string, lines []string) {
	if len(lines) == 0 {
		return
	}
	fmt.Fprintf(w, "  %s (%d):\n", title, len(lines))
	for _, line := range lines {
		fmt.Fprintf(w, "    - %s\n", strings.ReplaceAll(line, "\n", "\n      "))
	}
}
