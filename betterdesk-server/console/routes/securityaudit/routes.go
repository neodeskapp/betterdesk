// Package securityaudit answers the security audit page: an overview with
// a score made from the API server's health, keys, audit events and
// blocklist, the audit events themselves, a fixed list of hardening
// measures, and the count of the console's dependencies.
package securityaudit

import (
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
	"github.com/unitronix/betterdesk-server/console/session"
)

type handlers struct {
	api    *goapi.Client
	getenv func(string) string
	// packageFile is the old console's package.json, whose dependencies
	// the vulnerabilities route counts.
	packageFile string
	now         func() time.Time
	local       *time.Location
}

// Register adds the group's routes.
func Register(d *kit.Deps) {
	getenv := d.Config.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	h := &handlers{
		api:         d.GoAPI,
		getenv:      getenv,
		packageFile: filepath.Join(d.Config.ConsoleDir, "package.json"),
		now:         time.Now,
		local:       time.Local,
	}
	r := d.Router
	view := d.RequirePermission("audit.view")
	r.Get("/api/panel/security-audit/overview", h.overview, d.RequireAuth, view)
	r.Get("/api/panel/security-audit/events", h.events, d.RequireAuth, view)
	r.Get("/api/panel/security-audit/hardening", h.hardening, d.RequireAuth, view)
	r.Get("/api/panel/security-audit/vulnerabilities", h.vulnerabilities, d.RequireAuth, view)
}

const (
	day            = 24 * time.Hour
	keyMaxAge      = 90 * day
	overviewEvents = "/audit/events?limit=500"
)

// fetched is the answer to one of the overview's calls. data is the body
// when the call worked and the body says anything; an answer without a
// body reads as an object without members.
type fetched struct {
	ok   bool
	data any
}

func (h *handlers) fetch(r *http.Request, path string) fetched {
	resp, err := h.api.Get(r.Context(), path)
	if err != nil {
		return fetched{}
	}
	data := resp.Data()
	if !goapi.Truthy(data) {
		data = goapi.NewObject()
	}
	return fetched{ok: true, data: data}
}

// or returns the data of a call that worked and fallback otherwise.
func (f fetched) or(fallback any) any {
	if f.ok {
		return f.data
	}
	return fallback
}

type check struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Pass bool   `json:"pass"`
}

type overviewStats struct {
	FailedLogins  int     `json:"failed_logins"`
	Bans          float64 `json:"bans"`
	ConfigChanges int     `json:"config_changes"`
	APIKeys       int     `json:"api_keys"`
	OldKeys       int     `json:"old_keys"`
}

// tlsState says where TLS is on. Signal and relay are not reported by the
// API server and stay null.
type tlsState struct {
	Signal *bool `json:"signal"`
	Relay  *bool `json:"relay"`
	API    bool  `json:"api"`
	WSS    bool  `json:"wss"`
}

type overview struct {
	Score  int           `json:"score"`
	Checks []check       `json:"checks"`
	Stats  overviewStats `json:"stats"`
	TLS    tlsState      `json:"tls"`
}

// overview answers GET /api/panel/security-audit/overview: 502 when the
// API server's keys or events are of a kind that is no list.
func (h *handlers) overview(w http.ResponseWriter, r *http.Request) {
	paths := []string{"/health", "/keys", overviewEvents, "/blocklist"}
	results := make([]fetched, len(paths))
	var wg sync.WaitGroup
	for i, path := range paths {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = h.fetch(r, path)
		}()
	}
	wg.Wait()

	out, ok := h.score(r, results[0], results[1], results[2], results[3])
	if !ok {
		kit.JSON(w, r, http.StatusBadGateway, failure{Error: "The API server's audit data cannot be read"})
		return
	}
	kit.JSON(w, r, http.StatusOK, out)
}

// listIn is "Array.isArray(v) ? v : (v[name] || [])" followed by a use
// as a list. ok is false when what is found is set but no list.
func listIn(v any, name string) (list []any, ok bool) {
	if l, isList := v.([]any); isList {
		return l, true
	}
	inner := goapi.Dig(v, name)
	if !goapi.Truthy(inner) {
		return nil, true
	}
	list, ok = inner.([]any)
	return list, ok
}

// objects are the entries of a list that are objects; the others are
// skipped.
func objects(list []any) []*goapi.Object {
	var out []*goapi.Object
	for _, entry := range list {
		if o, isObject := entry.(*goapi.Object); isObject {
			out = append(out, o)
		}
	}
	return out
}

