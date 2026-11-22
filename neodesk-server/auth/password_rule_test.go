package auth

import (
	"strings"
	"testing"
)

func TestCheckNewPasswordIsEightTo128UTF16Units(t *testing.T) {
	cases := []struct {
		password string
		want     error
	}{
		{"", ErrPasswordTooShort},
		{"sixsix7", ErrPasswordTooShort},
		// An emoji is two UTF-16 units: 1 + 2 + 4 = 7
		{"a\U0001F600bcde", ErrPasswordTooShort},
		{"a\U0001F600bcdef", nil},
		// Any eight characters do: no mix of kinds is asked for
		{"alllower", nil},
		{"12345678", nil},
		{strings.Repeat("x", 128), nil},
		{strings.Repeat("x", 129), ErrPasswordTooLong},
		{strings.Repeat("\U0001F600", 64), nil},
		{strings.Repeat("\U0001F600", 64) + "x", ErrPasswordTooLong},
	}
	for _, c := range cases {
		if got := CheckNewPassword(c.password); got != c.want {
			t.Errorf("CheckNewPassword(%q) = %v, want %v", c.password, got, c.want)
		}
	}
}
