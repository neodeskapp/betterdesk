package authn

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// testService is a service on db whose Go server answers what the script
// says. Hashes are made with cost 4.
func testService(t *testing.T, db *store.DB, mutate func(*Options)) (*Service, *goStub, *logCapture) {
	t.Helper()
	stub, logs := &goStub{}, &logCapture{}
	opts := Options{
		DB:    db,
		GoAPI: goapi.New(stub, goapi.WithTimeout(80*time.Millisecond)),
		QR:    func(text string) (string, error) { return "QR:" + text, nil },
		Log:   logs,
		FS:    newMemFS(),
		Sleep: func(context.Context, time.Duration) error { return nil },
	}
	if mutate != nil {
		mutate(&opts)
	}
	s, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	s.bcryptCost, s.dummy = bcryptMinCost, testDummyHash
	return s, stub, logs
}

func addUser(t *testing.T, s *Service, name, password, role, provider string) int64 {
	t.Helper()
	ctx := context.Background()
	hash, err := s.HashPassword(ctx, password)
	if err != nil {
		t.Fatal(err)
	}
	if err := createUser(ctx, s.db, name, hash, role, provider); err != nil {
		t.Fatal(err)
	}
	user, err := getUserByUsername(ctx, s.db, name)
	if err != nil || user == nil {
		t.Fatalf("read %s back: %v", name, err)
	}
	return user.ID.V
}

func itoa(n int) string {
	raw, _ := json.Marshal(n)
	return string(raw)
}

func TestNewNeedsDatabaseAndClient(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Error("New without a database succeeds")
	}
	db := storetest.Migrated(t)
	if _, err := New(Options{DB: db}); err == nil {
		t.Error("New without a client for the Go server succeeds")
	}
	if _, err := New(Options{DB: db, GoAPI: goapi.New(http.NotFoundHandler())}); err != nil {
		t.Errorf("New: %v", err)
	}
}

