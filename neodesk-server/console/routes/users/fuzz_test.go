package users

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
)

// bodyMember runs a JSON body {"v": value} through the console's body
// parser and calls fn with the member; it reports false when the parser
// refused the body.
func bodyMember(value string, fn func(v jsbind.Value)) bool {
	parsed := false
	handler := middleware.Bodies(middleware.BodyOptions{})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		parsed = true
		fn(member(middleware.Body(r), "v"))
	}))
	req := httptest.NewRequest(http.MethodPost, "/api/users", strings.NewReader(`{"v":`+value+`}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	return parsed
}

// FuzzRequestValues reads every kind of JSON value the way the account
// routes read the members of a body: as user group guids and as an email
// address. Nothing may panic, and what comes out keeps the rules the
// routes rely on.
func FuzzRequestValues(f *testing.F) {
	for _, seed := range []string{
		`"ug-alpha, ug-beta,,ug-alpha"`, `["a", " b ", null, 0, false, ["c"], {"guid": "x"}]`, `{"toString": 1}`,
		`[{"toString": "x"}]`, `"  Mail@Example.com  "`, `"a@b.c"`, `"two@@at.example"`, `" a@b.c"`,
		`12345678`, `true`, `["Abcdefgh1!"]`, `{"length": "20"}`, `{"length": {"toString": 1}}`, `"Strong-Pass-1!"`,
		`"\ud800abc"`, `1e400`, `-0`, `null`, `""`, `[[[]]]`,
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		bodyMember(value, func(v jsbind.Value) {
			guids, err := normalizeGroupGUIDs(v)
			if err == nil {
				if len(guids) > maxGroupGUIDs {
					t.Fatalf("%d guids", len(guids))
				}
				seen := map[string]bool{}
				for _, guid := range guids {
					if guid == "" || guid != jsbind.Trim(guid) || seen[guid] {
						t.Fatalf("guid %q in %q", guid, guids)
					}
					seen[guid] = true
				}
			} else if err != errNotPrimitive {
				t.Fatalf("group guids: %v", err)
			}

			email, err := normalizeEmail(v)
			switch {
			case err != nil:
				if err != errNotPrimitive && statusOf(err) != http.StatusBadRequest {
					t.Fatalf("email: %v", err)
				}
			case email != nil:
				if *email != jsbind.Trim(*email) || jsLength(*email) > 200 || (*email != "" && !emailPattern.MatchString(*email)) {
					t.Fatalf("email %q", *email)
				}
			case !v.IsUndefined() && !v.IsNull():
				t.Fatalf("no email for %s", value)
			}
		})
	})
}
