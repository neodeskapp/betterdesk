//go:build nodeinterop

package authn

// Interop tests against the Node console's real auth service
// (testdata/nodeoracle/interop.js). Node runs as a child process on the
// test's database; what one side writes, the other must accept.
//
//	go test -tags nodeinterop ./console/authn/ -run Interop
//
// They need node on the PATH and the Node console at /repo/web-nodejs, as
// in the dev image.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/auth"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

type nodeProcess struct {
	t      *testing.T
	stdin  io.WriteCloser
	stdout *bufio.Scanner
}

type nodeAnswer struct {
	Result json.RawMessage `json:"result"`
	Thrown *string         `json:"thrown"`
}

// startNode runs the Node console's auth service on the database.
func startNode(t *testing.T, db *store.DB) *nodeProcess {
	t.Helper()
	cmd := exec.Command("node", "testdata/nodeoracle/interop.js")
	dir := t.TempDir()
	cmd.Env = append(os.Environ(), "NODE_ENV=production", "DATA_DIR="+dir, "KEYS_PATH="+dir,
		"SESSION_SECRET=interop-secret-5f2c9a7e41d04b6f8a3c1e9d7b5f2a60")
	if db.IsPostgres() {
		var schema string
		if err := db.QueryRow(context.Background(), `SELECT current_schema()`).Scan(&schema); err != nil {
			t.Fatal(err)
		}
		dsn := os.Getenv(storetest.PostgresEnv)
		sep := "?"
		if strings.Contains(dsn, "?") {
			sep = "&"
		}
		cmd.Env = append(cmd.Env, "DB_TYPE=postgres", "DATABASE_URL="+dsn+sep+"options="+url.QueryEscape("-c search_path="+schema))
	} else {
		var seq int
		var name, file string
		if err := db.QueryRow(context.Background(), `PRAGMA database_list`).Scan(&seq, &name, &file); err != nil {
			t.Fatal(err)
		}
		cmd.Env = append(cmd.Env, "DB_TYPE=sqlite", "DB_PATH="+file)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start node: %v", err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			_ = cmd.Process.Kill()
		}
		if t.Failed() && stderr.Len() > 0 {
			t.Logf("node stderr:\n%s", stderr.String())
		}
	})
	n := &nodeProcess{t: t, stdin: stdin, stdout: bufio.NewScanner(stdout)}
	n.stdout.Buffer(make([]byte, 1<<20), 1<<24)
	if !n.stdout.Scan() || !strings.Contains(n.stdout.Text(), "ready") {
		t.Fatalf("node did not start: %q\n%s", n.stdout.Text(), stderr.String())
	}
	return n
}

// call runs one function in Node. at fixes Node's clock when it is not
// the zero time.
func (n *nodeProcess) call(at time.Time, fn string, args ...any) nodeAnswer {
	n.t.Helper()
	request := map[string]any{"fn": fn, "args": args}
	if !at.IsZero() {
		request["now"] = at.UnixMilli()
	}
	line, err := json.Marshal(request)
	if err != nil {
		n.t.Fatal(err)
	}
	if _, err := n.stdin.Write(append(line, '\n')); err != nil {
		n.t.Fatalf("write to node: %v", err)
	}
	if !n.stdout.Scan() {
		n.t.Fatalf("node gave no answer to %s", fn)
	}
	var answer nodeAnswer
	if err := json.Unmarshal(n.stdout.Bytes(), &answer); err != nil {
		n.t.Fatalf("answer of node to %s: %v", fn, err)
	}
	return answer
}

// value is call for a function that must not throw.
func (n *nodeProcess) value(at time.Time, fn string, args ...any) string {
	n.t.Helper()
	answer := n.call(at, fn, args...)
	if answer.Thrown != nil {
		n.t.Fatalf("node %s threw %q", fn, *answer.Thrown)
	}
	return string(answer.Result)
}

