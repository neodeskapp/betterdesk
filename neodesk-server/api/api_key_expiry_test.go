package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// TestAPIKeyExpiryIsWrittenInUTC: a key's expiry is written in UTC, the zone
// the credential check reads it in, so outside UTC a key ends when it should
// (RV70).
func TestAPIKeyExpiryIsWrittenInUTC(t *testing.T) {
	prev := time.Local
	time.Local = time.FixedZone("UTC+2", 2*60*60)
	t.Cleanup(func() { time.Local = prev })

	srv, _ := newInternalServer(t, nil)
	status, body := asConsole(t, srv, http.MethodPost, "/api/keys", `{"name":"expiring","role":"viewer","expires_in_days":1}`)
	var key struct {
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal([]byte(body), &key); status != http.StatusCreated || err != nil {
		t.Fatalf("create a key: %d %s", status, body)
	}
	exp, err := time.Parse("2006-01-02 15:04:05", key.ExpiresAt)
	if err != nil {
		t.Fatalf("expires_at %q: %v", key.ExpiresAt, err)
	}
	if off := exp.Sub(time.Now().Add(24 * time.Hour)); off < -time.Minute || off > time.Minute {
		t.Errorf("a key for one day expires at %s, %s away from now plus a day", key.ExpiresAt, off)
	}
}
