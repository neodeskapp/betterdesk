package auditlog

import "testing"

// The first four tests are the cases of web-nodejs/tests/logRedact.test.js.

func TestRedactURLStripsCredentials(t *testing.T) {
	if got := RedactURL("https://user:secret@example.com/path"); got != "https://example.com/path" {
		t.Errorf("got %q", got)
	}
}

func TestRedactUsernameMasksUsername(t *testing.T) {
	if got := RedactUsername("admin"); got != "a***n" {
		t.Errorf("admin: got %q", got)
	}
	if got := RedactUsername(""); got != "(empty)" {
		t.Errorf("empty: got %q", got)
	}
}

func TestSanitizeLogValueStripsNewlines(t *testing.T) {
	if got := SanitizeLogValue("line1\nline2"); got != `line1\nline2` {
		t.Errorf("got %q", got)
	}
}

func TestRedactDetailsMasksUsernameAndSecrets(t *testing.T) {
	if got := RedactDetails("Username: administrator"); got != "Username: a***r" {
		t.Errorf("username: got %q", got)
	}
	if got := RedactDetails("token=abc123"); got != "token=***" {
		t.Errorf("token: got %q", got)
	}
}

func TestRedactUsername(t *testing.T) {
	cases := []struct{ in, want string }{
		{"a", "***"},
		{"ab", "***"},
		{"abc", "a***c"},
		{"  admin\t", "a***n"},
		{"   ", "(empty)"},
		{"\ufeffadmin\u00a0", "a***n"},
		// U+0085 is no white space for JavaScript.
		{"\u0085admin", "\u0085***n"},
		// Lengths count UTF-16 code units: one emoji is two.
		{"\U0001F600", "***"},
		{"\U0001F600a", "\uFFFD***a"},
		{"a\U0001F600", "a***\uFFFD"},
		{"\u00e9t\u00e9", "\u00e9***\u00e9"},
		{"bad\xffbyte", "b***e"},
		{"\xffab", "\uFFFD***b"},
	}
	for _, c := range cases {
		if got := RedactUsername(c.in); got != c.want {
			t.Errorf("RedactUsername(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSanitizeLogValue(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"a\r\nb", `a\n\nb`},
		{"a\rb", `a\nb`},
		{"a\u2028b\u2029c", `a\nb\nc`},
		{"a\u0085b\vc\td", "a\u0085b\vc\td"},
		{`a\nb`, `a\nb`},
	}
	for _, c := range cases {
		if got := SanitizeLogValue(c.in); got != c.want {
			t.Errorf("SanitizeLogValue(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRedactDetails(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"empty stays empty", "", ""},
		{"plain text", "User logged in", "User logged in"},
		// The name runs to the next white space, so the semicolon is taken
		// for its last character.
		{"punctuation after the name", "Username: alice; ip=203.0.113.7", "Username: a***; ip=203.0.113.7"},
		{"user label", "User: bob, IP: 198.51.100.4", "User: b***, IP: 198.51.100.4"},
		{"label in any letter case", "USERNAME: carol", "USERNAME: c***l"},
		{"label inside a word", "Users: bob XUser: bob", "Users: bob XUser: bob"},
		{"no colon", "Updated user: erin and Created user dave", "Updated user: e***n and Created user dave"},
		{"no space after the colon", "User:bob", "User: b***b"},
		{"every key", "password=a secret:b token = c api_key=d api-key=e key=f", "password=*** secret:*** token = *** api_key=*** api-key=*** key=***"},
		{"key in any letter case", "PASSWORD=a Token: b", "PASSWORD=*** Token: ***"},
		{"key at the end of a word", "monkey=1 apikey=2 my_key=3", "monkey=1 apikey=2 my_key=3"},
		{"key after a hyphen or a dot", "x-api-key: abc my.token=abc", "x-api-key: *** my.token=***"},
		{"value ends at white space", "token=abc def", "token=*** def"},
		{"line breaks", "a\r\nb\u2028c", `a\n\nb\nc`},
		{"a line break ends no value", "token=abc\ntoken=def", `token=***`},
		{"no-break space is white space", "User:\u00a0bob\u00a0x", "User: b***b\u00a0x"},
		{"U+0085 is not", "token=a\u0085b c", "token=*** c"},
		{"only ASCII letters fold", "\u017fecret=x to\u212aen=y", "\u017fecret=x to\u212aen=y"},
		{"label then key", "User: token=abc", "User: t***c"},
		{"key then label", "token: User: bob", "token: *** b***b"},
		{"emoji name", "Username: \U0001F600xy", "Username: \uFFFD***y"},
		{"bytes that are no UTF-8", "token=a\xffb User: \xffbc", "token=*** User: \uFFFD***c"},
	}
	for _, c := range cases {
		if got := RedactDetails(c.in); got != c.want {
			t.Errorf("%s: RedactDetails(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestSensitiveDetailKey(t *testing.T) {
	for _, name := range []string{"password", "user_password", "SECRET_KEY", "accessToken", "api_key", "api-key", "ApiKey", "key", "KEY"} {
		if !SensitiveDetailKey(name) {
			t.Errorf("SensitiveDetailKey(%q) = false", name)
		}
	}
	for _, name := range []string{"", "username", "keys", "monkey", "api key", "key ", "pass", "to\u212aen"} {
		if SensitiveDetailKey(name) {
			t.Errorf("SensitiveDetailKey(%q) = true", name)
		}
	}
}

func TestRedactURL(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"empty", "", ""},
		{"white space only", " \t\u00a0", ""},
		{"no credentials", "http://localhost:21114/api", "http://localhost:21114/api"},
		{"user only", "http://user@example.com", "http://example.com/"},
		{"password with @", "http://user:p@ss@example.com/x", "http://example.com/x"},
		{"white space around", "  https://u:p@example.com/x\n", "https://example.com/x"},
		{"host and scheme in lower case", "HTTPS://U:P@Example.COM/Path", "https://example.com/Path"},
		{"default port dropped", "https://u:p@example.com:443/", "https://example.com/"},
		{"other port kept", "https://u:p@example.com:8443", "https://example.com:8443/"},
		{"port without its zeros", "http://u:p@h:008080/", "http://h:8080/"},
		{"dot segments", "http://u:p@h/a/./b/../c", "http://h/a/c"},
		{"encoded dot segments", "http://u:p@h/a/%2E%2e/c", "http://h/c"},
		{"dot segments Node overlooks", "http://u:p@h/a/.b/../c", "http://h/a/.b/../c"},
		{"path, query and fragment encoded", "http://u:p@h/a b?c d'#e f`", "http://h/a%20b?c%20d%27#e%20f%60"},
		{"backslashes", `http:\\u:p@h\a\b`, "http://h/a/b"},
		{"IPv4 in other forms", "http://u:p@0x7f.1/", "http://127.0.0.1/"},
		{"IPv6", "http://u:p@[2001:DB8:0:0:0:0:0:1]:80/", "http://[2001:db8::1]/"},
		{"IPv6 with IPv4 end", "http://u:p@[::ffff:192.168.1.1]/", "http://[::ffff:c0a8:101]/"},
		{"other scheme", "postgres://betterdesk:pw@db:5432/betterdesk?sslmode=disable", "postgres://db:5432/betterdesk?sslmode=disable"},
		{"other scheme keeps the host as written", "foo://U:P@HOST/a/../b", "foo://HOST/b"},
		{"other scheme, nothing left of the path", "foo://u:p@h/..", "foo://h"},
		{"no host", "mailto:user@example.com", "mailto:user@example.com"},
		{"a host name with a port reads as a scheme", "localhost:21114/api", "localhost:21114/api"},
		{"file", `file:c:\x\..\y`, "file:///c:/y"},
		{"file on localhost", "file://localhost/etc/passwd", "file:///etc/passwd"},
		// What the parser refuses comes back with the first user part
		// masked.
		{"no scheme", "//user:pw@host/x", "//***@host/x"},
		{"space in the host", "http://user:pw@exa mple.com/x", "http://***@exa mple.com/x"},
		{"refused, password with @", "http://user:p@ss@exa mple.com/", "http://***@ss@exa mple.com/"},
		{"refused, two user parts", "x //a@b //c@d", "x //***@b //c@d"},
		{"port too large", "http://u:p@h:65536/", "http://***@h:65536/"},
		{"no host after the user", "http://u:p@/x", "http://***@/x"},
		{"refused, nothing to mask", "not a url", "not a url"},
	}
	for _, c := range cases {
		if got := RedactURL(c.in); got != c.want {
			t.Errorf("%s: RedactURL(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestRedactURLInternationalHost pins the one kind of URL that comes out
// different from Node: "node" is what Node answers.
func TestRedactURLInternationalHost(t *testing.T) {
	cases := []struct{ in, want, node string }{
		{"http://u:p@b\u00fccher.de/x", "http://b\u00fccher.de/x", "http://xn--bcher-kva.de/x"},
		{"HTTP://U:P@B\u00dcCHER.DE:80", "http://b\u00dccher.de/", "http://xn--bcher-kva.de/"},
		{"http://u:p@%C3%BC.de/", "http://\u00fc.de/", "http://xn--tda.de/"},
		{"http://u:p@XN--bcher-kva.de:80/a/../b", "http://xn--bcher-kva.de/b", "http://xn--bcher-kva.de/b"},
		{"http://u:p@xn--a.de/", "http://xn--a.de/", "http://***@xn--a.de/"},
		{"http://u:p@b\u00fc cher.de/", "http://***@b\u00fc cher.de/", "http://***@b\u00fc cher.de/"},
	}
	for _, c := range cases {
		got, international := redactURL(c.in)
		if got != c.want || !international {
			t.Errorf("redactURL(%q) = %q, %v; want %q, true (Node: %q)", c.in, got, international, c.want, c.node)
		}
	}
	if _, international := redactURL("http://u:p@example.com/b\u00fccher?xn--"); international {
		t.Error("letters outside ASCII in the path count as an international host")
	}
}
