package contract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// turnWith is a turn with the given variables and no secrets.
func turnWith(vars variables) *turn {
	tn := newTurn(nil)
	if vars != nil {
		tn.vars = vars
	}
	return tn
}

// writeSecrets makes a secrets folder with the files given.
func writeSecrets(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoadSecrets(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	dir := writeSecrets(t, map[string]string{"device_token": token + "\n", "pin": "1234"})
	if err := os.Mkdir(filepath.Join(dir, "folder"), 0o700); err != nil {
		t.Fatal(err)
	}
	set, err := loadSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	if value, err := set.file("device_token"); err != nil || value != token {
		t.Errorf("file(device_token) = %q, %v; want the file's content without the line break", value, err)
	}
	for name, want := range map[string]string{"pin": "fewer than 8 characters", "nothing": "there is no file " + filepath.ToSlash(dir) + "/nothing", "folder": "no file"} {
		if _, err := set.file(name); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("file(%s) = %v, want an error that says %q", name, err, want)
		}
	}
	if got := set.mask("a " + token + " b 1234"); got != "a {secret:device_token} b 1234" {
		t.Errorf("mask = %q: the file's value is masked, a value too short to be a secret is not", got)
	}

	empty, err := loadSecrets(filepath.Join(dir, "missing"))
	if err != nil || len(empty.names) != 0 {
		t.Errorf("a folder that does not exist: %v, %d secrets; want none and no error", err, len(empty.names))
	}
	var none *secretSet
	if none.mask("x") != "x" || none.foundIn([]byte("x")) != nil {
		t.Error("a nil set must mask nothing and find nothing")
	}
	if _, err := none.file("device_token"); err == nil {
		t.Error("a nil set has no files")
	}
}

