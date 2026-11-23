package settingsupdates

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
	"github.com/neodeskapp/neodesk/neodesk-server/console/updater"
)

// serverInfo answers GET /api/settings/updates/server-info: how a new
// server binary is obtained (a release download, DECISIONS A29).
func (h *handlers) serverInfo(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, answer{Success: true, Data: h.d.Updater.ServerInfo()})
}

// installGo answers POST /api/settings/updates/install-go, which
// downloaded a Go toolchain: updates install prebuilt releases (spec
// 4.6).
func (h *handlers) installGo(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusGone, failure{Error: "Updates install prebuilt releases; no Go toolchain is needed"})
}

// preflight answers GET /api/settings/updates/preflight?sha=: what an
// install needs and whether it has it.
func (h *handlers) preflight(w http.ResponseWriter, r *http.Request) {
	sha := ""
	if v := jsbind.Query(r.URL.RawQuery, "sha"); v.IsString() {
		sha = v.Str()
	}
	result := h.d.Updater.Preflight(r.Context(), sha)
	capabilities := h.d.Updater.Capabilities()
	result.Capabilities = &capabilities
	kit.JSON(w, r, http.StatusOK, answer{Success: true, Data: result})
}

// getChannel answers GET /api/settings/updates/channel.
func (h *handlers) getChannel(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, answer{Success: true, Data: h.d.Updater.Channel()})
}

// setChannel answers POST /api/settings/updates/channel with the body
// {channel: "stable" | "development"}. Any other name is refused.
func (h *handlers) setChannel(w http.ResponseWriter, r *http.Request) {
	value := jsbind.Of(kit.Body(r).Get("channel"))
	name := value.ToString()
	if !value.Truthy() || (name != updater.ChannelStable && name != updater.ChannelDevelopment) {
		kit.JSON(w, r, http.StatusBadRequest, failure{Error: "Invalid channel. Use stable or development."})
		return
	}
	result, err := h.d.Updater.SetChannel(name)
	if err != nil {
		var docker *updater.ErrDockerChannel
		if errors.As(err, &docker) {
			kit.JSON(w, r, http.StatusBadRequest, failureCode{Error: docker.Message, Code: "DOCKER_IMAGE_CHANNEL"})
			return
		}
		h.log.Error("Update channel error:", err)
		kit.JSON(w, r, http.StatusInternalServerError, failure{Error: err.Error()})
		return
	}
	kit.JSON(w, r, http.StatusOK, answer{Success: true, Data: result})
}

// githubFailure answers a failed request to GitHub: 503 with the code
// GITHUB_RATE_LIMIT for an exhausted rate limit, else 500.
func (h *handlers) githubFailure(w http.ResponseWriter, r *http.Request, prefix string, err error) {
	if updater.IsRateLimited(err) {
		kit.JSON(w, r, http.StatusServiceUnavailable, failureCode{Error: prefix + err.Error(), Code: "GITHUB_RATE_LIMIT"})
		return
	}
	kit.JSON(w, r, http.StatusInternalServerError, failureCode{Error: prefix + err.Error()})
}

// check answers GET /api/settings/updates/check.
func (h *handlers) check(w http.ResponseWriter, r *http.Request) {
	result, err := h.d.Updater.Check(r.Context())
	if err != nil {
		h.log.Error("Update check error:", err)
		h.githubFailure(w, r, "Failed to check for updates: ", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, answer{Success: true, Data: result})
}

// lastResult answers GET /api/settings/updates/last-result: the stored
// result of the last install while it still has something to show.
func (h *handlers) lastResult(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, answer{Success: true, Data: h.d.Updater.LastResult(r.Context())})
}

// installStatus answers GET /api/settings/updates/install-status.
func (h *handlers) installStatus(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, answer{Success: true, Data: h.d.Updater.InstallStatus()})
}

// reSHA is the commit the routes accept: 7 to 40 hex digits.
var reSHA = regexp.MustCompile(`^(?i)[0-9a-f]{7,40}$`)

