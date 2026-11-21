package authn

import (
	"context"
	"fmt"
	"math"
	"net/netip"
	"regexp"
	"time"

	"github.com/unitronix/betterdesk-server/console/store"
)

// The texts of a blocked sign-in.
const (
	reasonAccountLocked = "Account temporarily locked due to too many failed attempts"
	reasonIPLimited     = "Too many failed attempts from this IP address"
)

// BruteForce is the answer of CheckBruteForce.
type BruteForce struct {
	// Blocked says the sign-in must not be tried.
	Blocked bool
	// Reason is the English text for the client.
	Reason string
	// RetryAfter is the number of seconds to wait, at least 1.
	RetryAfter int64
	// RetryUnknown is set when the end of the lockout could not be read
	// as a time. The JSON then has "retryAfter": null.
	RetryUnknown bool
}

// MarshalJSON writes {"blocked":false}, or blocked, reason and
// retryAfter.
func (b BruteForce) MarshalJSON() ([]byte, error) {
	if !b.Blocked {
		return []byte(`{"blocked":false}`), nil
	}
	reason, _ := store.NullString{String: b.Reason, Valid: true}.MarshalJSON()
	retry := "null"
	if !b.RetryUnknown {
		retry = fmt.Sprint(b.RetryAfter)
	}
	return []byte(`{"blocked":true,"reason":` + string(reason) + `,"retryAfter":` + retry + `}`), nil
}

// CheckBruteForce says whether a sign-in for this user from this address
// is blocked: by a lockout of the account at this address that has not run
// out, or by too many failed attempts from the address within the window.
// An empty username skips the account's lockout, an empty address the
// address limit.
//
// The lockout counts the account's failed attempts from this address
// only (DECISIONS A68): Settings.MaxFailedAttempts of them within the
// window, after its last successful sign-in from there, lock the account
// for this address for Settings.LockoutMinutes after the last of them.
// Failures from other addresses never keep the owner out; they count
// against those addresses, and against the limit of each address. An IPv6
// address counts as its /64 (attemptAddress).
func (s *Service) CheckBruteForce(ctx context.Context, username, ip string) (BruteForce, error) {
	var name any
	if username != "" {
		name = username
	}
	return s.CheckBruteForceFor(ctx, name, ip)
}

// CheckBruteForceFor is CheckBruteForce for a user name a request gave as
// a JSON value of any type. name is the statement parameter the old
// console's database driver made of that value (console/jsbind): a text, a
// double on SQLite, the text form of a list or an object on PostgreSQL.
// The failed attempts are looked up for that parameter as it is, so a
// number finds the attempts written for it. nil skips the account's
// lockout, as a NULL finds none.
func (s *Service) CheckBruteForceFor(ctx context.Context, name any, ip string) (BruteForce, error) {
	ip = attemptAddress(ip)
	if name != nil {
		answer, err := s.addressLockout(ctx, name, ip)
		if err != nil || answer.Blocked {
			return answer, err
		}
	}
	if ip != "" {
		attempts, err := countRecentFailed(ctx, s.db, "ip_address", ip, s.set.AttemptWindowMinutes)
		if err != nil {
			return BruteForce{}, err
		}
		if attempts >= int64(s.set.IPRateLimit) {
			return BruteForce{Blocked: true, Reason: reasonIPLimited, RetryAfter: int64(s.set.AttemptWindowMinutes) * 60}, nil
		}
	}
	return BruteForce{}, nil
}

// addressLockout is the lockout of the account at the address (see
// CheckBruteForce): blocked with the seconds left until LockoutMinutes
// after the last failed attempt, when the newest MaxFailedAttempts failures
// since the last success fall within the window.
func (s *Service) addressLockout(ctx context.Context, name any, ip string) (BruteForce, error) {
	limit := s.set.MaxFailedAttempts
	failures, err := recentFailures(ctx, s.db, name, ip, limit)
	if err != nil || limit <= 0 || len(failures) < limit {
		return BruteForce{}, err
	}
	postgres := s.db.IsPostgres()
	last, ok := attemptTime(failures[0], postgres)
	if !ok {
		return BruteForce{Blocked: true, Reason: reasonAccountLocked, RetryUnknown: true}, nil
	}
	if first, ok := attemptTime(failures[limit-1], postgres); ok &&
		last.Sub(first) >= time.Duration(s.set.AttemptWindowMinutes)*time.Minute {
		return BruteForce{}, nil
	}
	end := last.Add(time.Duration(s.set.LockoutMinutes) * time.Minute)
	left := math.Ceil(float64(end.UnixMilli()-s.now().UnixMilli()) / 1000)
	if left <= 0 {
		return BruteForce{}, nil
	}
	return BruteForce{Blocked: true, Reason: reasonAccountLocked, RetryAfter: int64(left)}, nil
}

var attemptText = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}(\.\d+)?$`)

// attemptTime is the instant a time column of an attempt holds, to the
// millisecond. On SQLite the column is text in UTC; text that is no time
// gives none.
func attemptTime(stored store.Time, postgres bool) (time.Time, bool) {
	if postgres {
		instant, ok := stored.Instant()
		return instant.Truncate(time.Millisecond), ok
	}
	text := stored.String()
	if !attemptText.MatchString(text) {
		return time.Time{}, false
	}
	at, ok := store.ParseTime(text)
	if !ok {
		return time.Time{}, false
	}
	return at.Truncate(time.Millisecond), true
}

// expiryText is how an end time the service computed (of an access token)
// is written: the UTC text the console's SQLite times have,
// and on PostgreSQL the same text with its zone, so a timestamptz column
// gets that instant whatever time zone the database session is in.
func (s *Service) expiryText(t time.Time) string {
	text := store.DateTimeMillis(t)
	if s.db.IsPostgres() {
		return text + "+00"
	}
	return text
}

// RecordAttempt writes a sign-in attempt, which the lockout of the account
// at the address and the limit of the address count (CheckBruteForce). A
// success ends the account's lockout at that address; every further
// failure there moves its end.
func (s *Service) RecordAttempt(ctx context.Context, username, ip string, success bool) error {
	return s.RecordAttemptFor(ctx, username, ip, success)
}

// RecordAttemptFor is RecordAttempt for a user name a request gave as a
// JSON value of any type, passed as the statement parameter the old
// console's database driver made of it (see CheckBruteForceFor). A nil
// name fails like the NULL it stands for.
func (s *Service) RecordAttemptFor(ctx context.Context, name any, ip string, success bool) error {
	return recordLoginAttempt(ctx, s.db, name, attemptAddress(ip), success)
}

// attemptAddress is the address a sign-in attempt counts for. An IPv6
// address counts as its /64 network, written "2001:db8:1:2::/64": one host
// commonly has a whole /64, and a new address for every attempt would
// otherwise never block. IPv4, also written as IPv6, and text that is no
// address stay as they are.
func attemptAddress(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil || !addr.Is6() || addr.Is4In6() {
		return ip
	}
	network, err := addr.WithZone("").Prefix(64)
	if err != nil {
		return ip
	}
	return network.String()
}

// CleanupHousekeeping deletes access tokens that are expired or revoked
// and sign-in attempts older than 24 hours. A failure is logged and ends
// the run; it is not returned.
func (s *Service) CleanupHousekeeping(ctx context.Context) {
	err := cleanupExpiredTokens(ctx, s.db)
	if err == nil {
		err = cleanupOldLoginAttempts(ctx, s.db)
	}
	if err != nil {
		s.log.Error("Housekeeping error:", err.Error())
	}
}
