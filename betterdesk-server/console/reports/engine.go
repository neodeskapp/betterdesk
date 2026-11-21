// Package reports is the report engine of the console: reports made on
// demand from the devices, activity, data guard events, audit log,
// tickets, network targets, inventories and alerts, as JSON or CSV.
//
// Each report reads its tables as the old console's database adapter did
// and computes with the values its JavaScript had, so a report has the
// same bytes on each database as before: counts by name (see tally),
// times as text on SQLite and as dates on PostgreSQL, numbers as
// JavaScript prints them.
package reports

import (
	"context"
	"math"
	"sort"
	"time"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/store"
)

// Options configure an Engine.
type Options struct {
	// DB is the console's database.
	DB *store.DB
	// Now is the clock for generated_at. Default time.Now.
	Now func() time.Time
	// Location is the time zone of the process (TZ), in which a
	// PostgreSQL time is written where a report counts values by their
	// text. Default time.Local.
	Location *time.Location
	// ZoneName names the zone of such a time, as JavaScript does in
	// Date.toString ("Coordinated Universal Time"). Default
	// DefaultZoneName.
	ZoneName func(time.Time) string
	// SyncPeers brings the console's device table up to date with the
	// server's before the engine reads it (syncGoPeers in getAllPeers and
	// getPeerStats). Default: the table is read as it is.
	SyncPeers func(context.Context)
}

// Engine makes reports.
type Engine struct {
	db    *store.DB
	now   func() time.Time
	clock clock
	sync  func(context.Context)
}

// New returns an engine.
func New(opts Options) *Engine {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Location == nil {
		opts.Location = time.Local
	}
	if opts.ZoneName == nil {
		opts.ZoneName = DefaultZoneName
	}
	if opts.SyncPeers == nil {
		opts.SyncPeers = func(context.Context) {}
	}
	return &Engine{db: opts.DB, now: opts.Now, clock: clock{loc: opts.Location, zoneName: opts.ZoneName}, sync: opts.SyncPeers}
}

// Filters are the time range of a report as the request gave it, and the
// devices of the account it is made for. Only the activity report reads
// the range; it goes back in every report's filters.
type Filters struct {
	From, To jsbind.Value
	// Scope holds the devices the account may see; nil sees every device.
	// The devices outside it, and their activity, data guard events,
	// inventories and alerts, are left out of the reports. The alert
	// rules, the audit log, the tickets and the network targets are read
	// whole.
	Scope *access.DeviceScope
	// from and to are the range as the statements bind it (see bound),
	// set by Generate.
	from, to string
}

// RangeError is the error for a from or to that is no date or time.
type RangeError struct {
	Name string
}

func (e *RangeError) Error() string { return e.Name + " must be a date or a time" }

// bound reads from or to (name) of a request as an ISO time in UTC with
// milliseconds, the form the activity tables hold; "" for a value that is
// not true, which sets no bound. A date alone is the start of that day in
// UTC, as to its last millisecond; a time without a zone is UTC. Anything
// else is a RangeError.
func bound(v jsbind.Value, name string) (string, error) {
	if !v.Truthy() {
		return "", nil
	}
	if v.IsString() {
		if day, err := time.Parse(time.DateOnly, v.Str()); err == nil {
			if name == "to" {
				day = day.Add(24*time.Hour - time.Millisecond)
			}
			return store.ISOTime(day), nil
		}
		if t, ok := store.ParseTime(v.Str()); ok {
			return store.ISOTime(t), nil
		}
	}
	return "", &RangeError{Name: name}
}

// JSON is JSON.stringify of the filters: the members that are not
// undefined, in the order from, to.
func (f Filters) JSON() string {
	out := []byte{'{'}
	for _, m := range []struct {
		key   string
		value jsbind.Value
	}{{"from", f.From}, {"to", f.To}} {
		if m.value.IsUndefined() {
			continue
		}
		if len(out) > 1 {
			out = append(out, ',')
		}
		out = appendQuoted(out, m.key)
		out = append(out, ':')
		out = append(out, m.value.JSON()...)
	}
	return string(append(out, '}'))
}

// Report is a generated report.
type Report struct {
	// Type is the name of the report, the text the request gave.
	Type        jsbind.Value
	Label       string
	GeneratedAt string
	Filters     Filters
	// Data is the report itself.
	Data *goapi.Object
}

