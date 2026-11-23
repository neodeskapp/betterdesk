package main

// The demo data. Every list keeps the order the rows and requests go out
// in, because ids and the order of lists in the console follow it.

// device is an obviously fake device.
type device struct {
	ID, Hostname, OS, Version, User string
	// Tags are comma separated; Folder is empty for no folder.
	Tags, Folder string
	Online       bool
	// Banned is the ban reason, empty for a device that is not banned.
	Banned  string
	Deleted bool
	Note    string
}

var devices = []device{
	{ID: "900100001", Hostname: "demo-reception-01", OS: "Windows 11 Pro", Version: "1.4.2", User: "demo.front", Tags: "office,ground-floor", Folder: "Office", Online: true},
	{ID: "900100002", Hostname: "demo-reception-02", OS: "Windows 11 Pro", Version: "1.4.2", User: "demo.front", Tags: "office,ground-floor", Folder: "Office"},
	{ID: "900100003", Hostname: "demo-accounts-01", OS: "Windows 10 Pro", Version: "1.3.9", User: "demo.ledger", Tags: "office,finance", Folder: "Office", Online: true},
	{ID: "900100004", Hostname: "demo-accounts-02", OS: "Windows 10 Pro", Version: "1.3.9", User: "demo.ledger", Tags: "finance", Folder: "Office"},
	{ID: "900100005", Hostname: "demo-ceo-macbook", OS: "macOS 15.1", Version: "1.4.2", User: "demo.boss", Tags: "vip", Folder: "Office", Online: true},
	{ID: "900100006", Hostname: "demo-design-imac", OS: "macOS 14.6", Version: "1.4.1", User: "demo.pixel", Tags: "design", Folder: "Office", Online: true},
	{ID: "900100007", Hostname: "demo-design-mbp", OS: "macOS 15.0", Version: "1.4.2", User: "demo.pixel", Tags: "design,laptop", Folder: "Office"},
	{ID: "900100008", Hostname: "demo-warehouse-pc", OS: "Windows 10 IoT", Version: "1.3.7", User: "demo.stock", Tags: "warehouse", Folder: "Warehouse", Online: true},
	{ID: "900100009", Hostname: "demo-warehouse-scan-1", OS: "Android 14", Version: "1.4.0", Tags: "warehouse,scanner", Folder: "Warehouse", Online: true},
	{ID: "900100010", Hostname: "demo-warehouse-scan-2", OS: "Android 13", Version: "1.3.9", Tags: "warehouse,scanner", Folder: "Warehouse"},
	{ID: "900100011", Hostname: "demo-warehouse-scan-3", OS: "Android 14", Version: "1.4.0", Tags: "warehouse,scanner", Folder: "Warehouse"},
	{ID: "900100012", Hostname: "demo-build-server", OS: "Ubuntu 24.04", Version: "1.4.2", User: "demo.ci", Tags: "server,linux", Folder: "Servers", Online: true},
	{ID: "900100013", Hostname: "demo-file-server", OS: "Debian 12", Version: "1.4.1", User: "demo.ops", Tags: "server,linux", Folder: "Servers", Online: true},
	{ID: "900100014", Hostname: "demo-backup-nas", OS: "Linux 6.1", Version: "1.3.8", User: "demo.ops", Tags: "server,storage", Folder: "Servers"},
	{ID: "900100015", Hostname: "demo-db-primary", OS: "Ubuntu 22.04", Version: "1.4.2", User: "demo.ops", Tags: "server,linux,critical", Folder: "Servers", Online: true},
	{ID: "900100016", Hostname: "demo-db-replica", OS: "Ubuntu 22.04", Version: "1.4.2", User: "demo.ops", Tags: "server,linux", Folder: "Servers"},
	{ID: "900100017", Hostname: "demo-sales-laptop-01", OS: "Windows 11 Home", Version: "1.4.0", User: "demo.seller", Tags: "laptop,sales", Online: true},
	{ID: "900100018", Hostname: "demo-sales-laptop-02", OS: "Windows 11 Home", Version: "1.3.9", User: "demo.seller", Tags: "laptop,sales"},
	{ID: "900100019", Hostname: "demo-sales-laptop-03", OS: "Windows 11 Pro", Version: "1.4.2", User: "demo.seller", Tags: "laptop,sales"},
	{ID: "900100020", Hostname: "demo-kiosk-lobby", OS: "Windows 10 IoT", Version: "1.3.5", Tags: "kiosk", Online: true},
	{ID: "900100021", Hostname: "demo-kiosk-canteen", OS: "Windows 10 IoT", Version: "1.3.5", Tags: "kiosk"},
	{ID: "900100022", Hostname: "demo-support-ipad", OS: "iOS 18.1", Version: "1.4.0", User: "demo.help", Tags: "mobile"},
	{ID: "900100023", Hostname: "demo-support-pixel", OS: "Android 15", Version: "1.4.2", User: "demo.help", Tags: "mobile", Online: true},
	{ID: "900100024", Hostname: "demo-lab-rpi-01", OS: "Linux 6.6 (Raspberry Pi)", Version: "1.3.9", Tags: "lab,iot", Folder: "Lab", Online: true},
	{ID: "900100025", Hostname: "demo-lab-rpi-02", OS: "Linux 6.6 (Raspberry Pi)", Version: "1.3.9", Tags: "lab,iot", Folder: "Lab"},
	{ID: "900100026", Hostname: "demo-lab-win-vm", OS: "Windows Server 2022", Version: "1.4.1", User: "demo.lab", Tags: "lab,vm", Folder: "Lab", Online: true},
	{ID: "900100027", Hostname: "demo-old-xp-box", OS: "Windows 7", Version: "1.1.9", Tags: "legacy", Folder: "Lab"},
	{ID: "900100028", Hostname: "demo-stolen-laptop", OS: "Windows 11 Pro", Version: "1.4.0", User: "demo.lost", Tags: "laptop", Banned: "Reported stolen"},
	{ID: "900100029", Hostname: "demo-unknown-box", OS: "Linux 5.15", Version: "1.2.0", Banned: "Unknown device, blocked"},
	{ID: "900100030", Hostname: "demo-retired-pc", OS: "Windows 10 Pro", Version: "1.3.0", User: "demo.old", Tags: "retired", Deleted: true},
	{ID: "900100031", Hostname: "demo-noted-pc", OS: "Windows 11 Pro", Version: "1.4.2", User: "demo.front", Tags: "office", Folder: "Office", Note: "Fan is loud, replace in spring"},
}

