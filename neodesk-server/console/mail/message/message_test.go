package message

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func ptr(s string) *string { return &s }

// sequence gives the bytes 0x00, 0x01, 0x02 and so on, so a test can tell
// which random bytes went where.
type sequence struct{ next byte }

func (s *sequence) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = s.next
		s.next++
	}
	return len(p), nil
}

// fixed is the clock of the tests: a time in a zone that is not UTC.
func fixed() time.Time {
	return time.Date(2026, time.November, 10, 21, 25, 17, 987654321, time.FixedZone("CET", 3600))
}

func build(t *testing.T, m Mail) *Message {
	t.Helper()
	message, err := Build(m, Options{Now: fixed, Rand: &sequence{}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return message
}

// TestBuildShapes pins the whole message for the four ways a mail can have
// bodies.
func TestBuildShapes(t *testing.T) {
	const head = "From: betterdesk@localhost\r\nTo: admin@example.com\r\nSubject: Hello\r\n"
	const date = "Date: Tue, 10 Nov 2026 20:25:17 +0000\r\nMIME-Version: 1.0\r\n"
	tests := []struct {
		name       string
		text, html *string
		id         string
		want       string
	}{
		{
			name: "text only", text: ptr("Hi there\n"),
			id: "<08090a0b-0c0d-0e0f-1011-121314151617@localhost>",
			want: head + "Message-ID: <08090a0b-0c0d-0e0f-1011-121314151617@localhost>\r\n" +
				"Content-Transfer-Encoding: 7bit\r\n" + date +
				"Content-Type: text/plain; charset=utf-8\r\n\r\nHi there\n",
		},
		{
			name: "html only", html: ptr("<p>Gr\u00fc\u00dfe</p>"),
			id: "<08090a0b-0c0d-0e0f-1011-121314151617@localhost>",
			want: head + "Message-ID: <08090a0b-0c0d-0e0f-1011-121314151617@localhost>\r\n" +
				"Content-Transfer-Encoding: quoted-printable\r\n" + date +
				"Content-Type: text/html; charset=utf-8\r\n\r\n<p>Gr=C3=BC=C3=9Fe</p>\r\n",
		},
		{
			name: "text and html", text: ptr("Hi"), html: ptr("<p>Hi</p>"),
			id: "<18191a1b-1c1d-1e1f-2021-222324252627@localhost>",
			want: head + "Message-ID: <18191a1b-1c1d-1e1f-2021-222324252627@localhost>\r\n" + date +
				"Content-Type: multipart/alternative;\r\n boundary=\"--_NmP-0001020304050607-Part_1\"\r\n\r\n" +
				"----_NmP-0001020304050607-Part_1\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 7bit\r\n\r\nHi\r\n" +
				"----_NmP-0001020304050607-Part_1\r\nContent-Type: text/html; charset=utf-8\r\nContent-Transfer-Encoding: 7bit\r\n\r\n<p>Hi</p>\r\n" +
				"----_NmP-0001020304050607-Part_1--\r\n",
		},
		{
			name: "no body",
			id:   "<08090a0b-0c0d-0e0f-1011-121314151617@localhost>",
			want: head + "Message-ID: <08090a0b-0c0d-0e0f-1011-121314151617@localhost>\r\n" + date +
				"Content-Type: text/plain\r\n\r\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message := build(t, Mail{From: "betterdesk@localhost", To: []string{"admin@example.com"}, Subject: "Hello", Text: tt.text, HTML: tt.html})
			if string(message.Raw) != tt.want {
				t.Errorf("message\n got  %q\n want %q", message.Raw, tt.want)
			}
			if message.MessageID != tt.id {
				t.Errorf("Message-ID %q, want %q", message.MessageID, tt.id)
			}
			if message.EnvelopeFrom != "betterdesk@localhost" || !slices.Equal(message.EnvelopeTo, []string{"admin@example.com"}) {
				t.Errorf("envelope %q -> %q", message.EnvelopeFrom, message.EnvelopeTo)
			}
		})
	}
}

// TestEmptyValuesAreAlike checks the inputs that nodemailer does not tell
// apart: a missing body and an empty one, no recipients and empty ones.
func TestEmptyValuesAreAlike(t *testing.T) {
	base := Mail{From: "a@example.com", To: []string{"b@example.com"}, Subject: "S", Text: ptr("text")}
	same := func(name string, a, b Mail) {
		t.Helper()
		if x, y := build(t, a), build(t, b); !bytes.Equal(x.Raw, y.Raw) || !slices.Equal(x.EnvelopeTo, y.EnvelopeTo) || x.EnvelopeFrom != y.EnvelopeFrom {
			t.Errorf("%s: the two messages differ\n %q\n %q", name, x.Raw, y.Raw)
		}
	}
	withHTML := base
	withHTML.HTML = ptr("")
	same("html nil and empty", base, withHTML)

	noText, emptyText := base, base
	noText.Text, emptyText.Text = nil, ptr("")
	same("text nil and empty", noText, emptyText)

	noTo, emptyTo, blankTo := base, base, base
	noTo.To, emptyTo.To, blankTo.To = nil, []string{}, []string{"", " "}
	same("to nil and empty list", noTo, emptyTo)
	same("to nil and blank strings", noTo, blankTo)
	if m := build(t, noTo); m.EnvelopeTo == nil || len(m.EnvelopeTo) != 0 || bytes.Contains(m.Raw, []byte("To:")) {
		t.Errorf("no recipients: envelope %#v, message %q", m.EnvelopeTo, m.Raw)
	}

	list := base
	list.To = []string{"b@example.com, c@example.com"}
	split := base
	split.To = []string{"b@example.com", "c@example.com"}
	same("one list and two elements", list, split)

	// a name with a comma is one address in one string and two entries in two
	joined := build(t, Mail{To: []string{"Doe, Jane <jane@example.com>"}})
	apart := build(t, Mail{To: []string{"Doe", "Jane <jane@example.com>"}})
	if !bytes.Contains(joined.Raw, []byte("To: \"Doe, Jane\" <jane@example.com>\r\n")) || !bytes.Contains(apart.Raw, []byte("To: Jane <jane@example.com>\r\n")) {
		t.Errorf("name with a comma:\n one string   %q\n two strings  %q", joined.Raw, apart.Raw)
	}
}

// TestNoSender: without a usable sender there is no From header, the
// envelope sender is empty and the Message-ID is at localhost.
func TestNoSender(t *testing.T) {
	for _, from := range []string{"", " ", "Just A Name", ",", "<>", "root", "<root>"} {
		m := build(t, Mail{From: from, To: []string{"b@example.com"}, Subject: "S", Text: ptr("x")})
		if m.EnvelopeFrom != "" || bytes.Contains(m.Raw, []byte("From:")) || !strings.HasSuffix(m.MessageID, "@localhost>") {
			t.Errorf("from %q: envelope from %q, Message-ID %q, message %q", from, m.EnvelopeFrom, m.MessageID, m.Raw)
		}
	}
	// the first address of a list is the envelope sender, and its domain
	// goes into the Message-ID
	m := build(t, Mail{From: "A <a@One.example>, b@two.example", To: []string{"c@example.com"}})
	if m.EnvelopeFrom != "a@one.example" || !strings.HasSuffix(m.MessageID, "@one.example>") || !bytes.Contains(m.Raw, []byte("From: A <a@one.example>, b@two.example\r\n")) {
		t.Errorf("sender list: envelope from %q, Message-ID %q, message %q", m.EnvelopeFrom, m.MessageID, m.Raw)
	}
	// a sender with an empty domain
	m = build(t, Mail{From: "user@"})
	if m.EnvelopeFrom != "user@" || !strings.HasSuffix(m.MessageID, "-121314151617@>") || !bytes.HasPrefix(m.Raw, []byte("From: <user@>\r\n")) {
		t.Errorf("sender with an empty domain: envelope from %q, Message-ID %q, message %q", m.EnvelopeFrom, m.MessageID, m.Raw)
	}
}

// TestRandomBytes checks how many random bytes Build draws and where they
// go, and that a failing source fails the build.
func TestRandomBytes(t *testing.T) {
	single := Mail{From: "a@example.com", Text: ptr("x")}
	double := Mail{From: "a@example.com", Text: ptr("x"), HTML: ptr("y")}
	for _, tt := range []struct {
		mail  Mail
		drawn int
		id    string
	}{
		{single, 24, "<08090a0b-0c0d-0e0f-1011-121314151617@example.com>"},
		{double, 40, "<18191a1b-1c1d-1e1f-2021-222324252627@example.com>"},
	} {
		source := &sequence{}
		m, err := Build(tt.mail, Options{Now: fixed, Rand: source})
		if err != nil {
			t.Fatal(err)
		}
		if int(source.next) != tt.drawn || m.MessageID != tt.id {
			t.Errorf("%d random bytes drawn and Message-ID %q, want %d and %q", source.next, m.MessageID, tt.drawn, tt.id)
		}
		for short := 0; short < tt.drawn; short++ {
			_, err := Build(tt.mail, Options{Now: fixed, Rand: io.LimitReader(&sequence{}, int64(short))})
			if err == nil || !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
				t.Fatalf("with %d of %d random bytes: error %v, want a read error", short, tt.drawn, err)
			}
		}
	}
	// without options the clock and crypto/rand are used
	a, err := Build(double, Options{})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Build(double, Options{})
	if a.MessageID == b.MessageID || len(a.MessageID) != len("<18191a1b-1c1d-1e1f-2021-222324252627@example.com>") {
		t.Errorf("default random source: Message-IDs %q and %q", a.MessageID, b.MessageID)
	}
	date := "Date: " + time.Now().UTC().Format("Mon, 02 Jan 2006")
	if !bytes.Contains(a.Raw, []byte(date)) {
		t.Errorf("default clock: no %q in %q", date, a.Raw)
	}
}

