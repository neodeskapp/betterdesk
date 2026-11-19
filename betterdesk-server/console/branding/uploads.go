package branding

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The size limits of uploads. A file may be exactly this long.
const (
	LogoMaxBytes       = 2 * 1024 * 1024
	BackgroundMaxBytes = 8 * 1024 * 1024
	FontMaxBytes       = 5 * 1024 * 1024
)

// ErrFileTooLarge is returned for an upload that is longer than its limit.
var ErrFileTooLarge = errors.New("file too large")

// ErrInvalidImageType is returned for a logo or background whose content
// type AcceptImageUpload refuses.
var ErrInvalidImageType = errors.New("Invalid file type")

var (
	reManagedLogo       = regexp.MustCompile(`^` + ci("logo-") + `[0-9a-fA-F]{16}\.` + ciAny("png", "jpg", "jpeg", "gif", "webp", "svg") + `$`)
	reManagedBackground = regexp.MustCompile(`^` + ci("bg-") + `[0-9a-fA-F]{16}\.` + ciAny("png", "jpg", "jpeg", "gif", "webp") + `$`)
	reFontFileName      = regexp.MustCompile(`\.` + ciAny("woff2", "ttf") + `$`)
)

// imageExtensions are the content types a logo or background may have,
// each with the extension its file gets.
var imageExtensions = map[string]string{
	"image/png":     ".png",
	"image/jpeg":    ".jpg",
	"image/gif":     ".gif",
	"image/webp":    ".webp",
	"image/svg+xml": ".svg",
}

// AcceptImageUpload reports whether a logo or background upload has an
// accepted content type: png, jpeg, gif, webp or svg+xml, as the client
// named it. The content itself is not looked at.
func AcceptImageUpload(mimeType string) bool {
	_, ok := imageExtensions[mimeType]
	return ok
}

// AcceptFontUpload reports whether a font upload is accepted: a file name
// that ends in .woff2 or .ttf, or one of the font content types. The
// content itself is not looked at.
func AcceptFontUpload(originalName, mimeType string) bool {
	if reFontFileName.MatchString(originalName) {
		return true
	}
	switch mimeType {
	case "font/woff2", "font/ttf", "application/font-woff2", "application/x-font-ttf":
		return true
	}
	return false
}

// StoreLogo writes an uploaded logo of the content type mimeType into
// UploadsDir and returns its file name, logo-<16 hex digits><extension>.
// The extension is the one of the type (.png, .jpg, .gif, .webp, .svg),
// whatever the client named the file, so an upload is never served as a
// page or a script. It fails with ErrInvalidImageType for any other type,
// and with ErrFileTooLarge, leaving no file, when content is longer than
// LogoMaxBytes. The picture is served as /uploads/<name>.
func (s *Service) StoreLogo(mimeType string, content io.Reader) (string, error) {
	return s.storeUpload("logo-", LogoMaxBytes, mimeType, content)
}

// StoreBackground writes an uploaded background picture into UploadsDir
// as StoreLogo does and returns its file name, bg-<16 hex
// digits><extension>. The limit is BackgroundMaxBytes.
func (s *Service) StoreBackground(mimeType string, content io.Reader) (string, error) {
	return s.storeUpload("bg-", BackgroundMaxBytes, mimeType, content)
}

func (s *Service) storeUpload(prefix string, limit int64, mimeType string, content io.Reader) (string, error) {
	ext, ok := imageExtensions[mimeType]
	if !ok {
		return "", ErrInvalidImageType
	}
	random := make([]byte, 8)
	if _, err := io.ReadFull(s.opts.Rand, random); err != nil {
		return "", wrap("make a file name", err)
	}
	name := prefix + hex.EncodeToString(random) + ext
	path := filepath.Join(s.opts.UploadsDir, name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o666)
	if err != nil {
		return "", fsError(err, "open", path)
	}
	written, err := io.Copy(file, io.LimitReader(content, limit+1))
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil && written > limit {
		err = ErrFileTooLarge
	}
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return name, nil
}

// RemovePreviousLogo deletes the logo file the current settings point to
// when it is one this service stored (logo-<16 hex>.<picture type> under
// /uploads/), is a regular file, and is not newFileName. Failures are
// ignored. The logo upload calls it after StoreLogo.
func (s *Service) RemovePreviousLogo(newFileName string) {
	logoURL := s.GetBranding().Text("logoUrl")
	if !strings.HasPrefix(logoURL, "/uploads/") {
		return
	}
	base := posixBasename(logoURL)
	if !reManagedLogo.MatchString(base) || base == newFileName {
		return
	}
	previous := filepath.Join(s.opts.UploadsDir, base)
	if !exists(previous) {
		return
	}
	if info, err := os.Lstat(previous); err == nil && info.Mode().IsRegular() {
		_ = os.Remove(previous)
	}
}

