package rustdeskapi_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/devicegroups"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// What the Node recordings cannot pin down by themselves: which requests
// the client API leaves to the console routes, the second step of the
// sign-in, the TOTP bypass, the pass-through of the telemetry key, the key
// fingerprint and the audit lists the API server holds.

const viewerToken = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

func world(t *testing.T, db *store.DB, configure func(cfg *config.Config)) *replay.World {
	t.Helper()
	w := replay.NewWorld(t, db, configure)
	tokenFor(w, db, 6, viewerToken)
	return w
}

// tokenFor stores an access token of the user that does not run out.
func tokenFor(w *replay.World, db *store.DB, userID int, token string) {
	sum := sha256.Sum256([]byte(token))
	revoked := any(0)
	if db.IsPostgres() {
		revoked = false
	}
	w.Exec("INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at, revoked) VALUES (?, ?, ?, 'c', 'u', '2999-01-01 00:00:00', ?)",
		[]any{token, string(hexOf(sum[:])), userID, revoked})
}

func hexOf(b []byte) []byte {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&15])
	}
	return out
}

func bearer(token string) map[string]string {
	return map[string]string{"Authorization": "Bearer " + token}
}

// Without a bearer token the five routes step aside: the console's route
// at that address answers, or the console's 404 where it has none. With a
// token they answer themselves.
func TestTheFiveRoutesLeaveRequestsWithoutABearerToken(t *testing.T) {
	const signedOut = `{"success":false,"error":"Unauthorized. Please log in.","code":"session_required"}`
	// The console has routes of its own at two of the five addresses
	own := map[string]bool{"/api/users": true, "/api/devices": true}
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db, nil)
		for _, path := range []string{"/api/users", "/api/peers", "/api/strategies", "/api/strategies/x", "/api/devices"} {
			for _, headers := range []map[string]string{nil, {"Authorization": "Basic eDp5"}, {"Authorization": "Bearer    "}, {"Authorization": "bearer x"}} {
				got := w.Do(replay.Request{Path: path, Headers: headers})
				if own[path] && (got.Status != http.StatusUnauthorized || got.Body != signedOut) {
					t.Errorf("%s with %v: %d %s, want the console route's 401", path, headers, got.Status, got.Body)
				}
				if !own[path] && got.Status != http.StatusNotFound {
					t.Errorf("%s with %v: status %d, want the console's 404: %s", path, headers, got.Status, got.Body)
				}
			}
			if got := w.Do(replay.Request{Path: path, Headers: bearer("contract-no-such-token")}); got.Status != http.StatusUnauthorized || got.Body == signedOut {
				t.Errorf("%s with an unknown token: %d %s, want the client API's 401", path, got.Status, got.Body)
			}
		}
		// A console route at one of the other three addresses gets the
		// browser's request too, and the client API keeps the client's
		for _, path := range []string{"/api/peers", "/api/strategies", "/api/strategies/{guid}"} {
			w.Server.Router().Get(path, func(rw http.ResponseWriter, r *http.Request) {
				rw.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(rw, `{"console":true}`)
			})
		}
		for _, path := range []string{"/api/users", "/api/peers", "/api/strategies", "/api/strategies/x", "/api/devices"} {
			got := w.Do(replay.Request{Role: "admin", Path: path})
			if own[path] && (got.Status != http.StatusOK || !strings.HasPrefix(got.Body, `{"success":true,"data":{`)) {
				t.Errorf("%s from a browser: %d %s, want the console route", path, got.Status, got.Body)
			}
			if !own[path] && (got.Status != http.StatusOK || got.Body != `{"console":true}`) {
				t.Errorf("%s from a browser: %d %s, want the console route", path, got.Status, got.Body)
			}
			if got := w.Do(replay.Request{Role: "admin", Path: path, Headers: bearer("contract-no-such-token")}); got.Status != http.StatusUnauthorized {
				t.Errorf("%s from a client with a session: %d %s, want the client API's 401", path, got.Status, got.Body)
			}
		}
		if got := w.Do(replay.Request{Path: "/api/devices", Headers: bearer(viewerToken)}); got.Status != http.StatusOK || got.Body != `{"total":0,"data":[]}` {
			t.Errorf("GET /api/devices with a token: %d %s", got.Status, got.Body)
		}
		// The token is what follows "Bearer " with the spaces around it
		// trimmed; without the space after "Bearer" there is no token
		if got := w.Do(replay.Request{Path: "/api/devices", Headers: map[string]string{"Authorization": "Bearer   " + viewerToken + "  "}}); got.Status != http.StatusOK || got.Body != `{"total":0,"data":[]}` {
			t.Errorf("GET /api/devices with spaces around the token: %d %s", got.Status, got.Body)
		}
		if got := w.Do(replay.Request{Path: "/api/devices", Headers: map[string]string{"Authorization": "Bearer" + viewerToken}}); got.Body != signedOut {
			t.Errorf("GET /api/devices with no space after Bearer: %d %s, want the console route", got.Status, got.Body)
		}
	})
}

var secretField = regexp.MustCompile(`"secret":"([0-9a-f]{32})"`)

