package devicegroups

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"testing"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/goapi"
)

// The tests in this file replay what the Node console's device group
// payload helpers, tag service and address book sync answered
// (testdata/nodeoracle/vectors.js). Every record must get the same answer
// here.

func loadVectors(t *testing.T, name string, into any) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name+".json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewDecoder(zr).Decode(into); err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
}

// parsed reads recorded JSON the way the device layer hands values on:
// objects as *goapi.Object.
func parsed(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	if len(raw) == 0 {
		return nil
	}
	v, err := goapi.Parse(raw)
	if err != nil {
		t.Fatalf("vector value %s: %v", raw, err)
	}
	return v
}

func list(t *testing.T, raw json.RawMessage) []any {
	items, _ := parsed(t, raw).([]any)
	return items
}

// same compares a Go value with recorded JSON after both went through
// encoding/json.
func same(t *testing.T, got any, want json.RawMessage) bool {
	t.Helper()
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var a, b any
	if err := json.Unmarshal(encoded, &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &b); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(a, b)
}

type payloadJSON struct {
	GUID          string   `json:"guid"`
	Name          string   `json:"name"`
	SourceType    string   `json:"source_type"`
	TagFilter     string   `json:"tag_filter"`
	AllowedUsers  []string `json:"allowed_users"`
	AllowedGroups []string `json:"allowed_groups"`
	Note          *string  `json:"note,omitempty"`
	TeamID        *string  `json:"team_id,omitempty"`
}

type fieldsJSON struct {
	Name       *string `json:"name"`
	Note       *string `json:"note,omitempty"`
	TeamID     *string `json:"team_id,omitempty"`
	SourceType *string `json:"source_type"`
	TagFilter  *string `json:"tag_filter"`
}

func fieldsOf(f GroupFields) fieldsJSON {
	return fieldsJSON{Name: f.Name, Note: f.Note, TeamID: f.TeamID, SourceType: f.SourceType, TagFilter: f.TagFilter}
}

func TestOraclePayload(t *testing.T) {
	var records []struct {
		Body    map[string]any  `json:"body"`
		Payload json.RawMessage `json:"payload"`
		HasNote bool            `json:"hasNote"`
		HasTeam bool            `json:"hasTeam"`
		Create  json.RawMessage `json:"create"`
		Update  json.RawMessage `json:"update"`
	}
	loadVectors(t, "payload", &records)
	if len(records) < 1900 {
		t.Fatalf("%d payload vectors", len(records))
	}
	for _, rec := range records {
		body, _ := json.Marshal(rec.Body)
		p := NormalizeGroupPayload(rec.Body)
		got := payloadJSON{p.GUID, p.Name, p.SourceType, p.TagFilter, p.AllowedUsers, p.AllowedGroups, p.Note, p.TeamID}
		if !same(t, got, rec.Payload) || (p.Note != nil) != rec.HasNote || (p.TeamID != nil) != rec.HasTeam {
			encoded, _ := json.Marshal(got)
			t.Errorf("NormalizeGroupPayload(%s) = %s, Node %s", body, encoded, rec.Payload)
			continue
		}
		if !same(t, fieldsOf(p.CreateFields()), rec.Create) {
			t.Errorf("CreateFields of %s: Node %s", body, rec.Create)
		}
		if !same(t, fieldsOf(p.UpdateFields()), rec.Update) {
			t.Errorf("UpdateFields of %s: Node %s", body, rec.Update)
		}
	}
	if p := NormalizeGroupPayload(nil); p.Name != "" || p.SourceType != "manual" || len(p.AllowedUsers) != 0 {
		t.Errorf("a missing body: %+v", p)
	}
}

