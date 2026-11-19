package generator

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	gen "github.com/unitronix/betterdesk-server/console/generator"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
	"github.com/unitronix/betterdesk-server/console/serverinfo"
	"github.com/unitronix/betterdesk-server/console/store"
)

// failure is {"success":false,"error":...}.
type failure struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

func (h *handlers) fail(w http.ResponseWriter, r *http.Request, status int, key string) {
	kit.JSON(w, r, status, failure{Error: kit.T(r, key, nil)})
}

func (h *handlers) serverError(w http.ResponseWriter, r *http.Request, what string, err error) {
	h.log.Error("[generator] "+what+" error:", err.Error())
	h.fail(w, r, http.StatusInternalServerError, "errors.server_error")
}

// ok answers {"success":true,"data":data} with data as JSON.stringify
// wrote it.
func ok(w http.ResponseWriter, r *http.Request, data any) {
	kit.JSON(w, r, http.StatusOK, gen.JSON{V: goapi.NewObject("success", true, "data", data)})
}

// raw puts JSON that is ready into a value.
func raw(v json.Marshaler) json.RawMessage {
	b, err := v.MarshalJSON()
	if err != nil {
		return json.RawMessage("null")
	}
	return b
}

// timeJSON is a time column as the old route sent it.
func timeJSON(t store.Time) json.RawMessage { return raw(t) }

// serializeBundle is a bundle as the routes send it: its profile without
// the enrollment fields, its address, its product and generation.
func serializeBundle(b *gen.Bundle) any {
	if b == nil {
		return nil
	}
	publicID := b.PublicID()
	var commit, generation any
	if b.ClientCommit.String != "" {
		commit = b.ClientCommit.String
	}
	switch {
	case b.ActiveGenerationHash.String != "":
		generation = b.ActiveGenerationHash.String
	case b.BrandingHash != "":
		generation = b.BrandingHash
	}
	return goapi.NewObject(
		"bundle_id", b.BundleID,
		"slug", b.Slug.String,
		"public_id", publicID,
		"name", b.Name,
		"branding", gen.PublicBrandingView(gen.ParseStoredBranding(b.Branding)),
		"branding_hash", b.BrandingHash,
		"revoked", b.IsRevoked(),
		"legacy", !gen.IsSupportBundle(b.Branding),
		"download_count", downloadCount(b),
		"created_at", timeJSON(b.CreatedAt),
		"updated_at", timeJSON(b.UpdatedAt),
		"download_url", "/d/"+publicID,
		"product_type", gen.NormalizeProductType(b.ProductType),
		"client_commit", commit,
		"active_generation_hash", generation,
	)
}

// downloadCount is Number(row.download_count || 0).
func downloadCount(b *gen.Bundle) float64 {
	v := b.DownloadCount.Value()
	if !v.Truthy() {
		return 0
	}
	return v.ToNumber()
}

// availableKeys are platform/arch/format of the installers the install
// can build now.
func (h *handlers) availableKeys() map[string]bool {
	keys := map[string]bool{}
	for _, p := range h.gen.AvailablePlatforms() {
		keys[p.Key()] = true
	}
	return keys
}

// buildList is builds as the routes send them.
func buildList(builds []*gen.Build) []any {
	out := make([]any, 0, len(builds))
	for _, b := range builds {
		out = append(out, raw(b))
	}
	return out
}

// filterAvailableBuilds keeps the builds of installers the install can
// build now.
func (h *handlers) filterAvailableBuilds(builds []*gen.Build) []*gen.Build {
	keys := h.availableKeys()
	out := []*gen.Build{}
	for _, b := range builds {
		if keys[b.Key()] {
			out = append(out, b)
		}
	}
	return out
}

// listBundles answers GET /api/generator/bundles: the template
// generator's bundles, with ?includeLegacy=1 also the older ones, with
// ?includeRevoked=1 also the revoked ones.
func (h *handlers) listBundles(w http.ResponseWriter, r *http.Request) {
	rows, err := h.gen.ListBundles(r.Context(), jsreq.Query(r, "includeRevoked") == "1")
	if err != nil {
		h.serverError(w, r, "list bundles", err)
		return
	}
	includeLegacy := jsreq.Query(r, "includeLegacy") == "1"
	list := []any{}
	for _, row := range rows {
		if includeLegacy || gen.IsSupportBundle(row.Branding) {
			list = append(list, serializeBundle(row))
		}
	}
	ok(w, r, goapi.NewObject("bundles", list))
}

