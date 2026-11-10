package goapi

import (
	"context"
	"testing"
)

// dispatch calls the methods of Client by the names of the Node client's
// functions, with the arguments a JavaScript caller gave. The request table
// and the comparison with the Node client both go through it.
var dispatch = map[string]func(c *Client, ctx context.Context, a []any) any{
	"getHealth":      func(c *Client, ctx context.Context, a []any) any { return c.GetHealth(ctx) },
	"getServerStats": func(c *Client, ctx context.Context, a []any) any { return c.GetServerStats(ctx) },
	"getServerInfo":  func(c *Client, ctx context.Context, a []any) any { return c.GetServerInfo(ctx) },
	"getAllPeers": func(c *Client, ctx context.Context, a []any) any {
		return c.GetAllPeers(ctx, Truthy(prop(arg(a, 0), "includeDeleted")))
	},
	"getPeerIncludingDeleted": func(c *Client, ctx context.Context, a []any) any { return c.GetPeerIncludingDeleted(ctx, str(a, 0)) },
	"getPeer":                 func(c *Client, ctx context.Context, a []any) any { return c.GetPeer(ctx, str(a, 0)) },
	"deletePeer": func(c *Client, ctx context.Context, a []any) any {
		o := arg(a, 1)
		return c.DeletePeer(ctx, str(a, 0), DeleteOptions{
			Revoke: Truthy(prop(o, "revoke")), Cascade: Truthy(prop(o, "cascade")), Hard: Truthy(prop(o, "hard")),
		})
	},
	"banPeer":           func(c *Client, ctx context.Context, a []any) any { return c.BanPeer(ctx, str(a, 0), str(a, 1)) },
	"unbanPeer":         func(c *Client, ctx context.Context, a []any) any { return c.UnbanPeer(ctx, str(a, 0)) },
	"getConnectionMode": func(c *Client, ctx context.Context, a []any) any { return c.GetConnectionMode(ctx, str(a, 0)) },
	"setConnectionMode": func(c *Client, ctx context.Context, a []any) any {
		return c.SetConnectionMode(ctx, str(a, 0), arg(a, 1))
	},
	"restorePeer":      func(c *Client, ctx context.Context, a []any) any { return c.RestorePeer(ctx, str(a, 0)) },
	"changePeerId":     func(c *Client, ctx context.Context, a []any) any { return c.ChangePeerID(ctx, str(a, 0), str(a, 1)) },
	"getStatusSummary": func(c *Client, ctx context.Context, a []any) any { return c.GetStatusSummary(ctx) },
	"getOnlinePeers":   func(c *Client, ctx context.Context, a []any) any { return c.GetOnlinePeers(ctx) },
	"getPeerStatus":    func(c *Client, ctx context.Context, a []any) any { return c.GetPeerStatus(ctx, str(a, 0)) },
	"getPeerTelemetry": func(c *Client, ctx context.Context, a []any) any { return c.GetPeerTelemetry(ctx, str(a, 0)) },
	"refreshPeerHardware": func(c *Client, ctx context.Context, a []any) any {
		return c.RefreshPeerHardware(ctx, str(a, 0))
	},
	"queuePeerTelemetryCommand": func(c *Client, ctx context.Context, a []any) any {
		return c.QueuePeerTelemetryCommand(ctx, str(a, 0), arg(a, 1), arg(a, 2))
	},
	"getBlocklist":         func(c *Client, ctx context.Context, a []any) any { return c.GetBlocklist(ctx) },
	"addBlocklistEntry":    func(c *Client, ctx context.Context, a []any) any { return c.AddBlocklistEntry(ctx, arg(a, 0)) },
	"removeBlocklistEntry": func(c *Client, ctx context.Context, a []any) any { return c.RemoveBlocklistEntry(ctx, str(a, 0)) },
	"setPeerTags":          func(c *Client, ctx context.Context, a []any) any { return c.SetPeerTags(ctx, str(a, 0), arg(a, 1)) },
	"getPeersByTag":        func(c *Client, ctx context.Context, a []any) any { return c.GetPeersByTag(ctx, str(a, 0)) },
	"updatePeer":           func(c *Client, ctx context.Context, a []any) any { return c.UpdatePeer(ctx, str(a, 0), arg(a, 1)) },
	"getAllRemoteTargets": func(c *Client, ctx context.Context, a []any) any {
		o := arg(a, 0)
		orgID, _ := or(prop(o, "orgId"), "").(string)
		return c.GetAllRemoteTargets(ctx, orgID, Truthy(prop(o, "includeDisabled")))
	},
	"testRemoteTarget":   func(c *Client, ctx context.Context, a []any) any { return c.TestRemoteTarget(ctx, arg(a, 0)) },
	"getRemoteTarget":    func(c *Client, ctx context.Context, a []any) any { return c.GetRemoteTarget(ctx, str(a, 0)) },
	"createRemoteTarget": func(c *Client, ctx context.Context, a []any) any { return c.CreateRemoteTarget(ctx, arg(a, 0)) },
	"updateRemoteTarget": func(c *Client, ctx context.Context, a []any) any {
		return c.UpdateRemoteTarget(ctx, str(a, 0), arg(a, 1))
	},
	"deleteRemoteTarget": func(c *Client, ctx context.Context, a []any) any { return c.DeleteRemoteTarget(ctx, str(a, 0)) },
	"getRemoteTargetCredentialStatus": func(c *Client, ctx context.Context, a []any) any {
		return c.GetRemoteTargetCredentialStatus(ctx, str(a, 0))
	},
	"setRemoteTargetCredentials": func(c *Client, ctx context.Context, a []any) any {
		return c.SetRemoteTargetCredentials(ctx, str(a, 0), arg(a, 1))
	},
	"clearRemoteTargetCredentials": func(c *Client, ctx context.Context, a []any) any {
		return c.ClearRemoteTargetCredentials(ctx, str(a, 0))
	},
	"acceptRemoteTargetCertificate": func(c *Client, ctx context.Context, a []any) any {
		return c.AcceptRemoteTargetCertificate(ctx, str(a, 0), arg(a, 1))
	},
	"getAuditEvents":            func(c *Client, ctx context.Context, a []any) any { return c.GetAuditEvents(ctx, arg(a, 0)) },
	"getClientAuditConnections": func(c *Client, ctx context.Context, a []any) any { return c.GetClientAuditConnections(ctx, arg(a, 0)) },
	"getClientAuditFiles":       func(c *Client, ctx context.Context, a []any) any { return c.GetClientAuditFiles(ctx, arg(a, 0)) },
	"getClientAuditAlarms":      func(c *Client, ctx context.Context, a []any) any { return c.GetClientAuditAlarms(ctx, arg(a, 0)) },
	"getConfig":                 func(c *Client, ctx context.Context, a []any) any { return c.GetConfig(ctx, str(a, 0)) },
	"setConfig":                 func(c *Client, ctx context.Context, a []any) any { return c.SetConfig(ctx, str(a, 0), arg(a, 1)) },
	"getPeerAvailability": func(c *Client, ctx context.Context, a []any) any {
		return c.GetPeerAvailability(ctx, str(a, 0), prop(arg(a, 1), "day"), prop(arg(a, 1), "tzOffset"))
	},
	"getPeerConnectionSessions": func(c *Client, ctx context.Context, a []any) any {
		return c.GetPeerConnectionSessions(ctx, str(a, 0), prop(arg(a, 1), "limit"))
	},
	"syncOnlineStatus":      func(c *Client, ctx context.Context, a []any) any { return c.SyncOnlineStatus() },
	"getMeshStatus":         func(c *Client, ctx context.Context, a []any) any { return c.GetMeshStatus(ctx) },
	"getCDAPStatus":         func(c *Client, ctx context.Context, a []any) any { return c.GetCDAPStatus(ctx) },
	"getCDAPDevices":        func(c *Client, ctx context.Context, a []any) any { return c.GetCDAPDevices(ctx) },
	"getCDAPDeviceInfo":     func(c *Client, ctx context.Context, a []any) any { return c.GetCDAPDeviceInfo(ctx, str(a, 0)) },
	"getCDAPDeviceManifest": func(c *Client, ctx context.Context, a []any) any { return c.GetCDAPDeviceManifest(ctx, str(a, 0)) },
	"getCDAPDeviceState":    func(c *Client, ctx context.Context, a []any) any { return c.GetCDAPDeviceState(ctx, str(a, 0)) },
	"sendCDAPCommand": func(c *Client, ctx context.Context, a []any) any {
		return c.SendCDAPCommand(ctx, str(a, 0), arg(a, 1), arg(a, 2), arg(a, 3), arg(a, 4))
	},
	"getCDAPAlerts":     func(c *Client, ctx context.Context, a []any) any { return c.GetCDAPAlerts(ctx, arg(a, 0)) },
	"getLinkedPeers":    func(c *Client, ctx context.Context, a []any) any { return c.GetLinkedPeers(ctx, str(a, 0)) },
	"linkDevice":        func(c *Client, ctx context.Context, a []any) any { return c.LinkDevice(ctx, str(a, 0), arg(a, 1)) },
	"listDeviceTokens":  func(c *Client, ctx context.Context, a []any) any { return c.ListDeviceTokens(ctx, Truthy(arg(a, 0))) },
	"createDeviceToken": func(c *Client, ctx context.Context, a []any) any { return c.CreateDeviceToken(ctx, arg(a, 0)) },
	"getDeviceToken":    func(c *Client, ctx context.Context, a []any) any { return c.GetDeviceToken(ctx, id(a, 0)) },
	"updateDeviceToken": func(c *Client, ctx context.Context, a []any) any {
		return c.UpdateDeviceToken(ctx, id(a, 0), arg(a, 1))
	},
	"revokeDeviceToken": func(c *Client, ctx context.Context, a []any) any { return c.RevokeDeviceToken(ctx, id(a, 0)) },
	"bulkGenerateTokens": func(c *Client, ctx context.Context, a []any) any {
		return c.BulkGenerateTokens(ctx, arg(a, 0))
	},
	"bindTokenToPeer":      func(c *Client, ctx context.Context, a []any) any { return c.BindTokenToPeer(ctx, id(a, 0), id(a, 1)) },
	"getEnrollmentMode":    func(c *Client, ctx context.Context, a []any) any { return c.GetEnrollmentMode(ctx) },
	"setEnrollmentMode":    func(c *Client, ctx context.Context, a []any) any { return c.SetEnrollmentMode(ctx, arg(a, 0)) },
	"getEnrollmentPending": func(c *Client, ctx context.Context, a []any) any { return c.GetEnrollmentPending(ctx) },
	"getEnrollmentHistory": func(c *Client, ctx context.Context, a []any) any { return c.GetEnrollmentHistory(ctx, arg(a, 0)) },
	"approveEnrollment": func(c *Client, ctx context.Context, a []any) any {
		return c.ApproveEnrollment(ctx, str(a, 0), arg(a, 1), arg(a, 2), arg(a, 3))
	},
	"rejectEnrollment": func(c *Client, ctx context.Context, a []any) any {
		return c.RejectEnrollment(ctx, str(a, 0), Truthy(arg(a, 1)))
	},
	"clearEnrollmentRejection": func(c *Client, ctx context.Context, a []any) any {
		return c.ClearEnrollmentRejection(ctx, str(a, 0))
	},
	"createPairingCode":  func(c *Client, ctx context.Context, a []any) any { return c.CreatePairingCode(ctx, arg(a, 0)) },
	"listPairingCodes":   func(c *Client, ctx context.Context, a []any) any { return c.ListPairingCodes(ctx) },
	"revokePairingCode":  func(c *Client, ctx context.Context, a []any) any { return c.RevokePairingCode(ctx, str(a, 0)) },
	"getBranding":        func(c *Client, ctx context.Context, a []any) any { return c.GetBranding(ctx) },
	"saveBranding":       func(c *Client, ctx context.Context, a []any) any { return c.SaveBranding(ctx, arg(a, 0)) },
	"getAccessPolicy":    func(c *Client, ctx context.Context, a []any) any { return c.GetAccessPolicy(ctx, id(a, 0)) },
	"saveAccessPolicy":   func(c *Client, ctx context.Context, a []any) any { return c.SaveAccessPolicy(ctx, id(a, 0), arg(a, 1)) },
	"deleteAccessPolicy": func(c *Client, ctx context.Context, a []any) any { return c.DeleteAccessPolicy(ctx, id(a, 0)) },
	"getPeerConnectPassword": func(c *Client, ctx context.Context, a []any) any {
		data, err := c.GetPeerConnectPassword(ctx, id(a, 0))
		if err != nil {
			return thrown{AsError(err)}
		}
		return data
	},
	"listRoles":          func(c *Client, ctx context.Context, a []any) any { return c.ListRoles(ctx) },
	"getRolePermissions": func(c *Client, ctx context.Context, a []any) any { return c.GetRolePermissions(ctx, id(a, 0)) },
	"listRolePermissionOverrides": func(c *Client, ctx context.Context, a []any) any {
		role, _ := or(arg(a, 0), "").(string)
		return c.ListRolePermissionOverrides(ctx, role)
	},
	"setRolePermission": func(c *Client, ctx context.Context, a []any) any {
		return c.SetRolePermission(ctx, id(a, 0), id(a, 1), arg(a, 2))
	},
	"deleteRolePermission": func(c *Client, ctx context.Context, a []any) any {
		return c.DeleteRolePermission(ctx, id(a, 0), id(a, 1))
	},
	"listHelpRequests":       func(c *Client, ctx context.Context, a []any) any { return c.ListHelpRequests(ctx, arg(a, 0)) },
	"acknowledgeHelpRequest": func(c *Client, ctx context.Context, a []any) any { return c.AcknowledgeHelpRequest(ctx, id(a, 0)) },
	"resolveHelpRequest":     func(c *Client, ctx context.Context, a []any) any { return c.ResolveHelpRequest(ctx, id(a, 0)) },
	"getLDAPConfig":          func(c *Client, ctx context.Context, a []any) any { return c.GetLDAPConfig(ctx) },
	"saveLDAPConfig":         func(c *Client, ctx context.Context, a []any) any { return c.SaveLDAPConfig(ctx, arg(a, 0)) },
	"testLDAPConnection":     func(c *Client, ctx context.Context, a []any) any { return c.TestLDAPConnection(ctx, arg(a, 0)) },
	"getOIDCConfig":          func(c *Client, ctx context.Context, a []any) any { return c.GetOIDCConfig(ctx) },
	"saveOIDCConfig":         func(c *Client, ctx context.Context, a []any) any { return c.SaveOIDCConfig(ctx, arg(a, 0)) },
	"testOIDCDiscovery":      func(c *Client, ctx context.Context, a []any) any { return c.TestOIDCDiscovery(ctx, arg(a, 0)) },
	"getOIDCStatus":          func(c *Client, ctx context.Context, a []any) any { return c.GetOIDCStatus(ctx) },
	"exchangeOIDCCode":       func(c *Client, ctx context.Context, a []any) any { return c.ExchangeOIDCCode(ctx, arg(a, 0)) },
	"startOIDCAuthorize": func(c *Client, ctx context.Context, a []any) any {
		returnURL, _ := or(arg(a, 0), "").(string)
		return c.StartOIDCAuthorize(ctx, returnURL)
	},
	"assignStrategy": func(c *Client, ctx context.Context, a []any) any { return c.AssignStrategy(ctx, arg(a, 0)) },
	"getStrategy":    func(c *Client, ctx context.Context, a []any) any { return c.GetStrategy(ctx, str(a, 0)) },
	"deleteStrategy": func(c *Client, ctx context.Context, a []any) any { return c.DeleteStrategy(ctx, str(a, 0)) },
	"setStrategyStatus": func(c *Client, ctx context.Context, a []any) any {
		return c.SetStrategyStatus(ctx, str(a, 0), arg(a, 1))
	},
	"listProDevices": func(c *Client, ctx context.Context, a []any) any { return c.ListProDevices(ctx, arg(a, 0)) },

	"authService.checkGoServerHealth": func(c *Client, ctx context.Context, a []any) any { return c.CheckGoServerHealth(ctx) },
	"authService.authenticateViaGo": func(c *Client, ctx context.Context, a []any) any {
		return c.AuthenticateViaGo(ctx, str(a, 0), str(a, 1))
	},
	"authService.verifyTotpViaGo": func(c *Client, ctx context.Context, a []any) any {
		return c.VerifyTOTPViaGo(ctx, str(a, 0), str(a, 1))
	},
	"authService.getGoSSOStatus": func(c *Client, ctx context.Context, a []any) any { return c.GetGoSSOStatus(ctx) },
	"authService.tryGoServerAuth": func(c *Client, ctx context.Context, a []any) any {
		return c.TryGoServerAuth(ctx, str(a, 0), str(a, 1))
	},
	"authService.tryLdapVerifyOnGo": func(c *Client, ctx context.Context, a []any) any {
		return c.TryLDAPVerifyOnGo(ctx, str(a, 0), str(a, 1))
	},
}

