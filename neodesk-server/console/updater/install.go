package updater

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

// Deploy is how the binary was put in place (serverDeploy of the answer).
type Deploy struct {
	Success    bool    `json:"success"`
	Error      *string `json:"error"`
	BackupPath *string `json:"backupPath"`
	TargetPath string  `json:"targetPath"`
	Method     string  `json:"method"`
}

// Step is a step of an install that the panel reports (serverBuild):
// here the download and check of the release binary.
type Step struct {
	Success  bool   `json:"success"`
	Duration int64  `json:"duration"`
	Version  string `json:"version"`
	Method   string `json:"method"`
}

// InstallResult is the answer of an install, in the shape of the old
// updater's result. Applied names the installed binary.
type InstallResult struct {
	Applied             []string         `json:"applied"`
	Failed              []Failure        `json:"failed"`
	Removed             []string         `json:"removed"`
	Skipped             []string         `json:"skipped"`
	NpmInstalled        bool             `json:"npmInstalled"`
	ServicesRestarted   []string         `json:"servicesRestarted"`
	ServicesFailed      []ServiceFailure `json:"servicesFailed"`
	BackupPath          *string          `json:"backupPath"`
	BackedUp            int              `json:"backedUp"`
	NeedsConsoleRestart bool             `json:"needsConsoleRestart"`
	NeedsServerRestart  bool             `json:"needsServerRestart"`
	NeedsAgentRestart   bool             `json:"needsAgentRestart"`
	SelectedComponents  []string         `json:"selectedComponents"`
	ShaSaved            bool             `json:"shaSaved"`
	ServerBuild         *Step            `json:"serverBuild"`
	ServerDeploy        *Deploy          `json:"serverDeploy"`
	CriticalFailures    []Failure        `json:"criticalFailures"`
	NonCriticalFailures []Failure        `json:"nonCriticalFailures"`
	Signature           Trust            `json:"signature"`
	Version             string           `json:"version"`
	// ConsoleRestartBlocked says why the process does not end by itself;
	// it is only there when it does not.
	ConsoleRestartBlocked *string `json:"consoleRestartBlocked,omitempty"`
}

// NoChange is the answer of an install when the running binary is the
// release's already.
type NoChange struct {
	Applied []string `json:"applied"`
	Message string   `json:"message"`
}

// RestartBlocked is what the panel shows when no supervisor starts the
// process again (spec 4.5).
const RestartBlocked = "Start the server again to finish the update"

// InstallOptions are what the operator chose.
type InstallOptions struct {
	// SHA is the release commit the operator saw in the check.
	SHA string
	// Backup makes a backup first.
	Backup bool
}

// Install installs the newest release of the channel (spec 4.4): it
// checks that the release is the one the operator saw, newer or as new
// as the running binary, signed as required, and has a binary for this
// platform; downloads the binary into DATA_DIR/updates/<version>/ and
// checks its size and SHA-256; runs it with -version and checks the
// version and commit it names; makes the backup; swaps the binary (the
// old one stays as .prev); and writes the pending marker, so that the
// new binary is replaced by the old one when it does not become ready.
// The process is not ended here: the route does that after its answer.
//
// The answer is *InstallResult, or *NoChange when the release is what
// runs. Every failure before the swap leaves the install as it was.
func (s *Service) Install(ctx context.Context, o InstallOptions) (any, error) {
	if s.ImageMode() {
		return nil, errors.New("In-app updates are disabled in Docker image deployments. Update containers instead: " + s.DockerHint())
	}
	if !s.busy.CompareAndSwap(false, true) {
		return nil, ErrBusy
	}
	defer s.busy.Store(false)
	defer s.files.clearInProgress()
	if s.opts.Commit == "" {
		return nil, errNoBaseline
	}

	rel, err := s.lookupRelease(ctx, "", true)
	if err != nil {
		return nil, err
	}
	if !shaMatches(o.SHA, rel.Manifest.SHA) {
		return nil, &RequestError{Status: 409, Message: "The newest release of the update channel is now " + prefix7(rel.Manifest.SHA) + "; check for updates again"}
	}
	if shaMatches(s.opts.Commit, rel.Manifest.SHA) {
		return &NoChange{Applied: []string{}, Message: "No files to update"}, nil
	}
	if CompareVersions(rel.Manifest.Version, s.opts.Version) < 0 {
		return nil, fmt.Errorf("release %s is older than the running version %s", rel.Manifest.Version, s.opts.Version)
	}
	return s.installRelease(ctx, rel, "update", o.Backup)
}

