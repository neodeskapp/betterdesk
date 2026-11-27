package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/db"
	"github.com/neodeskapp/neodesk/neodesk-server/internal/cron"
)

// The step types of the workflow builder (web-ui/src/lib/canvas/workflow.ts).
var fleetStepTypes = map[string]string{
	"trigger_manual": "trigger", "trigger_schedule": "trigger", "trigger_event": "trigger",
	"cond_os": "condition", "cond_threshold": "condition", "cond_time_window": "condition",
	"action_script": "action", "action_install": "action", "action_restart": "action",
	"action_file_deploy": "action", "action_message": "action",
	"result_log": "result", "result_alert": "result", "result_chain": "result",
	"transform_parse": "transform", "transform_filter": "transform", "transform_delay": "transform",
}

const (
	fleetMaxSteps       = 200
	fleetMaxConnections = 400
	fleetMaxActions     = 50
	fleetMaxChain       = 3
)

// wfNode is a workflow step as the builder saves it.
type wfNode struct {
	ID     string
	Type   string
	Label  string
	Config map[string]string
}

type wfConn struct{ From, To string }

// wfGraph is a workflow's steps and connections, with the raw JSON kept
// for saving.
type wfGraph struct {
	nodes    []wfNode
	byID     map[string]*wfNode
	next     map[string][]string
	rawNodes json.RawMessage
	rawConns json.RawMessage
}

func wfText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	}
	raw, _ := json.Marshal(v)
	return string(raw)
}

// parseWorkflow reads and checks steps and connections: known step types,
// each id once, connections between steps that exist and never into a
// trigger.
func parseWorkflow(nodesRaw, connsRaw json.RawMessage) (*wfGraph, error) {
	if len(nodesRaw) == 0 || string(nodesRaw) == "null" {
		nodesRaw = json.RawMessage(`[]`)
	}
	if len(connsRaw) == 0 || string(connsRaw) == "null" {
		connsRaw = json.RawMessage(`[]`)
	}
	var nodes []map[string]any
	if err := json.Unmarshal(nodesRaw, &nodes); err != nil {
		return nil, fmt.Errorf("the steps are no list")
	}
	var conns []map[string]any
	if err := json.Unmarshal(connsRaw, &conns); err != nil {
		return nil, fmt.Errorf("the connections are no list")
	}
	if len(nodes) > fleetMaxSteps {
		return nil, fmt.Errorf("a workflow has at most %d steps", fleetMaxSteps)
	}
	if len(conns) > fleetMaxConnections {
		return nil, fmt.Errorf("a workflow has at most %d connections", fleetMaxConnections)
	}
	g := &wfGraph{byID: map[string]*wfNode{}, next: map[string][]string{}}
	for _, n := range nodes {
		node := wfNode{ID: wfText(n["id"]), Type: wfText(n["type"]), Label: wfText(n["label"]), Config: map[string]string{}}
		if node.ID == "" {
			return nil, fmt.Errorf("a step has no id")
		}
		if fleetStepTypes[node.Type] == "" {
			return nil, fmt.Errorf("step %s has the unknown type %q", node.ID, node.Type)
		}
		if _, dup := g.byID[node.ID]; dup {
			return nil, fmt.Errorf("two steps have the id %s", node.ID)
		}
		if config, ok := n["config"].(map[string]any); ok {
			for k, v := range config {
				node.Config[k] = strings.TrimSpace(wfText(v))
			}
		}
		g.nodes = append(g.nodes, node)
		g.byID[node.ID] = nil
	}
	for i := range g.nodes {
		g.byID[g.nodes[i].ID] = &g.nodes[i]
	}
	for _, c := range conns {
		from, to := wfText(c["from"]), wfText(c["to"])
		if g.byID[from] == nil || g.byID[to] == nil {
			return nil, fmt.Errorf("a connection joins a step that does not exist")
		}
		if fleetStepTypes[g.byID[to].Type] == "trigger" {
			return nil, fmt.Errorf("a connection leads into a trigger")
		}
		g.next[from] = append(g.next[from], to)
	}
	g.rawNodes, _ = json.Marshal(nodes)
	g.rawConns, _ = json.Marshal(conns)
	if nodes == nil {
		g.rawNodes = json.RawMessage(`[]`)
	}
	if conns == nil {
		g.rawConns = json.RawMessage(`[]`)
	}
	return g, nil
}