type tagRun struct {
	Kind    string          `json:"kind"`
	Devices json.RawMessage `json:"devices"`
	Groups  []struct {
		GUID       string  `json:"guid"`
		SourceType *string `json:"source_type"`
		TagFilter  string  `json:"tag_filter"`
	} `json:"groups"`
	From    json.RawMessage `json:"from"`
	To      json.RawMessage `json:"to"`
	Answers []bool          `json:"answers"`
	Result  json.RawMessage `json:"result"`
	Calls   json.RawMessage `json:"calls"`
	Updates []struct {
		GUID string `json:"guid"`
		Data struct {
			TagFilter string `json:"tag_filter"`
		} `json:"data"`
	} `json:"updates"`
	DevicesAfter json.RawMessage `json:"devicesAfter"`
	FiltersAfter []string        `json:"filtersAfter"`
}

func TestOracleTags(t *testing.T) {
	var v struct {
		Sanitize []struct {
			Value json.RawMessage `json:"value"`
			Out   string          `json:"out"`
		} `json:"sanitize"`
		Equal []struct {
			A, B string
			Out  bool
		} `json:"equal"`
		Has []struct {
			Device json.RawMessage `json:"device"`
			Tag    json.RawMessage `json:"tag"`
			Out    bool            `json:"out"`
		} `json:"has"`
		Rename []struct {
			Tags json.RawMessage `json:"tags"`
			From json.RawMessage `json:"from"`
			To   json.RawMessage `json:"to"`
			Out  struct {
				Tags    []string `json:"tags"`
				Changed bool     `json:"changed"`
			} `json:"out"`
		} `json:"rename"`
		Remove []struct {
			Tags json.RawMessage `json:"tags"`
			Tag  json.RawMessage `json:"tag"`
			Out  struct {
				Tags    []string `json:"tags"`
				Changed bool     `json:"changed"`
			} `json:"out"`
		} `json:"remove"`
		Runs []tagRun `json:"runs"`
	}
	loadVectors(t, "tags", &v)
	if len(v.Equal) < 1400 || len(v.Rename) < 800 || len(v.Runs) < 600 {
		t.Fatalf("vectors are missing: %d, %d, %d", len(v.Equal), len(v.Rename), len(v.Runs))
	}
	for _, rec := range v.Sanitize {
		if got := SanitizeTagName(parsed(t, rec.Value)); got != rec.Out {
			t.Errorf("SanitizeTagName(%s) = %q, Node %q", rec.Value, got, rec.Out)
		}
	}
	for _, rec := range v.Equal {
		if got := TagsEqual(rec.A, rec.B); got != rec.Out {
			t.Errorf("TagsEqual(%q, %q) = %v, Node %v", rec.A, rec.B, got, rec.Out)
		}
	}
	for _, rec := range v.Has {
		device, _ := parsed(t, rec.Device).(*goapi.Object)
		if got := DeviceHasTag(device, parsed(t, rec.Tag)); got != rec.Out {
			t.Errorf("DeviceHasTag(%s, %s) = %v, Node %v", rec.Device, rec.Tag, got, rec.Out)
		}
	}
	for _, rec := range v.Rename {
		tags, changed := RewriteTagsRename(parsed(t, rec.Tags), parsed(t, rec.From), parsed(t, rec.To))
		if !reflect.DeepEqual(tags, rec.Out.Tags) || changed != rec.Out.Changed {
			t.Errorf("RewriteTagsRename(%s, %s, %s) = %q, %v; Node %q, %v", rec.Tags, rec.From, rec.To, tags, changed, rec.Out.Tags, rec.Out.Changed)
		}
	}
	for _, rec := range v.Remove {
		tags, changed := RewriteTagsDelete(parsed(t, rec.Tags), parsed(t, rec.Tag))
		if !reflect.DeepEqual(tags, rec.Out.Tags) || changed != rec.Out.Changed {
			t.Errorf("RewriteTagsDelete(%s, %s) = %q, %v; Node %q, %v", rec.Tags, rec.Tag, tags, changed, rec.Out.Tags, rec.Out.Changed)
		}
	}
	for i, run := range v.Runs {
		var devices []*goapi.Object
		for _, d := range list(t, run.Devices) {
			devices = append(devices, d.(*goapi.Object))
		}
		var groups []*access.DeviceGroup
		for _, g := range run.Groups {
			group := &access.DeviceGroup{GUID: g.GUID, SourceType: "manual", TagFilter: g.TagFilter}
			if g.SourceType != nil && *g.SourceType != "" {
				group.SourceType = *g.SourceType
			}
			groups = append(groups, group)
		}
		type call struct {
			ID   any      `json:"id"`
			Tags []string `json:"tags"`
		}
		calls := []call{}
		setPeerTags := func(_ context.Context, id any, tags []string) (bool, error) {
			calls = append(calls, call{id, tags})
			return run.Answers[(len(calls)-1)%len(run.Answers)], nil
		}
		type update struct{ guid, filter string }
		var updates []update
		var result any
		if run.Kind == "rename" {
			res, err := RenameTagAcrossScope(context.Background(), devices, groups, parsed(t, run.From), parsed(t, run.To), setPeerTags,
				func(_ context.Context, guid, filter string) error {
					updates = append(updates, update{guid, filter})
					return nil
				})
			if err != nil {
				t.Fatal(err)
			}
			result = res
		} else {
			res, err := DeleteTagAcrossScope(context.Background(), devices, groups, parsed(t, run.From), setPeerTags)
			if err != nil {
				t.Fatal(err)
			}
			result = res
		}
		if !same(t, result, run.Result) {
			encoded, _ := json.Marshal(result)
			t.Errorf("run %d (%s %s to %s): %s, Node %s", i, run.Kind, run.From, run.To, encoded, run.Result)
			continue
		}
		if !same(t, calls, run.Calls) {
			encoded, _ := json.Marshal(calls)
			t.Errorf("run %d (%s): stored %s, Node %s", i, run.Kind, encoded, run.Calls)
		}
		if len(updates) != len(run.Updates) {
			t.Errorf("run %d (%s): %d group updates, Node %d", i, run.Kind, len(updates), len(run.Updates))
		}
		for j, u := range updates {
			if j < len(run.Updates) && (u.guid != run.Updates[j].GUID || u.filter != run.Updates[j].Data.TagFilter) {
				t.Errorf("run %d: group update %+v, Node %+v", i, u, run.Updates[j])
			}
		}
		after := []any{}
		for _, d := range devices {
			after = append(after, d.Get("tags"))
		}
		if !same(t, after, run.DevicesAfter) {
			t.Errorf("run %d (%s): the devices' tags afterwards differ from Node's %s", i, run.Kind, run.DevicesAfter)
		}
		for j, g := range groups {
			if g.TagFilter != run.FiltersAfter[j] {
				t.Errorf("run %d: filter of group %d is %q, Node %q", i, j, g.TagFilter, run.FiltersAfter[j])
			}
		}
	}
}

