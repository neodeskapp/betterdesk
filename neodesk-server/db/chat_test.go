package db

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

// TestGetUnreadBySender: the unread messages of a device, the ones
// GetUnreadCount counts, split by sender id and name (BUGS CH6).
func TestGetUnreadBySender(t *testing.T) {
	panelGroupStores(t, func(t *testing.T, database Database) {
		// The PostgreSQL test database is shared: ids of this run only,
		// removed afterwards
		suffix := strings.ReplaceAll(t.Name(), "/", "-")
		me, other, b, c := "CH6-ME-"+suffix, "CH6-OTHER-"+suffix, "CH6-B-"+suffix, "CH6-C-"+suffix
		t.Cleanup(func() {
			_ = database.DeleteChatHistory(me)
			_ = database.DeleteChatHistory(other)
		})
		for _, m := range []ChatMessage{
			{ConversationID: me, FromID: b, FromName: "b", Text: "1"},
			{ConversationID: me, FromID: b, FromName: "b", Text: "2"},
			{ConversationID: other, FromID: c, FromName: "c", ToID: me, Text: "3"},
			{ConversationID: me, FromID: "operator", FromName: "alice", ToID: me, Text: "4"},
			{ConversationID: me, FromID: "operator", FromName: "bob", ToID: me, Text: "5"},
			{ConversationID: me, FromID: me, Text: "own"},
			{ConversationID: me, FromID: c, FromName: "c", Read: true, Text: "read"},
			{ConversationID: other, FromID: b, FromName: "b", Text: "not mine"},
		} {
			if _, err := database.SaveChatMessage(&m); err != nil {
				t.Fatal(err)
			}
		}
		counts, err := database.GetUnreadBySender(me)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, u := range counts {
			got = append(got, fmt.Sprintf("%s/%s=%d", strings.TrimSuffix(u.FromID, "-"+suffix), u.FromName, u.Count))
		}
		sort.Strings(got)
		if want := "CH6-B/b=2 CH6-C/c=1 operator/alice=1 operator/bob=1"; strings.Join(got, " ") != want {
			t.Errorf("unread by sender: %q, want %q", strings.Join(got, " "), want)
		}
	})
}