// The second step takes the code of the account's second factor. A wrong
// code is refused, counts as a failed sign-in and keeps the secret, so the
// client can ask again; the right code signs in once, and only for a
// secret that is text.
func TestSecondStepRefusesAWrongCode(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db, nil)
		setTOTP(t, w, db)
		first := w.Do(replay.Request{Method: "POST", Path: "/api/login", Body: replay.Text(`{"username":"e2e_viewer","password":"Rd-Test-1!x","id":"cl-1","uuid":"u1"}`)})
		m := secretField.FindStringSubmatch(first.Body)
		if first.Status != http.StatusOK || m == nil || !strings.Contains(first.Body, `"type":"email_check","tfa_type":"tfa_check"`) {
			t.Fatalf("password step: %d %s", first.Status, first.Body)
		}
		if got := w.Do(replay.Request{Method: "POST", Path: "/api/login", Body: replay.Text(`{"tfaCode":"000000","secret":5}`)}); got.Status != http.StatusUnauthorized {
			t.Errorf("a secret that is no text: %d %s", got.Status, got.Body)
		}
		for _, body := range []string{
			`{"verificationCode":"` + wrongCode(t, totpSecret) + `","secret":"` + m[1] + `"}`,
			`{"tfaCode":123456,"secret":"` + m[1] + `"}`,
		} {
			got := w.Do(replay.Request{Method: "POST", Path: "/api/login", Body: replay.Text(body)})
			if got.Status != http.StatusUnauthorized || got.Body != `{"error":"Invalid verification code"}` {
				t.Errorf("second step %s: %d %s", body, got.Status, got.Body)
			}
		}
		var action, details string
		if err := db.QueryRow(context.Background(), "SELECT action, details FROM audit_log ORDER BY id DESC LIMIT 1").Scan(&action, &details); err != nil || action != "api_tfa_failed" || details != "Client: cl-1" {
			t.Errorf("audit entry of a wrong code: %q %q, %v", action, details, err)
		}
		var failed int
		if err := db.QueryRow(context.Background(), "SELECT COUNT(*) FROM login_attempts WHERE username = 'e2e_viewer' AND NOT success").Scan(&failed); err != nil || failed != 2 {
			t.Errorf("failed attempts recorded: %d, %v", failed, err)
		}
		second := w.Do(replay.Request{Method: "POST", Path: "/api/login", Body: replay.Text(`{"tfaCode":"` + currentCode(t, totpSecret) + `","secret":"` + m[1] + `"}`)})
		if second.Status != http.StatusOK || !strings.Contains(second.Body, `"type":"access_token"`) || !strings.Contains(second.Body, `"name":"e2e_viewer"`) {
			t.Fatalf("second step with the right code after wrong ones: %d %s", second.Status, second.Body)
		}
		if got := w.Do(replay.Request{Method: "POST", Path: "/api/login", Body: replay.Text(`{"tfaCode":"` + currentCode(t, totpSecret) + `","secret":"` + m[1] + `"}`)}); got.Status != http.StatusUnauthorized || got.Body != `{"error":"TFA session expired or invalid"}` {
			t.Errorf("a used secret: %d %s", got.Status, got.Body)
		}
		// The token of the second step names the client of the first
		var client string
		if err := db.QueryRow(context.Background(), "SELECT client_id || '/' || client_uuid FROM access_tokens WHERE user_id = 6 AND client_id = 'cl-1'").Scan(&client); err != nil || client != "cl-1/u1" {
			t.Errorf("the token's client: %q, %v", client, err)
		}
	})
}

// The second step checks the lockout of the account and of the address
// before the code, as the password step does (BUGS RD13): once the wrong
// codes have locked the account, or the address has too many failed
// sign-ins, the right code for a pending secret is refused with 429 too.
func TestSecondStepChecksTheLockout(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		code := func(w *replay.World, secret, code string) replay.Answer {
			return w.Do(replay.Request{Method: "POST", Path: "/api/login", Body: replay.Text(`{"tfaCode":"` + code + `","secret":"` + secret + `"}`)})
		}
		pending := func(w *replay.World) string {
			first := w.Do(replay.Request{Method: "POST", Path: "/api/login", Body: replay.Text(`{"username":"e2e_viewer","password":"Rd-Test-1!x","id":"cl-1","uuid":"u1"}`)})
			m := secretField.FindStringSubmatch(first.Body)
			if first.Status != http.StatusOK || m == nil {
				t.Fatalf("password step: %d %s", first.Status, first.Body)
			}
			return m[1]
		}
		refused := func(got replay.Answer, reason string) bool {
			var body struct {
				Error      string `json:"error"`
				RetryAfter *int64 `json:"retry_after"`
			}
			return got.Status == http.StatusTooManyRequests && json.Unmarshal([]byte(got.Body), &body) == nil &&
				body.Error == reason && body.RetryAfter != nil && *body.RetryAfter > 0
		}
		tokens := func() int {
			var n int
			if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM access_tokens WHERE user_id = 6 AND client_id = 'cl-1'").Scan(&n); err != nil {
				t.Fatal(err)
			}
			return n
		}

		w := world(t, db, nil)
		setTOTP(t, w, db)

		t.Run("account", func(t *testing.T) {
			secret := pending(w)
			for i := 1; i <= 10; i++ {
				if got := code(w, secret, wrongCode(t, totpSecret)); got.Status != http.StatusUnauthorized || got.Body != `{"error":"Invalid verification code"}` {
					t.Fatalf("wrong code %d: %d %s", i, got.Status, got.Body)
				}
			}
			if got := code(w, secret, wrongCode(t, totpSecret)); !refused(got, "Account temporarily locked due to too many failed attempts") {
				t.Errorf("a wrong code on a locked account: %d %s", got.Status, got.Body)
			}
			if got := code(w, secret, currentCode(t, totpSecret)); !refused(got, "Account temporarily locked due to too many failed attempts") {
				t.Errorf("the right code on a locked account: %d %s", got.Status, got.Body)
			}
			if n := tokens(); n != 0 {
				t.Errorf("%d tokens issued to a locked account", n)
			}
			var action, details string
			if err := db.QueryRow(ctx, "SELECT action, details FROM audit_log ORDER BY id DESC LIMIT 1").Scan(&action, &details); err != nil ||
				action != "api_login_blocked" || !strings.HasSuffix(details, ", Reason: Account temporarily locked due to too many failed attempts") {
				t.Errorf("audit entry of a blocked second step: %q %q, %v", action, details, err)
			}
		})

		t.Run("address", func(t *testing.T) {
			if _, err := db.Exec(ctx, "DELETE FROM login_attempts"); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, "DELETE FROM account_lockouts"); err != nil {
				t.Fatal(err)
			}
			secret := pending(w)
			if got := code(w, secret, wrongCode(t, totpSecret)); got.Status != http.StatusUnauthorized {
				t.Fatalf("wrong code: %d %s", got.Status, got.Body)
			}
			var ip string
			if err := db.QueryRow(ctx, "SELECT ip_address FROM login_attempts ORDER BY id DESC LIMIT 1").Scan(&ip); err != nil || ip == "" {
				t.Fatalf("the address of the failed attempt: %q, %v", ip, err)
			}
			failed := any(0)
			if db.IsPostgres() {
				failed = false
			}
			for i := 0; i < 30; i++ {
				if _, err := db.Exec(ctx, "INSERT INTO login_attempts (username, ip_address, success) VALUES (?, ?, ?)", "someone-else", ip, failed); err != nil {
					t.Fatal(err)
				}
			}
			if got := code(w, secret, currentCode(t, totpSecret)); !refused(got, "Too many failed attempts from this IP address") {
				t.Errorf("the right code from a blocked address: %d %s", got.Status, got.Body)
			}
			if n := tokens(); n != 0 {
				t.Errorf("%d tokens issued from a blocked address", n)
			}
		})
	})
}

