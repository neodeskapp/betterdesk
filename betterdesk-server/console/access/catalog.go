// Package access decides what a console account may do: the permissions of
// its role (the map in package auth plus the overrides stored per role), the
// panel access policy of the account (user rows over group rows over the
// role), the guard that maps a request to a panel section, and which devices
// an account may see. It answers with values; the middleware in console/kit
// writes the responses.
//
// Per request, in this order:
//
//	c := service.Load(ctx, access.Session{UserID: ..., HasUser: ..., Role: ...})
//	guard := c.Guard(r.Method, path)        // path as sent, without the query
//	if !guard.Decision.Passed() { answer guard.Decision }
//	...
//	d := c.RequirePermission("device.view", originalURL) // per route
//	if !d.Passed() { answer d }
//
// A Decision says what to answer: a JSON refusal, a redirect or the
// console's error page. Checks without a request (websocket upgrades) use
// Service.RoleHasEffectivePermission.
//
// The package reads and writes the console's tables and never creates
// them.
package access

import (
	"encoding/json"
	"strings"
)

// Level is how much of a panel section an account may use.
type Level string

// The three levels, lowest first.
const (
	LevelNone  Level = "none"
	LevelRead  Level = "read"
	LevelWrite Level = "write"
)

// Levels lists the levels in rising order, as the policy editors show them.
var Levels = []Level{LevelNone, LevelRead, LevelWrite}

// rank orders the levels. Anything that is not a level ranks as none.
func rank(l Level) int {
	switch l {
	case LevelWrite:
		return 2
	case LevelRead:
		return 1
	}
	return 0
}

// Permission is a permission name in the catalog. The empty permission
// stands for "no permission grants this" and is null in JSON.
type Permission string

// MarshalJSON writes null for the empty permission.
func (p Permission) MarshalJSON() ([]byte, error) {
	if p == "" {
		return []byte("null"), nil
	}
	return json.Marshal(string(p))
}

// Section is one entry of the panel access catalog: a part of the console
// and the role permissions that stand for reading and writing it.
type Section struct {
	Key             string     `json:"key"`
	Label           string     `json:"label"`
	Group           string     `json:"group"`
	ReadPermission  Permission `json:"readPermission"`
	WritePermission Permission `json:"writePermission"`
}

// SectionGroup is a heading in the policy editors. A policy row for
// "group:<key>" sets every section of the group at once.
type SectionGroup struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// ClientFeature is a remote client feature a policy can turn off. A hard
// locked feature is off for everyone.
type ClientFeature struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	HardLock bool   `json:"hardLock"`
}

// Catalog is the list of panel sections, in the order the policy editors
// and the resolved policy use. It is the same list the Go API serves at
// GET /api/panel/catalog (package api, panelSectionCatalog).
var Catalog = []Section{
	{"dashboard", "Dashboard", "core", "device.view", ""},
	{"devices", "Devices", "core", "device.view", "device.edit"},
	{"registrations", "Registrations", "core", "enrollment.approve", "enrollment.manage"},
	{"remote", "Remote access", "core", "remote_target.view", "remote_target.connect"},
	{"users", "Users", "administration", "user.view", "user.edit"},
	{"organizations", "Organizations", "administration", "user.view", "org.manage_devices"},
	{"folders", "Folders", "administration", "device.view", "device.edit"},
	{"strategies", "Strategies", "administration", "device.view", "device.edit"},
	{"guests", "Guest access", "administration", "guest.create", "guest.create"},
	{"tickets", "Tickets", "operations", "device.view", "device.edit"},
	{"help-requests", "Help requests", "operations", "device.view", "device.edit"},
	{"cdap", "CDAP", "operations", "cdap.view", "cdap.command"},
	{"fleet", "Fleet", "operations", "org.manage_devices", "org.manage_devices"},
	{"chat", "Chat", "operations", "chat.access", "chat.access"},
	{"activity", "Activity", "operations", "audit.view", ""},
	{"automation", "Automation", "operations", "device.view", "device.edit"},
	{"files", "File transfer", "operations", "device.view", "device.edit"},
	{"network", "Network", "operations", "device.view", "server.config"},
	{"dataguard", "Data guard", "operations", "server.config", "server.config"},
	{"reports", "Reports", "operations", "audit.view", "audit.view"},
	{"agents", "Agents", "operations", "device.view", "device.edit"},
	{"toolkit", "Toolkit", "operations", "device.connect", "device.connect"},
	{"commercialization", "Commercialization", "commercialization", "billing.view", "billing.manage"},
	{"scaling", "Scaling", "commercialization", "server.config", "server.config"},
	{"cross-platform", "Cross-platform", "commercialization", "device.view", "device.edit"},
	{"security-audit", "Security audit", "security", "audit.view", "server.config"},
	{"languages", "Languages", "system", "server.config", "server.config"},
	{"resource-control", "Resource control", "system", "server.config", "server.config"},
	{"system", "System", "system", "server.config", "server.config"},
	{"permissions", "Permissions", "system", "server.config", "server.config"},
	{"server-management", "Server management", "system", "server.config", "server.config"},
	{"settings", "Settings", "system", "server.config", "server.config"},
	{"server-attestation", "Server attestation", "system", "server.attestation", "branding.edit"},
}

