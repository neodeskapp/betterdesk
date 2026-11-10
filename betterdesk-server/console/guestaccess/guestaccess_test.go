package guestaccess

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
)

func request(target, cookie string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "http://console.test"+target, nil)
	if cookie != "" {
		r.Header.Set("Cookie", cookie)
	}
	return r
}

func parse(t *testing.T, text string) any {
	t.Helper()
	value, err := goapi.Parse([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestPeerAllowedChecksTheList(t *testing.T) {
	cases := []struct {
		grant, peer string
		want        bool
	}{
		{`{"peer_ids":["A","B"]}`, "A", true},
		{`{"peer_ids":["A","B"]}`, "C", false},
		{`{"peer_ids":["A","B"]}`, "a", false},
		{`{"peer_ids":["A","B"]}`, "", false},
		{`null`, "A", false},
		{`{}`, "A", false},
		{`{"allowed_peer_ids":["A"]}`, "A", true},
		{`{"peer_ids":[],"allowed_peer_ids":["A"]}`, "A", false},
		{`{"peer_ids":null,"allowed_peer_ids":["A"]}`, "A", true},
		{`{"peer_ids":"","allowed_peer_ids":["A"]}`, "A", true},
		{`{"peer_ids":"xAy"}`, "A", true},
		{`{"peer_ids":"xy"}`, "A", false},
		{`{"peer_ids":[["A"],1,null]}`, "A", false},
		{`"text"`, "A", false},
	}
	for _, c := range cases {
		got, err := PeerAllowed(parse(t, c.grant), c.peer)
		if err != nil || got != c.want {
			t.Errorf("PeerAllowed(%s, %q) = %v, %v; want %v", c.grant, c.peer, got, err, c.want)
		}
	}
	for _, grant := range []string{`{"peer_ids":5}`, `{"peer_ids":{"0":"A"}}`, `{"peer_ids":true}`} {
		if _, err := PeerAllowed(parse(t, grant), "A"); !errors.Is(err, ErrBadGrant) {
			t.Errorf("PeerAllowed(%s): %v, want ErrBadGrant", grant, err)
		}
	}
}

func TestTokenComesFromTheQueryBeforeTheCookie(t *testing.T) {
	s := New(Options{})
	cases := []struct {
		target, cookie    string
		query, jar, token string
	}{
		{"/x?guest=abc", "", "abc", "", "abc"},
		{"/x?t=xyz", "", "xyz", "", "xyz"},
		{"/x?guest=abc&t=xyz", "", "abc", "", "abc"},
		{"/x?guest=&t=xyz", "", "xyz", "", "xyz"},
		{"/x", "bd.guest=cookieTok", "", "cookieTok", "cookieTok"},
		{"/x?guest=fromQuery", "bd.guest=fromCookie", "fromQuery", "fromCookie", "fromQuery"},
		{"/x?guest=%20%09padded%0A", "bd.guest=%20c%20", "padded", "c", "padded"},
		{"/x?guest=+", "bd.guest=c", "", "c", "c"},
		{"/x?guest=a&guest=b", "", "a,b", "", "a,b"},
		{"/x?guest[k]=a", "", "[object Object]", "", "[object Object]"},
		{"/x?other=1", "betterdesk.guest=c; other=2", "", "", ""},
		{"/x", `bd.guest=j:"json"`, "", "json", "json"},
		{"/x", `bd.guest=j:["a",null,"b"]`, "", "a,,b", "a,,b"},
		{"/x", `bd.guest=j:{"a":1}`, "", "[object Object]", "[object Object]"},
		{"/x", `bd.guest=j:0`, "", "j:0", "j:0"},
		{"/x", `bd.guest=j:{bad`, "", "j:{bad", "j:{bad"},
		{"/x", `bd.guest=`, "", "", ""},
	}
	for _, c := range cases {
		r := request(c.target, c.cookie)
		if got, err := s.TokenFromQuery(r); err != nil || got != c.query {
			t.Errorf("%s: token of the query %q, %v; want %q", c.target, got, err, c.query)
		}
		if got, err := s.TokenFromCookie(r); err != nil || got != c.jar {
			t.Errorf("%s with %q: token of the cookie %q, %v; want %q", c.target, c.cookie, got, err, c.jar)
		}
		if got, err := s.Token(r); err != nil || got != c.token {
			t.Errorf("%s with %q: token %q, %v; want %q", c.target, c.cookie, got, err, c.token)
		}
	}
}

func TestTokensWithoutAText(t *testing.T) {
	s := New(Options{})
	for _, target := range []string{"/x?guest[toString]=1", "/x?t[toString]=1", "/x?guest[][toString]=1"} {
		if _, err := s.Token(request(target, "bd.guest=fine")); !errors.Is(err, ErrNotText) {
			t.Errorf("%s: %v, want ErrNotText", target, err)
		}
	}
	if _, err := s.Token(request("/x", `bd.guest=j:{"toString":1}`)); !errors.Is(err, ErrNotText) {
		t.Errorf("a cookie without a text: %v", err)
	}
	// The query is read first: its token wins before the cookie is looked at
	if token, err := s.Token(request("/x?t=q", `bd.guest=j:{"toString":1}`)); err != nil || token != "q" {
		t.Errorf("query before a bad cookie: %q, %v", token, err)
	}
}

func TestCookieNameFollowsHTTPS(t *testing.T) {
	if got := New(Options{}).CookieName(); got != "bd.guest" {
		t.Errorf("plain: %q", got)
	}
	secure := New(Options{HTTPS: true})
	if got := secure.CookieName(); got != "betterdesk.guest" {
		t.Errorf("HTTPS: %q", got)
	}
	if token, _ := secure.Token(request("/x", "bd.guest=old; betterdesk.guest=new")); token != "new" {
		t.Errorf("HTTPS reads the cookie %q", token)
	}
}

func TestSetCookieLastsAsLongAsTheLink(t *testing.T) {
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name      string
		expiresAt any
		maxAge    string
		expires   string
	}{
		{"in an hour", "2026-06-01T13:00:00Z", "3600", "Mon, 01 Jun 2026 13:00:00 GMT"},
		{"in an hour and a bit, with a zone", "2026-06-01T15:00:00.999999999+02:00", "3600", "Mon, 01 Jun 2026 13:00:00 GMT"},
		{"in half a second", "2026-06-01T12:00:00.500Z", "0", "Mon, 01 Jun 2026 12:00:00 GMT"},
		{"in exactly a day", "2026-06-02T12:00:00Z", "86400", "Tue, 02 Jun 2026 12:00:00 GMT"},
		{"in a day less a second", "2026-06-02T11:59:59Z", "86399", "Tue, 02 Jun 2026 11:59:59 GMT"},
		{"in two days", "2026-06-03T12:00:00Z", "86400", "Tue, 02 Jun 2026 12:00:00 GMT"},
		{"in the past", "2026-06-01T11:00:00Z", "86400", "Tue, 02 Jun 2026 12:00:00 GMT"},
		{"right now", "2026-06-01T12:00:00Z", "86400", "Tue, 02 Jun 2026 12:00:00 GMT"},
		{"no time", "soon", "86400", "Tue, 02 Jun 2026 12:00:00 GMT"},
		{"nothing", nil, "86400", "Tue, 02 Jun 2026 12:00:00 GMT"},
		{"milliseconds", float64(now.Add(90*time.Second).UnixMilli()) + 0.7, "90", "Mon, 01 Jun 2026 12:01:30 GMT"},
		{"a plain date", "2026-06-02", "43200", "Tue, 02 Jun 2026 00:00:00 GMT"},
	}
	for _, c := range cases {
		s := New(Options{Now: func() time.Time { return now }})
		rec := httptest.NewRecorder()
		s.SetCookie(rec, "tok en", c.expiresAt)
		want := "bd.guest=tok%20en; Max-Age=" + c.maxAge + "; Path=/; Expires=" + c.expires + "; HttpOnly; SameSite=Lax"
		if got := rec.Header().Get("Set-Cookie"); got != want {
			t.Errorf("%s:\n   %s\nwant %s", c.name, got, want)
		}
	}
	rec := httptest.NewRecorder()
	New(Options{HTTPS: true, Now: func() time.Time { return now }}).SetCookie(rec, "t", nil)
	if got := rec.Header().Get("Set-Cookie"); got != "betterdesk.guest=t; Max-Age=86400; Path=/; Expires=Tue, 02 Jun 2026 12:00:00 GMT; HttpOnly; Secure; SameSite=Lax" {
		t.Errorf("HTTPS: %s", got)
	}
}

func TestClearCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	New(Options{}).ClearCookie(rec)
	if got := rec.Header().Get("Set-Cookie"); got != "bd.guest=; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT; HttpOnly; SameSite=Lax" {
		t.Errorf("plain: %s", got)
	}
	rec = httptest.NewRecorder()
	New(Options{HTTPS: true}).ClearCookie(rec)
	if got := rec.Header().Get("Set-Cookie"); got != "betterdesk.guest=; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT; HttpOnly; Secure; SameSite=Lax" {
		t.Errorf("HTTPS: %s", got)
	}
}

func TestHashToken(t *testing.T) {
	if got := HashToken("abc"); got != "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Errorf("HashToken(abc) = %s", got)
	}
}

