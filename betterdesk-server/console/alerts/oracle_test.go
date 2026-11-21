package alerts

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/mail"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// The recordings in testdata were made by the scripts in
// testdata/nodeoracle from the Node console's alertRulesEngine.js.

func readGzip(t testing.TB, name string, into any) {
	t.Helper()
	file, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	unzipped, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewDecoder(unzipped).Decode(into); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

// bodyOf is req.body for a JSON request with the given text.
func bodyOf(raw string) middleware.JSONObject {
	var body middleware.JSONObject
	steps := middleware.Bodies(middleware.BodyOptions{})(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		body = middleware.Body(r)
	}))
	req := httptest.NewRequest("POST", "/", strings.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	steps.ServeHTTP(httptest.NewRecorder(), req)
	return body
}

var (
	bindCache   = map[string]jsbind.Value{}
	bindCacheMu sync.Mutex
)

// bindValue is an input value of a recording: JSON text, or one of the
// words JSON lacks. It is the value a route takes from a request body.
func bindValue(t testing.TB, text string) jsbind.Value {
	t.Helper()
	switch text {
	case "", "undefined":
		return jsbind.Undefined
	case "NaN":
		return jsbind.Number(math.NaN())
	}
	bindCacheMu.Lock()
	defer bindCacheMu.Unlock()
	if v, ok := bindCache[text]; ok {
		return v
	}
	body := bodyOf(`{"v":` + text + `}`)
	if !body.Has("v") {
		t.Fatalf("input %q is no JSON", text)
	}
	v := jsbind.Of(body.Get("v"))
	bindCache[text] = v
	return v
}

