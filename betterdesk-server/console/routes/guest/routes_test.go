package guest

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/goapipath/proxytest"
	"github.com/unitronix/betterdesk-server/console/guestaccess"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/limits"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// vector is a proxytest vector with what the vectors of this group add:
// the device scope the account has, what the scope was asked for, and the
// links the socket relay was told are gone.
type vector struct {
	proxytest.Vector
	Scope     json.RawMessage `json:"scope"`
	ScopeCall *struct {
		Database bool `json:"database"`
		User     *struct {
			ID       int64  `json:"id"`
			Username string `json:"username"`
			Role     string `json:"role"`
		} `json:"user"`
		IDs []string `json:"ids"`
	} `json:"scopeCall"`
	Revoked []string `json:"revoked"`
}

// listed is a device scope that holds the ids of a list.
type listed map[string]bool

func (l listed) Unlimited() bool    { return false }
func (l listed) Has(id string) bool { return l[id] }

type unlimited struct{}

func (unlimited) Unlimited() bool { return true }
func (unlimited) Has(string) bool { return true }

// world is the group behind proxytest's pipeline, with a scripted device
// scope and a listener for deleted links.
type world struct {
	*proxytest.World
	scope   json.RawMessage
	asked   *scopeCall
	revoked []string
}

type scopeCall struct {
	user *access.ScopeUser
	ids  []string
}

func newWorld(t *testing.T) *world {
	w := &world{}
	w.World = proxytest.NewWorld(t, func(d *kit.Deps) {
		d.Limits = limits.NewSet(limits.Options{
			ClientIP: httpx.SocketIP,
			UserKey:  func(*http.Request) string { return "" },
			Getenv:   func(string) string { return "100000000" },
		})
		d.GuestAccess = guestaccess.New(guestaccess.Options{GoAPI: d.GoAPI})
		d.GuestAccess.OnRevoke(func(id string) int {
			w.revoked = append(w.revoked, id)
			return 0
		})
		register(d, func(_ context.Context, user *access.ScopeUser, devices []access.Device) (deviceScope, error) {
			call := &scopeCall{user: user}
			for _, device := range devices {
				call.ids = append(call.ids, device.ID)
			}
			w.asked = call
			var thrown struct {
				Throws *string `json:"throws"`
			}
			if json.Unmarshal(w.scope, &thrown) == nil && thrown.Throws != nil {
				return nil, errors.New(*thrown.Throws)
			}
			var ids []string
			if string(w.scope) == "null" || len(w.scope) == 0 || json.Unmarshal(w.scope, &ids) != nil {
				return unlimited{}, nil
			}
			scope := listed{}
			for _, id := range ids {
				scope[id] = true
			}
			return scope, nil
		}, anyDevice)
	})
	return w
}

// anyDevice is a device table that has every id, without a folder or
// tags.
func anyDevice(_ context.Context, id string) *goapi.Object { return goapi.NewObject("id", id) }

var (
	cookieExpires = regexp.MustCompile(`Expires=[A-Za-z]{3}, [0-9]{2} [A-Za-z]{3} [0-9]{4} [0-9:]{8} GMT`)
	listIndex     = regexp.MustCompile(`\[[0-9]+\]`)
)

// cookies writes the Set-Cookie headers of an answer the way the vectors
// hold them, with the day a cookie ends left out: it is a day from the
// moment of the request.
func cookies(header []string) string {
	var guest []string
	for _, value := range header {
		// The session cookie of the test's pipeline is not the group's
		if strings.HasPrefix(value, "bd.guest=") || strings.HasPrefix(value, "betterdesk.guest=") {
			guest = append(guest, value)
		}
	}
	if len(guest) == 0 {
		return ""
	}
	return cookieExpires.ReplaceAllString("cookie: "+strings.Join(guest, " | "), "Expires=<date>")
}

