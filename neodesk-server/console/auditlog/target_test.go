package auditlog

import (
	"context"
	"database/sql"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
)

// An entry about an account keeps that account in target_user_id next to
// the account that acted; a plain entry leaves it NULL. The migrated
// schema has the column.
func TestEntryAboutAnAccount(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	for _, stmt := range []string{
		`INSERT INTO users (id, username, password_hash, role) VALUES (5, 'five', 'x', 'admin')`,
		`INSERT INTO users (id, username, password_hash, role) VALUES (7, 'seven', 'x', 'viewer')`,
	} {
		if _, err := db.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	log := New(db)
	if err := log.WriteAbout(ctx, 5, 7, "user_unlocked", "Unlocked user: seven", "10.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if err := log.Write(ctx, 5, "login", "Signed in", "10.0.0.1"); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(ctx, `SELECT action, user_id, target_user_id FROM audit_log ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var action string
		var user, target sql.NullInt64
		if err := rows.Scan(&action, &user, &target); err != nil {
			t.Fatal(err)
		}
		row := action
		if user.Valid {
			row += " by " + itoa(user.Int64)
		}
		if target.Valid {
			row += " about " + itoa(target.Int64)
		}
		got = append(got, row)
	}
	if len(got) != 2 || got[0] != "user_unlocked by 5 about 7" || got[1] != "login by 5" {
		t.Fatalf("rows %q", got)
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}
