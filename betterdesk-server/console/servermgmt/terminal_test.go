//go:build unix

package servermgmt

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/ws"
	"github.com/unitronix/betterdesk-server/console/ws/wstest"
)

// fakeSession is a session the test can end or hand to another user.
type fakeSession struct {
	mu       sync.Mutex
	signedIn bool
	userID   int64
	role     string
}

func (f *fakeSession) get() (bool, int64, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.signedIn, f.userID, f.role
}

func (f *fakeSession) set(signedIn bool, userID int64) {
	f.mu.Lock()
	f.signedIn, f.userID = signedIn, userID
	f.mu.Unlock()
}

type sessionView struct {
	signedIn bool
	userID   int64
	role     string
}

func (s sessionView) SignedIn() bool   { return s.signedIn }
func (s sessionView) UserID() int64    { return s.userID }
func (s sessionView) UserRole() string { return s.role }
func (s sessionView) Role() string     { return s.role }
func (s sessionView) Username() string { return "tester" }

// User is the account for a device scope check; the terminal checks
// permissions by role only.
func (s sessionView) User() *session.User { return nil }

// termWorld is the terminal behind a test server: a shell of the test's
// own account (/bin/sh in a folder of the test), a session and a
// permission table the test controls, and the audit entries.
type termWorld struct {
	t       *testing.T
	srv     *httptest.Server
	reg     *ws.Registry
	svc     *Service
	session *fakeSession
	mu      sync.Mutex
	audits  []string
	lookups []string
	// revoked takes server.config from admin, as a role override does;
	// lookupFails makes every lookup fail
	revoked, lookupFails bool
}

// setPermission changes the permission table while sockets are open.
func (w *termWorld) setPermission(revoked, lookupFails bool) {
	w.mu.Lock()
	w.revoked, w.lookupFails = revoked, lookupFails
	w.mu.Unlock()
}

func newTermWorld(t *testing.T, noPty bool, mutate func(*Options)) *termWorld {
	t.Helper()
	w := &termWorld{t: t, session: &fakeSession{signedIn: true, userID: 7, role: "admin"}}
	w.reg = ws.New(ws.Options{
		Session: func(*http.Request) ws.Session {
			in, id, role := w.session.get()
			return sessionView{signedIn: in, userID: id, role: role}
		},
		Permission: func(_ context.Context, role, permission string) (bool, error) {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.lookups = append(w.lookups, role+" "+permission)
			if role == "throws" || w.lookupFails {
				return false, errors.New("lookup failed")
			}
			return role == "admin" && permission == "server.config" && !w.revoked, nil
		},
	})
	home := t.TempDir()
	opts := Options{
		GOOS: "linux",
		Getenv: func(k string) string {
			if k == "SHELL" {
				return ""
			}
			return os.Getenv(k)
		},
		Terminal: TerminalOptions{
			NoPty:        noPty,
			SessionCheck: 30 * time.Millisecond,
			KillWait:     500 * time.Millisecond,
			Passwd:       func() (string, error) { return "", nil },
			IsRoot:       func() bool { return false },
			CurrentUser: func() Account {
				return Account{Username: "tester", UID: float64(os.Getuid()), GID: float64(os.Getgid()), Home: home, Shell: "/bin/sh"}
			},
			Audit: func(_ context.Context, userID int64, action, details, ip string) {
				w.mu.Lock()
				w.audits = append(w.audits, strconv.FormatInt(userID, 10)+" "+action+" "+ip)
				w.mu.Unlock()
			},
		},
	}
	if mutate != nil {
		mutate(&opts)
	}
	w.svc = New(opts)
	w.svc.Register(w.reg)
	w.srv = httptest.NewServer(w.reg)
	t.Cleanup(func() {
		w.srv.Close()
		w.reg.Close()
	})
	return w
}

func (w *termWorld) addr() string { return strings.TrimPrefix(w.srv.URL, "http://") }

// dial opens the terminal with the console's own origin.
func (w *termWorld) dial(extra http.Header) (*wstest.Client, *http.Response) {
	w.t.Helper()
	h := http.Header{"Origin": {"http://" + w.addr()}}
	for k, v := range extra {
		if v[0] == "" {
			h.Del(k)
			continue
		}
		h[k] = v
	}
	c, res, err := wstest.Dial(w.addr(), TerminalPath, h)
	if err != nil {
		w.t.Fatal(err)
	}
	return c, res
}

