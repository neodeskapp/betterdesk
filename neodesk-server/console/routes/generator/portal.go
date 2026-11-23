package generator

import (
	"net/http"
	"path/filepath"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/branding"
	gen "github.com/neodeskapp/neodesk/neodesk-server/console/generator"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
)

// The public download portal of a bundle: /api/d/{publicId}/... answer
// without a session, the slug or bundle id being the access token. An
// unknown or revoked bundle is 404 {"success":false}.

type bare struct {
	Success bool `json:"success"`
}

func (h *handlers) notFoundBare(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusNotFound, bare{})
}

func (h *handlers) failBare(w http.ResponseWriter, r *http.Request, what string, err error) {
	h.log.Error("[generator] "+what+" error:", err.Error())
	kit.JSON(w, r, http.StatusInternalServerError, bare{})
}

// liveBundle is the bundle of the address, nil when it is unknown or
// revoked.
func (h *handlers) liveBundle(r *http.Request) (*gen.Bundle, error) {
	row, err := h.gen.GetBundleByPublicID(r.Context(), r.PathValue("publicId"))
	if err != nil || row == nil || row.IsRevoked() {
		return nil, err
	}
	return row, nil
}

// manifest answers GET /api/d/{publicId}/manifest: every installer the
// install can build, with its build's state, size and checksum.
func (h *handlers) manifest(w http.ResponseWriter, r *http.Request) {
	row, err := h.liveBundle(r)
	if err != nil {
		h.failBare(w, r, "manifest", err)
		return
	}
	if row == nil {
		h.notFoundBare(w, r)
		return
	}
	builds, err := h.gen.ListBuildsForHash(r.Context(), row.ActiveBuildHash())
	if err != nil {
		h.failBare(w, r, "manifest", err)
		return
	}
	byKey := map[string]*gen.Build{}
	for _, b := range h.filterAvailableBuilds(builds) {
		byKey[b.Key()] = b
	}
	platforms := []any{}
	for _, p := range h.gen.AvailablePlatforms() {
		entry := goapi.NewObject("platform", p.Platform, "arch", p.Arch, "format", p.Format, "label", p.Label)
		if b := byKey[p.Key()]; b != nil {
			var sum any
			if b.ArtifactSHA256 != "" {
				sum = b.ArtifactSHA256
			}
			entry.Set("status", statusValue(b))
			entry.Set("size", b.Size())
			entry.Set("sha256", sum)
		} else {
			entry.Set("status", "pending")
		}
		platforms = append(platforms, entry)
	}
	ok(w, r, goapi.NewObject(
		"bundle_id", row.BundleID,
		"public_id", row.PublicID(),
		"name", row.Name,
		"product_type", gen.NormalizeProductType(row.ProductType),
		"platforms", platforms,
	))
}

// statusValue is the build row's status column as it was stored.
func statusValue(b *gen.Build) any { return b.Status }

