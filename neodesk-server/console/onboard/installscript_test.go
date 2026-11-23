package onboard

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// runInstallScript runs the invite's Linux install script with stand-ins
// for the system's commands, which write their arguments to a log, and
// returns the log. installed is the version of NeoDesk the system has
// ("" for none), offered the one of the downloaded package; manager is
// apt-get, dnf or zypper.
func runInstallScript(t *testing.T, manager, installed, offered string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a POSIX shell")
	}
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(dir, "log")
	state := filepath.Join(dir, "installed")
	if err := os.WriteFile(state, []byte(installed), 0o644); err != nil {
		t.Fatal(err)
	}
	// The tools the script needs beside the stand-ins; nothing else of the
	// system, so its own package manager is not found
	for _, tool := range []string{"mktemp", "chmod", "rm", "cat"} {
		path, err := exec.LookPath(tool)
		if err != nil {
			t.Skip("no " + tool)
		}
		if err := os.Symlink(path, filepath.Join(bin, tool)); err != nil {
			t.Fatal(err)
		}
	}
	// down is what the stand-ins count as a downgrade: 1.5.0 to 1.5.0~rc.6
	const down = `down() { [ -s "$STATE" ] && [ "$(cat "$STATE")" != "$OFFERED" ] && case "$OFFERED" in *~*) true ;; *) false ;; esac; }`
	stub := func(name, body string) {
		script := "#!/bin/sh\nLOG='" + logFile + "'\nSTATE='" + state + "'\nOFFERED='" + offered + "'\n" + down + "\n" + body + "\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	stub("id", `echo 0`)
	stub("uname", `echo x86_64`)
	stub("curl", `while [ $# -gt 1 ]; do [ "$1" = -o ] && : > "$2"; shift; done`)
	stub("neodesk", `echo "neodesk $*" >> "$LOG"`)
	// A package manager that installs what it is told, and refuses a
	// downgrade or does nothing for the same version the way apt and dnf do
	// without the flags that allow it
	stub("rpm", `for a in "$@"; do [ "$a" = '%{NAME}' ] && { printf neodesk; exit 0; }; done
case "$*" in
	*-qp*) printf 'neodesk-%s' "$OFFERED" ;;
	*-q*) [ -s "$STATE" ] && printf 'neodesk-%s' "$(cat "$STATE")" || exit 1 ;;
esac`)
	switch manager {
	case "apt-get":
		stub("apt-get", `echo "apt-get $*" >> "$LOG"
case " $* " in *" --allow-downgrades "*) ;; *) down && { echo "downgrade refused" >> "$LOG"; exit 100; } ;; esac
case " $* " in *" --reinstall "*) ;; *) [ "$(cat "$STATE")" = "$OFFERED" ] && { echo "already the newest version" >> "$LOG"; exit 0; } ;; esac
printf '%s' "$OFFERED" > "$STATE"`)
	case "dnf":
		stub("dnf", `echo "dnf $*" >> "$LOG"
case "$1" in
	install) down && { echo "higher version installed" >> "$LOG"; exit 0; }
		[ "$(cat "$STATE")" = "$OFFERED" ] && { echo "already installed" >> "$LOG"; exit 0; } ;;
esac
printf '%s' "$OFFERED" > "$STATE"`)
	case "zypper":
		stub("zypper", `echo "zypper $*" >> "$LOG"
case " $* " in *" --oldpackage "*) ;; *) down && exit 1 ;; esac
case " $* " in *" --force "*) ;; *) [ "$(cat "$STATE")" = "$OFFERED" ] && exit 0 ;; esac
printf '%s' "$OFFERED" > "$STATE"`)
	}
	cmd := exec.Command(sh, "-c", InstallScript("http://10.10.10.74:5000", "ABCD2345"))
	cmd.Env = []string{"PATH=" + bin, "HOME=" + dir}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s from %q to %q: %v\n%s", manager, installed, offered, err, out)
	}
	got, _ := os.ReadFile(state)
	if string(got) != offered {
		t.Errorf("%s from %q to %q: %q is installed", manager, installed, offered, got)
	}
	log, _ := os.ReadFile(logFile)
	if !strings.Contains(string(log), "neodesk --join http://10.10.10.74:5000/join/ABCD2345") {
		t.Errorf("%s: the computer did not join:\n%s", manager, log)
	}
	return string(log)
}

// TestTheInviteInstallsTheOfferedPackageWhateverIsThere: a computer with
// client rc.4 (version 1.5.0) gets the later 1.5.0~rc.6, which apt and
// dnf see as a downgrade, and the same version is installed again; a new
// computer and an upgrade work as before (L420).
func TestTheInviteInstallsTheOfferedPackageWhateverIsThere(t *testing.T) {
	for _, manager := range []string{"apt-get", "dnf", "zypper"} {
		for _, c := range []struct{ installed, offered string }{
			{"", "1.5.0~rc.6"},
			{"1.5.0", "1.5.0~rc.6"},
			{"1.5.0", "1.5.0"},
			{"1.4.6", "1.5.0"},
		} {
			runInstallScript(t, manager, c.installed, c.offered)
		}
	}
}
