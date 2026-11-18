package servermgmt

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"os/user"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/ws"
	"github.com/unitronix/betterdesk-server/logging"
)

// TerminalPath is the address of the terminal socket.
const TerminalPath = "/ws/server-management/terminal"

// terminalPermission is what an account needs for the terminal: the same
// as for the page and its routes.
const terminalPermission = "server.config"

// TerminalOptions configure the terminal. A zero field is the real thing.
type TerminalOptions struct {
	// Audit writes an audit log entry (db.logAction) for the opening and
	// the closing of a terminal. Nil writes none.
	Audit func(ctx context.Context, userID int64, action, details, ip string)
	// SessionCheck is how often an open terminal looks whether its session
	// still exists; when it has ended (signed out, ended elsewhere, past
	// its limits) the socket is closed and the shells end. Default 10
	// seconds.
	SessionCheck time.Duration
	// KillWait is how long a shell has to end after it was asked to (a
	// hang-up for a terminal, a termination for pipes) before it and its
	// process group are killed. Default 3 seconds.
	KillWait time.Duration
	// NoPty starts every shell with pipes, as the old console did without
	// node-pty, and reports no terminal.
	NoPty bool
	// Passwd reads /etc/passwd.
	Passwd func() (string, error)
	// CurrentUser is the console's own account (os.userInfo()).
	CurrentUser func() Account
	// IsRoot reports a console that runs as root.
	IsRoot func() bool
	// Executable reports a regular file the console may execute.
	Executable func(path string) bool
	// Environ is the console's environment, which a shell inherits.
	Environ func() []string
}

// terminalDefaults fills the zero fields of the terminal's options.
func (s *Service) terminalDefaults(t TerminalOptions) TerminalOptions {
	if t.SessionCheck <= 0 {
		t.SessionCheck = 10 * time.Second
	}
	if t.KillWait <= 0 {
		t.KillWait = 3 * time.Second
	}
	if t.Passwd == nil {
		t.Passwd = func() (string, error) {
			b, err := os.ReadFile("/etc/passwd")
			return string(b), err
		}
	}
	if t.CurrentUser == nil {
		t.CurrentUser = s.currentAccount
	}
	if t.IsRoot == nil {
		t.IsRoot = isRoot
	}
	if t.Executable == nil {
		t.Executable = executableFile
	}
	if t.Environ == nil {
		t.Environ = os.Environ
	}
	return t
}

// currentAccount is os.userInfo(): the account the console runs as, with
// its shell from /etc/passwd.
func (s *Service) currentAccount() Account {
	a := Account{UID: -1, GID: -1}
	if u, err := user.Current(); err == nil {
		a.Username, a.Home = u.Username, u.HomeDir
		if uid, err := strconv.Atoi(u.Uid); err == nil {
			a.UID = float64(uid)
		}
		if gid, err := strconv.Atoi(u.Gid); err == nil {
			a.GID = float64(gid)
		}
	}
	if s.windows() {
		return a
	}
	text, _ := s.opts.Terminal.Passwd()
	for _, e := range parsePasswd(text, "") {
		if e.uid == a.UID && (e.username == a.Username || a.Username == "") {
			a.Shell = e.shell
			break
		}
	}
	return a
}

// PtyAvailable reports whether the terminal can give a shell a
// pseudo-terminal on this machine (ptyAvailable, pty_available).
func (s *Service) PtyAvailable() bool {
	if s.opts.Terminal.NoPty {
		return false
	}
	s.ptyOnce.Do(func() { s.ptyOK = ptyWorks() })
	return s.ptyOK
}

// ActiveSessions counts the terminals with a shell (activeSessionCount).
func (s *Service) ActiveSessions() int {
	s.termMu.Lock()
	defer s.termMu.Unlock()
	return len(s.terminals)
}

// Register adds the terminal socket.
func (s *Service) Register(reg *ws.Registry) {
	reg.Handle(TerminalPath, s.serveTerminal)
}

