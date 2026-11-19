// Package restart coordinates settings changes that only take effect after
// the server has been restarted (services/restartCoordinator.js of the old
// console).
//
// A route that saves such a setting registers the change with a
// description of how to undo it. The changes of one operator collect in a
// transaction, which the web app shows as "restart required". The operator
// then cancels it (every change is undone, newest first) or confirms it
// (the server is restarted). After a confirmed restart the web app polls
// the public status with the transaction's id and token until a console
// process with another boot id finds the server healthy, and completes the
// transaction.
//
// The transaction is one JSON file in the data folder,
// .restart-transaction.json, because the console may be gone while the
// restart runs and the next process has to find it. The file holds no
// secret: a rollback is a small descriptor, never a value that was saved.
//
// # The file
//
//	{
//	  "id": "<uuid>", "userId": "<id of the operator, as text>",
//	  "phase": "pending" | "restarting" | "ready" | "failed",
//	  "createdAt": "<ISO time>", "updatedAt": "<ISO time>",
//	  "changes": [{"key": "...", "label": "...", "rollback": {"type": "...", ...}}],
//	  "startedAt", "bootId", "token", "error", "restart", "rollbackFailures"   (from confirm and cancel)
//	}
//
// It is written to a temporary file next to it (mode 0600) and renamed,
// with two spaces of indent, and its keys keep the order in which they
// were first written.
//
// # Rollbacks
//
// A descriptor names its kind in "type". The package undoes "go-config"
// itself (a value of the server's configuration, through the API client).
// Every other kind is a function in Options.Rollbacks or added with
// SetRollback: "connection-mode", "advanced-file", "billing-clock" and
// "env-config" are the kinds the old console knew. A kind without a
// function fails with "unknown_restart_rollback".
package restart

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/serverinfo"
	"github.com/unitronix/betterdesk-server/logging"
)

const (
	// stateFile is the name of the transaction file in the data folder.
	stateFile = ".restart-transaction.json"
	// MaxChanges is how many changes one transaction can hold.
	MaxChanges = 32
	// restartTokenBytes is the length of the token the web app polls the
	// public status with, before it is written as hex.
	restartTokenBytes = 24
	// ExitDelay is how long a confirmed restart waits before it ends the
	// console process, so that the answer reaches the web app first.
	ExitDelay = 1500 * time.Millisecond
)

// The phases of a transaction.
const (
	PhasePending    = "pending"
	PhaseRestarting = "restarting"
	PhaseReady      = "ready"
	PhaseFailed     = "failed"
)

// Rollback undoes one change from its descriptor, the "rollback" object
// the change was registered with.
type Rollback func(ctx context.Context, descriptor *goapi.Object) error

// Logger takes the package's warnings and its one info line.
type Logger interface {
	Warn(args ...any)
	Info(args ...any)
}

// Options are the settings of a Service.
type Options struct {
	// DataDir is the console's data folder (config.dataDir: DATA_DIR, else
	// /app/data in a container, else the folder "data" of the console).
	DataDir string
	// GoAPI calls the server's API: the health of the server for the
	// public status, and its configuration for a "go-config" rollback.
	GoAPI *goapi.Client
	// RestartServer restarts the server
	// (serverConnectionConfigService.restartServer). A failed restart
	// shows in the result; the error is for "there is nothing to restart
	// with". Nil fails every Confirm with "no_restart".
	RestartServer func(ctx context.Context) (serverinfo.RestartResult, error)
	// Rollbacks are the functions for the kinds of rollback other than
	// "go-config", by the "type" of the descriptor.
	Rollbacks map[string]Rollback
	// Exit ends the process with the exit status that asks its
	// supervisor to start it again (DECISIONS A30): ExitDelay after a
	// confirmed restart, and through ExitLater after an installed update
	// or a restore. The console sets it only when an installer named a
	// supervisor (BETTERDESK_SUPERVISOR). Nil ends nothing: the routes
	// then answer as they do where no restart is possible.
	Exit func()
	// After runs f once d has passed. Nil means time.AfterFunc.
	After func(d time.Duration, f func())
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
	// Random gives the random bytes of the boot id, the transaction ids
	// and the tokens. Nil means crypto/rand.
	Random io.Reader
	// PID names the temporary file a save goes through. 0 means the
	// process id.
	PID int
	// Logger takes warnings. Nil logs through the logging package.
	Logger Logger
}

