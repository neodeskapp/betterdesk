// Package crossplatform answers the cross-platform page: how the devices
// spread over operating systems, architectures and client versions, which
// codecs and features the devices that are online name, and one device as
// the API server describes it.
//
// The two counts read one page of the API server's device list. A page
// holds at most 500 devices, whatever size is asked for.
package crossplatform

import (
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

// peersPage is the device list the counts are made from.
const peersPage = "/peers?page_size=9999"

type handlers struct {
	api *goapi.Client
}

// Register adds the group's routes.
func Register(d *kit.Deps) {
	h := &handlers{api: d.GoAPI}
	r := d.Router
	view := d.RequirePermission("device.view")
	r.Get("/api/panel/cross-platform/distribution", h.distribution, d.RequireAuth, view)
	r.Get("/api/panel/cross-platform/capabilities/{deviceId}", h.capabilities, d.RequireAuth, view)
	r.Get("/api/panel/cross-platform/protocols", h.protocols, d.RequireAuth, view)
}

type failure struct {
	Error string `json:"error"`
}

// errShape is a device list, or an entry or field of it, of a kind the
// counts cannot read.
var errShape = errors.New("the device list has a value of the wrong kind")

// device is one entry of the device list. An entry that is no object
// (text, a number, a list) has no fields and counts as a device nothing is
// known about.
type device struct {
	fields *goapi.Object
}

// devices reads the device list: the "peers" of the API server's answer,
// else its "data". Text in the place of the list is walked letter by
// letter, each letter an entry without fields.
func (h *handlers) devices(r *http.Request) ([]device, error) {
	resp, err := h.api.Get(r.Context(), peersPage)
	if err != nil {
		return nil, err
	}
	data := resp.Data()
	found := goapi.Dig(data, "peers")
	if !goapi.Truthy(found) {
		found = goapi.Dig(data, "data")
	}
	if !goapi.Truthy(found) {
		return nil, nil
	}
	switch t := found.(type) {
	case []any:
		list := make([]device, len(t))
		for i, entry := range t {
			if entry == nil {
				return nil, errShape
			}
			list[i].fields, _ = entry.(*goapi.Object)
		}
		return list, nil
	case string:
		return make([]device, utf8.RuneCountInString(t)), nil
	}
	return nil, errShape
}

type distribution struct {
	Total     int    `json:"total"`
	Platforms *tally `json:"platforms"`
	Archs     *tally `json:"archs"`
	Versions  *tally `json:"versions"`
}

// distribution answers GET /api/panel/cross-platform/distribution: the
// devices counted by platform, architecture and client version. When the
// API server refuses the list, the answer has its status.
func (h *handlers) distribution(w http.ResponseWriter, r *http.Request) {
	out, err := h.countDistribution(r)
	if err != nil {
		status := http.StatusInternalServerError
		if s := goapi.AsError(err).Status(); s != 0 {
			status = s
		}
		kit.JSON(w, r, status, failure{Error: "Failed to fetch platform data"})
		return
	}
	kit.JSON(w, r, http.StatusOK, out)
}

func (h *handlers) countDistribution(r *http.Request) (*distribution, error) {
	devices, err := h.devices(r)
	if err != nil {
		return nil, err
	}
	out := &distribution{Platforms: newTally(), Archs: newTally(), Versions: newTally()}
	for _, d := range devices {
		out.Total++
		platform, ok := lowerText(firstTruthy(d.fields.Get("platform"), d.fields.Get("os")), "Unknown")
		if !ok {
			return nil, errShape
		}
		arch, ok := lowerText(d.fields.Get("arch"), "unknown")
		if !ok {
			return nil, errShape
		}
		version := "unknown"
		if v := d.fields.Get("version"); goapi.Truthy(v) {
			version = jsreq.String(v)
		}
		out.Platforms.add(platformKey(platform))
		out.Archs.add(arch)
		out.Versions.add(version)
	}
	return out, nil
}

func firstTruthy(values ...any) any {
	for _, v := range values {
		if goapi.Truthy(v) {
			return v
		}
	}
	return nil
}

// lowerText is (v || fallback).toLowerCase(). ok is false for a value
// that is set but no text: JavaScript has no toLowerCase for it.
func lowerText(v any, fallback string) (string, bool) {
	if !goapi.Truthy(v) {
		return strings.ToLower(fallback), true
	}
	s, ok := v.(string)
	return strings.ToLower(s), ok
}

// platformKey sorts a platform name in lower case into the families the
// page shows. A name of no family stays as it is; no name at all is
// "other".
func platformKey(platform string) string {
	has := func(part string) bool { return strings.Contains(platform, part) }
	switch {
	case has("windows"):
		return "windows"
	case has("linux"):
		return "linux"
	case has("mac"), has("darwin"):
		return "macos"
	case has("android"):
		return "android"
	case has("ios"), has("iphone"), has("ipad"):
		return "ios"
	case has("chrome"):
		return "chromeos"
	case platform != "unknown":
		return platform
	}
	return "other"
}

// capabilities answers GET /api/panel/cross-platform/capabilities/{deviceId}
// with the API server's answer for the device, status and body.
func (h *handlers) capabilities(w http.ResponseWriter, r *http.Request) {
	jsreq.ProxyToGo(w, r, h.api, http.MethodGet, func() (string, error) {
		segment, err := jsreq.SafeSegment(r.PathValue("deviceId"), "deviceId")
		if err != nil {
			return "", err
		}
		return "/peers/" + segment, nil
	})
}

type protocols struct {
	TotalOnline int    `json:"totalOnline"`
	Codecs      *tally `json:"codecs"`
	Features    *tally `json:"features"`
}

// protocols answers GET /api/panel/cross-platform/protocols: the codecs
// and features the devices that are online name, counted.
func (h *handlers) protocols(w http.ResponseWriter, r *http.Request) {
	devices, err := h.devices(r)
	if err != nil {
		kit.JSON(w, r, http.StatusInternalServerError, failure{Error: "Failed to fetch protocol data"})
		return
	}
	out := protocols{Codecs: newTally(), Features: newTally()}
	for _, d := range devices {
		if !goapi.Truthy(d.fields.Get("live_online")) && d.fields.Get("status") != "ONLINE" {
			continue
		}
		out.TotalOnline++
		out.Codecs.addEach(d.fields.Get("supported_codecs"))
		out.Features.addEach(d.fields.Get("supported_features"))
	}
	kit.JSON(w, r, http.StatusOK, out)
}