// dumpBind writes a value as dump() of the oracle's lib.js does.
func dumpBind(v jsbind.Value) string {
	switch {
	case v.IsUndefined():
		return "undefined"
	case v.IsNumber():
		n := v.ToNumber()
		switch {
		case math.IsNaN(n):
			return "NaN"
		case n == 0 && math.Signbit(n):
			return "-0"
		}
		return jsbind.NumberString(n)
	case v.IsArray():
		parts := make([]string, len(v.Items()))
		for i, item := range v.Items() {
			parts[i] = dumpBind(item)
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	return v.JSON()
}

// ---- Date.parse ----

func TestDateParseAsNode(t *testing.T) {
	total := 0
	for file, zone := range map[string]string{"dates_utc.json.gz": "UTC", "dates_berlin.json.gz": "Europe/Berlin", "dates_stjohns.json.gz": "America/St_Johns"} {
		var recording struct {
			TZ      string `json:"tz"`
			Vectors [][2]json.RawMessage
		}
		readGzip(t, file, &recording)
		if recording.TZ != zone {
			t.Fatalf("%s was recorded in %q", file, recording.TZ)
		}
		loc, err := time.LoadLocation(zone)
		if err != nil {
			t.Fatal(err)
		}
		dates := 0
		for _, vector := range recording.Vectors {
			var text string
			if err := json.Unmarshal(vector[0], &text); err != nil {
				t.Fatal(err)
			}
			got, ok := dateParse(text, loc)
			if string(vector[1]) == "null" {
				if ok {
					t.Errorf("%s: Date.parse(%q) = %v, Node: NaN", zone, text, got)
				}
				continue
			}
			dates++
			want, err := strconv.ParseFloat(string(vector[1]), 64)
			if err != nil {
				t.Fatal(err)
			}
			if !ok || got != want {
				t.Errorf("%s: Date.parse(%q) = %v %v, Node: %v", zone, text, got, ok, want)
			}
		}
		if len(recording.Vectors) < 38000 || dates < 17000 {
			t.Fatalf("%s: %d texts, %d dates", file, len(recording.Vectors), dates)
		}
		total += len(recording.Vectors)
	}
	t.Logf("%d texts", total)
}

// ---- evalCondition ----

func TestEvalConditionAsNode(t *testing.T) {
	var vectors []struct {
		V      string  `json:"v"`
		Op     string  `json:"op"`
		T      string  `json:"t"`
		Out    bool    `json:"out"`
		Throws *string `json:"throws"`
	}
	readGzip(t, "cond.json.gz", &vectors)
	if len(vectors) < 60000 {
		t.Fatalf("%d vectors", len(vectors))
	}
	held, failed, members := 0, 0, 0
	for _, v := range vectors {
		got, err := EvalCondition(bindValue(t, v.V), v.Op, bindValue(t, v.T))
		switch {
		case slices.Contains(objectMembers, v.Op):
			// BUGS AE2: a name every object has is no operator; Node held,
			// refused or threw for it
			members++
			if got || err != nil {
				t.Errorf("evalCondition(%s, %q, %s) = %v %v, want false", v.V, v.Op, v.T, got, err)
			}
			continue
		case v.Throws != nil:
			failed++
			if err == nil || err.Error() != *v.Throws {
				t.Errorf("evalCondition(%s, %q, %s): %v %v, Node threw %q", v.V, v.Op, v.T, got, err, *v.Throws)
			}
		case err != nil || got != v.Out:
			t.Errorf("evalCondition(%s, %q, %s) = %v %v, Node: %v", v.V, v.Op, v.T, got, err, v.Out)
		}
		if got {
			held++
		}
	}
	if held < 5000 || failed < 1000 || members < 1000 {
		t.Fatalf("%d conditions held, %d failed, %d named an object's member: the recording is one-sided", held, failed, members)
	}
}

// ---- the engine with its database calls replaced ----

type oracleMail struct {
	Alert     map[string]string `json:"alert"`
	Triggered string            `json:"triggered"`
	Rule      map[string]string `json:"rule"`
}

type memCase struct {
	Fn           string              `json:"fn"`
	Rules        []map[string]string `json:"rules"`
	Device       string              `json:"device"`
	Type         string              `json:"type"`
	Metric       string              `json:"metric"`
	Min          string              `json:"min"`
	Telemetry    map[string]string   `json:"telemetry"`
	Devices      string              `json:"devices"`
	DevicesThrow *string             `json:"devicesThrow"`
	Now          int64               `json:"now"`
	Recent       string              `json:"recent"`
	Create       string              `json:"create"`
	Mail         string              `json:"mail"`
	LoadThrows   bool                `json:"loadThrows"`
	OddID        bool                `json:"oddId"`

	Throws      *string     `json:"throws"`
	Filters     []string    `json:"filters"`
	RecentCalls [][3]string `json:"recentCalls"`
	Created     []struct {
		Alert string `json:"alert"`
		ISO   bool   `json:"iso"`
	} `json:"created"`
	Mails     []oracleMail `json:"mails"`
	ListCalls int          `json:"listCalls"`
	Logs      [][2]string  `json:"logs"`
}

// memStoreTime is the time memStore stamps an alert with.
const memStoreTime = "2026-11-10T08:30:15.123Z"

// memStore stands in for the database as the oracle's replaced adapter
// methods did.
type memStore struct {
	t       testing.TB
	c       *memCase
	filters []string
	recent  [][3]string
	created []string
	times   []string
}

func (s *memStore) enabledRules(_ context.Context, conditionType string) ([]Rule, error) {
	s.filters = append(s.filters, `{"enabled":true,"condition_type":`+strconv.Quote(conditionType)+`}`)
	if s.c.LoadThrows {
		return nil, errors.New("load failed")
	}
	rules := make([]Rule, len(s.c.Rules))
	for i, fields := range s.c.Rules {
		rules[i] = Rule{
			ID:             bindValue(s.t, fields["id"]),
			Name:           bindValue(s.t, fields["name"]),
			Description:    bindValue(s.t, fields["description"]),
			ConditionType:  bindValue(s.t, fields["condition_type"]),
			ConditionOp:    bindValue(s.t, fields["condition_op"]),
			ConditionValue: bindValue(s.t, fields["condition_value"]),
			Severity:       bindValue(s.t, fields["severity"]),
			ScopeDeviceID:  bindValue(s.t, fields["scope_device_id"]),
			CooldownSecs:   bindValue(s.t, fields["cooldown_secs"]),
			NotifyEmails:   bindValue(s.t, fields["notify_emails"]),
		}
	}
	return rules, nil
}

func (s *memStore) cooldownStep(_ context.Context, _, _ jsbind.Value, fn func(alertStore) error) error {
	return fn(s)
}

func (s *memStore) recentAlert(_ context.Context, ruleID, deviceID, cooldown jsbind.Value) (bool, error) {
	s.recent = append(s.recent, [3]string{dumpBind(ruleID), dumpBind(deviceID), dumpBind(cooldown)})
	if s.c.Recent == "throws" {
		return false, errors.New("recent failed")
	}
	return s.c.Recent == "all", nil
}

func (s *memStore) createAlert(_ context.Context, alert NewAlert) (string, error) {
	s.created = append(s.created, `{"rule_id":`+dumpBind(alert.RuleID)+`,"device_id":`+dumpBind(alert.DeviceID)+`,"severity":`+dumpBind(alert.Severity)+
		`,"message":`+dumpBind(alert.Message)+`,"acknowledged":false}`)
	s.times = append(s.times, dumpBind(alert.TriggeredAt))
	if s.c.Create == "throws" {
		return "", errors.New("create failed")
	}
	return memStoreTime, nil
}

type sentMail struct {
	alert mail.Alert
	rule  mail.AlertRule
}

type fakeMailer struct {
	mu   sync.Mutex
	mode string
	sent []sentMail
}

func (m *fakeMailer) SendAlertEmail(_ context.Context, alert mail.Alert, rule mail.AlertRule) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, sentMail{alert, rule})
	switch m.mode {
	case "rejects":
		return false, errors.New("mail rejected")
	case "throws":
		return false, errors.New("mail threw")
	}
	return true, nil
}