// Service is the restart coordinator of one console process. It is safe
// for concurrent use: one call at a time reads or changes the transaction.
type Service struct {
	opts   Options
	bootID string

	// mu makes each call one step, as the calls of the old console were
	// between two waits. It also guards rollbacks.
	mu        sync.Mutex
	rollbacks map[string]Rollback
}

// New returns a Service with a fresh boot id.
func New(opts Options) *Service {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Random == nil {
		opts.Random = rand.Reader
	}
	if opts.PID == 0 {
		opts.PID = os.Getpid()
	}
	if opts.After == nil {
		opts.After = func(d time.Duration, f func()) { time.AfterFunc(d, f) }
	}
	if opts.Logger == nil {
		opts.Logger = packageLogger{}
	}
	s := &Service{opts: opts, rollbacks: map[string]Rollback{}}
	for kind, fn := range opts.Rollbacks {
		s.rollbacks[kind] = fn
	}
	s.bootID = s.randomHex(16)
	return s
}

type packageLogger struct{}

func (packageLogger) Warn(args ...any) { logging.Logf("console", "WARN: %s", fmt.Sprint(args...)) }
func (packageLogger) Info(args ...any) { logging.Logf("console", "%s", fmt.Sprint(args...)) }

// StatePath is the path of the transaction file (STATE_PATH).
func (s *Service) StatePath() string {
	return filepath.Join(s.opts.DataDir, stateFile)
}

// BootID is the random id of this console process. A transaction that is
// restarting under another boot id has outlived the process that
// confirmed it.
func (s *Service) BootID() string { return s.bootID }

// Supervised reports whether the process ends, to be started again, after
// a confirmed restart, an installed update or a restore.
func (s *Service) Supervised() bool { return s.opts.Exit != nil }

// ExitLater ends the process d from now, so that the answer a route just
// wrote reaches the client first, and logs why. It reports false, and
// does nothing, when the process is not supervised.
func (s *Service) ExitLater(d time.Duration, reason string) bool {
	if s.opts.Exit == nil {
		return false
	}
	s.opts.After(d, func() {
		s.opts.Logger.Info(reason)
		s.opts.Exit()
	})
	return true
}

// SetRollback sets the function that undoes changes of one kind, the
// "type" of their descriptors. The group that owns the setting calls it
// when it registers its routes.
func (s *Service) SetRollback(kind string, fn Rollback) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rollbacks[kind] = fn
}

// Error is a refusal of this package. Message is the text a route answers
// with, Status the HTTP status the old console gave it; 0 means it named
// none and the routes answered 500.
type Error struct {
	Message string
	Status  int
	// Details lists the changes that could not be undone, each as
	// {key, error}. Only "restart_rollback_failed" has them.
	Details []any
}

func (e *Error) Error() string { return e.Message }

// StatusCode is the status a route answers this error with
// (err.statusCode || 500).
func (e *Error) StatusCode() int {
	if e.Status == 0 {
		return http.StatusInternalServerError
	}
	return e.Status
}

func refusal(status int, message string) *Error {
	return &Error{Message: message, Status: status}
}

// Change is a saved setting that needs a restart, and how to undo it.
type Change struct {
	// Key names the setting. A second change with the same key keeps the
	// first rollback, so a cancel returns to the value before the first
	// edit, and takes the new label.
	Key string
	// Label is what the web app shows. Empty means the key.
	Label string
	// Rollback is the descriptor: {"type": kind, ...}. It must not hold a
	// secret.
	Rollback *goapi.Object
}

// Public is a transaction as the web app sees it:
//
//	{id, phase, status, ready, failed, startedAt, updatedAt, changes: [{key, label}], error}
//
// The answer of Confirm has "token" and "restart" after those. A nil
// *Public encodes as null.
type Public struct {
	fields *goapi.Object
}

// MarshalJSON writes the object with its keys in the order above.
func (p *Public) MarshalJSON() ([]byte, error) { return httpx.Marshal(plain(p.fields)) }

// Get is the value of one key, nil when the object does not have it.
func (p *Public) Get(key string) any {
	if p == nil {
		return nil
	}
	return p.fields.Get(key)
}

// ID is the transaction's id.
func (p *Public) ID() string { return p.text("id") }

// Phase is the transaction's phase, or "unknown".
func (p *Public) Phase() string { return p.text("phase") }

// Token is the token of a confirmed restart.
func (p *Public) Token() string { return p.text("token") }

func (p *Public) text(key string) string {
	text, _ := p.Get(key).(string)
	return text
}

