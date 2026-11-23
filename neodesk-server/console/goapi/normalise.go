package goapi

import (
	"math"
	"strconv"
	"strings"
	"time"
)

func itoa(i int) string { return strconv.Itoa(i) }

// noSignalWindow is how long after its last contact an offline device is
// shown as "no signal" instead of offline.
const noSignalWindow = 5 * time.Minute

// NormalisePeer maps a peer as the API server sends it to the shape the
// console works with. The API server's names are on the left:
//
//	user                      -> username
//	os                        -> platform and os
//	live_online               -> online
//	live_status, status_text  -> status_tier
//	tags ("a,b" or a list)    -> tags (a list)
//	disabled or soft_deleted  -> disabled
//
// device_type and client_type default to "rustdesk", missing texts become
// "", banned_at, folder_id and deleted_at become null when empty, and
// no_signal is true for a device that is offline, not banned and was seen
// in the last five minutes. pk, status, status_text and the API server's
// own platform are dropped. A null peer stays null.
func NormalisePeer(peer any) any {
	if o := normalisePeer(peer, time.Now()); o != nil {
		return o
	}
	return nil
}

func normalisePeer(peer any, now time.Time) *Object {
	if !Truthy(peer) {
		return nil
	}
	p := func(key string) any { return prop(peer, key) }
	text := func(key string) any { return or(p(key), "") }

	liveOnline := Truthy(p("live_online"))
	banned := Truthy(p("banned"))
	lastOnline := text("last_online")
	deviceType := strings.ToLower(jsTrim(jsString(text("device_type"))))
	if deviceType == "" {
		deviceType = "rustdesk"
	}

	tags := []any{}
	switch t := p("tags").(type) {
	case []any:
		tags = t
	case string:
		if t == "" {
			break
		}
		if parsed, err := Parse([]byte(t)); err == nil {
			if arr, ok := parsed.([]any); ok {
				tags = arr
			} else {
				tags = []any{t}
			}
			break
		}
		for _, part := range strings.Split(t, ",") {
			if part = jsTrim(part); part != "" {
				tags = append(tags, part)
			}
		}
	}

	agentConnected := (p("device_type") == "os_agent" && Truthy(p("cdap_connected"))) ||
		(p("device_type") == "mesh_agent" && Truthy(p("mesh_connected")))
	statusTier := p("live_status")
	if !Truthy(statusTier) {
		statusTier = p("status_text")
	}
	if !Truthy(statusTier) {
		statusTier = "offline"
		if liveOnline {
			statusTier = "online"
		}
	}

	out := NewObject(
		"id", text("id"),
		"hostname", text("hostname"),
		"display_name", text("display_name"),
		"username", text("user"),
		"platform", text("os"),
		"os", text("os"),
		"version", text("version"),
		"ip", text("ip"),
		"note", text("note"),
		"online", liveOnline,
		"signal_ready", p("signal_ready") != false && liveOnline,
		"banned", banned,
		"no_signal", !liveOnline && !banned && seenRecently(lastOnline, now) && !agentConnected,
		"created_at", text("created_at"),
		"last_online", lastOnline,
		"ban_reason", text("ban_reason"),
		"banned_at", or(p("banned_at"), nil),
		"folder_id", or(p("folder_id"), nil),
		"tags", tags,
		"status_tier", statusTier,
		"uuid", text("uuid"),
		"nat_type", or(p("nat_type"), float64(0)),
		"disabled", Truthy(p("disabled")) || Truthy(p("soft_deleted")),
		"soft_deleted", Truthy(p("soft_deleted")),
		"deleted_at", or(p("deleted_at"), nil),
		"device_type", deviceType,
		"client_type", deviceType,
		"cdap_connected", Truthy(p("cdap_connected")),
		"mesh_connected", Truthy(p("mesh_connected")),
		"mesh_node_id", text("mesh_node_id"),
		"linked_peer_id", text("linked_peer_id"),
	)
	// The device came back with another key, which the server refuses
	if Truthy(p("key_refused")) {
		out.Set("key_refused", true)
	}
	// A new key it brought to an invite, which an administrator approves
	if req, ok := p("key_request").(*Object); ok {
		out.Set("key_request", req)
	}
	return out
}

