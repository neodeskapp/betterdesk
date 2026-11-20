package inventory_test

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// unreadable are the reports of testdata/node_*.json that the old list
// could not read (BUGS AI1): each was put in alone and taken out again,
// and while it was there the list answered 500 for every account. The Go
// list has it as an entry of its own, first, with what cannot be read
// left empty. Here each is the members of its entry that differ from a
// report with nothing in it.
var unreadable = map[string]map[string]string{
	"sqlite":   withUnreadableMembers("F-NO-JSON", "F-NO-JSON-SOFTWARE", "F-CUT", "F-TWO-VALUES", "F-COMMA", "F-HALF-ESCAPE", "F-CONTROL"),
	"postgres": withUnreadableMembers("F-TEXT-NO-JSON", "F-TEXT-NO-JSON-SOFTWARE", "F-TEXT-HEX", "F-TEXT-WORD"),
}

// withUnreadableMembers is the reports whose members the list cannot make
// text or a number of, which both recordings have, and the reports that
// are no JSON, which each database holds in its own way.
func withUnreadableMembers(noJSON ...string) map[string]string {
	disk, mount := `"disk_count":1`, `"disk_max_used_percent":20,"disk_max_mount":"","disk_max_free_bytes":4,`
	reports := map[string]string{
		"F-NAME":           "",
		"F-VERSION":        `"os":"OS","os_name":"OS"`,
		"F-NAME-IN-LIST":   "",
		"F-TOTAL":          "",
		"F-USED":           "",
		"F-DISK-TOTAL":     disk,
		"F-DISK-TOTAL-GB":  disk,
		"F-DISK-USED":      disk,
		"F-DISK-USED-GB":   disk,
		"F-DISK-AVAILABLE": disk,
		"F-DISK-FREE":      disk,
		"F-DISK-MOUNT":     mount + disk,
		"F-DISK-DEVICE":    mount + disk,
	}
	for _, id := range noJSON {
		reports[id] = ""
	}
	return reports
}

// entryOf is the list entry of a report with the members that differ
// from an empty one, without its two times.
func entryOf(id, members string) string {
	os := `"os":"","os_name":""`
	if strings.HasPrefix(members, `"os"`) {
		os, members = members, ""
	}
	mount, disks := "", `"disk_count":0`
	if before, after, found := strings.Cut(members, `"disk_count"`); found {
		mount, disks = before, `"disk_count"`+after
	}
	return `{"device_id":"` + id + `","hostname":"` + id + `",` + os + `,` + mount +
		`"cpu":"Unknown","cpu_cores":0,"cpu_usage":null,"memory_total_mb":0,"memory_used_mb":0,` + disks + `,"software_count":0}`
}

// times are the two times of a list entry.
var times = regexp.MustCompile(`,"last_seen":"[^"]*","collected_at":"[^"]*"`)

// unreadableListed checks the steps of TestNodeAnswers where Node
// answered the list with 500 because of one of the unreadable reports:
// the Go list answers 200 with that report first. done fails for the
// reports no such step listed.
func unreadableListed(db *store.DB) (check replay.Check, done func(t *testing.T)) {
	want := unreadable[db.Dialect().String()]
	seen := map[string]int{}
	check = func(t *testing.T, step replay.Step, got replay.Answer) bool {
		if step.Status != 500 || step.Path != "/api/inventory?limit=1" {
			return false
		}
		var list struct {
			Devices []json.RawMessage `json:"devices"`
		}
		if got.Status != 200 || json.Unmarshal([]byte(got.Body), &list) != nil || len(list.Devices) != 1 {
			t.Errorf("%s (AI1): %d %s, want the list with one report", step.Name, got.Status, got.Body)
			return true
		}
		entry := times.ReplaceAllString(string(list.Devices[0]), "")
		var id struct {
			DeviceID string `json:"device_id"`
		}
		_ = json.Unmarshal(list.Devices[0], &id)
		seen[id.DeviceID]++
		members, known := want[id.DeviceID]
		if expected := entryOf(id.DeviceID, members); !known || entry != expected {
			t.Errorf("%s (AI1): the entry\n    go: %s\n  want: %s", step.Name, entry, expected)
		}
		return true
	}
	done = func(t *testing.T) {
		var missing []string
		for id := range want {
			if seen[id] != 1 {
				missing = append(missing, id)
			}
		}
		sort.Strings(missing)
		if len(missing) > 0 {
			t.Errorf("reports that were not listed once where Node answered 500: %v", missing)
		}
	}
	return check, done
}

