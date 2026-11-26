package phase45

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/branding"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapipath"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/lifecycle"
	"github.com/neodeskapp/neodesk/neodesk-server/console/passwordlinks"
	"github.com/neodeskapp/neodesk/neodesk-server/console/people"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/emailchange"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// orEmpty is the column, or "" when it is empty in JavaScript's eyes.
func orEmpty(c jsbind.Cell) any {
	if !c.Value().Truthy() {
		return ""
	}
	return c
}

// cellText is String(value || ”) of a column.
func cellText(c jsbind.Cell) string {
	if !c.Value().Truthy() {
		return ""
	}
	return c.Value().ToString()
}

type profileAnswer struct {
	Success bool        `json:"success"`
	Profile profileData `json:"profile"`
}

type profileData struct {
	ID          store.Int64String `json:"id"`
	Username    jsbind.Cell       `json:"username"`
	Role        jsbind.Cell       `json:"role"`
	FirstName   any               `json:"first_name"`
	LastName    any               `json:"last_name"`
	Email       any               `json:"email"`
	Phone       any               `json:"phone"`
	RoleDisplay any               `json:"role_display"`
	AvatarURL   any               `json:"avatar_url"`
	// TimeZone is the person's time zone (an IANA name), "" for none.
	TimeZone string `json:"time_zone"`
}

// profile answers GET /api/users/me/profile: the signed-in operator's
// identity, each profile field "" when it is empty.
func (h *handlers) profile(w http.ResponseWriter, r *http.Request) {
	user, err := getUserByID(r.Context(), h.d.Store, kit.UserID(r))
	if err != nil {
		fail(w, r, http.StatusInternalServerError, jsbind.Message(errors.Unwrap(err)))
		return
	}
	if user == nil {
		fail(w, r, http.StatusNotFound, "not_found")
		return
	}
	kit.JSON(w, r, http.StatusOK, profileAnswer{Success: true, Profile: profileData{
		ID: user.ID, Username: user.Username, Role: user.Role,
		FirstName: orEmpty(user.FirstName), LastName: orEmpty(user.LastName), Email: orEmpty(user.Email),
		Phone: orEmpty(user.Phone), RoleDisplay: orEmpty(user.RoleDisplay), AvatarURL: orEmpty(user.AvatarURL),
		TimeZone: people.TimeZone(r.Context(), h.d.Store, kit.UserID(r)),
	}})
}

// saved is the answer of a saved profile: what happened to the picture
// ("unchanged", or the action of the picture sync) and, when the sync
// failed, why.
type saved struct {
	Success     bool   `json:"success"`
	Avatar      string `json:"avatar"`
	AvatarError string `json:"avatar_error,omitempty"`
}

// partlySaved is the answer of a save whose new email address was
// refused while its other fields were saved: the refusal with its code,
// the names of the fields saved, and the picture as in saved when the
// body had one.
type partlySaved struct {
	Success     bool     `json:"success"`
	Error       string   `json:"error"`
	Code        string   `json:"code,omitempty"`
	Saved       []string `json:"saved"`
	Avatar      string   `json:"avatar,omitempty"`
	AvatarError string   `json:"avatar_error,omitempty"`
}

