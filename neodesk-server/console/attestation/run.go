package attestation

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/unitronix/betterdesk-server/console/branding"
)

// run is one capacity test.
type run struct {
	s       *Service
	started time.Time
	// abort is closed at the first abort request of the test; aborted
	// (guarded by s.mu) says whether it came
	abort     chan struct{}
	aborted   bool
	rampSteps []any
}

// stable is a number of connections the host carried, with the reading
// that showed it.
type stable struct {
	connections float64
	metrics     *metrics
}

// abortRequested is runState.abortRequested.
func (r *run) abortRequested() bool {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	return r.s.abortRequested
}

// pause waits for d (sleep(ms, abortSignal)). It fails with errAborted the
// moment an abort comes, and at once when the test was aborted before:
// the abort channel is closed then.
func (r *run) pause(ctx context.Context, d time.Duration) error {
	clock := r.s.opts.Clock
	reached, err := clock.SleepUntil(ctx, clock.Now().Add(d), r.abort)
	if err != nil {
		return err
	}
	if !reached {
		return errAborted
	}
	return nil
}

// loadTest is runLoadTest after its start: the baseline, the probe, the
// ramp-up, the stabilization and the result.
func (r *run) loadTest(ctx context.Context) (*branding.Object, error) {
	s := r.s
	s.opts.Warm()
	s.emit("phase", "baseline", "message", "Collecting baseline metrics…", "connections", float64(0), "rampSteps", []any{})
	if err := r.pause(ctx, baselineWait); err != nil {
		return nil, err
	}
	baseline := s.collectMetrics(ctx)
	s.emit("phase", "probe", "message", "Probing WebSocket endpoints…", "connections", float64(0), "baseline", baseline.object(), "rampSteps", []any{})

	urls, err := r.resolveTargets(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.phase = "ramp-up"
	s.mu.Unlock()
	message := "WebSocket unreachable — using capacity estimate…"
	if urls.resolved {
		message = "Ramping up connections…"
	}
	s.emit("phase", "ramp-up", "message", message, "connections", float64(0), "baseline", baseline.object(), "rampSteps", []any{}, "probeLog", urls.probeLog())

	var best, last stable
	if urls.resolved {
		for r.total() < maxConnections {
			if s.opts.Clock.Now().Sub(r.started) > maxTest {
				break
			}
			if r.abortRequested() {
				break
			}
			if err := r.addSignal(ctx, urls.signal); err != nil {
				return nil, err
			}
			if err := r.addRelayPairs(ctx, urls.relay); err != nil {
				return nil, err
			}
			if err := r.pause(ctx, settleWait); err != nil && err != errAborted {
				return nil, err
			}
			m := s.collectMetrics(ctx)
			total, signal, pairs, rate, attempts := r.counts()
			step := branding.NewObject()
			step.Set("ts", float64(m.ts))
			step.Set("connections", float64(total))
			step.Set("signalConnections", float64(signal))
			step.Set("relaySessions", float64(pairs))
			step.Set("cpu", m.cpu)
			step.Set("mem", m.memPercent)
			step.Set("disk", m.diskPercent)
			step.Set("errorRate", rate)
			r.rampSteps = append(r.rampSteps, step)
			s.emit("phase", "ramp-up", "message", fmt.Sprintf("Testing %d connections…", total), "connections", float64(total),
				"metrics", m.object(), "rampSteps", r.steps())

			if total == 0 && attempts >= 12 {
				break
			}
			if total > 0 && rate > errorRateAbort && attempts > 20 {
				break
			}
			if overThreshold(m, baseline) {
				last = stable{math.Max(0, float64(total-batchSignal-batchRelay*2)), m}
				break
			}
			best = stable{float64(total), m}
			if err := r.pause(ctx, rampInterval); err != nil {
				return nil, err
			}
		}
	}
	// An aborted test stores no result: the one before stays
	if r.abortRequested() {
		return nil, errAborted
	}

	s.mu.Lock()
	s.phase = "stabilize"
	s.mu.Unlock()
	s.emit("phase", "stabilize", "message", "Stabilizing…", "connections", float64(r.total()), "rampSteps", r.steps())

	stabilizeStart := s.opts.Clock.Now()
	final, finalMetrics := best.connections, best.metrics
	if finalMetrics == nil {
		finalMetrics = s.collectMetrics(ctx)
	}
	for s.opts.Clock.Now().Sub(stabilizeStart) < stabilizeTime {
		if r.abortRequested() {
			break
		}
		m := s.collectMetrics(ctx)
		if calm(m, baseline) && last.connections > 0 {
			final, finalMetrics = last.connections, last.metrics
			break
		}
		if !overThreshold(m, baseline) {
			final, finalMetrics = float64(r.total()), m
		}
		if err := r.pause(ctx, stabilizeInterval); err != nil && err != errAborted {
			return nil, err
		}
	}
	if r.abortRequested() {
		return nil, errAborted
	}

	mode := "estimated"
	if urls.resolved {
		mode = "loadtest"
	}
	if final <= 0 {
		snap := fromSnapshot(s.opts.Snapshot(), s.nowMillis())
		if estimated := estimateCapacity(baseline, snap); estimated > 0 {
			final, mode = estimated, "estimated"
		}
	} else if r.total() > 0 {
		mode = "loadtest"
	}

	tier := DetermineTier(final)
	_, signal, pairs, rate, _ := r.counts()
	now := s.opts.Clock.Now()
	stamp := now.UTC().Format("2006-01-02T15:04:05.000Z")
	finalObject := branding.NewObject()
	finalObject.Set("cpu", finalMetrics.cpu)
	finalObject.Set("mem", finalMetrics.memPercent)
	finalObject.Set("disk", finalMetrics.diskPercent)

	result := branding.NewObject()
	result.Set("valid", final > 0 && tier != nil)
	result.Set("mode", mode)
	result.Set("tier", tier)
	result.Set("maxConnections", final)
	result.Set("signalConnections", float64(signal))
	result.Set("relaySessions", float64(pairs))
	result.Set("threshold", float64(Threshold))
	result.Set("testedAt", stamp)
	result.Set("completedAt", stamp)
	result.Set("durationMs", float64(now.UnixMilli()-r.started.UnixMilli()))
	result.Set("aborted", false)
	result.Set("metricsHost", finalMetrics.hostname)
	result.Set("finalMetrics", finalObject)
	result.Set("baseline", baseline.object())
	result.Set("rampSteps", r.steps())
	result.Set("targets", urls.object())
	result.Set("probeLog", urls.probeLog())
	result.Set("errorRate", rate)
	result.Set("diskBaselineHigh", baseline.diskPercent >= 75)
	result.Set("wsResolved", urls.resolved)

	if err := s.opts.Settings.Set(ctx, ResultKey, branding.Stringify(result)); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.lastResult, s.resultLoaded = result, true
	s.phase = "done"
	s.mu.Unlock()
	s.emit("phase", "done", "message", "Benchmark complete", "result", result, "rampSteps", r.steps())
	return result, nil
}

// steps is a copy of the ramp steps so far.
func (r *run) steps() []any { return append([]any{}, r.rampSteps...) }

// total is the number of connections the test holds.
func (r *run) total() int {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	return r.s.pool.total()
}

// counts are the connections, the signal connections, the relay pairs,
// the share of failed connection attempts and the attempts.
func (r *run) counts() (total, signal, pairs int, rate float64, attempts int) {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	p := r.s.pool
	return p.total(), len(p.signal), p.relayPairs, p.errorRate(), p.attempts
}

// addSignal opens batchSignal connections to the signal server, each
// sending a health check (addSignalConnections). A failed connection
// counts as a failure. Only the end of ctx is an error.
func (r *run) addSignal(ctx context.Context, url string) error {
	if url == "" {
		return nil
	}
	for i := 0; i < batchSignal; i++ {
		if r.abortRequested() {
			return nil
		}
		r.s.mu.Lock()
		r.s.pool.attempts++
		r.s.mu.Unlock()
		conn, err := r.s.opts.Dialer.Dial(ctx, url, connectTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			r.s.mu.Lock()
			r.s.pool.failures++
			r.s.mu.Unlock()
			continue
		}
		_ = conn.Send(healthCheck())
		conn.Drain()
		r.s.mu.Lock()
		r.s.pool.signal = append(r.s.pool.signal, conn)
		r.s.mu.Unlock()
	}
	return nil
}

// addRelayPairs opens batchRelay pairs of connections to the relay, both
// of a pair asking for the same session (addRelayPairs). A pair whose
// second connection fails counts as one failure, and its first connection
// is closed. Only the end of ctx is an error.
func (r *run) addRelayPairs(ctx context.Context, url string) error {
	if url == "" {
		return nil
	}
	for i := 0; i < batchRelay; i++ {
		if r.abortRequested() {
			return nil
		}
		id := make([]byte, 8)
		if _, err := io.ReadFull(r.s.opts.Random, id); err != nil {
			return fmt.Errorf("relay session id: %w", err)
		}
		uuid := "bd-bench-" + hex.EncodeToString(id)
		r.s.mu.Lock()
		r.s.pool.attempts += 2
		r.s.mu.Unlock()
		first, err := r.s.opts.Dialer.Dial(ctx, url, connectTimeout)
		if err == nil {
			var second Conn
			second, err = r.s.opts.Dialer.Dial(ctx, url, connectTimeout)
			if err == nil {
				payload := requestRelay(uuid)
				_ = first.Send(payload)
				_ = second.Send(payload)
				first.Drain()
				second.Drain()
				r.s.mu.Lock()
				r.s.pool.relay = append(r.s.pool.relay, first, second)
				r.s.pool.relayPairs++
				r.s.mu.Unlock()
				continue
			}
			first.Close()
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r.s.mu.Lock()
		r.s.pool.failures++
		r.s.mu.Unlock()
	}
	return nil
}

// pool is the connections a test holds (ConnectionPool).
type pool struct {
	signal, relay      []Conn
	relayPairs         int
	attempts, failures int
}

func (p *pool) total() int {
	if p == nil {
		return 0
	}
	return len(p.signal) + len(p.relay)
}

func (p *pool) errorRate() float64 {
	if p == nil || p.attempts == 0 {
		return 0
	}
	return float64(p.failures) / float64(p.attempts)
}

// closeAll closes every connection and forgets them; the counts of
// attempts and failures stay.
func (p *pool) closeAll() {
	for _, c := range p.signal {
		c.Close()
	}
	for _, c := range p.relay {
		c.Close()
	}
	p.signal, p.relay, p.relayPairs = nil, nil, 0
}
