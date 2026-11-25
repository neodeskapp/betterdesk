package session

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

func write(key string, value any) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		From(r).Set(key, value)
		w.WriteHeader(http.StatusOK)
	}
}

// The rows of the table "what was seen" in the spec, section 1.5.

func TestNoCookieSessionUntouched(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		var seen *Session
		rec := f.serve(get("/"), func(w http.ResponseWriter, r *http.Request) {
			seen = From(r)
			w.WriteHeader(http.StatusOK)
		})
		if seen == nil || seen.UserID() != 0 || seen.User() != nil || len(seen.ID()) != 32 {
			t.Fatalf("a visitor has an empty session, got %+v", seen)
		}
		if got := setCookies(rec); len(got) != 0 {
			t.Fatalf("Set-Cookie %q, want none", got)
		}
		if n := f.rowCount(); n != 0 {
			t.Fatalf("%d rows, want none", n)
		}
	})
}

func TestNoCookieSessionWritten(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		rec := f.serve(get("/"), write("x", 1))
		pair := f.sessionCookie(rec)
		if pair == "" {
			t.Fatalf("no session cookie in %q", setCookies(rec))
		}
		sid := f.sidOf(pair)
		row := f.row(sid)
		if row == nil || !row.active || row.userID.Valid {
			t.Fatalf("row: %+v", row)
		}
		now := f.clock.Now()
		wantJSON := fmt.Sprintf(`{"cookie":{"originalMaxAge":1800000,"expires":"%s","secure":false,"httpOnly":true,"path":"/","sameSite":"lax"},"x":1}`,
			httpx.ISOTime(now.Add(30*time.Minute)))
		if row.json != wantJSON {
			t.Fatalf("session_json:\n got %s\nwant %s", row.json, wantJSON)
		}
		if row.expiresAt != now.Add(30*time.Minute).UnixMilli() || row.createdAt.Int64 != now.UnixMilli() || row.lastActivityAt.Int64 != now.UnixMilli() {
			t.Fatalf("row times: %+v", row)
		}
	})
}

func TestValidCookieUnchanged(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		pair := f.sessionCookie(f.serve(get("/"), write("x", 1)))
		sid := f.sidOf(pair)
		before := f.row(sid)

		f.clock.Advance(10 * time.Second)
		var x int
		rec := f.serve(get("/static/app.css", pair), func(w http.ResponseWriter, r *http.Request) {
			From(r).Get("x", &x)
			http.NotFound(w, r)
		})
		if x != 1 {
			t.Fatalf("x = %d, want the stored 1", x)
		}
		if got := f.sessionCookie(rec); got != pair {
			t.Fatalf("cookie %q, want the same id again (%q), also on a 404", got, pair)
		}
		after := f.row(sid)
		if after.json != before.json {
			t.Fatalf("an unchanged session was rewritten:\n was %s\n now %s", before.json, after.json)
		}
		if want := before.expiresAt + 10_000; after.expiresAt != want {
			t.Fatalf("expires_at moved by %d ms, want 10000", after.expiresAt-before.expiresAt)
		}
		// A session without its own last activity counts the request
		if after.lastActivityAt.Int64 != f.clock.Now().UnixMilli() {
			t.Fatalf("last_activity_at = %d, want the time of the request", after.lastActivityAt.Int64)
		}
		wantExpires := f.clock.Now().Add(30 * time.Minute).UTC().Format(http.TimeFormat)
		if c := setCookies(rec)[0]; !strings.Contains(c, "; Expires="+wantExpires+";") {
			t.Fatalf("cookie %q, want Expires=%s", c, wantExpires)
		}
	})
}

func TestValidCookieWithoutURLEncoding(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		pair := f.sessionCookie(f.serve(get("/"), write("x", 1)))
		sid := f.sidOf(pair)
		raw := f.m.cookieName + "=" + cookiePrefix + sign(sid, testSecret)
		if raw == pair {
			t.Fatal("the raw and the encoded form are the same, the test shows nothing")
		}
		var x int
		rec := f.serve(get("/", raw), func(w http.ResponseWriter, r *http.Request) {
			From(r).Get("x", &x)
			w.WriteHeader(http.StatusOK)
		})
		if x != 1 || f.sessionCookie(rec) != pair {
			t.Fatalf("x = %d, cookie %q; want the session and its encoded cookie", x, f.sessionCookie(rec))
		}
	})
}

func forged(f *fixture, sid string) string {
	return f.m.cookieName + "=" + httpx.EncodeURIComponent(cookiePrefix+sign(sid, "another-secret"))
}

func TestBadSignatureSessionUntouched(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		victim := f.sidOf(f.signIn(alice))
		var userID int64 = -1
		rec := f.serve(get("/", forged(f, victim)), func(w http.ResponseWriter, r *http.Request) {
			userID = From(r).UserID()
			w.WriteHeader(http.StatusOK)
		})
		if userID != 0 {
			t.Fatalf("a cookie signed with another secret signed user %d in", userID)
		}
		if got := setCookies(rec); len(got) != 0 {
			t.Fatalf("Set-Cookie %q, want none: nothing clears a bad cookie", got)
		}
		if n := f.rowCount(); n != 1 {
			t.Fatalf("%d rows, want only the victim's", n)
		}
	})
}

func TestBadSignatureSessionWritten(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		rec := f.serve(get("/", forged(f, "forged-session-id")), write("x", 1))
		pair := f.sessionCookie(rec)
		if pair == "" {
			t.Fatal("no cookie for the new session")
		}
		if sid := f.sidOf(pair); sid == "forged-session-id" || f.row(sid) == nil {
			t.Fatalf("new session id %q; want a fresh one with a row", sid)
		}
		if f.row("forged-session-id") != nil {
			t.Fatal("a row under the forged id")
		}
	})
}

func TestUnknownIDSessionUntouched(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		rec := f.serve(get("/", f.cookieFor("SID00000000000000000000000000002")), nil)
		if got := setCookies(rec); len(got) != 0 {
			t.Fatalf("Set-Cookie %q, want none", got)
		}
		if n := f.rowCount(); n != 0 {
			t.Fatalf("%d rows, want none", n)
		}
	})
}

func TestUnknownIDSessionWritten(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		const unknown = "SID00000000000000000000000000002"
		rec := f.serve(get("/", f.cookieFor(unknown)), write("x", 1))
		pair := f.sessionCookie(rec)
		if pair == "" || f.sidOf(pair) == unknown {
			t.Fatalf("cookie %q, want a new id: a well-signed id without a row is not adopted", pair)
		}
		if f.row(unknown) != nil || f.row(f.sidOf(pair)) == nil {
			t.Fatal("want a row under the new id only")
		}
	})
}

func TestCookieWithoutPrefix(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		sid := f.sidOf(f.signIn(alice))
		for _, value := range []string{sign(sid, testSecret), "S:" + sign(sid, testSecret), "j:" + sign(sid, testSecret), sid, ""} {
			var userID int64 = -1
			rec := f.serve(get("/", f.m.cookieName+"="+httpx.EncodeURIComponent(value)), func(w http.ResponseWriter, r *http.Request) {
				userID = From(r).UserID()
				w.WriteHeader(http.StatusOK)
			})
			if userID != 0 || len(setCookies(rec)) != 0 {
				t.Errorf("value %q: user %d, Set-Cookie %q; want signed out and no cookie", value, userID, setCookies(rec))
			}
		}
	})
}

