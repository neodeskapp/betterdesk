// Package message builds the MIME message and the SMTP envelope for a mail
// of the console, byte for byte as nodemailer 10.4.0 on Node 22 builds them
// for
//
//	transporter.sendMail({ from, to, subject, text, html })
//
// The result is what the SMTP transport starts from: the envelope, the
// Message-ID and the message before the SMTP layer changes line endings and
// doubles leading dots.
//
// What a person typed cannot leave its place in the message. In the subject
// every line break becomes a space, and a subject with a control character,
// a quote or a character outside ASCII goes out as encoded words. Senders
// and recipients are read by the address parser, which drops control
// characters; what is left is written as `Name <address>` with the name
// quoted or encoded and the address normalized (see normalizeAddress), so
// the header lists exactly the addresses of the envelope. The bodies are
// encoded when they hold anything but short lines of plain ASCII.
package message

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxRecipients is the number of envelope recipients one message may have.
const MaxRecipients = 100000

// Mail is what the console hands to sendMail.
type Mail struct {
	// From is the sender as an admin saved it: an address, with or without
	// a display name. Of a list, every address goes into the From header
	// and the first one into the envelope.
	From string
	// To holds the recipients: one element for a `to` that is a string
	// (which can be a list separated by commas), one per element for a `to`
	// that is an array. Every element is read as an address list of its
	// own. A string and an array with that one string give the same
	// message, and so do an empty string, an empty array and no `to`.
	To []string
	// Subject is the subject line. An empty one leaves the header out.
	Subject string
	// Text and HTML are the two bodies. nil and a pointer to "" both stand
	// for "no such body".
	Text *string
	HTML *string
	// Inline are images the HTML shows by Content-ID. With any, the HTML
	// goes out in a multipart/related part with them; an image whose ID,
	// type or name could break a header is left out. Without an HTML body
	// they are ignored, and a mail without them is built as nodemailer
	// builds it.
	Inline []Inline
}

// Options holds everything the message depends on besides the mail.
type Options struct {
	// Now gives the time of the Date header, which is written in UTC
	// whatever the time zone is. nil means time.Now.
	Now func() time.Time
	// Rand gives the random bytes of the boundary and the Message-ID: 8
	// for the message, 8 more for each part when there are two bodies, and
	// then 4, 2, 2, 2 and 6 for the Message-ID. nil means crypto/rand.
	Rand io.Reader
}

// Message is a mail ready for the SMTP transport.
type Message struct {
	// EnvelopeFrom is the address for MAIL FROM: the first address of From,
	// normalized. It is empty when From holds no address.
	EnvelopeFrom string
	// EnvelopeTo holds the addresses for RCPT TO: every address of To in
	// order, normalized, each one once. It can be empty.
	//
	// No envelope address holds a CR, LF, '<', '>' or an ASCII control
	// character. One can end in white space (a domain label in its "xn--"
	// form can decode to that); nodemailer's SMTP layer trims every address
	// before it uses it.
	EnvelopeTo []string
	// MessageID is the value of the Message-ID header, in angle brackets.
	MessageID string
	// Raw is the message: headers, an empty line and the body, ending in a
	// line feed. Header lines end in CR LF. A body that is not encoded
	// keeps the line endings of the text it was made from.
	Raw []byte
}

// builder carries what the steps of Build share.
type builder struct {
	tables *unicodeTables
	random io.Reader
}

// part is one text of the message with its content type.
type part struct {
	contentType string
	content     string
}

