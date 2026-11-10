package devicegroups_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/devicegroups"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// peersRecording is what testdata/nodeoracle/peers.js recorded.
type peersRecording struct {
	Dialect string            `json:"dialect"`
	Seed    []json.RawMessage `json:"seed"`
	Steps   []struct {
		Op     string  `json:"op"`
		SQL    string  `json:"sql"`
		Result *string `json:"result"`
		Error  *string `json:"error"`
	} `json:"steps"`
}

// rowsJSON returns the rows of a query as JSON objects with the columns
// in order, the values as the Node drivers gave them for these columns.
func rowsJSON(t *testing.T, db *store.DB, query string) string {
	t.Helper()
	res, err := db.Query(context.Background(), query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer res.Close()
	columns, _ := res.Columns()
	var out []string
	for res.Next() {
		cells := make([]jsbind.Cell, len(columns))
		dest := make([]any, len(columns))
		for i := range cells {
			dest[i] = &cells[i]
		}
		if err := res.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		parts := make([]string, len(columns))
		for i, column := range columns {
			name, _ := json.Marshal(column)
			value, _ := cells[i].MarshalJSON()
			parts[i] = string(name) + ":" + string(value)
		}
		out = append(out, "{"+strings.Join(parts, ",")+"}")
	}
	return "[" + strings.Join(out, ",") + "]"
}

// TestPeerSyncAgainstNode replays the recordings of the Node adapter's
// reads that copy the server's devices into the console's table first
// (getPeerStats and getAllPeers with syncGoPeers): new, refreshed and kept
// rows, a ghost of an id change deleted, a rename carried into the side
// tables, and no second copy within 30 seconds. The counts, the lists and
// the rows of peer afterwards must be Node's, byte for byte.
func TestPeerSyncAgainstNode(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		raw, err := os.ReadFile(filepath.Join("testdata", "peers_"+db.Dialect().String()+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var rec peersRecording
		if err := json.Unmarshal(raw, &rec); err != nil {
			t.Fatal(err)
		}
		for _, seed := range rec.Seed {
			var pair struct {
				SQL    string
				Params []any
			}
			list := []any{&pair.SQL, &pair.Params}
			if err := json.Unmarshal(seed, &list); err != nil {
				t.Fatal(err)
			}
			exec(t, db, pair.SQL, pair.Params...)
		}
		s := devicegroups.New(devicegroups.Options{DB: db, Access: access.New(db, nil, access.Options{})})
		ctx := context.Background()
		for i, step := range rec.Steps {
			var got string
			switch step.Op {
			case "getPeerStats":
				stats, err := s.PeerStats(ctx)
				if err != nil {
					t.Fatalf("step %d: %v", i, err)
				}
				got = string(mustJSON(t, stats))
			case "getAllPeers":
				peers, err := s.AllPeers(ctx)
				if err != nil {
					t.Fatalf("step %d: %v", i, err)
				}
				got = string(mustJSON(t, peers))
			case "rows":
				got = rowsJSON(t, db, step.SQL)
			case "exec":
				exec(t, db, step.SQL)
				continue
			default:
				t.Fatalf("step %d: unknown op %s", i, step.Op)
			}
			if step.Result == nil || got != *step.Result {
				t.Errorf("step %d %s:\n got %s\nwant %v", i, step.Op, got, *step.Result)
			}
		}
	})
}

// TestPeerSyncRunsOncePer30Seconds checks the clock of the copy: a read
// copies, a read within 30 seconds does not, a read after does.
func TestPeerSyncRunsOncePer30Seconds(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	exec(t, db, `CREATE TABLE peers (id TEXT PRIMARY KEY, uuid TEXT DEFAULT '', pk BLOB, ip TEXT DEFAULT '', user TEXT DEFAULT '',
		hostname TEXT DEFAULT '', os TEXT DEFAULT '', version TEXT DEFAULT '', status TEXT DEFAULT 'OFFLINE', last_online TEXT,
		created_at TEXT, banned INTEGER DEFAULT 0, banned_at TEXT, ban_reason TEXT DEFAULT '', soft_deleted INTEGER DEFAULT 0, tags TEXT DEFAULT '')`)
	exec(t, db, `INSERT INTO peers (id, status, created_at) VALUES ('A', 'ONLINE', '2020-01-01 00:00:00')`)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s := devicegroups.New(devicegroups.Options{DB: db, Access: access.New(db, nil, access.Options{}), Now: func() time.Time { return now }})
	count := func() float64 {
		stats, err := s.PeerStats(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return stats.Get("total").(float64)
	}
	if got := count(); got != 1 {
		t.Fatalf("first read: %v devices, want 1", got)
	}
	exec(t, db, `INSERT INTO peers (id, created_at) VALUES ('B', '2020-01-01 00:00:00')`)
	now = now.Add(29 * time.Second)
	if got := count(); got != 1 {
		t.Errorf("read after 29 seconds: %v devices, want the copy of the first read (1)", got)
	}
	now = now.Add(time.Second)
	if got := count(); got != 2 {
		t.Errorf("read after 30 seconds: %v devices, want 2", got)
	}
}
