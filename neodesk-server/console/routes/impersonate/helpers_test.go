package impersonate_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"database/sql"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/config"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/routetest"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The test accounts by role (routetest.RoleIDs).
var (
	idAdmin       = routetest.RoleIDs["admin"]
	idSuper       = routetest.RoleIDs["super_admin"]
	idServerAdmin = routetest.RoleIDs["server_admin"]
	idGlobal      = routetest.RoleIDs["global_admin"]
	idOperator    = routetest.RoleIDs["operator"]
	idViewer      = routetest.RoleIDs["viewer"]
	idPro         = routetest.RoleIDs["pro"]
)

// adminPassword is the password every test account gets.
const adminPassword = "correct horse 42"

// impCookie is the impersonation cookie of a console set up for HTTP.
const impCookie = "nd.imp"

// sidCookie is the session cookie of a console set up for HTTP.
const sidCookie = "nd.sid"

type outbox struct {
	mu   sync.Mutex
	sent []mail.Outgoing
}

func (o *outbox) Send(_ context.Context, _ mail.Settings, m mail.Outgoing) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.sent = append(o.sent, m)
	return nil
}

func (o *outbox) Verify(context.Context, mail.Settings) error { return nil }

func (o *outbox) all() []mail.Outgoing {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]mail.Outgoing(nil), o.sent...)
}

// waitForMails waits until the outbox holds n mails, at most two seconds.
func waitForMails(t *testing.T, box *outbox, n int) []mail.Outgoing {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if got := box.all(); len(got) >= n {
			return got
		}
	}
	got := box.all()
	t.Fatalf("%d mails, want %d", len(got), n)
	return got
}

type options struct {
	roles routetest.Overrides
	env   map[string]string
	// db is the database to run on; nil is a new SQLite one.
	db *store.DB
}

// newWorld is a console with the real language files, a made-up mail
// server and a password for every test account.
func newWorld(t *testing.T, o options) (*routetest.World, *outbox) {
	t.Helper()
	lang, err := filepath.Abs(filepath.Join("..", "..", "static", "files", "lang"))
	if err != nil {
		t.Fatal(err)
	}
	trust, err := httpx.ParseTrustProxy("true")
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"PANEL_PUBLIC_URL": "https://desk.example"}
	for k, v := range o.env {
		env[k] = v
	}
	opts := routetest.Options{
		Env: env,
		Config: func(cfg *config.Config) {
			cfg.LangDir = lang
			cfg.TrustProxy = trust
			if v := env["DEVICE_SCOPE_DEFAULT"]; v != "" {
				cfg.DeviceScopeDefault = v
			}
		},
	}
	if o.roles != nil {
		opts.Roles = o.roles
	}
	opts.DB = o.db
	w := routetest.New(t, opts)
	box := &outbox{}
	w.Server.Deps().Mail = mail.New(mail.Options{DB: w.DB, Transport: box, Getenv: func(name string) string { return env[name] }})
	ctx := context.Background()
	if _, err := w.DB.Exec(ctx, `INSERT INTO settings (key, value) VALUES ('smtp_config', '{"host":"smtp.example.test","port":25,"from":"desk@example.test"}')`); err != nil {
		t.Fatal(err)
	}
	hash, err := w.Server.Deps().Authn.HashPassword(ctx, adminPassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.DB.Exec(ctx, `UPDATE users SET password_hash = ?`, hash); err != nil {
		t.Fatal(err)
	}
	return w, box
}

