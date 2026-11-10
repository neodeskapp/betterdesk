package devicegroups_test

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/unitronix/betterdesk-server/console/devicegroups"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/store"
	"github.com/unitronix/betterdesk-server/console/store/storetest"
)

// storeRecording is what testdata/nodeoracle/store.js recorded from the
// Node console's database adapter.
type storeRecording struct {
	Dialect string            `json:"dialect"`
	Seed    []json.RawMessage `json:"seed"`
	Steps   []struct {
		Op     string            `json:"op"`
		Args   []json.RawMessage `json:"args"`
		Result *string           `json:"result"`
		Error  *string           `json:"error"`
	} `json:"steps"`
}

// argValue reads an argument of the recording: JSON, or {"$":"NaN"} and
// {"$":"undefined"}.
func argValue(t *testing.T, raw json.RawMessage) jsbind.Value {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return valueOfAny(t, v)
}

func valueOfAny(t *testing.T, v any) jsbind.Value {
	t.Helper()
	switch x := v.(type) {
	case nil:
		return jsbind.Null
	case bool:
		return jsbind.Bool(x)
	case float64:
		return jsbind.Number(x)
	case string:
		return jsbind.String(x)
	case map[string]any:
		if x["$"] == "NaN" {
			return jsbind.Number(math.NaN())
		}
		if x["$"] == "undefined" {
			return jsbind.Undefined
		}
	}
	t.Fatalf("no request value for %v", v)
	return jsbind.Undefined
}

func listArg(t *testing.T, raw json.RawMessage) []jsbind.Value {
	t.Helper()
	var items []any
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatal(err)
	}
	out := make([]jsbind.Value, len(items))
	for i, item := range items {
		out[i] = valueOfAny(t, item)
	}
	return out
}

func textsArg(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	var items []string
	if err := json.Unmarshal(raw, &items); err != nil {
		t.Fatal(err)
	}
	return items
}

func groupArg(t *testing.T, raw json.RawMessage) devicegroups.GroupData {
	t.Helper()
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	get := func(key string) jsbind.Value {
		if v, ok := fields[key]; ok {
			return valueOfAny(t, v)
		}
		return jsbind.Undefined
	}
	return devicegroups.GroupData{GUID: get("guid"), Name: get("name"), Note: get("note"), TeamID: get("team_id"), SourceType: get("source_type"), TagFilter: get("tag_filter")}
}

func textArg(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	return s
}

// Times the database fills in and random guids differ from run to run.
var (
	runTime = regexp.MustCompile(`"\d{4}-\d\d-\d\d( |T)\d\d:\d\d:\d\d(\.\d{3}Z)?"`)
	runGUID = regexp.MustCompile(`"[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}"`)
)

func normalized(s string) string {
	return runGUID.ReplaceAllString(runTime.ReplaceAllString(s, `"<time>"`), `"<guid>"`)
}

// TestStoreAgainstNode replays every call of the recordings on a database
// with the same rows: the Go store must return what the Node adapter
// returned (the JSON, byte for byte after times and random guids) and
// fail where it failed, with Node's message in Go's (the SQLite driver
// adds the kind of failure and its code).
func TestStoreAgainstNode(t *testing.T) {
	storetest.EachMigrated(t, func(t *testing.T, db *store.DB) {
		raw, err := os.ReadFile(filepath.Join("testdata", "store_"+db.Dialect().String()+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var rec storeRecording
		if err := json.Unmarshal(raw, &rec); err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		for _, seed := range rec.Seed {
			var pair struct {
				SQL    string
				Params []any
			}
			list := []any{&pair.SQL, &pair.Params}
			if err := json.Unmarshal(seed, &list); err != nil {
				t.Fatal(err)
			}
			exec(t, db, pair.SQL, pair.Params...)
		}
		s := newService(t, db)
		for i, step := range rec.Steps {
			a := step.Args
			var result any
			var err error
			switch step.Op {
			case "createFolder":
				result, err = s.CreateFolder(ctx, argValue(t, a[0]), argValue(t, a[1]), argValue(t, a[2]))
			case "getAllFolders":
				result, err = s.AllFolders(ctx)
			case "getFolderById":
				result, err = s.FolderByID(ctx, argValue(t, a[0]))
			case "updateFolder":
				err = s.UpdateFolder(ctx, argValue(t, a[0]), argValue(t, a[1]), argValue(t, a[2]), argValue(t, a[3]))
			case "deleteFolder":
				err = s.DeleteFolder(ctx, argValue(t, a[0]))
			case "assignDevicesToFolder":
				err = s.AssignDevicesToFolder(ctx, listArg(t, a[0]), argValue(t, a[1]))
			case "assignDeviceToFolder":
				err = s.AssignDeviceToFolder(ctx, argValue(t, a[0]), argValue(t, a[1]))
			case "unassignDevicesFromFolder":
				err = s.UnassignDevicesFromFolder(ctx, argValue(t, a[0]))
			case "getAllFolderAssignments":
				result, err = s.AllFolderAssignments(ctx)
			case "createDeviceGroup":
				result, err = s.CreateDeviceGroup(ctx, groupArg(t, a[0]))
			case "updateDeviceGroup":
				result, err = s.UpdateDeviceGroup(ctx, textArg(t, a[0]), groupArg(t, a[1]))
			case "deleteDeviceGroup":
				err = s.DeleteDeviceGroup(ctx, textArg(t, a[0]))
			case "setDeviceGroupUserAccess":
				result, err = s.SetDeviceGroupUserAccess(ctx, textArg(t, a[0]), textsArg(t, a[1]))
			case "setDeviceGroupUserGroupAccess":
				result, err = s.SetDeviceGroupUserGroupAccess(ctx, textArg(t, a[0]), textsArg(t, a[1]))
			case "getDeviceGroupByGuid":
				result, err = s.DeviceGroupByGUID(ctx, textArg(t, a[0]))
			case "getAllDeviceGroups":
				result, err = s.AllDeviceGroups(ctx)
			case "getAllUserGroups":
				result, err = s.AllUserGroups(ctx)
			case "getUserById":
				id := argValue(t, a[0]).ToNumber()
				var found bool
				var userID int64
				userID, found, err = s.UserIDByID(ctx, id)
				if found {
					result = userID
				}
			default:
				t.Fatalf("step %d: unknown op %s", i, step.Op)
			}
			what := step.Op + " " + string(mustJSON(t, a))
			if step.Error != nil {
				if err == nil {
					t.Errorf("step %d %s: Node failed with %q, Go did not", i, what, *step.Error)
				} else if got := jsbind.Message(err); !strings.Contains(got, *step.Error) {
					t.Errorf("step %d %s: error %q, Node %q", i, what, got, *step.Error)
				}
				continue
			}
			if err != nil {
				t.Errorf("step %d %s: %v, Node answered %s", i, what, err, *step.Result)
				continue
			}
			got := "null"
			if result != nil {
				got = string(mustJSON(t, result))
			}
			if step.Op == "getAllFolderAssignments" {
				var want, have map[string]any
				_ = json.Unmarshal([]byte(*step.Result), &want)
				_ = json.Unmarshal([]byte(got), &have)
				if !reflect.DeepEqual(want, have) {
					t.Errorf("step %d %s:\n got %s\nwant %s", i, what, got, *step.Result)
				}
				continue
			}
			if normalized(got) != normalized(*step.Result) {
				t.Errorf("step %d %s:\n got %s\nwant %s", i, what, got, *step.Result)
			}
		}
	})
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
