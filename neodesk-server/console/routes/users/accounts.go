package users

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"regexp"
	"strconv"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
	"github.com/neodeskapp/neodesk/neodesk-server/console/passwordlinks"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/emailchange"
	"github.com/neodeskapp/neodesk/neodesk-server/console/securitymail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The roles an account can be given here.
var assignableRoles = map[string]bool{
	"super_admin": true, "admin": true, "server_admin": true, "global_admin": true,
	"operator": true, "viewer": true, "pro": true, "guest_issuer": true,
}

// validRole is VALID_USER_ROLES.has(role).
func validRole(v jsbind.Value) bool { return v.IsString() && assignableRoles[v.Str()] }

// canAssignRole is canAssignUserRole: whether the caller may give an
// account the target role (access.Service.MayGiveRole). The super admin
// roles may give any role, global_admin operator, viewer, pro and
// guest_issuer, nobody else any; and no role is given that holds a
// permission the caller does not hold (L387).
func (h *handlers) canAssignRole(r *http.Request, target string) (bool, error) {
	return h.d.Access.MayGiveRole(r.Context(), callerRole(r), target)
}

// mayChange reports whether the caller may change, reset or delete an
// account, its user groups included (access.Service.MayChangeAccount):
// its own, or one whose role the caller could give.
func (h *handlers) mayChange(r *http.Request, target *account) (bool, error) {
	return h.d.Access.MayChangeAccount(r.Context(), kit.UserID(r), callerRole(r), access.PolicyUser{ID: target.ID.V, Role: target.role()})
}

// callerRole is req.session.user.role.
func callerRole(r *http.Request) string {
	if user := kit.User(r); user != nil {
		return user.Role
	}
	return ""
}

// isSessionAccount reports whether the account with the id is the
// caller's own. The session keeps the id as a number, and as text where
// the users table has 64-bit ids; both count as the number.
func isSessionAccount(r *http.Request, id int64) bool {
	own := kit.UserID(r)
	return own != 0 && own == id
}

// errRoleAboveOwn is the reply to a role the caller may not give.
var errRoleAboveOwn = say(http.StatusForbidden, "Cannot assign a role higher than your own")

// errOwnAccess is the reply to an account that changes what it may reach.
var errOwnAccess = say(http.StatusForbidden, "You cannot change your own device access")

// accessFields are the members of an account change that decide what the
// account may reach: its user groups, Direct devices, Folder access and
// strategy.
var accessFields = []string{"groupGuids", "folderIds", "peerIds", "strategyGuid"}

// ownAccessUnchanged refuses a change of the caller's own access fields
// unless the caller could change the account as someone else's: an
// account may change its own password and email, but no account widens
// what it may reach (L390).
func (h *handlers) ownAccessUnchanged(r *http.Request, user *account, body middleware.JSONObject) error {
	if !isSessionAccount(r, user.ID.V) {
		return nil
	}
	for _, field := range accessFields {
		if !body.Has(field) {
			continue
		}
		ok, err := h.canAssignRole(r, user.role())
		if err != nil {
			return err
		}
		if !ok {
			return errOwnAccess
		}
		return nil
	}
	return nil
}

// refusedOr is err when a role check failed, else errRoleAboveOwn.
func refusedOr(err error) error {
	if err != nil {
		return err
	}
	return errRoleAboveOwn
}

// errPasswordTooLong is the reply to a password the sign-in would refuse.
var errPasswordTooLong = say(http.StatusBadRequest, auth.ErrPasswordTooLong.Error())

// sayKey is the reply {"success":false,"error":<text of key>}.
func sayKey(r *http.Request, status int, key string) error {
	return say(status, kit.T(r, key, nil))
}

// hash is authService.hashPassword(password): text only, bcrypt refused
// anything else.
func (h *handlers) hash(ctx context.Context, password jsbind.Value) (string, error) {
	if !password.IsString() {
		return "", errors.New("data must be a string or Buffer and salt must either be a salt string or a number of rounds")
	}
	return h.d.Authn.HashPassword(ctx, password.Str())
}

// ---- The account list ----

