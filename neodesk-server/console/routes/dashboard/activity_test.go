package dashboard

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

const noEvents = `{"success":true,"events":[]}`

func TestActivityNeedsAuditView(t *testing.T) {
	w := newWorld(t, nil, nil)
	w.api.set("/api/audit/events", `{"events":[{"action":"x"}]}`)
	// guest_issuer has device.view but not audit.view
	want(t, "guest_issuer", w.get("guest_issuer", "/api/dashboard/activity"), 403, `{"success":false,"error":"Permission denied: audit.view"}`)
	want(t, "signed out", w.get("", "/api/dashboard/activity"), 401, signedOut)
	if calls := w.api.seen(); len(calls) != 0 {
		t.Errorf("refused requests asked the server for its audit log: %v", calls)
	}
	for _, role := range []string{"viewer", "operator", "global_admin", "server_admin", "super_admin", "admin"} {
		if rec := w.get(role, "/api/dashboard/activity"); rec.Code != 200 {
			t.Errorf("%s: %d %s", role, rec.Code, rec.Body.String())
		}
	}
}

func TestActivityListsTheServersEvents(t *testing.T) {
	w := newWorld(t, nil, nil)
	if _, err := w.db.Exec(context.Background(), "INSERT INTO audit_log (user_id, action, details) VALUES (1, 'local_only', 'panel log')"); err != nil {
		t.Fatal(err)
	}
	w.api.set("/api/audit/events", `{"enabled":true,"total":2,"count":2,"events":[`+
		`{"timestamp":"2026-11-09T09:58:00Z","action":"peer_banned","actor":"admin","target":"123456789","details":{"reason":"lost laptop"}},`+
		`{"timestamp":"2026-11-09T09:59:00Z","action":"config_changed","actor":"api","details":{"message":"relay changed","peer_id":"987654321"}}]}`)
	// The server answered, so the console's own log is not the source
	want(t, "events", w.get("admin", "/api/dashboard/activity"), 200, `{"success":true,"events":[`+
		`{"action":"peer_banned","action_label":"peer_banned","device_id":"123456789","details":"lost laptop","timestamp":"2026-11-09T09:58:00Z"},`+
		`{"action":"config_changed","action_label":"config_changed","device_id":"987654321","details":"relay changed","timestamp":"2026-11-09T09:59:00Z"}]}`)
	if calls := w.api.seen(); len(calls) != 1 || calls[0] != "GET /api/audit/events?limit=10" {
		t.Errorf("calls = %v", calls)
	}

	// The older form of the answer
	w.api.set("/api/audit/events", `{"entries":[{"action":"peer_deleted","peer_id":"555555555","details":{"message":"gone"},"created_at":"2026-11-08T08:00:00Z"}]}`)
	want(t, "entries", w.get("admin", "/api/dashboard/activity"), 200, `{"success":true,"events":[`+
		`{"action":"peer_deleted","action_label":"peer_deleted","device_id":"555555555","details":"gone","timestamp":"2026-11-08T08:00:00Z"}]}`)
}

