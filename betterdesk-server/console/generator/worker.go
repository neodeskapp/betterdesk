package generator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
)

// The build queue (services/clientTemplateWorker.js). A bundle's
// installers are rows of agent_bundle_builds, one per platform, keyed by
// the bundle's branding hash. Routes queue them; the worker takes one
// queued row at a time, builds it and records the artifact below
// ArtifactDir. In download mode a build copies a template and adds a
// signed custom.txt; in embedded mode the client builder compiles every
// available target at once with the signed configuration inside, and a
// new generation becomes the one a bundle hands out only when all its
// targets are ready.
//
// A worker claims a queued row with one update that succeeds only while
// the row is still queued (claimBuild), so two workers on one database
// never build the same row.

type workerState struct {
	mu           sync.Mutex
	running      bool
	active       int
	lastFinished time.Time
	startupReady bool
}

// AvailablePlatforms are the installers this install can build now: in
// download mode the portable ones with an installed template, in
// embedded mode the provider's targets.
func (s *Service) AvailablePlatforms() []Platform {
	var out []Platform
	if !s.opts.Embedded {
		for _, p := range Platforms {
			if p.Format == "portable" && s.ResolveTemplateDir(p.Platform, p.Arch) != "" {
				out = append(out, p)
			}
		}
		return out
	}
	supported := map[string]bool{}
	targets, _ := s.BuildCapabilities().Get("targets").([]any)
	for _, t := range targets {
		supported[jsString(prop(t, "platform"))+"/"+jsString(prop(t, "arch"))+"/"+jsString(prop(t, "format"))] = true
	}
	for _, p := range Platforms {
		if supported[p.Key()] {
			out = append(out, p)
		}
	}
	return out
}

// PlatformStatus is one entry of the catalogue with what the install
// can do about it.
type PlatformStatus struct {
	Platform  string  `json:"platform"`
	Arch      string  `json:"arch"`
	Format    string  `json:"format"`
	Label     string  `json:"label"`
	Available bool    `json:"available"`
	Reason    *string `json:"reason"`
	Provider  string  `json:"provider"`
}

func (s *Service) platformStatusReason(p Platform, caps *goapi.Object) string {
	if !s.opts.Embedded {
		return "template_missing"
	}
	targets, _ := caps.Get("targets").([]any)
	if caps.Get("mode") == "remote" {
		if len(targets) > 0 {
			return "provider_target_unavailable"
		}
		return "provider_targets_missing"
	}
	toolchain := caps.Object("toolchain")
	if !truthy(toolchain.Get("ready")) {
		missing, _ := toolchain.Get("missing").([]any)
		if len(missing) > 0 {
			parts := make([]string, len(missing))
			for i, m := range missing {
				if m != nil {
					parts[i] = jsString(m)
				}
			}
			return "local_builder_toolchain_missing:" + strings.Join(parts, ",")
		}
		return "local_builder_toolchain_missing"
	}
	for _, t := range targets {
		if jsString(prop(t, "platform"))+"/"+jsString(prop(t, "arch"))+"/"+jsString(prop(t, "format")) == p.Key() {
			return ""
		}
	}
	return "provider_host_only"
}

// PlatformCatalog is every installer of the catalogue with whether this
// install can build it and why not.
func (s *Service) PlatformCatalog() []PlatformStatus {
	var caps *goapi.Object
	if s.opts.Embedded {
		caps = s.BuildCapabilities()
	} else {
		caps = goapi.NewObject("mode", "legacy-template", "targets", nil, "toolchain", goapi.NewObject("ready", true, "missing", []any{}))
	}
	available := map[string]bool{}
	for _, p := range s.AvailablePlatforms() {
		available[p.Key()] = true
	}
	out := make([]PlatformStatus, 0, len(Platforms))
	for _, p := range Platforms {
		st := PlatformStatus{Platform: p.Platform, Arch: p.Arch, Format: p.Format, Label: p.Label, Available: available[p.Key()], Provider: jsString(caps.Get("mode"))}
		if !st.Available {
			if reason := s.platformStatusReason(p, caps); reason != "" {
				st.Reason = &reason
			}
		}
		out = append(out, st)
	}
	return out
}

// requestedPlatform is a platform as a request names it.
func requestedPlatform(v any) (platform, arch, format string) {
	return jsString(or(or(prop(v, "platform"), prop(v, "os")), "")),
		jsString(or(prop(v, "arch"), "x64")),
		jsString(or(prop(v, "format"), "portable"))
}

// filterPlatforms is the available platforms a list names; nil is all.
func (s *Service) filterPlatforms(only any, all bool) []Platform {
	available := s.AvailablePlatforms()
	if all {
		return available
	}
	list, _ := only.([]any)
	var out []Platform
	for _, p := range available {
		for _, o := range list {
			if pl, ar, fo := requestedPlatform(o); pl == p.Platform && ar == p.Arch && fo == p.Format {
				out = append(out, p)
				break
			}
		}
	}
	return out
}

// Selection is what ValidatePlatformSelection makes of a request's
// platforms.
type Selection struct {
	Valid    bool
	Error    string
	Details  []string
	Selected []Platform
}

// ValidatePlatformSelection checks the platforms a request names (nil:
// it names none, which means all). It fails when nothing can be built,
// when the list is empty or no list, and when it names a platform that
// cannot be built now.
func (s *Service) ValidatePlatformSelection(selection any, given bool) Selection {
	available := s.AvailablePlatforms()
	if len(available) == 0 {
		return Selection{Error: "local_builder_no_supported_targets", Details: []string{}}
	}
	if !given {
		return Selection{Valid: true, Selected: available}
	}
	list, isList := selection.([]any)
	if !isList || len(list) == 0 {
		return Selection{Error: "platforms_required", Details: []string{"platforms_required"}}
	}
	availableKeys := map[string]bool{}
	for _, p := range available {
		availableKeys[p.Key()] = true
	}
	var requested []string
	seen := map[string]bool{}
	for _, item := range list {
		pl, ar, fo := requestedPlatform(item)
		key := pl + "/" + ar + "/" + fo
		if !seen[key] {
			seen[key] = true
			requested = append(requested, key)
		}
	}
	var unsupported []string
	for _, key := range requested {
		if !availableKeys[key] {
			unsupported = append(unsupported, key)
		}
	}
	if len(unsupported) > 0 {
		return Selection{Error: "unsupported_platform", Details: unsupported}
	}
	var selected []Platform
	for _, p := range available {
		if seen[p.Key()] {
			selected = append(selected, p)
		}
	}
	return Selection{Valid: true, Selected: selected}
}

