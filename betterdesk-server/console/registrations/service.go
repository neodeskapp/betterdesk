// Package registrations keeps the registration requests devices send
// after LAN discovery (the pending_registrations table), and builds the
// registration entries of the notification list from them and from the
// API server's enrollment queue (services/registrationNotifications.js).
//
// Rows come back as the old console's database drivers handed them to
// JSON: the columns in table order, numbers as numbers, times as the text
// SQLite stored or as an ISO string with milliseconds on PostgreSQL.
package registrations

import (
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/store"
)

// Service reads and writes registration requests. It is safe for
// concurrent use.
type Service struct {
	db  *store.DB
	api *goapi.Client
	now func() time.Time
	loc *time.Location
}

// New returns a Service on the console's database and the API server's
// client.
func New(db *store.DB, api *goapi.Client) *Service {
	return &Service{db: db, api: api, now: time.Now, loc: time.Local}
}

// WithClock returns a copy of the Service that reads the time from now and
// takes a time written without a zone as a time in loc, as the old
// console's process did with its own zone. For tests.
func (s *Service) WithClock(now func() time.Time, loc *time.Location) *Service {
	c := *s
	c.now, c.loc = now, loc
	return &c
}
