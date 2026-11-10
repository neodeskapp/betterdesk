package cdapstudio

import (
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/middleware"
	"github.com/unitronix/betterdesk-server/console/session"
	"github.com/unitronix/betterdesk-server/console/store"
)

// maxFlowUnits is the longest flow JSON, counted the way JavaScript counts
// the length of a string.
const maxFlowUnits = 5 * 1024 * 1024

const (
	notFound     = "Flow not found"
	invalidJSON  = "Invalid flow JSON"
	flowTooLarge = "Flow data too large (max 5MB)"
)

type flowList struct {
	Success bool         `json:"success"`
	Flows   []listedFlow `json:"flows"`
}

type oneFlow struct {
	Success bool  `json:"success"`
	Flow    *flow `json:"flow"`
}

type created struct {
	Success bool   `json:"success"`
	ID      string `json:"id"`
}

type deployed struct {
	Success bool   `json:"success"`
	Status  string `json:"status"`
}

// failed logs why a route could not answer and sends 500 with the
// route's own message.
func (h *handlers) failed(w http.ResponseWriter, r *http.Request, what string, err error, message string) {
	h.log.Error("[cdap-studio] "+what+":", err)
	kit.Fail(w, r, http.StatusInternalServerError, message)
}

// mayAccess reports whether the account may see and change the flow.
func mayAccess(user *session.User, f *flow) bool {
	return user != nil && (access.IsSuperAdminRole(user.Role) || string(f.CreatedBy) == user.Username)
}

