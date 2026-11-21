package authn

import (
	"context"
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"strings"

	"github.com/unitronix/betterdesk-server/auth"
)

const (
	// dummyHash is compared with the password of an unknown user, so that
	// an unknown name takes as long as a wrong password: a hash of the
	// form and cost HashPassword makes, which no password fits.
	dummyHash = "pbkdf2-sha256$600000$9f3c2a1e7b5d4c6f8a0e1d2c3b4a5968$" +
		"4d1e7a9c3b5f2e8d6a0c4b7e1f9d3a5c8e2b6f0a4d7c1e9b3f5a8d2c6e0b4f7a"

	pbkdf2LegacyIterations = 100_000
	pbkdf2KeyLength        = 32
	pbkdf2ModernPrefix     = "pbkdf2-sha256$"
	pbkdf2MaxIterations    = 10_000_000
)

// errNoHash is what a compare with a missing stored hash fails with.
var errNoHash = errors.New("data and hash arguments required")

// isPBKDF2Hash reports whether a stored hash is one of the signal server's
// two PBKDF2 forms: "pbkdf2-sha256$<iterations>$<hex salt>$<hex hash>", or
// 32 hex digits, a colon and 64 hex digits. Hashes that start with "$2a$"
// or "$2b$" never are.
func isPBKDF2Hash(hash string) bool {
	if hash == "" || strings.HasPrefix(hash, "$2b$") || strings.HasPrefix(hash, "$2a$") {
		return false
	}
	if strings.HasPrefix(hash, pbkdf2ModernPrefix) {
		return true
	}
	parts := strings.Split(hash, ":")
	return len(parts) == 2 && isHexRun(parts[0], 32) && isHexRun(parts[1], 64)
}

// verifyPBKDF2 checks password against a PBKDF2-HMAC-SHA256 hash in either
// form. Salt and hash are read as hex up to the first letter that is not
// hex.
func verifyPBKDF2(password, stored string) (bool, error) {
	if strings.HasPrefix(stored, pbkdf2ModernPrefix) {
		parts := strings.Split(stored, "$")
		if len(parts) != 4 {
			return false, nil
		}
		iterations, ok := parseInt(parts[1])
		if !ok || iterations <= 0 || iterations > pbkdf2MaxIterations {
			return false, nil
		}
		salt, expected := lenientHex(parts[2]), lenientHex(parts[3])
		if len(salt) == 0 || len(expected) == 0 {
			return false, nil
		}
		return pbkdf2Equal(password, salt, int(iterations), expected)
	}
	parts := strings.Split(stored, ":")
	if len(parts) != 2 {
		return false, nil
	}
	salt, expected := lenientHex(parts[0]), lenientHex(parts[1])
	if len(expected) != pbkdf2KeyLength {
		return false, nil
	}
	return pbkdf2Equal(password, salt, pbkdf2LegacyIterations, expected)
}

func pbkdf2Equal(password string, salt []byte, iterations int, expected []byte) (bool, error) {
	derived, err := pbkdf2.Key(sha256.New, password, salt, iterations, len(expected))
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(expected, derived) == 1, nil
}

// HashPassword makes the hash the console stores for a password, with the
// API server's code (auth.HashPasswordFrom): PBKDF2-HMAC-SHA256,
// "pbkdf2-sha256$600000$" with a hex salt and hash. The API server stores
// the same form, so a sign-in on either side leaves the hash as it is.
func (s *Service) HashPassword(_ context.Context, password string) (string, error) {
	return auth.HashPasswordFrom(s.rand, s.hashIterations, toWellFormed(password))
}

// CheckPassword is VerifyPassword without a Service.
func CheckPassword(ctx context.Context, password, hash string) (bool, error) {
	return (&Service{}).VerifyPassword(ctx, password, hash)
}

// VerifyPassword reports whether password fits the stored hash in any of
// the formats an account can have: the API server's two PBKDF2 forms and
// bcrypt ("$2a$", "$2b$" and the old "$2$"), which older consoles wrote.
// Anything else is a hash no password fits, also "$2y$". A hash that fits
// stays as it is.
func (s *Service) VerifyPassword(ctx context.Context, password, hash string) (bool, error) {
	password = toWellFormed(password)
	if isPBKDF2Hash(hash) {
		return verifyPBKDF2(password, hash)
	}
	return bcryptCompare(ctx, password, hash)
}

