package managedconfig

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// KeptBackups is how many backups of a configuration file are kept: the
// newest by the time in their names. Every save of the file, and every
// switch of the update channel, makes one, and each holds the file's
// secrets.
const KeptBackups = 5

// backupStamp is the time in the name of a backup "<file>.bak.<time>", as
// Apply and the configuration file editor write it: toISOString with ":"
// and "." made "-". Names of this form sort by time.
var backupStamp = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}-\d{3}Z$`)

// PruneBackups removes the backups "<path>.bak.<time>" of a file but the
// KeptBackups newest. Files of another name next to them, such as a copy
// an admin made by hand, stay.
func PruneBackups(path string) error {
	dir, base := filepath.Split(path)
	if dir == "" {
		dir = "."
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("list the backups of %s: %w", path, err)
	}
	prefix := base + ".bak."
	var backups []string
	for _, e := range entries {
		name := e.Name()
		if e.Type().IsRegular() && strings.HasPrefix(name, prefix) && backupStamp.MatchString(name[len(prefix):]) {
			backups = append(backups, name)
		}
	}
	sort.Strings(backups)
	var errs []error
	for _, name := range backups[:max(0, len(backups)-KeptBackups)] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
