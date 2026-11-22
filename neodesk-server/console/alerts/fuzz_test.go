package alerts

import (
	"context"
	"math"
	"regexp"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
)

var strictISO = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,9})?(Z|[+-]\d{2}:\d{2})$`)

var berlin = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		return time.FixedZone("CET", 3600)
	}
	return loc
}()

// FuzzDateParse reads any text as a device's last_online.
func FuzzDateParse(f *testing.F) {
	for _, seed := range []string{
		"2026-11-09T10:00:00Z", "2026-11-09T10:00:00.123456789+01:00", "0001-01-01T00:00:00Z", "2026-11-09 10:00:00", "2026-11-09", "",
		"Mon, 09 Nov 2026 10:00:00 GMT", "Nov 9 2026 10:00 PM PST", "11/9/26", "12", "1700000000000", "+275760-09-13T00:00:00.000Z",
		"-271821-04-20T00:00:00Z", "2026-11-09T24:00:00", "9 Nov 2026 10:: GMT+0130 (x (y))", "1 Jan 2000 00:00:00 GMT+1200000:", "2021-03-28T02:30:00",
		"2026-11-09T10:00:00\x00junk", "x9 Nov 2026", "Nov 9 2026", "１２", "2026-11-09T10:00:00.5-00:00", "((((", "10:00:00.123456789012",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		ms, ok := dateParse(text, time.UTC)
		if ok && (math.Abs(ms) > maxTime || ms != math.Trunc(ms) || math.IsNaN(ms)) {
			t.Fatalf("Date.parse(%q) = %v", text, ms)
		}
		// Go's own reading of the strict ISO form agrees
		if strictISO.MatchString(text) {
			if want, err := time.Parse(time.RFC3339Nano, text); err == nil {
				if !ok || ms != float64(want.UnixMilli()) {
					t.Fatalf("Date.parse(%q) = %v %v, time.Parse: %d", text, ms, ok, want.UnixMilli())
				}
			}
		}
		// Another zone changes the answer only by its offset, and not at
		// all for a text that names its own zone or is a date alone.
		there, okThere := dateParse(text, berlin)
		if okThere != ok {
			// Possible only at the very edge of the range of dates
			if valid := ms + there; math.Abs(valid) < maxTime-msPerDay {
				t.Fatalf("Date.parse(%q): %v %v in UTC, %v %v in Berlin", text, ms, ok, there, okThere)
			}
			return
		}
		if ok && ms != there && math.Abs(ms-there) > 3*3600*1000 {
			t.Fatalf("Date.parse(%q): %v in UTC, %v in Berlin", text, ms, there)
		}
		if ok && strictISO.MatchString(text) && ms != there {
			t.Fatalf("Date.parse(%q) depends on the zone: %v, %v", text, ms, there)
		}
	})
}

// FuzzDeviceList runs the offline sweep over any device list the API
// could hand out.
func FuzzDeviceList(f *testing.F) {
	for _, seed := range []string{
		`[{"id":"dev-1","online":false,"last_online":"2026-11-09T10:00:00Z"}]`,
		`[{"id":"a","online":true,"last_online":"2026-11-09T10:00:00Z"},{"id":"b","banned":1,"last_online":"2026-11-09T10:00:00Z"},{"id":"c","soft_deleted":"x","last_online":"2026-11-09"}]`,
		`[null,0,"x",[],{},{"id":""},{"id":0},{"id":123,"last_online":1700000000000},{"id":true,"last_online":"Nov 9 2026"}]`,
		`[{"id":"t","remote_target":true,"last_online":"2026-11-09T10:00:00Z"},{"id":"z","last_online":"0001-01-01T00:00:00Z"}]`,
		`[{"id":["a"],"last_online":"2026-11-09T10:00:00Z"},{"id":{"toString":1},"last_online":"2026-11-09T10:00:00Z"},{"id":"o","last_online":{"toString":1}}]`,
		`[{"id":"n","online":null,"banned":0,"soft_deleted":"","remote_target":[],"last_online":["2026-11-09T10:00:00Z"]}]`,
		`{"not":"a list"}`, `[{"id":"f","last_online":"2026-11-09T12:00:01Z"}]`, `[{"id":"e","last_online":"2026-11-09T11:59:59.001Z"}]`,
	} {
		f.Add(seed, int64(1794225600000))
	}
	f.Fuzz(func(t *testing.T, list string, nowMs int64) {
		parsed, err := goapi.Parse([]byte(list))
		if err != nil {
			return
		}
		items, isList := parsed.([]any)
		if !isList {
			return
		}
		if nowMs > 8.64e15 || nowMs < -8.64e15 {
			return
		}
		devices := make([]*goapi.Object, len(items))
		for i, item := range items {
			devices[i], _ = item.(*goapi.Object)
		}
		mem := &memStore{t: t, c: &memCase{Rules: []map[string]string{{"id": "1", "name": `"Rule"`, "condition_op": `"gte"`, "condition_value": "0", "notify_emails": `""`}}}}
		logs := &logLines{}
		engine := New(Options{Logger: logs, Location: time.UTC, Devices: listOf(devices)})
		engine.store = mem
		err = engine.PeriodicCheckAt(context.Background(), time.UnixMilli(nowMs))
		engine.Wait()
		if err != nil && err != errNotPrimitive {
			t.Fatalf("%s: %v", list, err)
		}

		// What the sweep has to have looked at, read off the list again
		var want [][3]string
		for _, d := range devices {
			if d == nil {
				continue
			}
			flagged := false
			for _, flag := range []string{"online", "banned", "soft_deleted", "remote_target"} {
				switch v := d.Get(flag).(type) {
				case nil:
				case bool:
					flagged = flagged || v
				case float64:
					flagged = flagged || (v != 0 && !math.IsNaN(v))
				case string:
					flagged = flagged || v != ""
				default:
					flagged = true
				}
			}
			var id jsbind.Value
			switch v := d.Get("id").(type) {
			case string:
				id = jsbind.String(v)
			case float64:
				id = jsbind.Number(v)
			case bool:
				id = jsbind.Bool(v)
			case nil:
				continue
			default:
				id = jsbind.Undefined // a list or an object
			}
			if flagged || (!id.IsUndefined() && !id.Truthy()) {
				continue
			}
			last := d.Get("last_online")
			if !goapi.Truthy(last) {
				continue
			}
			text, textErr := anyString(last)
			if textErr != nil {
				// The sweep ends here
				if err != errNotPrimitive {
					t.Fatalf("%s: the sweep got past a last_online it cannot read", list)
				}
				break
			}
			ms, ok := dateParse(text, time.UTC)
			// A time before 1970 is never seen (BUGS AE1)
			if !ok || ms < 0 {
				continue
			}
			secs := math.Floor((float64(nowMs) - ms) / 1000)
			if secs <= 0 || id.IsUndefined() {
				continue
			}
			want = append(want, [3]string{"1", dumpBind(id), dumpBind(jsbind.Number(math.Max(secs, DefaultCooldownSecs)))})
		}
		if len(mem.recent) != len(want) {
			t.Fatalf("%s at %d: checked %q, want %q", list, nowMs, mem.recent, want)
		}
		for i := range want {
			if mem.recent[i] != want[i] {
				t.Fatalf("%s at %d: checked %q, want %q", list, nowMs, mem.recent, want)
			}
		}
		if len(mem.created) != len(want) {
			t.Fatalf("%s: %d alerts for %d devices", list, len(mem.created), len(want))
		}
	})
}
