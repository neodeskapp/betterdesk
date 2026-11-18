package remoterelay

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

type peerRecording struct {
	Rows  [][2]json.RawMessage `json:"rows"`
	Steps []struct {
		Name   string                     `json:"name"`
		ID     string                     `json:"id"`
		Device map[string]json.RawMessage `json:"device"`
		Error  string                     `json:"error"`
	} `json:"steps"`
	Table []map[string]any `json:"table"`
}

// TestNodePeerLookups replays testdata/node_peer_*.json: the same rows in
// the console's table, the lookups before and after the signal server's
// table exists, and what the console's table holds afterwards. Each
// device must have the fields parsePeer gave it on that database, and a
// lookup that threw must fail.
func TestNodePeerLookups(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		name := "node_peer_sqlite.json"
		if db.IsPostgres() {
			name = "node_peer_postgres.json"
		}
		raw, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		var rec peerRecording
		if err := json.Unmarshal(raw, &rec); err != nil {
			t.Fatal(err)
		}
		for _, row := range rec.Rows {
			var id string
			var cols map[string]any
			if err := json.Unmarshal(row[0], &id); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(row[1], &cols); err != nil {
				t.Fatal(err)
			}
			names := make([]string, 0, len(cols))
			for k := range cols {
				names = append(names, k)
			}
			sort.Strings(names)
			args := []any{id}
			quoted := make([]string, len(names))
			for i, k := range names {
				quoted[i] = `"` + k + `"`
				v := cols[k]
				if f, ok := v.(float64); ok && f == math.Trunc(f) && k != "info" {
					v = int64(f)
				}
				args = append(args, v)
			}
			query := "INSERT INTO peer (id, " + strings.Join(quoted, ", ") + ") VALUES (?" + strings.Repeat(", ?", len(names)) + ")"
			if _, err := db.Exec(ctx, query, args...); err != nil {
				t.Fatalf("%s %v: %v", query, args, err)
			}
		}
		yes, no := db.Pick("1", "TRUE"), db.Pick("0", "FALSE")
		peersMade := false
		for _, st := range rec.Steps {
			if !strings.HasPrefix(st.Name, "no peers table") && !peersMade {
				peersMade = true
				storetest.GoServer(t, db)
				for _, statement := range []string{
					`INSERT INTO peers (id, uuid, hostname, os, version, ip, "user", status, banned, ban_reason` + db.Pick(", tags", "") + `) VALUES ('S1', 'su-1', 'from-signal', 'Linux', '1.4', '9.9.9.9', 'bob', 'ONLINE', ` + no + `, 'r'` + db.Pick(", 'x,y'", "") + `)`,
					`INSERT INTO peers (id, hostname, soft_deleted) VALUES ('S2', 'deleted there', ` + yes + `)`,
					`INSERT INTO peers (id, hostname, os, banned) VALUES ('P12', 'back from deleted', 'BSD', ` + yes + `)`,
				} {
					if _, err := db.Exec(ctx, statement); err != nil {
						t.Fatalf("%s: %v", statement, err)
					}
				}
			}
			device, err := PeerByID(ctx, db, st.ID)
			if st.Error != "" {
				if err == nil || err.Error() != st.Error {
					t.Errorf("%s: %v, want the error %q", st.Name, err, st.Error)
				}
				continue
			}
			if err != nil {
				t.Errorf("%s: %v", st.Name, err)
				continue
			}
			if st.Device == nil {
				if device != nil {
					t.Errorf("%s: %v, want no device", st.Name, device)
				}
				continue
			}
			if device == nil {
				t.Errorf("%s: no device, want %v", st.Name, st.Device)
				continue
			}
			for key, want := range st.Device {
				if key == "pk" {
					continue
				}
				got, _ := json.Marshal(device.Get(key))
				if text, ok := device.Get(key).(string); ok {
					// Half of a surrogate pair is kept as JavaScript had it
					if quoted := string(appendQuoted(nil, text)); quoted != string(want) {
						t.Errorf("%s %s: %s, want %s", st.Name, key, quoted, want)
					}
					continue
				}
				if fmt.Sprint(jsonValue(t, got)) != fmt.Sprint(jsonValue(t, want)) {
					t.Errorf("%s %s: %s, want %s", st.Name, key, got, want)
				}
			}
		}
		// What the lookups copied into the console's table
		cols := `id, note, ` + db.Pick("info", "info::text") + `, status_online, is_banned, is_deleted, ` + db.Pick("tags, ", "") + `"user", uuid`
		rows, err := db.Query(ctx, "SELECT "+cols+" FROM peer ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		names, _ := rows.Columns()
		var got []map[string]any
		for rows.Next() {
			values := make([]any, len(names))
			dest := make([]any, len(names))
			for i := range values {
				dest[i] = &values[i]
			}
			if err := rows.Scan(dest...); err != nil {
				t.Fatal(err)
			}
			row := map[string]any{}
			for i, n := range names {
				switch v := values[i].(type) {
				case int64:
					row[n] = float64(v)
				case []byte:
					row[n] = string(v)
				default:
					row[n] = v
				}
			}
			row["info"] = row[names[2]]
			delete(row, "info::text")
			got = append(got, row)
		}
		if fmt.Sprint(got) != fmt.Sprint(rec.Table) {
			t.Errorf("the console's table:\n   go %v\n node %v", got, rec.Table)
		}
	})
}

func jsonValue(t *testing.T, raw []byte) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return v
}
