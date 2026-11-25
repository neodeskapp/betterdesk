package chatstore

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// clock is a test clock that moves when told.
type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }
func newClock() *clock               { return &clock{t: time.Date(2026, 11, 25, 9, 0, 0, 0, time.UTC)} }

// must is v, and a failed test (a panic) on an error.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

var (
	anna = User(5, "anna")
	ben  = User(6, "ben")
	cleo = User(8, "cleo")
	dev1 = Device("dev1", "reception-01")
)

// each runs fn on a fresh store on every database and every way the
// database came to be: made by the console, by the API server (which
// made chat_messages first) or by the old Node console.
func each(t *testing.T, fn func(t *testing.T, s *Store, c *clock)) {
	storetest.EachOrigin(t, func(t *testing.T, db *store.DB, _ storetest.Origin) {
		if err := Ensure(context.Background(), db); err != nil {
			t.Fatal(err)
		}
		if err := Ensure(context.Background(), db); err != nil {
			t.Fatalf("a second run: %v", err)
		}
		c := newClock()
		fn(t, New(db, c.now), c)
	})
}

func post(t *testing.T, s *Store, conv string, from Party, text string) Message {
	t.Helper()
	return must(s.Post(context.Background(), Message{ConversationID: conv, From: from, Text: text}, nil))
}

// TestReadStateIsPerAccount: what one account reads stays unread for
// every other account (L492: one read flag for everyone before), its own
// messages are never unread for it, and a mark only moves forward.
func TestReadStateIsPerAccount(t *testing.T) {
	each(t, func(t *testing.T, s *Store, _ *clock) {
		ctx := context.Background()
		post(t, s, "dev1", dev1, "my printer stopped")
		post(t, s, "dev1", dev1, "it blinks red")
		mine := post(t, s, "dev1", anna, "looking at it")

		count := func(who Party) int {
			return must(s.Unread(ctx, who, []string{"dev1"}))["dev1"]
		}
		if a, b := count(anna), count(ben); a != 2 || b != 3 {
			t.Fatalf("unread: anna %d, ben %d; want 2 (not her own) and 3", a, b)
		}
		if n := must(s.MarkRead(ctx, "dev1", ben, 0)); n != mine.ID {
			t.Errorf("ben's mark %d, want the newest %d", n, mine.ID)
		}
		if a, b := count(anna), count(ben); a != 2 || b != 0 {
			t.Errorf("after ben read: anna %d, ben %d; want 2 and 0", a, b)
		}
		// An older mark (another tab that was behind) doesn't move it back
		if n := must(s.MarkRead(ctx, "dev1", ben, 1)); n != mine.ID {
			t.Errorf("ben's mark went back to %d", n)
		}
		// The device reads the console's messages
		if d := count(dev1); d != 1 {
			t.Errorf("the device's unread: %d, want anna's one", d)
		}
	})
}

// TestHistoryBeforeTheUpgradeIsRead: messages stored before the chat of
// version 2 count as read for everyone and make closed conversations, so
// an update doesn't flood the inbox; what comes after counts.
func TestHistoryBeforeTheUpgradeIsRead(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		if err := Ensure(ctx, db); err != nil {
			t.Fatal(err)
		}
		// Ensure ran once on an empty table; this store's baseline is the
		// newest message of a database that had history
		for _, text := range []string{"old one", "old two"} {
			if _, err := db.Exec(ctx, `INSERT INTO chat_messages (conversation_id, from_id, from_name, text) VALUES ('dev1', 'dev1', 'reception-01', ?)`, text); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := db.Exec(ctx, `UPDATE chat_settings SET value = (SELECT CAST(MAX(id) AS TEXT) FROM chat_messages) WHERE key = 'read_baseline'`); err != nil {
			t.Fatal(err)
		}
		s := New(db, newClock().now)
		if n := must(s.Unread(ctx, anna, nil))["dev1"]; n != 0 {
			t.Errorf("old history unread: %d", n)
		}
		c := must(s.Conversation(ctx, "dev1"))
		if c.State != StateClosed || c.WaitingSinceMs != 0 {
			t.Errorf("old conversation: %+v, want closed and not waiting", c)
		}
		post(t, s, "dev1", dev1, "new")
		if n := must(s.Unread(ctx, anna, nil))["dev1"]; n != 1 {
			t.Errorf("unread after a new message: %d, want 1", n)
		}
		if c := must(s.Conversation(ctx, "dev1")); c.State != StateOpen || c.WaitingSinceMs == 0 {
			t.Errorf("after a new message: %+v, want open and waiting", c)
		}
	})
}

