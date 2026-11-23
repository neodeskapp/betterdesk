package generator

import (
	"archive/zip"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The client builder: a pinned snapshot of the NeoDesk-Client source
// below DataDir/modules/neodesk-client-builder, kept up to date from
// GitHub with git, and a build provider that compiles the client with the
// signed Support configuration inside (services/clientBuilderService.js).
// The provider is the local builder of this package, an external command
// (NEODESK_CLIENT_BUILD_COMMAND) or a GitHub Actions workflow.

const (
	defaultBuilderRef = "main"
)

var providerTargetKeys = map[string]bool{
	"windows/x64/portable": true, "windows/arm64/portable": true,
	"linux/x64/portable": true, "linux/x64/deb": true, "linux/x64/rpm": true, "linux/x64/arch": true,
	"linux/arm64/portable": true, "linux/arm64/deb": true, "linux/arm64/rpm": true, "linux/arm64/arch": true,
	"macos/x64/portable": true, "macos/arm64/portable": true,
}

func (s *Service) builderRoot() string {
	return filepath.Join(s.opts.DataDir, "modules", "neodesk-client-builder")
}

// SourceDir is the client source snapshot.
func (s *Service) SourceDir() string        { return filepath.Join(s.builderRoot(), "source") }
func (s *Service) builderStatePath() string { return filepath.Join(s.builderRoot(), "state.json") }
func (s *Service) requestRoot() string      { return filepath.Join(s.builderRoot(), "requests") }
func (s *Service) outputRoot() string       { return filepath.Join(s.builderRoot(), "outputs") }
func (s *Service) signingPublicKeyPath() string {
	return filepath.Join(s.builderRoot(), "custom-client-signing.pub")
}

var (
	repoPattern     = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
	commitPattern   = regexp.MustCompile(`(?i)^[a-f0-9]{40}$`)
	requestIDRule   = regexp.MustCompile(`(?i)^[a-f0-9-]{8,128}$`)
	workflowPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
	progressLine    = regexp.MustCompile(`^NEODESK_BUILD_PROGRESS\|(\d{1,3})\|(.+)$`)
)

// NormalizeRepo checks a repository name: owner/name, and one of
// NEODESK_CLIENT_ALLOWED_REPOS (default neodeskapp/neodesk-client).
// value is the request's value as it came; nil or "" take the setting.
func (s *Service) NormalizeRepo(value any) (string, error) {
	repo := jsTrim(jsString(or(or(value, s.getenv("NEODESK_CLIENT_REPO")), defaultClientRepo)))
	if !repoPattern.MatchString(repo) {
		return "", codeError("neodesk_client_repo_invalid")
	}
	allowedText := s.getenv("NEODESK_CLIENT_ALLOWED_REPOS")
	if allowedText == "" {
		allowedText = defaultClientRepo
	}
	for _, entry := range strings.Split(allowedText, ",") {
		if e := jsTrim(entry); e != "" && e == repo {
			return repo, nil
		}
	}
	return "", codeError("neodesk_client_repo_not_allowed")
}

// ClientRef checks a git ref (NEODESK_CLIENT_REF, default main): not
// empty, at most 200 characters, no NUL or line break.
func (s *Service) ClientRef(value any) (string, error) {
	ref := jsTrim(jsString(or(or(value, s.getenv("NEODESK_CLIENT_REF")), defaultBuilderRef)))
	if ref == "" || jsLength(ref) > 200 || strings.ContainsAny(ref, "\x00\r\n") {
		return "", codeError("neodesk_client_ref_invalid")
	}
	return ref, nil
}

// decodeSigningSeed reads a seed: white space removed, strict base64 of
// 32 bytes, else nil.
func decodeSigningSeed(value string) []byte {
	text := removeJSSpace(value)
	if !strictBase64.MatchString(text) {
		return nil
	}
	seed := nodeBase64Decode(text)
	if len(seed) != ed25519.SeedSize {
		return nil
	}
	return seed
}

func (s *Service) readStoredSigningSeed() []byte {
	raw, err := os.ReadFile(s.SigningSeedPath())
	if err != nil {
		return nil
	}
	return decodeSigningSeed(decodeUTF8(raw))
}

// writeSigningFile replaces a file with value and a line break, through
// a temporary file of a name of its own next to it that is renamed onto
// it, so a reader sees the old file or the new one and never none.
func (s *Service) writeSigningFile(path, value string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return nodeFSError(err, "mkdir", filepath.Dir(path))
	}
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".tmp-*")
	if err != nil {
		return nodeFSError(err, "open", path)
	}
	temporary := f.Name()
	_, err = f.WriteString(value + "\n")
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Chmod(temporary, mode)
	}
	if err != nil {
		_ = os.Remove(temporary)
		return nodeFSError(err, "write", temporary)
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return nodeFSError(err, "rename", temporary)
	}
	return nil
}

