package limits

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/httpx"
)

// clock is a hand-moved clock in milliseconds.
type clock struct{ ms int64 }

func (c *clock) now() time.Time { return time.UnixMilli(c.ms) }

func ok(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }

func request(method, target, ip string) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	r.RemoteAddr = ip + ":40000"
	return r
}

func hit(h http.Handler, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

func newSet(c *clock, env map[string]string) *Set {
	return NewSet(Options{
		ClientIP: httpx.SocketIP,
		UserKey:  func(r *http.Request) string { return r.Header.Get("X-User") },
		Getenv:   func(name string) string { return env[name] },
		Now:      c.now,
	})
}

// The sign-in limiter with its defaults, as seen on the wire
// (spec/auth-sessions.md 7.1).
func TestLoginLimiterHeadersAndAnswer(t *testing.T) {
	c := &clock{ms: 1750000000000}
	h := newSet(c, nil).Login.Middleware(http.HandlerFunc(ok))
	for i := 1; i <= 5; i++ {
		rec := hit(h, request("POST", "/api/auth/login", "203.0.113.1"))
		want := map[string]string{"RateLimit-Policy": "5;w=60", "RateLimit-Limit": "5", "RateLimit-Remaining": fmt.Sprint(5 - i), "RateLimit-Reset": "60"}
		if rec.Code != 200 {
			t.Fatalf("request %d: status %d", i, rec.Code)
		}
		for name, value := range want {
			if rec.Header().Get(name) != value {
				t.Errorf("request %d: %s = %q, want %q", i, name, rec.Header().Get(name), value)
			}
		}
		if rec.Header().Get("Retry-After") != "" || rec.Header().Get("X-RateLimit-Limit") != "" {
			t.Errorf("request %d: Retry-After %q, X-RateLimit-Limit %q", i, rec.Header().Get("Retry-After"), rec.Header().Get("X-RateLimit-Limit"))
		}
	}
	rec := hit(h, request("POST", "/api/auth/login", "203.0.113.1"))
	if rec.Code != 429 {
		t.Fatalf("request 6: status %d, want 429", rec.Code)
	}
	for name, value := range map[string]string{"RateLimit-Policy": "5;w=60", "RateLimit-Limit": "5", "RateLimit-Remaining": "0", "RateLimit-Reset": "60",
		"Retry-After": "60", "Content-Type": "application/json; charset=utf-8"} {
		if rec.Header().Get(name) != value {
			t.Errorf("request 6: %s = %q, want %q", name, rec.Header().Get(name), value)
		}
	}
	if rec.Body.String() != `{"success":false,"error":"Too many login attempts. Please try again in a minute."}` {
		t.Errorf("request 6: body %s", rec.Body.String())
	}
}

func TestEveryLimiterHasItsOwnAnswer(t *testing.T) {
	c := &clock{}
	set := newSet(c, map[string]string{"RATE_LIMIT_MAX": "1", "LOGIN_RATE_LIMIT_MAX": "1", "PANEL_READ_RATE_LIMIT_MAX": "1", "WIDGET_RATE_LIMIT_MAX": "1",
		"PANEL_PREFERENCE_RATE_LIMIT_MAX": "1", "RDCLIENT_PAGE_RATE_LIMIT_MAX": "1", "UPLOAD_RATE_LIMIT_MAX": "1", "FILE_ACCESS_RATE_LIMIT_MAX": "1", "BD_REGISTER_RATE_LIMIT_MAX": "1"})
	want := map[string]string{
		"apiLimiter":             "Too many requests. Please try again later.",
		"sessionReadLimiter":     "Too many requests. Please slow down.",
		"widgetLimiter":          "Too many widget requests. Please slow down.",
		"panelReadLimiter":       "Too many panel read requests. Please slow down.",
		"panelPreferenceLimiter": "Too many layout save requests. Please slow down.",
		"rdClientPageLimiter":    "Too many remote page requests. Please slow down.",
		"loginLimiter":           "Too many login attempts. Please try again in a minute.",
		"totpVerifyLimiter":      "Too many verification attempts. Please try again in a minute.",
		"passwordChangeLimiter":  "Too many password change attempts. Please try again later.",
		"uploadLimiter":          "Too many upload requests. Please try again later.",
		"fileAccessLimiter":      "Too many file requests. Please try again later.",
		"registerLimiter":        "Too many registration requests. Please slow down.",
	}
	if len(set.All()) != len(want) {
		t.Fatalf("%d limiters, want %d", len(set.All()), len(want))
	}
	for _, l := range set.All() {
		h := l.Middleware(http.HandlerFunc(ok))
		// a request each limiter counts: a signed-in read for SessionRead, a write for the rest
		newRequest := func() *http.Request {
			r := request("POST", "/x", "203.0.113.1")
			if l == set.SessionRead {
				r = request("GET", "/x", "203.0.113.1")
				r.Header.Set("X-User", "7")
			}
			return r
		}
		var last *httptest.ResponseRecorder
		for i := int64(0); i <= l.Max(); i++ {
			last = hit(h, newRequest())
		}
		if last.Code != 429 || last.Body.String() != `{"success":false,"error":"`+want[l.Name()]+`"}` {
			t.Errorf("%s: %d %s", l.Name(), last.Code, last.Body.String())
		}
	}
}

func counter(c *clock, windowMs, maximum int64) (*Limiter, http.Handler) {
	l := New(Config{Name: "test", WindowMs: windowMs, Max: maximum, Message: message{Error: "slow down"},
		Key: func(r *http.Request) string { return httpx.SocketIP(r) }, Now: c.now})
	return l, l.Middleware(http.HandlerFunc(ok))
}

func TestFixedWindow(t *testing.T) {
	c := &clock{ms: 1000}
	_, h := counter(c, 10_000, 2)
	at := func(ms int64) *httptest.ResponseRecorder {
		c.ms = ms
		return hit(h, request("GET", "/x", "203.0.113.1"))
	}
	check := func(rec *httptest.ResponseRecorder, status int, remaining, reset, retry string) {
		t.Helper()
		if rec.Code != status || rec.Header().Get("RateLimit-Remaining") != remaining || rec.Header().Get("RateLimit-Reset") != reset || rec.Header().Get("Retry-After") != retry {
			t.Errorf("at %d: %d remaining=%q reset=%q retry=%q; want %d %q %q %q", c.ms, rec.Code, rec.Header().Get("RateLimit-Remaining"),
				rec.Header().Get("RateLimit-Reset"), rec.Header().Get("Retry-After"), status, remaining, reset, retry)
		}
	}
	// The first request starts the window: it ends at 11000
	check(at(1000), 200, "1", "10", "")
	// Exactly the maximum passes
	check(at(4001), 200, "0", "7", "")
	check(at(4001), 429, "0", "7", "7")
	// A refused request counts, and does not move the window's end
	check(at(10_000), 429, "0", "1", "1")
	check(at(10_999), 429, "0", "1", "1")
	// At the window's end a new one starts with this request
	check(at(11_000), 200, "1", "10", "")
	check(at(20_999), 200, "0", "1", "")
	check(at(20_999), 429, "0", "1", "1")
	// A window starts with the first request after the old one ended, not on a grid
	check(at(35_500), 200, "1", "10", "")
	check(at(45_499), 200, "0", "1", "")
	check(at(45_500), 200, "1", "10", "")
}

func TestKeysCountApart(t *testing.T) {
	c := &clock{}
	_, h := counter(c, 60_000, 1)
	if hit(h, request("GET", "/x", "203.0.113.1")).Code != 200 || hit(h, request("GET", "/x", "203.0.113.1")).Code != 429 {
		t.Fatal("the second request of an address must be refused")
	}
	if hit(h, request("GET", "/x", "203.0.113.2")).Code != 200 {
		t.Error("another address has its own count")
	}
}

func TestSkippedRequestsAreNotCounted(t *testing.T) {
	c := &clock{}
	l := New(Config{Name: "test", WindowMs: 60_000, Max: 1, Message: message{}, Key: httpx.SocketIP, Now: c.now,
		Skip: func(r *http.Request, _ string) bool { return r.Method == http.MethodGet }})
	h := l.Middleware(http.HandlerFunc(ok))
	for i := 0; i < 5; i++ {
		rec := hit(h, request("GET", "/x", "203.0.113.1"))
		if rec.Code != 200 || rec.Header().Get("RateLimit-Limit") != "" {
			t.Fatalf("a skipped request: %d, RateLimit-Limit %q", rec.Code, rec.Header().Get("RateLimit-Limit"))
		}
	}
	if hit(h, request("POST", "/x", "203.0.113.1")).Code != 200 {
		t.Error("the skipped requests used up the budget")
	}
	if hit(h, request("POST", "/x", "203.0.113.1")).Code != 429 {
		t.Error("the second counted request must be refused")
	}
}

func TestWindowAndResetRounding(t *testing.T) {
	for _, c := range []struct {
		windowMs      int64
		policy, reset string
	}{
		{60_000, "3;w=60", "60"},
		{1500, "3;w=2", "2"},
		{999, "3;w=1", "1"},
		{1, "3;w=1", "1"},
		{-500, "3;w=0", ""},
		{-1500, "3;w=-1", ""},
	} {
		clk := &clock{ms: 5000}
		_, h := counter(clk, c.windowMs, 3)
		rec := hit(h, request("GET", "/x", "203.0.113.1"))
		if rec.Header().Get("RateLimit-Policy") != c.policy || rec.Header().Get("RateLimit-Reset") != c.reset {
			t.Errorf("window %d: policy %q reset %q, want %q %q", c.windowMs, rec.Header().Get("RateLimit-Policy"), rec.Header().Get("RateLimit-Reset"), c.policy, c.reset)
		}
	}
	// A window that ends before it starts never refuses
	clk := &clock{ms: 5000}
	_, h := counter(clk, -500, 1)
	for i := 0; i < 5; i++ {
		if rec := hit(h, request("GET", "/x", "203.0.113.1")); rec.Code != 200 || rec.Header().Get("RateLimit-Remaining") != "0" {
			t.Fatalf("negative window, request %d: %d remaining %q", i, rec.Code, rec.Header().Get("RateLimit-Remaining"))
		}
	}
	// A negative maximum refuses everything
	_, h = counter(clk, 60_000, -3)
	rec := hit(h, request("GET", "/x", "203.0.113.1"))
	if rec.Code != 429 || rec.Header().Get("RateLimit-Policy") != "-3;w=60" || rec.Header().Get("RateLimit-Limit") != "-3" || rec.Header().Get("RateLimit-Remaining") != "0" {
		t.Errorf("negative maximum: %d %v", rec.Code, rec.Header())
	}
}

// keys is how many keys the limiter holds.
func (l *Limiter) keys() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.current) + len(l.previous)
}

