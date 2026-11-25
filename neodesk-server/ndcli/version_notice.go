package ndcli

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// The server names its version on the answers to API token requests
// (X-NeoDesk-Version). A command line older than the server says so on
// stderr, once per pair of versions: the pair it told about last is kept
// in the config folder (neodesk/version-notice). On a Linux host install
// that is the root-owned copy after an update from the console, which
// `sudo neodesk-setup --update` brings up to date. Without a usable config
// folder it says so once per run. Development builds and answers without
// a version say nothing.

const versionHeader = "X-NeoDesk-Version"

// setupCommand is the Linux installer's manager, which also updates the
// command line's copy.
const setupCommand = "/usr/local/sbin/neodesk-setup"

// releaseVersion is a version X.Y.Z with an optional pre-release suffix.
var releaseVersion = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.]+))?$`)

// olderVersion reports whether a is an older release than b; false when
// either is no release version.
func olderVersion(a, b string) bool {
	ma, mb := releaseVersion.FindStringSubmatch(a), releaseVersion.FindStringSubmatch(b)
	if ma == nil || mb == nil {
		return false
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(ma[i])
		y, _ := strconv.Atoi(mb[i])
		if x != y {
			return x < y
		}
	}
	switch {
	case ma[4] == mb[4]:
		return false
	case ma[4] == "":
		return false
	case mb[4] == "":
		return true
	}
	// Pre-releases: identifier by identifier, numbers as numbers and
	// before text, a shorter list first
	pa, pb := strings.Split(ma[4], "."), strings.Split(mb[4], ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		x, errX := strconv.Atoi(pa[i])
		y, errY := strconv.Atoi(pb[i])
		switch {
		case errX == nil && errY == nil:
			if x != y {
				return x < y
			}
		case errX == nil:
			return true
		case errY == nil:
			return false
		case pa[i] != pb[i]:
			return pa[i] < pb[i]
		}
	}
	return len(pa) < len(pb)
}

// noteServerVersion tells once when the command line is older than the
// server that answered.
func (c *conn) noteServerVersion(server string) {
	if c.env == nil || c.versionNoted || server == "" || !olderVersion(c.env.Version, server) {
		return
	}
	c.versionNoted = true
	pair := c.env.Version + " " + server
	var state string
	if dir, err := c.env.UserConfigDir(); err == nil {
		state = filepath.Join(dir, "neodesk", "version-notice")
		if data, err := os.ReadFile(state); err == nil && strings.TrimSpace(string(data)) == pair {
			return
		}
	}
	advice := "update it to the server's version"
	if _, err := os.Stat(setupCommand); err == nil {
		advice = "update it on this server with: sudo neodesk-setup --update"
	}
	fmt.Fprintf(c.env.Stderr, "neodesk: this command line (%s) is older than the server (%s): %s\n", c.env.Version, server, advice)
	if state != "" && os.MkdirAll(filepath.Dir(state), 0o700) == nil {
		_ = os.WriteFile(state, []byte(pair+"\n"), 0o600)
	}
}