func buildError(code string, details []string) error {
	return &CodedError{Code: code, Message: code, Details: details}
}

// GeneratorReady reports whether builds can be queued: the client
// builder in embedded mode, the template module in download mode.
func (s *Service) GeneratorReady() (bool, error) {
	if s.opts.Embedded {
		return s.BuilderReadyErr()
	}
	return s.ModuleReady(nil)
}

// EnqueueBuildsForHash queues the builds of a hash for the platforms
// (nil: every available one). A build that is ready or building is left
// alone unless force is set.
func (s *Service) EnqueueBuildsForHash(ctx context.Context, brandingHash string, force bool, platforms any, given bool) error {
	if brandingHash == "" {
		return errors.New("brandingHash required")
	}
	if s.opts.Embedded {
		ready, err := s.BuilderReadyErr()
		if err != nil {
			return err
		}
		if !ready {
			return buildError("betterdesk_client_builder_not_ready", []string{})
		}
	} else {
		ready, err := s.ModuleReady(nil)
		if err != nil {
			return err
		}
		if !ready {
			return buildError("support_generator_module_not_ready", []string{})
		}
		if !s.TemplatesHaveBinaries() {
			return errors.New("support_generator_templates_incomplete: installed templates have no BetterDesk desktop binaries. " +
				"Publish a full Client release with generator-templates, then reinstall the module.")
		}
	}
	selection := s.ValidatePlatformSelection(platforms, given)
	if !selection.Valid {
		return buildError(selection.Error, selection.Details)
	}
	if len(selection.Selected) == 0 {
		return buildError("local_builder_no_supported_targets", []string{})
	}
	for _, p := range selection.Selected {
		existing, err := s.GetBuild(ctx, brandingHash, p.Platform, p.Arch, p.Format)
		if err != nil {
			return err
		}
		if !force && existing != nil && (existing.Status == "ready" || existing.Status == "building") {
			continue
		}
		u := BuildUpdate{BrandingHash: brandingHash, Platform: p.Platform, Arch: p.Arch, Format: p.Format, Status: "queued", ArtifactSize: int64(0)}
		if existing != nil {
			u.ArtifactPath, u.ArtifactSHA256 = existing.ArtifactPath, existing.ArtifactSHA256
			u.ArtifactSize = sizeArg(existing)
		}
		if err := s.UpsertBuild(ctx, u); err != nil {
			return err
		}
	}
	return nil
}

// sizeArg is existing.artifact_size || 0 as the old code bound it.
func sizeArg(b *Build) any {
	v := b.ArtifactSize.Value()
	if !v.Truthy() {
		return int64(0)
	}
	return int64(v.ToNumber())
}

// platformList turns platforms into the list a request would send.
func platformList(platforms []Platform) []any {
	out := make([]any, len(platforms))
	for i, p := range platforms {
		out[i] = goapi.NewObject("platform", p.Platform, "arch", p.Arch, "format", p.Format, "label", p.Label)
	}
	return out
}

// RebuildResult is what RebuildBundleByID did.
type RebuildResult struct {
	Success      bool
	Error        string
	Platforms    int
	BrandingHash string
}

// RebuildBundleByID queues every build of a bundle again, for the
// platforms (all available ones when not given).
func (s *Service) RebuildBundleByID(ctx context.Context, bundleID string, platforms any, given bool) (RebuildResult, error) {
	row, err := s.GetBundle(ctx, bundleID)
	if err != nil {
		return RebuildResult{}, err
	}
	if row == nil {
		return RebuildResult{Error: "not_found"}, nil
	}
	if row.BrandingHash == "" {
		return RebuildResult{Error: "missing_hash"}, nil
	}
	selected := s.filterPlatforms(platforms, !given)
	if err := s.EnqueueBuildsForHash(ctx, row.BrandingHash, true, platformList(selected), true); err != nil {
		return RebuildResult{}, err
	}
	return RebuildResult{Success: true, Platforms: len(selected), BrandingHash: row.BrandingHash}, nil
}

// RebuildAllForClientRevision prepares a new generation of every live
// bundle for a client commit and queues its builds. The generation a
// bundle hands out stays until the new one is complete.
func (s *Service) RebuildAllForClientRevision(ctx context.Context, clientCommit string, force bool) (*goapi.Object, error) {
	commit := jsTrim(clientCommit)
	if !commitPattern.MatchString(commit) {
		return nil, errors.New("betterdesk_client_commit_invalid")
	}
	bundles, err := s.ListBundles(ctx, false)
	if err != nil {
		return nil, err
	}
	queued := []any{}
	for _, b := range bundles {
		if b.IsRevoked() {
			continue
		}
		fingerprint := b.ConfigFingerprint
		if fingerprint == "" {
			fingerprint = b.BrandingHash
		}
		generation := GenerationKey(fingerprint, commit)
		if !force && b.BrandingHash == generation {
			continue
		}
		if _, err := s.PrepareGeneration(ctx, b.BundleID, generation, fingerprint, commit); err != nil {
			return nil, err
		}
		if err := s.EnqueueBuildsForHash(ctx, generation, force, nil, false); err != nil {
			return nil, err
		}
		queued = append(queued, goapi.NewObject("bundleId", b.BundleID, "configFingerprint", fingerprint, "brandingHash", generation))
	}
	return goapi.NewObject("clientCommit", commit, "queued", queued), nil
}

