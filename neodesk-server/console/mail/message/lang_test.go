package message

import (
	"bytes"
	"strings"
	"testing"
)

// A mail written in a language says so in a Content-Language header; a
// mail without one is built as before, with no such header.
func TestContentLanguage(t *testing.T) {
	text, html := "Hallo", "<p>Hallo</p>"
	m := Mail{From: "a@example.org", To: []string{"b@example.org"}, Subject: "Hi", Text: &text, HTML: &html, Lang: "de"}
	got, err := Build(m, Options{Rand: bytes.NewReader(make([]byte, 64))})
	if err != nil {
		t.Fatal(err)
	}
	head, _, _ := strings.Cut(string(got.Raw), "\r\n\r\n")
	if !strings.Contains(head, "\r\nContent-Language: de\r\n") {
		t.Errorf("no Content-Language header:\n%s", head)
	}
	m.Lang = ""
	got, err = Build(m, Options{Rand: bytes.NewReader(make([]byte, 64))})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got.Raw), "Content-Language") {
		t.Error("a mail without a language got a Content-Language header")
	}
	// A language that could break the header is left out
	m.Lang = "de\r\nBcc: x@example.org"
	got, err = Build(m, Options{Rand: bytes.NewReader(make([]byte, 64))})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got.Raw), "Bcc:") || strings.Contains(string(got.Raw), "Content-Language") {
		t.Error("a language with a line break got into the header")
	}
}