// serveTerminal runs the checks of the old upgrade handler in its order:
// the Origin ("403 Forbidden"), a session with a user ("401
// Unauthorized"), the permission server.config of the session's role with
// the role overrides ("403 Forbidden", also when the lookup fails), then
// the handshake. The session is looked at again every SessionCheck while
// the socket is open.
func (s *Service) serveTerminal(u *ws.Upgrade) {
	if !u.CheckOrigin("server-management-terminal") {
		return
	}
	sess := u.Session()
	if !sess.SignedIn() {
		u.Refuse(http.StatusUnauthorized)
		return
	}
	role := sess.Role()
	username := sess.Username()
	if username == "" {
		username = "user#" + strconv.FormatInt(sess.UserID(), 10)
	}
	allowed, err := u.RoleHasPermission(role, terminalPermission)
	if err != nil || !allowed {
		logging.Logf("console", "WARN: [srv-term] 403 upgrade rejected (user=%s role=%s)", username, role)
		u.Refuse(http.StatusForbidden)
		return
	}
	ip := forwardedAddr(u)
	conn, ok := u.Accept(ws.AcceptOptions{})
	if !ok {
		return
	}
	defer conn.Wait()
	t := &terminal{s: s, conn: conn, userID: sess.UserID(), ip: ip}
	t.run(u, role, username)
}

// forwardedAddr is the address the terminal's audit entries name: the
// first entry of X-Forwarded-For as the client sent it, else the
// connection's address.
func forwardedAddr(u *ws.Upgrade) string {
	value := strings.Join(u.R.Header.Values("X-Forwarded-For"), ", ")
	if value == "" {
		value = u.SocketAddr()
	}
	first, _, _ := strings.Cut(value, ",")
	return jsbind.Trim(first)
}

// terminal is one open terminal socket and the shells started on it.
type terminal struct {
	s      *Service
	conn   *ws.Conn
	id     string
	userID int64
	ip     string

	mu      sync.Mutex
	current *shellProc
	shells  []*shellProc
}

// run reads the socket's messages until it ends, then ends every shell
// and waits for them.
func (t *terminal) run(u *ws.Upgrade, role, username string) {
	s := t.s
	s.termMu.Lock()
	s.nextTerm++
	t.id = "srv-term-" + strconv.FormatInt(s.opts.Now().UnixMilli(), 36) + "-" + strconv.FormatInt(s.nextTerm, 10)
	s.termMu.Unlock()
	t.audit("server_terminal_open", "session="+t.id+" role="+role)
	logging.Logf("console", "[srv-term] session %s opened by %s", t.id, username)

	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		t.watchSession(u)
	}()

	for {
		_, data, err := t.conn.Read()
		if err != nil {
			break
		}
		t.handle(data)
	}
	t.conn.CloseNow()
	<-watchDone

	t.mu.Lock()
	shells := append([]*shellProc(nil), t.shells...)
	t.mu.Unlock()
	for _, p := range shells {
		p.kill(s.opts.Terminal.KillWait)
	}
	for _, p := range shells {
		<-p.ended
	}
	s.termMu.Lock()
	delete(s.terminals, t.id)
	s.termMu.Unlock()
	t.audit("server_terminal_close", "session="+t.id)
	logging.Logf("console", "[srv-term] session %s closed", t.id)
}

// watchSession closes the socket when its session has ended or belongs to
// another user, and returns when the socket has ended.
func (t *terminal) watchSession(u *ws.Upgrade) {
	tick := time.NewTicker(t.s.opts.Terminal.SessionCheck)
	defer tick.Stop()
	for {
		select {
		case <-t.conn.Context().Done():
			return
		case <-tick.C:
			ctx, cancel := context.WithTimeout(t.conn.Context(), 5*time.Second)
			check := *u
			check.R = u.R.WithContext(ctx)
			sess := check.Session()
			cancel()
			if t.conn.Context().Err() != nil {
				return
			}
			if !sess.SignedIn() || sess.UserID() != t.userID {
				t.conn.Close(1008, "Session ended")
				return
			}
		}
	}
}

func (t *terminal) audit(action, details string) {
	if a := t.s.opts.Terminal.Audit; a != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		a(ctx, t.userID, action, details, t.ip)
	}
}