// RequeuePlatformBuild queues one build again and drops its artifact.
// The error text of a refusal is "missing_args" or
// "unsupported_platform".
func (s *Service) RequeuePlatformBuild(ctx context.Context, brandingHash, platform, arch, format string) (string, error) {
	if brandingHash == "" || platform == "" || arch == "" || format == "" {
		return "missing_args", nil
	}
	known, available := false, false
	for _, p := range Platforms {
		if p.Platform == platform && p.Arch == arch && p.Format == format {
			known = true
		}
	}
	if !known {
		return "unsupported_platform", nil
	}
	for _, p := range s.AvailablePlatforms() {
		if p.Platform == platform && p.Arch == arch && p.Format == format {
			available = true
		}
	}
	if !available {
		return "unsupported_platform", nil
	}
	return "", s.UpsertBuild(ctx, BuildUpdate{BrandingHash: brandingHash, Platform: platform, Arch: arch, Format: format, Status: "queued", ArtifactSize: int64(0)})
}

// WorkerStatus answers GET /api/generator/build-status.
type WorkerStatus struct {
	WorkerEnabled      bool             `json:"workerEnabled"`
	Kind               string           `json:"kind"`
	Mode               string           `json:"mode"`
	ClientBuilderReady bool             `json:"clientBuilderReady"`
	ModuleReady        bool             `json:"moduleReady"`
	TemplatesPresent   bool             `json:"templatesPresent"`
	TemplatesDir       string           `json:"templatesDir"`
	ArtifactRoot       string           `json:"artifactRoot"`
	Platforms          []Platform       `json:"platforms"`
	Capabilities       JSON             `json:"capabilities"`
	PlatformCatalog    []PlatformStatus `json:"platformCatalog"`
	BuildReady         bool             `json:"buildReady"`
	ActiveBuilds       int              `json:"activeBuilds"`
	StartupReady       bool             `json:"startupReady"`
}

// BuildWorkerStatus says what the queue can do now and what this
// console's worker is doing.
func (s *Service) BuildWorkerStatus() WorkerStatus {
	moduleReady, err := s.ModuleReady(nil)
	templates := false
	if err != nil {
		moduleReady = false
	} else {
		templates = s.TemplatesExist()
	}
	st := WorkerStatus{
		WorkerEnabled:    s.opts.WorkerEnabled,
		Kind:             "client-template",
		Mode:             "legacy-custom-txt",
		ModuleReady:      moduleReady,
		TemplatesPresent: templates,
		TemplatesDir:     s.TemplatesDir(),
		ArtifactRoot:     s.opts.ArtifactDir,
		Platforms:        nonNilPlatforms(s.AvailablePlatforms()),
		PlatformCatalog:  s.PlatformCatalog(),
	}
	if s.opts.Embedded {
		st.Mode = "embedded"
		st.ClientBuilderReady = s.BuilderReady()
		st.Capabilities = JSON{s.BuildCapabilities()}
		st.BuildReady = truthy(s.BuildReadiness().Get("buildReady"))
	} else {
		st.BuildReady = moduleReady
	}
	s.worker.mu.Lock()
	st.ActiveBuilds, st.StartupReady = s.worker.active, s.worker.startupReady
	s.worker.mu.Unlock()
	return st
}

func nonNilPlatforms(list []Platform) []Platform {
	if list == nil {
		return []Platform{}
	}
	return list
}

// IsArtifactPathSafe reports a path strictly below ArtifactDir.
func (s *Service) IsArtifactPathSafe(filePath string) bool {
	if filePath == "" {
		return false
	}
	root := s.absPath(s.opts.ArtifactDir)
	candidate := s.absPath(filePath)
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// ReadyArtifact is the build of a platform when it is ready and its file
// lies below ArtifactDir and can be read; nil otherwise.
func (s *Service) ReadyArtifact(ctx context.Context, brandingHash, platform, arch, format string) (*Build, error) {
	row, err := s.GetBuild(ctx, brandingHash, platform, arch, format)
	if err != nil || row == nil || row.Status != "ready" || row.ArtifactPath == "" {
		return nil, err
	}
	if !s.IsArtifactPathSafe(row.ArtifactPath) {
		return nil, nil
	}
	f, err := os.Open(row.ArtifactPath)
	if err != nil {
		return nil, nil
	}
	f.Close()
	return row, nil
}

// recoverInterruptedBuilds queues again what a stopped worker left
// building.
func (s *Service) recoverInterruptedBuilds(ctx context.Context) error {
	bundles, err := s.ListBundles(ctx, true)
	if err != nil {
		return err
	}
	recovered := 0
	for _, b := range bundles {
		builds, err := s.ListBuildsForHash(ctx, b.BrandingHash)
		if err != nil {
			return err
		}
		for _, build := range builds {
			if build.Status != "building" {
				continue
			}
			commit := build.ClientCommit
			if commit == "" {
				commit = b.ClientCommit.String
			}
			fingerprint := build.ConfigFingerprint
			if fingerprint == "" {
				fingerprint = b.ConfigFingerprint
			}
			if err := s.UpsertBuild(ctx, BuildUpdate{
				BrandingHash: build.BrandingHash, Platform: build.Platform, Arch: build.Arch, Format: build.Format,
				Status: "queued", ArtifactSize: int64(0), ErrorMessage: "requeued_after_worker_restart",
				ClientCommit: commit, GenerationID: build.GenerationID, ConfigFingerprint: fingerprint,
				ProgressPhase: "requeued_after_restart",
			}); err != nil {
				return err
			}
			recovered++
		}
	}
	if recovered > 0 {
		s.opts.Logger.Info(fmt.Sprintf("[clientTemplateWorker] recovered %d interrupted build(s)", recovered))
	}
	return nil
}

// Run is the worker: it puts back what a stopped worker left building,
// then looks for a queued build every PollInterval and builds one at a
// time, with Cooldown between two builds. It returns when ctx ends.
func (s *Service) Run(ctx context.Context) {
	s.opts.Logger.Info("[clientTemplateWorker] templates=" + s.TemplatesDir())
	s.worker.mu.Lock()
	s.worker.startupReady = false
	s.worker.mu.Unlock()
	s.opts.Logger.Info(fmt.Sprintf("[clientTemplateWorker] started (poll %dms)", s.opts.PollInterval.Milliseconds()))
	if err := s.recoverInterruptedBuilds(ctx); err != nil {
		s.opts.Logger.Error("[clientTemplateWorker] recovery error:", err.Error())
	}
	s.worker.mu.Lock()
	s.worker.startupReady = true
	s.worker.mu.Unlock()
	s.Tick(ctx)
	interval := s.opts.PollInterval
	if interval <= 0 {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	defer func() {
		s.worker.mu.Lock()
		s.worker.startupReady = false
		s.worker.mu.Unlock()
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.Tick(ctx)
		}
	}
}

// Tick claims and builds one queued build, unless one is running, the
// worker has not finished starting, or the cooldown has not passed.
func (s *Service) Tick(ctx context.Context) {
	w := &s.worker
	w.mu.Lock()
	if !w.startupReady || w.running || w.active > 0 || (s.opts.Cooldown > 0 && s.opts.Now().Sub(w.lastFinished) < s.opts.Cooldown) {
		w.mu.Unlock()
		return
	}
	w.running = true
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		w.running = false
		w.mu.Unlock()
	}()
	claimed, err := s.claimNextBuild(ctx)
	if err != nil {
		s.opts.Logger.Error("[clientTemplateWorker] tick error:", err.Error())
		return
	}
	if claimed == nil {
		return
	}
	w.mu.Lock()
	w.active++
	w.mu.Unlock()
	defer func() {
		w.mu.Lock()
		w.active--
		w.lastFinished = s.opts.Now()
		w.mu.Unlock()
	}()
	if err := s.runOne(ctx, claimed); err != nil {
		s.opts.Logger.Error("[clientTemplateWorker] build crashed:", err.Error())
	}
}

