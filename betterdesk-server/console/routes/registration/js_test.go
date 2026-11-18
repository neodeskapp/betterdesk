package registration

import "testing"

// replaceFirst is String.prototype.replace with a text pattern: the
// first match only, and the replacement's $ patterns. A Host header may
// carry $, & and ', so the console's address in an approval does.
func TestReplaceFirst(t *testing.T) {
	for _, c := range []struct{ s, replacement, want string }{
		{"http://0.0.0.0:5000", "example.test", "http://example.test:5000"},
		{"0.0.0.0 and 0.0.0.0", "h", "h and 0.0.0.0"},
		{"http://0.0.0.0:5000", "a$&b", "http://a0.0.0.0b:5000"},
		{"http://0.0.0.0:5000", "a$$b", "http://a$b:5000"},
		{"http://0.0.0.0:5000", "a$'b", "http://a:5000b:5000"},
		{"http://0.0.0.0:5000", "a$`b", "http://ahttp://b:5000"},
		{"http://0.0.0.0:5000", "a$1b$", "http://a$1b$:5000"},
		{"no address", "h", "no address"},
	} {
		if got := replaceFirst(c.s, "0.0.0.0", c.replacement); got != c.want {
			t.Errorf("%q with %q: %q, want %q", c.s, c.replacement, got, c.want)
		}
	}
}

// cut counts UTF-16 units and keeps half of a pair as U+FFFD.
func TestCut(t *testing.T) {
	for _, c := range []struct {
		s    string
		n    int
		want string
	}{
		{"abc", 2, "ab"},
		{"abc", 5, "abc"},
		{"é日本", 2, "é日"},
		{"a😀b", 2, "a�"},
		{"a😀b", 3, "a😀"},
		{"", 3, ""},
	} {
		if got := cut(c.s, c.n); got != c.want {
			t.Errorf("cut(%q, %d) = %q, want %q", c.s, c.n, got, c.want)
		}
	}
}
