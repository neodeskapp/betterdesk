import { test, expect } from './fixtures.ts';
import { UI } from './roles.ts';
import { SEARCH_INDEX } from '../src/lib/settings/search-index.ts';
import { TABS } from '../src/lib/settings/tabs.ts';

/**
 * The settings list (search-index.ts) is the one list both searches read,
 * and the tabs are written by hand. This keeps the two the same: on every
 * tab, each row's element is on the page, and each field on the page sits
 * inside an element a row names. A field added without a row, or a row
 * whose element went away, fails here.
 */

/** Elements that show only after a choice on the page, named with the choice that shows them */
const SHOWN_LATER: Readonly<Record<string, string>> = {
	'font-upload-name': 'Fonts to search: Upload custom',
	'font-upload-file': 'Fonts to search: Upload custom'
};

test.use({ role: 'super_admin' });

for (const tab of TABS) {
	test(`settings list and the ${tab} tab name the same settings`, async ({ page }) => {
		test.setTimeout(60_000);
		await page.goto(`${UI}/settings${tab === 'general' ? '' : `?tab=${tab}`}`);
		await expect(page.locator('.settings-page')).toBeVisible();
		await expect(
			page.locator('.settings-page [aria-busy="true"], .settings-page .state-loading')
		).toHaveCount(0, {
			timeout: 15_000
		});
		// Every section draws when its data is in
		await page.waitForLoadState('networkidle');

		const ids = [...new Set(SEARCH_INDEX.filter((r) => r[0] === tab).map((r) => r[1]))];
		const missing = await page.evaluate(
			(list) => list.filter((id) => !document.getElementById(id)),
			ids.filter((id) => !SHOWN_LATER[id])
		);
		expect(missing, `rows of the ${tab} tab whose element is not on the page`).toEqual([]);

		const unlisted = await page.evaluate((list) => {
			const known = new Set(list);
			const root = document.querySelector('.settings-page');
			if (!root) return ['no settings page'];
			const fields = [...root.querySelectorAll<HTMLElement>('input, select, textarea')].filter(
				(el) =>
					el.id !== 'settings-search-input' &&
					!(el instanceof HTMLInputElement && el.type === 'hidden') &&
					!el.closest('.settings-search-results, [role="search"]')
			);
			const out: string[] = [];
			for (const el of fields) {
				let cur: HTMLElement | null = el;
				let hit = false;
				while (cur && cur !== root) {
					if (cur.id && known.has(cur.id)) {
						hit = true;
						break;
					}
					cur = cur.parentElement;
				}
				if (!hit) {
					const label =
						(el.id && document.querySelector(`label[for="${CSS.escape(el.id)}"]`)?.textContent) ||
						el.getAttribute('aria-label') ||
						el.closest('label')?.textContent ||
						el.getAttribute('name') ||
						el.outerHTML.slice(0, 80);
					out.push(`${el.id || '(no id)'}: ${label.replace(/\s+/g, ' ').trim()}`);
				}
			}
			return out;
		}, ids);
		expect(unlisted, `fields on the ${tab} tab without a row in the settings list`).toEqual([]);
	});
}