// claim is a build the worker took, with its bundle.
type claim struct {
	build  *Build
	bundle *Bundle
}

func (s *Service) findBundleForHash(ctx context.Context, hash string) (*Bundle, error) {
	bundles, err := s.ListBundles(ctx, true)
	if err != nil {
		return nil, err
	}
	for _, b := range bundles {
		if b.BrandingHash == hash {
			return b, nil
		}
	}
	return nil, nil
}

func (s *Service) listPendingBuilds(ctx context.Context, limit int) ([]*Build, error) {
	bundles, err := s.ListBundles(ctx, false)
	if err != nil {
		return nil, err
	}
	var out []*Build
	for _, b := range bundles {
		if b.IsRevoked() {
			continue
		}
		builds, err := s.ListBuildsForHash(ctx, b.BrandingHash)
		if err != nil {
			return nil, err
		}
		for _, row := range builds {
			if isQueuedBuildStatus(row.Status) {
				out = append(out, row)
			}
			if len(out) >= limit {
				return out, nil
			}
		}
	}
	return out, nil
}

func (s *Service) hostPlatformKey() string {
	platform, arch := s.newLocalBuilder(nil).hostTarget()
	return platform + "/" + arch + "/portable"
}

// createdAt is the time a build was queued; the zero time for a row
// without one, which then goes first.
func createdAt(b *Build) time.Time {
	t, _ := b.CreatedAt.Instant()
	return t
}

func (s *Service) claimNextBuild(ctx context.Context) (*claim, error) {
	ready, err := s.GeneratorReady()
	if err != nil {
		return nil, err
	}
	if !ready {
		return nil, nil
	}
	candidates, err := s.listPendingBuilds(ctx, 50)
	if err != nil {
		return nil, err
	}
	host := s.hostPlatformKey()
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		ah, bh := 1, 1
		if a.Key() == host {
			ah = 0
		}
		if b.Key() == host {
			bh = 0
		}
		if ah != bh {
			return ah < bh
		}
		return createdAt(a).Before(createdAt(b))
	})
	for _, row := range candidates {
		bundle, err := s.findBundleForHash(ctx, row.BrandingHash)
		if err != nil {
			return nil, err
		}
		if bundle == nil {
			continue
		}
		if !s.opts.Embedded && s.ResolveTemplateDir(row.Platform, row.Arch) == "" {
			if err := s.UpsertBuild(ctx, BuildUpdate{BrandingHash: row.BrandingHash, Platform: row.Platform, Arch: row.Arch, Format: row.Format,
				Status: "failed", ArtifactSize: int64(0), ErrorMessage: "template_missing:" + row.Platform + "/" + row.Arch}); err != nil {
				return nil, err
			}
			continue
		}
		took, err := s.claimBuild(ctx, row)
		if err != nil {
			return nil, err
		}
		if took {
			return &claim{build: row, bundle: bundle}, nil
		}
	}
	return nil, nil
}

