package inventory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/unitronix/betterdesk-server/console/routes/internal/jsjson"
	"github.com/unitronix/betterdesk-server/console/store"
)

// The console keeps its own table of devices, peer, beside the signal
// server's table, peers. An upload writes the info of a device's row in
// peer, if peer has one that is not marked deleted, and writes no other
// row of either table.
//
// The two databases keep the info differently: SQLite as text, PostgreSQL
// in a JSON column (jsonb), which holds any JSON value and normalizes an
// object (its keys sorted by length, then by name).

// peer is what the upload reads of a device the console knows.
type peer struct {
	// info is the device's info as its column holds it. Not valid stands
	// for NULL.
	info sql.NullString
	// document says the column is a PostgreSQL JSON column, which holds a
	// JSON value. A text column holds text that should be JSON.
	document bool
}

// parsedInfo is the info as the upload saw it after JSON.parse. It fails
// where the lookup or JSON.parse threw.
//
// From a text column, no info is an empty object and the rest is parsed;
// the text null failed already in the lookup, which read members of what
// it holds. From a JSON column, a text is parsed like the text of a text
// column, NULL is null, and every other value is itself (a number or a
// flag has no members, like an empty object).
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
		err = errNullInfo
	}
	return value, err
}

// errNullInfo is the TypeError of reading a member of null: the info of
// a device was the text "null".
var errNullInfo = errors.New("Cannot read properties of null")

// readPeer reads the info of the device's row in the console's table,
// unless the row is marked deleted.
func readPeer(ctx context.Context, db *store.DB, id string) (*peer, error) {
	rows, err := db.Query(ctx, "SELECT info FROM peer WHERE id = ? AND "+db.Pick("is_deleted = 0", "NOT is_deleted"), id)
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
	p := &peer{document: strings.HasPrefix(strings.ToUpper(types[0].DatabaseTypeName()), "JSON")}
	if err := rows.Scan(&p.info); err != nil {
		return nil, fmt.Errorf("load device %s: %w", id, err)
	}
	return p, rows.Close()
}

// updatePeer is updatePeer(id, {info}): the device's info becomes the
// JSON text.
func updatePeer(ctx context.Context, db *store.DB, id, info string) error {
	if _, err := db.Exec(ctx, "UPDATE peer SET info = ? WHERE id = ?", info, id); err != nil {
		return fmt.Errorf("update the info of device %s: %w", id, err)
	}
	return nil
}
