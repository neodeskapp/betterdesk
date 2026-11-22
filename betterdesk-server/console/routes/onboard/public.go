package onboard

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/unitronix/betterdesk-server/console/authn"
	"github.com/unitronix/betterdesk-server/console/clientrelease"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/onboard"
)

// publicServer is the console as the public page names it.
type publicServer struct {
	Name  string `json:"name"`
	Host  string `json:"host"`
	HTTPS bool   `json:"https"`
}

// publicDownload is one package the page offers.
type publicDownload struct {
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	Format  string `json:"format"`
	Version string `json:"version"`
	Size    int64  `json:"size"`
	File    string `json:"file"`
	URL     string `json:"url"`
}

// publicAnswer is GET /api/onboard/public/{code}: what the pages /join/
// and /s/ show.
type publicAnswer struct {
	Success        bool             `json:"success"`
	Kind           string           `json:"kind"`
	Code           string           `json:"code"`
	DisplayCode    string           `json:"display_code"`
	ExpiresAt      string           `json:"expires_at"`
	Server         publicServer     `json:"server"`
	Publisher      string           `json:"publisher"`
	Supporter      string           `json:"supporter"`
	Company        string           `json:"company"`
	Downloads      []publicDownload `json:"downloads"`
	InstallCommand string           `json:"install_command,omitempty"`
	RunCommand     string           `json:"run_command,omitempty"`
	DeepLink       string           `json:"deep_link"`
	ReleaseError   string           `json:"release_error,omitempty"`
}

// lookupPublic reads the code of the path and answers the one refusal
// when it cannot be used.
func (h *handlers) lookupPublic(w http.ResponseWriter, r *http.Request) (string, onboard.Code, bool) {
	code := onboard.Normalize(r.PathValue("code"))
	if !onboard.ValidFormat(code) {
		unusable(w, r)
		return "", onboard.Code{}, false
	}
	rec, err := h.codes.Lookup(r.Context(), code)
	if errors.Is(err, onboard.ErrUnusable) {
		unusable(w, r)
		return "", onboard.Code{}, false
	}
	if err != nil {
		h.serverError(w, r, "Look up code:", err)
		return "", onboard.Code{}, false
	}
	return code, rec, true
}

// publicCode answers GET /api/onboard/public/{code}.
func (h *handlers) publicCode(w http.ResponseWriter, r *http.Request) {
	code, rec, ok := h.lookupPublic(w, r)
	if !ok {
		return
	}
	c := h.consoleOf(r)
	out := publicAnswer{
		Success: true, Kind: rec.Kind, Code: code, DisplayCode: onboard.Display(code), ExpiresAt: rec.ExpiresAt,
		Server:    publicServer{Name: h.serverName(), Host: c.Host, HTTPS: c.HTTPS},
		Publisher: clientrelease.Publisher, Downloads: []publicDownload{},
		DeepLink: c.DeepLink(rec.Kind, code),
	}
	offers, err := h.releases.Offers(r.Context(), rec.Kind)
	if err != nil {
		out.ReleaseError = clientrelease.ErrUnavailable.Error()
	}
	for _, o := range offers {
		q := url.Values{"os": {o.OS}, "arch": {o.Arch}, "format": {o.Format}}
		out.Downloads = append(out.Downloads, publicDownload{
			OS: o.OS, Arch: o.Arch, Format: o.Format, Version: o.Version, Size: o.Size,
			File: c.FileName(rec.Kind, code, o.Ext()),
			URL:  "/api/onboard/public/" + code + "/download?" + q.Encode(),
		})
	}
	if rec.Kind == onboard.KindJoin {
		out.InstallCommand = "curl -fsSL '" + c.Base() + "/join/" + code + "/install.sh' | sudo sh"
	} else {
		out.RunCommand = "curl -fsSL '" + c.Base() + "/s/" + code + "/run.sh' | sh"
	}
	// Who sent the link: the page names them, as the mail does
	out.Supporter = h.personName(r.Context(), rec.CreatedByID, rec.CreatedBy)
	out.Company = h.serverName()
	kit.JSON(w, r, http.StatusOK, out)
}

// contentTypes are the types the packages are served with.
var contentTypes = map[string]string{
	"exe":    "application/vnd.microsoft.portable-executable",
	"dmg":    "application/x-apple-diskimage",
	"deb":    "application/vnd.debian.binary-package",
	"rpm":    "application/x-rpm",
	"tar.gz": "application/gzip",
}

// download answers GET /api/onboard/public/{code}/download?os=&arch=&format=:
// the client package for that system under the code's file name. The
// code is not spent.
func (h *handlers) download(w http.ResponseWriter, r *http.Request) {
	code, rec, ok := h.lookupPublic(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	f, offer, err := h.releases.Open(r.Context(), rec.Kind, q.Get("os"), q.Get("arch"), q.Get("format"))
	switch {
	case errors.Is(err, clientrelease.ErrNoPackage):
		kit.JSON(w, r, http.StatusNotFound, refusal{Error: "no_package"})
		return
	case err != nil:
		if !errors.Is(err, clientrelease.ErrUnavailable) {
			h.log.Error("Open client package:", err)
		}
		kit.JSON(w, r, http.StatusServiceUnavailable, refusal{Error: clientrelease.ErrUnavailable.Error()})
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		h.serverError(w, r, "Read client package:", err)
		return
	}
	name := h.consoleOf(r).FileName(rec.Kind, code, offer.Ext())
	w.Header().Set("Content-Type", contentTypes[offer.Format])
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", info.ModTime(), f)
}

// scriptGone is what a script address answers for a code that cannot be
// used; curl -f stops on the status.
func scriptGone(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNotFound)
	_, _ = w.Write([]byte("This link has expired or was already used.\n"))
}

