package devices_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
)

// The recordings in testdata (node_<db>.json, node_agent_<db>.json) hold
// what the Node console answered, and they stay as Node recorded them.
// Where a bug fix changes an answer on purpose, the step is listed here
// with the bug of BUGS.md, and the replays compare that step with the Go
// console's answer in testdata/<recording>_<db>_fixed.json instead of
// Node's.
//
// DEVICES_RECORD_FIXED=1 writes those files from the Go console's answers
// to the listed steps. Look at their diff before a commit: only what the
// fixes change may differ from Node's answer.
var fixedSteps = []fixedStep{
	// The hard deletes of A1001 and D1004 clear their system information
	// and metrics. They also clear A1001's folder and groups, so when the
	// recording brings A1001 back with a restore the made-up server
	// allows, it is outside the operator's scope.
	{"node", "264 cleared", "DE1"},
	{"node", `403 PATCH /api/devices/A1001/folder {"folderId":999} as operator`, "DE1"},
}

// fixedStep is a step of a recording that the fix of a bug changed. The
// recording is "node" or "node_agent" for the files of both databases, or
// one file ("node_sqlite"). A step is named by its index in the recording
// and its name ("257 DELETE /api/devices/A1001?hard=true {} as admin").
type fixedStep struct {
	recording, step, bug string
}

// stepKey names the step of a recording.
func stepKey(i int, name string) string { return strconv.Itoa(i) + " " + name }

// fixesOf lists the fixed steps of one recording file ("node_sqlite")
// with their bugs.
func fixesOf(file string) map[string]string {
	out := map[string]string{}
	for _, f := range fixedSteps {
		if f.recording == file || f.recording+"_sqlite" == file || f.recording+"_postgres" == file {
			out[f.step] = f.bug
		}
	}
	return out
}

// fixedAnswer is the Go console's answer to a fixed step: a request's
// status, body, calls to the API server and requests to the agent, or the
// rows of a query, each normalized.
type fixedAnswer struct {
	Status int      `json:"status,omitempty"`
	Answer string   `json:"answer,omitempty"`
	Calls  []string `json:"calls,omitempty"`
	Agent  []string `json:"agent,omitempty"`
	Rows   string   `json:"rows,omitempty"`
}

type fixedEntry struct {
	Bug    string      `json:"bug"`
	Answer fixedAnswer `json:"answer"`
}

func fixedFile(file string) string { return filepath.Join("testdata", file+"_fixed.json") }

func recordingFixed() bool { return os.Getenv("DEVICES_RECORD_FIXED") == "1" }

// loadFixed reads the answers of the fixed steps of a recording file. It
// fails when the file and the list above name other steps or bugs.
func loadFixed(t *testing.T, file string) map[string]fixedAnswer {
	t.Helper()
	entries := map[string]fixedEntry{}
	raw, err := os.ReadFile(fixedFile(file))
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read the fixed steps: %v", err)
	}
	if err == nil {
		if err := json.Unmarshal(raw, &entries); err != nil {
			t.Fatalf("%s: %v", fixedFile(file), err)
		}
	}
	listed := fixesOf(file)
	var problems []string
	for step, bug := range listed {
		if entry, ok := entries[step]; !ok || entry.Bug != bug {
			problems = append(problems, step+" ("+bug+") has no answer of that bug in "+fixedFile(file))
		}
	}
	for step := range entries {
		if _, ok := listed[step]; !ok {
			problems = append(problems, step+" is in "+fixedFile(file)+" but not in fixedSteps")
		}
	}
	sort.Strings(problems)
	for _, p := range problems {
		t.Errorf("%s", p)
	}
	if len(problems) > 0 {
		t.FailNow()
	}
	answers := map[string]fixedAnswer{}
	for step, entry := range entries {
		answers[step] = entry.Answer
	}
	return answers
}

// writeFixed writes the Go console's answers to the fixed steps of a
// recording file. answers holds every listed step.
func writeFixed(t *testing.T, file string, answers map[string]fixedAnswer) {
	t.Helper()
	entries := map[string]fixedEntry{}
	for step, bug := range fixesOf(file) {
		answer, ok := answers[step]
		if !ok {
			t.Fatalf("the fixed step %q was not answered", step)
		}
		entries[step] = fixedEntry{Bug: bug, Answer: answer}
	}
	if len(entries) == 0 {
		if err := os.Remove(fixedFile(file)); err != nil && !os.IsNotExist(err) {
			t.Fatalf("%v", err)
		}
		return
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(entries); err != nil {
		t.Fatalf("encode the fixed steps: %v", err)
	}
	if err := os.WriteFile(fixedFile(file), out.Bytes(), 0o644); err != nil {
		t.Fatalf("write the fixed steps: %v", err)
	}
	t.Logf("wrote %d fixed steps to %s", len(entries), fixedFile(file))
}

// checkListed fails for a listed step the recording does not have.
func checkListed(t *testing.T, file string, names []string) {
	t.Helper()
	have := map[string]bool{}
	for i, n := range names {
		have[stepKey(i, n)] = true
	}
	for step := range fixesOf(file) {
		if !have[step] {
			t.Errorf("the fixed step %q is not in the recording %s", step, file)
		}
	}
}

// normalizeAll is normalize of each text.
func normalizeAll(texts []string) []string {
	var out []string
	for _, s := range texts {
		out = append(out, normalize(s))
	}
	return out
}
