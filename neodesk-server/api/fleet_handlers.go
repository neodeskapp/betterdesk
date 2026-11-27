package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/audit"
	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// fleetRoutes are the fleet routes the console's Fleet page calls
// (console/routes/fleet), and the two the console's agent routes pass on.
func (s *Server) fleetRoutes(mux *http.ServeMux) {
	// Resources of an organization
	mux.HandleFunc("GET /api/fleet/resources/{orgId}", s.requirePermission(auth.PermOrgManageDevices, s.requireOrgMembership("orgId", s.handleListFleetResources)))
	mux.HandleFunc("POST /api/fleet/resources/{orgId}", s.requirePermission(auth.PermOrgManageDevices, s.requireOrgMembership("orgId", s.handleCreateFleetResource)))
	mux.HandleFunc("PUT /api/fleet/resources/{orgId}/{resourceId}", s.requirePermission(auth.PermOrgManageDevices, s.requireOrgMembership("orgId", s.handleUpdateFleetResource)))
	mux.HandleFunc("DELETE /api/fleet/resources/{orgId}/{resourceId}", s.requirePermission(auth.PermOrgManageDevices, s.requireOrgMembership("orgId", s.handleDeleteFleetResource)))

	// Tasks
	mux.HandleFunc("GET /api/fleet/tasks", s.requirePermission(auth.PermOrgManageDevices, s.handleListFleetTasks))
	mux.HandleFunc("POST /api/fleet/tasks", s.requirePermission(auth.PermOrgManageDevices, s.handleCreateFleetTask))
	mux.HandleFunc("GET /api/fleet/tasks/{taskId}", s.requirePermission(auth.PermOrgManageDevices, s.handleGetFleetTask))
	mux.HandleFunc("PUT /api/fleet/tasks/{taskId}", s.requirePermission(auth.PermOrgManageDevices, s.handleUpdateFleetTask))
	mux.HandleFunc("DELETE /api/fleet/tasks/{taskId}", s.requirePermission(auth.PermOrgManageDevices, s.handleDeleteFleetTask))
	mux.HandleFunc("GET /api/fleet/tasks/{taskId}/output", s.requirePermission(auth.PermOrgManageDevices, s.handleFleetTaskOutput))
	mux.HandleFunc("POST /api/fleet/tasks/{taskId}/retry", s.requirePermission(auth.PermOrgManageDevices, s.handleRetryFleetTask))

	// Workflows
	mux.HandleFunc("GET /api/fleet/workflows", s.requirePermission(auth.PermOrgManageDevices, s.handleListFleetWorkflows))
	mux.HandleFunc("POST /api/fleet/workflows", s.requirePermission(auth.PermOrgManageDevices, s.handleCreateFleetWorkflow))
	mux.HandleFunc("GET /api/fleet/workflows/{workflowId}", s.requirePermission(auth.PermOrgManageDevices, s.handleGetFleetWorkflow))
	mux.HandleFunc("PUT /api/fleet/workflows/{workflowId}", s.requirePermission(auth.PermOrgManageDevices, s.handleUpdateFleetWorkflow))
	mux.HandleFunc("DELETE /api/fleet/workflows/{workflowId}", s.requirePermission(auth.PermOrgManageDevices, s.handleDeleteFleetWorkflow))
	mux.HandleFunc("POST /api/fleet/workflows/{workflowId}/execute", s.requirePermission(auth.PermOrgManageDevices, s.handleExecuteFleetWorkflow))
	mux.HandleFunc("GET /api/fleet/workflows/{workflowId}/history", s.requirePermission(auth.PermOrgManageDevices, s.handleFleetWorkflowHistory))

	// Compliance
	mux.HandleFunc("GET /api/fleet/compliance", s.requirePermission(auth.PermOrgManageDevices, s.handleListFleetCompliance))
	mux.HandleFunc("GET /api/fleet/compliance/{deviceId}", s.requirePermission(auth.PermOrgManageDevices, s.handleGetFleetCompliance))
	mux.HandleFunc("POST /api/fleet/compliance/{deviceId}/scan", s.requirePermission(auth.PermOrgManageDevices, s.handleFleetScan))
	mux.HandleFunc("POST /api/fleet/compliance/{deviceId}/remediate", s.requirePermission(auth.PermOrgManageDevices, s.handleFleetRemediate))

	// What agents report through the console, which names the device of
	// the agent's token as device_id
	mux.HandleFunc("POST /api/fleet/task-result", s.requireConsoleCaller(s.handleFleetAgentTaskResult))
	mux.HandleFunc("POST /api/fleet/software", s.requireConsoleCaller(s.handleFleetAgentSoftware))
}

// ── Request helpers ────────────────────────────────────────────────────

