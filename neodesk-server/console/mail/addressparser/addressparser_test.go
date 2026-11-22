package addressparser

import (
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestResultShape(t *testing.T) {
	if got := Parse(""); got == nil || len(got) != 0 {
		t.Errorf("Parse(\"\") = %#v, want an empty list that is not nil", got)
	}
	if got := Flatten(nil); got == nil || len(got) != 0 {
		t.Errorf("Flatten(nil) = %#v, want an empty list that is not nil", got)
	}

	got := Parse("Empty:; a@example.com, Team: Joe <joe@example.com>, b@example.com;")
	want := []Address{
		{Name: "Empty", IsGroup: true, Group: []Address{}},
		{Address: "a@example.com"},
		{Name: "Team", IsGroup: true, Group: []Address{
			{Name: "Joe", Address: "joe@example.com"},
			{Address: "b@example.com"},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Parse\n got  %s\n want %s", show(got), show(want))
	}
	if got[0].Group == nil {
		t.Error("the members of an empty group are nil, want an empty list")
	}
	if got[1].Group != nil {
		t.Error("a mailbox has a member list, want nil")
	}
}

func TestFlatten(t *testing.T) {
	a := Address{Name: "A", Address: "a@example.com"}
	b := Address{Address: "b@example.com"}
	c := Address{Name: "C"}
	d := Address{Address: "d@example.com"}
	tests := []struct {
		name string
		list []Address
		want []Address
	}{
		{"mailboxes only", []Address{a, b}, []Address{a, b}},
		{"group in the middle", []Address{a, {Name: "G", IsGroup: true, Group: []Address{b, c}}, d}, []Address{a, b, c, d}},
		{"empty group", []Address{{Name: "G", IsGroup: true, Group: []Address{}}, a}, []Address{a}},
		{"group without a member list", []Address{{Name: "G", IsGroup: true}}, []Address{}},
		{"group in a group", []Address{{Name: "G", IsGroup: true, Group: []Address{a, {Name: "H", IsGroup: true, Group: []Address{b, c}}, d}}}, []Address{a, b, c, d}},
		{"a group name is not an entry", []Address{{Name: "G", Address: "ignored@example.com", IsGroup: true, Group: []Address{b}}}, []Address{b}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Flatten(test.list); !reflect.DeepEqual(got, test.want) {
				t.Errorf("got %s, want %s", show(got), show(test.want))
			}
		})
	}

	// Flatten leaves the list it is given alone
	list := Parse("G: a@example.com, b@example.com;, c@example.com")
	before := show(list)
	flat := Flatten(list)
	flat[0].Address = "changed@example.com"
	if show(list) != before {
		t.Errorf("Flatten changed its argument: %s, was %s", show(list), before)
	}
}

// Bytes that are no valid UTF-8 read as U+FFFD, one for every byte, and the
// result is valid UTF-8.
func TestInvalidUTF8(t *testing.T) {
	tests := []struct {
		in   string
		want []Address
	}{
		{"a\xff@b", []Address{{Address: "a\ufffd@b"}}},
		{"\xff\xfe <a@b>", []Address{{Name: "\ufffd\ufffd", Address: "a@b"}}},
		// a surrogate half in UTF-8 form is three invalid bytes
		{"\xed\xa0\x80 <a@b>", []Address{{Name: "\ufffd\ufffd\ufffd", Address: "a@b"}}},
		// a sequence cut short
		{"a@b\xf0\x9f\x98", []Address{{Address: "a@b\ufffd\ufffd\ufffd"}}},
		// an invalid byte is no whitespace and no word character
		{"x \xffa@b\xff y", []Address{{Name: "x \ufffd \ufffd y", Address: "a@b"}}},
		{"\xc0\x80", []Address{{Name: "\ufffd\ufffd"}}},
	}
	for _, test := range tests {
		got := Parse(test.in)
		if !reflect.DeepEqual(got, test.want) {
			t.Errorf("Parse(%q)\n got  %s\n want %s", test.in, show(got), show(test.want))
		}
		for _, entry := range got {
			if !utf8.ValidString(entry.Name) || !utf8.ValidString(entry.Address) {
				t.Errorf("Parse(%q) gives %s, which is no valid UTF-8", test.in, show(got))
			}
		}
	}
}

// No value in a result holds a control character other than a tab, so no
// address or name can carry a line break into a header.
func TestNoControlCharacters(t *testing.T) {
	inputs := []string{
		"a@b\r\nBcc: x@y",
		"Name\r\nBcc: x@y <a@b>",
		"\"Name\r\nBcc: x@y\" <a@b>",
		"<a@b\r\nBcc: x@y>",
		"(a\r\nb) <a@b(c\r\nd)>",
		"G\r\n: a\x00@b\x1f;",
		"\"a\\\r\\\n\"@b",
	}
	for _, input := range inputs {
		for _, entry := range Flatten(Parse(input)) {
			for _, value := range []string{entry.Name, entry.Address} {
				if i := strings.IndexFunc(value, func(c rune) bool { return c < 0x20 && c != '\t' }); i >= 0 {
					t.Errorf("Parse(%q) gives %q, which holds a control character", input, value)
				}
			}
		}
	}
	got := Parse("a@b\r\nBcc: x@y")
	want := []Address{{Name: "a@b Bcc", IsGroup: true, Group: []Address{{Address: "x@y"}}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %s, want %s", show(got), show(want))
	}
}

func TestIsSpace(t *testing.T) {
	spaces := map[rune]bool{
		0x09: true, 0x0a: true, 0x0b: true, 0x0c: true, 0x0d: true, 0x20: true, 0xa0: true, 0x1680: true,
		0x2000: true, 0x2001: true, 0x2002: true, 0x2003: true, 0x2004: true, 0x2005: true, 0x2006: true,
		0x2007: true, 0x2008: true, 0x2009: true, 0x200a: true, 0x2028: true, 0x2029: true, 0x202f: true,
		0x205f: true, 0x3000: true, 0xfeff: true,
	}
	for c := rune(0); c <= 0x10ffff; c++ {
		if isSpace(c) != spaces[c] {
			t.Errorf("isSpace(U+%04X) = %v, want %v", c, isSpace(c), spaces[c])
		}
	}
}

func TestIsWordAndBoundary(t *testing.T) {
	for c := rune(0); c <= 0x10ffff; c++ {
		want := strings.ContainsRune("0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz_", c)
		if isWord(c) != want {
			t.Errorf("isWord(U+%04X) = %v, want %v", c, isWord(c), want)
		}
	}
	text := []rune("ab \u00e9_z")
	want := map[int]bool{0: true, 2: true, 4: true, 6: true}
	for at := -2; at <= len(text)+2; at++ {
		if got := isBoundary(text, at); got != want[at] {
			t.Errorf("isBoundary(%q, %d) = %v, want %v", string(text), at, got, want[at])
		}
	}
	if isBoundary(nil, 0) {
		t.Error("isBoundary on an empty text is true")
	}
}

func TestQuotedPrefix(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{``, -1},
		{`a"b"`, -1},
		{`"`, -1},
		{`""`, 2},
		{`"a"`, 3},
		{`"a"b"`, 3},
		{`"a\"b"c`, 6},
		{`"a\"`, -1},
		{`"a\`, -1},
		{`"a\\"`, 5},
		{`"\\\"" x`, 6},
		{"\"a\nb\" x", 5},
	}
	for _, test := range tests {
		if got := quotedPrefix([]rune(test.in)); got != test.want {
			t.Errorf("quotedPrefix(%q) = %d, want %d", test.in, got, test.want)
		}
	}
}

func TestDomainAt(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"", -1},
		{"ab", -1},
		{"a@b", 1},
		{"a@b@c", 3},
		{"a@[x@y]", 1},
		{"a@[x@y", 4},
		{"a@x@y]", 3},
		{"a@[x]@y]", 5},
		{"a@[x[@y]", 5},
		{"a@[x\\@y]", 5},
		{"a@b[x@y]", 5},
		{"[x@y]", 2},
		{"@[]", 0},
		{"]", -1},
	}
	for _, test := range tests {
		if got := domainAt([]rune(test.in)); got != test.want {
			t.Errorf("domainAt(%q) = %d, want %d", test.in, got, test.want)
		}
	}
}

func TestQuoteLocalPart(t *testing.T) {
	tests := []struct{ in, want string }{
		{"nodomain", "nodomain"},
		{"user@example.com", "user@example.com"},
		{"user@evil.com@good.com", `"user@evil.com"@good.com`},
		{"a b@c", `"a b"@c`},
		{"a\u00a0b@c", "\"a\u00a0b\"@c"},
		{`a"b\c@d`, `"a\"b\\c"@d`},
		{`"a b"@c`, `"a b"@c`},
		{`"a"b@c`, `"\"a\"b"@c`},
		{`"a\"@c`, `"\"a\\\""@c`},
		{"@c", `""@c`},
		{"a,b@c", `"a,b"@c`},
		{"a@x@[y@z]", `"a@x"@[y@z]`},
	}
	for _, test := range tests {
		if got := string(quoteLocalPart([]rune(test.in))); got != test.want {
			t.Errorf("quoteLocalPart(%q) = %q, want %q", test.in, got, test.want)
		}
	}
	for _, special := range `"(),:;<>@[\]` + " \t" {
		in := "a" + string(special) + "b@c"
		if got := string(quoteLocalPart([]rune(in))); !strings.HasPrefix(got, `"`) {
			t.Errorf("quoteLocalPart(%q) = %q, want the local part quoted", in, got)
		}
	}
}

func TestIsAddrSpec(t *testing.T) {
	tests := []struct {
		in            string
		strict, loose bool
	}{
		{"", false, false},
		{"a", false, false},
		{"@", false, false},
		{"a@", false, false},
		{"@b", false, false},
		{"a@b", true, true},
		{"a@b@c", false, true},
		{"a@@", false, true},
		{"a @b", false, false},
		{"a@b\u2028", false, false},
		{"a@[]", true, true},
		{"a@[x@y]", true, true},
		{"a@[x@y", false, true},
		{"a@[x]y]", true, true},
		{"a@[x]@y]", false, true},
		{"a@[x[y]", true, true},
		{"a@[x@[y]", false, true},
		{"a@[x@\\y]", false, true},
		{"a@[x@y]z", false, true},
		{"a@x[@y]", false, true},
	}
	for _, test := range tests {
		if got := isAddrSpec([]rune(test.in)); got != test.strict {
			t.Errorf("isAddrSpec(%q) = %v, want %v", test.in, got, test.strict)
		}
		if got := isLooseAddrSpec([]rune(test.in)); got != test.loose {
			t.Errorf("isLooseAddrSpec(%q) = %v, want %v", test.in, got, test.loose)
		}
	}
}

func TestIsQuotedLocalDomain(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"@", false},
		{"b", false},
		{"@b", true},
		{"@b@c", false},
		{"@b c", false},
		{`@b"c`, false},
		{"@[]", true},
		{"@[x]", true},
		{"@[x@y]", true},
		{`@["]`, true},
		{`@[x@"y]`, true},
		{"@[x@y", false},
		{"@[x@ y]", false},
		{"@[x@]y]", false},
		{"@[x@[y]", false},
		{"@[x@\\y]", false},
		{"@[x@y]z", false},
	}
	for _, test := range tests {
		if got := isQuotedLocalDomain([]rune(test.in)); got != test.want {
			t.Errorf("isQuotedLocalDomain(%q) = %v, want %v", test.in, got, test.want)
		}
	}
}

func TestSplitQuotedLocalAddr(t *testing.T) {
	tests := []struct {
		in         string
		addr, rest string
		hasRest    bool
		ok         bool
	}{
		{`"a b"@c`, `"a b"@c`, "", false, true},
		{`"a b"@c d e`, `"a b"@c`, "d e", true, true},
		{"\"a\"@c \u00a0\td  ", `"a"@c`, "d  ", true, true},
		// whitespace up to the end: all but the last character parts the two
		{`"a"@c  `, `"a"@c`, " ", true, true},
		{"\"a\"@c \t\u00a0", `"a"@c`, "\u00a0", true, true},
		// a single whitespace character at the end is no rest
		{`"a"@c `, "", "", false, false},
		{`"a"@`, "", "", false, false},
		{`"a"@ c`, "", "", false, false},
		{`"a" @c`, "", "", false, false},
		{`"a"`, "", "", false, false},
		{`"a@c d`, "", "", false, false},
		{`a"b"@c d`, "", "", false, false},
		{`"a\"@c d`, "", "", false, false},
		{`"a\" b"@c d`, `"a\" b"@c`, "d", true, true},
	}
	for _, test := range tests {
		addr, rest, hasRest, ok := splitQuotedLocalAddr([]rune(test.in))
		if string(addr) != test.addr || string(rest) != test.rest || hasRest != test.hasRest || ok != test.ok {
			t.Errorf("splitQuotedLocalAddr(%q) = %q, %q, %v, %v, want %q, %q, %v, %v",
				test.in, string(addr), string(rest), hasRest, ok, test.addr, test.rest, test.hasRest, test.ok)
		}
	}
}

func TestSplitSpace(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"", []string{""}},
		{"a", []string{"a"}},
		{"a b", []string{"a", "b"}},
		{"a \t\u00a0 b", []string{"a", "b"}},
		{" a b ", []string{"", "a", "b", ""}},
		{"  ", []string{"", ""}},
	}
	for _, test := range tests {
		var got []string
		for _, part := range splitSpace([]rune(test.in)) {
			got = append(got, string(part))
		}
		if !reflect.DeepEqual(got, test.want) {
			t.Errorf("splitSpace(%q) = %q, want %q", test.in, got, test.want)
		}
	}
}

