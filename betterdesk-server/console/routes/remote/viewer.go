package remote

import (
	"context"
	"net/http"
	"regexp"
	"strings"

	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/guestaccess"
	"github.com/unitronix/betterdesk-server/console/jsbind"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/routes/internal/reqquery"
	"github.com/unitronix/betterdesk-server/console/serverinfo"
)

// deviceIDPattern is a device id the viewer and the redirects take.
var deviceIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{3,64}$`)

// viewerAnswer is the answer of GET /api/remote/viewer/:deviceId, with the
// keys in the order the web app has always received them.
type viewerAnswer struct {
	Success      bool          `json:"success"`
	DeviceID     string        `json:"deviceId"`
	Device       viewerDevice  `json:"device"`
	Capabilities capabilities  `json:"capabilities"`
	ServerPubKey string        `json:"serverPubKey"`
	Connection   *goapi.Object `json:"connection"`
	GuestToken   string        `json:"guestToken"`
	MockRemote   any           `json:"mockRemote"`
	User         *viewerUser   `json:"user"`
	WorkReports  bool          `json:"workReports"`
	CSRFToken    string        `json:"csrfToken"`
}

type viewerDevice struct {
	Name     any `json:"name"`
	Platform any `json:"platform"`
}

type viewerUser struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// capabilities say how the viewer connects to the device.
type capabilities struct {
	Transport          string        `json:"transport"`
	OSAgent            bool          `json:"os_agent"`
	CDAPConnected      bool          `json:"cdap_connected"`
	MeshConnected      bool          `json:"mesh_connected"`
	DeviceType         string        `json:"device_type"`
	MeshShare          bool          `json:"mesh_share"`
	MeshViewOnly       bool          `json:"mesh_view_only"`
	RemoteTarget       bool          `json:"remote_target"`
	RemoteProtocol     string        `json:"remote_protocol"`
	RemoteTargetConfig *goapi.Object `json:"remote_target_config"`
	GuestAccess        bool          `json:"guest_access"`
	GuestViewOnly      bool          `json:"guest_view_only"`
	GuestPeerIDs       any           `json:"guest_peer_ids"`

	// meshAgent says the API server calls the device a mesh agent.
	meshAgent bool
}

type viewerFailure struct {
	Success bool   `json:"success"`
	Code    string `json:"code"`
	Error   string `json:"error"`
}

// viewer answers GET /api/remote/viewer/:deviceId with everything the
// viewer needs for one device, once requireRemoteAccess let the request
// through: the device's name and platform, how to reach it (transport and
// capabilities), the server's key, the connection policy, the guest token,
// the dev mock, the account and whether it may write work reports.
func (h *handlers) viewer(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	deviceID := r.PathValue("deviceId")
	w.Header().Set("Cache-Control", "no-store")
	if deviceID == "" || deviceID == "login" || !deviceIDPattern.MatchString(deviceID) {
		kit.JSON(w, r, http.StatusBadRequest, viewerFailure{Code: "invalid_id", Error: "Invalid device ID"})
		return
	}
	g := grantsOf(r)
	target := strings.HasPrefix(deviceID, "rt_")
	var device *goapi.Object
	if target {
		device = h.d.GoAPI.GetRemoteTarget(ctx, deviceID)
	} else if h.src.relay != nil {
		// A failed lookup leaves the viewer without the device's name
		if found, err := h.src.relay.Device(ctx, deviceID); err == nil {
			device = found
		}
	}
	// A remote target's protocol and settings come from its record:
	// without one there is nothing to connect to
	if target && device == nil {
		kit.JSON(w, r, http.StatusNotFound, viewerFailure{Code: "not_found", Error: kit.T(r, "errors.not_found_message", nil)})
		return
	}

	caps := capabilities{GuestPeerIDs: []any{}}
	var peer *goapi.Object
	if target {
		peer = device
	} else {
		peer = h.d.GoAPI.GetPeer(ctx, deviceID)
	}
	if peer != nil {
		readPeerType(peer, target, &caps)
	}

	forced := ""
	if value, ok := queryText(reqquery.Of(r), "transport", ""); ok {
		forced = jsbind.ToLower(value)
	} else {
		abort()
	}
	switch {
	case target:
		protocol, ok := guestaccess.JSText(orValue(device.Get("remote_protocol"), orValue(device.Get("protocol"), "")))
		if !ok {
			abort()
		}
		caps.Transport = jsbind.ToLower(protocol)
	case forced == "mesh" || forced == "cdap" || forced == "rd":
		caps.Transport = forced
	case caps.meshAgent && caps.MeshConnected:
		caps.Transport = "mesh"
	case caps.OSAgent || caps.CDAPConnected:
		caps.Transport = "cdap"
	default:
		caps.Transport = "rd"
	}
	if peer != nil && goapi.Truthy(peer.Get("device_type")) {
		text, ok := guestaccess.JSText(peer.Get("device_type"))
		if !ok {
			abort()
		}
		caps.DeviceType = text
	}
	caps.MeshShare = g.hasShare
	caps.MeshViewOnly = goapi.Truthy(goapi.Dig(g.meshShare, "view_only"))
	caps.RemoteTarget = target
	if target {
		caps.RemoteProtocol = caps.Transport
		caps.RemoteTargetConfig = goapi.NewObject(
			"id", device.Get("id"),
			"name", orValue(device.Get("name"), orValue(device.Get("display_name"), "")),
			"username", orValue(device.Get("username"), ""),
			"credential_mode", orValue(device.Get("credential_mode"), "prompt"),
			"tls_mode", orValue(device.Get("tls_mode"), "preferred"),
			"cert_fingerprint", orValue(device.Get("cert_fingerprint"), ""),
		)
	}
	if g.guest != nil {
		caps.GuestAccess = true
		caps.GuestViewOnly = g.guest.ViewOnly()
		if ids, ok := goapi.Dig(g.guest.Data, "peer_ids").([]any); ok {
			caps.GuestPeerIDs = ids
		}
	}

	key := h.serverKey(ctx)
	connection := h.connectionSnapshot(ctx)
	guestToken := g.guestToken
	if guestToken == "" {
		token, err := h.d.GuestAccess.TokenFromQuery(r)
		if err != nil {
			abort()
		}
		guestToken = token
	}
	mock, ok := h.mockFor(r, deviceID)
	if !ok {
		abort()
	}

	answer := viewerAnswer{
		Success:      true,
		DeviceID:     deviceID,
		Device:       viewerDevice{Name: "", Platform: ""},
		Capabilities: caps,
		ServerPubKey: key,
		Connection:   connection,
		GuestToken:   guestToken,
		MockRemote:   mock,
		CSRFToken:    kit.CSRFToken(r),
	}
	if device != nil {
		name := orValue(device.Get("display_name"), device.Get("hostname"))
		if !goapi.Truthy(name) {
			name = ""
			if target {
				name = orValue(device.Get("name"), "")
			}
		}
		answer.Device = viewerDevice{Name: name, Platform: orValue(device.Get("platform"), orValue(device.Get("os"), ""))}
	}
	if user := kit.User(r); user != nil {
		answer.User = &viewerUser{ID: user.ID, Name: user.Username}
		answer.WorkReports = kit.HasPermission(r, "billing.reports")
	}
	kit.JSON(w, r, http.StatusOK, answer)
}

// readPeerType reads what kind of device the API server says it is. A
// type that cannot be turned into text stops the reading where it stood,
// as the old route's guarded block did.
func readPeerType(peer *goapi.Object, target bool, caps *capabilities) {
	deviceType, ok := guestaccess.JSText(orValue(peer.Get("device_type"), ""))
	if !ok {
		return
	}
	deviceType = jsbind.ToLower(deviceType)
	caps.OSAgent = !target && deviceType == "os_agent"
	caps.CDAPConnected = goapi.Truthy(peer.Get("cdap_connected"))
	caps.meshAgent = !target && deviceType == "mesh_agent"
	caps.MeshConnected = goapi.Truthy(peer.Get("mesh_connected"))
}

// orValue is JavaScript's a || b.
func orValue(a, b any) any {
	if goapi.Truthy(a) {
		return a
	}
	return b
}

// serverKey is the server's public key for the viewer, "" when there is
// none or it cannot be read.
func (h *handlers) serverKey(ctx context.Context) string {
	key, err := h.src.serverKey(ctx)
	if err != nil {
		h.log.Warn("Warning: Could not resolve server public key:", err.Error())
		return ""
	}
	return key
}

// The connection policy the viewer starts from.
var defaultConnection = []any{
	"p2p_first", true,
	"always_use_relay", false,
	"p2p_fallback_ms", float64(2000),
	"same_nat_relay", true,
	"allow_shared_nat_initiator", false,
	"relay_servers", "",
}

// connectionSnapshot is the server's connection policy for the viewer's
// timeouts: the "connection" block of the server's health, else the
// connection mode of the settings, else the defaults.
func (h *handlers) connectionSnapshot(ctx context.Context) *goapi.Object {
	health := h.d.GoAPI.GetHealth(ctx)
	if block, ok := health.Get("connection").(*goapi.Object); ok && block != nil {
		snapshot := goapi.NewObject(defaultConnection...)
		for _, k := range block.Keys() {
			snapshot.Set(k, block.Get(k))
		}
		return snapshot
	}
	if list, ok := health.Get("connection").([]any); ok {
		snapshot := goapi.NewObject(defaultConnection...)
		for i, item := range list {
			snapshot.Set(itoa(i), item)
		}
		return snapshot
	}
	mode, err := h.src.connectionMode(ctx)
	if err != nil {
		h.log.Debug("Connection snapshot from settings failed:", err.Error())
		return goapi.NewObject(defaultConnection...)
	}
	fallback := float64(2000)
	if ms := float64(mode.P2PFallbackMs); ms >= 0 {
		fallback = ms
	}
	return goapi.NewObject(defaultConnection...).
		Set("p2p_first", mode.Mode != serverinfo.ModeRelayOnly).
		Set("always_use_relay", mode.Mode == serverinfo.ModeRelayOnly).
		Set("p2p_fallback_ms", fallback).
		Set("same_nat_relay", mode.SameNATRelay).
		Set("allow_shared_nat_initiator", mode.AllowSharedNATInitiator)
}

func itoa(i int) string {
	const digits = "0123456789"
	if i < 10 {
		return digits[i : i+1]
	}
	return itoa(i/10) + digits[i%10:i%10+1]
}