// fleetBody is a JSON object body with lenient readers: the console's
// forms send numbers as numbers or text.
type fleetBody map[string]any

func readFleetBody(w http.ResponseWriter, r *http.Request) (fleetBody, bool) {
	var body fleetBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 512*1024))
	if err := dec.Decode(&body); err != nil || body == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "The request needs a JSON object"})
		return nil, false
	}
	return body, true
}

func (b fleetBody) has(key string) bool { _, ok := b[key]; return ok }

func (b fleetBody) text(key string) string {
	switch v := b[key].(type) {
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	}
	return ""
}

func (b fleetBody) number(key string, fallback int) (int, bool) {
	switch v := b[key].(type) {
	case nil:
		return fallback, true
	case float64:
		if v != float64(int(v)) {
			return 0, false
		}
		return int(v), true
	case string:
		if strings.TrimSpace(v) == "" {
			return fallback, true
		}
		n, err := strconv.Atoi(strings.TrimSpace(v))
		return n, err == nil
	}
	return 0, false
}

func (b fleetBody) flag(key string, fallback bool) bool {
	switch v := b[key].(type) {
	case bool:
		return v
	case float64:
		return v != 0
	case string:
		return v == "1" || strings.EqualFold(v, "true")
	}
	return fallback
}

func badFleetRequest(w http.ResponseWriter, message string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": message})
}

func fleetPathID(w http.ResponseWriter, r *http.Request, name, what string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id <= 0 {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": what + " not found"})
		return 0, false
	}
	return id, true
}

// fleetCreator is who created something: the console names its signed-in
// account as created_by, other callers are who they are.
func fleetCreator(r *http.Request, body fleetBody) string {
	if arrivedInternally(r) {
		if by := strings.TrimSpace(body.text("created_by")); by != "" && len(by) <= 128 {
			return by
		}
	}
	return actorFromCtx(r)
}

// fleetOrgOf is the organization a caller with an organization scope is
// held to, "" for one without.
func fleetOrgOf(r *http.Request) string { return getOrgIDFromCtx(r) }

// ── Resources ──────────────────────────────────────────────────────────

var fleetResourceTypes = map[string]bool{"printer": true, "network_drive": true, "dns": true, "vpn": true, "proxy": true}

// fleetResourceJSON is a resource with how many of the devices that get
// it applied its current list.
type fleetResourceJSON struct {
	*db.FleetResource
	Devices int `json:"devices"`
	Applied int `json:"applied"`
}

func (s *Server) handleListFleetResources(w http.ResponseWriter, r *http.Request) {
	orgID := r.PathValue("orgId")
	if org, err := s.db.GetOrganization(orgID); err != nil || org == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Organization not found"})
		return
	}
	fleet := s.db.Fleet()
	resources, err := fleet.ListFleetResources(orgID)
	if err != nil {
		writeInternalError(w, err, "list fleet resources")
		return
	}
	devices, err := s.db.ListOrgDevices(orgID)
	if err != nil {
		writeInternalError(w, err, "list fleet resources")
		return
	}
	ids := make([]string, 0, len(devices))
	for _, d := range devices {
		ids = append(ids, d.DeviceID)
	}
	states, err := fleet.ListFleetDeviceResources(ids)
	if err != nil {
		writeInternalError(w, err, "list fleet resources")
		return
	}
	out := make([]fleetResourceJSON, len(resources))
	index := map[int64]int{}
	for i, res := range resources {
		out[i] = fleetResourceJSON{FleetResource: res}
		index[res.ID] = i
	}
	groups := &fleetGroups{s: s}
	for _, id := range ids {
		items := fleetResourcesFor(id, resources, groups)
		applied := false
		if state := states[id]; state != nil {
			applied = state.Applied == db.FleetResourceRevision(items)
		}
		for _, item := range items {
			i := index[item.ID]
			out[i].Devices++
			if applied {
				out[i].Applied++
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "resources": out})
}

