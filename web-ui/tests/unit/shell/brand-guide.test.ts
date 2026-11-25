/**
 * A right-click on NeoDesk's own logo opens the brand guide in a new tab
 * instead of the browser's menu; the context-menu key and Shift+F10 on the
 * link around it do the same. A logo an install drew itself (another name)
 * keeps the browser's menu.
 */
import { cleanup, render } from '@testing-library/svelte';
import { afterEach, beforeEach, expect, test, vi } from 'vitest';
import BrandLogo from '../../../src/lib/shell/BrandLogo.svelte';
import { BRAND_GUIDE_URL } from '../../../src/lib/util/links.ts';

let open: ReturnType<typeof vi.fn>;
beforeEach(() => {
	open = vi.fn();
	vi.stubGlobal('open', open);
});
afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
});

const menu = (el: Element) => {
	const e = new MouseEvent('contextmenu', { bubbles: true, cancelable: true });
	el.dispatchEvent(e);
	return e;
};

test('the address is the brand guide', () => {
	expect(BRAND_GUIDE_URL).toBe('https://neode.sk/brand');
});

test('right-click on the NeoDesk logo opens the brand guide in a new tab, without access back', () => {
	const { container } = render(BrandLogo, { name: 'NeoDesk' });
	const e = menu(container.querySelector('svg')!);
	expect(e.defaultPrevented).toBe(true);
	expect(open).toHaveBeenCalledWith(BRAND_GUIDE_URL, '_blank', 'noopener');
});

test('the mark alone is ours too', () => {
	const { container } = render(BrandLogo, { mark: true });
	expect(menu(container.querySelector('svg')!).defaultPrevented).toBe(true);
	expect(open).toHaveBeenCalledTimes(1);
});

test('an install with its own name keeps the browser menu', () => {
	const { container } = render(BrandLogo, { name: 'Acme Remote' });
	const e = menu(container.querySelector('svg')!);
	expect(e.defaultPrevented).toBe(false);
	expect(open).not.toHaveBeenCalled();
});

test('the context-menu key on the link around the logo opens it too; a left click does not', () => {
	const link = document.createElement('a');
	link.href = '/';
	document.body.appendChild(link);
	render(BrandLogo, { target: link, props: { name: 'NeoDesk' } });
	link.focus();
	expect(menu(link).defaultPrevented).toBe(true);
	expect(open).toHaveBeenCalledTimes(1);
	link.dispatchEvent(new MouseEvent('click', { bubbles: true, cancelable: true }));
	expect(open).toHaveBeenCalledTimes(1);
	link.remove();
});
