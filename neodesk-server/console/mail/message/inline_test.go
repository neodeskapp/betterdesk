package message

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"net/mail"
	"strings"
	"testing"
)

// A mail with inline images is text and, as its alternative, the HTML
// with the images in a multipart/related part; each image keeps its
// Content-ID and bytes. A bad image is left out, and without images the
// message is the same as before.
func TestInlineImages(t *testing.T) {
	text, html := "Hello", `<p>Hi <img src="cid:logo@x"></p>`
	png := []byte("\x89PNG\r\n\x1a\nfake image bytes that are long enough to wrap the base64 lines of the part")
	m := Mail{From: "a@example.com", To: []string{"b@example.com"}, Subject: "S", Text: &text, HTML: &html,
		Inline: []Inline{
			{ID: "logo@x", ContentType: "image/png", Filename: "logo.png", Data: png},
			{ID: "bad>id", ContentType: "image/png", Filename: "x.png", Data: png},
			{ID: "svg@x", ContentType: "image/svg+xml", Filename: "x.svg", Data: png},
		}}
	built, err := Build(m, Options{})
	if err != nil {
		t.Fatal(err)
	}
	msg, err := mail.ReadMessage(bytes.NewReader(built.Raw))
	if err != nil {
		t.Fatal(err)
	}
	mediaType, params, _ := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if mediaType != "multipart/alternative" {
		t.Fatalf("root %s", mediaType)
	}
	alt := multipart.NewReader(msg.Body, params["boundary"])
	first, _ := alt.NextPart()
	if body, _ := io.ReadAll(first); string(body) != text {
		t.Errorf("text part %q", body)
	}
	second, err := alt.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	relType, relParams, _ := mime.ParseMediaType(second.Header.Get("Content-Type"))
	if relType != "multipart/related" || relParams["type"] != "text/html" {
		t.Fatalf("second part %s %v", relType, relParams)
	}
	rel := multipart.NewReader(second, relParams["boundary"])
	htmlPart, _ := rel.NextPart()
	if body, _ := io.ReadAll(htmlPart); !strings.Contains(string(body), "cid:logo@x") {
		t.Errorf("html part %q", body)
	}
	img, err := rel.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	if img.Header.Get("Content-ID") != "<logo@x>" || !strings.HasPrefix(img.Header.Get("Content-Disposition"), "inline") {
		t.Errorf("image headers %v", img.Header)
	}
	if img.Header.Get("Content-Transfer-Encoding") != "base64" {
		t.Errorf("image encoding %q", img.Header.Get("Content-Transfer-Encoding"))
	}
	if data, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, img)); err != nil || !bytes.Equal(data, png) {
		t.Errorf("image bytes differ: %v", err)
	}
	if _, err := rel.NextPart(); err != io.EOF {
		t.Errorf("a bad image went in: %v", err)
	}

	// Only HTML: the related part is the message
	m.Text = nil
	built, err = Build(m, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if msg, _ := mail.ReadMessage(bytes.NewReader(built.Raw)); !strings.HasPrefix(msg.Header.Get("Content-Type"), "multipart/related;") {
		t.Errorf("HTML only: %s", msg.Header.Get("Content-Type"))
	}

	// Without images, or with only bad ones, nothing changes
	m.Text = &text
	fixed := func(inline []Inline) []byte {
		c := m
		c.Inline = inline
		out, err := Build(c, Options{Rand: bytes.NewReader(make([]byte, 256))})
		if err != nil {
			t.Fatal(err)
		}
		return out.Raw
	}
	if !bytes.Equal(fixed(nil), fixed([]Inline{{ID: "bad>id", ContentType: "image/png", Filename: "x.png", Data: png}})) {
		t.Error("bad images changed the message")
	}
}
