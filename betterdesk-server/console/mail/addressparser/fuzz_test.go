package addressparser

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// checkResult fails when a parsed list breaks one of the rules that hold for
// every input.
func checkResult(t *testing.T, input string, list []Address) {
	t.Helper()
	if list == nil {
		t.Fatalf("Parse(%q) is nil", input)
	}
	size := 0
	checkText := func(value string) {
		size += len(value)
		if !utf8.ValidString(value) {
			t.Errorf("Parse(%q) gives %q, which is no valid UTF-8", input, value)
		}
		if strings.IndexFunc(value, func(c rune) bool { return c < 0x20 && c != '\t' }) >= 0 {
			t.Errorf("Parse(%q) gives %q, which holds a control character", input, value)
		}
	}
	checkMailbox := func(entry Address) {
		if entry.Group != nil {
			t.Errorf("Parse(%q) gives the mailbox %q a member list", input, entry.Name)
		}
		checkText(entry.Name)
		checkText(entry.Address)
	}
	mailboxes := 0
	for _, entry := range list {
		if !entry.IsGroup {
			mailboxes++
			checkMailbox(entry)
			continue
		}
		if entry.Address != "" {
			t.Errorf("Parse(%q) gives the group %q an address", input, entry.Name)
		}
		if entry.Group == nil {
			t.Errorf("Parse(%q) gives the group %q a nil member list", input, entry.Name)
		}
		checkText(entry.Name)
		for _, member := range entry.Group {
			if member.IsGroup {
				t.Errorf("Parse(%q) gives a group inside the group %q", input, entry.Name)
			}
			mailboxes++
			checkMailbox(member)
		}
	}
	// every character of the result comes from one of the input, plus at
	// most a backslash, a space or a quote beside it
	if size > 4*len(input)+8 {
		t.Errorf("Parse(%q) gives %d bytes of text for %d bytes of input", input, size, len(input))
	}

	flat := Flatten(list)
	if len(flat) != mailboxes {
		t.Errorf("Flatten(Parse(%q)) has %d entries, want %d", input, len(flat), mailboxes)
	}
	next := 0
	for _, entry := range list {
		members := []Address{entry}
		if entry.IsGroup {
			members = entry.Group
		}
		for _, member := range members {
			if next < len(flat) && !reflect.DeepEqual(flat[next], member) {
				t.Errorf("Flatten(Parse(%q)) entry %d is %s, want %s", input, next, show(flat[next:next+1]), show([]Address{member}))
			}
			next++
		}
	}
	for _, entry := range flat {
		if entry.IsGroup {
			t.Errorf("Flatten(Parse(%q)) holds the group %q", input, entry.Name)
		}
	}

	// the same input gives the same list, and Parse keeps no state
	if again := Parse(input); !reflect.DeepEqual(again, list) {
		t.Errorf("Parse(%q) gives %s the second time, %s the first", input, show(again), show(list))
	}
}

// TestVectorsKeepTheRules checks the rules of checkResult on every input of
// the vector file.
func TestVectorsKeepTheRules(t *testing.T) {
	for _, v := range loadVectors(t, "testdata/addressparser.json") {
		checkResult(t, v.In, Parse(v.In))
	}
}

func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		"",
		"user@example.com",
		"Joe Foo, PhD <joe@example.com>, \"Foo, Bar\" <bar@example.com>",
		"Group: a@example.com, B <b@example.com>;, c@example.com",
		"Outer: Inner: a@b; c@d;",
		"\"user@evil.com\"@good.com (comment (nested) \\) x) <a(c)@[IPv6:2001:db8::1]>",
		"<@relay1,@relay2:user@example.com>",
		"text a@b.c-- text\u00a0x @ y\u2028\ufeff",
		"a@b\r\nBcc: x@y\x00\x01\x7f",
		"<\"a b\"@c d> \"\"@e \\ ' [ ] : ; ,",
		"\xff\xed\xa0\x80\U0001F600 <a@b>",
	} {
		f.Add(seed)
	}
	vectors := loadVectors(f, "testdata/addressparser.json")
	for i := 0; i < len(vectors); i += 20 {
		if len(vectors[i].In) <= 200 {
			f.Add(vectors[i].In)
		}
	}
	f.Fuzz(func(t *testing.T, input string) {
		checkResult(t, input, Parse(input))
	})
}
