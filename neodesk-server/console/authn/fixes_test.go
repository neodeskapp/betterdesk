package authn

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/console/auditlog"
)

// The bug fixes of BUGS.md that change what the recorded scenarios of
// testdata/vectors/db-*.json hold. Each fix rewrites a scenario's
// recording into what the console does now, before the scenario runs, and
// reports whether it changed anything; TestScenarios fails for a fix that
// changes no scenario.

// scenarioFix is one bug fix's rewrite.
type scenarioFix struct {
	bug     string
	rewrite func(sc *scenarioVector) bool
}

var scenarioFixes = []scenarioFix{
	{"S3", stopAtSyncsFromTheSignalServer},
	{"AN4", noPasswordFromTheNextLine},
	{"AN7", noCodesInOtherLists},
	{"AN8", strictSecrets},
	{"S12", oneCredentialWriter},
	{"UM21", stopAtAccountLockouts},
	{"AL4", maskNamesInSignInLogs},
	{"AN6", logTheWrittenCredentialsPath},
	{"RV117", stopAtOtherSpellings},
	{"RV118", ipv6AttemptsCountPerNetwork},
	{"RV122", codesWorkOnce},
	{"RV123", noPasswordsTheSignInRefuses},
	{"RV1", signInTokensProveNoDevice},
	{"RV128", tokensKeptAsTheirHash},
	{"L351", stopAtOtherServerNames},
}

// recordedLogins are the answers to /api/auth/login a step's stand-in of
// the signal server gives: status and body, the body's fields decoded.
func recordedLogins(step *stepVector) []struct {
	Status int
	Body   map[string]any
} {
	var out []struct {
		Status int
		Body   map[string]any
	}
	if step.Go == nil {
		return out
	}
	for _, raw := range step.Go.Login {
		var answer struct {
			Status int            `json:"status"`
			Body   map[string]any `json:"body"`
		}
		if json.Unmarshal(raw, &answer) == nil {
			out = append(out, struct {
				Status int
				Body   map[string]any
			}{answer.Status, answer.Body})
		}
	}
	return out
}

// stopAtFirstSignIn ends the recording before the first sign-in step whose
// stand-in answers match, and leaves the rows at the end uncompared.
func stopAtFirstSignIn(sc *scenarioVector, match func(step *stepVector) bool) bool {
	for i := range sc.Steps {
		step := &sc.Steps[i]
		if step.Fn == "authenticate" && match(step) {
			sc.Steps, sc.skipTables = sc.Steps[:i], true
			return true
		}
	}
	return false
}

// stopAtOtherServerNames: a directory sign-in is the account the signal
// server names in its answer (L351); Node kept the typed name and made a
// second account. The recordings' stand-in names every account
// "someone", so a recording ends before the first sign-in whose answer
// names another account than the one typed;
// TestDirectorySignInUsesTheServersAccountName holds the rule.
func stopAtOtherServerNames(sc *scenarioVector) bool {
	return stopAtFirstSignIn(sc, func(step *stepVector) bool {
		var typed string
		if len(step.Args) == 0 || json.Unmarshal(step.Args[0], &typed) != nil {
			return false
		}
		for _, answer := range recordedLogins(step) {
			if name, ok := answer.Body["username"].(string); ok && name != "" && name != strings.ToLower(strings.TrimSpace(typed)) {
				return true
			}
		}
		return false
	})
}

// fixScenario applies every fix to sc and counts the ones that changed it.
func fixScenario(sc *scenarioVector, changed map[string]int) {
	for _, fix := range scenarioFixes {
		if fix.rewrite(sc) {
			changed[fix.bug]++
		}
	}
}

// rewriteLogs runs edit over every log line of the scenario's steps.
func rewriteLogs(sc *scenarioVector, edit func(level, text string) string) bool {
	changed := false
	for i := range sc.Steps {
		for _, line := range sc.Steps[i].Logs {
			if len(line) != 2 {
				continue
			}
			if next := edit(line[0], line[1]); next != line[1] {
				line[1], changed = next, true
			}
		}
	}
	return changed
}