// claimBuild marks a queued build as building, in one statement that
// changes the row only while it still has the status it was read with.
// Of two workers on one database that both read it as queued, only one
// takes it; it reports whether this one did.
func (s *Service) claimBuild(ctx context.Context, row *Build) (bool, error) {
	now := "datetime('now')"
	if s.opts.DB.IsPostgres() {
		now = "NOW()"
	}
	res, err := s.opts.DB.Exec(ctx, `UPDATE agent_bundle_builds
		SET status = 'building', error_message = '', progress_percent = 0, progress_phase = '',
			started_at = `+now+`, updated_at = `+now+`
		WHERE branding_hash = ? AND platform = ? AND arch = ? AND format = ? AND status = ?`,
		row.BrandingHash, row.Platform, row.Arch, row.Format, row.Status)
	if err != nil {
		return false, fmt.Errorf("claim agent bundle build %s: %w", row.Key(), err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("claim agent bundle build %s: %w", row.Key(), err)
	}
	return n == 1, nil
}

// parseRowBranding is the bundle's profile as the worker read it: {} for
// an empty column or text that is no JSON.
func parseRowBranding(raw string) any {
	if raw == "" {
		return goapi.NewObject()
	}
	v, err := parseJSON(raw)
	if err != nil {
		return goapi.NewObject()
	}
	return v
}

// buildAPIServer is the API address a template build's custom.txt names.
func (s *Service) buildAPIServer(branding any) string {
	if v := prop(branding, "api_server"); truthy(v) {
		return jsTrim(jsString(v))
	}
	host := or(prop(branding, "server_host"), s.opts.ServerInfo.DefaultServerHost())
	useHTTPS := truthy(nullish(prop(branding, "use_https"), true))
	port := jsString(or(prop(branding, "api_port"), s.opts.ServerInfo.DefaultAPIPort()))
	scheme := "http"
	if useHTTPS {
		scheme = "https"
	}
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		return scheme + "://" + jsString(host)
	}
	return scheme + "://" + jsString(host) + ":" + port
}

// BuildCustomTxtContent signs the custom.txt of a bundle's profile with
// the install's seed.
func (s *Service) BuildCustomTxtContent(ctx context.Context, branding any) (Signed, error) {
	if branding == nil {
		return Signed{}, typeError("Cannot read properties of null (reading 'server_host')")
	}
	host := or(prop(branding, "server_host"), s.opts.ServerInfo.DefaultServerHost())
	key := or(or(prop(branding, "public_key"), prop(branding, "server_key")), prop(prop(branding, "server"), "public_key"))
	if !truthy(key) {
		key = or(string(s.opts.ServerInfo.ResolvePublicKey(ctx)), "")
	}
	appName := or(or(or(prop(branding, "app_name"), prop(branding, "company_name")), prop(branding, "product_name")), "BetterDesk Support Agent")
	signed, _, err := BuildAndSignSupportCustomTxt(CustomTxtOptions{
		AppName:            appName,
		Host:               host,
		Relay:              or(or(prop(branding, "relay_host"), prop(branding, "relay_server")), host),
		API:                s.buildAPIServer(branding),
		Key:                key,
		DisableSettings:    prop(branding, "disable_settings") != false,
		DisableSettingsSet: true,
		RelayPort:          s.relayPort,
	}, s.SigningSeed())
	return signed, err
}

func findDirEnding(root, suffix string) string {
	stack := []string{root}
	for len(stack) > 0 {
		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			full := filepath.Join(dir, e.Name())
			if strings.HasSuffix(e.Name(), suffix) {
				return full
			}
			stack = append(stack, full)
		}
	}
	return ""
}

func findDirNamed(root, name string) string {
	stack := []string{root}
	for len(stack) > 0 {
		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			full := filepath.Join(dir, e.Name())
			if e.Name() == name {
				return full
			}
			stack = append(stack, full)
		}
	}
	return ""
}

// customTxtTarget is the folder custom.txt goes into: on macOS the one
// with the marker, else Contents/MacOS of the app; elsewhere the stage.
func customTxtTarget(stageDir, platform string) string {
	if strings.ToLower(platform) == "macos" {
		if marker := findFile(stageDir, ".custom-txt-here"); marker != "" {
			return filepath.Dir(marker)
		}
		if app := findDirEnding(stageDir, ".app"); app != "" {
			if macos := filepath.Join(app, "Contents", "MacOS"); exists(macos) {
				return macos
			}
		}
		if dir := findDirNamed(stageDir, "MacOS"); dir != "" {
			return dir
		}
	}
	return stageDir
}

// templateHasBinary reports a client binary over 100 KB in the stage, or
// an .app folder.
func templateHasBinary(stageDir, platform string) bool {
	if strings.ToLower(platform) == "macos" && findDirEnding(stageDir, ".app") != "" {
		return true
	}
	names := map[string]bool{"betterdesk.exe": true, "rustdesk.exe": true, "betterdesk": true, "rustdesk": true}
	stack := []string{stageDir}
	for len(stack) > 0 {
		dir := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			full := filepath.Join(dir, e.Name())
			if e.IsDir() {
				if strings.HasSuffix(e.Name(), ".app") {
					return true
				}
				stack = append(stack, full)
			} else if names[e.Name()] || names[strings.ToLower(e.Name())] {
				if info, err := os.Stat(full); err == nil && info.Size() > 100*1024 {
					return true
				}
			}
		}
	}
	return false
}

func (s *Service) workRoot() string {
	return filepath.Join(s.opts.DataDir, "build-cache", "support-templates")
}

// packArtifact packs the stage: a zip for Windows (or a .zip name), a
// tar.gz with the stage as its one folder otherwise.
func (s *Service) packArtifact(ctx context.Context, stageDir, outPath, platform string) error {
	if err := os.MkdirAll(filepath.Dir(outPath), 0o777); err != nil {
		return nodeFSError(err, "mkdir", filepath.Dir(outPath))
	}
	if exists(outPath) {
		if err := os.Remove(outPath); err != nil {
			return nodeFSError(err, "unlink", outPath)
		}
	}
	if strings.ToLower(platform) == "windows" || strings.HasSuffix(outPath, ".zip") {
		return writeFolderZip(stageDir, filepath.Base(stageDir), outPath)
	}
	res, err := s.opts.Run.Run(ctx, Command{Name: "tar", Args: []string{"-czf", outPath, filepath.Base(stageDir)}, Dir: filepath.Dir(stageDir)})
	if err != nil {
		return err
	}
	if res.Code != 0 {
		text := jsTrim(res.Stderr)
		if text == "" {
			text = "unknown"
		}
		return fmt.Errorf("tar failed (%s): %s", codeText(res.Code), text)
	}
	return nil
}