func (w *termWorld) auditsSoFar() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.audits...)
}

// messages are the JSON messages a client got, without the output.
func messages(c *wstest.Client) []map[string]any {
	var out []map[string]any
	for _, f := range c.Frames() {
		var m map[string]any
		if json.Unmarshal(f.Data, &m) == nil && m["type"] != "output" {
			out = append(out, m)
		}
	}
	return out
}

func outputOf(c *wstest.Client) string {
	var b strings.Builder
	for _, f := range c.Frames() {
		var m map[string]any
		if json.Unmarshal(f.Data, &m) == nil && m["type"] == "output" {
			b.WriteString(m["data"].(string))
		}
	}
	return b.String()
}

func send(t *testing.T, c *wstest.Client, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	if err := c.Text(string(b)); err != nil {
		t.Fatal(err)
	}
}

// shellPID asks the newest shell of c for its process id, which it prints
// after marker.
func shellPID(t *testing.T, c *wstest.Client, marker string) int {
	t.Helper()
	send(t, c, map[string]any{"type": "input", "data": "echo " + marker + "$$\n"})
	return numberAfter(t, c, marker)
}

// numberAfter waits for the output to hold marker followed by digits and
// returns them.
func numberAfter(t *testing.T, c *wstest.Client, marker string) int {
	t.Helper()
	var pid int
	ok := c.Wait(5*time.Second, func([]wstest.Frame, bool) bool {
		out := outputOf(c)
		i := strings.LastIndex(out, marker)
		if i < 0 {
			return false
		}
		digits := out[i+len(marker):]
		end := strings.IndexFunc(digits, func(r rune) bool { return r < '0' || r > '9' })
		if end <= 0 {
			return false
		}
		pid, _ = strconv.Atoi(digits[:end])
		return true
	})
	if !ok {
		t.Fatalf("no process id after %s in %q", marker, outputOf(c))
	}
	return pid
}

func waitReady(t *testing.T, c *wstest.Client, n int) {
	t.Helper()
	if !c.Wait(5*time.Second, func([]wstest.Frame, bool) bool {
		count := 0
		for _, m := range messages(c) {
			if m["type"] == "ready" {
				count++
			}
		}
		return count >= n
	}) {
		t.Fatalf("no ready message: %v", messages(c))
	}
}

// gone waits up to limit for a process to end.
func gone(pid int, limit time.Duration) bool {
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) != nil {
			return true
		}
		if stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil && strings.Contains(string(stat), ") Z") {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

// The checks before the handshake, in their order: the origin, a session
// with a user, then server.config of the session's role (a failed lookup
// refuses too). Only a socket that opens writes audit entries.
func TestTerminalUpgradeChecks(t *testing.T) {
	cases := []struct {
		name     string
		header   http.Header
		signedIn bool
		role     string
		want     int
		lookups  string
	}{
		{"foreign origin", http.Header{"Origin": {"https://evil.example"}}, true, "admin", 403, ""},
		{"no origin with a session cookie", http.Header{"Origin": {""}, "Cookie": {"bd.sid=s%3Ax"}}, true, "admin", 403, ""},
		{"signed out", nil, false, "admin", 401, ""},
		{"signed out, foreign origin", http.Header{"Origin": {"https://evil.example"}}, false, "", 403, ""},
		{"role without server.config", nil, true, "viewer", 403, "viewer server.config"},
		{"lookup fails", nil, true, "throws", 403, "throws server.config"},
		{"no origin, no cookie", http.Header{"Origin": {""}}, true, "admin", 101, "admin server.config"},
		{"allowed", http.Header{"X-Forwarded-For": {" 198.51.100.9 , 10.0.0.1"}}, true, "admin", 101, "admin server.config"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer wstest.Goroutines(t)()
			// no session check while the socket is open: the lookups are
			// the upgrade's only
			w := newTermWorld(t, true, func(o *Options) { o.Terminal.SessionCheck = time.Hour })
			w.session.mu.Lock()
			w.session.signedIn, w.session.role = tc.signedIn, tc.role
			w.session.mu.Unlock()
			c, res := w.dial(tc.header)
			if res.StatusCode != tc.want {
				t.Errorf("status %d, want %d", res.StatusCode, tc.want)
			}
			if c != nil {
				c.Close(1000)
			}
			w.srv.Close()
			w.reg.Close()
			if got := strings.Join(w.lookups, ","); got != tc.lookups {
				t.Errorf("lookups %q, want %q", got, tc.lookups)
			}
			// without a ClientIP the address is the connection's, whatever
			// X-Forwarded-For says
			audits := w.auditsSoFar()
			ip := "127.0.0.1"
			if tc.want == 101 && (len(audits) != 2 || audits[0] != "7 server_terminal_open "+ip || audits[1] != "7 server_terminal_close "+ip) {
				t.Errorf("audits %v", audits)
			}
			if tc.want != 101 && len(audits) != 0 {
				t.Errorf("audits of a refused socket: %v", audits)
			}
		})
	}
}