// handle is one message from the browser: JSON text with a type. Before
// "init" only "init" counts; a message that is not a JSON object, or of
// another type, does nothing.
func (t *terminal) handle(data []byte) {
	msg, ok := parseMessage(data)
	if !ok {
		return
	}
	typ, _ := msg["type"].(string)
	t.mu.Lock()
	current := t.current
	t.mu.Unlock()
	if current == nil && typ != "init" {
		return
	}
	switch typ {
	case "init":
		t.init(dimension(msg["cols"], 20, 500, 80), dimension(msg["rows"], 5, 200, 24))
	case "input":
		if text, ok := msg["data"].(string); ok {
			current.write(text)
		}
	case "resize":
		current.resize(dimension(msg["cols"], 20, 500, 80), dimension(msg["rows"], 5, 200, 24))
	case "close":
		// the socket closes first, so nothing the ending shell prints
		// still goes out
		t.conn.Close(1005, "")
		current.kill(t.s.opts.Terminal.KillWait)
	}
}

// init starts a shell and tells the browser about it. A second init starts
// another shell; input then goes to the newest, and all of them end with
// the socket.
func (t *terminal) init(cols, rows int) {
	s := t.s
	account := s.PickTerminalUser()
	p, err := s.startShell(cols, rows, account)
	if err != nil {
		logging.Logf("console", "WARN: [srv-term] session %s: %v", t.id, err)
		t.send(spawnError{Type: "error", Error: "failed_to_spawn_shell"})
		t.conn.Close(1005, "")
		return
	}
	t.mu.Lock()
	t.current = p
	t.shells = append(t.shells, p)
	t.mu.Unlock()
	s.termMu.Lock()
	s.terminals[t.id] = t
	s.termMu.Unlock()
	t.send(ready{
		Type: "ready", SessionID: t.id, Kind: p.kind, Shell: firstSet(account.Shell, s.lastResortShell()),
		Platform: s.Platform(), User: account.Username, Cwd: firstSet(account.Home, s.home()), PtyAvailable: s.PtyAvailable(),
	})
	p.start(func(text string) { t.send(output{Type: "output", Data: text}) }, func(code, signal any) {
		t.send(end{Type: "end", Reason: "exit", Code: code, Signal: signal})
		t.conn.Close(1005, "")
	})
}

func (t *terminal) send(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	t.conn.SendText(string(b))
}

// The messages to the browser, with their keys in the old order.
type ready struct {
	Type         string `json:"type"`
	SessionID    string `json:"session_id"`
	Kind         string `json:"kind"`
	Shell        string `json:"shell"`
	Platform     string `json:"platform"`
	User         string `json:"user"`
	Cwd          string `json:"cwd"`
	PtyAvailable bool   `json:"pty_available"`
}

type output struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

type spawnError struct {
	Type  string `json:"type"`
	Error string `json:"error"`
}

type end struct {
	Type   string `json:"type"`
	Reason string `json:"reason"`
	Code   any    `json:"code"`
	Signal any    `json:"signal"`
}

// parseMessage is JSON.parse of a message's bytes read as UTF-8, when the
// result is an object. A list, a number, text or broken JSON is not.
func parseMessage(data []byte) (map[string]any, bool) {
	text := decodeUTF8(data)
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	if strings.TrimLeft(text[dec.InputOffset():], " \t\r\n") != "" {
		return nil, false
	}
	m, ok := v.(map[string]any)
	return m, ok
}

// dimension is Math.max(low, Math.min(high, parseInt(v, 10) || fallback)):
// the value as text, its leading digits, the fallback for none or 0, kept
// between low and high.
func dimension(v any, low, high, fallback int) int {
	n := parseIntJS(jsString(v))
	if math.IsNaN(n) || n == 0 {
		n = float64(fallback)
	}
	return int(math.Max(float64(low), math.Min(float64(high), n)))
}

// jsString is String(v) of a value JSON.parse made; a missing member is
// undefined.
func jsString(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case bool:
		return strconv.FormatBool(x)
	case json.Number:
		f, _ := strconv.ParseFloat(string(x), 64)
		return jsbind.NumberString(f)
	case string:
		return x
	case []any:
		parts := make([]string, len(x))
		for i, item := range x {
			if item != nil {
				parts[i] = jsString(item)
			}
		}
		return strings.Join(parts, ",")
	case map[string]any:
		return "[object Object]"
	}
	return fmt.Sprint(v)
}
