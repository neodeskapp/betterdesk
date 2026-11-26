/**
 * The chat of version 2 (A242) as the server sends it: messages, people,
 * computers and conversations. The socket is /ws/chat-operator/panel?v=2,
 * the HTTP routes are under /api/chat (lib/chat/api.ts);
 * neodesk-server/console/routes/chat/PROTOCOL.md describes both.
 */

export type PartyKind = 'device' | 'user' | 'system';

/** Who wrote, read or reacted */
export interface Party {
	kind: PartyKind;
	/** A device id, or an account id as text */
	id: string;
	name: string;
}

export interface Attachment {
	id: string;
	name: string;
	type: string;
	size: number;
	width?: number;
	height?: number;
	url: string;
}

export interface ReplyQuote {
	id: string;
	from_name: string;
	preview: string;
	deleted?: boolean;
}

/** What a line of the system says, for the console to word */
export interface SystemEvent {
	event: 'assigned' | 'taken' | 'unassigned' | 'closed' | 'reopened' | string;
	by?: string;
	to?: string;
	note?: string;
}

export interface Message {
	id: string;
	conversation_id: string;
	from: Party;
	text: string;
	reply_to: ReplyQuote | null;
	attachments: Attachment[];
	/** ms */
	created_at: number;
	edited_at: number | null;
	deleted: boolean;
	reactions: Record<string, Party[]>;
	client_id?: string;
	event?: SystemEvent;
	/** Not from the server: still on its way (sending) or refused (failed) */
	pending?: 'sending' | 'failed';
}

/**
 * group: colleagues one of them picked; team: set up by people with Manage
 * teams, with people and user groups; old: the chat of version 1 that
 * belongs to no two accounts, for admins to read
 */
export type ConversationKind = 'support' | 'dm' | 'group' | 'team' | 'old';

export interface Conversation {
	id: string;
	kind: ConversationKind;
	state: 'open' | 'closed';
	title?: string;
	assignee_id?: number;
	assignee_name?: string;
	last_message_id: string;
	last_message_at: number;
	waiting_since?: number;
	closed_at?: number;
	closed_by?: string;
	close_note?: string;
	help_request_id?: number;
	ticket_id?: number;
}

/** A computer as the chat shows it */
export interface Device {
	id: string;
	name: string;
	hostname: string;
	owner: string;
	folder: string;
	platform: string;
	online: boolean;
	/** The NeoDesk app's chat window is connected */
	chat_connected: boolean;
	/** An agent that takes chat (CDAP) is connected */
	agent_chat: boolean;
}

/** A colleague */
export interface Person {
	id: number;
	name: string;
	username: string;
	role: string;
	status: 'online' | 'away' | 'busy' | 'offline';
}

export interface LastMessage {
	text: string;
	from_kind: PartyKind;
	from_name: string;
	at: number;
	deleted?: boolean;
	files?: number;
	/** A line of the system, as JSON */
	event?: string;
}

export interface InboxItem extends Conversation {
	device: Device;
	last_message: LastMessage | null;
	unread: number;
}

/** A conversation a search found messages in */
export interface FoundConversation {
	id: string;
	kind: ConversationKind;
	name: string;
}

export interface TeamItem extends Conversation {
	people: Person[];
	last_message: LastMessage | null;
	unread: number;
}

/** A team as the people with Manage teams see it */
export interface TeamSetup {
	id: string;
	title: string;
	/** Picked by hand */
	people: number[];
	/** Guids of the user groups whose members are in it */
	groups: string[];
}

/** A user group a team can include, with the accounts in it */
export interface UserGroupOption {
	guid: string;
	name: string;
	members: number[];
}

export interface InboxCounts {
	open: number;
	mine: number;
	closed: number;
	unassigned: number;
}

export interface CannedReply {
	id: number;
	owner_id: number;
	title: string;
	body: string;
	updated_at: number;
}

export interface AccountSettings {
	browser: boolean;
	sound: boolean;
	mail: boolean;
}

export interface ServerSettings {
	mail_after_minutes: number;
	mail_preview: boolean;
	/** Computers may open the chat ("Chat with computers", A259) */
	computers: boolean;
}

export interface Summary {
	support?: { unread_conversations: number; waiting: number; unread: Record<string, number> };
	team?: { unread: number; by_conversation: Record<string, number> };
}

export interface ReadMark {
	reader: { kind: PartyKind; id: string };
	up_to: string;
}

export type Section = 'support' | 'team';
export type InboxFilter = 'open' | 'mine' | 'closed';
