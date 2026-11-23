package message

import (
	"bytes"
	"encoding/base64"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// zeros is a random source that only gives zero bytes, so a fuzz input
// always builds the same message.
type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// zeroBoundary is the boundary a message gets from the zeros source.
const zeroBoundary = "--_NmP-0000000000000000-Part_1"

// headerNames are the headers a message can have, in their order.
var headerNames = []string{"From", "To", "Subject", "Message-ID", "Content-Transfer-Encoding", "Date", "MIME-Version", "Content-Type"}

// hasASCIIControl reports whether s holds an ASCII control character other
// than the ones in allowed.
func hasASCIIControl(s, allowed string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; (c < 0x20 || c == 0x7f) && strings.IndexByte(allowed, c) < 0 {
			return true
		}
	}
	return false
}

// utf16Len returns the length of a string in UTF-16 code units, which is
// what the header folding counts.
func utf16Len(s string) int {
	return len(utf16Units(s))
}

// checkHeaders checks the header block of a message and returns the
// unfolded value of every header.
func checkHeaders(t *testing.T, head string) map[string]string {
	if hasASCIIControl(strings.ReplaceAll(head, "\r\n", ""), "\t") {
		t.Fatalf("control character or bare line break in the headers: %q", head)
	}
	values := make(map[string]string)
	next := 0
	current := ""
	for i, line := range strings.Split(head, "\r\n") {
		// a line is at most 76 characters long, or it holds one word that
		// could not be broken
		if utf16Len(line) > maxHeaderLine && strings.ContainsAny(strings.TrimLeft(line, " \t"), " \t") {
			t.Fatalf("header line %d has %d characters and more than one word: %q", i, utf16Len(line), line)
		}
		if line == "" {
			t.Fatalf("empty line %d in the headers: %q", i, head)
		}
		if line[0] == ' ' || line[0] == '\t' {
			// The line continues the header before it: a header is folded
			// only in front of a space or a tab (BUGS EM13)
			if current == "" {
				t.Fatalf("the headers start with a continuation line: %q", head)
			}
			values[current] += line
			continue
		}
		// every other line starts one of the known headers, in order
		name, value, found := strings.Cut(line, ":")
		for next < len(headerNames) && headerNames[next] != name {
			next++
		}
		if !found || next == len(headerNames) {
			t.Fatalf("header line %d starts no header of the message: %q in %q", i, line, head)
		}
		next++
		if value != "" && value[0] != ' ' {
			t.Fatalf("header line %d has no space after the colon: %q", i, line)
		}
		current = name
		values[name] = value
	}
	for _, name := range []string{"Message-ID", "Date", "MIME-Version", "Content-Type"} {
		if _, found := values[name]; !found {
			t.Fatalf("no %s header in %q", name, head)
		}
	}
	for name, value := range values {
		values[name] = strings.TrimPrefix(value, " ")
	}
	return values
}

// decodeBody takes the transfer encoding off a body.
func decodeBody(t *testing.T, body, encoding string) string {
	switch encoding {
	case transfer7bit:
		return body
	case transferQP:
		// The encoding is taken off by its own rules here, without the
		// standard library's decoder, which also drops white space at the
		// end of a line: "=" and CR LF is a soft break, "=XX" is a byte,
		// the rest is literal.
		body = strings.ReplaceAll(body, "=\r\n", "")
		decoded := make([]byte, 0, len(body))
		for i := 0; i < len(body); i++ {
			if body[i] != '=' {
				decoded = append(decoded, body[i])
				continue
			}
			if i+2 >= len(body) || !isHexDigit(body[i+1]) || !isHexDigit(body[i+2]) {
				t.Fatalf("quoted-printable body with a broken escape at %d: %q", i, body)
			}
			decoded = append(decoded, byte(hexValue(body[i+1], body[i+2])))
			i += 2
		}
		return string(decoded)
	case transferBase64:
		for _, line := range strings.Split(body, "\r\n") {
			if len(line) > maxBodyLine {
				t.Fatalf("base64 line of %d characters", len(line))
			}
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(body, "\r\n", ""))
		if err != nil {
			t.Fatalf("base64 body does not decode: %v in %q", err, body)
		}
		return string(decoded)
	}
	t.Fatalf("transfer encoding %q", encoding)
	return ""
}

