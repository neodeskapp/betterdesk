package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/audit"
	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// signInAddressKey holds the address an in-process sign-in comes from.
type signInAddressKey struct{}

// WithSignInAddress returns ctx naming the address of the browser or client
// that signs in through an in-process call of the web console. The login,
// second-factor and directory checks count and record that address, so the
// sign-ins of different browsers do not share the console's own budget. A
// request from the listener is counted by its own address.
func WithSignInAddress(ctx context.Context, ip string) context.Context {
	return context.WithValue(ctx, signInAddressKey{}, ip)
}

// SignInAddress returns the address WithSignInAddress put into ctx.
func SignInAddress(ctx context.Context) (string, bool) {
	ip, ok := ctx.Value(signInAddressKey{}).(string)
	return ip, ok && ip != ""
}

// signInIP is the address a sign-in counts against: the one an in-process
// caller names, else the request's.
func (s *Server) signInIP(r *http.Request) string {
	if arrivedInternally(r) {
		if ip, ok := SignInAddress(r.Context()); ok {
			return ip
		}
	}
	return s.remoteIP(r)
}

// SignInDeadline is how long one sign-in may take, the directory's answer
// included. The console gives the whole sign-in this deadline and passes
// it on with its call to the API server, which stops waiting for the
// directory signInAnswerTime before it: an account is made, and a sign-in
// noted, only for a sign-in that is answered within the deadline. A
// caller without a deadline (a RustDesk client on the API port) gets this
// one.
var SignInDeadline = 30 * time.Second

// signInAnswerTime is the part of the deadline kept for writing the
// account and answering after the directory answered.
var signInAnswerTime = 2 * time.Second

// errDirectoryTimeout is a directory that did not answer before the
// sign-in's deadline.
var errDirectoryTimeout = errors.New("the directory server took too long to answer")

// directoryTimeoutMessage and directoryTimeoutCode answer a sign-in whose
// directory took too long.
const (
	directoryTimeoutMessage = "The directory server took too long to answer"
	directoryTimeoutCode    = "directory_timeout"
)

// writeDirectoryTimeout answers a sign-in whose directory did not answer
// before the deadline.
func writeDirectoryTimeout(w http.ResponseWriter) {
	writeJSON(w, http.StatusGatewayTimeout, map[string]string{"error": directoryTimeoutMessage, "code": directoryTimeoutCode})
}

// directoryContext is the context the directory work of a sign-in runs
// in: it ends signInAnswerTime before the deadline of the request (the
// console's sign-in), or of SignInDeadline from now when the request has
// none.
func directoryContext(ctx context.Context) (context.Context, context.CancelFunc) {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(SignInDeadline)
	}
	return context.WithDeadline(ctx, deadline.Add(-signInAnswerTime))
}

// contextLDAP is a directory that stops when its context ends
// (auth.LDAPProvider.AuthenticateContext).
type contextLDAP interface {
	AuthenticateContext(ctx context.Context, username, password string) (*auth.LDAPResult, error)
}

// ldapAuthenticate asks the directory within ctx. A directory that has
// not answered when ctx ends gives errDirectoryTimeout; its late answer
// is dropped.
func (s *Server) ldapAuthenticate(ctx context.Context, username, password string) (*auth.LDAPResult, error) {
	if ctx.Err() != nil {
		return nil, errDirectoryTimeout
	}
	type answer struct {
		result *auth.LDAPResult
		err    error
	}
	done := make(chan answer, 1)
	go func() {
		var a answer
		if p, ok := s.ldapProvider.(contextLDAP); ok {
			a.result, a.err = p.AuthenticateContext(ctx, username, password)
		} else {
			a.result, a.err = s.ldapProvider.Authenticate(username, password)
		}
		done <- a
	}()
	select {
	case a := <-done:
		if deadline, ok := ctx.Deadline(); ctx.Err() != nil || ok && !time.Now().Before(deadline) {
			return nil, errDirectoryTimeout
		}
		return a.result, a.err
	case <-ctx.Done():
		return nil, errDirectoryTimeout
	}
}

// passwordLoginStatus classifies the outcome of username+password authentication.
type passwordLoginStatus int

const (
	passwordLoginOK passwordLoginStatus = iota
	passwordLoginInvalid
	passwordLoginOIDC
	passwordLoginInternal
	// passwordLoginDisabled: the credentials are right, the account is
	// disabled.
	passwordLoginDisabled
	// passwordLoginTimeout: the directory did not answer before the
	// sign-in's deadline. Nothing was written.
	passwordLoginTimeout
)

// storeProviderFields keeps the email address and display name the
// directory or single sign-on gave at this sign-in on the account; a
// failure is logged and does not stop the sign-in.
func (s *Server) storeProviderFields(user *db.User, email, displayName string) {
	if err := s.db.UpdateUserFromProvider(user.ID, strings.TrimSpace(email), strings.TrimSpace(displayName)); err != nil {
		log.Printf("[auth] Storing the email and name of %s from the provider failed: %v", user.Username, err)
	}
}

