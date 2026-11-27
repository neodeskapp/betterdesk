/**
 * The words of feedback after quick support: the kinds of question, what
 * is wrong with one, a question in one line, and an answer as the console
 * shows it.
 */

import { tf } from '#lib/i18n/i18n.svelte.ts';
import {
	MAX_LABEL,
	MAX_OPTIONS,
	MIN_OPTIONS,
	type FeedbackQuestion,
	type QuestionKind,
	type QuestionProblem
} from './feedback-form.ts';

export function kindName(kind: QuestionKind): string {
	switch (kind) {
		case 'rating':
			return tf('quick_support_feedback.kind_rating', 'Stars from 1 to 5');
		case 'text':
			return tf('quick_support_feedback.kind_text', 'Text');
		case 'choice':
			return tf('quick_support_feedback.kind_choice', 'One of several options');
		case 'yesno':
			return tf('quick_support_feedback.kind_yesno', 'Yes or no');
	}
}

export function problemText(problem: QuestionProblem): string {
	switch (problem) {
		case 'label_missing':
			return tf('quick_support_feedback.errors.label_missing', 'Type the question.');
		case 'label_too_long':
			return tf('quick_support_feedback.errors.label_too_long', 'Use at most {max} characters.', {
				max: MAX_LABEL
			});
		case 'option_count':
			return tf('quick_support_feedback.errors.option_count', 'Give from {min} to {max} options.', {
				min: MIN_OPTIONS,
				max: MAX_OPTIONS
			});
		case 'option_missing':
			return tf('quick_support_feedback.errors.option_missing', 'Fill in or remove the empty option.');
		case 'option_too_long':
			return tf(
				'quick_support_feedback.errors.option_too_long',
				'Use at most {max} characters for an option.',
				{
					max: MAX_LABEL
				}
			);
		case 'option_twice':
			return tf('quick_support_feedback.errors.option_twice', 'Each option can be there once.');
	}
}

/** What sort of answer a question takes, in one line under its text */
export function questionLine(q: FeedbackQuestion): string {
	let kind = kindName(q.kind);
	if (q.kind === 'text')
		kind = q.long
			? tf('quick_support_feedback.kind_text_long', 'Long text')
			: tf('quick_support_feedback.kind_text_short', 'Short text');
	if (q.kind === 'choice') kind = `${kind}: ${q.options.join(', ')}`;
	return q.required
		? tf('quick_support_feedback.line_required', '{kind}, must be answered', { kind })
		: tf('quick_support_feedback.line_optional', '{kind}, optional', { kind });
}

/** An answer as text: the stars as "4 of 5", yes or no, the text as given */
export function answerText(kind: string, value: unknown): string {
	if (value === null || value === undefined || value === '') return '';
	if (kind === 'rating') return tf('quick_support_feedback.stars_of', '{n} of 5', { n: Number(value) });
	if (kind === 'yesno')
		return value === true ? tf('quick_support_feedback.yes', 'Yes') : tf('quick_support_feedback.no', 'No');
	return String(value);
}
