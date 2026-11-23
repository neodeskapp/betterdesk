package registrations

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
	_ "time/tzdata" // the vectors were recorded in Europe/Berlin

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
)

// notificationVectors is testdata/notifications.json, recorded from
// services/registrationNotifications.js (testdata/README.md). A value
// {"$undefined": true} stands for undefined, {"$date": text} for a Date
// (the times of PostgreSQL rows).
type notificationVectors struct {
	Fixed      int64              `json:"fixed"`
	TZ         string             `json:"tz"`
	LAN        []notificationCase `json:"lan"`
	Enrollment []notificationCase `json:"enrollment"`
	List       []listCase         `json:"list"`
}

type notificationCase struct {
	Input json.RawMessage `json:"input"`
	Out   struct {
		Value     json.RawMessage `json:"value"`
		Undefined bool            `json:"undefined"`
		Error     string          `json:"error"`
	} `json:"out"`
}

type listCase struct {
	LAN     json.RawMessage `json:"lan"`
	API     json.RawMessage `json:"api"`
	Filters json.RawMessage `json:"filters"`
	Items   json.RawMessage `json:"items"`
	Count   int             `json:"count"`
}

func loadNotificationVectors(t *testing.T) notificationVectors {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "notifications.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v notificationVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// revive turns a value of the vector file into what the Go code gets: a
// Date becomes the ISO text this package reads PostgreSQL times as, and
// an undefined member is left out.
func revive(v any) (any, bool) {
	switch t := v.(type) {
	case *goapi.Object:
		if t.Len() == 1 && t.Get("$undefined") == true {
			return nil, false
		}
		if s, ok := t.Get("$date").(string); ok && t.Len() == 1 {
			return s, true
		}
		out := goapi.NewObject()
		for _, k := range t.Keys() {
			if x, ok := revive(t.Get(k)); ok {
				out.Set(k, x)
			}
		}
		return out, true
	case []any:
		out := make([]any, len(t))
		for i, x := range t {
			out[i], _ = revive(x)
		}
		return out, true
	}
	return v, true
}

func parseVector(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	v, err := goapi.Parse(raw)
	if err != nil {
		t.Fatalf("vector %s: %v", raw, err)
	}
	out, _ := revive(v)
	return out
}

func compact(t *testing.T, raw []byte) string {
	t.Helper()
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return b.String()
}

func vectorService(t *testing.T, v notificationVectors) *Service {
	t.Helper()
	loc, err := time.LoadLocation(v.TZ)
	if err != nil {
		t.Fatal(err)
	}
	fixed := time.UnixMilli(v.Fixed)
	return New(nil, nil).WithClock(func() time.Time { return fixed }, loc)
}

// TestLANNotificationVectors replays lanRegistrationToNotification for
// rows with times in every form and fields of every type.
func TestLANNotificationVectors(t *testing.T) {
	v := loadNotificationVectors(t)
	s := vectorService(t, v)
	for i, c := range v.LAN {
		input := parseVector(t, c.Input)
		want := "null"
		if len(c.Out.Value) > 0 {
			want = compact(t, c.Out.Value)
		}
		got := "null"
		if row, ok := input.(*goapi.Object); ok {
			if item, _ := s.lanNotification(row); item != nil {
				raw, err := httpx.Marshal(item)
				if err != nil {
					t.Fatal(err)
				}
				got = string(raw)
			}
		}
		if got != want {
			t.Errorf("LAN vector %d %s:\n  node: %s\n    go: %s", i, c.Input, want, got)
		}
	}
	if len(v.LAN) < 80 {
		t.Fatalf("only %d LAN vectors", len(v.LAN))
	}
}

// TestEnrollmentNotificationVectors replays enrollmentToNotification for
// the entries of the API server's queue.
func TestEnrollmentNotificationVectors(t *testing.T) {
	v := loadNotificationVectors(t)
	s := vectorService(t, v)
	for i, c := range v.Enrollment {
		input := parseVector(t, c.Input)
		want := "null"
		if len(c.Out.Value) > 0 {
			want = compact(t, c.Out.Value)
		}
		got := "null"
		if item, _ := s.enrollmentNotification(input); item != nil {
			raw, err := httpx.Marshal(item)
			if err != nil {
				t.Fatal(err)
			}
			got = string(raw)
		}
		if got != want {
			t.Errorf("enrollment vector %d %s:\n  node: %s\n    go: %s", i, c.Input, want, got)
		}
	}
	if len(v.Enrollment) < 80 {
		t.Fatalf("only %d enrollment vectors", len(v.Enrollment))
	}
}

// TestNotificationListVectors replays listRegistrationNotifications: both
// sources together, newest first, a failing source empty.
func TestNotificationListVectors(t *testing.T) {
	v := loadNotificationVectors(t)
	s := vectorService(t, v)
	for i, c := range v.List {
		var lan []*goapi.Object
		if rows, ok := parseVector(t, c.LAN).([]any); ok {
			for _, r := range rows {
				row, _ := r.(*goapi.Object)
				lan = append(lan, row)
			}
		}
		pending, _ := parseVector(t, c.API).(*goapi.Object)
		if len(c.Filters) > 0 {
			if f := compact(t, c.Filters); f != `{"status":"pending"}` {
				t.Errorf("list %d: Node asked for the rows with %s", i, f)
			}
		}
		items := s.notificationsOf(lan, pending)
		raw, err := httpx.Marshal(items)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := string(raw), compact(t, c.Items); got != want {
			t.Errorf("list %d:\n  node: %s\n    go: %s", i, want, got)
		}
		if len(items) != c.Count {
			t.Errorf("list %d: %d items, Node counted %d", i, len(items), c.Count)
		}
	}
}
