package helpmail

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/devicegroups"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/mail"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
	"github.com/unitronix/betterdesk-server/events"
)

// The vectors are a fixed recording of the Node console
// (testdata/helpmail.json).
type oracle struct {
	Configs [][2]json.RawMessage `json:"configs"`
	Runs    []struct {
		Stored   *string `json:"stored"`
		Scenario struct {
			Name      string                       `json:"name"`
			Operators []devicegroups.OperatorEmail `json:"operators"`
			Alert     string                       `json:"alert"`
			Folder    string                       `json:"folder"`
			Sent      []string                     `json:"sent"`
			Refuse    []string                     `json:"refuse"`
			Throws    bool                         `json:"folderThrows"`
		} `json:"scenario"`
		Event  map[string]string   `json:"event"`
		Thrown *string             `json:"thrown"`
		Calls  [][]json.RawMessage `json:"calls"`
		Log    [][2]string         `json:"log"`
	} `json:"runs"`
}

func loadOracle(t *testing.T) oracle {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "helpmail.json"))
	if err != nil {
		t.Fatal(err)
	}
	var o oracle
	if err := json.Unmarshal(data, &o); err != nil {
		t.Fatal(err)
	}
	return o
}

// TestConfigVectors replays parseCommercializationEmailConfig on stored
// values of every kind.
func TestConfigVectors(t *testing.T) {
	o := loadOracle(t)
	for _, c := range o.Configs {
		var stored *string
		_ = json.Unmarshal(c[0], &stored)
		raw, ok := "", stored != nil
		if ok {
			raw = *stored
		}
		got, _ := json.Marshal(ParseConfig(raw, ok))
		if !sameJSON(got, c[1]) {
			t.Errorf("ParseConfig(%s) = %s, Node %s", c[0], got, c[1])
		}
	}
}

func sameJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// recorder is the mail service and the folder lookups of a run, as the
// vector scripts them; it writes down every call as the oracle did.
type recorder struct {
	mu    sync.Mutex
	calls []any
	alert string
	sent  []string
	fail  []string
}

func (r *recorder) note(call ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
}

func (r *recorder) GetAlertEmail(context.Context) (string, error) {
	r.note("alert")
	return r.alert, nil
}

func (r *recorder) SendHelpRequestEmail(_ context.Context, m mail.HelpRequestEmail) (bool, error) {
	r.note("send", map[string]any{
		"to": strings.Join(m.To, ","),
		"helpRequest": map[string]any{
			"device_id": m.HelpRequest.DeviceID.Str(), "hostname": m.HelpRequest.Hostname.Str(), "message": m.HelpRequest.Message.Str(),
		},
		"folderName": m.FolderName.Str(),
	})
	for _, refused := range r.fail {
		if refused == m.To[0] {
			return false, nil
		}
	}
	return true, nil
}

func (r *recorder) HasEmailNotificationSent(_ context.Context, kind, id, to string) (bool, error) {
	r.note("has", kind, id, to)
	for _, s := range r.sent {
		if s == to {
			return true, nil
		}
	}
	return false, nil
}

func (r *recorder) LogEmailNotificationSent(_ context.Context, kind, id, to string) error {
	r.note("log", kind, id, to)
	return nil
}

// lines takes the log lines.
type lines struct {
	mu  sync.Mutex
	got [][2]string
}

func (l *lines) add(level string, args []any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.got = append(l.got, [2]string{level, "[HelpRequestEmail] " + fmt.Sprint(args...)})
}
func (l *lines) Info(args ...any)  { l.add("log", args) }
func (l *lines) Warn(args ...any)  { l.add("warn", args) }
func (l *lines) Error(args ...any) { l.add("error", args) }

