package db

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestListChatGroupsWholeMember: a member's groups are the groups that
// have the id as one whole member of the list. A part of a member's id,
// LIKE wildcards and another case match nothing (BUGS RV69).
func TestListChatGroupsWholeMember(t *testing.T) {
	panelGroupStores(t, func(t *testing.T, database Database) {
		// The PostgreSQL test database is shared: ids of this run only,
		// removed afterwards
		run := fmt.Sprintf("%d", time.Now().UnixNano())
		a, b, c := "RVAAA"+run, "RVBBB"+run, "RVCCC"+run
		groups := map[string]string{
			"group:rv69-ab-" + run: a + "," + b,
			"group:rv69-bc-" + run: b + "," + c,
		}
		for id, members := range groups {
			if err := database.CreateChatGroup(&ChatGroup{ID: id, Name: id, Members: members}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = database.DeleteChatGroup(id) })
		}
		mine := func(member string) string {
			t.Helper()
			list, err := database.ListChatGroups(member)
			if err != nil {
				t.Fatalf("ListChatGroups(%q): %v", member, err)
			}
			var out []string
			for _, g := range list {
				if _, ok := groups[g.ID]; ok {
					out = append(out, strings.TrimSuffix(strings.TrimPrefix(g.ID, "group:rv69-"), "-"+run))
				}
			}
			slices.Sort(out)
			return strings.Join(out, " ")
		}
		for member, want := range map[string]string{
			a:                     "ab",
			b:                     "ab bc",
			c:                     "bc",
			"______":              "",
			"%":                   "",
			"RV%" + run:           "",
			"RV___" + run:         "",
			a[:8]:                 "",
			run:                   "",
			strings.ToLower(a):    "",
			a + "," + b:           "",
			"RVAAA" + run[:6]:     "",
			"RVAAA" + run + "0":   "",
			"\\" + a:              "",
			"RVAAA" + run + ",RV": "",
		} {
			if got := mine(member); got != want {
				t.Errorf("groups of %q: %q, want %q", member, got, want)
			}
		}
	})
}