// fleetResourceFrom checks a resource's fields from body onto res.
func (s *Server) fleetResourceFrom(body fleetBody, res *db.FleetResource) string {
	if body.has("name") || res.ID == 0 {
		res.Name = strings.TrimSpace(body.text("name"))
	}
	if body.has("type") || res.ID == 0 {
		res.Type = strings.TrimSpace(body.text("type"))
	}
	if body.has("address") || body.has("value") || res.ID == 0 {
		res.Address = strings.TrimSpace(body.text("address"))
		if res.Address == "" {
			res.Address = strings.TrimSpace(body.text("value"))
		}
	}
	if body.has("scope") || res.ID == 0 {
		res.Scope = strings.TrimSpace(body.text("scope"))
	}
	if body.has("scope_value") || res.ID == 0 {
		res.ScopeValue = strings.TrimSpace(body.text("scope_value"))
	}
	if body.has("active") {
		res.Active = body.flag("active", true)
	}
	if res.Scope == "" {
		res.Scope = "org"
	}
	switch {
	case res.Name == "":
		return "Enter a name"
	case len(res.Name) > 120:
		return "The name is longer than 120 characters"
	case !fleetResourceTypes[res.Type]:
		return "Unknown resource type " + strconv.Quote(res.Type)
	case res.Address == "":
		return "Enter the address"
	case len(res.Address) > 1024:
		return "The address is longer than 1024 characters"
	case res.Scope != "org" && res.Scope != "group" && res.Scope != "device":
		return "Applies to is org, group or device"
	case res.Scope != "org" && res.ScopeValue == "":
		return "Name the group or the devices"
	case len(res.ScopeValue) > 1024:
		return "The group or devices are longer than 1024 characters"
	}
	if res.Scope == "org" {
		res.ScopeValue = ""
	}
	if res.Scope == "group" {
		groups := &fleetGroups{s: s}
		for _, ref := range splitTargets(res.ScopeValue) {
			if groups.find(ref) == nil {
				return "Unknown device group " + strconv.Quote(ref)
			}
		}
	}
	if res.Scope == "device" {
		for _, id := range splitTargets(res.ScopeValue) {
			if od, _ := s.db.GetOrgDevice(res.OrgID, id); od == nil {
				return "Device " + id + " is not in this organization"
			}
		}
	}
	return ""
}

func (s *Server) handleCreateFleetResource(w http.ResponseWriter, r *http.Request) {
	orgID := r.PathValue("orgId")
	if org, err := s.db.GetOrganization(orgID); err != nil || org == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Organization not found"})
		return
	}
	body, ok := readFleetBody(w, r)
	if !ok {
		return
	}
	res := &db.FleetResource{OrgID: orgID, Active: true}
	if problem := s.fleetResourceFrom(body, res); problem != "" {
		badFleetRequest(w, problem)
		return
	}
	if err := s.db.Fleet().CreateFleetResource(res); err != nil {
		writeInternalError(w, err, "create fleet resource")
		return
	}
	queued := s.syncFleetOrg(s.eventActor(r), orgID)
	s.auditFleetResource(r, "created", res, queued)
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "id": res.ID, "resource": res, "queued": queued})
}

func (s *Server) handleUpdateFleetResource(w http.ResponseWriter, r *http.Request) {
	orgID := r.PathValue("orgId")
	id, ok := fleetPathID(w, r, "resourceId", "Resource")
	if !ok {
		return
	}
	fleet := s.db.Fleet()
	res, err := fleet.GetFleetResource(orgID, id)
	if errors.Is(err, db.ErrFleetNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Resource not found"})
		return
	}
	if err != nil {
		writeInternalError(w, err, "update fleet resource")
		return
	}
	body, ok := readFleetBody(w, r)
	if !ok {
		return
	}
	if problem := s.fleetResourceFrom(body, res); problem != "" {
		badFleetRequest(w, problem)
		return
	}
	if err := fleet.UpdateFleetResource(res); err != nil {
		writeInternalError(w, err, "update fleet resource")
		return
	}
	queued := s.syncFleetOrg(s.eventActor(r), orgID)
	s.auditFleetResource(r, "changed", res, queued)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "id": res.ID, "resource": res, "queued": queued})
}

func (s *Server) handleDeleteFleetResource(w http.ResponseWriter, r *http.Request) {
	orgID := r.PathValue("orgId")
	id, ok := fleetPathID(w, r, "resourceId", "Resource")
	if !ok {
		return
	}
	err := s.db.Fleet().DeleteFleetResource(orgID, id)
	if errors.Is(err, db.ErrFleetNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Resource not found"})
		return
	}
	if err != nil {
		writeInternalError(w, err, "delete fleet resource")
		return
	}
	queued := s.syncFleetOrg(s.eventActor(r), orgID)
	s.auditFleetResource(r, "deleted", &db.FleetResource{ID: id, OrgID: orgID}, queued)
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "queued": queued})
}

// auditFleetResource records a change of an organization's resources.
func (s *Server) auditFleetResource(r *http.Request, change string, res *db.FleetResource, queued int) {
	details := map[string]string{"change": change, "org_id": res.OrgID, "resource_id": strconv.FormatInt(res.ID, 10),
		"devices_queued": strconv.Itoa(queued)}
	if res.Name != "" {
		details["name"], details["type"] = res.Name, res.Type
	}
	s.auditFleet(r, audit.ActionFleetResourceChanged, res.OrgID, details)
}

// fleetTaskDetails are the audit details of a task.
func fleetTaskDetails(task *db.FleetTask, extra map[string]string) map[string]string {
	details := map[string]string{"task_id": strconv.FormatInt(task.ID, 10), "name": task.Name, "type": task.Type,
		"targets": task.Targets, "schedule_type": task.ScheduleType}
	for k, v := range extra {
		details[k] = v
	}
	return details
}