// TestDate checks the Date header: always UTC, whatever zone the time has.
func TestDate(t *testing.T) {
	tests := []struct {
		when time.Time
		want string
	}{
		{time.Date(2026, 11, 10, 21, 25, 17, 0, time.FixedZone("CET", 3600)), "Tue, 10 Nov 2026 20:25:17 +0000"},
		{time.Date(2026, 1, 1, 0, 30, 0, 0, time.FixedZone("EAST", 5*3600)), "Wed, 31 Dec 2025 19:30:00 +0000"},
		{time.Date(2026, 3, 5, 4, 5, 6, 999999999, time.UTC), "Thu, 05 Mar 2026 04:05:06 +0000"},
		{time.Date(987, 6, 5, 4, 3, 2, 0, time.UTC), "Tue, 05 Jun 0987 04:03:02 +0000"},
		{time.Date(99, 1, 1, 0, 0, 0, 0, time.UTC), "Thu, 01 Jan 0099 00:00:00 +0000"},
		{time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC), "Sat, 01 Jan 0000 00:00:00 +0000"},
		{time.Date(12345, 6, 6, 0, 0, 0, 0, time.UTC), "Wed, 06 Jun 12345 00:00:00 +0000"},
		{time.Date(-1, 12, 31, 0, 0, 0, 0, time.UTC), "Fri, 31 Dec -0001 00:00:00 +0000"},
		{time.UnixMilli(-1), "Wed, 31 Dec 1969 23:59:59 +0000"},
	}
	for _, tt := range tests {
		if got := formatDate(tt.when); got != tt.want {
			t.Errorf("formatDate(%v) = %q, want %q", tt.when, got, tt.want)
		}
		m, err := Build(Mail{}, Options{Now: func() time.Time { return tt.when }, Rand: &sequence{}})
		if err != nil || !bytes.Contains(m.Raw, []byte("\r\nDate: "+tt.want+"\r\n")) {
			t.Errorf("Build at %v: %v, message %q", tt.when, err, m.Raw)
		}
	}
}

// TestTooManyRecipients checks the one error a mail can cause.
func TestTooManyRecipients(t *testing.T) {
	to := make([]string, MaxRecipients+1)
	for i := range to {
		to[i] = fmt.Sprintf("r%d@example.com", i)
	}
	if _, err := Build(Mail{To: to}, Options{Now: fixed, Rand: &sequence{}}); err == nil ||
		err.Error() != "Message has 100001 recipients, which is over the 100000 allowed by maxRecipients" {
		t.Errorf("%d recipients: error %v", len(to), err)
	}
	// the same address many times is one recipient
	to[MaxRecipients] = to[0]
	m, err := Build(Mail{To: to}, Options{Now: fixed, Rand: &sequence{}})
	if err != nil || len(m.EnvelopeTo) != MaxRecipients {
		t.Errorf("%d recipients, one twice: %v", len(to), err)
	}
}

// headerLines returns the header block of a message split into lines.
func headerLines(t *testing.T, raw []byte) []string {
	t.Helper()
	head, _, found := bytes.Cut(raw, []byte("\r\n\r\n"))
	if !found {
		t.Fatalf("no end of the headers in %q", raw)
	}
	return strings.Split(string(head), "\r\n")
}

