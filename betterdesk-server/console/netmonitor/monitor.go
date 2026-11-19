// Package netmonitor checks network targets for the console: whether a
// host answers a ping, a port accepts a connection, a URL answers a GET.
// It runs single checks for the network routes, stores the result of a
// target's check, and polls all enabled targets on a schedule once
// somebody has started the monitor.
//
// Every check asks console/ssrfguard first, which refuses loopback,
// link-local and cloud metadata addresses, so the tools cannot be pointed
// at the server itself.
//
// A failed check carries the message Node's error had (errors.go), since
// those messages are stored and shown.
package netmonitor

import (
	"context"
	"math"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/unitronix/betterdesk-server/console/store"
)

const (
	// DefaultPollIntervalMs is the time between two polls when Start is
	// given none.
	DefaultPollIntervalMs = 60000
	// maxTargetsPerPoll caps the work of one poll.
	maxTargetsPerPoll = 500
)

// Options is what a Monitor works with. Only Store is required.
type Options struct {
	// Store is the console's database.
	Store *store.DB
	// AllowSelfSignedCerts makes HTTPS checks accept any certificate. It
	// is the console's ALLOW_SELF_SIGNED_CERTS setting (default false).
	AllowSelfSignedCerts bool

	// Resolver looks names up. Default: the system's resolver.
	Resolver Resolver
	// Dialer opens the connections of TCP and HTTP checks. Default: a
	// net.Dialer.
	Dialer Dialer
	// Transport sends the requests of HTTP checks. Default: a transport
	// on Dialer and Resolver without proxy, compression and HTTP/2.
	Transport http.RoundTripper
	// Ping runs the ping command. Default: the system's.
	Ping PingRunner
	// Windows picks the arguments and the output format of ping on
	// Windows. Default: whether the server runs on Windows.
	Windows *bool
	// Now is the clock. Default: time.Now.
	Now func() time.Time
	// Log takes the monitor's log lines. Default: none.
	Log Logger
}

// Logger is where the monitor writes its log lines. *kit.Logger is one.
type Logger interface {
	Info(args ...any)
	Error(args ...any)
}

type noLog struct{}

func (noLog) Info(...any)  {}
func (noLog) Error(...any) {}

// Monitor runs checks and the polling loop (the NetworkMonitor class and
// the check functions of services/networkMonitor.js).
type Monitor struct {
	db     *store.DB
	probe  prober
	now    func() time.Time
	log    Logger
	wakeup chan struct{}

	mu sync.Mutex
	// running is true from Start to Stop.
	running bool
	// interval is the time between two polls, in milliseconds.
	interval Number
	// inFlight is true while a poll runs.
	inFlight bool
	// generation counts the changes of running, so the loop notices a
	// stop and start while it waits.
	generation uint64
}

// New returns a monitor that is not polling.
func New(opts Options) *Monitor {
	if opts.Resolver == nil {
		opts.Resolver = net.DefaultResolver
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Dialer == nil {
		opts.Dialer = &net.Dialer{}
	}
	if opts.Transport == nil {
		opts.Transport = newTransport(&connector{
			dialer:          opts.Dialer,
			resolver:        opts.Resolver,
			allowSelfSigned: opts.AllowSelfSignedCerts,
			now:             opts.Now,
		})
	}
	if opts.Ping == nil {
		opts.Ping = runCommand
	}
	windows := runtime.GOOS == "windows"
	if opts.Windows != nil {
		windows = *opts.Windows
	}
	if opts.Log == nil {
		opts.Log = noLog{}
	}
	return &Monitor{
		db: opts.Store,
		probe: prober{
			guard:     newGuard(opts.Resolver),
			resolver:  opts.Resolver,
			dialer:    opts.Dialer,
			transport: opts.Transport,
			ping:      opts.Ping,
			windows:   windows,
			now:       opts.Now,
		},
		now:      opts.Now,
		log:      opts.Log,
		wakeup:   make(chan struct{}, 1),
		interval: DefaultPollIntervalMs,
	}
}

// ResolveHost returns the first address of a host that passes the guard.
// The error's text is for the client.
func (m *Monitor) ResolveHost(ctx context.Context, host string) (string, error) {
	return m.probe.resolveHost(ctx, host)
}

// PingHost sends one echo request with the system's ping command.
func (m *Monitor) PingHost(ctx context.Context, host string, timeoutMs Number) Result {
	return m.probe.pingHost(ctx, host, timeoutMs)
}

// CheckTCPPort opens a connection to a port and closes it.
func (m *Monitor) CheckTCPPort(ctx context.Context, host string, port, timeoutMs Number) Result {
	return m.probe.checkTCPPort(ctx, host, port, timeoutMs)
}

// CheckHTTP sends one GET request. A status from 200 to 399 is a
// success; a redirect is not followed.
func (m *Monitor) CheckHTTP(ctx context.Context, rawURL string, timeoutMs Number) Result {
	return m.probe.checkHTTP(ctx, rawURL, timeoutMs)
}

// Running reports whether the monitor polls.
func (m *Monitor) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running
}