// ── Planning ───────────────────────────────────────────────────────────

// wfAction is a task a workflow run makes.
type wfAction struct {
	key      string
	name     string
	taskType string
	script   string
	devices  []string
	have     map[string]bool
	delay    int
}

// wfPlan is what a workflow run would do: lines to show, problems that
// stop it, and the tasks it makes.
type wfPlan struct {
	s        *Server
	r        *http.Request
	now      time.Time
	lines    []string
	problems []string
	actions  []*wfAction
	byKey    map[string]*wfAction
	seen     map[int64]bool
	orgID    string
}

func (p *wfPlan) say(depth int, format string, args ...any) {
	p.lines = append(p.lines, strings.Repeat("  ", depth)+fmt.Sprintf(format, args...))
}

func (p *wfPlan) problem(depth int, format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	p.problems = append(p.problems, text)
	p.say(depth, "Problem: %s", text)
}

func stepName(n *wfNode) string {
	if n.Label != "" {
		return n.Label
	}
	return n.Type
}

func countDevices(devices []string) string {
	if len(devices) == 1 {
		return "1 device (" + devices[0] + ")"
	}
	shown := devices
	if len(shown) > 10 {
		shown = shown[:10]
	}
	more := ""
	if len(devices) > len(shown) {
		more = fmt.Sprintf(" and %d more", len(devices)-len(shown))
	}
	return fmt.Sprintf("%d devices (%s%s)", len(devices), strings.Join(shown, ", "), more)
}

// planGraph walks a workflow from its triggers (only the one named
// trigger when onlyTrigger is set) and notes what each step does.
func (p *wfPlan) planGraph(name string, g *wfGraph, onlyTrigger string, depth int) {
	triggers := 0
	for i := range g.nodes {
		n := &g.nodes[i]
		if fleetStepTypes[n.Type] != "trigger" || (onlyTrigger != "" && n.ID != onlyTrigger) {
			continue
		}
		triggers++
		targets := n.Config["targets"]
		if targets == "" {
			p.problem(depth, "%s: set the devices it runs on", stepName(n))
			continue
		}
		resolved, terr := p.s.resolveFleetTargets(p.r, targets)
		if terr != nil {
			p.problem(depth, "%s: %s", stepName(n), terr.message)
			continue
		}
		switch n.Type {
		case "trigger_schedule":
			if _, err := cron.Parse(n.Config["cron"]); err != nil {
				p.problem(depth, "%s: the cron expression cannot be read (%v)", stepName(n), err)
				continue
			}
		case "trigger_event":
			p.say(depth, "%s: events are not watched yet; a run starts this branch now", stepName(n))
		}
		p.say(depth, "%s: %s", stepName(n), countDevices(resolved.Devices))
		if len(resolved.Skipped) > 0 {
			p.say(depth+1, "Left out, support agents take no fleet tasks: %s", strings.Join(resolved.Skipped, ", "))
		}
		if p.orgID == "" {
			p.orgID = resolved.singleOrg()
		}
		p.walk(name, g, n, resolved.Devices, 0, depth+1, map[string]bool{n.ID: true})
	}
	if triggers == 0 && onlyTrigger == "" {
		p.problem(depth, "%s has no trigger to start from", name)
	}
}

// walk plans the steps after node for devices.
func (p *wfPlan) walk(name string, g *wfGraph, from *wfNode, devices []string, delay, depth int, path map[string]bool) {
	for _, id := range g.next[from.ID] {
		n := g.byID[id]
		if path[id] {
			p.say(depth, "%s: already on this path, not run twice", stepName(n))
			continue
		}
		next, nextDelay, stop := p.step(name, n, devices, delay, depth)
		if stop {
			continue
		}
		inner := make(map[string]bool, len(path)+1)
		for k := range path {
			inner[k] = true
		}
		inner[id] = true
		p.walk(name, g, n, next, nextDelay, depth+1, inner)
	}
}

