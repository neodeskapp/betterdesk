package kit

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/authn"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
	"github.com/unitronix/betterdesk-server/console/webui"
)

// deviceWorld is the two device guards in front of a handler that answers
// what it sees, behind the body step of the pipeline, on db. It answers
// the paths of testdata/nodeoracle/devicetoken.js:
//
//	/api/probe/token        RequireDeviceToken
//	/api/probe/match        RequireDeviceToken, RequireTokenDeviceMatch
//	/api/probe/match-alone  RequireTokenDeviceMatch
func deviceWorld(t *testing.T, db *store.DB) http.Handler {
	t.Helper()
	service, err := authn.New(authn.Options{DB: db, GoAPI: goapi.New(http.NotFoundHandler())})
	if err != nil {
		t.Fatal(err)
	}
	d := &Deps{Authn: service, WebUI: webui.New(webui.Options{Production: true})}
	answer := http.HandlerFunc(probeAnswer)
	routes := map[string]http.Handler{
		"/api/probe/token":       d.RequireDeviceToken(answer),
		"/api/probe/match":       d.RequireDeviceToken(d.RequireTokenDeviceMatch(answer)),
		"/api/probe/match-alone": d.RequireTokenDeviceMatch(answer),
	}
	return middleware.Bodies(middleware.BodyOptions{})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route, ok := routes[r.URL.Path]
		if !ok {
			t.Fatalf("no probe at %s", r.URL.Path)
		}
		route.ServeHTTP(w, Begin(r, httpx.TrustProxy{}, nil))
	}))
}

type probed struct {
	Next     bool   `json:"next"`
	DeviceID string `json:"deviceId"`
	ClientID string `json:"clientId"`
	UserID   int64  `json:"userId"`
}

// probeAnswer is the handler behind the guards: what kit.Device says.
func probeAnswer(w http.ResponseWriter, r *http.Request) {
	device := Device(r)
	if device == nil {
		httpx.WriteJSON(w, r, http.StatusOK, struct {
			Next bool `json:"next"`
		}{true})
		return
	}
	httpx.WriteJSON(w, r, http.StatusOK, probed{Next: true, DeviceID: device.DeviceID, ClientID: device.Token.ClientID.String, UserID: device.Token.UserID.V})
}

// deviceRecording is a file of testdata/nodeoracle/devicetoken.js.
type deviceRecording struct {
	Dialect string              `json:"dialect"`
	RawSeed [][]json.RawMessage `json:"seed"`
	Steps   []struct {
		Name    string  `json:"name"`
		Raw     string  `json:"raw"`
		SQL     string  `json:"sql"`
		Params  []any   `json:"params"`
		Status  int     `json:"status"`
		Answer  string  `json:"answer"`
		Touched []int64 `json:"touched"`
	} `json:"steps"`
}

// statementArgs are the parameters of a recorded statement: JSON has one
// kind of number, and the statements bind ids and counts as integers.
func statementArgs(params []any) []any {
	args := make([]any, len(params))
	for i, p := range params {
		args[i] = p
		if f, ok := p.(float64); ok && f == float64(int64(f)) {
			args[i] = int64(f)
		}
	}
	return args
}

// bytesOf turns a recorded request back into its bytes: each character
// of the text stands for one byte.
func bytesOf(text string) []byte {
	out := make([]byte, 0, len(text))
	for _, r := range text {
		out = append(out, byte(r))
	}
	return out
}

// readRaw parses a request as the server would read it from the wire.
func readRaw(t *testing.T, raw string) *http.Request {
	t.Helper()
	r, err := http.ReadRequest(bufio.NewReader(strings.NewReader(string(bytesOf(raw)))))
	if err != nil {
		t.Fatalf("read the request %q: %v", raw, err)
	}
	r.RemoteAddr = "127.0.0.1:40000"
	return r
}

