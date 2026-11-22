package db

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
)

// TestPanelSyncReadsCurrentSchema: on a PostgreSQL whose search_path names
// another schema than public, the panel sync reads the device groups,
// their members, the direct grants and the restricted default from that
// schema, where Migrate and the console write them (BUGS RV72).
func TestPanelSyncReadsCurrentSchema(t *testing.T) {
	name := pgFreshDatabase(t)
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, pgTestDSN(t, name, nil))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `CREATE SCHEMA rvschema`); err != nil {
		t.Fatal(err)
	}
	conn.Close(ctx)

	database := openTestPostgres(t, pgTestDSN(t, name, map[string]string{"search_path": "rvschema"}))
	// The console's tables of the grants and the settings
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS user_peer_grants (user_id INTEGER NOT NULL, peer_id TEXT NOT NULL, PRIMARY KEY (user_id, peer_id))`,
		`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT)`,
		`INSERT INTO settings (key, value) VALUES ('device_scope_default', 'restricted')`,
	} {
		if _, err := database.pool.Exec(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	var schema string
	if err := database.pool.QueryRow(ctx, `SELECT current_schema()`).Scan(&schema); err != nil || schema != "rvschema" {
		t.Fatalf("current schema %q, %v", schema, err)
	}

	group := &DeviceGroup{Name: "RV72 group", SourceType: "manual"}
	if err := database.CreateDeviceGroup(group); err != nil {
		t.Fatal(err)
	}
	if err := database.SetDeviceGroupPeers(group.GUID, []string{"RV72PEER"}); err != nil {
		t.Fatal(err)
	}
	user := &User{Username: "rv72user", PasswordHash: "x", Role: "viewer"}
	if err := database.CreateUser(user); err != nil {
		t.Fatal(err)
	}
	if err := database.SetUserPeerGrants(user.ID, []string{"RV72GRANT"}); err != nil {
		t.Fatal(err)
	}

	groups, err := database.ListPanelDeviceGroups()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, g := range groups {
		names = append(names, g.Name)
	}
	if got := strings.Join(names, ","); got != "RV72 group" {
		t.Errorf("device groups %q, want the group of this schema", got)
	}
	if members, err := database.ListDeviceGroupPeerIDs(group.GUID); err != nil || strings.Join(members, ",") != "RV72PEER" {
		t.Errorf("members %q, %v", members, err)
	}
	if grants, err := database.ListUserPeerGrants(user.ID); err != nil || strings.Join(grants, ",") != "RV72GRANT" {
		t.Errorf("grants %q, %v", grants, err)
	}
	if !database.DeviceScopeDefaultRestricted() {
		t.Error("the restricted default of this schema's settings was not read")
	}
}
