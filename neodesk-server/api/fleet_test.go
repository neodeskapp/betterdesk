package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// fleetServer is newInternalServer with organization org-a holding both
// of its devices (PEERAAA1 on Windows, PEERBBB2 on Linux) and org-b with
// a third one, PEERCCC3.
func fleetServer(t *testing.T) (*Server, db.Database) {
	t.Helper()
	srv, database := newInternalServer(t, nil)
	if err := database.UpsertPeer(&db.Peer{ID: "PEERCCC3", Hostname: "host-c", OS: "macOS", Status: "OFFLINE"}); err != nil {
		t.Fatal(err)
	}
	for _, org := range []string{"org-a", "org-b"} {
		if err := database.CreateOrganization(&db.Organization{ID: org, Name: org, Slug: org, CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	for device, org := range map[string]string{"PEERAAA1": "org-a", "PEERBBB2": "org-a", "PEERCCC3": "org-b"} {
		if err := database.AssignDeviceToOrg(&db.OrgDevice{OrgID: org, DeviceID: device}); err != nil {
			t.Fatal(err)
		}
	}
	return srv, database
}

func decode(t *testing.T, body string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return out
}

func pendingCommands(t *testing.T, database db.Database, device string) []*db.TelemetryCommand {
	t.Helper()
	list, err := database.GetPendingTelemetryCommands(device, 20)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func commandsNamed(t *testing.T, database db.Database, device, name string) []*db.TelemetryCommand {
	t.Helper()
	var out []*db.TelemetryCommand
	for _, c := range pendingCommands(t, database, device) {
		if c.Command == name {
			out = append(out, c)
		}
	}
	return out
}

// deviceAnswers sends a device's results as its verified telemetry does.
func deviceAnswers(t *testing.T, srv *Server, device string, results ...map[string]any) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"telemetry_schema": 1, "results": results})
	if err != nil {
		t.Fatal(err)
	}
	srv.saveClientTelemetry(device, raw)
}

func makeSupportAgent(t *testing.T, database db.Database, device string) {
	t.Helper()
	if err := database.UpsertPeer(&db.Peer{ID: device, Hostname: "support", OS: "Windows", Status: "OFFLINE"}); err != nil {
		t.Fatal(err)
	}
	if err := database.SaveTelemetrySnapshot(&db.TelemetrySnapshot{DeviceID: device, Kind: "identity", Status: "ok",
		Payload: `{"product_sku":"neodesk-support"}`, CollectedAt: "2026-11-27T00:00:00Z"}); err != nil {
		t.Fatal(err)
	}
}

func createTask(t *testing.T, srv *Server, body map[string]any) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(body)
	status, text := asConsole(t, srv, http.MethodPost, "/api/fleet/tasks", string(raw))
	return status, decode(t, text)
}

// Every fleet route asks org.manage_devices, as the console's routes do,
// and the two agent routes take only the console.
func TestFleetRoutesNeedManageDevices(t *testing.T) {
	table := routeTable(t)
	n := 0
	for pattern, g := range table {
		if !strings.Contains(pattern, "/api/fleet/") {
			continue
		}
		n++
		if strings.HasSuffix(pattern, "/task-result") || strings.HasSuffix(pattern, "/software") {
			if !g.has("requireConsoleCaller") {
				t.Errorf("%s does not ask for the console", pattern)
			}
			continue
		}
		if len(g.permissions) != 1 || g.permissions[0] != auth.PermOrgManageDevices {
			t.Errorf("%s asks %v, want org.manage_devices", pattern, g.permissions)
		}
		if strings.Contains(pattern, "{orgId}") && !g.has("requireOrgMembership") {
			t.Errorf("%s does not check the organization", pattern)
		}
	}
	if n != 24 {
		t.Errorf("%d fleet routes, want 24 (every path the console passes on)", n)
	}

	srv, _ := fleetServer(t)
	viewer := Identity{Username: "watcher", Role: auth.RoleViewer}
	for _, path := range []string{"/api/fleet/tasks", "/api/fleet/workflows", "/api/fleet/compliance", "/api/fleet/resources/org-a"} {
		if status, body := callAs(t, srv, viewer, http.MethodGet, path, ""); status != http.StatusForbidden {
			t.Errorf("a viewer reads %s: %d %s", path, status, body)
		}
		if status, body := asConsole(t, srv, http.MethodGet, path, ""); status != http.StatusOK {
			t.Errorf("the console reads %s: %d %s", path, status, body)
		}
	}
	if status, _ := callAs(t, srv, viewer, http.MethodPost, "/api/fleet/tasks", `{"name":"x","type":"script","targets":"PEERAAA1","script":"id"}`); status != http.StatusForbidden {
		t.Errorf("a viewer creates a task: %d", status)
	}
}