// interopService is a Go service on the database. The Go server it would
// ask is down, as it is for the Node process.
func interopService(t *testing.T, db *store.DB) *Service {
	t.Helper()
	down := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })
	s, err := New(Options{DB: db, GoAPI: goapi.New(down), QR: func(text string) (string, error) { return "QR:" + text, nil }})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func interopPasswords() []string {
	list := []string{"", "a", "password", strings.Repeat("a", 71), strings.Repeat("a", 72), strings.Repeat("a", 73), strings.Repeat("a", 128),
		"abc\x00def", "\x00", "é", "é", "😀", strings.Repeat("😀", 18), strings.Repeat("€", 24), strings.Repeat("€", 25),
		strings.Repeat("a", 71) + "é", strings.Repeat("a", 70) + "€", "  pw  ", "tab\tpw\n", `"quoted"`, `back\slash`, strings.Repeat("x", 300)}
	gen := &mulberry{a: 99}
	alphabet := []rune("abcXYZ019 !$%äöüß€😀日本́")
	for i := 0; i < 280; i++ {
		var raw [2]byte
		_, _ = gen.Read(raw[:])
		length := int(raw[0]) % 90
		var b strings.Builder
		for j := 0; j < length; j++ {
			_, _ = gen.Read(raw[:1])
			b.WriteRune(alphabet[int(raw[0])%len(alphabet)])
		}
		list = append(list, b.String())
	}
	return list
}

// TestInteropHashes: what Go hashes, Node verifies.
func TestInteropHashes(t *testing.T) {
	db := storetest.Migrated(t)
	node := startNode(t, db)
	ctx := context.Background()
	fast := interopService(t, db)
	fast.hashIterations = testIterations
	real := interopService(t, db)

	passwords := interopPasswords()
	if len(passwords) < 300 {
		t.Fatalf("only %d passwords", len(passwords))
	}
	check := func(s *Service, password string) {
		t.Helper()
		hash, err := s.HashPassword(ctx, password)
		if err != nil {
			t.Fatalf("hash %q: %v", password, err)
		}
		if got := node.value(time.Time{}, "verifyPassword", password, hash); got != "true" {
			t.Errorf("Node does not accept Go's hash %s of %q: %s", hash, password, got)
		}
		wrong := password + "x"
		goSays, err := s.VerifyPassword(ctx, wrong, hash)
		if err != nil {
			t.Fatal(err)
		}
		if got := node.value(time.Time{}, "verifyPassword", wrong, hash); got != fmt.Sprint(goSays) {
			t.Errorf("hash %s with the longer password %q: Node says %s, Go says %v", hash, wrong, got, goSays)
		}
		if len(password) < 72 && goSays {
			t.Errorf("a longer password fits the hash of %q", password)
		}
	}
	for _, password := range passwords {
		check(fast, password)
	}
	for _, password := range []string{"admin", "correct horse battery staple", strings.Repeat("a", 100), "😀pwé", "abc\x00def", ""} {
		check(real, password)
	}
	hash, err := real.HashPassword(ctx, "form")
	if err != nil || !strings.HasPrefix(hash, "pbkdf2-sha256$600000$") || auth.NeedsRehash(hash) {
		t.Errorf("Go's hash %q, %v: want the API server's form (BUGS S12)", hash, err)
	}
}

