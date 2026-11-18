package updater

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

func TestPreflight(t *testing.T) {
	in := newInstall(t)
	in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
	pf := in.svc.Preflight(context.Background(), "abc1234")
	if !pf.Ready || len(pf.Issues) != 0 || pf.RemoteSHA == nil || *pf.RemoteSHA != "abc1234" || pf.Release == nil || pf.Release.Asset != asset {
		t.Fatalf("%+v", pf)
	}
	// Unsigned and unsupervised: two warnings, no issue.
	if len(pf.Warnings) != 2 || !strings.Contains(strings.Join(pf.Warnings, "|"), "BETTERDESK_SUPERVISOR") ||
		!strings.Contains(strings.Join(pf.Warnings, "|"), "was not signed") {
		t.Errorf("warnings %v", pf.Warnings)
	}

	in.opts.FreeSpace = func(string) (int64, error) { return 10 << 20, nil }
	in.env["BETTERDESK_SUPERVISOR"] = "systemd"
	in.env[EnvPublicKey] = base64.StdEncoding.EncodeToString(ed25519.NewKeyFromSeed(bytes.Repeat([]byte{4}, 32)).Public().(ed25519.PublicKey))
	in.svc = New(in.opts)
	pf = in.svc.Preflight(context.Background(), "")
	issues := strings.Join(pf.Issues, "|")
	if pf.Ready || !strings.Contains(issues, "Insufficient free disk space") || !strings.Contains(issues, "carries no manifest.sig") {
		t.Errorf("issues %v", pf.Issues)
	}
	if pf.RemoteSHA != nil || pf.Supervisor == nil || *pf.Supervisor != "systemd" {
		t.Errorf("%+v", pf)
	}

	if err := os.Chmod(in.dir, 0o555); err == nil && os.Geteuid() != 0 {
		defer os.Chmod(in.dir, 0o755)
		in.svc = New(in.opts)
		if pf := in.svc.Preflight(context.Background(), ""); !strings.Contains(strings.Join(pf.Issues, "|"), "folder of the server binary is not writable") {
			t.Errorf("read-only binary folder: %v", pf.Issues)
		}
	}

	docker := newInstall(t, func(o *Options) { o.Docker = true })
	if pf := docker.svc.Preflight(context.Background(), ""); pf.Ready || !pf.DockerImageMode || pf.DockerUpdate == nil {
		t.Errorf("docker %+v", pf)
	}
}

// The report keeps the old ids and order; the restart and the update
// checks follow DECISIONS A29.
func TestCapabilities(t *testing.T) {
	in := newInstall(t)
	c := in.svc.Capabilities()
	var ids []string
	for _, check := range c.Checks {
		ids = append(ids, check.ID)
	}
	if got := strings.Join(ids, ","); got != "console-read,console-write,data-write,server-read,server-write,backup-write,env-write,restart,config-schema,update" {
		t.Errorf("checks %s", got)
	}
	if c.Platform != "linux" || len(c.Services) != 1 || c.Services[0] != "betterdesk-server" {
		t.Errorf("%+v", c)
	}
	restart := c.Checks[7]
	if restart.Ready || restart.Method != "unavailable" || !strings.Contains(restart.Error, "BETTERDESK_SUPERVISOR") || c.Groups.Restart.Ready {
		t.Errorf("restart %+v", restart)
	}
	update := c.Checks[9]
	if !update.Ready || update.Method != "release" {
		t.Errorf("update %+v", update)
	}
	in.env["BETTERDESK_SUPERVISOR"] = "nssm"
	if c := in.svc.Capabilities(); !c.Checks[7].Ready || c.Checks[7].Method != "exit" || !c.Groups.Restart.Ready {
		t.Errorf("supervised %+v", c.Checks[7])
	}
	s := mustJSON(t, c.Groups.Health)
	if s != `{"ready":true,"checks":[{"id":"health","ready":true,"method":"post-operation-health-check"}]}` {
		t.Errorf("health %s", s)
	}
	if s := mustJSON(t, c.Checks[8]); s != `{"id":"config-schema","ready":true}` {
		t.Errorf("schema %s", s)
	}
	windows := newInstall(t, func(o *Options) { o.GOOS = "windows" })
	if c := windows.svc.Capabilities(); c.Platform != "win32" || c.Services[0] != "BetterDeskServer" {
		t.Errorf("windows %+v", c)
	}
}

func TestSupervisorValues(t *testing.T) {
	for value, want := range map[string]string{"": "", "0": "", "false": "", "none": "", "OFF": "", " systemd ": "systemd", "docker": "docker"} {
		if got := Supervisor(func(string) string { return value }); got != want {
			t.Errorf("%q: %q", value, got)
		}
	}
}
