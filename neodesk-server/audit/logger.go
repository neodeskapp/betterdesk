// Package audit provides structured audit logging for admin-visible operations.
// Each event is recorded with a timestamp, actor, action type, and details.
// Events are kept in the database for RetentionDays (UseStore), or in
// memory (ring buffer) without one, and optionally written to a file.
package audit

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Action represents the type of auditable operation.
type Action string

const (
	ActionPeerBanned            Action = "peer_banned"
	ActionPeerUnbanned          Action = "peer_unbanned"
	ActionPeerDeleted           Action = "peer_deleted"
	ActionPeerRevoked           Action = "peer_revoked"
	ActionPeerRestored          Action = "peer_restored"
	ActionPeerUpdated           Action = "peer_updated"
	ActionPeerIDChanged         Action = "peer_id_changed"
	ActionPeerTagsUpdated       Action = "peer_tags_updated"
	ActionConnectionModeChanged Action = "connection_mode_changed"
	// ActionEnrollmentPending is logged when an unknown device is placed into
	// the pending enrollment queue (managed mode) awaiting operator approval.
	ActionEnrollmentPending Action = "enrollment_pending"
	// ActionPeerRegistrationRejected is logged when a registration attempt is
	// rejected by the signal server (soft-deleted, banned, blocklisted,
	// enrollment policy, renamed ID). Used for forensic visibility of
	// attempted identity replays (GHSA-3v82-3gf8-fxx8). The "reason" field
	// in details carries the specific cause.
	ActionPeerRegistrationRejected Action = "peer_registration_rejected"
	// ActionConnectionDenied is logged when PunchHole/RequestRelay is refused
	// because the initiator is not an authorized/enrolled peer.
	ActionConnectionDenied Action = "connection_denied"
	// ActionConnectionAllowed is logged when PunchHole/RequestRelay passes
	// initiator authorization, including operator-only session checks.
	ActionConnectionAllowed      Action = "connection_allowed"
	ActionBlocklistAdd           Action = "blocklist_add"
	ActionBlocklistRemove        Action = "blocklist_remove"
	ActionConfigChanged          Action = "config_changed"
	ActionAdminLogin             Action = "admin_login"
	ActionServerStart            Action = "server_start"
	ActionServerStop             Action = "server_stop"
	ActionBlocklistReload        Action = "blocklist_reload"
	ActionStatusTransition       Action = "status_transition"
	ActionAuthLogin              Action = "auth_login"
	ActionAuthLoginFailed        Action = "auth_login_failed"
	ActionUserCreated            Action = "user_created"
	ActionUserUpdated            Action = "user_updated"
	ActionUserDeleted            Action = "user_deleted"
	ActionAPIKeyCreated          Action = "apikey_created"
	ActionAPIKeyRevoked          Action = "apikey_revoked"
	ActionSysinfoUpdated         Action = "sysinfo_updated"
	ActionSysinfoError           Action = "sysinfo_error"
	ActionTelemetrySnapshotError Action = "telemetry_snapshot_error"
	ActionTelemetryCommandResult Action = "telemetry_command_result"
	ActionTelemetryCommandQueued Action = "telemetry_command_queued"
	// Help requests and chat (raised by agent devices via CDAP).
	ActionHelpRequestCreated Action = "help_request_created"
	ActionHelpRequestUpdated Action = "help_request_updated"
	ActionChatMessage        Action = "chat_message"
	// Org peer credential vault — never include password material in Details.
	ActionOrgPeerCredentialSet   Action = "org_peer_credential_set"
	ActionOrgPeerCredentialClear Action = "org_peer_credential_clear"
	ActionOrgPeerCredentialFetch Action = "org_peer_credential_fetch"
	ActionGuestSessionOpened     Action = "guest_session_opened"
	ActionGuestSessionClosed     Action = "guest_session_closed"
)

// Event represents a single audit log entry.
type Event struct {
	Timestamp time.Time         `json:"timestamp"`
	Action    Action            `json:"action"`
	Actor     string            `json:"actor"` // "api", "admin", "system", IP address, etc.
	Target    string            `json:"target,omitempty"`
	Details   map[string]string `json:"details,omitempty"`
}

// Logger is the audit event logger.
// With a store it queues events for a writer that keeps them in the
// database; without one it maintains a ring buffer of recent events. It
// optionally writes to a file.
type Logger struct {
	mu      sync.RWMutex
	events  []Event
	maxSize int // ring buffer capacity
	cursor  int // next write position
	total   int64
	// logged counts every event Log took since start, for the metrics
	logged   atomic.Int64
	file     *os.File
	filePath string

	// qmu guards the store and the queue: Log and the readers send under
	// the read lock, Close closes the queue under the write lock.
	qmu        sync.RWMutex
	store      Store
	queue      chan queued
	closed     bool
	writerDone chan struct{}
}

