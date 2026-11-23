// Package apitokens answers the API token routes: a signed-in admin
// makes, lists and revokes the tokens that scripts, the command line
// (neodesk) and its MCP server use. Package admintoken has the rules.
//
//	GET    /api/panel/api-tokens              the caller's tokens (?all=true: every account's, super admins)
//	POST   /api/panel/api-tokens              make one: {name, permissions, read_only, expires_at | expires_in_days, allow_list}
//	GET    /api/panel/api-tokens/permissions  what a new token may be given: the caller's permissions
//	GET    /api/panel/api-tokens/current      the token the request came with
//	DELETE /api/panel/api-tokens/{id}         revoke (one's own; super admins any)
//
// Making a token needs a browser session: a token never makes another,
// so a stolen token cannot outlive its own revocation.
package apitokens

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/admintoken"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
)

type handlers struct {
	d      *kit.Deps
	tokens *admintoken.Service
	now    func() time.Time
}

// Register adds the group's routes.
func Register(d *kit.Deps) {
	h := &handlers{d: d, tokens: d.AdminTokens, now: time.Now}
	r := d.Router
	r.Get("/api/panel/api-tokens", h.list, d.RequireAuth, h.admin)
	r.Post("/api/panel/api-tokens", h.create, d.RequireAuth, h.admin)
	r.Get("/api/panel/api-tokens/permissions", h.permissions, d.RequireAuth, h.admin)
	r.Get("/api/panel/api-tokens/current", h.current, d.RequireAuth)
	r.Delete("/api/panel/api-tokens/{id}", h.revoke, d.RequireAuth, h.admin)
}

// admin lets the four admin roles through, by the account's role: a
// token of an admin may list and revoke tokens whatever its
// permissions, since neither gives it more.
func (h *handlers) admin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := kit.User(r)
		if user == nil || !access.IsAdminRole(user.Role) {
			kit.Fail(w, r, http.StatusForbidden, access.MsgAdminRequired)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// tokenJSON is a token as the routes answer it.
type tokenJSON struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Hint        string   `json:"hint"`
	UserID      int64    `json:"user_id"`
	Username    string   `json:"username"`
	Permissions []string `json:"permissions"`
	ReadOnly    bool     `json:"read_only"`
	AllowList   []string `json:"allow_list"`
	State       string   `json:"state"`
	CreatedAt   *string  `json:"created_at"`
	ExpiresAt   *string  `json:"expires_at"`
	LastUsedAt  *string  `json:"last_used_at"`
	LastUsedIP  *string  `json:"last_used_ip"`
	RevokedAt   *string  `json:"revoked_at"`
	RevokedBy   *string  `json:"revoked_by"`
}

func timeText(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}

func textOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func (h *handlers) toJSON(t *admintoken.Token) tokenJSON {
	return tokenJSON{
		ID: t.ID, Name: t.Name, Hint: t.Hint(), UserID: t.UserID, Username: t.Username,
		Permissions: t.Permissions, ReadOnly: t.ReadOnly, AllowList: t.AllowList, State: t.State(h.now()),
		CreatedAt: timeText(t.CreatedAt), ExpiresAt: timeText(t.ExpiresAt), LastUsedAt: timeText(t.LastUsedAt),
		LastUsedIP: textOrNil(t.LastUsedIP), RevokedAt: timeText(t.RevokedAt), RevokedBy: textOrNil(t.RevokedBy),
	}
}

type listData struct {
	Tokens []tokenJSON `json:"tokens"`
}

// list answers GET /api/panel/api-tokens: the caller's tokens, newest
// first; with ?all=true a super admin gets every account's.
func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
	user := kit.User(r)
	owner := user.ID
	if r.URL.Query().Get("all") == "true" {
		if !access.IsSuperAdminRole(user.Role) {
			kit.Fail(w, r, http.StatusForbidden, "Only super admins can list every account's tokens")
			return
		}
		owner = 0
	}
	tokens, err := h.tokens.List(r.Context(), owner)
	if err != nil {
		kit.Fail(w, r, http.StatusInternalServerError, "Failed to list the API tokens")
		return
	}
	out := listData{Tokens: make([]tokenJSON, 0, len(tokens))}
	for _, t := range tokens {
		out.Tokens = append(out.Tokens, h.toJSON(t))
	}
	kit.OK(w, r, out)
}