// TestEditAndDelete: a sender edits within 24 hours, nobody else does;
// the sender or an admin deletes, which leaves a deleted mark without
// text for everyone; the trail keeps every old text.
func TestEditAndDelete(t *testing.T) {
	each(t, func(t *testing.T, s *Store, c *clock) {
		ctx := context.Background()
		m := post(t, s, DirectID(5, 6), anna, "helo")
		if _, err := s.Edit(ctx, m.ID, ben, "hijacked"); !errors.Is(err, ErrNotAllowed) {
			t.Errorf("ben edits anna's message: %v", err)
		}
		edited := must(s.Edit(ctx, m.ID, anna, "hello"))
		if edited.Text != "hello" || edited.EditedMs == 0 {
			t.Errorf("edited: %+v", edited)
		}
		c.add(25 * time.Hour)
		if _, err := s.Edit(ctx, m.ID, anna, "too late"); !errors.Is(err, ErrTooLate) {
			t.Errorf("an edit after 25 hours: %v", err)
		}
		if _, err := s.Delete(ctx, m.ID, ben, false); !errors.Is(err, ErrNotAllowed) {
			t.Errorf("ben deletes anna's message: %v", err)
		}
		gone := must(s.Delete(ctx, m.ID, anna, false))
		if !gone.Deleted() || gone.Text != "" {
			t.Errorf("deleted: %+v", gone)
		}
		if _, err := s.Delete(ctx, m.ID, anna, false); !errors.Is(err, ErrDeleted) {
			t.Errorf("a second delete: %v", err)
		}
		other := post(t, s, DirectID(5, 6), ben, "secret")
		if _, err := s.Delete(ctx, other.ID, cleo, true); err != nil {
			t.Errorf("an admin deletes: %v", err)
		}
		history := must(s.History(ctx, DirectID(5, 6), 0, 50))
		if len(history) != 2 || !history[0].Deleted() || history[0].Text != "" || !history[1].Deleted() {
			t.Errorf("history after deletes: %+v", history)
		}
		trail := must(s.Trail(ctx, m.ID))
		if len(trail) != 2 || trail[0].Action != "edit" || trail[0].OldText != "helo" || trail[1].Action != "delete" || trail[1].OldText != "hello" || trail[1].Actor != anna {
			t.Errorf("trail: %+v", trail)
		}
		if trail := must(s.Trail(ctx, other.ID)); len(trail) != 1 || trail[0].OldText != "secret" || trail[0].Actor.Name != "cleo" {
			t.Errorf("the admin's delete in the trail: %+v", trail)
		}
		// Deleted messages don't count as unread
		if n := must(s.Unread(ctx, cleo, nil))[DirectID(5, 6)]; n != 0 {
			t.Errorf("deleted messages unread: %d", n)
		}
	})
}

