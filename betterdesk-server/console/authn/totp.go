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
// The secret is base32 text (RFC 4648) without padding, read as the API
// server reads the same column: the letters A to Z and the digits 2 to 7.
// Its bytes are the HMAC key as they are, whatever their number. A secret
// with any other character has no codes: every code is wrong for it.
// Secrets made here have 20 bytes.

const (
	totpIssuer     = "BetterDesk"
	totpStepMillis = 30_000
	totpDigits     = 6
	totpKeyLength  = 20
	// recoveryCodeCount is the number of recovery codes made when
	// two-factor sign-in is turned on.
	recoveryCodeCount = 8
)

var errNotBase32 = errors.New("the two-factor secret is not base32 text")

// decodeSecret reads a stored secret.
func decodeSecret(secret string) ([]byte, error) {
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		return nil, errNotBase32
	}
	return key, nil
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
	mac := hmac.New(sha1.New, decoded)
	mac.Write(message[:])
	digest := mac.Sum(nil)
	offset := digest[len(digest)-1] & 0x0f
	binaryCode := binary.BigEndian.Uint32(digest[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, binaryCode%1_000_000), nil
}

// totpVerify reports whether token is the code of the current step. The
// token must be exactly the six digits: no spaces, no other digits. No
// token is the code of a secret that is not base32.
func totpVerify(token, secret string, at time.Time) (bool, error) {
	code, err := totpCode(secret, at)
	if errors.Is(err, errNotBase32) {
		return false, nil
	}
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