// TestHostileValues checks what becomes of line breaks, NUL and other
// control characters in every field: nothing a person typed starts a header
// line, and the envelope holds what the headers show.
func TestHostileValues(t *testing.T) {
	tests := []struct {
		name    string
		mail    Mail
		headers []string
		from    string
		to      []string
	}{
		{
			name:    "line break in the subject",
			mail:    Mail{From: "a@b.c", To: []string{"x@y.z"}, Subject: "Hi\r\nBcc: third@example.com\r\n\r\nbody\nmore\rend"},
			headers: []string{"From: a@b.c", "To: x@y.z", "Subject: Hi Bcc: third@example.com  body more end"},
			from:    "a@b.c", to: []string{"x@y.z"},
		},
		{
			name:    "control characters in the subject",
			mail:    Mail{From: "a@b.c", To: []string{"x@y.z"}, Subject: "nul\x00 bell\x07"},
			headers: []string{"From: a@b.c", "To: x@y.z", "Subject: =?UTF-8?Q?nul=00_bell=07?="},
			from:    "a@b.c", to: []string{"x@y.z"},
		},
		{
			name:    "tab and DEL in the subject",
			mail:    Mail{From: "a@b.c", To: []string{"x@y.z"}, Subject: "a\tb\x7f"},
			headers: []string{"From: a@b.c", "To: x@y.z", "Subject: =?UTF-8?Q?a=09b=7F?="},
			from:    "a@b.c", to: []string{"x@y.z"},
		},
		{
			name:    "a subject that looks encoded",
			mail:    Mail{From: "a@b.c", To: []string{"x@y.z"}, Subject: "=?UTF-8?Q?fake?= \"x\""},
			headers: []string{"From: a@b.c", "To: x@y.z", "Subject: =?UTF-8?Q?=3D=3FUTF-8=3FQ=3Ffake=3F=3D_=22x=22?="},
			from:    "a@b.c", to: []string{"x@y.z"},
		},
		{
			// the line break reads as a space, and "word:" then starts a
			// group: the mail goes to the address after it
			name:    "line break in sender and recipient",
			mail:    Mail{From: "a@b.c\r\nBcc: victim@example.com", To: []string{"x@y.z\r\nCc: second@example.com"}, Subject: "S"},
			headers: []string{"From: \"a@b.c Bcc\":victim@example.com;", "To: \"x@y.z Cc\":second@example.com;", "Subject: S"},
			from:    "victim@example.com", to: []string{"second@example.com"},
		},
		{
			name:    "an SMTP command in the recipient",
			mail:    Mail{From: "a@b.c", To: []string{"x@y.z>\r\nRCPT TO:<victim@example.com"}, Subject: "S"},
			headers: []string{"From: a@b.c", "To: \"x@y.z> RCPT TO\":victim@example.com;", "Subject: S"},
			from:    "a@b.c", to: []string{"victim@example.com"},
		},
		{
			name:    "control characters in names and addresses",
			mail:    Mail{From: "a\x00b@c.d", To: []string{"Name\x00 <n@example.com>, \"Quoted\r\nName\" <q@example.com>, <\"a\r\n\"@b.c>"}, Subject: "S"},
			headers: []string{"From: ab@c.d", "To: Name <n@example.com>, Quoted Name <q@example.com>, <\"a \"@b.c>", "Subject: S"},
			from:    "ab@c.d", to: []string{"n@example.com", "q@example.com", "\"a \"@b.c"},
		},
		{
			name:    "an address in the display name",
			mail:    Mail{From: "a@b.c", To: []string{"\"x@evil.example, y@evil.example\" <real@example.com>"}, Subject: "S"},
			headers: []string{"From: a@b.c", "To: \"x@evil.example, y@evil.example\" <real@example.com>", "Subject: S"},
			from:    "a@b.c", to: []string{"real@example.com"},
		},
		{
			name:    "two at signs",
			mail:    Mail{From: "a@b.c", To: []string{"user@evil.example@good.example"}, Subject: "S"},
			headers: []string{"From: a@b.c", "To: <\"user@evil.example\"@good.example>", "Subject: S"},
			from:    "a@b.c", to: []string{"\"user@evil.example\"@good.example"},
		},
		{
			name:    "separators in the domain",
			mail:    Mail{From: "a@b.c", To: []string{"<user@a;b.example>"}, Subject: "S"},
			headers: []string{"From: a@b.c", "To: <user@a;b.example>", "Subject: S"},
			from:    "a@b.c", to: []string{"user@a;b.example"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := build(t, tt.mail)
			lines := headerLines(t, m.Raw)
			if len(lines) < len(tt.headers) || !slices.Equal(lines[:len(tt.headers)], tt.headers) {
				t.Errorf("headers\n got  %q\n want %q", lines, tt.headers)
			}
			if !strings.HasPrefix(lines[len(tt.headers)], "Message-ID: ") {
				t.Errorf("line after the expected headers: %q", lines[len(tt.headers)])
			}
			if m.EnvelopeFrom != tt.from || !slices.Equal(m.EnvelopeTo, tt.to) {
				t.Errorf("envelope %q -> %q, want %q -> %q", m.EnvelopeFrom, m.EnvelopeTo, tt.from, tt.to)
			}
		})
	}
}

// TestFoldAtUnicodeSpace checks that a header is folded only in front of a
// space or a tab. nodemailer folded in front of any Unicode white space,
// which inside a quoted local part left a line that started with the bytes
// of that character, a new header for a lenient reader (BUGS EM13).
func TestFoldAtUnicodeSpace(t *testing.T) {
	to := "\"" + strings.Repeat("a", 70) + "\u00a0X-Header: value\"@example.com"
	m := build(t, Mail{From: "a@b.c", To: []string{to}, Subject: "S"})
	lines := headerLines(t, m.Raw)
	want := []string{"From: a@b.c", "To:", " <\"" + strings.Repeat("a", 70) + "\u00a0X-Header:", " value\"@example.com>", "Subject: S"}
	if len(lines) < len(want) || !slices.Equal(lines[:len(want)], want) {
		t.Errorf("headers\n got  %q\n want %q", lines, want)
	}
	if !slices.Equal(m.EnvelopeTo, []string{to}) {
		t.Errorf("envelope to %q", m.EnvelopeTo)
	}
}

// TestBodiesStayInTheBody checks that a body cannot end the message or
// leave its part: what it holds is either encoded or passed on as it is,
// and the boundary comes from the random source.
func TestBodiesStayInTheBody(t *testing.T) {
	text := "line\r\n.\r\nMAIL FROM:<x>\r\n----_NmP-0001020304050607-Part_1--\r\n"
	m := build(t, Mail{From: "a@b.c", To: []string{"x@y.z"}, Text: ptr(text), HTML: ptr("nul\x00 here")})
	raw := string(m.Raw)
	if !strings.Contains(raw, "Content-Transfer-Encoding: 7bit\r\n\r\n"+text+"\r\n----_NmP-0001020304050607-Part_1\r\n") {
		t.Errorf("plain text part: %q", raw)
	}
	if !strings.Contains(raw, "Content-Transfer-Encoding: quoted-printable\r\n\r\nnul=00 here\r\n----_NmP-0001020304050607-Part_1--\r\n") {
		t.Errorf("html part with NUL: %q", raw)
	}
}

// TestTransferEncoding checks which encoding a text gets.
func TestTransferEncoding(t *testing.T) {
	tests := []struct {
		name, content, want string
	}{
		{"short ascii", "Hello\nWorld\r\n", transfer7bit},
		{"76 characters", strings.Repeat("a", 76), transfer7bit},
		{"76 characters per line", strings.Repeat("a", 76) + "\n" + strings.Repeat("b", 76) + "\r" + strings.Repeat("c", 76), transfer7bit},
		{"77 characters", strings.Repeat("a", 77), transferQP},
		{"77 characters in the second line", "a\n" + strings.Repeat("a", 77), transferQP},
		{"tab is plain", "a\tb", transfer7bit},
		{"DEL is plain", "a\x7fb", transfer7bit},
		{"NUL", "abc\x00", transferQP},
		{"vertical tab", "ab\x0b", transferQP},
		{"form feed", "ab\x0c", transferQP},
		{"0x0e", "ab\x0e", transferQP},
		{"0x1f", "ab\x1f", transferQP},
		{"0x08", "ab\x08", transferQP},
		{"one umlaut among letters", "Gr\u00fc\u00dfe aus", transferQP},
		{"as many letters as other characters", "ab\u00fc\u00fc", transferBase64},
		{"more letters than other characters", "abc\u00fc\u00fc", transferQP},
		{"japanese", "\u65e5\u672c\u8a9e", transferBase64},
		{"an emoji counts as two", "abc\U0001F600\U0001F600", transferBase64},
		{"an emoji counts as two, five letters", "abcde\U0001F600\U0001F600", transferQP},
		{"digits are no letters", "12345678\u00fc", transferBase64},
		{"long lines of digits", strings.Repeat("1", 77), transferBase64},
		{"more than 128 KiB of short lines", strings.Repeat("ab\n", 44000), transferQP},
		{"128 KiB of short lines", strings.Repeat("abc\n", 32768), transfer7bit},
		{"one byte more than 128 KiB of short lines", strings.Repeat("abc\n", 32768) + "a", transferQP},
	}
	for _, tt := range tests {
		if got := transferEncoding(tt.content); got != tt.want {
			t.Errorf("%s: %s, want %s", tt.name, got, tt.want)
		}
	}
}

