/**
 * The feedback form after quick support without a DOM
 * (lib/support/feedback-form.ts, feedback-list.ts): adding, moving,
 * removing and changing questions, the limits the server checks too, the
 * default questions, and which answers show under the stars.
 */
import { describe, expect, test } from 'vitest';
import {
	MAX_QUESTIONS,
	addQuestion,
	canAdd,
	cleanQuestion,
	defaultQuestions,
	firstProblem,
	moveQuestion,
	newQuestion,
	questionProblem,
	removeQuestion,
	replaceQuestion,
	switchedOn,
	withKind,
	type FeedbackQuestion
} from '#lib/support/feedback-form.ts';
import { feedbackRest } from '#lib/support/feedback-list.ts';

const q = (label: string, more: Partial<FeedbackQuestion> = {}): FeedbackQuestion => ({
	...newQuestion('yesno'),
	label,
	...more
});
const labels = (list: readonly FeedbackQuestion[]) => list.map((x) => x.label);

describe('the questions of the form', () => {
	const list = [q('A', { id: 'q1' }), q('B', { id: 'q2' }), q('C', { id: 'q3' })];

	test('a question moves by one place and keeps its id; at an end nothing moves', () => {
		const up = moveQuestion(list, 2, -1);
		expect(labels(up)).toEqual(['A', 'C', 'B']);
		expect(up.map((x) => x.id)).toEqual(['q1', 'q3', 'q2']);
		expect(labels(moveQuestion(list, 0, 1))).toEqual(['B', 'A', 'C']);
		expect(labels(moveQuestion(list, 0, -1))).toEqual(['A', 'B', 'C']);
		expect(labels(moveQuestion(list, 2, 1))).toEqual(['A', 'B', 'C']);
		expect(labels(list)).toEqual(['A', 'B', 'C']);
	});

	test('remove and replace touch only their question', () => {
		expect(labels(removeQuestion(list, 1))).toEqual(['A', 'C']);
		const changed = replaceQuestion(list, 1, q('B2', { id: 'q2', required: true }));
		expect(labels(changed)).toEqual(['A', 'B2', 'C']);
		expect(changed[1]).toMatchObject({ id: 'q2', required: true });
		expect(labels(replaceQuestion(list, 7, q('X')))).toEqual(['A', 'B', 'C']);
	});

	test('the form takes at most ten questions', () => {
		let full: FeedbackQuestion[] = [];
		for (let i = 0; i < MAX_QUESTIONS + 2; i++) full = addQuestion(full, q(`Q${i}`));
		expect(full).toHaveLength(MAX_QUESTIONS);
		expect(canAdd(full)).toBe(false);
		expect(canAdd(full.slice(1))).toBe(true);
	});

	test('switched on without questions, the form starts with a required rating and an optional long text', () => {
		const defaults = () => defaultQuestions('How was the help?', 'Anything else?');
		const started = switchedOn([], defaults);
		expect(started).toEqual([
			{ id: '', kind: 'rating', label: 'How was the help?', required: true, long: false, options: [] },
			{ id: '', kind: 'text', label: 'Anything else?', required: false, long: true, options: [] }
		]);
		// Questions it has stay as they are
		expect(labels(switchedOn(list, defaults))).toEqual(['A', 'B', 'C']);
	});
});

describe('one question', () => {
	test('the label is needed and has at most 200 characters, counted as people count them', () => {
		expect(questionProblem(q('   '))).toBe('label_missing');
		expect(questionProblem(q('é'.repeat(200)))).toBe('');
		expect(questionProblem(q('😀'.repeat(200)))).toBe('');
		expect(questionProblem(q('é'.repeat(201)))).toBe('label_too_long');
	});

	test('a choice has 2 to 8 options, each filled in and there once', () => {
		const choice = (options: string[]) => q('Pick', { kind: 'choice', options });
		expect(questionProblem(choice(['A']))).toBe('option_count');
		expect(questionProblem(choice(['A', 'B']))).toBe('');
		expect(questionProblem(choice(['1', '2', '3', '4', '5', '6', '7', '8', '9']))).toBe('option_count');
		expect(questionProblem(choice(['A', ' ']))).toBe('option_missing');
		expect(questionProblem(choice(['A', 'A']))).toBe('option_twice');
		expect(questionProblem(choice(['A', 'x'.repeat(201)]))).toBe('option_too_long');
		expect(firstProblem([q('fine'), choice(['A'])])).toEqual({ index: 1, problem: 'option_count' });
		expect(firstProblem([q('fine')])).toBeNull();
	});

	test('changing the kind keeps the label and required and drops what the kind does not use', () => {
		const text = q('Words', { kind: 'text', long: true, required: true });
		const choice = withKind(text, 'choice');
		expect(choice).toMatchObject({
			kind: 'choice',
			label: 'Words',
			required: true,
			long: false,
			options: ['', '']
		});
		const back = withKind({ ...choice, options: ['A', 'B'] }, 'rating');
		expect(back).toMatchObject({ kind: 'rating', options: [], long: false });
		expect(withKind(text, 'text')).toBe(text);
	});

	test('a question is saved without empty options and without the long field of another kind', () => {
		expect(cleanQuestion(q('Pick', { kind: 'choice', options: ['A', '', 'B'], long: true }))).toEqual({
			id: '',
			kind: 'choice',
			label: 'Pick',
			required: false,
			long: false,
			options: ['A', 'B']
		});
		// The label stays as typed
		expect(cleanQuestion(q('  Spaced  ', { options: ['stray'] }))).toMatchObject({
			label: '  Spaced  ',
			options: []
		});
	});
});

describe('an answer on the Feedback page', () => {
	const answers = [
		{ id: 'q1', kind: 'text', label: 'First words', value: 'hi' },
		{ id: 'q2', kind: 'rating', label: 'Stars', value: 4 },
		{ id: 'q3', kind: 'rating', label: 'More stars', value: 2 },
		{ id: 'q4', kind: 'yesno', label: 'Solved?', value: false }
	];

	test('the first rating goes on top as stars; the other answers are listed, a false no included', () => {
		expect(feedbackRest({ stars: 4, answers }).map((a) => a.id)).toEqual(['q1', 'q3', 'q4']);
		expect(
			feedbackRest({ stars: null, answers: answers.filter((a) => a.kind !== 'rating') }).map((a) => a.id)
		).toEqual(['q1', 'q4']);
	});
});