// PollInFlight reports whether a poll is under way. A poll that was
// running when the monitor was stopped still finishes.
func (m *Monitor) PollInFlight() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inFlight
}

// PollInterval is the time between two polls in milliseconds.
func (m *Monitor) PollInterval() Number {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.interval
}

// Start makes the monitor poll: once at once, then every intervalMs
// milliseconds after a poll has finished. An interval that is 0 or NaN
// stands for the default. Start does nothing while the monitor runs, so
// the interval of a running monitor stays.
//
// The polls run in Run, which has to be going.
func (m *Monitor) Start(intervalMs Number) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running {
		return
	}
	m.interval = intervalMs.Or(DefaultPollIntervalMs)
	m.running = true
	m.generation++
	m.log.Info("[NetworkMonitor] Started with interval " + m.interval.String() + "ms")
	m.wake()
}

// Stop ends the polling. A poll that is under way finishes.
func (m *Monitor) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.running = false
	m.generation++
	m.log.Info("[NetworkMonitor] Stopped")
	m.wake()
}

// wake tells Run that running changed.
func (m *Monitor) wake() {
	select {
	case m.wakeup <- struct{}{}:
	default:
	}
}

// Run is the monitor's background job. It does nothing until Start, then
// polls until Stop, and returns when ctx ends.
//
// It follows the timers of the old monitor: Start polls at once unless a
// poll is still under way; after a poll the next one is due one interval
// later, with the interval of that moment; Stop drops the wait.
func (m *Monitor) Run(ctx context.Context) {
	for {
		m.mu.Lock()
		running := m.running
		m.inFlight = running
		m.mu.Unlock()

		if !running {
			select {
			case <-ctx.Done():
				return
			case <-m.wakeup:
			}
			continue
		}

		m.CheckAll(ctx)

		m.mu.Lock()
		m.inFlight = false
		running = m.running
		generation := m.generation
		wait := timerDelay(m.interval)
		m.mu.Unlock()
		if !running {
			continue
		}
		// Wait one interval. A stop ends the wait; a stop and start
		// during the wait polls at once, as a new start does. A start
		// that came while the poll ran changes nothing here.
		timer := time.NewTimer(wait)
	waiting:
		for {
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				break waiting
			case <-m.wakeup:
				m.mu.Lock()
				changed := m.generation != generation
				m.mu.Unlock()
				if changed {
					timer.Stop()
					break waiting
				}
			}
		}
	}
}

// timerDelay is the time a JavaScript timer waits for a delay in
// milliseconds: at least one millisecond, and one millisecond as well for
// a delay that is NaN or too long for a timer.
func timerDelay(ms Number) time.Duration {
	f := float64(ms)
	if math.IsNaN(f) || f < 1 || f > timerMax {
		return time.Millisecond
	}
	return time.Duration(math.Trunc(f)) * time.Millisecond
}

// CheckResult is the answer of CheckTarget: {target_id, ...result,
// status}.
type CheckResult struct {
	TargetID store.Int64String
	Result
	// Status is "up" or "down".
	Status string
}

