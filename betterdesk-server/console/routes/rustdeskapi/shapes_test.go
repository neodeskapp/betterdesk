package rustdeskapi_test

import (
	"testing"

	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// A file transfer record is written as the table takes its flag, in each
// shape of audit_files: a boolean in the console's own PostgreSQL table,
// an integer in the API server's and on SQLite. The API server's
// PostgreSQL table refused the console's boolean (BUGS D5, DECISIONS
// A65).
func TestFileAuditOnEitherShape(t *testing.T) {
	storetest.EachOrigin(t, func(t *testing.T, db *store.DB, _ storetest.Origin) {
		w := world(t, db, nil)
		for _, flag := range []string{"true", "false"} {
			got := w.Do(replay.Request{Method: "POST", Path: "/api/audit/file", Body: replay.Text(`{"host_id":"900100001","path":"/tmp/x","is_file":` + flag + `}`)})
			if got.Status != 200 || got.Body != `{}` {
				t.Fatalf("is_file %s: %d %s", flag, got.Status, got.Body)
			}
		}
		const read = `SELECT CASE WHEN CAST(is_file AS TEXT) IN ('1', 'true') THEN 'file' ELSE 'folder' END AS kind FROM audit_files ORDER BY id`
		if got := w.Rows(read, nil); got != `[{"kind":"file"},{"kind":"folder"}]` {
			t.Errorf("stored %s", got)
		}
	})
}