func TestTwoCookiesFirstInvalid(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		good := f.signIn(alice)
		var userID int64 = -1
		handler := func(w http.ResponseWriter, r *http.Request) {
			userID = From(r).UserID()
			w.WriteHeader(http.StatusOK)
		}
		rec := f.serve(get("/", f.m.cookieName+"=garbage; "+good), handler)
		if userID != 0 || len(setCookies(rec)) != 0 {
			t.Fatalf("user %d, Set-Cookie %q; the second cookie of the name is never looked at", userID, setCookies(rec))
		}
		// The other way round the first, good one counts
		f.serve(get("/", good+"; "+f.m.cookieName+"=garbage"), handler)
		if userID != alice.ID {
			t.Fatalf("user %d, want %d", userID, alice.ID)
		}
		// Also over two Cookie headers
		f.serve(get("/", f.m.cookieName+"=garbage", good), handler)
		if userID != 0 {
			t.Fatalf("user %d over two headers, want 0", userID)
		}
	})
}

func TestRegenerateThenWrite(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		oldPair := f.sessionCookie(f.serve(get("/"), write("pending", "yes")))
		oldSID := f.sidOf(oldPair)
		rec := f.serve(get("/", oldPair), func(w http.ResponseWriter, r *http.Request) {
			s := From(r)
			if err := s.Regenerate(); err != nil {
				t.Errorf("Regenerate: %v", err)
			}
			var pending string
			if s.Get("pending", &pending) {
				t.Errorf("the new session carries the old field %q", pending)
			}
			s.Set("y", 2)
			w.WriteHeader(http.StatusOK)
		})
		newPair := f.sessionCookie(rec)
		if newPair == "" || newPair == oldPair {
			t.Fatalf("cookie %q, want a new id", newPair)
		}
		old := f.row(oldSID)
		if old.active || old.reason.String != ReasonDestroyed || old.json != "{}" {
			t.Fatalf("old row: %+v", old)
		}
		fresh := f.row(f.sidOf(newPair))
		if fresh == nil || !fresh.active || !strings.HasSuffix(fresh.json, `,"y":2}`) {
			t.Fatalf("new row: %+v", fresh)
		}
	})
}

func TestRegenerateWithoutWrite(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		rec := f.serve(get("/"), func(w http.ResponseWriter, r *http.Request) {
			if err := From(r).Regenerate(); err != nil {
				t.Errorf("Regenerate: %v", err)
			}
			w.WriteHeader(http.StatusOK)
		})
		pair := f.sessionCookie(rec)
		if pair == "" || f.row(f.sidOf(pair)) == nil {
			t.Fatalf("cookie %q; a regenerated session is stored and sent also when empty", pair)
		}
	})
}

func TestDestroyAndClearCookies(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		pair := f.signIn(alice)
		sid := f.sidOf(pair)
		rec := f.serve(get("/logout", pair), func(w http.ResponseWriter, r *http.Request) {
			s := From(r)
			s.End(ReasonLogout)
			if err := s.Destroy(); err != nil {
				t.Errorf("Destroy: %v", err)
			}
			if s.UserID() != 0 || s.User() != nil {
				t.Error("a destroyed session still has its user")
			}
			s.Set("late", 1)
			s.ClearCookies(w)
			w.WriteHeader(http.StatusOK)
		})
		want := []string{
			"neodesk.sid=; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
			"nd.sid=; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
		}
		if got := setCookies(rec); strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("Set-Cookie:\n got %q\nwant %q", got, want)
		}
		row := f.row(sid)
		if row.active || row.reason.String != ReasonLogout || row.json != "{}" || !row.endedAt.Valid {
			t.Fatalf("row after sign-out: %+v", row)
		}
		var userID int64 = -1
		f.serve(get("/", pair), func(w http.ResponseWriter, r *http.Request) { userID = From(r).UserID() })
		if userID != 0 {
			t.Fatalf("the old cookie still signs user %d in", userID)
		}
	})
}

func TestClearCookies(t *testing.T) {
	f := newFixture(t, storetest.SQLite(t))
	rec := f.serve(get("/logout"), func(w http.ResponseWriter, r *http.Request) {
		From(r).ClearCookies(w)
		w.WriteHeader(http.StatusFound)
	})
	want := []string{
		"neodesk.sid=; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
		"nd.sid=; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
	}
	if got := setCookies(rec); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("Set-Cookie:\n got %q\nwant %q", got, want)
	}
}

func httpsMode(o *Options) {
	o.HTTPS, o.Secure = true, true
	trust, _ := httpx.ParseTrustProxy("loopback")
	o.TrustProxy = trust
}

// In HTTPS mode without a usable certificate (Secure off) the cookie keeps
// its HTTPS name; it is Secure on a request that came over HTTPS through a
// trusted proxy, and not on one over plain HTTP, which then keeps its
// session (BUGS LS3).
func TestFallbackCookieIsSecureOnlyOverHTTPS(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db, func(o *Options) {
			httpsMode(o)
			o.Secure = false
		})
		proxied := get("/")
		proxied.RemoteAddr = "127.0.0.1:50000"
		proxied.Header.Set("X-Forwarded-Proto", "https")
		for _, c := range []struct {
			name   string
			r      *http.Request
			secure bool
		}{{"plain", get("/"), false}, {"through a proxy over HTTPS", proxied, true}} {
			cookies := setCookies(f.serve(c.r, write("x", 1)))
			if len(cookies) != 1 || !strings.HasPrefix(cookies[0], "neodesk.sid=") || strings.HasSuffix(cookies[0], "; HttpOnly; Secure; SameSite=Lax") != c.secure {
				t.Errorf("%s: Set-Cookie %q, Secure %v", c.name, cookies, c.secure)
			}
		}
	})
}

// Without HTTPS mode, behind a TLS reverse proxy the console trusts, the
// cookie keeps its HTTP name and is Secure; a forwarded proto from a peer
// that is not trusted, or a plain request, gets no Secure (L89).
func TestHTTPModeCookieIsSecureBehindATrustedTLSProxy(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db, func(o *Options) {
			o.TrustProxy, _ = httpx.ParseTrustProxy("loopback")
		})
		proxied := func() *http.Request {
			r := get("/")
			r.RemoteAddr = "127.0.0.1:50000"
			r.Header.Set("X-Forwarded-Proto", "https")
			return r
		}
		untrusted := func() *http.Request {
			r := get("/")
			r.Header.Set("X-Forwarded-Proto", "https")
			return r
		}
		plain := func() *http.Request { return get("/") }
		for _, c := range []struct {
			name   string
			r      func() *http.Request
			secure bool
		}{{"plain", plain, false}, {"untrusted peer", untrusted, false}, {"through a trusted proxy over HTTPS", proxied, true}} {
			if got := f.m.SecureCookie(c.r()); got != c.secure {
				t.Errorf("%s: SecureCookie %v, want %v", c.name, got, c.secure)
			}
			cookies := setCookies(f.serve(c.r(), write("x", 1)))
			if len(cookies) != 1 || !strings.HasPrefix(cookies[0], "nd.sid=") || strings.HasSuffix(cookies[0], "; HttpOnly; Secure; SameSite=Lax") != c.secure {
				t.Errorf("%s: Set-Cookie %q, want Secure %v", c.name, cookies, c.secure)
			}
		}
	})
}

