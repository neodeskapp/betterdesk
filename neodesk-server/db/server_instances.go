package db

import (
	"time"
)

// ServerInstance is one server process that uses this database, written
// every few seconds while it runs (cluster.Heartbeat). Times are unix
// milliseconds in both databases.
type ServerInstance struct {
	Name      string
	Host      string
	Clustered bool
	StartedAt time.Time
	LastSeen  time.Time
}

const serverInstancesSQLite = `CREATE TABLE IF NOT EXISTS server_instances (
	name TEXT PRIMARY KEY,
	host TEXT NOT NULL DEFAULT '',
	clustered INTEGER NOT NULL DEFAULT 0,
	started_at INTEGER NOT NULL,
	last_seen INTEGER NOT NULL
)`

const serverInstancesPostgres = `CREATE TABLE IF NOT EXISTS server_instances (
	name TEXT PRIMARY KEY,
	host TEXT NOT NULL DEFAULT '',
	clustered BOOLEAN NOT NULL DEFAULT FALSE,
	started_at BIGINT NOT NULL,
	last_seen BIGINT NOT NULL
)`

func (s *SQLiteDB) TouchServerInstance(inst ServerInstance) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	clustered := 0
	if inst.Clustered {
		clustered = 1
	}
	_, err := s.db.Exec(`INSERT INTO server_instances (name, host, clustered, started_at, last_seen)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET host = excluded.host, clustered = excluded.clustered,
			started_at = excluded.started_at, last_seen = excluded.last_seen`,
		inst.Name, inst.Host, clustered, inst.StartedAt.UnixMilli(), inst.LastSeen.UnixMilli())
	return err
}

func (s *SQLiteDB) ListServerInstances(since time.Time) ([]ServerInstance, error) {
	rows, err := s.db.Query(`SELECT name, host, clustered, started_at, last_seen FROM server_instances WHERE last_seen >= ? ORDER BY name`, since.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServerInstance
	for rows.Next() {
		var inst ServerInstance
		var clustered int
		var started, seen int64
		if err := rows.Scan(&inst.Name, &inst.Host, &clustered, &started, &seen); err != nil {
			return nil, err
		}
		inst.Clustered = clustered != 0
		inst.StartedAt = time.UnixMilli(started)
		inst.LastSeen = time.UnixMilli(seen)
		out = append(out, inst)
	}
	return out, rows.Err()
}

func (s *SQLiteDB) RemoveServerInstance(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM server_instances WHERE name = ?`, name)
	return err
}

func (pg *PostgresDB) TouchServerInstance(inst ServerInstance) error {
	_, err := pg.pool.Exec(pg.ctx, `INSERT INTO server_instances (name, host, clustered, started_at, last_seen)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (name) DO UPDATE SET host = EXCLUDED.host, clustered = EXCLUDED.clustered,
			started_at = EXCLUDED.started_at, last_seen = EXCLUDED.last_seen`,
		inst.Name, inst.Host, inst.Clustered, inst.StartedAt.UnixMilli(), inst.LastSeen.UnixMilli())
	return err
}

func (pg *PostgresDB) ListServerInstances(since time.Time) ([]ServerInstance, error) {
	rows, err := pg.pool.Query(pg.ctx, `SELECT name, host, clustered, started_at, last_seen FROM server_instances WHERE last_seen >= $1 ORDER BY name`, since.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServerInstance
	for rows.Next() {
		var inst ServerInstance
		var started, seen int64
		if err := rows.Scan(&inst.Name, &inst.Host, &inst.Clustered, &started, &seen); err != nil {
			return nil, err
		}
		inst.StartedAt = time.UnixMilli(started)
		inst.LastSeen = time.UnixMilli(seen)
		out = append(out, inst)
	}
	return out, rows.Err()
}

func (pg *PostgresDB) RemoveServerInstance(name string) error {
	_, err := pg.pool.Exec(pg.ctx, `DELETE FROM server_instances WHERE name = $1`, name)
	return err
}