// normalisePeers maps a list of peers. A null entry stays null.
func normalisePeers(peers []any, now time.Time) []*Object {
	out := make([]*Object, len(peers))
	for i, p := range peers {
		out[i] = normalisePeer(p, now)
	}
	return out
}

// seenRecently reports whether lastOnline is a time less than five minutes
// before now.
func seenRecently(lastOnline any, now time.Time) bool {
	if !Truthy(lastOnline) {
		return false
	}
	ms, ok := jsDate(lastOnline)
	if !ok {
		return false
	}
	return float64(now.UnixMilli())-ms < float64(noSignalWindow.Milliseconds())
}

// zonedLayouts carry their own offset; localLayouts are read in the
// server's time zone; dateLayouts are UTC. This is how JavaScript's Date
// reads the same texts.
var (
	zonedLayouts = []string{
		"2006-01-02T15:04:05.999999999Z07:00",
		"2006-01-02T15:04Z07:00",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04Z07:00",
	}
	localLayouts = []string{
		"2006-01-02T15:04:05.999999999",
		"2006-01-02T15:04",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04",
	}
	dateLayouts = []string{"2006-01-02", "2006-01", "2006"}
)

// jsDate is new Date(v).getTime() for the values a peer's last_online can
// hold: milliseconds as a number, or a date and time as text.
func jsDate(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		if math.IsNaN(t) || math.Abs(t) > 8.64e15 {
			return 0, false
		}
		return math.Trunc(t), true
	case bool:
		return 1, true
	case string:
		s := jsTrim(t)
		for _, layout := range zonedLayouts {
			if parsed, err := time.Parse(layout, s); err == nil {
				return float64(parsed.UnixMilli()), true
			}
		}
		for _, layout := range localLayouts {
			if parsed, err := time.ParseInLocation(layout, s, time.Local); err == nil {
				return float64(parsed.UnixMilli()), true
			}
		}
		for _, layout := range dateLayouts {
			if parsed, err := time.Parse(layout, s); err == nil {
				return float64(parsed.UnixMilli()), true
			}
		}
	}
	return 0, false
}

// normaliseRemoteTarget maps an RDP or VNC target as the API server sends
// it to a device the console can list next to peers. It keeps every field
// of the target and sets id (as text), hostname and display_name (the name,
// else the host), platform and os, device_type and remote_protocol (the
// protocol in lower case), remote_target, soft_deleted and status (from
// enabled), online (always false), last_online (the last test) and note
// ("host:port").
func normaliseRemoteTarget(target any) *Object {
	if !Truthy(target) {
		target = NewObject()
	}
	p := func(key string) any { return prop(target, key) }
	protocol := strings.ToLower(jsString(or(p("protocol"), "")))
	name := or(p("name"), or(p("host"), ""))
	disabled := p("enabled") == false
	status := "OFFLINE"
	if disabled {
		status = "DISABLED"
	}
	note := ""
	if Truthy(p("host")) {
		note = jsString(p("host")) + ":" + jsString(or(p("port"), ""))
	}

	o := spread(target)
	o.Set("id", jsString(or(p("id"), "")))
	o.Set("hostname", name)
	o.Set("display_name", name)
	o.Set("platform", or(p("platform"), ""))
	o.Set("os", or(p("platform"), ""))
	o.Set("device_type", protocol)
	o.Set("remote_target", true)
	o.Set("remote_protocol", protocol)
	o.Set("soft_deleted", disabled)
	o.Set("online", false)
	o.Set("status", status)
	o.Set("last_online", or(p("last_test_at"), ""))
	o.Set("note", note)
	return o
}
