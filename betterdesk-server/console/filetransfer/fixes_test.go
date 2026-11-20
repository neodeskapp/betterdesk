package filetransfer

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The bug fixes of BUGS.md that change what the vectors recorded from
// the Node service. A fix that changes every step alike rewrites Node's
// answers (rewrites); the others name the steps they change (stepFixes),
// by their number counted over all scenarios from 0, and
// testdata/vectors_fixed.json.gz holds what the console answers there. A
// step a fix changes also changes the records and files the steps after
// it see, until a boot starts the service over, so those steps are named
// too. A step that several fixes change stays with the first of them.

// stepFixes are the steps each fix changed, as spans ("12-15,40").
var stepFixes = []struct{ bug, steps string }{
	// A device's chunk and cancel reach only its own transfers
	{"FT2", "301-332,376-438,455-466,475-539,557-565,583-601,611-737,744-839,857-908,941-1017,1064-1369,1394-1530,1539-1772,1796-2136,2174-2179,2197-2345,2387-2443,2453-2551,2579,2596,2609-2773,2776-2812,2854-2913,2922-3092,3104-3139,3179-3355,3374-3427,3451-3500,3519-3648,3681-3685,3694-3918,3952-4001,4016,4018-4023,4029-4203,4219-4305,4360-4519,4526-4607,4629,4676-4770,4794-4797,4801-4826,4830-5013,5066-5211,5237-5284"},
	// A chunk takes no more than MaxChunkSize bytes and no byte beyond the
	// size, and a completed transfer does not start over
	{"FT3", "130-300,372-375,854-856,1058-1063,2153-2173,2585-2595,2597-2608,2845-2853,4010-4015,4017,4211-4218,4635-4643,4798-4800,5054-5065,5229-5230"},
}

// rewrite is a fix that changes Node's answer of every step alike. It
// reports whether it changed the step.
type rewrite struct {
	bug    string
	change func(step vectorStep, want *answer) bool
}

// globalRewrites are the fixes that change every step alike.
var globalRewrites = []func() rewrite{leftoversGoAtBoot}

// leftoversGoAtBoot: a service that starts removes the files of the
// transfers before it from the folder (FT1). Node's folder keeps them,
// from the boot on.
func leftoversGoAtBoot() rewrite {
	gone := map[string]bool{}
	return rewrite{bug: "FT1", change: func(step vectorStep, want *answer) bool {
		if step.Op == "boot" && want.dir != nil {
			for _, entry := range *want.dir {
				if entry[1] != "dir" && transferFile.MatchString(entry[0]) {
					gone[entry[0]] = true
				}
			}
		}
		if want.dir == nil || len(gone) == 0 {
			return false
		}
		kept := [][]string{}
		for _, entry := range *want.dir {
			if !gone[entry[0]] {
				kept = append(kept, entry)
			}
		}
		if len(kept) == len(*want.dir) {
			return false
		}
		want.dir = &kept
		return true
	}}
}

// answer is what a step gave: the result or the error, the records and
// the transfers folder.
type answer struct {
	out, err *string
	all      string
	dir      *[][]string
}

func answerOf(w *oracleWorld, out *string, err error) answer {
	a := answer{out: out, all: describe(w.text(w.svc.ListTransfers(Filter{}))), dir: w.dirState()}
	if err != nil {
		text := w.mask(err.Error())
		a.out, a.err = nil, &text
	}
	return a
}

func nodeAnswer(step vectorStep) answer {
	a := answer{out: step.Out, err: step.Err, all: step.All, dir: step.Dir}
	if a.err != nil {
		a.out = nil
	}
	return a
}

// differences lists how a differs from want.
func (a answer) differences(want answer) []string {
	var problems []string
	problem := func(what, got, want string) {
		problems = append(problems, what+":\n      go: "+got+"\n    want: "+want)
	}
	switch {
	case want.err != nil && a.err == nil:
		problem("error", "none, result "+describe(a.out), *want.err)
	case want.err == nil && a.err != nil:
		problem("error", *a.err, "none, result "+describe(want.out))
	case want.err != nil:
		if *a.err != *want.err {
			problem("error", *a.err, *want.err)
		}
	case describe(a.out) != describe(want.out):
		problem("result", describe(a.out), describe(want.out))
	}
	if a.all != want.all {
		problem("records", a.all, want.all)
	}
	if got, want := describeDir(a.dir), describeDir(want.dir); got != want {
		problem("transfers folder", got, want)
	}
	return problems
}

// rewrites applies the global rewrites and counts the steps each changed.
type rewrites struct {
	list    []rewrite
	changed map[string]int
}

func newRewrites() *rewrites {
	r := &rewrites{changed: map[string]int{}}
	for _, build := range globalRewrites {
		r.list = append(r.list, build())
	}
	return r
}

