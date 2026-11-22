package db

import (
	"testing"
	"time"
)

// TestRemoteTargetLastTestAt: the time of a remote target's last test reads
// back as written on both databases, after create and after update, also
// when it was taken in another zone (BUGS RV77).
func TestRemoteTargetLastTestAt(t *testing.T) {
	freshStores(t, func(t *testing.T, database Database) {
		if err := database.CreateOrganization(&Organization{ID: "rv77-org", Name: "RV77", Slug: "rv77", Settings: "{}", CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		tested := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
		target := &RemoteTarget{
			ID: "rv77-target", OrgID: "rv77-org", Name: "RV77", Protocol: "rdp", Host: "10.0.0.7", Port: 3389,
			CredentialMode: "none", TLSMode: "preferred", Enabled: true, LastTestAt: &tested, LastTestStatus: "ok",
		}
		if err := database.CreateRemoteTarget(target); err != nil {
			t.Fatal(err)
		}
		check := func(when string, want time.Time) {
			t.Helper()
			got, err := database.GetRemoteTarget(target.ID)
			if err != nil || got == nil {
				t.Fatalf("%s: GetRemoteTarget: %v %v", when, got, err)
			}
			if got.LastTestAt == nil || !got.LastTestAt.Equal(want) {
				t.Errorf("%s: last test at %v, want %s", when, got.LastTestAt, want)
			}
			list, err := database.ListRemoteTargets("rv77-org", true)
			if err != nil || len(list) != 1 {
				t.Fatalf("%s: ListRemoteTargets: %v %v", when, list, err)
			}
			if list[0].LastTestAt == nil || !list[0].LastTestAt.Equal(want) {
				t.Errorf("%s: listed last test at %v, want %s", when, list[0].LastTestAt, want)
			}
		}
		check("created", tested)

		retested := time.Date(2026, 11, 21, 14, 30, 5, 0, time.FixedZone("plus2", 2*3600))
		target.LastTestAt = &retested
		if err := database.UpdateRemoteTarget(target); err != nil {
			t.Fatal(err)
		}
		check("updated", retested)

		target.LastTestAt = nil
		if err := database.UpdateRemoteTarget(target); err != nil {
			t.Fatal(err)
		}
		if got, err := database.GetRemoteTarget(target.ID); err != nil || got == nil || got.LastTestAt != nil {
			t.Errorf("cleared last test at: %+v %v", got, err)
		}
	})
}
