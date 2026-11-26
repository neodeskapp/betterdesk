package console

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/admintoken"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/lifecycle"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
	"github.com/neodeskapp/neodesk/neodesk-server/logging"
)

// The API tokens in the pipeline (package admintoken has the rules in
// plain words). A request with "Authorization: Bearer ndt_..." skips the
// session step: the token signs it in for this request only, as the
// account that made the token, and its cookies are not read. Step 10 caps
// the account's permissions to the token's, and the CSRF steps leave the
// request alone: a browser cannot send the header to another site
// without asking first.

// tokenRefusal is the answer to a token that is not accepted. It is the
// same for every reason, so the answer tells nothing about the token.
type tokenRefusal struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Code    string `json:"code"`
}

const msgSecrets = "This hands out or replaces the server's secrets: it needs a read-write API token that carries all of its account's permissions"

const msgTokenInvalid = "The API token is not valid: it is unknown, expired or revoked, its account is disabled, or this address may not use it"

// tokenOrSession is step 6: the session, or for a request with an API
// token the token's account.
func (s *Server) tokenOrSession(next http.Handler) http.Handler {
	sessions := s.deps.Sessions.Middleware(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value, isToken := admintoken.FromHeader(r)
		if !isToken || s.tokens == nil {
			sessions.ServeHTTP(w, r)
			return
		}
		ip := kit.ClientIP(r)
		tok, err := s.tokens.Authenticate(r.Context(), value, ip)
		if err != nil {
			s.refuseToken(w, r, err, ip)
			return
		}
		user, err := s.deps.Authn.UserByID(r.Context(), tok.UserID)
		if err != nil {
			logging.Logf("console", "ERROR: API token %s: load its account: %v", tok.ID, err)
			httpx.WriteJSON(w, r, http.StatusServiceUnavailable, tokenRefusal{Error: "The token's account could not be loaded", Code: "token_unavailable"})
			return
		}
		role := ""
		if user != nil {
			role = user.Role.String
		}
		// An account whose end date passed counts as disabled before the
		// job of the users routes disables it
		expired := false
		if user != nil {
			if expired, err = lifecycle.Expired(r.Context(), s.deps.Store, int64(user.ID.V), time.Now()); err != nil {
				expired = true
			}
		}
		if user == nil || user.Disabled.True() || expired || role == "" || auth.IsProRole(role) || auth.IsDeviceRole(role) {
			s.tokens.NoteFailure(ip)
			s.refuseToken(w, r, &admintoken.Refusal{Reason: admintoken.RefusedAccount, Token: tok}, ip)
			return
		}
		if tok.ReadOnly && !safeMethod(r.Method) {
			s.auditRefusal(r, tok, ip, "read-only token, "+r.Method+" "+httpx.RawPath(r))
			httpx.WriteJSON(w, r, http.StatusForbidden, tokenRefusal{Error: "This API token is read-only: it can only read", Code: "token_read_only"})
			return
		}
		if reason := deniedToTokens(r, int64(user.ID.V)); reason != "" {
			s.auditRefusal(r, tok, ip, "never open to tokens, "+r.Method+" "+httpx.RawPath(r))
			httpx.WriteJSON(w, r, http.StatusForbidden, tokenRefusal{Error: reason, Code: "token_not_allowed"})
			return
		}
		s.tokens.Touch(context.WithoutCancel(r.Context()), tok, ip)
		// The command line says when it is older than the server
		if s.cfg.Version != "" {
			w.Header().Set("X-NeoDesk-Version", s.cfg.Version)
		}

		// The token's header and the cookies go: no route after this sees
		// them, so the client API and device routes don't take the token
		// for theirs, and no cookie signs the request in as someone else
		r.Header.Del("Authorization")
		r.Header.Del("Cookie")
		use := &admintoken.Use{Token: tok}
		r = r.WithContext(admintoken.WithUse(r.Context(), use))
		r = s.deps.Sessions.Ephemeral(r, session.User{
			ID: int64(user.ID.V), Username: user.Username, Role: role, PreferredLanguage: user.PreferredLanguage.String,
		})
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		where := r.Method + " " + httpx.RawPath(r)
		if rec.status == http.StatusForbidden {
			// A refusal of what the token or its account may not do: a
			// stolen token that probes shows in the audit log
			s.auditRefusal(r, tok, ip, "403, "+where)
		}
		if safeMethod(r.Method) {
			return
		}
		details := fmt.Sprintf("%s %s -> %d", tok.Hint(), where, rec.status)
		if err := s.deps.Audit.Write(context.WithoutCancel(r.Context()), tok.UserID, "api_token_write", details, ip); err != nil {
			logging.Logf("console", "WARN: audit of an API token write: %v", err)
		}
	})
}

