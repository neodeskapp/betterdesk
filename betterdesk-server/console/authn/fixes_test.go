package authn

import (
	"context"
	"encoding/json"
	"io"
	"regexp"
	"strings"
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
}

// fixScenario applies every fix to sc and counts the ones that changed it.
func fixScenario(sc *scenarioVector, changed map[string]int) {
	for _, fix := range scenarioFixes {
		if fix.rewrite(sc) {
			changed[fix.bug]++
		}
	}
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
		"/opt/betterdesk", "/opt/betterdesk/data", "/opt/rustdesk/data"} {
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
