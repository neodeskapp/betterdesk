//go:build contract

package contract_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"net/url"
	"time"

	"github.com/unitronix/betterdesk-server/console/contract"
)

// The backup routes of the settings group: the full backup and the
// settings snapshot as downloads, the scheduled backup with its
// destination check, the restore and the retention count of the updater's
// backups. The network share login is gone (L122): its routes are unknown
// paths, so the matrix and the cases leave them out.
//
// The restore is on the matrix skip list. Its cases never put anything
// back: they are the refusals (no file, not JSON, not a backup, a broken
// or truncated archive, a manifest that is missing, wrong or null, a
// file in another field) and two uploads that pass the checks with every
// part switched off. A real restore is covered by the unit tests of
// console/backup.
//
// What a backup holds depends on what the groups before this one left on
// the instance: the general settings cases add settings rows and may make
// the session secret file. The counts of settings, the session secret
// flag and the list of parts are compared by type; the unit tests of
// console/backup pin them against Node.
func init() {
	some := contract.Roles("signed-out", "viewer", "server_admin", "admin")
	admin := contract.Roles("admin")
	disposition := []string{"content-disposition"}
	snapshotOff := []contract.Part{
		{Field: "restoreSettings", Content: []byte("false")},
		{Field: "restoreBranding", Content: []byte("false")},
		{Field: "restoreFolders", Content: []byte("false")},
		{Field: "restoreGroups", Content: []byte("false")},
		{Field: "restoreAddressBooks", Content: []byte("false")},
	}
	fullOff := []contract.Part{
		{Field: "restoreDatabase", Content: []byte("false")},
		{Field: "restoreUploads", Content: []byte("false")},
	}
	upload := func(name string, content []byte, extra ...contract.Part) []contract.Part {
		return append([]contract.Part{{Field: "backup", FileName: name, ContentType: "application/octet-stream", Content: content}}, extra...)
	}
	manifest := func(text string) []byte {
		return tarGz([2]string{"manifest.json", text}, [2]string{"console/data.json", `{"_engine":"sqlite","tables":{}}`})
	}

	contract.Group("settings-backup",
		contract.Case{Name: "stats", Path: "/api/settings/backup/stats", Volatile: []string{"~data.components.sessionSecret", "~data.settings"}},

		contract.Case{Name: "download", Path: "/api/settings/backup", Roles: some, VolatileHeaders: disposition, Volatile: []string{"$"}},
		contract.Case{
			Name: "download-snapshot", Path: "/api/settings/backup", Query: url.Values{"format": {"json"}},
			Roles: contract.Roles("viewer", "admin"), VolatileHeaders: disposition,
			Volatile: []string{"_created", "~console", "~goServer"},
		},
		contract.Case{
			Name: "download-format-upper-case", Path: "/api/settings/backup", Query: url.Values{"format": {"JSON"}},
			Roles: admin, VolatileHeaders: disposition, Volatile: []string{"$"},
		},
		contract.Case{
			Name: "download-format-twice", Path: "/api/settings/backup", Query: url.Values{"format": {"json", "json"}},
			Roles: admin, VolatileHeaders: disposition, Volatile: []string{"$"},
		},

		contract.Case{Name: "retention", Path: "/api/settings/backup/retention", Roles: some},
		contract.Case{Name: "retention-set", Method: "PUT", Path: "/api/settings/backup/retention", Body: map[string]any{"count": 5}, Roles: some},
		contract.Case{Name: "retention-after-set", Path: "/api/settings/backup/retention", Roles: admin},
		contract.Case{Name: "retention-negative", Method: "PUT", Path: "/api/settings/backup/retention", Body: map[string]any{"count": -1}, Roles: admin},
		contract.Case{Name: "retention-text", Method: "PUT", Path: "/api/settings/backup/retention", Body: map[string]any{"count": "abc"}, Roles: admin},
		contract.Case{Name: "retention-too-large", Method: "PUT", Path: "/api/settings/backup/retention", Body: map[string]any{"count": 1001}, Roles: admin},
		contract.Case{Name: "retention-missing", Method: "PUT", Path: "/api/settings/backup/retention", Body: map[string]any{}, Roles: admin},
		contract.Case{Name: "retention-fraction-text", Method: "PUT", Path: "/api/settings/backup/retention", Body: map[string]any{"count": " 7.9x"}, Roles: admin},
		contract.Case{Name: "retention-exponent", Method: "PUT", Path: "/api/settings/backup/retention", Body: map[string]any{"count": 1e21}, Roles: admin},
		contract.Case{Name: "retention-list", Method: "PUT", Path: "/api/settings/backup/retention", Body: map[string]any{"count": []any{"12"}}, Roles: admin},
		contract.Case{Name: "retention-unlimited", Method: "PUT", Path: "/api/settings/backup/retention", Body: map[string]any{"count": 0}, Roles: admin},
		contract.Case{Name: "retention-after-unlimited", Path: "/api/settings/backup/retention", Roles: admin},

		contract.Case{Name: "schedule", Path: "/api/settings/backup/schedule", Roles: some, Volatile: []string{"~data.status.lastValidation.components"}},
		contract.Case{Name: "schedule-status", Path: "/api/settings/backup/schedule/status", Roles: some, Volatile: []string{"~data.lastValidation.components"}},
		contract.Case{
			Name: "schedule-save", Method: "PUT", Path: "/api/settings/backup/schedule",
			Body: map[string]any{"enabled": false, "intervalHours": "12", "retentionCount": 3, "destinationType": "local", "destinationPath": "/ignored"}, Roles: some,
		},
		contract.Case{Name: "schedule-after-save", Path: "/api/settings/backup/schedule", Roles: admin, Volatile: []string{"~data.status.lastValidation.components"}},
		contract.Case{
			Name: "schedule-save-odd", Method: "PUT", Path: "/api/settings/backup/schedule",
			Body: map[string]any{"enabled": "true", "intervalHours": nil, "retentionCount": "0x10", "destinationType": "LOCAL"}, Roles: admin,
		},
		contract.Case{
			Name: "schedule-save-clamped", Method: "PUT", Path: "/api/settings/backup/schedule",
			Body: map[string]any{"enabled": 1, "intervalHours": 100000, "retentionCount": 2.5}, Roles: admin,
		},
		contract.Case{
			Name: "schedule-save-relative", Method: "PUT", Path: "/api/settings/backup/schedule",
			Body: map[string]any{"destinationType": "path", "destinationPath": "backups"}, Roles: admin,
		},
		contract.Case{
			Name: "schedule-save-no-path", Method: "PUT", Path: "/api/settings/backup/schedule",
			Body: map[string]any{"destinationType": "path", "destinationPath": 5}, Roles: admin,
		},
		contract.Case{
			Name: "schedule-save-root", Method: "PUT", Path: "/api/settings/backup/schedule",
			Body: map[string]any{"destinationType": "path", "destinationPath": " /./ "}, Roles: admin,
		},
		contract.Case{
			Name: "schedule-save-app-folder", Method: "PUT", Path: "/api/settings/backup/schedule",
			Body: map[string]any{"destinationType": "path", "destinationPath": "/repo/web-nodejs/backups"}, Roles: admin,
		},
		contract.Case{
			Name: "schedule-save-path", Method: "PUT", Path: "/api/settings/backup/schedule",
			Body: map[string]any{"destinationType": "path", "destinationPath": "/tmp/bk-contract/../bk-contract-dest/"}, Roles: admin,
		},
		contract.Case{Name: "schedule-after-path", Path: "/api/settings/backup/schedule", Roles: admin, Volatile: []string{"~data.status.lastValidation.components"}},

		contract.Case{Name: "destination-local", Method: "POST", Path: "/api/settings/backup/destination/test", Body: map[string]any{}, Roles: some},
		contract.Case{
			Name: "destination-path", Method: "POST", Path: "/api/settings/backup/destination/test",
			Body: map[string]any{"destinationType": "path", "destinationPath": "/tmp/bk-contract-probe"}, Roles: admin,
		},
		contract.Case{
			Name: "destination-root", Method: "POST", Path: "/api/settings/backup/destination/test",
			Body: map[string]any{"destinationType": "path", "destinationPath": "/"}, Roles: admin,
		},
		contract.Case{
			Name: "destination-relative", Method: "POST", Path: "/api/settings/backup/destination/test",
			Body: map[string]any{"destinationType": "path", "destinationPath": "./x"}, Roles: admin,
		},
		contract.Case{
			Name: "destination-unwritable", Method: "POST", Path: "/api/settings/backup/destination/test",
			Body: map[string]any{"destinationType": "path", "destinationPath": "/etc/hostname/bk-contract"}, Roles: admin,
		},

		// A run to a folder that cannot be made fails and leaves its error
		// in the state; then a run to the default folder.
		contract.Case{
			Name: "schedule-to-unwritable", Method: "PUT", Path: "/api/settings/backup/schedule",
			Body: map[string]any{"destinationType": "path", "destinationPath": "/etc/hostname/bk-contract"}, Roles: admin,
		},
		contract.Case{Name: "run-fails", Method: "POST", Path: "/api/settings/backup/schedule/run", Body: map[string]any{}, Roles: admin},
		contract.Case{Name: "status-after-failed-run", Path: "/api/settings/backup/schedule/status", Roles: admin},
		contract.Case{Name: "schedule-back-to-local", Method: "PUT", Path: "/api/settings/backup/schedule", Body: map[string]any{}, Roles: admin},
		contract.Case{Name: "run", Method: "POST", Path: "/api/settings/backup/schedule/run", Body: map[string]any{}, Roles: some, Volatile: []string{"~data.validation.components"}},
		contract.Case{Name: "status-after-run", Path: "/api/settings/backup/schedule/status", Roles: admin, Volatile: []string{"~data.lastValidation.components"}},

		contract.Case{Name: "restore-no-file", Method: "POST", Path: "/api/settings/restore", Body: map[string]any{}, Roles: some},
		contract.Case{
			Name: "restore-no-file-german", Method: "POST", Path: "/api/settings/restore", Body: map[string]any{},
			Headers: map[string]string{"Accept-Language": "de"}, Roles: admin,
		},
		contract.Case{Name: "restore-only-fields", Method: "POST", Path: "/api/settings/restore", Multipart: fullOff, Roles: admin},
		contract.Case{Name: "restore-not-json", Method: "POST", Path: "/api/settings/restore", Multipart: upload("backup.json", []byte("not json")), Roles: some},
		contract.Case{Name: "restore-empty-file", Method: "POST", Path: "/api/settings/restore", Multipart: upload("backup.json", nil), Roles: admin},
		contract.Case{
			Name: "restore-json-not-backup", Method: "POST", Path: "/api/settings/restore",
			Multipart: upload("backup.json", []byte(`{"a":1}`)), Roles: admin,
		},
		contract.Case{
			Name: "restore-json-list", Method: "POST", Path: "/api/settings/restore",
			Multipart: upload("backup.json", []byte(`[1,2]`)), Roles: admin,
		},
		contract.Case{
			Name: "restore-json-newer", Method: "POST", Path: "/api/settings/restore",
			Multipart: upload("backup.json", []byte(`{"_format":"betterdesk-backup","_version":2,"console":{}}`)), Roles: admin,
		},
		contract.Case{
			Name: "restore-json-nothing-chosen", Method: "POST", Path: "/api/settings/restore",
			Multipart: upload("backup.json", []byte(`{"_format":"betterdesk-backup","_version":1,"_created":"2026-01-02T03:04:05.000Z","_console_version":"3.5.0","console":{"settings":{"x":"y"},"folders":[]}}`), snapshotOff...),
			Roles:     some,
		},
		contract.Case{
			Name: "restore-broken-gzip", Method: "POST", Path: "/api/settings/restore",
			Multipart: upload("backup.tar.gz", []byte{0x1f, 0x8b, 0x08, 0x00, 0, 0, 0, 0, 0, 3, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0, 0, 0, 0, 0, 0, 0, 0}), Roles: some,
		},
		contract.Case{
			Name: "restore-truncated", Method: "POST", Path: "/api/settings/restore",
			Multipart: upload("backup.tar.gz", manifest(`{"_format":"betterdesk-full-backup","_version":1}`)[:60]), Roles: admin,
		},
		contract.Case{
			Name: "restore-not-gzip-method", Method: "POST", Path: "/api/settings/restore",
			Multipart: upload("backup.tar.gz", []byte{0x1f, 0x8b, 0x07, 0x00, 0, 0, 0, 0, 0, 3}), Roles: admin,
		},
		contract.Case{
			Name: "restore-no-manifest", Method: "POST", Path: "/api/settings/restore",
			Multipart: upload("backup.tar.gz", tarGz([2]string{"README.txt", "x"})), Roles: admin,
		},
		contract.Case{
			Name: "restore-manifest-not-json", Method: "POST", Path: "/api/settings/restore",
			Multipart: upload("backup.tar.gz", manifest(`{"_format":`)), Roles: admin,
		},
		contract.Case{
			Name: "restore-manifest-wrong", Method: "POST", Path: "/api/settings/restore",
			Multipart: upload("backup.tar.gz", manifest(`{"_format":"other","_version":"1"}`)), Roles: admin,
		},
		contract.Case{
			Name: "restore-manifest-null", Method: "POST", Path: "/api/settings/restore",
			Multipart: upload("backup.tar.gz", manifest(`null`)), Roles: admin,
		},
		contract.Case{
			Name: "restore-full-nothing-chosen", Method: "POST", Path: "/api/settings/restore",
			Multipart: upload("backup.tar.gz", manifest(`{"_format":"betterdesk-full-backup","_version":1}`), fullOff...), Roles: some,
		},
		contract.Case{
			Name: "restore-other-field", Method: "POST", Path: "/api/settings/restore",
			Multipart: []contract.Part{{Field: "file", FileName: "backup.json", ContentType: "application/json", Content: []byte(`{}`)}}, Roles: admin,
		},
	)
}

// tarGz is a gzip-compressed tar file with the given files.
func tarGz(files ...[2]string) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for _, f := range files {
		_ = tw.WriteHeader(&tar.Header{Name: f[0], Mode: 0o644, Size: int64(len(f[1])), ModTime: time.Unix(1700000000, 0), Typeflag: tar.TypeReg, Format: tar.FormatUSTAR})
		_, _ = tw.Write([]byte(f[1]))
	}
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}
