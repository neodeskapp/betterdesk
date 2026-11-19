package devicegroups

import (
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
