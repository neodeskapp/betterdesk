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
	return twins
}