// The loose reading of an address in free text is tried at one offset only,
// and it takes the whitespace on both sides along.
func TestMatchLooseTextAddr(t *testing.T) {
	tests := []struct {
		in   string
		at   int
		end  int
		want bool
	}{
		{"a@b", 0, 3, true},
		{"x a@b.c y", 1, 8, true},
		{"x a@b.c y", 2, 8, true},
		{"x  a@b.c \u00a0 y", 1, 11, true},
		// no search: the match has to begin at the offset
		{"x a@b.c y", 0, 0, false},
		// a boundary is needed where the address begins
		{"x .a@b y", 1, 0, false},
		{"x .a@b y", 3, 7, true},
		{"xa@b", 1, 0, false},
		// and where it ends: the end falls back to the last boundary of the run
		{"a@b.c--- y", 0, 5, true},
		{"a@b--.c)) y", 0, 7, true},
		{"a@--- y", 0, 0, false},
		{"a@", 0, 0, false},
		{"a@ b", 0, 0, false},
		{"a @b", 0, 0, false},
		{"@b", 0, 0, false},
		{"-@b", 0, 0, false},
		{"a-@b", 0, 4, true},
		{"a@b@c@d e", 0, 8, true},
		{"a@@", 0, 0, false},
		{"a@b  ", 0, 5, true},
		{"", 0, 0, false},
		{"   ", 0, 0, false},
	}
	for _, test := range tests {
		end, ok := matchLooseTextAddr([]rune(test.in), test.at)
		if ok != test.want || (ok && end != test.end) {
			t.Errorf("matchLooseTextAddr(%q, %d) = %d, %v, want %d, %v", test.in, test.at, end, ok, test.end, test.want)
		}
	}
}

