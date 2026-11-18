package generator

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"hash/crc32"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// copyFile is fs.copyFile: the bytes and the mode.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return nodeFSError(err, "copyfile", src)
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	_ = os.Remove(dst)
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return nodeFSError(err, "copyfile", src)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, info.Mode().Perm())
}

// copyTree is fs.cp(src, dst, {recursive: true}): folders are made and
// get the source's mode, files keep their mode, and a symbolic link is
// made again pointing at its target, a relative target made absolute
// against the source folder. Times are not kept.
func copyTree(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return nodeFSError(err, "lstat", src)
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(src), target)
		}
		_ = os.Remove(dst)
		return os.Symlink(target, dst)
	case info.IsDir():
		created := false
		if _, err := os.Stat(dst); err != nil {
			if err := os.Mkdir(dst, 0o777); err != nil {
				return nodeFSError(err, "mkdir", dst)
			}
			created = true
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return nodeFSError(err, "scandir", src)
		}
		for _, e := range entries {
			if err := copyTree(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
				return err
			}
		}
		if created {
			return os.Chmod(dst, info.Mode().Perm())
		}
		return nil
	default:
		return copyFile(src, dst)
	}
}

// writeFolderZip packs the contents of dir below root/ into a zip the
// way adm-zip's addLocalFolder and writeZip did: an entry for every
// folder (name ending in "/") and file below dir, but none for dir
// itself; entries ordered by their lower-cased names as localeCompare
// orders them; the Unix mode in the external attributes and "made by
// Unix"; the file's modification time as a DOS time in local time; the
// UTF-8 flag on every name; folders and empty files stored, everything
// else deflated; no extra fields and no data descriptors.
//
// The compressed bytes are Go's deflate, not zlib's, so the archive's
// bytes and checksum differ from the old one's for the same files; what a
// program that unpacks it sees is the same.
func writeFolderZip(dir, root, outPath string) error {
	type item struct {
		name string
		path string
		info os.FileInfo
	}
	var items []item
	rootName := strings.Trim(path.Clean("/"+strings.ReplaceAll(root, `\`, "/")+"/"), "/")
	var walk func(local, rel string) error
	walk = func(local, rel string) error {
		entries, err := os.ReadDir(local)
		if err != nil {
			return nodeFSError(err, "scandir", local)
		}
		for _, e := range entries {
			full := filepath.Join(local, e.Name())
			info, err := os.Stat(full)
			if err != nil {
				return nodeFSError(err, "stat", full)
			}
			name := rel + e.Name()
			if info.IsDir() {
				items = append(items, item{name: rootName + "/" + name + "/", path: full, info: info})
				if err := walk(full, name+"/"); err != nil {
					return err
				}
				continue
			}
			items = append(items, item{name: rootName + "/" + name, path: full, info: info})
		}
		return nil
	}
	if err := walk(dir, ""); err != nil {
		return err
	}
	col := collate.New(language.English)
	sort.SliceStable(items, func(i, j int) bool {
		return col.CompareString(strings.ToLower(items[i].name), strings.ToLower(items[j].name)) < 0
	})

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, it := range items {
		isDir := it.info.IsDir()
		var data []byte
		if !isDir {
			content, err := os.ReadFile(it.path)
			if err != nil {
				return nodeFSError(err, "open", it.path)
			}
			data = content
		}
		unix := uint32(0x8000)
		dos := uint32(0)
		if isDir {
			unix, dos = 0x4000, 0x10
		}
		unix |= uint32(it.info.Mode().Perm()) | modeBits(it.info.Mode())
		dosDate, dosTime := dosDateTime(it.info.ModTime())
		fh := &zip.FileHeader{
			Name:               it.name,
			Flags:              0x800,
			CreatorVersion:     0x0314,
			ReaderVersion:      10,
			Method:             zip.Store,
			ModifiedDate:       dosDate,
			ModifiedTime:       dosTime,
			ExternalAttrs:      dos | unix<<16,
			CRC32:              crc32.ChecksumIEEE(data),
			UncompressedSize64: uint64(len(data)),
		}
		payload := data
		if !isDir && len(data) > 0 {
			var deflated bytes.Buffer
			fw, _ := flate.NewWriter(&deflated, 6)
			_, _ = fw.Write(data)
			_ = fw.Close()
			payload = deflated.Bytes()
			fh.Method = zip.Deflate
			fh.ReaderVersion = 20
		}
		fh.CompressedSize64 = uint64(len(payload))
		w, err := zw.CreateRaw(fh)
		if err != nil {
			return err
		}
		if _, err := w.Write(payload); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(outPath), 0o777); err != nil {
		return err
	}
	return os.WriteFile(outPath, buf.Bytes(), 0o666)
}

// zipEntryNames lists the names in a zip.
func zipEntryNames(path string) ([]string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, codeError("Invalid or unsupported zip format. No END header found")
	}
	defer zr.Close()
	names := make([]string, len(zr.File))
	for i, f := range zr.File {
		names[i] = f.Name
	}
	return names, nil
}

// modeBits are the setuid, setgid and sticky bits of a mode, in the place
// stat.mode has them.
func modeBits(m os.FileMode) uint32 {
	var bits uint32
	if m&os.ModeSetuid != 0 {
		bits |= 0o4000
	}
	if m&os.ModeSetgid != 0 {
		bits |= 0o2000
	}
	if m&os.ModeSticky != 0 {
		bits |= 0o1000
	}
	return bits
}

// dosDateTime is adm-zip's fromDate2DOS: local time, two-second steps,
// zero before 1980.
func dosDateTime(t time.Time) (date, clock uint16) {
	t = t.Local()
	if t.Year() <= 1979 {
		return 0, 0
	}
	date = uint16((t.Year()-1980)&0x7f)<<9 | uint16(t.Month())<<5 | uint16(t.Day())
	clock = uint16(t.Hour())<<11 | uint16(t.Minute())<<5 | uint16(t.Second()>>1)
	return date, clock
}
