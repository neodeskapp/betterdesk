package avatars

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// TestOracleStore replays a scenario the Node console's real
// services/avatarService.js ran on a scratch database
// (testdata/store_*.json.gz): the same rows, the same calls in the
// same order, and every answer must be the same.

type storeStep struct {
	Op       string          `json:"op"`
	UserID   int64           `json:"userId"`
	Exchange json.RawMessage `json:"exchange"`
	URL      string          `json:"url"`
	Answer   *struct {
		Status  int               `json:"status"`
		Headers map[string]string `json:"headers"`
		Body    string            `json:"body"`
	} `json:"answer"`
	SQL   string  `json:"sql"`
	Args  []any   `json:"args"`
	Ms    int64   `json:"ms"`
	Users []int64 `json:"users"`

	Result  json.RawMessage `json:"result"`
	Error   *string         `json:"error"`
	Warned  int             `json:"warned"`
	Warning string          `json:"warning"`
}

type storeScenario struct {
	Dialect string      `json:"dialect"`
	Seed    [][2]any    `json:"seed"`
	Steps   []storeStep `json:"steps"`
}

// claims is the exchange answer of a step: a JSON object.
type claims map[string]any

func (c claims) Has(key string) bool { _, ok := c[key]; return ok }
func (c claims) Get(key string) any  { return c[key] }

type warnings struct {
	mu    sync.Mutex
	lines []string
}

func (w *warnings) Warn(args ...any) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.lines = append(w.lines, strings.TrimSuffix(fmt.Sprintln(args...), "\n"))
}

func (w *warnings) take() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	lines := w.lines
	w.lines = nil
	return lines
}

// plainAvatar is a stored picture in the shape the scenario records.
type plainAvatar struct {
	UserID        int64            `json:"user_id"`
	MimeType      string           `json:"mime_type"`
	Data          []byte           `json:"data"`
	Version       string           `json:"version"`
	Source        string           `json:"source"`
	SourceURL     store.NullString `json:"source_url"`
	UpdatedAtType string           `json:"updated_at_type"`
}

var (
	sqliteTime = regexp.MustCompile(`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$`)
	isoTime    = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)
)

// sameJSON compares a value with recorded JSON after both went through
// encoding/json.
func sameJSON(t *testing.T, got any, want json.RawMessage) bool {
	t.Helper()
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	if err := json.Unmarshal(encoded, &a); err != nil {
		t.Fatal(err)
	}
	if len(want) == 0 {
		want = json.RawMessage("null")
	}
	if err := json.Unmarshal(want, &b); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(a, b)
}

func resultJSON(r Result) map[string]string {
	out := map[string]string{"action": r.Action}
	if r.Code != "" {
		out["code"] = r.Code
	}
	return out
}

// fetchCodes are the codes Node answered for a profile picture whose fetch
// failed.
var fetchCodes = map[string]bool{
	"address_refused": true, "dns_failed": true, "fetch_failed": true, "http_status": true, "not_an_image": true,
	"timeout": true, "too_large": true, "too_many_redirects": true, "type_mismatch": true,
}

// intendedProfileResult is Node's result of a profile picture sync with
// what Go answers on purpose in its place: one code, fetch_failed, for
// every failed fetch, where Node named the reason (RV10).
func intendedProfileResult(t *testing.T, recorded json.RawMessage) json.RawMessage {
	t.Helper()
	var result map[string]string
	if err := json.Unmarshal(recorded, &result); err != nil {
		t.Fatalf("profile result %s: %v", recorded, err)
	}
	if result["action"] != "failed" || !fetchCodes[result["code"]] {
		return recorded
	}
	result["code"] = "fetch_failed"
	out, _ := json.Marshal(result)
	return out
}

