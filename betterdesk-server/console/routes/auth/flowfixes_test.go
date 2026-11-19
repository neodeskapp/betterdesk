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
