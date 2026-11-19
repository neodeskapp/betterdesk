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
		answer: `{"success":true,"data":{"valid":false,"errors":["accent_color_invalid"],"branding":{"app_name":"BetterDesk Support Agent",` +
			`"company_name":"BetterDesk Support Agent","relay_host":"h.example:21117","api_server":"https://h.example:21114","api_port":"21114",` +
			`"disable_settings":true,"short_text":"","product_label":"","hide_product_type":false,"contact_email":"","contact_phone":"",` +
			`"contact_url":"","logo_data_url":"","primary_color":"#2563eb","accent_color":"#e0f2fe","background_color":"#ffffff",` +
			`"surface_color":"#f3f4f6","text_color":"#1f2937","text_muted_color":"#6b7280","status_ready_color":"#22c55e",` +
			`"header_text_color":"#1f2937","allow_unattended":false,"install_service":false,"autostart":false,` +
			`"capabilities":{"desktop":true,"files":true,"clipboard":true,"audio":true,"terminal":true,"restart":true},"default_lang":"en",` +
			`"server_host":"h.example","use_https":true,"server":{"address":"https://h.example:21114","api_url":"https://h.example:21114",` +
			`"public_key":"AwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwMDAwM="}}},"errors":["accent_color_invalid"]}`},
}