const defaultMaxEvents = 10000
const maxRecentLimit = 500

// NewLogger creates a new audit logger.
// If filePath is non-empty, events are also appended to that file as JSON lines.
func NewLogger(filePath string) *Logger {
	l := &Logger{
		events:   make([]Event, defaultMaxEvents),
		maxSize:  defaultMaxEvents,
		filePath: filePath,
	}

	if filePath != "" {
		// Only the server's user may read the file: it holds addresses,
		// user names and device ids. An existing file is narrowed too.
		f, err := os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			log.Printf("[audit] Failed to open audit log file %s: %v", filePath, err)
		} else {
			if err := f.Chmod(0o600); err != nil {
				log.Printf("[audit] Failed to restrict audit log file %s: %v", filePath, err)
			}
			l.file = f
		}
	}

	return l
}

// Log records an audit event.
func (l *Logger) Log(action Action, actor, target string, details map[string]string) {
	event := Event{
		Timestamp: time.Now(),
		Action:    action,
		Actor:     actor,
		Target:    target,
		Details:   details,
	}

	l.logged.Add(1)
	stored := l.enqueue(event)

	l.mu.Lock()
	if !stored {
		l.events[l.cursor] = event
		l.cursor = (l.cursor + 1) % l.maxSize
		l.total++
	}

	// Write to file if configured (inside lock to prevent interleaved writes)
	if l.file != nil {
		data, err := json.Marshal(event)
		if err == nil {
			l.file.Write(append(data, '\n'))
		}
	}
	l.mu.Unlock()

	log.Printf("[audit] %s actor=%s target=%s %v", action, actor, target, redactDetailsForLog(details))
}

func redactDetailsForLog(details map[string]string) map[string]string {
	if len(details) == 0 {
		return nil
	}
	out := make(map[string]string, len(details))
	for k, v := range details {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "password") || strings.Contains(lk, "secret") ||
			strings.Contains(lk, "token") || strings.Contains(lk, "api_key") || lk == "key" {
			out[k] = "***"
		} else {
			out[k] = v
		}
	}
	return out
}

func clampRecentLimit(n int) int {
	if n <= 0 {
		return 0
	}
	if n > maxRecentLimit {
		return maxRecentLimit
	}
	return n
}

// Page returns the events newest first, those of action when it is not
// "", skipping offset and at most limit (up to 500), and how many there
// are of them: the stored ones with a store, else the ones in memory (all
// logged since start without an action).
func (l *Logger) Page(action Action, limit, offset int) ([]Event, int64, error) {
	limit = clampRecentLimit(limit)
	if offset < 0 {
		offset = 0
	}
	if store := l.storeOf(); store != nil {
		return storedPage(store, action, limit, offset)
	}

	l.mu.RLock()
	defer l.mu.RUnlock()
	if action == "" && limit == 0 {
		return nil, l.total, nil
	}
	count := int(l.total)
	if count > l.maxSize {
		count = l.maxSize
	}
	var events []Event
	matching := int64(0)
	for i := 0; i < count; i++ {
		e := l.events[(l.cursor-1-i+l.maxSize)%l.maxSize]
		if action != "" && e.Action != action {
			continue
		}
		matching++
		if matching > int64(offset) && len(events) < limit {
			events = append(events, e)
		}
	}
	if action == "" {
		matching = l.total
	}
	return events, matching, nil
}

// Recent returns the most recent n events (newest first). A store that
// fails gives none, with a log line.
func (l *Logger) Recent(n int) []Event {
	events, _, err := l.Page("", n, 0)
	if err != nil {
		log.Printf("[audit] reading the server events: %v", err)
	}
	return events
}

// RecentByAction returns the most recent n events filtered by action type.
func (l *Logger) RecentByAction(action Action, n int) []Event {
	events, _, err := l.Page(action, n, 0)
	if err != nil {
		log.Printf("[audit] reading the server events: %v", err)
	}
	if events == nil {
		events = []Event{}
	}
	return events
}

// Total returns the number of events: the stored ones with a store, else
// the ones logged since start.
func (l *Logger) Total() int64 {
	_, total, err := l.Page("", 0, 0)
	if err != nil {
		log.Printf("[audit] counting the server events: %v", err)
	}
	return total
}

// Logged returns the number of events logged since start, whether they
// were stored or not. It only grows, unlike Total, which the retention
// lowers.
func (l *Logger) Logged() int64 {
	return l.logged.Load()
}

// Close stores what is queued, stops the writer and closes the audit log
// file.
func (l *Logger) Close() error {
	l.stopWriter()
	if l.file != nil {
		return l.file.Close()
	}
	return nil
}

// String returns a human-readable summary.
func (l *Logger) String() string {
	return fmt.Sprintf("AuditLogger(total=%d, file=%s)", l.Total(), l.filePath)
}