// getBundle answers GET /api/generator/bundles/{bundleId} with the
// bundle and the builds of the generation it hands out.
func (h *handlers) getBundle(w http.ResponseWriter, r *http.Request) {
	row, err := h.gen.GetBundle(r.Context(), r.PathValue("bundleId"))
	if err != nil {
		h.serverError(w, r, "get bundle", err)
		return
	}
	if row == nil {
		h.fail(w, r, http.StatusNotFound, "errors.not_found")
		return
	}
	bundle := serializeBundle(row).(*goapi.Object)
	builds, err := h.gen.ListBuildsForHash(r.Context(), row.ActiveBuildHash())
	if err != nil {
		h.serverError(w, r, "get bundle", err)
		return
	}
	bundle.Set("builds", buildList(h.filterAvailableBuilds(builds)))
	ok(w, r, goapi.NewObject("bundle", bundle))
}

// defaultsAnswer is the answer of GET /api/generator/defaults.
type defaultsAnswer struct {
	Success bool         `json:"success"`
	Data    defaultsData `json:"data"`
}

type defaultsData struct {
	ServerHost string `json:"server_host"`
	RelayHost  string `json:"relay_host"`
	UseHTTPS   bool   `json:"use_https"`
	APIPort    string `json:"api_port"`
	PublicKey  string `json:"public_key"`
	AppName    string `json:"app_name"`
}

// defaults answers GET /api/generator/defaults: what the editor of a new
// profile starts with.
func (h *handlers) defaults(w http.ResponseWriter, r *http.Request) {
	host := h.serverInfo.ResolveClientFacingHost(serverinfo.RequestHostOf(r), "")
	if host == "" {
		host = h.serverInfo.DefaultServerHost()
	}
	kit.JSON(w, r, http.StatusOK, defaultsAnswer{Success: true, Data: defaultsData{
		ServerHost: host,
		RelayHost:  host,
		UseHTTPS:   h.serverInfo.DefaultUseHTTPS(),
		APIPort:    h.serverInfo.DefaultAPIPort(),
		PublicKey:  string(h.serverInfo.ResolvePublicKey(r.Context())),
		AppName:    "BetterDesk Support Agent",
	}})
}

// buildErrorKeys turn a build error's code into the text the web app
// shows.
var buildErrorKeys = map[string]string{
	"platforms_required":                  "platforms_required",
	"unsupported_platform":                "unsupported_platform",
	"local_builder_no_supported_targets":  "no_supported_targets",
	"local_builder_toolchain_missing":     "toolchain_missing",
	"betterdesk_client_builder_not_ready": "validation_failed",
	"support_generator_module_not_ready":  "validation_failed",
}

func buildErrorKey(code string) string {
	if key, known := buildErrorKeys[code]; known {
		return "generator.errors." + key
	}
	return "errors.bad_request"
}

// buildFailure is the answer to a build request that cannot be queued.
type buildFailure struct {
	Success bool     `json:"success"`
	Error   string   `json:"error"`
	Code    string   `json:"code"`
	Details []string `json:"details"`
}

// sendBuildError answers a build error: its code up to the first colon,
// the text for it, and for the platform errors the platforms concerned.
func (h *handlers) sendBuildError(w http.ResponseWriter, r *http.Request, err error, status int) {
	code := gen.ErrorCode(err)
	if code == "" {
		code = err.Error()
	}
	if code == "" {
		code = "build_request_invalid"
	}
	code, _, _ = strings.Cut(code, ":")
	details := []string{}
	var coded *gen.CodedError
	if (code == "platforms_required" || code == "unsupported_platform") && errorsAs(err, &coded) && len(coded.Details) > 0 {
		details = coded.Details
	}
	kit.JSON(w, r, status, buildFailure{Error: kit.T(r, buildErrorKey(code), nil), Code: code, Details: details})
}