func TestSettingsFromEnv(t *testing.T) {
	env := map[string]string{}
	get := func(key string) string { return env[key] }

	got := SettingsFromEnv(get, "/data", "/keys")
	want := Settings{TokenExpiryDays: 7, MaxFailedAttempts: 10, LockoutMinutes: 15, IPRateLimit: 30, AttemptWindowMinutes: 15,
		DataDir: "/data", KeysPath: "/keys", DBType: "sqlite", DockerAdminBootstrapWait: 30 * time.Second}
	if got != want {
		t.Errorf("empty environment:\n got %+v\nwant %+v", got, want)
	}
	if filled := got.withDefaults(); filled.DefaultAdminUsername != "admin" {
		t.Errorf("default admin name %q", filled.DefaultAdminUsername)
	}
	if zero := (Settings{}).withDefaults(); zero.TokenExpiryDays != 7 || zero.MaxFailedAttempts != 10 || zero.LockoutMinutes != 15 ||
		zero.IPRateLimit != 30 || zero.AttemptWindowMinutes != 15 || zero.DBType != "sqlite" || zero.DockerAdminBootstrapWait != 30*time.Second {
		t.Errorf("zero settings with defaults: %+v", zero)
	}

	for _, c := range []struct {
		key, value string
		read       func(Settings) any
		want       any
	}{
		{"BETTERDESK_AUTH_AUTOCREATE", "true", func(s Settings) any { return s.AutoCreate }, true},
		{"BETTERDESK_AUTH_AUTOCREATE", "TRUE", func(s Settings) any { return s.AutoCreate }, false},
		{"BETTERDESK_AUTH_AUTOCREATE", "1", func(s Settings) any { return s.AutoCreate }, false},
		{"FORCE_PASSWORD_UPDATE", "true", func(s Settings) any { return s.ForcePasswordUpdate }, true},
		{"FORCE_PASSWORD_UPDATE", "yes", func(s Settings) any { return s.ForcePasswordUpdate }, false},
		{"API_TOKEN_EXPIRY_DAYS", "30", func(s Settings) any { return s.TokenExpiryDays }, 30},
		{"API_TOKEN_EXPIRY_DAYS", "0", func(s Settings) any { return s.TokenExpiryDays }, 7},
		{"API_TOKEN_EXPIRY_DAYS", "abc", func(s Settings) any { return s.TokenExpiryDays }, 7},
		{"API_TOKEN_EXPIRY_DAYS", "-1", func(s Settings) any { return s.TokenExpiryDays }, -1},
		{"API_TOKEN_EXPIRY_DAYS", " 3days", func(s Settings) any { return s.TokenExpiryDays }, 3},
		{"API_MAX_FAILED_ATTEMPTS", "2x", func(s Settings) any { return s.MaxFailedAttempts }, 2},
		{"API_LOCKOUT_MINUTES", "1.9", func(s Settings) any { return s.LockoutMinutes }, 1},
		{"API_IP_RATE_LIMIT", "+4", func(s Settings) any { return s.IPRateLimit }, 4},
		{"API_ATTEMPT_WINDOW", "0x10", func(s Settings) any { return s.AttemptWindowMinutes }, 15},
		{"DB_TYPE", "PostgreSQL", func(s Settings) any { return s.DBType }, "PostgreSQL"},
		{"DATABASE_URL", "postgres://u@h/db", func(s Settings) any { return s.DBType }, "postgres"},
		{"DATABASE_URL", "PostgreSQL://u@h/db", func(s Settings) any { return s.DBType }, "postgres"},
		{"DATABASE_URL", "mysql://u@h/db", func(s Settings) any { return s.DBType }, "sqlite"},
		{"DOCKER_ADMIN_BOOTSTRAP_WAIT_MS", "5", func(s Settings) any { return s.DockerAdminBootstrapWait }, time.Second},
		{"DOCKER_ADMIN_BOOTSTRAP_WAIT_MS", "2500", func(s Settings) any { return s.DockerAdminBootstrapWait }, 2500 * time.Millisecond},
		{"DOCKER_ADMIN_BOOTSTRAP_WAIT_MS", "abc", func(s Settings) any { return s.DockerAdminBootstrapWait }, 30 * time.Second},
		{"DOCKER_ADMIN_BOOTSTRAP_WAIT_MS", "0", func(s Settings) any { return s.DockerAdminBootstrapWait }, 30 * time.Second},
		{"DOCKER_ADMIN_BOOTSTRAP_WAIT_MS", "-7", func(s Settings) any { return s.DockerAdminBootstrapWait }, time.Second},
		{"AUTH_DB_PATH", "/x/auth.db", func(s Settings) any { return s.AuthDBPath }, "/x/auth.db"},
		{"DEFAULT_ADMIN_USERNAME", "root", func(s Settings) any { return s.DefaultAdminUsername }, "root"},
		{"DEFAULT_ADMIN_PASSWORD", "pw", func(s Settings) any { return s.DefaultAdminPassword }, "pw"},
		{"DOCKER", "True", func(s Settings) any { return s.Docker }, "True"},
		{"SQLITE_AUTH_DB_MODE", "legacy", func(s Settings) any { return s.SQLiteAuthDBMode }, "legacy"},
	} {
		env = map[string]string{c.key: c.value}
		if got := c.read(SettingsFromEnv(get, "/data", "/keys")); got != c.want {
			t.Errorf("%s=%q: got %v, want %v", c.key, c.value, got, c.want)
		}
	}
	env = map[string]string{"DB_TYPE": "sqlite", "DATABASE_URL": "postgres://u@h/db"}
	if got := SettingsFromEnv(get, "", "").DBType; got != "sqlite" {
		t.Errorf("DB_TYPE wins over DATABASE_URL: got %q", got)
	}
}