func TestIdleKeysAreDropped(t *testing.T) {
	c := &clock{ms: 0}
	l, h := counter(c, 10_000, 5)
	for i := 0; i < 5000; i++ {
		hit(h, request("GET", "/x", fmt.Sprintf("10.%d.%d.%d", i>>16, (i>>8)&255, i&255)))
	}
	if l.keys() != 5000 {
		t.Fatalf("%d keys, want 5000", l.keys())
	}
	// One window later the idle keys are on their way out, two later they are gone
	c.ms = 10_000
	hit(h, request("GET", "/x", "203.0.113.1"))
	if l.keys() != 5001 {
		t.Errorf("after one window: %d keys, want 5001", l.keys())
	}
	c.ms = 20_000
	hit(h, request("GET", "/x", "203.0.113.2"))
	if l.keys() != 2 {
		t.Errorf("after two windows: %d keys, want 2", l.keys())
	}
	// A long pause drops everything at once
	c.ms = 500_000
	hit(h, request("GET", "/x", "203.0.113.3"))
	if l.keys() != 1 {
		t.Errorf("after a long pause: %d keys, want 1", l.keys())
	}
}

func TestActiveKeyKeepsItsWindowAcrossCleanups(t *testing.T) {
	c := &clock{ms: 0}
	l, h := counter(c, 10_000, 3)
	at := func(ms int64) *httptest.ResponseRecorder {
		c.ms = ms
		return hit(h, request("GET", "/x", "203.0.113.1"))
	}
	// The window starts late in the first cleanup period and runs into the second
	if rec := at(9000); rec.Header().Get("RateLimit-Remaining") != "2" {
		t.Fatalf("first: %v", rec.Header())
	}
	if rec := at(15_000); rec.Header().Get("RateLimit-Remaining") != "1" || rec.Header().Get("RateLimit-Reset") != "4" {
		t.Errorf("after one cleanup the count and the window must be kept: %v", rec.Header())
	}
	if rec := at(18_999); rec.Header().Get("RateLimit-Remaining") != "0" {
		t.Errorf("third: %v", rec.Header())
	}
	if rec := at(18_999); rec.Code != 429 {
		t.Errorf("fourth in the window: %d", rec.Code)
	}
	if rec := at(19_000); rec.Code != 200 || rec.Header().Get("RateLimit-Remaining") != "2" {
		t.Errorf("new window: %d %v", rec.Code, rec.Header())
	}
	if l.keys() != 1 {
		t.Errorf("%d keys", l.keys())
	}
}

