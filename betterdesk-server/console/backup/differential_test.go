package backup

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/branding"
	"github.com/unitronix/betterdesk-server/console/devicegroups"
)

// Differential tests of the settings snapshot and of ImportAllTables
// against the Node console (testdata/nodeoracle/roundtrip.js). Each case
// runs on two fresh installs, one restored by Node and one by Go, and the
// tables it touches must end up the same.

// fullService gives the service of an install the branding and group
// services of the console. The branding is loaded from the install's
// database as the console loads it at start.
func (in *install) fullService(t *testing.T) *Service {
	t.Helper()
	b, err := branding.New(branding.Options{
		DB:           in.db,
		UploadsDir:   filepath.Join(in.dataDir, "uploads"),
		PalettesFile: filepath.Join(webNodeJS, "config", "theme-palettes.json"),
		FaviconFile:  filepath.Join(webNodeJS, "public", "favicon.svg"),
		ThemesDir:    filepath.Join(webNodeJS, "themes"),
	})
	if err != nil {
		t.Fatal(err)
	}
	b.LoadBranding(context.Background())
	s := in.service(t, "0.0.0")
	s.o.Branding = b
	s.o.Groups = devicegroups.New(devicegroups.Options{DB: in.db})
	return s
}

// settle gives the rows the migrations made with random values fixed
// ones, so two installs start the same.
func (in *install) settle(t *testing.T) {
	t.Helper()
	mustExec(t, in.db, `UPDATE user_groups SET guid = 'default-user-group'`)
	mustExec(t, in.db, `UPDATE device_groups SET guid = 'default-device-group'`)
}

var anyTime = regexp.MustCompile(`\d{4}-\d\d-\d\d[ T]\d\d:\d\d:\d\d(\.\d+)?(Z|[+-]\d\d(:\d\d)?)?`)

// tablesOf is the snapshot of some tables with every time masked: rows
// the restore writes get the time of the moment.
func tablesOf(t *testing.T, in *install, tables []string) map[string]string {
	t.Helper()
	all := snapshot(t, in.db)
	out := map[string]string{}
	for _, name := range tables {
		rows, ok := all[name]
		if !ok {
			continue
		}
		out[name] = anyTime.ReplaceAllString(strings.Join(rows, "\n"), "<time>")
	}
	return out
}

func TestSnapshotMatchesNode(t *testing.T) {
	needNode(t)
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			src := newInstall(t, dialect)
			src.seed(t)
			var made struct {
				Text       string `json:"text"`
				AppVersion string `json:"appVersion"`
			}
			src.node(t, "snapshot-make", nil, &made)
			s := src.fullService(t)
			s.o.AppVersion = made.AppVersion
			snap, err := s.CreateBackup(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			got, err := SnapshotJSON(snap)
			if err != nil {
				t.Fatal(err)
			}
			created := regexp.MustCompile(`"_created": "[^"]*"`)
			g := created.ReplaceAllString(string(got), `"_created": ""`)
			n := created.ReplaceAllString(made.Text, `"_created": ""`)
			if g != n {
				gl, nl := strings.Split(g, "\n"), strings.Split(n, "\n")
				for i := 0; i < len(gl) && i < len(nl); i++ {
					if gl[i] != nl[i] {
						t.Fatalf("snapshot differs at line %d:\n go:   %s\n node: %s", i+1, gl[i], nl[i])
					}
				}
				t.Fatalf("snapshot differs in length: %d and %d lines", len(gl), len(nl))
			}
		})
	}
}

// snapshotRestoreDifferences are the rows Go restores otherwise than Node
// on purpose, by dialect, case and table: each pair turns a piece of
// Node's rows (times masked) into what Go writes there.
var snapshotRestoreDifferences = map[string][][2]string{
	// BK11: a rule's permissions are stored as JSON text once, not as the
	// JSON text of their JSON text
	"sqlite/odd values/strategies": {
		{`'"{\"x\":1}"'`, `'{"x":1}'`},
		{`'"{\"y\":true}"'`, `'{"y":true}'`},
	},
	"postgres/odd values/strategies": {
		{`"""{\\""x\\"":1}"""`, `"{""x"":1}"`},
		{`"""{\\""y\\"":true}"""`, `"{""y"":true}"`},
	},
	// BK2: a whole number goes into a text column as "5", not "5.0"
	"sqlite/odd values/settings":    {{`'b'|'5.0'|`, `'b'|'5'|`}},
	"sqlite/odd values/user_groups": {{`|'5.0'|'num'|`, `|'5'|'num'|`}},
}

