package rustdeskapi

import (
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/store"
)

// What a RustDesk client sends without signing in: heartbeats with load
// figures and its system information. Both are taken only for a device
// the server knows, and nothing is taken from a banned one.

const (
	maxIDLen       = 32
	maxHostnameLen = 256
	maxStringLen   = 512
	maxPathLen     = 1024
)

// clientDeviceID is sanitizeStr(body.id || body.uuid || ”, 32).
func clientDeviceID(body middleware.JSONObject) string {
	return sanitize(or(or(member(body, "id"), member(body, "uuid")), ""), maxIDLen)
}

// logThrottle lets a line about a device through once in five minutes
// (shouldLogSysinfoRequest).
type logThrottle struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (t *logThrottle) allow(id string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.last == nil {
		t.last = map[string]time.Time{}
	}
	if now.Sub(t.last[id]) <= 5*time.Minute {
		return false
	}
	t.last[id] = now
	if len(t.last) > 1000 {
		for key, at := range t.last {
			if now.Sub(at) > 10*time.Minute {
				delete(t.last, key)
			}
		}
	}
	return true
}

type heartbeatAnswer struct {
	ModifiedAt string `json:"modified_at"`
	Sysinfo    bool   `json:"sysinfo,omitempty"`
}

// heartbeat answers POST /api/heartbeat. It stores the load figures, marks
// the device online and asks for system information (sysinfo: true) when
// it has none or none from the last hour.
func (h *handlers) heartbeat(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	body := kit.Body(r)
	modified := func(askSysinfo bool) {
		kit.JSON(w, r, http.StatusOK, heartbeatAnswer{ModifiedAt: store.ISOTime(h.now()), Sysinfo: askSysinfo})
	}
	id := clientDeviceID(body)
	if id == "" || !isValidDeviceID(id) {
		modified(false)
		return
	}
	device, err := h.store.peerByID(ctx, id)
	if err != nil {
		h.log.Error("[API:HEARTBEAT] Device lookup failed:", err.Error())
		abort()
	}
	if device == nil {
		modified(false)
		return
	}
	if device.banned {
		fail(w, r, http.StatusOK, "BANNED")
		return
	}
	load := func(name string) float64 {
		if f, ok := number(member(body, name)); ok {
			return clamp(f, 0, 100)
		}
		return 0
	}
	cpu, memory, disk := load("cpu"), load("memory"), load("disk")
	err = h.store.insertMetric(ctx, id, cpu, memory, disk)
	if err == nil {
		err = h.store.markOnline(ctx, id)
	}
	if err != nil {
		h.log.Warn("[API:HEARTBEAT] Failed to store metrics:", err.Error())
	}
	at, found, readable, err := h.store.sysinfoUpdatedAt(ctx, id)
	if err == nil {
		switch {
		case !found:
			if h.sysinfoLog.allow(id, h.now()) {
				h.log.Info(fmt.Sprintf("[API:HEARTBEAT] Requesting sysinfo from %s (missing)", id))
			}
			modified(true)
			return
		case readable && at.UnixMilli() < h.now().UnixMilli()-int64(time.Hour/time.Millisecond):
			if h.sysinfoLog.allow(id, h.now()) {
				h.log.Info(fmt.Sprintf("[API:HEARTBEAT] Requesting sysinfo refresh from %s (stale)", id))
			}
			modified(true)
			return
		}
	}
	modified(false)
}

// plainText answers with text/plain, as res.type('text/plain').send did.
func plainText(w http.ResponseWriter, r *http.Request, text string) {
	httpx.WriteBody(w, r, http.StatusOK, "text/plain; charset=utf-8", []byte(text))
}

// jsSpace is \s of a JavaScript regular expression.
const jsSpace = `[\t\n\v\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`

var (
	ghzPart    = regexp.MustCompile(`(?i)^\d+\.?\d*GHz$`)
	ghzSuffix  = regexp.MustCompile(`(?i)GHz$`)
	coresPart  = regexp.MustCompile(`(?i)^\d+/?\d*` + jsSpace + `*cores?$`)
	coresCount = regexp.MustCompile(`^(\d+)/?(\d+)?`)
	memoryText = regexp.MustCompile(`(?i)^(\d+\.?\d*)` + jsSpace + `*GB$`)
	floatStart = regexp.MustCompile(`^[+-]?(Infinity|(\d+\.?\d*|\.\d+)([eE][+-]?\d+)?)`)
)

// parseFloatJS is parseFloat(text): the number at the start of the
// trimmed text, NaN without one.
func parseFloatJS(text string) float64 {
	m := floatStart.FindString(jsTrim(text))
	if m == "" {
		return math.NaN()
	}
	switch strings.TrimLeft(m, "+-") {
	case "Infinity":
		if strings.HasPrefix(m, "-") {
			return math.Inf(-1)
		}
		return math.Inf(1)
	}
	f, _ := strconv.ParseFloat(m, 64)
	return f
}

// orZero is n || 0.
func orZero(n float64) float64 {
	if math.IsNaN(n) {
		return 0
	}
	return n
}

