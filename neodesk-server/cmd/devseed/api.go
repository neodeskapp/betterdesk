package main

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
)

// The parts that go through the console's API: users, folders, groups,
// tags, organizations, policies, billing packages, contracts and saved
// reports are created the normal way. The requests carry the headers the
// console has always seen from the seed (a Node fetch), because the
// console keeps the user agent of the seed's session and puts it into
// the audit log.

// cookie is a cookie the console set.
type cookie struct{ name, value string }

// consoleClient talks to the console as a signed-in admin.
type consoleClient struct {
	base string
	hc   *http.Client
	// cookies keep the order they were first set in.
	cookies []cookie
	csrf    string
	out     io.Writer
	warn    io.Writer
	sleep   func(ctx context.Context, d time.Duration) error
}

func newConsoleClient(base string, out, warn io.Writer) *consoleClient {
	return &consoleClient{
		base: base,
		hc:   &http.Client{Timeout: 5 * time.Minute},
		out:  out,
		warn: warn,
		sleep: func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-t.C:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
}

func (c *consoleClient) cookieHeader() string {
	parts := make([]string, len(c.cookies))
	for i, k := range c.cookies {
		parts[i] = k.name + "=" + k.value
	}
	return strings.Join(parts, "; ")
}

func (c *consoleClient) setCookie(name, value string) {
	for i := range c.cookies {
		if c.cookies[i].name == name {
			c.cookies[i].value = value
			return
		}
	}
	c.cookies = append(c.cookies, cookie{name, value})
}

// keepCookies takes the name and value of every cookie an answer sets.
func (c *consoleClient) keepCookies(header http.Header) {
	for _, line := range header.Values("Set-Cookie") {
		pair, _, _ := strings.Cut(line, ";")
		eq := strings.Index(pair, "=")
		if eq < 0 {
			// No "=": the name is all but the last character, the value all
			// of it, as slicing at -1 gave.
			name := ""
			if pair != "" {
				name = pair[:len(pair)-1]
			}
			c.setCookie(strings.TrimSpace(name), strings.TrimSpace(pair))
			continue
		}
		c.setCookie(strings.TrimSpace(pair[:eq]), strings.TrimSpace(pair[eq+1:]))
	}
}

// answer is a response with its body read.
type answer struct {
	status int
	header http.Header
	body   []byte
}

func (a answer) ok() bool { return a.status >= 200 && a.status <= 299 }

// json is the body read as JSON, or nil when it is not JSON.
func (a answer) json() any {
	var v any
	if err := json.Unmarshal(a.body, &v); err != nil {
		return nil
	}
	return v
}

// fetch sends one request with the headers a Node fetch adds: accept
// (unless headers name one), accept-language, sec-fetch-mode, user-agent
// and accept-encoding.
func (c *consoleClient) fetch(ctx context.Context, method, path string, headers [][2]string, body []byte) (answer, error) {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return answer{}, fmt.Errorf("%s %s: %w", method, path, err)
	}
	for _, h := range headers {
		req.Header.Set(h[0], h[1])
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "*/*")
	}
	req.Header.Set("Accept-Language", "*")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("User-Agent", "node")
	req.Header.Set("Accept-Encoding", "gzip, deflate")
	res, err := c.hc.Do(req)
	if err != nil {
		return answer{}, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer res.Body.Close()
	data, err := decodeBody(res)
	if err != nil {
		return answer{}, fmt.Errorf("%s %s: read the answer: %w", method, path, err)
	}
	return answer{status: res.StatusCode, header: res.Header, body: data}, nil
}

// decodeBody reads a body in the encoding the console chose.
func decodeBody(res *http.Response) ([]byte, error) {
	var r io.Reader = res.Body
	switch strings.ToLower(strings.TrimSpace(res.Header.Get("Content-Encoding"))) {
	case "", "identity":
	case "gzip", "x-gzip":
		gz, err := gzip.NewReader(res.Body)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		r = gz
	case "deflate":
		raw, err := io.ReadAll(res.Body)
		if err != nil {
			return nil, err
		}
		if z, err := zlib.NewReader(bytes.NewReader(raw)); err == nil {
			defer z.Close()
			return io.ReadAll(z)
		}
		return io.ReadAll(flate.NewReader(bytes.NewReader(raw)))
	default:
		return nil, fmt.Errorf("content encoding %q", res.Header.Get("Content-Encoding"))
	}
	return io.ReadAll(r)
}