var fleetServiceName = regexp.MustCompile(`^[A-Za-z0-9_.@-]{1,128}$`)

// osFamily is windows, linux or macos from what the device said it runs.
func osFamily(p *db.Peer) string {
	text := strings.ToLower(p.OS)
	switch {
	case strings.Contains(text, "windows"):
		return "windows"
	case strings.Contains(text, "mac") || strings.Contains(text, "darwin"):
		return "macos"
	case strings.Contains(text, "linux") || strings.Contains(text, "ubuntu") || strings.Contains(text, "debian") ||
		strings.Contains(text, "fedora") || strings.Contains(text, "centos") || strings.Contains(text, "suse"):
		return "linux"
	}
	return ""
}

func compare(value float64, op string, limit float64) bool {
	switch op {
	case ">":
		return value > limit
	case "<":
		return value < limit
	case ">=":
		return value >= limit
	case "<=":
		return value <= limit
	case "==":
		return value == limit
	}
	return false
}

func clockMinutes(text string) (int, bool) {
	h, m, ok := strings.Cut(text, ":")
	hour, err1 := strconv.Atoi(h)
	minute, err2 := strconv.Atoi(m)
	if !ok || err1 != nil || err2 != nil || hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return 0, false
	}
	return hour*60 + minute, true
}

