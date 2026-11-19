package devicegroups

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/unitronix/betterdesk-server/console/goapi"
)

// A RustDesk address book owns explicit peer tags only. Folders are shown
// to RustDesk clients as device groups, and a folder's name is never read
// as a tag or taken out of the tags: people use the same word for both on
// purpose.
//
// Address book data is JSON whose keys keep their order, so it is held as
// *goapi.Object here, and devices are the objects the device layer
// returns. A device that is no object counts as one without an id.

// MaxTagLength is the length a tag is cut to.
const MaxTagLength = 50

// SanitizeTag cleans one tag: control characters go (tab, line feed and
// carriage return count as white space instead), white space around it
// goes, and what is left is cut to 50 characters.
func SanitizeTag(value string) string {
	value = strings.Map(func(r rune) rune {
		if r <= 0x08 || r == 0x0b || r == 0x0c || r >= 0x0e && r <= 0x1f {
			return -1
		}
		return r
	}, value)
	return cut(jsTrim(value), MaxTagLength)
}

// tagList collects tags without repeats, in the order they come.
type tagList struct {
	tags []string
	seen map[string]bool
}

func newTagList(tags []string) *tagList {
	l := &tagList{tags: tags, seen: make(map[string]bool, len(tags))}
	for _, tag := range tags {
		l.seen[tag] = true
	}
	return l
}

func (l *tagList) push(value string) {
	tag := SanitizeTag(value)
	if tag == "" || l.seen[tag] {
		return
	}
	l.seen[tag] = true
	l.tags = append(l.tags, tag)
}

// NormalizeTags reads tags from a list, from a JSON list in text or from
// comma-separated text: each cleaned by SanitizeTag, without empty ones
// and repeats. Anything else, and text that is JSON but no list, has no
// tags.
func NormalizeTags(value any) []string {
	out := newTagList([]string{})
	var items []any
	switch x := value.(type) {
	case []any:
		items = x
	case []string:
		for _, item := range x {
			out.push(item)
		}
		return out.tags
	case string:
		trimmed := jsTrim(x)
		if trimmed == "" {
			return out.tags
		}
		if strings.HasPrefix(trimmed, "[") {
			if parsed, err := goapi.Parse([]byte(trimmed)); err == nil {
				list, ok := parsed.([]any)
				if !ok {
					return out.tags
				}
				items = list
				break
			}
		}
		for _, item := range strings.Split(trimmed, ",") {
			out.push(item)
		}
		return out.tags
	default:
		return out.tags
	}
	for _, item := range items {
		out.push(text(item))
	}
	return out.tags
}

// FilterFolderTags is NormalizeTags: folder names are not taken out of
// the tags.
func FilterFolderTags(tags any) []string { return NormalizeTags(tags) }

// ParseAddressBookData reads address book data: an object, or JSON text
// of one. Anything else is an empty book. The result always has "peers"
// as a list and "tags" as a normalised list. An object is copied at its
// top level; the peers in it are the caller's.
func ParseAddressBookData(data any) *goapi.Object {
	var parsed *goapi.Object
	switch x := data.(type) {
	case *goapi.Object:
		if x != nil {
			parsed = x.Clone()
		}
	case []any:
		// A list read as an object has its positions as keys
		parsed = goapi.NewObject()
		for i, item := range x {
			parsed.Set(strconv.Itoa(i), item)
		}
	case string:
		if jsTrim(x) != "" {
			if value, err := goapi.Parse([]byte(x)); err == nil {
				parsed, _ = value.(*goapi.Object)
			}
		}
	}
	if parsed == nil {
		parsed = goapi.NewObject()
	}
	peers, ok := parsed.Get("peers").([]any)
	if !ok {
		peers = []any{}
	}
	parsed.Set("peers", peers)
	parsed.Set("tags", tagValues(NormalizeTags(parsed.Get("tags"))))
	return parsed
}

// tagValues is a list of tags as a JSON list.
func tagValues(tags []string) []any {
	out := make([]any, len(tags))
	for i, tag := range tags {
		out[i] = tag
	}
	return out
}

func stringify(o *goapi.Object) string {
	out, err := o.MarshalJSON()
	if err != nil {
		return "{}"
	}
	return string(out)
}

// idOf is the trimmed id of a peer or a device, "" for one without.
func idOf(o *goapi.Object) string {
	if o == nil {
		return ""
	}
	return jsTrim(text(o.Get("id")))
}

func asObject(v any) *goapi.Object {
	o, _ := v.(*goapi.Object)
	return o
}

// MergeOptions say which devices an address book is merged with.
type MergeOptions struct {
	// Devices are the devices the caller may see.
	Devices []any
	// KnownOnly keeps devices out of the book that are not in it yet:
	// only the tags of peers already there are refreshed.
	KnownOnly bool
}

