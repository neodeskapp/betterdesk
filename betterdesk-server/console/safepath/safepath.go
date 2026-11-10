// Package safepath keeps file paths inside the folder they belong to. A
// path that comes from a request, from a database row or from an archive
// goes through one of the Resolve functions before the console opens,
// writes or deletes anything, and a path that leaves its folder, by ".."
// or through a symbolic link, is an error.
//
// The functions and their error texts are those of the old console's
// lib/safePath.js:
//
//	ResolveChild(root, name)            resolveChildPath          one file or folder name below root
//	ResolveUnderRoot(root, relative)    resolvePathUnderRoot      a relative path with slashes below root
//	ResolveWithinRoot(path, root)       resolvePathWithinRoot     a whole path that has to lie in root
//	ResolveWithinAnyRoot(path, roots)   resolvePathWithinAnyRoot  the same for several folders
//	IsInsideRoot(path, root)            isPathInsideRoot          the check behind all of them
//
// Relative paths are taken from the working directory of the process.
package safepath

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// The errors of this package. Their texts are the messages the old
// console threw.
var (
	ErrInvalidSegment       = errors.New("Invalid path segment")
	ErrOutside              = errors.New("Path outside allowed directory")
	ErrResolvesOutside      = errors.New("Path resolves outside allowed directory")
	ErrOutsideRoots         = errors.New("Path outside allowed directory roots")
	ErrResolvesOutsideRoots = errors.New("Path resolves outside allowed directory roots")
	ErrPathRequired         = errors.New("Path is required")
	ErrInvalidPath          = errors.New("Invalid path")
	ErrRelativeRequired     = errors.New("Relative path is required")
	ErrInvalidRelative      = errors.New("Invalid relative path")
)

// resolve is path.resolve(p): the absolute, cleaned path.
func resolve(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return abs
}

// exists is fs.existsSync(p): false for a missing path, for a link that
// points nowhere and for a path that cannot be looked at.
func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// comparable returns the path with every symbolic link resolved, as far
// as the path exists: the real path of its longest existing part with the
// rest appended. Where the links cannot be resolved the path stays as
// written.
func comparable(value string) string {
	normalized := resolve(value)
	var missing []string
	existing := normalized
	for !exists(existing) {
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		missing = append([]string{filepath.Base(existing)}, missing...)
		existing = parent
	}
	if exists(existing) {
		if real, err := filepath.EvalSymlinks(existing); err == nil {
			normalized = filepath.Join(append([]string{real}, missing...)...)
		}
	}
	if runtime.GOOS == "windows" {
		normalized = strings.ToLower(strings.TrimPrefix(normalized, `\\?\`))
	}
	return normalized
}

// IsInsideRoot reports whether path is root or lies below it, after
// symbolic links in both are resolved. A name that starts with two dots
// directly below root ("..data") counts as outside, as it always did.
func IsInsideRoot(path, root string) bool {
	rel, err := filepath.Rel(comparable(root), comparable(path))
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, "..") && !filepath.IsAbs(rel))
}

// ResolveChild returns the path of name directly below root. name is one
// path segment: not empty, not "." or "..", without slash, backslash or
// NUL (ErrInvalidSegment). A name that is a link out of root is
// ErrOutside. The file need not exist.
func ResolveChild(root, name string) (string, error) {
	if name == "" || strings.ContainsAny(name, "\x00/\\") || name == "." || name == ".." {
		return "", ErrInvalidSegment
	}
	root = resolve(root)
	target := filepath.Join(root, name)
	if !IsInsideRoot(target, root) {
		return "", ErrOutside
	}
	return target, nil
}

// ResolveWithinRoot checks a whole path, absolute or relative to the
// working directory, against root. For a path that exists the result is
// its real path, which has to lie in root too (ErrResolvesOutside); for
// one that does not exist it is the absolute path.
func ResolveWithinRoot(path, root string) (string, error) {
	if path == "" {
		return "", ErrPathRequired
	}
	if strings.ContainsRune(path, 0) {
		return "", ErrInvalidPath
	}
	root = resolve(root)
	abs := resolve(path)
	if !IsInsideRoot(abs, root) {
		return "", ErrOutside
	}
	if !exists(abs) {
		return abs, nil
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	if !IsInsideRoot(real, root) {
		return "", ErrResolvesOutside
	}
	return real, nil
}

// ResolveWithinAnyRoot is ResolveWithinRoot for a path that may lie in
// any of several folders.
func ResolveWithinAnyRoot(path string, roots []string) (string, error) {
	if path == "" {
		return "", ErrPathRequired
	}
	if strings.ContainsRune(path, 0) {
		return "", ErrInvalidPath
	}
	abs := resolve(path)
	inside := func(p string) bool {
		for _, root := range roots {
			if IsInsideRoot(p, resolve(root)) {
				return true
			}
		}
		return false
	}
	if !inside(abs) {
		return "", ErrOutsideRoots
	}
	if !exists(abs) {
		return abs, nil
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	if !inside(real) {
		return "", ErrResolvesOutsideRoots
	}
	return real, nil
}

// ResolveUnderRoot returns the path of a relative path below root. The
// path may use slashes or backslashes; empty parts are skipped, and every
// other part has to pass ResolveChild, so "." and ".." are errors. An
// absolute path is ErrInvalidRelative.
func ResolveUnderRoot(root, relative string) (string, error) {
	if relative == "" {
		return "", ErrRelativeRequired
	}
	if strings.ContainsRune(relative, 0) {
		return "", ErrInvalidRelative
	}
	normalized := strings.ReplaceAll(relative, `\`, "/")
	if strings.HasPrefix(normalized, "/") || filepath.IsAbs(normalized) {
		return "", ErrInvalidRelative
	}
	current := resolve(root)
	for _, segment := range strings.Split(normalized, "/") {
		if segment == "" {
			continue
		}
		next, err := ResolveChild(current, segment)
		if err != nil {
			return "", err
		}
		current = next
	}
	return current, nil
}
