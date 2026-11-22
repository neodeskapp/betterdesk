package trends

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

// collection is what the reports page gets: every time is in milliseconds
// since 1970.
//
// One collection is kept for every account. It notes on which device each
// session ran, which devices were read and which were listed, so that
// visibleTo can cut it to the devices of an account.
type collection struct {
	Days           int            `json:"days"`
	Since          int64          `json:"since"`
	Sessions       []sessionSpan  `json:"sessions"`
	Help           []helpActivity `json:"help"`
	DevicesChecked int            `json:"devicesChecked"`
	DevicesTotal   int            `json:"devicesTotal"`

	// reporters holds, for each session, the device it ran on
	reporters [][]string
	// checked are the devices whose sessions were read
	checked []string
	// listed are the ids of the API server's devices; a device without
	// an id is listed as ""
	listed []string
}

// visibleTo is the part of the collection an account with the device
// scope sees: the sessions a device of the scope reported, and the
// devices of the scope among those read and listed. A nil scope sees all
// of it.
func (c *collection) visibleTo(scope *access.DeviceScope) *collection {
	if scope == nil {
		return c
	}
	out := &collection{Days: c.Days, Since: c.Since, Sessions: []sessionSpan{}, Help: c.Help}
	for i, span := range c.Sessions {
		if slices.ContainsFunc(c.reporters[i], scope.Has) {
			out.Sessions = append(out.Sessions, span)
		}
	}
	for _, id := range c.checked {
		if scope.Has(id) {
			out.DevicesChecked++
		}
	}
	for _, id := range c.listed {
		if id != "" && scope.Has(id) {
			out.DevicesTotal++
		}
	}
	return out
}

// sessionSpan is one remote session. End is null while it runs, and when
// its end cannot be read as a time.
type sessionSpan struct {
	Start int64  `json:"start"`
	End   *int64 `json:"end"`
}

// helpActivity is one help request that was made or changed in the
// period. A time that cannot be read is null.
type helpActivity struct {
	Created *int64 `json:"created"`
	Updated *int64 `json:"updated"`
	Status  string `json:"status"`
}

// errShape is an answer of the API client that the collection cannot walk:
// a missing entry where a device, a session or a help request should be,
// or a single value where a list should be.
var errShape = errors.New("the API server's answer has an entry or a list of the wrong kind")

// collect reads the sessions of every device the API server lists and the
// help requests, and keeps what started (for a help request: was made or
// changed) in the days before now. A help request changes only when its
// status does, so a resolved one was changed when it was resolved.
func collect(ctx context.Context, api source, stored sessionSource, days int, now int64) (*collection, error) {
	since := now - int64(days)*dayMillis

	peers := api.GetAllPeers(ctx, false)
	var ids []string
	known := map[string]bool{}
	listed := make([]string, len(peers))
	for i, p := range peers {
		if p == nil {
			return nil, errShape
		}
		id := p.Get("id")
		if !goapi.Truthy(id) {
			id = p.Get("peer_id")
		}
		if goapi.Truthy(id) {
			listed[i] = jsreq.String(id)
			ids = append(ids, listed[i])
			known[listed[i]] = true
		}
	}

	// The sessions of the listed devices, device by device in the order of
	// the list, each device's newest first
	found, err := stored(ctx, since)
	if err != nil {
		return nil, err
	}
	byHost := map[string][]storedSession{}
	for _, s := range found {
		if known[s.HostID] && s.Start >= since {
			byHost[s.HostID] = append(byHost[s.HostID], s)
		}
	}
	sessions := []sessionSpan{}
	var reporters [][]string
	for _, id := range ids {
		list := byHost[id]
		delete(byHost, id)
		slices.SortStableFunc(list, func(a, b storedSession) int {
			if a.Start != b.Start {
				return cmp.Compare(b.Start, a.Start)
			}
			return cmp.Compare(b.ID, a.ID)
		})
		for _, s := range list {
			sessions = append(sessions, sessionSpan{Start: s.Start, End: s.End})
			reporters = append(reporters, []string{id})
		}
	}

	help := []helpActivity{}
	rows, err := iterable(api.ListHelpRequests(ctx, goapi.NewObject("limit", float64(helpLimit))).Get("data"))
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row == nil {
			return nil, errShape
		}
		r, _ := row.(*goapi.Object)
		created, hasCreated := toTime(r.Get("created_at"))
		updated, hasUpdated := toTime(r.Get("updated_at"))
		if !(hasCreated && created >= since) && !(hasUpdated && updated >= since) {
			continue
		}
		entry := helpActivity{Status: jsreq.StringOr(r.Get("status"), "")}
		if hasCreated {
			entry.Created = &created
		}
		if hasUpdated {
			entry.Updated = &updated
		}
		help = append(help, entry)
	}

	return &collection{
		Days: days, Since: since, Sessions: sessions, Help: help,
		DevicesChecked: len(ids), DevicesTotal: len(peers),
		reporters: reporters, checked: ids, listed: listed,
	}, nil
}

// iterable returns the entries a JavaScript for-of loop walks over "v ||
// []": the elements of a list, nothing for an empty value, and nothing a
// row can be read from for text. Other values cannot be walked.
func iterable(v any) ([]any, error) {
	if !goapi.Truthy(v) {
		return nil, nil
	}
	switch t := v.(type) {
	case []any:
		return t, nil
	case string:
		return nil, nil
	}
	return nil, errShape
}

// toTime reads a time of the API server as milliseconds. Text without a
// time zone is a time of the database, which is UTC with a space between
// date and time.
func toTime(v any) (int64, bool) {
	if !goapi.Truthy(v) {
		return 0, false
	}
	text := jsreq.String(v)
	if !zoned(text) {
		text = strings.Replace(text, " ", "T", 1) + "Z"
	}
	return jsreq.ParseDate(text, time.Local)
}

// zoned reports whether text has a "z" or "Z" anywhere or ends in an
// offset like +01:00 or -0100.
func zoned(text string) bool {
	if strings.ContainsAny(text, "zZ") {
		return true
	}
	rest := text
	digits := func(n int) bool {
		if len(rest) < n {
			return false
		}
		for _, c := range []byte(rest[len(rest)-n:]) {
			if c < '0' || c > '9' {
				return false
			}
		}
		rest = rest[:len(rest)-n]
		return true
	}
	if !digits(2) {
		return false
	}
	rest = strings.TrimSuffix(rest, ":")
	if !digits(2) {
		return false
	}
	return strings.HasSuffix(rest, "+") || strings.HasSuffix(rest, "-")
}
