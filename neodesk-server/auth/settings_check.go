package auth

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"

	ldapv3 "github.com/go-ldap/ldap/v3"
)

// SettingError is a refused directory (LDAP) or single sign-on (OIDC)
// setting: the field in the settings' JSON, the code of the reason, and
// what the reason names. Code is the key of the console's text
// (settings.signin.check_<code>); Error is that text in English.
type SettingError struct {
	Field string
	Code  string
	// Line is the entry of a role map, from 1; Text is the value the
	// reason quotes.
	Line int
	Text string
}

// settingMessages are the English texts of the codes, with {line} and
// {text} for Line and Text. They are the texts of lang/en.json.
var settingMessages = map[string]string{
	"host_required":         "Enter the directory's host name or address.",
	"host_invalid":          "Enter only the host name or address, without ldap:// and without a port.",
	"port_range":            "Enter a port from 1 to 65535.",
	"timeout_range":         "Enter a whole number of seconds from 1 to 120.",
	"base_dn_required":      "Enter the Base DN under which people are searched.",
	"dn_invalid":            "This is not a valid DN, for example dc=example,dc=com.",
	"template_required":     "Enter the DN template, with {{username}} where the name goes.",
	"template_placeholder":  "The template needs {{username}} where the name goes.",
	"user_filter_username":  "The user filter needs {{username}} where the name goes.",
	"filter_invalid":        "This is not a valid LDAP filter. Check the brackets, for example (uid={{username}}).",
	"role_invalid":          "This role does not exist: {text}",
	"map_line":              "Entry {line} of the role map needs the form group=role: {text}",
	"map_role":              "Entry {line} of the role map names a role that does not exist: {text}",
	"client_id_required":    "Enter the client ID from your identity provider.",
	"issuer_required":       "Enter the issuer URL, or turn off Auto-discovery and enter the endpoints.",
	"endpoint_required":     "Without Auto-discovery, enter this endpoint.",
	"url_invalid":           "Enter a full address that starts with https:// or http://.",
	"network_invalid":       "This is not a private network: {text}. Use addresses or ranges in 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16 or fc00::/7.",
	"issuer_private":        "The issuer is on a private network. Add its network under Allowed private OIDC networks.",
	"issuer_not_allowed":    "The server does not fetch from this address: localhost, link-local and multicast addresses are refused.",
	"redirect_url_required": "Enter the redirect URL.",
}

// SettingCodes lists the codes, for the check that every one has a text.
func SettingCodes() []string {
	codes := make([]string, 0, len(settingMessages))
	for code := range settingMessages {
		codes = append(codes, code)
	}
	return codes
}

// SettingMessage is the English text of a code with {line} and {text}
// left in place.
func SettingMessage(code string) string { return settingMessages[code] }

func (e *SettingError) Error() string {
	text := settingMessages[e.Code]
	text = strings.ReplaceAll(text, "{line}", strconv.Itoa(e.Line))
	return strings.ReplaceAll(text, "{text}", e.Text)
}

func refused(field, code string) *SettingError { return &SettingError{Field: field, Code: code} }

// CheckLDAPConfig checks the settings of a directory that is switched on
// before they are saved: everything a sign-in needs is there and can be
// used as it is written. With forTest it checks what the connection test
// uses: the Base DN and the DN template may be missing. Port and timeout 0
// mean their defaults.
func CheckLDAPConfig(cfg *LDAPConfig, forTest bool) *SettingError {
	host := strings.TrimSpace(cfg.Host)
	if host == "" {
		return refused("host", "host_required")
	}
	if !validLDAPHost(host) {
		return refused("host", "host_invalid")
	}
	if cfg.Port < 0 || cfg.Port > 65535 {
		return refused("port", "port_range")
	}
	if cfg.ConnTimeoutSec < 0 || cfg.ConnTimeoutSec > 120 {
		return refused("conn_timeout_sec", "timeout_range")
	}
	if cfg.DirectBind {
		template := strings.TrimSpace(cfg.DirectBindDN)
		switch {
		case template == "" && !forTest:
			return refused("direct_bind_dn", "template_required")
		case template == "":
		case !strings.Contains(template, "{{username}}"):
			return refused("direct_bind_dn", "template_placeholder")
		case !validDN(strings.ReplaceAll(template, "{{username}}", "x")):
			return refused("direct_bind_dn", "dn_invalid")
		}
	} else {
		base := strings.TrimSpace(cfg.BaseDN)
		switch {
		case base == "" && !forTest:
			return refused("base_dn", "base_dn_required")
		case base != "" && !validDN(base):
			return refused("base_dn", "dn_invalid")
		}
		if filter := strings.TrimSpace(cfg.UserFilter); filter != "" {
			if !strings.Contains(filter, "{{username}}") {
				return refused("user_filter", "user_filter_username")
			}
			if !validFilter(strings.ReplaceAll(filter, "{{username}}", "x")) {
				return refused("user_filter", "filter_invalid")
			}
		}
	}
	if base := strings.TrimSpace(cfg.GroupBaseDN); base != "" && !validDN(base) {
		return refused("group_base_dn", "dn_invalid")
	}
	if filter := strings.TrimSpace(cfg.GroupFilter); filter != "" {
		filled := strings.NewReplacer("{{dn}}", "cn=x", "{{username}}", "x").Replace(filter)
		if !validFilter(filled) {
			return refused("group_filter", "filter_invalid")
		}
	}
	if err := checkDefaultRole(cfg.DefaultRole); err != nil {
		return err
	}
	return CheckGroupRoleMap(cfg.GroupRoleMap)
}

