package db

import (
	"errors"
	"sort"
	"strings"
	"testing"
	"time"
)

func queueTestCommand(t *testing.T, store Database, deviceID, command string, expires time.Time) int64 {
	t.Helper()
	id, err := store.QueueTelemetryCommand(&TelemetryCommand{
		DeviceID: deviceID, Command: command, Args: `{}`,
		ExpiresAt: expires.UTC().Format("2006-01-02 15:04:05"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// TestFleetResourcesStore: resources belong to one organization, and a
// device's answer counts only for the list last sent to it.
func TestFleetResourcesStore(t *testing.T) {
	freshStores(t, func(t *testing.T, store Database) {
		fleet := store.Fleet()
		printer := &FleetResource{OrgID: "org-a", Name: "Printer", Type: "printer", Address: `\\srv\p1`, Scope: "org", Active: true}
		if err := fleet.CreateFleetResource(printer); err != nil || printer.ID == 0 {
			t.Fatalf("create: %v (id %d)", err, printer.ID)
		}
		other := &FleetResource{OrgID: "org-b", Name: "DNS", Type: "dns", Address: "1.1.1.1", Scope: "org"}
		if err := fleet.CreateFleetResource(other); err != nil {
			t.Fatal(err)
		}
		list, err := fleet.ListFleetResources("org-a")
		if err != nil || len(list) != 1 || list[0].Name != "Printer" || !list[0].Active {
			t.Fatalf("list of org-a: %+v %v", list, err)
		}
		if _, err := fleet.GetFleetResource("org-a", other.ID); !errors.Is(err, ErrFleetNotFound) {
			t.Errorf("another organization's resource: %v, want not found", err)
		}
		printer.Address, printer.Active = `\\srv\p2`, false
		if err := fleet.UpdateFleetResource(printer); err != nil {
			t.Fatal(err)
		}
		got, err := fleet.GetFleetResource("org-a", printer.ID)
		if err != nil || got.Address != `\\srv\p2` || got.Active {
			t.Errorf("after update: %+v %v", got, err)
		}
		if err := fleet.DeleteFleetResource("org-b", printer.ID); !errors.Is(err, ErrFleetNotFound) {
			t.Errorf("delete through another organization: %v", err)
		}
		if err := fleet.DeleteFleetResource("org-a", printer.ID); err != nil {
			t.Fatal(err)
		}

		if err := fleet.SaveFleetResourcesSent("dev1", "rev-1", 11); err != nil {
			t.Fatal(err)
		}
		// An answer to an older command changes nothing
		if err := fleet.SaveFleetResourcesResult("dev1", 10, true, "rev-0", `[]`); err != nil {
			t.Fatal(err)
		}
		state, err := fleet.GetFleetDeviceResources("dev1")
		if err != nil || state == nil || state.Applied != "" || state.Status != "pending" || state.Sent != "rev-1" {
			t.Fatalf("after a stale answer: %+v %v", state, err)
		}
		if err := fleet.SaveFleetResourcesResult("dev1", 11, true, "rev-1", `[{"id":3,"status":"ok"}]`); err != nil {
			t.Fatal(err)
		}
		states, err := fleet.ListFleetDeviceResources([]string{"dev1", "dev2"})
		if err != nil || len(states) != 1 || states["dev1"].Applied != "rev-1" || states["dev1"].Status != "applied" ||
			!strings.Contains(states["dev1"].Items, `"ok"`) {
			t.Errorf("after the answer: %+v %v", states["dev1"], err)
		}
		if err := fleet.SaveFleetResourcesSent("dev1", "rev-2", 12); err != nil {
			t.Fatal(err)
		}
		if err := fleet.SaveFleetResourcesResult("dev1", 12, false, "rev-2", `[]`); err != nil {
			t.Fatal(err)
		}
		state, _ = fleet.GetFleetDeviceResources("dev1")
		if state.Applied != "rev-1" || state.Status != "failed" {
			t.Errorf("a failed list: %+v, want rev-1 kept and failed", state)
		}
	})
}

func TestFleetResourceRevision(t *testing.T) {
	a := FleetResourceItem{ID: 1, Type: "dns", Name: "DNS", Address: "1.1.1.1"}
	b := FleetResourceItem{ID: 2, Type: "proxy", Name: "Proxy", Address: "http://p:8080"}
	if FleetResourceRevision(nil) != "" {
		t.Error("an empty list has a revision")
	}
	if FleetResourceRevision([]FleetResourceItem{a, b}) != FleetResourceRevision([]FleetResourceItem{b, a}) {
		t.Error("the revision depends on the order")
	}
	changed := b
	changed.Address = "http://p:3128"
	if FleetResourceRevision([]FleetResourceItem{a, b}) == FleetResourceRevision([]FleetResourceItem{a, changed}) {
		t.Error("a changed address keeps the revision")
	}
}

// TestFleetTasksStore: a task's status follows the device runs of its
// last run, the filters find tasks by status, device and organization,
// and the scheduler finds the tasks that are due.
func TestFleetTasksStore(t *testing.T) {
	freshStores(t, func(t *testing.T, store Database) {
		fleet := store.Fleet()
		task := &FleetTask{OrgID: "org-a", Name: "Clean", Type: "script", Targets: "dev1, dev2", ScheduleType: "immediate",
			Script: "echo hi", Retries: 1, Timeout: 60, CreatedBy: "alice"}
		if err := fleet.CreateFleetTask(task); err != nil || task.ID == 0 {
			t.Fatalf("create: %v", err)
		}
		other := &FleetTask{Name: "Later", Type: "restart", Targets: "dev3", ScheduleType: "once",
			NextRun: "2030-01-01T00:00:00Z", Timeout: 300}
		if err := fleet.CreateFleetTask(other); err != nil {
			t.Fatal(err)
		}
		batch, err := fleet.StartFleetTaskBatch(task.ID, "")
		if err != nil || batch != 1 {
			t.Fatalf("first run: %d %v", batch, err)
		}
		runs := map[string]*FleetRun{}
		for i, device := range []string{"dev1", "dev2"} {
			r := &FleetRun{TaskID: task.ID, Batch: batch, DeviceID: device, CommandID: int64(100 + i)}
			if err := fleet.CreateFleetRun(r); err != nil {
				t.Fatal(err)
			}
			runs[device] = r
		}
		if status, _ := fleet.RefreshFleetTaskStatus(task.ID); status != FleetRunPending {
			t.Errorf("no device started: %s, want pending", status)
		}
		runs["dev1"].Status = FleetRunRunning
		if err := fleet.UpdateFleetRun(runs["dev1"]); err != nil {
			t.Fatal(err)
		}
		if status, _ := fleet.RefreshFleetTaskStatus(task.ID); status != FleetRunRunning {
			t.Errorf("one device started: %s, want running", status)
		}
		zero, one := int64(0), int64(1)
		runs["dev1"].Status, runs["dev1"].ExitCode, runs["dev1"].Output = FleetRunSuccess, &zero, strings.Repeat("x", FleetRunOutputMax+10)+"END"
		runs["dev2"].Status, runs["dev2"].ExitCode = FleetRunFailed, &one
		for _, r := range runs {
			if err := fleet.UpdateFleetRun(r); err != nil {
				t.Fatal(err)
			}
		}
		if status, _ := fleet.RefreshFleetTaskStatus(task.ID); status != FleetRunFailed {
			t.Errorf("one device failed: %s, want failed", status)
		}
		got, err := fleet.GetFleetRunByCommand("dev1", 100)
		if err != nil || got == nil || got.ExitCode == nil || *got.ExitCode != 0 || len(got.Output) != FleetRunOutputMax ||
			!strings.HasSuffix(got.Output, "END") {
			t.Fatalf("run by command: %+v %v", got, err)
		}
		if got, _ := fleet.GetFleetRunByCommand("dev2", 100); got != nil {
			t.Error("another device's command found a run")
		}

		failed, err := fleet.ListFleetTasks(FleetTaskFilter{Status: "failed"})
		if err != nil || len(failed) != 1 || failed[0].ID != task.ID || failed[0].Counts["failed"] != 1 || failed[0].Counts["success"] != 1 {
			t.Fatalf("failed tasks: %+v %v", failed, err)
		}
		if l, _ := fleet.ListFleetTasks(FleetTaskFilter{DeviceID: "dev2"}); len(l) != 1 || l[0].ID != task.ID {
			t.Errorf("tasks of dev2: %+v", l)
		}
		if l, _ := fleet.ListFleetTasks(FleetTaskFilter{OrgID: "org-b"}); len(l) != 0 {
			t.Errorf("tasks of org-b: %+v", l)
		}
		if l, _ := fleet.ListFleetTasks(FleetTaskFilter{}); len(l) != 2 {
			t.Errorf("all tasks: %d", len(l))
		}

		// A new run starts the status over
		batch, _ = fleet.StartFleetTaskBatch(task.ID, "")
		if batch != 2 {
			t.Fatalf("second run %d", batch)
		}
		if status, _ := fleet.RefreshFleetTaskStatus(task.ID); status != FleetRunPending {
			t.Errorf("a new run without devices: %s", status)
		}
		if l, _ := fleet.ListFleetRuns(task.ID, 0); len(l) != 0 {
			t.Errorf("runs of the new batch: %+v", l)
		}
		if l, _ := fleet.ListFleetRuns(task.ID, 1); len(l) != 2 || l[0].DeviceID != "dev1" {
			t.Errorf("runs of batch 1: %+v", l)
		}

		due, err := fleet.ListDueFleetTasks(time.Date(2029, 12, 31, 0, 0, 0, 0, time.UTC))
		if err != nil || len(due) != 0 {
			t.Errorf("due before the time: %+v %v", due, err)
		}
		due, _ = fleet.ListDueFleetTasks(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
		if len(due) != 1 || due[0].ID != other.ID {
			t.Errorf("due at the time: %+v", due)
		}

		other.Name, other.Targets = "Renamed", "dev4"
		if err := fleet.UpdateFleetTask(other); err != nil {
			t.Fatal(err)
		}
		if got, _ := fleet.GetFleetTask(other.ID); got.Name != "Renamed" || got.Targets != "dev4" {
			t.Errorf("after update: %+v", got)
		}
	})
}

// TestFleetRunsGoStale: an open run whose command expired, was closed or
// is gone can no longer get an answer; one with an open command can.
// Deleting a task takes back the commands no device picked up.
func TestFleetRunsGoStale(t *testing.T) {
	freshStores(t, func(t *testing.T, store Database) {
		fleet := store.Fleet()
		task := &FleetTask{Name: "T", Type: "script", Targets: "d1", ScheduleType: "immediate", Timeout: 60}
		if err := fleet.CreateFleetTask(task); err != nil {
			t.Fatal(err)
		}
		batch, _ := fleet.StartFleetTaskBatch(task.ID, "")
		live := queueTestCommand(t, store, "d1", "fleet.run", time.Now().Add(time.Hour))
		expired := queueTestCommand(t, store, "d2", "fleet.run", time.Now().Add(-time.Minute))
		closed := queueTestCommand(t, store, "d3", "fleet.run", time.Now().Add(time.Hour))
		if err := store.CompleteTelemetryCommand("d3", closed, "ok", `{}`); err != nil {
			t.Fatal(err)
		}
		for device, command := range map[string]int64{"d1": live, "d2": expired, "d3": closed, "d4": 999999} {
			if err := fleet.CreateFleetRun(&FleetRun{TaskID: task.ID, Batch: batch, DeviceID: device, CommandID: command}); err != nil {
				t.Fatal(err)
			}
		}
		stale, err := fleet.ListStaleFleetRuns()
		if err != nil {
			t.Fatal(err)
		}
		var devices []string
		for _, r := range stale {
			devices = append(devices, r.DeviceID)
		}
		sort.Strings(devices)
		if strings.Join(devices, ",") != "d2,d3,d4" {
			t.Errorf("stale runs on %v, want d2,d3,d4", devices)
		}

		if err := fleet.DeleteFleetTask(task.ID); err != nil {
			t.Fatal(err)
		}
		if pending, _ := store.GetPendingTelemetryCommands("d1", 10); len(pending) != 0 {
			t.Errorf("the deleted task's command is still handed out: %+v", pending)
		}
		if _, err := fleet.GetFleetTask(task.ID); !errors.Is(err, ErrFleetNotFound) {
			t.Errorf("deleted task: %v", err)
		}
	})
}

// TestTelemetryCommandRunning: a command the device reported as running
// is no longer handed out, still takes its final answer, and only from
// its own device.
func TestTelemetryCommandRunning(t *testing.T) {
	freshStores(t, func(t *testing.T, store Database) {
		id := queueTestCommand(t, store, "d1", "fleet.run", time.Now().Add(time.Hour))
		other := queueTestCommand(t, store, "d2", "fleet.run", time.Now().Add(time.Hour))
		open, err := store.OpenTelemetryCommandIDs("d1", []int64{id, other, 424242})
		if err != nil || len(open) != 1 || !open[id] {
			t.Fatalf("open commands of d1: %v %v", open, err)
		}
		if err := store.MarkTelemetryCommandRunning("d2", id, time.Now().Add(2*time.Hour)); err != nil {
			t.Fatal(err)
		}
		if pending, _ := store.GetPendingTelemetryCommands("d1", 10); len(pending) != 1 {
			t.Fatalf("another device marked d1's command running: %+v", pending)
		}
		if err := store.MarkTelemetryCommandRunning("d1", id, time.Now().Add(2*time.Hour)); err != nil {
			t.Fatal(err)
		}
		if pending, _ := store.GetPendingTelemetryCommands("d1", 10); len(pending) != 0 {
			t.Errorf("a running command is handed out again: %+v", pending)
		}
		if open, _ := store.OpenTelemetryCommandIDs("d1", []int64{id}); !open[id] {
			t.Error("a running command takes no answer")
		}
		if err := store.CompleteTelemetryCommand("d1", id, "ok", `{"exit_code":0}`); err != nil {
			t.Fatal(err)
		}
		if open, _ := store.OpenTelemetryCommandIDs("d1", []int64{id}); open[id] {
			t.Error("a completed command still takes answers")
		}
		// Running with an expiry in the past: it takes no answer
		late := queueTestCommand(t, store, "d1", "fleet.run", time.Now().Add(time.Hour))
		if err := store.MarkTelemetryCommandRunning("d1", late, time.Now().Add(-time.Minute)); err != nil {
			t.Fatal(err)
		}
		if open, _ := store.OpenTelemetryCommandIDs("d1", []int64{late}); open[late] {
			t.Error("an expired running command takes answers")
		}
	})
}

func TestFleetWorkflowsAndComplianceStore(t *testing.T) {
	freshStores(t, func(t *testing.T, store Database) {
		fleet := store.Fleet()
		wf := &FleetWorkflow{Name: "Nightly", Nodes: `[{"id":"n1"}]`, Connections: `[]`, CreatedBy: "alice"}
		if err := fleet.CreateFleetWorkflow(wf); err != nil || wf.ID == 0 {
			t.Fatal(err)
		}
		scoped := &FleetWorkflow{OrgID: "org-a", Name: "Org", Nodes: `[]`, Connections: `[]`}
		if err := fleet.CreateFleetWorkflow(scoped); err != nil {
			t.Fatal(err)
		}
		if l, _ := fleet.ListFleetWorkflows(""); len(l) != 2 {
			t.Errorf("all workflows: %+v", l)
		}
		if l, _ := fleet.ListFleetWorkflows("org-a"); len(l) != 1 || l[0].ID != scoped.ID {
			t.Errorf("workflows of org-a: %+v", l)
		}
		wf.Name = "Nightly 2"
		if err := fleet.UpdateFleetWorkflow(wf); err != nil {
			t.Fatal(err)
		}
		for _, dry := range []bool{true, false} {
			if err := fleet.AddFleetWorkflowRun(&FleetWorkflowRun{WorkflowID: wf.ID, DryRun: dry, Status: "ok", Output: "x", StartedBy: "alice"}); err != nil {
				t.Fatal(err)
			}
		}
		runs, err := fleet.ListFleetWorkflowRuns(wf.ID, 10)
		if err != nil || len(runs) != 2 || runs[0].DryRun || !runs[1].DryRun || runs[0].TaskIDs != "[]" {
			t.Errorf("history: %+v %v", runs, err)
		}
		if err := fleet.DeleteFleetWorkflow(wf.ID); err != nil {
			t.Fatal(err)
		}
		if runs, _ := fleet.ListFleetWorkflowRuns(wf.ID, 10); len(runs) != 0 {
			t.Errorf("history of a deleted workflow: %+v", runs)
		}

		if err := fleet.SaveFleetComplianceRequest("d1", 7, false); err != nil {
			t.Fatal(err)
		}
		if err := fleet.SaveFleetComplianceResult(&FleetCompliance{DeviceID: "d1", CommandID: 6, Score: 10}); err == nil {
			t.Error("an answer to another command was stored")
		}
		if err := fleet.SaveFleetComplianceResult(&FleetCompliance{DeviceID: "d1", CommandID: 7, Score: 60, Issues: 2,
			Checks: `[{"key":"firewall","ok":false}]`, Software: `[{"name":"7-Zip"}]`}); err != nil {
			t.Fatal(err)
		}
		all, err := fleet.ListFleetCompliance([]string{"d1", "d2"})
		if err != nil || len(all) != 1 || all["d1"].Score != 60 || all["d1"].Issues != 2 || all["d1"].LastScan == "" ||
			all["d1"].Status != "ok" || !strings.Contains(all["d1"].Software, "7-Zip") {
			t.Errorf("compliance: %+v %v", all["d1"], err)
		}
		if err := fleet.SaveFleetComplianceRequest("d1", 8, true); err != nil {
			t.Fatal(err)
		}
		got, _ := fleet.GetFleetCompliance("d1")
		if got.Score != 60 || got.Status != "pending" || !got.Remediate || got.CommandID != 8 {
			t.Errorf("a new request keeps the last result: %+v", got)
		}
	})
}

func TestFleetTaskStatus(t *testing.T) {
	for _, c := range []struct {
		counts map[string]int
		want   string
	}{
		{nil, "pending"},
		{map[string]int{"pending": 2}, "pending"},
		{map[string]int{"pending": 1, "success": 1}, "running"},
		{map[string]int{"running": 1}, "running"},
		{map[string]int{"success": 2}, "success"},
		{map[string]int{"success": 1, "timeout": 1}, "failed"},
		{map[string]int{"unsupported": 1}, "failed"},
	} {
		if got := FleetTaskStatus(c.counts); got != c.want {
			t.Errorf("%v: %s, want %s", c.counts, got, c.want)
		}
	}
}
