package db

import (
	"testing"
	"time"
)

func TestServerInstancesAreTouchedListedAndRemoved(t *testing.T) {
	freshStores(t, func(t *testing.T, store Database) {
		now := time.UnixMilli(time.Now().UnixMilli())
		a := ServerInstance{Name: "pod-a", Host: "node-1", Clustered: true, StartedAt: now.Add(-time.Hour), LastSeen: now}
		old := ServerInstance{Name: "pod-old", StartedAt: now.Add(-2 * time.Hour), LastSeen: now.Add(-time.Minute)}
		for _, inst := range []ServerInstance{a, old} {
			if err := store.TouchServerInstance(inst); err != nil {
				t.Fatal(err)
			}
		}
		got, err := store.ListServerInstances(now.Add(-30 * time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if len(got) != 1 || got[0] != a {
			t.Fatalf("live instances %+v, want only %+v", got, a)
		}

		// A touch refreshes the row in place
		a.LastSeen = now.Add(time.Second)
		a.Clustered = false
		if err := store.TouchServerInstance(a); err != nil {
			t.Fatal(err)
		}
		got, _ = store.ListServerInstances(now.Add(-30 * time.Second))
		if len(got) != 1 || got[0] != a {
			t.Fatalf("after refresh %+v, want %+v", got, a)
		}

		if err := store.RemoveServerInstance("pod-a"); err != nil {
			t.Fatal(err)
		}
		if got, _ = store.ListServerInstances(time.Time{}); len(got) != 1 || got[0].Name != "pod-old" {
			t.Fatalf("after remove %+v", got)
		}
	})
}
