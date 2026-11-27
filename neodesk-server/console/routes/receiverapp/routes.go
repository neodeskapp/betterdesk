// Package receiverapp holds the routes of Devices › Add › Receiver app:
// what an admin needs to install the NeoDesk app (the receiver) on a
// computer without anyone clicking through it, for this server.
//
//   - GET /api/panel/receiver/setup: the values of the silent install
//     (ID server, key, API address) and the MSI of the newest imported
//     generic Support Agent release, if there is one.
//   - POST /api/panel/receiver/installer {token, group?, arch?}: a zip of
//     that MSI as neodesk-support.msi and an install.cmd that runs
//     msiexec with the MSI properties SERVER, KEY, API, TOKEN and GROUP.
//
// The MSI is the release's own, unchanged: the console can't write the
// signed custom.txt into the template MSI's cabinet (no MSI or cabinet
// writer, and the package would lose its signature), so the server
// settings travel as MSI properties. The property contract is in
// lead/receiver-app/MSI-CONTRACT.md and the PR that added this package.
// Both routes need enrollment.manage, as the device tokens do.
package receiverapp

import (
	"archive/zip"
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/agentreleases"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsreq"
	"github.com/neodeskapp/neodesk/neodesk-server/console/serverinfo"
)

// MSIName is the name the MSI has in the zip and in the command.
const MSIName = "neodesk-support.msi"

// Archs are the processors an MSI is offered for, as the release names
// them, the first the default.
var Archs = []string{"x86_64", "aarch64"}

type handlers struct {
	d        *kit.Deps
	releases *agentreleases.Service
	log      *kit.Logger
}

// Register adds the group's routes.
func Register(d *kit.Deps) {
	getenv := d.Config.Getenv
	if getenv == nil {
		getenv = func(string) string { return "" }
	}
	h := &handlers{
		d: d,
		releases: agentreleases.New(agentreleases.Options{
			DataDir:   d.Config.DataDir,
			Repo:      getenv("NEODESK_SUPPORT_RELEASE_REPO"),
			PublicKey: getenv("NEODESK_SUPPORT_RELEASE_PUBLIC_KEY"),
		}),
		log: d.Log.Child("ReceiverApp"),
	}
	manage := d.RequirePermission("enrollment.manage")
	d.Router.Get("/api/panel/receiver/setup", h.setup, d.RequireAuth, manage)
	d.Router.Post("/api/panel/receiver/installer", h.installer, d.RequireAuth, manage)
}

// Server is how the installed app reaches this server: the MSI
// properties SERVER, KEY and API.
type Server struct {
	IDServer  string `json:"id_server"`
	Key       string `json:"key"`
	APIServer string `json:"api_server"`
}

func (h *handlers) serverOf(r *http.Request) Server {
	if h.d.ServerInfo == nil {
		return Server{}
	}
	ep := h.d.ServerInfo.ResolveRustDeskEndpoints(serverinfo.RequestHostOf(r), "")
	return Server{IDServer: ep.Host, Key: string(h.d.ServerInfo.ResolvePublicKey(r.Context())), APIServer: ep.API}
}

