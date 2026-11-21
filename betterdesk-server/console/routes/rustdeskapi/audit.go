package rustdeskapi

import (
	"context"
	"math"
	"net/http"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/httpx"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/reqquery"
	"github.com/unitronix/betterdesk-server/console/store"
)

// The client audit records: connections, file transfers and alarms that
// RustDesk clients report, and the lists of them. Where the console runs
// without its own client API port (API_ENABLED=false) the clients report
// to the server's API, and the lists come from there (AUDIT_SOURCE_IS_GO);
// the console's own tables answer when that call fails.

// The connection types and alarm types a report may name.
var (
	connTypes  = map[float64]bool{0: true, 1: true, 2: true, 3: true, 4: true}
	alarmTypes = map[float64]bool{0: true, 1: true, 2: true, 3: true, 4: true, 5: true, 6: true}
)

// reportedIP is sanitizeStr(body.ip || req.ip, 64).
func reportedIP(r *http.Request, v any) string {
	if truthy(v) {
		return sanitize(v, 64)
	}
	return sanitize(kit.ClientIP(r), 64)
}

// textOrEmpty is String(v) when v != null, else "".
func textOrEmpty(v any) (string, error) {
	if v == nil || isUndefined(v) {
		return "", nil
	}
	return str(v)
}

// reportConnection answers POST /api/audit/conn.
func (h *handlers) reportConnection(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	serverError := func(err error) {
		h.log.Error("[API:AUDIT/CONN] Error:", jsbind.Message(err))
		fail(w, r, http.StatusInternalServerError, msgServerError)
	}
	hostID, err := textOrEmpty(member(body, "host_id"))
	if err != nil {
		serverError(err)
		return
	}
	if hostID == "" {
		fail(w, r, http.StatusBadRequest, "host_id is required")
		return
	}
	connType := 0.0
	if f, ok := number(member(body, "conn_type")); ok {
		connType = f
	}
	if !connTypes[connType] {
		fail(w, r, http.StatusBadRequest, "Invalid conn_type")
		return
	}
	hostUUID, err := textOrEmpty(member(body, "host_uuid"))
	if err != nil {
		serverError(err)
		return
	}
	peerID, err := textOrEmpty(member(body, "peer_id"))
	if err != nil {
		serverError(err)
		return
	}
	err = h.store.exec(r.Context(), `
                INSERT INTO audit_connections (host_id, host_uuid, peer_id, peer_name, action, conn_type, session_id, ip)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?)
            `, 8,
		jsbind.String(sanitize(hostID, maxIDLen)),
		jsbind.String(sanitize(hostUUID, maxIDLen)),
		jsbind.String(sanitize(peerID, maxIDLen)),
		jsbind.String(sanitize(or(member(body, "peer_name"), ""), maxHostnameLen)),
		jsbind.String(sanitize(or(member(body, "action"), "connect"), 32)),
		jsbind.Number(connType+0),
		jsbind.String(sanitize(or(member(body, "session_id"), ""), 64)),
		jsbind.String(reportedIP(r, member(body, "ip"))),
	)
	if err != nil {
		serverError(err)
		return
	}
	kit.JSON(w, r, http.StatusOK, struct{}{})
}

