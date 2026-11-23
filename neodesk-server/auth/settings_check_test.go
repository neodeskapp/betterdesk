package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Every reason a directory or single sign-on setting is refused for has
// its text in the console's language files: the same English as the API
// answers, and German of its own (L419).
func TestSettingReasonsHaveTheirTexts(t *testing.T) {
	texts := func(lang string) map[string]any {
		raw, err := os.ReadFile(filepath.Join("..", "console", "static", "files", "lang", lang+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var file struct {
			Settings struct {
				Signin map[string]any `json:"signin"`
			} `json:"settings"`
		}
		if err := json.Unmarshal(raw, &file); err != nil {
			t.Fatal(err)
		}
		return file.Settings.Signin
	}
	en, de := texts("en"), texts("de")
	for _, code := range SettingCodes() {
		key := "check_" + code
		if en[key] != SettingMessage(code) {
			t.Errorf("en.json settings.signin.%s = %q, the API answers %q", key, en[key], SettingMessage(code))
		}
		if text, _ := de[key].(string); text == "" || text == SettingMessage(code) {
			t.Errorf("de.json settings.signin.%s = %q, want German", key, de[key])
		}
	}
}

func TestCheckGroupRoleMap(t *testing.T) {
	for raw, want := range map[string]string{
		"":                                    "",
		"admins=admin":                        "",
		"CN=Admins,OU=Groups,DC=x,DC=y=admin": "",
		"admins=admin|ops=operator\n\nviewers=viewer\r\n": "",
		"admins=superuser":  "map_role 1 admins=superuser",
		"admins=admin\nops": "map_line 2 ops",
		"=admin":            "map_line 1 =admin",
		"admins=":           "map_line 1 admins=",
	} {
		got := ""
		if err := CheckGroupRoleMap(raw); err != nil {
			got = err.Code + " " + itoa(err.Line) + " " + err.Text
		}
		if got != want {
			t.Errorf("%q: %q, want %q", raw, got, want)
		}
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