func (r *rewrites) apply(step vectorStep) answer {
	want := nodeAnswer(step)
	for _, fix := range r.list {
		if fix.change(step, &want) {
			r.changed[fix.bug]++
		}
	}
	return want
}

func (r *rewrites) check(t *testing.T) {
	t.Helper()
	for _, fix := range r.list {
		if r.changed[fix.bug] == 0 {
			t.Errorf("the fix of %s changes no step", fix.bug)
		}
		t.Logf("the fix of %s changes %d steps", fix.bug, r.changed[fix.bug])
	}
}

// listedSteps are the steps of stepFixes with their bug.
func listedSteps(t *testing.T) map[int]string {
	t.Helper()
	listed := map[int]string{}
	for _, fix := range stepFixes {
		for _, part := range strings.Split(fix.steps, ",") {
			from, to, isSpan := strings.Cut(part, "-")
			if !isSpan {
				to = from
			}
			first, err1 := strconv.Atoi(from)
			last, err2 := strconv.Atoi(to)
			if err1 != nil || err2 != nil || last < first {
				t.Fatalf("the steps %q of %s are no span", part, fix.bug)
			}
			for n := first; n <= last; n++ {
				if other, twice := listed[n]; twice {
					t.Fatalf("step %d is listed for %s and %s", n, other, fix.bug)
				}
				listed[n] = fix.bug
			}
		}
	}
	return listed
}

// spans writes numbers in order as spans: "3-5,9".
func spans(numbers []int) string {
	sort.Ints(numbers)
	var parts []string
	for i := 0; i < len(numbers); {
		j := i
		for j+1 < len(numbers) && numbers[j+1] == numbers[j]+1 {
			j++
		}
		part := strconv.Itoa(numbers[i])
		if j > i {
			part += "-" + strconv.Itoa(numbers[j])
		}
		parts = append(parts, part)
		i = j + 1
	}
	return strings.Join(parts, ",")
}

// fixedStep is the console's answer to a listed step, in the parts that
// differ from Node's: the result or error, the records that differ (null
// for one the console does not have), the files that differ (null for one
// that is not there). All and Dir hold the whole list or folder where
// that cannot be said record by record or file by file.
type fixedStep struct {
	Step     int                `json:"step"`
	Bug      string             `json:"bug"`
	Scenario string             `json:"scenario"`
	Op       string             `json:"op"`
	Outcome  *outcome           `json:"outcome,omitempty"`
	Records  map[string]*string `json:"records,omitempty"`
	All      *string            `json:"all,omitempty"`
	Files    map[string]*string `json:"files,omitempty"`
	Dir      json.RawMessage    `json:"dir,omitempty"`
}

type outcome struct {
	Out *string `json:"out"`
	Err *string `json:"err"`
}

const fixedFile = "vectors_fixed.json.gz"

// fixedFrom is the entry for a listed step, and whether the step differs
// from Node's answer at all.
func fixedFrom(t *testing.T, got, want answer) (fixedStep, bool) {
	t.Helper()
	var entry fixedStep
	differs := false
	if describe(got.out) != describe(want.out) || describe(got.err) != describe(want.err) {
		entry.Outcome, differs = &outcome{Out: got.out, Err: got.err}, true
	}
	if got.all != want.all {
		differs = true
		entry.Records = recordPatch(t, got.all, want.all)
		if patched, ok := patchRecords(want.all, entry.Records); !ok || patched != got.all {
			entry.Records, entry.All = nil, &got.all
		}
	}
	if describeDir(got.dir) != describeDir(want.dir) {
		differs = true
		if got.dir != nil && want.dir != nil {
			entry.Files = filePatch(*got.dir, *want.dir)
		}
		if entry.Files == nil || describeDir(patchFiles(want.dir, entry.Files)) != describeDir(got.dir) {
			entry.Files = nil
			entry.Dir, _ = json.Marshal(got.dir)
		}
	}
	return entry, differs
}

// onto is Node's answer with the entry's parts in place.
func (e fixedStep) onto(t *testing.T, want answer) answer {
	t.Helper()
	if e.Outcome != nil {
		want.out, want.err = e.Outcome.Out, e.Outcome.Err
	}
	switch {
	case e.All != nil:
		want.all = *e.All
	case e.Records != nil:
		patched, ok := patchRecords(want.all, e.Records)
		if !ok {
			t.Fatalf("step %d: the records of %s do not fit Node's list", e.Step, fixedFile)
		}
		want.all = patched
	}
	switch {
	case e.Dir != nil:
		var dir *[][]string
		if err := json.Unmarshal(e.Dir, &dir); err != nil {
			t.Fatalf("step %d: the folder of %s: %v", e.Step, fixedFile, err)
		}
		want.dir = dir
	case e.Files != nil:
		want.dir = patchFiles(want.dir, e.Files)
	}
	return want
}