// TestQuotedPrintableBody pins the line breaking of the quoted-printable
// body encoder.
func TestQuotedPrintableBody(t *testing.T) {
	a := func(n int) string { return strings.Repeat("a", n) }
	tests := []struct {
		name, in, want string
	}{
		{"empty", "", ""},
		{"equals sign", "a=b", "a=3Db"},
		{"every line end becomes CR LF", "a\nb\rc\r\nd\n\re", "a\r\nb\r\nc\r\nd\r\n\r\ne"},
		{"space at the end", "a ", "a=20"},
		{"tab at the end", "a\t", "a=09"},
		{"only the last space of a run at the end", "a   ", "a  =20"},
		{"space in front of a line feed", "a \nb", "a=20\r\nb"},
		{"space in front of a carriage return", "a\t\rb", "a=09\r\nb"},
		{"76 characters stay on one line", a(76), a(76)},
		{"77 characters break after 75", a(77), a(75) + "=\r\n" + a(2)},
		{"a break goes after the last space of the last third", a(60) + " " + a(20), a(60) + " =\r\n" + a(20)},
		{"a space in the first two thirds is no place to break", a(40) + " " + a(40), a(40) + " " + a(34) + "=\r\n" + a(6)},
		{"a break goes after a full stop", a(70) + "." + a(10), a(70) + ".=\r\n" + a(10)},
		{"an escape is not cut", a(74) + "==", a(74) + "=\r\n=3D=3D"},
		{"an escape that ends the line moves down", a(73) + "=" + a(5), a(73) + "=\r\n=3D" + a(5)},
		{"the bytes of a character stay together", a(71) + "\u00fc" + a(5), a(71) + "=\r\n=C3=BC" + a(5)},
		{"three bytes stay together", a(68) + "\u20ac" + a(5), a(68) + "=\r\n=E2=82=AC" + a(5)},
		{"a line end in the last third ends the line", a(70) + "\n" + a(20), a(70) + "\r\n" + a(20)},
		{"a line that ends at the limit", a(76) + "\n" + a(3), a(75) + "=\r\na\r\n" + a(3)},
		{"control characters", "\x00\x1f\x7f\xff", "=00=1F=7F=FF"},
		{"a break in the last part of the text", a(116) + " " + a(10) + "b", a(75) + "=\r\n" + a(41) + " =\r\n" + a(10) + "b"},
		{"no break in a last part of 51 characters", a(75) + a(39) + " " + a(10) + "b", a(75) + "=\r\n" + a(39) + " " + a(10) + "b"},
		// bytes that are no UTF-8: the search for the start of a character
		// stops when four escapes are left on the line
		{"stray bytes from the inside of a character", strings.Repeat("\x80", 30), "=80=80=80=80=\r\n=80=80=80=80=\r\n" + strings.Repeat("=80", 22)},
		{"stray bytes after text", a(70) + strings.Repeat("\xbf", 8), a(70) + "=\r\n" + strings.Repeat("=BF", 8)},
	}
	for _, tt := range tests {
		if got := string(qpEncodeBody([]byte(tt.in))); got != tt.want {
			t.Errorf("%s:\n got  %q\n want %q", tt.name, got, tt.want)
		}
	}
	// a long text goes through in slices, and comes back whole
	long := strings.Repeat("S\u00fc\u00df and enough latin letters. ", 6000) + strings.Repeat(" \t", 40000)
	decoded, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(qpEncodeBody([]byte(long)))))
	if err != nil || string(decoded) != long {
		t.Errorf("a long text does not come back from quoted-printable: %v", err)
	}
}

// TestQuotedPrintableLineEnds checks that every line end of a
// quoted-printable body is CR LF, also a bare CR or LF of the text: a line
// that ends in another way is no line of quoted-printable (BUGS EM15).
func TestQuotedPrintableLineEnds(t *testing.T) {
	text := "Grüße\nzwei \rdrei\r\nvier ä\n\n"
	m := build(t, Mail{From: "a@b.c", To: []string{"x@y.z"}, Text: ptr(text)})
	_, body, _ := strings.Cut(string(m.Raw), "\r\n\r\n")
	if want := "Gr=C3=BC=C3=9Fe\r\nzwei=20\r\ndrei\r\nvier =C3=A4\r\n\r\n"; body != want {
		t.Errorf("body %q, want %q", body, want)
	}
	decoded, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(body)))
	if want := crlfText(text); err != nil || string(decoded) != want {
		t.Errorf("decoded %q %v, want %q", decoded, err, want)
	}
}

// TestBase64Body pins the line breaking of the base64 body.
func TestBase64Body(t *testing.T) {
	for _, length := range []int{0, 1, 2, 3, 56, 57, 58, 113, 114, 115, 171, 1000} {
		content := bytes.Repeat([]byte{0xe6, 0x97, 0xa5}, length)[:length]
		got := string(base64Body(content))
		lines := strings.Split(got, "\r\n")
		for i, line := range lines {
			if (i < len(lines)-1 && len(line) != 76) || len(line) > 76 || (len(line) == 0 && length > 0) {
				t.Errorf("%d bytes: line %d has %d characters in %q", length, i, len(line), got)
			}
		}
		if decoded, err := base64.StdEncoding.DecodeString(strings.Join(lines, "")); err != nil || !bytes.Equal(decoded, content) {
			t.Errorf("%d bytes: does not decode: %v", length, err)
		}
	}
}

// TestSubjectEncoding pins how a subject goes on its line.
func TestSubjectEncoding(t *testing.T) {
	tests := []struct {
		name, subject string
		want          []string
	}{
		{"plain", "Hello World", []string{"Subject: Hello World"}},
		{"empty", "", nil},
		{"only spaces", "  \t ", nil},
		{"special characters stay", "a=b?c_d (e) <f>", []string{"Subject: a=b?c_d (e) <f>"}},
		{"a quote", "say \"hi\"", []string{"Subject: =?UTF-8?Q?say_=22hi=22?="}},
		{"mostly letters: Q", "Gr\u00fc\u00dfe aus K\u00f6ln", []string{"Subject: =?UTF-8?Q?Gr=C3=BC=C3=9Fe_aus_K=C3=B6ln?="}},
		{"mostly other characters: B", "\u65e5\u672c\u8a9e", []string{"Subject: =?UTF-8?B?5pel5pys6Kqe?="}},
		{"a space at the end", "\u00fcber ", []string{"Subject: =?UTF-8?Q?=C3=BCber=20?="}},
		{"plain and long", strings.Repeat("word ", 20), []string{"Subject: " + strings.TrimSuffix(strings.Repeat("word ", 13), " "), strings.Repeat(" word", 7) + " "}},
		{"a long word is not broken, the line breaks in front of it", strings.Repeat("a", 100), []string{"Subject:", " " + strings.Repeat("a", 100)}},
		{"a line of 76 characters is folded", strings.Repeat("a", 61) + " bbbbb", []string{"Subject: " + strings.Repeat("a", 61), " bbbbb"}},
		{"a line of 75 characters is not", strings.Repeat("a", 60) + " bbbbb", []string{"Subject: " + strings.Repeat("a", 60) + " bbbbb"}},
		{"Q words of at most 52 characters", strings.Repeat("\u00e4bc", 8), []string{
			"Subject: =?UTF-8?Q?=C3=A4bc=C3=A4bc=C3=A4bc=C3=A4bc=C3=A4bc?=",
			" =?UTF-8?Q?=C3=A4bc=C3=A4bc=C3=A4bc?=",
		}},
		{"as many letters as other characters: B", strings.Repeat("\u00e4b", 12), []string{
			"Subject: =?UTF-8?B?w6Riw6Riw6Riw6Riw6Riw6Riw6Riw6Riw6Riw6Ri?=",
			" =?UTF-8?B?w6Riw6Ri?=",
		}},
		{"B words hold at most 30 bytes", strings.Repeat("\u65e5", 21), []string{
			"Subject: =?UTF-8?B?5pel5pel5pel5pel5pel5pel5pel5pel5pel5pel?=",
			" =?UTF-8?B?5pel5pel5pel5pel5pel5pel5pel5pel5pel5pel?= =?UTF-8?B?5pel?=",
		}},
		{"B words do not cut a character", "a" + strings.Repeat("\U0001F600", 8), []string{
			"Subject: =?UTF-8?B?YfCfmIDwn5iA8J+YgPCfmIDwn5iA8J+YgPCfmIA=?=",
			" =?UTF-8?B?8J+YgA==?=",
		}},
	}
	for _, tt := range tests {
		m := build(t, Mail{Subject: tt.subject})
		var got []string
		in := false
		for _, line := range headerLines(t, m.Raw) {
			switch {
			case strings.HasPrefix(line, "Subject:"):
				in = true
			case !strings.HasPrefix(line, " "):
				in = false
			}
			if in {
				got = append(got, line)
			}
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("%s:\n got  %q\n want %q", tt.name, got, tt.want)
		}
	}
}

