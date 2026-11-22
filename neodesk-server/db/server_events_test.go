package db

import (
	"reflect"
	"testing"
	"time"
)

// TestServerEventsAreKeptAndPaged: events come back newest first with
// their time, actor and details, by action, from an offset, counted, and
// the prune deletes only those before the cutoff (L91, L126).
func TestServerEventsAreKeptAndPaged(t *testing.T) {
	freshStores(t, func(t *testing.T, store Database) {
		base := time.Date(2026, 11, 22, 9, 0, 0, 123456000, time.UTC)
		var events []*ServerEvent
		for i, action := range []string{"auth_login_failed", "peer_banned", "auth_login_failed", "config_changed", "auth_login_failed"} {
			events = append(events, &ServerEvent{
				Time: base.Add(time.Duration(i) * time.Minute), Action: action, Actor: "alice", Target: string(rune('A' + i)),
				Details: map[string]string{"n": string(rune('0' + i))},
			})
		}
		events[3].Details = nil
		if err := store.AddServerEvents(events); err != nil {
			t.Fatal(err)
		}

		page, err := store.ListServerEvents("auth_login_failed", 2, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) != 2 || page[0].Target != "C" || page[1].Target != "A" {
			t.Fatalf("second and third failed sign-ins: %+v", page)
		}
		if !page[0].Time.Equal(base.Add(2*time.Minute)) || page[0].Actor != "alice" || !reflect.DeepEqual(page[0].Details, map[string]string{"n": "2"}) {
			t.Errorf("event %+v", page[0])
		}
		all, err := store.ListServerEvents("", 10, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(all) != 5 || all[0].Target != "E" || all[1].Details != nil {
			t.Fatalf("all events: %+v", all)
		}
		if n, err := store.CountServerEvents("auth_login_failed"); err != nil || n != 3 {
			t.Errorf("failed sign-ins counted %d, %v", n, err)
		}
		if n, err := store.CountServerEvents(""); err != nil || n != 5 {
			t.Errorf("events counted %d, %v", n, err)
		}

		gone, err := store.PruneServerEvents(base.Add(2 * time.Minute))
		if err != nil || gone != 2 {
			t.Fatalf("pruned %d, %v; want the two before the cutoff", gone, err)
		}
		if left, _ := store.ListServerEvents("", 10, 0); len(left) != 3 || left[2].Target != "C" {
			t.Errorf("left after the prune: %+v", left)
		}
	})
}