func TestSecureCookieOverPlainHTTP(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db, httpsMode)
		var sid string
		rec := f.serve(get("/"), func(w http.ResponseWriter, r *http.Request) {
			sid = From(r).ID()
			From(r).Set("x", 1)
			w.WriteHeader(http.StatusOK)
		})
		if got := setCookies(rec); len(got) != 0 {
			t.Fatalf("Set-Cookie %q over plain HTTP, want none for a Secure cookie", got)
		}
		row := f.row(sid)
		if row == nil || !strings.Contains(row.json, `"secure":true`) {
			t.Fatalf("row %+v; the session is stored all the same", row)
		}
		// An untrusted peer's X-Forwarded-Proto does not count
		r := get("/")
		r.Header.Set("X-Forwarded-Proto", "https")
		if got := setCookies(f.serve(r, write("x", 1))); len(got) != 0 {
			t.Fatalf("Set-Cookie %q for a forwarded proto from an untrusted peer", got)
		}
	})
}

func TestSecureCookieOverHTTPS(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db, httpsMode)
		behindProxy := func(r *http.Request) *http.Request {
			r.RemoteAddr = "127.0.0.1:50000"
			r.Header.Set("X-Forwarded-Proto", "https")
			r.Header.Set("X-Forwarded-For", "198.51.100.7")
			return r
		}
		rec := f.serve(behindProxy(get("/")), write("x", 1))
		cookies := setCookies(rec)
		if len(cookies) != 1 {
			t.Fatalf("Set-Cookie %q, want one", cookies)
		}
		wantTail := "; Path=/; Expires=" + f.clock.Now().Add(30*time.Minute).UTC().Format(http.TimeFormat) + "; HttpOnly; Secure; SameSite=Lax"
		if !strings.HasPrefix(cookies[0], "neodesk.sid=s%3A") || !strings.HasSuffix(cookies[0], wantTail) {
			t.Fatalf("cookie %q, want neodesk.sid=s%%3A...%s", cookies[0], wantTail)
		}
		// The HTTP name is not read in HTTPS mode
		pair, _, _ := strings.Cut(cookies[0], ";")
		var x int
		f.serve(behindProxy(get("/", strings.Replace(pair, "neodesk.sid=", "nd.sid=", 1))), func(w http.ResponseWriter, r *http.Request) {
			From(r).Get("x", &x)
		})
		if x != 0 {
			t.Fatal("a session was read from nd.sid in HTTPS mode")
		}
	})
}

// TestSetCookieText checks the header byte for byte against the spec's
// vectors: a stored session under a known id, at a known time.
func TestSetCookieText(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		for i, v := range signVectors {
			f := newFixture(t, db)
			f.clock.t = time.Date(2026, 11, 10, 1, 3, 6, 250e6, time.UTC)
			now := f.clock.Now().UnixMilli()
			f.mustSet(v.sid, record{
				json:      fmt.Sprintf(`{"cookie":{"originalMaxAge":1800000,"expires":"2026-11-10T01:00:00.000Z","secure":false,"httpOnly":true,"path":"/","sameSite":"lax"},"n":%d}`, i),
				expiresAt: now + 1000, createdAt: now, lastActivityAt: now,
			})
			rec := f.serve(get("/", "nd.sid="+v.wire), nil)
			want := "nd.sid=" + v.wire + "; Path=/; Expires=Tue, 10 Nov 2026 01:33:06 GMT; HttpOnly; SameSite=Lax"
			if got := setCookies(rec); len(got) != 1 || got[0] != want {
				t.Fatalf("Set-Cookie:\n got %q\nwant %q", got, want)
			}
			// The raw, unencoded value is accepted too
			if got := setCookies(f.serve(get("/", "nd.sid="+v.signed), nil)); len(got) != 1 || got[0] != want {
				t.Fatalf("Set-Cookie for the raw value:\n got %q\nwant %q", got, want)
			}
		}
	})
}

func TestCookieAttributesComeFromTheStoredSession(t *testing.T) {
	f := newFixture(t, storetest.SQLite(t))
	f.clock.t = time.Date(2026, 11, 10, 1, 3, 6, 0, time.UTC)
	now := f.clock.Now().UnixMilli()
	sid := signVectors[0].sid
	cases := []struct{ cookie, wantTail string }{
		{`{"originalMaxAge":60000,"expires":null,"secure":false,"httpOnly":false,"path":"/app","sameSite":"strict"}`, "; Path=/app; Expires=Tue, 10 Nov 2026 01:04:06 GMT; SameSite=Strict"},
		{`{"originalMaxAge":null,"expires":"2026-11-10T01:00:00.000Z"}`, "; Path=/; HttpOnly"},
		{`{"originalMaxAge":60000,"sameSite":true}`, "; Path=/; Expires=Tue, 10 Nov 2026 01:04:06 GMT; HttpOnly; SameSite=Strict"},
		{`{"originalMaxAge":60000,"sameSite":"none"}`, "; Path=/; Expires=Tue, 10 Nov 2026 01:04:06 GMT; HttpOnly; SameSite=None"},
		{`{}`, "; Path=/; HttpOnly"},
	}
	for _, tc := range cases {
		f.exec(`DELETE FROM panel_sessions`)
		f.mustSet(sid, record{json: `{"cookie":` + tc.cookie + `}`, expiresAt: now + 60_000, createdAt: now, lastActivityAt: now})
		got := setCookies(f.serve(get("/", "nd.sid="+signVectors[0].wire), nil))
		if want := "nd.sid=" + signVectors[0].wire + tc.wantTail; len(got) != 1 || got[0] != want {
			t.Errorf("cookie %s:\n got %q\nwant %q", tc.cookie, got, want)
		}
	}
}

// TestStoreIsWrittenBeforeTheFirstByte looks at the row from inside the
// connection's WriteHeader: the session must be there by then.
func TestStoreIsWrittenBeforeTheFirstByte(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		for name, respond := range map[string]func(w http.ResponseWriter){
			"WriteHeader": func(w http.ResponseWriter) { w.WriteHeader(http.StatusCreated) },
			"Write":       func(w http.ResponseWriter) { _, _ = w.Write([]byte("body")) },
			"Flush":       func(w http.ResponseWriter) { w.(http.Flusher).Flush() },
			"ReadFrom":    func(w http.ResponseWriter) { _, _ = w.(io.ReaderFrom).ReadFrom(strings.NewReader("file")) },
			"nothing":     func(http.ResponseWriter) {},
		} {
			spy := &spyWriter{ResponseRecorder: httptest.NewRecorder()}
			var sid string
			spy.onHeader = func() {
				spy.rowThere = f.row(sid) != nil
				spy.cookieThere = len(spy.Header().Values("Set-Cookie")) == 1
			}
			f.m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sid = From(r).ID()
				From(r).Set("x", 1)
				respond(w)
			})).ServeHTTP(spy, get("/"))
			if name == "nothing" {
				// The connection writes the header after the handlers; the
				// cookie and the row must be ready when they return
				spy.onHeader()
			}
			if !spy.rowThere || !spy.cookieThere {
				t.Errorf("%s: row stored %v, cookie set %v when the header went out", name, spy.rowThere, spy.cookieThere)
			}
		}
	})
}

type spyWriter struct {
	*httptest.ResponseRecorder
	onHeader              func()
	seen                  bool
	rowThere, cookieThere bool
}

func (w *spyWriter) first() {
	if !w.seen {
		w.seen = true
		w.onHeader()
	}
}

func (w *spyWriter) WriteHeader(status int) {
	if status >= 100 && status < 200 {
		return
	}
	w.first()
	w.ResponseRecorder.WriteHeader(status)
}

func (w *spyWriter) Write(p []byte) (int, error) {
	w.first()
	return w.ResponseRecorder.Write(p)
}

