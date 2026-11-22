package alerts

import (
	"context"
	"maps"
	"math"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// oddRules names the rules of a recorded case that the engine reads
// otherwise than Node did (BUGS AE3): badLimit the ones whose limit is no
// finite number, badCooldown the ones whose cooldown is set and no whole
// number above 0. fields are a rule's columns as the recordings dump them;
// a value JavaScript cannot make a number of failed in both engines alike.
func oddRules(t testing.TB, rules []map[string]string) (badLimit, badCooldown map[string]bool) {
	badLimit, badCooldown = map[string]bool{}, map[string]bool{}
	for _, fields := range rules {
		id := dumpBind(bindValue(t, fields["id"]))
		if limit, err := jsNumber(bindValue(t, fields["condition_value"])); err == nil && (math.IsNaN(limit) || math.IsInf(limit, 0)) {
			badLimit[id] = true
		}
		cooldown := bindValue(t, fields["cooldown_secs"])
		if secs, err := jsNumber(cooldown); err == nil && cooldown.Truthy() && !(secs > 0 && secs == math.Trunc(secs) && !math.IsInf(secs, 0)) {
			badCooldown[id] = true
		}
	}
	return badLimit, badCooldown
}

// expectAE3 turns what Node recorded for a case into what the Go console
// does instead (BUGS AE3), given the cooldown checks the Go console made:
// the rules whose limit is no finite number are not checked, so their
// cooldown checks, alerts, mails and log lines go; a cooldown check of a
// rule whose cooldown is no whole number above 0 carries the cooldown the
// Go console asked with: 300 seconds, or the case's shortest cooldown when
// that is longer (Math.max, so NaN for NaN). In the sweep the shortest
// cooldown is the time offline, and the cooldown has to be a whole number
// of at least 300 seconds. It returns what is wrong with a check, or "".
func expectAE3(t testing.TB, c *memCase, recent [][3]string, badLimit, badCooldown map[string]bool) string {
	calls := c.RecentCalls[:0:0]
	for _, call := range c.RecentCalls {
		if !badLimit[call[0]] {
			calls = append(calls, call)
		}
	}
	shortest := 0.0
	if c.Min != "" && c.Min != "undefined" {
		shortest = bindValue(t, c.Min).ToNumber()
	}
	for k := range calls {
		if !badCooldown[calls[k][0]] || k >= len(recent) || recent[k][0] != calls[k][0] || recent[k][1] != calls[k][1] {
			continue
		}
		secs, err := strconv.ParseFloat(recent[k][2], 64)
		switch {
		case c.Fn == "sweep" && (err != nil || secs < DefaultCooldownSecs || secs != math.Trunc(secs)),
			c.Fn != "sweep" && recent[k][2] != dumpBind(jsbind.Number(jsMax(DefaultCooldownSecs, shortest))):
			return "rule " + recent[k][0] + " checked with the cooldown " + recent[k][2]
		}
		calls[k][2] = recent[k][2]
	}
	c.RecentCalls = calls

	created := c.Created[:0:0]
	removedAlerts := 0
	for _, alert := range c.Created {
		if id, _, _ := strings.Cut(strings.TrimPrefix(alert.Alert, `{"rule_id":`), ","); badLimit[id] {
			removedAlerts++
			continue
		}
		created = append(created, alert)
	}
	c.Created = created

	removedMails := 0
	for _, fields := range c.Rules {
		if !badLimit[dumpBind(bindValue(t, fields["id"]))] {
			continue
		}
		name, op, limit := dumpBind(bindValue(t, fields["name"])), dumpBind(bindValue(t, fields["condition_op"])), dumpBind(bindValue(t, fields["condition_value"]))
		mails := c.Mails[:0:0]
		for _, m := range c.Mails {
			if m.Rule["name"] == name && m.Rule["condition_op"] == op && m.Rule["condition_value"] == limit {
				removedMails++
				continue
			}
			mails = append(mails, m)
		}
		c.Mails = mails
		var text texts
		marker := " (" + text.of(bindValue(t, fields["condition_op"])) + " " + text.of(bindValue(t, fields["condition_value"])) + ") for "
		logs := c.Logs[:0:0]
		for _, line := range c.Logs {
			if strings.HasPrefix(line[1], "[AlertEngine] ALERT fired: ") && strings.Contains(line[1], marker) {
				continue
			}
			logs = append(logs, line)
		}
		c.Logs = logs
	}
	// A mail that failed logged a line of its own, and so did an alert
	// whose device id could not be made text
	logs := c.Logs[:0:0]
	for _, line := range c.Logs {
		switch {
		case removedMails > 0 && strings.HasPrefix(line[1], "[AlertEngine] Email failed:"):
			removedMails--
		case removedAlerts > 0 && strings.HasPrefix(line[1], "[AlertEngine] Failed to create alert:"):
			removedAlerts--
		default:
			logs = append(logs, line)
		}
	}
	c.Logs = logs
	return ""
}

// subsequence reports whether got is want with some items left out.
func subsequence(got, want []string) bool {
	k := 0
	for _, item := range want {
		if k < len(got) && got[k] == item {
			k++
		}
	}
	return k == len(got)
}

// Reports of one device that arrive together raise one alert and one mail
// per rule: the cooldown's check and the alert's insert are one step, so
// the reports do not all find no recent alert (BUGS RV34). Another device
// and another rule still get their own alert.
func TestReportsAtTheSameTimeRaiseOneAlert(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		cpu := addRule(t, db, "cpu_usage", "gt", 80, map[string]any{"cooldown_secs": 300, "notify_emails": "ops@example.com"})
		other := addRule(t, db, "cpu_usage", "gt", 50, map[string]any{"cooldown_secs": 300})
		mailer := &fakeMailer{}
		engine := New(Options{DB: db, Mail: mailer, Logger: &logLines{}})
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := range 20 {
			device := "dev-1"
			if i == 0 {
				device = "dev-2"
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if err := engine.EvaluateRules(ctx, jsbind.String(device), "cpu_usage", jsbind.Number(95), EvalOptions{}); err != nil {
					t.Error(err)
				}
			}()
		}
		close(start)
		wg.Wait()
		engine.Wait()
		got := map[string]int{}
		for _, a := range alertsIn(t, db) {
			got[strconv.FormatInt(a.ruleID, 10)+" "+a.deviceID]++
		}
		want := map[string]int{
			strconv.FormatInt(cpu, 10) + " dev-1": 1, strconv.FormatInt(cpu, 10) + " dev-2": 1,
			strconv.FormatInt(other, 10) + " dev-1": 1, strconv.FormatInt(other, 10) + " dev-2": 1,
		}
		if !maps.Equal(got, want) {
			t.Fatalf("alerts per rule and device %v, want %v", got, want)
		}
		if n := len(mailer.keys()); n != 2 {
			t.Fatalf("%d mails, want one per device for the rule with recipients", n)
		}
	})
}

