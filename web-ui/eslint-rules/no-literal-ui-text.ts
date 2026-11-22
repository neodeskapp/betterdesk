/**
 * Visible text in Svelte markup must come from the translation files.
 *
 * Reports text nodes with letters in them and literal values of attributes
 * people read (aria-label, title, placeholder, alt, label). Mustache
 * expressions ({t('key')}) are fine. Symbols and punctuation alone (×, –, :)
 * are allowed, so are elements marked translate="no" (brand names, sample IDs).
 */
import type { Rule } from 'eslint';

const READ_ATTRS = new Set([
	'aria-label',
	'aria-description',
	'title',
	'placeholder',
	'alt',
	'label',
	'aria-roledescription',
	'aria-valuetext'
]);
const LETTERS = /\p{L}/u;

/** The parts of svelte-eslint-parser's nodes this rule reads */
interface SvelteNode {
	type: string;
	parent?: SvelteNode | null;
	startTag?: { attributes: SvelteNode[] };
	key?: { name?: string } | null;
	value?: unknown;
}

interface SvelteText extends SvelteNode {
	value: string;
}

interface SvelteAttribute extends SvelteNode {
	value: SvelteNode[];
}

/** A literal part of an attribute's value, with its text */
function literalText(part: SvelteNode | undefined): string | undefined {
	return part?.type === 'SvelteLiteral' && typeof part.value === 'string' ? part.value : undefined;
}

function insideNoTranslate(node: SvelteNode): boolean {
	for (let p = node.parent; p; p = p.parent) {
		if (p.type === 'SvelteElement' && p.startTag) {
			const attr = p.startTag.attributes.find(
				(a) => a.type === 'SvelteAttribute' && a.key && a.key.name === 'translate'
			) as SvelteAttribute | undefined;
			if (attr && attr.value.length === 1 && literalText(attr.value[0]) === 'no') return true;
		}
		if (p.type === 'SvelteStyleElement' || p.type === 'SvelteScriptElement') return true;
	}
	return false;
}

/** ESLint reports at the node's location; Svelte nodes have one like ESTree nodes */
function at(node: SvelteNode): Rule.Node {
	return node as unknown as Rule.Node;
}

const rule = {
	meta: {
		type: 'problem',
		docs: { description: 'Disallow hard-coded user-visible strings in Svelte markup' },
		schema: [],
		messages: {
			text: 'Visible text "{{text}}" must come from the translation files (t(...)).',
			attr: 'Attribute {{name}}="{{text}}" must come from the translation files (t(...)).'
		}
	} satisfies Rule.RuleMetaData,
	create(context: Rule.RuleContext) {
		return {
			SvelteText(node: SvelteText) {
				const text = node.value.trim();
				if (!text || !LETTERS.test(text) || insideNoTranslate(node)) return;
				context.report({ node: at(node), messageId: 'text', data: { text: text.slice(0, 40) } });
			},
			SvelteAttribute(node: SvelteAttribute) {
				const name = node.key && node.key.name;
				if (!name || !READ_ATTRS.has(name)) return;
				for (const part of node.value) {
					const value = literalText(part);
					if (value !== undefined && LETTERS.test(value) && !insideNoTranslate(node)) {
						context.report({ node: at(node), messageId: 'attr', data: { name, text: value.slice(0, 40) } });
					}
				}
			}
		};
	}
};

export default rule;