// TestSupportConversation: a device's message opens the conversation and
// starts the wait, an answer ends it, a closed conversation opens again
// without its assignee when the person writes again, and the waits past a
// time are listed once until mailed.
func TestSupportConversation(t *testing.T) {
	each(t, func(t *testing.T, s *Store, c *clock) {
		ctx := context.Background()
		first := post(t, s, "dev1", dev1, "help")
		conv := must(s.Conversation(ctx, "dev1"))
		if conv.Kind != ConvSupport || conv.State != StateOpen || conv.WaitingSinceMs != first.CreatedMs || conv.LastMessageID != first.ID {
			t.Fatalf("after the device wrote: %+v", conv)
		}
		c.add(20 * time.Minute)
		due := must(s.Unanswered(ctx, c.now().Add(-15*time.Minute).UnixMilli()))
		if len(due) != 1 || due[0].ID != "dev1" {
			t.Errorf("unanswered: %+v", due)
		}
		if err := s.MarkMailed(ctx, "dev1"); err != nil {
			t.Fatal(err)
		}
		if due := must(s.Unanswered(ctx, c.now().UnixMilli())); len(due) != 0 {
			t.Errorf("unanswered after the mail: %+v", due)
		}
		must(s.Assign(ctx, "dev1", 5))
		post(t, s, "dev1", anna, "on it")
		if conv := must(s.Conversation(ctx, "dev1")); conv.WaitingSinceMs != 0 || conv.AssigneeID != 5 {
			t.Errorf("after the answer: %+v", conv)
		}
		closed := must(s.Close(ctx, "dev1", "anna", "printer driver"))
		if closed.State != StateClosed || closed.CloseNote != "printer driver" {
			t.Errorf("closed: %+v", closed)
		}
		if open := must(s.List(ctx, Filter{Kinds: []string{ConvSupport}, State: StateOpen})); len(open) != 0 {
			t.Errorf("open after closing: %+v", open)
		}
		post(t, s, "dev1", dev1, "again")
		conv = must(s.Conversation(ctx, "dev1"))
		if conv.State != StateOpen || conv.AssigneeID != 0 || conv.CloseNote != "" || conv.WaitingSinceMs == 0 {
			t.Errorf("after the person wrote again: %+v", conv)
		}
		mine := must(s.List(ctx, Filter{Kinds: []string{ConvSupport}, Assignee: 5}))
		unassigned := must(s.List(ctx, Filter{Kinds: []string{ConvSupport}, Assignee: -1, State: StateOpen}))
		if len(mine) != 0 || len(unassigned) != 1 {
			t.Errorf("mine %d, unassigned %d; want 0 and 1", len(mine), len(unassigned))
		}
		if _, err := s.Assign(ctx, DirectID(5, 6), 5); !errors.Is(err, ErrNotFound) {
			t.Errorf("assigning a direct conversation: %v", err)
		}
	})
}

// TestDirectAndGroups: a direct conversation has one id for both people
// whichever writes first, and only they are its members; a group's
// members are listed for them only.
func TestDirectAndGroups(t *testing.T) {
	each(t, func(t *testing.T, s *Store, _ *clock) {
		ctx := context.Background()
		a := must(s.Direct(ctx, 6, 5))
		if a.ID != "dm:5:6" || a.Kind != ConvDirect {
			t.Errorf("direct: %+v", a)
		}
		if ok := must(s.IsMember(ctx, "dm:5:6", 8)); ok {
			t.Error("cleo is a member of anna and ben's conversation")
		}
		if ok := must(s.IsMember(ctx, "dm:5:6", 6)); !ok {
			t.Error("ben is no member of his own conversation")
		}
		g := must(s.NewGroup(ctx, " Night shift ", 5, []int64{6}))
		if g.Title != "Night shift" || g.Kind != ConvGroup {
			t.Errorf("group: %+v", g)
		}
		if m := must(s.Members(ctx, g.ID)); !slices.Equal(m, []int64{5, 6}) {
			t.Errorf("members: %v", m)
		}
		post(t, s, g.ID, anna, "hi")
		post(t, s, "dm:5:6", ben, "yo")
		forCleo := must(s.List(ctx, Filter{Kinds: []string{ConvDirect, ConvGroup}, Member: 8}))
		forBen := must(s.List(ctx, Filter{Kinds: []string{ConvDirect, ConvGroup}, Member: 6}))
		if len(forCleo) != 0 || len(forBen) != 2 {
			t.Errorf("cleo sees %d, ben %d; want 0 and 2", len(forCleo), len(forBen))
		}
		if _, err := s.Direct(ctx, 5, 5); err == nil {
			t.Error("a direct conversation with oneself")
		}
	})
}

