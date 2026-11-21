package bdapi_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// The chat history is the conversation of the device the token is bound
// to: a body that names the own device does not open another device's
// conversation through the query (BUGS RV5).
func TestChatHistoryIsTheTokensDevice(t *testing.T) {
	api := routetest.NewAPI().
		On("GET /api/chat/history/DEV1?limit=100", http.StatusOK, `{"conversation_id":"DEV1","messages":[{"id":1,"conversation_id":"DEV1","from_id":"DEV1","text":"own","created_at":"2026-11-18T10:00:00Z"}]}`).
		On("GET /api/chat/history/DEV2?limit=100", http.StatusOK, `{"conversation_id":"DEV2","messages":[{"id":2,"conversation_id":"DEV2","from_id":"DEV2","text":"secret","created_at":"2026-11-18T10:00:00Z"}]}`)
	w := routetest.New(t, routetest.Options{API: api})
	auth := []string{"Authorization", "Bearer " + deviceToken(t, w, "tok-chat-rv5-0123456789", "DEV1")}

	got := w.SignedOut().Do("GET", "/api/bd/chat/history?device_id=DEV2", `{"device_id":"DEV1"}`, auth...)
	if got.Status == 200 && got.Body != `[{"id":"1","device_id":"DEV1","sender":"DEV1","content":"own","timestamp":"2026-11-18T10:00:00Z"}]` {
		t.Errorf("history with the own id in the body and another in the query: %d %s", got.Status, got.Body)
	}
	for _, call := range api.Calls() {
		if call != "GET /api/chat/history/DEV1?limit=100" {
			t.Errorf("asked the API server %s", call)
		}
	}
	if got := w.SignedOut().Do("GET", "/api/bd/chat/history?device_id=DEV2", "", auth...); got.Status != 403 {
		t.Errorf("another device in the query: %d %s", got.Status, got.Body)
	}
}

// The operator's device list holds the devices of the account's scope:
// with devices nobody was given hidden, the operator granted D1 sees D1
// alone, the admin both (BUGS RV4).
func TestOperatorDevicesFollowTheScope(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db, Config: func(cfg *config.Config) { cfg.DeviceScopeDefault = "restricted" }})
		ctx := context.Background()
		for _, q := range []string{
			`INSERT INTO peer (id, uuid, info) VALUES ('D1', 'u-1', '{}'), ('D2', 'u-2', '{}')`,
			`INSERT INTO user_peer_grants (user_id, peer_id) VALUES (5, 'D1')`,
		} {
			if _, err := db.Exec(ctx, q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
		for _, c := range []struct {
			role string
			user int64
			want string
		}{{"admin", 1, "D1,D2"}, {"operator", 5, "D1"}} {
			token := "tok-operator-devices-" + c.role
			sum := sha256.Sum256([]byte(token))
			if _, err := db.Exec(ctx, `INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at)
				VALUES (?, ?, ?, '', '', '2999-01-01 00:00:00')`, token, hex.EncodeToString(sum[:]), c.user); err != nil {
				t.Fatal(err)
			}
			got := w.SignedOut().Do("GET", "/api/bd/operator/devices", "", "Authorization", "Bearer "+token)
			var answer struct {
				Devices []struct {
					ID string `json:"id"`
				} `json:"devices"`
			}
			_ = json.Unmarshal([]byte(got.Body), &answer)
			var ids []string
			for _, d := range answer.Devices {
				ids = append(ids, d.ID)
			}
			if found := strings.Join(ids, ","); got.Status != 200 || found != c.want {
				t.Errorf("%s: %d %s, want the devices %q", c.role, got.Status, got.Body, c.want)
			}
		}
	})
}

// A connection record that the client audit route takes without a
// credential does not show in an operator's session history, even when it
// names the operator as its host; the operator's own records do (BUGS
// RV3).
func TestSessionHistoryIsNotForgedThroughTheClientAudit(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db})
		forged := w.SignedOut().Do("POST", "/api/audit/conn", `{"host_id":"e2e_operator","peer_id":"DEV9","action":"session_start","conn_type":1,"session_id":"forged-1"}`)
		if forged.Status != 200 {
			t.Fatalf("client audit record: %d %s", forged.Status, forged.Body)
		}
		auth := []string{"Authorization", "Bearer " + operatorToken(t, w, "tok-history-forged-01")}
		if got := w.SignedOut().Do("POST", "/api/bd/operator/sessions", `{"action":"start","device_id":"DEV1","session_id":"own-1"}`, auth...); got.Status != 200 {
			t.Fatalf("own session: %d %s", got.Status, got.Body)
		}
		got := w.SignedOut().Do("GET", "/api/bd/operator/sessions", "", auth...)
		if got.Status != 200 || strings.Contains(got.Body, "forged-1") || !strings.Contains(got.Body, `"id":"own-1"`) {
			t.Errorf("history: %d %s", got.Status, got.Body)
		}
	})
}
