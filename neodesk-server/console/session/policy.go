package session

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// Policy is how long a session lives and how many an account may have. All
// times are in milliseconds. The JSON form is what the console's API hands
// to the web app.
type Policy struct {
	// IdleTimeoutMs ends a session that long after its last counted request.
	IdleTimeoutMs int64 `json:"idleTimeoutMs"`
	// MaxAgeMs ends a session that long after sign-in, whatever happens.
	MaxAgeMs int64 `json:"maxAgeMs"`
	// WarnBeforeMs is how long before the idle end the web app warns.
	WarnBeforeMs int64 `json:"warnBeforeMs"`
	// MaxSessionsPerUser is the number of sessions an account may have at
	// once. 0 means any number. Without SESSION_MAX_PER_USER it is the cap
	// of DefaultMaxPerUser, and the setting "One session per account"
	// (Manager.Limit) can make it 1.
	MaxSessionsPerUser int `json:"maxSessionsPerUser"`
	// MaxPerUserEnv is SESSION_MAX_PER_USER as it was set, trimmed; "" when
	// it was not set or not a number. A value here decides over the
	// setting.
	MaxPerUserEnv string `json:"-"`
}

const (
	minIdleMs       = 60 * 1000
	maxIdleMs       = 24 * 60 * 60 * 1000
	defaultIdleMs   = 30 * 60 * 1000
	minMaxAgeMs     = 5 * 60 * 1000
	maxMaxAgeMs     = 7 * 24 * 60 * 60 * 1000
	defaultMaxAgeMs = 24 * 60 * 60 * 1000
	defaultWarnMs   = 60 * 1000
	minWarnMs       = 15 * 1000
	maxWarnMs       = 10 * 60 * 1000
	maxMaxPerUser   = 1000
)

// DefaultMaxPerUser is how many sessions an account may have at once when
// SESSION_MAX_PER_USER is not set and "One session per account" is off: a
// person's browsers and devices, with the least recently used one ended
// at a sign-in beyond it.
const DefaultMaxPerUser = 10

// PolicyFromEnv reads the policy from the environment:
//
//	SESSION_IDLE_TIMEOUT   default 30 minutes, 1 minute to 24 hours
//	SESSION_MAX_AGE        default 24 hours, 5 minutes to 7 days, never below the idle timeout
//	SESSION_IDLE_WARN_MS   default 1 minute, 15 seconds to the idle timeout or 10 minutes
//	SESSION_MAX_PER_USER   not set by default, 0 (no limit) to 1000
//
// The first three take the leading whole number of the value, and a value
// without one, or one that is not above zero, gives the default.
// SESSION_MAX_PER_USER must be digits only; empty or anything else counts
// as not set, and then the account's limit is DefaultMaxPerUser, or 1 with
// the setting "One session per account" (Manager.Limit).
func PolicyFromEnv(getenv func(string) string) Policy {
	idle := clamp(leadingInt(getenv("SESSION_IDLE_TIMEOUT"), defaultIdleMs), minIdleMs, maxIdleMs)
	maxAge := clamp(leadingInt(getenv("SESSION_MAX_AGE"), defaultMaxAgeMs), minMaxAgeMs, maxMaxAgeMs)
	if maxAge < idle {
		maxAge = idle
	}
	warn := clamp(leadingInt(getenv("SESSION_IDLE_WARN_MS"), defaultWarnMs), minWarnMs, min(idle, maxWarnMs))
	p := Policy{
		IdleTimeoutMs:      idle,
		MaxAgeMs:           maxAge,
		WarnBeforeMs:       warn,
		MaxSessionsPerUser: DefaultMaxPerUser,
	}
	raw := strings.Trim(getenv("SESSION_MAX_PER_USER"), jsWhitespace)
	if n := digitsOnly(raw, -1); n >= 0 {
		p.MaxSessionsPerUser = int(min(maxMaxPerUser, n))
		p.MaxPerUserEnv = raw
	}
	return p
}

// CleanupInterval is how often ended sessions are swept: the idle timeout,
// but not more often than every 5 minutes and at least once an hour.
func (p Policy) CleanupInterval() time.Duration {
	return time.Duration(max(5*60*1000, min(60*60*1000, p.IdleTimeoutMs))) * time.Millisecond
}

