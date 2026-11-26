package backup

import (
	"context"
	"strings"
	"testing"
)

// A backup made before audit_log.target_user_id existed restores after
// the update: its entries come back about no account, and the entries of
// a newer backup keep the account they are about.
func TestAuditEntriesFromBeforeTheTargetColumnRestore(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			if dialect == "postgres" {
				needPostgres(t)
			}
			ctx := context.Background()
			in := newInstall(t, dialect)
			dump := parsedJSON(t, `{"tables":{"audit_log":[`+
				`{"id":1,"user_id":50,"action":"login","details":"older","ip_address":"127.0.0.1","created_at":"2026-11-18 14:58:36"},`+
				`{"id":2,"user_id":50,"action":"user_disabled","details":"newer","ip_address":"127.0.0.1","created_at":"2026-11-18 14:58:37","target_user_id":51}`+
				`]}}`)
			res, err := ImportAllTables(ctx, in.db, dump)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Join(res.Restored, ",") != "audit_log" || len(res.Warnings) != 0 {
				t.Fatalf("result %+v", res)
			}
			got := map[string]string{}
			rows, err := in.db.Query(ctx, `SELECT details, COALESCE(CAST(target_user_id AS TEXT), 'none') FROM audit_log`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			for rows.Next() {
				var details, target string
				if err := rows.Scan(&details, &target); err != nil {
					t.Fatal(err)
				}
				got[details] = target
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if len(got) != 2 || got["older"] != "none" || got["newer"] != "51" {
				t.Errorf("restored entries and their accounts: %v", got)
			}
		})
	}
}
