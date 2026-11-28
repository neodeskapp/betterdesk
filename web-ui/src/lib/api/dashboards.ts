/**
 * The overview's dashboards (console/routes/dashboards): the account's own
 * layout, and the shared ones people with server.config publish. Reading
 * is forgiving: a board or widget the console can't read is left out
 * (lib/dashboard/model.ts), never the whole answer.
 */

import * as v from 'valibot';
import {
	ownBody,
	readBoard,
	readShared,
	sharedBody,
	type Board,
	type OwnBoards,
	type SharedBoard
} from '#lib/dashboard/model.ts';
import { api, Ok, wrapped } from './client.ts';

const Strings = v.pipe(
	v.optional(v.nullable(v.array(v.unknown())), null),
	v.transform((a) => (a ?? []).filter((x): x is string => typeof x === 'string'))
);

const Own = v.pipe(
	v.looseObject({
		boards: v.optional(v.nullable(v.array(v.unknown())), null),
		active: v.optional(v.nullable(v.string()), ''),
		hidden: Strings
	}),
	v.transform((o): OwnBoards => ({
		boards: (o.boards ?? []).map(readBoard).filter((b): b is Board => !!b),
		active: o.active ?? '',
		hidden: o.hidden
	}))
);

const SharedList = v.pipe(
	v.optional(v.nullable(v.array(v.unknown())), null),
	v.transform((a) => (a ?? []).map(readShared).filter((b): b is SharedBoard => !!b))
);

const Dashboards = wrapped(
	v.looseObject({
		own: v.optional(v.nullable(Own), null),
		shared: SharedList,
		canPublish: v.optional(v.nullable(v.boolean()), false)
	})
);

export interface DashboardsAnswer {
	own: OwnBoards | null;
	shared: SharedBoard[];
	canPublish: boolean;
}

export async function getDashboards(signal?: AbortSignal): Promise<DashboardsAnswer> {
	const out = await api(Dashboards, '/api/dashboards', { signal });
	return { own: out.own ?? null, shared: out.shared, canPublish: out.canPublish === true };
}

export function saveOwnDashboards(own: OwnBoards): Promise<OwnBoards> {
	return api(wrapped(Own), '/api/dashboards', { method: 'PUT', body: ownBody(own) });
}

/** Back to the built-in dashboard and every shared one shown */
export async function resetOwnDashboards(): Promise<void> {
	await api(Ok, '/api/dashboards', { method: 'DELETE' });
}

export async function publishSharedDashboards(boards: SharedBoard[]): Promise<SharedBoard[]> {
	const out = await api(wrapped(v.looseObject({ boards: SharedList })), '/api/dashboards/shared', {
		method: 'PUT',
		body: sharedBody(boards)
	});
	return out.boards;
}
