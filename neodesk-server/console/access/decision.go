package access

import "strings"

// Answer says how a refused request is answered.
type Answer int

// The kinds of answer a check can ask for.
const (
	// Pass lets the request through.
	Pass Answer = iota
	// AnswerJSON sends Status with Body as JSON.
	AnswerJSON
	// AnswerRedirect sends a 302 to Location.
	AnswerRedirect
	// AnswerLoginRedirect sends a 302 to the sign-in page with the way back
	// to this request and the "expired" notice, as the session package
	// builds it for the request.
	AnswerLoginRedirect
	// AnswerErrorPage sends the console's error page (Page.Kind) with
	// Status. Without a console build, or outside its base, the answer is
	// Page.Text as text/plain with the same status.
	AnswerErrorPage
)

// Refusal is the JSON body of a refused API request.
type Refusal struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Code    string `json:"code,omitempty"`
}

// ErrorPage describes the console error page of a refused page request.
type ErrorPage struct {
	// Kind picks the page: 403 or 500.
	Kind int
	// Message is shown on the page instead of its own words; empty for none.
	Message string
	// Text is the plain-text body used when the page cannot be shown.
	Text string
}

// Decision is the outcome of a check: let the request pass, or the answer to
// send.
type Decision struct {
	Answer   Answer
	Status   int
	Body     *Refusal
	Location string
	Page     ErrorPage
	// DestroySession is set when the session must be ended before answering
	// (accounts with the pro role have no console access).
	DestroySession bool
}

// Passed reports whether the request may go on.
func (d Decision) Passed() bool { return d.Answer == Pass }

// The messages and codes of the refusals.
const (
	MsgLoginRequired        = "Unauthorized. Please log in."
	MsgUnauthorized         = "Unauthorized"
	MsgForbidden            = "Forbidden"
	MsgAdminRequired        = "Admin access required"
	MsgProAccount           = "Pro accounts can only access the RustDesk desktop client API"
	MsgPermissionService    = "Permission service unavailable"
	MsgPolicyUnavailable    = "Panel access policy unavailable"
	MsgPanelSectionDenied   = "You do not have permission to access this panel section"
	msgPermissionDenied     = "Permission denied: "
	msgPanelAccessDenied    = "Panel access denied: "
	CodeSessionRequired     = "session_required"
	CodePermissionService   = "permission_service_unavailable"
	CodePolicyUnavailable   = "panel_access_policy_unavailable"
	CodePanelAccessDenied   = "panel_access_denied"
	loginPath               = "/login"
	statusFound             = 302
	statusUnauthorized      = 401
	statusForbidden         = 403
	statusUnavailable       = 503
	errorPageForbidden      = 403
	errorPageServerError    = 500
	plainTextForbiddenReply = "Forbidden"
)

// MsgPermissionDenied is the error text for a missing permission; several
// names are joined with " or ".
func MsgPermissionDenied(permissions ...string) string {
	return msgPermissionDenied + strings.Join(permissions, " or ")
}

// MsgPanelAccessDenied is the error text of the guard for a section.
func MsgPanelAccessDenied(section string) string {
	return msgPanelAccessDenied + section
}

// IsAPIRequest reports whether a request is answered with JSON: its
// original URL (path and query as sent, before any router stripped a
// prefix) starts with /api/.
func IsAPIRequest(originalURL string) bool {
	return strings.HasPrefix(originalURL, "/api/")
}

func pass() Decision { return Decision{Answer: Pass} }

func refuseJSON(status int, message, code string) Decision {
	return Decision{Answer: AnswerJSON, Status: status, Body: &Refusal{Error: message, Code: code}}
}

func redirect(location string) Decision {
	return Decision{Answer: AnswerRedirect, Status: statusFound, Location: location}
}

// refusePage is the answer to a refused page request: the 403 page in its
// own words, or for another status the server error page with the message.
func refusePage(status int, message string) Decision {
	if status == statusForbidden {
		return Decision{Answer: AnswerErrorPage, Status: status, Page: ErrorPage{Kind: errorPageForbidden, Text: orForbidden(message)}}
	}
	return Decision{Answer: AnswerErrorPage, Status: status, Page: ErrorPage{Kind: errorPageServerError, Message: message, Text: orForbidden(message)}}
}

func orForbidden(message string) string {
	if message == "" {
		return plainTextForbiddenReply
	}
	return message
}

