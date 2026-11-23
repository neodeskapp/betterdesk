package jsreq

import (
	"strconv"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
)

// Refusal reads the result of an API client method that fails with the
// API server's status (F3 in console/goapi). refused is true when the
// API server refused the call or gave no answer; status is then the one
// to answer with (502 without an answer) and reason what the API server
// said: the error it sent, the text it answered with (without white space
// around it), or the client's own words for the status when it answered
// neither.
func Refusal(result *goapi.Object) (status int, reason any, refused bool) {
	n, ok := result.Number("status")
	if !ok {
		return 0, nil, false
	}
	status = int(n)
	if !result.Bool("success") {
		return status, result.Get("error"), true
	}
	if text, isText := result.Get("data").(string); isText && strings.TrimSpace(text) != "" {
		return status, strings.TrimSpace(text), true
	}
	return status, "Request failed with status code " + strconv.Itoa(status), true
}
