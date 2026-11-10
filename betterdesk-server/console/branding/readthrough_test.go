package branding

import (
	"context"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// steppingClock moves on by one second at every reading, so a revision
// taken from the clock differs at every load.
type steppingClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *steppingClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(time.Second)
	return c.now
}

func readThroughService(t *testing.T, db *store.DB, readThrough bool) *Service {
	t.Helper()
	opts := fixtureOptions(t.TempDir())
	opts.DB = db
	opts.Now = (&steppingClock{now: fixedNow}).Now
	opts.Location = time.UTC
	opts.ReadThrough = readThrough
	s, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// The Node console behind the Go one saves into the same table and tells
// nobody. With ReadThrough the next read sees the save; without it the
// cache keeps what the last load read, as Node's did.
func TestRefreshSeesWhatAnotherProcessSaved(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		other := readThroughService(t, db, false) // stands in for Node
		for _, readThrough := range []bool{true, false} {
			t.Run("readThrough="+strconv.FormatBool(readThrough), func(t *testing.T) {
				if err := other.ResetBranding(ctx); err != nil {
					t.Fatal(err)
				}
				s := readThroughService(t, db, readThrough)
				s.LoadBranding(ctx)
				before := s.Revision()
				if err := other.SaveBranding(ctx, object("appName", "Saved Elsewhere")); err != nil {
					t.Fatal(err)
				}
				s.Refresh(ctx)
				name, revision := s.GetBranding().Text("appName"), s.Revision()
				if readThrough {
					if name != "Saved Elsewhere" || revision == before || revision != other.Revision() {
						t.Errorf("after the other's save: appName %q, revision %q (was %q, the other has %q)", name, revision, before, other.Revision())
					}
				} else if name == "Saved Elsewhere" || revision != before {
					t.Errorf("without ReadThrough Refresh read the database: appName %q, revision %q (was %q)", name, revision, before)
				}
			})
		}
	})
}

// With no stored setting the revision comes from the clock. Refresh keeps
// it while the table stays empty, takes the stored time once there is one,
// and a new time from the clock after the table was emptied again.
func TestRefreshKeepsTheRevisionOfAnEmptyTable(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s := readThroughService(t, db, true)
		other := readThroughService(t, db, false)
		s.LoadBranding(ctx)
		first := s.Revision()
		s.Refresh(ctx)
		s.Refresh(ctx)
		if got := s.Revision(); got != first {
			t.Errorf("revision of an empty table changed from %q to %q without a save", first, got)
		}
		if err := other.SaveBranding(ctx, object("appName", "X")); err != nil {
			t.Fatal(err)
		}
		s.Refresh(ctx)
		saved := s.Revision()
		if saved == first || saved != other.Revision() {
			t.Errorf("revision after a save = %q, want the stored time %q", saved, other.Revision())
		}
		if err := other.ResetBranding(ctx); err != nil {
			t.Fatal(err)
		}
		s.Refresh(ctx)
		emptied := s.Revision()
		if emptied == saved || emptied == first {
			t.Errorf("revision after a reset = %q, want a new one (saved %q, first %q)", emptied, saved, first)
		}
		if s.GetBranding().Text("appName") != DefaultBranding().Text("appName") {
			t.Errorf("appName after a reset = %q", s.GetBranding().Text("appName"))
		}
		s.Refresh(ctx)
		if got := s.Revision(); got != emptied {
			t.Errorf("revision after a reset changed from %q to %q without a save", emptied, got)
		}
		// LoadBranding itself takes a new time, as Node's loadBranding did
		s.LoadBranding(ctx)
		if got := s.Revision(); got == emptied {
			t.Errorf("LoadBranding kept the revision %q of an empty table", got)
		}
	})
}

func TestNoPalettesFile(t *testing.T) {
	opts := fixtureOptions(t.TempDir())
	opts.PalettesFile = ""
	s, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := Stringify(s.BootPalettes()); got != `{"light":{},"dark":{}}` {
		t.Errorf("palettes without a file = %s", got)
	}
	opts.PalettesFile = opts.ThemesDir + "/missing.json"
	if _, err := New(opts); err == nil {
		t.Error("New took a palettes file that does not exist")
	}
}