func (h *handlers) score(r *http.Request, healthRes, keysRes, auditRes, blocklistRes fetched) (*overview, bool) {
	health := healthRes.or(goapi.NewObject())
	blocklist := blocklistRes.or(goapi.NewObject())
	events, ok := listIn(auditRes.or([]any{}), "events")
	if !ok {
		return nil, false
	}
	now := h.now().UnixMilli()

	// An event is recent when its time can be read and lies in the last
	// day
	since := now - day.Milliseconds()
	failedLogins, configChanges := 0, 0
	for _, event := range objects(events) {
		at := event.Get("timestamp")
		if !goapi.Truthy(at) {
			at = event.Get("created_at")
		}
		recent := false
		if goapi.Truthy(at) {
			ms, read := jsreq.ParseDate(jsreq.String(at), h.local)
			recent = read && ms >= since
		}
		switch event.Get("action") {
		case "login_failed", "auth_failed", "auth_login_failed":
			if recent {
				failedLogins++
			}
		case "config_changed", "setting_changed":
			configChanges++
		}
	}

	// The count when it is a number, else the length of the entries
	bans := 0.0
	if n, isNumber := goapi.Dig(blocklist, "count").(float64); isNumber && !isNonFinite(n) {
		bans = n
	} else if entries, isList := goapi.Dig(blocklist, "entries").([]any); isList {
		bans = float64(len(entries))
	}

	list, ok := listIn(keysRes.or([]any{}), "keys")
	if !ok {
		return nil, false
	}
	keys := objects(list)
	oldKeys := 0
	for _, key := range keys {
		created := key.Get("created_at")
		if !goapi.Truthy(created) {
			continue
		}
		if ms, read := jsreq.DateOf(created, h.local); read && now-ms > keyMaxAge.Milliseconds() {
			oldKeys++
		}
	}

	tls := goapi.Truthy(goapi.Dig(health, "tls"))
	score := 100
	switch {
	case failedLogins > 20:
		score -= 15
	case failedLogins > 5:
		score -= 5
	}
	if bans > 0 {
		score -= 10
	}
	if oldKeys > 0 {
		score -= 10
	}
	if !tls {
		score -= 10
	}

	return &overview{
		Score: score,
		Checks: []check{
			{"tls_api", "API TLS", tls},
			{"api_key_age", "API Key Rotation (< 90 days)", oldKeys == 0},
			{"failed_logins", "Low Failed Login Rate", failedLogins < 10},
			{"no_bans", "No Active Bans", bans == 0},
			{"audit_log", "Audit Logging", auditRes.ok},
			{"blocklist", "Blocklist Available", blocklistRes.ok},
		},
		Stats: overviewStats{FailedLogins: failedLogins, Bans: bans, ConfigChanges: configChanges, APIKeys: len(keys), OldKeys: oldKeys},
		// The browser reached the console over HTTPS, by the connection or
		// by a proxy the settings trust
		TLS: tlsState{API: tls, WSS: kit.Secure(r)},
	}, true
}

func isNonFinite(f float64) bool { return math.IsNaN(f) || math.IsInf(f, 0) }

type failure struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

// noEvents is the answer for an API answer without a body.
type noEvents struct {
	Events []any `json:"events"`
	Total  int   `json:"total"`
}

// events answers GET /api/panel/security-audit/events?limit=&offset=&action=
// with the API server's audit events: limit from 1 to 500 (50 for 0 or no
// number), offset from 0 to 10000, both read as decimal numbers. A refusal
// has the API server's status, a call without an answer 502. An answer
// without a body (or null, 0, false, "") is no events.
func (h *handlers) events(w http.ResponseWriter, r *http.Request) {
	path := "/audit/events?limit=" + jsreq.NumberString(jsreq.ClampedInt(jsreq.Query(r, "limit"), 50, 1, 500)) +
		"&offset=" + jsreq.NumberString(jsreq.ClampedInt(jsreq.Query(r, "offset"), 0, 0, 10000))
	if action := jsreq.Query(r, "action"); goapi.Truthy(action) {
		path += "&action=" + goapi.EncodeURIComponent(jsreq.String(action))
	}
	resp, err := h.api.Get(r.Context(), path)
	if err != nil {
		status := http.StatusBadGateway
		if s := goapi.AsError(err).Status(); s != 0 {
			status = s
		}
		kit.JSON(w, r, status, failure{Error: err.Error()})
		return
	}
	data := resp.Data()
	if !goapi.Truthy(data) {
		kit.JSON(w, r, http.StatusOK, noEvents{Events: []any{}})
		return
	}
	kit.JSON(w, r, http.StatusOK, data)
}

type priority struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Category    string `json:"category"`
	Severity    string `json:"severity"`
	Implemented bool   `json:"implemented"`
	Description string `json:"description"`
}

