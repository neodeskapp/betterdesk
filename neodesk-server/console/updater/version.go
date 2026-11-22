package updater

import (
	"regexp"
	"strconv"
	"strings"
)

// Channels and their branches. UPDATE_GITHUB_BRANCH holds the branch.
const (
	ChannelStable      = "stable"
	ChannelDevelopment = "development"
	BranchStable       = "main"
	BranchDevelopment  = "dev"
)

// ChannelOf maps a branch to its channel: "dev" is development,
// everything else stable (branchToChannel).
func ChannelOf(branch string) string {
	if branch == BranchDevelopment {
		return ChannelDevelopment
	}
	return ChannelStable
}

// BranchOf maps a channel to its branch, "" for an unknown channel.
func BranchOf(channel string) string {
	switch channel {
	case ChannelStable:
		return BranchStable
	case ChannelDevelopment:
		return BranchDevelopment
	}
	return ""
}

// ChannelLabel is the name the panel shows for a channel.
func ChannelLabel(channel string) string {
	switch channel {
	case ChannelStable:
		return "Stable"
	case ChannelDevelopment:
		return "Development"
	}
	return channel
}

// CompareVersions compares two versions X.Y.Z[-suffix]: -1, 0 or 1. A
// version with a suffix comes before the same version without one; two
// suffixes compare as semver orders pre-releases, identifier by
// identifier, numbers as numbers (rc.9 before rc.10). A version that is
// not of that form (a development build's "dev") comes before every
// version that is.
func CompareVersions(a, b string) int {
	pa, okA := parseVersion(a)
	pb, okB := parseVersion(b)
	switch {
	case !okA && !okB:
		return strings.Compare(a, b)
	case !okA:
		return -1
	case !okB:
		return 1
	}
	for i := 0; i < 3; i++ {
		if pa.num[i] != pb.num[i] {
			if pa.num[i] < pb.num[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case pa.suffix == pb.suffix:
		return 0
	case pa.suffix == "":
		return 1
	case pb.suffix == "":
		return -1
	}
	return comparePrerelease(pa.suffix, pb.suffix)
}

// comparePrerelease compares two pre-release suffixes (rc.1, beta.2) by
// semver's rule: numeric identifiers as numbers and before text ones,
// text ones as text, and a shorter list first when all else is equal.
func comparePrerelease(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		na, errA := strconv.ParseUint(as[i], 10, 64)
		nb, errB := strconv.ParseUint(bs[i], 10, 64)
		switch {
		case errA == nil && errB == nil:
			if na != nb {
				if na < nb {
					return -1
				}
				return 1
			}
		case errA == nil:
			return -1
		case errB == nil:
			return 1
		default:
			if c := strings.Compare(as[i], bs[i]); c != 0 {
				return c
			}
		}
	}
	switch {
	case len(as) < len(bs):
		return -1
	case len(as) > len(bs):
		return 1
	}
	return 0
}

type version struct {
	num    [3]int
	suffix string
}

func parseVersion(v string) (version, bool) {
	if !reVersion.MatchString(v) {
		return version{}, false
	}
	var out version
	main, suffix, _ := strings.Cut(v, "-")
	out.suffix = suffix
	for i, part := range strings.SplitN(main, ".", 3) {
		n, err := strconv.Atoi(part)
		if err != nil {
			return version{}, false
		}
		out.num[i] = n
	}
	return out, true
}

// reVersionLine reads what "betterdesk-server -version" prints:
// "betterdesk-server 3.6.0 (built 2026-11-18, commit <hex>)". Builds
// without a commit leave ", commit ..." out.
var reVersionLine = regexp.MustCompile(`^betterdesk-server (\S+) \(built [^,)]*(?:, commit ([0-9a-f]{7,40}))?\)\s*$`)

// ParseVersionOutput reads the version and the commit from the output of
// -version. ok is false when the output is not that line.
func ParseVersionOutput(out string) (ver, commit string, ok bool) {
	m := reVersionLine.FindStringSubmatch(strings.TrimSpace(out))
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// AssetName is the name of the binary for a platform:
// betterdesk-server-<goos>-<arch>, with armv7 for 32-bit ARM and ".exe"
// on Windows.
func AssetName(goos, goarch string) string {
	arch := goarch
	if goarch == "arm" {
		arch = "armv7"
	}
	name := Product + "-" + goos + "-" + arch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// shaMatches is the updater's rule for "the same commit": equal, or one
// starts with the first seven characters of the other, ignoring case
// (updateResultStore.shaMatches).
func shaMatches(a, b string) bool {
	left, right := strings.ToLower(strings.TrimSpace(a)), strings.ToLower(strings.TrimSpace(b))
	if left == "" || right == "" {
		return false
	}
	return left == right || strings.HasPrefix(left, prefix7(right)) || strings.HasPrefix(right, prefix7(left))
}

func prefix7(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

// reSHA is what the routes accept as a commit: 7 to 40 hex digits.
var reSHA = regexp.MustCompile(`^(?i)[0-9a-f]{7,40}$`)

// ValidSHA reports a commit as the routes accept it.
func ValidSHA(s string) bool { return reSHA.MatchString(s) }
