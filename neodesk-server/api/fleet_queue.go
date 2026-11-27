package api

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/audit"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
	"github.com/neodeskapp/neodesk/neodesk-server/internal/cron"
)

// The fleet commands. They reach the device in the sealed heartbeat
// answer like every telemetry command; the contract with the client is
// in the fleet notes (fleet.run, fleet.resources, fleet.compliance).
const (
	fleetRunCommand        = "fleet.run"
	fleetResourcesCommand  = "fleet.resources"
	fleetComplianceCommand = "fleet.compliance"
)

const (
	// fleetDeliveryWait is how long a fleet command waits for an offline
	// device before it counts as timed out.
	fleetDeliveryWait = 7 * 24 * time.Hour
	// fleetRunSlack is added to a task's time limit once the device runs it.
	fleetRunSlack = 10 * time.Minute
	// fleetResourcesTTL and fleetComplianceTTL are how long a resource list
	// and a scan wait for their device.
	fleetResourcesTTL  = 24 * time.Hour
	fleetComplianceTTL = 24 * time.Hour
	// fleetComplianceEvery is how often each device of an organization is
	// scanned.
	fleetComplianceEvery = 24 * time.Hour
	// fleetSyncEvery is how often a heartbeat checks the device's resources.
	fleetSyncEvery = 5 * time.Minute
	// fleetTick is the scheduler's interval.
	fleetTick = 30 * time.Second
)

// fleetState is the scheduler's and the heartbeat check's memory.
type fleetState struct {
	mu sync.Mutex // one change of runs at a time
	// synced is when a heartbeat last checked a device's resources
	syncMu sync.Mutex
	synced map[string]time.Time
	// fired are the workflow schedule triggers already started, by
	// workflow and step, with the minute they started in
	fired map[string]string
	// complianceAt is when the scheduler last looked for devices to scan
	complianceAt time.Time

	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

func (f *fleetState) init() {
	f.synced = map[string]time.Time{}
	f.fired = map[string]string{}
	f.stop = make(chan struct{})
}

func dbTime(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05") }

// queueFleetCommand queues one fleet command for a device, which takes it
// with its next verified heartbeat.
func (s *Server) queueFleetCommand(actor, deviceID, command string, args any, ttl time.Duration) (int64, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return 0, err
	}
	argsJSON, err := telemetryJSON(raw, 48*1024)
	if err != nil {
		return 0, fmt.Errorf("fleet: the command is too large")
	}
	id, err := s.db.QueueTelemetryCommand(&db.TelemetryCommand{
		DeviceID: deviceID, Command: command, Args: argsJSON, ExpiresAt: dbTime(time.Now().Add(ttl)),
	})
	if err != nil {
		return 0, err
	}
	if s.auditLog != nil {
		s.auditLog.Log(audit.ActionTelemetryCommandQueued, actor, deviceID,
			map[string]string{"command": command, "command_id": fmt.Sprint(id)})
	}
	return id, nil
}

// ── Tasks ──────────────────────────────────────────────────────────────

// fleetRunArgs are the arguments of fleet.run.
type fleetRunArgs struct {
	RunID   int64  `json:"run_id"`
	TaskID  int64  `json:"task_id"`
	Type    string `json:"type"`
	Script  string `json:"script"`
	Shell   string `json:"shell"`
	Timeout int    `json:"timeout"`
}

// fleetTimeout is the time limit the device gets: 10 to 3600 seconds.
func fleetTimeout(seconds int) int {
	if seconds < 10 {
		return 10
	}
	if seconds > 3600 {
		return 3600
	}
	return seconds
}

// queueFleetRun sends the run's command to its device and notes it on the
// run. A command that cannot be queued fails the run.
func (s *Server) queueFleetRun(actor string, task *db.FleetTask, run *db.FleetRun) error {
	timeout := fleetTimeout(task.Timeout)
	id, err := s.queueFleetCommand(actor, run.DeviceID, fleetRunCommand, fleetRunArgs{
		RunID: run.ID, TaskID: task.ID, Type: task.Type, Script: task.Script, Shell: "auto", Timeout: timeout,
	}, fleetDeliveryWait+time.Duration(timeout)*time.Second)
	run.ExitCode, run.StartedAt, run.FinishedAt = nil, "", ""
	if err != nil {
		run.Status, run.Output = db.FleetRunFailed, "The server could not queue the task: "+err.Error()
		run.FinishedAt = time.Now().UTC().Format(time.RFC3339)
	} else {
		run.Status, run.CommandID, run.Output = db.FleetRunPending, id, ""
	}
	if uerr := s.db.Fleet().UpdateFleetRun(run); uerr != nil && err == nil {
		err = uerr
	}
	return err
}

// fleetNextRun is when a task's schedule runs it next, after now: "" for
// one that runs right away or whose time has passed.
func fleetNextRun(scheduleType, value string, now time.Time) (string, error) {
	switch scheduleType {
	case "once":
		at, err := parseFleetTime(value)
		if err != nil {
			return "", err
		}
		if !at.After(now) {
			return "", nil
		}
		return at.UTC().Format(time.RFC3339), nil
	case "cron":
		sched, err := cron.Parse(value)
		if err != nil {
			return "", err
		}
		return sched.Next(now.In(time.Local)).UTC().Format(time.RFC3339), nil
	}
	return "", nil
}

// parseFleetTime reads the time of a "once" task: RFC 3339, or a date and
// time without a zone (the server's time zone).
func parseFleetTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t, nil
	}
	for _, layout := range []string{"2006-01-02T15:04", "2006-01-02T15:04:05", "2006-01-02 15:04", "2006-01-02 15:04:05"} {
		if t, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%q is no date and time", value)
}

