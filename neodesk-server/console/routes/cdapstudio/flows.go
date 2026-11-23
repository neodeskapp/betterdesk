package cdapstudio

import (
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"strings"
	"unicode/utf16"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/httpx"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
	"github.com/neodeskapp/neodesk/neodesk-server/console/store"
)

// maxFlowBytes is the longest flow JSON that is stored, in bytes: the
// limit of a request body. A flow sent as text cannot be longer; one sent
// as a JSON value can, when its numbers grow as they are written out
// again (1e20 is stored as 21 digits).
const maxFlowBytes = middleware.DefaultBodyLimit

// maxExportFlowBytes is the most bytes a flow may take in its export,
// where it is written with two spaces per level: deep nesting makes that
// many times its stored length, so a flow over it is refused when it is
// stored and is not exported.
const maxExportFlowBytes = 8 << 20

// exportTooLarge reports whether the flow, one level into the export file,
// takes more than maxExportFlowBytes there.
func exportTooLarge(flow any) bool {
	return indentedLen(flow, 2, 1, maxExportFlowBytes) > maxExportFlowBytes
}

const (
	notFound     = "Flow not found"
	invalidJSON  = "Invalid flow JSON"
	flowTooLarge = "Flow data too large (max 2MB)"
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

// flowOfAccount loads the flow of the address for an account that may see
// and change it. For any other account, and for an id that names no flow,
// it answers 404 and returns nil, so the flow of somebody else reads like
// one that does not exist.
func (h *handlers) flowOfAccount(w http.ResponseWriter, r *http.Request) (*flow, error) {
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
	if len(text) > maxFlowBytes {
		return "", flowTooLarge
	}
	parsed, err := parseJSON(text)
	if err != nil {
		return "", invalidJSON
	}
	if exportTooLarge(parsed) {
		return "", flowTooLarge
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
	f, err := h.flowOfAccount(w, r)
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
	existing, err := h.flowOfAccount(w, r)
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
	existing, err := h.flowOfAccount(w, r)
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
	existing, err := h.flowOfAccount(w, r)
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

// test answers POST /api/cdap-studio/flows/{id}/test with a dry run: one
// made-up result per node of the flow, and the numbers of nodes and
// wires. Nothing of the flow runs.
func (h *handlers) test(w http.ResponseWriter, r *http.Request) {
	f, err := h.flowOfAccount(w, r)
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
	text, _ := stringify(dryRun(data), "")
	httpx.WriteJSONBytes(w, r, http.StatusOK, []byte(text))
}

// dryRun builds the answer of a test run from the parsed flow. nodes and
// wires count only when they are lists; anything else counts as an empty
// list. A node that is no object, or whose type cannot be written as text,
// gets no result; nodeCount still counts it.
func dryRun(data any) *object {
	nodes, _ := property(data, "nodes").([]any)
	wires, _ := property(data, "wires").([]any)
	results := make([]any, 0, len(nodes))
	for _, node := range nodes {
		if _, isObject := node.(*object); !isObject {
			continue
		}
		kind := property(node, "type")
		kindText, ok := toText(kind)
		if !ok {
			continue
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
	answer.set("nodeCount", float64(len(nodes)))
	answer.set("wireCount", float64(len(wires)))
	return answer
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
	f, err := h.flowOfAccount(w, r)
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
	if exportTooLarge(data) {
		kit.Fail(w, r, http.StatusBadRequest, flowTooLarge)
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