// Build makes the message and the envelope for a mail.
//
// It fails when the mail has more than MaxRecipients recipients, with the
// text nodemailer gives that error, and when the random source fails. A
// mail without a sender or without recipients is no error here: the
// envelope comes back with EnvelopeFrom empty or EnvelopeTo empty, and it
// is the SMTP transport that refuses to send to nobody.
//
// Bytes in the mail that are no valid UTF-8 are read as U+FFFD.
func Build(mail Mail, opts Options) (*Message, error) {
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	b := &builder{tables: tables(), random: opts.Rand}
	if b.random == nil {
		b.random = rand.Reader
	}
	date := now()

	var parts []part
	if mail.Text != nil && *mail.Text != "" {
		parts = append(parts, part{"text/plain; charset=utf-8", validUTF8(*mail.Text)})
	}
	if mail.HTML != nil && *mail.HTML != "" {
		parts = append(parts, part{"text/html; charset=utf-8", validUTF8(*mail.HTML)})
	}

	// Every node of the message draws eight random bytes when it is made.
	// Only the ones of the root are used, for the boundary.
	base, err := b.randomHex(8)
	if err != nil {
		return nil, err
	}
	if len(parts) == 2 {
		for range parts {
			if _, err := b.randomHex(8); err != nil {
				return nil, err
			}
		}
	}
	var images []Inline
	for _, img := range mail.Inline {
		if img.usable() {
			images = append(images, img)
		}
	}
	relatedBoundary := ""
	if len(images) > 0 && len(parts) > 0 && strings.HasPrefix(parts[len(parts)-1].contentType, "text/html") {
		more, err := b.randomHex(8)
		if err != nil {
			return nil, err
		}
		relatedBoundary = "--_NmP-" + more + "-Part_2"
	}

	from := validUTF8(mail.From)
	to := make([]string, len(mail.To))
	for i, value := range mail.To {
		to[i] = validUTF8(value)
	}
	subject := validUTF8(mail.Subject)

	message := &Message{EnvelopeTo: []string{}}
	var sender, recipient recipients
	fromValue := b.formatAddresses(parseAddresses(b.tables, []string{from}), &sender)
	toValue := b.formatAddresses(parseAddresses(b.tables, to), &recipient)
	if len(sender.list) > 0 {
		message.EnvelopeFrom = sender.list[0]
	}
	if recipient.list != nil {
		message.EnvelopeTo = recipient.list
	}

	// The Message-ID takes the domain of the envelope sender.
	domain := "localhost"
	if message.EnvelopeFrom != "" {
		domain = message.EnvelopeFrom[strings.LastIndexByte(message.EnvelopeFrom, '@')+1:]
	}
	id := make([]string, 5)
	for i, length := range []int{4, 2, 2, 2, 6} {
		if id[i], err = b.randomHex(length); err != nil {
			return nil, err
		}
	}
	message.MessageID = "<" + strings.Join(id, "-") + "@" + domain + ">"

	if len(message.EnvelopeTo) > MaxRecipients {
		// the text is nodemailer's, capital letter included
		return nil, fmt.Errorf("Message has %d recipients, which is over the %d allowed by maxRecipients", len(message.EnvelopeTo), MaxRecipients)
	}

	root := part{contentType: "text/plain"}
	boundary := ""
	switch len(parts) {
	case 1:
		root = parts[0]
	case 2:
		boundary = "--_NmP-" + base + "-Part_1"
		root = part{contentType: `multipart/alternative; boundary="` + boundary + `"`}
	}
	// The HTML with its images: the whole message when it is the only
	// body, else the second part of the alternative
	relatedType := `multipart/related; type="text/html"; boundary="` + relatedBoundary + `"`
	var relatedBody []byte
	if relatedBoundary != "" {
		relatedBody = related(parts[len(parts)-1].content, relatedBoundary, images)
		if len(parts) == 1 {
			root = part{contentType: relatedType}
		}
	}

	var headers []string
	add := func(key, value string) {
		// a header whose value is blank is left out
		if jsTrim(value) != "" {
			headers = append(headers, foldLines(key+": "+value))
		}
	}
	add("From", fromValue)
	add("To", toValue)
	add("Subject", encodeHeaderText(lineBreaksToSpaces(subject)))
	add("Message-ID", messageIDHeader(message.MessageID))
	rootEncoding := ""
	if root.content != "" {
		rootEncoding = transferEncoding(root.content)
		add("Content-Transfer-Encoding", rootEncoding)
	}
	add("Date", formatDate(date))
	add("MIME-Version", "1.0")
	add("Content-Type", root.contentType)

	raw := []byte(strings.Join(headers, "\r\n") + "\r\n\r\n")
	raw = append(raw, encodeBody(root.content, rootEncoding)...)
	if relatedBoundary != "" && len(parts) == 1 {
		raw = append(raw, relatedBody...)
	}
	if boundary != "" {
		for i, p := range parts {
			if i > 0 {
				raw = append(raw, "\r\n"...)
			}
			if relatedBoundary != "" && i == len(parts)-1 {
				raw = append(raw, "--"+boundary+"\r\n"+foldLines("Content-Type: "+relatedType)+"\r\n\r\n"...)
				raw = append(raw, relatedBody...)
				continue
			}
			encoding := transferEncoding(p.content)
			raw = append(raw, "--"+boundary+"\r\n"+
				foldLines("Content-Type: "+p.contentType)+"\r\n"+
				foldLines("Content-Transfer-Encoding: "+encoding)+"\r\n\r\n"...)
			raw = append(raw, encodeBody(p.content, encoding)...)
		}
		raw = append(raw, "\r\n--"+boundary+"--\r\n"...)
	}
	message.Raw = endsWithNewline(raw)
	return message, nil
}

// validUTF8 returns s with every byte that is no valid UTF-8 replaced by
// U+FFFD.
func validUTF8(s string) string {
	if utf8.ValidString(s) {
		return s
	}
	return string([]rune(s))
}

// randomHex reads n random bytes and returns them as hex digits.
func (b *builder) randomHex(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := io.ReadFull(b.random, buf); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// messageIDHeader returns a Message-ID as it goes on the header line: line
// breaks become spaces, control characters other than tab are dropped, and
// the angle brackets are added where they are missing.
func messageIDHeader(id string) string {
	id = lineBreaksToSpaces(id)
	id = strings.Map(func(r rune) rune {
		if r < 0x80 && (isControl(byte(r)) || r == 0x7f) {
			return -1
		}
		return r
	}, id)
	if !strings.HasPrefix(id, "<") {
		id = "<" + id
	}
	if !strings.HasSuffix(id, ">") {
		id += ">"
	}
	return id
}

// formatDate writes a time as the Date header has it: in UTC, with the
// zone "+0000", like "Tue, 10 Nov 2026 19:25:17 +0000".
func formatDate(t time.Time) string {
	t = t.UTC()
	year := t.Year()
	sign := ""
	if year < 0 {
		sign, year = "-", -year
	}
	return t.Format("Mon, 02 Jan ") + sign + fmt.Sprintf("%04d", year) + t.Format(" 15:04:05 +0000")
}