// TestInteropTOTP: codes and secrets in both directions.
func TestInteropTOTP(t *testing.T) {
	db := storetest.Migrated(t)
	node := startNode(t, db)
	s := interopService(t, db)
	gen := &mulberry{a: 7}
	for i := 0; i < 60; i++ {
		secret, err := s.generateSecret()
		if err != nil {
			t.Fatal(err)
		}
		var raw [4]byte
		_, _ = gen.Read(raw[:])
		step := int64(50_000_000) + int64(raw[0])<<16 + int64(raw[1])<<8 + int64(raw[2])
		for _, at := range []time.Time{time.UnixMilli(step*30000 - 1), time.UnixMilli(step * 30000), time.UnixMilli(step*30000 + 29999), time.UnixMilli(step*30000 + int64(raw[3])*100)} {
			code, err := totpCode(secret, at)
			if err != nil {
				t.Fatal(err)
			}
			if got := node.value(at, "checkCode", secret, code); got != "true" {
				t.Errorf("Node refuses Go's code %s for %s at %d", code, secret, at.UnixMilli())
			}
			if got := node.value(at.Add(30*time.Second), "checkCode", secret, code); got != "false" {
				t.Errorf("Node accepts Go's code %s for %s one step late", code, secret)
			}
			var fromNode string
			if err := json.Unmarshal([]byte(node.value(at, "codeAt", secret)), &fromNode); err != nil {
				t.Fatal(err)
			}
			if ok, err := totpVerify(fromNode, secret, at); err != nil || !ok {
				t.Errorf("Go refuses Node's code %s for %s at %d: %v", fromNode, secret, at.UnixMilli(), err)
			}
			if ok, _ := totpVerify(fromNode, secret, at.Add(-30*time.Second)); ok {
				t.Errorf("Go accepts Node's code %s for %s one step early", fromNode, secret)
			}
		}
	}
}