// parseCPU reads "Intel Core i7-12700K, 5.2GHz, 24/16 cores": the name is
// the first part, the frequency and the cores the parts of that form.
func parseCPU(raw string) (name string, cores, ghz float64) {
	if raw == "" {
		return "", 0, 0
	}
	parts := strings.Split(raw, ",")
	for i := range parts {
		parts[i] = jsTrim(parts[i])
	}
	freq, first, second := "", "", ""
	for _, p := range parts {
		if ghzPart.MatchString(p) {
			freq = ghzSuffix.ReplaceAllString(p, "")
			break
		}
	}
	hasCores := false
	for _, p := range parts {
		if coresPart.MatchString(p) {
			if m := coresCount.FindStringSubmatch(p); m != nil {
				first, second, hasCores = m[1], m[2], true
			}
			break
		}
	}
	name = parts[0]
	if name == "" {
		name = raw
	}
	ghz = orZero(parseFloatJS(freq))
	if hasCores {
		cores = parseIntText(first)
		if cores == 0 || math.IsNaN(cores) {
			cores = orZero(parseIntText(second))
		}
	}
	return name, cores, ghz
}

// sysinfo answers POST /api/sysinfo in plain text: SYSINFO_UPDATED makes
// the client switch to PRO mode, ID_NOT_FOUND makes it try again at once,
// anything else (ERROR) after 120 seconds.
func (h *handlers) sysinfo(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	body := kit.Body(r)
	id := clientDeviceID(body)
	if id == "" || !isValidDeviceID(id) {
		h.log.Info("[API:SYSINFO] Invalid device ID:", id)
		plainText(w, r, "ID_NOT_FOUND")
		return
	}
	device, err := h.store.peerByID(ctx, id)
	if err != nil {
		h.log.Error("[API:SYSINFO] Device lookup failed:", err.Error())
		abort()
	}
	if device == nil {
		h.log.Info(fmt.Sprintf("[API:SYSINFO] Device not found: %s (must register first)", id))
		plainText(w, r, "ID_NOT_FOUND")
		return
	}
	if device.banned {
		h.log.Info("[API:SYSINFO] Rejected sysinfo from banned device: " + id)
		plainText(w, r, "ID_NOT_FOUND")
		return
	}

	text := func(name string, max int) string { return sanitize(or(member(body, name), ""), max) }
	cpuName, cpuCores, cpuGHz := parseCPU(text("cpu", maxStringLen))
	memoryGB := 0.0
	if raw := text("memory", 64); raw != "" {
		if m := memoryText.FindStringSubmatch(raw); m != nil {
			memoryGB = orZero(parseFloatJS(m[1]))
		}
	}
	limited := func(name string, lo, hi float64) float64 {
		if f, ok := number(member(body, name)); ok {
			return math.Max(lo, math.Min(hi, f))
		}
		return 0
	}
	firstSet := func(n float64, fallback func() float64) float64 {
		if n == 0 || math.IsNaN(n) {
			return fallback()
		}
		return n
	}
	list := func(name string, n int) []any {
		items, ok := member(body, name).([]any)
		if !ok {
			return []any{}
		}
		if len(items) > n {
			items = items[:n]
		}
		return items
	}
	object := func(name string) any {
		switch v := member(body, name).(type) {
		case *goapi.Object, []any:
			return v
		}
		return goapi.NewObject()
	}
	si := sysinfo{
		hostname:   text("hostname", maxHostnameLen),
		username:   text("username", maxHostnameLen),
		platform:   sanitize(or(or(member(body, "platform"), member(body, "os")), ""), maxStringLen),
		version:    text("version", 64),
		cpuName:    cpuName,
		cpuCores:   firstSet(cpuCores, func() float64 { return limited("cpu_num", 0, 1024) }),
		cpuFreqGHz: firstSet(cpuGHz, func() float64 { return limited("cpu_freq", 0, 100) }),
		memoryGB:   firstSet(memoryGB, func() float64 { return limited("memory_total", 0, 65536) }),
		osFull:     sanitize(or(or(member(body, "os"), member(body, "os_full")), ""), maxStringLen),
		displays:   list("displays", 10),
		encoding:   list("encoding", 20),
	}
	if si.cpuName == "" {
		si.cpuName = text("cpu_name", maxStringLen)
	}
	features, additions := object("features"), object("platform_additions")
	// The size limits (audit M-6), in UTF-16 units of the JSON
	if utf16Len(stringify(si.displays)) > 4096 && len(si.displays) > 4 {
		si.displays = si.displays[:4]
	}
	if utf16Len(stringify(si.encoding)) > 4096 && len(si.encoding) > 5 {
		si.encoding = si.encoding[:5]
	}
	if utf16Len(stringify(features)) > 4096 {
		features = goapi.NewObject()
	}
	si.features, si.platformAdditions = features, additions
	if err := h.store.upsertSysinfo(ctx, id, si); err != nil {
		h.log.Warn("[API:SYSINFO] Failed to store sysinfo:", err.Error())
		plainText(w, r, "ERROR")
		return
	}
	h.log.Info(fmt.Sprintf("[API:SYSINFO] PRO activated for %s: %s (%s) CPU: %s RAM: %sGB", id, si.hostname, si.platform, si.cpuName, strOr(si.memoryGB)))
	plainText(w, r, "SYSINFO_UPDATED")
}

// sysinfoVersion answers POST /api/sysinfo_ver with the empty text, which
// makes the client upload its system information again. The hash it was
// meant to answer is built from a field the stored row is never read with
// (BUGS RD1), so the answer is always empty.
func (h *handlers) sysinfoVersion(w http.ResponseWriter, r *http.Request) {
	if id := clientDeviceID(kit.Body(r)); id != "" && isValidDeviceID(id) {
		if _, _, _, err := h.store.sysinfoUpdatedAt(r.Context(), id); err != nil {
			h.log.Warn("[API:SYSINFO_VER] Error:", err.Error())
		}
	}
	plainText(w, r, "")
}
