package api

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// TestConnectPasswordAnswerKeepsThePasswordOnTheServer: the viewer gets the
// answer to the device's password question made from the organization's
// stored device password, never the password, for the device's
// organization and for an organization whose shared address book holds
// the device even when the device is not assigned to it; not for an
// account outside both (BUGS L454).
func TestConnectPasswordAnswerKeepsThePasswordOnTheServer(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	if err := srv.InitPeerCredentialVault("l454-vault-test-secret-0123456789abcdef"); err != nil {
		t.Fatal(err)
	}
	for _, org := range []string{"org-dev", "org-book"} {
		if err := database.CreateOrganization(&db.Organization{ID: org, Name: org, Slug: org, CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.AssignDeviceToOrg(&db.OrgDevice{OrgID: "org-dev", DeviceID: "PEERAAA1"}); err != nil {
		t.Fatal(err)
	}
	for _, u := range []*db.User{
		{Username: "l454_member", PasswordHash: "hash", Role: auth.RoleOperator},
		{Username: "l454_book", PasswordHash: "hash", Role: auth.RoleOperator},
		{Username: "l454_outside", PasswordHash: "hash", Role: auth.RoleOperator},
		{Username: "l454_boss", PasswordHash: "hash", Role: auth.RoleSuperAdmin},
	} {
		if err := database.CreateUser(u); err != nil {
			t.Fatal(err)
		}
	}
	member, _ := database.GetUser("l454_member")
	book, _ := database.GetUser("l454_book")
	if err := database.CreateOrgUser(&db.OrgUser{ID: "ou1", OrgID: "org-dev", ServerUserID: member.ID, Username: member.Username, Role: "user"}); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateOrgUser(&db.OrgUser{ID: "ou2", OrgID: "org-book", ServerUserID: book.ID, Username: book.Username, Role: "user"}); err != nil {
		t.Fatal(err)
	}
	keep := func(org, peerID, pw string) {
		t.Helper()
		if status, body := asConsole(t, srv, http.MethodPut, "/api/org/"+org+"/peer-credentials/"+peerID, `{"password":"`+pw+`"}`); status != http.StatusOK {
			t.Fatalf("store %s/%s: %d %s", org, peerID, status, body)
		}
	}
	keep("org-dev", "PEERAAA1", "dev-org-pass")
	keep("org-book", "PEERBBB2", "book-only-pass")

	want := func(pw, salt, challenge string) string {
		first := sha256.Sum256([]byte(pw + salt))
		sum := sha256.Sum256(append(first[:], challenge...))
		return base64.StdEncoding.EncodeToString(sum[:])
	}
	ask := func(who, peerID string) (int, string) {
		role := auth.RoleOperator
		if who == "l454_boss" {
			role = auth.RoleSuperAdmin
		}
		return callAs(t, srv, Identity{Username: who, Role: role}, http.MethodPost, "/api/peers/"+peerID+"/connect-password/answer", `{"salt":"salt-1","challenge":"challenge-1"}`)
	}
	for _, c := range []struct {
		who, peer, pw string
	}{
		{"l454_member", "PEERAAA1", "dev-org-pass"},
		{"l454_book", "PEERBBB2", "book-only-pass"},
		{"l454_boss", "PEERAAA1", "dev-org-pass"},
		{"l454_boss", "PEERBBB2", "book-only-pass"},
	} {
		status, body := ask(c.who, c.peer)
		if status != http.StatusOK || !strings.Contains(body, want(c.pw, "salt-1", "challenge-1")) {
			t.Errorf("%s for %s: %d %s, want the answer from %s", c.who, c.peer, status, body, c.pw)
		}
		if strings.Contains(body, c.pw) {
			t.Errorf("%s for %s: the answer holds the password: %s", c.who, c.peer, body)
		}
	}
	for _, c := range []struct{ who, peer string }{{"l454_outside", "PEERAAA1"}, {"l454_outside", "PEERBBB2"}, {"l454_member", "PEERBBB2"}} {
		if status, body := ask(c.who, c.peer); status != http.StatusNotFound || !strings.Contains(body, `"password_set":false`) {
			t.Errorf("%s for %s: %d %s, want 404", c.who, c.peer, status, body)
		}
	}
	if status, body := callAs(t, srv, Identity{Username: "l454_member", Role: auth.RoleOperator}, http.MethodPost,
		"/api/peers/PEERAAA1/connect-password/answer", `{"salt":"","challenge":"c"}`); status != http.StatusBadRequest {
		t.Errorf("no salt: %d %s", status, body)
	}
}