// Repair installs the release of the running version again (the rebuild
// route): the channel's release when it has the running version, else the
// release tagged v<version>.
func (s *Service) Repair(ctx context.Context) (*InstallResult, error) {
	if s.ImageMode() {
		return nil, errors.New("Server rebuild is not available in Docker image deployments. Update the server container instead: " + s.DockerHint())
	}
	if !s.busy.CompareAndSwap(false, true) {
		return nil, ErrBusy
	}
	defer s.busy.Store(false)
	defer s.files.clearInProgress()

	rel, err := s.lookupRelease(ctx, "", true)
	if err != nil || rel.Manifest.Version != s.opts.Version {
		rel, err = s.lookupRelease(ctx, "v"+s.opts.Version, true)
		if err != nil {
			return nil, fmt.Errorf("no release of version %s found: %w", s.opts.Version, err)
		}
	}
	if rel.Manifest.Version != s.opts.Version {
		return nil, fmt.Errorf("no release of version %s found", s.opts.Version)
	}
	result, err := s.installRelease(ctx, rel, "repair", false)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// installRelease does the work of Install and Repair.
func (s *Service) installRelease(ctx context.Context, rel *Release, kind string, backup bool) (*InstallResult, error) {
	if !rel.Trust.OK {
		return nil, errors.New(rel.Trust.Reason)
	}
	if rel.Asset == nil {
		return nil, fmt.Errorf("release %s has no binary for %s/%s", rel.Tag, s.opts.GOOS, s.opts.GOARCH)
	}
	paths, err := ResolveBinaryPaths(s.opts.Executable)
	if err != nil {
		return nil, err
	}
	if err := s.files.markInProgress(rel.Manifest.SHA, rel.Manifest.Version, s.opts.PID); err != nil {
		return nil, fmt.Errorf("write the install marker: %w", err)
	}
	started := s.opts.Now()
	staged, err := s.fetchBinary(ctx, rel)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(filepath.Dir(staged))
	if err := s.selfTest(ctx, staged, rel.Manifest.Version, rel.Manifest.SHA); err != nil {
		return nil, err
	}
	build := &Step{Success: true, Duration: s.opts.Now().Sub(started).Milliseconds(), Version: rel.Manifest.Version, Method: "release"}

	result := newResult(rel)
	result.ServerBuild = build
	if backup {
		dir, count, err := s.createBackup(ctx, paths, rel.Manifest.MinRollbackVersion)
		if err != nil {
			return nil, err
		}
		result.BackupPath, result.BackedUp = &dir, count
	}
	deploy, err := s.swapIn(paths, staged, rel.Asset.SHA256, pendingMarker{
		Kind: kind, From: s.opts.Version, FromCommit: s.opts.Commit, To: rel.Manifest.Version, ToCommit: rel.Manifest.SHA,
	})
	if err != nil {
		return nil, err
	}
	deploy.BackupPath = result.BackupPath
	result.ServerDeploy = deploy
	result.Applied = []string{Product + " " + rel.Manifest.Version}
	if !s.Supervised() {
		blocked := RestartBlocked
		result.ConsoleRestartBlocked = &blocked
	}
	return result, nil
}

func newResult(rel *Release) *InstallResult {
	return &InstallResult{
		Applied: []string{}, Failed: []Failure{}, Removed: []string{}, Skipped: []string{},
		ServicesRestarted: []string{}, ServicesFailed: []ServiceFailure{},
		NeedsConsoleRestart: true, NeedsServerRestart: true, SelectedComponents: []string{"server"},
		CriticalFailures: []Failure{}, NonCriticalFailures: []Failure{}, Signature: rel.Trust, Version: rel.Manifest.Version,
	}
}

// swapIn swaps the binary and writes the pending marker. When the marker
// cannot be written the old binary is put back: a swap without a marker
// would have no automatic rollback.
func (s *Service) swapIn(paths BinaryPaths, staged, sum string, marker pendingMarker) (*Deploy, error) {
	prevSum, err := fileSHA256(paths.Binary)
	if err != nil {
		return nil, fmt.Errorf("read the running binary: %w", err)
	}
	sw := s.swapper(paths)
	if err := sw.install(staged, sum); err != nil {
		return nil, err
	}
	marker.PrevSHA256, marker.DataDir, marker.At = prevSum, s.opts.DataDir, s.isoNow()
	if err := writeMarker(paths.Pending, marker); err != nil {
		if back := sw.restorePrev(false); back != nil {
			return nil, fmt.Errorf("write the pending marker: %w; and the previous binary could not be put back: %v", err, back)
		}
		return nil, fmt.Errorf("write the pending marker: %w", err)
	}
	s.files.clearStale()
	return &Deploy{Success: true, TargetPath: paths.Binary, Method: "release"}, nil
}

func (s *Service) swapper(paths BinaryPaths) *swapper {
	return &swapper{paths: paths, ops: s.fileOps(), renameAside: *s.opts.RenameAside, pid: s.opts.PID, now: s.opts.Now}
}

// fileOps are the file calls of a swap. A test replaces them.
func (s *Service) fileOps() fileOps {
	if s.ops.rename != nil {
		return s.ops
	}
	return osFileOps()
}

// fetchBinary downloads this platform's binary of the release into
// DATA_DIR/updates/<version>/<name> and checks its size and SHA-256
// against the manifest. Version and name passed the manifest's checks,
// and the path is checked once more to lie in the updates folder.
func (s *Service) fetchBinary(ctx context.Context, rel *Release) (string, error) {
	root := filepath.Join(s.opts.DataDir, updatesDir)
	dir := filepath.Join(root, rel.Manifest.Version)
	target := filepath.Join(dir, rel.Asset.Path)
	if filepath.Dir(dir) != root || filepath.Dir(target) != dir {
		return "", fmt.Errorf("the download path for %s is outside %s", rel.Asset.Path, root)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	part := target + ".part"
	f, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o700)
	if err != nil {
		return "", fmt.Errorf("create %s: %w", part, err)
	}
	h := sha256.New()
	file := releaseFile{owner: s.Owner(), repo: s.Repo(), tag: rel.Tag, name: rel.Asset.Path}
	n, err := s.gh.download(ctx, rel.AssetURL, file, rel.downloadLimit(), io.MultiWriter(f, h))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(part)
		return "", err
	}
	if rel.Asset.Size > 0 && n != rel.Asset.Size {
		_ = os.Remove(part)
		return "", fmt.Errorf("%s has %d bytes, the manifest says %d", rel.Asset.Path, n, rel.Asset.Size)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != rel.Asset.SHA256 {
		_ = os.Remove(part)
		return "", fmt.Errorf("%s does not have the SHA-256 the manifest names", rel.Asset.Path)
	}
	if err := os.Rename(part, target); err != nil {
		_ = os.Remove(part)
		return "", fmt.Errorf("keep %s: %w", target, err)
	}
	return target, nil
}

// selfTest runs the binary with -version and checks that it names the
// release's version and commit (a release build always knows its
// commit). It catches a binary for another processor or C library before
// the swap, and ties the unsigned version of the manifest to the signed
// binary.
func (s *Service) selfTest(ctx context.Context, binary, version, commit string) error {
	out, err := s.opts.RunVersion(ctx, binary)
	if err != nil {
		return fmt.Errorf("the new binary does not run here: %w", err)
	}
	gotVersion, gotCommit, ok := ParseVersionOutput(out)
	switch {
	case !ok:
		return fmt.Errorf("the new binary printed no version: %q", cut(out, 200))
	case gotVersion != version:
		return fmt.Errorf("the new binary is version %s, the release says %s", gotVersion, version)
	case gotCommit == "":
		return errors.New("the new binary names no commit")
	case !shaMatches(gotCommit, commit):
		return fmt.Errorf("the new binary was built from %s, the release says %s", gotCommit, prefix7(commit))
	}
	return nil
}

// runVersion runs "<binary> -version" with only PATH (and on Windows
// SystemRoot) in its environment, for at most 10 seconds, and returns
// the first 4 KiB it printed.
func runVersion(ctx context.Context, binary string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-version")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	if runtime.GOOS == "windows" {
		cmd.Env = append(cmd.Env, "SystemRoot="+os.Getenv("SystemRoot"))
	}
	cmd.Dir = filepath.Dir(binary)
	var out bytes.Buffer
	cmd.Stdout = &limitedBuffer{buf: &out, left: 4096}
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return "", errors.New("it did not answer within 10 seconds")
		}
		return "", err
	}
	return out.String(), nil
}

