package users

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/authn"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/store"
)

// The roles an account can be given here. guest_issuer is not one of them.
var assignableRoles = map[string]bool{
	"super_admin": true, "admin": true, "server_admin": true, "global_admin": true,
	"operator": true, "viewer": true, "pro": true,
}

// validRole is VALID_USER_ROLES.has(role).
func validRole(v jsbind.Value) bool { return v.IsString() && assignableRoles[v.Str()] }

// canAssignRole is canAssignUserRole: the super admin roles may give any
// role, global_admin may give operator, viewer and pro, nobody else any.
func canAssignRole(caller, target string) bool {
	if access.IsSuperAdminRole(caller) {
		return true
	}
	if caller == "global_admin" {
		return target == "operator" || target == "viewer" || target == "pro"
	}
	return false
}

// mayChange reports whether the caller may change, reset or delete an
// account: its own, or one whose role the caller could give. global_admin
// also looks after guest_issuer accounts, a role nobody is given here.
func mayChange(r *http.Request, target *account) bool {
	if isSessionAccount(r, target.ID.V) {
		return true
	}
	caller, role := callerRole(r), target.role()
	return canAssignRole(caller, role) || caller == "global_admin" && role == "guest_issuer"
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

// weakPassword is the reply to a password that rates weak, with what it
// lacks.
func weakPassword(rating authn.PasswordStrength) error {
	return &reply{write: func(w http.ResponseWriter, r *http.Request) {
		answer(w, r, http.StatusBadRequest, struct {
			Success  bool     `json:"success"`
			Error    string   `json:"error"`
			Feedback []string `json:"feedback"`
		}{Error: kit.T(r, "users.weak_password", nil), Feedback: rating.Feedback})
	}}
}

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
}

// withPicture is a listed account with what the list says about its
// picture.
type withPicture struct {
	listedAccount
	HasAvatar     bool             `json:"has_avatar"`
	AvatarVersion store.NullString `json:"avatar_version"`
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
		CreatedAt: a.CreatedAt, LastLogin: a.LastLogin, TOTPEnabled: totpOn(a.TOTPEnabled),
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
	out := make([]withPicture, len(accounts))
	for i, a := range accounts {
		has, version := versions.Fields(a.ID.V)
		out[i] = withPicture{listedAccount: h.serialize(ctx, a), HasAvatar: has, AvatarVersion: version}
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
	created, err := h.createAccount(r)
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

func (h *handlers) createAccount(r *http.Request) (*createdAccount, error) {
	ctx := r.Context()
	body := kit.Body(r)
	username, password, role, email := member(body, "username"), member(body, "password"), member(body, "role"), member(body, "email")
	if !username.Truthy() || !password.Truthy() {
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
	rating, err := strength(password)
	if err != nil {
		return nil, err
	}
	if rating.Strength == "weak" {
		return nil, weakPassword(rating)
	}
	if password.IsString() && rating.TooShort() {
		return nil, sayKey(r, http.StatusBadRequest, "users.password_too_short")
	}
	assigned := "viewer"
	if validRole(role) {
		assigned = role.Str()
	}
	if !canAssignRole(callerRole(r), assigned) {
		return nil, errRoleAboveOwn
	}
	hash, err := h.hash(ctx, password)
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
	return out, nil
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
// "strategyGuid"}. What it checks, in order: the id, the account,
// whether the caller may change it (mayChange), the password and role of
// a directory or single sign-on account, the new username, the caller's
// own demotion, the role (the last administrator keeps one), the
// password, the user groups, the device scope, the email. Only then is
// anything written, in the same order. The answer is the account as the
// list has it.
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
	if !mayChange(r, user) {
		return nil, errRoleAboveOwn
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
		if !canAssignRole(callerRole(r), role.Str()) {
			return nil, errRoleAboveOwn
		}
		if access.IsSuperAdminRole(user.role()) && !access.IsSuperAdminRole(role.Str()) {
			admins, err := countAdmins(ctx, h.d.Store)
			if err != nil {
				return nil, err
			}
			if admins <= 1 {
				return nil, sayKey(r, http.StatusBadRequest, "users.last_admin")
			}
		}
	}
	hash := ""
	if password.Truthy() {
		rating, err := strength(password)
		if err != nil {
			return nil, err
		}
		if rating.Strength == "weak" {
			return nil, weakPassword(rating)
		}
		if password.IsString() && rating.TooShort() {
			return nil, sayKey(r, http.StatusBadRequest, "users.password_too_short")
		}
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
		if err := updateEmail(ctx, h.d.Store, id, textOf(normalized)); err != nil {
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
	return &out, nil
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

// remove answers DELETE /api/users/{id}. An account cannot delete itself
// or an account it may not change (mayChange), and the last account with
// the role admin or super_admin cannot be deleted. The account's panel
// access policy is emptied first.
func (h *handlers) remove(w http.ResponseWriter, r *http.Request) {
	userID, ok := pathID(r)
	if !ok {
		invalidID(w, r)
		return
	}
	ctx := r.Context()
	user, err := accountByID(ctx, h.d.Store, userID)
	if err != nil {
		h.serverError(w, r, "Delete user error:", err)
		return
	}
	if user == nil {
		failKey(w, r, http.StatusNotFound, "users.not_found")
		return
	}
	if isSessionAccount(r, user.ID.V) {
		failKey(w, r, http.StatusBadRequest, "users.cannot_delete_self")
		return
	}
	if !mayChange(r, user) {
		sent(w, r, errRoleAboveOwn)
		return
	}
	admins, err := countAdmins(ctx, h.d.Store)
	if err != nil {
		h.serverError(w, r, "Delete user error:", err)
		return
	}
	if access.IsSuperAdminRole(user.role()) && admins <= 1 {
		failKey(w, r, http.StatusBadRequest, "users.last_admin")
		return
	}
	if _, err := h.d.Access.SavePolicy(ctx, access.SubjectUser, jsbind.NumberString(userID), nil, nil); err != nil {
		h.serverError(w, r, "Delete user error:", err)
		return
	}
	if err := h.endSessions(r, user.ID.V, false); err != nil {
		h.serverError(w, r, "Delete user error:", err)
		return
	}
	if err := deleteAccount(ctx, h.d.Store, user.ID.V); err != nil {
		h.serverError(w, r, "Delete user error:", err)
		return
	}
	// The list of who has a picture forgets the account now
	h.d.Avatars.ResetCache()
	if err := h.d.LogAction(r, "user_deleted", "Deleted user: "+user.username()); err != nil {
		h.serverError(w, r, "Delete user error:", err)
		return
	}
	answer(w, r, http.StatusOK, done{Success: true})
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
	if !mayChange(r, user) {
		sent(w, r, errRoleAboveOwn)
		return
	}
	if !password.Truthy() {
		failKey(w, r, http.StatusBadRequest, "users.password_required")
		return
	}
	rating, err := strength(password)
	if err != nil {
		h.serverError(w, r, "Reset password error:", err)
		return
	}
	if rating.Strength == "weak" {
		sent(w, r, weakPassword(rating))
		return
	}
	if password.IsString() && rating.TooShort() {
		failKey(w, r, http.StatusBadRequest, "users.password_too_short")
		return
	}
	hash, err := h.hash(ctx, password)
	if err == nil {
		err = updatePasswordHash(ctx, h.d.Store, user.ID.V, hash)
	}
	if err == nil {
		err = h.endSessions(r, user.ID.V, true)
	}
	if err == nil {
		err = h.d.LogAction(r, "password_reset", "Reset password for user: "+user.username())
	}
	if err != nil {
		h.serverError(w, r, "Reset password error:", err)
		return
	}
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