func TestActivityEventFields(t *testing.T) {
	w := newWorld(t, nil, nil)
	for _, c := range []struct{ name, entry, event string }{
		{"nothing", `{}`, `{"action":"info","action_label":"Event","device_id":"","details":""}`},
		{"empty action", `{"action":""}`, `{"action":"info","action_label":"Event","device_id":"","details":""}`},
		{"peer_id first", `{"peer_id":"1","target":"3","details":{"peer_id":"2"}}`, `{"action":"info","action_label":"Event","device_id":"1","details":""}`},
		{"then the details' peer_id", `{"peer_id":"","target":"3","details":{"peer_id":"2"}}`, `{"action":"info","action_label":"Event","device_id":"2","details":""}`},
		{"then the target", `{"target":"3","details":{}}`, `{"action":"info","action_label":"Event","device_id":"3","details":""}`},
		{"message before reason", `{"details":{"message":"m","reason":"r"}}`, `{"action":"info","action_label":"Event","device_id":"","details":"m"}`},
		{"details as text", `{"details":"plain"}`, `{"action":"info","action_label":"Event","device_id":"","details":""}`},
		{"timestamp before created_at", `{"timestamp":"t","created_at":"c"}`, `{"action":"info","action_label":"Event","device_id":"","details":"","timestamp":"t"}`},
		{"empty timestamp", `{"timestamp":"","created_at":"c"}`, `{"action":"info","action_label":"Event","device_id":"","details":"","timestamp":"c"}`},
		{"created_at null", `{"timestamp":0,"created_at":null}`, `{"action":"info","action_label":"Event","device_id":"","details":"","timestamp":null}`},
		{"timestamp null alone", `{"timestamp":null}`, `{"action":"info","action_label":"Event","device_id":"","details":""}`},
		{"other types pass", `{"action":7,"target":[1],"details":{"message":{"a":1}},"timestamp":5}`, `{"action":7,"action_label":7,"device_id":[1],"details":{"a":1},"timestamp":5}`},
		{"an entry that is a number", `5`, `{"action":"info","action_label":"Event","device_id":"","details":""}`},
		{"an entry that is text", `"x"`, `{"action":"info","action_label":"Event","device_id":"","details":""}`},
	} {
		w.api.set("/api/audit/events", `{"events":[`+c.entry+`]}`)
		want(t, c.name, w.get("admin", "/api/dashboard/activity"), 200, `{"success":true,"events":[`+c.event+`]}`)
	}
}

func TestActivityWhichEntriesCount(t *testing.T) {
	w := newWorld(t, nil, nil)
	one := `{"action":"a","action_label":"a","device_id":"","details":""}`
	var twelve []string
	for i := 0; i < 12; i++ {
		twelve = append(twelve, fmt.Sprintf(`{"action":"a%d"}`, i))
	}
	for _, c := range []struct {
		name, server string
		// events is how many events the answer has
		events int
	}{
		{"ten of twelve", `{"events":[` + strings.Join(twelve, ",") + `]}`, 10},
		{"events before entries", `{"events":[{"action":"a"}],"entries":[{"action":"b"},{"action":"c"}]}`, 1},
		{"an empty events list is taken", `{"events":[],"entries":[{"action":"b"}]}`, 0},
		{"events null: entries", `{"events":null,"entries":[{"action":"a"}]}`, 1},
		{"events that is no list", `{"events":{"0":{"action":"a"}}}`, 0},
		// a null entry is skipped and a bare list is read (BUGS SI12: reading
		// stopped at the null, and a bare list gave no events)
		{"a null entry is skipped", `{"events":[{"action":"a"},null,{"action":"b"}]}`, 2},
		{"ten after a null", `{"events":[null,` + strings.Join(twelve, ",") + `]}`, 10},
		{"a bare list is read", `[{"action":"a"},null,{"action":"b"}]`, 2},
		{"text", `"events"`, 0},
		{"nothing", `{}`, 0},
	} {
		w.api.set("/api/audit/events", c.server)
		body := w.get("admin", "/api/dashboard/activity").Body.String()
		if got := strings.Count(body, `"action":`); got != c.events {
			t.Errorf("%s: %d events, want %d: %s", c.name, got, c.events, body)
		}
		if c.events == 0 && body != noEvents {
			t.Errorf("%s: %s", c.name, body)
		}
		if c.name == "a null entry is skipped" && body != `{"success":true,"events":[`+one+`,`+strings.ReplaceAll(one, `"a"`, `"b"`)+`]}` {
			t.Errorf("%s: %s", c.name, body)
		}
		if c.name == "ten after a null" && (!strings.Contains(body, `"a9"`) || strings.Contains(body, `"a10"`)) {
			t.Errorf("%s: %s", c.name, body)
		}
	}
	w.api.set("/api/audit/events", `{"events":[`+strings.Join(twelve, ",")+`]}`)
	if body := w.get("admin", "/api/dashboard/activity").Body.String(); !strings.Contains(body, `"a0"`) || !strings.Contains(body, `"a9"`) || strings.Contains(body, `"a10"`) {
		t.Errorf("the first ten of twelve: %s", body)
	}
}

