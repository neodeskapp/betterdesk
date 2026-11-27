package db

// What a permanent delete (HardDeletePeer) removes with the device besides
// its row and its device tokens: everything the server keeps of that
// install, so that a new install that takes the same ID starts with none
// of it (L464). The device's history (audit, connections, availability,
// help requests, billing) stays.

// deviceConfigPrefixes are the server_config keys kept per device: its
// telemetry sequence, public key, key request and refusal, second factor
// secret, name, sync mode, bundle, CDAP manifest, mesh group and quick
// support mark.
var deviceConfigPrefixes = []string{
	"telemetry_seq_", "device_public_key_", "device_key_request_", "device_key_refused_",
	"device_totp_secret_", "device_display_name_", "device_sync_mode_", "device_bundle_id_",
	"cdap_manifest_", "mesh_group_id_", "quick_support_device_",
}

// deviceRowTables are the tables with rows of the device: its passwords
// stored for organizations, its organization, its access policy and
// password, the accounts' grants, its groups and folder, its connection
// policy and commands, its telemetry and metrics.
var deviceRowTables = []struct{ table, column string }{
	{"org_peer_credentials", "peer_id"},
	{"org_devices", "device_id"},
	{"access_policies", "peer_id"},
	{"user_peer_grants", "peer_id"},
	{"device_group_members", "peer_id"},
	{"device_folder_assignments", "device_id"},
	{"device_connection_policies", "device_id"},
	{"device_connection_mode_commands", "device_id"},
	{"device_telemetry_snapshots", "device_id"},
	{"device_telemetry_commands", "device_id"},
	{"peer_metrics", "peer_id"},
	{"fleet_device_resources", "device_id"},
	{"fleet_compliance", "device_id"},
}

// deviceConfigKeys are the server_config keys of the device id.
func deviceConfigKeys(id string) []any {
	keys := make([]any, len(deviceConfigPrefixes))
	for i, prefix := range deviceConfigPrefixes {
		keys[i] = prefix + id
	}
	return keys
}
