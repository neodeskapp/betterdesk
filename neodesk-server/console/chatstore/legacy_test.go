package chatstore

import (
	"context"
	"slices"
	"testing"
)

// legacyRow is a message as the chat of version 1 stored it.
type legacyRow struct{ conv, fromID, fromName, text string }

// legacyHistory writes the accounts, the groups and the messages of a
// console that ran the chat of version 1, and returns the messages' ids by
// their text. The read baseline is the newest of them, as on the first
// start of version 2.
func legacyHistory(t *testing.T, s *Store, rows []legacyRow) map[string]int64 {
	t.Helper()
	ctx := context.Background()
	for _, a := range []struct {
		id   int64
		name string
	}{{5, "anna"}, {6, "ben"}, {8, "cleo"}} {
		if _, err := s.db.Exec(ctx, `INSERT INTO users (id, username, password_hash, role) VALUES (?, ?, 'x', 'operator')`, a.id, a.name); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(ctx, `CREATE TABLE IF NOT EXISTS chat_groups (id TEXT PRIMARY KEY, name TEXT NOT NULL, members TEXT DEFAULT '', created_by TEXT DEFAULT '')`); err != nil {
		t.Fatal(err)
	}
	for _, g := range [][4]string{
		{"group:aaaa1111", "Night shift", "operator:anna,operator:ben,dev1", "anna"},
		{"group:bbbb2222", "Just me", "operator:anna,dev1", "anna"},
	} {
		if _, err := s.db.Exec(ctx, `INSERT INTO chat_groups (id, name, members, created_by) VALUES (?, ?, ?, ?)`, g[0], g[1], g[2], g[3]); err != nil {
			t.Fatal(err)
		}
	}
	ids := map[string]int64{}
	for _, r := range rows {
		ids[r.text] = must(s.db.Insert(ctx, `INSERT INTO chat_messages (conversation_id, from_id, from_name, text) VALUES (?, ?, ?, ?)`,
			r.conv, r.fromID, r.fromName, r.text))
	}
	if _, err := s.db.Exec(ctx, `UPDATE chat_settings SET value = (SELECT CAST(MAX(id) AS TEXT) FROM chat_messages) WHERE key = 'read_baseline'`); err != nil {
		t.Fatal(err)
	}
	return ids
}

// TestLegacyHistoryIsImported: the first start of version 2 moves the
// chat of version 1 into its conversations (the lead's call on question 3):
// a direct message ("operator:<name>", which every chat user received
// before L492) into the direct conversation of its two people only; a
// group into a group of its accounts; what a computer wrote without a
// conversation into the computer's; what maps to no two people into an
// "old messages" conversation only admins read. It runs once.
func TestLegacyHistoryIsImported(t *testing.T) {
	each(t, func(t *testing.T, s *Store, _ *clock) {
		ctx := context.Background()
		ids := legacyHistory(t, s, []legacyRow{
			{"operator:ben", "operator", "anna", "anna to ben"},
			{"operator:anna", "operator", "ben", "ben to anna"},
			{"operator:ben", "operator:cleo", "cleo", "cleo to ben"},
			{"operator:ghost", "operator", "anna", "anna to someone gone"},
			{"operator:anna", "operator", "anna", "anna to herself"},
			{"operator", "dev1", "reception-01", "a computer without a conversation"},
			{"operator", "operator", "anna", "anna into the void"},
			{"group:aaaa1111", "operator", "anna", "night shift starts"},
			{"group:aaaa1111", "dev1", "reception-01", "the computer in the group"},
			{"group:bbbb2222", "operator", "anna", "a group of one"},
			{"group:cccc3333", "operator", "ben", "a group that is gone"},
			{"dev2", "operator", "ben", "a support message stays"},
		})
		report := must(s.ImportLegacy(ctx))

		where := func(text string) string {
			return must(s.Get(ctx, ids[text])).ConversationID
		}
		for text, want := range map[string]string{
			"anna to ben":                       "dm:5:6",
			"ben to anna":                       "dm:5:6",
			"cleo to ben":                       "dm:6:8",
			"anna to someone gone":              "old:operator:ghost",
			"anna to herself":                   "old:operator:anna",
			"a computer without a conversation": "dev1",
			"anna into the void":                "old:operator",
			"night shift starts":                "group:aaaa1111",
			"the computer in the group":         "group:aaaa1111",
			"a group of one":                    "old:group:bbbb2222",
			"a group that is gone":              "old:group:cccc3333",
			"a support message stays":           "dev2",
		} {
			if got := where(text); got != want {
				t.Errorf("%q is in %q, want %q", text, got, want)
			}
		}
		if from := must(s.Get(ctx, ids["anna to ben"])).From; from != anna {
			t.Errorf("anna's message is from %+v, want her account", from)
		}

		// The direct conversations are their two people's only
		for conv, want := range map[string][]int64{"dm:5:6": {5, 6}, "dm:6:8": {6, 8}, "group:aaaa1111": {5, 6}} {
			if got := must(s.Members(ctx, conv)); !slices.Equal(got, want) {
				t.Errorf("members of %s: %v, want %v", conv, got, want)
			}
		}
		if in := must(s.IsMember(ctx, "dm:5:6", 8)); in {
			t.Error("cleo is in anna's and ben's conversation")
		}
		group := must(s.Conversation(ctx, "group:aaaa1111"))
		if group.Title != "Night shift" || group.CreatedBy != 5 || group.LastMessageID != ids["the computer in the group"] {
			t.Errorf("the group: %+v", group)
		}
		dm := must(s.Conversation(ctx, "dm:5:6"))
		if dm.Kind != ConvDirect || dm.LastMessageID != ids["ben to anna"] {
			t.Errorf("the direct conversation: %+v", dm)
		}

		// What maps to nobody is kept, under a name that says where it was
		old := must(s.List(ctx, Filter{Kinds: []string{ConvOld}}))
		titles := map[string]string{}
		for _, c := range old {
			titles[c.ID] = c.Title
		}
		want := map[string]string{"old:operator:ghost": "ghost", "old:operator:anna": "anna", "old:operator": "operator",
			"old:group:bbbb2222": "Just me", "old:group:cccc3333": "group:cccc3333"}
		if len(titles) != len(want) {
			t.Errorf("old conversations %v, want %v", titles, want)
		}
		for id, title := range want {
			if titles[id] != title {
				t.Errorf("old conversation %s is called %q, want %q", id, titles[id], title)
			}
		}
		if must(s.List(ctx, Filter{Kinds: []string{ConvGroup}, Member: 0}))[0].ID != "group:aaaa1111" {
			t.Error("the group of one or the gone group is still a group")
		}

		// History before the upgrade stays read
		if n := must(s.Unread(ctx, ben, []string{"dm:5:6", "dm:6:8"})); n["dm:5:6"] != 0 || n["dm:6:8"] != 0 {
			t.Errorf("ben's unread after the import: %v", n)
		}
		if report.Direct != 3 || report.Groups != 2 || report.Devices != 1 || report.Old != 5 {
			t.Errorf("report %+v", report)
		}

		// Once: a second start moves nothing, not even what came since
		late := must(s.db.Insert(ctx, `INSERT INTO chat_messages (conversation_id, from_id, from_name, text) VALUES ('operator:ben', 'operator', 'anna', 'late')`))
		again := must(s.ImportLegacy(ctx))
		if again != (LegacyReport{}) {
			t.Errorf("a second import: %+v", again)
		}
		if got := must(s.Get(ctx, late)).ConversationID; got != "operator:ben" {
			t.Errorf("a second import moved a message to %s", got)
		}
	})
}

// TestLegacyImportWithoutHistory: a console that never chatted, or whose
// database has no groups table, imports nothing and is marked done.
func TestLegacyImportWithoutHistory(t *testing.T) {
	each(t, func(t *testing.T, s *Store, _ *clock) {
		ctx := context.Background()
		if _, err := s.db.Exec(ctx, `DROP TABLE IF EXISTS chat_groups`); err != nil {
			t.Fatal(err)
		}
		if r := must(s.ImportLegacy(ctx)); r != (LegacyReport{}) {
			t.Errorf("report %+v", r)
		}
		if v := must(s.Setting(ctx, keyLegacyImported)); v == "" {
			t.Error("the import is not marked done")
		}
	})
}