func (w *spyWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *spyWriter) Flush() {
	w.first()
	w.ResponseRecorder.Flush()
}

func TestInterimAnswerDoesNotCommit(t *testing.T) {
	f := newFixture(t, storetest.SQLite(t))
	spy := &spyWriter{ResponseRecorder: httptest.NewRecorder()}
	spy.onHeader = func() { spy.cookieThere = len(spy.Header().Values("Set-Cookie")) == 1 }
	f.m.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusEarlyHints)
		From(r).Set("x", 1)
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(spy, get("/"))
	if !spy.cookieThere {
		t.Fatal("a write after an interim answer got no cookie")
	}
}

func TestChangeAfterTheAnswerStarted(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		pair := f.signIn(alice)
		sid := f.sidOf(pair)
		rec := f.serve(get("/", pair), func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("started"))
			From(r).Set("late", "change")
		})
		if f.sessionCookie(rec) != pair {
			t.Fatal("the cookie was decided at the first byte and is the session's")
		}
		if row := f.row(sid); !strings.Contains(row.json, `"late":"change"`) {
			t.Fatalf("a change after the first byte was lost: %s", row.json)
		}

		// On a new session the row is written, and no cookie can follow
		var newSID string
		rec = f.serve(get("/"), func(w http.ResponseWriter, r *http.Request) {
			newSID = From(r).ID()
			_, _ = w.Write([]byte("started"))
			From(r).Set("late", "change")
		})
		if len(setCookies(rec)) != 0 {
			t.Fatalf("Set-Cookie %q after the header went out", setCookies(rec))
		}
		if row := f.row(newSID); row == nil || !strings.Contains(row.json, `"late":"change"`) {
			t.Fatalf("the late change of a new session was not stored: %+v", row)
		}

		// A sign-out after the first byte still ends the session
		f.serve(get("/", pair), func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("started"))
			From(r).End(ReasonLogout)
			_ = From(r).Destroy()
		})
		if row := f.row(sid); row.active || row.json != "{}" {
			t.Fatalf("row after a late sign-out: %+v", row)
		}
	})
}

// Sign-in, the lifetimes and the answers of an ended session.

func TestEstablish(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		now := f.clock.Now()
		pair := f.signIn(User{ID: 7, Username: "alice", Role: "operator"}, func(r *http.Request) {
			r.RemoteAddr = "[::ffff:203.0.113.9]:1234"
			r.Header.Set("User-Agent", "  ExpAgent/1.0 ")
		})
		sid := f.sidOf(pair)
		row := f.row(sid)
		wantJSON := fmt.Sprintf(`{"cookie":{"originalMaxAge":1800000,"expires":"%s","secure":false,"httpOnly":true,"path":"/","sameSite":"lax"},`+
			`"userId":7,"user":{"id":7,"username":"alice","role":"operator","preferred_language":null},`+
			`"createdAt":%d,"lastActivityAt":%d,"loginIp":"203.0.113.9","loginUserAgent":"ExpAgent/1.0","clientFingerprint":"fp_abcdef123456"}`,
			httpx.ISOTime(now.Add(30*time.Minute)), now.UnixMilli(), now.UnixMilli())
		if row.json != wantJSON {
			t.Fatalf("session_json:\n got %s\nwant %s", row.json, wantJSON)
		}
		if !row.active || row.userID.Int64 != 7 || row.username.String != "alice" || row.ip.String != "203.0.113.9" ||
			row.userAgent.String != "ExpAgent/1.0" || row.fp.String != "fp_abcdef123456" ||
			row.createdAt.Int64 != now.UnixMilli() || row.lastActivityAt.Int64 != now.UnixMilli() ||
			row.expiresAt != now.Add(30*time.Minute).UnixMilli() || row.reason.Valid || row.endedAt.Valid {
			t.Fatalf("row: %+v", row)
		}

		var got *User
		var createdAt int64
		var loginIP string
		f.serve(get("/", pair), func(w http.ResponseWriter, r *http.Request) {
			s := From(r)
			got = s.User()
			s.Get("createdAt", &createdAt)
			s.Get("loginIp", &loginIP)
			if s.UserID() != 7 || s.ID() != sid {
				t.Errorf("UserID %d, ID %s", s.UserID(), s.ID())
			}
		})
		if got == nil || *got != (User{ID: 7, Username: "alice", Role: "operator"}) || createdAt != now.UnixMilli() || loginIP != "203.0.113.9" {
			t.Fatalf("user %+v, createdAt %d, loginIp %q", got, createdAt, loginIP)
		}
	})
}

func TestEstablishWithLanguageAndLimit(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db, withLimit(1))
		first := f.signIn(alice)
		var replaced int
		rec := f.serve(get("/"), func(w http.ResponseWriter, r *http.Request) {
			s := From(r)
			_ = s.Regenerate()
			var err error
			replaced, err = s.Establish(User{ID: 7, Username: "alice", Role: "admin", PreferredLanguage: "de"}, Meta{IP: "198.51.100.1"})
			if err != nil {
				t.Errorf("Establish: %v", err)
			}
			s.Set("emergencyMode", true)
		})
		if replaced != 1 {
			t.Fatalf("replaced %d sessions, want 1", replaced)
		}
		old := f.row(f.sidOf(first))
		if old.active || old.reason.String != ReasonReplaced {
			t.Fatalf("the first session: %+v, want replaced", old)
		}
		row := f.row(f.sidOf(f.sessionCookie(rec)))
		if !strings.Contains(row.json, `"user":{"id":7,"username":"alice","role":"admin","preferred_language":"de"}`) ||
			!strings.Contains(row.json, `"clientFingerprint":""`) || !strings.HasSuffix(row.json, `,"emergencyMode":true}`) {
			t.Fatalf("session_json: %s", row.json)
		}
		if row.fp.Valid || row.userAgent.Valid {
			t.Fatalf("empty details must be NULL: %+v", row)
		}
		// The replaced browser is told why at its next page: the sign-in
		// page with the reason, its session cookie cleared, and the reason
		// kept a few minutes for the answers after this one (A239)
		var userID int64 = -1
		rec = f.serve(get("/", first), func(w http.ResponseWriter, r *http.Request) { userID = From(r).UserID() })
		cookies := setCookies(rec)
		if userID != -1 || rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), "reason=session_replaced") ||
			len(cookies) != 3 || cookies[2] != "nd.ended=session_replaced; Max-Age=600; Path=/; HttpOnly; SameSite=Lax" {
			t.Fatalf("replaced browser: user %d, %d %q, Set-Cookie %q", userID, rec.Code, rec.Header().Get("Location"), setCookies(rec))
		}
	})
}

func TestActivityMovesTheSession(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		pair := f.signIn(alice)
		sid := f.sidOf(pair)
		start := f.clock.Now()

		f.clock.Advance(10 * time.Minute)
		rec := f.serve(get("/api/devices", pair), nil)
		now := f.clock.Now()
		row := f.row(sid)
		if row.lastActivityAt.Int64 != now.UnixMilli() || !strings.Contains(row.json, fmt.Sprintf(`"lastActivityAt":%d`, now.UnixMilli())) {
			t.Fatalf("last activity did not move: %+v", row)
		}
		if row.createdAt.Int64 != start.UnixMilli() || !strings.Contains(row.json, fmt.Sprintf(`"createdAt":%d`, start.UnixMilli())) {
			t.Fatalf("created_at moved: %+v", row)
		}
		if want := now.Add(30 * time.Minute).UnixMilli(); row.expiresAt != want {
			t.Fatalf("expires_at = %d, want now plus the idle timeout", row.expiresAt)
		}
		if c := setCookies(rec)[0]; !strings.Contains(c, "Expires="+now.Add(30*time.Minute).UTC().Format(http.TimeFormat)) {
			t.Fatalf("cookie %q", c)
		}
	})
}

