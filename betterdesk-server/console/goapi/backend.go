package goapi

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/unitronix/betterdesk-server/logging"
)

// Store is what Backend reads and writes in the console's own tables.
type Store interface {
	// GetStats returns the device counts from the console's tables.
	GetStats(ctx context.Context) (any, error)
	// GetAllFolderAssignments returns the folder of each device that has
	// one, by device id.
	GetAllFolderAssignments(ctx context.Context) (map[string]any, error)
	// GetAllPeerSysinfo returns the system information rows, each with
	// peer_id, hostname, platform, os_full and version.
	GetAllPeerSysinfo(ctx context.Context) ([]*Object, error)
	// UpdateDevice writes the fields of data to the console's device row.
	UpdateDevice(ctx context.Context, id string, data *Object) error
	// SetSetting stores a console setting.
	SetSetting(ctx context.Context, key, value string) error
}

// Backend is the device layer most console routes use: the calls of Client
// with what the console's own tables add to them (folders, system
// information, a second copy of notes).
type Backend struct {
	api   *Client
	store Store
}

// NewBackend returns the device layer on top of api and store.
func NewBackend(api *Client, store Store) *Backend {
	return &Backend{api: api, store: store}
}

// GetActiveBackend returns "betterdesk", the only backend.
func (b *Backend) GetActiveBackend() string { return "betterdesk" }

// SetActiveBackend stores the backend name. Only "betterdesk" is accepted.
func (b *Backend) SetActiveBackend(ctx context.Context, name string) error {
	if name != "betterdesk" {
		return fmt.Errorf("Invalid backend: %s. Only 'betterdesk' is supported.", name)
	}
	return b.store.SetSetting(ctx, "server_backend", name)
}

// IsBetterDesk reports true: the Go server is the only backend.
func (b *Backend) IsBetterDesk() bool { return true }

// GetHealth is Client.GetHealth.
func (b *Backend) GetHealth(ctx context.Context) *Object { return b.api.GetHealth(ctx) }

// GetStats returns {total, online, offline, banned, withNotes} from
// Client.GetServerStats, or the counts of the console's own tables when
// that call fails.
func (b *Backend) GetStats(ctx context.Context) (any, error) {
	result := b.api.GetServerStats(ctx)
	if d := result.Get("data"); result.Bool("success") && Truthy(d) {
		total := firstSet(float64(0), prop(d, "peers_total"), prop(d, "total_peers"), prop(d, "total"))
		online := firstSet(float64(0), prop(d, "peers_online"), prop(d, "peers_online_live"), prop(d, "online_peers"), prop(d, "online"))
		return NewObject(
			"total", total,
			"online", online,
			"offline", jsNumber(total)-jsNumber(online),
			"banned", firstSet(float64(0), prop(d, "peers_banned"), prop(d, "banned_peers"), prop(d, "banned")),
			"withNotes", firstSet(float64(0), prop(d, "with_notes")),
		), nil
	}
	stats, err := b.store.GetStats(ctx)
	if err != nil {
		return nil, fmt.Errorf("device counts from the console tables: %w", err)
	}
	return stats, nil
}

// firstSet returns the first value that is not null, or fallback.
func firstSet(fallback any, values ...any) any {
	for _, v := range values {
		if v != nil {
			return v
		}
	}
	return fallback
}

// GetServerInfo is Client.GetServerInfo.
func (b *Backend) GetServerInfo(ctx context.Context) *Object { return b.api.GetServerInfo(ctx) }

// DeviceFilters select and order the devices of GetAllDevices.
type DeviceFilters struct {
	// IncludeDeleted adds deleted peers and disabled remote targets.
	IncludeDeleted bool
	// Search keeps devices whose id, username, hostname or note contains
	// the text, whatever the case.
	Search string
	// Status keeps "online", "offline" (not online and not banned) or
	// "banned" devices. Anything else keeps all.
	Status string
	// HasNotes keeps devices with a note.
	HasNotes bool
	// SortBy is the field to order by, last_online when empty.
	SortBy string
	// SortOrder is "asc" for ascending. Anything else is descending.
	SortOrder string
}