func (h *handlers) notReadyCode() string {
	if h.gen.Embedded() {
		return "betterdesk_client_builder_not_ready"
	}
	return "module_not_ready"
}

func (h *handlers) sendNotReady(w http.ResponseWriter, r *http.Request) {
	code := h.notReadyCode()
	kit.JSON(w, r, http.StatusBadRequest, buildFailure{Error: kit.T(r, buildErrorKey(code), nil), Code: code, Details: []string{}})
}

// validatePlatforms checks body.platforms when it is a list. It answers
// the error itself and returns false then.
func (h *handlers) validatePlatforms(w http.ResponseWriter, r *http.Request, body any) (gen.Selection, bool) {
	requested := prop(body, "platforms")
	_, isList := requested.([]any)
	selection := h.gen.ValidatePlatformSelection(requested, isList)
	if !selection.Valid {
		h.sendBuildError(w, r, &gen.CodedError{Code: selection.Error, Message: selection.Error, Details: selection.Details}, http.StatusBadRequest)
		return selection, false
	}
	return selection, true
}

// validationFailure is a profile or slug that did not pass.
type validationFailure struct {
	Success bool     `json:"success"`
	Error   string   `json:"error"`
	Errors  []string `json:"errors"`
	Details []string `json:"details"`
}

func (h *handlers) failValidation(w http.ResponseWriter, r *http.Request, errs []string) {
	kit.JSON(w, r, http.StatusBadRequest, validationFailure{Error: kit.T(r, "generator.errors.validation_failed", nil), Errors: errs, Details: errs})
}

// resolveSlug is the address part of a new or changed bundle: the one
// asked for, checked and free, or, when none was asked for, a free one
// made from the name, else from the bundle's id, with -2, -3 and so on
// while it is taken. exclude is the bundle being changed, whose own slug
// counts as free.
func (h *handlers) resolveSlug(ctx context.Context, preferred, name, bundleID, exclude string) (slug, problem string, err error) {
	normalized := ""
	if preferred != "" {
		normalized = store.SlugifyBundleName(preferred)
	}
	if normalized == "" {
		var lookupErr error
		slug := store.AllocateBundleSlug("", name, bundleID, func(candidate string) bool {
			if lookupErr != nil {
				return false
			}
			taken, err := h.gen.IsSlugTaken(ctx, candidate, exclude)
			lookupErr = err
			return taken
		})
		return slug, "", lookupErr
	}
	if problem := gen.ValidateSlug(normalized); problem != "" {
		return "", problem, nil
	}
	taken, err := h.gen.IsSlugTaken(ctx, normalized, exclude)
	if err != nil {
		return "", "", err
	}
	if taken {
		return "", "slug_taken", nil
	}
	return normalized, "", nil
}

// currentClientRevision is the client commit of embedded builds, "" in
// download mode.
func (h *handlers) currentClientRevision() (string, error) {
	if !h.gen.Embedded() {
		return "", nil
	}
	return h.gen.InstalledCommit()
}

func (h *handlers) generationFor(fingerprint, commit string) string {
	if !h.gen.Embedded() || commit == "" {
		return fingerprint
	}
	return gen.GenerationKey(fingerprint, commit)
}

