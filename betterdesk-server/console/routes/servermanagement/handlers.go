package servermanagement

import (
	"errors"
	"math"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"strconv"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/servermgmt"
)

// infoAnswer is GET /api/server-management/info. nodeVersion names the
// runtime of the console, Go's here, and pid this process (spec
// updater-installers 4.6).
type infoAnswer struct {
	Success          bool          `json:"success"`
	Platform         string        `json:"platform"`
	Arch             string        `json:"arch"`
	NodeVersion      string        `json:"nodeVersion"`
	PID              int           `json:"pid"`
	PtyAvailable     bool          `json:"ptyAvailable"`
	TerminalSessions int           `json:"terminalSessions"`
	FileRoots        []string      `json:"fileRoots"`
	MutableFileRoots []string      `json:"mutableFileRoots"`
	Server           *serverStatus `json:"server"`
	Beta             bool          `json:"beta"`
}

// serverStatus is what the API server's health says of it.
type serverStatus struct {
	Running       bool `json:"running"`
	UptimeSeconds any  `json:"uptimeSeconds"`
	Version       any  `json:"version"`
}

func (h *handlers) info(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, infoAnswer{
		Success: true, Platform: h.svc.Platform(), Arch: servermgmt.Arch(), NodeVersion: runtime.Version(),
		PID: os.Getpid(), PtyAvailable: h.svc.PtyAvailable(), TerminalSessions: h.svc.ActiveSessions(),
		FileRoots: h.svc.ReadRoots(), MutableFileRoots: h.svc.WriteRoots(),
		Server: serverFromHealth(h.d.Backend.GetHealth(r.Context())), Beta: true,
	})
}

// serverFromHealth reads the API server's health: running when its status
// is "running", the uptime from its Go duration, the version when it has
// a truthy one.
func serverFromHealth(health *goapi.Object) *serverStatus {
	s := &serverStatus{UptimeSeconds: nil, Version: nil}
	if health == nil {
		return s
	}
	status, _ := health.Get("status").(string)
	s.Running = status == "running"
	if uptime, ok := health.Get("uptime").(string); ok {
		s.UptimeSeconds = goDurationSeconds(uptime)
	}
	if v := health.Get("version"); goapi.Truthy(v) {
		s.Version = v
	}
	return s
}

// durationPart is one number and unit of a Go duration ("26h3m4.5s").
var durationPart = regexp.MustCompile(`([0-9.]+)(ms|µs|us|ns|h|m|s)`)

var durationUnits = map[string]float64{"h": 3600, "m": 60, "s": 1, "ms": 0.001, "µs": 0.000001, "us": 0.000001, "ns": 1e-9}

// goDurationSeconds turns a Go duration into whole seconds: the parts
// found anywhere in the text added up and rounded down. nil when the text
// is empty or has no part; a part like ".." makes the result null in JSON
// (NaN), as it did.
func goDurationSeconds(text string) any {
	if text == "" {
		return nil
	}
	matches := durationPart.FindAllStringSubmatch(text, -1)
	if matches == nil {
		return nil
	}
	total := 0.0
	for _, m := range matches {
		total += parseFloatJS(m[1]) * durationUnits[m[2]]
	}
	return servermgmt.Num(math.Floor(total))
}

// parseFloatJS is parseFloat for digits and dots: the longest start that
// is a number, NaN when there is none.
func parseFloatJS(text string) float64 {
	end, dot, digits := 0, false, false
	for end < len(text) {
		c := text[end]
		if c == '.' && !dot {
			dot = true
		} else if c >= '0' && c <= '9' {
			digits = true
		} else {
			break
		}
		end++
	}
	if !digits {
		return math.NaN()
	}
	f, err := strconv.ParseFloat(text[:end], 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return math.NaN()
	}
	return f
}

func (h *handlers) resources(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, struct {
		Success  bool                `json:"success"`
		Snapshot servermgmt.Snapshot `json:"snapshot"`
	}{true, h.svc.Snapshot()})
}

// queryText is String(req.query[name] || fallback).
func queryText(r *http.Request, name, fallback string) string {
	return jsbind.Query(r.URL.RawQuery, name).Or(jsbind.String(fallback)).ToString()
}

// listAnswer is a folder list with the old route's extra names for two of
// its fields (cwd, entries).
type listAnswer struct {
	Success bool               `json:"success"`
	Path    string             `json:"path"`
	Parent  string             `json:"parent"`
	Items   []servermgmt.Entry `json:"items"`
	Cwd     string             `json:"cwd"`
	Entries []servermgmt.Entry `json:"entries"`
}

func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
	listing, err := h.svc.ListDirectory(queryText(r, "path", "/"))
	if err != nil {
		fail(w, r, http.StatusBadRequest, err, "list_failed")
		return
	}
	kit.JSON(w, r, http.StatusOK, listAnswer{
		Success: true, Path: listing.Path, Parent: listing.Parent, Items: listing.Items, Cwd: listing.Path, Entries: listing.Items,
	})
}

