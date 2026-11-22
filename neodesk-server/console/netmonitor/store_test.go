package netmonitor

import (
	"context"
	"fmt"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// Checks of the same second come newest first, so a limit cuts the oldest
// of them (BUGS.md NM8).
func TestHistoryOfOneSecondKeepsItsOrder(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		if _, err := db.Exec(ctx, "INSERT INTO network_targets (id, name, host, check_type) VALUES (1, 't', 'h', 'ping')"); err != nil {
			t.Fatal(err)
		}
		for i := 1; i <= 3; i++ {
			if _, err := db.Exec(ctx, "INSERT INTO network_checks (id, target_id, status, checked_at) VALUES (?, 1, 'up', ?)", i, "2026-01-01 00:00:00"); err != nil {
				t.Fatal(err)
			}
		}
		limit := Num(2)
		checks, err := CheckHistory(ctx, db, Num(1), HistoryFilter{Limit: &limit})
		if err != nil {
			t.Fatal(err)
		}
		var ids []int64
		for _, c := range checks {
			ids = append(ids, c.ID.V)
		}
		if fmt.Sprint(ids) != "[3 2]" {
			t.Errorf("checks %v, want the newest two: [3 2]", ids)
		}
	})
}

// Targets of one name come in the order of their ids, also after one of
// them was changed, which moves its row on PostgreSQL (BUGS.md NM12).
func TestTargetsOfOneNameKeepTheirOrder(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		for _, row := range []string{"(1, 'same', 'a')", "(2, 'same', 'b')", "(3, 'other', 'c')", "(4, 'same', 'd')"} {
			if _, err := db.Exec(ctx, "INSERT INTO network_targets (id, name, host, check_type) VALUES "+row[:len(row)-1]+", 'ping')"); err != nil {
				t.Fatal(err)
			}
		}
		for _, id := range []int{1, 2, 1} {
			status := Text("up")
			if _, err := UpdateTarget(ctx, db, Num(Number(id)), TargetChanges{LastStatus: &status}); err != nil {
				t.Fatal(err)
			}
		}
		targets, err := ListTargets(ctx, db, TargetFilter{})
		if err != nil {
			t.Fatal(err)
		}
		var order []int64
		for _, target := range targets {
			order = append(order, target.ID.V)
		}
		if fmt.Sprint(order) != "[3 1 2 4]" {
			t.Errorf("targets %v, want [3 1 2 4]: by name, then id", order)
		}
	})
}

// A target keeps its newest 10,000 checks: storing a check removes the
// target's checks before them, and the checks of other targets stay (BUGS
// RV49).
func TestATargetKeepsItsNewestChecks(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		for _, row := range []string{
			"INSERT INTO network_targets (id, name, host, port, check_type) VALUES (1, 'kept', '10.1.2.3', 80, 'tcp')",
			"INSERT INTO network_targets (id, name, host, check_type) VALUES (2, 'other', '10.1.2.4', 'ping')",
			"WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < 10005) INSERT INTO network_checks (target_id, status) SELECT 1, 'down' FROM n",
			"INSERT INTO network_checks (target_id, status) VALUES (2, 'up'), (2, 'up')",
		} {
			if _, err := db.Exec(ctx, row); err != nil {
				t.Fatal(err)
			}
		}
		count := func(target int) (n, oldest int64) {
			t.Helper()
			if err := db.QueryRow(ctx, "SELECT COUNT(*), MIN(id) FROM network_checks WHERE target_id = ?", target).Scan(&n, &oldest); err != nil {
				t.Fatal(err)
			}
			return n, oldest
		}
		no := false
		m := New(Options{Store: db, Resolver: &tableResolver{names: map[string][]string{}, errors: map[string]string{}}, Windows: &no, Dialer: stuckDialer{}})
		target, err := GetTarget(ctx, db, Num(1))
		if err != nil || target == nil {
			t.Fatalf("target: %v", err)
		}
		target.TimeoutMs = store.Float{V: 1, Valid: true}
		m.CheckTarget(ctx, target)
		if n, oldest := count(1); n != 10000 || oldest != 7 {
			t.Errorf("target 1 has %d checks from id %d, want the newest 10000 (from id 7)", n, oldest)
		}
		if n, _ := count(2); n != 2 {
			t.Errorf("target 2 has %d checks, want its 2", n)
		}
	})
}
