package authn

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The first account. EnsureDefaultAdmin runs once at start.
//
// Files it reads and writes:
//
//   - .admin_credentials: where the password of the first account is
//     found when the environment does not give it, and where a made-up
//     password is written (mode 0600). Installers, the signal server and
//     docker/show-admin-credentials.sh read and write the same file. The
//     password is on the line "Admin Password: <password>".
//   - <DataDir>/.force_password_update: an installer puts it there to have
//     the admin's password overwritten at the next start. It is deleted
//     when seen.

const (
	credentialsFile = ".admin_credentials"
	forceFile       = ".force_password_update"
	// credentialsRemovedFile stays where a credentials file was removed
	// after the admin changed the first password; it holds no password.
	credentialsRemovedFile = ".admin_credentials.removed"
)

// retryDelays are the waits for the signal server's credentials file on
// an empty database.
var retryDelays = []time.Duration{2 * time.Second, 3 * time.Second, 5 * time.Second, 5 * time.Second, 10 * time.Second}

// ErrNoBootstrapAdmin is returned by EnsureDefaultAdmin in Docker when the
// signal server did not create the first account in time.
var ErrNoBootstrapAdmin = errors.New("Docker bootstrap admin was not created by the Go server before the timeout; " +
	"refusing to generate a second independent admin account")

// adminPasswordLine finds the first line "Admin Password: <value>" of a
// credentials file. Lines end at LF, CR, U+2028 or U+2029. The white space
// after the colon stays on the line, so a line without a value gives no
// password.
var adminPasswordLine = regexp.MustCompile(
	`(?:^|[\n\r\x{2028}\x{2029}])Admin Password:` +
		`[\t\v\f \x{a0}\x{1680}\x{2000}-\x{200a}\x{202f}\x{205f}\x{3000}\x{feff}]*` +
		`([^\n\r\x{2028}\x{2029}]+)`)

// credentialDirs lists the folders to look for .admin_credentials in, in
// order and without repeats.
func (s *Service) credentialDirs() []string {
	return credentialDirsFor(s.fs, s.set, s.windows)
}

func credentialDirsFor(fsys FS, set Settings, windows bool) []string {
	var dirs []string
	add := func(dir string) {
		if dir != "" && !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}
	add(set.KeysPath)
	add("/opt/rustdesk")
	add(filepath.Join(set.KeysPath, "data"))
	add(set.DataDir)
	add("/opt/neodesk")
	add("/opt/neodesk/data")
	add("/opt/rustdesk/data")
	if windows {
		add(`C:\NeoDesk`)
		add(`C:\NeoDesk\data`)
		add(`C:\RustDesk`)
		add(`C:\RustDesk\data`)
	}
	if fsys.Exists("/.dockerenv") || set.Docker == "true" {
		add("/app/data")
	}
	return dirs
}

// ReadAdminCredentialsFile returns the admin password from the first
// .admin_credentials file that has one, or "". The folders, in order:
// KeysPath, /opt/rustdesk, <KeysPath>/data, DataDir, /opt/neodesk,
// /opt/neodesk/data, /opt/rustdesk/data, on Windows C:\NeoDesk,
// C:\NeoDesk\data, C:\RustDesk and C:\RustDesk\data, and in Docker
// /app/data. A file that cannot be read is skipped.
func (s *Service) ReadAdminCredentialsFile() string {
	for _, dir := range s.credentialDirs() {
		path := filepath.Join(dir, credentialsFile)
		if !s.fs.Exists(path) {
			continue
		}
		content, err := s.fs.ReadFile(path)
		if err != nil {
			continue
		}
		match := adminPasswordLine.FindStringSubmatch(toWellFormed(string(content)))
		if match == nil {
			continue
		}
		if password := jsTrim(match[1]); password != "" {
			s.log.Info("[AUTH] Read admin password from " + path)
			return password
		}
	}
	return ""
}

// checkForcePasswordUpdate reports whether an installer asked for the
// admin's password to be overwritten: through the setting, which works
// once, or through the file, which is deleted.
func (s *Service) checkForcePasswordUpdate() bool {
	s.mu.Lock()
	fromSetting := s.set.ForcePasswordUpdate && !s.forceUsed
	if fromSetting {
		s.forceUsed = true
	}
	s.mu.Unlock()
	if fromSetting {
		s.log.Info("[AUTH] FORCE_PASSWORD_UPDATE env var detected — will force admin password update")
		return true
	}
	dir := s.set.DataDir
	if dir == "" {
		dir = "."
	}
	sentinel := filepath.Join(dir, forceFile)
	if s.fs.Exists(sentinel) {
		s.log.Info("[AUTH] .force_password_update sentinel file detected — will force admin password update")
		return s.fs.Remove(sentinel) == nil
	}
	return false
}

