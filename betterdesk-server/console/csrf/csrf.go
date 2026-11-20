// Package csrf protects the console's writes against cross-site request
// forgery with a double-submit token, in the format the console has always
// used, so a token handed out by the Node console is good in Go and the
// other way round.
//
// The format in plain words: the server makes a token of 64 random bytes
// (128 hex characters) and a hash, the SHA-256 of the token followed by the
// secret, in hex. It is a plain hash of the joined text, not an HMAC, and
// nothing ties it to a session or a person. Both go into one cookie,
//
//	__csrf_h=<token>|<hash>       HTTP setup
//	__csrf=<token>|<hash>         HTTPS setup (HTTPS_ENABLED=true), with Secure
//	                              when the console serves HTTPS
//
// with "|" written as %7C, Path=/, HttpOnly and SameSite=Lax, and no
// expiry. The page cannot read the cookie; it gets the token from the API
// and sends it back in the X-CSRF-Token header (or the field _csrf of a
// JSON or form body). A write passes when the token it sends is the one in
// the cookie and the hash in the cookie fits that token.
//
// Every GET, HEAD and OPTIONS request that passes Provider sets the cookie
// again: with the same pair when the cookie is good, with a new pair when
// it is missing or broken.
package csrf

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/session/cookie"
)

// ErrInvalidToken is what Check returns for a write without a good token.
var ErrInvalidToken = errors.New("invalid csrf token")

const (
	cookieNameHTTPS = "__csrf"
	cookieNameHTTP  = "__csrf_h"
	// tokenBytes is the size of a token before hex encoding.
	tokenBytes = 64
	// bodyLimit is how much of a body is read to find the _csrf field.
	bodyLimit = 2 << 20
	// devicePrefix is where device agents call in, some routes without a
	// bearer token (registration).
	devicePrefix = "/api/bd/"
)

// Options configure a Protector.
type Options struct {
	// Secret is the console's session secret.
	Secret string
	// HTTPS says the console is set up for HTTPS (HTTPS_ENABLED=true). It
	// picks the cookie's name.
	HTTPS bool
	// Secure marks the cookie Secure: set it when the console serves
	// HTTPS. In HTTPS mode without a usable certificate the console serves
	// plain HTTP, and the cookie keeps its HTTPS name without Secure.
	Secure bool
	// SignedIn reports whether a request carries a signed-in session. Gate
	// asks it for requests under /api/bd/ and requests with a bearer token.
	SignedIn func(r *http.Request) bool
	// BodyToken returns the _csrf field of a request's body, "" when the
	// body has none. Set it where something in front of the protector has
	// read the body. nil makes the protector read the field from r.Body
	// itself.
	BodyToken func(r *http.Request) string
	// OnError answers a request that Gate refused. nil answers 403 with a
	// plain text.
	OnError func(w http.ResponseWriter, r *http.Request)
}

// Protector hands out and checks tokens.
type Protector struct {
	secret     string
	cookieName string
	secure     bool
	signedIn   func(r *http.Request) bool
	bodyToken  func(r *http.Request) string
	onError    func(w http.ResponseWriter, r *http.Request)
}

// New returns a Protector.
func New(opts Options) (*Protector, error) {
	if opts.Secret == "" {
		return nil, errors.New("csrf: the secret is empty")
	}
	if opts.SignedIn == nil {
		return nil, errors.New("csrf: SignedIn is not set")
	}
	p := &Protector{
		secret:     opts.Secret,
		cookieName: cookieNameHTTP,
		secure:     opts.Secure,
		signedIn:   opts.SignedIn,
		bodyToken:  opts.BodyToken,
		onError:    opts.OnError,
	}
	if p.bodyToken == nil {
		p.bodyToken = bodyToken
	}
	if opts.HTTPS {
		p.cookieName = cookieNameHTTPS
	}
	if p.onError == nil {
		p.onError = func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, ErrInvalidToken.Error(), http.StatusForbidden)
		}
	}
	return p, nil
}

type contextKey struct{}

// Token is the token for the page to send back with its writes. It is set
// behind Provider on GET, HEAD and OPTIONS requests and "" otherwise.
func Token(r *http.Request) string {
	token, _ := r.Context().Value(contextKey{}).(string)
	return token
}

// Provider gives GET, HEAD and OPTIONS requests a token and sets the
// cookie: the pair from the request's cookie when that is good, a new pair
// otherwise. A broken cookie is cleared first. Other methods pass
// untouched.
func (p *Protector) Provider(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !safeMethod(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		token, hash, ok := "", "", false
		if value, found := p.cookie(r); found {
			token, hash, ok = splitPair(value)
			ok = ok && p.fits(token, hash)
			if !ok {
				cookie.Clear(w, p.cookieName, p.attributes())
			}
		}
		if !ok {
			var err error
			if token, err = newToken(); err == nil {
				hash = p.hash(token)
			}
		}
		if token != "" || ok {
			cookie.Set(w, p.cookieName, token+"|"+hash, p.attributes())
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKey{}, token)))
	})
}

