package message

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// messageVector is one mail of a file that testdata/nodeoracle/message.js
// wrote, with what nodemailer handed to the transport for it.
type messageVector struct {
	// the mail as sendMail got it; a key that is missing was undefined
	From    *string         `json:"from"`
	To      json.RawMessage `json:"to"`
	Subject *string         `json:"subject"`
	Text    *string         `json:"text"`
	HTML    *string         `json:"html"`
	// the clock in milliseconds and the random bytes that were drawn
	Now  int64  `json:"now"`
	Rand string `json:"rand"`
	// the result: the envelope (the sender is false when there is none),
	// the Message-ID and the message; or the error text
	EnvFrom json.RawMessage `json:"envFrom"`
	EnvTo   []string        `json:"envTo"`
	ID      string          `json:"id"`
	Raw     *string         `json:"raw"`
	Err     *string         `json:"err"`
	// large results come as a length and a hash
	RawLength   int    `json:"rawLength"`
	RawSHA256   string `json:"rawSha256"`
	EnvToCount  int    `json:"envToCount"`
	EnvToSHA256 string `json:"envToSha256"`

	// foldedAtOtherSpace is set by check for a message nodemailer folded
	// in front of white space other than a space or a tab, bareLineEnds
	// for one whose quoted-printable body kept a bare CR or LF
	foldedAtOtherSpace, bareLineEnds bool
}

// foldedAtOtherSpace reports whether a message's header block has a line
// that starts with white space other than a space or a tab, as nodemailer
// folded in front of any Unicode white space (BUGS EM13).
func foldedAtOtherSpace(raw []byte) bool {
	head, _, _ := bytes.Cut(raw, []byte("\r\n\r\n"))
	for _, line := range bytes.Split(head, []byte("\r\n"))[1:] {
		if r, _ := utf8.DecodeRune(line); isJSSpace(r) && r != ' ' && r != '\t' {
			return true
		}
	}
	return false
}

// unfold joins the folded lines of a message's header block: every CR LF
// in front of white space goes.
func unfold(raw []byte) []byte {
	head, body, _ := bytes.Cut(raw, []byte("\r\n\r\n"))
	var out []byte
	for i, line := range bytes.Split(head, []byte("\r\n")) {
		if r, _ := utf8.DecodeRune(line); i > 0 && !isJSSpace(r) {
			out = append(out, '\r', '\n')
		}
		out = append(out, line...)
	}
	return append(append(out, "\r\n\r\n"...), body...)
}

