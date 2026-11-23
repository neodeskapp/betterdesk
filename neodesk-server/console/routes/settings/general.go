package settings

import (
	"context"
	"errors"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/advancedconfig"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/restart"
)

// general holds the routes of the settings area that are not about the
// branding.
type general struct {
	d     *kit.Deps
	log   *kit.Logger
	files *advancedconfig.Service
	// version is the product version (config.appVersion).
	version string
	// started is when the routes were registered, which stands for the
	// start of the process.
	started time.Time
	// cacheVersion is the version and the start in milliseconds
	// (app.locals.cacheVersion).
	cacheVersion string
}

func newGeneral(d *kit.Deps, log *kit.Logger) *general {
	g := &general{d: d, log: log, started: time.Now()}
	var consoleDir, dataDir, keysPath string
	getenv := func(string) string { return "" }
	if d.Config != nil {
		consoleDir, dataDir, keysPath = d.Config.ConsoleDir, d.Config.DataDir, d.Config.KeysPath
		g.version = d.Config.Version
		if d.Config.Getenv != nil {
			getenv = d.Config.Getenv
		}
	}
	g.cacheVersion = g.version + "." + strconv.FormatInt(g.started.UnixMilli(), 10)
	g.files = advancedconfig.New(advancedconfig.Options{
		ConsoleDir: consoleDir, DataDir: dataDir, KeysPath: keysPath, Getenv: getenv,
	})
	if d.Restart != nil {
		d.Restart.SetRollback("advanced-file", g.rollbackFile)
	}
	return g
}

// rollbackFile undoes a saved configuration file for the restart
// transaction: a file the save created is removed, one that was there gets
// its backup back.
func (g *general) rollbackFile(_ context.Context, descriptor *goapi.Object) error {
	return g.files.RestoreBackup(descriptor.Get("fileId"), descriptor.Get("backupPath"), descriptor.Get("created") == true)
}

// answer is {success, data} with data always present, also as null.
type answer struct {
	Success bool `json:"success"`
	Data    any  `json:"data"`
}

// answerMessage is {success, data, message}.
type answerMessage struct {
	Success bool   `json:"success"`
	Data    any    `json:"data"`
	Message string `json:"message"`
}

// failure is {success: false, error} with an error of any JSON type.
type failure struct {
	Success bool `json:"success"`
	Error   any  `json:"error"`
}

// serverError answers 500 with the translated "server error".
func (g *general) serverError(w http.ResponseWriter, r *http.Request, what string, err error) {
	g.log.Error(what+":", err)
	kit.Fail(w, r, http.StatusInternalServerError, kit.T(r, "errors.server_error", nil))
}

// messageOr is err.message || fallback.
func messageOr(err error, fallback string) string {
	if msg := err.Error(); msg != "" {
		return msg
	}
	return fallback
}

// orText is "value || text" for a value from the API client.
func orText(value any, text string) any {
	if goapi.Truthy(value) {
		return value
	}
	return text
}

// statusOf is err.statusCode || 500.
func statusOf(err error) int {
	var refusal *restart.Error
	if errors.As(err, &refusal) {
		return refusal.StatusCode()
	}
	return http.StatusInternalServerError
}

// nodePlatform is process.platform for the system Go runs on.
func nodePlatform() string {
	switch runtime.GOOS {
	case "windows":
		return "win32"
	case "solaris", "illumos":
		return "sunos"
	}
	return runtime.GOOS
}

// nodeArch is process.arch for the processor Go runs on.
func nodeArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	case "mipsle":
		return "mipsel"
	case "ppc64le":
		return "ppc64"
	}
	return runtime.GOARCH
}

// hostname is os.hostname().
func hostname() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return name
}

// heapUsed is process.memoryUsage().heapUsed: the bytes of the heap in
// use.
func heapUsed() uint64 {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	return mem.HeapAlloc
}
