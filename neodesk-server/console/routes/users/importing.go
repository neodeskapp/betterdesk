package users

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// Importing accounts from a CSV file (importcsv.go reads it), permission
// user.create. POST /api/users/import/preview checks every line and
// changes nothing; POST /api/users/import checks them again and makes the
// accounts of the lines without problems, each as POST /api/users or
// /api/users/invite makes one, with the same role rules. How the new
// accounts get a password is the body's mode:
//
//	invite      an invite by mail sets it (needs the email settings and an
//	            address on every line); the mail is in the line's language
//	temporary   a temporary password each, answered once; it must be
//	            changed at the first sign-in
//	none        a password nobody knows: an admin sends an invite or gives a
//	            temporary password later

// The ways new accounts of an import get a password.
const (
	importInvite    = "invite"
	importTemporary = "temporary"
	importNone      = "none"
)

// importPlan is a checked file.
type importPlan struct {
	mode      string
	mailReady bool
	rows      []importRow
	ignored   []string
	// roles and orgs are what the lines name, as the import writes them
	roles map[int]string
	orgs  map[int]string
}

// planImport reads and checks the body's file: {csv, mode}.
func (h *handlers) planImport(r *http.Request) (*importPlan, error) {
	body := kit.Body(r)
	file := member(body, "csv")
	if !file.IsString() {
		return nil, sayKey(r, http.StatusBadRequest, "users.import_csv_required")
	}
	plan := &importPlan{mode: importInvite, mailReady: h.mailReady(r.Context()), roles: map[int]string{}, orgs: map[int]string{}}
	if mode := member(body, "mode"); !mode.IsUndefined() {
		if !mode.IsString() || (mode.Str() != importInvite && mode.Str() != importTemporary && mode.Str() != importNone) {
			return nil, say(http.StatusBadRequest, "Invalid value for mode")
		}
		plan.mode = mode.Str()
	}
	rows, ignored, err := parseImport(file.Str())
	var problem *csvProblem
	if errors.As(err, &problem) {
		return nil, say(http.StatusBadRequest, kit.T(r, problem.key, kit.Vars(problem.vars...)))
	}
	if err != nil {
		return nil, err
	}
	plan.rows, plan.ignored = rows, ignored
	if plan.ignored == nil {
		plan.ignored = []string{}
	}
	return plan, h.checkImport(r, plan)
}

// importOrg is an organization as the API server lists it.
type importOrg struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

// checkImport notes what is wrong with each line: the fields a new account
// needs, a username or address that another line or account has, a role
// the caller may not give, an organization the caller doesn't see or may
// not add members to, a language the console doesn't have.
func (h *handlers) checkImport(r *http.Request, plan *importPlan) error {
	ctx := r.Context()
	accounts, err := allAccounts(ctx, h.d.Store)
	if err != nil {
		return err
	}
	names, addresses := map[string]bool{}, map[string]bool{}
	for _, a := range accounts {
		names[strings.ToLower(a.username())] = true
		if email := strings.ToLower(addressOf(a)); email != "" {
			addresses[email] = true
		}
	}
	var orgs []importOrg
	orgsRead, orgsFailed := false, false
	mayGive := map[string]bool{}
	nameLine, emailLine := map[string]int{}, map[string]int{}
	problem := func(row *importRow, key string, vars ...any) {
		row.Problems = append(row.Problems, kit.T(r, key, kit.Vars(vars...)))
	}
	for i := range plan.rows {
		row := &plan.rows[i]
		// Username
		name := strings.ToLower(row.Username)
		switch {
		case row.Username == "":
			problem(row, "users.import_username_missing")
		case !usernamePattern.MatchString(row.Username):
			problem(row, "users.invalid_username")
		case names[name]:
			problem(row, "users.import_username_taken")
		case nameLine[name] != 0:
			problem(row, "users.import_username_twice", "line", nameLine[name])
		}
		if row.Username != "" && nameLine[name] == 0 {
			nameLine[name] = row.Line
		}
		// Address
		email := strings.ToLower(row.Email)
		if row.Email == "" {
			if plan.mode == importInvite {
				problem(row, "users.import_email_missing")
			}
		} else if _, err := normalizeEmail(jsbind.String(row.Email)); err != nil {
			problem(row, "users.import_email_invalid")
		} else if addresses[email] {
			problem(row, "users.import_email_taken")
		} else if emailLine[email] != 0 {
			problem(row, "users.import_email_twice", "line", emailLine[email])
		}
		if row.Email != "" && emailLine[email] == 0 {
			emailLine[email] = row.Line
		}
		// Names
		if jsLength(row.FirstName) > maxImportName || jsLength(row.LastName) > maxImportName {
			problem(row, "users.import_name_too_long", "max", maxImportName)
		}
		// Role
		role := importRole(row.Role)
		if !assignableRoles[role] {
			problem(row, "users.import_role_unknown", "role", row.Role)
		} else {
			ok, known := mayGive[role]
			if !known {
				if ok, err = h.canAssignRole(r, role); err != nil {
					return err
				}
				mayGive[role] = ok
			}
			if !ok {
				problem(row, "users.import_role_refused", "role", row.Role)
			}
			plan.roles[row.Line] = role
		}
		// Organization
		if row.Organization != "" {
			if !kit.Access(r).HasPermission(auth.PermOrgManageUsers) {
				problem(row, "users.import_org_permission")
			} else {
				if !orgsRead {
					orgsRead = true
					if orgs, err = h.listOrgs(ctx); err != nil {
						h.log.Warn("[users] import: organizations unreadable:", err.Error())
						orgsFailed = true
					}
				}
				if orgsFailed {
					problem(row, "users.import_org_unreadable")
				} else if id := findOrg(orgs, row.Organization); id == "" {
					problem(row, "users.import_org_unknown", "organization", row.Organization)
				} else {
					plan.orgs[row.Line] = id
				}
			}
		}
		// Language
		if row.Language != "" {
			if code := h.languageCode(row.Language); code == "" {
				problem(row, "users.import_language_unknown", "language", row.Language)
			} else {
				row.Language = code
			}
		}
	}
	return nil
}

