// Package devicegroups holds what the console does with device groups,
// tags and address books besides deciding who may see a device (that is
// console/access): reading a submitted device group, renaming and
// deleting tags across devices, and keeping RustDesk address books in
// line with the devices' tags.
//
// A submitted device group reaches this package as decoded JSON (nil,
// bool, float64, string, []any, map[string]any). Devices and address
// book data are the values of console/goapi, whose objects keep their
// keys in order. Both are read the way the Node console read them: what
// counts as empty, how a value reads as text.
package devicegroups

import (
	"math"
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
)

// GroupPayload is a submitted device group after normalising.
type GroupPayload struct {
	// GUID is at most 64 characters, empty when none was given.
	GUID string
	// Name is at most 80 characters.
	Name string
	// SourceType is "tag" for a group whose members are the devices with a
	// tag, else "manual".
	SourceType string
	// TagFilter is the tag of a "tag" group, at most 50 characters, and
	// empty for a manual group.
	TagFilter string
	// AllowedUsers and AllowedGroups are the access lists: usernames and
	// user group guids.
	AllowedUsers  []string
	AllowedGroups []string
	// Note and TeamID are set only when the body had the key.
	Note   *string
	TeamID *string
	// NotText names the first member read as text that the body gives as
	// something else (a list, an object, a number, a flag): name, guid,
	// note, team_id, and for a tag group tag_filter and tag. "" when there
	// is none; null counts as not given. A route refuses such a group.
	NotText string
	// TooLong names the access list that holds more than
	// MaxAccessListEntries entries (AccessListTooLong), "" when neither
	// does. AllowedUsers and AllowedGroups keep only the first 100; a route
	// refuses such a group.
	TooLong string
}

// MaxAccessListEntries is the most usernames, and the most user groups, an
// access list of a device group or a folder takes.
const MaxAccessListEntries = 100

// AccessListTooLong names the submitted access list that holds more than
// MaxAccessListEntries different entries, read as the save reads it
// (trimmed, without blanks and repeats): "allowed_users" for the
// usernames, "allowed_groups" for the user groups, "" when neither does.
func AccessListTooLong(users, groups any) string {
	switch {
	case len(access.StoredUsernames(users)) > MaxAccessListEntries:
		return "allowed_users"
	case len(access.StoredGroupGUIDs(groups)) > MaxAccessListEntries:
		return "allowed_groups"
	}
	return ""
}

// NormalizeGroupPayload reads a submitted device group. A nil body reads
// as an empty one.
func NormalizeGroupPayload(body map[string]any) GroupPayload {
	p := GroupPayload{SourceType: "manual"}
	textMembers := []string{"name", "guid", "note", "team_id"}
	if s, ok := body["source_type"].(string); ok && s == "tag" || body["dynamic"] == true {
		p.SourceType = "tag"
		p.TagFilter = cut(jsTrim(text(or(body["tag_filter"], body["tag"]))), 50)
		textMembers = append(textMembers, "tag_filter", "tag")
	}
	for _, member := range textMembers {
		if _, isText := body[member].(string); body[member] != nil && !isText {
			p.NotText = member
			break
		}
	}
	if truthy(body["guid"]) {
		p.GUID = cut(jsTrim(jsString(body["guid"])), 64)
	}
	p.Name = cut(jsTrim(text(body["name"])), 80)
	groups := or(or(body["allowed_groups"], body["allowed_user_groups"]), body["user_group_guids"])
	p.AllowedUsers = access.NormalizeUsernames(body["allowed_users"])
	p.AllowedGroups = access.NormalizeGroupGUIDs(groups)
	p.TooLong = AccessListTooLong(body["allowed_users"], groups)
	if value, ok := body["note"]; ok {
		note := cut(jsTrim(text(value)), 512)
		p.Note = &note
	}
	if value, ok := body["team_id"]; ok {
		team := cut(jsTrim(text(value)), 64)
		p.TeamID = &team
	}
	return p
}

