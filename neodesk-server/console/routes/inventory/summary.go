package inventory

import (
	"math"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsjson"
)

// A report is whatever JSON a device sent. The list reads each member it
// shows by type, so an entry holds what the web app's row takes: names as
// text (a number as its text), counts and the load as numbers or null,
// the numbers of disks and apps as the length of a list. A value of
// another type is no value there. The sizes of memory and disks are read
// as Number() reads them, a text as the number it spells.

// number is a JavaScript number in an answer: null when it is no number
// or infinite.
type number float64

// MarshalJSON implements json.Marshaler.
func (n number) MarshalJSON() ([]byte, error) {
	f := float64(n)
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return []byte("null"), nil
	}
	return []byte(jsbind.NumberString(f)), nil
}

// noNumber is null in an answer.
var noNumber = number(math.NaN())

// round is Math.round: the nearest whole number, a half going up.
func round(x float64) float64 {
	floor := math.Floor(x)
	if x-floor >= 0.5 {
		return floor + 1
	}
	return floor
}

// summary is a device in the list. The three disk members are there when
// the report has a disk with a usable size.
type summary struct {
	DeviceID           any            `json:"device_id"`
	Hostname           any            `json:"hostname"`
	OS                 jsjson.Quoted  `json:"os"`
	OSName             jsjson.Quoted  `json:"os_name"`
	DiskMaxUsedPercent *number        `json:"disk_max_used_percent,omitempty"`
	DiskMaxMount       *jsjson.Quoted `json:"disk_max_mount,omitempty"`
	DiskMaxFreeBytes   *number        `json:"disk_max_free_bytes,omitempty"`
	CPU                jsjson.Quoted  `json:"cpu"`
	CPUCores           number         `json:"cpu_cores"`
	CPUUsage           number         `json:"cpu_usage"`
	MemoryTotalMB      number         `json:"memory_total_mb"`
	MemoryUsedMB       number         `json:"memory_used_mb"`
	DiskCount          int            `json:"disk_count"`
	SoftwareCount      int            `json:"software_count"`
	LastSeen           any            `json:"last_seen"`
	CollectedAt        any            `json:"collected_at"`
}

// megabyte is the divisor of the memory sizes.
const megabyte = 1048576

// megabytes is "bytes ? Math.round(bytes / 1048576) : 0".
func megabytes(bytes jsjson.Value) (number, error) {
	if !bytes.Truthy() {
		return 0, nil
	}
	n, err := bytes.Num()
	return number(round(n / megabyte)), err
}

// nameOf reads a member that names something: a text, or a finite number
// as its text. A value that is false for JavaScript (missing, null, 0,
// the empty text) and a value of another type is no name: "".
func nameOf(v jsjson.Value) string {
	if v.Truthy() {
		switch v.Kind() {
		case jsjson.String:
			return v.Str()
		case jsjson.Number:
			if !math.IsInf(v.Float(), 0) {
				return jsbind.NumberString(v.Float())
			}
		}
	}
	return ""
}

// countOf reads a member that holds a number: the number, missing where
// the report has no value there (one that is false for JavaScript), and
// null for a value of another type.
func countOf(v jsjson.Value, missing number) number {
	switch {
	case v.Kind() == jsjson.Number:
		return number(v.Float())
	case !v.Truthy():
		return missing
	}
	return noNumber
}