// refreshCSRF takes the CSRF token from JSON: the bootstrap once signed
// in, the sign-in page's data before.
func (c *consoleClient) refreshCSRF(ctx context.Context) error {
	c.csrf = ""
	for _, path := range []string{"/api/console/bootstrap", "/api/auth/login-page"} {
		res, err := c.fetch(ctx, http.MethodGet, path, [][2]string{{"Cookie", c.cookieHeader()}, {"Accept", "application/json"}}, nil)
		if err != nil {
			return err
		}
		c.keepCookies(res.header)
		token := prop(res.json(), "csrfToken")
		if res.ok() && truthy(token) {
			c.csrf = toString(token)
			return nil
		}
	}
	return nil
}

// api sends a request to the console API. A 429 is waited out (the API
// allows about 100 requests a minute per address) up to eight times. An
// answer that is not a 2xx is reported unless its status is in quiet. The
// result is the JSON answer, or an empty object when there is none.
func (c *consoleClient) api(ctx context.Context, method, path string, body any, quiet ...int) (any, error) {
	var payload []byte
	if body != nil {
		s, err := stringify(body)
		if err != nil {
			return nil, fmt.Errorf("%s %s: %w", method, path, err)
		}
		payload = s
	}
	var res answer
	for attempt := 0; ; attempt++ {
		var err error
		res, err = c.fetch(ctx, method, path, [][2]string{
			{"Content-Type", "application/json"}, {"X-Csrf-Token", c.csrf}, {"Cookie", c.cookieHeader()},
		}, payload)
		if err != nil {
			return nil, err
		}
		c.keepCookies(res.header)
		if res.status != http.StatusTooManyRequests || attempt >= 8 {
			break
		}
		wait := toNumber(res.header.Get("Ratelimit-Reset"))
		if !truthy(wait) {
			wait = 15
		}
		fmt.Fprintf(c.out, "  rate limited, waiting %ss\n", numberString(wait))
		if err := c.sleep(ctx, time.Duration((wait+1)*1000)*time.Millisecond); err != nil {
			return nil, err
		}
	}
	data := res.json()
	if !res.ok() && !slices.Contains(quiet, res.status) {
		msg := ""
		if data != nil {
			if m := or(prop(data, "error"), prop(data, "message")); truthy(m) {
				msg = toString(m)
			}
		}
		fmt.Fprintf(c.warn, "  %s %s -> %d %s\n", method, path, res.status, msg)
	}
	if !truthy(data) {
		return map[string]any{}, nil
	}
	return data, nil
}

// signIn signs in as the admin, or takes the session of a Playwright
// storage state file.
func (c *consoleClient) signIn(ctx context.Context, cookieFile, user, password string) error {
	if cookieFile != "" {
		if err := c.loadCookieFile(cookieFile); err != nil {
			return err
		}
		res, err := c.fetch(ctx, http.MethodGet, "/api/console/bootstrap", [][2]string{{"Cookie", c.cookieHeader()}, {"Accept", "application/json"}}, nil)
		if err != nil {
			return err
		}
		if res.status != http.StatusOK {
			return fmt.Errorf("session in %s is not valid (GET /api/console/bootstrap -> %d). Refresh it and try again", cookieFile, res.status)
		}
		return c.refreshCSRF(ctx)
	}
	if err := c.refreshCSRF(ctx); err != nil {
		return err
	}
	login, err := c.api(ctx, http.MethodPost, "/api/auth/login", object{{"username", user}, {"password", password}})
	if err != nil {
		return err
	}
	if !truthy(prop(login, "success")) {
		return errors.New("admin login failed")
	}
	return c.refreshCSRF(ctx)
}

func (c *consoleClient) loadCookieFile(file string) error {
	data, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("read the cookie file: %w", err)
	}
	var state struct {
		Cookies []struct {
			Name  any `json:"name"`
			Value any `json:"value"`
		} `json:"cookies"`
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("read the cookie file %s: %w", file, err)
	}
	for _, k := range state.Cookies {
		c.setCookie(toString(k.Name), toString(k.Value))
	}
	return nil
}

var emptyObject = map[string]any{}

