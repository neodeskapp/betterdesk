// Package desktop keeps each account's desktop of the console: the widget
// layout and the wallpaper it picked, and lists the wallpapers there are.
//
// A layout is stored in the settings table under desktop_layout_<user id>
// and the wallpaper under desktop_wallpaper_<user id>. When the database
// refuses the write, the layout goes to the file
// DATA_DIR/desktop-layouts/<user id>.json, which is read when the database
// holds no layout.
package desktop

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/jsreq"
	"github.com/unitronix/betterdesk-server/console/store"
)

const (
	// maxLayoutSize is the most a layout may measure as JSON text.
	maxLayoutSize = 512 * 1024
	maxWidgets    = 100
)

type handlers struct {
	store         *store.DB
	log           *kit.Logger
	dataDir       string
	wallpapersDir string
	now           func() time.Time
}

// Register adds the group's routes below its mount, /api/desktop.
func Register(d *kit.Deps) {
	h := &handlers{
		store:         d.Store,
		log:           d.Log.Child("Desktop"),
		dataDir:       d.Config.DataDir,
		wallpapersDir: d.Config.WallpapersDir,
		now:           time.Now,
	}
	r := d.Router
	r.Post("/layout", h.save, d.RequireAuth)
	r.Get("/layout", h.load, d.RequireAuth)
	r.Get("/wallpapers", h.wallpapers, d.RequireAuth)
}

// validWidgets is isValidWidgetArray: a list of at most 100 objects, each
// with id (text, at most 40) and type (text, at most 60) and the numbers
// x, y, w and h.
func validWidgets(v any) bool {
	list, ok := v.([]any)
	if !ok || len(list) > maxWidgets {
		return false
	}
	for _, entry := range list {
		w, _ := entry.(*goapi.Object)
		if w == nil {
			return false
		}
		if id, ok := w.Get("id").(string); !ok || jsreq.Length(id) > 40 {
			return false
		}
		if kind, ok := w.Get("type").(string); !ok || jsreq.Length(kind) > 60 {
			return false
		}
		for _, name := range []string{"x", "y", "w", "h"} {
			if _, ok := w.Get(name).(float64); !ok {
				return false
			}
		}
	}
	return true
}

var (
	wallpaperFile  = regexp.MustCompile(`^/wallpapers/[0-9]{1,4}\.png$`)
	wallpaperColor = regexp.MustCompile(`^solid:#[0-9a-fA-F]{3,8}$`)
)

// validWallpaper is isValidWallpaperPath: null, "", /wallpapers/<number>.png
// or solid:<hex color>.
func validWallpaper(v any) bool {
	if v == nil || v == "" {
		return true
	}
	s, ok := v.(string)
	if !ok || jsreq.Length(s) > 100 {
		return false
	}
	return wallpaperFile.MatchString(s) || wallpaperColor.MatchString(s)
}

// save answers POST /api/desktop/layout with the body {widgets,
// wallpaper}. Without widgets the layout is stored empty; without
// wallpaper the stored one stays, and a wallpaper of null or "" takes it
// away.
func (h *handlers) save(w http.ResponseWriter, r *http.Request) {
	body := jsreq.Body(r)
	object, _ := body.(*goapi.Object)
	widgets, wallpaper := object.Get("widgets"), object.Get("wallpaper")
	if object.Has("widgets") && !validWidgets(widgets) {
		kit.Fail(w, r, http.StatusBadRequest, "Invalid widget layout")
		return
	}
	if !goapi.Truthy(widgets) {
		widgets = []any{}
	}
	payload := jsreq.Stringify(widgets)
	if jsreq.Length(payload) > maxLayoutSize {
		kit.Fail(w, r, http.StatusRequestEntityTooLarge, "Layout too large")
		return
	}
	if object.Has("wallpaper") && !validWallpaper(wallpaper) {
		kit.Fail(w, r, http.StatusBadRequest, "Invalid wallpaper path")
		return
	}

	user := strconv.FormatInt(kit.UserID(r), 10)
	err := setSetting(r.Context(), h.store, layoutKey(user), payload)
	if err == nil && object.Has("wallpaper") {
		stored := wallpaper
		if stored == nil {
			stored = ""
		}
		err = setSetting(r.Context(), h.store, wallpaperKey(user), stored)
	}
	if err != nil {
		h.log.Warn("DB layout save failed, using file fallback:", err)
		if err := h.writeFallback(user, widgets, wallpaper); err != nil {
			h.log.Error("Save layout error:", err)
			kit.Fail(w, r, http.StatusInternalServerError, "Failed to save layout")
			return
		}
	}
	kit.OK(w, r, nil)
}

