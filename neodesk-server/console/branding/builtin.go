package branding

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
)

// The built-in files (the theme presets, the palettes and the favicon)
// are read from disk where their option names a path, and from
// Options.Files at their place in the console's tree otherwise.
const (
	builtinThemes   = "themes"
	builtinPalettes = "config/theme-palettes.json"
	builtinFavicon  = "public/favicon.svg"
)

// readBuiltin reads one built-in file: the file at file on disk, or name
// in opts.Files when file is empty and Files is set. where is what
// messages call it.
func readBuiltin(opts Options, file, name string) (data []byte, where string, err error) {
	if file == "" && opts.Files != nil {
		data, err = fs.ReadFile(opts.Files, name)
		return data, name, err
	}
	data, err = os.ReadFile(file)
	return data, file, err
}

// themeFolder is the folder of the theme presets: dir on disk, or the
// folder dir of fsys.
type themeFolder struct {
	dir  string
	fsys fs.FS
}

func (s *Service) themeFolder() themeFolder {
	if s.opts.ThemesDir == "" && s.opts.Files != nil {
		return themeFolder{dir: builtinThemes, fsys: s.opts.Files}
	}
	return themeFolder{dir: s.opts.ThemesDir}
}

// exists says whether the folder, or a file of it, is there.
func (f themeFolder) exists(file string) bool {
	if f.fsys != nil {
		_, err := fs.Stat(f.fsys, file)
		return err == nil
	}
	return exists(file)
}

// file is the path of a preset: on disk it must lie in the folder, also
// after symbolic links are resolved (resolveChildPath).
func (f themeFolder) file(name string) string {
	if f.fsys != nil {
		return path.Join(f.dir, name)
	}
	return resolveChildPath(f.dir, name)
}

func (f themeFolder) list() ([]fs.DirEntry, error) {
	if f.fsys != nil {
		return fs.ReadDir(f.fsys, f.dir)
	}
	return os.ReadDir(f.dir)
}

func (f themeFolder) read(file string) ([]byte, error) {
	if f.fsys != nil {
		return fs.ReadFile(f.fsys, file)
	}
	return os.ReadFile(file)
}

// join is the file of an entry the folder lists.
func (f themeFolder) join(name string) string {
	if f.fsys != nil {
		return path.Join(f.dir, name)
	}
	return filepath.Join(f.dir, name)
}