// signInLogName finds the user name in a log line of the sign-in.
var signInLogName = []*regexp.Regexp{
	regexp.MustCompile(`^(\[AUTH\] Synced ')(.*)(' from Go \(provider=.*)$`),
	regexp.MustCompile(`^(\[AUTH\] Login failed: ')(.*)(' is an SSO account \(password login not allowed\))$`),
	regexp.MustCompile(`^(\[AUTH\] Login failed: LDAP credentials rejected for ')(.*)(')$`),
	regexp.MustCompile(`^(\[AUTH\] Go server accepted LDAP login for ')(.*)(' — syncing local account)$`),
	regexp.MustCompile(`^(\[AUTH\] Verifying password for ')(.*)(' \(hash type: .*)$`),
	regexp.MustCompile(`^(\[AUTH\] Login blocked: username collision for local ')(.*)(' \(.*)$`),
	regexp.MustCompile(`^(\[AUTH\] Go server accepted password for local ')(.*)(' — syncing hash)$`),
	regexp.MustCompile(`^(\[AUTH\] Login failed: password mismatch for ')(.*)(' \(hash type: .*)$`),
	regexp.MustCompile(`^(\[AUTH\] Login successful for ')(.*)(')$`),
	regexp.MustCompile(`^(\[AUTH\] Migrated password hash from PBKDF2 to bcrypt for user: )(.*)()$`),
	regexp.MustCompile(`^(\[AUTH\] Go server accepted credentials for ')(.*)(' — provisioning local user \(.*)$`),
	regexp.MustCompile(`^(\[AUTH\] Login failed: user ')(.*)(' not found in database)$`),
}

// maskNamesInSignInLogs: the sign-in's log lines shorten the user name
// where they are built (AL4). Node printed it as it was.
func maskNamesInSignInLogs(sc *scenarioVector) bool {
	return rewriteLogs(sc, func(_, text string) string {
		for _, re := range signInLogName {
			if m := re.FindStringSubmatch(text); m != nil {
				return m[1] + auditlog.RedactUsername(m[2]) + m[3]
			}
		}
		return text
	})
}

// logTheWrittenCredentialsPath: the line about a made-up password names
// the file it was written to (AN6). Node named <DATA_DIR>/.admin_credentials
// also when the file went to KEYS_PATH; the step's own line about the write
// names the real path.
func logTheWrittenCredentialsPath(sc *scenarioVector) bool {
	const (
		generated = "[AUTH] Generated admin credentials written to "
		wrote     = "[AUTH] Wrote generated admin credentials to "
		failed    = "[AUTH] Could not write .admin_credentials to "
	)
	changed := false
	for i := range sc.Steps {
		path := ""
		for _, line := range sc.Steps[i].Logs {
			if len(line) != 2 {
				continue
			}
			switch text := line[1]; {
			case strings.HasPrefix(text, wrote):
				path = strings.TrimSuffix(strings.TrimPrefix(text, wrote), " (mode 0600)")
			case strings.HasPrefix(text, failed):
				rest := strings.TrimPrefix(text, failed)
				if cut := strings.Index(rest, ": "); cut >= 0 {
					path = rest[:cut]
				}
			case strings.HasPrefix(text, generated) && path != "":
				next := generated + path + " — change password immediately"
				if next != text {
					line[1], changed = next, true
				}
			}
		}
	}
	return changed
}

// topBitCleared reads bytes with their top bit cleared, as Node turned the
// random bytes of a two-factor secret into text first (AN1).
type topBitCleared struct{ r io.Reader }

func (c topBitCleared) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	for i := range p[:n] {
		p[i] &= 0x7f
	}
	return n, err
}

// nodePasswordLine is the "Admin Password:" rule of Node: the white space
// after the colon may run over line ends.
var nodePasswordLine = regexp.MustCompile(
	`(?:^|[\n\r\x{2028}\x{2029}])Admin Password:` +
		`[\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]*` +
		`([^\n\r\x{2028}\x{2029}]+)`)

// filePassword is the password a credentials file gives under a rule, ""
// for none.
func filePassword(rule *regexp.Regexp, content string) string {
	m := rule.FindStringSubmatch(toWellFormed(content))
	if m == nil {
		return ""
	}
	return jsTrim(m[1])
}