type folder struct{ Name, Color string }

var folders = []folder{
	{"Office", "#22c55e"},
	{"Warehouse", "#f59e0b"},
	{"Servers", "#6366f1"},
	{"Lab", "#64748b"},
}

// deviceGroup is a device group. TagFilter is left out of the request
// when empty; Members are set one device at a time.
type deviceGroup struct {
	Name, SourceType, TagFilter string
	Members                     []string
}

var deviceGroups = []deviceGroup{
	{Name: "All laptops", SourceType: "tag", TagFilter: "laptop"},
	{Name: "Critical servers", SourceType: "tag", TagFilter: "critical"},
	{Name: "Support team devices", SourceType: "manual", Members: []string{"900100022", "900100023"}},
}

// The user API allows three new accounts per five minutes.
type demoUser struct{ Username, Role, Email string }

var demoUsers = []demoUser{
	{"demo_operator", "operator", "operator@example.invalid"},
	{"demo_viewer", "viewer", "viewer@example.invalid"},
	{"demo_serveradmin", "server_admin", "serveradmin@example.invalid"},
}

// pendingDevice waits in the Go enrollment queue (server_config
// pending_device_<id>).
type pendingDevice struct {
	ID, Hostname, Platform, Version, IP string
	AgeHours                            float64
}

var pendingDevices = []pendingDevice{
	{"900200001", "demo-new-laptop", "Windows", "1.4.2", "10.0.0.51", 1},
	{"900200002", "demo-new-tablet", "Android", "1.4.0", "10.0.0.52", 2},
	{"900200003", "", "Linux", "1.4.2", "10.0.0.53", 3},
	{"900200004", "demo-pos-terminal", "Windows", "1.4.2", "10.0.0.54", 0.8},
	{"900200005", "demo-field-tablet", "Android", "1.3.9", "10.0.0.55", 26},
	{"900200006", "demo-intern-laptop", "Windows", "1.4.1", "10.0.0.56", 74},
	{"900200007", "", "macOS", "1.4.2", "10.0.0.57", 146},
}