// nodeCalls is the calls of a vector without the read of the settings,
// with the help request's id left out of the mail (the mail never shows
// it).
func nodeCalls(t *testing.T, calls [][]json.RawMessage) []any {
	t.Helper()
	var out []any
	for _, call := range calls {
		var name string
		_ = json.Unmarshal(call[0], &name)
		if name == "getSetting" {
			continue
		}
		entry := []any{name}
		for _, arg := range call[1:] {
			var v any
			_ = json.Unmarshal(arg, &v)
			if m, ok := v.(map[string]any); ok && name == "send" {
				delete(m["helpRequest"].(map[string]any), "id")
			}
			entry = append(entry, v)
		}
		out = append(out, entry)
	}
	return out
}

// TestEventVectors replays handleHelpRequestEvent: the stored
// configuration, what the folder lookups and the mail service answer,
// and the event; the calls in their order, what failed, and the log.
func TestEventVectors(t *testing.T) {
	o := loadOracle(t)
	if len(o.Runs) < 1000 {
		t.Fatalf("only %d runs", len(o.Runs))
	}
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		for i, run := range o.Runs {
			if _, err := db.Exec(ctx, "DELETE FROM settings WHERE key = ?", ConfigKey); err != nil {
				t.Fatal(err)
			}
			if run.Stored != nil {
				if err := setSetting(ctx, db, ConfigKey, *run.Stored); err != nil {
					t.Fatal(err)
				}
			}
			sc := run.Scenario
			rec := &recorder{alert: sc.Alert, sent: sc.Sent, fail: sc.Refuse}
			log := &lines{}
			s := New(Options{
				DB: db, Mail: rec, Logger: log,
				FolderName: func(_ context.Context, id string) (string, error) {
					rec.note("folder", id)
					if sc.Throws {
						return "", devicegroups.ErrAssignmentsNotIterable
					}
					return sc.Folder, nil
				},
				OperatorEmails: func(_ context.Context, id string) ([]devicegroups.OperatorEmail, error) {
					rec.note("operators", id)
					return sc.Operators, nil
				},
			})
			data := goapi.NewObject()
			for _, key := range []string{"id", "request_id", "device_id", "hostname", "message", "status", "handled_by"} {
				if v, ok := run.Event[key]; ok {
					data.Set(key, v)
				}
			}
			err := s.HandleEvent(ctx, data)
			what := fmt.Sprintf("run %d (%s, stored %v, event %v)", i, sc.Name, describe(run.Stored), run.Event)
			if status := run.Event["status"]; status != "" && status != "pending" {
				// G18: a status change is no new request and sends
				// nothing, where Node mailed it as one when the request
				// had not been mailed
				if err != nil || len(rec.calls) != 0 || len(log.got) != 0 {
					t.Errorf("%s (G18): error %v, calls %v, log %q, want nothing", what, err, rec.calls, log.got)
				}
				continue
			}
			switch {
			case run.Thrown == nil && err != nil:
				t.Errorf("%s: error %v, Node none", what, err)
			case run.Thrown != nil && (err == nil || err.Error() != *run.Thrown):
				t.Errorf("%s: error %v, Node %q", what, err, *run.Thrown)
			}
			got, _ := json.Marshal(rec.calls)
			want, _ := json.Marshal(nodeCalls(t, run.Calls))
			if !sameJSON(got, want) {
				t.Errorf("%s: calls\n got %s\nNode %s", what, got, want)
			}
			if !reflect.DeepEqual(log.got, run.Log) && (len(log.got) > 0 || len(run.Log) > 0) {
				t.Errorf("%s: log %q, Node %q", what, log.got, run.Log)
			}
		}
	})
}

func describe(s *string) string {
	if s == nil {
		return "none"
	}
	return *s
}

// transport is a mail server that keeps what it was given.
type transport struct {
	mu   sync.Mutex
	sent []mail.Outgoing
}

func (t *transport) Send(_ context.Context, _ mail.Settings, m mail.Outgoing) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sent = append(t.sent, m)
	return nil
}

func (t *transport) Verify(context.Context, mail.Settings) error { return nil }

func (t *transport) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.sent)
}

