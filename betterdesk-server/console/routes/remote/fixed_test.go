package remote

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// textDeviceList is the device list of a scripted guest link when it is
// a text.
var textDeviceList = regexp.MustCompile(`"peer_ids":"[^"]*"`)

// answeredAs maps the Node vectors whose answer a bug fix of BUGS.md
// changed on purpose to the vector whose recorded answer they get now.
//
// GA2: a guest link whose list of devices is a text reaches no device.
// The oracle sent every guest request also with a link that lists no
// device; a vector with a text list is held to that vector's answer.
//
// DV4: only text names a device. An agent token request whose body names
// the device with a number, true or false, a list or an object is held to
// the answer of the same request for another device, the device guard's
// refusal.
func answeredAs(t *testing.T, vectors []vector) map[int]int {
	t.Helper()
	const validate = "GET /api/guest/access-links/validate"
	key := func(v vector) string {
		text, err := json.Marshal(struct {
			Request request
			World   scene
		}{v.Request, v.World})
		if err != nil {
			t.Fatal(err)
		}
		return string(text)
	}
	index := make(map[string]int, len(vectors))
	for i, v := range vectors {
		index[key(v)] = i
	}
	twins := map[int]int{}
	for i, v := range vectors {
		link, ok := v.World.API[validate]
		if !ok || !textDeviceList.MatchString(link.Body) {
			continue
		}
		twin := v
		twin.World.API = map[string]apiAnswer{}
		for name, answer := range v.World.API {
			twin.World.API[name] = answer
		}
		link.Body = textDeviceList.ReplaceAllString(link.Body, `"peer_ids":[]`)
		twin.World.API[validate] = link
		j, found := index[key(twin)]
		if !found {
			t.Fatalf("vector %d (%s, link %s) has no twin with a link that lists no device", i, v.Request.Path, v.World.API[validate].Body)
		}
		twins[i] = j
	}
	if len(twins) == 0 {
		t.Fatal("no vector has a guest link whose device list is a text")
	}
	refused := 0
	for i, v := range vectors {
		if !namesNoText(v.Request) {
			continue
		}
		twin := v
		other := `{"device_id":"dev-b"}`
		twin.Request.Body = &other
		twin.Request.Headers = map[string]string{"Authorization": "Bearer tok-device-a"}
		j, found := index[key(twin)]
		if !found {
			t.Fatalf("vector %d (%s %s) has no twin that names another device", i, v.Request.Path, *v.Request.Body)
		}
		twins[i] = j
		refused++
	}
	if refused == 0 {
		t.Fatal("no agent token request names its device with a value that is no text")
	}
	return twins
}

// refusedSessions holds the Node vectors whose answer RM14 changed on
// purpose, with the answer they get now. The list of remote sessions and
// a session's state need device.connect: a request to either route by a
// role the remote desk refused for lacking device.connect gets the desk's
// refusal, and the relay is not asked.
func refusedSessions(t *testing.T, vectors []vector) map[int]vector {
	t.Helper()
	const denied = `{"success":false,"error":"Permission denied: device.connect"}`
	without := map[string]bool{}
	for _, v := range vectors {
		if v.Request.Path == "/api/remote/desk" && v.Request.Session != nil && v.Status == http.StatusForbidden && v.Body == denied {
			without[v.Request.Session.User.Role] = true
		}
	}
	if len(without) == 0 {
		t.Fatal("no role was refused the remote desk for lacking device.connect")
	}
	fixed := map[int]vector{}
	for i, v := range vectors {
		path := v.Request.Path
		if v.Request.Session == nil || !without[v.Request.Session.User.Role] ||
			(path != "/api/remote/sessions" && !strings.HasPrefix(path, "/api/remote/session/")) {
			continue
		}
		v.Status, v.Body, v.RelayCalls = http.StatusForbidden, denied, nil
		fixed[i] = v
	}
	if len(fixed) == 0 {
		t.Fatal("no vector asks for the remote sessions with a role without device.connect")
	}
	return fixed
}

// issues counts the agent tokens Node had the relay issue for a vector.
func issues(v *vector) int {
	n := 0
	for _, call := range v.RelayCalls {
		if call.Call == "issue" {
			n++
		}
	}
	return n
}

// namesNoText reports whether r asks for an agent token and names its
// device in the body with a value that is no text and not null: a number,
// true or false, a list or an object. The device guard refuses such a
// request as another device (DV4).
func namesNoText(r request) bool {
	if r.Method != "POST" || r.Path != "/api/bd/remote-agent-token" || r.Body == nil {
		return false
	}
	var body map[string]any
	if json.Unmarshal([]byte(*r.Body), &body) != nil {
		return false
	}
	claim, named := body["device_id"]
	switch claim.(type) {
	case string, nil:
		return false
	}
	return named
}

// targetLookups holds the Node vectors of the viewer for an rt_ id, whose
// answer RV30 changed on purpose. The access check asks the API server
// for the remote target before anything else, as the id names a target
// only when one exists: a vector whose scene has the target gets that
// lookup first in its calls, and answers as before. One whose scene has
// no target (not found, a failed or hanging call) is a device now; those
// are listed in devices, and their answer is the viewer's device data
// (checked in TestNodeVectors and TestAnIDWithTheTargetPrefixIsADeviceWhenNoTargetHasIt).
func targetLookups(t *testing.T, vectors []vector) (fixed map[int]vector, devices map[int]bool) {
	t.Helper()
	fixed, devices = map[int]vector{}, map[int]bool{}
	for i, v := range vectors {
		path, _, _ := strings.Cut(v.Request.Path, "?")
		id, ok := strings.CutPrefix(path, "/api/remote/viewer/")
		if !ok || !strings.HasPrefix(id, "rt_") {
			continue
		}
		lookup := apiCall{Method: http.MethodGet, URL: "/api/remote-targets/" + id}
		if answer, found := v.World.API["GET /api/remote-targets/"+id]; !found || answer.Hang || answer.Status != http.StatusOK {
			devices[i] = true
			continue
		}
		if len(v.Calls) > 0 && v.Calls[0] == lookup {
			continue
		}
		v.Calls = append([]apiCall{lookup}, v.Calls...)
		fixed[i] = v
	}
	if len(fixed) == 0 || len(devices) == 0 {
		t.Fatalf("%d viewer vectors of a target without its lookup first, %d of an rt_ id no target has; want both", len(fixed), len(devices))
	}
	return fixed, devices
}
