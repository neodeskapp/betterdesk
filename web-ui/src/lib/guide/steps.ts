/**
 * What each step of the setup guide says and where it leads (no DOM,
 * unit tested). A step that is done on a page leads there with ?guide=<id>,
 * so the page can offer the way back (GuideReturn); Add a computer opens
 * on the overview itself, and HTTPS is set up outside the console, so it
 * leads to the docs.
 */

import { STEP_IDS, type StepId } from '#lib/api/setupGuide.ts';

/** Where HTTPS in front of the console is explained */
export const HTTPS_DOCS = 'https://neode.sk/docs/operations/reverse-proxy-tls';

export type StepTarget =
	{ kind: 'page'; href: string } | { kind: 'docs'; href: string } | { kind: 'add-computer' };

/** A page in the console with the step named in its query, before any #id */
function page(path: string, step: StepId, hash = ''): StepTarget {
	const [base, query = ''] = path.split('?');
	const q = new URLSearchParams(query);
	q.set('guide', step);
	return { kind: 'page', href: `${base}?${q.toString()}${hash ? `#${hash}` : ''}` };
}

export const STEP_TARGETS: Readonly<Record<StepId, StepTarget>> = {
	password: page('/settings?tab=auth', 'password', 'account-password'),
	two_factor: page('/settings?tab=auth', 'two_factor', 'auth-panel-totp'),
	address: page('/settings', 'address', 'public-endpoints'),
	https: { kind: 'docs', href: HTTPS_DOCS },
	computer: { kind: 'add-computer' },
	session: page('/devices', 'session'),
	people: page('/users', 'people'),
	backup: page('/server/backups', 'backup', 'backup-create')
};

export const stepTitleKey = (id: StepId) => `setup_guide.steps.${id}.title`;
export const stepHintKey = (id: StepId) => `setup_guide.steps.${id}.hint`;

/** The step a page was opened for (?guide=<id>), or null */
export function guideStep(search: { get(name: string): string | null }): StepId | null {
	const id = search.get('guide');
	return id && (STEP_IDS as readonly string[]).includes(id) ? (id as StepId) : null;
}