// reportFile answers POST /api/audit/file.
func (h *handlers) reportFile(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	host, isText := member(body, "host_id").(string)
	if !isText || host == "" {
		fail(w, r, http.StatusBadRequest, "host_id is required")
		return
	}
	direction := 0.0
	if f, ok := number(member(body, "direction")); ok && (f == 0 || f == 1) {
		direction = f
	}
	numFiles := 0.0
	if f, ok := number(member(body, "num_files")); ok {
		numFiles = math.Max(0, math.Min(10000, f))
	}
	files := []any{}
	if list, ok := member(body, "files").([]any); ok {
		if len(list) > 100 {
			list = list[:100]
		}
		for _, f := range list {
			var name any
			size := 0.0
			switch x := f.(type) {
			case *goapi.Object:
				name = or(x.Get("name"), "")
				if s, ok := number(x.Get("size")); ok {
					size = math.Max(0, s)
				}
			case []any:
				name = ""
			default:
				text, err := str(or(f, ""))
				if err != nil {
					h.log.Error("[API:AUDIT/FILE] Error:", err.Error())
					fail(w, r, http.StatusInternalServerError, msgServerError)
					return
				}
				name = text
			}
			files = append(files, goapi.NewObject("name", sanitize(name, maxPathLen), "size", size))
		}
	}
	// is_file is a boolean in the console's own PostgreSQL table and an
	// integer in the API server's and on SQLite: the flag is written as the
	// table the store found takes it (DECISIONS D2)
	isFile := member(body, "is_file") != false
	var fileFlag jsbind.Value
	switch {
	case h.d.Store.Shape().Table("audit_files").Type("is_file") == store.TypeBoolean:
		fileFlag = jsbind.Bool(isFile)
	case isFile:
		fileFlag = jsbind.Number(1)
	default:
		fileFlag = jsbind.Number(0)
	}
	err := h.store.exec(r.Context(), `
                INSERT INTO audit_files (host_id, host_uuid, peer_id, direction, path, is_file, num_files, files_json, ip, peer_name)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
            `, 10,
		jsbind.String(sanitize(host, maxIDLen)),
		jsbind.String(sanitize(or(member(body, "host_uuid"), ""), maxIDLen)),
		jsbind.String(sanitize(or(member(body, "peer_id"), ""), maxIDLen)),
		jsbind.Number(orZero(direction)+0),
		jsbind.String(sanitize(or(member(body, "path"), ""), maxPathLen)),
		fileFlag,
		jsbind.Number(numFiles+0),
		jsbind.String(stringify(files)),
		jsbind.String(reportedIP(r, member(body, "ip"))),
		jsbind.String(sanitize(or(member(body, "peer_name"), ""), maxHostnameLen)),
	)
	if err != nil {
		h.log.Error("[API:AUDIT/FILE] Error:", jsbind.Message(err))
		fail(w, r, http.StatusInternalServerError, msgServerError)
		return
	}
	kit.JSON(w, r, http.StatusOK, struct{}{})
}

// maxDetailsLen is the most UTF-16 units of an alarm's details, the limit
// the system information has for its features.
const maxDetailsLen = 4096

// reportAlarm answers POST /api/audit/alarm.
func (h *handlers) reportAlarm(w http.ResponseWriter, r *http.Request) {
	body := kit.Body(r)
	alarmType := 0.0
	if f, ok := number(member(body, "alarm_type")); ok {
		alarmType = f
	}
	if !alarmTypes[alarmType] {
		fail(w, r, http.StatusBadRequest, "Invalid alarm_type (0-6)")
		return
	}
	details := or(member(body, "details"), goapi.NewObject())
	detailsText, isText := details.(string)
	if !isText {
		detailsText = stringify(details)
	}
	// Details longer than the limit are stored as {}, as the features of
	// the system information are: a text cut short would no longer be the
	// JSON the PostgreSQL column takes
	if utf16Len(detailsText) > maxDetailsLen {
		detailsText = "{}"
	}
	err := h.store.exec(r.Context(), `
                INSERT INTO audit_alarms (alarm_type, alarm_name, host_id, peer_id, ip, details)
                VALUES (?, ?, ?, ?, ?, ?)
            `, 6,
		jsbind.Number(alarmType+0),
		jsbind.String(sanitize(or(member(body, "alarm_name"), ""), maxStringLen)),
		jsbind.String(sanitize(or(member(body, "host_id"), ""), maxIDLen)),
		jsbind.String(sanitize(or(member(body, "peer_id"), ""), maxIDLen)),
		jsbind.String(reportedIP(r, member(body, "ip"))),
		jsbind.String(detailsText),
	)
	if err != nil {
		h.log.Error("[API:AUDIT/ALARM] Error:", jsbind.Message(err))
		fail(w, r, http.StatusInternalServerError, msgServerError)
		return
	}
	kit.JSON(w, r, http.StatusOK, struct{}{})
}

