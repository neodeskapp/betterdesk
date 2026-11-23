package guest

import (
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapipath/proxytest"
)

// twoLinks is the API server's list with a link of e2e_operator and one
// of another account.
const twoLinks = `{"grants":[{"id":"mine","peer_ids":["dev-1"],"created_by":"e2e_operator"},{"id":"theirs","peer_ids":["dev-2"],"created_by":"e2e_other"}]}`

// TestOnlyAdminsSeeAndRevokeOtherAccountsLinks: an account whose role is
// no admin lists only the links it created and cannot take back a link of
// another account; the API server is not asked to delete it. Admins see
// and take back every link (RV111).
func TestOnlyAdminsSeeAndRevokeOtherAccountsLinks(t *testing.T) {
	w := newWorld(t)
	listAnswer := &proxytest.Answer{Status: 200, Type: "application/json", Body: twoLinks}
	ids := map[string]int64{"e2e_operator": 5, "e2e_issuer": 6, "e2e_admin": 1, "e2e_super": 2, "e2e_global": 3}
	as := func(username, role string) *proxytest.Account {
		account := &proxytest.Account{UserID: ids[username]}
		account.User.Username, account.User.Role = username, role
		return account
	}
	for _, c := range []struct {
		username, role, want string
	}{
		{"e2e_operator", "operator", `{"grants":[{"id":"mine","peer_ids":["dev-1"],"created_by":"e2e_operator"}]}`},
		{"e2e_issuer", "guest_issuer", `{"grants":[]}`},
		{"e2e_admin", "admin", twoLinks},
		{"e2e_super", "super_admin", twoLinks},
		{"e2e_global", "global_admin", twoLinks},
	} {
		rec, _ := w.Do(proxytest.Request{Method: "GET", Path: "/api/guest/access-links", Session: as(c.username, c.role)}, listAnswer)
		if rec.Code != 200 || rec.Body.String() != c.want {
			t.Errorf("%s lists: %d %s", c.role, rec.Code, rec.Body)
		}
	}

	deleted := func(calls []proxytest.Call) bool {
		for _, call := range calls {
			if call.Method == "DELETE" {
				return true
			}
		}
		return false
	}
	for _, c := range []struct {
		username, role, id string
		allowed            bool
	}{
		{"e2e_operator", "operator", "theirs", false},
		{"e2e_issuer", "guest_issuer", "mine", false},
		{"e2e_operator", "operator", "mine", true},
		{"e2e_operator", "operator", "unknown", true},
		{"e2e_admin", "admin", "theirs", true},
		{"e2e_global", "global_admin", "mine", true},
	} {
		w.revoked = nil
		rec, calls := w.Do(proxytest.Request{Method: "DELETE", Path: "/api/guest/access-links/" + c.id, Session: as(c.username, c.role)}, listAnswer)
		if c.allowed != deleted(calls) {
			t.Errorf("%s takes back %s: the API server was asked to delete it: %v", c.role, c.id, deleted(calls))
		}
		if !c.allowed && (rec.Code != 403 || rec.Body.String() != `{"error":"forbidden"}` || len(w.revoked) != 0) {
			t.Errorf("%s takes back %s: %d %s, sessions ended for %v", c.role, c.id, rec.Code, rec.Body, w.revoked)
		}
	}
}
