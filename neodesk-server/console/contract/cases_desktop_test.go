//go:build contract

package contract_test

import "github.com/unitronix/betterdesk-server/console/contract"

// The desktop of the console: each account's widget layout and wallpaper.
// Every role writes its own and ends its turn with an empty layout and no
// wallpaper, which reads like an account that never stored one.
func init() {
	some := contract.Roles("signed-out", "viewer", "admin")
	writers := contract.Roles("viewer", "admin")
	widget := func(id string) map[string]any {
		return map[string]any{"id": id, "type": "clock", "x": 0, "y": 0, "w": 2, "h": 2}
	}
	save := func(name string, body any, roles []contract.Role) contract.Case {
		return contract.Case{Name: name, Method: "POST", Path: "/api/desktop/layout", Body: body, Roles: roles}
	}
	load := func(name string, roles []contract.Role) contract.Case {
		return contract.Case{Name: name, Path: "/api/desktop/layout", Roles: roles}
	}
	many := make([]any, 101)
	for i := range many {
		many[i] = widget("w")
	}
	longID := "0123456789012345678901234567890123456789x"

	contract.Group("desktop",
		contract.Case{Name: "wallpapers", Path: "/api/desktop/wallpapers"},
		contract.Case{Name: "wallpapers-head", Method: "HEAD", Path: "/api/desktop/wallpapers", Roles: some},
		load("layout", nil),

		save("save", map[string]any{
			"widgets": []any{
				map[string]any{"id": "w1", "type": "clock", "x": 0, "y": 0, "w": 2, "h": 2, "settings": map[string]any{"zone": "Europe/Berlin", "b": 1, "a": 2}},
				map[string]any{"id": "w2", "type": "devices", "x": 2.5, "y": -1, "w": 4, "h": 3, "title": "Geräte <b>&amp;</b>"},
			},
			"wallpaper": "/wallpapers/3.png",
		}, nil),
		load("layout-after-save", nil),
		save("save-wallpaper-color-alone", map[string]any{"wallpaper": "solid:#1a2B3c"}, writers),
		load("layout-after-wallpaper-alone", writers),
		save("save-widgets-alone", map[string]any{"widgets": []any{widget("w3")}}, writers),
		load("layout-after-widgets-alone", writers),
		contract.Case{
			Name: "save-numbers-as-javascript-writes-them", Method: "POST", Path: "/api/desktop/layout",
			RawBody:     `{"widgets":[{"id":"w4","type":"note","x":1.0,"y":2e1,"w":3,"h":4,"h":5,"10":"b","9":"a"}]}`,
			ContentType: "application/json", Roles: writers,
		},
		load("layout-after-numbers", writers),
		save("save-wallpaper-null", map[string]any{"widgets": []any{widget("w5")}, "wallpaper": nil}, writers),
		load("layout-after-wallpaper-null", writers),
		save("save-empty-body", map[string]any{}, writers),
		load("layout-after-empty-body", writers),
		contract.Case{
			Name: "save-with-a-list-as-body", Method: "POST", Path: "/api/desktop/layout",
			RawBody: `[{"id":"w6","type":"clock","x":0,"y":0,"w":1,"h":1}]`, ContentType: "application/json", Roles: writers,
		},

		save("save-widgets-as-text", map[string]any{"widgets": "w1"}, writers),
		save("save-widgets-null", map[string]any{"widgets": nil}, writers),
		save("save-widgets-as-an-object", map[string]any{"widgets": map[string]any{"0": widget("w")}}, writers),
		save("save-too-many-widgets", map[string]any{"widgets": many}, writers),
		save("save-widget-without-an-id", map[string]any{"widgets": []any{map[string]any{"type": "clock", "x": 0, "y": 0, "w": 1, "h": 1}}}, writers),
		save("save-widget-with-a-long-id", map[string]any{"widgets": []any{widget(longID)}}, writers),
		save("save-widget-with-a-number-as-type", map[string]any{"widgets": []any{map[string]any{"id": "w", "type": 5, "x": 0, "y": 0, "w": 1, "h": 1}}}, writers),
		save("save-widget-with-text-as-x", map[string]any{"widgets": []any{map[string]any{"id": "w", "type": "clock", "x": "0", "y": 0, "w": 1, "h": 1}}}, writers),
		save("save-widget-without-a-height", map[string]any{"widgets": []any{map[string]any{"id": "w", "type": "clock", "x": 0, "y": 0, "w": 1}}}, writers),
		save("save-widget-that-is-a-list", map[string]any{"widgets": []any{[]any{"w"}}}, writers),
		save("save-widget-that-is-null", map[string]any{"widgets": []any{nil}}, writers),
		save("save-wallpaper-outside-the-folder", map[string]any{"wallpaper": "/wallpapers/../secret.png"}, writers),
		save("save-wallpaper-with-five-digits", map[string]any{"wallpaper": "/wallpapers/12345.png"}, writers),
		save("save-wallpaper-short-color", map[string]any{"wallpaper": "solid:#12"}, writers),
		save("save-wallpaper-number", map[string]any{"wallpaper": 3}, writers),
		save("save-wallpaper-with-a-line-break", map[string]any{"wallpaper": "/wallpapers/3.png\n"}, writers),
		save("save-bad-widgets-and-bad-wallpaper", map[string]any{"widgets": "x", "wallpaper": 3}, writers),
		contract.Case{
			Name: "save-without-csrf-token", Method: "POST", Path: "/api/desktop/layout",
			Body: map[string]any{"widgets": []any{}}, NoCSRF: true, Roles: some,
		},
		contract.Case{
			Name: "save-as-form", Method: "POST", Path: "/api/desktop/layout",
			RawBody: "wallpaper=solid:%23fff", ContentType: "application/x-www-form-urlencoded", Roles: some,
		},
		load("layout-after-refused-saves", writers),

		save("reset", map[string]any{"widgets": []any{}, "wallpaper": ""}, nil),
		load("layout-after-reset", nil),
	)
}
