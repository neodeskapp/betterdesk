package csrf

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// The vector of the spec: a token and its hash under testSecret, as the
// Node console's library computed it.
const (
	testSecret  = "test-secret-0123456789abcdef0123456789abcdef"
	vectorToken = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"
	vectorHash  = "7795cf16fdd846350dba43cce9f1d7a0185c2b3f1abb6023031c754a2fdec3dd"
	vectorPair  = "__csrf_h=" + vectorToken + "%7C" + vectorHash
)

type fixture struct {
	t        *testing.T
	p        *Protector
	signedIn bool
	refused  int
}

func newFixture(t *testing.T, change ...func(*Options)) *fixture {
	t.Helper()
	f := &fixture{t: t}
	opts := Options{
		Secret:   testSecret,
		SignedIn: func(*http.Request) bool { return f.signedIn },
		OnError: func(w http.ResponseWriter, _ *http.Request) {
			f.refused++
			http.Error(w, "refused", http.StatusForbidden)
		},
	}
	for _, fn := range change {
		fn(&opts)
	}
	p, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	f.p = p
	return f
}

// provide runs a request through Provider and returns the token handlers
// see and the answer.
func (f *fixture) provide(r *http.Request) (string, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	token := "handler did not run"
	f.p.Provider(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token = Token(r)
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(rec, r)
	return token, rec
}

// gate runs a request through Gate and reports whether it passed.
func (f *fixture) gate(r *http.Request) bool {
	rec := httptest.NewRecorder()
	passed := false
	f.p.Gate(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { passed = true })).ServeHTTP(rec, r)
	if !passed && rec.Code != http.StatusForbidden {
		f.t.Fatalf("a refused request got status %d", rec.Code)
	}
	return passed
}

func req(method, path, cookie, header string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	if cookie != "" {
		r.Header.Set("Cookie", cookie)
	}
	if header != "" {
		r.Header.Set("X-CSRF-Token", header)
	}
	return r
}

func setCookies(rec *httptest.ResponseRecorder) []string {
	return rec.Result().Header.Values("Set-Cookie")
}

func TestHashVector(t *testing.T) {
	f := newFixture(t)
	if got := f.p.hash(vectorToken); got != vectorHash {
		t.Fatalf("hash = %s, want %s", got, vectorHash)
	}
	// A plain SHA-256 of token and secret joined, not an HMAC
	sum := sha256.Sum256([]byte(vectorToken + testSecret))
	if hex.EncodeToString(sum[:]) != vectorHash {
		t.Fatal("the vector is not sha256(token + secret)")
	}
}

func TestProviderMakesAPair(t *testing.T) {
	f := newFixture(t)
	token, rec := f.provide(req("GET", "/", "", ""))
	if !regexp.MustCompile(`^[0-9a-f]{128}$`).MatchString(token) {
		t.Fatalf("token %q is not 128 hex characters", token)
	}
	want := "__csrf_h=" + token + "%7C" + f.p.hash(token) + "; Path=/; HttpOnly; SameSite=Lax"
	if got := setCookies(rec); len(got) != 1 || got[0] != want {
		t.Fatalf("Set-Cookie:\n got %q\nwant %q", got, want)
	}
	other, _ := f.provide(req("GET", "/", "", ""))
	if other == token {
		t.Fatal("two visitors got the same token")
	}
}

func TestProviderHTTPSMode(t *testing.T) {
	f := newFixture(t, func(o *Options) { o.HTTPS, o.Secure = true, true })
	token, rec := f.provide(req("GET", "/", "", ""))
	want := "__csrf=" + token + "%7C" + f.p.hash(token) + "; Path=/; HttpOnly; Secure; SameSite=Lax"
	if got := setCookies(rec); len(got) != 1 || got[0] != want {
		t.Fatalf("Set-Cookie:\n got %q\nwant %q", got, want)
	}
	// HTTPS mode on plain HTTP (no usable certificate): the HTTPS name
	// without Secure
	fallback := newFixture(t, func(o *Options) { o.HTTPS = true })
	token, rec = fallback.provide(req("GET", "/", "", ""))
	want = "__csrf=" + token + "%7C" + fallback.p.hash(token) + "; Path=/; HttpOnly; SameSite=Lax"
	if got := setCookies(rec); len(got) != 1 || got[0] != want {
		t.Fatalf("fallback Set-Cookie:\n got %q\nwant %q", got, want)
	}
	// The HTTP name is not read in HTTPS mode, and the other way round
	if got, _ := f.provide(req("GET", "/", vectorPair, "")); got == vectorToken {
		t.Fatal("HTTPS mode reused a pair from __csrf_h")
	}
	http := newFixture(t)
	if got, _ := http.provide(req("GET", "/", "__csrf="+vectorToken+"%7C"+vectorHash, "")); got == vectorToken {
		t.Fatal("HTTP mode reused a pair from __csrf")
	}
}

