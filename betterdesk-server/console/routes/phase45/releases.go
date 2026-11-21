package phase45

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"regexp"
	"time"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/agentreleases"
	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/devicegroups"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/goapipath"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
)

type releaseList struct {
	Success  bool                    `json:"success"`
	Releases []agentreleases.Release `json:"releases"`
}

// listReleases answers GET /api/agent-releases: the releases in the local
// cache, highest tag first.
func (h *handlers) listReleases(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, releaseList{Success: true, Releases: h.releases.ListCached()})
}

type signingStatus struct {
	Success     bool   `json:"success"`
	Fingerprint string `json:"fingerprint"`
	Source      string `json:"source"`
}

// signingStatus answers GET /api/agent-releases/signing-status: which key
// release manifests must be signed with, and where it comes from.
func (h *handlers) signingStatus(w http.ResponseWriter, r *http.Request) {
	source := "bundled"
	if h.releases.KeyFromEnvironment() {
		source = "environment"
	}
	kit.JSON(w, r, http.StatusOK, signingStatus{Success: true, Fingerprint: h.releases.SigningKeyFingerprint(), Source: source})
}

type syncAnswer struct {
	Success  bool   `json:"success"`
	Tag      string `json:"tag"`
	Manifest any    `json:"manifest"`
}

// importTimeout is how long a release import may take. It runs on its own
// deadline, not the request's, so a browser or a proxy that gives up on
// the request does not stop it.
const importTimeout = 30 * time.Minute

// syncRelease answers POST /api/agent-releases/sync: it imports the newest
// release from GitHub. Every failure is 502 with the reason.
func (h *handlers) syncRelease(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), importTimeout)
	defer cancel()
	imported, err := h.releases.ImportLatest(ctx)
	if err != nil {
		fail(w, r, http.StatusBadGateway, err.Error())
		return
	}
	_ = h.d.LogAction(r, "generic_agent_release_imported", "Imported generic Support Agent release "+imported.Tag)
	kit.JSON(w, r, http.StatusCreated, syncAnswer{Success: true, Tag: imported.Tag, Manifest: imported.Manifest})
}

var reDeviceID = regexp.MustCompile(`^[A-Za-z0-9_.:-]{3,128}$`)

type activated struct {
	Success  bool   `json:"success"`
	DeviceID string `json:"device_id"`
}

// activate answers POST /api/agent-pairing/activate {device_id,
// display_name, folder_id}: a generic agent that waits for approval is
// approved under the name, one that is known already gets the name, and
// the device goes into the folder when one is named. A known device must
// be in the account's device scope and the folder open to it. A failure
// of the API server is answered with its own answer, any other with 502.
func (h *handlers) activate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	deviceID, err1 := textOr(field(r, "device_id"), "")
	displayName, err2 := textOr(field(r, "display_name"), "")
	if err := errors.Join(err1, err2); err != nil {
		// Node threw here outside its try and never answered
		h.d.WebUI.ServerError(w, r, err, http.StatusInternalServerError)
		return
	}
	deviceID = goapipath.Slice(branding.Trim(deviceID), 128)
	displayName = goapipath.Slice(branding.Trim(displayName), 120)
	folder := field(r, "folder_id")
	if folder == branding.Undefined {
		folder = nil
	}
	folderID, ok := jsreq.ParseInt(folder, 10)
	if !ok {
		folderID = 0
	}
	if !reDeviceID.MatchString(deviceID) {
		fail(w, r, http.StatusBadRequest, "invalid_device_id")
		return
	}
	pending := h.d.GoAPI.GetEnrollmentPending(ctx)
	waiting := false
	data, _ := pending.Get("data").([]any)
	for _, item := range data {
		if item == nil {
			fail(w, r, http.StatusBadGateway, "Cannot read properties of null (reading 'device_id')")
			return
		}
		if jsreq.StringOr(goapi.Dig(item, "device_id"), "") == deviceID {
			waiting = true
			break
		}
	}
	if !waiting && !h.mayRename(w, r, deviceID) || folderID > 0 && !h.mayFill(w, r, folderID) {
		return
	}
	var result *goapi.Object
	if waiting {
		result = h.d.GoAPI.ApproveEnrollment(ctx, deviceID, displayName, "standard", "")
	} else {
		result = h.d.GoAPI.UpdatePeer(ctx, deviceID, goapi.NewObject("display_name", displayName, "note", displayName))
	}
	if !goapi.Truthy(result.Get("success")) {
		kit.JSON(w, r, http.StatusBadGateway, result)
		return
	}
	if folderID > 0 {
		if err := h.d.DeviceGroups.AssignDeviceToFolder(ctx, jsbind.String(deviceID), jsbind.Number(folderID)); err != nil {
			fail(w, r, http.StatusBadGateway, jsbind.Message(errors.Unwrap(err)))
			return
		}
	}
	if err := h.d.LogAction(r, "generic_agent_activated", "Activated generic Support Agent "+deviceID); err != nil {
		fail(w, r, http.StatusBadGateway, jsbind.Message(errors.Unwrap(err)))
		return
	}
	kit.JSON(w, r, http.StatusOK, activated{Success: true, DeviceID: deviceID})
}