func TestParseIntOr(t *testing.T) {
	for in, want := range map[string]int64{
		"":      100,
		"abc":   100,
		"0":     100,
		"00":    100,
		"-0":    100,
		"7":     7,
		" 7 ":   7,
		"\t7":   7,
		"7.9":   7,
		"7abc":  7,
		"+6":    6,
		"-3":    -3,
		"1e2":   1,
		"0x10":  100,
		"08":    8,
		"1 000": 1,
		"--5":   100,
		"+":     100,
		"٣":     100,
		"60000": 60000,
	} {
		if got := parseIntOr(in, 100); got != want {
			t.Errorf("parseIntOr(%q) = %d, want %d", in, got, want)
		}
	}
	if got := parseIntOr("99999999999999999999999999", 1); got < 1<<53 {
		t.Errorf("a huge number: %d", got)
	}
}

func TestSetFromEnvironment(t *testing.T) {
	set := newSet(&clock{}, nil)
	type row struct {
		window time.Duration
		max    int64
	}
	defaults := map[string]row{
		"apiLimiter": {time.Minute, 100}, "sessionReadLimiter": {time.Minute, 600}, "widgetLimiter": {time.Minute, 600}, "panelReadLimiter": {time.Minute, 600},
		"panelPreferenceLimiter": {time.Minute, 30}, "rdClientPageLimiter": {time.Minute, 120}, "loginLimiter": {time.Minute, 5}, "totpVerifyLimiter": {time.Minute, 5},
		"passwordChangeLimiter": {5 * time.Minute, 3}, "uploadLimiter": {time.Minute, 30}, "fileAccessLimiter": {time.Minute, 120},
		"registerLimiter": {time.Minute, 20},
	}
	for _, l := range set.All() {
		if want := defaults[l.Name()]; l.Window() != want.window || l.Max() != want.max {
			t.Errorf("%s: %v / %d, want %v / %d", l.Name(), l.Window(), l.Max(), want.window, want.max)
		}
	}
	set = newSet(&clock{}, map[string]string{"RATE_LIMIT_WINDOW_MS": "2000", "RATE_LIMIT_MAX": "11", "LOGIN_RATE_LIMIT_MAX": "12", "PANEL_READ_RATE_LIMIT_MAX": "13",
		"WIDGET_RATE_LIMIT_MAX": "14", "PANEL_PREFERENCE_RATE_LIMIT_MAX": "15", "RDCLIENT_PAGE_RATE_LIMIT_MAX": "16", "UPLOAD_RATE_LIMIT_MAX": "17", "FILE_ACCESS_RATE_LIMIT_MAX": "18", "BD_REGISTER_RATE_LIMIT_MAX": "19"})
	custom := map[string]row{
		// RATE_LIMIT_WINDOW_MS is the window of five limiters; the others keep theirs
		"apiLimiter": {2 * time.Second, 11}, "sessionReadLimiter": {2 * time.Second, 13}, "widgetLimiter": {time.Minute, 14}, "panelReadLimiter": {2 * time.Second, 13},
		"panelPreferenceLimiter": {time.Minute, 15}, "rdClientPageLimiter": {time.Minute, 16}, "loginLimiter": {2 * time.Second, 12}, "totpVerifyLimiter": {2 * time.Second, 12},
		"passwordChangeLimiter": {5 * time.Minute, 3}, "uploadLimiter": {time.Minute, 17}, "fileAccessLimiter": {time.Minute, 18},
		"registerLimiter": {time.Minute, 19},
	}
	for _, l := range set.All() {
		if want := custom[l.Name()]; l.Window() != want.window || l.Max() != want.max {
			t.Errorf("%s: %v / %d, want %v / %d", l.Name(), l.Window(), l.Max(), want.window, want.max)
		}
	}
	// Options may be left out
	bare := NewSet(Options{})
	if rec := hit(bare.Login.Middleware(http.HandlerFunc(ok)), request("POST", "/x", "203.0.113.1")); rec.Code != 200 || rec.Header().Get("RateLimit-Limit") != "5" {
		t.Errorf("a set without options: %d %v", rec.Code, rec.Header())
	}
}