// keys are the mails as comparable text, sorted: the engine does not wait
// for a mail, so their order is not fixed.
func (m *fakeMailer) keys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, len(m.sent))
	for i, s := range m.sent {
		out[i] = strings.Join([]string{dumpBind(s.alert.DeviceID), dumpBind(s.alert.Severity), dumpBind(s.alert.Message),
			dumpBind(s.rule.Name), dumpBind(s.rule.Description), dumpBind(s.rule.ConditionType), dumpBind(s.rule.ConditionOp),
			dumpBind(s.rule.ConditionValue), dumpBind(s.rule.NotifyEmails)}, " | ")
	}
	sort.Strings(out)
	return out
}

func mailKeys(mails []oracleMail) []string {
	out := make([]string, len(mails))
	for i, m := range mails {
		out[i] = strings.Join([]string{m.Alert["device_id"], m.Alert["severity"], m.Alert["message"], m.Rule["name"], m.Rule["description"],
			m.Rule["condition_type"], m.Rule["condition_op"], m.Rule["condition_value"], m.Rule["notify_emails"]}, " | ")
	}
	sort.Strings(out)
	return out
}

type logLines struct {
	mu    sync.Mutex
	lines [][2]string
}

func (l *logLines) add(level string, args []any) {
	parts := make([]string, len(args))
	for i, arg := range args {
		parts[i], _ = arg.(string)
	}
	l.mu.Lock()
	l.lines = append(l.lines, [2]string{level, "[AlertEngine] " + strings.Join(parts, " ")})
	l.mu.Unlock()
}
func (l *logLines) Info(args ...any)  { l.add("info", args) }
func (l *logLines) Error(args ...any) { l.add("error", args) }

