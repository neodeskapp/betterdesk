package onboard

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/authn"
	"github.com/neodeskapp/neodesk/neodesk-server/console/devicegroups"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	consolemail "github.com/neodeskapp/neodesk/neodesk-server/console/mail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/onboard"
)

const (
	// The lifetimes and uses an invite may have, and what it gets without.
	defaultInviteHours = 7 * 24
	maxInviteHours     = 30 * 24
	defaultInviteUses  = 1
	maxInviteUses      = 1000
	nameMax            = 64
	emailMax           = 254
)

// statusAnswer is GET /api/onboard/status: what the dialogs need to know.
type statusAnswer struct {
	Success bool                 `json:"success"`
	Console consoleAnswer        `json:"console"`
	Mail    bool                 `json:"mail"`
	Release clientreleaseSummary `json:"release"`
}

type consoleAnswer struct {
	Base  string `json:"base"`
	HTTPS bool   `json:"https"`
}

type clientreleaseSummary struct {
	Source   string `json:"source"`
	Tag      string `json:"tag"`
	Verified bool   `json:"verified"`
	Packages int    `json:"packages"`
	Error    string `json:"error"`
}

// status answers GET /api/onboard/status: the console's address for the
// links, whether mail can be sent, and the client release in use.
func (h *handlers) status(w http.ResponseWriter, r *http.Request) {
	c := h.consoleOf(r)
	st := h.releases.Status(r.Context())
	kit.JSON(w, r, http.StatusOK, statusAnswer{
		Success: true,
		Console: consoleAnswer{Base: c.Base(), HTTPS: c.HTTPS},
		Mail:    h.mailReady(r.Context()),
		Release: clientreleaseSummary{Source: st.Source, Tag: st.Tag, Verified: st.Verified, Packages: st.Packages, Error: st.Error},
	})
}

func (h *handlers) mailReady(ctx context.Context) bool {
	return h.d.Mail != nil && h.d.Mail.LoadSMTPConfig(ctx) != nil
}

// invite is a join code as the console lists it.
type invite struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	FolderID   *int64 `json:"folder_id"`
	FolderName string `json:"folder_name"`
	Email      string `json:"email"`
	CreatedBy  string `json:"created_by"`
	CreatedAt  string `json:"created_at"`
	ExpiresAt  string `json:"expires_at"`
	MaxUses    int    `json:"max_uses"`
	UsesLeft   int    `json:"uses_left"`
}

func (h *handlers) inviteOf(ctx context.Context, c onboard.Code) invite {
	inv := invite{
		ID: c.ID, Name: c.Name, Email: c.Email, CreatedBy: c.CreatedBy,
		CreatedAt: c.CreatedAt, ExpiresAt: c.ExpiresAt, MaxUses: c.MaxUses, UsesLeft: c.UsesLeft,
	}
	if c.FolderID > 0 {
		id := c.FolderID
		inv.FolderID = &id
		inv.FolderName = h.folderName(ctx, id)
	}
	return inv
}

func (h *handlers) folderName(ctx context.Context, id int64) string {
	if h.d.Store == nil {
		return ""
	}
	var name sql.NullString
	if err := h.d.Store.QueryRow(ctx, `SELECT name FROM folders WHERE id = ?`, id).Scan(&name); err != nil {
		return ""
	}
	return name.String
}

type invitesAnswer struct {
	Success bool     `json:"success"`
	Invites []invite `json:"invites"`
}

// listInvites answers GET /api/onboard/invites: the open invites of every
// account, newest first.
func (h *handlers) listInvites(w http.ResponseWriter, r *http.Request) {
	codes, err := h.codes.List(r.Context(), onboard.KindJoin)
	if err != nil {
		h.serverError(w, r, "List invites:", err)
		return
	}
	out := invitesAnswer{Success: true, Invites: []invite{}}
	for _, c := range codes {
		out.Invites = append(out.Invites, h.inviteOf(r.Context(), c))
	}
	kit.JSON(w, r, http.StatusOK, out)
}