// noPasswordFromTheNextLine: an "Admin Password:" line without a value
// gives no password (AN4). Node took the next line with text as the value.
// The console takes the value on the line, or reads on in the next
// credentials file, and the first account gets that password.
func noPasswordFromTheNextLine(sc *scenarioVector) bool {
	const read = "[AUTH] Read admin password from "
	if sc.Dirs == nil {
		return false
	}
	content := map[string]string{}
	for _, f := range sc.Files {
		if !f.Dir {
			content[f.Path] = f.Content
		}
	}
	// The folders the credentials file is looked for in, in order
	var candidates []string
	for _, dir := range []string{sc.Dirs.KeysPath, "/opt/rustdesk", sc.Dirs.KeysPath + "/data", sc.Dirs.DataDir,
		"/opt/neodesk", "/opt/neodesk/data", "/opt/rustdesk/data"} {
		candidates = append(candidates, dir+"/.admin_credentials")
	}
	changed := false
	for i := range sc.Steps {
		step := &sc.Steps[i]
		for _, line := range step.Logs {
			if len(line) != 2 || !strings.HasPrefix(line[1], read) {
				continue
			}
			path := strings.TrimPrefix(line[1], read)
			nodePassword := filePassword(nodePasswordLine, content[path])
			newPath, newPassword := "", ""
			for _, candidate := range candidates {
				if text, ok := content[candidate]; ok {
					if newPassword = filePassword(adminPasswordLine, text); newPassword != "" {
						newPath = candidate
						break
					}
				}
			}
			if newPath == "" || newPath == path && newPassword == nodePassword {
				continue
			}
			line[1] = read + newPath
			if step.Fn == "ensureDefaultAdmin" && string(step.Result) == "true" {
				hash, err := bcryptHash(context.Background(), &mulberry{a: step.Seed}, newPassword, bcryptMinCost)
				if err != nil {
					panic(err)
				}
				for _, row := range sc.Tables["users"] {
					if row["username"] == "admin" {
						row["password_hash"] = hash
					}
				}
			}
			changed = true
		}
	}
	return changed
}

// textList reports whether stored is a JSON list of texts.
func textList(stored string) bool {
	var entries []any
	if json.Unmarshal([]byte(stored), &entries) != nil || entries == nil {
		return false
	}
	for _, entry := range entries {
		if _, ok := entry.(string); !ok {
			return false
		}
	}
	return true
}

// noCodesInOtherLists: a stored recovery list that is JSON but not a list
// of texts holds no codes (AN7). Node threw on it, or took a code that
// came before the first entry that is not text; the console answers no
// code, and the list stays as it is.
func noCodesInOtherLists(sc *scenarioVector) bool {
	if !strings.HasPrefix(sc.Name, "recovery/") || len(sc.Seed.Users) == 0 || sc.Seed.Users[0].TOTPRecoveryCodes == nil {
		return false
	}
	stored := *sc.Seed.Users[0].TOTPRecoveryCodes
	if !json.Valid([]byte(stored)) || textList(stored) {
		return false
	}
	changed := false
	for i := range sc.Steps {
		step := &sc.Steps[i]
		if step.Fn != "verifyRecoveryCode" || len(step.Args) == 0 || string(step.Args[0]) != "1" {
			continue
		}
		if step.Thrown != nil || string(step.Result) != "false" {
			step.Thrown, step.Result, changed = nil, json.RawMessage("false"), true
		}
	}
	for _, row := range sc.Tables["users"] {
		if row["username"] == sc.Seed.Users[0].Username && row["totp_recovery_codes"] != stored {
			row["totp_recovery_codes"], changed = stored, true
		}
	}
	return changed
}

// stopAtSyncsFromTheSignalServer: when the signal server asks for a second
// factor the console writes nothing to the account, and it never guesses
// the provider of an account that exists (S3, AN10). Node wrote the role
// admin and a guessed provider. What that changes depends on the rows each
// step leaves, so such a scenario is compared up to its first step with an
// answer of that kind and its rows at the end are not; the tests of S3
// (TestLDAPAccountWithTOTPKeepsRoleAndProvider) hold the new behaviour.
func stopAtSyncsFromTheSignalServer(sc *scenarioVector) bool {
	for i := range sc.Steps {
		if syncsFromTheSignalServer(&sc.Steps[i]) {
			sc.Steps, sc.skipTables = sc.Steps[:i], true
			return true
		}
	}
	return false
}

