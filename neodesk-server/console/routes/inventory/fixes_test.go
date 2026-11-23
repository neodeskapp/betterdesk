package inventory_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/replay"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
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

// A telemetry value that is NULL is null in a device's report on both
// databases, where PostgreSQL answered 0 (BUGS AI3).
func TestTelemetryNullIsNullOnBothDatabases(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		w.Exec("INSERT INTO device_inventory (device_id, hardware, software) VALUES ('D1', '{}', '{}'), ('D2', '{}', '{}')", nil)
		w.Exec(`INSERT INTO device_telemetry (device_id, cpu_usage_percent, memory_used_bytes, memory_total_bytes, uptime_secs) VALUES ('D1', NULL, NULL, NULL, NULL), ('D2', 12.5, 1024, 0, 60)`, nil)
		for id, want := range map[string]string{
			"D1": `"cpu_usage_percent":null,"memory_used_bytes":null,"memory_total_bytes":null,"uptime_secs":null`,
			"D2": `"cpu_usage_percent":12.5,"memory_used_bytes":1024,"memory_total_bytes":0,"uptime_secs":60`,
		} {
			if got := w.Do(replay.Request{Role: "admin", Path: "/api/inventory/" + id}); got.Status != 200 || !strings.Contains(got.Body, want) {
				t.Errorf("the report of %s: %d %s, want %s", id, got.Status, got.Body, want)
			}
		}
	})
}

// readFixes are the steps of testdata/node_*.json that the bug fixes
// change, by their request.
var readFixes replay.Fixes

// AI3: a telemetry value that is NULL is null on PostgreSQL too, where
// Node answered 0.
var _ = readFixes.Add("AI3", map[string]replay.Fix{
	"admin GET /api/inventory/EMPTY ": {Dialect: "postgres", Status: 200, Has: []string{
		`"telemetry":{"device_id":"EMPTY","cpu_usage_percent":null,"memory_used_bytes":null,"memory_total_bytes":null,"uptime_secs":null,"timestamp":null,"received_at":null}`,
	}},
})

