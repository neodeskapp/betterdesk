package registrations

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
)

// Notifications is listRegistrationNotifications(db, betterdeskApi).items:
// the pending LAN registrations and the devices waiting in the API
// server's enrollment queue, newest first (LAN entries before enrollment
// entries of the same time), each with the keys id, title, message, icon,
// link, read, created_at, kind, source, device_id, hostname, platform, ip
// and status. A source that fails counts as empty.
func (s *Service) Notifications(ctx context.Context) []*goapi.Object {
	var lan []*goapi.Object
	var pending *goapi.Object
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		rows, err := s.List(ctx, Filters{Status: jsbind.String("pending")})
		if err == nil {
			lan = rows
		}
	}()
	if s.api != nil {
		pending = s.api.GetEnrollmentPending(ctx)
	}
	wg.Wait()
	return s.notificationsOf(lan, pending)
}

// notificationsOf makes the list of the rows of pending LAN registrations
// and the API client's answer for the enrollment queue: the entries of
// both, newest first.
func (s *Service) notificationsOf(lan []*goapi.Object, pending *goapi.Object) []*goapi.Object {
	type entry struct {
		item *goapi.Object
		ms   float64
	}
	var entries []entry
	for _, row := range lan {
		if item, ms := s.lanNotification(row); item != nil {
			entries = append(entries, entry{item, ms})
		}
	}
	if list, ok := pending.Get("data").([]any); ok {
		for _, enrollment := range list {
			if item, ms := s.enrollmentNotification(enrollment); item != nil {
				entries = append(entries, entry{item, ms})
			}
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].ms > entries[j].ms })
	items := make([]*goapi.Object, len(entries))
	for i, e := range entries {
		items[i] = e.item
	}
	return items
}

// lanNotification is lanRegistrationToNotification for a row of the
// table, with the time of its created_at in milliseconds.
func (s *Service) lanNotification(row *goapi.Object) (*goapi.Object, float64) {
	if row.Get("id") == nil {
		return nil, 0
	}
	deviceID := jsString(orValue(row.Get("device_id"), ""))
	createdAt, ms := s.createdAt(orValue(row.Get("created_at"), row.Get("updated_at")))
	return goapi.NewObject(
		"id", "registration:lan:"+jsString(row.Get("id")),
		"title", orValue(row.Get("hostname"), deviceID),
		"message", message(deviceID, row.Get("platform"), row.Get("ip_address")),
		"icon", "how_to_reg",
		"link", "/registrations",
		"read", false,
		"created_at", createdAt,
		"kind", "registration",
		"source", "lan",
		"device_id", deviceID,
		"hostname", jsString(orValue(row.Get("hostname"), "")),
		"platform", jsString(orValue(row.Get("platform"), "")),
		"ip", jsString(orValue(row.Get("ip_address"), "")),
		"status", "pending",
	), ms
}

// enrollmentNotification is enrollmentToNotification for one device of
// the enrollment queue as the API server listed it; nil for an entry
// without a device id.
func (s *Service) enrollmentNotification(v any) (*goapi.Object, float64) {
	enrollment, _ := v.(*goapi.Object)
	if enrollment == nil || !goapi.Truthy(enrollment.Get("device_id")) {
		return nil, 0
	}
	deviceID := jsString(enrollment.Get("device_id"))
	createdAt, ms := s.createdAt(enrollment.Get("created_at"))
	return goapi.NewObject(
		"id", "registration:enrollment:"+deviceID,
		"title", orValue(enrollment.Get("hostname"), deviceID),
		"message", message(deviceID, enrollment.Get("platform"), enrollment.Get("ip")),
		"icon", "how_to_reg",
		"link", "/registrations",
		"read", false,
		"created_at", createdAt,
		"kind", "registration",
		"source", "enrollment",
		"device_id", deviceID,
		"hostname", jsString(orValue(enrollment.Get("hostname"), "")),
		"platform", jsString(orValue(enrollment.Get("platform"), "")),
		"ip", jsString(orValue(enrollment.Get("ip"), "")),
		"status", "pending",
	), ms
}

// createdAt is toCreatedAt(value): new Date(value || Date.now()) as ISO
// text, the current time for a value that is no date. It also returns the
// time in milliseconds, which the list is sorted by.
func (s *Service) createdAt(value any) (string, float64) {
	ms, ok := dateValue(value, s.loc)
	if !goapi.Truthy(value) || !ok {
		ms = float64(s.now().UnixMilli())
	}
	return isoString(ms), ms
}

// message is [deviceId, platform, ip].filter(Boolean).join(' · ').
func message(deviceID string, platform, ip any) string {
	var parts []string
	for _, v := range []any{deviceID, platform, ip} {
		if goapi.Truthy(v) {
			parts = append(parts, jsString(v))
		}
	}
	return strings.Join(parts, " · ")
}