type createInput struct {
	Name          string   `json:"name"`
	Permissions   []string `json:"permissions"`
	ReadOnly      *bool    `json:"read_only"`
	ExpiresAt     string   `json:"expires_at"`
	ExpiresInDays int      `json:"expires_in_days"`
	AllowList     []string `json:"allow_list"`
}

type created struct {
	// Token is the whole token, shown this once.
	Token    string    `json:"token"`
	APIToken tokenJSON `json:"api_token"`
}

type fieldFailure struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Field   string `json:"field,omitempty"`
}

// create answers POST /api/panel/api-tokens. Only a browser session makes
// tokens. read_only is true unless the body says false. The expiry is a
// date (2027-01-31, the end of that day in UTC) or a time
// (RFC 3339), or a number of days from now.
func (h *handlers) create(w http.ResponseWriter, r *http.Request) {
	if admintoken.FromRequest(r) != nil {
		kit.FailCode(w, r, http.StatusForbidden, "API tokens can only be made in the console, signed in with a password", "token_not_allowed")
		return
	}
	var in createInput
	if err := middleware.Bind(r, &in); err != nil {
		kit.JSON(w, r, http.StatusBadRequest, fieldFailure{Error: "The body must be a JSON object with name, permissions and an expiry"})
		return
	}
	expires, err := h.expiry(in)
	if err != nil {
		kit.JSON(w, r, http.StatusBadRequest, fieldFailure{Error: err.Error(), Field: "expires_at"})
		return
	}
	readOnly := true
	if in.ReadOnly != nil {
		readOnly = *in.ReadOnly
	}
	user := kit.User(r)
	ctx := kit.Access(r)
	value, tok, err := h.tokens.Create(r.Context(), admintoken.Creator{ID: user.ID, Username: user.Username, Holds: ctx.HasPermission},
		admintoken.Input{Name: in.Name, Permissions: in.Permissions, ReadOnly: readOnly, ExpiresAt: expires, AllowList: in.AllowList},
		auth.AllPermissions)
	if err != nil {
		if bad, ok := err.(*admintoken.InputError); ok {
			kit.JSON(w, r, http.StatusBadRequest, fieldFailure{Error: bad.Message, Field: bad.Field})
			return
		}
		kit.Fail(w, r, http.StatusInternalServerError, "Failed to make the API token")
		return
	}
	access := "read and write"
	if tok.ReadOnly {
		access = "read only"
	}
	allow := "any address"
	if len(tok.AllowList) > 0 {
		allow = strings.Join(tok.AllowList, ", ")
	}
	_ = h.d.LogAction(r, "api_token_created", "API token "+tok.Name+" ("+tok.Hint()+"): "+access+", "+
		strings.Join(tok.Permissions, " ")+"; expires "+tok.ExpiresAt.Format("2006-01-02")+"; from "+allow)
	kit.JSON(w, r, http.StatusCreated, struct {
		Success bool    `json:"success"`
		Data    created `json:"data"`
	}{true, created{Token: value, APIToken: h.toJSON(tok)}})
}

type expiryError string

func (e expiryError) Error() string { return string(e) }

