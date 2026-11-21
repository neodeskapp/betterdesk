package db

import (
	"fmt"
	"testing"
	"time"
)

// TestAPIKeyAndUserTimesInUTC: the times of an API key and of an account
// come back as UTC text on both databases, also when the server process
// runs in another zone. An expiry written as UTC text reads back as
// written (BUGS RV70).
func TestAPIKeyAndUserTimesInUTC(t *testing.T) {
	saved := time.Local
	time.Local = time.FixedZone("plus2", 2*3600)
	t.Cleanup(func() { time.Local = saved })

	panelGroupStores(t, func(t *testing.T, database Database) {
		// The PostgreSQL test database is shared: names of this run only,
		// removed afterwards
		run := fmt.Sprintf("%d", time.Now().UnixNano())
		near := func(what, text string) {
			t.Helper()
			at, err := time.Parse("2006-01-02 15:04:05", text)
			if err != nil {
				t.Fatalf("%s %q: %v", what, text, err)
			}
			if d := time.Since(at); d < -time.Minute || d > time.Minute {
				t.Errorf("%s %s read as UTC is %s away from now", what, text, d.Round(time.Second))
			}
		}

		key := &APIKey{KeyHash: "rv70" + run, KeyPrefix: "rv70", Name: "rv70", Role: "viewer", ExpiresAt: "2030-01-01 12:00:00"}
		if err := database.CreateAPIKey(key); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.DeleteAPIKey(key.ID) })
		if err := database.TouchAPIKey(key.ID); err != nil {
			t.Fatal(err)
		}
		got, err := database.GetAPIKeyByHash(key.KeyHash)
		if err != nil || got == nil {
			t.Fatalf("GetAPIKeyByHash: %v %v", got, err)
		}
		if got.ExpiresAt != "2030-01-01 12:00:00" {
			t.Errorf("key expires %q, written 2030-01-01 12:00:00", got.ExpiresAt)
		}
		near("key created_at", got.CreatedAt)
		near("key last_used", got.LastUsed)

		user := &User{Username: "rv70" + run, PasswordHash: "x", Role: "viewer"}
		if err := database.CreateUser(user); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.DeleteUser(user.ID) })
		if err := database.UpdateUserLogin(user.ID); err != nil {
			t.Fatal(err)
		}
		account, err := database.GetUserByID(user.ID)
		if err != nil || account == nil {
			t.Fatalf("GetUserByID: %v %v", account, err)
		}
		near("account created_at", account.CreatedAt)
		near("account last_login", account.LastLogin)
	})
}