// dockerUsesGoOwnedAdminBootstrap reports whether the signal server makes
// the first account: in Docker, when both programs share one user table.
func (s *Service) dockerUsesGoOwnedAdminBootstrap() bool {
	if asciiLower(s.set.Docker) != "true" {
		return false
	}
	if dbType := asciiLower(s.set.DBType); dbType == "postgres" || dbType == "postgresql" {
		return true
	}
	switch asciiLower(s.set.SQLiteAuthDBMode) {
	case "legacy":
		return false
	case "consolidated":
		return true
	}
	legacy := s.set.AuthDBPath
	if legacy == "" {
		dir := s.set.DataDir
		if dir == "" {
			dir = "/app/data"
		}
		legacy = filepath.Join(dir, "auth.db")
	}
	return !s.fs.Exists(legacy)
}

// waitForDockerBootstrapAdmin waits, where the signal server makes the
// first account, until there is one. It reports whether it saw users.
func (s *Service) waitForDockerBootstrapAdmin(ctx context.Context) (bool, error) {
	if !s.dockerUsesGoOwnedAdminBootstrap() {
		return false, nil
	}
	if has, err := hasUsers(ctx, s.db); err != nil || has {
		return has, err
	}
	maxWait := s.set.DockerAdminBootstrapWait
	deadline := s.now().Add(maxWait)
	s.log.Info(fmt.Sprintf("[AUTH] Waiting for Go server to create the Docker bootstrap admin (up to %dms)...", maxWait.Milliseconds()))
	for s.now().Before(deadline) {
		if err := s.sleep(ctx, min(time.Second, deadline.Sub(s.now()))); err != nil {
			return false, err
		}
		if has, err := hasUsers(ctx, s.db); err != nil || has {
			return has, err
		}
	}
	return false, ErrNoBootstrapAdmin
}

// EnsureDefaultAdmin makes sure there is a first account. It reports
// whether it created one.
//
// The name is Settings.DefaultAdminUsername. The password is
// Settings.DefaultAdminPassword, else the one in a .admin_credentials
// file. The server makes the first account before the console starts,
// from INIT_ADMIN_PASS under the password rule (main.go), so this creates
// one only on a database the server did not set up.
//
// When there are users it creates nothing. If the password is known and
// an account of that name exists, its hash is overwritten only when an
// installer asked for it (checkForcePasswordUpdate) and the password keeps
// the password rule (auth.CheckNewPassword).
//
// When there are none it creates the account with the role "admin". With
// no password known it first waits for the signal server's credentials
// file (2, 3, 5, 5 and 10 seconds), then makes up a password of 32 hex
// characters and writes it to <KeysPath, else DataDir, else .>/.admin_credentials
// with mode 0600.
//
// In Docker with one shared user table the signal server makes the first
// account, and EnsureDefaultAdmin waits for it up to
// Settings.DockerAdminBootstrapWait. If none appears it returns
// ErrNoBootstrapAdmin.
func (s *Service) EnsureDefaultAdmin(ctx context.Context) (created bool, err error) {
	username := s.set.DefaultAdminUsername
	password := s.set.DefaultAdminPassword
	if password == "" {
		password = s.ReadAdminCredentialsFile()
	}
	force := s.checkForcePasswordUpdate()

	s.log.Info("[AUTH] ensureDefaultAdmin: checking for existing users...")

	exist, err := s.waitForDockerBootstrapAdmin(ctx)
	if err != nil {
		return false, err
	}
	if !exist {
		if exist, err = hasUsers(ctx, s.db); err != nil {
			return false, err
		}
	}
	if exist {
		return false, s.reviewExistingAdmin(ctx, username, password, force)
	}

	known := password != ""
	// credentialsPath is where a made-up password was written.
	var credentialsPath string
	if !known {
		for i, delay := range retryDelays {
			s.log.Info(fmt.Sprintf("[AUTH] No admin password found. Waiting for Go server (attempt %d/%d)...", i+1, len(retryDelays)))
			if err := s.sleep(ctx, delay); err != nil {
				return false, err
			}
			if password = s.ReadAdminCredentialsFile(); password != "" {
				s.log.Info(fmt.Sprintf("[AUTH] Found admin password from Go server on retry %d", i+1))
				known = true
				break
			}
		}
	}
	if !known {
		if password, err = s.randomHex(16); err != nil {
			return false, fmt.Errorf("admin password: %w", err)
		}
		credentialsPath = s.writeCredentials(username, password)
	}

	hash, err := s.HashPassword(ctx, password)
	if err != nil {
		return false, err
	}
	if err := createUser(ctx, s.db, username, hash, "admin", providerLocal); err != nil {
		return false, err
	}

	// Read the row back and try the password on it.
	stored, err := getUserByUsername(ctx, s.db, username)
	if err != nil {
		return false, err
	}
	if stored == nil {
		s.log.Error("[AUTH] CRITICAL: createUser succeeded but getUserByUsername returned null for default admin")
	} else {
		status, err := s.verifyAdminPasswordHash(ctx, stored, password)
		if err != nil {
			return false, err
		}
		switch status {
		case "verified":
			s.log.Info("[AUTH] Default admin user created and verified successfully")
		case "recovered":
			s.log.Info("[AUTH] Admin password self-test recovered after re-hash")
		case "failed":
			s.log.Error("[AUTH] CRITICAL: Admin password self-test STILL FAILING — password hashing may be broken")
		}
	}

	if known {
		s.log.Info("[AUTH] Default admin account created from configured credentials")
	} else {
		s.log.Info(fmt.Sprintf("[AUTH] Generated admin credentials written to %s — change password immediately",
			credentialsPath))
	}
	return true, nil
}

