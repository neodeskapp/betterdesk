package kit

// Leases decides which instance runs the console's once-per-installation
// jobs when several instances share one database (cluster.Leases is one).
// A job asks Held before each run and skips the run when it is false.
type Leases interface {
	Held(name string) bool
}

// The leases of the console's jobs. The server's own are in the main
// package (leasenames.go).
const (
	// LeaseAlertSweep: the alert rules' sweep every minute (alerts.Engine.Run)
	LeaseAlertSweep = "alert-sweep"
	// LeaseChatWaitMail: the mails about people waiting in the chat
	LeaseChatWaitMail = "chat-wait-mail"
	// LeaseHelpRequestMail: the mail for each help request event
	LeaseHelpRequestMail = "help-request-mail"
	// LeaseAPITokenEnds: the end of what expired and deleted API tokens
	// made (admintoken.Service.Run)
	LeaseAPITokenEnds = "api-token-ends"
	// LeasePanelSessionCleanup: the delete of expired console sessions
	LeasePanelSessionCleanup = "panel-session-cleanup"
	// LeaseRetention: the hourly retention clean-up of the console's tables
	// (housekeeping.Jobs.RunIntegration)
	LeaseRetention = "retention"
	// LeaseStalePeerSync: setting stale devices offline
	// (housekeeping.Jobs.RunStatusSync)
	LeaseStalePeerSync = "stale-peer-sync"
	// LeaseSignInHousekeeping: the delete of ended access tokens and old
	// sign-in attempts
	LeaseSignInHousekeeping = "sign-in-housekeeping"
	// LeaseOnboardSweep: the quick support sweep: ending sessions,
	// removing their temporary devices, pruning old records and codes
	LeaseOnboardSweep = "onboard-sweep"
	// LeaseAccountLifeCycle: end dates, expiry notices and the rule for
	// inactive accounts
	LeaseAccountLifeCycle = "account-life-cycle"
	// LeaseScheduledBackups: the scheduled backup
	LeaseScheduledBackups = "scheduled-backups"
	// LeaseNetworkMonitor: the network monitor's polls
	LeaseNetworkMonitor = "network-monitor"
)

// Held reports whether this instance runs the job of the lease name now.
// Without a lease set (a lone console, and tests) it runs every job.
func (d *Deps) Held(name string) bool {
	if d == nil || d.Leases == nil {
		return true
	}
	return d.Leases.Held(name)
}

// LeaseFunc is Held for one name, for a service that takes a func() bool.
func (d *Deps) LeaseFunc(name string) func() bool {
	return func() bool { return d.Held(name) }
}
