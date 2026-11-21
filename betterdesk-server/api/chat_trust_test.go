package api

import (
	"encoding/json"
	"net/http"
	"testing"
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