// totpSecret is the second factor setTOTP gives the viewer.
const totpSecret = "JBSWY3DPEHPK3PXP"

func setTOTP(t *testing.T, w *replay.World, db *store.DB) {
	t.Helper()
	hash, err := w.Server.Deps().Authn.HashPassword(context.Background(), "Rd-Test-1!x")
	if err != nil {
		t.Fatal(err)
	}
	enabled := any(1)
	if db.IsPostgres() {
		enabled = true
	}
	w.Exec("UPDATE users SET password_hash = ?, totp_enabled = ?, totp_secret = ? WHERE id = 6", []any{hash, enabled, totpSecret})
}

// totpAt is the code of a base32 secret for the step of 30 seconds that
// holds the time (RFC 6238, HMAC-SHA1, six digits).
func totpAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatalf("the secret is not base32: %v", err)
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(at.Unix()/30))
	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", value%1000000)
}

// currentCode is the code of the step that is running; near the end of a
// step this waits for the next.
func currentCode(t *testing.T, secret string) string {
	t.Helper()
	if left := 30 - time.Now().Unix()%30; left <= 4 {
		time.Sleep(time.Duration(left)*time.Second + 200*time.Millisecond)
	}
	return totpAt(t, secret, time.Now())
}

// wrongCode is six digits that are not the code of this step or the next.
func wrongCode(t *testing.T, secret string) string {
	t.Helper()
	now := time.Now()
	for _, candidate := range []string{"000000", "111111", "222222"} {
		if candidate != totpAt(t, secret, now) && candidate != totpAt(t, secret, now.Add(30*time.Second)) {
			return candidate
		}
	}
	return "333333"
}

// RUSTDESK_API_DISABLE_TOTP skips the second factor only together with its
// acknowledgement.
func TestTheTOTPBypassNeedsItsAcknowledgement(t *testing.T) {
	for _, c := range []struct {
		name   string
		env    map[string]string
		bypass bool
	}{
		{"without-acknowledgement", map[string]string{"RUSTDESK_API_DISABLE_TOTP": "TRUE"}, false},
		{"acknowledgement-alone", map[string]string{"RUSTDESK_API_DISABLE_TOTP_ACKNOWLEDGED": "true"}, false},
		{"both", map[string]string{"RUSTDESK_API_DISABLE_TOTP": "true", "RUSTDESK_API_DISABLE_TOTP_ACKNOWLEDGED": "True"}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			db := storetest.Migrated(t)
			w := world(t, db, func(cfg *config.Config) {
				getenv := cfg.Getenv
				cfg.Getenv = func(key string) string {
					if v, ok := c.env[key]; ok {
						return v
					}
					return getenv(key)
				}
			})
			setTOTP(t, w, db)
			got := w.Do(replay.Request{Method: "POST", Path: "/api/login", Body: replay.Text(`{"username":"e2e_viewer","password":"Rd-Test-1!x","id":"c9"}`)})
			if bypassed := strings.Contains(got.Body, `"type":"access_token"`); bypassed != c.bypass {
				t.Errorf("%v: %d %s", c.env, got.Status, got.Body)
			}
			var action string
			_ = db.QueryRow(context.Background(), "SELECT action FROM audit_log ORDER BY id DESC LIMIT 1").Scan(&action)
			want := "api_login_tfa_required"
			if c.bypass {
				want = "api_login_success_totp_bypassed"
			}
			if action != want {
				t.Errorf("%v: audit action %q, want %q", c.env, action, want)
			}
		})
	}
}

// fakeAPI answers what a test sets and keeps what it was asked.
type fakeAPI struct {
	mu     sync.Mutex
	asked  []string
	answer func(w http.ResponseWriter, r *http.Request)
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	f.mu.Lock()
	f.asked = append(f.asked, r.Method+" "+r.URL.RequestURI())
	f.mu.Unlock()
	f.answer(w, r)
}

func useAPI(w *replay.World, api http.Handler) {
	d := w.Server.Deps()
	d.GoAPI = goapi.New(api)
	d.Backend = goapi.NewBackend(d.GoAPI, devicegroups.DeviceStore{Groups: d.DeviceGroups})
}

// GET /api/telemetry/key is the API server's answer: its status, headers
// and body, without the console's cache header and ETag.
func TestTheTelemetryKeyIsTheAPIServersAnswer(t *testing.T) {
	db := storetest.Migrated(t)
	w := world(t, db, nil)
	api := &fakeAPI{answer: func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Real-Ip") != "203.0.113.7" || r.Header.Get("X-Forwarded-For") != "203.0.113.7" {
			w.WriteHeader(http.StatusTeapot)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Key-Version", "1")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"key_id":"k","version":1}`)
	}}
	useAPI(w, api)
	got := w.Do(replay.Request{Path: "/api/telemetry/key?v=1"})
	if got.Status != http.StatusCreated || got.Body != `{"key_id":"k","version":1}` || got.Header.Get("X-Key-Version") != "1" ||
		got.Header.Get("Cache-Control") != "no-store" || got.Header.Get("ETag") != "" {
		t.Errorf("telemetry key: %d %v %s", got.Status, got.Header, got.Body)
	}
	if len(api.asked) != 1 || api.asked[0] != "GET /api/telemetry/key?v=1" {
		t.Errorf("the API server was asked %v", api.asked)
	}
}