// importRole is a role as a line writes it, as the server names it: an
// empty one is viewer, "Super admin" is super_admin.
func importRole(text string) string {
	role := strings.ToLower(strings.TrimSpace(text))
	if role == "" {
		return "viewer"
	}
	return strings.NewReplacer(" ", "_", "-", "_").Replace(role)
}

// listOrgs are the organizations the API server shows the caller.
func (h *handlers) listOrgs(ctx context.Context) ([]importOrg, error) {
	resp, err := h.d.GoAPI.Get(ctx, "/org")
	if err != nil {
		return nil, err
	}
	var out struct {
		Organizations []importOrg `json:"organizations"`
	}
	if err := resp.Decode(&out); err != nil {
		return nil, err
	}
	return out.Organizations, nil
}

// findOrg is the id of the organization a line names by its name, slug or
// id, letter case aside; "" for none.
func findOrg(orgs []importOrg, given string) string {
	for _, o := range orgs {
		if strings.EqualFold(o.Name, given) || strings.EqualFold(o.Slug, given) || o.ID == given {
			return o.ID
		}
	}
	return ""
}

// languageCode is the code of a console language a line names ("de",
// "DE", "zh-tw"), "" for one the console doesn't have.
func (h *handlers) languageCode(given string) string {
	if h.d.I18n == nil || h.d.I18n.Bundle == nil {
		return ""
	}
	given = strings.TrimSpace(given)
	lower := strings.ToLower(given)
	candidates := []string{given, lower}
	if base, region, ok := strings.Cut(strings.ReplaceAll(lower, "_", "-"), "-"); ok {
		candidates = append(candidates, base+"-"+strings.ToUpper(region), base)
	}
	for _, code := range candidates {
		if h.d.I18n.Bundle.Has(code) {
			return code
		}
	}
	return ""
}

// importPreview answers POST /api/users/import/preview with {csv, mode}:
// every line of the file with what is wrong with it, the columns the
// import doesn't know, and whether the console can send invites. Nothing
// is written.
func (h *handlers) importPreview(w http.ResponseWriter, r *http.Request) {
	plan, err := h.planImport(r)
	if err != nil {
		if !sent(w, r, err) {
			h.serverError(w, r, "Import preview error:", err)
		}
		return
	}
	ready := 0
	for _, row := range plan.rows {
		if len(row.Problems) == 0 {
			ready++
		}
	}
	answer(w, r, http.StatusOK, struct {
		Success bool `json:"success"`
		Data    any  `json:"data"`
	}{true, struct {
		Rows           []importRow `json:"rows"`
		Ready          int         `json:"ready"`
		IgnoredColumns []string    `json:"ignored_columns"`
		MailReady      bool        `json:"mail_ready"`
		Mode           string      `json:"mode"`
	}{plan.rows, ready, plan.ignored, plan.mailReady, plan.mode}})
}

// importResult is what happened to one line of an import.
type importResult struct {
	Line     int    `json:"line"`
	Username string `json:"username"`
	// Outcome is created, refused (the line has problems; Error lists
	// them), skipped (not among the lines to import) or failed.
	Outcome    string             `json:"outcome"`
	Error      string             `json:"error,omitempty"`
	ID         *store.Int64String `json:"id,omitempty"`
	InviteSent *bool              `json:"invite_sent,omitempty"`
	// Password is the temporary password, shown this once.
	Password string `json:"password,omitempty"`
	// Warning says what didn't work after the account was made.
	Warning string `json:"warning,omitempty"`
}