// lanPending waits in the console's LAN discovery queue
// (pending_registrations).
type lanPending struct {
	ID, Hostname, Platform, Version, IP string
	MinutesAgo                          float64
}

var lanPendings = []lanPending{
	{"900300001", "demo-lan-printer-pc", "Windows", "1.4.2", "10.0.9.14", 0},
	{"900300002", "demo-meeting-room-pc", "Windows", "1.4.1", "10.0.9.15", 2 * 24 * 60},
	{"900300003", "demo-label-printer-host", "Linux", "1.3.9", "10.0.9.16", 5*24*60 + 300},
}

type helpRequest struct{ DeviceID, Hostname, Message, Status, HandledBy string }

var helpRequests = []helpRequest{
	{"900100001", "demo-reception-01", "Printer does not show up", "pending", ""},
	{"900100017", "demo-sales-laptop-01", "VPN keeps dropping since the update", "pending", ""},
	{"900100008", "demo-warehouse-pc", "Label software crashes on start", "accepted", "demo_operator"},
	{"900100006", "demo-design-imac", "Need a font installed", "resolved", "demo_helpdesk"},
}

// ---- Organizations, policies, billing (created through the console API) ----

type orgMember struct{ Username, Role string }

type orgLocalUser struct{ Username, DisplayName, Email, Role string }

type orgDevice struct{ DeviceID, Department, Location, Building, Assignee string }

// policy is one category of an organization's policies with its body.
type policy struct {
	Category string
	Body     object
}

type organization struct {
	Key, Name, Slug string
	Members         []orgMember
	LocalUsers      []orgLocalUser
	Devices         []orgDevice
	// Settings are sent one key at a time, in this order.
	Settings []field
	Policies []policy
}

var organizations = []organization{
	{
		Key: "northwind", Name: "Demo Northwind Clinic", Slug: "demo-northwind-clinic",
		Members: []orgMember{{"demo_operator", "admin"}, {"demo_viewer", "user"}},
		LocalUsers: []orgLocalUser{
			{"demo.nurse", "Demo Nurse Station", "nurse@northwind.example.invalid", "user"},
			{"demo.practice", "Demo Practice Manager", "manager@northwind.example.invalid", "operator"},
		},
		Devices: []orgDevice{
			{"900100001", "Reception", "Front desk", "Main building", "demo.nurse"},
			{"900100002", "Reception", "Front desk", "Main building", ""},
			{"900100003", "Accounts", "Room 1.04", "Main building", "demo.practice"},
			{"900100004", "Accounts", "Room 1.04", "Main building", ""},
			{"900100005", "Management", "Room 2.01", "Main building", "demo.practice"},
			{"900100006", "Marketing", "Room 2.07", "Annex", ""},
			{"900100031", "Reception", "Back office", "Main building", ""},
		},
		Settings: []field{{"timezone", "Europe/Berlin"}, {"support_email", "it@northwind.example.invalid"}},
		Policies: []policy{
			{"connection", object{{"mode", "attended"}, {"max_session_duration", 240.0}, {"idle_lock_timeout", 10.0}, {"wallpaper", "hide_during_session"}}},
			{"features", object{{"allow_file_transfer", true}, {"allow_clipboard", true}, {"allow_audio", false}, {"allow_recording", true}}},
			{"security", object{{"require_2fa", true}, {"password_min_length", 14.0}, {"password_require_special", true}, {"password_max_age_days", 90.0}, {"allowed_operators", []any{"admin", "demo_operator"}}}},
			{"network", object{{"block_direct_p2p", false}, {"allowed_relay_servers", []any{"relay.example.invalid"}}, {"ip_allowlist", []any{"10.0.1.0/24", "10.0.2.0/24"}}}},
			{"update", object{{"auto_update", true}, {"channel", "stable"}}},
		},
	},
	{
		Key: "harbor", Name: "Demo Harbor Logistics", Slug: "demo-harbor-logistics",
		Members:    []orgMember{{"demo_serveradmin", "operator"}},
		LocalUsers: []orgLocalUser{{"demo.dispatch", "Demo Dispatch Desk", "dispatch@harbor.example.invalid", "user"}},
		Devices: []orgDevice{
			{"900100008", "Warehouse", "Goods in", "Hall A", "demo.dispatch"},
			{"900100009", "Warehouse", "Hall A", "Hall A", ""},
			{"900100010", "Warehouse", "Hall B", "Hall B", ""},
			{"900100011", "Warehouse", "Hall B", "Hall B", ""},
			{"900100017", "Sales", "Mobile", "", ""},
		},
		Settings: []field{{"timezone", "Europe/Hamburg"}},
		Policies: []policy{
			{"connection", object{{"mode", "unattended"}, {"max_session_duration", 480.0}, {"idle_lock_timeout", 30.0}, {"wallpaper", "show"}}},
			{"update", object{{"auto_update", false}, {"channel", "beta"}}},
		},
	},
	{
		Key: "lab", Name: "Demo Lab Collective", Slug: "demo-lab-collective",
		Devices: []orgDevice{
			{"900100024", "Lab", "Bench 1", "Annex", ""},
			{"900100025", "Lab", "Bench 2", "Annex", ""},
			{"900100026", "Lab", "Rack", "Annex", ""},
		},
		Policies: []policy{
			{"network", object{{"block_direct_p2p", true}, {"allowed_relay_servers", []any{}}, {"ip_allowlist", []any{"10.0.4.0/24"}}}},
		},
	},
}

