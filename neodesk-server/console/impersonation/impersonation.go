// Package impersonation decides "Sign in as": an admin views the console
// as another account sees it, to reproduce a problem or check what a role
// can do. It is read-only, and the server refuses everything else.
//
// The session side (a session of its own, a second cookie, the admin's
// session untouched) is in package session. This package holds the rules:
//
//   - who may view whom (MayView): only an account with fewer rights in
//     every respect, checked when it starts and again on every request;
//   - the step of the request pipeline (Gate) that refuses every write and
//     every read that shows a secret or starts remote work, writes an audit
//     entry for each API request, and ends the impersonation when a check
//     fails or its time is up;
//   - which websockets an impersonation may open (RefuseUpgrade).
package impersonation

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/neodeskapp/neodesk/neodesk-server/console/lifecycle"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/authn"
	"github.com/neodeskapp/neodesk/neodesk-server/console/devicegroups"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/i18n"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// How long an impersonation lasts: Period from the start, and MaxPeriod
// from the start once it was made longer (once).
const (
	Period    = 15 * time.Minute
	MaxPeriod = 30 * time.Minute
)

// NoticeMailKey is the key in server_config of Settings, Sign-in, "Mail
// people when an admin views NeoDesk as them": "false" turns the mail off,
// anything else (and no value) leaves it on.
const NoticeMailKey = "impersonation_notice_mail"

// Service holds the checks. It reads the console's services from the
// Deps it was made with at every call.
type Service struct {
	d   *kit.Deps
	now func() time.Time
}

// New returns the checks for a console.
func New(d *kit.Deps) *Service { return &Service{d: d, now: time.Now} }

// Now is the service's clock.
func (s *Service) Now() time.Time { return s.now() }

// SetClock replaces the clock, for tests.
func (s *Service) SetClock(now func() time.Time) { s.now = now }

// Refusal is why an admin may not view the console as an account, or may
// no longer: a code for the API and the text in the request language.
type Refusal struct {
	// Code is the API's code, "impersonation_<what>".
	Code string
	// Key is the language key of the text, English the text when the
	// language has none, Vars its placeholders.
	Key     string
	English string
	Vars    i18n.Vars
}

// Text is the refusal in the language of the request's answer; the
// English text when the language files have none for it.
func (f *Refusal) Text(lang string, bundle *i18n.Bundle) string {
	english := f.English
	for _, v := range f.Vars {
		english = strings.ReplaceAll(english, "{"+v.Name+"}", fmt.Sprint(v.Value))
	}
	if bundle == nil {
		return english
	}
	if lang == "" {
		lang = bundle.DefaultLang
	}
	if text := bundle.T(lang, f.Key, f.Vars); text != f.Key {
		return text
	}
	return english
}

func refuse(code, english string, vars ...any) *Refusal {
	return &Refusal{Code: "impersonation_" + code, Key: "impersonation.refused_" + code, English: english, Vars: i18n.V(vars...)}
}

// The refusals of MayView and of the start.
var (
	refusedSelf     = func() *Refusal { return refuse("self", "You can't sign in as yourself.") }
	refusedNotFound = func() *Refusal { return refuse("not_found", "This account no longer exists.") }
	refusedDisabled = func(name string) *Refusal {
		return refuse("disabled", "{username} is disabled. Enable the account to view NeoDesk as it.", "username", name)
	}
	refusedNoConsole = func(name string) *Refusal {
		return refuse("no_console", "{username} can't sign in to the console, so there is nothing to view.", "username", name)
	}
	refusedRole = func(name string) *Refusal {
		return refuse("role", "You can only view NeoDesk as accounts with fewer rights than yours. {username} has the same role as you or a higher one.", "username", name)
	}
	refusedPermissions = func(name string) *Refusal {
		return refuse("permissions", "{username} has permissions you don't have. You can only view NeoDesk as accounts with fewer rights than yours.", "username", name)
	}
	refusedPanelAccess = func(name string) *Refusal {
		return refuse("panel_access", "{username} can reach parts of the console you can't. You can only view NeoDesk as accounts with fewer rights than yours.", "username", name)
	}
	refusedScope = func(name string) *Refusal {
		return refuse("scope", "{username} can see devices you can't. You can only view NeoDesk as accounts with fewer rights than yours.", "username", name)
	}
	refusedNotAllowed = func() *Refusal {
		return refuse("not_allowed", "Your account may no longer view NeoDesk as other accounts.")
	}
	refusedAdminDisabled = func() *Refusal { return refuse("admin_disabled", "Your account was disabled.") }
	refusedExpired       = func() *Refusal { return refuse("expired", "The time to view NeoDesk as another account is up.") }
	refusedChanged       = func(name string) *Refusal {
		return refuse("changed", "The role of {username} changed.", "username", name)
	}
)

