package generator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/store"
)

// The template module: portable desktop builds of the BetterDesk client
// without a configuration, downloaded from the client's GitHub releases
// (or uploaded as the same archive) into
// DataDir/modules/betterdesk-support-generator. A build copies a template,
// adds a signed custom.txt and packs it
// (services/supportGeneratorModule.js).

const (
	moduleID          = "betterdesk-support-generator"
	defaultClientRepo = "UNITRONIX/BetterDesk-Client"
	signingSeedName   = "custom-client-signing.seed"
	minBinarySize     = 32 * 1024
)

// requiredTemplates are the template folders a complete module has.
var requiredTemplates = []string{
	"windows-x86_64", "windows-aarch64", "linux-x86_64", "linux-aarch64", "macos-x86_64", "macos-aarch64",
}

var stateStatuses = map[string]bool{"not_installed": true, "downloading": true, "ready": true, "error": true}

// ModuleDir is the module's folder.
func (s *Service) ModuleDir() string {
	return filepath.Join(s.opts.DataDir, "modules", moduleID)
}

func (s *Service) moduleStatePath() string { return filepath.Join(s.ModuleDir(), "state.json") }

// TemplatesDir is the folder of the installed templates.
func (s *Service) TemplatesDir() string { return filepath.Join(s.ModuleDir(), "templates") }

// SigningSeedPath is the file of the seed custom.txt files are signed
// with.
func (s *Service) SigningSeedPath() string { return filepath.Join(s.ModuleDir(), signingSeedName) }