// The fingerprint is the SHA-256 of the key's bytes in pairs of upper-case
// hex digits.
func TestTheKeyFingerprint(t *testing.T) {
	db := storetest.Migrated(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "id_ed25519.pub")
	if err := os.WriteFile(file, []byte("9rVmB1qx9o6SbZGnm4FyF417DnTbJ0SjnnnRHQ2X+3Q=\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := world(t, db, func(cfg *config.Config) { cfg.PubKeyPath = file })
	if got := w.Do(replay.Request{Path: "/api/server-key"}); got.Body != `{"key":"9rVmB1qx9o6SbZGnm4FyF417DnTbJ0SjnnnRHQ2X+3Q="}` {
		t.Errorf("server key: %s", got.Body)
	}
	got := w.Do(replay.Request{Path: "/api/server-key/fingerprint"})
	want := `{"fingerprint":"53:C9:9B:3F:10:AE:8F:05:00:CA:07:58:D7:96:09:3D:1B:C4:F0:A9:2E:A0:4B:4F:17:F0:DA:FE:0E:D1:17:73","algorithm":"SHA-256"}`
	if got.Body != want {
		t.Errorf("fingerprint: %s\n want %s", got.Body, want)
	}
}

// Without its own client API port the console reads the audit lists from
// the API server, and from its own tables when that call fails.
func TestTheAuditListsComeFromTheAPIServer(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db, func(cfg *config.Config) { cfg.APIEnabled = false })
		answer := `{"data":[{"z":1,"a":2}],"total":7}`
		api := &fakeAPI{answer: func(w http.ResponseWriter, r *http.Request) {
			if answer == "" {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			_, _ = io.WriteString(w, answer)
		}}
		useAPI(w, api)
		got := w.Do(replay.Request{Path: "/api/audit/conn?host_id=h1&limit=5&offset=2", Headers: bearer(viewerToken)})
		if got.Body != `{"data":[{"z":1,"a":2}],"total":7}` {
			t.Errorf("from the API server: %s", got.Body)
		}
		if len(api.asked) != 1 || api.asked[0] != "GET /api/audit/conn?host_id=h1&peer_id=&action=&limit=5&offset=2" {
			t.Errorf("the API server was asked %v", api.asked)
		}
		got = w.Do(replay.Request{Path: "/api/audit", Headers: bearer(viewerToken)})
		if got.Body != `{"data":{"connections":[{"z":1,"a":2}],"files":[{"z":1,"a":2}],"alarms":[{"z":1,"a":2}]}}` {
			t.Errorf("the summary from the API server: %s", got.Body)
		}
		answer = ""
		w.Exec("INSERT INTO audit_alarms (alarm_type, alarm_name, host_id) VALUES (3, 'n', 'h')", nil)
		got = w.Do(replay.Request{Path: "/api/audit/alarm?alarm_type=3", Headers: bearer(viewerToken)})
		var list struct {
			Data  []map[string]any `json:"data"`
			Total float64          `json:"total"`
		}
		if err := json.Unmarshal([]byte(got.Body), &list); err != nil || list.Total != 1 || len(list.Data) != 1 || list.Data[0]["alarm_name"] != "n" {
			t.Errorf("from the console's table when the API server fails: %s", got.Body)
		}
	})
}

// A sign-in without a user name or without a password is refused before
// anything else, and an account locked for the client's address (ten
// failed sign-ins from there) gets 429 with the lockout's end.
func TestTheSignInRefusals(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db, nil)
		for _, body := range []string{`{"username":"e2e_viewer","password":""}`, `{"username":"  ","password":"x"}`, `{"username":"e2e_viewer"}`} {
			if got := w.Do(replay.Request{Method: "POST", Path: "/api/login", Body: replay.Text(body)}); got.Status != http.StatusBadRequest || got.Body != `{"error":"Missing credentials"}` {
				t.Errorf("%s: %d %s", body, got.Status, got.Body)
			}
		}
		for i := 1; i <= 10; i++ {
			if got := w.Do(replay.Request{Method: "POST", Path: "/api/login", Body: replay.Text(`{"username":"e2e_viewer","password":"x"}`)}); got.Status != http.StatusUnauthorized {
				t.Fatalf("failure %d: %d %s", i, got.Status, got.Body)
			}
		}
		got := w.Do(replay.Request{Method: "POST", Path: "/api/login", Body: replay.Text(`{"username":"e2e_viewer","password":"x"}`)})
		if got.Status != http.StatusTooManyRequests || !strings.HasPrefix(got.Body, `{"error":"`) || !strings.Contains(got.Body, `"retry_after":`) {
			t.Errorf("a locked account: %d %s", got.Status, got.Body)
		}
		var action string
		_ = db.QueryRow(context.Background(), "SELECT action FROM audit_log ORDER BY id DESC LIMIT 1").Scan(&action)
		if action != "api_login_blocked" {
			t.Errorf("audit action %q, want api_login_blocked", action)
		}
	})
}

// An account whose role may not see the device list by the built-in role
// map (an old "user" account) gets only the devices of its address book; a
// viewer gets them all.
func TestAnAccountWithoutTheDeviceListSeesItsAddressBook(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db, nil)
		const guestToken = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
		w.Exec("INSERT INTO users (id, username, password_hash, role) VALUES (20, 'rd_old_user', 'x', 'user')", nil)
		tokenFor(w, db, 20, guestToken)
		useAPI(w, &fakeAPI{answer: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/api/peers":
				_, _ = io.WriteString(w, `[{"id":"900200001","hostname":"a"},{"id":"900200002","hostname":"b"}]`)
			case "/api/remote-targets":
				_, _ = io.WriteString(w, `[]`)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}})
		ids := func(token string) string {
			got := w.Do(replay.Request{Path: "/api/peers", Headers: bearer(token)})
			var list struct {
				Data []struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(got.Body), &list); err != nil {
				t.Fatalf("GET /api/peers: %d %s", got.Status, got.Body)
			}
			var out []string
			for _, d := range list.Data {
				out = append(out, d.ID)
			}
			return strings.Join(out, ",")
		}
		if got := ids(viewerToken); got != "900200001,900200002" {
			t.Errorf("a viewer sees %q", got)
		}
		if got := ids(guestToken); got != "" {
			t.Errorf("without an address book the account sees %q", got)
		}
		w.Do(replay.Request{Method: "POST", Path: "/api/ab", Headers: bearer(guestToken), Body: replay.Text(`{"data":"{\"tags\":[],\"peers\":[{\"id\":\"900200002\"}]}"}`)})
		if got := ids(guestToken); got != "900200002" {
			t.Errorf("with one device in the address book the account sees %q", got)
		}
	})
}

