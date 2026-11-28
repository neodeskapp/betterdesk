// Package dashboards keeps the layouts of the console's overview: the
// boards an account arranged for itself, and the shared boards an admin
// publishes for everyone or for some roles.
//
// An account's own boards are one row per account in user_dashboards, as
// JSON in its boards column; an account that never saved has no row and
// sees the web app's default. The shared boards are one JSON array in the
// settings table under the key dashboards.shared.
//
// A board is a list of widgets. The server does not know the widget
// types: it checks that a type looks like one and keeps it, and the web
// app leaves out the types it does not know. What a widget shows is in
// its config, an object the server keeps as it came, up to a size.
//
// Everything that comes from a browser is decoded strictly (Parse,
// ParseShared): a key the server does not know, a value of the wrong kind
// or null where a value is needed refuses the whole change with an
// InvalidError that says what is wrong. What is stored is the canonical
// form, written again from the decoded values. What is read back from the
// database is decoded leniently: keys a later version added are skipped,
// and data that cannot be read counts as none saved.
package dashboards

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The limits of a layout.
const (
	// MaxBoards is the most boards an account keeps, and the most shared
	// boards.
	MaxBoards = 20
	// MaxWidgets is the most widgets on one board.
	MaxWidgets = 40
	// MaxNameLength is the longest board name and widget title, in
	// characters.
	MaxNameLength = 80
	// MaxConfigBytes is the largest widget config, as compact JSON.
	MaxConfigBytes = 8192
	// MaxHidden is the most boards an account hides.
	MaxHidden = 50
	// MaxRoles is the most roles a shared board names.
	MaxRoles = 20
	// MaxRoleLength is the longest role name, in characters.
	MaxRoleLength = 40
	// MaxBodyBytes is the largest layout a browser may send.
	MaxBodyBytes = 256 << 10
)

// SharedKey is the settings key of the shared boards.
const SharedKey = "dashboards.shared"

