package authn

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/store"
)

// Two-factor sign-in with time-based codes: HMAC-SHA1, 6 digits, a step
// of 30 seconds, and no tolerance. Only the code of the current step is
// accepted; the code of the step before is refused.
//
// The secret is base32 text. It is read leniently: lower case counts as
// upper case, reading stops at "=", and a character below "0" (a space, a
// dash) counts as five zero bits. A secret of fewer than 10 bytes is
// repeated to fill the 20 bytes of the key, so its codes are not the ones
// other apps compute for the same short secret. Secrets made here have 20
// bytes.

const (
	totpIssuer     = "BetterDesk"
	totpStepMillis = 30_000
	totpDigits     = 6
	totpKeyLength  = 20
	// recoveryCodeCount is the number of recovery codes made when
	// two-factor sign-in is turned on.
	recoveryCodeCount = 8
)

var errNotBase32 = errors.New("Invalid input - it is not base32 encoded string")

// base32Table maps the characters "0" to DEL to their five bits. 0xff
// stands for characters outside the alphabet; their eight bits are used
// as they are.
var base32Table = [80]byte{
	0xff, 0xff, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f,
	0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff,
	0xff, 0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06,
	0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e,
	0x0f, 0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16,
	0x17, 0x18, 0x19, 0xff, 0xff, 0xff, 0xff, 0xff,
	0xff, 0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06,
	0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e,
	0x0f, 0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16,
	0x17, 0x18, 0x19, 0xff, 0xff, 0xff, 0xff, 0xff,
}

// decodeSecret reads a stored secret. It fails only for a byte above DEL.
func decodeSecret(secret string) ([]byte, error) {
	out := make([]byte, 0, len(secret)*5/8+1)
	var shift uint
	var char int
	for i := 0; i < len(secret); i++ {
		c := secret[i]
		if c == '=' {
			break
		}
		if c >= 0x80 {
			return nil, errNotBase32
		}
		digit := 0
		if c >= '0' {
			digit = int(base32Table[c-'0'])
		}
		if shift <= 3 {
			shift = (shift + 5) % 8
			if shift == 0 {
				char |= digit
				out = append(out, byte(char))
				char = 0
			} else {
				char |= 0xff & (digit << (8 - shift))
			}
		} else {
			shift = (shift + 5) % 8
			char |= 0xff & (digit >> shift)
			out = append(out, byte(char))
			char = 0xff & (digit << (8 - shift))
		}
	}
	return out, nil
}

// totpKey is the HMAC key for a decoded secret: the secret itself, or a
// short one repeated.
func totpKey(secret []byte) []byte {
	hexLength := 2 * len(secret)
	if hexLength >= totpKeyLength {
		return secret
	}
	key := make([]byte, 0, totpKeyLength)
	for i := 0; i < totpKeyLength-hexLength && len(key) < totpKeyLength; i++ {
		key = append(key, secret...)
	}
	if len(key) > totpKeyLength {
		key = key[:totpKeyLength]
	}
	return key
}

// totpCode computes the code of secret for the step that contains at.
func totpCode(secret string, at time.Time) (string, error) {
	decoded, err := decodeSecret(secret)
	if err != nil {
		return "", err
	}
	counter := math.Max(0, math.Floor(float64(at.UnixMilli())/30/1000))
	var message [8]byte
	binary.BigEndian.PutUint64(message[:], uint64(counter))
	mac := hmac.New(sha1.New, totpKey(decoded))
	mac.Write(message[:])
	digest := mac.Sum(nil)
	offset := digest[len(digest)-1] & 0x0f
	binaryCode := binary.BigEndian.Uint32(digest[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, binaryCode%1_000_000), nil
}

// totpVerify reports whether token is the code of the current step. The
// token must be exactly the six digits: no spaces, no other digits.
func totpVerify(token, secret string, at time.Time) (bool, error) {
	code, err := totpCode(secret, at)
	if err != nil {
		return false, err
	}
	return hmac.Equal([]byte(token), []byte(code)), nil
}

// formEncode writes a value the way a URL query built from form fields
// has it: letters, digits and * - . _ stay, a space becomes "+", every
// other byte becomes %XX.
func formEncode(s string) string {
	s = toWellFormed(s)
	const upperhex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '*', c == '-', c == '.', c == '_':
			b.WriteByte(c)
		case c == ' ':
			b.WriteByte('+')
		default:
			b.WriteByte('%')
			b.WriteByte(upperhex[c>>4])
			b.WriteByte(upperhex[c&15])
		}
	}
	return b.String()
}