// saveProfile answers PUT /api/users/me/profile. The fields the body has
// are saved, each cut to 200 characters (null as empty); a field the body
// leaves out keeps its value, and a body with none of them changes
// nothing. An http(s) picture address that changed, or whose picture is
// missing, is fetched and stored like a picture from the identity
// provider; a failed fetch does not fail the save. time_zone is an IANA
// name the server knows, or "" or null for none; any other is refused
// with time_zone_invalid before anything is written. Another email
// address waits for its confirmation (package emailchange); when that is
// refused (no email settings, a mail server that doesn't take the mail)
// the answer is the refusal with its status and code, and the other
// fields stay saved: the answer then names them in saved, its text says
// that only the address was not changed (A274), and the picture is
// fetched as after a save.
func (h *handlers) saveProfile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := kit.UserID(r)
	serverError := func(err error) {
		fail(w, r, http.StatusInternalServerError, branding.Message(err))
	}
	email := field(r, "email")
	if branding.Truthy(email) {
		text, err := branding.ToString(email)
		if err != nil {
			serverError(err)
			return
		}
		if utf16Length(text) > 200 {
			fail(w, r, http.StatusBadRequest, "email_too_long")
			return
		}
	}
	// The time zone: an IANA name the server knows, or "" (or null) for none
	zone, zoneGiven := "", false
	if v := field(r, "time_zone"); v != branding.Undefined {
		text, ok := v.(string)
		if v != nil && !ok {
			fail(w, r, http.StatusBadRequest, "time_zone_invalid")
			return
		}
		checked, err := people.CheckTimeZone(text)
		if err != nil {
			fail(w, r, http.StatusBadRequest, "time_zone_invalid")
			return
		}
		zone, zoneGiven = checked, true
	}
	before, err := getUserByID(ctx, h.d.Store, userID)
	if err != nil {
		fail(w, r, http.StatusInternalServerError, jsbind.Message(errors.Unwrap(err)))
		return
	}
	values := map[string]string{}
	for _, name := range profileFields {
		value := field(r, name)
		if value == branding.Undefined {
			continue
		}
		text, err := nullishText(value)
		if err != nil {
			serverError(err)
			return
		}
		values[name] = goapipath.Slice(text, 200)
	}
	// A new address waits for its confirmation like one from Your account
	// (package emailchange); the same address is written as it was
	newEmail, emailGiven := values["email"]
	current := ""
	if before != nil {
		current = cellText(before.Email)
	}
	if emailGiven && newEmail != current {
		delete(values, "email")
	} else {
		emailGiven = false
	}
	if err := updateUserProfile(ctx, h.d.Store, userID, values); err != nil {
		fail(w, r, http.StatusInternalServerError, jsbind.Message(errors.Unwrap(err)))
		return
	}
	if zoneGiven {
		if err := people.SetTimeZone(ctx, h.d.Store, userID, zone); err != nil {
			serverError(err)
			return
		}
	}
	// The other fields are saved: a refused address below leaves them so
	_ = h.d.LogAction(r, "profile_updated", "Updated operator profile")
	var refused *partlySaved
	refusedStatus := 0
	if emailGiven && before != nil {
		if status, code, message := h.changeEmail(r, before, current, newEmail); status != 0 {
			names := savedNames(values, zoneGiven)
			if len(names) == 0 || code == "" {
				kit.JSON(w, r, status, failure{Error: message, Code: code})
				return
			}
			refused = &partlySaved{Error: emailchange.RestSaved(r, message), Code: code, Saved: names}
			refusedStatus = status
		}
	}

	answer := saved{Success: true, Avatar: "unchanged"}
	if avatarURL := field(r, "avatar_url"); avatarURL != branding.Undefined {
		text, _ := nullishText(avatarURL)
		next := goapipath.Slice(branding.Trim(text), 200)
		prev := ""
		if before != nil {
			prev = branding.Trim(cellText(before.AvatarURL))
		}
		missing := next != "" && !h.d.Avatars.GetAvatarVersion(ctx, userID).Valid
		if next != prev || missing {
			result := h.d.Avatars.SyncProfileAvatarURL(ctx, userID, next)
			answer.Avatar, answer.AvatarError = result.Action, result.Code
		}
	}
	if refused != nil {
		if _, given := values["avatar_url"]; given {
			refused.Avatar, refused.AvatarError = answer.Avatar, answer.AvatarError
		}
		kit.JSON(w, r, refusedStatus, refused)
		return
	}
	kit.JSON(w, r, http.StatusOK, answer)
}

// savedNames are the fields a save wrote besides the address, in the
// order of profileFields, and time_zone last when it was given.
func savedNames(values map[string]string, zone bool) []string {
	names := []string{}
	for _, name := range profileFields {
		if _, ok := values[name]; ok {
			names = append(names, name)
		}
	}
	if zone {
		names = append(names, "time_zone")
	}
	return names
}