// step plans one step. It returns the devices and delay the steps after
// it get, and stop when nothing after it runs.
func (p *wfPlan) step(name string, n *wfNode, devices []string, delay, depth int) ([]string, int, bool) {
	label := stepName(n)
	if len(devices) == 0 {
		p.say(depth, "%s: no devices left, skipped", label)
		return nil, delay, true
	}
	peers, _ := p.s.db.GetPeersByIDs(devices)
	switch n.Type {
	case "cond_os":
		want := strings.ToLower(n.Config["os"])
		if want == "" || want == "any" {
			p.say(depth, "%s: any system, %s", label, countDevices(devices))
			return devices, delay, false
		}
		var kept []string
		for _, id := range devices {
			if peer := peers[id]; peer != nil && osFamily(peer) == want {
				kept = append(kept, id)
			}
		}
		if len(kept) == 0 {
			p.say(depth, "%s: no device runs %s", label, want)
			return nil, delay, true
		}
		p.say(depth, "%s: %s on %s", label, countDevices(kept), want)
		return kept, delay, false

	case "cond_threshold":
		metric, op := n.Config["metric"], n.Config["operator"]
		limit, err := strconv.ParseFloat(n.Config["value"], 64)
		if op == "" {
			op = ">"
		}
		if err != nil {
			p.problem(depth, "%s: the value %q is no number", label, n.Config["value"])
			return nil, delay, true
		}
		if metric == "network" {
			p.say(depth, "%s: network use is not checked; all devices pass", label)
			return devices, delay, false
		}
		var kept []string
		for _, id := range devices {
			m, err := p.s.db.GetLatestPeerMetric(id)
			if err != nil || m == nil {
				continue
			}
			var v *float64
			switch metric {
			case "cpu":
				v = m.CPU
			case "ram":
				v = m.Memory
			case "disk":
				v = m.Disk
			}
			if v != nil && compare(*v, op, limit) {
				kept = append(kept, id)
			}
		}
		if len(kept) == 0 {
			p.say(depth, "%s: no device has %s %s %s right now", label, metric, op, n.Config["value"])
			return nil, delay, true
		}
		p.say(depth, "%s: %s with %s %s %s", label, countDevices(kept), metric, op, n.Config["value"])
		return kept, delay, false

	case "cond_time_window":
		start, ok1 := clockMinutes(n.Config["start_time"])
		end, ok2 := clockMinutes(n.Config["end_time"])
		if !ok1 || !ok2 {
			p.problem(depth, "%s: From and Until are times like 08:00", label)
			return nil, delay, true
		}
		local := p.now.Add(time.Duration(delay) * time.Second).In(time.Local)
		minute := local.Hour()*60 + local.Minute()
		inside := minute >= start && minute < end
		if start > end {
			inside = minute >= start || minute < end
		}
		if days := n.Config["days"]; days != "" {
			dayOK := false
			for _, d := range strings.Split(days, ",") {
				if v, err := strconv.Atoi(strings.TrimSpace(d)); err == nil && v == int(local.Weekday()) {
					dayOK = true
				}
			}
			inside = inside && dayOK
		}
		if !inside {
			p.say(depth, "%s: outside the time window now (server time %s), the steps after it do not run", label, local.Format("Mon 15:04"))
			return nil, delay, true
		}
		p.say(depth, "%s: inside the time window", label)
		return devices, delay, false

	case "transform_delay":
		seconds, err := strconv.Atoi(n.Config["seconds"])
		if n.Config["seconds"] == "" {
			seconds, err = 10, nil
		}
		if err != nil || seconds < 0 || seconds > 86400 {
			p.problem(depth, "%s: wait 0 to 86400 seconds", label)
			return nil, delay, true
		}
		p.say(depth, "%s: the steps after it start %d seconds later", label, seconds)
		return devices, delay + seconds, false

	case "transform_parse", "transform_filter":
		p.say(depth, "%s: not run; it works on outputs, which come back later. The devices go on unchanged", label)
		return devices, delay, false

	case "result_log":
		p.say(depth, "%s: the output of each device is kept with its task", label)
		return devices, delay, false

	case "result_alert":
		p.say(depth, "%s: not run; alerts are not sent yet", label)
		return devices, delay, false

	case "action_message":
		p.say(depth, "%s: not run; messages to the device's user are not sent yet", label)
		return devices, delay, false

	case "result_chain":
		id, err := strconv.ParseInt(n.Config["workflow_id"], 10, 64)
		if err != nil || id <= 0 {
			p.problem(depth, "%s: set the ID of the workflow to start", label)
			return nil, delay, true
		}
		if p.seen[id] || len(p.seen) >= fleetMaxChain+1 {
			p.problem(depth, "%s: workflow %d is already running in this chain, or the chain is too long", label, id)
			return nil, delay, true
		}
		other, err := p.s.db.Fleet().GetFleetWorkflow(id)
		if err != nil || (p.r != nil && !p.s.fleetWorkflowVisible(p.r, other)) {
			p.problem(depth, "%s: there is no workflow %d", label, id)
			return nil, delay, true
		}
		g, err := parseWorkflow(json.RawMessage(other.Nodes), json.RawMessage(other.Connections))
		if err != nil {
			p.problem(depth, "%s: workflow %d cannot be read (%v)", label, id, err)
			return nil, delay, true
		}
		p.seen[id] = true
		p.say(depth, "%s: starts workflow %d %q with its own triggers", label, id, other.Name)
		p.planGraph(other.Name, g, "", depth+1)
		return devices, delay, false
	}

	// Actions
	switch n.Type {
	case "action_script":
		if n.Config["script"] == "" {
			p.problem(depth, "%s: enter the script", label)
			return nil, delay, true
		}
		p.addAction(name, n, "", "script", n.Config["script"], devices, delay, depth)
	case "action_install":
		pkg := n.Config["package"]
		if pkg == "" {
			p.problem(depth, "%s: enter the package or the installer's address", label)
			return nil, delay, true
		}
		if strings.Contains(pkg, "://") && !strings.HasPrefix(strings.ToLower(pkg), "https://") {
			p.problem(depth, "%s: an installer is downloaded over https only", label)
			return nil, delay, true
		}
		p.addAction(name, n, "", "install", pkg, devices, delay, depth)
	case "action_file_deploy":
		src, dst := n.Config["source"], n.Config["destination"]
		if !strings.HasPrefix(strings.ToLower(src), "https://") {
			p.problem(depth, "%s: the source is an https address", label)
			return nil, delay, true
		}
		if !fleetAbsolutePath(dst) {
			p.problem(depth, "%s: the destination is an absolute path", label)
			return nil, delay, true
		}
		spec, _ := json.Marshal(map[string]string{"url": src, "path": dst})
		p.addAction(name, n, "", "file_deploy", string(spec), devices, delay, depth)
	case "action_restart":
		service := n.Config["service"]
		if !fleetServiceName.MatchString(service) {
			p.problem(depth, "%s: enter the service's name (letters, digits, . _ @ -)", label)
			return nil, delay, true
		}
		byFamily := map[string][]string{}
		var unknown []string
		for _, id := range devices {
			family := ""
			if peer := peers[id]; peer != nil {
				family = osFamily(peer)
			}
			if family == "" {
				unknown = append(unknown, id)
				continue
			}
			byFamily[family] = append(byFamily[family], id)
		}
		scripts := map[string]string{
			"windows": "Restart-Service -Name '" + service + "' -Force",
			"linux":   "systemctl restart '" + service + "'",
			"macos":   "launchctl kickstart -k 'system/" + service + "'",
		}
		for _, family := range []string{"windows", "linux", "macos"} {
			if ids := byFamily[family]; len(ids) > 0 {
				p.addAction(name, n, family, "script", scripts[family], ids, delay, depth)
			}
		}
		if len(unknown) > 0 {
			p.say(depth+1, "Left out, their system is not known: %s", strings.Join(unknown, ", "))
		}
	}
	return devices, delay, false
}

