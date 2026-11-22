//go:build authflow

package auth_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// The flow test signs in the way a person does, on an account of its own:
// two-factor enrolment with codes computed from the secret the console
// hands out, recovery codes, a password change, a second sign-in that ends
// the first session, sign-out through the page, the lockout after ten
// wrong passwords, the pro account, and the audit entries all of that
// leaves.
//
// The contract suite cannot do this: its cases send fixed bodies. So this
// test keeps its own recording, testdata/flow.json, made from the Node
// console before the routes moved. It runs against a test instance:
//
//	BD_URL=http://bdgo-au2:5000 go test -tags authflow -count=1 -run TestSignInFlow ./console/routes/auth/
//
// The recording is fixed: it can't be made again. CONTRACT_REQUIRE_GO=1
// fails when a sign-in route was not answered by the Go console. Use a
// fresh instance: the flow counts on the sign-in attempts and audit
// entries it finds.

const (
	flowFile        = "testdata/flow.json"
	flowUser        = "flow_auth_tmp"
	flowPassword    = "Flow-Auth-1!first"
	flowNewPassword = "Flow-Auth-2!second"
)

// flowStep is one request of the flow in the recording.
type flowStep struct {
	Name    string                `json:"name"`
	Request string                `json:"request"`
	Answer  contract.GoldenAnswer `json:"answer"`
}

// flow runs the steps and keeps their answers.
type flow struct {
	t      *testing.T
	client *contract.Client
	steps  []flowStep
	// got holds the answers as they came, in the order of steps.
	got []contract.Answer
	// asked lists the method and path of every sign-in route the flow used.
	asked map[string]bool
}

// do sends a request and keeps the answer under name. volatile lists the
// paths of the body whose values change from run to run; a path may carry
// what is still checked there after a space: "integer", or the format of a
// string ("data:image/png;base64,").
func (f *flow) do(name string, s *contract.Session, method, path string, body any, volatile ...string) contract.Answer {
	f.t.Helper()
	request := contract.Request{Method: method, Path: path}
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			f.t.Fatalf("%s: encode the body: %v", name, err)
		}
		request.Body, request.ContentType = raw, "application/json"
	}
	answer := s.Do(request)
	if answer.Failed != "" {
		f.t.Fatalf("%s: no answer (%s)", name, answer.Failed)
	}
	if strings.HasPrefix(path, "/api/auth/") || path == "/logout" {
		f.asked[method+" "+strings.SplitN(path, "?", 2)[0]] = true
	}
	f.keep(name, method+" "+path, answer, volatile...)
	return answer
}

// keep adds an answer to the recording.
func (f *flow) keep(name, request string, answer contract.Answer, volatile ...string) {
	f.t.Helper()
	marks := make([]contract.VolatileMark, 0, len(volatile))
	paths := make([]string, 0, len(volatile))
	for _, text := range volatile {
		p, check, _ := strings.Cut(text, " ")
		mark := contract.VolatileMark{Path: p}
		switch {
		case check == "integer":
			mark.Integer = true
		case check != "":
			mark.Pattern = check
		}
		marks = append(marks, mark)
		paths = append(paths, p)
	}
	golden := contract.GoldenAnswer{
		Status: answer.Status, Redirects: answer.Redirects, Headers: answer.Headers, Body: answer.Body, Volatile: marks,
	}
	if answer.Body.Kind == contract.BodyJSON && len(paths) > 0 {
		masked, err := maskPaths(answer.Body.JSON, paths)
		if err != nil {
			f.t.Fatalf("%s: %v", name, err)
		}
		golden.Body.JSON = masked
	}
	f.steps = append(f.steps, flowStep{Name: name, Request: request, Answer: golden})
	f.got = append(f.got, answer)
}

// field reads a value out of a JSON answer by keys.
func field(t *testing.T, answer contract.Answer, keys ...string) any {
	t.Helper()
	var value any
	if err := json.Unmarshal(answer.Body.JSON, &value); err != nil {
		t.Fatalf("the answer is not JSON: %v", err)
	}
	for _, key := range keys {
		object, ok := value.(map[string]any)
		if !ok {
			t.Fatalf("the answer has no %s", strings.Join(keys, "."))
		}
		value = object[key]
	}
	return value
}