// The JSON of a sign-in answer is kept in the session by the route, so its
// keys and their order are part of the shared session format.
func TestUserJSON(t *testing.T) {
	storetest.EachOrigin(t, func(t *testing.T, db *store.DB, origin storetest.Origin) {
		ctx := context.Background()
		s, _, _ := testService(t, db, nil)
		id := addUser(t, s, "alice", "pw", "operator", "local")
		idText := itoa(int(id))
		if db.IsPostgres() && origin == storetest.GoServerFirst {
			// The signal server's table has 64-bit ids, which went out as text.
			idText = `"` + idText + `"`
		}
		outcome, err := s.Authenticate(ctx, "alice", "pw")
		if err != nil || outcome.User == nil {
			t.Fatalf("sign-in: %+v, %v", outcome, err)
		}
		raw, _ := json.Marshal(outcome.User)
		if want := `{"id":` + idText + `,"username":"alice","role":"operator","preferred_language":null,"totpRequired":false}`; string(raw) != want {
			t.Errorf("answer:\n got %s\nwant %s", raw, want)
		}

		mustExec(t, db, `UPDATE users SET preferred_language = 'de', totp_secret = 'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ', totp_enabled = `+db.Pick("1", "TRUE")+` WHERE id = ?`, id)
		outcome, err = s.Authenticate(ctx, "alice", "pw")
		if err != nil || outcome.User == nil {
			t.Fatalf("sign-in with a second factor: %+v, %v", outcome, err)
		}
		raw, _ = json.Marshal(outcome.User)
		if want := `{"id":` + idText + `,"username":"alice","role":"operator","preferred_language":"de","totpRequired":true,"goPartialToken":null}`; string(raw) != want {
			t.Errorf("answer with a second factor:\n got %s\nwant %s", raw, want)
		}
	})
}

func TestBruteForceJSON(t *testing.T) {
	for _, c := range []struct {
		value BruteForce
		want  string
	}{
		{BruteForce{}, `{"blocked":false}`},
		{BruteForce{Blocked: true, Reason: reasonAccountLocked, RetryAfter: 900}, `{"blocked":true,"reason":"Account temporarily locked due to too many failed attempts","retryAfter":900}`},
		{BruteForce{Blocked: true, Reason: reasonIPLimited, RetryAfter: 60}, `{"blocked":true,"reason":"Too many failed attempts from this IP address","retryAfter":60}`},
		{BruteForce{Blocked: true, Reason: "x", RetryUnknown: true}, `{"blocked":true,"reason":"x","retryAfter":null}`},
	} {
		raw, err := json.Marshal(c.value)
		if err != nil || string(raw) != c.want {
			t.Errorf("%+v: %s, %v; want %s", c.value, raw, err, c.want)
		}
	}
}