// page answers GET /api/d/{publicId}/page: what the download page shows.
// The bundle's colour is used for the button only when white text on it
// is readable; contact links are http(s) only.
func (h *handlers) page(w http.ResponseWriter, r *http.Request) {
	row, err := h.liveBundle(r)
	if err != nil {
		h.failBare(w, r, "page", err)
		return
	}
	if row == nil {
		h.notFoundBare(w, r)
		return
	}
	brand := gen.PublicBrandingView(gen.ParseStoredBranding(row.Branding))
	if !gen.Truthy(brand) {
		brand = goapi.NewObject()
	}
	builds, err := h.gen.ListBuildsForHash(r.Context(), row.ActiveBuildHash())
	if err != nil {
		h.failBare(w, r, "page", err)
		return
	}
	statusOf := map[string]string{}
	for _, b := range h.filterAvailableBuilds(builds) {
		statusOf[b.Key()] = b.Status
	}
	gb := h.currentBranding(r)
	logo := jsString(or(prop(brand, "logo_data_url"), pick(gb.Text("logoType") == "image", or(gb.Text("logoUrl"), ""), "")))
	background, _ := branding.BuildBackgroundValue(value(gb, "agentBgType"), value(gb, "agentBgColor"), value(gb, "agentBgGradient"), value(gb, "agentBgImageUrl"))
	accent := ""
	if gen.ReadableOnWhite(prop(brand, "primary_color")) {
		accent = jsTrim(jsString(prop(brand, "primary_color")))
	}
	var defaultLang any
	if v := prop(brand, "default_lang"); gen.Truthy(v) {
		defaultLang = jsString(v)
	}
	platforms := []any{}
	for _, p := range h.gen.AvailablePlatforms() {
		status := statusOf[p.Key()]
		if status == "" {
			status = "pending"
		}
		platforms = append(platforms, goapi.NewObject("platform", p.Platform, "arch", p.Arch, "format", p.Format, "label", p.Label, "status", status))
	}
	poweredBy, _ := gb.Get("agentShowPoweredBy")
	w.Header().Set("Cache-Control", "no-store")
	ok(w, r, goapi.NewObject(
		"publicId", row.PublicID(),
		"companyName", jsString(or(or(prop(brand, "company_name"), value(gb, "appName")), "NeoDesk Support")),
		"shortText", jsString(or(prop(brand, "short_text"), "")),
		"logo", logo,
		"contact", goapi.NewObject(
			"email", jsString(or(prop(brand, "contact_email"), "")),
			"phone", jsString(or(prop(brand, "contact_phone"), "")),
			"url", gen.HTTPURLOrEmpty(prop(brand, "contact_url")),
		),
		"accent", accent,
		"background", background,
		"showPoweredBy", poweredBy != "false",
		"product", goapi.NewObject(
			"type", gen.NormalizeProductType(row.ProductType),
			"label", jsString(or(prop(brand, "product_label"), "")),
			"hidden", gen.Truthy(prop(brand, "hide_product_type")),
		),
		"defaultLang", defaultLang,
		"platforms", platforms,
	))
}

// currentBranding is the console's appearance settings as last saved.
func (h *handlers) currentBranding(r *http.Request) *branding.Object {
	if h.branding == nil {
		return branding.DefaultBranding()
	}
	return h.branding.GetBranding()
}

// value is a setting as JavaScript read it: undefined (nil) when it is
// not there.
func value(o *branding.Object, key string) any {
	v, _ := o.Get(key)
	return v
}

func pick(cond bool, a, b any) any {
	if cond {
		return a
	}
	return b
}

// pendingAnswer is the answer while an installer is not built.
type pendingAnswer struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Status  string `json:"status"`
}

// download answers GET /api/d/{publicId}/download/{platform}/{arch}/{format}
// with the installer as an attachment, and counts the download. Without
// a ready build it is 503 build_pending.
func (h *handlers) download(w http.ResponseWriter, r *http.Request) {
	row, err := h.liveBundle(r)
	if err != nil {
		h.failBare(w, r, "download", err)
		return
	}
	if row == nil {
		h.notFoundBare(w, r)
		return
	}
	build, err := h.gen.ReadyArtifact(r.Context(), row.ActiveBuildHash(), r.PathValue("platform"), r.PathValue("arch"), r.PathValue("format"))
	if err != nil {
		h.failBare(w, r, "download", err)
		return
	}
	if build == nil || build.ArtifactPath == "" {
		kit.JSON(w, r, http.StatusServiceUnavailable, pendingAnswer{Error: "build_pending", Status: "pending"})
		return
	}
	if err := h.gen.IncrementDownload(r.Context(), row.BundleID); err != nil {
		h.failBare(w, r, "download", err)
		return
	}
	abs, err := filepath.Abs(build.ArtifactPath)
	if err != nil {
		abs = filepath.Clean(build.ArtifactPath)
	}
	if inHiddenFolder(abs) {
		// The old file sender treated every part of an absolute path
		// like a file name and did not send from below a dot folder.
		h.errors.ServerError(w, r, errNotFound, http.StatusNotFound)
		return
	}
	root, err := filepath.Abs(h.gen.ArtifactDir())
	if err != nil {
		root = h.gen.ArtifactDir()
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		h.failBare(w, r, "download", err)
		return
	}
	h.artifacts.ServeName(w, r, filepath.ToSlash(rel), nil)
}

type notFoundError struct{}

func (notFoundError) Error() string { return "Not Found" }

var errNotFound error = notFoundError{}

// inHiddenFolder reports a path with a part that starts with a dot.
func inHiddenFolder(abs string) bool {
	for _, part := range strings.Split(filepath.ToSlash(abs), "/") {
		if len(part) > 1 && part[0] == '.' {
			return true
		}
	}
	return false
}