func TestSignInFlow(t *testing.T) {
	settings, err := contract.LoadSettings(os.Getenv)
	if err != nil {
		t.Skipf("no console to test: %v", err)
	}
	client, err := contract.NewClient(settings)
	if err != nil {
		t.Fatalf("%v", err)
	}
	f := &flow{t: t, client: client, asked: map[string]bool{}}

	admin := client.NewSession("admin")
	if _, err := admin.SignIn(); err != nil || !admin.SignedIn() {
		t.Fatalf("sign in as admin: %v", err)
	}
	browser := client.NewSession(contract.SignedOut)
	if _, err := browser.SignIn(); err != nil {
		t.Fatalf("open a browser: %v", err)
	}
	second := client.NewSession(contract.SignedOut)
	if _, err := second.SignIn(); err != nil {
		t.Fatalf("open a second browser: %v", err)
	}
	baseline := lastAuditID(t, admin)

	created := admin.Do(jsonRequest(t, http.MethodPost, "/api/users", map[string]any{"username": flowUser, "password": flowPassword, "role": "operator"}))
	if created.Status != http.StatusOK {
		t.Fatalf("create the account %s: status %d (a used instance still has it; start a fresh one)", flowUser, created.Status)
	}
	accountID := fmt.Sprint(field(t, created, "data", "id"))
	defer func() {
		if gone := admin.Do(contract.Request{Method: http.MethodDelete, Path: "/api/users/" + accountID}); gone.Status != http.StatusOK {
			t.Errorf("delete the account %s: status %d", flowUser, gone.Status)
		}
	}()

	credentials := func(password string) map[string]any {
		return map[string]any{"username": flowUser, "password": password}
	}
	sessionTimes := []string{
		"data.sessions[*].id", "data.sessions[*].createdAt integer", "data.sessions[*].lastActivityAt integer",
		"data.sessions[*].endedAt integer", "data.sessions[*].expiresAt integer",
	}

	// Enrolment.
	f.do("sign-in", browser, "POST", "/api/auth/login", credentials(flowPassword))
	f.do("status-before", browser, "GET", "/api/auth/totp/status", nil)
	f.do("enable-before-setup", browser, "POST", "/api/auth/totp/enable", map[string]any{"code": "123456"})
	setup := f.do("setup", browser, "POST", "/api/auth/totp/setup", map[string]any{},
		`qrCode ^aaaa:aaaaa/aaa;aaaa99,[9aA+/_=-]+$`, `secret ^[9aA+/_=-]{32}$`, "otpauthUrl")
	secret, _ := field(t, setup, "secret").(string)
	if !regexp.MustCompile(`^[A-Z2-7]{32}$`).MatchString(secret) {
		t.Fatalf("setup: the secret is not 32 base32 characters")
	}
	wantURL := "otpauth://totp/BetterDesk:" + flowUser + "?secret=" + secret + "&issuer=BetterDesk&digits=6&period=30"
	if got := field(t, setup, "otpauthUrl"); got != wantURL {
		t.Errorf("setup: otpauthUrl is not the address of the secret for %s", flowUser)
	}
	f.do("enable-wrong-code", browser, "POST", "/api/auth/totp/enable", map[string]any{"code": wrongCode(t, secret)})
	f.do("status-while-pending", browser, "GET", "/api/auth/totp/status", nil)
	enabled := f.do("enable", browser, "POST", "/api/auth/totp/enable", map[string]any{"code": currentCode(t, secret)},
		`recoveryCodes[*] ^[9aA+/_=-]{9}$`)
	codes, _ := field(t, enabled, "recoveryCodes").([]any)
	if len(codes) != 8 {
		t.Fatalf("enable: %d recovery codes, want 8", len(codes))
	}
	for _, code := range codes {
		if text, _ := code.(string); !regexp.MustCompile(`^[0-9A-F]{4}-[0-9A-F]{4}$`).MatchString(text) {
			t.Fatalf("enable: a recovery code is not XXXX-XXXX in upper-case hex")
		}
	}
	f.do("status-enabled", browser, "GET", "/api/auth/totp/status", nil)
	f.do("setup-when-enabled", browser, "POST", "/api/auth/totp/setup", map[string]any{})
	f.do("sign-out", browser, "POST", "/api/auth/logout", map[string]any{})

	// Password, then code.
	f.do("password-step", browser, "POST", "/api/auth/login", map[string]any{"username": flowUser, "password": flowPassword, "return": "/devices"})
	f.do("pending-is-not-signed-in", browser, "GET", "/api/auth/verify", nil)
	f.do("code-wrong", browser, "POST", "/api/auth/totp/verify", map[string]any{"code": wrongCode(t, secret)})
	f.do("code-missing", browser, "POST", "/api/auth/totp/verify", map[string]any{})
	f.do("code-right", browser, "POST", "/api/auth/totp/verify", map[string]any{"code": currentCode(t, secret), "return": "/devices"})
	f.do("verify-after-code", browser, "GET", "/api/auth/verify", nil,
		"user.id integer", "session.createdAt integer", "session.lastActivityAt integer",
		"session.idleExpiresAt integer", "session.absoluteExpiresAt integer", "session.serverNow integer")
	f.do("code-after-sign-in", browser, "POST", "/api/auth/totp/verify", map[string]any{"code": currentCode(t, secret)})
	f.do("sessions-after-code", browser, "GET", "/api/auth/sessions", nil, sessionTimes...)
	f.do("sign-out-2", browser, "POST", "/api/auth/logout", map[string]any{})

	// Recovery codes: each works once, in any letter case.
	first, _ := codes[0].(string)
	secondCode, _ := codes[1].(string)
	f.do("password-step-2", browser, "POST", "/api/auth/login", credentials(flowPassword))
	f.do("recovery-wrong", browser, "POST", "/api/auth/totp/verify", map[string]any{"recoveryCode": "0000-0000"})
	f.do("recovery-right", browser, "POST", "/api/auth/totp/verify", map[string]any{"recoveryCode": first})
	f.do("sign-out-3", browser, "POST", "/api/auth/logout", map[string]any{})
	f.do("password-step-3", browser, "POST", "/api/auth/login", credentials(flowPassword))
	f.do("recovery-used", browser, "POST", "/api/auth/totp/verify", map[string]any{"recoveryCode": first})
	f.do("recovery-lower-case", browser, "POST", "/api/auth/totp/verify", map[string]any{"recoveryCode": " " + strings.ToLower(secondCode) + " "})

	// Switching the second factor off needs the password.
	f.do("disable-wrong-password", browser, "POST", "/api/auth/totp/disable", map[string]any{"password": "not-the-password"})
	f.do("disable", browser, "POST", "/api/auth/totp/disable", map[string]any{"password": flowPassword})
	f.do("status-disabled", browser, "GET", "/api/auth/totp/status", nil)

	// The password. Creating the account and these two requests are the
	// three that the limit of password changes lets through in five minutes.
	f.do("password-mismatch", browser, "POST", "/api/auth/password",
		map[string]any{"currentPassword": flowPassword, "newPassword": flowNewPassword, "confirmPassword": flowPassword})
	f.do("password-change", browser, "POST", "/api/auth/password",
		map[string]any{"currentPassword": flowPassword, "newPassword": flowNewPassword, "confirmPassword": flowNewPassword})
	// The sign-out page refuses a GET and keeps the session; its form posts
	f.do("sign-out-page", browser, "GET", "/logout", nil)
	f.do("verify-after-sign-out-page", browser, "GET", "/api/auth/verify", nil,
		"user.id integer", "session.createdAt integer", "session.lastActivityAt integer",
		"session.idleExpiresAt integer", "session.absoluteExpiresAt integer", "session.serverNow integer")
	f.do("sign-out-form", browser, "POST", "/logout", nil)
	f.do("verify-after-sign-out-form", browser, "GET", "/api/auth/verify", nil)
	f.do("old-password", browser, "POST", "/api/auth/login", credentials(flowPassword))
	f.do("new-password", browser, "POST", "/api/auth/login", credentials(flowNewPassword))

	// A second browser signs in: the first session ends.
	f.do("second-browser", second, "POST", "/api/auth/login", credentials(flowNewPassword))
	f.do("first-browser-ended", browser, "GET", "/api/auth/verify", nil)
	f.do("sessions-in-second", second, "GET", "/api/auth/sessions", nil, sessionTimes...)
	f.do("second-sign-out", second, "POST", "/api/auth/logout", map[string]any{})

	// Ten wrong passwords lock the account: the console's sign-in and the
	// client API refuse it before they look at the password, the right one
	// included, so the lockout stays.
	// The old password above was the first failure
	for i := 1; i <= 8; i++ {
		browser.Do(jsonRequest(t, http.MethodPost, "/api/auth/login", credentials("not-the-password")))
	}
	f.do("tenth-wrong-password", browser, "POST", "/api/auth/login", credentials("not-the-password"))
	time.Sleep(500 * time.Millisecond)
	f.do("client-api-locked", browser, "POST", "/api/login", credentials("not-the-password"), "retry_after integer")
	f.do("locked-account-refused", browser, "POST", "/api/auth/login", credentials(flowNewPassword), "retry_after integer")
	time.Sleep(500 * time.Millisecond)
	f.do("client-api-still-locked", browser, "POST", "/api/login", credentials("not-the-password"), "retry_after integer")
	f.do("sign-out-4", browser, "POST", "/api/auth/logout", map[string]any{})

	// A pro account is for the desktop client only.
	pro := client.NewSession("pro")
	answer, err := pro.SignIn()
	if err != nil || answer == nil {
		t.Fatalf("sign in as pro: %v", err)
	}
	f.keep("pro-account", "POST /api/auth/login", *answer)

	f.keep("audit-entries", "GET /api/settings/audit", auditSince(t, admin, baseline))

	if settings.RequireGo {
		for id := range f.asked {
			method, path, _ := strings.Cut(id, " ")
			if handler := client.Handler(method, path); handler != contract.HandlerGo {
				t.Errorf("%s was answered by %q, not by the Go console", id, handler)
			}
		}
	}

	raw, err := os.ReadFile(flowFile)
	if err != nil {
		t.Fatalf("read the recording: %v", err)
	}
	var want []flowStep
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("read the recording: %v", err)
	}
	fixFlow(t, want)
	if len(want) != len(f.steps) {
		t.Fatalf("the recording has %d steps, the flow %d", len(want), len(f.steps))
	}
	for i, step := range f.steps {
		if want[i].Name != step.Name {
			t.Fatalf("step %d is %q in the recording and %q in the flow", i+1, want[i].Name, step.Name)
		}
		for _, diff := range contract.CompareAnswers(want[i].Answer, f.got[i]) {
			t.Errorf("%s (%s): %s", step.Name, step.Request, diff)
		}
	}
}

