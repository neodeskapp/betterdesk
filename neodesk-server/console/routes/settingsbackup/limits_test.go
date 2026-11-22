package settingsbackup

import (
	"fmt"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/backup"
)

// UM42: a limit that is no whole number of at least 1 gets the default,
// and the start says so once per variable.
func TestArchiveLimitsWarnAboutOddValues(t *testing.T) {
	for _, c := range []struct {
		bytes, entries string
		want           backup.Limits
		warned         []string
	}{
		{"", "", backup.Limits{}, nil},
		{"4096", "0x10", backup.Limits{MaxBytes: 4096, MaxEntries: 16}, nil},
		{"-1", "1.5", backup.Limits{}, []string{"BACKUP_MAX_DECOMPRESSED_BYTES", "BACKUP_MAX_ENTRIES"}},
		{"Infinity", "7", backup.Limits{MaxEntries: 7}, []string{"BACKUP_MAX_DECOMPRESSED_BYTES"}},
		{"1024", "abc", backup.Limits{MaxBytes: 1024}, []string{"BACKUP_MAX_ENTRIES"}},
	} {
		env := map[string]string{"BACKUP_MAX_DECOMPRESSED_BYTES": c.bytes, "BACKUP_MAX_ENTRIES": c.entries}
		var lines []string
		got := archiveLimits(func(k string) string { return env[k] }, func(args ...any) { lines = append(lines, fmt.Sprint(args...)) })
		if got != c.want {
			t.Errorf("%q %q: limits %+v, want %+v", c.bytes, c.entries, got, c.want)
		}
		if len(lines) != len(c.warned) {
			t.Errorf("%q %q: warnings %q", c.bytes, c.entries, lines)
			continue
		}
		for i, name := range c.warned {
			if !strings.HasPrefix(lines[i], name+" is not a whole number") {
				t.Errorf("%q %q: warning %q", c.bytes, c.entries, lines[i])
			}
		}
	}
}
