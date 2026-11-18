package updater

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCheckFindsTheRelease(t *testing.T) {
	in := newInstall(t)
	in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
	got, err := in.svc.Check(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.UpdateAvailable || got.RemoteSHA != newCommit || *got.LocalSHA != oldCommit || got.LocalVersion != "3.5.0" ||
		got.LatestVersion != "3.6.0" || got.ReleaseTag != "v3.6.0" || got.CommitsBehind != 3 || got.LatestMessage != "BetterDesk 3.6.0" ||
		got.LatestDate != "2026-11-18T10:00:00Z" || got.LatestAuthor != "unitronix" || got.DeploymentMode != "native" ||
		got.UpdateChannel != "stable" || got.GithubBranch != "main" || got.GithubOwner != "UNITRONIX" || got.GithubRepo != "BetterDesk" {
		t.Errorf("check %+v", got)
	}
	// docs/ is not counted; the renamed web app file and the installer
	// script are.
	want := `{"console":{"changed":true,"fileCount":1,"label":"Web Console","autoUpdate":true},` +
		`"scripts":{"changed":true,"fileCount":1,"label":"Scripts & Docker","autoUpdate":true},` +
		`"server":{"changed":true,"fileCount":1,"label":"Go Server","autoUpdate":true},` +
		`"webui":{"changed":true,"fileCount":1,"label":"Web Console (web-ui)","autoUpdate":true}}`
	if components := mustJSON(t, got.Components); components != want {
		t.Errorf("components %s", components)
	}
	if json := mustJSON(t, got); strings.Contains(json, "dockerImageMode") || strings.Contains(json, "baselineEstablished") || strings.Contains(json, "compareError") {
		t.Errorf("unexpected fields: %s", json)
	}
	// A second check within the cache time asks GitHub nothing.
	before := in.gh.count("GET https://api.github.com")
	if _, err := in.svc.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	if after := in.gh.count("GET https://api.github.com"); after != before {
		t.Errorf("the cache did not hold: %d API calls, then %d", before, after)
	}
}

func TestCheckCases(t *testing.T) {
	t.Run("up to date", func(t *testing.T) {
		in := newInstall(t, func(o *Options) { o.Version, o.Commit = "3.6.0", newCommit })
		in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
		got, err := in.svc.Check(context.Background())
		if err != nil || got.UpdateAvailable || got.CommitsBehind != 0 || len(got.Components) != 0 {
			t.Errorf("%+v %v", got, err)
		}
	})
	t.Run("older release", func(t *testing.T) {
		in := newInstall(t, func(o *Options) { o.Version = "3.7.0" })
		in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
		if got, err := in.svc.Check(context.Background()); err != nil || got.UpdateAvailable {
			t.Errorf("a downgrade is offered: %+v %v", got, err)
		}
	})
	t.Run("compare fails", func(t *testing.T) {
		in := newInstall(t)
		rel := newTestRelease("3.6.0", newCommit, BranchStable)
		in.gh.serve(rel)
		in.gh.on("GET https://api.github.com/repos/UNITRONIX/BetterDesk/compare/"+oldCommit+"..."+newCommit, func(*http.Request) *http.Response {
			return respond(404, "No common ancestor", nil)
		})
		got, err := in.svc.Check(context.Background())
		if err != nil || !got.UpdateAvailable || got.CommitsBehind != -1 || got.CompareError != "GitHub API 404: No common ancestor" {
			t.Errorf("%+v %v", got, err)
		}
	})
	t.Run("a build without a commit", func(t *testing.T) {
		in := newInstall(t, func(o *Options) { o.Version, o.Commit = "dev", "" })
		in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
		got, err := in.svc.Check(context.Background())
		if err != nil || got.UpdateAvailable || got.CommitsBehind != 0 || got.LocalSHA != nil || got.CompareError == "" {
			t.Errorf("%+v %v", got, err)
		}
	})
	t.Run("development channel", func(t *testing.T) {
		in := newInstall(t)
		in.env["UPDATE_GITHUB_BRANCH"] = "dev"
		in.gh.serve(newTestRelease("3.6.0", newCommit, BranchDevelopment))
		got, err := in.svc.Check(context.Background())
		if err != nil || got.ReleaseTag != "dev" || got.UpdateChannel != "development" || !got.UpdateAvailable {
			t.Errorf("%+v %v", got, err)
		}
	})
	t.Run("docker image", func(t *testing.T) {
		in := newInstall(t, func(o *Options) { o.Docker = true; o.Commit = "" })
		in.env["BETTERDESK_DOCKER_LAYOUT"] = "single"
		in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
		got, err := in.svc.Check(context.Background())
		if err != nil || !got.DockerShaUnknown || !got.DockerImageMode || got.DeploymentMode != "docker-image" ||
			got.DockerUpdate == nil || got.DockerUpdate.Images[0] != "ghcr.io/unitronix/betterdesk:latest" || *got.InAppUpdateSupported {
			t.Errorf("%+v %v", got, err)
		}
	})
	t.Run("rate limit", func(t *testing.T) {
		in := newInstall(t)
		in.env["UPDATE_GITHUB_TOKEN"] = "x"
		in.svc = New(in.opts)
		in.gh.on("GET https://api.github.com/repos/UNITRONIX/BetterDesk/releases/latest", func(*http.Request) *http.Response {
			return respond(429, "slow down", http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {"1795000000"}})
		})
		_, err := in.svc.Check(context.Background())
		if !IsRateLimited(err) || err.Error() != "GitHub API rate limit exceeded for the configured token. Resets at 2026-11-18T11:06:40.000Z." {
			t.Errorf("error %v", err)
		}
	})
}

