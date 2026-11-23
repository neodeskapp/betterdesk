package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/neodeskapp/neodesk/neodesk-server/console/branding"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/setupguide"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The settings snapshot: the older backup format, one JSON file with the
// console's settings, branding, accounts, folders, groups, rules and
// address books, and what the API server said. GET /api/settings/backup
// gives it for ?format=json; a restore takes it when the upload is not
// gzip.

// CreateBackup builds the snapshot (createBackup). The first error of a
// query ends it. The settings that hold a password go in without it (see
// dropPasswords).
func (s *Service) CreateBackup(ctx context.Context) (*goapi.Object, error) {
	timestamp := s.isoNow()
	db := s.o.Store
	settings, err := allSettings(ctx, db)
	if err != nil {
		return nil, err
	}
	dropPasswords(settings)
	var brand any = branding.NewObject()
	if s.o.Branding != nil {
		brand = s.o.Branding.GetBranding()
	}
	users, err := usersForBackup(ctx, db)
	if err != nil {
		return nil, err
	}
	folders, err := s.o.Groups.AllFolders(ctx)
	if err != nil {
		return nil, err
	}
	userGroups, err := s.o.Groups.AllUserGroups(ctx)
	if err != nil {
		return nil, err
	}
	deviceGroups, err := s.o.Groups.AllDeviceGroups(ctx)
	if err != nil {
		return nil, err
	}
	strategies, err := allStrategies(ctx, db)
	if err != nil {
		return nil, err
	}
	addressBooks, err := allAddressBooks(ctx, db)
	if err != nil {
		return nil, err
	}
	var goServer any
	if gs := s.goServerData(ctx); gs != nil {
		goServer = gs
	}
	return goapi.NewObject(
		"_format", FormatName,
		"_version", float64(FormatVersion),
		"_created", timestamp,
		"_console_version", s.o.AppVersion,
		"_backend", "neodesk",
		"console", goapi.NewObject(
			"settings", settings,
			"branding", brand,
			"users", users,
			"folders", folders,
			"userGroups", userGroups,
			"deviceGroups", deviceGroups,
			"strategies", strategies,
			"addressBooks", addressBooks,
		),
		"goServer", goServer,
	), nil
}

// SnapshotJSON is JSON.stringify(snapshot, null, 2).
func SnapshotJSON(snapshot *goapi.Object) ([]byte, error) { return indentJSON(snapshot) }

// passwordMembers are the settings rows that hold a password in clear:
// JSON text of an object, by key, with the member that is the password.
// The mail settings (package mail).
var passwordMembers = map[string]string{
	"smtp_config": "pass",
}

// shareLoginSetting is the scheduled backup's network share login of
// older consoles, with its password in clear. The console has no such
// login any more (DECISIONS A155) and a migration deletes the setting
// (BUGS L123); a restore of an older backup or settings file leaves it out.
const shareLoginSetting = "scheduled_backup_smb"

// dropPasswords takes the password out of each setting of a snapshot
// that holds one. A snapshot is a file people download and keep; the
// password stays in the database, and a restore keeps it (keepPassword).
// A value that is no JSON object stays as it is.
func dropPasswords(settings *goapi.Object) {
	for key, member := range passwordMembers {
		text, isText := settings.Get(key).(string)
		if !isText {
			continue
		}
		config, ok := settingObject(text)
		if !ok || !config.Has(member) {
			continue
		}
		without := branding.NewObject()
		for _, k := range config.Keys() {
			if k != member {
				value, _ := config.Get(k)
				without.Set(k, value)
			}
		}
		settings.Set(key, branding.Stringify(without))
	}
}

// keepPassword gives a setting from a snapshot that holds a password but
// brings none the password stored now, so restoring a snapshot does not
// clear it. A setting that brings a password (a snapshot of an older
// console) is restored as it is, as is every other setting.
func keepPassword(ctx context.Context, db *store.DB, key string, value any) (any, error) {
	member, holdsOne := passwordMembers[key]
	text, isText := value.(string)
	if !holdsOne || !isText {
		return value, nil
	}
	config, ok := settingObject(text)
	if !ok || config.Has(member) {
		return value, nil
	}
	var stored sql.NullString
	err := db.QueryRow(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !stored.Valid) {
		return value, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load the stored setting %s: %w", key, err)
	}
	current, ok := settingObject(stored.String)
	if !ok {
		return value, nil
	}
	password, has := current.Get(member)
	if !has {
		return value, nil
	}
	config.Set(member, password)
	return branding.Stringify(config), nil
}

// settingObject parses the text of a setting; ok is false for text that
// is no JSON object.
func settingObject(text string) (*branding.Object, bool) {
	parsed, err := branding.ParseJSON([]byte(text))
	if err != nil {
		return nil, false
	}
	o, ok := parsed.(*branding.Object)
	return o, ok && o != nil
}