// RequireAuth is the check every console route starts with: a signed-in
// account that is not a pro account.
func (c *Context) RequireAuth(originalURL string) Decision {
	if c.UserID == 0 {
		if IsAPIRequest(originalURL) {
			return refuseJSON(statusUnauthorized, MsgLoginRequired, CodeSessionRequired)
		}
		return Decision{Answer: AnswerLoginRedirect, Status: statusFound}
	}
	if c.HasUser && c.Role == rolePro {
		d := redirect(loginPath)
		if IsAPIRequest(originalURL) {
			d = refuseJSON(statusForbidden, MsgProAccount, "")
		}
		d.DestroySession = true
		return d
	}
	return pass()
}

// RequireRole lets the named role and super admins through.
func (c *Context) RequireRole(role, originalURL string) Decision {
	if c.UserID == 0 {
		return c.unauthorized(originalURL)
	}
	if IsSuperAdminRole(c.Role) || c.Role == role {
		return pass()
	}
	if IsAPIRequest(originalURL) {
		return refuseJSON(statusForbidden, MsgForbidden, "")
	}
	return refusePage(statusForbidden, "")
}

// RequireAdmin lets the four admin roles through: super_admin, admin,
// global_admin and server_admin.
func (c *Context) RequireAdmin(originalURL string) Decision {
	if c.UserID == 0 {
		return c.unauthorized(originalURL)
	}
	if IsAdminRole(c.Role) {
		return pass()
	}
	if IsAPIRequest(originalURL) {
		return refuseJSON(statusForbidden, MsgAdminRequired, "")
	}
	return refusePage(statusForbidden, "")
}

// RequirePermission lets the request through when the account has the
// permission (HasPermission). When the role's permissions could not be
// loaded the answer is a 503, before any other rule is looked at.
func (c *Context) RequirePermission(permission, originalURL string) Decision {
	if c.UserID == 0 {
		return c.unauthorized(originalURL)
	}
	if c.permissionLookupFailed() {
		if IsAPIRequest(originalURL) {
			return refuseJSON(statusUnavailable, MsgPermissionService, CodePermissionService)
		}
		return refusePage(statusUnavailable, MsgPermissionService)
	}
	if !c.HasPermission(permission) {
		if IsAPIRequest(originalURL) {
			return refuseJSON(statusForbidden, MsgPermissionDenied(permission), "")
		}
		return refusePage(statusForbidden, "")
	}
	return pass()
}

// RequireAnyPermission lets the request through when the account has one of
// the permissions. It answers JSON on every path and does not look at the
// session: the routes that use it check the session first.
func (c *Context) RequireAnyPermission(permissions ...string) Decision {
	if c.permissionLookupFailed() {
		return refuseJSON(statusUnavailable, MsgPermissionService, CodePermissionService)
	}
	for _, permission := range permissions {
		if c.HasPermission(permission) {
			return pass()
		}
	}
	return refuseJSON(statusForbidden, MsgPermissionDenied(permissions...), "")
}

// RequireRdClientAuth is RequireAuth for the remote client pages: people
// without a session go to the remote client's own sign-in page, and an
// optional permission is checked. returnQuery is the request's "return"
// query parameter when it was given once as text, else "".
func (c *Context) RequireRdClientAuth(permission, originalURL, returnQuery string) Decision {
	if c.UserID == 0 {
		if IsAPIRequest(originalURL) {
			return refuseJSON(statusUnauthorized, MsgLoginRequired, "")
		}
		return redirect(RdClientLoginRedirect(originalURL, returnQuery))
	}
	if c.HasUser && c.Role == rolePro {
		d := redirect(RdClientLoginRedirect(originalURL, returnQuery))
		if IsAPIRequest(originalURL) {
			d = refuseJSON(statusForbidden, MsgProAccount, "")
		}
		d.DestroySession = true
		return d
	}
	if permission != "" && !c.HasPermission(permission) {
		if IsAPIRequest(originalURL) {
			return refuseJSON(statusForbidden, MsgPermissionDenied(permission), "")
		}
		return refusePage(statusForbidden, "")
	}
	return pass()
}

// unauthorized is the answer of the role and permission checks to a request
// without a session: JSON for the API, the sign-in page without a way back
// for pages.
func (c *Context) unauthorized(originalURL string) Decision {
	if IsAPIRequest(originalURL) {
		return refuseJSON(statusUnauthorized, MsgUnauthorized, "")
	}
	return redirect(loginPath)
}
