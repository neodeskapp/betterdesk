package csrf

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/session/cookie"
)

// FuzzCSRFToken: whatever a client puts into the cookie, the header and the
// body, a write passes only when the cookie holds the token the request
// sent together with the SHA-256 of that token and the secret. Nobody
// without the secret can make such a pair.
func FuzzCSRFToken(f *testing.F) {
	f.Add(vectorPair, vectorToken, "")
	f.Add("__csrf_h="+vectorToken+"|"+vectorHash, vectorToken, "")
	f.Add("__csrf_h="+vectorToken+"%7C"+vectorHash+"%7Cx", vectorToken, "")
	f.Add("__csrf_h=%7C"+vectorHash, "", "")
	f.Add("__csrf_h=j%3A%22"+vectorToken+"%7C"+vectorHash+"%22", vectorToken, "")
	f.Add("a=b; __csrf_h=x|y; __csrf_h="+vectorToken+"|"+vectorHash, "x", "")
	f.Add("__csrf_h=%zz", "%zz", `{"_csrf":"x"}`)
	f.Add(vectorPair, "", `{"_csrf":"`+vectorToken+`"}`)
	f.Add(vectorPair, "", "a=1&_csrf="+vectorToken)
	f.Add("__csrf_h=|e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", "", "_csrf=")
	f.Add("__csrf_h=j:5", "5", "")
	p, err := New(Options{Secret: testSecret, SignedIn: func(*http.Request) bool { return true }})
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, cookieHeader, header, body string) {
		for _, contentType := range []string{"application/json", "application/x-www-form-urlencoded"} {
			r := httptest.NewRequest("POST", "/api/x", strings.NewReader(body))
			r.Header["Cookie"] = []string{cookieHeader}
			r.Header["X-Csrf-Token"] = []string{header}
			r.Header.Set("Content-Type", contentType)
			passed := p.Check(r) == nil

			token, hash, complete := pairInCookie(cookieHeader)
			sent := sentToken(header, body, contentType)
			sum := sha256.Sum256([]byte(sent + testSecret))
			want := complete && token == sent && hash == hex.EncodeToString(sum[:])
			if passed != want {
				t.Fatalf("Check passed = %v, want %v: cookie %q, header %q, body %q (%s)", passed, want, cookieHeader, header, body, contentType)
			}
		}

		// Provider: the token handlers see always fits the cookie it sets
		get := httptest.NewRequest("GET", "/", nil)
		get.Header["Cookie"] = []string{cookieHeader}
		rec := httptest.NewRecorder()
		var token string
		p.Provider(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { token = Token(r) })).ServeHTTP(rec, get)
		set := rec.Result().Header.Values("Set-Cookie")
		if len(set) == 0 {
			t.Fatalf("Provider set no cookie for %q", cookieHeader)
		}
		last, _, _ := strings.Cut(set[len(set)-1], ";")
		value, _ := cookie.Lookup(last, "__csrf_h")
		sum := sha256.Sum256([]byte(token + testSecret))
		if value != token+"|"+hex.EncodeToString(sum[:]) {
			t.Fatalf("Provider set %q for token %q", last, token)
		}
	})
}

// pairInCookie reads the token and the hash from a Cookie header, written
// out step by step from the format: the first cookie of the name, taken
// apart at "|". A value j:<JSON text> stands for the text.
func pairInCookie(header string) (token, hash string, complete bool) {
	value, found := cookie.Lookup(header, "__csrf_h")
	if !found {
		return "", "", false
	}
	if rest, ok := strings.CutPrefix(value, "j:"); ok {
		var parsed any
		if json.Unmarshal([]byte(rest), &parsed) == nil {
			switch x := parsed.(type) {
			case string:
				if x != "" {
					value = x
				}
			case float64:
				if x != 0 {
					return "", "", false
				}
			case bool:
				if x {
					return "", "", false
				}
			case nil:
			default:
				return "", "", false
			}
		}
	}
	parts := strings.Split(value, "|")
	if len(parts) < 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// sentToken is the token a request sends: the header, else the body field.
func sentToken(header, body, contentType string) string {
	if sent := httpx.Latin1(header); sent != "" {
		return sent
	}
	if contentType == "application/json" {
		var fields map[string]any
		if json.Unmarshal([]byte(body), &fields) != nil {
			return ""
		}
		text, _ := fields["_csrf"].(string)
		return text
	}
	form, err := url.ParseQuery(body)
	if err != nil || len(form["_csrf"]) != 1 {
		return ""
	}
	return form["_csrf"][0]
}