// mail returns the vector's input as Build takes it.
func (v *messageVector) mail() (Mail, error) {
	mail := Mail{Text: v.Text, HTML: v.HTML}
	if v.From != nil {
		mail.From = *v.From
	}
	if v.Subject != nil {
		mail.Subject = *v.Subject
	}
	switch {
	case len(v.To) == 0:
	case v.To[0] == '[':
		if err := json.Unmarshal(v.To, &mail.To); err != nil {
			return mail, err
		}
	default:
		var single string
		if err := json.Unmarshal(v.To, &single); err != nil {
			return mail, err
		}
		mail.To = []string{single}
	}
	return mail, nil
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// check builds the vector's mail with its clock and random bytes and
// returns what differs from the recorded result, or "".
func (v *messageVector) check() string {
	mail, err := v.mail()
	if err != nil {
		return "bad vector: " + err.Error()
	}
	random, err := hex.DecodeString(v.Rand)
	if err != nil {
		return "bad vector: " + err.Error()
	}
	source := bytes.NewReader(random)
	message, err := Build(mail, Options{
		Now:  func() time.Time { return time.UnixMilli(v.Now) },
		Rand: source,
	})
	if v.Err != nil {
		if err == nil {
			return fmt.Sprintf("no error, want %q", *v.Err)
		}
		if err.Error() != *v.Err {
			return fmt.Sprintf("error %q, want %q", err, *v.Err)
		}
		return ""
	}
	if err != nil {
		return "error: " + err.Error()
	}
	if source.Len() != 0 {
		return fmt.Sprintf("%d of %d random bytes not drawn", source.Len(), len(random))
	}

	wantFrom := ""
	if string(v.EnvFrom) != "false" {
		if err := json.Unmarshal(v.EnvFrom, &wantFrom); err != nil {
			return "bad vector: " + err.Error()
		}
		if wantFrom == "" {
			return "bad vector: an empty envelope sender that is not false"
		}
	}
	if message.EnvelopeFrom != wantFrom {
		return fmt.Sprintf("envelope from %q, want %q", message.EnvelopeFrom, wantFrom)
	}
	if message.EnvelopeTo == nil {
		return "envelope to is nil"
	}
	if v.EnvToSHA256 != "" {
		if got := sha256Hex([]byte(strings.Join(message.EnvelopeTo, "\n"))); len(message.EnvelopeTo) != v.EnvToCount || got != v.EnvToSHA256 {
			return fmt.Sprintf("envelope to: %d addresses with hash %s, want %d with %s", len(message.EnvelopeTo), got, v.EnvToCount, v.EnvToSHA256)
		}
	} else if !slices.Equal(message.EnvelopeTo, v.EnvTo) {
		return fmt.Sprintf("envelope to %q, want %q", message.EnvelopeTo, v.EnvTo)
	}
	if message.MessageID != v.ID {
		return fmt.Sprintf("Message-ID %q, want %q", message.MessageID, v.ID)
	}
	if v.Raw == nil {
		got := sha256Hex(message.Raw)
		if len(message.Raw) == v.RawLength && got == v.RawSHA256 {
			return ""
		}
		if texts := qpTexts(mail); slices.ContainsFunc(texts, func(text string) bool { return strings.ContainsAny(text, "\r\n") }) {
			// BUGS EM15, for a message recorded by its hash only: its
			// quoted-printable bodies decode to the texts with CR LF for
			// every line end
			v.bareLineEnds = true
			return checkQPBodies(message.Raw, texts)
		}
		return fmt.Sprintf("message of %d bytes with hash %s, want %d bytes with %s", len(message.Raw), got, v.RawLength, v.RawSHA256)
	}
	// the recorded message has one character per byte
	want := make([]byte, 0, len(*v.Raw))
	for _, r := range *v.Raw {
		if r > 0xff {
			return fmt.Sprintf("bad vector: character %U in the message", r)
		}
		want = append(want, byte(r))
	}
	got := message.Raw
	if bytes.Equal(got, want) {
		return ""
	}
	if foldedAtOtherSpace(want) {
		// BUGS EM13: nodemailer folded a header in front of white space
		// other than a space or a tab, Build folds only in front of those
		// two; unfolded, the headers are the same
		v.foldedAtOtherSpace = true
		if foldedAtOtherSpace(got) {
			return "a header line starts with white space other than a space or a tab"
		}
		got, want = unfold(got), unfold(want)
	}
	if gotRest, gotBodies := qpBodies(got); hasBareLineEnd(want) {
		// BUGS EM15: nodemailer kept a bare CR or LF of a text in its
		// quoted-printable body, Build writes every line end as CR LF; the
		// rest of the message is the same, and decoded, so is the text
		v.bareLineEnds = true
		wantRest, wantBodies := qpBodies(want)
		if !bytes.Equal(gotRest, wantRest) || len(gotBodies) != len(wantBodies) {
			return fmt.Sprintf("outside the quoted-printable bodies, the message differs\n got  %q\n want %q", gotRest, wantRest)
		}
		for k, body := range gotBodies {
			for _, line := range bytes.SplitAfter(body, []byte("\r\n")) {
				if line = bytes.TrimSuffix(line, []byte("\r\n")); bytes.ContainsAny(line, "\r\n") || len(line) > maxBodyLine {
					return fmt.Sprintf("quoted-printable line %q", line)
				}
			}
			if g, w := lineEndsAsLF(decodeQP(body)), lineEndsAsLF(decodeQP(wantBodies[k])); !bytes.Equal(g, w) {
				return fmt.Sprintf("quoted-printable body %d decodes to %q, want %q", k, g, w)
			}
		}
		return ""
	}
	if !bytes.Equal(got, want) {
		at := 0
		for at < len(want) && at < len(got) && want[at] == got[at] {
			at++
		}
		from := max(at-60, 0)
		return fmt.Sprintf("message differs at byte %d\n got  ...%q\n want ...%q", at,
			got[from:min(at+80, len(got))], want[from:min(at+80, len(want))])
	}
	return ""
}

// qpBodies cuts a message into the bodies of its quoted-printable parts
// and the rest, which keeps a mark where each body was. A body starts
// after the headers of its part and ends at the boundary of its part, or
// at the end of a message of one part.
func qpBodies(raw []byte) (rest []byte, bodies [][]byte) {
	head, _, _ := bytes.Cut(raw, []byte("\r\n\r\n"))
	var delimiter []byte
	if _, after, found := bytes.Cut(head, []byte(`boundary="`)); found {
		boundary, _, _ := bytes.Cut(after, []byte(`"`))
		delimiter = append([]byte("\r\n--"), boundary...)
	}
	for {
		at := bytes.Index(raw, []byte("Content-Transfer-Encoding: quoted-printable\r\n"))
		if at < 0 {
			return append(rest, raw...), bodies
		}
		start := bytes.Index(raw[at:], []byte("\r\n\r\n"))
		if start < 0 {
			return append(rest, raw...), bodies
		}
		start += at + len("\r\n\r\n")
		rest = append(rest, raw[:start]...)
		raw = raw[start:]
		end := len(raw)
		if delimiter != nil {
			if i := bytes.Index(raw, delimiter); i >= 0 {
				end = i
			}
		}
		bodies = append(bodies, raw[:end])
		rest = append(rest, "<body>"...)
		raw = raw[end:]
	}
}

// qpTexts are the texts of a mail that go into quoted-printable bodies, in
// the order of the parts.
func qpTexts(m Mail) []string {
	var texts []string
	for _, text := range []*string{m.Text, m.HTML} {
		if text != nil && transferEncoding(*text) == transferQP {
			texts = append(texts, *text)
		}
	}
	return texts
}

// checkQPBodies checks the quoted-printable bodies of a message against
// the texts that went into them: every line ends in CR LF and has at most
// 76 characters, and each body decodes to its text, whose line ends may be
// CR, LF or CR LF. A message of one part ends in a line break that its
// text may lack.
func checkQPBodies(raw []byte, texts []string) string {
	head, _, _ := bytes.Cut(raw, []byte("\r\n\r\n"))
	_, bodies := qpBodies(raw)
	if len(bodies) != len(texts) {
		return fmt.Sprintf("%d quoted-printable bodies for %d texts", len(bodies), len(texts))
	}
	for k, body := range bodies {
		for _, line := range bytes.SplitAfter(body, []byte("\r\n")) {
			if line = bytes.TrimSuffix(line, []byte("\r\n")); bytes.ContainsAny(line, "\r\n") || len(line) > maxBodyLine {
				return fmt.Sprintf("quoted-printable line %q", line)
			}
		}
		want := lineEndsAsLF([]byte(texts[k]))
		if !bytes.Contains(head, []byte("boundary=")) && !bytes.HasSuffix(want, []byte("\n")) {
			want = append(want, '\n')
		}
		if got := lineEndsAsLF(decodeQP(body)); !bytes.Equal(got, want) {
			return fmt.Sprintf("quoted-printable body %d decodes to %q, want %q", k, got, want)
		}
	}
	return ""
}

// hasBareLineEnd reports whether a quoted-printable body of a message has
// a CR or an LF that is not part of CR LF.
func hasBareLineEnd(raw []byte) bool {
	_, bodies := qpBodies(raw)
	for _, body := range bodies {
		if bytes.Count(body, []byte("\r")) != bytes.Count(body, []byte("\r\n")) || bytes.Count(body, []byte("\n")) != bytes.Count(body, []byte("\r\n")) {
			return true
		}
	}
	return false
}

// decodeQP takes the quoted-printable encoding off a body: "=" and CR LF
// is a soft break, "=XX" a byte, the rest is literal.
func decodeQP(body []byte) []byte {
	body = bytes.ReplaceAll(body, []byte("=\r\n"), nil)
	out := make([]byte, 0, len(body))
	for i := 0; i < len(body); i++ {
		if body[i] == '=' && i+2 < len(body) && isHexDigit(body[i+1]) && isHexDigit(body[i+2]) {
			out = append(out, byte(hexValue(body[i+1], body[i+2])))
			i += 2
			continue
		}
		out = append(out, body[i])
	}
	return out
}

// lineEndsAsLF writes every line end of a text, CR LF, CR or LF, as LF.
func lineEndsAsLF(text []byte) []byte {
	return []byte(strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(string(text)))
}

// describe prints the input of a vector for a failure message.
func (v *messageVector) describe() string {
	show := func(s *string) string {
		if s == nil {
			return "undefined"
		}
		if len(*s) > 300 {
			return fmt.Sprintf("%q... (%d bytes)", (*s)[:300], len(*s))
		}
		return fmt.Sprintf("%q", *s)
	}
	to := string(v.To)
	if len(to) > 300 {
		to = to[:300] + "..."
	}
	return fmt.Sprintf("from %s to %s subject %s\n text %s\n html %s", show(v.From), to, show(v.Subject), show(v.Text), show(v.HTML))
}

// vectorCounts counts the vectors of a file: all of them, the ones that
// recorded an error, the ones nodemailer folded in front of white space
// other than a space or a tab, and the ones whose quoted-printable body
// kept a bare CR or LF.
type vectorCounts struct{ all, withError, foldedAtOtherSpace, bareLineEnds int }

// replayMessages checks every vector of a file and counts them.
func replayMessages(t *testing.T, file string) vectorCounts {
	var n vectorCounts
	failed := 0
	readLines(t, file, func(line []byte) {
		var v messageVector
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&v); err != nil {
			t.Fatalf("decode vector %d: %v", n.all, err)
		}
		if v.Err != nil {
			n.withError++
		}
		if diff := v.check(); diff != "" {
			failed++
			t.Errorf("vector %d: %s\n %s", n.all, diff, v.describe())
			if failed >= 25 {
				t.Fatalf("stopping after %d failures", failed)
			}
		}
		if v.foldedAtOtherSpace {
			n.foldedAtOtherSpace++
		}
		if v.bareLineEnds {
			n.bareLineEnds++
		}
		n.all++
	})
	return n
}

// TestNodeMessageVectors replays the mails of
// testdata/nodeoracle/message.js: for each one Build gets the same mail,
// time and random bytes as nodemailer and has to give the same envelope,
// Message-ID and message bytes, or the same error. Where nodemailer folded
// a header in front of white space other than a space or a tab, the
// headers are the same once unfolded (BUGS EM13); where it kept a bare CR
// or LF in a quoted-printable body, the body decodes to the same text with
// CR LF for every line end (EM15).
func TestNodeMessageVectors(t *testing.T) {
	n := replayMessages(t, "testdata/message.json.gz")
	if n.all < 8000 || n.withError == 0 || n.foldedAtOtherSpace == 0 || n.bareLineEnds == 0 {
		t.Fatalf("testdata/message.json.gz is incomplete: %+v", n)
	}
	t.Logf("%+v", n)
}

// TestExtraMessageVectors replays a second file of vectors, named by the
// environment variable MESSAGE_VECTORS (see testdata/nodeoracle/README.md).
func TestExtraMessageVectors(t *testing.T) {
	file := os.Getenv("MESSAGE_VECTORS")
	if file == "" {
		t.Skip("MESSAGE_VECTORS is not set")
	}
	t.Logf("%+v", replayMessages(t, file))
}