// checkEncodedLines checks the line length of an encoded body.
func checkEncodedLines(t *testing.T, body, encoding string) {
	if encoding == transfer7bit {
		return
	}
	for _, line := range strings.Split(body, "\n") {
		if len(strings.TrimSuffix(line, "\r")) > maxBodyLine {
			t.Fatalf("%s line of %d characters: %q", encoding, len(line), line)
		}
	}
	if hasASCIIControl(body, "\t\r\n") || !isASCII(body) {
		t.Fatalf("%s body with a raw control character or byte outside ASCII: %q", encoding, body)
	}
	// Every line ends in CR LF (BUGS EM15)
	if strings.Count(body, "\r") != strings.Count(body, "\r\n") || strings.Count(body, "\n") != strings.Count(body, "\r\n") {
		t.Fatalf("%s body with a bare CR or LF: %q", encoding, body)
	}
}

// crlfText is a text with every line end written as CR LF, as a
// quoted-printable body carries it.
func crlfText(s string) string {
	return strings.NewReplacer("\r\n", "\r\n", "\r", "\r\n", "\n", "\r\n").Replace(s)
}

// FuzzBuild builds mails from arbitrary text and checks what must hold for
// every message: the headers are the known ones in their order, each on
// lines that end in CR LF and hold no control character; a header line is
// at most 76 characters long unless it is one word; the envelope holds
// clean addresses that the headers show; and the bodies decode to the text
// that went in.
func FuzzBuild(f *testing.F) {
	seeds := []struct {
		from, to, to2, subject, text, html string
		shape                              uint8
	}{
		{"neodesk@localhost", "admin@example.com", "", "Hello", "Hi there", "<p>Hi</p>", 0},
		{"NeoDesk <noreply@example.com>", "a@example.com, b@example.com", "c@example.com", "[NeoDesk Alert] CPU high — CRITICAL", "Alert: CPU high\nSeverity: critical\n", "", 1},
		{"\"Müller, Jürgen\" <j@müller.de>", "Jürgen <jürgen@müller.de>", "用户@例子.测试", "日本語の件名", "", "<p>日本語</p>", 1},
		{"a@b.c\r\nBcc: victim@example.com", "x@y.z\r\nCc: second@example.com", "", "Hi\r\nBcc: third@example.com\r\n\r\nbody", "line\r\n.\r\nMAIL FROM:<x>\r\n", "", 0},
		{"a\x00b@c.d", "Name\x00 <n@example.com>, \"Quoted\r\nName\" <q@example.com>", "<\"a\r\n\"@b.c>", "nul\x00 bell\x07 del\x7f", "nul\x00", "\x01\x02", 1},
		{"", "", "", "", "", "", 0},
		{" ", ",;:", "<>", " \t ", " ", "\t", 1},
		{"Team: a@b.c, d@e.f;", "undisclosed-recipients:;", "Group: \"N, N\" <a@b.c>, x@y.z;", "=?UTF-8?Q?fake?= \"quoted\"", strings.Repeat("a", 77), strings.Repeat("=", 80), 1},
		{"user@[::ffff:1.2.3.4]", "user@0x7f.1, user@1.2.3, user@xn--mller-kva.de, user@xn--a", "ü@xn--a.de, ü@XN---A.de x", strings.Repeat("ä", 40), strings.Repeat("äb ", 60), strings.Repeat("\U0001F600", 30), 1},
		{"\"" + strings.Repeat("a", 70) + " X-Header: value\"@example.com", "\"a b\"@example.com, a..b@example.com, \"a@b\"@c.d, a@b@c.d", "user@evil.example/mail.corp.example, a@[x@evil.example]", strings.Repeat("word ", 30), "trailing   \nspaces \t\n", "a\rb\nc\r\nd", 1},
		{"x@compa­ny.com", "a@אa.com, a@a‍b.com, a@क्‍ष.com", "a@ΟΔΟΣ.gr, a@straße.de", strings.Repeat("s", 100), strings.Repeat("x", 75) + "ä", strings.Repeat("x", 74) + "=" + strings.Repeat("y", 10), 1},
		{"a@b.c", "to@example.com", "", "S", "t", "h", 2},
		{"a@b.c", "to@example.com", "", "S", "t", "h", 4},
		{"a@b.c", "to@example.com", "", "S", "t", "h", 8},
	}
	for _, s := range seeds {
		f.Add(s.from, s.to, s.to2, s.subject, s.text, s.html, s.shape)
	}
	f.Fuzz(func(t *testing.T, from, to, to2, subject, text, html string, shape uint8) {
		mail := Mail{From: from, Subject: subject, To: []string{to}}
		switch {
		case shape&1 != 0:
			mail.To = []string{to, to2}
		case shape&2 != 0:
			mail.To = nil
		}
		if shape&4 == 0 {
			mail.Text = &text
		}
		if shape&8 == 0 {
			mail.HTML = &html
		}
		message, err := Build(mail, Options{Now: fixed, Rand: zeros{}})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		again, err := Build(mail, Options{Now: fixed, Rand: zeros{}})
		if err != nil || !bytes.Equal(again.Raw, message.Raw) || again.EnvelopeFrom != message.EnvelopeFrom || !slices.Equal(again.EnvelopeTo, message.EnvelopeTo) {
			t.Fatalf("the same mail builds another message the second time (%v)", err)
		}
		if !utf8.Valid(message.Raw) || !bytes.HasSuffix(message.Raw, []byte("\n")) {
			t.Fatalf("message is no UTF-8 or does not end in a line feed: %q", message.Raw)
		}
		head, body, found := strings.Cut(string(message.Raw), "\r\n\r\n")
		if !found {
			t.Fatalf("no end of the headers: %q", message.Raw)
		}
		headers := checkHeaders(t, head)

		// the envelope: clean addresses, each once, all of them in the headers
		if message.EnvelopeTo == nil {
			t.Fatal("EnvelopeTo is nil")
		}
		for i, address := range append([]string{message.EnvelopeFrom}, message.EnvelopeTo...) {
			header := "To"
			if i == 0 {
				if address == "" {
					continue
				}
				header = "From"
			}
			if address == "" || hasASCIIControl(address, "") || strings.ContainsAny(address, "<>") || !utf8.ValidString(address) {
				t.Fatalf("envelope address %q", address)
			}
			if !strings.Contains(headers[header], address) {
				t.Fatalf("envelope address %q is not in the %s header %q", address, header, headers[header])
			}
			if i > 0 && slices.Contains(message.EnvelopeTo[:i-1], address) {
				t.Fatalf("envelope recipient %q twice", address)
			}
		}
		if headers["Message-ID"] != message.MessageID || !strings.HasPrefix(message.MessageID, "<00000000-0000-0000-0000-000000000000@") || !strings.HasSuffix(message.MessageID, ">") {
			t.Fatalf("Message-ID %q, header %q", message.MessageID, headers["Message-ID"])
		}
		if headers["Date"] != "Tue, 10 Nov 2026 20:25:17 +0000" || headers["MIME-Version"] != "1.0" {
			t.Fatalf("Date %q, MIME-Version %q", headers["Date"], headers["MIME-Version"])
		}

		// the bodies
		var parts []part
		if mail.Text != nil && text != "" {
			parts = append(parts, part{"text/plain; charset=utf-8", validUTF8(text)})
		}
		if mail.HTML != nil && html != "" {
			parts = append(parts, part{"text/html; charset=utf-8", validUTF8(html)})
		}
		switch len(parts) {
		case 0:
			if headers["Content-Type"] != "text/plain" || body != "" || headers["Content-Transfer-Encoding"] != "" {
				t.Fatalf("message without a body: type %q, body %q", headers["Content-Type"], body)
			}
		case 1:
			encoding := headers["Content-Transfer-Encoding"]
			if headers["Content-Type"] != parts[0].contentType {
				t.Fatalf("content type %q, want %q", headers["Content-Type"], parts[0].contentType)
			}
			// The message ends in a line feed, which is added where the body
			// has none: after a carriage return a line feed, else CR LF. A
			// base64 body never ends in a line break of its own.
			content := parts[0].content
			if encoding == transferQP {
				content = crlfText(content)
			}
			switch {
			case encoding != transferBase64 && strings.HasSuffix(content, "\n"):
			case encoding != transferBase64 && strings.HasSuffix(content, "\r"):
				if !strings.HasSuffix(body, "\r\n") {
					t.Fatalf("no line feed after the body: %q", body)
				}
				body = body[:len(body)-1]
			default:
				if !strings.HasSuffix(body, "\r\n") {
					t.Fatalf("no line break after the body: %q", body)
				}
				body = body[:len(body)-2]
			}
			checkEncodedLines(t, body, encoding)
			if decoded := decodeBody(t, body, encoding); decoded != content {
				t.Fatalf("%s body decodes to %q, want %q", encoding, decoded, content)
			}
		case 2:
			if headers["Content-Type"] != `multipart/alternative; boundary="`+zeroBoundary+`"` || headers["Content-Transfer-Encoding"] != "" {
				t.Fatalf("content type %q", headers["Content-Type"])
			}
			if strings.Contains(text, "--_NmP-") || strings.Contains(html, "--_NmP-") {
				// the text holds the boundary, which the real random bytes rule out
				return
			}
			pieces := strings.Split(body, "--"+zeroBoundary)
			if len(pieces) != 4 || pieces[0] != "" || pieces[3] != "--\r\n" {
				t.Fatalf("%d pieces between the boundaries: %q", len(pieces), body)
			}
			for i, p := range parts {
				piece := pieces[i+1]
				if !strings.HasPrefix(piece, "\r\n") || !strings.HasSuffix(piece, "\r\n") {
					t.Fatalf("part %d does not sit between line breaks: %q", i, piece)
				}
				partHead, partBody, found := strings.Cut(piece[2:len(piece)-2], "\r\n\r\n")
				encoding, isEncoding := strings.CutPrefix(partHead, "Content-Type: "+p.contentType+"\r\nContent-Transfer-Encoding: ")
				if !found || !isEncoding {
					t.Fatalf("part %d has the headers %q", i, partHead)
				}
				checkEncodedLines(t, partBody, encoding)
				content := p.content
				if encoding == transferQP {
					content = crlfText(content)
				}
				if decoded := decodeBody(t, partBody, encoding); decoded != content {
					t.Fatalf("part %d: %s body decodes to %q, want %q", i, encoding, decoded, content)
				}
			}
		}
	})
}

