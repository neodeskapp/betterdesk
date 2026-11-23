// Package upstreamname writes NeoDesk's names the way upstream BetterDesk
// wrote them. Tests use it with recordings of upstream's Node console
// that keep an answer only as a hash, or whose bytes depend on the
// length of the names: Go's answer, written with upstream's names, must
// give what was recorded. Such a test also checks with Holds that Go's
// own answer names nothing of upstream, so a BetterDesk name left in the
// product still fails it.
package upstreamname

import (
	"regexp"
	"strings"
)

var names = strings.NewReplacer(
	"github.com/neodeskapp/neodesk/neodesk-server", "github.com/unitronix/betterdesk-server",
	"neodeskapp", "trybetterdesk",
	"neode.sk", "betterde.sk",
	"NEODE.SK", "BETTERDE.SK",
	"NeoDesk", "BetterDesk",
	"NEODESK", "BETTERDESK",
	"Neodesk", "Betterdesk",
	"neoDesk", "betterDesk",
	"neodesk", "betterdesk",
)

var (
	envName    = regexp.MustCompile(`\bND_([A-Z])`)
	storageKey = regexp.MustCompile(`\b([nN])([dD])(\.sid|\.SID|\.guest|\.GUEST|\.oidc|\.OIDC|_ui_shell|-theme-choice|_ux35_sidebar_|_nav_more_open|_nav_sections_collapsed|_client_config_host|\.studio\.|_rdclient_last_user|_my_presence|_devices_show_deleted|\.invitation|-billing-report-intent)`)
	apiPrefix  = regexp.MustCompile(`(?i)(/api/)(n)(d)\b`)
	wire       = strings.NewReplacer("/ws/nd-", "/ws/bd-", "nd-mgmt", "bd-mgmt", "X-ND-", "X-BD-", "X-Nd-", "X-Bd-", "x-nd-", "x-bd-", "nd-enrollment-v1", "bd-enrollment-v1")
)

// Names returns s with NeoDesk's names in upstream's: the product, its
// domain and organization, the ND_ environment variables, the nd cookie
// and browser storage names and the nd wire names of the device API.
func Names(s string) string {
	s = names.Replace(s)
	s = envName.ReplaceAllString(s, "BD_$1")
	s = storageKey.ReplaceAllStringFunc(s, func(m string) string {
		return map[byte]string{'n': "b", 'N': "B"}[m[0]] + m[1:]
	})
	s = apiPrefix.ReplaceAllStringFunc(s, func(m string) string {
		return m[:5] + map[byte]string{'n': "b", 'N': "B"}[m[5]] + m[6:]
	})
	return wire.Replace(s)
}

// Holds reports whether s names upstream's product, apart from the
// UNITRONIX/BetterDesk repositories NeoDesk credits.
func Holds(s string) bool {
	lower := strings.ReplaceAll(strings.ToLower(s), "unitronix/betterdesk", "")
	return strings.Contains(lower, "betterdesk") || strings.Contains(lower, "better desk") || strings.Contains(lower, "betterde.sk")
}