// runFleetTask starts a run of the task on its devices: one run row and
// one fleet.run command per device. A device that has not finished an
// earlier run of the task is left out, so a schedule does not pile up
// commands for an offline device. nextRun is the schedule's next time.
func (s *Server) runFleetTask(actor string, task *db.FleetTask, nextRun string) (queued int, skipped []string, err error) {
	fleet := s.db.Fleet()
	targets, terr := s.resolveFleetTargets(nil, task.Targets)
	if terr != nil {
		// Nothing runs; a schedule still moves on to its next time
		task.NextRun = nextRun
		_ = fleet.UpdateFleetTask(task)
		return 0, nil, fmt.Errorf("%s", terr.message)
	}
	batch, err := fleet.StartFleetTaskBatch(task.ID, nextRun)
	if err != nil {
		return 0, nil, err
	}
	task.Batch = batch
	open, _ := fleet.ListOpenFleetRuns(task.ID)
	busy := map[string]bool{}
	for _, r := range open {
		busy[r.DeviceID] = true
	}
	skipped = append(skipped, targets.Skipped...)
	s.fleet.mu.Lock()
	defer s.fleet.mu.Unlock()
	for _, device := range targets.Devices {
		if busy[device] {
			skipped = append(skipped, device)
			continue
		}
		run := &db.FleetRun{TaskID: task.ID, Batch: batch, DeviceID: device, Attempt: 1}
		if err := fleet.CreateFleetRun(run); err != nil {
			return queued, skipped, err
		}
		if err := s.queueFleetRun(actor, task, run); err == nil {
			queued++
		}
	}
	_, err = fleet.RefreshFleetTaskStatus(task.ID)
	return queued, skipped, err
}

// retryFleetRun queues a failed run again as its next attempt.
func (s *Server) retryFleetRun(actor string, task *db.FleetTask, run *db.FleetRun) error {
	run.Attempt++
	return s.queueFleetRun(actor, task, run)
}

// fleetRunStatus is a run's state from the device's final answer.
func fleetRunStatus(status string, exitCode *int64) string {
	switch status {
	case "ok":
		if exitCode != nil && *exitCode != 0 {
			return db.FleetRunFailed
		}
		return db.FleetRunSuccess
	case "timeout":
		return db.FleetRunTimeout
	case "unsupported":
		return db.FleetRunUnsupported
	}
	return db.FleetRunFailed
}