func background(r *http.Request) *http.Request {
	r.Header.Set("X-ND-Background", "1")
	return r
}

func TestBackgroundRequestDoesNotCount(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		pair := f.signIn(alice)
		sid := f.sidOf(pair)
		start := f.clock.Now()
		before := f.row(sid)

		f.clock.Advance(10 * time.Minute)
		var userID int64
		rec := f.serve(background(get("/api/poll", pair)), func(w http.ResponseWriter, r *http.Request) { userID = From(r).UserID() })
		if userID != alice.ID {
			t.Fatalf("a background request is answered signed in, got user %d", userID)
		}
		row := f.row(sid)
		if row.lastActivityAt.Int64 != start.UnixMilli() || row.json != before.json {
			t.Fatalf("a background request counted as activity:\n was %s\n now %s", before.json, row.json)
		}
		// The cookie and the row's expiry still move
		if f.sessionCookie(rec) != pair || row.expiresAt != f.clock.Now().Add(30*time.Minute).UnixMilli() {
			t.Fatalf("cookie %q, expires_at %d", f.sessionCookie(rec), row.expiresAt)
		}

		// Only exactly "1" marks a background request
		for _, value := range []string{"true", "0", "1 1"} {
			r := get("/api/poll", pair)
			r.Header.Set("X-ND-Background", value)
			f.serve(r, nil)
			if got := f.row(sid).lastActivityAt.Int64; got != f.clock.Now().UnixMilli() {
				t.Fatalf("X-ND-Background: %s did not count as activity", value)
			}
			f.clock.Advance(time.Second)
		}
		r := get("/api/poll", pair)
		r.Header.Add("X-ND-Background", "1")
		r.Header.Add("X-ND-Background", "1")
		f.serve(r, nil)
		if got := f.row(sid).lastActivityAt.Int64; got != f.clock.Now().UnixMilli() {
			t.Fatal("the header sent twice is not the value 1 and counts as activity")
		}
	})
}

func TestBackgroundRequestsDoNotKeepAnIdleSessionAlive(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		pair := f.signIn(alice)
		sid := f.sidOf(pair)
		for i := 0; i < 3; i++ {
			f.clock.Advance(10 * time.Minute)
			f.serve(background(get("/api/poll", pair)), nil)
		}
		if !f.row(sid).active {
			t.Fatal("the session ended at exactly the idle timeout")
		}
		f.clock.Advance(time.Millisecond)
		called := false
		rec := f.serve(background(get("/api/poll", pair)), func(http.ResponseWriter, *http.Request) { called = true })
		row := f.row(sid)
		if called || row.active || row.reason.String != ReasonIdle {
			t.Fatalf("handler ran %v, row %+v; want the session ended as idle", called, row)
		}
		// It is answered as an ended session: cookie cleared, audited
		if rec.Code != http.StatusUnauthorized || len(setCookies(rec)) == 0 || len(f.audits) != 1 || row.json != "{}" {
			t.Fatalf("status %d, Set-Cookie %q, audits %v, JSON %s", rec.Code, setCookies(rec), f.audits, row.json)
		}
	})
}

func TestAbsoluteLifetime(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db, func(o *Options) {
			o.Policy = Policy{IdleTimeoutMs: 20 * 60 * 1000, MaxAgeMs: 60 * 60 * 1000, MaxSessionsPerUser: 1}
		})
		pair := f.signIn(alice)
		sid := f.sidOf(pair)
		start := f.clock.Now()
		// Active every 15 minutes: never idle
		for i := 0; i < 3; i++ {
			f.clock.Advance(15 * time.Minute)
			f.serve(get("/", pair), nil)
		}
		// 45 minutes in, 15 are left: the cookie and the row end with the
		// absolute lifetime, not 20 minutes from now
		row := f.row(sid)
		if want := start.Add(time.Hour).UnixMilli(); row.expiresAt != want {
			t.Fatalf("expires_at is %d ms after sign-in, want 3600000", row.expiresAt-start.UnixMilli())
		}
		if !strings.Contains(row.json, `"originalMaxAge":900000`) {
			t.Fatalf("session_json: %s", row.json)
		}
		// One millisecond before the end the session is alive, and its
		// cookie lasts that one millisecond
		f.clock.Advance(15*time.Minute - time.Millisecond)
		f.serve(get("/", pair), nil)
		if row := f.row(sid); !row.active || !strings.Contains(row.json, `"originalMaxAge":1,`) || row.expiresAt != start.Add(time.Hour).UnixMilli() {
			t.Fatalf("row one millisecond before the end: %+v", row)
		}
		f.clock.Advance(2 * time.Millisecond)
		called := false
		rec := f.serve(get("/", pair), func(http.ResponseWriter, *http.Request) { called = true })
		if row := f.row(sid); called || rec.Code != http.StatusFound || row.active || row.reason.String != ReasonMaxAge {
			t.Fatalf("handler ran %v, status %d, row %+v; want ended as max_age and sent to the sign-in page", called, rec.Code, row)
		}
	})
}