func jsonRequest(t *testing.T, method, path string, body any) contract.Request {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode the body: %v", err)
	}
	return contract.Request{Method: method, Path: path, Body: raw, ContentType: "application/json"}
}

// auditRows reads the newest audit entries as the settings page gets them.
func auditRows(t *testing.T, admin *contract.Session) []map[string]any {
	t.Helper()
	answer := admin.Do(contract.Request{Method: http.MethodGet, Path: "/api/settings/audit?limit=500"})
	if answer.Status != http.StatusOK {
		t.Fatalf("read the audit log: status %d", answer.Status)
	}
	var body struct {
		Data []map[string]any `json:"data"`
	}
	dec := json.NewDecoder(bytes.NewReader(answer.Body.JSON))
	dec.UseNumber()
	if err := dec.Decode(&body); err != nil {
		t.Fatalf("read the audit log: %v", err)
	}
	return body.Data
}

func auditID(row map[string]any) int64 {
	var id int64
	_, _ = fmt.Sscan(fmt.Sprint(row["id"]), &id)
	return id
}

func lastAuditID(t *testing.T, admin *contract.Session) int64 {
	t.Helper()
	var last int64
	for _, row := range auditRows(t, admin) {
		last = max(last, auditID(row))
	}
	return last
}

// auditSince returns the audit entries written after the entry with the id
// baseline, oldest first, as an answer that can be compared: action,
// details, address and the name of the account of each. Entries of other
// accounts than the flow's and the pro account's are left out: the admin's
// own requests write some.
func auditSince(t *testing.T, admin *contract.Session, baseline int64) contract.Answer {
	t.Helper()
	rows := auditRows(t, admin)
	sort.Slice(rows, func(i, j int) bool { return auditID(rows[i]) < auditID(rows[j]) })
	type entry struct {
		Action   any `json:"action"`
		Details  any `json:"details"`
		Address  any `json:"ip_address"`
		Username any `json:"username"`
	}
	entries := []entry{}
	for _, row := range rows {
		if auditID(row) <= baseline {
			continue
		}
		details, _ := row["details"].(string)
		if name, _ := row["username"].(string); name != flowUser && !strings.HasPrefix(fmt.Sprint(row["action"]), "login_failed") {
			continue
		} else if name == "" && !strings.Contains(details, "f***") && !strings.Contains(details, "e***") {
			continue
		}
		entries = append(entries, entry{row["action"], row["details"], row["ip_address"], row["username"]})
	}
	raw, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("encode the audit entries: %v", err)
	}
	return contract.Answer{Status: http.StatusOK, Body: contract.Body{Kind: contract.BodyJSON, JSON: raw}}
}

// maskPaths replaces the values at the paths of a JSON text: strings by
// "<volatile>", numbers by 0. A path is keys joined by dots; "[*]" after a
// key stands for every element of the list there.
func maskPaths(raw json.RawMessage, paths []string) (json.RawMessage, error) {
	var value any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return nil, fmt.Errorf("mask: %w", err)
	}
	for _, p := range paths {
		value = maskAt(value, strings.Split(p, "."))
	}
	var out bytes.Buffer
	enc := json.NewEncoder(&out)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return nil, fmt.Errorf("mask: %w", err)
	}
	return bytes.TrimSpace(out.Bytes()), nil
}

func maskAt(value any, keys []string) any {
	if len(keys) == 0 {
		switch value.(type) {
		case string:
			return "<volatile>"
		case json.Number:
			return json.Number("0")
		}
		return value
	}
	key, every := strings.CutSuffix(keys[0], "[*]")
	object, ok := value.(map[string]any)
	if !ok {
		return value
	}
	child, has := object[key]
	if !has {
		return value
	}
	if every {
		if list, ok := child.([]any); ok {
			for i := range list {
				list[i] = maskAt(list[i], keys[1:])
			}
		}
	} else {
		object[key] = maskAt(child, keys[1:])
	}
	return value
}
