package billingclock

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// HostFS is what the time zone of the machine is read from: the TZ
// variable, the /etc/localtime link and the zoneinfo folder.
type HostFS struct {
	// LookupEnv is os.LookupEnv.
	LookupEnv func(string) (string, bool)
	// Realpath resolves every link of a path (realpath(3)).
	Realpath func(string) (string, error)
	// ReadFile is os.ReadFile.
	ReadFile func(string) ([]byte, error)
	// ZoneInfo is the zoneinfo folder, for a /etc/localtime that is a
	// copy and not a link.
	ZoneInfo fs.FS
}

const localtime = "/etc/localtime"

// SystemHostZone is HostZone on this machine.
func SystemHostZone() string {
	return HostZone(HostFS{
		LookupEnv: os.LookupEnv,
		Realpath:  filepath.EvalSymlinks,
		ReadFile:  os.ReadFile,
		ZoneInfo:  os.DirFS("/usr/share/zoneinfo"),
	})
}

// HostZone is the time zone Intl.DateTimeFormat().resolvedOptions()
// .timeZone reports on a Linux or macOS machine, found the way ICU finds
// it, or "" where it reports none that Intl takes:
//
//   - TZ, when set and shaped like a tz database name (letters and signs,
//     at most two digits at the end, or one of EST5EDT, CST6CDT, MST7MDT,
//     PST8PDT), without a leading ":" and a "posix/" or "right/" folder;
//   - else the name after "/zoneinfo/" in the target of /etc/localtime;
//   - else, for a copy, the first file of the zoneinfo folder with the
//     same bytes.
//
// The name must be one ICU knows, spelled as ICU spells it; Intl reports
// the zone's main name for it, and "+00:00" for GMT. On Windows nothing
// is found.
func HostZone(h HostFS) string {
	if tz, set := h.LookupEnv("TZ"); set && isOlsonShaped(tz) {
		return hostZoneName(skipZonePrefix(strings.TrimPrefix(tz, ":")))
	}
	target, err := h.Realpath(localtime)
	if err == nil && target != localtime {
		if _, tail, found := strings.Cut(target, "/zoneinfo/"); found {
			if tail = skipZonePrefix(tail); isOlsonShaped(tail) {
				return hostZoneName(tail)
			}
		}
		return ""
	}
	if h.ZoneInfo == nil || h.ReadFile == nil {
		return ""
	}
	content, err := h.ReadFile(localtime)
	if err != nil || len(content) == 0 {
		return ""
	}
	return hostZoneName(sameZoneFile(h.ZoneInfo, content))
}

// hostZoneName is what Intl reports for a host zone name.
func hostZoneName(name string) string {
	if name == "GMT" {
		return "+00:00"
	}
	resolved, ok := zoneNames[asciiLower(name)]
	if !ok || zoneSpelling[asciiLower(name)] != name {
		return ""
	}
	return resolved
}

// isOlsonShaped is ICU's isValidOlsonID: no digit and no comma before
// the end, except for up to two digits at the very end.
func isOlsonShaped(id string) bool {
	i := 0
	for i < len(id) && !isDigit(id[i]) && id[i] != ',' {
		i++
	}
	for limit := i + 2; i < len(id) && isDigit(id[i]) && i < limit; i++ {
	}
	switch id {
	case "PST8PDT", "MST7MDT", "CST6CDT", "EST5EDT":
		return true
	}
	return i == len(id)
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

// skipZonePrefix drops a "posix/" or "right/" folder in front.
func skipZonePrefix(id string) string {
	for _, prefix := range []string{"posix/", "right/"} {
		if strings.HasPrefix(id, prefix) {
			return id[len(prefix):]
		}
	}
	return id
}

// sameZoneFile is the name of the first file of the zoneinfo folder, in
// name order, whose bytes are content.
func sameZoneFile(zoneinfo fs.FS, content []byte) string {
	found := ""
	_ = fs.WalkDir(zoneinfo, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || found != "" {
			return fs.SkipDir
		}
		if d.IsDir() {
			if path == "posix" || path == "right" {
				return fs.SkipDir
			}
			return nil
		}
		if name := d.Name(); name == "posixrules" || name == "localtime" {
			return nil
		}
		if data, err := fs.ReadFile(zoneinfo, path); err == nil && bytes.Equal(data, content) {
			found = path
			return fs.SkipAll
		}
		return nil
	})
	return found
}