// listedAccount is an account as the list and an update send it.
type listedAccount struct {
	ID           store.Int64String `json:"id"`
	Username     jsbind.Cell       `json:"username"`
	Role         jsbind.Cell       `json:"role"`
	Email        any               `json:"email"`
	AuthProvider any               `json:"auth_provider"`
	CreatedAt    store.Time        `json:"created_at"`
	LastLogin    store.Time        `json:"last_login"`
	UserGroups   []string          `json:"user_groups"`
	FolderIDs    []int64           `json:"folder_ids"`
	PeerGrants   []string          `json:"peer_grants"`
	StrategyGUID string            `json:"strategy_guid"`
	TOTPEnabled  bool              `json:"totp_enabled"`
	// Disabled says the account may not sign in.
	Disabled bool `json:"disabled"`
	// EmailPending is a new address that waits for its confirmation.
	EmailPending *pendingEmail `json:"email_pending,omitempty"`
	// ExpiresAt is the account's end date (RFC 3339), Expired says it
	// has passed.
	ExpiresAt string `json:"expires_at,omitempty"`
	Expired   bool   `json:"expired,omitempty"`
	// InactiveExempt keeps the rule for inactive accounts away from it.
	InactiveExempt bool `json:"inactive_exempt,omitempty"`
}

// withPicture is a listed account with what the list says about its
// picture and its sign-in (security.go).
type withPicture struct {
	listedAccount
	HasAvatar     bool             `json:"has_avatar"`
	AvatarVersion store.NullString `json:"avatar_version"`
	signInState
}

// cellOr is v || fallback for a column.
func cellOr(c jsbind.Cell, fallback string) any {
	if c.Value().Truthy() {
		return c
	}
	return fallback
}

// totpOn reports whether the second factor is on: true, 1 or "1", the
// forms the two databases store.
func totpOn(c jsbind.Cell) bool {
	v := c.Value()
	return v.JSON() == "true" || (v.IsNumber() && v.ToNumber() == 1) || (v.IsString() && v.Str() == "1")
}

// serialize is serializeUserForList: the account with its user groups,
// folders, device grants and strategy. When one of the lists cannot be
// read the account goes out with all of them empty.
func (h *handlers) serialize(ctx context.Context, a *account) listedAccount {
	out := listedAccount{
		ID: a.ID, Username: a.Username, Role: a.Role,
		Email: cellOr(a.Email, ""), AuthProvider: cellOr(a.AuthProvider, "local"),
		CreatedAt: a.CreatedAt, LastLogin: a.LastLogin, TOTPEnabled: totpOn(a.TOTPEnabled), Disabled: a.Disabled.True(),
		UserGroups: []string{}, FolderIDs: []int64{}, PeerGrants: []string{},
	}
	folders, err := h.d.Access.UserFolderIDs(ctx, a.username())
	var grants []string
	if err == nil {
		grants, err = h.d.Access.UserPeerGrantIDs(ctx, a.ID.V)
	}
	strategy := ""
	if err == nil {
		// A failed read of the strategy leaves it empty and the rest as is
		strategy, _ = strategyOf(ctx, h.d.Store, a.ID.V)
	}
	var groups []string
	if err == nil {
		groups, err = h.groupGUIDsOfAccount(ctx, a.ID.V)
	}
	if err != nil {
		h.log.Warn("[users] scope enrichment failed for "+a.username()+":", err.Error())
		return out
	}
	out.UserGroups, out.FolderIDs, out.PeerGrants, out.StrategyGUID = groups, folders, grants, strategy
	return out
}

// groupGUIDsOfAccount returns the guids of the user groups an account is
// a member of.
func (h *handlers) groupGUIDsOfAccount(ctx context.Context, id int64) ([]string, error) {
	rows, err := h.d.DeviceGroups.UserGroupsForUser(ctx, id)
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, row := range rows {
		if guid := row.Get("guid"); truthy(guid) {
			out = append(out, rowText(guid))
		}
	}
	return out, nil
}

type accountList struct {
	Success bool            `json:"success"`
	Data    accountListData `json:"data"`
}

type accountListData struct {
	Users []withPicture `json:"users"`
	Total int           `json:"total"`
}