// SectionGroups lists the catalog's groups in display order.
var SectionGroups = []SectionGroup{
	{"core", "Core"},
	{"administration", "Administration"},
	{"operations", "Operations"},
	{"commercialization", "Commercialization"},
	{"security", "Security"},
	{"system", "System"},
}

// ClientFeatures lists the remote client features a policy can switch.
var ClientFeatures = []ClientFeature{
	{"remote_control", "Remote control", false},
	{"file_transfer", "File transfer", false},
	{"clipboard", "Clipboard", false},
	{"audio", "Audio", false},
	{"tunnel", "Tunnel", false},
	{"terminal", "Terminal", false},
	{"port_forwarding", "Port forwarding", false},
	{"reboot", "Remote reboot", false},
	{"privacy_mode", "Privacy mode", false},
	{"client_actions", "Client actions", true},
}

// NormalizeLevel reads a level from a stored or submitted value. A number 2
// or more is write, 1 is read, any other number none. Anything else is read
// as text, trimmed and lower-cased, and must name a level; if not it is none.
func NormalizeLevel(value any) Level {
	if l, ok := value.(Level); ok {
		value = string(l)
	}
	if f, ok := jsNumber(value); ok {
		switch {
		case f >= 2:
			return LevelWrite
		case f == 1:
			return LevelRead
		}
		return LevelNone
	}
	switch l := Level(asciiLower(jsTrim(jsStringOrEmpty(value)))); l {
	case LevelNone, LevelRead, LevelWrite:
		return l
	}
	return LevelNone
}

// asciiLower lower-cases A to Z and leaves every other character alone. No
// character outside ASCII lower-cases to a letter of a level name, so this
// is enough to recognise one.
func asciiLower(s string) string {
	if strings.IndexFunc(s, func(r rune) bool { return r >= 'A' && r <= 'Z' }) < 0 {
		return s
	}
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + ('a' - 'A')
		}
	}
	return string(b)
}

// CompareLevels is negative when left is the lower level, zero when both
// are the same and positive when left is the higher one.
func CompareLevels(left, right any) int {
	return rank(NormalizeLevel(left)) - rank(NormalizeLevel(right))
}

// CatalogEntry returns the first catalog entry with the key.
func CatalogEntry(key string) (Section, bool) {
	for _, entry := range Catalog {
		if entry.Key == key {
			return entry, true
		}
	}
	return Section{}, false
}

// LegacySectionCeiling is the level a role reaches in a section through its
// permissions alone: write with the section's write permission, read with
// its read permission, none otherwise. Super admins have write everywhere,
// also for keys the catalog does not know.
func LegacySectionCeiling(key string, hasPermission func(string) bool, isSuperAdmin bool) Level {
	if isSuperAdmin {
		return LevelWrite
	}
	entry, ok := CatalogEntry(key)
	if !ok {
		return LevelNone
	}
	if entry.WritePermission != "" && hasPermission(string(entry.WritePermission)) {
		return LevelWrite
	}
	if entry.ReadPermission != "" && hasPermission(string(entry.ReadPermission)) {
		return LevelRead
	}
	return LevelNone
}

// CatalogView is what the policy editors load: the levels, the groups, the
// sections and the client features.
type CatalogView struct {
	Levels   []Level         `json:"levels"`
	Groups   []SectionGroup  `json:"groups"`
	Sections []Section       `json:"sections"`
	Features []ClientFeature `json:"features"`
}

// GetCatalog returns the catalog for the policy editors.
func GetCatalog() CatalogView {
	return CatalogView{Levels: Levels, Groups: SectionGroups, Sections: Catalog, Features: ClientFeatures}
}