type billingPackage struct {
	Key, Name, Description string
	IncludedMinutes        float64
	OverageRate            float64
	Currency               string
	RoundingMinutes        float64
}

var billingPackages = []billingPackage{
	{"business", "Demo Business Care", "20 hours of remote support a month for small offices", 1200, 48, "EUR", 10},
	{"basic", "Demo Basic Support", "5 hours a month, billed in 15 minute steps", 300, 60, "EUR", 15},
	{"payg", "Demo Pay As You Go", "No included time, every session is billed", 0, 75, "EUR", 5},
}

// contract assigns a package to an organization ("org") or to the
// organization's default device group ("device_group").
type contract struct{ Org, Target, Package, Status string }

var contracts = []contract{
	{"northwind", "device_group", "business", "active"},
	{"harbor", "device_group", "basic", "active"},
	{"lab", "org", "payg", "suspended"},
}

// workReport is what the operator wrote after a session: summary,
// category and ticket reference.
type workReport struct{ Summary, Category, TicketRef string }

// billingSession is a support session. RawMinutes is nil for the one that
// is still running.
type billingSession struct {
	Org, DeviceID, Operator string
	StartedMinutesAgo       float64
	RawMinutes              *float64
	Status                  string
	Report                  *workReport
}

func minutes(n float64) *float64 { return &n }

var billingSessions = []billingSession{
	{"northwind", "900100001", "demo_operator", 6*24*60 + 120, minutes(42), "closed", &workReport{"Reinstalled printer driver and set default tray", "hardware", "#2"}},
	{"northwind", "900100003", "admin", 5*24*60 + 30, minutes(18), "closed", &workReport{"Unlocked accounting software licence", "software", ""}},
	{"northwind", "900100005", "demo_operator", 4*24*60 + 600, minutes(75), "closed", &workReport{"Moved mailbox to new laptop profile", "software", ""}},
	{"northwind", "900100006", "demo_operator", 3*24*60 + 200, minutes(9), "closed", &workReport{"Installed brand fonts", "software", "#9"}},
	{"northwind", "900100002", "admin", 2*24*60 + 90, minutes(33), "closed", &workReport{"Checked Windows update loop, cleared cache", "software", ""}},
	{"northwind", "900100004", "demo_operator", 26 * 60, minutes(51), "pending_report", nil},
	{"northwind", "900100001", "demo_operator", 25, nil, "active", nil},
	{"harbor", "900100008", "demo_operator", 5*24*60 + 400, minutes(118), "closed", &workReport{"Label software crash, rebuilt print spooler", "software", "#5"}},
	{"harbor", "900100009", "admin", 3*24*60 + 60, minutes(131), "closed", &workReport{"Re-enrolled scanner after factory reset", "hardware", ""}},
	{"harbor", "900100017", "demo_operator", 20 * 60, minutes(88), "pending_report", nil},
}

