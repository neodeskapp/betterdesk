package impersonate

import (
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/console/admintoken"
	"github.com/neodeskapp/neodesk/neodesk-server/console/authn"
	"github.com/neodeskapp/neodesk/neodesk-server/console/impersonation"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
)

// The reason's length in characters.
const (
	minReason = 3
	maxReason = 300
)

// cleanReason is the reason as one line: control characters and runs of
// white space become one space, and it is cut at maxReason characters.
func cleanReason(raw string) string {
	var b strings.Builder
	space := false
	for _, c := range raw {
		if unicode.IsSpace(c) || unicode.IsControl(c) {
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteRune(c)
	}
	out := b.String()
	if utf8.RuneCountInString(out) > maxReason {
		out = string([]rune(out)[:maxReason])
	}
	return out
}

// byToken reports whether an API token (or anything else that is no
// browser session) signed the request in: none of them may start an
// impersonation.
func byToken(r *http.Request) bool {
	return admintoken.FromRequest(r) != nil || kit.Session(r).Ephemeral()
}

var refusedToken = &impersonation.Refusal{Code: "impersonation_token", Key: "impersonation.refused_token",
	English: "API tokens and the command line can't sign in as other accounts. Use the console in a browser."}

// proof is how the admin proves to be the admin when starting: the
// password of a local account, the two-factor code when two-factor
// sign-in is on. An account of a directory or single sign-on has no
// console password; it needs two-factor sign-in.
type proof struct {
	Password bool `json:"password"`
	Code     bool `json:"code"`
}

func proofOf(admin *authn.Account) proof {
	return proof{Password: !authn.IsExternalAccount(admin), Code: admin.TOTPEnabled.True()}
}

// checkAnswer is the answer of GET /api/users/{id}/impersonation.
type checkAnswer struct {
	Success bool      `json:"success"`
	Data    checkData `json:"data"`
}

type checkData struct {
	// Allowed says the caller may start; Code and Error say why not.
	Allowed  bool   `json:"allowed"`
	Code     string `json:"code,omitempty"`
	Error    string `json:"error,omitempty"`
	Username string `json:"username"`
	// Needs is what the start asks for.
	Needs proof `json:"needs"`
	// Minutes is how long it lasts, MaxMinutes how long at most.
	Minutes    int `json:"minutes"`
	MaxMinutes int `json:"max_minutes"`
	// NoticeMail says the person gets a mail about it.
	NoticeMail bool `json:"notice_mail"`
}

// check answers GET /api/users/{id}/impersonation: whether the caller may
// view the console as the account, and what the start asks for.
func (h *handlers) check(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		fail(w, r, http.StatusBadRequest, "", "Invalid user ID")
		return
	}
	ctx := r.Context()
	admin, target, done := h.accounts(w, r, id)
	if done {
		return
	}
	data := checkData{Username: target.Username, Needs: proofOf(admin),
		Minutes: int(impersonation.Period.Minutes()), MaxMinutes: int(impersonation.MaxPeriod.Minutes())}
	why, err := h.mayStart(r, admin, target)
	if err != nil {
		h.serverError(w, r, "Impersonation check error:", err)
		return
	}
	if why != nil {
		data.Code, data.Error = why.Code, why.Text(kit.Lang(r), h.d.I18n.Bundle)
	} else {
		data.Allowed = true
	}
	data.NoticeMail = h.svc.NoticeMail(ctx) && h.emailOf(r, target.ID.V) != ""
	kit.JSON(w, r, http.StatusOK, checkAnswer{Success: true, Data: data})
}

// accounts loads the caller's account and the one of the path, and
// answers when either is missing.
func (h *handlers) accounts(w http.ResponseWriter, r *http.Request, id int64) (admin, target *authn.Account, done bool) {
	ctx := r.Context()
	admin, err := h.svc.Account(ctx, kit.ActorID(r))
	if err != nil {
		h.serverError(w, r, "Impersonation error:", err)
		return nil, nil, true
	}
	target, err = h.svc.Account(ctx, id)
	if err != nil {
		h.serverError(w, r, "Impersonation error:", err)
		return nil, nil, true
	}
	if admin == nil || target == nil {
		fail(w, r, http.StatusNotFound, "", kit.T(r, "users.not_found", nil))
		return nil, nil, true
	}
	return admin, target, false
}

// mayStart is MayView, and what the start refuses before it: a token, an
// impersonation already running, an admin of a directory or single
// sign-on without two-factor sign-in.
func (h *handlers) mayStart(r *http.Request, admin, target *authn.Account) (*impersonation.Refusal, error) {
	if byToken(r) {
		return refusedToken, nil
	}
	if kit.Impersonation(r) != nil {
		return &impersonation.Refusal{Code: "impersonation_active", Key: "impersonation.refused_active",
			English: "You are viewing NeoDesk as another account already. Stop that first."}, nil
	}
	if needs := proofOf(admin); !needs.Password && !needs.Code {
		return &impersonation.Refusal{Code: "impersonation_needs_two_factor", Key: "impersonation.refused_needs_two_factor",
			English: "Your account signs in with {provider}, so the console can't check your password. Turn on two-factor sign-in to use Sign in as.",
			Vars:    kit.Vars("provider", providerName(admin.AuthProvider.String))}, nil
	}
	return h.svc.MayView(r.Context(), admin, target)
}

