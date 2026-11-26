/**
 * The English texts of every failure reason (failures.ts), the defaults of
 * the failure.<reason>.<part> keys in the language files. Made from one
 * table with the German texts; failures.test.ts checks that en.json says
 * the same.
 */

export const FAILURE_TEXTS = {
	device_offline: {
		what: '{name} is offline.',
		why: 'It has not checked in with the server for a while.',
		todo: 'Turn it on or wake it up, and check that NeoDesk runs on it. Then reconnect.'
	},
	device_unknown: {
		what: 'The server does not know {name}.',
		why: 'No computer with this ID is registered on this server.',
		todo: 'Check the ID. If the computer was removed, add it again under Add computers.'
	},
	device_revoked: {
		what: '{name} was revoked on this server.',
		why: 'Someone revoked this computer in the console, so the server refuses it, even when it is on.',
		todo: 'If it should connect again, unblock it here. Then reconnect.',
		todo_ask: 'Ask an administrator to unblock this computer, then reconnect.'
	},
	device_blocked: {
		what: '{name} is blocked on this server.',
		why: "Its ID is on the server's blocklist, so the server refuses it, even when it is on.",
		todo: 'If it should connect again, unblock it here. Then reconnect.',
		todo_ask: 'Ask an administrator to unblock this computer, then reconnect.'
	},
	device_banned: {
		what: '{name} is banned on this server.',
		why: 'An administrator banned this computer, so the server refuses it.',
		todo: 'If it should connect again, unban it in the device panel.'
	},
	device_removed: {
		what: '{name} was deleted on this server.',
		why: 'The computer is with the deleted devices.',
		todo: 'Restore it in the device panel, or add it again under Add computers.'
	},
	device_rejected: {
		what: 'This server turned {name} away.',
		why: 'An administrator rejected this computer when it asked to join.',
		todo: 'Ask an administrator to allow it under Add computers, then try again.'
	},
	other_machine: {
		what: 'Another computer already uses the ID of {name}.',
		why: 'The server knows this ID from a different machine.',
		todo: 'Ask an administrator to delete the old device in the console, then try again.'
	},
	key_changed: {
		what: '{name} came back with a new key.',
		why: 'The server refuses a computer whose key changed until an administrator approves it. This keeps others from taking over its ID.',
		todo: 'If you expected the change, approve the new key in the device panel. Then reconnect.'
	},
	join_proof: {
		what: '{name} could not prove that it is the computer the server knows.',
		why: 'The ID is enrolled already, and this computer did not sign the join with the key the server has for it.',
		todo: 'Update NeoDesk on this computer and open the link again, or ask an administrator to delete the old device in the console.'
	},
	server_unreachable: {
		what: "This browser can't reach the NeoDesk server.",
		why: 'The server did not answer when the connection started.',
		todo: 'Check your network, then reconnect. If it keeps failing, ask an administrator to check the server.'
	},
	relay_unreachable: {
		what: 'The connection to {name} could not be set up.',
		why: 'This browser could not reach the part of the server that carries the session.',
		todo: "Reconnect. If it keeps failing, ask an administrator to check that the server's ports are open."
	},
	relay_refused: {
		what: 'The server stopped the connection to {name}.',
		why: 'The server did not let this session through.',
		todo: 'Reconnect. If it keeps happening, copy the details and send them to an administrator.'
	},
	no_answer: {
		what: '{name} did not answer.',
		why: 'The server asked {name} to connect, and no answer came in time. It may be asleep or busy, or its network may block NeoDesk.',
		todo: 'Wake it up or wait a moment, then reconnect.'
	},
	not_authorized: {
		what: "You can't connect to {name}.",
		why: 'The server does not let your account start sessions to this computer.',
		todo: 'Ask an administrator to give you access to this computer.'
	},
	feature_off: {
		what: 'This kind of session is turned off for your account.',
		why: "An administrator turned it off in your account's client features.",
		todo: 'Ask an administrator to turn it on.'
	},
	consent_scope: {
		what: '{person} did not allow this.',
		why: 'In this quick support session, {person} allowed only some things.',
		todo: 'Ask {person} to allow it in the quick support window.'
	},
	license_blocked: {
		what: "The server's license does not allow this connection.",
		why: 'The license key does not match this server.',
		todo: 'Ask an administrator to check the license.'
	},
	session_limit: {
		what: 'The server takes no more sessions right now.',
		why: 'The number of sessions is at the limit of its license.',
		todo: 'Wait until another session ends, or ask an administrator.'
	},
	billing_blocked: {
		what: 'The server does not allow sessions to {name} right now.',
		why: 'The contract of this computer does not allow sessions.',
		todo: "Ask an administrator to check the computer's contract."
	},
	client_too_old: {
		what: 'NeoDesk on {name} is too old for this.',
		why: 'The version on the computer does not support this connection.',
		todo: 'Update NeoDesk on {name}, then reconnect.'
	},
	consent_declined: {
		what: '{person} declined the connection.',
		why: 'The person at {name} clicked Decline or closed the window that asked.',
		todo: 'Ask them to try again, then reconnect.'
	},
	consent_timeout: {
		what: '{person} did not answer.',
		why: 'Nobody accepted the connection on {name} within 3 minutes.',
		todo: 'Ask them to watch for the window that asks, then reconnect.'
	},
	consent_not_allowed: {
		what: '{person} has not allowed quick support yet.',
		why: '{name} offers to let you in by click only after the person clicked Allow in the quick support window.',
		todo: 'Ask them to click Allow in the quick support window, then connect again.'
	},
	consent_needs_https: {
		what: "{name} can't let you in by click.",
		why: 'This console is not on HTTPS, so quick support takes only the one-time password.',
		todo: 'Send a new quick support link: its one-time password comes to you and connects you. To accept by click, the console needs HTTPS.'
	},
	consent_needs_update: {
		what: "{name} can't let you in by click.",
		why: 'NeoDesk on {name} is too old to accept by click.',
		todo: 'Send a new quick support link: its one-time password comes to you and connects you. Or ask {person} to update NeoDesk.'
	},
	password_wrong: {
		what: 'The password was not accepted.',
		why: '{name} refused the password.',
		todo: 'Enter the password set on {name}. NeoDesk shows it on that computer under Settings, Security.'
	},
	tfa_wrong: {
		what: 'The code was not accepted.',
		why: '{name} refused the two-factor code.',
		todo: 'Enter the current code from the authenticator app linked to {name}.'
	},
	rate_limited: {
		what: 'Too many tries.',
		why: '{name} blocks sign-ins for a while after several wrong ones.',
		todo: 'Wait a minute, then reconnect.'
	},
	device_not_allowed: {
		what: '{name} does not take this connection.',
		why: 'NeoDesk on {name} only lets people in with a password, and no password is set there, or its settings turn connections off.',
		todo: 'Ask someone at {name} to set a password or allow connections under Settings, Security.'
	},
	device_blocks_you: {
		what: '{name} blocks you.',
		why: 'NeoDesk on {name} has your ID or address on its own blocklist.',
		todo: 'Ask someone at {name} to remove the block in its settings.'
	},
	device_feature_off: {
		what: '{name} turned this off.',
		why: 'NeoDesk on {name} does not allow this kind of session.',
		todo: 'Ask someone at {name} to allow it under Settings, Security.'
	},
	device_not_installed: {
		what: "{name} can't do this without an installed NeoDesk.",
		why: 'NeoDesk runs there without being installed, and this needs the installed version.',
		todo: 'Ask someone at {name} to install NeoDesk, then reconnect.'
	},
	login_screen: {
		what: "{name} shows a sign-in screen NeoDesk can't share.",
		why: "The sign-in screen of {name} runs on Wayland, which NeoDesk can't capture.",
		todo: 'Sign in on {name} first, or switch its sign-in screen to X11. Then reconnect.'
	},
	privacy_mode: {
		what: 'Someone turned on privacy mode on {name}.',
		why: 'Another session holds {name} in privacy mode, which lets no second viewer in.',
		todo: 'Wait until that session ends, then reconnect.'
	},
	device_not_ready: {
		what: '{name} is not ready for this connection.',
		why: "NeoDesk on {name} said it can't take the session now.",
		todo: 'Ask someone at {name} to open NeoDesk, then reconnect.'
	},
	insecure: {
		what: 'The connection to {name} was stopped because it could not be secured.',
		why: '{name} did not prove its identity or did not encrypt the connection. Someone between this browser and the computer may be listening in, or the server key is not set up.',
		todo: 'No password was sent. Ask an administrator to check the server.'
	},
	https_required: {
		what: "{name} can't be shown over plain HTTP.",
		why: 'This computer sends a video format the browser plays only over HTTPS.',
		todo: 'Open the console over HTTPS to see its screen.'
	},
	signin_closed: {
		what: '{name} closed the connection when this browser signed in.',
		why: 'The computer refused the sign-in.',
		todo: 'Reconnect and sign in again. If it keeps happening, ask an administrator to look at the log of NeoDesk on the computer.'
	},
	dropped_early: {
		what: 'The connection closed before {name} answered.',
		why: 'The network between this browser, the server and {name} was interrupted while the session started.',
		todo: 'Reconnect. If it keeps happening, check the network of the computer.'
	},
	dropped: {
		what: 'The connection to {name} dropped.',
		why: 'The network between this browser, the server and {name} was interrupted.',
		todo: 'Reconnect. If it keeps happening, check the network of the computer.'
	},
	unknown: {
		what: 'Could not connect to {name}.',
		why: 'The cause is not known. The details below show what this browser saw.',
		todo: 'Reconnect. If it keeps failing, copy the details and send them to an administrator.'
	}
} as const;

export type Reason = keyof typeof FAILURE_TEXTS;
