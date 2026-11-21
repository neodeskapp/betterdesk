package rustdeskapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/auditlog"
	"github.com/unitronix/betterdesk-server/console/authn"
	"github.com/unitronix/betterdesk-server/console/config"
	"github.com/unitronix/betterdesk-server/console/devicegroups"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/store"
)

// The parsers of outside input: the address book a client stores, the
// audit records it uploads and the body of a sign-in.

// fuzzConsole is the client API on a SQLite database behind the body
// parser, as a handler.
func fuzzConsole(f *testing.F) http.Handler {
	f.Helper()
	h, _ := directConsole(f, http.NotFoundHandler())
	return h
}

// directConsole is the client API on a SQLite database behind the body
// parser, with apiServer as the API server of every service, also of the
// sign-in.
func directConsole(tb testing.TB, apiServer http.Handler) (http.Handler, *store.DB) {
	tb.Helper()
	ctx := context.Background()
	db, err := store.OpenSQLite(filepath.Join(tb.TempDir(), "fuzz.sqlite3"))
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx); err != nil {
		tb.Fatal(err)
	}
	for id, name := range map[int]string{1: "e2e_admin", 6: "e2e_viewer"} {
		if _, err := db.Exec(ctx, "INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, 'x', ?)", id, name, strings.TrimPrefix(name, "e2e_")); err != nil {
			tb.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, "INSERT INTO peer (id, is_banned) VALUES ('900100001', 0), ('900100002', 1)"); err != nil {
		tb.Fatal(err)
	}
	acc := access.New(db, access.NewDBRoleSource(nil), access.Options{})
	groups := devicegroups.New(devicegroups.Options{DB: db, Access: acc})
	api := goapi.New(apiServer)
	signIn, err := authn.New(authn.Options{DB: db, GoAPI: api})
	if err != nil {
		tb.Fatal(err)
	}
	d := &kit.Deps{
		Early: kit.NewRouter(), Router: kit.NewRouter(), Store: db, Authn: signIn, Audit: auditlog.New(db),
		Access: acc, DeviceGroups: groups, GoAPI: api, Backend: goapi.NewBackend(api, devicegroups.DeviceStore{Groups: groups}),
		Log: kit.NewLogger("silent", true), Config: &config.Config{APIEnabled: true},
	}
	Register(d)
	return middleware.Bodies(middleware.BodyOptions{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !d.Early.Serve(w, kit.Begin(r, httpx.TrustProxy{}, nil)) {
			w.WriteHeader(http.StatusNotFound)
		}
	})), db
}

// reachesRoute tells whether the strict JSON parser hands a body to the
// route: an empty one, or a JSON object or array. It refuses anything else
// with its own 400.
func reachesRoute(body string) bool {
	if body == "" {
		return true
	}
	first := strings.TrimLeft(body, " \t\n\r")
	return json.Valid([]byte(body)) && first != "" && (first[0] == '{' || first[0] == '[')
}

func post(h http.Handler, path string, body []byte) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(body)))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// FuzzAddressBook stores what a client may send as address book data and
// checks what it reads back: valid JSON in the form JSON.stringify writes,
// with a list of peers and a list of tags.
func FuzzAddressBook(f *testing.F) {
	for _, seed := range []string{
		`{"tags":["a"],"peers":[{"id":"900100001","tags":["x"]}]}`, `{"peers":[{"id":`, `[1,2]`, `"text"`, `null`, ``,
		`{"2":1,"1":2,"__proto__":{"a":1},"peers":[{"id":5,"tags":"a, b"}],"tags":"x,y"}`, `{"a":[-0,1e-7,1e21," "]}`,
		`{"peers":[{"id":{"toString":1},"tags":[{"a":1},null,true]}]}`, `{"":[{"id":"900100001"}]}`,
	} {
		f.Add(seed)
	}
	devices := []any{
		goapi.NewObject("id", "900100001", "tags", "office,lab", "hostname", "h"),
		goapi.NewObject("id", "900100002", "banned", true),
	}
	f.Fuzz(func(t *testing.T, data string) {
		merged := jsJSON(devicegroups.MergeAddressBookData(data, devicegroups.MergeOptions{Devices: devices, KnownOnly: true}))
		scoped := jsJSON(devicegroups.FilterAddressBookPeersByScope(merged, func(id string) bool { return id != "900100001" }, []any{"900100001", "900100002"}))
		for _, text := range []string{merged, scoped} {
			parsed, err := goapi.Parse([]byte(text))
			if err != nil {
				t.Fatalf("the address book read back is no JSON: %q", text)
			}
			book, ok := parsed.(*goapi.Object)
			if !ok {
				t.Fatalf("the address book read back is no object: %q", text)
			}
			if _, ok := book.Get("peers").([]any); !ok {
				t.Fatalf("no list of peers: %q", text)
			}
			if _, ok := book.Get("tags").([]any); !ok {
				t.Fatalf("no list of tags: %q", text)
			}
			if again := stringify(parsed); again != text {
				t.Fatalf("not in the form JSON.stringify writes:\n%q\n%q", text, again)
			}
		}
		book, _ := goapi.Parse([]byte(scoped))
		for _, p := range book.(*goapi.Object).Get("peers").([]any) {
			if peer, ok := p.(*goapi.Object); ok && peer.Get("id") == "900100001" {
				t.Fatalf("a device outside the scope stayed in the book: %q", scoped)
			}
		}
		_ = devicegroups.CollectPeerTagUpdates(data)
	})
}

