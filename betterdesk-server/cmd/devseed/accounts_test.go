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
		// An earlier device token of 900100001 and an earlier operator token
		// of the admin: the new tokens revoke them
		for _, stmt := range []string{
			`INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at, ip_address, token_kind)
				VALUES ('old-device', 'old-device', 1, '900100001', '', '2099-01-01 00:00:00.000', '', 'device')`,
			`INSERT INTO access_tokens (token, token_hash, user_id, client_id, client_uuid, expires_at, ip_address, token_kind)
				VALUES ('old-operator', 'old-operator', 1, '', 'bd-stack-operator-token', '2099-01-01 00:00:00.000', '', 'account')`,
		} {
			if _, err := db.Exec(ctx, stmt); err != nil {
				t.Fatal(err)
			}
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
		// row reads a token's row by its hash, the only form it is kept in
		row := func(file, token string) (owner int64, client, uuid, kind string, at time.Time) {
			t.Helper()
			var expires store.Time
			if err := store.SelectRow(ctx, db, `SELECT user_id, client_id, client_uuid, token_kind, expires_at FROM access_tokens WHERE token_hash = ?`,
				authn.HashAccessToken(token)).Scan(&owner, &client, &uuid, &kind, &expires); err != nil {
				t.Fatalf("%s: %v", file, err)
			}
			var plain int
			if err := db.QueryRow(ctx, `SELECT COUNT(*) FROM access_tokens WHERE token = ?`, token).Scan(&plain); err != nil || plain != 0 {
				t.Errorf("%s: %d rows keep the token itself (%v)", file, plain, err)
			}
			at, _ = expires.Instant()
			return owner, client, uuid, kind, at
		}
		read := func(file string) string {
			t.Helper()
			data, err := os.ReadFile(filepath.Join(dir, file))
			if err != nil {
				t.Fatal(err)
			}
			token := string(data)
			if !hex64.MatchString(token) {
				t.Fatalf("%s is not 64 hex characters", file)
			}
			if strings.Contains(out.String(), token) {
				t.Errorf("the token %s was printed", file)
			}
			if info, err := os.Stat(filepath.Join(dir, file)); err == nil && info.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
				t.Errorf("%s has mode %v", file, info.Mode().Perm())
			}
			return token
		}
		week := time.UnixMilli(1795083630456).Add(7 * 24 * time.Hour)
		for _, k := range deviceTokens {
			token := read(k.File)
			device, err := svc.RequireDeviceToken(ctx, "Bearer "+token)
			if err != nil || device.DeviceID != k.DeviceID {
				t.Errorf("%s is no device token of %s: %+v, %v", k.File, k.DeviceID, device, err)
			}
			if user, err := svc.ValidateAccessToken(ctx, token); user != nil || err != nil {
				t.Errorf("%s signs %+v in (%v)", k.File, user, err)
			}
			if owner, client, _, kind, at := row(k.File, token); owner != 1 || client != k.DeviceID || kind != authn.TokenKindDevice || !at.Equal(store.DeviceTokenExpiry()) {
				t.Errorf("%s: owner %d, device %s, kind %s, expires %v", k.File, owner, client, kind, at)
			}
		}
		operator := read(operatorTokenFile)
		if user, err := svc.ValidateAccessToken(ctx, operator); err != nil || user == nil || user.ID.V != 1 {
			t.Errorf("the operator token signs in %+v (%v), want the admin", user, err)
		}
		if _, err := svc.RequireDeviceToken(ctx, "Bearer "+operator); err == nil {
			t.Error("the operator token passes as a device token")
		}
		if owner, _, uuid, kind, at := row(operatorTokenFile, operator); owner != 1 || uuid != operatorTokenUUID || kind != authn.TokenKindAccount || !at.Equal(week) {
			t.Errorf("operator token: owner %d, uuid %s, kind %s, expires %v", owner, uuid, kind, at)
		}
		if strings.Count(out.String(), "created the ") != len(deviceTokens)+1 {
			t.Errorf("output:\n%s", out.String())
		}
		for _, old := range []string{"old-device", "old-operator"} {
			var revoked store.Bool
			if err := store.SelectRow(ctx, db, `SELECT revoked FROM access_tokens WHERE token_hash = ?`, old).Scan(&revoked); err != nil {
				t.Fatal(err)
			}
			if !revoked.True() {
				t.Errorf("the earlier token %s was not revoked", old)
			}
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
