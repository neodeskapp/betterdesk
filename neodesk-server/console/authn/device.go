package authn

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/store"
)

// Checks for requests and sockets that come from devices. A device proves
// itself with a token: a device token issued for it by the API server or
// the console at an enrollment, a pairing, a registration or a join
// (device_tokens, created_by "system"; IssueDeviceToken), one an older
// console issued in access_tokens (token_kind "device"), or on the device
// sockets also an enrollment token of the signal server. An account's sign-in token proves
// no device, whatever client id it stored. Device tokens do not expire by
// time. The header X-Device-Id names a device and proves
// nothing. Neither token proves anything while the signal server holds
// the device as banned or soft-deleted, whoever banned or deleted it.
// Every delete and ban also revokes the device's tokens for good: the
// signal server's own (package db, in the same transaction, whichever
// port or command made it) and the console's (RevokeDeviceTokens), so a
// hard delete, which leaves no row to check, ends them too.

// deviceIDPattern is the form of a device id a token can be bound to.
var deviceIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{3,64}$`)

// IsDeviceID reports whether id has the form of a device id: 3 to 64
// letters, digits, "_" or "-".
func IsDeviceID(id string) bool { return deviceIDPattern.MatchString(id) }

// ExtractBearerToken returns the token of an Authorization header of the
// form "Bearer <token>": the word, white space, and a token without white
// space up to the end. ok is false for anything else. header is the value
// as text, each byte one character (httpx.Latin1).
func ExtractBearerToken(header string) (token string, ok bool) {
	rest, found := strings.CutPrefix(header, "Bearer")
	if !found {
		return "", false
	}
	token = strings.TrimLeftFunc(rest, isJSSpace)
	if len(token) == len(rest) || token == "" || strings.IndexFunc(token, isJSSpace) >= 0 {
		return "", false
	}
	return token, true
}

// DeviceAuthError is a refused device request: the status and the text of
// the answer {"error": text}.
type DeviceAuthError struct {
	Status int
	Text   string
}

func (e *DeviceAuthError) Error() string { return e.Text }

// DeviceAuth is a device request that proved itself.
type DeviceAuth struct {
	// DeviceID is the device the token is bound to.
	DeviceID string
	// Token is the row of a console device token; nil for a device token
	// in device_tokens.
	Token *AccessToken
}

// RequireDeviceToken checks the Authorization header of a device request.
// It needs a Bearer device token (the console's or one in device_tokens)
// that is live and bound to a device id the signal server does not hold as
// banned or soft-deleted, and notes the use on a console token. It refuses
// with a *DeviceAuthError:
//
//	401 "Bearer access token required"     no Bearer token
//	401 "Invalid or expired access token"  a lookup failed
//	401 "Invalid or unbound access token"  no such token, an account's
//	                                       token, not bound to a device id,
//	                                       or its device banned or deleted
func (s *Service) RequireDeviceToken(ctx context.Context, authorization string) (*DeviceAuth, error) {
	token, ok := ExtractBearerToken(authorization)
	if !ok {
		return nil, &DeviceAuthError{Status: 401, Text: "Bearer access token required"}
	}
	row, err := getAccessToken(ctx, s.db, token)
	if err != nil {
		return nil, &DeviceAuthError{Status: 401, Text: "Invalid or expired access token"}
	}
	deviceID := ""
	switch {
	case row.IsDeviceToken():
		deviceID = row.ClientID.String
	case row == nil:
		if deviceID, err = s.ServerDeviceToken(ctx, token); err != nil {
			return nil, &DeviceAuthError{Status: 401, Text: "Invalid or expired access token"}
		}
	}
	if !IsDeviceID(deviceID) {
		return nil, &DeviceAuthError{Status: 401, Text: "Invalid or unbound access token"}
	}
	shutOut, err := deviceShutOut(ctx, s.db, deviceID)
	if err != nil {
		return nil, &DeviceAuthError{Status: 401, Text: "Invalid or expired access token"}
	}
	if shutOut {
		return nil, &DeviceAuthError{Status: 401, Text: "Invalid or unbound access token"}
	}
	if row != nil {
		// The token is good; a failure to note its use changes nothing.
		_ = touchAccessToken(ctx, s.db, token)
	}
	return &DeviceAuth{DeviceID: deviceID, Token: row}, nil
}

// ServerDeviceToken returns the device a device token in device_tokens is
// bound to: a token issued for a device (created_by "system"), active,
// with uses left, not expired and bound to a device id. It is "" for any
// other token, an operator's enrollment token among them. It does not look
// at the device's ban.
func (s *Service) ServerDeviceToken(ctx context.Context, token string) (string, error) {
	if token == "" {
		return "", nil
	}
	row, err := lookupServerDeviceToken(ctx, s.db, HashDeviceToken(token))
	if err != nil || row == nil {
		return "", err
	}
	device := row.peerID.String
	if row.status.String != "active" || !IsDeviceID(device) || !s.enrollmentValid(row, device) {
		return "", nil
	}
	return device, nil
}

// DeviceClaim is one place where a request can name the device it speaks
// for: the body's device_id, the query's device_id, the header
// X-Device-Id.
type DeviceClaim struct {
	// Present is false when the request has no such value.
	Present bool
	// Truthy says whether the value names a device at all: false for the
	// empty text and for null, which give way to the next place.
	Truthy bool
	// Text is the value when it is text.
	Text string
	// NoText marks a value that is no text: a number, true or false, a
	// list or an object. It names no device a token can be bound to.
	NoText bool
}

// ClaimFromText is the claim of a query parameter or a header: present is
// whether the request has it.
func ClaimFromText(value string, present bool) DeviceClaim {
	return DeviceClaim{Present: present, Truthy: present && value != "", Text: value}
}

// ClaimWithoutText is the claim of a value that is no text, such as a
// query parameter given as a list (device_id[]=a) or an object.
func ClaimWithoutText() DeviceClaim {
	return DeviceClaim{Present: true, Truthy: true, NoText: true}
}

// ClaimFromJSON is the claim of a body field, parsed with goapi.Parse:
// present is whether the body has the field. A string is text; null
// names no device, like the empty text; any other value is a claim
// without text.
func ClaimFromJSON(value any, present bool) DeviceClaim {
	switch v := value.(type) {
	case nil:
		return DeviceClaim{Present: present}
	case string:
		return ClaimFromText(v, present)
	}
	if !present {
		return DeviceClaim{}
	}
	return ClaimWithoutText()
}

// ErrDeviceMismatch is the refusal of a request that names another device
// than its token is bound to.
var ErrDeviceMismatch = &DeviceAuthError{Status: 403, Text: "Device ID mismatch"}

// RequireTokenDeviceMatch checks the device a request names against the
// device its token is bound to. The claims are looked at in order (body,
// query, header) and the first that names a device counts; when none
// does, the last one counts if the request has it. A request that names no
// device passes. It returns ErrDeviceMismatch for a claim of another
// device and for a claim that is no text.
func RequireTokenDeviceMatch(deviceID string, claims ...DeviceClaim) error {
	if len(claims) == 0 {
		return nil
	}
	claimed := claims[len(claims)-1]
	for _, claim := range claims {
		if claim.Present && claim.Truthy {
			claimed = claim
			break
		}
	}
	if claimed.Present && (claimed.NoText || claimed.Text != deviceID) {
		return ErrDeviceMismatch
	}
	return nil
}

// HashDeviceToken is the hash an enrollment token is stored by: the
// SHA-256 of its UTF-8 bytes in lower-case hex.
func HashDeviceToken(token string) string { return HashAccessToken(token) }

// minDeviceTokenLength is the shortest token a device socket may present,
// in UTF-16 units.
const minDeviceTokenLength = 8

// VerifyEnrollmentToken reports whether token is an enrollment token the
// device may use: known to the signal server, not revoked or expired, with
// uses left, and bound to this device. A token bound to no device passes
// only for a device id the signal server has no device row for yet: an
// enrolled device gets a token bound to it, so an unbound token is never
// the credential of a device that exists.
func (s *Service) VerifyEnrollmentToken(ctx context.Context, deviceID, token string) (bool, error) {
	row, err := lookupEnrollmentToken(ctx, s.db, HashDeviceToken(token))
	if err != nil {
		return false, err
	}
	if !s.enrollmentValid(row, deviceID) {
		return false, nil
	}
	if row.peerID.String != "" {
		return true, nil
	}
	known, err := deviceKnown(ctx, s.db, deviceID)
	if err != nil {
		return false, err
	}
	return !known, nil
}

func (s *Service) enrollmentValid(row *enrollmentToken, deviceID string) bool {
	if row == nil {
		return false
	}
	if row.status.String == "revoked" || row.status.String == "expired" {
		return false
	}
	if row.maxUses.Int64 > 0 && row.useCount.Int64 >= row.maxUses.Int64 {
		return false
	}
	if expires, ok := enrollmentExpiry(row.expiresAt, s.zone); ok && expires.Before(s.now()) {
		return false
	}
	if row.peerID.String != "" && row.peerID.String != deviceID {
		return false
	}
	return true
}

var (
	dateOnly     = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	dateTimeBare = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}(:\d{2}(\.\d+)?)?$`)
)