func (h *handlers) read(w http.ResponseWriter, r *http.Request) {
	preview, err := h.svc.ReadFilePreview(queryText(r, "path", ""))
	if err != nil {
		fail(w, r, http.StatusBadRequest, err, "read_failed")
		return
	}
	kit.JSON(w, r, http.StatusOK, struct {
		Success bool `json:"success"`
		*servermgmt.Preview
	}{true, preview})
}

func (h *handlers) write(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	p, pathOK := body.String("path")
	content, contentOK := body.String("content")
	if !pathOK || !contentOK {
		kit.Fail(w, r, http.StatusBadRequest, "path_and_content_required")
		return
	}
	written, err := h.svc.WriteFile(p, content)
	if err != nil {
		fail(w, r, http.StatusBadRequest, err, "write_failed")
		return
	}
	h.audit(r, "server_file_write", "path="+written.Path+" bytes="+strconv.Itoa(written.Size))
	kit.JSON(w, r, http.StatusOK, struct {
		Success bool `json:"success"`
		*servermgmt.Written
	}{true, written})
}

// pathParam is req.body.path when it is text that is not empty (the old
// route took (req.body && req.body.path) or the empty text).
func pathParam(r *http.Request) (string, bool) {
	p, ok := kit.Body(r).String("path")
	return p, ok && p != ""
}

func (h *handlers) mkdir(w http.ResponseWriter, r *http.Request) {
	p, ok := pathParam(r)
	if !ok {
		kit.Fail(w, r, http.StatusBadRequest, "path_required")
		return
	}
	made, err := h.svc.MakeDirectory(p)
	if err != nil {
		fail(w, r, http.StatusBadRequest, err, "mkdir_failed")
		return
	}
	h.audit(r, "server_file_mkdir", "path="+made.Path)
	pathAnswer(w, r, made)
}

func (h *handlers) rename(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	from, fromOK := body.String("from")
	to, toOK := body.String("to")
	if !fromOK || !toOK {
		kit.Fail(w, r, http.StatusBadRequest, "from_and_to_required")
		return
	}
	renamed, err := h.svc.RenamePath(from, to)
	if err != nil {
		fail(w, r, http.StatusBadRequest, err, "rename_failed")
		return
	}
	h.audit(r, "server_file_rename", "from="+renamed.From+" to="+renamed.To)
	kit.JSON(w, r, http.StatusOK, struct {
		Success bool `json:"success"`
		*servermgmt.Renamed
	}{true, renamed})
}

func (h *handlers) delete(w http.ResponseWriter, r *http.Request) {
	p, ok := pathParam(r)
	if !ok {
		kit.Fail(w, r, http.StatusBadRequest, "path_required")
		return
	}
	deleted, err := h.svc.DeletePath(p)
	if err != nil {
		fail(w, r, http.StatusBadRequest, err, "delete_failed")
		return
	}
	h.audit(r, "server_file_delete", "path="+deleted.Path)
	pathAnswer(w, r, deleted)
}

func pathAnswer(w http.ResponseWriter, r *http.Request, p *servermgmt.PathResult) {
	kit.JSON(w, r, http.StatusOK, struct {
		Success bool `json:"success"`
		*servermgmt.PathResult
	}{true, p})
}

// service is one entry of the service list, with the state and status
// the old route added: the active state and the sub state, "unknown" when
// empty.
type service struct {
	Name        string  `json:"name"`
	Load        *string `json:"load,omitempty"`
	Active      string  `json:"active"`
	Sub         string  `json:"sub"`
	Description string  `json:"description"`
	State       string  `json:"state"`
	Status      string  `json:"status"`
}

func (h *handlers) services(w http.ResponseWriter, r *http.Request) {
	list := []service{}
	for _, u := range h.svc.ListServices(r.Context()) {
		state := u.Active
		if state == "" {
			state = "unknown"
		}
		status := u.Sub
		if status == "" {
			status = state
		}
		list = append(list, service{Name: u.Name, Load: u.Load, Active: u.Active, Sub: u.Sub, Description: u.Description, State: state, Status: status})
	}
	kit.JSON(w, r, http.StatusOK, struct {
		Success  bool      `json:"success"`
		Platform string    `json:"platform"`
		Services []service `json:"services"`
	}{true, h.svc.Platform(), list})
}

func (h *handlers) control(w http.ResponseWriter, r *http.Request) {
	name, action := r.PathValue("name"), r.PathValue("action")
	if !servermgmt.ServiceNamePattern.MatchString(name) {
		kit.Fail(w, r, http.StatusBadRequest, "invalid_service_name")
		return
	}
	result, err := h.svc.ControlService(r.Context(), name, action)
	var notStarted *servermgmt.StartError
	if errors.As(err, &notStarted) {
		h.log.Error("Service command could not be started:", err)
		kit.Fail(w, r, http.StatusInternalServerError, "Service command could not be started")
		return
	}
	if err != nil {
		fail(w, r, http.StatusBadRequest, err, "control_failed")
		return
	}
	h.audit(r, "server_service_control", "name="+name+" action="+action+" exit="+strconv.Itoa(result.ExitCode))
	kit.JSON(w, r, http.StatusOK, struct {
		Success bool `json:"success"`
		*servermgmt.ControlResult
	}{true, result})
}