// GetAllDevices returns the peers (Client.GetAllPeers) followed by the
// remote targets (Client.GetAllRemoteTargets), with folder_id from the
// console's folder assignments, with hostname, platform, os and version
// filled in from the console's system information where the API server has
// none, filtered and ordered by filters. The order compares the fields as
// text.
func (b *Backend) GetAllDevices(ctx context.Context, filters DeviceFilters) []*Object {
	peers := b.api.GetAllPeers(ctx, filters.IncludeDeleted)
	peers = append(peers, b.api.GetAllRemoteTargets(ctx, "", filters.IncludeDeleted)...)

	if assignments, err := b.store.GetAllFolderAssignments(ctx); err != nil {
		logging.Logf("goapi", "failed to overlay folder assignments: %v", err)
	} else {
		for _, peer := range peers {
			overlayFolder(peer, assignments)
		}
	}

	if rows, err := b.store.GetAllPeerSysinfo(ctx); err != nil {
		logging.Logf("goapi", "failed to overlay sysinfo: %v", err)
	} else {
		byPeer := make(map[string]*Object, len(rows))
		for _, row := range rows {
			byPeer[jsString(row.Get("peer_id"))] = row
		}
		for _, peer := range peers {
			if peer == nil {
				continue
			}
			si := byPeer[jsString(peer.Get("id"))]
			if si == nil {
				continue
			}
			if !peer.Bool("hostname") && si.Bool("hostname") {
				peer.Set("hostname", si.Get("hostname"))
			}
			if (!peer.Bool("platform") || peer.Get("platform") == "-") && si.Bool("platform") {
				peer.Set("platform", si.Get("platform"))
			}
			if (!peer.Bool("os") || peer.Get("os") == "-") && si.Bool("os_full") {
				peer.Set("os", si.Get("os_full"))
			}
			if !peer.Bool("version") && si.Bool("version") {
				peer.Set("version", si.Get("version"))
			}
		}
	}

	keep := func(match func(p *Object) bool) {
		kept := make([]*Object, 0, len(peers))
		for _, p := range peers {
			if match(p) {
				kept = append(kept, p)
			}
		}
		peers = kept
	}
	if filters.Search != "" {
		needle := strings.ToLower(filters.Search)
		keep(func(p *Object) bool {
			for _, field := range []string{"id", "username", "hostname", "note"} {
				if s := p.Str(field); s != "" && strings.Contains(strings.ToLower(s), needle) {
					return true
				}
			}
			return false
		})
	}
	switch filters.Status {
	case "online":
		keep(func(p *Object) bool { return p.Bool("online") })
	case "offline":
		keep(func(p *Object) bool { return !p.Bool("online") && !p.Bool("banned") })
	case "banned":
		keep(func(p *Object) bool { return p.Bool("banned") })
	}
	if filters.HasNotes {
		keep(func(p *Object) bool { return jsTrim(p.Str("note")) != "" })
	}

	column := filters.SortBy
	if column == "" {
		column = "last_online"
	}
	ascending := filters.SortOrder == "asc"
	order := newCollator()
	sort.SliceStable(peers, func(i, j int) bool {
		a := jsString(or(peers[i].Get(column), ""))
		z := jsString(or(peers[j].Get(column), ""))
		if ascending {
			return order.compare(a, z) < 0
		}
		return order.compare(z, a) < 0
	})
	return peers
}

// overlayFolder sets folder_id from the console's assignment of the device,
// as a number when the stored value is one.
func overlayFolder(peer *Object, assignments map[string]any) {
	if peer == nil {
		return
	}
	raw, ok := assignments[jsString(peer.Get("id"))]
	if !ok {
		return
	}
	if n := jsNumber(raw); !math.IsNaN(n) && !math.IsInf(n, 0) {
		peer.Set("folder_id", n)
		return
	}
	peer.Set("folder_id", raw)
}

