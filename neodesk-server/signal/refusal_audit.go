package signal

import (
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/audit"
)

// Refusals of unauthenticated requests are audited once per address, action
// and reason in a window; the ones in between are counted and the next
// recorded event carries their number. A budget per window bounds what
// packets with forged source addresses can write into the audit ring the
// API reads.
const (
	refusalAuditWindow     = time.Minute
	refusalAuditBudget     = 100
	maxTrackedRefusalHosts = 10000
)

type refusalWindow struct {
	start      time.Time
	suppressed int
}

// refusalCounter decides which refusals are written to the audit log.
type refusalCounter struct {
	mu           sync.Mutex
	window       time.Duration
	maxPerWindow int
	windowStart  time.Time
	recorded     int
	byKey        map[string]*refusalWindow
}

func newRefusalCounter(window time.Duration, maxPerWindow int) *refusalCounter {
	return &refusalCounter{window: window, maxPerWindow: maxPerWindow, byKey: make(map[string]*refusalWindow)}
}

// note counts a refusal under key and reports whether to record it, with
// the number of refusals under key left out since its last recorded one.
func (c *refusalCounter) note(key string, now time.Time) (record bool, suppressed int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if now.Sub(c.windowStart) >= c.window {
		c.windowStart, c.recorded = now, 0
	}
	e := c.byKey[key]
	if e != nil && now.Sub(e.start) < c.window {
		e.suppressed++
		return false, 0
	}
	if c.recorded >= c.maxPerWindow {
		if e != nil {
			e.suppressed++
		}
		return false, 0
	}
	c.recorded++
	if e == nil {
		if len(c.byKey) >= maxTrackedRefusalHosts {
			c.prune(now)
		}
		if len(c.byKey) < maxTrackedRefusalHosts {
			c.byKey[key] = &refusalWindow{start: now}
		}
		return true, 0
	}
	suppressed = e.suppressed
	e.start, e.suppressed = now, 0
	return true, suppressed
}

// prune drops keys whose window has passed.
func (c *refusalCounter) prune(now time.Time) {
	for key, e := range c.byKey {
		if now.Sub(e.start) >= c.window {
			delete(c.byKey, key)
		}
	}
}

// noteRefusal counts a refusal of action from host for details["reason"].
func (s *Server) noteRefusal(action audit.Action, host string, details map[string]string) (record bool, suppressed int) {
	return s.refusals.note(string(action)+"|"+host+"|"+details["reason"], time.Now())
}

// auditRefusal writes a refusal of an unauthenticated request to the audit
// log, once per address, action and reason in a window.
func (s *Server) auditRefusal(action audit.Action, host, target string, details map[string]string) {
	if s.auditLog == nil {
		return
	}
	record, suppressed := s.noteRefusal(action, host, details)
	if !record {
		return
	}
	s.auditLog.Log(action, host, target, withSuppressed(details, suppressed))
}

func withSuppressed(details map[string]string, suppressed int) map[string]string {
	if suppressed > 0 {
		details["suppressed_since_last"] = strconv.Itoa(suppressed)
	}
	return details
}

// A device that waits for approval, or that the enrollment mode refuses,
// tries again with every heartbeat. Its log lines are written once per
// device in a window; the next line says how many were left out.
const (
	enrollmentLogWindow = 10 * time.Minute
	enrollmentLogBudget = 200
)

// enrollmentLogf writes a log line about the enrollment of device id,
// at most one per device in enrollmentLogWindow.
func (s *Server) enrollmentLogf(id, format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	if s.enrollmentLines == nil {
		log.Printf("[signal] %s", line)
		return
	}
	record, suppressed := s.enrollmentLines.note(id, time.Now())
	if !record {
		return
	}
	if suppressed > 0 {
		log.Printf("[signal] %s (%d more lines about %s left out since the last)", line, suppressed, id)
		return
	}
	log.Printf("[signal] %s", line)
}
