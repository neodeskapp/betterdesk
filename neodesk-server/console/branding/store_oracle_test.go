package branding

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store/storetest"
	"github.com/neodeskapp/neodesk/neodesk-server/internal/upstreamname"
)

// The scenario recorded in testdata/store_*.json.gz is replayed step
// by step on a fresh database. After every step the stored rows, the
// cached settings and everything made from them must be what Node had.

var (
	reRevisionField = regexp.MustCompile(`"revision":"[^"]*"`)
	reTimeFields    = regexp.MustCompile(`"(created_at|updated_at)":"[^"]*"`)
	reSQLiteTime    = regexp.MustCompile(`^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d$`)
	reISOTime       = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z$`)
)

func maskRevision(text string) string {
	return reRevisionField.ReplaceAllLiteralString(text, `"revision":"<REV>"`)
}

func maskTimes(text string) string {
	return reTimeFields.ReplaceAllString(text, `"$1":"<T>"`)
}

// fixedNow is the clock of replayed services.
var fixedNow = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

// replayService is a service on db with the installed fonts the recorded
// scenario had.
func replayService(t testing.TB, db *store.DB, fonts any) *Service {
	t.Helper()
	opts := fixtureOptions(t.TempDir())
	opts.DB = db
	opts.Now = func() time.Time { return fixedNow }
	entries(fonts, func(rel string, content any) {
		file := filepath.Join(opts.FontsDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(toString(content)), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	s, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// textOf is a recorded text: the text itself or, for a long one, its hash.
func sameText(got string, recorded any) (want string, same bool) {
	switch r := recorded.(type) {
	case string:
		return r, got == r
	case *Object:
		return "sha256 " + r.Text("sha256"), sameHash(got, r.Text("sha256"))
	}
	return "<nothing recorded>", false
}

// rowsRevisionOf is the revision of stored rows ([key, value] sorted by
// key) as the console makes it: the first eight hex digits of the SHA-256
// of every key and value, each ended by a zero byte.
func rowsRevisionOf(rows []any) string {
	pairs := make([][]any, len(rows))
	for i, row := range rows {
		pairs[i] = row.([]any)
	}
	sort.Slice(pairs, func(i, j int) bool { return toString(pairs[i][0]) < toString(pairs[j][0]) })
	h := sha256.New()
	for _, pair := range pairs {
		h.Write([]byte(toString(pair[0]) + "\x00" + toString(pair[1]) + "\x00"))
	}
	return hex.EncodeToString(h.Sum(nil))[:8]
}

func TestOracleStore(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		ctx := context.Background()
		data := loadVectors(t, "store_"+db.Dialect().String())
		s := replayService(t, db, property(data, "fonts"))
		steps, _ := property(data, "steps").([]any)
		if len(steps) < 3000 {
			t.Errorf("the recorded scenario has %d steps, it should have more than 3000", len(steps))
		}
		quickCommits(t, db)
		defaultFavicon, err := os.ReadFile(filepath.Join("testdata", "console", "public", "favicon.svg"))
		if err != nil {
			t.Fatal(err)
		}

		failures := 0
		fail := func(i int, step any, what, got, want string) {
			failures++
			if failures <= 12 {
				t.Errorf("step %d %s: %s\n got  %s\n want %s", i, clip(Stringify(step)), what, clip(got), clip(want))
			}
		}
		fixes := newStoreFixes(t, ctx, db)
		for i, entry := range steps {
			step := property(entry, "step")
			get := func(key string) any { return property(step, key) }
			id := func() int64 {
				n, _ := get("id").(float64)
				return int64(n)
			}
			fixes.before(step)
			var value any = Undefined
			var err error
			switch op := toString(get("op")); op {
			case "state":
			case "save":
				err = s.SaveBranding(ctx, get("updates"))
			case "reset":
				err = s.ResetBranding(ctx)
			case "import":
				value, err = s.ImportPreset(ctx, get("preset"))
			case "rows":
				rows, _ := get("rows").([]any)
				if err := db.InTx(ctx, func(tx *store.Tx) error {
					if _, err := tx.Exec(ctx, "DELETE FROM branding_config"); err != nil {
						return err
					}
					for _, row := range rows {
						pair := row.([]any)
						if _, err := tx.Exec(ctx, "INSERT INTO branding_config (key, value, updated_at) VALUES (?, ?, "+store.NowSQL(db.Dialect())+")", pair[0], pair[1]); err != nil {
							return err
						}
					}
					return nil
				}); err != nil {
					t.Fatalf("step %d: write rows: %v", i, err)
				}
				s.InvalidateCache()
				s.LoadBranding(ctx)
			case "load":
				s.InvalidateCache()
				s.LoadBranding(ctx)
			case "profileCreate":
				var name, description string
				err = try(func() {
					name = trim(toString(or(get("name"), "")))
					description = trim(toString(or(get("description"), "")))
				})
				if err == nil {
					value, err = s.CreateProfile(ctx, name, description, get("branding"))
				}
			case "profileUpdate":
				updates := get("updates")
				err = s.UpdateProfile(ctx, id(), ProfileUpdate{
					Name: property(updates, "name"), Description: property(updates, "description"), Branding: property(updates, "branding"),
				})
			case "profileApply":
				value, err = s.ApplyProfile(ctx, id())
			case "profileDelete":
				value, err = s.DeleteProfile(ctx, id())
			case "profileDuplicate":
				value, err = s.DuplicateProfile(ctx, id(), get("newName"))
			case "profileList":
				value, err = s.ListProfiles(ctx)
			default:
				t.Fatalf("step %d: unknown op %q", i, op)
			}
			if n, ok := value.(int64); ok {
				value = float64(n)
			}
			if list, ok := value.([]Profile); ok {
				items := make([]any, len(list))
				for j, p := range list {
					items[j] = p
				}
				value = items
			}
			fixes.after(step, value)

			// the answer of the call
			result := property(entry, "result")
			got := maskTimes(maskRevision(outcome(value, err)))
			want, _ := expected(result)
			want = maskTimes(want)
			var own *Error
			switch {
			case err != nil && property(result, "db") != Undefined && !errors.As(err, &own):
				// a failure of the database on both sides; the wording is the driver's
			case got != want && fixedProfileAnswer(step, got, want):
				fixes.fixed["BR11"]++
			case got != want && fixedImportAnswer(step, got):
				fixes.fixed["BR5"]++
			case got != want && fixes.fixedSVGAnswer(step, got, want):
				fixes.fixed["BR13"]++
			case got != want:
				fail(i, step, "answer", got, want)
			}

			// what is stored and what follows from it
			var mismatches [][3]string
			differ := func(what, got, want string) { mismatches = append(mismatches, [3]string{what, got, want}) }
			state := property(entry, "state")
			rows, queryErr := getBrandingConfigSorted(ctx, db)
			if queryErr != nil {
				t.Fatal(queryErr)
			}
			if want, same := sameText(Stringify(rows), property(state, "rows")); !same {
				differ("rows", Stringify(rows), want)
			}
			if want, same := sameText(Stringify(s.GetBranding()), property(state, "branding")); !same {
				differ("branding", Stringify(s.GetBranding()), want)
			}
			css, cssErr := s.GenerateThemeCSS()
			// the rules for this console's pages come on top of Node's
			// (BUGS L358, L359, L361; console_css_test.go)
			var cssValue any = stripConsoleRules(css)
			if got := outcome(cssValue, cssErr); true {
				if want, same := sameOutcome(got, property(state, "css")); !same {
					if fixedCSS(s.GetBranding(), got, property(state, "css")) {
						fixes.fixed["BR2"]++
					} else {
						differ("css", got, want)
					}
				}
			}
			branding := s.GetBranding()
			text := func(v any, err error) string { return maskRevision(outcome(v, err)) }
			model, modelErr := s.GetAppearanceModel(branding)
			readability, readErr := s.AssessAppearanceReadability(branding)
			favicon := s.GenerateFavicon()
			if favicon == string(defaultFavicon) {
				favicon = "<default favicon>"
			}
			derived := strings.Join([]string{
				text(model, modelErr),
				text(readability, readErr),
				text(s.GetPublicAppearance()),
				text(s.ExportPreset()),
				text(favicon, nil),
			}, "\n")
			switch recorded := property(state, "derived").(type) {
			case string:
				if !sameHash(derived, recorded) {
					differ("derived (appearance, readability, public, export, favicon)", derived, "sha256 "+recorded)
				}
			case []any:
				if want := joinValues(recorded, "\n"); derived != want {
					differ("derived (appearance, readability, public, export, favicon)", derived, want)
				}
			}

			// the revision: Node made it of the newest time of the rows, the
			// console of the rows themselves (BUGS BR9); "0" before the first
			// load on both
			if want := rowsRevisionOf(rows); toString(property(state, "revision")) == "0" {
				if s.Revision() != "0" {
					differ("revision before the first load", s.Revision(), "0")
				}
			} else if s.Revision() != want {
				differ("revision of the rows", s.Revision(), want)
			}

			if recorded := property(entry, "profiles"); recorded != Undefined {
				got := profileRows(t, ctx, db, fixes.profiles)
				want := maskTimes(Stringify(fixes.recordedProfiles(recorded)))
				if got != want {
					differ("profile rows", got, want)
				}
			}

			if fixes.compared(len(mismatches) == 0, rows) {
				for _, m := range mismatches {
					fail(i, step, m[0], m[1], m[2])
				}
			}
		}
		if failures > 12 {
			t.Errorf("%d differences in %d steps", failures, len(steps))
		}
		fixes.check()
	})
}

// quickCommits lets SQLite skip the wait for the disk at every commit. A
// replayed scenario has thousands of them and nothing to lose.
func quickCommits(t testing.TB, db *store.DB) {
	t.Helper()
	if db.IsPostgres() {
		return
	}
	if _, err := db.Exec(context.Background(), "PRAGMA synchronous = OFF"); err != nil {
		t.Fatal(err)
	}
}

// getBrandingConfigSorted reads the stored settings as [[key, value]...],
// sorted by key.
func getBrandingConfigSorted(ctx context.Context, db *store.DB) ([]any, error) {
	rows, err := db.Query(ctx, "SELECT key, value FROM branding_config ORDER BY key")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out = append(out, []any{key, value})
	}
	return out, rows.Err()
}

// profileRows is the profile table as the oracle records it, with the
// times masked and the stored settings hashed when long, without the
// profiles skip names.
func profileRows(t testing.TB, ctx context.Context, db *store.DB, skip map[int64]string) string {
	t.Helper()
	rows, err := store.Select(ctx, db, "SELECT id, name, description, data, is_active, created_at, updated_at FROM branding_profiles ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []any{}
	for rows.Next() {
		var id int64
		var name, data string
		var description store.NullString
		var active store.Bool
		var created, updated store.Time
		if err := rows.Scan(&id, &name, &description, &data, &active, &created, &updated); err != nil {
			t.Fatal(err)
		}
		if skip[id] != "" {
			continue
		}
		timeFormat := reSQLiteTime
		if db.IsPostgres() {
			timeFormat = reISOTime
		}
		for _, when := range []store.Time{created, updated} {
			if !timeFormat.MatchString(when.String()) {
				t.Errorf("profile %d: time %q is not in the format of this database", id, when.String())
			}
		}
		var storedData any = maskRevision(data)
		// Node recorded a long profile by its length and hash, with
		// upstream's names (sameHash)
		if upstream := upstreamname.Names(maskRevision(data)); len(units(upstream)) > 200 {
			if upstreamname.Holds(data) {
				t.Errorf("profile %d names upstream: %.200s", id, data)
			}
			storedData = object("len", float64(len(units(upstream))), "sha256", sha256Hex(upstream))
		}
		out = append(out, object(
			"id", float64(id), "name", name, "description", description, "is_active", active,
			"data", storedData, "created_at", created, "updated_at", updated,
		))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return maskTimes(Stringify(out))
}