func touchedTokens(t *testing.T, db *store.DB) []int64 {
	t.Helper()
	rows, err := db.Query(context.Background(), "SELECT id FROM access_tokens WHERE last_used IS NOT NULL ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

// TestDeviceGuardsAnswerAsNode replays what requireDeviceToken and
// requireTokenDeviceMatch of the Node console answered to the same bytes
// on the same token rows (testdata/nodeoracle/devicetoken.js): status,
// body byte for byte, and which token rows got their use noted.
func TestDeviceGuardsAnswerAsNode(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		data, err := os.ReadFile(filepath.Join("testdata", "device_"+db.Dialect().String()+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var rec deviceRecording
		if err := json.Unmarshal(data, &rec); err != nil {
			t.Fatal(err)
		}
		if rec.Dialect != db.Dialect().String() {
			t.Fatalf("the recording is of %s", rec.Dialect)
		}
		for _, pair := range rec.RawSeed {
			var sql string
			var params []any
			if err := json.Unmarshal(pair[0], &sql); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(pair[1], &params); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(ctx, sql, statementArgs(params)...); err != nil {
				t.Fatalf("seed %s: %v", sql, err)
			}
		}
		h := deviceWorld(t, db)
		requests, refused, thrown := 0, 0, 0
		for _, step := range rec.Steps {
			if step.SQL != "" {
				if _, err := db.Exec(ctx, step.SQL, statementArgs(step.Params)...); err != nil {
					t.Fatalf("%s: %v", step.SQL, err)
				}
				continue
			}
			if step.Touched != nil {
				if _, err := db.Exec(ctx, "UPDATE access_tokens SET last_used = NULL WHERE last_used IS NOT NULL"); err != nil {
					t.Fatal(err)
				}
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, readRaw(t, step.Raw))
			requests++
			switch {
			case rec.Code == 500:
				thrown++
			case rec.Code >= 400:
				refused++
			}
			if rec.Code != step.Status || rec.Body.String() != step.Answer {
				t.Errorf("%s:\n got %d %s\nNode %d %s", step.Name, rec.Code, rec.Body.String(), step.Status, step.Answer)
				continue
			}
			if step.Touched != nil {
				if got := touchedTokens(t, db); !reflect.DeepEqual(got, step.Touched) {
					t.Errorf("%s: noted the use of tokens %v, Node %v", step.Name, got, step.Touched)
				}
			}
		}
		if requests < 500 || refused < 300 || thrown < 20 {
			t.Errorf("%d requests, %d refused, %d server errors: the recording is thin", requests, refused, thrown)
		}
	})
}

// addToken stores a token row the way the RustDesk sign-in does.
func addToken(t *testing.T, db *store.DB, token, client string, expires string, revoked bool) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.Exec(ctx, "INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, 'x', 'operator') ON CONFLICT DO NOTHING", 7, "owner"); err != nil {
		t.Fatal(err)
	}
	flag := any(revoked)
	if !db.IsPostgres() {
		flag = 0
		if revoked {
			flag = 1
		}
	}
	if _, err := db.Exec(ctx, "INSERT INTO access_tokens (token, token_hash, user_id, client_id, expires_at, revoked) VALUES (?, ?, 7, ?, ?, ?)",
		token, authn.HashAccessToken(token), client, expires, flag); err != nil {
		t.Fatal(err)
	}
}

type deviceCase struct {
	name    string
	path    string
	method  string
	headers []string
	body    string
	form    bool
	status  int
	answer  string
}

func (c deviceCase) request() *http.Request {
	method := c.method
	if method == "" {
		method = http.MethodGet
	}
	var r *http.Request
	if c.body != "" {
		r = httptest.NewRequest(method, c.path, strings.NewReader(c.body))
		r.Header.Set("Content-Type", "application/json")
		if c.form {
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	} else {
		r = httptest.NewRequest(method, c.path, nil)
	}
	for i := 0; i+1 < len(c.headers); i += 2 {
		r.Header.Add(c.headers[i], c.headers[i+1])
	}
	return r
}

const (
	answerA        = `{"next":true,"deviceId":"device-a","clientId":"device-a","userId":7}`
	noBearer       = `{"error":"Bearer access token required"}`
	unbound        = `{"error":"Invalid or unbound access token"}`
	lookupFailed   = `{"error":"Invalid or expired access token"}`
	mismatch       = `{"error":"Device ID mismatch"}`
	serverError    = `{"success":false,"error":"Internal Server Error"}`
	futureExpiry   = "2999-01-01 00:00:00"
	pastExpiry     = "2001-01-01 00:00:00"
	tokenA         = "token-of-device-a"
	tokenB         = "token-of-device-b"
	bearerA        = "Bearer " + tokenA
	passedNoDevice = `{"next":true}`
)

func TestDeviceGuardsAnswer(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		addToken(t, db, tokenA, "device-a", futureExpiry, false)
		addToken(t, db, tokenB, "device-b", futureExpiry, false)
		addToken(t, db, "token-expired", "device-a", pastExpiry, false)
		addToken(t, db, "token-revoked", "device-a", futureExpiry, true)
		addToken(t, db, "token-unbound", "", futureExpiry, false)
		addToken(t, db, "token-bad-id", "a b", futureExpiry, false)
		h := deviceWorld(t, db)
		cases := []deviceCase{
			{name: "no header", path: "/api/probe/token", status: 401, answer: noBearer},
			{name: "another scheme", path: "/api/probe/token", headers: []string{"Authorization", "Basic " + tokenA}, status: 401, answer: noBearer},
			{name: "two words", path: "/api/probe/token", headers: []string{"Authorization", bearerA + " x"}, status: 401, answer: noBearer},
			{name: "X-Device-Id is no credential", path: "/api/probe/token", headers: []string{"X-Device-Id", "device-a"}, status: 401, answer: noBearer},
			{name: "unknown token", path: "/api/probe/token", headers: []string{"Authorization", "Bearer nope"}, status: 401, answer: unbound},
			{name: "expired", path: "/api/probe/token", headers: []string{"Authorization", "Bearer token-expired"}, status: 401, answer: unbound},
			{name: "revoked", path: "/api/probe/token", headers: []string{"Authorization", "Bearer token-revoked"}, status: 401, answer: unbound},
			{name: "bound to no device", path: "/api/probe/token", headers: []string{"Authorization", "Bearer token-unbound"}, status: 401, answer: unbound},
			{name: "bound to a bad id", path: "/api/probe/token", headers: []string{"Authorization", "Bearer token-bad-id"}, status: 401, answer: unbound},
			{name: "good token", path: "/api/probe/token", headers: []string{"Authorization", bearerA}, status: 200, answer: answerA},
			{name: "the first of two headers counts", path: "/api/probe/token", headers: []string{"Authorization", bearerA, "Authorization", "Bearer nope"}, status: 200, answer: answerA},

			{name: "names no device", path: "/api/probe/match", headers: []string{"Authorization", bearerA}, status: 200, answer: answerA},
			{name: "body names itself", path: "/api/probe/match", method: "POST", body: `{"device_id":"device-a"}`, headers: []string{"Authorization", bearerA}, status: 200, answer: answerA},
			{name: "body names another", path: "/api/probe/match", method: "POST", body: `{"device_id":"device-b"}`, headers: []string{"Authorization", bearerA}, status: 403, answer: mismatch},
			{name: "body comes before the query", path: "/api/probe/match?device_id=device-b", method: "POST", body: `{"device_id":"device-a"}`, headers: []string{"Authorization", bearerA}, status: 200, answer: answerA},
			{name: "an empty body value gives way", path: "/api/probe/match?device_id=device-b", method: "POST", body: `{"device_id":""}`, headers: []string{"Authorization", bearerA}, status: 403, answer: mismatch},
			{name: "a list in the body is its text", path: "/api/probe/match", method: "POST", body: `{"device_id":["device-a"]}`, headers: []string{"Authorization", bearerA}, status: 200, answer: answerA},
			{name: "a form body", path: "/api/probe/match", method: "POST", body: "device_id=device-b", form: true, headers: []string{"Authorization", bearerA}, status: 403, answer: mismatch},
			{name: "query names another", path: "/api/probe/match?device_id=device-b", headers: []string{"Authorization", bearerA}, status: 403, answer: mismatch},
			{name: "query as a list", path: "/api/probe/match?device_id[]=device-a", headers: []string{"Authorization", bearerA}, status: 200, answer: answerA},
			{name: "query as an object", path: "/api/probe/match?device_id[x]=device-a", headers: []string{"Authorization", bearerA}, status: 403, answer: mismatch},
			{name: "query comes before the header", path: "/api/probe/match?device_id=device-a", headers: []string{"Authorization", bearerA, "X-Device-Id", "device-b"}, status: 200, answer: answerA},
			{name: "header names another", path: "/api/probe/match", headers: []string{"Authorization", bearerA, "X-Device-Id", "device-b"}, status: 403, answer: mismatch},
			{name: "header empty and last", path: "/api/probe/match", headers: []string{"Authorization", bearerA, "X-Device-Id", ""}, status: 403, answer: mismatch},
			{name: "header given twice", path: "/api/probe/match", headers: []string{"Authorization", bearerA, "X-Device-Id", "device-a", "X-Device-Id", "device-a"}, status: 403, answer: mismatch},
			{name: "empty everywhere but no header", path: "/api/probe/match?device_id=", method: "POST", body: `{"device_id":null}`, headers: []string{"Authorization", bearerA}, status: 200, answer: answerA},
			{name: "a number in the body", path: "/api/probe/match", method: "POST", body: `{"device_id":7}`, headers: []string{"Authorization", bearerA}, status: 403, answer: mismatch},
			{name: "the token check comes first", path: "/api/probe/match", method: "POST", body: `{"device_id":"device-b"}`, status: 401, answer: noBearer},

			{name: "body without text", path: "/api/probe/match", method: "POST", body: `{"device_id":{"toString":1}}`, headers: []string{"Authorization", bearerA}, status: 500, answer: serverError},
			{name: "body list without text", path: "/api/probe/match", method: "POST", body: `{"device_id":[[{"toString":1}]]}`, headers: []string{"Authorization", bearerA}, status: 500, answer: serverError},
			{name: "body object with valueOf has text", path: "/api/probe/match", method: "POST", body: `{"device_id":{"valueOf":1}}`, headers: []string{"Authorization", bearerA}, status: 403, answer: mismatch},
			{name: "query without text", path: "/api/probe/match?device_id[toString]=x", headers: []string{"Authorization", bearerA}, status: 500, answer: serverError},
			{name: "query without text behind a body", path: "/api/probe/match?device_id[toString]=x", method: "POST", body: `{"device_id":"device-a"}`, headers: []string{"Authorization", bearerA}, status: 200, answer: answerA},
			{name: "body without text behind nothing", path: "/api/probe/match?device_id=device-a", method: "POST", body: `{"other":{"toString":1}}`, headers: []string{"Authorization", bearerA}, status: 200, answer: answerA},

			{name: "alone, no claim", path: "/api/probe/match-alone", status: 200, answer: passedNoDevice},
			{name: "alone, empty header", path: "/api/probe/match-alone", headers: []string{"X-Device-Id", ""}, status: 403, answer: mismatch},
			{name: "alone, a device", path: "/api/probe/match-alone?device_id=device-a", status: 403, answer: mismatch},
			{name: "alone, an empty body value", path: "/api/probe/match-alone", method: "POST", body: `{"device_id":""}`, status: 200, answer: passedNoDevice},
		}
		for _, c := range cases {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, c.request())
			if rec.Code != c.status || rec.Body.String() != c.answer {
				t.Errorf("%s: got %d %s, want %d %s", c.name, rec.Code, rec.Body.String(), c.status, c.answer)
			}
			if got := rec.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
				t.Errorf("%s: Content-Type %q", c.name, got)
			}
			if c.status != 500 && rec.Header().Get("ETag") == "" {
				t.Errorf("%s: no ETag", c.name)
			}
		}
	})
}