// The devices of a task are entered as text: device ids, group names,
// org:, group: and tag:. A name that is nothing is refused with the names
// it could not find, a support agent named by id is refused, and one a
// group brings in is left out.
func TestFleetTaskTargets(t *testing.T) {
	srv, database := fleetServer(t)
	if err := database.UpdatePeerTags("PEERBBB2", "lab"); err != nil {
		t.Fatal(err)
	}
	makeSupportAgent(t, database, "SUPPORT9")
	if err := database.AssignDeviceToOrg(&db.OrgDevice{OrgID: "org-a", DeviceID: "SUPPORT9"}); err != nil {
		t.Fatal(err)
	}
	base := map[string]any{"name": "Who am I", "type": "script", "script": "whoami", "schedule_type": "once", "schedule_value": "2099-01-01T10:00"}
	with := func(targets string) map[string]any {
		body := map[string]any{"targets": targets}
		for k, v := range base {
			body[k] = v
		}
		return body
	}
	status, out := createTask(t, srv, with("PEERAAA1, NOPE1234, group:Nothing"))
	if status != http.StatusBadRequest || !strings.Contains(out["error"].(string), "NOPE1234, group:Nothing") {
		t.Errorf("unknown names: %d %v", status, out)
	}
	status, out = createTask(t, srv, with("SUPPORT9"))
	if status != http.StatusForbidden || out["code"] != "support_agent_restricted" {
		t.Errorf("a support agent by id: %d %v", status, out)
	}
	for targets, want := range map[string]string{
		"PEERAAA1 PEERBBB2":            "PEERAAA1,PEERBBB2",
		"org:org-a":                    "PEERAAA1,PEERBBB2",
		"tag:lab":                      "PEERBBB2",
		"org:org-b, tag:lab, PEERCCC3": "PEERCCC3,PEERBBB2",
	} {
		status, out := createTask(t, srv, with(targets))
		if status != http.StatusCreated {
			t.Errorf("%q: %d %v", targets, status, out)
			continue
		}
		var got []string
		for _, d := range out["devices"].([]any) {
			got = append(got, d.(string))
		}
		if strings.Join(got, ",") != want {
			t.Errorf("%q resolved to %v, want %s", targets, got, want)
		}
		if targets == "org:org-a" {
			if skipped := out["skipped"].([]any); len(skipped) != 1 || skipped[0] != "SUPPORT9" {
				t.Errorf("the organization's support agent is not named as left out: %v", skipped)
			}
			if task := out["task"].(map[string]any); task["org_id"] != "org-a" || task["targets"] != "org:org-a" {
				t.Errorf("the task keeps its text and organization: %v", task)
			}
		}
	}
	// A caller held to org-a cannot name org-b's device
	scoped := Identity{Username: "orgop", Role: auth.RoleOperator, OrgID: "org-a"}
	for _, targets := range []string{"PEERCCC3", "org:org-b"} {
		raw, _ := json.Marshal(with(targets))
		if status, body := callAs(t, srv, scoped, http.MethodPost, "/api/fleet/tasks", string(raw)); status != http.StatusBadRequest {
			t.Errorf("another organization's %s: %d %s", targets, status, body)
		}
	}
	raw, _ := json.Marshal(with("PEERAAA1"))
	if status, body := callAs(t, srv, scoped, http.MethodPost, "/api/fleet/tasks", string(raw)); status != http.StatusCreated {
		t.Fatalf("its own organization's device: %d %s", status, body)
	}
	// It sees only its organization's tasks
	status, body := callAs(t, srv, scoped, http.MethodGet, "/api/fleet/tasks", "")
	for _, task := range decode(t, body)["tasks"].([]any) {
		if org := task.(map[string]any)["org_id"]; org != "org-a" {
			t.Errorf("an organization's operator sees a task of %q", org)
		}
	}
	if status != http.StatusOK {
		t.Errorf("list: %d", status)
	}
}