// addAction notes a task for the step, or adds devices to the one a
// second path to the same step made.
func (p *wfPlan) addAction(name string, n *wfNode, variant, taskType, script string, devices []string, delay, depth int) {
	key := n.ID + "/" + variant + "/" + strconv.Itoa(delay)
	a := p.byKey[key]
	if a == nil {
		if len(p.actions) >= fleetMaxActions {
			p.problem(depth, "a run makes at most %d tasks", fleetMaxActions)
			return
		}
		taskName := name + ": " + stepName(n)
		if variant != "" {
			taskName += " (" + variant + ")"
		}
		a = &wfAction{key: key, name: taskName, taskType: taskType, script: script, delay: delay, have: map[string]bool{}}
		p.byKey[key] = a
		p.actions = append(p.actions, a)
	}
	for _, d := range devices {
		if !a.have[d] {
			a.have[d] = true
			a.devices = append(a.devices, d)
		}
	}
	when := "right away"
	if delay > 0 {
		when = fmt.Sprintf("after %d seconds", delay)
	}
	p.say(depth, "%s: a %s task on %s, %s", stepName(n), taskType, countDevices(devices), when)
}

// runWorkflow plans the workflow and, unless dry, makes and starts its
// tasks. It returns the output text, the task ids and the problems.
func (s *Server) runWorkflow(r *http.Request, actor string, wf *db.FleetWorkflow, g *wfGraph, onlyTrigger string, dry bool) (string, []int64, []string) {
	now := time.Now()
	p := &wfPlan{s: s, r: r, now: now, byKey: map[string]*wfAction{}, seen: map[int64]bool{}}
	if wf.ID > 0 {
		p.seen[wf.ID] = true
	}
	if r != nil {
		p.orgID = fleetOrgOf(r)
	}
	if p.orgID == "" {
		p.orgID = wf.OrgID
	}
	name := wf.Name
	if name == "" {
		name = "Workflow"
	}
	p.planGraph(name, g, onlyTrigger, 0)
	if len(p.actions) == 0 && len(p.problems) == 0 {
		p.say(0, "Nothing to run: no step after the trigger makes a task.")
	}
	var ids []int64
	if dry || len(p.problems) > 0 {
		if dry {
			p.say(0, "Dry run: %d tasks would start. Nothing was sent to the devices.", len(p.actions))
		} else {
			p.say(0, "Nothing started because of the problems above.")
		}
		return strings.Join(p.lines, "\n"), ids, p.problems
	}
	fleet := s.db.Fleet()
	for _, a := range p.actions {
		task := &db.FleetTask{
			OrgID: p.orgID, Name: a.name, Type: a.taskType, Targets: strings.Join(a.devices, ", "),
			ScheduleType: "immediate", Script: a.script, Timeout: 300, CreatedBy: actor, WorkflowID: wf.ID,
		}
		if a.delay > 0 {
			at := now.Add(time.Duration(a.delay) * time.Second).UTC().Format(time.RFC3339)
			task.ScheduleType, task.ScheduleValue, task.NextRun = "once", at, at
		}
		if err := fleet.CreateFleetTask(task); err != nil {
			p.say(0, "Could not make the task %q: %v", a.name, err)
			continue
		}
		ids = append(ids, task.ID)
		if a.delay > 0 {
			p.say(0, "Task #%d %q starts at %s.", task.ID, a.name, now.Add(time.Duration(a.delay)*time.Second).Format("15:04:05"))
			continue
		}
		queued, _, err := s.runFleetTask(actor, task, "")
		if err != nil {
			p.say(0, "Task #%d %q could not start: %v", task.ID, a.name, err)
			continue
		}
		p.say(0, "Task #%d %q queued on %d devices; each runs it with its next check-in.", task.ID, a.name, queued)
	}
	return strings.Join(p.lines, "\n"), ids, nil
}

