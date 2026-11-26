package users

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/people"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The user's page (/users/{id}): one account with everything the list
// knows of it, its profile (display name, language, time zone), what its
// role allows, and its activity in the audit log. Anyone with user.view
// sees every account, as the list shows them all; an id without an
// account answers 404, and an id that is no positive number 400, as the
// other routes of /api/users/{id} do.

// pageAccount is an account as the user's page reads it.
type pageAccount struct {
	withPicture
	// FirstName and LastName are the profile's; the display name (of
	// withPicture) is both joined, else the username.
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	// PreferredLanguage is the language the console and mails use for the
	// account, "" for the console's default.
	PreferredLanguage string `json:"preferred_language"`
	// TimeZone is an IANA name, "" for none (UTC in mails).
	TimeZone string `json:"time_zone"`
	// Invite is the invite not used yet, nil for none.
	Invite *pageInvite `json:"invite"`
	// Permissions are the role's permissions in effect, overrides
	// included; a panel access policy may still close sections.
	Permissions []string `json:"permissions"`
	// TeamChat says the account may use Team chat (chat.team) and is not
	// disabled: the page offers Send a message.
	TeamChat bool `json:"team_chat"`
	// LastSuperAdmin says no other account with a super admin role could
	// sign in: it cannot be disabled, deleted or demoted.
	LastSuperAdmin bool `json:"last_super_admin"`
}

type pageInvite struct {
	ExpiresAt string `json:"expires_at"`
	Expired   bool   `json:"expired"`
}

// profileOf reads the profile names and the preferred language of an
// account.
func profileOf(ctx context.Context, db *store.DB, id int64) (first, last, language string, err error) {
	var f, l, lang sql.NullString
	err = db.QueryRow(ctx, `SELECT first_name, last_name, preferred_language FROM users WHERE id = ?`, id).Scan(&f, &l, &lang)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return strings.TrimSpace(f.String), strings.TrimSpace(l.String), strings.TrimSpace(lang.String), err
}