func TestProviderReusesAGoodCookie(t *testing.T) {
	f := newFixture(t)
	for _, method := range []string{"GET", "HEAD", "OPTIONS"} {
		token, rec := f.provide(req(method, "/api/x", vectorPair, ""))
		if token != vectorToken {
			t.Fatalf("%s: token %q, want the cookie's", method, token)
		}
		want := vectorPair + "; Path=/; HttpOnly; SameSite=Lax"
		if got := setCookies(rec); len(got) != 1 || got[0] != want {
			t.Fatalf("%s: Set-Cookie %q, want %q", method, got, want)
		}
	}
	// The unencoded form and extra parts are taken, and the cookie is set
	// again as token|hash
	for _, cookie := range []string{
		"__csrf_h=" + vectorToken + "|" + vectorHash,
		"__csrf_h=" + vectorToken + "%7C" + vectorHash + "%7Cextra",
		`__csrf_h="` + vectorToken + "%7C" + vectorHash + `"`,
		`__csrf_h=j%3A%22` + vectorToken + "%7C" + vectorHash + `%22`,
	} {
		token, rec := f.provide(req("GET", "/", cookie, ""))
		if got := setCookies(rec); token != vectorToken || len(got) != 1 || got[0] != vectorPair+"; Path=/; HttpOnly; SameSite=Lax" {
			t.Errorf("cookie %q: token %q, Set-Cookie %q", cookie, token, got)
		}
	}
}

func TestProviderReplacesABrokenCookie(t *testing.T) {
	f := newFixture(t)
	wrongHash := strings.Repeat("0", 64)
	otherSecret := sha256.Sum256([]byte(vectorToken + "another-secret"))
	cases := map[string]string{
		"wrong hash":          vectorToken + "%7C" + wrongHash,
		"another secret":      vectorToken + "%7C" + hex.EncodeToString(otherSecret[:]),
		"no hash":             vectorToken,
		"empty":               "",
		"hash only":           "%7C" + vectorHash,
		"token changed":       "1" + vectorToken[1:] + "%7C" + vectorHash,
		"upper-case hash":     vectorToken + "%7C" + strings.ToUpper(vectorHash),
		"hash with a space":   vectorToken + "%7C" + vectorHash + "%20",
		"another delimiter":   vectorToken + "." + vectorHash,
		"JSON that is broken": "j%3A%7B",
		"JSON null":           "j%3Anull",
	}
	for name, value := range cases {
		token, rec := f.provide(req("GET", "/", "__csrf_h="+value, ""))
		got := setCookies(rec)
		if len(got) != 2 {
			t.Errorf("%s: Set-Cookie %q, want a clearing header and a new pair", name, got)
			continue
		}
		if got[0] != "__csrf_h=; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT; HttpOnly; SameSite=Lax" {
			t.Errorf("%s: clearing header %q", name, got[0])
		}
		if token == vectorToken || len(token) != 128 || got[1] != "__csrf_h="+token+"%7C"+f.p.hash(token)+"; Path=/; HttpOnly; SameSite=Lax" {
			t.Errorf("%s: token %q, cookie %q", name, token, got[1])
		}
	}
	https := newFixture(t, func(o *Options) { o.HTTPS, o.Secure = true, true })
	_, rec := https.provide(req("GET", "/", "__csrf=broken", ""))
	if got := setCookies(rec); len(got) != 2 || got[0] != "__csrf=; Path=/; Expires=Thu, 01 Jan 1970 00:00:00 GMT; HttpOnly; Secure; SameSite=Lax" {
		t.Errorf("HTTPS clearing header: %q", got)
	}
}

