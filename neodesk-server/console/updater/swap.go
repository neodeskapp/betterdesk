package updater

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// BinaryPaths are the files the updater writes beside the running
// binary. Every one is the binary's own path with a fixed suffix, or a
// fixed name in its folder; nothing from a manifest or an address is part
// of them.
type BinaryPaths struct {
	// Binary is the running executable, symbolic links resolved.
	Binary string
	// Dir is its folder.
	Dir string
	// Prev is the binary before the last swap: <binary>.prev.
	Prev string
	// Pending is the marker of an update that has not started yet:
	// <binary>.update-pending.json.
	Pending string
}

// ResolveBinaryPaths finds the running binary through executable
// (os.Executable) and derives the paths beside it. It fails when the
// path is not absolute, is no regular file, or has a name the suffixes
// cannot be added to.
func ResolveBinaryPaths(executable func() (string, error)) (BinaryPaths, error) {
	exe, err := executable()
	if err != nil {
		return BinaryPaths{}, fmt.Errorf("find the running binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if !filepath.IsAbs(exe) || filepath.Clean(exe) != exe {
		return BinaryPaths{}, fmt.Errorf("the running binary's path %q is not absolute and clean", exe)
	}
	info, err := os.Stat(exe)
	if err != nil {
		return BinaryPaths{}, fmt.Errorf("find the running binary: %w", err)
	}
	if !info.Mode().IsRegular() {
		return BinaryPaths{}, fmt.Errorf("the running binary %s is not a regular file", exe)
	}
	base := filepath.Base(exe)
	if base == "" || base == "." || strings.HasPrefix(base, ".") || strings.ContainsAny(base, `/\`) {
		return BinaryPaths{}, fmt.Errorf("the running binary's name %q cannot be used", base)
	}
	p := BinaryPaths{Binary: exe, Dir: filepath.Dir(exe), Prev: exe + ".prev", Pending: exe + ".update-pending.json"}
	for _, derived := range []string{p.Prev, p.Pending} {
		if filepath.Dir(derived) != p.Dir {
			return BinaryPaths{}, fmt.Errorf("derived path %s is outside %s", derived, p.Dir)
		}
	}
	return p, nil
}

func (p BinaryPaths) base() string { return filepath.Base(p.Binary) }

// staging is the name a new binary is copied to before the swap:
// .<binary name>.new.<pid>.
func (p BinaryPaths) staging(pid int) string {
	return filepath.Join(p.Dir, "."+p.base()+".new."+strconv.Itoa(pid))
}

// aside is a name to move a binary out of the way when it cannot be
// deleted (Windows): <binary>.old.<pid>.<n>.
func (p BinaryPaths) aside(pid int, n int64) string {
	return p.Binary + ".old." + strconv.Itoa(pid) + "." + strconv.FormatInt(n, 10)
}

// leftovers lists the files of earlier swaps in the binary's folder:
// staging copies and binaries moved aside.
func (p BinaryPaths) leftovers() []string {
	entries, err := os.ReadDir(p.Dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if p.isLeftover(e.Name()) && e.Type().IsRegular() {
			out = append(out, filepath.Join(p.Dir, e.Name()))
		}
	}
	return out
}

// fileOps are the file system calls of a swap, so that a test can make
// each of them fail.
type fileOps struct {
	rename  func(oldpath, newpath string) error
	link    func(oldname, newname string) error
	remove  func(name string) error
	copy    func(src, dst string, sum string) error
	syncDir func(dir string) error
}

func osFileOps() fileOps {
	return fileOps{rename: os.Rename, link: os.Link, remove: os.Remove, copy: copyVerified, syncDir: syncDir}
}

// copyVerified copies src to a new file dst with mode 0755, flushes it to
// disk, and checks that what was written has the SHA-256 sum (when sum is
// not empty). dst must not exist. A failed copy removes dst.
func copyVerified(src, dst, sum string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			out.Close()
			_ = os.Remove(dst)
		}
	}()
	h := sha256.New()
	if _, err = io.Copy(io.MultiWriter(out, h), in); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	if err = out.Close(); err != nil {
		return err
	}
	if err = os.Chmod(dst, 0o755); err != nil {
		return err
	}
	if sum != "" && hex.EncodeToString(h.Sum(nil)) != sum {
		return fmt.Errorf("the copy of %s does not have the expected SHA-256", filepath.Base(src))
	}
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// swapper replaces the binary.
type swapper struct {
	paths BinaryPaths
	ops   fileOps
	// renameAside moves the running binary away by rename before the new
	// one takes its name (Windows, where a running binary cannot be
	// replaced but can be renamed). Elsewhere the new binary is renamed
	// over the old one, which the running process keeps open.
	renameAside bool
	pid         int
	now         func() time.Time
}

// install puts the verified file staged (any folder) in place of the
// binary and keeps the binary as .prev:
//
//  1. copy staged into the binary's folder as .<name>.new.<pid> and
//     check its SHA-256 again;
//  2. remove an old .prev (or move it aside);
//  3. keep the binary as .prev: a hard link, else a copy (renameAside:
//     a rename);
//  4. rename the copy over the binary (renameAside: to the binary's
//     name, and on failure the binary is renamed back).
//
// When it fails the binary is where it was and works.
func (s *swapper) install(staged, sum string) error {
	tmp := s.paths.staging(s.pid)
	_ = s.ops.remove(tmp)
	if err := s.ops.copy(staged, tmp, sum); err != nil {
		_ = s.ops.remove(tmp)
		return fmt.Errorf("copy the new binary into %s: %w", s.paths.Dir, err)
	}
	if err := s.clearPrev(); err != nil {
		_ = s.ops.remove(tmp)
		return err
	}
	if !s.renameAside {
		if err := s.ops.link(s.paths.Binary, s.paths.Prev); err != nil {
			if err := s.ops.copy(s.paths.Binary, s.paths.Prev, ""); err != nil {
				_ = s.ops.remove(tmp)
				return fmt.Errorf("keep the running binary as %s: %w", filepath.Base(s.paths.Prev), err)
			}
		}
		if err := s.ops.rename(tmp, s.paths.Binary); err != nil {
			_ = s.ops.remove(tmp)
			return fmt.Errorf("put the new binary in place: %w", err)
		}
		_ = s.ops.syncDir(s.paths.Dir)
		return nil
	}
	if err := s.ops.rename(s.paths.Binary, s.paths.Prev); err != nil {
		_ = s.ops.remove(tmp)
		return fmt.Errorf("move the running binary to %s: %w", filepath.Base(s.paths.Prev), err)
	}
	if err := s.ops.rename(tmp, s.paths.Binary); err != nil {
		if back := s.ops.rename(s.paths.Prev, s.paths.Binary); back != nil {
			return fmt.Errorf("put the new binary in place: %w; and the old binary could not be put back from %s: %v", err, s.paths.Prev, back)
		}
		_ = s.ops.remove(tmp)
		return fmt.Errorf("put the new binary in place: %w", err)
	}
	_ = s.ops.syncDir(s.paths.Dir)
	return nil
}

// clearPrev removes .prev, or moves it aside when it cannot be removed.
func (s *swapper) clearPrev() error {
	if _, err := os.Lstat(s.paths.Prev); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err := s.ops.remove(s.paths.Prev); err == nil {
		return nil
	}
	if err := s.ops.rename(s.paths.Prev, s.paths.aside(s.pid, s.now().UnixNano())); err != nil {
		return fmt.Errorf("make room for %s: %w", filepath.Base(s.paths.Prev), err)
	}
	return nil
}

// restorePrev puts .prev back in place of the binary. With keep the
// binary it replaces becomes the new .prev (an operator's rollback, which
// can be undone the same way); without, it is dropped (the automatic
// rollback of a binary that did not start). When it fails the binary is
// where it was.
func (s *swapper) restorePrev(keep bool) error {
	info, err := os.Lstat(s.paths.Prev)
	if err != nil {
		return fmt.Errorf("no previous binary %s: %w", s.paths.Prev, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("the previous binary %s is not a regular file", s.paths.Prev)
	}
	hold := s.paths.aside(s.pid, s.now().UnixNano())
	if !s.renameAside {
		if keep {
			if err := s.ops.link(s.paths.Binary, hold); err != nil {
				return fmt.Errorf("keep the current binary: %w", err)
			}
		}
		if err := s.ops.rename(s.paths.Prev, s.paths.Binary); err != nil {
			if keep {
				_ = s.ops.remove(hold)
			}
			return fmt.Errorf("put the previous binary back: %w", err)
		}
		if keep {
			if err := s.ops.rename(hold, s.paths.Prev); err != nil {
				return fmt.Errorf("keep the replaced binary as %s: %w", filepath.Base(s.paths.Prev), err)
			}
		}
		_ = s.ops.syncDir(s.paths.Dir)
		return nil
	}
	if err := s.ops.rename(s.paths.Binary, hold); err != nil {
		return fmt.Errorf("move the current binary aside: %w", err)
	}
	if err := s.ops.rename(s.paths.Prev, s.paths.Binary); err != nil {
		if back := s.ops.rename(hold, s.paths.Binary); back != nil {
			return fmt.Errorf("put the previous binary back: %w; and the current binary could not be put back from %s: %v", err, hold, back)
		}
		return fmt.Errorf("put the previous binary back: %w", err)
	}
	if keep {
		if err := s.ops.rename(hold, s.paths.Prev); err != nil {
			return fmt.Errorf("keep the replaced binary as %s: %w", filepath.Base(s.paths.Prev), err)
		}
	}
	_ = s.ops.syncDir(s.paths.Dir)
	return nil
}

// isLeftover reports .<name>.new.<digits> and <name>.old.<digits>.<digits>.
// The binary's name is compared as bytes: it may be any file name.
func (p BinaryPaths) isLeftover(name string) bool {
	if rest, ok := strings.CutPrefix(name, "."+p.base()+".new."); ok {
		return allDigits(rest)
	}
	if rest, ok := strings.CutPrefix(name, p.base()+".old."); ok {
		pid, n, found := strings.Cut(rest, ".")
		return found && allDigits(pid) && allDigits(n)
	}
	return false
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
