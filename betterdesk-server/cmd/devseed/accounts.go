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

// deviceTokens are the device tokens: the file they go into, the device
// they are bound to and the client UUID. Each has its own UUID, because
// issuing a token revokes the account's tokens with the same UUID.
var deviceTokens = []struct{ File, DeviceID, UUID string }{
	{"device_token", "900100001", "bd-stack-device-token"},
	{"device_token_other", "900100002", "bd-stack-device-token-other"},
}

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
	return issueDeviceTokens(ctx, svc, owner, filepath.Dir(passwordFile), out)
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

// issueDeviceTokens issues the device tokens of the owner's account, as
// the console does when a desktop client signs in as an operator: 64 hex
// characters, stored with their SHA-256, valid for API_TOKEN_EXPIRY_DAYS
// (7) days. A token file that exists is kept. A token is written only to
// its file (mode 0600), never printed.
func issueDeviceTokens(ctx context.Context, svc *authn.Service, owner, dir string, out io.Writer) error {
	account, err := svc.UserByUsername(ctx, owner)
	if err != nil {
		return err
	}
	if account == nil {
		return fmt.Errorf("no account %s to own the device tokens", owner)
	}
	ownerID := account.ID.V
	for _, t := range deviceTokens {
		file := filepath.Join(dir, t.File)
		if _, err := os.Stat(file); err == nil {
			continue
		}
		token, err := svc.GenerateAccessToken(ctx, ownerID, t.DeviceID, t.UUID, "")
		if err != nil {
			return err
		}
		if err := os.WriteFile(file, []byte(token), 0o600); err != nil {
			return fmt.Errorf("write %s: %w", t.File, err)
		}
		if err := os.Chmod(file, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", t.File, err)
		}
		fmt.Fprintf(out, "created the device token %s for %s\n", t.File, t.DeviceID)
	}
	return nil
}
