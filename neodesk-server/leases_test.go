package main

import (
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/cluster"
	"github.com/neodeskapp/neodesk/neodesk-server/db"
)

// noLease is the lease set of an instance another one beats to every
// lease.
type noLease struct{}

func (noLease) Held(string) bool { return false }
func (noLease) Close()           {}

// cleanupCounter counts the deletes the cleanup loops ask for.
type cleanupCounter struct {
	db.Database
	sessions, availability int
}

func (c *cleanupCounter) CleanupOldPeerConnectionSessions(time.Duration) (int64, error) {
	c.sessions++
	return 0, nil
}

func (c *cleanupCounter) IsAvailabilityHistoryEnabled() bool    { return true }
func (c *cleanupCounter) AvailabilityHistoryRetentionDays() int { return 30 }
func (c *cleanupCounter) CleanupOldAvailabilityEvents(time.Duration) (int64, error) {
	c.availability++
	return 0, nil
}

// TestCleanupLoopsRunOnlyOnTheLeaseHolder: an instance without the
// cleanup leases deletes nothing; a lone instance deletes.
func TestCleanupLoopsRunOnlyOnTheLeaseHolder(t *testing.T) {
	other := &cleanupCounter{}
	cleanupConnectionSessions(other, noLease{})
	cleanupAvailabilityHistory(other, noLease{})
	if other.sessions != 0 || other.availability != 0 {
		t.Errorf("without the leases: %d session and %d availability cleanups, want none", other.sessions, other.availability)
	}

	solo := &cleanupCounter{}
	cleanupConnectionSessions(solo, cluster.Solo{})
	cleanupAvailabilityHistory(solo, cluster.Solo{})
	if solo.sessions != 1 || solo.availability != 1 {
		t.Errorf("alone: %d session and %d availability cleanups, want one each", solo.sessions, solo.availability)
	}
}
