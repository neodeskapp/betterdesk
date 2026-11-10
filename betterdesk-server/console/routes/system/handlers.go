package system

import (
	"errors"
	"io/fs"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/unitronix/betterdesk-server/console/kit"
)

// The commands of the widgets, as the old routes ran them.
const (
	psCommand      = "ps aux --sort=-%cpu | head -16"
	dfCommand      = "df -B1 --output=target,size,used,avail 2>/dev/null | tail -n +2"
	processCommand = `powershell -NoProfile -Command "Get-Process | Sort-Object CPU -Descending | Select-Object -First 15 | ForEach-Object { $_.ProcessName + '|' + [math]::Round($_.CPU,1) + '|' + [math]::Round($_.WorkingSet64/1MB,1) }"`
	diskCommand    = `wmic logicaldisk where "DriveType=3" get DeviceID,FreeSpace,Size /format:csv`
	dockerCommand  = `docker ps -a --format "{{.Names}}|{{.Image}}|{{.State}}|{{.Status}}|{{.Ports}}" 2>/dev/null`
)

const (
	shortTimeout = 5 * time.Second
	longTimeout  = 10 * time.Second
)

// plainError is the error answer of this group: {"error":"..."} without
// the "success" member the rest of the console sends.
type plainError struct {
	Error string `json:"error"`
}

type process struct {
	Name jsText  `json:"name"`
	CPU  float64 `json:"cpu"`
	Mem  float64 `json:"mem"`
}

type disk struct {
	Mount jsText  `json:"mount"`
	Name  jsText  `json:"name,omitempty"`
	Total float64 `json:"total"`
	Used  float64 `json:"used"`
	Free  float64 `json:"free"`
}

type systemInfo struct {
	Processes []process   `json:"processes"`
	Disks     []disk      `json:"disks"`
	Node      ProcessInfo `json:"node"`
}

// info answers GET /api/system/info: the processes that use the most CPU,
// the disks, and the console's own process under the key "node".
func (h *handlers) info(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	result := systemInfo{Processes: []process{}, Disks: []disk{}}
	switch {
	case h.linux:
		result.Processes = parsePS(h.run(ctx, psCommand, shortTimeout), h.o.TotalMemory())
		result.Disks = parseDF(h.run(ctx, dfCommand, shortTimeout))
	case h.win:
		result.Processes = parseGetProcess(h.run(ctx, processCommand, longTimeout))
		result.Disks = parseWMIC(h.run(ctx, diskCommand, longTimeout))
	}
	result.Node = h.o.Process()
	kit.JSON(w, r, http.StatusOK, result)
}

// parsePS reads the output of ps aux: after the header line, every line
// with at least eleven fields is a process. Field 3 is its CPU share,
// field 4 its share of the memory, which becomes megabytes; the command
// line from field 11 on is its name, cut at 64 characters.
func parsePS(raw string, totalMemory uint64) []process {
	out := []process{}
	lines := strings.Split(raw, "\n")
	for _, line := range lines[1:] {
		parts := fields(trim(line))
		if len(parts) < 11 {
			continue
		}
		out = append(out, process{
			Name: jsText(substring(strings.Join(parts[10:], " "), 64)),
			CPU:  parseFloat(parts[2]),
			Mem:  parseFloat(parts[3]) / 100 * (float64(totalMemory) / megabyte),
		})
	}
	return out
}

// parseDF reads the output of df: mount point, size and used bytes. File
// systems of the kernel and ones under one megabyte are left out.
func parseDF(raw string) []disk {
	out := []disk{}
	for _, line := range strings.Split(raw, "\n") {
		parts := fields(trim(line))
		if len(parts) < 4 {
			continue
		}
		mount := parts[0]
		skip := false
		for _, prefix := range []string{"/dev", "/sys", "/proc", "/run", "/snap"} {
			skip = skip || strings.HasPrefix(mount, prefix)
		}
		total, used := parseIntOr(parts[1], 0), parseIntOr(parts[2], 0)
		if skip || total < megabyte {
			continue
		}
		out = append(out, disk{Mount: jsText(mount), Total: total, Used: used, Free: total - used})
	}
	return out
}

// parseGetProcess reads name|cpu|megabytes lines.
func parseGetProcess(raw string) []process {
	out := []process{}
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.Split(trim(line), "|")
		if len(parts) >= 3 {
			out = append(out, process{Name: jsText(parts[0]), CPU: parseFloat(parts[1]), Mem: parseFloat(parts[2])})
		}
	}
	return out
}

// parseWMIC reads the CSV of wmic: node, drive, free bytes, size.
func parseWMIC(raw string) []disk {
	out := []disk{}
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.Split(trim(line), ",")
		if len(parts) < 4 || parts[1] == "" {
			continue
		}
		free, total := parseIntOr(parts[2], 0), parseIntOr(parts[3], 0)
		if total > 0 {
			out = append(out, disk{Mount: jsText(parts[1]), Name: jsText(parts[1]), Total: total, Used: total - free, Free: free})
		}
	}
	return out
}

type logAnswer struct {
	Source any      `json:"source"`
	Lines  []jsText `json:"lines"`
}

// logFiles are read when the journal has nothing.
var logFiles = map[bool][]string{
	true:  {"/opt/rustdesk/betterdesk-server.log", "/opt/betterdesk/server.log", "/var/log/betterdesk-server.log"},
	false: {"/opt/rustdesk/console.log", "/var/log/betterdesk-console.log"},
}

