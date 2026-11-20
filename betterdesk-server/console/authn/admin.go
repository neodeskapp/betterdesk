package authn

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/store"
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
	var dirs []string
	add := func(dir string) {
		if dir != "" && !slices.Contains(dirs, dir) {
			dirs = append(dirs, dir)
		}
	}
	add(s.set.KeysPath)
	add("/opt/rustdesk")
	add(filepath.Join(s.set.KeysPath, "data"))
	add(s.set.DataDir)
	add("/opt/betterdesk")
	add("/opt/betterdesk/data")
	add("/opt/rustdesk/data")
	if s.windows {
		add(`C:\BetterDesk`)
		add(`C:\BetterDesk\data`)
		add(`C:\RustDesk`)
		add(`C:\RustDesk\data`)
	}
	if s.fs.Exists("/.dockerenv") || s.set.Docker == "true" {
		add("/app/data")
	}
	return dirs
}

// ReadAdminCredentialsFile returns the admin password from the first
// .admin_credentials file that has one, or "". The folders, in order:
// KeysPath, /opt/rustdesk, <KeysPath>/data, DataDir, /opt/betterdesk,
// /opt/betterdesk/data, /opt/rustdesk/data, on Windows C:\BetterDesk,
// C:\BetterDesk\data, C:\RustDesk and C:\RustDesk\data, and in Docker
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
// file.
//
// When there are users it creates nothing. If the password is known and
// an account of that name exists, its hash is overwritten only when an
// installer asked for it (checkForcePasswordUpdate).
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
		"Generated by: BetterDesk Console (Node.js)\n" +
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