// get is m[key], undefined when the key is not there.
func get(m map[string]any, key string) any {
	if v, ok := m[key]; ok {
		return v
	}
	return undefined
}

// and is a && b.
func and(a, b any) any {
	if truthy(a) {
		return b
	}
	return a
}

// listIn is (v.key || []) as a list.
func listIn(v any, key string) []any { return list(or(prop(v, key), []any{})) }

func (d *demo) seedViaAPI(ctx context.Context, c *consoleClient) error {
	// Account creation has its own limit (three per five minutes), so only
	// create the ones that are missing.
	res, err := c.api(ctx, http.MethodGet, "/api/users", nil)
	if err != nil {
		return err
	}
	var have []any
	for _, u := range listIn(or(prop(res, "data"), emptyObject), "users") {
		have = append(have, prop(u, "username"))
	}
	for _, u := range demoUsers {
		if some(have, func(x any) bool { return strictEqual(x, u.Username) }) {
			continue
		}
		body := object{{"username", u.Username}, {"role", u.Role}, {"email", u.Email}, {"password", "Demo-Password-" + u.Username + "-42!"}}
		if _, err := c.api(ctx, http.MethodPost, "/api/users", body, 400, 409); err != nil {
			return err
		}
	}

	res, err = c.api(ctx, http.MethodGet, "/api/folders", nil)
	if err != nil {
		return err
	}
	existing := listIn(or(prop(res, "data"), emptyObject), "folders")
	folderIDs := map[string]any{}
	for _, f := range folders {
		found := find(existing, func(x any) bool { return strictEqual(prop(x, "name"), f.Name) })
		if truthy(found) {
			folderIDs[f.Name] = prop(found, "id")
			continue
		}
		r, err := c.api(ctx, http.MethodPost, "/api/folders", object{{"name", f.Name}, {"color", f.Color}})
		if err != nil {
			return err
		}
		folderIDs[f.Name] = and(prop(r, "data"), prop(prop(r, "data"), "id"))
	}

	for _, dv := range devices {
		if dv.Deleted {
			continue
		}
		if dv.Tags != "" {
			if _, err := c.api(ctx, http.MethodPut, "/api/devices/"+dv.ID+"/tags", object{{"tags", anys(strings.Split(dv.Tags, ","))}}); err != nil {
				return err
			}
		}
		if dv.Folder != "" && truthy(get(folderIDs, dv.Folder)) {
			if _, err := c.api(ctx, http.MethodPatch, "/api/devices/"+dv.ID+"/folder", object{{"folderId", folderIDs[dv.Folder]}}); err != nil {
				return err
			}
		}
	}

	res, err = c.api(ctx, http.MethodGet, "/api/device-groups", nil)
	if err != nil {
		return err
	}
	groups := listIn(or(prop(res, "data"), emptyObject), "groups")
	for _, g := range deviceGroups {
		group := find(groups, func(x any) bool { return strictEqual(prop(x, "name"), g.Name) })
		if !truthy(group) {
			var tagFilter any = undefined
			if g.TagFilter != "" {
				tagFilter = g.TagFilter
			}
			r, err := c.api(ctx, http.MethodPost, "/api/device-groups", object{{"name", g.Name}, {"source_type", g.SourceType}, {"tag_filter", tagFilter}})
			if err != nil {
				return err
			}
			group = or(prop(r, "data"), prop(r, "group"))
		}
		if truthy(group) && g.Members != nil {
			for _, id := range g.Members {
				if _, err := c.api(ctx, http.MethodPut, "/api/devices/"+id+"/groups", object{{"groupGuids", []any{prop(group, "guid")}}}); err != nil {
					return err
				}
			}
		}
	}
	fmt.Fprintf(c.out, "API: %d users, %d folders, %d groups, tags set\n", len(demoUsers), len(folders), len(deviceGroups))
	return nil
}