func TestKeys(t *testing.T) {
	c := &clock{}
	address := ""
	set := NewSet(Options{ClientIP: func(*http.Request) string { return address }, UserKey: func(r *http.Request) string { return r.Header.Get("X-User") }, Now: c.now})
	r := request("POST", "/x", "198.51.100.1")
	// The address, else X-Forwarded-For as sent, else "unknown"
	if got := set.addressKey(r); got != "unknown" {
		t.Errorf("no address: %q", got)
	}
	r.Header.Add("X-Forwarded-For", "203.0.113.9, 10.0.0.1")
	r.Header.Add("X-Forwarded-For", "10.0.0.2")
	if got := set.addressKey(r); got != "203.0.113.9, 10.0.0.1, 10.0.0.2" {
		t.Errorf("X-Forwarded-For fallback: %q", got)
	}
	address = "203.0.113.5"
	if got := set.addressKey(r); got != "203.0.113.5" {
		t.Errorf("address: %q", got)
	}
	if got := set.PanelRead.cfg.Key(r); got != "203.0.113.5" {
		t.Errorf("PanelRead signed out: %q", got)
	}
	if got := set.PanelPreference.cfg.Key(r); got != "203.0.113.5" {
		t.Errorf("PanelPreference signed out: %q", got)
	}
	r.Header.Set("X-User", "7")
	for l, want := range map[*Limiter]string{set.SessionRead: "session-read:7", set.PanelRead: "panel-read:7", set.PanelPreference: "pref:7",
		set.API: "203.0.113.5", set.Widget: "203.0.113.5", set.Login: "203.0.113.5", set.Upload: "203.0.113.5", set.FileAccess: "203.0.113.5", set.Register: "203.0.113.5",
		set.RDClientPage: "203.0.113.5", set.PasswordChange: "203.0.113.5"} {
		if got := l.cfg.Key(r); got != want {
			t.Errorf("%s: key %q, want %q", l.Name(), got, want)
		}
	}
	// PasswordChange has the address alone, without the fallbacks; the
	// others fall back to X-Forwarded-For
	address = ""
	if got := set.Register.cfg.Key(r); got != "203.0.113.9, 10.0.0.1, 10.0.0.2" {
		t.Errorf("Register without an address: %q", got)
	}
	if got := set.PasswordChange.cfg.Key(r); got != "" {
		t.Errorf("PasswordChange without an address: %q", got)
	}
}

// tests/rateLimiter.test.js
func TestPathClassification(t *testing.T) {
	get := func(method string) *http.Request { return httptest.NewRequest(method, "/", nil) }
	poll := func(method, p string) bool { return IsPanelPoll(get(method), p) }
	read := func(method, p string) bool { return IsPanelRead(get(method), p) }
	pref := func(method, p string) bool { return IsPanelPreferenceWrite(get(method), p) }

	if !poll("GET", "/api/dashboard/client-config") {
		t.Error("dashboard client-config GET is a panel poll")
	}
	if poll("POST", "/api/devices") || poll("POST", "/api/dashboard/client-config") {
		t.Error("POST is never a panel poll")
	}
	for _, p := range []string{"/api/stats", "/api/dashboard/activity", "/api/folders", "/api/tags", "/api/device-groups", "/api/bd/notifications"} {
		if !contains(pollPaths, p) || !poll("GET", p) || !poll("HEAD", p) {
			t.Errorf("%s is a poll path", p)
		}
	}
	if !contains(readPaths, "/api/settings/branding") || !read("GET", "/api/settings/branding") || !read("HEAD", "/api/settings/info") {
		t.Error("settings reads")
	}
	if read("POST", "/api/settings/branding") || read("GET", "/api/settings/backup") {
		t.Error("a settings write or another settings path is no panel read")
	}
	if !poll("GET", "/api/panel/strategies") || !poll("GET", "/api/panel/user-groups") || !poll("GET", "/api/dashboard/anything") {
		t.Error("/api/panel/ and /api/dashboard/ are poll prefixes")
	}
	if poll("GET", "/api/settings/info") || poll("GET", "/api/panel") || poll("GET", "/api/devices/") || poll("GET", "/api/Devices") {
		t.Error("unrelated or differently spelled paths are no panel poll")
	}
	if !pref("POST", "/api/desktop/layout") || pref("GET", "/api/desktop/layout") || pref("POST", "/api/devices") || pref("POST", "/api/desktop/layout/") {
		t.Error("the layout save")
	}
	for p, want := range map[string]bool{"/api/users/avatars": true, "/api/users/7/avatar": true, "/api/users/x/avatar": false, "/api/users/7/avatar/": false, "/api/users": false} {
		if read("GET", p) != want {
			t.Errorf("IsPanelRead(%s) = %v", p, !want)
		}
	}
	if len(pollPaths) != 22 || len(readPaths) != 24 {
		t.Errorf("%d poll paths and %d read paths, the console has 22 and 24", len(pollPaths), len(readPaths))
	}

	// The path is rejoined with the /api mount it was cut from
	for _, c := range []struct{ base, sub, original, want string }{
		{"/api", "/bd/notifications", "/api/bd/notifications", "/api/bd/notifications"},
		{"/api", "/registrations/count", "/api/registrations/count", "/api/registrations/count"},
		{"/api", "/", "/api", "/api/"},
		{"", "/api/desktop/layout", "/api/desktop/layout", "/api/desktop/layout"},
		{"/api/devices", "/", "/api/devices", "/api/"},
		{"/API", "/devices", "/API/devices", "/devices"},
		{"/x", "/y", "/api/z", "/api/z"},
		{"", "/remote/a", "/remote/a", "/remote/a"},
		{"/apix", "/y", "/apix/y", "/y"},
	} {
		if got := resolveAPIPath(c.base, c.sub, c.original); got != c.want {
			t.Errorf("resolveAPIPath(%q, %q, %q) = %q, want %q", c.base, c.sub, c.original, got, c.want)
		}
	}
}