// summaryOf makes the list entry of a report. A size it cannot make a
// number of, or a disk name it cannot make text of (an object that has a
// member named toString), is left empty in the entry, and why is in the
// errors, so that one report never takes the list away from everyone.
func summaryOf(inv inventory) (summary, []error) {
	hw := inv.hardware
	cpu, memory, disks := hw.Get("cpu"), hw.Get("memory"), hw.Get("disks")
	osName := nameOf(hw.Get("os_name"))
	s := summary{
		DeviceID:      inv.deviceID,
		Hostname:      inv.deviceID,
		OS:            jsjson.Quoted(jsbind.Trim(osName + " " + nameOf(hw.Get("os_version")))),
		OSName:        jsjson.Quoted(osName),
		CPU:           "Unknown",
		CPUCores:      countOf(cpu.Get("logical_cores"), 0),
		CPUUsage:      countOf(cpu.Get("usage_percent"), noNumber),
		DiskCount:     len(disks.Items()),
		SoftwareCount: len(inv.software.Get("apps").Items()),
		LastSeen:      inv.receivedAt,
		CollectedAt:   inv.collectedAt,
	}
	if hostname := nameOf(hw.Get("hostname")); hostname != "" {
		s.Hostname = jsjson.Quoted(hostname)
	}
	if brand := nameOf(cpu.Get("brand")); brand != "" {
		s.CPU = jsjson.Quoted(brand)
	}

	fullest, problems := fullestDisk(disks)
	if fullest != nil {
		used, free, mount := number(round(fullest.percent*10)/10), number(round(fullest.free)), jsjson.Quoted(fullest.mount)
		s.DiskMaxUsedPercent, s.DiskMaxMount, s.DiskMaxFreeBytes = &used, &mount, &free
	}
	var err error
	for _, m := range []struct {
		into  *number
		bytes jsjson.Value
	}{{&s.MemoryTotalMB, memory.Get("total_bytes")}, {&s.MemoryUsedMB, memory.Get("used_bytes")}} {
		if *m.into, err = megabytes(m.bytes); err != nil {
			problems = append(problems, err)
		}
	}
	return s, problems
}

// disk is the fullest disk of a report.
type disk struct {
	// percent is the part of the disk that is used, 0 to 100.
	percent float64
	// free is the free space in bytes.
	free  float64
	mount string
}

// gigabyte is the factor of sizes given in GB.
const gigabyte = 1 << 30

// size reads a size of a disk: nothing for a member that is missing,
// null or the empty text, and Number() of everything else.
func size(value jsjson.Value) (n float64, given bool, err error) {
	if value.IsNullish() || value.Kind() == jsjson.String && value.Str() == "" {
		return 0, false, nil
	}
	n, err = value.Num()
	return n, true, err
}

// bytesOrGB reads a size that is given in bytes or, without that, in GB.
func bytesOrGB(disk jsjson.Value, bytes, gb string) (n float64, given bool, err error) {
	if n, given, err = size(disk.Get(bytes)); given || err != nil {
		return n, given, err
	}
	n, given, err = size(disk.Get(gb))
	return n * gigabyte, given, err
}

// fullestDisk finds the disk of a report with the largest used part, for
// the list's "disks almost full". Agents send sizes in bytes or in GB,
// and the used space itself or the space that is available or free.
// Removable disks and disks without a size above zero do not count. Of
// two disks that are equally full the first one wins. nil when no disk
// counts. What is no list has no items.
//
// A disk with a size that cannot be made a number (an object that has a
// member named toString) does not count either, and the fullest disk's
// name is empty when it cannot be made text. Both are in the errors.
func fullestDisk(disks jsjson.Value) (*disk, []error) {
	var best *disk
	var problems []error
	for _, d := range disks.Items() {
		if !d.IsObjectLike() || d.Get("removable").Truthy() {
			continue
		}
		percent, free, err := usedPart(d)
		if err != nil {
			problems = append(problems, err)
			continue
		}
		if math.IsNaN(percent) || best != nil && !(percent > best.percent) {
			continue
		}
		name, err := d.Get("mount_point").Or(d.Get("device")).Or(d.Get("name")).Or(jsjson.OfText("")).Text()
		if err != nil {
			problems = append(problems, err)
		}
		best = &disk{percent: percent, free: free, mount: jsjson.Slice(name, 120)}
	}
	return best, problems
}

// usedPart reads the sizes of a disk: the part that is used in percent
// and the free bytes. The part is NaN for a disk that does not count.
func usedPart(d jsjson.Value) (percent, free float64, err error) {
	total, given, err := bytesOrGB(d, "total_bytes", "total_gb")
	if err != nil || !given || !(total > 0) {
		return math.NaN(), 0, err
	}
	used, given, err := bytesOrGB(d, "used_bytes", "used_gb")
	for _, rest := range []string{"available_bytes", "free_bytes"} {
		if given || err != nil {
			break
		}
		var left float64
		left, given, err = size(d.Get(rest))
		used = total - left
	}
	if err != nil || !given || math.IsNaN(used) || math.IsInf(used, 0) {
		return math.NaN(), 0, err
	}
	return math.Max(0, math.Min(100, used/total*100)), math.Max(0, total-used), nil
}