func TestOracleAddressBook(t *testing.T) {
	var v struct {
		Normalize []struct {
			Value json.RawMessage `json:"value"`
			Out   []string        `json:"out"`
		} `json:"normalize"`
		Parse []struct {
			Data json.RawMessage `json:"data"`
			Out  json.RawMessage `json:"out"`
		} `json:"parse"`
		Merge []struct {
			Data      json.RawMessage `json:"data"`
			Devices   json.RawMessage `json:"devices"`
			KnownOnly bool            `json:"knownOnly"`
			Out       string          `json:"out"`
		} `json:"merge"`
		Filter []struct {
			Data    json.RawMessage `json:"data"`
			Visible *[]string       `json:"visible"`
			Known   json.RawMessage `json:"known"`
			Out     string          `json:"out"`
		} `json:"filter"`
		Visible []struct {
			Devices json.RawMessage `json:"devices"`
			Out     []string        `json:"out"`
		} `json:"visible"`
		Updates []struct {
			Data json.RawMessage `json:"data"`
			Out  json.RawMessage `json:"out"`
		} `json:"updates"`
	}
	loadVectors(t, "addressbook", &v)
	if len(v.Normalize) < 450 || len(v.Merge) < 1500 || len(v.Filter) < 1500 || len(v.Visible) < 1300 {
		t.Fatalf("vectors are missing: %d, %d, %d, %d", len(v.Normalize), len(v.Merge), len(v.Filter), len(v.Visible))
	}
	for _, rec := range v.Normalize {
		if got := NormalizeTags(parsed(t, rec.Value)); !reflect.DeepEqual(got, rec.Out) {
			t.Errorf("NormalizeTags(%s) = %q, Node %q", rec.Value, got, rec.Out)
		}
	}
	for _, rec := range v.Parse {
		if got := stringify(ParseAddressBookData(parsed(t, rec.Data))); got != nodeText(string(rec.Out)) {
			t.Errorf("ParseAddressBookData(%s) = %s, Node %s", rec.Data, got, rec.Out)
		}
	}
	for _, rec := range v.Merge {
		got := MergeAddressBookData(parsed(t, rec.Data), MergeOptions{Devices: list(t, rec.Devices), KnownOnly: rec.KnownOnly})
		if got != nodeText(rec.Out) {
			t.Errorf("MergeAddressBookData(%s, %s, known only %v)\n   = %s\nNode %s", rec.Data, rec.Devices, rec.KnownOnly, got, rec.Out)
		}
	}
	for _, rec := range v.Filter {
		var visible func(string) bool
		if rec.Visible != nil {
			set := map[string]bool{}
			for _, id := range *rec.Visible {
				set[id] = true
			}
			visible = func(id string) bool { return set[id] }
		}
		if got := FilterAddressBookPeersByScope(parsed(t, rec.Data), visible, list(t, rec.Known)); got != nodeText(rec.Out) {
			t.Errorf("FilterAddressBookPeersByScope(%s, %v, %s)\n   = %s\nNode %s", rec.Data, rec.Visible, rec.Known, got, rec.Out)
		}
	}
	for _, rec := range v.Visible {
		if got := CollectVisibleTags(list(t, rec.Devices)); !reflect.DeepEqual(got, rec.Out) {
			t.Errorf("CollectVisibleTags(%s) = %q, Node %q", rec.Devices, got, rec.Out)
		}
	}
	for _, rec := range v.Updates {
		if got := CollectPeerTagUpdates(parsed(t, rec.Data)); !same(t, got, rec.Out) {
			encoded, _ := json.Marshal(got)
			t.Errorf("CollectPeerTagUpdates(%s) = %s, Node %s", rec.Data, encoded, rec.Out)
		}
	}
}