// MarshalJSON writes {type, label, generated_at, filters, data}.
func (r *Report) MarshalJSON() ([]byte, error) {
	out := []byte(`{"type":`)
	out = append(out, r.Type.JSON()...)
	out = append(out, `,"label":`...)
	out = appendQuoted(out, r.Label)
	out = append(out, `,"generated_at":`...)
	out = appendQuoted(out, r.GeneratedAt)
	out = append(out, `,"filters":`...)
	out = append(out, r.Filters.JSON()...)
	out = append(out, `,"data":`...)
	out = appendJSON(out, r.Data)
	return append(out, '}'), nil
}

// TypeInfo is an entry of ListTypes.
type TypeInfo struct {
	Type  string `json:"type"`
	Label string `json:"label"`
}

type generator func(e *Engine, ctx context.Context, f Filters) (*goapi.Object, error)

// registry holds the report types in the order they are listed.
var registry = []struct {
	key, label string
	generate   generator
}{
	{"devices", "Device Summary", (*Engine).devices},
	{"activity", "Activity Summary", (*Engine).activity},
	{"security", "Security Report", (*Engine).security},
	{"tickets", "Ticket Report", (*Engine).tickets},
	{"network", "Network Report", (*Engine).network},
	{"inventory", "Inventory Report", (*Engine).inventory},
	{"alerts", "Alert Report", (*Engine).alerts},
}

// ListTypes is listReportTypes(): every type with its label.
func ListTypes() []TypeInfo {
	out := make([]TypeInfo, len(registry))
	for i, entry := range registry {
		out[i] = TypeInfo{Type: entry.key, Label: entry.label}
	}
	return out
}

// UnknownTypeError is the error for a type there is no report of. Its
// text is "Unknown report type: " and the type as text.
type UnknownTypeError struct {
	Type string
}

func (e *UnknownTypeError) Error() string { return "Unknown report type: " + e.Type }

// Generate is generateReport(type, opts). The type has to be a text that
// is the name of a report; anything else, a list that holds the name too,
// is an UnknownTypeError. From and to have to be dates or times (bound).
func (e *Engine) Generate(ctx context.Context, typ jsbind.Value, f Filters) (*Report, error) {
	for _, entry := range registry {
		if !typ.IsString() || entry.key != typ.Str() {
			continue
		}
		var err error
		if f.from, err = bound(f.From, "from"); err != nil {
			return nil, err
		}
		if f.to, err = bound(f.To, "to"); err != nil {
			return nil, err
		}
		data, err := entry.generate(e, ctx, f)
		if err != nil {
			return nil, err
		}
		return &Report{Type: typ, Label: entry.label, GeneratedAt: store.ISOTime(e.now()), Filters: f, Data: data}, nil
	}
	return nil, &UnknownTypeError{Type: typ.ToString()}
}

// numberOrZero is Number(v) || 0; found false is undefined.
func (e *Engine) numberOrZero(v any, found bool) float64 {
	if !found {
		return 0
	}
	n := e.clock.Number(v)
	if math.IsNaN(n) {
		return 0
	}
	return n
}

// devices is generateDeviceReport.
func (e *Engine) devices(ctx context.Context, f Filters) (*goapi.Object, error) {
	peers, err := e.allPeers(ctx, f.Scope)
	if err != nil {
		return nil, err
	}
	stats, err := e.peerStats(ctx, f.Scope)
	if err != nil {
		return nil, err
	}
	platforms := newTally()
	var online, banned float64
	for _, p := range peers {
		platforms.add(e.clock.String(or(p.platform, true, "Unknown")))
		if p.online {
			online++
		}
		if p.banned {
			banned++
		}
	}
	total := float64(len(peers))
	return goapi.NewObject(
		"total_devices", total,
		"online", online,
		"offline", total-online,
		"banned", banned,
		"platform_distribution", platforms.counts,
		"stats", stats,
	), nil
}

// appUsage is an entry of the activity report's top applications.
type appUsage struct {
	name, category               any
	totalSecs, sessions, devices float64
}