// TestEncodedWordsDecode checks that every encoded word of a long value
// decodes on its own, so no word ends inside a character.
func TestEncodedWordsDecode(t *testing.T) {
	values := []string{
		strings.Repeat("\u00e4", 100), strings.Repeat("a\u00e4", 60), strings.Repeat("\u20ac", 50), strings.Repeat("ab\U0001F600", 30),
		strings.Repeat("\U0001F600", 40), strings.Repeat("\u65e5\u672c\u8a9e ", 30), strings.Repeat("x", 39) + "\u00e4\u00e4", strings.Repeat("x", 38) + "\u20ac=",
	}
	decoder := new(mime.WordDecoder)
	for _, value := range values {
		for _, encoding := range []byte{'Q', 'B'} {
			encoded := encodeWord(value, encoding, maxEncodedWord)
			var decoded strings.Builder
			for _, word := range strings.Split(encoded, " ") {
				if len(word) > maxEncodedWord {
					t.Errorf("%c: word of %d characters: %q", encoding, len(word), word)
				}
				part, err := decoder.Decode(word)
				if err != nil || !utf8.ValidString(part) {
					t.Errorf("%c: word %q decodes to %q: %v", encoding, word, part, err)
				}
				decoded.WriteString(part)
			}
			if decoded.String() != value {
				t.Errorf("%c: %q comes back as %q", encoding, value, decoded.String())
			}
		}
	}
}

// TestAddressFormatting pins how addresses go into a header and into the
// envelope.
func TestAddressFormatting(t *testing.T) {
	tests := []struct {
		to       string
		header   string
		envelope []string
	}{
		{"user@example.com", "user@example.com", []string{"user@example.com"}},
		{"<user@example.com>", "user@example.com", []string{"user@example.com"}},
		{"User@EXAMPLE.com", "User@example.com", []string{"User@example.com"}},
		{"Jane Doe <jane@example.com>", "Jane Doe <jane@example.com>", []string{"jane@example.com"}},
		{"jane_doe 2 <jane@example.com>", "jane_doe 2 <jane@example.com>", []string{"jane@example.com"}},
		{"Doe, Jane <jane@example.com>", "\"Doe, Jane\" <jane@example.com>", []string{"jane@example.com"}},
		{"\"Jane \\\"JD\\\" Doe\" <jane@example.com>", "\"Jane \\\"JD\\\" Doe\" <jane@example.com>", []string{"jane@example.com"}},
		{"J\u00fcrgen <j@example.com>", "=?UTF-8?Q?J=C3=BCrgen?= <j@example.com>", []string{"j@example.com"}},
		{"\u7ba1\u7406\u8005 <admin@example.jp>", "=?UTF-8?B?566h55CG6ICF?= <admin@example.jp>", []string{"admin@example.jp"}},
		{"user@m\u00fcller.de", "user@xn--mller-kva.de", []string{"user@xn--mller-kva.de"}},
		{"user@M\u00dcLLER.de", "user@xn--mller-kva.de", []string{"user@xn--mller-kva.de"}},
		{"user@xn--mller-kva.de", "user@xn--mller-kva.de", []string{"user@xn--mller-kva.de"}},
		{"j\u00fcrgen@m\u00fcller.de", "j\u00fcrgen@m\u00fcller.de", []string{"j\u00fcrgen@m\u00fcller.de"}},
		{"j\u00fcrgen@xn--mller-kva.de", "j\u00fcrgen@m\u00fcller.de", []string{"j\u00fcrgen@m\u00fcller.de"}},
		{"user@stra\u00dfe.de", "user@xn--strae-oqa.de", []string{"user@xn--strae-oqa.de"}},
		{"user@compa\u00adny.com", "user@company.com", []string{"user@company.com"}},
		{"user@\uff45xample.com", "user@example.com", []string{"user@example.com"}},
		{"a b@example.com", "a <b@example.com>", []string{"b@example.com"}},
		{"\"a b\"@example.com", "<\"a b\"@example.com>", []string{"\"a b\"@example.com"}},
		{"a..b@example.com", "<\"a..b\"@example.com>", []string{"\"a..b\"@example.com"}},
		{"user@[192.168.1.10]", "<user@[192.168.1.10]>", []string{"user@[192.168.1.10]"}},
		{"user@[::1]", "<user@[::1]>", []string{"user@[::1]"}},
		{"user@[0:0:0:0:0:0:0:1]", "<user@[::1]>", []string{"user@[::1]"}},
		{"user@127.1", "user@127.0.0.1", []string{"user@127.0.0.1"}},
		{"user@0x7f.0.0.1", "user@127.0.0.1", []string{"user@127.0.0.1"}},
		{"user@192.168.1.10", "user@192.168.1.10", []string{"user@192.168.1.10"}},
		{"a@b.c, A@b.c, a@B.C, a@b.c", "a@b.c, A@b.c, a@b.c, a@b.c", []string{"a@b.c", "A@b.c"}},
		{"Team: a@b.c, B <b@b.c>;, c@b.c", "Team:a@b.c, B <b@b.c>;, c@b.c", []string{"a@b.c", "b@b.c", "c@b.c"}},
		{"Empty:;", "Empty:;", []string{}},
		{"undisclosed-recipients:;", "\"undisclosed-recipients\":;", []string{}},
		{"no address here", "", []string{}},
		{"root", "", []string{}},
		{"<root>", "", []string{}},
		{"user@", "<user@>", []string{"user@"}},
		{"@example.com", "<\"\"@example.com>", []string{"\"\"@example.com"}},
	}
	for _, tt := range tests {
		m := build(t, Mail{To: []string{tt.to}})
		header := ""
		lines := headerLines(t, m.Raw)
		for i, line := range lines {
			if strings.HasPrefix(line, "To:") {
				header = strings.TrimPrefix(line, "To:")
				for _, next := range lines[i+1:] {
					if !strings.HasPrefix(next, " ") {
						break
					}
					header += next
				}
				header = strings.TrimPrefix(header, " ")
			}
		}
		if header != tt.header || !slices.Equal(m.EnvelopeTo, tt.envelope) {
			t.Errorf("to %q:\n header   %q, want %q\n envelope %q, want %q", tt.to, header, tt.header, m.EnvelopeTo, tt.envelope)
		}
	}
}

