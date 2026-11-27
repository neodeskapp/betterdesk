/**
 * The feedback form after quick support, without a DOM (unit tested): the
 * questions, their limits, adding, moving, removing and changing one, and
 * what is wrong with one. The server checks the same limits
 * (console/onboard/feedback.go).
 */

export type QuestionKind = 'rating' | 'text' | 'choice' | 'yesno';

export const QUESTION_KINDS: readonly QuestionKind[] = ['rating', 'text', 'choice', 'yesno'];

export interface FeedbackQuestion {
	/** '' for a question not saved yet: the server gives it its id */
	id: string;
	kind: QuestionKind;
	label: string;
	required: boolean;
	/** text: a long field instead of one line */
	long: boolean;
	/** choice: 2 to 8 options */
	options: string[];
}

export const MAX_QUESTIONS = 10;
export const MAX_LABEL = 200;
export const MIN_OPTIONS = 2;
export const MAX_OPTIONS = 8;

/** What is wrong with a question */
export type QuestionProblem =
	'label_missing' | 'label_too_long' | 'option_count' | 'option_missing' | 'option_too_long' | 'option_twice';

/** Characters as people count them, not UTF-16 units */
export const length = (s: string): number => [...s].length;

/** A new question of a kind, with nothing typed yet */
export function newQuestion(kind: QuestionKind = 'rating'): FeedbackQuestion {
	return {
		id: '',
		kind,
		label: '',
		required: false,
		long: false,
		options: kind === 'choice' ? ['', ''] : []
	};
}

/**
 * The two questions a form switched on without any starts with, in the
 * console's language: a required rating and an optional long text.
 */
export function defaultQuestions(rating: string, text: string): FeedbackQuestion[] {
	return [
		{ id: '', kind: 'rating', label: rating, required: true, long: false, options: [] },
		{ id: '', kind: 'text', label: text, required: false, long: true, options: [] }
	];
}

/** The question as another kind: label and required stay, the rest fits the kind */
export function withKind(q: FeedbackQuestion, kind: QuestionKind): FeedbackQuestion {
	if (kind === q.kind) return q;
	const options = kind === 'choice' ? (q.options.length ? q.options : ['', '']) : [];
	return { ...q, kind, long: kind === 'text' ? q.long : false, options };
}

/** What is wrong with a question, '' when nothing */
export function questionProblem(q: FeedbackQuestion): QuestionProblem | '' {
	if (!q.label.trim()) return 'label_missing';
	if (length(q.label) > MAX_LABEL) return 'label_too_long';
	if (q.kind !== 'choice') return '';
	if (q.options.length < MIN_OPTIONS || q.options.length > MAX_OPTIONS) return 'option_count';
	if (q.options.some((o) => !o.trim())) return 'option_missing';
	if (q.options.some((o) => length(o) > MAX_LABEL)) return 'option_too_long';
	if (new Set(q.options).size !== q.options.length) return 'option_twice';
	return '';
}

/** The question as it is saved: options of a choice without empty rows, nothing the kind does not use */
export function cleanQuestion(q: FeedbackQuestion): FeedbackQuestion {
	return {
		id: q.id,
		kind: q.kind,
		label: q.label,
		required: q.required,
		long: q.kind === 'text' && q.long,
		options: q.kind === 'choice' ? q.options.filter((o) => o.trim() !== '') : []
	};
}

export const canAdd = (list: readonly FeedbackQuestion[]): boolean => list.length < MAX_QUESTIONS;

/** The list with q at the end; the list as it is when it is full */
export function addQuestion(list: readonly FeedbackQuestion[], q: FeedbackQuestion): FeedbackQuestion[] {
	return canAdd(list) ? [...list, q] : [...list];
}

/** The list with question i put in place of the old one */
export function replaceQuestion(
	list: readonly FeedbackQuestion[],
	i: number,
	q: FeedbackQuestion
): FeedbackQuestion[] {
	if (i < 0 || i >= list.length) return [...list];
	return list.map((old, j) => (j === i ? q : old));
}

/** The list with question i moved by delta places (-1 up, 1 down); the list as it is at an end */
export function moveQuestion(
	list: readonly FeedbackQuestion[],
	i: number,
	delta: number
): FeedbackQuestion[] {
	const to = i + delta;
	if (i < 0 || i >= list.length || to < 0 || to >= list.length) return [...list];
	const out = [...list];
	const [q] = out.splice(i, 1);
	if (q) out.splice(to, 0, q);
	return out;
}

export function removeQuestion(list: readonly FeedbackQuestion[], i: number): FeedbackQuestion[] {
	return list.filter((_, j) => j !== i);
}

/** The first question with a problem: its place and the problem; null when all are fine */
export function firstProblem(
	list: readonly FeedbackQuestion[]
): { index: number; problem: QuestionProblem } | null {
	for (const [index, q] of list.entries()) {
		const problem = questionProblem(q);
		if (problem) return { index, problem };
	}
	return null;
}

/** The form switched on: with the default questions when it has none */
export function switchedOn(
	list: readonly FeedbackQuestion[],
	defaults: () => FeedbackQuestion[]
): FeedbackQuestion[] {
	return list.length ? [...list] : defaults();
}
