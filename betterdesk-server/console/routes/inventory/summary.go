package inventory

import (
	"math"

	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsjson"
)

// A report is whatever JSON a device sent. The list reads it the way the
// old console's JavaScript did, member by member and without asking for a
// type, so every member can hold any JSON value and still has a defined
// result: a text where a number is expected is the number it spells, a
// list has a length, an object in the place of a name is shown as it is.

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
	OSName             any            `json:"os_name"`
	DiskMaxUsedPercent *number        `json:"disk_max_used_percent,omitempty"`
	DiskMaxMount       *jsjson.Quoted `json:"disk_max_mount,omitempty"`
	DiskMaxFreeBytes   *number        `json:"disk_max_free_bytes,omitempty"`
	CPU                any            `json:"cpu"`
	CPUCores           any            `json:"cpu_cores"`
	CPUUsage           jsjson.Value   `json:"cpu_usage"`
	MemoryTotalMB      number         `json:"memory_total_mb"`
	MemoryUsedMB       number         `json:"memory_used_mb"`
	DiskCount          any            `json:"disk_count"`
	SoftwareCount      any            `json:"software_count"`
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

// textOf is the text of a value that counts as true, and the empty text
// for every other value.
func textOf(value jsjson.Value) (string, error) {
	if !value.Truthy() {
		return "", nil
	}
	return value.Text()
}

// summaryOf makes the list entry of a report. A member it cannot make
// text or a number of (an object that has a member named toString) is
// left empty in the entry, and why is in the errors, so that one report
// never takes the list away from everyone.
func summaryOf(inv inventory) (summary, []error) {
	hw := inv.hardware
	cpu, memory, disks := hw.Get("cpu"), hw.Get("memory"), hw.Get("disks")
	zero, empty := jsjson.OfNumber(0), jsjson.OfText("")
	var problems []error
	note := func(err error) {
		if err != nil {
			problems = append(problems, err)
		}
	}

	s := summary{
		DeviceID:      inv.deviceID,
		Hostname:      inv.deviceID,
		OSName:        hw.Get("os_name").Or(empty),
		CPU:           cpu.Get("brand").Or(jsjson.OfText("Unknown")),
		CPUCores:      cpu.Get("logical_cores").Or(zero),
		CPUUsage:      cpu.Get("usage_percent"),
		DiskCount:     disks.Get("length").Or(zero),
		SoftwareCount: inv.software.Get("apps").Get("length").Or(zero),
		LastSeen:      inv.receivedAt,
		CollectedAt:   inv.collectedAt,
	}
	if hostname := hw.Get("hostname"); hostname.Truthy() {
		s.Hostname = hostname
	}
	if s.CPUUsage.IsNullish() {
		s.CPUUsage = jsjson.NullValue
	}
	name, err := textOf(hw.Get("os_name"))
	note(err)
	version, err := textOf(hw.Get("os_version"))
	note(err)
	s.OS = jsjson.Quoted(jsbind.Trim(name + " " + version))

	fullest, unread := fullestDisk(disks)
	problems = append(problems, unread...)
	if fullest != nil {
		used, free, mount := number(round(fullest.percent*10)/10), number(round(fullest.free)), jsjson.Quoted(fullest.mount)
		s.DiskMaxUsedPercent, s.DiskMaxMount, s.DiskMaxFreeBytes = &used, &mount, &free
	}
	s.MemoryTotalMB, err = megabytes(memory.Get("total_bytes"))
	note(err)
	s.MemoryUsedMB, err = megabytes(memory.Get("used_bytes"))
	note(err)
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
