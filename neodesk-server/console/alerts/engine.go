// Package alerts is the console's alert rules engine: it checks what
// devices report against the alert rules, writes an alert when a rule's
// condition holds, and has the alert mailed to the rule's recipients.
//
// A rule (a row of alert_rules) has a condition type, an operator and a
// value:
//
//	cpu_usage         the CPU load a device reports with its telemetry
//	memory_usage      used memory in percent, from the same report
//	disk_usage        the fullest disk in percent, from an inventory
//	idle_duration     seconds without input, from an activity report
//	offline_duration  seconds since a device was last seen, from the sweep
//	custom            never checked: it has nothing to compare
//
// The operators are gt, gte, lt, lte, eq and neq. A rule whose value is no
// finite number holds for nothing. A rule can be limited to one device,
// and it stays quiet for its cooldown (300 seconds unless it sets a whole
// number above 0) after it raised an alert for a device.
//
// The device routes hand the engine request values unchecked, and the
// rules are rows with whatever the database holds, so the engine works on
// JavaScript values and follows JavaScript's arithmetic: the Node console
// is the specification, and what it did with the text "95", a list or an
// empty value is what happens here.
//
// Nothing in the package runs on its own. The routes call EvaluateRules
// and EvaluateTelemetry (usually through EvaluateInBackground), and the
// console's job registry calls Sweep once a minute when it is told to.
package alerts

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/mail"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/logging"
)

// DefaultCooldownSecs is the time between two alerts of one rule for one
// device when the rule sets none.
const DefaultCooldownSecs = 300

// SweepInterval is how often the offline sweep runs.
const SweepInterval = time.Minute

// The alert mails: how many are sent at a time, and how many may wait. A
// sweep that finds many devices offline at once fills the queue, and the
// mails beyond it are dropped instead of opening a connection each.
const (
	DefaultMailWorkers = 4
	DefaultMailQueue   = 500
)

// Mailer sends the mail for an alert. *mail.Service is one.
type Mailer interface {
	SendAlertEmail(ctx context.Context, alert mail.Alert, rule mail.AlertRule) (bool, error)
}

// Logger takes the lines the engine logs. *kit.Logger is one.
type Logger interface {
	Info(args ...any)
	Error(args ...any)
}

// Options are the settings of an Engine.
type Options struct {
	// DB is the console's database: alert_rules and alert_history.
	DB *store.DB
	// Mail sends the alert mails. Nil sends none.
	Mail Mailer
	// Devices returns the device list for the offline sweep, as
	// serverBackend.getAllDevices() did: goapi.Backend.GetAllDevices with
	// no filter. The sweep reads id, online, banned, soft_deleted,
	// remote_target and last_online of each.
	Devices func(ctx context.Context) ([]*goapi.Object, error)
	// Now is the clock of the sweep. Nil means time.Now. The time of an
	// alert is the database's.
	Now func() time.Time
	// Location is the time zone a last_online without a zone is read in.
	// Nil means time.Local, the zone of the process, as in Node.
	Location *time.Location
	// Logger takes what the engine logs. Nil logs through the logging
	// package under "AlertEngine".
	Logger Logger
	// Context ends the work the engine does beside a request (background
	// evaluations, alert mails) when the console stops. Nil means it is
	// never cut short.
	Context context.Context
	// MailTimeout is how long one alert mail may take. Zero means ten
	// minutes, the longest nodemailer itself waited on a silent server.
	MailTimeout time.Duration
	// MailWorkers is how many alert mails are sent at a time. Zero means
	// DefaultMailWorkers.
	MailWorkers int
	// MailQueue is how many alert mails may wait to be sent. Zero means
	// DefaultMailQueue.
	MailQueue int
	// BackgroundTimeout is how long one background evaluation may take.
	// Zero means five minutes.
	BackgroundTimeout time.Duration
	// SweepEvery is the time between two sweeps of Run. Zero means
	// SweepInterval.
	SweepEvery time.Duration
}