// importUsers answers POST /api/users/import with {csv, mode, lines?}:
// the lines of the file without problems become accounts, of them only
// the lines named when lines is given. The answer says for each line of
// the file what happened.
func (h *handlers) importUsers(w http.ResponseWriter, r *http.Request) {
	plan, err := h.planImport(r)
	if err == nil && plan.mode == importInvite && !plan.mailReady {
		err = sayKey(r, http.StatusConflict, "users.invite_needs_email")
	}
	var only map[int]bool
	if err == nil {
		only, err = importLines(r)
	}
	if err != nil {
		if !sent(w, r, err) {
			h.serverError(w, r, "Import error:", err)
		}
		return
	}
	results := make([]importResult, 0, len(plan.rows))
	for _, row := range plan.rows {
		res := importResult{Line: row.Line, Username: row.Username}
		switch {
		case only != nil && !only[row.Line]:
			res.Outcome = "skipped"
		case len(row.Problems) > 0:
			res.Outcome, res.Error = outcomeRefused, strings.Join(row.Problems, " ")
		default:
			h.importRow(r, plan, row, &res)
		}
		results = append(results, res)
	}
	answer(w, r, http.StatusOK, struct {
		Success bool `json:"success"`
		Data    any  `json:"data"`
	}{true, map[string]any{"results": results}})
}

// importLines reads the body's "lines": nil when it is not there.
func importLines(r *http.Request) (map[int]bool, error) {
	given := member(kit.Body(r), "lines")
	if given.IsUndefined() || given.IsNull() {
		return nil, nil
	}
	if !given.IsArray() {
		return nil, say(http.StatusBadRequest, "Invalid value for lines")
	}
	out := map[int]bool{}
	for _, v := range given.Items() {
		n := v.ToNumber()
		if !v.IsNumber() || n != float64(int(n)) || n < 1 {
			return nil, say(http.StatusBadRequest, "Invalid value for lines")
		}
		out[int(n)] = true
	}
	return out, nil
}

// importRow makes the account of a line without problems.
func (h *handlers) importRow(r *http.Request, plan *importPlan, row importRow, res *importResult) {
	ctx := r.Context()
	body := map[string]any{"username": row.Username, "role": plan.roles[row.Line]}
	if row.Email != "" {
		body["email"] = row.Email
	}
	password := ""
	if plan.mode != importInvite {
		var err error
		if password, err = newTemporaryPassword(); err != nil {
			res.Outcome, res.Error = h.failedOne(r, "Import", err)
			return
		}
		body["password"] = password
	}
	raw, err := json.Marshal(body)
	if err != nil {
		res.Outcome, res.Error = h.failedOne(r, "Import", err)
		return
	}
	// The names and the language go in before the invite, which is
	// written in the language
	extra := func(ctx context.Context, id int64) error {
		_, err := h.d.Store.Exec(ctx, `UPDATE users SET first_name = ?, last_name = ?, preferred_language = ? WHERE id = ?`,
			row.FirstName, row.LastName, nullIfEmpty(row.Language), id)
		return err
	}
	created, err := h.createAccount(middleware.WithJSONBody(r, raw), plan.mode == importInvite, extra)
	if err != nil {
		if isUniqueViolation(err) {
			res.Outcome, res.Error = outcomeRefused, kit.T(r, "users.import_username_taken", nil)
			return
		}
		res.Outcome, res.Error = h.outcomeOf(r, "Import", err)
		return
	}
	res.Outcome, res.ID, res.InviteSent = "created", &created.ID, created.InviteSent
	var warnings []string
	if created.InviteSent != nil && !*created.InviteSent {
		warnings = append(warnings, kit.T(r, "users.import_invite_failed", nil))
	}
	if plan.mode == importTemporary {
		if err := h.d.Authn.SetMustChangePassword(ctx, created.ID.V, true); err != nil {
			h.log.Error("Import error:", err)
			warnings = append(warnings, kit.T(r, "errors.server_error", nil))
		} else {
			res.Password = password
		}
	}
	if org := plan.orgs[row.Line]; org != "" {
		_, err := h.d.GoAPI.Post(ctx, "/users/"+accountIDText(created.ID)+"/organizations", goapi.Body(map[string]string{"org_id": org}))
		if err := apiRefusal(err, http.StatusConflict); err != nil && !errors.Is(err, errUnchanged) {
			h.log.Warn("[users] import: organization of "+row.Username+":", err.Error())
			warnings = append(warnings, kit.T(r, "users.import_org_failed", kit.Vars("organization", row.Organization)))
		} else if err == nil {
			if err := h.d.LogAccountAction(r, created.ID.V, "user_org_assigned", "Added user "+row.Username+" to organization "+row.Organization); err != nil {
				h.log.Error("Import error:", err)
			}
		}
	}
	res.Warning = strings.Join(warnings, " ")
}

// nullIfEmpty is nil for "", else the text.
func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
