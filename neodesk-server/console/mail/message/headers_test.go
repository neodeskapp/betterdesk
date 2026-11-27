package message

import (
	"bytes"
	"net/mail"
	"strings"
	"testing"
)

// Reply-To, the extra headers and the Message-ID's domain go into the
// message; a header that could break it is left out, and a mail without
// them is built as before.
func TestReplyToHeadersAndMessageID(t *testing.T) {
	text := "Hello"
	plain := Mail{From: "neodesk@localhost", To: []string{"b@example.com"}, Subject: "S", Text: &text}
	before, err := Build(plain, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(before.MessageID, "@localhost>") || bytes.Contains(before.Raw, []byte("Reply-To")) {
		t.Fatalf("a plain mail changed: %s", before.Raw)
	}

	m := plain
	m.From = `"Acme IT via NeoDesk" <neodesk@localhost>`
	m.ReplyTo = "Jana Weber <jana@acme.example>"
	m.Headers = []Header{
		{Name: "Auto-Submitted", Value: "auto-generated"},
		{Name: "List-Unsubscribe", Value: "<https://desk.acme.example/mail/unsubscribe?t=abc>"},
		{Name: "List-Unsubscribe-Post", Value: "List-Unsubscribe=One-Click"},
		{Name: "Bad Name", Value: "x"},
		{Name: "X-Injected", Value: "a\r\nBcc: evil@example.com"},
	}
	m.MessageIDDomain = "desk.acme.example"
	built, err := Build(m, Options{})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(bytes.NewReader(built.Raw))
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{
		"Reply-To":              "Jana Weber <jana@acme.example>",
		"Auto-Submitted":        "auto-generated",
		"List-Unsubscribe":      "<https://desk.acme.example/mail/unsubscribe?t=abc>",
		"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
	} {
		if got := msg.Header.Get(name); got != want {
			t.Errorf("%s: %q, want %q", name, got, want)
		}
	}
	if from, err := mail.ParseAddress(msg.Header.Get("From")); err != nil || from.Name != "Acme IT via NeoDesk" || from.Address != "neodesk@localhost" {
		t.Errorf("From %q: %v", msg.Header.Get("From"), err)
	}
	if !strings.HasSuffix(msg.Header.Get("Message-ID"), "@desk.acme.example>") {
		t.Errorf("Message-ID %s", msg.Header.Get("Message-ID"))
	}
	if msg.Header.Get("Bcc") != "" || msg.Header.Get("X-Injected") != "" || bytes.Contains(built.Raw, []byte("Bad Name")) {
		t.Errorf("a broken header went in:\n%s", built.Raw)
	}
	if len(built.EnvelopeTo) != 1 {
		t.Errorf("Reply-To changed the envelope: %v", built.EnvelopeTo)
	}
}