// FuzzNormalizeAddress checks that an address always comes out without
// ASCII control characters and angle brackets, as valid UTF-8, and the same
// every time.
func FuzzNormalizeAddress(f *testing.F) {
	for _, seed := range []string{
		"user@example.com", "Jürgen@MÜLLER.de", "a@[::ffff:1.2.3.4]", "a@0x7f.1", "a@1.2.3.4.5", "a@xn--mller-kva.de", "ü@xn--a.b-", "a@b@c",
		"\"a b\"@c", "a@[a@b@c]", "a@א١1.com", "a@क्‍ष.com", "a@각ᆨ.kr", "a@ạ́.com", "a\x00<>@b\r\n.c",
		"a@xn--" + strings.Repeat("9", 20), "a@" + strings.Repeat("ü", 70), "ü@aΣ'b x", "a@[1:2:3:4:5:6:1.2.3.4]", "a@／%41.com",
	} {
		f.Add(seed)
	}
	tab := tables()
	f.Fuzz(func(t *testing.T, address string) {
		address = validUTF8(address)
		got := normalizeAddress(tab, address)
		if hasASCIIControl(got, "") || strings.ContainsAny(got, "<>") || !utf8.ValidString(got) {
			t.Fatalf("normalizeAddress(%q) = %q", address, got)
		}
		if again := normalizeAddress(tab, address); again != got {
			t.Fatalf("normalizeAddress(%q) = %q, then %q", address, got, again)
		}
		// White space at the end is possible: "ü@xn--0 -" comes out as
		// "ü@0 ", because the label is decoded after the address was
		// trimmed.
		if got != "" && isJSSpace([]rune(got)[0]) {
			t.Fatalf("normalizeAddress(%q) = %q with white space in front", address, got)
		}
	})
}