func TestOracleStore(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		var scenario storeScenario
		loadVectors(t, "store_"+db.Dialect().String(), &scenario)
		if len(scenario.Steps) < 100 {
			t.Fatalf("the scenario has %d steps", len(scenario.Steps))
		}
		ctx := context.Background()
		for _, statement := range scenario.Seed {
			if _, err := db.Exec(ctx, statement[0].(string), statement[1].([]any)...); err != nil {
				t.Fatalf("seed: %v", err)
			}
		}
		transport := &scriptedTransport{t: t}
		log := &warnings{}
		var offset time.Duration
		svc := New(Options{
			DB: db, Fetch: FetchOptions{Transport: transport}, Logger: log,
			Now: func() time.Time { return time.Now().Add(offset) },
		})

		for i, step := range scenario.Steps {
			what := fmt.Sprintf("step %d (%s, user %d)", i+1, step.Op, step.UserID)
			if step.Error != nil {
				t.Fatalf("%s: the scenario recorded an error: %s", what, *step.Error)
			}
			transport.answers = nil
			if step.Answer != nil {
				answer := scriptedAnswer{Status: step.Answer.Status, Headers: step.Answer.Headers}
				if step.Answer.Body != "" {
					answer.Chunks = []string{step.Answer.Body}
				}
				transport.answers = []scriptedAnswer{answer}
			}
			var got any
			switch step.Op {
			case "oidc":
				var exchange Claims
				if text := string(step.Exchange); text != "" && text != "null" {
					c := claims{}
					if err := json.Unmarshal(step.Exchange, &c); err != nil {
						t.Fatal(err)
					}
					exchange = c
				}
				got = resultJSON(svc.SyncOIDCPicture(ctx, step.UserID, exchange))
			case "profile":
				got = resultJSON(svc.SyncProfileAvatarURL(ctx, step.UserID, step.URL))
				step.Result = intendedProfileResult(t, step.Result)
			case "avatar":
				a, err := svc.GetAvatar(ctx, step.UserID)
				if err != nil {
					t.Fatalf("%s: %v", what, err)
				}
				if a != nil {
					plain := plainAvatar{UserID: a.UserID, MimeType: a.MimeType, Data: a.Data, Version: a.Version, Source: a.Source, SourceURL: a.SourceURL}
					plain.UpdatedAtType = "string"
					format := sqliteTime
					if db.IsPostgres() {
						plain.UpdatedAtType, format = "date", isoTime
					}
					got = plain
					if !format.MatchString(a.UpdatedAt.String()) {
						t.Errorf("%s: updated_at is %q", what, a.UpdatedAt.String())
					}
					// The scenario's own time is not compared
					var recorded map[string]json.RawMessage
					if err := json.Unmarshal(step.Result, &recorded); err != nil {
						t.Fatalf("%s: %v", what, err)
					}
					delete(recorded, "updated_at")
					step.Result, _ = json.Marshal(recorded)
				}
			case "version":
				got = svc.GetAvatarVersion(ctx, step.UserID)
			case "versions":
				versions, err := svc.GetVersionMap(ctx)
				if err != nil {
					t.Fatalf("%s: %v", what, err)
				}
				byText := map[string]string{}
				for id, version := range versions {
					byText[strconv.FormatInt(id, 10)] = version
				}
				got = byText
			case "list":
				entries, err := svc.ListDirectory(ctx)
				if err != nil {
					t.Fatalf("%s: %v", what, err)
				}
				got = entries
			case "fields":
				versions, err := svc.GetVersionMap(ctx)
				if err != nil {
					t.Fatalf("%s: %v", what, err)
				}
				type fields struct {
					ID            int64            `json:"id"`
					HasAvatar     bool             `json:"has_avatar"`
					AvatarVersion store.NullString `json:"avatar_version"`
				}
				list := []fields{}
				for _, id := range step.Users {
					has, version := versions.Fields(id)
					list = append(list, fields{id, has, version})
				}
				got = list
			case "sql":
				if _, err := db.Exec(ctx, step.SQL, step.Args...); err != nil {
					t.Fatalf("%s: %v", what, err)
				}
				continue
			case "advance":
				offset += time.Duration(step.Ms) * time.Millisecond
				continue
			case "reset":
				svc.ResetCache()
				continue
			default:
				t.Fatalf("%s: unknown step", what)
			}
			if !sameJSON(t, got, step.Result) {
				encoded, _ := json.Marshal(got)
				t.Errorf("%s: %s, Node %s", what, encoded, step.Result)
			}
			lines := log.take()
			if len(lines) != step.Warned {
				t.Errorf("%s: %d warnings %q, Node %d", what, len(lines), lines, step.Warned)
				continue
			}
			// The words of a database failure are the driver's own
			if step.Warned > 0 && !strings.Contains(step.Warning, "foreign key") && !strings.Contains(step.Warning, "FOREIGN KEY") {
				if want := strings.TrimPrefix(step.Warning, "[Avatar] "); lines[0] != want {
					t.Errorf("%s: warning %q, Node %q", what, lines[0], want)
				}
			}
		}
	})
}