// OtpauthURL builds the address an authenticator app reads from the QR
// code:
//
//	otpauth://totp/BetterDesk:<account>?secret=<secret>&issuer=BetterDesk&digits=6&period=30
//
// The issuer has no space and there is no algorithm parameter, because
// some apps refuse those.
func OtpauthURL(account, secret string) string {
	return otpauthURL(account, secret, totpIssuer)
}

func otpauthURL(account, secret, issuer string) string {
	return "otpauth://totp/" + goapi.EncodeURIComponent(toWellFormed(issuer)) + ":" + goapi.EncodeURIComponent(toWellFormed(account)) +
		"?secret=" + formEncode(secret) + "&issuer=" + formEncode(issuer) + "&digits=6&period=30"
}

// generateSecret makes a secret of 20 random bytes, 160 bits, as 32
// base32 characters.
func (s *Service) generateSecret() (string, error) {
	raw := make([]byte, totpKeyLength)
	if _, err := io.ReadFull(s.rand, raw); err != nil {
		return "", fmt.Errorf("two-factor secret: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw), nil
}

// TOTPSetup is what the set-up of two-factor sign-in shows the user.
type TOTPSetup struct {
	// QRCode is OtpauthURL as a PNG data URL.
	QRCode string `json:"qrCode"`
	// Secret is the new secret, for typing it into an app.
	Secret     string `json:"secret"`
	OtpauthURL string `json:"otpauthUrl"`
}

// GenerateTOTPSetup makes a new secret for the account, stores it at once
// (two-factor sign-in stays off until VerifyAndEnableTOTP) and returns it
// with its QR code. A secret from an unfinished set-up is replaced. It
// returns ErrUserNotFound for an unknown account.
func (s *Service) GenerateTOTPSetup(ctx context.Context, userID int64) (*TOTPSetup, error) {
	user, err := getUserByID(ctx, s.db, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, ErrUserNotFound
	}
	secret, err := s.generateSecret()
	if err != nil {
		return nil, err
	}
	if err := saveTOTPSecret(ctx, s.db, userID, secret); err != nil {
		return nil, err
	}
	address := OtpauthURL(user.Username, secret)
	if s.qr == nil {
		return nil, errors.New("authn: Options.QR is not set")
	}
	image, err := s.qr(address)
	if err != nil {
		return nil, fmt.Errorf("draw the two-factor QR code: %w", err)
	}
	return &TOTPSetup{QRCode: image, Secret: secret, OtpauthURL: address}, nil
}

// VerifyAndEnableTOTP finishes the set-up: when token is the current code
// of the stored secret it turns two-factor sign-in on and returns the 8
// new recovery codes. It returns ErrTOTPNotSetUp when the account has no
// secret and ErrInvalidTOTPCode for a wrong code.
func (s *Service) VerifyAndEnableTOTP(ctx context.Context, userID int64, token string) ([]string, error) {
	user, err := getUserByID(ctx, s.db, userID)
	if err != nil {
		return nil, err
	}
	if user == nil || user.TOTPSecret.String == "" {
		return nil, ErrTOTPNotSetUp
	}
	valid, err := totpVerify(token, user.TOTPSecret.String, s.now())
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, ErrInvalidTOTPCode
	}
	codes, err := s.generateRecoveryCodes(recoveryCodeCount)
	if err != nil {
		return nil, err
	}
	if err := enableTOTP(ctx, s.db, userID, stringifyCodes(codes)); err != nil {
		return nil, err
	}
	return codes, nil
}

// VerifyTOTPCode checks the second factor of a sign-in. It is false for an
// account without two-factor sign-in.
func (s *Service) VerifyTOTPCode(ctx context.Context, userID int64, token string) (bool, error) {
	user, err := getUserByID(ctx, s.db, userID)
	if err != nil {
		return false, err
	}
	if user == nil || !user.TOTPEnabled.True() || user.TOTPSecret.String == "" {
		return false, nil
	}
	return totpVerify(token, user.TOTPSecret.String, s.now())
}

// VerifyRecoveryCode checks a recovery code in place of the second factor
// and uses it up. White space around the code and the case of its letters
// do not matter; the dash does. It is false for an account without
// two-factor sign-in.
func (s *Service) VerifyRecoveryCode(ctx context.Context, userID int64, code string) (bool, error) {
	user, err := getUserByID(ctx, s.db, userID)
	if err != nil {
		return false, err
	}
	if user == nil || !user.TOTPEnabled.True() || user.TOTPRecoveryCodes.String == "" {
		return false, nil
	}
	left, matched, err := takeRecoveryCode(user.TOTPRecoveryCodes.String, code)
	if err != nil || !matched {
		return false, err
	}
	if err := useRecoveryCode(ctx, s.db, userID, left); err != nil {
		return false, err
	}
	return true, nil
}

// takeRecoveryCode looks for code in the stored JSON list of codes. When
// it is there, matched is true and left is the list without it.
//
// Stored text that is not a JSON list of texts holds no codes: text that
// is not JSON, JSON that is not a list, and a list with an entry that is
// not text (null included) match nothing.
func takeRecoveryCode(stored, code string) (left string, matched bool, err error) {
	var entries []any
	if err := json.Unmarshal([]byte(stored), &entries); err != nil || entries == nil {
		return "", false, nil
	}
	list := make([]string, len(entries))
	for i, entry := range entries {
		text, isText := entry.(string)
		if !isText {
			return "", false, nil
		}
		list[i] = text
	}
	upper := cases.Upper(language.Und)
	wanted := upper.String(jsTrim(toWellFormed(code)))
	for i, text := range list {
		if upper.String(text) == wanted {
			left, err := withoutElement(stored, i)
			return left, err == nil, err
		}
	}
	return "", false, nil
}

// withoutElement returns the JSON array in text without the element at
// index, written compactly as JSON.stringify writes it.
func withoutElement(text string, index int) (string, error) {
	var elements []json.RawMessage
	if err := json.Unmarshal([]byte(text), &elements); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteByte('[')
	for i, element := range elements {
		if i == index {
			continue
		}
		if b.Len() > 1 {
			b.WriteByte(',')
		}
		b.Write(element)
	}
	b.WriteByte(']')
	var normal store.JSONParsed
	if err := normal.Scan(b.String()); err != nil {
		return "", err
	}
	out, err := normal.MarshalJSON()
	return string(out), err
}

// DisableTOTP turns two-factor sign-in off and deletes the secret and the
// recovery codes.
func (s *Service) DisableTOTP(ctx context.Context, userID int64) error {
	return disableTOTP(ctx, s.db, userID)
}

// IsTOTPEnabled reports whether the account has two-factor sign-in on. An
// unknown account has not.
func (s *Service) IsTOTPEnabled(ctx context.Context, userID int64) (bool, error) {
	user, err := getUserByID(ctx, s.db, userID)
	if err != nil {
		return false, err
	}
	return user != nil && user.TOTPEnabled.True(), nil
}

// generateRecoveryCodes makes count codes of the form "3FA9-0C7E": four
// random bytes as upper-case hex with a dash in the middle.
func (s *Service) generateRecoveryCodes(count int) ([]string, error) {
	codes := make([]string, 0, count)
	for i := 0; i < count; i++ {
		raw, err := s.randomHex(4)
		if err != nil {
			return nil, fmt.Errorf("recovery code: %w", err)
		}
		raw = strings.ToUpper(raw)
		codes = append(codes, raw[:4]+"-"+raw[4:])
	}
	return codes, nil
}

// stringifyCodes writes the codes as the JSON array the column holds.
func stringifyCodes(codes []string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, code := range codes {
		if i > 0 {
			b.WriteByte(',')
		}
		quoted, _ := store.NullString{String: code, Valid: true}.MarshalJSON()
		b.Write(quoted)
	}
	b.WriteByte(']')
	return b.String()
}