// ── Tasks ──────────────────────────────────────────────────────────────

var fleetTaskTypes = map[string]bool{"script": true, "restart": true, "update": true, "install": true, "file_deploy": true, "registry": true}

// fleetTaskFrom checks a task's fields from body onto task. It returns
// the resolved targets, or writes the refusal and returns nil.
func (s *Server) fleetTaskFrom(w http.ResponseWriter, r *http.Request, body fleetBody, task *db.FleetTask) *fleetTargets {
	fresh := task.ID == 0
	set := func(key string, dst *string, trim bool) {
		if body.has(key) || fresh {
			v := body.text(key)
			if trim {
				v = strings.TrimSpace(v)
			}
			*dst = v
		}
	}
	set("name", &task.Name, true)
	if body.has("type") || body.has("task_type") || fresh {
		task.Type = strings.TrimSpace(body.text("type"))
		if task.Type == "" {
			task.Type = strings.TrimSpace(body.text("task_type"))
		}
	}
	set("targets", &task.Targets, true)
	if task.Targets == "" && body.has("device_id") {
		task.Targets = strings.TrimSpace(body.text("device_id"))
	}
	set("schedule_type", &task.ScheduleType, true)
	set("schedule_value", &task.ScheduleValue, true)
	if task.ScheduleValue == "" && body.has("cron") {
		task.ScheduleValue = strings.TrimSpace(body.text("cron"))
	}
	if body.has("script") || body.has("payload") || fresh {
		task.Script = body.text("script")
		if task.Script == "" {
			task.Script = body.text("payload")
		}
	}
	if task.ScheduleType == "" {
		task.ScheduleType = "immediate"
	}
	retries, okR := body.number("retries", task.Retries)
	timeout := task.Timeout
	if timeout == 0 {
		timeout = 300
	}
	timeout, okT := body.number("timeout", timeout)

	problem := ""
	switch {
	case task.Name == "":
		problem = "Enter a name"
	case len(task.Name) > 120:
		problem = "The name is longer than 120 characters"
	case !fleetTaskTypes[task.Type]:
		problem = "Unknown task type " + strconv.Quote(task.Type)
	case task.Targets == "":
		problem = "Enter at least one device ID or group"
	case len(task.Targets) > 8192:
		problem = "The devices are longer than 8192 characters"
	case !okR || retries < 0 || retries > 3:
		problem = "Retries are 0 to 3"
	case !okT || timeout < 10 || timeout > 3600:
		problem = "The time limit is 10 to 3600 seconds"
	case len(task.Script) > 32*1024:
		problem = "The script is longer than 32 KiB"
	case task.ScheduleType != "immediate" && task.ScheduleType != "once" && task.ScheduleType != "cron":
		problem = "The schedule is immediate, once or cron"
	case task.ScheduleType != "immediate" && task.ScheduleValue == "":
		problem = "Enter when the task should run"
	}
	if problem == "" {
		problem = fleetTaskScriptProblem(task)
	}
	if problem == "" && task.ScheduleType != "immediate" {
		next, err := fleetNextRun(task.ScheduleType, task.ScheduleValue, time.Now())
		switch {
		case err != nil:
			problem = "The schedule cannot be read: " + err.Error()
		case next == "":
			problem = "That time has passed"
		default:
			task.NextRun = next
		}
	}
	if task.ScheduleType == "immediate" {
		task.ScheduleValue, task.NextRun = "", ""
	}
	if problem != "" {
		badFleetRequest(w, problem)
		return nil
	}
	task.Retries, task.Timeout = retries, timeout
	targets, terr := s.resolveFleetTargets(r, task.Targets)
	if terr != nil {
		terr.write(w)
		return nil
	}
	return targets
}

// fleetTaskScriptProblem checks what a task runs, per type.
func fleetTaskScriptProblem(task *db.FleetTask) string {
	script := strings.TrimSpace(task.Script)
	switch task.Type {
	case "script", "registry":
		if script == "" {
			return "Enter the script"
		}
	case "install":
		if script == "" {
			return "Enter the package name or the installer's address"
		}
		if strings.Contains(script, "://") && !strings.HasPrefix(strings.ToLower(script), "https://") {
			return "An installer is downloaded over https only"
		}
	case "file_deploy":
		var spec struct {
			URL    string `json:"url"`
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
		}
		if err := json.Unmarshal([]byte(script), &spec); err != nil {
			return `Copy file needs {"url": "https://...", "path": "<target path>", "sha256": "<optional>"}`
		}
		if !strings.HasPrefix(strings.ToLower(spec.URL), "https://") {
			return "The file is downloaded over https only"
		}
		if !fleetAbsolutePath(spec.Path) {
			return "The target path must be absolute"
		}
		if spec.SHA256 != "" && !regexp.MustCompile(`^[0-9a-fA-F]{64}$`).MatchString(spec.SHA256) {
			return "sha256 is 64 hexadecimal characters"
		}
	}
	return ""
}