// flowToRead loads the flow of the address for an account that may see
// it. For any other account, and for an id that names no flow, it
// answers 404 and returns nil.
func (h *handlers) flowToRead(w http.ResponseWriter, r *http.Request) (*flow, error) {
	f, err := getFlow(r.Context(), h.db, r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	if f == nil || !mayAccess(kit.User(r), f) {
		kit.Fail(w, r, http.StatusNotFound, notFound)
		return nil, nil
	}
	return f, nil
}

// flowToChange loads the flow of the address for an account that may
// change it. It answers 404 for an id that names no flow and 403 for the
// flow of somebody else, and returns nil.
func (h *handlers) flowToChange(w http.ResponseWriter, r *http.Request) (*flow, error) {
	f, err := getFlow(r.Context(), h.db, r.PathValue("id"))
	if err != nil {
		return nil, err
	}
	if f == nil {
		kit.Fail(w, r, http.StatusNotFound, notFound)
		return nil, nil
	}
	if !mayAccess(kit.User(r), f) {
		kit.Fail(w, r, http.StatusForbidden, "Not authorized")
		return nil, nil
	}
	return f, nil
}

// flowText turns the flow_json of a request into the text that is
// stored: a string as it is, anything else written out as JSON, and {}
// for nothing. problem is the message for the client when the text is
// too long or not JSON.
func flowText(value middleware.Value) (text, problem string) {
	if s, isString := value.Str(); isString {
		text = s
	} else {
		text = "{}"
		if value.Truthy() {
			parsed, err := parseJSON(string(value.Raw()))
			if err != nil {
				return "", invalidJSON
			}
			text, _ = stringify(parsed, "")
		}
	}
	if units(text) > maxFlowUnits {
		return "", flowTooLarge
	}
	if _, err := parseJSON(text); err != nil {
		return "", invalidJSON
	}
	return text, ""
}

// trimmed is value.trim() for a string, and "" with false for anything
// else.
func trimmed(value middleware.Value) (string, bool) {
	s, isString := value.Str()
	if !isString {
		return "", false
	}
	return strings.TrimFunc(s, isSpace), true
}

// firstUnits is s.slice(0, n): the first n characters as JavaScript
// counts them.
func firstUnits(s string, n int) string {
	u := utf16.Encode([]rune(s))
	if len(u) <= n {
		return s
	}
	return string(utf16.Decode(u[:n]))
}

// description is the description of a request: trimmed and cut to 500
// characters, "" when it is no string.
func description(value middleware.Value) string {
	text, _ := trimmed(value)
	return firstUnits(text, 500)
}

// isSpace reports the white space JavaScript's trim removes.
func isSpace(r rune) bool {
	switch {
	case r >= '\t' && r <= '\r', r == ' ', r == 0xA0, r == 0x1680:
		return true
	case r >= 0x2000 && r <= 0x200A:
		return true
	case r == 0x2028, r == 0x2029, r == 0x202F, r == 0x205F, r == 0x3000, r == 0xFEFF:
		return true
	}
	return false
}

// newID makes the id of a new flow: 8 random bytes in hex.
func (h *handlers) newID() (string, error) {
	id := make([]byte, 8)
	if _, err := io.ReadFull(h.random, id); err != nil {
		return "", err
	}
	return hex.EncodeToString(id), nil
}

// store saves a new flow for the signed-in account and answers its id.
func (h *handlers) store(w http.ResponseWriter, r *http.Request, name, description, text string) error {
	user := kit.User(r)
	if user == nil {
		return errors.New("the session has no user")
	}
	id, err := h.newID()
	if err != nil {
		return err
	}
	if err := createFlow(r.Context(), h.db, id, name, description, text, user.Username, h.now()); err != nil {
		return err
	}
	kit.JSON(w, r, http.StatusOK, created{Success: true, ID: id})
	return nil
}

// list answers GET /api/cdap-studio/flows: the flows without their JSON,
// all of them for a super admin and the account's own for everyone else.
func (h *handlers) list(w http.ResponseWriter, r *http.Request) {
	var owner *string
	if user := kit.User(r); user != nil && !access.IsSuperAdminRole(user.Role) {
		owner = &user.Username
	}
	flows, err := listFlows(r.Context(), h.db, owner)
	if err != nil {
		h.failed(w, r, "list flows", err, "Failed to list flows")
		return
	}
	kit.JSON(w, r, http.StatusOK, flowList{Success: true, Flows: flows})
}

// get answers GET /api/cdap-studio/flows/{id} with the whole flow.
func (h *handlers) get(w http.ResponseWriter, r *http.Request) {
	f, err := h.flowToRead(w, r)
	if err != nil {
		h.failed(w, r, "get flow", err, "Failed to get flow")
		return
	}
	if f == nil {
		return
	}
	kit.JSON(w, r, http.StatusOK, oneFlow{Success: true, Flow: f})
}

// create answers POST /api/cdap-studio/flows with the body
// {"name", "description", "flow_json"}. flow_json may be JSON text or the
// JSON value itself.
func (h *handlers) create(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	name, _ := trimmed(body.Get("name"))
	if name == "" || units(name) > 100 {
		kit.Fail(w, r, http.StatusBadRequest, "Invalid flow name")
		return
	}
	text, problem := flowText(body.Get("flow_json"))
	if problem != "" {
		kit.Fail(w, r, http.StatusBadRequest, problem)
		return
	}
	if err := h.store(w, r, name, description(body.Get("description")), text); err != nil {
		h.failed(w, r, "create flow", err, "Failed to create flow")
	}
}

// update answers PUT /api/cdap-studio/flows/{id}. What the body leaves
// out keeps its value; so does a name that is empty or no string.
func (h *handlers) update(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	existing, err := h.flowToChange(w, r)
	if err != nil {
		h.failed(w, r, "update flow", err, "Failed to update flow")
		return
	}
	if existing == nil {
		return
	}
	text := string(existing.FlowJSON)
	if value := body.Get("flow_json"); value.Exists() {
		var problem string
		if text, problem = flowText(value); problem != "" {
			kit.Fail(w, r, http.StatusBadRequest, problem)
			return
		}
	}
	name := string(existing.Name)
	if given, _ := trimmed(body.Get("name")); given != "" {
		name = firstUnits(given, 100)
	}
	about := string(existing.Description)
	if value := body.Get("description"); value.Exists() {
		about = description(value)
	}
	if err := updateFlow(r.Context(), h.db, string(existing.ID), name, about, text, h.now()); err != nil {
		h.failed(w, r, "update flow", err, "Failed to update flow")
		return
	}
	kit.OK(w, r, nil)
}

// remove answers DELETE /api/cdap-studio/flows/{id}.
func (h *handlers) remove(w http.ResponseWriter, r *http.Request) {
	existing, err := h.flowToChange(w, r)
	if err == nil && existing != nil {
		err = deleteFlow(r.Context(), h.db, string(existing.ID))
	}
	if err != nil {
		h.failed(w, r, "delete flow", err, "Failed to delete flow")
		return
	}
	if existing != nil {
		kit.OK(w, r, nil)
	}
}

// deploy answers POST /api/cdap-studio/flows/{id}/deploy: it marks the
// flow as deployed. Nothing is started.
func (h *handlers) deploy(w http.ResponseWriter, r *http.Request) {
	existing, err := h.flowToChange(w, r)
	if err == nil && existing != nil {
		err = setFlowStatus(r.Context(), h.db, string(existing.ID), "deployed", h.now())
	}
	if err != nil {
		h.failed(w, r, "deploy flow", err, "Failed to deploy flow")
		return
	}
	if existing != nil {
		kit.JSON(w, r, http.StatusOK, deployed{Success: true, Status: "deployed"})
	}
}

// errNotRunnable is a flow whose nodes cannot be walked.
var errNotRunnable = errors.New("the flow's nodes cannot be read")

// test answers POST /api/cdap-studio/flows/{id}/test with a dry run: one
// made-up result per node of the flow, and the numbers of nodes and
// wires. Nothing of the flow runs.
func (h *handlers) test(w http.ResponseWriter, r *http.Request) {
	f, err := h.flowToRead(w, r)
	if err != nil {
		h.failed(w, r, "test flow", err, "Failed to test flow")
		return
	}
	if f == nil {
		return
	}
	data, err := parseJSON(string(f.FlowJSON))
	if err != nil {
		kit.Fail(w, r, http.StatusBadRequest, invalidJSON)
		return
	}
	answer, err := dryRun(data)
	if err != nil {
		h.failed(w, r, "test flow", err, "Failed to test flow")
		return
	}
	text, _ := stringify(answer, "")
	httpx.WriteJSONBytes(w, r, http.StatusOK, []byte(text))
}

// dryRun builds the answer of a test run from the parsed flow. It reads
// the flow the way the old console's JavaScript did, whatever the JSON
// holds: nodes may be a list or a text (one node per character), a node
// may be any value but null, and a member that is not there is left out
// of the result.
func dryRun(data any) (*object, error) {
	nodes, wires := any([]any{}), any([]any{})
	if truthy(data) {
		if v := property(data, "nodes"); truthy(v) {
			nodes = v
		}
		if v := property(data, "wires"); truthy(v) {
			wires = v
		}
	}
	var list []any
	switch t := nodes.(type) {
	case []any:
		list = t
	case string:
		list = characters(t)
	default:
		return nil, errNotRunnable
	}
	results := make([]any, 0, len(list))
	for _, node := range list {
		if node == nil {
			return nil, errNotRunnable
		}
		kind := property(node, "type")
		kindText, ok := toText(kind)
		if !ok {
			return nil, errNotRunnable
		}
		label := property(node, "label")
		if !truthy(label) {
			label = kind
		}
		output := newObject()
		output.set("_simulated", true)
		output.set("value", "Mock data from "+kindText)
		result := newObject()
		result.set("nodeId", property(node, "id"))
		result.set("nodeType", kind)
		result.set("label", label)
		result.set("status", "ok")
		result.set("output", output)
		results = append(results, result)
	}
	answer := newObject()
	answer.set("success", true)
	answer.set("results", results)
	answer.set("nodeCount", property(nodes, "length"))
	answer.set("wireCount", property(wires, "length"))
	return answer, nil
}

// characters cuts a text into its characters, each half of a broken
// surrogate pair on its own.
func characters(s string) []any {
	var out []any
	for i := 0; i < len(s); {
		size := 3
		if _, half := halfPair(s, i); !half {
			_, size = utf8.DecodeRuneInString(s[i:])
		}
		out = append(out, s[i:i+size])
		i += size
	}
	return out
}

// exported is a .bdflow file.
type exported struct {
	name, description, exportedAt, exportedBy string
	flow                                      any
}

func (e exported) text() string {
	file := newObject()
	file.set("version", "1.0")
	file.set("format", "bdflow")
	file.set("name", e.name)
	file.set("description", e.description)
	file.set("flow", e.flow)
	file.set("exportedAt", e.exportedAt)
	file.set("exportedBy", e.exportedBy)
	text, _ := stringify(file, "  ")
	return text
}

// export answers GET /api/cdap-studio/flows/{id}/export with the flow as
// a .bdflow file: JSON with two spaces of indentation, named after the
// flow with everything but letters, digits, _ and - replaced by _. The
// file is never compressed.
func (h *handlers) export(w http.ResponseWriter, r *http.Request) {
	f, err := h.flowToRead(w, r)
	if err != nil {
		h.failed(w, r, "export flow", err, "Failed to export flow")
		return
	}
	if f == nil {
		return
	}
	data, err := parseJSON(string(f.FlowJSON))
	if err != nil {
		kit.Fail(w, r, http.StatusBadRequest, invalidJSON)
		return
	}
	user := kit.User(r)
	if user == nil {
		h.failed(w, r, "export flow", errors.New("the session has no user"), "Failed to export flow")
		return
	}
	file := exported{name: string(f.Name), description: string(f.Description), flow: data, exportedAt: store.ISOTime(h.now()), exportedBy: user.Username}
	w.Header().Set("Content-Disposition", `attachment; filename="`+fileName(string(f.Name))+`.bdflow"`)
	httpx.WriteBody(w, r, http.StatusOK, "application/json; charset=utf-8", []byte(file.text()))
}

// fileName replaces every character of a flow's name that is not a
// letter, a digit, _ or - by _. A character outside the basic plane
// becomes two.
func fileName(name string) string {
	var b strings.Builder
	for _, unit := range utf16.Encode([]rune(name)) {
		c := byte('_')
		if unit < 0x80 && (unit >= 'a' && unit <= 'z' || unit >= 'A' && unit <= 'Z' || unit >= '0' && unit <= '9' || unit == '_' || unit == '-') {
			c = byte(unit)
		}
		b.WriteByte(c)
	}
	return b.String()
}

// importFlow answers POST /api/cdap-studio/flows/import with the body of
// a .bdflow file: {"name", "description", "flow"}. The flow has to be an
// object or a list; a file without a name becomes "Imported Flow".
func (h *handlers) importFlow(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	data := body.Get("flow")
	if kind := data.Kind(); kind != middleware.KindObject && kind != middleware.KindArray {
		kit.Fail(w, r, http.StatusBadRequest, "Invalid .bdflow format")
		return
	}
	text, problem := flowText(data)
	if problem != "" {
		kit.Fail(w, r, http.StatusBadRequest, problem)
		return
	}
	name, _ := trimmed(body.Get("name"))
	if name == "" {
		name = "Imported Flow"
	}
	if err := h.store(w, r, firstUnits(name, 100), description(body.Get("description")), text); err != nil {
		h.failed(w, r, "import flow", err, "Failed to import flow")
	}
}

// templates answers GET /api/cdap-studio/templates.
func (h *handlers) templates(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSONBytes(w, r, http.StatusOK, templates)
}