// tests/authService.totp.test.js
func TestTOTPSetup(t *testing.T) {
	uri := OtpauthURL("admin", "K5MQOUIXDV6D4BLCPFKQ6YRRJ4LQS43T")
	if uri != "otpauth://totp/BetterDesk:admin?secret=K5MQOUIXDV6D4BLCPFKQ6YRRJ4LQS43T&issuer=BetterDesk&digits=6&period=30" {
		t.Errorf("address: %s", uri)
	}
	if strings.Contains(uri, " ") || strings.Contains(strings.ToLower(uri), "algorithm=") {
		t.Errorf("address has a space or an algorithm: %s", uri)
	}
	if got := OtpauthURL("admin user", "ABCDEFGH"); !strings.Contains(got, "BetterDesk:admin%20user") {
		t.Errorf("a space in the account is not escaped: %s", got)
	}

	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		now := time.UnixMilli(1794287700123)
		s, _, _ := testService(t, db, func(o *Options) { o.Now = func() time.Time { return now } })
		id := addUser(t, s, "admin", "pw", "admin", "local")

		setup, err := s.GenerateTOTPSetup(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if !regexp.MustCompile(`^[A-Z2-7]{32}$`).MatchString(setup.Secret) {
			t.Errorf("secret %q is not 32 base32 characters", setup.Secret)
		}
		if setup.OtpauthURL != OtpauthURL("admin", setup.Secret) || setup.QRCode != "QR:"+setup.OtpauthURL {
			t.Errorf("setup: %+v", setup)
		}
		stored, _ := getUserByID(ctx, db, id)
		if stored.TOTPSecret.String != setup.Secret || stored.TOTPEnabled.True() {
			t.Errorf("after setup the row has secret %q, enabled %v", stored.TOTPSecret.String, stored.TOTPEnabled.True())
		}
		code, _ := totpCode(setup.Secret, now)
		codes, err := s.VerifyAndEnableTOTP(ctx, id, code)
		if err != nil || len(codes) != 8 {
			t.Fatalf("enable: %v, %v", codes, err)
		}
		form := regexp.MustCompile(`^[0-9A-F]{4}-[0-9A-F]{4}$`)
		for _, c := range codes {
			if !form.MatchString(c) {
				t.Errorf("recovery code %q has not the form XXXX-XXXX", c)
			}
		}
		stored, _ = getUserByID(ctx, db, id)
		if want := stringifyCodes(codes); stored.TOTPRecoveryCodes.String != want || !stored.TOTPEnabled.True() {
			t.Errorf("stored codes %q, want %q; enabled %v", stored.TOTPRecoveryCodes.String, want, stored.TOTPEnabled.True())
		}

		if _, err := s.GenerateTOTPSetup(ctx, id+100); !errors.Is(err, ErrUserNotFound) {
			t.Errorf("setup for an unknown account: %v", err)
		}

		noQR, _, _ := testService(t, db, func(o *Options) { o.QR = nil })
		if _, err := noQR.GenerateTOTPSetup(ctx, id); err == nil {
			t.Error("setup without a QR function succeeds")
		}
		failing, _, _ := testService(t, db, func(o *Options) { o.QR = func(string) (string, error) { return "", errors.New("no ink") } })
		if _, err := failing.GenerateTOTPSetup(ctx, id); err == nil || !strings.Contains(err.Error(), "no ink") {
			t.Errorf("setup with a failing QR function: %v", err)
		}
	})
}

