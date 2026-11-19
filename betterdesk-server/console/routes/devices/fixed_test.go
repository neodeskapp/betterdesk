package devices_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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
	// s-one is stored switched off (0 on SQLite); a change that leaves
	// enabled out keeps it off, where Node switched it on.
	{"node_sqlite", "425 PATCH /api/panel/strategies/s-one {} as admin", "DE2"},
	{"node_sqlite", `428 PATCH /api/panel/strategies/s-one {"permissions":{"enable-tunnel":"N"}} as admin`, "DE2"},
	{"node_sqlite", `429 PATCH /api/panel/strategies/s-one {"name":"Rule one!","user_group_guid":"ug-ops"} as admin`, "DE2"},
	{"node_sqlite", `430 PATCH /api/panel/strategies/s-one {"permissions":{"enable-audio":"custom","enable-camera":true,"other":"Y"}} as admin`, "DE2"},
	{"node_sqlite", "445 GET /api/panel/strategies/s-one as admin", "DE2"},
	{"node_sqlite", "455 GET /api/panel/strategies as admin", "DE2"},
	// The made-up server names X as deleted with A1001; the answer passes
	// it on.
	{"node", "258 DELETE /api/devices/A1001 {} as admin", "DE3"},
	{"node", "263 DELETE /api/devices/A1001?revoke=TRUE&hard=1 {} as admin", "DE3"},
	// search and sortOrder given as a list or an object are ignored: the
	// whole list in the default order, where Node answered 500.
	{"node", "9 GET /api/devices?search[]=x as admin", "DE4"},
	{"node", "10 GET /api/devices?search[a]=1 as admin", "DE4"},
	{"node", "18 GET /api/devices?sortOrder[]=asc as admin", "DE4"},
	{"node", "19 GET /api/devices?sortOrder[x]=1 as admin", "DE4"},
	// A new id that is no text is refused as an invalid id before
	// anything is asked, so the made-up server never renames A1001 to
	// 1234567 or true, and the audit log has no entries of it.
	{"node", `291 POST /api/devices/A1001/change-id {"newId":1234567} as admin`, "DE5"},
	{"node", `292 POST /api/devices/A1001/change-id {"newId":true} as admin`, "DE5"},
	{"node", `294 POST /api/devices/A1001/change-id {"newId":["a","b","c","d","e","f"]} as admin`, "DE5"},
	{"node", `296 POST /api/devices/A1001/change-id {"newId":{"length":8}} as admin`, "DE5"},
	{"node", `297 POST /api/devices/A1001/change-id {"newId":{"length":"7"}} as admin`, "DE5"},
	{"node", `300 POST /api/devices/A1001/change-id {"newId":{"a":1}} as admin`, "DE5"},
	// groupGuids that is no list is refused and changes nothing; the audit
	// log has no entry of it.
	{"node", `226 PUT /api/devices/A1001/groups {"groupGuids":"g-open"} as admin`, "DE6"},
	{"node", "232 PUT /api/devices/A1001/groups {} as admin", "DE6"},
	// A list that is no list and an empty reference are refused with 400
	// and their text, where Node answered 500.
	{"node", `436 POST /api/panel/strategies/s-two/assign {"peers":{"length":2}} as admin`, "DE7"},
	{"node", `438 POST /api/panel/strategies/s-two/assign {"peers":[""]} as admin`, "DE7"},
	// An agent that answers with the error agent_offline or agent_timeout
	// gets 502 with that text: no snapshot, no queued collection, no 503
	// or 504.
	{"node_agent", `17 GET /api/devices/A1001/services as admin agent {"ok":false,"error":"agent_timeout"}`, "WS8"},
	{"node_agent", `18 GET /api/devices/A1001/services as admin agent {"ok":false,"error":"agent_offline"}`, "WS8"},
	{"node_agent", `19 GET /api/devices/A1001/processes as admin agent {"ok":false,"error":"agent_offline"}`, "WS8"},
	{"node_agent", `20 GET /api/devices/A1001/clipboard as admin agent {"ok":false,"error":"agent_offline"}`, "WS8"},
	{"node_agent", `55 GET /api/devices/A1001/services as admin agent {"ok":false,"error":"agent_offline"}`, "WS8"},
	// The tag cut to 50 characters stops before the emoji that does not fit
	// whole, where Node kept half of it.
	{"node", `135 PUT /api/devices/A1001/tags {"tags":[" a ","",5,null,"b","  ","` + strings.Repeat("c", 60) + `","` + strings.Repeat("d😀", 30) + `"]} as admin`, "DGR2"},
	// The audit log after the steps of DE5, DE6 and DGR2
	{"node", "459 audit", "DE5, DE6, DGR2"},
	// The scope check does not take bulk-delete for a device id: no lookup
	// of a device of that name, and the made-up server's bulk-delete device
	// outside the operator's scope no longer refuses the bulk delete before
	// its permission check does.
	{"node", `384 POST /api/devices/bulk-delete {"ids":["A1001"]} as operator`, "UM35"},
	{"node", `385 POST /api/devices/bulk-delete {"ids":["C1003"]} as operator`, "UM35"},
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
