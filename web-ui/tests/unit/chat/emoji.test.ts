/**
 * The chat's emoji are drawn from Twemoji (static/img/emoji), so reactions
 * and the picker look the same on every system: every emoji of the set has
 * its file, unchanged from @twemoji/svg, the picture carries the character
 * as its text, and a character outside the set stays text.
 */
import fs from 'node:fs';
import path from 'node:path';
import { render } from '@testing-library/svelte';
import { describe, expect, test } from 'vitest';
import ChatEmoji from '../../../src/lib/chat/ChatEmoji.svelte';
import { ALL_EMOJI, emojiFile, QUICK } from '../../../src/lib/chat/emoji.ts';

const root = path.resolve(__dirname, '../../..');

describe('chat emoji', () => {
	test('every emoji of the set maps to a Twemoji file that ships', () => {
		expect(ALL_EMOJI.size).toBeGreaterThan(40);
		for (const e of ALL_EMOJI) {
			const file = emojiFile(e);
			expect(file, e).toMatch(/^[0-9a-f-]+\.svg$/);
			expect(fs.existsSync(path.join(root, 'static/img/emoji', file ?? '')), `${e} ${file}`).toBe(true);
		}
		for (const e of QUICK) expect(ALL_EMOJI.has(e)).toBe(true);
	});

	test('the file is the code points without the emoji variation selector', () => {
		expect(emojiFile('❤️')).toBe('2764.svg');
		expect(emojiFile('🖥️')).toBe('1f5a5.svg');
		expect(emojiFile('👍')).toBe('1f44d.svg');
	});

	test('an emoji of the set is a picture with the character as its text', () => {
		const { container } = render(ChatEmoji, { emoji: '👍' });
		const img = container.querySelector('img');
		expect(img?.getAttribute('alt')).toBe('👍');
		expect(img?.getAttribute('src')).toMatch(/img\/emoji\/1f44d\.svg$/);
	});

	test('a character outside the set stays text', () => {
		const { container } = render(ChatEmoji, { emoji: '🦄' });
		expect(container.querySelector('img')).toBeNull();
		expect(container.textContent).toBe('🦄');
	});
});