// The audit entries name the client's address as ClientIP gives it, the
// console's TRUST_PROXY rule: behind a trusted proxy on loopback the last
// address the proxies did not add, not the first X-Forwarded-For entry the
// client wrote.
func TestTerminalAuditAddress(t *testing.T) {
	trust, err := httpx.ParseTrustProxy("loopback")
	if err != nil {
		t.Fatal(err)
	}
	w := newTermWorld(t, true, func(o *Options) { o.Terminal.ClientIP = trust.ClientIP })
	c, res := w.dial(http.Header{"X-Forwarded-For": {" 198.51.100.9 , 10.0.0.1"}})
	if res.StatusCode != 101 {
		t.Fatalf("status %d", res.StatusCode)
	}
	c.Close(1000)
	w.srv.Close()
	w.reg.Close()
	if audits := w.auditsSoFar(); len(audits) != 2 || audits[0] != "7 server_terminal_open 10.0.0.1" || audits[1] != "7 server_terminal_close 10.0.0.1" {
		t.Errorf("audits %v", audits)
	}
}

// Before "init" nothing but "init" counts: no shell starts.
func TestTerminalNeedsInitFirst(t *testing.T) {
	defer wstest.Goroutines(t)()
	w := newTermWorld(t, true, nil)
	c, _ := w.dial(nil)
	send(t, c, map[string]any{"type": "input", "data": "echo hi\n"})
	send(t, c, map[string]any{"type": "resize", "cols": 100, "rows": 30})
	send(t, c, map[string]any{"type": "close"})
	_ = c.Text("[{\"type\":\"init\"}]")
	time.Sleep(300 * time.Millisecond)
	if n := len(c.Frames()); n != 0 || w.svc.ActiveSessions() != 0 {
		t.Errorf("%d messages and %d terminals before init", n, w.svc.ActiveSessions())
	}
	if closed, _, _ := c.Closed(); closed {
		t.Error("a close message before init closed the socket")
	}
	c.Close(1000)
	w.srv.Close()
	w.reg.Close()
}

// A shell ends with its socket: the close message, the client going away
// without a close frame, the end of the session, and the role losing
// server.config (or the lookup failing) all end every shell the socket
// started, the first of a second init too.
func TestTerminalShellsEndWithTheSocket(t *testing.T) {
	for _, how := range []string{"close message", "connection dropped", "session ended", "session of another user", "permission taken", "permission lookup fails", "console stops"} {
		t.Run(how, func(t *testing.T) {
			defer wstest.Goroutines(t)()
			w := newTermWorld(t, true, nil)
			c, _ := w.dial(nil)
			send(t, c, map[string]any{"type": "init"})
			waitReady(t, c, 1)
			first := shellPID(t, c, "FIRST")
			send(t, c, map[string]any{"type": "init"})
			waitReady(t, c, 2)
			second := shellPID(t, c, "SECOND")
			if first == second || w.svc.ActiveSessions() != 1 {
				t.Fatalf("shells %d %d, terminals %d", first, second, w.svc.ActiveSessions())
			}
			switch how {
			case "close message":
				send(t, c, map[string]any{"type": "close"})
			case "connection dropped":
				c.Drop()
			case "session ended":
				w.session.set(false, 7) // the same user, signed out
			case "session of another user":
				w.session.set(true, 8)
			case "permission taken":
				w.setPermission(true, false)
			case "permission lookup fails":
				w.setPermission(false, true)
			case "console stops":
				w.reg.Close()
			}
			if !gone(first, 3*time.Second) || !gone(second, 3*time.Second) {
				t.Errorf("shells still run after %s", how)
			}
			if how == "session ended" || how == "session of another user" || strings.HasPrefix(how, "permission") {
				c.Wait(3*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed })
				if closed, code, reason := c.Closed(); !closed || code != 1008 || reason != "Session ended" {
					t.Errorf("closed %v %d %q", closed, code, reason)
				}
			}
			if how == "close message" {
				c.Wait(3*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed })
				if _, code, _ := c.Closed(); code != 1005 {
					t.Errorf("close code %d", code)
				}
				for _, m := range messages(c) {
					if m["type"] == "end" {
						t.Errorf("an end message after the close message: %v", m)
					}
				}
			}
			c.Drop()
			w.srv.Close()
			w.reg.Close()
			if n := w.svc.ActiveSessions(); n != 0 {
				t.Errorf("%d terminals left", n)
			}
			audits := w.auditsSoFar()
			if len(audits) != 2 || audits[1] != "7 server_terminal_close 127.0.0.1" {
				t.Errorf("audits %v", audits)
			}
		})
	}
}