// fleetRunResult is the result object of fleet.run.
type fleetRunResult struct {
	RunID      int64  `json:"run_id"`
	ExitCode   *int64 `json:"exit_code"`
	Output     string `json:"output"`
	StartedAt  string `json:"started_at"`
	FinishedAt string `json:"finished_at"`
	Message    string `json:"message"`
	Code       string `json:"code"`
}

func rfc3339OrNow(value string) string {
	if t, err := time.Parse(time.RFC3339, value); err == nil {
		return t.UTC().Format(time.RFC3339)
	}
	return time.Now().UTC().Format(time.RFC3339)
}

// fleetRunAnswer applies a device's answer to a fleet.run command to its
// run. "running" leaves the run open; a final answer closes it, and a
// failed or timed out run goes again while the task has retries left.
// It reports how long the command should stay open for a "running"
// answer.
func (s *Server) fleetRunAnswer(deviceID string, commandID int64, status string, raw json.RawMessage) time.Duration {
	fleet := s.db.Fleet()
	s.fleet.mu.Lock()
	defer s.fleet.mu.Unlock()
	run, err := fleet.GetFleetRunByCommand(deviceID, commandID)
	if err != nil || run == nil || !run.Open() {
		return telemetryCommandTTL
	}
	task, err := fleet.GetFleetTask(run.TaskID)
	if err != nil {
		return telemetryCommandTTL
	}
	var result fleetRunResult
	_ = json.Unmarshal(raw, &result)
	if status == "running" {
		run.Status = db.FleetRunRunning
		run.StartedAt = rfc3339OrNow(result.StartedAt)
		if result.Output != "" {
			run.Output = result.Output
		}
		_ = fleet.UpdateFleetRun(run)
		_, _ = fleet.RefreshFleetTaskStatus(task.ID)
		return time.Duration(fleetTimeout(task.Timeout))*time.Second + fleetRunSlack
	}
	run.Status = fleetRunStatus(status, result.ExitCode)
	run.ExitCode = result.ExitCode
	run.Output = result.Output
	if run.Output == "" && result.Message != "" {
		run.Output = result.Message
	}
	if run.Output == "" && status != "ok" && result.Code != "" {
		run.Output = result.Code
	}
	if run.StartedAt == "" || result.StartedAt != "" {
		run.StartedAt = rfc3339OrNow(result.StartedAt)
	}
	run.FinishedAt = rfc3339OrNow(result.FinishedAt)
	if err := fleet.UpdateFleetRun(run); err != nil {
		log.Printf("[fleet] run %d of %s: %v", run.ID, deviceID, err)
	}
	if (run.Status == db.FleetRunFailed || run.Status == db.FleetRunTimeout) && run.Attempt <= task.Retries && run.Batch == task.Batch {
		_ = s.retryFleetRun("fleet-retry", task, run)
	}
	_, _ = fleet.RefreshFleetTaskStatus(task.ID)
	return 0
}

// sweepStaleFleetRuns closes the open runs whose command can no longer be
// answered (it expired, or the device was handed it and restarted), as
// timed out, and gives them their retries.
func (s *Server) sweepStaleFleetRuns() {
	fleet := s.db.Fleet()
	stale, err := fleet.ListStaleFleetRuns()
	if err != nil || len(stale) == 0 {
		return
	}
	s.fleet.mu.Lock()
	defer s.fleet.mu.Unlock()
	tasks := map[int64]*db.FleetTask{}
	for _, run := range stale {
		task, ok := tasks[run.TaskID]
		if !ok {
			task, _ = fleet.GetFleetTask(run.TaskID)
			tasks[run.TaskID] = task
		}
		wasPending := run.Status == db.FleetRunPending
		run.Status = db.FleetRunTimeout
		run.FinishedAt = time.Now().UTC().Format(time.RFC3339)
		if wasPending && run.Output == "" {
			run.Output = "The device did not pick the task up in time."
		}
		_ = fleet.UpdateFleetRun(run)
		if task != nil && run.Attempt <= task.Retries && run.Batch == task.Batch && !wasPending {
			_ = s.retryFleetRun("fleet-retry", task, run)
		}
	}
	for id := range tasks {
		_, _ = fleet.RefreshFleetTaskStatus(id)
	}
}

