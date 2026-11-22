package authn

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/unitronix/betterdesk-server/api"
	"github.com/unitronix/betterdesk-server/console/auditlog"
	"github.com/unitronix/betterdesk-server/console/store"
)

// WithClientAddress returns ctx naming the address a sign-in comes from
// (the browser's, or the client's). Authenticate passes it on to the API
// server with the checks it asks the API server for, whose limits count
// failed sign-ins per address: without it they count the console's own
// address, and every sign-in that reaches them shares one budget.
func WithClientAddress(ctx context.Context, ip string) context.Context {
	return api.WithSignInAddress(ctx, ip)
}

// The providers an account can belong to.
const (
	providerLocal = "local"
	providerLDAP  = "ldap"
	providerOIDC  = "oidc"
)

// FailureUsernameCollision is the failure code of a sign-in whose password
// belongs to a directory or single sign-on account of the same name as a
// local account.
const FailureUsernameCollision = "username_collision"

// FailureProOnly is the failure code of a sign-in with the right password
// for an account of the role "pro".
const FailureProOnly = "pro_only"

// FailureAccountDisabled is the failure code of a sign-in with the right
// credentials for an account that is disabled.
const FailureAccountDisabled = "account_disabled"

// rolePro marks accounts for the RustDesk desktop client only. They cannot
// sign in to the console.
const rolePro = "pro"

// User is the account a sign-in answers with. Its JSON is what the route
// keeps in the session while the second factor is pending.
type User struct {
	ID       store.Int64String
	Username string
	Role     store.NullString
	// PreferredLanguage is NULL when the account has none.
	PreferredLanguage store.NullString
	// TOTPRequired says the sign-in is not finished: the second factor is
	// due.
	TOTPRequired bool
	// TOTPSetupRequired says the sign-in is not finished either: two-factor
	// sign-in is mandatory (TwoFactorRequired) and this local account has
	// none, so it sets it up first. It is not part of the JSON.
	TOTPSetupRequired bool
	// nullPartialToken adds "goPartialToken": null to the JSON. The answer
	// for a local account with two-factor sign-in carries it, and sessions
	// that wait for the second factor store it.
	nullPartialToken bool
}