// syncsFromTheSignalServer reports whether a step has the signal server
// accept a sign-in with an answer that asks for a second factor, or with
// one that names no provider for an account Node then synced.
func syncsFromTheSignalServer(step *stepVector) bool {
	if step.Fn != "authenticate" || step.Go == nil {
		return false
	}
	synced := false
	for _, line := range step.Logs {
		if len(line) == 2 && strings.HasPrefix(line[1], "[AUTH] Synced '") {
			synced = true
		}
	}
	for _, raw := range step.Go.Login {
		var answer struct {
			Status int            `json:"status"`
			Body   map[string]any `json:"body"`
		}
		if json.Unmarshal(raw, &answer) != nil || answer.Body == nil {
			continue
		}
		if answer.Body["requires_2fa"] == true {
			return true
		}
		if provider, _ := answer.Body["auth_provider"].(string); synced && answer.Status == 200 && answer.Body["token"] != nil && provider == "" {
			return true
		}
	}
	return false
}

// strictSecrets: a stored two-factor secret is read as the API server
// reads it and used as it decodes (AN8). Node read it leniently and
// stretched a short one, so for such a secret Node's codes are not the
// console's. A code is checked against the API server's code
// (auth.ComputeTOTP) at the step's time, no secret that is not base32 is a
// fault, and turning two-factor on with a wrong code leaves the account's
// row as it was seeded.
func strictSecrets(sc *scenarioVector) bool {
	secrets := map[string]string{}
	seeded := map[string]seedUser{}
	for i, u := range sc.Seed.Users {
		id := strconv.Itoa(i + 1)
		seeded[id] = u
		if u.TOTPSecret != nil {
			secrets[id] = *u.TOTPSecret
		}
	}
	changed := false
	refused := map[string]bool{}
	for i := range sc.Steps {
		step := &sc.Steps[i]
		if len(step.Args) == 0 {
			continue
		}
		id := string(step.Args[0])
		if step.Fn == "generateTotpSetup" {
			delete(secrets, id)
			continue
		}
		secret, ok := secrets[id]
		if !ok || len(step.Args) < 2 || step.At.Abs == nil {
			continue
		}
		var token string
		if json.Unmarshal(step.Args[1], &token) != nil {
			continue
		}
		code, err := auth.ComputeTOTP(secret, time.UnixMilli(*step.At.Abs))
		right := err == nil && token == code
		switch step.Fn {
		case "verifyTotpCode":
			if step.Thrown != nil || string(step.Result) == "true" {
				result := json.RawMessage(strconv.FormatBool(right))
				if step.Thrown != nil || string(result) != string(step.Result) {
					step.Thrown, step.Result, changed = nil, result, true
				}
			}
		case "verifyAndEnableTotp":
			succeeded := strings.Contains(string(step.Result), `"success":true`)
			if !right && (succeeded || step.Thrown != nil) {
				step.Thrown, step.Result, changed = nil, json.RawMessage(`{"success":false,"error":"Invalid verification code"}`), true
				refused[id] = refused[id] || succeeded
			}
		}
	}
	for _, row := range sc.Tables["users"] {
		id := fmt.Sprint(row["id"])
		if !refused[id] {
			continue
		}
		u := seeded[id]
		row["totp_enabled"] = u.TOTPEnabled
		if u.TOTPRecoveryCodes == nil {
			row["totp_recovery_codes"] = nil
		} else {
			row["totp_recovery_codes"] = *u.TOTPRecoveryCodes
		}
	}
	return changed
}

// stopAtOtherSpellings: a typed name that differs from an account's only
// in letter case or spaces at the ends is refused, and a new directory
// account is made under the name lower-cased and trimmed (RV117), but such
// a spelling of a directory account's name signs in to that account
// (L115); Node signed such a name in as a second account. A scenario is
// compared up to its first sign-in with a name in another spelling than a
// seeded account of the name has, and its rows at the end are not;
// TestSignInComparesDirectoryNamesInLowerCase and
// TestNewDirectoryAccountsAreLowerCase hold the new behaviour.
func stopAtOtherSpellings(sc *scenarioVector) bool {
	seeded := map[string]bool{}
	for _, u := range sc.Seed.Users {
		seeded[u.Username] = true
	}
	for i := range sc.Steps {
		step := &sc.Steps[i]
		if step.Fn != "authenticate" || len(step.Args) == 0 {
			continue
		}
		var name string
		if json.Unmarshal(step.Args[0], &name) != nil || seeded[name] || name == strings.ToLower(strings.TrimSpace(name)) {
			continue
		}
		sc.Steps, sc.skipTables = sc.Steps[:i], true
		return true
	}
	return false
}