// GetDeviceByID returns one device: the remote target for an id that starts
// with "rt_" (Client.GetRemoteTarget), else the peer (Client.GetPeer, or
// Client.GetPeerIncludingDeleted when includeDeleted is set) with folder_id
// from the console's folder assignments. Nil when there is none, and for
// every failed call.
func (b *Backend) GetDeviceByID(ctx context.Context, id string, includeDeleted bool) *Object {
	if strings.HasPrefix(id, "rt_") {
		return b.api.GetRemoteTarget(ctx, id)
	}
	var peer *Object
	if includeDeleted {
		peer = b.api.GetPeerIncludingDeleted(ctx, id)
	} else {
		peer = b.api.GetPeer(ctx, id)
	}
	if peer != nil {
		if assignments, err := b.store.GetAllFolderAssignments(ctx); err == nil {
			overlayFolder(peer, assignments)
		}
	}
	return peer
}

// DeleteDevice is Client.DeletePeer.
func (b *Backend) DeleteDevice(ctx context.Context, id string, opts DeleteOptions) *Object {
	return b.api.DeletePeer(ctx, id, opts)
}

// SetBanStatus is Client.BanPeer when banned is set, else Client.UnbanPeer.
func (b *Backend) SetBanStatus(ctx context.Context, id string, banned bool, reason string) *Object {
	if banned {
		return b.api.BanPeer(ctx, id, reason)
	}
	return b.api.UnbanPeer(ctx, id)
}

// RestoreDevice is Client.RestorePeer.
func (b *Backend) RestoreDevice(ctx context.Context, id string) *Object {
	return b.api.RestorePeer(ctx, id)
}

// UpdateDevice sends the note, user and display_name of data that are set
// to the API server (Client.UpdatePeer), as text, and then writes data to
// the console's own device row. It returns {changes:1}, or {changes:0,
// error} when the API server refused; a failed write to the console's row
// is ignored.
func (b *Backend) UpdateDevice(ctx context.Context, id string, data *Object) *Object {
	fields := NewObject()
	for _, key := range []string{"note", "user", "display_name"} {
		if data.Has(key) {
			fields.Set(key, jsString(data.Get(key)))
		}
	}
	if fields.Len() > 0 {
		result := b.api.UpdatePeer(ctx, id, fields)
		if !result.Bool("success") {
			return NewObject("changes", float64(0), "error", or(result.Get("error"), "Failed to update peer"))
		}
	}
	if err := b.store.UpdateDevice(ctx, id, data); err != nil {
		logging.Logf("goapi", "update of the console's device row failed: %v", err)
	}
	return NewObject("changes", float64(1))
}

// ChangePeerID is Client.ChangePeerID.
func (b *Backend) ChangePeerID(ctx context.Context, oldID, newID string) *Object {
	return b.api.ChangePeerID(ctx, oldID, newID)
}

// SyncOnlineStatus is Client.SyncOnlineStatus: no call is made.
func (b *Backend) SyncOnlineStatus() *Object { return b.api.SyncOnlineStatus() }

// GetStatusSummary is Client.GetStatusSummary.
func (b *Backend) GetStatusSummary(ctx context.Context) *Object { return b.api.GetStatusSummary(ctx) }

// GetBlocklist is Client.GetBlocklist.
func (b *Backend) GetBlocklist(ctx context.Context) *Object { return b.api.GetBlocklist(ctx) }

// AddBlocklistEntry is Client.AddBlocklistEntry.
func (b *Backend) AddBlocklistEntry(ctx context.Context, entry any) *Object {
	return b.api.AddBlocklistEntry(ctx, entry)
}

// RemoveBlocklistEntry is Client.RemoveBlocklistEntry.
func (b *Backend) RemoveBlocklistEntry(ctx context.Context, entry string) *Object {
	return b.api.RemoveBlocklistEntry(ctx, entry)
}

// SetPeerTags is Client.SetPeerTags.
func (b *Backend) SetPeerTags(ctx context.Context, id string, tags any) *Object {
	return b.api.SetPeerTags(ctx, id, tags)
}

// GetPeersByTag is Client.GetPeersByTag.
func (b *Backend) GetPeersByTag(ctx context.Context, tag string) []*Object {
	return b.api.GetPeersByTag(ctx, tag)
}

// GetAuditEvents is Client.GetAuditEvents.
func (b *Backend) GetAuditEvents(ctx context.Context, limit any) *Object {
	return b.api.GetAuditEvents(ctx, limit)
}
