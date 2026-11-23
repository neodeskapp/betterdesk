package settings

import (
	"net/http"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// refusalKeys are the fixed English refusals of the appearance routes
// and the keys of their texts in the language files, so a refusal comes
// in the caller's language (BUGS L419).
var refusalKeys = map[string]string{
	"Invalid theme preset file":        "settings.appearance.refused_theme_file",
	"Invalid font file type":           "settings.appearance.refused_font_file",
	"Invalid file type":                "settings.appearance.refused_image_file",
	"Profile name is required":         "settings.appearance.refused_profile_name",
	"Profile name already exists":      "settings.appearance.refused_profile_taken",
	"Cannot delete the active profile": "settings.appearance.refused_active_profile",
}

// inCallersLanguage is an English refusal of refusalKeys in the caller's
// language; English, and any other text, as it is.
func inCallersLanguage(r *http.Request, english string) string {
	key, ok := refusalKeys[english]
	if !ok {
		return english
	}
	if text := kit.T(r, key, nil); text != key && text != "" {
		return text
	}
	return english
}