func newTestService(t *testing.T, db *store.DB, fetch FetchOptions) (*Service, *warnings) {
	t.Helper()
	log := &warnings{}
	return New(Options{DB: db, Fetch: fetch, Logger: log}), log
}

func addUser(t *testing.T, db *store.DB, name string) int64 {
	t.Helper()
	id, err := db.Insert(context.Background(), `INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)`, name, "x", "viewer")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

const pngDataURL = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGNgYGAAAAAEAAH2FzhVAAAAAElFTkSuQmCC"

// A user id PostgreSQL's integer column cannot hold is an error there
// and no row on SQLite: the picture route answers 500 on the one and 404
// on the other.
func TestGetAvatarWithAnIDBeyondInteger(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		svc, _ := newTestService(t, db, FetchOptions{})
		a, err := svc.GetAvatar(context.Background(), 9999999999)
		if db.IsPostgres() {
			if err == nil {
				t.Fatalf("PostgreSQL read a picture for an id beyond integer: %v", a)
			}
			return
		}
		if err != nil || a != nil {
			t.Fatalf("SQLite: %v, %v; want no picture and no error", a, err)
		}
	})
}

func TestStartOIDCSync(t *testing.T) {
	db := storetest.Migrated(t)
	svc, _ := newTestService(t, db, FetchOptions{})
	user := addUser(t, db, "alice")
	ctx := context.Background()

	if _, ok := svc.WaitForSync(ctx, user); ok {
		t.Fatal("a sync is reported before one was started")
	}
	svc.StartOIDCSync(user, claims{"picture": pngDataURL})
	result, ok := svc.WaitForSync(ctx, user)
	svc.Wait()
	// The sync may have ended before the wait began; then nothing is pending
	if ok && result.Action != "stored" {
		t.Fatalf("the sync ended with %+v", result)
	}
	a, err := svc.GetAvatar(ctx, user)
	if err != nil || a == nil || a.Source != "oidc" || a.Version != ImageVersion(onePixelPNG) {
		t.Fatalf("after the sync: %+v, %v", a, err)
	}
	if _, ok := svc.WaitForSync(ctx, user); ok {
		t.Fatal("a finished sync is still pending")
	}
}

func TestWaitForSyncEndsWithItsContext(t *testing.T) {
	db := storetest.Migrated(t)
	release := make(chan struct{})
	transport := roundTripFunc(func(ctx context.Context) error {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return errors.New("released")
	})
	svc, _ := newTestService(t, db, FetchOptions{Transport: transport})
	user := addUser(t, db, "alice")
	svc.StartOIDCSync(user, claims{"picture": "http://pictures.example/a.png"})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, ok := svc.WaitForSync(ctx, user); ok {
		t.Fatal("the wait returned a result while the fetch was still running")
	}
	close(release)
	result, ok := svc.WaitForSync(context.Background(), user)
	svc.Wait()
	if ok && (result.Action != "failed" || result.Code != "fetch_failed") {
		t.Fatalf("the sync ended with %+v", result)
	}
}

// A failed write is "failed" with the code the database gave, and the
// picture the user had stays.
func TestFailedWritesKeepThePicture(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		svc, log := newTestService(t, db, FetchOptions{})
		ctx := context.Background()
		got := svc.SyncOIDCPicture(ctx, 4242, claims{"picture": pngDataURL})
		want := "SQLITE_CONSTRAINT_FOREIGNKEY"
		if db.IsPostgres() {
			want = "23503"
		}
		if got.Action != "failed" || got.Code != want {
			t.Fatalf("a picture for a user that does not exist: %+v, want failed with %s", got, want)
		}
		if lines := log.take(); len(lines) != 1 || !strings.HasPrefix(lines[0], "OIDC picture for user 4242 not stored ("+want+"): ") || !strings.HasSuffix(lines[0], "; inline picture") {
			t.Fatalf("warnings: %q", lines)
		}
		if a, err := svc.GetAvatar(ctx, 4242); err != nil || a != nil {
			t.Fatalf("a picture was stored: %v, %v", a, err)
		}
	})
}

