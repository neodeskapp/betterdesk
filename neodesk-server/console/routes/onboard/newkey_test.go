package onboard_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestAnAdminApprovesTheNewKeyOfAResetComputer: a reset computer that
// brings a new key to an invite is told what to do, its device page shows
// the new key, and once an administrator approves it the same invite lets
// the computer back in (L334).
func TestAnAdminApprovesTheNewKeyOfAResetComputer(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, sdb *store.DB) {
		w := newWorld(t, sdb)
		admin := w.browser("admin")
		key := func() string {
			pub, _, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			return base64.StdEncoding.EncodeToString(pub)
		}
		join := func(code, publicKey string) answer {
			return w.app().do(http.MethodPost, "/api/onboard/join",
				`{"code":"`+code+`","device_id":"289485454","uuid":"machine-1","hostname":"ndlab-win11","platform":"windows","public_key":"`+publicKey+`"}`)
		}
		invite := func() string {
			return str(admin.do(http.MethodPost, "/api/onboard/invites", `{"name":"Lab","lifetime_hours":24,"uses":1}`).json(t), "code")
		}
		if res := join(invite(), key()); res.status != http.StatusOK {
			t.Fatalf("first join: %d %s", res.status, res.body)
		}

		code, newKey := invite(), key()
		res := join(code, newKey)
		if res.status != http.StatusForbidden || !strings.Contains(res.body, "Ask your administrator to approve the new key or to delete the device") {
			t.Fatalf("rejoin with a new key: %d %s", res.status, res.body)
		}
		page := admin.do(http.MethodGet, "/api/devices/289485454", "")
		if page.status != http.StatusOK || !strings.Contains(page.body, `"key_request":{`) || !strings.Contains(page.body, `"hostname":"ndlab-win11"`) {
			t.Fatalf("device page: %d %s", page.status, page.body)
		}
		if res := w.browser("operator").do(http.MethodPost, "/api/onboard/devices/289485454/approve-key", `{}`); res.status != http.StatusForbidden {
			t.Errorf("an operator approved the key: %d %s", res.status, res.body)
		}
		if res := admin.do(http.MethodPost, "/api/onboard/devices/289485454/approve-key", `{}`); res.status != http.StatusOK {
			t.Fatalf("approve: %d %s", res.status, res.body)
		}
		if res := admin.do(http.MethodPost, "/api/onboard/devices/289485454/approve-key", `{}`); res.status != http.StatusNotFound {
			t.Errorf("approving twice: %d %s", res.status, res.body)
		}
		if res := join(code, newKey); res.status != http.StatusOK {
			t.Fatalf("rejoin after the approval: %d %s", res.status, res.body)
		}
	})
}