// TestRunMailsEachRequestOnce sends help request events over a real event
// bus to the job with the real mail service: the alert address gets one
// mail per request, a repeated event and the status change of a mailed
// request send nothing more, and the job ends with its context. The
// folder lookups are stubs, as in the Node tests; the real ones (BUGS
// PA8) have a test of their own.
func TestRunMailsEachRequestOnce(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx, cancel := context.WithCancel(context.Background())
		if err := setSetting(ctx, db, "smtp_config", `{"host":"127.0.0.1","port":1,"alert_email":" ops@example.com "}`); err != nil {
			t.Fatal(err)
		}
		sink := &transport{}
		mailer := mail.New(mail.Options{DB: db, Transport: sink, Logger: &lines{}})
		bus := events.NewBus()
		api := goapi.New(nil, goapi.WithEventBus(bus))
		s := New(Options{
			DB: db, Mail: mailer, Events: api.Events, Logger: &lines{},
			FolderName:     func(context.Context, string) (string, error) { return "Support", nil },
			OperatorEmails: func(context.Context, string) ([]devicegroups.OperatorEmail, error) { return nil, nil },
		})
		done := make(chan struct{})
		go func() {
			s.Run(ctx)
			close(done)
		}()
		waitFor(t, func() bool { return bus.Count() == 1 })

		publish := func(data map[string]string) {
			bus.Publish(events.Event{Type: events.EventHelpRequest, Data: data})
		}
		publish(map[string]string{"id": "7", "device_id": "900100001", "hostname": "reception", "message": "Printer"})
		waitFor(t, func() bool {
			sent, _ := mailer.HasEmailNotificationSent(context.Background(), "help_request", "7", "ops@example.com")
			return sent
		})
		publish(map[string]string{"id": "7", "device_id": "900100001", "hostname": "reception", "message": "Printer"})
		publish(map[string]string{"id": "7", "device_id": "900100001", "status": "acknowledged"})
		bus.Publish(events.Event{Type: events.EventPeerOnline, Data: map[string]string{"id": "8", "device_id": "x"}})
		publish(map[string]string{"id": "9", "device_id": "900100002"})
		waitFor(t, func() bool { return sink.count() == 2 })
		time.Sleep(100 * time.Millisecond)
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("the job did not end with its context")
		}
		if n := sink.count(); n != 2 {
			t.Fatalf("%d mails, want 2", n)
		}
		first := sink.sent[0]
		if strings.Join(first.To, ",") != "ops@example.com" || first.Subject != "[Support] Help request — reception" {
			t.Errorf("first mail to %v, subject %q", first.To, first.Subject)
		}
		if sent, err := mailer.HasEmailNotificationSent(context.Background(), "help_request", "9", "ops@example.com"); err != nil || !sent {
			t.Errorf("the second request is not in the notification log: %v %v", sent, err)
		}
		if bus.Count() != 0 {
			t.Errorf("the job left %d subscriptions on the bus", bus.Count())
		}
	})
}

// TestStatusChangeSendsNoMail: the API server publishes a status change
// of a help request as help_request too; it is no new request and sends
// no mail, also when the request itself was never mailed (BUGS.md G18).
// A new request comes with the status pending or none.
func TestStatusChangeSendsNoMail(t *testing.T) {
	db := storetest.Migrated(t)
	rec := &recorder{alert: "ops@example.com"}
	s := New(Options{
		DB: db, Mail: rec, Logger: &lines{},
		FolderName:     func(context.Context, string) (string, error) { return "", nil },
		OperatorEmails: func(context.Context, string) ([]devicegroups.OperatorEmail, error) { return nil, nil },
	})
	for _, status := range []string{"acknowledged", "resolved", "cancelled"} {
		if err := s.HandleEvent(context.Background(), goapi.NewObject("id", "1", "device_id", "dev", "status", status, "handled_by", "admin")); err != nil || len(rec.calls) != 0 {
			t.Errorf("status %s: error %v, calls %v", status, err, rec.calls)
		}
	}
	for _, status := range []string{"pending", ""} {
		rec.calls = nil
		if err := s.HandleEvent(context.Background(), goapi.NewObject("id", "2", "device_id", "dev", "status", status)); err != nil || !strings.Contains(fmt.Sprint(rec.calls), "send") {
			t.Errorf("a new request with status %q: error %v, calls %v", status, err, rec.calls)
		}
	}
}