func TestSecretMasking(t *testing.T) {
	set, _ := loadSecrets("")
	for name, value := range map[string]string{"short": "abcdefgh", "long": "abcdefgh-and-more", "other": `a"b\c/d=ef`} {
		if err := set.add(name, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := set.add("again", "abcdefgh"); err != nil {
		t.Fatal(err)
	}
	if err := set.add("tiny", "1234567"); err == nil || strings.Contains(err.Error(), "1234567") {
		t.Errorf("a value of 7 characters: %v; want an error that does not show the value", err)
	}
	cases := map[string]string{
		"abcdefgh-and-more":         "{secret:long}",
		"xabcdefghx":                "x{secret:short}x",
		"abcdefgh abcdefgh-and-mor": "{secret:short} {secret:short}-and-mor",
		`path a"b\c/d=ef/x`:         "path {secret:other}/x",
		"nothing here":              "nothing here",
	}
	for in, want := range cases {
		if got := set.mask(in); got != want {
			t.Errorf("mask(%q) = %q, want %q", in, got, want)
		}
	}
	// A golden file holds "other" as JSON writes it: a\"b\\c/d=ef
	found := set.foundIn([]byte(`{"a":"x abcdefgh-and-more","b":"a\"b\\c/d=ef"}`))
	if strings.Join(found, ",") != "long,other,short" {
		t.Errorf("foundIn = %v, want long, other and short (also as JSON writes them)", found)
	}
	// and "html" as it is: a golden does not escape < > &
	if err := set.add("html", "a&b<c>d=ef"); err != nil {
		t.Fatal(err)
	}
	if found := set.foundIn([]byte(`{"b":"a&b<c>d=ef"}`)); strings.Join(found, ",") != "html" {
		t.Errorf("foundIn = %v, want html", found)
	}
	if found := set.foundIn([]byte(`{"a":"{secret:long}"}`)); len(found) != 0 {
		t.Errorf("foundIn of a masked text = %v, want nothing", found)
	}
}

// An answer is masked before anything else looks at it: the JSON (also
// where the console escaped a character of the value), the headers, and a
// text body before its hash is taken.
func TestAnswersAreMasked(t *testing.T) {
	const token = "tok/abcdef0123456789"
	set, _ := loadSecrets("")
	if err := set.add("device_token", token); err != nil {
		t.Fatal(err)
	}
	n := NewNormalizer("http://bdgo-c1:5000", "")
	n.secrets = set

	header := http.Header{"Content-Type": {"application/json"}, "Location": {"/next?token=" + token}}
	a := NewAnswer(200, header, []byte(`{"token":"`+token+`","escaped":"tok\/abcdef0123456789","tok\/abcdef0123456789":1,"at":"http://bdgo-c1:5000/x"}`), false, n)
	want := `{"at":"<base>/x","escaped":"{secret:device_token}","token":"{secret:device_token}","{secret:device_token}":1}`
	if string(a.Body.JSON) != want {
		t.Errorf("body = %s\nwant %s", a.Body.JSON, want)
	}
	if got := a.Headers["location"]; len(got) != 1 || got[0] != "/next?token={secret:device_token}" {
		t.Errorf("location = %q", got)
	}

	page := NewAnswer(200, http.Header{"Content-Type": {"text/plain"}}, []byte("token "+token), false, n)
	masked := NewAnswer(200, http.Header{"Content-Type": {"text/plain"}}, []byte("token {secret:device_token}"), false, nil)
	if page.Body.SHA256 != masked.Body.SHA256 || page.Body.Head != "token {secret:device_token}" {
		t.Errorf("text body = %+v; want the hash and head of the masked text", page.Body)
	}
}

func TestCaptureFromHeadersAndCookies(t *testing.T) {
	// The answer as the suite keeps it has the instance's address replaced;
	// a secret is taken as the console sent it
	answer := jsonAnswer(200, `{"data":{"token":"<host>/from-the-body-0001","n":12345678901}}`)
	answer.raw = &rawAnswer{status: 200, body: []byte(`{"data":{"token":"bdgo-c1:5000/from-the-body-0001","n":12345678901}}`), header: http.Header{
		"Content-Type": {"application/json"},
		"X-Api-Token":  {"from-a-header-0002"},
		"Set-Cookie":   {"other=1; Path=/", "issued=from-a-cookie-0003; Path=/; HttpOnly"},
	}}
	c := Case{
		Name: "x", Path: "/x",
		Capture: map[string]string{"header": "header:x-api-token", "cookie": "cookie:issued", "missing": "header:X-None", "gone": "cookie:none"},
		CaptureSecret: map[string]string{
			"body": "data.token", "fromHeader": "header:X-Api-Token", "fromCookie": "cookie:issued", "number": "data.n",
		},
	}
	run, _ := loadSecrets("")
	tn := newTurn(run)
	if err := c.capture(answer, tn); err != nil {
		t.Fatal(err)
	}
	if tn.vars["header"] != "from-a-header-0002" || tn.vars["cookie"] != "from-a-cookie-0003" || len(tn.vars) != 2 {
		t.Errorf("variables = %v", tn.vars)
	}
	wantSecrets := map[string]string{"body": "bdgo-c1:5000/from-the-body-0001", "fromHeader": "from-a-header-0002", "fromCookie": "from-a-cookie-0003", "number": "12345678901"}
	for name, value := range wantSecrets {
		if tn.secrets[name] != value {
			t.Errorf("secret %s = %q, want %q", name, tn.secrets[name], value)
		}
	}
	if got := run.mask("bdgo-c1:5000/from-the-body-0001 12345678901"); got != "{secret:body} {secret:number}" {
		t.Errorf("a captured secret is not known to the run: %q", got)
	}

	short := Case{Name: "x", Path: "/x", CaptureSecret: map[string]string{"pin": "data.pin"}}
	err := short.capture(jsonAnswer(200, `{"data":{"pin":"4711"}}`), newTurn(run))
	if err == nil || strings.Contains(err.Error(), "4711") {
		t.Errorf("a secret of 4 characters: %v; want an error that does not show it", err)
	}
	listed := Case{Name: "x", Path: "/x", CaptureSecret: map[string]string{"list": "data"}}
	if err := listed.capture(jsonAnswer(200, `{"data":["a"]}`), newTurn(run)); err == nil {
		t.Error("a secret that is a list must be refused")
	}
}

// The client keeps what the console sent, so a header off the allowlist
// and a cookie's value can be captured.
func TestCaptureThroughTheClient(t *testing.T) {
	client, _ := newTestClient(t, false)
	answer := client.NewSession(SignedOut).Do(Request{Method: "GET", Path: "/rotate-csrf"})
	if answer.Headers["set-cookie"][0] != "__csrf_h=<value>; Path=/" {
		t.Fatalf("set-cookie = %q", answer.Headers["set-cookie"])
	}
	c := Case{Name: "x", Path: "/x", Capture: map[string]string{"csrf": "cookie:__csrf_h", "type": "header:Content-Type"}}
	tn := newTurn(nil)
	if err := c.capture(answer, tn); err != nil {
		t.Fatal(err)
	}
	if tn.vars["csrf"] != "c2" || tn.vars["type"] != "application/json; charset=utf-8" {
		t.Errorf("variables = %v; want the cookie's value and the header as sent", tn.vars)
	}
}

func TestSecretsInRequests(t *testing.T) {
	run := &secretSet{files: map[string]string{"device_token": "file-token-000000"}, short: map[string]bool{}, names: map[string]string{}}
	tn := newTurn(run)
	tn.vars["id"] = json.Number("7")
	tn.secrets["api"] = "captured-token-1111"
	c := Case{
		Name: "x", Method: "POST", Path: "/api/x/{id}/{secret:device_token}",
		Query:   url.Values{"t": {"{secret:api}"}},
		Headers: map[string]string{"Authorization": "Bearer {secret:device_token}"},
		Body:    map[string]any{"token": "{secret:api}", "id": "{id}", "both": "{id}:{secret:api}"},
	}
	r, err := c.request(tn)
	if err != nil {
		t.Fatal(err)
	}
	if r.Path != "/api/x/7/file-token-000000?t=captured-token-1111" || r.Header.Get("Authorization") != "Bearer file-token-000000" {
		t.Errorf("request = %s, Authorization %q", r.Path, r.Header.Get("Authorization"))
	}
	if want := `{"both":"7:captured-token-1111","id":7,"token":"captured-token-1111"}`; string(r.Body) != want {
		t.Errorf("body = %s, want %s", r.Body, want)
	}
	info := c.info()
	if info.Path != "/api/x/{id}/{secret:device_token}?t=%7Bsecret%3Aapi%7D" || info.Notes[0] != "header Authorization: Bearer {secret:device_token}" {
		t.Errorf("info = %+v: the golden must hold the placeholders", info)
	}

	missing := Case{Name: "x", Path: "/x", Headers: map[string]string{"Authorization": "Bearer {secret:nobody}"}}
	if _, err := missing.request(tn); err == nil || !strings.Contains(err.Error(), "secret nobody") {
		t.Errorf("a secret nothing gave: %v", err)
	}
}

// secretConsole is a console that answers with what it was sent and hands
// out tokens: POST /api/echo answers the Authorization header and the
// body, and points Location at a path with the token; POST /api/issue
// makes a new token each time; GET /api/escaped answers the token of the
// last echo with its slashes escaped, as some JSON encoders write them.
type secretConsole struct {
	issued   int
	lastAuth string
	seen     []string
}

func newSecretSuite(t *testing.T, mode RecordMode, goldenDir, secretsDir string) (*Suite, *secretConsole) {
	t.Helper()
	console := &secretConsole{}
	fake := &fakeConsole{t: t, password: "the-password"}
	plumbing := fake.handler()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		answer := func(v any) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_ = json.NewEncoder(w).Encode(v)
		}
		switch {
		case r.URL.Path == "/__nd/routing":
			answer(map[string]string{"handler": HandlerNode})
		case r.URL.Path == "/api/echo":
			body, _ := io.ReadAll(r.Body)
			auth := r.Header.Get("Authorization")
			console.lastAuth = auth
			console.seen = append(console.seen, auth+" "+string(body)+" "+r.URL.RawQuery)
			w.Header().Set("Location", "/next?token="+strings.TrimPrefix(auth, "Bearer "))
			answer(map[string]any{"auth": auth, "body": json.RawMessage(body)})
		case r.URL.Path == "/api/issue":
			console.issued++
			token := fmt.Sprintf("issued-%d-%s", console.issued, strings.Repeat("x", 16))
			answer(map[string]any{"data": map[string]any{"token": token}, "note": "keep it"})
		case r.URL.Path == "/api/escaped":
			w.Header().Set("Content-Type", "application/json")
			escaped := strings.ReplaceAll(strings.TrimPrefix(console.lastAuth, "Bearer "), "/", `\/`)
			_, _ = io.WriteString(w, `{"echo":"`+escaped+`"}`)
		default:
			plumbing.ServeHTTP(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	passwordFile := filepath.Join(t.TempDir(), "e2e_password")
	if err := os.WriteFile(passwordFile, []byte("the-password\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, _ := url.Parse(srv.URL)
	settings := &Settings{BaseURL: base, PasswordFile: passwordFile, SecretsDir: secretsDir, Timeout: 2 * time.Second, Record: mode, GoldenDir: goldenDir}
	client, err := NewClient(settings)
	if err != nil {
		t.Fatal(err)
	}
	s := &Suite{
		Settings: settings, Client: client, Report: newReport(settings), Now: time.Now(),
		sessions: map[Role]*Session{}, goldens: map[string]*Golden{},
	}
	s.Report.redact = client.secrets.mask
	return s, console
}

func secretGroup() *CaseGroup {
	var group *CaseGroup
	withGroups(func() {
		Group("secrets",
			Case{
				Name: "echo", Method: "POST", Path: "/api/echo", Roles: Roles("admin"),
				Query:   url.Values{"t": {"{secret:device_token}"}},
				Headers: map[string]string{"Authorization": "Bearer {secret:device_token}"},
				Body:    map[string]any{"token": "{secret:device_token}", "n": 1},
			},
			Case{Name: "escaped", Path: "/api/escaped", Roles: Roles("admin")},
			Case{Name: "issue", Method: "POST", Path: "/api/issue", Roles: Roles("admin"), CaptureSecret: map[string]string{"api": "data.token"}},
			Case{Name: "use", Method: "POST", Path: "/api/echo", Roles: Roles("admin"), Headers: map[string]string{"Authorization": "Bearer {secret:api}"}, Body: map[string]any{}},
		)
		group = Groups()[0]
	})
	return group
}

// A case sends secrets from the secrets folder and from an earlier answer.
// The goldens hold their names only, and two instances with other tokens
// record and compare equal.
func TestCasesSendSecretsAndGoldensHoldTheirNames(t *testing.T) {
	dir := t.TempDir()
	tokens := []string{"dev/token-AAAA-0001", "dev/token-BBBB-0002", "dev/token-CCCC-0003"}
	for i, mode := range []RecordMode{RecordFirst, RecordMerge, Compare} {
		secrets := writeSecrets(t, map[string]string{"device_token": tokens[i]})
		s, console := newSecretSuite(t, mode, dir, secrets)
		s.runGroup(t, secretGroup())
		if len(console.seen) != 2 || !strings.HasPrefix(console.seen[0], "Bearer "+tokens[i]+` {"n":1,"token":"`+tokens[i]+`"} t=`+url.QueryEscape(tokens[i])) ||
			!strings.HasPrefix(console.seen[1], "Bearer issued-1-") {
			t.Fatalf("mode %d: the console got %q; want the token of the file, then the issued one", mode, console.seen)
		}
		if mode == Compare {
			continue
		}
		for _, name := range []string{"echo", "escaped", "issue", "use"} {
			data, err := os.ReadFile(goldenFile(dir, "cases", "secrets", name))
			if err != nil {
				t.Fatalf("mode %d: %v", mode, err)
			}
			for _, leak := range append(append([]string{}, tokens...), "issued-1-", `dev\/token`) {
				if bytes.Contains(data, []byte(leak)) {
					t.Errorf("mode %d: golden %s holds %q:\n%s", mode, name, leak, data)
				}
			}
			golden, _ := LoadGolden(goldenFile(dir, "cases", "secrets", name))
			if golden.Recordings != int(mode) || len(golden.Answers) != 1 || len(golden.Answers[0].Volatile) != 0 {
				t.Errorf("mode %d: golden %s has %d recordings and the marks %v; the masked values must be equal in both", mode, name, golden.Recordings, golden.Answers[0].Volatile)
			}
		}
		echo, _ := LoadGolden(goldenFile(dir, "cases", "secrets", "echo"))
		answer, _ := echo.For("admin")
		if want := `{"auth":"Bearer {secret:device_token}","body":{"n":1,"token":"{secret:device_token}"}}`; compactJSON(answer.Body.JSON) != want {
			t.Errorf("mode %d: echo body = %s, want %s", mode, answer.Body.JSON, want)
		}
		if loc := answer.Headers["location"]; len(loc) != 1 || loc[0] != "/next?token={secret:device_token}" {
			t.Errorf("mode %d: echo location = %q", mode, loc)
		}
		if notes := strings.Join(echo.Request.Notes, "; "); notes != "header Authorization: Bearer {secret:device_token}" {
			t.Errorf("mode %d: request notes = %q", mode, notes)
		}
		issue, _ := LoadGolden(goldenFile(dir, "cases", "secrets", "issue"))
		answer, _ = issue.For("admin")
		if want := `{"data":{"token":"{secret:api}"},"note":"keep it"}`; compactJSON(answer.Body.JSON) != want {
			t.Errorf("mode %d: the answer a secret was captured from is %s, want %s", mode, answer.Body.JSON, want)
		}
	}
}

// failRecorder takes the failures of a step instead of failing the test.
type failRecorder struct {
	testing.TB
	failures []string
}

func (r *failRecorder) Errorf(format string, args ...any) {
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

func (r *failRecorder) Helper() {}

// Masking cannot reach a value a case wrote into its own request, or one
// inside a body it cannot read. A recording that would write a known value
// writes nothing and leaves the file as it was.
func TestRecordingRefusesAGoldenThatHoldsASecret(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	dir := t.TempDir()
	s, _ := newSecretSuite(t, RecordFirst, dir, writeSecrets(t, map[string]string{"device_token": token}))
	file := goldenFile(dir, "cases", "g", "leak")
	before := nodeGolden(t, file, "g/leak", "GET", "/api/x")

	leak := newSlot("cases", "g", "leak", "g/leak", RequestInfo{Method: "GET", Path: "/api/x", Notes: []string{"header Authorization: Bearer " + token}})
	leak.rec.Answers["admin"] = jsonAnswer(200, `{"ok":true}`)
	clean := newSlot("cases", "g", "clean", "g/clean", RequestInfo{Method: "GET", Path: "/api/x", Notes: []string{"header Authorization: Bearer {secret:device_token}"}})
	clean.rec.Answers["admin"] = jsonAnswer(200, `{"ok":true}`)

	rec := &failRecorder{TB: t}
	s.flush(rec, []*slot{leak, clean})
	if len(rec.failures) != 1 || !strings.Contains(rec.failures[0], "the secret device_token") || strings.Contains(rec.failures[0], token) {
		t.Errorf("failures = %q; want one that names the secret and does not show it", rec.failures)
	}
	if after, _ := os.ReadFile(file); !bytes.Equal(after, before) {
		t.Errorf("the golden was written:\n%s", after)
	}
	if _, err := os.Stat(goldenFile(dir, "cases", "g", "clean")); err != nil {
		t.Errorf("the golden without a secret was not written: %v", err)
	}
	var summary strings.Builder
	s.Report.Print(&summary)
	if !strings.Contains(summary.String(), "g/leak: not written") {
		t.Errorf("the summary does not list the golden that was not written:\n%s", summary.String())
	}
}

// Messages and the summary hold the names of secrets, never their values.
func TestMessagesAndTheSummaryAreRedacted(t *testing.T) {
	const token = "0123456789abcdef0123456789abcdef"
	s, _ := newSecretSuite(t, Compare, t.TempDir(), writeSecrets(t, map[string]string{"device_token": token}))
	if got := s.redact("expected x, got \"" + token + "\""); got != `expected x, got "{secret:device_token}"` {
		t.Errorf("redact = %q", got)
	}
	s.Report.finding("a/b as admin: no answer for %s", token)
	s.Report.problem("[admin] body x: %s in the first recording", token)
	var summary strings.Builder
	s.Report.Print(&summary)
	if strings.Contains(summary.String(), token) || strings.Count(summary.String(), "{secret:device_token}") != 2 {
		t.Errorf("summary:\n%s", summary.String())
	}
}

// compactJSON is a JSON text on one line, as the suite encodes it.
func compactJSON(raw json.RawMessage) string {
	value, err := decodeJSON(raw)
	if err != nil {
		return "not JSON: " + string(raw)
	}
	return string(encodeJSON(value))
}

// An answer normalized before a secret was known is masked again from what
// the console sent, its redirects too.
func TestRemaskTakesTheSecretsLearnedSince(t *testing.T) {
	client, _ := newTestClient(t, false)
	answer := client.NewSession(SignedOut).Do(Request{Method: "GET", Path: "/hop1", FollowRedirects: true})
	if len(answer.Redirects) != 2 || !strings.Contains(answer.Redirects[1], "from=hop2") {
		t.Fatalf("redirects = %q", answer.Redirects)
	}
	if err := client.secrets.add("hop", "from=hop2"); err != nil {
		t.Fatal(err)
	}
	again := client.remask(answer)
	if strings.Contains(again.Redirects[1], "from=hop2") || !strings.Contains(again.Redirects[1], "{secret:hop}") {
		t.Errorf("redirects after the secret became known = %q", again.Redirects)
	}
	if strings.Contains(string(again.Body.JSON), "from=hop2") || !strings.Contains(string(again.Body.JSON), "{secret:hop}") {
		t.Errorf("body after the secret became known = %s", again.Body.JSON)
	}
}