// tests/deviceAuth.middleware.test.js
func TestDeviceRequestChecks(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s, _, _ := testService(t, db, nil)
		id := addUser(t, s, "operator", "pw", "operator", "local")
		bound, _ := s.GenerateAccessToken(ctx, id, "device-a", "uuid-a", "")
		unbound, _ := s.GenerateAccessToken(ctx, id, "", "uuid-b", "")

		// X-Device-Id alone is no credential.
		var refused *DeviceAuthError
		if _, err := s.RequireDeviceToken(ctx, ""); !errors.As(err, &refused) || refused.Status != 401 || refused.Text != "Bearer access token required" {
			t.Errorf("no Authorization header: %v", err)
		}
		if _, err := s.RequireDeviceToken(ctx, "Bearer "+unbound); !errors.As(err, &refused) || refused.Status != 401 || refused.Text != "Invalid or unbound access token" {
			t.Errorf("a token bound to no device: %v", err)
		}
		if _, err := s.RequireDeviceToken(ctx, "Bearer no-such-token"); !errors.As(err, &refused) || refused.Text != "Invalid or unbound access token" {
			t.Errorf("an unknown token: %v", err)
		}

		device, err := s.RequireDeviceToken(ctx, "Bearer "+bound)
		if err != nil || device.DeviceID != "device-a" || device.Token.UserID.V != id {
			t.Fatalf("a bound token: %+v, %v", device, err)
		}
		var used store.Time
		if err := store.SelectRow(ctx, db, `SELECT last_used FROM access_tokens WHERE token = ?`, bound).Scan(&used); err != nil || used.IsNull() {
			t.Errorf("the use of the token was not noted: %v, %v", used, err)
		}

		body := func(v string) DeviceClaim { return ClaimFromJSON(v, true) }
		none := DeviceClaim{}
		if err := RequireTokenDeviceMatch(device.DeviceID, body("device-b"), none, none); !errors.Is(err, ErrDeviceMismatch) {
			t.Errorf("another device in the body: %v", err)
		}
		if ErrDeviceMismatch.Status != 403 || ErrDeviceMismatch.Text != "Device ID mismatch" {
			t.Errorf("the mismatch answer is %d %q", ErrDeviceMismatch.Status, ErrDeviceMismatch.Text)
		}
		if err := RequireTokenDeviceMatch(device.DeviceID, none, none, none); err != nil {
			t.Errorf("no claim: %v", err)
		}
		if err := RequireTokenDeviceMatch(device.DeviceID, body("device-a"), none, ClaimFromText("device-b", true)); err != nil {
			t.Errorf("the body names the right device, the header another: %v", err)
		}
		if err := RequireTokenDeviceMatch(device.DeviceID, none, ClaimFromText("device-b", true), ClaimFromText("device-a", true)); !errors.Is(err, ErrDeviceMismatch) {
			t.Errorf("the query names another device: %v", err)
		}
		if err := RequireTokenDeviceMatch(device.DeviceID); err != nil {
			t.Errorf("no places to look: %v", err)
		}

		// A lookup that fails is a refusal too.
		broken, _, _ := testService(t, storetest.SQLite(t), nil)
		if _, err := broken.RequireDeviceToken(ctx, "Bearer "+bound); !errors.As(err, &refused) || refused.Status != 401 || refused.Text != "Invalid or expired access token" {
			t.Errorf("a failing lookup: %v", err)
		}
	})
}

// tests/tokenHash.test.js and the vector of the specification
func TestTokenHash(t *testing.T) {
	a, b := HashAccessToken(strings.Repeat("a", 64)), HashAccessToken(strings.Repeat("b", 64))
	if len(a) != 64 || a != HashAccessToken(strings.Repeat("a", 64)) || a == b {
		t.Errorf("hashes %q and %q", a, b)
	}
	if got := HashAccessToken(strings.Repeat("0123456789abcdef", 4)); got != "a8ae6e6ee929abea3afcfc5258c8ccd6f85273e0d4626d26c7279f3250f77c8e" {
		t.Errorf("fixed vector: %s", got)
	}
}

// tests/readAdminCredentialsFile.test.js
func TestReadAdminCredentialsFile(t *testing.T) {
	db := storetest.Migrated(t)
	files := newMemFS()
	s, _, _ := testService(t, db, func(o *Options) {
		o.FS = files
		o.Settings = Settings{KeysPath: "/opt/rustdesk", DataDir: "/app/data"}
	})
	if got := s.ReadAdminCredentialsFile(); got != "" {
		t.Errorf("no file: %q", got)
	}
	files.place(fileVector{Path: "/app/data/.admin_credentials", Content: "Admin Username: admin\nAdmin Password: from-console\n"})
	if got := s.ReadAdminCredentialsFile(); got != "from-console" {
		t.Errorf("only the data folder has one: %q", got)
	}
	files.place(fileVector{Path: "/opt/rustdesk/.admin_credentials", Content: "Admin Username: admin\nAdmin Password: from-rustdesk\n"})
	if got := s.ReadAdminCredentialsFile(); got != "from-rustdesk" {
		t.Errorf("the key folder comes first: %q", got)
	}
	want := []string{"/opt/rustdesk", "/opt/rustdesk/data", "/app/data", "/opt/betterdesk", "/opt/betterdesk/data"}
	if got := s.credentialDirs(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("folders %v, want %v", got, want)
	}
	s.windows = true
	if got := s.credentialDirs(); len(got) != len(want)+4 || got[len(got)-4] != `C:\BetterDesk` || got[len(got)-1] != `C:\RustDesk\data` {
		t.Errorf("folders on Windows: %v", got)
	}
}