// script answers a shell script for an open code of the kind.
func (h *handlers) script(w http.ResponseWriter, r *http.Request, kind string, build func(base, code string) string) {
	code := onboard.Normalize(r.PathValue("code"))
	if !onboard.ValidFormat(code) {
		scriptGone(w)
		return
	}
	rec, err := h.codes.Lookup(r.Context(), code)
	if err != nil || rec.Kind != kind {
		if err != nil && !errors.Is(err, onboard.ErrUnusable) {
			h.log.Error("Look up code:", err)
		}
		scriptGone(w)
		return
	}
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(build(h.consoleOf(r).Base(), code)))
}

// installScript answers GET /join/{code}/install.sh: the script that
// installs the client package and joins this server with the code.
func (h *handlers) installScript(w http.ResponseWriter, r *http.Request) {
	h.script(w, r, onboard.KindJoin, onboard.InstallScript)
}

// runScript answers GET /s/{code}/run.sh: the script that starts quick
// support from the portable package, without installing anything.
func (h *handlers) runScript(w http.ResponseWriter, r *http.Request) {
	h.script(w, r, onboard.KindSupport, onboard.RunScript)
}

// joinDevice is the body of POST /api/onboard/join.
type joinDevice struct {
	Code      string `json:"code"`
	DeviceID  string `json:"device_id"`
	UUID      string `json:"uuid"`
	Hostname  string `json:"hostname"`
	Platform  string `json:"platform"`
	Version   string `json:"version"`
	PublicKey string `json:"public_key"`
}

// joinedDevice is the device as the client learns it.
type joinedDevice struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	FolderID *int64 `json:"folder_id"`
}

// joinAnswer is POST /api/onboard/join.
type joinAnswer struct {
	Success     bool         `json:"success"`
	Server      serverAnswer `json:"server"`
	DeviceToken string       `json:"device_token"`
	Device      joinedDevice `json:"device"`
	Console     string       `json:"console"`
}

// join answers POST /api/onboard/join with the body {code, device_id,
// uuid, hostname, platform, version, public_key}: the client spends one
// use of an invite, its device is approved with the invite's name and put
// into its folder, and it gets how to reach the server and a device token,
// which no account holds (authn.IssueDeviceToken).
func (h *handlers) join(w http.ResponseWriter, r *http.Request) {
	var body joinDevice
	if err := kit.Bind(r, &body); err != nil {
		appFail(w, r, http.StatusBadRequest, "invalid_body", "")
		return
	}
	if !onboard.ValidFormat(body.Code) {
		unusable(w, r)
		return
	}
	deviceID := strings.TrimSpace(body.DeviceID)
	if !authn.IsDeviceID(deviceID) {
		appFail(w, r, http.StatusBadRequest, "invalid_device", "invalid device_id")
		return
	}
	ctx := r.Context()
	rec, _, err := h.codes.Redeem(ctx, body.Code, onboard.KindJoin, &onboard.Device{
		DeviceID: deviceID, UUID: body.UUID, Hostname: body.Hostname, Platform: body.Platform,
		Version: body.Version, PublicKey: body.PublicKey,
	})
	var refused *onboard.RefusedError
	var invalid *onboard.InvalidDeviceError
	switch {
	case errors.Is(err, onboard.ErrUnusable):
		unusable(w, r)
		return
	case errors.As(err, &refused):
		appFail(w, r, http.StatusForbidden, "device_refused", refused.Reason)
		return
	case errors.As(err, &invalid):
		appFail(w, r, http.StatusBadRequest, "invalid_device", invalid.Reason)
		return
	case err != nil:
		h.log.Error("Redeem invite:", err)
		appFail(w, r, http.StatusInternalServerError, "server_error", "")
		return
	}

	device := joinedDevice{ID: deviceID}
	if rec.MaxUses == 1 {
		device.Name = rec.Name
	}
	if rec.FolderID > 0 && h.d.DeviceGroups != nil {
		if err := h.d.DeviceGroups.AssignDeviceToFolder(ctx, jsbind.String(deviceID), jsbind.Number(float64(rec.FolderID))); err != nil {
			h.log.Error("Put joined device into its folder:", err)
		} else {
			folder := rec.FolderID
			device.FolderID = &folder
		}
	}
	creator := rec.CreatedByID
	if creator == 0 && h.d.Authn != nil {
		if account, err := h.d.Authn.UserByUsername(ctx, rec.CreatedBy); err == nil && account != nil {
			creator = account.ID.V
		}
	}
	if creator == 0 || h.d.Authn == nil {
		h.log.Error("Joined device without a device token: the invite's account is gone", deviceID)
		appFail(w, r, http.StatusInternalServerError, "server_error", "")
		return
	}
	token, err := h.d.Authn.IssueDeviceToken(ctx, deviceID, "Issued at a join with invite "+rec.ID)
	if err == nil && token == "" {
		err = errors.New("the database has no device_tokens")
	}
	if err != nil {
		h.log.Error("Issue device token for joined device:", err)
		appFail(w, r, http.StatusInternalServerError, "server_error", "")
		return
	}
	h.auditAs(r, creator, "invite_redeemed", "Device "+deviceID+" joined with invite "+rec.ID)
	kit.JSON(w, r, http.StatusOK, joinAnswer{
		Success: true, Server: h.serverOf(r), DeviceToken: token, Device: device, Console: h.consoleOf(r).Base(),
	})
}
