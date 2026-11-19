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
	// in front of white space other than a space or a tab
	foldedAtOtherSpace bool
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
		if got := sha256Hex(message.Raw); len(message.Raw) != v.RawLength || got != v.RawSHA256 {
			return fmt.Sprintf("message of %d bytes with hash %s, want %d bytes with %s", len(message.Raw), got, v.RawLength, v.RawSHA256)
		}
		return ""
	}
	// the recorded message has one character per byte
	want := make([]byte, 0, len(*v.Raw))
	for _, r := range *v.Raw {
		if r > 0xff {
			return fmt.Sprintf("bad vector: character %U in the message", r)
		}
		want = append(want, byte(r))
	}
	if !bytes.Equal(message.Raw, want) && foldedAtOtherSpace(want) {
		// BUGS EM13: nodemailer folded a header in front of white space
		// other than a space or a tab, Build folds only in front of those
		// two; unfolded, the headers are the same
		v.foldedAtOtherSpace = true
		switch {
		case foldedAtOtherSpace(message.Raw):
			return "a header line starts with white space other than a space or a tab"
		case !bytes.Equal(unfold(message.Raw), unfold(want)):
			return fmt.Sprintf("unfolded, the message differs\n got  %q\n want %q", unfold(message.Raw), unfold(want))
		}
		return ""
	}
	if !bytes.Equal(message.Raw, want) {
		at := 0
		for at < len(want) && at < len(message.Raw) && want[at] == message.Raw[at] {
			at++
		}
		from := max(at-60, 0)
		return fmt.Sprintf("message differs at byte %d\n got  ...%q\n want ...%q", at,
			message.Raw[from:min(at+80, len(message.Raw))], want[from:min(at+80, len(want))])
	}
	return ""
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

// replayMessages checks every vector of a file and returns how many there
// were, how many of them recorded an error and how many nodemailer folded
// in front of white space other than a space or a tab.
func replayMessages(t *testing.T, file string) (count, withError, foldedAtOtherSpace int) {
	failed := 0
	readLines(t, file, func(line []byte) {
		var v messageVector
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&v); err != nil {
			t.Fatalf("decode vector %d: %v", count, err)
		}
		if v.Err != nil {
			withError++
		}
		if diff := v.check(); diff != "" {
			failed++
			t.Errorf("vector %d: %s\n %s", count, diff, v.describe())
			if failed >= 25 {
				t.Fatalf("stopping after %d failures", failed)
			}
		}
		if v.foldedAtOtherSpace {
			foldedAtOtherSpace++
		}
		count++
	})
	return count, withError, foldedAtOtherSpace
}

// TestNodeMessageVectors replays the mails of
// testdata/nodeoracle/message.js: for each one Build gets the same mail,
// time and random bytes as nodemailer and has to give the same envelope,
// Message-ID and message bytes, or the same error. Where nodemailer folded
// a header in front of white space other than a space or a tab, the
// headers are the same once unfolded (BUGS EM13).
func TestNodeMessageVectors(t *testing.T) {
	count, withError, folded := replayMessages(t, "testdata/message.json.gz")
	if count < 8000 || withError == 0 || folded == 0 {
		t.Fatalf("%d vectors, %d with an error, %d folded at other white space, in testdata/message.json.gz: the file is incomplete", count, withError, folded)
	}
	t.Logf("%d vectors, %d with an error, %d folded at other white space", count, withError, folded)
}

// TestExtraMessageVectors replays a second file of vectors, named by the
// environment variable MESSAGE_VECTORS (see testdata/nodeoracle/README.md).
func TestExtraMessageVectors(t *testing.T) {
	file := os.Getenv("MESSAGE_VECTORS")
	if file == "" {
		t.Skip("MESSAGE_VECTORS is not set")
	}
	count, withError, folded := replayMessages(t, file)
	t.Logf("%d vectors, %d with an error, %d folded at other white space", count, withError, folded)
}