// createBundle answers POST /api/generator/bundles: a new Support bundle
// from a name, a profile, an optional slug and platforms, with its builds
// queued.
func (h *handlers) createBundle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ready, err := h.gen.GeneratorReady()
	if err != nil {
		h.serverError(w, r, "create bundle", err)
		return
	}
	if !ready {
		h.sendNotReady(w, r)
		return
	}
	body := requestBody(r)
	selection, valid := h.validatePlatforms(w, r, body)
	if !valid {
		return
	}
	name := jsSlice(jsTrim(jsString(or(prop(body, "name"), ""))), 100)
	if name == "" {
		h.fail(w, r, http.StatusBadRequest, "generator.errors.name_required")
		return
	}
	validation, err := gen.ValidateBranding(or(prop(body, "branding"), goapi.NewObject()), h.serverInfo.DefaultRelayPort)
	if err != nil {
		h.serverError(w, r, "create bundle", err)
		return
	}
	if !validation.Valid {
		h.failValidation(w, r, validation.Errors)
		return
	}
	bundleID, err := h.gen.NewBundleID()
	if err != nil {
		h.serverError(w, r, "create bundle", err)
		return
	}
	slug, problem, err := h.resolveSlug(ctx, jsTrim(jsString(or(prop(body, "slug"), ""))), name, bundleID, "")
	if err != nil {
		h.serverError(w, r, "create bundle", err)
		return
	}
	if problem != "" {
		h.failValidation(w, r, []string{problem})
		return
	}
	normalized := h.gen.FinalizeSupportBranding(ctx, validation.Normalized)
	normalized.Set("bundle_id", bundleID)
	fingerprint := gen.HashBranding(normalized)
	commit, err := h.currentClientRevision()
	if err != nil {
		h.serverError(w, r, "create bundle", err)
		return
	}
	hash := h.generationFor(fingerprint, commit)
	created, err := h.gen.CreateBundle(ctx, gen.NewBundle{
		BundleID: bundleID, Slug: slug, Name: name, Branding: gen.Stringify(normalized),
		BrandingHash: hash, ConfigFingerprint: fingerprint, ClientCommit: commit,
		CreatedBy: kit.UserID(r), ProductType: gen.ProductSupport,
	})
	if err != nil {
		h.serverError(w, r, "create bundle", err)
		return
	}
	platforms, isList := prop(body, "platforms").([]any)
	if !isList {
		platforms = gen.PlatformList(selection.Selected)
	}
	if err := h.gen.EnqueueBuildsForHash(ctx, hash, false, platforms, true); err != nil {
		h.log.Error("[generator] enqueue builds failed:", err.Error())
		h.sendBuildError(w, r, err, http.StatusServiceUnavailable)
		return
	}
	ok(w, r, goapi.NewObject("bundle", serializeBundle(created)))
}

// updateBundle answers PUT /api/generator/bundles/{bundleId}: a new name,
// profile or slug. A changed profile gets a new hash and its builds are
// queued; the answer does not wait for that.
func (h *handlers) updateBundle(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("bundleId")
	ready, err := h.gen.GeneratorReady()
	if err != nil {
		h.serverError(w, r, "update bundle", err)
		return
	}
	if !ready {
		h.sendNotReady(w, r)
		return
	}
	body := requestBody(r)
	selection, valid := h.validatePlatforms(w, r, body)
	if !valid {
		return
	}
	existing, err := h.gen.GetBundle(ctx, id)
	if err != nil {
		h.serverError(w, r, "update bundle", err)
		return
	}
	if existing == nil {
		h.fail(w, r, http.StatusNotFound, "errors.not_found")
		return
	}
	name := jsSlice(jsTrim(jsString(or(prop(body, "name"), existing.Name))), 100)
	if name == "" {
		h.fail(w, r, http.StatusBadRequest, "generator.errors.name_required")
		return
	}
	validation, err := gen.ValidateBranding(or(prop(body, "branding"), gen.ParseStoredBranding(existing.Branding)), h.serverInfo.DefaultRelayPort)
	if err != nil {
		h.serverError(w, r, "update bundle", err)
		return
	}
	if !validation.Valid {
		h.failValidation(w, r, validation.Errors)
		return
	}
	normalized := h.gen.FinalizeSupportBranding(ctx, validation.Normalized)
	normalized.Set("bundle_id", id)
	fingerprint := gen.HashBranding(normalized)
	commit, err := h.currentClientRevision()
	if err != nil {
		h.serverError(w, r, "update bundle", err)
		return
	}
	existingFingerprint := existing.ConfigFingerprint
	if existingFingerprint == "" {
		existingFingerprint = existing.BrandingHash
	}
	changed := existingFingerprint != fingerprint
	hash := existing.BrandingHash
	if changed {
		hash = h.generationFor(fingerprint, commit)
	}
	slug := existing.Slug.String
	if o, isObject := body.(*goapi.Object); isObject && o.Has("slug") || slug == "" {
		preferred := ""
		if isObject {
			preferred = jsTrim(jsString(or(o.Get("slug"), "")))
		}
		next, problem, err := h.resolveSlug(ctx, preferred, name, existing.BundleID, existing.BundleID)
		if err != nil {
			h.serverError(w, r, "update bundle", err)
			return
		}
		if problem != "" {
			h.failValidation(w, r, []string{problem})
			return
		}
		slug = next
	}
	keepCommit := commit
	if keepCommit == "" {
		keepCommit = existing.ClientCommit.String
	}
	updated, err := h.gen.UpdateBundle(ctx, id, gen.BundleUpdate{
		Name: name, Slug: slug, Branding: gen.Stringify(normalized), BrandingHash: hash,
		ConfigFingerprint: fingerprint, ClientCommit: keepCommit,
	})
	if err != nil {
		h.serverError(w, r, "update bundle", err)
		return
	}
	if existing.BrandingHash != hash || changed {
		platforms, isList := prop(body, "platforms").([]any)
		if !isList {
			platforms = gen.PlatformList(selection.Selected)
		}
		if err := h.gen.EnqueueBuildsForHash(ctx, hash, false, platforms, true); err != nil {
			h.log.Error("[generator] enqueue builds failed:", err.Error())
		}
	}
	ok(w, r, goapi.NewObject("bundle", serializeBundle(updated)))
}