// SigningKey is the signing key of this install.
type SigningKey struct {
	Seed      string
	PublicKey string
	Generated bool
}

// EnsureSigningKey makes sure the install has a signing seed and its
// public key on disk: the seed from NEODESK_CUSTOM_CLIENT_SIGNING_SEED
// when set, else the stored one, else a new random one. A file that
// holds its value already is left as it is.
func (s *Service) EnsureSigningKey() (SigningKey, error) {
	configured := decodeSigningSeed(s.getenv("NEODESK_CUSTOM_CLIENT_SIGNING_SEED"))
	existing := s.readStoredSigningSeed()
	seed := configured
	if seed == nil {
		seed = existing
	}
	generated := configured == nil && existing == nil
	if seed == nil {
		seed = make([]byte, ed25519.SeedSize)
		if _, err := io.ReadFull(s.opts.Rand, seed); err != nil {
			return SigningKey{}, err
		}
	}
	publicKey := base64.StdEncoding.EncodeToString(ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey))
	seedText := base64.StdEncoding.EncodeToString(seed)
	currentSeed := ""
	if existing != nil {
		currentSeed = base64.StdEncoding.EncodeToString(existing)
	}
	if existing == nil || currentSeed != seedText {
		if err := s.writeSigningFile(s.SigningSeedPath(), seedText, 0o600); err != nil {
			return SigningKey{}, err
		}
	}
	currentPublic := ""
	if raw, err := os.ReadFile(s.signingPublicKeyPath()); err == nil {
		currentPublic = jsTrim(decodeUTF8(raw))
	}
	if currentPublic != publicKey {
		if err := s.writeSigningFile(s.signingPublicKeyPath(), publicKey, 0o644); err != nil {
			return SigningKey{}, err
		}
	}
	return SigningKey{Seed: seedText, PublicKey: publicKey, Generated: generated}, nil
}

// prepareClientSigningPublicKey writes the install's public key into the
// source snapshot, where the build compiles it in.
func (s *Service) prepareClientSigningPublicKey() (SigningKey, error) {
	key, err := s.EnsureSigningKey()
	if err != nil {
		return key, err
	}
	target := filepath.Join(s.SourceDir(), "res", "neodesk", "custom-client-signing.pub")
	if !exists(s.SourceDir()) || !exists(filepath.Dir(target)) {
		return key, codeError("neodesk_client_source_missing_for_signing_key")
	}
	info, err := os.Lstat(target)
	if err != nil {
		return key, nodeFSError(err, "lstat", target)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return key, codeError("neodesk_client_signing_key_symlink")
	}
	raw, err := os.ReadFile(target)
	if err != nil {
		return key, nodeFSError(err, "open", target)
	}
	if jsTrim(decodeUTF8(raw)) != key.PublicKey {
		if err := s.writeSigningFile(target, key.PublicKey, 0o644); err != nil {
			return key, err
		}
	}
	return key, nil
}

// BuilderState reads the builder's state file. Without one it is the
// default state, which checks the configured repository and ref.
func (s *Service) BuilderState() (any, error) {
	raw, err := os.ReadFile(s.builderStatePath())
	if err == nil {
		if v, perr := parseJSON(decodeUTF8(raw)); perr == nil {
			return v, nil
		}
	}
	repo, err := s.NormalizeRepo(nil)
	if err != nil {
		return nil, err
	}
	ref, err := s.ClientRef(nil)
	if err != nil {
		return nil, err
	}
	return goapi.NewObject(
		"repo", repo,
		"ref", ref,
		"termsAccepted", false,
		"installedCommit", nil,
		"status", "not_installed",
		"error", nil,
	), nil
}

// writeBuilderState lays patch over the state and replaces the file.
func (s *Service) writeBuilderState(patch *goapi.Object) (*goapi.Object, error) {
	if err := os.MkdirAll(s.builderRoot(), 0o777); err != nil {
		return nil, nodeFSError(err, "mkdir", s.builderRoot())
	}
	current, err := s.BuilderState()
	if err != nil {
		return nil, err
	}
	next := spread(spread(goapi.NewObject(), current), patch)
	temporary := fmt.Sprintf("%s.tmp-%d-%d", s.builderStatePath(), s.opts.PID, s.opts.Now().UnixMilli())
	if err := writeFileMode(temporary, []byte(dbText(stringifyIndent(next)+"\n")), 0o600); err != nil {
		return nil, err
	}
	if err := os.Rename(temporary, s.builderStatePath()); err != nil {
		return nil, nodeFSError(err, "rename", temporary)
	}
	return next, nil
}