// displayNames reads the display name of every account, by id.
func displayNames(ctx context.Context, db *store.DB) (map[int64]string, error) {
	rows, err := db.Query(ctx, `SELECT id, username, first_name, last_name FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var username, first, last sql.NullString
		if err := rows.Scan(&id, &username, &first, &last); err != nil {
			return nil, err
		}
		out[id] = people.Name(first.String, last.String, username.String)
	}
	return out, rows.Err()
}

// effectivePermissions lists the permissions the role holds in effect, in
// the order of auth.AllPermissions: all for the super admin roles, the
// role's with its overrides for any other, none of the device permissions
// for pro.
func (h *handlers) effectivePermissions(ctx context.Context, role string) ([]string, error) {
	out := []string{}
	if access.IsSuperAdminRole(role) {
		return append(out, auth.AllPermissions...), nil
	}
	set, err := h.d.Access.EffectivePermissions(ctx, role)
	if err != nil {
		return nil, err
	}
	for _, p := range auth.AllPermissions {
		if set.Has(p) && !access.ProRoleBlocksPermission(role, p) {
			out = append(out, p)
		}
	}
	return out, nil
}

// numericID passes a request on whose {id} is no number of digits: the
// route is not meant for it ("me", "avatars", "invites").
func numericID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" || strings.Trim(id, "0123456789") != "" {
			kit.PassOn(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// pageTarget reads the account of the path: 400 for an id that is no
// positive number, 404 for none. It answers the request itself when not.
func (h *handlers) pageTarget(w http.ResponseWriter, r *http.Request, what string) (*account, bool) {
	id, ok := pathID(r)
	if !ok {
		invalidID(w, r)
		return nil, false
	}
	user, err := accountByID(r.Context(), h.d.Store, id)
	if err != nil {
		h.serverError(w, r, what, err)
		return nil, false
	}
	if user == nil {
		failKey(w, r, http.StatusNotFound, "users.not_found")
		return nil, false
	}
	return user, true
}

// one answers GET /api/users/{id}: the account as the list has it, with
// its profile, its invite, what its role allows and whether it may use
// Team chat.
func (h *handlers) one(w http.ResponseWriter, r *http.Request) {
	user, ok := h.pageTarget(w, r, "Get user error:")
	if !ok {
		return
	}
	out, err := h.pageAccountOf(r, user)
	if err != nil {
		h.serverError(w, r, "Get user error:", err)
		return
	}
	answer(w, r, http.StatusOK, struct {
		Success bool         `json:"success"`
		Data    *pageAccount `json:"data"`
	}{true, out})
}

func (h *handlers) pageAccountOf(r *http.Request, user *account) (*pageAccount, error) {
	ctx := r.Context()
	id := user.ID.V
	listed := h.serialize(ctx, user)
	pending, err := h.life.PendingEmail(ctx, id)
	if err != nil {
		return nil, err
	}
	listed.EmailPending = pendingOf(pending)
	st, err := h.life.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	now := h.now()
	withLife(&listed, st, now)
	facts, err := h.readSignInFacts(r)
	if err != nil {
		return nil, err
	}
	has, version := false, store.NullString{}
	if versions, err := h.d.Avatars.GetVersionMap(ctx); err == nil {
		has, version = versions.Fields(id)
	}
	out := &pageAccount{withPicture: withPicture{listedAccount: listed, HasAvatar: has, AvatarVersion: version, signInState: facts.of(user)}}
	if out.FirstName, out.LastName, out.PreferredLanguage, err = profileOf(ctx, h.d.Store, id); err != nil {
		return nil, err
	}
	out.withPicture.DisplayName = people.Name(out.FirstName, out.LastName, user.username())
	out.TimeZone = people.TimeZone(ctx, h.d.Store, id)
	invites, err := h.links.Invites(ctx)
	if err != nil {
		return nil, err
	}
	if inv, ok := invites[id]; ok && !user.signedIn() {
		out.Invite = &pageInvite{ExpiresAt: inv.ExpiresAt.Format(time.RFC3339), Expired: inv.Expired}
	}
	if out.Permissions, err = h.effectivePermissions(ctx, user.role()); err != nil {
		return nil, err
	}
	for _, p := range out.Permissions {
		if p == auth.PermChatTeam {
			out.TeamChat = !user.Disabled.True()
		}
	}
	if out.LastSuperAdmin, err = isLastSuperAdmin(ctx, h.d.Store, id, user.role(), now); err != nil {
		return nil, err
	}
	return out, nil
}

// ---- The profile of another account ----

// saveProfile answers PUT /api/users/{id}/profile with the body
// {"first_name", "last_name", "time_zone"}: each member given is set,
// the others stay. Names are cut to 200 characters, a time zone must be
// an IANA name the server knows or "" for none. The caller must be
// allowed to change the account (its own, or one whose role the caller
// could give). For an LDAP or single sign-on account the directory or
// the identity provider writes the name again at its next sign-in; the
// change is kept until then. The answer is the account as GET
// /api/users/{id} has it.
func (h *handlers) saveProfile(w http.ResponseWriter, r *http.Request) {
	user, ok := h.pageTarget(w, r, "Save profile error:")
	if !ok {
		return
	}
	if ok, err := h.mayChange(r, user); err != nil {
		h.serverError(w, r, "Save profile error:", err)
		return
	} else if !ok {
		sent(w, r, errRoleAboveOwn)
		return
	}
	body := kit.Body(r)
	names := map[string]string{}
	for _, field := range []string{"first_name", "last_name"} {
		v := member(body, field)
		if v.IsUndefined() {
			continue
		}
		if !v.IsString() && !v.IsNull() {
			fail(w, r, http.StatusBadRequest, field+" must be text")
			return
		}
		text := ""
		if v.IsString() {
			text = strings.TrimSpace(v.Str())
		}
		if runes := []rune(text); len(runes) > 200 {
			text = string(runes[:200])
		}
		names[field] = text
	}
	zone, zoneGiven := "", false
	if v := member(body, "time_zone"); !v.IsUndefined() {
		if !v.IsString() && !v.IsNull() {
			failKey(w, r, http.StatusBadRequest, "users.time_zone_invalid")
			return
		}
		checked, err := people.CheckTimeZone(v.Str())
		if v.IsNull() {
			checked, err = "", nil
		}
		if err != nil {
			failKey(w, r, http.StatusBadRequest, "users.time_zone_invalid")
			return
		}
		zone, zoneGiven = checked, true
	}
	if len(names) == 0 && !zoneGiven {
		fail(w, r, http.StatusBadRequest, "Give first_name, last_name or time_zone")
		return
	}
	ctx := r.Context()
	first, last, _, err := profileOf(ctx, h.d.Store, user.ID.V)
	if err != nil {
		h.serverError(w, r, "Save profile error:", err)
		return
	}
	before := people.Name(first, last, user.username())
	beforeZone := people.TimeZone(ctx, h.d.Store, user.ID.V)
	var changes []string
	if len(names) > 0 {
		if v, ok := names["first_name"]; ok {
			first = v
		}
		if v, ok := names["last_name"]; ok {
			last = v
		}
		if _, err := h.d.Store.Exec(ctx, `UPDATE users SET first_name = ?, last_name = ? WHERE id = ?`, first, last, user.ID.V); err != nil {
			h.serverError(w, r, "Save profile error:", err)
			return
		}
		if after := people.Name(first, last, user.username()); after != before {
			changes = append(changes, "name from "+before+" to "+after)
		}
	}
	if zoneGiven {
		if err := people.SetTimeZone(ctx, h.d.Store, user.ID.V, zone); err != nil {
			h.serverError(w, r, "Save profile error:", err)
			return
		}
		if zone != beforeZone {
			changes = append(changes, "time zone from "+orNone(beforeZone)+" to "+orNone(zone))
		}
	}
	if len(changes) > 0 {
		if err := h.d.LogAccountAction(r, user.ID.V, "user_profile_changed", "Changed the profile of "+user.username()+": "+strings.Join(changes, "; ")); err != nil {
			h.serverError(w, r, "Save profile error:", err)
			return
		}
	}
	out, err := h.pageAccountOf(r, user)
	if err != nil {
		h.serverError(w, r, "Save profile error:", err)
		return
	}
	answer(w, r, http.StatusOK, struct {
		Success bool         `json:"success"`
		Data    *pageAccount `json:"data"`
	}{true, out})
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// ---- Activity ----

// activityEntry is one audit entry by or about the account.
type activityEntry struct {
	ID      store.Int64String `json:"id"`
	Action  string            `json:"action"`
	At      store.Time        `json:"at"`
	Details string            `json:"details"`
	IP      string            `json:"ip"`
	// Actor is the account that acted, Target the account the entry is
	// about; nil for none (a gone account keeps its id and no names).
	Actor  *activityPerson `json:"actor"`
	Target *activityPerson `json:"target"`
}

type activityPerson struct {
	ID          int64  `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

func personOf(id int64, username, full string) *activityPerson {
	if id == 0 {
		return nil
	}
	return &activityPerson{ID: id, Username: username, DisplayName: people.Name(full, "", username)}
}

// activity answers GET /api/users/{id}/activity?limit=: the newest audit
// entries the account wrote (user_id) or that are about it
// (target_user_id), at most limit (50, up to 200). It needs user.view and
// audit.view: the entries are the audit log's.
func (h *handlers) activity(w http.ResponseWriter, r *http.Request) {
	user, ok := h.pageTarget(w, r, "User activity error:")
	if !ok {
		return
	}
	limit := 50
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 {
		limit = min(n, 200)
	}
	id := user.ID.V
	rows, err := store.Select(r.Context(), h.d.Store,
		`SELECT a.id, COALESCE(a.action, ''), a.created_at, COALESCE(a.details, ''), COALESCE(a.ip_address, ''),
		        COALESCE(a.user_id, 0), COALESCE(u.username, ''), `+people.FullNameSQL("u")+`,
		        COALESCE(a.target_user_id, 0), COALESCE(t.username, ''), `+people.FullNameSQL("t")+`
		 FROM audit_log a
		 LEFT JOIN users u ON u.id = a.user_id
		 LEFT JOIN users t ON t.id = a.target_user_id
		 WHERE a.user_id = ? OR a.target_user_id = ?
		 ORDER BY a.id DESC
		 LIMIT ?`, id, id, limit)
	if err != nil {
		h.serverError(w, r, "User activity error:", err)
		return
	}
	defer rows.Close()
	list := []activityEntry{}
	for rows.Next() {
		var e activityEntry
		var actorID, targetID int64
		var actor, actorName, target, targetName string
		if err := rows.Scan(&e.ID, &e.Action, &e.At, &e.Details, &e.IP, &actorID, &actor, &actorName, &targetID, &target, &targetName); err != nil {
			h.serverError(w, r, "User activity error:", err)
			return
		}
		e.Actor, e.Target = personOf(actorID, actor, actorName), personOf(targetID, target, targetName)
		list = append(list, e)
	}
	if err := rows.Err(); err != nil {
		h.serverError(w, r, "User activity error:", err)
		return
	}
	answer(w, r, http.StatusOK, struct {
		Success bool `json:"success"`
		Data    any  `json:"data"`
	}{true, map[string]any{"entries": list}})
}