var (
	idPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,39}$`)
	typePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
	sizes       = map[string]bool{"s": true, "m": true, "l": true, "full": true}
)

// Widget is one tile of a board.
type Widget struct {
	ID string `json:"id"`
	// Type names what the widget is; the web app knows the types.
	Type string `json:"type"`
	// Size is s, m, l or full (the whole width).
	Size string `json:"size"`
	// Title replaces the widget's own heading when it is not empty.
	Title string `json:"title,omitempty"`
	// Config is the widget's own settings, a JSON object; nil when it has
	// none.
	Config json.RawMessage `json:"config,omitempty"`
}

// Board is a named list of widgets.
type Board struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Widgets []Widget `json:"widgets"`
}

// Shared is a board an admin publishes.
type Shared struct {
	Board
	// Roles are the roles that see the board; empty means every account.
	Roles []string `json:"roles"`
	// Locked boards cannot be hidden by the accounts that see them.
	Locked bool `json:"locked"`
	// Home marks the board the overview opens on for an account that
	// chose none.
	Home bool `json:"home"`
}

// Own is the layout an account keeps for itself.
type Own struct {
	Boards []Board `json:"boards"`
	// Active is the id of the board the overview opens on, "" for the
	// default. It may name an own board or a shared one.
	Active string `json:"active"`
	// Hidden are the ids of the shared boards the account does not want
	// to see.
	Hidden []string `json:"hidden"`
}

// InvalidError says what is wrong with a layout, in a sentence the
// console can show.
type InvalidError struct {
	Message string
}

func (e *InvalidError) Error() string { return e.Message }

func invalid(format string, args ...any) error {
	return &InvalidError{Message: fmt.Sprintf(format, args...)}
}

// decoder reads a layout. A strict one refuses keys it does not know; a
// lenient one, for what the database holds, skips them.
type decoder struct {
	lenient bool
}

func isNull(raw json.RawMessage) bool {
	t := bytes.TrimSpace(raw)
	return len(t) == 0 || bytes.Equal(t, []byte("null"))
}

// object reads raw as a JSON object with the required and optional keys.
// A required key that is missing or null fails; an optional one that is
// null counts as missing.
func (d decoder) object(raw json.RawMessage, where string, required, optional []string) (map[string]json.RawMessage, error) {
	t := bytes.TrimSpace(raw)
	if len(t) == 0 || t[0] != '{' {
		return nil, invalid("%s must be a JSON object.", where)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(t, &m); err != nil {
		return nil, invalid("%s must be a JSON object.", where)
	}
	known := map[string]bool{}
	for _, k := range required {
		known[k] = true
		if isNull(m[k]) {
			return nil, invalid("%s needs %q.", where, k)
		}
	}
	for _, k := range optional {
		known[k] = true
		if v, ok := m[k]; ok && isNull(v) {
			delete(m, k)
		}
	}
	for k := range m {
		if !known[k] {
			if !d.lenient {
				return nil, invalid("%s has an unknown key %q.", where, k)
			}
			delete(m, k)
		}
	}
	return m, nil
}

func (d decoder) str(m map[string]json.RawMessage, key, where string) (string, error) {
	var s string
	if raw, ok := m[key]; ok {
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", invalid("%s: %q must be a string.", where, key)
		}
	}
	return s, nil
}

func (d decoder) boolean(m map[string]json.RawMessage, key, where string) (bool, error) {
	var b bool
	if raw, ok := m[key]; ok {
		if err := json.Unmarshal(raw, &b); err != nil {
			return false, invalid("%s: %q must be true or false.", where, key)
		}
	}
	return b, nil
}

// list reads a JSON array; a missing key is an empty one.
func (d decoder) list(m map[string]json.RawMessage, key, where string) ([]json.RawMessage, error) {
	var l []json.RawMessage
	if raw, ok := m[key]; ok {
		t := bytes.TrimSpace(raw)
		if len(t) == 0 || t[0] != '[' || json.Unmarshal(t, &l) != nil {
			return nil, invalid("%s: %q must be a list.", where, key)
		}
	}
	return l, nil
}

func (d decoder) strings(m map[string]json.RawMessage, key, where string) ([]string, error) {
	items, err := d.list(m, key, where)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(items))
	for _, raw := range items {
		var s string
		if isNull(raw) || json.Unmarshal(raw, &s) != nil {
			return nil, invalid("%s: %q must be a list of strings.", where, key)
		}
		out = append(out, s)
	}
	return out, nil
}

// name checks a board name or widget title: trimmed, at most
// MaxNameLength characters, and not empty when required.
func name(s, what, where string, required bool) (string, error) {
	s = strings.TrimSpace(s)
	if required && s == "" {
		return "", invalid("%s needs a %s.", where, what)
	}
	if utf8.RuneCountInString(s) > MaxNameLength {
		return "", invalid("%s: the %s is longer than %d characters.", where, what, MaxNameLength)
	}
	return s, nil
}

func checkID(id, where string) error {
	if !idPattern.MatchString(id) {
		return invalid("%s: the id %q must be 1 to 40 lowercase letters, digits and dashes, starting with a letter or digit.", where, id)
	}
	return nil
}

func (d decoder) widget(raw json.RawMessage, where string) (Widget, error) {
	m, err := d.object(raw, where, []string{"id", "type", "size"}, []string{"title", "config"})
	if err != nil {
		return Widget{}, err
	}
	var w Widget
	if w.ID, err = d.str(m, "id", where); err != nil {
		return Widget{}, err
	}
	if err := checkID(w.ID, where); err != nil {
		return Widget{}, err
	}
	if w.Type, err = d.str(m, "type", where); err != nil {
		return Widget{}, err
	}
	if !typePattern.MatchString(w.Type) {
		return Widget{}, invalid("%s: the type %q must be 1 to 40 lowercase letters, digits and dashes, starting with a letter.", where, w.Type)
	}
	if w.Size, err = d.str(m, "size", where); err != nil {
		return Widget{}, err
	}
	if !sizes[w.Size] {
		return Widget{}, invalid("%s: the size %q must be s, m, l or full.", where, w.Size)
	}
	title, err := d.str(m, "title", where)
	if err != nil {
		return Widget{}, err
	}
	if w.Title, err = name(title, "title", where, false); err != nil {
		return Widget{}, err
	}
	if raw, ok := m["config"]; ok {
		t := bytes.TrimSpace(raw)
		var buf bytes.Buffer
		if len(t) == 0 || t[0] != '{' || json.Compact(&buf, t) != nil {
			return Widget{}, invalid("%s: the config must be a JSON object.", where)
		}
		if buf.Len() > MaxConfigBytes {
			return Widget{}, invalid("%s: the config is larger than %d bytes.", where, MaxConfigBytes)
		}
		if buf.String() != "{}" {
			w.Config = json.RawMessage(buf.Bytes())
		}
	}
	return w, nil
}

// board reads the keys a board and a shared board have in common from m.
func (d decoder) board(m map[string]json.RawMessage, where string) (Board, error) {
	var b Board
	var err error
	if b.ID, err = d.str(m, "id", where); err != nil {
		return Board{}, err
	}
	if err := checkID(b.ID, where); err != nil {
		return Board{}, err
	}
	n, err := d.str(m, "name", where)
	if err != nil {
		return Board{}, err
	}
	if b.Name, err = name(n, "name", where, true); err != nil {
		return Board{}, err
	}
	items, err := d.list(m, "widgets", where)
	if err != nil {
		return Board{}, err
	}
	if len(items) > MaxWidgets {
		return Board{}, invalid("%s has more than %d widgets.", where, MaxWidgets)
	}
	b.Widgets = make([]Widget, 0, len(items))
	seen := map[string]bool{}
	for i, raw := range items {
		w, err := d.widget(raw, fmt.Sprintf("Widget %d of %s", i+1, strings.ToLower(where[:1])+where[1:]))
		if err != nil {
			return Board{}, err
		}
		if seen[w.ID] {
			return Board{}, invalid("%s has two widgets with the id %q.", where, w.ID)
		}
		seen[w.ID] = true
		b.Widgets = append(b.Widgets, w)
	}
	return b, nil
}

func (d decoder) own(raw json.RawMessage) (Own, error) {
	m, err := d.object(raw, "The layout", []string{"boards"}, []string{"active", "hidden"})
	if err != nil {
		return Own{}, err
	}
	items, err := d.list(m, "boards", "The layout")
	if err != nil {
		return Own{}, err
	}
	if len(items) > MaxBoards {
		return Own{}, invalid("The layout has more than %d boards.", MaxBoards)
	}
	o := Own{Boards: make([]Board, 0, len(items))}
	seen := map[string]bool{}
	for i, raw := range items {
		where := fmt.Sprintf("Board %d", i+1)
		bm, err := d.object(raw, where, []string{"id", "name", "widgets"}, nil)
		if err != nil {
			return Own{}, err
		}
		b, err := d.board(bm, where)
		if err != nil {
			return Own{}, err
		}
		if seen[b.ID] {
			return Own{}, invalid("The layout has two boards with the id %q.", b.ID)
		}
		seen[b.ID] = true
		o.Boards = append(o.Boards, b)
	}
	if o.Active, err = d.str(m, "active", "The layout"); err != nil {
		return Own{}, err
	}
	if o.Active != "" {
		if err := checkID(o.Active, "The active board"); err != nil {
			return Own{}, err
		}
	}
	if o.Hidden, err = d.strings(m, "hidden", "The layout"); err != nil {
		return Own{}, err
	}
	if len(o.Hidden) > MaxHidden {
		return Own{}, invalid("The layout hides more than %d boards.", MaxHidden)
	}
	for _, id := range o.Hidden {
		if err := checkID(id, "A hidden board"); err != nil {
			return Own{}, err
		}
	}
	return o, nil
}

func (d decoder) shared(raw json.RawMessage) ([]Shared, error) {
	m, err := d.object(raw, "The shared boards", []string{"boards"}, nil)
	if err != nil {
		return nil, err
	}
	items, err := d.list(m, "boards", "The shared boards")
	if err != nil {
		return nil, err
	}
	if len(items) > MaxBoards {
		return nil, invalid("There are more than %d shared boards.", MaxBoards)
	}
	out := make([]Shared, 0, len(items))
	seen := map[string]bool{}
	for i, raw := range items {
		where := fmt.Sprintf("Shared board %d", i+1)
		bm, err := d.object(raw, where, []string{"id", "name", "widgets"}, []string{"roles", "locked", "home"})
		if err != nil {
			return nil, err
		}
		b, err := d.board(bm, where)
		if err != nil {
			return nil, err
		}
		if seen[b.ID] {
			return nil, invalid("There are two shared boards with the id %q.", b.ID)
		}
		seen[b.ID] = true
		s := Shared{Board: b}
		roles, err := d.strings(bm, "roles", where)
		if err != nil {
			return nil, err
		}
		if len(roles) > MaxRoles {
			return nil, invalid("%s names more than %d roles.", where, MaxRoles)
		}
		s.Roles = make([]string, 0, len(roles))
		for _, role := range roles {
			role = strings.TrimSpace(role)
			if role == "" || utf8.RuneCountInString(role) > MaxRoleLength {
				return nil, invalid("%s: every role must be 1 to %d characters.", where, MaxRoleLength)
			}
			s.Roles = append(s.Roles, role)
		}
		if s.Locked, err = d.boolean(bm, "locked", where); err != nil {
			return nil, err
		}
		if s.Home, err = d.boolean(bm, "home", where); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func tooLarge(raw []byte) error {
	if len(raw) > MaxBodyBytes {
		return invalid("The layout is larger than %d KB.", MaxBodyBytes>>10)
	}
	return nil
}

// Parse reads an account's layout as a browser sends it, strictly. It
// fails with an InvalidError.
func Parse(raw []byte) (Own, error) {
	if err := tooLarge(raw); err != nil {
		return Own{}, err
	}
	return decoder{}.own(raw)
}

// ParseShared reads the shared boards as an admin sends them, an object
// with the key boards, strictly. It fails with an InvalidError.
func ParseShared(raw []byte) ([]Shared, error) {
	if err := tooLarge(raw); err != nil {
		return nil, err
	}
	return decoder{}.shared(raw)
}

// VisibleTo returns the shared boards an account of role sees: those for
// every account and those that name the role. An account that may
// publish (all) sees every board.
func VisibleTo(boards []Shared, role string, all bool) []Shared {
	out := make([]Shared, 0, len(boards))
	for _, b := range boards {
		if all || len(b.Roles) == 0 || contains(b.Roles, role) {
			out = append(out, b)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Load reads the layout of an account: nil when it never saved one, or
// when what it saved cannot be read.
func Load(ctx context.Context, db *store.DB, userID int64) (*Own, error) {
	var text string
	err := db.QueryRow(ctx, `SELECT boards FROM user_dashboards WHERE user_id = ?`, userID).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read the dashboards of user %d: %w", userID, err)
	}
	o, err := decoder{lenient: true}.own(json.RawMessage(text))
	if err != nil {
		return nil, nil
	}
	return &o, nil
}

// Save stores the layout of an account.
func Save(ctx context.Context, db *store.DB, userID int64, o Own) error {
	text, err := json.Marshal(o)
	if err != nil {
		return err
	}
	now := store.NowSQL(db.Dialect())
	_, err = db.Exec(ctx, `INSERT INTO user_dashboards (user_id, boards, updated_at) VALUES (?, ?, `+now+`)
		ON CONFLICT(user_id) DO UPDATE SET boards = excluded.boards, updated_at = `+now, userID, string(text))
	if err != nil {
		return fmt.Errorf("store the dashboards of user %d: %w", userID, err)
	}
	return nil
}

// Reset removes the layout of an account, which then sees the default.
func Reset(ctx context.Context, db *store.DB, userID int64) error {
	if _, err := db.Exec(ctx, `DELETE FROM user_dashboards WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("reset the dashboards of user %d: %w", userID, err)
	}
	return nil
}