// MergeAddressBookData brings address book data in line with the devices
// and returns it as JSON text:
//
//   - peers that are banned or deleted devices leave the book, and such
//     devices do not come in again
//   - every peer's tags are normalised, and a device's tags are added to
//     the tags of its peer
//   - a device that is not in the book is added as a peer, with its user,
//     host name, alias and platform
//   - the book's own tag list gets every tag a peer has
func MergeAddressBookData(data any, opts MergeOptions) string {
	ab := ParseAddressBookData(data)

	banned := map[string]bool{}
	for _, d := range opts.Devices {
		device := asObject(d)
		if id := idOf(device); id != "" && (truthy(device.Get("banned")) || truthy(device.Get("soft_deleted"))) {
			banned[id] = true
		}
	}

	global := newTagList(NormalizeTags(ab.Get("tags")))
	var peers []any
	byID := map[string]*goapi.Object{}
	for _, p := range ab.Get("peers").([]any) {
		peer := asObject(p)
		if id := idOf(peer); id != "" && !banned[id] {
			peers = append(peers, peer)
		}
	}
	for _, p := range peers {
		peer := p.(*goapi.Object)
		tags := NormalizeTags(peer.Get("tags"))
		peer.Set("tags", tagValues(tags))
		byID[idOf(peer)] = peer
		for _, tag := range tags {
			global.push(tag)
		}
	}

	for _, d := range opts.Devices {
		device := asObject(d)
		id := idOf(device)
		if id == "" || banned[id] {
			continue
		}
		existing := byID[id]
		if existing == nil && opts.KnownOnly {
			continue
		}
		var merged *tagList
		if existing != nil {
			merged = newTagList(NormalizeTags(existing.Get("tags")))
		} else {
			merged = newTagList([]string{})
		}
		for _, tag := range NormalizeTags(device.Get("tags")) {
			merged.push(tag)
			global.push(tag)
		}
		peer := mergePeerFields(existing, device, merged.tags)
		if existing == nil {
			peers = append(peers, peer)
			byID[id] = peer
		}
	}
	if peers == nil {
		peers = []any{}
	}
	ab.Set("peers", peers)
	ab.Set("tags", tagValues(global.tags))
	return stringify(ab)
}

// mergePeerFields fills what a peer lacks from its device and sets its
// tags.
func mergePeerFields(peer, device *goapi.Object, tags []string) *goapi.Object {
	if peer == nil {
		peer = goapi.NewObject()
	}
	peer.Set("id", text(or(peer.Get("id"), device.Get("id"))))
	fill := func(key string, sources ...string) {
		if truthy(peer.Get(key)) {
			return
		}
		for _, source := range sources {
			if value := device.Get(source); truthy(value) {
				peer.Set(key, jsString(value))
				return
			}
		}
	}
	fill("username", "username", "user")
	fill("hostname", "hostname")
	fill("alias", "display_name", "note")
	fill("platform", "platform", "os")
	peer.Set("tags", tagValues(tags))
	return peer
}

// CollectVisibleTags returns the tags of the devices, without repeats, in
// the order of JavaScript's localeCompare.
func CollectVisibleTags(devices []any) []string {
	list := newTagList([]string{})
	for _, d := range devices {
		if device := asObject(d); device != nil {
			for _, tag := range NormalizeTags(device.Get("tags")) {
				list.push(tag)
			}
		}
	}
	c := collate.New(language.English)
	sort.SliceStable(list.tags, func(i, j int) bool {
		return c.CompareString(asciiDigits(list.tags[i]), asciiDigits(list.tags[j])) < 0
	})
	return list.tags
}

// PeerTags are the tags an address book gives a peer.
type PeerTags struct {
	ID   string   `json:"id"`
	Tags []string `json:"tags"`
}

// CollectPeerTagUpdates returns the tags of every peer of an address
// book that has a "tags" key, for the first peer with each id.
func CollectPeerTagUpdates(data any) []PeerTags {
	updates := []PeerTags{}
	seen := map[string]bool{}
	for _, p := range ParseAddressBookData(data).Get("peers").([]any) {
		peer := asObject(p)
		id := idOf(peer)
		if id == "" || seen[id] || !peer.Has("tags") {
			continue
		}
		seen[id] = true
		updates = append(updates, PeerTags{ID: id, Tags: NormalizeTags(peer.Get("tags"))})
	}
	return updates
}

// FilterAddressBookPeersByScope takes the peers out of address book data
// that are devices of this server (knownDeviceIDs) outside the caller's
// scope (visible), and returns the book as JSON text. Peers the server
// does not know, ids a person typed in, stay. A nil visible set filters
// nothing.
func FilterAddressBookPeersByScope(data any, visible func(id string) bool, knownDeviceIDs []any) string {
	ab := ParseAddressBookData(data)
	if visible == nil {
		return stringify(ab)
	}
	known := map[string]bool{}
	for _, id := range knownDeviceIDs {
		if id := jsTrim(text(id)); id != "" {
			known[id] = true
		}
	}
	peers := []any{}
	for _, p := range ab.Get("peers").([]any) {
		peer := asObject(p)
		if id := idOf(peer); id != "" && !(known[id] && !visible(id)) {
			peers = append(peers, peer)
		}
	}
	ab.Set("peers", peers)
	return stringify(ab)
}

var _ = json.Valid

// asciiDigits replaces the decimal digits of other scripts by 0 to 9:
// JavaScript's order does not tell an Arabic or Devanagari digit from the
// ASCII one, the collation tables used here do. The fullwidth and the
// mathematical digits stay: both orders put them after the ASCII digit.
func asciiDigits(s string) string {
	folded := func(r rune) bool {
		if r <= unicode.MaxASCII || !unicode.IsDigit(r) {
			return false
		}
		return !(r >= 0xFF10 && r <= 0xFF19) && !(r >= 0x1D7CE && r <= 0x1D7FF)
	}
	if strings.IndexFunc(s, folded) < 0 {
		return s
	}
	return strings.Map(func(r rune) rune {
		if !folded(r) {
			return r
		}
		// The digits of a script are ten code points in a row, from 0 to 9
		zero := r
		for unicode.IsDigit(zero - 1) {
			zero--
		}
		return '0' + (r-zero)%10
	}, s)
}
