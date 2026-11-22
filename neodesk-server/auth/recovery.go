package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode"

	"golang.org/x/crypto/bcrypt"
)

const (
	// RecoveryCodeCount is the number of recovery codes made when two-factor
	// sign-in is turned on.
	RecoveryCodeCount = 8

	// RecoveryCodeCost is the bcrypt cost of a stored recovery code. The
	// codes are random, so a moderate cost keeps a check of all of them
	// short.
	RecoveryCodeCost = 10
)

// ErrMalformedRecoveryCodes is what ConsumeRecoveryCode returns for stored
// text that is not a JSON list of texts.
var ErrMalformedRecoveryCodes = errors.New("auth: malformed recovery code store")

// GenerateRecoveryCodes returns RecoveryCodeCount fresh recovery codes of the
// form "3FA9-0C7E": four random bytes as upper-case hex with a dash in the
// middle, nine characters as the sign-in page's field takes them. The codes
// are shown once; only the hashes of HashRecoveryCodes are stored.
func GenerateRecoveryCodes() ([]string, error) {
	return GenerateRecoveryCodesFrom(rand.Reader)
}

// GenerateRecoveryCodesFrom is GenerateRecoveryCodes with the bytes read from
// random.
func GenerateRecoveryCodesFrom(random io.Reader) ([]string, error) {
	codes := make([]string, RecoveryCodeCount)
	raw := make([]byte, 4)
	for i := range codes {
		if _, err := io.ReadFull(random, raw); err != nil {
			return nil, err
		}
		text := strings.ToUpper(hex.EncodeToString(raw))
		codes[i] = text[:4] + "-" + text[4:]
	}
	return codes, nil
}

// HashRecoveryCodes returns the JSON array of bcrypt hashes of the codes (at
// RecoveryCodeCost) that the users.totp_recovery_codes column holds.
func HashRecoveryCodes(plain []string) (string, error) {
	return HashRecoveryCodesCost(plain, RecoveryCodeCost)
}

// HashRecoveryCodesCost is HashRecoveryCodes with another bcrypt cost. Tests
// pass bcrypt.MinCost.
func HashRecoveryCodesCost(plain []string, cost int) (string, error) {
	hashes := make([]string, 0, len(plain))
	for _, code := range plain {
		h, err := bcrypt.GenerateFromPassword([]byte(normalizeRecoveryCode(code)), cost)
		if err != nil {
			return "", err
		}
		hashes = append(hashes, string(h))
	}
	b, err := json.Marshal(hashes)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ConsumeRecoveryCode validates `code` against the stored JSON list and, on a
// match, returns the list without the entry it matched. The caller MUST
// persist the returned string back to the database to make the code
// single-use.
//
// An entry is a bcrypt hash, or a code stored as it was shown (installs
// whose web console stored its codes that way): such a code keeps working
// until it is used or the codes are made anew. White space, dashes and the
// letter case of the code do not count. Stored text that is not a JSON list
// of texts is ErrMalformedRecoveryCodes.
//
// Returns: (newStored string, matched bool, err error).
func ConsumeRecoveryCode(stored, code string) (string, bool, error) {
	stored = strings.TrimSpace(stored)
	if stored == "" {
		return stored, false, nil
	}
	var entries []any
	if err := json.Unmarshal([]byte(stored), &entries); err != nil {
		return stored, false, ErrMalformedRecoveryCodes
	}
	list := make([]string, len(entries))
	for i, entry := range entries {
		text, ok := entry.(string)
		if !ok {
			return stored, false, ErrMalformedRecoveryCodes
		}
		list[i] = text
	}
	normalized := normalizeRecoveryCode(code)
	if normalized == "" {
		return stored, false, nil
	}
	for i, entry := range list {
		if recoveryCodeMatches(entry, normalized) {
			left, err := textList(append(list[:i], list[i+1:]...))
			if err != nil {
				return stored, true, err
			}
			return left, true, nil
		}
	}
	return stored, false, nil
}

// textList writes a list of texts as compact JSON, with "<", ">" and "&"
// as they are.
func textList(list []string) (string, error) {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(list); err != nil {
		return "", err
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}

// recoveryCodeMatches reports whether a stored entry is the normalized code:
// a bcrypt hash of it, or the code itself in any of the forms
// normalizeRecoveryCode makes alike.
func recoveryCodeMatches(entry, normalized string) bool {
	if isBcryptHash(entry) {
		return bcrypt.CompareHashAndPassword([]byte(entry), []byte(normalized)) == nil
	}
	return subtle.ConstantTimeCompare([]byte(normalizeRecoveryCode(entry)), []byte(normalized)) == 1
}

// isBcryptHash reports whether a stored entry is a bcrypt hash, which no
// recovery code is.
func isBcryptHash(entry string) bool {
	return strings.HasPrefix(entry, "$2a$") || strings.HasPrefix(entry, "$2b$") || strings.HasPrefix(entry, "$2y$")
}

// normalizeRecoveryCode strips white space and dashes and uppercases for
// matching.
func normalizeRecoveryCode(code string) string {
	var b strings.Builder
	for _, r := range code {
		if r == '-' || unicode.IsSpace(r) || r == 0xfeff {
			continue
		}
		if r >= 'a' && r <= 'z' {
			r -= 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}