// LoadShared reads the shared boards. It is empty, never nil, when none
// were published or what is stored cannot be read.
func LoadShared(ctx context.Context, db *store.DB) ([]Shared, error) {
	var text sql.NullString
	err := db.QueryRow(ctx, `SELECT value FROM settings WHERE key = ?`, SharedKey).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return []Shared{}, nil
	}
	if err != nil {
		return []Shared{}, fmt.Errorf("read the shared dashboards: %w", err)
	}
	stored := []byte(`{"boards":` + text.String + `}`)
	boards, err := decoder{lenient: true}.shared(stored)
	if err != nil || !text.Valid {
		return []Shared{}, nil
	}
	return boards, nil
}

// SaveShared stores the shared boards as a JSON array.
func SaveShared(ctx context.Context, db *store.DB, boards []Shared) error {
	if boards == nil {
		boards = []Shared{}
	}
	text, err := json.Marshal(boards)
	if err != nil {
		return err
	}
	now := store.NowSQL(db.Dialect())
	_, err = db.Exec(ctx, `INSERT INTO settings (key, value, updated_at) VALUES (?, ?, `+now+`)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = `+now, SharedKey, string(text))
	if err != nil {
		return fmt.Errorf("store the shared dashboards: %w", err)
	}
	return nil
}
