package access

import "github.com/neodeskapp/neodesk/neodesk-server/auth"

// PermissionNeed is a permission another one needs to be of any use, with
// a key that says why. The permissions page shows "Needs View devices"
// under a ticked permission whose needed one is off.
//
// Reasons:
//   - devices_page: the permission is only used on All devices and in the
//     device panel. All devices is shown with device.view (NavSections),
//     lists from GET /api/devices (device.view), and the panel opens a
//     device with GET /api/devices/{id} (device.view).
//   - users_page: only used on Users, which shows its content with
//     user.view and lists from GET /api/users (user.view).
//   - billing_page: only used on Billing, shown with billing.view.
//   - signin_use_connects: the server refuses to use a saved sign-in
//     without device.connect as well.
//   - cdap_terminal_command: the permission is only checked inside the
//     command route, which needs cdap.command.
type PermissionNeed struct {
	Permission string `json:"permission"`
	Reason     string `json:"reason"`
}

// permissionNeeds lists, for a permission, the permissions without which
// it does nothing. A permission is only listed when every use the code
// has for it sits behind the needed one; a permission that can be reached
// through either of two (chat.assign: chat.support or chat.team) is left
// out, since the list means all of them. The evidence is beside each
// entry; paths are under console/routes and web-ui/src.
var permissionNeeds = map[string][]PermissionNeed{
	// guest/routes.go: the links are made and listed with guest.create
	// alone, but the only way to them is GuestLinkDialog, opened from the
	// device actions (lib/devices/actions.svelte.ts, ask.svelte.ts) on All
	// devices and in the device panel
	auth.PermGuestCreate: {{auth.PermDeviceView, "devices_page"}},
	// devices/routes.go, folders/routes.go: every device.edit route acts
	// on a device or folder of All devices (lib/devices/panel-logic.ts,
	// routes/(console)/devices/+page.svelte). tickets/routes.go also lets
	// device.edit write tickets, but the Tickets page needs device.view
	// too; only the chat's "New ticket" menu item works without it
	auth.PermDeviceEdit: {{auth.PermDeviceView, "devices_page"}},
	// devices/routes.go DELETE /api/devices/{id}, restore, bulk-delete;
	// buttons in lib/devices/panel-logic.ts and on All devices
	auth.PermDeviceDelete: {{auth.PermDeviceView, "devices_page"}},
	// devices/routes.go ban/unban; lib/devices/panel-logic.ts
	auth.PermDeviceBan: {{auth.PermDeviceView, "devices_page"}},
	// devices/routes.go change-id; lib/devices/panel-logic.ts
	auth.PermDeviceChangeID: {{auth.PermDeviceView, "devices_page"}},
	// devices/routes.go connection-mode; lib/devices/panel-logic.ts
	auth.PermDeviceConnectionMode: {{auth.PermDeviceView, "devices_page"}},
	// devices/signin.go signinUseAnswer: signinGuard(..., signinUse,
	// auth.PermDeviceConnect), and may_use in the status needs both
	auth.PermDeviceSigninUse: {{auth.PermDeviceConnect, "signin_use_connects"}},
	// devices/signin.go set/change/clear take /api/devices/{id}/signin;
	// the form is in the device panel (lib/devices/panel-logic.ts)
	auth.PermDeviceSigninManage: {{auth.PermDeviceView, "devices_page"}},
	// Remote targets are rows of GET /api/devices (devices/list.go); the
	// console calls no list of its own (web-ui/src/lib/api/remoteTargets.ts
	// has none), and their actions are in lib/devices/actions.svelte.ts and
	// panel-logic.ts
	auth.PermRemoteTargetConnect: {{auth.PermDeviceView, "devices_page"}},
	auth.PermRemoteTargetEdit:    {{auth.PermDeviceView, "devices_page"}},
	auth.PermRemoteTargetDelete:  {{auth.PermDeviceView, "devices_page"}},
	auth.PermRemoteTargetTest:    {{auth.PermDeviceView, "devices_page"}},
	// users/routes.go POST /api/users, /api/users/invite, DELETE
	// /api/users/{id}; the buttons are on Users (routes/(console)/users,
	// allowed = can('user.view')). user.edit is left out: it also changes
	// user groups from All devices (FolderDialog, GroupSheet), which
	// GET /api/panel/user-groups lets device.edit read
	auth.PermUserCreate: {{auth.PermUserView, "users_page"}},
	auth.PermUserDelete: {{auth.PermUserView, "users_page"}},
	// cdap/routes.go: cdap.terminal is checked in the command handler
	// only, which sits behind RequirePermission("cdap.command")
	auth.PermCDAPTerminal: {{auth.PermCDAPCommand, "cdap_terminal_command"}},
	// meshcentral/routes.go /api/mesh/devices/{id}/...; the buttons are in
	// lib/devices/actions.svelte.ts and panel-logic.ts
	auth.PermMeshTerminal: {{auth.PermDeviceView, "devices_page"}},
	auth.PermMeshFiles:    {{auth.PermDeviceView, "devices_page"}},
	auth.PermMeshPower:    {{auth.PermDeviceView, "devices_page"}},
	// lib/devices/panel-actions.svelte.ts and panel-logic.ts: the file
	// browser of the device panel
	auth.PermAgentFiles: {{auth.PermDeviceView, "devices_page"}},
	// commercialization/routes.go: the two export routes; the button is on
	// Billing only (routes/(console)/billing). billing.manage and
	// billing.reports are left out: All devices offers billing for a
	// device group with billing.manage, and the remote viewer work reports
	// with billing.reports (remote/viewer.go)
	auth.PermBillingExport: {{auth.PermBillingView, "billing_page"}},
}

// PermissionNeeds returns a copy of the table: for each permission that
// needs others, the permissions it needs, each with its reason.
func PermissionNeeds() map[string][]PermissionNeed {
	out := make(map[string][]PermissionNeed, len(permissionNeeds))
	for p, needs := range permissionNeeds {
		out[p] = append([]PermissionNeed(nil), needs...)
	}
	return out
}
