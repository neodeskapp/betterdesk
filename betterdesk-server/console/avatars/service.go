// Package avatars keeps the profile pictures of console users.
//
// A picture is stored as checked bytes in the table user_avatars and
// served by GET /api/users/{id}/avatar. It comes from the identity
// provider's "picture" claim at a single sign-on (source "oidc", refreshed
// at every sign-in and removed when the claim is gone) or from the avatar
// address a user saves in their profile (source "profile"). The console
// fetches the picture once and serves its own copy, so a browser never
// loads it from somewhere else.
//
// A picture is checked by its type only: the first bytes must be those of
// a PNG, JPEG, WebP or GIF file, the declared type must agree, and the
// file may have 512 KB. It is not decoded, resized or written anew; the
// stored bytes are the bytes that came.
package avatars

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/logging"
)

// directoryTTL is how long the list of who has a picture is kept. Every
// write through this service drops it; the time covers writes by another
// process.
const directoryTTL = 30 * time.Second

// syncTimeout is the time a sync started with StartOIDCSync may take,
// the fetch and the database together.
const syncTimeout = 30 * time.Second

// Logger takes the warnings of the service. *kit.Logger is one.
type Logger interface {
	Warn(args ...any)
}

// Options are the settings of a Service. The old console had no setting
// for pictures: the limits are fixed.
type Options struct {
	// DB is the console's database.
	DB *store.DB
	// Fetch is the network access of picture fetches and, for tests, other
	// limits. The zero value is what the console uses.
	Fetch FetchOptions
	// Now is the clock of the directory cache. Nil means time.Now.
	Now func() time.Time
	// Logger takes warnings about pictures that could not be stored. Nil
	// logs through the logging package.
	Logger Logger
}

// Service stores and reads profile pictures. It is safe for concurrent
// use.
type Service struct {
	db    *store.DB
	fetch FetchOptions
	now   func() time.Time
	log   Logger

	mu        sync.Mutex
	directory []DirectoryEntry
	cached    bool
	loadedAt  time.Time
	pending   map[int64]*pendingSync
	running   sync.WaitGroup
}

type pendingSync struct {
	done   chan struct{}
	result Result
}

// New returns a Service.
func New(opts Options) *Service {
	s := &Service{db: opts.DB, fetch: opts.Fetch, now: opts.Now, log: opts.Logger, pending: map[int64]*pendingSync{}}
	if s.now == nil {
		s.now = time.Now
	}
	if s.log == nil {
		s.log = packageLogger{}
	}
	return s
}

type packageLogger struct{}

func (packageLogger) Warn(args ...any) {
	logging.Logf("Avatar", "WARN: %s", strings.TrimSuffix(fmt.Sprintln(args...), "\n"))
}

// Result says what a sync did. Action is "skipped", "cleared", "stored"
// or "failed"; Code is set for "failed" and names the reason (an Error
// code, the database's code, or "error").
type Result struct {
	Action string
	Code   string
}

func (s *Service) forget() {
	s.mu.Lock()
	s.cached = false
	s.directory = nil
	s.mu.Unlock()
}

// ResetCache drops the cached list of who has a picture.
func (s *Service) ResetCache() { s.forget() }

func (s *Service) storePicture(ctx context.Context, userID int64, picture, source string) error {
	img, err := LoadPicture(ctx, picture, s.fetch)
	if err != nil {
		return err
	}
	if err := setUserAvatar(ctx, s.db, userID, img, source); err != nil {
		return err
	}
	s.forget()
	return nil
}

func failed(err error) Result {
	code := errorCode(err)
	if code == "error" {
		if dbCode := databaseErrorCode(err); dbCode != "" {
			code = dbCode
		}
	}
	return Result{Action: "failed", Code: code}
}

// Claims is the answer of the single sign-on exchange as far as pictures
// go. *goapi.Object is one.
type Claims interface {
	Has(key string) bool
	Get(key string) any
}

// SyncOIDCPicture applies the "picture" field of the single sign-on
// exchange:
//
//   - no field (an older server): nothing changes, "skipped"
//   - an empty field, or one that is no text: the picture that came from
//     single sign-on is removed, "cleared"
//   - an address or a data URL: fetched or decoded and stored, "stored";
//     when that fails the old picture stays and the result is "failed"
//
// A failure is logged and never returned as an error: a picture must not
// stop a sign-in.
func (s *Service) SyncOIDCPicture(ctx context.Context, userID int64, exchange Claims) Result {
	if userID == 0 || exchange == nil || !exchange.Has("picture") {
		return Result{Action: "skipped"}
	}
	picture, _ := exchange.Get("picture").(string)
	picture = jsTrim(picture)
	var err error
	if picture == "" {
		if _, err = deleteUserAvatar(ctx, s.db, userID, "oidc"); err == nil {
			s.forget()
			return Result{Action: "cleared"}
		}
	} else if err = s.storePicture(ctx, userID, picture, "oidc"); err == nil {
		return Result{Action: "stored"}
	}
	result := failed(err)
	where := "inline picture"
	if !hasPrefixFold(picture, "data:") {
		where = sliceUnits(picture, 200)
	}
	s.log.Warn(fmt.Sprintf("OIDC picture for user %d not stored (%s): %s; %s", userID, result.Code, err.Error(), where))
	return result
}