func TestLooseAddressStart(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"", -1},
		{"no address here", -1},
		{"a@b", 0},
		{"x a@b y", 1},
		{"x  \u00a0a@b y", 1},
		{"  a@b", 0},
		{"x .a@b y", 3},
		{"x -@- a@b", 5},
		{"x @b y", -1},
		{"x a@ y", -1},
		{"x a@- y", -1},
		{"x -@b y", -1},
		{"x @a@b y", 3},
		{"x -@a@b y", 4},
		{"x a@@b y", 1},
		{"x a@b@ y", 1},
		{"x @@ @@a@b y", 7},
	}
	for _, test := range tests {
		if got := looseAddressStart([]rune(test.in)); got != test.want {
			t.Errorf("looseAddressStart(%q) = %d, want %d", test.in, got, test.want)
		}
	}
}

func TestCollapseAtSpacing(t *testing.T) {
	tests := []struct{ in, want string }{
		{"a @ b", "a@b"},
		{"a\u00a0\t@\u2028b", "a@b"},
		{"a@b", "a@b"},
		{"a b", "a b"},
		{"a @ b @ c", "a @ b @ c"},
		{`"q" @ b`, `"q"@b`},
		{`"q @ x" @ b`, `"q @ x"@b`},
		{`"q" @ b @ c`, `"q" @ b @ c`},
		{`"q" x @ b`, `"q" x@b`},
		{`a @ "b"`, `a @ "b"`},
		{`x "q" @ b`, `x "q"@b`},
		{" @ ", "@"},
	}
	for _, test := range tests {
		if got := string(collapseAtSpacing([]rune(test.in))); got != test.want {
			t.Errorf("collapseAtSpacing(%q) = %q, want %q", test.in, got, test.want)
		}
	}
}