func (s *Service) runOne(ctx context.Context, c *claim) error {
	if s.opts.Embedded {
		return s.runEmbeddedOne(ctx, c)
	}
	row := c.build
	key := row.Key()
	start := s.opts.Now()
	s.opts.Logger.Info("[clientTemplateWorker] build start hash=" + jsSlice(row.BrandingHash, 12) + " " + key)
	workDir := filepath.Join(s.workRoot(), jsSlice(row.BrandingHash, 16)+"_"+row.Platform+"_"+row.Arch)
	defer os.RemoveAll(workDir)
	err := func() error {
		_ = os.RemoveAll(workDir)
		for _, dir := range []string{workDir, s.opts.ArtifactDir} {
			if err := os.MkdirAll(dir, 0o777); err != nil {
				return nodeFSError(err, "mkdir", dir)
			}
		}
		templateDir := s.ResolveTemplateDir(row.Platform, row.Arch)
		if templateDir == "" {
			return errors.New("template_missing:" + key)
		}
		stageDir := filepath.Join(workDir, "betterdesk-support-"+row.Platform+"-"+row.Arch)
		if err := copyTree(templateDir, stageDir); err != nil {
			return err
		}
		if !templateHasBinary(stageDir, row.Platform) {
			return errors.New("template_missing_binary:" + key + " — installed templates are a stub without betterdesk.exe / binary. " +
				"Publish a full BetterDesk-Client release (workflow betterdesk-desktop-release.yml) " +
				"with generator-templates containing desktop binaries, then reinstall the module.")
		}
		branding := parseRowBranding(c.bundle.Branding)
		signed, err := s.BuildCustomTxtContent(ctx, branding)
		if err != nil {
			return err
		}
		injectDir := customTxtTarget(stageDir, row.Platform)
		if err := os.MkdirAll(injectDir, 0o777); err != nil {
			return nodeFSError(err, "mkdir", injectDir)
		}
		if err := os.WriteFile(filepath.Join(injectDir, "custom.txt"), []byte(signed.Content), 0o666); err != nil {
			return nodeFSError(err, "open", filepath.Join(injectDir, "custom.txt"))
		}
		_ = os.Remove(filepath.Join(stageDir, ".custom-txt-here"))
		if nested := findFile(stageDir, ".custom-txt-here"); nested != "" {
			_ = os.Remove(nested)
		}
		installer := InstallerOptions{InstallService: truthy(prop(branding, "install_service")), Autostart: truthy(prop(branding, "autostart"))}
		if row.Platform == "windows" {
			err = writeWindowsInstallers(stageDir, installer)
		} else {
			err = writeUnixInstallers(stageDir, row.Platform, installer)
		}
		if err != nil {
			return err
		}
		ext := "tar.gz"
		if row.Platform == "windows" {
			ext = "zip"
		}
		artifactPath := filepath.Join(s.opts.ArtifactDir, "betterdesk-support-"+jsSlice(row.BrandingHash, 12)+"-"+row.Platform+"-"+row.Arch+"."+ext)
		if err := s.packArtifact(ctx, stageDir, artifactPath, row.Platform); err != nil {
			return err
		}
		info, err := os.Stat(artifactPath)
		if err != nil {
			return nodeFSError(err, "stat", artifactPath)
		}
		sum, err := sha256File(artifactPath)
		if err != nil {
			return err
		}
		if err := s.UpsertBuild(ctx, BuildUpdate{BrandingHash: row.BrandingHash, Platform: row.Platform, Arch: row.Arch, Format: row.Format,
			Status: "ready", ArtifactPath: artifactPath, ArtifactSize: info.Size(), ArtifactSHA256: sum}); err != nil {
			return err
		}
		s.opts.Logger.Info(fmt.Sprintf("[clientTemplateWorker] build ready %s signed=true (%.2f MB, %.1fs)", key,
			float64(info.Size())/1024/1024, s.opts.Now().Sub(start).Seconds()))
		return nil
	}()
	if err != nil {
		msg := errorMessage(err)
		s.opts.Logger.Error("[clientTemplateWorker] build FAILED " + key + ": " + msg)
		return s.UpsertBuild(ctx, BuildUpdate{BrandingHash: row.BrandingHash, Platform: row.Platform, Arch: row.Arch, Format: row.Format,
			Status: "failed", ArtifactSize: int64(0), ErrorMessage: jsSlice(msg, 2000)})
	}
	return nil
}

// artifactExtension is the file extension of an installer.
func artifactExtension(platform, format string) string {
	switch {
	case strings.ToLower(platform) == "windows":
		return ".zip"
	case format == "deb":
		return ".deb"
	case format == "rpm":
		return ".rpm"
	case format == "arch":
		return ".pkg.tar.zst"
	}
	return ".tar.gz"
}

func manifestArch(arch string) string {
	switch strings.ToLower(arch) {
	case "x64", "amd64":
		return "x86_64"
	case "arm64":
		return "aarch64"
	}
	return strings.ToLower(arch)
}

// findManifestArtifact is the one asset of the provider's manifest for a
// platform, arch and format: inside root, with the right extension, a
// file.
func (s *Service) findManifestArtifact(root string, manifest any, platform, arch, format string) (string, error) {
	target := strings.ToLower(platform) + "/" + manifestArch(arch) + "/" + format
	assets, _ := prop(manifest, "assets").([]any)
	var matches []any
	for _, a := range assets {
		key := strings.ToLower(jsString(or(prop(a, "platform"), ""))) + "/" + strings.ToLower(jsString(or(prop(a, "arch"), ""))) + "/" + strings.ToLower(jsString(or(prop(a, "format"), "portable")))
		if key == target {
			matches = append(matches, a)
		}
	}
	if len(matches) != 1 {
		return "", errors.New("embedded_manifest_asset_target_count_invalid:" + platform + "/" + arch)
	}
	name := tmpl(matches[0], "name")
	candidate := s.absPath(root, jsString(prop(matches[0], "name")))
	rel, err := filepath.Rel(s.absPath(root), candidate)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) ||
		!strings.HasSuffix(strings.ToLower(candidate), artifactExtension(platform, format)) || !isFile(candidate) {
		return "", errors.New("embedded_manifest_asset_invalid:" + name)
	}
	return candidate, nil
}