// Canceled is the answer of Cancel.
type Canceled struct {
	Canceled bool   `json:"canceled"`
	ID       string `json:"id"`
}

// Completed is the answer of Complete.
type Completed struct {
	Completed bool   `json:"completed"`
	ID        string `json:"id"`
}

// operator is the id of the signed-in account as the file holds it
// (String(req.session.userId)); ok is false when nobody is signed in.
func operator(userID int64) (id string, ok bool) {
	if userID == 0 {
		return "", false
	}
	return strconv.FormatInt(userID, 10), true
}

// ownedBy is state.userId === safeUserId(req).
func ownedBy(state *goapi.Object, userID int64) bool {
	id, signedIn := operator(userID)
	if !state.Has("userId") {
		return false
	}
	owner := state.Get("userId")
	if !signedIn {
		return owner == nil
	}
	text, isText := owner.(string)
	return isText && text == id
}

// GetPending is the operator's transaction, nil when there is none or it
// belongs to someone else.
func (s *Service) GetPending(userID int64) (*Public, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.load()
	if state == nil {
		return nil, nil
	}
	switch phaseOf(state) {
	case PhasePending, PhaseRestarting, PhaseReady, PhaseFailed:
	default:
		return nil, nil
	}
	if !ownedBy(state, userID) {
		return nil, nil
	}
	return publicState(state)
}

// rawForOperator is the transaction with the id, when it is the
// operator's.
func (s *Service) rawForOperator(userID int64, id string) (*goapi.Object, error) {
	state := s.load()
	if state == nil || !isText(state.Get("id"), id) || !ownedBy(state, userID) {
		return nil, refusal(http.StatusNotFound, "restart_transaction_not_found")
	}
	return state, nil
}

