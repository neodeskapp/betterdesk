package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/auth"
	"github.com/unitronix/betterdesk-server/db"
)

// TestChatContactsCountUnreadPerContact: each contact carries the messages
// it sent that the asking device has not read, not the device's total: an
// operator's under operator:<name>, the console's sender "operator" with
// the operator's name (BUGS CH6).
func TestChatContactsCountUnreadPerContact(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	if err := database.CreateUser(&db.User{Username: "alice", PasswordHash: "hash", Role: auth.RoleOperator}); err != nil {
		t.Fatal(err)
	}
	if err := database.CreateChatGroup(&db.ChatGroup{ID: "group:ab", Name: "A and B", Members: "PEERAAA1,PEERBBB2"}); err != nil {
		t.Fatal(err)
	}
	for _, m := range []db.ChatMessage{
		{ConversationID: "PEERAAA1", FromID: "PEERBBB2", Text: "one"},
		{ConversationID: "PEERAAA1", FromID: "PEERBBB2", Text: "two"},
		{ConversationID: "PEERAAA1", FromID: "operator", FromName: "alice", ToID: "PEERAAA1", Text: "three"},
		{ConversationID: "PEERAAA1", FromID: "PEERAAA1", Text: "own"},
		{ConversationID: "PEERBBB2", FromID: "operator", FromName: "alice", ToID: "PEERBBB2", Text: "elsewhere"},
	} {
		if _, err := database.SaveChatMessage(&m); err != nil {
			t.Fatal(err)
		}
	}
	status, body := asConsole(t, srv, http.MethodGet, "/api/chat/contacts/PEERAAA1", "")
	var answer struct {
		Contacts []struct {
			ID     string `json:"id"`
			Unread int    `json:"unread"`
		} `json:"contacts"`
	}
	if err := json.Unmarshal([]byte(body), &answer); status != http.StatusOK || err != nil {
		t.Fatalf("contacts: %d %s", status, body)
	}
	got := map[string]int{}
	for _, c := range answer.Contacts {
		got[c.ID] = c.Unread
	}
	for id, want := range map[string]int{"PEERBBB2": 2, "operator:alice": 1} {
		if n, listed := got[id]; !listed || n != want {
			t.Errorf("unread of %s: %d (listed %v), want %d; all: %v", id, n, listed, want, got)
		}
	}
}

// contactIDs asks the API server for the chat contacts of an id and
// returns the ids of the contacts, in their order.
func contactIDs(t *testing.T, srv *Server, id string) []string {
	t.Helper()
	status, body := asConsole(t, srv, http.MethodGet, "/api/chat/contacts/"+id, "")
	var answer struct {
		Contacts []struct {
			ID string `json:"id"`
		} `json:"contacts"`
	}
	if err := json.Unmarshal([]byte(body), &answer); status != http.StatusOK || err != nil {
		t.Fatalf("contacts of %s: %d %s", id, status, body)
	}
	ids := []string{}
	for _, c := range answer.Contacts {
		ids = append(ids, c.ID)
	}
	return ids
}

// TestChatContactsOfADeviceAreItsGroups: a device's chat contacts are the
// operators and the devices it shares a chat group with, not every device
// of the server; the panel's inbox ("panel") lists every device, which the
// console filters by the account's scope (BUGS G24, DECISIONS A74).
func TestChatContactsOfADeviceAreItsGroups(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	for _, p := range []*db.Peer{
		{ID: "PEERCCC3", Hostname: "host-c", Status: "ONLINE"},
		{ID: "PEERAAA12", Hostname: "host-a12", Status: "ONLINE"},
		{ID: "PEERDDD4", Hostname: "host-d", Status: "ONLINE"},
	} {
		if err := database.UpsertPeer(p); err != nil {
			t.Fatal(err)
		}
	}
	if err := database.CreateUser(&db.User{Username: "alice", PasswordHash: "hash", Role: auth.RoleOperator}); err != nil {
		t.Fatal(err)
	}
	same := func(what string, got, want []string) {
		t.Helper()
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s: %q, want %q", what, got, want)
		}
	}

	same("a device in no group", contactIDs(t, srv, "PEERAAA1"), []string{"operator:alice"})
	for _, g := range []*db.ChatGroup{
		{ID: "group:one", Name: "one", Members: "PEERCCC3, PEERAAA1"},
		// A member whose id holds PEERAAA1 is no member PEERAAA1
		{ID: "group:two", Name: "two", Members: "PEERAAA12,PEERDDD4"},
		{ID: "group:three", Name: "three", Members: "PEERBBB2,PEERAAA1,GONE0001"},
	} {
		if err := database.CreateChatGroup(g); err != nil {
			t.Fatal(err)
		}
	}
	same("a device in two groups", contactIDs(t, srv, "PEERAAA1"), []string{"PEERBBB2", "PEERCCC3", "operator:alice"})
	same("a device whose id holds another's", contactIDs(t, srv, "PEERAAA12"), []string{"PEERDDD4", "operator:alice"})
	same("the panel's inbox", contactIDs(t, srv, "panel"),
		[]string{"PEERAAA1", "PEERAAA12", "PEERBBB2", "PEERCCC3", "PEERDDD4", "operator:alice"})
}
