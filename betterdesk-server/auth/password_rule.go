package auth

import (
	"errors"
	"unicode/utf8"
)

// The one rule for a new password, on every path that sets one: the
// console's own change, the Users page, the API's account and
// organization account routes, the reset-password command and the first
// admin's password from the environment. The length is counted in UTF-16
// units, as the browser counts it; nothing else about the password is
// checked.
const (
	MinPasswordLength = 8
	MaxPasswordLength = 128
)

// The refusals of CheckNewPassword.
var (
	ErrPasswordTooShort = errors.New("Password must be at least 8 characters")
	ErrPasswordTooLong  = errors.New("Password must be at most 128 characters")
)

// PasswordLength is the length of password in UTF-16 units. A byte that
// is not UTF-8 counts as one unit, as the replacement character it is read
// as.
func PasswordLength(password string) int {
	n := 0
	for _, r := range password {
		if r >= 0x10000 && r <= utf8.MaxRune {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// CheckNewPassword returns ErrPasswordTooShort or ErrPasswordTooLong when
// password breaks the rule, nil otherwise.
func CheckNewPassword(password string) error {
	switch n := PasswordLength(password); {
	case n < MinPasswordLength:
		return ErrPasswordTooShort
	case n > MaxPasswordLength:
		return ErrPasswordTooLong
	}
	return nil
}
