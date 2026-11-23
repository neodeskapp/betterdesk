package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// TestGuestLinksBelongToTheActingAccount: a guest link records the account
// the console acts for as its creator, and a caller below the guest admin
// roles lists and revokes only the links of the account it acts for (RV111).
func TestGuestLinksBelongToTheActingAccount(t *testing.T) {
	srv, _ := newInternalServer(t, nil)
	alice := consoleBridge
	alice.Actor = "alice"

	status, body := callAs(t, srv, alice, http.MethodPost, "/api/guest/access-links", `{"peer_ids":["PEERAAA1"],"ttl_minutes":30}`)
	var link struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &link); status != http.StatusCreated || err != nil || link.ID == "" {
		t.Fatalf("create a guest link for alice: %d %s", status, body)
	}
	if _, body := callAs(t, srv, alice, http.MethodGet, "/api/guest/access-links", ""); !strings.Contains(body, `"created_by":"alice"`) {
		t.Errorf("the link's creator: %s, want alice", body)
	}

	asAlice := Identity{Username: "rv_op", Role: auth.RoleOperator, Actor: "alice"}
	asBob := Identity{Username: "rv_op", Role: auth.RoleOperator, Actor: "bob"}
	if _, body := callAs(t, srv, asAlice, http.MethodGet, "/api/guest/access-links", ""); !strings.Contains(body, link.ID) {
		t.Errorf("an operator acting for alice does not list her link: %s", body)
	}
	if _, body := callAs(t, srv, asBob, http.MethodGet, "/api/guest/access-links", ""); strings.Contains(body, link.ID) {
		t.Errorf("an operator acting for bob lists alice's link: %s", body)
	}
	if status, body := callAs(t, srv, asBob, http.MethodDelete, "/api/guest/access-links/"+link.ID, ""); status != http.StatusForbidden {
		t.Errorf("an operator acting for bob revokes alice's link: %d %s, want 403", status, body)
	}
	if status, body := callAs(t, srv, asAlice, http.MethodDelete, "/api/guest/access-links/"+link.ID, ""); status != http.StatusOK {
		t.Errorf("an operator acting for alice revokes her link: %d %s, want 200", status, body)
	}
}

// TestGuestLinksKeepTheCreatorsAccountID: a guest link keeps the id of the
// account it was made for, and its validation names it (created_by_id), so
// the console holds a guest's session to that account's client features
// (L164). A creator the server has no account for gives none.
func TestGuestLinksKeepTheCreatorsAccountID(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	anna := &db.User{Username: "anna", PasswordHash: "x", Role: auth.RoleOperator}
	if err := database.CreateUser(anna); err != nil || anna.ID == 0 {
		t.Fatalf("CreateUser: %v (id %d)", err, anna.ID)
	}
	for _, c := range []struct {
		actor string
		want  int64
	}{{"anna", anna.ID}, {"nobody", 0}} {
		as := consoleBridge
		as.Actor = c.actor
		status, body := callAs(t, srv, as, http.MethodPost, "/api/guest/access-links", `{"peer_ids":["PEERAAA1"],"ttl_minutes":30}`)
		var link struct {
			Token string `json:"token"`
		}
		if err := json.Unmarshal([]byte(body), &link); status != http.StatusCreated || err != nil || link.Token == "" {
			t.Fatalf("create a guest link for %s: %d %s", c.actor, status, body)
		}
		_, body = callAs(t, srv, as, http.MethodGet, "/api/guest/access-links/validate?token="+link.Token, "")
		var grant struct {
			Valid       bool  `json:"valid"`
			CreatedByID int64 `json:"created_by_id"`
		}
		if err := json.Unmarshal([]byte(body), &grant); err != nil || !grant.Valid || grant.CreatedByID != c.want {
			t.Errorf("validation of %s's link: %s, want created_by_id %d", c.actor, body, c.want)
		}
	}
}
