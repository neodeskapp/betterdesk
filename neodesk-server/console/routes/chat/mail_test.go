package chat

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/brandmail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/chatstore"
	"github.com/neodeskapp/neodesk/neodesk-server/console/i18n"
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

// texts are the console's languages, as the server loads them.
func texts(t *testing.T) *i18n.Bundle {
	t.Helper()
	b := i18n.New("../../static/files/lang", "en")
	b.Load()
	if got := b.Translate("de", "chat.mail_subject", nil); got == "chat.mail_subject" {
		t.Fatalf("no text for the mail: %q", got)
	}
	return b
}

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
		if err := hub.mailWaiting(ctx, sent, brandmail.Branding{Server: "NeoDesk"}, texts(t), now.Add(-10*time.Minute)); err != nil || len(sent.list) != 0 {
			t.Fatalf("after 10 minutes: %d mails, %v", len(sent.list), err)
		}
		if err := hub.mailWaiting(ctx, sent, brandmail.Branding{Server: "NeoDesk"}, texts(t), now); err != nil {
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
		if err := hub.mailWaiting(ctx, sent, brandmail.Branding{Server: "NeoDesk"}, texts(t), now.Add(time.Hour)); err != nil || len(sent.list) != 1 {
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
		if err := hub.mailWaiting(ctx, sent, brandmail.Branding{Server: "NeoDesk"}, texts(t), now); err != nil || len(sent.list) != 2 {
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
		if err := hub.mailWaiting(ctx, sent, brandmail.Branding{Server: "NeoDesk"}, texts(t), now.Add(time.Hour)); err != nil || len(sent.list) != 2 {
			t.Errorf("with the mail off: %d mails, %v", len(sent.list), err)
		}
	})
}

// TestWaitMailSpeaksTheRecipientsLanguage: each recipient gets the mail in
// the language of the account (A242, the lead's call on question 1); an
// account without a language, or with one the console doesn't have, gets
// English.
func TestWaitMailSpeaksTheRecipientsLanguage(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		if err := chatstore.Ensure(ctx, db); err != nil {
			t.Fatal(err)
		}
		scope := realScope(t, db)
		for name, lang := range map[string]string{"anna": "de", "admin": "xx"} {
			if _, err := db.Exec(ctx, `UPDATE users SET email = ?, preferred_language = ? WHERE username = ?`, name+"@example.test", lang, name); err != nil {
				t.Fatal(err)
			}
		}
		now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
		clock := now.Add(-20 * time.Minute)
		st := chatstore.New(db, func() time.Time { return clock })
		hub := NewHub(HubOptions{Store: st, Scope: scope, Holds: func(_ context.Context, role, p string) (bool, error) {
			return p != permAssign || role != "viewer", nil
		}})
		if _, err := st.Post(ctx, chatstore.Message{ConversationID: "dev1", From: chatstore.Device("dev1", "reception-01"), Text: "Der Drucker blinkt"}, nil); err != nil {
			t.Fatal(err)
		}
		sent := &sentMails{}
		if err := hub.mailWaiting(ctx, sent, brandmail.Branding{Server: "NeoDesk"}, texts(t), now); err != nil {
			t.Fatal(err)
		}
		byTo := map[string]string{}
		for _, m := range sent.list {
			byTo[strings.Join(m.To, ",")] = m.Subject + "\n" + *m.Text + "\n" + *m.HTML
		}
		if len(sent.list) != 2 {
			t.Fatalf("%d mails, want one per language: %q", len(sent.list), byTo)
		}
		de := byTo["anna@example.test"]
		for _, want := range []string{"NeoDesk: dev1 wartet auf eine Antwort", "seit 20 Minuten", "Der Drucker blinkt", "https://console.example/chat?c=dev1"} {
			if !strings.Contains(de, want) {
				t.Errorf("anna's mail has no %q:\n%s", want, de)
			}
		}
		en := byTo["admin@example.test"]
		for _, want := range []string{"NeoDesk: dev1 is waiting for an answer", "waited 20 minutes", "Der Drucker blinkt"} {
			if !strings.Contains(en, want) {
				t.Errorf("admin's mail has no %q:\n%s", want, en)
			}
		}
	})
}

// TestWaitMailIsBranded: the mail comes in the server's branded layout,
// as the invite does: its name or logo, its accent colour on the one
// button and on the quote, and a footer that says why the mail came and
// where to turn it off.
func TestWaitMailIsBranded(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		if err := chatstore.Ensure(ctx, db); err != nil {
			t.Fatal(err)
		}
		scope := realScope(t, db)
		if _, err := db.Exec(ctx, `UPDATE users SET email = 'anna@example.test' WHERE username = 'anna'`); err != nil {
			t.Fatal(err)
		}
		now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
		clock := now.Add(-20 * time.Minute)
		st := chatstore.New(db, func() time.Time { return clock })
		hub := NewHub(HubOptions{Store: st, Scope: scope, Holds: func(_ context.Context, role, p string) (bool, error) {
			return p != permAssign || role != "viewer", nil
		}})
		if _, err := st.Post(ctx, chatstore.Message{ConversationID: "dev1", From: chatstore.Device("dev1", "reception-01"), Text: "The printer blinks"}, nil); err != nil {
			t.Fatal(err)
		}
		sent := &sentMails{}
		brand := brandmail.Branding{Server: "Acme Desk", Accent: "#FF0000", Logo: "https://desk.example/logo.png"}
		if err := hub.mailWaiting(ctx, sent, brand, texts(t), now); err != nil || len(sent.list) == 0 {
			t.Fatalf("%d mails, %v", len(sent.list), err)
		}
		m := sent.list[0]
		for _, want := range []string{`role="presentation"`, `<img src="https://desk.example/logo.png" alt="Acme Desk"`,
			`bgcolor="#ff0000"`, `href="https://console.example/chat?c=dev1"`, `border-left: 3px solid #ff0000;`, "The printer blinks",
			"Chat notifications"} {
			if !strings.Contains(*m.HTML, want) {
				t.Errorf("the mail has no %s", want)
			}
		}
		if m.Subject != "Acme Desk: dev1 is waiting for an answer" || !strings.Contains(*m.Text, "Chat notifications") {
			t.Errorf("subject %q, text %q", m.Subject, *m.Text)
		}
	})
}
