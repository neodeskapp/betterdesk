package replay

import (
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
)

// Fix is a recorded request whose answer a bug fix changed on purpose: the
// Go console answers Status and Answer where Node answered what the
// recording holds. Where Node's request left rows that later steps read,
// After brings about the same rows by other means (the request as a client
// sends it, or the rows themselves), so that the steps after it still
// compare with Node's answers.
type Fix struct {
	Bug    string
	Status int
	Answer string
	After  func(t *testing.T, w *World, step Step)
}

// FixKey names a recorded request: role, method, path and body.
func FixKey(step Step) string {
	body := ""
	if step.Body != nil {
		body = *step.Body
	}
	return strings.Join([]string{step.Role, step.Method, step.Path, body}, " ")
}

// Fixes holds the steps of a recording that the bug fixes change, by
// FixKey: a package's list of intended differences.
type Fixes struct {
	steps map[string]Fix
	used  map[string]int
}

// Add puts the steps of a bug fix on the list and returns true, so a
// package can fill its list in var declarations.
func (l *Fixes) Add(bug string, steps map[string]Fix) bool {
	if l.steps == nil {
		l.steps = map[string]Fix{}
	}
	for key, f := range steps {
		if _, twice := l.steps[key]; twice {
			panic("two fixes for " + key)
		}
		f.Bug = bug
		l.steps[key] = f
	}
	return true
}

// Check is a Check for World.Run that compares the steps on the list with
// the answers their bug fixes give, and leaves every other step to next
// (nil: to Run). Call Unused when the run is over.
func (l *Fixes) Check(w *World, next Check) Check {
	l.used = map[string]int{}
	return func(t *testing.T, step Step, got Answer) bool {
		t.Helper()
		key := FixKey(step)
		f, listed := l.steps[key]
		if !listed {
			return next != nil && next(t, step, got)
		}
		l.used[key]++
		if got.Status != f.Status || Normalize(got.Body) != Normalize(f.Answer) {
			t.Errorf("%s (%s): %s\n   fix: %d %s\n    go: %d %s", step.Name, f.Bug, key, f.Status, f.Answer, got.Status, got.Body)
		}
		if f.After != nil {
			f.After(t, w, step)
		}
		return true
	}
}

// Unused fails for the entries of the list that no step of the run named,
// so the list holds only steps there are.
func (l *Fixes) Unused(t *testing.T) {
	t.Helper()
	for key, f := range l.steps {
		if l.used[key] == 0 {
			t.Errorf("the fix of %s names no step of the recording: %s", f.Bug, key)
		}
	}
}

// Resend is a Fix.After that sends the step's request again with another
// body, which has to get Node's answer to the recorded one.
func Resend(body string) func(t *testing.T, w *World, step Step) {
	return ResendBy(body, body)
}

// ResendBy is Resend with a body per database, for a value Node stored
// differently on each; "" sends nothing on that database, where Node's
// request stored nothing.
func ResendBy(sqlite, postgres string) func(t *testing.T, w *World, step Step) {
	return func(t *testing.T, w *World, step Step) {
		t.Helper()
		body := w.DB.Pick(sqlite, postgres)
		if body == "" {
			return
		}
		got := w.Do(Request{Role: step.Role, Method: step.Method, Path: step.Path, Body: Text(body), Headers: step.Headers})
		if got.Status != step.Status || Normalize(got.Body) != Normalize(step.Answer) {
			t.Errorf("%s again as %s: %d %s, want Node's %d %s", step.Name, body, got.Status, got.Body, step.Status, step.Answer)
		}
	}
}

// Write is a Fix.After that runs statements writing what Node's request
// wrote; <now> in them is the database's clock.
func Write(queries ...string) func(t *testing.T, w *World, step Step) {
	return func(t *testing.T, w *World, step Step) {
		t.Helper()
		for _, query := range queries {
			w.Exec(strings.ReplaceAll(query, "<now>", store.NowSQL(w.DB.Dialect())), nil)
		}
	}
}