// RegisterChange adds a change to the operator's pending transaction, or
// starts one, and returns the transaction. A transaction that is past
// pending, whoever owns it, is replaced.
//
// Errors: "restart_change_not_reversible" without a rollback,
// "restart_auth_required" when nobody is signed in,
// "restart_transaction_owned" (409) while another operator's transaction
// is pending, "restart_too_many_changes" for the 33rd change.
func (s *Service) RegisterChange(userID int64, change Change) (*Public, error) {
	if change.Rollback == nil {
		return nil, &Error{Message: "restart_change_not_reversible"}
	}
	id, signedIn := operator(userID)
	if !signedIn {
		return nil, &Error{Message: "restart_auth_required"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	state := s.load()
	if state != nil && phaseOf(state) != PhasePending {
		state = nil
	}
	if state != nil && !isText(state.Get("userId"), id) {
		return nil, refusal(http.StatusConflict, "restart_transaction_owned")
	}
	if state == nil {
		state = goapi.NewObject(
			"id", s.randomUUID(),
			"userId", id,
			"phase", PhasePending,
			"createdAt", s.nowISO(),
			"updatedAt", s.nowISO(),
			"changes", []any{},
		)
	}

	changes, isList := state.Get("changes").([]any)
	if !isList {
		if !state.Has("changes") {
			return nil, typeError("Cannot read properties of undefined (reading 'find')")
		}
		if state.Get("changes") == nil {
			return nil, typeError("Cannot read properties of null (reading 'find')")
		}
		return nil, typeError("state.changes.find is not a function")
	}
	label := change.Label
	if label == "" {
		label = change.Key
	}
	var existing *goapi.Object
	found := false
	for _, item := range changes {
		if item == nil {
			return nil, typeError("Cannot read properties of null (reading 'key')")
		}
		if entry, isObject := item.(*goapi.Object); isObject && isText(entry.Get("key"), change.Key) {
			existing, found = entry, true
			break
		}
	}
	switch {
	case found:
		existing.Set("label", label)
	case len(changes) >= MaxChanges:
		return nil, &Error{Message: "restart_too_many_changes"}
	default:
		state.Set("changes", append(changes, goapi.NewObject("key", change.Key, "label", label, "rollback", change.Rollback)))
	}
	if err := s.touch(state); err != nil {
		return nil, err
	}
	return publicState(state)
}

// Cancel undoes every change of the operator's pending transaction,
// newest first, and removes it.
//
// Errors: "restart_transaction_not_found" (404),
// "restart_transaction_not_cancelable" (409) when it is past pending, and
// "restart_rollback_failed" (500, with Details) when a change could not be
// undone; the transaction is then failed and stays.
func (s *Service) Cancel(ctx context.Context, userID int64, id string) (*Canceled, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.rawForOperator(userID, id)
	if err != nil {
		return nil, err
	}
	if phaseOf(state) != PhasePending {
		return nil, refusal(http.StatusConflict, "restart_transaction_not_cancelable")
	}
	changes, err := spread(state.Get("changes"), "state.changes")
	if err != nil {
		return nil, err
	}
	failures, err := s.rollbackAll(ctx, changes)
	if err != nil {
		return nil, err
	}
	if len(failures) > 0 {
		state.Set("phase", PhaseFailed)
		state.Set("error", "rollback_failed")
		state.Set("rollbackFailures", failures)
		if err := s.touch(state); err != nil {
			return nil, err
		}
		return nil, &Error{Message: "restart_rollback_failed", Status: http.StatusInternalServerError, Details: failures}
	}
	if err := s.remove(); err != nil {
		return nil, err
	}
	return &Canceled{Canceled: true, ID: id}, nil
}

// Confirm restarts the server for the operator's transaction, which must
// be pending or failed. The answer is the transaction with "token" and
// "restart" (what the restart reported).
//
// When the restart reports a failure, every change is undone and the
// transaction is failed with the error
// "service_restart_failed_rolled_back" or
// "service_restart_failed_rollback_failed"; that is an answer, not an
// error. When the restart worked, the transaction stays in the phase
// "restarting" and Options.Exit is called after ExitDelay.
//
// Errors: "restart_transaction_not_found" (404),
// "restart_transaction_not_pending" (409), and whatever RestartServer
// returned as its error ("no_restart"). The changes are then undone as
// for a reported failure, and the transaction is failed with the same
// error texts.
func (s *Service) Confirm(ctx context.Context, userID int64, id string) (*Public, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.rawForOperator(userID, id)
	if err != nil {
		return nil, err
	}
	if phase := phaseOf(state); phase != PhasePending && phase != PhaseFailed {
		return nil, refusal(http.StatusConflict, "restart_transaction_not_pending")
	}
	state.Set("phase", PhaseRestarting)
	state.Set("startedAt", s.nowISO())
	state.Set("bootId", s.bootID)
	state.Set("token", s.randomHex(restartTokenBytes))
	state.Set("error", nil)
	if err := s.touch(state); err != nil {
		return nil, err
	}

	answer, err := s.restart(ctx, state)
	var failed *failedRestart
	if errors.As(err, &failed) {
		return nil, failed.err
	}
	if err != nil {
		message := err.Error()
		if message == "" {
			message = "service_restart_failed"
		}
		state.Set("phase", PhaseFailed)
		state.Set("error", message)
		if touchErr := s.touch(state); touchErr != nil {
			return nil, touchErr
		}
		return nil, err
	}
	return answer, nil
}

// failedRestart is the error of RestartServer after restart undid the
// changes and stored the failed transaction.
type failedRestart struct{ err error }

func (f *failedRestart) Error() string { return f.err.Error() }

// restart is the part of Confirm that may fail the transaction.
func (s *Service) restart(ctx context.Context, state *goapi.Object) (*Public, error) {
	var result serverinfo.RestartResult
	err := serverinfo.ErrNoRestart
	if s.opts.RestartServer != nil {
		result, err = s.opts.RestartServer(ctx)
	}
	if err != nil {
		if undoErr := s.undo(ctx, state); undoErr != nil {
			return nil, undoErr
		}
		if touchErr := s.touch(state); touchErr != nil {
			return nil, touchErr
		}
		return nil, &failedRestart{err: err}
	}
	restart, err := goapi.FromGo(result)
	if err != nil {
		return nil, fmt.Errorf("restart result: %w", err)
	}
	state.Set("restart", restart)

	failed := restartFailed(result)
	if failed {
		if err := s.undo(ctx, state); err != nil {
			return nil, err
		}
	}
	if err := s.touch(state); err != nil {
		return nil, err
	}
	if !failed && s.opts.Exit != nil {
		s.opts.After(ExitDelay, func() {
			s.opts.Logger.Info("[RESTART] Exiting console for the confirmed full service restart")
			s.opts.Exit()
		})
	}
	public, err := publicState(state)
	if err != nil {
		return nil, err
	}
	public.fields.Set("token", state.Get("token"))
	public.fields.Set("restart", restart)
	return public, nil
}

// undo puts back every change of a transaction whose restart failed and
// marks it failed, with the error saying whether everything came back.
func (s *Service) undo(ctx context.Context, state *goapi.Object) error {
	changes, err := spread(orEmpty(state.Get("changes")), "(state.changes || [])")
	if err != nil {
		return err
	}
	failures, err := s.rollbackAll(ctx, changes)
	if err != nil {
		return err
	}
	state.Set("rollbackFailures", failures)
	state.Set("phase", PhaseFailed)
	if len(failures) > 0 {
		state.Set("error", "service_restart_failed_rollback_failed")
	} else {
		state.Set("error", "service_restart_failed_rolled_back")
	}
	return nil
}

// restartFailed reports a restart whose reload of the units or whose
// restart of a service says success: false.
func restartFailed(result serverinfo.RestartResult) bool {
	if result.DaemonReload != nil && isFalse(result.DaemonReload) {
		return true
	}
	for _, item := range result.Restarts {
		if item != nil && isFalse(item) {
			return true
		}
	}
	return false
}

func isFalse(o *goapi.Object) bool {
	value, isBool := o.Get("success").(bool)
	return isBool && !value
}

// GetPublicStatus is the transaction for someone who knows its id and its
// token, signed in or not: the web app asks while the console restarts.
// Anything else gets {phase: "unknown", status: "unknown", ready: false}.
//
// A transaction that is restarting under the boot id of an earlier process
// becomes ready once the server's health answers.
func (s *Service) GetPublicStatus(ctx context.Context, id, token string) (*Public, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.load()
	if state == nil || !tokensEqual(state.Get("id"), id) || !tokensEqual(state.Get("token"), token) {
		return &Public{fields: goapi.NewObject("phase", "unknown", "status", "unknown", "ready", false)}, nil
	}
	if phaseOf(state) == PhaseRestarting && !isText(state.Get("bootId"), s.bootID) {
		if s.opts.GoAPI == nil {
			return nil, &Error{Message: "restart: no API client configured"}
		}
		if isText(s.opts.GoAPI.GetHealth(ctx).Get("status"), "running") {
			state.Set("phase", PhaseReady)
			if err := s.touch(state); err != nil {
				return nil, err
			}
		}
	}
	return publicState(state)
}

// Complete removes the operator's transaction once it is ready or failed.
//
// Errors: "restart_transaction_not_found" (404),
// "restart_transaction_not_ready" (409).
func (s *Service) Complete(userID int64, id string) (*Completed, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.rawForOperator(userID, id)
	if err != nil {
		return nil, err
	}
	if phase := phaseOf(state); phase != PhaseReady && phase != PhaseFailed {
		return nil, refusal(http.StatusConflict, "restart_transaction_not_ready")
	}
	if err := s.remove(); err != nil {
		return nil, err
	}
	return &Completed{Completed: true, ID: id}, nil
}

// DismissFailed removes the operator's failed transaction and reports
// whether it did. With a key it only removes a transaction that has a
// change with that key.
func (s *Service) DismissFailed(userID int64, key string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.load()
	if state == nil || phaseOf(state) != PhaseFailed || !ownedBy(state, userID) {
		return false, nil
	}
	if key != "" {
		changes, err := listOf(orEmpty(state.Get("changes")), "(state.changes || []).some")
		if err != nil {
			return false, err
		}
		has := false
		for _, item := range changes {
			if item == nil {
				return false, typeError("Cannot read properties of null (reading 'key')")
			}
			if entry, isObject := item.(*goapi.Object); isObject && isText(entry.Get("key"), key) {
				has = true
				break
			}
		}
		if !has {
			return false, nil
		}
	}
	if err := s.remove(); err != nil {
		return false, err
	}
	return true, nil
}

// ReadState is the transaction file as it is: the parsed JSON value
// (goapi.Parse), nil when there is no file or it cannot be read.
func (s *Service) ReadState() any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read()
}

func (s *Service) nowISO() string { return httpx.ISOTime(s.opts.Now()) }

// randomBytes reads n random bytes. Without randomness there is no safe
// token, so a failing source stops the console.
func (s *Service) randomBytes(n int) []byte {
	buf := make([]byte, n)
	if _, err := io.ReadFull(s.opts.Random, buf); err != nil {
		panic(fmt.Errorf("restart: random source: %w", err))
	}
	return buf
}

func (s *Service) randomHex(n int) string { return hex.EncodeToString(s.randomBytes(n)) }

// randomUUID is a version 4 UUID (crypto.randomUUID).
func (s *Service) randomUUID() string {
	b := s.randomBytes(16)
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