// What the single sign-on route does with the local account
// (routes/auth.routes.js, GET /api/auth/oidc/session).
func TestOIDCUser(t *testing.T) {
	storetest.EachOrigin(t, func(t *testing.T, db *store.DB, _ storetest.Origin) {
		ctx := context.Background()
		s, _, _ := testService(t, db, nil)

		created, err := s.OIDCUser(ctx, "sso-new", "operator")
		if err != nil || created == nil {
			t.Fatalf("new user: %+v, %v", created, err)
		}
		if created.Role.String != "operator" || created.AuthProvider.String != "oidc" || !strings.HasPrefix(created.PasswordHash.String, "$2b$") {
			t.Errorf("new user: %+v", created)
		}
		if outcome, _ := s.Authenticate(ctx, "sso-new", ""); outcome.User != nil {
			t.Error("a single sign-on account signs in with a password")
		}
		noRole, err := s.OIDCUser(ctx, "sso-plain", "")
		if err != nil || noRole == nil || noRole.Role.String != "viewer" {
			t.Errorf("new user without a role: %+v, %v", noRole, err)
		}

		id := addUser(t, s, "was-local", "pw", "admin", "local")
		synced, err := s.OIDCUser(ctx, "was-local", "viewer")
		if err != nil || synced == nil || synced.ID.V != id || synced.Role.String != "viewer" || synced.AuthProvider.String != "oidc" {
			t.Errorf("existing user: %+v, %v", synced, err)
		}
		kept, err := s.OIDCUser(ctx, "was-local", "")
		if err != nil || kept == nil || kept.Role.String != "viewer" {
			t.Errorf("existing user, no role from the server: %+v, %v", kept, err)
		}
		// Its password still fits the hash, but the provider forbids it.
		if ok, _ := s.VerifyPassword(ctx, "pw", synced.PasswordHash.String); !ok {
			t.Error("the existing user's hash was replaced")
		}
		if outcome, _ := s.Authenticate(ctx, "was-local", "pw"); outcome.User != nil {
			t.Error("the account still signs in with its password")
		}
	})
}

func TestTouchLastLoginAndUserByID(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s, _, _ := testService(t, db, nil)
		id := addUser(t, s, "alice", "pw", "admin", "local")
		user, err := s.UserByID(ctx, id)
		if err != nil || user == nil || user.Username != "alice" {
			t.Fatalf("UserByID: %+v, %v", user, err)
		}
		if missing, err := s.UserByID(ctx, id+1); err != nil || missing != nil {
			t.Errorf("unknown id: %+v, %v", missing, err)
		}
		raw, _ := json.Marshal(user)
		if bytes.Contains(raw, []byte("$2b$")) || bytes.Contains(raw, []byte("password")) || bytes.Contains(raw, []byte("totp_secret")) {
			t.Errorf("the JSON of an account shows its secrets: %s", raw)
		}
		var before, after store.Time
		_ = store.SelectRow(ctx, db, `SELECT last_login FROM users WHERE id = ?`, id).Scan(&before)
		if err := s.TouchLastLogin(ctx, id); err != nil {
			t.Fatal(err)
		}
		_ = store.SelectRow(ctx, db, `SELECT last_login FROM users WHERE id = ?`, id).Scan(&after)
		if !before.IsNull() || after.IsNull() {
			t.Errorf("last_login before %v, after %v", before, after)
		}
	})
}