// activity is generateActivityReport.
func (e *Engine) activity(ctx context.Context, f Filters) (*goapi.Object, error) {
	summaries, err := e.allActivitySummaries(ctx, f.from, f.to, f.Scope)
	if err != nil {
		return nil, err
	}
	var active, idle float64
	var devices []any
	seen := map[any]bool{}
	for _, s := range summaries {
		v, found := prop(s, "total_active_secs")
		active += e.numberOrZero(v, found)
		v, found = prop(s, "idle_seconds")
		idle += e.numberOrZero(v, found)
		if id := s.Get("device_id"); Truthy(id) {
			// a Set: equal texts and numbers are one entry, every
			// buffer is its own
			key := id
			if b, isBuffer := id.(Buffer); isBuffer {
				key = &b
			}
			if !seen[key] {
				seen[key] = true
				devices = append(devices, id)
			}
		}
	}

	usage := goapi.NewObject()
	for _, id := range devices {
		apps, err := e.topApps(ctx, id, f.from, f.to)
		if err != nil {
			apps = nil
		}
		for _, app := range apps {
			name := or(app.Get("app_name"), true, "Unknown")
			key := e.clock.String(name)
			entry, _ := usage.Get(key).(*appUsage)
			if entry == nil {
				entry = &appUsage{name: name, category: or(app.Get("category"), true, "")}
				usage.Set(key, entry)
			}
			v, found := prop(app, "total_secs")
			entry.totalSecs += e.numberOrZero(v, found)
			v, found = prop(app, "session_count")
			entry.sessions += e.numberOrZero(v, found)
			entry.devices++
		}
	}
	keys := usage.Keys()
	entries := make([]*appUsage, len(keys))
	for i, key := range keys {
		entries[i] = usage.Get(key).(*appUsage)
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].totalSecs > entries[j].totalSecs })
	if len(entries) > 20 {
		entries = entries[:20]
	}
	top := make([]any, len(entries))
	for i, entry := range entries {
		top[i] = goapi.NewObject(
			"name", entry.name, "category", entry.category,
			"total_secs", entry.totalSecs, "sessions", entry.sessions, "devices", entry.devices,
		)
	}
	return goapi.NewObject(
		"devices_with_activity", float64(len(devices)),
		"daily_reports", float64(len(summaries)),
		"total_active_secs", active,
		"total_idle_secs", idle,
		"top_applications", top,
	), nil
}

// security is generateSecurityReport.
func (e *Engine) security(ctx context.Context, f Filters) (*goapi.Object, error) {
	stats, err := e.dlpEventStats(ctx, f.Scope)
	if err != nil {
		stats = goapi.NewObject("total", 0.0, "blocked", 0.0, "logged", 0.0, "usb_events", 0.0, "file_events", 0.0)
	}

	// The events with details parsed
	var recent []*goapi.Object
	if events, err := e.dlpEvents(ctx, 50, f.Scope); err == nil {
		recent = withParsedDetails(events)
	}

	peers, err := e.allPeers(ctx, f.Scope)
	if err != nil {
		return nil, err
	}
	banned := []any{}
	for _, p := range peers {
		if p.banned {
			banned = append(banned, goapi.NewObject("id", p.id, "hostname", p.hostname, "ip", p.ip, "ban_reason", p.banReason))
		}
	}

	audit, err := e.auditLogs(ctx, 100, 0)
	if err != nil {
		audit = nil
	}
	if len(audit) > 100 {
		audit = audit[:100]
	}

	return goapi.NewObject(
		"dlp", goapi.NewObject("stats", stats, "recent_events", objects(recent, 20)),
		"banned_devices", banned,
		"audit_log_count", float64(len(audit)),
		"recent_audit", objects(audit, 20),
	), nil
}

// withParsedDetails is the events with details as
//
//	typeof details === 'string' ? JSON.parse(details) : (details || {})
//
// where details that are text and no JSON are {}.
func withParsedDetails(events []*goapi.Object) []*goapi.Object {
	out := make([]*goapi.Object, len(events))
	for i, event := range events {
		details, found := prop(event, "details")
		var value any = goapi.NewObject()
		switch text, isText := details.(string); {
		case isText:
			if parsed, err := ParseJSON(text); err == nil {
				value = parsed
			}
		case found && Truthy(details):
			value = details
		}
		out[i] = event.Clone().Set("details", value)
	}
	return out
}