type hardeningList struct {
	Priorities []priority `json:"priorities"`
}

// hardening answers GET /api/panel/security-audit/hardening: the fixed
// list of measures. Only the session limit depends on the configuration.
func (h *handlers) hardening(w http.ResponseWriter, r *http.Request) {
	perUser := session.PolicyFromEnv(h.getenv).MaxSessionsPerUser
	limit := "unlimited"
	if perUser > 0 {
		limit = strconv.Itoa(perUser)
	}
	kit.JSON(w, r, http.StatusOK, hardeningList{Priorities: []priority{
		{"mutual_tls", "Mutual TLS Console ↔ Go Server", "network", "high", false, "Replace plain HTTP localhost with mutual TLS authentication between web console and Go server."},
		{"api_key_rotation", "API Key Auto-Rotation", "auth", "high", false, "Automatically rotate API keys every 30 days with a grace period for old keys."},
		{"audit_tamper", "Audit Log Tamper Detection", "audit", "medium", false, "HMAC chain where each audit entry signs the previous — detects log tampering."},
		{"session_timeout", "Panel Idle and Absolute Session Timeout", "auth", "medium", true, "Server-enforced idle timeout and absolute max age for console sessions (SESSION_IDLE_TIMEOUT / SESSION_MAX_AGE)."},
		{"single_session", "Concurrent Panel Session Limit per Account", "auth", "high", perUser > 0, "A new console login ends the least recently active sessions beyond SESSION_MAX_PER_USER (currently " + limit + ") and records IP/User-Agent for audit."},
		{"ip_allowlist", "IP Allowlisting", "network", "medium", false, "Restrict console and API access to specific IP ranges per organization."},
		{"cert_pinning", "Certificate Pinning", "network", "low", false, "Pin TLS certificates in desktop client to prevent MITM attacks."},
		{"mem_safe_creds", "Memory-Safe Credential Handling", "crypto", "medium", false, "Zeroize secrets in memory after use to prevent memory dump attacks."},
		{"csp_headers", "CSP Headers Hardening", "web", "high", true, "Content Security Policy headers to prevent XSS and code injection."},
		{"rate_limit_review", "Rate Limiting Review", "network", "medium", true, "Review and enforce rate limiting on all public-facing endpoints."},
		{"docker_scan", "Docker Image Scanning", "infra", "medium", false, "Automated vulnerability scanning of Docker images using Trivy in CI/CD pipeline."},
	}})
}

type dependencySet struct {
	Total      int   `json:"total"`
	Vulnerable int   `json:"vulnerable"`
	Items      []any `json:"items"`
}

type severityCounts struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
	Total    int `json:"total"`
}

type vulnerabilityReport struct {
	ScanAvailable   bool           `json:"scan_available"`
	Counts          severityCounts `json:"counts"`
	Vulnerabilities []any          `json:"vulnerabilities"`
	Dependencies    struct {
		Go     dependencySet `json:"go"`
		NodeJS dependencySet `json:"nodejs"`
	} `json:"dependencies"`
	LastScan *string `json:"last_scan"`
}

// vulnerabilities answers GET /api/panel/security-audit/vulnerabilities.
// No scan runs: the answer says so and counts the dependencies named in
// the old console's package.json, 0 when that file is not there.
func (h *handlers) vulnerabilities(w http.ResponseWriter, r *http.Request) {
	out := vulnerabilityReport{Vulnerabilities: []any{}}
	out.Dependencies.Go.Items = []any{}
	out.Dependencies.NodeJS.Items = []any{}
	out.Dependencies.NodeJS.Total = h.packageDependencies()
	kit.JSON(w, r, http.StatusOK, out)
}

// packageDependencies counts the names under "dependencies" and
// "devDependencies" of the package file, each name once.
func (h *handlers) packageDependencies() int {
	raw, err := os.ReadFile(h.packageFile)
	if err != nil {
		return 0
	}
	pkg, err := goapi.Parse([]byte(strings.TrimPrefix(string(raw), string(rune(0xFEFF)))))
	if err != nil {
		return 0
	}
	names := map[string]bool{}
	for _, section := range []string{"dependencies", "devDependencies"} {
		switch t := goapi.Dig(pkg, section).(type) {
		case *goapi.Object:
			for _, name := range t.Keys() {
				names[name] = true
			}
		case []any:
			for i := range t {
				names[strconv.Itoa(i)] = true
			}
		case string:
			for i := 0; i < jsreq.Length(t); i++ {
				names[strconv.Itoa(i)] = true
			}
		}
	}
	return len(names)
}
