package inventory_test

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/replay"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The device recordings read the info of a device's row in the console's
// table after an upload ("the info of ..."). An upload sets the members of
// the info the report has a value for and keeps the others, leaves an info
// that is no object alone, and writes no row a lookup would have copied or
// brought back (BUGS AI13), so those rows differ from Node's. keptInfo
// takes these statements out of the recording and checks them itself:
//
//   - A device the seed has no row of, or a deleted one, keeps what the
//     seed put there: no row, or the deleted row as it was.
//   - An info that is no object for the upload, or one whose last upload
//     the Go console did not store, stays as it was.
//   - Otherwise the info is Node's, with the members Node removed because
//     the report had no true value for them as they were.
//
// The statements that give device-a its info back are run here too, and
// "the copies" finds only the deleted row of the seed.

// infoMembers are the members of the info an upload sets.
var infoMembers = []string{"hostname", "os", "os_version", "cpu", "cpu_cores", "memory_mb"}

// seedRow is a device's row the seed puts into the console's table.
type seedRow struct {
	note    any
	info    any // the text, nil for NULL
	deleted bool
}

// keptInfo takes the statements of the info out of the recording and
// returns the Check that checks them and follows the uploads, with the
// number of statements whose rows differ from Node's.
func keptInfo(rec *replay.Recording, w *replay.World, copies string) (replay.Check, *int) {
	postgres := w.DB.IsPostgres()
	seeded := map[string]seedRow{}
	for _, seed := range rec.Seed {
		if strings.HasPrefix(seed.SQL, "INSERT INTO peer (id, note, info, is_deleted)") {
			id, _ := seed.Params[0].(string)
			seeded[id] = seedRow{note: seed.Params[1], info: seed.Params[2], deleted: seed.Params[3] == true || seed.Params[3] == 1.0}
		}
	}
	tokens := tokensOf(*rec)
	statements := map[string]replay.Step{}
	for i, step := range rec.Steps {
		if step.SQL != "" && (strings.HasPrefix(step.Name, "the info of ") || step.Name == "the copies" || strings.Contains(step.Name, " gets its info back ")) {
			name := fmt.Sprintf("%s (step %d)", step.Name, i)
			statements[name] = step
			rec.Steps[i] = replay.Step{Name: name}
		}
	}
	// The info of each device now and before its last upload, and whether
	// it was uploaded since the last statement of the device
	info, before, uploaded := map[string]any{}, map[string]any{}, map[string]bool{}
	for id, row := range seeded {
		info[id] = row.info
	}
	current := func(id string) any {
		return rawInfo(w.Rows("SELECT "+w.DB.Pick("info", "info::text AS info")+" FROM peer WHERE id = ?", []any{id}))
	}
	differ := 0
	return func(t *testing.T, step replay.Step, got replay.Answer) bool {
		if step.Method == "POST" && strings.HasSuffix(strings.ToLower(strings.TrimSuffix(strings.Split(step.Path, "?")[0], "/")), "/inventory") {
			id := tokens[strings.TrimSpace(strings.TrimPrefix(step.Headers["Authorization"], "Bearer "))]
			if row, known := seeded[id]; known && !row.deleted && got.Status == 200 {
				before[id], info[id], uploaded[id] = info[id], current(id), true
			}
			return false
		}
		original, ok := statements[step.Name]
		switch {
		case !ok:
			return false
		case original.Name == "the copies":
			have := w.Rows(original.SQL, original.Params)
			if have != original.Rows {
				differ++
			}
			if have != copies {
				t.Errorf("%s (AI13): %s, want %s", step.Name, have, copies)
			}
			return true
		case !strings.HasPrefix(original.Name, "the info of "):
			w.Exec(original.SQL, original.Params)
			id, _ := original.Params[1].(string)
			info[id] = original.Params[0]
			return true
		}
		id := strings.TrimPrefix(original.Name, "the info of ")
		have := w.Rows(original.SQL, original.Params)
		haveRows, nodeRows := decodedRows(have), decodedRows(original.Rows)
		if have != original.Rows {
			differ++
		}
		row, isSeeded := seeded[id]
		switch {
		case !isSeeded:
			if len(haveRows) != 0 {
				t.Errorf("%s (AI13): %s, want no row", step.Name, have)
			}
			return true
		case row.deleted:
			want := []map[string]any{{"id": id, "note": row.note, "info": row.info, "is_deleted": json.Number("1")}}
			if !sameRows(haveRows, want) {
				t.Errorf("%s (AI13): %s, want the seed's deleted row", step.Name, have)
			}
			return true
		case len(haveRows) != 1 || len(nodeRows) != 1:
			t.Errorf("%s: %s, want one row as Node's %s", step.Name, have, original.Rows)
			return true
		}
		// The last upload set what Node's info has of the members and kept
		// the members Node removed; an info that is no object it left alone
		want, _ := infoValue(info[id], postgres)
		if uploaded[id] {
			old, isObject := infoValue(before[id], postgres)
			after, nodeObject := infoValue(nodeRows[0]["info"], postgres)
			want = old
			if isObject && nodeObject {
				kept := after.(map[string]any)
				for _, member := range infoMembers {
					if value, had := old.(map[string]any)[member]; had {
						if _, set := kept[member]; !set {
							kept[member] = value
						}
					}
				}
				want = kept
			}
		}
		haveInfo, _ := infoValue(haveRows[0]["info"], postgres)
		nodeRows[0]["info"], haveRows[0]["info"] = nil, nil
		if !reflect.DeepEqual(haveInfo, want) || !reflect.DeepEqual(haveRows, nodeRows) {
			t.Errorf("%s (AI13): %s\n  want the info %v, the rest as Node's %s", step.Name, have, want, original.Rows)
		}
		info[id], uploaded[id] = rawInfo(have), false
		return true
	}, &differ
}

