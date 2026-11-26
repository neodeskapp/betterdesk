/**
 * Accessibility probes (audit evidence, A11Y_SWEEP=1): what a script can
 * measure of the manual checks. Per route: the Tab order with each stop's
 * role, name and whether a focus indicator shows and is not covered;
 * keyboard traps; clipping under the WCAG 1.4.12 text spacing; targets
 * under 24 and 44 px; and screenshots with forced colours. Writes
 * test-results/a11y-probe/; it never fails.
 */
import fs from 'node:fs';
import type { Page } from '@playwright/test';
import { test } from './fixtures.ts';

const OUT = 'test-results/a11y-probe';
const ROUTES = [
	'/',
	'/devices',
	'/devices/add',
	'/users',
	'/settings',
	'/settings?tab=auth',
	'/account',
	'/chat',
	'/help-requests',
	'/remote/sessions',
	'/remote',
	'/server/backups',
	'/security-audit',
	'/organizations',
	'/reports',
	'/cdap-studio'
];
const PUBLIC = ['/login', '/portal', '/set-password', '/invite', '/remote/login'];

const slug = (s: string) => s.replace(/^\//, '').replace(/[^a-z0-9]+/gi, '_') || 'home';

async function settle(page: Page) {
	await page.waitForLoadState('networkidle', { timeout: 10_000 }).catch(() => undefined);
	await page.waitForTimeout(400);
}

/** Tab through the page, recording each stop */
async function tabWalk(page: Page, max = 160) {
	const stops: Record<string, unknown>[] = [];
	const seen = new Map<string, number>();
	let trap = '';
	for (let i = 0; i < max; i++) {
		await page.keyboard.press('Tab');
		const s = await page.evaluate(() => {
			const el = document.activeElement as HTMLElement | null;
			if (!el || el === document.body) return null;
			const r = el.getBoundingClientRect();
			const cs = getComputedStyle(el);
			const path = (() => {
				const parts: string[] = [];
				for (let e: Element | null = el; e && parts.length < 4; e = e.parentElement)
					parts.unshift(
						e.tagName.toLowerCase() + (e.id ? '#' + e.id : '') + (e.classList[0] ? '.' + e.classList[0] : '')
					);
				return parts.join('>');
			})();
			// Visible indicator: an outline, or a box shadow, that focus added
			const outline = cs.outlineStyle !== 'none' && parseFloat(cs.outlineWidth) > 0;
			const shadow = cs.boxShadow !== 'none';
			// Covered: the centre point hits something outside the element
			const cx = Math.min(Math.max(r.left + r.width / 2, 0), innerWidth - 1);
			const cy = Math.min(Math.max(r.top + r.height / 2, 0), innerHeight - 1);
			const hit = document.elementFromPoint(cx, cy);
			const covered = !!hit && !el.contains(hit) && !hit.contains(el);
			const offscreen = r.bottom < 0 || r.top > innerHeight || r.right < 0 || r.left > innerWidth;
			const name =
				el.getAttribute('aria-label') ||
				(el.getAttribute('aria-labelledby') || '')
					.split(/\s+/)
					.map((id) => document.getElementById(id)?.textContent?.trim())
					.join(' ') ||
				(el as HTMLInputElement).labels?.[0]?.textContent?.trim() ||
				el.textContent?.trim().slice(0, 60) ||
				el.getAttribute('title') ||
				el.getAttribute('placeholder') ||
				'';
			return {
				path,
				tag: el.tagName.toLowerCase(),
				role: el.getAttribute('role') || '',
				name: name.replace(/\s+/g, ' ').slice(0, 80),
				outline: outline
					? `${cs.outlineWidth} ${cs.outlineStyle} ${cs.outlineColor} off ${cs.outlineOffset}`
					: '',
				shadow: shadow ? cs.boxShadow.slice(0, 80) : '',
				indicator: outline || shadow,
				covered: covered
					? (hit as HTMLElement).tagName.toLowerCase() + '.' + ((hit as HTMLElement).classList[0] ?? '')
					: '',
				offscreen,
				size: `${Math.round(r.width)}x${Math.round(r.height)}`,
				small24: r.width < 24 || r.height < 24,
				inDialog: !!el.closest('[role=dialog]')
			};
		});
		if (!s) {
			stops.push({ body: true });
			continue;
		}
		const k = `${s.path}|${s.name}`;
		const n = (seen.get(k) ?? 0) + 1;
		seen.set(k, n);
		stops.push(s);
		// Back at a stop seen before: the cycle is complete
		if (n > 1 && stops.length > 3) break;
	}
	const distinct = new Set(stops.map((s) => JSON.stringify([s.path, s.name]))).size;
	if (stops.length >= max && distinct < 4) trap = 'focus cycles among fewer than 4 elements';
	return { stops, trap };
}

async function textSpacing(page: Page) {
	await page.addStyleTag({
		content: `* { line-height: 1.5 !important; letter-spacing: 0.12em !important; word-spacing: 0.16em !important; } p { margin-bottom: 2em !important; }`
	});
	await page.waitForTimeout(300);
	return page.evaluate(() => {
		const out: string[] = [];
		for (const el of document.querySelectorAll<HTMLElement>('body *')) {
			if (!el.offsetParent && getComputedStyle(el).position !== 'fixed') continue;
			if (!el.childNodes.length || ![...el.childNodes].some((n) => n.nodeType === 3 && n.textContent?.trim()))
				continue;
			const cs = getComputedStyle(el);
			const clipsX =
				/hidden|clip/.test(cs.overflowX) &&
				el.scrollWidth > el.clientWidth + 1 &&
				cs.textOverflow !== 'ellipsis';
			const clipsY = /hidden|clip/.test(cs.overflowY) && el.scrollHeight > el.clientHeight + 1;
			if (clipsX || clipsY)
				out.push(
					`${el.tagName.toLowerCase()}.${el.classList[0] ?? ''} "${el.textContent?.trim().slice(0, 40)}" ${clipsX ? 'x' : ''}${clipsY ? 'y' : ''}`
				);
		}
		return out.slice(0, 60);
	});
}

async function targets(page: Page) {
	return page.evaluate(() => {
		const list = [
			...document.querySelectorAll<HTMLElement>(
				'a[href],button,input,select,textarea,[role=button],[role=link],[role=tab],[role=menuitem],[role=checkbox],[role=switch],[tabindex="0"]'
			)
		];
		const small24: string[] = [];
		let under44 = 0;
		let total = 0;
		for (const el of list) {
			const r = el.getBoundingClientRect();
			if (!r.width || !r.height) continue;
			if (getComputedStyle(el).visibility === 'hidden') continue;
			total++;
			if (r.width < 44 || r.height < 44) under44++;
			// Inline links in text are exempt from 2.5.8
			const inline = el.tagName === 'A' && getComputedStyle(el).display === 'inline';
			if (!inline && (r.width < 24 || r.height < 24))
				small24.push(
					`${el.tagName.toLowerCase()}.${el.classList[0] ?? ''} "${(el.getAttribute('aria-label') || el.textContent || '').trim().slice(0, 30)}" ${Math.round(r.width)}x${Math.round(r.height)}`
				);
		}
		return { total, under44, small24 };
	});
}

test.skip(!process.env.A11Y_SWEEP, 'audit probes run with A11Y_SWEEP=1');
test.describe.configure({ mode: 'parallel' });
test.use({ allowProblems: [/./] });

function probe(route: string) {
	return async ({ page }: { page: Page }) => {
		test.setTimeout(120_000);
		fs.mkdirSync(OUT, { recursive: true });
		await page.setViewportSize({ width: 1280, height: 900 });
		await page.goto(route);
		await settle(page);
		const walk = await tabWalk(page);
		const tg = await targets(page);
		await page.setViewportSize({ width: 320, height: 640 });
		await page.reload();
		await settle(page);
		const walk320 = await tabWalk(page, 80);
		await page.setViewportSize({ width: 1280, height: 900 });
		await page.reload();
		await settle(page);
		const spacing = await textSpacing(page);
		await page.emulateMedia({ forcedColors: 'active', colorScheme: 'dark' });
		await page.reload();
		await settle(page);
		await page.screenshot({ path: `${OUT}/${slug(route)}__forced.png` });
		await page.keyboard.press('Tab');
		await page.keyboard.press('Tab');
		await page.keyboard.press('Tab');
		await page.screenshot({ path: `${OUT}/${slug(route)}__forced-focus.png` });
		fs.writeFileSync(
			`${OUT}/${slug(route)}.json`,
			JSON.stringify({ route, walk, walk320, targets: tg, spacing }, null, 1)
		);
	};
}

for (const r of ROUTES) test(`probe ${r}`, probe(r));
test.describe('public', () => {
	test.use({ storageState: { cookies: [], origins: [] } });
	for (const r of PUBLIC) test(`probe ${r}`, probe(r));
});