// CheckTarget runs the check of a target, stores its result in
// network_checks and on the target, and returns it. A result that cannot
// be stored is logged and returned all the same.
func (m *Monitor) CheckTarget(ctx context.Context, target *Target) CheckResult {
	timeout := Number(target.TimeoutMs.V)
	if !target.TimeoutMs.Valid {
		timeout = 0
	}
	timeout = timeout.Or(DefaultTimeoutMs)
	port := Number(target.Port.V)
	if !target.Port.Valid {
		port = 0
	}
	host := "null"
	if target.Host.Valid {
		host = target.Host.String
	}
	checkType := target.CheckType.String

	var result Result
	switch checkType {
	case "ping":
		if !target.Host.Valid {
			result = InvalidHost()
		} else {
			result = m.probe.pingHost(ctx, host, timeout)
		}
	case "tcp":
		if !target.Host.Valid {
			result = InvalidHost()
		} else {
			result = m.probe.checkTCPPort(ctx, host, port.Or(80), timeout)
		}
	case "http", "https":
		rawURL := target.URL.String
		if rawURL == "" {
			defaultPort := Number(80)
			if checkType == "https" {
				defaultPort = 443
			}
			rawURL = checkType + "://" + host + ":" + port.Or(defaultPort).String()
		}
		result = m.probe.checkHTTP(ctx, rawURL, timeout)
	default:
		result = failed("Unknown check type: " + checkType)
	}

	status := "down"
	if result.Success {
		status = "up"
	}
	id := Num(Number(target.ID.V))
	rtt, statusCode, errorMsg := Null(), Null(), Null()
	if result.RTT != nil {
		rtt = Num(*result.RTT)
	}
	if result.StatusCode != nil && *result.StatusCode != 0 {
		statusCode = Num(Number(*result.StatusCode))
	}
	if result.Error != nil {
		errorMsg = Text(*result.Error)
	}
	if err := m.save(ctx, id, status, rtt, statusCode, errorMsg); err != nil {
		m.log.Error("[NetworkMonitor] DB save error for target "+strconv.FormatInt(target.ID.V, 10)+":", DriverMessage(err))
	}
	return CheckResult{TargetID: target.ID, Result: result, Status: status}
}

// save stores the result of a check and marks the target with it.
func (m *Monitor) save(ctx context.Context, id Param, status string, rtt, statusCode, errorMsg Param) error {
	err := InsertCheck(ctx, m.db, NewCheck{TargetID: id, Status: Text(status), RTT: rtt, StatusCode: statusCode, ErrorMsg: errorMsg})
	if err != nil {
		return err
	}
	lastStatus, checkedAt := Text(status), Text(store.ISOTime(m.now()))
	_, err = UpdateTarget(ctx, m.db, id, TargetChanges{LastStatus: &lastStatus, LastCheckAt: &checkedAt, LastRTT: &rtt})
	return err
}

// IsDue reports whether a target's own interval has passed since its last
// check. A small allowance keeps a target whose interval equals the poll
// interval from being skipped because the poll before finished a moment
// late. A target that was never checked is due.
func (m *Monitor) IsDue(target *Target, now time.Time) bool {
	last, ok := jsDate(target.LastCheckAt.String())
	if !ok {
		return true
	}
	pollInterval := m.PollInterval()
	interval := Number(target.IntervalMs.V)
	if !target.IntervalMs.Valid {
		interval = 0
	}
	interval = interval.Or(pollInterval)
	allowance := Number(math.Min(5000, float64(pollInterval)/2))
	return Number(now.UnixMilli()-last.UnixMilli()) >= interval-allowance
}

// The forms of a time JavaScript's Date reads from text: with a zone,
// without one (local time), and a date alone (UTC).
var (
	zonedTimes = []string{"2006-01-02T15:04:05.999999999Z07:00", "2006-01-02T15:04Z07:00", "2006-01-02 15:04:05.999999999Z07:00"}
	localTimes = []string{"2006-01-02T15:04:05.999999999", "2006-01-02T15:04", "2006-01-02 15:04:05.999999999", "2006-01-02 15:04"}
	dates      = []string{"2006-01-02", "2006-01", "2006"}
)

// jsDate is new Date(text): the point in time text names, and false for
// text that names none.
func jsDate(text string) (time.Time, bool) {
	for _, layout := range zonedTimes {
		if t, err := time.Parse(layout, text); err == nil {
			return t, true
		}
	}
	for _, layout := range localTimes {
		if t, err := time.ParseInLocation(layout, text, time.Local); err == nil {
			return t, true
		}
	}
	for _, layout := range dates {
		if t, err := time.Parse(layout, text); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// CheckAll checks every enabled target that is due, one after the other,
// at most 500 in one call.
func (m *Monitor) CheckAll(ctx context.Context) []CheckResult {
	enabled := true
	targets, err := ListTargets(ctx, m.db, TargetFilter{Enabled: &enabled})
	if err != nil {
		m.log.Error("[NetworkMonitor] checkAll error:", DriverMessage(err))
		return []CheckResult{}
	}
	now := m.now()
	var due []*Target
	for i := range targets {
		if m.IsDue(&targets[i], now) && len(due) < maxTargetsPerPoll {
			due = append(due, &targets[i])
		}
	}
	results := []CheckResult{}
	for _, target := range due {
		results = append(results, m.CheckTarget(ctx, target))
	}
	return results
}