func fleetAbsolutePath(p string) bool {
	return strings.HasPrefix(p, "/") || regexp.MustCompile(`^[A-Za-z]:[\\/]`).MatchString(p) || strings.HasPrefix(p, `\\`)
}

// fleetTaskJSON is a task as the console's list reads it.
type fleetTaskJSON struct {
	*db.FleetTask
	Devices int `json:"devices"`
}

func fleetTaskOut(t *db.FleetTask) fleetTaskJSON {
	n := 0
	for _, c := range t.Counts {
		n += c
	}
	if t.Counts == nil {
		t.Counts = map[string]int{}
	}
	return fleetTaskJSON{FleetTask: t, Devices: n}
}

// fleetTaskFor loads the task of the path that the caller may see.
func (s *Server) fleetTaskFor(w http.ResponseWriter, r *http.Request) *db.FleetTask {
	id, ok := fleetPathID(w, r, "taskId", "Task")
	if !ok {
		return nil
	}
	task, err := s.db.Fleet().GetFleetTask(id)
	if errors.Is(err, db.ErrFleetNotFound) || (err == nil && !s.fleetTaskVisible(r, task)) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Task not found"})
		return nil
	}
	if err != nil {
		writeInternalError(w, err, "load fleet task")
		return nil
	}
	return task
}

func (s *Server) fleetTaskVisible(r *http.Request, task *db.FleetTask) bool {
	own := fleetOrgOf(r)
	if own != "" {
		return task.OrgID == own
	}
	return task.OrgID == "" || s.fleetOrgAllowed(r, task.OrgID)
}

func (s *Server) handleListFleetTasks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := db.FleetTaskFilter{OrgID: q.Get("org_id"), Status: q.Get("status"), DeviceID: q.Get("device_id")}
	if own := fleetOrgOf(r); own != "" {
		if filter.OrgID != "" && filter.OrgID != own {
			writeJSON(w, http.StatusOK, map[string]any{"success": true, "tasks": []any{}})
			return
		}
		filter.OrgID = own
	} else if filter.OrgID != "" && !s.fleetOrgAllowed(r, filter.OrgID) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "Not a member of this organization"})
		return
	}
	tasks, err := s.db.Fleet().ListFleetTasks(filter)
	if err != nil {
		writeInternalError(w, err, "list fleet tasks")
		return
	}
	out := make([]fleetTaskJSON, 0, len(tasks))
	for _, t := range tasks {
		if s.fleetTaskVisible(r, t) {
			out = append(out, fleetTaskOut(t))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "tasks": out})
}

func (s *Server) handleCreateFleetTask(w http.ResponseWriter, r *http.Request) {
	body, ok := readFleetBody(w, r)
	if !ok {
		return
	}
	task := &db.FleetTask{}
	targets := s.fleetTaskFrom(w, r, body, task)
	if targets == nil {
		return
	}
	task.CreatedBy = fleetCreator(r, body)
	task.OrgID = fleetOrgOf(r)
	if task.OrgID == "" {
		task.OrgID = targets.singleOrg()
	}
	fleet := s.db.Fleet()
	if err := fleet.CreateFleetTask(task); err != nil {
		writeInternalError(w, err, "create fleet task")
		return
	}
	queued, skipped := 0, targets.Skipped
	if task.ScheduleType == "immediate" {
		var err error
		if queued, skipped, err = s.runFleetTask(s.eventActor(r), task, ""); err != nil {
			writeInternalError(w, err, "run fleet task")
			return
		}
	}
	s.auditFleet(r, audit.ActionFleetTaskCreated, strconv.FormatInt(task.ID, 10), fleetTaskDetails(task, map[string]string{
		"devices": strconv.Itoa(len(targets.Devices)), "devices_queued": strconv.Itoa(queued)}))
	saved, _ := fleet.GetFleetTask(task.ID)
	if saved == nil {
		saved = task
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"success": true, "id": task.ID, "task": fleetTaskOut(saved), "devices": targets.Devices,
		"queued": queued, "skipped": nonNil(skipped),
	})
}

func nonNil(list []string) []string {
	if list == nil {
		return []string{}
	}
	return list
}

func (s *Server) handleGetFleetTask(w http.ResponseWriter, r *http.Request) {
	task := s.fleetTaskFor(w, r)
	if task == nil {
		return
	}
	runs, err := s.db.Fleet().ListFleetRuns(task.ID, 0)
	if err != nil {
		writeInternalError(w, err, "load fleet task")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "task": task, "runs": s.fleetRunsOut(runs)})
}