// ipv6AttemptsCountPerNetwork: a sign-in attempt from an IPv6 address is
// written and counted for its /64 (RV118); Node wrote the address. The
// recorded rows of login_attempts get the /64 of such an address;
// TestLockoutCountsAnIPv6NetworkAsOneAddress holds the counting.
func ipv6AttemptsCountPerNetwork(sc *scenarioVector) bool {
	changed := false
	for _, row := range sc.Tables["login_attempts"] {
		ip, ok := row["ip_address"].(string)
		if !ok {
			continue
		}
		if network := attemptAddress(ip); network != ip {
			row["ip_address"], changed = network, true
		}
	}
	return changed
}

// codesWorkOnce: a code of the second factor signs an account in once
// (RV122); Node took the same code again within its 30 seconds. A
// recorded success of verifyTotpCode at a step (of a fixed time) whose
// code already signed the account in becomes false; TestTOTPCodeWorksOnce
// holds the rule.
func codesWorkOnce(sc *scenarioVector) bool {
	changed := false
	used := map[string]int64{}
	for i := range sc.Steps {
		step := &sc.Steps[i]
		if step.Fn != "verifyTotpCode" || step.At.Abs == nil || len(step.Args) == 0 || string(step.Result) != "true" {
			continue
		}
		user, at := string(step.Args[0]), totpStep(time.UnixMilli(*step.At.Abs))
		if last, ok := used[user]; ok && last >= at {
			step.Result, changed = json.RawMessage("false"), true
			continue
		}
		used[user] = at
	}
	return changed
}

// noPasswordsTheSignInRefuses: a new password longer than the sign-in
// takes (MaxPasswordLength UTF-16 units) is refused (RV123); Node stored
// it. Such a scenario is compared up to that step, which answers the
// refusal, and its rows at the end are not;
// TestPasswordChangeRefusesWhatTheSignInRefuses holds the rule.
func noPasswordsTheSignInRefuses(sc *scenarioVector) bool {
	for i := range sc.Steps {
		step := &sc.Steps[i]
		if step.Fn != "changePassword" || len(step.Args) < 3 {
			continue
		}
		var next string
		var recorded struct{ Success bool }
		if json.Unmarshal(step.Args[2], &next) != nil || utf16Len(toWellFormed(next)) <= MaxPasswordLength ||
			json.Unmarshal(step.Result, &recorded) != nil || !recorded.Success {
			continue
		}
		step.Result = json.RawMessage(`{"success":false,"error":"` + string(ErrPasswordTooLong) + `"}`)
		sc.Steps, sc.skipTables = sc.Steps[:i+1], true
		return true
	}
	return false
}

// stopAtAccountLockouts: failed sign-ins lock an account for the address
// they came from only, counted from the attempts themselves (UM21,
// DECISIONS A68); Node locked the name for every address and kept the
// lockout in account_lockouts, with its end on the scenario's clock. A
// scenario is compared up to its first lockout check of a name that has
// failed attempts by then, where the two rules part, and its rows at the
// end are not; TestLockoutIsPerAccountAndAddress and
// TestLockoutCountsWithinTheWindow hold the new rule. A scenario without
// such a check is compared whole, without the lockouts Node wrote.
func stopAtAccountLockouts(sc *scenarioVector) bool {
	failed := map[string]bool{}
	for i := range sc.Steps {
		step := &sc.Steps[i]
		if len(step.Args) == 0 {
			continue
		}
		name := string(step.Args[0])
		switch step.Fn {
		case "recordAttempt":
			if len(step.Args) > 2 && string(step.Args[2]) == "false" {
				failed[name] = true
			}
		case "checkBruteForce":
			if failed[name] || strings.Contains(string(step.Result), reasonAccountLocked) {
				sc.Steps, sc.skipTables = sc.Steps[:i], true
				return true
			}
		}
	}
	if _, wrote := sc.Tables["account_lockouts"]; wrote {
		delete(sc.Tables, "account_lockouts")
		return true
	}
	return false
}

