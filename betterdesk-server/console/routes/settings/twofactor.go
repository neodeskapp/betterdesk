package settings

import (
	"net/http"
	"strconv"

	"github.com/unitronix/betterdesk-server/console/authn"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

type twoFactorData struct {
	Required bool `json:"required"`
}

// getTwoFactor answers GET /api/settings/two-factor: whether every local
// account must have two-factor sign-in (the server keeps the switch).
func (g *general) getTwoFactor(w http.ResponseWriter, r *http.Request) {
	value := sessionConfig(g.d.GoAPI.GetConfig(r.Context(), authn.RequireTwoFactorKey))
	kit.OK(w, r, twoFactorData{Required: parseSessionBool(value, false)})
}

// setTwoFactor answers PUT /api/settings/two-factor with {required}: true
// makes two-factor sign-in mandatory for local accounts, which set it up
// right after their next password sign-in; directory and single sign-on
// accounts are left to their provider. A body without the value is
// refused.
func (g *general) setTwoFactor(w http.ResponseWriter, r *http.Request) {
	body := jsreq.Body(r)
	if refuseNothingToSave(w, r, body, "required") {
		return
	}
	required := jsreq.Prop(body, "required") == true
	if result, ok := g.setConfigs(r, []configPair{{authn.RequireTwoFactorKey, strconv.FormatBool(required)}}); !ok {
		kit.JSON(w, r, http.StatusInternalServerError, failure{Error: orText(result.Get("error"), "Failed to save two-factor settings")})
		return
	}
	if err := g.d.LogAction(r, "two_factor_policy_changed", "Two-factor sign-in required: "+strconv.FormatBool(required)); err != nil {
		g.serverError(w, r, "Save two-factor settings error", err)
		return
	}
	kit.OK(w, r, nil)
}