// InstalledCommit is the commit of the source snapshot when it is a full
// commit id, "" otherwise. The error is the old code's when the state
// cannot be read (see BuilderState) or is null.
func (s *Service) InstalledCommit() (string, error) {
	state, err := s.BuilderState()
	if err != nil {
		return "", err
	}
	if state == nil {
		return "", typeError("Cannot read properties of null (reading 'installedCommit')")
	}
	commit := jsTrim(jsString(or(prop(state, "installedCommit"), "")))
	if commitPattern.MatchString(commit) {
		return commit, nil
	}
	return "", nil
}

// RemoteRevision is the commit the repository's ref points at.
type RemoteRevision struct {
	Repo, Ref, SHA string
}

// GetRemoteRevision asks the API for the commit of the ref.
func (s *Service) GetRemoteRevision(ctx context.Context, repo, ref any) (RemoteRevision, error) {
	targetRepo, err := s.NormalizeRepo(repo)
	if err != nil {
		return RemoteRevision{}, err
	}
	targetRef, err := s.ClientRef(ref)
	if err != nil {
		return RemoteRevision{}, err
	}
	commit, err := s.githubJSON(ctx, "https://api.github.com/repos/"+targetRepo+"/commits/"+goapi.EncodeURIComponent(targetRef))
	if err != nil {
		return RemoteRevision{}, err
	}
	sha := jsTrim(jsString(or(prop(commit, "sha"), "")))
	if !commitPattern.MatchString(sha) {
		return RemoteRevision{}, codeError("neodesk_client_commit_invalid")
	}
	return RemoteRevision{Repo: targetRepo, Ref: targetRef, SHA: sha}, nil
}

// SyncResult is what a source sync did.
type SyncResult struct {
	Changed   bool
	Remote    RemoteRevision
	SourceDir string
}

// Object is the result as the route sends it.
func (r SyncResult) Object() *goapi.Object {
	return goapi.NewObject("changed", r.Changed, "repo", r.Remote.Repo, "ref", r.Remote.Ref, "sha", r.Remote.SHA, "sourceDir", r.SourceDir)
}

// runCommand runs a tool and keeps the last 16,000 characters of its
// output.
func (s *Service) runCommand(ctx context.Context, name string, args []string, dir string, env []string) (Result, error) {
	if env == nil {
		env = s.childEnv(nil)
	}
	res, err := s.opts.Run.Run(ctx, Command{Name: name, Args: args, Dir: dir, Env: env})
	if err != nil {
		return res, err
	}
	res.Stdout, res.Stderr = lastChars(res.Stdout, 16000), lastChars(res.Stderr, 16000)
	return res, nil
}