// decodedRows reads rows as Rows writes them, numbers as json.Number.
func decodedRows(text string) []map[string]any {
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var rows []map[string]any
	_ = dec.Decode(&rows)
	return rows
}

// rawInfo is the info column of the one row of a Rows text.
func rawInfo(rows string) any {
	var decoded []map[string]any
	_ = json.Unmarshal([]byte(rows), &decoded)
	if len(decoded) != 1 {
		return nil
	}
	return decoded[0]["info"]
}

// sameRows compares rows whose info is compared as the JSON it holds.
func sameRows(have, want []map[string]any) bool {
	if len(have) != len(want) {
		return false
	}
	for i := range have {
		a, _ := infoValue(have[i]["info"], false)
		b, _ := infoValue(want[i]["info"], false)
		if !reflect.DeepEqual(a, b) {
			return false
		}
		for key, value := range want[i] {
			if key != "info" && fmt.Sprint(have[i][key]) != fmt.Sprint(value) {
				return false
			}
		}
	}
	return true
}

// infoValue is the info of a row as an upload reads it, and whether that
// is an object it sets members of. No info (NULL) is an empty object. On
// SQLite the text column holds JSON text, and the empty text is an empty
// object. On PostgreSQL the JSON column holds a JSON value, and a text
// there holds JSON text itself. What is no JSON is the text.
func infoValue(column any, postgres bool) (any, bool) {
	text, isText := column.(string)
	if !isText {
		return map[string]any{}, true
	}
	if text == "" {
		return map[string]any{}, true
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var value any
	if dec.Decode(&value) != nil {
		return text, false
	}
	if inner, isString := value.(string); isString && postgres {
		if inner == "" {
			return map[string]any{}, true
		}
		dec := json.NewDecoder(strings.NewReader(inner))
		dec.UseNumber()
		var innerValue any
		if dec.Decode(&innerValue) == nil {
			if object, isObject := innerValue.(map[string]any); isObject {
				return object, true
			}
		}
		return value, false
	}
	object, isObject := value.(map[string]any)
	if isObject {
		return object, true
	}
	return value, false
}

// The uploads check the types of their values (BUGS AI15): a time of
// collection or of telemetry is a time, the CPU load a finite number, the
// memory and the uptime whole numbers from 0, and a report holds no
// U+0000 and no half of a surrogate pair. typedRefusal is the 400 an
// upload of the recordings gets for that ("" for none), and typedRows
// checks the statements read after the uploads: after an upload the Go
// console refused and Node stored or failed, the report and the
// telemetry of the device are what they were at the statement before, and
// no alert was raised.

// telemetryFields are the numbers of the telemetry in the order the route
// checks them: the largest value and whether a fraction is one.
var telemetryFields = []struct {
	name     string
	max      float64
	fraction bool
}{
	{"cpu_usage_percent", math.MaxFloat32, true},
	{"memory_used_bytes", 1<<53 - 1, false},
	{"memory_total_bytes", 1<<53 - 1, false},
	{"uptime_secs", math.MaxInt32, false},
}

// typedRefusal is the name in the 400 of the type checks for an upload of
// the recordings, "" when they let it pass.
func typedRefusal(path, body string) string {
	dec := json.NewDecoder(strings.NewReader(body))
	dec.UseNumber()
	var b map[string]any
	if dec.Decode(&b) != nil {
		return ""
	}
	isTime := func(v any) bool {
		if !truthy(v) {
			return true
		}
		text, isText := v.(string)
		at, ok := store.ParseTime(text)
		return isText && ok && at.UTC().Year() >= 1 && at.UTC().Year() <= 9999
	}
	if strings.HasSuffix(strings.ToLower(strings.TrimSuffix(path, "/")), "/telemetry") {
		for _, field := range telemetryFields {
			v, given := b[field.name]
			if !given || v == nil {
				continue
			}
			number, isNumber := v.(json.Number)
			n, err := strconv.ParseFloat(string(number), 64)
			if !isNumber || err != nil || math.Abs(n) > field.max || !field.fraction && (n < 0 || n != math.Trunc(n)) {
				return field.name
			}
		}
		if !isTime(b["timestamp"]) {
			return "timestamp"
		}
		return ""
	}
	if !truthy(b["device_id"]) || !truthy(b["hardware"]) {
		return ""
	}
	if !isTime(b["collected_at"]) {
		return "collected_at"
	}
	var raw map[string]json.RawMessage
	_ = json.Unmarshal([]byte(body), &raw)
	for _, part := range []string{"hardware", "software"} {
		if !storableJSON(string(raw[part])) {
			return part
		}
	}
	return ""
}

// truthy is JavaScript's !!v for a value decoded with UseNumber.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case json.Number:
		n, _ := strconv.ParseFloat(string(x), 64)
		return n != 0 && !math.IsNaN(n)
	case string:
		return x != ""
	}
	return true
}

