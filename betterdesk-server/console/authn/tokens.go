package authn

import (
	"context"
	"fmt"
	"time"

	"github.com/unitronix/betterdesk-server/console/store"
)

// Access tokens of the RustDesk client API: 32 random bytes as 64
// lower-case hex characters. A row keeps only the token's SHA-256, by
// which every lookup finds it.

// accessTokenLength is the length a presented token must have, in UTF-16
// units.
const accessTokenLength = 64

// HashAccessToken is the hash an access token is stored and looked up by:
// the SHA-256 of its UTF-8 bytes in lower-case hex.
func HashAccessToken(token string) string {
	return store.HashAccessToken(toWellFormed(token))
}

// GenerateSignInToken issues an account's token for a sign-in of a client.
// It first revokes the user's tokens with the same client UUID (the client
// id plays no part in that; an empty UUID revokes the tokens that have
// none). The token lasts TokenExpiryDays days. It is an account's token
// whatever client id it stores: it never proves a device.
func (s *Service) GenerateSignInToken(ctx context.Context, userID int64, clientID, clientUUID, ip string) (string, error) {
	if err := revokeUserClientTokens(ctx, s.db, userID, clientUUID); err != nil {
		return "", err
	}
	return s.IssueAccessToken(ctx, userID, clientID, clientUUID, ip)
}

// IssueDeviceToken issues a device token: a token the console gives a
// device it admitted, bound to its id, which the device guards
// (RequireDeviceToken, VerifyDeviceWSAuth) accept. It revokes the device
// tokens the device had. userID is the account on whose behalf the token
// is issued; the token never counts as that account's sign-in. The token
// lasts TokenExpiryDays days.
func (s *Service) IssueDeviceToken(ctx context.Context, userID int64, deviceID, ip string) (string, error) {
	if !IsDeviceID(deviceID) {
		return "", fmt.Errorf("device token: %q is no device id", deviceID)
	}
	if err := revokeDeviceTokens(ctx, s.db, deviceID); err != nil {
		return "", err
	}
	return s.issue(ctx, TokenKindDevice, userID, deviceID, "", ip)
}

// RevokeDeviceTokens revokes the device tokens issued for the device, for
// a device that is banned or removed. The tokens of accounts stay.
func (s *Service) RevokeDeviceTokens(ctx context.Context, deviceID string) error {
	return revokeDeviceTokens(ctx, s.db, deviceID)
}

// GenerateAccessToken issues a device token the way cmd/devseed issues the
// test stack's device tokens: it revokes the user's account tokens with the
// same client UUID, then stores a device token bound to clientID with that
// UUID. Nothing in the console calls it; cmd/devseed is to call
// IssueDeviceToken instead, and then this goes.
func (s *Service) GenerateAccessToken(ctx context.Context, userID int64, clientID, clientUUID, ip string) (string, error) {
	if err := revokeUserClientTokens(ctx, s.db, userID, clientUUID); err != nil {
		return "", err
	}
	return s.issue(ctx, TokenKindDevice, userID, clientID, clientUUID, ip)
}

// TokenExpiryDays is how many days a new access token lasts.
func (s *Service) TokenExpiryDays() int { return int(s.tokenDays.Load()) }

// SetTokenExpiryDays changes how long new access tokens last, from the
// text of the setting as API_TOKEN_EXPIRY_DAYS is read: its whole number,
// or 7 when it has none or the number is 0.
func (s *Service) SetTokenExpiryDays(text string) {
	s.tokenDays.Store(int64(intOr(text, 7)))
}

// IssueAccessToken issues an account's token for a sign-in and revokes
// none. It is for a sign-in whose request names no client UUID at all:
// nothing is revoked, and the token is stored with an empty UUID.
func (s *Service) IssueAccessToken(ctx context.Context, userID int64, clientID, clientUUID, ip string) (string, error) {
	return s.issue(ctx, TokenKindAccount, userID, clientID, clientUUID, ip)
}

// issue stores a new token of the kind and returns it.
func (s *Service) issue(ctx context.Context, kind string, userID int64, clientID, clientUUID, ip string) (string, error) {
	token, err := s.randomHex(32)
	if err != nil {
		return "", fmt.Errorf("access token: %w", err)
	}
	expires := s.now().Add(time.Duration(s.tokenDays.Load()) * 24 * time.Hour)
	if err := createAccessToken(ctx, s.db, kind, token, userID, clientID, clientUUID, s.expiryText(expires), ip); err != nil {
		return "", err
	}
	return token, nil
}

// TokenUser is the account behind an access token.
type TokenUser struct {
	ID         store.Int64String `json:"id"`
	Username   string            `json:"username"`
	Role       store.NullString  `json:"role"`
	ClientID   store.NullString  `json:"clientId"`
	ClientUUID store.NullString  `json:"clientUuid"`
}

// ValidateAccessToken returns the account a token belongs to and notes the
// use on the token. It is nil for a token that is not 64 characters long,
// unknown, revoked or expired, for a device token, which is no account's
// sign-in, and for a token whose account is gone.
func (s *Service) ValidateAccessToken(ctx context.Context, token string) (*TokenUser, error) {
	if utf16Len(toWellFormed(token)) != accessTokenLength {
		return nil, nil
	}
	record, err := getAccessToken(ctx, s.db, token)
	if err != nil || record == nil || record.IsDeviceToken() {
		return nil, err
	}
	user, err := getUserByID(ctx, s.db, record.UserID.V)
	if err != nil || user == nil {
		return nil, err
	}
	if err := touchAccessToken(ctx, s.db, token); err != nil {
		return nil, err
	}
	return &TokenUser{
		ID:         user.ID,
		Username:   user.Username,
		Role:       user.Role,
		ClientID:   record.ClientID,
		ClientUUID: record.ClientUUID,
	}, nil
}

// RevokeClientTokens revokes the user's tokens with the client UUID when
// both the client id and the UUID are given, and all of the user's tokens
// otherwise.
func (s *Service) RevokeClientTokens(ctx context.Context, userID int64, clientID, clientUUID string) error {
	if clientID != "" && clientUUID != "" {
		return revokeUserClientTokens(ctx, s.db, userID, clientUUID)
	}
	return revokeAllUserTokens(ctx, s.db, userID)
}

// RevokeClientUUIDTokens revokes the user's tokens with this client UUID,
// an empty one included. The client API's sign-in revokes this way for a
// UUID the request names in any form (db.revokeUserClientTokens).
func (s *Service) RevokeClientUUIDTokens(ctx context.Context, userID int64, clientUUID string) error {
	return revokeUserClientTokens(ctx, s.db, userID, clientUUID)
}

// LookupAccessToken returns the row of a live access token, found by the
// token's hash, or nil. The device routes that check tokens themselves use
// it.
func (s *Service) LookupAccessToken(ctx context.Context, token string) (*AccessToken, error) {
	return getAccessToken(ctx, s.db, token)
}

// TouchAccessToken notes the use of a token on its row.
func (s *Service) TouchAccessToken(ctx context.Context, token string) error {
	return touchAccessToken(ctx, s.db, token)
}