// ---- Tickets ----

type ticketComment struct {
	Author, Body string
	Internal     bool
	AgoHours     float64
}

// ticket is a help desk ticket. Device and AssignedTo are empty for NULL;
// ResolvedAgoHours and ClosedAgoHours are nil for a ticket that never got
// there.
type ticket struct {
	Title, Status, Priority, Category string
	Device, CreatedBy, AssignedTo     string
	AgeHours                          float64
	ResolvedAgoHours, ClosedAgoHours  *float64
	Description                       string
	Comments                          []ticketComment
}

func hours(n float64) *float64 { return &n }

var tickets = []ticket{
	{
		Title: "Reception printer prints blank pages", Status: "open", Priority: "medium", Category: "hardware",
		Device: "900100001", CreatedBy: "admin", AgeHours: 3,
		Description: "Since this morning every job from demo-reception-01 comes out blank. The test page from the printer menu is fine.",
	},
	{
		Title: "VPN drops every 20 minutes on sales laptop", Status: "in_progress", Priority: "high", Category: "network",
		Device: "900100017", CreatedBy: "demo_operator", AssignedTo: "demo_operator", AgeHours: 27,
		Description: "Started after the client update last week. Happens on hotel Wi-Fi and at home.",
		Comments: []ticketComment{
			{"demo_operator", "Collected the VPN client log, it times out on rekey.", false, 24},
			{"admin", "Try lowering the MTU to 1380, the hotel networks often break fragmented packets.", true, 20},
			{"demo_operator", "MTU change applied. Waiting for the user to report back.", false, 4},
		},
	},
	{
		Title: "Database replica lagging behind primary", Status: "open", Priority: "critical", Category: "software",
		Device: "900100016", CreatedBy: "admin", AssignedTo: "admin", AgeHours: 6,
		Description: "Replication lag on demo-db-replica is above 40 minutes and growing.",
		Comments:    []ticketComment{{"admin", "Replica went offline overnight, checking disk space first.", true, 5}},
	},
	{
		Title: "New starter needs accounts and a laptop", Status: "open", Priority: "low", Category: "access",
		CreatedBy: "admin", AssignedTo: "demo_operator", AgeHours: 50,
		Description: "Starts on Monday in accounts. Needs a laptop, mail account and access to the finance share.",
	},
	{
		Title: "Label software crashes on start", Status: "resolved", Priority: "medium", Category: "software",
		Device: "900100008", CreatedBy: "demo_operator", AssignedTo: "demo_operator", AgeHours: 5*24 + 8, ResolvedAgoHours: hours(5*24 + 2),
		Description: "The label printing app closes right after the splash screen.",
		Comments: []ticketComment{
			{"demo_operator", "Print spooler was stuck. Rebuilt it and the app starts again.", false, 5*24 + 2},
		},
	},
	{
		Title: "Replace loud fan in demo-noted-pc", Status: "closed", Priority: "low", Category: "hardware",
		Device: "900100031", CreatedBy: "demo_operator", AssignedTo: "admin", AgeHours: 12 * 24, ResolvedAgoHours: hours(6 * 24), ClosedAgoHours: hours(5 * 24),
		Description: "User reports a grinding noise from the case fan.",
	},
	{
		Title: "Lobby kiosk shows a certificate warning", Status: "open", Priority: "high", Category: "network",
		Device: "900100020", CreatedBy: "admin", AgeHours: 0.5,
		Description: "Visitors see a browser certificate warning on the check-in page.",
	},
	{
		Title: "Backup NAS offline since the weekend", Status: "in_progress", Priority: "critical", Category: "hardware",
		Device: "900100014", CreatedBy: "demo_serveradmin", AssignedTo: "demo_serveradmin", AgeHours: 52,
		Description: "Nightly backups failed twice. The NAS does not answer ping.",
		Comments: []ticketComment{
			{"demo_serveradmin", "Power supply LED is off. Ordered a replacement.", true, 30},
			{"admin", "Moved tonight's backup job to the file server as a stopgap.", false, 26},
		},
	},
	{
		Title: "Install brand fonts on design iMac", Status: "closed", Priority: "low", Category: "software",
		Device: "900100006", CreatedBy: "admin", AssignedTo: "demo_operator", AgeHours: 4 * 24, ResolvedAgoHours: hours(3*24 + 20), ClosedAgoHours: hours(3 * 24),
		Description: "Marketing needs the new brand fonts for the spring campaign.",
	},
}

