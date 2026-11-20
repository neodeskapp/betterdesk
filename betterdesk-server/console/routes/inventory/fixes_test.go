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
		"F-NAME":           `"os":"","os_name":{"toString":1}`,
		"F-VERSION":        `"os":"OS","os_name":"OS"`,
		"F-NAME-IN-LIST":   `"os":"","os_name":[1,[2,{"toString":"x"}]]`,
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