func statuses(h http.Handler, n int, newRequest func() *http.Request) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = hit(h, newRequest()).Code
	}
	return out
}

func count(list []int, status int) int {
	n := 0
	for _, s := range list {
		if s == status {
			n++
		}
	}
	return n
}

func indexOf(list []int, status int) int {
	for i, s := range list {
		if s == status {
			return i
		}
	}
	return -1
}

// tests/rateLimiter.sessionReads.test.js
func TestReadBudgetPerSignedInPerson(t *testing.T) {
	app := func() http.Handler { return newSet(&clock{}, nil).AppLevel()(http.HandlerFunc(ok)) }
	as := func(method, target, user string) func() *http.Request {
		return func() *http.Request {
			r := request(method, target, "203.0.113.1")
			if user != "" {
				r.Header.Set("X-User", user)
			}
			return r
		}
	}
	// 150 reads in a minute by one person are not refused
	if s := statuses(app(), 150, as("GET", "/api/users/3/effective-scope", "7")); count(s, 429) != 0 {
		t.Errorf("%d of 150 reads refused", count(s, 429))
	}
	// People behind one address each have their own budget for /api/panel reads
	a := app()
	one := statuses(a, 350, as("GET", "/api/panel/security-audit/overview", "1"))
	two := statuses(a, 350, as("GET", "/api/panel/security-audit/overview", "2"))
	if count(one, 429)+count(two, 429) != 0 {
		t.Errorf("%d and %d reads of two people refused", count(one, 429), count(two, 429))
	}
	// The per-person read budget still ends
	s := statuses(app(), 610, as("GET", "/api/users/3/effective-scope", "7"))
	if count(s[:600], 200) != 600 || count(s[600:], 429) != 10 {
		t.Errorf("610 reads: %d passed of the first 600, %d refused of the last 10", count(s[:600], 200), count(s[600:], 429))
	}
	// Reads without a session keep the per-address limit
	if s := statuses(app(), 110, as("GET", "/api/users/3/effective-scope", "")); indexOf(s, 429) != 100 {
		t.Errorf("signed out: first 429 at %d, want 100", indexOf(s, 429))
	}
	// Writes keep the per-address limit
	if s := statuses(app(), 110, as("POST", "/api/users", "7")); indexOf(s, 429) != 100 {
		t.Errorf("writes: first 429 at %d, want 100", indexOf(s, 429))
	}
}

// tests/security/04-brute-force.test.js
func TestSignInIsRefusedAfterTheMaximum(t *testing.T) {
	set := newSet(&clock{}, map[string]string{"LOGIN_RATE_LIMIT_MAX": "3"})
	h := set.Login.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	s := statuses(h, 5, func() *http.Request { return request("POST", "/api/auth/login", "203.0.113.1") })
	if fmt.Sprint(s) != "[401 401 401 429 429]" {
		t.Errorf("failed sign-ins: %v", s)
	}
}