// codeRequest is the body of a new invite or quick support session.
type codeRequest struct {
	Name          string `json:"name"`
	FolderID      *int64 `json:"folder_id"`
	Email         string `json:"email"`
	LifetimeHours *int64 `json:"lifetime_hours"`
	Uses          *int   `json:"uses"`
	SendEmail     bool   `json:"send_email"`
}

// readCodeRequest reads and checks the body; it answers 400 and returns
// false for a body it cannot take.
func readCodeRequest(w http.ResponseWriter, r *http.Request) (codeRequest, bool) {
	var req codeRequest
	if err := kit.Bind(r, &req); err != nil {
		fail(w, r, http.StatusBadRequest, "invalid_body")
		return req, false
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Email = strings.TrimSpace(req.Email)
	switch {
	case utf8.RuneCountInString(req.Name) > nameMax || strings.ContainsAny(req.Name, "<>\r\n"):
		fail(w, r, http.StatusBadRequest, "invalid_name")
		return req, false
	case req.Email != "" && !validEmail(req.Email):
		fail(w, r, http.StatusBadRequest, "invalid_email")
		return req, false
	case req.SendEmail && req.Email == "":
		fail(w, r, http.StatusBadRequest, "invalid_email")
		return req, false
	case req.FolderID != nil && *req.FolderID < 0:
		fail(w, r, http.StatusBadRequest, "invalid_folder")
		return req, false
	}
	return req, true
}

// validEmail takes one plain address, without a display name.
func validEmail(s string) bool {
	if len(s) > emailMax {
		return false
	}
	addr, err := mail.ParseAddress(s)
	return err == nil && addr.Name == "" && addr.Address == s && strings.Contains(s, "@")
}

// mayUseFolder checks that the folder exists and is open to the account
// for putting devices into it (access.GroupRightsForUser). It answers the
// refusal and returns false.
func (h *handlers) mayUseFolder(w http.ResponseWriter, r *http.Request, folderID int64) bool {
	ctx := r.Context()
	if h.d.DeviceGroups == nil || h.d.Access == nil {
		fail(w, r, http.StatusBadRequest, "invalid_folder")
		return false
	}
	folder, err := h.d.DeviceGroups.FolderByID(ctx, jsbind.Number(float64(folderID)))
	if err != nil {
		h.serverError(w, r, "Look up folder:", err)
		return false
	}
	if folder == nil {
		fail(w, r, http.StatusBadRequest, "invalid_folder")
		return false
	}
	group, err := h.d.Access.DeviceGroupByGUID(ctx, access.FolderGroupGUID(folderID))
	var rights access.GroupRights
	if err == nil {
		rights, err = h.d.Access.GroupRightsForUser(ctx, devicegroups.ScopeUser(kit.User(r)), group)
	}
	if err != nil {
		h.serverError(w, r, "Folder rights:", err)
		return false
	}
	if !rights.Change {
		fail(w, r, http.StatusForbidden, "folder_forbidden")
		return false
	}
	return true
}

// createdAnswer is the answer to a new invite or session. The code is in
// it and nowhere else.
type createdAnswer struct {
	Success     bool            `json:"success"`
	Invite      *invite         `json:"invite,omitempty"`
	Session     *supportSession `json:"session,omitempty"`
	Code        string          `json:"code"`
	DisplayCode string          `json:"display_code"`
	Link        string          `json:"link"`
	DeepLink    string          `json:"deep_link"`
	Mailed      bool            `json:"mailed"`
	MailError   string          `json:"mail_error,omitempty"`
}

// createCodeFor makes a code of the kind for the signed-in account and
// answers the refusal when that does not work.
func (h *handlers) createCodeFor(w http.ResponseWriter, r *http.Request, kind string, req codeRequest, ttl time.Duration, uses int) (string, onboard.Code, bool) {
	folder := int64(0)
	if req.FolderID != nil && *req.FolderID > 0 {
		if !h.mayUseFolder(w, r, *req.FolderID) {
			return "", onboard.Code{}, false
		}
		folder = *req.FolderID
	}
	text, code, err := h.codes.Create(r.Context(), onboard.NewCode{
		Kind: kind, Name: req.Name, FolderID: folder, Email: req.Email,
		TTLSeconds: int64(ttl / time.Second), MaxUses: uses, CreatedByID: kit.UserID(r),
	})
	switch {
	case errors.Is(err, onboard.ErrTooManyCodes):
		fail(w, r, http.StatusConflict, "too_many_codes")
		return "", onboard.Code{}, false
	case err != nil:
		h.log.Error("Create code:", err)
		fail(w, r, http.StatusBadGateway, "server_error")
		return "", onboard.Code{}, false
	}
	return text, code, true
}

// createInvite answers POST /api/onboard/invites with the body {name,
// folder_id, email, lifetime_hours, uses, send_email}.
func (h *handlers) createInvite(w http.ResponseWriter, r *http.Request) {
	req, ok := readCodeRequest(w, r)
	if !ok {
		return
	}
	hours := int64(defaultInviteHours)
	if req.LifetimeHours != nil {
		hours = *req.LifetimeHours
	}
	uses := defaultInviteUses
	if req.Uses != nil {
		uses = *req.Uses
	}
	if hours < 1 || hours > maxInviteHours {
		fail(w, r, http.StatusBadRequest, "invalid_lifetime")
		return
	}
	if uses < 1 || uses > maxInviteUses {
		fail(w, r, http.StatusBadRequest, "invalid_uses")
		return
	}
	text, code, ok := h.createCodeFor(w, r, onboard.KindJoin, req, time.Duration(hours)*time.Hour, uses)
	if !ok {
		return
	}
	details := "Created invite " + code.ID
	if code.Name != "" {
		details += " (" + code.Name + ")"
	}
	h.audit(r, "invite_created", details)

	c := h.consoleOf(r)
	inv := h.inviteOf(r.Context(), code)
	out := createdAnswer{
		Success: true, Invite: &inv, Code: onboard.Normalize(text), DisplayCode: onboard.Display(text),
		Link: c.JoinLink(text), DeepLink: c.DeepLink(onboard.KindJoin, text),
	}
	if req.SendEmail {
		out.Mailed, out.MailError = h.sendCodeMail(r, code, text, req.Email)
	}
	kit.JSON(w, r, http.StatusCreated, out)
}

// audit writes an entry of the console's audit log; a failed write is
// logged.
func (h *handlers) audit(r *http.Request, action, details string) {
	if h.d.Audit == nil {
		return
	}
	if err := h.d.LogAction(r, action, details); err != nil {
		h.log.Warn("Audit entry "+action+" failed:", err)
	}
}

// revokeInvite answers DELETE /api/onboard/invites/{id}.
func (h *handlers) revokeInvite(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if !codeID(id) {
		fail(w, r, http.StatusNotFound, "not_found")
		return
	}
	// Only join codes are invites: a support code is ended through its
	// session.
	codes, err := h.codes.List(r.Context(), onboard.KindJoin)
	if err != nil {
		h.serverError(w, r, "Revoke invite:", err)
		return
	}
	found := false
	for _, c := range codes {
		found = found || c.ID == id
	}
	if !found {
		fail(w, r, http.StatusNotFound, "not_found")
		return
	}
	if _, err := h.codes.Revoke(r.Context(), id); errors.Is(err, onboard.ErrNotFound) {
		fail(w, r, http.StatusNotFound, "not_found")
		return
	} else if err != nil {
		h.serverError(w, r, "Revoke invite:", err)
		return
	}
	h.audit(r, "invite_revoked", "Revoked invite "+id)
	kit.JSON(w, r, http.StatusOK, map[string]bool{"success": true})
}

// codeID is the form of a code's id: 16 lower-case hex digits.
func codeID(id string) bool {
	if len(id) != 16 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// mailRequest is the body of a mail of a code.
type mailRequest struct {
	Code string `json:"code"`
	To   string `json:"to"`
}

// mailInvite answers POST /api/onboard/invites/{id}/mail with the body
// {code, to}: the browser still holds the code, the server only its hash.
func (h *handlers) mailInvite(w http.ResponseWriter, r *http.Request) {
	h.mailCode(w, r, onboard.KindJoin)
}

// mailCode sends the mail of a code whose id is in the path, after
// checking that the code is that open code of the kind.
func (h *handlers) mailCode(w http.ResponseWriter, r *http.Request, kind string) {
	var req mailRequest
	if err := kit.Bind(r, &req); err != nil {
		fail(w, r, http.StatusBadRequest, "invalid_body")
		return
	}
	req.To = strings.TrimSpace(req.To)
	if !validEmail(req.To) {
		fail(w, r, http.StatusBadRequest, "invalid_email")
		return
	}
	code, err := h.codes.Lookup(r.Context(), req.Code)
	if errors.Is(err, onboard.ErrUnusable) || (err == nil && (code.ID != r.PathValue("id") || code.Kind != kind)) {
		unusable(w, r)
		return
	}
	if err != nil {
		h.serverError(w, r, "Look up code:", err)
		return
	}
	if kind == onboard.KindSupport && code.CreatedByID != kit.UserID(r) {
		fail(w, r, http.StatusNotFound, "not_found")
		return
	}
	if !h.mailReady(r.Context()) {
		fail(w, r, http.StatusConflict, "smtp_not_configured")
		return
	}
	if sent, _ := h.sendCodeMail(r, code, req.Code, req.To); !sent {
		fail(w, r, http.StatusBadGateway, "mail_failed")
		return
	}
	kit.JSON(w, r, http.StatusOK, map[string]bool{"success": true, "sent": true})
}

// sendCodeMail mails the link of a code to one address, in the request
// language. It reports whether the mail server took it, and why not.
func (h *handlers) sendCodeMail(r *http.Request, code onboard.Code, text, to string) (bool, string) {
	if !h.mailReady(r.Context()) {
		return false, "smtp_not_configured"
	}
	c := h.consoleOf(r)
	expires := code.ExpiresAt
	if t, err := time.Parse(time.RFC3339, code.ExpiresAt); err == nil {
		expires = t.UTC().Format("2006-01-02 15:04") + " UTC"
	}
	m := onboard.BuildMail(func(key string, vars map[string]any) string {
		return kit.T(r, key, kit.Vars(flatten(vars)...))
	}, onboard.MailFacts{
		Kind: code.Kind, Link: c.Link(code.Kind, text), Code: onboard.Display(text),
		Server: h.serverName(), Sender: h.personName(r.Context(), kit.UserID(r), code.CreatedBy), Host: c.Host, Expires: expires,
	})
	sent, err := h.d.Mail.SendEmail(r.Context(), consolemail.Email{To: []string{to}, Subject: m.Subject, Text: &m.Text, HTML: &m.HTML})
	if err != nil || !sent {
		return false, "mail_failed"
	}
	h.audit(r, "onboard_mail_sent", "Sent the link of "+code.Kind+" code "+code.ID+" by mail")
	return true, ""
}

// flatten turns a map into name, value pairs for kit.Vars.
func flatten(vars map[string]any) []any {
	out := make([]any, 0, 2*len(vars))
	for k, v := range vars {
		out = append(out, k, v)
	}
	return out
}

// approveKey answers POST /api/onboard/devices/{id}/approve-key: the new
// key an enrolled computer brought to an invite becomes its key (a
// reinstall or reset makes a new key). The computer then rejoins with the
// same invite; its old tokens and sessions end.
func (h *handlers) approveKey(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if !authn.IsDeviceID(id) {
		fail(w, r, http.StatusNotFound, onboard.ErrNoKeyRequest.Error())
		return
	}
	actor := ""
	if u := kit.User(r); u != nil {
		actor = u.Username
	}
	if err := h.codes.ApproveKey(r.Context(), id, actor); errors.Is(err, onboard.ErrNoKeyRequest) {
		fail(w, r, http.StatusNotFound, onboard.ErrNoKeyRequest.Error())
		return
	} else if err != nil {
		h.serverError(w, r, "Approve device key:", err)
		return
	}
	h.audit(r, "device_key_approved", "Approved the new key of device "+id)
	kit.JSON(w, r, http.StatusOK, map[string]bool{"success": true})
}
