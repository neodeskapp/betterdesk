package api

import (
	"encoding/json"
	"net/http"
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
