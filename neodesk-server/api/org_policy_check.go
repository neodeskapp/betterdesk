package api

import (
	"fmt"
	"math"
	"net"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// The settings of an organization's policy groups, as the Policies page
// offers them, and the check of a group before it is stored: devices get
// these values as they are, so a value of the wrong kind or out of range
// is refused with a reason that names the setting.

type policyRule func(value any) string

func policyChoice(choices ...string) policyRule {
	return func(value any) string {
		text, ok := value.(string)
		if !ok || !slices.Contains(choices, text) {
			return "must be one of " + strings.Join(choices, ", ")
		}
		return ""
	}
}

func policyWhole(min, max int) policyRule {
	return func(value any) string {
		n, ok := value.(float64)
		if !ok || n != math.Trunc(n) || n < float64(min) || n > float64(max) {
			return fmt.Sprintf("must be a whole number from %d to %d", min, max)
		}
		return ""
	}
}

func policySwitch(value any) string {
	if _, ok := value.(bool); !ok {
		return "must be true or false"
	}
	return ""
}

// policyList checks a list of texts, each with entry; entry returns ""
// for a good one.
func policyList(what string, entry func(string) bool) policyRule {
	return func(value any) string {
		items, ok := value.([]any)
		if !ok {
			return "must be a list of " + what
		}
		for _, item := range items {
			text, ok := item.(string)
			if !ok || !entry(strings.TrimSpace(text)) {
				return fmt.Sprintf("must be a list of %s; %s is not one", what, quotePolicyValue(item))
			}
		}
		return ""
	}
}

func quotePolicyValue(v any) string {
	if text, ok := v.(string); ok {
		return strconv.Quote(text)
	}
	return fmt.Sprint(v)
}

var policyHostName = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?)*$`)

// policyRelayAddress is a relay server: a host name or an IP address,
// with a port of 1 to 65535 or none; an IPv6 address with a port is
// written in brackets.
func policyRelayAddress(text string) bool {
	host := text
	if h, port, err := net.SplitHostPort(text); err == nil {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return false
		}
		host = h
	} else if strings.HasPrefix(text, "[") || strings.Count(text, ":") == 1 {
		return false
	}
	if net.ParseIP(host) != nil {
		return true
	}
	return len(host) <= 253 && policyHostName.MatchString(host)
}

// policyAddressRange is an IP address or a range in CIDR notation.
func policyAddressRange(text string) bool {
	if strings.Contains(text, "/") {
		_, _, err := net.ParseCIDR(text)
		return err == nil
	}
	return net.ParseIP(text) != nil
}

func policyAnyName(text string) bool { return text != "" }

var policySettings = map[string]map[string]policyRule{
	"connection": {
		"mode":                 policyChoice("attended", "unattended", "ask_always"),
		"max_session_duration": policyWhole(0, 1440),
		"idle_lock_timeout":    policyWhole(0, 120),
		"wallpaper":            policyChoice("show", "hide_during_session", "hide_always"),
	},
	"features": {
		"allow_file_transfer": policySwitch,
		"allow_clipboard":     policySwitch,
		"allow_audio":         policySwitch,
		"allow_recording":     policySwitch,
	},
	"security": {
		"require_2fa":              policySwitch,
		"password_min_length":      policyWhole(6, 128),
		"password_require_special": policySwitch,
		"password_max_age_days":    policyWhole(0, 365),
		"allowed_operators":        policyList("account names", policyAnyName),
	},
	"network": {
		"block_direct_p2p":      policySwitch,
		"allowed_relay_servers": policyList("server addresses (host or host:port)", policyRelayAddress),
		"ip_allowlist":          policyList("IP addresses or ranges such as 192.168.1.0/24", policyAddressRange),
	},
	"update": {
		"auto_update": policySwitch,
		"channel":     policyChoice("stable", "beta", "dev"),
	},
}

// checkOrgPolicy says what is wrong with a policy group as a request body
// gave it, "" when nothing is. A group is a JSON object of the group's
// settings; an empty one clears the group.
func checkOrgPolicy(category string, body any) string {
	group, ok := body.(map[string]any)
	if !ok {
		return "a policy group must be a JSON object of its settings"
	}
	rules := policySettings[category]
	keys := make([]string, 0, len(group))
	for key := range group {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		rule, known := rules[key]
		if !known {
			return key + " is not a setting of the " + category + " policy"
		}
		if problem := rule(group[key]); problem != "" {
			return key + " " + problem
		}
	}
	return ""
}