// TestRunWithoutBusEnds pins that the job ends at once without a bus.
func TestRunWithoutBusEnds(t *testing.T) {
	log := &lines{}
	s := New(Options{Events: goapi.New(nil).Events, Logger: log})
	s.Run(context.Background())
	if len(log.got) != 1 || log.got[0][0] != "warn" {
		t.Errorf("log %q", log.got)
	}
}

// TestRealLookupsFindTheFolderAndItsOperators: with the folder lookups of
// package devicegroups a request of a device in a folder goes to the
// operators on the folder's access list, with the folder in the subject,
// and not to the alert address (PA8).
func TestRealLookupsFindTheFolderAndItsOperators(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	for _, query := range []string{
		`INSERT INTO users (id, username, password_hash, role, email) VALUES (40, 'alice', 'x', 'operator', 'alice@example.org')`,
		`INSERT INTO folders (id, name, color, icon, sort_order) VALUES (7, 'Büro', '#ffffff', 'folder', 0)`,
		`INSERT INTO device_folder_assignments (device_id, folder_id) VALUES ('dev', 7)`,
		`INSERT INTO device_groups (guid, name, note, team_id, source_type, tag_filter) VALUES ('folder_7', 'Büro', '', '', 'manual', '')`,
		`INSERT INTO device_group_user_access (device_group_id, user_id) SELECT id, 40 FROM device_groups WHERE guid = 'folder_7'`,
	} {
		if _, err := db.Exec(ctx, query); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	rec := &recorder{alert: "ops@example.com"}
	s := New(Options{DB: db, Mail: rec, Logger: &lines{}})
	if err := s.HandleEvent(ctx, goapi.NewObject("id", "1", "device_id", "dev")); err != nil {
		t.Fatal(err)
	}
	want := []any{
		[]any{"has", "help_request", "1", "alice@example.org"},
		[]any{"send", map[string]any{
			"to":          "alice@example.org",
			"helpRequest": map[string]any{"device_id": "dev", "hostname": "dev", "message": ""},
			"folderName":  "Büro",
		}},
		[]any{"log", "help_request", "1", "alice@example.org"},
	}
	if !reflect.DeepEqual(rec.calls, want) {
		t.Errorf("calls %v", rec.calls)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestRunSkipsOtherEvents feeds the job a frame of another type, as the
// websocket of the old service could deliver one: it is not handled.
func TestRunSkipsOtherEvents(t *testing.T) {
	db := storetest.Migrated(t)
	rec := &recorder{alert: "ops@example.com"}
	frames := make(chan *goapi.Object, 2)
	frames <- goapi.NewObject("type", "peer_online", "data", goapi.NewObject("id", "1", "device_id", "dev"))
	frames <- goapi.NewObject("type", "help_request", "data", goapi.NewObject("id", "2", "device_id", "dev"))
	close(frames)
	s := New(Options{
		DB: db, Mail: rec, Logger: &lines{},
		Events:         func(context.Context, string) (<-chan *goapi.Object, error) { return frames, nil },
		FolderName:     func(context.Context, string) (string, error) { return "", nil },
		OperatorEmails: func(context.Context, string) ([]devicegroups.OperatorEmail, error) { return nil, nil },
	})
	s.Run(context.Background())
	got, _ := json.Marshal(rec.calls)
	if strings.Count(string(got), `"has"`) != 1 || !strings.Contains(string(got), `"has","help_request","2"`) {
		t.Errorf("calls %s", got)
	}
}