var slaHours = map[string]float64{"critical": 4, "high": 8, "medium": 24, "low": 72}

// ---- Chat ----

// chatMessage is a message: "admin" and "demo_operator" are console
// operators; a device id means a message typed on that device.
type chatMessage struct {
	From       string
	MinutesAgo float64
	Text       string
}

type chatGroup struct{ Name, Members string }

type conversation struct {
	ID       string
	Group    *chatGroup
	Messages []chatMessage
}

var conversations = []conversation{
	{
		ID: "operator:demo_operator",
		Messages: []chatMessage{
			{"admin", 26 * 60, "Can you take the VPN ticket for the sales laptop today?"},
			{"demo_operator", 26*60 - 4, "Sure, I will call the user after lunch."},
			{"demo_operator", 4 * 60, "MTU change is on the laptop now. Waiting for feedback."},
			{"admin", 4*60 - 2, "Thanks. Leave the ticket in progress until they confirm."},
			{"demo_operator", 35, "Also: the backup NAS is still dark, should I escalate?"},
		},
	},
	{
		ID: "900100008",
		Messages: []chatMessage{
			{"900100008", 5*24*60 + 20, "Hello, the label program closes as soon as I open it."},
			{"demo_operator", 5*24*60 + 15, "I am connecting now, please leave the PC on."},
			{"900100008", 5*24*60 + 14, "OK"},
			{"demo_operator", 5*24*60 - 30, "Fixed. Please try printing a label."},
			{"900100008", 5*24*60 - 34, "Works again, thank you!"},
		},
	},
	{
		ID: "900100017",
		Messages: []chatMessage{
			{"900100017", 50, "VPN dropped again during a customer call."},
			{"900100017", 48, "Is there anything I can do in the meantime?"},
		},
	},
	{
		ID:    "group:demo0001",
		Group: &chatGroup{"Demo on-call", "panel,operator:admin,operator:demo_operator,operator:demo_serveradmin"},
		Messages: []chatMessage{
			{"admin", 3 * 24 * 60, "On-call this week: demo_operator until Wednesday, then demo_serveradmin."},
			{"demo_serveradmin", 3*24*60 - 10, "Noted."},
			{"demo_operator", 2 * 60, "Heads-up: db replica is lagging, admin is on it."},
		},
	},
}

// ---- Inventory, activity, history ----

var inventoryDevices = []string{
	"900100001", "900100002", "900100003", "900100004", "900100005", "900100006", "900100008",
	"900100012", "900100013", "900100015", "900100016", "900100017", "900100024", "900100026",
}

var activityDevices = []string{"900100001", "900100003", "900100005", "900100006", "900100017", "900100026"}

// historyDevice is a device with a week of status history: desktops
// follow office hours, servers stay up with the odd reboot.
type historyDevice struct{ ID, Kind string }

var historyDevices = []historyDevice{
	{"900100001", "office"}, {"900100003", "office"}, {"900100005", "office"}, {"900100002", "office"},
	{"900100012", "server"}, {"900100015", "server"}, {"900100016", "server"}, {"900100026", "server"},
}

// app is an installed application: name, version, publisher.
type app struct{ Name, Version, Publisher string }

