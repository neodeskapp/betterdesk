package mailcatalog

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	netmail "net/mail"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/console/brandmail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/i18n"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail/message"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// wire is a transport that builds each message as the SMTP transport
// does and keeps it.
type wire struct{ raw [][]byte }

func (w *wire) Send(_ context.Context, _ mail.Settings, m mail.Outgoing) error {
	built, err := message.Build(message.Mail{From: m.From, To: m.To, Subject: m.Subject, Text: m.Text, HTML: m.HTML, Inline: m.Inline, Lang: m.Lang,
		ReplyTo: m.ReplyTo, Headers: m.Headers, MessageIDDomain: m.MessageIDDomain}, message.Options{})
	if err != nil {
		return err
	}
	w.raw = append(w.raw, built.Raw)
	return nil
}
func (w *wire) Verify(context.Context, mail.Settings) error { return nil }

// parsed is a message taken apart.
type parsed struct {
	header      netmail.Header
	text, html  string
	images      map[string]string // Content-ID -> type
	related     bool
	alternative bool
}

func parse(t *testing.T, raw []byte) parsed {
	t.Helper()
	msg, err := netmail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	p := parsed{header: msg.Header, images: map[string]string{}}
	mt, params, _ := mime.ParseMediaType(msg.Header.Get("Content-Type"))
	if mt != "multipart/alternative" {
		t.Fatalf("root is %s", mt)
	}
	p.alternative = true
	alt := multipart.NewReader(msg.Body, params["boundary"])
	for {
		part, err := alt.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		pt, pp, _ := mime.ParseMediaType(part.Header.Get("Content-Type"))
		switch pt {
		case "text/plain":
			p.text = body(part)
		case "multipart/related":
			p.related = true
			rel := multipart.NewReader(part, pp["boundary"])
			for {
				rp, err := rel.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				rt, _, _ := mime.ParseMediaType(rp.Header.Get("Content-Type"))
				if rt == "text/html" {
					p.html = body(rp)
				} else {
					p.images[strings.Trim(rp.Header.Get("Content-ID"), "<>")] = rt
				}
			}
		}
	}
	return p
}

// body is a part's text, decoded, with LF line ends.
func body(p *multipart.Part) string {
	var r io.Reader = p
	if strings.EqualFold(p.Header.Get("Content-Transfer-Encoding"), "base64") {
		r = base64.NewDecoder(base64.StdEncoding, p)
	}
	b, _ := io.ReadAll(r)
	return strings.ReplaceAll(string(b), "\r\n", "\n")
}

var (
	leftover   = regexp.MustCompile(`\{[a-z_]+\}|\b(mail|security_notice|email_change|onboard|password_link|account_mail|impersonation|chat|settings)\.[a-z_]+\.?[a-z_]*\b`)
	remoteRef  = regexp.MustCompile(`(?i)(<img[^>]+src="https?:|<link|<script|@import|url\()`)
	imgTags    = regexp.MustCompile(`<img [^>]*>`)
	cidRef     = regexp.MustCompile(`src="cid:([^"]+)"`)
	buttonLink = regexp.MustCompile(`<a class="nd-on nd-btn-a" href="([^"]+)"`)
)

// dated are the kinds that name a time, with the time the sample gives.
func dated(now time.Time) map[string]time.Time {
	week, hour := now.Add(7*24*time.Hour), now.Add(time.Hour)
	return map[string]time.Time{
		"invite": week, "support": hour, "account-invite": week, "reset": hour, "reset-self": hour,
		"email-confirm": now.Add(24 * time.Hour), "expiry": week, "inactive": week, "security-signin": now,
		"security-password": now, "security-two-factor": now, "security-email": now, "impersonation": now,
		"chat": now.Add(-15 * time.Minute), "alert": now,
	}
}

// automatic are the kinds the server sends on its own.
var automatic = map[string]bool{
	"reset-self": true, "email-notice": true, "expiry": true, "inactive": true, "security-signin": true, "security-password": true,
	"security-two-factor": true, "security-email": true, "impersonation": true, "chat": true, "alert": true, "help": true,
}

// TestEveryMail builds every kind of mail in English, German and Arabic,
// with NeoDesk's brand and a custom one, sends it through the mail
// service and checks the message as a mail client gets it.
func TestEveryMail(t *testing.T) {
	old := time.Local
	time.Local = time.FixedZone("CET", 3600)
	defer func() { time.Local = old }()
	ctx := context.Background()
	bundle := i18n.New(filepath.Join("..", "..", "..", "static", "files", "lang"), "en")
	bundle.Load()
	db := storetest.Migrated(t)
	if _, err := db.Exec(ctx, `INSERT INTO settings (key, value) VALUES ('smtp_config', '{"host":"smtp.example.com","from":"neodesk@localhost"}')`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 26, 0, 12, 0, 0, time.UTC)
	logo := func() ([]byte, string) {
		var buf bytes.Buffer
		buf.Write(brandmail.Inline(brandmail.Branding{Server: "x"})[0].Data) // the mark, a PNG
		return buf.Bytes(), "image/png"
	}
	for _, brand := range []brandmail.Branding{{Server: "NeoDesk"}, {Server: "Acme IT", Accent: "#0f766e"}} {
		if brand.Server == "Acme IT" {
			brand.LogoImage, brand.LogoType = logo()
		}
		w := &wire{}
		svc := mail.New(mail.Options{DB: db, Transport: w, Texts: func() *i18n.Bundle { return bundle },
			Branding: func() brandmail.Branding { return brand },
			Getenv:   func(k string) string { return map[string]string{"PANEL_PUBLIC_URL": "https://desk.example.com"}[k] }})
		src := Source{Bundle: bundle, Mail: svc, Base: "https://desk.example.com", Server: brand.Server, Now: now}
		for _, lang := range []string{"en", "de", "ar"} {
			for _, kind := range Kinds {
				name := brand.Server + "/" + lang + "/" + kind
				m, err := src.Build(ctx, kind, lang)
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				before := len(w.raw)
				if ok, _ := svc.SendEmail(ctx, mail.Email{To: []string{"reader@example.org"}, Mail: m}); !ok || len(w.raw) != before+1 {
					t.Fatalf("%s: not sent", name)
				}
				p := parse(t, w.raw[len(w.raw)-1])
				checkMail(t, name, kind, lang, brand, m, p, dated(now))
			}
		}
	}
}