// An upload sets the members of the device's info that the report has a
// value for and keeps the others. An info that is no object stays as it
// is, and a device the console's table lacks, or has deleted, is not
// written there. Node removed what the report left out, spread a list or
// a text into the keys 0, 1, ..., and copied a device of the signal
// server's table into the console's, bringing back a deleted one (BUGS
// AI13).
func TestUploadKeepsTheInfoTheReportDoesNotName(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		storetest.GoServer(t, db)
		w := replay.NewWorld(t, db, nil)
		for i, device := range []string{"keep-1", "list-1", "text-1", "gone-1", "sig-1"} {
			w.Exec("INSERT INTO access_tokens (token, token_hash, user_id, client_id, expires_at, token_kind) VALUES (?, ?, 5, ?, '2999-01-01 00:00:00', 'device')",
				[]any{fmt.Sprintf("tok-%d", i), sha256Hex(fmt.Sprintf("tok-%d", i)), device})
		}
		deleted := db.Pick("1", "TRUE")
		for _, row := range [][]any{
			{"keep-1", `{"hostname":"old","os":"Old OS","cpu":"Old CPU","memory_mb":1,"note":"mine"}`, db.Pick("0", "FALSE")},
			{"list-1", `[1,2]`, db.Pick("0", "FALSE")},
			{"text-1", `"text"`, db.Pick("0", "FALSE")},
			{"gone-1", `{"hostname":"gone"}`, deleted},
		} {
			w.Exec("INSERT INTO peer (id, note, info, is_deleted) VALUES (?, '', ?, "+row[2].(string)+")", row[:2])
		}
		w.Exec("INSERT INTO peers (id, hostname) VALUES ('sig-1', 'from-the-signal-server')", nil)
		w.Exec("INSERT INTO peers (id, hostname) VALUES ('gone-1', 'back')", nil)
		upload := func(i int, device, hardware string) {
			t.Helper()
			body := `{"device_id":"` + device + `","hardware":` + hardware + `}`
			got := w.Do(replay.Request{Method: "POST", Path: "/api/nd/inventory", Body: replay.Text(body), Headers: map[string]string{"Authorization": fmt.Sprintf("Bearer tok-%d", i)}})
			if got.Status != 200 {
				t.Errorf("upload of %s: %d %s", device, got.Status, got.Body)
			}
		}
		upload(0, "keep-1", `{"hostname":"new","os_name":"","cpu":{"brand":null,"logical_cores":4}}`)
		upload(1, "list-1", `{"hostname":"new"}`)
		upload(2, "text-1", `{"hostname":"new"}`)
		upload(3, "gone-1", `{"hostname":"new"}`)
		upload(4, "sig-1", `{"hostname":"new"}`)
		info := func(id string) any {
			var rows []struct{ Info string }
			_ = json.Unmarshal([]byte(w.Rows("SELECT "+db.Pick("info", "info::text AS info")+" FROM peer WHERE id = ?", []any{id})), &rows)
			if len(rows) != 1 {
				return nil
			}
			var v any
			_ = json.Unmarshal([]byte(rows[0].Info), &v)
			return v
		}
		want := map[string]any{"hostname": "new", "os": "Old OS", "cpu": "Old CPU", "cpu_cores": 4.0, "memory_mb": 1.0, "note": "mine"}
		if got := info("keep-1"); !reflect.DeepEqual(got, want) {
			t.Errorf("the info of keep-1: %v, want %v", got, want)
		}
		if got := info("list-1"); !reflect.DeepEqual(got, []any{1.0, 2.0}) {
			t.Errorf("the info of list-1: %v, want it unchanged", got)
		}
		if got := info("text-1"); got != "text" {
			t.Errorf("the info of text-1: %v, want it unchanged", got)
		}
		if got := w.Rows("SELECT id, CASE WHEN is_deleted"+db.Pick(" = 1", "")+" THEN 1 ELSE 0 END AS deleted FROM peer WHERE id IN ('gone-1', 'sig-1') ORDER BY id", nil); got != `[{"id":"gone-1","deleted":1}]` {
			t.Errorf("the console's rows of gone-1 and sig-1: %s, want only gone-1, deleted", got)
		}
		if got := info("gone-1"); !reflect.DeepEqual(got, map[string]any{"hostname": "gone"}) {
			t.Errorf("the info of gone-1: %v, want it unchanged", got)
		}
	})
}

// sha256Hex is the SHA-256 of a text in lower-case hex, as access tokens
// are stored.
func sha256Hex(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// A report with a disk whose size or name the route cannot make a number
// or text of is refused before anything is stored. A count of apps that
// cannot be made text no longer fails the report: the log counts the
// apps of a list. Node stored the report and the device's info and then
// answered 500 (BUGS AI14).
func TestARefusedReportIsNotStored(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		tokenDeviceA(w)
		w.Exec("INSERT INTO peer (id, note, info) VALUES ('device-a', '', '{\"hostname\":\"old\"}')", nil)
		upload := func(hardware, software string) replay.Answer {
			body := `{"device_id":"device-a","hardware":` + hardware + `,"software":` + software + `,"collected_at":"2011-03-04T10:00:00.000Z"}`
			return w.Do(replay.Request{Method: "POST", Path: "/api/nd/inventory", Body: replay.Text(body), Headers: deviceA})
		}
		for _, disk := range []string{
			`{"mount_point":"/t","total_bytes":{"toString":1},"used_bytes":10}`,
			`{"mount_point":"/t","total_gb":{"toString":1},"used_gb":0.5}`,
			`{"mount_point":"/u","total_bytes":200,"used_bytes":{"toString":1}}`,
			`{"mount_point":"/u","total_bytes":200,"free_bytes":{"toString":1}}`,
			`{"mount_point":{"toString":1},"total_bytes":5,"used_bytes":1}`,
		} {
			got := upload(`{"hostname":"new","disks":[`+disk+`]}`, `{"apps":[]}`)
			if got.Status != 400 || got.Body != `{"success":false,"error":"Invalid disks"}` {
				t.Errorf("a report with the disk %s: %d %s, want 400 Invalid disks", disk, got.Status, got.Body)
			}
		}
		if rows := w.Rows("SELECT device_id FROM device_inventory", nil) + w.Rows("SELECT "+db.Pick("info", "info::text AS info")+" FROM peer", nil); rows != `[][{"info":"{\"hostname\":`+db.Pick("", " ")+`\"old\"}"}]` {
			t.Errorf("after the refused reports: %s, want no report and the info as it was", rows)
		}
		for _, apps := range []string{`{"length":{"toString":1}}`, `{"length":[{"toString":1}]}`, `"many"`} {
			if got := upload(`{"hostname":"new"}`, `{"apps":`+apps+`}`); got.Status != 200 || got.Body != `{"success":true}` {
				t.Errorf("a report with the apps %s: %d %s, want it stored", apps, got.Status, got.Body)
			}
		}
	})
}