// nullishText is String(v == null ? ” : v).
func nullishText(v any) (string, error) {
	if v == nil || v == branding.Undefined {
		return "", nil
	}
	return branding.ToString(v)
}

// utf16Length is text.length in JavaScript.
func utf16Length(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

type operatorAnswer struct {
	Success   bool         `json:"success"`
	SessionID string       `json:"session_id"`
	Operator  operatorData `json:"operator"`
}

type operatorData struct {
	DisplayName any `json:"display_name"`
	Username    any `json:"username"`
	RoleDisplay any `json:"role_display"`
	Email       any `json:"email"`
	Phone       any `json:"phone"`
	AvatarURL   any `json:"avatar_url"`
}

// operatorInfo answers GET /api/nd/operator-info?session_id=: who is asking
// for a remote session, for the agent's consent popup. Until sessions are
// brokered it is the signed-in account itself.
func (h *handlers) operatorInfo(w http.ResponseWriter, r *http.Request) {
	sessionID := ""
	if value := goapipath.Query(r, "session_id"); value.Truthy() {
		text, err := value.Text()
		if err != nil {
			fail(w, r, http.StatusInternalServerError, err.Error())
			return
		}
		sessionID = text
	}
	sessionID = goapipath.Slice(sessionID, 128)
	userID := kit.UserID(r)
	if userID == 0 {
		fail(w, r, http.StatusUnauthorized, "unauthorized")
		return
	}
	user, err := getUserByID(r.Context(), h.d.Store, userID)
	if err != nil {
		fail(w, r, http.StatusInternalServerError, jsbind.Message(errors.Unwrap(err)))
		return
	}
	if user == nil {
		fail(w, r, http.StatusNotFound, "not_found")
		return
	}
	var names []string
	for _, c := range []jsbind.Cell{user.FirstName, user.LastName} {
		if c.Value().Truthy() {
			names = append(names, c.Value().ToString())
		}
	}
	var display any = user.Username
	if joined := strings.Join(names, " "); joined != "" {
		display = joined
	}
	var roleDisplay any = user.RoleDisplay
	if !user.RoleDisplay.Value().Truthy() {
		roleDisplay = user.Role
	}
	kit.JSON(w, r, http.StatusOK, operatorAnswer{Success: true, SessionID: sessionID, Operator: operatorData{
		DisplayName: display, Username: user.Username, RoleDisplay: roleDisplay,
		Email: orEmpty(user.Email), Phone: orEmpty(user.Phone), AvatarURL: orEmpty(user.AvatarURL),
	}})
}

// changeEmail asks for the account's address to become next. It answers
// a status, a code (when the refusal has one) and a text when it was
// refused, status 0 otherwise. Without email settings the change is
// refused with emailchange.NeedsMail (A272).
func (h *handlers) changeEmail(r *http.Request, user *userRow, current, next string) (int, string, string) {
	var provider sql.NullString
	_ = h.d.Store.QueryRow(r.Context(), `SELECT auth_provider FROM users WHERE id = ?`, user.ID.V).Scan(&provider)
	changer := &emailchange.Changer{D: h.d, Life: lifecycle.New(h.d.Store, nil), Links: passwordlinks.New(h.d.Store, nil)}
	target := emailchange.Target{ID: user.ID.V, Username: cellText(user.Username), Email: current, Provider: provider.String}
	_, err := changer.Request(r, target, next, true)
	var refusal *emailchange.Refusal
	switch {
	case err == nil:
		return 0, "", ""
	case errors.As(err, &refusal):
		pairs := make([]any, 0, 2*len(refusal.Vars))
		for k, v := range refusal.Vars {
			pairs = append(pairs, k, v)
		}
		return refusal.Status, refusal.Code, kit.T(r, refusal.Key, kit.Vars(pairs...))
	}
	h.d.Log.Error("Profile email change error:", err.Error())
	return http.StatusInternalServerError, "", kit.T(r, "errors.server_error", nil)
}
