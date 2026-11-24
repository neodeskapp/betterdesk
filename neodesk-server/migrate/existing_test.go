package migrate

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIntoPostgresCountsRowsThatWereThere: rust2go into PostgreSQL leaves
// a peer that is there already as it was, and counts it apart from the
// rows it copied (BUGS L137).
func TestIntoPostgresCountsRowsThatWereThere(t *testing.T) {
	var logs bytes.Buffer
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)
	dir := t.TempDir()
	peers := []string{
		`INSERT INTO peer (guid, id, uuid, pk, info) VALUES (x'01', '111111111', x'02', x'03', '{}')`,
		`INSERT INTO peer (guid, id, uuid, pk, info) VALUES (x'04', '222222222', x'05', x'06', '{}')`,
	}

	rust := filepath.Join(dir, "rust.sqlite3")
	execSQLite(t, rust, append([]string{rustPeerTable}, peers...)...)
	dsn := pgDSN(t, `INSERT INTO peers (id) VALUES ('111111111')`)
	if err := runRust2Go(rust, dsn, false); err != nil {
		t.Fatal(err)
	}
	if want := "Migration complete: 1 peers copied to PostgreSQL, 1 already there and left as they were"; !strings.Contains(logs.String(), want) {
		t.Errorf("rust2go's log has no line %q:\n%s", want, logs.String())
	}
}