// A folder lists its devices in the order of the keys of a JavaScript
// object: ids that are array indexes by value, then the others as stored.
func TestAFolderListsItsDevicesInObjectKeyOrder(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db, nil)
		w.Exec("INSERT INTO folders (id, name) VALUES (1, 'F')", nil)
		for _, id := range []string{"abc", "900100009", "x1", "900100002"} {
			w.Exec("INSERT INTO device_folder_assignments (device_id, folder_id) VALUES (?, 1)", []any{id})
		}
		got := w.Do(replay.Request{Path: "/api/device-group", Headers: bearer(viewerToken)})
		var list struct {
			Data []struct {
				GUID string `json:"guid"`
				Team struct {
					Peers []struct {
						ID string `json:"id"`
					} `json:"peers"`
				} `json:"team"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(got.Body), &list); err != nil {
			t.Fatalf("GET /api/device-group: %d %s", got.Status, got.Body)
		}
		var ids []string
		for _, g := range list.Data {
			if g.GUID == "folder_1" {
				for _, p := range g.Team.Peers {
					ids = append(ids, p.ID)
				}
			}
		}
		if strings.Join(ids, ",") != "900100002,900100009,abc,x1" {
			t.Errorf("the folder's devices: %v in %s", ids, got.Body)
		}
	})
}

// System information over the size limits is cut: more than 4096 UTF-16
// units of displays keeps four, of encodings five, and features that long
// are dropped. Short lists are kept whole.
func TestTheSystemInformationLimits(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db, nil)
		w.Exec("INSERT INTO peer (id) VALUES ('900300002')", nil)
		registerDevice(t, db, "900300002", "u-2")
		long := func(n int) string {
			items := make([]string, n)
			for i := range items {
				items[i] = `"` + strings.Repeat("d", 1000) + `"`
			}
			return "[" + strings.Join(items, ",") + "]"
		}
		stored := func() (displays, encoding int, features string) {
			var d, e string
			if err := db.QueryRow(context.Background(), "SELECT displays, encoding, features FROM peer_sysinfo WHERE peer_id = '900300002'").Scan(&d, &e, &features); err != nil {
				t.Fatal(err)
			}
			var dl, el []any
			if json.Unmarshal([]byte(d), &dl) != nil || json.Unmarshal([]byte(e), &el) != nil {
				t.Fatalf("stored lists %q %q", d, e)
			}
			return len(dl), len(el), strings.ReplaceAll(features, " ", "") // jsonb adds a space
		}
		body := `{"id":"900300002","uuid":"u-2","hostname":"h","displays":` + long(5) + `,"encoding":` + long(6) + `,"features":{"a":"` + strings.Repeat("f", 5000) + `"}}`
		if got := w.Do(replay.Request{Method: "POST", Path: "/api/sysinfo", Body: replay.Text(body)}); got.Body != "SYSINFO_UPDATED" {
			t.Fatalf("sysinfo: %d %s", got.Status, got.Body)
		}
		if d, e, f := stored(); d != 4 || e != 5 || f != "{}" {
			t.Errorf("over the limits: %d displays, %d encodings, features %.40q", d, e, f)
		}
		body = `{"id":"900300002","uuid":"u-2","hostname":"h","displays":["a","b","c","d","e"],"encoding":["a","b","c","d","e","f"],"features":{"a":1}}`
		w.Do(replay.Request{Method: "POST", Path: "/api/sysinfo", Body: replay.Text(body)})
		if d, e, f := stored(); d != 5 || e != 6 || f != `{"a":1}` {
			t.Errorf("under the limits: %d displays, %d encodings, features %q", d, e, f)
		}
	})
}

// The version of the stored system information is the first 16 hex
// digits of the SHA-256 of the stored JSON: the same until the device
// uploads other information, and the empty text for a device without
// any, so the client uploads.
func TestSysinfoVersionIsTheStoredHash(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db, nil)
		w.Exec("INSERT INTO peer (id) VALUES ('900300003')", nil)
		registerDevice(t, db, "900300003", "u-3")
		version := func(body string) string {
			got := w.Do(replay.Request{Method: "POST", Path: "/api/sysinfo_ver", Body: replay.Text(body)})
			if got.Status != http.StatusOK || !strings.HasPrefix(got.Header.Get("Content-Type"), "text/plain") {
				t.Fatalf("sysinfo_ver %s: %d %s %s", body, got.Status, got.Header.Get("Content-Type"), got.Body)
			}
			return got.Body
		}
		upload := func(hostname string) {
			if got := w.Do(replay.Request{Method: "POST", Path: "/api/sysinfo", Body: replay.Text(`{"id":"900300003","uuid":"u-3","hostname":"` + hostname + `","os":"Linux"}`)}); got.Body != "SYSINFO_UPDATED" {
				t.Fatalf("sysinfo: %d %s", got.Status, got.Body)
			}
		}
		if got := version(`{"id":"900300003"}`); got != "" {
			t.Errorf("without stored information: %q", got)
		}
		upload("first")
		first := version(`{"id":"900300003"}`)
		if !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(first) || version(`{"uuid":"900300003"}`) != first {
			t.Errorf("the version of stored information: %q", first)
		}
		// The stored JSON with its keys in order, so both databases give
		// the same version (PostgreSQL keeps jsonb in an order of its own)
		var raw string
		if err := db.QueryRow(context.Background(), "SELECT CAST(raw_json AS TEXT) FROM peer_sysinfo WHERE peer_id = '900300003'").Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var parsed any
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			t.Fatal(err)
		}
		sorted, _ := json.Marshal(parsed)
		if sum := sha256.Sum256(sorted); first != string(hexOf(sum[:]))[:16] || first != "4379a2be5012a53b" {
			t.Errorf("version %q of %s", first, raw)
		}
		upload("second")
		if second := version(`{"id":"900300003"}`); second == first || len(second) != 16 {
			t.Errorf("the version after other information: %q, before %q", second, first)
		}
		for _, body := range []string{`{}`, `{"id":"900300099"}`, `{"id":"bad id"}`} {
			if got := version(body); got != "" {
				t.Errorf("%s: %q", body, got)
			}
		}
	})
}

// The device list has at most 200 devices a page, and a device in folder
// 0 is listed with its device group (0 is no folder).
func TestThePeerList(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db, nil)
		const adminToken = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
		tokenFor(w, db, 1, adminToken)
		devices := []string{`{"id":"900200001","tags":"lab"}`}
		for i := 2; i <= 250; i++ {
			devices = append(devices, fmt.Sprintf(`{"id":"9002%05d"}`, i))
		}
		useAPI(w, &fakeAPI{answer: func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/api/peers":
				_, _ = io.WriteString(w, "["+strings.Join(devices, ",")+"]")
			case "/api/remote-targets":
				_, _ = io.WriteString(w, `[]`)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}})
		w.Exec("INSERT INTO folders (id, name) VALUES (0, 'Zero')", nil)
		w.Exec("INSERT INTO device_folder_assignments (device_id, folder_id) VALUES ('900200001', 0)", nil)
		if got := w.Do(replay.Request{Method: "POST", Path: "/api/device-group", Headers: bearer(adminToken), Body: replay.Text(`{"name":"Lab","source_type":"tag","tag_filter":"lab"}`)}); got.Status != http.StatusOK {
			t.Fatalf("create the group: %d %s", got.Status, got.Body)
		}
		got := w.Do(replay.Request{Path: "/api/peers?pageSize=500", Headers: bearer(viewerToken)})
		var list struct {
			Total int `json:"total"`
			Data  []struct {
				ID    string `json:"id"`
				Group string `json:"device_group_name"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(got.Body), &list); err != nil {
			t.Fatalf("GET /api/peers: %d %.200s", got.Status, got.Body)
		}
		if list.Total != 250 || len(list.Data) != 200 {
			t.Errorf("total %d with %d on the page, want 250 and 200", list.Total, len(list.Data))
		}
		group := "(not on the page)"
		for _, d := range list.Data {
			if d.ID == "900200001" {
				group = d.Group
			}
		}
		if group != "Lab" {
			t.Errorf("the device in folder 0 is listed with group %q, want Lab", group)
		}
	})
}