// list answers GET /api/users: every account, ordered by id, with its
// groups, folders, grants, strategy and picture.
func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	accounts, err := allAccounts(ctx, h.d.Store)
	if err != nil {
		h.serverError(w, r, "Get users error:", err)
		return
	}
	versions, err := h.d.Avatars.GetVersionMap(ctx)
	if err != nil {
		versions = nil
	}
	facts, err := h.readSignInFacts(r)
	if err != nil {
		h.serverError(w, r, "Get users error:", err)
		return
	}
	pending, err := h.life.PendingEmails(ctx)
	if err != nil {
		h.serverError(w, r, "Get users error:", err)
		return
	}
	states, err := h.life.All(ctx)
	if err != nil {
		h.serverError(w, r, "Get users error:", err)
		return
	}
	now := h.now()
	out := make([]withPicture, len(accounts))
	for i, a := range accounts {
		has, version := versions.Fields(a.ID.V)
		listed := h.serialize(ctx, a)
		if c, ok := pending[a.ID.V]; ok {
			listed.EmailPending = pendingOf(&c)
		}
		withLife(&listed, states[a.ID.V], now)
		out[i] = withPicture{listedAccount: listed, HasAvatar: has, AvatarVersion: version, signInState: facts.of(a)}
	}
	answer(w, r, http.StatusOK, accountList{Success: true, Data: accountListData{Users: out, Total: len(out)}})
}

// ---- Creating ----

