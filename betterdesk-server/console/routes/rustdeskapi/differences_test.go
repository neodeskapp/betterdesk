package rustdeskapi_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The recordings in testdata (node_<db>.json, flow_<db>.json) hold what the
// Node console answered, and they stay as Node recorded them. Where a bug
// fix changes an answer on purpose, the step is listed here with the bug
// of BUGS.md, and the tests compare that step with the Go console's answer
// in testdata/<recording>_<db>_fixed.json instead of Node's.
//
// RDAPI_RECORD_FIXED=1 writes those files from the Go console's answers to
// the listed steps (for the flow, on a fresh instance as for a recording).
// Look at their diff before a commit: only what the fixes change may
// differ from Node's answer.
var fixedSteps = []fixedStep{
	// A wrong code is refused and keeps the secret; the right code then
	// signs in with it. The wrong code counts as a failed sign-in, so the
	// ninth wrong password locks the account and the tenth is refused.
	{"flow", "tfa-wrong-code", "S5"},
	{"flow", "tfa-current-user", "S5"},
	{"flow", "tfa-secret-used", "S5"},
	{"flow", "wrong-password-10", "S5"},
	{"flow", "audit-entries", "S5"},
	// The version of the system information hbok1 uploaded, not the
	// empty text.
	{"node", "396 POST /api/sysinfo_ver", "RD1"},
	// The switch of a strategy is read from {"enabled": <boolean>}; any
	// other body is refused before the strategy is looked up, so it stays
	// switched on.
	{"node", "666 PUT /api/strategies/s-fixed-1/status", "RD2"},
	{"node", "667 PUT /api/strategies/nope/status", "RD2"},
	{"node_sqlite", "668 GET /api/strategies", "RD2"},
	{"flow", "admin-strategy-status", "RD2"},
	{"flow", "admin-strategy-status-unknown", "RD2"},
	// The list reads a stored true as switched on.
	{"node_postgres", "511 GET /api/strategies", "RD3"},
	{"node_postgres", "519 GET /api/strategies", "RD3"},
	{"node_postgres", "527 GET /api/strategies", "RD3"},
	{"node_postgres", "668 GET /api/strategies", "RD3"},
	{"flow_postgres", "admin-strategies", "RD3"},
	// A page of the device list holds at most pageSize devices (the
	// PostgreSQL instance has none in the console's table).
	{"flow_sqlite", "viewer-devices", "RD4"},
	{"flow_sqlite", "admin-devices-page", "RD4"},
	// A client id or UUID that is no text counts as missing: the sign-in
	// stores the empty text in its place and gives the token, its audit
	// entry names the client "unknown", and such a UUID revokes nothing.
	{"node_sqlite", "462 POST /api/login", "RD7"},
	{"node_sqlite", "463 POST /api/login", "RD7"},
	{"node_sqlite", "465 POST /api/login", "RD7"},
	{"node_sqlite", "466 POST /api/login", "RD7"},
	{"node_sqlite", "467 POST /api/login", "RD7"},
	{"node_sqlite", "468 POST /api/login", "RD7"},
	{"node", "469 POST /api/login", "RD7"},
	{"node", "the tokens", "RD7"},
	{"node", "the tokens after sign-out", "RD7"},
	// The audit entry of a sign-in from a banned device names the account
	// in full: only a failed sign-in's "Username: " entry is shortened.
	{"node", "the audit entries", "RD7, AL1"},
	// The accessible route gives the short form in any letter case.
	{"node", "609 GET /API/Device-Group/Accessible", "RD11"},
}

// fixedStep is a step of the recordings of both databases ("node",
// "flow") or of one ("node_postgres") that the fix of a bug changed.
type fixedStep struct {
	recording, step, bug string
}

// fixesOf lists the fixed steps of one recording file ("flow_sqlite")
// with their bugs.
func fixesOf(file string) map[string]string {
	out := map[string]string{}
	for _, f := range fixedSteps {
		if f.recording == file || strings.HasPrefix(file, f.recording+"_") {
			out[f.step] = f.bug
		}
	}
	return out
}

// fixedEntry is the Go console's answer to a fixed step, in the form of
// the recording it replaces.
type fixedEntry struct {
	Bug    string          `json:"bug"`
	Answer json.RawMessage `json:"answer"`
}

func fixedFile(file string) string { return filepath.Join("testdata", file+"_fixed.json") }

func recordingFixed() bool { return os.Getenv("RDAPI_RECORD_FIXED") == "1" }

// loadFixed reads the answers of the fixed steps of a recording file. It
// fails when the file and the list above name other steps or bugs.
func loadFixed(t *testing.T, file string) map[string]fixedEntry {
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
	return entries
}

// writeFixed writes the Go console's answers to the fixed steps of a
// recording file. answers holds every listed step.
func writeFixed(t *testing.T, file string, answers map[string]any) {
	t.Helper()
	listed := fixesOf(file)
	entries := map[string]any{}
	for step, bug := range listed {
		answer, ok := answers[step]
		if !ok {
			t.Fatalf("the fixed step %q was not answered", step)
		}
		entries[step] = map[string]any{"bug": bug, "answer": answer}
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
	for _, n := range names {
		have[n] = true
	}
	for step := range fixesOf(file) {
		if !have[step] {
			t.Errorf("the fixed step %q is not in the recording %s", step, file)
		}
	}
}