// seedOrganizations creates the organizations with their members, users,
// devices, settings and policies, and puts each organization's devices
// into the device group made with it. The result has each organization's
// id under its key and its group's guid under "<key>:group".
func (d *demo) seedOrganizations(ctx context.Context, c *consoleClient) (map[string]any, error) {
	res, err := c.api(ctx, http.MethodGet, "/api/users", nil)
	if err != nil {
		return nil, err
	}
	userIDs := map[string]any{}
	for _, u := range listIn(or(prop(res, "data"), emptyObject), "users") {
		userIDs[toString(prop(u, "username"))] = prop(u, "id")
	}
	res, err = c.api(ctx, http.MethodGet, "/api/panel/org", nil)
	if err != nil {
		return nil, err
	}
	listed := listIn(res, "organizations")
	orgIDs := map[string]any{}
	for _, o := range organizations {
		org := find(listed, func(x any) bool { return strictEqual(prop(x, "slug"), o.Slug) })
		if !truthy(org) {
			if org, err = c.api(ctx, http.MethodPost, "/api/panel/org", object{{"name", o.Name}, {"slug", o.Slug}}); err != nil {
				return nil, err
			}
		}
		if !truthy(org) || !truthy(prop(org, "id")) {
			fmt.Fprintf(c.warn, "  could not create organization %s\n", o.Slug)
			continue
		}
		orgID := prop(org, "id")
		orgIDs[o.Key] = orgID
		base := "/api/panel/org/" + toString(orgID)

		for _, m := range o.Members {
			if id := get(userIDs, m.Username); truthy(id) {
				if _, err := c.api(ctx, http.MethodPost, base+"/members", object{{"user_id", toNumber(id)}, {"role", m.Role}}, 409); err != nil {
					return nil, err
				}
			}
		}
		for _, u := range o.LocalUsers {
			body := object{{"username", u.Username}, {"display_name", u.DisplayName}, {"email", u.Email}, {"role", u.Role},
				{"password", "Demo-Password-" + u.Username + "-42!"}}
			if _, err := c.api(ctx, http.MethodPost, base+"/users", body, 409); err != nil {
				return nil, err
			}
		}
		orgUsersRes, err := c.api(ctx, http.MethodGet, base+"/users", nil)
		if err != nil {
			return nil, err
		}
		orgUsers, isList := orgUsersRes.([]any)
		if !isList {
			orgUsers = listIn(orgUsersRes, "users")
		}
		orgUserID := func(name string) any {
			u := or(find(orgUsers, func(x any) bool { return strictEqual(prop(x, "username"), name) }), emptyObject)
			return or(prop(u, "id"), "")
		}

		for _, dv := range o.Devices {
			var assigned any = ""
			if dv.Assignee != "" {
				assigned = orgUserID(dv.Assignee)
			}
			if _, err := c.api(ctx, http.MethodPost, base+"/devices", object{
				{"device_id", dv.DeviceID}, {"department", dv.Department}, {"location", dv.Location}, {"building", dv.Building},
				{"assigned_user_id", assigned}, {"tags", d.dev[dv.DeviceID].Tags},
			}); err != nil {
				return nil, err
			}
		}
		for _, s := range o.Settings {
			if _, err := c.api(ctx, http.MethodPut, base+"/settings", object{{"key", s.Key}, {"value", s.Value}}); err != nil {
				return nil, err
			}
		}
		for _, p := range o.Policies {
			if _, err := c.api(ctx, http.MethodPut, "/api/panel/policies/"+toString(orgID)+"/"+p.Category, p.Body); err != nil {
				return nil, err
			}
		}

		// Put the org's devices into the default group created with the org.
		res, err := c.api(ctx, http.MethodGet, base+"/device-groups", nil)
		if err != nil {
			return nil, err
		}
		linked := listIn(res, "device_groups")
		group := find(linked, func(x any) bool { return strictEqual(prop(x, "name"), o.Name) })
		if !truthy(group) && len(linked) > 0 {
			group = linked[0]
		}
		if truthy(group) {
			orgIDs[o.Key+":group"] = prop(group, "guid")
			for _, dv := range o.Devices {
				if _, err := c.api(ctx, http.MethodPut, "/api/devices/"+dv.DeviceID+"/groups", object{{"groupGuids", []any{prop(group, "guid")}}}); err != nil {
					return nil, err
				}
			}
		}
	}
	parts := make([]string, len(organizations))
	for i, o := range organizations {
		parts[i] = o.Slug + "=" + toString(or(get(orgIDs, o.Key), "?"))
	}
	fmt.Fprintf(c.out, "API: organizations %s\n", strings.Join(parts, ", "))
	return orgIDs, nil
}

