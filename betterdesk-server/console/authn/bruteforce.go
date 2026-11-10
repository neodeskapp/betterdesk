package authn

import (
	"context"
	"fmt"
	"math"
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
// is blocked: by a lockout of the account that has not run out, or by too
// many failed attempts from the address within the window. An empty
// username or address skips that check.
func (s *Service) CheckBruteForce(ctx context.Context, username, ip string) (BruteForce, error) {
	if username != "" {
		lockedUntil, found, err := getAccountLockout(ctx, s.db, username)
		if err != nil {
			return BruteForce{}, err
		}
		if found {
			answer := BruteForce{Blocked: true, Reason: reasonAccountLocked, RetryUnknown: true}
			if end, ok := lockoutEnd(lockedUntil, s.db.IsPostgres(), s.zone); ok {
				left := math.Ceil(float64(end.UnixMilli()-s.now().UnixMilli()) / 1000)
				answer.RetryAfter, answer.RetryUnknown = int64(math.Max(left, 1)), false
			}
			return answer, nil
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

var lockoutText = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}:\d{2}(\.\d+)?$`)

// lockoutEnd is the time the remaining seconds are counted to.
//
// On SQLite the column is text in UTC and means what it says. On
// PostgreSQL the answer is computed from the column's time as this
// process's clock on the wall shows it, to the second, read as if it were
// UTC: with a time zone other than UTC the remaining time is off by the
// zone's offset (BUGS.md AN3).
func lockoutEnd(lockedUntil store.Time, postgres bool, zone *time.Location) (time.Time, bool) {
	if postgres {
		instant, ok := lockedUntil.Instant()
		if !ok {
			return time.Time{}, false
		}
		wall := instant.In(zone)
		return time.Date(wall.Year(), wall.Month(), wall.Day(), wall.Hour(), wall.Minute(), wall.Second(), 0, time.UTC), true
	}
	text := lockedUntil.String()
	if !lockoutText.MatchString(text) {
		return time.Time{}, false
	}
	end, ok := store.ParseTime(text)
	if !ok {
		return time.Time{}, false
	}
	return end.Truncate(time.Millisecond), true
}

// RecordAttempt writes a sign-in attempt. A success clears the account's
// lockout. A failure locks the account for Settings.LockoutMinutes once
// its failed attempts within the window reach Settings.MaxFailedAttempts;
// every further failure moves the end of the lockout.
func (s *Service) RecordAttempt(ctx context.Context, username, ip string, success bool) error {
	if err := recordLoginAttempt(ctx, s.db, username, ip, success); err != nil {
		return err
	}
	if success {
		return clearAccountLockout(ctx, s.db, username)
	}
	failed, err := countRecentFailed(ctx, s.db, "username", username, s.set.AttemptWindowMinutes)
	if err != nil {
		return err
	}
	if failed >= int64(s.set.MaxFailedAttempts) {
		until := s.now().Add(time.Duration(s.set.LockoutMinutes) * time.Minute)
		return lockAccount(ctx, s.db, username, store.DateTimeMillis(until), failed)
	}
	return nil
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