// rebuild answers POST /api/generator/bundles/{bundleId}/rebuild: every
// build of the bundle (or the platforms asked for) queued again.
func (h *handlers) rebuild(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := r.PathValue("bundleId")
	row, err := h.gen.GetBundle(ctx, id)
	if err != nil {
		h.serverError(w, r, "rebuild bundle", err)
		return
	}
	if row == nil {
		h.fail(w, r, http.StatusNotFound, "errors.not_found")
		return
	}
	if row.IsRevoked() {
		h.fail(w, r, http.StatusBadRequest, "generator.errors.rebuild_revoked")
		return
	}
	ready, err := h.gen.GeneratorReady()
	if err != nil {
		h.serverError(w, r, "rebuild bundle", err)
		return
	}
	if !ready {
		h.sendNotReady(w, r)
		return
	}
	body := requestBody(r)
	platforms, isList := prop(body, "platforms").([]any)
	if _, valid := h.validatePlatforms(w, r, body); !valid {
		return
	}
	result, err := h.gen.RebuildBundleByID(ctx, id, platforms, isList)
	if err != nil {
		h.serverError(w, r, "rebuild bundle", err)
		return
	}
	if !result.Success {
		h.fail(w, r, http.StatusNotFound, "errors.not_found")
		return
	}
	hash := result.BrandingHash
	if hash == "" {
		hash = row.BrandingHash
	}
	builds, err := h.gen.ListBuildsForHash(ctx, hash)
	if err != nil {
		h.serverError(w, r, "rebuild bundle", err)
		return
	}
	ok(w, r, goapi.NewObject("queued", float64(result.Platforms), "builds", buildList(h.filterAvailableBuilds(builds))))
}

// rebuildPlatform answers POST
// /api/generator/bundles/{bundleId}/rebuild/{platform}/{arch}/{format}:
// one build queued again.
func (h *handlers) rebuildPlatform(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	row, err := h.gen.GetBundle(ctx, r.PathValue("bundleId"))
	if err != nil {
		h.serverError(w, r, "rebuild platform", err)
		return
	}
	if row == nil {
		h.fail(w, r, http.StatusNotFound, "errors.not_found")
		return
	}
	if row.IsRevoked() {
		h.fail(w, r, http.StatusBadRequest, "generator.errors.rebuild_revoked")
		return
	}
	if row.BrandingHash == "" {
		h.fail(w, r, http.StatusBadRequest, "generator.errors.missing_hash")
		return
	}
	ready, err := h.gen.GeneratorReady()
	if err != nil {
		h.serverError(w, r, "rebuild platform", err)
		return
	}
	if !ready {
		kit.JSON(w, r, http.StatusBadRequest, failure{Error: h.notReadyCode()})
		return
	}
	hash := row.ActiveBuildHash()
	refused, err := h.gen.RequeuePlatformBuild(ctx, hash, r.PathValue("platform"), r.PathValue("arch"), r.PathValue("format"))
	if err != nil {
		h.serverError(w, r, "rebuild platform", err)
		return
	}
	if refused != "" {
		key := "errors.bad_request"
		if refused == "unsupported_platform" {
			key = "generator.errors.unsupported_platform"
		}
		h.fail(w, r, http.StatusBadRequest, key)
		return
	}
	builds, err := h.gen.ListBuildsForHash(ctx, hash)
	if err != nil {
		h.serverError(w, r, "rebuild platform", err)
		return
	}
	ok(w, r, goapi.NewObject("builds", buildList(builds)))
}

