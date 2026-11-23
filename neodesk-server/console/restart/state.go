package restart

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/serverinfo"
)

// read is the parsed transaction file, nil when it is missing or broken.
// A file that is there but cannot be read or parsed is worth a warning.
func (s *Service) read() any {
	raw, err := os.ReadFile(s.StatePath())
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			s.opts.Logger.Warn("[RESTART] Could not read transaction state: " + err.Error())
		}
		return nil
	}
	value, err := goapi.Parse(raw)
	if err != nil {
		s.opts.Logger.Warn("[RESTART] Could not read transaction state: " + err.Error())
		return nil
	}
	return value
}

// load is the transaction when the file holds a JSON object. Any other
// value has none of the fields the checks look at, so it counts as no
// transaction.
func (s *Service) load() *goapi.Object {
	state, _ := s.read().(*goapi.Object)
	return state
}

// write saves the transaction: a temporary file beside it, then a rename,
// so a reader never sees half a file.
func (s *Service) write(state *goapi.Object) error {
	if err := os.MkdirAll(s.opts.DataDir, 0o777); err != nil {
		return fmt.Errorf("create the data folder: %w", err)
	}
	compact, err := httpx.Marshal(plain(state))
	if err != nil {
		return fmt.Errorf("encode the restart transaction: %w", err)
	}
	var text bytes.Buffer
	if err := json.Indent(&text, compact, "", "  "); err != nil {
		return fmt.Errorf("encode the restart transaction: %w", err)
	}
	path := s.StatePath()
	tmp := path + ".tmp-" + strconv.Itoa(s.opts.PID)
	if err := os.WriteFile(tmp, text.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write the restart transaction: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("write the restart transaction: %w", err)
	}
	return nil
}

// touch stamps the transaction and saves it.
func (s *Service) touch(state *goapi.Object) error {
	state.Set("updatedAt", s.nowISO())
	return s.write(state)
}

// remove deletes the transaction file. A file that is gone already is
// fine.
func (s *Service) remove() error {
	if err := os.Remove(s.StatePath()); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove the restart transaction: %w", err)
	}
	return nil
}

// plain copies a parsed JSON value with every negative zero as 0, which
// is how JSON.stringify writes one.
func plain(v any) any {
	switch t := v.(type) {
	case float64:
		if t == 0 {
			return float64(0)
		}
	case []any:
		out := make([]any, len(t))
		for i, element := range t {
			out[i] = plain(element)
		}
		return out
	case *goapi.Object:
		if t == nil {
			return v
		}
		out := goapi.NewObject()
		for _, key := range t.Keys() {
			out.Set(key, plain(t.Get(key)))
		}
		return out
	}
	return v
}

func phaseOf(state *goapi.Object) string {
	phase, _ := state.Get("phase").(string)
	return phase
}

// isText is value === text.
func isText(value any, text string) bool {
	s, ok := value.(string)
	return ok && s == text
}

// tokensEqual is the comparison of a transaction's id or token with what a
// caller sent: both text, neither empty, the same bytes, compared in
// constant time.
func tokensEqual(expected any, actual string) bool {
	s, ok := expected.(string)
	if !ok || s == "" || actual == "" || len(s) != len(actual) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(s), []byte(actual)) == 1
}

// orNull is value || null.
func orNull(value any) any {
	if goapi.Truthy(value) {
		return value
	}
	return nil
}

// orEmpty is value || [].
func orEmpty(value any) any {
	if goapi.Truthy(value) {
		return value
	}
	return []any{}
}

// TypeError is the failure of a transaction file that somebody edited
// into a shape the console never writes: a "changes" that is no list, a
// change that is null. Its text is the one JavaScript gave, which the old
// routes answered with.
type TypeError struct{ Message string }

func (e *TypeError) Error() string { return e.Message }

func typeError(message string) error { return &TypeError{Message: message} }

// listOf is the list a method of Array is called on. what names the call
// for the error ("state.changes.find").
func listOf(value any, what string) ([]any, error) {
	list, ok := value.([]any)
	if !ok {
		return nil, typeError(what + " is not a function")
	}
	return list, nil
}

// spread is [...value] for a value of the file: the elements of a list
// and the characters of a text. what is the expression, for the error.
func spread(value any, what string) ([]any, error) {
	switch t := value.(type) {
	case []any:
		return t, nil
	case string:
		out := []any{}
		for _, char := range t {
			out = append(out, string(char))
		}
		return out, nil
	}
	return nil, typeError(what + " is not iterable")
}