// SyncSource fetches the ref's commit into the source snapshot with git,
// unless the snapshot has it already (or force is set). A failed sync
// leaves the old snapshot.
func (s *Service) SyncSource(ctx context.Context, repo, ref any, force bool) (SyncResult, error) {
	remote, err := s.GetRemoteRevision(ctx, repo, ref)
	if err != nil {
		return SyncResult{}, err
	}
	state, err := s.BuilderState()
	if err != nil {
		return SyncResult{}, err
	}
	installed := prop(state, "installedCommit")
	if !force && installed == remote.SHA && exists(filepath.Join(s.SourceDir(), "Cargo.toml")) {
		if prop(state, "status") != "ready" || prop(state, "phase") != "ready" {
			if _, err := s.writeBuilderState(goapi.NewObject("status", "ready", "phase", "ready", "error", nil)); err != nil {
				return SyncResult{}, err
			}
		}
		return SyncResult{Changed: false, Remote: remote, SourceDir: s.SourceDir()}, nil
	}
	temporary := filepath.Join(s.builderRoot(), fmt.Sprintf(".sync-%d-%d", s.opts.PID, s.opts.Now().UnixMilli()))
	staged := filepath.Join(temporary, "source")
	defer os.RemoveAll(temporary)
	result, err := func() (SyncResult, error) {
		if _, err := s.writeBuilderState(goapi.NewObject("status", "downloading", "phase", "syncing_source", "error", nil, "syncStartedAt", isoNow(s))); err != nil {
			return SyncResult{}, err
		}
		_ = os.RemoveAll(temporary)
		if err := os.MkdirAll(temporary, 0o777); err != nil {
			return SyncResult{}, nodeFSError(err, "mkdir", temporary)
		}
		clone, err := s.runCommand(ctx, "git", []string{"clone", "--filter=blob:none", "--no-tags", "--no-checkout", "https://github.com/" + remote.Repo + ".git", staged}, "", nil)
		if err != nil {
			return SyncResult{}, err
		}
		if clone.Code != 0 {
			return SyncResult{}, codeError("neodesk_client_git_clone_failed:" + clone.Stderr)
		}
		if _, err := s.writeBuilderState(goapi.NewObject("phase", "syncing_submodules")); err != nil {
			return SyncResult{}, err
		}
		checkout, err := s.runCommand(ctx, "git", []string{"-C", staged, "checkout", "--detach", remote.SHA}, "", nil)
		if err != nil {
			return SyncResult{}, err
		}
		if checkout.Code != 0 {
			fetch, err := s.runCommand(ctx, "git", []string{"-C", staged, "fetch", "--depth", "1", "origin", remote.SHA}, "", nil)
			if err != nil {
				return SyncResult{}, err
			}
			if fetch.Code == 0 {
				if checkout, err = s.runCommand(ctx, "git", []string{"-C", staged, "checkout", "--detach", remote.SHA}, "", nil); err != nil {
					return SyncResult{}, err
				}
			}
		}
		if checkout.Code != 0 {
			return SyncResult{}, codeError("neodesk_client_git_checkout_failed:" + checkout.Stderr)
		}
		submodules, err := s.runCommand(ctx, "git", []string{"-C", staged, "submodule", "update", "--init", "--recursive"}, "", nil)
		if err != nil {
			return SyncResult{}, err
		}
		if submodules.Code != 0 {
			return SyncResult{}, codeError("neodesk_client_submodule_failed:" + submodules.Stderr)
		}
		if !exists(filepath.Join(staged, "Cargo.toml")) || !exists(filepath.Join(staged, "libs", "hbb_common", "Cargo.toml")) {
			return SyncResult{}, codeError("neodesk_client_source_invalid")
		}
		previous := filepath.Join(s.builderRoot(), ".source.previous")
		_ = os.RemoveAll(previous)
		if exists(s.SourceDir()) {
			if err := os.Rename(s.SourceDir(), previous); err != nil {
				return SyncResult{}, nodeFSError(err, "rename", s.SourceDir())
			}
		}
		if err := os.Rename(staged, s.SourceDir()); err != nil {
			if exists(previous) && !exists(s.SourceDir()) {
				_ = os.Rename(previous, s.SourceDir())
			}
			return SyncResult{}, nodeFSError(err, "rename", staged)
		}
		_ = os.RemoveAll(previous)
		if _, err := s.writeBuilderState(goapi.NewObject(
			"repo", remote.Repo,
			"ref", remote.Ref,
			"installedCommit", remote.SHA,
			"syncedAt", isoNow(s),
			"status", "ready",
			"phase", "ready",
			"error", nil,
		)); err != nil {
			return SyncResult{}, err
		}
		return SyncResult{Changed: installed != remote.SHA, Remote: remote, SourceDir: s.SourceDir()}, nil
	}()
	if err != nil {
		_, _ = s.writeBuilderState(goapi.NewObject("status", "error", "phase", "error", "error", errorMessage(err)))
		return SyncResult{}, err
	}
	return result, nil
}

// SyncAndQueueRebuilds is the start-up and update hook of embedded mode:
// it syncs the source and, when the commit changed, queues a new
// generation of every live bundle. The bundles keep handing out their
// current builds until the new ones are all ready.
func (s *Service) SyncAndQueueRebuilds(ctx context.Context, force bool) (*goapi.Object, error) {
	result, err := s.SyncSource(ctx, nil, nil, force)
	if err != nil {
		return nil, err
	}
	out := result.Object()
	if !result.Changed && !force {
		out.Set("queued", false)
		return out, nil
	}
	rebuild, err := s.RebuildAllForClientRevision(ctx, result.Remote.SHA, false)
	if err != nil {
		return nil, err
	}
	out.Set("queued", true)
	out.Set("rebuild", rebuild)
	return out, nil
}

func (s *Service) externalProviderCommand() string {
	return jsTrim(s.getenv("NEODESK_CLIENT_BUILD_COMMAND"))
}

func normalizeProviderTarget(raw any) *goapi.Object {
	platform := strings.ToLower(jsTrim(jsString(or(or(prop(raw, "platform"), prop(raw, "os")), ""))))
	arch := strings.ToLower(jsTrim(jsString(or(prop(raw, "arch"), "x64"))))
	format := strings.ToLower(jsTrim(jsString(or(prop(raw, "format"), "portable"))))
	if !providerTargetKeys[platform+"/"+arch+"/"+format] {
		return nil
	}
	return goapi.NewObject("platform", platform, "arch", arch, "format", format)
}