// writeAccountDisabled answers a sign-in of a disabled account.
func writeAccountDisabled(w http.ResponseWriter) {
	writeJSON(w, http.StatusForbidden, map[string]string{"error": "This account is disabled", "code": "account_disabled"})
}

// refuseDisabled is the outcome for an account whose credentials were
// right but that is disabled; the audit log notes the refusal.
func (s *Server) refuseDisabled(user *db.User, clientIP, method string) passwordLoginResult {
	if s.auditLog != nil {
		details := map[string]string{"reason": "account_disabled"}
		if method != "" {
			details["method"] = method
		}
		s.auditLog.Log(audit.ActionAuthLoginFailed, clientIP, user.Username, details)
	}
	return passwordLoginResult{Status: passwordLoginDisabled}
}

// passwordLoginResult holds the resolved user after provider-bound authentication
// (Issue #148). Used by panel /api/auth/login and RustDesk client /api/login.
type passwordLoginResult struct {
	User       *db.User
	Status     passwordLoginStatus
	AuthMethod string // "ldap" when authenticated via LDAP; empty for local
}

// authenticatePasswordLogin validates credentials using the same provider-bound
// rules as the web panel: OIDC accounts reject password login; LDAP-bound and
// unknown users authenticate via LDAP when enabled; local accounts use local
// hash only (no LDAP fallthrough). A disabled account whose credentials are
// right is passwordLoginDisabled; a disabled directory account stays as it
// is, it is neither changed nor made again.
func (s *Server) authenticatePasswordLogin(username, password, clientIP string) passwordLoginResult {
	return s.authenticatePasswordLoginContext(context.Background(), username, password, clientIP)
}

