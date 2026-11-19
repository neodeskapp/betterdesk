package bdapi_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/routes/internal/routetest"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// deviceToken adds a live access token of the admin account bound to a
// device and returns it.
func deviceToken(t *testing.T, w *routetest.World, token, device string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(token))
	if _, err := w.DB.Exec(context.Background(), `INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at)
		VALUES (?, ?, 1, ?, '', '2999-01-01 00:00:00')`, token, hex.EncodeToString(sum[:]), device); err != nil {
		t.Fatal(err)
	}
	return token
}

// The registration route has its own limit: 20 a minute per address
// (BD_REGISTER_RATE_LIMIT_MAX), answered before the device is looked at.
func TestRegisterIsLimited(t *testing.T) {
	w := routetest.New(t, routetest.Options{Env: map[string]string{"BD_REGISTER_RATE_LIMIT_MAX": "2", "RATE_LIMIT_MAX": "1000"}})
	device := w.SignedOut()
	for i, want := range []int{401, 401, 429} {
		got := device.Do("POST", "/api/bd/register", `{}`)
		if got.Status != want {
			t.Fatalf("request %d: %d %s, want %d", i+1, got.Status, got.Body, want)
		}
		if i == 2 && got.Body != `{"success":false,"error":"Too many registration requests. Please slow down."}` {
			t.Errorf("refusal: %s", got.Body)
		}
	}
	// The limit is the registration's own: a heartbeat is not counted
	if got := device.Do("POST", "/api/bd/heartbeat", `{}`); got.Status != 401 {
		t.Errorf("heartbeat: %d %s", got.Status, got.Body)
	}
}

// BD_REGISTER_ALLOW_OPEN lets a device register without a proof when it
// is true, 1 or yes, in any letter case and with white space around.
func TestOpenRegistration(t *testing.T) {
	for value, open := range map[string]bool{"": false, "true": true, " YES ": true, "1": true, "on": false, "false": false} {
		w := routetest.New(t, routetest.Options{Env: map[string]string{"BD_REGISTER_ALLOW_OPEN": value}})
		got := w.SignedOut().Do("POST", "/api/bd/register", `{"device_id":"NEWDEV1"}`, "X-Device-Id", "NEWDEV1")
		if (got.Status == 200) != open {
			t.Errorf("BD_REGISTER_ALLOW_OPEN=%q: %d %s", value, got.Status, got.Body)
		}
	}
}

// A history request that passes its checks ended the old console's
// process. The Go console drops the connection without an answer and
// goes on.
func TestChatHistoryDropsTheConnection(t *testing.T) {
	w := routetest.New(t, routetest.Options{})
	token := deviceToken(t, w, "tok-chat-0123456789", "DEV1")
	req, _ := http.NewRequest(http.MethodGet, "http://"+w.Addr()+"/api/bd/chat/history?device_id=DEV1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err == nil {
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		t.Fatalf("an answer: %d %s", res.StatusCode, body)
	}
	if got := w.SignedOut().Do("GET", "/api/bd/chat/history", "", "Authorization", "Bearer "+token); got.Status != 400 {
		t.Errorf("without a device id: %d %s", got.Status, got.Body)
	}
}

// The two notification writes check the CSRF token themselves: the gate
// of the pipeline comes after the device API.
func TestNotificationWritesNeedTheCSRFToken(t *testing.T) {
	w := routetest.New(t, routetest.Options{API: routetest.NewAPI().On("GET /api/help/requests", 200, `[]`)})
	for _, path := range []string{"/api/bd/notifications/x/read", "/api/bd/notifications/read-all"} {
		for _, role := range []string{"", "admin"} {
			agent := w.SignedOut()
			if role != "" {
				agent = w.As(role)
			}
			got := agent.Do("POST", path, `{}`, "X-CSRF-Token", "wrong")
			if got.Status != 403 || !strings.Contains(got.Body, "Invalid CSRF token") {
				t.Errorf("%s as %q without the token: %d %s", path, role, got.Status, got.Body)
			}
		}
		if got := w.As("viewer").Do("POST", path, `{}`); got.Status != 200 {
			t.Errorf("%s with the token: %d %s", path, got.Status, got.Body)
		}
	}
}

// The operator routes take a token of an admin or operator; any other
// role is refused.
func TestOperatorRoutesNeedAnOperator(t *testing.T) {
	w := routetest.New(t, routetest.Options{})
	ctx := context.Background()
	for _, row := range []struct {
		token string
		user  int
		want  int
	}{{"tok-op-admin-01234", 1, 200}, {"tok-op-operator-01", 5, 200}, {"tok-op-viewer-0123", 6, 403}, {"tok-op-super-01234", 2, 403}} {
		sum := sha256.Sum256([]byte(row.token))
		if _, err := w.DB.Exec(ctx, `INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at)
			VALUES (?, ?, ?, '', '', '2999-01-01 00:00:00')`, row.token, hex.EncodeToString(sum[:]), row.user); err != nil {
			t.Fatal(err)
		}
		got := w.SignedOut().Do("GET", "/api/bd/operator/sessions", "", "Authorization", "Bearer "+row.token)
		if got.Status != row.want {
			t.Errorf("token of user %d: %d %s, want %d", row.user, got.Status, got.Body, row.want)
		}
	}
}

