/**
 * Who this browser is to a device, once per connection. The desktop client
 * 1.5 latches the scope of a connection's first login (login_scope_digest in
 * src/server/connection.rs: my_id and the session's kind) and closes the
 * connection on a later login whose scope differs (L530). So every login of
 * one connection, the one without a password, a typed password and the
 * server's answer for an organisation's password, carries the same identity;
 * a new connection gets a new one. The real client and the mock device both
 * take their logins' identity from here, and the mock latches it the same
 * way, so the end-to-end tests catch a login that changes it.
 */

export interface LoginIdentity {
	/** LoginRequest.my_id: the caller, latched by the device */
	readonly myId: string;
	/** LoginRequest.session_id: kept with my_id, so a connection is one session to the device */
	readonly sessionId: number;
}

export function newLoginIdentity(now = Date.now()): LoginIdentity {
	return { myId: 'neodesk-web-' + now.toString(36), sessionId: now };
}

/** The identity of one connection: the first login makes it, every later login reuses it, a new connection starts over */
export class ConnectionLogins {
	private current: LoginIdentity | null = null;

	forLogin(): LoginIdentity {
		this.current ??= newLoginIdentity();
		return this.current;
	}

	reset(): void {
		this.current = null;
	}
}

/** What a device compares across the logins of one connection */
export function loginLatch(login: { myId?: string | null; kind?: string }): string {
	return JSON.stringify([login.kind || 'remote', login.myId || '']);
}
