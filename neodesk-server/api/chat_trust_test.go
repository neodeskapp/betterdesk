package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// TestChatSenderAndGroupDelete: a message posted over the network carries
// the caller as its sender, whatever the body names, and only the console
// names the sender itself; a delete removes only a chat group, and only
// for its creator or an admin (RV179).
func TestChatSenderAndGroupDelete(t *testing.T) {
	srv, database := newInternalServer(t, nil)
	viewer := map[string]string{"X-API-Key": testViewerKey}
	operator := map[string]string{"X-API-Key": testOperatorKey}

	if status, body := listenerCall(t, srv, http.MethodPost, "/api/chat/messages",
		`{"conversation_id":"PEERAAA1","from_id":"operator","from_name":"the-boss","to_id":"PEERAAA1","text":"hi"}`, viewer); status != http.StatusOK {
		t.Fatalf("viewer posts: %d %s", status, body)
	}
	if status, body := asConsole(t, srv, http.MethodPost, "/api/chat/messages",
		`{"conversation_id":"PEERAAA1","from_id":"operator","from_name":"Operator Olga","to_id":"PEERAAA1","text":"hello"}`); status != http.StatusOK {
		t.Fatalf("console posts: %d %s", status, body)
	}
	msgs, err := database.GetChatHistory("PEERAAA1", 10)
	if err != nil || len(msgs) != 2 {
		t.Fatalf("history: %v %d", err, len(msgs))
	}
	senders := map[string]string{}
	for _, m := range msgs {
		senders[m.Text] = m.FromID + "/" + m.FromName
	}
	if got := senders["hi"]; got != "apikey:viewer-key/apikey:viewer-key" {
		t.Errorf("the viewer's message has the sender %q", got)
	}
	if got := senders["hello"]; got != "operator/Operator Olga" {
		t.Errorf("the console's message has the sender %q", got)
	}

	if status, body := listenerCall(t, srv, http.MethodDelete, "/api/chat/groups/PEERAAA1", "", viewer); status != http.StatusNotFound {
		t.Errorf("delete a device conversation as a group: %d %s, want 404", status, body)
	}
	if msgs, _ := database.GetChatHistory("PEERAAA1", 10); len(msgs) != 2 {
		t.Errorf("the device conversation lost messages: %d left", len(msgs))
	}

	status, body := listenerCall(t, srv, http.MethodPost, "/api/chat/groups", `{"name":"team","members":["PEERAAA1"],"created_by":"apikey:viewer-key"}`, operator)
	var group struct {
		ID        string `json:"id"`
		CreatedBy string `json:"created_by"`
	}
	if err := json.Unmarshal([]byte(body), &group); status != http.StatusOK || err != nil || group.ID == "" {
		t.Fatalf("create group: %d %s", status, body)
	}
	if group.CreatedBy != "apikey:operator-key" {
		t.Errorf("the group's creator is %q, want the caller", group.CreatedBy)
	}
	if status, body := listenerCall(t, srv, http.MethodDelete, "/api/chat/groups/"+group.ID, "", viewer); status != http.StatusForbidden {
		t.Errorf("another account deletes the group: %d %s, want 403", status, body)
	}
	if status, body := listenerCall(t, srv, http.MethodDelete, "/api/chat/groups/"+group.ID, "", operator); status != http.StatusOK {
		t.Errorf("the creator deletes the group: %d %s, want 200", status, body)
	}
}

// TestChatGroupUpdateAndReadMarksCheckTheID: a group update needs an
// existing group and its creator or an admin, like the delete; a read mark
// from a caller other than the console marks as the caller, needs an
// existing group for a group conversation and a device in the caller's
// scope for a device conversation.
func TestChatGroupUpdateAndReadMarksCheckTheID(t *testing.T) {
	srv := scopedFixture(t)
	database := srv.db
	viewer := map[string]string{"X-API-Key": testViewerKey}
	operator := map[string]string{"X-API-Key": testOperatorKey}

	status, body := listenerCall(t, srv, http.MethodPost, "/api/chat/groups", `{"name":"team","members":["PEERBBB2"]}`, operator)
	var group struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &group); status != http.StatusOK || err != nil || group.ID == "" {
		t.Fatalf("create group: %d %s", status, body)
	}
	update := `{"name":"renamed","members":["PEERBBB2"]}`
	if status, body := listenerCall(t, srv, http.MethodPut, "/api/chat/groups/group:nosuch", update, operator); status != http.StatusNotFound {
		t.Errorf("update an unknown group: %d %s, want 404", status, body)
	}
	if status, body := listenerCall(t, srv, http.MethodPut, "/api/chat/groups/"+group.ID, update, viewer); status != http.StatusForbidden {
		t.Errorf("another account updates the group: %d %s, want 403", status, body)
	}
	if status, body := listenerCall(t, srv, http.MethodPut, "/api/chat/groups/"+group.ID, update, operator); status != http.StatusOK {
		t.Errorf("the creator updates the group: %d %s, want 200", status, body)
	}

	for _, m := range []db.ChatMessage{
		{ConversationID: "PEERBBB2", FromID: "operator:alice", Text: "one"},
		{ConversationID: "PEERAAA1", FromID: "PEERAAA1", Text: "two"},
	} {
		if _, err := database.SaveChatMessage(&m); err != nil {
			t.Fatal(err)
		}
	}
	// reader_id names the message's sender, which would mark nothing read.
	if status, body := listenerCall(t, srv, http.MethodPost, "/api/chat/read", `{"conversation_id":"PEERBBB2","reader_id":"operator:alice"}`, operator); status != http.StatusOK {
		t.Fatalf("mark read: %d %s", status, body)
	}
	if n, _ := database.GetUnreadCount("PEERBBB2"); n != 0 {
		t.Errorf("unread of PEERBBB2 after the operator's read mark: %d, want 0 (marked as the operator)", n)
	}
	if status, body := listenerCall(t, srv, http.MethodPost, "/api/chat/read", `{"conversation_id":"PEERAAA1","reader_id":"x"}`, operator); status != http.StatusForbidden {
		t.Errorf("mark read for a device outside the scope: %d %s, want 403", status, body)
	}
	if status, body := listenerCall(t, srv, http.MethodPost, "/api/chat/read", `{"conversation_id":"group:nosuch","reader_id":"x"}`, operator); status != http.StatusNotFound {
		t.Errorf("mark read for an unknown group: %d %s, want 404", status, body)
	}
}
