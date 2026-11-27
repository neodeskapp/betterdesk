import { test as base, expect, type Page } from '@playwright/test';
import AxeBuilder from '@axe-core/playwright';
import fs from 'node:fs';
import { authFile, type Role } from './roles.ts';

/**
 * Every test fails on a console error, an uncaught exception, a CSP
 * violation, a failed request or an HTTP error response, unless the test
 * lists it in `allowProblems` (for tests that mock failures on purpose).
 */
export type Problems = { list: string[]; allow: RegExp[] };

export const test = base.extend<{ problems: Problems; role: Role; allowProblems: RegExp[] }>({
	role: ['admin', { option: true }],
	allowProblems: [[], { option: true }],
	storageState: async ({ role }, use, info) => {
		const available: string[] = fs.existsSync('e2e/.auth/available.json')
			? JSON.parse(fs.readFileSync('e2e/.auth/available.json', 'utf8'))
			: [];
		info.skip(!available.includes(role), `no e2e session for ${role}`);
		await use(authFile(role));
	},
	problems: [
		async ({ page, allowProblems }, use) => {
			const problems: Problems = { list: [], allow: allowProblems };
			await page.addInitScript(() => {
				document.addEventListener('securitypolicyviolation', (e) => {
					console.error(`CSP violation: ${e.violatedDirective} ${e.blockedURI}`);
				});
			});
			page.on('console', (m) => {
				if (m.type() !== 'error') return;
				// Firefox also logs a font download that a navigation away aborted
				// (status NS_BINDING_ABORTED); like the aborted request below, no failure
				if (/downloadable font: download failed .*status=2152398850\b/.test(m.text())) return;
				// The init script above runs in every frame; in a frame sandboxed
				// without scripts (the mail preview) Chromium blocks it and says so
				if (
					/^Blocked script execution in 'about:srcdoc' because the document's frame is sandboxed/.test(
						m.text()
					)
				)
					return;
				problems.list.push('console: ' + m.text());
			});
			page.on('pageerror', (e) => problems.list.push('pageerror: ' + e.message));
			page.on('requestfailed', (r) => {
				const err = r.failure()?.errorText ?? '';
				// Navigations away and closed sockets abort requests; they are not failures
				if (/ERR_ABORTED|NS_BINDING_ABORTED|aborted/i.test(err)) return;
				problems.list.push(`failed: ${r.method()} ${r.url()} ${err}`);
			});
			page.on('response', (r) => {
				if (r.status() >= 400) problems.list.push(`http ${r.status()}: ${r.request().method()} ${r.url()}`);
			});
			await use(problems);
			const left = problems.list.filter((p) => !problems.allow.some((re) => re.test(p)));
			expect(left, 'console errors, CSP violations or failed requests').toEqual([]);
		},
		{ auto: true }
	]
});

export { expect };

/** axe with the WCAG 2.2 AA rules (2.0 and 2.1 included); fails the test on any violation */
export async function expectNoAxeViolations(page: Page, include?: string): Promise<void> {
	await settledMotion(page);
	let builder = new AxeBuilder({ page }).withTags(['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa']);
	if (include) builder = builder.include(include);
	const result = await builder.analyze();
	const summary = result.violations.map(
		(v) => `${v.id}: ${v.nodes.map((n) => n.target.join(' ')).join(', ')}`
	);
	expect(summary, 'axe violations').toEqual([]);
}

/**
 * Waits until transitions and animations that end have ended (a dialog
 * fades in over 120 ms): axe measures colours, and half-way through a fade
 * a button's text has less contrast than it ends with. Spinners and other
 * endless animations are not waited for.
 */
async function settledMotion(page: Page): Promise<void> {
	await page.evaluate(async () => {
		const ending = document
			.getAnimations()
			.filter((a) => a.playState === 'running' && a.effect?.getComputedTiming().iterations !== Infinity)
			.map((a) => a.finished.catch(() => undefined));
		await Promise.race([Promise.all(ending), new Promise((done) => setTimeout(done, 2_000))]);
	});
}