// records splits a list of records into the records and their ids.
func records(all string) ([]json.RawMessage, []string, bool) {
	var list []json.RawMessage
	if json.Unmarshal([]byte(all), &list) != nil {
		return nil, nil, false
	}
	ids := make([]string, len(list))
	for i, record := range list {
		var head struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(record, &head) != nil {
			return nil, nil, false
		}
		ids[i] = head.ID
	}
	return list, ids, true
}

// recordPatch is the records of got that differ from want's, and null for
// a record of want that got does not have.
func recordPatch(t *testing.T, got, want string) map[string]*string {
	t.Helper()
	ours, ourIDs, ok1 := records(got)
	theirs, theirIDs, ok2 := records(want)
	if !ok1 || !ok2 {
		return nil
	}
	patch := map[string]*string{}
	have := map[string]json.RawMessage{}
	for i, id := range ourIDs {
		have[id] = ours[i]
	}
	for i, id := range theirIDs {
		record, kept := have[id]
		switch {
		case !kept:
			patch[id] = nil
		case !bytes.Equal(record, theirs[i]):
			text := string(record)
			patch[id] = &text
		}
	}
	return patch
}

// patchRecords is want's list with the records of the patch in place.
func patchRecords(want string, patch map[string]*string) (string, bool) {
	list, ids, ok := records(want)
	if !ok {
		return "", false
	}
	var kept []string
	for i, id := range ids {
		record, patched := patch[id]
		switch {
		case !patched:
			kept = append(kept, string(list[i]))
		case record != nil:
			kept = append(kept, *record)
		}
	}
	return "[" + strings.Join(kept, ",") + "]", true
}

// filePatch is the files of got that differ from want's, and null for a
// file of want that got does not have.
func filePatch(got, want [][]string) map[string]*string {
	patch := map[string]*string{}
	have := map[string]string{}
	for _, entry := range got {
		have[entry[0]] = entry[1]
	}
	seen := map[string]bool{}
	for _, entry := range want {
		seen[entry[0]] = true
		hash, kept := have[entry[0]]
		switch {
		case !kept:
			patch[entry[0]] = nil
		case hash != entry[1]:
			patch[entry[0]] = &hash
		}
	}
	for name, hash := range have {
		if !seen[name] {
			patch[name] = &hash
		}
	}
	return patch
}

// patchFiles is want's folder with the files of the patch in place.
func patchFiles(want *[][]string, patch map[string]*string) *[][]string {
	if want == nil {
		return nil
	}
	dir := [][]string{}
	seen := map[string]bool{}
	for _, entry := range *want {
		seen[entry[0]] = true
		hash, patched := patch[entry[0]]
		switch {
		case !patched:
			dir = append(dir, entry)
		case hash != nil:
			dir = append(dir, []string{entry[0], *hash})
		}
	}
	for name, hash := range patch {
		if !seen[name] && hash != nil {
			dir = append(dir, []string{name, *hash})
		}
	}
	sort.Slice(dir, func(i, j int) bool { return dir[i][0] < dir[j][0] })
	return &dir
}

// loadFixedSteps reads the console's answers to the listed steps and
// stops the test when the file and the list name other steps or bugs.
func loadFixedSteps(t *testing.T, listed map[int]string) map[int]fixedStep {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", fixedFile))
	if os.IsNotExist(err) && len(listed) == 0 {
		return map[int]fixedStep{}
	}
	if err != nil {
		t.Fatalf("read the fixed steps: %v", err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("read %s: %v", fixedFile, err)
	}
	var entries []fixedStep
	if err := json.NewDecoder(zr).Decode(&entries); err != nil {
		t.Fatalf("parse %s: %v", fixedFile, err)
	}
	fixed := map[int]fixedStep{}
	var problems []string
	for _, entry := range entries {
		if bug, ok := listed[entry.Step]; !ok || bug != entry.Bug {
			problems = append(problems, "step "+strconv.Itoa(entry.Step)+" of "+entry.Bug+" is in "+fixedFile+" but listed for "+strconv.Quote(listed[entry.Step]))
		}
		fixed[entry.Step] = entry
	}
	for step, bug := range listed {
		if _, ok := fixed[step]; !ok {
			problems = append(problems, "step "+strconv.Itoa(step)+" ("+bug+") has no answer in "+fixedFile)
		}
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		if len(problems) > 20 {
			problems = append(problems[:20], strconv.Itoa(len(problems)-20)+" more")
		}
		t.Fatalf("%s", strings.Join(problems, "\n"))
	}
	return fixed
}

// writeFixedSteps writes the console's answers to the listed steps, or
// removes the file when there are none.
func writeFixedSteps(t *testing.T, entries []fixedStep) {
	t.Helper()
	path := filepath.Join("testdata", fixedFile)
	if len(entries) == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return
	}
	var out bytes.Buffer
	zw := gzip.NewWriter(&out)
	enc := json.NewEncoder(zw)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(entries); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("wrote %d fixed steps to %s", len(entries), path)
}