// FuzzAuditUploads sends upload bodies to the three audit routes: every
// answer is JSON with a status the routes have, and a refusal is one of
// their texts. No device is registered, so nothing is taken (BUGS RV2).
func FuzzAuditUploads(f *testing.F) {
	for _, seed := range []string{
		`{}`, `{"host_id":"h","conn_type":1}`, `{"host_id":5,"files":["a",{"name":"b","size":-1},null]}`,
		`{"alarm_type":6,"details":{"a":[1]}}`, `{"host_id":{"toString":1}}`, `[1]`, `{"host_id":"h","files":"x","is_file":0}`, `""`, ` {} `,
	} {
		f.Add(seed)
	}
	h := fuzzConsole(f)
	refusals := map[string]bool{
		`{"error":"host_id is required"}`: true, `{"error":"Invalid conn_type"}`: true,
		`{"error":"Invalid alarm_type (0-6)"}`: true, `{"error":"Server error"}`: true,
	}
	f.Fuzz(func(t *testing.T, body string) {
		for _, path := range []string{"/api/audit/conn", "/api/audit/file", "/api/audit/alarm"} {
			w := post(h, path, []byte(body))
			if !reachesRoute(body) {
				continue
			}
			switch w.Code {
			case http.StatusNotFound:
				if w.Body.String() != `{"error":"Device not found"}` {
					t.Fatalf("%s %q: 404 %s", path, body, w.Body)
				}
			case http.StatusBadRequest, http.StatusInternalServerError:
				if !refusals[w.Body.String()] && !strings.Contains(w.Body.String(), "Internal Server Error") {
					t.Fatalf("%s %q: %d %s", path, body, w.Code, w.Body)
				}
			default:
				t.Fatalf("%s %q: status %d", path, body, w.Code)
			}
		}
	})
}

// FuzzLoginBody sends sign-in bodies: no answer but a success carries a
// token, and every answer has a status of the route.
func FuzzLoginBody(f *testing.F) {
	for _, seed := range []string{
		`{"username":"e2e_viewer","password":"x"}`, `{"username":"  ","password":"x"}`, `{"username":["a"],"password":5}`,
		`{"username":"e2e_viewer","password":"y","id":"900100002"}`, `{"tfaCode":"1","secret":"x"}`, `{"verificationCode":1,"secret":[]}`,
		`{"username":"nobody","password":"p","id":{"toString":1},"uuid":[1,2]}`, `[]`, `{"__proto__":{"username":"e2e_admin"}}`, `0`,
	} {
		f.Add(seed)
	}
	h := fuzzConsole(f)
	allowed := map[int]bool{200: true, 400: true, 401: true, 403: true, 409: true, 429: true, 500: true}
	f.Fuzz(func(t *testing.T, body string) {
		w := post(h, "/api/login", []byte(body))
		if !reachesRoute(body) {
			return
		}
		if !allowed[w.Code] {
			t.Fatalf("%q: status %d", body, w.Code)
		}
		if w.Code != http.StatusOK && strings.Contains(w.Body.String(), "access_token") {
			t.Fatalf("%q: a refusal with a token: %s", body, w.Body)
		}
		if !json.Valid(w.Body.Bytes()) {
			t.Fatalf("%q: answer is no JSON: %s", body, w.Body)
		}
	})
}