func TestAppLevelMounts(t *testing.T) {
	c := &clock{}
	set := newSet(c, nil)
	var seen []string
	for _, l := range set.All() {
		l.counted = func(_ *http.Request, name string, _, _ int64, _ bool) { seen = append(seen, name) }
	}
	h := set.AppLevel()(http.HandlerFunc(ok))
	cases := []struct {
		method, target, user string
		want                 string
	}{
		{"GET", "/x", "", ""},
		{"GET", "/apix", "", ""},
		{"GET", "/api/x", "", "apiLimiter"},
		{"POST", "/api/x", "7", "apiLimiter"},
		{"GET", "/api/x", "7", "sessionReadLimiter"},
		{"GET", "/api/devices", "", "widgetLimiter"},
		{"GET", "/api/devices", "7", "sessionReadLimiter"},
		{"POST", "/api/devices", "7", "widgetLimiter apiLimiter"},
		{"GET", "/api/devices/7", "", "widgetLimiter apiLimiter"},
		{"GET", "/api/settings/info", "", "panelReadLimiter"},
		{"GET", "/api/settings/info", "7", "sessionReadLimiter panelReadLimiter"},
		{"PUT", "/api/settings/info", "7", "panelReadLimiter apiLimiter"},
		{"GET", "/api/settings/branding/backgrounds", "", "panelReadLimiter panelReadLimiter"},
		{"GET", "/api/settings/fonts/local", "", "panelReadLimiter panelReadLimiter"},
		{"GET", "/api/settings/branding/export", "", "panelReadLimiter panelReadLimiter"},
		{"GET", "/api/settings/branding/profiles", "", "panelReadLimiter panelReadLimiter"},
		{"GET", "/api/panel/x", "", "widgetLimiter"},
		{"POST", "/api/panel/x", "", "widgetLimiter apiLimiter"},
		{"POST", "/api/desktop/layout", "7", "panelPreferenceLimiter"},
		{"GET", "/api/desktop/layout", "7", "sessionReadLimiter panelPreferenceLimiter"},
		{"GET", "/api/desktop/layout", "", "panelPreferenceLimiter apiLimiter"},
		{"GET", "/api/dashboard/anything", "", ""},
		{"GET", "/API/devices", "", "widgetLimiter apiLimiter"},
		{"GET", "/api/logs/recent", "", "widgetLimiter"},
		{"POST", "/api/auth/login", "", "apiLimiter"},
	}
	for _, tc := range cases {
		seen = nil
		r := request(tc.method, tc.target, "203.0.113.1")
		if tc.user != "" {
			r.Header.Set("X-User", tc.user)
		}
		if rec := hit(h, r); rec.Code != 200 {
			t.Errorf("%s %s: status %d", tc.method, tc.target, rec.Code)
		}
		if got := strings.Join(seen, " "); got != tc.want {
			t.Errorf("%s %s (user %q): counted by %q, want %q", tc.method, tc.target, tc.user, got, tc.want)
		}
	}
}

func TestRefusalStopsTheChain(t *testing.T) {
	c := &clock{}
	set := newSet(c, map[string]string{"WIDGET_RATE_LIMIT_MAX": "1", "RATE_LIMIT_MAX": "5"})
	var seen []string
	for _, l := range set.All() {
		l.counted = func(_ *http.Request, name string, _, _ int64, refused bool) {
			seen = append(seen, fmt.Sprint(name, " ", refused))
		}
	}
	reached := 0
	h := set.AppLevel()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached++ }))
	hit(h, request("POST", "/api/devices", "203.0.113.1"))
	seen = nil
	rec := hit(h, request("POST", "/api/devices", "203.0.113.1"))
	if rec.Code != 429 || reached != 1 || strings.Join(seen, ",") != "widgetLimiter true" {
		t.Errorf("status %d, route reached %d times, counted %v", rec.Code, reached, seen)
	}
	if !strings.Contains(rec.Body.String(), "Too many widget requests") || rec.Header().Get("RateLimit-Policy") != "1;w=60" {
		t.Errorf("the refusing limiter answers: %s %v", rec.Body.String(), rec.Header())
	}
	// The limiter after the refusing one did not count: 4 of its 5 are left
	rec = hit(h, request("POST", "/api/x", "203.0.113.1"))
	if rec.Header().Get("RateLimit-Remaining") != "3" {
		t.Errorf("apiLimiter remaining %q, want 3 (one POST counted before, this one)", rec.Header().Get("RateLimit-Remaining"))
	}
}

func TestRouteLimiterSeesTheAPIPath(t *testing.T) {
	set := newSet(&clock{}, nil)
	h := set.API.Middleware(http.HandlerFunc(ok))
	// GET /api/logs/recent and /api/database/stats carry apiLimiter, which skips poll paths
	for _, target := range []string{"/api/logs/recent", "/api/database/stats?x=1"} {
		if rec := hit(h, request("GET", target, "203.0.113.1")); rec.Header().Get("RateLimit-Limit") != "" {
			t.Errorf("GET %s was counted", target)
		}
	}
	if rec := hit(h, request("POST", "/api/logs/recent", "203.0.113.1")); rec.Header().Get("RateLimit-Limit") != "100" {
		t.Error("a write to a poll path is counted")
	}
}

func TestConcurrentRequests(t *testing.T) {
	c := &clock{}
	_, h := counter(c, 60_000, 100)
	var passed, refused atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 20; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				switch hit(h, request("GET", "/x", "203.0.113.1")).Code {
				case 200:
					passed.Add(1)
				case 429:
					refused.Add(1)
				}
			}
		}()
	}
	wg.Wait()
	if passed.Load() != 100 || refused.Load() != 900 {
		t.Errorf("%d passed and %d refused, want 100 and 900", passed.Load(), refused.Load())
	}
}

func wanHandler(opts WANOptions, next http.HandlerFunc) http.Handler {
	if next == nil {
		next = func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			_, _ = fmt.Fprintf(w, "reached %d", len(body))
		}
	}
	if opts.Log == nil {
		opts.Log = func(bool, string) {}
	}
	return WANStack(opts)(next)
}

func wanRequest(method, target string, bodySize int) *http.Request {
	var body io.Reader
	if bodySize >= 0 {
		body = strings.NewReader(strings.Repeat("a", bodySize))
	}
	r := httptest.NewRequest(method, target, body)
	r.RemoteAddr = "203.0.113.1:40000"
	if method == "POST" {
		r.Header.Set("Content-Type", "application/json")
	}
	return r
}