// changes answers GET /api/settings/updates/changes?sha=.
func (h *handlers) changes(w http.ResponseWriter, r *http.Request) {
	value := jsbind.Query(r.URL.RawQuery, "sha")
	sha := value.ToString()
	if !value.Truthy() || !reSHA.MatchString(sha) {
		kit.JSON(w, r, http.StatusBadRequest, failure{Error: "Valid SHA parameter required"})
		return
	}
	result, err := h.d.Updater.Changes(r.Context(), sha)
	if err != nil {
		var refused *updater.RequestError
		if errors.As(err, &refused) {
			kit.JSON(w, r, refused.Status, failure{Error: refused.Message})
			return
		}
		h.log.Error("Get changes error:", err)
		h.githubFailure(w, r, "Failed to get changed files: ", err)
		return
	}
	kit.JSON(w, r, http.StatusOK, answer{Success: true, Data: result})
}

// dockerRefusal is the answer to an install in a Docker image install.
type dockerRefusal struct {
	Success         bool                 `json:"success"`
	Error           string               `json:"error"`
	DockerImageMode bool                 `json:"dockerImageMode"`
	DockerUpdate    updater.DockerUpdate `json:"dockerUpdate"`
}

// install answers POST /api/settings/updates/install with the body
// {remoteSHA, createBackup}: it installs the release and, under a
// supervisor, ends the process two seconds after the answer.
func (h *handlers) install(w http.ResponseWriter, r *http.Request) {
	u := h.d.Updater
	if u.ImageMode() {
		kit.JSON(w, r, http.StatusBadRequest, dockerRefusal{
			Error:           "In-app updates are disabled in Docker image deployments. Run: " + u.DockerHint(),
			DockerImageMode: true, DockerUpdate: u.DockerInstructions(),
		})
		return
	}
	body := kit.Body(r)
	remote := jsbind.Of(body.Get("remoteSHA"))
	sha := remote.ToString()
	if !remote.Truthy() || !reSHA.MatchString(sha) {
		kit.JSON(w, r, http.StatusBadRequest, failure{Error: "Valid remoteSHA is required"})
		return
	}
	backup := true
	if v := body.Get("createBackup"); v.Kind() == middleware.KindBool {
		if flag, _ := v.Bool(); !flag {
			backup = false
		}
	}
	outcome, err := u.Install(r.Context(), updater.InstallOptions{SHA: sha, Backup: backup})
	if err != nil {
		var refused *updater.RequestError
		if errors.As(err, &refused) {
			kit.JSON(w, r, refused.Status, failure{Error: refused.Message})
			return
		}
		h.log.Error("Update install error:", err)
		kit.JSON(w, r, http.StatusInternalServerError, failure{Error: "Update failed: " + err.Error()})
		return
	}
	result, installed := outcome.(*updater.InstallResult)
	if !installed {
		kit.JSON(w, r, http.StatusOK, answer{Success: true, Data: outcome})
		return
	}
	_ = h.d.LogAction(r, "system_update", fmt.Sprintf("Updated to %s (%d applied, %d failed)", first7(sha), len(result.Applied), len(result.Failed)))
	if err := u.RecordInstall(sha, result); err != nil {
		h.log.Warn("[UPDATE] Could not persist last update result:", err)
	}
	kit.JSON(w, r, http.StatusOK, answer{Success: true, Data: result})
	if result.ConsoleRestartBlocked == nil {
		h.d.Restart.ExitLater(exitDelay, "[UPDATE] Restarting console after update to "+first7(sha)+"...")
	}
}

func first7(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

// binaryStatus answers GET /api/settings/updates/server-binary/status.
func (h *handlers) binaryStatus(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, answer{Success: true, Data: h.d.Updater.BinaryStatus()})
}

// repairFailure is the answer of a failed repair.
type repairFailure struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Data    any    `json:"data"`
}

// rebuild answers POST /api/settings/updates/server-binary/rebuild: the
// release of the running version is installed again, and under a
// supervisor the process ends two seconds after the answer.
func (h *handlers) rebuild(w http.ResponseWriter, r *http.Request) {
	result := h.d.Updater.RepairResult(r.Context())
	outcome := "success"
	if !result.Success {
		outcome = "failed: " + result.Error
	}
	_ = h.d.LogAction(r, "system_update", "Rebuilt server binary ("+outcome+")")
	if !result.Success {
		kit.JSON(w, r, http.StatusInternalServerError, repairFailure{Error: result.Error, Data: result})
		return
	}
	kit.JSON(w, r, http.StatusOK, answer{Success: true, Data: result})
	if h.d.Updater.Supervised() {
		h.d.Restart.ExitLater(exitDelay, "[UPDATE] Restarting after the server binary was installed again...")
	}
}

