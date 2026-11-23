// Package inventory holds the inventory routes. The console mounts the
// group twice, at /api/nd and at /api/inventory, so each route answers at
// both:
//
//   - The routes of the desktop agent, which take a device token: it
//     uploads its hardware and software (POST inventory) and its load
//     (POST telemetry), and reads its own last report (GET inventory/{id}).
//     They are behind the device guards of package kit (device.go).
//   - The routes people read inventory with: the list of devices with their
//     hardware in short, and one device's last report with its last
//     telemetry. Both need the permission device.view and keep to the
//     account's device scope, and both step aside at /api/nd for the
//     routes of other groups there.
package inventory

import (
	"math"
	"net/http"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/alerts"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/devicemount"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsjson"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

type handlers struct {
	db     *store.DB
	api    *goapi.Client
	alerts *alerts.Engine
	log    *kit.Logger
	// telemetryLog takes what the telemetry route logs
	telemetryLog *kit.Logger
	// scope is the device scope of the account of a request, nil when it
	// sees every device
	scope func(r *http.Request) (*access.DeviceScope, error)
}

// Register adds the group's routes below the mount.
func Register(d *kit.Deps) {
	h := &handlers{
		db: d.Store, api: d.GoAPI, alerts: d.Alerts,
		log: d.Log.Child("Inventory"), telemetryLog: d.Log.Child("Telemetry"),
		scope: func(r *http.Request) (*access.DeviceScope, error) {
			ctx := r.Context()
			return d.DeviceGroups.LimitedScope(ctx, kit.User(r), func() []*goapi.Object {
				return d.Backend.GetAllDevices(ctx, goapi.DeviceFilters{})
			})
		},
	}
	r := d.Router
	canRead := d.RequirePermission("device.view")
	device := []kit.Middleware{d.RequireDeviceToken, d.RequireTokenDeviceMatch}
	r.Post("/inventory", h.upload, device...)
	r.Post("/telemetry", h.telemetry, device...)
	r.Get("/inventory/{id}", h.ownReport, device...)
	r.Get("/", h.list, devicemount.Not, d.RequireAuth, canRead)
	r.Get("/{id}", h.detail, devicemount.Not, d.RequireAuth, canRead)
}

// failure is the group's error answer: {"error":"..."}.
type failure struct {
	Error string `json:"error"`
}

func fail(w http.ResponseWriter, r *http.Request, status int, message string) {
	kit.JSON(w, r, status, failure{Error: message})
}

// serverError answers what every route's catch block answered.
func (h *handlers) serverError(w http.ResponseWriter, r *http.Request, what string, err error) {
	h.log.Error(what+":", err)
	fail(w, r, http.StatusInternalServerError, "Internal server error")
}

// maxSafeInteger is Number.MAX_SAFE_INTEGER, the largest page there is.
const maxSafeInteger = 1<<53 - 1

// pageNumber reads one of the two paging parameters: a text of digits
// that is a whole number from 1 up, cut down to max. A parameter that is
// missing is the fallback. Everything else is refused: other text, a
// number too large to be exact, and a parameter given as a list or an
// object.
func pageNumber(value jsbind.Value, fallback, max float64) (float64, bool) {
	if value.IsUndefined() {
		return fallback, true
	}
	// A list or an object has no text, and no text is the number 0
	digits := value.Str()
	if strings.Trim(digits, "0123456789") != "" {
		return 0, false
	}
	parsed := jsbind.ParseNumber(digits)
	if parsed > maxSafeInteger || parsed < 1 {
		return 0, false
	}
	return math.Min(parsed, max), true
}

type deviceList struct {
	Devices    []any  `json:"devices"`
	Total      int    `json:"total"`
	Page       number `json:"page"`
	Limit      number `json:"limit"`
	TotalPages int    `json:"total_pages"`
}

// list answers GET /api/inventory?page=&limit=: one page of the devices
// that have reported their inventory, the last report first, each with
// its hardware in short. 50 devices make a page unless limit says
// otherwise, 100 at most. Each entry is made on its own: what a report
// holds that cannot be read (a document that is no JSON, a member that
// cannot be made text or a number) is logged and left empty in that
// report's entry.
//
// While no device has reported, the list shows the devices the API
// server knows instead, marked with source "peer_list". An account limited
// to some devices gets only those.
func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
	query, _, _ := strings.Cut(r.URL.RawQuery, "#")
	page, pageOK := pageNumber(jsbind.Query(query, "page"), 1, maxSafeInteger)
	limit, limitOK := pageNumber(jsbind.Query(query, "limit"), 50, 100)
	if !pageOK || !limitOK {
		fail(w, r, http.StatusBadRequest, "Invalid pagination parameters")
		return
	}
	scope, err := h.scope(r)
	if err != nil {
		h.serverError(w, r, "List error", err)
		return
	}
	inventories, err := allInventories(r.Context(), h.db)
	if err != nil {
		h.serverError(w, r, "List error", err)
		return
	}
	devices := []any{}
	for _, inv := range inventories {
		if !scope.Has(inv.deviceID.Value().ToString()) {
			continue
		}
		device, problems := summaryOf(inv)
		if inv.unreadable != nil {
			problems = append(problems, inv.unreadable)
		}
		for _, problem := range problems {
			h.log.Warn("Listing the inventory of device "+inv.deviceID.Value().ToString()+" without what cannot be read:", jsbind.Message(problem))
		}
		devices = append(devices, device)
	}
	if len(inventories) == 0 {
		devices = h.knownPeers(r, scope)
	}

	total := len(devices)
	size := int(limit)
	totalPages := (total + size - 1) / size
	start := total
	if page <= float64(totalPages) {
		start = (int(page) - 1) * size
	}
	end := min(start+size, total)
	kit.JSON(w, r, http.StatusOK, deviceList{
		Devices:    devices[start:end],
		Total:      total,
		Page:       number(page),
		Limit:      number(limit),
		TotalPages: totalPages,
	})
}

