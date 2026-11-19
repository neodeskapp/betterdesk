package remote

import (
	"encoding/json"
	"regexp"
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
