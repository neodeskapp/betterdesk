package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/auth"
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
