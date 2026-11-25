/**
 * The chat inside a remote session and the device's support conversation
 * are one conversation (A242). A session's chat rides RustDesk's own
 * ChatMessage, which every client knows, stock RustDesk included; the
 * server cannot read it (the session is end to end). So the viewer bridges
 * the two in the browser:
 *
 *   const bridge = sessionChat(deviceId, (text) => client.sendChat(text));
 *   on('chat', (text) => bridge?.received(text));   // the person wrote
 *   bridge?.send(text);                              // the viewer's own chat field
 *   bridge?.dispose();                               // the session ended
 *
 * - What the person writes in the session is stored in the device's
 *   conversation, from the device (frame session_line).
 * - What this account writes to the device, in the chat page, the panel or
 *   the session's own sheet, goes into the session too, as plain text.
 *   Replies, edits and reactions don't reach the session; a file goes in
 *   as "File: <name>" without a link (A242 f).
 *
 * Without Support chat there is no bridge (null): the session's chat stays
 * as RustDesk has it, in the session only.
 */

import { can } from '#lib/state/session.svelte.ts';
import { chat } from './client.svelte.ts';

export interface SessionBridge {
	/** A line the person at the device wrote in the session */
	received(text: string): void;
	/** Sends the viewer's own line through the conversation; false: send it directly */
	send(text: string): boolean;
	dispose(): void;
}

export function sessionChat(deviceId: string, sendInSession: (text: string) => void): SessionBridge | null {
	if (!can('chat.support')) return null;
	// A viewer in its own window has no shell that started the chat
	if (chat.status === 'idle') chat.start();
	const off = chat.addSessionSink(deviceId, (text) => {
		if (text) sendInSession(text);
	});
	return {
		received(text: string) {
			const line = text.trim();
			if (!line) return;
			chat.send({ type: 'session_line', conversation_id: deviceId, text: line });
		},
		send(text: string) {
			if (chat.status !== 'open' || !chat.may.support) return false;
			return chat.sendMessage(deviceId, text);
		},
		dispose() {
			off();
		}
	};
}
