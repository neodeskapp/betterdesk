package users

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/neodeskapp/neodesk/neodesk-server/auth"
	"github.com/neodeskapp/neodesk/neodesk-server/console/devicegroups"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsreq"
)

// What the routes made of the values of a request. A body member is a
// jsbind.Value, the value JSON.parse gave the old console; the functions
// here are the JavaScript expressions the routes wrote on it.

// errNotPrimitive is the TypeError JavaScript throws when it turns an
// object into text whose own toString member hides the method.
var errNotPrimitive = errors.New("Cannot convert object to primitive value")

// refusal is an error the routes answer with its status and its text, as
// the errors with a status the old routes threw.
type refusal struct {
	status  int
	message string
	// key, when set, names the text of the answer in the request's
	// language; message is then the key.
	key string
}

func (e *refusal) Error() string { return e.message }

func badRequest(message string) error {
	return &refusal{status: http.StatusBadRequest, message: message}
}

// badRequestKey is a refusal answered with the text of a message key.
func badRequestKey(key string) error {
	return &refusal{status: http.StatusBadRequest, message: key, key: key}
}

// refusalText is the answer's text for err: the text of its key in the
// request's language, or its own text.
func refusalText(r *http.Request, err error) string {
	var rf *refusal
	if errors.As(err, &rf) && rf.key != "" {
		return kit.T(r, rf.key, nil)
	}
	return err.Error()
}

// reply is an answer a route gave on its own instead of throwing: it is
// sent as it is, without the route's error handling.
type reply struct {
	write func(w http.ResponseWriter, r *http.Request)
	// status and message are what write answers
	status  int
	message string
}

func (e *reply) Error() string { return "answered by the route" }

// say is the reply {"success":false,"error":message}.
func say(status int, message string) error {
	return &reply{status: status, message: message, write: func(w http.ResponseWriter, r *http.Request) { fail(w, r, status, message) }}
}

// sayCode is the reply {"success":false,"error":message,"code":code}.
func sayCode(status int, code, message string) error {
	return &reply{status: status, message: message, write: func(w http.ResponseWriter, r *http.Request) {
		answer(w, r, status, failure{Error: message, Code: code})
	}}
}

// sayPartly is the reply of a change whose new email address was refused
// while the other fields of the request were saved (A274):
// {"success":false,"error":message,"code":code,"saved":[field names]}.
func sayPartly(status int, code, message string, saved []string) error {
	return &reply{status: status, message: message, write: func(w http.ResponseWriter, r *http.Request) {
		answer(w, r, status, struct {
			failure
			Saved []string `json:"saved"`
		}{failure{Error: message, Code: code}, saved})
	}}
}

// refusedText is the text a route would answer for err when err is a
// refusal of the request (a reply, or a refusal with a status below 500):
// the reason one account of a request for several was left alone. ok is
// false for any other error, which is the server's.
func refusedText(r *http.Request, err error) (text string, ok bool) {
	var answered *reply
	if errors.As(err, &answered) {
		return answered.message, true
	}
	if status := statusOf(err); status > 0 && status < http.StatusInternalServerError {
		return refusalText(r, err), true
	}
	return "", false
}

// sent writes err when it is a reply and reports whether it was one.
func sent(w http.ResponseWriter, r *http.Request, err error) bool {
	var answered *reply
	if errors.As(err, &answered) {
		answered.write(w, r)
		return true
	}
	return false
}

// errNoRow is the TypeError of reading a field of a row that was not
// there.
var errNoRow = errors.New("Cannot read properties of null (reading 'name')")

// statusOf is err.status, 0 for an error without one.
func statusOf(err error) int {
	var r *refusal
	if errors.As(err, &r) {
		return r.status
	}
	return 0
}

// truthy is !!v for a value of a row.
func truthy(v any) bool { return goapi.Truthy(devicegroups.Plain(v)) }

// rowText is String(v) for a value of a row.
func rowText(v any) string { return jsreq.String(devicegroups.Plain(v)) }

// givenFields are the members of updateFields the body has.
func givenFields(body middleware.JSONObject) []string {
	given := []string{}
	for _, name := range updateFields {
		if body.Has(name) {
			given = append(given, name)
		}
	}
	return given
}

// member is body[name].
func member(body middleware.JSONObject, name string) jsbind.Value {
	return jsbind.Of(body.Get(name))
}

// text is String(v). An object with its own toString member cannot be
// turned into text, also inside a list.
func text(v jsbind.Value) (string, error) {
	if hidesToString(v) {
		return "", errNotPrimitive
	}
	return v.ToString(), nil
}