// StartOIDCSync runs SyncOIDCPicture beside the sign-in that asked for
// it and returns at once. WaitForSync waits for the last sync started for
// a user.
func (s *Service) StartOIDCSync(userID int64, exchange Claims) {
	run := &pendingSync{done: make(chan struct{})}
	s.mu.Lock()
	s.pending[userID] = run
	s.mu.Unlock()
	s.running.Add(1)
	go func() {
		defer s.running.Done()
		ctx, cancel := context.WithTimeout(context.Background(), syncTimeout)
		defer cancel()
		run.result = s.SyncOIDCPicture(ctx, userID, exchange)
		s.mu.Lock()
		if s.pending[userID] == run {
			delete(s.pending, userID)
		}
		s.mu.Unlock()
		close(run.done)
	}()
}

// WaitForSync waits for the sync StartOIDCSync last started for the user
// and returns its result. ok is false when none is running or ctx ends
// first.
func (s *Service) WaitForSync(ctx context.Context, userID int64) (result Result, ok bool) {
	s.mu.Lock()
	run := s.pending[userID]
	s.mu.Unlock()
	if run == nil {
		return Result{}, false
	}
	select {
	case <-run.done:
		return run.result, true
	case <-ctx.Done():
		return Result{}, false
	}
}

// Wait returns when every sync started with StartOIDCSync has ended. The
// console calls it when it stops.
func (s *Service) Wait() { s.running.Wait() }

// SyncProfileAvatarURL applies the avatar address a user saved in their
// profile. An http or https address is fetched and stored ("stored"). An
// empty value removes a picture that came from the profile ("cleared",
// or "skipped" when there was none); a picture from single sign-on stays.
// Anything else is "failed" with the code "invalid_url".
func (s *Service) SyncProfileAvatarURL(ctx context.Context, userID int64, avatarURL string) Result {
	value := jsTrim(avatarURL)
	var err error
	switch {
	case value == "":
		var removed int64
		if removed, err = deleteUserAvatar(ctx, s.db, userID, "profile"); err == nil {
			s.forget()
			if removed > 0 {
				return Result{Action: "cleared"}
			}
			return Result{Action: "skipped"}
		}
	case !hasPrefixFold(value, "http://") && !hasPrefixFold(value, "https://"):
		return Result{Action: "failed", Code: "invalid_url"}
	default:
		if err = s.storePicture(ctx, userID, value, "profile"); err == nil {
			return Result{Action: "stored"}
		}
	}
	result := failed(err)
	s.log.Warn(fmt.Sprintf("Profile picture for user %d not stored (%s): %s", userID, result.Code, err.Error()))
	return result
}

// GetAvatar returns the stored picture of a user, or nil when there is
// none.
func (s *Service) GetAvatar(ctx context.Context, userID int64) (*Avatar, error) {
	return getUserAvatar(ctx, s.db, userID)
}

// Versions tells the version of each user's picture by user id.
type Versions map[int64]string

// Fields returns what a user payload says about the picture: has_avatar
// and avatar_version, which is null without a picture.
func (v Versions) Fields(userID int64) (hasAvatar bool, version store.NullString) {
	if text := v[userID]; text != "" {
		return true, store.NullString{String: text, Valid: true}
	}
	return false, store.NullString{}
}

// loadDirectory returns the list of who has a picture, read from the
// database when the kept one is older than 30 seconds or was dropped.
func (s *Service) loadDirectory(ctx context.Context) ([]DirectoryEntry, error) {
	s.mu.Lock()
	if s.cached && s.now().Sub(s.loadedAt) <= directoryTTL {
		entries := s.directory
		s.mu.Unlock()
		return entries, nil
	}
	s.mu.Unlock()
	entries, err := listUserAvatarVersions(ctx, s.db)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.directory, s.cached, s.loadedAt = entries, true, s.now()
	s.mu.Unlock()
	return entries, nil
}

// GetVersionMap returns the picture version of every user who has one.
func (s *Service) GetVersionMap(ctx context.Context) (Versions, error) {
	entries, err := s.loadDirectory(ctx)
	if err != nil {
		return nil, err
	}
	versions := make(Versions, len(entries))
	for _, e := range entries {
		versions[e.ID] = e.AvatarVersion
	}
	return versions, nil
}

// GetAvatarVersion returns the version of a user's picture. The result is
// null when the user has none, when the id is not a positive number and
// when the list cannot be read.
func (s *Service) GetAvatarVersion(ctx context.Context, userID int64) store.NullString {
	if userID <= 0 {
		return store.NullString{}
	}
	versions, err := s.GetVersionMap(ctx)
	if err != nil {
		return store.NullString{}
	}
	_, version := versions.Fields(userID)
	return version
}

// ListDirectory returns every user with a picture: id, name and version,
// by user id. The slice is shared: do not change it.
func (s *Service) ListDirectory(ctx context.Context) ([]DirectoryEntry, error) {
	return s.loadDirectory(ctx)
}