// mayRename reports whether the account may change a known device: one
// its device scope lets it see. An id no device has passes; the API
// server refuses it. A refusal is answered here.
func (h *handlers) mayRename(w http.ResponseWriter, r *http.Request, deviceID string) bool {
	ctx := r.Context()
	user := kit.User(r)
	scope, err := h.d.DeviceGroups.DeviceScopeForUser(ctx, user, nil)
	if err == nil && scope == nil {
		return true
	}
	allowed := true
	if err == nil {
		if device := h.d.Backend.GetDeviceByID(ctx, deviceID, true); device != nil {
			allowed, err = h.d.DeviceGroups.UserCanAccessDevice(ctx, user, device)
		}
	}
	if err != nil {
		h.d.Log.Error("Agent activation scope error:", err.Error())
		kit.Fail(w, r, http.StatusInternalServerError, kit.T(r, "errors.server_error", nil))
		return false
	}
	if !allowed {
		kit.Fail(w, r, http.StatusForbidden, kit.T(r, "errors.forbidden", nil))
	}
	return allowed
}

// mayFill reports whether the account may put a device into the folder:
// one that is open to it (access.GroupRightsForUser). A refusal is
// answered here.
func (h *handlers) mayFill(w http.ResponseWriter, r *http.Request, folderID float64) bool {
	ctx := r.Context()
	group, err := h.d.Access.DeviceGroupByGUID(ctx, access.FolderGroupGUID(int64(folderID)))
	var rights access.GroupRights
	if err == nil {
		rights, err = h.d.Access.GroupRightsForUser(ctx, devicegroups.ScopeUser(kit.User(r)), group)
	}
	if err != nil {
		h.d.Log.Error("Agent activation scope error:", err.Error())
		kit.Fail(w, r, http.StatusInternalServerError, kit.T(r, "errors.server_error", nil))
		return false
	}
	if !rights.Change {
		kit.Fail(w, r, http.StatusForbidden, kit.T(r, "errors.no_permission", nil))
	}
	return rights.Change
}

// download answers GET /downloads/generic-agent/{tag}/{name} with a file
// of a cached release, as an attachment. Anyone may load it.
func (h *handlers) download(w http.ResponseWriter, r *http.Request) {
	file := h.releases.ResolveAsset(r.PathValue("tag"), r.PathValue("name"))
	if file == "" {
		httpx.WriteBody(w, r, http.StatusNotFound, "text/html; charset=utf-8", []byte("Agent release asset not found"))
		return
	}
	failed := func(w http.ResponseWriter, r *http.Request, status int, err error) {
		h.d.WebUI.ServerError(w, r, err, status)
	}
	files := httpx.NewStatic(filepath.Dir(file), httpx.StaticOptions{
		NoFallthrough: true, NoIndex: true, NoRedirect: true, OnError: failed,
	})
	w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(file)+`"`)
	files.ServeName(w, r, filepath.Base(file), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		failed(w, r, http.StatusNotFound, errors.New("Not Found"))
	}))
}

type installerList struct {
	Success    bool  `json:"success"`
	Release    any   `json:"release"`
	Reason     any   `json:"reason"`
	Installers []any `json:"installers"`
}

// installers answers GET /api/portal/installers, for the public portal:
// the files of the newest cached release with their addresses on this
// server. An empty list says why: no release was ever imported (no
// cache folder), or the cache holds no usable release. It counts against
// the file limit (fileAccessLimiter), as the downloads do.
func (h *handlers) installers(w http.ResponseWriter, r *http.Request) {
	scheme := "http"
	if kit.Secure(r) {
		scheme = "https"
	}
	base := scheme + "://" + r.Host
	cached := h.releases.ListCached()
	missing := !h.releases.CacheExists()
	answer := installerList{Success: true, Installers: []any{}}
	if len(cached) == 0 {
		answer.Reason = "release_cache_incomplete"
		if missing {
			answer.Reason = "no_release_imported"
		}
		kit.JSON(w, r, http.StatusOK, answer)
		return
	}
	latest := cached[0]
	assets, ok := goapi.Dig(latest.Manifest, "assets").([]any)
	if !ok {
		// Node failed here without an answer
		h.d.WebUI.ServerError(w, r, errors.New("latest.manifest.assets.map is not a function"), http.StatusInternalServerError)
		return
	}
	for _, asset := range assets {
		object, isObject := asset.(*goapi.Object)
		if asset == nil {
			h.d.WebUI.ServerError(w, r, errors.New("Cannot read properties of null (reading 'platform')"), http.StatusInternalServerError)
			return
		}
		item := goapi.NewObject()
		for _, key := range []string{"platform", "arch"} {
			if isObject && object.Has(key) {
				item.Set(key, object.Get(key))
			}
		}
		name := "undefined"
		if isObject && object.Has("name") {
			name = jsreq.String(object.Get("name"))
		}
		item.Set("url", base+"/downloads/generic-agent/"+goapi.EncodeURIComponent(latest.Tag)+"/"+goapi.EncodeURIComponent(name))
		format := goapi.Dig(asset, "format")
		if !goapi.Truthy(format) {
			format = "portable"
		}
		item.Set("format", format)
		item.Set("release", latest.Tag)
		answer.Installers = append(answer.Installers, item)
	}
	answer.Release = latest.Tag
	if latest.Tag == "" {
		answer.Release = nil
	}
	kit.JSON(w, r, http.StatusOK, answer)
}