// TestReactionsAndReplies: reactions are kept per person and emoji, come
// back with the history and go with a second "off"; a reply names a
// message of its own conversation only.
func TestReactionsAndReplies(t *testing.T) {
	each(t, func(t *testing.T, s *Store, _ *clock) {
		ctx := context.Background()
		m := post(t, s, "dev1", dev1, "thanks")
		must(s.React(ctx, m.ID, anna, "👍", true))
		must(s.React(ctx, m.ID, anna, "👍", true))
		got := must(s.React(ctx, m.ID, ben, "👍", true))
		if who := got.Reactions["👍"]; len(who) != 2 || who[0].Name != "anna" {
			t.Errorf("reactions: %+v", got.Reactions)
		}
		got = must(s.React(ctx, m.ID, anna, "👍", false))
		if who := got.Reactions["👍"]; len(who) != 1 || who[0] != ben {
			t.Errorf("after anna took hers back: %+v", got.Reactions)
		}
		reply := must(s.Post(ctx, Message{ConversationID: "dev1", From: anna, Text: "you're welcome", ReplyTo: m.ID}, nil))
		elsewhere := must(s.Post(ctx, Message{ConversationID: "dev2", From: anna, Text: "x", ReplyTo: m.ID}, nil))
		if reply.ReplyTo != m.ID || elsewhere.ReplyTo != 0 {
			t.Errorf("reply to %d, from another conversation %d", reply.ReplyTo, elsewhere.ReplyTo)
		}
		history := must(s.History(ctx, "dev1", 0, 10))
		if len(history) != 2 || history[0].Reactions["👍"][0] != ben || history[1].ReplyTo != m.ID {
			t.Errorf("history: %+v", history)
		}
		if _, err := s.Post(ctx, Message{ConversationID: "dev1", From: anna, Text: "  "}, nil); !errors.Is(err, ErrEmpty) {
			t.Errorf("an empty message: %v", err)
		}
		file := must(s.Post(ctx, Message{ConversationID: "dev1", From: anna, Attachments: []Attachment{{ID: "ab", Name: "shot.png", Type: "image/png", Size: 3}}}, nil))
		if got := must(s.Get(ctx, file.ID)); got.Text != "" || len(got.Attachments) != 1 || got.Attachments[0].Name != "shot.png" {
			t.Errorf("a file without text: %+v", got)
		}
	})
}

// TestPostThroughTheAPIServer: a message posted through an InsertFunc (the
// relay's, which reaches CDAP agents) is stored with the API server's
// columns of version 1 and the details of version 2.
func TestPostThroughTheAPIServer(t *testing.T) {
	each(t, func(t *testing.T, s *Store, _ *clock) {
		ctx := context.Background()
		var calls []string
		insert := func(ctx context.Context, conv, fromID, fromName, toID, text string) (int64, error) {
			calls = append(calls, conv+"|"+fromID+"|"+fromName+"|"+toID+"|"+text)
			return s.db.Insert(ctx, `INSERT INTO chat_messages (conversation_id, from_id, from_name, to_id, text) VALUES (?, ?, ?, ?, ?)`, conv, fromID, fromName, toID, text)
		}
		m := must(s.Post(ctx, Message{ConversationID: "dev1", From: anna, Text: "hi", ClientID: "c1"}, insert))
		if !slices.Equal(calls, []string{"dev1|operator|anna|dev1|hi"}) {
			t.Errorf("API calls: %q", calls)
		}
		got := must(s.Get(ctx, m.ID))
		if got.From != anna || got.ClientID != "c1" || got.CreatedMs == 0 {
			t.Errorf("stored: %+v", got)
		}
	})
}

// TestCanned: an account sees the shared replies and its own, not other
// accounts' own.
func TestCanned(t *testing.T) {
	each(t, func(t *testing.T, s *Store, _ *clock) {
		ctx := context.Background()
		must(s.AddCanned(ctx, 0, 1, "Restart", "Please restart the computer."))
		must(s.AddCanned(ctx, 5, 5, "anna's", "Hi, Anna here."))
		must(s.AddCanned(ctx, 6, 6, "ben's", "Hi, Ben here."))
		list := must(s.CannedFor(ctx, 5))
		var titles []string
		for _, c := range list {
			titles = append(titles, c.Title)
		}
		if !slices.Equal(titles, []string{"anna's", "Restart"}) {
			t.Errorf("anna's replies: %q", titles)
		}
		if _, err := s.AddCanned(ctx, 0, 1, "", "x"); !errors.Is(err, ErrEmpty) {
			t.Errorf("a reply without a title: %v", err)
		}
	})
}