type limitedBuffer struct {
	buf  *bytes.Buffer
	left int
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if l.left > 0 {
		take := min(len(p), l.left)
		l.buf.Write(p[:take])
		l.left -= take
	}
	return len(p), nil
}

// RestoreResult is the answer of a restore.
type RestoreResult struct {
	Restored   int    `json:"restored"`
	Removed    int    `json:"removed"`
	Version    string `json:"version"`
	SHA        string `json:"sha"`
	TotalFiles int    `json:"totalFiles"`
	// ConsoleRestartBlocked is there when no supervisor starts the
	// process again.
	ConsoleRestartBlocked *string `json:"consoleRestartBlocked,omitempty"`
}

// Restore puts the binary of a backup this updater made back in place,
// with the same swap and pending marker as an install. A backup of the
// old console is refused (DECISIONS A29), and so is one whose version is
// older than the release installed over it allows, or whose binary was
// changed since.
func (s *Service) Restore(ctx context.Context, name string) (*RestoreResult, error) {
	if s.ImageMode() {
		return nil, &BackupError{Message: "In-app restore is disabled in Docker image deployments. Run: " + s.DockerHint()}
	}
	dir, err := s.backupDir(name)
	if err != nil {
		return nil, err
	}
	m, err := readBackupManifest(dir)
	if err != nil {
		return nil, err
	}
	if m.MinRollbackVersion != "" && CompareVersions(m.Version, m.MinRollbackVersion) < 0 {
		return nil, &BackupError{Message: "This backup holds version " + m.Version + ", older than the installed release allows (" + m.MinRollbackVersion + ")"}
	}
	if !s.busy.CompareAndSwap(false, true) {
		return nil, ErrBusy
	}
	defer s.busy.Store(false)
	paths, err := ResolveBinaryPaths(s.opts.Executable)
	if err != nil {
		return nil, err
	}
	binary := filepath.Join(dir, "server", filepath.Base(m.Files[0]))
	if filepath.Dir(binary) != filepath.Join(dir, "server") || filepath.Base(binary) != filepath.Base(paths.Binary) {
		return nil, &BackupError{Message: "Invalid backup — it holds another binary than the running one"}
	}
	if info, err := os.Lstat(binary); err != nil || !info.Mode().IsRegular() {
		return nil, &BackupError{Message: "Invalid backup — the binary is missing"}
	}
	if sum, err := fileSHA256(binary); err != nil || sum != m.BinarySHA256 {
		return nil, &BackupError{Message: "Invalid backup — the binary was changed since the backup"}
	}
	if err := s.selfTestVersion(ctx, binary, m.Version, m.SHA); err != nil {
		return nil, err
	}
	if _, err := s.swapIn(paths, binary, m.BinarySHA256, pendingMarker{
		Kind: "restore", From: s.opts.Version, FromCommit: s.opts.Commit, To: m.Version, ToCommit: m.SHA,
	}); err != nil {
		return nil, err
	}
	out := &RestoreResult{Restored: 1, Version: m.Version, SHA: m.SHA, TotalFiles: len(m.Files)}
	if !s.Supervised() {
		blocked := RestartBlocked
		out.ConsoleRestartBlocked = &blocked
	}
	return out, nil
}