// Running a task queues one fleet.run per device and one run row each;
// the devices' answers set the runs and the task's status. A "running"
// answer keeps the command open and stops handing it out; a failed run
// goes again while retries are left; a device answers only its own
// commands.
func TestFleetTaskRunsOnDevices(t *testing.T) {
	srv, database := fleetServer(t)
	status, out := createTask(t, srv, map[string]any{
		"name": "Disk", "type": "script", "targets": "PEERAAA1, PEERBBB2", "script": "df -h", "retries": 1, "timeout": "120",
		"created_by": "alice",
	})
	if status != http.StatusCreated || out["queued"].(float64) != 2 {
		t.Fatalf("create: %d %v", status, out)
	}
	taskID := int64(out["id"].(float64))
	commands := map[string]*db.TelemetryCommand{}
	for _, device := range []string{"PEERAAA1", "PEERBBB2"} {
		list := commandsNamed(t, database, device, fleetRunCommand)
		if len(list) != 1 {
			t.Fatalf("%s has %d fleet.run commands", device, len(list))
		}
		commands[device] = list[0]
		var args fleetRunArgs
		if err := json.Unmarshal([]byte(list[0].Args), &args); err != nil || args.TaskID != taskID || args.RunID == 0 ||
			args.Type != "script" || args.Script != "df -h" || args.Shell != "auto" || args.Timeout != 120 {
			t.Errorf("args of %s: %s", device, list[0].Args)
		}
	}
	task, _ := database.Fleet().GetFleetTask(taskID)
	if task.Status != "pending" || task.CreatedBy != "alice" || task.LastRun == "" {
		t.Errorf("after queueing: %+v", task)
	}

	a, b := commands["PEERAAA1"], commands["PEERBBB2"]
	// B cannot answer A's command
	deviceAnswers(t, srv, "PEERBBB2", map[string]any{"id": a.ID, "status": "ok", "result": map[string]any{"exit_code": 0, "output": "forged"}})
	if runs, _ := database.Fleet().ListFleetRuns(taskID, 0); runs[0].Status != "pending" {
		t.Fatalf("another device answered A's run: %+v", runs[0])
	}

	deviceAnswers(t, srv, "PEERAAA1", map[string]any{"id": a.ID, "status": "running", "result": map[string]any{"run_id": 1, "started_at": "2026-11-27T10:00:00Z"}})
	if left := commandsNamed(t, database, "PEERAAA1", fleetRunCommand); len(left) != 0 {
		t.Error("a running command is handed out again")
	}
	if task, _ := database.Fleet().GetFleetTask(taskID); task.Status != "running" {
		t.Errorf("one device running: %s", task.Status)
	}
	deviceAnswers(t, srv, "PEERAAA1", map[string]any{"id": a.ID, "status": "ok", "result": map[string]any{
		"run_id": 1, "exit_code": 0, "output": "C: 40% used", "started_at": "2026-11-27T10:00:00Z", "finished_at": "2026-11-27T10:00:05Z"}})
	deviceAnswers(t, srv, "PEERBBB2", map[string]any{"id": b.ID, "status": "ok", "result": map[string]any{"exit_code": 2, "output": "df: no"}})

	runs, _ := database.Fleet().ListFleetRuns(taskID, 0)
	if runs[0].Status != "success" || runs[0].Output != "C: 40% used" || runs[0].FinishedAt != "2026-11-27T10:00:05Z" {
		t.Errorf("A's run: %+v", runs[0])
	}
	// B failed with retries left: attempt 2 is queued
	if runs[1].Status != "pending" || runs[1].Attempt != 2 || runs[1].CommandID == b.ID {
		t.Fatalf("B's run after a failure with a retry left: %+v", runs[1])
	}
	if again := commandsNamed(t, database, "PEERBBB2", fleetRunCommand); len(again) != 1 || again[0].ID != runs[1].CommandID {
		t.Fatalf("B's retry command: %+v", again)
	}
	// The old command takes no answer any more
	deviceAnswers(t, srv, "PEERBBB2", map[string]any{"id": b.ID, "status": "ok", "result": map[string]any{"exit_code": 0}})
	if runs, _ := database.Fleet().ListFleetRuns(taskID, 0); runs[1].Status != "pending" {
		t.Errorf("an answer to the first attempt closed the second: %+v", runs[1])
	}
	deviceAnswers(t, srv, "PEERBBB2", map[string]any{"id": runs[1].CommandID, "status": "timeout", "result": map[string]any{"output": "still running"}})
	runs, _ = database.Fleet().ListFleetRuns(taskID, 0)
	if runs[1].Status != "timeout" || runs[1].Attempt != 2 {
		t.Errorf("B after its last attempt: %+v", runs[1])
	}
	if task, _ := database.Fleet().GetFleetTask(taskID); task.Status != "failed" {
		t.Errorf("one device timed out: %s", task.Status)
	}

	status, body := asConsole(t, srv, http.MethodGet, "/api/fleet/tasks/"+strconv.FormatInt(taskID, 10)+"/output", "")
	output := decode(t, body)
	if status != http.StatusOK || !strings.Contains(output["output"].(string), "[PEERAAA1 (host-a)] success, exit code 0\nC: 40% used") ||
		len(output["runs"].([]any)) != 2 {
		t.Errorf("output: %d %s", status, body)
	}
	status, body = asConsole(t, srv, http.MethodGet, "/api/fleet/tasks?status=failed", "")
	if list := decode(t, body)["tasks"].([]any); status != http.StatusOK || len(list) != 1 {
		t.Errorf("failed tasks: %s", body)
	}
	if status, body = asConsole(t, srv, http.MethodGet, "/api/fleet/tasks?device_id=PEERCCC3", ""); len(decode(t, body)["tasks"].([]any)) != 0 {
		t.Errorf("tasks of PEERCCC3: %s", body)
	}

	// Retry: the timed out run goes again; with none failed there is nothing to retry
	status, body = asConsole(t, srv, http.MethodPost, "/api/fleet/tasks/"+strconv.FormatInt(taskID, 10)+"/retry", "")
	if status != http.StatusOK || decode(t, body)["queued"].(float64) != 1 {
		t.Fatalf("retry: %d %s", status, body)
	}
	runs, _ = database.Fleet().ListFleetRuns(taskID, 0)
	if runs[1].Status != "pending" || runs[1].Attempt != 3 {
		t.Errorf("after the retry: %+v", runs[1])
	}
	if status, _ = asConsole(t, srv, http.MethodPost, "/api/fleet/tasks/"+strconv.FormatInt(taskID, 10)+"/retry", ""); status != http.StatusBadRequest {
		t.Errorf("a retry without failed runs: %d", status)
	}

	// Delete takes back the command no device picked up
	if status, _ = asConsole(t, srv, http.MethodDelete, "/api/fleet/tasks/"+strconv.FormatInt(taskID, 10), ""); status != http.StatusOK {
		t.Fatalf("delete: %d", status)
	}
	if left := commandsNamed(t, database, "PEERBBB2", fleetRunCommand); len(left) != 0 {
		t.Errorf("commands of a deleted task: %+v", left)
	}
}

