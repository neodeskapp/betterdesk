// Package accessibility keeps the accessibility settings an account chose
// for itself, so they follow it to every browser: one row per account in
// user_preferences, the settings as JSON in its accessibility column. The
// web app keeps a copy in the browser for the pages before sign-in and for
// the first paint (web-ui/src/lib/shell/a11y.svelte.ts has the same
// settings and defaults).
package accessibility

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The choices of a setting that can follow the system.
const (
	System = "system"
	On     = "on"
	Off    = "off"
)

// Settings are the accessibility settings of an account.
type Settings struct {
	// ReduceMotion turns off transitions and animations: "system" follows
	// prefers-reduced-motion, "on" and "off" override it.
	ReduceMotion string `json:"reduceMotion"`
	// MoreContrast switches to the high-contrast palette of the theme:
	// "system" follows prefers-contrast: more, "on" and "off" override it.
	MoreContrast string `json:"moreContrast"`
	// UnderlineLinks underlines every link.
	UnderlineLinks bool `json:"underlineLinks"`
	// ReadableFont uses a plain font with wider letter and word spacing.
	ReadableFont bool `json:"readableFont"`
	// NoSingleKeyShortcuts turns off the shortcuts of one character key
	// (the bare "/" that opens search).
	NoSingleKeyShortcuts bool `json:"noSingleKeyShortcuts"`
	// PauseLiveUpdates stops lists and pages from refreshing by themselves.
	PauseLiveUpdates bool `json:"pauseLiveUpdates"`
}

// Defaults are the settings of an account that never chose any: motion
// and contrast follow the system, everything else is off.
func Defaults() Settings {
	return Settings{ReduceMotion: System, MoreContrast: System}
}

// InvalidError says which setting of a change was not understood.
type InvalidError struct {
	Key string
}

func (e *InvalidError) Error() string {
	return fmt.Sprintf("invalid accessibility setting %q", e.Key)
}

// Apply returns s with the settings of a change: a JSON object with some
// or all of the keys of Settings. A key Settings does not have, or a value
// of the wrong kind, fails with an InvalidError and changes nothing.
func Apply(s Settings, change map[string]json.RawMessage) (Settings, error) {
	next := s
	for key, raw := range change {
		var err error
		switch key {
		case "reduceMotion":
			next.ReduceMotion, err = choice(raw)
		case "moreContrast":
			next.MoreContrast, err = choice(raw)
		case "underlineLinks":
			next.UnderlineLinks, err = flag(raw)
		case "readableFont":
			next.ReadableFont, err = flag(raw)
		case "noSingleKeyShortcuts":
			next.NoSingleKeyShortcuts, err = flag(raw)
		case "pauseLiveUpdates":
			next.PauseLiveUpdates, err = flag(raw)
		default:
			err = errors.New("unknown")
		}
		if err != nil {
			return s, &InvalidError{Key: key}
		}
	}
	return next, nil
}

func choice(raw json.RawMessage) (string, error) {
	var v string
	if err := strictDecode(raw, &v); err != nil {
		return "", err
	}
	if v != System && v != On && v != Off {
		return "", errors.New("not a choice")
	}
	return v, nil
}

func flag(raw json.RawMessage) (bool, error) {
	var v bool
	err := strictDecode(raw, &v)
	return v, err
}

// strictDecode decodes raw into v, refusing null (which encoding/json
// takes as "leave v as it is").
func strictDecode(raw json.RawMessage, v any) error {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("null")
	}
	return json.Unmarshal(raw, v)
}

// parseStored reads the column: what it cannot read stays at the default,
// so a row written by a later version still gives the settings this one
// knows.
func parseStored(text string) Settings {
	s := Defaults()
	var stored map[string]json.RawMessage
	if json.Unmarshal([]byte(text), &stored) != nil {
		return s
	}
	for key, raw := range stored {
		if next, err := Apply(s, map[string]json.RawMessage{key: raw}); err == nil {
			s = next
		}
	}
	return s
}

// Load reads the settings of an account. found is false when it never
// saved any; s is then Defaults().
func Load(ctx context.Context, db *store.DB, userID int64) (s Settings, found bool, err error) {
	var text string
	err = db.QueryRow(ctx, `SELECT accessibility FROM user_preferences WHERE user_id = ?`, userID).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return Defaults(), false, nil
	}
	if err != nil {
		return Defaults(), false, fmt.Errorf("read the accessibility settings of user %d: %w", userID, err)
	}
	return parseStored(text), true, nil
}

// Save stores the settings of an account.
func Save(ctx context.Context, db *store.DB, userID int64, s Settings) error {
	text, err := json.Marshal(s)
	if err != nil {
		return err
	}
	now := store.NowSQL(db.Dialect())
	_, err = db.Exec(ctx, `INSERT INTO user_preferences (user_id, accessibility, updated_at) VALUES (?, ?, `+now+`)
		ON CONFLICT(user_id) DO UPDATE SET accessibility = excluded.accessibility, updated_at = `+now, userID, string(text))
	if err != nil {
		return fmt.Errorf("store the accessibility settings of user %d: %w", userID, err)
	}
	return nil
}
