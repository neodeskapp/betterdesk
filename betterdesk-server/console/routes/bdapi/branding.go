package bdapi

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
)

// What a client may read of the panel without signing in: who it is and
// how it looks. The routes read the branding through Refresh, so they
// see what was saved last, by whichever console.

type appearanceContract struct {
	Version  string `json:"version"`
	Endpoint string `json:"endpoint"`
	Revision string `json:"revision"`
}

type serverInfoAnswer struct {
	OK                 bool               `json:"ok"`
	Product            string             `json:"product"`
	Version            string             `json:"version"`
	PanelName          any                `json:"panel_name"`
	AppearanceContract appearanceContract `json:"appearance_contract"`
}

// serverInfo answers GET /api/bd/server-info: the panel's identity, which
// the remote viewer checks a panel address with.
func (h *handlers) serverInfo(w http.ResponseWriter, r *http.Request) {
	h.d.Branding.Refresh(r.Context())
	b := h.d.Branding.GetBranding()
	kit.JSON(w, r, http.StatusOK, serverInfoAnswer{
		OK: true, Product: "betterdesk-panel", Version: h.version,
		PanelName: orValue(member(b, "appName"), configuredAppName),
		AppearanceContract: appearanceContract{
			Version: "2.0", Endpoint: "/api/bd/appearance", Revision: h.d.Branding.Revision(),
		},
	})
}

type brandingAnswer struct {
	CompanyName    any `json:"company_name"`
	AccentColor    any `json:"accent_color"`
	SupportContact any `json:"support_contact"`
	// Appearance is missing from the answer when the appearance could
	// not be made.
	Appearance *branding.Object `json:"appearance,omitempty"`
}

// branding answers GET /api/bd/branding: the company's name, colour and
// contact for the desktop client, with the public appearance. When the
// appearance cannot be made the defaults go out, never an error.
func (h *handlers) branding(w http.ResponseWriter, r *http.Request) {
	h.d.Branding.Refresh(r.Context())
	b := h.d.Branding.GetBranding()
	appearance, err := h.d.Branding.GetPublicAppearance()
	if err != nil {
		h.log.Error("Branding error:", err)
		kit.JSON(w, r, http.StatusOK, brandingAnswer{CompanyName: "BetterDesk", AccentColor: "#3b82f6", SupportContact: ""})
		return
	}
	colors, _ := member(b, "colors").(*branding.Object)
	kit.JSON(w, r, http.StatusOK, brandingAnswer{
		CompanyName:    orValue(member(b, "appName"), "BetterDesk"),
		AccentColor:    orValue(member(colors, "accentBlue"), "#3b82f6"),
		SupportContact: orValue(member(b, "supportContact"), ""),
		Appearance:     appearance,
	})
}

// appearance answers GET /api/bd/appearance: the public appearance, never
// cached; a fixed dark appearance when it cannot be made.
func (h *handlers) appearance(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, private")
	h.d.Branding.Refresh(r.Context())
	appearance, err := h.d.Branding.GetPublicAppearance()
	if err != nil {
		h.log.Error("Appearance error:", err)
		kit.JSON(w, r, http.StatusOK, goapi.NewObject("success", true, "data", fallbackAppearance()))
		return
	}
	kit.JSON(w, r, http.StatusOK, goapi.NewObject("success", true, "data", appearance))
}

// fallbackAppearance is the appearance of the route's catch block.
func fallbackAppearance() *goapi.Object {
	return goapi.NewObject(
		"version", "2.0",
		"revision", "fallback",
		"product", "betterdesk-appearance",
		"identity", goapi.NewObject("appName", "BetterDesk", "logoType", "icon", "logoIcon", "dns"),
		"palette", goapi.NewObject(
			"mode", "dark",
			"primary", "#58a6ff",
			"background", "#0d1117",
			"surface", "#161b22",
			"surfaceRaised", "#21262d",
			"text", "#e6edf3",
			"muted", "#8b949e",
			"border", "#30363d",
			"danger", "#f85149",
			"warning", "#d29922",
			"success", "#2ea44f",
		),
		"surfaces", goapi.NewObject("glassEnabled", true, "glassBlur", "16", "glassOpacity", "55"),
		"background", goapi.NewObject("type", "none", "color", "", "gradient", "", "imageUrl", "", "overlay", "", "size", "cover"),
		"readability", goapi.NewObject("ok", true, "issues", []any{}),
	)
}

// member is b[key] of a branding object, nil when it has none.
func member(b *branding.Object, key string) any {
	v, _ := b.Get(key)
	return v
}