// ── Routes ─────────────────────────────────────────────────────────────

func (s *Server) fleetWorkflowVisible(r *http.Request, wf *db.FleetWorkflow) bool {
	if wf == nil {
		return false
	}
	if own := fleetOrgOf(r); own != "" {
		return wf.OrgID == own
	}
	return wf.OrgID == "" || s.fleetOrgAllowed(r, wf.OrgID)
}

func (s *Server) fleetWorkflowFor(w http.ResponseWriter, r *http.Request) *db.FleetWorkflow {
	id, ok := fleetPathID(w, r, "workflowId", "Workflow")
	if !ok {
		return nil
	}
	wf, err := s.db.Fleet().GetFleetWorkflow(id)
	if errors.Is(err, db.ErrFleetNotFound) || (err == nil && !s.fleetWorkflowVisible(r, wf)) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Workflow not found"})
		return nil
	}
	if err != nil {
		writeInternalError(w, err, "load fleet workflow")
		return nil
	}
	return wf
}

// fleetWorkflowJSON is a saved workflow with its steps as JSON.
func fleetWorkflowJSON(wf *db.FleetWorkflow) map[string]any {
	return map[string]any{
		"id": wf.ID, "org_id": wf.OrgID, "name": wf.Name, "created_by": wf.CreatedBy,
		"created_at": wf.CreatedAt, "updated_at": wf.UpdatedAt,
		"nodes": json.RawMessage(wf.Nodes), "connections": json.RawMessage(wf.Connections),
	}
}

func (s *Server) handleListFleetWorkflows(w http.ResponseWriter, r *http.Request) {
	list, err := s.db.Fleet().ListFleetWorkflows(fleetOrgOf(r))
	if err != nil {
		writeInternalError(w, err, "list fleet workflows")
		return
	}
	out := make([]map[string]any, 0, len(list))
	for _, wf := range list {
		if !s.fleetWorkflowVisible(r, wf) {
			continue
		}
		var nodes []json.RawMessage
		_ = json.Unmarshal([]byte(wf.Nodes), &nodes)
		out = append(out, map[string]any{"id": wf.ID, "name": wf.Name, "created_by": wf.CreatedBy,
			"created_at": wf.CreatedAt, "updated_at": wf.UpdatedAt, "steps": len(nodes)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "workflows": out})
}

// workflowBody reads name, nodes and connections.
type workflowBody struct {
	Name        *string         `json:"name"`
	Nodes       json.RawMessage `json:"nodes"`
	Connections json.RawMessage `json:"connections"`
	CreatedBy   string          `json:"created_by"`
	DryRun      bool            `json:"dry_run"`
}

func readWorkflowBody(w http.ResponseWriter, r *http.Request) (*workflowBody, bool) {
	var body workflowBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024*1024)).Decode(&body); err != nil {
		badFleetRequest(w, "The request needs a JSON object")
		return nil, false
	}
	return &body, true
}

