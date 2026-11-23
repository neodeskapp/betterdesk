package chat

import (
	"context"

	"github.com/neodeskapp/neodesk/neodesk-server/console/access"
	"github.com/neodeskapp/neodesk/neodesk-server/console/devicegroups"
	"github.com/neodeskapp/neodesk/neodesk-server/console/goapi"
	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsjson"
	"github.com/neodeskapp/neodesk/neodesk-server/console/session"
)

// Scope is the device scope of the device routes as the chat applies it.
// A conversation whose id is a device's id is that device's chat: an
// account sees it when the device is in its scope. Any other conversation
// (between operators, of a group, the inbox's own) is no device's, and
// every account with chat.access sees it. The zero Scope has no limits.
type Scope struct {
	// Device is the device of an id, deleted ones included; nil when
	// there is none (goapi.Backend.GetDeviceByID).
	Device func(ctx context.Context, id string) *goapi.Object
	// Devices are every device, deleted ones included, for the lists that
	// name many (goapi.Backend.GetAllDevices).
	Devices func(ctx context.Context) []*goapi.Object
	// Of is the account's scope over a list of devices, nil without a
	// limit (devicegroups.Service.DeviceScopeForUser).
	Of func(ctx context.Context, user *session.User, devices []*goapi.Object) (*access.DeviceScope, error)
}

// ScopeOf is the scope of the device routes, from the console's device
// layer and device groups.
func ScopeOf(d *kit.Deps) Scope {
	return Scope{
		Device: func(ctx context.Context, id string) *goapi.Object { return d.Backend.GetDeviceByID(ctx, id, true) },
		Devices: func(ctx context.Context) []*goapi.Object {
			return d.Backend.GetAllDevices(ctx, goapi.DeviceFilters{IncludeDeleted: true})
		},
		Of: d.DeviceGroups.DeviceScopeForUser,
	}
}

// device is the device of id, nil when there is none or the scope has no
// limits.
func (s Scope) device(ctx context.Context, id string) *goapi.Object {
	if s.Of == nil || s.Device == nil || id == "" {
		return nil
	}
	return s.Device(ctx, id)
}

// reaches reports whether the account sees the device; nil is no device,
// which every account sees. A failed check sees nothing.
func (s Scope) reaches(ctx context.Context, user *session.User, device *goapi.Object) bool {
	if s.Of == nil || device == nil {
		return true
	}
	if user == nil {
		return false
	}
	scope, err := s.Of(ctx, user, []*goapi.Object{device})
	if err != nil {
		return false
	}
	return scope == nil || scope.Has(devicegroups.ScopeDevices([]*goapi.Object{device})[0].ID)
}

// sees reports whether the account sees the conversation id.
func (s Scope) sees(ctx context.Context, user *session.User, id string) bool {
	return s.reaches(ctx, user, s.device(ctx, id))
}

// room reports whether the account may open the room of id: it is a
// device the account sees.
func (s Scope) room(ctx context.Context, user *session.User, id string) bool {
	if s.Of == nil || s.Device == nil {
		return true
	}
	device := s.Device(ctx, id)
	return device != nil && s.reaches(ctx, user, device)
}

// unlimited reports whether the account sees every device. A failed
// check is false.
func (s Scope) unlimited(ctx context.Context, user *session.User) bool {
	if s.Of == nil {
		return true
	}
	if user == nil {
		return false
	}
	scope, err := s.Of(ctx, user, nil)
	return err == nil && scope == nil
}

// view is what an account sees of the devices a list names: the ids of
// every device and the account's scope over them. A nil view sees every
// device.
type view struct {
	known map[string]bool
	scope *access.DeviceScope
	// blind is a view whose check failed: it sees no device and keeps
	// only what names none
	blind bool
}

// viewOf is the account's view of the devices, nil when it sees every
// device.
func (s Scope) viewOf(ctx context.Context, user *session.User) *view {
	if s.unlimited(ctx, user) {
		return nil
	}
	if user == nil || s.Devices == nil {
		return &view{blind: true}
	}
	devices := s.Devices(ctx)
	scope, err := s.Of(ctx, user, devices)
	if err != nil {
		return &view{blind: true}
	}
	known := make(map[string]bool, len(devices))
	for _, device := range devicegroups.ScopeDevices(devices) {
		known[device.ID] = true
	}
	return &view{known: known, scope: scope}
}

// hides reports whether the view hides what names id.
func (v *view) hides(id string) bool {
	switch {
	case v == nil:
		return false
	case v.blind:
		return true
	}
	return v.known[id] && !v.scope.Has(id)
}

// filter writes list as JSON without the items whose text member named by
// one of fields the view hides. A value that is no list is written as it
// is.
func (v *view) filter(list jsjson.Value, fields ...string) []byte {
	if v == nil || list.Kind() != jsjson.Array {
		b, _ := list.MarshalJSON()
		return b
	}
	kept := [][]byte{}
	for _, item := range list.Items() {
		hidden := false
		for _, field := range fields {
			if value := item.Get(field); value.Kind() == jsjson.String && v.hides(value.Str()) {
				hidden = true
			}
		}
		if !hidden {
			b, _ := item.MarshalJSON()
			kept = append(kept, b)
		}
	}
	return array(kept)
}

// contacts is a contact list without the devices the view hides.
func (v *view) contacts(list jsjson.Value) []byte { return v.filter(list, "id") }

// messages is a history without the messages from or to a device the view
// hides.
func (v *view) messages(list jsjson.Value) []byte { return v.filter(list, "from_id", "to_id") }