// describeCalls writes the calls the API server got, with the query of
// each as the API server reads it (see the same function of package
// cdap for the two spellings that differ from the Node console's client).
func describeCalls(calls []proxytest.Call) string {
	out := make([]proxytest.Call, len(calls))
	for i, call := range calls {
		out[i] = call
		path, query, hasQuery := strings.Cut(call.URL, "?")
		if !hasQuery {
			continue
		}
		var pairs []string
		for _, pair := range strings.Split(query, "&") {
			name, value, _ := strings.Cut(pair, "=")
			name, _ = url.QueryUnescape(name)
			value, _ = url.QueryUnescape(value)
			pairs = append(pairs, strconv.Quote(listIndex.ReplaceAllString(name, "[]"))+"="+strconv.Quote(value))
		}
		out[i].URL = path + "?" + strings.Join(pairs, "&")
	}
	text, _ := json.Marshal(out)
	return string(text)
}

// TestVectors replays what routes/guest.routes.js of the Node console
// answered, asked the API server, asked the device scope and told the
// socket relay (testdata/nodeoracle/run.js). The vectors a bug fix
// changed on purpose (fixedVectors) are compared with what the fix
// answers instead.
func TestVectors(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "vectors.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vectors []vector
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	if len(vectors) < 2300 {
		t.Fatalf("%d vectors, the file should hold more than 2300", len(vectors))
	}
	w := newWorld(t)
	failures, spelledDifferently := 0, 0
	used := map[string]bool{}
	for i, v := range vectors {
		key := vectorKey(v)
		for bug, fixes := range fixedVectors {
			if fix, ok := fixes[key]; ok {
				fix(&v)
				used[bug+" "+key] = true
			}
		}
		for _, f := range fixedWhere {
			if f.match(v) {
				f.fix(&v)
				used[f.bug+" where"] = true
			}
		}
		w.scope, w.asked, w.revoked = v.Scope, nil, nil
		rec, calls := w.Do(v.Request, v.API)
		var problems []string
		if rec.Code != v.Status {
			problems = append(problems, "status "+strconv.Itoa(rec.Code)+", want "+strconv.Itoa(v.Status))
		}
		if got := rec.Header().Get("Content-Type"); got != v.Type {
			problems = append(problems, "Content-Type "+strconv.Quote(got)+", want "+strconv.Quote(v.Type))
		}
		if got, want := cookies(rec.Header().Values("Set-Cookie")), cookieExpires.ReplaceAllString(v.Disposition, "Expires=<date>"); got != want {
			problems = append(problems, "Set-Cookie "+strconv.Quote(got)+"\n      want "+strconv.Quote(want))
		}
		if got := rec.Body.String(); got != v.Body {
			problems = append(problems, "body "+strconv.Quote(got)+"\n      want "+strconv.Quote(v.Body))
		}
		if got, want := describeCalls(calls), describeCalls(v.Calls); got != want {
			problems = append(problems, "API calls "+got+"\n      want "+want)
		} else {
			for n := range calls {
				if calls[n] != v.Calls[n] {
					spelledDifferently++
					break
				}
			}
		}
		switch {
		case (w.asked == nil) != (v.ScopeCall == nil):
			problems = append(problems, "the device scope was asked: "+strconv.FormatBool(w.asked != nil)+", want "+strconv.FormatBool(v.ScopeCall != nil))
		case w.asked != nil:
			if got, want := strings.Join(w.asked.ids, "|"), strings.Join(v.ScopeCall.IDs, "|"); got != want {
				problems = append(problems, "scope asked for the devices "+got+", want "+want)
			}
			switch {
			case (w.asked.user == nil) != (v.ScopeCall.User == nil):
				problems = append(problems, "scope asked with an account: "+strconv.FormatBool(w.asked.user != nil))
			case w.asked.user != nil:
				if u, want := w.asked.user, v.ScopeCall.User; u.ID != want.ID || u.Username != want.Username || u.Role != want.Role {
					problems = append(problems, "scope asked for another account than "+want.Username)
				}
			}
		}
		if got, want := strings.Join(w.revoked, "|"), strings.Join(v.Revoked, "|"); got != want {
			problems = append(problems, "links told as gone: "+strconv.Quote(got)+", want "+strconv.Quote(want))
		}
		if len(problems) > 0 {
			failures++
			if failures <= 20 {
				body := ""
				if v.Request.Body != nil {
					body = " " + *v.Request.Body
				}
				api, _ := json.Marshal(v.API)
				t.Errorf("vector %d: %s %s%s cookie %q scope %s (API answers %s):\n  %s", i, v.Request.Method, v.Request.Path, body, v.Request.Headers["Cookie"], v.Scope, api, strings.Join(problems, "\n  "))
			}
		}
	}
	if failures > 20 {
		t.Errorf("%d of %d vectors differ; the first 20 are shown", failures, len(vectors))
	}
	for _, f := range fixedWhere {
		if !used[f.bug+" where"] {
			t.Errorf("%s: no vector matches", f.bug)
		}
	}
	for bug, fixes := range fixedVectors {
		for key := range fixes {
			if !used[bug+" "+key] {
				t.Errorf("%s: no vector %q", bug, key)
			}
		}
	}
	t.Logf("%d vectors, %d of them with a call for the API server spelled differently", len(vectors), spelledDifferently)
}