func (s *Server) saveWorkflowFrom(w http.ResponseWriter, body *workflowBody, wf *db.FleetWorkflow) bool {
	if body.Name != nil || wf.ID == 0 {
		name := ""
		if body.Name != nil {
			name = strings.TrimSpace(*body.Name)
		}
		if name == "" {
			badFleetRequest(w, "Enter a name")
			return false
		}
		if len(name) > 120 {
			badFleetRequest(w, "The name is longer than 120 characters")
			return false
		}
		wf.Name = name
	}
	if body.Nodes != nil || body.Connections != nil || wf.ID == 0 {
		nodes, conns := body.Nodes, body.Connections
		if nodes == nil {
			nodes = json.RawMessage(wf.Nodes)
		}
		if conns == nil {
			conns = json.RawMessage(wf.Connections)
		}
		g, err := parseWorkflow(nodes, conns)
		if err != nil {
			badFleetRequest(w, "The workflow cannot be saved: "+err.Error())
			return false
		}
		wf.Nodes, wf.Connections = string(g.rawNodes), string(g.rawConns)
	}
	return true
}

func (s *Server) handleCreateFleetWorkflow(w http.ResponseWriter, r *http.Request) {
	body, ok := readWorkflowBody(w, r)
	if !ok {
		return
	}
	wf := &db.FleetWorkflow{OrgID: fleetOrgOf(r)}
	if !s.saveWorkflowFrom(w, body, wf) {
		return
	}
	wf.CreatedBy = fleetCreator(r, fleetBody{"created_by": body.CreatedBy})
	if err := s.db.Fleet().CreateFleetWorkflow(wf); err != nil {
		writeInternalError(w, err, "create fleet workflow")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"success": true, "id": wf.ID, "data": fleetWorkflowJSON(wf)})
}

func (s *Server) handleGetFleetWorkflow(w http.ResponseWriter, r *http.Request) {
	wf := s.fleetWorkflowFor(w, r)
	if wf == nil {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "data": fleetWorkflowJSON(wf)})
}

func (s *Server) handleUpdateFleetWorkflow(w http.ResponseWriter, r *http.Request) {
	wf := s.fleetWorkflowFor(w, r)
	if wf == nil {
		return
	}
	body, ok := readWorkflowBody(w, r)
	if !ok {
		return
	}
	if !s.saveWorkflowFrom(w, body, wf) {
		return
	}
	if err := s.db.Fleet().UpdateFleetWorkflow(wf); err != nil {
		writeInternalError(w, err, "update fleet workflow")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "id": wf.ID, "data": fleetWorkflowJSON(wf)})
}