// logs answers GET /api/logs/recent?source=go|console&limit=50: the last
// lines of the server's or the console's log, at most 200, from the
// journal or else from the first log file that exists. Any source but
// "go" is the console's log, and the source goes back as it came. A limit
// that cannot be read as text (limit[toString]=1) is answered with 500.
func (h *handlers) logs(w http.ResponseWriter, r *http.Request) {
	var source any = "console"
	if given := queryValue(r.URL.RawQuery, "source"); given != nil && given != "" {
		source = given
	}
	limitText, ok := queryText(r.URL.RawQuery, "limit")
	if !ok {
		kit.JSON(w, r, http.StatusInternalServerError, plainError{"Failed to read logs"})
		return
	}
	limit := math.Min(parseIntOr(limitText, 50), 200)
	n := numberText(limit)
	server := source == "go"

	var lines []string
	if h.linux {
		command := "journalctl -u betterdesk-console --no-pager -n " + n + " --output=short-iso 2>/dev/null"
		if server {
			command = "journalctl -u betterdesk-server --no-pager -n " + n + " --output=short-iso 2>/dev/null || journalctl -u betterdesk-go --no-pager -n " + n + " --output=short-iso 2>/dev/null"
		}
		if raw := h.run(r.Context(), command, shortTimeout); raw != "" {
			lines = strings.Split(raw, "\n")
		}
	}
	if len(lines) == 0 {
		for _, path := range logFiles[server] {
			content, err := h.o.ReadFile(path)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				kit.JSON(w, r, http.StatusInternalServerError, plainError{"Failed to read logs"})
				return
			}
			lines = lastLines(strings.Split(decodeUTF8(content), "\n"), limit)
			break
		}
	}
	answer := logAnswer{Source: source, Lines: []jsText{}}
	if text, ok := source.(string); ok {
		answer.Source = jsText(text)
	}
	for _, line := range lines {
		if trim(line) != "" {
			answer.Lines = append(answer.Lines, jsText(line))
		}
	}
	kit.JSON(w, r, http.StatusOK, answer)
}

// lastLines is lines.slice(-limit): the last limit lines, and for a
// negative limit everything from that line on.
func lastLines(lines []string, limit float64) []string {
	start := float64(len(lines)) - limit
	if limit < 0 {
		start = -limit
	}
	switch {
	case start <= 0:
		return lines
	case start >= float64(len(lines)):
		return nil
	}
	return lines[int(start):]
}

type container struct {
	Name   jsText `json:"name"`
	Image  jsText `json:"image"`
	State  jsText `json:"state"`
	Status jsText `json:"status"`
	Ports  jsText `json:"ports"`
}

type containerList struct {
	Containers []container `json:"containers"`
	Available  bool        `json:"available"`
}

// containers answers GET /api/docker/containers: what docker ps -a lists.
// Without docker the list is empty and "available" is false.
func (h *handlers) containers(w http.ResponseWriter, r *http.Request) {
	raw := h.run(r.Context(), dockerCommand, longTimeout)
	answer := containerList{Containers: []container{}, Available: raw != ""}
	if raw != "" {
		for _, line := range strings.Split(raw, "\n") {
			if trim(line) == "" {
				continue
			}
			parts := append(strings.Split(line, "|"), "", "", "", "")
			answer.Containers = append(answer.Containers, container{jsText(parts[0]), jsText(parts[1]), jsText(parts[2]), jsText(parts[3]), jsText(parts[4])})
		}
	}
	kit.JSON(w, r, http.StatusOK, answer)
}

// allowedCommands are the commands the runner takes, word for word.
var allowedCommands = []string{
	"uptime", "date", "hostname", "whoami", "df -h", "free -m",
	"uname -a", "cat /etc/os-release", "systemctl status betterdesk-server",
	"systemctl status betterdesk-console", "docker ps", "docker stats --no-stream",
	"ip addr", "ss -tlnp", "netstat -tlnp", "top -bn1 | head -20",
}

type execAnswer struct {
	Output  jsText `json:"output"`
	Command string `json:"command"`
}

// exec answers POST /api/system/exec with the body {"command":"uptime"}:
// it runs one of the allowed commands and sends what it printed. A
// command that fails or prints nothing gives "(no output)".
func (h *handlers) exec(w http.ResponseWriter, r *http.Request) {
	command, ok := kit.Body(r).String("command")
	if !ok || command == "" {
		kit.JSON(w, r, http.StatusBadRequest, plainError{"Command is required"})
		return
	}
	trimmed := trim(command)
	allowed := false
	for _, candidate := range allowedCommands {
		allowed = allowed || candidate == trimmed
	}
	if !allowed {
		kit.JSON(w, r, http.StatusForbidden, plainError{"Command not in allowed list. Allowed: " + strings.Join(allowedCommands, ", ")})
		return
	}
	output := h.run(r.Context(), trimmed, longTimeout)
	if output == "" {
		output = "(no output)"
	}
	kit.JSON(w, r, http.StatusOK, execAnswer{Output: jsText(output), Command: trimmed})
}

const (
	speedTestDefault = 1048576
	speedTestMax     = 10485760
	speedTestChunk   = 65536
)

// speedTest answers GET /api/speed-test?size=1048576 with that many
// bytes "B", at most 10 MB, for the client to time the download.
func (h *handlers) speedTest(w http.ResponseWriter, r *http.Request) {
	sizeText, _ := queryText(r.URL.RawQuery, "size")
	size := int64(math.Min(parseIntOr(sizeText, speedTestDefault), speedTestMax))
	if size < 0 {
		size = 0
	}
	header := w.Header()
	header.Set("Content-Type", "application/octet-stream")
	header.Set("Content-Length", strconv.FormatInt(size, 10))
	header.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	chunk := []byte(strings.Repeat("B", speedTestChunk))
	for remaining := size; remaining > 0; {
		n := min(remaining, speedTestChunk)
		if _, err := w.Write(chunk[:n]); err != nil {
			return
		}
		remaining -= n
	}
}