func TestAttachGrant(t *testing.T) {
	var asked []string
	answer, status := `{"valid":true,"id":"g1","peer_ids":["dev-1"],"view_only":true}`, 200
	api := goapi.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Path+"?"+r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(answer))
	}))
	s := New(Options{GoAPI: api})
	ctx := context.Background()

	grant, err := s.AttachGrant(ctx, request("/x?t=%20tok%20", ""), "dev-1")
	if err != nil || grant == nil || grant.Token != "tok" || !grant.ViewOnly() || goapi.Dig(grant.Data, "id") != "g1" {
		t.Fatalf("a good link: %+v, %v", grant, err)
	}
	if len(asked) != 1 || asked[0] != "/api/guest/access-links/validate?token=tok&peer_id=dev-1" {
		t.Errorf("the API server was asked %v", asked)
	}
	asked = nil
	if _, err := s.AttachGrant(ctx, request("/x", "bd.guest=c"), ""); err != nil || len(asked) != 1 || asked[0] != "/api/guest/access-links/validate?token=c" {
		t.Errorf("without a device: asked %v, %v", asked, err)
	}

	asked = nil
	if grant, err := s.AttachGrant(ctx, request("/x", ""), "dev-1"); grant != nil || err != nil || len(asked) != 0 {
		t.Errorf("without a token: %v, %v, asked %v", grant, err, asked)
	}
	if _, err := s.AttachGrant(ctx, request("/x?guest[toString]=1", ""), "dev-1"); !errors.Is(err, ErrNotText) || len(asked) != 0 {
		t.Errorf("a token without a text: %v, asked %v", err, asked)
	}
	for _, bad := range []struct {
		body   string
		status int
	}{{`{"valid":false,"error":"expired"}`, 200}, {`{"valid":0}`, 200}, {`{}`, 200}, {`"text"`, 200}, {``, 200}, {`{"valid":true}`, 403}, {`{"valid":true}`, 500}} {
		answer, status = bad.body, bad.status
		if grant, err := s.AttachGrant(ctx, request("/x?guest=tok", ""), "dev-1"); grant != nil || err != nil {
			t.Errorf("API answer %d %s: %+v, %v", bad.status, bad.body, grant, err)
		}
	}
	var none *Grant
	if none.ViewOnly() {
		t.Error("no link is view-only")
	}
}