var apps = map[string][]app{
	"win": {
		{"Microsoft Edge", "130.0.2849.68", "Microsoft Corporation"},
		{"Google Chrome", "130.0.6723.117", "Google LLC"},
		{"Microsoft 365 Apps for business", "16.0.18129.20116", "Microsoft Corporation"},
		{"Adobe Acrobat Reader", "24.004.20243", "Adobe"},
		{"7-Zip 24.08 (x64)", "24.08", "Igor Pavlov"},
		{"Notepad++ (64-bit x64)", "8.7.1", "Notepad++ Team"},
		{"VLC media player", "3.0.21", "VideoLAN"},
		{"NeoDesk", "1.4.2", "NeoDesk (demo)"},
		{"Demo Label Studio", "5.2.0", "Example Software GmbH"},
		{"Demo Accounting Suite", "2024.3", "Example Ledger Ltd"},
	},
	"mac": {
		{"Safari", "18.1", "Apple"},
		{"Google Chrome", "130.0.6723.117", "Google LLC"},
		{"Microsoft Outlook", "16.90", "Microsoft Corporation"},
		{"Slack", "4.41.97", "Slack Technologies"},
		{"Figma", "124.5.5", "Figma, Inc."},
		{"Adobe Photoshop 2025", "26.1", "Adobe"},
		{"NeoDesk", "1.4.2", "NeoDesk (demo)"},
	},
	"linux": {
		{"openssh-server", "1:9.6p1-3ubuntu13.5", "Ubuntu Developers"},
		{"postgresql-16", "16.4-0ubuntu0.24.04.2", "Ubuntu Developers"},
		{"docker-ce", "5:27.3.1-1~ubuntu.24.04~noble", "Docker Inc."},
		{"nginx", "1.24.0-2ubuntu7.1", "Ubuntu Developers"},
		{"prometheus-node-exporter", "1.7.0-1ubuntu0.2", "Ubuntu Developers"},
		{"neodesk", "1.4.2", "NeoDesk (demo)"},
	},
	"rpi": {
		{"raspi-config", "20240617", "Raspberry Pi Ltd"},
		{"python3", "3.11.2-1+b1", "Debian"},
		{"mosquitto", "2.0.11-1.2", "Debian"},
		{"neodesk", "1.3.9", "NeoDesk (demo)"},
	},
	"winserver": {
		{"Microsoft Edge", "130.0.2849.68", "Microsoft Corporation"},
		{"Windows Admin Center", "2410", "Microsoft Corporation"},
		{"Microsoft SQL Server 2022 (64-bit)", "16.0.4135.4", "Microsoft Corporation"},
		{"PowerShell 7-x64", "7.4.6", "Microsoft Corporation"},
		{"NeoDesk", "1.4.1", "NeoDesk (demo)"},
	},
}

// activityApp is a foreground window: application, window title,
// category.
type activityApp struct{ App, Title, Category string }

var activityApps = []activityApp{
	{"Microsoft Outlook", "Inbox - demo@example.invalid", "communication"},
	{"Microsoft Teams", "Chat | Demo Team", "communication"},
	{"Google Chrome", "Supplier portal - Google Chrome", "browser"},
	{"Microsoft Edge", "Intranet - Microsoft Edge", "browser"},
	{"Microsoft Excel", "Q4 budget.xlsx - Excel", "productivity"},
	{"Microsoft Word", "Offer draft.docx - Word", "productivity"},
	{"Demo Accounting Suite", "Ledger - November", "productivity"},
	{"Visual Studio Code", "deploy.yml - demo-infra", "development"},
	{"Spotify", "Focus playlist", "media"},
	{"Windows Explorer", "Documents", "system"},
}

// auditEntry is a row of the audit log. User is "1" for the user id 1,
// "" for NULL, or a demo user's name, whose id is looked up at seed time
// (NULL while the account does not exist yet).
type auditEntry struct {
	User, Action, Details, IP string
	MinutesAgo                float64
}