// configuredProviderTargets reads the targets an external provider
// builds: NEODESK_CLIENT_PROVIDER_TARGETS_JSON, else the file in
// NEODESK_CLIENT_PROVIDER_TARGETS_FILE; a list or {targets: [...]}.
func (s *Service) configuredProviderTargets() ([]*goapi.Object, string) {
	raw := jsTrim(s.getenv("NEODESK_CLIENT_PROVIDER_TARGETS_JSON"))
	if raw == "" {
		if file := jsTrim(s.getenv("NEODESK_CLIENT_PROVIDER_TARGETS_FILE")); file != "" {
			data, err := os.ReadFile(file)
			if err != nil {
				code := "unknown"
				if e := nodeFSError(err, "open", file).Error(); strings.Contains(e, ":") {
					code = e[:strings.Index(e, ":")]
				}
				return []*goapi.Object{}, "provider_targets_file_unreadable:" + code
			}
			raw = decodeUTF8(data)
		}
	}
	if raw == "" {
		return []*goapi.Object{}, "provider_targets_missing"
	}
	parsed, err := parseJSON(raw)
	if err != nil {
		return []*goapi.Object{}, "provider_targets_invalid"
	}
	values, isList := parsed.([]any)
	if !isList {
		values, isList = prop(parsed, "targets").([]any)
	}
	if !isList {
		return []*goapi.Object{}, "provider_targets_invalid"
	}
	var order []string
	byKey := map[string]*goapi.Object{}
	for _, v := range values {
		t := normalizeProviderTarget(v)
		if t == nil {
			continue
		}
		key := t.Str("platform") + "/" + t.Str("arch") + "/" + t.Str("format")
		if _, seen := byKey[key]; !seen {
			order = append(order, key)
		}
		byKey[key] = t
	}
	targets := make([]*goapi.Object, 0, len(order))
	for _, key := range order {
		targets = append(targets, byKey[key])
	}
	if len(targets) == 0 {
		return []*goapi.Object{}, "provider_targets_invalid"
	}
	return targets, ""
}

func (s *Service) githubActionsEnabled() bool {
	v := s.getenv("NEODESK_CLIENT_GITHUB_ACTIONS")
	if v == "" {
		v = "off"
	}
	return strings.ToLower(jsTrim(v)) != "off"
}

func (s *Service) githubTokenPresent() bool { return s.githubToken() != "" }

func (s *Service) usesRemoteProvider() bool {
	return s.externalProviderCommand() != "" || (s.githubActionsEnabled() && s.githubTokenPresent())
}

// BuildCapabilities says what the build provider can build: the targets
// of a remote provider as configured, or what the local toolchain makes
// on this machine.
func (s *Service) BuildCapabilities() *goapi.Object {
	if s.usesRemoteProvider() {
		targets, reason := s.configuredProviderTargets()
		provider := "github-actions"
		if s.externalProviderCommand() != "" {
			provider = "command"
		}
		missing := []any{}
		var reasonValue any
		if len(targets) == 0 {
			missing = []any{reason}
			reasonValue = reason
		}
		return goapi.NewObject(
			"mode", "remote",
			"provider", provider,
			"targets", objectList(targets),
			"host", nil,
			"toolchain", goapi.NewObject("ready", len(targets) > 0, "missing", missing),
			"buildReady", len(targets) > 0,
			"reason", reasonValue,
		)
	}
	caps := s.LocalCapabilities()
	toolchain := caps.Object("toolchain")
	targets, _ := caps.Get("targets").([]any)
	ready := truthy(toolchain.Get("ready"))
	out := caps.Clone()
	out.Set("buildReady", ready && len(targets) > 0)
	switch {
	case !ready:
		out.Set("reason", "local_builder_toolchain_missing")
	case len(targets) == 0:
		out.Set("reason", "local_builder_no_supported_targets")
	default:
		out.Set("reason", nil)
	}
	return out
}

func objectList(list []*goapi.Object) []any {
	out := make([]any, len(list))
	for i, o := range list {
		out[i] = o
	}
	return out
}

// BuildReadiness says whether the source is ready and builds can run.
func (s *Service) BuildReadiness() *goapi.Object {
	sourceReady := s.BuilderReady()
	caps := s.BuildCapabilities()
	reason := caps.Get("reason")
	if !sourceReady {
		reason = "neodesk_client_builder_not_ready"
	}
	return goapi.NewObject(
		"sourceReady", sourceReady,
		"buildReady", sourceReady && truthy(caps.Get("buildReady")),
		"reason", reason,
		"capabilities", caps,
	)
}

func (s *Service) signingSeedPresent() bool {
	if _, err := s.EnsureSigningKey(); err != nil {
		return false
	}
	return s.SigningSeedValid()
}

// BuilderReady reports a builder that can build: terms accepted, a
// synced commit, the source there and a signing seed. An unreadable
// state counts as not ready.
func (s *Service) BuilderReady() bool {
	ready, _ := s.BuilderReadyErr()
	return ready
}

// BuilderReadyErr is BuilderReady with the error the old code threw when
// the state could not be read.
func (s *Service) BuilderReadyErr() (bool, error) {
	state, err := s.BuilderState()
	if err != nil {
		return false, err
	}
	return truthy(prop(state, "termsAccepted")) && truthy(prop(state, "installedCommit")) && prop(state, "status") == "ready" &&
		exists(filepath.Join(s.SourceDir(), "Cargo.toml")) && s.signingSeedPresent(), nil
}

