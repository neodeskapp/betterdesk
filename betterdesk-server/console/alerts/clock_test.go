package alerts

import (
	"context"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// The time of an alert is the database's, the clock its cooldown is
// checked against. An engine whose clock is a day behind stores and mails
// the database's time, and the rule stays quiet for its cooldown; with
// the engine's time the alert lay a day back and every report raised
// another (BUGS AE6).
func TestTheAlertTimeIsTheDatabasesClock(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		addRule(t, db, "cpu_usage", "gt", 80, map[string]any{"cooldown_secs": 600, "notify_emails": "ops@example.com"})
		mailer := &fakeMailer{}
		engine := New(Options{DB: db, Mail: mailer, Logger: &logLines{}, Now: func() time.Time { return time.Now().Add(-24 * time.Hour) }})
		for range 2 {
			if err := engine.EvaluateRules(ctx, jsbind.String("dev-1"), "cpu_usage", jsbind.Number(95), EvalOptions{}); err != nil {
				t.Fatal(err)
			}
		}
		engine.Wait()
		if got := alertsIn(t, db); len(got) != 1 {
			t.Fatalf("%d alerts for two reports inside the cooldown", len(got))
		}
		rows, err := store.Select(ctx, db, "SELECT triggered_at FROM alert_history")
		if err != nil {
			t.Fatal(err)
		}
		var stored store.Time
		for rows.Next() {
			if err := rows.Scan(&stored); err != nil {
				t.Fatal(err)
			}
		}
		rows.Close()
		at, ok := stored.Instant()
		if !ok || time.Since(at) > time.Minute || time.Since(at) < -time.Minute {
			t.Fatalf("the alert is stored at %s, not at the database's time", stored.String())
		}
		if len(mailer.sent) != 1 || mailer.sent[0].alert.TriggeredAt.Str() != store.ISOTime(at) {
			t.Fatalf("the mail says %v, the alert was stored at %s", mailer.sent, store.ISOTime(at))
		}
	})

	// A store whose clock differs from the engine's: the mail carries the
	// store's time
	mem := &memStore{t: t, c: &memCase{Rules: []map[string]string{{"id": "1", "name": `"Rule"`, "condition_op": `"gt"`, "condition_value": "1", "notify_emails": `"ops@example.com"`}}}}
	mailer := &fakeMailer{}
	engine := New(Options{Mail: mailer, Logger: &logLines{}, Now: func() time.Time { return time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC) }})
	engine.store = mem
	if err := engine.EvaluateRules(context.Background(), jsbind.String("dev-1"), "cpu_usage", jsbind.Number(5), EvalOptions{}); err != nil {
		t.Fatal(err)
	}
	engine.Wait()
	if len(mailer.sent) != 1 || mailer.sent[0].alert.TriggeredAt.Str() != memStoreTime {
		t.Fatalf("the mail says %v, the store's time is %s", mailer.sent, memStoreTime)
	}
}
