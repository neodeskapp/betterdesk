package bdapi_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
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

	got := w.SignedOut().Do("GET", "/api/nd/chat/history?device_id=DEV2", `{"device_id":"DEV1"}`, auth...)
	if got.Status == 200 && got.Body != `[{"id":"1","device_id":"DEV1","sender":"DEV1","content":"own","timestamp":"2026-11-18T10:00:00Z"}]` {
		t.Errorf("history with the own id in the body and another in the query: %d %s", got.Status, got.Body)
	}
	for _, call := range api.Calls() {
		if call != "GET /api/chat/history/DEV1?limit=100" {
			t.Errorf("asked the API server %s", call)
		}
	}
	if got := w.SignedOut().Do("GET", "/api/nd/chat/history?device_id=DEV2", "", auth...); got.Status != 403 {
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
			got := w.SignedOut().Do("GET", "/api/nd/operator/devices", "", "Authorization", "Bearer "+token)
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
		// A device that took the operator's name, with the uuid it registered
		// with, which the client audit asks for (BUGS RV2)
		for _, statement := range []string{
			`CREATE TABLE IF NOT EXISTS peers (id TEXT PRIMARY KEY, uuid TEXT DEFAULT '', soft_deleted BOOLEAN DEFAULT FALSE)`,
			`INSERT INTO peers (id, uuid) VALUES ('e2e_operator', 'u-forger')`,
		} {
			if _, err := db.Exec(context.Background(), statement); err != nil {
				t.Fatal(err)
			}
		}
		forged := w.SignedOut().Do("POST", "/api/audit/conn", `{"host_id":"e2e_operator","uuid":"u-forger","peer_id":"DEV9","action":"session_start","conn_type":1,"session_id":"forged-1"}`)
		if forged.Status != 200 {
			t.Fatalf("client audit record: %d %s", forged.Status, forged.Body)
		}
		auth := []string{"Authorization", "Bearer " + operatorToken(t, w, "tok-history-forged-01")}
		if got := w.SignedOut().Do("POST", "/api/nd/operator/sessions", `{"action":"start","device_id":"DEV1","session_id":"own-1"}`, auth...); got.Status != 200 {
			t.Fatalf("own session: %d %s", got.Status, got.Body)
		}
		got := w.SignedOut().Do("GET", "/api/nd/operator/sessions", "", auth...)
		if got.Status != 200 || strings.Contains(got.Body, "forged-1") || !strings.Contains(got.Body, `"id":"own-1"`) {
			t.Errorf("history: %d %s", got.Status, got.Body)
		}
	})
}

// The operator sign-in stores its device_id as the token's client id only
// when it has the form of a device id; a longer or other text counts as
// none (BUGS RV1).
func TestOperatorLoginTakesOnlyADeviceID(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db, Env: map[string]string{"RATE_LIMIT_MAX": "1000", "LOGIN_RATE_LIMIT_MAX": "1000"}})
		ctx := context.Background()
		hash, err := w.Server.Deps().Authn.HashPassword(ctx, "Rv1-Op-1!x")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `UPDATE users SET password_hash = ? WHERE id = 5`, hash); err != nil {
			t.Fatal(err)
		}
		for id, want := range map[string]string{
			"OPDESK-1":              "OPDESK-1",
			strings.Repeat("7", 40): strings.Repeat("7", 40),
			strings.Repeat("7", 65): "",
			"desk 1":                "",
		} {
			body, _ := json.Marshal(map[string]string{"username": "e2e_operator", "password": "Rv1-Op-1!x", "device_id": id})
			got := w.SignedOut().Do("POST", "/api/nd/operator/login", string(body))
			if got.Status != 200 {
				t.Fatalf("operator sign-in with %q: %d %s", id, got.Status, got.Body)
			}
			var stored string
			if err := db.QueryRow(ctx, `SELECT client_id FROM access_tokens WHERE user_id = 5 ORDER BY id DESC LIMIT 1`).Scan(&stored); err != nil || stored != want {
				t.Errorf("operator sign-in with %q stored client id %q (%v), want %q", id, stored, err, want)
			}
		}
	})
}

