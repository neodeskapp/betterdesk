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
	// and metrics.
	{"node", "264 cleared", "DE1"},
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
	{"node", "9 GET /api/devices?search[]=x as admin", "DE4, RV25"},
	{"node", "10 GET /api/devices?search[a]=1 as admin", "DE4, RV25"},
	{"node", "18 GET /api/devices?sortOrder[]=asc as admin", "DE4, RV25"},
	{"node", "19 GET /api/devices?sortOrder[x]=1 as admin", "DE4, RV25"},
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
	{"node", `226 PUT /api/devices/A1001/groups {"groupGuids":"g-open"} as admin`, "DE6, RV25"},
	{"node", "232 PUT /api/devices/A1001/groups {} as admin", "DE6, RV25"},
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
	// A group whose name is a list and whose note is a number is refused,
	// where Node saved it as "a,b" with the note "5". The groups made
	// after it get other ids, and the lists leave it out. The operator,
	// whose scope is limited, may not make a group with an access list
	// (RV59): the lists and the writes of the device's groups after it
	// leave "By operator" out.
	{"node", `210 POST /api/device-groups {"name":["a","b"],"note":5,"team_id":null,"allowed_users":"e2e_viewer, e2e_operator ,x","allowed_groups":[{"guid":"ug-ops"},"ug-ops","missing"]} as admin`, "DGR5"},
	{"node", `214 POST /api/device-groups {"name":"By operator","allowed_users":["e2e_operator"]} as operator`, "DGR5, RV59"},
	{"node", "215 GET /api/device-groups as admin", "DGR5, UM33, RV59, RV25"},
	{"node", "220 GET /api/devices/A1001/groups as admin", "DGR5, UM33, RV59, RV25"},
	{"node", "221 GET /api/devices/A1001/groups as operator", "DGR5, UM33, RV25"},
	{"node", "223 GET /api/devices/C1003/groups as admin", "DGR5, UM33, RV59, RV25"},
	{"node", "237 GET /api/devices/A1001/groups as admin", "DGR5, RV59, RV25"},
	{"node", "356 GET /api/devices/C1003/groups as viewer", "DGR5, RV59, RV25"},
	// The audit log after the steps of DE5, DE6, DGR2, DGR5, UM33 and
	// RV59
	{"node", "459 audit", "DE5, DE6, DGR2, DGR5, UM33, RV59"},
	// The scope check does not take bulk-delete for a device id: no lookup
	// of a device of that name, and the made-up server's bulk-delete device
	// outside the operator's scope no longer refuses the bulk delete before
	// its permission check does.
	{"node", `384 POST /api/devices/bulk-delete {"ids":["A1001"]} as operator`, "UM35"},
	{"node", `385 POST /api/devices/bulk-delete {"ids":["C1003"]} as operator`, "UM35"},
	// The device's manual groups go to the API server in one call, which
	// the made-up server of the recording does not have (404): the
	// console's own writer takes each write, where Node read and wrote
	// every group through the server and answered its refusals. The rows
	// and lists after them follow (steps 215 to 223 and 459 above).
	{"node", `148 PUT /api/devices/A1001/groups {"groupGuids":["g-ops"]} as admin`, "UM33, RV25"},
	{"node", `149 PUT /api/devices/A1001/groups {"groupGuids":["g-open"]} as operator`, "UM33, RV25"},
	{"node", `150 PUT /api/devices/A1001/groups {"groupGuids":["g-viewer"]} as admin`, "UM33, RV25"},
	{"node", `151 PUT /api/devices/B1002/groups {"groupGuids":[]} as admin`, "UM33, RV25"},
	{"node", `152 PUT /api/devices/B1002/groups {"groupGuids":[]} as admin`, "UM33, RV25"},
	{"node", `153 PUT /api/devices/B1002/groups {"groupGuids":[]} as admin`, "UM33, RV25"},
	{"node", `154 PUT /api/devices/B1002/groups {"groupGuids":[]} as admin`, "UM33, RV25"},
	{"node", `155 PUT /api/devices/B1002/groups {"groupGuids":[]} as admin`, "UM33, RV25"},
	{"node", `156 PUT /api/devices/B1002/groups {"groupGuids":[]} as admin`, "UM33, RV25"},
	{"node", `157 PUT /api/devices/B1002/groups {"groupGuids":[]} as admin`, "UM33, RV25"},
	{"node", `158 PUT /api/devices/B1002/groups {"groupGuids":[]} as admin`, "UM33, RV25"},
	{"node", `159 PUT /api/devices/B1002/groups {"groupGuids":[]} as admin`, "UM33, RV25"},
	{"node", `160 PUT /api/devices/B1002/groups {"groupGuids":[]} as admin`, "UM33, RV25"},
	{"node", `161 PUT /api/devices/B1002/groups {"groupGuids":[]} as admin`, "UM33, RV25"},
	{"node", `162 PUT /api/devices/B1002/groups {"groupGuids":[]} as admin`, "UM33, RV25"},
	{"node", `163 PUT /api/devices/B1002/groups {"groupGuids":[]} as admin`, "UM33, RV25"},
	{"node", `164 PUT /api/devices/B1002/groups {"groupGuids":[]} as admin`, "UM33, RV25"},
	{"node", `165 PUT /api/devices/B1002/groups {"groupGuids":[]} as admin`, "UM33, RV25"},
	{"node", `166 PUT /api/devices/B1002/groups {"groupGuids":[]} as admin`, "UM33, RV25"},
	{"node", `167 PUT /api/devices/B1002/groups {"groupGuids":[]} as admin`, "UM33, RV25"},
	{"node", `168 PUT /api/devices/B1002/groups {"groupGuids":[]} as admin`, "UM33, RV25"},
	{"node", "169 members after the panel writes", "UM33"},
	{"node", "222 GET /api/devices/A1001/groups as viewer", "UM33, RV59, RV25"},
	{"node", `230 PUT /api/devices/A1001/groups {"groupGuids":["g-ops","g-ops","no-such","g-lab"]} as admin`, "UM33, RV59, RV25"},
	{"node", `231 PUT /api/devices/A1001/groups {"groupGuids":[null,5]} as admin`, "UM33, RV59, RV25"},
	{"node", `233 PUT /api/devices/A1001/groups {"groupGuids":["g-ops","g-viewer"]} as operator`, "UM33, RV59, RV25"},
	{"node", `236 PUT /api/devices/A1001/groups {"groupGuids":["g-open"]} as admin`, "UM33, RV59, RV25"},
	// folder_5 is open to e2e_viewer alone, so the operator, whose scope
	// it limits, puts no device into folder 5. A1001 then stays in the
	// operator's scope, and the next step answers Node's 404 for folder
	// 999 again.
	{"node", `402 PATCH /api/devices/A1001/folder {"folderId":5} as operator`, "RV11"},
	// An id that starts with rt_ and names no remote target is looked up
	// as a peer too, alive and deleted: the answers stay, the calls to the
	// API server grow.
	{"node", "42 GET /api/devices/rt_nope as admin", "RV30"},
	{"node", `304 POST /api/devices/A1001/change-id {"newId":"rt_target1"} as admin`, "RV30"},
	// The device list asks the API server for the remote targets with
	// include_disabled=true, so a switched-off RDP or VNC target is listed
	// without Show deleted. Every step that reads the device list (the
	// lists, the tags, the device groups and the group writes) makes that
	// call; the steps of other bugs above that make it carry RV25 too.
	// The lists show the disabled rt_2 where the made-up server has it.
	{"node", "0 GET /api/devices?sortBy=id&sortOrder=asc as admin", "RV25"},
	{"node", "1 GET /api/devices?sortBy=id&sortOrder=asc as operator", "RV25"},
	{"node", "2 GET /api/devices?sortBy=id&sortOrder=asc as viewer", "RV25"},
	{"node", "3 GET /api/devices?sortBy=id&sortOrder=asc as guest_issuer", "RV25"},
	{"node", "4 GET /api/devices as admin", "RV25"},
	{"node", "5 GET /api/devices?sortBy=hostname&sortOrder=DESC as admin", "RV25"},
	{"node", "6 GET /api/devices?search=ALP as admin", "RV25"},
	{"node", "7 GET /api/devices?search=fan&sortBy=note as admin", "RV25"},
	{"node", "8 GET /api/devices?search= as admin", "RV25"},
	{"node", "11 GET /api/devices?status=online as admin", "RV25"},
	{"node", "12 GET /api/devices?status=offline as admin", "RV25"},
	{"node", "13 GET /api/devices?status=banned as admin", "RV25"},
	{"node", "14 GET /api/devices?status[]=banned as admin", "RV25"},
	{"node", "15 GET /api/devices?hasNotes=true as admin", "RV25"},
	{"node", "16 GET /api/devices?hasNotes=TRUE as admin", "RV25"},
	{"node", "20 GET /api/devices?sortOrder= as admin", "RV25"},
	{"node", "21 GET /api/devices?sortBy=password&sortOrder=asc as admin", "RV25"},
	{"node", "22 GET /api/devices?sortBy[]=id as admin", "RV25"},
	{"node", "23 GET /api/devices?sortBy=version&sortOrder=asc as admin", "RV25"},
	{"node", "24 GET /api/devices?sortBy=created_at as admin", "RV25"},
	{"node", "25 GET /api/devices?sortBy=os&sortOrder=ASC as admin", "RV25"},
	{"node", "26 GET /api/devices?sortBy=username as admin", "RV25"},
	{"node", "27 GET /api/devices?page=2&limit=1&sortBy=id as admin", "RV25"},
	{"node", "29 GET /api/devices?sortBy=id as admin", "RV25"},
	{"node", "30 GET /api/devices?sortBy=id as admin", "RV25"},
	{"node", "31 GET /api/devices?sortBy=id as admin", "RV25"},
	{"node", "114 GET /api/tags as admin", "RV25"},
	{"node", "115 GET /api/tags as operator", "RV25"},
	{"node", "116 GET /api/tags as viewer", "RV25"},
	{"node", "117 GET /api/tags as guest_issuer", "RV25"},
	{"node", `122 POST /api/tags/rename {"from":5,"to":6} as admin`, "RV25"},
	{"node", `123 POST /api/tags/rename {"from":"nobody-has","to":"y"} as admin`, "RV25"},
	{"node", `124 POST /api/tags/rename {"from":"LAB","to":"lab-2"} as admin`, "RV25"},
	{"node", `125 POST /api/tags/rename {"from":"lab-2","to":"lab"} as operator`, "RV25"},
	{"node", `126 POST /api/tags/rename {"from":"lab","to":"lab-3"} as admin`, "RV25"},
	{"node", `129 POST /api/tags/delete {"tag":42} as admin`, "RV25"},
	{"node", `130 POST /api/tags/delete {"tag":"Office"} as admin`, "RV25"},
	{"node", `131 POST /api/tags/delete {"tag":"fan"} as admin`, "RV25"},
	{"node", `132 POST /api/tags/delete {"tag":"kiosk"} as operator`, "RV25"},
	{"node", "144 GET /api/device-groups as admin", "RV25"},
	{"node", "145 GET /api/device-groups as operator", "RV25"},
	{"node", "146 GET /api/device-groups as viewer", "RV25"},
	{"node", "147 GET /api/device-groups as guest_issuer", "RV25"},
	{"node", `229 PUT /api/devices/A1001/groups {"groupGuids":["g-open","g-ops"]} as admin`, "RV25"},
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