// ── Resources ──────────────────────────────────────────────────────────

// fleetResourcesFor are the resources a device of orgID gets, of the
// organization's resources: the active ones whose scope takes in the
// device.
func fleetResourcesFor(deviceID string, resources []*db.FleetResource, groups *fleetGroups) []db.FleetResourceItem {
	items := []db.FleetResourceItem{}
	for _, r := range resources {
		if !r.Active || !fleetScopeTakes(r, deviceID, groups) {
			continue
		}
		items = append(items, db.FleetResourceItem{ID: r.ID, Type: r.Type, Name: r.Name, Address: r.Address})
	}
	return items
}

func fleetScopeTakes(r *db.FleetResource, deviceID string, groups *fleetGroups) bool {
	switch r.Scope {
	case "", "org":
		return true
	case "device":
		for _, id := range splitTargets(r.ScopeValue) {
			if id == deviceID {
				return true
			}
		}
	case "group":
		for _, ref := range splitTargets(r.ScopeValue) {
			if group := groups.find(ref); group != nil {
				for _, id := range groups.members(group) {
					if id == deviceID {
						return true
					}
				}
			}
		}
	}
	return false
}

// fleetResourcesArgs are the arguments of fleet.resources.
type fleetResourcesArgs struct {
	Revision  string                 `json:"revision"`
	Resources []db.FleetResourceItem `json:"resources"`
}

// syncFleetResources queues the device's full resource list when the
// device has not applied its current revision and is not already being
// sent it. A list the device failed to apply goes again only once it
// changes. It reports whether it queued one.
func (s *Server) syncFleetResources(actor, deviceID string, resources []*db.FleetResource, groups *fleetGroups) bool {
	fleet := s.db.Fleet()
	items := fleetResourcesFor(deviceID, resources, groups)
	want := db.FleetResourceRevision(items)
	state, err := fleet.GetFleetDeviceResources(deviceID)
	if err != nil {
		return false
	}
	applied := db.FleetEmptyRevision
	if state != nil && state.Applied != "" {
		applied = state.Applied
	}
	if want == applied {
		return false
	}
	if state != nil && state.Sent == want {
		if state.Status == "failed" {
			return false
		}
		if state.Status == "pending" {
			if open, _ := s.db.OpenTelemetryCommands(deviceID, []int64{state.CommandID}); open[state.CommandID] != "" {
				return false
			}
		}
	}
	if s.isSupportAgent(deviceID) {
		return false
	}
	id, err := s.queueFleetCommand(actor, deviceID, fleetResourcesCommand,
		fleetResourcesArgs{Revision: want, Resources: items}, fleetResourcesTTL)
	if err != nil {
		log.Printf("[fleet] resources of %s: %v", deviceID, err)
		return false
	}
	_ = fleet.SaveFleetResourcesSent(deviceID, want, id)
	return true
}

// syncFleetOrg sends every device of the organization whose resource list
// changed its new list. It returns how many it queued.
func (s *Server) syncFleetOrg(actor, orgID string) int {
	resources, err := s.db.Fleet().ListFleetResources(orgID)
	if err != nil {
		return 0
	}
	devices, err := s.db.ListOrgDevices(orgID)
	if err != nil {
		return 0
	}
	groups := &fleetGroups{s: s}
	queued := 0
	for _, d := range devices {
		if s.syncFleetResources(actor, d.DeviceID, resources, groups) {
			queued++
		}
	}
	return queued
}

