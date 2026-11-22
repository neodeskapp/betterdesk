package reports

import (
	"context"
	"fmt"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	engine "github.com/unitronix/betterdesk-server/console/reports"
	"github.com/unitronix/betterdesk-server/console/store"
)

// The saved-report methods of services/dbAdapter.js. The rows come back
// as the old drivers gave them: filters and payload as text on SQLite and
// as parsed JSON on PostgreSQL, created_at as the stored text on SQLite
// and a time on PostgreSQL. On SQLite created_at is declared DATETIME,
// which the Go driver would turn into a time; the second created_at
// (+created_at has no declared type) puts the stored text in its place.
//
// Ids are bound as Number(req.params.id) was: NaN finds nothing on SQLite
// and is an error on PostgreSQL, like a fraction or a number beyond the
// column's range.

// savedReports is getSavedReports(): every saved report, newest first.
func savedReports(ctx context.Context, db *store.DB) ([]*goapi.Object, error) {
	rows, err := engine.SelectRows(ctx, db, nil, db.Pick(
		"SELECT *, +created_at AS created_at FROM saved_reports ORDER BY saved_reports.created_at DESC",
		"SELECT * FROM saved_reports ORDER BY created_at DESC",
	))
	if err != nil {
		return nil, fmt.Errorf("list the saved reports: %w", err)
	}
	return rows, nil
}

// savedReportByID is getSavedReportById(id); nil when there is none.
func savedReportByID(ctx context.Context, db *store.DB, id jsbind.Value) (*goapi.Object, error) {
	args, err := jsbind.Args(db.Dialect(), 1, id)
	if err != nil {
		return nil, err
	}
	rows, err := engine.SelectRows(ctx, db, nil, db.Pick(
		"SELECT *, +created_at AS created_at FROM saved_reports WHERE id = ?",
		"SELECT * FROM saved_reports WHERE id = ?",
	), args...)
	if err != nil {
		return nil, fmt.Errorf("read a saved report: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return rows[0], nil
}

// newSavedReport is what createSavedReport stores. Title and type are
// the request's values, bound as the old drivers bound them.
type newSavedReport struct {
	title, reportType           jsbind.Value
	filters, payload, createdBy string
}

// createSavedReport is createSavedReport(data): it stores the report and
// returns the row as it reads back.
func createSavedReport(ctx context.Context, db *store.DB, data newSavedReport) (*goapi.Object, error) {
	args, err := jsbind.Args(db.Dialect(), 5, data.title, data.reportType,
		jsbind.String(data.filters), jsbind.String(data.payload), jsbind.String(data.createdBy))
	if err != nil {
		return nil, err
	}
	if db.IsPostgres() {
		rows, err := engine.SelectRows(ctx, db, nil, `
                INSERT INTO saved_reports (title, report_type, filters, payload, created_by)
                VALUES (?, ?, ?, ?, ?)
                RETURNING *
            `, args...)
		if err != nil {
			return nil, fmt.Errorf("save a report: %w", err)
		}
		if len(rows) == 0 {
			return nil, nil
		}
		return rows[0], nil
	}
	id, err := db.Insert(ctx, `
                INSERT INTO saved_reports (title, report_type, filters, payload, created_by)
                VALUES (?, ?, ?, ?, ?)
            `, args...)
	if err != nil {
		return nil, fmt.Errorf("save a report: %w", err)
	}
	return savedReportByID(ctx, db, jsbind.Number(float64(id)))
}

// deleteSavedReport is deleteSavedReport(id): whether a row was deleted.
func deleteSavedReport(ctx context.Context, db *store.DB, id jsbind.Value) (bool, error) {
	args, err := jsbind.Args(db.Dialect(), 1, id)
	if err != nil {
		return false, err
	}
	result, err := db.Exec(ctx, "DELETE FROM saved_reports WHERE id = ?", args...)
	if err != nil {
		return false, fmt.Errorf("delete a saved report: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("delete a saved report: %w", err)
	}
	return n > 0, nil
}