// Someone signed in to the console reads the client connection records
// only with audit.view: a guest issuer and a role that lost it get 403, a
// viewer and an admin the list. A client with a token is asked for
// audit.view of its account too (BUGS RV4).
func TestConnectionRecordsNeedAuditView(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := replay.NewWorldWithRoles(t, db, nil, replay.RoleOverrides{"operator": {"audit.view": false}})
		const issuerToken = "9999999999999999999999999999999999999999999999999999999999999999"
		tokenFor(w, db, 7, issuerToken)
		const list = `{"data":[],"total":0}`
		const refused = `{"success":false,"error":"Permission denied: audit.view"}`
		for role, want := range map[string]string{"admin": list, "viewer": list, "guest_issuer": refused, "operator": refused} {
			status := http.StatusOK
			if want == refused {
				status = http.StatusForbidden
			}
			if got := w.Do(replay.Request{Role: role, Path: "/api/audit/conn?host_id=h1"}); got.Status != status || got.Body != want {
				t.Errorf("as %s: %d %s", role, got.Status, got.Body)
			}
		}
		if got := w.Do(replay.Request{Path: "/api/audit/conn", Headers: bearer(issuerToken)}); got.Status != http.StatusForbidden || got.Body != `{"error":"Permission denied: audit.view"}` {
			t.Errorf("a guest issuer's client with a token: %d %s", got.Status, got.Body)
		}
	})
}

// The audit uploads take connection types 0 to 4 and alarm types 0 to 6.
func TestTheAuditTypes(t *testing.T) {
	db := storetest.Migrated(t)
	w := world(t, db, nil)
	registerDevice(t, db, "h", "u-h")
	for _, c := range []struct {
		path, body string
		ok         bool
	}{
		{"/api/audit/conn", `{"host_id":"h","uuid":"u-h","conn_type":0}`, true},
		{"/api/audit/conn", `{"host_id":"h","uuid":"u-h","conn_type":4}`, true},
		{"/api/audit/conn", `{"host_id":"h","uuid":"u-h","conn_type":5}`, false},
		{"/api/audit/conn", `{"host_id":"h","uuid":"u-h","conn_type":-1}`, false},
		{"/api/audit/alarm", `{"alarm_type":0,"host_id":"h","uuid":"u-h"}`, true},
		{"/api/audit/alarm", `{"alarm_type":6,"host_id":"h","uuid":"u-h"}`, true},
		{"/api/audit/alarm", `{"alarm_type":7,"host_id":"h","uuid":"u-h"}`, false},
	} {
		got := w.Do(replay.Request{Method: "POST", Path: c.path, Body: replay.Text(c.body)})
		if ok := got.Status == http.StatusOK && got.Body == "{}"; ok != c.ok {
			t.Errorf("%s %s: %d %s", c.path, c.body, got.Status, got.Body)
		}
	}
}

// A pro account gets no device's key; other accounts get the stored one.
func TestAProAccountGetsNoDeviceKey(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db, nil)
		const proToken = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
		tokenFor(w, db, 8, proToken)
		w.Exec("INSERT INTO peer (id, pk) VALUES ('900300001', ?)", []any{[]byte{1, 2, 3}})
		if got := w.Do(replay.Request{Path: "/api/peer-key/900300001", Headers: bearer(viewerToken)}); got.Body != `{"id":"900300001","pk":{"type":"Buffer","data":[1,2,3]}}` {
			t.Errorf("as a viewer: %d %s", got.Status, got.Body)
		}
		if got := w.Do(replay.Request{Path: "/api/peer-key/900300001", Headers: bearer(proToken)}); got.Body != `{"id":"900300001","pk":""}` {
			t.Errorf("as a pro account: %d %s", got.Status, got.Body)
		}
	})
}

