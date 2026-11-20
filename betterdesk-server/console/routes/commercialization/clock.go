package commercialization

import (
	"errors"
	"net/http"

	"github.com/unitronix/betterdesk-server/console/billingclock"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/restart"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

// failure is {success: false, error}.
type failure struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

func fail(w http.ResponseWriter, r *http.Request, status int, err error) {
	kit.JSON(w, r, status, failure{Error: jsbind.Message(err)})
}

type clockAnswer struct {
	Success    bool                  `json:"success"`
	Settings   billingclock.Settings `json:"settings"`
	DockerMode bool                  `json:"dockerMode"`
}

// clockSettings answers GET /api/panel/billing/clock/settings: the
// settings in effect, and whether the server takes a save at once
// (dockerMode).
func (h *handlers) clockSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := h.clock.GetRuntimeClockSettings(r.Context())
	if err != nil {
		fail(w, r, http.StatusInternalServerError, err)
		return
	}
	kit.JSON(w, r, http.StatusOK, clockAnswer{Success: true, Settings: settings, DockerMode: h.clock.IsDockerSplitDeployment()})
}

type savedClock struct {
	Success         bool                  `json:"success"`
	Settings        billingclock.Settings `json:"settings"`
	ServiceConfig   any                   `json:"serviceConfig"`
	Status          any                   `json:"status"`
	Restart         any                   `json:"restart"`
	RestartRequired *restart.Public       `json:"restartRequired"`
	DockerMode      bool                  `json:"dockerMode"`
}

// saveClockSettings answers PUT /api/panel/billing/clock/settings with
// the body {ntp_servers, max_skew_ms, require_synced_clock,
// trust_os_ntp, billing_timezone}; what is missing gets its default.
// The settings take effect when the server restarts, so the save is a
// change of the operator's restart transaction, which can put back the
// settings from before (restartRequired). The file is written only once
// the transaction takes the change: while another operator's change
// waits the answer is 409 restart_transaction_owned and nothing is
// written. Where the server takes them at once (dockerMode) there is no
// restart, and a failed restart of an earlier clock save is dismissed.
//
// A body with none of the settings answers 400 No settings to save; a
// refused value answers 400 with invalid_ntp_servers, invalid_max_skew
// or invalid_billing_timezone; anything else that fails answers 500 with
// its message.
func (h *handlers) saveClockSettings(w http.ResponseWriter, r *http.Request) {
	body := jsreq.Body(r)
	if refuseNothingToSave(w, r, body, "ntp_servers", "max_skew_ms", "require_synced_clock", "trust_os_ntp", "billing_timezone") {
		return
	}
	previous, err := h.clock.GetRuntimeClockSettings(r.Context())
	if err != nil {
		h.failSave(w, r, err)
		return
	}
	var result billingclock.SaveResult
	var pending *restart.Public
	if h.clock.IsDockerSplitDeployment() {
		if result, err = h.clock.SaveClockSettings(r.Context(), body); err == nil {
			_, err = h.d.Restart.DismissFailed(kit.UserID(r), "billing-clock")
		}
	} else if _, err = h.clock.ValidateSettings(body); err == nil {
		pending, err = h.d.Restart.RegisterChangeAfter(kit.UserID(r), "billing-clock", func() (restart.Change, error) {
			var saveErr error
			if result, saveErr = h.clock.SaveClockSettings(r.Context(), body); saveErr != nil {
				return restart.Change{}, saveErr
			}
			return restart.Change{
				Key:      "billing-clock",
				Label:    kit.T(r, "commercialization.clock.title", nil),
				Rollback: goapi.NewObject("type", "billing-clock", "settings", previous.Object()),
			}, nil
		})
	}
	if err != nil {
		h.failSave(w, r, err)
		return
	}
	// A failure to write the audit entry does not change the answer
	_ = h.d.LogAction(r, "billing_clock_settings_updated", "Updated billing/NTP clock settings")
	kit.JSON(w, r, http.StatusOK, savedClock{
		Success:         true,
		Settings:        result.Settings,
		ServiceConfig:   result.ServiceConfig,
		Status:          result.Status,
		RestartRequired: pending,
		DockerMode:      result.DockerMode,
	})
}

func (h *handlers) failSave(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	var transaction *restart.Error
	switch {
	case billingclock.IsRefusal(err):
		status = http.StatusBadRequest
	case errors.As(err, &transaction):
		status = transaction.StatusCode()
	}
	fail(w, r, status, err)
}

type detected struct {
	Success  bool     `json:"success"`
	Timezone string   `json:"timezone"`
	Common   []string `json:"common"`
}

// detectTimezone answers GET /api/panel/billing/clock/detect-timezone:
// the machine's time zone and the zones the settings page lists.
func (h *handlers) detectTimezone(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, detected{Success: true, Timezone: h.clock.DetectOSTimezone(), Common: billingclock.CommonTimezones()})
}
