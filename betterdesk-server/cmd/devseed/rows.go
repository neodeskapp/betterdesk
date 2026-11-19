package main

import (
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Times are milliseconds since 1970 as float64, computed in the order the
// seed always computed them, so a fraction that comes up is the same and
// a Date made from it is cut the same way.
const (
	minute = 60000.0
	hour   = 60 * minute
	day    = 24 * hour
)

// rng returns the seeded generator the demo data comes from (mulberry32):
// the same numbers on every run, so re-seeding gives the same pictures.
func rng(seed uint32) func() float64 {
	a := seed
	return func() float64 {
		a += 0x6D2B79F5
		t := a
		t = (t ^ (t >> 15)) * (t | 1)
		t ^= t + (t^(t>>7))*(t|61)
		return float64(t^(t>>14)) / 4294967296
	}
}

// seedFor hashes a text into a generator seed (FNV-1a over UTF-16 code
// units, as the text's characters).
func seedFor(text string) uint32 {
	h := uint32(2166136261)
	for _, r := range text {
		h = (h ^ uint32(firstUnit(r))) * 16777619
	}
	return h
}

// firstUnit is the first UTF-16 code unit of a character.
func firstUnit(r rune) uint16 {
	if r >= 0x10000 {
		return uint16(0xD800 + ((r - 0x10000) >> 10))
	}
	return uint16(r)
}

// jsDate is new Date(ms): the instant, with a fraction of a millisecond
// cut off towards zero.
func jsDate(ms float64) time.Time {
	return time.UnixMilli(int64(math.Trunc(ms))).UTC()
}

// utcMidnight is the start of the UTC day of ms, as setUTCHours(0, 0, 0, 0)
// gives it.
func utcMidnight(ms float64) (float64, time.Weekday) {
	t := jsDate(ms)
	m := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	return float64(m.UnixMilli()), m.Weekday()
}

func weekend(d time.Weekday) bool { return d == time.Sunday || d == time.Saturday }

// devInfo is a demo device with what the seed derives from its place in
// the list.
type devInfo struct {
	device
	Index int
	IP    string
	// LastOnline is when the device was last seen: now when it is online.
	LastOnline float64
}

// demo is one run of the demo seed: its clock and the devices by id.
type demo struct {
	now float64
	dev map[string]*devInfo
}

func newDemo(now time.Time) *demo {
	d := &demo{now: float64(now.UnixMilli()), dev: map[string]*devInfo{}}
	for i, dv := range devices {
		last := d.now
		if !dv.Online {
			last = d.now - float64(2+i*7)*hour
		}
		d.dev[dv.ID] = &devInfo{
			device:     dv,
			Index:      i,
			IP:         "10.0." + strconv.Itoa(1+i%4) + "." + strconv.Itoa(20+i),
			LastOnline: last,
		}
	}
	return d
}

// ago is the SQLite time minutesAgo minutes before now (iso in the seed).
func (d *demo) ago(minutesAgo float64) stamp {
	return stamp{ms: d.now - mul(minutesAgo, minute), form: sqlSeconds}
}

var mobileOS = regexp.MustCompile(`Android|iOS`)

// peerRow is a row of peers.
type peerRow struct {
	ID, UUID, IP, User, Hostname, OS, Version string
	LastOnline, CreatedAt                     stamp
	Banned                                    bool
	BanReason                                 string
	BannedAt                                  any // stamp or nil
	SoftDeleted                               bool
	DeletedAt                                 any // stamp or nil
	Note, DeviceType                          string
}

func (d *demo) peerRows() []peerRow {
	rows := make([]peerRow, 0, len(devices))
	for i, dv := range devices {
		r := peerRow{
			ID: dv.ID, UUID: "demo-uuid-" + dv.ID, IP: d.dev[dv.ID].IP, User: dv.User,
			Hostname: dv.Hostname, OS: dv.OS, Version: dv.Version,
			CreatedAt: d.ago(float64(60 * 24 * (40 - i))),
			Banned:    dv.Banned != "", BanReason: dv.Banned, SoftDeleted: dv.Deleted,
			Note: dv.Note, DeviceType: "rustdesk",
		}
		if dv.Online {
			r.LastOnline = d.ago(0)
		} else {
			r.LastOnline = d.ago(float64(60 * (2 + i*7)))
		}
		if dv.Banned != "" {
			r.BannedAt = d.ago(60 * 24 * 3)
		}
		if dv.Deleted {
			r.DeletedAt = d.ago(60 * 24 * 10)
		}
		if mobileOS.MatchString(dv.OS) {
			r.DeviceType = "mobile"
		}
		rows = append(rows, r)
	}
	return rows
}

// pendingJSON is the server_config value of a device in the enrollment
// queue.
func (d *demo) pendingJSON(p pendingDevice) string {
	return mustStringify(object{
		{"device_id", p.ID},
		{"hostname", p.Hostname},
		{"platform", p.Platform},
		{"version", p.Version},
		{"ip", p.IP},
		{"created_at", isoText(d.now - mul(p.AgeHours, hour))},
	})
}

// chatRow is a row of chat_messages.
type chatRow struct {
	Conversation, FromID, FromName, To, Text string
	Read                                     bool
	CreatedAt                                stamp
}

// chatRows are a conversation's messages, oldest first. The newest
// message stays unread unless the admin wrote it.
func (d *demo) chatRows(c conversation) []chatRow {
	sorted := append([]chatMessage(nil), c.Messages...)
	sort.SliceStable(sorted, func(a, b int) bool { return sorted[a].MinutesAgo > sorted[b].MinutesAgo })
	rows := make([]chatRow, 0, len(sorted))
	for i, m := range sorted {
		var fromID, fromName string
		switch {
		case m.From == "admin":
			fromID, fromName = "operator", "admin"
		case isDigits(m.From):
			fromID = m.From
			fromName = d.dev[m.From].User
			if fromName == "" {
				fromName = d.dev[m.From].Hostname
			}
		default:
			fromID, fromName = "operator:"+m.From, m.From
		}
		last := i == len(sorted)-1
		rows = append(rows, chatRow{
			Conversation: c.ID, FromID: fromID, FromName: fromName, To: c.ID, Text: m.Text,
			Read:      !(last && m.From != "admin"),
			CreatedAt: d.ago(m.MinutesAgo),
		})
	}
	return rows
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// osClass sorts an operating system into the groups the inventory knows.
func osClass(os string) string {
	switch {
	case strings.Contains(os, "macOS"):
		return "mac"
	case strings.Contains(os, "Raspberry"):
		return "rpi"
	case strings.Contains(os, "Windows Server"):
		return "winserver"
	case strings.Contains(os, "Windows"):
		return "win"
	}
	return "linux"
}

// The inventory report as JSON, its properties in the order the seed set
// them.
type hardware struct {
	Hostname          string         `json:"hostname"`
	OSName            string         `json:"os_name"`
	Architecture      string         `json:"architecture"`
	NetworkInterfaces []netInterface `json:"network_interfaces"`
	OSVersion         string         `json:"os_version"`
	Kernel            string         `json:"kernel"`
	CPU               cpuInfo        `json:"cpu"`
	Disks             []disk         `json:"disks"`
	Memory            memoryInfo     `json:"memory"`
}

type netInterface struct {
	Name        string   `json:"name"`
	MAC         string   `json:"mac"`
	IPAddresses []string `json:"ip_addresses"`
}

type cpuInfo struct {
	Brand        string  `json:"brand"`
	LogicalCores float64 `json:"logical_cores"`
	UsagePercent float64 `json:"usage_percent"`
}

type disk struct {
	Device     string  `json:"device"`
	MountPoint string  `json:"mount_point"`
	Filesystem string  `json:"filesystem"`
	TotalGB    float64 `json:"total_gb"`
	UsedGB     float64 `json:"used_gb"`
}

type memoryInfo struct {
	TotalBytes float64 `json:"total_bytes"`
	UsedBytes  float64 `json:"used_bytes"`
}

type software struct {
	Apps []installedApp `json:"apps"`
}

type installedApp struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Publisher   string `json:"publisher"`
	InstalledAt string `json:"installed_at"`
}

func pickString(r func() float64, items ...string) string {
	return items[int(math.Floor(r()*float64(len(items))))]
}

func pickNumber(r func() float64, items ...float64) float64 {
	return items[int(math.Floor(r()*float64(len(items))))]
}

// buildInventory makes a device's inventory report.
func (d *demo) buildInventory(dv *devInfo) (hardware, software) {
	r := rng(seedFor("inv" + dv.ID))
	cls := osClass(dv.OS)
	const gb = 1024 * 1024 * 1024
	hw := hardware{Hostname: dv.Hostname, OSName: dv.OS, Architecture: "x86_64"}
	if cls == "mac" || cls == "rpi" {
		hw.Architecture = "arm64"
	}
	nic := netInterface{Name: "eth0", IPAddresses: []string{dv.IP}}
	switch cls {
	case "win", "winserver":
		nic.Name = "Ethernet"
	case "mac":
		nic.Name = "en0"
	}
	last6 := dv.ID[len(dv.ID)-6:]
	nic.MAC = "02:00:5e:" + last6[0:2] + ":" + last6[2:4] + ":" + last6[4:6]
	hw.NetworkInterfaces = []netInterface{nic}

	var memGB float64
	switch cls {
	case "win":
		if strings.Contains(dv.OS, "11") {
			hw.OSVersion, hw.Kernel = "23H2 (22631.4460)", "10.0.22631"
		} else {
			hw.OSVersion, hw.Kernel = "22H2 (19045.5131)", "10.0.19045"
		}
		hw.CPU.Brand = pickString(r, "Intel(R) Core(TM) i5-12500", "Intel(R) Core(TM) i7-1365U", "AMD Ryzen 7 PRO 7840U")
		hw.CPU.LogicalCores = pickNumber(r, 8, 12, 16)
		memGB = pickNumber(r, 8, 16, 16, 32)
		hw.Disks = []disk{{Device: "C:", MountPoint: `C:\`, Filesystem: "NTFS", TotalGB: pickNumber(r, 237.9, 475.8, 951.6)}}
	case "mac":
		hw.OSVersion = strings.Replace(dv.OS, "macOS ", "", 1)
		hw.Kernel = "Darwin 24.1.0"
		hw.CPU.Brand = pickString(r, "Apple M3 Pro", "Apple M2", "Apple M1 Max")
		hw.CPU.LogicalCores = pickNumber(r, 8, 10, 12)
		memGB = pickNumber(r, 16, 18, 32)
		hw.Disks = []disk{{Device: "disk3s1", MountPoint: "/", Filesystem: "APFS", TotalGB: pickNumber(r, 494.4, 994.7)}}
	case "rpi":
		hw.OSVersion = "Debian 12 (bookworm)"
		hw.Kernel = "6.6.51+rpt-rpi-2712"
		hw.CPU = cpuInfo{Brand: "Broadcom BCM2712 (Cortex-A76)", LogicalCores: 4}
		memGB = 8
		hw.Disks = []disk{{Device: "/dev/mmcblk0p2", MountPoint: "/", Filesystem: "ext4", TotalGB: 58.2}}
	case "winserver":
		hw.OSVersion = "21H2 (20348.2849)"
		hw.Kernel = "10.0.20348"
		hw.CPU = cpuInfo{Brand: "Intel(R) Xeon(R) Silver 4310 CPU @ 2.10GHz", LogicalCores: 8}
		memGB = 32
		hw.Disks = []disk{
			{Device: "C:", MountPoint: `C:\`, Filesystem: "NTFS", TotalGB: 126.4},
			{Device: "D:", MountPoint: `D:\`, Filesystem: "NTFS", TotalGB: 511.9},
		}
	default:
		hw.OSVersion = linuxVersion(dv.OS)
		switch {
		case strings.Contains(dv.OS, "24.04"):
			hw.Kernel = "6.8.0-48-generic"
		case strings.Contains(dv.OS, "22.04"):
			hw.Kernel = "5.15.0-125-generic"
		default:
			hw.Kernel = "6.1.0-26-amd64"
		}
		hw.CPU.Brand = pickString(r, "Intel(R) Xeon(R) E-2388G CPU @ 3.20GHz", "AMD EPYC 7313P 16-Core Processor")
		hw.CPU.LogicalCores = pickNumber(r, 16, 32)
		memGB = pickNumber(r, 32, 64, 128)
		hw.Disks = []disk{
			{Device: "/dev/nvme0n1p2", MountPoint: "/", Filesystem: "ext4", TotalGB: 233.7},
			{Device: "/dev/md0", MountPoint: "/srv", Filesystem: "xfs", TotalGB: 3725.3},
		}
	}
	for i := range hw.Disks {
		hw.Disks[i].UsedGB = round(mul(mul(hw.Disks[i].TotalGB, 0.25+mul(r(), 0.7)), 10)) / 10
	}
	usage := 0.0
	if dv.Online {
		usage = 4 + mul(r(), 50)
	}
	hw.CPU.UsagePercent = round(mul(usage, 10)) / 10
	total := mul(memGB, gb)
	hw.Memory = memoryInfo{TotalBytes: total, UsedBytes: round(mul(total, 0.3+mul(r(), 0.55)))}

	var sw software
	for _, a := range apps[cls] {
		daysAgo := math.Floor(30 + mul(r(), 600))
		sw.Apps = append(sw.Apps, installedApp{
			Name: a.Name, Version: a.Version, Publisher: a.Publisher,
			InstalledAt: isoText(d.now - mul(daysAgo, day))[:10],
		})
	}
	return hw, sw
}

// linuxVersion is the operating system's name without its first word, or
// "6.1" when nothing is left.
func linuxVersion(os string) string {
	i := strings.IndexFunc(os, isJSSpace)
	if i == 0 {
		return os
	}
	if i < 0 {
		return "6.1"
	}
	rest := strings.TrimLeftFunc(os[i:], isJSSpace)
	if rest == "" {
		return "6.1"
	}
	return rest
}

func isJSSpace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\v', '\f', '\r', 0xA0, 0x1680, 0x2028, 0x2029, 0x202F, 0x205F, 0x3000, 0xFEFF:
		return true
	}
	return r >= 0x2000 && r <= 0x200A
}

// inventoryRow is a device's rows of device_inventory and
// device_telemetry.
type inventoryRow struct {
	DeviceID           string
	Hardware, Software string
	CollectedAt        stamp
	ReceivedAt         stamp
	CPU                float64
	MemoryUsed         float64
	MemoryTotal        float64
	Uptime             float64
}

func (d *demo) inventoryRows() []inventoryRow {
	rows := make([]inventoryRow, 0, len(inventoryDevices))
	for _, id := range inventoryDevices {
		dv := d.dev[id]
		hw, sw := d.buildInventory(dv)
		seen := dv.LastOnline - 5*minute
		if dv.Online {
			seen = d.now - 12*minute
		}
		uptime := float64(7*3600 + 1200)
		if osClass(dv.OS) == "linux" {
			uptime = 19*86400 + 3600
		}
		rows = append(rows, inventoryRow{
			DeviceID: id, Hardware: mustStringify(hw), Software: mustStringify(sw),
			CollectedAt: stamp{seen, isoMillis}, ReceivedAt: stamp{seen, sqlSeconds},
			CPU: hw.CPU.UsagePercent, MemoryUsed: hw.Memory.UsedBytes, MemoryTotal: hw.Memory.TotalBytes,
			Uptime: uptime,
		})
	}
	return rows
}

// activitySession and activitySummary are rows of activity_sessions and
// activity_summaries.
type activitySession struct {
	DeviceID, App, Title, Category string
	StartedAt, EndedAt             stamp
	Duration                       float64
	CreatedAt                      stamp
}

type activitySummary struct {
	DeviceID               string
	Idle, Sessions, Active float64
	ReportedAt, ReceivedAt stamp
}

// activityRows are a week of app usage over working hours: one day after
// the other, most weekends left out.
func (d *demo) activityRows(id string) ([]activitySession, []activitySummary) {
	r := rng(seedFor("act" + id))
	var sessions []activitySession
	var summaries []activitySummary
	for back := 6; back >= 0; back-- {
		dayStart0, weekday := utcMidnight(d.now - float64(back)*day)
		we := weekend(weekday)
		if we && r() < 0.7 {
			continue
		}
		t := dayStart0 + (7*60+math.Floor(r()*90))*minute
		endHours := 11.0
		if !we {
			endHours = 16 + mul(r(), 2)
		}
		end := dayStart0 + mul(endHours, hour)
		var active, n float64
		dayStart := t
		for t < end && t < d.now-10*minute {
			a := activityApps[int(math.Floor(r()*float64(len(activityApps))))]
			secs := math.Floor(mul(3+mul(r(), 45), 60))
			stop := jsMin(t+secs*1000, d.now-5*minute)
			dur := math.Floor((stop - t) / 1000)
			sessions = append(sessions, activitySession{
				DeviceID: id, App: a.App, Title: a.Title, Category: a.Category,
				StartedAt: stamp{t, isoMillis}, EndedAt: stamp{stop, isoMillis},
				Duration: dur, CreatedAt: stamp{stop, sqlSeconds},
			})
			active += dur
			n++
			t = stop + math.Floor(r()*20)*minute
		}
		if n > 0 {
			span := math.Floor((jsMin(t, d.now) - dayStart) / 1000)
			summaries = append(summaries, activitySummary{
				DeviceID: id, Idle: jsMax(0, span-active), Sessions: n, Active: active,
				ReportedAt: stamp{dayStart, isoMillis}, ReceivedAt: stamp{jsMin(t, d.now), sqlSeconds},
			})
		}
	}
	return sessions, summaries
}

// event is a change of a device's status.
type event struct {
	At             float64
	Status, Source string
}

// buildTimeline is a device's status over the last 7 days, matched to its
// state in the peers table.
func (d *demo) buildTimeline(id, kind string) []event {
	dv := d.dev[id]
	r := rng(seedFor("hist" + id))
	start := d.now - 7*day
	var events []event
	goOffline := func(t float64) {
		events = append(events, event{t, "DEGRADED", "signal"}, event{t + 30*1000, "CRITICAL", "signal"}, event{t + 90*1000, "OFFLINE", "signal"})
	}
	if kind == "server" {
		events = append(events, event{start, "ONLINE", "signal"})
		outages := 1 + math.Floor(r()*2)
		for i := 0.0; i < outages; i++ {
			t := start + mul(1+mul(r(), 5), day)
			goOffline(t)
			events = append(events, event{t + mul(4+mul(r(), 25), minute), "ONLINE", "heartbeat"})
		}
	} else {
		for back := 7; back >= 0; back-- {
			midnight, weekday := utcMidnight(d.now - float64(back)*day)
			if weekend(weekday) {
				continue
			}
			on := midnight + mul(6.5+mul(r(), 1.5), hour)
			off := midnight + mul(15.5+mul(r(), 2.5), hour)
			if on < start {
				continue
			}
			events = append(events, event{on, "ONLINE", "signal"})
			if r() < 0.25 {
				blip := on + mul(2+mul(r(), 4), hour)
				events = append(events, event{blip, "DEGRADED", "signal"}, event{blip + 40*1000, "ONLINE", "heartbeat"})
			}
			goOffline(off)
		}
	}
	sort.SliceStable(events, func(a, b int) bool { return events[a].At < events[b].At })

	cut := dv.LastOnline
	if dv.Online {
		cut -= 50 * minute
	}
	var out []event
	for _, e := range events {
		if e.At < cut {
			out = append(out, e)
		}
	}
	last := "OFFLINE"
	if len(out) > 0 {
		last = out[len(out)-1].Status
	}
	if dv.Online && last != "ONLINE" {
		out = append(out, event{d.now - 50*minute, "ONLINE", "signal"})
	}
	lastAt := 0.0
	if len(out) > 0 {
		lastAt = out[len(out)-1].At
	}
	if !dv.Online && last == "OFFLINE" && lastAt < dv.LastOnline-2*hour {
		// Seen once more after the last office day, e.g. a weekend check-in.
		out = append(out, event{dv.LastOnline - 40*minute, "ONLINE", "signal"})
	}
	if !dv.Online && (last != "OFFLINE" || lastAt < dv.LastOnline-2*hour) {
		out = append(out,
			event{dv.LastOnline, "DEGRADED", "signal"},
			event{dv.LastOnline + 30*1000, "CRITICAL", "signal"},
			event{dv.LastOnline + 90*1000, "OFFLINE", "signal"})
	}
	kept := out[:0]
	for _, e := range out {
		if e.At < d.now-minute {
			kept = append(kept, e)
		}
	}
	return kept
}

// statusAt is the status the timeline gives at t: before the first event
// the opposite of an ONLINE first event, else ONLINE.
func statusAt(timeline []event, t float64) string {
	s := "OFFLINE"
	if len(timeline) > 0 {
		if timeline[0].Status == "ONLINE" {
			s = "OFFLINE"
		} else {
			s = "ONLINE"
		}
	}
	for _, e := range timeline {
		if e.At > t {
			break
		}
		s = e.Status
	}
	return s
}

// The rows of a device's history: availability events, metric samples
// and remote sessions.
type availabilityRow struct {
	PeerID, From, To, Source string
	CreatedAt                stamp
}

type metricRow struct {
	PeerID         string
	CPU, Mem, Disk float64
	CreatedAt      stamp
}

type connectionRow struct {
	HostID, PeerID, PeerName, PeerIP, Username, Transport, RelayUUID string
	StartedAt, EndedAt                                               stamp
}

func (d *demo) historyRows(h historyDevice) ([]availabilityRow, []metricRow, []connectionRow) {
	r := rng(seedFor("metrics" + h.ID))
	timeline := d.buildTimeline(h.ID, h.Kind)
	var events []availabilityRow
	before := d.now
	if len(timeline) > 0 {
		before = timeline[0].At - 1
	}
	prev := statusAt(timeline, before)
	for _, e := range timeline {
		events = append(events, availabilityRow{h.ID, prev, e.Status, e.Source, stamp{e.At, sqlSeconds}})
		prev = e.Status
	}

	// Samples every 30 minutes while the device was up.
	base := 12.0
	var diskBase float64
	if h.Kind == "server" {
		base = 25
		diskBase = 55 + mul(r(), 20)
	} else {
		diskBase = 35 + mul(r(), 30)
	}
	var metrics []metricRow
	for t := d.now - 7*day; t < d.now; t += 30 * minute {
		if statusAt(timeline, t) != "ONLINE" {
			continue
		}
		hr := jsDate(t).Hour()
		busy := 0.4
		if hr >= 8 && hr <= 17 {
			busy = 1
		}
		load := mul(base, busy) + mul(r(), 30)
		if r() < 0.03 {
			load += 45
		}
		cpu := jsMin(99, load)
		mem := jsMin(97, 40+mul(busy, 15)+mul(r(), 20))
		days := (t - (d.now - 7*day)) / day
		metrics = append(metrics, metricRow{
			PeerID: h.ID,
			CPU:    round(mul(cpu, 10)) / 10, Mem: round(mul(mem, 10)) / 10,
			Disk:      round(mul(diskBase+mul(days, 0.4), 10)) / 10,
			CreatedAt: stamp{t, sqlSeconds},
		})
	}

	// Remote sessions inside online windows.
	var sessions []connectionRow
	pixel := d.dev["900100023"]
	n := 2 + math.Floor(r()*3)
	for i := 0.0; i < n; i++ {
		t := d.now - mul(0.3+mul(r(), 6.5), day)
		for tries := 0; tries < 50 && statusAt(timeline, t) != "ONLINE"; tries++ {
			t += hour
		}
		if statusAt(timeline, t) != "ONLINE" || t > d.now-30*minute {
			continue
		}
		dur := mul(3+mul(r(), 70), minute)
		web := r() < 0.4
		relay := r() < 0.5
		row := connectionRow{HostID: h.ID, StartedAt: stamp{t, sqlSeconds}, EndedAt: stamp{jsMin(t+dur, d.now-5*minute), sqlSeconds}}
		if web {
			row.PeerID, row.PeerName, row.PeerIP, row.Username = "panel-web-remote", "Web Remote", "127.0.0.1", "web_remote"
		} else {
			row.PeerID, row.PeerName, row.PeerIP = "900100023", "demo-support-pixel", pixel.IP
			row.Username = "admin"
			if r() < 0.5 {
				row.Username = "demo_operator"
			}
		}
		row.Transport = "punch_hole"
		if relay {
			row.Transport = "request_relay"
			row.RelayUUID = "demo-relay-" + h.ID + "-" + numberString(i)
		}
		sessions = append(sessions, row)
	}
	return events, metrics, sessions
}

// ticketRow is a ticket with its comments, in the order they are written:
// oldest ticket first, so ticket numbers follow creation order.
type ticketRow struct {
	Title, Description, Status, Priority, Category string
	Device, CreatedBy, AssignedTo                  any // string or nil
	SLADueAt                                       stamp
	ResolvedAt, ClosedAt                           any // stamp or nil
	CreatedAt, UpdatedAt                           stamp
	Comments                                       []commentRow
}

type commentRow struct {
	Author, Body string
	Internal     bool
	CreatedAt    stamp
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (d *demo) ticketRows() []ticketRow {
	sorted := append([]ticket(nil), tickets...)
	sort.SliceStable(sorted, func(a, b int) bool { return sorted[a].AgeHours > sorted[b].AgeHours })
	rows := make([]ticketRow, 0, len(sorted))
	for _, t := range sorted {
		created := d.now - mul(t.AgeHours, hour)
		// The last change: the newest of closing, resolving, comments and
		// creation, in hours ago.
		touches := []float64{math.Inf(1), math.Inf(1)}
		if t.ClosedAgoHours != nil {
			touches[0] = *t.ClosedAgoHours
		}
		if t.ResolvedAgoHours != nil {
			touches[1] = *t.ResolvedAgoHours
		}
		for _, c := range t.Comments {
			touches = append(touches, c.AgoHours)
		}
		touches = append(touches, t.AgeHours)
		lastTouch := jsMin(touches[0], touches[1:]...)
		row := ticketRow{
			Title: t.Title, Description: t.Description, Status: t.Status, Priority: t.Priority, Category: t.Category,
			Device: nullable(t.Device), CreatedBy: t.CreatedBy, AssignedTo: nullable(t.AssignedTo),
			SLADueAt:  stamp{created + mul(slaHours[t.Priority], hour), isoMillis},
			CreatedAt: stamp{created, sqlSeconds}, UpdatedAt: d.ago(lastTouch * 60),
		}
		if t.ResolvedAgoHours != nil {
			row.ResolvedAt = d.ago(*t.ResolvedAgoHours * 60)
		}
		if t.ClosedAgoHours != nil {
			row.ClosedAt = d.ago(*t.ClosedAgoHours * 60)
		}
		for _, c := range t.Comments {
			row.Comments = append(row.Comments, commentRow{c.Author, c.Body, c.Internal, d.ago(c.AgoHours * 60)})
		}
		rows = append(rows, row)
	}
	return rows
}

// pkgTerms are the terms of a billing package as the console's answer
// gave them.
type pkgTerms struct {
	ID              any
	Currency        any
	IncludedMinutes float64
	RoundingMinutes float64
	OverageRate     float64
}

// termsOf reads a package from the console's answer, as the seed used the
// values: numbers through Number(), the rounding 10 when it is missing or 0.
func termsOf(pkg any) pkgTerms {
	rounding := or(prop(pkg, "rounding_minutes"), 10.0)
	return pkgTerms{
		ID:              prop(pkg, "id"),
		Currency:        prop(pkg, "currency"),
		IncludedMinutes: toNumber(prop(pkg, "included_minutes")),
		RoundingMinutes: toNumber(rounding),
		OverageRate:     toNumber(prop(pkg, "overage_rate")),
	}
}

// billingRow is a billing session with its ledger and work report.
type billingRow struct {
	ID, Org                        string
	OrgID, ContractID              any
	Operator, DeviceID, DeviceName string
	RelayUUID, Status, Phase       string
	StartedAt                      stamp
	EndedAt                        any // stamp, or "" for the running session
	RawSeconds, Billed, Included   float64
	Overage, Amount                float64
	Currency                       any
	CreatedAt, UpdatedAt           stamp
	Ledger                         []ledgerRow
	Report                         *workReport
	ReportAt                       stamp
}

type ledgerRow struct {
	Event, Details string
	At             stamp
}

// billingRows are the sessions of the organizations that got a contract,
// oldest first. Each organization's included minutes are used up in that
// order; what is left over is billed at the package's rate. used ends up
// with the included minutes each organization used.
func (d *demo) billingRows(orgIDs map[string]any, terms map[string]pkgTerms, contractIDs map[string]any) ([]billingRow, map[string]float64) {
	sorted := append([]billingSession(nil), billingSessions...)
	sort.SliceStable(sorted, func(a, b int) bool { return sorted[a].StartedMinutesAgo > sorted[b].StartedMinutesAgo })
	used := map[string]float64{}
	var rows []billingRow
	for i, s := range sorted {
		pkg, hasContract := terms[s.Org]
		if !hasContract || !truthy(orgIDs[s.Org]) {
			continue
		}
		id := "demo-bs-" + leftPad(strconv.Itoa(i+1), 3)
		started := d.now - mul(s.StartedMinutesAgo, minute)
		var ended float64
		running := s.RawMinutes == nil
		if !running {
			ended = started + mul(*s.RawMinutes, minute)
		}
		rounding := pkg.RoundingMinutes
		billed := 0.0
		if !running {
			billed = mul(math.Ceil(*s.RawMinutes/rounding), rounding)
		}
		left := jsMax(0, pkg.IncludedMinutes-usedSoFar(used, s.Org))
		included := jsMin(billed, left)
		overage := billed - included
		used[s.Org] = usedSoFar(used, s.Org) + included
		amount := round(mul(mul(overage/60, pkg.OverageRate), 100)) / 100
		relay := "demo-relay-bs-" + strconv.Itoa(i+1)
		row := billingRow{
			ID: id, Org: s.Org, OrgID: orgIDs[s.Org], ContractID: contractIDs[s.Org],
			Operator: s.Operator, DeviceID: s.DeviceID, DeviceName: d.dev[s.DeviceID].Hostname,
			RelayUUID: relay, Status: s.Status, Phase: "included",
			StartedAt: stamp{started, sqlSeconds}, EndedAt: "",
			Billed: billed, Included: included, Overage: overage, Amount: amount,
			Currency: pkg.Currency, CreatedAt: stamp{started, sqlSeconds}, UpdatedAt: stamp{started, sqlSeconds},
		}
		if overage > 0 {
			row.Phase = "overage"
		}
		if !running {
			row.RawSeconds = mul(*s.RawMinutes, 60) + 17
		}
		if truthy(ended) {
			row.EndedAt = stamp{ended, sqlSeconds}
			row.UpdatedAt = stamp{ended, sqlSeconds}
		}
		row.Ledger = append(row.Ledger, ledgerRow{"session_start", relay, stamp{started, sqlSeconds}})
		if overage > 0 && truthy(ended) {
			row.Ledger = append(row.Ledger, ledgerRow{"phase_change", "included -> overage", stamp{ended - mul(overage, minute), sqlSeconds}})
		}
		if truthy(ended) {
			row.Ledger = append(row.Ledger, ledgerRow{"session_end", "billed=" + numberString(billed), stamp{ended, sqlSeconds}})
		}
		if s.Report != nil && truthy(ended) {
			row.Report = s.Report
			row.ReportAt = stamp{ended + 6*minute, sqlSeconds}
		}
		rows = append(rows, row)
	}
	return rows, used
}

// usedSoFar is the included minutes an organization has used, 0 before
// its first session (and in place of NaN, as used[org] || 0 gave).
func usedSoFar(used map[string]float64, org string) float64 {
	u := used[org]
	if math.IsNaN(u) {
		return 0
	}
	return u
}

func leftPad(s string, n int) string {
	for len(s) < n {
		s = "0" + s
	}
	return s
}

// onlineIDs are the devices fake heartbeats keep online: online, neither
// banned nor deleted.
func onlineIDs() []string {
	var ids []string
	for _, dv := range devices {
		if dv.Online && dv.Banned == "" && !dv.Deleted {
			ids = append(ids, dv.ID)
		}
	}
	return ids
}
