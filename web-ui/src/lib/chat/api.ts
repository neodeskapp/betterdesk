/**
 * The chat's HTTP routes (A242): the support inbox and its actions, the
 * team's conversations and people, canned replies, settings and the trail
 * of a message. Support routes need chat.support, team routes chat.team,
 * the rest either. Messages travel on the socket (client.svelte.ts).
 */

import { request } from '#lib/api/client.ts';
import type {
	AccountSettings,
	CannedReply,
	Conversation,
	Device,
	InboxCounts,
	InboxFilter,
	InboxItem,
	Person,
	ServerSettings,
	Summary,
	TeamItem
} from './types.ts';

type Raw = Record<string, unknown>;

function obj(v: unknown): Raw {
	return v && typeof v === 'object' && !Array.isArray(v) ? (v as Raw) : {};
}

function list<T>(v: unknown): T[] {
	return Array.isArray(v) ? (v as T[]) : [];
}

export async function loadInbox(
	filter: InboxFilter,
	q = '',
	signal?: AbortSignal
): Promise<{ conversations: InboxItem[]; counts: InboxCounts }> {
	const data = obj(await request('/api/chat/inbox', { query: { filter, q: q || undefined }, signal }));
	const counts = obj(data.counts);
	return {
		conversations: list<InboxItem>(data.conversations),
		counts: {
			open: Number(counts.open) || 0,
			mine: Number(counts.mine) || 0,
			closed: Number(counts.closed) || 0,
			unassigned: Number(counts.unassigned) || 0
		}
	};
}

export async function findDevices(q: string, signal?: AbortSignal): Promise<Device[]> {
	return list<Device>(obj(await request('/api/chat/devices', { query: { q }, signal })).devices);
}

export interface ConversationDetail {
	conversation: Conversation;
	device?: Device;
	people?: Person[];
	/** Who a support conversation can be handed to */
	assignable?: Person[];
}

export async function loadConversation(id: string): Promise<ConversationDetail> {
	const data = obj(await request(`/api/chat/conversations/${encodeURIComponent(id)}`));
	return {
		conversation: data.conversation as Conversation,
		device: data.device as Device | undefined,
		people: data.people as Person[] | undefined,
		assignable: data.assignable as Person[] | undefined
	};
}

export async function assign(id: string, accountId: number): Promise<Conversation> {
	const data = obj(
		await request(`/api/chat/conversations/${encodeURIComponent(id)}/assign`, {
			method: 'POST',
			body: { account_id: accountId }
		})
	);
	return data.conversation as Conversation;
}

export async function closeConversation(
	id: string,
	note: string,
	resolveHelpRequest: boolean
): Promise<{ conversation: Conversation; resolved: number }> {
	const data = obj(
		await request(`/api/chat/conversations/${encodeURIComponent(id)}/close`, {
			method: 'POST',
			body: { note, resolve_help_request: resolveHelpRequest }
		})
	);
	return {
		conversation: data.conversation as Conversation,
		resolved: Number(data.help_requests_resolved) || 0
	};
}

export async function reopenConversation(id: string): Promise<Conversation> {
	const data = obj(
		await request(`/api/chat/conversations/${encodeURIComponent(id)}/reopen`, { method: 'POST', body: {} })
	);
	return data.conversation as Conversation;
}

export async function linkConversation(
	id: string,
	link: { ticket_id?: number; help_request_id?: number }
): Promise<Conversation> {
	const data = obj(
		await request(`/api/chat/conversations/${encodeURIComponent(id)}/link`, { method: 'POST', body: link })
	);
	return data.conversation as Conversation;
}

export async function loadSummary(): Promise<Summary> {
	return obj(await request('/api/chat/summary')) as Summary;
}

export async function loadTeam(): Promise<{
	conversations: TeamItem[];
	people: Person[];
	old: TeamItem[];
}> {
	const data = obj(await request('/api/chat/team'));
	return {
		conversations: list<TeamItem>(data.conversations),
		people: list<Person>(data.people),
		old: list<TeamItem>(data.old)
	};
}

export async function openDirect(accountId: number): Promise<Conversation> {
	const data = obj(
		await request('/api/chat/team/direct', { method: 'POST', body: { account_id: accountId } })
	);
	return data.conversation as Conversation;
}

export async function newGroup(title: string, members: number[]): Promise<Conversation> {
	const data = obj(await request('/api/chat/team/groups', { method: 'POST', body: { title, members } }));
	return data.conversation as Conversation;
}

export async function editGroup(id: string, title: string, members?: number[]): Promise<Conversation> {
	const data = obj(
		await request(`/api/chat/team/groups/${encodeURIComponent(id)}`, {
			method: 'PATCH',
			body: { title, members }
		})
	);
	return data.conversation as Conversation;
}

export async function loadCanned(): Promise<{ replies: CannedReply[]; mayManageShared: boolean }> {
	const data = obj(await request('/api/chat/canned'));
	return { replies: list<CannedReply>(data.replies), mayManageShared: data.may_manage_shared === true };
}

export async function addCanned(title: string, body: string, shared: boolean): Promise<CannedReply> {
	return obj(await request('/api/chat/canned', { method: 'POST', body: { title, body, shared } }))
		.reply as CannedReply;
}

export async function updateCanned(id: number, title: string, body: string): Promise<CannedReply> {
	return obj(await request(`/api/chat/canned/${id}`, { method: 'PATCH', body: { title, body } }))
		.reply as CannedReply;
}

export async function deleteCanned(id: number): Promise<void> {
	await request(`/api/chat/canned/${id}`, { method: 'DELETE' });
}

export async function loadMySettings(): Promise<AccountSettings> {
	const s = obj(obj(await request('/api/chat/me')).settings);
	return { browser: s.browser !== false, sound: s.sound === true, mail: s.mail !== false };
}

export async function saveMySettings(settings: AccountSettings): Promise<void> {
	await request('/api/chat/me', { method: 'PUT', body: settings });
}

export async function loadServerSettings(): Promise<ServerSettings> {
	const s = obj(obj(await request('/api/chat/settings')).settings);
	return { mail_after_minutes: Number(s.mail_after_minutes) || 0, mail_preview: s.mail_preview !== false };
}

export async function saveServerSettings(settings: ServerSettings): Promise<void> {
	await request('/api/chat/settings', { method: 'PUT', body: settings });
}

export interface TrailStep {
	action: 'edit' | 'delete';
	actor: { kind: string; id: string; name: string };
	old_text: string;
	at: number;
}

export async function loadTrail(messageId: string): Promise<TrailStep[]> {
	return list<TrailStep>(
		obj(await request(`/api/chat/messages/${encodeURIComponent(messageId)}/trail`)).trail
	);
}

/** The largest file the server takes */
export const MAX_FILE = 50 * 1024 * 1024;

export interface Uploaded {
	file_id: string;
}

/** Upload a file for a message; the message names it by its id */
export async function uploadFile(file: File, signal?: AbortSignal): Promise<Uploaded> {
	const body = new FormData();
	body.append('file', file, file.name);
	const data = obj(await request('/api/chat/upload', { method: 'POST', body, signal }));
	return { file_id: String(data.file_id ?? '') };
}
