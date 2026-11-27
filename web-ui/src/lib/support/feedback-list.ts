/**
 * The answers after quick support as the Feedback page shows them
 * (unit tested): the stars go on top, the other answers under them.
 */

import { formatNumber } from '#lib/i18n/format.ts';
import { tf } from '#lib/i18n/i18n.svelte.ts';

interface Answer {
	id: string;
	kind: string;
	label: string;
	value: unknown;
}

/** The answers but the first rating, which the stars on top show */
export function feedbackRest<A extends Answer>(f: { stars: number | null; answers: readonly A[] }): A[] {
	const first = f.stars === null ? -1 : f.answers.findIndex((a) => a.kind === 'rating');
	return f.answers.filter((a, i) => i !== first && a.value !== null && a.value !== '');
}

/** "4 of 5 stars", "4.3 of 5 stars" for an average */
export function starsLabel(n: number): string {
	return tf('quick_support_feedback.stars_label', '{n} of 5 stars', {
		n: formatNumber(n, { maximumFractionDigits: 1 })
	});
}
