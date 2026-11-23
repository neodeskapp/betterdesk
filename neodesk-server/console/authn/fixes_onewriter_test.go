package authn

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	xbcrypt "golang.org/x/crypto/bcrypt"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
)

// newHashMark stands for a password hash a scenario wrote: Node's bcrypt
// hash, the console's PBKDF2 hash of the test cost.
const newHashMark = "<a hash the scenario wrote>"

var (
	verifyingLine = regexp.MustCompile(`^(\[AUTH\] Verifying password for '.*' \(hash type: )[^,]*, length: \d+(\))$`)
	mismatchLine  = regexp.MustCompile(`^(\[AUTH\] Login failed: password mismatch for '.*' \(hash type: )[^)]*(\))$`)
	existsLine    = regexp.MustCompile(`^(\[AUTH\] Default admin account exists \()[^)]*(\) — password unchanged)$`)
	syncedLine    = regexp.MustCompile(`^\[AUTH\] Synced '.*' from Go \(provider=([^,]*),`)
)

// oneCredentialWriter: the console hashes passwords with the API server's
// code, replaces no hash it can check, and stores hashes of its recovery
// codes, checked by the API server's code (S12, DECISIONS A64). Node made
// bcrypt hashes, replaced a PBKDF2 hash with one at sign-in and at the
// admin check (where the force flag did not apply to a PBKDF2 hash), and
// stored the codes as they were shown, compared after trimming and in
// Unicode upper case with the dash.
//
// The rewrite follows each account's hash through the steps: the seeded
// one until a step writes a new one (a sign-in the signal server accepted,
// a password change, the first account, a forced update). The log lines
// that name a hash's type and length name the console's, and Node's lines
// of a replacement go. At the end every account the scenario wrote a hash
// for has newHashMark on both sides; the others keep their seeded hash.
// The recovery codes are followed per account the same way, with the
// console's check, and the stored hashes of the console's codes compare
// as the codes they hash.
func oneCredentialWriter(sc *scenarioVector) bool {
	changed := false
	seeded := map[string]string{}
	providers := map[string]string{}
	names := map[string]string{}
	codes := map[string]*recoveryState{}
	seed := func(u seedUser) {
		if _, ok := seeded[u.Username]; !ok {
			var provider string
			_ = json.Unmarshal(u.AuthProvider, &provider)
			seeded[u.Username], providers[u.Username] = u.PasswordHash, normalizeAuthProvider(provider)
		}
	}
	for i, u := range sc.Seed.Users {
		id := strconv.Itoa(i + 1)
		seed(u)
		names[id] = u.Username
		state := &recoveryState{enabled: u.TOTPEnabled}
		if u.TOTPRecoveryCodes != nil {
			state.text = *u.TOTPRecoveryCodes
		}
		codes[id] = state
	}
	for _, step := range sc.Steps {
		for _, hooks := range step.OnSleep {
			for _, hook := range hooks {
				for _, u := range hook.Users {
					seed(u)
				}
			}
		}
	}
	admin := "admin"
	if name := sc.Env["DEFAULT_ADMIN_USERNAME"]; name != nil && *name != "" {
		admin = *name
	}
	written := map[string]bool{}
	// current is the console's hash of an account before a step: its
	// seeded hash, or a new one of the test cost.
	newHash := fmt.Sprintf("pbkdf2-sha256$%d$", testIterations) + strings.Repeat("0", 97)
	current := func(name string) string {
		if written[name] {
			return newHash
		}
		return seeded[name]
	}
	rewrite := func(line []string, next string) {
		if line[1] != next {
			line[1], changed = next, true
		}
	}
	var candidates []string
	for i := range sc.Steps {
		step := &sc.Steps[i]
		var name string
		if len(step.Args) > 0 {
			_ = json.Unmarshal(step.Args[0], &name)
		}
		switch step.Fn {
		case "authenticate":
			hash := current(name)
			var password string
			if len(step.Args) > 1 {
				_ = json.Unmarshal(step.Args[1], &password)
			}
			var logs [][]string
			for _, line := range step.Logs {
				if len(line) == 2 {
					switch {
					case strings.HasPrefix(line[1], "[AUTH] Migrated password hash from PBKDF2 to bcrypt for user: "):
						// Node replaced the hash; the console keeps it
						changed = true
						continue
					case verifyingLine.MatchString(line[1]):
						m := verifyingLine.FindStringSubmatch(line[1])
						rewrite(line, fmt.Sprintf("%s%s, length: %d%s", m[1], hashKind(hash), utf16Len(hash), m[2]))
					case mismatchLine.MatchString(line[1]):
						m := mismatchLine.FindStringSubmatch(line[1])
						rewrite(line, m[1]+hashKind(hash)+m[2])
					case strings.HasSuffix(line[1], "' — syncing hash") && password != "",
						strings.Contains(line[1], "' — provisioning local user ("):
						written[name] = true
					case syncedLine.MatchString(line[1]):
						// A local account that turns into a directory or
						// single sign-on account gets an unusable hash
						provider := syncedLine.FindStringSubmatch(line[1])[1]
						if provider != providerLocal && provider != providers[name] {
							written[name] = true
						}
						providers[name] = provider
					}
				}
				logs = append(logs, line)
			}
			step.Logs = logs
		case "changePassword":
			if strings.Contains(string(step.Result), `"success":true`) {
				written[names[string(step.Args[0])]] = true
			}
		case "ensureDefaultAdmin":
			force := false
			var logs [][]string
			for j := 0; j < len(step.Logs); j++ {
				line := step.Logs[j]
				if len(line) != 2 {
					logs = append(logs, line)
					continue
				}
				switch text := line[1]; {
				case strings.Contains(text, "will force admin password update"):
					force = true
				case text == "[AUTH] Found admin user with PBKDF2 hash (created by Go server). Migrating to bcrypt...":
					// Node's replacement and the line after it go; the
					// console updates the hash only when forced
					j++
					changed = true
					if force {
						logs = append(logs, []string{"info", "[AUTH] Force password update requested — updating admin password"},
							[]string{"info", "[AUTH] Admin password hash force-updated to match DEFAULT_ADMIN_PASSWORD"})
						written[admin] = true
					} else {
						logs = append(logs, []string{"info", "[AUTH] Default admin account exists (" + hashKind(current(admin)) + ") — password unchanged"})
					}
					continue
				case text == "[AUTH] Admin password hash force-updated to match DEFAULT_ADMIN_PASSWORD":
					written[admin] = true
				case existsLine.MatchString(text):
					m := existsLine.FindStringSubmatch(text)
					rewrite(line, m[1]+hashKind(current(admin))+m[2])
				}
				logs = append(logs, line)
			}
			step.Logs = logs
			if string(step.Result) == "true" {
				written[admin] = true
			}
		case "verifyAndEnableTotp":
			var enabled struct {
				Success       bool     `json:"success"`
				RecoveryCodes []string `json:"recoveryCodes"`
			}
			if json.Unmarshal(step.Result, &enabled) == nil && enabled.Success {
				text, err := json.Marshal(enabled.RecoveryCodes)
				if err != nil {
					panic(err)
				}
				codes[string(step.Args[0])] = &recoveryState{enabled: true, text: string(text), set: true}
				candidates = append(candidates, enabled.RecoveryCodes...)
			}
		case "disableTotp":
			if state := codes[string(step.Args[0])]; state != nil && strings.Contains(string(step.Result), `"success":true`) {
				state.enabled, state.text, state.null, state.set = false, "", true, true
			}
		case "verifyRecoveryCode":
			state := codes[string(step.Args[0])]
			var code string
			if state == nil || len(step.Args) < 2 || json.Unmarshal(step.Args[1], &code) != nil || step.Thrown != nil {
				continue
			}
			matched := false
			if state.enabled && state.text != "" {
				left, ok, err := auth.ConsumeRecoveryCode(state.text, code)
				if err == nil && ok {
					matched, state.text, state.set = true, left, true
				}
			}
			if result := json.RawMessage(strconv.FormatBool(matched)); string(result) != string(step.Result) {
				step.Result, changed = result, true
			}
		}
	}
	for _, row := range sc.Tables["users"] {
		name, _ := row["username"].(string)
		if hash, ok := seeded[name]; ok && !written[name] {
			if row["password_hash"] != hash {
				row["password_hash"], changed = hash, true
			}
		} else if row["password_hash"] != newHashMark {
			row["password_hash"], changed = newHashMark, true
		}
		if state := codes[fmt.Sprint(row["id"])]; state != nil && state.set {
			var want any = state.text
			if state.null {
				want = nil
			}
			if row["totp_recovery_codes"] != want {
				row["totp_recovery_codes"], changed = want, true
			}
		}
	}
	sc.normalize = func(got map[string][]map[string]any) {
		for _, row := range got["users"] {
			name, _ := row["username"].(string)
			if hash, ok := seeded[name]; !ok || row["password_hash"] != hash {
				row["password_hash"] = newHashMark
			}
			if text, ok := row["totp_recovery_codes"].(string); ok {
				row["totp_recovery_codes"] = codesOfHashes(text, candidates)
			}
		}
	}
	return changed
}

