package branding

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// legacyFontsCopied is the file in FontsDir that says the fonts of
// LegacyFontsDir were copied, so a font deleted later does not come back.
const legacyFontsCopied = ".copied-from-legacy-fonts"

// copyLegacyFonts copies the installed fonts of the folder they were kept
// in before (from) into the fonts folder in the data folder (to), once:
// every font folder with its regular files. Links and anything else are
// left behind. A missing from, or a to that was filled before, copies
// nothing.
func copyLegacyFonts(from, to string) error {
	if from == "" || filepath.Clean(from) == filepath.Clean(to) {
		return nil
	}
	if _, err := os.Stat(filepath.Join(to, legacyFontsCopied)); err == nil {
		return nil
	}
	folders, err := os.ReadDir(from)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := os.MkdirAll(to, 0o777); err != nil {
		return err
	}
	for _, folder := range folders {
		if !folder.IsDir() {
			continue
		}
		if err := copyFontFolder(filepath.Join(from, folder.Name()), filepath.Join(to, folder.Name())); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(to, legacyFontsCopied), nil, 0o666)
}

// copyFontFolder copies the regular files of one font folder; a file that
// is there already stays.
func copyFontFolder(from, to string) error {
	files, err := os.ReadDir(from)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(to, 0o777); err != nil {
		return err
	}
	for _, file := range files {
		if !file.Type().IsRegular() {
			continue
		}
		dest := filepath.Join(to, file.Name())
		if _, err := os.Lstat(dest); err == nil {
			continue
		}
		if err := copyFile(filepath.Join(from, file.Name()), dest); err != nil {
			return err
		}
	}
	return nil
}