func (s *Server) handleUpdateFleetTask(w http.ResponseWriter, r *http.Request) {
	task := s.fleetTaskFor(w, r)
	if task == nil {
		return
	}
	body, ok := readFleetBody(w, r)
	if !ok {
		return
	}
	if s.fleetTaskFrom(w, r, body, task) == nil {
		return
	}
	if err := s.db.Fleet().UpdateFleetTask(task); err != nil {
		writeInternalError(w, err, "update fleet task")
		return
	}
	s.auditFleet(r, audit.ActionFleetTaskChanged, strconv.FormatInt(task.ID, 10), fleetTaskDetails(task, nil))
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "id": task.ID, "task": task})
}

func (s *Server) handleDeleteFleetTask(w http.ResponseWriter, r *http.Request) {
	task := s.fleetTaskFor(w, r)
	if task == nil {
		return
	}
	if err := s.db.Fleet().DeleteFleetTask(task.ID); err != nil {
		writeInternalError(w, err, "delete fleet task")
		return
	}
	s.auditFleet(r, audit.ActionFleetTaskDeleted, strconv.FormatInt(task.ID, 10), fleetTaskDetails(task, nil))
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// fleetRunJSON is one device's run as the output sheet shows it.
type fleetRunJSON struct {
	*db.FleetRun
	Hostname string `json:"hostname"`
}

func (s *Server) fleetRunsOut(runs []*db.FleetRun) []fleetRunJSON {
	ids := make([]string, len(runs))
	for i, run := range runs {
		ids[i] = run.DeviceID
	}
	peers, _ := s.db.GetPeersByIDs(ids)
	out := make([]fleetRunJSON, len(runs))
	for i, run := range runs {
		out[i] = fleetRunJSON{FleetRun: run}
		if p := peers[run.DeviceID]; p != nil {
			out[i].Hostname = p.Hostname
		}
	}
	return out
}

// fleetOutputText is the output of all devices of a run as one text: a
// line per device with its state, then what it printed.
func fleetOutputText(runs []fleetRunJSON) string {
	var b strings.Builder
	for i, run := range runs {
		if i > 0 {
			b.WriteString("\n")
		}
		name := run.DeviceID
		if run.Hostname != "" {
			name += " (" + run.Hostname + ")"
		}
		fmt.Fprintf(&b, "[%s] %s", name, run.Status)
		if run.ExitCode != nil {
			fmt.Fprintf(&b, ", exit code %d", *run.ExitCode)
		}
		if run.Attempt > 1 {
			fmt.Fprintf(&b, ", attempt %d", run.Attempt)
		}
		b.WriteString("\n")
		if run.Output != "" {
			b.WriteString(strings.TrimRight(run.Output, "\n"))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (s *Server) handleFleetTaskOutput(w http.ResponseWriter, r *http.Request) {
	task := s.fleetTaskFor(w, r)
	if task == nil {
		return
	}
	runs, err := s.db.Fleet().ListFleetRuns(task.ID, 0)
	if err != nil {
		writeInternalError(w, err, "fleet task output")
		return
	}
	out := s.fleetRunsOut(runs)
	writeJSON(w, http.StatusOK, map[string]any{
		"success": true, "task_id": task.ID, "status": task.Status, "batch": task.Batch, "last_run": task.LastRun,
		"next_run": task.NextRun, "output": fleetOutputText(out), "runs": out,
	})
}

func (s *Server) handleRetryFleetTask(w http.ResponseWriter, r *http.Request) {
	task := s.fleetTaskFor(w, r)
	if task == nil {
		return
	}
	fleet := s.db.Fleet()
	s.fleet.mu.Lock()
	runs, err := fleet.ListFleetRuns(task.ID, 0)
	queued := 0
	if err == nil {
		for _, run := range runs {
			if run.Status != db.FleetRunFailed && run.Status != db.FleetRunTimeout {
				continue
			}
			if s.isSupportAgent(run.DeviceID) {
				continue
			}
			if s.retryFleetRun(s.eventActor(r), task, run) == nil {
				queued++
			}
		}
	}
	s.fleet.mu.Unlock()
	if err != nil {
		writeInternalError(w, err, "retry fleet task")
		return
	}
	if queued == 0 {
		badFleetRequest(w, "No failed or timed out runs to try again")
		return
	}
	status, _ := fleet.RefreshFleetTaskStatus(task.ID)
	s.auditFleet(r, audit.ActionFleetTaskRetried, strconv.FormatInt(task.ID, 10), fleetTaskDetails(task, map[string]string{
		"devices_queued": strconv.Itoa(queued)}))
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "queued": queued, "status": status})
}

// ── Compliance ─────────────────────────────────────────────────────────

// fleetComplianceRow is a device of the compliance list. Score is null
// for a device that was never scanned.
type fleetComplianceRow struct {
	ID       string `json:"id"`
	DeviceID string `json:"device_id"`
	OrgID    string `json:"org_id"`
	Hostname string `json:"hostname"`
	OS       string `json:"os"`
	Platform string `json:"platform"`
	Score    *int   `json:"score"`
	Issues   *int   `json:"issues"`
	LastScan string `json:"last_scan"`
	Status   string `json:"status"`
}

func (s *Server) handleListFleetCompliance(w http.ResponseWriter, r *http.Request) {
	orgID := r.URL.Query().Get("org_id")
	if own := fleetOrgOf(r); own != "" {
		if orgID != "" && orgID != own {
			writeJSON(w, http.StatusOK, map[string]any{"success": true, "devices": []any{}})
			return
		}
		orgID = own
	}
	if orgID != "" {
		if org, err := s.db.GetOrganization(orgID); err != nil || org == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "Organization not found"})
			return
		}
		if !s.fleetOrgAllowed(r, orgID) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "Not a member of this organization"})
			return
		}
	}
	ids, orgOf, err := s.fleetOrgDevices(orgID)
	if err != nil {
		writeInternalError(w, err, "list compliance")
		return
	}
	if orgID == "" {
		// Every organization: only those the caller is a member of
		kept := ids[:0]
		allowed := map[string]bool{}
		for _, id := range ids {
			org := orgOf[id]
			ok, seen := allowed[org]
			if !seen {
				ok = s.fleetOrgAllowed(r, org)
				allowed[org] = ok
			}
			if ok {
				kept = append(kept, id)
			}
		}
		ids = kept
	}
	peerMap, err := s.db.GetPeersByIDs(ids)
	if err != nil {
		writeInternalError(w, err, "list compliance")
		return
	}
	peers := make([]*db.Peer, 0, len(ids))
	for _, id := range ids {
		if p := peerMap[id]; p != nil {
			peers = append(peers, p)
		}
	}
	if peers, err = s.visiblePeers(r, peers); err != nil {
		writeInternalError(w, err, "list compliance")
		return
	}
	visibleIDs := make([]string, len(peers))
	for i, p := range peers {
		visibleIDs[i] = p.ID
	}
	records, err := s.db.Fleet().ListFleetCompliance(visibleIDs)
	if err != nil {
		writeInternalError(w, err, "list compliance")
		return
	}
	rows := make([]fleetComplianceRow, 0, len(peers))
	for _, p := range peers {
		row := fleetComplianceRow{ID: p.ID, DeviceID: p.ID, OrgID: orgOf[p.ID], Hostname: p.Hostname, OS: p.OS}
		if c := records[p.ID]; c != nil {
			row.Status = c.Status
			if c.LastScan != "" {
				score, issues := c.Score, c.Issues
				row.Score, row.Issues, row.LastScan = &score, &issues, c.LastScan
			}
		}
		rows = append(rows, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "devices": rows})
}

