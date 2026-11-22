//go:build contract

package contract

import (
	"path/filepath"
	"strings"
	"testing"
)

// The cases where a hand mark freed the very thing the case is about
// (BUGS RV86): each committed golden, with the case's own marks, must
// refuse a console that answers wrongly there. The answers are the
// golden's own with one change. The cases are registered by the case
// files of package contract_test, which this binary holds.

// expected loads the golden of a case or matrix route and returns what
// role is expected to get, with the marks the code sets by hand, as a
// check compares it.
func expected(t *testing.T, file string, role Role, volatile []string) GoldenAnswer {
	t.Helper()
	g, err := LoadGolden(filepath.Join("testdata", "golden", file))
	if err != nil {
		t.Fatal(err)
	}
	want, ok := g.For(role)
	if !ok {
		t.Fatalf("%s has no answer for %s", file, role)
	}
	if want.Status != 200 || want.Body.Kind != BodyJSON {
		t.Fatalf("%s as %s: status %d, %s, want a JSON answer with 200", file, role, want.Status, want.Body.Kind)
	}
	return withHandMarks(want, volatile, nil)
}

// handMarksOf returns the hand marks of a registered case.
func handMarksOf(t *testing.T, group, name string) []string {
	t.Helper()
	g := caseGroups[group]
	if g == nil {
		t.Fatalf("no group %q is registered", group)
	}
	for _, c := range g.Cases {
		if c.Name == name {
			return c.Volatile
		}
	}
	t.Fatalf("group %q has no case %q", group, name)
	return nil
}

// changed is the golden's body with change applied to it, as an answer.
func changed(t *testing.T, want GoldenAnswer, change func(body map[string]any)) Answer {
	t.Helper()
	value, err := decodeJSON(want.Body.JSON)
	if err != nil {
		t.Fatal(err)
	}
	body := value.(map[string]any)
	change(body)
	a := want.Answer()
	a.Body = Body{Kind: BodyJSON, JSON: encodeJSON(body)}
	return a
}

// parse decodes JSON text the way answers hold it.
func parse(t *testing.T, text string) any {
	t.Helper()
	value, err := decodeJSON([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// refuses fails unless the answer differs from the golden at where.
func refuses(t *testing.T, what string, want GoldenAnswer, got Answer, where string) {
	t.Helper()
	diffs := CompareAnswers(want, got)
	for _, d := range diffs {
		if strings.HasPrefix(d.Where, where) {
			return
		}
	}
	t.Errorf("%s: no difference at %s, got %q", what, where, diffStrings(diffs))
}

// passes fails when the answer differs from the golden.
func passes(t *testing.T, what string, want GoldenAnswer, got Answer) {
	t.Helper()
	if diffs := CompareAnswers(want, got); len(diffs) > 0 {
		t.Errorf("%s: %q", what, diffStrings(diffs))
	}
}

func TestMarksKeepWhatTheCaseIsAbout(t *testing.T) {
	t.Run("another permission list for a role", func(t *testing.T) {
		want := expected(t, "cases/permissions/role-operator.json", "admin", handMarksOf(t, "permissions", "role-operator"))
		refuses(t, "other permissions", want, changed(t, want, func(b map[string]any) {
			b["data"].(map[string]any)["permissions"] = parse(t, `["server.config","user.delete","no.such.permission"]`)
		}), "body data.permissions")
		refuses(t, "one permission more", want, changed(t, want, func(b map[string]any) {
			data := b["data"].(map[string]any)
			data["permissions"] = append(data["permissions"].([]any), "user.delete")
		}), "body data.permissions")
		passes(t, "the same permissions in another order", want, changed(t, want, func(b map[string]any) {
			data := b["data"].(map[string]any)
			list := data["permissions"].([]any)
			reversed := make([]any, len(list))
			for i, p := range list {
				reversed[len(list)-1-i] = p
			}
			data["permissions"] = reversed
		}))
	})

	t.Run("another permission list in the role list", func(t *testing.T) {
		want := expected(t, "matrix/permissions/GET_api_panel_roles.json", "admin", matrixVolatile["GET /api/panel/roles"])
		refuses(t, "viewer with user.delete", want, changed(t, want, func(b map[string]any) {
			roles := b["data"].(map[string]any)["roles"].([]any)
			for _, r := range roles {
				role := r.(map[string]any)
				if role["role"] == "viewer" || role["name"] == "viewer" || role["id"] == "viewer" {
					role["permissions"] = append(role["permissions"].([]any), "user.delete")
					return
				}
			}
			t.Fatalf("no viewer in %s", want.Body.JSON)
		}), "body data.roles[")
	})

	t.Run("500 events for limit=2", func(t *testing.T) {
		want := expected(t, "cases/security-audit/events-two.json", "admin", handMarksOf(t, "security-audit", "events-two"))
		refuses(t, "500 events", want, changed(t, want, func(b map[string]any) {
			events := b["events"].([]any)
			many := make([]any, 500)
			for i := range many {
				many[i] = events[0]
			}
			b["events"] = many
		}), "body events")
	})

	t.Run("another action for the action filter", func(t *testing.T) {
		want := expected(t, "cases/security-audit/events-of-an-action.json", "admin", handMarksOf(t, "security-audit", "events-of-an-action"))
		refuses(t, "a login event", want, changed(t, want, func(b map[string]any) {
			b["events"].([]any)[0].(map[string]any)["action"] = "api_login_failed"
		}), "body events[0].action")
	})

	t.Run("a password hash in available-users", func(t *testing.T) {
		want := expected(t, "cases/organizations/available-users.json", "admin", handMarksOf(t, "organizations", "available-users"))
		refuses(t, "an account with its hash", want, changed(t, want, func(b map[string]any) {
			b["users"] = parse(t, `[{"id":1,"password_hash":"x","username":"admin"}]`)
		}), "body users")
	})

	t.Run("another device's command", func(t *testing.T) {
		want := expected(t, "cases/automation-device/fetch-own-device.json", "signed-out", handMarksOf(t, "automation-device", "fetch-own-device"))
		var sample map[string]any
		changedAnswer := changed(t, want, func(b map[string]any) {
			commands := b["commands"].([]any)
			if len(commands) == 0 {
				t.Fatalf("the golden has no command of the device: %s", want.Body.JSON)
			}
			sample = commands[0].(map[string]any)
			other := map[string]any{}
			for k, v := range sample {
				other[k] = v
			}
			other["device_id"] = "900100002"
			b["commands"] = append(commands, other)
		})
		if sample["device_id"] != "900100001" {
			t.Fatalf("the golden's command is of %v, want 900100001", sample["device_id"])
		}
		refuses(t, "a command of 900100002", want, changedAnswer, "body commands[")
	})
}
