package activity

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsjson"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// The console keeps its own table of devices, peer, beside the signal
// server's table, peers. The routes name a device by what they find in
// either; they write to neither.

// peer is what the routes read of a device the console knows.
type peer struct {
	note string
	// info is the device's info as its column holds it. Not valid stands
	// for NULL.
	info sql.NullString
	// document says the column is a PostgreSQL JSON column, which holds a
	// JSON value. A text column holds text that should be JSON.
	document bool
}

// parsedInfo is the info as the routes saw it after JSON.parse. It fails
// where JSON.parse threw.
//
// From a text column, no info is an empty object and the rest is parsed.
// From a JSON column, a text is parsed like the text of a text column,
// NULL is null, and every other value is itself (a number or a flag has
// no members, like an empty object).
func (p *peer) parsedInfo() (jsjson.Value, error) {
	text := p.info.String
	if p.document {
		if !p.info.Valid {
			return jsjson.NullValue, nil
		}
		value, err := jsjson.Parse(text)
		if err != nil {
			return jsjson.Value{}, fmt.Errorf("read the info document: %w", err)
		}
		if value.Kind() != jsjson.String {
			return value, nil
		}
		text = value.Str()
	}
	if text == "" {
		return jsjson.Parse("{}")
	}
	value, err := jsjson.Parse(text)
	if err == nil && value.Kind() == jsjson.Null {
		// The lookup itself read members of what the text holds
		err = errNullInfo
	}
	return value, err
}

// peerByID returns the device with the id as the console knows it, nil
// when neither table has it: its row in the console's table, unless that
// is marked deleted, else its row in the signal server's table. Nothing
// is written. What goes wrong while reading the signal server's table is
// logged and the device counts as unknown.
func (h *handlers) peerByID(ctx context.Context, id string) (*peer, error) {
	p, err := readPeer(ctx, h.db, id)
	if err != nil || p != nil {
		return p, err
	}
	p, err = signalServerPeer(ctx, h.db, id)
	if err != nil {
		// The signal server's table is missing until the server has run
		if message := jsbind.Message(err); !strings.Contains(message, "no such table") && !strings.Contains(message, "does not exist") {
			h.log.Warn("Fallback peers lookup error:", err)
		}
		return nil, nil
	}
	return p, nil
}

// readPeer reads the device's row in the console's table, unless it is
// marked deleted.
func readPeer(ctx context.Context, db *store.DB, id string) (*peer, error) {
	rows, err := db.Query(ctx, "SELECT note, info FROM peer WHERE id = ? AND "+db.Pick("is_deleted = 0", "NOT is_deleted"), id)
	if err != nil {
		return nil, fmt.Errorf("load device %s: %w", id, err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("load device %s: %w", id, err)
		}
		return nil, nil
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, fmt.Errorf("load device %s: %w", id, err)
	}
	var note sql.NullString
	p := &peer{document: strings.HasPrefix(strings.ToUpper(types[1].DatabaseTypeName()), "JSON")}
	if err := rows.Scan(&note, &p.info); err != nil {
		return nil, fmt.Errorf("load device %s: %w", id, err)
	}
	p.note = note.String
	return p, rows.Close()
}

// signalServerPeer reads a device in the signal server's table, unless it
// is deleted there: no note, and an info with the hostname the signal
// server has for it.
func signalServerPeer(ctx context.Context, db *store.DB, id string) (*peer, error) {
	var hostname jsbind.Cell
	err := db.QueryRow(ctx, "SELECT hostname FROM peers WHERE id = ? AND NOT soft_deleted", id).Scan(&hostname)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("look device %s up in the signal server's table: %w", id, err)
	}
	info := `{"hostname":` + hostname.Value().Or(jsbind.String("")).JSON() + `}`
	return &peer{info: sql.NullString{String: info, Valid: true}}, nil
}

// errNullInfo is the TypeError of reading a member of null: the info of
// a device was the text "null".
var errNullInfo = errors.New("Cannot read properties of null")