// normalized makes a policy given in the options usable: an idle timeout of
// at least a minute and an absolute lifetime of at least the idle timeout.
// The zero Policy becomes the defaults.
func (p Policy) normalized() Policy {
	def := PolicyFromEnv(func(string) string { return "" })
	if p == (Policy{}) {
		return def
	}
	if p.IdleTimeoutMs <= 0 {
		p.IdleTimeoutMs = def.IdleTimeoutMs
	}
	p.IdleTimeoutMs = max(minIdleMs, p.IdleTimeoutMs)
	if p.MaxAgeMs <= 0 {
		p.MaxAgeMs = def.MaxAgeMs
	}
	p.MaxAgeMs = max(p.IdleTimeoutMs, p.MaxAgeMs)
	if p.WarnBeforeMs <= 0 {
		p.WarnBeforeMs = def.WarnBeforeMs
	}
	if p.MaxSessionsPerUser < 0 {
		p.MaxSessionsPerUser = def.MaxSessionsPerUser
		p.MaxPerUserEnv = ""
	}
	return p
}

func clamp(n, lo, hi int64) int64 {
	return min(hi, max(lo, n))
}

// leadingInt reads a value as JavaScript's parseInt(value, 10) does: white
// space, an optional sign, then digits, and whatever follows is ignored. It
// answers fallback unless the number is above zero.
func leadingInt(raw string, fallback int64) int64 {
	s := strings.TrimLeft(raw, jsWhitespace)
	negative := false
	if s != "" && (s[0] == '+' || s[0] == '-') {
		negative = s[0] == '-'
		s = s[1:]
	}
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 || negative {
		return fallback
	}
	n, err := strconv.ParseFloat(s[:end], 64)
	if err != nil || math.IsInf(n, 0) || n <= 0 {
		return fallback
	}
	if n >= math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(n)
}

// digitsOnly reads a value that must be digits only after trimming.
func digitsOnly(raw string, fallback int64) int64 {
	s := strings.Trim(raw, jsWhitespace)
	if s == "" {
		return fallback
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return fallback
		}
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil || n >= math.MaxInt64 {
		return math.MaxInt64
	}
	return int64(n)
}

// jsWhitespace is what JavaScript's trim and parseInt skip.
const jsWhitespace = "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff"

// The reasons a session ended, as stored in panel_sessions.end_reason.
const (
	ReasonLogout    = "logout"
	ReasonReplaced  = "replaced"
	ReasonIdle      = "idle"
	ReasonMaxAge    = "max_age"
	ReasonRevoked   = "revoked"
	ReasonDestroyed = "destroyed"
	// ReasonLimit ends the least recently used session when a sign-in
	// goes beyond the account's limit of several sessions; ReasonReplaced
	// is the same with a limit of one.
	ReasonLimit = "limit"
	// ReasonSignedOut ends a session the account signed out from another
	// browser (Signed-in devices).
	ReasonSignedOut = "signed_out"
	// ReasonEndedByAdmin ends a session someone who manages accounts
	// ended from the user's page.
	ReasonEndedByAdmin = "ended_by_admin"
	// ReasonUnreadable ends a row whose stored session is not one the
	// console can read.
	ReasonUnreadable = "unreadable"
)

// cleanIP trims an address, removes the ::ffff: prefix of an IPv4 address
// on an IPv6 socket and cuts it at 128 characters.
func cleanIP(ip string) string {
	s := strings.Trim(ip, jsWhitespace)
	s = strings.TrimPrefix(s, "::ffff:")
	return cut(s, 128)
}

// cleanUserAgent trims a user agent and cuts it at 512 characters.
func cleanUserAgent(ua string) string {
	return cut(strings.Trim(ua, jsWhitespace), 512)
}

var fingerprintPattern = regexp.MustCompile(`^[A-Za-z0-9_.=+/-]{8,128}$`)

// cleanFingerprint answers the browser's fingerprint if it is 8 to 128
// characters of letters, digits and _ . = + / -, and "" otherwise.
func cleanFingerprint(fp string) string {
	s := strings.Trim(fp, jsWhitespace)
	if !fingerprintPattern.MatchString(s) {
		return ""
	}
	return s
}

// cut shortens s to n characters as JavaScript counts them (UTF-16 units).
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	units := utf16.Encode([]rune(s))
	if len(units) <= n {
		return s
	}
	return string(utf16.Decode(units[:n]))
}
