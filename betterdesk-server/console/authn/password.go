package authn

import (
	"context"
	"crypto/pbkdf2"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"strings"
)

const (
	// saltRounds is the bcrypt cost of every hash the console makes.
	saltRounds = 12

	// dummyHash is compared with the password of an unknown user, so that
	// an unknown name takes as long as a wrong password.
	dummyHash = "$2b$12$KiXeOj5vHpJRJHGMhWzadeKfRJLvJRaRHQbMGBBdkpu.jQfXAzgWS"

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

// HashPassword makes the hash the console stores for a password: bcrypt
// with cost 12, "$2b$12$" and 53 more letters. bcrypt reads at most the
// first 72 bytes of a password.
func (s *Service) HashPassword(ctx context.Context, password string) (string, error) {
	return bcryptHash(ctx, s.rand, toWellFormed(password), s.bcryptCost)
}

// PasswordCheck is the outcome of checking a password against a stored
// hash.
type PasswordCheck struct {
	// Valid says the password is the right one.
	Valid bool
	// NeedsMigration says the stored hash is one of the signal server's
	// PBKDF2 forms. Sign-in replaces such a hash with a bcrypt hash once
	// the password was right.
	NeedsMigration bool
}

// VerifyPasswordEx checks password against a stored hash in any of the
// formats an account can have: bcrypt ("$2a$", "$2b$" and the old "$2$")
// and the signal server's two PBKDF2 forms. Anything else is a hash no
// password fits, also "$2y$".
func (s *Service) VerifyPasswordEx(ctx context.Context, password, hash string) (PasswordCheck, error) {
	password = toWellFormed(password)
	if isPBKDF2Hash(hash) {
		valid, err := verifyPBKDF2(password, hash)
		return PasswordCheck{Valid: valid, NeedsMigration: true}, err
	}
	valid, err := bcryptCompare(ctx, password, hash)
	return PasswordCheck{Valid: valid}, err
}

// VerifyPassword reports whether password fits the stored hash.
func (s *Service) VerifyPassword(ctx context.Context, password, hash string) (bool, error) {
	check, err := s.VerifyPasswordEx(ctx, password, hash)
	return check.Valid, err
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
}

// ValidatePasswordStrength rates a password. Letters and digits are the
// ASCII ones; every other character counts as a special character. The
// length is counted in UTF-16 units.
func ValidatePasswordStrength(password string) PasswordStrength {
	password = toWellFormed(password)
	result := PasswordStrength{Feedback: []string{}}
	length := utf16Len(password)
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
	ErrTOTPNotSetUp     Refusal = "TOTP not set up"
	ErrInvalidTOTPCode  Refusal = "Invalid verification code"
)

// minNewPasswordLength is the shortest password ChangePassword takes, in
// UTF-16 units.
const minNewPasswordLength = 8

// ChangePassword replaces the password of an account after checking the
// current one. The new password must have 8 UTF-16 units or more; nothing
// else about it is checked. It returns ErrUserNotFound, ErrWrongPassword
// or ErrPasswordTooShort when it refuses.
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
	if utf16Len(toWellFormed(newPassword)) < minNewPasswordLength {
		return ErrPasswordTooShort
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
