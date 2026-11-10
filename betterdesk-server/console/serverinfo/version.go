package serverinfo

import (
	"encoding/json"
	"io"
	"path/filepath"
	"regexp"
	"strings"
)

// DefaultVersionFallback is the version ReadProductVersion reports when it
// finds none and the caller has no better answer.
const DefaultVersionFallback = "0.0.0"

var semverPattern = regexp.MustCompile(`^([0-9]+)\.([0-9]+)\.([0-9]+)$`)

// IsSemver reports whether v is three numbers with dots between them.
func IsSemver(v string) bool {
	return semverPattern.MatchString(v)
}

// readVersionFile returns the version in a VERSION file, or false when the
// file is missing, cannot be read or holds something else.
func readVersionFile(fsys FS, path string) (string, bool) {
	if !fsys.Exists(path) {
		return "", false
	}
	text, err := readText(fsys, path)
	if err != nil {
		return "", false
	}
	if v := jsTrim(text); IsSemver(v) {
		return v, true
	}
	return "", false
}

// ReadPackageJSONVersion returns the "version" of the package.json in
// consoleDir, or false when there is none that is three numbers.
func ReadPackageJSONVersion(fsys FS, consoleDir string) (string, bool) {
	text, err := readText(fsys, filepath.Join(consoleDir, "package.json"))
	if err != nil {
		return "", false
	}
	// Numbers stay text, so one that is too large for a float does not
	// make the file unreadable.
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var pkg any
	if err := dec.Decode(&pkg); err != nil {
		return "", false
	}
	if _, err := dec.Token(); err != io.EOF {
		return "", false
	}
	object, _ := pkg.(map[string]any)
	version, _ := object["version"].(string)
	if v := jsTrim(version); IsSemver(v) {
		return v, true
	}
	return "", false
}

// ReadProductVersion returns the version of the product: the VERSION file
// in rootDir, else the VERSION file in consoleDir, else the version of the
// package.json in consoleDir, else fallback. An empty consoleDir means the
// folder "web-nodejs" in rootDir.
func ReadProductVersion(fsys FS, rootDir, consoleDir, fallback string) string {
	if v, ok := readVersionFile(fsys, filepath.Join(rootDir, "VERSION")); ok {
		return v
	}
	if consoleDir == "" {
		consoleDir = filepath.Join(rootDir, "web-nodejs")
	}
	if v, ok := readVersionFile(fsys, filepath.Join(consoleDir, "VERSION")); ok {
		return v
	}
	if v, ok := ReadPackageJSONVersion(fsys, consoleDir); ok {
		return v
	}
	return fallback
}

// ReadEmbeddedServerVersion returns the version the server is built with:
// the file internal/productversion/VERSION in the server's source folder.
// It reports false for an empty serverDir, a missing file and a file that
// holds no version.
func ReadEmbeddedServerVersion(fsys FS, serverDir string) (string, bool) {
	if serverDir == "" {
		return "", false
	}
	return readVersionFile(fsys, filepath.Join(serverDir, "internal", "productversion", "VERSION"))
}
