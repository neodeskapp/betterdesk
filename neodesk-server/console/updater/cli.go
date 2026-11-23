package updater

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

// CLI runs the update and rollback subcommands with the code of the
// routes. It prints to Out and reads answers from In.
type CLI struct {
	Service *Service
	In      io.Reader
	Out     io.Writer
}

func (c CLI) say(format string, args ...any) { fmt.Fprintf(c.Out, format+"\n", args...) }

// reYes is an answer that means yes.
var reYes = regexp.MustCompile(`(?i)^y(es)?$`)

// Update is "neodesk-server update [--check] [--yes] [--no-backup]"
// (scripts/update-cli.js): it checks the release of the channel, shows
// what changed, asks (unless --yes, -y or --auto), and installs it into
// the binary that runs the command. --check (or --dry-run) stops before
// the install, --no-backup skips the backup. The service is not
// restarted: the command says how. The exit status is 0 when the binary
// is current or was installed, 1 on a failure and 2 for an unknown
// argument.
func (c CLI) Update(ctx context.Context, args []string) int {
	var yes, check, backup = false, false, true
	for _, arg := range args {
		switch arg {
		case "--yes", "-y", "--auto":
			yes = true
		case "--check", "--dry-run":
			check = true
		case "--no-backup":
			backup = false
		default:
			c.say("Unknown argument %q. Use: neodesk-server update [--check] [--yes] [--no-backup]", arg)
			return 2
		}
	}
	s := c.Service
	c.say("NeoDesk updater")
	if s.ImageMode() {
		c.say("This is a Docker image install. Update it with: %s", s.DockerHint())
		return 1
	}
	paths, err := ResolveBinaryPaths(s.opts.Executable)
	if err != nil {
		c.say("Update failed: %v", err)
		return 1
	}
	c.say("[1/4] Checking the newest release of the %s channel (%s/%s)", ChannelOf(s.Branch()), s.Owner(), s.Repo())
	result, err := s.Check(ctx)
	if err != nil {
		c.say("Update failed: %v", err)
		return 1
	}
	c.say("Running: %s %s (%s)", Product, s.opts.Version, orUnknown(s.opts.Commit))
	c.say("Release: %s %s (%s), tag %s", Product, result.LatestVersion, prefix7(result.RemoteSHA), result.ReleaseTag)
	if result.note != "" {
		c.say("Note: %s", result.note)
	}
	switch {
	case result.Signature.Verified:
		c.say("Signature: verified")
	case result.Signature.OK:
		c.say("Signature: not verified. %s", result.Signature.Reason)
	default:
		c.say("Signature: refused. %s", result.Signature.Reason)
	}
	if !result.UpdateAvailable {
		c.say("Already up to date (%s).", prefix7(result.RemoteSHA))
		return 0
	}
	if result.CommitsBehind >= 0 {
		c.say("Commits behind: %d", result.CommitsBehind)
	}
	names := make([]string, 0, len(result.Components))
	for name := range result.Components {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > 0 {
		c.say("Changed components:")
		for _, name := range names {
			comp := result.Components[name]
			c.say("  - %s: %d file(s)", comp.Label, comp.FileCount)
		}
	}

	c.say("[2/4] Checking what the install needs")
	pf := s.Preflight(ctx, result.RemoteSHA)
	for _, w := range pf.Warnings {
		c.say("  warning: %s", w)
	}
	for _, issue := range pf.Issues {
		c.say("  issue: %s", issue)
	}
	if check {
		c.say("Check complete. Nothing was changed.")
		return 0
	}
	if !pf.Ready {
		c.say("Update failed: the install cannot run until the issues above are solved.")
		return 1
	}
	if !yes {
		fmt.Fprintf(c.Out, "Install %s %s into %s now? [y/N] ", Product, result.LatestVersion, paths.Binary)
		answer, _ := bufio.NewReader(c.In).ReadString('\n')
		if !reYes.MatchString(strings.TrimSpace(answer)) {
			c.say("Update cancelled.")
			return 0
		}
	}

	if backup {
		c.say("[3/4] Downloading, checking and installing, with a backup of the running binary")
	} else {
		c.say("[3/4] Downloading, checking and installing, without a backup")
	}
	outcome, err := s.Install(ctx, InstallOptions{SHA: result.RemoteSHA, Backup: backup})
	if err != nil {
		c.say("Update failed: %v", err)
		c.say("Nothing was changed.")
		return 1
	}
	installed, ok := outcome.(*InstallResult)
	if !ok {
		c.say("Already up to date (%s).", prefix7(result.RemoteSHA))
		return 0
	}
	c.say("[4/4] Done")
	c.say("  Installed: %s", strings.Join(installed.Applied, ", "))
	c.say("  Binary: %s (the previous one is %s)", paths.Binary, paths.Prev)
	if installed.BackupPath != nil {
		c.say("  Backup: %s", *installed.BackupPath)
	} else {
		c.say("  Backup: not created")
	}
	c.say("Restart the service to start the new version (systemctl restart neodesk-server, or Restart-Service NeoDeskServer).")
	c.say("If it does not start three times in a row, the previous version is put back; `neodesk-server rollback` does it by hand.")
	return 0
}

// Rollback is "neodesk-server rollback": the binary before the last
// update goes back in place, and the replaced one is kept as .prev, so a
// second rollback undoes the first. The service is not restarted.
func (c CLI) Rollback(args []string) int {
	if len(args) > 0 {
		c.say("Unknown argument %q. Use: neodesk-server rollback", args[0])
		return 2
	}
	binary, err := c.Service.Rollback()
	if err != nil {
		c.say("Rollback failed: %v", err)
		c.say("Nothing was changed.")
		return 1
	}
	c.say("Put the previous binary back in place of %s; the replaced one is now %s.prev.", binary, binary)
	c.say("Restart the service to start it (systemctl restart neodesk-server, or Restart-Service NeoDeskServer).")
	return 0
}

func orUnknown(s string) string {
	if s == "" {
		return "commit unknown"
	}
	return prefix7(s)
}