// PasswordStrength is the rating of a new password.
type PasswordStrength struct {
	// Score is 0 to 6: one point each for 8 characters, 12 characters, a
	// lower-case letter, an upper-case letter, a digit and any other
	// character.
	Score int `json:"score"`
	// Feedback names what is missing, in English.
	Feedback []string `json:"feedback"`
	// Strength is "weak" up to 2 points, "medium" up to 4, else "strong".
	Strength string `json:"strength"`
	// length is the password's length in UTF-16 units.
	length int
}

// TooShort reports whether the password is shorter than MinPasswordLength,
// which no new password may be, whatever its strength.
func (p PasswordStrength) TooShort() bool { return p.length < MinPasswordLength }

// TooLong reports whether the password is longer than MaxPasswordLength,
// which the sign-in refuses.
func (p PasswordStrength) TooLong() bool { return p.length > MaxPasswordLength }

// ValidatePasswordStrength rates a password. Letters and digits are the
// ASCII ones; every other character counts as a special character. The
// length is counted in UTF-16 units.
func ValidatePasswordStrength(password string) PasswordStrength {
	password = toWellFormed(password)
	length := utf16Len(password)
	result := PasswordStrength{Feedback: []string{}, length: length}
	var lower, upper, digit, other bool
	for i := 0; i < len(password); i++ {
		switch c := password[i]; {
		case c >= 'a' && c <= 'z':
			lower = true
		case c >= 'A' && c <= 'Z':
			upper = true
		case c >= '0' && c <= '9':
			digit = true
		default:
			other = true
		}
	}
	if length >= 8 {
		result.Score++
	} else {
		result.Feedback = append(result.Feedback, "Use at least 8 characters")
	}
	if length >= 12 {
		result.Score++
	}
	for _, rule := range []struct {
		met  bool
		hint string
	}{
		{lower, "Add lowercase letters"},
		{upper, "Add uppercase letters"},
		{digit, "Add numbers"},
		{other, "Add special characters"},
	} {
		if rule.met {
			result.Score++
		} else {
			result.Feedback = append(result.Feedback, rule.hint)
		}
	}
	switch {
	case result.Score <= 2:
		result.Strength = "weak"
	case result.Score <= 4:
		result.Strength = "medium"
	default:
		result.Strength = "strong"
	}
	return result
}

// Refusal is an answer of the service that is not a fault of the server:
// its text is what the route sends as "error".
type Refusal string

func (r Refusal) Error() string { return string(r) }

// The refusals of the password and two-factor functions.
const (
	ErrUserNotFound     Refusal = "User not found"
	ErrWrongPassword    Refusal = "Current password is incorrect"
	ErrPasswordTooShort Refusal = "Password must be at least 8 characters"
	ErrPasswordTooLong  Refusal = "Password must be at most 128 characters"
	ErrTOTPNotSetUp     Refusal = "TOTP not set up"
	ErrInvalidTOTPCode  Refusal = "Invalid verification code"
)

// MinPasswordLength is the shortest new password, in UTF-16 units, as the
// web app's forms ask for it.
const MinPasswordLength = 8

// MaxPasswordLength is the longest password, in UTF-16 units, that the
// sign-in takes.
const MaxPasswordLength = 128

// ChangePassword replaces the password of an account after checking the
// current one. The new password must have 8 to 128 UTF-16 units; nothing
// else about it is checked. It returns ErrUserNotFound, ErrWrongPassword,
// ErrPasswordTooShort or ErrPasswordTooLong when it refuses.
func (s *Service) ChangePassword(ctx context.Context, userID int64, currentPassword, newPassword string) error {
	user, err := getUserByID(ctx, s.db, userID)
	if err != nil {
		return err
	}
	if user == nil {
		return ErrUserNotFound
	}
	valid, err := s.verifyStored(ctx, currentPassword, user.PasswordHash)
	if err != nil {
		return err
	}
	if !valid {
		return ErrWrongPassword
	}
	switch length := utf16Len(toWellFormed(newPassword)); {
	case length < MinPasswordLength:
		return ErrPasswordTooShort
	case length > MaxPasswordLength:
		return ErrPasswordTooLong
	}
	hash, err := s.HashPassword(ctx, newPassword)
	if err != nil {
		return err
	}
	return updateUserPassword(ctx, s.db, user.ID.V, hash)
}

// verifyStored checks a password against the hash column of a row. A row
// without a hash cannot be checked at all.
func (s *Service) verifyStored(ctx context.Context, password string, hash nullText) (bool, error) {
	if !hash.Valid {
		return false, errNoHash
	}
	return s.VerifyPassword(ctx, password, hash.String)
}
