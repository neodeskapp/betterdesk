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
		})
	})
	return w
}

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

// TestTokensWithoutATextCloseTheConnection covers the requests the old
// console stopped at (BUGS.md MC1), which no vector can hold: the Go
// console closes the connection and stays up.
func TestTokensWithoutATextCloseTheConnection(t *testing.T) {
	w := newWorld(t)
	grant := &proxytest.Answer{Status: 200, Type: "application/json", Body: `{"valid":true,"peer_ids":["dev-1"]}`}
	body := "{}"
	for _, target := range []string{
		"/api/guest/access-links/peers?guest[toString]=x",
		"/api/guest/access-links/peers?t[toString]=x",
		"/api/guest/access-links/peers?guest[][toString]=x",
	} {
		if closed, rec := aborted(t, w, proxytest.Request{Path: target}, grant); !closed {
			t.Errorf("GET %s was answered with %d %s", target, rec.Code, rec.Body)
		}
	}
	for _, query := range []string{"guest[toString]=x", "t[toString]=x", "mesh_share[toString]=x", "guest=tok&mesh_share[toString]=x"} {
		req := proxytest.Request{Method: "POST", Path: "/api/guest/mesh/devices/dev-1/desktop?" + query, Type: "application/json", Body: &body}
		if closed, rec := aborted(t, w, req, grant); !closed {
			t.Errorf("POST with ?%s was answered with %d %s", query, rec.Code, rec.Body)
		}
	}
	cookie := proxytest.Request{Path: "/api/guest/access-links/peers", Headers: map[string]string{"Cookie": `bd.guest=j:{"toString":1}`}}
	if closed, rec := aborted(t, w, cookie, grant); !closed {
		t.Errorf("a cookie without a text was answered with %d %s", rec.Code, rec.Body)
	}
	// A link whose list of devices is neither a list nor a text
	for _, bad := range []string{`{"valid":true,"peer_ids":5}`, `{"valid":true,"peer_ids":{"0":"dev-1"}}`, `{"valid":true,"allowed_peer_ids":true}`} {
		req := proxytest.Request{Method: "POST", Path: "/api/guest/mesh/devices/dev-1/desktop?guest=tok", Type: "application/json", Body: &body}
		if closed, rec := aborted(t, w, req, &proxytest.Answer{Status: 200, Type: "application/json", Body: bad}); !closed {
			t.Errorf("a link with %s was answered with %d %s", bad, rec.Code, rec.Body)
		}
	}
	// The console is still there
	if closed, rec := aborted(t, w, proxytest.Request{Path: "/api/guest/access-links/peers?t=tok"}, grant); closed || rec.Code != 200 {
		t.Errorf("a plain request afterwards: closed %v", closed)
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
		Register(d)
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
			})
		})
		for n := 1; n <= 3; n++ {
			rec, _ := w.Do(req, nil)
			if limited := rec.Code == http.StatusTooManyRequests; limited != (n == 3) {
				t.Errorf("%s request %d: status %d", req.Path, n, rec.Code)
			}
		}
	}
}