// reviewExistingAdmin is what EnsureDefaultAdmin does when there are
// users.
func (s *Service) reviewExistingAdmin(ctx context.Context, username, password string, force bool) error {
	if password == "" {
		s.log.Info("[AUTH] Users exist, no DEFAULT_ADMIN_PASSWORD set — skipping admin check")
		return nil
	}
	admin, err := getUserByUsername(ctx, s.db, username)
	if err != nil || admin == nil {
		return err
	}
	if force {
		if s.keepingRule(password) == "" {
			return nil
		}
		s.log.Info("[AUTH] Force password update requested — updating admin password")
		hash, err := s.HashPassword(ctx, password)
		if err != nil {
			return err
		}
		if err := updateUserPassword(ctx, s.db, admin.ID.V, hash); err != nil {
			return err
		}
		s.log.Info("[AUTH] Admin password hash force-updated to match DEFAULT_ADMIN_PASSWORD")
		return nil
	}
	hashType := "unknown"
	switch {
	case isPBKDF2Hash(admin.PasswordHash.String):
		hashType = "PBKDF2"
	case strings.HasPrefix(admin.PasswordHash.String, "$2"):
		hashType = "bcrypt"
	}
	s.log.Info(fmt.Sprintf("[AUTH] Default admin account exists (%s) — password unchanged", hashType))
	return nil
}

// adminUserLine finds the line "Admin Username: <value>" of a credentials
// file.
var adminUserLine = regexp.MustCompile(`(?m)^Admin Username:[ \t]*(\S+)`)

// removeFirstPassword removes the first password of the account username
// after the account changed its password (removeFirstPasswordIn). Failures
// are logged.
func (s *Service) removeFirstPassword(username string) {
	for _, f := range removeFirstPasswordIn(s.fs, s.credentialDirs(), username, s.now(), "after the account changed it") {
		if f.Unread {
			continue
		}
		if f.Err != nil {
			s.log.Warn(fmt.Sprintf("[AUTH] Could not remove the first password in %s: %s", f.Path, f.Err.Error()))
			continue
		}
		if f.NoteErr != nil {
			s.log.Warn(fmt.Sprintf("[AUTH] Could not leave a note in %s: %s", filepath.Dir(f.Path), f.NoteErr.Error()))
		}
		s.log.Info("[AUTH] Removed the first password in " + f.Path + " after " + username + " changed it")
	}
}

// FirstPasswordReset removes the first password of the account username
// after an administrator set its password in the console (the Users
// page): the password in the file signs nobody in any more. It looks
// where the account's own password change looks and leaves the same note.
// Failures are logged; the new password stays.
func (s *Service) FirstPasswordReset(username string) {
	for _, f := range removeFirstPasswordIn(s.fs, s.credentialDirs(), username, s.now(), "after a password reset") {
		if f.Unread {
			continue
		}
		if f.Err != nil {
			s.log.Warn(fmt.Sprintf("[AUTH] Could not remove the first password in %s: %s", f.Path, f.Err.Error()))
			continue
		}
		if f.NoteErr != nil {
			s.log.Warn(fmt.Sprintf("[AUTH] Could not leave a note in %s: %s", filepath.Dir(f.Path), f.NoteErr.Error()))
		}
		s.log.Info("[AUTH] Removed the first password in " + f.Path + " after a password reset of " + username)
	}
}

// FirstPasswordFile is a credentials file that held the first password of
// an account whose password was set again, or one that may hold it.
type FirstPasswordFile struct {
	// Path is the file.
	Path string
	// Err is why the file is still there; nil when it was removed.
	Err error
	// Unread says the file could not be read for lack of permission (Err
	// says so), so whose first password it holds is not known.
	Unread bool
	// NoteErr is why no note took the file's place; nil when one did.
	NoteErr error
}