func TestChanges(t *testing.T) {
	in := newInstall(t)
	in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
	got, err := in.svc.Changes(context.Background(), newCommit[:7])
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalFiles != 4 || got.CommitsBehind != 3 || got.CompareTruncated {
		t.Errorf("%+v", got)
	}
	if s := mustJSON(t, got.Commits); s != `[{"sha":"2222222","message":"last","date":"d2","author":"b"},{"sha":"1111111","message":"first","date":"d1","author":"a"}]` {
		t.Errorf("commits %s", s)
	}
	if s := mustJSON(t, got.Grouped.WebUI); s != `[{"path":"web-ui/src/app.ts","status":"renamed","sha":"","component":"webui","localPath":"src/app.ts","previousPath":"src/old.ts"}]` {
		t.Errorf("webui %s", s)
	}
	if s := mustJSON(t, got.Grouped.Console); s != `[{"path":"web-nodejs/server.js","status":"removed","sha":"","component":"console","localPath":"server.js"}]` {
		t.Errorf("console %s", s)
	}
	if s := mustJSON(t, got.Grouped.Scripts); s != `[{"path":"betterdesk.sh","status":"modified","sha":"","component":"scripts","localPath":"betterdesk.sh"}]` {
		t.Errorf("scripts %s", s)
	}
	if _, err := in.svc.Changes(context.Background(), "abcdef1"); err == nil {
		t.Error("a commit that is not the release's was compared")
	}
}

// The old updater's path rules, by the cases its tests name.
func TestClassifyAndExclude(t *testing.T) {
	for path, want := range map[string]string{
		"web-nodejs/server.js": "console", "web-ui/src/a.ts": "webui", "betterdesk-server/main.go": "server",
		"betterdesk-agent/x": "agent", "docker/entrypoint.sh": "scripts", "Dockerfile": "scripts", "README.md": "other",
	} {
		if got := classifyFile(path); got != want {
			t.Errorf("classifyFile(%q) = %q", path, got)
		}
	}
	for path, want := range map[string]bool{
		".github/x": true, "docs/a.md": true, "web-nodejs/data/auth.db": true, "betterdesk-server/data/x": true,
		"x.sqlite3-wal": true, "web-nodejs/.env": true, "web-nodejs/.env.example": true, "betterdesk-server/betterdesk-server": true,
		"web-ui/node_modules/x": true, "web-ui/build/x": true, "web-ui/tests/a.ts": true, "web-ui/src/lib/data/x.ts": false,
		"web-ui/../x": true, "web-nodejs/server.js": false, "a.exe": true,
	} {
		if got := isExcluded(path); got != want {
			t.Errorf("isExcluded(%q) = %v", path, got)
		}
	}
}

func TestChannel(t *testing.T) {
	in := newInstall(t)
	in.opts.EnvFile = filepath.Join(t.TempDir(), "console.env")
	in.svc = New(in.opts)
	if err := os.WriteFile(in.opts.EnvFile, []byte("PORT=5000\r\nUPDATE_GITHUB_BRANCH=main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := mustJSON(t, in.svc.Channel()); got != `{"channel":"stable","branch":"main","label":"Stable","owner":"UNITRONIX","repo":"BetterDesk"}` {
		t.Errorf("channel %s", got)
	}
	switched, err := in.svc.SetChannel("development")
	if err != nil {
		t.Fatal(err)
	}
	if got := mustJSON(t, switched); got != `{"channel":"development","branch":"dev","label":"Development","previousBranch":"main","previousChannel":"stable"}` {
		t.Errorf("switched %s", got)
	}
	data, _ := os.ReadFile(in.opts.EnvFile)
	if string(data) != "PORT=5000\nUPDATE_GITHUB_BRANCH=dev\n" {
		t.Errorf("env file %q", data)
	}
	// A file the switch creates is private; an existing one keeps its mode,
	// as with the old console's writeFileSync.
	fresh := newInstall(t)
	fresh.opts.EnvFile = filepath.Join(t.TempDir(), "new.env")
	fresh.svc = New(fresh.opts)
	if _, err := fresh.svc.SetChannel("stable"); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(fresh.opts.EnvFile); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("mode %o", info.Mode().Perm())
	}
	if in.svc.Branch() != "dev" {
		t.Error("the process did not switch")
	}
	if _, err := in.svc.SetChannel("nightly"); err == nil {
		t.Error("an unknown channel was accepted")
	}
	previous, err := in.svc.SetBranchUndefined()
	if err != nil || previous != "dev" || in.svc.Branch() != "undefined" {
		t.Errorf("undefined: %q %v %q", previous, err, in.svc.Branch())
	}
	docker := newInstall(t, func(o *Options) { o.Docker = true })
	if _, err := docker.svc.SetChannel("development"); err == nil || !strings.Contains(err.Error(), "BETTERDESK_IMAGE_TAG=dev") {
		t.Errorf("docker: %v", err)
	}
}
