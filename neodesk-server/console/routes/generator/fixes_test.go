package generator_test

// fix is a recorded step that the Go console answers otherwise on
// purpose: Node's answer was a bug that is fixed here (bug names the row
// of BUGS.md). The answer is the same on both databases.
type fix struct {
	bug    string
	status int
	answer string
}

// fixes are the steps of the recordings (testdata/node_*.json) that the
// Go console answers otherwise, by the step's name.
var fixes = map[string]fix{
	// A colour given as a list is an invalid colour, not a TypeError.
	"28 POST /api/generator/bundles": {bug: "GN2", status: 400,
		answer: `{"success":false,"error":"Validation failed","errors":["primary_color_invalid"],"details":["primary_color_invalid"]}`},
	"104 POST /api/generator/preview": {bug: "GN2", status: 200,
		answer: `{"success":true,"data":{"valid":false,"errors":["accent_color_invalid"],"branding":{"app_name":"NeoDesk Support Agent",` +
			`"company_name":"NeoDesk Support Agent","relay_host":"h.example:21117","api_server":"https://h.example:21114","api_port":"21114",` +
			`"disable_settings":true,"short_text":"","product_label":"","hide_product_type":false,"contact_email":"","contact_phone":"",` +
			`"contact_url":"","logo_data_url":"","primary_color":"#2563eb","accent_color":"#e0f2fe","background_color":"#ffffff",` +
			`"surface_color":"#f3f4f6","text_color":"#1f2937","text_muted_color":"#6b7280","status_ready_color":"#22c55e",` +
			`"header_text_color":"#1f2937","allow_unattended":false,"install_service":false,"autostart":false,` +
			`"capabilities":{"desktop":true,"files":true,"clipboard":true,"audio":true,"terminal":true,"restart":true},"default_lang":"en",` +
			`"server_host":"h.example","use_https":true,"server":{"address":"https://h.example:21114","api_url":"https://h.example:21114",` +
			`"public_key":"AwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwM="}}},"errors":["accent_color_invalid"]}`},
	// A change without a profile of a bundle whose stored profile is null
	// starts from the default profile, which asks for the server's host.
	"148 PUT /api/generator/bundles/nullbr00001": {bug: "GN3", status: 400,
		answer: `{"success":false,"error":"Validation failed","errors":["server_host_required"],"details":["server_host_required"]}`},
	// The relay of the rustdesk.toml: a port that is not valid gives way
	// to the relay port, a URL's host and port are read after its scheme,
	// and a relayPort that is no port from 1 to 65535 is the server's.
	"113 POST /api/generator/generate-config": tomlWithRelay("r:21117"),
	"114 POST /api/generator/generate-config": tomlWithRelay("r:7"),
	"120 POST /api/generator/generate-config": tomlWithRelay("R.example:8443"),
	"128 POST /api/generator/generate-config": tomlWithRelay("r:21117"),
	"129 POST /api/generator/generate-config": tomlWithRelay("r:21117"),
	"130 POST /api/generator/generate-config": tomlWithRelay("r:21117"),
	"131 POST /api/generator/generate-config": tomlWithRelay("r:21117"),
	"133 POST /api/generator/generate-config": tomlWithRelay("r:21117"),
}

// tomlWithRelay is the old toml generator's answer for the host h with
// the relay given (UM4).
func tomlWithRelay(relay string) fix {
	return fix{bug: "UM4", status: 200, answer: `{"success":true,"data":{"config":"rendezvous_server = h:21116\nrelay_server = ` + relay +
		`\nkey = AwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwM=","fileName":"rustdesk.toml"}}`}
}