// GroupFields are the columns of a device group a write sets. A nil
// field is left as it is by an update.
type GroupFields struct {
	GUID       string
	Name       *string
	Note       *string
	TeamID     *string
	SourceType *string
	TagFilter  *string
}

// CreateFields are the columns a new device group gets from a payload: a
// note and a team id that were not submitted are empty.
func (p GroupPayload) CreateFields() GroupFields {
	empty := ""
	f := GroupFields{Name: &p.Name, Note: &empty, TeamID: &empty, SourceType: &p.SourceType, TagFilter: &p.TagFilter}
	if p.Note != nil {
		f.Note = p.Note
	}
	if p.TeamID != nil {
		f.TeamID = p.TeamID
	}
	return f
}

// UpdateFields are the columns an update of a device group sets from a
// payload: a note and a team id that were not submitted stay.
func (p GroupPayload) UpdateFields() GroupFields {
	return GroupFields{Name: &p.Name, Note: p.Note, TeamID: p.TeamID, SourceType: &p.SourceType, TagFilter: &p.TagFilter}
}

// truthy reports whether a decoded JSON value counts as set in
// JavaScript: nil, false, 0, NaN and "" do not.
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0 && !math.IsNaN(x)
	}
	return true
}

// or is JavaScript's a || b.
func or(a, b any) any {
	if truthy(a) {
		return a
	}
	return b
}

// text is JavaScript's String(v || ”).
func text(v any) string {
	if !truthy(v) {
		return ""
	}
	return jsString(v)
}

// jsString is JavaScript's String(v) for a decoded JSON value: a list is
// its items joined by commas (null items empty), an object is "[object
// Object]".
func jsString(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return jsNumberString(x)
	case []any:
		parts := make([]string, len(x))
		for i, item := range x {
			if item != nil {
				parts[i] = jsString(item)
			}
		}
		return strings.Join(parts, ",")
	case []string:
		return strings.Join(x, ",")
	}
	return "[object Object]"
}

// jsNumberString writes a number as JavaScript does: the shortest digits
// that read back as the same number, plain between 1e-7 and 1e21 and with
// an exponent outside.
func jsNumberString(f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	case f == 0:
		return "0"
	}
	sign := ""
	if f < 0 {
		sign, f = "-", -f
	}
	mantissa, exponent, _ := strings.Cut(strconv.FormatFloat(f, 'e', -1, 64), "e")
	digits := strings.Replace(mantissa, ".", "", 1)
	e, _ := strconv.Atoi(exponent)
	n, k := e+1, len(digits)
	switch {
	case k <= n && n <= 21:
		return sign + digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		return sign + digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return sign + "0." + strings.Repeat("0", -n) + digits
	}
	out := sign + digits[:1]
	if k > 1 {
		out += "." + digits[1:]
	}
	if n-1 < 0 {
		return out + "e-" + strconv.Itoa(1-n)
	}
	return out + "e+" + strconv.Itoa(n-1)
}

// jsTrim removes what JavaScript's trim removes.
func jsTrim(s string) string { return strings.TrimFunc(s, isJSSpace) }

func isJSSpace(r rune) bool {
	switch r {
	case '\t', '\n', '\v', '\f', '\r', ' ', 0xa0, 0x1680, 0x2028, 0x2029, 0x202f, 0x205f, 0x3000, 0xfeff:
		return true
	}
	return r >= 0x2000 && r <= 0x200a
}

// cut returns s cut to at most n UTF-16 code units, the length JavaScript
// counts. It stops before a character outside the Basic Multilingual
// Plane that does not fit whole, so no half of one is left.
func cut(s string, n int) string {
	units := 0
	for i, r := range s {
		size := 1
		if r > 0xffff {
			size = 2
		}
		if units+size > n {
			return s[:i]
		}
		units += size
	}
	return s
}