// listBackups answers GET /api/settings/updates/backups.
func (h *handlers) listBackups(w http.ResponseWriter, r *http.Request) {
	kit.JSON(w, r, http.StatusOK, answer{Success: true, Data: h.d.Updater.ListBackups()})
}

// restore answers POST /api/settings/updates/restore with the body
// {backupName}: the backup's binary goes back in place, and under a
// supervisor the process ends two seconds after the answer. A backup of
// the old console is refused with 400 (DECISIONS A29), and so is a name
// that is no backup's or a backup that is broken, as for a delete.
func (h *handlers) restore(w http.ResponseWriter, r *http.Request) {
	name, ok := kit.Body(r).String("backupName")
	if !ok || name == "" {
		kit.JSON(w, r, http.StatusBadRequest, failure{Error: "backupName is required"})
		return
	}
	result, err := h.d.Updater.Restore(r.Context(), name)
	if err != nil {
		h.log.Error("Restore error:", err)
		status := http.StatusInternalServerError
		if updater.IsNodeBackup(err) || reBackupClientError.MatchString(err.Error()) {
			status = http.StatusBadRequest
		}
		kit.JSON(w, r, status, failure{Error: err.Error()})
		return
	}
	_ = h.d.LogAction(r, "system_restore", fmt.Sprintf("Restored from backup: %s (v%s, %d files)", name, result.Version, result.Restored))
	kit.JSON(w, r, http.StatusOK, answer{Success: true, Data: result})
	if result.ConsoleRestartBlocked == nil {
		h.d.Restart.ExitLater(exitDelay, "[UPDATE] Restarting after restore from "+name+"...")
	}
}

// reBackupClientError are the refusals of a delete the old route answered
// with 400.
var reBackupClientError = regexp.MustCompile(`(?i)invalid|not found|outside`)

// deleteBackup answers DELETE /api/settings/updates/backups/{name}.
func (h *handlers) deleteBackup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	result, err := h.d.Updater.DeleteBackup(name)
	if err != nil {
		h.log.Error("Delete backup error:", err)
		status := http.StatusInternalServerError
		if reBackupClientError.MatchString(err.Error()) {
			status = http.StatusBadRequest
		}
		kit.JSON(w, r, status, failure{Error: err.Error()})
		return
	}
	_ = h.d.LogAction(r, "backup_deleted", "Deleted update backup: "+name)
	kit.JSON(w, r, http.StatusOK, answer{Success: true, Data: result})
}

// pruneBackups answers POST /api/settings/updates/backups/prune with the
// body {keep}: the keep newest backups stay.
func (h *handlers) pruneBackups(w http.ResponseWriter, r *http.Request) {
	keep, ok := parseInt(jsbind.Of(kit.Body(r).Get("keep")))
	if !ok || keep <= 0 {
		kit.JSON(w, r, http.StatusBadRequest, failure{Error: "keep must be a positive integer"})
		return
	}
	result := h.d.Updater.PruneBackups(keep)
	if len(result.Deleted) > 0 {
		_ = h.d.LogAction(r, "backup_pruned", fmt.Sprintf("Manual prune: kept %d, deleted %d backup(s)", keep, len(result.Deleted)))
	}
	kit.JSON(w, r, http.StatusOK, answer{Success: true, Data: result})
}

// parseInt is parseInt(value, 10): the whole number at the start of
// String(value) after white space, false when there is none. Numbers too
// large for an int are refused.
func parseInt(value jsbind.Value) (int, bool) {
	if value.IsUndefined() {
		return 0, false
	}
	text := strings.TrimLeftFunc(value.ToString(), jsbind.IsSpace)
	end := 0
	if end < len(text) && (text[end] == '+' || text[end] == '-') {
		end++
	}
	start := end
	for end < len(text) && text[end] >= '0' && text[end] <= '9' {
		end++
	}
	if end == start {
		return 0, false
	}
	n, err := strconv.Atoi(text[:end])
	if err != nil {
		if text[0] == '-' {
			return 0, true
		}
		return int(^uint(0) >> 1), true
	}
	return n, true
}