// One report the list cannot read in full takes nothing away but its own
// unread members (BUGS AI1). Beside a good report, every kind of report
// the old console threw on: members it could not make text or a number
// of (an object with a member named toString) and documents that are no
// JSON. The list answers 200 with all of them, the good one as it is and
// each bad one with what could not be read left empty.
func TestOneBadReportDoesNotFailTheList(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		second := 0
		insert := func(id, hardware, software string) {
			second++
			w.Exec("INSERT INTO device_inventory (device_id, hardware, software, collected_at, received_at) VALUES (?, ?, ?, ?, ?)",
				[]any{id, hardware, software, "2019-03-04T00:00:00.000Z", "2019-03-05 00:00:0" + string(rune('0'+second))})
		}
		noJSON := db.Pick("not json", `"not json"`)
		insert("NO-JSON-SOFTWARE", `{"hostname":"soft"}`, noJSON)
		insert("NO-JSON", noJSON, `{}`)
		insert("DISK", `{"disks":[{"total_bytes":{"toString":1}},{"total_bytes":4,"used_bytes":1,"mount_point":[{"toString":1}]}]}`, `{}`)
		insert("MEMORY", `{"memory":{"total_bytes":{"toString":1},"used_bytes":1048576}}`, `{}`)
		insert("NAME", `{"os_name":{"toString":1},"os_version":"1"}`, `{}`)
		insert("GOOD", `{"hostname":"good","os_name":"Linux","os_version":"6","memory":{"total_bytes":2147483648}}`, `{"apps":[1,2]}`)

		got := w.Do(replay.Request{Role: "admin", Path: "/api/inventory"})
		var list struct {
			Devices []map[string]any `json:"devices"`
			Total   int              `json:"total"`
		}
		if err := json.Unmarshal([]byte(got.Body), &list); got.Status != 200 || err != nil {
			t.Fatalf("the list with bad reports: %d %s", got.Status, got.Body)
		}
		ids := ""
		for _, d := range list.Devices {
			ids += d["device_id"].(string) + " "
		}
		if list.Total != 6 || ids != "GOOD NAME MEMORY DISK NO-JSON NO-JSON-SOFTWARE " {
			t.Fatalf("the list holds %d reports: %s", list.Total, ids)
		}
		for i, want := range []map[string]any{
			{"hostname": "good", "os": "Linux 6", "memory_total_mb": 2048.0, "software_count": 2.0},
			{"os": "1"},
			{"memory_total_mb": 0.0, "memory_used_mb": 1.0},
			{"disk_max_used_percent": 25.0, "disk_max_mount": "", "disk_max_free_bytes": 3.0},
			{"hostname": "NO-JSON", "os": "", "disk_count": 0.0},
			{"hostname": "soft", "software_count": 0.0},
		} {
			for key, value := range want {
				if list.Devices[i][key] != value {
					t.Errorf("%s: %s is %v, want %v", list.Devices[i]["device_id"], key, list.Devices[i][key], value)
				}
			}
		}

		// The report itself is read as before: a document that is no JSON
		// is the route's 500 for that device alone
		if got := w.Do(replay.Request{Role: "admin", Path: "/api/inventory/NO-JSON"}); got.Status != 500 {
			t.Errorf("the report that is no JSON: %d %s, want 500", got.Status, got.Body)
		}
		if got := w.Do(replay.Request{Role: "admin", Path: "/api/inventory/NAME"}); got.Status != 200 {
			t.Errorf("the report with a name that cannot be read: %d %s, want 200", got.Status, got.Body)
		}
	})
}

// typedMembers are the members of a list entry that the list reads by
// type (BUGS AI2), and how each is read.
var typedMembers = map[string]string{
	"hostname": "name", "os": "text", "os_name": "name", "cpu": "name",
	"cpu_cores": "number", "cpu_usage": "number", "disk_count": "length", "software_count": "length",
}

// listedByType checks the list steps whose answer the typed reading
// changes (BUGS AI2): the list holds Node's devices in Node's order, every
// entry with Node's members in Node's order, and only the typed members
// may differ from Node's. Each of those holds what the web app's row
// takes: a name is a text, the same as Node's where Node's was a text, a
// number's text where Node's was a number; a number is Node's where
// Node's was a number and null for any other value; a length is a whole
// number from 0. changed counts the entries that differ.
func listedByType(changed map[string]int) replay.Check {
	return func(t *testing.T, step replay.Step, got replay.Answer) bool {
		if step.Status != 200 || got.Status != 200 || !strings.HasPrefix(step.Answer, `{"devices":[`) || got.Body == step.Answer {
			return false
		}
		var node, have struct {
			Devices []json.RawMessage `json:"devices"`
		}
		rest := func(body string) string { return body[strings.LastIndex(body, "]"):] }
		if json.Unmarshal([]byte(step.Answer), &node) != nil || json.Unmarshal([]byte(got.Body), &have) != nil ||
			len(node.Devices) != len(have.Devices) || rest(step.Answer) != rest(got.Body) {
			t.Errorf("%s (AI2): the list differs beyond its entries:\n   node: %s\n     go: %s", step.Name, step.Answer, got.Body)
			return true
		}
		for i := range node.Devices {
			if string(node.Devices[i]) == string(have.Devices[i]) {
				continue
			}
			if problem := typedEntry(node.Devices[i], have.Devices[i]); problem != "" {
				t.Errorf("%s (AI2): entry %d: %s\n   node: %s\n     go: %s", step.Name, i, problem, node.Devices[i], have.Devices[i])
				continue
			}
			changed[step.Name]++
		}
		return true
	}
}