// TestNormalizeAddress pins the parts of address normalization one by one.
func TestNormalizeAddress(t *testing.T) {
	tab := tables()
	tests := []struct{ in, want string }{
		{"", ""},
		{" \t ", ""},
		{"\u00a0user@example.com\ufeff", "user@example.com"},
		{"us\x00er@exa\x1fmple.com", "\"us er\"@exa mple.com"},
		{"<user>@example.com", "\"user \"@example.com"},
		{"a<b>c@example.com", "\"a b c\"@example.com"},
		{"us\x7fer@example.com", "\"us er\"@example.com"},
		{"user", "user"},
		{"us er", "\"us er\""},
		{"a\"b", "\"a\\\"b\""},
		{"a\\b@c.d", "\"a\\\\b\"@c.d"},
		{"\"a\\\"b\"@c.d", "\"a\\\"b\"@c.d"},
		{"\"a\"b\"@c.d", "\"\\\"a\\\"b\\\"\"@c.d"},
		{"\"a\\\"@c.d", "\"\\\"a\\\\\\\"\"@c.d"},
		{"\"@c.d", "\"\\\"\"@c.d"},
		{".a@c.d", "\".a\"@c.d"},
		{"a.@c.d", "\"a.\"@c.d"},
		{"a.b.c@c.d", "a.b.c@c.d"},
		{"a!#$%&'*+-/=?^_`{|}~b@c.d", "a!#$%&'*+-/=?^_`{|}~b@c.d"},
		{"a(b@c.d", "\"a(b\"@c.d"},
		{"a@b@c.d", "\"a@b\"@c.d"},
		{"a@[x@evil.example]", "a@[x@evil.example]"},
		{"a@b@[x]", "\"a@b\"@[x]"},
		{"a@[x]y]", "a@[x]y]"},
		{"a@[x\\]", "a@[x\\]"},
		{"a@b[x]", "a@b[x]"},
		{"a@[a@b@c]", "a@[a@b"},
		{"a@[ü.x@bü.c]", "a@[ü.x@xn--b-eha.c]"},
		{"a@256.0x1.1.1", "a@256.0x1.1.1"},
		{"a@255.0x1.1.1", "a@255.1.1.1"},
		// a zero width joiner between two Arabic letters is not valid, a
		// non-joiner is: only then the ligature after it is taken apart
		{"a@ن‍مﻻ.com", "a@xn--hhbb119ql07r.com"},
		{"a@ن‌مﻻ.com", "a@xn--mgb1dcd4014a.com"},
		{"a@EXAMPLE.Com", "a@example.com"},
		{"a@\u0130.tr", "a@xn--i-9bb.tr"},
		{"a@\u039f\u0394\u039f\u03a3 x", "a@xn-- x-k9b7bbs"},
		{"\u00fc@\u039f\u0394\u039f\u03a3 x", "\u00fc@\u03bf\u03b4\u03bf\u03c2 x"},
		{"\u00fc@a\u03a3b x", "\u00fc@a\u03c3b x"},
		{"\u00fc@\u03a3 x", "\u00fc@\u03c3 x"},
		{"\u00fc@a'\u03a3 x", "\u00fc@a'\u03c2 x"},
		{"\u00fc@a\u03a3'b x", "\u00fc@a\u03c3'b x"},
		{"a@1.2.3", "a@1.2.0.3"},
		{"a@1.2.3.4.5", "a@1.2.3.4.5"},
		{"a@0x.1", "a@0.0.0.1"},
		{"a@08.1", "a@08.1"},
		{"a@4294967295", "a@255.255.255.255"},
		{"a@4294967296", "a@4294967296"},
		{"a@1.2.3.4.", "a@1.2.3.4"},
		{"a@example.com.", "a@example.com."},
		{"a@[2001:DB8:0:0:0:0:0:1]", "a@[2001:db8::1]"},
		{"a@[1:0:0:2:0:0:0:3]", "a@[1:0:0:2::3]"},
		{"a@[::ffff:1.2.3.4]", "a@[::ffff:102:304]"},
		{"a@[1:2:3:4:5:6:7]", "a@[1:2:3:4:5:6:7]"},
		{"a@[::1]:25", "a@[::1]:25"},
		{"a@b:25", "a@b:25"},
		{"a@xn--a", "a@xn--a"},
		{"a@xn--zca.de", "a@xn--zca.de"},
		{"\u00fc@xn--zca.de", "\u00fc@\u00df.de"},
		{"\u00fc@xn--a.de", "\u00fc@\u0080.de"},
		{"\u00fc@XN---A.de", "\u00fc@XN---A.de"},
		{"\u00fc@xn--.de x", "\u00fc@.de x"},
		{"a@a\u200db.com", "a@xn--ab-m1t.com"},
		{"a@\u0915\u094d\u200d\u0937.com", "a@xn--11b2ezcw70k.com"},
		{"a@\u05d0a.com", "a@xn--a-zhc.com"},
		{"a@a\u05d0.com", "a@xn--a-0hc.com"},
		{"a@evil.example/mail.corp.example", "a@evil.example/mail.corp.example"},
		{"a@\uff0f.com", "a@xn--6g7c.com"},
		{"a@a%41.com", "a@a%41.com"},
		{"a@b\u3002c", "a@b.c"},
		// the host parser has no length limit, the fallback codec has
		{"a@" + strings.Repeat("\u00fc", 64) + ".com", "a@xn--td" + strings.Repeat("a", 64) + ".com"},
		{"a@" + strings.Repeat("\u00dc", 64) + " x", "a@" + strings.Repeat("\u00fc", 64) + " x"},
		{"a@" + strings.Repeat("\u00dc", 61) + " x", "a@xn-- x-wk" + strings.Repeat("a", 61)},
	}
	for _, tt := range tests {
		if got := normalizeAddress(tab, tt.in); got != tt.want {
			t.Errorf("normalizeAddress(%q)\n got  %q\n want %q", tt.in, got, tt.want)
		}
	}
}

// TestAddressShapes pins the checks that decide how an address is written:
// bare, in angle brackets, or with a quoted local part.
func TestAddressShapes(t *testing.T) {
	plain := map[string]bool{
		"a@b": true, "a.b+c@d-e.f": true, "ü@ü": true,
		"": false, "a": false, "@": false, "@b": false, "a@": false, "a@b@c": false, "a b@c": false, "a@b c": false, "a@b c": false,
		"a@b c": false, "a\tb@c": false, "\"a\"@b": false, "a(b@c": false, "a)b@c": false, "a,b@c": false, "a@b:c": false, "a;b@c": false,
		"a<b@c": false, "a>b@c": false, "a@[b]": false, "a@b]": false, "a\\b@c": false,
	}
	for address, want := range plain {
		if got := isPlainAddress(address); got != want {
			t.Errorf("isPlainAddress(%q) = %v, want %v", address, got, want)
		}
	}
	dotAtom := map[string]bool{
		"a": true, "a.b": true, "a.b.c": true, "ü.日": true, "a!#$%&'*+-/=?^_`{|}~": true, "A1": true,
		"": false, ".": false, ".a": false, "a.": false, "a..b": false, "a b": false, "a@b": false, "a\"b": false, "a(b": false, "a,b": false,
		"a:b": false, "a;b": false, "a<b": false, "a[b": false, "a\\b": false, "a\x7fb": false, "a\x00b": false,
	}
	for local, want := range dotAtom {
		if got := isDotAtom(local); got != want {
			t.Errorf("isDotAtom(%q) = %v, want %v", local, got, want)
		}
	}
	quoted := map[string]bool{
		"\"\"": true, "\"a\"": true, "\"a b\"": true, "\"a\\\"b\"": true, "\"a\\\\\"": true, "\"\\ü\"": true,
		"": false, "\"": false, "a": false, "\"a": false, "a\"": false, "\"a\"b\"": false, "\"a\\\"": false, "\"a\"b": false, "\"\\\"": false,
	}
	for local, want := range quoted {
		if got := isQuotedString(local); got != want {
			t.Errorf("isQuotedString(%q) = %v, want %v", local, got, want)
		}
	}
}

