package registration

import "testing"

// An approved device is told the public ID server setting's host, else
// the host of the approver's request taken apart as a host and a port: an
// IPv6 address keeps its brackets, and $ patterns are text (BUGS RG5).
func TestApprovalAddresses(t *testing.T) {
	for _, c := range []struct{ host, publicID, console, server string }{
		{"panel.example:5000", "", "http://panel.example:5000", "panel.example:21116"},
		{"panel.example", "", "http://panel.example:5000", "panel.example:21116"},
		{"[::1]:5000", "", "http://[::1]:5000", "[::1]:21116"},
		{"[2001:db8::1]", "", "http://[2001:db8::1]:5000", "[2001:db8::1]:21116"},
		{"a$&b:5000", "", "http://a$&b:5000", "a$&b:21116"},
		{"a$'b", "", "http://a$'b:5000", "a$'b:21116"},
		{"", "", "http://localhost:5000", "localhost:21116"},
		{"10.0.0.5:5000", "rd.example.com", "http://rd.example.com:5000", "rd.example.com:21116"},
		{"10.0.0.5:5000", "rd.example.com:31116", "http://rd.example.com:5000", "rd.example.com:31116"},
		{"10.0.0.5:5000", "2001:db8::2", "http://[2001:db8::2]:5000", "[2001:db8::2]:21116"},
	} {
		console, server := approvalAddresses("http", 5000, c.host, c.publicID)
		if console != c.console || server != c.server {
			t.Errorf("Host %q, public ID %q: %q %q, want %q %q", c.host, c.publicID, console, server, c.console, c.server)
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
