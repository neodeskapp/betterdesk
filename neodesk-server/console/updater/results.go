package updater

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
)

// The files the updater keeps in the data folder. Their names and forms
// are the old console's, so a console of either kind reads what the other
// wrote.
const (
	lastResultFile = ".last_update_result.json"
	staleFile      = ".server_binary_stale"
	inProgressFile = ".update_in_progress.json"
	shaFile        = ".update_sha"
	backupsDir     = "backups"
	updatesDir     = "updates"
)

// dataFiles reads and writes the updater's files in one data folder.
type dataFiles struct {
	dir string
	now func() time.Time
}

func (d dataFiles) path(name string) string { return filepath.Join(d.dir, name) }

func (d dataFiles) isoNow() string { return d.now().UTC().Format("2006-01-02T15:04:05.000Z") }

// writeJSON writes v with an indent of two spaces, as JSON.stringify(v,
// null, 2) did, through a temporary file.
func (d dataFiles) writeJSON(name string, v any) error {
	text, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(d.dir, 0o755); err != nil {
		return err
	}
	target := d.path(name)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, text, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// readJSON parses a file as JSON keeping the order of its keys; nil when
// the file is missing or no JSON.
func (d dataFiles) readJSON(name string) any {
	data, err := os.ReadFile(d.path(name))
	if err != nil {
		return nil
	}
	v, err := goapi.Parse(data)
	if err != nil {
		return nil
	}
	return v
}

func (d dataFiles) remove(name string) bool {
	err := os.Remove(d.path(name))
	return err == nil
}

// ---- the last result ----

// LastResult is what an install left for the panel to show
// (.last_update_result.json).
type LastResult struct {
	SavedAt               string           `json:"savedAt"`
	SHA                   string           `json:"sha"`
	Applied               int              `json:"applied"`
	Failed                []Failure        `json:"failed"`
	ServicesFailed        []ServiceFailure `json:"servicesFailed"`
	ServicesRestarted     []string         `json:"servicesRestarted"`
	ShaSaved              bool             `json:"shaSaved"`
	ConsoleRestartBlocked *string          `json:"consoleRestartBlocked"`
	ConsoleRestartNote    *string          `json:"consoleRestartNote"`
	ServerDeploy          *Deploy          `json:"serverDeploy"`
	RolledBack            bool             `json:"rolledBack,omitempty"`
}

// Failure is a step of an install that failed, under the name of what it
// was about.
type Failure struct {
	File  string `json:"file"`
	Error string `json:"error"`
}

// ServiceFailure is a service that could not be restarted.
type ServiceFailure struct {
	Service string `json:"service"`
	Error   string `json:"error"`
}

func (d dataFiles) persistResult(r LastResult) error {
	r.SavedAt = d.isoNow()
	return d.writeJSON(lastResultFile, r)
}

func (d dataFiles) readResult() any { return d.readJSON(lastResultFile) }

func (d dataFiles) clearResult() bool { return d.remove(lastResultFile) }

// nonCriticalFailures are the failures of the old updater that never
// blocked an install (updateFailurePolicy.js).
var nonCriticalFailures = map[string]bool{
	"neodesk-server": true, "neodesk-server-deploy": true, "neodesk-server.service": true,
	"server-source": true, "npm install": true, "web-ui": true, "web-ui source": true, "web-ui build": true,
	"web-ui env": true, "support-agent-source-sync": true, "support-agent-rebuild-defer": true,
	"neodesk.sh": true, "neodesk.ps1": true, "neodesk-docker.sh": true, "docker-compose.yml": true,
	"docker-compose.single.yml": true, "docker-compose.quick.yml": true, "docker-compose.quick.single.yml": true,
	"docker-compose.quick.single.macvlan.yml": true, "Dockerfile": true, "Dockerfile.server": true,
	"Dockerfile.console": true, "VERSION": true,
}

// splitFailures sorts the failures of a stored result into critical and
// non-critical ones (splitUpdateFailures). consoleDir is the old console's
// folder, where a failure about a file the old console never loaded is
// recognised.
func splitFailures(failed []any, consoleDir string) (critical, nonCritical []any) {
	critical, nonCritical = []any{}, []any{}
	for _, entry := range failed {
		if failureNonCritical(entry, consoleDir) {
			nonCritical = append(nonCritical, entry)
		} else {
			critical = append(critical, entry)
		}
	}
	return critical, nonCritical
}

func failureNonCritical(entry any, consoleDir string) bool {
	if !goapi.Truthy(entry) {
		return false
	}
	if goapi.Truthy(goapi.Dig(entry, "nonCritical")) {
		return true
	}
	file, _ := goapi.Dig(entry, "file").(string)
	if nonCriticalFailures[file] {
		return true
	}
	if file == "npm install" && goapi.Truthy(goapi.Dig(entry, "nodeModulesOk")) {
		return true
	}
	return phantomRepairFailure(file, consoleDir)
}

// phantomRepairFailure is isPhantomRepairFailure: a repair that asked for
// a file the old console never loads.
func phantomRepairFailure(file, consoleDir string) bool {
	local := strings.TrimPrefix(file, "web-nodejs/")
	if local == "" || consoleDir == "" {
		return false
	}
	if local == "routes.js" {
		return exists(filepath.Join(consoleDir, "routes", "index.js"))
	}
	if strings.HasPrefix(local, "scripts/scripts/") {
		return true
	}
	if strings.HasSuffix(local, ".js") {
		return exists(filepath.Join(consoleDir, strings.TrimSuffix(local, ".js"), "index.js"))
	}
	return false
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// resultForDisplay is resolveLastUpdateResultForDisplay: the stored
// result when it still has something to show, else nil (and a result
// that has nothing to show any more is removed).
func (d dataFiles) resultForDisplay(localSHA, remoteSHA, consoleDir string) any {
	record := d.readResult()
	if record == nil {
		return nil
	}
	recordSHA := jsText(goapi.Dig(record, "sha"))
	failed, _ := goapi.Dig(record, "failed").([]any)
	critical, nonCritical := splitFailures(failed, consoleDir)
	services, _ := goapi.Dig(record, "servicesFailed").([]any)
	hasServiceFailures := len(services) > 0
	hasRestartBlock := goapi.Truthy(goapi.Dig(record, "consoleRestartBlocked"))

	if localSHA != "" && recordSHA != "" && shaMatches(localSHA, recordSHA) {
		d.clearResult()
		return nil
	}
	if localSHA != "" && remoteSHA != "" && shaMatches(localSHA, remoteSHA) && recordSHA != "" && !shaMatches(recordSHA, remoteSHA) {
		d.clearResult()
		return nil
	}
	if len(critical) == 0 && !hasServiceFailures && !hasRestartBlock {
		if len(failed) > 0 || len(nonCritical) > 0 {
			d.clearResult()
		}
		return nil
	}
	obj, ok := record.(*goapi.Object)
	if !ok {
		return record
	}
	out := obj.Clone()
	out.Set("failed", critical)
	out.Set("nonCriticalFailures", nonCritical)
	return out
}

// jsText is String(v) for the values a stored sha can have, "" for a
// value that is not truthy.
func jsText(v any) string {
	if !goapi.Truthy(v) {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case float64, bool:
		return fmt.Sprint(t)
	}
	return ""
}

// ---- the stale binary marker ----

// BinaryStatus is the answer about the running binary
// (getServerBinaryStatus): {stale: false}, {stale: false, dockerMode:
// true}, or StaleStatus.
type BinaryStatus struct {
	Stale      bool `json:"stale"`
	DockerMode bool `json:"dockerMode,omitempty"`
}

// StaleStatus is the answer while the stale marker exists: every field
// is the marker's value when it is truthy, else null.
type StaleStatus struct {
	Stale  bool `json:"stale"`
	Reason any  `json:"reason"`
	Detail any  `json:"detail"`
	SHA    any  `json:"sha"`
	Since  any  `json:"since"`
}

type staleMarker struct {
	Stale  bool    `json:"stale"`
	Reason string  `json:"reason"`
	Detail *string `json:"detail"`
	SHA    *string `json:"sha"`
	Since  string  `json:"since"`
}

func (d dataFiles) markStale(reason, detail, sha string) error {
	return d.writeJSON(staleFile, staleMarker{Stale: true, Reason: reason, Detail: nullable(detail), SHA: nullable(sha), Since: d.isoNow()})
}

func (d dataFiles) clearStale() { d.remove(staleFile) }

// binaryStatus reads the marker. A marker that is no JSON, or is null,
// counts as none.
func (d dataFiles) binaryStatus() any {
	data, err := os.ReadFile(d.path(staleFile))
	if err != nil {
		return BinaryStatus{}
	}
	marker, err := goapi.Parse(data)
	if err != nil || marker == nil {
		return BinaryStatus{}
	}
	field := func(key string) any {
		if v := goapi.Dig(marker, key); goapi.Truthy(v) {
			return v
		}
		return nil
	}
	return StaleStatus{Stale: true, Reason: field("reason"), Detail: field("detail"), SHA: field("sha"), Since: field("since")}
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// ---- the in-progress marker and the installed commit ----

type inProgress struct {
	StartedAt string `json:"startedAt"`
	SHA       string `json:"sha"`
	PID       int    `json:"pid"`
	Version   string `json:"version,omitempty"`
}

func (d dataFiles) markInProgress(sha, version string, pid int) error {
	return d.writeJSON(inProgressFile, inProgress{StartedAt: d.isoNow(), SHA: sha, PID: pid, Version: version})
}

func (d dataFiles) clearInProgress() { d.remove(inProgressFile) }

func (d dataFiles) readInProgress() any { return d.readJSON(inProgressFile) }

// saveSHA writes the installed commit (.update_sha) for an old console
// that is started after a rollback.
func (d dataFiles) saveSHA(sha string) error {
	if !ValidSHA(sha) {
		return errors.New("not a commit")
	}
	if err := os.MkdirAll(d.dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(d.path(shaFile), []byte(strings.TrimSpace(sha)+"\n"), 0o644)
}
