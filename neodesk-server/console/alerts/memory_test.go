package alerts

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// memoryRules is how the engine asks for the memory rules.
const memoryRules = `{"enabled":true,"condition_type":"memory_usage"}`

// memoryReads reports for a telemetry report whether Node and the Go
// console read its memory values: Node when both are truthy, the Go
// console when neither is missing, null or the empty text (BUGS AE5).
func memoryReads(t testing.TB, telemetry map[string]string) (node, goConsole bool) {
	used, total := bindValue(t, telemetry["memory_used_bytes"]), bindValue(t, telemetry["memory_total_bytes"])
	set := func(v jsbind.Value) bool { return !v.IsUndefined() && !v.IsNull() && !(v.IsString() && v.Str() == "") }
	return used.Truthy() && total.Truthy(), set(used) && set(total)
}

// memoryChecks reports whether Node and the Go console check the memory
// rules for a telemetry report: each where it reads both values and the
// percentage is a finite number, the Go console only for a total above 0
// (BUGS AE5).
func memoryChecks(t testing.TB, telemetry map[string]string) (node, goConsole bool) {
	nodeReads, goReads := memoryReads(t, telemetry)
	used, errUsed := jsNumber(bindValue(t, telemetry["memory_used_bytes"]))
	total, errTotal := jsNumber(bindValue(t, telemetry["memory_total_bytes"]))
	if errUsed != nil || errTotal != nil {
		return false, false
	}
	ratio := jsRound(used / total * 100)
	finite := !math.IsNaN(ratio) && !math.IsInf(ratio, 0)
	return nodeReads && finite, goReads && total > 0 && finite
}

// isPrefix reports whether short is where long starts.
func isPrefix(short, long []string) bool {
	return len(short) <= len(long) && sameStrings(short, long[:len(short)])
}

// sameBeforeMemory compares a telemetry case where one side checked memory
// and the other did not (BUGS AE5): the other side's rule loads, cooldown
// checks and alerts are where the one that checked memory started, before
// memory. Where neither checked it, they are the same. It returns what
// differs, or "".
func sameBeforeMemory(mem *memStore, c *memCase, goChecked, nodeChecked bool) string {
	var nodeCreated []string
	for _, alert := range c.Created {
		nodeCreated = append(nodeCreated, alert.Alert)
	}
	nodeRecent, goRecent := make([]string, len(c.RecentCalls)), make([]string, len(mem.recent))
	for k, call := range c.RecentCalls {
		nodeRecent[k] = strings.Join(call[:], " ")
	}
	for k, call := range mem.recent {
		goRecent[k] = strings.Join(call[:], " ")
	}
	short, long := [3][]string{c.Filters, nodeRecent, nodeCreated}, [3][]string{mem.filters, goRecent, mem.created}
	if nodeChecked {
		short, long = long, short
	}
	extra := 0
	if goChecked != nodeChecked {
		extra = 1
	}
	if len(long[0]) != len(short[0])+extra || extra == 1 && long[0][len(long[0])-1] != memoryRules || !isPrefix(short[0], long[0]) {
		return "rule loads " + strings.Join(mem.filters, ", ") + ", Node: " + strings.Join(c.Filters, ", ")
	}
	for k, what := range []string{"cooldown checks", "alerts"} {
		if !isPrefix(short[k+1], long[k+1]) {
			return what + " " + strings.Join(long[k+1], ", ") + " do not start with " + strings.Join(short[k+1], ", ")
		}
	}
	return ""
}

// Memory is checked when a report has both values and the total is above
// 0: also at 0 bytes used, which the old engine skipped because 0 is
// false, and never for a total of 0 or below, which it divided by
// (BUGS AE5).
func TestMemoryRuleAtZeroUsed(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		id := addRule(t, db, "memory_usage", "lt", 10, map[string]any{"cooldown_secs": 1})
		for _, report := range []struct {
			used, total jsbind.Value
			raises      bool
		}{
			{jsbind.Number(0), jsbind.Number(100), true},
			{jsbind.Number(5), jsbind.Number(100), true},
			{jsbind.String("0"), jsbind.Number(100), true},
			{jsbind.Number(5), jsbind.String("100"), true},
			{jsbind.Number(50), jsbind.Number(100), false},
			{jsbind.Number(0), jsbind.Number(0), false},
			{jsbind.Number(5), jsbind.Number(0), false},
			{jsbind.Number(5), jsbind.Number(-100), false},
			{jsbind.Number(5), jsbind.String("0"), false},
			{jsbind.Number(5), jsbind.Null, false},
			{jsbind.Null, jsbind.Number(100), false},
			{jsbind.String(""), jsbind.Number(100), false},
			{jsbind.Undefined, jsbind.Number(100), false},
		} {
			if _, err := db.Exec(ctx, "DELETE FROM alert_history"); err != nil {
				t.Fatal(err)
			}
			engine := New(Options{DB: db, Logger: &logLines{}})
			if err := engine.EvaluateTelemetry(ctx, jsbind.String("dev-1"), Telemetry{MemoryUsedBytes: report.used, MemoryTotalBytes: report.total}); err != nil {
				t.Fatal(err)
			}
			got := alertsIn(t, db)
			if raised := len(got) > 0; raised != report.raises {
				t.Errorf("used %s of %s: raised %v, want %v", report.used.JSON(), report.total.JSON(), raised, report.raises)
			}
			if len(got) > 0 && (got[0].ruleID != id || got[0].deviceID != "dev-1") {
				t.Errorf("used %s of %s: %+v", report.used.JSON(), report.total.JSON(), got[0])
			}
		}
	})
}