func TestStripAddressComments(t *testing.T) {
	tests := []struct {
		in       string
		want     string
		comments []string
	}{
		{"a@b", "a@b", nil},
		{" a@b ", "a@b", nil},
		{"a(x)@b", "a@b", []string{"x"}},
		{"a@(x)b", "a@b", []string{"x"}},
		{"a(x)b@c", "a b@c", []string{"x"}},
		{"a(x) b@c", "a b@c", []string{"x"}},
		{"a(x)(y)@b", "a@b", []string{"x", "y"}},
		{"a( x (y) \\) z )b", "a b", []string{"x (y) ) z"}},
		{"a()b", "a b", nil},
		{"a(x", "a", []string{"x"}},
		{"a(x\\", "a", []string{"x\\"}},
		{`"a(x)"@b`, `"a(x)"@b`, nil},
		{`"a\"(x)"@b`, `"a\"(x)"@b`, nil},
		{"a@[(x)]", "a@[(x)]", nil},
		{"a@[\\](x)", "a@[\\]", []string{"x"}},
		{"junk(x)<a@b", "a@b", nil},
		{`"a<b"@c`, `"a<b"@c`, nil},
		{"a(x)<", "", nil},
		{"@r1,@r2:a@b", "a@b", nil},
		{" @r1:a@b", "a@b", nil},
		{"@r1:a", "@r1:a", nil},
		{"@r1:a@b:c@d", "a@b:c@d", nil},
		{"x@r1:a@b", "x@r1:a@b", nil},
		{`@"r":a@b`, `@"r":a@b`, nil},
		{"@[r:1]:a@b", "a@b", nil},
		{"x<@r:a@b", "a@b", nil},
		{"@r:x<a@b", "a@b", nil},
	}
	for _, test := range tests {
		got, comments := stripAddressComments([]rune(test.in))
		var texts []string
		for _, comment := range comments {
			texts = append(texts, string(comment))
		}
		if string(got) != test.want || !reflect.DeepEqual(texts, test.comments) {
			t.Errorf("stripAddressComments(%q) = %q, %q, want %q, %q", test.in, string(got), texts, test.want, test.comments)
		}
	}
}

