package bdapi_test

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

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

// A device's chat goes through the API server's chat routes, in the
// conversation named by the device: a message is stored and answered with
// its id, and the history comes back as the device API's list (BUGS BD1,
// BD2, G2).
func TestChatGoesThroughTheAPIServer(t *testing.T) {
	api := routetest.NewAPI().
		On("POST /api/chat/messages", http.StatusOK, `{"id":7,"conversation_id":"DEV1","from_id":"DEV1","from_name":"Front desk","text":"hello"}`).
		On("GET /api/chat/history/DEV1?limit=100", http.StatusOK, `{"conversation_id":"DEV1","messages":[`+
			`{"id":5,"conversation_id":"DEV1","from_id":"operator:alice","from_name":"Alice","text":"hi","read":false,"created_at":"2026-11-18T10:00:00Z"},`+
			`{"id":7,"conversation_id":"DEV1","from_id":"DEV1","from_name":"","text":"hello","read":false,"created_at":"2026-11-18T10:01:00Z"}]}`).
		On("GET /api/chat/history/DEV1?limit=1", http.StatusOK, `{"conversation_id":"DEV1","messages":[]}`)
	w := routetest.New(t, routetest.Options{API: api})
	token := deviceToken(t, w, "tok-chat-0123456789", "DEV1")
	auth := []string{"Authorization", "Bearer " + token}

	got := w.SignedOut().Do("POST", "/api/bd/chat/send", `{"device_id":"DEV1","sender":" Front desk ","content":" hello "}`, auth...)
	if got.Status != 200 || got.Body != `{"success":true,"message_id":"7"}` {
		t.Errorf("send: %d %s", got.Status, got.Body)
	}
	if calls := api.Calls(); len(calls) != 1 || calls[0] != `POST /api/chat/messages {"conversation_id":"DEV1","from_id":"DEV1","from_name":"Front desk","to_id":"","text":"hello"}` {
		t.Errorf("send called %q", calls)
	}

	got = w.SignedOut().Do("GET", "/api/bd/chat/history?device_id=DEV1", "", auth...)
	want := `[{"id":"5","device_id":"DEV1","sender":"Alice","content":"hi","timestamp":"2026-11-18T10:00:00Z"},` +
		`{"id":"7","device_id":"DEV1","sender":"DEV1","content":"hello","timestamp":"2026-11-18T10:01:00Z"}]`
	if got.Status != 200 || got.Body != want {
		t.Errorf("history: %d %s, want %s", got.Status, got.Body, want)
	}
	if got := w.SignedOut().Do("GET", "/api/bd/chat/history?device_id=DEV1&limit=1", "", auth...); got.Status != 200 || got.Body != `[]` {
		t.Errorf("history of one: %d %s", got.Status, got.Body)
	}
	if got := w.SignedOut().Do("GET", "/api/bd/chat/history", "", auth...); got.Status != 400 {
		t.Errorf("without a device id: %d %s", got.Status, got.Body)
	}
	if got := w.SignedOut().Do("GET", "/api/bd/chat/history?device_id[toString]=x", "", auth...); got.Status == 200 {
		t.Errorf("a device id without text: %d %s", got.Status, got.Body)
	}

	// The API server refuses: the send is a 502, the history empty
	refusing := routetest.New(t, routetest.Options{})
	token = deviceToken(t, refusing, "tok-chat-9876543210", "DEV1")
	auth = []string{"Authorization", "Bearer " + token}
	if got := refusing.SignedOut().Do("POST", "/api/bd/chat/send", `{"device_id":"DEV1","content":"x"}`, auth...); got.Status != 502 || got.Body != `{"error":"Failed to send message"}` {
		t.Errorf("send refused: %d %s", got.Status, got.Body)
	}
	if got := refusing.SignedOut().Do("GET", "/api/bd/chat/history?device_id=DEV1", "", auth...); got.Status != 200 || got.Body != `[]` {
		t.Errorf("history refused: %d %s", got.Status, got.Body)
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

// A stored key that is bytes counts as the device's key: 32 bytes as the
// key itself (a row copied from the signal server's table), other bytes as
// the base64 text the console stored in them. Then a registration with
// another key, or another uuid, needs a proof made with the stored key, on
// both databases (BUGS BD6).
func TestStoredKeyAsBytes(t *testing.T) {
	stored, storedPriv, _ := ed25519.GenerateKey(nil)
	other, _, _ := ed25519.GenerateKey(nil)
	storedText := base64.StdEncoding.EncodeToString(stored)
	otherText := base64.StdEncoding.EncodeToString(other)
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db})
		ctx := context.Background()
		for id, pk := range map[string][]byte{"RAWKEY1": stored, "TEXTKEY1": []byte(storedText)} {
			if _, err := w.DB.Exec(ctx, "INSERT INTO peer (id, uuid, pk, info) VALUES (?, 'uuid-1', ?, '{}')", id, pk); err != nil {
				t.Fatal(err)
			}
			register := func(body string, header ...string) string {
				got := w.SignedOut().Do("POST", "/api/bd/register", body, append([]string{"X-Device-Id", id}, header...)...)
				if got.Status == 200 {
					return "200"
				}
				return fmt.Sprint(got.Status, " ", got.Body)
			}
			refused := `403 {"error":"identity_proof_required","message":"Changing device identity requires proof of possession (missing_proof_headers)"}`
			// The stored key last: on SQLite registering writes it as text
			for _, c := range []struct{ name, body, want string }{
				{"another key", `{"public_key":"` + otherText + `","uuid":"uuid-1"}`, refused},
				{"another uuid", `{"uuid":"uuid-2"}`, refused},
				{"the stored key", `{"public_key":"` + storedText + `","uuid":"uuid-1"}`, "200"},
			} {
				if got := register(c.body); got != c.want {
					t.Errorf("%s, %s: %s, want %s", id, c.name, got, c.want)
				}
			}
			// A proof made with the stored key allows the change
			timestamp := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
			nonce := "nonce-" + id
			payload := strings.Join([]string{"bd-enrollment-v1", "POST", "/api/bd/register", id, storedText, timestamp, nonce}, "\n")
			proof := []string{"X-BD-Enrollment-Timestamp", timestamp, "X-BD-Enrollment-Nonce", nonce,
				"X-BD-Enrollment-Signature", base64.StdEncoding.EncodeToString(ed25519.Sign(storedPriv, []byte(payload)))}
			if got := register(`{"public_key":"`+otherText+`","uuid":"uuid-1"}`, proof...); got != "200" {
				t.Errorf("%s, another key with a proof of the stored one: %s", id, got)
			}
		}
	})
}