// selfTestVersion is selfTest for a backup, whose binary may name no
// commit (a build without one).
func (s *Service) selfTestVersion(ctx context.Context, binary, version, commit string) error {
	out, err := s.opts.RunVersion(ctx, binary)
	if err != nil {
		return fmt.Errorf("the backed up binary does not run here: %w", err)
	}
	gotVersion, gotCommit, ok := ParseVersionOutput(out)
	if !ok || gotVersion != version || (gotCommit != "" && commit != "" && !shaMatches(gotCommit, commit)) {
		return errors.New("the backed up binary is not the version its backup names")
	}
	return nil
}

// Rollback puts .prev back in place of the binary and keeps the replaced
// binary as .prev (the rollback subcommand). A pending marker is removed.
func (s *Service) Rollback() (string, error) {
	paths, err := ResolveBinaryPaths(s.opts.Executable)
	if err != nil {
		return "", err
	}
	if err := s.swapper(paths).restorePrev(true); err != nil {
		return "", err
	}
	_ = os.Remove(paths.Pending)
	return paths.Binary, nil
}

// RecordInstall keeps the outcome of an install for the panel: when the
// process does not end by itself, the result with the reason, so the
// panel keeps showing it; else no result, and the new binary records its
// own once it is ready.
func (s *Service) RecordInstall(sha string, r *InstallResult) error {
	if r.ConsoleRestartBlocked == nil {
		s.files.clearResult()
		return nil
	}
	return s.files.persistResult(LastResult{
		SHA: sha, Applied: len(r.Applied), Failed: []Failure{}, ServicesFailed: []ServiceFailure{},
		ServicesRestarted: []string{}, ConsoleRestartBlocked: r.ConsoleRestartBlocked, ServerDeploy: r.ServerDeploy,
	})
}