// fleetHeartbeat checks, at most every few minutes per device, whether a
// device that sent a verified heartbeat has its current resources, and
// queues them if not, so they go out in the answer to this heartbeat.
func (s *Server) fleetHeartbeat(deviceID string) {
	s.fleet.syncMu.Lock()
	last, seen := s.fleet.synced[deviceID]
	if seen && time.Since(last) < fleetSyncEvery {
		s.fleet.syncMu.Unlock()
		return
	}
	s.fleet.synced[deviceID] = time.Now()
	s.fleet.syncMu.Unlock()

	orgID, _ := s.db.GetDeviceOrgID(deviceID)
	var resources []*db.FleetResource
	if orgID != "" {
		resources, _ = s.db.Fleet().ListFleetResources(orgID)
	}
	if len(resources) == 0 {
		// Nothing to get: only a device that applied a list before needs
		// to hear that it is empty now
		state, err := s.db.Fleet().GetFleetDeviceResources(deviceID)
		if err != nil || state == nil || state.Applied == "" || state.Applied == db.FleetEmptyRevision {
			return
		}
	}
	s.syncFleetResources("fleet-sync", deviceID, resources, &fleetGroups{s: s})
}

// fleetResourcesAnswer stores a device's answer to fleet.resources.
func (s *Server) fleetResourcesAnswer(deviceID string, commandID int64, status string, raw json.RawMessage) {
	var result struct {
		Revision string          `json:"revision"`
		Items    json.RawMessage `json:"items"`
	}
	_ = json.Unmarshal(raw, &result)
	fleet := s.db.Fleet()
	state, err := fleet.GetFleetDeviceResources(deviceID)
	if err != nil || state == nil || state.CommandID != commandID {
		return
	}
	items := "[]"
	if len(result.Items) > 0 && json.Valid(result.Items) && strings.HasPrefix(strings.TrimSpace(string(result.Items)), "[") {
		items = string(result.Items)
	}
	if err := fleet.SaveFleetResourcesResult(deviceID, commandID, status == "ok", state.Sent, items); err != nil {
		log.Printf("[fleet] resources answer of %s: %v", deviceID, err)
	}
}

// ── Compliance ─────────────────────────────────────────────────────────

// fleetCheck is one check of a compliance scan.
type fleetCheck struct {
	Key     string `json:"key"`
	OK      bool   `json:"ok"`
	Detail  string `json:"detail"`
	Fixable bool   `json:"fixable"`
}

// fleetSoftware is one installed program.
type fleetSoftware struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Publisher string `json:"publisher"`
}

// queueFleetScan asks the device for a compliance scan, first fixing what
// it can when remediate is set.
func (s *Server) queueFleetScan(actor, deviceID string, remediate bool) (int64, error) {
	id, err := s.queueFleetCommand(actor, deviceID, fleetComplianceCommand, map[string]bool{"remediate": remediate}, fleetComplianceTTL)
	if err != nil {
		return 0, err
	}
	return id, s.db.Fleet().SaveFleetComplianceRequest(deviceID, id, remediate)
}

// fleetComplianceScore is passed checks / all checks * 100 and the failed
// checks; a scan without checks found nothing wrong.
func fleetComplianceScore(checks []fleetCheck) (score, issues int) {
	if len(checks) == 0 {
		return 100, 0
	}
	passed := 0
	for _, c := range checks {
		if c.OK {
			passed++
		}
	}
	return passed * 100 / len(checks), len(checks) - passed
}