func TestActivityFallsBackOnTheConsolesAuditLog(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := newWorld(t, db, nil)
		ctx := context.Background()
		// Without events from the server and with an empty log there is
		// nothing to show
		want(t, "nothing anywhere", w.get("admin", "/api/dashboard/activity"), 200, noEvents)

		stamp := func(minute int) string {
			return fmt.Sprintf("2026-11-09 10:%02d:00", minute)
		}
		insert := "INSERT INTO audit_log (user_id, action, details, ip_address, created_at) VALUES (?, ?, ?, ?, ?)"
		rows := []struct {
			user            any
			action          string
			details, ip     any
			minute          int
			wantDetails     string
			wantActionLabel string
		}{
			{1, "login", "Signed in", "203.0.113.7", 1, "Signed in", "login"},
			{nil, "login_failed", nil, "203.0.113.8", 2, "203.0.113.8", "login_failed"},
			{1, "note", "", "203.0.113.9", 3, "203.0.113.9", "note"},
			{1, "quiet", nil, nil, 4, "", "quiet"},
			{1, "", "no action", nil, 5, "no action", "Event"},
		}
		for _, row := range rows {
			if _, err := db.Exec(ctx, insert, row.user, row.action, row.details, row.ip, stamp(row.minute)); err != nil {
				t.Fatal(err)
			}
		}
		created := func(minute int) string {
			if db.IsPostgres() {
				return fmt.Sprintf("2026-11-09T10:%02d:00.000Z", minute)
			}
			return stamp(minute)
		}
		var events []string
		for i := len(rows) - 1; i >= 0; i-- {
			row := rows[i]
			action := row.action
			if action == "" {
				action = "info"
			}
			events = append(events, fmt.Sprintf(`{"action":%q,"action_label":%q,"device_id":"","details":%q,"timestamp":%q}`,
				action, row.wantActionLabel, row.wantDetails, created(row.minute)))
		}
		for _, server := range []string{`{"events":[]}`, `{}`} {
			w.api.set("/api/audit/events", server)
			want(t, "newest first, server says "+server, w.get("admin", "/api/dashboard/activity"), 200, `{"success":true,"events":[`+strings.Join(events, ",")+`]}`)
		}
		w.api.setStatus("/api/audit/events", 500, `{"error":"down"}`)
		want(t, "newest first, server down", w.get("admin", "/api/dashboard/activity"), 200, `{"success":true,"events":[`+strings.Join(events, ",")+`]}`)

		// Ten at most, and an entry without a time has no timestamp
		for minute := 10; minute < 20; minute++ {
			if _, err := db.Exec(ctx, insert, 1, "more", "x", nil, stamp(minute)); err != nil {
				t.Fatal(err)
			}
		}
		body := w.get("admin", "/api/dashboard/activity").Body.String()
		if strings.Count(body, `"action":`) != 10 || strings.Contains(body, `"login"`) || !strings.Contains(body, created(19)) || !strings.Contains(body, created(10)) {
			t.Errorf("ten newest: %s", body)
		}

		// The audit log cannot be read: an empty feed, not an error
		if _, err := db.Exec(ctx, "ALTER TABLE audit_log RENAME TO audit_log_gone"); err != nil {
			t.Fatal(err)
		}
		want(t, "no audit log", w.get("admin", "/api/dashboard/activity"), 200, noEvents)
	})
}

func TestActivityEntryWithoutATime(t *testing.T) {
	w := newWorld(t, nil, nil)
	if _, err := w.db.Exec(context.Background(), "INSERT INTO audit_log (action, created_at) VALUES ('old', NULL)"); err != nil {
		t.Fatal(err)
	}
	want(t, "no time", w.get("admin", "/api/dashboard/activity"), 200,
		`{"success":true,"events":[{"action":"old","action_label":"old","device_id":"","details":""}]}`)
}
