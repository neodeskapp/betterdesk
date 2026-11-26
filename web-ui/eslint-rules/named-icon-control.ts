/**
 * A button or link that shows only icons needs a name: aria-label or
 * aria-labelledby with a value (WCAG 4.1.2). The icons are aria-hidden
 * glyphs, so without one screen readers say only "button" and voice
 * control has nothing to call it by. A title alone is not enough: it
 * shows on hover only. IconButton takes a required label; this is for
 * the hand-made ones. An element with a spread ({...rest}) is left out,
 * since the spread may carry the name, and so is one marked
 * aria-hidden="true": a pointer shortcut kept out of the accessibility
 * tree on purpose, whose keyboard way must be described elsewhere.
 */
import type { Rule } from 'eslint';

interface SvelteNode {
	type: string;
	kind?: string;
	name?: { type: string; name?: string };
	value?: unknown;
	startTag?: { attributes: SvelteNode[] };
	key?: { name?: string } | null;
	children?: SvelteNode[];
}

const CONTROLS = new Set(['button', 'a']);
const NAMING = new Set(['aria-label', 'aria-labelledby']);

function elementName(node: SvelteNode): string | undefined {
	return node.name?.name;
}

/** An attribute that names the element: present and not an empty literal */
function names(attr: SvelteNode): boolean {
	if (attr.type !== 'SvelteAttribute' || !attr.key?.name || !NAMING.has(attr.key.name)) return false;
	const parts = (attr.value as SvelteNode[] | undefined) ?? [];
	const [first] = parts;
	if (!first) return false;
	return !(parts.length === 1 && first.type === 'SvelteLiteral' && String(first.value).trim() === '');
}

/** aria-hidden="true", written as a literal */
function hidden(attr: SvelteNode): boolean {
	if (attr.type !== 'SvelteAttribute' || attr.key?.name !== 'aria-hidden') return false;
	const [first] = (attr.value as SvelteNode[] | undefined) ?? [];
	return first?.type === 'SvelteLiteral' && String(first.value) === 'true';
}

/** Only icon components (and white space or comments) inside */
function onlyIcons(children: SvelteNode[]): boolean {
	let icons = 0;
	for (const c of children) {
		if (c.type === 'SvelteText' && String(c.value).trim() === '') continue;
		if (c.type === 'SvelteHTMLComment') continue;
		if (c.type === 'SvelteElement' && c.kind === 'component' && elementName(c) === 'Icon') {
			icons++;
			continue;
		}
		return false;
	}
	return icons > 0;
}

const rule = {
	meta: {
		type: 'problem',
		docs: { description: 'Require a name on buttons and links that show only an icon' },
		schema: [],
		messages: {
			unnamed:
				'This {{tag}} shows only an icon: give it aria-label={t(...)} (or use IconButton), so screen readers and voice control can name it.'
		}
	} satisfies Rule.RuleMetaData,
	create(context: Rule.RuleContext) {
		return {
			SvelteElement(node: SvelteNode) {
				const tag = elementName(node);
				if (node.kind !== 'html' || !tag || !CONTROLS.has(tag)) return;
				const attrs = node.startTag?.attributes ?? [];
				if (attrs.some((a) => a.type === 'SvelteSpreadAttribute')) return;
				if (attrs.some(names) || attrs.some(hidden)) return;
				if (!onlyIcons(node.children ?? [])) return;
				context.report({ node: node as unknown as Rule.Node, messageId: 'unnamed', data: { tag } });
			}
		};
	}
};

export default rule;