// Engine evaluates alert rules. It is safe for concurrent use.
type Engine struct {
	opts  Options
	store ruleStore
	log   Logger
	now   func() time.Time
	base  context.Context

	// background counts evaluations and mails still running or waiting
	background sync.WaitGroup
	// mailQueue holds the alert mails waiting to be sent, mailWorkers is
	// the number of goroutines sending them
	mailMu      sync.Mutex
	mailQueue   []alertMail
	mailWorkers int
	// sweeping is set while a sweep runs
	sweeping atomic.Bool
}

// New returns an engine. It starts nothing: no timer, no goroutine, no
// query.
func New(opts Options) *Engine {
	e := &Engine{opts: opts, store: dbStore{db: opts.DB}, log: opts.Logger, now: opts.Now, base: opts.Context}
	if e.log == nil {
		e.log = defaultLogger{}
	}
	if e.now == nil {
		e.now = time.Now
	}
	if e.base == nil {
		e.base = context.Background()
	}
	if e.opts.Location == nil {
		e.opts.Location = time.Local
	}
	if e.opts.MailTimeout <= 0 {
		e.opts.MailTimeout = 10 * time.Minute
	}
	if e.opts.MailWorkers <= 0 {
		e.opts.MailWorkers = DefaultMailWorkers
	}
	if e.opts.MailQueue <= 0 {
		e.opts.MailQueue = DefaultMailQueue
	}
	if e.opts.BackgroundTimeout <= 0 {
		e.opts.BackgroundTimeout = 5 * time.Minute
	}
	if e.opts.SweepEvery <= 0 {
		e.opts.SweepEvery = SweepInterval
	}
	return e
}

type defaultLogger struct{}

func (defaultLogger) Info(args ...any) { logging.Logf("AlertEngine", "%s", fmt.Sprint(args...)) }
func (defaultLogger) Error(args ...any) {
	logging.Logf("AlertEngine", "ERROR: %s", fmt.Sprint(args...))
}

// Wait returns when the background evaluations and alert mails started so
// far have finished.
func (e *Engine) Wait() { e.background.Wait() }

// ---- conditions ----

// EvalCondition reports whether value and threshold, both as numbers
// (Number(...)), stand in the relation op names: gt, gte, lt, lte, eq
// (===) or neq (!==). A value that is no number is NaN, for which only
// neq holds.
//
// Any other op is false, also the names every JavaScript object carries
// ("constructor", "toString", ...), which the Node console looked up as
// if they were operators. The error is the TypeError for a value or
// threshold JavaScript cannot make a number of.
func EvalCondition(value jsbind.Value, op string, threshold jsbind.Value) (bool, error) {
	var compare func(a, b float64) bool
	switch op {
	case "gt":
		compare = func(a, b float64) bool { return a > b }
	case "gte":
		compare = func(a, b float64) bool { return a >= b }
	case "lt":
		compare = func(a, b float64) bool { return a < b }
	case "lte":
		compare = func(a, b float64) bool { return a <= b }
	case "eq":
		compare = func(a, b float64) bool { return a == b }
	case "neq":
		compare = func(a, b float64) bool { return a != b }
	default:
		return false, nil
	}
	a, err := jsNumber(value)
	if err != nil {
		return false, err
	}
	b, err := jsNumber(threshold)
	if err != nil {
		return false, err
	}
	return compare(a, b), nil
}

// ---- evaluation ----

func (e *Engine) loadRules(ctx context.Context, metricType string) []Rule {
	rules, err := e.store.enabledRules(ctx, metricType)
	if err != nil {
		e.log.Error("Failed to load rules: " + jsbind.Message(err))
		return nil
	}
	return rules
}