func TestSnapshotRestoreMatchesNode(t *testing.T) {
	needNode(t)
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			src := newInstall(t, dialect)
			src.seed(t)
			var made struct {
				Text string `json:"text"`
			}
			src.node(t, "snapshot-make", nil, &made)
			base := made.Text

			odd := `{"_format":"betterdesk-backup","_version":1,"_created":5,"console":{` +
				`"settings":{"b":5,"a":"x","1":"one","t":true,"n":null,"o":{"x":1},"l":[1,2]},` +
				`"branding":{"appName":"Odd <b>","colors":{"primary":"#123456","bad":"x"},"logoUrl":"javascript:alert(1)"},` +
				`"folders":[{"name":"Büro 😀"},{"name":"new","color":"","icon":0},{"name":"new"},{"color":"#fff"}],` +
				`"userGroups":[{"guid":"g1","name":"one"},{"guid":"g1","name":"dup"},{"guid":5,"name":"num"},{"guid":"","name":"none"},{"guid":"g2"}],` +
				`"deviceGroups":[{"guid":"default-device-group","name":"taken"},{"guid":"dg","name":"d","note":0}],` +
				`"strategies":[{"guid":"s1","name":"a","permissions":"{\"x\":1}","enabled":0},{"guid":"s2","name":"b","permissions":{"y":true}},` +
				`{"guid":"s3","name":"c","permissions":"","enabled":"no"},{"guid":"g-1","name":"exists"},{"guid":"s4"}],` +
				`"addressBooks":[{"user_id":50,"ab_type":"legacy","data":"{\"tags\":[]}"},{"user_id":"51","ab_type":"x"},{"user_id":999,"ab_type":"y","data":"{}"},{"ab_type":"z"}],` +
				`"users":[{"id":60,"username":"restored","password_hash":"h","role":"viewer","unknown_column":1}]}}`
			cases := []struct {
				name string
				data string
				opts map[string]any
			}{
				{"as made", base, nil},
				{"as made with users", base, map[string]any{"restoreUsers": true}},
				{"odd values", odd, map[string]any{"restoreUsers": true}},
				{"nothing", odd, map[string]any{"restoreSettings": false, "restoreBranding": false, "restoreFolders": false, "restoreGroups": false, "restoreAddressBooks": false}},
				{"null rows", `{"_format":"betterdesk-backup","_version":1,"console":{"settings":"ab","folders":[null],"userGroups":[null],"addressBooks":[null],"users":[null]}}`, map[string]any{"restoreUsers": true}},
				{"bad users", `{"_format":"betterdesk-backup","_version":1,"console":{"users":[{"id":61,"username":"x","password_hash":"h","totp_enabled":true}]}}`, map[string]any{"restoreUsers": true}},
			}
			tables := []string{"settings", "branding_config", "users", "folders", "user_groups", "device_groups", "strategies", "address_books", "access_tokens", "user_avatars"}
			for _, c := range cases {
				t.Run(c.name, func(t *testing.T) {
					opts := SnapshotOptions{Settings: true, Branding: true, Folders: true, Groups: true, AddressBooks: true}
					flags := map[string]*bool{"restoreSettings": &opts.Settings, "restoreBranding": &opts.Branding, "restoreUsers": &opts.Users,
						"restoreFolders": &opts.Folders, "restoreGroups": &opts.Groups, "restoreAddressBooks": &opts.AddressBooks}
					for k, v := range c.opts {
						*flags[k] = v.(bool)
					}

					nodeDst := newInstall(t, dialect)
					nodeDst.seed(t)
					nodeDst.settle(t)
					var got struct {
						Result json.RawMessage `json:"result"`
					}
					nodeDst.node(t, "snapshot-restore", map[string]any{"data": json.RawMessage(c.data), "opts": c.opts}, &got)

					goDst := newInstall(t, dialect)
					goDst.seed(t)
					goDst.settle(t)
					parsed, err := branding.ParseJSON([]byte(c.data))
					if err != nil {
						t.Fatal(err)
					}
					res := goDst.fullService(t).RestoreBackup(context.Background(), parsed, opts)
					if r := restoreResultJSON(res); r != snapshotResultJSON(t, got.Result) {
						t.Errorf("result:\n go:   %s\n node: %s", r, got.Result)
					}
					want, have := tablesOf(t, nodeDst, tables), tablesOf(t, goDst, tables)
					for _, table := range tables {
						if pairs, ok := snapshotRestoreDifferences[dialect+"/"+c.name+"/"+table]; ok {
							fixed := want[table]
							for _, p := range pairs {
								if !strings.Contains(fixed, p[0]) {
									t.Errorf("table %s: Node's rows have no %s", table, p[0])
								}
								fixed = strings.ReplaceAll(fixed, p[0], p[1])
							}
							if have[table] != fixed {
								t.Errorf("table %s:\n go:   %s\n want: %s", table, have[table], fixed)
							}
							continue
						}
						if want[table] != have[table] {
							t.Errorf("table %s:\n go:   %s\n node: %s", table, have[table], want[table])
						}
					}
				})
			}
		})
	}
}