// failedPlatformsForError are the rows a failed embedded build marks: the
// claimed one for a local provider or a local builder error, every
// catalogue entry otherwise.
func (s *Service) failedPlatformsForError(message string, row *Build) []Platform {
	local := s.opts.Embedded && s.BuildCapabilities().Get("mode") == "local"
	if local || strings.HasPrefix(message, "local_builder") {
		return []Platform{{Platform: row.Platform, Arch: row.Arch, Format: row.Format}}
	}
	return Platforms
}

// assertEmbeddedArtifact refuses an artifact that carries a custom.txt or
// its marker: an embedded build has its configuration compiled in.
func (s *Service) assertEmbeddedArtifact(ctx context.Context, artifactPath, format string) error {
	if strings.HasSuffix(artifactPath, ".zip") {
		names, err := zipEntryNames(artifactPath)
		if err != nil {
			return err
		}
		for _, n := range names {
			name := strings.ToLower(strings.ReplaceAll(n, `\`, "/"))
			if strings.HasSuffix(name, "/custom.txt") || strings.HasSuffix(name, "/.custom-txt-here") {
				return errors.New("embedded_artifact_contains_external_config")
			}
		}
		return nil
	}
	command, args := "tar", []string{"-tzf", artifactPath}
	switch format {
	case "deb":
		command, args = "dpkg-deb", []string{"-c", artifactPath}
	case "rpm":
		command, args = "rpm", []string{"-qlp", artifactPath}
	case "arch":
		args = []string{"--zstd", "-tf", artifactPath}
	}
	res, err := s.opts.Run.Run(ctx, Command{Name: command, Args: args, Dir: filepath.Dir(artifactPath)})
	if err != nil {
		return err
	}
	if res.Code != 0 {
		detail := jsTrim(res.Stderr)
		if detail == "" {
			detail = codeText(res.Code)
		}
		return errors.New("embedded_artifact_list_failed:" + detail)
	}
	for _, line := range splitLines(res.Stdout) {
		name := strings.ToLower(strings.ReplaceAll(line, `\`, "/"))
		if strings.HasSuffix(name, "/custom.txt") || strings.HasSuffix(name, "/.custom-txt-here") {
			return errors.New("embedded_artifact_contains_external_config")
		}
	}
	return nil
}

var assetNamePattern = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

// validateEmbeddedManifest checks the provider's manifest.json: schema 1,
// sku betterdesk-support, no external configuration, the bundle's client
// commit, and for every asset a safe unique name, a catalogue target, a
// SHA-256 and size that match the file. Every required target must be
// there.
func (s *Service) validateEmbeddedManifest(outputDir string, bundle *Bundle, required []Platform) (any, error) {
	path := filepath.Join(outputDir, "manifest.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("embedded_manifest_invalid:" + nodeFSError(err, "open", path).Error())
	}
	manifest, err := parseJSON(decodeUTF8(raw))
	if err != nil {
		return nil, errors.New("embedded_manifest_invalid:" + syntaxError(decodeUTF8(raw)).Error())
	}
	if manifest == nil {
		return nil, typeError("Cannot read properties of null (reading 'schema_version')")
	}
	if prop(manifest, "schema_version") != float64(1) || prop(manifest, "sku") != ProductSupport {
		return nil, errors.New("embedded_manifest_contract_invalid")
	}
	if prop(manifest, "external_config") != false {
		return nil, errors.New("embedded_manifest_external_config")
	}
	if bundle != nil && bundle.ClientCommit.String != "" &&
		strings.ToLower(jsString(or(prop(manifest, "client_commit"), ""))) != strings.ToLower(bundle.ClientCommit.String) {
		return nil, errors.New("embedded_manifest_client_commit_mismatch")
	}
	assets, isList := prop(manifest, "assets").([]any)
	if !isList || len(assets) == 0 {
		return nil, errors.New("embedded_manifest_assets_missing")
	}
	validTargets := map[string]bool{}
	for _, p := range Platforms {
		validTargets[p.Platform+"/"+providerArch(p.Arch)+"/"+p.Format] = true
	}
	names, targets := map[string]bool{}, map[string]bool{}
	for _, a := range assets {
		name, nameOK := prop(a, "name").(string)
		pl, plOK := prop(a, "platform").(string)
		ar, arOK := prop(a, "arch").(string)
		fo, foOK := prop(a, "format").(string)
		if a == nil || !nameOK || !assetNamePattern.MatchString(name) || names[name] || !plOK || !arOK || !foOK {
			return nil, errors.New("embedded_manifest_asset_invalid")
		}
		names[name] = true
		key := strings.ToLower(pl) + "/" + strings.ToLower(ar) + "/" + strings.ToLower(fo)
		if !validTargets[key] || targets[key] {
			return nil, errors.New("embedded_manifest_asset_target_invalid:" + key)
		}
		targets[key] = true
		if !sha256Text.MatchString(jsString(or(prop(a, "sha256"), ""))) {
			return nil, errors.New("embedded_manifest_asset_checksum_missing:" + name)
		}
		size, isNumber := prop(a, "size").(float64)
		if !isNumber || size != float64(int64(size)) || size > 1<<53-1 || size < 1 {
			return nil, errors.New("embedded_manifest_asset_size_invalid:" + name)
		}
		assetPath := filepath.Join(outputDir, name)
		info, err := os.Stat(assetPath)
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("embedded_manifest_asset_missing:" + name)
		}
		if float64(info.Size()) != size {
			return nil, errors.New("embedded_manifest_asset_size_mismatch:" + name)
		}
		actual, err := sha256File(assetPath)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(actual, jsString(prop(a, "sha256"))) {
			return nil, errors.New("embedded_manifest_asset_checksum_mismatch:" + name)
		}
	}
	for _, p := range required {
		key := p.Platform + "/" + providerArch(p.Arch) + "/" + p.Format
		if !targets[key] {
			return nil, errors.New("embedded_manifest_required_target_missing:" + key)
		}
	}
	return manifest, nil
}

func (s *Service) runEmbeddedOne(ctx context.Context, c *claim) error {
	row := c.build
	key := row.Key()
	start := s.opts.Now()
	generationID := jsSlice(row.BrandingHash, 48)
	commit := c.bundle.ClientCommit.String
	fingerprint := c.bundle.ConfigFingerprint
	update := func(p Platform, status, path string, size any, sum, message string, percent float64, phase string) error {
		return s.UpsertBuild(ctx, BuildUpdate{BrandingHash: row.BrandingHash, Platform: p.Platform, Arch: p.Arch, Format: p.Format,
			Status: status, ArtifactPath: path, ArtifactSize: size, ArtifactSHA256: sum, ErrorMessage: message,
			ClientCommit: commit, GenerationID: generationID, ConfigFingerprint: fingerprint, ProgressPercent: percent, ProgressPhase: phase})
	}
	claimed := Platform{Platform: row.Platform, Arch: row.Arch, Format: row.Format}
	err := func() error {
		if err := update(claimed, "building", "", int64(0), "", "", 0, "preparing"); err != nil {
			return err
		}
		signed, err := s.BuildCustomTxtContent(ctx, parseRowBranding(c.bundle.Branding))
		if err != nil {
			return err
		}
		platforms := s.AvailablePlatforms()
		if len(platforms) == 0 {
			return errors.New("local_builder_no_supported_targets")
		}
		targets := make([]any, len(platforms))
		for i, p := range platforms {
			targets[i] = goapi.NewObject("platform", p.Platform, "arch", p.Arch, "format", p.Format)
		}
		request := goapi.NewObject(
			"schema_version", float64(1),
			"product_sku", ProductSupport,
			"generationId", generationID,
			"clientCommit", nullable(commit),
			"configFingerprint", nullable(fingerprint),
			"targets", targets,
			"signed_config_b64", signed.Content,
		)
		result, err := s.RunBuild(ctx, request, func(percent float64, phase string) {
			_ = update(claimed, "building", "", int64(0), "", "", percent, phase)
		})
		if err != nil {
			return err
		}
		manifest, err := s.validateEmbeddedManifest(result.OutputDir, c.bundle, platforms)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(s.opts.ArtifactDir, 0o777); err != nil {
			return nodeFSError(err, "mkdir", s.opts.ArtifactDir)
		}
		have := map[string]bool{}
		assets, _ := prop(manifest, "assets").([]any)
		for _, a := range assets {
			have[jsString(prop(a, "platform"))+"/"+jsString(prop(a, "arch"))+"/"+jsString(or(prop(a, "format"), "portable"))] = true
		}
		for _, p := range platforms {
			targetKey := p.Platform + "/" + providerArch(p.Arch) + "/" + p.Format
			if !have[targetKey] {
				return errors.New("embedded_build_target_missing:" + targetKey)
			}
			source, err := s.findManifestArtifact(result.OutputDir, manifest, p.Platform, p.Arch, p.Format)
			if err != nil {
				return err
			}
			if err := s.assertEmbeddedArtifact(ctx, source, p.Format); err != nil {
				return err
			}
			ext := strings.TrimPrefix(artifactExtension(p.Platform, p.Format), ".")
			artifactPath := filepath.Join(s.opts.ArtifactDir, "betterdesk-support-"+jsSlice(row.BrandingHash, 12)+"-"+p.Platform+"-"+p.Arch+"."+ext)
			temporary := fmt.Sprintf("%s.tmp-%d-%d", artifactPath, s.opts.PID, s.opts.Now().UnixMilli())
			if err := copyFile(source, temporary); err != nil {
				return err
			}
			if err := os.Rename(temporary, artifactPath); err != nil {
				return nodeFSError(err, "rename", temporary)
			}
			info, err := os.Stat(artifactPath)
			if err != nil {
				return nodeFSError(err, "stat", artifactPath)
			}
			sum, err := sha256File(artifactPath)
			if err != nil {
				return err
			}
			if err := update(p, "ready", artifactPath, info.Size(), sum, "", 100, "complete"); err != nil {
				return err
			}
		}
		if err := s.promoteIfComplete(ctx, row.BrandingHash); err != nil {
			return err
		}
		s.opts.Logger.Info(fmt.Sprintf("[clientTemplateWorker] embedded build ready %s (%.1fs)", key, s.opts.Now().Sub(start).Seconds()))
		return nil
	}()
	if err != nil {
		msg := errorMessage(err)
		s.opts.Logger.Error("[clientTemplateWorker] embedded build FAILED " + key + ": " + msg)
		var first error
		for _, p := range s.failedPlatformsForError(msg, row) {
			if uerr := update(p, "failed", "", int64(0), "", jsSlice(msg, 2000), 0, "failed"); uerr != nil && first == nil {
				first = uerr
			}
		}
		return first
	}
	return nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// promoteIfComplete makes a generation live once every available target
// is ready.
func (s *Service) promoteIfComplete(ctx context.Context, brandingHash string) error {
	builds, err := s.ListBuildsForHash(ctx, brandingHash)
	if err != nil {
		return err
	}
	ready := map[string]bool{}
	for _, b := range builds {
		if b.Status == "ready" {
			ready[b.Key()] = true
		}
	}
	for _, p := range s.AvailablePlatforms() {
		if !ready[p.Key()] {
			return nil
		}
	}
	return s.PromoteGeneration(ctx, brandingHash)
}
