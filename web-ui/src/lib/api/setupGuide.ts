/**
 * The first-install guide on the overview (server.config). The server
 * decides who sees it and ticks each step from what it sees; the console
 * only shows it, hides it and brings it back.
 */

import * as v from 'valibot';
import { api, wrapped } from './client.ts';

export const STEP_IDS = [
	'password',
	'two_factor',
	'address',
	'https',
	'computer',
	'session',
	'people',
	'backup'
] as const;
export type StepId = (typeof STEP_IDS)[number];

const Step = v.object({ id: v.string(), done: v.boolean() });

export const GuideStatus = v.object({
	visible: v.boolean(),
	// Steps this console does not know (a newer server) are left out
	steps: v.pipe(
		v.array(Step),
		v.transform((steps) =>
			steps.filter((s): s is { id: StepId; done: boolean } => (STEP_IDS as readonly string[]).includes(s.id))
		)
	)
});
export type GuideStatus = v.InferOutput<typeof GuideStatus>;

export function getSetupGuide(signal?: AbortSignal): Promise<GuideStatus> {
	return api(wrapped(GuideStatus), '/api/setup-guide', { signal });
}

/** Opens (true) or hides (false) the guide for the signed-in account */
export function setSetupGuide(open: boolean): Promise<GuideStatus> {
	return api(wrapped(GuideStatus), open ? '/api/setup-guide/show' : '/api/setup-guide/hide', {
		method: 'POST',
		body: {}
	});
}
