package session

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/session/cookie"
	"github.com/unitronix/betterdesk-server/console/store"
)

// signedByHand is the signed cookie value for a session id, computed
// without the package's own signing code.
func signedByHand(sid string) string {
	mac := hmac.New(sha256.New, []byte(testSecret))
	mac.Write([]byte(sid))
	return "s:" + sid + "." + strings.TrimRight(base64.StdEncoding.EncodeToString(mac.Sum(nil)), "=")
}

// FuzzCookieValue feeds arbitrary Cookie headers to the middleware. It
// must never panic, and it may take a session id from the header only when
// the header's cookie carries exactly the signature made with the secret.
func FuzzCookieValue(f *testing.F) {
	const victim = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	for _, v := range signVectors {
		f.Add("bd.sid=" + v.wire)
		f.Add("bd.sid=" + v.signed)
		f.Add("a=b; bd.sid=" + v.wire[:len(v.wire)-1])
	}
	f.Add("bd.sid=s%3A" + victim + ".")
	f.Add("bd.sid=s%3A" + victim)
	f.Add("bd.sid=" + victim)
	f.Add("bd.sid=s%3A.47DEQpj8HBSa%2B%2FTImW%2B5JCeuQeRkm5NMpJWZG3hSuFU")
	f.Add("bd.sid=s:%zz; bd.sid=" + signVectors[0].wire)
	f.Add(`bd.sid="` + signVectors[0].signed + `"`)
	f.Add("bd.sid=s%3A" + victim + ".Qb%2FrWF8%2BjkBOBM6RtELK7KYpNGwASfnZPgGVN%2BFmpz4%3D")
	f.Add("betterdesk.sid=" + signVectors[0].wire)

	db, err := store.OpenSQLite(filepath.Join(f.TempDir(), "fuzz.sqlite3"))
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(func() { _ = db.Close() })
	start := time.Now()
	m, err := New(context.Background(), Options{DB: db, Secret: testSecret, Now: func() time.Time { return start }})
	if err != nil {
		f.Fatal(err)
	}
	now := start.UnixMilli()
	// The victim is signed in; every other id has no row
	err = m.table.set(context.Background(), victim, record{
		json:      `{"cookie":{"originalMaxAge":1800000,"expires":null},"userId":7,"createdAt":` + mustJSONString(now) + `,"lastActivityAt":` + mustJSONString(now) + `}`,
		expiresAt: now + 3_600_000, createdAt: now, lastActivityAt: now, userID: int64(7),
	})
	if err != nil {
		f.Fatal(err)
	}

	f.Fuzz(func(t *testing.T, header string) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header["Cookie"] = []string{header}
		r.Header.Set("X-BD-Background", "1")

		value, found := cookie.Lookup(header, "bd.sid")
		id, ok := m.cookieID(r)
		if ok {
			if !found || id == "" || value != signedByHand(id) {
				t.Fatalf("header %q: took session id %q, but the cookie value %q is not its signed form", header, id, value)
			}
		} else if found && strings.HasPrefix(value, "s:") {
			// Refused: then no id has this value as its signed form
			if dot := strings.LastIndexByte(value, '.'); dot > 2 && value == signedByHand(value[2:dot]) {
				t.Fatalf("header %q: a well-signed cookie was refused", header)
			}
		}

		var userID int64
		rec := httptest.NewRecorder()
		m.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			userID = From(r).UserID()
		})).ServeHTTP(rec, r)
		wantUser := int64(0)
		if found && value == signedByHand(victim) {
			wantUser = 7
		}
		if userID != wantUser {
			t.Fatalf("header %q: signed in as user %d, want %d", header, userID, wantUser)
		}
		if (len(rec.Result().Header.Values("Set-Cookie")) != 0) != (wantUser != 0) {
			t.Fatalf("header %q: Set-Cookie %q", header, rec.Result().Header.Values("Set-Cookie"))
		}
	})
}

func mustJSONString(v any) string { return string(mustJSON(v)) }