func hidesToString(v jsbind.Value) bool {
	switch {
	case v.IsObject():
		return !v.Member("toString").IsUndefined()
	case v.IsArray():
		for _, item := range v.Items() {
			if hidesToString(item) {
				return true
			}
		}
	}
	return false
}

// textOr is String(v || fallback).
func textOr(v jsbind.Value, fallback string) (string, error) {
	if !v.Truthy() {
		return fallback, nil
	}
	return text(v)
}

// trimmedOr is String(v || fallback).trim().
func trimmedOr(v jsbind.Value, fallback string) (string, error) {
	s, err := textOr(v, fallback)
	return jsbind.Trim(s), err
}

// jsLength is s.length in UTF-16 code units.
func jsLength(s string) int { return jsreq.Length(s) }

// pathID is parseInt(req.params.id, 10); ok is false where the routes
// answered "Invalid user ID": NaN, zero and below.
func pathID(r *http.Request) (id float64, ok bool) {
	id, parsed := jsreq.ParseInt(r.PathValue("id"), 10)
	return id, parsed && id > 0
}

// ---- User group guids ----

var groupGUIDPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]+$`)

// validGroupGUID is isValidGroupGuid for text: 1 to 80 UTF-16 units of
// letters, digits and _ . : -.
func validGroupGUID(guid string) bool {
	n := jsLength(guid)
	return n > 0 && n <= 80 && groupGUIDPattern.MatchString(guid)
}

const maxGroupGUIDs = 100

// normalizeGroupGUIDs reads a list of user group guids from a list or
// comma-separated text: each item as String(item || "") trimmed, without
// empty ones and repeats, at most 100.
func normalizeGroupGUIDs(value jsbind.Value) ([]string, error) {
	var raw []jsbind.Value
	if value.IsArray() {
		raw = value.Items()
	} else {
		s, err := textOr(value, "")
		if err != nil {
			return nil, err
		}
		for _, part := range strings.Split(s, ",") {
			raw = append(raw, jsbind.String(part))
		}
	}
	out := []string{}
	seen := map[string]bool{}
	for _, item := range raw {
		guid, err := trimmedOr(item, "")
		if err != nil {
			return nil, err
		}
		if guid == "" || seen[guid] {
			continue
		}
		seen[guid] = true
		out = append(out, guid)
	}
	if len(out) > maxGroupGUIDs {
		out = out[:maxGroupGUIDs]
	}
	return out, nil
}

// groupGUIDsOf is validateGroupGuidsFromBody: the guids of body.groupGuids
// when the body has the member (given), refused when one of them is no
// valid guid.
func groupGUIDsOf(body middleware.JSONObject) (guids []string, given bool, err error) {
	if !body.Has("groupGuids") {
		return nil, false, nil
	}
	guids, err = normalizeGroupGUIDs(member(body, "groupGuids"))
	if err != nil {
		return nil, true, err
	}
	for _, guid := range guids {
		if !validGroupGUID(guid) {
			return nil, true, badRequest("Invalid user group identifier")
		}
	}
	return guids, true, nil
}

// ---- Email ----

// jsSpace is what \s matches in a JavaScript regular expression.
const jsSpace = `\t\n\v\f\r \x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

var emailPattern = regexp.MustCompile(`^[^` + jsSpace + `@]+@[^` + jsSpace + `@]+\.[^` + jsSpace + `@]+$`)

// normalizeEmail is normalizeUserEmail: nil for undefined and null, the
// text trimmed, "" for blank text, refused when it is longer than 200
// UTF-16 units or no address.
func normalizeEmail(v jsbind.Value) (*string, error) {
	if v.IsUndefined() || v.IsNull() {
		return nil, nil
	}
	s, err := text(v)
	if err != nil {
		return nil, err
	}
	trimmed := jsbind.Trim(s)
	switch {
	case trimmed == "":
	case jsLength(trimmed) > 200:
		return nil, badRequestKey("users.email_too_long")
	case !emailPattern.MatchString(trimmed):
		return nil, badRequestKey("users.invalid_email")
	}
	return &trimmed, nil
}

// ---- Passwords ----

// passwordRefusal is the reply to a new password that breaks the password
// rule (auth.CheckNewPassword): no text, shorter than 8 or longer than 128
// UTF-16 units. nil for one that keeps it.
func passwordRefusal(r *http.Request, password jsbind.Value) error {
	if !password.IsString() {
		return sayKey(r, http.StatusBadRequest, "users.password_too_short")
	}
	switch auth.CheckNewPassword(password.Str()) {
	case auth.ErrPasswordTooShort:
		return sayKey(r, http.StatusBadRequest, "users.password_too_short")
	case auth.ErrPasswordTooLong:
		return errPasswordTooLong
	}
	return nil
}