// A task for a support agent's group never reaches the agent, and a
// device that has not finished an earlier run of the task is not queued
// again by the next one.
func TestFleetTaskSkipsSupportAgentsAndBusyDevices(t *testing.T) {
	srv, database := fleetServer(t)
	makeSupportAgent(t, database, "SUPPORT9")
	if err := database.AssignDeviceToOrg(&db.OrgDevice{OrgID: "org-a", DeviceID: "SUPPORT9"}); err != nil {
		t.Fatal(err)
	}
	status, out := createTask(t, srv, map[string]any{"name": "Every minute", "type": "update", "targets": "org:org-a",
		"schedule_type": "cron", "schedule_value": "* * * * *"})
	if status != http.StatusCreated {
		t.Fatalf("create: %d %v", status, out)
	}
	taskID := int64(out["id"].(float64))
	task, _ := database.Fleet().GetFleetTask(taskID)
	if task.NextRun == "" || task.Batch != 0 {
		t.Fatalf("a cron task before its time: %+v", task)
	}
	next, _ := time.Parse(time.RFC3339, task.NextRun)
	srv.fleetTick(next)
	task, _ = database.Fleet().GetFleetTask(taskID)
	if task.Batch != 1 || task.NextRun <= next.UTC().Format(time.RFC3339) {
		t.Fatalf("after its time: %+v", task)
	}
	if got := commandsNamed(t, database, "SUPPORT9", fleetRunCommand); len(got) != 0 {
		t.Errorf("the support agent got %d commands", len(got))
	}
	if got := commandsNamed(t, database, "PEERAAA1", fleetRunCommand); len(got) != 1 {
		t.Fatalf("PEERAAA1 got %d commands", len(got))
	}
	// The next minute: both devices still have the first run open
	next2, _ := time.Parse(time.RFC3339, task.NextRun)
	srv.fleetTick(next2)
	if got := commandsNamed(t, database, "PEERAAA1", fleetRunCommand); len(got) != 1 {
		t.Errorf("a second run queued %d commands for a device that has not finished the first", len(got))
	}
}