// enrollmentExpiry reads the expiry of an enrollment token. The signal
// server writes it with a time zone. Text without one is a time in this
// process's zone, a date alone is midnight UTC, and text that is no time
// means the token does not expire.
func enrollmentExpiry(value store.Time, zone *time.Location) (time.Time, bool) {
	text := value.String()
	if value.IsNull() || text == "" {
		return time.Time{}, false
	}
	switch {
	case dateOnly.MatchString(text):
		t, err := time.Parse("2006-01-02", text)
		return t, err == nil
	case dateTimeBare.MatchString(text):
		for _, layout := range []string{"2006-01-02 15:04:05.999999999", "2006-01-02T15:04:05.999999999", "2006-01-02 15:04", "2006-01-02T15:04"} {
			if t, err := time.ParseInLocation(layout, text, zone); err == nil {
				return t.Truncate(time.Millisecond), true
			}
		}
		return time.Time{}, false
	}
	t, ok := value.Instant()
	if !ok {
		return time.Time{}, false
	}
	return t.Truncate(time.Millisecond), true
}

// VerifyDeviceWSAuth checks the token a device presents on one of the
// device sockets. The device id and a token of 8 characters or more are
// needed. The token passes as an enrollment token (VerifyEnrollmentToken)
// or as a live device token bound to this device id, either only while
// the signal server does not hold the device as banned or soft-deleted.
//
// A failed enrollment lookup is returned as an error. A failed access
// token or ban lookup counts as "no".
func (s *Service) VerifyDeviceWSAuth(ctx context.Context, deviceID, token string) (bool, error) {
	if deviceID == "" || token == "" || utf16Len(toWellFormed(token)) < minDeviceTokenLength {
		return false, nil
	}
	enrolled, err := s.VerifyEnrollmentToken(ctx, deviceID, token)
	if err != nil {
		return false, err
	}
	if !enrolled {
		row, err := getAccessToken(ctx, s.db, token)
		if err != nil || !row.IsDeviceToken() || !row.ClientID.Valid || row.ClientID.String != deviceID {
			return false, nil
		}
	}
	shutOut, err := deviceShutOut(ctx, s.db, deviceID)
	return err == nil && !shutOut, nil
}