// AcceptBuilderTerms records that the operator accepted the client's
// licence terms.
func (s *Service) AcceptBuilderTerms() (*goapi.Object, error) {
	return s.writeBuilderState(goapi.NewObject("termsAccepted", true, "error", nil))
}

// buildTimeout is NEODESK_CLIENT_BUILD_TIMEOUT_MS (buildTimeoutOf).
func (s *Service) buildTimeout() time.Duration {
	return buildTimeoutOf(s.getenv("NEODESK_CLIENT_BUILD_TIMEOUT_MS"))
}

// buildTimeoutOf is the time an embedded build may take, from the
// setting's text: at least a minute, and an hour when the setting is
// empty or no number.
func buildTimeoutOf(raw string) time.Duration {
	ms, ok := jsParseInt(raw)
	if !ok {
		return time.Hour
	}
	if ms < 60000 {
		ms = 60000
	}
	return time.Duration(ms) * time.Millisecond
}

// jsParseInt is parseInt(text, 10) for the cases a setting meets.
func jsParseInt(text string) (int64, bool) {
	t := strings.TrimLeftFunc(text, isJSSpace)
	sign := int64(1)
	if strings.HasPrefix(t, "-") || strings.HasPrefix(t, "+") {
		if t[0] == '-' {
			sign = -1
		}
		t = t[1:]
	}
	n, digits := int64(0), 0
	for digits < len(t) && t[digits] >= '0' && t[digits] <= '9' {
		if n < 1<<53 {
			n = n*10 + int64(t[digits]-'0')
		}
		digits++
	}
	if digits == 0 {
		return 0, false
	}
	return sign * n, true
}

// BuildResult is where a provider left its output.
type BuildResult struct {
	OutputDir string
	Stdout    string
	Stderr    string
}

// Progress takes a build's progress.
type Progress func(percent float64, phase string)

// RunBuild hands one build request to the provider: the request file
// with the install's public key below requests/, the output folder below
// outputs/, both named by the generation.
func (s *Service) RunBuild(ctx context.Context, request *goapi.Object, onProgress Progress) (BuildResult, error) {
	readiness := s.BuildReadiness()
	if !truthy(readiness.Get("sourceReady")) {
		return BuildResult{}, codeError("neodesk_client_builder_not_ready")
	}
	if !truthy(readiness.Get("buildReady")) {
		reason := jsString(or(readiness.Get("reason"), "neodesk_client_builder_no_supported_targets"))
		return BuildResult{}, codeError(reason)
	}
	key, err := s.prepareClientSigningPublicKey()
	if err != nil {
		return BuildResult{}, err
	}
	request = request.Clone()
	request.Set("signing_public_key", key.PublicKey)
	requestID := jsTrim(jsString(or(request.Get("generationId"), "")))
	if !requestIDRule.MatchString(requestID) {
		return BuildResult{}, codeError("neodesk_client_generation_id_invalid")
	}
	for _, dir := range []string{s.requestRoot(), s.outputRoot()} {
		if err := os.MkdirAll(dir, 0o777); err != nil {
			return BuildResult{}, nodeFSError(err, "mkdir", dir)
		}
	}
	requestPath := filepath.Join(s.requestRoot(), requestID+".json")
	outputDir := filepath.Join(s.outputRoot(), requestID)
	_ = os.RemoveAll(outputDir)
	if err := os.MkdirAll(outputDir, 0o777); err != nil {
		return BuildResult{}, nodeFSError(err, "mkdir", outputDir)
	}
	if err := writeFileMode(requestPath, []byte(dbText(stringifyIndent(request)+"\n")), 0o600); err != nil {
		return BuildResult{}, err
	}
	progress := func(percent float64, phase string) {
		if onProgress != nil {
			onProgress(percent, phase)
		}
	}

	external := s.externalProviderCommand()
	if external == "" && s.githubActionsEnabled() && s.githubTokenPresent() {
		progress(10, "queued_remote_build")
		result, err := s.runGithubActionsBuild(ctx, request, requestPath, outputDir)
		if err != nil {
			return BuildResult{}, err
		}
		progress(100, "complete")
		return result, nil
	}
	env := s.childEnv(map[string]string{
		"NEODESK_CLIENT_SOURCE_DIR":    s.SourceDir(),
		"NEODESK_CLIENT_OUTPUT_DIR":    outputDir,
		"NEODESK_CLIENT_BUILD_REQUEST": requestPath,
	})
	var res Result
	if external != "" {
		res, err = s.opts.Run.Run(ctx, Command{
			Name: external, Args: []string{requestPath}, Dir: s.SourceDir(), Env: env, Timeout: s.buildTimeout(),
			Stdout: &progressWriter{report: progress},
		})
		if err != nil {
			return BuildResult{}, err
		}
	} else {
		res = s.runLocalBuilder(ctx, requestPath, s.SourceDir(), outputDir, env, progress)
	}
	if res.TimedOut {
		return BuildResult{}, codeError("neodesk_client_build_timeout")
	}
	if res.Code != 0 {
		return BuildResult{}, codeError("neodesk_client_build_failed:" + codeText(res.Code) + ":" + lastChars(res.Stderr, 4000))
	}
	return BuildResult{OutputDir: outputDir, Stdout: lastChars(res.Stdout, 4000), Stderr: lastChars(res.Stderr, 4000)}, nil
}