func exec(t *testing.T, w *routetest.World, query string, args ...any) {
	t.Helper()
	if _, err := w.DB.Exec(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// startBody is the body of a start with the test password.
func startBody(reason string) string {
	b, _ := json.Marshal(map[string]string{"password": adminPassword, "reason": reason})
	return string(b)
}

// start signs in as the role and views the console as the account id.
func start(t *testing.T, w *routetest.World, role string, id int64) *routetest.Agent {
	t.Helper()
	a := w.As(role)
	res := a.Do("POST", fmt.Sprintf("/api/users/%d/impersonate", id), startBody("Reproduce the ticket 4711"))
	if res.Status != http.StatusOK {
		t.Fatalf("start as %s for %d: %d %s", role, id, res.Status, res.Body)
	}
	if a.Cookie(impCookie) == "" {
		t.Fatalf("no impersonation cookie after the start: %s", res.Body)
	}
	return a
}

// code is the "code" of a JSON answer.
func code(t *testing.T, res routetest.Answer) string {
	t.Helper()
	var out struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal([]byte(res.Body), &out)
	return out.Code
}

// whoAmI is the username of the bootstrap: who the browser is signed in as.
func whoAmI(t *testing.T, a *routetest.Agent) (string, routetest.Answer) {
	t.Helper()
	res := a.Do("GET", "/api/console/bootstrap", "")
	var out struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
	}
	_ = json.Unmarshal([]byte(res.Body), &out)
	return out.User.Username, res
}

// auditAbout lists the audit entries about an account as
// "action|user_id|details", oldest first.
func auditAbout(t *testing.T, w *routetest.World, target int64) []string {
	t.Helper()
	rows, err := w.DB.Query(context.Background(), `SELECT action, COALESCE(user_id, 0), COALESCE(details, '') FROM audit_log WHERE target_user_id = ? ORDER BY id`, target)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var action, details string
		var user int64
		if err := rows.Scan(&action, &user, &details); err != nil {
			t.Fatal(err)
		}
		out = append(out, action+"|"+strconv.FormatInt(user, 10)+"|"+details)
	}
	return out
}

// impRow is the impersonation session's row: whether it is active, its
// end reason and impersonated_by.
func impRow(t *testing.T, w *routetest.World, target int64) (active bool, reason string, by int64) {
	t.Helper()
	var a int64
	var r sql.NullString
	var b sql.NullInt64
	err := w.DB.QueryRow(context.Background(), `SELECT CASE WHEN `+w.DB.Pick("is_active = 1", "is_active")+` THEN 1 ELSE 0 END, end_reason, impersonated_by
		FROM panel_sessions WHERE user_id = ? AND impersonated_by IS NOT NULL ORDER BY created_at DESC LIMIT 1`, target).Scan(&a, &r, &b)
	if err != nil {
		t.Fatalf("the impersonation row: %v", err)
	}
	return a == 1, r.String, b.Int64
}

// moveExpiry rewrites the impersonation's expiry in its stored session.
func moveExpiry(t *testing.T, w *routetest.World, target int64, at time.Time) {
	t.Helper()
	ctx := context.Background()
	var sid, text string
	if err := w.DB.QueryRow(ctx, `SELECT sid, session_json FROM panel_sessions WHERE user_id = ? AND impersonated_by IS NOT NULL AND `+w.DB.Pick("is_active = 1", "is_active"), target).Scan(&sid, &text); err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatal(err)
	}
	imp := data["impersonation"].(map[string]any)
	imp["expiresAt"] = at.UnixMilli()
	raw, _ := json.Marshal(data)
	exec(t, w, `UPDATE panel_sessions SET session_json = ? WHERE sid = ?`, string(raw), sid)
}

// totpSecret is a two-factor secret for the tests.
const totpSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

// totpNow is the current code of totpSecret. The server accepts only the
// code of the step it checks in, after the password check, which can take
// seconds under the race detector. So when less than 15 seconds of the
// step are left, totpNow waits for the next step and gives its code.
func totpNow(t *testing.T) string {
	t.Helper()
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(totpSecret)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if left := 30 - now.Unix()%30; left < 15 {
		time.Sleep(time.Duration(left)*time.Second - time.Duration(now.Nanosecond()) + 100*time.Millisecond)
		now = time.Now()
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(now.Unix()/30))
	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", n%1_000_000)
}

// fill gives a route pattern's parameters plausible values.
func fill(pattern string) string {
	parts := strings.Split(pattern, "/")
	for i, p := range parts {
		if !strings.HasPrefix(p, "{") {
			continue
		}
		switch {
		case strings.HasSuffix(p, "...}"):
			parts[i] = "a/b"
		case strings.Contains(strings.ToLower(p), "guid"):
			parts[i] = "g-1"
		case strings.Contains(strings.ToLower(p), "code"):
			parts[i] = "en"
		default:
			parts[i] = "1"
		}
	}
	return strings.Join(parts, "/")
}