// validLDAPHost is a host name or address alone: no scheme, no port, no
// spaces. An IPv6 address may be written with or without brackets.
func validLDAPHost(host string) bool {
	if strings.ContainsAny(host, " \t\r\n/\\@") {
		return false
	}
	if strings.Contains(host, ":") {
		_, err := netip.ParseAddr(strings.TrimSuffix(strings.TrimPrefix(host, "["), "]"))
		return err == nil
	}
	return true
}

func validDN(dn string) bool {
	parsed, err := ldapv3.ParseDN(dn)
	return err == nil && len(parsed.RDNs) > 0
}

func validFilter(filter string) bool {
	_, err := ldapv3.CompileFilter(filter)
	return err == nil
}

func checkDefaultRole(role string) *SettingError {
	if role = strings.TrimSpace(role); role != "" && !ValidRole(role) {
		return &SettingError{Field: "default_role", Code: "role_invalid", Text: role}
	}
	return nil
}

// CheckGroupRoleMap checks a group to role map as parseGroupRoleMap reads
// it: entries separated by lines or |, each group=role with a role that
// exists. parseGroupRoleMap skips an entry it can't read; a save refuses
// it instead.
func CheckGroupRoleMap(raw string) *SettingError {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	raw = strings.ReplaceAll(raw, "\n", "|")
	line := 0
	for _, entry := range strings.Split(raw, "|") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		line++
		idx := strings.LastIndex(entry, "=")
		if idx <= 0 || strings.TrimSpace(entry[:idx]) == "" || strings.TrimSpace(entry[idx+1:]) == "" {
			return &SettingError{Field: "group_role_map", Code: "map_line", Line: line, Text: entry}
		}
		if !ValidRole(strings.TrimSpace(entry[idx+1:])) {
			return &SettingError{Field: "group_role_map", Code: "map_role", Line: line, Text: entry}
		}
	}
	return nil
}

// CheckOIDCConfig checks the settings of single sign-on that is switched
// on before they are saved: the client ID, the issuer (with Auto-discovery)
// or the endpoints (without it), addresses that are http or https, an
// issuer the server may reach, and the roles. The network list is checked
// whether single sign-on is on or not.
func CheckOIDCConfig(ctx context.Context, cfg *OIDCConfig) *SettingError {
	if entry, ok := invalidPrivateNetwork(cfg.AllowedPrivateCIDRs); ok {
		return &SettingError{Field: "allowed_private_cidrs", Code: "network_invalid", Text: entry}
	}
	if !cfg.Enabled {
		return nil
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return refused("client_id", "client_id_required")
	}
	issuer := strings.TrimSpace(cfg.IssuerURL)
	manual := strings.TrimSpace(cfg.AuthorizationURL) != "" && strings.TrimSpace(cfg.TokenURL) != ""
	if cfg.AutoDiscovery {
		// Without an issuer the provider uses the endpoints as they are
		if issuer == "" && !manual {
			return refused("issuer_url", "issuer_required")
		}
	} else {
		for _, field := range []struct{ name, value string }{
			{"authorization_url", cfg.AuthorizationURL},
			{"token_url", cfg.TokenURL},
		} {
			if strings.TrimSpace(field.value) == "" {
				return refused(field.name, "endpoint_required")
			}
		}
	}
	for _, field := range []struct{ name, value string }{
		{"issuer_url", cfg.IssuerURL},
		{"authorization_url", cfg.AuthorizationURL},
		{"token_url", cfg.TokenURL},
		{"userinfo_url", cfg.UserinfoURL},
		{"redirect_url", cfg.RedirectURL},
		{"panel_url", cfg.PanelURL},
	} {
		if value := strings.TrimSpace(field.value); value != "" {
			if _, err := validateOIDCFetchURL(value); err != nil {
				return refused(field.name, "url_invalid")
			}
		}
	}
	if strings.TrimSpace(cfg.RedirectURL) == "" {
		return refused("redirect_url", "redirect_url_required")
	}
	if cfg.AutoDiscovery && issuer != "" {
		if err := checkIssuerHost(ctx, issuer, cfg.AllowedPrivateCIDRs); err != nil {
			return err
		}
	}
	if err := checkDefaultRole(cfg.DefaultRole); err != nil {
		return err
	}
	return CheckGroupRoleMap(cfg.GroupRoleMap)
}

// invalidPrivateNetwork returns the first entry of the network list that
// newOIDCFetchPolicy refuses.
func invalidPrivateNetwork(raw string) (string, bool) {
	for _, entry := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r' || r == '\t' || r == ' '
	}) {
		if _, err := newOIDCFetchPolicy(entry); err != nil {
			return entry, true
		}
	}
	return "", false
}

// checkIssuerHost refuses an issuer the server would refuse to fetch:
// localhost, or an address on a private network outside the allowed
// ones. A name is looked up; when the lookup fails the save goes through
// and the sign-in says what is wrong.
func checkIssuerHost(ctx context.Context, issuer, allowed string) *SettingError {
	u, err := validateOIDCFetchURL(issuer)
	if err != nil {
		return refused("issuer_url", "url_invalid")
	}
	policy, err := newOIDCFetchPolicy(allowed)
	if err != nil {
		return refused("allowed_private_cidrs", "network_invalid")
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return refused("issuer_url", "issuer_not_allowed")
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		lookup, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		addrs, err := net.DefaultResolver.LookupIPAddr(lookup, host)
		if err != nil {
			return nil
		}
		for _, a := range addrs {
			ips = append(ips, a.IP)
		}
	}
	if len(ips) == 0 {
		return nil
	}
	for _, ip := range ips {
		if validateOIDCFetchIPWithPolicy(ip, policy) == nil {
			return nil
		}
	}
	if addr, ok := netip.AddrFromSlice(ips[0]); ok && addr.Unmap().IsPrivate() {
		return refused("issuer_url", "issuer_private")
	}
	return refused("issuer_url", "issuer_not_allowed")
}