// thrown marks a result that the Node function threw instead of returned.
type thrown struct{ err *Error }

// arg returns argument i, or nil when the caller gave fewer.
func arg(a []any, i int) any {
	if i < len(a) {
		return a[i]
	}
	return nil
}

// str returns argument i as the text encodeURIComponent made of it; a
// missing argument is "".
func str(a []any, i int) string {
	v := arg(a, i)
	if v == nil {
		return ""
	}
	return jsString(v)
}

// id returns argument i as the text the Node client's id check saw:
// String(value || "").
func id(a []any, i int) string {
	return jsString(or(arg(a, i), ""))
}

// exportedFunctions are the functions the Node client exports, without
// normalisePeer, proxyOIDCCallback and the axios client itself.
var exportedFunctions = []string{
	"getHealth", "getServerStats", "getServerInfo",
	"getAllPeers", "getPeer", "getPeerIncludingDeleted", "deletePeer", "banPeer", "unbanPeer",
	"getConnectionMode", "setConnectionMode", "restorePeer", "changePeerId",
	"getStatusSummary", "getOnlinePeers", "getPeerStatus", "getPeerTelemetry", "refreshPeerHardware", "queuePeerTelemetryCommand",
	"getBlocklist", "addBlocklistEntry", "removeBlocklistEntry",
	"setPeerTags", "getPeersByTag", "updatePeer",
	"getAllRemoteTargets", "testRemoteTarget", "getRemoteTarget", "createRemoteTarget", "updateRemoteTarget", "deleteRemoteTarget",
	"getRemoteTargetCredentialStatus", "setRemoteTargetCredentials", "clearRemoteTargetCredentials", "acceptRemoteTargetCertificate",
	"getAuditEvents", "getClientAuditConnections", "getClientAuditFiles", "getClientAuditAlarms",
	"getConfig", "setConfig", "getPeerAvailability", "getPeerConnectionSessions",
	"syncOnlineStatus",
	"getCDAPStatus", "getCDAPDevices", "getCDAPDeviceInfo", "getCDAPDeviceManifest", "getCDAPDeviceState", "sendCDAPCommand", "getCDAPAlerts",
	"getMeshStatus", "getLinkedPeers", "linkDevice",
	"listDeviceTokens", "createDeviceToken", "getDeviceToken", "updateDeviceToken", "revokeDeviceToken", "bulkGenerateTokens", "bindTokenToPeer",
	"getEnrollmentMode", "setEnrollmentMode",
	"getEnrollmentPending", "getEnrollmentHistory", "approveEnrollment", "rejectEnrollment", "clearEnrollmentRejection",
	"getBranding", "saveBranding", "createPairingCode", "listPairingCodes", "revokePairingCode",
	"getAccessPolicy", "saveAccessPolicy", "deleteAccessPolicy", "getPeerConnectPassword",
	"listRoles", "getRolePermissions", "listRolePermissionOverrides", "setRolePermission", "deleteRolePermission",
	"getLDAPConfig", "saveLDAPConfig", "testLDAPConnection",
	"getOIDCConfig", "saveOIDCConfig", "testOIDCDiscovery", "getOIDCStatus", "exchangeOIDCCode", "startOIDCAuthorize",
	"assignStrategy", "getStrategy", "deleteStrategy", "setStrategyStatus", "listProDevices",
	"listHelpRequests", "acknowledgeHelpRequest", "resolveHelpRequest",
}

func TestDispatchCoversEveryExportedFunction(t *testing.T) {
	if len(exportedFunctions) != 99 {
		t.Errorf("the list holds %d functions, the Node client exports 99 besides normalisePeer and proxyOIDCCallback", len(exportedFunctions))
	}
	for _, name := range exportedFunctions {
		if dispatch[name] == nil {
			t.Errorf("no method for %s", name)
		}
	}
}
