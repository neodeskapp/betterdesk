package devicegroups

import (
	"context"
	"errors"
	"strings"
	"unicode"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/goapi"
)

// Tags are free text on devices. Renaming or deleting a tag rewrites the
// tags of the devices the caller may see, one device after the other.

// SanitizeTagName cleans a submitted tag name like SanitizeTag. A value
// that is no text is "".
func SanitizeTagName(value any) string {
	s, ok := value.(string)
	if !ok {
		return ""
	}
	return SanitizeTag(s)
}

// TagsEqual reports whether two tags are the same but for white space
// around them and the case of their letters.
func TagsEqual(a, b string) bool {
	return jsLower(jsTrim(a)) == jsLower(jsTrim(b))
}

// jsLower lowers text as JavaScript's toLowerCase does. It differs from
// strings.ToLower in two letters: "İ" becomes "i" and a dot above, and a
// capital sigma at the end of a word becomes the final form "ς".
func jsLower(s string) string {
	if !strings.ContainsAny(s, "İΣ") {
		return strings.ToLower(s)
	}
	runes := []rune(s)
	var b strings.Builder
	for i, r := range runes {
		switch r {
		case 'İ':
			b.WriteString("i̇")
		case 'Σ':
			if finalSigma(runes, i) {
				b.WriteRune('ς')
			} else {
				b.WriteRune('σ')
			}
		default:
			b.WriteRune(unicode.ToLower(r))
		}
	}
	return b.String()
}

// finalSigma reports whether the sigma at i ends a word: a letter comes
// before it and none after it, marks and the like aside.
func finalSigma(runes []rune, i int) bool {
	ignorable := func(r rune) bool {
		return unicode.In(r, unicode.Mn, unicode.Me, unicode.Cf, unicode.Lm, unicode.Sk) || strings.ContainsRune("'.:·’", r)
	}
	cased := func(r rune) bool { return unicode.IsUpper(r) || unicode.IsLower(r) || unicode.IsTitle(r) }
	before := false
	for j := i - 1; j >= 0; j-- {
		if ignorable(runes[j]) {
			continue
		}
		before = cased(runes[j])
		break
	}
	if !before {
		return false
	}
	for j := i + 1; j < len(runes); j++ {
		if ignorable(runes[j]) {
			continue
		}
		return !cased(runes[j])
	}
	return true
}

// DeviceHasTag reports whether a device has the tag, in any letter case.
func DeviceHasTag(device *goapi.Object, tag any) bool {
	expected := jsLower(SanitizeTagName(tag))
	if expected == "" || device == nil {
		return false
	}
	for _, t := range NormalizeTags(device.Get("tags")) {
		if jsLower(t) == expected {
			return true
		}
	}
	return false
}

// RewriteTagsRename returns tags with every tag equal to from replaced by
// to, without tags that then repeat (in any letter case). A rename never
// adds a tag, so every other tag of the device stays. changed says whether
// a tag was replaced. With an empty from or to the tags come back
// normalised and unchanged.
func RewriteTagsRename(tags any, fromTag, toTag any) (next []string, changed bool) {
	from, to := SanitizeTagName(fromTag), SanitizeTagName(toTag)
	if from == "" || to == "" {
		return NormalizeTags(tags), false
	}
	next = []string{}
	seen := map[string]bool{}
	for _, tag := range NormalizeTags(tags) {
		value := tag
		if TagsEqual(tag, from) {
			value, changed = to, true
		}
		key := jsLower(value)
		if seen[key] {
			continue
		}
		seen[key] = true
		next = append(next, value)
	}
	return next, changed
}

// RewriteTagsDelete returns tags without the tag, in any letter case.
func RewriteTagsDelete(tags any, tagToRemove any) (next []string, changed bool) {
	before := NormalizeTags(tags)
	remove := SanitizeTagName(tagToRemove)
	if remove == "" {
		return before, false
	}
	next = []string{}
	for _, tag := range before {
		if !TagsEqual(tag, remove) {
			next = append(next, tag)
		}
	}
	return next, len(next) != len(before)
}

// SetPeerTags stores the tags of a device. ok is false when the server
// refused; an error ends the whole rename or delete.
type SetPeerTags func(ctx context.Context, deviceID any, tags []string) (ok bool, err error)

// RenameResult is what a rename did.
type RenameResult struct {
	UpdatedDevices int `json:"updatedDevices"`
	UpdatedGroups  int `json:"updatedGroups"`
	Skipped        int `json:"skipped"`
	// Errors holds "invalid_tag" or "identical" when nothing was tried,
	// else the ids of the devices whose tags could not be stored.
	Errors []string `json:"errors"`
	From   string   `json:"from"`
	To     string   `json:"to"`
}