// TestInteropSharedDatabase: Go and Node on one database, for each way the
// tables can have come about.
func TestInteropSharedDatabase(t *testing.T) {
	storetest.EachOrigin(t, func(t *testing.T, db *store.DB, origin storetest.Origin) {
		ctx := context.Background()
		node := startNode(t, db)
		s := interopService(t, db)
		now := time.Now()
		goServerPostgres := origin == storetest.GoServerFirst && db.IsPostgres()
		// On PostgreSQL the socket check fails as a whole while the signal
		// server's token table does not exist (BUGS.md AN9).
		noEnrollmentTable := origin != storetest.GoServerFirst && db.IsPostgres()
		socketCheck := func(device, token string, want bool) {
			t.Helper()
			got, err := s.VerifyDeviceWSAuth(ctx, device, token)
			fromNode := node.call(time.Time{}, "verifyDeviceWsAuth", device, token)
			if noEnrollmentTable {
				if err == nil || fromNode.Thrown == nil {
					t.Errorf("socket check without the token table: Go %v, %v; Node %+v; both should fail", got, err, fromNode)
				}
				return
			}
			if err != nil || got != want {
				t.Errorf("Go: device %s with %s = %v, %v; want %v", device, token, got, err, want)
			}
			if fromNode.Thrown != nil || string(fromNode.Result) != fmt.Sprint(want) {
				t.Errorf("Node: device %s with %s = %+v; want %v", device, token, fromNode, want)
			}
		}

		addUser := func(name, hash, role string) int64 {
			t.Helper()
			if err := createUser(ctx, db, name, hash, role, providerLocal); err != nil {
				t.Fatal(err)
			}
			return userID(t, db, name)
		}
		// sameSignIn signs in on both sides and wants the same answer, letter
		// for letter.
		sameSignIn := func(name, password string, wantUser bool) {
			t.Helper()
			fromNode := node.value(time.Time{}, "authenticate", name, password)
			outcome, err := s.Authenticate(ctx, name, password)
			if err != nil {
				t.Fatalf("Go Authenticate(%s): %v", name, err)
			}
			fromGo := "null"
			if outcome.User != nil {
				raw, err := json.Marshal(outcome.User)
				if err != nil {
					t.Fatal(err)
				}
				fromGo = string(raw)
			}
			if fromNode != fromGo {
				t.Errorf("sign-in of %s: Node answers %s, Go answers %s", name, fromNode, fromGo)
			}
			if wantUser != (outcome.User != nil) {
				t.Errorf("sign-in of %s: user = %v, want %v (%s)", name, outcome.User != nil, wantUser, fromGo)
			}
		}

		t.Run("passwords", func(t *testing.T) {
			goHash, err := s.HashPassword(ctx, "Go-made-1")
			if err != nil {
				t.Fatal(err)
			}
			addUser("gohash", goHash, "operator")
			var nodeHash string
			if err := json.Unmarshal([]byte(node.value(time.Time{}, "hashPassword", "Node-made-1")), &nodeHash); err != nil {
				t.Fatal(err)
			}
			addUser("nodehash", nodeHash, "viewer")
			sameSignIn("gohash", "Go-made-1", true)
			sameSignIn("gohash", "wrong", false)
			sameSignIn("nodehash", "Node-made-1", true)
			sameSignIn("nodehash", "wrong", false)
			sameSignIn("nobody", "x", false)

			// The signal server's own hash: Node replaces it at sign-in, and
			// Go signs in with what Node stored. Then the other way round:
			// Go keeps the hash (BUGS S12), and Node signs in with it.
			for _, name := range []string{"pbkdf2-node-first", "pbkdf2-go-first"} {
				serverHash, err := auth.HashPassword("Server-made-1")
				if err != nil {
					t.Fatal(err)
				}
				id := addUser(name, serverHash, "admin")
				if name == "pbkdf2-node-first" {
					if got := node.value(time.Time{}, "authenticate", name, "Server-made-1"); got == "null" {
						t.Fatalf("Node refuses the signal server's hash")
					}
				} else if outcome, err := s.Authenticate(ctx, name, "Server-made-1"); err != nil || outcome.User == nil {
					t.Fatalf("Go refuses the signal server's hash: %v", err)
				}
				stored, err := getUserByID(ctx, db, id)
				if err != nil {
					t.Fatal(err)
				}
				if replaced := stored.PasswordHash.String != serverHash; replaced != (name == "pbkdf2-node-first") {
					t.Errorf("%s: the hash is %q", name, stored.PasswordHash.String)
				}
				sameSignIn(name, "Server-made-1", true)
			}

			if err := s.ChangePassword(ctx, userID(t, db, "nodehash"), "Node-made-1", "Changed-by-Go-1"); err != nil {
				t.Fatal(err)
			}
			sameSignIn("nodehash", "Changed-by-Go-1", true)
			if got := node.value(time.Time{}, "changePassword", userID(t, db, "gohash"), "Go-made-1", "Changed-by-Node-1"); got != `{"success":true}` {
				t.Fatalf("Node changePassword: %s", got)
			}
			sameSignIn("gohash", "Changed-by-Node-1", true)
			sameSignIn("gohash", "Go-made-1", false)
		})

		t.Run("second factor", func(t *testing.T) {
			hash, _ := s.HashPassword(ctx, "pw")
			goSide := addUser("totp-go", hash, "admin")
			nodeSide := addUser("totp-node", hash, "admin")

			setup, err := s.GenerateTOTPSetup(ctx, goSide)
			if err != nil {
				t.Fatal(err)
			}
			code, _ := totpCode(setup.Secret, now)
			s.now = func() time.Time { return now }
			codes, err := s.VerifyAndEnableTOTP(ctx, goSide, code)
			if err != nil || len(codes) != 8 {
				t.Fatalf("enable in Go: %v, %v", codes, err)
			}
			if got := node.value(now, "verifyTotpCode", goSide, code); got != "true" {
				t.Errorf("Node refuses the code of a secret Go made: %s", got)
			}
			if got := node.value(now.Add(30*time.Second), "verifyTotpCode", goSide, code); got != "false" {
				t.Errorf("Node accepts the code one step late: %s", got)
			}
			sameSignIn("totp-go", "pw", true)
			// Go stores hashes of its recovery codes (BUGS S12), which Node
			// compared as plain text: Node takes none of them, Go each once.
			if got := node.value(now, "verifyRecoveryCode", goSide, codes[0]); got != "false" {
				t.Errorf("Node takes Go's hashed recovery code: %s", got)
			}
			if ok, err := s.VerifyRecoveryCode(ctx, goSide, codes[0]); err != nil || !ok {
				t.Errorf("Go refuses its own recovery code: %v, %v", ok, err)
			}
			if ok, err := s.VerifyRecoveryCode(ctx, goSide, codes[0]); err != nil || ok {
				t.Errorf("Go takes a recovery code twice: %v, %v", ok, err)
			}
			if ok, err := s.VerifyRecoveryCode(ctx, goSide, " "+strings.ToLower(codes[1])+"\n"); err != nil || !ok {
				t.Errorf("Go refuses its own recovery code in lower case: %v, %v", ok, err)
			}

			var nodeSetup struct {
				Secret     string `json:"secret"`
				OtpauthURL string `json:"otpauthUrl"`
			}
			if err := json.Unmarshal([]byte(node.value(now, "generateTotpSetup", nodeSide)), &nodeSetup); err != nil {
				t.Fatal(err)
			}
			if want := OtpauthURL("totp-node", nodeSetup.Secret); nodeSetup.OtpauthURL != want {
				t.Errorf("Node's address %q, Go builds %q", nodeSetup.OtpauthURL, want)
			}
			nodeCode, _ := totpCode(nodeSetup.Secret, now)
			var enabled struct {
				Success       bool     `json:"success"`
				RecoveryCodes []string `json:"recoveryCodes"`
			}
			if err := json.Unmarshal([]byte(node.value(now, "verifyAndEnableTotp", nodeSide, nodeCode)), &enabled); err != nil || !enabled.Success {
				t.Fatalf("enable in Node with Go's code: %v, %+v", err, enabled)
			}
			if ok, err := s.VerifyTOTPCode(ctx, nodeSide, nodeCode); err != nil || !ok {
				t.Errorf("Go refuses the code of a secret Node made: %v, %v", ok, err)
			}
			if ok, err := s.VerifyRecoveryCode(ctx, nodeSide, enabled.RecoveryCodes[3]); err != nil || !ok {
				t.Errorf("Go refuses Node's recovery code: %v, %v", ok, err)
			}
			if got := node.value(now, "verifyRecoveryCode", nodeSide, enabled.RecoveryCodes[3]); got != "false" {
				t.Errorf("Node accepts a recovery code Go used up: %s", got)
			}
			if enabledNow, err := s.IsTOTPEnabled(ctx, nodeSide); err != nil || !enabledNow {
				t.Errorf("Go does not see the second factor Node turned on: %v, %v", enabledNow, err)
			}

			// Turning it off fails on a table the signal server made on
			// PostgreSQL, where the secret cannot be NULL. Both sides fail.
			goErr := s.DisableTOTP(ctx, goSide)
			nodeOff := node.call(now, "disableTotp", nodeSide)
			if goServerPostgres {
				if goErr == nil || nodeOff.Thrown == nil {
					t.Errorf("disable on the signal server's table: Go %v, Node %+v; both should fail", goErr, nodeOff)
				}
				return
			}
			if goErr != nil || nodeOff.Thrown != nil {
				t.Fatalf("disable: Go %v, Node %+v", goErr, nodeOff)
			}
			if got := node.value(now, "isTotpEnabled", goSide); got != "false" {
				t.Errorf("Node still sees the second factor Go turned off: %s", got)
			}
			if on, _ := s.IsTOTPEnabled(ctx, nodeSide); on {
				t.Error("Go still sees the second factor Node turned off")
			}
		})

		t.Run("access tokens", func(t *testing.T) {
			hash, _ := s.HashPassword(ctx, "pw")
			id := addUser("token-user", hash, "operator")
			s.now = time.Now

			goToken, err := s.GenerateAccessToken(ctx, id, "device-go", "uuid-go", "198.51.100.1")
			if err != nil {
				t.Fatal(err)
			}
			fromNode := node.value(time.Time{}, "validateAccessToken", goToken)
			fromGo, err := s.ValidateAccessToken(ctx, goToken)
			if err != nil || fromGo == nil {
				t.Fatalf("Go refuses its own token: %v", err)
			}
			if raw, _ := json.Marshal(fromGo); fromNode != string(raw) {
				t.Errorf("token user: Node answers %s, Go answers %s", fromNode, raw)
			}
			if got := node.value(time.Time{}, "requireDeviceToken", "Bearer "+goToken); got != `{"next":true,"deviceId":"device-go"}` {
				t.Errorf("Node's device check of Go's token: %s", got)
			}
			socketCheck("device-go", goToken, true)

			var nodeToken string
			if err := json.Unmarshal([]byte(node.value(time.Time{}, "generateAccessToken", id, "device-node", "uuid-node", "198.51.100.2")), &nodeToken); err != nil {
				t.Fatal(err)
			}
			if user, err := s.ValidateAccessToken(ctx, nodeToken); err != nil || user == nil || user.ClientID.String != "device-node" {
				t.Errorf("Go refuses Node's token: %+v, %v", user, err)
			}
			if device, err := s.RequireDeviceToken(ctx, "Bearer "+nodeToken); err != nil || device.DeviceID != "device-node" {
				t.Errorf("Go's device check of Node's token: %+v, %v", device, err)
			}
			socketCheck("device-node", nodeToken, true)
			socketCheck("device-other", nodeToken, false)

			if err := s.RevokeClientTokens(ctx, id, "device-go", "uuid-go"); err != nil {
				t.Fatal(err)
			}
			if got := node.value(time.Time{}, "validateAccessToken", goToken); got != "null" {
				t.Errorf("Node accepts a token Go revoked: %s", got)
			}
			if got := node.value(time.Time{}, "revokeClientTokens", id, "", ""); got != "null" {
				t.Fatalf("Node revokeClientTokens: %s", got)
			}
			if user, _ := s.ValidateAccessToken(ctx, nodeToken); user != nil {
				t.Error("Go accepts a token Node revoked")
			}

			// A token issued eight days ago has run out for both.
			s.now = func() time.Time { return time.Now().Add(-8 * 24 * time.Hour) }
			old, err := s.GenerateAccessToken(ctx, id, "device-old", "uuid-old", "")
			s.now = time.Now
			if err != nil {
				t.Fatal(err)
			}
			if got := node.value(time.Time{}, "validateAccessToken", old); got != "null" {
				t.Errorf("Node accepts a token that ran out: %s", got)
			}
			if user, _ := s.ValidateAccessToken(ctx, old); user != nil {
				t.Error("Go accepts a token that ran out")
			}
			var nodeOld string
			if err := json.Unmarshal([]byte(node.value(time.Now().Add(-8*24*time.Hour), "generateAccessToken", id, "device-old2", "uuid-old2", "")), &nodeOld); err != nil {
				t.Fatal(err)
			}
			if user, _ := s.ValidateAccessToken(ctx, nodeOld); user != nil {
				t.Error("Go accepts a token of Node's that ran out")
			}
		})

		t.Run("lockouts", func(t *testing.T) {
			s.now = time.Now
			for i := 0; i < 10; i++ {
				if err := s.RecordAttempt(ctx, "locked-by-go", "198.51.100.77", false); err != nil {
					t.Fatal(err)
				}
			}
			var blocked struct {
				Blocked    bool   `json:"blocked"`
				Reason     string `json:"reason"`
				RetryAfter *int64 `json:"retryAfter"`
			}
			if err := json.Unmarshal([]byte(node.value(time.Time{}, "checkBruteForce", "locked-by-go", "203.0.113.5")), &blocked); err != nil {
				t.Fatal(err)
			}
			if !blocked.Blocked || blocked.Reason != reasonAccountLocked || blocked.RetryAfter == nil || *blocked.RetryAfter < 890 || *blocked.RetryAfter > 900 {
				t.Errorf("Node on an account Go locked: %+v", blocked)
			}
			if got := node.value(time.Time{}, "recordAttempt", "locked-by-go", "198.51.100.77", true); got != "null" {
				t.Fatal(got)
			}
			if check, err := s.CheckBruteForce(ctx, "locked-by-go", "203.0.113.5"); err != nil || check.Blocked {
				t.Errorf("Go still blocks an account Node unlocked: %+v, %v", check, err)
			}

			for i := 0; i < 10; i++ {
				node.value(time.Time{}, "recordAttempt", "locked-by-node", "198.51.100.78", false)
			}
			check, err := s.CheckBruteForce(ctx, "locked-by-node", "203.0.113.5")
			if err != nil || !check.Blocked || check.Reason != reasonAccountLocked || check.RetryUnknown || check.RetryAfter < 890 || check.RetryAfter > 900 {
				t.Errorf("Go on an account Node locked: %+v, %v", check, err)
			}
			fromNode := node.value(time.Time{}, "checkBruteForce", "locked-by-node", "203.0.113.5")
			if err := json.Unmarshal([]byte(fromNode), &blocked); err != nil || blocked.RetryAfter == nil || *blocked.RetryAfter < check.RetryAfter-3 || *blocked.RetryAfter > check.RetryAfter+3 {
				t.Errorf("remaining seconds: Node %s, Go %d", fromNode, check.RetryAfter)
			}
			// Twenty failures from one address in Go, ten more in Node: the
			// address is blocked for both.
			for i := 0; i < 20; i++ {
				if err := s.RecordAttempt(ctx, fmt.Sprintf("spray-%d", i), "198.51.100.79", false); err != nil {
					t.Fatal(err)
				}
			}
			for i := 0; i < 10; i++ {
				node.value(time.Time{}, "recordAttempt", fmt.Sprintf("spray-n%d", i), "198.51.100.79", false)
			}
			goCheck, err := s.CheckBruteForce(ctx, "fresh-name", "198.51.100.79")
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(goCheck)
			if got := node.value(time.Time{}, "checkBruteForce", "fresh-name", "198.51.100.79"); got != string(raw) || !goCheck.Blocked {
				t.Errorf("address limit: Node answers %s, Go answers %s", got, raw)
			}
		})

		t.Run("enrollment tokens", func(t *testing.T) {
			if origin != storetest.GoServerFirst {
				t.Skip("the table is the signal server's")
			}
			insert := `INSERT INTO device_tokens (token_hash, token_prefix, name, peer_id, status, max_uses, use_count, expires_at) VALUES (?, 'enroll-x', 'n', ?, ?, ?, ?, ` +
				db.Pick("strftime('%Y-%m-%dT%H:%M:%SZ', 'now', ?)", "NOW() + CAST(? AS INTERVAL)") + `)`
			add := func(token, peer, status string, max, used int, expires string) {
				t.Helper()
				if _, err := db.Exec(ctx, insert, HashDeviceToken(token), peer, status, max, used, expires); err != nil {
					t.Fatal(err)
				}
			}
			add("enroll-open-token", "", "pending", 1, 0, "+60 minutes")
			add("enroll-bound-token", "device-a", "active", 5, 1, "+60 minutes")
			add("enroll-spent-token", "", "active", 2, 2, "+60 minutes")
			add("enroll-late-token", "", "pending", 1, 0, "-60 minutes")
			add("enroll-revoked-token", "", "revoked", 1, 0, "+60 minutes")
			for _, c := range []struct {
				device, token string
				want          bool
			}{
				{"device-a", "enroll-open-token", true}, {"device-b", "enroll-open-token", true},
				{"device-a", "enroll-bound-token", true}, {"device-b", "enroll-bound-token", false},
				{"device-a", "enroll-spent-token", false}, {"device-a", "enroll-late-token", false},
				{"device-a", "enroll-revoked-token", false}, {"device-a", "enroll-unknown-token", false},
			} {
				socketCheck(c.device, c.token, c.want)
			}
		})
	})
}
