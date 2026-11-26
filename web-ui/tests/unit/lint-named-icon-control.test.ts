/**
 * The lint rule local/named-icon-control: a button or link that shows only
 * an icon must have a name (aria-label or aria-labelledby), or screen
 * readers say "button" and voice control can't reach it (WCAG 4.1.2).
 * Positive tabindex and click handlers on non-interactive elements are
 * Svelte's own warnings (a11y_positive_tabindex, ...), which the lint and
 * svelte-check runs fail on; the last test shows they fire.
 */
import { RuleTester } from 'eslint';
import svelteParser from 'svelte-eslint-parser';
import { compile } from 'svelte/compiler';
import { afterAll, describe, expect, it, test } from 'vitest';
import rule from '../../eslint-rules/named-icon-control.ts';

// Not in the type definitions, but read by RuleTester
(RuleTester as unknown as { afterAll: typeof afterAll }).afterAll = afterAll;
RuleTester.describe = describe;
RuleTester.it = it;
RuleTester.itOnly = it.only;

const tester = new RuleTester({ languageOptions: { parser: svelteParser } });

tester.run('named-icon-control', rule, {
	valid: [
		{ code: `<button aria-label={t('close')}><Icon name="close" /></button>`, filename: 'a.svelte' },
		{ code: `<a href="/x" aria-labelledby="h"><Icon name="arrow" /></a>`, filename: 'a.svelte' },
		{ code: `<button><Icon name="add" />{t('add')}</button>`, filename: 'a.svelte' },
		{
			code: `<button><Icon name="add" /><span class="sr-only">{t('add')}</span></button>`,
			filename: 'a.svelte'
		},
		// A spread may carry the name: not knowable here
		{ code: `<button {...rest}><Icon name="add" /></button>`, filename: 'a.svelte' },
		{ code: `<div><Icon name="info" /></div>`, filename: 'a.svelte' },
		// Kept out of the accessibility tree on purpose (the remote session tab's x: Delete closes)
		{ code: `<button tabindex="-1" aria-hidden="true"><Icon name="close" /></button>`, filename: 'a.svelte' }
	],
	invalid: [
		{
			code: `<button><Icon name="close" /></button>`,
			filename: 'a.svelte',
			errors: [{ messageId: 'unnamed' }]
		},
		{
			code: `<button title={t('close')}>\n\t<Icon name="close" />\n</button>`,
			filename: 'a.svelte',
			errors: [{ messageId: 'unnamed' }]
		},
		{
			code: `<a href="/settings"><Icon name="settings" /></a>`,
			filename: 'a.svelte',
			errors: [{ messageId: 'unnamed' }]
		},
		{
			code: `<button aria-label=""><Icon name="close" /></button>`,
			filename: 'a.svelte',
			errors: [{ messageId: 'unnamed' }]
		}
	]
});

test('Svelte itself warns about positive tabindex and clicks on non-interactive elements', () => {
	const codes = (src: string) => compile(src, { generate: false }).warnings.map((w) => w.code);
	expect(codes('<div tabindex="2">x</div>')).toContain('a11y_positive_tabindex');
	expect(codes('<div onclick={() => {}}>x</div>')).toEqual(
		expect.arrayContaining(['a11y_click_events_have_key_events', 'a11y_no_static_element_interactions'])
	);
});