// publicState is the transaction without what only the console needs: the
// owner, the rollbacks, the boot id, the token.
func publicState(state *goapi.Object) (*Public, error) {
	out := goapi.NewObject()
	if state.Has("id") {
		out.Set("id", state.Get("id"))
	}
	if state.Has("phase") {
		out.Set("phase", state.Get("phase"))
		out.Set("status", state.Get("phase"))
	}
	out.Set("ready", phaseOf(state) == PhaseReady)
	out.Set("failed", phaseOf(state) == PhaseFailed)
	out.Set("startedAt", orNull(state.Get("startedAt")))
	out.Set("updatedAt", orNull(state.Get("updatedAt")))
	list, err := listOf(orEmpty(state.Get("changes")), "(state.changes || []).map")
	if err != nil {
		return nil, err
	}
	changes := make([]any, 0, len(list))
	for _, item := range list {
		if item == nil {
			return nil, typeError("Cannot read properties of null (reading 'key')")
		}
		change := goapi.NewObject()
		if entry, isObject := item.(*goapi.Object); isObject {
			if entry.Has("key") {
				change.Set("key", entry.Get("key"))
			}
			if entry.Has("label") {
				change.Set("label", entry.Get("label"))
			}
		}
		changes = append(changes, change)
	}
	out.Set("changes", changes)
	out.Set("error", orNull(state.Get("error")))
	return &Public{fields: out}, nil
}

// rollbackAll undoes the changes, newest first, and returns one
// {key, error} per change that could not be undone. A change that is
// null stops it with an error.
func (s *Service) rollbackAll(ctx context.Context, changes []any) ([]any, error) {
	failures := []any{}
	for i := len(changes) - 1; i >= 0; i-- {
		if changes[i] == nil {
			return nil, typeError("Cannot read properties of null (reading 'key')")
		}
		if err := s.rollbackChange(ctx, changes[i]); err != nil {
			failure := goapi.NewObject()
			if entry, isObject := changes[i].(*goapi.Object); isObject && entry.Has("key") {
				failure.Set("key", entry.Get("key"))
			}
			failure.Set("error", err.Error())
			failures = append(failures, failure)
		}
	}
	return failures, nil
}

// rollbackChange undoes one change by the "type" of its descriptor.
func (s *Service) rollbackChange(ctx context.Context, change any) error {
	descriptor := goapi.NewObject()
	if entry, isObject := change.(*goapi.Object); isObject {
		if rollback := entry.Get("rollback"); goapi.Truthy(rollback) {
			descriptor, _ = rollback.(*goapi.Object)
		}
	}
	// A descriptor that is no object has no type
	kind, _ := descriptor.Get("type").(string)
	if kind == "go-config" {
		return s.rollbackGoConfig(ctx, descriptor)
	}
	if fn := s.rollbacks[kind]; fn != nil && kind != "" {
		return fn(ctx, descriptor)
	}
	return errors.New("unknown_restart_rollback")
}

// rollbackGoConfig puts a value of the server's configuration back:
// {type: "go-config", key, value}.
func (s *Service) rollbackGoConfig(ctx context.Context, descriptor *goapi.Object) error {
	if s.opts.GoAPI == nil {
		return errors.New("go_config_rollback_failed")
	}
	key, _ := descriptor.Get("key").(string)
	result := s.opts.GoAPI.SetConfig(ctx, key, descriptor.Get("value"))
	if goapi.Truthy(result.Get("success")) {
		return nil
	}
	if message, isString := result.Get("error").(string); isString && message != "" {
		return errors.New(message)
	}
	return errors.New("go_config_rollback_failed")
}

// ConnectionModeRollback is the Rollback for "connection-mode"
// descriptors, {type, settings}: it stores the settings the descriptor
// holds, as serverConnectionConfigService.setConnectionMode did.
func ConnectionModeRollback(info *serverinfo.Service) Rollback {
	return func(ctx context.Context, descriptor *goapi.Object) error {
		settings, isObject := descriptor.Get("settings").(*goapi.Object)
		if !isObject {
			if descriptor.Get("settings") == nil {
				kind := "undefined"
				if descriptor.Has("settings") {
					kind = "null"
				}
				return typeError("Cannot read properties of " + kind + " (reading 'mode')")
			}
			settings = goapi.NewObject()
		}
		field := func(key string) any {
			if !settings.Has(key) {
				return serverinfo.Undefined
			}
			return settings.Get(key)
		}
		_, err := info.SetConnectionMode(ctx, serverinfo.ConnectionInput{
			Mode:                    field("mode"),
			P2PFallbackMs:           field("p2p_fallback_ms"),
			SameNATRelay:            field("same_nat_relay"),
			AllowSharedNATInitiator: field("allow_shared_nat_initiator"),
			LoggedInOnlyInitiator:   field("logged_in_only_initiator"),
			OperatorOnlyOutbound:    field("operator_only_outbound"),
		})
		return err
	}
}