// The guards: no token is 401, a token of another role than an admin's is
// 403 on the writes, and the check comes before the body is read.
func TestTheGuardsOfTheWrites(t *testing.T) {
	db := storetest.Migrated(t)
	w := world(t, db, nil)
	for _, path := range []string{"/api/device-group", "/api/user-groups", "/api/strategies", "/api/strategies/assign"} {
		if got := w.Do(replay.Request{Method: "POST", Path: path, Body: replay.Text(`{}`)}); got.Status != 401 || got.Body != `{"error":"Authorization required"}` {
			t.Errorf("%s without a token: %d %s", path, got.Status, got.Body)
		}
		if got := w.Do(replay.Request{Method: "POST", Path: path, Body: replay.Text(`{}`), Headers: bearer(viewerToken)}); got.Status != 403 || got.Body != `{"error":"Admin privileges required"}` {
			t.Errorf("%s as a viewer: %d %s", path, got.Status, got.Body)
		}
	}
	for _, c := range []struct{ method, path string }{{"DELETE", "/api/strategies/x"}, {"PUT", "/api/strategies/x/status"}} {
		if got := w.Do(replay.Request{Method: c.method, Path: c.path, Headers: bearer(viewerToken)}); got.Status != 403 {
			t.Errorf("%s %s as a viewer: %d %s", c.method, c.path, got.Status, got.Body)
		}
	}
}

// GET /api/devices answers one page: at most pageSize devices (50 when it
// is missing or not a number, 1000 at most) of page current, and total
// counts the devices of every page.
func TestDevicesComePageByPage(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db, nil)
		for i := 1; i <= 7; i++ {
			w.Exec("INSERT INTO peer (id, uuid) VALUES (?, ?)", []any{fmt.Sprintf("90040000%d", i), fmt.Sprintf("u-%d", i)})
		}
		page := func(query string) (int, string) {
			got := w.Do(replay.Request{Path: "/api/devices" + query, Headers: bearer(viewerToken)})
			var list struct {
				Total int `json:"total"`
				Data  []struct {
					ID string `json:"id"`
				} `json:"data"`
			}
			if err := json.Unmarshal([]byte(got.Body), &list); err != nil || got.Status != http.StatusOK {
				t.Fatalf("GET /api/devices%s: %d %s", query, got.Status, got.Body)
			}
			var ids []string
			for _, d := range list.Data {
				ids = append(ids, strings.TrimPrefix(d.ID, "90040000"))
			}
			return list.Total, strings.Join(ids, ",")
		}
		for query, want := range map[string]string{
			"?pageSize=3":                        "1,2,3",
			"?pageSize=3&current=2":              "4,5,6",
			"?pageSize=3&current=3":              "7",
			"?pageSize=3&current=4":              "",
			"?pageSize=3&current=0":              "1,2,3",
			"?pageSize=3&current=x":              "1,2,3",
			"?pageSize=0":                        "1,2,3,4,5,6,7",
			"?pageSize=abc":                      "1,2,3,4,5,6,7",
			"":                                   "1,2,3,4,5,6,7",
			"?pageSize=2&current=2&current=9":    "3,4",
			"?id=900400005&pageSize=1&current=1": "5",
		} {
			total, ids := page(query)
			wantTotal := 7
			if strings.Contains(query, "id=") {
				wantTotal = 1
			}
			if total != wantTotal || ids != want {
				t.Errorf("%q: total %d, devices %q; want %d and %q", query, total, ids, wantTotal, want)
			}
		}
	})
}

// The client id and UUID of a sign-in count when they are text. A number,
// a boolean, a list or an object counts as missing: the token is stored
// with the empty text in its place, the client gets it, and a UUID that is
// no text revokes no token of the account.
func TestSignInStoresTextClientIDsOnly(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db, nil)
		hash, err := w.Server.Deps().Authn.HashPassword(context.Background(), "Rd-Test-1!x")
		if err != nil {
			t.Fatal(err)
		}
		w.Exec("UPDATE users SET password_hash = ? WHERE id = 6", []any{hash})
		ctx := context.Background()
		for _, c := range []struct{ ids, id, uuid string }{
			{`"id":"cl-1","uuid":"uu-1"`, "cl-1", "uu-1"},
			{`"id":5,"uuid":7`, "", ""},
			{`"id":1.5,"uuid":true`, "", ""},
			{`"id":false,"uuid":false`, "", ""},
			{`"id":["a"],"uuid":["b","c"]`, "", ""},
			{`"id":{"a":1},"uuid":"uu-3"`, "", "uu-3"},
			{`"id":{"toString":1},"uuid":{"toString":1}`, "", ""},
		} {
			got := w.Do(replay.Request{Method: "POST", Path: "/api/login", Body: replay.Text(`{"username":"e2e_viewer","password":"Rd-Test-1!x",` + c.ids + `}`)})
			if got.Status != http.StatusOK || !strings.Contains(got.Body, `"type":"access_token"`) {
				t.Errorf("sign-in with %s: %d %s", c.ids, got.Status, got.Body)
				continue
			}
			var id, uuid string
			if err := db.QueryRow(ctx, "SELECT client_id, client_uuid FROM access_tokens WHERE user_id = 6 ORDER BY id DESC LIMIT 1").Scan(&id, &uuid); err != nil || id != c.id || uuid != c.uuid {
				t.Errorf("sign-in with %s stored client %q and UUID %q, %v", c.ids, id, uuid, err)
			}
			client := c.id
			if client == "" {
				client = "unknown"
			}
			var details string
			if err := db.QueryRow(ctx, "SELECT details FROM audit_log ORDER BY id DESC LIMIT 1").Scan(&details); err != nil || details != "Client: "+client {
				t.Errorf("sign-in with %s: audit details %q, %v", c.ids, details, err)
			}
		}
		var kept int
		if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM access_tokens WHERE user_id = 6 AND client_uuid = 'uu-1' AND NOT revoked").Scan(&kept); err != nil || kept != 1 {
			t.Errorf("tokens of uu-1 left after sign-ins with UUIDs that are no text: %d, %v", kept, err)
		}
		// The five sign-ins whose UUID is no text keep each other's tokens
		if err := db.QueryRow(ctx, "SELECT COUNT(*) FROM access_tokens WHERE user_id = 6 AND client_uuid = '' AND NOT revoked").Scan(&kept); err != nil || kept != 5 {
			t.Errorf("tokens without a UUID left: %d, %v", kept, err)
		}
	})
}

