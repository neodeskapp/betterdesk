package updater

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func runCLI(in *install, input string, args ...string) (int, string) {
	var out bytes.Buffer
	code := CLI{Service: in.svc, In: strings.NewReader(input), Out: &out}.Update(context.Background(), args)
	return code, out.String()
}

func TestUpdateCommand(t *testing.T) {
	in := newInstall(t)
	in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))

	code, out := runCLI(in, "", "--check")
	if code != 0 || !strings.Contains(out, "Release: betterdesk-server 3.6.0 (2222222), tag v3.6.0") || !strings.Contains(out, "Check complete") {
		t.Errorf("check: %d\n%s", code, out)
	}
	in.assertUntouched()

	if code, out = runCLI(in, "n\n"); code != 0 || !strings.Contains(out, "Update cancelled.") {
		t.Errorf("cancel: %d\n%s", code, out)
	}
	in.assertUntouched()

	if code, _ = runCLI(in, "", "--force"); code != 2 {
		t.Errorf("unknown argument: %d", code)
	}

	code, out = runCLI(in, "yes\n", "--no-backup")
	if code != 0 || !strings.Contains(out, "Installed: betterdesk-server 3.6.0") || !strings.Contains(out, "Backup: not created") {
		t.Fatalf("install: %d\n%s", code, out)
	}
	if in.binaryContent() != string(binaryFor("3.6.0", newCommit)) {
		t.Error("not installed")
	}
}

func TestUpdateCommandFailures(t *testing.T) {
	in := newInstall(t)
	if code, out := runCLI(in, "", "--yes"); code != 1 || !strings.Contains(out, "Update failed: GitHub API 404") {
		t.Errorf("no release: %d\n%s", code, out)
	}
	up := newInstall(t, func(o *Options) { o.Version, o.Commit = "3.6.0", newCommit })
	up.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
	if code, out := runCLI(up, "", "--yes"); code != 0 || !strings.Contains(out, "Already up to date (2222222).") {
		t.Errorf("current: %d\n%s", code, out)
	}
	bad := newInstall(t)
	rel := newTestRelease("3.6.0", newCommit, BranchStable)
	rel.binaries[asset] = []byte("tampered")
	bad.gh.serve(rel)
	if code, out := runCLI(bad, "", "-y"); code != 1 || !strings.Contains(out, "Nothing was changed.") {
		t.Errorf("tampered: %d\n%s", code, out)
	}
	bad.assertUntouched()
}

func TestRollbackCommandOutput(t *testing.T) {
	in := newInstall(t)
	var out bytes.Buffer
	if code := (CLI{Service: in.svc, Out: &out}).Rollback(nil); code != 1 || !strings.Contains(out.String(), "Rollback failed") {
		t.Errorf("no prev: %d %s", code, out.String())
	}
	in.gh.serve(newTestRelease("3.6.0", newCommit, BranchStable))
	installed(t, in, newCommit)
	out.Reset()
	if code := (CLI{Service: in.svc, Out: &out}).Rollback(nil); code != 0 || !strings.Contains(out.String(), "Put the previous binary back") {
		t.Errorf("rollback: %d %s", code, out.String())
	}
	if code := (CLI{Service: in.svc, Out: &out}).Rollback([]string{"--x"}); code != 2 {
		t.Errorf("argument: %d", code)
	}
}