func checkMail(t *testing.T, name, kind, lang string, brand brandmail.Branding, m brandmail.Mail, p parsed, dates map[string]time.Time) {
	t.Helper()
	dec := new(mime.WordDecoder)
	subject, _ := dec.DecodeHeader(p.header.Get("Subject"))
	// Both parts, the HTML with its images
	if p.text == "" || p.html == "" || !p.related {
		t.Errorf("%s: text %d bytes, html %d bytes, related %v", name, len(p.text), len(p.html), p.related)
	}
	// Subject and preheader are set and differ; the subject is short in
	// English and German
	if subject == "" || m.Preheader == "" || strings.EqualFold(subject, m.Preheader) {
		t.Errorf("%s: subject %q, preheader %q", name, subject, m.Preheader)
	}
	if (lang == "en" || lang == "de") && utf8.RuneCountInString(subject) > 60 {
		t.Errorf("%s: subject of %d characters: %q", name, utf8.RuneCountInString(subject), subject)
	}
	if !strings.Contains(p.html, `class="nd-preheader"`) {
		t.Errorf("%s: no preheader in the HTML", name)
	}
	// Exactly one primary button, whose link is in both parts
	links := buttonLink.FindAllStringSubmatch(p.html, -1)
	if len(links) != 1 {
		t.Fatalf("%s: %d buttons", name, len(links))
	}
	link := strings.ReplaceAll(links[0][1], "&amp;", "&")
	if !strings.Contains(p.text, link) || !strings.Contains(p.html, `<v:roundrect`) {
		t.Errorf("%s: the link %s is not in the text part, or no Outlook button", name, link)
	}
	// Images are carried in the mail, with alt text; nothing is loaded
	for _, tag := range imgTags.FindAllString(p.html, -1) {
		if !strings.Contains(tag, ` alt="`) {
			t.Errorf("%s: image without alt: %s", name, tag)
		}
	}
	for _, ref := range cidRef.FindAllStringSubmatch(p.html, -1) {
		if p.images[ref[1]] == "" {
			t.Errorf("%s: shows cid:%s, which the mail doesn't carry", name, ref[1])
		}
	}
	if len(p.images) == 0 || remoteRef.MatchString(p.html) {
		t.Errorf("%s: %d images, or something loads from elsewhere", name, len(p.images))
	}
	// No placeholder or key is left
	for part, body := range map[string]string{"text": p.text, "html": p.html, "subject": subject} {
		if found := leftover.FindString(body); found != "" && !strings.Contains(found, "example") {
			t.Errorf("%s: %s part has %q", name, part, found)
		}
	}
	// The reader's language
	dir := "ltr"
	if lang == "ar" {
		dir = "rtl"
	}
	if p.header.Get("Content-Language") != lang || !strings.Contains(p.html, `<html lang="`+lang+`" dir="`+dir+`"`) {
		t.Errorf("%s: Content-Language %q", name, p.header.Get("Content-Language"))
	}
	// Times the reader's way, never a bare UTC timestamp
	if at, ok := dates[kind]; ok {
		day := brandmail.Date(at, lang, time.Local)
		if !strings.Contains(p.text, day) {
			t.Errorf("%s: no %q in the text part:\n%s", name, day, p.text)
		}
	}
	if regexp.MustCompile(`\d{4}-\d{2}-\d{2} \d{2}:\d{2}`).MatchString(p.text) {
		t.Errorf("%s: a raw timestamp in:\n%s", name, p.text)
	}
	// The From name, the Message-ID domain, the headers of automatic mails
	// and notifications
	from, err := netmail.ParseAddress(p.header.Get("From"))
	wantFrom := "NeoDesk"
	if brand.OwnName() {
		wantFrom = brand.Server + " via NeoDesk"
	}
	if err != nil || from.Name != wantFrom {
		t.Errorf("%s: From %q, want the name %q", name, p.header.Get("From"), wantFrom)
	}
	if !strings.HasSuffix(p.header.Get("Message-ID"), "@desk.example.com>") {
		t.Errorf("%s: Message-ID %s", name, p.header.Get("Message-ID"))
	}
	if got := p.header.Get("Auto-Submitted"); (got == "auto-generated") != automatic[kind] {
		t.Errorf("%s: Auto-Submitted %q", name, got)
	}
	_, notification := Notifications[kind]
	unsub := p.header.Get("List-Unsubscribe")
	if notification != strings.HasPrefix(unsub, "<https://desk.example.com/mail/unsubscribe?t=") ||
		notification != (p.header.Get("List-Unsubscribe-Post") == "List-Unsubscribe=One-Click") {
		t.Errorf("%s: List-Unsubscribe %q", name, unsub)
	}
	// The footer says which server sent it and why
	if m.Why == "" || !strings.Contains(p.text, "-- \n") {
		t.Errorf("%s: no reason in the footer", name)
	}
	// Codes stay out of the subject
	if strings.Contains(subject, "9H83") {
		t.Errorf("%s: the code is in the subject", name)
	}
}
