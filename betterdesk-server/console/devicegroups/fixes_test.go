package devicegroups

import (
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// The bugs of BUGS.md that this package fixed, each with the behaviour the
// fix changed. The oracle tests list the Node vectors each fix changes.

// TestMergeLeavesBannedDevicesOut: a merge of every device leaves banned
// and deleted devices out of the book, as it takes them out of the stored
// peers, with their tags; the other devices come in as before (BUGS DGR1).
func TestMergeLeavesBannedDevicesOut(t *testing.T) {
	devices := list(t, []byte(`[
		{"id":"dev-1","tags":["lab"]},
		{"id":"dev-2","tags":["banned-tag"],"banned":true},
		{"id":"dev-3","tags":["deleted-tag"],"soft_deleted":1},
		{"id":"dev-4","tags":["new"],"hostname":"h4"}]`))
	data := `{"tags":["lab"],"peers":[{"id":"dev-1","tags":["lab"]},{"id":"dev-2","tags":["kept"]}]}`
	want := `{"tags":["lab","new"],"peers":[{"id":"dev-1","tags":["lab"]},{"id":"dev-4","hostname":"h4","tags":["new"]}]}`
	if got := MergeAddressBookData(data, MergeOptions{Devices: devices}); got != want {
		t.Errorf("merge of every device:\n got  %s\n want %s", got, want)
	}
	if got := MergeAddressBookData(data, MergeOptions{Devices: devices, KnownOnly: true}); got != `{"tags":["lab"],"peers":[{"id":"dev-1","tags":["lab"]}]}` {
		t.Errorf("merge of the known peers: %s", got)
	}
}

// TestRenameKeepsEveryTag: renaming a tag replaces it and keeps every
// other tag of the device, also past the 20th; a tag that then repeats
// goes as before (BUGS DGR4).
func TestRenameKeepsEveryTag(t *testing.T) {
	tags := make([]any, 25)
	want := make([]string, 25)
	for i := range tags {
		tags[i] = "t" + strconv.Itoa(i)
		want[i] = "t" + strconv.Itoa(i)
	}
	tags[22], want[22] = "old", "new"
	if got, changed := RewriteTagsRename(tags, "old", "new"); !changed || !reflect.DeepEqual(got, want) {
		t.Errorf("rename on a device with 25 tags: %q, %v", got, changed)
	}
	if got, changed := RewriteTagsRename(tags, "old", "t24"); !changed || len(got) != 24 || got[22] != "t24" {
		t.Errorf("rename into a tag the device has: %q, %v", got, changed)
	}
}

// TestCutKeepsWholeCharacters: a tag, and the fields of a device group,
// cut to a length in UTF-16 units stop before a character outside the
// Basic Multilingual Plane that does not fit whole, where half of it was
// kept as U+FFFD (BUGS DGR2).
func TestCutKeepsWholeCharacters(t *testing.T) {
	a := func(n int) string { return strings.Repeat("a", n) }
	for _, c := range []struct {
		in   string
		n    int
		want string
	}{
		{a(49) + "😀", 50, a(49)},
		{a(49) + "😀b", 50, a(49)},
		{a(48) + "😀b", 50, a(48) + "😀"},
		{"😀😀", 3, "😀"},
		{"é😀", 2, "é"},
		{a(50), 50, a(50)},
	} {
		if got := cut(c.in, c.n); got != c.want {
			t.Errorf("cut(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
		}
	}
	if got := SanitizeTag(a(49) + "😀"); got != a(49) {
		t.Errorf("SanitizeTag of 49 letters and an emoji: %q", got)
	}
	if got := NormalizeTags([]any{a(49) + "😀", a(49)}); len(got) != 1 || got[0] != a(49) {
		t.Errorf("NormalizeTags of two tags equal after the cut: %q", got)
	}
	p := NormalizeGroupPayload(map[string]any{"name": a(79) + "😀", "source_type": "tag", "tag_filter": a(49) + "😀", "note": a(511) + "😀"})
	if p.Name != a(79) || p.TagFilter != a(49) || p.Note == nil || *p.Note != a(511) {
		t.Errorf("group payload: name %q, tag filter %q, note %v", p.Name, p.TagFilter, p.Note)
	}
}
