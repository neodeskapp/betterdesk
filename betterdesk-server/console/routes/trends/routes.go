// Package trends answers GET /api/reports/trends, the data behind the
// charts of the reports page: the remote sessions the API server saw and
// the help requests of the last days, as times in milliseconds that the
// browser sorts into its own days.
//
// The sessions of every device come from one query of the signal
// server's table. The result is kept for five minutes, for every account;
// an account limited to some devices sees the sessions and counts of
// those.
package trends

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

const (
	cacheFor  = 5 * time.Minute
	helpLimit = 5000
	dayMillis = 86400000

	defaultDays = 14
	minDays     = 1
	maxDays     = 90
)

// source is the part of the API client the collection reads from.
type source interface {
	GetAllPeers(ctx context.Context, includeDeleted bool) []*goapi.Object
	ListHelpRequests(ctx context.Context, filter any) *goapi.Object
}

// scopeFunc is the device scope of the account of a request, nil when
// it sees every device.
type scopeFunc func(r *http.Request) (*access.DeviceScope, error)

type handlers struct {
	api      source
	sessions sessionSource
	scope    scopeFunc
	log      *kit.Logger
	now      func() time.Time

	mu    sync.Mutex
	cache *cached
	// running holds the collections in progress by their number of days
	running map[int]*run
}

// run is a collection in progress. done closes when entry or err is set.
type run struct {
	done  chan struct{}
	entry *cached
	err   error
}

// cached is the last collection: for how many days, when it was started,
// and what it found.
type cached struct {
	days int
	at   int64
	data *collection
}

// Register adds the group's route.
func Register(d *kit.Deps) {
	h := &handlers{api: d.GoAPI, sessions: sessionsSince(d.Store), scope: scopeOf(d), log: d.Log.Child("TRENDS"), now: time.Now}
	d.Router.Get("/api/reports/trends", h.trends, d.RequireAuth, d.RequirePermission("audit.view"))
}

// scopeOf reads the scope over every device of the device layer, which
// knows their folders and tags, and only for an account that has a limit.
func scopeOf(d *kit.Deps) scopeFunc {
	return func(r *http.Request) (*access.DeviceScope, error) {
		ctx, user := r.Context(), kit.User(r)
		scope, err := d.DeviceGroups.DeviceScopeForUser(ctx, user, nil)
		if err != nil || scope == nil {
			return scope, err
		}
		return d.DeviceGroups.DeviceScopeForUser(ctx, user, d.Backend.GetAllDevices(ctx, goapi.DeviceFilters{}))
	}
}

type answer struct {
	Success     bool        `json:"success"`
	Data        *collection `json:"data"`
	GeneratedAt int64       `json:"generatedAt"`
}

// trends answers GET /api/reports/trends?days=14. days is cut to 1 to 90;
// anything that is no number, and 0, means 14.
func (h *handlers) trends(w http.ResponseWriter, r *http.Request) {
	days := daysOf(jsreq.Query(r, "days"))
	scope, err := h.scope(r)
	if err != nil {
		h.log.Error("Trends device scope error:", err)
		kit.Fail(w, r, http.StatusInternalServerError, kit.T(r, "errors.server_error", nil))
		return
	}
	entry, err := h.collected(r.Context(), days)
	if err != nil {
		h.log.Error("Trends error:", err)
		kit.Fail(w, r, http.StatusBadGateway, kit.T(r, "errors.server_error", nil))
		return
	}
	kit.JSON(w, r, http.StatusOK, answer{Success: true, Data: entry.data.visibleTo(scope), GeneratedAt: entry.at})
}

// daysOf is Math.min(90, Math.max(1, parseInt(days, 10) || 14)).
func daysOf(v any) int {
	n, ok := jsreq.ParseInt(v, 10)
	if !ok || n == 0 {
		n = defaultDays
	}
	if n < minDays {
		return minDays
	}
	if n > maxDays {
		return maxDays
	}
	return int(n)
}

// collected returns the kept collection when it is for the same number of
// days and no older than five minutes, and collects anew otherwise. A
// request that comes while a collection for the same number of days runs
// waits for that one, and gets its result or its failure. A collection
// goes on when the request that started it ends.
func (h *handlers) collected(ctx context.Context, days int) (*cached, error) {
	at := h.now().UnixMilli()
	h.mu.Lock()
	if kept := h.cache; kept != nil && kept.days == days && at-kept.at <= cacheFor.Milliseconds() {
		h.mu.Unlock()
		return kept, nil
	}
	current, joined := h.running[days]
	if !joined {
		current = &run{done: make(chan struct{})}
		if h.running == nil {
			h.running = map[int]*run{}
		}
		h.running[days] = current
	}
	h.mu.Unlock()

	if !joined {
		data, err := collect(context.WithoutCancel(ctx), h.api, h.sessions, days, at)
		h.mu.Lock()
		delete(h.running, days)
		if err == nil {
			current.entry = &cached{days: days, at: at, data: data}
			h.cache = current.entry
		}
		current.err = err
		h.mu.Unlock()
		close(current.done)
	}
	select {
	case <-current.done:
		return current.entry, current.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