// TestParseBack reads a built message with the mail packages of the
// standard library and finds subject, addresses and both bodies again.
func TestParseBack(t *testing.T) {
	subject := "\u00c4rger mit dem Dr\u00fccker \"Empfang\" \u2014 bitte pr\u00fcfen, es eilt sehr (wirklich) \U0001F600"
	text := "Gr\u00fc\u00dfe aus K\u00f6ln\nZweite Zeile mit = und einem ziemlich langen Satz, der \u00fcber die sechsundsiebzig Zeichen hinausgeht.\n"
	html := "<p>\u65e5\u672c\u8a9e\u306e\u30e1\u30c3\u30bb\u30fc\u30b8\u3067\u3059\u3002" + strings.Repeat("\u9577\u3044", 60) + "</p>"
	m := build(t, Mail{
		From:    "\"B\u00fcro, Technik\" <technik@m\u00fcller.de>",
		To:      []string{"J\u00fcrgen M\u00fcller <jm@example.com>, ops@example.com", "\u7ba1\u7406\u8005 <admin@example.jp>"},
		Subject: subject,
		Text:    &text,
		HTML:    &html,
	})
	parsed, err := mail.ReadMessage(bytes.NewReader(m.Raw))
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	decoder := new(mime.WordDecoder)
	if got, err := decoder.DecodeHeader(parsed.Header.Get("Subject")); err != nil || got != subject {
		t.Errorf("subject %q (%v), want %q", got, err, subject)
	}
	from, err := parsed.Header.AddressList("From")
	if err != nil || len(from) != 1 || from[0].Name != "B\u00fcro, Technik" || from[0].Address != "technik@xn--mller-kva.de" || from[0].Address != m.EnvelopeFrom {
		t.Errorf("from %v (%v), envelope %q", from, err, m.EnvelopeFrom)
	}
	to, err := parsed.Header.AddressList("To")
	if err != nil || len(to) != 3 || to[0].Name != "J\u00fcrgen M\u00fcller" || to[2].Name != "\u7ba1\u7406\u8005" {
		t.Fatalf("to %v (%v)", to, err)
	}
	for i, address := range to {
		if address.Address != m.EnvelopeTo[i] {
			t.Errorf("recipient %d: header %q, envelope %q", i, address.Address, m.EnvelopeTo[i])
		}
	}
	if id := parsed.Header.Get("Message-ID"); id != m.MessageID || !strings.HasSuffix(id, "@xn--mller-kva.de>") {
		t.Errorf("Message-ID header %q, value %q", id, m.MessageID)
	}
	if date, err := parsed.Header.Date(); err != nil || !date.Equal(fixed().Truncate(time.Second)) {
		t.Errorf("date %v (%v)", date, err)
	}
	if got := parsed.Header.Get("MIME-Version"); got != "1.0" {
		t.Errorf("MIME-Version %q", got)
	}

	mediaType, params, err := mime.ParseMediaType(parsed.Header.Get("Content-Type"))
	if err != nil || mediaType != "multipart/alternative" {
		t.Fatalf("content type %q (%v)", mediaType, err)
	}
	reader := multipart.NewReader(parsed.Body, params["boundary"])
	wants := []struct{ mediaType, body string }{{"text/plain", text}, {"text/html", html}}
	for _, want := range wants {
		// NextRawPart leaves the transfer encoding alone
		p, err := reader.NextRawPart()
		if err != nil {
			t.Fatalf("part %s: %v", want.mediaType, err)
		}
		partType, partParams, err := mime.ParseMediaType(p.Header.Get("Content-Type"))
		if err != nil || partType != want.mediaType || partParams["charset"] != "utf-8" {
			t.Errorf("part type %q %v (%v), want %s in utf-8", partType, partParams, err, want.mediaType)
		}
		var body io.Reader = p
		switch encoding := p.Header.Get("Content-Transfer-Encoding"); encoding {
		case transferQP:
			// every line end of the text comes back as CR LF (BUGS EM15)
			body = quotedprintable.NewReader(p)
			want.body = crlfText(want.body)
		case transferBase64:
			body = base64.NewDecoder(base64.StdEncoding, p)
		default:
			t.Errorf("part %s: transfer encoding %q", want.mediaType, encoding)
		}
		decoded, err := io.ReadAll(body)
		if err != nil || string(decoded) != want.body {
			t.Errorf("part %s (%v):\n got  %q\n want %q", want.mediaType, err, decoded, want.body)
		}
	}
	if _, err := reader.NextRawPart(); err != io.EOF {
		t.Errorf("after the two parts: %v, want the end", err)
	}
}

// TestInvalidUTF8 checks that bytes that are no UTF-8 become U+FFFD.
func TestInvalidUTF8(t *testing.T) {
	m := build(t, Mail{From: "a\xff@b.c", To: []string{"N\xfe <x@y.z>"}, Subject: "s\xc3", Text: ptr("t\xe2\x82")})
	want := build(t, Mail{From: "a\ufffd@b.c", To: []string{"N\ufffd <x@y.z>"}, Subject: "s\ufffd", Text: ptr("t\ufffd\ufffd")})
	if !bytes.Equal(m.Raw, want.Raw) || m.EnvelopeFrom != want.EnvelopeFrom || !utf8.Valid(m.Raw) {
		t.Errorf("invalid UTF-8:\n got  %q\n want %q", m.Raw, want.Raw)
	}
}

