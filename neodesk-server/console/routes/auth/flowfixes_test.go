//go:build authflow

package auth_test

import (
	"encoding/json"
	"strings"
	"testing"
)

// The bug fixes of BUGS.md that change answers of testdata/flow.json, the
// Node console's recording. Each rewrites the recorded steps into what the
// console answers now, before they are compared; TestSignInFlow fails for
// a fix that changes no step.

type flowFix struct {
	bug     string
	rewrite func(t *testing.T, steps []flowStep) bool
}

var flowFixes = []flowFix{
	{"S11", proAccountGets403},
	{"S19", wholeNameInSignInEntries},
	{"S1", signOutClearsOnlySessionCookies},
	{"UM27", signOutPageAllowsPost},
	{"L344", wrongPasswordKeepsTheSession},
}

// wrongPasswordKeepsTheSession: a wrong password to turn the second factor
// off is a 400 that names the password, not the 401 of a lost session.
func wrongPasswordKeepsTheSession(t *testing.T, steps []flowStep) bool {
	changed := false
	for i := range steps {
		if steps[i].Name == "disable-wrong-password" && steps[i].Answer.Status == 401 {
			steps[i].Answer.Status = 400
			steps[i].Answer.Body.JSON = json.RawMessage(`{"error":"Current password is incorrect","success":false}`)
			changed = true
		}
	}
	return changed
}

// signOutPageAllowsPost: the 405 that GET /logout gives a signed-in
// browser names POST in its Allow header.
func signOutPageAllowsPost(t *testing.T, steps []flowStep) bool {
	changed := false
	for i := range steps {
		if steps[i].Request == "GET /logout" && steps[i].Answer.Status == 405 {
			steps[i].Answer.Headers["allow"] = []string{"POST"}
			changed = true
		}
	}
	return changed
}

// fixFlow applies every fix to the recorded steps.
func fixFlow(t *testing.T, steps []flowStep) {
	t.Helper()
	for _, fix := range flowFixes {
		if !fix.rewrite(t, steps) {
			t.Errorf("the fix of %s changes no step of the recording", fix.bug)
		}
	}
}

// auditEntries reads and writes the entries of the audit-entries step.
func auditEntries(t *testing.T, steps []flowStep, edit func(entries []map[string]any) []map[string]any) bool {
	t.Helper()
	for i := range steps {
		if steps[i].Name != "audit-entries" {
			continue
		}
		var entries []map[string]any
		if err := json.Unmarshal(steps[i].Answer.Body.JSON, &entries); err != nil {
			t.Fatalf("the recorded audit entries: %v", err)
		}
		before := string(steps[i].Answer.Body.JSON)
		raw, err := json.Marshal(edit(entries))
		if err != nil {
			t.Fatal(err)
		}
		steps[i].Answer.Body.JSON = raw
		return string(raw) != before
	}
	return false
}

// proAccountGets403: the right password of a pro account is answered with
// the 403 that says what the account is for, and writes no login_failed
// entry.
func proAccountGets403(t *testing.T, steps []flowStep) bool {
	changed := false
	for i := range steps {
		if steps[i].Name == "pro-account" && steps[i].Answer.Status != 403 {
			steps[i].Answer.Status = 403
			steps[i].Answer.Body.JSON = json.RawMessage(`{"error":"This account is for RustDesk desktop client only. Please use the RustDesk app to log in.","success":false}`)
			changed = true
		}
	}
	return auditEntries(t, steps, func(entries []map[string]any) []map[string]any {
		var kept []map[string]any
		for _, e := range entries {
			if details, _ := e["details"].(string); e["action"] == "login_failed" && strings.HasPrefix(details, "Username: e***;") {
				continue
			}
			kept = append(kept, e)
		}
		return kept
	}) && changed
}

// wholeNameInSignInEntries: the name of a login_failed entry is shortened
// whole, its last character included.
func wholeNameInSignInEntries(t *testing.T, steps []flowStep) bool {
	return auditEntries(t, steps, func(entries []map[string]any) []map[string]any {
		for _, e := range entries {
			if details, _ := e["details"].(string); strings.HasPrefix(details, "Username: f***;") {
				e["details"] = "Username: f***p;" + strings.TrimPrefix(details, "Username: f***;")
			}
		}
		return entries
	})
}

// signOutClearsOnlySessionCookies: a sign-out through the API clears the
// session cookie under its two names and no cookie called "undefined".
func signOutClearsOnlySessionCookies(t *testing.T, steps []flowStep) bool {
	changed := false
	for i := range steps {
		cookies := steps[i].Answer.Headers["set-cookie"]
		var kept []string
		for _, c := range cookies {
			if strings.HasPrefix(c, "undefined=") {
				changed = true
				continue
			}
			kept = append(kept, c)
		}
		if len(kept) != len(cookies) {
			steps[i].Answer.Headers["set-cookie"] = kept
		}
	}
	return changed
}
