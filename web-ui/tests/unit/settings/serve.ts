/**
 * A fake server for the settings tab tests: answers by method and path
 * with { success, data } (or a raw body), and records what was sent.
 *
 *   const server = serve({ 'GET /api/settings/ldap': { enabled: true } });
 *   server.sent('PUT', '/api/settings/ldap') // the bodies sent there
 */
import { vi } from 'vitest';

export type Answer = unknown | ((body: unknown) => unknown);
/** An answer with its own status, or one that is not wrapped in { success, data } */
export class Reply {
	constructor(
		readonly body: unknown,
		readonly status = 200
	) {}
}

export function serve(routes: Record<string, Answer>) {
	const calls: { method: string; path: string; body: unknown }[] = [];
	vi.stubGlobal(
		'fetch',
		vi.fn(async (input: string, init: RequestInit = {}) => {
			const method = (init.method ?? 'GET').toUpperCase();
			const path = new URL(input, 'http://localhost').pathname;
			const body = init.body ? JSON.parse(String(init.body)) : null;
			calls.push({ method, path, body });
			const route = routes[`${method} ${path}`];
			const answer = typeof route === 'function' ? (route as (b: unknown) => unknown)(body) : route;
			const reply =
				answer instanceof Reply
					? answer
					: new Reply(route === undefined ? { success: true, data: {} } : { success: true, data: answer });
			return new Response(JSON.stringify(reply.body), {
				status: reply.status,
				headers: { 'Content-Type': 'application/json' }
			});
		})
	);
	return {
		calls,
		sent: (method: string, path: string) =>
			calls.filter((c) => c.method === method && c.path === path).map((c) => c.body)
	};
}
