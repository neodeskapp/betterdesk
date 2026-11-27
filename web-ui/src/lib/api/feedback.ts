/**
 * Feedback after quick support: the form the quick support app shows the
 * person once the session ended (server.config sets it up) and the answers
 * (device.connect, the quick support permission, reads them). The app's
 * side is in neodesk-server/console/routes/onboard/PROTOCOL.md.
 */

import * as v from 'valibot';
import { api } from './client.ts';
import type { FeedbackQuestion, QuestionKind } from '#lib/support/feedback-form.ts';

const Text = v.optional(
	v.pipe(
		v.nullable(v.string()),
		v.transform((s) => s ?? '')
	),
	''
);

const Kind = v.picklist(['rating', 'text', 'choice', 'yesno'] satisfies QuestionKind[]);

const Question = v.object({
	id: v.string(),
	kind: Kind,
	label: v.string(),
	required: v.optional(v.boolean(), false),
	long: v.optional(v.boolean(), false),
	options: v.optional(v.array(v.string()), [])
});

const FormAnswer = v.object({
	form: v.object({ enabled: v.boolean(), questions: v.array(Question) })
});

export interface FeedbackFormValue {
	enabled: boolean;
	questions: FeedbackQuestion[];
}

export async function getFeedbackForm(signal?: AbortSignal): Promise<FeedbackFormValue> {
	return (await api(FormAnswer, '/api/support/feedback/form', { signal })).form;
}

/** Saves the whole form; the answer is the form as stored, with the ids of new questions */
export async function saveFeedbackForm(form: FeedbackFormValue): Promise<FeedbackFormValue> {
	const body = {
		enabled: form.enabled,
		questions: form.questions.map((q) => ({
			...(q.id ? { id: q.id } : {}),
			kind: q.kind,
			label: q.label,
			required: q.required,
			...(q.kind === 'text' ? { long: q.long } : {}),
			...(q.kind === 'choice' ? { options: q.options } : {})
		}))
	};
	return (await api(FormAnswer, '/api/support/feedback/form', { method: 'PUT', body })).form;
}

const AnswerValue = v.union([v.string(), v.number(), v.boolean(), v.null()]);

const GivenAnswer = v.object({
	id: Text,
	kind: Text,
	label: Text,
	value: v.optional(AnswerValue, null)
});
export type GivenAnswer = v.InferOutput<typeof GivenAnswer>;

const Item = v.object({
	session_id: v.string(),
	answered_at: Text,
	started_at: Text,
	ended_at: Text,
	device_id: Text,
	hostname: Text,
	helper_id: v.optional(v.number(), 0),
	helper: Text,
	name: Text,
	email: Text,
	stars: v.optional(v.nullable(v.number()), null),
	answers: v.optional(v.array(GivenAnswer), [])
});
export type FeedbackItem = v.InferOutput<typeof Item>;

const List = v.object({
	feedback: v.array(Item),
	count: v.number(),
	average: v.optional(v.nullable(v.number()), null),
	helpers: v.optional(v.array(v.object({ id: v.number(), name: Text })), [])
});
export type FeedbackList = v.InferOutput<typeof List>;

/** The answers newest first; `helper` limits them, the count and the average to one helper */
export async function listFeedback(helper?: number, signal?: AbortSignal): Promise<FeedbackList> {
	return api(List, '/api/support/feedback', {
		signal,
		query: helper ? { helper: String(helper) } : undefined
	});
}

/** "Offer to install after quick support": GET/PUT /api/support/receiver-offer (server.config) */
const ReceiverOffer = v.object({ enabled: v.boolean() });

export async function getReceiverOffer(signal?: AbortSignal): Promise<boolean> {
	return (await api(ReceiverOffer, '/api/support/receiver-offer', { signal })).enabled;
}

export async function saveReceiverOffer(enabled: boolean): Promise<boolean> {
	return (await api(ReceiverOffer, '/api/support/receiver-offer', { method: 'PUT', body: { enabled } }))
		.enabled;
}