// auditRefusal records that a valid token was refused something, once a
// minute per token.
func (s *Server) auditRefusal(r *http.Request, tok *admintoken.Token, ip, what string) {
	if !s.tokenRefusals.due(tok.ID + " refused") {
		return
	}
	if err := s.deps.Audit.Write(context.WithoutCancel(r.Context()), tok.UserID, "api_token_refused", tok.Hint()+" refused: "+what, ip); err != nil {
		logging.Logf("console", "WARN: audit of an API token refusal: %v", err)
	}
}

// refuseToken answers a token that was not accepted, and records the
// refusal of a token whose row was found (once a minute per token).
func (s *Server) refuseToken(w http.ResponseWriter, r *http.Request, err error, ip string) {
	refusal, ok := err.(*admintoken.Refusal)
	if !ok {
		logging.Logf("console", "ERROR: API token check: %v", err)
		httpx.WriteJSON(w, r, http.StatusServiceUnavailable, tokenRefusal{Error: "API tokens cannot be checked right now", Code: "token_unavailable"})
		return
	}
	if refusal.Token != nil && s.tokenRefusals.due(refusal.Token.ID+refusal.Reason) {
		details := refusal.Token.Hint() + " refused: " + refusal.Reason
		if err := s.deps.Audit.Write(context.WithoutCancel(r.Context()), refusal.Token.UserID, "api_token_refused", details, ip); err != nil {
			logging.Logf("console", "WARN: audit of an API token refusal: %v", err)
		}
	}
	if refusal.Reason == admintoken.RefusedLimited {
		w.Header().Set("Retry-After", strconv.Itoa(int((refusal.RetryAfter+time.Second-1)/time.Second)))
		httpx.WriteJSON(w, r, http.StatusTooManyRequests, tokenRefusal{Error: "Too many requests with API tokens. Wait and try again", Code: "token_rate_limited"})
		return
	}
	w.Header().Set("WWW-Authenticate", `Bearer realm="neodesk", error="invalid_token"`)
	httpx.WriteJSON(w, r, http.StatusUnauthorized, tokenRefusal{Error: msgTokenInvalid, Code: "token_invalid"})
}

func safeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// ownAccount matches the console's routes that change one account by its
// id: /api/users/{id} and below.
var ownAccount = regexp.MustCompile(`^/api/users/([0-9]+)(/.*)?$`)

// deniedToTokens says why a route is never open to an API token, "" when
// it is open: the account's own sign-in, password, second factor,
// sessions and profile, also through the account routes by its id (a
// stolen token must not lock its owner out), and the console's API key in
// full (the key no token rule limits). creator is the token's account.
func deniedToTokens(r *http.Request, creator int64) string {
	path := strings.ToLower(strings.TrimSuffix(httpx.RawPath(r), "/"))
	if m := ownAccount.FindStringSubmatch(path); m != nil && !safeMethod(r.Method) && m[1] == strconv.FormatInt(creator, 10) {
		return "API tokens cannot change their own account: its password, name, role or second factor"
	}
	switch {
	case path == "/api/auth/verify" && safeMethod(r.Method):
		return ""
	case path == "/api/auth" || strings.HasPrefix(path, "/api/auth/") || path == "/logout" || path == "/api/logout" || path == "/api/login":
		return "API tokens cannot sign in or out, or change passwords, second factors or sessions"
	case (path == "/api/users/me" || strings.HasPrefix(path, "/api/users/me/")) && !safeMethod(r.Method):
		return "API tokens cannot change their account's own profile"
	case path == "/api/keys/api" && showsWholeKey(r):
		return "API tokens cannot read the console's API key"
	}
	return ""
}

// showsWholeKey reports whether a request for the API key asks for all of
// it (?show=true, in any spelling the route reads).
func showsWholeKey(r *http.Request) bool {
	for _, v := range r.URL.Query()["show"] {
		if strings.EqualFold(strings.TrimSpace(v), "true") {
			return true
		}
	}
	return false
}

