package authn

import (
	"encoding/json"
	"io"
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
