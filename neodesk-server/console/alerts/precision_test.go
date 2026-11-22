package alerts

import (
	"context"
	"testing"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// A rule's limit keeps its eight bytes on both databases, so the same rule
// compares the same everywhere. PostgreSQL kept condition_value in four
// bytes: 16777217 came back as 16777216 and 123456.789 as 123456.79, and
// eq, neq and the edges of the other operators differed from SQLite
// (BUGS AE4).
func TestConditionValueKeepsEightBytes(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		limits := map[float64]int64{}
		for _, limit := range []float64{16777217, 123456.789, 0.1} {
			limits[limit] = addRule(t, db, "cpu_usage", "eq", limit, map[string]any{"cooldown_secs": 1})
		}
		rules, err := GetAlertRules(ctx, db, RuleFilter{})
		if err != nil {
			t.Fatal(err)
		}
		for _, rule := range rules {
			for limit, id := range limits {
				if rule.ID.ToNumber() == float64(id) && rule.ConditionValue.ToNumber() != limit {
					t.Errorf("the limit %v reads as %v", limit, rule.ConditionValue.ToNumber())
				}
			}
		}
		engine := New(Options{DB: db, Logger: &logLines{}})
		for limit, id := range limits {
			if err := engine.EvaluateRules(ctx, jsbind.String("dev-1"), "cpu_usage", jsbind.Number(limit), EvalOptions{}); err != nil {
				t.Fatal(err)
			}
			raised := 0
			for _, a := range alertsIn(t, db) {
				if a.ruleID == id {
					raised++
				}
			}
			if raised != 1 {
				t.Errorf("a value equal to the limit %v raised %d alerts of its rule, want 1", limit, raised)
			}
		}
	})
}