func TestProviderTreatsAJSONCookieThatIsNoTextAsMissing(t *testing.T) {
	f := newFixture(t)
	for _, value := range []string{"j%3A%7B%7D", "j%3A%5B1%5D", "j%3A5", "j%3Atrue"} {
		token, rec := f.provide(req("GET", "/", "__csrf_h="+value, ""))
		if got := setCookies(rec); len(got) != 1 || len(token) != 128 {
			t.Errorf("cookie %q: Set-Cookie %q; want a new pair and no clearing header", value, got)
		}
		if f.gate(req("POST", "/api/x", "__csrf_h="+value, "")) {
			t.Errorf("cookie %q passed the gate", value)
		}
	}
}

func TestProviderLeavesWritesAlone(t *testing.T) {
	f := newFixture(t)
	for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
		token, rec := f.provide(req(method, "/api/x", "__csrf_h=broken", ""))
		if token != "" || len(setCookies(rec)) != 0 {
			t.Errorf("%s: token %q, Set-Cookie %q; want neither", method, token, setCookies(rec))
		}
	}
	if Token(req("GET", "/", "", "")) != "" {
		t.Error("Token outside Provider is not empty")
	}
}

func TestCheck(t *testing.T) {
	f := newFixture(t)
	other := sha256.Sum256([]byte(vectorToken + "another-secret"))
	otherPair := "__csrf_h=" + vectorToken + "%7C" + hex.EncodeToString(other[:])
	cases := []struct {
		name, cookie, header string
		ok                   bool
	}{
		{"token in the header", vectorPair, vectorToken, true},
		{"raw cookie", "__csrf_h=" + vectorToken + "|" + vectorHash, vectorToken, true},
		{"no header", vectorPair, "", false},
		{"no cookie", "", vectorToken, false},
		{"neither", "", "", false},
		{"another token in the header", vectorPair, strings.Repeat("0", 128), false},
		{"token one character off", vectorPair, vectorToken[:127] + "0", false},
		{"token in upper case", vectorPair, strings.ToUpper(vectorToken), false},
		{"token with the hash", vectorPair, vectorToken + "|" + vectorHash, false},
		{"the hash as the token", vectorPair, vectorHash, false},
		{"pair signed with another secret", otherPair, vectorToken, false},
		{"the hash of the sent token next to another token", "__csrf_h=" + strings.Repeat("0", 128) + "%7C" + vectorHash, vectorToken, false},
		{"the cookie's token sent, its hash for another token", "__csrf_h=" + strings.Repeat("0", 128) + "%7C" + vectorHash, strings.Repeat("0", 128), false},
		{"cookie without a hash", "__csrf_h=" + vectorToken, vectorToken, false},
		{"cookie with a wrong hash", "__csrf_h=" + vectorToken + "%7C" + strings.Repeat("a", 64), vectorToken, false},
		{"cookie under the HTTPS name", "__csrf=" + vectorToken + "%7C" + vectorHash, vectorToken, false},
		{"second cookie of the name is good", "__csrf_h=bad; " + vectorPair, vectorToken, false},
		{"empty token and the hash of the secret alone, unknown to a client", "__csrf_h=%7C" + vectorHash, "", false},
	}
	for _, tc := range cases {
		for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
			err := f.p.Check(req(method, "/api/x", tc.cookie, tc.header))
			if (err == nil) != tc.ok {
				t.Errorf("%s (%s): Check = %v, want ok %v", tc.name, method, err, tc.ok)
			}
			if err != nil && err != ErrInvalidToken {
				t.Errorf("%s: error %v, want ErrInvalidToken", tc.name, err)
			}
		}
		for _, method := range []string{"GET", "HEAD", "OPTIONS"} {
			if err := f.p.Check(req(method, "/api/x", tc.cookie, tc.header)); err != nil {
				t.Errorf("%s: %s was refused", tc.name, method)
			}
		}
	}
	if ErrInvalidToken.Error() != "invalid csrf token" {
		t.Errorf("error text %q", ErrInvalidToken.Error())
	}
}

