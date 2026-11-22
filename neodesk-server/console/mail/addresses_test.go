package mail

import (
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/mail/addressparser"
)

// What an alert rule's notify_emails may hold: plain addresses separated
// by commas. A line break, a group or a display name would change who gets
// the mail (BUGS EM14), a space that is no ASCII space would start a
// header line after folding (EM13), and a domain like 1.2.3 is rewritten
// as an IPv4 address (EM16).
func TestPlainAddressList(t *testing.T) {
	for _, ok := range []string{
		"", " ", "ops@example.com", "a@example.com,b@example.org", " a@example.com , b@example.org ", "a@example.com,",
		"first.last+tag@mail.example.co.uk", "x!#$%&'*+-/=?^_`{|}~@example.com", "a@localhost", "a@sub-domain.example", "a@1.2.example3",
		"a@x" + strings.Repeat(".y", 100),
	} {
		if !PlainAddressList(ok) {
			t.Errorf("%q is refused", ok)
		}
	}
	for _, bad := range []string{
		"x@y.z\r\nCc: b@c.d", "x@y.z\nb@c.d", "a@example.com\tb@example.com", "Ops <ops@example.com>", "ops@example.com (ops)",
		"group: a@example.com;", `"a b"@example.com`, "a@@example.com", "@example.com", "a@", "a", "a b@example.com",
		"a@example..com", "a@.example.com", "a@-example.com", "a@example-.com", ".a@example.com", "a.@example.com", "a..b@example.com",
		"\"aaa X-Header: v\"@example.com", "a @example.com", "a@example.com ", " a@example.com", "é@example.com", "a@exämple.com",
		"a@1.2.3", "a@1.2.0.3", "a@[127.0.0.1]", "a@[a@b@c]", "a@example.123", "a@0x10", "a@example.com\x00", "a@example_x.com",
		strings.Repeat("a", 65) + "@example.com", "a@" + strings.Repeat("b", 64) + ".com",
	} {
		if PlainAddressList(bad) {
			t.Errorf("%q is taken", bad)
		}
	}
}

// Whatever text PlainAddressList takes, nodemailer's parser reads it as
// the same plain addresses, one per entry, without a name or a group.
func FuzzPlainAddressList(f *testing.F) {
	for _, seed := range []string{"a@example.com", "a@b.c,d@e.f", "x@y.z\r\nCc: b@c.d", "a <b@c.d>", "g: a@b.c;"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if !PlainAddressList(text) {
			return
		}
		var want []string
		for _, entry := range strings.Split(text, ",") {
			if entry = strings.Trim(entry, " \t"); entry != "" {
				want = append(want, entry)
			}
		}
		got := addressparser.Parse(text)
		if len(got) != len(want) {
			t.Fatalf("%q reads as %d addresses, want %d", text, len(got), len(want))
		}
		for i, address := range got {
			if address.Name != "" || address.IsGroup || address.Address != want[i] {
				t.Fatalf("%q: entry %d reads as %+v, want the address %q", text, i, address, want[i])
			}
		}
	})
}