func TestRegisteredRoutes(t *testing.T) {
	d := &kit.Deps{Router: kit.NewRouter(), Log: kit.NewLogger("error", true), Limits: limits.NewSet(limits.Options{ClientIP: httpx.SocketIP, UserKey: func(*http.Request) string { return "" }})}
	Register(d)
	var got []string
	for _, route := range d.Router.Routes() {
		got = append(got, route.Method+" "+route.Path)
	}
	want := []string{
		"POST /api/guest/access-links",
		"GET /api/guest/access-links",
		"DELETE /api/guest/access-links/{id}",
		"GET /api/guest/access-links/validate",
		"GET /api/guest/access-links/peers",
		"POST /api/guest/mesh/devices/{id}/desktop",
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("registered routes:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// aborted reports whether the handler closed the connection without an
// answer.
func aborted(t *testing.T, w *world, req proxytest.Request, api *proxytest.Answer) (closed bool, rec *httptest.ResponseRecorder) {
	t.Helper()
	defer func() {
		if p := recover(); p != nil {
			if p != http.ErrAbortHandler {
				panic(p)
			}
			closed = true
		}
	}()
	rec, _ = w.Do(req, api)
	return false, rec
}

// TestTokensWithoutATextAreRefused covers the requests the old console
// stopped at (BUGS.md MC1, GA1), which no vector can hold: a guest token,
// share token or guest cookie that is no text is refused with 400 before
// the API server is asked, as the mesh desktop route refuses it.
func TestTokensWithoutATextAreRefused(t *testing.T) {
	w := newWorld(t)
	grant := &proxytest.Answer{Status: 200, Type: "application/json", Body: `{"valid":true,"peer_ids":["dev-1"]}`}
	const badGuest, badShare = `{"error":"Invalid guest token"}`, `{"error":"Invalid share token"}`
	body := "{}"
	refused := func(what string, req proxytest.Request, want string) {
		t.Helper()
		rec, calls := w.Do(req, grant)
		if rec.Code != 400 || rec.Body.String() != want || len(calls) != 0 {
			t.Errorf("%s: %d %s, %d calls; want 400 %s", what, rec.Code, rec.Body, len(calls), want)
		}
	}
	for _, target := range []string{
		"/api/guest/access-links/peers?guest[toString]=x",
		"/api/guest/access-links/peers?t[toString]=x",
		"/api/guest/access-links/peers?guest[][toString]=x",
	} {
		refused("GET "+target, proxytest.Request{Path: target}, badGuest)
	}
	for query, want := range map[string]string{
		"guest[toString]=x":                badGuest,
		"t[toString]=x":                    badGuest,
		"mesh_share[toString]=x":           badShare,
		"guest=tok&mesh_share[toString]=x": badShare,
	} {
		refused("POST ?"+query, proxytest.Request{Method: "POST", Path: "/api/guest/mesh/devices/dev-1/desktop?" + query, Type: "application/json", Body: &body}, want)
	}
	cookie := map[string]string{"Cookie": `bd.guest=j:{"toString":1}`}
	refused("a cookie without a text", proxytest.Request{Path: "/api/guest/access-links/peers", Headers: cookie}, badGuest)
	refused("a cookie without a text on the desktop route", proxytest.Request{Method: "POST", Path: "/api/guest/mesh/devices/dev-1/desktop", Headers: cookie, Type: "application/json", Body: &body}, badGuest)
}

// TestALinkReachesTheDevicesOfItsListOnly: a link whose list of devices
// is no list reaches no device, also where a text holds the device's id
// (BUGS.md GA2); the old console stopped at a list that was neither a
// list nor a text.
func TestALinkReachesTheDevicesOfItsListOnly(t *testing.T) {
	w := newWorld(t)
	body := "{}"
	for _, grant := range []string{
		`{"valid":true,"peer_ids":"xx dev-1 yy"}`,
		`{"valid":true,"peer_ids":"dev-1"}`,
		`{"valid":true,"peer_ids":5}`,
		`{"valid":true,"peer_ids":{"0":"dev-1"}}`,
		`{"valid":true,"allowed_peer_ids":true}`,
	} {
		req := proxytest.Request{Method: "POST", Path: "/api/guest/mesh/devices/dev-1/desktop?guest=tok", Type: "application/json", Body: &body}
		closed, rec := aborted(t, w, req, &proxytest.Answer{Status: 200, Type: "application/json", Body: grant})
		if closed || rec.Code != 403 || rec.Body.String() != `{"error":"Invalid or expired guest link"}` {
			t.Errorf("a link %s: closed %v", grant, closed)
			if !closed {
				t.Errorf("answered %d %s", rec.Code, rec.Body)
			}
		}
	}
	rec, _ := w.Do(proxytest.Request{Method: "POST", Path: "/api/guest/mesh/devices/dev-1/desktop?guest=tok", Type: "application/json", Body: &body},
		&proxytest.Answer{Status: 200, Type: "application/json", Body: `{"valid":true,"peer_ids":["dev-1"]}`})
	if rec.Code != 200 {
		t.Errorf("a link with the device in its list: %d %s", rec.Code, rec.Body)
	}
}

// TestDesktopSendsTheCheckedTokenAsGuest: the desktop route passes the
// token it checked on as guest, also where the query has an empty or
// blank guest beside it, so the API server checks the link again and
// writes the tunnel to the link's creator (BUGS.md MC3).
func TestDesktopSendsTheCheckedTokenAsGuest(t *testing.T) {
	w := newWorld(t)
	body := "{}"
	cases := []struct {
		query, cookie, grant, sent string
	}{
		{"guest=&t=tok", "", `{"valid":true,"peer_ids":["dev-1"]}`, "guest=tok"},
		{"guest=%20&x=1", "bd.guest=tok", `{"valid":true,"peer_ids":["dev-1"]}`, "guest=tok&x=1"},
		{"t=tok&view_only=0", "", `{"valid":true,"peer_ids":["dev-1"],"view_only":true}`, "view_only=1&guest=tok"},
		{"guest=%20tok%20", "", `{"valid":true,"peer_ids":["dev-1"]}`, "guest=tok"},
	}
	for _, c := range cases {
		req := proxytest.Request{Method: "POST", Path: "/api/guest/mesh/devices/dev-1/desktop?" + c.query, Type: "application/json", Body: &body}
		if c.cookie != "" {
			req.Headers = map[string]string{"Cookie": c.cookie}
		}
		rec, calls := w.Do(req, &proxytest.Answer{Status: 200, Type: "application/json", Body: c.grant})
		if rec.Code != 200 || len(calls) != 2 {
			t.Errorf("?%s: %d %s, %d calls", c.query, rec.Code, rec.Body, len(calls))
			continue
		}
		if got, want := calls[1].URL, "/api/mesh/devices/dev-1/desktop?"+c.sent; got != want {
			t.Errorf("?%s: the API server got %s, want %s", c.query, got, want)
		}
	}
}

// TestCreateChecksTheTypes: the create route checks ttl_minutes, label
// and max_uses itself and answers 400 in its own words, where the API
// server answered "invalid json" (BUGS.md GA4). Good values go on as they
// came.
func TestCreateChecksTheTypes(t *testing.T) {
	w := newWorld(t)
	admin := &proxytest.Account{UserID: 1}
	admin.User.Username, admin.User.Role = "e2e_admin", "admin"
	created := &proxytest.Answer{Status: 201, Type: "application/json", Body: `{"id":"g1"}`}
	post := func(body string) (*httptest.ResponseRecorder, []proxytest.Call) {
		return w.Do(proxytest.Request{Method: "POST", Path: "/api/guest/access-links", Session: admin, Type: "application/json", Body: &body}, created)
	}
	refused := map[string]string{
		`"ttl_minutes":"soon"`:     "Invalid ttl_minutes",
		`"ttl_minutes":"30"`:       "Invalid ttl_minutes",
		`"ttl_minutes":0`:          "Invalid ttl_minutes",
		`"ttl_minutes":-5`:         "Invalid ttl_minutes",
		`"ttl_minutes":2.5`:        "Invalid ttl_minutes",
		`"ttl_minutes":1e300`:      "Invalid ttl_minutes",
		`"ttl_minutes":true`:       "Invalid ttl_minutes",
		`"ttl_minutes":[5]`:        "Invalid ttl_minutes",
		`"label":7`:                "Invalid label",
		`"label":true`:             "Invalid label",
		`"label":false`:            "Invalid label",
		`"label":{"a":1}`:          "Invalid label",
		`"label":["x"]`:            "Invalid label",
		`"max_uses":"2"`:           "Invalid max_uses",
		`"max_uses":-1`:            "Invalid max_uses",
		`"max_uses":1.5`:           "Invalid max_uses",
		`"max_uses":false`:         "Invalid max_uses",
		`"max_uses":{}`:            "Invalid max_uses",
		`"label":7,"max_uses":"2"`: "Invalid label",
	}
	for fields, message := range refused {
		rec, calls := post(`{"peer_ids":["dev-1"],` + fields + `}`)
		if want := `{"error":"` + message + `"}`; rec.Code != 400 || rec.Body.String() != want || len(calls) != 0 {
			t.Errorf("%s: %d %s, %d calls; want 400 %s", fields, rec.Code, rec.Body, len(calls), want)
		}
	}
	sent := map[string]string{
		``:                                    `{"peer_ids":["dev-1"],"view_only":false,"label":"","max_uses":0}`,
		`,"ttl_minutes":30`:                   `{"peer_ids":["dev-1"],"ttl_minutes":30,"view_only":false,"label":"","max_uses":0}`,
		`,"ttl_minutes":null`:                 `{"peer_ids":["dev-1"],"ttl_minutes":null,"view_only":false,"label":"","max_uses":0}`,
		`,"label":"Desk","max_uses":3`:        `{"peer_ids":["dev-1"],"view_only":false,"label":"Desk","max_uses":3}`,
		`,"label":null,"max_uses":null`:       `{"peer_ids":["dev-1"],"view_only":false,"label":"","max_uses":0}`,
		`,"label":"","max_uses":0`:            `{"peer_ids":["dev-1"],"view_only":false,"label":"","max_uses":0}`,
		`,"ttl_minutes":1440,"view_only":"x"`: `{"peer_ids":["dev-1"],"ttl_minutes":1440,"view_only":true,"label":"","max_uses":0}`,
	}
	for fields, want := range sent {
		rec, calls := post(`{"peer_ids":["dev-1"]` + fields + `}`)
		if rec.Code != 201 || len(calls) != 1 || calls[0].Body != want {
			t.Errorf("%q: %d %s, calls %+v; want the API server to get %s", fields, rec.Code, rec.Body, calls, want)
		}
	}
}

// TestDeviceScopeOfTheAccount runs the create route on the real device
// scope of package access: an operator under a restricted default reaches
// nothing, a device granted to the account, and an admin everything.
func TestDeviceScopeOfTheAccount(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	if _, err := db.Exec(ctx, "INSERT INTO users (id, username, password_hash, role) VALUES (5, 'e2e_operator', 'x', 'operator')"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO user_peer_grants (user_id, peer_id) VALUES (5, 'dev-1')"); err != nil {
		t.Fatal(err)
	}
	scopes := access.New(db, access.NewDBRoleSource(nil), access.Options{DeviceScopeDefault: "restricted"})
	w := &world{}
	w.World = proxytest.NewWorld(t, func(d *kit.Deps) {
		d.Access = scopes
		d.GuestAccess = guestaccess.New(guestaccess.Options{GoAPI: d.GoAPI})
		register(d, func(ctx context.Context, user *access.ScopeUser, devices []access.Device) (deviceScope, error) {
			return scopes.DeviceScopeForUser(ctx, user, devices)
		}, anyDevice)
	})
	created := &proxytest.Answer{Status: 201, Type: "application/json", Body: `{"id":"g1"}`}
	post := func(role string, id int64, body string) (int, string) {
		account := &proxytest.Account{UserID: id}
		account.User.Username, account.User.Role = "e2e_"+role, role
		rec, _ := w.Do(proxytest.Request{Method: "POST", Path: "/api/guest/access-links", Session: account, Type: "application/json", Body: &body}, created)
		return rec.Code, rec.Body.String()
	}
	if code, body := post("operator", 5, `{"peer_ids":["dev-1"]}`); code != 201 {
		t.Errorf("a granted device: %d %s", code, body)
	}
	if code, body := post("operator", 5, `{"peer_ids":["dev-1","dev-2"]}`); code != 403 || body != `{"error":"One or more devices are outside your device scope"}` {
		t.Errorf("a device outside the scope: %d %s", code, body)
	}
	if code, body := post("admin", 1, `{"peer_ids":["dev-2"]}`); code != 201 {
		t.Errorf("an admin: %d %s", code, body)
	}
}

// TestLinkRoutesAreRateLimited checks that the three routes of a link
// holder count against the limit of the remote pages before they look at
// anything else: with a limit of two, the third request is refused.
func TestLinkRoutesAreRateLimited(t *testing.T) {
	body := "{}"
	requests := []proxytest.Request{
		{Path: "/api/guest/access-links/validate"},
		{Path: "/api/guest/access-links/peers"},
		{Method: "POST", Path: "/api/guest/mesh/devices/dev-1/desktop", Type: "application/json", Body: &body},
	}
	for _, req := range requests {
		w := proxytest.NewWorld(t, func(d *kit.Deps) {
			d.Limits = limits.NewSet(limits.Options{
				ClientIP: httpx.SocketIP,
				UserKey:  func(*http.Request) string { return "" },
				Getenv: func(key string) string {
					if key == "RDCLIENT_PAGE_RATE_LIMIT_MAX" {
						return "2"
					}
					return ""
				},
			})
			d.GuestAccess = guestaccess.New(guestaccess.Options{GoAPI: d.GoAPI})
			register(d, func(context.Context, *access.ScopeUser, []access.Device) (deviceScope, error) {
				return unlimited{}, nil
			}, anyDevice)
		})
		for n := 1; n <= 3; n++ {
			rec, _ := w.Do(req, nil)
			if limited := rec.Code == http.StatusTooManyRequests; limited != (n == 3) {
				t.Errorf("%s request %d: status %d", req.Path, n, rec.Code)
			}
		}
	}
}
