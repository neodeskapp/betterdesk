/**
 * The server's readability check of the appearance in words (BUGS L374):
 * its ids name the part checked ("page-text") and the kind of background
 * ("image"); the console's words for them go into the message.
 */

import { t } from '#lib/i18n/i18n.svelte.ts';

export interface ReadabilityIssue {
	severity?: string | null;
	messageKey?: string | null;
	messageParams?: Record<string, unknown> | null;
	message?: string | null;
}

function readableParams(params: Record<string, unknown> | null | undefined) {
	if (!params) return undefined;
	const out: Record<string, string | number> = {};
	for (const [key, value] of Object.entries(params)) {
		const raw = typeof value === 'number' ? value : String(value ?? '');
		const word =
			key === 'item'
				? `settings.appearance.readability_item_${String(raw).replace(/-/g, '_')}`
				: key === 'type'
					? `settings.appearance.readability_type_${String(raw)}`
					: '';
		const tr = word ? t(word) : '';
		out[key] = tr && tr !== word ? tr : raw;
	}
	return out;
}

/** The first three findings as one text */
export function readabilityText(issues: readonly ReadabilityIssue[]): string {
	return issues
		.slice(0, 3)
		.map((issue) => {
			if (issue.messageKey) {
				const tr = t(issue.messageKey, readableParams(issue.messageParams));
				if (tr !== issue.messageKey) return tr;
			}
			return issue.message ?? '';
		})
		.filter(Boolean)
		.join(' ');
}
