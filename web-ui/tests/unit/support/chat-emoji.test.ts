/**
 * The chat's emoji are drawn from Twemoji (static/img/emoji), so reactions
 * and the picker look the same on every system: every emoji of the set and
 * of the quick reactions has its file, the image carries the character as
 * its text, and a character outside the set stays text.
 */
import fs from 'node:fs';
import path from 'node:path';
import { render } from '@testing-library/svelte';
import { describe, expect, test } from 'vitest';
import ChatEmoji, { emojiFile } from '../../../src/lib/support/ChatEmoji.svelte';
import { EMOJI_SET, QUICK_REACTIONS } from '../../../src/lib/support/chat.ts';

const root = path.resolve(__dirname, '../../..');

describe('chat emoji', () => {
	test('every emoji of the set and of the quick reactions maps to a Twemoji file that ships', () => {
		for (const e of [...EMOJI_SET, ...QUICK_REACTIONS]) {
			const file = emojiFile(e);
			expect(file, e).toMatch(/^[0-9a-f-]+\.svg$/);
			expect(fs.existsSync(path.join(root, 'static/img/emoji', file ?? '')), `${e} ${file}`).toBe(true);
		}
	});

	test('the file is Twemoji code points without the emoji variation selector', () => {
		expect(emojiFile('❤️')).toBe('2764.svg');
		expect(emojiFile('👍')).toBe('1f44d.svg');
		expect(emojiFile('⚡')).toBe('26a1.svg');
	});

	test('an emoji of the set is an image with the character as its text', () => {
		const { container } = render(ChatEmoji, { emoji: '👍' });
		const img = container.querySelector('img');
		expect(img?.getAttribute('alt')).toBe('👍');
		expect(img?.getAttribute('src')).toMatch(/img\/emoji\/1f44d\.svg$/);
	});

	test('a character outside the set stays text', () => {
		const { container } = render(ChatEmoji, { emoji: '🙂' });
		expect(container.querySelector('img')).toBeNull();
		expect(container.textContent).toBe('🙂');
		expect(emojiFile('🙂')).toBeNull();
	});
});