func providerName(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "ldap":
		return "LDAP"
	case "oidc":
		return "single sign-on"
	}
	return provider
}

// startAnswer is the answer of a start.
type startAnswer struct {
	Success bool       `json:"success"`
	Data    statusData `json:"data"`
}

// start answers POST /api/users/{id}/impersonate with {password?, code?,
// reason}: the caller views the console as the account, read-only, for
// impersonation.Period. The password (and the two-factor code) are the
// caller's own; a wrong one is a failed sign-in attempt of the caller at
// the client's address, and the lockout that refuses the caller's
// sign-in there refuses this too. The reason goes into the audit log and
// the mail the person gets.
func (h *handlers) start(w http.ResponseWriter, r *http.Request) {
	if byToken(r) {
		h.refuse(w, r, http.StatusForbidden, refusedToken)
		return
	}
	id, ok := pathID(r)
	if !ok {
		fail(w, r, http.StatusBadRequest, "", "Invalid user ID")
		return
	}
	admin, target, done := h.accounts(w, r, id)
	if done {
		return
	}
	why, err := h.mayStart(r, admin, target)
	if err != nil {
		h.serverError(w, r, "Impersonation start error:", err)
		return
	}
	if why != nil {
		h.refuse(w, r, http.StatusForbidden, why)
		return
	}
	body := kit.Body(r)
	rawReason, _ := body.Get("reason").Str()
	reason := cleanReason(rawReason)
	if utf8.RuneCountInString(reason) < minReason {
		h.failKey(w, r, http.StatusBadRequest, "impersonation_reason_required", "impersonation.reason_required",
			"Say why you want to view NeoDesk as this account. The reason goes into the audit log.")
		return
	}
	if !h.proven(w, r, admin, body.Get("password"), body.Get("code")) {
		return
	}

	now := h.svc.Now()
	imp := session.Impersonation{Reason: reason, StartedAt: now.UnixMilli(), ExpiresAt: now.Add(impersonation.Period).UnixMilli()}
	user := session.User{ID: target.ID.V, Username: target.Username, Role: target.Role.String, PreferredLanguage: target.PreferredLanguage.String}
	if err := h.d.Sessions.Impersonate(w, r, user, imp); err != nil {
		h.serverError(w, r, "Impersonation start error:", err)
		return
	}
	h.svc.Audit(r, admin.ID.V, target.ID.V, impersonation.ActionStarted, "Viewed NeoDesk as "+target.Username+" for: "+reason)
	h.notify(r, admin, target, reason, now)
	imp.AdminID, imp.AdminUsername = admin.ID.V, admin.Username
	kit.JSON(w, r, http.StatusOK, startAnswer{Success: true, Data: statusOf(&imp, target.Username)})
}

// proven checks the admin's password and two-factor code, and answers
// when they don't fit.
func (h *handlers) proven(w http.ResponseWriter, r *http.Request, admin *authn.Account, password, code interface{ Str() (string, bool) }) bool {
	needs := proofOf(admin)
	passwordText, _ := password.Str()
	codeText, _ := code.Str()
	codeText = strings.TrimSpace(codeText)
	if needs.Password && passwordText == "" {
		h.failKey(w, r, http.StatusBadRequest, "impersonation_password_required", "impersonation.password_required", "Enter your password.")
		return false
	}
	if needs.Code && codeText == "" {
		h.failKey(w, r, http.StatusBadRequest, "impersonation_code_required", "impersonation.code_required", "Enter the code from your authenticator app.")
		return false
	}
	ctx := r.Context()
	ip := kit.ClientIP(r)
	lockout, err := h.d.Authn.CheckBruteForce(ctx, admin.Username, ip)
	if err != nil {
		h.serverError(w, r, "Impersonation start error:", err)
		return false
	}
	if lockout.Blocked {
		w.Header().Set("Retry-After", strconv.FormatInt(max(lockout.RetryAfter, 60), 10))
		fail(w, r, http.StatusTooManyRequests, "account_locked", kit.T(r, "auth.too_many_attempts", nil))
		return false
	}
	if needs.Password {
		valid, err := h.d.Authn.VerifyPassword(ctx, passwordText, admin.PasswordHash.String)
		if err != nil {
			h.serverError(w, r, "Impersonation start error:", err)
			return false
		}
		if !valid {
			h.failedAttempt(r, admin)
			// 400, not 401: the admin is still signed in
			h.failKey(w, r, http.StatusBadRequest, "impersonation_wrong_password", "impersonation.wrong_password", "That is not your password.")
			return false
		}
	}
	if needs.Code {
		valid, err := h.d.Authn.VerifyTOTPCode(ctx, admin.ID.V, codeText)
		if err != nil {
			h.serverError(w, r, "Impersonation start error:", err)
			return false
		}
		if !valid {
			h.failedAttempt(r, admin)
			h.failKey(w, r, http.StatusBadRequest, "impersonation_wrong_code", "impersonation.wrong_code", "That code is not right. Enter the current code from your authenticator app.")
			return false
		}
	}
	return true
}

// failedAttempt counts a wrong password or code as a failed sign-in of the
// admin at the client's address.
func (h *handlers) failedAttempt(r *http.Request, admin *authn.Account) {
	if err := h.d.Authn.RecordAttempt(r.Context(), admin.Username, kit.ClientIP(r), false); err != nil {
		h.log.Warn("Recording a failed impersonation attempt failed:", err)
	}
}