// seedBilling creates the packages and assigns them. The result is the
// contracts made or found, in the order of the demo data.
func (d *demo) seedBilling(ctx context.Context, c *consoleClient, orgIDs map[string]any) ([]contractTerms, error) {
	res, err := c.api(ctx, http.MethodGet, "/api/panel/billing/packages", nil)
	if err != nil {
		return nil, err
	}
	pkgList := listIn(res, "packages")
	pkgs := map[string]any{}
	var pkgKeys []string
	for _, p := range billingPackages {
		pkg := find(pkgList, func(x any) bool { return strictEqual(prop(x, "name"), p.Name) })
		if !truthy(pkg) {
			if pkg, err = c.api(ctx, http.MethodPost, "/api/panel/billing/packages", object{
				{"name", p.Name}, {"description", p.Description}, {"included_minutes", p.IncludedMinutes},
				{"overage_rate", p.OverageRate}, {"currency", p.Currency}, {"rounding_minutes", p.RoundingMinutes},
			}); err != nil {
				return nil, err
			}
		}
		if _, seen := pkgs[p.Key]; !seen {
			pkgKeys = append(pkgKeys, p.Key)
		}
		pkgs[p.Key] = pkg
	}

	res, err = c.api(ctx, http.MethodGet, "/api/panel/billing/contracts", nil)
	if err != nil {
		return nil, err
	}
	contractList := listIn(res, "contracts")
	var made []contractTerms
	for _, k := range contracts {
		targetKey, otherKey := get(orgIDs, k.Org+":group"), get(orgIDs, k.Org)
		if k.Target == "org" {
			targetKey, otherKey = otherKey, targetKey
		}
		pkg := get(pkgs, k.Package)
		if !truthy(targetKey) || !truthy(pkg) || !truthy(prop(pkg, "id")) {
			continue
		}
		// Drop an assignment from an earlier run that used the other target type.
		for _, stale := range contractList {
			if !strictEqual(prop(stale, "target_type"), k.Target) && strictEqual(prop(stale, "target_key"), otherKey) {
				if _, err := c.api(ctx, http.MethodDelete, "/api/panel/billing/contracts/"+toString(prop(stale, "id")), nil); err != nil {
					return nil, err
				}
			}
		}
		found := find(contractList, func(x any) bool {
			return strictEqual(prop(x, "target_type"), k.Target) && strictEqual(prop(x, "target_key"), targetKey)
		})
		if !truthy(found) {
			if found, err = c.api(ctx, http.MethodPost, "/api/panel/billing/contracts", object{
				{"target_type", k.Target}, {"target_key", targetKey}, {"package_id", prop(pkg, "id")}, {"status", k.Status},
				{"currency", prop(pkg, "currency")}, {"hourly_rate", 0.0},
			}); err != nil {
				return nil, err
			}
		}
		if truthy(found) && truthy(prop(found, "id")) {
			made = setContract(made, contractTerms{Org: k.Org, ID: prop(found, "id"), Pkg: termsOf(pkg)})
		}
	}
	fmt.Fprintf(c.out, "API: %d billing packages, %d contracts\n", len(pkgKeys), len(made))
	return made, nil
}

// setContract records an organization's contract: a second one for the
// same organization takes the first one's place.
func setContract(made []contractTerms, c contractTerms) []contractTerms {
	for i := range made {
		if made[i].Org == c.Org {
			made[i] = c
			return made
		}
	}
	return append(made, c)
}

func (d *demo) seedSavedReports(ctx context.Context, c *consoleClient) error {
	saved, err := c.api(ctx, http.MethodGet, "/api/reports/saved", nil)
	if err != nil {
		return err
	}
	have, isList := saved.([]any)
	if !isList {
		have = list(or(or(prop(saved, "reports"), prop(saved, "data")), []any{}))
	}
	n := 0
	for _, r := range savedReports {
		if some(have, func(x any) bool { return strictEqual(prop(x, "title"), r.Title) }) {
			continue
		}
		if _, err := c.api(ctx, http.MethodPost, "/api/reports/save", object{{"type", r.Type}, {"title", r.Title}}); err != nil {
			return err
		}
		n++
	}
	fmt.Fprintf(c.out, "API: %d saved reports added (%d already there)\n", n, len(savedReports)-n)
	return nil
}