// MarshalJSON writes {id, username, role, preferred_language,
// totpRequired} and, for a local account with two-factor sign-in,
// "goPartialToken": null.
func (u User) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(`{"id":`)
	for i, field := range []struct {
		name  string
		value interface{ MarshalJSON() ([]byte, error) }
	}{
		{"", u.ID},
		{"username", store.NullString{String: u.Username, Valid: true}},
		{"role", u.Role},
		{"preferred_language", u.PreferredLanguage},
	} {
		if i > 0 {
			b.WriteString(`,"` + field.name + `":`)
		}
		raw, err := field.value.MarshalJSON()
		if err != nil {
			return nil, err
		}
		b.Write(raw)
	}
	fmt.Fprintf(&b, `,"totpRequired":%t`, u.TOTPRequired)
	if u.nullPartialToken {
		b.WriteString(`,"goPartialToken":null`)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// Outcome is the answer of Authenticate: an account, a refusal with a
// code, or a plain refusal (both empty).
type Outcome struct {
	// User is the account that signed in, nil when sign-in was refused.
	User *User
	// Failure is the code of a refusal that has its own answer:
	// FailureUsernameCollision, FailureProOnly or FailureAccountDisabled.
	// Empty otherwise.
	Failure string
}

// IsFailure reports whether the outcome is a refusal with a code.
func (o Outcome) IsFailure() bool { return o.Failure != "" }

// normalizeAuthProvider returns "ldap", "oidc" or "local" for the provider
// of an account or of the signal server's answer. Anything unknown is
// "local".
func normalizeAuthProvider(provider string) string {
	if provider == "" {
		return providerLocal
	}
	switch value := asciiLower(jsTrim(provider)); value {
	case providerLDAP, providerOIDC:
		return value
	}
	return providerLocal
}

func isExternalAuthProvider(provider string) bool {
	p := normalizeAuthProvider(provider)
	return p == providerLDAP || p == providerOIDC
}

// goAccept is what the signal server answered to a sign-in it accepted.
type goAccept struct {
	// role is the role it reports, empty when it sent none.
	role string
	// provider is its auth_provider, empty when it sent none.
	provider string
	// requires2FA says it asks for a second factor.
	requires2FA bool
	// disabled says the credentials are right and the account is
	// disabled: the server signs nobody in.
	disabled bool
}

// tryGoServerAuth asks the signal server to check the credentials. It
// returns nil when the server refuses or does not answer.
func (s *Service) tryGoServerAuth(ctx context.Context, username, password string) *goAccept {
	answer := s.goAPI.TryGoServerAuth(ctx, username, password)
	if answer == nil {
		return nil
	}
	accept := &goAccept{requires2FA: answer.Bool("requires2fa"), disabled: answer.Bool("account_disabled")}
	if role, ok := answer.Get("role").(string); ok {
		accept.role = role
	}
	if provider, ok := answer.Get("auth_provider").(string); ok {
		accept.provider = provider
	}
	return accept
}

// ssoStatus says which single sign-on the signal server has on.
type ssoStatus struct{ ldap, oidc, any bool }

func (s *Service) goSSOStatus(ctx context.Context) ssoStatus {
	status := s.goAPI.GetGoSSOStatus(ctx)
	return ssoStatus{ldap: status.Bool("ldap"), oidc: status.Bool("oidc"), any: status.Bool("any")}
}

// inferAuthProvider is the provider of a new account the signal server
// accepted: the one it names, else the one single sign-on that is on, else
// "local".
func inferAuthProvider(accept *goAccept, sso ssoStatus) string {
	if accept != nil && accept.provider != "" {
		return normalizeAuthProvider(accept.provider)
	}
	if sso.ldap && !sso.oidc {
		return providerLDAP
	}
	if sso.oidc && !sso.ldap {
		return providerOIDC
	}
	return providerLocal
}

// unusablePasswordHash is the hash of a random password nobody knows, for
// accounts that sign in elsewhere.
func (s *Service) unusablePasswordHash(ctx context.Context) (string, error) {
	random, err := s.randomHex(32)
	if err != nil {
		return "", err
	}
	return s.HashPassword(ctx, random)
}

// syncLocalUserFromGo brings the local row in line with what the signal
// server said about the account: provider, role, and the password hash
// when password is given for a local account. An account that turns into
// a directory or single sign-on account gets an unusable hash. A provider
// the answer does not name stays as the row has it, and an answer that asks
// for a second factor changes nothing: it names neither role nor provider.
// It returns the account as it is afterwards.
func (s *Service) syncLocalUserFromGo(ctx context.Context, local *Account, accept *goAccept, password string) (*Account, error) {
	if accept.requires2FA {
		return local, nil
	}
	localProvider := normalizeAuthProvider(local.AuthProvider.String)
	provider := localProvider
	if accept.provider != "" {
		provider = normalizeAuthProvider(accept.provider)
	}
	role := accept.role
	if role == "" {
		role = local.Role.String
	}
	var change userSync
	if provider != localProvider {
		change.authProvider = &provider
	}
	if role != "" && role != local.Role.String {
		change.role = &role
	}
	switch {
	case password != "" && provider == providerLocal:
		hash, err := s.HashPassword(ctx, password)
		if err != nil {
			return nil, err
		}
		change.passwordHash = &hash
	case provider != providerLocal && provider != localProvider:
		hash, err := s.unusablePasswordHash(ctx)
		if err != nil {
			return nil, err
		}
		change.passwordHash = &hash
	}
	if change.empty() {
		return local, nil
	}
	if err := syncUserFromGo(ctx, s.db, local.ID.V, change); err != nil {
		return nil, err
	}
	s.log.Info(fmt.Sprintf("[AUTH] Synced '%s' from Go (provider=%s, role=%s)", logName(local.Username), provider, roleText(role, local)))

	updated := *local
	if change.role != nil {
		updated.Role = nullText{String: *change.role, Valid: true}
	}
	switch {
	case change.authProvider != nil:
		updated.AuthProvider = nullText{String: *change.authProvider, Valid: true}
	case local.AuthProvider.String == "":
		updated.AuthProvider = nullText{String: providerLocal, Valid: true}
	}
	return &updated, nil
}

// roleText is how a role prints in a log line: a row without a role
// printed "null".
func roleText(role string, local *Account) string {
	if role == "" && !local.Role.Valid {
		return "null"
	}
	return role
}

// provisionLocalUserFromGo gives a user the signal server accepted a local
// row, with the server's role (else "viewer") and provider. A local
// account gets the hash of the password it signed in with, any other an
// unusable hash. A directory or single sign-on account is named as the
// signal server names it: lower-cased and without spaces at the ends.
func (s *Service) provisionLocalUserFromGo(ctx context.Context, username, password string, accept *goAccept, sso ssoStatus) (*Account, error) {
	provider := inferAuthProvider(accept, sso)
	if provider != providerLocal {
		username = strings.ToLower(strings.TrimSpace(username))
	}
	role := accept.role
	if role == "" {
		role = "viewer"
	}
	var hash string
	var err error
	if provider == providerLocal {
		hash, err = s.HashPassword(ctx, password)
	} else {
		hash, err = s.unusablePasswordHash(ctx)
	}
	if err != nil {
		return nil, err
	}

	user, err := getUserByUsername(ctx, s.db, username)
	if err != nil {
		return nil, err
	}
	if user != nil {
		return s.syncLocalUserFromGo(ctx, user, accept, "")
	}
	if createErr := createUser(ctx, s.db, username, hash, role, provider); createErr != nil {
		// The signal server shares the table and may have made the row in
		// the meantime.
		user, err = getUserByUsername(ctx, s.db, username)
		if err != nil {
			return nil, err
		}
		if user == nil {
			return nil, createErr
		}
		return s.syncLocalUserFromGo(ctx, user, accept, "")
	}

	user, err = getUserByUsername(ctx, s.db, username)
	if err != nil || user == nil {
		return nil, err
	}
	if !user.AuthProvider.Valid || user.AuthProvider.String != provider || !user.Role.Valid || user.Role.String != role {
		if err := syncUserFromGo(ctx, s.db, user.ID.V, userSync{authProvider: &provider, role: &role}); err != nil {
			return nil, err
		}
		user.AuthProvider = nullText{String: provider, Valid: true}
		user.Role = nullText{String: role, Valid: true}
	}
	return user, nil
}

// authSuccessFromUser builds the answer for an account whose credentials
// were accepted. It is nil for the role "pro". accept is the signal
// server's answer when it was the one that accepted.
func authSuccessFromUser(user *Account, accept *goAccept) *User {
	if user.Role.Valid && user.Role.String == rolePro {
		return nil
	}
	result := &User{
		ID:                user.ID,
		Username:          user.Username,
		Role:              user.Role,
		PreferredLanguage: user.PreferredLanguage.OrNull(),
	}
	if user.TOTPEnabled.True() {
		result.TOTPRequired = true
		result.nullPartialToken = accept == nil
		return result
	}
	result.TOTPRequired = accept != nil && accept.requires2FA
	return result
}

// finish turns accepted credentials into the outcome and notes the
// sign-in on the row unless the second factor is still due. A disabled
// account is the failure FailureAccountDisabled, a pro account the failure
// FailureProOnly.
func (s *Service) finish(ctx context.Context, user *Account, accept *goAccept) (Outcome, error) {
	if user.Disabled.True() {
		s.log.Info(fmt.Sprintf("[AUTH] Login refused: '%s' is disabled", logName(user.Username)))
		return Outcome{Failure: FailureAccountDisabled}, nil
	}
	result := authSuccessFromUser(user, accept)
	if result == nil {
		return Outcome{Failure: FailureProOnly}, nil
	}
	if !result.TOTPRequired && normalizeAuthProvider(user.AuthProvider.String) == providerLocal {
		required, err := s.TwoFactorRequired(ctx)
		if err != nil {
			return Outcome{}, err
		}
		result.TOTPSetupRequired = required
	}
	if !result.TOTPRequired && !result.TOTPSetupRequired {
		if err := touchLastLogin(ctx, s.db, user.ID.V); err != nil {
			return Outcome{}, err
		}
	}
	return Outcome{User: result}, nil
}

// Authenticate checks a user name and a password.
//
//   - An account bound to single sign-on (provider "oidc") cannot sign in
//     with a password.
//   - A directory account (provider "ldap") is checked by the signal
//     server every time, and its role and provider are copied from the
//     server's answer. An answer that asks for a second factor names
//     neither and changes nothing on the row.
//   - A local account is checked against its stored hash. When that fails
//     the signal server is asked, because it may hold a newer hash; if it
//     accepts through a directory or single sign-on, or if the directory
//     alone accepts the password, the outcome is the failure
//     FailureUsernameCollision. A hash the password fits stays as it is,
//     whatever its form.
//   - A name that differs from a directory account's only in letter case
//     or in spaces at the ends signs in to that account: the directory
//     compares names in lower case. For any other account such a name is
//     refused.
//   - An unknown user is refused, unless single sign-on is on or
//     Settings.AutoCreate is set and the signal server accepts the
//     credentials: then a local row is made, for a directory or single
//     sign-on account under the name lower-cased and trimmed.
//
// An account with the role "pro" is refused: with FailureProOnly when its
// credentials were accepted, plainly otherwise. So is a disabled account,
// with FailureAccountDisabled. The outcome's user has
// TOTPRequired set when the second factor is due, and TOTPSetupRequired
// when two-factor sign-in is mandatory and the local account has none yet;
// only otherwise is the sign-in noted on the row (last_login).
//
// Authenticate does not look at lockouts and does not record the attempt:
// CheckBruteForce and RecordAttempt are separate calls.
func (s *Service) Authenticate(ctx context.Context, username, password string) (Outcome, error) {
	if jsTrim(username) == "" {
		quoted, _ := store.NullString{String: username, Valid: true}.MarshalJSON()
		s.log.Info("[AUTH] Rejected authenticate() with empty/invalid username: " + string(quoted))
		return Outcome{}, nil
	}

	sso := s.goSSOStatus(ctx)
	user, err := getUserByUsername(ctx, s.db, username)
	if err != nil {
		return Outcome{}, err
	}
	if user == nil {
		// A directory compares names in lower case: another spelling of a
		// directory account's name is that account
		if user, err = directoryAccountNamed(ctx, s.db, username); err != nil {
			return Outcome{}, err
		}
	}
	if user == nil {
		// Another spelling of any other account would sign in as a second
		// account
		other, err := otherSpellingExists(ctx, s.db, username)
		if err != nil {
			return Outcome{}, err
		}
		if other {
			s.log.Info(fmt.Sprintf("[AUTH] Login failed: '%s' is another spelling of an existing account", logName(username)))
			return Outcome{}, nil
		}
		return s.authenticateUnknown(ctx, username, password, sso)
	}

	switch normalizeAuthProvider(user.AuthProvider.String) {
	case providerOIDC:
		s.log.Info(fmt.Sprintf("[AUTH] Login failed: '%s' is an SSO account (password login not allowed)", logName(username)))
		return Outcome{}, nil
	case providerLDAP:
		accept := s.tryGoServerAuth(ctx, username, password)
		if accept == nil {
			s.log.Info(fmt.Sprintf("[AUTH] Login failed: LDAP credentials rejected for '%s'", logName(username)))
			return Outcome{}, nil
		}
		if accept.disabled {
			s.log.Info(fmt.Sprintf("[AUTH] Login refused: '%s' is disabled", logName(username)))
			return Outcome{Failure: FailureAccountDisabled}, nil
		}
		s.log.Info(fmt.Sprintf("[AUTH] Go server accepted LDAP login for '%s' — syncing local account", logName(username)))
		user, err = s.syncLocalUserFromGo(ctx, user, accept, "")
		if err != nil {
			return Outcome{}, err
		}
		return s.finish(ctx, user, accept)
	}
	return s.authenticateLocal(ctx, user, username, password, sso)
}

// authenticateLocal is the sign-in of a local account.
func (s *Service) authenticateLocal(ctx context.Context, user *Account, username, password string, sso ssoStatus) (Outcome, error) {
	hashType := "unknown"
	switch {
	case isPBKDF2Hash(user.PasswordHash.String):
		hashType = "PBKDF2"
	case strings.HasPrefix(user.PasswordHash.String, "$2"):
		hashType = "bcrypt"
	}
	s.log.Info(fmt.Sprintf("[AUTH] Verifying password for '%s' (hash type: %s, length: %d)",
		logName(username), hashType, utf16Len(user.PasswordHash.String)))

	if !user.PasswordHash.Valid {
		return Outcome{}, errNoHash
	}
	valid, err := s.VerifyPassword(ctx, password, user.PasswordHash.String)
	if err != nil {
		return Outcome{}, err
	}

	if !valid {
		accept := s.tryGoServerAuth(ctx, username, password)
		switch {
		case accept != nil && accept.disabled:
			s.log.Info(fmt.Sprintf("[AUTH] Login refused: '%s' is disabled", logName(username)))
			return Outcome{Failure: FailureAccountDisabled}, nil
		case accept != nil:
			// A local account is never turned into a directory or single
			// sign-on account by a sign-in.
			if isExternalAuthProvider(accept.provider) {
				s.log.Info(fmt.Sprintf("[AUTH] Login blocked: username collision for local '%s' (Go provider=%s)",
					logName(username), normalizeAuthProvider(accept.provider)))
				return Outcome{Failure: FailureUsernameCollision}, nil
			}
			s.log.Info(fmt.Sprintf("[AUTH] Go server accepted password for local '%s' — syncing hash", logName(username)))
			user, err = s.syncLocalUserFromGo(ctx, user, accept, password)
			if err != nil {
				return Outcome{}, err
			}
		case sso.ldap && s.goAPI.TryLDAPVerifyOnGo(ctx, username, password):
			s.log.Info(fmt.Sprintf("[AUTH] Login blocked: username collision for local '%s' (LDAP credentials valid)", logName(username)))
			return Outcome{Failure: FailureUsernameCollision}, nil
		default:
			s.log.Info(fmt.Sprintf("[AUTH] Login failed: password mismatch for '%s' (hash type: %s)", logName(username), hashType))
			return Outcome{}, nil
		}
	} else {
		s.log.Info(fmt.Sprintf("[AUTH] Login successful for '%s'", logName(username)))
	}
	return s.finish(ctx, user, nil)
}

// authenticateUnknown is the sign-in of a name without a local row.
func (s *Service) authenticateUnknown(ctx context.Context, username, password string, sso ssoStatus) (Outcome, error) {
	// The compare keeps an unknown name as slow as a wrong password.
	if _, err := s.VerifyPassword(ctx, password, s.dummy); err != nil {
		return Outcome{}, err
	}

	if s.set.AutoCreate || sso.any {
		if accept := s.tryGoServerAuth(ctx, username, password); accept != nil && accept.disabled {
			return Outcome{Failure: FailureAccountDisabled}, nil
		} else if accept != nil {
			reason := "OIDC enabled on Go server"
			switch {
			case s.set.AutoCreate:
				reason = "BETTERDESK_AUTH_AUTOCREATE=true"
			case sso.ldap && sso.oidc:
				reason = "LDAP+OIDC enabled on Go server"
			case sso.ldap:
				reason = "LDAP enabled on Go server"
			}
			s.log.Info(fmt.Sprintf("[AUTH] Go server accepted credentials for '%s' — provisioning local user (%s)", logName(username), reason))
			created, err := s.provisionLocalUserFromGo(ctx, username, password, accept, sso)
			if err != nil {
				return Outcome{}, err
			}
			if created != nil {
				return s.finish(ctx, created, accept)
			}
		}
	}
	s.log.Info(fmt.Sprintf("[AUTH] Login failed: user '%s' not found in database", logName(username)))
	return Outcome{}, nil
}

// logName is a user name as the sign-in log lines print it: shortened to
// its first and last character, so the log does not hold the account
// names people sign in with.
func logName(username string) string { return auditlog.RedactUsername(username) }