// ListBackgrounds lists the background pictures this service stored
// (bg-<16 hex>.png, jpg, jpeg, gif or webp; regular files only), newest
// first, each as {name, url, size, updatedAt}.
func (s *Service) ListBackgrounds() ([]any, error) {
	list, err := os.ReadDir(s.opts.UploadsDir)
	if err != nil {
		return nil, fsError(err, "scandir", s.opts.UploadsDir)
	}
	type background struct {
		object   *Object
		modified int64
	}
	var found []background
	for _, entry := range list {
		name := decodeUTF8([]byte(entry.Name()))
		if !reManagedBackground.MatchString(name) {
			continue
		}
		path := filepath.Join(s.opts.UploadsDir, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fsError(err, "lstat", path)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		modified := jsFileTime(info)
		found = append(found, background{
			object: object(
				"name", name,
				"url", "/uploads/"+name,
				"size", float64(info.Size()),
				"updatedAt", time.UnixMilli(modified).UTC().Format("2006-01-02T15:04:05.000Z"),
			),
			modified: modified,
		})
	}
	sort.SliceStable(found, func(i, j int) bool { return found[i].modified > found[j].modified })
	out := make([]any, len(found))
	for i, b := range found {
		out[i] = b.object
	}
	return out, nil
}

// jsFileTime is a file's modification time in milliseconds as Node's
// stat reports it: seconds and nanoseconds added up as floating point,
// then rounded to the nearest millisecond.
func jsFileTime(info os.FileInfo) int64 {
	t := info.ModTime()
	return int64(math.Floor(float64(t.Unix())*1e3 + float64(t.Nanosecond())/1e6 + 0.5))
}

// backgroundTargets pairs each setting that holds a background picture
// with the setting that holds the kind of that background.
var backgroundTargets = map[string]string{
	"bgImageUrl":         "bgType",
	"loginBgImageUrl":    "loginBgType",
	"agentBgImageUrl":    "agentBgType",
	"rdclientBgImageUrl": "rdclientBgType",
}

// ApplyBackground stores an uploaded picture as the background named by
// target (bgImageUrl, loginBgImageUrl, agentBgImageUrl or
// rdclientBgImageUrl) and switches that background to "image". For any
// other target it does nothing and answers applied false. With applied
// true, branding is the saved settings.
//
// A target that is the name of something every JavaScript object has
// ("constructor", "toString" ...) counts as known (BUGS BR10): nothing
// about the background is stored, and applied is true.
func (s *Service) ApplyBackground(ctx context.Context, target, url string) (branding *Object, applied bool, err error) {
	typeKey, known := backgroundTargets[target]
	if !known {
		typeKey, known = objectPrototype[target]
	}
	if target == "" || !known {
		return nil, false, nil
	}
	if err := s.SaveBranding(ctx, object(target, url, typeKey, "image")); err != nil {
		return nil, false, err
	}
	return s.GetBranding(), true, nil
}

// UploadFont installs an uploaded font file. family is the name the form
// sent; nothing stands for the file name without its extension. The bytes
// go to TempDir as font-upload-<milliseconds><.ttf or .woff2> first and
// from there into the font's folder (see RegisterUploadedFont). A file
// name that does not end in .ttf is stored as .woff2.
func (s *Service) UploadFont(family any, originalName string, content []byte) (font *Object, err error) {
	defer catch(&err)
	name := fontUploadFamily(family, originalName)
	ext := ".woff2"
	if toLower(posixExtname(originalName)) == ".ttf" {
		ext = ".ttf"
	}
	if !exists(s.opts.TempDir) {
		if err := os.MkdirAll(s.opts.TempDir, 0o777); err != nil {
			return nil, fsError(err, "mkdir", s.opts.TempDir)
		}
	}
	tmpPath := filepath.Join(s.opts.TempDir, "font-upload-"+strconv.FormatInt(s.opts.Now().UnixMilli(), 10)+ext)
	if err := writeFile(tmpPath, content); err != nil {
		return nil, err
	}
	return s.RegisterUploadedFont(name, tmpPath, posixBasename(filepath.ToSlash(tmpPath)))
}

// FontUploadFamily is the name UploadFont gives a font: the family the
// form sent, or the file name without its extension when that is empty
// in JavaScript's eyes, as text and trimmed. It fails where the family
// cannot be made text.
func FontUploadFamily(family any, originalName string) (name string, err error) {
	defer catch(&err)
	return fontUploadFamily(family, originalName), nil
}

func fontUploadFamily(family any, originalName string) string {
	return trim(toString(or(family, basenameWithout(originalName, posixExtname(originalName)))))
}