// Package is an MSI of the release.
type Package struct {
	Arch string `json:"arch"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// Release is the release the MSI comes from.
type Release struct {
	Tag      string    `json:"tag"`
	Packages []Package `json:"packages"`
}

type setupAnswer struct {
	Success bool     `json:"success"`
	Server  Server   `json:"server"`
	Release *Release `json:"release"`
}

// msiOf returns the newest cached release with its Windows MSIs, nil when
// none is imported or it has no MSI.
func (h *handlers) msiOf() *Release {
	cached := h.releases.ListCached()
	if len(cached) == 0 {
		return nil
	}
	latest := cached[0]
	assets, _ := goapi.Dig(latest.Manifest, "assets").([]any)
	out := &Release{Tag: latest.Tag, Packages: []Package{}}
	for _, arch := range Archs {
		for _, asset := range assets {
			if jsreq.String(goapi.Dig(asset, "platform")) != "windows" || jsreq.String(goapi.Dig(asset, "format")) != "msi" ||
				jsreq.String(goapi.Dig(asset, "arch")) != arch {
				continue
			}
			name := jsreq.String(goapi.Dig(asset, "name"))
			path := h.releases.ResolveAsset(latest.Tag, name)
			if path == "" {
				continue
			}
			info, err := os.Stat(path)
			if err != nil {
				continue
			}
			out.Packages = append(out.Packages, Package{Arch: arch, Name: name, Size: info.Size()})
			break
		}
	}
	if len(out.Packages) == 0 {
		return nil
	}
	return out
}

// setup answers GET /api/panel/receiver/setup.
func (h *handlers) setup(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, setupAnswer{Success: true, Server: h.serverOf(r), Release: h.msiOf()})
}

// refusal is {success:false, error, code}: error in the request's
// language (receiver_app.errors.<code>), the code itself without one.
type refusal struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Code    string `json:"code"`
}

func fail(w http.ResponseWriter, r *http.Request, status int, code string) {
	text := kit.T(r, "receiver_app.errors."+code, nil)
	if text == "" || text == "receiver_app.errors."+code {
		text = code
	}
	kit.JSON(w, r, status, refusal{Error: text, Code: code})
}

// tokenPattern is what an enrollment token looks like: the console makes
// 32 hex digits; older and scripted ones may use base64url.
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

// errTokenUnknown, errTokenUsed: a token no computer can join with.
var (
	errTokenUnknown = errors.New("token_unknown")
	errTokenUsed    = errors.New("token_used")
)

// checkToken reports whether a new computer can join with the token: it
// exists, is not revoked or expired, no computer joined with it yet and it
// has uses left.
func (h *handlers) checkToken(ctx context.Context, token string) error {
	var status, peer string
	var maxUses, useCount sql.NullInt64
	err := h.d.Store.QueryRow(ctx, `SELECT status, COALESCE(peer_id, ''), max_uses, use_count FROM device_tokens WHERE token_hash = ?`,
		HashToken(token)).Scan(&status, &peer, &maxUses, &useCount)
	if errors.Is(err, sql.ErrNoRows) {
		return errTokenUnknown
	}
	if err != nil {
		return err
	}
	switch {
	case status == "revoked" || status == "expired":
		return errTokenUnknown
	case peer != "" || maxUses.Int64 > 0 && useCount.Int64 >= maxUses.Int64:
		return errTokenUsed
	}
	return nil
}

// installer answers POST /api/panel/receiver/installer with the body
// {token, group?, arch?}: a zip with the MSI and install.cmd. group is a
// folder id (GROUP), arch one of Archs (x86_64 when left out).
//
//	token missing or of the wrong form         400 invalid_token
//	token no new computer can join with        400 token_unknown, token_used
//	group that names no folder                 400 invalid_group
//	no imported release with an MSI for arch   409 no_release
func (h *handlers) installer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
		Group any    `json:"group"`
		Arch  string `json:"arch"`
	}
	if err := kit.Bind(r, &body); err != nil {
		fail(w, r, http.StatusBadRequest, "invalid_body")
		return
	}
	token := strings.TrimSpace(body.Token)
	if !tokenPattern.MatchString(token) {
		fail(w, r, http.StatusBadRequest, "invalid_token")
		return
	}
	group, ok := h.folderID(r.Context(), body.Group)
	if !ok {
		fail(w, r, http.StatusBadRequest, "invalid_group")
		return
	}
	arch := body.Arch
	if arch == "" {
		arch = Archs[0]
	}
	if err := h.checkToken(r.Context(), token); errors.Is(err, errTokenUnknown) || errors.Is(err, errTokenUsed) {
		fail(w, r, http.StatusBadRequest, err.Error())
		return
	} else if err != nil {
		h.log.Error("Check the enrollment token:", err)
		fail(w, r, http.StatusInternalServerError, "server_error")
		return
	}
	rel := h.msiOf()
	var pkg *Package
	if rel != nil {
		for i := range rel.Packages {
			if rel.Packages[i].Arch == arch {
				pkg = &rel.Packages[i]
			}
		}
	}
	if pkg == nil {
		fail(w, r, http.StatusConflict, "no_release")
		return
	}
	msi, err := os.Open(h.releases.ResolveAsset(rel.Tag, pkg.Name))
	if err != nil {
		h.log.Error("Open the receiver MSI:", err)
		fail(w, r, http.StatusInternalServerError, "server_error")
		return
	}
	defer msi.Close()

	props := Properties{Server: h.serverOf(r), Token: token, Group: group}
	host := strings.Split(props.Server.IDServer, ":")[0]
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="neodesk-support-`+safeFileName(host)+`.zip"`)
	w.Header().Set("Cache-Control", "no-store")
	zw := zip.NewWriter(w)
	cmd, err := zw.Create("install.cmd")
	if err == nil {
		_, err = io.WriteString(cmd, InstallCmd(props))
	}
	if err == nil {
		var part io.Writer
		part, err = zw.CreateHeader(&zip.FileHeader{Name: MSIName, Method: zip.Store})
		if err == nil {
			_, err = io.Copy(part, msi)
		}
	}
	if err == nil {
		err = zw.Close()
	}
	if err != nil {
		h.log.Warn("Send the receiver installer:", err)
		return
	}
	_ = h.d.LogAction(r, "receiver_installer_downloaded", "Downloaded the receiver installer "+rel.Tag+" "+arch)
}

// folderID reads the body's group: nothing, a folder id as a number or
// text. It reports false for anything else or a folder that does not
// exist.
func (h *handlers) folderID(ctx context.Context, v any) (int64, bool) {
	var id int64
	switch g := v.(type) {
	case nil:
		return 0, true
	case float64:
		id = int64(g)
		if float64(id) != g {
			return 0, false
		}
	case string:
		if strings.TrimSpace(g) == "" {
			return 0, true
		}
		n, err := strconv.ParseInt(strings.TrimSpace(g), 10, 64)
		if err != nil {
			return 0, false
		}
		id = n
	default:
		return 0, false
	}
	if id == 0 {
		return 0, true
	}
	if id < 0 {
		return 0, false
	}
	var found int64
	if err := h.d.Store.QueryRow(ctx, `SELECT id FROM folders WHERE id = ?`, id).Scan(&found); err != nil {
		return 0, false
	}
	return id, true
}

var reUnsafeFileName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func safeFileName(s string) string {
	s = reUnsafeFileName.ReplaceAllString(s, "-")
	if s == "" {
		return "server"
	}
	return s
}