// Account is an account as the checks read it, from the users table.
type Account = authn.Account

// Account loads an account by id, nil when there is none.
func (s *Service) Account(ctx context.Context, id int64) (*Account, error) {
	return s.d.Authn.UserByID(ctx, id)
}

func roleOf(a *Account) string { return a.Role.String }

// consoleRole reports whether a role signs in to the console: the roles
// people have, but not pro (the RustDesk Pro activation, API only) and not
// the devices' own.
func consoleRole(role string) bool {
	return auth.ValidRole(role) && !auth.IsProRole(role) && !auth.IsDeviceRole(role)
}

// MayView decides whether admin may view the console as target: never
// itself, never a disabled account or one that can't use the console, and
// only an account with fewer rights in every respect: a lower role level
// (server_admin and global_admin share one), no permission the admin lacks
// (the role overrides counted), no panel section at a higher level than
// the admin's, and no device the admin can't see. nil means yes; a failed
// lookup is an error, and the caller refuses.
func (s *Service) MayView(ctx context.Context, admin, target *Account) (*Refusal, error) {
	switch {
	case target == nil:
		return refusedNotFound(), nil
	case admin == nil:
		return nil, errors.New("impersonation: no admin account")
	case admin.ID.V == target.ID.V:
		return refusedSelf(), nil
	case target.Disabled.True():
		return refusedDisabled(target.Username), nil
	}
	// An account past its end date is as good as disabled (sign-in and API
	// tokens refuse it), also before the job sets disabled
	if ended, err := lifecycle.Expired(ctx, s.d.Store, target.ID.V, s.now()); err != nil {
		return nil, err
	} else if ended {
		return refusedDisabled(target.Username), nil
	}
	adminRole, targetRole := roleOf(admin), roleOf(target)
	if !consoleRole(targetRole) {
		return refusedNoConsole(target.Username), nil
	}
	if auth.RoleLevel(targetRole) >= auth.RoleLevel(adminRole) {
		return refusedRole(target.Username), nil
	}
	for _, p := range auth.AllPermissions {
		theirs, err := s.d.Access.RoleHasEffectivePermission(ctx, targetRole, p)
		if err != nil {
			return nil, err
		}
		if !theirs {
			continue
		}
		mine, err := s.d.Access.RoleHasEffectivePermission(ctx, adminRole, p)
		if err != nil {
			return nil, err
		}
		if !mine {
			return refusedPermissions(target.Username), nil
		}
	}
	wider, err := s.widerPanelAccess(ctx, admin, target)
	if err != nil {
		return nil, err
	}
	if wider {
		return refusedPanelAccess(target.Username), nil
	}
	more, err := s.seesMoreDevices(ctx, admin, target)
	if err != nil {
		return nil, err
	}
	if more {
		return refusedScope(target.Username), nil
	}
	return nil, nil
}

// widerPanelAccess reports whether the target's panel access policy
// reaches a higher level than the admin's in any section.
func (s *Service) widerPanelAccess(ctx context.Context, admin, target *Account) (bool, error) {
	mine, err := s.d.Access.ResolveForUser(ctx, access.PolicyUser{ID: admin.ID.V, Role: roleOf(admin)})
	if err != nil {
		return false, err
	}
	theirs, err := s.d.Access.ResolveForUser(ctx, access.PolicyUser{ID: target.ID.V, Role: roleOf(target)})
	if err != nil {
		return false, err
	}
	for _, key := range theirs.Sections.Keys() {
		their, _ := theirs.Sections.Get(key)
		my, ok := mine.Sections.Get(key)
		level := access.Level("none")
		if ok {
			level = my.Access
		}
		if access.CompareLevels(string(their.Access), string(level)) > 0 {
			return true, nil
		}
	}
	return false, nil
}