// TestHelpers pins small helpers that the vectors only reach in passing.
func TestHelpers(t *testing.T) {
	if got := foldLines("Key: " + strings.Repeat("a", 60) + "\r\n" + strings.Repeat("b", 30)); got != "Key: "+strings.Repeat("a", 60)+"\r\n"+strings.Repeat("b", 30) {
		t.Errorf("a line break in a header line: %q", got)
	}
	if got := foldLines(strings.Repeat("a", 75) + "\rb" + strings.Repeat("c", 80)); got != strings.Repeat("a", 75)+"\rb"+strings.Repeat("c", 80) {
		t.Errorf("a carriage return at the end of the window: %q", got)
	}
	if got := foldLines(strings.Repeat("a", 74) + "\n" + strings.Repeat("c", 10) + " d"); got != strings.Repeat("a", 74)+"\n"+strings.Repeat("c", 10)+" d" {
		t.Errorf("a line feed in the window: %q", got)
	}
	if got := foldLines(strings.Repeat("\U0001F600", 40) + " x"); got != strings.Repeat("\U0001F600", 40)+"\r\n x" {
		t.Errorf("characters of two units: %q", got)
	}
	if got := foldLines(" " + strings.Repeat("a", 80) + " b"); got != " "+strings.Repeat("a", 80)+"\r\n b" {
		t.Errorf("white space only at the start: %q", got)
	}
	if got := foldLines(strings.Repeat("a", 76)); got != strings.Repeat("a", 76) {
		t.Errorf("76 characters without a space: %q", got)
	}
	if got := messageIDHeader("a\r\nb\x00c\td\x7f"); got != "<a bc\td>" {
		t.Errorf("messageIDHeader: %q", got)
	}
	if got := messageIDHeader("<id@host>"); got != "<id@host>" {
		t.Errorf("messageIDHeader: %q", got)
	}
	for in, want := range map[string]string{"a\r\nb": "a b", "a\nb": "a b", "a\rb": "a b", "a\r\n\r\nb": "a  b", "a\n\rb": "a  b", "ab": "ab"} {
		if got := lineBreaksToSpaces(in); got != want {
			t.Errorf("lineBreaksToSpaces(%q) = %q, want %q", in, got, want)
		}
	}
	if got := splitQEncoded("=C3=A4=C3=A4=C3=A4=C3=A4=C3=A4", 12); !slices.Equal(got, []string{"=C3=A4=C3=A4", "=C3=A4=C3=A4", "=C3=A4"}) {
		t.Errorf("splitQEncoded: %q", got)
	}
	// bytes that are no UTF-8 have no place to cut that keeps a character whole
	if got := splitQEncoded("=80=80=80=80=80=80", 12); !slices.Equal(got, []string{"=80=80=80=80", "=80=80"}) {
		t.Errorf("splitQEncoded of stray bytes: %q", got)
	}
	if got := splitQEncoded(strings.Repeat("a", 11)+"=3D", 12); !slices.Equal(got, []string{strings.Repeat("a", 11), "=3D"}) {
		t.Errorf("splitQEncoded with an escape at the cut: %q", got)
	}
	// 0x7F is a character of its own, 0xC1 counts as a byte from the inside of one
	if got := splitQEncoded(strings.Repeat("a", 12)+"=7F", 12); !slices.Equal(got, []string{strings.Repeat("a", 12), "=7F"}) {
		t.Errorf("splitQEncoded in front of DEL: %q", got)
	}
	if got := splitQEncoded("=E4=B8=C1=C1=80", 12); !slices.Equal(got, []string{"=E4=B8=C1=C1", "=80"}) {
		t.Errorf("splitQEncoded in front of 0xC1: %q", got)
	}
	if got := splitQEncoded("aaa=E4=B8=C1=C2=80", 12); !slices.Equal(got, []string{"aaa=E4=B8=C1", "=C2=80"}) {
		t.Errorf("splitQEncoded in front of 0xC2: %q", got)
	}
	for in, want := range map[string]string{"a": "a", "\u00a0 a \ufeff\u2028": "a", "\x00a": "\x00a", "\u200ba": "\u200ba", "\u3000\u2003a\u1680\u205f\u202f": "a"} {
		if got := jsTrim(in); got != want {
			t.Errorf("jsTrim(%q) = %q, want %q", in, got, want)
		}
	}
	units := utf16Units("a\u00fc\U0001F600")
	if !slices.Equal(units, []uint16{'a', 0xfc, 0xd83d, 0xde00}) || unitsString(units) != "a\u00fc\U0001F600" || utf16Len("a\u00fc\U0001F600") != 4 {
		t.Errorf("utf16Units: %x", units)
	}
	if got := unitsString([]uint16{0xd83d, 'a', 0xde00, 0xd83d}); got != "\ufffda\ufffd\ufffd" {
		t.Errorf("unitsString of lone surrogates: %q", got)
	}
}

// TestFallbackPunycode pins the plain Punycode codec that takes the
// domains the host parser does not accept.
func TestFallbackPunycode(t *testing.T) {
	tab := tables()
	if got, err := fallbackToASCII("b\u00fccher\u3002ex\uff0eample\uff61com"); err != nil || got != "xn--bcher-kva.ex.ample.com" {
		t.Errorf("fallbackToASCII: %q, %v", got, err)
	}
	if got, err := fallbackToASCII("[a@b\u00fc@c]"); err != nil || got != "[a@xn--b-eha" {
		t.Errorf("fallbackToASCII with at signs: %q, %v", got, err)
	}
	if got, err := fallbackToASCII("[\u00fc.x@b\u00fc.c]"); err != nil || got != "[\u00fc.x@xn--b-eha.c]" {
		t.Errorf("fallbackToASCII with one at sign: %q, %v", got, err)
	}
	for domain, want := range map[string]bool{
		"example.com": false, "\u00fc.de": false, "a:b": false, "[::1]": false, "": false,
		"a/b": true, "a\\b": true, "a?b": true, "a#b": true, "a%41": true, "a b": true, "a\tb": true, "a\x00b": true, "a\x1fb": true, "a\x7fb": true,
	} {
		if got := hasURLUnsafe(domain); got != want {
			t.Errorf("hasURLUnsafe(%q) = %v, want %v", domain, got, want)
		}
	}
	if got, err := fallbackToUnicode(tab, "xn--bcher-kva.xn--MLLER-kva.x"); err != nil || got != "b\u00fccher.m\u00fcller.x" {
		t.Errorf("fallbackToUnicode: %q, %v", got, err)
	}
	if got, err := fallbackToUnicode(tab, "xn--"); err != nil || got != "" {
		t.Errorf("fallbackToUnicode of an empty label: %q, %v", got, err)
	}
	if got, err := fallbackToUnicode(tab, "xn--a.b-.xn--a-"); err != nil || got != "\u0080.b-.a" {
		t.Errorf("fallbackToUnicode of labels without checks: %q, %v", got, err)
	}
	for _, bad := range []string{"xn--\u00fc-a", "xn---a", "xn--99999999999", "xn--a-!", "xn--zzzzzzzz", "xn--ab-zzzzzz", "xn--a.xn--!"} {
		if got, err := fallbackToUnicode(tab, bad); err == nil {
			t.Errorf("fallbackToUnicode(%q) = %q, want an error", bad, got)
		}
	}
	// the last code point decodes, the value after it is an error, and half
	// a surrogate pair is U+FFFD
	for in, want := range map[string]string{"a-eha": "a\u00fc", "a-jv3s": "a\U0001F600", "a-h023p": "a\U0010FFFF", "a-rc4g": "a\ufffd", "a-qo7g": "a\ufffd"} {
		if got, err := fallbackDecode(in); err != nil || got != want {
			t.Errorf("fallbackDecode(%q) = %q, %v, want %q", in, got, err, want)
		}
	}
	if got, err := fallbackDecode("a-j023p"); err == nil {
		t.Errorf("a value that is no code point decodes to %q", got)
	}
	if !exceedsDomainLimits(strings.Repeat("a", 64)) || exceedsDomainLimits(strings.Repeat("a", 63)+"\uff61"+strings.Repeat("\u00fc", 63)) ||
		!exceedsDomainLimits(strings.Repeat("a.", 127)) || exceedsDomainLimits(strings.Repeat("a.", 126)+"a") {
		t.Error("exceedsDomainLimits")
	}
}

// TestTablesFile checks that a damaged table file is refused.
func TestTablesFile(t *testing.T) {
	if _, err := loadTables(tablesFile); err != nil {
		t.Fatalf("the embedded tables do not load: %v", err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(tablesFile))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	pack := func(data []byte) []byte {
		var out bytes.Buffer
		writer := gzip.NewWriter(&out)
		writer.Write(data)
		writer.Close()
		return out.Bytes()
	}
	damaged := map[string][]byte{
		"not gzip":          raw,
		"empty":             pack(nil),
		"wrong magic":       pack(append([]byte("X"), raw[1:]...)),
		"cut off":           pack(raw[:len(raw)-3]),
		"cut in a header":   pack(raw[:len(tablesMagic)+4]),
		"a section missing": pack(raw[:len(raw)/2]),
		"a section twice":   pack(append(slices.Clone(raw), raw[len(tablesMagic):]...)),
	}
	for name, file := range damaged {
		if _, err := loadTables(file); err == nil {
			t.Errorf("%s: loads without an error", name)
		}
	}
}