// recoveryState is an account's recovery codes in a scenario: whether
// two-factor sign-in is on, the stored text (null when null is true), and
// whether a step changed it.
type recoveryState struct {
	enabled, null, set bool
	text               string
}

// hashKind is the type of a stored hash the sign-in's log line names.
func hashKind(hash string) string {
	switch {
	case isPBKDF2Hash(hash):
		return "PBKDF2"
	case strings.HasPrefix(hash, "$2"):
		return "bcrypt"
	}
	return "unknown"
}

// codesOfHashes writes a stored list of recovery codes with each bcrypt
// hash replaced by the code of candidates it hashes, as the list of the
// codes themselves.
func codesOfHashes(text string, candidates []string) string {
	var list []string
	if json.Unmarshal([]byte(text), &list) != nil {
		return text
	}
	hashed := false
	for i, entry := range list {
		if !strings.HasPrefix(entry, "$2a$") {
			continue
		}
		hashed = true
		for _, code := range candidates {
			if xbcrypt.CompareHashAndPassword([]byte(entry), []byte(strings.ReplaceAll(code, "-", ""))) == nil {
				list[i] = code
				break
			}
		}
	}
	if !hashed {
		return text
	}
	out, err := json.Marshal(list)
	if err != nil {
		panic(err)
	}
	return string(out)
}
