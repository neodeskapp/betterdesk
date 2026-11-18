// Package attestation is the server attestation: a capacity test that
// opens websocket connections to the signal server and the relay until
// the host is 80 percent busy, the tier the number of connections earns,
// and the capacity badge that shows the tier on the sign-in page and at
// the public badge route (services/serverAttestation.js).
//
// The last result and the badge's texts are two rows of the settings
// table. A test runs in the background, one at a time, and can be
// aborted; Status tells how far it got.
package attestation

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/unitronix/betterdesk-server/console/branding"
)

// The two rows of the settings table and the limits of the badge texts.
const (
	ResultKey      = "server_attestation_result"
	BadgeConfigKey = "server_attestation_badge_config"
	BadgeTitleMax  = 96
	BadgeLabelMax  = 32
)

// The rules of a capacity test.
const (
	// Threshold is the load in percent of processor, memory or disk at
	// which the test stops adding connections.
	Threshold = 80
	// stabilizeThreshold is the load below which the host counts as calm
	// again after the test stopped at Threshold.
	stabilizeThreshold = 70
	maxConnections     = 500
	maxTest            = 10 * time.Minute
	baselineWait       = 10 * time.Second
	rampInterval       = 5 * time.Second
	settleWait         = 1500 * time.Millisecond
	stabilizeTime      = 15 * time.Second
	stabilizeInterval  = 3 * time.Second
	batchSignal        = 5
	batchRelay         = 3
	connectTimeout     = 8 * time.Second
	probeTimeout       = 5 * time.Second
	errorRateAbort     = 0.05
	// watchInterval is how often a waiting run looks for an abort.
	watchInterval = 200 * time.Millisecond
)

// Options configure the service. Settings is required; any other zero
// field is the real thing.
type Options struct {
	// Settings is the settings table.
	Settings Settings
	// Network is where the test connects.
	Network Network
	// Snapshot reads the host (serverManagement.getResourceSnapshot).
	// Default: a servermgmt.Host of the service's own (HostSnapshot).
	Snapshot func() Snapshot
	// ServerStats is betterdeskApi.getServerStats: the data of a
	// successful answer (branding.Undefined when it has none), nil when the
	// call failed. Default: always nil.
	ServerStats func(ctx context.Context) any
	// Dialer opens the test's connections. Default: WebSocketDialer.
	Dialer Dialer
	// Clock is the time of the test. Default: RealClock.
	Clock Clock
	// Random gives the bytes of the relay pairs' ids. Default: crypto/rand.
	Random io.Reader
	// Logf writes a log line. Default: none.
	Logf func(format string, args ...any)
}

// Service holds the state of the capacity test and the badge texts it
// read. One console has one.
type Service struct {
	opts   Options
	starts chan *run

	loadOnce sync.Once

	mu             sync.Mutex
	running        bool
	phase          string
	progress       *branding.Object
	abortRequested bool
	pool           *pool
	current        *run
	// lastResult is the result of the last test of this process, or the
	// one stored when the service first gave its status
	lastResult   any
	resultLoaded bool
	// badge is the badge texts as read or saved last; nil until then
	badge *BadgeConfig
	// onProgress is called after each step of a test (tests use it)
	onProgress func()
}

// New returns the service.
func New(opts Options) *Service {
	if opts.Snapshot == nil {
		opts.Snapshot = ownHost()
	}
	if opts.ServerStats == nil {
		opts.ServerStats = func(context.Context) any { return nil }
	}
	if opts.Dialer == nil {
		opts.Dialer = WebSocketDialer{AllowSelfSigned: opts.Network.AllowSelfSigned}
	}
	if opts.Clock == nil {
		opts.Clock = RealClock{}
	}
	if opts.Random == nil {
		opts.Random = rand.Reader
	}
	if opts.Logf == nil {
		opts.Logf = func(string, ...any) {}
	}
	return &Service{opts: opts, starts: make(chan *run, 1), phase: "idle"}
}

// Serve runs the tests that Start asks for, one after the other, until
// ctx ends. The end of ctx also ends a test that runs. It is the job the
// console starts with the service.
func (s *Service) Serve(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case r := <-s.starts:
			_, _ = s.execute(ctx, r)
		}
	}
}

// Status is the state of the capacity test (getStatus).
type Status struct {
	Running bool
	// Phase is idle before the first test, then baseline, ramp-up,
	// stabilize, done, and cool-down from the end of a test on.
	Phase string
	// Progress is what the test reported last (*branding.Object), nil
	// before the first test.
	Progress any
	// LastResult is the result of the last test of this process, or the
	// one that was stored when the service first gave its status; nil
	// when there is none or it is a value JavaScript counts as false.
	LastResult any
}