// The walk over the tokens of an entry holds for token lists the tokenizer
// never builds, which the vectors cannot show.
func TestCollectAddressPartsOnOddTokens(t *testing.T) {
	texts := func(data *addressParts) []string {
		var list []string
		for _, run := range data.runs[stateText] {
			list = append(list, string(run))
		}
		return list
	}

	// An entry that begins at the closing quote of `x""@b`: the quote in
	// front of the entry is not looked at, so the run opens on no empty
	// quoted string.
	list := tokenize([]rune(`x""@b`))
	data, _ := collectAddressParts(list, 2, len(list.tokens))
	if got := texts(data); !reflect.DeepEqual(got, []string{"@b"}) {
		t.Errorf("text runs = %q, want [\"@b\"]", got)
	}
	if !reflect.DeepEqual(data.quotedLocalAt, []int{-1}) {
		t.Errorf("quotedLocalAt = %v, want [-1]", data.quotedLocalAt)
	}

	// The whole of `""@b` does open on one.
	list = tokenize([]rune(`""@b`))
	data, _ = collectAddressParts(list, 0, len(list.tokens))
	if !reflect.DeepEqual(data.quotedLocalAt, []int{0}) || !reflect.DeepEqual(data.textWasQuoted, []bool{true}) {
		t.Errorf("quotedLocalAt = %v, textWasQuoted = %v, want [0], [true]", data.quotedLocalAt, data.textWasQuoted)
	}

	// An opening quote that runs on into its text: the text joins the run in
	// front of the quote and marks it as quoted.
	list = &tokenList{str: []rune(`a"b`), tokens: []token{
		{kind: 0, start: 0, end: 1},
		{kind: tokenOperator | tokenNoBreak, start: 1, end: 2},
		{kind: 0, start: 2, end: 3},
	}}
	data, _ = collectAddressParts(list, 0, 3)
	if got := texts(data); !reflect.DeepEqual(got, []string{"ab"}) {
		t.Errorf("text runs = %q, want [\"ab\"]", got)
	}
	if !reflect.DeepEqual(data.textWasQuoted, []bool{true}) || !reflect.DeepEqual(data.quotedLocalAt, []int{-1}) {
		t.Errorf("textWasQuoted = %v, quotedLocalAt = %v, want [true], [-1]", data.textWasQuoted, data.quotedLocalAt)
	}
	if string(list.str) != `a"b` {
		t.Errorf("joining runs changed the field to %q", string(list.str))
	}
}