// changedUploads are the uploads of the device recordings whose answer a
// bug fix of BUGS.md changes, by database and name (the names differ
// between the recordings): a disk the route cannot read is 400 before the
// report is stored, and a count of apps that cannot be made text no
// longer fails the stored report (BUGS AI14).
var changedUploads = map[string]map[string]replay.Fix{
	"sqlite": {
		"549 POST /api/nd/inventory": invalidDisks, "550 POST /api/nd/inventory": invalidDisks, "608 POST /api/nd/inventory": invalidDisks,
		"609 POST /api/nd/inventory": invalidDisks, "610 POST /api/nd/inventory": invalidDisks, "620 POST /api/nd/inventory": invalidDisks,
		"629 POST /api/nd/inventory": appsCounted, "630 POST /api/nd/inventory": appsCounted,
	},
	"postgres": {
		"547 POST /api/nd/inventory": invalidDisks, "548 POST /api/nd/inventory": invalidDisks, "606 POST /api/nd/inventory": invalidDisks,
		"607 POST /api/nd/inventory": invalidDisks, "608 POST /api/nd/inventory": invalidDisks, "618 POST /api/nd/inventory": invalidDisks,
		"627 POST /api/nd/inventory": appsCounted, "628 POST /api/nd/inventory": appsCounted,
	},
}

var (
	invalidDisks = replay.Fix{Bug: "AI14", Status: 400, Answer: `{"success":false,"error":"Invalid disks"}`}
	appsCounted  = replay.Fix{Bug: "AI14", Status: 200, Answer: `{"success":true}`}
)

// changedUpload checks the steps of changedUploads, and done fails for
// those the recording does not have.
func changedUpload(db *store.DB) (check replay.Check, done func(t *testing.T)) {
	steps := changedUploads[db.Dialect().String()]
	seen := map[string]bool{}
	return func(t *testing.T, step replay.Step, got replay.Answer) bool {
			fix, ok := steps[step.Name]
			if !ok {
				return false
			}
			seen[step.Name] = true
			if got.Status != fix.Status || got.Body != fix.Answer {
				t.Errorf("%s (%s): %d %s, want %d %s", step.Name, fix.Bug, got.Status, got.Body, fix.Status, fix.Answer)
			}
			return true
		}, func(t *testing.T) {
			for name := range steps {
				if !seen[name] {
					t.Errorf("the listed step %q is not in the recording", name)
				}
			}
		}
}

