package ws

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/coder/websocket"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/logging"
)

// Upgrade is one upgrade request to a registered socket, before the
// handshake is answered. A handler runs its checks on it in the order
// the old service ran them, then calls Refuse or Accept once.
type Upgrade struct {
	W http.ResponseWriter
	R *http.Request
	// Path is the pathname the socket matched.
	Path string

	query    string
	params   map[string]string
	reg      *Registry
	answered bool
}

// Param is the parameter segment of the path as it was sent: still
// percent-encoded.
func (u *Upgrade) Param(name string) string { return u.params[name] }

// SearchParam is url.searchParams.get(name): the first value of the
// query parameter, decoded; ok is false when there is none (null).
func (u *Upgrade) SearchParam(name string) (value string, ok bool) {
	return searchParam(u.query, name)
}

// Context ends when the console stops.
func (u *Upgrade) Context() context.Context { return u.reg.ctx }

// CheckOrigin is enforceOrigin: it refuses an upgrade from a page the
// origin policy does not allow with "HTTP/1.1 403 Forbidden" and reports
// false. label names the socket in the log line.
func (u *Upgrade) CheckOrigin(label string) bool {
	ok, reason := u.reg.opts.Origins.Allow(u.R)
	if ok {
		return true
	}
	if label == "" {
		label = "unknown"
	}
	logging.Logf("console", "WARN: [WS-ORIGIN] Rejected upgrade (%s) from %s: %s", label, u.SocketAddr(), reason)
	u.Refuse(http.StatusForbidden)
	return false
}

// Session reads the session the cookie names, without renewing it, as
// the session middleware did when a service called it on an upgrade.
// Without a session, or when it cannot be read, nobody is signed in.
func (u *Upgrade) Session() Session {
	if u.reg.opts.Session == nil {
		return storedSession{}
	}
	return u.reg.opts.Session(u.R)
}

// RoleHasPermission is roleHasEffectivePermission: the role's built-in
// permissions with the stored overrides. err is a failed lookup.
func (u *Upgrade) RoleHasPermission(role, permission string) (bool, error) {
	if u.reg.opts.Permission == nil {
		return false, nil
	}
	return u.reg.opts.Permission(u.R.Context(), role, permission)
}

// storedSession is a session of the session store; the zero value has
// nobody signed in.
type storedSession struct {
	s *session.Session
}

func (i storedSession) SignedIn() bool {
	return i.s != nil && i.s.User() != nil
}

func (i storedSession) UserID() int64 {
	if i.s == nil {
		return 0
	}
	return i.s.UserID()
}

func (i storedSession) UserRole() string {
	var user struct {
		Role any `json:"role"`
	}
	if i.s == nil || !i.s.Get("user", &user) {
		return ""
	}
	role, _ := user.Role.(string)
	return role
}

func (i storedSession) Role() string {
	if i.s == nil {
		return ""
	}
	if u := i.s.User(); u != nil {
		return u.Role
	}
	return ""
}

func (i storedSession) User() *session.User {
	if i.s == nil {
		return nil
	}
	return i.s.User()
}

func (i storedSession) Username() string {
	if i.s == nil {
		return ""
	}
	if u := i.s.User(); u != nil {
		return u.Username
	}
	return ""
}

// Refuse answers the upgrade the way the old services did by hand: the
// status line ("HTTP/1.1 401 Unauthorized") and an empty line, then the
// connection is closed.
func (u *Upgrade) Refuse(status int) {
	u.refuse(statusLine(status))
}

// RefuseRaw writes text as the whole answer and closes the connection,
// for a service whose refusal had headers.
func (u *Upgrade) RefuseRaw(text string) {
	u.refuse(refusal(text))
}

func (u *Upgrade) refuse(r refusal) {
	if u.answered {
		return
	}
	u.answered = true
	writeRefusal(u.W, u.R, r)
}

// AcceptOptions configure Accept.
type AcceptOptions struct {
	// MaxPayload is the largest message the socket takes; 0 is the
	// library's default, DefaultMaxPayload.
	MaxPayload int64
}

// Accept answers the handshake the way the websocket library did: its
// checks first (see checkHandshake), with its answer when one fails, then
// "101 Switching Protocols" naming the first subprotocol the client
// offered. ok is false when the upgrade was refused.
func (u *Upgrade) Accept(opts AcceptOptions) (*Conn, bool) {
	if u.answered {
		return nil, false
	}
	u.answered = true
	h, refused := checkHandshake(u.R)
	if refused != "" {
		writeRefusal(u.W, u.R, refused)
		return nil, false
	}
	// The library took version 8 too; the framing is the same.
	u.R.Header.Set("Sec-WebSocket-Version", "13")
	u.R.Header.Set("Sec-WebSocket-Key", strings.TrimSpace(u.R.Header.Get("Sec-WebSocket-Key")))
	accept := &websocket.AcceptOptions{InsecureSkipVerify: true, CompressionMode: websocket.CompressionDisabled}
	if h.protocol != "" {
		accept.Subprotocols = []string{h.protocol}
		u.R.Header.Set("Sec-WebSocket-Protocol", h.protocol)
	}
	c, err := websocket.Accept(u.W, u.R, accept)
	if err != nil {
		return nil, false
	}
	return newConn(u.reg.ctx, c, opts.MaxPayload, h.protocol), true
}

// ClientAddr is the address the old relay services counted connections
// by: the first entry of X-Forwarded-For as the client sent it, else the
// connection's address.
func (u *Upgrade) ClientAddr() string {
	if forwarded := headerText(u.R, "X-Forwarded-For"); forwarded != "" {
		first, _, _ := strings.Cut(forwarded, ",")
		if first = jsbind.Trim(first); first != "" {
			return first
		}
	}
	if addr := u.SocketAddr(); addr != "" {
		return addr
	}
	return "unknown"
}

// SocketAddr is the address of the connection (req.socket.remoteAddress).
func (u *Upgrade) SocketAddr() string {
	host, _, err := net.SplitHostPort(u.R.RemoteAddr)
	if err != nil {
		return u.R.RemoteAddr
	}
	return host
}

// writeRefusal writes the bytes of a refusal on the bare connection and
// closes it. Where the connection cannot be taken over (HTTP/2, a test
// recorder) the status goes out as a plain answer.
func writeRefusal(w http.ResponseWriter, r *http.Request, text refusal) {
	conn, rw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		w.Header().Set("Connection", "close")
		w.WriteHeader(refusalStatus(text))
		_, _ = w.Write([]byte(refusalBody(text)))
		return
	}
	defer conn.Close()
	_, _ = rw.WriteString(string(text))
	_ = rw.Flush()
}

// refusalStatus reads the status code out of a refusal's status line.
func refusalStatus(text refusal) int {
	line, _, _ := strings.Cut(string(text), "\r\n")
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return http.StatusBadRequest
	}
	status := 0
	for _, c := range fields[1] {
		status = status*10 + int(c-'0')
	}
	return status
}

func refusalBody(text refusal) string {
	_, body, _ := strings.Cut(string(text), "\r\n\r\n")
	return body
}