// progressWriter reads NEODESK_BUILD_PROGRESS|<percent>|<phase> lines
// from a provider's output.
type progressWriter struct {
	report  Progress
	pending string
}

func (p *progressWriter) Write(b []byte) (int, error) {
	text := p.pending + decodeUTF8(b)
	lines := splitLines(text)
	p.pending = lines[len(lines)-1]
	for _, line := range lines[:len(lines)-1] {
		reportProgress(line, p.report)
	}
	return len(b), nil
}

func reportProgress(line string, report Progress) {
	m := progressLine.FindStringSubmatch(line)
	if m == nil {
		return
	}
	percent, _ := strconv.Atoi(m[1])
	if percent > 100 {
		percent = 100
	}
	report(float64(percent), jsTrim(m[2]))
}

// runGithubActionsBuild dispatches the Support build workflow, waits for
// its run, downloads the artifact and unpacks it into the output folder.
func (s *Service) runGithubActionsBuild(ctx context.Context, request *goapi.Object, requestPath, outputDir string) (BuildResult, error) {
	state, err := s.BuilderState()
	if err != nil {
		return BuildResult{}, err
	}
	repo, err := s.NormalizeRepo(prop(state, "repo"))
	if err != nil {
		return BuildResult{}, err
	}
	workflow := s.getenv("NEODESK_CLIENT_BUILD_WORKFLOW")
	if workflow == "" {
		workflow = "support-build.yml"
	}
	workflow = jsTrim(workflow)
	if !workflowPattern.MatchString(workflow) {
		return BuildResult{}, codeError("neodesk_client_workflow_invalid")
	}
	startedAt := s.opts.Now()
	clientCommit := or(request.Get("clientCommit"), prop(state, "installedCommit"))
	targets := request.Get("targets")
	if !truthy(targets) {
		targets = []any{}
	}
	base := "https://api.github.com/repos/" + repo + "/actions/workflows/" + workflow
	if _, err := s.githubRequest(ctx, "POST", base+"/dispatches", goapi.NewObject(
		"ref", or(prop(state, "ref"), defaultBuilderRef),
		"inputs", goapi.NewObject(
			"generation_id", request.Get("generationId"),
			"config_b64", request.Get("signed_config_b64"),
			"client_commit", clientCommit,
			"targets_json", stringify(targets),
		),
	)); err != nil {
		return BuildResult{}, err
	}
	runsURL := base + "/runs?event=workflow_dispatch&per_page=20"
	deadline := s.opts.Now().Add(s.buildTimeout())
	var run any
	for s.opts.Now().Before(deadline) {
		data, err := s.githubJSON(ctx, runsURL)
		if err != nil {
			return BuildResult{}, err
		}
		runs, _ := prop(data, "workflow_runs").([]any)
		want := strings.ToLower(jsString(clientCommit))
		for _, candidate := range runs {
			created := jsDateMillis(prop(candidate, "created_at"))
			if strings.ToLower(jsString(or(prop(candidate, "head_sha"), ""))) == want && created >= float64(startedAt.UnixMilli()-90000) {
				run = candidate
				break
			}
		}
		if run != nil {
			break
		}
		if err := s.opts.Sleep(ctx, 5*time.Second); err != nil {
			return BuildResult{}, err
		}
	}
	if run == nil {
		return BuildResult{}, codeError("neodesk_client_workflow_run_not_found")
	}
	runID := jsString(prop(run, "id"))
	runState := run
	for s.opts.Now().Before(deadline) {
		if runState, err = s.githubJSON(ctx, "https://api.github.com/repos/"+repo+"/actions/runs/"+runID); err != nil {
			return BuildResult{}, err
		}
		if prop(runState, "status") == "completed" {
			break
		}
		if err := s.opts.Sleep(ctx, 10*time.Second); err != nil {
			return BuildResult{}, err
		}
	}
	if prop(runState, "status") != "completed" || prop(runState, "conclusion") != "success" {
		return BuildResult{}, codeError("neodesk_client_workflow_failed:" + jsString(or(prop(runState, "status"), "unknown")) + ":" + jsString(or(prop(runState, "conclusion"), "unknown")))
	}
	artifacts, err := s.githubJSON(ctx, "https://api.github.com/repos/"+repo+"/actions/runs/"+runID+"/artifacts?per_page=100")
	if err != nil {
		return BuildResult{}, err
	}
	list, _ := prop(artifacts, "artifacts").([]any)
	var artifact any
	name := "neodesk-support-" + jsString(request.Get("generationId"))
	for _, candidate := range list {
		if prop(candidate, "name") == name && !truthy(prop(candidate, "expired")) {
			artifact = candidate
			break
		}
	}
	if !truthy(prop(artifact, "archive_download_url")) {
		return BuildResult{}, codeError("neodesk_client_workflow_artifact_missing")
	}
	archivePath := filepath.Join(s.requestRoot(), jsString(request.Get("generationId"))+".actions.zip")
	if err := s.downloadToFile(ctx, jsString(prop(artifact, "archive_download_url")), archivePath); err != nil {
		return BuildResult{}, err
	}
	if err := extractWorkflowArtifact(archivePath, outputDir); err != nil {
		return BuildResult{}, err
	}
	_ = os.Remove(archivePath)
	return BuildResult{OutputDir: outputDir, Stdout: "GitHub Actions run " + runID}, nil
}