func (h *handlers) expiry(in createInput) (time.Time, error) {
	text := strings.TrimSpace(in.ExpiresAt)
	switch {
	case text != "" && in.ExpiresInDays != 0:
		return time.Time{}, expiryError("Give either expires_at or expires_in_days, not both.")
	case in.ExpiresInDays != 0:
		if in.ExpiresInDays < 1 || in.ExpiresInDays > 366 {
			return time.Time{}, expiryError("expires_in_days must be between 1 and 366.")
		}
		return h.now().Add(time.Duration(in.ExpiresInDays) * 24 * time.Hour), nil
	case text == "":
		return time.Time{}, nil
	}
	if day, err := time.Parse("2006-01-02", text); err == nil {
		return day.Add(24*time.Hour - time.Second), nil
	}
	if at, err := time.Parse(time.RFC3339, text); err == nil {
		return at, nil
	}
	return time.Time{}, expiryError("The expiry must be a date like 2027-01-31 or a time like 2027-01-31T18:00:00Z.")
}

type permissionsData struct {
	// Permissions are the ones the caller holds, which a new token may get.
	Permissions []string `json:"permissions"`
	// All are every permission there is.
	All []string `json:"all"`
}

// permissions answers GET /api/panel/api-tokens/permissions.
func (h *handlers) permissions(w http.ResponseWriter, r *http.Request) {
	ctx := kit.Access(r)
	held := []string{}
	for _, p := range auth.AllPermissions {
		if ctx.HasPermission(p) {
			held = append(held, p)
		}
	}
	all := append([]string(nil), auth.AllPermissions...)
	sort.Strings(all)
	kit.OK(w, r, permissionsData{Permissions: held, All: all})
}

type account struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

type currentData struct {
	APIToken tokenJSON `json:"api_token"`
	Account  account   `json:"account"`
	// Permissions are what the token may do now: its own permissions that
	// the account still holds.
	Permissions []string `json:"permissions"`
	// Full says the token carries every permission of its account.
	Full bool `json:"full"`
}

// current answers GET /api/panel/api-tokens/current: the token the
// request came with, the account it acts as and what it may do now. A
// browser session has no token: 404.
func (h *handlers) current(w http.ResponseWriter, r *http.Request) {
	use := admintoken.FromRequest(r)
	if use == nil {
		kit.FailCode(w, r, http.StatusNotFound, "This request did not come with an API token", "no_token")
		return
	}
	ctx := kit.Access(r)
	now := []string{}
	for _, p := range use.Token.Permissions {
		if ctx.HasPermission(p) {
			now = append(now, p)
		}
	}
	user := kit.User(r)
	kit.OK(w, r, currentData{
		APIToken:    h.toJSON(use.Token),
		Account:     account{ID: user.ID, Username: user.Username, Role: user.Role},
		Permissions: now,
		Full:        use.Full,
	})
}

// revoke answers DELETE /api/panel/api-tokens/{id}: one's own token, or
// any for a super admin.
func (h *handlers) revoke(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	if parsed, ok := admintoken.Parse(id); ok {
		id = parsed
	}
	id = strings.TrimSuffix(strings.TrimPrefix(id, admintoken.Prefix), "_…")
	user := kit.User(r)
	tok, err := h.tokens.Get(r.Context(), id)
	if err != nil {
		kit.Fail(w, r, http.StatusInternalServerError, "Failed to revoke the API token")
		return
	}
	if tok == nil || (tok.UserID != user.ID && !access.IsSuperAdminRole(user.Role)) {
		kit.Fail(w, r, http.StatusNotFound, "API token not found")
		return
	}
	if !tok.RevokedAt.IsZero() {
		kit.OK(w, r, h.toJSON(tok))
		return
	}
	if _, err := h.tokens.Revoke(r.Context(), tok.ID, user.Username); err != nil {
		kit.Fail(w, r, http.StatusInternalServerError, "Failed to revoke the API token")
		return
	}
	_ = h.d.LogAction(r, "api_token_revoked", "API token "+tok.Name+" ("+tok.Hint()+") of "+tok.Username+" revoked")
	tok, err = h.tokens.Get(r.Context(), tok.ID)
	if err != nil || tok == nil {
		kit.OK(w, r, nil)
		return
	}
	kit.OK(w, r, h.toJSON(tok))
}