// objects is rows.slice(0, n) as an array.
func objects(rows []*goapi.Object, n int) []any {
	if len(rows) > n {
		rows = rows[:n]
	}
	out := make([]any, len(rows))
	for i, row := range rows {
		out[i] = row
	}
	return out
}

// tickets is generateTicketReport.
func (e *Engine) tickets(ctx context.Context, _ Filters) (*goapi.Object, error) {
	stats, err := e.ticketStats(ctx)
	if err != nil {
		stats = goapi.NewObject("total", 0.0, "open", 0.0, "closed", 0.0, "in_progress", 0.0)
	}
	tickets, err := e.allTickets(ctx)
	if err != nil {
		tickets = nil
	}
	categories, priorities := newTally(), newTally()
	for _, t := range tickets {
		categories.add(e.clock.String(or(t.Get("category"), true, "uncategorized")))
		priorities.add(e.clock.String(or(t.Get("priority"), true, "normal")))
	}
	return goapi.NewObject(
		"stats", stats,
		"total_tickets", float64(len(tickets)),
		"category_distribution", categories.counts,
		"priority_distribution", priorities.counts,
	), nil
}

// network is generateNetworkReport.
func (e *Engine) network(ctx context.Context, _ Filters) (*goapi.Object, error) {
	targets, err := e.networkTargets(ctx)
	if err != nil {
		targets = nil
	}
	var up, down, unknown float64
	list := make([]any, len(targets))
	for i, t := range targets {
		status, _ := t.Get("last_status").(string)
		switch jsLower(status) {
		case "up":
			up++
		case "down":
			down++
		default:
			unknown++
		}
		list[i] = t
	}
	return goapi.NewObject(
		"total_targets", float64(len(targets)),
		"up", up,
		"down", down,
		"unknown", unknown,
		"targets", list,
	), nil
}

// inventory is generateInventoryReport.
func (e *Engine) inventory(ctx context.Context, f Filters) (*goapi.Object, error) {
	inventories, err := e.allInventories(ctx, f.Scope)
	if err != nil {
		inventories = nil
	}
	systems := newTally()
	var ramMB float64
	for _, inv := range inventories {
		var hw any = goapi.NewObject()
		if isObject(inv.hardware) {
			hw = inv.hardware
		}
		osName, found := prop(hw, "os_name")
		systems.add(e.clock.String(or(osName, found, "Unknown")))

		// Number(hw.memory && hw.memory.total_bytes) || 0
		memory, memoryFound := prop(hw, "memory")
		bytes, bytesFound := memory, memoryFound
		if memoryFound && Truthy(memory) {
			bytes, bytesFound = prop(memory, "total_bytes")
		}
		ramBytes := e.numberOrZero(bytes, bytesFound)
		if ramBytes != 0 {
			ramMB += jsRound(ramBytes / 1048576)
		}
	}
	avg := 0.0
	if n := float64(len(inventories)); n > 0 {
		avg = jsRound(ramMB / n)
	}
	return goapi.NewObject(
		"total_inventoried", float64(len(inventories)),
		"os_distribution", systems.counts,
		"avg_ram_mb", avg,
	), nil
}

// alerts is generateAlertReport.
func (e *Engine) alerts(ctx context.Context, f Filters) (*goapi.Object, error) {
	rules, err := e.alertRules(ctx)
	if err != nil {
		rules = nil
	}
	history, err := e.alertHistory(ctx, f.Scope)
	if err != nil {
		history = nil
	}
	if len(history) > 200 {
		history = history[:200]
	}
	perRule := newTally()
	for _, h := range history {
		perRule.add(e.clock.String(or(h.Get("rule_id"), true, "unknown")))
	}
	active := 0.0
	for _, r := range rules {
		enabled, enabledFound := prop(r, "enabled")
		on, onFound := prop(r, "active")
		if (enabledFound && Truthy(enabled)) || (onFound && Truthy(on)) {
			active++
		}
	}
	return goapi.NewObject(
		"total_rules", float64(len(rules)),
		"active_rules", active,
		"total_alerts", float64(len(history)),
		"alerts_per_rule", perRule.counts,
		"recent_alerts", objects(history, 20),
	), nil
}