// RemoveFirstPassword removes the credentials files that hold the first
// password of the account username, after its password was set again
// (the reset-password command): the password in them signs nobody in any
// more. It looks where the console looks (the folders of set, from
// SettingsFromEnv, and the install folders) and does what the console does
// after a password change. why ends the note ("after a password reset").
func RemoveFirstPassword(fsys FS, set Settings, username string, now time.Time, why string) []FirstPasswordFile {
	return removeFirstPasswordIn(fsys, credentialDirsFor(fsys, set, runtime.GOOS == "windows"), username, now, why)
}

// removeFirstPasswordIn removes the credentials files in dirs that hold
// the first password of the account username. A file without a user name
// line is the admin's. A note without the password takes each file's
// place, so the credentials command can say what happened; why ends it.
// It returns the files it found for the account, removed or not, and the
// ones it was not allowed to read.
func removeFirstPasswordIn(fsys FS, dirs []string, username string, now time.Time, why string) []FirstPasswordFile {
	var found []FirstPasswordFile
	for _, dir := range dirs {
		path := filepath.Join(dir, credentialsFile)
		// Read without asking whether the file exists first: in a folder the
		// process may not search, it does not seem to
		content, err := fsys.ReadFile(path)
		if errors.Is(err, fs.ErrPermission) {
			found = append(found, FirstPasswordFile{Path: path, Err: err, Unread: true})
			continue
		}
		if err != nil {
			continue
		}
		owner := "admin"
		if match := adminUserLine.FindStringSubmatch(string(content)); match != nil {
			owner = match[1]
		}
		if owner != username {
			continue
		}
		f := FirstPasswordFile{Path: path}
		if f.Err = fsys.Remove(path); f.Err == nil {
			note := "The first password of " + username + " was removed on " + store.ISOTime(now) + " " + why + ".\n"
			f.NoteErr = fsys.WriteFile(filepath.Join(dir, credentialsRemovedFile), []byte(note), 0o600)
		}
		found = append(found, f)
	}
	return found
}

// keepingRule is password when it keeps the password rule
// (auth.CheckNewPassword), else "" with an error in the log, so the admin
// account never gets a configured password that the rule refuses.
func (s *Service) keepingRule(password string) string {
	if password == "" {
		return ""
	}
	if err := auth.CheckNewPassword(password); err != nil {
		s.log.Error("[AUTH] The configured admin password is not used: " + err.Error())
		return ""
	}
	return password
}

// writeCredentials writes the made-up password where the user and the
// installers find it, and returns the path of the file. A failure is
// logged; the account is created anyway.
func (s *Service) writeCredentials(username, password string) string {
	dir := s.set.KeysPath
	if dir == "" {
		dir = s.set.DataDir
	}
	if dir == "" {
		dir = "."
	}
	path := filepath.Join(dir, credentialsFile)
	content := "Admin Username: " + username + "\n" +
		"Admin Password: " + password + "\n" +
		"Generated by: NeoDesk Console (Node.js)\n" +
		"Timestamp: " + store.ISOTime(s.now()) + "\n"
	if err := s.fs.WriteFile(path, []byte(content), 0o600); err != nil {
		s.log.Warn(fmt.Sprintf("[AUTH] Could not write .admin_credentials to %s: %s", path, err.Error()))
		return path
	}
	// A file that was there already keeps its mode through WriteFile, so
	// the mode is set afterwards. A file system without modes stays as it
	// is.
	_ = s.fs.Chmod(path, 0o600)
	s.log.Info("[AUTH] Wrote generated admin credentials to " + path + " (mode 0600)")
	return path
}

// verifyAdminPasswordHash tries the password on the stored hash of the new
// account. On a mismatch it hashes and stores once more and tries again.
// It answers "verified", "recovered" or "failed".
func (s *Service) verifyAdminPasswordHash(ctx context.Context, admin *Account, password string) (string, error) {
	if !admin.PasswordHash.Valid {
		return "", errNoHash
	}
	ok, err := s.VerifyPassword(ctx, password, admin.PasswordHash.String)
	if err != nil {
		return "", err
	}
	if ok {
		return "verified", nil
	}
	retry, err := s.HashPassword(ctx, password)
	if err != nil {
		return "", err
	}
	if err := updateUserPassword(ctx, s.db, admin.ID.V, retry); err != nil {
		return "", err
	}
	ok, err = s.VerifyPassword(ctx, password, retry)
	if err != nil {
		return "", err
	}
	if ok {
		return "recovered", nil
	}
	return "failed", nil
}