// storableJSON reports whether a JSON text has no escape of U+0000 and no
// escape of half a surrogate pair without the other half.
func storableJSON(text string) bool {
	unit := func(i int) (rune, bool) {
		if i+6 > len(text) || text[i] != '\\' || text[i+1] != 'u' {
			return 0, false
		}
		n, err := strconv.ParseUint(text[i+2:i+6], 16, 32)
		return rune(n), err == nil
	}
	for i := 0; i < len(text); i++ {
		if text[i] != '\\' {
			continue
		}
		u, ok := unit(i)
		if !ok {
			// Another escape: the escaped character is no escape itself
			i++
			continue
		}
		switch {
		case u == 0, u >= 0xDC00 && u <= 0xDFFF:
			return false
		case u >= 0xD800 && u <= 0xDBFF:
			low, ok := unit(i + 6)
			if !ok || low < 0xDC00 || low > 0xDFFF {
				return false
			}
			i += 6
		}
		i += 5
	}
	return true
}

// typedUploads checks the answers of the uploads the type checks refuse,
// and counts them.
func typedUploads(count *int) replay.Check {
	return func(t *testing.T, step replay.Step, got replay.Answer) bool {
		if step.Method != "POST" || step.Body == nil || step.Status == 400 {
			return false
		}
		name := typedRefusal(strings.Split(step.Path, "?")[0], *step.Body)
		if name == "" {
			return false
		}
		*count++
		if want := `{"success":false,"error":"Invalid ` + name + `"}`; got.Status != 400 || got.Body != want {
			t.Errorf("%s (AI15): %d %s, want 400 %s", step.Name, got.Status, got.Body, want)
		}
		return true
	}
}

// typedRows takes the statements of the reports, the telemetry and the
// alerts out of the recording and returns the Check that checks them and
// follows the uploads, with the number of statements whose rows are no
// longer Node's.
func typedRows(rec *replay.Recording, w *replay.World) (replay.Check, *int) {
	tokens := tokensOf(*rec)
	statements := map[string]replay.Step{}
	for i, step := range rec.Steps {
		if step.SQL != "" && (strings.HasPrefix(step.Name, "the report of ") || strings.HasPrefix(step.Name, "the telemetry of ") || step.Name == "the alerts raised" || step.Name == "a time was stored") {
			name := fmt.Sprintf("%s (step %d)", step.Name, i)
			statements[name] = step
			rec.Steps[i] = replay.Step{Name: name}
		}
	}
	// refused names the statements whose rows the Go console left as they
	// were at an upload Node stored, until it stores one again; stored the
	// alerts an upload changed since the statement before, after which the
	// alerts go; last holds the rows of each statement the last time it
	// was read
	refused, stored, last := map[string]bool{}, map[string]bool{}, map[string]string{}
	differ := 0
	return func(t *testing.T, step replay.Step, got replay.Answer) bool {
		if step.Method == "POST" {
			path := strings.ToLower(strings.TrimSuffix(strings.Split(step.Path, "?")[0], "/"))
			device := tokens[strings.TrimSpace(strings.TrimPrefix(step.Headers["Authorization"], "Bearer "))]
			kept := got.Status == 400 && step.Status != 400
			if got.Status != 200 && !kept {
				return false
			}
			names := []string{"the alerts raised"}
			switch {
			case strings.HasSuffix(path, "/telemetry"):
				names = append(names, "the telemetry of "+device, "a time was stored")
			case strings.HasSuffix(path, "/inventory"):
				names = append(names, "the report of "+device)
			}
			for _, name := range names {
				refused[name] = kept
				stored[name] = stored[name] || !kept
			}
			return false
		}
		original, ok := statements[step.Name]
		if !ok {
			return false
		}
		have := w.Rows(original.SQL, original.Params)
		want := original.Rows
		switch {
		case !refused[original.Name]:
		case original.Name != "the alerts raised":
			want = last[original.Name]
		case !stored[original.Name]:
			// The alerts went after the statement before
			want = "[]"
		}
		if want != original.Rows {
			differ++
		}
		if have != want {
			t.Errorf("%s (AI15): %s\n  want %s (Node: %s)", step.Name, have, want, original.Rows)
		}
		last[original.Name] = have
		if original.Name == "the alerts raised" {
			refused[original.Name], stored[original.Name] = false, false
		}
		return true
	}, &differ
}
