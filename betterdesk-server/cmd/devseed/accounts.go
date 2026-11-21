package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/authn"
	"github.com/unitronix/betterdesk-server/console/goapi"
)

// roleAccounts are the test accounts, one per role.
var roleAccounts = []struct{ Username, Role string }{
	{"e2e_admin", "admin"},
	{"e2e_super_admin", "super_admin"},
	{"e2e_server_admin", "server_admin"},
	{"e2e_global_admin", "global_admin"},
	{"e2e_operator", "operator"},
	{"e2e_viewer", "viewer"},
	{"e2e_guest_issuer", "guest_issuer"},
	{"e2e_pro", "pro"},
}

// deviceTokens are the device tokens: the file they go into and the
// device they are issued for.
var deviceTokens = []struct{ File, DeviceID string }{
	{"device_token", "900100001"},
	{"device_token_other", "900100002"},
}

// The operator token: an account token of the owner for the operator
// routes of the device API, which refuse a device token. Its client UUID
// is its own, because a sign-in revokes the account's tokens with the
// same UUID.
const (
	operatorTokenFile = "operator_token"
	operatorTokenUUID = "bd-stack-operator-token"
)

func runAccounts(ctx context.Context, args []string, getenv func(string) string, out io.Writer) error {
	f, err := parseFlags(args, []string{"go-db", "now"}, []string{"device-tokens-only"})
	if err != nil {
		return err
	}
	if len(f.rest) != 1 {
		return errors.New("usage: devseed accounts [--device-tokens-only] <file with the password>")
	}
	passwordFile := f.rest[0]
	at, err := seedTime(f.get("now", ""))
	if err != nil {
		return err
	}
	clock := time.Now
	if f.get("now", "") != "" {
		clock = func() time.Time { return at }
	}
	db, err := openDatabase(ctx, databaseFrom(f.get("go-db", ""), getenv))
	if err != nil {
		return err
	}
	defer db.Close()
	if err := requireMarker(ctx, db); err != nil {
		return err
	}
	// The service hashes passwords and issues tokens; neither asks the
	// signal server, so its API client goes nowhere.
	svc, err := authn.New(authn.Options{
		DB:       db.DB,
		GoAPI:    goapi.New(http.NotFoundHandler()),
		Settings: authn.SettingsFromEnv(getenv, getenv("DATA_DIR"), ""),
		Now:      clock,
	})
	if err != nil {
		return err
	}
	if !f.switches["device-tokens-only"] {
		data, err := os.ReadFile(passwordFile)
		if err != nil {
			return fmt.Errorf("read the password: %w", err)
		}
		if err := makeAccounts(ctx, db, svc, strings.TrimSpace(string(data)), out); err != nil {
			return err
		}
	}
	owner := getenv("DEFAULT_ADMIN_USERNAME")
	if owner == "" {
		owner = "admin"
	}
	return issueTokens(ctx, svc, owner, filepath.Dir(passwordFile), out)
}

// makeAccounts creates the role accounts that are missing, with the row
// the console writes for a new local account.
func makeAccounts(ctx context.Context, db *database, svc *authn.Service, password string, out io.Writer) error {
	for _, a := range roleAccounts {
		existing, err := svc.UserByUsername(ctx, a.Username)
		if err != nil {
			return err
		}
		if existing != nil {
			continue
		}
		hash, err := svc.HashPassword(ctx, password)
		if err != nil {
			return fmt.Errorf("hash the password of %s: %w", a.Username, err)
		}
		if _, err := db.Exec(ctx, `INSERT INTO users (username, password_hash, role, auth_provider) VALUES (?, ?, ?, ?)`,
			a.Username, hash, a.Role, "local"); err != nil {
			return fmt.Errorf("create %s: %w", a.Username, err)
		}
		fmt.Fprintf(out, "created %s (%s)\n", a.Username, a.Role)
	}
	return nil
}

// issueTokens issues the tokens of the owner's account: the device
// tokens, as the console issues them for a device it admitted
// (IssueDeviceToken), and the operator token, as the console issues it when
// a desktop client signs in as an operator (GenerateSignInToken). Each is
// 64 hex characters, stored as its SHA-256 only and valid for
// API_TOKEN_EXPIRY_DAYS (7) days. A token file that exists is kept. A
// token is written only to its file (mode 0600), never printed.
func issueTokens(ctx context.Context, svc *authn.Service, owner, dir string, out io.Writer) error {
	account, err := svc.UserByUsername(ctx, owner)
	if err != nil {
		return err
	}
	if account == nil {
		return fmt.Errorf("no account %s to own the device tokens", owner)
	}
	ownerID := account.ID.V
	for _, t := range deviceTokens {
		issue := func() (string, error) { return svc.IssueDeviceToken(ctx, ownerID, t.DeviceID, "") }
		if err := writeToken(dir, t.File, issue, out, "the device token "+t.File+" for "+t.DeviceID); err != nil {
			return err
		}
	}
	issue := func() (string, error) { return svc.GenerateSignInToken(ctx, ownerID, "", operatorTokenUUID, "") }
	return writeToken(dir, operatorTokenFile, issue, out, "the operator token "+operatorTokenFile+" of "+owner)
}

// writeToken issues a token into the file name in dir unless the file
// exists, and says so on out as what.
func writeToken(dir, name string, issue func() (string, error), out io.Writer, what string) error {
	file := filepath.Join(dir, name)
	if _, err := os.Stat(file); err == nil {
		return nil
	}
	token, err := issue()
	if err != nil {
		return err
	}
	if err := os.WriteFile(file, []byte(token), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	if err := os.Chmod(file, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	fmt.Fprintf(out, "created %s\n", what)
	return nil
}