func TestAbsoluteLifetimeNearTheEndRefusesWithoutACookieLifetime(t *testing.T) {
	// A request at the very millisecond the lifetime ends passes the row's
	// check when the row's expiry lies ahead, and is refused because no
	// lifetime is left for its cookie
	f := newFixture(t, storetest.SQLite(t), func(o *Options) {
		o.Policy = Policy{IdleTimeoutMs: 20 * 60 * 1000, MaxAgeMs: 60 * 60 * 1000, MaxSessionsPerUser: 1}
	})
	pair := f.signIn(alice)
	sid := f.sidOf(pair)
	for i := 0; i < 3; i++ {
		f.clock.Advance(15 * time.Minute)
		f.serve(get("/", pair), nil)
	}
	f.clock.Advance(15 * time.Minute)
	f.setExpires(sid, f.clock.Now().UnixMilli()+60_000)
	rec := f.serve(get("/api/x", pair), nil)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"code":"session_expired"`) {
		t.Fatalf("answer %d %s", rec.Code, rec.Body.String())
	}
	if row := f.row(sid); row.active || row.reason.String != ReasonMaxAge {
		t.Fatalf("row: %+v", row)
	}
}

// staleJSON makes the session's own times older than the row's, so the
// row passes and the check on the session itself answers.
func (f *fixture) staleJSON(sid, field string, age time.Duration) {
	f.t.Helper()
	row := f.row(sid)
	old := f.clock.Now().Add(-age).UnixMilli()
	data, err := parseFields([]byte(row.json))
	if err != nil {
		f.t.Fatal(err)
	}
	data.set(field, mustJSON(old))
	f.exec(`UPDATE panel_sessions SET session_json = ? WHERE sid = ?`, data.encode(true), sid)
}

func TestExpiredAnswerForTheAPI(t *testing.T) {
	cases := []struct {
		name, field string
		age         time.Duration
		reason      string
		code        string
	}{
		{"idle", "lastActivityAt", 31 * time.Minute, ReasonIdle, "session_idle"},
		{"absolute lifetime", "createdAt", 25 * time.Hour, ReasonMaxAge, "session_expired"},
	}
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				f := newFixture(t, db)
				pair := f.signIn(alice)
				sid := f.sidOf(pair)
				f.staleJSON(sid, tc.field, tc.age)
				called := false
				r := get("/api/devices", pair)
				r.RemoteAddr = "[::ffff:203.0.113.5]:40000"
				rec := f.serve(r, func(http.ResponseWriter, *http.Request) { called = true })
				if called {
					t.Fatal("the handler ran for an ended session")
				}
				wantBody := `{"success":false,"error":"Session expired. Please log in again.","code":"` + tc.code + `"}`
				if rec.Code != http.StatusUnauthorized || rec.Body.String() != wantBody {
					t.Fatalf("answer %d %s\nwant 401 %s", rec.Code, rec.Body.String(), wantBody)
				}
				if ct := rec.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
					t.Fatalf("Content-Type %q", ct)
				}
				wantCookies := []string{
					"nd.sid=; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
					"neodesk.sid=; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
					"nd.sid=; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
				}
				if got := setCookies(rec); strings.Join(got, "\n") != strings.Join(wantCookies, "\n") {
					t.Fatalf("Set-Cookie:\n got %q\nwant %q", got, wantCookies)
				}
				row := f.row(sid)
				if row.active || row.reason.String != tc.reason || row.json != "{}" || row.endedAt.Int64 != f.clock.Now().UnixMilli() {
					t.Fatalf("row: %+v", row)
				}
				want := auditEntry{alice.ID, "session_expired", "reason=" + tc.reason, "::ffff:203.0.113.5"}
				if len(f.audits) != 1 || f.audits[0] != want {
					t.Fatalf("audit %+v, want %+v", f.audits, want)
				}
			})
		}
	})
}

func TestExpiredAnswerForAPage(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db, httpsMode)
		behindProxy := func(r *http.Request) *http.Request {
			r.RemoteAddr = "127.0.0.1:50000"
			r.Header.Set("X-Forwarded-Proto", "https")
			return r
		}
		pair := f.signIn(alice, func(r *http.Request) { behindProxy(r) })
		cases := []struct {
			accept, wantType, wantBody string
			json                       bool
		}{
			{"text/html,application/xhtml+xml,*/*;q=0.8", "text/html; charset=utf-8", "<p>Found. Redirecting to /login?expired=1&amp;return=/devices</p>", false},
			{"*/*", "text/plain; charset=utf-8", "Found. Redirecting to /login?expired=1&return=/devices", false},
			{"", "text/plain; charset=utf-8", "Found. Redirecting to /login?expired=1&return=/devices", false},
			{"text/*;q=0.5, text/html;q=0.4", "text/plain; charset=utf-8", "Found. Redirecting to /login?expired=1&return=/devices", false},
			{"text/plain;q=0, text/html", "text/html; charset=utf-8", "<p>Found. Redirecting to /login?expired=1&amp;return=/devices</p>", false},
			{"image/png", "", "", false},
			{"text/html, application/json", "", "", true},
		}
		for _, tc := range cases {
			pair = f.signIn(alice, func(r *http.Request) { behindProxy(r) })
			sid := f.sidOf(pair)
			f.staleJSON(sid, "lastActivityAt", 31*time.Minute)
			r := behindProxy(get("/devices", pair))
			if tc.accept != "" {
				r.Header.Set("Accept", tc.accept)
			}
			rec := f.serve(r, nil)
			if tc.json {
				if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"code":"session_idle"`) {
					t.Errorf("Accept %q: %d %s, want the JSON answer", tc.accept, rec.Code, rec.Body.String())
				}
				continue
			}
			if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/login?expired=1&return=/devices" {
				t.Errorf("Accept %q: %d to %q", tc.accept, rec.Code, rec.Header().Get("Location"))
			}
			if rec.Header().Get("Content-Type") != tc.wantType || rec.Body.String() != tc.wantBody || rec.Header().Get("Vary") != "Accept" ||
				rec.Header().Get("Content-Length") != fmt.Sprint(len(tc.wantBody)) {
				t.Errorf("Accept %q: type %q, body %q, headers %v", tc.accept, rec.Header().Get("Content-Type"), rec.Body.String(), rec.Header())
			}
			wantCookies := []string{
				"neodesk.sid=; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
				"neodesk.sid=; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
				"nd.sid=; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT",
			}
			if got := setCookies(rec); strings.Join(got, "\n") != strings.Join(wantCookies, "\n") {
				t.Errorf("Accept %q: Set-Cookie %q", tc.accept, got)
			}
			if row := f.row(sid); row.active || row.reason.String != ReasonIdle {
				t.Errorf("Accept %q: row %+v", tc.accept, row)
			}
		}
		// HEAD gets no body
		pair = f.signIn(alice, func(r *http.Request) { behindProxy(r) })
		f.staleJSON(f.sidOf(pair), "lastActivityAt", 31*time.Minute)
		r := behindProxy(get("/devices", pair))
		r.Method = http.MethodHead
		if rec := f.serve(r, nil); rec.Code != http.StatusFound || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != "54" {
			t.Errorf("HEAD: %d, body %q, length %s", rec.Code, rec.Body.String(), rec.Header().Get("Content-Length"))
		}
	})
}

func TestSessionWithoutCreatedAt(t *testing.T) {
	f := newFixture(t, storetest.SQLite(t))
	pair := f.signIn(alice)
	sid := f.sidOf(pair)
	data, _ := parseFields([]byte(f.row(sid).json))
	data.delete("createdAt")
	data.delete("lastActivityAt")
	f.exec(`UPDATE panel_sessions SET session_json = ? WHERE sid = ?`, data.encode(true), sid)
	f.clock.Advance(time.Minute)
	f.serve(get("/", pair), nil)
	now := f.clock.Now().UnixMilli()
	if row := f.row(sid); !strings.Contains(row.json, fmt.Sprintf(`"createdAt":%d`, now)) || !strings.Contains(row.json, fmt.Sprintf(`"lastActivityAt":%d`, now)) {
		t.Fatalf("a session without times gets them on its next request: %s", row.json)
	}
}

func TestIPChangeIsAuditedOncePerAddress(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		pair := f.signIn(alice)
		sid := f.sidOf(pair)
		from := func(addr string) *http.Request {
			r := background(get("/api/x", pair))
			r.RemoteAddr = addr
			r.Header.Set("User-Agent", strings.Repeat("U", 200))
			return r
		}
		f.serve(from("203.0.113.5:1"), nil)
		if len(f.audits) != 0 {
			t.Fatalf("audit for the sign-in address: %+v", f.audits)
		}
		f.serve(from("[::ffff:198.51.100.7]:2"), nil)
		f.serve(from("198.51.100.7:3"), nil)
		want := auditEntry{alice.ID, "session_ip_change",
			"IP change during session: loginIp=203.0.113.5 currentIp=198.51.100.7 ua=" + strings.Repeat("U", 160), "198.51.100.7"}
		if len(f.audits) != 1 || f.audits[0] != want {
			t.Fatalf("audits:\n got %+v\nwant %+v", f.audits, want)
		}
		if row := f.row(sid); !strings.HasSuffix(row.json, `,"_lastSeenIp":"198.51.100.7"}`) || !row.active {
			t.Fatalf("row: %+v", row)
		}
		// Back to the sign-in address: no entry, and the last seen stays.
		// Then the other address again: still the last seen, no entry.
		f.serve(from("203.0.113.5:4"), nil)
		f.serve(from("198.51.100.7:5"), nil)
		if len(f.audits) != 1 {
			t.Fatalf("audits: %+v", f.audits)
		}
		f.serve(from("192.0.2.1:6"), nil)
		if len(f.audits) != 2 || f.audits[1].ip != "192.0.2.1" {
			t.Fatalf("audits: %+v", f.audits)
		}
	})
}