// jsDateMillis is new Date(value || 0).getTime() for the ISO times GitHub
// sends; NaN for anything else.
func jsDateMillis(v any) float64 {
	if !truthy(v) {
		return 0
	}
	text, ok := v.(string)
	if !ok {
		return jsNumber(v)
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05Z07:00"} {
		if t, err := time.Parse(layout, text); err == nil {
			return float64(t.UnixMilli())
		}
	}
	return mathNaN()
}

// extractWorkflowArtifact unpacks the workflow's zip: an entry with an
// absolute path, a drive letter, a ".." part or a custom.txt refuses the
// whole archive.
func extractWorkflowArtifact(archivePath, outputDir string) error {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return codeError("Invalid or unsupported zip format. No END header found")
	}
	defer zr.Close()
	for _, f := range zr.File {
		name := strings.ReplaceAll(f.Name, `\`, "/")
		if strings.HasPrefix(name, "/") || driveLetter.MatchString(name) || hasDotDot(name) || strings.HasSuffix(strings.ToLower(name), "/custom.txt") {
			return codeError("neodesk_client_workflow_artifact_invalid")
		}
	}
	for _, f := range zr.File {
		target := filepath.Join(outputDir, filepath.FromSlash(strings.ReplaceAll(f.Name, `\`, "/")))
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o777); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o777); err != nil {
			return err
		}
		src, err := f.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
		if err != nil {
			src.Close()
			return err
		}
		_, err = io.Copy(out, src)
		src.Close()
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// BuilderStatus answers GET /api/generator/module/status in embedded
// mode. It makes a signing key when the install has none, and asks GitHub
// for the ref's commit.
func (s *Service) BuilderStatus(ctx context.Context) (*goapi.Object, error) {
	state, err := s.BuilderState()
	if err != nil {
		return nil, err
	}
	key, err := s.EnsureSigningKey()
	if err != nil {
		return nil, err
	}
	readiness := s.BuildReadiness()
	var remoteSHA any
	var updateError any
	remote, rerr := s.GetRemoteRevision(ctx, prop(state, "repo"), prop(state, "ref"))
	if rerr != nil {
		updateError = errorMessage(rerr)
	} else {
		remoteSHA = remote.SHA
	}
	cargo := exists(filepath.Join(s.SourceDir(), "Cargo.toml"))
	out := spread(goapi.NewObject(), state)
	out.Set("mode", "embedded")
	out.Set("sourceDir", s.SourceDir())
	out.Set("providerConfigured", true)
	out.Set("buildCapabilities", s.BuildCapabilities())
	out.Set("templatesPresent", cargo)
	out.Set("binariesPresent", cargo)
	out.Set("signingSeedPresent", s.signingSeedPresent())
	out.Set("signingKeyGenerated", key.Generated)
	out.Set("signingPublicKey", key.PublicKey)
	out.Set("signingSeedPath", s.SigningSeedPath())
	out.Set("ready", readiness.Get("sourceReady"))
	out.Set("buildReady", readiness.Get("buildReady"))
	out.Set("buildReadiness", readiness)
	out.Set("remoteCommit", remoteSHA)
	out.Set("updateAvailable", remoteSHA != nil && remoteSHA != prop(state, "installedCommit"))
	out.Set("updateError", updateError)
	return out, nil
}

// GenerationKey is the hash of a build generation: a profile built from
// one client commit.
func GenerationKey(configFingerprint, clientCommit string) string {
	sum := sha256.Sum256([]byte(configFingerprint + ":" + clientCommit))
	return hex.EncodeToString(sum[:])
}

func isoNow(s *Service) string { return isoTimeOf(s.opts.Now()) }

// isoTimeOf is Date.toISOString.
func isoTimeOf(t time.Time) string { return store.ISOTime(t) }