func TestOperatorLookupsFailAsTheyAlwaysDid(t *testing.T) {
	if names, err := ResolveOperatorUsernamesForDevice("  "); err != nil || len(names) != 0 {
		t.Fatalf("an empty id: %v, %v", names, err)
	}
	if name, err := ResolveFolderNameForDevice(""); err != nil || name != "" {
		t.Fatalf("an empty id: %q, %v", name, err)
	}
	if emails, err := ResolveOperatorEmailsForDevice(""); err != nil || len(emails) != 0 {
		t.Fatalf("an empty id: %v, %v", emails, err)
	}
	if _, err := ResolveOperatorUsernamesForDevice("dev-1"); err != ErrAssignmentsNotIterable {
		t.Fatalf("a device id: %v", err)
	}
	if _, err := ResolveOperatorEmailsForDevice("dev-1"); err != ErrAssignmentsNotIterable {
		t.Fatalf("a device id: %v", err)
	}
	if _, err := ResolveFolderNameForDevice("dev-1"); err != ErrAssignmentsNotIterable {
		t.Fatalf("a device id: %v", err)
	}
}

// halfCharacter matches half of a character outside the Basic
// Multilingual Plane as JSON.stringify writes it. Node leaves one where a
// tag is cut to 50 characters in the middle of such a character; a Go
// string cannot hold one and has U+FFFD in its place.
var halfCharacter = regexp.MustCompile(`\x5cud[89a-f][0-9a-f]{2}`)

func nodeText(s string) string { return halfCharacter.ReplaceAllString(s, "\ufffd") }