// A session cookie whose row cannot be read for a failure of the database
// gets the error answer, and without an OnError of its own a plain 500.
func TestDefaultErrorAnswer(t *testing.T) {
	db := storetest.SQLite(t)
	f := newFixture(t, db, func(o *Options) { o.OnError = nil })
	now := f.clock.Now().UnixMilli()
	f.mustSet("row", record{json: `{"cookie":{}}`, expiresAt: now + 60_000, createdAt: now, lastActivityAt: now})
	f.exec(`DROP TABLE panel_sessions`)
	if rec := f.serve(get("/", f.cookieFor("row")), nil); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
}

func TestLoadForWebsockets(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		pair := f.signIn(alice)
		sid := f.sidOf(pair)
		before := f.row(sid)
		f.clock.Advance(5 * time.Minute)

		s, err := f.m.Load(get("/ws/chat-operator/1", pair))
		if err != nil || s.UserID() != alice.ID || s.User().Role != "operator" || s.ID() != sid {
			t.Fatalf("Load: %+v, %v", s, err)
		}
		after := f.row(sid)
		if after.json != before.json || after.expiresAt != before.expiresAt || after.lastActivityAt.Int64 != before.lastActivityAt.Int64 {
			t.Fatalf("Load touched the row:\n was %+v\n now %+v", before, after)
		}

		for name, cookie := range map[string]string{"no cookie": "", "bad signature": forged(f, sid), "unknown id": f.cookieFor("nobody")} {
			s, err := f.m.Load(get("/ws/x", cookie))
			if err != nil || s == nil || s.UserID() != 0 {
				t.Errorf("%s: %+v, %v; want an empty session", name, s, err)
			}
		}

		// The store's expiry check applies
		f.clock.Advance(26 * time.Minute)
		s, err = f.m.Load(get("/ws/x", pair))
		if err != nil || s.UserID() != 0 {
			t.Fatalf("Load after the idle timeout: user %d, %v", s.UserID(), err)
		}
		if row := f.row(sid); row.active || row.reason.String != ReasonIdle {
			t.Fatalf("row: %+v", row)
		}
		if From(get("/", pair)) != nil {
			t.Fatal("From outside the middleware is nil")
		}
	})
}

// Fields.

func TestUnknownFieldsSurviveAReadModifyWrite(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		now := f.clock.Now().UnixMilli()
		const sid = "node-session"
		// What a Node route could have stored, in forms a float64 would change
		stored := `{"big":12345678901234567890123,"cookie":{"originalMaxAge":1800000,"expires":"2026-11-10T01:34:49.646Z","secure":false,"httpOnly":true,"path":"/","sameSite":"lax"},` +
			`"float":1.0,"exp":1e3,"neg":-0,"precise":0.1000000000000000055511151231257827,"nested":{"b":[1,2,{"c":null}],"a":"\u00e9\ud83d\ude00<>&"},"text":"a\/b","empty":"","no":false,"nil":null}`
		f.mustSet(sid, record{json: stored, expiresAt: now + 60_000, createdAt: now, lastActivityAt: now})
		f.serve(get("/", f.cookieFor(sid)), func(w http.ResponseWriter, r *http.Request) {
			s := From(r)
			s.Set("added", map[string]any{"x": 1})
			s.Set("text", "replaced")
			s.Delete("empty")
		})
		got := f.row(sid).json
		want := `{"big":12345678901234567890123,"cookie":{"originalMaxAge":1800000,"expires":"` + httpx.ISOTime(f.clock.Now().Add(30*time.Minute)) + `","secure":false,"httpOnly":true,"path":"/","sameSite":"lax"},` +
			`"float":1.0,"exp":1e3,"neg":-0,"precise":0.1000000000000000055511151231257827,"nested":{"b":[1,2,{"c":null}],"a":"\u00e9\ud83d\ude00<>&"},"text":"replaced","no":false,"nil":null,"added":{"x":1}}`
		if got != want {
			t.Fatalf("session_json after a write:\n got %s\nwant %s", got, want)
		}
	})
}

func TestGetSetDelete(t *testing.T) {
	f := newFixture(t, storetest.SQLite(t))
	type pending struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
		TOTP     bool   `json:"totpRequired"`
	}
	pair := f.sessionCookie(f.serve(get("/"), func(w http.ResponseWriter, r *http.Request) {
		s := From(r)
		s.Set("pendingTotpUserId", int64(7))
		s.Set("pendingTotpUser", pending{7, "alice", true})
		s.Set("pendingClientMeta", Meta{IP: "1.2.3.4", UserAgent: "UA <1>", Fingerprint: "fp_12345678"})
		s.Set("cookie", "ignored")
	}))
	row := f.row(f.sidOf(pair))
	if !strings.HasSuffix(row.json, `,"pendingTotpUserId":7,"pendingTotpUser":{"id":7,"username":"alice","totpRequired":true},`+
		`"pendingClientMeta":{"ip":"1.2.3.4","userAgent":"UA <1>","clientFingerprint":"fp_12345678"}}`) {
		t.Fatalf("session_json: %s", row.json)
	}
	if row.userID.Valid {
		t.Fatal("a session waiting for the second factor has no account in its row")
	}
	f.serve(get("/", pair), func(w http.ResponseWriter, r *http.Request) {
		s := From(r)
		var id int64
		var user pending
		var meta Meta
		var text string
		if !s.Get("pendingTotpUserId", &id) || !s.Get("pendingTotpUser", &user) || !s.Get("pendingClientMeta", &meta) {
			t.Error("a stored field was not found")
		}
		if id != 7 || user != (pending{7, "alice", true}) || meta != (Meta{"1.2.3.4", "UA <1>", "fp_12345678"}) {
			t.Errorf("got %d, %+v, %+v", id, user, meta)
		}
		if s.Get("missing", &text) || s.Get("pendingTotpUser", &text) || s.Get("cookie", &text) {
			t.Error("Get answered true for a missing field, a value of another type or the cookie")
		}
		if s.UserID() != 0 || s.User() != nil {
			t.Error("a pending session counts as signed in")
		}
		s.Delete("pendingTotpUser")
		s.Delete("cookie")
		s.Delete("missing")
	})
	if got := f.row(f.sidOf(pair)).json; strings.Contains(got, "pendingTotpUser\"") || !strings.Contains(got, `"cookie":{`) {
		t.Fatalf("after Delete: %s", got)
	}
}

func TestSetPanicsOnAValueJSONCannotHold(t *testing.T) {
	f := newFixture(t, storetest.SQLite(t))
	f.serve(get("/"), func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recover() == nil {
				t.Error("Set took a channel")
			}
		}()
		From(r).Set("bad", make(chan int))
	})
}