// auditList is one of the three lists, read from the server's API first
// when it holds the records.
type auditList struct {
	table   string
	fromAPI func(ctx context.Context, filters any) *goapi.Object
}

// pageNumbers are limit and offset of a list query:
// Math.min(200, Math.max(1, parseInt(limit) || 100)) and
// Math.max(0, parseInt(offset) || 0). ok is false where parseInt threw.
func pageNumbers(q *reqquery.Query) (limit, offset float64, ok bool) {
	limitText, ok1 := q.Get("limit").Text()
	offsetText, ok2 := q.Get("offset").Text()
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	return math.Min(200, math.Max(1, orNaN(parseIntText(limitText), 100))), math.Max(0, orNaN(parseIntText(offsetText), 0)), true
}

type auditListAnswer struct {
	Data  any `json:"data"`
	Total any `json:"total"`
}

// list answers a list with the filters: {data, total}, or empty when a
// filter cannot be read or the query fails.
func (h *handlers) list(w http.ResponseWriter, r *http.Request, l auditList, keys []string, alarm bool) {
	ctx := r.Context()
	q := reqquery.Of(r)
	empty := auditListAnswer{Data: []any{}, Total: float64(0)}
	limit, offset, ok := pageNumbers(q)
	if !ok {
		kit.JSON(w, r, http.StatusOK, empty)
		return
	}
	params := goapi.NewObject()
	var filters []auditFilter
	if alarm {
		value := q.Get("alarm_type")
		if value.Defined() {
			text, ok := value.Text()
			if !ok {
				kit.JSON(w, r, http.StatusOK, empty)
				return
			}
			n := parseIntText(text)
			params.Set("alarm_type", n)
			filters = append(filters, auditFilter{column: "alarm_type", value: jsbind.Number(n), always: true})
		} else {
			params.Set("alarm_type", nil)
		}
	}
	for _, key := range keys {
		value := q.Get(key)
		var param any = ""
		if value.Truthy() {
			param = value.JSON()
		}
		params.Set(key, param)
		filters = append(filters, auditFilter{column: key, value: jsbind.Query(r.URL.RawQuery, key)})
	}
	params.Set("limit", limit)
	params.Set("offset", offset)
	if h.auditFromGo {
		if remote := l.fromAPI(ctx, params); remote != nil {
			kit.JSON(w, r, http.StatusOK, remote)
			return
		}
	}
	rows, err := h.store.auditRows(ctx, l.table, filters, limit, offset)
	var total float64
	if err == nil {
		total, err = h.store.auditCount(ctx, l.table, filters)
	}
	if err != nil {
		h.log.Error("[API:AUDIT] Query error:", jsbind.Message(err))
		kit.JSON(w, r, http.StatusOK, empty)
		return
	}
	kit.JSON(w, r, http.StatusOK, auditListAnswer{Data: rows, Total: total})
}

// mayReadAudit checks that the account of a token may read the audit
// log: audit.view, with the overrides of its role, as the console asks of
// a session. It answers 403 {"error":"Permission denied: audit.view"}
// when not, and 500 when the permissions cannot be read.
func (h *handlers) mayReadAudit(w http.ResponseWriter, r *http.Request, user *tokenUser) bool {
	allowed, err := h.d.Access.RoleHasEffectivePermission(r.Context(), user.role(), auditView)
	if err != nil {
		h.serverFailure(w, r, "Permission lookup failed:", err)
		return false
	}
	if !allowed {
		fail(w, r, http.StatusForbidden, access.MsgPermissionDenied(auditView))
		return false
	}
	return true
}