// Only a token the console issued for a device proves that device. The
// operator sign-in stores the device id it names, and its token keeps
// working on the operator routes, but the device routes refuse it, it
// names no device at the registration and starts no relay session. A
// device token passes them and is no operator's sign-in (BUGS RV1).
func TestOnlyADeviceTokenProvesADevice(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		storetest.GoServerMigrate(t, db)
		w := routetest.New(t, routetest.Options{DB: db, Env: map[string]string{"RATE_LIMIT_MAX": "1000", "LOGIN_RATE_LIMIT_MAX": "1000"}})
		ctx := context.Background()
		authn := w.Server.Deps().Authn
		hash, err := authn.HashPassword(ctx, "Rv1-Op-2!x")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `UPDATE users SET password_hash = ? WHERE id = 5`, hash); err != nil {
			t.Fatal(err)
		}
		got := w.SignedOut().Do("POST", "/api/nd/operator/login", `{"username":"e2e_operator","password":"Rv1-Op-2!x","device_id":"900100001"}`)
		var signedIn struct {
			Token string `json:"access_token"`
		}
		if err := json.Unmarshal([]byte(got.Body), &signedIn); err != nil || got.Status != 200 || signedIn.Token == "" {
			t.Fatalf("operator sign-in: %d %s", got.Status, got.Body)
		}
		operator := []string{"Authorization", "Bearer " + signedIn.Token}
		device, err := authn.IssueDeviceToken(ctx, "900100001", "test")
		if err != nil {
			t.Fatal(err)
		}
		bound := []string{"Authorization", "Bearer " + device}
		agent := w.SignedOut()

		for _, c := range []struct {
			name, method, target, body string
			auth                       []string
			status                     int
			answer                     string
		}{
			{"heartbeat with the operator's token", "POST", "/api/nd/heartbeat", `{"device_id":"900100001"}`, operator,
				401, `{"error":"Invalid or unbound access token"}`},
			{"chat with the operator's token", "POST", "/api/nd/chat/send", `{"device_id":"900100001","message":"hi"}`, operator,
				401, `{"error":"Invalid or unbound access token"}`},
			{"relay session with the operator's token", "POST", "/api/nd/connect", `{"target_id":"900100002"}`, operator,
				403, `{"error":"A device-bound access token is required"}`},
			{"registration with the operator's token", "POST", "/api/nd/register", `{}`, operator,
				401, `{"error":"Missing device identification"}`},
			{"operator devices with the device token", "GET", "/api/nd/operator/devices", "", bound,
				403, `{"error":"Insufficient permissions"}`},
			{"relay session with the device token", "POST", "/api/nd/connect", `{"target_id":"900100002"}`, bound,
				404, `{"error":"Target device not found"}`},
		} {
			got := agent.Do(c.method, c.target, c.body, c.auth...)
			if got.Status != c.status || got.Body != c.answer {
				t.Errorf("%s: %d %s, want %d %s", c.name, got.Status, got.Body, c.status, c.answer)
			}
		}
		if got := agent.Do("GET", "/api/nd/operator/devices", "", operator...); got.Status != 200 {
			t.Errorf("operator devices with the operator's token: %d %s", got.Status, got.Body)
		}
		if got := agent.Do("POST", "/api/nd/heartbeat", `{"device_id":"900100001"}`, bound...); got.Status == 401 || got.Status == 403 {
			t.Errorf("heartbeat with the device token: %d %s", got.Status, got.Body)
		}
		if got := agent.Do("POST", "/api/nd/register", `{"device_id":"900100002"}`, bound...); got.Status != 403 || got.Body != `{"error":"device_id mismatch"}` {
			t.Errorf("registration of another device with the device token: %d %s", got.Status, got.Body)
		}
	})
}
