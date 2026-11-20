package authn

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/auth"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// The console and the API server share the users row. The API server's
// auth code is the one credential writer (BUGS S12, DECISIONS A64): a hash
// one side makes is one the other side keeps, and a recovery code works on
// both sides once.

// storedHash reads the password hash of an account.
func storedHash(t *testing.T, db *store.DB, id int64) string {
	t.Helper()
	user, err := getUserByID(context.Background(), db, id)
	if err != nil || user == nil {
		t.Fatalf("account %d: %v", id, err)
	}
	return user.PasswordHash.String
}

// TestPasswordHashStaysAcrossBothSignIns: the console hashes passwords as
// the API server does, so the API server's sign-in does not replace the
// console's hash, and the console's sign-in leaves the API server's hash
// as it is.
func TestPasswordHashStaysAcrossBothSignIns(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s, _, _ := testService(t, db, nil)
		s.hashIterations = auth.PasswordIterations

		// Made by the console: the API server takes it and keeps it
		id := addUser(t, s, "console-made", "Console-1!", "operator", "local")
		hash := storedHash(t, db, id)
		if !strings.HasPrefix(hash, "pbkdf2-sha256$600000$") || !auth.VerifyPassword(hash, "Console-1!") || auth.NeedsRehash(hash) {
			t.Errorf("the console's hash %q is not one the API server keeps", hash)
		}
		if outcome, err := s.Authenticate(ctx, "console-made", "Console-1!"); err != nil || outcome.User == nil {
			t.Fatalf("sign-in on the console's hash: %+v, %v", outcome, err)
		}
		if got := storedHash(t, db, id); got != hash {
			t.Errorf("the console's sign-in replaced its own hash: %q -> %q", hash, got)
		}

		// Made by the API server: the console's sign-in keeps it
		serverHash, err := auth.HashPassword("Server-1!")
		if err != nil {
			t.Fatal(err)
		}
		if err := createUser(ctx, db, "server-made", serverHash, "admin", providerLocal); err != nil {
			t.Fatal(err)
		}
		serverID := userID(t, db, "server-made")
		if outcome, err := s.Authenticate(ctx, "server-made", "Server-1!"); err != nil || outcome.User == nil {
			t.Fatalf("sign-in on the API server's hash: %+v, %v", outcome, err)
		}
		if got := storedHash(t, db, serverID); got != serverHash {
			t.Errorf("the console's sign-in replaced the API server's hash: %q -> %q", serverHash, got)
		}

		// A password change is the API server's form too
		if err := s.ChangePassword(ctx, serverID, "Server-1!", "Changed-2!"); err != nil {
			t.Fatal(err)
		}
		if got := storedHash(t, db, serverID); !auth.VerifyPassword(got, "Changed-2!") || auth.NeedsRehash(got) {
			t.Errorf("the changed password's hash %q is not one the API server keeps", got)
		}

		// A bcrypt hash an older console wrote stays until the API server
		// replaces it
		old, err := bcryptHash(ctx, strings.NewReader(strings.Repeat("s", 16)), "Old-4!", bcryptMinCost)
		if err != nil {
			t.Fatal(err)
		}
		if err := createUser(ctx, db, "bcrypt-made", old, "viewer", providerLocal); err != nil {
			t.Fatal(err)
		}
		if outcome, err := s.Authenticate(ctx, "bcrypt-made", "Old-4!"); err != nil || outcome.User == nil {
			t.Fatalf("sign-in on a bcrypt hash: %+v, %v", outcome, err)
		}
		if got := storedHash(t, db, userID(t, db, "bcrypt-made")); got != old || !auth.VerifyPassword(got, "Old-4!") {
			t.Errorf("the console's sign-in replaced a bcrypt hash: %q -> %q", old, got)
		}
	})
}

// userID reads the id of an account.
func userID(t *testing.T, db *store.DB, username string) int64 {
	t.Helper()
	user, err := getUserByUsername(context.Background(), db, username)
	if err != nil || user == nil {
		t.Fatalf("user %s: %v", username, err)
	}
	return user.ID.V
}