// seesMoreDevices reports whether the target's device scope holds a
// device the admin's does not. An admin without a limit sees every device;
// a target without one sees more than any admin with one.
func (s *Service) seesMoreDevices(ctx context.Context, admin, target *Account) (bool, error) {
	adminUser := &access.ScopeUser{ID: admin.ID.V, Username: admin.Username, Role: roleOf(admin)}
	limited, err := s.d.Access.DeviceScopeForUser(ctx, adminUser, nil)
	if err != nil {
		return false, err
	}
	if limited.Unlimited() {
		return false, nil
	}
	var all []*goapi.Object
	if s.d.Backend != nil {
		all = s.d.Backend.GetAllDevices(ctx, goapi.DeviceFilters{})
	}
	devices := devicegroups.ScopeDevices(all)
	mine, err := s.d.Access.DeviceScopeForUser(ctx, adminUser, devices)
	if err != nil {
		return false, err
	}
	theirs, err := s.d.Access.DeviceScopeForUser(ctx, &access.ScopeUser{ID: target.ID.V, Username: target.Username, Role: roleOf(target)}, devices)
	if err != nil {
		return false, err
	}
	if mine.Unlimited() {
		return false, nil
	}
	if theirs.Unlimited() {
		return true, nil
	}
	for _, id := range theirs.IDs() {
		if !mine.Has(id) {
			return true, nil
		}
	}
	return false, nil
}

// MayStillView is the check of every request of an impersonation: the
// time is not up, the admin's account still exists, is enabled and still
// has the permission, the account viewed has the role the session was
// opened with, and MayView still says yes.
func (s *Service) MayStillView(ctx context.Context, adminID, targetID int64, targetRole string, expiresAt int64) (*Refusal, error) {
	if s.now().UnixMilli() >= expiresAt {
		return refusedExpired(), nil
	}
	admin, err := s.Account(ctx, adminID)
	if err != nil {
		return nil, err
	}
	if admin == nil || admin.Disabled.True() {
		return refusedAdminDisabled(), nil
	}
	if ended, err := lifecycle.Expired(ctx, s.d.Store, adminID, s.now()); err != nil {
		return nil, err
	} else if ended {
		return refusedAdminDisabled(), nil
	}
	allowed, err := s.d.Access.RoleHasEffectivePermission(ctx, roleOf(admin), auth.PermUserImpersonate)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return refusedNotAllowed(), nil
	}
	target, err := s.Account(ctx, targetID)
	if err != nil {
		return nil, err
	}
	if target != nil && roleOf(target) != targetRole {
		return refusedChanged(target.Username), nil
	}
	return s.MayView(ctx, admin, target)
}

// NoticeMail reports whether the people viewed get a mail (NoticeMailKey).
// A setting that can't be read leaves the mail on.
func (s *Service) NoticeMail(ctx context.Context) bool {
	var value sql.NullString
	err := s.d.Store.QueryRow(ctx, `SELECT value FROM server_config WHERE key = ?`, NoticeMailKey).Scan(&value)
	if err != nil {
		return true
	}
	return !strings.EqualFold(strings.TrimSpace(value.String), "false")
}

// SetNoticeMail saves NoticeMailKey.
func (s *Service) SetNoticeMail(ctx context.Context, on bool) error {
	db := s.d.Store
	create := db.Pick(`CREATE TABLE IF NOT EXISTS server_config (key TEXT PRIMARY KEY, value TEXT DEFAULT '')`,
		`CREATE TABLE IF NOT EXISTS server_config (key TEXT PRIMARY KEY, value TEXT NOT NULL DEFAULT '')`)
	if _, err := db.Exec(ctx, create); err != nil {
		return fmt.Errorf("save %s: %w", NoticeMailKey, err)
	}
	value := "false"
	if on {
		value = "true"
	}
	if _, err := db.Exec(ctx, `INSERT INTO server_config (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, NoticeMailKey, value); err != nil {
		return fmt.Errorf("save %s: %w", NoticeMailKey, err)
	}
	return nil
}
