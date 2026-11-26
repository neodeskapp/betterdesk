package auditlog

import (
	"context"
	"errors"
	"fmt"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// Log writes the audit_log table.
type Log struct {
	db *store.DB
}

// New returns the audit log of a console database.
func New(db *store.DB) *Log {
	return &Log{db: db}
}

// Entry is one audit entry with every nullable column under the caller's
// control: a nil field is stored as NULL.
type Entry struct {
	// UserID is the account that acted. A pointer to 0 stores 0, which
	// PostgreSQL refuses because no user has that id.
	UserID *int64
	// Action names what happened ("login", "device_deleted"). It is stored
	// as given.
	Action string
	// Details is free text. It is stored after RedactDetails.
	Details *string
	// IP is the client address as the route resolved it. It is stored as
	// given.
	IP *string
	// TargetUserID is the account the entry is about, when it is about
	// one: whose password was reset, who was disabled. nil stores NULL.
	TargetUserID *int64
}

// Write stores an entry for the common call: userID 0 stands for no account
// and is stored as NULL, details (after RedactDetails) and ip are stored as
// given, also when they are empty.
func (l *Log) Write(ctx context.Context, userID int64, action, details, ip string) error {
	e := Entry{Action: action, Details: &details, IP: &ip}
	if userID != 0 {
		e.UserID = &userID
	}
	return l.WriteEntry(ctx, e)
}

// WriteEntry stores an entry. The database sets id and created_at.
//
// The error says which action could not be written and never holds the
// details. Whether a failed entry fails the request is the caller's choice.
func (l *Log) WriteEntry(ctx context.Context, e Entry) error {
	if l == nil || l.db == nil {
		return fmt.Errorf("write audit entry %q: %w", e.Action, errNoDatabase)
	}
	var userID, details, ip, target any
	if e.UserID != nil {
		userID = *e.UserID
	}
	if e.TargetUserID != nil {
		target = *e.TargetUserID
	}
	if e.Details != nil {
		details = RedactDetails(*e.Details)
	}
	if e.IP != nil {
		ip = toUTF8(*e.IP)
	}
	var err error
	if target == nil {
		_, err = l.db.Exec(ctx, `INSERT INTO audit_log (user_id, action, details, ip_address) VALUES (?, ?, ?, ?)`,
			userID, toUTF8(e.Action), details, ip)
	} else {
		_, err = l.db.Exec(ctx, `INSERT INTO audit_log (user_id, action, details, ip_address, target_user_id) VALUES (?, ?, ?, ?, ?)`,
			userID, toUTF8(e.Action), details, ip, target)
	}
	if err != nil {
		return fmt.Errorf("write audit entry %q: %w", e.Action, err)
	}
	return nil
}

// WriteAbout stores an entry like Write that is about the account
// target: the account that acted is userID (0 for none), the account the
// action changed or looked at is target.
func (l *Log) WriteAbout(ctx context.Context, userID, target int64, action, details, ip string) error {
	e := Entry{Action: action, Details: &details, IP: &ip, TargetUserID: &target}
	if userID != 0 {
		e.UserID = &userID
	}
	return l.WriteEntry(ctx, e)
}

var errNoDatabase = errors.New("the audit log has no database")
