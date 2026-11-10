package trends

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

// collection is what the reports page gets: every time is in milliseconds
// since 1970.
type collection struct {
	Days           int            `json:"days"`
	Since          int64          `json:"since"`
	Sessions       []sessionSpan  `json:"sessions"`
	Help           []helpActivity `json:"help"`
	DevicesChecked int            `json:"devicesChecked"`
	DevicesTotal   int            `json:"devicesTotal"`
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

// collect reads the sessions of every device and the help requests, and
// keeps what started (for a help request: was made or changed) in the days
// before now. A session two devices both report counts once.
func collect(ctx context.Context, api source, days int, now int64) (*collection, error) {
	since := now - int64(days)*dayMillis

	peers := api.GetAllPeers(ctx, false)
	var ids []string
	for _, p := range peers {
		if p == nil {
			return nil, errShape
		}
		id := p.Get("id")
		if !goapi.Truthy(id) {
			id = p.Get("peer_id")
		}
		if goapi.Truthy(id) && len(ids) < maxPeers {
			ids = append(ids, jsreq.String(id))
		}
	}

	lists := sessionLists(ctx, api, ids)
	seen := map[string]bool{}
	sessions := []sessionSpan{}
	for _, list := range lists {
		rows, err := iterable(list)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if row == nil {
				return nil, errShape
			}
			s, _ := row.(*goapi.Object)
			start, ok := toTime(s.Get("started_at"))
			if !ok || start < since {
				continue
			}
			key := sessionKey(s)
			if seen[key] {
				continue
			}
			seen[key] = true
			span := sessionSpan{Start: start}
			if end, ok := toTime(s.Get("ended_at")); ok {
				span.End = &end
			}
			sessions = append(sessions, span)
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
	}, nil
}

// sessionLists asks for the sessions of each device, a few devices at a
// time, and returns what the answers hold as their list, in the order of
// ids. The answer of the API client is {success, data: {sessions}} or, for
// a refusal, whatever the API server sent.
func sessionLists(ctx context.Context, api source, ids []string) []any {
	lists := make([]any, len(ids))
	next := make(chan int)
	var wg sync.WaitGroup
	for i := 0; i < min(concurrency, len(ids)); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range next {
				lists[idx] = sessionList(api.GetPeerConnectionSessions(ctx, ids[idx], float64(perPeerLimit)))
			}
		}()
	}
	for idx := range ids {
		next <- idx
	}
	close(next)
	wg.Wait()
	return lists
}

// sessionList is the list inside one answer: data or sessions when that is
// a list, else its "sessions".
func sessionList(r *goapi.Object) any {
	rows := r.Get("data")
	if !goapi.Truthy(rows) {
		rows = r.Get("sessions")
	}
	if !goapi.Truthy(rows) {
		return nil
	}
	if list, ok := rows.([]any); ok {
		return list
	}
	inner, _ := rows.(*goapi.Object)
	return inner.Get("sessions")
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

// sessionKey names a session for the check against doubles: its id, or its
// two devices and its start when it has none.
func sessionKey(s *goapi.Object) string {
	if id := s.Get("id"); id != nil {
		return "id:" + jsreq.String(id)
	}
	parts := make([]string, 3)
	for i, name := range []string{"host_id", "peer_id", "started_at"} {
		if v := s.Get(name); v != nil {
			parts[i] = jsreq.String(v)
		}
	}
	return strings.Join(parts, "|")
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
