// Package billingclock holds the clock settings of the billing: the NTP
// servers the server checks its clock against, how far the clock may be
// off, whether a session may be billed while the clock is not in sync,
// whether the system's own time sync counts, and the time zone billing
// months are counted in.
//
// The settings live in the console's .env file (NTP_SERVERS,
// BILLING_MAX_CLOCK_SKEW_MS, BILLING_REQUIRE_SYNCED_CLOCK,
// BILLING_TRUST_OS_NTP, BILLING_TIMEZONE), which the server reads when it
// starts: a save takes effect after a restart, and the restart
// transaction can put the old values back (Service.Rollback). In the
// Docker layout with one container for the console and one for the
// server, the server keeps them in its database instead and takes a save
// at once, through its API (/timesync/config).
//
// Values come from a request body or from the restart file, so they can
// be of any JSON type; the package reads them the way the console's
// JavaScript did (String, parseInt, truthiness).
package billingclock

import (
	"errors"
	"regexp"
	"strings"
	"unicode/utf16"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/ssrfguard"
)

// The defaults of the settings. trust_os_ntp defaults to true on Linux
// and to false elsewhere (Options.Linux).
const (
	DefaultNTPServers      = "pool.ntp.org,time.google.com,time.cloudflare.com"
	DefaultMaxSkewMS       = 2000
	DefaultBillingTimezone = "UTC"
	// maxSkewLimit is the largest skew a save takes, in milliseconds.
	maxSkewLimit = 600000
)

// The refusals of a save. Their texts are what the route answers.
var (
	ErrInvalidNTPServers      = errors.New("invalid_ntp_servers")
	ErrInvalidMaxSkew         = errors.New("invalid_max_skew")
	ErrInvalidBillingTimezone = errors.New("invalid_billing_timezone")
)

// IsRefusal reports whether err is one of the three refusals of a save,
// which the route answers with 400.
func IsRefusal(err error) bool {
	return errors.Is(err, ErrInvalidNTPServers) || errors.Is(err, ErrInvalidMaxSkew) || errors.Is(err, ErrInvalidBillingTimezone)
}

// CommonTimezones are the zones the settings page offers in its list.
// Other valid zones can still be typed in.
func CommonTimezones() []string {
	return []string{
		"UTC",
		"Europe/Warsaw",
		"Europe/Berlin",
		"Europe/London",
		"Europe/Paris",
		"Europe/Amsterdam",
		"Europe/Madrid",
		"Europe/Rome",
		"Europe/Prague",
		"Europe/Stockholm",
		"Europe/Helsinki",
		"Europe/Athens",
		"Europe/Bucharest",
		"Europe/Istanbul",
		"Europe/Kyiv",
		"America/New_York",
		"America/Chicago",
		"America/Denver",
		"America/Los_Angeles",
		"America/Sao_Paulo",
		"Asia/Tokyo",
		"Asia/Shanghai",
		"Asia/Hong_Kong",
		"Asia/Singapore",
		"Asia/Kolkata",
		"Asia/Dubai",
		"Australia/Sydney",
		"Pacific/Auckland",
	}
}

// Settings are the clock settings, with the keys and the key order of
// their JSON in the console's answers and in the restart file.
type Settings struct {
	// NTPServers is a comma separated list.
	NTPServers string `json:"ntp_servers"`
	// MaxSkewMS is a whole number above 0. A value read from the file is
	// not capped, so it can be beyond what a float64 holds exactly.
	MaxSkewMS          float64 `json:"max_skew_ms"`
	RequireSyncedClock bool    `json:"require_synced_clock"`
	TrustOSNTP         bool    `json:"trust_os_ntp"`
	BillingTimezone    string  `json:"billing_timezone"`
}

// Object is the settings as a JSON object, for a rollback descriptor.
func (s Settings) Object() *goapi.Object {
	return goapi.NewObject(
		"ntp_servers", s.NTPServers,
		"max_skew_ms", s.MaxSkewMS,
		"require_synced_clock", s.RequireSyncedClock,
		"trust_os_ntp", s.TrustOSNTP,
		"billing_timezone", s.BillingTimezone,
	)
}

// Undefined stands for a value JavaScript would read as undefined: a
// member a body does not have, or no value at all. nil is null.
var Undefined any = undefined{}

type undefined struct{}

// member is raw[key] for a value that is not null: the member of an
// object, undefined for everything else.
func member(raw any, key string) any {
	if o, ok := raw.(*goapi.Object); ok && o.Has(key) {
		return o.Get(key)
	}
	return Undefined
}

