package console

import (
	"net/http"

	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/kit"
)

// supporters answers GET /api/console/supporters: the project's supporters
// and links for the Help panel, {success, honorary, individuals, links}.
func (h *handlers) supporters(w http.ResponseWriter, r *http.Request) {
	answer := branding.NewObject()
	answer.Set("success", true)
	list := h.d.Branding.LoadSupporters()
	for _, key := range list.Keys() {
		value, _ := list.Get(key)
		answer.Set(key, value)
	}
	kit.JSON(w, r, http.StatusOK, answer)
}