// signInTokensProveNoDevice: no token of the recordings proves a device
// (RV1). Each was made by a sign-in (generateAccessToken) or written into
// access_tokens by the seed, the way an older console wrote its sign-ins,
// and is an account's token, whatever client id it stored. Node's device
// checks took such a token as the device's token; the console's refuse it
// as a token bound to no device. The enrollment tokens of the signal
// server still open the device sockets.
func signInTokensProveNoDevice(sc *scenarioVector) bool {
	changed := false
	var seeded strings.Builder
	for _, statement := range append(append([]dialectSQL{}, sc.Seed.SQL...), sc.Seed.After...) {
		if strings.Contains(statement.SQLite, "access_tokens") {
			seeded.WriteString(statement.SQLite)
		}
	}
	accountToken, refused, used := map[string]bool{}, map[string]bool{}, map[string]bool{}
	isAccountToken := func(token string) bool {
		return accountToken[token] || seeded.Len() > 0 &&
			(strings.Contains(seeded.String(), "'"+token+"'") || strings.Contains(seeded.String(), "'"+HashAccessToken(token)+"'"))
	}
	for i := range sc.Steps {
		step := &sc.Steps[i]
		switch step.Fn {
		case "generateAccessToken":
			var token string
			if json.Unmarshal(step.Result, &token) == nil {
				accountToken[token] = true
			}
		case "requireDeviceToken":
			var header string
			var passed struct{ Next bool }
			if len(step.Args) == 0 || json.Unmarshal(step.Args[0], &header) != nil || json.Unmarshal(step.Result, &passed) != nil || !passed.Next {
				continue
			}
			if token, ok := ExtractBearerToken(header); ok && isAccountToken(token) {
				step.Result, changed = json.RawMessage(`{"status":401,"body":{"error":"Invalid or unbound access token"}}`), true
				refused[token] = true
			}
		case "validateAccessToken":
			var token string
			if len(step.Args) > 0 && json.Unmarshal(step.Args[0], &token) == nil && string(step.Result) != "null" {
				used[token] = true
			}
		case "verifyDeviceWsAuth":
			var token string
			if len(step.Args) < 2 || json.Unmarshal(step.Args[1], &token) != nil || string(step.Result) != "true" {
				continue
			}
			if isAccountToken(token) {
				step.Result, changed = json.RawMessage("false"), true
			}
		}
	}
	// A refused device request notes no use on the token
	for token := range refused {
		if used[token] {
			continue
		}
		for _, row := range sc.Tables["access_tokens"] {
			if row["token"] == token || row["token_hash"] == HashAccessToken(token) {
				row["last_used"] = nil
			}
		}
	}
	return changed
}

// seededTokenSQL is the text of the scenario's seed statements that write
// access_tokens.
func seededTokenSQL(sc *scenarioVector) string {
	var seeded strings.Builder
	for _, statement := range append(append([]dialectSQL{}, sc.Seed.SQL...), sc.Seed.After...) {
		if strings.Contains(statement.SQLite, "access_tokens") {
			seeded.WriteString(statement.SQLite)
		}
	}
	return seeded.String()
}

// tokensKeptAsTheirHash: the console keeps an access token as its hash only
// and looks it up by the hash alone (RV128). A token the seed wrote as it
// is, without its hash (a row from before the hash column, which the
// console hashes when it starts), is found by no lookup: it signs no one
// in, and no use is noted on its row. The rows the console writes hold
// "sha256 " and the hash in place of the token.
func tokensKeptAsTheirHash(sc *scenarioVector) bool {
	changed := false
	seeded := seededTokenSQL(sc)
	unhashed := func(token string) bool {
		return seeded != "" && strings.Contains(seeded, "'"+token+"'") && !strings.Contains(seeded, "'"+HashAccessToken(token)+"'")
	}
	unfound := map[string]bool{}
	for i := range sc.Steps {
		step := &sc.Steps[i]
		var token string
		if step.Fn != "validateAccessToken" || len(step.Args) == 0 || json.Unmarshal(step.Args[0], &token) != nil || !unhashed(token) {
			continue
		}
		unfound[token] = true
		if string(step.Result) != "null" {
			step.Result, changed = json.RawMessage("null"), true
		}
	}
	for _, row := range sc.Tables["access_tokens"] {
		token, _ := row["token"].(string)
		hash, _ := row["token_hash"].(string)
		switch {
		case unfound[token]:
			if row["last_used"] != nil {
				row["last_used"], changed = nil, true
			}
		case token != "" && hash != "" && !strings.Contains(seeded, "'"+token+"'"):
			row["token"], changed = "sha256 "+hash, true
		}
	}
	return changed
}