// On SQLite the end of a lockout is the UTC text the column holds, read to
// the millisecond; text that is no time of that form gives none.
func TestLockoutEndOfSQLiteText(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	var text store.Time
	if err := store.SelectRow(ctx, db, `SELECT '2026-11-10 05:15:00.123'`).Scan(&text); err != nil {
		t.Fatal(err)
	}
	end, ok := lockoutEnd(text, false)
	if want := time.Date(2026, 11, 10, 5, 15, 0, 123e6, time.UTC); !ok || !end.Equal(want) {
		t.Errorf("SQLite text: %v, %v; want %v", end, ok, want)
	}
	for _, bad := range []string{"", "soon", "2026-11-10T05:15:00Z", "2026-11-10"} {
		var odd store.Time
		_ = store.SelectRow(ctx, db, `SELECT ?`, bad).Scan(&odd)
		if _, ok := lockoutEnd(odd, false); ok {
			t.Errorf("SQLite text %q counts as a time", bad)
		}
	}
}

// The known defects of sign-in that the port keeps (BUGS.md). Each test
// states the behaviour as it is, so the commit that fixes one changes its
// test with it.
func TestKeptDefects(t *testing.T) {
	t.Run("S6: a locked account signs in", func(t *testing.T) {
		db := storetest.Migrated(t)
		ctx := context.Background()
		s, _, _ := testService(t, db, func(o *Options) { o.Settings = Settings{MaxFailedAttempts: 2} })
		addUser(t, s, "alice", "pw", "admin", "local")
		for i := 0; i < 2; i++ {
			if err := s.RecordAttempt(ctx, "alice", "198.51.100.1", false); err != nil {
				t.Fatal(err)
			}
		}
		if check, err := s.CheckBruteForce(ctx, "alice", "198.51.100.1"); err != nil || !check.Blocked {
			t.Fatalf("the account is not locked: %+v, %v", check, err)
		}
		if outcome, err := s.Authenticate(ctx, "alice", "pw"); err != nil || outcome.User == nil {
			t.Errorf("sign-in of the locked account: %+v, %v; Authenticate does not look at lockouts", outcome, err)
		}
	})

	t.Run("S12: recovery codes are stored as they are, and the Go server's are not understood", func(t *testing.T) {
		db := storetest.Migrated(t)
		ctx := context.Background()
		s, _, _ := testService(t, db, nil)
		id := addUser(t, s, "alice", "pw", "admin", "local")
		// What the Go server stores: bcrypt hashes of codes like ABCD-EFGH-IJ.
		hash, _ := s.HashPassword(ctx, "ABCD-EFGH-IJ")
		mustExec(t, db, `UPDATE users SET totp_secret = 'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ', totp_enabled = `+db.Pick("1", "TRUE")+`, totp_recovery_codes = ? WHERE id = ?`,
			stringifyCodes([]string{hash}), id)
		if ok, err := s.VerifyRecoveryCode(ctx, id, "ABCD-EFGH-IJ"); err != nil || ok {
			t.Errorf("a code of the Go server: %v, %v; the console cannot check those", ok, err)
		}
		if ok, err := s.VerifyRecoveryCode(ctx, id, hash); err != nil || !ok {
			t.Errorf("the stored text itself: %v, %v; codes are compared as plain text", ok, err)
		}
	})
}

func TestHousekeepingLogsAFailure(t *testing.T) {
	db := storetest.SQLite(t) // no tables
	s, _, logs := testService(t, db, nil)
	s.CleanupHousekeeping(context.Background())
	lines := logs.take()
	if len(lines) != 1 || lines[0][0] != "error" || lines[0][1] != "Housekeeping error:" {
		t.Errorf("log lines: %v", lines)
	}
}

func TestContextEndsAHighCostCompare(t *testing.T) {
	// A stored hash with a cost of 2^30 rounds would run for hours.
	stored := "$2b$30$KiXeOj5vHpJRJHGMhWzadeGcZXEL6ZJOxJHQmO7oKUwgLGDMaRJ3O"
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	ok, err := pureService().VerifyPassword(ctx, "pw", stored)
	if ok || !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 5*time.Second {
		t.Errorf("compare with cost 30: %v, %v after %v", ok, err, time.Since(start))
	}
}