// tests/wanSecurity.test.js
func TestClientAPIPathList(t *testing.T) {
	cases := []struct {
		method, target string
		want           int
	}{
		// the public telemetry key endpoint, GET only
		{"GET", "/api/telemetry/key", 200},
		{"POST", "/api/telemetry/key", 405},
		// public branding reads, no writes
		{"GET", "/api/branding", 200},
		{"POST", "/api/branding", 405},
		// unauthenticated enrollment bootstrap endpoints
		{"POST", "/api/devices/register", 200},
		{"GET", "/api/devices/register/status", 200},
		// pairing redeem and preview, but only as POST
		{"POST", "/api/pairing/redeem", 200},
		{"POST", "/api/pairing/config", 200},
		{"GET", "/api/pairing/redeem", 405},
		{"GET", "/api/pairing/config", 405},
		// pairing code minting stays off the public client API
		{"POST", "/api/pairing/create", 404},
		{"POST", "/api/pairing/codes", 404},
		// both current-user methods used by clients, no others
		{"GET", "/api/currentUser", 200},
		{"POST", "/api/currentUser", 200},
		{"PUT", "/api/currentUser", 405},
		{"DELETE", "/api/currentUser", 405},
		// unknown paths
		{"GET", "/api/unknown", 404},
		{"GET", "/", 404},
		{"GET", "/api/login/", 404},
		{"GET", "/API/login", 404},
		// paths with a parameter take GET only
		{"GET", "/api/peer-key/abc_DEF-123", 200},
		{"POST", "/api/peer-key/abc", 405},
		{"GET", "/api/peer-key/" + strings.Repeat("a", 33), 404},
		{"GET", "/api/strategies/01234567-89AB-cdef-0123-456789abcdef/status", 200},
		{"HEAD", "/api/peers", 405},
		{"DELETE", "/api/ab", 200},
	}
	h := wanHandler(WANOptions{}, nil)
	for _, c := range cases {
		rec := hit(h, wanRequest(c.method, c.target, -1))
		if rec.Code != c.want {
			t.Errorf("%s %s: %d, want %d", c.method, c.target, rec.Code, c.want)
		}
		if c.want != 200 && rec.Body.Len() != 0 {
			t.Errorf("%s %s: a body with %d: %q", c.method, c.target, c.want, rec.Body.String())
		}
	}
}

func TestClientAPIBodyLimits(t *testing.T) {
	h := wanHandler(WANOptions{}, nil)
	post := func(target string, size int) *httptest.ResponseRecorder {
		r := wanRequest("POST", target, size)
		r.RemoteAddr = fmt.Sprintf("203.0.113.%d:40000", size%250)
		return hit(h, r)
	}
	// heartbeat payloads larger than the default limit are allowed
	if rec := post("/api/heartbeat", 2048); rec.Code != 200 || rec.Body.String() != "reached 2048" {
		t.Errorf("heartbeat with 2048 bytes: %d %s", rec.Code, rec.Body.String())
	}
	if rec := post("/api/heartbeat", 8192); rec.Code != 200 {
		t.Errorf("heartbeat at its limit: %d", rec.Code)
	}
	if rec := post("/api/heartbeat", 8193); rec.Code != 413 || rec.Body.String() != `{"error":"Request too large"}` || rec.Header().Get("Connection") != "close" {
		t.Errorf("heartbeat over its limit: %d %s", rec.Code, rec.Body.String())
	}
	// oversized bodies on paths without their own limit are rejected
	if rec := post("/api/devices/register", 1024); rec.Code != 200 || rec.Body.String() != "reached 1024" {
		t.Errorf("default limit, exactly: %d %s", rec.Code, rec.Body.String())
	}
	if rec := post("/api/devices/register", 1025); rec.Code != 413 {
		t.Errorf("default limit, one over: %d", rec.Code)
	}
	if rec := post("/api/devices/register", 2048); rec.Code != 413 {
		t.Errorf("default limit, 2048 bytes: %d", rec.Code)
	}
	// POST without JSON in its type
	r := wanRequest("POST", "/api/heartbeat", 2)
	r.Header.Set("Content-Type", "text/plain")
	if rec := hit(h, r); rec.Code != 415 || rec.Body.String() != `{"error":"Content-Type must be application/json"}` {
		t.Errorf("POST as text: %d %s", rec.Code, rec.Body.String())
	}
	r = wanRequest("PUT", "/api/ab", 2)
	r.Header.Set("Content-Type", "text/plain")
	if rec := hit(h, r); rec.Code != 200 {
		t.Errorf("PUT as text: %d; only POST has to be JSON", rec.Code)
	}
}

func TestClientAPIHeadersAndOptions(t *testing.T) {
	reached := false
	h := wanHandler(WANOptions{}, func(http.ResponseWriter, *http.Request) { reached = true })
	want := map[string]string{
		"Cache-Control":                "no-store, no-cache, must-revalidate, private",
		"Pragma":                       "no-cache",
		"Expires":                      "0",
		"X-Content-Type-Options":       "nosniff",
		"X-Frame-Options":              "DENY",
		"X-Xss-Protection":             "0",
		"Referrer-Policy":              "no-referrer",
		"Permissions-Policy":           "accelerometer=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), usb=()",
		"Content-Security-Policy":      "default-src 'none'; frame-ancestors 'none'",
		"Access-Control-Allow-Methods": "GET, POST",
		"Access-Control-Allow-Headers": "Content-Type, Authorization",
		"Access-Control-Max-Age":       "86400",
	}
	for _, target := range []string{"/api/peers", "/api/unknown", "/"} {
		rec := hit(h, wanRequest("OPTIONS", target, -1))
		if rec.Code != 204 || reached || len(rec.Header()) != len(want) {
			t.Errorf("OPTIONS %s: %d, reached %v, headers %v", target, rec.Code, reached, rec.Header())
		}
		for name, value := range want {
			if rec.Header().Get(name) != value {
				t.Errorf("OPTIONS %s: %s = %q", target, name, rec.Header().Get(name))
			}
		}
	}
	rec := hit(h, wanRequest("GET", "/api/unknown", -1))
	for name, value := range want {
		if rec.Header().Get(name) != value {
			t.Errorf("404: %s = %q", name, rec.Header().Get(name))
		}
	}
}