// Status tells how far the capacity test got.
func (s *Service) Status(ctx context.Context) Status {
	s.loadOnce.Do(func() {
		stored := LastResult(ctx, s.opts.Settings)
		s.mu.Lock()
		if !s.resultLoaded {
			s.lastResult, s.resultLoaded = stored, true
		}
		s.mu.Unlock()
	})
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{Running: s.running, Phase: s.phase}
	if s.progress != nil {
		st.Progress = s.progress
	}
	if branding.Truthy(s.lastResult) {
		st.LastResult = s.lastResult
	}
	return st
}

// LastResult reads the stored result of the last test (getLastResult).
func (s *Service) LastResult(ctx context.Context) any { return LastResult(ctx, s.opts.Settings) }

// Start starts a capacity test in the background (runLoadTest). It is
// false when a test is running already.
func (s *Service) Start() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return false
	}
	s.running = true
	s.abortRequested = false
	s.phase = "baseline"
	s.pool = &pool{}
	r := &run{s: s, started: s.opts.Clock.Now(), abort: make(chan struct{})}
	s.current = r
	select {
	case s.starts <- r:
	default:
	}
	return true
}

// RequestAbort asks the running test to stop and closes its connections
// (requestAbort). It is false when no test runs.
func (s *Service) RequestAbort() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.running {
		return false
	}
	s.abortRequested = true
	if r := s.current; r != nil && !r.aborted {
		r.aborted = true
		r.abortAt = s.opts.Clock.Now()
		close(r.abort)
	}
	if s.pool != nil {
		s.pool.closeAll()
	}
	return true
}

// BadgeConfig is the badge texts (getBadgeConfig). The stored row is read
// once; later calls answer what was read or saved last. A row that cannot
// be read gives the default texts, and they are kept as well.
func (s *Service) BadgeConfig(ctx context.Context) BadgeConfig {
	s.mu.Lock()
	cached := s.badge
	s.mu.Unlock()
	if cached != nil {
		return *cached
	}
	c := readBadgeConfig(ctx, s.opts.Settings)
	s.mu.Lock()
	s.badge = &c
	s.mu.Unlock()
	return c
}

// SaveBadgeConfig cleans and stores the badge texts (saveBadgeConfig).
// It fails where the texts cannot be made text, and when the row cannot
// be written; then the texts read before stay.
func (s *Service) SaveBadgeConfig(ctx context.Context, input any) (BadgeConfig, error) {
	c, err := NormalizeBadgeConfig(input)
	if err != nil {
		return BadgeConfig{}, err
	}
	if err := s.opts.Settings.Set(ctx, BadgeConfigKey, branding.Stringify(c.Object())); err != nil {
		return BadgeConfig{}, err
	}
	s.mu.Lock()
	s.badge = &c
	s.mu.Unlock()
	return c, nil
}

// BadgePresentation is the presentation of the badge texts for a tier
// (getBadgePresentation).
func (s *Service) BadgePresentation(ctx context.Context, tier any, t Translate, brandName any) (*Presentation, error) {
	return ResolveBadgePresentation(s.BadgeConfig(ctx), tier, t, brandName)
}

// PublicBadge is what GET /api/public/server-attestation answers: the
// public summary of the stored result with the badge texts, or the badge
// without a tier when it could not be made.
func (s *Service) PublicBadge(ctx context.Context, t Translate, brandName any) *branding.Object {
	result := s.LastResult(ctx)
	return publicBadge(result, s.BadgeConfig(ctx), t, brandName)
}

// errAborted is what a wait answers when the test was aborted.
var errAborted = errors.New("aborted")

// execute runs a test that Start set up, then puts the service back to
// rest (the finally of runLoadTest). It returns the result, or why there
// is none.
func (s *Service) execute(ctx context.Context, r *run) (*branding.Object, error) {
	result, err := r.loadTest(ctx)
	s.mu.Lock()
	s.phase = "cool-down"
	if s.pool != nil {
		s.pool.closeAll()
	}
	s.pool = nil
	s.current = nil
	s.running = false
	s.abortRequested = false
	if s.progress != nil {
		s.progress = patched(s.progress, "phase", "idle")
	}
	s.mu.Unlock()
	if err != nil {
		s.opts.Logf("run failed: %s", branding.Message(err))
	}
	return result, err
}

// patched is { ...o, ...pairs }: a copy of o with the pairs set; a key o
// has keeps its place.
func patched(o *branding.Object, pairs ...any) *branding.Object {
	out := branding.NewObject()
	if o != nil {
		for _, k := range o.Keys() {
			v, _ := o.Get(k)
			out.Set(k, v)
		}
	}
	for i := 0; i+1 < len(pairs); i += 2 {
		out.Set(pairs[i].(string), pairs[i+1])
	}
	return out
}

// emit records a step of the test in the progress.
func (s *Service) emit(pairs ...any) {
	s.mu.Lock()
	s.progress = patched(s.progress, pairs...)
	hook := s.onProgress
	s.mu.Unlock()
	if hook != nil {
		hook()
	}
}

// nowMillis is Date.now().
func (s *Service) nowMillis() int64 { return s.opts.Clock.Now().UnixMilli() }