func TestCheckTakesTheHeaderTwiceAsOneValue(t *testing.T) {
	f := newFixture(t)
	r := req("POST", "/api/x", vectorPair, "")
	r.Header.Add("X-CSRF-Token", vectorToken)
	r.Header.Add("X-CSRF-Token", vectorToken)
	if f.p.Check(r) == nil {
		t.Fatal("two headers are one value joined with a comma, which is not the token")
	}
}

func TestCheckReadsTheBodyField(t *testing.T) {
	f := newFixture(t)
	post := func(contentType, body, header string) (*http.Request, error) {
		r := httptest.NewRequest("POST", "/api/x", strings.NewReader(body))
		r.Header.Set("Cookie", vectorPair)
		if contentType != "" {
			r.Header.Set("Content-Type", contentType)
		}
		if header != "" {
			r.Header.Set("X-CSRF-Token", header)
		}
		return r, f.p.Check(r)
	}
	cases := []struct {
		name, contentType, body, header string
		ok                              bool
	}{
		{"JSON field", "application/json", `{"a":1,"_csrf":"` + vectorToken + `"}`, "", true},
		{"JSON field with a charset", "application/json; charset=utf-8", `{"_csrf":"` + vectorToken + `"}`, "", true},
		{"form field", "application/x-www-form-urlencoded", "a=1&_csrf=" + vectorToken, "", true},
		{"wrong JSON field", "application/json", `{"_csrf":"` + vectorHash + `"}`, "", false},
		{"JSON field that is a number", "application/json", `{"_csrf":5}`, "", false},
		{"JSON array", "application/json", `["` + vectorToken + `"]`, "", false},
		{"form field twice", "application/x-www-form-urlencoded", "_csrf=" + vectorToken + "&_csrf=" + vectorToken, "", false},
		{"field in a text body", "text/plain", `{"_csrf":"` + vectorToken + `"}`, "", false},
		{"field without a content type", "", `{"_csrf":"` + vectorToken + `"}`, "", false},
		{"header wins over a good field", "application/json", `{"_csrf":"` + vectorToken + `"}`, "wrong", false},
		{"good header, wrong field", "application/json", `{"_csrf":"wrong"}`, vectorToken, true},
	}
	for _, tc := range cases {
		r, err := post(tc.contentType, tc.body, tc.header)
		if (err == nil) != tc.ok {
			t.Errorf("%s: Check = %v, want ok %v", tc.name, err, tc.ok)
		}
		// The handler still reads the whole body
		rest := new(strings.Builder)
		buf := make([]byte, 7)
		for {
			n, readErr := r.Body.Read(buf)
			rest.Write(buf[:n])
			if readErr != nil {
				break
			}
		}
		if rest.String() != tc.body {
			t.Errorf("%s: the body after Check is %q, want %q", tc.name, rest.String(), tc.body)
		}
	}

	// A body over the limit is not searched, and is still whole afterwards
	big := `{"pad":"` + strings.Repeat("x", bodyLimit) + `","_csrf":"` + vectorToken + `"}`
	r, err := post("application/json", big, "")
	if err == nil {
		t.Error("a field beyond the body limit was found")
	}
	n := 0
	buf := make([]byte, 64<<10)
	for {
		m, readErr := r.Body.Read(buf)
		n += m
		if readErr != nil {
			break
		}
	}
	if n != len(big) {
		t.Errorf("%d of %d body bytes left after Check", n, len(big))
	}
}

func TestGate(t *testing.T) {
	f := newFixture(t)
	if !f.gate(req("POST", "/api/devices/1/ban", vectorPair, vectorToken)) {
		t.Fatal("a write with the token was refused")
	}
	if f.refused != 0 {
		t.Fatalf("OnError ran %d times", f.refused)
	}
	for _, signedIn := range []bool{false, true} {
		f.signedIn = signedIn
		before := f.refused
		if f.gate(req("POST", "/api/devices/1/ban", "", "")) {
			t.Fatalf("signed in %v: a write without the token passed", signedIn)
		}
		if f.gate(req("PUT", "/settings", vectorPair, "")) {
			t.Fatalf("signed in %v: a write outside /api passed without the token", signedIn)
		}
		if f.refused != before+2 {
			t.Fatalf("OnError ran %d times for two refusals", f.refused-before)
		}
		if !f.gate(req("GET", "/api/devices", "", "")) || !f.gate(req("OPTIONS", "/api/devices", "", "")) || !f.gate(req("HEAD", "/", "__csrf_h=broken", "")) {
			t.Fatalf("signed in %v: a read was refused", signedIn)
		}
	}
}