func TestClientAPILimiters(t *testing.T) {
	c := &clock{}
	h := wanHandler(WANOptions{Now: c.now}, func(http.ResponseWriter, *http.Request) {})
	from := func(method, target, ip string) *http.Request {
		r := wanRequest(method, target, 2)
		r.RemoteAddr = ip + ":40000"
		return r
	}
	// 5 a minute for login and logout together
	s := statuses(h, 4, func() *http.Request { return from("POST", "/api/login", "203.0.113.1") })
	s = append(s, statuses(h, 3, func() *http.Request { return from("POST", "/api/logout", "203.0.113.1") })...)
	if fmt.Sprint(s) != "[200 200 200 200 200 429 429]" {
		t.Errorf("login and logout: %v", s)
	}
	rec := hit(h, from("POST", "/api/login", "203.0.113.1"))
	if rec.Body.String() != `{"error":"Too many requests, please try again later"}` || rec.Header().Get("RateLimit-Policy") != "5;w=60" {
		t.Errorf("login refusal: %s %v", rec.Body.String(), rec.Header())
	}
	if hit(h, from("GET", "/api/currentUser", "203.0.113.1")).Code != 200 {
		t.Error("other paths are not under the login limit")
	}
	// 20 a minute for POST under /api/audit/
	s = statuses(h, 22, func() *http.Request { return from("POST", "/api/audit/conn", "203.0.113.2") })
	if indexOf(s, 429) != 20 {
		t.Errorf("audit: first 429 at %d, want 20", indexOf(s, 429))
	}
	rec = hit(h, from("POST", "/api/audit/file", "203.0.113.2"))
	if rec.Code != 429 || rec.Body.String() != `{"error":"Audit rate limit exceeded"}` {
		t.Errorf("audit refusal: %d %s", rec.Code, rec.Body.String())
	}
	if hit(h, from("GET", "/api/audit/conn", "203.0.113.2")).Code != 200 {
		t.Error("reading the audit paths is not under the audit limit")
	}
	// 60 a minute for everything
	s = statuses(h, 62, func() *http.Request { return from("GET", "/api/peers", "203.0.113.3") })
	if indexOf(s, 429) != 60 {
		t.Errorf("everything: first 429 at %d, want 60", indexOf(s, 429))
	}
	rec = hit(h, from("GET", "/api/health", "203.0.113.3"))
	if rec.Code != 429 || rec.Body.String() != `{"error":"Too many requests"}` || rec.Header().Get("Retry-After") != "60" {
		t.Errorf("refusal: %d %s", rec.Code, rec.Body.String())
	}
	// Paths off the list do not count
	if hit(h, from("GET", "/api/unknown", "203.0.113.3")).Code != 404 {
		t.Error("an unknown path is refused by the list, not the limiter")
	}
	c.ms += 60_000
	if hit(h, from("GET", "/api/peers", "203.0.113.3")).Code != 200 {
		t.Error("a minute later the budget is back")
	}
	// Without an address everyone shares "unknown"
	h = WANStack(WANOptions{Now: c.now, ClientIP: func(*http.Request) string { return "" }, Log: func(bool, string) {}})(http.HandlerFunc(ok))
	s = statuses(h, 61, func() *http.Request { return from("GET", "/api/peers", "203.0.113.9") })
	if indexOf(s, 429) != 60 {
		t.Errorf("without an address: first 429 at %d", indexOf(s, 429))
	}
}

func TestClientAPIContextIsCancelledAfterTheRequest(t *testing.T) {
	var ctx context.Context
	h := wanHandler(WANOptions{}, func(_ http.ResponseWriter, r *http.Request) { ctx = r.Context() })
	hit(h, wanRequest("GET", "/api/peers", -1))
	if ctx == nil || ctx.Err() == nil {
		t.Error("the request's context has to end with the request")
	}
}

func TestClientAPILog(t *testing.T) {
	c := &clock{ms: 1000}
	type line struct {
		warn bool
		text string
	}
	var lines []line
	h := WANStack(WANOptions{Now: c.now, Log: func(warn bool, text string) { lines = append(lines, line{warn, text}) }})(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			c.ms += 42
			w.WriteHeader(http.StatusCreated)
		}))
	r := wanRequest("GET", "/api/peers?x=1", -1)
	hit(h, r)
	r = wanRequest("GET", "/api/unknown", -1)
	r.Header.Set("X-Forwarded-For", " 198.51.100.7 , 10.0.0.1")
	hit(h, r)
	r = wanRequest("POST", "/api/peers", 0)
	r.Header.Set("X-Real-IP", "198.51.100.8")
	hit(h, r)
	hit(h, wanRequest("OPTIONS", "/api/peers", -1))
	want := []line{
		{false, "[API:GET] /api/peers 201 42ms IP:203.0.113.1"},
		{true, "[API:GET] /api/unknown 404 0ms IP:198.51.100.7"},
		{true, "[API:POST] /api/peers 405 0ms IP:198.51.100.8"},
	}
	if fmt.Sprint(lines) != fmt.Sprint(want) {
		t.Errorf("log lines\n got  %v\n want %v", lines, want)
	}
}
