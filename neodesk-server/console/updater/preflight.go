package updater

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/managedconfig"
)

// Check is one capability check (getCapabilityReport).
type Check struct {
	ID        string `json:"id"`
	Path      string `json:"path,omitempty"`
	Ready     bool   `json:"ready"`
	Exists    *bool  `json:"exists,omitempty"`
	Directory *bool  `json:"directory,omitempty"`
	Method    string `json:"method,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Group is a group of checks.
type Group struct {
	Ready  bool    `json:"ready"`
	Checks []Check `json:"checks"`
}

// Groups are the groups of the report, in the old console's order.
type Groups struct {
	Update      Group `json:"update"`
	Config      Group `json:"config"`
	Restart     Group `json:"restart"`
	Permissions Group `json:"permissions"`
	Backup      Group `json:"backup"`
	Health      Group `json:"health"`
}

// Capabilities is the report of what this install can do from the panel
// (the management capabilities route and the preflight).
type Capabilities struct {
	Success  bool                `json:"success"`
	Platform string              `json:"platform"`
	Docker   bool                `json:"docker"`
	Paths    managedconfig.Paths `json:"paths"`
	Services []string            `json:"services"`
	Groups   Groups              `json:"groups"`
	Checks   []Check             `json:"checks"`
}

// nodePlatform is process.platform for a Go platform name.
func nodePlatform(goos string) string {
	if goos == "windows" {
		return "win32"
	}
	return goos
}

// Capabilities reports the folders the console can read and write, how
// the process is restarted, whether updates can be installed and whether
// configuration can be saved. The checks keep the old console's ids and
// order. What changed (DECISIONS A29, spec 4.6): one service, the restart
// is the process's exit under a supervisor (method "exit"), and an update
// needs the binary's folder to be writable (method "release").
func (s *Service) Capabilities() Capabilities {
	paths := s.managed.Paths()
	checks := []Check{
		pathCheck(paths.ConsolePath, false, "console-read"),
		pathCheck(paths.ConsolePath, true, "console-write"),
		pathCheck(paths.DataPath, true, "data-write"),
		pathCheck(paths.ServerPath, false, "server-read"),
		pathCheck(paths.ServerPath, true, "server-write"),
		pathCheck(paths.BackupPath, true, "backup-write"),
		pathCheck(paths.EnvPath, true, "env-write"),
		s.restartCheck(),
		{ID: "config-schema", Ready: s.managed.HasSchema()},
		s.updateCheck(),
	}
	pick := func(ids ...string) Group {
		g := Group{Checks: []Check{}}
		for _, c := range checks {
			for _, id := range ids {
				if c.ID == id {
					g.Checks = append(g.Checks, c)
				}
			}
		}
		g.Ready = len(g.Checks) > 0
		for _, c := range g.Checks {
			g.Ready = g.Ready && c.Ready
		}
		return g
	}
	service := "neodesk-server"
	switch {
	case s.opts.Docker:
		service = "neodesk"
	case s.opts.GOOS == "windows":
		service = "NeoDeskServer"
	}
	return Capabilities{
		Success: true, Platform: nodePlatform(s.opts.GOOS), Docker: s.opts.Docker, Paths: paths,
		Services: []string{service},
		Groups: Groups{
			Update:      pick("console-read", "console-write", "data-write", "server-read", "server-write", "update"),
			Config:      pick("env-write", "config-schema", "console-write"),
			Restart:     pick("restart"),
			Permissions: pick("server-write", "data-write", "env-write"),
			Backup:      pick("backup-write"),
			Health:      Group{Ready: true, Checks: []Check{{ID: "health", Ready: true, Method: "post-operation-health-check"}}},
		},
		Checks: checks,
	}
}

func (s *Service) restartCheck() Check {
	if s.Supervised() {
		return Check{ID: "restart", Ready: true, Method: "exit"}
	}
	return Check{ID: "restart", Method: "unavailable", Error: "No supervisor is configured (NEODESK_SUPERVISOR)"}
}

func (s *Service) updateCheck() Check {
	if s.ImageMode() {
		return Check{ID: "update", Method: "image", Error: "Docker image deployments update by pulling a new image"}
	}
	paths, err := ResolveBinaryPaths(s.opts.Executable)
	if err != nil {
		return Check{ID: "update", Method: "release", Error: err.Error()}
	}
	if err := writable(paths.Dir); err != nil {
		return Check{ID: "update", Method: "release", Error: "The folder of the server binary is not writable: " + paths.Dir}
	}
	return Check{ID: "update", Ready: true, Method: "release"}
}

// pathCheck is the old console's pathCheck: a path that exists is checked
// itself, one that does not exist through its folder.
func pathCheck(path string, write bool, id string) Check {
	c := Check{ID: id, Path: path}
	info, statErr := os.Stat(path)
	target := path
	if statErr != nil {
		target = filepath.Dir(path)
	}
	var err error
	if write {
		err = writable(target)
	} else {
		err = readable(target)
	}
	if err != nil {
		c.Error = errorCode(err)
		return c
	}
	present, dir := statErr == nil, statErr == nil && info.IsDir()
	c.Ready, c.Exists, c.Directory = true, &present, &dir
	return c
}

// errorCode is err.code of a Node file error.
func errorCode(err error) string {
	switch {
	case os.IsNotExist(err):
		return "ENOENT"
	case os.IsPermission(err):
		return "EACCES"
	}
	return err.Error()
}

// Disk is the free space check (checkUpdateDiskSpace).
type Disk struct {
	AvailableBytes   *int64 `json:"availableBytes"`
	MinimumFreeBytes int64  `json:"minimumFreeBytes"`
	Path             string `json:"path"`
	Supported        bool   `json:"supported"`
	Sufficient       *bool  `json:"sufficient"`
}

// disk checks the free space at path against max(64 MiB,
// UPDATE_MIN_FREE_MB or 512 MiB).
func (s *Service) disk(path string) Disk {
	mb := 512
	if n, ok := parseLeadingInt(s.getenv("UPDATE_MIN_FREE_MB")); ok && n != 0 {
		mb = n
	}
	minimum := max(int64(64)<<20, int64(mb)<<20)
	d := Disk{MinimumFreeBytes: minimum, Path: path}
	free, err := s.opts.FreeSpace(path)
	if err != nil {
		return d
	}
	enough := free >= minimum
	d.Supported, d.AvailableBytes, d.Sufficient = true, &free, &enough
	return d
}

// GoFacts are the toolchain facts the preflight had (go); a release
// install needs no toolchain.
type GoFacts struct {
	Available     bool    `json:"available"`
	Version       string  `json:"version"`
	VersionNumber string  `json:"versionNumber"`
	BinPath       *string `json:"binPath"`
	Source        string  `json:"source"`
	MeetsMinimum  bool    `json:"meetsMinimum"`
	NeedsUpgrade  bool    `json:"needsUpgrade"`
}

// Preflight is the answer of the preflight route.
type Preflight struct {
	Ready           bool          `json:"ready"`
	Issues          []string      `json:"issues"`
	Warnings        []string      `json:"warnings"`
	Capabilities    *Capabilities `json:"capabilities"`
	Go              GoFacts       `json:"go"`
	CanBuildServer  bool          `json:"canBuildServer"`
	Disk            *Disk         `json:"disk,omitempty"`
	BinaryDisk      *Disk         `json:"binaryDisk,omitempty"`
	RemoteSHA       *string       `json:"remoteSHA"`
	Release         *ReleaseFacts `json:"release,omitempty"`
	Supervisor      *string       `json:"supervisor"`
	DockerImageMode bool          `json:"dockerImageMode,omitempty"`
	DockerUpdate    *DockerUpdate `json:"dockerUpdate,omitempty"`
}

// ReleaseFacts name the release the preflight looked at.
type ReleaseFacts struct {
	Tag       string `json:"tag"`
	Version   string `json:"version"`
	SHA       string `json:"sha"`
	Asset     string `json:"asset"`
	Signature Trust  `json:"signature"`
}

func goFacts() GoFacts {
	v := runtime.Version()
	return GoFacts{Available: true, Version: v, VersionNumber: strings.TrimPrefix(v, "go"), Source: "path", MeetsMinimum: true}
}

// Preflight checks, before an install, what it needs (spec 4.6): the data
// folder and the binary's folder writable with enough free space, the
// release reachable with a binary for this platform and a signature as
// required. Issues block the install; warnings do not. remoteSHA is
// passed through as the old console did.
func (s *Service) Preflight(ctx context.Context, remoteSHA string) Preflight {
	out := Preflight{Issues: []string{}, Warnings: []string{}, Go: goFacts(), RemoteSHA: nullable(remoteSHA), Supervisor: nullable(Supervisor(s.getenv))}
	if s.ImageMode() {
		docker := s.DockerInstructions()
		out.Issues = append(out.Issues, `Docker image deployment — use "`+s.DockerHint()+`" instead of in-app install`)
		out.DockerImageMode, out.DockerUpdate = true, &docker
		out.Capabilities = nil
		return out
	}
	out.CanBuildServer = true
	if err := os.MkdirAll(s.opts.DataDir, 0o755); err != nil || writable(s.opts.DataDir) != nil {
		out.Issues = append(out.Issues, "Console data directory is not writable: "+s.opts.DataDir)
	}
	disk := s.disk(s.opts.DataDir)
	out.Disk = &disk
	s.diskIssue(&out, disk)

	paths, err := ResolveBinaryPaths(s.opts.Executable)
	if err != nil {
		out.Issues = append(out.Issues, "The server binary cannot be found: "+err.Error())
	} else {
		if writable(paths.Dir) != nil {
			out.Issues = append(out.Issues, "The folder of the server binary is not writable: "+paths.Dir+
				" — run `neodesk-server update` as its owner, or let the service account write there")
		}
		if paths.Dir != s.opts.DataDir {
			bd := s.disk(paths.Dir)
			out.BinaryDisk = &bd
			s.diskIssue(&out, bd)
		}
	}
	if !s.Supervised() {
		out.Warnings = append(out.Warnings, "No supervisor is configured (NEODESK_SUPERVISOR): after the update the server has to be started again by hand")
	}

	rel, err := s.lookupRelease(ctx, "", false)
	if err != nil {
		out.Issues = append(out.Issues, "Cannot reach GitHub API: "+err.Error())
	} else {
		facts := &ReleaseFacts{Tag: rel.Tag, Version: rel.Manifest.Version, SHA: rel.Manifest.SHA, Signature: rel.Trust}
		if rel.Asset != nil {
			facts.Asset = rel.Asset.Path
		} else {
			out.Issues = append(out.Issues, fmt.Sprintf("Release %s has no binary for %s/%s", rel.Tag, s.opts.GOOS, s.opts.GOARCH))
		}
		switch {
		case !rel.Trust.OK:
			out.Issues = append(out.Issues, rel.Trust.Reason)
		case !rel.Trust.Verified:
			out.Warnings = append(out.Warnings, rel.Trust.Reason)
		}
		if CompareVersions(rel.Manifest.Version, s.opts.Version) < 0 {
			out.Issues = append(out.Issues, "Release "+rel.Manifest.Version+" is older than the running version "+s.opts.Version)
		}
		if min := rel.Manifest.MinRollbackVersion; min != "" && CompareVersions(s.opts.Version, min) < 0 {
			out.Warnings = append(out.Warnings, "After this update, going back to "+s.opts.Version+" is not supported: release "+
				rel.Manifest.Version+" can be rolled back to "+min+" or newer only")
		}
		out.Release = facts
	}
	capabilities := s.Capabilities()
	out.Capabilities = &capabilities
	out.Ready = len(out.Issues) == 0
	return out
}

func (s *Service) diskIssue(out *Preflight, d Disk) {
	switch {
	case d.Sufficient != nil && !*d.Sufficient:
		out.Issues = append(out.Issues, "Insufficient free disk space under "+d.Path+": "+
			strconv.FormatInt(*d.AvailableBytes>>20, 10)+" MiB available, "+strconv.FormatInt(d.MinimumFreeBytes>>20, 10)+" MiB required")
	case !d.Supported:
		out.Warnings = append(out.Warnings, "Free disk-space check is unavailable on this platform")
	}
}