// RenameTagAcrossScope renames a tag on the devices given, which are the
// ones the caller may see, and in the filter of every tag group that
// selects by the old name. Devices and groups are changed in place. A
// rename that only changes the case of letters goes ahead.
func RenameTagAcrossScope(ctx context.Context, devices []*goapi.Object, groups []*access.DeviceGroup, fromTag, toTag any,
	setPeerTags SetPeerTags, updateDeviceGroup func(ctx context.Context, guid string, tagFilter string) error) (RenameResult, error) {
	res := RenameResult{Errors: []string{}, From: SanitizeTagName(fromTag), To: SanitizeTagName(toTag)}
	if res.From == "" || res.To == "" {
		res.Errors = []string{"invalid_tag"}
		return res, nil
	}
	if res.From == res.To {
		res.Errors = []string{"identical"}
		return res, nil
	}
	for _, device := range devices {
		if !DeviceHasTag(device, res.From) {
			continue
		}
		tags, changed := RewriteTagsRename(device.Get("tags"), res.From, res.To)
		if !changed {
			res.Skipped++
			continue
		}
		ok, err := setPeerTags(ctx, device.Get("id"), tags)
		if err != nil {
			return res, err
		}
		if !ok {
			res.Errors = append(res.Errors, jsString(device.Get("id")))
			res.Skipped++
			continue
		}
		device.Set("tags", tagValues(tags))
		res.UpdatedDevices++
	}
	for _, group := range groups {
		if group == nil || group.SourceType != "tag" {
			continue
		}
		filter := jsTrim(group.TagFilter)
		if filter == "" || !TagsEqual(filter, res.From) || updateDeviceGroup == nil {
			continue
		}
		if err := updateDeviceGroup(ctx, group.GUID, res.To); err != nil {
			return res, err
		}
		group.TagFilter = res.To
		res.UpdatedGroups++
	}
	return res, nil
}

// DeleteResult is what a delete did.
type DeleteResult struct {
	UpdatedDevices int      `json:"updatedDevices"`
	Skipped        int      `json:"skipped"`
	Errors         []string `json:"errors"`
	// AffectedGroups counts the tag groups that select by the deleted
	// tag. Their filter stays.
	AffectedGroups int    `json:"affectedGroups"`
	Tag            string `json:"tag"`
}

// DeleteTagAcrossScope takes a tag off the devices given, which are the
// ones the caller may see. Devices are changed in place.
func DeleteTagAcrossScope(ctx context.Context, devices []*goapi.Object, groups []*access.DeviceGroup, tag any, setPeerTags SetPeerTags) (DeleteResult, error) {
	res := DeleteResult{Errors: []string{}, Tag: SanitizeTagName(tag)}
	if res.Tag == "" {
		res.Errors = []string{"invalid_tag"}
		return res, nil
	}
	for _, device := range devices {
		if !DeviceHasTag(device, res.Tag) {
			continue
		}
		tags, changed := RewriteTagsDelete(device.Get("tags"), res.Tag)
		if !changed {
			res.Skipped++
			continue
		}
		ok, err := setPeerTags(ctx, device.Get("id"), tags)
		if err != nil {
			return res, err
		}
		if !ok {
			res.Errors = append(res.Errors, jsString(device.Get("id")))
			res.Skipped++
			continue
		}
		device.Set("tags", tagValues(tags))
		res.UpdatedDevices++
	}
	for _, group := range groups {
		if group != nil && group.SourceType == "tag" && TagsEqual(group.TagFilter, res.Tag) {
			res.AffectedGroups++
		}
	}
	return res, nil
}

// ErrAssignmentsNotIterable is the failure of the lookups below for every
// device id that is not empty (BUGS PA8): the Node console looped over
// the folder assignments as a list, and they are a map.
var ErrAssignmentsNotIterable = errors.New("(assignments || []) is not iterable")

// ResolveOperatorUsernamesForDevice names the accounts on the access
// lists of a device's folder and groups. It answers an empty list for an
// empty id and fails for every other id (BUGS PA8).
func ResolveOperatorUsernamesForDevice(deviceID string) ([]string, error) {
	if jsTrim(deviceID) == "" {
		return []string{}, nil
	}
	return nil, ErrAssignmentsNotIterable
}

// OperatorEmail is an account with its mail address.
type OperatorEmail struct {
	Username string `json:"username"`
	Email    string `json:"email"`
}

// ResolveOperatorEmailsForDevice is ResolveOperatorUsernamesForDevice
// with the mail addresses of the accounts.
func ResolveOperatorEmailsForDevice(deviceID string) ([]OperatorEmail, error) {
	if _, err := ResolveOperatorUsernamesForDevice(deviceID); err != nil {
		return nil, err
	}
	return []OperatorEmail{}, nil
}

// ResolveFolderNameForDevice names the folder a device is in. It answers
// "" for an empty id and fails for every other id (BUGS PA8).
func ResolveFolderNameForDevice(deviceID string) (string, error) {
	if jsTrim(deviceID) == "" {
		return "", nil
	}
	return "", ErrAssignmentsNotIterable
}
