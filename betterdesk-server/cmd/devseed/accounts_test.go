package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/console/authn"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

func TestAccountsMakesOneAccountPerRoleAndTheDeviceTokens(t *testing.T) {
	storetest.Each(t, func(t *testing.T, db *store.DB) {
		storetest.Prepare(t, db, storetest.GoServerFirst)
		markStack(t, db)
		ctx := context.Background()
		// The first account gets the id 1, the owner of the tokens
		for _, name := range []string{"admin", "e2e_viewer"} {
			if _, err := db.Exec(ctx, `INSERT INTO users (username, password_hash, role) VALUES (?, 'old-hash', 'viewer')`, name); err != nil {
				t.Fatal(err)
			}
		}
		// A token of the admin with the stack's UUID from before: issuing the
		// new one revokes it
		if _, err := db.Exec(ctx, `INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at, ip_address)
			VALUES ('old', 'old', 1, '900100001', 'bd-stack-device-token', '2099-01-01 00:00:00.000', '')`); err != nil {
			t.Fatal(err)
		}
		dir := t.TempDir()
		passwordFile := filepath.Join(dir, "e2e_password")
		if err := os.WriteFile(passwordFile, []byte("E2e-Secret-1!\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		args := []string{"accounts", "--go-db", testDSN(t, db), "--now", "1795083630456", passwordFile}
		if err := run(ctx, args, func(string) string { return "" }, &out, io.Discard); err != nil {
			t.Fatal(err)
		}

		svc, err := authn.New(authn.Options{DB: db, GoAPI: goapi.New(http.NotFoundHandler())})
		if err != nil {
			t.Fatal(err)
		}
		for _, a := range roleAccounts {
			acc, err := svc.UserByUsername(ctx, a.Username)
			if err != nil || acc == nil {
				t.Fatalf("%s: %v %v", a.Username, acc, err)
			}
			if a.Username == "e2e_viewer" {
				if acc.PasswordHash.String != "old-hash" {
					t.Error("an existing account got a new password")
				}
				continue
			}
			if acc.Role.String != a.Role || acc.AuthProvider.String != "local" {
				t.Errorf("%s: role %q, provider %q", a.Username, acc.Role.String, acc.AuthProvider.String)
			}
			// The password from the file, without the line break
			if ok, err := svc.VerifyPassword(ctx, "E2e-Secret-1!", acc.PasswordHash.String); err != nil || !ok {
				t.Errorf("%s: the password does not sign in (%v)", a.Username, err)
			}
		}
		if strings.Count(out.String(), "created e2e_") != len(roleAccounts)-1 {
			t.Errorf("output:\n%s", out.String())
		}

		hex64 := regexp.MustCompile(`^[0-9a-f]{64}$`)
		for _, k := range deviceTokens {
			data, err := os.ReadFile(filepath.Join(dir, k.File))
			if err != nil {
				t.Fatal(err)
			}
			token := string(data)
			if !hex64.MatchString(token) {
				t.Fatalf("%s is not 64 hex characters", k.File)
			}
			if strings.Contains(out.String(), token) {
				t.Errorf("the token %s was printed", k.File)
			}
			if info, err := os.Stat(filepath.Join(dir, k.File)); err == nil && info.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
				t.Errorf("%s has mode %v", k.File, info.Mode().Perm())
			}
			found, err := svc.ValidateAccessToken(ctx, token)
			if err != nil || found == nil {
				t.Fatalf("%s does not validate: %v", k.File, err)
			}
			var owner int64
			var device, uuid string
			var expires store.Time
			if err := store.SelectRow(ctx, db, `SELECT user_id, client_id, client_uuid, expires_at FROM access_tokens WHERE token = ?`, token).
				Scan(&owner, &device, &uuid, &expires); err != nil {
				t.Fatal(err)
			}
			at, _ := expires.Instant()
			if owner != 1 || device != k.DeviceID || uuid != k.UUID || !at.Equal(time.UnixMilli(1795083630456).Add(7*24*time.Hour)) {
				t.Errorf("%s: owner %d, device %s, uuid %s, expires %v", k.File, owner, device, uuid, at)
			}
		}
		var revoked store.Bool
		if err := store.SelectRow(ctx, db, `SELECT revoked FROM access_tokens WHERE token = 'old'`).Scan(&revoked); err != nil {
			t.Fatal(err)
		}
		if !revoked.True() {
			t.Error("the older token with the same UUID was not revoked")
		}

		// A second run keeps the files and makes nothing new
		before, _ := os.ReadFile(filepath.Join(dir, "device_token"))
		out.Reset()
		if err := run(ctx, args, func(string) string { return "" }, &out, io.Discard); err != nil {
			t.Fatal(err)
		}
		after, _ := os.ReadFile(filepath.Join(dir, "device_token"))
		if out.Len() != 0 || !bytes.Equal(before, after) {
			t.Errorf("a second run changed something: %q", out.String())
		}
	})
}

func TestAccountsNeedTheTokenOwner(t *testing.T) {
	db := storetest.SQLite(t)
	storetest.Prepare(t, db, storetest.GoServerFirst)
	markStack(t, db)
	dir := t.TempDir()
	passwordFile := filepath.Join(dir, "e2e_password")
	if err := os.WriteFile(passwordFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := run(context.Background(), []string{"accounts", "--device-tokens-only", "--go-db", testDSN(t, db), passwordFile},
		func(k string) string { return map[string]string{"DEFAULT_ADMIN_USERNAME": "boss"}[k] }, io.Discard, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "no account boss") {
		t.Fatalf("tokens without an owner: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "device_token")); err == nil {
		t.Error("a token file was written")
	}
}