func TestGateDeviceAPIException(t *testing.T) {
	f := newFixture(t)
	// A device agent: no session, a bearer token, no CSRF token
	f.signedIn = false
	for _, path := range []string{"/api/bd/commands", "/api/bd/", "/api/bd/a/b?x=1"} {
		if !f.gate(req("POST", path, "", "")) {
			t.Errorf("%s without a session was checked", path)
		}
	}
	for _, path := range []string{"/api/bd", "/api/bdx/commands", "/API/BD/commands", "/api/%62d/commands", "/x/api/bd/commands", "/api/devices"} {
		if f.gate(req("POST", path, "", "")) {
			t.Errorf("%s without a session passed unchecked", path)
		}
	}
	// A signed-in browser on the same paths is checked
	f.signedIn = true
	if f.gate(req("POST", "/api/bd/commands", "", "")) {
		t.Fatal("a signed-in browser posted to /api/bd/ without the token")
	}
	if f.gate(req("POST", "/api/bd/commands", vectorPair, "wrong")) {
		t.Fatal("a signed-in browser posted to /api/bd/ with a wrong token")
	}
	if !f.gate(req("POST", "/api/bd/commands", vectorPair, vectorToken)) {
		t.Fatal("a signed-in browser with the token was refused")
	}
}

// TestABearerWriteWithoutASessionPasses: a write with a bearer token in
// its Authorization header and no session passes without a CSRF token on
// any path, so a device reaches its routes outside /api/bd/ and in any
// spelling of it. A browser cannot send that header to another site
// without asking first, and without a session there is nothing to forge.
// With a session the write is checked (BUGS DV2, DV9).
func TestABearerWriteWithoutASessionPasses(t *testing.T) {
	f := newFixture(t)
	bearer := func(path, authorization, cookie, token string) *http.Request {
		r := req("POST", path, cookie, token)
		if authorization != "" {
			r.Header.Set("Authorization", authorization)
		}
		return r
	}
	f.signedIn = false
	for _, path := range []string{"/api/tickets/bd", "/api/dataguard/dlp-events", "/api/activity/activity", "/API/BD/Activity/", "/api/bd", "/api/devices"} {
		if !f.gate(bearer(path, "Bearer tok-device-a", "", "")) {
			t.Errorf("%s with a bearer token and no session was checked", path)
		}
	}
	for _, authorization := range []string{"", "Basic dG9rOng=", "bearer tok-device-a", "Token Bearer x"} {
		if f.gate(bearer("/api/tickets/bd", authorization, "", "")) {
			t.Errorf("/api/tickets/bd with Authorization %q and no session passed unchecked", authorization)
		}
	}
	f.signedIn = true
	if f.gate(bearer("/api/tickets/bd", "Bearer tok-device-a", "", "")) {
		t.Error("a signed-in browser with a bearer token wrote without the CSRF token")
	}
	if !f.gate(bearer("/api/tickets/bd", "Bearer tok-device-a", vectorPair, vectorToken)) {
		t.Error("a signed-in browser with a bearer token and the CSRF token was refused")
	}
}

func TestProviderThenGate(t *testing.T) {
	f := newFixture(t)
	token, rec := f.provide(req("GET", "/api/auth/login-page", "", ""))
	pair, _, _ := strings.Cut(setCookies(rec)[0], ";")
	if !f.gate(req("POST", "/api/auth/login", pair, token)) {
		t.Fatal("the token Provider handed out was refused")
	}
	// Under another secret the same pair is worth nothing
	other := newFixture(t, func(o *Options) { o.Secret = "another-secret" })
	if other.gate(req("POST", "/api/auth/login", pair, token)) {
		t.Fatal("a pair made with another secret passed")
	}
	if got, _ := other.provide(req("GET", "/", pair, "")); got == token {
		t.Fatal("a pair made with another secret was reused")
	}
}

