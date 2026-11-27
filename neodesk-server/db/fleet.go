package db

// Fleet tasks: resources the devices of an organization receive, tasks
// run on many devices, saved workflows and compliance results. The work
// goes to the devices as heartbeat telemetry commands (fleet.run,
// fleet.resources, fleet.compliance); these tables hold what was asked
// and what the devices answered.
//
// Both engines share one implementation (fleetStore) over a small backend
// that runs a statement on SQLite or PostgreSQL. Every time the store
// writes itself is RFC 3339 text in UTC, on both engines, so the rows read
// back the same and compare as text.

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ErrFleetNotFound is a fleet row that does not exist.
var ErrFleetNotFound = errors.New("fleet: not found")

// Run states of one device's part of a task run.
const (
	FleetRunPending     = "pending"
	FleetRunRunning     = "running"
	FleetRunSuccess     = "success"
	FleetRunFailed      = "failed"
	FleetRunTimeout     = "timeout"
	FleetRunUnsupported = "unsupported"
)

// FleetRunOutputMax is the most output a run keeps: the last 64 KiB.
const FleetRunOutputMax = 64 * 1024

// FleetResource is a printer, network drive, DNS, VPN or proxy setting the
// devices of an organization receive. Scope says which of them: all of
// the organization ("org"), the members of a device group ("group",
// ScopeValue its name or guid) or devices by id ("device", ScopeValue the
// ids).
type FleetResource struct {
	ID         int64  `json:"id"`
	OrgID      string `json:"org_id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Address    string `json:"address"`
	Scope      string `json:"scope"`
	ScopeValue string `json:"scope_value"`
	Active     bool   `json:"active"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// FleetResourceItem is one resource as a device gets it.
type FleetResourceItem struct {
	ID      int64  `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Address string `json:"address"`
}

// FleetResourceRevision is the revision of a device's resource list: the
// SHA-256 of its JSON with the items in id order. An empty list has the
// revision "", which a device that never got any has too.
func FleetResourceRevision(items []FleetResourceItem) string {
	if len(items) == 0 {
		return ""
	}
	sorted := append([]FleetResourceItem(nil), items...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	raw, _ := json.Marshal(sorted)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// FleetDeviceResources is what the server sent a device of its resources
// and what the device applied.
type FleetDeviceResources struct {
	DeviceID string `json:"device_id"`
	// Sent is the revision of the last list queued, CommandID its command.
	Sent      string `json:"revision_sent"`
	CommandID int64  `json:"command_id"`
	// Applied is the revision the device last reported as applied.
	Applied string `json:"revision_applied"`
	// Status is "pending" while the device has not answered the last
	// list, "applied" or "failed" after.
	Status    string `json:"status"`
	Items     string `json:"items"` // the device's per-item answer, JSON
	UpdatedAt string `json:"updated_at"`
}

// FleetTask is a job run on many devices, now, once at a time or on a
// cron schedule.
type FleetTask struct {
	ID    int64  `json:"id"`
	OrgID string `json:"org_id"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	// Targets is the text the devices were entered as.
	Targets       string `json:"targets"`
	ScheduleType  string `json:"schedule_type"`
	ScheduleValue string `json:"schedule_value"`
	Script        string `json:"script"`
	Retries       int    `json:"retries"`
	Timeout       int    `json:"timeout"`
	Status        string `json:"status"`
	CreatedBy     string `json:"created_by"`
	CreatedAt     string `json:"created_at"`
	UpdatedAt     string `json:"updated_at"`
	LastRun       string `json:"last_run"`
	NextRun       string `json:"next_run"`
	// Batch counts the task's runs: its runs on the devices carry the
	// number of the run they belong to.
	Batch      int   `json:"batch"`
	WorkflowID int64 `json:"workflow_id,omitempty"`
	// Counts are the states of the devices in the last run (list reads).
	Counts map[string]int `json:"counts,omitempty"`
}

// FleetRun is one device's part of one run of a task.
type FleetRun struct {
	ID         int64  `json:"id"`
	TaskID     int64  `json:"task_id"`
	Batch      int    `json:"batch"`
	DeviceID   string `json:"device_id"`
	CommandID  int64  `json:"command_id"`
	Attempt    int    `json:"attempt"`
	Status     string `json:"status"`
	ExitCode   *int64 `json:"exit_code"`
	Output     string `json:"output"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

// Open reports whether the run still waits for the device.
func (r *FleetRun) Open() bool {
	return r.Status == FleetRunPending || r.Status == FleetRunRunning
}

// FleetTaskFilter narrows ListFleetTasks; empty fields match all.
type FleetTaskFilter struct {
	OrgID    string
	Status   string
	DeviceID string
}

// FleetWorkflow is a saved workflow: its steps and their connections as
// the builder saves them (JSON lists).
type FleetWorkflow struct {
	ID          int64  `json:"id"`
	OrgID       string `json:"org_id"`
	Name        string `json:"name"`
	Nodes       string `json:"nodes"`
	Connections string `json:"connections"`
	CreatedBy   string `json:"created_by"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

// FleetWorkflowRun is one execution (or dry run) of a workflow.
type FleetWorkflowRun struct {
	ID         int64  `json:"id"`
	WorkflowID int64  `json:"workflow_id"`
	DryRun     bool   `json:"dry_run"`
	Status     string `json:"status"`
	Output     string `json:"output"`
	TaskIDs    string `json:"task_ids"` // JSON list
	StartedBy  string `json:"started_by"`
	CreatedAt  string `json:"created_at"`
}

// FleetCompliance is a device's last compliance scan.
type FleetCompliance struct {
	DeviceID string `json:"device_id"`
	// Score is passed checks / all checks * 100, Issues the failed checks.
	Score    int    `json:"score"`
	Issues   int    `json:"issues"`
	Checks   string `json:"checks"`   // JSON list
	Software string `json:"software"` // JSON list
	LastScan string `json:"last_scan"`
	// CommandID and RequestedAt are the last scan asked for.
	CommandID   int64  `json:"command_id"`
	RequestedAt string `json:"requested_at"`
	Remediate   bool   `json:"remediate"`
	Status      string `json:"status"` // pending, ok, error
}

// FleetStore keeps the fleet tables. Both engines give one (Fleet).
type FleetStore interface {
	ListFleetResources(orgID string) ([]*FleetResource, error)
	GetFleetResource(orgID string, id int64) (*FleetResource, error)
	CreateFleetResource(r *FleetResource) error
	UpdateFleetResource(r *FleetResource) error
	DeleteFleetResource(orgID string, id int64) error

	GetFleetDeviceResources(deviceID string) (*FleetDeviceResources, error)
	ListFleetDeviceResources(deviceIDs []string) (map[string]*FleetDeviceResources, error)
	// SaveFleetResourcesSent notes that revision went to the device with
	// commandID; the device has not answered it yet.
	SaveFleetResourcesSent(deviceID, revision string, commandID int64) error
	// SaveFleetResourcesResult stores the device's answer to commandID:
	// applied says whether it applied revision. An answer to another
	// command than the last one sent changes nothing.
	SaveFleetResourcesResult(deviceID string, commandID int64, applied bool, revision, items string) error

	CreateFleetTask(t *FleetTask) error
	UpdateFleetTask(t *FleetTask) error
	GetFleetTask(id int64) (*FleetTask, error)
	ListFleetTasks(f FleetTaskFilter) ([]*FleetTask, error)
	DeleteFleetTask(id int64) error
	// ListDueFleetTasks are the tasks whose next run is at now or before.
	ListDueFleetTasks(now time.Time) ([]*FleetTask, error)
	// StartFleetTaskBatch counts a new run of the task and returns its
	// number; nextRun is when the schedule runs it again ("" never).
	StartFleetTaskBatch(id int64, nextRun string) (int, error)
	// RefreshFleetTaskStatus sets the task's status from its last run and
	// returns it.
	RefreshFleetTaskStatus(id int64) (string, error)

	CreateFleetRun(r *FleetRun) error
	UpdateFleetRun(r *FleetRun) error
	GetFleetRunByCommand(deviceID string, commandID int64) (*FleetRun, error)
	// ListFleetRuns are the device runs of one run of the task (batch 0:
	// the last one), in device order.
	ListFleetRuns(taskID int64, batch int) ([]*FleetRun, error)
	// ListStaleFleetRuns are the open runs whose command the device can no
	// longer answer: expired, closed without a result, or gone.
	ListStaleFleetRuns() ([]*FleetRun, error)

	CreateFleetWorkflow(w *FleetWorkflow) error
	UpdateFleetWorkflow(w *FleetWorkflow) error
	GetFleetWorkflow(id int64) (*FleetWorkflow, error)
	ListFleetWorkflows(orgID string) ([]*FleetWorkflow, error)
	DeleteFleetWorkflow(id int64) error
	AddFleetWorkflowRun(r *FleetWorkflowRun) error
	ListFleetWorkflowRuns(workflowID int64, limit int) ([]*FleetWorkflowRun, error)

	GetFleetCompliance(deviceID string) (*FleetCompliance, error)
	ListFleetCompliance(deviceIDs []string) (map[string]*FleetCompliance, error)
	SaveFleetComplianceRequest(deviceID string, commandID int64, remediate bool) error
	// SaveFleetComplianceResult stores the scan the device answered
	// commandID with. An answer to another command changes nothing.
	SaveFleetComplianceResult(c *FleetCompliance) error
}

// Compile-time checks: both engines give a FleetStore.
var (
	_ FleetStore = (*fleetStore)(nil)
)

// Fleet returns the fleet tables of the SQLite database.
func (s *SQLiteDB) Fleet() FleetStore { return &fleetStore{b: sqliteFleet{s}, now: time.Now} }

// Fleet returns the fleet tables of the PostgreSQL database.
func (pg *PostgresDB) Fleet() FleetStore { return &fleetStore{b: pgFleet{pg}, now: time.Now} }

// fleetSchema is the fleet tables, with serial the id column of the engine.
func fleetSchema(serial, bigint string) []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS fleet_resources (
			id ` + serial + `,
			org_id TEXT NOT NULL,
			name TEXT NOT NULL DEFAULT '',
			type TEXT NOT NULL DEFAULT '',
			address TEXT NOT NULL DEFAULT '',
			scope TEXT NOT NULL DEFAULT 'org',
			scope_value TEXT NOT NULL DEFAULT '',
			active ` + bigint + ` NOT NULL DEFAULT 1,
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_fleet_resources_org ON fleet_resources(org_id)`,
		`CREATE TABLE IF NOT EXISTS fleet_device_resources (
			device_id TEXT PRIMARY KEY,
			revision_sent TEXT NOT NULL DEFAULT '',
			command_id ` + bigint + ` NOT NULL DEFAULT 0,
			revision_applied TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT '',
			items TEXT NOT NULL DEFAULT '[]',
			updated_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS fleet_tasks (
			id ` + serial + `,
			org_id TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			type TEXT NOT NULL DEFAULT '',
			targets TEXT NOT NULL DEFAULT '',
			schedule_type TEXT NOT NULL DEFAULT 'immediate',
			schedule_value TEXT NOT NULL DEFAULT '',
			script TEXT NOT NULL DEFAULT '',
			retries ` + bigint + ` NOT NULL DEFAULT 0,
			timeout ` + bigint + ` NOT NULL DEFAULT 300,
			status TEXT NOT NULL DEFAULT 'pending',
			created_by TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT '',
			last_run TEXT NOT NULL DEFAULT '',
			next_run TEXT NOT NULL DEFAULT '',
			batch ` + bigint + ` NOT NULL DEFAULT 0,
			workflow_id ` + bigint + ` NOT NULL DEFAULT 0
		)`,
		`CREATE INDEX IF NOT EXISTS idx_fleet_tasks_next_run ON fleet_tasks(next_run)`,
		`CREATE TABLE IF NOT EXISTS fleet_task_runs (
			id ` + serial + `,
			task_id ` + bigint + ` NOT NULL,
			batch ` + bigint + ` NOT NULL DEFAULT 1,
			device_id TEXT NOT NULL,
			command_id ` + bigint + ` NOT NULL DEFAULT 0,
			attempt ` + bigint + ` NOT NULL DEFAULT 1,
			status TEXT NOT NULL DEFAULT 'pending',
			exit_code ` + bigint + `,
			output TEXT NOT NULL DEFAULT '',
			started_at TEXT NOT NULL DEFAULT '',
			finished_at TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_fleet_task_runs_task ON fleet_task_runs(task_id, batch)`,
		`CREATE INDEX IF NOT EXISTS idx_fleet_task_runs_command ON fleet_task_runs(device_id, command_id)`,
		`CREATE TABLE IF NOT EXISTS fleet_workflows (
			id ` + serial + `,
			org_id TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			nodes TEXT NOT NULL DEFAULT '[]',
			connections TEXT NOT NULL DEFAULT '[]',
			created_by TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT '',
			updated_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS fleet_workflow_runs (
			id ` + serial + `,
			workflow_id ` + bigint + ` NOT NULL,
			dry_run ` + bigint + ` NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT '',
			output TEXT NOT NULL DEFAULT '',
			task_ids TEXT NOT NULL DEFAULT '[]',
			started_by TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_fleet_workflow_runs_workflow ON fleet_workflow_runs(workflow_id)`,
		`CREATE TABLE IF NOT EXISTS fleet_compliance (
			device_id TEXT PRIMARY KEY,
			score ` + bigint + ` NOT NULL DEFAULT 0,
			issues ` + bigint + ` NOT NULL DEFAULT 0,
			checks TEXT NOT NULL DEFAULT '[]',
			software TEXT NOT NULL DEFAULT '[]',
			last_scan TEXT NOT NULL DEFAULT '',
			command_id ` + bigint + ` NOT NULL DEFAULT 0,
			requested_at TEXT NOT NULL DEFAULT '',
			remediate ` + bigint + ` NOT NULL DEFAULT 0,
			status TEXT NOT NULL DEFAULT ''
		)`,
	}
}

// fleetBackend runs the store's statements on one engine. Statements are
// written with ? placeholders.
type fleetBackend interface {
	exec(query string, args ...any) (int64, error)
	insert(query string, args ...any) (int64, error) // query ends in RETURNING id
	each(query string, args []any, row func(scan func(dest ...any) error) error) error
	// openCommand is the condition on device_telemetry_commands c that
	// the command still takes an answer.
	openCommand() string
	// nowSQL is the engine's current time for device_telemetry_commands.
	nowSQL() string
}

type sqliteFleet struct{ s *SQLiteDB }

func (b sqliteFleet) exec(query string, args ...any) (int64, error) {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	res, err := b.s.db.Exec(query, args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (b sqliteFleet) insert(query string, args ...any) (int64, error) {
	b.s.mu.Lock()
	defer b.s.mu.Unlock()
	var id int64
	err := b.s.db.QueryRow(query, args...).Scan(&id)
	return id, err
}

func (b sqliteFleet) each(query string, args []any, row func(scan func(dest ...any) error) error) error {
	b.s.mu.RLock()
	defer b.s.mu.RUnlock()
	rows, err := b.s.db.Query(query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := row(rows.Scan); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (sqliteFleet) nowSQL() string { return `datetime('now')` }

func (sqliteFleet) openCommand() string {
	return `c.status IN ('pending', 'running') AND (c.expires_at = '' OR c.expires_at > datetime('now'))`
}

type pgFleet struct{ pg *PostgresDB }

// rebind turns ? placeholders into $1, $2, ...
func rebind(query string) string {
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func (b pgFleet) exec(query string, args ...any) (int64, error) {
	tag, err := b.pg.pool.Exec(b.pg.ctx, rebind(query), args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func (b pgFleet) insert(query string, args ...any) (int64, error) {
	var id int64
	err := b.pg.pool.QueryRow(b.pg.ctx, rebind(query), args...).Scan(&id)
	return id, err
}

func (b pgFleet) each(query string, args []any, row func(scan func(dest ...any) error) error) error {
	rows, err := b.pg.pool.Query(b.pg.ctx, rebind(query), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		if err := row(rows.Scan); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (pgFleet) nowSQL() string { return `NOW()` }

func (pgFleet) openCommand() string {
	return `c.status IN ('pending', 'running') AND c.expires_at > NOW()`
}

type fleetStore struct {
	b   fleetBackend
	now func() time.Time
}

func (f *fleetStore) stamp() string { return f.now().UTC().Format(time.RFC3339) }

func boolInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

// inList is "?, ?, ?" for n values.
func inList(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

func stringArgs(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

// ── Resources ──────────────────────────────────────────────────────────

const fleetResourceColumns = `id, org_id, name, type, address, scope, scope_value, active, created_at, updated_at`

func scanFleetResource(scan func(dest ...any) error) (*FleetResource, error) {
	r := &FleetResource{}
	var active int64
	if err := scan(&r.ID, &r.OrgID, &r.Name, &r.Type, &r.Address, &r.Scope, &r.ScopeValue, &active, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	r.Active = active != 0
	return r, nil
}

func (f *fleetStore) ListFleetResources(orgID string) ([]*FleetResource, error) {
	out := []*FleetResource{}
	err := f.b.each(`SELECT `+fleetResourceColumns+` FROM fleet_resources WHERE org_id = ? ORDER BY id`, []any{orgID},
		func(scan func(dest ...any) error) error {
			r, err := scanFleetResource(scan)
			if err == nil {
				out = append(out, r)
			}
			return err
		})
	return out, err
}

func (f *fleetStore) GetFleetResource(orgID string, id int64) (*FleetResource, error) {
	var found *FleetResource
	err := f.b.each(`SELECT `+fleetResourceColumns+` FROM fleet_resources WHERE org_id = ? AND id = ?`, []any{orgID, id},
		func(scan func(dest ...any) error) error {
			r, err := scanFleetResource(scan)
			found = r
			return err
		})
	if err == nil && found == nil {
		err = ErrFleetNotFound
	}
	return found, err
}

func (f *fleetStore) CreateFleetResource(r *FleetResource) error {
	now := f.stamp()
	r.CreatedAt, r.UpdatedAt = now, now
	id, err := f.b.insert(`INSERT INTO fleet_resources (org_id, name, type, address, scope, scope_value, active, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		r.OrgID, r.Name, r.Type, r.Address, r.Scope, r.ScopeValue, boolInt(r.Active), now, now)
	r.ID = id
	return err
}

func (f *fleetStore) UpdateFleetResource(r *FleetResource) error {
	r.UpdatedAt = f.stamp()
	n, err := f.b.exec(`UPDATE fleet_resources SET name = ?, type = ?, address = ?, scope = ?, scope_value = ?, active = ?, updated_at = ?
		WHERE org_id = ? AND id = ?`,
		r.Name, r.Type, r.Address, r.Scope, r.ScopeValue, boolInt(r.Active), r.UpdatedAt, r.OrgID, r.ID)
	if err == nil && n == 0 {
		err = ErrFleetNotFound
	}
	return err
}

func (f *fleetStore) DeleteFleetResource(orgID string, id int64) error {
	n, err := f.b.exec(`DELETE FROM fleet_resources WHERE org_id = ? AND id = ?`, orgID, id)
	if err == nil && n == 0 {
		err = ErrFleetNotFound
	}
	return err
}

const fleetDeviceColumns = `device_id, revision_sent, command_id, revision_applied, status, items, updated_at`

func scanFleetDevice(scan func(dest ...any) error) (*FleetDeviceResources, error) {
	d := &FleetDeviceResources{}
	err := scan(&d.DeviceID, &d.Sent, &d.CommandID, &d.Applied, &d.Status, &d.Items, &d.UpdatedAt)
	return d, err
}

func (f *fleetStore) GetFleetDeviceResources(deviceID string) (*FleetDeviceResources, error) {
	var found *FleetDeviceResources
	err := f.b.each(`SELECT `+fleetDeviceColumns+` FROM fleet_device_resources WHERE device_id = ?`, []any{deviceID},
		func(scan func(dest ...any) error) error {
			d, err := scanFleetDevice(scan)
			found = d
			return err
		})
	return found, err
}

func (f *fleetStore) ListFleetDeviceResources(deviceIDs []string) (map[string]*FleetDeviceResources, error) {
	out := map[string]*FleetDeviceResources{}
	if len(deviceIDs) == 0 {
		return out, nil
	}
	err := f.b.each(`SELECT `+fleetDeviceColumns+` FROM fleet_device_resources WHERE device_id IN (`+inList(len(deviceIDs))+`)`,
		stringArgs(deviceIDs), func(scan func(dest ...any) error) error {
			d, err := scanFleetDevice(scan)
			if err == nil {
				out[d.DeviceID] = d
			}
			return err
		})
	return out, err
}

func (f *fleetStore) SaveFleetResourcesSent(deviceID, revision string, commandID int64) error {
	_, err := f.b.exec(`INSERT INTO fleet_device_resources (device_id, revision_sent, command_id, status, updated_at)
		VALUES (?, ?, ?, 'pending', ?)
		ON CONFLICT (device_id) DO UPDATE SET revision_sent = excluded.revision_sent, command_id = excluded.command_id,
			status = 'pending', updated_at = excluded.updated_at`,
		deviceID, revision, commandID, f.stamp())
	return err
}

func (f *fleetStore) SaveFleetResourcesResult(deviceID string, commandID int64, applied bool, revision, items string) error {
	if items == "" {
		items = "[]"
	}
	if applied {
		_, err := f.b.exec(`UPDATE fleet_device_resources SET revision_applied = ?, status = 'applied', items = ?, updated_at = ?
			WHERE device_id = ? AND command_id = ?`, revision, items, f.stamp(), deviceID, commandID)
		return err
	}
	_, err := f.b.exec(`UPDATE fleet_device_resources SET status = 'failed', items = ?, updated_at = ?
		WHERE device_id = ? AND command_id = ?`, items, f.stamp(), deviceID, commandID)
	return err
}

// ── Tasks ──────────────────────────────────────────────────────────────

const fleetTaskColumns = `id, org_id, name, type, targets, schedule_type, schedule_value, script, retries, timeout,
	status, created_by, created_at, updated_at, last_run, next_run, batch, workflow_id`

func scanFleetTask(scan func(dest ...any) error) (*FleetTask, error) {
	t := &FleetTask{}
	var retries, timeout, batch int64
	err := scan(&t.ID, &t.OrgID, &t.Name, &t.Type, &t.Targets, &t.ScheduleType, &t.ScheduleValue, &t.Script,
		&retries, &timeout, &t.Status, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt, &t.LastRun, &t.NextRun, &batch, &t.WorkflowID)
	t.Retries, t.Timeout, t.Batch = int(retries), int(timeout), int(batch)
	return t, err
}

func (f *fleetStore) CreateFleetTask(t *FleetTask) error {
	now := f.stamp()
	t.CreatedAt, t.UpdatedAt = now, now
	if t.Status == "" {
		t.Status = FleetRunPending
	}
	id, err := f.b.insert(`INSERT INTO fleet_tasks (org_id, name, type, targets, schedule_type, schedule_value, script, retries, timeout,
			status, created_by, created_at, updated_at, last_run, next_run, batch, workflow_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?, 0, ?) RETURNING id`,
		t.OrgID, t.Name, t.Type, t.Targets, t.ScheduleType, t.ScheduleValue, t.Script, int64(t.Retries), int64(t.Timeout),
		t.Status, t.CreatedBy, now, now, t.NextRun, t.WorkflowID)
	t.ID = id
	return err
}

func (f *fleetStore) UpdateFleetTask(t *FleetTask) error {
	t.UpdatedAt = f.stamp()
	n, err := f.b.exec(`UPDATE fleet_tasks SET name = ?, type = ?, targets = ?, schedule_type = ?, schedule_value = ?, script = ?,
			retries = ?, timeout = ?, next_run = ?, updated_at = ? WHERE id = ?`,
		t.Name, t.Type, t.Targets, t.ScheduleType, t.ScheduleValue, t.Script, int64(t.Retries), int64(t.Timeout),
		t.NextRun, t.UpdatedAt, t.ID)
	if err == nil && n == 0 {
		err = ErrFleetNotFound
	}
	return err
}

func (f *fleetStore) GetFleetTask(id int64) (*FleetTask, error) {
	var found *FleetTask
	err := f.b.each(`SELECT `+fleetTaskColumns+` FROM fleet_tasks WHERE id = ?`, []any{id},
		func(scan func(dest ...any) error) error {
			t, err := scanFleetTask(scan)
			found = t
			return err
		})
	if err == nil && found == nil {
		err = ErrFleetNotFound
	}
	return found, err
}

func (f *fleetStore) ListFleetTasks(filter FleetTaskFilter) ([]*FleetTask, error) {
	query := `SELECT ` + fleetTaskColumns + ` FROM fleet_tasks WHERE 1 = 1`
	var args []any
	if filter.OrgID != "" {
		query += ` AND org_id = ?`
		args = append(args, filter.OrgID)
	}
	if filter.Status != "" {
		query += ` AND status = ?`
		args = append(args, filter.Status)
	}
	if filter.DeviceID != "" {
		query += ` AND EXISTS (SELECT 1 FROM fleet_task_runs r WHERE r.task_id = fleet_tasks.id AND r.device_id = ?)`
		args = append(args, filter.DeviceID)
	}
	query += ` ORDER BY id DESC`
	out := []*FleetTask{}
	byID := map[int64]*FleetTask{}
	err := f.b.each(query, args, func(scan func(dest ...any) error) error {
		t, err := scanFleetTask(scan)
		if err == nil {
			t.Counts = map[string]int{}
			out = append(out, t)
			byID[t.ID] = t
		}
		return err
	})
	if err != nil || len(out) == 0 {
		return out, err
	}
	err = f.b.each(`SELECT r.task_id, r.status, COUNT(*) FROM fleet_task_runs r
		JOIN fleet_tasks t ON t.id = r.task_id AND r.batch = t.batch
		GROUP BY r.task_id, r.status`, nil, func(scan func(dest ...any) error) error {
		var id, n int64
		var status string
		if err := scan(&id, &status, &n); err != nil {
			return err
		}
		if t := byID[id]; t != nil {
			t.Counts[status] = int(n)
		}
		return nil
	})
	return out, err
}

func (f *fleetStore) DeleteFleetTask(id int64) error {
	// The commands no device has picked up yet go with the task.
	if _, err := f.b.exec(`UPDATE device_telemetry_commands SET status = 'cancelled', completed_at = `+f.b.nowSQL()+`
		WHERE status = 'pending' AND id IN (SELECT command_id FROM fleet_task_runs WHERE task_id = ? AND status = 'pending')`, id); err != nil {
		return err
	}
	if _, err := f.b.exec(`DELETE FROM fleet_task_runs WHERE task_id = ?`, id); err != nil {
		return err
	}
	n, err := f.b.exec(`DELETE FROM fleet_tasks WHERE id = ?`, id)
	if err == nil && n == 0 {
		err = ErrFleetNotFound
	}
	return err
}

func (f *fleetStore) ListDueFleetTasks(now time.Time) ([]*FleetTask, error) {
	out := []*FleetTask{}
	err := f.b.each(`SELECT `+fleetTaskColumns+` FROM fleet_tasks WHERE next_run <> '' AND next_run <= ? ORDER BY next_run, id`,
		[]any{now.UTC().Format(time.RFC3339)}, func(scan func(dest ...any) error) error {
			t, err := scanFleetTask(scan)
			if err == nil {
				out = append(out, t)
			}
			return err
		})
	return out, err
}

func (f *fleetStore) StartFleetTaskBatch(id int64, nextRun string) (int, error) {
	now := f.stamp()
	n, err := f.b.exec(`UPDATE fleet_tasks SET batch = batch + 1, last_run = ?, next_run = ?, status = 'pending', updated_at = ? WHERE id = ?`,
		now, nextRun, now, id)
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, ErrFleetNotFound
	}
	t, err := f.GetFleetTask(id)
	if err != nil {
		return 0, err
	}
	return t.Batch, nil
}

// FleetTaskStatus is a task's status from the states of its last run:
// pending while no device has started or finished, running while some
// device has not finished, failed when a device failed, timed out or could
// not run it, success when all succeeded. A task that has not run yet is
// pending.
func FleetTaskStatus(counts map[string]int) string {
	total, open := 0, 0
	for status, n := range counts {
		total += n
		if status == FleetRunPending || status == FleetRunRunning {
			open += n
		}
	}
	switch {
	case total == 0:
		return FleetRunPending
	case open > 0 && counts[FleetRunPending] == total:
		return FleetRunPending
	case open > 0:
		return FleetRunRunning
	case counts[FleetRunFailed]+counts[FleetRunTimeout]+counts[FleetRunUnsupported] > 0:
		return FleetRunFailed
	default:
		return FleetRunSuccess
	}
}

func (f *fleetStore) RefreshFleetTaskStatus(id int64) (string, error) {
	counts := map[string]int{}
	err := f.b.each(`SELECT r.status, COUNT(*) FROM fleet_task_runs r JOIN fleet_tasks t ON t.id = r.task_id AND r.batch = t.batch
		WHERE r.task_id = ? GROUP BY r.status`, []any{id}, func(scan func(dest ...any) error) error {
		var status string
		var n int64
		if err := scan(&status, &n); err != nil {
			return err
		}
		counts[status] = int(n)
		return nil
	})
	if err != nil {
		return "", err
	}
	status := FleetTaskStatus(counts)
	_, err = f.b.exec(`UPDATE fleet_tasks SET status = ? WHERE id = ?`, status, id)
	return status, err
}

const fleetRunColumns = `id, task_id, batch, device_id, command_id, attempt, status, exit_code, output, started_at, finished_at, created_at, updated_at`

func scanFleetRun(scan func(dest ...any) error) (*FleetRun, error) {
	r := &FleetRun{}
	var batch, attempt int64
	var exit sql.NullInt64
	err := scan(&r.ID, &r.TaskID, &batch, &r.DeviceID, &r.CommandID, &attempt, &r.Status, &exit, &r.Output,
		&r.StartedAt, &r.FinishedAt, &r.CreatedAt, &r.UpdatedAt)
	r.Batch, r.Attempt = int(batch), int(attempt)
	if exit.Valid {
		code := exit.Int64
		r.ExitCode = &code
	}
	return r, err
}

func exitArg(code *int64) any {
	if code == nil {
		return nil
	}
	return *code
}

// cutOutput keeps the last FleetRunOutputMax bytes of output, on a rune
// boundary.
func cutOutput(output string) string {
	if len(output) <= FleetRunOutputMax {
		return output
	}
	cut := output[len(output)-FleetRunOutputMax:]
	for i := 0; i < 4 && i < len(cut); i++ {
		if b := cut[i]; b < 0x80 || b >= 0xC0 {
			return cut[i:]
		}
	}
	return cut
}

func (f *fleetStore) CreateFleetRun(r *FleetRun) error {
	now := f.stamp()
	r.CreatedAt, r.UpdatedAt = now, now
	if r.Status == "" {
		r.Status = FleetRunPending
	}
	if r.Attempt == 0 {
		r.Attempt = 1
	}
	r.Output = cutOutput(r.Output)
	id, err := f.b.insert(`INSERT INTO fleet_task_runs (task_id, batch, device_id, command_id, attempt, status, exit_code, output,
			started_at, finished_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		r.TaskID, int64(r.Batch), r.DeviceID, r.CommandID, int64(r.Attempt), r.Status, exitArg(r.ExitCode), r.Output,
		r.StartedAt, r.FinishedAt, now, now)
	r.ID = id
	return err
}

func (f *fleetStore) UpdateFleetRun(r *FleetRun) error {
	r.UpdatedAt = f.stamp()
	r.Output = cutOutput(r.Output)
	n, err := f.b.exec(`UPDATE fleet_task_runs SET command_id = ?, attempt = ?, status = ?, exit_code = ?, output = ?,
			started_at = ?, finished_at = ?, updated_at = ? WHERE id = ?`,
		r.CommandID, int64(r.Attempt), r.Status, exitArg(r.ExitCode), r.Output, r.StartedAt, r.FinishedAt, r.UpdatedAt, r.ID)
	if err == nil && n == 0 {
		err = ErrFleetNotFound
	}
	return err
}

func (f *fleetStore) GetFleetRunByCommand(deviceID string, commandID int64) (*FleetRun, error) {
	var found *FleetRun
	err := f.b.each(`SELECT `+fleetRunColumns+` FROM fleet_task_runs WHERE device_id = ? AND command_id = ?`,
		[]any{deviceID, commandID}, func(scan func(dest ...any) error) error {
			r, err := scanFleetRun(scan)
			found = r
			return err
		})
	return found, err
}

func (f *fleetStore) ListFleetRuns(taskID int64, batch int) ([]*FleetRun, error) {
	query := `SELECT ` + fleetRunColumns + ` FROM fleet_task_runs WHERE task_id = ? AND batch = ? ORDER BY device_id, id`
	args := []any{taskID, int64(batch)}
	if batch <= 0 {
		query = `SELECT ` + fleetRunColumns + ` FROM fleet_task_runs
			WHERE task_id = ? AND batch = (SELECT batch FROM fleet_tasks WHERE id = ?) ORDER BY device_id, id`
		args = []any{taskID, taskID}
	}
	out := []*FleetRun{}
	err := f.b.each(query, args, func(scan func(dest ...any) error) error {
		r, err := scanFleetRun(scan)
		if err == nil {
			out = append(out, r)
		}
		return err
	})
	return out, err
}

func (f *fleetStore) ListStaleFleetRuns() ([]*FleetRun, error) {
	out := []*FleetRun{}
	err := f.b.each(`SELECT `+prefixed("r.", fleetRunColumns)+` FROM fleet_task_runs r
		WHERE r.status IN ('pending', 'running') AND NOT EXISTS (
			SELECT 1 FROM device_telemetry_commands c WHERE c.id = r.command_id AND c.device_id = r.device_id AND `+f.b.openCommand()+`)
		ORDER BY r.id`, nil, func(scan func(dest ...any) error) error {
		r, err := scanFleetRun(scan)
		if err == nil {
			out = append(out, r)
		}
		return err
	})
	return out, err
}

// prefixed puts prefix before every column of a column list.
func prefixed(prefix, columns string) string {
	parts := strings.Split(columns, ",")
	for i, p := range parts {
		parts[i] = prefix + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

// ── Workflows ──────────────────────────────────────────────────────────

const fleetWorkflowColumns = `id, org_id, name, nodes, connections, created_by, created_at, updated_at`

func scanFleetWorkflow(scan func(dest ...any) error) (*FleetWorkflow, error) {
	w := &FleetWorkflow{}
	err := scan(&w.ID, &w.OrgID, &w.Name, &w.Nodes, &w.Connections, &w.CreatedBy, &w.CreatedAt, &w.UpdatedAt)
	return w, err
}

func (f *fleetStore) CreateFleetWorkflow(w *FleetWorkflow) error {
	now := f.stamp()
	w.CreatedAt, w.UpdatedAt = now, now
	id, err := f.b.insert(`INSERT INTO fleet_workflows (org_id, name, nodes, connections, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id`, w.OrgID, w.Name, w.Nodes, w.Connections, w.CreatedBy, now, now)
	w.ID = id
	return err
}

func (f *fleetStore) UpdateFleetWorkflow(w *FleetWorkflow) error {
	w.UpdatedAt = f.stamp()
	n, err := f.b.exec(`UPDATE fleet_workflows SET name = ?, nodes = ?, connections = ?, updated_at = ? WHERE id = ?`,
		w.Name, w.Nodes, w.Connections, w.UpdatedAt, w.ID)
	if err == nil && n == 0 {
		err = ErrFleetNotFound
	}
	return err
}

func (f *fleetStore) GetFleetWorkflow(id int64) (*FleetWorkflow, error) {
	var found *FleetWorkflow
	err := f.b.each(`SELECT `+fleetWorkflowColumns+` FROM fleet_workflows WHERE id = ?`, []any{id},
		func(scan func(dest ...any) error) error {
			w, err := scanFleetWorkflow(scan)
			found = w
			return err
		})
	if err == nil && found == nil {
		err = ErrFleetNotFound
	}
	return found, err
}

func (f *fleetStore) ListFleetWorkflows(orgID string) ([]*FleetWorkflow, error) {
	query := `SELECT ` + fleetWorkflowColumns + ` FROM fleet_workflows`
	var args []any
	if orgID != "" {
		query += ` WHERE org_id = ?`
		args = append(args, orgID)
	}
	out := []*FleetWorkflow{}
	err := f.b.each(query+` ORDER BY name, id`, args, func(scan func(dest ...any) error) error {
		w, err := scanFleetWorkflow(scan)
		if err == nil {
			out = append(out, w)
		}
		return err
	})
	return out, err
}

func (f *fleetStore) DeleteFleetWorkflow(id int64) error {
	if _, err := f.b.exec(`DELETE FROM fleet_workflow_runs WHERE workflow_id = ?`, id); err != nil {
		return err
	}
	n, err := f.b.exec(`DELETE FROM fleet_workflows WHERE id = ?`, id)
	if err == nil && n == 0 {
		err = ErrFleetNotFound
	}
	return err
}

func (f *fleetStore) AddFleetWorkflowRun(r *FleetWorkflowRun) error {
	r.CreatedAt = f.stamp()
	if r.TaskIDs == "" {
		r.TaskIDs = "[]"
	}
	id, err := f.b.insert(`INSERT INTO fleet_workflow_runs (workflow_id, dry_run, status, output, task_ids, started_by, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING id`,
		r.WorkflowID, boolInt(r.DryRun), r.Status, cutOutput(r.Output), r.TaskIDs, r.StartedBy, r.CreatedAt)
	r.ID = id
	return err
}

func (f *fleetStore) ListFleetWorkflowRuns(workflowID int64, limit int) ([]*FleetWorkflowRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	out := []*FleetWorkflowRun{}
	err := f.b.each(`SELECT id, workflow_id, dry_run, status, output, task_ids, started_by, created_at
		FROM fleet_workflow_runs WHERE workflow_id = ? ORDER BY id DESC LIMIT ?`, []any{workflowID, int64(limit)},
		func(scan func(dest ...any) error) error {
			r := &FleetWorkflowRun{}
			var dry int64
			if err := scan(&r.ID, &r.WorkflowID, &dry, &r.Status, &r.Output, &r.TaskIDs, &r.StartedBy, &r.CreatedAt); err != nil {
				return err
			}
			r.DryRun = dry != 0
			out = append(out, r)
			return nil
		})
	return out, err
}

// ── Compliance ─────────────────────────────────────────────────────────

const fleetComplianceColumns = `device_id, score, issues, checks, software, last_scan, command_id, requested_at, remediate, status`

func scanFleetCompliance(scan func(dest ...any) error) (*FleetCompliance, error) {
	c := &FleetCompliance{}
	var score, issues, remediate int64
	err := scan(&c.DeviceID, &score, &issues, &c.Checks, &c.Software, &c.LastScan, &c.CommandID, &c.RequestedAt, &remediate, &c.Status)
	c.Score, c.Issues, c.Remediate = int(score), int(issues), remediate != 0
	return c, err
}

func (f *fleetStore) GetFleetCompliance(deviceID string) (*FleetCompliance, error) {
	var found *FleetCompliance
	err := f.b.each(`SELECT `+fleetComplianceColumns+` FROM fleet_compliance WHERE device_id = ?`, []any{deviceID},
		func(scan func(dest ...any) error) error {
			c, err := scanFleetCompliance(scan)
			found = c
			return err
		})
	return found, err
}

func (f *fleetStore) ListFleetCompliance(deviceIDs []string) (map[string]*FleetCompliance, error) {
	out := map[string]*FleetCompliance{}
	if len(deviceIDs) == 0 {
		return out, nil
	}
	err := f.b.each(`SELECT `+fleetComplianceColumns+` FROM fleet_compliance WHERE device_id IN (`+inList(len(deviceIDs))+`)`,
		stringArgs(deviceIDs), func(scan func(dest ...any) error) error {
			c, err := scanFleetCompliance(scan)
			if err == nil {
				out[c.DeviceID] = c
			}
			return err
		})
	return out, err
}

func (f *fleetStore) SaveFleetComplianceRequest(deviceID string, commandID int64, remediate bool) error {
	_, err := f.b.exec(`INSERT INTO fleet_compliance (device_id, command_id, requested_at, remediate, status)
		VALUES (?, ?, ?, ?, 'pending')
		ON CONFLICT (device_id) DO UPDATE SET command_id = excluded.command_id, requested_at = excluded.requested_at,
			remediate = excluded.remediate, status = 'pending'`,
		deviceID, commandID, f.stamp(), boolInt(remediate))
	return err
}

func (f *fleetStore) SaveFleetComplianceResult(c *FleetCompliance) error {
	if c.LastScan == "" {
		c.LastScan = f.stamp()
	}
	if c.Checks == "" {
		c.Checks = "[]"
	}
	if c.Software == "" {
		c.Software = "[]"
	}
	if c.Status == "error" {
		_, err := f.b.exec(`UPDATE fleet_compliance SET status = 'error' WHERE device_id = ? AND command_id = ?`, c.DeviceID, c.CommandID)
		return err
	}
	n, err := f.b.exec(`UPDATE fleet_compliance SET score = ?, issues = ?, checks = ?, software = ?, last_scan = ?, status = 'ok'
		WHERE device_id = ? AND command_id = ?`,
		int64(c.Score), int64(c.Issues), c.Checks, c.Software, c.LastScan, c.DeviceID, c.CommandID)
	if err == nil && n == 0 {
		err = fmt.Errorf("fleet: compliance answer for command %d of %s: %w", c.CommandID, c.DeviceID, ErrFleetNotFound)
	}
	return err
}