// A registration without a uuid keeps the uuid the device has, on SQLite
// as on PostgreSQL, so the identity check still compares the next uuid
// with it (BUGS BD7, the part that changes nothing for an agent).
func TestRegistrationWithoutUUIDKeepsTheStoredOne(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db})
		ctx := context.Background()
		if _, err := w.DB.Exec(ctx, "INSERT INTO peer (id, uuid, info) VALUES ('UUIDDEV1', 'uuid-1', '{}')"); err != nil {
			t.Fatal(err)
		}
		for _, body := range []string{`{}`, `{"uuid":""}`} {
			if got := w.SignedOut().Do("POST", "/api/bd/register", body, "X-Device-Id", "UUIDDEV1"); got.Status != 200 {
				t.Fatalf("%s: %d %s", body, got.Status, got.Body)
			}
			var uuid string
			if err := w.DB.QueryRow(ctx, "SELECT uuid FROM peer WHERE id = 'UUIDDEV1'").Scan(&uuid); err != nil || uuid != "uuid-1" {
				t.Errorf("%s: stored uuid %q (%v), want uuid-1", body, uuid, err)
			}
		}
		got := w.SignedOut().Do("POST", "/api/bd/register", `{"uuid":"uuid-2"}`, "X-Device-Id", "UUIDDEV1")
		if got.Status != 409 || !strings.Contains(got.Body, "identity_change_not_allowed") {
			t.Errorf("another uuid afterwards: %d %s", got.Status, got.Body)
		}
	})
}