// FuzzSessionJSON loads arbitrary stored JSON. Loading must never panic,
// and a load followed by a save must keep every field it did not touch,
// byte for byte.
func FuzzSessionJSON(f *testing.F) {
	f.Add(`{"cookie":{"originalMaxAge":1800000,"expires":"2026-11-10T01:34:49.646Z","secure":false,"httpOnly":true,"path":"/","sameSite":"lax"},"userId":7,"user":{"id":7,"username":"alice","role":"operator","preferred_language":null},"createdAt":1794272689642,"lastActivityAt":1794272689642,"loginIp":"127.0.0.1","loginUserAgent":"ExpAgent/1.0","clientFingerprint":"fp_abcdef123456"}`)
	f.Add(`{"cookie":{"originalMaxAge":null,"expires":null},"pendingTotpUserId":7,"pendingTotpUser":{"id":7,"totpRequired":true,"goPartialToken":null},"pendingClientMeta":{"ip":"::1"}}`)
	f.Add(`{"big":12345678901234567890123,"cookie":{},"float":1.0,"exp":1e3,"neg":-0,"s":"é😀","dup":1,"dup":[2],"touched":"old","gone":true}`)
	f.Add(`{"cookie":[],"a":{"cookie":{"x":1}}, "b" : [ 1 , 2 ] }`)
	f.Add(`{}`)
	f.Add(`{"cookie":null}`)
	f.Add(`[]`)
	f.Add(`{"cookie":{"originalMaxAge":"x","expires":5,"secure":"yes","sameSite":true,"path":7}}`)
	f.Add("{\"cookie\":{},\"k\":\"\xff\"}")
	f.Add(`{"cookie":{"originalMaxAge":1e400}}`)

	db, err := store.OpenSQLite(filepath.Join(f.TempDir(), "fuzz.sqlite3"))
	if err != nil {
		f.Fatal(err)
	}
	f.Cleanup(func() { _ = db.Close() })
	m, err := New(context.Background(), Options{DB: db, Secret: testSecret})
	if err != nil {
		f.Fatal(err)
	}

	f.Fuzz(func(t *testing.T, text string) {
		s, err := m.newSession(httptest.NewRequest(http.MethodGet, "/", nil))
		if err != nil {
			t.Fatal(err)
		}
		if s.restore(text) != nil {
			return
		}
		// Loaded: use it the way requests do
		_ = s.UserID()
		_ = s.User()
		_, _ = s.touchActivity(s.req)
		_ = m.cookieHeader(s)
		s.cookie.touch(m.now())

		var before map[string]json.RawMessage
		if err := json.Unmarshal([]byte(text), &before); err != nil {
			t.Fatalf("restore took what encoding/json refuses: %v\n%s", err, text)
		}
		if err := s.restore(text); err != nil {
			t.Fatal(err)
		}
		s.Set("touched", 1)
		s.Delete("gone")
		saved := s.record(m.now()).json

		var after map[string]json.RawMessage
		if err := json.Unmarshal([]byte(saved), &after); err != nil {
			t.Fatalf("the saved session is not JSON: %v\nloaded %s\nsaved  %s", err, text, saved)
		}
		for key, raw := range before {
			switch key {
			case "cookie", "touched", "gone":
				continue
			}
			if got, ok := after[key]; !ok || !bytes.Equal(got, raw) {
				t.Fatalf("field %q changed from %s to %s\nloaded %s\nsaved  %s", key, raw, got, text, saved)
			}
		}
		if _, ok := after["gone"]; ok {
			t.Fatalf("a deleted field was saved: %s", saved)
		}
		if string(after["touched"]) != "1" {
			t.Fatalf("the set field was not saved: %s", saved)
		}
		if len(after) != len(before)+addedKeys(before) {
			t.Fatalf("the saved session has %d fields, loaded %d\nloaded %s\nsaved  %s", len(after), len(before), text, saved)
		}
		var cookieObject map[string]json.RawMessage
		if err := json.Unmarshal(after["cookie"], &cookieObject); err != nil {
			t.Fatalf("the saved cookie is not an object: %s", after["cookie"])
		}
		for _, key := range []string{"originalMaxAge", "expires", "httpOnly", "path"} {
			if _, ok := cookieObject[key]; !ok {
				t.Fatalf("the saved cookie lacks %s: %s", key, after["cookie"])
			}
		}
	})
}

// addedKeys is how many fields the fuzz body adds to or removes from a
// loaded session: "touched" comes in when new, "gone" goes when there.
func addedKeys(before map[string]json.RawMessage) int {
	n := 0
	if _, ok := before["touched"]; !ok {
		n++
	}
	if _, ok := before["gone"]; ok {
		n--
	}
	return n
}