// fleetComplianceAnswer stores a device's scan.
func (s *Server) fleetComplianceAnswer(deviceID string, commandID int64, status string, raw json.RawMessage) {
	record := &db.FleetCompliance{DeviceID: deviceID, CommandID: commandID}
	if status != "ok" {
		record.Status = "error"
	} else {
		var result struct {
			Software []fleetSoftware `json:"software"`
			Checks   []fleetCheck    `json:"checks"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			record.Status = "error"
		} else {
			if result.Software == nil {
				result.Software = []fleetSoftware{}
			}
			if result.Checks == nil {
				result.Checks = []fleetCheck{}
			}
			record.Score, record.Issues = fleetComplianceScore(result.Checks)
			checks, _ := json.Marshal(result.Checks)
			software, _ := json.Marshal(result.Software)
			record.Checks, record.Software = string(checks), string(software)
		}
	}
	if err := s.db.Fleet().SaveFleetComplianceResult(record); err != nil {
		log.Printf("[fleet] compliance answer of %s: %v", deviceID, err)
	}
}

// scheduleFleetScans queues a scan for each device of an organization
// whose last scan is a day old or that never had one, unless a scan asked
// for in the last day still waits for it.
func (s *Server) scheduleFleetScans(now time.Time) int {
	devices, _, err := s.fleetOrgDevices("")
	if err != nil || len(devices) == 0 {
		return 0
	}
	records, err := s.db.Fleet().ListFleetCompliance(devices)
	if err != nil {
		return 0
	}
	queued := 0
	for _, device := range devices {
		if c := records[device]; c != nil {
			if last, err := time.Parse(time.RFC3339, c.LastScan); err == nil && now.Sub(last) < fleetComplianceEvery {
				continue
			}
			if asked, err := time.Parse(time.RFC3339, c.RequestedAt); err == nil && now.Sub(asked) < fleetComplianceTTL {
				continue
			}
		}
		if s.isSupportAgent(device) {
			continue
		}
		if _, err := s.queueFleetScan("fleet-schedule", device, false); err == nil {
			queued++
		}
	}
	return queued
}

// ── Answers and the scheduler ──────────────────────────────────────────

// fleetAnswer hands a device's answer to a fleet command to the fleet
// tables. For "running" it returns how long the command stays open.
func (s *Server) fleetAnswer(deviceID, command string, commandID int64, status string, raw json.RawMessage) time.Duration {
	switch command {
	case fleetRunCommand:
		return s.fleetRunAnswer(deviceID, commandID, status, raw)
	case fleetResourcesCommand:
		if status != "running" {
			s.fleetResourcesAnswer(deviceID, commandID, status, raw)
		}
		return fleetResourcesTTL
	case fleetComplianceCommand:
		if status != "running" {
			s.fleetComplianceAnswer(deviceID, commandID, status, raw)
		}
		return fleetComplianceTTL
	}
	return telemetryCommandTTL
}

// startFleetScheduler runs the fleet scheduler until Stop.
func (s *Server) startFleetScheduler() {
	s.fleet.wg.Add(1)
	go func() {
		defer s.fleet.wg.Done()
		ticker := time.NewTicker(fleetTick)
		defer ticker.Stop()
		for {
			select {
			case <-s.fleet.stop:
				return
			case now := <-ticker.C:
				s.fleetTick(now)
			}
		}
	}()
}

// stopFleetScheduler ends the scheduler and waits for it.
func (s *Server) stopFleetScheduler() {
	s.fleet.stopOnce.Do(func() { close(s.fleet.stop) })
	s.fleet.wg.Wait()
}

// fleetTick is one round of the scheduler: due tasks, runs that can no
// longer be answered, scheduled workflows, and the daily scans.
func (s *Server) fleetTick(now time.Time) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[fleet] scheduler: %v", r)
		}
	}()
	s.runDueFleetTasks(now)
	s.sweepStaleFleetRuns()
	s.runScheduledWorkflows(now)
	if now.Sub(s.fleet.complianceAt) >= time.Hour {
		s.fleet.complianceAt = now
		s.scheduleFleetScans(now)
	}
}

// runDueFleetTasks starts the tasks whose time has come and sets their
// next time.
func (s *Server) runDueFleetTasks(now time.Time) {
	due, err := s.db.Fleet().ListDueFleetTasks(now)
	if err != nil {
		return
	}
	for _, task := range due {
		next := ""
		if task.ScheduleType == "cron" {
			next, _ = fleetNextRun("cron", task.ScheduleValue, now)
		}
		if _, _, err := s.runFleetTask(fmt.Sprintf("schedule:%s", task.CreatedBy), task, next); err != nil {
			log.Printf("[fleet] scheduled task %d: %v", task.ID, err)
		}
	}
}