func layoutKey(user string) string    { return "desktop_layout_" + user }
func wallpaperKey(user string) string { return "desktop_wallpaper_" + user }

// fallbackPath is the file a layout goes to when the database refuses it.
func (h *handlers) fallbackPath(user string) string {
	return filepath.Join(h.dataDir, "desktop-layouts", user+".json")
}

func (h *handlers) writeFallback(user string, widgets, wallpaper any) error {
	if !goapi.Truthy(wallpaper) {
		wallpaper = ""
	}
	path := h.fallbackPath(user)
	if err := os.MkdirAll(filepath.Dir(path), 0o777); err != nil {
		return err
	}
	content := jsreq.Stringify(goapi.NewObject(
		"widgets", widgets,
		"wallpaper", wallpaper,
		"updated_at", h.now().UTC().Format("2006-01-02T15:04:05.000Z"),
	))
	return os.WriteFile(path, []byte(content), 0o600)
}

// readFallback returns the content of the fallback file, nil when there is
// none or it is not JSON.
func (h *handlers) readFallback(user string) any {
	raw, err := os.ReadFile(h.fallbackPath(user))
	if err != nil {
		return nil
	}
	v, err := goapi.Parse(raw)
	if err != nil {
		return nil
	}
	return v
}

type layout struct {
	Widgets   json.RawMessage `json:"widgets"`
	Wallpaper json.RawMessage `json:"wallpaper"`
}

// load answers GET /api/desktop/layout: the stored layout and wallpaper,
// an empty layout when nothing is stored or what is stored is not JSON.
func (h *handlers) load(w http.ResponseWriter, r *http.Request) {
	user := strconv.FormatInt(kit.UserID(r), 10)
	var wallpaper any = ""
	layoutRaw, err := getSetting(r.Context(), h.store, layoutKey(user))
	if err == nil {
		var stored string
		if stored, err = getSetting(r.Context(), h.store, wallpaperKey(user)); err == nil {
			wallpaper = stored
		}
	}
	if err != nil {
		h.log.Warn("DB layout load failed, using file fallback:", err)
	}

	var widgets any = []any{}
	if layoutRaw != "" {
		if parsed, err := goapi.Parse([]byte(layoutRaw)); err == nil {
			widgets = parsed
		}
	} else if fallback := h.readFallback(user); goapi.Truthy(fallback) {
		widgets = []any{}
		if list, ok := goapi.Dig(fallback, "widgets").([]any); ok {
			widgets = list
		}
		if !goapi.Truthy(wallpaper) {
			wallpaper = goapi.Dig(fallback, "wallpaper")
		}
	}
	if !goapi.Truthy(wallpaper) {
		wallpaper = ""
	}
	kit.OK(w, r, layout{
		Widgets:   json.RawMessage(jsreq.Stringify(widgets)),
		Wallpaper: json.RawMessage(jsreq.Stringify(wallpaper)),
	})
}

var wallpaperName = regexp.MustCompile(`^[0-9]+\.(?i:png)$`)

// wallpapers answers GET /api/desktop/wallpapers: the files <number>.png
// of the wallpapers folder as addresses, in the order of their numbers.
func (h *handlers) wallpapers(w http.ResponseWriter, r *http.Request) {
	files := []string{}
	if _, err := os.Stat(h.wallpapersDir); err != nil {
		kit.OK(w, r, files)
		return
	}
	entries, err := os.ReadDir(h.wallpapersDir)
	if err != nil {
		h.log.Error("List wallpapers error:", err)
		kit.Fail(w, r, http.StatusInternalServerError, "Failed to list wallpapers")
		return
	}
	var names []string
	for _, entry := range entries {
		if wallpaperName.MatchString(entry.Name()) {
			names = append(names, entry.Name())
		}
	}
	number := func(name string) float64 {
		n, _ := jsreq.ParseInt(name, 10)
		return n
	}
	sort.SliceStable(names, func(i, j int) bool { return number(names[i]) < number(names[j]) })
	for _, name := range names {
		files = append(files, "/wallpapers/"+name)
	}
	kit.OK(w, r, files)
}