func TestUserFallbacks(t *testing.T) {
	f := newFixture(t, storetest.SQLite(t))
	now := f.clock.Now().UnixMilli()
	cases := []struct {
		fields string
		id     int64
		want   *User
	}{
		{`"userId":7,"user":{"id":7,"username":"alice","role":"admin","preferred_language":"pl"}`, 7, &User{7, "alice", "admin", "pl"}},
		{`"userId":7,"username":"oidc-user","role":"viewer"`, 7, &User{7, "oidc-user", "viewer", ""}},
		{`"userId":7,"user":{"id":7},"username":"top","role":"operator"`, 7, &User{7, "top", "operator", ""}},
		{`"userId":7,"user":"broken"`, 7, &User{ID: 7}},
		{`"userId":0,"user":{"id":7,"username":"alice","role":"admin"}`, 0, nil},
		{`"userId":null,"user":{"id":7}`, 0, nil},
		{`"user":{"id":7,"username":"alice","role":"admin"}`, 0, nil},
		{`"userId":"","user":{"id":7}`, 0, nil},
		{`"userId":false`, 0, nil},
		{`"userId":"12"`, 12, &User{ID: 12}},
		{`"userId":7.5`, 0, &User{}},
	}
	for i, tc := range cases {
		sid := fmt.Sprintf("u-%d", i)
		text := fmt.Sprintf(`{"cookie":{"originalMaxAge":60000},"createdAt":%d,"lastActivityAt":%d,%s}`, now, now, tc.fields)
		f.mustSet(sid, record{json: text, expiresAt: now + 60_000, createdAt: now, lastActivityAt: now})
		f.serve(background(get("/", f.cookieFor(sid))), func(w http.ResponseWriter, r *http.Request) {
			s := From(r)
			got := s.User()
			if s.UserID() != tc.id || (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Errorf("%s: UserID %d, User %+v; want %d, %+v", tc.fields, s.UserID(), got, tc.id, tc.want)
			}
		})
	}
}

func TestUserJSON(t *testing.T) {
	raw, err := httpx.Marshal(User{ID: 3, Username: `a"b<c>`, Role: "viewer"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"id":3,"username":"a\"b<c>","role":"viewer","preferred_language":null}`; string(raw) != want {
		t.Fatalf("got %s, want %s", raw, want)
	}
}

func TestClientMeta(t *testing.T) {
	f := newFixture(t, storetest.SQLite(t), func(o *Options) {
		o.TrustProxy, _ = httpx.ParseTrustProxy("1")
	})
	r := get("/")
	r.RemoteAddr = "127.0.0.1:1"
	r.Header.Set("X-Forwarded-For", "203.0.113.9, ::ffff:198.51.100.7")
	r.Header.Set("User-Agent", " Caf\xe9/1.0 ")
	r.Header.Set("X-Client-Fingerprint", "fp_from_header")
	if got, want := f.m.ClientMeta(r, ""), (Meta{"198.51.100.7", "Café/1.0", "fp_from_header"}); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	if got := f.m.ClientMeta(r, "fp_from_body").Fingerprint; got != "fp_from_body" {
		t.Fatalf("fingerprint %q, want the body's", got)
	}
	if got := f.m.ClientMeta(r, "bad body value").Fingerprint; got != "" {
		t.Fatalf("fingerprint %q; a bad one in the body does not fall back to the header", got)
	}
}

func TestNewRefusesBadOptions(t *testing.T) {
	db := storetest.SQLite(t)
	if _, err := New(ctx, Options{DB: db}); err == nil {
		t.Error("New took an empty secret")
	}
	if _, err := New(ctx, Options{Secret: "x"}); err == nil {
		t.Error("New took no database")
	}
	m, err := New(ctx, Options{DB: db, Secret: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Policy() != PolicyFromEnv(env()) {
		t.Errorf("policy %+v, want the defaults", m.Policy())
	}
}

func TestEmptySessionID(t *testing.T) {
	f := newFixture(t, storetest.SQLite(t))
	// An empty id with its true signature is still no session id
	r := get("/", f.m.cookieName+"="+httpx.EncodeURIComponent(cookiePrefix+sign("", testSecret)))
	if id, ok := f.m.cookieID(r); ok {
		t.Fatalf("took the empty session id %q", id)
	}
}

func TestDestroyAlone(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db)
		pair := f.signIn(alice)
		sid := f.sidOf(pair)
		rec := f.serve(get("/", pair), func(w http.ResponseWriter, r *http.Request) {
			if err := From(r).Destroy(); err != nil {
				t.Errorf("Destroy: %v", err)
			}
			// A second call is harmless
			if err := From(r).Destroy(); err != nil {
				t.Errorf("second Destroy: %v", err)
			}
			w.WriteHeader(http.StatusOK)
		})
		if got := setCookies(rec); len(got) != 0 {
			t.Fatalf("Set-Cookie %q after Destroy, want none", got)
		}
		row := f.row(sid)
		if row.active || row.reason.String != ReasonDestroyed || row.json != "{}" || row.endedAt.Int64 != f.clock.Now().UnixMilli() {
			t.Fatalf("row after Destroy: %+v", row)
		}
		var userID int64 = -1
		f.serve(get("/", pair), func(w http.ResponseWriter, r *http.Request) { userID = From(r).UserID() })
		if userID != 0 {
			t.Fatalf("user %d after Destroy", userID)
		}
	})
}

func TestFieldWithAnEmptyNameSurvives(t *testing.T) {
	f := newFixture(t, storetest.SQLite(t))
	now := f.clock.Now().UnixMilli()
	f.mustSet("sid", record{json: `{"cookie":{"originalMaxAge":60000},"":"nameless","a":1}`, expiresAt: now + 60_000, createdAt: now, lastActivityAt: now})
	f.serve(get("/", f.cookieFor("sid")), write("b", 2))
	if got := f.row("sid").json; !strings.Contains(got, `,"":"nameless","a":1,"b":2}`) {
		t.Fatalf("session_json after a write: %s", got)
	}
}

// A background request is checked against both limits like any other.
func TestExpiredAnswerForABackgroundRequest(t *testing.T) {
	cases := []struct {
		field string
		age   time.Duration
		code  string
	}{
		{"lastActivityAt", 31 * time.Minute, "session_idle"},
		{"createdAt", 25 * time.Hour, "session_expired"},
	}
	f := newFixture(t, storetest.SQLite(t))
	for _, tc := range cases {
		pair := f.signIn(alice)
		f.staleJSON(f.sidOf(pair), tc.field, tc.age)
		rec := f.serve(background(get("/api/poll", pair)), nil)
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"code":"`+tc.code+`"`) {
			t.Errorf("%s too old: %d %s, want 401 with %s", tc.field, rec.Code, rec.Body.String(), tc.code)
		}
	}
}

// TestTheEndedReasonCookieIsSecureLikeTheSession: the cookie that keeps
// why a session ended is Secure exactly when the session cookie is: behind
// a TLS proxy the console trusts, and not over plain HTTP (A239).
func TestTheEndedReasonCookieIsSecureLikeTheSession(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		f := newFixture(t, db, withLimit(1), func(o *Options) {
			o.TrustProxy, _ = httpx.ParseTrustProxy("loopback")
		})
		overHTTPS := func(r *http.Request) {
			r.RemoteAddr = "127.0.0.1:50000"
			r.Header.Set("X-Forwarded-Proto", "https")
		}
		for _, c := range []struct {
			name   string
			change []func(*http.Request)
			secure bool
		}{{"plain HTTP", nil, false}, {"through a trusted proxy over HTTPS", []func(*http.Request){overHTTPS}, true}} {
			first := f.signIn(alice, c.change...)
			f.signIn(alice, c.change...)
			r := get("/api/x", first)
			for _, fn := range c.change {
				fn(r)
			}
			var ended string
			for _, line := range setCookies(f.serve(r, nil)) {
				if strings.HasPrefix(line, httpx.EndedCookie+"=") {
					ended = line
				}
			}
			if ended == "" || strings.Contains(ended, "; Secure") != c.secure || f.m.SecureCookie(r) != c.secure {
				t.Errorf("%s: %q, want Secure %v as the session cookie", c.name, ended, c.secure)
			}
		}
	})
}