// auditView is the permission the audit lists ask for.
const auditView = "audit.view"

// connections answers GET /api/audit/conn: with a bearer token or for
// someone signed in to the console, when the account's role may read the
// audit log (audit.view; the console's refusal for a session).
func (h *handlers) connections(w http.ResponseWriter, r *http.Request) {
	if token := bearerToken(r); token != "" {
		user, ok := h.validate(w, r, token)
		if !ok {
			return
		}
		if user == nil {
			fail(w, r, http.StatusUnauthorized, msgInvalidToken)
			return
		}
		if !h.mayReadAudit(w, r, user) {
			return
		}
	} else if kit.UserID(r) == 0 {
		fail(w, r, http.StatusUnauthorized, msgAuthRequired)
		return
	} else if decision := kit.Access(r).RequirePermission("audit.view", httpx.OriginalURL(r)); !decision.Passed() {
		h.d.Refuse(w, r, decision)
		return
	}
	h.list(w, r, auditList{table: "audit_connections", fromAPI: h.d.GoAPI.GetClientAuditConnections}, []string{"host_id", "peer_id", "action"}, false)
}

// files answers GET /api/audit/file.
func (h *handlers) files(w http.ResponseWriter, r *http.Request) {
	if !h.mayReadAudit(w, r, authUser(r)) {
		return
	}
	h.list(w, r, auditList{table: "audit_files", fromAPI: h.d.GoAPI.GetClientAuditFiles}, []string{"host_id", "peer_id"}, false)
}

// alarms answers GET /api/audit/alarm.
func (h *handlers) alarms(w http.ResponseWriter, r *http.Request) {
	if !h.mayReadAudit(w, r, authUser(r)) {
		return
	}
	h.list(w, r, auditList{table: "audit_alarms", fromAPI: h.d.GoAPI.GetClientAuditAlarms}, []string{"host_id"}, true)
}

type auditSummary struct {
	Connections any `json:"connections"`
	Files       any `json:"files"`
	Alarms      any `json:"alarms"`
}

// summary answers GET /api/audit: the newest 50 of each kind.
func (h *handlers) summary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var user *tokenUser
	if token := bearerToken(r); token != "" {
		var ok bool
		if user, ok = h.validate(w, r, token); !ok {
			return
		}
	}
	if user == nil {
		fail(w, r, http.StatusUnauthorized, msgAuthRequired)
		return
	}
	if !h.mayReadAudit(w, r, user) {
		return
	}
	answer := func(s auditSummary) {
		kit.JSON(w, r, http.StatusOK, map[string]any{"data": s})
	}
	if h.auditFromGo {
		filters := goapi.NewObject("limit", float64(50))
		c, f, a := h.d.GoAPI.GetClientAuditConnections(ctx, filters), h.d.GoAPI.GetClientAuditFiles(ctx, filters), h.d.GoAPI.GetClientAuditAlarms(ctx, filters)
		if c != nil || f != nil || a != nil {
			rowsOf := func(o *goapi.Object) any {
				if o != nil && truthy(o.Get("data")) {
					return o.Get("data")
				}
				return []any{}
			}
			answer(auditSummary{Connections: rowsOf(c), Files: rowsOf(f), Alarms: rowsOf(a)})
			return
		}
	}
	var s auditSummary
	var err error
	for _, part := range []struct {
		table string
		into  *any
	}{{"audit_connections", &s.Connections}, {"audit_files", &s.Files}, {"audit_alarms", &s.Alarms}} {
		var rows []*goapi.Object
		if rows, err = h.store.auditRows(ctx, part.table, nil, 50, 0); err != nil {
			break
		}
		*part.into = rows
	}
	if err != nil {
		h.log.Error("[API:AUDIT] Error:", jsbind.Message(err))
		answer(auditSummary{Connections: []any{}, Files: []any{}, Alarms: []any{}})
		return
	}
	answer(s)
}
