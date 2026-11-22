package reports

import (
	"context"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/store"
)

// SelectRows runs a query and returns its rows as the old console's
// drivers returned them: one object per row with the columns in order,
// each value as fromDriver makes it. Of two columns with one name the
// object keeps the place of the first and the value of the last, as a
// JavaScript row object did.
//
// local is the zone the old console read PostgreSQL times without a
// zone in; nil is time.Local.
func SelectRows(ctx context.Context, q store.Querier, local *time.Location, query string, args ...any) ([]*goapi.Object, error) {
	if local == nil {
		local = time.Local
	}
	rows, err := q.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, err
	}
	names := make([]string, len(types))
	typeNames := make([]string, len(types))
	for i, t := range types {
		names[i], typeNames[i] = t.Name(), t.DatabaseTypeName()
	}
	out := []*goapi.Object{}
	values := make([]any, len(types))
	targets := make([]any, len(types))
	for i := range values {
		targets[i] = &values[i]
	}
	for rows.Next() {
		for i := range values {
			values[i] = nil
		}
		if err := rows.Scan(targets...); err != nil {
			return nil, err
		}
		row := goapi.NewObject()
		for i, name := range names {
			row.Set(name, fromDriver(q.Dialect(), typeNames[i], values[i], local))
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