// evaluateAgainst checks one value against rules already loaded (all of
// one condition type). minCooldownSecs stretches the cooldown: an offline
// device stays over its limit for as long as it is offline, so its alert
// counts for that whole time instead of being raised again every few
// minutes.
func (e *Engine) evaluateAgainst(ctx context.Context, rules []Rule, deviceID jsbind.Value, metricType string, metric, minCooldownSecs float64) error {
	for _, rule := range rules {
		if err := ctx.Err(); err != nil {
			return err
		}
		// A rule for one device looks at that device only
		if rule.ScopeDeviceID.Truthy() && !strictEqual(rule.ScopeDeviceID, deviceID) {
			continue
		}
		op, err := jsString(rule.ConditionOp)
		if err != nil {
			return err
		}
		triggered, err := EvalCondition(jsbind.Number(metric), op, rule.ConditionValue)
		if err != nil {
			return err
		}
		if !triggered {
			continue
		}
		// A limit that is no finite number (text on SQLite, an infinity on
		// PostgreSQL) holds for nothing; EvalCondition has read it already
		if limit, _ := jsNumber(rule.ConditionValue); math.IsNaN(limit) || math.IsInf(limit, 0) {
			continue
		}

		cooldown, err := ruleCooldown(rule.CooldownSecs)
		if err != nil {
			return err
		}
		cooldown = jsMax(cooldown, minCooldownSecs)

		// The check and the insert are one step, so reports that arrive
		// together raise one alert
		var (
			message, triggeredAt string
			inCooldown           bool
			textErr              error
		)
		severity := rule.Severity.Or(jsbind.String("warning"))
		err = e.store.cooldownStep(ctx, rule.ID, deviceID, func(s alertStore) error {
			// A check that fails does not hold the alert back
			if recent, err := s.recentAlert(ctx, rule.ID, deviceID, jsbind.Number(cooldown)); err == nil && recent {
				inCooldown = true
				return nil
			}
			var t texts
			message = t.of(rule.Name) + ": " + metricType + " = " + jsbind.NumberString(metric) +
				" (" + op + " " + t.of(rule.ConditionValue) + ")"
			if textErr = t.err; textErr != nil {
				return textErr
			}
			// The database stamps the alert: its clock is the one the
			// cooldown is checked against
			var err error
			triggeredAt, err = s.createAlert(ctx, NewAlert{RuleID: rule.ID, DeviceID: deviceID, Severity: severity, Message: jsbind.String(message)})
			return err
		})
		if textErr != nil {
			return textErr
		}
		if inCooldown {
			continue
		}
		var device string
		if err == nil {
			device, err = jsString(deviceID)
		}
		if err != nil {
			e.log.Error("Failed to create alert: " + jsbind.Message(err))
			continue
		}
		e.log.Info("ALERT fired: " + message + " for " + device)

		if rule.NotifyEmails.Truthy() && e.opts.Mail != nil {
			e.mailAlert(
				mail.Alert{Severity: severity, DeviceID: deviceID, Message: jsbind.String(message), TriggeredAt: jsbind.String(triggeredAt)},
				mail.AlertRule{
					Name: rule.Name, Description: rule.Description, ConditionType: rule.ConditionType,
					ConditionOp: rule.ConditionOp, ConditionValue: rule.ConditionValue, NotifyEmails: rule.NotifyEmails,
				})
		}
	}
	return nil
}

// ruleCooldown is a rule's cooldown in seconds: the stored value when it is
// a whole number above 0 (also text of one, which SQLite can hold), else
// DefaultCooldownSecs, also for 0 and null, which the rule routes store as
// 300 on a new rule and in a change; older rows can hold them. The
// error is the TypeError for a value JavaScript cannot make a number of.
func ruleCooldown(stored jsbind.Value) (float64, error) {
	secs, err := jsNumber(stored)
	if err != nil {
		return 0, err
	}
	if secs <= 0 || secs != math.Trunc(secs) || math.IsInf(secs, 0) {
		return DefaultCooldownSecs, nil
	}
	return secs, nil
}

// alertMail is an alert mail waiting to be sent.
type alertMail struct {
	alert mail.Alert
	rule  mail.AlertRule
}

// mailAlert queues the alert mail; the evaluation does not wait for it.
// At most MailWorkers mails are sent at a time, each over a connection of
// its own, and at most MailQueue wait: a mail that finds the queue full
// is dropped and logged. A failure is logged.
func (e *Engine) mailAlert(alert mail.Alert, rule mail.AlertRule) {
	e.mailMu.Lock()
	defer e.mailMu.Unlock()
	if len(e.mailQueue) >= e.opts.MailQueue {
		e.log.Error("Email dropped: " + strconv.Itoa(len(e.mailQueue)) + " alert mails are waiting already")
		return
	}
	e.background.Add(1)
	e.mailQueue = append(e.mailQueue, alertMail{alert, rule})
	if e.mailWorkers < e.opts.MailWorkers {
		e.mailWorkers++
		go e.sendMails()
	}
}