// isSafeRelativePath accepts a relative path without a ".." part and
// without a drive letter, in either slash.
func (s *Service) isSafeRelativePath(v any) bool {
	value, ok := v.(string)
	if !ok || value == "" || s.isAbsolute(value) {
		return false
	}
	normalized := strings.ReplaceAll(value, `\`, "/")
	for _, part := range strings.Split(normalized, "/") {
		if part == ".." {
			return false
		}
	}
	return !driveLetter.MatchString(normalized)
}

var driveLetter = regexp.MustCompile(`^[a-zA-Z]:`)

// isAbsolute is path.isAbsolute on the console's system.
func (s *Service) isAbsolute(p string) bool {
	if s.opts.GOOS != "windows" {
		return strings.HasPrefix(p, "/")
	}
	if p == "" {
		return false
	}
	if p[0] == '/' || p[0] == '\\' {
		return true
	}
	return len(p) > 2 && driveLetter.MatchString(p) && (p[2] == '/' || p[2] == '\\')
}

// findFile walks root depth first, the last folder read first, and
// returns the first file with the name.
func findFile(root, name string) string {
	stack := []string{root}
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
				stack = append(stack, full)
			} else if e.Name() == name {
				return full
			}
		}
	}
	return ""
}

// readManifestAt reads root/manifest.json. A missing file or text that is
// not JSON gives nil, and so does JSON null.
func readManifestAt(root string) any {
	raw, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		return nil
	}
	v, err := parseJSON(decodeUTF8(raw))
	if err != nil {
		return nil
	}
	return v
}

var errSymlink = codeError("generator_archive_symlink")

// assertNoSymlinks fails on the first symbolic link below root.
func assertNoSymlinks(root string) error {
	stack := []string{root}
	for len(stack) > 0 {
		current := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		entries, err := os.ReadDir(current)
		if err != nil {
			return nodeFSError(err, "scandir", current)
		}
		for _, e := range entries {
			full := filepath.Join(current, e.Name())
			info, err := os.Lstat(full)
			if err != nil {
				return nodeFSError(err, "lstat", full)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return errSymlink
			}
			if info.IsDir() {
				stack = append(stack, full)
			}
		}
	}
	return nil
}

// tmpl is `${value}` of a property: "undefined" when it is missing.
func tmpl(v any, name string) string {
	o, _ := v.(*goapi.Object)
	if o == nil || !o.Has(name) {
		return "undefined"
	}
	return jsString(o.Get(name))
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// absPath is path.resolve(parts...).
func (s *Service) absPath(parts ...string) string {
	joined := filepath.Join(parts...)
	if !filepath.IsAbs(joined) {
		joined = filepath.Join(s.opts.WorkDir, joined)
	}
	return filepath.Clean(joined)
}

// ValidateTemplates checks an installed or unpacked template tree:
// the manifest (schema 2, sku generator-templates, the six required
// platforms), no symbolic links, and for each portable template its
// folder with the marker for custom.txt and without a custom.txt, its
// binary of at least 32 KB, its checksum and its archive. It returns the
// manifest and the number of portable templates.
func (s *Service) ValidateTemplates(root string) (any, int, error) {
	manifest := readManifestAt(root)
	if !truthy(manifest) || prop(manifest, "schema_version") != float64(2) || prop(manifest, "sku") != "generator-templates" {
		return nil, 0, codeError("generator_manifest_invalid")
	}
	if err := assertNoSymlinks(root); err != nil {
		return nil, 0, err
	}
	declared, _ := prop(manifest, "required_platforms").([]any)
	if jsSortedJoin(declared) != strings.Join(sortedStrings(requiredTemplates), "|") {
		return nil, 0, codeError("generator_manifest_platforms_invalid")
	}
	rootAbs := s.absPath(root)
	sep := string(filepath.Separator)
	entries, _ := prop(manifest, "templates").([]any)
	portable := map[string]bool{}
	for _, entry := range entries {
		if prop(entry, "format") != "portable" {
			continue
		}
		key := tmpl(entry, "platform") + "-" + tmpl(entry, "arch")
		if portable[key] {
			return nil, 0, codeError("generator_template_duplicate:" + key)
		}
		portable[key] = true
		templatePath := prop(entry, "template_path")
		if !s.isSafeRelativePath(templatePath) {
			return nil, 0, codeError("generator_template_path_invalid:" + key)
		}
		templateRoot := s.absPath(root, templatePath.(string))
		if !strings.HasPrefix(templateRoot, rootAbs+sep) {
			return nil, 0, codeError("generator_template_path_escape:" + key)
		}
		if !isDir(templateRoot) {
			return nil, 0, codeError("generator_template_missing:" + key)
		}
		if findFile(templateRoot, ".custom-txt-here") == "" {
			return nil, 0, codeError("generator_template_marker_missing:" + key)
		}
		if findFile(templateRoot, "custom.txt") != "" {
			return nil, 0, codeError("generator_template_contains_custom_txt:" + key)
		}
		binaryPath := prop(entry, "binary_path")
		if !s.isSafeRelativePath(binaryPath) {
			return nil, 0, codeError("generator_template_binary_path_invalid:" + key)
		}
		binary := s.absPath(templateRoot, binaryPath.(string))
		info, err := os.Stat(binary)
		if !strings.HasPrefix(binary, templateRoot+sep) || err != nil || !info.Mode().IsRegular() || info.Size() < minBinarySize {
			return nil, 0, codeError("generator_template_binary_missing:" + key)
		}
		if !sha256Text.MatchString(jsString(or(prop(entry, "sha256"), ""))) {
			return nil, 0, codeError("generator_template_hash_invalid:" + key)
		}
		archive := prop(entry, "archive")
		if !s.isSafeRelativePath(archive) {
			return nil, 0, codeError("generator_template_archive_path_invalid:" + key)
		}
		archivePath := s.absPath(root, archive.(string))
		if !strings.HasPrefix(archivePath, rootAbs+sep) || !isFile(archivePath) {
			return nil, 0, codeError("generator_template_archive_missing:" + key)
		}
	}
	if len(portable) != len(requiredTemplates) {
		return nil, 0, codeError("generator_manifest_templates_incomplete")
	}
	return manifest, len(portable), nil
}

var sha256Text = regexp.MustCompile(`(?i)^[a-f0-9]{64}$`)

// jsSortedJoin is list.slice().sort().join('|').
func jsSortedJoin(list []any) string {
	items := append([]any(nil), list...)
	sort.SliceStable(items, func(i, j int) bool { return lessUnits(jsString(items[i]), jsString(items[j])) })
	parts := make([]string, len(items))
	for i, it := range items {
		if it != nil {
			parts[i] = jsString(it)
		}
	}
	return strings.Join(parts, "|")
}

func sortedStrings(list []string) []string {
	out := append([]string(nil), list...)
	sortKeys(out)
	return out
}

// ClientRepo is the GitHub repository the templates come from
// (BETTERDESK_CLIENT_REPO, default UNITRONIX/BetterDesk-Client).
func (s *Service) ClientRepo() string {
	repo := jsTrim(s.getenv("BETTERDESK_CLIENT_REPO"))
	if s.getenv("BETTERDESK_CLIENT_REPO") == "" || repo == "" {
		return defaultClientRepo
	}
	return repo
}

func defaultModuleState() *goapi.Object {
	return goapi.NewObject(
		"termsAccepted", false,
		"installedVersion", nil,
		"status", "not_installed",
		"error", nil,
		"installedAt", nil,
		"manifestVersion", nil,
		"templateCount", float64(0),
	)
}

func (s *Service) ensureModuleDir() error {
	if err := os.MkdirAll(s.ModuleDir(), 0o777); err != nil {
		return nodeFSError(err, "mkdir", s.ModuleDir())
	}
	return nil
}

// ModuleState reads the module's state file over the defaults. A file
// that cannot be read gives the defaults.
func (s *Service) ModuleState() (*goapi.Object, error) {
	if err := s.ensureModuleDir(); err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(s.moduleStatePath())
	if err != nil {
		return defaultModuleState(), nil
	}
	parsed, err := parseJSON(decodeUTF8(raw))
	if err != nil || parsed == nil {
		return defaultModuleState(), nil
	}
	state := spread(defaultModuleState(), parsed)
	status := prop(parsed, "status")
	if text, ok := status.(string); ok && stateStatuses[text] {
		state.Set("status", text)
	} else {
		state.Set("status", "not_installed")
	}
	state.Set("termsAccepted", truthy(prop(parsed, "termsAccepted")))
	return state, nil
}

// writeModuleState lays patch over the state, keeps the old status when
// the new one is not one of the four, and writes the file.
func (s *Service) writeModuleState(patch *goapi.Object) (*goapi.Object, error) {
	if err := s.ensureModuleDir(); err != nil {
		return nil, err
	}
	current, err := s.ModuleState()
	if err != nil {
		return nil, err
	}
	next := spread(current.Clone(), patch)
	if status, ok := next.Get("status").(string); !ok || !stateStatuses[status] {
		next.Set("status", current.Get("status"))
	}
	if err := writeFileMode(s.moduleStatePath(), []byte(dbText(stringifyIndent(next)+"\n")), 0o600); err != nil {
		return nil, err
	}
	return next, nil
}

// writeFileMode is fs.writeFile with a mode: the mode counts for a new
// file only.
func writeFileMode(path string, data []byte, mode os.FileMode) error {
	if err := os.WriteFile(path, data, mode); err != nil {
		return nodeFSError(err, "open", path)
	}
	return nil
}

// TemplatesExist reports a complete, valid template tree.
func (s *Service) TemplatesExist() bool {
	root := s.TemplatesDir()
	if !exists(root) {
		return false
	}
	_, _, err := s.ValidateTemplates(root)
	return err == nil
}

// TemplatesHaveBinaries is TemplatesExist: a valid tree has its binaries.
func (s *Service) TemplatesHaveBinaries() bool {
	return s.TemplatesExist() && exists(s.TemplatesDir())
}

// ModuleReady reports whether template builds can run: the terms are
// accepted, the state is ready, the templates are valid and the signing
// seed is there. state nil reads the state file, and an error comes from
// a file that is no JSON (or JSON null), which the old code did not catch.
func (s *Service) ModuleReady(state *goapi.Object) (bool, error) {
	var s0 any = state
	if state == nil {
		raw, err := os.ReadFile(s.moduleStatePath())
		switch {
		case err == nil:
			parsed, perr := parseJSON(decodeUTF8(raw))
			if perr != nil {
				return false, perr
			}
			if parsed == nil {
				return false, typeError("Cannot read properties of null (reading 'termsAccepted')")
			}
			s0 = parsed
		case os.IsNotExist(err):
			s0 = defaultModuleState()
		default:
			return false, nodeFSError(err, "open", s.moduleStatePath())
		}
	}
	return truthy(prop(s0, "termsAccepted")) && prop(s0, "status") == "ready" && s.TemplatesExist() && s.SigningSeedValid(), nil
}

// SigningSeedValid reports a seed file with base64 of 32 bytes.
func (s *Service) SigningSeedValid() bool {
	raw, err := os.ReadFile(s.SigningSeedPath())
	if err != nil {
		return false
	}
	value := jsTrim(decodeUTF8(raw))
	return strictBase64.MatchString(value) && len(nodeBase64Decode(value)) == 32
}

var strictBase64 = regexp.MustCompile(`^[A-Za-z0-9+/]+={0,2}$`)

// ModuleStatus answers GET /api/generator/module/status in download mode.
func (s *Service) ModuleStatus() (*goapi.Object, error) {
	state, err := s.ModuleState()
	if err != nil {
		return nil, err
	}
	binaries := s.TemplatesHaveBinaries()
	manifest, _, verr := s.ValidateTemplates(s.TemplatesDir())
	var manifestError any
	if verr != nil {
		manifestError = errorMessage(verr)
		manifest = nil
	}
	templateCount := float64(0)
	if manifest != nil {
		list, _ := prop(manifest, "templates").([]any)
		templateCount = float64(len(list))
	}
	ready, err := s.ModuleReady(state)
	if err != nil {
		return nil, err
	}
	var warning any
	seedValid := s.SigningSeedValid()
	switch {
	case !binaries:
		warning = "Templates are missing, invalid, or incomplete. Publish a complete Client release and reinstall."
	case !seedValid:
		warning = "Signing seed is missing or invalid; production Support builds are disabled."
	}
	out := state.Clone()
	out.Set("moduleDir", s.ModuleDir())
	out.Set("templatesDir", s.TemplatesDir())
	out.Set("templatesPresent", s.TemplatesExist())
	out.Set("binariesPresent", binaries)
	out.Set("buildsPossible", binaries)
	out.Set("signingSeedPresent", seedValid)
	out.Set("manifestValid", manifest != nil)
	out.Set("manifestError", manifestError)
	out.Set("templateCount", templateCount)
	out.Set("ready", ready)
	out.Set("clientRepo", s.ClientRepo())
	out.Set("warning", warning)
	return out, nil
}

// errorMessage is err.message || String(err).
func errorMessage(err error) string {
	return err.Error()
}

// AcceptModuleTerms records that the operator accepted the client's
// licence terms.
func (s *Service) AcceptModuleTerms() (*goapi.Object, error) {
	return s.writeModuleState(goapi.NewObject("termsAccepted", true, "error", nil))
}

// CodedError is an error with err.code set, which the routes answer
// differently.
type CodedError struct {
	Code    string
	Message string
	// StatusCode is the HTTP status of a failed GitHub request.
	StatusCode int
	Details    []string
}

func (e *CodedError) Error() string { return e.Message }

// ErrorCode is err.code: the Code of a CodedError, "" otherwise.
func ErrorCode(err error) string {
	var c *CodedError
	if errors.As(err, &c) {
		return c.Code
	}
	return ""
}

// StatusCodeOf is err.statusCode.
func StatusCodeOf(err error) int {
	var c *CodedError
	if errors.As(err, &c) {
		return c.StatusCode
	}
	return 0
}

// flight lets concurrent installs share one run: a second request while
// one runs gets its result.
type flight struct {
	done  chan struct{}
	state *goapi.Object
	err   error
}

func (s *Service) single(run func() (*goapi.Object, error)) (*goapi.Object, error) {
	s.installMu.Lock()
	if f := s.install; f != nil {
		s.installMu.Unlock()
		<-f.done
		return f.state, f.err
	}
	f := &flight{done: make(chan struct{})}
	s.install = f
	s.installMu.Unlock()
	f.state, f.err = run()
	s.installMu.Lock()
	s.install = nil
	s.installMu.Unlock()
	close(f.done)
	return f.state, f.err
}

func termsNotAccepted() error {
	return &CodedError{Code: "terms_not_accepted", Message: "terms_not_accepted"}
}

// InstallFromGitHub downloads the newest release (or the given tag) of
// the client repository that carries a generator-templates-*.tar.gz,
// checks its SHA-256 and installs it.
func (s *Service) InstallFromGitHub(ctx context.Context, repo, tag string) (*goapi.Object, error) {
	return s.single(func() (*goapi.Object, error) { return s.installFromGitHub(ctx, repo, tag) })
}

func (s *Service) installFromGitHub(ctx context.Context, repo, tag string) (*goapi.Object, error) {
	state, err := s.ModuleState()
	if err != nil {
		return nil, err
	}
	if !truthy(state.Get("termsAccepted")) {
		return nil, termsNotAccepted()
	}
	// The repository becomes part of the API address the token goes to:
	// it has to be owner/name and on the allow list, as in embedded mode.
	var requested any
	if jsTrim(repo) != "" {
		requested = repo
	}
	targetRepo, err := s.NormalizeRepo(requested)
	if err != nil {
		return nil, err
	}
	if _, err := s.writeModuleState(goapi.NewObject("status", "downloading", "error", nil)); err != nil {
		return nil, err
	}
	tmpDir := ""
	result, err := func() (*goapi.Object, error) {
		releaseTag := jsTrim(tag)
		release, err := s.resolveRelease(ctx, targetRepo, releaseTag)
		if err != nil {
			return nil, err
		}
		assets := prop(release, "assets")
		asset := pickTemplateAsset(assets)
		if asset == nil || !truthy(prop(asset, "browser_download_url")) {
			msg := "No generator-templates-*.tar.gz asset found on " + targetRepo
			if truthy(prop(release, "tag_name")) {
				msg += " (" + jsString(prop(release, "tag_name")) + ")"
			}
			return nil, &CodedError{Code: "no_template_asset", Message: msg}
		}
		tmpDir = filepath.Join(s.ModuleDir(), ".tmp-install")
		_ = os.RemoveAll(tmpDir)
		if err := os.MkdirAll(tmpDir, 0o777); err != nil {
			return nil, nodeFSError(err, "mkdir", tmpDir)
		}
		name := jsString(or(prop(asset, "name"), "generator-templates.tar.gz"))
		archivePath := filepath.Join(tmpDir, name)
		body, err := s.githubGetBuffer(ctx, jsString(prop(asset, "browser_download_url")), "")
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(archivePath, body, 0o666); err != nil {
			return nil, nodeFSError(err, "open", archivePath)
		}
		version := strings.TrimPrefix(jsString(or(or(or(prop(release, "tag_name"), prop(release, "name")), releaseTag), "unknown")), "v")
		if err := s.verifyArchive(ctx, archivePath, asset, or(assets, []any{})); err != nil {
			return nil, err
		}
		return s.installArchive(ctx, archivePath, version)
	}()
	if err != nil {
		_, _ = s.writeModuleState(goapi.NewObject("status", "error", "error", errorMessage(err)))
	}
	if tmpDir != "" {
		_ = os.RemoveAll(tmpDir)
	}
	return result, err
}

// resolveRelease finds the release to install: the tag (argument, else
// BETTERDESK_CLIENT_RELEASE_TAG) when one is given, else /releases/latest
// when it has the archive, else the newest of the last 30 releases that
// has it.
func (s *Service) resolveRelease(ctx context.Context, targetRepo, preferredTag string) (any, error) {
	apiBase := "https://api.github.com/repos/" + targetRepo + "/releases"
	envTag := jsTrim(preferredTag)
	if preferredTag == "" {
		envTag = jsTrim(s.getenv("BETTERDESK_CLIENT_RELEASE_TAG"))
	}
	if envTag != "" {
		release, err := s.githubGetJSON(ctx, apiBase+"/tags/"+goapi.EncodeURIComponent(envTag))
		if err != nil {
			return nil, err
		}
		if release == nil {
			return nil, typeError("Cannot read properties of null (reading 'assets')")
		}
		if pickTemplateAsset(prop(release, "assets")) != nil {
			return release, nil
		}
		return nil, &CodedError{Code: "no_template_asset", Message: "Release " + envTag + " on " + targetRepo + " has no generator-templates-*.tar.gz asset"}
	}
	latest, err := s.githubGetJSON(ctx, apiBase+"/latest")
	switch {
	case err != nil:
		if code := StatusCodeOf(err); code != 0 && code != 404 {
			return nil, err
		}
	case latest != nil && pickTemplateAsset(prop(latest, "assets")) != nil:
		return latest, nil
	}
	listValue, err := s.githubGetJSON(ctx, apiBase+"?per_page=30")
	if err != nil {
		return nil, err
	}
	list, isList := listValue.([]any)
	if !isList || len(list) == 0 {
		return nil, &CodedError{Code: "no_release", Message: "No GitHub releases found for " + targetRepo + ". " +
			"Publish a release with generator-templates-*.tar.gz " +
			"(workflow betterdesk-desktop-release.yml, tag desktop/*), " +
			"or set BETTERDESK_CLIENT_RELEASE_TAG."}
	}
	for _, release := range list {
		if release == nil {
			return nil, typeError("Cannot read properties of null (reading 'assets')")
		}
		if pickTemplateAsset(prop(release, "assets")) != nil {
			return release, nil
		}
	}
	return nil, &CodedError{Code: "no_template_asset", Message: fmt.Sprintf("No generator-templates-*.tar.gz found in the last %d releases of %s. ", len(list), targetRepo) +
		"Run Client CI workflow betterdesk-desktop-release.yml or attach the archive to a release."}
}

var (
	templateAssetName  = regexp.MustCompile(`(?i)^generator-templates-.*\.tar\.gz$`)
	templateAssetLoose = regexp.MustCompile(`(?i)generator-templates`)
	tarGzSuffix        = regexp.MustCompile(`(?i)\.tar\.gz$`)
	checksumAssetName  = regexp.MustCompile(`(?i)generator-templates.*\.sha256(?:sum)?$`)
	digestValue        = regexp.MustCompile(`(?i)^sha256:([a-f0-9]{64})$`)
	checksumWord       = regexp.MustCompile(`(?i)\b[a-f0-9]{64}\b`)
)

// assetName is String(asset.name || ”).
func assetName(asset any) string {
	return jsString(or(prop(asset, "name"), ""))
}

// pickTemplateAsset takes the release asset generator-templates-*.tar.gz,
// else any .tar.gz with generator-templates in its name.
func pickTemplateAsset(assets any) any {
	list, _ := assets.([]any)
	for _, a := range list {
		if templateAssetName.MatchString(assetName(a)) {
			return a
		}
	}
	for _, a := range list {
		if name := assetName(a); templateAssetLoose.MatchString(name) && tarGzSuffix.MatchString(name) {
			return a
		}
	}
	return nil
}

// checksumAsset is the archive's .sha256 or .sha256sum asset, else any
// generator-templates checksum.
func checksumAsset(assets any, archiveName string) any {
	list, _ := assets.([]any)
	for _, a := range list {
		if name := prop(a, "name"); name == archiveName+".sha256" || name == archiveName+".sha256sum" {
			return a
		}
	}
	for _, a := range list {
		if checksumAssetName.MatchString(assetName(a)) {
			return a
		}
	}
	return nil
}

// verifyArchive compares the archive's SHA-256 with the digest GitHub
// lists for the asset, else with the checksum asset's text.
func (s *Service) verifyArchive(ctx context.Context, archivePath string, asset, assets any) error {
	expected := ""
	if m := digestValue.FindStringSubmatch(jsString(or(prop(asset, "digest"), ""))); m != nil {
		expected = m[1]
	}
	check := checksumAsset(assets, tmpl(asset, "name"))
	if expected == "" && truthy(prop(check, "browser_download_url")) {
		body, err := s.githubGetBuffer(ctx, jsString(prop(check, "browser_download_url")), "text/plain")
		if err != nil {
			return err
		}
		expected = checksumWord.FindString(decodeUTF8(body))
	}
	if expected == "" {
		return codeError("generator_archive_checksum_missing")
	}
	actual, err := sha256File(archivePath)
	if err != nil {
		return err
	}
	if !strings.EqualFold(actual, expected) {
		return codeError("generator_archive_checksum_mismatch")
	}
	return nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", nodeFSError(err, "open", path)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// runTar runs tar and fails with "tar failed (<code>): <stderr>".
func (s *Service) runTar(ctx context.Context, args []string, dir string) (string, error) {
	res, err := s.opts.Run.Run(ctx, Command{Name: "tar", Args: args, Dir: dir})
	if err != nil {
		return "", err
	}
	if res.Code != 0 {
		text := jsTrim(res.Stderr)
		if text == "" {
			text = "unknown"
		}
		return "", fmt.Errorf("tar failed (%s): %s", codeText(res.Code), text)
	}
	return res.Stdout, nil
}

// extractTarGz lists the archive first and refuses absolute paths, drive
// letters and ".." parts, then unpacks it into dest.
func (s *Service) extractTarGz(ctx context.Context, archivePath, dest string) error {
	if err := os.MkdirAll(dest, 0o777); err != nil {
		return nodeFSError(err, "mkdir", dest)
	}
	listing, err := s.runTar(ctx, []string{"-tzf", archivePath}, filepath.Dir(archivePath))
	if err != nil {
		return err
	}
	for _, raw := range splitLines(listing) {
		if raw == "" {
			continue
		}
		entry := strings.ReplaceAll(raw, `\`, "/")
		if strings.HasPrefix(entry, "/") || driveLetter.MatchString(entry) || hasDotDot(entry) {
			return codeError("generator_archive_path_traversal")
		}
	}
	_, err = s.runTar(ctx, []string{"-xzf", archivePath, "-C", dest}, "")
	return err
}

func hasDotDot(entry string) bool {
	for _, part := range strings.Split(entry, "/") {
		if part == ".." {
			return true
		}
	}
	return false
}

// installArchive unpacks a template archive, checks it, and swaps it in
// for the installed templates; the old tree comes back when the swap
// fails. It needs a valid signing seed first.
func (s *Service) installArchive(ctx context.Context, archivePath, version string) (*goapi.Object, error) {
	if _, err := s.CopySigningSeedIfPresent(); err != nil {
		return nil, err
	}
	if !s.SigningSeedValid() {
		return nil, ErrSigningSeedRequired
	}
	tmpDir := filepath.Join(s.ModuleDir(), fmt.Sprintf(".tmp-install-%d-%d", s.opts.PID, s.opts.Now().UnixMilli()))
	extractRoot := filepath.Join(tmpDir, "extract")
	previousDir := filepath.Join(s.ModuleDir(), ".templates.previous")
	dest := s.TemplatesDir()
	defer os.RemoveAll(tmpDir)
	state, err := func() (*goapi.Object, error) {
		_ = os.RemoveAll(tmpDir)
		if err := os.MkdirAll(extractRoot, 0o777); err != nil {
			return nil, nodeFSError(err, "mkdir", extractRoot)
		}
		if err := s.extractTarGz(ctx, archivePath, extractRoot); err != nil {
			return nil, err
		}
		if err := assertNoSymlinks(extractRoot); err != nil {
			return nil, err
		}
		source := filepath.Join(extractRoot, "generator-templates")
		if !exists(source) {
			source = extractRoot
			if entries, err := os.ReadDir(extractRoot); err == nil {
				for _, e := range entries {
					if e.IsDir() {
						source = filepath.Join(extractRoot, e.Name())
						break
					}
				}
			}
		}
		manifest, count, err := s.ValidateTemplates(source)
		if err != nil {
			return nil, err
		}
		_ = os.RemoveAll(previousDir)
		if exists(dest) {
			if err := os.Rename(dest, previousDir); err != nil {
				return nil, nodeFSError(err, "rename", dest)
			}
		}
		if err := os.Rename(source, dest); err != nil {
			if exists(previousDir) && !exists(dest) {
				_ = os.Rename(previousDir, dest)
			}
			return nil, nodeFSError(err, "rename", source)
		}
		_ = os.RemoveAll(previousDir)
		var manifestVersion any
		if v := prop(manifest, "version"); truthy(v) {
			manifestVersion = v
		}
		installed := version
		if installed == "" {
			installed = jsString(or(prop(manifest, "version"), "unknown"))
		}
		return s.writeModuleState(goapi.NewObject(
			"status", "ready",
			"error", nil,
			"installedVersion", installed,
			"manifestVersion", manifestVersion,
			"templateCount", float64(count),
			"installedAt", store.ISOTime(s.opts.Now()),
		))
	}()
	if err != nil {
		_, _ = s.writeModuleState(goapi.NewObject("status", "error", "error", errorMessage(err)))
		return nil, err
	}
	return state, nil
}

// CopySigningSeedIfPresent takes the signing seed from
// BETTERDESK_CUSTOM_CLIENT_SIGNING_SEED, else from the first of these
// files that exists: DataDir, ConsoleDir, ../res/betterdesk next to
// ConsoleDir and the working directory, each custom-client-signing.seed.
// It writes it into the module and reports whether it found one; a seed
// that is not base64 of 32 bytes is an error.
func (s *Service) CopySigningSeedIfPresent() (bool, error) {
	if err := s.ensureModuleDir(); err != nil {
		return false, err
	}
	write := func(value string) (bool, error) {
		if !strictBase64.MatchString(value) || len(nodeBase64Decode(value)) != 32 {
			return false, ErrSigningSeedInvalid
		}
		if err := writeFileMode(s.SigningSeedPath(), []byte(value+"\n"), 0o600); err != nil {
			return false, err
		}
		return true, nil
	}
	if fromEnv := jsTrim(s.getenv("BETTERDESK_CUSTOM_CLIENT_SIGNING_SEED")); fromEnv != "" {
		return write(removeJSSpace(fromEnv))
	}
	candidates := []string{
		filepath.Join(s.opts.DataDir, signingSeedName),
		filepath.Join(s.opts.ConsoleDir, signingSeedName),
		filepath.Join(s.opts.ConsoleDir, "..", "res", "betterdesk", signingSeedName),
		filepath.Join(s.opts.WorkDir, signingSeedName),
	}
	for _, src := range candidates {
		if !exists(src) {
			continue
		}
		raw, err := os.ReadFile(src)
		if err != nil {
			return false, nodeFSError(err, "open", src)
		}
		return write(removeJSSpace(decodeUTF8(raw)))
	}
	return false, nil
}

// removeJSSpace is text.replace(/\s+/g, ”).
func removeJSSpace(text string) string {
	return strings.Map(func(r rune) rune {
		if isJSSpace(r) {
			return -1
		}
		return r
	}, text)
}

// InstallFromLocalArchive installs an uploaded template archive.
func (s *Service) InstallFromLocalArchive(ctx context.Context, archivePath, version string) (*goapi.Object, error) {
	return s.single(func() (*goapi.Object, error) {
		state, err := s.ModuleState()
		if err != nil {
			return nil, err
		}
		if !truthy(state.Get("termsAccepted")) {
			return nil, termsNotAccepted()
		}
		archive := s.absPath(archivePath)
		if !isFile(archive) {
			return nil, codeError("generator_archive_missing")
		}
		if version == "" {
			version = "local"
		}
		result, err := s.installArchive(ctx, archive, version)
		if err != nil {
			_, _ = s.writeModuleState(goapi.NewObject("status", "error", "error", errorMessage(err)))
			return nil, err
		}
		return result, nil
	})
}

// ResolveTemplateDir is the installed template folder of a platform and
// architecture (x64 and amd64 are x86_64, arm64 is aarch64), "" when
// there is none.
func (s *Service) ResolveTemplateDir(platform, arch string) string {
	archMap := map[string]string{"x64": "x86_64", "amd64": "x86_64", "x86_64": "x86_64", "arm64": "aarch64", "aarch64": "aarch64"}
	p := jsbind.ToLower(platform)
	a, ok := archMap[jsbind.ToLower(arch)]
	if !ok {
		a = arch
	}
	name := p + "-" + a
	for _, c := range []string{filepath.Join(s.TemplatesDir(), name), filepath.Join(s.TemplatesDir(), "generator-templates", name)} {
		if exists(c) {
			return c
		}
	}
	return ""
}

// SigningSeed is the seed custom.txt files are signed with: the module's
// seed file, else BETTERDESK_CUSTOM_CLIENT_SIGNING_SEED.
func (s *Service) SigningSeed() string {
	if raw, err := os.ReadFile(s.SigningSeedPath()); err == nil {
		return jsTrim(decodeUTF8(raw))
	} else if exists(s.SigningSeedPath()) {
		return ""
	}
	return jsTrim(s.getenv("BETTERDESK_CUSTOM_CLIENT_SIGNING_SEED"))
}