// NormalizeSettings reads settings out of raw the way the console did
// (normalizeSettings): a missing or null member gets its default, the
// servers are trimmed and empty entries dropped, the skew is
// parseInt(value, 10) when that is above 0, the two flags are the
// truthiness of a member that is there, and the time zone is the trimmed
// text, "UTC" when empty.
//
// raw is a parsed JSON value, or Undefined (the default {}). For null the
// error is the TypeError of reading a member of null, and for an object
// with a member named toString where text was needed it is the TypeError
// of turning it into text.
func (s *Service) NormalizeSettings(raw any) (Settings, error) {
	if raw == nil {
		return Settings{}, errors.New("Cannot read properties of null (reading 'ntp_servers')")
	}
	servers, err := textOr(member(raw, "ntp_servers"), DefaultNTPServers)
	if err != nil {
		return Settings{}, err
	}
	var kept []string
	for _, part := range strings.Split(servers, ",") {
		if part = jsTrim(part); part != "" {
			kept = append(kept, part)
		}
	}
	skewText, err := toText(member(raw, "max_skew_ms"))
	if err != nil {
		return Settings{}, err
	}
	skew, ok := parseInt10(skewText)
	if !ok || skew <= 0 {
		skew = DefaultMaxSkewMS
	}
	zone, err := textOr(member(raw, "billing_timezone"), DefaultBillingTimezone)
	if err != nil {
		return Settings{}, err
	}
	if zone = jsTrim(zone); zone == "" {
		zone = DefaultBillingTimezone
	}
	return Settings{
		NTPServers:         strings.Join(kept, ","),
		MaxSkewMS:          skew,
		RequireSyncedClock: flagOr(member(raw, "require_synced_clock"), true),
		TrustOSNTP:         flagOr(member(raw, "trust_os_ntp"), s.opts.Linux),
		BillingTimezone:    zone,
	}, nil
}

// ValidateSettings is NormalizeSettings with the checks of a save: at
// least one server and each a host name or an IP address, a skew up to
// ten minutes, and a time zone Intl knows. The first check that fails is
// the error.
func (s *Service) ValidateSettings(raw any) (Settings, error) {
	settings, err := s.NormalizeSettings(raw)
	if err != nil {
		return Settings{}, err
	}
	var servers []string
	for _, part := range strings.Split(settings.NTPServers, ",") {
		if part = jsTrim(part); part != "" {
			servers = append(servers, part)
		}
	}
	if len(servers) == 0 {
		return Settings{}, ErrInvalidNTPServers
	}
	for _, server := range servers {
		if !IsValidNTPServerEntry(server) {
			return Settings{}, ErrInvalidNTPServers
		}
	}
	if settings.MaxSkewMS <= 0 || settings.MaxSkewMS > maxSkewLimit {
		return Settings{}, ErrInvalidMaxSkew
	}
	if !IsValidIANATimezone(settings.BillingTimezone) {
		return Settings{}, ErrInvalidBillingTimezone
	}
	return settings, nil
}

var ntpHostName = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9.-]*[a-zA-Z0-9])?$`)

// IsValidNTPServerEntry reports whether entry, trimmed, is an IP address
// as Node's net.isIP takes one, or a host name of letters, digits, dots
// and dashes that starts and ends with a letter or digit, of at most 253
// characters.
func IsValidNTPServerEntry(entry string) bool {
	s := jsTrim(entry)
	if s == "" || utf16Length(s) > 253 {
		return false
	}
	return ssrfguard.IsIP(s) != 0 || ntpHostName.MatchString(s)
}

// IsValidIANATimezone reports whether tz, trimmed, is a time zone
// Intl.DateTimeFormat takes, of at most 64 characters: a name of the
// tz database as ICU knows it, in any letter case, or an offset (+01,
// +0100, +01:00).
func IsValidIANATimezone(tz string) bool {
	value := jsTrim(tz)
	if value == "" || utf16Length(value) > 64 {
		return false
	}
	_, ok := resolveZone(value)
	return ok
}

// textOr is String(value ?? fallback).
func textOr(v any, fallback string) (string, error) {
	if v == nil || v == Undefined {
		return fallback, nil
	}
	return toText(v)
}

// flagOr is value !== undefined ? !!value : fallback.
func flagOr(v any, fallback bool) bool {
	if v == Undefined {
		return fallback
	}
	return goapi.Truthy(v)
}

// errNotPrimitive is the TypeError of String() on an object with its own
// member toString, which JSON can give and JavaScript cannot call.
var errNotPrimitive = errors.New("Cannot convert object to primitive value")

// toText is JavaScript's String(v) for a parsed JSON value.
func toText(v any) (string, error) {
	switch t := v.(type) {
	case undefined:
		return "undefined", nil
	case nil:
		return "null", nil
	case string:
		return t, nil
	case bool:
		if t {
			return "true", nil
		}
		return "false", nil
	case float64:
		return numberText(t), nil
	case []any:
		parts := make([]string, len(t))
		for i, element := range t {
			if element == nil {
				continue
			}
			text, err := toText(element)
			if err != nil {
				return "", err
			}
			parts[i] = text
		}
		return strings.Join(parts, ","), nil
	case *goapi.Object:
		if t.Has("toString") {
			return "", errNotPrimitive
		}
	}
	return "[object Object]", nil
}

// utf16Length is s.length in JavaScript.
func utf16Length(s string) int {
	return len(utf16.Encode([]rune(s)))
}