// fleetDeviceFor checks the path's device: known and in the caller's
// scope.
func (s *Server) fleetDeviceFor(w http.ResponseWriter, r *http.Request) *db.Peer {
	id := r.PathValue("deviceId")
	p, err := s.db.GetPeer(id)
	if err != nil || p == nil || p.SoftDeleted {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "device_not_found"})
		return nil
	}
	if !s.deviceInScope(w, r, id) {
		return nil
	}
	return p
}

func (s *Server) handleGetFleetCompliance(w http.ResponseWriter, r *http.Request) {
	p := s.fleetDeviceFor(w, r)
	if p == nil {
		return
	}
	c, err := s.db.Fleet().GetFleetCompliance(p.ID)
	if err != nil {
		writeInternalError(w, err, "load compliance")
		return
	}
	out := map[string]any{"success": true, "id": p.ID, "device_id": p.ID, "hostname": p.Hostname, "os": p.OS,
		"score": nil, "issues": nil, "last_scan": "", "status": "", "checks": []any{}, "software": []any{}}
	if c != nil {
		out["status"] = c.Status
		if c.LastScan != "" {
			out["score"], out["issues"], out["last_scan"] = c.Score, c.Issues, c.LastScan
			out["checks"], out["software"] = json.RawMessage(c.Checks), json.RawMessage(c.Software)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) fleetScan(w http.ResponseWriter, r *http.Request, remediate bool) {
	p := s.fleetDeviceFor(w, r)
	if p == nil {
		return
	}
	if s.isSupportAgent(p.ID) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "support_agent_restricted"})
		return
	}
	id, err := s.queueFleetScan(s.eventActor(r), p.ID, remediate)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "command_queue_failed"})
		return
	}
	s.auditFleet(r, audit.ActionFleetScanRequested, p.ID, map[string]string{
		"remediate": strconv.FormatBool(remediate), "command_id": strconv.FormatInt(id, 10)})
	writeJSON(w, http.StatusAccepted, map[string]any{"success": true, "accepted": true, "command_id": id, "device_id": p.ID})
}