// sendMails sends queued alert mails until the queue is empty.
func (e *Engine) sendMails() {
	for {
		e.mailMu.Lock()
		if len(e.mailQueue) == 0 {
			e.mailWorkers--
			e.mailMu.Unlock()
			return
		}
		next := e.mailQueue[0]
		e.mailQueue[0] = alertMail{}
		e.mailQueue = e.mailQueue[1:]
		e.mailMu.Unlock()

		ctx, cancel := context.WithTimeout(e.base, e.opts.MailTimeout)
		if _, err := e.opts.Mail.SendAlertEmail(ctx, next.alert, next.rule); err != nil {
			e.log.Error("Email failed: " + err.Error())
		}
		cancel()
		e.background.Done()
	}
}

// EvalOptions are the options of EvaluateRules.
type EvalOptions struct {
	// MinCooldownSecs is the shortest cooldown to apply, whatever the
	// rules say.
	MinCooldownSecs float64
}

// EvaluateRules checks a value a device reported against the enabled
// rules of one condition type and raises the alerts that are due.
//
// deviceID and metricValue are the request's values as they came. Nothing
// happens without a device id, for a metric that is null, undefined or
// the empty text, and for one that is no finite number after Number():
// "95", true and [95] are numbers, "abc" and {} are not.
//
// The error is what the Node console threw: the TypeError for a value
// JavaScript cannot make a number or text of, or for a rule whose
// operator is one of the names EvalCondition fails on. A failing database
// is not an error: it is logged, and a rule whose cooldown cannot be
// checked raises its alert.
func (e *Engine) EvaluateRules(ctx context.Context, deviceID jsbind.Value, metricType string, metricValue jsbind.Value, opts EvalOptions) error {
	if !deviceID.Truthy() || metricValue.IsNull() || metricValue.IsUndefined() || (metricValue.IsString() && metricValue.Str() == "") {
		return nil
	}
	value, err := jsNumber(metricValue)
	if err != nil {
		return err
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}
	rules := e.loadRules(ctx, metricType)
	if len(rules) == 0 {
		return nil
	}
	return e.evaluateAgainst(ctx, rules, deviceID, metricType, value, opts.MinCooldownSecs)
}

// Telemetry is the part of a device's telemetry report the rules look at,
// as the request had it.
type Telemetry struct {
	CPUUsagePercent  jsbind.Value
	MemoryUsedBytes  jsbind.Value
	MemoryTotalBytes jsbind.Value
}

// EvaluateTelemetry checks a telemetry report: the CPU load against the
// cpu_usage rules when the report has one, and used memory in percent,
// rounded to a whole number, against the memory_usage rules when the
// report has both memory values (neither missing, null nor the empty
// text, 0 used counts) and the total is above 0. The error is as for
// EvaluateRules.
func (e *Engine) EvaluateTelemetry(ctx context.Context, deviceID jsbind.Value, telemetry Telemetry) error {
	if cpu := telemetry.CPUUsagePercent; !cpu.IsUndefined() && !cpu.IsNull() {
		if err := e.EvaluateRules(ctx, deviceID, "cpu_usage", cpu, EvalOptions{}); err != nil {
			return err
		}
	}
	set := func(v jsbind.Value) bool { return !v.IsUndefined() && !v.IsNull() && !(v.IsString() && v.Str() == "") }
	if set(telemetry.MemoryUsedBytes) && set(telemetry.MemoryTotalBytes) {
		used, err := jsNumber(telemetry.MemoryUsedBytes)
		if err != nil {
			return err
		}
		total, err := jsNumber(telemetry.MemoryTotalBytes)
		if err != nil || !(total > 0) {
			return err
		}
		ratio := used / total * 100
		return e.EvaluateRules(ctx, deviceID, "memory_usage", jsbind.Number(jsRound(ratio)), EvalOptions{})
	}
	return nil
}

