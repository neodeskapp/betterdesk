package authn

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/store"
)

// Checks for requests and sockets that come from devices. A device proves
// itself with a token: an access token bound to its id, or on the device
// sockets also an enrollment token of the signal server. The header
// X-Device-Id names a device and proves nothing.

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
	// Token is the token's row.
	Token *AccessToken
}

// RequireDeviceToken checks the Authorization header of a device request.
// It needs a Bearer access token that is live and bound to a device id,
// and notes the use on the token. It refuses with a *DeviceAuthError:
//
//	401 "Bearer access token required"     no Bearer token
//	401 "Invalid or expired access token"  the lookup failed
//	401 "Invalid or unbound access token"  no such token, or not bound to a device id
func (s *Service) RequireDeviceToken(ctx context.Context, authorization string) (*DeviceAuth, error) {
	token, ok := ExtractBearerToken(authorization)
	if !ok {
		return nil, &DeviceAuthError{Status: 401, Text: "Bearer access token required"}
	}
	row, err := getAccessToken(ctx, s.db, token)
	if err != nil {
		return nil, &DeviceAuthError{Status: 401, Text: "Invalid or expired access token"}
	}
	if row == nil || row.ClientID.String == "" || !IsDeviceID(row.ClientID.String) {
		return nil, &DeviceAuthError{Status: 401, Text: "Invalid or unbound access token"}
	}
	// The token is good; a failure to note its use changes nothing.
	_ = touchAccessToken(ctx, s.db, token)
	return &DeviceAuth{DeviceID: row.ClientID.String, Token: row}, nil
}

// DeviceClaim is one place where a request can name the device it speaks
// for: the body's device_id, the query's device_id, the header
// X-Device-Id.
type DeviceClaim struct {
	// Present is false when the request has no such value.
	Present bool
	// Truthy says whether JavaScript takes the value for true: not "", 0,
	// false or null.
	Truthy bool
	// Text is the value as text.
	Text string
}

// ClaimFromText is the claim of a query parameter or a header: present is
// whether the request has it.
func ClaimFromText(value string, present bool) DeviceClaim {
	return DeviceClaim{Present: present, Truthy: present && value != "", Text: value}
}

// ClaimFromJSON is the claim of a body field, parsed with goapi.Parse:
// present is whether the body has the field.
func ClaimFromJSON(value any, present bool) DeviceClaim {
	if !present {
		return DeviceClaim{}
	}
	return DeviceClaim{Present: true, Truthy: goapi.Truthy(value), Text: jsonText(value)}
}

// jsonText is String(value) for a parsed JSON value.
func jsonText(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		quoted, _ := store.JSONParsed{}.MarshalJSON()
		var number store.JSONParsed
		if err := number.Scan(strconv.FormatFloat(v, 'g', -1, 64)); err == nil {
			quoted, _ = number.MarshalJSON()
		}
		return string(quoted)
	case []any:
		parts := make([]string, len(v))
		for i, element := range v {
			if element != nil {
				parts[i] = jsonText(element)
			}
		}
		return strings.Join(parts, ",")
	}
	return "[object Object]"
}

// ErrDeviceMismatch is the refusal of a request that names another device
// than its token is bound to.
var ErrDeviceMismatch = &DeviceAuthError{Status: 403, Text: "Device ID mismatch"}

// RequireTokenDeviceMatch checks the device a request names against the
// device its token is bound to. The claims are looked at in order (body,
// query, header) and the first that is true counts; when none is, the last
// one counts if the request has it. A request that names no device passes.
// It returns ErrDeviceMismatch otherwise.
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
	if claimed.Present && claimed.Text != deviceID {
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
// uses left, and bound to this device or to none.
func (s *Service) VerifyEnrollmentToken(ctx context.Context, deviceID, token string) (bool, error) {
	row, err := lookupEnrollmentToken(ctx, s.db, HashDeviceToken(token))
	if err != nil {
		return false, err
	}
	return s.enrollmentValid(row, deviceID), nil
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
// or as a live access token bound to this device id.
//
// A failed enrollment lookup is returned as an error. A failed access
// token lookup counts as "no".
func (s *Service) VerifyDeviceWSAuth(ctx context.Context, deviceID, token string) (bool, error) {
	if deviceID == "" || token == "" || utf16Len(toWellFormed(token)) < minDeviceTokenLength {
		return false, nil
	}
	enrolled, err := s.VerifyEnrollmentToken(ctx, deviceID, token)
	if err != nil {
		return false, err
	}
	if enrolled {
		return true, nil
	}
	for _, candidate := range []string{token, HashAccessToken(token)} {
		row, err := getAccessToken(ctx, s.db, candidate)
		if err != nil {
			return false, nil
		}
		if row != nil && row.ClientID.Valid && row.ClientID.String == deviceID {
			return true, nil
		}
	}
	return false, nil
}
