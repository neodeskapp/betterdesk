package onboard

import (
	"net/http"
	"net/url"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/onboard"
)

// joinPage answers GET /join/{code}, the invite page: the web app's page
// with, for a browser without JavaScript, the three steps and the
// downloads in a <noscript> block (onboard.NoScriptJoin). The block is
// made from the address's code and the client release alone, without
// looking the code up, so the page tells nobody whether a code works.
// Without a build of the web app, the web host answers as for any page.
func (h *handlers) joinPage(w http.ResponseWriter, r *http.Request) {
	code := onboard.Normalize(r.PathValue("code"))
	if h.d.WebUI == nil {
		http.NotFound(w, r)
		return
	}
	if !onboard.ValidFormat(code) || !h.d.WebUI.PageWith(w, r, h.noScriptJoin(r, code)) {
		if !h.d.WebUI.Handle(w, r) {
			h.d.WebUI.NotFound(w, r)
		}
	}
}

// noScriptJoin is the <noscript> block of the invite page for code.
func (h *handlers) noScriptJoin(r *http.Request, code string) string {
	c := h.consoleOf(r)
	facts := onboard.NoScriptFacts{
		Server: h.serverName(), Address: c.Base(), DisplayCode: onboard.Display(code),
		UserAgent:      r.UserAgent(),
		InstallCommand: "curl -fsSL '" + c.Base() + "/join/" + code + "/install.sh' | sudo sh",
		DeepLink:       c.DeepLink(onboard.KindJoin, code),
	}
	if offers, err := h.releases.Offers(r.Context(), onboard.KindJoin); err == nil {
		for _, o := range offers {
			q := url.Values{"os": {o.OS}, "arch": {o.Arch}, "format": {o.Format}}
			facts.Downloads = append(facts.Downloads, onboard.NoScriptDownload{
				OS: o.OS, Arch: o.Arch, Format: o.Format,
				URL:  "/api/onboard/public/" + code + "/download?" + q.Encode(),
				File: c.FileName(onboard.KindJoin, code, o.Ext()),
			})
		}
	}
	return onboard.NoScriptJoin(func(key string, vars map[string]any) string {
		return kit.T(r, key, kit.Vars(flatten(vars)...))
	}, facts)
}