func TestRevokeTellsEveryListener(t *testing.T) {
	s := New(Options{})
	if got := s.Revoke("g1"); got != 0 {
		t.Errorf("without listeners: %d", got)
	}
	var heard []string
	s.OnRevoke(func(id string) int { heard = append(heard, "a:"+id); return 2 })
	s.OnRevoke(func(id string) int { heard = append(heard, "b:"+id); return 3 })
	if got := s.Revoke("g1"); got != 5 || strings.Join(heard, " ") != "a:g1 b:g1" {
		t.Errorf("ended %d, heard %v", got, heard)
	}
}

func TestJSText(t *testing.T) {
	cases := map[string]string{
		`"text"`: "text", `5`: "5", `1.5`: "1.5", `-0`: "0", `1e21`: "1e+21", `1e-7`: "1e-7", `true`: "true", `false`: "false", `null`: "null",
		`[]`: "", `[1,[2,3],null,"a"]`: "1,2,3,,a", `{}`: "[object Object]", `{"a":1}`: "[object Object]", `[{"a":1}]`: "[object Object]",
	}
	for input, want := range cases {
		if got, ok := JSText(parse(t, input)); !ok || got != want {
			t.Errorf("JSText(%s) = %q, %v; want %q", input, got, ok, want)
		}
	}
	for _, input := range []string{`{"toString":1}`, `[1,{"toString":null}]`, `[[{"toString":"x"}]]`} {
		if got, ok := JSText(parse(t, input)); ok {
			t.Errorf("JSText(%s) = %q, want no text", input, got)
		}
	}
}

// FuzzToken reads the token of arbitrary queries and cookies. Whatever
// comes, the token has no white space around it and the call returns.
func FuzzToken(f *testing.F) {
	f.Add("guest=tok", "bd.guest=c")
	f.Add("t=%20x&guest[]=a", `bd.guest=j:["a"]`)
	f.Add("guest[toString]=1", `bd.guest=j:{"toString":1}`)
	f.Add("%zz=1&t=%E4", "bd.guest=%zz; x")
	s := New(Options{})
	f.Fuzz(func(t *testing.T, query, cookie string) {
		if strings.ContainsAny(query, " \r\n#\x00") || strings.ContainsAny(cookie, "\r\n\x00") {
			return
		}
		r := httptest.NewRequest(http.MethodGet, "http://console.test/x", nil)
		r.URL.RawQuery = query
		r.Header.Set("Cookie", cookie)
		token, err := s.Token(r)
		if err != nil {
			if !errors.Is(err, ErrNotText) || token != "" {
				t.Fatalf("Token(%q, %q) = %q, %v", query, cookie, token, err)
			}
			return
		}
		if token != strings.TrimFunc(token, isJSSpace) {
			t.Fatalf("Token(%q, %q) = %q, with white space around it", query, cookie, token)
		}
	})
}