// knownPeers is the list of devices the API server knows that are in the
// scope, in the form of the inventory list. When the API server gives no
// list, it is empty.
func (h *handlers) knownPeers(r *http.Request, scope *access.DeviceScope) []any {
	devices := []any{}
	for _, p := range h.api.GetAllPeers(r.Context(), false) {
		if p == nil {
			h.log.Warn("Peer list fallback failed: a peer of the list is null")
			return []any{}
		}
		if !scope.Has(p.Str("id")) {
			continue
		}
		os := p.Get("platform")
		if !goapi.Truthy(os) {
			os = p.Get("os")
		}
		if !goapi.Truthy(os) {
			os = ""
		}
		hostname := p.Get("hostname")
		if !goapi.Truthy(hostname) {
			hostname = p.Get("id")
		}
		lastSeen := p.Get("last_online")
		if !goapi.Truthy(lastSeen) {
			lastSeen = nil
		}
		devices = append(devices, goapi.NewObject(
			"device_id", p.Get("id"),
			"hostname", hostname,
			"os", os,
			"cpu", "",
			"cpu_cores", 0,
			"cpu_usage", nil,
			"memory_total_mb", 0,
			"memory_used_mb", 0,
			"disk_count", 0,
			"software_count", 0,
			"last_seen", lastSeen,
			"collected_at", nil,
			"source", "peer_list",
		))
	}
	return devices
}

type report struct {
	DeviceID    any          `json:"device_id"`
	Hardware    jsjson.Value `json:"hardware"`
	Software    jsjson.Value `json:"software"`
	CollectedAt any          `json:"collected_at"`
	ReceivedAt  any          `json:"received_at"`
	Telemetry   *telemetry   `json:"telemetry"`
}

// detail answers GET /api/inventory/{id}: the device's last report as it
// came, with its last telemetry, null when it sent none. A device outside
// the account's scope, or one no device has, is refused with 403.
func (h *handlers) detail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	scope, err := h.scope(r)
	if err != nil {
		h.serverError(w, r, "Detail error", err)
		return
	}
	if !scope.Has(id) {
		fail(w, r, http.StatusForbidden, kit.T(r, "errors.forbidden", nil))
		return
	}
	inv, err := inventoryOf(r.Context(), h.db, id)
	var last *telemetry
	if err == nil {
		last, err = telemetryOf(r.Context(), h.db, id)
	}
	if err != nil {
		h.serverError(w, r, "Detail error", err)
		return
	}
	if inv == nil {
		fail(w, r, http.StatusNotFound, "No inventory data for this device")
		return
	}
	kit.JSON(w, r, http.StatusOK, report{
		DeviceID:    inv.deviceID,
		Hardware:    inv.hardware,
		Software:    inv.software,
		CollectedAt: inv.collectedAt,
		ReceivedAt:  inv.receivedAt,
		Telemetry:   last,
	})
}