// ValidateBackup checks a parsed snapshot (validateBackup).
func ValidateBackup(data any) (valid bool, errs []string) {
	if !truthy(data) || !isObject(data) {
		return false, []string{"Invalid backup file: not a JSON object"}
	}
	if prop(data, "_format") != FormatName {
		errs = append(errs, "Invalid backup file: missing or wrong _format field")
	}
	version := prop(data, "_version")
	if n, isNumber := version.(float64); !isNumber || n > FormatVersion {
		errs = append(errs, "Unsupported backup version: "+jsString(version)+" (max supported: 1)")
	}
	if c := prop(data, "console"); !truthy(c) || !isObject(c) {
		errs = append(errs, "Invalid backup file: missing console section")
	}
	return len(errs) == 0, errs
}

// isObject is typeof v === 'object' for a value that is not null.
func isObject(v any) bool {
	switch v.(type) {
	case *branding.Object, []any:
		return true
	}
	return false
}

// SnapshotOptions say which parts of a snapshot RestoreBackup puts back.
type SnapshotOptions struct {
	Settings     bool // route default on
	Branding     bool // on
	Users        bool // off: it replaces every account
	Folders      bool // on
	Groups       bool // on
	AddressBooks bool // on
}

// RestoreBackup puts a validated snapshot back (restoreBackup). Each part
// runs on its own; a part that fails stops where it failed and leaves a
// warning:
//
//   - settings: every key is written as it is in the snapshot, the mail
//     settings with the stored password when they bring none; an older
//     snapshot's network share login (shareLoginSetting) and the setup
//     guide's rows (setupguide.OwnsSetting), which belong to the install
//     the file came from, are left out;
//   - branding: through the branding service, which cleans the values;
//   - users (only when asked, and only a non-empty list): every account is
//     replaced (restoreUsers), the sign-in tokens of the accounts whose
//     credentials it replaced end, and the list of who has a picture is
//     dropped;
//   - folders: those whose name is not taken yet are added;
//   - groups: user groups, device groups and rules whose guid is not taken
//     yet are added, failures of single ones are ignored;
//   - address books: saved for the account the row names by user_id, or
//     by username in a snapshot made on PostgreSQL; a row for an account
//     that does not exist is skipped.
func (s *Service) RestoreBackup(ctx context.Context, data any, o SnapshotOptions) RestoreResult {
	res := RestoreResult{Restored: []string{}, Skipped: []string{}, Warnings: []string{}}
	db := s.o.Store
	cons := prop(data, "console")

	if settings := prop(cons, "settings"); o.Settings && truthy(settings) {
		err := func() error {
			for _, e := range settingEntries(settings) {
				if e.key == shareLoginSetting || setupguide.OwnsSetting(e.key) {
					continue
				}
				value, err := keepPassword(ctx, db, e.key, e.value)
				if err != nil {
					return err
				}
				if err := setSetting(ctx, db, e.key, value); err != nil {
					return err
				}
			}
			return nil
		}()
		if err != nil {
			res.Warnings = append(res.Warnings, "Settings restore failed: "+driverMessage(err))
		} else {
			res.Restored = append(res.Restored, "settings")
		}
	} else {
		res.Skipped = append(res.Skipped, "settings")
	}

	if brand := prop(cons, "branding"); o.Branding && truthy(brand) && s.o.Branding != nil {
		if err := s.o.Branding.SaveBranding(ctx, brand); err != nil {
			res.Warnings = append(res.Warnings, "Branding restore failed: "+driverMessage(err))
		} else {
			res.Restored = append(res.Restored, "branding")
		}
	} else {
		res.Skipped = append(res.Skipped, "branding")
	}

	if users, isList := prop(cons, "users").([]any); o.Users && isList && len(users) > 0 {
		if err := restoreUsers(ctx, db, users, s.o.Now); err != nil {
			res.Warnings = append(res.Warnings, "Users restore failed: "+driverMessage(err))
		} else {
			res.Restored = append(res.Restored, "users")
			s.forgetPictures()
		}
	} else {
		res.Skipped = append(res.Skipped, "users")
	}

	if folders, isList := prop(cons, "folders").([]any); o.Folders && isList {
		if err := s.restoreFolders(ctx, folders); err != nil {
			res.Warnings = append(res.Warnings, "Folders restore failed: "+driverMessage(err))
		} else {
			res.Restored = append(res.Restored, "folders")
		}
	} else {
		res.Skipped = append(res.Skipped, "folders")
	}

	if o.Groups {
		if err := s.restoreGroups(ctx, cons); err != nil {
			res.Warnings = append(res.Warnings, "Groups restore failed: "+driverMessage(err))
		} else {
			res.Restored = append(res.Restored, "groups")
		}
	} else {
		res.Skipped = append(res.Skipped, "groups")
	}

	if books, isList := prop(cons, "addressBooks").([]any); o.AddressBooks && isList {
		err := func() error {
			for _, ab := range books {
				userID, err := readProp(ab, "user_id")
				if err != nil {
					return err
				}
				abType := prop(ab, "ab_type")
				if !truthy(abType) {
					continue
				}
				data := orValue(prop(ab, "data"), "{}")
				switch username := prop(ab, "username"); {
				case truthy(userID):
					err = saveAddressBook(ctx, db, userID, abType, data)
				case truthy(username):
					// a snapshot made on PostgreSQL names the account
					err = saveAddressBookOf(ctx, db, username, abType, data)
				default:
					continue
				}
				if err != nil {
					return err
				}
			}
			return nil
		}()
		if err != nil {
			res.Warnings = append(res.Warnings, "Address books restore failed: "+driverMessage(err))
		} else {
			res.Restored = append(res.Restored, "addressBooks")
		}
	} else {
		res.Skipped = append(res.Skipped, "addressBooks")
	}
	return res
}