func (s *Server) handleFleetScan(w http.ResponseWriter, r *http.Request) { s.fleetScan(w, r, false) }

func (s *Server) handleFleetRemediate(w http.ResponseWriter, r *http.Request) {
	s.fleetScan(w, r, true)
}

// ── What agents report through the console ─────────────────────────────

// handleFleetAgentTaskResult takes a run's final answer from an agent
// that reports over REST: device_id is the device of the agent's token,
// run_id (or task_id: the task's last run) names the run.
func (s *Server) handleFleetAgentTaskResult(w http.ResponseWriter, r *http.Request) {
	body, ok := readFleetBody(w, r)
	if !ok {
		return
	}
	deviceID := body.text("device_id")
	if deviceID == "" {
		badFleetRequest(w, "device_id is missing")
		return
	}
	var run *db.FleetRun
	if runID, ok := body.number("run_id", 0); ok && runID > 0 {
		runs, _ := s.fleetRunsOfDevice(deviceID, 0, int64(runID))
		if len(runs) > 0 {
			run = runs[0]
		}
	} else if taskID, ok := body.number("task_id", 0); ok && taskID > 0 {
		runs, _ := s.fleetRunsOfDevice(deviceID, int64(taskID), 0)
		if len(runs) > 0 {
			run = runs[0]
		}
	}
	if run == nil || !run.Open() {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "No open run of this device"})
		return
	}
	status := body.text("status")
	switch status {
	case "success", "done", "completed":
		status = "ok"
	case "failed":
		status = "error"
	}
	result := map[string]any{"output": body.text("output")}
	if body.has("exit_code") {
		if code, ok := body.number("exit_code", 0); ok {
			result["exit_code"] = code
		}
	}
	raw, _ := json.Marshal(result)
	if status != "running" {
		_ = s.db.CompleteTelemetryCommand(deviceID, run.CommandID, status, string(raw))
	}
	s.fleetRunAnswer(deviceID, run.CommandID, status, raw)
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// fleetRunsOfDevice finds a device's run by its id, or the device's run
// in a task's last run.
func (s *Server) fleetRunsOfDevice(deviceID string, taskID, runID int64) ([]*db.FleetRun, error) {
	fleet := s.db.Fleet()
	if runID > 0 {
		// The run's task is not known: look through the device's open runs
		tasks, err := fleet.ListFleetTasks(db.FleetTaskFilter{DeviceID: deviceID})
		if err != nil {
			return nil, err
		}
		for _, t := range tasks {
			runs, _ := fleet.ListOpenFleetRuns(t.ID)
			for _, run := range runs {
				if run.ID == runID && run.DeviceID == deviceID {
					return []*db.FleetRun{run}, nil
				}
			}
		}
		return nil, nil
	}
	runs, err := fleet.ListFleetRuns(taskID, 0)
	if err != nil {
		return nil, err
	}
	for _, run := range runs {
		if run.DeviceID == deviceID {
			return []*db.FleetRun{run}, nil
		}
	}
	return nil, nil
}

// handleFleetAgentSoftware stores the installed programs an agent
// reports over REST for the device of its token.
func (s *Server) handleFleetAgentSoftware(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DeviceID string          `json:"device_id"`
		Software []fleetSoftware `json:"software"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2*1024*1024)).Decode(&body); err != nil || body.DeviceID == "" {
		badFleetRequest(w, "The request needs device_id and software")
		return
	}
	fleet := s.db.Fleet()
	current, err := fleet.GetFleetCompliance(body.DeviceID)
	if err != nil {
		writeInternalError(w, err, "fleet software")
		return
	}
	if body.Software == nil {
		body.Software = []fleetSoftware{}
	}
	software, _ := json.Marshal(body.Software)
	// The list goes in as the answer to a scan of its own
	if err := fleet.SaveFleetComplianceRequest(body.DeviceID, 0, false); err != nil {
		writeInternalError(w, err, "fleet software")
		return
	}
	record := &db.FleetCompliance{DeviceID: body.DeviceID, CommandID: 0, Score: 100, Software: string(software)}
	if current != nil && current.LastScan != "" {
		record.Score, record.Issues, record.Checks = current.Score, current.Issues, current.Checks
	}
	if err := fleet.SaveFleetComplianceResult(record); err != nil {
		writeInternalError(w, err, "fleet software")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "count": len(body.Software)})
}
