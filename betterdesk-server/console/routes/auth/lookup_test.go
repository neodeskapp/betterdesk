package auth_test

import (
	"net/http"
	"strings"
	"testing"
)

// The console has no account typeahead: GET /login/lookup is an unknown
// path, so a visitor who is not signed in learns no account names from it
// (BUGS UM26, DECISIONS A71).
func TestNoAccountLookup(t *testing.T) {
	eachWorld(t, setup{}, func(t *testing.T, w *world) {
		w.addUser(user{name: "op_one"})
		a := w.agent()
		for _, target := range []string{"/login/lookup?q=op", "/login/lookup"} {
			res := a.get(target)
			if res.status != http.StatusNotFound || strings.Contains(res.body, "op_one") || strings.Contains(res.body, "matches") {
				t.Errorf("%s: %d %s", target, res.status, res.body)
			}
		}
	})
}
