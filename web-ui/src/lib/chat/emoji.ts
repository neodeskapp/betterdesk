/**
 * The chat's emoji (A242): the quick reactions on a message and the
 * picker's set, each drawn from Twemoji (ChatEmoji.svelte,
 * static/img/emoji/<code points>.svg). The wire and the message text carry
 * the character; an emoji someone types that is not in the set stays
 * text.
 */

/** The reactions a message offers in one click */
export const QUICK = ['👍', '❤️', '😂', '😮', '🎉', '✅'] as const;

/** The picker, in groups */
export const EMOJI_GROUPS: { id: 'faces' | 'hands' | 'symbols' | 'things'; emoji: readonly string[] }[] = [
	{
		id: 'faces',
		emoji: ['😀', '😄', '😂', '😅', '🙂', '😉', '😊', '😍', '😎', '🙃', '🤔', '😮', '😬', '😢', '😴', '🤯']
	},
	{ id: 'hands', emoji: ['👍', '👎', '👏', '🙏', '👋', '👌', '💪', '🙌'] },
	{ id: 'symbols', emoji: ['❤️', '🔥', '🎉', '✅', '❌', '⚠️', '❓', '💯', '⭐', '⚡'] },
	{ id: 'things', emoji: ['🚀', '🐛', '🔧', '📎', '🔒', '💻', '🖥️', '🖨️', '📞', '📧', '☕', '⏳'] }
];

/** Every emoji the console draws */
export const ALL_EMOJI: ReadonlySet<string> = new Set([...QUICK, ...EMOJI_GROUPS.flatMap((g) => g.emoji)]);

/** The Twemoji file of an emoji of the set: its code points in hex, without the emoji variation selector */
export function emojiFile(emoji: string): string | null {
	if (!ALL_EMOJI.has(emoji)) return null;
	const points = Array.from(emoji)
		.map((c) => c.codePointAt(0) ?? 0)
		.filter((p) => p !== 0xfe0f)
		.map((p) => p.toString(16));
	return `${points.join('-')}.svg`;
}