// buildStatus answers GET /api/generator/build-status.
func (h *handlers) buildStatus(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, statusAnswer{Success: true, Data: h.gen.BuildWorkerStatus()})
}

type statusAnswer struct {
	Success bool             `json:"success"`
	Data    gen.WorkerStatus `json:"data"`
}

// revoke answers POST /api/generator/bundles/{bundleId}/revoke: the
// bundle revoked, or with {"revoked": false} live again.
func (h *handlers) revoke(w http.ResponseWriter, r *http.Request) {
	revoked := prop(requestBody(r), "revoked") != false
	row, err := h.gen.SetRevoked(r.Context(), r.PathValue("bundleId"), revoked)
	if err != nil {
		h.serverError(w, r, "revoke bundle", err)
		return
	}
	if row == nil {
		h.fail(w, r, http.StatusNotFound, "errors.not_found")
		return
	}
	ok(w, r, goapi.NewObject("bundle", serializeBundle(row)))
}

type done struct {
	Success bool `json:"success"`
}

// deleteBundle answers DELETE /api/generator/bundles/{bundleId}. The
// builds and their files stay.
func (h *handlers) deleteBundle(w http.ResponseWriter, r *http.Request) {
	deleted, err := h.gen.DeleteBundle(r.Context(), r.PathValue("bundleId"))
	if err != nil {
		h.serverError(w, r, "delete bundle", err)
		return
	}
	if !deleted {
		h.fail(w, r, http.StatusNotFound, "errors.not_found")
		return
	}
	kit.JSON(w, r, http.StatusOK, done{Success: true})
}

// preview answers POST /api/generator/preview: a profile as it would be
// stored, and what is wrong with it.
func (h *handlers) preview(w http.ResponseWriter, r *http.Request) {
	finalized := h.gen.FinalizeSupportBranding(r.Context(), or(prop(requestBody(r), "branding"), goapi.NewObject()))
	validation, err := gen.ValidateBranding(finalized, h.serverInfo.DefaultRelayPort)
	if err != nil {
		h.serverError(w, r, "preview", err)
		return
	}
	errs := make([]any, len(validation.Errors))
	for i, e := range validation.Errors {
		errs[i] = e
	}
	kit.JSON(w, r, http.StatusOK, gen.JSON{V: goapi.NewObject(
		"success", true,
		"data", goapi.NewObject("valid", validation.Valid, "errors", errs, "branding", gen.PublicBrandingView(validation.Normalized)),
		"errors", errs,
	)})
}

type platformsAnswer struct {
	Success bool          `json:"success"`
	Data    platformsData `json:"data"`
}

type platformsData struct {
	Platforms          []gen.PlatformStatus `json:"platforms"`
	AvailablePlatforms []gen.Platform       `json:"availablePlatforms"`
	Capabilities       gen.JSON             `json:"capabilities"`
}

// platforms answers GET /api/generator/platforms.
func (h *handlers) platforms(w http.ResponseWriter, r *http.Request) {
	var caps any
	if h.gen.Embedded() {
		caps = h.gen.BuildCapabilities()
	}
	available := h.gen.AvailablePlatforms()
	if available == nil {
		available = []gen.Platform{}
	}
	kit.JSON(w, r, http.StatusOK, platformsAnswer{Success: true, Data: platformsData{
		Platforms:          h.gen.PlatformCatalog(),
		AvailablePlatforms: available,
		Capabilities:       gen.JSON{V: caps},
	}})
}
