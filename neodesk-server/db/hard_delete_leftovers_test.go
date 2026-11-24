package db

import (
	"strings"
	"testing"
)

// deviceKeys are the server_config keys the server keeps per device:
// credentials and bindings of that install.
var deviceKeys = []string{
	"device_public_key_", "device_key_request_", "device_key_refused_", "device_totp_secret_",
	"device_display_name_", "device_sync_mode_", "device_bundle_id_", "cdap_manifest_",
	"mesh_group_id_", "quick_support_device_", "telemetry_seq_",
}

// deviceRows are the tables with a row per device that a new install
// with the same ID must not inherit: stored passwords, access, the
// organization and the connection policy.
var deviceRows = []struct{ table, column, insert string }{
	{"org_peer_credentials", "peer_id", `INSERT INTO org_peer_credentials (org_id, peer_id, ciphertext, nonce) VALUES ('org-1', ?, 'c', 'n')`},
	{"org_devices", "device_id", `INSERT INTO org_devices (org_id, device_id) VALUES ('org-1', ?)`},
	{"access_policies", "peer_id", `INSERT INTO access_policies (peer_id, password_hash) VALUES (?, 'h')`},
	{"user_peer_grants", "peer_id", `INSERT INTO user_peer_grants (user_id, peer_id) VALUES (1, ?)`},
	{"device_group_members", "peer_id", `INSERT INTO device_group_members (device_group_id, peer_id) VALUES (1, ?)`},
	{"device_folder_assignments", "device_id", `INSERT INTO device_folder_assignments (device_id, folder_id) VALUES (?, 1)`},
	{"device_connection_policies", "device_id", `INSERT INTO device_connection_policies (device_id, device_uuid, desired_mode, revision) VALUES (?, 'u', 'attended', 1)`},
	{"device_telemetry_snapshots", "device_id", `INSERT INTO device_telemetry_snapshots (device_id, kind) VALUES (?, 'os')`},
	{"device_telemetry_commands", "device_id", `INSERT INTO device_telemetry_commands (device_id, command, expires_at) VALUES (?, 'ping', '2030-01-01T00:00:00Z')`},
	{"peer_metrics", "peer_id", `INSERT INTO peer_metrics (peer_id) VALUES (?)`},
}

func rowCount(t *testing.T, store Database, table, column, id string) int {
	t.Helper()
	var n int
	var err error
	switch d := store.(type) {
	case *SQLiteDB:
		err = d.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+column+` = ?`, id).Scan(&n)
	case *PostgresDB:
		err = d.pool.QueryRow(d.ctx, `SELECT COUNT(*) FROM `+table+` WHERE `+column+` = $1`, id).Scan(&n)
	}
	if err != nil {
		t.Fatalf("count %s of %s: %v", table, id, err)
	}
	return n
}

// TestHardDeleteLeavesNothingOfTheInstall: a permanent delete removes what
// the server keeps of the device's install: its public key (which a new
// install with the same ID must not be checked against), its key request
// and refusal, second factor secret, name, sync mode, bundle, manifest,
// mesh group, quick support mark, stored passwords, access grants, groups,
// folder, organization and connection policy (L464). Another device keeps
// its own, a soft delete keeps everything (the device can come back), and
// the device's audit history stays.
func TestHardDeleteLeavesNothingOfTheInstall(t *testing.T) {
	freshStores(t, func(t *testing.T, store Database) {
		rawExec(t, store, `INSERT INTO organizations (id, name, slug) VALUES ('org-1', 'Org', 'org')`)
		rawExec(t, store, `INSERT INTO device_groups (id, guid, name) VALUES (1, 'g-1', 'Group')`)
		rawExec(t, store, `INSERT INTO folders (id, name) VALUES (1, 'Folder')`)
		rawExec(t, store, `INSERT INTO users (id, username, password_hash, role) VALUES (1, 'ann', 'x', 'operator')`)
		for _, id := range []string{"GONE1", "KEPT1"} {
			if err := store.UpsertPeer(&Peer{ID: id, Status: "ONLINE"}); err != nil {
				t.Fatal(err)
			}
			for _, prefix := range deviceKeys {
				if err := store.SetConfig(prefix+id, "value of "+id); err != nil {
					t.Fatal(err)
				}
			}
			for _, r := range deviceRows {
				rawExec(t, store, r.insert, id)
			}
			rawExec(t, store, `INSERT INTO audit_connections (host_id, peer_id, action) VALUES ('HOST1', ?, 'conn_start')`, id)
		}

		if err := store.DeletePeer("GONE1"); err != nil {
			t.Fatal(err)
		}
		if v, _ := store.GetConfig("device_public_key_GONE1"); v == "" {
			t.Error("a soft delete removed the device's key: the device could not come back")
		}
		if err := store.HardDeletePeer("GONE1"); err != nil {
			t.Fatal(err)
		}

		var left []string
		for _, prefix := range deviceKeys {
			if v, _ := store.GetConfig(prefix + "GONE1"); v != "" {
				left = append(left, "server_config "+prefix+"GONE1")
			}
			if v, _ := store.GetConfig(prefix + "KEPT1"); v != "value of KEPT1" {
				t.Errorf("the other device lost %s", prefix)
			}
		}
		for _, r := range deviceRows {
			if rowCount(t, store, r.table, r.column, "GONE1") != 0 {
				left = append(left, r.table)
			}
			if rowCount(t, store, r.table, r.column, "KEPT1") != 1 {
				t.Errorf("the other device lost its row in %s", r.table)
			}
		}
		if len(left) > 0 {
			t.Errorf("a permanent delete left behind:\n  %s", strings.Join(left, "\n  "))
		}
		if rowCount(t, store, "audit_connections", "peer_id", "GONE1") != 1 {
			t.Error("the device's audit history went with it")
		}
	})
}