// typedEntry says what is wrong with the Go entry of a device beside
// Node's, "" when nothing is.
func typedEntry(nodeEntry, goEntry json.RawMessage) string {
	nodeKeys, node := members(nodeEntry)
	goKeys, have := members(goEntry)
	if strings.Join(nodeKeys, ",") != strings.Join(goKeys, ",") {
		return "the members differ"
	}
	for _, key := range nodeKeys {
		was, is := node[key], have[key]
		var kind any
		_ = json.Unmarshal(was, &kind)
		switch typedMembers[key] {
		case "":
			if string(was) != string(is) {
				return key + " differs"
			}
		case "text":
			if !strings.HasPrefix(string(is), `"`) {
				return key + " is no text"
			}
		case "name":
			switch kind.(type) {
			case string:
				if string(was) != string(is) {
					return key + " was a text and differs"
				}
			case float64:
				if `"`+string(was)+`"` != string(is) {
					return key + " is not the text of Node's number"
				}
			default:
				if fallback := map[string]string{"hostname": string(node["device_id"]), "os_name": `""`, "cpu": `"Unknown"`}[key]; string(is) != fallback {
					return key + " is not " + fallback
				}
			}
		case "number":
			_, isNumber := kind.(float64)
			if isNumber && string(was) != string(is) || !isNumber && string(is) != "null" {
				return key + " is not Node's number or null"
			}
		case "length":
			var n int
			if json.Unmarshal(is, &n) != nil || n < 0 {
				return key + " is no length"
			}
		}
	}
	return ""
}

// members reads an object's members and their order.
func members(object json.RawMessage) ([]string, map[string]json.RawMessage) {
	var values map[string]json.RawMessage
	_ = json.Unmarshal(object, &values)
	dec := json.NewDecoder(strings.NewReader(string(object)))
	var keys []string
	_, _ = dec.Token()
	for dec.More() {
		key, _ := dec.Token()
		keys = append(keys, key.(string))
		var skip json.RawMessage
		_ = dec.Decode(&skip)
	}
	return keys, values
}

// The list reads each member it shows by type, so that one value of an
// unexpected type does not make the web app's page fail (BUGS AI2): names
// are text (a number as its text), the numbers of cores and the load are
// numbers or null, the numbers of disks and apps are the length of a
// list.
func TestListMembersHaveTheirTypes(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		second := 0
		insert := func(id, hardware, software string) {
			second++
			w.Exec("INSERT INTO device_inventory (device_id, hardware, software, collected_at, received_at) VALUES (?, ?, ?, ?, ?)",
				[]any{id, hardware, software, "2019-03-04T00:00:00.000Z", "2019-03-05 00:00:0" + string(rune('0'+second))})
		}
		insert("OTHER-TYPES", `{"hostname":["pc"],"os_name":{"name":"Linux"},"os_version":true,"cpu":{"brand":["Intel"],"logical_cores":"8","usage_percent":"5"},"disks":"abc"}`,
			`{"apps":{"length":4}}`)
		insert("NUMBERS", `{"hostname":42,"os_name":11,"os_version":"Pro","cpu":{"brand":7,"logical_cores":8,"usage_percent":12.5},"disks":[{},{}]}`, `{"apps":[1,2,3]}`)
		insert("NOTHING", `{"hostname":"","cpu":{"brand":0,"logical_cores":0,"usage_percent":0}}`, `{"apps":"abc"}`)
		insert("FALSE", `{"hostname":false,"os_name":0,"cpu":{"logical_cores":false,"usage_percent":false}}`, `{}`)

		got := w.Do(replay.Request{Role: "admin", Path: "/api/inventory"})
		if got.Status != 200 {
			t.Fatalf("the list: %d %s", got.Status, got.Body)
		}
		entries := times.ReplaceAllString(got.Body, "")
		for _, want := range []string{
			`{"device_id":"FALSE","hostname":"FALSE","os":"","os_name":"","cpu":"Unknown","cpu_cores":0,"cpu_usage":null,"memory_total_mb":0,"memory_used_mb":0,"disk_count":0,"software_count":0}`,
			`{"device_id":"NOTHING","hostname":"NOTHING","os":"","os_name":"","cpu":"Unknown","cpu_cores":0,"cpu_usage":0,"memory_total_mb":0,"memory_used_mb":0,"disk_count":0,"software_count":0}`,
			`{"device_id":"NUMBERS","hostname":"42","os":"11 Pro","os_name":"11","cpu":"7","cpu_cores":8,"cpu_usage":12.5,"memory_total_mb":0,"memory_used_mb":0,"disk_count":2,"software_count":3}`,
			`{"device_id":"OTHER-TYPES","hostname":"OTHER-TYPES","os":"","os_name":"","cpu":"Unknown","cpu_cores":null,"cpu_usage":null,"memory_total_mb":0,"memory_used_mb":0,"disk_count":0,"software_count":0}`,
		} {
			if !strings.Contains(entries, want) {
				t.Errorf("the list lacks the entry %s:\n%s", want, entries)
			}
		}
	})
}