// authenticatePasswordLoginContext is authenticatePasswordLogin within the
// deadline of ctx (directoryContext): a directory that answers too late
// gives passwordLoginTimeout, and nothing is written for it.
func (s *Server) authenticatePasswordLoginContext(ctx context.Context, username, password, clientIP string) passwordLoginResult {
	user, err := s.db.GetUser(username)
	if err != nil {
		return passwordLoginResult{Status: passwordLoginInternal}
	}

	provider := db.AuthProviderLocal
	if user != nil && user.AuthProvider != "" {
		provider = user.AuthProvider
	}

	if user != nil && provider == db.AuthProviderOIDC {
		if s.auditLog != nil {
			s.auditLog.Log(audit.ActionAuthLoginFailed, clientIP, username, map[string]string{"reason": "oidc_account_password_login"})
		}
		return passwordLoginResult{Status: passwordLoginOIDC}
	}

	ldapEnabled := s.ldapProvider != nil && s.ldapProvider.IsEnabled()

	if ldapEnabled && (user == nil || provider == db.AuthProviderLDAP) {
		dirCtx, cancel := directoryContext(ctx)
		defer cancel()
		ldapResult, ldapErr := s.ldapAuthenticate(dirCtx, username, password)
		if errors.Is(ldapErr, errDirectoryTimeout) {
			log.Printf("[LDAP] No answer for %s before the sign-in's deadline", username)
			if s.auditLog != nil {
				s.auditLog.Log(audit.ActionAuthLoginFailed, clientIP, username, map[string]string{"method": "ldap", "reason": directoryTimeoutCode})
			}
			return passwordLoginResult{Status: passwordLoginTimeout}
		}
		if ldapErr != nil {
			log.Printf("[LDAP] Auth error for %s: %v", username, ldapErr)
			ldapResult = nil
		}

		if ldapResult != nil && ldapResult.Authenticated {
			role := ldapResult.Role
			if role == "" {
				role = auth.RoleViewer
			}

			// The account is the one the directory entry names, not the
			// typed spelling: a directory matches names without regard to
			// case, so "Alice" signs in as alice and creates no second row.
			if name := strings.TrimSpace(ldapResult.Username); name != "" && name != username {
				username = name
				if user, err = s.db.GetUser(username); err != nil {
					return passwordLoginResult{Status: passwordLoginInternal}
				}
				// A local or single sign-on account of that name is never
				// turned into a directory account by a sign-in.
				if user != nil && user.AuthProvider != db.AuthProviderLDAP {
					if s.auditLog != nil {
						s.auditLog.Log(audit.ActionAuthLoginFailed, clientIP, username, map[string]string{"method": "ldap", "reason": "username_collision"})
					}
					return passwordLoginResult{Status: passwordLoginInvalid}
				}
			}

			// Directory names are compared in lower case: a directory
			// account made under a mixed-case name signs in as itself
			// and gets no second account. A name two accounts share in
			// lower case, or a local or single sign-on account's name in
			// another case, is refused.
			if user == nil {
				others, err := s.accountsInOtherCase(username)
				if err != nil {
					return passwordLoginResult{Status: passwordLoginInternal}
				}
				switch {
				case len(others) == 1 && others[0].AuthProvider == db.AuthProviderLDAP:
					user = others[0]
				case len(others) > 0:
					if s.auditLog != nil {
						s.auditLog.Log(audit.ActionAuthLoginFailed, clientIP, username, map[string]string{"method": "ldap", "reason": "username_collision"})
					}
					return passwordLoginResult{Status: passwordLoginInvalid}
				}
			}

			if user != nil && user.Disabled {
				return s.refuseDisabled(user, clientIP, "ldap")
			}
			if user == nil && !s.ldapProvider.Config().AllowSignup {
				log.Printf("[LDAP] No account for %s and Auto-create users is off", username)
				if s.auditLog != nil {
					s.auditLog.Log(audit.ActionAuthLoginFailed, clientIP, username, map[string]string{"method": "ldap", "reason": "no_account"})
				}
				return passwordLoginResult{Status: passwordLoginInvalid}
			}
			if user == nil {
				randomSecret, randErr := auth.GenerateRandomString(32)
				if randErr != nil {
					return passwordLoginResult{Status: passwordLoginInternal}
				}
				unusable, hashErr := auth.HashPassword(randomSecret)
				if hashErr != nil {
					return passwordLoginResult{Status: passwordLoginInternal}
				}
				newUser := &db.User{
					Username:     username,
					PasswordHash: unusable,
					Role:         role,
					AuthProvider: db.AuthProviderLDAP,
				}
				// Past the deadline the person is told that the sign-in
				// failed: no account for it
				if dirCtx.Err() != nil {
					return passwordLoginResult{Status: passwordLoginTimeout}
				}
				if createErr := s.db.CreateUser(newUser); createErr != nil {
					log.Printf("[LDAP] Failed to auto-create user %s: %v", username, createErr)
					return passwordLoginResult{Status: passwordLoginInternal}
				}
				user, _ = s.db.GetUser(username)
				if user == nil {
					return passwordLoginResult{Status: passwordLoginInternal}
				}
				log.Printf("[LDAP] Auto-provisioned user %s with role %s", username, role)
				s.storeProviderFields(user, ldapResult.Email, ldapResult.DisplayName)
			} else {
				if dirCtx.Err() != nil {
					return passwordLoginResult{Status: passwordLoginTimeout}
				}
				changed := false
				if ldapResult.Role != "" && ldapResult.Role != user.Role {
					user.Role = ldapResult.Role
					changed = true
					log.Printf("[LDAP] Updated role for %s to %s", username, ldapResult.Role)
				}
				if user.AuthProvider != db.AuthProviderLDAP {
					user.AuthProvider = db.AuthProviderLDAP
					changed = true
				}
				if changed {
					_ = s.db.UpdateUser(user)
				}
				s.storeProviderFields(user, ldapResult.Email, ldapResult.DisplayName)
			}

			if s.auditLog != nil {
				s.auditLog.Log(audit.ActionAuthLogin, clientIP, user.Username, map[string]string{"method": "ldap"})
			}
			return passwordLoginResult{User: user, Status: passwordLoginOK, AuthMethod: "ldap"}
		}

		if user != nil && provider == db.AuthProviderLDAP {
			if s.auditLog != nil {
				s.auditLog.Log(audit.ActionAuthLoginFailed, clientIP, username, map[string]string{"method": "ldap"})
			}
			return passwordLoginResult{Status: passwordLoginInvalid}
		}
	}

	if user == nil || provider != db.AuthProviderLocal || !auth.VerifyPassword(user.PasswordHash, password) {
		if s.auditLog != nil {
			s.auditLog.Log(audit.ActionAuthLoginFailed, clientIP, username, nil)
		}
		return passwordLoginResult{Status: passwordLoginInvalid}
	}

	if user.Disabled {
		return s.refuseDisabled(user, clientIP, "")
	}

	if auth.NeedsRehash(user.PasswordHash) {
		if newHash, hashErr := auth.HashPassword(password); hashErr == nil {
			user.PasswordHash = newHash
			_ = s.db.UpdateUser(user)
			log.Printf("[auth] Rehashed password for %s (migrated to PBKDF2)", user.Username)
		}
	}

	if s.auditLog != nil {
		s.auditLog.Log(audit.ActionAuthLogin, clientIP, user.Username, nil)
	}

	return passwordLoginResult{User: user, Status: passwordLoginOK}
}

// accountsInOtherCase returns the accounts whose name is name in another
// letter case or with spaces at its ends.
func (s *Server) accountsInOtherCase(name string) ([]*db.User, error) {
	users, err := s.db.ListUsers()
	if err != nil {
		return nil, err
	}
	want := strings.ToLower(strings.TrimSpace(name))
	var out []*db.User
	for _, u := range users {
		if u.Username != name && strings.ToLower(strings.TrimSpace(u.Username)) == want {
			out = append(out, u)
		}
	}
	return out, nil
}