// snapshotResultJSON is a restoreBackup result in the shape
// restoreResultJSON writes (it has no requiresRestart).
func snapshotResultJSON(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	v, err := branding.ParseJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	o := v.(*branding.Object)
	o.Set("requiresRestart", false)
	return branding.Stringify(o)
}

func TestImportMatchesNode(t *testing.T) {
	needNode(t)
	dumps := []string{
		`null`, `"abc"`, `[1]`, `{"tables":null}`, `{"tables":"ab"}`, `{"tables":[[]]}`, `{"tables":5}`,
		`{"tables":{"folders":[{"id":1,"name":"a","color":true}]}}`,
		`{"tables":{"settings":[{"key":"o","value":{"a":1}},{"key":"l","value":[1,2]}]}}`,
		`{"tables":{"settings":[{"key":"l1","value":["x"]}],"folders":[{"id":2,"name":["n"]}]}}`,
		`{"tables":{"users":[null]}}`,
		`{"tables":{"audit_log":[{"id":1,"action":"x","details":{"__buf__":"aGVs bG8=junk"}},{"id":2,"action":"y","details":{"__buf__":5}}]}}`,
		`{"tables":{"nope":[],"1bad":[],"user_groups":"string","device_groups":5,"Bad-Name":[],"sqlite_sequence":[]}}`,
		`{"tables":{"peer":[{"id":"p","unknown":1,"info":"{}","1":"x","bad-col":2},5,"str",[1],{}]}}`,
		`{"tables":{"access_tokens":[{"token":"t","user_id":999,"expires_at":"2026-01-01 00:00:00"}],"users":[{"id":999,"username":"late","password_hash":"h"}]}}`,
		`{"tables":{"strategies":[{"guid":"g","name":"n","enabled":"yes","permissions":"{}"}]}}`,
		`{"tables":{"device_telemetry":[{"id":1,"device_id":"d","memory_total_bytes":1e21,"cpu_usage_percent":0.30000000000000004,"uptime_secs":-0}]}}`,
		`{"tables":{"folders":[{"id":1,"name":"a"},{"id":1,"name":"b"}],"settings":[{"key":"k","value":"v"},{"key":"k","value":"w"}]}}`,
		`{"tables":{"users":[{"id":"7","username":"s","password_hash":"h","totp_enabled":1,"created_at":"2026-11-10T01:05:50.930Z"}]}}`,
	}
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for i, raw := range dumps {
				nodeDst := newInstall(t, dialect)
				nodeDst.settle(t)
				var got struct {
					Result json.RawMessage `json:"result"`
					Error  string          `json:"error"`
				}
				nodeDst.node(t, "import", map[string]any{"dump": json.RawMessage(raw)}, &got)

				goDst := newInstall(t, dialect)
				goDst.settle(t)
				parsed, err := branding.ParseJSON([]byte(raw))
				if err != nil {
					t.Fatal(err)
				}
				res, err := ImportAllTables(context.Background(), goDst.db, parsed)
				if got.Error != "" || err != nil {
					t.Errorf("dump %d: go error %v, node error %q", i, err, got.Error)
					continue
				}
				o := branding.NewObject()
				o.Set("restored", toAny(res.Restored))
				o.Set("skipped", toAny(res.Skipped))
				o.Set("warnings", toAny(res.Warnings))
				if g, n := branding.Stringify(o), canonJSON(t, got.Result); g != n {
					t.Errorf("dump %d %s:\n go:   %s\n node: %s", i, raw, g, n)
				}
				var tables []string
				for _, table := range snapshotNames(t, goDst) {
					if strings.Contains(raw, `"`+table+`"`) {
						tables = append(tables, table)
					}
				}
				want, have := tablesOf(t, nodeDst, tables), tablesOf(t, goDst, tables)
				for _, table := range tables {
					if want[table] != have[table] {
						t.Errorf("dump %d table %s:\n go:   %s\n node: %s", i, table, have[table], want[table])
					}
				}
			}
		})
	}
}

func snapshotNames(t *testing.T, in *install) []string {
	t.Helper()
	var names []string
	for name := range snapshot(t, in.db) {
		names = append(names, name)
	}
	return names
}