var auditEntries = []auditEntry{
	{"1", "device_ban", "Banned 900100028 (Reported stolen)", "10.0.0.2", 30},
	{"1", "device_ban", "Banned 900100029 (Unknown device, blocked)", "10.0.0.3", 230},
	{"1", "device_delete", "Deleted 900100030", "10.0.0.4", 430},
	{"1", "settings_change", "Changed relay server", "10.0.0.5", 630},
	{"1", "user_create", "Created user demo_viewer", "10.0.0.6", 830},
	{"1", "remote_connect", "Connected to 900100012", "10.0.0.7", 1030},
	{"1", "remote_connect", "Connected to 900100005", "10.0.0.8", 1230},
	{"1", "folder_create", "Created folder Lab", "10.0.0.9", 1430},
	{"", "login_failed", "Username: root; ip=203.0.113.45; ua=curl/8.5.0", "203.0.113.45", 6*60 + 3},
	{"", "login_failed", "Username: root; ip=203.0.113.45; ua=curl/8.5.0", "203.0.113.45", 6*60 + 2},
	{"", "login_failed", "Username: administrator; ip=198.51.100.7; ua=python-requests/2.32.3", "198.51.100.7", 14 * 60},
	{"demo_viewer", "login_failed", "Username: demo_viewer; ip=10.0.2.41; ua=Mozilla/5.0 (Macintosh; Intel Mac OS X 14_6) Firefox/132.0", "10.0.2.41", 3*60 + 2},
	{"demo_viewer", "login", "User logged in; ip=10.0.2.41; ua=Mozilla/5.0 (Macintosh; Intel Mac OS X 14_6) Firefox/132.0", "10.0.2.41", 3 * 60},
	{"demo_operator", "login", "User logged in; ip=10.0.1.33; ua=Mozilla/5.0 (Windows NT 10.0; Win64; x64) Edg/130.0", "10.0.1.33", 2 * 24 * 60},
	{"demo_operator", "logout", "User logged out", "10.0.1.33", 2*24*60 - 470},
	{"demo_serveradmin", "login", "User logged in; ip=10.0.3.12; ua=Mozilla/5.0 (X11; Linux x86_64) Firefox/132.0", "10.0.3.12", 30 * 60},
	{"demo_serveradmin", "session_expired", "reason=idle", "10.0.3.12", 30*60 - 45},
}

// loginAttempt is a failed or good sign-in inside the 24 h window the
// console keeps, older than any lockout window and from TEST-NET
// addresses.
type loginAttempt struct {
	Username, IP string
	Success      bool
	MinutesAgo   float64
}

var loginAttempts = []loginAttempt{
	{"root", "203.0.113.45", false, 6*60 + 5}, {"root", "203.0.113.45", false, 6*60 + 4},
	{"root", "203.0.113.45", false, 6*60 + 3}, {"root", "203.0.113.45", false, 6*60 + 2},
	{"administrator", "198.51.100.7", false, 14*60 + 1}, {"administrator", "198.51.100.7", false, 14 * 60},
	{"demo_viewer", "10.0.2.41", false, 3*60 + 2}, {"demo_viewer", "10.0.2.41", true, 3 * 60},
	{"demo_serveradmin", "10.0.3.12", true, 30 * 60},
}

// endedSession is a console session that has ended, so the demo users'
// session history is not empty.
type endedSession struct {
	Username, IP, UserAgent     string
	StartMinutes, LengthMinutes float64
	Reason                      string
}

var endedSessions = []endedSession{
	{"demo_operator", "10.0.1.33", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.0.0", 2 * 24 * 60, 470, "logout"},
	{"demo_operator", "10.0.1.33", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36 Edg/130.0.0.0", 4 * 24 * 60, 300, "idle"},
	{"demo_operator", "10.0.4.20", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_1 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.1 Mobile/15E148 Safari/604.1", 5 * 24 * 60, 12, "replaced"},
	{"demo_viewer", "10.0.2.41", "Mozilla/5.0 (Macintosh; Intel Mac OS X 14.6; rv:132.0) Gecko/20100101 Firefox/132.0", 3 * 24 * 60, 95, "logout"},
	{"demo_serveradmin", "10.0.3.12", "Mozilla/5.0 (X11; Linux x86_64; rv:132.0) Gecko/20100101 Firefox/132.0", 30 * 60, 45, "idle"},
}

// savedReport is a report saved through the API: type and title.
type savedReport struct{ Type, Title string }

var savedReports = []savedReport{
	{"devices", "Demo weekly device summary"},
	{"tickets", "Demo open tickets"},
	{"inventory", "Demo hardware inventory"},
	{"activity", "Demo app usage this week"},
}