func TestNewRefusesBadOptions(t *testing.T) {
	signedIn := func(*http.Request) bool { return false }
	if _, err := New(Options{SignedIn: signedIn}); err == nil {
		t.Error("New took an empty secret")
	}
	if _, err := New(Options{Secret: "x"}); err == nil {
		t.Error("New took no SignedIn")
	}
	p, err := New(Options{Secret: "x", SignedIn: signedIn})
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	p.Gate(http.NotFoundHandler()).ServeHTTP(rec, req("POST", "/", "", ""))
	if rec.Code != http.StatusForbidden {
		t.Errorf("default refusal: status %d, want 403", rec.Code)
	}
}

// With Options.BodyToken the protector takes the body field from the hook
// and does not read r.Body: something in front of it has read the body.
func TestBodyTokenHook(t *testing.T) {
	asked, field := 0, vectorToken
	f := newFixture(t, func(o *Options) {
		o.BodyToken = func(*http.Request) string {
			asked++
			return field
		}
	})
	post := func(body, header string) *http.Request {
		r := httptest.NewRequest("POST", "/api/x", strings.NewReader(body))
		r.Header.Set("Cookie", vectorPair)
		r.Header.Set("Content-Type", "application/json")
		if header != "" {
			r.Header.Set("X-CSRF-Token", header)
		}
		return r
	}
	inBody := `{"_csrf":"` + vectorToken + `"}`

	// The body is read to its end, as behind the pipeline's body step
	r := post(inBody, "")
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		t.Fatal(err)
	}
	if err := f.p.Check(r); err != nil {
		t.Errorf("Check with the token from the hook: %v", err)
	}
	r = post(inBody, "")
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		t.Fatal(err)
	}
	if !f.gate(r) {
		t.Error("Gate refused the token from the hook")
	}
	if asked != 2 {
		t.Errorf("the hook was asked %d times for two writes", asked)
	}

	// The hook is the only source: a good field in r.Body does not count
	field = ""
	r = post(inBody, "")
	if err := f.p.Check(r); err == nil {
		t.Error("the protector read r.Body although it has a hook")
	}
	if left, _ := io.ReadAll(r.Body); string(left) != inBody {
		t.Errorf("the body after Check is %q, want it untouched", left)
	}
	field = "wrong"
	if f.gate(post(inBody, "")) {
		t.Error("Gate passed a wrong token from the hook")
	}

	// The header comes first, and the hook is not asked next to it
	asked, field = 0, vectorToken
	if err := f.p.Check(post(inBody, "wrong")); err == nil {
		t.Error("a wrong header passed because the hook had the token")
	}
	field = "wrong"
	if err := f.p.Check(post(inBody, vectorToken)); err != nil {
		t.Errorf("a good header next to a wrong hook: %v", err)
	}
	if err := f.p.Check(req("GET", "/api/x", vectorPair, "")); err != nil {
		t.Errorf("GET: %v", err)
	}
	if asked != 0 {
		t.Errorf("the hook was asked %d times where the header or the method decides", asked)
	}
}

// Behind a TLS proxy in HTTPS mode without a certificate the cookie is
// Secure for a request that came over HTTPS, as the session cookie is
// (RV124).
func TestProviderSecureBehindATLSProxy(t *testing.T) {
	f := newFixture(t, func(o *Options) {
		o.HTTPS = true
		o.SecureRequest = func(r *http.Request) bool { return r.Header.Get("X-Forwarded-Proto") == "https" }
	})
	r := req("GET", "/", "", "")
	r.Header.Set("X-Forwarded-Proto", "https")
	token, rec := f.provide(r)
	want := "__csrf=" + token + "%7C" + f.p.hash(token) + "; Path=/; HttpOnly; Secure; SameSite=Lax"
	if got := setCookies(rec); len(got) != 1 || got[0] != want {
		t.Fatalf("over HTTPS:\n got %q\nwant %q", got, want)
	}
	token, rec = f.provide(req("GET", "/", "", ""))
	want = "__csrf=" + token + "%7C" + f.p.hash(token) + "; Path=/; HttpOnly; SameSite=Lax"
	if got := setCookies(rec); len(got) != 1 || got[0] != want {
		t.Fatalf("over plain HTTP:\n got %q\nwant %q", got, want)
	}
}
