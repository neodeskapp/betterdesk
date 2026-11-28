package cluster

import (
	"context"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"
)

// Instance is one server process that uses the installation's database.
type Instance struct {
	Name      string
	Host      string
	Clustered bool // started with Valkey
	StartedAt time.Time
	LastSeen  time.Time
}

// InstanceStore keeps the instances in the database (db.Database).
type InstanceStore interface {
	// TouchInstance writes or refreshes the row of name.
	TouchInstance(inst Instance) error
	// ListInstances returns the instances seen at or after since.
	ListInstances(since time.Time) ([]Instance, error)
	// RemoveInstance deletes the row of name.
	RemoveInstance(name string) error
}

const (
	// InstanceHeartbeat is how often an instance refreshes its row.
	InstanceHeartbeat = 10 * time.Second
	// InstanceLive is how long a row counts as a running instance after
	// its last refresh: three heartbeats, so one late write is no gap.
	InstanceLive = 3 * InstanceHeartbeat
)

// CheckStart says why this instance may not start next to the instances
// already running on the database, or nil when it may. Several instances
// need a database they can share (PostgreSQL) and Valkey on every one of
// them; the message names what this one lacks. A row with this instance's
// own name is its previous run (a restart) and doesn't count.
func CheckStart(store InstanceStore, self Config, sqlite bool, now time.Time) error {
	if store == nil {
		return nil
	}
	others, err := ListOthers(store, self.Name, now)
	if err != nil {
		return fmt.Errorf("cannot read the running instances: %w", err)
	}
	if len(others) == 0 {
		return nil
	}
	var missing []string
	if sqlite {
		missing = append(missing, "a PostgreSQL database shared by all of them (DATABASE_URL=postgres://...); this one uses SQLite, a file on one disk")
	}
	if !self.Enabled() {
		missing = append(missing, "Valkey or Redis (VALKEY_URL=redis://host:6379 or REDIS_URL)")
	}
	unclustered := make([]string, 0)
	for _, o := range others {
		if !o.Clustered {
			unclustered = append(unclustered, o.Name)
		}
	}
	if len(missing) == 0 && len(unclustered) == 0 {
		return nil
	}
	names := make([]string, 0, len(others))
	for _, o := range others {
		names = append(names, fmt.Sprintf("%s (seen %s ago)", o.Name, now.Sub(o.LastSeen).Round(time.Second)))
	}
	msg := fmt.Sprintf("another NeoDesk server runs on this database: %s. Several servers can share one installation only with ", strings.Join(names, ", "))
	if len(missing) > 0 {
		msg += strings.Join(missing, " and ")
	} else {
		msg += fmt.Sprintf("Valkey set on every one of them; %s runs without VALKEY_URL", strings.Join(unclustered, ", "))
	}
	return fmt.Errorf("%s. Stop the other server first, or give every server the missing setting", msg)
}

// ListOthers returns the live instances except self, sorted by name.
func ListOthers(store InstanceStore, self string, now time.Time) ([]Instance, error) {
	all, err := store.ListInstances(now.Add(-InstanceLive))
	if err != nil {
		return nil, err
	}
	others := all[:0]
	for _, inst := range all {
		if inst.Name != self {
			others = append(others, inst)
		}
	}
	sort.Slice(others, func(i, j int) bool { return others[i].Name < others[j].Name })
	return others, nil
}

// Heartbeat writes this instance's row now and every InstanceHeartbeat
// until ctx ends, then removes it, so a stopped instance stops counting at
// once. A failed write is logged and tried again on the next beat.
func Heartbeat(ctx context.Context, store InstanceStore, self Config, started time.Time) {
	host, _ := os.Hostname()
	inst := Instance{Name: self.Name, Host: host, Clustered: self.Enabled(), StartedAt: started}
	touch := func() {
		inst.LastSeen = time.Now()
		if err := store.TouchInstance(inst); err != nil {
			log.Printf("[cluster] WARN: could not refresh this instance's row: %v", err)
		}
	}
	touch()
	t := time.NewTicker(InstanceHeartbeat)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			if err := store.RemoveInstance(inst.Name); err != nil {
				log.Printf("[cluster] WARN: could not remove this instance's row: %v", err)
			}
			return
		case <-t.C:
			touch()
		}
	}
}