// settingEntries is Object.entries(settings): the characters of a string
// count as entries too, each as its own UTF-16 unit.
func settingEntries(v any) []entry {
	if s, ok := v.(string); ok {
		var out []entry
		i := 0
		for _, r := range s {
			if r >= 0x10000 {
				out = append(out, entry{itoa(i), "\ufffd"}, entry{itoa(i + 1), "\ufffd"})
				i += 2
				continue
			}
			out = append(out, entry{itoa(i), string(r)})
			i++
		}
		return out
	}
	return entriesOf(v)
}

func itoa(i int) string { return strconv.Itoa(i) }

// setKey is a value as a key of a Set: text, numbers, booleans and null
// compare by value; arrays and objects never equal a stored value.
func setKey(v any) (any, bool) {
	switch v.(type) {
	case nil, string, float64, bool:
		return v, true
	}
	return nil, false
}

func inSet(set map[any]bool, v any) bool {
	k, ok := setKey(v)
	return ok && set[k]
}

func (s *Service) restoreFolders(ctx context.Context, folders []any) error {
	existing, err := s.o.Groups.AllFolders(ctx)
	if err != nil {
		return err
	}
	names := map[any]bool{}
	for _, f := range existing {
		if k, ok := setKey(devicegroupsPlain(f.Get("name"))); ok {
			names[k] = true
		}
	}
	for _, f := range folders {
		name, err := readProp(f, "name")
		if err != nil {
			return err
		}
		if inSet(names, name) {
			continue
		}
		if err := createFolder(ctx, s.o.Store, name, prop(f, "color"), prop(f, "icon")); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) restoreGroups(ctx context.Context, cons any) error {
	db := s.o.Store
	guidsOf := func(rows []*goapi.Object) map[any]bool {
		set := map[any]bool{}
		for _, r := range rows {
			if k, ok := setKey(devicegroupsPlain(r.Get("guid"))); ok {
				set[k] = true
			}
		}
		return set
	}
	if groups, ok := prop(cons, "userGroups").([]any); ok {
		existing, err := s.o.Groups.AllUserGroups(ctx)
		if err != nil {
			return err
		}
		have := guidsOf(existing)
		for _, g := range groups {
			guid, err := readProp(g, "guid")
			if err != nil {
				return err
			}
			if truthy(guid) && !inSet(have, guid) {
				_ = createUserGroup(ctx, db, guid, prop(g, "name"), orValue(prop(g, "note"), ""))
			}
		}
	}
	if groups, ok := prop(cons, "deviceGroups").([]any); ok {
		existing, err := s.o.Groups.AllDeviceGroups(ctx)
		if err != nil {
			return err
		}
		have := guidsOf(existing)
		for _, g := range groups {
			guid, err := readProp(g, "guid")
			if err != nil {
				return err
			}
			if truthy(guid) && !inSet(have, guid) {
				_ = createDeviceGroup(ctx, db, guid, prop(g, "name"), orValue(prop(g, "note"), ""))
			}
		}
	}
	if rules, ok := prop(cons, "strategies").([]any); ok {
		existing, err := allStrategies(ctx, db)
		if err != nil {
			return err
		}
		have := guidsOf(existing)
		for _, r := range rules {
			guid, err := readProp(r, "guid")
			if err != nil {
				return err
			}
			if !truthy(guid) || inSet(have, guid) {
				continue
			}
			enabled := prop(r, "enabled")
			if enabled == branding.Undefined {
				enabled = float64(1)
			}
			_ = createStrategy(ctx, db, guid, prop(r, "name"), prop(r, "user_group_guid"), prop(r, "device_group_guid"), enabled, prop(r, "permissions"))
		}
	}
	return nil
}

// devicegroupsPlain turns a row value into the plain value Set.has
// compared with: text stays text, numbers are float64.
func devicegroupsPlain(v any) any {
	raw, err := goapi.FromGo(v)
	if err != nil {
		return nil
	}
	return raw
}