// split separates the lines about mails, which come in no fixed order,
// from the rest.
func splitLogs(lines [][2]string) (ordered [][2]string, mails []string) {
	for _, line := range lines {
		if strings.HasPrefix(line[1], "[AlertEngine] Email failed:") {
			mails = append(mails, line[1])
			continue
		}
		ordered = append(ordered, line)
	}
	sort.Strings(mails)
	return ordered, mails
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sameLines(a, b [][2]string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// deviceObjects reads a device list as the Go API client hands it over.
// What is no object in the list is nil, which the sweep skips as it
// skipped everything without an id.
func deviceObjects(t testing.TB, text string) []*goapi.Object {
	t.Helper()
	parsed, err := goapi.Parse([]byte(text))
	if err != nil {
		t.Fatalf("device list %s: %v", text, err)
	}
	list, _ := parsed.([]any)
	out := make([]*goapi.Object, len(list))
	for i, item := range list {
		out[i], _ = item.(*goapi.Object)
	}
	return out
}

func show(p *string) string {
	if p == nil {
		return "nothing"
	}
	return strconv.Quote(*p)
}

const isoForm = `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`

func TestEngineAsNodeWithoutDatabase(t *testing.T) {
	var recording struct {
		Cases      []memCase `json:"cases"`
		Background []struct {
			Name     string      `json:"name"`
			Returned string      `json:"returned"`
			Logs     [][2]string `json:"logs"`
		} `json:"background"`
	}
	readGzip(t, "mem.json.gz", &recording)
	if len(recording.Cases) < 10000 {
		t.Fatalf("%d cases", len(recording.Cases))
	}
	counts := map[string]int{}
	clock := time.Date(2026, 11, 10, 8, 30, 15, 123456789, time.UTC)
	for i := range recording.Cases {
		c := &recording.Cases[i]
		mem := &memStore{t: t, c: c}
		mailer := &fakeMailer{mode: c.Mail}
		logs := &logLines{}
		listCalls := 0
		engine := New(Options{
			Mail: mailer, Logger: logs, Now: func() time.Time { return clock }, Location: time.UTC,
			Devices: func(context.Context) ([]*goapi.Object, error) {
				listCalls++
				if c.DevicesThrow != nil {
					return nil, errors.New(*c.DevicesThrow)
				}
				return deviceObjects(t, c.Devices), nil
			},
		})
		engine.store = mem
		ctx := context.Background()
		var err error
		switch c.Fn {
		case "rules":
			opts := EvalOptions{}
			if c.Min != "" && c.Min != "undefined" {
				opts.MinCooldownSecs = bindValue(t, c.Min).ToNumber()
			}
			err = engine.EvaluateRules(ctx, bindValue(t, c.Device), c.Type, bindValue(t, c.Metric), opts)
		case "telemetry":
			err = engine.EvaluateTelemetry(ctx, bindValue(t, c.Device), Telemetry{
				CPUUsagePercent:  bindValue(t, c.Telemetry["cpu_usage_percent"]),
				MemoryUsedBytes:  bindValue(t, c.Telemetry["memory_used_bytes"]),
				MemoryTotalBytes: bindValue(t, c.Telemetry["memory_total_bytes"]),
			})
		case "sweep":
			err = engine.PeriodicCheckAt(ctx, time.UnixMilli(c.Now))
		default:
			t.Fatalf("case %d: unknown function %q", i, c.Fn)
		}
		engine.Wait()
		counts[c.Fn]++

		describe := func() string {
			in, _ := json.Marshal(struct {
				Fn, Device, Type, Metric, Min, Devices string
				Rules                                  []map[string]string
				Telemetry                              map[string]string
			}{c.Fn, c.Device, c.Type, c.Metric, c.Min, c.Devices, c.Rules, c.Telemetry})
			return string(in)
		}
		if c.OddID {
			// A device id that is a list or an object: the Go API never
			// sends one, and the engine leaves such a device out.
			counts["odd ids"]++
			if err != nil || len(mem.recent) != 0 || len(mem.created) != 0 || len(logs.lines) != 1 ||
				logs.lines[0] != [2]string{"error", "[AlertEngine] Device list failed: a device id that is no text"} {
				t.Fatalf("case %d %s: %v, %d checks, logs %q", i, describe(), err, len(mem.recent), logs.lines)
			}
			continue
		}
		if memberRules := memberOperatorRules(c.Rules); len(memberRules) > 0 {
			// BUGS AE2: a rule whose operator is a name every object has
			// holds for nothing and fails nothing, so the rules after it
			// are checked too. Node held such a rule, or threw and stopped.
			counts["AE2: a rule with an object's member as its operator"]++
			if err != nil && strings.Contains(err.Error(), "undefined or null") {
				t.Fatalf("case %d %s: %v", i, describe(), err)
			}
			for _, created := range mem.created {
				for _, id := range memberRules {
					if strings.HasPrefix(created, `{"rule_id":`+id+`,`) {
						t.Fatalf("case %d %s: rule %s raised %s", i, describe(), id, created)
					}
				}
			}
			continue
		}
		if never, ambiguous := sweepNeverOnline(t, c.Fn, c.Devices); len(never) > 0 {
			// BUGS AE1: a device whose last_online lies before 1970 was
			// never online and is not checked; Node took it as offline
			// since then
			counts["AE1: a device never online in the sweep"]++
			// Where Node threw at such a device, or the list has its id
			// twice, the Go console raises no more than Node
			if ambiguous || c.Throws != nil {
				if len(mem.created) > len(c.Created) || err != nil && (c.Throws == nil || err.Error() != *c.Throws) {
					t.Fatalf("case %d %s: %v, alerts %q", i, describe(), err, mem.created)
				}
				continue
			}
			c.RecentCalls = withoutDevices(c.RecentCalls, never, func(call [3]string) string { return call[1] })
			c.Mails = withoutDevices(c.Mails, never, func(m oracleMail) string { return m.Alert["device_id"] })
			c.Logs = withoutDevices(c.Logs, never, func(line [2]string) string { return line[1] })
			created := c.Created[:0:0]
			for _, alert := range c.Created {
				if len(withoutDevices([]string{alert.Alert}, never, func(s string) string { return s })) == 1 {
					created = append(created, alert)
				}
			}
			c.Created = created
		}
		goChecked, nodeChecked := slices.Contains(mem.filters, memoryRules), slices.Contains(c.Filters, memoryRules)
		nodeReads, goReads := memoryReads(t, c.Telemetry)
		memoryDiffers := c.Fn == "telemetry" && (goChecked != nodeChecked || goReads != nodeReads)
		badLimit, badCooldown := oddRules(t, c.Rules)
		touched := slices.ContainsFunc(c.RecentCalls, func(call [3]string) bool { return badLimit[call[0]] || badCooldown[call[0]] })
		if touched && c.Throws == nil && c.Create != "throws" && c.Mail != "throws" {
			// BUGS AE3: Node checked a rule whose limit is no finite number,
			// or used a cooldown that is no whole number above 0
			counts["AE3: a rule whose limit or cooldown Node read as it was"]++
			if problem := expectAE3(t, c, mem.recent, badLimit, badCooldown); problem != "" {
				t.Fatalf("case %d %s: %s", i, describe(), problem)
			}
		}
		if memoryDiffers {
			// BUGS AE5: the Go console reads memory at 0 bytes used and
			// checks it, and does not check it for a total of 0 or below.
			// Memory comes last, so what came before it is the same; an
			// error may come from a value only the Go console read.
			counts["AE5: memory checked by one side only"]++
			problem := ""
			if !touched || c.Throws == nil && c.Create != "throws" && c.Mail != "throws" {
				problem = sameBeforeMemory(mem, c, goChecked, nodeChecked)
			}
			if problem != "" || err != nil && !(goReads && !nodeReads) && (c.Throws == nil || err.Error() != *c.Throws) {
				t.Fatalf("case %d %s: %s %v", i, describe(), problem, err)
			}
			continue
		}
		if !(touched && c.Throws == nil && c.Create != "throws" && c.Mail != "throws") && (touched || c.Throws != nil && len(badLimit) > 0) {
			// The same where Node threw or a failure ended its loop over
			// the rules early: the Go console raises nothing for the first
			// kind of rule and takes 300 seconds for the second, so it
			// raises no alert Node did not.
			counts["AE3: the same where Node threw"]++
			if err != nil && (c.Throws == nil || err.Error() != *c.Throws) {
				t.Fatalf("case %d %s: error %v, Node threw %s", i, describe(), err, show(c.Throws))
			}
			var want []string
			for _, alert := range c.Created {
				want = append(want, alert.Alert)
			}
			if !subsequence(mem.created, want) {
				t.Fatalf("case %d %s: alerts %q, Node: %v", i, describe(), mem.created, c.Created)
			}
			for _, call := range mem.recent {
				if badLimit[call[0]] {
					t.Fatalf("case %d %s: rule %s with a limit that is no finite number was checked", i, describe(), call[0])
				}
				if secs, err := strconv.ParseFloat(call[2], 64); badCooldown[call[0]] && (err != nil || secs < DefaultCooldownSecs) {
					t.Fatalf("case %d %s: rule %s checked with the cooldown %s", i, describe(), call[0], call[2])
				}
			}
			continue
		}
		if (err == nil) != (c.Throws == nil) || (err != nil && err.Error() != *c.Throws) {
			t.Fatalf("case %d %s: error %v, Node threw %s", i, describe(), err, show(c.Throws))
		}
		if c.Throws != nil {
			counts["thrown"]++
		}
		if !sameStrings(mem.filters, c.Filters) {
			t.Fatalf("case %d %s: rules asked for with %q, Node: %q", i, describe(), mem.filters, c.Filters)
		}
		if len(mem.recent) != len(c.RecentCalls) {
			t.Fatalf("case %d %s: %d cooldown checks %q, Node: %q", i, describe(), len(mem.recent), mem.recent, c.RecentCalls)
		}
		for k := range mem.recent {
			if mem.recent[k] != c.RecentCalls[k] {
				t.Fatalf("case %d %s: cooldown check %q, Node: %q", i, describe(), mem.recent[k], c.RecentCalls[k])
			}
		}
		if len(mem.created) != len(c.Created) {
			t.Fatalf("case %d %s: %d alerts %q, Node: %v", i, describe(), len(mem.created), mem.created, c.Created)
		}
		// Node stamped an alert with its own clock as ISO text; the Go
		// console leaves the time to the database (BUGS AE6)
		for k := range mem.created {
			if mem.created[k] != c.Created[k].Alert || !c.Created[k].ISO || mem.times[k] != "undefined" {
				t.Fatalf("case %d %s: alert %s at %s, Node: %s", i, describe(), mem.created[k], mem.times[k], c.Created[k].Alert)
			}
		}
		counts["alerts"] += len(mem.created)
		if got, want := mailer.keys(), mailKeys(c.Mails); !sameStrings(got, want) {
			t.Fatalf("case %d %s: mails %q, Node: %q", i, describe(), got, want)
		}
		for _, m := range c.Mails {
			if !regexp.MustCompile(isoForm).MatchString(strings.Trim(m.Triggered, `"`)) {
				t.Fatalf("case %d: Node's alert time %s", i, m.Triggered)
			}
		}
		for _, m := range mailer.sent {
			if m.alert.TriggeredAt.Str() != memStoreTime {
				t.Fatalf("case %d: alert time %q, the store's %q", i, m.alert.TriggeredAt.Str(), memStoreTime)
			}
		}
		counts["mails"] += len(c.Mails)
		if listCalls != c.ListCalls {
			t.Fatalf("case %d %s: device list read %d times, Node: %d", i, describe(), listCalls, c.ListCalls)
		}
		// A mail function that throws instead of failing its promise does
		// not exist: the mail service's is asynchronous. The fake that did
		// was logged by Node as a failed alert; here it is a failed mail.
		want := c.Logs
		if c.Mail == "throws" {
			want = make([][2]string, len(c.Logs))
			for k, line := range c.Logs {
				want[k] = [2]string{line[0], strings.Replace(line[1], "Failed to create alert: mail threw", "Email failed: mail threw", 1)}
			}
		}
		gotOrdered, gotMails := splitLogs(logs.lines)
		wantOrdered, wantMails := splitLogs(want)
		if !sameLines(gotOrdered, wantOrdered) || !sameStrings(gotMails, wantMails) {
			t.Fatalf("case %d %s: logs %q\nNode: %q", i, describe(), logs.lines, c.Logs)
		}
	}
	for _, name := range []string{"rules", "telemetry", "sweep", "odd ids", "thrown", "alerts", "mails", "AE2: a rule with an object's member as its operator", "AE1: a device never online in the sweep",
		"AE3: a rule whose limit or cooldown Node read as it was", "AE3: the same where Node threw", "AE5: memory checked by one side only"} {
		if counts[name] == 0 {
			t.Errorf("the recording has no %q", name)
		}
	}
	t.Logf("%d cases: %v", len(recording.Cases), counts)

	// evaluateInBackground: the failure is logged, nothing is thrown
	for _, bg := range recording.Background {
		logs := &logLines{}
		engine := New(Options{Logger: logs})
		engine.EvaluateInBackground(func(context.Context) error {
			switch bg.Name {
			case "rejects":
				return errors.New("evaluation rejected")
			case "throws":
				return errors.New("evaluation threw")
			}
			return nil
		})
		engine.Wait()
		if bg.Returned != "undefined" || !sameLines(logs.lines, bg.Logs) {
			t.Fatalf("background %s: logs %q, Node returned %s and logged %q", bg.Name, logs.lines, bg.Returned, bg.Logs)
		}
	}
	if len(recording.Background) != 3 {
		t.Fatalf("%d background runs", len(recording.Background))
	}
}

// ---- the engine on real rows ----

type dbCase struct {
	Rules []struct {
		ID   int               `json:"id"`
		Cols map[string]string `json:"cols"`
	} `json:"rules"`
	History []struct {
		Rule   int    `json:"rule"`
		Device string `json:"device"`
		Ago    int    `json:"ago"`
		Form   string `json:"form"`
	} `json:"history"`
	Fn           string            `json:"fn"`
	Device       string            `json:"device"`
	Type         string            `json:"type"`
	Metric       string            `json:"metric"`
	Telemetry    map[string]string `json:"telemetry"`
	Devices      string            `json:"devices"`
	DevicesThrow *string           `json:"devicesThrow"`
	Now          int64             `json:"now"`
	Mail         string            `json:"mail"`

	Stored    []string     `json:"stored"`
	Throws    *string      `json:"throws"`
	Rows      []string     `json:"rows"`
	Triggered []string     `json:"triggered"`
	Mails     []oracleMail `json:"mails"`
	ListCalls int          `json:"listCalls"`
	Logs      [][2]string  `json:"logs"`
}

var ruleColumns = []string{"name", "description", "enabled", "condition_type", "condition_op", "condition_value", "severity", "scope_device_id", "cooldown_secs", "notify_emails"}

func mustExec(t testing.TB, db *store.DB, query string, values ...jsbind.Value) {
	t.Helper()
	args, err := jsbind.Args(db.Dialect(), len(values), values...)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	if _, err := db.Exec(context.Background(), query, args...); err != nil {
		t.Fatalf("%s %v: %v", query, args, err)
	}
}

// cellRows reads rows as JSON lists, the way JSON.stringify wrote the
// rows the Node driver returned. fourBytes names the columns to give as
// the four-byte real Node's PostgreSQL column held (BUGS AE4): the
// shortest number that names the value in four bytes.
func cellRows(t testing.TB, db *store.DB, query string, columns int, fourBytes map[int]bool) []string {
	t.Helper()
	rows, err := db.Query(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		cells := make([]jsbind.Cell, columns)
		dest := make([]any, columns)
		for i := range cells {
			dest[i] = &cells[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		parts := make([]string, columns)
		for i, cell := range cells {
			v := cell.Value()
			if f := v.ToNumber(); fourBytes[i] && v.IsNumber() && !math.IsNaN(f) && !math.IsInf(f, 0) {
				short, _ := strconv.ParseFloat(strconv.FormatFloat(f, 'g', -1, 32), 64)
				v = jsbind.Number(short)
			}
			parts[i] = v.JSON()
		}
		out = append(out, "["+strings.Join(parts, ",")+"]")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestEngineAsNodeOnRows(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		var recording struct {
			Dialect string   `json:"dialect"`
			Cases   []dbCase `json:"cases"`
		}
		readGzip(t, "db_"+db.Dialect().String()+".json.gz", &recording)
		if recording.Dialect != db.Dialect().String() || len(recording.Cases) < 800 {
			t.Fatalf("recording of %s with %d cases", recording.Dialect, len(recording.Cases))
		}
		ctx := context.Background()
		counts := map[string]int{}
		for i := range recording.Cases {
			c := &recording.Cases[i]
			for _, table := range []string{"alert_history", "alert_rules"} {
				if _, err := db.Exec(ctx, "DELETE FROM "+table); err != nil {
					t.Fatal(err)
				}
			}
			// Rules get older down the list, a second apart
			for k, rule := range c.Rules {
				values := []jsbind.Value{jsbind.Number(float64(rule.ID))}
				for _, column := range ruleColumns {
					values = append(values, bindValue(t, rule.Cols[column]))
				}
				values = append(values, jsbind.String("2026-01-01 00:00:"+strconv.Itoa(59-k)))
				mustExec(t, db, "INSERT INTO alert_rules (id, "+strings.Join(ruleColumns, ", ")+", created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", values...)
			}
			for _, h := range c.History {
				at := time.Now().Add(-time.Duration(h.Ago) * time.Second).UTC()
				text := store.ISOTime(at)
				if h.Form == "space" {
					text = at.Format("2006-01-02 15:04:05")
				}
				mustExec(t, db, "INSERT INTO alert_history (rule_id, device_id, severity, message, triggered_at) VALUES (?, ?, ?, ?, ?)",
					jsbind.Number(float64(h.Rule)), bindValue(t, h.Device), jsbind.String("warning"), jsbind.String("seeded"), jsbind.String(text))
			}
			storedRules := "SELECT id, " + strings.Join(ruleColumns, ", ") + " FROM alert_rules ORDER BY id"
			stored := cellRows(t, db, storedRules, 11, nil)
			// BUGS AE4: on PostgreSQL Node's column held four bytes, the Go
			// console's eight
			nodeStored, widened := stored, false
			if db.IsPostgres() {
				nodeStored = cellRows(t, db, storedRules, 11, map[int]bool{6: true})
				widened = !sameStrings(nodeStored, stored)
			}
			if !sameStrings(nodeStored, c.Stored) {
				t.Fatalf("case %d: the database holds %q\nNode's: %q", i, stored, c.Stored)
			}

			mailer := &fakeMailer{mode: c.Mail}
			logs := &logLines{}
			listCalls := 0
			engine := New(Options{
				DB: db, Mail: mailer, Logger: logs, Location: time.UTC,
				Devices: func(context.Context) ([]*goapi.Object, error) {
					listCalls++
					if c.DevicesThrow != nil {
						return nil, errors.New(*c.DevicesThrow)
					}
					return deviceObjects(t, c.Devices), nil
				},
			})
			var err error
			switch c.Fn {
			case "rules":
				err = engine.EvaluateRules(ctx, bindValue(t, c.Device), c.Type, bindValue(t, c.Metric), EvalOptions{})
			case "telemetry":
				err = engine.EvaluateTelemetry(ctx, bindValue(t, c.Device), Telemetry{
					CPUUsagePercent:  bindValue(t, c.Telemetry["cpu_usage_percent"]),
					MemoryUsedBytes:  bindValue(t, c.Telemetry["memory_used_bytes"]),
					MemoryTotalBytes: bindValue(t, c.Telemetry["memory_total_bytes"]),
				})
			case "sweep":
				err = engine.PeriodicCheckAt(ctx, time.UnixMilli(c.Now))
			default:
				t.Fatalf("case %d: unknown function %q", i, c.Fn)
			}
			engine.Wait()
			counts[c.Fn]++
			describe := func() string {
				in, _ := json.Marshal(struct {
					Fn, Device, Type, Metric string
					Rules, History           any
					Telemetry                map[string]string
				}{c.Fn, c.Device, c.Type, c.Metric, c.Rules, c.History, c.Telemetry})
				return string(in)
			}
			rows := cellRows(t, db, "SELECT rule_id, device_id, severity, message, acknowledged, acknowledged_by FROM alert_history WHERE message <> 'seeded' ORDER BY id", 6, nil)
			if widened && (!sameStrings(rows, c.Rows) || (err == nil) != (c.Throws == nil)) {
				// BUGS AE4: a limit Node's four bytes rounded compares, and
				// reads in the alert's message, as it was given
				counts["AE4: a limit PostgreSQL kept in four bytes"]++
				continue
			}
			var cols []map[string]string
			for _, rule := range c.Rules {
				cols = append(cols, map[string]string{"id": strconv.Itoa(rule.ID), "condition_op": rule.Cols["condition_op"]})
			}
			if memberRules := memberOperatorRules(cols); len(memberRules) > 0 {
				// BUGS AE2, as in TestEngineAsNodeWithoutDatabase
				counts["AE2: a rule with an object's member as its operator"]++
				if err != nil && strings.Contains(err.Error(), "undefined or null") {
					t.Fatalf("case %d %s: %v", i, describe(), err)
				}
				for _, row := range rows {
					for _, id := range memberRules {
						if strings.HasPrefix(row, "["+id+",") {
							t.Fatalf("case %d %s: rule %s raised %s", i, describe(), id, row)
						}
					}
				}
				continue
			}
			if never, ambiguous := sweepNeverOnline(t, c.Fn, c.Devices); len(never) > 0 {
				// BUGS AE1, as in TestEngineAsNodeWithoutDatabase
				counts["AE1: a device never online in the sweep"]++
				if ambiguous || c.Throws != nil {
					if len(rows) > len(c.Rows) || err != nil && (c.Throws == nil || err.Error() != *c.Throws) {
						t.Fatalf("case %d %s: %v, alerts %q", i, describe(), err, rows)
					}
					continue
				}
				c.Rows = withoutDevices(c.Rows, never, func(row string) string { return row })
				c.Mails = withoutDevices(c.Mails, never, func(m oracleMail) string { return m.Alert["device_id"] })
				c.Logs = withoutDevices(c.Logs, never, func(line [2]string) string { return line[1] })
				c.Triggered = c.Triggered[:len(c.Rows)]
			}
			if nodeChecks, goChecks := memoryChecks(t, c.Telemetry); c.Fn == "telemetry" && nodeChecks != goChecks {
				// BUGS AE5, as in TestEngineAsNodeWithoutDatabase: memory
				// comes last, so the alerts before it are the same
				counts["AE5: memory checked by one side only"]++
				short, long := c.Rows, rows
				if nodeChecks {
					short, long = rows, c.Rows
				}
				if !isPrefix(short, long) || err != nil && (c.Throws == nil || err.Error() != *c.Throws) {
					t.Fatalf("case %d %s: alerts %q, Node: %q, %v", i, describe(), rows, c.Rows, err)
				}
				continue
			}
			var fields []map[string]string
			for _, rule := range c.Rules {
				fields = append(fields, map[string]string{"id": strconv.Itoa(rule.ID), "condition_value": rule.Cols["condition_value"], "cooldown_secs": rule.Cols["cooldown_secs"]})
			}
			if badLimit, badCooldown := oddRules(t, fields); c.Throws != nil && len(badLimit)+len(badCooldown) > 0 || slices.ContainsFunc(c.Rows, func(row string) bool {
				id, _, _ := strings.Cut(strings.TrimPrefix(row, "["), ",")
				return badLimit[id] || badCooldown[id]
			}) {
				// BUGS AE3, as in TestEngineAsNodeWithoutDatabase: the
				// cooldown of 300 seconds holds back alerts that Node's
				// cooldown let through
				counts["AE3: a rule whose limit or cooldown Node read as it was"]++
				if err != nil && (c.Throws == nil || err.Error() != *c.Throws) {
					t.Fatalf("case %d %s: error %v, Node threw %s", i, describe(), err, show(c.Throws))
				}
				if !subsequence(rows, c.Rows) || slices.ContainsFunc(rows, func(row string) bool {
					id, _, _ := strings.Cut(strings.TrimPrefix(row, "["), ",")
					return badLimit[id]
				}) {
					t.Fatalf("case %d %s: alerts %q\nNode: %q", i, describe(), rows, c.Rows)
				}
				continue
			}
			if (err == nil) != (c.Throws == nil) || (err != nil && err.Error() != *c.Throws) {
				t.Fatalf("case %d %s: error %v, Node threw %s", i, describe(), err, show(c.Throws))
			}
			if !sameStrings(rows, c.Rows) {
				t.Fatalf("case %d %s: alerts %q\nNode: %q\nlogs %q\nNode's %q", i, describe(), rows, c.Rows, logs.lines, c.Logs)
			}
			counts["alerts"] += len(rows)
			if len(c.Rows) == 0 && len(c.History) > 0 {
				counts["held back by the cooldown"]++
			}
			// The time of an alert: ISO text on SQLite, a time on PostgreSQL
			times, err := store.Select(ctx, db, "SELECT triggered_at FROM alert_history WHERE message <> 'seeded' ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			for k := 0; times.Next(); k++ {
				var at store.Time
				if err := times.Scan(&at); err != nil {
					t.Fatal(err)
				}
				instant, ok := at.Instant()
				if !ok || time.Since(instant) > time.Minute || time.Since(instant) < -time.Minute || !regexp.MustCompile(isoForm).MatchString(at.String()) {
					t.Fatalf("case %d: alert time %q", i, at.String())
				}
				if want := map[bool]string{false: "iso", true: "date"}[db.IsPostgres()]; c.Triggered[k] != want {
					t.Fatalf("case %d: Node read the alert time as %s", i, c.Triggered[k])
				}
			}
			times.Close()
			if got, want := mailer.keys(), mailKeys(c.Mails); !sameStrings(got, want) {
				t.Fatalf("case %d %s: mails %q\nNode: %q", i, describe(), got, want)
			}
			counts["mails"] += len(c.Mails)
			if listCalls != c.ListCalls {
				t.Fatalf("case %d %s: device list read %d times, Node: %d", i, describe(), listCalls, c.ListCalls)
			}
			// The text of a database error is the driver's own; the rest
			// of the log is the same.
			blur := func(lines [][2]string) [][2]string {
				out := make([][2]string, len(lines))
				for k, line := range lines {
					if at := strings.Index(line[1], "Failed to create alert: "); at >= 0 && !strings.HasSuffix(line[1], errNotPrimitive.Error()) {
						line[1] = line[1][:at] + "Failed to create alert: (database)"
					}
					out[k] = line
				}
				return out
			}
			gotOrdered, gotMails := splitLogs(blur(logs.lines))
			wantOrdered, wantMails := splitLogs(blur(c.Logs))
			if !sameLines(gotOrdered, wantOrdered) || !sameStrings(gotMails, wantMails) {
				t.Fatalf("case %d %s: logs %q\nNode: %q", i, describe(), logs.lines, c.Logs)
			}
		}
		for _, name := range []string{"rules", "telemetry", "sweep", "alerts", "mails", "held back by the cooldown", "AE1: a device never online in the sweep",
			"AE3: a rule whose limit or cooldown Node read as it was", "AE5: memory checked by one side only"} {
			if counts[name] == 0 {
				t.Errorf("the recording has no %q", name)
			}
		}
		if db.IsPostgres() && counts["AE4: a limit PostgreSQL kept in four bytes"] == 0 {
			t.Error("the PostgreSQL recording has no limit its four bytes rounded")
		}
		t.Logf("%d cases: %v", len(recording.Cases), counts)
	})
}
