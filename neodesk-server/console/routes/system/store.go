package system

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// safeIdentPattern is a table or column name that can stand in a query
// without quoting: letters, digits and underscores, not starting with a
// digit.
var safeIdentPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// isSafeSQLIdent reports whether name can be put into a query as it is.
func isSafeSQLIdent(name string) bool { return safeIdentPattern.MatchString(name) }

// assertSafeSQLIdent returns name when it can be put into a query as it
// is, and an error otherwise.
func assertSafeSQLIdent(name string) (string, error) {
	if !isSafeSQLIdent(name) {
		return "", fmt.Errorf("Unsafe SQL identifier: %s", name)
	}
	return name, nil
}

type tableCount struct {
	Name string `json:"name"`
	// Count is a number on SQLite and a text on PostgreSQL, whose
	// counts are 64-bit numbers the old console read as text.
	Count any `json:"count"`
}

type databaseStats struct {
	Type       string       `json:"type"`
	Tables     []tableCount `json:"tables"`
	Size       string       `json:"size"`
	LastBackup string       `json:"last_backup,omitempty"`
}

// databaseStats answers GET /api/database/stats: the type of the
// database, the row count of each table, its size and the time of the
// newest backup. A database that cannot be listed is 500.
func (h *handlers) databaseStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.collectStats(r.Context())
	if err != nil {
		h.log.Error("database stats:", err)
		kit.JSON(w, r, http.StatusInternalServerError, plainError{"Failed to get database stats"})
		return
	}
	kit.JSON(w, r, http.StatusOK, stats)
}

// collectStats counts the rows of every table with a safe name and reads
// the size of the database and the time of the newest backup file.
func (h *handlers) collectStats(ctx context.Context) (databaseStats, error) {
	stats := databaseStats{Type: h.db.Pick("SQLite", "PostgreSQL"), Tables: []tableCount{}, Size: "-"}
	names, err := tableNames(ctx, h.db)
	if err != nil {
		return stats, err
	}
	for _, name := range names {
		// A table with an unsafe name, or one that cannot be counted, is
		// left out
		if table, err := countRows(ctx, h.db, name); err == nil {
			stats.Tables = append(stats.Tables, table)
		}
	}
	if h.db.IsPostgres() {
		var size string
		if err := h.db.QueryRow(ctx, "SELECT pg_size_pretty(pg_database_size(current_database())) AS size").Scan(&size); err == nil {
			stats.Size = size
		}
	} else if info, err := os.Stat(h.o.DatabaseFile); err == nil {
		kb := float64(info.Size()) / 1024
		if kb > 1024 {
			stats.Size = strconv.FormatFloat(kb/1024, 'f', 1, 64) + " MB"
		} else {
			stats.Size = strconv.FormatFloat(math.Floor(kb+0.5), 'f', 0, 64) + " KB"
		}
	}
	if entries, err := os.ReadDir(h.o.BackupDir); err == nil {
		var files []string
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasSuffix(name, ".db") || strings.HasSuffix(name, ".sql") || strings.HasSuffix(name, ".gz") {
				files = append(files, name)
			}
		}
		if len(files) > 0 {
			sort.Strings(files)
			if info, err := os.Stat(filepath.Join(h.o.BackupDir, files[len(files)-1])); err == nil {
				stats.LastBackup = info.ModTime().UTC().Format("2006-01-02T15:04:05.000Z")
			}
		}
	}
	return stats, nil
}

// countRows counts the rows of a table whose name is safe to put into a
// query.
func countRows(ctx context.Context, db *store.DB, name string) (tableCount, error) {
	safe, err := assertSafeSQLIdent(name)
	if err != nil {
		return tableCount{}, err
	}
	var count int64
	if err := db.QueryRow(ctx, `SELECT COUNT(*) AS count FROM "`+safe+`"`).Scan(&count); err != nil {
		return tableCount{}, fmt.Errorf("count the rows of %s: %w", safe, err)
	}
	if db.IsPostgres() {
		return tableCount{safe, strconv.FormatInt(count, 10)}, nil
	}
	return tableCount{safe, count}, nil
}

// tableNames lists the tables of the database by name. On PostgreSQL
// these are the tables of the schema "public".
func tableNames(ctx context.Context, db *store.DB) ([]string, error) {
	query := db.Pick(
		"SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name",
		"SELECT tablename AS name FROM pg_catalog.pg_tables WHERE schemaname = 'public' ORDER BY tablename",
	)
	rows, err := db.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list tables: %w", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("read a table name: %w", err)
		}
		names = append(names, name)
	}
	return names, rows.Err()
}