func TestDeviceTokenNotesItsUse(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		addToken(t, db, tokenA, "device-a", futureExpiry, false)
		addToken(t, db, tokenB, "device-b", futureExpiry, false)
		h := deviceWorld(t, db)
		for _, c := range []deviceCase{
			{path: "/api/probe/token", headers: []string{"Authorization", "Bearer nope"}},
			{path: "/api/probe/match", headers: []string{"Authorization", "Bearer " + tokenB, "X-Device-Id", "device-a"}},
		} {
			h.ServeHTTP(httptest.NewRecorder(), c.request())
		}
		// A refused match was still a good token: Node noted it before the
		// match ran
		if got := touchedTokens(t, db); !reflect.DeepEqual(got, []int64{2}) {
			t.Errorf("tokens with a noted use: %v, want [2]", got)
		}
	})
}

func TestDeviceTokenLookupFails(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		addToken(t, db, tokenA, "device-a", futureExpiry, false)
		h := deviceWorld(t, db)
		if _, err := db.Exec(context.Background(), "DROP TABLE access_tokens"); err != nil {
			t.Fatal(err)
		}
		for _, c := range []deviceCase{
			{path: "/api/probe/token", headers: []string{"Authorization", bearerA}, status: 401, answer: lookupFailed},
			{path: "/api/probe/token", status: 401, answer: noBearer},
		} {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, c.request())
			if rec.Code != c.status || rec.Body.String() != c.answer {
				t.Errorf("%v: got %d %s, want %d %s", c.headers, rec.Code, rec.Body.String(), c.status, c.answer)
			}
		}
	})
}

func TestDeviceIsNilWithoutTheGuard(t *testing.T) {
	if Device(httptest.NewRequest("GET", "/", nil)) != nil {
		t.Error("a request that passed no guard has a device")
	}
}

func TestDeviceGuardServerErrorWithoutTheWebApp(t *testing.T) {
	d := &Deps{}
	r := httptest.NewRequest("POST", "/api/bd/x", strings.NewReader(`{"device_id":{"toString":1}}`))
	r.Header.Set("Content-Type", "application/json")
	reached := false
	h := middleware.Bodies(middleware.BodyOptions{})(d.RequireTokenDeviceMatch(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if reached || rec.Code != 500 || rec.Body.String() != serverError {
		t.Errorf("got %d %s, reached %v", rec.Code, rec.Body.String(), reached)
	}
}