// The scheduler closes a run its device can no longer answer as timed
// out.
func TestFleetSchedulerClosesStaleRuns(t *testing.T) {
	srv, database := fleetServer(t)
	status, out := createTask(t, srv, map[string]any{"name": "T", "type": "restart", "targets": "PEERAAA1"})
	if status != http.StatusCreated {
		t.Fatalf("create: %d %v", status, out)
	}
	taskID := int64(out["id"].(float64))
	command := commandsNamed(t, database, "PEERAAA1", fleetRunCommand)[0]
	// The device started it and then restarted: the command expires
	deviceAnswers(t, srv, "PEERAAA1", map[string]any{"id": command.ID, "status": "running", "result": map[string]any{}})
	if err := database.MarkTelemetryCommandRunning("PEERAAA1", command.ID, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	srv.fleetTick(time.Now())
	runs, _ := database.Fleet().ListFleetRuns(taskID, 0)
	if len(runs) != 1 || runs[0].Status != "timeout" {
		t.Errorf("a run whose command expired: %+v", runs)
	}
}

// Resources go to the devices of the organization as their full list; a
// device that applied the current list is not sent it again, a change
// sends it again, and the list shows how many devices applied it.
func TestFleetResourcesReachDevices(t *testing.T) {
	srv, database := fleetServer(t)
	makeSupportAgent(t, database, "SUPPORT9")
	if err := database.AssignDeviceToOrg(&db.OrgDevice{OrgID: "org-a", DeviceID: "SUPPORT9"}); err != nil {
		t.Fatal(err)
	}
	status, body := asConsole(t, srv, http.MethodPost, "/api/fleet/resources/org-a",
		`{"name":"Office printer","type":"printer","address":"\\\\srv\\office","scope":"org","scope_value":""}`)
	if status != http.StatusCreated || decode(t, body)["queued"].(float64) != 2 {
		t.Fatalf("create: %d %s", status, body)
	}
	if status, body := asConsole(t, srv, http.MethodPost, "/api/fleet/resources/org-a", `{"name":"x","type":"fax","address":"y"}`); status != http.StatusBadRequest {
		t.Errorf("an unknown type: %d %s", status, body)
	}
	if status, _ := asConsole(t, srv, http.MethodPost, "/api/fleet/resources/nope", `{"name":"x","type":"dns","address":"1.1.1.1"}`); status != http.StatusNotFound {
		t.Errorf("an unknown organization: %d", status)
	}
	if status, _ := asConsole(t, srv, http.MethodPost, "/api/fleet/resources/org-a", `{"name":"x","type":"dns","address":"1.1.1.1","scope":"device","scope_value":"PEERCCC3"}`); status != http.StatusBadRequest {
		t.Errorf("a device of another organization: %d", status)
	}
	if got := commandsNamed(t, database, "SUPPORT9", fleetResourcesCommand); len(got) != 0 {
		t.Error("the support agent got resources")
	}
	if got := commandsNamed(t, database, "PEERCCC3", fleetResourcesCommand); len(got) != 0 {
		t.Error("a device of another organization got resources")
	}
	cmd := commandsNamed(t, database, "PEERAAA1", fleetResourcesCommand)
	if len(cmd) != 1 {
		t.Fatalf("PEERAAA1 has %d resource lists", len(cmd))
	}
	var args fleetResourcesArgs
	if err := json.Unmarshal([]byte(cmd[0].Args), &args); err != nil || len(args.Resources) != 1 ||
		args.Resources[0].Address != `\\srv\office` || args.Revision != db.FleetResourceRevision(args.Resources) {
		t.Fatalf("args: %s", cmd[0].Args)
	}
	deviceAnswers(t, srv, "PEERAAA1", map[string]any{"id": cmd[0].ID, "status": "ok", "result": map[string]any{
		"revision": args.Revision, "items": []map[string]any{{"id": args.Resources[0].ID, "status": "ok"}}}})

	status, body = asConsole(t, srv, http.MethodGet, "/api/fleet/resources/org-a", "")
	list := decode(t, body)["resources"].([]any)
	res := list[0].(map[string]any)
	if status != http.StatusOK || len(list) != 1 || res["devices"].(float64) != 3 || res["applied"].(float64) != 1 {
		// SUPPORT9 is in the organization: it counts but never applies
		t.Errorf("list: %d %s", status, body)
	}

	// A heartbeat of the device that applied it queues nothing, nor one of
	// the device that has its list on the way
	srv.fleetHeartbeat("PEERAAA1")
	srv.fleetHeartbeat("PEERBBB2")
	if got := commandsNamed(t, database, "PEERBBB2", fleetResourcesCommand); len(got) != 1 {
		t.Errorf("a list on its way was queued again: %d", len(got))
	}
	if got := commandsNamed(t, database, "PEERAAA1", fleetResourcesCommand); len(got) != 0 {
		t.Errorf("an applied list was queued again: %d", len(got))
	}
	// A change goes out again, to both
	id := strconv.FormatInt(int64(res["id"].(float64)), 10)
	status, body = asConsole(t, srv, http.MethodPut, "/api/fleet/resources/org-a/"+id,
		`{"name":"Office printer","type":"printer","address":"\\\\srv\\office2","scope":"org","scope_value":""}`)
	if status != http.StatusOK || decode(t, body)["queued"].(float64) != 2 {
		t.Fatalf("update: %d %s", status, body)
	}
	if got := commandsNamed(t, database, "PEERAAA1", fleetResourcesCommand); len(got) != 1 || !strings.Contains(got[0].Args, `office2`) {
		t.Errorf("after the change: %+v", got)
	}
	// Deleting it sends the empty list to each device
	if status, _ := asConsole(t, srv, http.MethodDelete, "/api/fleet/resources/org-a/"+id, ""); status != http.StatusOK {
		t.Fatalf("delete: %d", status)
	}
	got := commandsNamed(t, database, "PEERAAA1", fleetResourcesCommand)
	if len(got) == 0 || !strings.Contains(got[len(got)-1].Args, `"resources":[]`) {
		t.Errorf("after the delete: %+v", got)
	}
	// Another organization's member may not read org-a's resources
	outsider := Identity{Username: "outsider", Role: auth.RoleOperator}
	if status, _ := callAs(t, srv, outsider, http.MethodGet, "/api/fleet/resources/org-a", ""); status != http.StatusForbidden {
		t.Errorf("a non-member reads the resources: %d", status)
	}
}

// Scans queue fleet.compliance; the device's answer gives the score
// (passed / all * 100) and the issues; the list shows every device of
// the organization, unscanned ones without a score.
func TestFleetCompliance(t *testing.T) {
	srv, database := fleetServer(t)
	makeSupportAgent(t, database, "SUPPORT9")
	if status, body := asConsole(t, srv, http.MethodPost, "/api/fleet/compliance/SUPPORT9/scan", "{}"); status != http.StatusForbidden ||
		!strings.Contains(body, "support_agent_restricted") {
		t.Errorf("scan a support agent: %d %s", status, body)
	}
	if status, _ := asConsole(t, srv, http.MethodPost, "/api/fleet/compliance/NOPE1234/scan", "{}"); status != http.StatusNotFound {
		t.Errorf("scan an unknown device: %d", status)
	}
	if status, _ := asConsole(t, srv, http.MethodPost, "/api/fleet/compliance/PEERAAA1/remediate", `{"action":"auto"}`); status != http.StatusAccepted {
		t.Fatalf("remediate: %d", status)
	}
	cmd := commandsNamed(t, database, "PEERAAA1", fleetComplianceCommand)
	if len(cmd) != 1 || cmd[0].Args != `{"remediate":true}` {
		t.Fatalf("command: %+v", cmd)
	}
	deviceAnswers(t, srv, "PEERAAA1", map[string]any{"id": cmd[0].ID, "status": "ok", "result": map[string]any{
		"software": []map[string]any{{"name": "7-Zip", "version": "24.08", "publisher": "Igor Pavlov"}},
		"checks": []map[string]any{
			{"key": "firewall", "ok": true}, {"key": "disk_encryption", "ok": false, "detail": "off"},
			{"key": "os_updates", "ok": true}, {"key": "antivirus", "ok": true}, {"key": "screen_lock", "ok": false},
		}}})
	status, body := asConsole(t, srv, http.MethodGet, "/api/fleet/compliance?org_id=org-a", "")
	devices := decode(t, body)["devices"].([]any)
	if status != http.StatusOK || len(devices) != 2 {
		t.Fatalf("list: %d %s", status, body)
	}
	byID := map[string]map[string]any{}
	for _, d := range devices {
		row := d.(map[string]any)
		byID[row["device_id"].(string)] = row
	}
	if a := byID["PEERAAA1"]; a["score"].(float64) != 60 || a["issues"].(float64) != 2 || a["last_scan"] == "" || a["hostname"] != "host-a" {
		t.Errorf("scanned device: %v", a)
	}
	if b := byID["PEERBBB2"]; b["score"] != nil || b["last_scan"] != "" {
		t.Errorf("unscanned device: %v", b)
	}
	status, body = asConsole(t, srv, http.MethodGet, "/api/fleet/compliance/PEERAAA1", "")
	if one := decode(t, body); status != http.StatusOK || len(one["software"].([]any)) != 1 || len(one["checks"].([]any)) != 5 {
		t.Errorf("device: %d %s", status, body)
	}

	// The daily scan asks every device of an organization once
	if n := srv.scheduleFleetScans(time.Now()); n != 2 {
		t.Errorf("the daily scan queued %d, want PEERBBB2 and PEERCCC3", n)
	}
	if n := srv.scheduleFleetScans(time.Now()); n != 0 {
		t.Errorf("a second daily round queued %d", n)
	}
	if n := srv.scheduleFleetScans(time.Now().Add(25 * time.Hour)); n != 3 {
		t.Errorf("a day later the round queued %d", n)
	}
}

// A workflow walks from its triggers: the OS check narrows the devices,
// the script step makes one task for what is left, a dry run only says
// so, and steps that cannot run say that they do not.
func TestFleetWorkflowExecute(t *testing.T) {
	srv, database := fleetServer(t)
	wf := `{"name":"Patch Windows","created_by":"alice","nodes":[
		{"id":"n1","type":"trigger_manual","category":"trigger","label":"Start","x":0,"y":0,"config":{"targets":"PEERAAA1, PEERBBB2"}},
		{"id":"n2","type":"cond_os","category":"condition","label":"Windows only","config":{"os":"windows"}},
		{"id":"n3","type":"action_script","category":"action","label":"Update","config":{"script":"winget upgrade --all","shell":"powershell"}},
		{"id":"n4","type":"action_message","category":"action","label":"Tell","config":{"message":"hi"}}
	],"connections":[{"id":"c5","from":"n1","to":"n2"},{"id":"c6","from":"n2","to":"n3"},{"id":"c7","from":"n3","to":"n4"}]}`
	status, body := asConsole(t, srv, http.MethodPost, "/api/fleet/workflows", wf)
	if status != http.StatusCreated {
		t.Fatalf("save: %d %s", status, body)
	}
	id := strconv.FormatInt(int64(decode(t, body)["id"].(float64)), 10)
	status, body = asConsole(t, srv, http.MethodGet, "/api/fleet/workflows/"+id, "")
	if data := decode(t, body)["data"].(map[string]any); status != http.StatusOK || len(data["nodes"].([]any)) != 4 || data["name"] != "Patch Windows" {
		t.Fatalf("load: %d %s", status, body)
	}
	if status, _ := asConsole(t, srv, http.MethodPost, "/api/fleet/workflows", `{"name":"Bad","nodes":[{"id":"n1","type":"rocket"}],"connections":[]}`); status != http.StatusBadRequest {
		t.Errorf("an unknown step: %d", status)
	}

	status, body = asConsole(t, srv, http.MethodPost, "/api/fleet/workflows/"+id+"/execute", `{"dry_run":true}`)
	dry := decode(t, body)
	output, _ := dry["output"].(string)
	if status != http.StatusOK || !strings.Contains(output, "Windows only: 1 device (PEERAAA1) on windows") ||
		!strings.Contains(output, "Tell: not run") || !strings.Contains(output, "1 tasks would start") {
		t.Errorf("dry run: %d %s", status, body)
	}
	if got := commandsNamed(t, database, "PEERAAA1", fleetRunCommand); len(got) != 0 {
		t.Fatal("a dry run queued a command")
	}

	status, body = asConsole(t, srv, http.MethodPost, "/api/fleet/workflows/"+id+"/execute", `{"dry_run":false}`)
	if status != http.StatusOK || len(decode(t, body)["tasks"].([]any)) != 1 {
		t.Fatalf("run: %d %s", status, body)
	}
	got := commandsNamed(t, database, "PEERAAA1", fleetRunCommand)
	if len(got) != 1 || !strings.Contains(got[0].Args, "winget upgrade --all") {
		t.Errorf("PEERAAA1: %+v", got)
	}
	if other := commandsNamed(t, database, "PEERBBB2", fleetRunCommand); len(other) != 0 {
		t.Errorf("the Linux device got the Windows step: %+v", other)
	}
	status, body = asConsole(t, srv, http.MethodGet, "/api/fleet/workflows/"+id+"/history", "")
	if history := decode(t, body)["history"].([]any); status != http.StatusOK || len(history) != 2 {
		t.Errorf("history: %d %s", status, body)
	}

	// A trigger without devices stops the run with a 400 and queues nothing
	status, body = asConsole(t, srv, http.MethodPost, "/api/fleet/workflows/draft/execute", `{"dry_run":false,
		"nodes":[{"id":"n1","type":"trigger_manual","config":{}},{"id":"n2","type":"action_restart","config":{"service":"spooler"}}],
		"connections":[{"from":"n1","to":"n2"}]}`)
	if status != http.StatusBadRequest || !strings.Contains(body, "set the devices") {
		t.Errorf("a trigger without devices: %d %s", status, body)
	}
}

// The availability probe of the console finds the fleet routes.
func TestFleetIsAvailable(t *testing.T) {
	srv, _ := fleetServer(t)
	if status, body := asConsole(t, srv, http.MethodGet, "/api/fleet/tasks", ""); status != http.StatusOK || !strings.Contains(body, `"tasks":[]`) {
		t.Errorf("GET /api/fleet/tasks: %d %s", status, body)
	}
}

// A verified heartbeat of a device of an organization with resources gets
// its list in the sealed answer.
func TestFleetResourcesGoOutWithTheHeartbeat(t *testing.T) {
	srv, deviceID, deviceUUID, devicePriv, responsePub, responsePriv := newConnectionModeFixture(t)
	if err := srv.db.CreateOrganization(&db.Organization{ID: "org-h", Name: "H", Slug: "h", CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.AssignDeviceToOrg(&db.OrgDevice{OrgID: "org-h", DeviceID: deviceID}); err != nil {
		t.Fatal(err)
	}
	if err := srv.db.Fleet().CreateFleetResource(&db.FleetResource{OrgID: "org-h", Name: "DNS", Type: "dns", Address: "9.9.9.9", Scope: "org", Active: true}); err != nil {
		t.Fatal(err)
	}
	payload := desktopIdentity(deviceID, deviceUUID, "")
	resp := postHeartbeat(t, srv, deviceID, deviceUUID, sealClientHeartbeat(t, srv, deviceID, devicePriv, 1, payload, responsePub))
	sealed := openSealedHeartbeat(t, srv, resp, responsePriv)
	commands, _ := sealed["telemetry_commands"].([]any)
	if len(commands) != 1 {
		t.Fatalf("telemetry_commands %v, want the resource list", sealed["telemetry_commands"])
	}
	cmd := commands[0].(map[string]any)
	args, _ := cmd["args"].(map[string]any)
	if cmd["command"] != fleetResourcesCommand || args["revision"] == "" || len(args["resources"].([]any)) != 1 {
		t.Errorf("command %v", cmd)
	}
}
