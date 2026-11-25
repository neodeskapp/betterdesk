package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/chatstore"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

type sentMails struct{ list []mail.Email }

func (s *sentMails) SendEmail(_ context.Context, m mail.Email) (bool, error) {
	s.list = append(s.list, m)
	return true, nil
}
func (s *sentMails) GetPanelBaseURL() string { return "https://console.example" }

// TestWaitMail: a person who waited longer than the set minutes gets the
// console a mail: to whoever has the conversation, else to those who may
// assign it and see the computer, with chat mails on. It quotes the start
// of the message unless that is off, links to the conversation, and goes
// once per wait. 0 minutes sends nothing.
func TestWaitMail(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		if err := chatstore.Ensure(ctx, db); err != nil {
			t.Fatal(err)
		}
		scope := realScope(t, db)
		for name, email := range map[string]string{"anna": "anna@example.test", "ben": "ben@example.test", "cleo": "cleo@example.test", "admin": "admin@example.test"} {
			if _, err := db.Exec(ctx, `UPDATE users SET email = ? WHERE username = ?`, email, name); err != nil {
				t.Fatal(err)
			}
		}
		now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
		clock := now.Add(-20 * time.Minute)
		st := chatstore.New(db, func() time.Time { return clock })
		// anna (operator) and admin may assign and see dev1; ben sees dev2
		// only; cleo is a viewer, who may not assign
		hub := NewHub(HubOptions{Store: st, Scope: scope, Holds: func(_ context.Context, role, p string) (bool, error) {
			return p != permAssign || role != "viewer", nil
		}})
		if err := st.SetSetting(ctx, "account:1", "1,0,0"); err != nil { // admin: no chat mails
			t.Fatal(err)
		}
		long := "My printer stopped printing this morning and now it blinks red and orange and shows error E-42 forever"
		if _, err := st.Post(ctx, chatstore.Message{ConversationID: "dev1", From: chatstore.Device("dev1", "reception-01"), Text: long}, nil); err != nil {
			t.Fatal(err)
		}
		sent := &sentMails{}
		if err := hub.mailWaiting(ctx, sent, "NeoDesk", now.Add(-10*time.Minute)); err != nil || len(sent.list) != 0 {
			t.Fatalf("after 10 minutes: %d mails, %v", len(sent.list), err)
		}
		if err := hub.mailWaiting(ctx, sent, "NeoDesk", now); err != nil {
			t.Fatal(err)
		}
		if len(sent.list) != 1 {
			t.Fatalf("after 20 minutes: %d mails", len(sent.list))
		}
		m := sent.list[0]
		if strings.Join(m.To, ",") != "anna@example.test" {
			t.Errorf("to %v, want anna only (admin has chat mails off, ben doesn't see dev1, cleo may not assign)", m.To)
		}
		if !strings.Contains(m.Subject, "is waiting for an answer") || !strings.Contains(*m.Text, "waited 20 minutes") ||
			!strings.Contains(*m.Text, "My printer stopped printing this morning and now it blinks red and orange and sh…") ||
			strings.Contains(*m.Text, "E-42") || !strings.Contains(*m.Text, "https://console.example/chat?c=dev1") {
			t.Errorf("mail: %s\n%s", m.Subject, *m.Text)
		}
		if err := hub.mailWaiting(ctx, sent, "NeoDesk", now.Add(time.Hour)); err != nil || len(sent.list) != 1 {
			t.Errorf("a second mail for the same wait: %d, %v", len(sent.list), err)
		}

		// An answer ends the wait; the next wait goes to the assignee, without the quote
		if _, err := st.Assign(ctx, "dev1", 6); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Post(ctx, chatstore.Message{ConversationID: "dev1", From: chatstore.User(5, "anna"), Text: "on it"}, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Post(ctx, chatstore.Message{ConversationID: "dev1", From: chatstore.Device("dev1", "reception-01"), Text: "still broken"}, nil); err != nil {
			t.Fatal(err)
		}
		if err := st.SetSetting(ctx, settingMailPreview, "0"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO user_peer_grants (user_id, peer_id) VALUES (6, 'dev1')`); err != nil {
			t.Fatal(err)
		}
		if err := hub.mailWaiting(ctx, sent, "NeoDesk", now); err != nil || len(sent.list) != 2 {
			t.Fatalf("the second wait: %d mails, %v", len(sent.list), err)
		}
		m = sent.list[1]
		if strings.Join(m.To, ",") != "ben@example.test" || strings.Contains(*m.Text, "still broken") {
			t.Errorf("second mail to %v: %s", m.To, *m.Text)
		}

		if err := st.SetSetting(ctx, settingMailAfter, "0"); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Post(ctx, chatstore.Message{ConversationID: "dev1", From: chatstore.User(6, "ben"), Text: "ok"}, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := st.Post(ctx, chatstore.Message{ConversationID: "dev1", From: chatstore.Device("dev1", "reception-01"), Text: "again"}, nil); err != nil {
			t.Fatal(err)
		}
		if err := hub.mailWaiting(ctx, sent, "NeoDesk", now.Add(time.Hour)); err != nil || len(sent.list) != 2 {
			t.Errorf("with the mail off: %d mails, %v", len(sent.list), err)
		}
	})
}