// A run of name fragments in front of a mailbox folds into its name, and
// nothing else does.
func TestMergeNameFragments(t *testing.T) {
	group := Address{Name: "G", IsGroup: true, Group: []Address{}}
	tests := []struct {
		name string
		list []Address
		want []Address
	}{
		{"empty", nil, []Address{}},
		{"run of fragments",
			[]Address{{Name: "A"}, {Name: "B"}, {Name: "C", Address: "c@d"}},
			[]Address{{Name: "A, B, C", Address: "c@d"}}},
		{"address without a name takes none",
			[]Address{{Name: "A"}, {Address: "c@d"}},
			[]Address{{Name: "A"}, {Address: "c@d"}}},
		{"fragment with an address is none",
			[]Address{{Name: "A", Address: "a@b"}, {Name: "C", Address: "c@d"}},
			[]Address{{Name: "A", Address: "a@b"}, {Name: "C", Address: "c@d"}}},
		{"empty entry stops the run",
			[]Address{{Name: "A"}, {}, {Name: "B"}, {Name: "C", Address: "c@d"}},
			[]Address{{Name: "A"}, {}, {Name: "B, C", Address: "c@d"}}},
		{"trailing fragments stay",
			[]Address{{Name: "C", Address: "c@d"}, {Name: "A"}, {Name: "B"}},
			[]Address{{Name: "C", Address: "c@d"}, {Name: "A"}, {Name: "B"}}},
		{"two runs",
			[]Address{{Name: "A"}, {Name: "B", Address: "b@c"}, {Name: "D"}, {Name: "E"}, {Name: "F", Address: "f@g"}},
			[]Address{{Name: "A, B", Address: "b@c"}, {Name: "D, E, F", Address: "f@g"}}},
		{"a group is no fragment and takes none",
			[]Address{{Name: "A"}, group, {Name: "B", Address: "b@c"}},
			[]Address{{Name: "A"}, group, {Name: "B", Address: "b@c"}}},
		{"a group in front of a mailbox",
			[]Address{group, {Name: "B", Address: "b@c"}},
			[]Address{group, {Name: "B", Address: "b@c"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := mergeNameFragments(test.list); !reflect.DeepEqual(got, test.want) {
				t.Errorf("got %s, want %s", show(got), show(test.want))
			}
		})
	}
}

// Parsing takes time in proportion to the length of the input. Every shape
// here is 200,000 repetitions or more and parses in well under a second;
// code that goes over the input once per repetition needs most of a minute.
func TestLinearTime(t *testing.T) {
	if testing.Short() {
		t.Skip("long inputs")
	}
	const n = 200000
	shapes := map[string]string{
		"one letter names":            strings.Repeat("a,", n),
		"addresses":                   strings.Repeat("a@b,", n),
		"name fragments":              strings.Repeat("Joe, ", n) + "Foo <joe@example.com>",
		"name fragments in a group":   "G: " + strings.Repeat("Joe, ", n) + "Foo <joe@example.com>;",
		"free text":                   strings.Repeat("word ", n),
		"free text, address at end":   strings.Repeat("word ", n) + "a@b",
		"free text, late boundary":    strings.Repeat("- ", n) + "a@b",
		"comment joined atoms":        strings.Repeat("a(x)", n) + "@b",
		"comments in brackets":        "<" + strings.Repeat("a(x)", n) + "@b>",
		"atoms joined over comments":  strings.Repeat("a@(x)", n),
		"at signs joined over them":   strings.Repeat("@(x)", n),
		"text joined over quotes":     strings.Repeat("\"\"a", n),
		"text joined over brackets":   strings.Repeat("<a>b", n),
		"comments joined":             strings.Repeat("(x)", n),
		"only at signs":               strings.Repeat("@", n),
		"at signs without boundary":   strings.Repeat("-@", n),
		"runs without boundary":       strings.Repeat("-@- ", n),
		"at after every letter":       strings.Repeat("a@", n),
		"spaced at signs":             strings.Repeat(" @", n),
		"whitespace beside one at":    "x" + strings.Repeat(" ", n) + "@" + strings.Repeat(" ", n) + "y",
		"whitespace in brackets":      "<x" + strings.Repeat(" ", n) + "y>",
		"whitespace runs in brackets": "<" + strings.Repeat("x ", n) + ">",
		"nested comments":             strings.Repeat("(", n) + "a@b" + strings.Repeat(")", n),
		"nested brackets":             strings.Repeat("<", n) + "a@b" + strings.Repeat(">", n),
		"comments in open brackets":   "<" + strings.Repeat("(", n) + "a@b",
		"quotes":                      strings.Repeat("\"", n+1) + "a@b",
		"quoted pairs":                strings.Repeat("\"a\"", n) + "@b",
		"backslashes":                 strings.Repeat("\\", n+1),
		"quoted backslashes":          "\"" + strings.Repeat("\\", n+1) + "\"@b",
		"quotes in an address":        "<" + strings.Repeat("\"", n+1) + "@b c>",
		"nested groups":               strings.Repeat("G:", n) + "a@b" + strings.Repeat(";", n),
		"groups":                      strings.Repeat("G:a@b;", n),
		"colons in a literal":         "a@[" + strings.Repeat(":", n) + "]",
		"open literal":                "a@[" + strings.Repeat("@[", n),
		"literals in brackets":        "<a@" + strings.Repeat("[x]@", n) + "b c>",
		"routes":                      "<@" + strings.Repeat("a:", n) + "b@c>",
		"long address":                strings.Repeat("x", n) + "@" + strings.Repeat("y", n),
		"quoted local part":           "\"" + strings.Repeat("q ", n) + "\"@example.com",
		"quoted local part with tail": "<\"" + strings.Repeat("q ", n) + "\"@example.com " + strings.Repeat("t ", n) + ">",
		"control characters":          strings.Repeat("\x00", n) + "a@b",
		"line breaks":                 strings.Repeat("a\r\n", n) + "@b",
		"emoji":                       strings.Repeat("\U0001F600", n) + " <a@b>",
		"invalid bytes":               strings.Repeat("\xff", n) + " <a@b>",
		"brackets after text":         strings.Repeat("a <b@c> ", n),
		"second addresses":            strings.Repeat("<b@c>", n),
	}
	for name, input := range shapes {
		start := time.Now()
		Parse(input)
		if took := time.Since(start); took > 5*time.Second {
			t.Errorf("%s: %d bytes took %v", name, len(input), took)
		} else {
			t.Logf("%-30s %8d bytes %v", name, len(input), took)
		}
	}
}