// Gate refuses writes without a good token through OnError. A request
// without a signed-in session passes unchecked when its path starts with
// /api/bd/, where device agents call in, or when it carries a bearer token
// in its Authorization header, on any path: device agents send one, a
// browser cannot send it to another site without asking first, and
// without a session there is nothing to forge. With a session a request is
// a browser and is checked like any other.
func (p *Protector) Gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (strings.HasPrefix(r.URL.EscapedPath(), devicePrefix) || bearer(r)) && !p.signedIn(r) {
			next.ServeHTTP(w, r)
			return
		}
		if err := p.Check(r); err != nil {
			p.onError(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Check reports whether a request may go on: GET, HEAD and OPTIONS always,
// any other method only with the cookie's token in the X-CSRF-Token header
// or, when the header is empty, in the _csrf field of the body. It returns
// ErrInvalidToken otherwise.
func (p *Protector) Check(r *http.Request) error {
	if safeMethod(r.Method) {
		return nil
	}
	value, found := p.cookie(r)
	if !found {
		return ErrInvalidToken
	}
	token, hash, ok := splitPair(value)
	sent := p.requestToken(r)
	if !ok || subtle.ConstantTimeCompare([]byte(token), []byte(sent)) != 1 || !p.fits(sent, hash) {
		return ErrInvalidToken
	}
	return nil
}

// bearer reports whether the request's Authorization header holds a bearer
// token: it starts with "Bearer", as the device guards want it.
func bearer(r *http.Request) bool {
	return strings.HasPrefix(r.Header.Get("Authorization"), "Bearer")
}

func safeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

func (p *Protector) attributes() cookie.Attributes {
	return cookie.Attributes{Path: "/", HTTPOnly: true, Secure: p.secure, SameSite: "lax"}
}

// cookie reads the request's token cookie. A value of the form j:<JSON>
// counts as the JSON's value when that is a non-empty string, and as no
// cookie when it is an object, an array, a number other than 0 or true.
func (p *Protector) cookie(r *http.Request) (value string, found bool) {
	value, found = cookie.Get(r, p.cookieName)
	if !found || !strings.HasPrefix(value, "j:") {
		return value, found
	}
	var parsed any
	if json.Unmarshal([]byte(value[2:]), &parsed) != nil {
		return value, true
	}
	switch x := parsed.(type) {
	case nil:
		return value, true
	case string:
		if x == "" {
			return value, true
		}
		return x, true
	case bool:
		return value, !x
	case float64:
		return value, x == 0
	}
	return "", false
}

// splitPair takes a cookie value apart at "|". ok is false when there is
// no hash part.
func splitPair(value string) (token, hash string, ok bool) {
	parts := strings.Split(value, "|")
	if len(parts) < 2 {
		return parts[0], "", false
	}
	return parts[0], parts[1], true
}

func newToken() (string, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

// hash is the hex SHA-256 of the token followed by the secret.
func (p *Protector) hash(token string) string {
	sum := sha256.Sum256([]byte(token + p.secret))
	return hex.EncodeToString(sum[:])
}

func (p *Protector) fits(token, hash string) bool {
	return subtle.ConstantTimeCompare([]byte(p.hash(token)), []byte(hash)) == 1
}

// requestToken is the token a write sends: the X-CSRF-Token header, or the
// _csrf field of the body when the header is empty.
func (p *Protector) requestToken(r *http.Request) string {
	if header := httpx.Latin1(strings.Join(r.Header.Values("X-CSRF-Token"), ", ")); header != "" {
		return header
	}
	return p.bodyToken(r)
}

// bodyToken reads the _csrf field of a JSON or form body and puts the body
// back, so the handler reads it from the start. It is what a Protector
// without Options.BodyToken uses.
func bodyToken(r *http.Request) string {
	if r.Body == nil || r.Body == http.NoBody || r.Header.Get("Content-Encoding") != "" {
		return ""
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || (mediaType != "application/json" && mediaType != "application/x-www-form-urlencoded") {
		return ""
	}
	head, err := io.ReadAll(io.LimitReader(r.Body, bodyLimit+1))
	r.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(head), r.Body), r.Body}
	if err != nil || len(head) > bodyLimit {
		return ""
	}
	if mediaType == "application/json" {
		var fields map[string]json.RawMessage
		var token string
		if json.Unmarshal(head, &fields) != nil || json.Unmarshal(fields["_csrf"], &token) != nil {
			return ""
		}
		return token
	}
	form, err := url.ParseQuery(string(head))
	if err != nil || len(form["_csrf"]) != 1 {
		return ""
	}
	return form.Get("_csrf")
}