// An approval whose request carried a key admits the first registration
// only with that key; a device registered before is not asked again
// (BUGS BD8).
func TestApprovalWithAKeyNeedsThatKey(t *testing.T) {
	reviewed, _, _ := ed25519.GenerateKey(nil)
	reviewedText := base64.StdEncoding.EncodeToString(reviewed)
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db})
		ctx := context.Background()
		for _, id := range []string{"APPKEY1", "APPKEY2"} {
			if _, err := w.DB.Exec(ctx, "INSERT INTO pending_registrations (device_id, public_key, status) VALUES (?, ?, 'approved')", id, reviewedText); err != nil {
				t.Fatal(err)
			}
		}
		register := func(id, body string) string {
			got := w.SignedOut().Do("POST", "/api/bd/register", body, "X-Device-Id", id)
			if got.Status == 200 {
				return "200"
			}
			return fmt.Sprint(got.Status, " ", got.Body)
		}
		refused := `403 {"error":"enrollment_proof_required","message":"Registration requires operator approval, an enrollment token, or an Ed25519 proof of possession"}`
		if got := register("APPKEY1", `{"uuid":"u-1"}`); got != refused {
			t.Errorf("without the reviewed key: %s", got)
		}
		if got := register("APPKEY1", `{"uuid":"u-1","public_key":"`+reviewedText+`"}`); got != "200" {
			t.Errorf("with the reviewed key: %s", got)
		}
		// Once the device is registered, it registers again as it did
		if got := register("APPKEY1", `{"uuid":"u-1"}`); got != "200" {
			t.Errorf("a registered device without its key: %s", got)
		}
		if _, err := w.DB.Exec(ctx, "INSERT INTO peer (id, uuid, info) VALUES ('APPKEY2', 'u-2', '{}')"); err != nil {
			t.Fatal(err)
		}
		if got := register("APPKEY2", `{"uuid":"u-2"}`); got != "200" {
			t.Errorf("a device registered before the approval: %s", got)
		}
	})
}

// The identity check compares uuids as the rename guard does: without
// dashes and letter case, so a device whose uuid is written otherwise is
// the same device (BUGS BD10).
func TestUUIDInOtherLetterCaseIsTheSame(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db})
		if _, err := w.DB.Exec(context.Background(), "INSERT INTO peer (id, uuid, info) VALUES ('CASEDEV1', 'ab12-cd34', '{}')"); err != nil {
			t.Fatal(err)
		}
		for body, want := range map[string]int{
			`{"uuid":"AB12CD34"}`:    200,
			`{"uuid":"ab-12-CD-34"}`: 200,
			`{"uuid":"ab12-cd35"}`:   409,
		} {
			if got := w.SignedOut().Do("POST", "/api/bd/register", body, "X-Device-Id", "CASEDEV1"); got.Status != want {
				t.Errorf("%s: %d %s, want %d", body, got.Status, got.Body, want)
			}
		}
	})
}

// The rename guard runs on both databases: an id the signal server
// renamed is refused with the new id, unless the device proves to be the
// renamed one by its key or its uuid, then it registers under the new id
// (BUGS BD5, D3).
func TestRenameGuardOnBothDatabases(t *testing.T) {
	key, _, _ := ed25519.GenerateKey(nil)
	keyText := base64.StdEncoding.EncodeToString(key)
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db})
		ctx := context.Background()
		for _, stmt := range []string{
			db.Pick("CREATE TABLE IF NOT EXISTS id_change_history (id INTEGER PRIMARY KEY AUTOINCREMENT, old_id TEXT NOT NULL, new_id TEXT NOT NULL)",
				"CREATE TABLE IF NOT EXISTS id_change_history (id SERIAL PRIMARY KEY, old_id TEXT NOT NULL, new_id TEXT NOT NULL)"),
			"INSERT INTO id_change_history (old_id, new_id) VALUES ('OLDREN1', 'GONEREN1')",
			"INSERT INTO id_change_history (old_id, new_id) VALUES ('OLDREN1', 'NEWREN1')",
			"INSERT INTO id_change_history (old_id, new_id) VALUES ('OLDREN2', 'NEWREN2')",
			"INSERT INTO peer (id, uuid, ip, info) VALUES ('NEWREN2', '', '127.0.0.1:5123', '{}')",
		} {
			if _, err := w.DB.Exec(ctx, stmt); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := w.DB.Exec(ctx, "INSERT INTO peer (id, uuid, pk, info) VALUES ('NEWREN1', 'uuid-r', ?, '{}')", keyText); err != nil {
			t.Fatal(err)
		}
		renamed := `409 {"error":"Device ID has been changed","new_id":"NEWREN1","message":"This device ID was renamed to NEWREN1. Please update your configuration."}`
		for _, c := range []struct{ name, body, want string }{
			{"another uuid", `{"uuid":"uuid-x"}`, renamed},
			{"another key", `{"public_key":"other"}`, renamed},
			{"the uuid", `{"uuid":"UUID-R"}`, "200 NEWREN1"},
			{"the key", `{"public_key":"` + keyText + `"}`, "200 NEWREN1"},
			// Neither key nor uuid: the address of the connection
			{"OLDREN2 the address", `{}`, "200 NEWREN2"},
		} {
			id, newID := "OLDREN1", "NEWREN1"
			if strings.HasPrefix(c.name, "OLDREN2") {
				id, newID = "OLDREN2", "NEWREN2"
			}
			got := w.SignedOut().Do("POST", "/api/bd/register", c.body, "X-Device-Id", id)
			have := fmt.Sprint(got.Status, " ", got.Body)
			if got.Status == 200 && strings.Contains(got.Body, `"device_id":"`+newID+`"`) {
				have = "200 " + newID
			}
			if have != c.want {
				t.Errorf("%s: %s, want %s", c.name, have, c.want)
			}
		}
	})
}