func (s *Server) handleDeleteFleetWorkflow(w http.ResponseWriter, r *http.Request) {
	wf := s.fleetWorkflowFor(w, r)
	if wf == nil {
		return
	}
	if err := s.db.Fleet().DeleteFleetWorkflow(wf.ID); err != nil {
		writeInternalError(w, err, "delete fleet workflow")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

// handleExecuteFleetWorkflow runs a workflow, or with dry_run says what it
// would run. The steps of the body count when it has them (the builder
// sends what is on the canvas, saved or not); "draft" is a workflow that
// was never saved.
func (s *Server) handleExecuteFleetWorkflow(w http.ResponseWriter, r *http.Request) {
	var wf *db.FleetWorkflow
	if r.PathValue("workflowId") == "draft" {
		wf = &db.FleetWorkflow{OrgID: fleetOrgOf(r), Nodes: "[]", Connections: "[]"}
	} else if wf = s.fleetWorkflowFor(w, r); wf == nil {
		return
	}
	body, ok := readWorkflowBody(w, r)
	if !ok {
		return
	}
	nodes, conns := json.RawMessage(wf.Nodes), json.RawMessage(wf.Connections)
	if body.Nodes != nil {
		nodes, conns = body.Nodes, body.Connections
	}
	if body.Name != nil && strings.TrimSpace(*body.Name) != "" && wf.ID == 0 {
		wf.Name = strings.TrimSpace(*body.Name)
	}
	g, err := parseWorkflow(nodes, conns)
	if err != nil {
		badFleetRequest(w, "The workflow cannot be run: "+err.Error())
		return
	}
	actor := fleetCreator(r, fleetBody{"created_by": body.CreatedBy})
	output, ids, problems := s.runWorkflow(r, actor, wf, g, "", body.DryRun)
	if ids == nil {
		ids = []int64{}
	}
	status := "ok"
	if len(problems) > 0 {
		status = "problems"
	}
	if wf.ID > 0 {
		taskIDs, _ := json.Marshal(ids)
		_ = s.db.Fleet().AddFleetWorkflowRun(&db.FleetWorkflowRun{WorkflowID: wf.ID, DryRun: body.DryRun, Status: status,
			Output: output, TaskIDs: string(taskIDs), StartedBy: actor})
	}
	if len(problems) > 0 && !body.DryRun {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": strings.Join(problems, "; "), "output": output, "problems": problems})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "dry_run": body.DryRun, "output": output,
		"tasks": ids, "problems": nonNil(problems)})
}

func (s *Server) handleFleetWorkflowHistory(w http.ResponseWriter, r *http.Request) {
	wf := s.fleetWorkflowFor(w, r)
	if wf == nil {
		return
	}
	runs, err := s.db.Fleet().ListFleetWorkflowRuns(wf.ID, 50)
	if err != nil {
		writeInternalError(w, err, "fleet workflow history")
		return
	}
	out := make([]map[string]any, len(runs))
	for i, run := range runs {
		out[i] = map[string]any{"id": run.ID, "workflow_id": run.WorkflowID, "dry_run": run.DryRun, "status": run.Status,
			"output": run.Output, "tasks": json.RawMessage(run.TaskIDs), "started_by": run.StartedBy, "created_at": run.CreatedAt}
	}
	writeJSON(w, http.StatusOK, map[string]any{"success": true, "history": out})
}

// runScheduledWorkflows starts the branches of the schedule triggers whose
// cron expression matches this minute (server time), once per minute.
func (s *Server) runScheduledWorkflows(now time.Time) {
	list, err := s.db.Fleet().ListFleetWorkflows("")
	if err != nil {
		return
	}
	local := now.In(time.Local)
	minute := local.Format("2006-01-02T15:04")
	for _, wf := range list {
		if !strings.Contains(wf.Nodes, "trigger_schedule") {
			continue
		}
		g, err := parseWorkflow(json.RawMessage(wf.Nodes), json.RawMessage(wf.Connections))
		if err != nil {
			continue
		}
		for _, n := range g.nodes {
			if n.Type != "trigger_schedule" {
				continue
			}
			sched, err := cron.Parse(n.Config["cron"])
			if err != nil || !sched.Matches(local) {
				continue
			}
			key := strconv.FormatInt(wf.ID, 10) + "/" + n.ID
			s.fleet.syncMu.Lock()
			done := s.fleet.fired[key] == minute
			s.fleet.fired[key] = minute
			s.fleet.syncMu.Unlock()
			if done {
				continue
			}
			actor := "schedule:" + wf.CreatedBy
			output, ids, problems := s.runWorkflow(nil, actor, wf, g, n.ID, false)
			status := "ok"
			if len(problems) > 0 {
				status = "problems"
				log.Printf("[fleet] scheduled workflow %d: %s", wf.ID, strings.Join(problems, "; "))
			}
			taskIDs, _ := json.Marshal(ids)
			_ = s.db.Fleet().AddFleetWorkflowRun(&db.FleetWorkflowRun{WorkflowID: wf.ID, Status: status, Output: output,
				TaskIDs: string(taskIDs), StartedBy: actor})
		}
	}
}