// The values of the two uploads have their types, the same on both
// databases: a time of collection or of telemetry is a time, the CPU load
// a finite number, the memory in bytes and the uptime in seconds whole
// numbers from 0 that their columns hold, and a report holds no U+0000
// and no half of a surrogate pair. Anything else is 400 and stores
// nothing, where SQLite stored it as it came and PostgreSQL answered 500
// (BUGS AI15).
func TestUploadValuesHaveTheirTypes(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorld(t, db, nil)
		tokenDeviceA(w)
		send := func(path, body string) replay.Answer {
			return w.Do(replay.Request{Method: "POST", Path: path, Body: replay.Text(body), Headers: deviceA})
		}
		good := `"cpu_usage_percent":12.5,"memory_used_bytes":1048576,"memory_total_bytes":2097152,"uptime_secs":60,"timestamp":"2011-03-04T10:00:00.000Z"`
		if got := send("/api/nd/telemetry", `{"device_id":"device-a",`+good+`}`); got.Status != 200 {
			t.Fatalf("good telemetry: %d %s", got.Status, got.Body)
		}
		stored := w.Rows("SELECT cpu_usage_percent, memory_used_bytes, memory_total_bytes, uptime_secs FROM device_telemetry", nil)
		for _, c := range []struct{ name, values string }{
			{"cpu_usage_percent", `"hot"`}, {"cpu_usage_percent", `"50"`}, {"cpu_usage_percent", `true`}, {"cpu_usage_percent", `[]`},
			{"cpu_usage_percent", `{}`}, {"cpu_usage_percent", `1e400`}, {"cpu_usage_percent", `1e39`},
			{"memory_used_bytes", `1.5`}, {"memory_used_bytes", `-1`}, {"memory_used_bytes", `"1024"`}, {"memory_used_bytes", `1e30`},
			{"memory_total_bytes", `false`}, {"memory_total_bytes", `{"toString":1}`},
			{"uptime_secs", `0.5`}, {"uptime_secs", `"60"`}, {"uptime_secs", `3000000000`},
			{"timestamp", `5`}, {"timestamp", `"garbage"`}, {"timestamp", `true`}, {"timestamp", `"2011-13-01T00:00:00Z"`},
		} {
			body := `{"device_id":"device-a",` + strings.Replace(good, `"`+c.name+`":`, `"`+c.name+`":`+c.values+`,"was_`+c.name+`":`, 1) + `}`
			if got := send("/api/nd/telemetry", body); got.Status != 400 || got.Body != `{"success":false,"error":"Invalid `+c.name+`"}` {
				t.Errorf("telemetry with %s %s: %d %s, want 400 Invalid %s", c.name, c.values, got.Status, got.Body, c.name)
			}
		}
		if got := w.Rows("SELECT cpu_usage_percent, memory_used_bytes, memory_total_bytes, uptime_secs FROM device_telemetry", nil); got != stored {
			t.Errorf("the telemetry after the refused uploads: %s, want %s", got, stored)
		}
		if got := send("/api/nd/telemetry", `{"device_id":"device-a","cpu_usage_percent":null,"timestamp":null}`); got.Status != 200 {
			t.Errorf("telemetry without values: %d %s, want it stored", got.Status, got.Body)
		}

		report := func(hardware, software, collectedAt string) string {
			return `{"device_id":"device-a","hardware":` + hardware + `,"software":` + software + `,"collected_at":` + collectedAt + `}`
		}
		if got := send("/api/nd/inventory", report(`{"hostname":"pc"}`, `{"apps":[]}`, `"2011-03-04T10:00:00.000Z"`)); got.Status != 200 {
			t.Fatalf("a good report: %d %s", got.Status, got.Body)
		}
		for _, c := range []struct{ name, body string }{
			{"collected_at", report(`{"hostname":"x"}`, `{}`, `5`)},
			{"collected_at", report(`{"hostname":"x"}`, `{}`, `"garbage"`)},
			{"collected_at", report(`{"hostname":"x"}`, `{}`, `["2011-03-04T10:00:00.000Z"]`)},
			{"hardware", report(`{"hostname":"a\u0000b"}`, `{}`, `null`)},
			{"hardware", report(`{"disks":[{"mount_point":"\ud800"}]}`, `{}`, `null`)},
			{"hardware", report(`{"\udc00":1}`, `{}`, `null`)},
			{"software", report(`{"hostname":"x"}`, `{"apps":[{"name":"\u0000"}]}`, `null`)},
		} {
			if got := send("/api/nd/inventory", c.body); got.Status != 400 || got.Body != `{"success":false,"error":"Invalid `+c.name+`"}` {
				t.Errorf("the report %s: %d %s, want 400 Invalid %s", c.body, got.Status, got.Body, c.name)
			}
		}
		if got := w.Rows("SELECT hardware FROM device_inventory", nil); got != `[{"hardware":"{\"hostname\":`+db.Pick("", " ")+`\"pc\"}"}]` {
			t.Errorf("the report after the refused uploads: %s, want the good one", got)
		}
		if got := send("/api/nd/inventory", report(`{"hostname":"a😀"}`, `{}`, `false`)); got.Status != 200 {
			t.Errorf("a report with a whole surrogate pair and no time: %d %s, want it stored", got.Status, got.Body)
		}
	})
}