// A stored cooldown counts when it is a whole number of seconds above 0;
// one that is no such number keeps the default of 300 seconds, as a
// cooldown that is not set or 0 does. A rule whose limit is no finite
// number raises nothing. The old engine made text a cooldown of NaN and a
// negative one no cooldown, both of which held nothing back, and compared
// with a limit of NaN or an infinity (BUGS AE3).
func TestARuleWithACooldownOfTextKeepsTheDefault(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		rules := map[string]int64{
			"not set":           addRule(t, db, "cpu_usage", "gt", 80, map[string]any{"cooldown_secs": nil}),
			"0":                 addRule(t, db, "cpu_usage", "gt", 80, map[string]any{"cooldown_secs": 0}),
			"negative":          addRule(t, db, "cpu_usage", "gt", 80, map[string]any{"cooldown_secs": -5}),
			"limit an infinity": addRule(t, db, "cpu_usage", "lt", math.Inf(1), map[string]any{"cooldown_secs": 1}),
		}
		want := map[string]int{"not set": 1, "0": 1, "negative": 1, "limit an infinity": 0}
		if !db.IsPostgres() {
			// Only SQLite keeps text or a fraction in these columns
			rules["text"] = addRule(t, db, "cpu_usage", "gt", 80, map[string]any{"cooldown_secs": "soon"})
			rules["fraction"] = addRule(t, db, "cpu_usage", "gt", 80, map[string]any{"cooldown_secs": 0.5})
			rules["text of digits"] = addRule(t, db, "cpu_usage", "gt", 80, map[string]any{"cooldown_secs": "600"})
			rules["limit text"] = addRule(t, db, "cpu_usage", "neq", 0, map[string]any{"condition_value": "high", "cooldown_secs": 1})
			want["text"], want["fraction"], want["text of digits"], want["limit text"] = 1, 1, 1, 0
		}
		engine := New(Options{DB: db, Logger: &logLines{}})
		for range 3 {
			if err := engine.EvaluateRules(ctx, jsbind.String("dev-1"), "cpu_usage", jsbind.Number(95), EvalOptions{}); err != nil {
				t.Fatal(err)
			}
		}
		got := map[int64]int{}
		for _, a := range alertsIn(t, db) {
			got[a.ruleID]++
		}
		for name, id := range rules {
			if got[id] != want[name] {
				t.Errorf("a rule whose cooldown or limit is %s raised %d alerts in three reports, want %d", name, got[id], want[name])
			}
		}
	})
}
