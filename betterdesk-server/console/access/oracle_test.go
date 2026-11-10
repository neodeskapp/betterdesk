package access

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// The tests in this file replay vectors recorded from the console's Node
// modules (testdata/nodeoracle). Each vector is an input and what Node
// answered; the Go code must answer the same.

func loadVectors(t *testing.T, name string, into any) {
	t.Helper()
	file, err := os.Open(filepath.Join("testdata", name+".json.gz"))
	if err != nil {
		t.Fatalf("open vectors: %v", err)
	}
	defer file.Close()
	unzipped, err := gzip.NewReader(file)
	if err != nil {
		t.Fatalf("unzip %s: %v", name, err)
	}
	body, err := io.ReadAll(unzipped)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if err := json.Unmarshal(body, into); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
}

// jsJSON marshals v the way JSON.stringify does: compact, without escaping
// <, > and &.
func jsJSON(t *testing.T, v any) string {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// jsonOrHash is a recorded JSON text, or its SHA-256 where the text would
// bloat the fixture.
type jsonOrHash struct {
	JSON   *string `json:"json"`
	SHA256 string  `json:"sha256"`
	Error  bool    `json:"error"`
	Status *int    `json:"status"`
}

func (e jsonOrHash) check(t *testing.T, label, got string) {
	t.Helper()
	if e.JSON != nil {
		if got != *e.JSON {
			t.Fatalf("%s:\n got  %s\n want %s", label, got, *e.JSON)
		}
		return
	}
	sum := sha256.Sum256([]byte(got))
	if hex.EncodeToString(sum[:]) != e.SHA256 {
		t.Fatalf("%s: JSON differs from what Node sent (hash mismatch):\n got %s", label, got)
	}
}

// nodeAnswer is what a middleware did with a fake response.
type nodeAnswer struct {
	Next      bool            `json:"next"`
	Status    int             `json:"status"`
	JSON      json.RawMessage `json:"json"`
	Type      string          `json:"type"`
	Text      *string         `json:"text"`
	Redirect  *string         `json:"redirect"`
	ErrorPage *struct {
		Kind int `json:"kind"`
		Opts struct {
			Status  int    `json:"status"`
			Message string `json:"message"`
		} `json:"opts"`
	} `json:"errorPage"`
	Section   *string `json:"section"`
	Destroyed bool    `json:"destroyed"`
}

// nodeOutcome is one middleware call recorded twice: with the console's
// error page available and without (plain text).
type nodeOutcome struct {
	Page  nodeAnswer `json:"page"`
	Plain nodeAnswer `json:"plain"`
}

// checkDecision compares a Decision with what the Node middleware did.
func checkDecision(t *testing.T, label func() string, d Decision, want nodeOutcome, loginRedirect bool) {
	t.Helper()
	page, plain := want.Page, want.Plain
	fail := func(format string, args ...any) {
		t.Helper()
		t.Fatalf(label()+": "+format+"\n decision %+v\n node %s", append(args, d, jsJSON(t, want))...)
	}
	if d.DestroySession != page.Destroyed || d.DestroySession != plain.Destroyed {
		fail("session destroyed: got %v", d.DestroySession)
	}
	switch {
	case page.Next:
		if !d.Passed() || !plain.Next {
			fail("Node let the request pass")
		}
	case page.JSON != nil:
		if d.Answer != AnswerJSON || d.Status != page.Status || d.Body == nil {
			fail("Node answered JSON with status %d", page.Status)
		}
		if got := jsJSON(t, d.Body); got != string(page.JSON) || got != string(plain.JSON) {
			fail("JSON body: got %s", got)
		}
	case page.Redirect != nil:
		if d.Status != 302 {
			fail("redirect status %d", d.Status)
		}
		if loginRedirect {
			if d.Answer != AnswerLoginRedirect || !strings.HasPrefix(*page.Redirect, "/login") {
				fail("Node redirected to the sign-in page with the way back")
			}
			return
		}
		if d.Answer != AnswerRedirect || d.Location != *page.Redirect || plain.Redirect == nil || d.Location != *plain.Redirect {
			fail("redirect: got %q", d.Location)
		}
	case page.ErrorPage != nil:
		if d.Answer != AnswerErrorPage {
			fail("Node sent the error page")
		}
		if d.Page.Kind != page.ErrorPage.Kind || d.Status != page.Status || d.Page.Message != page.ErrorPage.Opts.Message {
			fail("error page: kind %d status %d message %q", d.Page.Kind, d.Status, d.Page.Message)
		}
		if plain.Text == nil || plain.Type != "text" || plain.Status != d.Status || *plain.Text != d.Page.Text {
			fail("plain text fallback: status %d text %q", d.Status, d.Page.Text)
		}
	default:
		fail("unknown kind of Node answer")
	}
}

func TestOracleRoleHasPermission(t *testing.T) {
	var v struct {
		Roles        []string          `json:"roles"`
		Permissions  []string          `json:"permissions"`
		Rows         map[string]string `json:"rows"`
		IsSuperAdmin map[string]bool   `json:"isSuperAdmin"`
	}
	loadVectors(t, "role_has_permission", &v)
	if len(v.Roles) < 10 || len(v.Permissions) < 45 {
		t.Fatalf("vectors look empty: %d roles, %d permissions", len(v.Roles), len(v.Permissions))
	}
	for _, role := range v.Roles {
		if got := IsSuperAdminRole(role); got != v.IsSuperAdmin[role] {
			t.Errorf("IsSuperAdminRole(%q) = %v, Node says %v", role, got, v.IsSuperAdmin[role])
		}
		for i, permission := range v.Permissions {
			want := v.Rows[role][i] == '1'
			if got := RoleHasPermission(role, permission); got != want {
				t.Errorf("RoleHasPermission(%q, %q) = %v, Node says %v", role, permission, got, want)
			}
		}
	}
}

func TestOracleFindRule(t *testing.T) {
	var v struct {
		Rules []Rule `json:"rules"`
		Cases []struct {
			Path string `json:"path"`
			Rule *Rule  `json:"rule"`
		} `json:"cases"`
		Extra  []Rule `json:"extra"`
		Nested []struct {
			Path    string  `json:"path"`
			Section *string `json:"section"`
		} `json:"nested"`
	}
	loadVectors(t, "find_rule", &v)
	if len(v.Extra) < 5 || len(v.Nested) < 15 {
		t.Fatalf("no nested rules recorded: %d rules, %d cases", len(v.Extra), len(v.Nested))
	}
	longer := append(append([]Rule(nil), RouteRules...), v.Extra...)
	for _, c := range v.Nested {
		rule, ok := findRuleIn(longer, c.Path)
		if ok != (c.Section != nil) || (ok && rule.Section != *c.Section) {
			t.Errorf("with nested rules, findRuleIn(%q) = %q (found %v), Node finds %s", c.Path, rule.Section, ok, deref(c.Section))
		}
	}
	if !reflect.DeepEqual(v.Rules, RouteRules) {
		t.Fatalf("RouteRules differ from Node's ROUTE_RULES:\n got  %+v\n want %+v", RouteRules, v.Rules)
	}
	if len(v.Cases) < 1000 {
		t.Fatalf("only %d cases", len(v.Cases))
	}
	for _, c := range v.Cases {
		rule, ok := FindRule(c.Path)
		switch {
		case c.Rule == nil && ok:
			t.Errorf("FindRule(%q) = %+v, Node finds none", c.Path, rule)
		case c.Rule != nil && (!ok || rule != *c.Rule):
			t.Errorf("FindRule(%q) = %+v (found %v), Node finds %+v", c.Path, rule, ok, *c.Rule)
		}
	}
}

// oracleSession is a session as the Node vectors describe it.
type oracleSession struct {
	UserID int64 `json:"userId"`
	User   *struct {
		Role string `json:"role"`
	} `json:"user"`
}

func (s *oracleSession) context() *Context {
	c := &Context{}
	if s == nil {
		return c
	}
	c.UserID = s.UserID
	if s.User != nil {
		c.HasUser, c.Role = true, s.User.Role
	}
	return c
}

func policyWith(section string, access Level) *Policy {
	p := &Policy{SubjectType: SubjectUser, SubjectID: "1"}
	p.Sections.Set(section, SectionAccess{Access: access})
	return p
}

func TestOracleGuard(t *testing.T) {
	var v struct {
		Methods        []string         `json:"methods"`
		RequiredLevels []Level          `json:"requiredLevels"`
		Sessions       []*oracleSession `json:"sessions"`
		PolicyStates   []string         `json:"policyStates"`
		Outcomes       []nodeOutcome    `json:"outcomes"`
		Cases          []struct {
			Path string `json:"path"`
			R    []int  `json:"r"`
		} `json:"cases"`
	}
	loadVectors(t, "guard", &v)
	for i, method := range v.Methods {
		if got := RequiredLevel(method); got != v.RequiredLevels[i] {
			t.Errorf("RequiredLevel(%s) = %s, Node says %s", method, got, v.RequiredLevels[i])
		}
	}
	checked := 0
	for _, c := range v.Cases {
		rule, _ := FindRule(c.Path)
		key := rule.Section
		if key == "" {
			key = "devices"
		}
		i := 0
		for _, method := range v.Methods {
			for si, session := range v.Sessions {
				for _, state := range v.PolicyStates {
					want := v.Outcomes[c.R[i]]
					i++
					ctx := session.context()
					switch state {
					case "no policy":
					case "load error":
						ctx.PolicyErr = errors.New("boom")
					case "section missing":
						ctx.Policy = &Policy{}
					case "load error with write":
						ctx.PolicyErr = errors.New("boom")
						ctx.Policy = policyWith(key, LevelWrite)
					default:
						ctx.Policy = policyWith(key, Level(state))
					}
					label := func() string {
						return method + " " + c.Path + " session " + jsJSON(t, v.Sessions[si]) + " policy " + state
					}
					got := ctx.Guard(method, c.Path)
					wantSection := ""
					if want.Page.Section != nil {
						wantSection = *want.Page.Section
					}
					if got.Section != wantSection || ctx.Section != wantSection {
						t.Fatalf("%s: section %q (context %q), Node set %q", label(), got.Section, ctx.Section, wantSection)
					}
					checkDecision(t, label, got.Decision, want, false)
					if want.Page.Next != (got.Verdict == VerdictNoRule || got.Verdict == VerdictPage || got.Verdict == VerdictAllowed) {
						t.Fatalf("%s: verdict %d does not fit the answer", label(), got.Verdict)
					}
					if (got.Verdict == VerdictNoRule) != (wantSection == "") {
						t.Fatalf("%s: verdict %d with section %q", label(), got.Verdict, wantSection)
					}
					checked++
				}
			}
		}
		if i != len(c.R) {
			t.Fatalf("%s: %d results, used %d", c.Path, len(c.R), i)
		}
	}
	if checked < 100000 {
		t.Fatalf("only %d guard vectors ran", checked)
	}
}

func TestOracleRequestHasPermission(t *testing.T) {
	var v struct {
		Roles           []*string `json:"roles"`
		Sections        []*string `json:"sections"`
		PolicyStates    []string  `json:"policyStates"`
		Permissions     []string  `json:"permissions"`
		EffectiveStates []string  `json:"effectiveStates"`
		LoadStates      []struct {
			Loaded bool `json:"loaded"`
			Error  bool `json:"error"`
		} `json:"loadStates"`
		Bits string `json:"bits"`
	}
	loadVectors(t, "request_has_permission", &v)
	i := 0
	for _, role := range v.Roles {
		for _, section := range v.Sections {
			for _, state := range v.PolicyStates {
				for _, permission := range v.Permissions {
					for _, effective := range v.EffectiveStates {
						for _, load := range v.LoadStates {
							c := &Context{Loaded: load.Loaded}
							if role != nil {
								c.UserID, c.HasUser, c.Role = 5, true, *role
							}
							key := "devices"
							if section != nil {
								c.Section, key = *section, *section
							}
							switch state {
							case "no policy":
							case "section missing":
								c.Policy = &Policy{}
							case "no access value":
								c.Policy = policyWith(key, "")
							default:
								c.Policy = policyWith(key, Level(state))
							}
							switch effective {
							case "empty":
								c.Permissions = PermissionSet{}
							case "has it":
								c.Permissions = newPermissionSet([]string{permission, "x.y"})
							case "has others":
								c.Permissions = PermissionSet{}
								for _, p := range v.Permissions {
									if p != permission {
										c.Permissions[p] = struct{}{}
									}
								}
							}
							if load.Error {
								c.PermissionErr = errors.New("boom")
							}
							want := v.Bits[i] == '1'
							i++
							if got := c.HasPermission(permission); got != want {
								t.Fatalf("HasPermission(%q) = %v, Node says %v\n role %v section %v policy %q effective %q load %+v",
									permission, got, want, deref(role), deref(section), state, effective, load)
							}
						}
					}
				}
			}
		}
	}
	if i != len(v.Bits) || i < 200000 {
		t.Fatalf("ran %d of %d vectors", i, len(v.Bits))
	}
}

func deref(s *string) string {
	if s == nil {
		return "<none>"
	}
	return *s
}

func TestOracleNormalize(t *testing.T) {
	var v struct {
		Levels []struct {
			Input any   `json:"input"`
			Level Level `json:"level"`
		} `json:"levels"`
		Compare []struct {
			Left   any `json:"left"`
			Right  any `json:"right"`
			Result int `json:"result"`
		} `json:"compare"`
		CeilingKeys []string `json:"ceilingKeys"`
		Ceilings    []struct {
			Permissions []string `json:"permissions"`
			Levels      []Level  `json:"levels"`
			SuperAdmin  []Level  `json:"superAdmin"`
		} `json:"ceilings"`
		Sections []struct {
			Input any             `json:"input"`
			Rows  json.RawMessage `json:"rows"`
		} `json:"sections"`
		Features []struct {
			Input any             `json:"input"`
			Rows  json.RawMessage `json:"rows"`
		} `json:"features"`
		Subjects []struct {
			Type   string  `json:"type"`
			ID     string  `json:"id"`
			Value  *string `json:"value"`
			Error  string  `json:"error"`
			Status int     `json:"status"`
		} `json:"subjects"`
		Catalog json.RawMessage `json:"catalog"`
	}
	loadVectors(t, "normalize", &v)

	for _, c := range v.Levels {
		if got := NormalizeLevel(c.Input); got != c.Level {
			t.Errorf("NormalizeLevel(%#v) = %q, Node says %q", c.Input, got, c.Level)
		}
	}
	for _, c := range v.Compare {
		got := CompareLevels(c.Left, c.Right)
		if (got > 0) != (c.Result > 0) || (got < 0) != (c.Result < 0) {
			t.Errorf("CompareLevels(%#v, %#v) = %d, Node's sign is %d", c.Left, c.Right, got, c.Result)
		}
	}
	for _, c := range v.Ceilings {
		set := newPermissionSet(c.Permissions)
		for i, key := range v.CeilingKeys {
			if got := LegacySectionCeiling(key, set.Has, false); got != c.Levels[i] {
				t.Errorf("LegacySectionCeiling(%q, %v) = %q, Node says %q", key, c.Permissions, got, c.Levels[i])
			}
			if got := LegacySectionCeiling(key, set.Has, true); got != c.SuperAdmin[i] {
				t.Errorf("LegacySectionCeiling(%q, super admin) = %q, Node says %q", key, got, c.SuperAdmin[i])
			}
		}
	}
	for _, c := range v.Sections {
		if got := jsJSON(t, NormalizeSectionPolicies(c.Input)); got != string(c.Rows) {
			t.Errorf("NormalizeSectionPolicies(%s)\n got  %s\n want %s", jsJSON(t, c.Input), got, c.Rows)
		}
	}
	for _, c := range v.Features {
		if got := jsJSON(t, NormalizeFeaturePolicies(c.Input)); got != string(c.Rows) {
			t.Errorf("NormalizeFeaturePolicies(%s)\n got  %s\n want %s", jsJSON(t, c.Input), got, c.Rows)
		}
	}
	for _, c := range v.Subjects {
		got, err := ValidateSubject(c.Type, c.ID)
		if c.Value != nil {
			if err != nil || got != *c.Value {
				t.Errorf("ValidateSubject(%q, %q) = %q, %v; Node says %q", c.Type, c.ID, got, err, *c.Value)
			}
			continue
		}
		var se *StatusError
		if !errors.As(err, &se) || se.Status != c.Status || se.Message != c.Error {
			t.Errorf("ValidateSubject(%q, %q) = %q, %v; Node refuses with %d %q", c.Type, c.ID, got, err, c.Status, c.Error)
		}
	}
	if got := jsJSON(t, GetCatalog()); got != string(v.Catalog) {
		t.Errorf("GetCatalog()\n got  %s\n want %s", got, v.Catalog)
	}
}

func TestOracleAccessAllows(t *testing.T) {
	var v struct {
		Shapes       []string  `json:"shapes"`
		AccessValues []*string `json:"accessValues"`
		Required     []*string `json:"required"`
		Bits         string    `json:"bits"`
	}
	loadVectors(t, "access_allows", &v)
	i := 0
	for _, shape := range v.Shapes {
		for _, access := range v.AccessValues {
			for _, required := range v.Required {
				var policy *Policy
				level := Level("")
				if access != nil {
					level = Level(*access)
				}
				switch shape {
				case "no sections":
					policy = &Policy{}
				case "other section":
					policy = policyWith("users", level)
				case "section":
					policy = policyWith("devices", level)
				}
				need := LevelRead
				if required != nil {
					need = Level(*required)
				}
				want := v.Bits[i] == '1'
				i++
				if got := AccessAllows(policy, "devices", need); got != want {
					t.Errorf("AccessAllows(%s, access %s, required %s) = %v, Node says %v", shape, deref(access), deref(required), got, want)
				}
			}
		}
	}
	if i != len(v.Bits) || i == 0 {
		t.Fatalf("ran %d of %d vectors", i, len(v.Bits))
	}
}

// fakeData serves stored rows from memory.
type fakeData struct {
	sections     map[string][]SectionRow
	features     map[string][]FeatureRow
	userGroups   []UserGroup
	deviceGroups []DeviceGroup
	members      map[string][]string
	grants       []string
	settingValue string
	failing      string
}

var errFake = errors.New("fake failure")

func (f *fakeData) PanelAccessPolicies(_ context.Context, subjectType, subjectID string) ([]SectionRow, error) {
	if f.failing == "policies" {
		return nil, errFake
	}
	return f.sections[subjectType+":"+subjectID], nil
}

func (f *fakeData) ClientFeaturePolicies(_ context.Context, subjectType, subjectID string) ([]FeatureRow, error) {
	return f.features[subjectType+":"+subjectID], nil
}

func (f *fakeData) UserGroupsForUser(context.Context, int64) ([]UserGroup, error) {
	if f.failing == "userGroups" {
		return nil, errFake
	}
	return f.userGroups, nil
}

func (f *fakeData) AllDeviceGroups(context.Context) ([]DeviceGroup, error) {
	if f.failing == "deviceGroups" {
		return nil, errFake
	}
	return f.deviceGroups, nil
}

func (f *fakeData) DeviceGroupMembers(_ context.Context, guid string) ([]string, error) {
	if f.failing == "members" {
		return nil, errFake
	}
	return f.members[guid], nil
}

func (f *fakeData) UserPeerGrants(context.Context, int64) ([]string, error) {
	if f.failing == "grants" {
		return nil, errFake
	}
	return f.grants, nil
}

func (f *fakeData) setting(context.Context, string) (string, error) {
	if f.failing == "setting" {
		return "", errFake
	}
	return f.settingValue, nil
}

// fakeRoles answers role permissions from a map and counts the calls. A
// role that is not in the map fails.
type fakeRoles struct {
	permissions map[string][]string
	calls       int
	gate        chan struct{}
}

func (f *fakeRoles) RolePermissions(_ context.Context, role string) ([]string, error) {
	f.calls++
	if f.gate != nil {
		<-f.gate
	}
	permissions, ok := f.permissions[role]
	if !ok {
		return nil, &RoleError{Status: 400, Message: "Invalid role"}
	}
	return permissions, nil
}

// fakeService is a Service on fakes instead of a database.
func fakeService(data *fakeData, roles RoleSource) *Service {
	s := &Service{roles: newRoleCache(roles, time.Now), now: time.Now}
	s.data = data
	return s
}

func TestOraclePolicyMerge(t *testing.T) {
	type policyRows struct {
		Sections []SectionRow `json:"sections"`
		Features []FeatureRow `json:"features"`
	}
	var v struct {
		Cases []struct {
			Role            *string  `json:"role"`
			RolePermissions []string `json:"rolePermissions"`
			UserID          int64    `json:"userId"`
			Groups          []struct {
				ID   int64  `json:"id"`
				GUID string `json:"guid"`
				Name string `json:"name"`
			} `json:"groups"`
			Policies map[string]policyRows `json:"policies"`
			Expected struct {
				User   jsonOrHash   `json:"user"`
				Groups []jsonOrHash `json:"groups"`
			} `json:"expected"`
		} `json:"cases"`
		Edges []struct {
			Fn     string `json:"fn"`
			Error  string `json:"error"`
			Status int    `json:"status"`
		} `json:"edges"`
	}
	loadVectors(t, "policy_merge", &v)
	if len(v.Cases) < 600 {
		t.Fatalf("only %d cases", len(v.Cases))
	}
	ctx := context.Background()
	failed := 0
	for i, c := range v.Cases {
		data := &fakeData{sections: map[string][]SectionRow{}, features: map[string][]FeatureRow{}}
		for key, rows := range c.Policies {
			data.sections[key], data.features[key] = rows.Sections, rows.Features
		}
		for _, g := range c.Groups {
			data.userGroups = append(data.userGroups, UserGroup{ID: g.ID, GUID: g.GUID, Name: g.Name})
		}
		role, lookup := "", "viewer"
		if c.Role != nil {
			role, lookup = *c.Role, *c.Role
		}
		roles := &fakeRoles{permissions: map[string][]string{}}
		if c.RolePermissions != nil {
			roles.permissions[lookup] = c.RolePermissions
		}
		s := fakeService(data, roles)

		policy, err := s.ResolveForUser(ctx, PolicyUser{ID: c.UserID, Role: role})
		if c.Expected.User.Error {
			if err == nil {
				t.Fatalf("case %d: Node fails for role %q, Go resolved a policy", i, role)
			}
			failed++
		} else {
			if err != nil {
				t.Fatalf("case %d: %v", i, err)
			}
			c.Expected.User.check(t, "case "+jsJSON(t, i)+" user policy", jsJSON(t, policy))
		}
		for j, g := range c.Groups {
			group, err := s.ResolveForGroup(ctx, g.GUID)
			if err != nil {
				t.Fatalf("case %d group %s: %v", i, g.GUID, err)
			}
			c.Expected.Groups[j].check(t, "case "+jsJSON(t, i)+" group "+g.GUID, jsJSON(t, group))
		}
	}
	if failed == 0 {
		t.Fatal("no case with a failed role lookup ran")
	}
	for _, e := range v.Edges {
		s := fakeService(&fakeData{}, &fakeRoles{permissions: map[string][]string{"viewer": {}}})
		var err error
		if e.Fn == "resolveForUser" {
			_, err = s.ResolveForUser(ctx, PolicyUser{Role: "viewer"})
		} else {
			_, err = s.ResolveForGroup(ctx, "")
		}
		var se *StatusError
		if !errors.As(err, &se) || se.Status != e.Status || se.Message != e.Error {
			t.Errorf("%s without a subject: %v, Node answers %d %q", e.Fn, err, e.Status, e.Error)
		}
	}
}

func TestOracleRequireGuards(t *testing.T) {
	var v struct {
		Guards []struct {
			Name string  `json:"name"`
			Arg  *string `json:"arg"`
		} `json:"guards"`
		Sessions []*oracleSession `json:"sessions"`
		URLs     []struct {
			OriginalURL string `json:"originalUrl"`
			URL         string `json:"url"`
			Query       struct {
				Return any `json:"return"`
			} `json:"query"`
		} `json:"urls"`
		LoadStates []struct {
			Loaded    bool     `json:"permissionContextLoaded"`
			LoadError bool     `json:"permissionLoadError"`
			Effective []string `json:"effective"`
		} `json:"loadStates"`
		Contexts []struct {
			Section string `json:"section"`
			Level   Level  `json:"level"`
		} `json:"contexts"`
		Outcomes []nodeOutcome `json:"outcomes"`
		Results  []int         `json:"results"`
	}
	loadVectors(t, "require_guards", &v)
	i := 0
	for _, guard := range v.Guards {
		arg := ""
		if guard.Arg != nil {
			arg = *guard.Arg
		}
		for si, session := range v.Sessions {
			for _, u := range v.URLs {
				originalURL := u.OriginalURL
				if originalURL == "" {
					originalURL = u.URL
				}
				returnQuery, _ := u.Query.Return.(string)
				for _, load := range v.LoadStates {
					for _, section := range v.Contexts {
						c := session.context()
						c.Loaded = load.Loaded
						if load.LoadError {
							c.PermissionErr = errors.New("boom")
						}
						if load.Effective != nil {
							c.Permissions = newPermissionSet(load.Effective)
						}
						if section.Section != "" {
							c.Section = section.Section
							c.Policy = policyWith(section.Section, section.Level)
						}
						var d Decision
						switch guard.Name {
						case "requireAuth":
							d = c.RequireAuth(originalURL)
						case "requireAdmin":
							d = c.RequireAdmin(originalURL)
						case "requireRole":
							d = c.RequireRole(arg, originalURL)
						case "requirePermission":
							d = c.RequirePermission(arg, originalURL)
						case "requireRdClientAuth":
							d = c.RequireRdClientAuth(arg, originalURL, returnQuery)
						default:
							t.Fatalf("unknown guard %s", guard.Name)
						}
						want := v.Outcomes[v.Results[i]]
						i++
						label := func() string {
							return guard.Name + "(" + arg + ") " + originalURL + " session " + jsJSON(t, v.Sessions[si]) +
								" load " + jsJSON(t, load) + " context " + jsJSON(t, section)
						}
						login := guard.Name == "requireAuth" && want.Page.Redirect != nil && !want.Page.Destroyed
						checkDecision(t, label, d, want, login)
					}
				}
			}
		}
	}
	if i != len(v.Results) || i < 50000 {
		t.Fatalf("ran %d of %d vectors", i, len(v.Results))
	}
}

func TestOracleRdClientReturnURL(t *testing.T) {
	var v struct {
		Cases []struct {
			Input  string  `json:"input"`
			Output *string `json:"output"`
			Safe   bool    `json:"safe"`
		} `json:"cases"`
	}
	loadVectors(t, "rdclient", &v)
	if len(v.Cases) < 100 {
		t.Fatalf("only %d cases", len(v.Cases))
	}
	for _, c := range v.Cases {
		got, ok := NormalizeRdClientReturnURL(c.Input)
		switch {
		case c.Output == nil && ok:
			t.Errorf("NormalizeRdClientReturnURL(%q) = %q, Node refuses", c.Input, got)
		case c.Output != nil && (!ok || got != *c.Output):
			t.Errorf("NormalizeRdClientReturnURL(%q) = %q, %v; Node says %q", c.Input, got, ok, *c.Output)
		}
		if IsSafeRdClientReturnURL(c.Input) != c.Safe {
			t.Errorf("IsSafeRdClientReturnURL(%q) = %v, Node says %v", c.Input, !c.Safe, c.Safe)
		}
	}
}

// catalogKeys returns the catalog's section keys without the repeat.
func catalogKeys() []string {
	var keys []string
	seen := map[string]bool{}
	for _, entry := range Catalog {
		if !seen[entry.Key] {
			seen[entry.Key] = true
			keys = append(keys, entry.Key)
		}
	}
	return keys
}

func TestOracleBootstrap(t *testing.T) {
	var v struct {
		Cases []struct {
			Role      string           `json:"role"`
			Variant   string           `json:"variant"`
			Effective []string         `json:"effective"`
			Policy    map[string]Level `json:"policy"`
			Expected  struct {
				Permissions  []string   `json:"permissions"`
				IsSuperAdmin bool       `json:"isSuperAdmin"`
				Access       jsonOrHash `json:"access"`
				Nav          jsonOrHash `json:"nav"`
			} `json:"expected"`
		} `json:"cases"`
		Nav []struct {
			Role             string   `json:"role"`
			CurrentPage      string   `json:"currentPage"`
			CurrentTab       string   `json:"currentTab"`
			ActivateAgentNav bool     `json:"activateAgentNav"`
			Key              string   `json:"key"`
			ActiveSection    *string  `json:"activeSection"`
			ActivePage       *string  `json:"activePage"`
			Pages            []string `json:"pages"`
			Texts            string   `json:"texts"`
		} `json:"nav"`
		NoRole        []string `json:"noRole"`
		NoCan         []string `json:"noCan"`
		UIPermissions []string `json:"uiPermissions"`
	}
	loadVectors(t, "bootstrap", &v)
	if !reflect.DeepEqual(v.UIPermissions, UIPermissions) {
		t.Fatalf("UIPermissions differ from Node's UI_PERMISSIONS:\n got  %v\n want %v", UIPermissions, v.UIPermissions)
	}
	if len(v.Cases) < 40 {
		t.Fatalf("only %d cases", len(v.Cases))
	}
	for _, c := range v.Cases {
		label := c.Role + " / " + c.Variant
		ctx := &Context{UserID: 3, HasUser: true, Role: c.Role, Loaded: c.Variant != "static map"}
		if c.Effective != nil {
			ctx.Permissions = newPermissionSet(c.Effective)
		}
		if c.Variant == "load error" {
			ctx.PermissionErr = errors.New("boom")
		}
		if c.Policy != nil {
			ctx.Policy = &Policy{SubjectType: SubjectUser, SubjectID: "3"}
			for _, key := range catalogKeys() {
				ctx.Policy.Sections.Set(key, SectionAccess{Access: c.Policy[key], Source: SourceRole, RoleCeiling: LevelNone})
			}
		}
		if got := ctx.GrantedUIPermissions(); !reflect.DeepEqual(got, c.Expected.Permissions) {
			t.Fatalf("%s: permissions\n got  %v\n want %v", label, got, c.Expected.Permissions)
		}
		if IsSuperAdminRole(c.Role) != c.Expected.IsSuperAdmin {
			t.Fatalf("%s: isSuperAdmin", label)
		}
		c.Expected.Access.check(t, label+": access", jsJSON(t, AccessFor(ctx.Policy)))
		nav := BuildNav(NavInput{
			Can: ctx.HasPermission, IsSuperAdmin: IsSuperAdminRole(c.Role), Role: c.Role,
			Allows: PageAllowed(ctx.Policy),
		})
		c.Expected.Nav.check(t, label+": nav", jsJSON(t, nav.Bootstrap()))
	}

	for _, c := range v.Nav {
		label := jsJSON(t, []any{c.Role, c.CurrentPage, c.CurrentTab, c.ActivateAgentNav})
		if got := NavPageKey(c.CurrentPage, c.CurrentTab, c.ActivateAgentNav); got != c.Key {
			t.Fatalf("NavPageKey%s = %q, Node says %q", label, got, c.Key)
		}
		nav := BuildNav(NavInput{
			Can:          func(p string) bool { return RoleHasPermission(c.Role, p) },
			IsSuperAdmin: IsSuperAdminRole(c.Role), Role: c.Role,
			T:           func(key string) string { return "T(" + key + ")" },
			CurrentPage: c.CurrentPage, CurrentTab: c.CurrentTab, ActivateAgentNav: c.ActivateAgentNav,
		})
		var pages []string
		texts := [][]any{}
		for _, section := range nav.Sections {
			var names []string
			pageTexts := [][]string{}
			for _, page := range section.Pages {
				name := page.Page
				if page.Active {
					name += "*"
				}
				names = append(names, name)
				pageTexts = append(pageTexts, []string{page.Text, page.GroupText})
			}
			pages = append(pages, section.ID+":"+strings.Join(names, ","))
			texts = append(texts, []any{section.Text, pageTexts})
		}
		if !reflect.DeepEqual(pages, c.Pages) {
			t.Fatalf("BuildNav%s pages\n got  %v\n want %v", label, pages, c.Pages)
		}
		jsonOrHash{SHA256: c.Texts}.check(t, "BuildNav"+label+" texts", jsJSON(t, texts))
		gotSection, gotPage := "<none>", "<none>"
		if nav.ActiveSection != nil {
			gotSection = nav.ActiveSection.ID
		}
		if nav.ActivePage != nil {
			gotPage = nav.ActivePage.Page
		}
		if gotSection != deref(c.ActiveSection) || gotPage != deref(c.ActivePage) {
			t.Fatalf("BuildNav%s active %s / %s, Node says %s / %s", label, gotSection, gotPage, deref(c.ActiveSection), deref(c.ActivePage))
		}
	}

	pagesOf := func(nav Nav) []string {
		var out []string
		for _, section := range nav.Sections {
			for _, page := range section.Pages {
				out = append(out, page.Page)
			}
		}
		return out
	}
	if got := pagesOf(BuildNav(NavInput{Can: func(string) bool { return true }})); !reflect.DeepEqual(got, v.NoRole) {
		t.Errorf("navigation without a role\n got  %v\n want %v", got, v.NoRole)
	}
	if got := pagesOf(BuildNav(NavInput{IsSuperAdmin: true, Role: "admin"})); !reflect.DeepEqual(got, v.NoCan) {
		t.Errorf("navigation without a permission check\n got  %v\n want %v", got, v.NoCan)
	}
}

// oracleDevice is a device as the Node vectors describe it.
type oracleDevice struct {
	ID       string `json:"id"`
	FolderID *int64 `json:"folder_id"`
	Tags     any    `json:"tags"`
}

func toDevices(in []oracleDevice) []Device {
	out := make([]Device, len(in))
	for i, d := range in {
		out[i] = Device{ID: d.ID, FolderID: d.FolderID, Tags: NormalizeTags(d.Tags)}
	}
	return out
}

// oracleUser is an account as the Node vectors describe it. UserGroups is
// nil when the vector has no user_groups.
type oracleUser struct {
	ID         int64    `json:"id"`
	Username   string   `json:"username"`
	Role       string   `json:"role"`
	UserGroups []string `json:"user_groups"`
}

func (u *oracleUser) scopeUser() *ScopeUser {
	if u == nil {
		return nil
	}
	return &ScopeUser{ID: u.ID, Username: u.Username, Role: u.Role, UserGroups: u.UserGroups}
}

type oracleDeviceGroup struct {
	GUID          string   `json:"guid"`
	Name          string   `json:"name"`
	SourceType    string   `json:"source_type"`
	TagFilter     string   `json:"tag_filter"`
	AllowedUsers  []string `json:"allowed_users"`
	AllowedGroups []string `json:"allowed_groups"`
}

func TestOracleScope(t *testing.T) {
	type anyCase struct {
		Input  any             `json:"input"`
		Output json.RawMessage `json:"output"`
	}
	var v struct {
		Tags        []anyCase `json:"tags"`
		Usernames   []anyCase `json:"usernames"`
		GUIDs       []anyCase `json:"guids"`
		FolderGUIDs []struct {
			Input  *string  `json:"input"`
			Output *float64 `json:"output"`
		} `json:"folderGuids"`
		FolderIDs []struct {
			Input  any       `json:"input"`
			Output []float64 `json:"output"`
		} `json:"folderIds"`
		PeerIDs []anyCase `json:"peerIds"`
		Allowed []struct {
			Group   oracleDeviceGroup `json:"group"`
			User    *oracleUser       `json:"user"`
			Allowed bool              `json:"allowed"`
		} `json:"allowed"`
		Scopes []struct {
			Devices       []oracleDevice      `json:"devices"`
			Groups        []oracleDeviceGroup `json:"groups"`
			Members       map[string][]string `json:"members"`
			Grants        []string            `json:"grants"`
			User          *oracleUser         `json:"user"`
			UserGroupRows []struct {
				GUID string `json:"guid"`
			} `json:"userGroupRows"`
			Setting  *string       `json:"setting"`
			Env      string        `json:"env"`
			Failing  *string       `json:"failing"`
			Target   *oracleDevice `json:"target"`
			Expected struct {
				Scope             []string   `json:"scope"`
				CanAccessAlone    bool       `json:"canAccessAlone"`
				CanAccessAmongAll bool       `json:"canAccessAmongAll"`
				Count             ScopeCount `json:"count"`
				Filtered          []string   `json:"filtered"`
				MemberCounts      []int      `json:"memberCounts"`
			} `json:"expected"`
		} `json:"scopes"`
	}
	loadVectors(t, "scope", &v)

	for _, c := range v.Tags {
		if got := jsJSON(t, NormalizeTags(c.Input)); got != string(c.Output) {
			t.Errorf("NormalizeTags(%s) = %s, Node says %s", jsJSON(t, c.Input), got, c.Output)
		}
	}
	for _, c := range v.Usernames {
		if got := jsJSON(t, NormalizeUsernames(c.Input)); got != string(c.Output) {
			t.Errorf("NormalizeUsernames(%s) = %s, Node says %s", jsJSON(t, c.Input), got, c.Output)
		}
	}
	for _, c := range v.GUIDs {
		if got := jsJSON(t, NormalizeGroupGUIDs(c.Input)); got != string(c.Output) {
			t.Errorf("NormalizeGroupGUIDs(%s) = %s, Node says %s", jsJSON(t, c.Input), got, c.Output)
		}
	}
	for _, c := range v.FolderGUIDs {
		input := ""
		if c.Input != nil {
			input = *c.Input
		}
		got, ok := folderIDFromGroupGUID(input)
		if ok != (c.Output != nil) || (ok && got != *c.Output) {
			t.Errorf("folderIDFromGroupGUID(%q) = %v, %v; Node says %v", input, got, ok, c.Output)
		}
	}
	for _, c := range v.FolderIDs {
		// Ids beyond the range of a stored id are dropped on the Go side.
		want := []int64{}
		for _, f := range c.Output {
			if f >= -(1<<63) && f < 1<<63 {
				want = append(want, int64(f))
			}
		}
		if got := NormalizeFolderIDs(c.Input); !reflect.DeepEqual(got, want) {
			t.Errorf("NormalizeFolderIDs(%s) = %v, Node says %v", jsJSON(t, c.Input), got, c.Output)
		}
	}
	for _, c := range v.PeerIDs {
		if got := jsJSON(t, NormalizePeerIDs(c.Input)); got != string(c.Output) {
			t.Errorf("NormalizePeerIDs(%s) = %s, Node says %s", jsJSON(t, c.Input), got, c.Output)
		}
	}
	for _, c := range v.Allowed {
		group := DeviceGroup{AllowedUsers: c.Group.AllowedUsers, AllowedGroups: c.Group.AllowedGroups}
		if got := GroupAllowedForUser(group, c.User.scopeUser()); got != c.Allowed {
			t.Errorf("GroupAllowedForUser(%s, %s) = %v, Node says %v", jsJSON(t, c.Group), jsJSON(t, c.User), got, c.Allowed)
		}
	}

	if len(v.Scopes) < 1000 {
		t.Fatalf("only %d scope cases", len(v.Scopes))
	}
	ctx := context.Background()
	limited := 0
	for i, c := range v.Scopes {
		data := &fakeData{members: c.Members, grants: c.Grants}
		if c.Setting != nil {
			data.settingValue = *c.Setting
		}
		if c.Failing != nil {
			data.failing = *c.Failing
		}
		for _, g := range c.Groups {
			data.deviceGroups = append(data.deviceGroups, DeviceGroup{
				GUID: g.GUID, Name: g.Name, SourceType: g.SourceType, TagFilter: g.TagFilter,
				AllowedUsers: g.AllowedUsers, AllowedGroups: g.AllowedGroups,
			})
		}
		for _, g := range c.UserGroupRows {
			data.userGroups = append(data.userGroups, UserGroup{GUID: g.GUID})
		}
		// A fresh service per call: the default mode is not carried over.
		service := func() *Service {
			s := fakeService(data, &fakeRoles{})
			s.scopeEnvRestricted = c.Env == "restricted"
			return s
		}
		devices := toDevices(c.Devices)
		user := c.User.scopeUser()
		label := "scope case " + jsJSON(t, i)

		scope, err := service().DeviceScopeForUser(ctx, user, devices)
		if err != nil {
			t.Fatalf("%s: %v", label, err)
		}
		if (scope == nil) != (c.Expected.Scope == nil) {
			t.Fatalf("%s: unlimited %v, Node says %v\n %s", label, scope == nil, c.Expected.Scope == nil, jsJSON(t, c))
		}
		if scope != nil {
			limited++
			if got := scope.IDs(); !reflect.DeepEqual(got, c.Expected.Scope) {
				t.Fatalf("%s: scope %v, Node says %v\n %s", label, got, c.Expected.Scope, jsJSON(t, c))
			}
		}
		filtered := []string{}
		for _, d := range FilterByScope(devices, scope, func(d Device) string { return d.ID }) {
			filtered = append(filtered, d.ID)
		}
		if !reflect.DeepEqual(filtered, c.Expected.Filtered) {
			t.Fatalf("%s: filtered %v, Node says %v", label, filtered, c.Expected.Filtered)
		}

		var target *Device
		if c.Target != nil {
			target = &toDevices([]oracleDevice{*c.Target})[0]
		}
		if got, err := service().UserCanAccessDevice(ctx, user, target, nil); err != nil || got != c.Expected.CanAccessAlone {
			t.Fatalf("%s: UserCanAccessDevice alone = %v, %v; Node says %v\n %s", label, got, err, c.Expected.CanAccessAlone, jsJSON(t, c))
		}
		if got, err := service().UserCanAccessDevice(ctx, user, target, devices); err != nil || got != c.Expected.CanAccessAmongAll {
			t.Fatalf("%s: UserCanAccessDevice among all = %v, %v; Node says %v\n %s", label, got, err, c.Expected.CanAccessAmongAll, jsJSON(t, c))
		}
		if got, err := service().CountEffectiveScope(ctx, user, devices); err != nil || got != c.Expected.Count {
			t.Fatalf("%s: CountEffectiveScope = %+v, %v; Node says %+v", label, got, err, c.Expected.Count)
		}
		counts := []int{}
		for _, g := range service().EnrichGroups(ctx, data.deviceGroups, devices) {
			counts = append(counts, *g.MemberCount)
		}
		if !reflect.DeepEqual(counts, c.Expected.MemberCounts) {
			t.Fatalf("%s: member counts %v, Node says %v", label, counts, c.Expected.MemberCounts)
		}
	}
	if limited < 200 {
		t.Fatalf("only %d cases with a limited scope", limited)
	}
}

func TestOracleRoleService(t *testing.T) {
	var v struct {
		Steps []struct {
			Label string          `json:"label"`
			Value json.RawMessage `json:"value"`
			Error bool            `json:"error"`
			Calls int             `json:"calls"`
		} `json:"steps"`
		Loads []struct {
			Session   *oracleSession `json:"session"`
			Loaded    bool           `json:"loaded"`
			Effective []string       `json:"effective"`
			Error     bool           `json:"error"`
		} `json:"loads"`
	}
	loadVectors(t, "role_service", &v)
	ctx := context.Background()
	roles := &fakeRoles{permissions: map[string][]string{
		"operator": {"device.view", "user.view"},
		"viewer":   {"device.view"},
	}}
	s := fakeService(&fakeData{}, roles)
	sorted := func(set PermissionSet) []string {
		out := []string{}
		for p := range set {
			out = append(out, p)
		}
		sort.Strings(out)
		return out
	}
	lookup := func(role string) func() (any, error) {
		return func() (any, error) {
			set, err := s.EffectivePermissions(ctx, role)
			if err != nil {
				return nil, err
			}
			return sorted(set), nil
		}
	}
	actions := map[string]func() (any, error){
		"first lookup":            lookup("operator"),
		"second lookup is cached": lookup("operator"),
		"after invalidate": func() (any, error) {
			s.InvalidateRole("operator")
			roles.permissions["operator"] = []string{}
			return lookup("operator")()
		},
		"no role":                 lookup(""),
		"unknown role fails":      lookup("nobody"),
		"a failure is not cached": lookup("nobody"),
		"two at once share one call": func() (any, error) {
			s.ClearRoleCache()
			roles.gate = make(chan struct{})
			defer func() { roles.gate = nil }()
			type result struct {
				set PermissionSet
				err error
			}
			results := make(chan result, 2)
			for range 2 {
				go func() {
					set, err := s.EffectivePermissions(ctx, "viewer")
					results <- result{set, err}
				}()
			}
			// Both callers are under way before the source answers.
			time.Sleep(50 * time.Millisecond)
			close(roles.gate)
			a, b := <-results, <-results
			if err := errors.Join(a.err, b.err); err != nil {
				return nil, err
			}
			return [][]string{sorted(a.set), sorted(b.set)}, nil
		},
	}
	ran := 0
	for _, step := range v.Steps {
		action, ok := actions[step.Label]
		if !ok {
			var role, permission string
			if n, _ := sscan(step.Label, "roleHasEffectivePermission", &role, &permission); n != 2 {
				t.Fatalf("unknown step %q", step.Label)
			}
			action = func() (any, error) {
				s.ClearRoleCache()
				return s.RoleHasEffectivePermission(ctx, role, permission)
			}
		}
		before := roles.calls
		value, err := action()
		calls := roles.calls - before
		if (err != nil) != step.Error {
			t.Fatalf("%s: error %v, Node failed: %v", step.Label, err, step.Error)
		}
		if err == nil {
			if got := jsJSON(t, value); got != string(step.Value) {
				t.Fatalf("%s: %s, Node says %s", step.Label, got, step.Value)
			}
		}
		if calls != step.Calls {
			t.Fatalf("%s: %d lookups, Node made %d", step.Label, calls, step.Calls)
		}
		ran++
	}
	if ran < 10 {
		t.Fatalf("only %d steps", ran)
	}

	roles.permissions["operator"] = []string{"device.view", "user.view"}
	for _, load := range v.Loads {
		s.ClearRoleCache()
		session := Session{}
		if load.Session != nil {
			session.UserID = load.Session.UserID
			if load.Session.User != nil {
				session.HasUser, session.Role = true, load.Session.User.Role
			}
		}
		c := s.Load(ctx, session)
		label := "Load " + jsJSON(t, load.Session)
		if c.Loaded != load.Loaded || (c.PermissionErr != nil) != load.Error {
			t.Fatalf("%s: loaded %v error %v, Node: loaded %v error %v", label, c.Loaded, c.PermissionErr, load.Loaded, load.Error)
		}
		if (c.Permissions == nil) != (load.Effective == nil) {
			t.Fatalf("%s: permissions %v, Node: %v", label, c.Permissions, load.Effective)
		}
		if c.Permissions != nil && !reflect.DeepEqual(sorted(c.Permissions), load.Effective) {
			t.Fatalf("%s: permissions %v, Node: %v", label, sorted(c.Permissions), load.Effective)
		}
	}
}

// sscan splits "prefix a b" into its two words after the prefix.
func sscan(label, prefix string, a, b *string) (int, error) {
	fields := strings.Fields(strings.TrimPrefix(label, prefix))
	if !strings.HasPrefix(label, prefix) || len(fields) != 2 {
		return 0, errors.New("no match")
	}
	*a, *b = fields[0], fields[1]
	return 2, nil
}