// The three ways a shell is ended, each on its own: the end of its input
// (an interactive shell ignores SIGTERM), the signal to its process group
// (a background job the shell started), and the kill after the wait (a
// process that ignores both).
func TestTerminalKillLayers(t *testing.T) {
	run := func(t *testing.T, killWait time.Duration, script string) (pid int) {
		w := newTermWorld(t, true, func(o *Options) { o.Terminal.KillWait = killWait })
		c, _ := w.dial(nil)
		send(t, c, map[string]any{"type": "init"})
		waitReady(t, c, 1)
		send(t, c, map[string]any{"type": "input", "data": script})
		pid = shellPID(t, c, "PID")
		c.Drop()
		return pid
	}
	t.Run("input ends", func(t *testing.T) {
		// no kill for an hour: only the closed input can end the shell
		pid := run(t, time.Hour, "")
		if !gone(pid, 3*time.Second) {
			t.Error("the shell still runs after its input ended")
		}
	})
	t.Run("process group", func(t *testing.T) {
		w := newTermWorld(t, true, func(o *Options) { o.Terminal.KillWait = time.Hour })
		c, _ := w.dial(nil)
		send(t, c, map[string]any{"type": "init"})
		waitReady(t, c, 1)
		send(t, c, map[string]any{"type": "input", "data": "sleep 1000 < /dev/null > /dev/null 2>&1 &\n"})
		send(t, c, map[string]any{"type": "input", "data": "echo JOB$!\n"})
		job := numberAfter(t, c, "JOB")
		// until the job runs sleep, it is the shell's child, which still
		// ignores SIGTERM as the interactive shell does
		deadline := time.Now().Add(3 * time.Second)
		for time.Now().Before(deadline) {
			if comm, _ := os.ReadFile("/proc/" + strconv.Itoa(job) + "/comm"); strings.TrimSpace(string(comm)) == "sleep" {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		c.Drop()
		if !gone(job, 3*time.Second) {
			t.Error("a background job of the shell still runs")
		}
	})
	t.Run("kill", func(t *testing.T) {
		// TERM ignored and no input read: only the kill ends it
		start := time.Now()
		w := newTermWorld(t, true, func(o *Options) { o.Terminal.KillWait = 300 * time.Millisecond })
		c, _ := w.dial(nil)
		send(t, c, map[string]any{"type": "init"})
		waitReady(t, c, 1)
		send(t, c, map[string]any{"type": "input", "data": "trap '' TERM HUP; echo STUB$$; exec sleep 1000 < /dev/null\n"})
		pid := numberAfter(t, c, "STUB")
		c.Drop()
		if !gone(pid, 3*time.Second) {
			t.Error("a shell that ignores TERM and its input still runs")
		}
		if time.Since(start) > 10*time.Second {
			t.Error("the kill took too long")
		}
	})
}

// The size of the init and of a resize, kept within 20 to 500 columns and
// 5 to 200 rows: in the shell's COLUMNS and LINES, and on a
// pseudo-terminal in its window size.
func TestTerminalSize(t *testing.T) {
	sizes := []struct {
		cols, rows any
		want       string
	}{{1000, 1, "500 5"}, {19, 4, "20 5"}, {501, 201, "500 200"}, {"abc", nil, "80 24"}}
	for _, noPty := range []bool{true, false} {
		if !noPty && !ptyWorks() {
			continue
		}
		for _, size := range sizes {
			w := newTermWorld(t, noPty, nil)
			c, _ := w.dial(nil)
			send(t, c, map[string]any{"type": "init", "cols": size.cols, "rows": size.rows})
			waitReady(t, c, 1)
			send(t, c, map[string]any{"type": "input", "data": "echo SIZE$COLUMNS $LINES; stty size 2>/dev/null | sed 's/^/TTY/'\n"})
			want := "SIZE" + size.want
			if !c.Wait(5*time.Second, func([]wstest.Frame, bool) bool {
				return strings.Contains(outputOf(c), want+"\r\n") || strings.Contains(outputOf(c), want+"\n")
			}) {
				t.Errorf("pipe %v, %v x %v: %q", noPty, size.cols, size.rows, outputOf(c))
			}
			if !noPty {
				cols, rows, _ := strings.Cut(size.want, " ")
				tty := "TTY" + rows + " " + cols
				if !c.Wait(5*time.Second, func([]wstest.Frame, bool) bool { return strings.Contains(outputOf(c), tty) }) {
					t.Errorf("pty %v x %v: window %q", size.cols, size.rows, outputOf(c))
				}
				send(t, c, map[string]any{"type": "resize", "cols": 9999, "rows": 0})
				send(t, c, map[string]any{"type": "input", "data": "stty size | sed 's/^/NEW/'\n"})
				if !c.Wait(5*time.Second, func([]wstest.Frame, bool) bool { return strings.Contains(outputOf(c), "NEW24 500") }) {
					t.Errorf("after the resize: %q", outputOf(c))
				}
			}
			c.Close(1000)
			w.srv.Close()
			w.reg.Close()
		}
	}
}

// Output is decoded across reads in both modes: a character whose bytes
// come in two reads arrives whole.
func TestTerminalSplitCharacter(t *testing.T) {
	for _, noPty := range []bool{true, false} {
		if !noPty && !ptyWorks() {
			continue
		}
		w := newTermWorld(t, noPty, nil)
		c, _ := w.dial(nil)
		send(t, c, map[string]any{"type": "init"})
		waitReady(t, c, 1)
		// the prompt goes to standard error, a pipe of its own, which may
		// come in between
		send(t, c, map[string]any{"type": "input", "data": "exec 2>/dev/null\nprintf 'A\\303'; sleep 0.3; printf '\\251Z\\n'\n"})
		if !c.Wait(5*time.Second, func([]wstest.Frame, bool) bool { return strings.Contains(outputOf(c), "AéZ") }) || strings.ContainsRune(outputOf(c), '�') {
			t.Errorf("pipe %v: %q", noPty, outputOf(c))
		}
		c.Close(1000)
		w.srv.Close()
		w.reg.Close()
	}
}

// A shell that ends by itself sends end with its code and closes the
// socket; one killed by a signal names it (pipes).
func TestTerminalShellExit(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"exit 3\n", `{"type":"end","reason":"exit","code":3,"signal":null}`},
		{"kill -9 $$\n", `{"type":"end","reason":"exit","code":-1,"signal":"SIGKILL"}`},
	} {
		defer wstest.Goroutines(t)()
		w := newTermWorld(t, true, nil)
		c, _ := w.dial(nil)
		send(t, c, map[string]any{"type": "init"})
		waitReady(t, c, 1)
		send(t, c, map[string]any{"type": "input", "data": tc.input})
		c.Wait(5*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed })
		frames := c.Frames()
		if got := frames[len(frames)-1].Text(); got != tc.want {
			t.Errorf("%q: %s, want %s", tc.input, got, tc.want)
		}
		if _, code, _ := c.Closed(); code != 1005 {
			t.Errorf("close code %d", code)
		}
		w.srv.Close()
		w.reg.Close()
	}
}

