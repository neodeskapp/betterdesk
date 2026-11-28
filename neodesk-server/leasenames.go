package main

// The leases of the server's own once-per-installation jobs
// (cluster.Leases). With several instances only the one that holds a
// lease runs its job; a lone instance holds them all. The console's
// leases are in console/kit/leases.go.
const (
	// leaseAvailabilityCleanup: the hourly delete of expired
	// peer_availability_events (runAvailabilityHistoryCleanup)
	leaseAvailabilityCleanup = "availability-cleanup"
	// leaseConnectionSessionCleanup: the hourly delete of old Metrics
	// connection sessions (runConnectionSessionCleanup)
	leaseConnectionSessionCleanup = "connection-session-cleanup"
	// leaseAuditPrune: the hourly delete of server events older than
	// audit.RetentionDays; every instance still writes its own events
	leaseAuditPrune = "audit-prune"
	// leaseFleetScheduler: the fleet scheduler's 30-second round (due
	// tasks, stale runs, scheduled workflows, compliance scans)
	leaseFleetScheduler = "fleet-scheduler"
)
