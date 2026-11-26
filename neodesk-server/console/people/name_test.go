package people

import "testing"

func TestNameIsFirstAndLastElseTheUsername(t *testing.T) {
	for _, c := range []struct{ first, last, username, want string }{
		{"Ada", "Lovelace", "ada", "Ada Lovelace"},
		{" Ada ", "", "ada", "Ada"},
		{"", "Lovelace", "ada", "Lovelace"},
		{"  ", " ", "ada", "ada"},
		// LDAP and single sign-on write the whole name into first_name
		{"Grace Brewster Hopper", "", "ghopper", "Grace Brewster Hopper"},
	} {
		if got := Name(c.first, c.last, c.username); got != c.want {
			t.Errorf("Name(%q, %q, %q) = %q, want %q", c.first, c.last, c.username, got, c.want)
		}
	}
	if got := FullName("  ", ""); got != "" {
		t.Errorf("FullName of nothing = %q", got)
	}
}