// The warning about an address names its scheme, host and path, at most
// 200 characters of them, and never its query, fragment or credentials:
// identity providers put signed tokens there.
func TestWarningShowsNoQueryOrCredentials(t *testing.T) {
	db := storetest.Migrated(t)
	svc, log := newTestService(t, db, FetchOptions{Transport: &scriptedTransport{t: t}})
	user := addUser(t, db, "alice")
	long := "https://idp.example/" + strings.Repeat("a", 400)
	for _, c := range []struct{ address, want string }{
		{"https://idp.example/pictures/alice.png?token=signed-secret#part-secret", "https://idp.example/pictures/alice.png"},
		{"https://user:pass-secret@idp.example:8443/a.png?sig=secret", "https://idp.example:8443/a.png"},
		{`HTTPS:\\user:pass-secret@IDP.example\a.png?sig=secret`, "https://idp.example/a.png"},
		{"ftp://user:pass-secret@example.com/a.png?token=secret", "ftp://example.com/a.png"},
		{"not a picture?token=secret", "not a picture"},
		{long + "?token=secret", long[:200]},
	} {
		svc.SyncOIDCPicture(context.Background(), user, claims{"picture": c.address})
		lines := log.take()
		if len(lines) != 1 || !strings.HasSuffix(lines[0], "; "+c.want) || strings.Contains(lines[0], "secret") {
			t.Errorf("%s: warnings %q, want one that ends with %q", c.address, lines, c.want)
		}
	}
}

func TestDatabaseErrorCode(t *testing.T) {
	if got := databaseErrorCode(errors.New("plain")); got != "" {
		t.Fatalf("a plain error has the code %q", got)
	}
	if got := failed(errors.New("plain")); got.Code != "error" {
		t.Fatalf("a plain error fails with %+v", got)
	}
	if got := failed(fmt.Errorf("wrapped: %w", refuse("too_large", "x"))); got.Code != "too_large" {
		t.Fatalf("a wrapped refusal fails with %+v", got)
	}
}

// A version that is empty text counts as no picture.
func TestVersionFields(t *testing.T) {
	versions := Versions{1: "c47dd9465c00e9a0", 2: ""}
	if has, version := versions.Fields(1); !has || !version.Valid || version.String != "c47dd9465c00e9a0" {
		t.Errorf("a user with a picture: %v, %+v", has, version)
	}
	for _, id := range []int64{2, 3} {
		if has, version := versions.Fields(id); has || version.Valid {
			t.Errorf("user %d: %v, %+v; want no picture", id, has, version)
		}
	}
}

// A picture the user uploaded stays when they sign in with single sign-on
// again, with or without a picture claim; once they remove it, the claim
// fills the place again.
func TestOIDCSyncKeepsAnUploadedPicture(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		svc, _ := newTestService(t, db, FetchOptions{})
		ctx := context.Background()
		user := addUser(t, db, "alice")
		uploaded := append([]byte("\xff\xd8\xff"), make([]byte, 40)...)
		if _, err := svc.SetUploadedPicture(ctx, user, "image/jpeg", uploaded); err != nil {
			t.Fatal(err)
		}
		for _, exchange := range []claims{{"picture": pngDataURL}, {"picture": ""}} {
			if got := svc.SyncOIDCPicture(ctx, user, exchange); got.Action != "skipped" {
				t.Errorf("sync with %v: %+v, want skipped", exchange, got)
			}
			a, err := svc.GetAvatar(ctx, user)
			if err != nil || a == nil || a.Source != SourceUpload || a.Version != ImageVersion(uploaded) {
				t.Fatalf("after the sync with %v: %+v, %v", exchange, a, err)
			}
		}
		if removed, err := svc.RemovePicture(ctx, user); err != nil || !removed {
			t.Fatalf("remove: %v, %v", removed, err)
		}
		if got := svc.SyncOIDCPicture(ctx, user, claims{"picture": pngDataURL}); got.Action != "stored" {
			t.Fatalf("sync after the removal: %+v", got)
		}
		a, err := svc.GetAvatar(ctx, user)
		if err != nil || a == nil || a.Source != SourceOIDC {
			t.Fatalf("after the removal and a sync: %+v, %v", a, err)
		}
	})
}
