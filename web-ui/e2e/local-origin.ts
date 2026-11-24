/**
 * The console at http://localhost:<port>: a secure context in every
 * browser, so the clipboard API, WebCodecs and the other APIs that need one
 * are there. The e2e runs reach the console by its container name
 * (http://ndgo-e2e:5000), which browsers treat as plain HTTP; this test
 * forwards a port on localhost to it, one per worker, and gives the
 * role's session cookies to localhost.
 */
import fs from 'node:fs';
import net from 'node:net';
import { test as base } from './fixtures.ts';
import { authFile } from './roles.ts';

export const test = base.extend<object, { localOrigin: string }>({
	localOrigin: [
		// eslint-disable-next-line no-empty-pattern -- Playwright reads the fixtures a fixture needs from this pattern
		async ({}, use) => {
			const target = new URL(process.env.ND_URL ?? 'http://host.docker.internal:5000');
			if (target.hostname === 'localhost' || target.hostname === '127.0.0.1') {
				await use(target.origin);
				return;
			}
			const server = net.createServer((client) => {
				const upstream = net.connect(Number(target.port || 80), target.hostname);
				client.pipe(upstream).pipe(client);
				client.on('error', () => upstream.destroy());
				upstream.on('error', () => client.destroy());
			});
			await new Promise<void>((done) => server.listen(0, '127.0.0.1', done));
			const { port } = server.address() as net.AddressInfo;
			await use(`http://localhost:${port}`);
			await new Promise((done) => server.close(done));
		},
		{ scope: 'worker' }
	],
	baseURL: async ({ localOrigin }, use) => use(localOrigin),
	storageState: async ({ role, localOrigin }, use, info) => {
		const available: string[] = fs.existsSync('e2e/.auth/available.json')
			? JSON.parse(fs.readFileSync('e2e/.auth/available.json', 'utf8'))
			: [];
		info.skip(!available.includes(role), `no e2e session for ${role}`);
		const state = JSON.parse(fs.readFileSync(authFile(role), 'utf8')) as {
			cookies: { domain: string }[];
			origins: unknown[];
		};
		const host = new URL(localOrigin).hostname;
		await use({ cookies: state.cookies.map((c) => ({ ...c, domain: host })), origins: [] } as never);
	}
});

export { expect } from './fixtures.ts';