// is_admin is one rule in every answer about an account: the four admin
// roles are admins at sign-in, in GET /api/currentUser and in GET
// /api/users alike.
func TestIsAdminIsOneRule(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db, nil)
		hash, err := w.Server.Deps().Authn.HashPassword(context.Background(), "Rd-Test-1!x")
		if err != nil {
			t.Fatal(err)
		}
		for id, admin := range map[int]bool{1: true, 2: true, 3: true, 4: true, 5: false, 6: false} {
			w.Exec("UPDATE users SET password_hash = ? WHERE id = ?", []any{hash, id})
			var name string
			if err := db.QueryRow(context.Background(), "SELECT username FROM users WHERE id = ?", id).Scan(&name); err != nil {
				t.Fatal(err)
			}
			signIn := w.Do(replay.Request{Method: "POST", Path: "/api/login", Body: replay.Text(`{"username":"` + name + `","password":"Rd-Test-1!x","id":"c-` + name + `"}`)})
			var answer struct {
				Token string `json:"access_token"`
				User  struct {
					IsAdmin bool `json:"is_admin"`
				} `json:"user"`
			}
			if err := json.Unmarshal([]byte(signIn.Body), &answer); err != nil || answer.Token == "" {
				t.Fatalf("sign in as %s: %d %s", name, signIn.Status, signIn.Body)
			}
			var current struct {
				IsAdmin bool `json:"is_admin"`
			}
			got := w.Do(replay.Request{Path: "/api/currentUser", Headers: bearer(answer.Token)})
			if err := json.Unmarshal([]byte(got.Body), &current); err != nil {
				t.Fatalf("currentUser of %s: %d %s", name, got.Status, got.Body)
			}
			var users struct {
				Data []struct {
					IsAdmin bool `json:"is_admin"`
				} `json:"data"`
			}
			got = w.Do(replay.Request{Path: "/api/users", Headers: bearer(answer.Token)})
			if err := json.Unmarshal([]byte(got.Body), &users); err != nil || len(users.Data) != 1 {
				t.Fatalf("users of %s: %d %s", name, got.Status, got.Body)
			}
			if answer.User.IsAdmin != admin || current.IsAdmin != admin || users.Data[0].IsAdmin != admin {
				t.Errorf("%s: is_admin %t at sign-in, %t in currentUser, %t in users; want %t", name, answer.User.IsAdmin, current.IsAdmin, users.Data[0].IsAdmin, admin)
			}
		}
	})
}

// When the database fails under the token check or the device lookup, the
// client gets 500 {"error":"Server error"} instead of a closed connection.
func TestADatabaseFailureAnswers500(t *testing.T) {
	db := storetest.Migrated(t)
	w := world(t, db, nil)
	server := httptest.NewServer(w.Server)
	defer server.Close()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ method, path, body string }{
		{"GET", "/api/ab", ""},
		{"GET", "/api/peers", ""},
		{"GET", "/api/users", ""},
		{"GET", "/api/audit", ""},
		{"GET", "/api/audit/conn", ""},
		{"GET", "/api/device-group", ""},
		{"POST", "/api/heartbeat", `{"id":"900100001"}`},
		{"POST", "/api/sysinfo", `{"id":"900100001"}`},
	} {
		req, err := http.NewRequest(c.method, server.URL+c.path, strings.NewReader(c.body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+viewerToken)
		if c.body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		res, err := server.Client().Do(req)
		if err != nil {
			t.Errorf("%s %s: no answer: %v", c.method, c.path, err)
			continue
		}
		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if res.StatusCode != http.StatusInternalServerError || string(body) != `{"error":"Server error"}` {
			t.Errorf("%s %s: %d %s", c.method, c.path, res.StatusCode, body)
		}
	}
}

// The list form of the device groups follows the route, not the letter
// case of the address: the accessible route gives the short form (name,
// guid, note, sort) in any spelling, the other group routes the long one.
func TestDeviceGroupFormFollowsTheRoute(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db, nil)
		for path, short := range map[string]bool{
			"/api/device-group/accessible":  true,
			"/API/Device-Group/Accessible":  true,
			"/api/device-group/accessible/": true,
			"/api/device-group":             false,
			"/API/Device-Group":             false,
			"/api/group":                    false,
			"/api/group/get":                false,
		} {
			got := w.Do(replay.Request{Path: path, Headers: bearer(viewerToken)})
			var list struct {
				Data []map[string]json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal([]byte(got.Body), &list); err != nil || got.Status != http.StatusOK || len(list.Data) == 0 {
				t.Fatalf("%s: %d %s", path, got.Status, got.Body)
			}
			_, team := list.Data[0]["team"]
			if _, sort := list.Data[0]["sort"]; team == short || !sort {
				t.Errorf("%s: %s, want the short form %t", path, got.Body, short)
			}
		}
	})
}

// POST /api/device-group refuses a group whose name, guid, note, team id
// or tag filter is no text with 400 before anything is written, where it
// saved the value read as text; null and a missing member are no refusal
// (BUGS DGR5).
func TestDeviceGroupSaveTakesTextOnly(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		w := world(t, db, nil)
		tokenFor(w, db, 1, adminToken)
		count := func() string { return w.Rows(`SELECT COUNT(*) AS n FROM device_groups`, nil) }
		before := count()
		for _, c := range []struct{ body, member string }{
			{`{"name":["a","b"]}`, "name"},
			{`{"name":{"a":1}}`, "name"},
			{`{"name":"Lab","guid":1e21}`, "guid"},
			{`{"name":"Lab","note":true}`, "note"},
			{`{"name":"Lab","team_id":5}`, "team_id"},
			{`{"name":"Lab","dynamic":true,"tag":["lab"]}`, "tag"},
		} {
			got := w.Do(replay.Request{Method: "POST", Path: "/api/device-group", Headers: bearer(adminToken), Body: replay.Text(c.body)})
			if want := `{"error":"` + c.member + ` must be text"}`; got.Status != http.StatusBadRequest || got.Body != want {
				t.Errorf("%s: %d %s, want 400 %s", c.body, got.Status, got.Body, want)
			}
			if got := count(); got != before {
				t.Errorf("%s wrote a group: %s, before %s", c.body, got, before)
			}
		}
		if got := w.Do(replay.Request{Method: "POST", Path: "/api/device-group", Headers: bearer(adminToken), Body: replay.Text(`{"name":"Lab","note":null,"team_id":null}`)}); got.Status != http.StatusOK {
			t.Errorf("a group with null note and team id: %d %s", got.Status, got.Body)
		}
	})
}