// A shell that cannot start (its folder is missing) is reported and the
// socket closes; nothing counts as a terminal.
func TestTerminalSpawnFailure(t *testing.T) {
	defer wstest.Goroutines(t)()
	w := newTermWorld(t, true, func(o *Options) {
		o.Terminal.CurrentUser = func() Account {
			return Account{Username: "tester", UID: float64(os.Getuid()), GID: float64(os.Getgid()), Home: "/nonexistent/sm-home", Shell: "/bin/sh"}
		}
	})
	c, _ := w.dial(nil)
	send(t, c, map[string]any{"type": "init"})
	c.Wait(5*time.Second, func(_ []wstest.Frame, closed bool) bool { return closed })
	if frames := c.Frames(); len(frames) != 1 || frames[0].Text() != `{"type":"error","error":"failed_to_spawn_shell"}` {
		t.Errorf("messages %v", frames)
	}
	if _, code, _ := c.Closed(); code != 1005 || w.svc.ActiveSessions() != 0 {
		t.Errorf("close %d, terminals %d", code, w.svc.ActiveSessions())
	}
	w.srv.Close()
	w.reg.Close()
}

// The shell gets a fixed environment: PATH from the console's, the
// account, the terminal type and its size; PWD only on a pseudo-terminal,
// as node-pty set it. Nothing else of the console's environment (the
// database address, keys) reaches it. On Windows the variables its shells
// need come from the console's environment too, as the console spells
// them.
func TestShellEnv(t *testing.T) {
	getenv := func(k string) string { return map[string]string{"LANG": ""}[k] }
	s := New(Options{GOOS: "linux", Getenv: getenv, Terminal: TerminalOptions{
		Environ: func() []string {
			return []string{"PATH=/bin", "HOME=/root", "SECRET=x", "DB_URL=postgres://bd:pw@db/bd", "TERM=vt100", "PWD=/repo"}
		},
	}})
	a := Account{Username: "node", Home: "/home/node", Shell: "/bin/bash"}
	pipe := strings.Join(s.shellEnv(a, 90, 30, false), " ")
	if pipe != "PATH=/bin HOME=/home/node USER=node LOGNAME=node SHELL=/bin/bash TERM=dumb LANG=en_US.UTF-8 COLUMNS=90 LINES=30" {
		t.Errorf("pipe env %s", pipe)
	}
	pty := strings.Join(s.shellEnv(a, 90, 30, true), " ")
	if pty != "PATH=/bin HOME=/home/node USER=node LOGNAME=node SHELL=/bin/bash TERM=xterm-256color LANG=en_US.UTF-8 COLUMNS=90 LINES=30 PWD=/home/node" {
		t.Errorf("pty env %s", pty)
	}

	win := New(Options{GOOS: "windows", Getenv: getenv, Terminal: TerminalOptions{
		Environ: func() []string {
			return []string{`ComSpec=C:\Windows\system32\cmd.exe`, `Path=C:\Windows`, "SESSION_SECRET=y", `SystemRoot=C:\Windows`,
				"PATHEXT=.COM;.EXE", `TEMP=C:\T`, `TMP=C:\T`, `USERPROFILE=C:\Users\svc`, `APPDATA=C:\Users\svc\AppData`}
		},
	}})
	got := strings.Join(win.shellEnv(Account{Username: "svc", Home: `C:\Users\svc`, Shell: "cmd.exe"}, 80, 24, false), " ")
	want := `Path=C:\Windows ComSpec=C:\Windows\system32\cmd.exe SystemRoot=C:\Windows PATHEXT=.COM;.EXE TEMP=C:\T TMP=C:\T USERPROFILE=C:\Users\svc ` +
		`HOME=C:\Users\svc USER=svc LOGNAME=svc SHELL=cmd.exe TERM=dumb LANG=en_US.UTF-8 COLUMNS=80 LINES=24`
	if got != want {
		t.Errorf("windows env\n got %s\nwant %s", got, want)
	}
}

// Only a console that runs as root switches the shell to another account,
// never to root, and only to ids the system takes.
func TestCredential(t *testing.T) {
	root := true
	s := New(Options{GOOS: "linux", Terminal: TerminalOptions{IsRoot: func() bool { return root }}})
	if c, err := s.credential(Account{UID: 1000, GID: 1001}); err != nil || c == nil || c.Uid != 1000 || c.Gid != 1001 {
		t.Errorf("root to 1000: %+v %v", c, err)
	}
	if c, err := s.credential(Account{UID: 0, GID: 5}); err != nil || c != nil {
		t.Errorf("root to root: %+v %v", c, err)
	}
	for _, bad := range []Account{{UID: -1, GID: 1}, {UID: 1.5, GID: 1}, {UID: 1 << 33, GID: 1}, {UID: 5, GID: -2}} {
		if _, err := s.credential(bad); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	root = false
	if c, err := s.credential(Account{UID: 1000, GID: 1000}); err != nil || c != nil {
		t.Errorf("not root: %+v %v", c, err)
	}
}