// The operator sign-in of the desktop client issues a token bound to the
// device to an admin or operator without a second factor; a wrong
// password, another role and an account with a second factor get none,
// and the lockout counts (BUGS S4, DECISIONS A37).
func TestOperatorLogin(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := routetest.New(t, routetest.Options{DB: db, Env: map[string]string{"RATE_LIMIT_MAX": "1000"}})
		ctx := context.Background()
		hash, err := w.Server.Deps().Authn.HashPassword(ctx, "op-secret-1")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.DB.Exec(ctx, "UPDATE users SET password_hash = ?", hash); err != nil {
			t.Fatal(err)
		}
		if _, err := w.DB.Exec(ctx, "UPDATE users SET totp_enabled = "+db.Pick("1", "TRUE")+", totp_secret = 'JBSWY3DPEHPK3PXP' WHERE username = 'e2e_admin'"); err != nil {
			t.Fatal(err)
		}
		login := func(username, password string) (int, string) {
			got := w.SignedOut().Do("POST", "/api/bd/operator/login", `{"username":"`+username+`","password":"`+password+`","device_id":"OPDESK1"}`)
			return got.Status, got.Body
		}
		status, body := login("e2e_operator", "op-secret-1")
		var answer struct {
			Token string `json:"access_token"`
			User  struct{ Name, Role string }
		}
		if err := json.Unmarshal([]byte(body), &answer); status != 200 || err != nil || len(answer.Token) != 64 || answer.User.Name != "e2e_operator" || answer.User.Role != "operator" {
			t.Fatalf("operator: %d %s", status, body)
		}
		// The token is bound to the device and opens the operator routes
		var client string
		if err := w.DB.QueryRow(ctx, "SELECT client_id FROM access_tokens WHERE user_id = 5").Scan(&client); err != nil || client != "OPDESK1" {
			t.Errorf("token bound to %q (%v)", client, err)
		}
		if got := w.SignedOut().Do("GET", "/api/bd/operator/sessions", "", "Authorization", "Bearer "+answer.Token); got.Status != 200 {
			t.Errorf("operator route with the token: %d %s", got.Status, got.Body)
		}
		for _, c := range []struct{ user, password, want string }{
			{"e2e_admin", "op-secret-1", `403 {"error":"Two-factor authentication is required for this account","code":"totp_required"}`},
			{"e2e_viewer", "op-secret-1", `403 {"error":"Insufficient permissions. Admin or operator role required."}`},
			{"e2e_operator", "wrong", `401 {"error":"Invalid credentials"}`},
		} {
			if status, body := login(c.user, c.password); fmt.Sprint(status, " ", body) != c.want {
				t.Errorf("%s: %d %s, want %s", c.user, status, body, c.want)
			}
		}
		if rows := w.AuditRows(); len(rows) != 1 || !strings.HasPrefix(rows[0], "operator_login|Operator login from desktop client (OPDESK1)|") {
			t.Errorf("audit %q", rows)
		}
		// Failed attempts lock the account
		for i := 0; i < 10; i++ {
			login("e2e_operator", "wrong")
		}
		if status, body := login("e2e_operator", "op-secret-1"); status != 429 || body != `{"error":"Too many failed attempts. Try again later."}` {
			t.Errorf("locked: %d %s", status, body)
		}
	})
}
