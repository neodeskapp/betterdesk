package chat

import (
	"context"

	"github.com/unitronix/betterdesk-server/console/access"
	"github.com/unitronix/betterdesk-server/console/devicegroups"
	"github.com/unitronix/betterdesk-server/console/goapi"
	"github.com/unitronix/betterdesk-server/console/kit"
	"github.com/unitronix/betterdesk-server/console/session"
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
	// Of is the account's scope over a list of devices, nil without a
	// limit (devicegroups.Service.DeviceScopeForUser).
	Of func(ctx context.Context, user *session.User, devices []*goapi.Object) (*access.DeviceScope, error)
}

// ScopeOf is the scope of the device routes, from the console's device
// layer and device groups.
func ScopeOf(d *kit.Deps) Scope {
	return Scope{
		Device: func(ctx context.Context, id string) *goapi.Object { return d.Backend.GetDeviceByID(ctx, id, true) },
		Of:     d.DeviceGroups.DeviceScopeForUser,
	}
}

// device is the device of id, nil when there is none or the scope has no
// limits.
func (s Scope) device(ctx context.Context, id string) *goapi.Object {
	if s.Of == nil || s.Device == nil {
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