// EvaluateInBackground runs an evaluation without holding up the device's
// request: fn runs in a goroutine of its own, with a context that
// outlives the request. A failure, also a panic, is logged and goes no
// further.
func (e *Engine) EvaluateInBackground(fn func(ctx context.Context) error) {
	e.background.Add(1)
	go func() {
		defer e.background.Done()
		ctx, cancel := context.WithTimeout(e.base, e.opts.BackgroundTimeout)
		defer cancel()
		defer func() {
			if p := recover(); p != nil {
				e.log.Error("Evaluation failed: " + fmt.Sprint(p))
			}
		}()
		if err := fn(ctx); err != nil {
			e.log.Error("Evaluation failed: " + jsbind.Message(err))
		}
	}()
}

// PeriodicCheck is one offline sweep: every device that has been offline
// for a while is checked against the offline_duration rules, with the
// seconds since it was last seen. The device list is read only when such
// a rule is on. Devices that are online, banned or deleted, RDP and VNC
// targets, and devices never seen (no last_online, one that is no date,
// or one before 1970) are left out.
//
// The cooldown of an offline alert is at least as long as the device has
// been offline, so one stretch offline raises one alert per rule.
//
// The error is what the Node console threw, as for EvaluateRules; it ends
// the sweep where it happens.
func (e *Engine) PeriodicCheck(ctx context.Context) error {
	return e.PeriodicCheckAt(ctx, e.now())
}

// PeriodicCheckAt is PeriodicCheck with the time to measure "offline
// since" from, which the Node function took as its `now` option. The
// alerts it raises carry the database's time.
func (e *Engine) PeriodicCheckAt(ctx context.Context, at time.Time) error {
	rules := e.loadRules(ctx, "offline_duration")
	if len(rules) == 0 {
		return nil
	}
	if e.opts.Devices == nil {
		e.log.Error("Device list failed: no device list")
		return nil
	}
	devices, err := e.opts.Devices(ctx)
	if err != nil {
		e.log.Error("Device list failed: " + jsbind.Message(err))
		return nil
	}
	now := float64(at.UnixMilli())
	for _, d := range devices {
		if err := ctx.Err(); err != nil {
			return err
		}
		if d == nil || !goapi.Truthy(d.Get("id")) || d.Bool("online") || d.Bool("banned") || d.Bool("soft_deleted") || d.Bool("remote_target") {
			continue
		}
		lastOnline := ""
		if v := d.Get("last_online"); goapi.Truthy(v) {
			if lastOnline, err = anyString(v); err != nil {
				return err
			}
		}
		last, ok := dateParse(lastOnline, e.opts.Location)
		// A time before 1970 is no time the device was seen: the API server
		// sends Go's zero time for a device that was never online
		if !ok || last < 0 {
			continue
		}
		secs := math.Floor((now - last) / 1000)
		if secs <= 0 {
			continue
		}
		id, ok := anyValue(d.Get("id"))
		if !ok {
			// A list or an object as an id: the server never sends one
			e.log.Error("Device list failed: a device id that is no text")
			continue
		}
		if err := e.evaluateAgainst(ctx, rules, id, "offline_duration", secs, secs); err != nil {
			return err
		}
	}
	return nil
}

// Run is the console's "alert sweep" job: a Sweep once a minute, the
// first after a minute, until ctx ends. A sweep that takes longer than a
// minute makes the runs that fall into it drop out; the next one comes
// with the next tick after it.
//
// Run is start() of the Node console's engine. The engine never calls it
// itself: the console runs it as its "alert sweep" job, one per process.
func (e *Engine) Run(ctx context.Context) {
	ticker := time.NewTicker(e.opts.SweepEvery)
	defer ticker.Stop()
	e.sweepOn(ctx, ticker.C)
}

// sweepOn runs a Sweep for every tick until ctx ends.
func (e *Engine) sweepOn(ctx context.Context, ticks <-chan time.Time) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticks:
			e.Sweep(ctx)
			// A run that came due during the sweep is dropped
			select {
			case <-ticks:
			default:
			}
		}
	}
}

// Sweep is one run of the job: a PeriodicCheck, with its failure logged.
// A call that finds a sweep still running does nothing, so there is one
// sweep at a time however the job is driven.
func (e *Engine) Sweep(ctx context.Context) {
	if !e.sweeping.CompareAndSwap(false, true) {
		return
	}
	defer e.sweeping.Store(false)
	if err := e.PeriodicCheck(ctx); err != nil {
		e.log.Error("Sweep failed: " + jsbind.Message(err))
	}
}