// accountChanges are the writes that change accounts, roles, panel
// access or how people sign in. A token reaches them only when it is
// full: a token with fewer permissions than its account could otherwise
// make an account with more than the token holds and sign in as it.
var accountChanges = regexp.MustCompile(`^(/api/nd)?/api/(users(/.*)?|panel/(user-groups|access|role-permissions)(/.*)?|panel/org/[^/]+/(users|members|invite|invitations)(/.*)?|tenants/[^/]+/users(/.*)?|settings/(ldap|oidc|two-factor|console-sessions|device-scope|client-sessions|enrollment)(/.*)?)$`)

// secretRoutes read or replace the server's secrets: the full backup
// holds the password hashes, the keys and the session secret, a restore
// replaces the accounts, the file browser reaches the key files and the
// database, the advanced settings files hold the session secret and the
// .env, and the backup schedule picks where a full backup is written. A
// token reaches them only when it is full and may write, also to read:
// what they hand out is worth more than any write.
var secretRoutes = regexp.MustCompile(`^/api/(settings/(backup|restore)|settings/backup/(schedule|schedule/run|destination/test)|server-management/files(/.*)?|settings/advanced/files(/.*)?)$`)

// secretRequest reports whether secretRoutes guard the request: every
// method, but for the backup schedule only the writes.
func secretRequest(method, path string) bool {
	if !secretRoutes.MatchString(path) {
		return false
	}
	return !strings.HasPrefix(path, "/api/settings/backup/") || !safeMethod(method)
}

// tokenAccess is the part of step 10 for API tokens: the account's
// permissions capped to the token's, and the account changes only for a
// full token. It answers false when it refused the request.
func (s *Server) tokenAccess(w http.ResponseWriter, r *http.Request, loaded *access.Context) bool {
	use := admintoken.FromRequest(r)
	if use == nil {
		return true
	}
	loaded.LimitToToken(use.Token.Permissions, auth.AllPermissions)
	use.Full = loaded.CapFull
	// server.config reaches the server's files, its sign-in settings and
	// what restarts run: a token has it only together with every other
	// permission of its account
	if !use.Full {
		loaded.DropFromCap(auth.PermServerConfig)
	}
	path := strings.ToLower(strings.TrimSuffix(httpx.RawPath(r), "/"))
	if secretRequest(r.Method, path) && (!use.Full || use.Token.ReadOnly) {
		s.auditRefusal(r, use.Token, kit.ClientIP(r), "the server's secrets, "+r.Method+" "+httpx.RawPath(r))
		httpx.WriteJSON(w, r, http.StatusForbidden, tokenRefusal{Error: msgSecrets, Code: access.CodeTokenNotFull})
		return false
	}
	if !use.Full && !safeMethod(r.Method) && accountChanges.MatchString(path) {
		s.auditRefusal(r, use.Token, kit.ClientIP(r), "not full, "+r.Method+" "+httpx.RawPath(r))
		httpx.WriteJSON(w, r, http.StatusForbidden, tokenRefusal{Error: access.MsgTokenNotFull, Code: access.CodeTokenNotFull})
		return false
	}
	return true
}

// statusRecorder keeps the status of the answer for the audit entry of a
// token's write.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusRecorder) WriteHeader(status int) {
	if !w.wrote && (status < 100 || status > 199) {
		w.status, w.wrote = status, true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusRecorder) Write(p []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(p)
}

func (w *statusRecorder) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap lets http.ResponseController reach the connection.
func (w *statusRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// onceAMinute lets one thing through per key and minute.
type onceAMinute struct {
	now  func() time.Time
	mu   chan struct{}
	last map[string]time.Time
}

func newOnceAMinute() *onceAMinute {
	return &onceAMinute{now: time.Now, mu: make(chan struct{}, 1), last: map[string]time.Time{}}
}

func (o *onceAMinute) due(key string) bool {
	o.mu <- struct{}{}
	defer func() { <-o.mu }()
	now := o.now()
	if last, ok := o.last[key]; ok && now.Sub(last) < time.Minute {
		return false
	}
	if len(o.last) > 10000 {
		o.last = map[string]time.Time{}
	}
	o.last[key] = now
	return true
}