// usernamePattern is the form of a new account's name, the one the signal server
// checks too: 3 to 64 letters, digits and the characters _ . @ -.
var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_.@-]{3,64}$`)

// createdAccount is the answer to a new account, with the user groups it
// became a member of.
type createdAccount struct {
	ID         store.Int64String `json:"id"`
	Username   string            `json:"username"`
	Role       string            `json:"role"`
	Email      string            `json:"email"`
	UserGroups []string          `json:"user_groups"`
	FolderIDs  []int64           `json:"folder_ids"`
	PeerGrants []string          `json:"peer_grants"`
	// InviteSent says whether the invite mail went out, for an invite.
	InviteSent *bool `json:"invite_sent,omitempty"`
}

var uniqueViolation = regexp.MustCompile(`(?i)unique|users_username_key`)

// isUniqueViolation reports what the route took for a username taken in
// the meantime: PostgreSQL's code 23505, or a message that says unique.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return true
	}
	return uniqueViolation.MatchString(err.Error())
}

// create answers POST /api/users with the body {"username", "password",
// "role", "email", "groupGuids", "folderIds", "peerIds", "strategyGuid"}.
// What it checks, in order: both given, the username's form (text only),
// that it is free, the user group guids, the password's strength, the
// role the caller may give, the email, the device scope. Only then is
// anything written: the account, its groups, its email, its device scope
// and the audit entry.
func (h *handlers) create(w http.ResponseWriter, r *http.Request) {
	h.createWith(w, r, false)
}

// invite answers POST /api/users/invite with the body of create without
// the password and with the email address required: the account gets a
// password nobody knows, and the address a link to choose one. Without a
// username the part of the address before the @ gives one. Nothing is
// written when the console has no mail settings. The answer says in
// invite_sent whether the mail went out; the account stays either way.
func (h *handlers) invite(w http.ResponseWriter, r *http.Request) {
	h.createWith(w, r, true)
}

func (h *handlers) createWith(w http.ResponseWriter, r *http.Request, inviting bool) {
	created, err := h.createAccount(r, inviting)
	if err == nil {
		answer(w, r, http.StatusOK, struct {
			Success bool            `json:"success"`
			Data    *createdAccount `json:"data"`
		}{true, created})
		return
	}
	if sent(w, r, err) {
		return
	}
	h.log.Error("Create user error:", err.Error())
	// A username taken in the meantime
	if isUniqueViolation(err) {
		failKey(w, r, http.StatusBadRequest, "users.username_exists")
		return
	}
	h.clientError(w, r, err)
}

func (h *handlers) createAccount(r *http.Request, inviting bool) (*createdAccount, error) {
	ctx := r.Context()
	body := kit.Body(r)
	username, password, role, email := member(body, "username"), member(body, "password"), member(body, "role"), member(body, "email")
	// An invite has an email address and no password; the person chooses
	// one from the mail. Without a username the address gives one.
	var inviteTo *string
	if inviting {
		if password.Truthy() {
			return nil, sayKey(r, http.StatusBadRequest, "users.invite_password_given")
		}
		to, err := normalizeEmail(email)
		if err != nil {
			return nil, err
		}
		if to == nil || *to == "" {
			return nil, sayKey(r, http.StatusBadRequest, "users.invite_email_required")
		}
		if !h.mailReady(ctx) {
			return nil, sayKey(r, http.StatusConflict, "users.invite_needs_email")
		}
		inviteTo = to
		if !username.Truthy() {
			derived, err := h.usernameFromEmail(r, *to)
			if err != nil {
				return nil, err
			}
			username = jsbind.String(derived)
		}
	} else if !username.Truthy() || !password.Truthy() {
		return nil, sayKey(r, http.StatusBadRequest, "users.fill_required")
	}
	if !username.IsString() || !usernamePattern.MatchString(username.Str()) {
		return nil, sayKey(r, http.StatusBadRequest, "users.invalid_username")
	}
	name := username.Str()
	taken, err := usernameTaken(ctx, h.d.Store, name)
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, sayKey(r, http.StatusBadRequest, "users.username_exists")
	}
	groups, _, err := groupGUIDsOf(body)
	if err != nil {
		return nil, err
	}
	if groups == nil {
		groups = []string{}
	}
	if !inviting {
		if err := passwordRefusal(r, password); err != nil {
			return nil, err
		}
	}
	assigned := "viewer"
	switch {
	case validRole(role):
		assigned = role.Str()
	case role.Truthy():
		// A role the server doesn't know is refused, not made a viewer
		return nil, sayKey(r, http.StatusBadRequest, "users.invalid_role")
	}
	if ok, err := h.canAssignRole(r, assigned); err != nil || !ok {
		return nil, refusedOr(err)
	}
	var hash string
	if inviting {
		// A password nobody knows, until the invite sets one
		hash, err = h.unusableHash(ctx)
	} else {
		hash, err = h.hash(ctx, password)
	}
	if err != nil {
		return nil, err
	}
	var saved *string
	if !email.IsUndefined() {
		if saved, err = normalizeEmail(email); err != nil {
			return nil, err
		}
	}
	strategy, err := h.checkScope(ctx, body)
	if err != nil {
		return nil, err
	}

	id, err := insertAccount(ctx, h.d.Store, name, hash, assigned)
	if err != nil {
		return nil, err
	}
	if _, err := h.d.DeviceGroups.SetUserGroupMemberships(ctx, id.V, groups); err != nil {
		return nil, err
	}
	if !email.IsUndefined() {
		if err := updateEmail(ctx, h.d.Store, id.V, textOf(saved)); err != nil {
			return nil, err
		}
	}
	if err := h.applyScope(ctx, id.V, name, body, strategy); err != nil {
		return nil, err
	}
	if err := h.d.LogAction(r, "user_created", "Created user: "+name+" ("+assigned+")"); err != nil {
		return nil, err
	}
	out := &createdAccount{ID: id, Username: name, Role: assigned, Email: textOf(saved)}
	if out.UserGroups, err = h.groupGUIDsOfAccount(ctx, id.V); err != nil {
		return nil, err
	}
	if out.FolderIDs, err = h.d.Access.UserFolderIDs(ctx, name); err != nil {
		return nil, err
	}
	if out.PeerGrants, err = h.d.Access.UserPeerGrantIDs(ctx, id.V); err != nil {
		return nil, err
	}
	if inviting {
		// The account stays when the mail fails: the users page can send
		// the invite again
		delivered, _, err := h.sendLink(r, id.V, name, *inviteTo, passwordlinks.PurposeInvite, "")
		if err != nil {
			h.log.Error("Invite mail error:", err.Error())
		}
		out.InviteSent = &delivered
		if delivered {
			if err := h.d.LogAction(r, "user_invite_sent", "Sent an invite to user: "+name); err != nil {
				return nil, err
			}
		}
	}
	return out, nil
}

// unusableHash is the hash of a random password nobody knows.
func (h *handlers) unusableHash(ctx context.Context) (string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return h.d.Authn.HashPassword(ctx, hex.EncodeToString(random))
}

// textOf is the text p points to, "" for nil.
func textOf(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// clientError answers what an account write threw: its status, and its
// text when the status is 400; the server error otherwise
// (clientErrorMessage).
func (h *handlers) clientError(w http.ResponseWriter, r *http.Request, err error) {
	status := statusOf(err)
	if status == 0 {
		status = http.StatusInternalServerError
	}
	if status == http.StatusBadRequest {
		fail(w, r, status, refusalText(r, err))
		return
	}
	failKey(w, r, status, "errors.server_error")
}

// ---- Changing ----

// update answers PATCH /api/users/{id} with the body {"username", "role",
// "password", "email", "groupGuids", "folderIds", "peerIds",
// "strategyGuid", "disabled", "totp_enabled"}. What it checks, in order:
// the id, the account, whether the caller may change it (mayChange), the
// password and role of a directory or single sign-on account, the new
// username, the caller's own demotion, the role (the last administrator
// keeps one), the password, the user groups, the device scope, the email,
// disabled (true or false, never for the caller's own account),
// totp_enabled (only false, never for the caller's own account). Only then
// is anything written, in the same order. Disabling ends the account's
// sessions and revokes its access tokens; the account and its history
// stay. Turning off the second factor deletes its secret and recovery
// codes and ends the account's sessions too. The answer is the account as
// the list has it.
func (h *handlers) update(w http.ResponseWriter, r *http.Request) {
	userID, ok := pathID(r)
	if !ok {
		invalidID(w, r)
		return
	}
	updated, err := h.updateAccount(r, userID)
	if err == nil {
		answer(w, r, http.StatusOK, struct {
			Success bool           `json:"success"`
			Data    *listedAccount `json:"data"`
		}{true, updated})
		return
	}
	if sent(w, r, err) {
		return
	}
	h.log.Error("Update user error:", err.Error())
	h.clientError(w, r, err)
}

func (h *handlers) updateAccount(r *http.Request, userID float64) (*listedAccount, error) {
	ctx := r.Context()
	body := kit.Body(r)
	role, password, email := member(body, "role"), member(body, "password"), member(body, "email")
	user, err := accountByID(ctx, h.d.Store, userID)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, sayKey(r, http.StatusNotFound, "users.not_found")
	}
	if ok, err := h.mayChange(r, user); err != nil || !ok {
		return nil, refusedOr(err)
	}
	if err := h.ownAccessUnchanged(r, user, body); err != nil {
		return nil, err
	}
	provider, err := providerOf(user)
	if err != nil {
		return nil, err
	}
	external := provider == "ldap" || provider == "oidc"
	if external && password.Truthy() || provider == "ldap" && role.Truthy() {
		return nil, sayKey(r, http.StatusBadRequest, "users.provider_managed_hint")
	}
	next, renaming := newUsername(body, user)
	if renaming {
		if err := h.checkRename(r, user, next, external); err != nil {
			return nil, err
		}
	}
	if isSessionAccount(r, user.ID.V) && role.Truthy() && access.IsSuperAdminRole(callerRole(r)) && !(role.IsString() && access.IsSuperAdminRole(role.Str())) {
		return nil, sayKey(r, http.StatusBadRequest, "users.cannot_demote_self")
	}
	if role.Truthy() {
		if !validRole(role) {
			return nil, sayKey(r, http.StatusBadRequest, "users.invalid_role")
		}
		if ok, err := h.canAssignRole(r, role.Str()); err != nil || !ok {
			return nil, refusedOr(err)
		}
		if !access.IsSuperAdminRole(role.Str()) {
			// The last Super admin that can sign in keeps the role
			last, err := isLastSuperAdmin(ctx, h.d.Store, user.ID.V, user.role(), h.now())
			if err != nil {
				return nil, err
			}
			if last {
				return nil, sayKey(r, http.StatusBadRequest, "users.last_admin")
			}
		}
	}
	hash := ""
	if password.Truthy() {
		if err := passwordRefusal(r, password); err != nil {
			return nil, err
		}
		var err error
		if hash, err = h.hash(ctx, password); err != nil {
			return nil, err
		}
	}
	groups, given, err := groupGUIDsOf(body)
	if err != nil {
		return nil, err
	}
	strategy, err := h.checkScope(ctx, body)
	if err != nil {
		return nil, err
	}
	var normalized *string
	if !email.IsUndefined() {
		if normalized, err = normalizeEmail(email); err != nil {
			return nil, err
		}
		// A directory or single sign-on gives the address
		if err := emailchange.Refuse(targetOf(user), textOf(normalized)); err != nil {
			return nil, changeRefusal(r, err)
		}
	}
	disable, changeDisabled, err := disabledOf(r, body, user)
	if err != nil {
		return nil, err
	}
	if disable && changeDisabled {
		if last, err := isLastSuperAdmin(ctx, h.d.Store, user.ID.V, user.role(), h.now()); err != nil {
			return nil, err
		} else if last {
			return nil, sayKey(r, http.StatusBadRequest, "users.last_super_admin")
		}
	}
	expiresAt, changeExpiry, err := h.expiryOf(r, body, user)
	if err != nil {
		return nil, err
	}
	exempt, changeExempt, err := h.exemptOf(r, body, user)
	if err != nil {
		return nil, err
	}
	totpOff, err := totpOffOf(r, body, user)
	if err != nil {
		return nil, err
	}

	id := user.ID.V
	if renaming {
		if err := h.rename(r, user, next); err != nil {
			return nil, err
		}
	}
	if role.Truthy() {
		if err := updateRole(ctx, h.d.Store, id, role.Str()); err != nil {
			return nil, err
		}
	}
	if password.Truthy() {
		if err := updatePasswordHash(ctx, h.d.Store, id, hash); err != nil {
			return nil, err
		}
		h.d.Authn.FirstPasswordReset(user.username())
	}
	if given {
		if _, err := h.d.DeviceGroups.SetUserGroupMemberships(ctx, id, groups); err != nil {
			return nil, err
		}
	}
	if err := h.applyScope(ctx, id, user.username(), body, strategy); err != nil {
		return nil, err
	}
	if !email.IsUndefined() {
		// A new address waits for its confirmation (package emailchange)
		if _, err := h.changer().Request(r, targetOf(user), textOf(normalized), isSessionAccount(r, id)); err != nil {
			return nil, changeRefusal(r, err)
		}
	}
	if changeDisabled {
		if err := h.setDisabled(r, user, disable); err != nil {
			return nil, err
		}
	}
	if totpOff {
		if err := h.turnOffTOTP(r, user); err != nil {
			return nil, err
		}
	}
	if changeExpiry {
		if err := h.setExpiry(r, user, expiresAt); err != nil {
			return nil, err
		}
	}
	if changeExempt {
		if err := h.setExempt(r, user, exempt); err != nil {
			return nil, err
		}
	}
	// A new name or role ends every session of the account; a new password
	// keeps the session of a caller who changed their own
	if newRole := role.Truthy() && role.Str() != user.role(); renaming || newRole || password.Truthy() {
		if err := h.endSessions(r, id, !renaming && !newRole); err != nil {
			return nil, err
		}
	}
	if err := h.d.LogAction(r, "user_updated", "Updated user: "+user.username()); err != nil {
		return nil, err
	}
	updated, err := accountByID(ctx, h.d.Store, userID)
	if err != nil {
		return nil, err
	}
	if updated == nil {
		updated = user
	}
	out := h.serialize(ctx, updated)
	pending, err := h.life.PendingEmail(ctx, id)
	if err != nil {
		return nil, err
	}
	out.EmailPending = pendingOf(pending)
	st, err := h.life.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	withLife(&out, st, h.now())
	return &out, nil
}

// disabledOf reads the body's "disabled": change says it is there and
// differs from the account's flag. It must be true or false, and an
// account cannot disable itself.
func disabledOf(r *http.Request, body middleware.JSONObject, user *account) (disable, change bool, err error) {
	given := member(body, "disabled")
	if given.IsUndefined() {
		return false, false, nil
	}
	switch given.JSON() {
	case "true":
		disable = true
	case "false":
	default:
		return false, false, say(http.StatusBadRequest, "Invalid value for disabled")
	}
	if disable && isSessionAccount(r, user.ID.V) {
		return false, false, sayKey(r, http.StatusBadRequest, "users.cannot_disable_self")
	}
	return disable, disable != user.Disabled.True(), nil
}

// setDisabled disables or enables the account and notes it in the audit
// log. Disabling ends every session of the account and revokes its access
// tokens.
func (h *handlers) setDisabled(r *http.Request, user *account, disable bool) error {
	if err := setDisabled(r.Context(), h.d.Store, user.ID.V, disable); err != nil {
		return err
	}
	action, details := "user_enabled", "Enabled user: "
	if !disable {
		// Inactivity counts from now, and an end date that passed goes
		if err := h.life.Enabled(r.Context(), user.ID.V); err != nil {
			return err
		}
	}
	if disable {
		if err := h.endSessions(r, user.ID.V, false); err != nil {
			return err
		}
		action, details = "user_disabled", "Disabled user: "
	}
	return h.d.LogAction(r, action, details+user.username())
}

// totpOffOf reads the body's "totp_enabled": off says it is false and the
// account has two-factor sign-in on. Only false is taken, and not for the
// caller's own account, which turns it off in its profile with a code or
// its password.
func totpOffOf(r *http.Request, body middleware.JSONObject, user *account) (off bool, err error) {
	given := member(body, "totp_enabled")
	if given.IsUndefined() {
		return false, nil
	}
	if given.JSON() != "false" {
		return false, say(http.StatusBadRequest, "Invalid value for totp_enabled")
	}
	if isSessionAccount(r, user.ID.V) {
		return false, sayKey(r, http.StatusBadRequest, "users.totp_off_own")
	}
	// Two-factor sign-in of another account is its sign-in security
	if !kit.Access(r).HasPermission(auth.PermUserSecurity) {
		return false, say(http.StatusForbidden, kit.T(r, "users.security_needed", nil))
	}
	return totpOn(user.TOTPEnabled), nil
}

// turnOffTOTP turns off the account's two-factor sign-in for a person
// who lost their authenticator: the secret and the recovery codes go, the
// account's sessions end and its access tokens are revoked, and the audit
// log names the account.
func (h *handlers) turnOffTOTP(r *http.Request, user *account) error {
	again, err := h.d.Authn.TwoFactorDue(r.Context(), user.ID.V, isLocalAccount(user))
	if err != nil {
		return err
	}
	return h.clearTwoFactor(r, user, again)
}

// newUsername is the body's username, trimmed, when it is text and names
// the account differently. A username that is no text, or is empty after
// trimming, changes nothing.
func newUsername(body middleware.JSONObject, user *account) (string, bool) {
	given := member(body, "username")
	if !given.IsString() {
		return "", false
	}
	next := jsbind.Trim(given.Str())
	if current := user.Username.Value(); next == "" || current.IsString() && current.Str() == next {
		return "", false
	}
	return next, true
}

// checkRename checks a new username before anything is written: a
// directory or single sign-on account keeps the name its provider gives
// it, the name has the username's form and no other account has it.
func (h *handlers) checkRename(r *http.Request, user *account, next string, external bool) error {
	if external {
		return sayKey(r, http.StatusBadRequest, "users.provider_managed_hint")
	}
	if !usernamePattern.MatchString(next) {
		return sayKey(r, http.StatusBadRequest, "users.invalid_username")
	}
	taken, err := nameTakenByOther(r.Context(), h.d.Store, user.ID.V, next)
	if err != nil {
		return err
	}
	if taken {
		return sayKey(r, http.StatusConflict, "users.username_exists")
	}
	return nil
}

// rename gives the account the new username checkRename passed, with
// what is keyed by the name, and notes it in the audit log. Afterwards
// the account goes by the new name for the rest of the request.
func (h *handlers) rename(r *http.Request, user *account, next string) error {
	outcome, err := renameAccount(r.Context(), h.d.Store, user.ID.V, next)
	switch {
	case err != nil:
		return err
	case outcome == renameTaken:
		return sayKey(r, http.StatusConflict, "users.username_exists")
	case outcome == renameMissing:
		return sayKey(r, http.StatusNotFound, "users.not_found")
	}
	if err := h.d.LogAction(r, "user_renamed", "Renamed user "+user.username()+" -> "+next); err != nil {
		return err
	}
	user.Username = jsbind.CellOf(next)
	return nil
}

// providerOf is String(user.auth_provider || 'local').trim().toLowerCase().
func providerOf(a *account) (string, error) {
	provider, err := textOr(a.AuthProvider.Value(), "local")
	if err != nil {
		return "", err
	}
	return jsbind.ToLower(jsbind.Trim(provider)), nil
}

// ---- Deleting ----

// remove answers DELETE /api/users/{id}, with ?handover_to=<id> to give
// the account's API tokens, organization memberships, saved reports and
// open tickets to another account first (deletion.go). An account cannot
// delete itself, an account it may not change (mayChange), or the last
// Super admin that can sign in. What still points at the account goes
// with it (leftovers); the audit log keeps its history.
func (h *handlers) remove(w http.ResponseWriter, r *http.Request) {
	user := h.deletable(w, r)
	if user == nil {
		return
	}
	to, ok := h.handoverTarget(w, r, user)
	if !ok {
		return
	}
	details := "Deleted user: " + user.username()
	if to != nil {
		moved, err := h.handOver(r, user, to)
		if err != nil {
			h.serverError(w, r, "Delete user error:", err)
			return
		}
		details += "; handed over to " + to.username() + ": " + moved.String()
	}
	if err := h.leftovers(r, user); err != nil {
		h.serverError(w, r, "Delete user error:", err)
		return
	}
	if err := h.wipe(r, user); err != nil {
		h.serverError(w, r, "Delete user error:", err)
		return
	}
	if err := h.d.LogAccountAction(r, user.ID.V, "user_deleted", details); err != nil {
		h.serverError(w, r, "Delete user error:", err)
		return
	}
	answer(w, r, http.StatusOK, done{Success: true})
}

// wipe deletes an account the checks let go: its panel access policy, its
// sessions and access tokens, the account with its user group memberships
// and picture, its password links, its end date and address change.
func (h *handlers) wipe(r *http.Request, user *account) error {
	ctx := r.Context()
	if _, err := h.d.Access.SavePolicy(ctx, access.SubjectUser, accountIDText(user.ID), nil, nil); err != nil {
		return err
	}
	if err := h.endSessions(r, user.ID.V, false); err != nil {
		return err
	}
	if err := deleteAccount(ctx, h.d.Store, user.ID.V); err != nil {
		return err
	}
	if err := h.links.Revoke(ctx, user.ID.V); err != nil {
		return err
	}
	if err := h.life.Forget(ctx, user.ID.V); err != nil {
		return err
	}
	if err := h.d.Authn.ForgetAccount(ctx, user.ID.V); err != nil {
		return err
	}
	// The list of who has a picture forgets the account now
	h.d.Avatars.ResetCache()
	return nil
}

// ---- Resetting a password ----

// resetPassword answers POST /api/users/{id}/reset-password with the body
// {"newPassword"}, for an account the caller may change (mayChange). The
// account's sessions end and its access tokens are revoked, but the
// caller keeps the session of a reset of their own password.
func (h *handlers) resetPassword(w http.ResponseWriter, r *http.Request) {
	userID, ok := pathID(r)
	if !ok {
		invalidID(w, r)
		return
	}
	ctx := r.Context()
	password := member(kit.Body(r), "newPassword")
	user, err := accountByID(ctx, h.d.Store, userID)
	if err != nil {
		h.serverError(w, r, "Reset password error:", err)
		return
	}
	if user == nil {
		failKey(w, r, http.StatusNotFound, "users.not_found")
		return
	}
	if ok, err := h.mayChange(r, user); err != nil {
		h.serverError(w, r, "Reset password error:", err)
		return
	} else if !ok {
		sent(w, r, errRoleAboveOwn)
		return
	}
	if d := externalPassword(r, user); d != nil {
		deny(w, r, d)
		return
	}
	if !password.Truthy() {
		failKey(w, r, http.StatusBadRequest, "users.password_required")
		return
	}
	if refusal := passwordRefusal(r, password); refusal != nil {
		sent(w, r, refusal)
		return
	}
	hash, err := h.hash(ctx, password)
	if err == nil {
		err = updatePasswordHash(ctx, h.d.Store, user.ID.V, hash)
	}
	if err == nil {
		// The first-run password signs nobody in any more
		h.d.Authn.FirstPasswordReset(user.username())
		err = h.endSessions(r, user.ID.V, true)
	}
	if err == nil {
		// A link sent before would replace the password set here
		err = h.links.Revoke(ctx, user.ID.V)
	}
	if err == nil {
		err = h.d.LogAccountAction(r, user.ID.V, "password_reset", "Reset password for user: "+user.username())
	}
	if err != nil {
		h.serverError(w, r, "Reset password error:", err)
		return
	}
	how := securitymail.PasswordByAdmin
	if isSessionAccount(r, user.ID.V) {
		how = securitymail.PasswordBySelf
	}
	h.notices.PasswordChanged(ctx, user.ID.V, how)
	answer(w, r, http.StatusOK, done{Success: true})
}

// accountIDText is an account's id as the API server's paths have it.
func accountIDText(id store.Int64String) string { return strconv.FormatInt(id.V, 10) }

// endSessions ends the account's sessions and revokes its access tokens
// after a change to who it is or what it may do, so nothing goes on with
// what the account was at sign-in. keepOwn keeps the caller's session when
// the account is the caller's own.
func (h *handlers) endSessions(r *http.Request, id int64, keepOwn bool) error {
	keep := ""
	if keepOwn && isSessionAccount(r, id) {
		keep = kit.Session(r).ID()
	}
	if _, err := h.d.Sessions.EndAccount(r.Context(), id, keep); err != nil {
		return err
	}
	return h.d.Authn.RevokeClientTokens(r.Context(), id, "", "")
}