// IsNodeBackup reports the refusal to restore a backup of the old console.
func IsNodeBackup(err error) bool { return errors.Is(err, errNodeBackup) }

// RepairOutcome is the answer of the rebuild route: whether the release of
// the running version was installed again, and its steps.
type RepairOutcome struct {
	Success bool        `json:"success"`
	Error   string      `json:"error,omitempty"`
	Steps   RepairSteps `json:"steps"`
}

// RepairSteps are the steps of a repair.
type RepairSteps struct {
	Download *Step    `json:"download,omitempty"`
	Deploy   *Deploy  `json:"deploy,omitempty"`
	Restart  *Restart `json:"restart,omitempty"`
}

// Restart says whether the process ends to start the new binary.
type Restart struct {
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// RepairResult runs Repair and words its outcome for the route.
func (s *Service) RepairResult(ctx context.Context) RepairOutcome {
	result, err := s.Repair(ctx)
	if err != nil {
		return RepairOutcome{Error: err.Error()}
	}
	restart := &Restart{Success: true}
	if result.ConsoleRestartBlocked != nil {
		restart = &Restart{Error: *result.ConsoleRestartBlocked}
	}
	return RepairOutcome{Success: true, Steps: RepairSteps{Download: result.ServerBuild, Deploy: result.ServerDeploy, Restart: restart}}
}
