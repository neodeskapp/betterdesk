// Package jobs runs the console's background work: timers and loops that
// start with the console and stop with it.
//
// A job is a function that runs until its context ends. The registry
// starts every job in its own goroutine and Stop waits until all of them
// have returned, so nothing keeps running after the console stopped.
package jobs

import (
	"context"
	"sync"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/logging"
)

// StopTimeout is how long Stop waits for the jobs to return.
const StopTimeout = 10 * time.Second

type job struct {
	name string
	run  func(ctx context.Context)
}

// Registry holds the jobs of one console server.
type Registry struct {
	mu      sync.Mutex
	jobs    []job
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	stopped bool
}

// New returns an empty registry.
func New() *Registry { return &Registry{} }

// Add registers a job. run is called once and has to return when its
// context ends. A job added after Start starts at once; one added after
// Stop never runs.
func (r *Registry) Add(name string, run func(ctx context.Context)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	j := job{name: name, run: run}
	r.jobs = append(r.jobs, j)
	if r.ctx != nil && !r.stopped {
		r.launch(j)
	}
}

// Every registers a job that calls fn each time interval has passed,
// first after one interval. A call that is still running when the next
// one is due delays it.
func (r *Registry) Every(name string, interval time.Duration, fn func(ctx context.Context)) {
	r.Add(name, func(ctx context.Context) {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				fn(ctx)
			}
		}
	})
}

// Names lists the registered jobs in the order they were added.
func (r *Registry) Names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]string, len(r.jobs))
	for i, j := range r.jobs {
		names[i] = j.name
	}
	return names
}

// Start runs every registered job. The jobs end when ctx ends or Stop is
// called. A second Start does nothing.
func (r *Registry) Start(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ctx != nil || r.stopped {
		return
	}
	r.ctx, r.cancel = context.WithCancel(ctx)
	for _, j := range r.jobs {
		r.launch(j)
	}
}

// launch starts one job. The caller holds the lock.
func (r *Registry) launch(j job) {
	ctx := r.ctx
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer func() {
			if p := recover(); p != nil {
				logging.Logf("console", "ERROR: job %s stopped: %v", j.name, p)
			}
		}()
		j.run(ctx)
	}()
}

// Stop ends the jobs and waits until each has returned, at most
// StopTimeout. It reports whether all of them did.
func (r *Registry) Stop() bool {
	r.mu.Lock()
	r.stopped = true
	cancel := r.cancel
	r.mu.Unlock()
	if cancel == nil {
		return true
	}
	cancel()
	done := make(chan struct{})
	go func() {
		r.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(StopTimeout):
		logging.Logf("console", "WARN: background jobs still running %s after stop", StopTimeout)
		return false
	}
}