// TestRecoveryCodeWorksOnBothSides: the console stores hashes of its eight
// codes, which the API server's check takes; codes the API server made
// work on the console; and the codes an older console stored as they were
// shown keep working, on both sides, until they are used.
func TestRecoveryCodeWorksOnBothSides(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		s, _, _ := testService(t, db, nil)
		storedCodes := func(id int64) string {
			t.Helper()
			user, err := getUserByID(ctx, db, id)
			if err != nil || user == nil {
				t.Fatalf("account %d: %v", id, err)
			}
			return user.TOTPRecoveryCodes.String
		}
		withCodes := func(name, stored string) int64 {
			t.Helper()
			id := addUser(t, s, name, "pw", "admin", "local")
			mustExec(t, db, `UPDATE users SET totp_secret = 'GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ', totp_enabled = `+db.Pick("1", "TRUE")+`,
				totp_recovery_codes = ? WHERE id = ?`, stored, id)
			return id
		}

		// The console's codes: hashes the API server checks
		id := addUser(t, s, "console-codes", "pw", "admin", "local")
		setup, err := s.GenerateTOTPSetup(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		code, _ := totpCode(setup.Secret, s.now())
		codes, err := s.VerifyAndEnableTOTP(ctx, id, code)
		if err != nil || len(codes) != 8 {
			t.Fatalf("enable: %v, %v", codes, err)
		}
		stored := storedCodes(id)
		for _, c := range codes {
			if strings.Contains(stored, c) {
				t.Fatalf("the code %s is stored as it is: %s", c, stored)
			}
		}
		left, matched, err := auth.ConsumeRecoveryCode(stored, codes[0])
		if err != nil || !matched {
			t.Fatalf("the API server's check of the console's code: %v, %v", matched, err)
		}
		mustExec(t, db, `UPDATE users SET totp_recovery_codes = ? WHERE id = ?`, left, id)
		if ok, err := s.VerifyRecoveryCode(ctx, id, codes[0]); err != nil || ok {
			t.Errorf("the console takes a code the API server used up: %v, %v", ok, err)
		}
		if ok, err := s.VerifyRecoveryCode(ctx, id, " "+strings.ToLower(codes[1])+"\n"); err != nil || !ok {
			t.Errorf("the console's own code in lower case: %v, %v", ok, err)
		}
		if _, matched, _ := auth.ConsumeRecoveryCode(storedCodes(id), codes[1]); matched {
			t.Error("the API server takes a code the console used up")
		}

		// The API server's codes: the console checks them
		apiCodes, err := auth.GenerateRecoveryCodes()
		if err != nil {
			t.Fatal(err)
		}
		hashed, err := auth.HashRecoveryCodesCost(apiCodes, testRecoveryCost)
		if err != nil {
			t.Fatal(err)
		}
		apiID := withCodes("api-codes", hashed)
		if ok, err := s.VerifyRecoveryCode(ctx, apiID, apiCodes[3]); err != nil || !ok {
			t.Errorf("the console's check of the API server's code: %v, %v", ok, err)
		}
		if ok, _ := s.VerifyRecoveryCode(ctx, apiID, apiCodes[3]); ok {
			t.Error("a code works twice")
		}

		// Codes an older console stored as they were shown
		plainID := withCodes("plain-codes", `["3FA9-0C7E","AB12-CD34","77EE-0001"]`)
		if ok, err := s.VerifyRecoveryCode(ctx, plainID, "ab12-cd34"); err != nil || !ok {
			t.Errorf("the console's check of a stored plain code: %v, %v", ok, err)
		}
		var rest []string
		if err := json.Unmarshal([]byte(storedCodes(plainID)), &rest); err != nil || strings.Join(rest, ",") != "3FA9-0C7E,77EE-0001" {
			t.Errorf("after one plain code the list is %q (%v)", storedCodes(plainID), err)
		}
		left, matched, err = auth.ConsumeRecoveryCode(storedCodes(plainID), "3FA9-0C7E")
		if err != nil || !matched || left != `["77EE-0001"]` {
			t.Errorf("the API server's check of a stored plain code: %q, %v, %v", left, matched, err)
		}
		if ok, _ := s.VerifyRecoveryCode(ctx, plainID, "AB12-CD34"); ok {
			t.Error("a plain code works twice")
		}
	})
}