// An enrollment token is read from the body trimmed and cut to 64
// characters, then looked up; a template admits one device, the first
// that redeems it, and that one again later.
func TestEnrollmentToken(t *testing.T) {
	w := routetest.New(t, routetest.Options{})
	ctx := context.Background()
	if _, err := w.DB.Exec(ctx, `CREATE TABLE agent_templates (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE,
		description TEXT NOT NULL DEFAULT '', config_json TEXT NOT NULL DEFAULT '{}', enrollment_token TEXT NOT NULL UNIQUE,
		enrolled_device_id TEXT DEFAULT NULL, enrolled_at TEXT DEFAULT NULL)`); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("t", 64)
	if _, err := w.DB.Exec(ctx, "INSERT INTO agent_templates (name, enrollment_token) VALUES ('kiosk', ?)", token); err != nil {
		t.Fatal(err)
	}
	register := func(device, tok string) int {
		return w.SignedOut().Do("POST", "/api/bd/register", `{"enrollment_token":"`+tok+`"}`, "X-Device-Id", device).Status
	}
	if got := register("TOKDEV1", "  "+token+"xyz"); got != 200 {
		t.Errorf("a token with more after its 64 characters: %d", got)
	}
	if got := register("TOKDEV2", token); got != 403 {
		t.Errorf("the token for a second device: %d", got)
	}
	var bound string
	if err := w.DB.QueryRow(ctx, "SELECT enrolled_device_id FROM agent_templates").Scan(&bound); err != nil || bound != "TOKDEV1" {
		t.Errorf("bound to %q (%v)", bound, err)
	}
}

// The address of a registration is the client's as TRUST_PROXY gives it.
// The rename guard matches a device that sends neither key nor uuid by
// that address, so an X-Forwarded-For from anyone else names no stored
// address (BUGS BD3).
func TestRegistrationAddressIsTheTrustedOne(t *testing.T) {
	for _, c := range []struct {
		trust, answer, ip string
	}{
		{"", `409 {"error":"Device ID has been changed","new_id":"NEWID1","message":"This device ID was renamed to NEWID1. Please update your configuration."}`, ""},
		{"1", `200 NEWID1`, "203.0.113.9"},
	} {
		w := routetest.New(t, routetest.Options{Config: func(cfg *config.Config) {
			cfg.TrustProxy, _ = httpx.ParseTrustProxy(c.trust)
		}})
		ctx := context.Background()
		for _, stmt := range []string{
			"CREATE TABLE IF NOT EXISTS id_change_history (id INTEGER PRIMARY KEY AUTOINCREMENT, old_id TEXT NOT NULL, new_id TEXT NOT NULL)",
			"INSERT INTO id_change_history (old_id, new_id) VALUES ('OLDID1', 'NEWID1')",
			"INSERT INTO peer (id, uuid, ip, info) VALUES ('NEWID1', '', '203.0.113.9', '{}')",
		} {
			if _, err := w.DB.Exec(ctx, stmt); err != nil {
				t.Fatal(err)
			}
		}
		got := w.SignedOut().Do("POST", "/api/bd/register", `{}`, "X-Device-Id", "OLDID1", "X-Forwarded-For", "203.0.113.9")
		have := fmt.Sprint(got.Status, " ", got.Body)
		if got.Status == 200 && strings.Contains(got.Body, `"device_id":"NEWID1"`) {
			have = "200 NEWID1"
		}
		if have != c.answer {
			t.Errorf("TRUST_PROXY=%q: %s, want %s", c.trust, have, c.answer)
		}
		var ip string
		if err := w.DB.QueryRow(ctx, "SELECT ip FROM peer WHERE id = 'NEWID1'").Scan(&ip); err != nil || c.ip != "" && ip != c.ip {
			t.Errorf("TRUST_PROXY=%q: stored address %q (%v)", c.trust, ip, err)
		}
	}
}

// A banned device is no target: connect refuses it, and the device
// lookup says it is banned (BUGS BD4).
func TestBannedTarget(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db})
		ctx := context.Background()
		for _, stmt := range []string{
			"INSERT INTO peer (id, note, is_banned) VALUES ('BANNED1', 'banned box', " + db.Pick("1", "TRUE") + ")",
			"INSERT INTO peer (id, note) VALUES ('FREE1', 'free box')",
		} {
			if _, err := w.DB.Exec(ctx, stmt); err != nil {
				t.Fatal(err)
			}
		}
		token := deviceToken(t, w, "tok-banned-target-01", "DEV1")
		auth := []string{"Authorization", "Bearer " + token}
		if got := w.SignedOut().Do("POST", "/api/bd/connect", `{"target_id":"BANNED1"}`, auth...); got.Status != 403 || got.Body != `{"error":"Target device is banned"}` {
			t.Errorf("connect to a banned device: %d %s", got.Status, got.Body)
		}
		if got := w.SignedOut().Do("POST", "/api/bd/connect", `{"target_id":"FREE1"}`, auth...); got.Status != 200 {
			t.Errorf("connect to a device that is not banned: %d %s", got.Status, got.Body)
		}
		for id, want := range map[string]string{
			"BANNED1": `{"success":true,"peer":{"id":"BANNED1","hostname":"banned box","banned":true}}`,
			"FREE1":   `{"success":true,"peer":{"id":"FREE1","hostname":"free box","banned":false}}`,
		} {
			if got := w.SignedOut().Do("GET", "/api/bd/peer/"+id, "", auth...); got.Status != 200 || got.Body != want {
				t.Errorf("peer %s: %d %s, want %s", id, got.Status, got.Body, want)
			}
		}
	})
}
