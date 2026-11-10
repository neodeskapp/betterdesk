package contract

// MissingID is the placeholder for a path parameter, and for a variable of
// a case that was not captured: an ID nothing in a seeded install has.
const MissingID = "999999999"

// matrixParams holds the placeholder for a parameter by its name, where
// MissingID would not fit what the route expects. Like MissingID, these
// name nothing that exists.
var matrixParams = map[string]string{
	// A wildcard: the rest of the path.
	"*": "contract-missing",
}

// matrixRouteParams holds placeholders for single routes, by route ID and
// parameter name. Only GET routes are listed with things that exist (the
// seeded devices are 900100001 to 900100020): a write with a real ID would
// change or delete what later requests read.
var matrixRouteParams = map[string]map[string]string{}

// matrixPaths holds the whole path to send for routes whose path is not one
// FillPath can fill (a regular expression), by route ID.
var matrixPaths = map[string]string{}

// matrixQueries holds a query string for GET routes that answer in an order
// that changes from minute to minute without one, by route ID.
var matrixQueries = map[string]string{
	// The default order is by last contact, newest first, and the seeded
	// devices report in every few seconds.
	"GET /api/devices (2)": "sortBy=id&sortOrder=asc",
}

// matrixVolatile and matrixVolatileHeaders mark by hand what two recordings
// cannot find out: paths of a route's answer that are volatile although two
// recordings may agree on them, and headers that only have to be present.
// A path that starts with "~" is free in format too. By matrix entry ID.
var (
	matrixVolatile = map[string][]string{
		// Each role's permissions come in another order on every call.
		"GET /api/panel/roles": {"data.roles[*].permissions"},
		// What the machine the console runs on looks like right now: its
		// processes, disks, processor, memory and network.
		"GET /api/system/info": {
			"disks[#]", "processes[#]",
			"node.heapTotal", "node.heapUsed", "node.rss", "node.uptime", "node.pid",
		},
		"GET /api/server-management/resources": {
			"~snapshot.arch", "snapshot.cpuCount", "~snapshot.cpuModel", "~snapshot.release", "~snapshot.platform",
			"snapshot.mem.total", "snapshot.mem.free", "snapshot.mem.used", "snapshot.mem.percent",
			"snapshot.swap.free", "snapshot.swap.total", "snapshot.swap.used",
			"snapshot.cpu", "snapshot.load", "snapshot.uptime", "snapshot.nodeUptime", "snapshot.ts",
			"snapshot.disks[#]", "snapshot.network[#]", "snapshot.history[#]",
		},
		// Process numbers and how long the servers have been up. Two
		// recordings can agree on these by chance.
		"GET /api/server-management/info":  {"pid", "server.uptimeSeconds"},
		"GET /api/settings/restart-status": {"data.uptime"},
		"GET /api/server/status":           {"data.uptime"},
		// What the last look at a public time server found.
		"GET /api/panel/billing/timesync/status": {"stratum", "offset_ms", "last_check_at"},
		// The sessions and help requests of the last 14 days. The seed dates
		// its history back from the moment it runs and follows working
		// hours, so how many fall into the window changes with the hour.
		"GET /api/reports/trends": {"data.sessions[#]", "data.help[#]"},
		// The same for the activity the seed spreads over working hours:
		// which devices have summaries, and their numbers, change with the
		// hour, and so does the saved report made from them.
		"GET /api/activity":      {"~summaries", "total"},
		"GET /api/reports/saved": {"~[*]"},
		// One entry holds the size of the backup the matrix downloads
		// ("0.33 MB"), which changes with the instance.
		"GET /api/settings/audit": {"~data[*].details"},
		// How long the Go server has been up, as Go prints a duration:
		// "51.3s", "1m3.2s", "1h0m5s".
		"GET /api/stats":         {"~data.hbbs.uptime"},
		"GET /api/settings/info": {"~data.goServer.uptime", "data.server.uptime", "data.server.memoryUsage"},
	}

	matrixVolatileHeaders = map[string][]string{
		// The file name holds the time of the backup.
		"GET /api/settings/backup": {"content-disposition"},
		// The revision is the time the branding was last saved, in base 36.
		"GET /css/branding.css": {"x-branding-revision"},
	}
)
